package parse

import "testing"

func TestHTMLBlocks(t *testing.T) {
	cases := []struct {
		name string
		html string
		drop string
		want []Block
	}{
		{
			name: "heading then paragraphs",
			html: `<html><head><title>x</title></head><body><h1>第一章 出发</h1><p>第一段。</p><p>第二段。</p></body></html>`,
			drop: "第一章 出发",
			want: []Block{{K: "p", T: "第一段。"}, {K: "p", T: "第二段。"}},
		},
		{
			name: "nested div",
			html: `<div>外层<div>内层</div>尾巴</div>`,
			want: []Block{{K: "p", T: "外层"}, {K: "p", T: "内层"}, {K: "p", T: "尾巴"}},
		},
		{
			name: "br and inline",
			html: `<p>甲<br/>乙<b>丙</b></p>`,
			want: []Block{{K: "p", T: "甲"}, {K: "p", T: "乙丙"}},
		},
		{
			name: "quote hr img",
			html: `<blockquote>引用</blockquote><hr/><img alt="图"/><p>尾</p>`,
			want: []Block{{K: "quote", T: "引用"}, {K: "hr", T: ""}, {K: "img", T: "图"}, {K: "p", T: "尾"}},
		},
	}
	for _, c := range cases {
		got := htmlToBlocks(c.html, c.drop)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %#v, want %#v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s[%d]: got %#v want %#v", c.name, i, got[i], c.want[i])
			}
		}
	}
}

func TestEntities(t *testing.T) {
	if s := decodeEntities("a&amp;b&nbsp;c&#65;&#x42;"); s != "a&b\u00a0cAB" {
		t.Fatalf("entities: %q", s)
	}
}

func TestGBK(t *testing.T) {
	raw := []byte{0xb5, 0xda, 0xd2, 0xbb, 0xd5, 0xc2} // 第一章
	s, ok := decodeGBK(raw)
	if !ok || s != "第一章" {
		t.Fatalf("gbk: %q ok=%v", s, ok)
	}
}
