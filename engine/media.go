package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"fnparse/parse"
)

// 软总线 2.6.17+ 的 media 类任务：cover / intro / bookinfo。
// 解析顺序：payload.source 对应的书源 ruleBookInfo 规则优先（书源解析），
// 规则取不到时回退通用网页解析（og/meta、JSON-LD、简介区块），保证任务有兜底。
type mediaPayload struct {
	BookURL string `json:"bookUrl"`
	Source  string `json:"source"`
	Name    string `json:"name"`
	Author  string `json:"author"`
}

var mediaHTTP = &http.Client{
	Timeout: 25 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("重定向次数过多")
		}
		return nil
	},
}

func (e *Engine) runMediaTask(ctx context.Context, typ string, raw json.RawMessage) (interface{}, error) {
	var p mediaPayload
	if err := decodePayload(raw, &p); err != nil {
		return nil, &parse.Error{Code: "corrupt", Msg: "payload 解析失败：" + err.Error()}
	}
	bookURL := strings.TrimSpace(p.BookURL)
	if bookURL == "" {
		return nil, &parse.Error{Code: "corrupt", Msg: "任务缺少 bookUrl"}
	}
	u, err := url.Parse(bookURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, &parse.Error{Code: "corrupt", Msg: "bookUrl 必须是 http/https 地址"}
	}

	vars := parse.RuleVars{Name: p.Name, Author: p.Author, BookURL: bookURL}
	rule := e.bookSourceRule(ctx, p.Source)
	pageURL := bookURL
	if rule != nil {
		if initURL := parse.ResolveInitURL(rule.Init, vars); initURL != "" {
			pageURL = initURL
			e.log.Infof("书源规则 init 指定详情页：%s", pageURL)
		}
	}

	start := time.Now()
	page, finalURL, err := fetchBookPage(ctx, pageURL)
	if err != nil {
		return nil, &parse.Error{Code: "fetch", Msg: err.Error()}
	}

	var meta parse.WebMeta
	if rule != nil {
		if res, used := parse.ApplyBookInfoRule(page, finalURL, rule, vars); used {
			meta = webMetaFromRule(res)
			e.log.Infof("书源规则解析 %s：name=%q cover=%v intro=%d 字（%s）",
				bookURL, meta.Title, meta.CoverURL != "", len([]rune(meta.Intro)), p.Source)
		} else {
			e.log.Infof("书源规则未命中 %s（%s），回退通用解析", bookURL, p.Source)
		}
	}
	// 通用网页解析兜底：只补规则没拿到的空字段
	web := parse.ExtractWebMeta(page, finalURL)
	fillWebMeta(&meta, web)
	e.log.Infof("详情页解析 %s：cover=%v intro=%d 字 用时 %d ms",
		bookURL, meta.CoverURL != "", len([]rune(meta.Intro)), time.Since(start).Milliseconds())

	switch typ {
	case "cover":
		if meta.CoverURL == "" {
			return nil, &parse.Error{Code: "not-found", Msg: "详情页没有可用封面"}
		}
		return map[string]interface{}{"coverUrl": meta.CoverURL}, nil
	case "intro":
		if meta.Intro == "" && meta.Author == "" {
			return nil, &parse.Error{Code: "not-found", Msg: "详情页没有简介"}
		}
		out := map[string]interface{}{}
		if meta.Intro != "" {
			out["intro"] = meta.Intro
		}
		addMetaFields(out, meta)
		return out, nil
	case "bookinfo":
		if meta.Intro == "" && meta.CoverURL == "" && meta.Author == "" {
			return nil, &parse.Error{Code: "not-found", Msg: "详情页没有可提取的书籍信息"}
		}
		out := map[string]interface{}{}
		if meta.Intro != "" {
			out["intro"] = meta.Intro
		}
		if meta.CoverURL != "" {
			out["coverUrl"] = meta.CoverURL
		}
		addMetaFields(out, meta)
		return out, nil
	default:
		return nil, &parseErrorUnsupported{typ}
	}
}

// webMetaFromRule 把书源规则结果转成统一元信息结构。
func webMetaFromRule(r parse.RuleResult) parse.WebMeta {
	return parse.WebMeta{
		Title:       r.Name,
		Author:      r.Author,
		Intro:       r.Intro,
		CoverURL:    r.CoverURL,
		Kind:        r.Kind,
		WordCount:   r.WordCount,
		LastChapter: r.LastChapter,
	}
}

// fillWebMeta 用通用解析结果补空字段，不覆盖书源规则已拿到的值。
func fillWebMeta(dst *parse.WebMeta, src parse.WebMeta) {
	if dst.Title == "" {
		dst.Title = src.Title
	}
	if dst.Author == "" {
		dst.Author = src.Author
	}
	if dst.Intro == "" {
		dst.Intro = src.Intro
	}
	if dst.CoverURL == "" {
		dst.CoverURL = src.CoverURL
	}
	if dst.Kind == "" {
		dst.Kind = src.Kind
	}
	if dst.WordCount == "" {
		dst.WordCount = src.WordCount
	}
	if dst.LastChapter == "" {
		dst.LastChapter = src.LastChapter
	}
	if dst.TotalChapterNum == 0 {
		dst.TotalChapterNum = src.TotalChapterNum
	}
}

func addMetaFields(out map[string]interface{}, meta parse.WebMeta) {
	if meta.Author != "" {
		out["author"] = meta.Author
	}
	if meta.Kind != "" {
		out["kind"] = meta.Kind
	}
	if meta.WordCount != "" {
		out["wordCount"] = meta.WordCount
	}
	if meta.LastChapter != "" {
		out["lastChapter"] = meta.LastChapter
	}
	if meta.TotalChapterNum > 0 {
		out["totalChapterNum"] = meta.TotalChapterNum
	}
}

func fetchBookPage(ctx context.Context, rawURL string) (string, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.6")

	resp, err := mediaHTTP.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return "", nil, fmt.Errorf("详情页返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", nil, err
	}
	if len(body) == 0 {
		return "", nil, fmt.Errorf("详情页为空")
	}
	return parse.DecodeHTML(body), resp.Request.URL, nil
}
