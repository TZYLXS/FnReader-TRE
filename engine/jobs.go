package main

import (
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"fnparse/parse"
)

type ebookPayload struct {
	Schema       string `json:"schema"`
	Sha1         string `json:"sha1"`
	Name         string `json:"name"`
	Format       string `json:"format"`
	Size         int64  `json:"size"`
	FileURL      string `json:"fileUrl"`
	Token        string `json:"token"`
	PartMaxBytes int    `json:"partMaxBytes"`
	Index        int    `json:"index"`
}

func decodePayload(raw json.RawMessage, out interface{}) error {
	if len(raw) == 0 {
		return errors.New("任务缺少 payload")
	}
	return json.Unmarshal(raw, out)
}

func validSHA1(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func fileSHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (e *Engine) tokenFor(p ebookPayload) string {
	if strings.TrimSpace(p.Token) != "" {
		return p.Token
	}
	return e.cfg.Token
}

// fetchBook 下载原文件并做 sha1 校验；已下载且大小一致则直接复用。
func (e *Engine) fetchBook(ctx context.Context, p ebookPayload) (string, error) {
	if !validSHA1(p.Sha1) {
		return "", fmt.Errorf("任务里的 sha1 非法")
	}
	format := strings.ToLower(p.Format)
	dest := e.cache.DownloadPath(p.Sha1, format)
	if st, err := os.Stat(dest); err == nil {
		if p.Size <= 0 || st.Size() == p.Size {
			return dest, nil
		}
		e.log.Warnf("缓存文件大小不符（%d/%d），重新下载 %s", st.Size(), p.Size, p.Sha1)
		_ = os.Remove(dest)
	}
	fileURL := p.FileURL
	if fileURL == "" {
		fileURL = "/api/m/ebook/file/" + p.Sha1
	}
	// 任务令牌只用于本次下载：复制一份客户端，避免并发任务互相改 token
	cl := *e.api
	if tok := e.tokenFor(p); tok != "" {
		cl.token = tok
	}

	e.log.Infof("下载原文件 %s（%s，%d KB）", p.Name, format, p.Size>>10)
	if err := cl.Download(ctx, fileURL, dest, p.Size); err != nil {
		_ = os.Remove(dest)
		return "", err
	}
	if sum, err := fileSHA1(dest); err == nil && !strings.EqualFold(sum, p.Sha1) {
		_ = os.Remove(dest)
		e.log.Warnf("sha1 不一致（期望 %s，实际 %s），重下一次", p.Sha1, sum)
		if err := cl.Download(ctx, fileURL, dest, p.Size); err != nil {
			return "", err
		}
		if sum2, err2 := fileSHA1(dest); err2 == nil && !strings.EqualFold(sum2, p.Sha1) {
			_ = os.Remove(dest)
			return "", &parse.Error{Code: "corrupt", Msg: "原文件 sha1 校验失败"}
		}
	}
	return dest, nil
}

func (e *Engine) loadParsed(sha1 string) (*parse.Book, bool) {
	b, err := e.cache.LoadParsed(sha1)
	if err != nil || b == nil || len(b.Chapters) == 0 {
		return nil, false
	}
	return b, true
}

// parseBook 解析并缓存结果；缓存命中直接返回。
func (e *Engine) parseBook(ctx context.Context, p ebookPayload, path string) (*parse.Book, error) {
	if b, ok := e.loadParsed(p.Sha1); ok {
		e.log.Infof("命中解析缓存 %s（%d 章）", p.Name, len(b.Chapters))
		return b, nil
	}
	start := time.Now()
	book, err := parse.ParseFile(path, p.Format, p.Name)
	if err != nil {
		return nil, err
	}
	if err := e.cache.SaveParsed(p.Sha1, book); err != nil {
		e.log.Warnf("写解析缓存失败：%v", err)
	}
	e.log.Infof("解析完成 %s：%d 章 %d 字，用时 %d ms", p.Name, len(book.Chapters), book.Chars, time.Since(start).Milliseconds())
	return book, nil
}

func (e *Engine) runEbookParse(ctx context.Context, job busJob, raw json.RawMessage) (interface{}, error) {
	var p ebookPayload
	if err := decodePayload(raw, &p); err != nil {
		return nil, &parse.Error{Code: "corrupt", Msg: "payload 解析失败：" + err.Error()}
	}
	if p.Schema != parse.SCHEMA {
		return nil, &parse.Error{Code: "schema", Msg: "schema 不匹配：" + p.Schema}
	}
	path, err := e.fetchBook(ctx, p)
	if err != nil {
		return nil, err
	}
	book, err := e.parseBook(ctx, p, path)
	if err != nil {
		return nil, err
	}
	partMax := e.cfg.partBytes()
	if p.PartMaxBytes > 0 && p.PartMaxBytes < partMax {
		partMax = p.PartMaxBytes
	}
	parts, total, err := buildParts(book, p.Sha1, strings.ToLower(p.Format), p.Name, partMax)
	if err != nil {
		return nil, &parse.Error{Code: "too-large", Msg: err.Error()}
	}

	// 断点续传：服务端已收的分片跳过（重试/重派时省流量）
	received := map[int]bool{}
	if st, err := e.api.PartsReceived(ctx, p.Sha1); err == nil {
		for _, seq := range st.Parts {
			received[seq] = true
		}
	}

	for seq, body := range parts {
		if received[seq] {
			e.log.Debugf("%s 分片 %d/%d 已存在，跳过", p.Name, seq+1, total)
			continue
		}
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(attempt) * 2 * time.Second):
				}
			}
			start := time.Now()
			_, lastErr = e.api.UploadPart(ctx, p.Sha1, seq, total, body)
			if lastErr == nil {
				e.log.Infof("上传分片 %s %d/%d（%d KB，%d ms）", p.Name, seq+1, total, len(body)>>10, time.Since(start).Milliseconds())
				break
			}
			if seq == 0 && strings.Contains(lastErr.Error(), "toc-locked") {
				e.log.Warnf("%s part0 已存在，按已完成处理", p.Name)
				lastErr = nil
				break
			}
			e.log.Warnf("分片 %d/%d 上传失败（%d/3）：%v", seq+1, total, attempt+1, lastErr)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		if lastErr != nil {
			return nil, fmt.Errorf("上传分片失败：%w", lastErr)
		}
	}
	return map[string]interface{}{
		"chapters": len(book.Chapters),
		"parts":    total,
		"chars":    book.Chars,
	}, nil
}

func (e *Engine) runEbookChapter(ctx context.Context, job busJob, raw json.RawMessage) (interface{}, error) {
	var p ebookPayload
	if err := decodePayload(raw, &p); err != nil {
		return nil, &parse.Error{Code: "corrupt", Msg: "payload 解析失败：" + err.Error()}
	}
	if !validSHA1(p.Sha1) {
		return nil, &parse.Error{Code: "corrupt", Msg: "任务里的 sha1 非法"}
	}
	book, ok := e.loadParsed(p.Sha1)
	if !ok {
		path, err := e.fetchBook(ctx, p)
		if err != nil {
			return nil, err
		}
		book, err = e.parseBook(ctx, p, path)
		if err != nil {
			return nil, err
		}
	}
	if p.Index < 0 || p.Index >= len(book.Chapters) {
		return nil, &parse.Error{Code: "index", Msg: fmt.Sprintf("章节号越界：%d / %d", p.Index, len(book.Chapters))}
	}
	ch := book.Chapters[p.Index]
	title := ch.Title
	if title == "" {
		title = fmt.Sprintf("第 %d 章", p.Index+1)
	}
	return map[string]interface{}{
		"index":      p.Index,
		"title":      title,
		"paragraphs": ch.Paragraphs,
	}, nil
}

// SaveParsed / LoadParsed：缓存解析产物，供 ebook-chapter 秒回。
func (c *Cache) SaveParsed(sha1 string, book *parse.Book) error {
	if c.Root() == "" {
		return errors.New("缓存目录未就绪")
	}
	data, err := jsonNoEscape(book)
	if err != nil {
		return err
	}
	path := c.ParsedPath(sha1)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	if _, err := zw.Write(data); err != nil {
		zw.Close()
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func (c *Cache) LoadParsed(sha1 string) (*parse.Book, error) {
	f, err := os.Open(c.ParsedPath(sha1))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var book parse.Book
	if err := json.NewDecoder(io.LimitReader(zr, 512<<20)).Decode(&book); err != nil {
		return nil, err
	}
	return &book, nil
}

func jobErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var pe *parse.Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "aborted"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "下载"), strings.Contains(msg, "404"):
		return "download"
	case strings.Contains(msg, "上传"):
		return "upload"
	case strings.Contains(msg, "sha1"):
		return "corrupt"
	case strings.Contains(msg, "json"):
		return "corrupt"
	}
	return "io"
}
