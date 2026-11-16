package parse

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// WebMeta 是从书籍详情页能拿到的通用元信息。
// 不依赖任何书源规则，只读 HTML 的 meta / JSON-LD / 简介区块，
// 供软总线 2.6.17+ 的 cover / intro / bookinfo 任务使用。
type WebMeta struct {
	Title           string
	Author          string
	Intro           string
	CoverURL        string
	Kind            string
	WordCount       string
	LastChapter     string
	TotalChapterNum int
}

var (
	reMetaTag   = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	reLinkTag   = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	reImgTag    = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	reAttr      = regexp.MustCompile(`(?is)([a-z_:][-a-z0-9_:.]*)\s*=\s*("([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	reLDJSON    = regexp.MustCompile(`(?is)<script[^>]+type\s*=\s*["']application/ld\+json["'][^>]*>(.*?)</script>`)
	reIntroOpen = regexp.MustCompile(`(?is)<(div|section|article|p|dd|blockquote)\b[^>]*>`)
	reTags      = regexp.MustCompile(`(?is)<[^>]+>`)
	reWordCount = regexp.MustCompile(`(?i)(?:字数|word\s*count)[^0-9]{0,8}([0-9]+(?:\.[0-9]+)?\s*[万wW]?)`)
	reLastChap  = regexp.MustCompile(`(?i)(?:最新章节|last\s*chapter)[^：:]{0,6}[：:]\s*([^<\n]{1,80})`)
	reTotalChap = regexp.MustCompile(`(?i)(?:章节|总章节|chapters?)[^0-9]{0,10}([0-9]{1,6})`)
)

// ExtractWebMeta 解析详情页；base 用于把相对 URL 补成绝对地址。
func ExtractWebMeta(src string, base *url.URL) WebMeta {
	var m WebMeta
	if src == "" {
		return m
	}
	attrsList := map[string]string{}
	for _, tag := range reMetaTag.FindAllString(src, -1) {
		a := attrsOf(tag)
		key := strings.ToLower(firstNonEmpty(a["property"], a["name"], a["itemprop"]))
		val := strings.TrimSpace(firstNonEmpty(a["content"], a["value"]))
		if key == "" || val == "" {
			continue
		}
		if _, ok := attrsList[key]; !ok {
			attrsList[key] = decodeEntities(val)
		}
	}

	m.Title = attrsList["og:title"]
	m.Author = firstNonEmpty(attrsList["book:author"], attrsList["og:book:author"], attrsList["author"], attrsList["itemprop:author"])
	m.Intro = firstNonEmpty(attrsList["og:description"], attrsList["description"], attrsList["twitter:description"], attrsList["itemprop:description"])
	m.CoverURL = firstNonEmpty(
		attrsList["og:image"], attrsList["og:image:url"], attrsList["og:image:secure_url"],
		attrsList["twitter:image"], attrsList["twitter:image:src"], attrsList["image"],
		attrsList["itemprop:image"],
	)
	m.Kind = firstNonEmpty(attrsList["book:tag"], attrsList["article:section"], attrsList["og:book:tag"])

	// JSON-LD（Book / CreativeWork），只补空字段，不覆盖更可信的 og:*
	for _, sm := range reLDJSON.FindAllStringSubmatch(src, -1) {
		var v interface{}
		if json.Unmarshal([]byte(strings.TrimSpace(sm[1])), &v) == nil {
			fillLDJSON(v, &m)
		}
	}

	// <link rel="image_src"> 与封面 img 兜底
	for _, tag := range reLinkTag.FindAllString(src, -1) {
		a := attrsOf(tag)
		rel := strings.ToLower(a["rel"])
		if strings.Contains(rel, "image_src") && a["href"] != "" && m.CoverURL == "" {
			m.CoverURL = a["href"]
		}
	}
	if m.CoverURL == "" {
		for _, tag := range reImgTag.FindAllString(src, -1) {
			a := attrsOf(tag)
			hint := strings.ToLower(a["class"] + " " + a["id"] + " " + a["alt"])
			if !strings.Contains(hint, "cover") && !strings.Contains(hint, "poster") && !strings.Contains(hint, "thumb") {
				continue
			}
			if v := firstNonEmpty(a["src"], a["data-src"], a["data-original"], a["data-lazy-src"]); v != "" {
				m.CoverURL = v
				break
			}
		}
	}

	// 简介区块兜底：class/id 含 intro/desc/summary/book-info 等，取最长的一段
	if len(m.Intro) < 40 {
		best := ""
		for _, loc := range reIntroOpen.FindAllStringIndex(src, -1) {
			tag := src[loc[0]:loc[1]]
			a := attrsOf(tag)
			hint := strings.ToLower(a["class"] + " " + a["id"] + " " + a["itemprop"])
			if !containsAny(hint, "intro", "desc", "summary", "abstract", "book-info", "bookinfo", "content-desc") {
				continue
			}
			name := strings.ToLower(src[loc[0]+1 : loc[0]+1+tagNameLen(src[loc[0]+1:])])
			end := indexCloseTag(src, loc[1], name)
			if end < 0 {
				continue
			}
			closeStart := strings.LastIndex(src[loc[1]:end], "</")
			if closeStart < 0 {
				continue
			}
			text := cleanHTMLText(src[loc[1] : loc[1]+closeStart])
			if len(text) > len(best) {
				best = text
			}
		}
		if len(best) > len(m.Intro) {
			m.Intro = best
		}
	}

	// 页面可见文本里的字数 / 最新章节 / 章节数
	pageText := cleanHTMLText(src)
	if m.WordCount == "" {
		if mm := reWordCount.FindStringSubmatch(pageText); len(mm) > 1 {
			m.WordCount = strings.TrimSpace(mm[1])
		}
	}
	if m.LastChapter == "" {
		if mm := reLastChap.FindStringSubmatch(pageText); len(mm) > 1 {
			last := cleanHTMLText(mm[1])
			for _, stop := range []string{" 章节", " 字数", " 作者", " 分类", " 状态", " 更新时间", " 点击"} {
				if i := strings.Index(last, stop); i > 0 {
					last = last[:i]
				}
			}
			m.LastChapter = strings.TrimSpace(last)
		}
	}
	if m.TotalChapterNum == 0 {
		if mm := reTotalChap.FindStringSubmatch(pageText); len(mm) > 1 {
			m.TotalChapterNum = atoiSafe(mm[1])
		}
	}

	if base != nil && m.CoverURL != "" {
		if u, err := url.Parse(m.CoverURL); err == nil {
			m.CoverURL = base.ResolveReference(u).String()
		}
	}
	m.Intro = trimIntro(m.Intro)
	return m
}

func attrsOf(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range reAttr.FindAllStringSubmatch(tag, -1) {
		k := lowerASCII(m[1])
		v := m[3]
		if v == "" {
			v = m[4]
		}
		if v == "" {
			v = m[5]
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}

func fillLDJSON(v interface{}, m *WebMeta) {
	switch x := v.(type) {
	case []interface{}:
		for _, it := range x {
			fillLDJSON(it, m)
		}
	case map[string]interface{}:
		if g, ok := x["@graph"]; ok {
			fillLDJSON(g, m)
		}
		typ := strings.ToLower(asString(x["@type"]))
		if typ != "" && !strings.Contains(typ, "book") && !strings.Contains(typ, "creativework") {
			return
		}
		if m.Title == "" {
			m.Title = asString(x["name"])
		}
		if m.Author == "" {
			m.Author = personName(x["author"])
		}
		if m.CoverURL == "" {
			m.CoverURL = imageURL(x["image"])
		}
		if m.Intro == "" {
			m.Intro = asString(x["description"])
		}
		if m.Kind == "" {
			m.Kind = asString(x["genre"])
		}
		if m.TotalChapterNum == 0 {
			m.TotalChapterNum = atoiSafe(asString(x["numberOfPages"]))
		}
	}
}

func personName(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]interface{}:
		return asString(x["name"])
	case []interface{}:
		names := make([]string, 0, len(x))
		for _, it := range x {
			if n := personName(it); n != "" {
				names = append(names, n)
			}
		}
		return strings.Join(names, " / ")
	}
	return ""
}

func imageURL(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]interface{}:
		return firstNonEmpty(asString(x["url"]), asString(x["contentUrl"]))
	case []interface{}:
		for _, it := range x {
			if u := imageURL(it); u != "" {
				return u
			}
		}
	}
	return ""
}

func asString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	}
	return ""
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return 0
		}
	}
	return n
}

func cleanHTMLText(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = reTags.ReplaceAllString(s, " ")
	s = decodeEntities(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func trimIntro(s string) string {
	s = cleanHTMLText(s)
	for _, p := range []string{"简介：", "简介:", "内容简介：", "内容简介:", "作品简介：", "作品简介:"} {
		s = strings.TrimPrefix(s, p)
	}
	if len([]rune(s)) > 4000 {
		s = string([]rune(s)[:4000])
	}
	return strings.TrimSpace(s)
}

func tagNameLen(s string) int {
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == ':' || c == '-' || c == '_' {
			i++
			continue
		}
		break
	}
	return i
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
