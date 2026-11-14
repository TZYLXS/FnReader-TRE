package parse

import (
	"os"
	"regexp"
	"strings"
)

var reTxtChapter = regexp.MustCompile(`(?i)^(?:第[0-9一二三四五六七八九十百千零两]{1,8}[章节回卷部篇]|Chapter\s+\d+|[0-9]{1,4}[、.．]\s*\S)`)

func parseTxt(file, fallbackName string) (*Book, error) {
	buf, err := os.ReadFile(file)
	if err != nil {
		return nil, failf("io", "读取文件失败：%v", err)
	}
	text := decodeAuto(buf)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if strings.TrimSpace(text) == "" {
		return nil, failf("corrupt", "文本为空")
	}
	lines := strings.Split(text, "\n")

	chapters := make([]Chapter, 0, 128)
	var title string
	body := make([]string, 0, 256)
	flush := func() {
		ps := make([]Block, 0, len(body))
		for _, line := range body {
			t := Clean(line)
			if t == "" {
				continue
			}
			for _, piece := range splitLong(t, 4096) {
				ps = append(ps, Block{K: "p", T: piece})
			}
		}
		if len(ps) > 0 {
			if title == "" {
				title = "第 " + itoa(len(chapters)+1) + " 章"
			}
			chapters = append(chapters, Chapter{Title: title, Level: 1, Paragraphs: ps})
		}
		title = ""
		body = body[:0]
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t != "" && len([]rune(t)) <= 60 && reTxtChapter.MatchString(t) {
			flush()
			title = t
			continue
		}
		body = append(body, line)
	}
	flush()

	if len(chapters) <= 1 {
		// 没有章节结构：按 3000 字左右切块
		var chunks []string
		var sb strings.Builder
		for _, line := range lines {
			sb.WriteString(line)
			sb.WriteByte('\n')
			if len([]rune(sb.String())) >= 3000 {
				chunks = append(chunks, sb.String())
				sb.Reset()
			}
		}
		if strings.TrimSpace(sb.String()) != "" {
			chunks = append(chunks, sb.String())
		}
		chapters = chapters[:0]
		for i, chunk := range chunks {
			ps := make([]Block, 0, 64)
			for _, line := range strings.Split(chunk, "\n") {
				t := Clean(line)
				if t == "" {
					continue
				}
				for _, piece := range splitLong(t, 4096) {
					ps = append(ps, Block{K: "p", T: piece})
				}
			}
			if len(ps) > 0 {
				chapters = append(chapters, Chapter{
					Title:      "第 " + itoa(i+1) + " 节",
					Level:      1,
					Paragraphs: ps,
				})
			}
		}
	}
	if len(chapters) == 0 {
		return nil, failf("corrupt", "解析不出任何章节")
	}
	return &Book{
		Format:   "txt",
		Meta:     Meta{Title: Clean(fallbackName)},
		Chapters: chapters,
		Chars:    countChars(chapters),
	}, nil
}
