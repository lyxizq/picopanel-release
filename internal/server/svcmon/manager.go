// Package svcmon 服务监控：对「服务器 → 端口」两级配置里的每个端口做轮询检测，
// 把最新状态写库（store 层原位覆盖，不留历史；状态翻转另落 svc_events 事件）。
// 仿 aiusage/mail Manager：常驻调度 + 单端口失败互不影响 + 检测并发上限。检查
// 间隔/启停取所属服务器（366 重构），调度与互斥都按端口 id 记。检测方式按端口
// 行协议（495）：TCP 握手或 HTTP(S) 请求（GET 跟随重定向，终态 2xx 算通，请求
// 不通/错误码算失败）。翻转经 SetNotify 回调交推送装配方（500，批量合并）。
package svcmon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

const (
	// DialTimeout 单次 TCP 握手超时（同工具箱 net-check 口径）。
	DialTimeout = 5 * time.Second
	// schedTick 调度节拍（检查到期的最小粒度）。
	schedTick = 5 * time.Second
	// maxParallel 一轮拨测的并发上限（全不通时按 5s 超时串行会拖爆周期）。
	maxParallel = 8
)

// Manager 按所属服务器的间隔轮询拨测每个端口。lastCheck 记每端口上次派发时刻
// （按派发算到期，不按完成），busy 防同一端口轮询与手动检测重入。状态翻转时
// 落 svc_events（store 层）并回调 onFlip（500：一轮内多个翻转合并成一批交给
// 推送装配方，防路由故障整组掉线时逐端口刷屏）。
type Manager struct {
	st        *store.Store
	mu        sync.Mutex
	lastCheck map[int64]time.Time
	busy      map[int64]bool
	onFlip    func([]Flip)
}

// Flip 一次端口状态翻转（通↔不通；未检测态 -1 的首轮检测不算）。
type Flip struct {
	PortID     int64
	ServerID   int64
	ServerName string
	PortName   string
	Host       string
	Port       int
	Scheme     string
	Domain     string
	Up         bool
	LatencyMs  int64
	ErrMsg     string
	At         int64
}

// label 端口业务名（空回落探测目标描述）。
func (f Flip) label() string {
	if f.PortName != "" {
		return f.PortName
	}
	return f.target()
}

// target 探测目标人读形态：http(s)://domain[:port] 或 host:port（TCP）。
func (f Flip) target() string {
	if f.Scheme == "http" || f.Scheme == "https" {
		d := f.Domain
		if d == "" {
			d = f.Host
		}
		if (f.Scheme == "http" && f.Port == 80) || (f.Scheme == "https" && f.Port == 443) {
			return f.Scheme + "://" + d
		}
		return fmt.Sprintf("%s://%s:%d", f.Scheme, d, f.Port)
	}
	return net.JoinHostPort(f.Host, strconv.Itoa(f.Port))
}

// SetNotify 注册翻转回调（server.go 装配推送与站内消息；nil=只落事件表不出站）。
func (m *Manager) SetNotify(fn func([]Flip)) {
	m.mu.Lock()
	m.onFlip = fn
	m.mu.Unlock()
}

// emit 把一批翻转交给回调（异步跑，推送的 HTTP 往返不拖慢调度节拍）。
func (m *Manager) emit(flips []Flip) {
	m.mu.Lock()
	fn := m.onFlip
	m.mu.Unlock()
	if fn == nil || len(flips) == 0 {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("服务监控翻转回调 panic（已隔离不影响调度）: %v", r)
			}
		}()
		fn(flips)
	}()
}

// New 构造。
func New(st *store.Store) *Manager {
	return &Manager{st: st, lastCheck: map[int64]time.Time{}, busy: map[int64]bool{}}
}

// Run 常驻调度（ctx 取消退出）：启动先查一轮把状态快速填上。
func (m *Manager) Run(ctx context.Context) {
	m.checkDue(time.Now())
	t := time.NewTicker(schedTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.checkDue(now)
		}
	}
}

// checkDue 拨测所有到期的启用端口（服务器停用整组跳过；并发上限内）。
func (m *Manager) checkDue(now time.Time) {
	rows, err := m.st.ListSvcPortRows()
	if err != nil {
		return
	}
	var due []store.SvcPortRow
	m.mu.Lock()
	for _, r := range rows {
		if !r.Enabled || m.busy[r.ID] {
			continue
		}
		if last, ok := m.lastCheck[r.ID]; ok && now.Sub(last) < time.Duration(r.Interval)*time.Second {
			continue
		}
		m.lastCheck[r.ID] = now
		m.busy[r.ID] = true
		due = append(due, r)
	}
	m.mu.Unlock()
	if len(due) == 0 {
		return
	}
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	var flipsMu sync.Mutex
	var flips []Flip
	for _, r := range due {
		wg.Add(1)
		go func(r store.SvcPortRow) {
			defer wg.Done()
			defer func() { // busy 标记无论成败（含 panic）都清，否则该端口不再被调度
				m.mu.Lock()
				delete(m.busy, r.ID)
				m.mu.Unlock()
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			if f := m.checkOne(r); f != nil {
				flipsMu.Lock()
				flips = append(flips, *f)
				flipsMu.Unlock()
			}
		}(r)
	}
	wg.Wait()
	m.gc(rows)
	if len(flips) > 0 {
		m.emit(flips)
	}
}

// checkOne 拨测单端口并写库（状态与翻转事件在 store 层单事务落库）；发生状态
// 翻转时返回翻转描述供批量推送，其余返回 nil（原态未检测 -1 或未翻转）。
// checkOne 拨测单端口并写库（状态与翻转事件在 store 层单事务落库）；发生状态
// 翻转返回 Flip 供推送。panic 在此统一隔离（checkDue 子协程 / CheckNow /
// KickAll 三处调用方共用；拨测目标异常不能带崩进程或漏删 busy 标记）。
func (m *Manager) checkOne(r store.SvcPortRow) (flip *Flip) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("服务拨测 %s:%d panic（已隔离，本轮按未翻转处理）: %v", r.Host, r.Port, p)
		}
	}()
	up, ms, errMsg := Probe(r)
	now := time.Now().Unix()
	flipped, err := m.st.RecordSvcCheck(r.ID, up, ms, errMsg)
	if err != nil || !flipped {
		return nil
	}
	return &Flip{
		PortID: r.ID, ServerID: r.ServerID, ServerName: r.SrvName,
		PortName: r.Name, Host: r.Host, Port: r.Port, Scheme: r.Scheme, Domain: r.Domain,
		Up: up, LatencyMs: ms, ErrMsg: errMsg, At: now,
	}
}

// Probe 按端口行协议探测：http/https 走请求检测，其余 TCP 握手。
func Probe(r store.SvcPortRow) (bool, int64, string) {
	if r.Scheme == "http" || r.Scheme == "https" {
		return CheckHTTP(r.Scheme, r.SvcPort.EffectiveDomain(r.Host), strconv.Itoa(r.Port))
	}
	return CheckPort(r.Host, r.Port)
}

// probeHTTPClient 请求检测客户端：跟随重定向（默认最多 10 次，nginx 跳转落到
// 错误页会以终态码判定）；跳过 TLS 证书校验——探测的是站点可用性而非证书有效性
//（证书另有 certs 模块盯），内网自签不误报。
var probeHTTPClient = &http.Client{
	Timeout:   DialTimeout,
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

// CheckHTTP 一次请求检测：GET scheme://domain[:port]，终态 2xx 算通；
// 请求不通（连接失败/超时/重定向超限）与非 2xx 终态（含 nginx 跳转报错页）
// 都算失败。域名不带端口时 http 默认 80、https 默认 443（URL 省略默认端口）。
func CheckHTTP(scheme, domain, port string) (bool, int64, string) {
	host := domain
	if (scheme == "http" && port != "80") || (scheme == "https" && port != "443") {
		host = net.JoinHostPort(domain, port)
	}
	start := time.Now()
	resp, err := probeHTTPClient.Get(scheme + "://" + host)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		// 剥掉 url.Error 的 "Get \"URL\":" 前缀，只留底层原因（ refused/timeout 等）
		msg := err.Error()
		var ue *url.Error
		if errors.As(err, &ue) {
			msg = ue.Err.Error()
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return false, ms, msg
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, ms, ""
	}
	return false, ms, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

// Check 一次 TCP 拨测：返回 是否连通/握手耗时 ms/失败原因。
func Check(host, port string) (bool, int64, string) {
	addr := net.JoinHostPort(host, port)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, DialTimeout)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return false, ms, err.Error()
	}
	_ = conn.Close()
	return true, ms, ""
}

// CheckPort int 端口版（API 入口用）。
func CheckPort(host string, port int) (bool, int64, string) {
	return Check(host, strconv.Itoa(port))
}

// CheckNow 手动检测单端口，返回刷新后的完整行。同端口已在拨测中则等它
// 结束直接读库（不重复打对端）。
func (m *Manager) CheckNow(id int64) (*store.SvcPortRow, error) {
	r, err := m.st.GetSvcPortRow(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.busy[r.ID] {
		m.mu.Unlock()
		m.waitIdle(r.ID)
		return m.st.GetSvcPortRow(id)
	}
	m.busy[r.ID] = true
	m.lastCheck[r.ID] = time.Now()
	m.mu.Unlock()
	if f := m.checkOne(*r); f != nil {
		m.emit([]Flip{*f})
	}
	m.mu.Lock()
	delete(m.busy, r.ID)
	m.mu.Unlock()
	return m.st.GetSvcPortRow(id)
}

// KickAll 全部启用端口立即重查（后台异步跑一轮，前端靠轮询看到结果）。
func (m *Manager) KickAll() {
	rows, err := m.st.ListSvcPortRows()
	if err != nil {
		return
	}
	m.mu.Lock()
	for _, r := range rows {
		if r.Enabled {
			delete(m.lastCheck, r.ID)
		}
	}
	m.mu.Unlock()
	go m.checkDue(time.Now())
}

// KickServer 单台服务器的启用端口立即重查（编辑换址/手动整台检测用，异步）。
func (m *Manager) KickServer(serverID int64) {
	rows, err := m.st.ListSvcPortRows()
	if err != nil {
		return
	}
	m.mu.Lock()
	for _, r := range rows {
		if r.ServerID == serverID && r.Enabled {
			delete(m.lastCheck, r.ID)
		}
	}
	m.mu.Unlock()
	go m.checkDue(time.Now())
}

// waitIdle 等端口的进行中拨测结束（忙标记消失）。
func (m *Manager) waitIdle(id int64) {
	for {
		m.mu.Lock()
		busy := m.busy[id]
		m.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (m *Manager) gc(rows []store.SvcPortRow) {
	alive := map[int64]bool{}
	for _, r := range rows {
		alive[r.ID] = true
	}
	m.mu.Lock()
	for id := range m.lastCheck {
		if !alive[id] {
			// 只清 lastCheck：busy 由各拨测协程的 defer 自清（本函数快照可能
			// 已过期，并发 Kick 轮刚建立的新端口 busy 会被这里误删）
			delete(m.lastCheck, id)
		}
	}
	m.mu.Unlock()
}

// Drop 删除端口时清调度状态。
func (m *Manager) Drop(id int64) {
	m.mu.Lock()
	delete(m.lastCheck, id)
	delete(m.busy, id)
	m.mu.Unlock()
}
