package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Config 对应 /etc/config/fnparse（UCI 格式，纯文本解析，不依赖 libuci）。
type Config struct {
	Enabled     bool
	Server      string
	Token       string
	CacheDir    string
	USBAuto     bool
	Concurrency int
	PollWait    int
	PartMaxMB   int
	CacheMaxMB  int
	TmpTTLHours int
	DeviceName  string
	LogLevel    string
	ClaimTypes  []string
}

func defaultConfig() *Config {
	return &Config{
		Enabled:     true,
		USBAuto:     true,
		Concurrency: 2,
		PollWait:    20,
		PartMaxMB:   2,
		CacheMaxMB:  2048,
		TmpTTLHours: 48,
		LogLevel:    "info",
		ClaimTypes:  []string{"ebook-parse", "ebook-chapter", "cover", "intro", "bookinfo"},
	}
}

func loadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()

	section := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := splitUCI(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "config":
			// config fnparse 'main'
			if len(fields) >= 3 {
				section = strings.Trim(fields[2], "'\"")
			} else if len(fields) >= 2 {
				section = fields[1]
			}
		case "option":
			if len(fields) < 3 {
				continue
			}
			if section != "" && section != "main" {
				continue
			}
			applyOption(cfg, strings.Trim(fields[1], "'\""), strings.Trim(fields[2], "'\""))
		}
	}
	if err := sc.Err(); err != nil {
		return cfg, err
	}
	cfg.Server = normalizeServer(cfg.Server)
	if len(cfg.ClaimTypes) == 0 {
		cfg.ClaimTypes = []string{"ebook-parse"}
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Concurrency > 8 {
		cfg.Concurrency = 8
	}
	return cfg, nil
}

// splitUCI 按 shell 风格切分：'单引号'、"双引号"、裸值。
func splitUCI(line string) []string {
	var out []string
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		q := line[i]
		if q == '"' || q == '\'' {
			i++
			start := i
			for i < len(line) && line[i] != q {
				i++
			}
			out = append(out, line[start:i])
			if i < len(line) {
				i++
			}
			continue
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		out = append(out, line[start:i])
	}
	return out
}

func applyOption(cfg *Config, key, val string) {
	switch key {
	case "enabled":
		cfg.Enabled = parseBool(val, true)
	case "server":
		cfg.Server = val
	case "token":
		cfg.Token = strings.TrimSpace(val)
	case "cache_dir":
		cfg.CacheDir = strings.TrimSpace(val)
	case "usb_autodetect":
		cfg.USBAuto = parseBool(val, true)
	case "concurrency":
		cfg.Concurrency = parseInt(val, cfg.Concurrency)
	case "poll_wait":
		cfg.PollWait = parseInt(val, cfg.PollWait)
	case "part_max_mb":
		cfg.PartMaxMB = parseInt(val, cfg.PartMaxMB)
	case "cache_max_mb":
		cfg.CacheMaxMB = parseInt(val, cfg.CacheMaxMB)
	case "tmp_ttl_hours":
		cfg.TmpTTLHours = parseInt(val, cfg.TmpTTLHours)
	case "device_name":
		cfg.DeviceName = strings.TrimSpace(val)
	case "log_level":
		cfg.LogLevel = strings.ToLower(strings.TrimSpace(val))
	case "claim_types":
		parts := strings.Split(val, ",")
		cfg.ClaimTypes = cfg.ClaimTypes[:0]
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				cfg.ClaimTypes = append(cfg.ClaimTypes, t)
			}
		}
	}
}

func parseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true
	case "0", "false", "no", "off", "disable", "disabled":
		return false
	}
	return def
}

func parseInt(s string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return v
}

func normalizeServer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return strings.TrimRight(s, "/")
}

func (c *Config) claims(t string) bool {
	for _, v := range c.ClaimTypes {
		if v == t {
			return true
		}
	}
	return false
}

func (c *Config) partBytes() int {
	mb := c.PartMaxMB
	if mb < 1 {
		mb = 1
	}
	if mb > 8 {
		mb = 8
	}
	return mb * 1024 * 1024
}
