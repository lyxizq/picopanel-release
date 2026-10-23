package agent

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"lz-monitor/internal/agent/collect"
	"lz-monitor/internal/logutil"
	"lz-monitor/internal/proto"
	"lz-monitor/internal/update"
)

// snapshot 保存最近一次采集结果，供指标端口并发读取。
type snapshot struct {
	mu  sync.RWMutex
	rep proto.Report
}

func (s *snapshot) store(r *proto.Report) {
	s.mu.Lock()
	s.rep = *r
	s.mu.Unlock()
}

func (s *snapshot) load() proto.Report {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rep
}

// Run 启动采集，按配置模式输出数据，阻塞直至收到退出信号：
//   - pull（缺省）：本地周期采集 + 只读指标端口，GET /metrics 返回最近快照，
//     POST /upgrade 接受服务端在线升级推送（token 必配，396）；
//   - push：本地周期采集后主动 POST 上报服务端（agent 需知道服务端地址），
//     响应捎带升级指令时回源下载应用。
//
// CPU/网卡是差值采集器，两种模式都必须持续采样。rt 带构建版本串与验签公钥
// （版本随 Sys 上报；公钥用于升级束验签）。健康自检（395⑧）：pull 绑定指标
// 端口成功、push 完成初始化即清除升级标记；初始化失败由 main 凭标记回滚。
func Run(cfg *Config, rt Runtime) error {
	// 日志三种形态：log_file="off" 零日志（文件与终端都不写，彻底无磁盘写入）；
	// 置空串仅终端；其他值 = 滚动日志文件 + 终端双写（nohup 重定向到同名文件时自动只留终端）
	if strings.EqualFold(cfg.LogFile, "off") {
		log.SetOutput(io.Discard)
	} else {
		log.SetOutput(logutil.New(cfg.LogFile, int64(cfg.LogMaxMB)<<20, logutil.Backups))
	}
	marker := update.DefaultMarkerPath()
	if update.MarkerExists(marker) {
		log.Printf("检测到升级标记：本次为升级后启动，健康自检通过后自动清除")
	}
	log.Printf("lz-agent 启动：%s version=%s", cfg, rt.Version)

	collectors := []collect.Collector{
		collect.NewHostCollector(),
		collect.NewCPUCollector(),
		collect.NewMemCollector(),
		collect.NewLoadCollector(),
		collect.NewDiskCollector(),
		collect.NewNetCollector(),
		collect.NewTCPCollector(),
		collect.NewThermalCollector(),
	}
	// 监听端口可选采集：collect_listens: false 时不读端口也不上报，
	// 与旧版 agent（无此能力）在服务端同语义——端口列表与端口告警停更
	if cfg.ListensEnabled() {
		collectors = append(collectors, collect.NewListenCollector())
	}
	// 应用负载快照（54）：Docker 容器 + 虚拟机列表（能力缺失自动留空）
	collectors = append(collectors, collect.NewAppsCollector())

	// 启动先采一次基线（差值采集器建立初始计数，基线本身不对外提供）
	runCollectors(collectors, &proto.Report{})

	snap := &snapshot{}
	collectOnce := func() *proto.Report {
		r := &proto.Report{TS: time.Now().Unix()}
		runCollectors(collectors, r)
		if r.Sys != nil {
			r.Sys.AgentVersion = rt.Version // 396：服务端按此列展示与判定升级
		}
		snap.store(r)
		return r
	}
	collectOnce()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.Mode == "push" {
		return runPush(ctx, cfg, rt, collectOnce)
	}
	return runPull(ctx, cfg, rt, collectOnce, snap)
}

// runPull 启动指标端口等待服务端抓取，同时周期刷新快照。
func runPull(ctx context.Context, cfg *Config, rt Runtime, collectOnce func() *proto.Report, snap *snapshot) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// 可选共享密钥：配置了 token 则要求抓取方携带相同 X-Server-Token
		if cfg.Token != "" {
			got := r.Header.Get("X-Server-Token")
			if subtle.ConstantTimeCompare([]byte(got), []byte(cfg.Token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		rep := snap.load()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(&rep); err != nil {
			log.Printf("输出指标失败: %v", err)
		}
	})
	// 在线升级推送（396）：POST /upgrade，服务端直推分发束；token 必配才开
	updH := &UpgradeHandler{cfg: cfg, rt: rt}
	mux.Handle("/upgrade", updH)

	// 请求超时：指标端口暴露在内网，防慢连接占用资源。Read 给到 120s——
	// /upgrade 要收完整分发束（约 4MB），服务端直推窗口 90s，10s 的旧值在
	// 慢链路（VPN 中转等）下上传中途就掐线，升级永远 EOF 打不进去（v1.0.6
	// 现场排查候选根因）；指标 GET 瞬时完成不受影响
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       120 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       75 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // 内部采集循环
		defer wg.Done()
		ticker := time.NewTicker(cfg.Interval.D())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collectOnce()
			}
		}
	}()

	wg.Add(1)
	go func() { // HTTP 退出信号
		defer wg.Done()
		<-ctx.Done()
		log.Printf("收到退出信号，正在关闭…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// 安全提醒（2026-10-20 评估）：未配 token 时 /metrics 无鉴权，主机名/内核/
	// 监听端口/容器清单对任何可达来源可见；不强制（存量机群兼容），但公网
	// 机器应配置 token——它同时也是在线升级（POST /upgrade）的硬性前提
	if cfg.Token == "" {
		log.Printf("安全提醒：抓取密钥（token）未配置，%s/metrics 无鉴权，任何能连到该端口的来源都可读取指标；公网机器请在 agent.yaml 配置 token（也是远程升级的前提）", cfg.Listen)
	}
	log.Printf("指标端口已就绪：http://%s/metrics（等待服务端抓取）", cfg.Listen)
	// 先绑端口再 Serve：绑定成功即「监听健康」，升级标记在此刻清除（395⑧）——
	// 绑定失败走返回错误路径，main 凭标记自动回滚旧版
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	update.ClearMarker(update.DefaultMarkerPath())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	wg.Wait()
	log.Printf("lz-agent 已退出")
	return nil
}

// runPush 周期采集并主动上报服务端，无需开任何端口。
func runPush(ctx context.Context, cfg *Config, rt Runtime, collectOnce func() *proto.Report) error {
	rep := NewReporter(cfg.ServerURL, cfg.Token, 10*time.Second)
	marker := update.DefaultMarkerPath()
	// push 无端口可验：初始化（配置/采集器）完成即视为健康，清升级标记
	update.ClearMarker(marker)

	// 成功只在链路状态翻转时记一次；失败每周期记（便于发现断流）
	ok := false
	post := func() {
		down, err := rep.Post(collectOnce())
		if err != nil {
			log.Printf("上报失败: %v（下周期自动重试）", err)
			ok = false
			return
		}
		if !ok {
			log.Printf("上报链路已连通 → %s（每 %s 一报）", cfg.ServerURL, cfg.Interval.D())
			ok = true
		}
		// 在线升级指令（396）：回源下载分发束并应用（内部验签+校验+试运行，
		// 成功即 exec 换映像不返回；失败只记日志，下轮上报重新领指令）
		if down.Upgrade != nil && cfg.UpgradeEnabled() {
			if err := upgradeSelf(rep, down.Upgrade.Version, rt); err != nil {
				log.Printf("在线升级未完成: %v（下轮上报重试）", err)
			}
		}
		// PT 中转抓取任务（391 扩展）：响应捎带、本机网络执行、结果回传。
		// 零重试纪律：失败原样回传错误，绝不自行重打站点。
		if len(down.PtTasks) > 0 {
			RunPtTasks(rep, down.PtTasks)
		}
		// 通用中转请求任务（548）：同通道、host 白名单兜底。
		if len(down.NetTasks) > 0 {
			RunNetTasks(rep, down.NetTasks)
		}
		// B 站追更任务（TODO 6）：入后台队列串行执行（下载分钟级，绝不阻塞上报心跳）。
		if len(down.BiliStops) > 0 {
			SetBiliStops(down.BiliStops) // 手动停止命令先置位（8）
		}
		if len(down.BiliTasks) > 0 {
			EnqueueBiliTasks(rep, down.BiliTasks)
		}
		// 视频下载任务（TODO 4）：入后台队列串行执行（yt-dlp 下载小时级，
		// 绝不阻塞上报心跳）；停止命令先置位。
		if len(down.VideoDlStops) > 0 {
			SetVideoDlStops(down.VideoDlStops)
		}
		if len(down.VideoDlTasks) > 0 {
			EnqueueVideoDlTasks(rep, down.VideoDlTasks)
		}
		// 应用操作任务（54）：Docker/虚拟机启停，本机执行、结果回传（秒级完成，
		// 顺序执行不阻塞下轮上报）。
		if len(down.AppTasks) > 0 {
			RunAppTasks(rep, down.AppTasks)
		}
	}
	post() // 启动立即报一次，便于尽早确认链路

	ticker := time.NewTicker(cfg.Interval.D())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("收到退出信号，lz-agent 已退出")
			return nil
		case <-ticker.C:
			post()
		}
	}
}

// runCollectors 顺序执行所有采集器，单个失败仅记日志不影响其余指标。
func runCollectors(cols []collect.Collector, r *proto.Report) {
	for _, c := range cols {
		if err := c.Collect(r); err != nil {
			log.Printf("采集 %s 失败: %v", c.Name(), err)
		}
	}
}
