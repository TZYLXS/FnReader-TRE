package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type JobInfo struct {
	At     int64  `json:"at"`
	Type   string `json:"type"`
	Name   string `json:"name,omitempty"`
	Sha1   string `json:"sha1,omitempty"`
	Format string `json:"format,omitempty"`
	OK     bool   `json:"ok"`
	MS     int64  `json:"ms"`
	Note   string `json:"note,omitempty"`
}

// State 是引擎对外的状态快照，fnparse status 和 LuCI 都读它。
type State struct {
	UpdatedAt   int64    `json:"updatedAt"`
	PID         int      `json:"pid"`
	StartedAt   int64    `json:"startedAt"`
	Version     string   `json:"version"`
	DeviceID    string   `json:"deviceId"`
	DeviceName  string   `json:"deviceName"`
	Server      string   `json:"server"`
	TokenSet    bool     `json:"tokenSet"`
	CacheDir    string   `json:"cacheDir"`
	CacheReady  bool     `json:"cacheReady"`
	CacheUsedMB int64    `json:"cacheUsedMB"`
	CacheFreeMB int64    `json:"cacheFreeMB"`
	Inflight    int      `json:"inflight"`
	Done        int      `json:"done"`
	Failed      int      `json:"failed"`
	Last        *JobInfo `json:"last,omitempty"`
	LastError   string   `json:"lastError,omitempty"`
	LastErrorAt int64    `json:"lastErrorAt,omitempty"`
	ServerOK    bool     `json:"serverOk"`
	LogPath     string   `json:"logPath,omitempty"`
	Mounts      []Mount  `json:"mounts,omitempty"`
}

type stateStore struct {
	mu   sync.Mutex
	path string
	s    State
}

func newStateStore(path string) *stateStore {
	return &stateStore{path: path}
}

func (st *stateStore) update(fn func(*State)) {
	st.mu.Lock()
	fn(&st.s)
	st.s.UpdatedAt = time.Now().Unix()
	st.mu.Unlock()
	_ = st.save()
}

func (st *stateStore) apply(fn func(*State)) {
	st.update(fn)
}

func (st *stateStore) snap() State {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

func (st *stateStore) save() error {
	st.mu.Lock()
	data, err := json.MarshalIndent(st.s, "", "  ")
	path := st.path
	updated := st.s.UpdatedAt
	st.mu.Unlock()
	if err != nil {
		return err
	}
	if path == "" || updated == 0 {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func stateFilePath(cacheRoot string) string {
	if runtime.GOOS != "windows" && dirWritable("/var/run") {
		return "/var/run/fnparse.state.json"
	}
	if cacheRoot != "" {
		return filepath.Join(cacheRoot, "state.json")
	}
	return filepath.Join(os.TempDir(), "fnparse.state.json")
}

// loadState 依次尝试运行时目录、缓存目录里的状态文件。
func loadState(cacheRoot string) (*State, string) {
	candidates := []string{}
	if runtime.GOOS != "windows" {
		candidates = append(candidates, "/var/run/fnparse.state.json")
	}
	if cacheRoot != "" {
		candidates = append(candidates, filepath.Join(cacheRoot, "state.json"))
	}
	candidates = append(candidates, filepath.Join(os.TempDir(), "fnparse.state.json"))
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var s State
		if json.Unmarshal(b, &s) == nil {
			return &s, p
		}
	}
	return nil, ""
}
