package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fnparse/parse"
)

// sourceRepo 缓存服务端书源列表（/api/m/sources.json），给 cover/intro/bookinfo
// 任务提供 ruleBookInfo 规则。磁盘缓存 10 分钟 TTL，网络失败时继续用旧缓存。
const sourceCacheFile = "sources.json"
const sourceCacheTTL = 10 * time.Minute

type sourceRepo struct {
	mu       sync.Mutex
	path     string
	loadedAt time.Time
	list     []parse.BookSource
}

func newSourceRepo() *sourceRepo {
	return &sourceRepo{}
}

// bookSourceRule 取 payload.source（bookSourceUrl）对应的书源详情规则。
func (e *Engine) bookSourceRule(ctx context.Context, sourceURL string) *parse.BookInfoRule {
	if e.sources == nil || e.api == nil {
		return nil
	}
	return e.sources.ruleFor(ctx, e.api, sourceURL)
}

func (r *sourceRepo) setRoot(root string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if root == "" {
		r.path = ""
		return
	}
	r.path = filepath.Join(root, sourceCacheFile)
}

// ruleFor 返回指定 bookSourceUrl 的 ruleBookInfo；缓存过期时同步刷新。
// 引擎只有 1 个任务线程，串行调用，持锁刷新不会产生竞争。
func (r *sourceRepo) ruleFor(ctx context.Context, cl *apiClient, sourceURL string) *parse.BookInfoRule {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.loadedAt) > sourceCacheTTL || len(r.list) == 0 {
		r.refreshLocked(ctx, cl)
	}
	for i := range r.list {
		if r.list[i].URL == sourceURL {
			return r.list[i].Rule
		}
	}
	return nil
}

func (r *sourceRepo) refreshLocked(ctx context.Context, cl *apiClient) {
	// 磁盘缓存兜底：先读旧列表，保证离线也能解析
	if r.path != "" {
		if b, err := os.ReadFile(r.path); err == nil {
			if list := parse.ParseBookSourceList(b); len(list) > 0 {
				r.list = list
				r.loadedAt = time.Now()
			}
		}
	}
	if cl == nil {
		return
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	b, err := cl.fetchSources(fetchCtx)
	if err != nil {
		return
	}
	list := parse.ParseBookSourceList(b)
	if len(list) == 0 {
		return
	}
	r.list = list
	r.loadedAt = time.Now()
	if r.path != "" {
		if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err == nil {
			_ = os.WriteFile(r.path, b, 0o644)
		}
	}
}
