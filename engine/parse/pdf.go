package parse

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	rePDFSplit  = regexp.MustCompile(`\bT[dD]\b|\bT\*\b`)
	rePDFToken  = regexp.MustCompile(`\(((?:\\.|[^\\()])*)\)|<([0-9A-Fa-f\s]+)>`)
	rePDFBFRang = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	rePDFBFChar = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	rePDFTitle  = regexp.MustCompile(`/Title\s*\(((?:\\.|[^\\()])*)\)`)
)

// PDF 文本层抽取（与 legado-web pdf.js 同一策略）：
// 解 Flate 流 → 取 Tj/TJ/Td → ToUnicode 映射 → 乱码判定 → Ghostscript 兜底。
func parsePDF(file, fallbackName string) (*Book, error) {
	buf, err := os.ReadFile(file)
	if err != nil {
		return nil, failf("io", "读取文件失败：%v", err)
	}
	if len(buf) < 5 || string(buf[:5]) != "%PDF-" {
		return nil, failf("unsupported", "不是 PDF 文件")
	}

	streams := extractPDFStreams(buf)
	toUni := buildToUnicode(streams)

	var lines []string
	for _, s := range streams {
		if !bytes.Contains(s, []byte("BT")) || !bytes.Contains(s, []byte("Tj")) && !bytes.Contains(s, []byte("TJ")) {
			continue
		}
		lines = append(lines, extractPageText(s, toUni)...)
	}
	text := strings.Join(lines, "\n")

	// 乱码时用 Ghostscript 重新抽取（路由器上通常没装，装了则显著提升）
	if looksGarbled(text) {
		if gs, lookErr := exec.LookPath("gs"); lookErr == nil {
			if gsText, gsErr := gsExtract(gs, file, 180*time.Second); gsErr == nil && gsText != "" && !looksGarbled(gsText) {
				text = gsText
				lines = lines[:0]
				for _, ln := range strings.FieldsFunc(gsText, func(r rune) bool { return r == '\n' || r == '\r' || r == '\f' }) {
					if t := Clean(ln); t != "" {
						lines = append(lines, t)
					}
				}
			}
		}
	}

	if len([]rune(Clean(text))) < 20 {
		return nil, failf("no-text-layer", "PDF 没有文本层（可能是扫描件），请先 OCR 或转 EPUB")
	}
	if looksGarbled(text) {
		return nil, failf("font-encoding", "PDF 使用嵌入字体且缺少可解映射，文本层无法正确解码（可安装 Ghostscript 或先转 EPUB）")
	}

	paragraphs := make([]Block, 0, len(lines))
	for _, line := range lines {
		for _, piece := range splitLong(line, 4096) {
			paragraphs = append(paragraphs, Block{K: "p", T: piece})
		}
	}
	if len(paragraphs) < 3 {
		return nil, failf("no-text-layer", "PDF 文本层抽取失败")
	}

	chapters := make([]Chapter, 0, len(paragraphs)/40+1)
	chunk := make([]Block, 0, 64)
	chars := 0
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		chapters = append(chapters, Chapter{
			Title:      "第 " + strconv.Itoa(len(chapters)+1) + " 节",
			Level:      1,
			Paragraphs: chunk,
		})
		chunk = make([]Block, 0, 64)
		chars = 0
	}
	for _, p := range paragraphs {
		chunk = append(chunk, p)
		chars += len([]rune(p.T))
		if chars >= 5000 {
			flush()
		}
	}
	flush()

	title := fallbackName
	if m := rePDFTitle.FindSubmatch(buf); len(m) > 1 {
		if t := Clean(unescapePDFLiteral(string(m[1]))); t != "" {
			title = t
		}
	}
	return &Book{
		Format:   "pdf",
		Meta:     Meta{Title: title},
		Chapters: chapters,
		Chars:    countChars(chapters),
	}, nil
}

func inflateMaybe(data []byte, dict string) []byte {
	if !strings.Contains(dict, "FlateDecode") {
		return data
	}
	if r, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
		defer r.Close()
		out, err := io.ReadAll(io.LimitReader(r, 128<<20))
		if err == nil {
			return out
		}
	}
	if r := flate.NewReader(bytes.NewReader(data)); r != nil {
		defer r.Close()
		out, err := io.ReadAll(io.LimitReader(r, 128<<20))
		if err == nil {
			return out
		}
	}
	return nil
}

func extractPDFStreams(buf []byte) [][]byte {
	out := make([][]byte, 0, 64)
	pos := 0
	for pos < len(buf) {
		i := bytes.Index(buf[pos:], []byte("stream"))
		if i < 0 {
			break
		}
		at := pos + i
		start := at + 6
		if start < len(buf) && buf[start] == '\r' {
			start++
		}
		if start < len(buf) && buf[start] == '\n' {
			start++
		}
		end := bytes.Index(buf[start:], []byte("endstream"))
		if end < 0 {
			break
		}
		endAt := start + end
		dictStart := at - 800
		if dictStart < 0 {
			dictStart = 0
		}
		dict := string(buf[dictStart:at])
		if data := inflateMaybe(buf[start:endAt], dict); len(data) > 0 {
			out = append(out, data)
		}
		pos = endAt + 9
	}
	return out
}

func buildToUnicode(streams [][]byte) map[uint32]string {
	m := make(map[uint32]string, 4096)
	for _, s := range streams {
		if !bytes.Contains(s, []byte("beginbfchar")) && !bytes.Contains(s, []byte("beginbfrange")) {
			continue
		}
		str := string(s)
		// 先展开 bfrange（区间），再用 bfchar 的精确映射覆盖
		for _, mm := range rePDFBFRang.FindAllStringSubmatch(str, -1) {
			from := hexU32(mm[1])
			to := hexU32(mm[2])
			dst := hexU32(mm[3])
			if to < from || to-from > 65535 {
				continue
			}
			for c := from; c <= to && c-from < 4096; c++ {
				m[c] = string(rune(dst + (c - from)))
			}
		}
		for _, mm := range rePDFBFChar.FindAllStringSubmatch(str, -1) {
			m[hexU32(mm[1])] = unicodeFromHex(mm[2])
		}
	}
	return m
}

func hexU32(s string) uint32 {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 16, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}

func unicodeFromHex(hex string) string {
	var sb strings.Builder
	for i := 0; i+4 <= len(hex); i += 4 {
		v, err := strconv.ParseUint(hex[i:i+4], 16, 16)
		if err == nil {
			sb.WriteRune(rune(v))
		}
	}
	return sb.String()
}

func unescapePDFLiteral(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			sb.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			sb.WriteByte('\n')
		case 'r':
			sb.WriteByte('\r')
		case 't':
			sb.WriteByte('\t')
		case 'b':
			sb.WriteByte('\b')
		case 'f':
			sb.WriteByte('\f')
		case '(', ')', '\\':
			sb.WriteByte(s[i])
		default:
			if s[i] >= '0' && s[i] <= '7' {
				num := 0
				digits := 0
				for digits < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
					num = num*8 + int(s[i]-'0')
					i++
					digits++
				}
				i--
				sb.WriteRune(rune(num))
			} else {
				sb.WriteByte(s[i])
			}
		}
	}
	return sb.String()
}

func decodeHexString(hex string, m map[uint32]string) string {
	clean := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, hex)
	bs := make([]byte, 0, len(clean)/2)
	for i := 0; i+1 < len(clean); i += 2 {
		v, err := strconv.ParseUint(clean[i:i+2], 16, 8)
		if err != nil {
			continue
		}
		bs = append(bs, byte(v))
	}
	if len(m) == 0 {
		if len(bs) >= 2 && bs[0] == 0xfe && bs[1] == 0xff {
			var sb strings.Builder
			for i := 2; i+1 < len(bs); i += 2 {
				sb.WriteRune(rune(uint16(bs[i])<<8 | uint16(bs[i+1])))
			}
			return sb.String()
		}
		return latin1(bs)
	}
	var sb strings.Builder
	for i := 0; i+1 < len(bs); i += 2 {
		code := uint32(bs[i])<<8 | uint32(bs[i+1])
		if v, ok := m[code]; ok {
			sb.WriteString(v)
		} else if code >= 0x20 && code < 0xfffe {
			sb.WriteRune(rune(code))
		}
	}
	return sb.String()
}

func extractPageText(content []byte, m map[uint32]string) []string {
	parts := rePDFSplit.Split(string(content), -1)
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		var text strings.Builder
		for _, tok := range rePDFToken.FindAllStringSubmatch(part, -1) {
			if tok[1] != "" {
				text.WriteString(unescapePDFLiteral(tok[1]))
			} else if tok[2] != "" {
				text.WriteString(decodeHexString(tok[2], m))
			}
		}
		if t := Clean(text.String()); t != "" {
			lines = append(lines, t)
		}
	}
	return lines
}

// looksGarbled：中文 PDF 用 CID 嵌入字体时简易解码会得到大量 Latin-1 噪声。
func looksGarbled(text string) bool {
	r := []rune(text)
	if len(r) < 200 {
		return false
	}
	cjk, high, bad := 0, 0, 0
	for _, c := range r {
		switch {
		case (c >= 0x4e00 && c <= 0x9fff) || (c >= 0x3000 && c <= 0x303f) || (c >= 0xff00 && c <= 0xffef):
			cjk++
		case c >= 0x80 && c <= 0xff:
			high++
		case c < 0x20 && c != '\n' && c != '\r' && c != '\t':
			bad++
		}
	}
	if float64(bad)/float64(len(r)) > 0.05 {
		return true
	}
	return float64(high)/float64(len(r)) > 0.25 && float64(cjk)/float64(len(r)) < 0.03
}

func gsExtract(gs, file string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, gs, "-q", "-dNOPAUSE", "-dBATCH", "-sDEVICE=txtwrite", "-sOutputFile=-", file)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
