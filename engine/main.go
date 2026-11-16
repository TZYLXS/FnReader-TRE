package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"fnparse/parse"
)

var version = "0.2"

const defaultConfigPath = "/etc/config/fnparse"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "run":
		err = cmdRun(args)
	case "once":
		err = cmdOnce(args)
	case "status":
		err = cmdStatus(args)
	case "test":
		err = cmdTest(args)
	case "usb":
		err = cmdUSB(args)
	case "clearcache":
		err = cmdClearCache(args)
	case "logs":
		err = cmdLogs(args)
	case "parse":
		err = cmdParse(args)
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令：%s\n\n", cmd)
		printUsage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`fnparse %s — FnReader 软总线解析引擎（OpenWrt/LEDE）

用法：
  fnparse run [-c 配置文件]        前台运行（procd 管理时由 init 脚本调用）
  fnparse once [-c 配置文件]       领一轮任务并处理第一个（调试）
  fnparse status [-c 配置文件]     输出运行状态 JSON（LuCI 用）
  fnparse test [-c 配置文件]       测试与服务端的连通性/令牌
  fnparse usb                      列出可用挂载点（USB 缓存选盘参考）
  fnparse clearcache [-all]        清理解析缓存（-all 清空，默认按 TTL）
  fnparse logs [-n 行数]           输出最近日志
  fnparse parse <文件> [-format]   本地解析一个文件并打印统计（调试）
  fnparse version                  版本号
`, version)
}

func bindConfig(fs *flag.FlagSet) *string {
	p := defaultConfigPath
	if runtime.GOOS == "windows" {
		p = "fnparse.conf"
	}
	return fs.String("c", p, "配置文件路径（UCI 格式）")
}

func loadCfgOrDie(path string) *Config {
	cfg, err := loadConfig(path)
	if err != nil {
		// 配置文件不存在时用默认值继续，方便先装后配
		fmt.Fprintf(os.Stderr, "读取配置 %s 失败：%v（使用默认值）\n", path, err)
	}
	return cfg
}

func resolveRoot(cfg *Config) string {
	c := newCache(cfg, newLogger("error", ""))
	if root, err := c.Resolve(); err == nil {
		return root
	}
	return cfg.CacheDir
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := bindConfig(fs)
	_ = fs.Parse(args)

	cfg := loadCfgOrDie(*cfgPath)
	if !cfg.Enabled {
		fmt.Fprintln(os.Stderr, "服务未启用（enabled=0），退出")
		return nil
	}
	logPath := defaultLogPath(resolveRoot(cfg))
	lg := newLogger(cfg.LogLevel, logPath)
	defer lg.Close()
	lg.Infof("日志文件：%s", logPath)

	eng := newEngine(cfg, lg, version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := eng.Run(ctx); err != nil {
		return err
	}
	lg.Infof("已退出")
	return nil
}

func cmdOnce(args []string) error {
	fs := flag.NewFlagSet("once", flag.ExitOnError)
	cfgPath := bindConfig(fs)
	_ = fs.Parse(args)
	cfg := loadCfgOrDie(*cfgPath)
	lg := newLogger("debug", defaultLogPath(resolveRoot(cfg)))
	defer lg.Close()
	eng := newEngine(cfg, lg, version)
	return eng.Once(context.Background())
}

type statusReport struct {
	Running     bool   `json:"running"`
	StateAgeSec int64  `json:"stateAgeSec"`
	StateFile   string `json:"stateFile"`
	ConfigPath  string `json:"configPath"`
	Mode        string `json:"mode"`
	*State
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	cfgPath := bindConfig(fs)
	_ = fs.Parse(args)

	cfg := loadCfgOrDie(*cfgPath)
	root := resolveRoot(cfg)
	rep := &statusReport{ConfigPath: *cfgPath, Mode: "standalone", State: &State{}}
	st, path := loadState(root)
	if st != nil {
		rep.State = st
		rep.StateFile = path
		age := time.Now().Unix() - st.UpdatedAt
		rep.StateAgeSec = age
		rep.Running = age > 0 && age < 90
	} else {
		rep.State = &State{}
	}
	if root != "" {
		c := newCache(cfg, newLogger("error", ""))
		c.setRoot(root)
		if used, free, err := diskUsage(root); err == nil {
			rep.State.CacheUsedMB = used >> 20
			rep.State.CacheFreeMB = free >> 20
		}
		rep.State.CacheDir = root
	}
	rep.State.Mounts = listMounts()
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	return nil
}

func cmdTest(args []string) error {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	cfgPath := bindConfig(fs)
	_ = fs.Parse(args)
	cfg := loadCfgOrDie(*cfgPath)
	if cfg.Server == "" {
		return fmt.Errorf("未配置服务端地址（接入IP）")
	}
	if cfg.Token == "" {
		return fmt.Errorf("未配置访问令牌（Token）")
	}
	root := resolveRoot(cfg)
	cl := newAPIClient(cfg, deriveDeviceID(root), "fnparse-probe", version)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	res := map[string]interface{}{"server": cfg.Server}
	info, err := cl.Info(ctx)
	if err != nil {
		res["ok"] = false
		res["error"] = err.Error()
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	res["ok"] = true
	res["info"] = info
	if bus, err := cl.Bus(ctx); err == nil {
		res["bus"] = bus
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
	return nil
}

func cmdUSB(args []string) error {
	fs := flag.NewFlagSet("usb", flag.ExitOnError)
	cfgPath := bindConfig(fs)
	_ = fs.Parse(args)
	cfg := loadCfgOrDie(*cfgPath)
	res := map[string]interface{}{
		"mounts": listMounts(),
	}
	if root, err := newCache(cfg, newLogger("error", "")).Resolve(); err == nil {
		res["cacheDir"] = root
	} else {
		res["cacheDirError"] = err.Error()
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
	return nil
}

func cmdClearCache(args []string) error {
	fs := flag.NewFlagSet("clearcache", flag.ExitOnError)
	all := fs.Bool("all", false, "清空全部缓存（默认只删过期文件）")
	cfgPath := bindConfig(fs)
	_ = fs.Parse(args)
	cfg := loadCfgOrDie(*cfgPath)
	c := newCache(cfg, newLogger("error", ""))
	if root, err := c.Resolve(); err == nil {
		c.setRoot(root)
	} else if cfg.CacheDir != "" {
		c.setRoot(cfg.CacheDir)
	} else {
		return err
	}
	removed, freed, err := c.Cleanup(*all)
	out, _ := json.MarshalIndent(map[string]interface{}{
		"ok":       err == nil,
		"cacheDir": c.Root(),
		"removed":  removed,
		"freedMB":  freed >> 20,
		"error":    errText(err),
	}, "", "  ")
	fmt.Println(string(out))
	return err
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	n := fs.Int("n", 100, "输出最近 N 行")
	asJSON := fs.Bool("json", false, "以 JSON 输出（LuCI/rpcd 用）")
	cfgPath := bindConfig(fs)
	_ = fs.Parse(args)
	cfg := loadCfgOrDie(*cfgPath)
	root := resolveRoot(cfg)
	paths := []string{}
	if st, _ := loadState(root); st != nil && st.LogPath != "" {
		paths = append(paths, st.LogPath)
	}
	if p := defaultLogPath(root); p != "" {
		paths = append(paths, p)
	}
	if runtime.GOOS != "windows" {
		paths = append(paths, "/var/log/fnparse.log")
	}
	for _, p := range paths {
		if txt, err := tailFile(p, *n); err == nil {
			if *asJSON {
				out, _ := json.Marshal(map[string]interface{}{"ok": true, "path": p, "log": txt})
				fmt.Println(string(out))
			} else {
				fmt.Println(txt)
			}
			return nil
		}
	}
	if *asJSON {
		out, _ := json.Marshal(map[string]interface{}{"ok": false, "error": "没有找到日志文件"})
		fmt.Println(string(out))
		return nil
	}
	return fmt.Errorf("没有找到日志文件")
}

func cmdParse(args []string) error {
	fs := flag.NewFlagSet("parse", flag.ExitOnError)
	format := fs.String("format", "", "强制格式（epub/mobi/pdf/txt），默认按扩展名")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("用法：fnparse parse <文件> [-format epub]")
	}
	file := fs.Arg(0)
	name := filepath.Base(file)
	start := time.Now()
	book, err := parse.ParseFile(file, *format, name)
	res := map[string]interface{}{
		"file":      file,
		"ok":        err == nil,
		"elapsedMs": time.Since(start).Milliseconds(),
	}
	if err != nil {
		res["error"] = err.Error()
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	res["format"] = book.Format
	res["title"] = book.Meta.Title
	res["author"] = book.Meta.Author
	res["chapters"] = len(book.Chapters)
	res["chars"] = book.Chars
	heads := make([]string, 0, 5)
	for i := 0; i < len(book.Chapters) && i < 5; i++ {
		heads = append(heads, book.Chapters[i].Title)
	}
	res["firstChapters"] = heads
	if len(book.Chapters) > 0 {
		first := book.Chapters[0]
		preview := ""
		if len(first.Paragraphs) > 0 {
			preview = truncate(first.Paragraphs[0].T, 120)
		}
		res["firstParagraph"] = preview
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
	return nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
