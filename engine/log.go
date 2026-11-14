package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type logLevel int

const (
	lvlDebug logLevel = iota
	lvlInfo
	lvlWarn
	lvlError
)

func parseLevel(s string) logLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return lvlDebug
	case "warn", "warning":
		return lvlWarn
	case "error":
		return lvlError
	default:
		return lvlInfo
	}
}

func (l logLevel) String() string {
	switch l {
	case lvlDebug:
		return "DEBUG"
	case lvlWarn:
		return "WARN"
	case lvlError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// Logger 同时写 stdout（procd 收集进 logread）和文件（LuCI 查看用）。
type Logger struct {
	mu       sync.Mutex
	level    logLevel
	path     string
	f        *os.File
	maxBytes int64
}

func newLogger(level, path string) *Logger {
	l := &Logger{level: parseLevel(level), maxBytes: 8 << 20}
	l.path = path
	if path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			l.f = f
		}
	}
	return l
}

func (l *Logger) Path() string { return l.path }

func (l *Logger) SetPath(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
	l.path = path
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		l.f = f
	}
}

func (l *Logger) rotateLocked() {
	if l.f == nil || l.maxBytes <= 0 {
		return
	}
	st, err := l.f.Stat()
	if err != nil || st.Size() < l.maxBytes {
		return
	}
	l.f.Close()
	l.f = nil
	_ = os.Remove(l.path + ".1")
	_ = os.Rename(l.path, l.path+".1")
	if f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		l.f = f
	}
}

func (l *Logger) logf(lv logLevel, format string, args ...interface{}) {
	if lv < l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	line := time.Now().Format("2006-01-02 15:04:05") + " [" + lv.String() + "] " + msg
	// 控制字符会污染 LuCI 日志视图
	line = strings.ReplaceAll(line, "\x00", "")
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(os.Stdout, line)
	if l.f != nil {
		l.rotateLocked()
		if l.f != nil {
			fmt.Fprintln(l.f, line)
			_ = l.f.Sync()
		}
	}
}

func (l *Logger) Debugf(f string, a ...interface{}) { l.logf(lvlDebug, f, a...) }
func (l *Logger) Infof(f string, a ...interface{})  { l.logf(lvlInfo, f, a...) }
func (l *Logger) Warnf(f string, a ...interface{})  { l.logf(lvlWarn, f, a...) }
func (l *Logger) Errorf(f string, a ...interface{}) { l.logf(lvlError, f, a...) }

func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}

// defaultLogPath：优先 /var/log（掉电不占 Flash），否则放缓存目录。
func defaultLogPath(cacheRoot string) string {
	if runtime.GOOS != "windows" {
		if dirWritable("/var/log") {
			return "/var/log/fnparse.log"
		}
	}
	if cacheRoot != "" {
		return filepath.Join(cacheRoot, "fnparse.log")
	}
	return ""
}

func dirWritable(dir string) bool {
	if dir == "" {
		return false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".fnparse-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

func tailFile(path string, n int) (string, error) {
	if n <= 0 {
		n = 100
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}
