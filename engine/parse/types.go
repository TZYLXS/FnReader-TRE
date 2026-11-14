package parse

import (
	"fmt"
	"strings"
)

// SCHEMA 与服务端 legado-web / yuedu-pro 完全一致，比较时区分大小写。
const SCHEMA = "fnreader.ebook.v1"

// Block 对应协议里的 {"k":..., "t":...}
type Block struct {
	K string `json:"k"`
	T string `json:"t"`
}

type Chapter struct {
	Title      string  `json:"title"`
	Level      int     `json:"level"`
	Paragraphs []Block `json:"paragraphs"`
}

type Meta struct {
	Title       string `json:"title"`
	Author      string `json:"author"`
	Language    string `json:"language"`
	Publisher   string `json:"publisher"`
	PublishedAt string `json:"publishedAt"`
	ISBN        string `json:"isbn"`
	Description string `json:"description"`
}

// Book 是解析结果，也是缓存里 parsed/<sha1>.json.gz 的内容。
type Book struct {
	Format   string    `json:"format"`
	Meta     Meta      `json:"meta"`
	Chapters []Chapter `json:"chapters"`
	Chars    int       `json:"chars"`
}

// Error 带错误码；错误码直接回传给软总线（服务端据此决定是否本地兜底）：
//
//	drm / corrupt / unsupported / kf8-unsupported -> 服务端标记 failed，不再兜底
//	其他（no-text-layer / font-encoding / io / ...）-> 服务端本地兜底
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return e.Code
	}
	return e.Code + ": " + e.Msg
}

func failf(code, format string, args ...interface{}) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// ParseFile 按格式分发；format 取扩展名（epub/mobi/azw/azw3/pdf/txt）。
func ParseFile(path, format, fallbackName string) (*Book, error) {
	f := strings.ToLower(strings.TrimSpace(format))
	if f == "" && strings.Contains(path, ".") {
		f = strings.ToLower(path[strings.LastIndex(path, ".")+1:])
	}
	switch f {
	case "epub":
		return parseEpub(path, fallbackName)
	case "mobi", "prc", "azw", "azw3":
		return parseMobi(path, fallbackName)
	case "pdf":
		return parsePDF(path, fallbackName)
	case "txt", "text":
		return parseTxt(path, fallbackName)
	default:
		return nil, failf("unsupported", "不支持的文件格式：%s", f)
	}
}
