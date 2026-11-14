package parse

import (
	"strings"
)

// 轻量 HTML 解析器（零依赖）：
//   - 容错：未闭合标签自动闭合、大小写不敏感、属性单双引号/裸值都支持
//   - 只服务于「正文 → k/t 段落」，不实现完整 DOM 语义
//   - 跳过 script/style/noscript/head/svg 子树
type htmlNode struct {
	tag   string
	attrs map[string]string
	text  string
	kids  []*htmlNode
}

var voidTags = map[string]bool{
	"br": true, "img": true, "hr": true, "meta": true, "link": true, "input": true,
	"area": true, "base": true, "col": true, "embed": true, "param": true,
	"source": true, "track": true, "wbr": true, "frame": true, "basefont": true,
}

var skipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "head": true, "svg": true,
}

var blockTags = map[string]bool{
	"p": true, "div": true, "section": true, "article": true, "li": true,
	"blockquote": true, "pre": true, "td": true, "dd": true, "dt": true,
	"figcaption": true, "header": true, "footer": true, "aside": true,
	"main": true, "nav": true, "center": true,
}

func isHeadingTag(t string) bool {
	return len(t) == 2 && t[0] == 'h' && t[1] >= '1' && t[1] <= '6'
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func lowerASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b.WriteByte(c)
	}
	return b.String()
}

func parseHTML(src string) *htmlNode {
	root := &htmlNode{tag: "#doc"}
	stack := []*htmlNode{root}
	appendText := func(s string) {
		if s == "" {
			return
		}
		top := stack[len(stack)-1]
		if n := len(top.kids); n > 0 && top.kids[n-1].tag == "#text" {
			top.kids[n-1].text += s
			return
		}
		top.kids = append(top.kids, &htmlNode{tag: "#text", text: s})
	}

	i := 0
	for i < len(src) {
		if src[i] != '<' {
			n := strings.IndexByte(src[i:], '<')
			if n < 0 {
				appendText(src[i:])
				break
			}
			appendText(src[i : i+n])
			i += n
			continue
		}
		// 注释
		if strings.HasPrefix(src[i:], "<!--") {
			rest := src[i+4:]
			if n := strings.Index(rest, "-->"); n >= 0 {
				i = i + 4 + n + 3
			} else {
				break
			}
			continue
		}
		// doctype / 处理指令
		if strings.HasPrefix(src[i:], "<!") || strings.HasPrefix(src[i:], "<?") {
			if n := strings.IndexByte(src[i:], '>'); n >= 0 {
				i += n + 1
			} else {
				break
			}
			continue
		}
		// 结束标签
		if i+1 < len(src) && src[i+1] == '/' {
			j := i + 2
			for j < len(src) && (isASCIILetter(src[j]) || (src[j] >= '0' && src[j] <= '9') || src[j] == ':' || src[j] == '-' || src[j] == '_') {
				j++
			}
			name := lowerASCII(src[i+2 : j])
			for j < len(src) && src[j] != '>' {
				j++
			}
			if j < len(src) {
				j++
			}
			if name != "" {
				for k := len(stack) - 1; k >= 1; k-- {
					if stack[k].tag == name {
						stack = stack[:k]
						break
					}
				}
			}
			i = j
			continue
		}
		// 开始标签
		if i+1 < len(src) && isASCIILetter(src[i+1]) {
			j := i + 1
			for j < len(src) && (isASCIILetter(src[j]) || (src[j] >= '0' && src[j] <= '9') || src[j] == ':' || src[j] == '-' || src[j] == '_') {
				j++
			}
			name := lowerASCII(src[i+1 : j])
			attrs := map[string]string{}
			selfClose := false
			for j < len(src) {
				for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\r' || src[j] == '\n') {
					j++
				}
				if j >= len(src) {
					break
				}
				if src[j] == '>' {
					j++
					break
				}
				if src[j] == '/' {
					selfClose = true
					j++
					continue
				}
				an := j
				for j < len(src) && src[j] != '=' && src[j] != '>' && src[j] != ' ' && src[j] != '\t' && src[j] != '\r' && src[j] != '\n' && src[j] != '/' {
					j++
				}
				key := lowerASCII(src[an:j])
				for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\r' || src[j] == '\n') {
					j++
				}
				val := ""
				if j < len(src) && src[j] == '=' {
					j++
					for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\r' || src[j] == '\n') {
						j++
					}
					if j < len(src) && (src[j] == '"' || src[j] == '\'') {
						q := src[j]
						j++
						start := j
						for j < len(src) && src[j] != q {
							j++
						}
						val = src[start:j]
						if j < len(src) {
							j++
						}
					} else {
						start := j
						for j < len(src) && src[j] != '>' && src[j] != ' ' && src[j] != '\t' && src[j] != '\r' && src[j] != '\n' {
							j++
						}
						val = src[start:j]
					}
				}
				if key != "" {
					attrs[key] = decodeEntities(val)
				}
			}
			i = j

			// 跳过整棵 script/style/head/svg：直接找对应结束标签
			if skipTags[name] {
				if !selfClose && !voidTags[name] {
					if n := indexCloseTag(src, i, name); n >= 0 {
						i = n
					}
				}
				continue
			}
			node := &htmlNode{tag: name, attrs: attrs}
			top := stack[len(stack)-1]
			top.kids = append(top.kids, node)
			if !selfClose && !voidTags[name] {
				stack = append(stack, node)
			}
			continue
		}
		// 孤立的 '<'
		appendText("<")
		i++
	}
	return root
}

// indexCloseTag 从 from 开始找 </name ...> 结束后的位置；找不到返回 -1。
func indexCloseTag(src string, from int, name string) int {
	needle := "</" + name
	for from < len(src) {
		n := strings.Index(src[from:], "<")
		if n < 0 {
			return -1
		}
		at := from + n
		if at+2+len(name) <= len(src) && strings.HasPrefix(lowerASCII(src[at:at+2+len(name)]), needle) {
			if gt := strings.IndexByte(src[at:], '>'); gt >= 0 {
				return at + gt + 1
			}
			return -1
		}
		from = at + 1
	}
	return -1
}

func nodeText(n *htmlNode) string {
	if n.tag == "#text" {
		return decodeEntities(n.text)
	}
	var b strings.Builder
	for _, k := range n.kids {
		b.WriteString(nodeText(k))
	}
	return b.String()
}

func hasBlockChild(n *htmlNode) bool {
	for _, k := range n.kids {
		if k.tag == "#text" {
			continue
		}
		if blockTags[k.tag] || isHeadingTag(k.tag) || k.tag == "br" {
			return true
		}
	}
	return false
}

// htmlToBlocks 与服务端 html.js 同一套块级规则。
// dropTitle 非空时，若首段内容与章节标题相同则丢弃。
func htmlToBlocks(html, dropTitle string) []Block {
	doc := parseHTML(html)
	root := doc
	for _, k := range doc.kids {
		if k.tag == "body" {
			root = k
			break
		}
	}
	out := make([]Block, 0, 64)
	var buf strings.Builder
	flush := func(kind string) {
		t := Clean(buf.String())
		buf.Reset()
		if t == "" {
			return
		}
		for _, piece := range splitLong(t, 4096) {
			out = append(out, Block{K: kind, T: piece})
		}
	}

	var walk func(n *htmlNode)
	walk = func(n *htmlNode) {
		for _, child := range n.kids {
			if child.tag == "#text" {
				buf.WriteString(decodeEntities(child.text))
				continue
			}
			tag := child.tag
			switch {
			case tag == "br":
				flush("p")
			case isHeadingTag(tag):
				flush("p")
				t := Clean(nodeText(child))
				if t != "" {
					for _, piece := range splitLong(t, 4096) {
						out = append(out, Block{K: tag, T: piece})
					}
				}
			case tag == "img":
				flush("p")
				alt := Clean(child.attrs["alt"])
				if alt == "" {
					alt = Clean(child.attrs["title"])
				}
				out = append(out, Block{K: "img", T: alt})
			case tag == "hr":
				flush("p")
				out = append(out, Block{K: "hr", T: ""})
			case blockTags[tag]:
				if hasBlockChild(child) {
					flush("p")
					walk(child)
					flush("p")
					continue
				}
				flush("p")
				kind := "p"
				if tag == "blockquote" {
					kind = "quote"
				}
				t := Clean(nodeText(child))
				if t != "" {
					for _, piece := range splitLong(t, 4096) {
						out = append(out, Block{K: kind, T: piece})
					}
				}
			default:
				walk(child)
			}
		}
	}

	walk(root)
	flush("p")

	if dropTitle != "" {
		title := Clean(dropTitle)
		if len(out) > 0 && Clean(out[0].T) == title {
			out = out[1:]
		}
	}
	return out
}
