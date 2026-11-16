package parse

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// cp1252 0x80-0x9F 区段的特殊字符（其余字节按 latin-1 直出）。
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

func decodeCP1252(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		if c >= 0x80 && c <= 0x9f {
			sb.WriteRune(cp1252High[c-0x80])
		} else {
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

func gbkTrailIndex(t byte) int {
	switch {
	case t >= 0x40 && t <= 0x7e:
		return int(t - 0x40)
	case t >= 0x80 && t <= 0xfe:
		return 63 + int(t-0x80)
	}
	return -1
}

// decodeGBK 只解 GBK 双字节区；遇到 4 字节 GB18030 扩展返回 ok=false，
// 由调用方决定是否退回 latin-1。
func decodeGBK(b []byte) (string, bool) {
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		if c == 0x80 {
			return "", false
		}
		if i+1 >= len(b) {
			return "", false
		}
		t := b[i+1]
		if t >= 0x30 && t <= 0x39 {
			// GB18030 四字节
			return "", false
		}
		ti := gbkTrailIndex(t)
		if ti < 0 {
			return "", false
		}
		code := gbkTable[int(c-0x81)*190+ti]
		if code == 0 {
			return "", false
		}
		sb.WriteRune(rune(code))
		i += 2
	}
	return sb.String(), true
}

// decodeAuto 与服务端 text.js 的 decodeBuffer 同序：BOM → UTF-8 → GBK → latin-1。
func decodeAuto(b []byte) string {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return string(b[3:])
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return decodeUTF16LE(b[2:])
	}
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		return decodeUTF16BE(b[2:])
	}
	if utf8.Valid(b) {
		return string(b)
	}
	if s, ok := decodeGBK(b); ok {
		return s
	}
	return latin1(b)
}

func latin1(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

func decodeUTF16LE(b []byte) string {
	var sb strings.Builder
	for i := 0; i+1 < len(b); i += 2 {
		v := uint16(b[i]) | uint16(b[i+1])<<8
		if v >= 0xD800 && v <= 0xDBFF && i+3 < len(b) {
			lo := uint16(b[i+2]) | uint16(b[i+3])<<8
			if lo >= 0xDC00 && lo <= 0xDFFF {
				sb.WriteRune(rune(0x10000 + (int(v-0xD800) << 10) + int(lo-0xDC00)))
				i += 2
				continue
			}
		}
		sb.WriteRune(rune(v))
	}
	return sb.String()
}

func decodeUTF16BE(b []byte) string {
	var sb strings.Builder
	for i := 0; i+1 < len(b); i += 2 {
		v := uint16(b[i+1]) | uint16(b[i])<<8
		sb.WriteRune(rune(v))
	}
	return sb.String()
}

// decodeWithCharset 按声明的字符集解码（XML 声明 / meta charset）。
func decodeWithCharset(b []byte, charset string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8":
		return string(b), true
	case "gbk", "gb2312", "gb-2312", "gb18030", "cp936", "x-gbk":
		if s, ok := decodeGBK(b); ok {
			return s, true
		}
		return "", false
	case "windows-1252", "cp1252", "latin1", "iso-8859-1":
		return decodeCP1252(b), true
	case "utf-16", "utf-16le":
		return decodeUTF16LE(b), true
	case "utf-16be":
		return decodeUTF16BE(b), true
	case "big5", "big-5":
		// 未内置 Big5 表：按 UTF-8 失败后退回 latin-1，保证不崩
		return latin1(b), true
	}
	return "", false
}

// sniffXMLCharset 从 <?xml ... encoding="xxx"?> 或 <meta charset> 里取字符集。
func sniffXMLCharset(head string) string {
	l := lowerASCII(head)
	if i := strings.Index(l, "encoding="); i >= 0 {
		rest := head[i+len("encoding="):]
		rest = strings.TrimLeft(rest, " \t\r\n")
		if len(rest) > 0 && (rest[0] == '"' || rest[0] == '\'') {
			q := rest[0]
			if j := strings.IndexByte(rest[1:], q); j >= 0 {
				return rest[1 : 1+j]
			}
		}
	}
	if i := strings.Index(l, "charset="); i >= 0 {
		rest := head[i+len("charset="):]
		rest = strings.TrimLeft(rest, " \t\r\n")
		rest = strings.TrimPrefix(rest, "\"")
		rest = strings.TrimPrefix(rest, "'")
		j := strings.IndexAny(rest, "\"'; >")
		if j >= 0 {
			return rest[:j]
		}
	}
	return ""
}

// DecodeHTML 按文档内 XML/meta 声明嗅探编码并解码（UTF-8/GBK/UTF-16/latin-1）。
func DecodeHTML(b []byte) string {
	return decodeDoc(b)
}

// decodeDoc 解码一章 HTML/XHTML：先看 XML 声明，再退回自动嗅探。
func decodeDoc(b []byte) string {
	head := b
	if len(head) > 512 {
		head = head[:512]
	}
	if cs := sniffXMLCharset(string(head)); cs != "" {
		if s, ok := decodeWithCharset(b, cs); ok {
			return s
		}
	}
	if bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) {
		return string(b[3:])
	}
	if utf8.Valid(b) {
		return string(b)
	}
	return decodeAuto(b)
}
