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
// 这三类只依赖书籍详情页，不需要书源规则，适合由 OpenWRT 低功耗节点承接。
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

	start := time.Now()
	page, finalURL, err := fetchBookPage(ctx, bookURL)
	if err != nil {
		return nil, &parse.Error{Code: "fetch", Msg: err.Error()}
	}
	meta := parse.ExtractWebMeta(page, finalURL)
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
