package parse

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// rulex.go：Legado 书源「ruleBookInfo」规则子集解析器。
//
// 支持语法：
//   - 字段规则用 `||` 分隔多段，依次尝试，取第一个非空结果
//   - 每段按 `@` 分步：选择器（css:/class./id./tag./裸标签）→ 取值（text/textNodes/
//     ownText/all/html 或任意属性名，如 src、href、data-src）→ 可选 `##正则##`
//   - `{{bookUrl}}`、`{{name}}`、`{{author}}` 占位替换
//   - `ruleBookInfo.init` 只识别「替换后是 http(s) 地址」或含第一个 http(s) 地址的写法
//
// 不支持的语法（@js: / @json: / @put: / @get: / JS 表达式等）整段放弃，由调用方
// 回退到通用网页解析，保证 cover/intro 任务永远有兜底。

// BookInfoRule 是书源 JSON 里 ruleBookInfo 的子集。
type BookInfoRule struct {
	Init        string
	Name        string
	Author      string
	CoverURL    string
	Intro       string
	Kind        string
	WordCount   string
	LastChapter string
	TocURL      string
}

// BookSource 是 /api/m/sources.json 一条书源的子集。
type BookSource struct {
	URL  string
	Name string
	Rule *BookInfoRule
}

// RuleVars 是规则里的占位变量。
type RuleVars struct {
	Name    string
	Author  string
	BookURL string
}

// RuleResult 是规则提取出的书籍信息。
type RuleResult struct {
	Name        string
	Author      string
	CoverURL    string
	Intro       string
	Kind        string
	WordCount   string
	LastChapter string
}

// Any 是否提取到任一字段。
func (r RuleResult) Any() bool {
	return r.Name != "" || r.Author != "" || r.CoverURL != "" || r.Intro != "" ||
		r.Kind != "" || r.WordCount != "" || r.LastChapter != ""
}

// ParseBookSourceList 解析服务端 /api/m/sources.json（Legado 导出格式的数组）。
func ParseBookSourceList(data []byte) []BookSource {
	var raw []struct {
		BookSourceURL  string `json:"bookSourceUrl"`
		BookSourceName string `json:"bookSourceName"`
		RuleBookInfo   *struct {
			Init        string `json:"init"`
			Name        string `json:"name"`
			Author      string `json:"author"`
			CoverURL    string `json:"coverUrl"`
			Intro       string `json:"intro"`
			Kind        string `json:"kind"`
			WordCount   string `json:"wordCount"`
			LastChapter string `json:"lastChapter"`
			TocURL      string `json:"tocUrl"`
		} `json:"ruleBookInfo"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	out := make([]BookSource, 0, len(raw))
	for _, s := range raw {
		if strings.TrimSpace(s.BookSourceURL) == "" || s.RuleBookInfo == nil {
			continue
		}
		r := s.RuleBookInfo
		rule := &BookInfoRule{
			Init:        r.Init,
			Name:        r.Name,
			Author:      r.Author,
			CoverURL:    r.CoverURL,
			Intro:       r.Intro,
			Kind:        r.Kind,
			WordCount:   r.WordCount,
			LastChapter: r.LastChapter,
			TocURL:      r.TocURL,
		}
		if !rule.empty() {
			out = append(out, BookSource{URL: strings.TrimSpace(s.BookSourceURL), Name: s.BookSourceName, Rule: rule})
		}
	}
	return out
}

func (r *BookInfoRule) empty() bool {
	return r.Init == "" && r.Name == "" && r.Author == "" && r.CoverURL == "" &&
		r.Intro == "" && r.Kind == "" && r.WordCount == "" && r.LastChapter == "" && r.TocURL == ""
}

// ResolveInitURL 把 ruleBookInfo.init 换算成详情页地址：
// 替换占位后若本身就是完整 http(s) 地址、或能从中提取出第一个 http(s) 地址则返回，
// 否则返回空（调用方继续用 bookUrl 原地址）。
func ResolveInitURL(init string, vars RuleVars) string {
	s := strings.TrimSpace(replaceVars(init, vars))
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			return s
		}
	}
	re := regexp.MustCompile(`https?://[^\s"'<>\\]+`)
	if m := re.FindString(s); m != "" {
		return strings.TrimRight(m, ",;)]}，；）】")
	}
	return ""
}

// ApplyBookInfoRule 在详情页上按书源规则提取字段；base 用于补全相对封面地址。
func ApplyBookInfoRule(src string, base *url.URL, rule *BookInfoRule, vars RuleVars) (RuleResult, bool) {
	var out RuleResult
	if src == "" || rule == nil {
		return out, false
	}
	if rule.Name != "" {
		out.Name = Clean(applyRuleField(src, rule.Name, vars))
	}
	if rule.Author != "" {
		out.Author = Clean(applyRuleField(src, rule.Author, vars))
	}
	if rule.CoverURL != "" {
		c := strings.TrimSpace(applyRuleField(src, rule.CoverURL, vars))
		c = strings.Trim(c, `"'`)
		if c != "" {
			if u, err := url.Parse(c); err == nil && base != nil && !u.IsAbs() {
				c = base.ResolveReference(u).String()
			}
			out.CoverURL = c
		}
	}
	if rule.Intro != "" {
		if v := applyRuleField(src, rule.Intro, vars); v != "" {
			out.Intro = trimIntro(v)
		}
	}
	if rule.Kind != "" {
		out.Kind = Clean(applyRuleField(src, rule.Kind, vars))
	}
	if rule.WordCount != "" {
		out.WordCount = Clean(applyRuleField(src, rule.WordCount, vars))
	}
	if rule.LastChapter != "" {
		out.LastChapter = Clean(applyRuleField(src, rule.LastChapter, vars))
	}
	return out, out.Any()
}

// ---- 规则执行 ----

func replaceVars(s string, vars RuleVars) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	table := map[string]string{
		"bookUrl": vars.BookURL,
		"name":    vars.Name,
		"author":  vars.Author,
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], "{{")
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+j])
		k := strings.Index(s[i+j+2:], "}}")
		if k < 0 {
			b.WriteString(s[i+j:])
			break
		}
		body := strings.TrimSpace(s[i+j+2 : i+j+2+k])
		key := body
		if dot := strings.LastIndex(body, "."); dot >= 0 && strings.HasPrefix(body, "$") {
			key = body[dot+1:]
		}
		if v, ok := table[key]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(s[i+j : i+j+2+k+2])
		}
		i = i + j + 2 + k + 2
	}
	return b.String()
}

type ruleStepKind int

const (
	stepSelector ruleStepKind = iota
	stepValue
	stepRegex
	stepUnsupported
)

type ruleStep struct {
	kind ruleStepKind
	sel  string // 选择器 token：css:xxx / class.xxx / id.xxx / tag.xxx / 裸标签
	val  string // 取值关键字（text/textNodes/ownText/all/html）
	re   string // ##正则## 的正文
	note string // unsupported 原因
}

func isValueKeyword(tok string) bool {
	switch tok {
	case "text", "textNodes", "ownText", "all", "html":
		return true
	}
	return false
}

var unsupportedPrefixes = []string{"js:", "json:", "put:", "get:", "select:", "a:", "h:", "contains:", "if:", "not:", "title:", "name:"}

func classifyToken(tok string) ruleStep {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return ruleStep{kind: stepUnsupported, note: "空步骤"}
	}
	if isValueKeyword(tok) {
		return ruleStep{kind: stepValue, val: tok}
	}
	for _, p := range unsupportedPrefixes {
		if strings.HasPrefix(tok, p) {
			return ruleStep{kind: stepUnsupported, note: tok}
		}
	}
	if strings.HasPrefix(tok, "css:") {
		sel := strings.TrimSpace(tok[len("css:"):])
		if sel == "" {
			return ruleStep{kind: stepUnsupported, note: tok}
		}
		return ruleStep{kind: stepSelector, sel: "css:" + sel}
	}
	// 带前缀的才是选择器（class./id./tag.）；裸 token 一律按属性名取值
	//（src、href、data-src、content……），与 Legado 规则语义一致。
	if strings.HasPrefix(tok, "class.") || strings.HasPrefix(tok, "id.") || strings.HasPrefix(tok, "tag.") {
		return ruleStep{kind: stepSelector, sel: tok}
	}
	return ruleStep{kind: stepValue, val: tok}
}

// tokenize 把一段规则按 @ 切步；##…## 保持为独立正则步（正则内部允许 @）。
func tokenize(seg string) []ruleStep {
	var steps []ruleStep
	for i := 0; i < len(seg); {
		if strings.HasPrefix(seg[i:], "##") {
			j := strings.Index(seg[i+2:], "##")
			if j < 0 {
				break
			}
			steps = append(steps, ruleStep{kind: stepRegex, re: seg[i+2 : i+2+j]})
			i += 2 + j + 2
			continue
		}
		if seg[i] == '@' {
			i++
		} else {
			// 段首裸 token（一般不会出现，容错按同一规则读）
		}
		start := i
		for i < len(seg) && seg[i] != '@' && !strings.HasPrefix(seg[i:], "##") {
			i++
		}
		if tok := strings.TrimSpace(seg[start:i]); tok != "" {
			steps = append(steps, classifyToken(tok))
		}
	}
	return steps
}

func applyRuleField(page, ruleStr string, vars RuleVars) string {
	ruleStr = replaceVars(ruleStr, vars)
	root := parseHTML(page)
	for _, seg := range strings.Split(ruleStr, "||") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if v := applySegment(seg, root); v != "" {
			return v
		}
	}
	return ""
}

func applySegment(seg string, root *htmlNode) string {
	steps := tokenize(seg)
	elems := []*htmlNode{root}
	cur := ""
	haveStr := false

	for idx := 0; idx < len(steps); idx++ {
		st := steps[idx]
		switch st.kind {
		case stepUnsupported:
			return ""
		case stepSelector:
			if haveStr {
				return ""
			}
			elems = selectBy(elems, st.sel)
			if len(elems) == 0 {
				return ""
			}
		case stepValue:
			if len(elems) == 0 {
				return ""
			}
			cur = valueOf(elems[0], st.val)
			haveStr = true
			elems = nil
		case stepRegex:
			if !haveStr {
				if len(elems) == 0 {
					return ""
				}
				cur = nodeText(elems[0])
				haveStr = true
				elems = nil
			}
			// 连续两个 ##…##：第一个是匹配正则，第二个是替换串（replace 形式）
			if idx+1 < len(steps) && steps[idx+1].kind == stepRegex {
				next := steps[idx+1]
				idx++
				re, err := regexp.Compile(st.re)
				if err != nil {
					return ""
				}
				cur = re.ReplaceAllString(cur, next.re)
				if cur == "" {
					return ""
				}
				continue
			}
			re, err := regexp.Compile(st.re)
			if err != nil {
				return ""
			}
			m := re.FindStringSubmatch(cur)
			if m == nil {
				return ""
			}
			if len(m) > 1 {
				cur = m[1]
			} else {
				cur = m[0]
			}
			if cur == "" {
				return ""
			}
		}
	}

	if haveStr {
		return strings.TrimSpace(cur)
	}
	if len(elems) == 0 {
		return ""
	}
	return strings.TrimSpace(nodeText(elems[0]))
}

func valueOf(n *htmlNode, kw string) string {
	switch kw {
	case "text", "textNodes":
		return nodeText(n)
	case "ownText":
		var b strings.Builder
		for _, k := range n.kids {
			if k.tag == "#text" {
				b.WriteString(decodeEntities(k.text))
			}
		}
		return b.String()
	case "all", "html":
		return innerHTML(n)
	}
	return ruleAttr(n, kw)
}

func ruleAttr(n *htmlNode, key string) string {
	key = strings.ToLower(key)
	if key == "src" || key == "data-src" {
		for _, k := range []string{"src", "data-src", "data-original", "data-lazy-src", "data-original-src", "data-echo"} {
			if v := strings.TrimSpace(n.attrs[k]); v != "" {
				return v
			}
		}
		return ""
	}
	return n.attrs[key]
}

func innerHTML(n *htmlNode) string {
	var b strings.Builder
	writeNode := func(node *htmlNode) {}
	writeNode = func(node *htmlNode) {
		if node.tag == "#text" {
			b.WriteString(escapeHTMLText(node.text))
			return
		}
		b.WriteByte('<')
		b.WriteString(node.tag)
		for k, v := range node.attrs {
			b.WriteByte(' ')
			b.WriteString(k)
			b.WriteString(`="`)
			b.WriteString(strings.ReplaceAll(v, `"`, "&quot;"))
			b.WriteByte('"')
		}
		b.WriteByte('>')
		for _, k := range node.kids {
			writeNode(k)
		}
		b.WriteString("</")
		b.WriteString(node.tag)
		b.WriteByte('>')
	}
	for _, k := range n.kids {
		writeNode(k)
	}
	return b.String()
}

func escapeHTMLText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// ---- 选择器 ----

// splitIndex 拆 `名字.数字` 形式的索引：`mesc.1` → (mesc, 1)；`div` → (div, -1)。
func splitIndex(tok string) (string, int) {
	if i := strings.LastIndexByte(tok, '.'); i > 0 {
		if n, err := strconv.Atoi(tok[i+1:]); err == nil {
			return tok[:i], n
		}
	}
	return tok, -1
}

func isTagName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isASCIILetter(c) && (c < '0' || c > '9') && c != '-' && c != ':' && c != '_' {
			return false
		}
	}
	return isASCIILetter(s[0])
}

func hasClass(n *htmlNode, cls string) bool {
	for _, c := range strings.Fields(n.attrs["class"]) {
		if c == cls {
			return true
		}
	}
	return false
}

// findIn 在给定集合的子树里找匹配节点，可选取第 N 个。
func findIn(scope []*htmlNode, match func(*htmlNode) bool, idx int) []*htmlNode {
	var found []*htmlNode
	var walk func(n *htmlNode)
	walk = func(n *htmlNode) {
		for _, k := range n.kids {
			if k.tag == "#text" {
				continue
			}
			if match(k) {
				found = append(found, k)
			}
			walk(k)
		}
	}
	for _, s := range scope {
		walk(s)
	}
	if idx >= 0 {
		if idx < len(found) {
			return []*htmlNode{found[idx]}
		}
		return nil
	}
	return found
}

func selectBy(scope []*htmlNode, token string) []*htmlNode {
	token = strings.TrimSpace(token)
	switch {
	case strings.HasPrefix(token, "css:"):
		return queryCSS(scope, token[len("css:"):])
	case strings.HasPrefix(token, "class."):
		name, idx := splitIndex(token[len("class."):])
		return findIn(scope, func(n *htmlNode) bool { return hasClass(n, name) }, idx)
	case strings.HasPrefix(token, "id."):
		name, idx := splitIndex(token[len("id."):])
		return findIn(scope, func(n *htmlNode) bool { return n.attrs["id"] == name }, idx)
	case strings.HasPrefix(token, "tag."):
		name, idx := splitIndex(token[len("tag."):])
		name = strings.ToLower(name)
		return findIn(scope, func(n *htmlNode) bool { return n.tag == name }, idx)
	default:
		name, idx := splitIndex(token)
		if isTagName(name) {
			name = strings.ToLower(name)
			return findIn(scope, func(n *htmlNode) bool { return n.tag == name }, idx)
		}
		return nil
	}
}

// ---- CSS 选择器子集：tag、.class、#id、[attr]、[attr=val]、[attr^=val]，
//      组合器：空格（后代）、>（子代）；逗号分组。 ----

type cssAttrCond struct {
	key   string
	op    byte // 0 存在、'=' 相等、'^' 前缀
	value string
}

type cssCompound struct {
	tag     string
	classes []string
	ids     []string
	attrs   []cssAttrCond
}

type cssStep struct {
	comp cssCompound
	rel  byte // ' ' 后代，'>' 子代
}

func parseCSSComplex(sel string) []cssStep {
	var steps []cssStep
	i := 0
	rel := byte(0) // 首个 compound 的 rel 无意义
	for i < len(sel) {
		for i < len(sel) && (sel[i] == ' ' || sel[i] == '\t') {
			i++
		}
		if i >= len(sel) {
			break
		}
		if sel[i] == '>' {
			rel = '>'
			i++
			for i < len(sel) && (sel[i] == ' ' || sel[i] == '\t') {
				i++
			}
			if i >= len(sel) {
				break
			}
		} else if len(steps) > 0 {
			rel = ' '
		}
		var comp cssCompound
		// 读一个 compound：tag/.class/#id/[attr] 序列
		for i < len(sel) {
			c := sel[i]
			if c == ' ' || c == '\t' || c == '>' || c == ',' {
				break
			}
			switch {
			case c == '.':
				j := i + 1
				for j < len(sel) && sel[j] != '.' && sel[j] != '#' && sel[j] != '[' && sel[j] != ' ' && sel[j] != '\t' && sel[j] != '>' && sel[j] != ',' {
					j++
				}
				if j > i+1 {
					comp.classes = append(comp.classes, sel[i+1:j])
				}
				i = j
			case c == '#':
				j := i + 1
				for j < len(sel) && sel[j] != '.' && sel[j] != '#' && sel[j] != '[' && sel[j] != ' ' && sel[j] != '\t' && sel[j] != '>' && sel[j] != ',' {
					j++
				}
				if j > i+1 {
					comp.ids = append(comp.ids, sel[i+1:j])
				}
				i = j
			case c == '[':
				j := strings.IndexByte(sel[i+1:], ']')
				if j < 0 {
					return steps
				}
				body := strings.TrimSpace(sel[i+1 : i+1+j])
				cond := cssAttrCond{}
				if k := strings.IndexAny(body, "=^"); k >= 0 {
					cond.key = strings.TrimSpace(body[:k])
					cond.op = body[k]
					cond.value = strings.Trim(strings.TrimSpace(body[k+1:]), `"'`)
				} else {
					cond.key = body
				}
				if cond.key != "" {
					comp.attrs = append(comp.attrs, cond)
				}
				i += j + 2
			default:
				j := i
				for j < len(sel) && sel[j] != '.' && sel[j] != '#' && sel[j] != '[' && sel[j] != ' ' && sel[j] != '\t' && sel[j] != '>' && sel[j] != ',' {
					j++
				}
				if j > i {
					comp.tag = strings.ToLower(sel[i:j])
				}
				i = j
			}
		}
		if comp.tag != "" || len(comp.classes) > 0 || len(comp.ids) > 0 || len(comp.attrs) > 0 {
			steps = append(steps, cssStep{comp: comp, rel: rel})
		}
		if i < len(sel) && sel[i] == ',' {
			break
		}
	}
	return steps
}

func splitCSSGroups(sel string) []string {
	var out []string
	start := 0
	for i := 0; i < len(sel); i++ {
		if sel[i] == ',' {
			if g := strings.TrimSpace(sel[start:i]); g != "" {
				out = append(out, g)
			}
			start = i + 1
		}
	}
	if g := strings.TrimSpace(sel[start:]); g != "" {
		out = append(out, g)
	}
	return out
}

func matchCompound(n *htmlNode, comp cssCompound) bool {
	if n.tag == "#text" {
		return false
	}
	if comp.tag != "" && n.tag != comp.tag {
		return false
	}
	for _, c := range comp.classes {
		if !hasClass(n, c) {
			return false
		}
	}
	for _, id := range comp.ids {
		if n.attrs["id"] != id {
			return false
		}
	}
	for _, a := range comp.attrs {
		v := n.attrs[a.key]
		switch a.op {
		case 0:
			if _, ok := n.attrs[a.key]; !ok {
				return false
			}
		case '=':
			if v != a.value {
				return false
			}
		case '^':
			if !strings.HasPrefix(v, a.value) {
				return false
			}
		}
	}
	return true
}

func queryCSS(scope []*htmlNode, sel string) []*htmlNode {
	var out []*htmlNode
	for _, group := range splitCSSGroups(sel) {
		steps := parseCSSComplex(group)
		if len(steps) == 0 {
			continue
		}
		// 右端 compound 先在 scope 子树里找全部候选
		right := steps[len(steps)-1]
		var cands []*htmlNode
		for _, s := range scope {
			var walk func(n *htmlNode)
			walk = func(n *htmlNode) {
				for _, k := range n.kids {
					if k.tag == "#text" {
						continue
					}
					if matchCompound(k, right.comp) {
						cands = append(cands, k)
					}
					walk(k)
				}
			}
			walk(s)
		}
		if len(steps) == 1 {
			out = append(out, cands...)
			continue
		}
		// 向左依次验证祖先链
		for _, c := range cands {
			if matchCSSAncestors(c, steps[:len(steps)-1]) {
				out = append(out, c)
			}
		}
	}
	return out
}

func matchCSSAncestors(n *htmlNode, left []cssStep) bool {
	cur := n
	for i := len(left) - 1; i >= 0; i-- {
		step := left[i]
		p := cur.parent
		if p == nil {
			return false
		}
		if step.rel == '>' {
			if !matchCompound(p, step.comp) {
				return false
			}
			cur = p
			continue
		}
		found := false
		for p != nil {
			if matchCompound(p, step.comp) {
				found = true
				break
			}
			p = p.parent
		}
		if !found {
			return false
		}
		cur = p
	}
	return true
}
