package parse

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var (
	reOPFPath   = regexp.MustCompile(`(?i)full-path\s*=\s*"([^"]+)"`)
	reItemTag   = regexp.MustCompile(`(?i)<item\b[^>]*>`)
	reItemref   = regexp.MustCompile(`(?i)<itemref\b[^>]*>`)
	reAttrAll   = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	reDCTitle   = regexp.MustCompile(`(?i)<dc:title[^>]*>([\s\S]*?)</dc:title>`)
	reDCCreator = regexp.MustCompile(`(?i)<dc:creator[^>]*>([\s\S]*?)</dc:creator>`)
	reDCLang    = regexp.MustCompile(`(?i)<dc:language[^>]*>([\s\S]*?)</dc:language>`)
	reDCPub     = regexp.MustCompile(`(?i)<dc:publisher[^>]*>([\s\S]*?)</dc:publisher>`)
	reDCDate    = regexp.MustCompile(`(?i)<dc:date[^>]*>([\s\S]*?)</dc:date>`)
	reDCIdent   = regexp.MustCompile(`(?i)<dc:identifier[^>]*>([\s\S]*?)</dc:identifier>`)
	reDCDesc    = regexp.MustCompile(`(?i)<dc:description[^>]*>([\s\S]*?)</dc:description>`)
	reFirstH    = regexp.MustCompile(`(?i)<h([12])[^>]*>([\s\S]*?)</h[12]>`)
)

type manifestItem struct {
	id, href, mediaType, props string
}

type tocEntry struct {
	title string
	level int
}

func attrOf(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range reAttrAll.FindAllStringSubmatch(tag, -1) {
		v := m[2]
		if v == "" {
			v = m[3]
		}
		out[strings.ToLower(m[1])] = v
	}
	return out
}

func readZipEntry(zr *zip.ReadCloser, name string) ([]byte, bool) {
	if name == "" {
		return nil, false
	}
	for _, f := range zr.File {
		n := f.Name
		if n != name {
			if dec, err := url.PathUnescape(n); err != nil || dec != name {
				continue
			}
		}
		rc, err := f.Open()
		if err != nil {
			return nil, false
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		if err != nil {
			return nil, false
		}
		return data, true
	}
	return nil, false
}

// resolveHref 归一化 EPUB 内部相对路径：a/b/../c.xhtml -> a/c.xhtml
func resolveHref(baseDir, href string) string {
	href = strings.SplitN(href, "#", 2)[0]
	if dec, err := url.PathUnescape(href); err == nil {
		href = dec
	}
	p := baseDir
	if p != "" {
		p = p + "/" + href
	} else {
		p = href
	}
	clean := path.Clean(p)
	return strings.TrimPrefix(clean, "./")
}

func parseEpub(file, fallbackName string) (*Book, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, failf("corrupt", "不是有效的 EPUB：%v", err)
	}
	defer zr.Close()

	if enc, ok := readZipEntry(zr, "META-INF/encryption.xml"); ok && bytes.Contains(enc, []byte("EncryptedReference")) {
		return nil, failf("drm", "这本书带 DRM 加密")
	}

	container, ok := readZipEntry(zr, "META-INF/container.xml")
	if !ok {
		return nil, failf("corrupt", "EPUB 缺少 container.xml")
	}
	m := reOPFPath.FindSubmatch(container)
	if len(m) < 2 {
		return nil, failf("corrupt", "container.xml 里没有 OPF 路径")
	}
	opfPath := string(m[1])
	opfBytes, ok := readZipEntry(zr, opfPath)
	if !ok {
		return nil, failf("corrupt", "OPF 文件不存在：%s", opfPath)
	}
	opf := decodeDoc(opfBytes)
	baseDir := ""
	if i := strings.LastIndex(opfPath, "/"); i >= 0 {
		baseDir = opfPath[:i]
	}

	manifest := map[string]manifestItem{}
	for _, tag := range reItemTag.FindAllString(opf, -1) {
		a := attrOf(tag)
		if a["id"] != "" && a["href"] != "" {
			manifest[a["id"]] = manifestItem{
				id: a["id"], href: a["href"],
				mediaType: a["media-type"], props: a["properties"],
			}
		}
	}
	spine := make([]manifestItem, 0, len(manifest))
	for _, tag := range reItemref.FindAllString(opf, -1) {
		a := attrOf(tag)
		if it, ok := manifest[a["idref"]]; ok {
			spine = append(spine, it)
		}
	}
	if len(spine) == 0 {
		return nil, failf("corrupt", "EPUB spine 为空")
	}

	tocMap := map[string]tocEntry{}
	// EPUB3：properties 里带 nav
	for _, it := range manifest {
		if strings.Contains(it.props, "nav") {
			if data, ok := readZipEntry(zr, resolveHref(baseDir, it.href)); ok {
				parseNavDoc(decodeDoc(data), baseDir, tocMap)
			}
			break
		}
	}
	// EPUB2 NCX
	if len(tocMap) == 0 {
		for _, it := range manifest {
			if it.mediaType == "application/x-dtbncx+xml" || strings.HasSuffix(strings.ToLower(it.href), ".ncx") {
				if data, ok := readZipEntry(zr, resolveHref(baseDir, it.href)); ok {
					parseNCX(data, baseDir, tocMap)
				}
				break
			}
		}
	}

	getMeta := func(re *regexp.Regexp, fallback string) string {
		if mm := re.FindStringSubmatch(opf); len(mm) > 1 {
			if v := stripTags(mm[1]); v != "" {
				return v
			}
		}
		return fallback
	}
	meta := Meta{
		Title:       getMeta(reDCTitle, fallbackName),
		Author:      getMeta(reDCCreator, ""),
		Language:    getMeta(reDCLang, ""),
		Publisher:   getMeta(reDCPub, ""),
		PublishedAt: getMeta(reDCDate, ""),
		ISBN:        getMeta(reDCIdent, ""),
		Description: getMeta(reDCDesc, ""),
	}

	chapters := make([]Chapter, 0, len(spine))
	for _, item := range spine {
		if len(chapters) >= 3000 {
			break
		}
		isHTML := strings.Contains(item.mediaType, "html") || strings.Contains(item.mediaType, "xml") ||
			hasAnySuffix(strings.ToLower(item.href), ".xhtml", ".html", ".htm")
		if !isHTML {
			continue
		}
		p := resolveHref(baseDir, item.href)
		data, ok := readZipEntry(zr, p)
		if !ok {
			continue
		}
		html := decodeDoc(data)
		title := ""
		if t, ok := tocMap[p]; ok {
			title = t.title
		}
		level := 1
		if t, ok := tocMap[p]; ok && t.level > 0 {
			level = t.level
		}
		if title == "" {
			if hm := reFirstH.FindStringSubmatch(html); len(hm) > 2 {
				title = stripTags(hm[2])
			}
		}
		if title == "" {
			title = chTitleFallback(len(chapters) + 1)
		}
		ps := htmlToBlocks(html, title)
		if len(ps) == 0 {
			continue
		}
		chapters = append(chapters, Chapter{Title: title, Level: clampLevel(level), Paragraphs: ps})
	}
	if len(chapters) == 0 {
		return nil, failf("corrupt", "EPUB 正文为空")
	}
	return &Book{Format: "epub", Meta: meta, Chapters: chapters, Chars: countChars(chapters)}, nil
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

func chTitleFallback(n int) string {
	return "第 " + itoa(n) + " 章"
}

func clampLevel(l int) int {
	if l < 1 {
		return 1
	}
	if l > 6 {
		return 6
	}
	return l
}

func countChars(chs []Chapter) int {
	n := 0
	for _, c := range chs {
		for _, p := range c.Paragraphs {
			n += len([]rune(p.T))
		}
	}
	return n
}

type ncxNavPoint struct {
	NavLabel struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Points []ncxNavPoint `xml:"navPoint"`
}

type ncxDoc struct {
	NavMap struct {
		Points []ncxNavPoint `xml:"navPoint"`
	} `xml:"navMap"`
}

func parseNCX(data []byte, baseDir string, out map[string]tocEntry) {
	var doc ncxDoc
	if err := xml.Unmarshal(data, &doc); err == nil && len(doc.NavMap.Points) > 0 {
		var walk func(ps []ncxNavPoint, depth int)
		walk = func(ps []ncxNavPoint, depth int) {
			for _, p := range ps {
				src := strings.SplitN(p.Content.Src, "#", 2)[0]
				if src != "" {
					key := resolveHref(baseDir, src)
					if _, exists := out[key]; !exists {
						title := Clean(p.NavLabel.Text)
						if title == "" {
							title = "未命名章节"
						}
						out[key] = tocEntry{title: title, level: depth}
					}
				}
				walk(p.Points, depth+1)
			}
		}
		walk(doc.NavMap.Points, 1)
		return
	}
	// 兜底：正则解析（部分 NCX 结构不规范）
	reNP := regexp.MustCompile(`(?i)<navPoint\b[^>]*>((?:(?!<navPoint\b)[\s\S])*?)</navPoint>`)
	reTitle := regexp.MustCompile(`(?i)<title>([\s\S]*?)</title>`)
	reSrc := regexp.MustCompile(`(?i)<(?:content|src)\b[^>]*src="([^"]+)"|<src[^>]*>([^<]*)</src>`)
	for _, m := range reNP.FindAllStringSubmatch(string(data), -1) {
		inner := m[1]
		src := ""
		if sm := reSrc.FindStringSubmatch(inner); len(sm) > 1 {
			src = sm[1]
			if src == "" && len(sm) > 2 {
				src = sm[2]
			}
		}
		if src == "" {
			continue
		}
		src = strings.SplitN(src, "#", 2)[0]
		key := resolveHref(baseDir, src)
		if _, exists := out[key]; exists {
			continue
		}
		title := ""
		if tm := reTitle.FindStringSubmatch(inner); len(tm) > 1 {
			title = stripTags(tm[1])
		}
		if title == "" {
			title = "未命名章节"
		}
		out[key] = tocEntry{title: title, level: 1}
	}
}

// parseNavDoc 解析 EPUB3 的 nav.xhtml，按 li 嵌套深度算层级。
func parseNavDoc(html, baseDir string, out map[string]tocEntry) {
	doc := parseHTML(html)
	var nav *htmlNode
	var findNav func(n *htmlNode)
	findNav = func(n *htmlNode) {
		for _, k := range n.kids {
			if k.tag == "nav" {
				if nav == nil {
					nav = k
				}
				if strings.Contains(strings.ToLower(k.attrs["epub:type"]), "toc") {
					nav = k
					return
				}
			}
			findNav(k)
		}
	}
	findNav(doc)
	if nav == nil {
		return
	}
	var walk func(n *htmlNode, liDepth int)
	walk = func(n *htmlNode, liDepth int) {
		for _, k := range n.kids {
			if k.tag == "#text" {
				continue
			}
			depth := liDepth
			if k.tag == "li" {
				depth++
			}
			if k.tag == "a" {
				href := k.attrs["href"]
				if href != "" {
					key := resolveHref(baseDir, href)
					title := Clean(nodeText(k))
					if title == "" {
						title = "未命名章节"
					}
					if _, exists := out[key]; !exists {
						lv := liDepth
						if lv < 1 {
							lv = 1
						}
						out[key] = tocEntry{title: title, level: lv}
					}
				}
			}
			walk(k, depth)
		}
	}
	walk(nav, 0)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
