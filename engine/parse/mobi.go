package parse

import (
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MOBI6 / PRC：PalmDB 记录表 + PalmDOC 压缩 + EXTH 元数据。
// KF8（AZW3）按协议返回 kf8-unsupported，不硬解。
func parseMobi(file, fallbackName string) (*Book, error) {
	buf, err := os.ReadFile(file)
	if err != nil {
		return nil, failf("io", "读取文件失败：%v", err)
	}
	if len(buf) < 100 {
		return nil, failf("corrupt", "文件太小")
	}
	if string(buf[60:64]) != "BOOK" || string(buf[64:68]) != "MOBI" {
		return nil, failf("unsupported", "不是 MOBI 文件（type=%s, creator=%s）", string(buf[60:64]), string(buf[64:68]))
	}
	numRecords := int(u16(buf, 76))
	if numRecords <= 0 || 78+numRecords*8 > len(buf) {
		return nil, failf("corrupt", "记录表为空或越界")
	}
	offsets := make([]int, numRecords)
	for i := 0; i < numRecords; i++ {
		offsets[i] = int(u32(buf, 78+i*8))
	}
	rec := func(i int) []byte {
		if i < 0 || i >= len(offsets) {
			return nil
		}
		start := offsets[i]
		end := len(buf)
		if i+1 < len(offsets) {
			end = offsets[i+1]
		}
		if start < 0 || start > len(buf) || end < start {
			return nil
		}
		if end > len(buf) {
			end = len(buf)
		}
		return buf[start:end]
	}

	record0 := rec(0)
	if len(record0) < 16 {
		return nil, failf("corrupt", "PalmDOC 头缺失")
	}
	compression := int(u16(record0, 0))
	textLength := int(u32(record0, 4))
	recordCount := int(u16(record0, 8))
	if enc := u16(record0, 12); enc != 0 {
		return nil, failf("drm", "这本书带 DRM 加密")
	}

	encoding := 1252
	version := 6
	headerLen := 0
	fullName := ""
	exthAuthor, exthTitle, exthLang := "", "", ""
	if len(record0) >= 20 && string(record0[16:20]) == "MOBI" {
		headerLen = int(u32(record0, 20))
		encoding = int(u32(record0, 28))
		version = int(u32(record0, 36))
		nameOff := int(u32(record0, 16+0x54))
		nameLen := int(u32(record0, 16+0x58))
		if nameOff >= 0 && nameLen > 0 && nameOff+nameLen <= len(record0) {
			fullName = decodeTextBytes(record0[nameOff:nameOff+nameLen], encoding)
		}
		exthAuthor, exthTitle, exthLang = readEXTH(record0, headerLen, encoding)
	}
	if version >= 8 {
		return nil, failf("kf8-unsupported", "这是 KF8（AZW3）结构，暂不支持，请转 EPUB")
	}
	if compression != 1 && compression != 2 {
		return nil, failf("unsupported", "不支持的 MOBI 压缩方式：%d", compression)
	}

	maxRec := recordCount
	if maxRec > len(offsets)-1 {
		maxRec = len(offsets) - 1
	}
	if maxRec <= 0 {
		return nil, failf("corrupt", "没有正文记录")
	}
	out := &palmBuf{}
	for i := 1; i <= maxRec; i++ {
		data := rec(i)
		if data == nil {
			continue
		}
		if compression == 1 {
			out.writeAll(data)
		} else {
			decompressPalmDOC(data, out)
		}
		if textLength > 0 && len(out.data) >= textLength {
			break
		}
	}
	textBytes := out.data
	if textLength > 0 && len(textBytes) > textLength {
		textBytes = textBytes[:textLength]
	}
	html := decodeTextBytes(textBytes, encoding)
	if strings.TrimSpace(html) == "" {
		return nil, failf("corrupt", "正文解压为空")
	}

	chapters := splitMobiChapters(html)
	if len(chapters) == 0 {
		return nil, failf("corrupt", "MOBI 正文为空")
	}
	title := fullName
	if title == "" {
		title = exthTitle
	}
	if title == "" {
		title = fallbackName
	}
	meta := Meta{
		Title:    Clean(title),
		Author:   Clean(exthAuthor),
		Language: Clean(exthLang),
	}
	return &Book{Format: "mobi", Meta: meta, Chapters: chapters, Chars: countChars(chapters)}, nil
}

func decodeTextBytes(b []byte, encoding int) string {
	switch encoding {
	case 65001:
		return string(b)
	case 1252:
		// 不少自制 MOBI 标着 1252，正文其实是 UTF-8；合法 UTF-8 优先按 UTF-8 解
		if utf8.Valid(b) {
			return string(b)
		}
		return decodeCP1252(b)
	case 936, 54936:
		if s, ok := decodeGBK(b); ok {
			return s
		}
		return latin1(b)
	case 65000, 65002: // UTF-7 / UTF-16 极少见
		return decodeAuto(b)
	default:
		return latin1(b)
	}
}

// palmBuf 支持跨记录反向引用的输出缓冲
type palmBuf struct {
	data []byte
}

func (p *palmBuf) writeAll(b []byte) {
	p.data = append(p.data, b...)
}

func (p *palmBuf) writeByte(c byte) {
	p.data = append(p.data, c)
}

func (p *palmBuf) copyBack(distance, length int) {
	if distance <= 0 {
		return
	}
	src := len(p.data) - distance
	if src < 0 {
		src = 0
	}
	for i := 0; i < length; i++ {
		if src >= len(p.data) {
			return
		}
		p.data = append(p.data, p.data[src])
		src++
	}
}

func decompressPalmDOC(rec []byte, out *palmBuf) {
	i := 2 // 前 2 字节是记录内偏移/标志
	for i < len(rec) {
		b := rec[i]
		i++
		switch {
		case b == 0:
			if i < len(rec) {
				out.writeByte(rec[i])
				i++
			}
		case b <= 8:
			n := int(b)
			for k := 0; k < n; k++ {
				if i < len(rec) {
					out.writeByte(rec[i])
					i++
				}
			}
		case b <= 0x7f:
			out.writeByte(b)
		case b <= 0xbf:
			var next byte
			if i < len(rec) {
				next = rec[i]
				i++
			}
			length := int(b&0x07) + 3
			distance := int(b&0x78)<<5 | int(next)
			out.copyBack(distance, length)
		default:
			out.writeByte(0x20)
			out.writeByte(b ^ 0x80)
		}
	}
}

func readEXTH(record0 []byte, mobiHeaderLen, encoding int) (author, title, language string) {
	from := 16 + mobiHeaderLen
	if from < 0 || from >= len(record0) {
		return
	}
	at := -1
	limit := from + 1024
	if limit > len(record0)-4 {
		limit = len(record0) - 4
	}
	for i := from; i <= limit; i++ {
		if string(record0[i:i+4]) == "EXTH" {
			at = i
			break
		}
	}
	if at < 0 || at+12 > len(record0) {
		return
	}
	count := int(u32(record0, at+8))
	p := at + 12
	for n := 0; n < count; n++ {
		if p+8 > len(record0) {
			return
		}
		typ := int(u32(record0, p))
		l := int(u32(record0, p+4))
		if l < 8 || p+l > len(record0) {
			return
		}
		data := record0[p+8 : p+l]
		switch typ {
		case 100:
			author = decodeTextBytes(data, encoding)
		case 503:
			title = decodeTextBytes(data, encoding)
		case 524:
			language = decodeTextBytes(data, encoding)
		}
		p += l
	}
	return
}

var (
	reMobiHeading = regexp.MustCompile(`(?i)<h([1-3])\b[^>]*>([\s\S]*?)</h[1-3]>`)
	rePageBreak   = regexp.MustCompile(`(?i)<mbp:pagebreak\b[^>]*/?>|</?mbp:pagebreak\b[^>]*>`)
	reAnyTag      = regexp.MustCompile(`<[^>]*>`)
)

func splitMobiChapters(html string) []Chapter {
	marks := reMobiHeading.FindAllStringSubmatchIndex(html, -1)
	if len(marks) >= 2 {
		chapters := make([]Chapter, 0, len(marks))
		firstStart := marks[0][0]
		if firstStart > 0 {
			preface := html[:firstStart]
			if len([]rune(Clean(stripTags(preface)))) > 200 {
				ps := htmlToBlocks(preface, "前言")
				if len(ps) > 0 {
					chapters = append(chapters, Chapter{Title: "前言", Level: 1, Paragraphs: ps})
				}
			}
		}
		for i, m := range marks {
			end := len(html)
			if i+1 < len(marks) {
				end = marks[i+1][0]
			}
			level := 1
			if m[2] >= 0 && m[3] > m[2] {
				if lv := int(html[m[2]] - '0'); lv >= 1 && lv <= 3 {
					level = lv
				}
			}
			rawTitle := ""
			if m[4] >= 0 {
				rawTitle = html[m[4]:m[5]]
			}
			title := Clean(stripTags(rawTitle))
			if title == "" {
				title = chTitleFallback(len(chapters) + 1)
			}
			bodyStart := m[1]
			if bodyStart < 0 || bodyStart > len(html) {
				bodyStart = 0
			}
			body := html[bodyStart:end]
			ps := htmlToBlocks(body, title)
			if len(ps) == 0 {
				continue
			}
			chapters = append(chapters, Chapter{Title: title, Level: level, Paragraphs: ps})
		}
		if len(chapters) > 0 {
			return chapters
		}
	}

	parts := rePageBreak.Split(html, -1)
	nonEmpty := 0
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			nonEmpty++
		}
	}
	if nonEmpty > 5 {
		chapters := make([]Chapter, 0, 8)
		var sb strings.Builder
		chars := 0
		for _, part := range parts {
			sb.WriteString(part)
			chars += len([]rune(Clean(stripTags(part))))
			if chars > 200000 {
				n := len(chapters) + 1
				if ps := htmlToBlocks(sb.String(), ""); len(ps) > 0 {
					chapters = append(chapters, Chapter{Title: chTitleFallback(n), Level: 1, Paragraphs: ps})
				}
				sb.Reset()
				chars = 0
			}
		}
		if strings.TrimSpace(sb.String()) != "" {
			n := len(chapters) + 1
			if ps := htmlToBlocks(sb.String(), ""); len(ps) > 0 {
				chapters = append(chapters, Chapter{Title: chTitleFallback(n), Level: 1, Paragraphs: ps})
			}
		}
		if len(chapters) > 1 {
			return chapters
		}
	}
	return []Chapter{{Title: "", Level: 1, Paragraphs: htmlToBlocks(html, "")}}
}

func u16(b []byte, at int) uint16 {
	if at < 0 || at+2 > len(b) {
		return 0
	}
	return uint16(b[at])<<8 | uint16(b[at+1])
}

func u32(b []byte, at int) uint32 {
	if at < 0 || at+4 > len(b) {
		return 0
	}
	return uint32(b[at])<<24 | uint32(b[at+1])<<16 | uint32(b[at+2])<<8 | uint32(b[at+3])
}
