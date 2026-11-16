package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchBookPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><head><meta property="og:image" content="/c.jpg"></head><body>ok</body></html>`))
	}))
	defer srv.Close()

	page, finalURL, err := fetchBookPage(context.Background(), srv.URL+"/book/1")
	if err != nil {
		t.Fatal(err)
	}
	if finalURL == nil || finalURL.Path != "/book/1" {
		t.Fatalf("finalURL = %v", finalURL)
	}
	if page == "" || finalURL.Host == "" {
		t.Fatalf("page empty or host missing: %q %v", page, finalURL)
	}
}

func TestRunMediaTaskCoverAndIntro(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><head>
<meta property="og:image" content="/img/c.jpg">
<meta property="og:description" content="一段测试简介，长度足够用于媒体任务。">
<meta name="author" content="测试作者">
</head><body></body></html>`))
	}))
	defer srv.Close()

	e := &Engine{log: newLogger("error", "")}
	raw, _ := json.Marshal(mediaPayload{BookURL: srv.URL + "/book/1"})

	data, err := e.runMediaTask(context.Background(), "cover", raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := data.(map[string]interface{})
	if !ok || m["coverUrl"] != srv.URL+"/img/c.jpg" {
		t.Fatalf("cover result = %#v", data)
	}

	data, err = e.runMediaTask(context.Background(), "intro", raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok = data.(map[string]interface{})
	if !ok || m["intro"] == "" || m["author"] != "测试作者" {
		t.Fatalf("intro result = %#v", data)
	}
}

func TestResultError(t *testing.T) {
	cases := []struct {
		ok       interface{}
		accepted interface{}
		wantErr  bool
	}{
		{true, true, false},
		{true, false, false},
		{false, false, true},
		{"duplicate", "duplicate", false},
		{"stale", "stale", false},
		{"ignored", false, false},
		{"weird", false, true},
		{nil, false, true},
	}
	for _, c := range cases {
		err := resultError(c.ok, c.accepted)
		if (err != nil) != c.wantErr {
			t.Fatalf("resultError(%v,%v) err=%v wantErr=%v", c.ok, c.accepted, err, c.wantErr)
		}
	}
}
