package parse

import (
	"net/url"
	"testing"
)

func TestAsStringFloat(t *testing.T) {
	if got := asString(float64(100)); got != "100" {
		t.Fatalf("asString(100) = %q", got)
	}
	if got := asString(8.5); got != "8.5" {
		t.Fatalf("asString(8.5) = %q", got)
	}
}

func TestExtractWebMetaFromOG(t *testing.T) {
	src := `<!doctype html><html><head>
<meta property="og:title" content="测试书">
<meta property="og:image" content="/img/cover.jpg">
<meta property="og:description" content="这是一段简介，讲的是一个测试故事。">
<meta name="author" content="张三">
<meta name="book:tag" content="玄幻">
</head><body>
<div class="book-info"><p>简介：这里是简介区块的补充内容，长度超过四十个字，用来验证详情页正文兜底是否工作正常。</p></div>
<p>字数：12.5万</p><p>最新章节：第356章 大结局</p><p>章节：356</p>
</body></html>`
	base, _ := url.Parse("https://books.example.com/book/123")
	m := ExtractWebMeta(src, base)
	if m.CoverURL != "https://books.example.com/img/cover.jpg" {
		t.Fatalf("cover = %q", m.CoverURL)
	}
	if m.Author != "张三" {
		t.Fatalf("author = %q", m.Author)
	}
	if m.Intro == "" {
		t.Fatal("intro empty")
	}
	if m.WordCount != "12.5万" {
		t.Fatalf("wordCount = %q", m.WordCount)
	}
	if m.LastChapter != "第356章 大结局" {
		t.Fatalf("lastChapter = %q", m.LastChapter)
	}
	if m.TotalChapterNum != 356 {
		t.Fatalf("totalChapterNum = %d", m.TotalChapterNum)
	}
}

func TestExtractWebMetaFromJSONLD(t *testing.T) {
	src := `<html><head>
<script type="application/ld+json">
{"@context":"https://schema.org","@type":"Book","name":"JSON 书",
"author":[{"@type":"Person","name":"李四"}],"image":"//cdn.example.com/c.jpg",
"description":"JSON-LD 里的简介内容。"}
</script></head><body></body></html>`
	base, _ := url.Parse("https://example.com/a/b")
	m := ExtractWebMeta(src, base)
	if m.Author != "李四" {
		t.Fatalf("author = %q", m.Author)
	}
	if m.CoverURL != "https://cdn.example.com/c.jpg" {
		t.Fatalf("cover = %q", m.CoverURL)
	}
	if m.Intro != "JSON-LD 里的简介内容。" {
		t.Fatalf("intro = %q", m.Intro)
	}
}
