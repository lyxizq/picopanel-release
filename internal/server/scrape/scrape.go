// Package scrape 实现抓取管理器：按服务器配置的采集方式周期拉取快照——
// agent 模式走 HTTP GET /metrics（JSON 快照），node 模式拉 node_exporter 的
// /metrics 文本（见 nodecollect 包），ssh 模式由内置 SSH 客户端直连目标机
// 执行只读命令（见 sshcollect 包）。抓取成功即写库并触发告警评估；
// 连续失败只记一次日志（状态变化时），机器离线判定交给 offline 检测。
package scrape

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/nodecollect"
	"lz-monitor/internal/server/sshcollect"
	"lz-monitor/internal/server/store"
)

// 抓取并发上限：几十台规模下既快又不至于打满连接。
const maxConcurrency = 8

// 单次响应体上限（快照 JSON 正常 <100KB，1MB 兜底防滥用）。
const maxBody = 1 << 20

// node 模式响应体上限：node_exporter 默认 collector 全量输出可达数百 KB
// 甚至更大（取决于机器规模），放宽到 4MB。
const maxNodeBody = 4 << 20

// Config 是抓取管理器配置。
type Config struct {
	Interval time.Duration // 抓取周期（各拉取模式共用）
	Timeout  time.Duration // 单次抓取/执行超时
}

// Manager 周期抓取全部启用中的服务器。
type Manager struct {
	store  *store.Store
	engine *engine.Engine
	cfg    Config
	client *http.Client

	mu       sync.Mutex
	lastDown map[int64]bool // 上轮抓取是否失败（只在状态翻转时打日志）

	sshMu   sync.Mutex
	sshPool map[int64]*sshEntry // ssh 模式客户端池（每台一个长连接）

	nodeMu      sync.Mutex
	nodeClients map[int64]*nodecollect.Client // node 模式差值状态（每台一份上一样本）
}

// sshEntry 是池中一台 ssh 目标的客户端与其创建参数（参数变化时重建连接）。
type sshEntry struct {
	opts   sshcollect.Options
	client *sshcollect.Client
}

// New 创建抓取管理器。
func New(st *store.Store, eng *engine.Engine, cfg Config) *Manager {
	return &Manager{
		store:       st,
		engine:      eng,
		cfg:         cfg,
		client:      &http.Client{Timeout: cfg.Timeout},
		lastDown:    make(map[int64]bool),
		sshPool:     make(map[int64]*sshEntry),
		nodeClients: make(map[int64]*nodecollect.Client),
	}
}

// Run 阻塞循环，直至 ctx 取消。启动立即抓一轮。
func (m *Manager) Run(ctx context.Context) {
	m.scrapeAll()
	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.closeSSH()
			return
		case <-ticker.C:
			m.scrapeAll()
		}
	}
}

// closeSSH 关闭全部 SSH 长连接（进程退出时）。
func (m *Manager) closeSSH() {
	m.sshMu.Lock()
	defer m.sshMu.Unlock()
	for _, e := range m.sshPool {
		e.client.Close()
	}
	m.sshPool = make(map[int64]*sshEntry)
}

func (m *Manager) scrapeAll() {
	servers, err := m.store.EnabledServers()
	if err != nil {
		log.Printf("抓取：查询服务器列表失败: %v", err)
		return
	}
	sshActive := map[int64]bool{}
	nodeActive := map[int64]bool{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)
	for i := range servers {
		srv := servers[i]
		if srv.CollectMode == "push" {
			continue // 上报模式：agent 主动 POST /agent/report，服务端不抓取
		}
		if srv.AgentAddr == "" {
			continue // 未配置地址，跳过
		}
		switch srv.CollectMode {
		case "ssh":
			sshActive[srv.ID] = true
		case "node":
			nodeActive[srv.ID] = true
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { // panic 隔离：单机采集异常按离线处理，不带崩抓取循环
				if p := recover(); p != nil {
					log.Printf("抓取 %s panic（按离线处理）: %v", srv.Name, p)
					m.markDown(srv, fmt.Errorf("采集器内部异常"))
				}
			}()
			defer func() { <-sem }() // 单次抓取由 client/SSH Timeout 兜底时长
			if err := m.scrapeOne(srv); err != nil {
				m.markDown(srv, err)
				return
			}
			m.markUp(srv)
		}()
	}
	wg.Wait()
	m.reapStale(sshActive, nodeActive)
}

// reapStale 关闭已不在当前启用清单里的池化客户端：服务器删除/停用/采集
// 模式改走后，池内条目不再被任何路径引用，不关闭的话 SSH 长连接会一直
// 挂在目标机上（空闲 sshd 进程）直到监控端重启。EnabledServers 查询失败
// 的轮次不会走到这里，瞬时 DB 错误不会误杀连接。
func (m *Manager) reapStale(sshActive, nodeActive map[int64]bool) {
	m.sshMu.Lock()
	var stale []*sshEntry
	for id, e := range m.sshPool {
		if !sshActive[id] {
			stale = append(stale, e)
			delete(m.sshPool, id)
		}
	}
	m.sshMu.Unlock()
	for _, e := range stale {
		e.client.Close()
	}
	m.nodeMu.Lock()
	for id := range m.nodeClients {
		if !nodeActive[id] {
			delete(m.nodeClients, id) // 无连接，纯差值状态，删掉即可
		}
	}
	m.nodeMu.Unlock()
}

// scrapeOne 按采集方式路由：agent → HTTP 拉取 JSON；node → 拉取
// node_exporter 文本；ssh → 直连执行。
func (m *Manager) scrapeOne(srv store.Server) error {
	switch srv.CollectMode {
	case "ssh":
		return m.scrapeSSH(srv)
	case "node":
		return m.scrapeNode(srv)
	}
	return m.scrapeAgent(srv)
}

// scrapeAgent 拉取一台 agent 的快照。
func (m *Manager) scrapeAgent(srv store.Server) error {
	// AgentAddr 存的是 host:port（无 scheme），拼 URL 时补 http://
	url := srv.AgentAddr + "/metrics"
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("构造请求: %w", err)
	}
	if srv.Token != "" {
		req.Header.Set("X-Server-Token", srv.Token)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var r proto.Report
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&r); err != nil {
		return fmt.Errorf("解析快照: %w", err)
	}
	return m.ingest(srv, &r)
}

// scrapeNode 拉取一台 node_exporter 的 /metrics 文本（指标映射与差值计算
// 见 nodecollect 包），产出统一 Report 入库。token 语义与 agent 模式相同
// （可选 X-Server-Token：node_exporter 本体不校验，供前置反代校验用）。
func (m *Manager) scrapeNode(srv store.Server) error {
	url := srv.AgentAddr + "/metrics"
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("构造请求: %w", err)
	}
	if srv.Token != "" {
		req.Header.Set("X-Server-Token", srv.Token)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	s, err := nodecollect.Parse(io.LimitReader(resp.Body, maxNodeBody))
	if err != nil {
		return fmt.Errorf("解析 /metrics: %w", err)
	}
	m.nodeMu.Lock()
	cl := m.nodeClients[srv.ID]
	if cl == nil {
		cl = &nodecollect.Client{}
		m.nodeClients[srv.ID] = cl
	}
	m.nodeMu.Unlock()
	return m.ingest(srv, cl.Report(s, time.Now()))
}

// sshOpts 由服务器配置生成 SSH 客户端参数。
func (m *Manager) sshOpts(srv store.Server) sshcollect.Options {
	return sshcollect.Options{
		Addr:     srv.AgentAddr,
		User:     srv.SSHUser,
		Password: srv.SSHPassword,
		KeyPath:  srv.SSHKeyPath,
		HostKey:  srv.SSHHostKey,
		ServerID: srv.ID,
		Timeout:  m.cfg.Timeout,
		OnHostKey: func(id int64, fp string) {
			if err := m.store.SetHostKey(id, fp); err != nil {
				log.Printf("记录主机指纹失败 server=%d: %v", id, err)
				return
			}
			log.Printf("已记录 SSH 主机指纹 server=%s addr=%s fp=%s（TOFU，之后变更将拒绝连接）",
				srv.Name, srv.AgentAddr, fp)
		},
	}
}

// scrapeSSH 采集一台 ssh 模式服务器：客户端池按 id 复用长连接，参数变化时重建。
func (m *Manager) scrapeSSH(srv store.Server) error {
	opts := m.sshOpts(srv)

	m.sshMu.Lock()
	entry, ok := m.sshPool[srv.ID]
	if ok && !sameSSHOpts(entry.opts, opts) { // 地址/用户/密钥/指纹变了，旧连接作废
		entry.client.Close()
		ok = false
	}
	if !ok {
		entry = &sshEntry{opts: opts, client: sshcollect.NewClient(opts)}
		m.sshPool[srv.ID] = entry
	}
	client := entry.client
	m.sshMu.Unlock()

	r, err := client.Collect()
	if err != nil {
		// 淘汰失效客户端，下轮自动重连重建
		m.sshMu.Lock()
		if m.sshPool[srv.ID] == entry {
			client.Close()
			delete(m.sshPool, srv.ID)
		}
		m.sshMu.Unlock()
		return err
	}
	return m.ingest(srv, r)
}

// sameSSHOpts 比较影响连接复用的参数（OnHostKey 闭包语义恒定，不参与比较）。
func sameSSHOpts(a, b sshcollect.Options) bool {
	return a.Addr == b.Addr && a.User == b.User && a.Password == b.Password &&
		a.KeyPath == b.KeyPath && a.HostKey == b.HostKey &&
		a.ServerID == b.ServerID && a.Timeout == b.Timeout
}

// ingest 统一入库：upsert 服务器信息 + 写指标 + 告警评估（两种模式共用）。
func (m *Manager) ingest(srv store.Server, r *proto.Report) error {
	now := time.Now().Unix() // 存储与告警判定一律用服务端时间，被采集机时钟不可信
	if err := m.store.UpsertOnReport(srv.ID, r.Sys, now); err != nil {
		log.Printf("更新服务器 %d 信息失败: %v", srv.ID, err)
	}
	if err := m.store.InsertMetric(srv.ID, now, r); err != nil {
		return fmt.Errorf("写入指标: %w", err)
	}
	m.engine.Evaluate(srv.ID, r, now)
	return nil
}

func (m *Manager) markDown(srv store.Server, err error) {
	m.mu.Lock()
	was := m.lastDown[srv.ID]
	m.lastDown[srv.ID] = true
	m.mu.Unlock()
	if !was { // 持续失败只记一次，避免每轮刷屏
		log.Printf("抓取失败 server=%s mode=%s addr=%s: %v（恢复后自动继续）",
			srv.Name, srv.CollectMode, srv.AgentAddr, err)
	}
}

func (m *Manager) markUp(srv store.Server) {
	m.mu.Lock()
	was := m.lastDown[srv.ID]
	delete(m.lastDown, srv.ID)
	m.mu.Unlock()
	if was {
		log.Printf("抓取恢复 server=%s mode=%s addr=%s", srv.Name, srv.CollectMode, srv.AgentAddr)
	}
}
