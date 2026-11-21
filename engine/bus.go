package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// 软总线领取策略（写死，不随配置变化）：
//   - busWorkers = 1：固定单线程领取/处理
//   - pendingLimit = 1：每次只领一个任务
//   - 只承接 media 类（cover/intro/bookinfo），其余已领取任务立即回传失败
//
// 关于「隐藏分最高 50」：服务端隐藏分由 server.js 内部维护（成功 +4 / 失败 −8），
// 客户端没有字段可以直接覆盖，因此无法把分数真正写死。插件用两个手段实现等效效果：
//   1) 只领 media 类——即使服务端误派 book/content，也立即回传失败（−8 分），
//      分数涨上去后会被打回来，无法稳定维持高分；
//   2) 单线程 + 每次只领 1 个，减少成功加分频率。
// 因此插件实际只会停留在低分档（<55，media only），等同于「隐藏分封顶 50」。
const (
	busWorkers     = 1
	busQueueCap    = 4
	hiddenScoreCap = 50
)

var mediaTaskTypes = map[string]bool{
	"cover":    true,
	"intro":    true,
	"bookinfo": true,
}

type Engine struct {
	cfg     *Config
	log     *Logger
	api     *apiClient
	cache   *Cache
	st      *stateStore
	sources *sourceRepo
	version string

	jobs chan busJob

	mu    sync.Mutex
	inIDs map[string]bool
	inSha map[string]bool
}

func newEngine(cfg *Config, log *Logger, version string) *Engine {
	devName := cfg.DeviceName
	if devName == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "openwrt"
		}
		devName = "OpenWRT-" + host
	}
	e := &Engine{
		cfg:     cfg,
		log:     log,
		cache:   newCache(cfg, log),
		sources: newSourceRepo(),
		version: version,
		jobs:    make(chan busJob, busQueueCap),
		inIDs:   map[string]bool{},
		inSha:   map[string]bool{},
	}
	e.api = newAPIClient(cfg, deriveDeviceID(""), devName, version)
	e.st = newStateStore("")
	return e
}

func shaFromPayload(raw json.RawMessage) string {
	var p struct {
		Sha1 string `json:"sha1"`
	}
	_ = json.Unmarshal(raw, &p)
	return strings.ToLower(p.Sha1)
}

// Run 阻塞运行直到 ctx 取消。
func (e *Engine) Run(ctx context.Context) error {
	e.mu.Lock()
	e.inSha = map[string]bool{}
	e.mu.Unlock()

	e.log.Infof("fnparse %s 启动：server=%s dev=%s", e.version, e.cfg.Server, e.api.devName)
	if e.cfg.Server == "" {
		e.log.Errorf("未配置服务端地址（接入IP），请在 LuCI 里填写")
	}
	if e.cfg.Token == "" {
		e.log.Errorf("未配置访问令牌（Token），任务将无法领取")
	}

	// 设备 id 需要缓存目录（随机兜底时落盘），先尝试解析
	if root, err := e.cache.Resolve(); err == nil {
		e.cache.setRoot(root)
		e.api.devID = deriveDeviceID(root)
	} else {
		e.api.devID = deriveDeviceID("")
	}
	e.log.Infof("设备标识 %s（%s）", e.api.devID, e.api.devName)

	e.st = newStateStore(stateFilePath(e.cache.Root()))
	e.st.update(func(s *State) {
		s.PID = os.Getpid()
		s.StartedAt = time.Now().Unix()
		s.Version = e.version
		s.DeviceID = e.api.devID
		s.DeviceName = e.api.devName
		s.Server = e.cfg.Server
		s.TokenSet = e.cfg.Token != ""
		s.LogPath = e.log.Path()
	})

	for i := 0; i < busWorkers; i++ {
		go e.worker(ctx, i)
	}

	go e.periodic(ctx)

	var backoff time.Duration
	for ctx.Err() == nil {
		if !e.ready() {
			time.Sleep(10 * time.Second)
			continue
		}
		wait := e.cfg.PollWait
		if wait < 0 {
			wait = 0
		}
		if wait > 30 {
			wait = 30
		}
		pollCtx, cancel := context.WithTimeout(ctx, time.Duration(wait+50)*time.Second)
		jobs, err := e.api.pending(pollCtx, wait)
		cancel()
		if err != nil {
			e.st.update(func(s *State) {
				s.ServerOK = false
				s.LastError = err.Error()
				s.LastErrorAt = time.Now().Unix()
			})
			e.log.Warnf("领取任务失败：%v", err)
			if backoff == 0 {
				backoff = 2 * time.Second
			} else if backoff < 60*time.Second {
				backoff *= 2
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			continue
		}
		backoff = 0
		e.st.update(func(s *State) {
			s.ServerOK = true
			s.LastError = ""
		})
		for _, j := range jobs {
			e.enqueue(j)
		}
		if len(jobs) > 0 {
			// 任务还在队列里时避免空转（服务端会把未完成任务重复下发）
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}

func (e *Engine) ready() bool {
	root := e.cache.Root()
	if root == "" {
		r, err := e.cache.Resolve()
		if err != nil {
			e.st.update(func(s *State) {
				s.CacheReady = false
				s.LastError = err.Error()
				s.LastErrorAt = time.Now().Unix()
				s.Mounts = listMounts()
			})
			e.log.Warnf("缓存目录未就绪：%v", err)
			return false
		}
		e.cache.setRoot(r)
		e.api.devID = deriveDeviceID(r)
		e.st.update(func(s *State) { s.DeviceID = e.api.devID })
		if e.log.Path() == "" || !strings.Contains(e.log.Path(), "/var/log/") {
			e.log.SetPath(defaultLogPath(r))
		}
		e.st.update(func(s *State) { s.LogPath = e.log.Path() })
	}
	if err := e.cache.Ensure(); err != nil {
		e.st.update(func(s *State) {
			s.CacheReady = false
			s.LastError = err.Error()
			s.LastErrorAt = time.Now().Unix()
		})
		e.log.Warnf("缓存目录不可用：%v", err)
		return false
	}
	e.sources.setRoot(e.cache.Root())
	e.st.update(func(s *State) {
		s.CacheReady = true
		s.CacheDir = e.cache.Root()
	})
	e.log.Infof("解析缓存目录：%s", e.cache.Root())
	return true
}

func (e *Engine) enqueue(j busJob) {
	if !mediaTaskTypes[j.Type] {
		// 插件只承接简介/封面：非 media 类任务立即回传失败。
		// 服务端会按「失败 −8 分」调低隐藏分并自己兜底，等效把分数压回 50 以下。
		e.log.Infof("拒绝非媒体任务 %s type=%s class=%s，立即回传 unsupported（隐藏分上限 %d）",
			j.ID, j.Type, j.TaskClass, hiddenScoreCap)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := e.api.postResult(ctx, j.ID, false, nil, "unsupported"); err != nil {
				e.log.Warnf("unsupported 回传失败 %s：%v", j.ID, err)
			}
		}()
		return
	}
	if !e.cfg.claims(j.Type) {
		e.log.Debugf("跳过非本机任务类型 %s（%s）", j.Type, j.ID)
		return
	}
	sha := shaFromPayload(j.Payload)
	e.mu.Lock()
	if e.inIDs[j.ID] {
		e.mu.Unlock()
		return
	}
	if j.Type == "ebook-parse" && sha != "" && e.inSha[sha] {
		e.mu.Unlock()
		e.log.Debugf("同一本书已有任务在跑，跳过 %s", sha)
		return
	}
	e.inIDs[j.ID] = true
	if sha != "" {
		e.inSha[sha] = true
	}
	e.mu.Unlock()

	select {
	case e.jobs <- j:
		e.st.update(func(s *State) { s.Inflight = len(e.jobs) })
	default:
		e.release(j, sha)
		e.log.Warnf("任务队列已满，让服务端重派 %s（%s）", j.Type, j.ID)
	}
}

func (e *Engine) release(j busJob, sha string) {
	e.mu.Lock()
	delete(e.inIDs, j.ID)
	if sha != "" {
		delete(e.inSha, sha)
	}
	e.mu.Unlock()
	e.st.update(func(s *State) { s.Inflight = len(e.jobs) })
}

func (e *Engine) worker(ctx context.Context, idx int) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-e.jobs:
			e.handle(ctx, job)
		}
	}
}

func (e *Engine) handle(parent context.Context, job busJob) {
	sha := shaFromPayload(job.Payload)
	start := time.Now()
	e.log.Infof("开始任务 %s type=%s class=%s sha=%s", job.ID, job.Type, job.TaskClass, sha)

	timeout := 12 * time.Minute
	switch job.Type {
	case "ebook-chapter":
		timeout = 3 * time.Minute
	case "cover", "intro", "bookinfo":
		timeout = 45 * time.Second
	}
	// 服务端 2.6.19 起按「任务类 + 设备实测」下发执行预算（timeoutMs）。
	// 有预算就按预算收手：否则服务端早已判超时、我们还在跑，回传只能拿到 stale。
	if job.TimeoutMs > 0 {
		budget := time.Duration(job.TimeoutMs)*time.Millisecond - 300*time.Millisecond
		if budget < 3*time.Second {
			budget = 3 * time.Second
		}
		// 长任务保底：整本电子书不可能在服务端 30 秒的 ebook 基线下跑完，
		// 但 ebook-parse 的 waitMs 是 600 秒，3 分钟内回传不会被判 stale。
		if job.Type == "ebook-parse" && budget < 3*time.Minute {
			budget = 3 * time.Minute
		}
		if job.Type == "ebook-chapter" && budget < 30*time.Second {
			budget = 30 * time.Second
		}
		timeout = budget
	}
	e.log.Infof("任务 %s 执行预算 %s（服务端 timeoutMs=%d）", job.ID, timeout, job.TimeoutMs)
	jobCtx, cancel := context.WithTimeout(parent, timeout)
	var data interface{}
	var err error
	switch job.Type {
	case "ebook-parse":
		data, err = e.runEbookParse(jobCtx, job, job.Payload)
	case "ebook-chapter":
		data, err = e.runEbookChapter(jobCtx, job, job.Payload)
	case "cover", "intro", "bookinfo":
		data, err = e.runMediaTask(jobCtx, job.Type, job.Payload)
	default:
		err = &parseErrorUnsupported{job.Type}
	}
	cancel()

	// 回结果用独立 ctx，避免任务超时后连结果都发不出去
	resCtx, resCancel := context.WithTimeout(context.Background(), 45*time.Second)
	var submitErr error
	if err == nil {
		submitErr = e.api.postResult(resCtx, job.ID, true, data, "")
	} else {
		code := jobErrorCode(err)
		e.log.Errorf("任务失败 %s type=%s：%v（回传 %s）", job.ID, job.Type, err, code)
		submitErr = e.api.postResult(resCtx, job.ID, false, nil, code)
	}
	resCancel()
	if submitErr != nil {
		e.log.Warnf("任务结果回传失败 %s：%v", job.ID, submitErr)
	}

	info := &JobInfo{
		At:   time.Now().Unix(),
		Type: job.Type,
		Sha1: sha,
		OK:   err == nil,
		MS:   time.Since(start).Milliseconds(),
	}
	if p, perr := payloadSummary(job.Payload); perr == nil {
		info.Name = p.Name
		info.Format = p.Format
	}
	if err != nil {
		info.Note = jobErrorCode(err)
	}
	e.st.update(func(s *State) {
		s.Last = info
		if err == nil {
			s.Done++
		} else {
			s.Failed++
		}
		s.Inflight = len(e.jobs)
	})
	e.release(job, sha)
	e.log.Infof("任务结束 %s ok=%v 用时 %d ms", job.ID, err == nil, time.Since(start).Milliseconds())
}

type parseErrorUnsupported struct{ typ string }

func (p *parseErrorUnsupported) Error() string {
	return "本机不处理任务类型：" + p.typ
}

func payloadSummary(raw json.RawMessage) (ebookPayload, error) {
	var p ebookPayload
	err := json.Unmarshal(raw, &p)
	return p, err
}

// periodic：状态落盘、缓存清理、缓存目录/挂载点刷新。
func (e *Engine) periodic(ctx context.Context) {
	statusTick := time.NewTicker(5 * time.Second)
	usageTick := time.NewTicker(30 * time.Second)
	cleanTick := time.NewTicker(1 * time.Hour)
	defer statusTick.Stop()
	defer usageTick.Stop()
	defer cleanTick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-statusTick.C:
			_ = e.st.save()
		case <-usageTick.C:
			if root := e.cache.Root(); root != "" {
				used, free := e.cache.Usage()
				e.st.update(func(s *State) {
					s.CacheDir = root
					s.CacheUsedMB = used
					s.CacheFreeMB = free
					s.Mounts = listMounts()
				})
			}
		case <-cleanTick.C:
			if e.cache.Root() != "" {
				removed, freed, err := e.cache.Cleanup(false)
				if err == nil && removed > 0 {
					e.log.Infof("清理缓存：%d 个文件，释放 %d MB", removed, freed>>20)
				}
			}
		}
	}
}

// Once 供 CLI 调试：领一轮任务并同步处理第一个。
func (e *Engine) Once(ctx context.Context) error {
	if _, err := e.cache.Resolve(); err != nil {
		return err
	}
	if err := e.cache.Ensure(); err != nil {
		return err
	}
	e.api.devID = deriveDeviceID(e.cache.Root())
	jobs, err := e.api.pending(ctx, 0)
	if err != nil {
		return err
	}
	fmt.Printf("pending jobs: %d\n", len(jobs))
	for _, j := range jobs {
		fmt.Printf("- %s type=%s age=%dms payload=%s\n", j.ID, j.Type, j.Age, truncate(string(j.Payload), 160))
	}
	if len(jobs) == 0 {
		return nil
	}
	job := jobs[0]
	if !e.cfg.claims(job.Type) {
		return fmt.Errorf("第一个任务类型 %s 不在 claim_types 内", job.Type)
	}
	e.handle(ctx, job)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
