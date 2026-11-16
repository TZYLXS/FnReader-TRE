package parse

import (
	"net/url"
	"testing"
)

const rulePage = `<html><body>
<div class="mesc">
  <p class="intro">这是一段测试简介，长度足够。</p>
  <img id="fm" class="cover" data-src="/img/c.jpg" alt="封面">
  <h1 class="title">黎明之剑</h1>
  <span class="author">远瞳</span>
  <span class="kind">奇幻</span>
  <span class="word">500万字</span>
  <span class="lc">最新章节：第1594章 8:05</span>
</div>
</body></html>`

func mustBase(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestApplyBookInfoRuleBasic(t *testing.T) {
	rule := &BookInfoRule{
		Name:        "class.title@text",
		Author:      "class.author@text",
		CoverURL:    "id.fm@data-src",
		Intro:       "class.intro@text",
		Kind:        "class.kind@text",
		WordCount:   "class.word@text",
		LastChapter: "class.lc@text##：(.+)##",
	}
	res, ok := ApplyBookInfoRule(rulePage, mustBase(t, "https://www.example.com/book/1"), rule, RuleVars{})
	if !ok {
		t.Fatalf("ApplyBookInfoRule ok=false")
	}
	if res.Name != "黎明之剑" {
		t.Fatalf("name = %q", res.Name)
	}
	if res.Author != "远瞳" {
		t.Fatalf("author = %q", res.Author)
	}
	if res.CoverURL != "https://www.example.com/img/c.jpg" {
		t.Fatalf("cover = %q", res.CoverURL)
	}
	if res.Intro != "这是一段测试简介，长度足够。" {
		t.Fatalf("intro = %q", res.Intro)
	}
	if res.Kind != "奇幻" || res.WordCount != "500万字" {
		t.Fatalf("kind/word = %q/%q", res.Kind, res.WordCount)
	}
	if res.LastChapter != "第1594章 8:05" {
		t.Fatalf("lastChapter = %q", res.LastChapter)
	}
}

func TestApplyBookInfoRuleFallbackAndCSS(t *testing.T) {
	rule := &BookInfoRule{
		Name:   "class.not-exist@text||@css:div.mesc > h1.title@text",
		Intro:  "tag.p.0@text",
		Author: "tag.span.0@text",
	}
	res, ok := ApplyBookInfoRule(rulePage, nil, rule, RuleVars{})
	if !ok {
		t.Fatalf("ok=false")
	}
	if res.Name != "黎明之剑" {
		t.Fatalf("name = %q", res.Name)
	}
	if res.Intro != "这是一段测试简介，长度足够。" {
		t.Fatalf("intro = %q", res.Intro)
	}
	if res.Author != "远瞳" {
		t.Fatalf("author = %q", res.Author)
	}
}

func TestApplyBookInfoRuleUnsupportedJS(t *testing.T) {
	rule := &BookInfoRule{
		Name: "@js:document.querySelector('h1').innerText||class.title@text",
	}
	res, ok := ApplyBookInfoRule(rulePage, nil, rule, RuleVars{})
	if !ok || res.Name != "黎明之剑" {
		t.Fatalf("res = %+v ok=%v", res, ok)
	}
}

func TestResolveInitURL(t *testing.T) {
	vars := RuleVars{BookURL: "https://www.example.com/book/123"}
	if got := ResolveInitURL("{{bookUrl}}", vars); got != vars.BookURL {
		t.Fatalf("init = %q", got)
	}
	if got := ResolveInitURL("let u = 'https://m.example.com/book/123'; u", vars); got != "https://m.example.com/book/123" {
		t.Fatalf("init = %q", got)
	}
	if got := ResolveInitURL("", vars); got != "" {
		t.Fatalf("init = %q", got)
	}
	if got := ResolveInitURL("some.js.code()", vars); got != "" {
		t.Fatalf("init = %q", got)
	}
}

func TestParseBookSourceList(t *testing.T) {
	data := []byte(`[
	  {"bookSourceUrl":"https://www.example.com/","bookSourceName":"示例源",
	   "ruleBookInfo":{"name":"h1@text","intro":"div.intro@text"}},
	  {"bookSourceUrl":"https://no-rule.example/","bookSourceName":"无规则"}
	]`)
	list := ParseBookSourceList(data)
	if len(list) != 1 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].URL != "https://www.example.com/" || list[0].Rule.Name != "h1@text" {
		t.Fatalf("list[0] = %+v", list[0])
	}
}
