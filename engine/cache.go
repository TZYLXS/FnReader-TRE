package main

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Mount 用于 LuCI/USB 检测展示。
type Mount struct {
	Device   string `json:"device"`
	Mount    string `json:"mount"`
	FSType   string `json:"fstype"`
	USB      bool   `json:"usb"`
	Writable bool   `json:"writable"`
	FreeMB   int64  `json:"freeMB"`
}

type Cache struct {
	cfg  *Config
	log  *Logger
	mu   sync.Mutex
	root string
}

func newCache(cfg *Config, lg *Logger) *Cache {
	return &Cache{cfg: cfg, log: lg}
}

func (c *Cache) Root() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.root
}

// Resolve 决定缓存根目录：显式配置优先（且必须已挂载），否则自动找 USB。
func (c *Cache) Resolve() (string, error) {
	cfg := c.cfg
	if cfg.CacheDir != "" && !cfg.USBAuto {
		c.setRoot(cfg.CacheDir)
		return c.root, nil
	}
	if cfg.CacheDir != "" && cfg.USBAuto && underMountedPath(cfg.CacheDir) && dirWritable(cfg.CacheDir) {
		c.setRoot(cfg.CacheDir)
		return c.root, nil
	}
	if !cfg.USBAuto {
		return "", errors.New("缓存目录未配置")
	}
	mounts := listMounts()
	for _, m := range mounts {
		if m.USB && m.Writable {
			root := filepath.Join(m.Mount, "fnparse")
			if dirWritable(root) {
				c.setRoot(root)
				return root, nil
			}
		}
	}
	return "", errors.New("未找到可用 USB 存储：请插入 U 盘，或在设置里关闭自动检测并指定缓存目录")
}

func (c *Cache) setRoot(p string) {
	c.mu.Lock()
	c.root = p
	c.mu.Unlock()
}

func (c *Cache) Ensure() error {
	root := c.Root()
	if root == "" {
		return errors.New("缓存目录未就绪")
	}
	for _, sub := range []string{"", "download", "parsed"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return err
		}
	}
	if !dirWritable(root) {
		return errors.New("缓存目录不可写：" + root)
	}
	return nil
}

func (c *Cache) DownloadPath(sha1, format string) string {
	if format == "" {
		format = "bin"
	}
	return filepath.Join(c.Root(), "download", safeName(sha1)+"."+safeName(format))
}

func (c *Cache) ParsedPath(sha1 string) string {
	return filepath.Join(c.Root(), "parsed", safeName(sha1)+".json.gz")
}

func (c *Cache) TempPath(sha1, ext string) string {
	return filepath.Join(c.Root(), "download", safeName(sha1)+"."+safeName(ext)+".part")
}

func (c *Cache) Usage() (usedMB, freeMB int64) {
	root := c.Root()
	if root == "" {
		return 0, 0
	}
	used, free, err := diskUsage(root)
	if err != nil {
		return 0, 0
	}
	return used >> 20, free >> 20
}

// Cleanup 删除过期的原文件与解析缓存；all=true 全清。
func (c *Cache) Cleanup(all bool) (removedFiles int, freedBytes int64, err error) {
	root := c.Root()
	if root == "" {
		return 0, 0, errors.New("缓存目录未就绪")
	}
	ttl := time.Duration(c.cfg.TmpTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	now := time.Now()
	dirs := []string{filepath.Join(root, "download"), filepath.Join(root, "parsed")}
	for _, dir := range dirs {
		ents, e := os.ReadDir(dir)
		if e != nil {
			continue
		}
		for _, ent := range ents {
			if ent.IsDir() {
				continue
			}
			info, e2 := ent.Info()
			if e2 != nil {
				continue
			}
			if !all && now.Sub(info.ModTime()) < ttl {
				continue
			}
			p := filepath.Join(dir, ent.Name())
			sz := info.Size()
			if e3 := os.Remove(p); e3 == nil {
				removedFiles++
				freedBytes += sz
			}
		}
	}
	c.enforceLimit()
	return removedFiles, freedBytes, nil
}

// enforceLimit 超过 cache_max_mb 时按最旧优先删解析缓存。
func (c *Cache) enforceLimit() {
	maxBytes := int64(c.cfg.CacheMaxMB) << 20
	if maxBytes <= 0 {
		return
	}
	used, _, err := diskUsage(c.Root())
	if err != nil || used <= maxBytes {
		return
	}
	type fileInfo struct {
		path string
		size int64
		mod  time.Time
	}
	dir := filepath.Join(c.Root(), "parsed")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	files := make([]fileInfo, 0, len(ents))
	for _, ent := range ents {
		info, e := ent.Info()
		if e != nil || ent.IsDir() {
			continue
		}
		files = append(files, fileInfo{filepath.Join(dir, ent.Name()), info.Size(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if used <= maxBytes {
			break
		}
		if err := os.Remove(f.path); err == nil {
			used -= f.size
			c.log.Debugf("清理缓存 %s (%d KB)", filepath.Base(f.path), f.size>>10)
		}
	}
	if used > maxBytes {
		c.log.Warnf("解析缓存仍超过上限（%d MB），可手动清理", c.cfg.CacheMaxMB)
	}
}

func safeName(s string) string {
	if s == "" {
		return "x"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func underMountedPath(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	for _, m := range listMounts() {
		if !m.Writable {
			continue
		}
		if m.Mount == "/" {
			continue
		}
		prefix := strings.TrimRight(m.Mount, "/")
		if abs == prefix || strings.HasPrefix(abs, prefix+"/") {
			return true
		}
	}
	return false
}
