package parse

import (
	"strings"
	"unicode"
)

// Clean 对应服务端 html.js 的 clean()：去 NUL、全角空格转半角、压缩空白。
func Clean(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\u3000", " ")
	if strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		s = strings.Join(strings.Fields(s), " ")
	}
	return strings.TrimSpace(s)
}

// splitLong 超长段落按句号/问号/叹号/分号切开（与服务端 splitLong 一致）。
func splitLong(text string, max int) []string {
	if max <= 0 {
		max = 4096
	}
	r := []rune(text)
	if len(r) <= max {
		if text == "" {
			return nil
		}
		return []string{text}
	}
	const punct = "。！？!?.；;"
	out := make([]string, 0, len(r)/max+1)
	rest := r
	for len(rest) > max {
		cut := -1
		for i := max; i > max/2; i-- {
			if strings.ContainsRune(punct, rest[i]) {
				cut = i + 1
				break
			}
		}
		if cut < 0 {
			cut = max
		}
		if seg := strings.TrimSpace(string(rest[:cut])); seg != "" {
			out = append(out, seg)
		}
		rest = []rune(strings.TrimSpace(string(rest[cut:])))
	}
	if seg := strings.TrimSpace(string(rest)); seg != "" {
		out = append(out, seg)
	}
	return out
}

// stripTags 去掉标签并解码实体，用于 OPF/NCX 里 dc:title 这类字段。
func stripTags(s string) string {
	s = decodeEntities(s)
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteByte(s[i])
			}
		}
	}
	return Clean(b.String())
}
