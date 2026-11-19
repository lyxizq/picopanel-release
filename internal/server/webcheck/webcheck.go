// Package webcheck 网站检查（服务监控页新模块，2026-11-03）：按目标配置的 URL
// 抓取网页做多规则关键字判定——规则内关键字全部包含才算该规则通过（且），任一
// 规则通过即通过（或），全部规则都不满足才算失败；失败经状态翻转推三渠道 +
// 站内消息。调度口径：每 2 小时一轮，本地时间 0:00–6:00 夜间静默跳过（手动检测
// 不受限）。仿 ghwatch/svcmon Manager：常驻调度 + 单目标失败互不影响 + 忙标记
// 防重入，状态只存最新一份。
package webcheck

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

const (
	// HTTPTimeout 单次抓取超时（比 TCP 拨测放宽——要等整页响应体）。
	HTTPTimeout = 20 * time.Second
	// CheckInterval 检查间隔：每 2 小时一轮（2026-11-03 用户指定）。
	CheckInterval = 2 * time.Hour
	// maxBody 页面体读入上限（关键字匹配用，超长截尾；正常页面远小于此）。
	maxBody = 4 << 20
	// userAgent 抓取 UA：带浏览器兼容形态，减少被 WAF 按空 UA/爬虫拦截的误报。
	userAgent = "Mozilla/5.0 (compatible; PicoPanel)"
	// maxParallel 一轮检查的并发上限。
	maxParallel = 4
)

// Manager 常驻调度：每 2 小时全量检查启用目标；busy 防轮询与手动检测重入。
// 翻转经 onFlip 回调交推送装配方（一轮内多个翻转合并成一批，同 svcmon 口径）。
type Manager struct {
	st      *store.Store
	hc      *http.Client
	blocked func(host string) bool // 出站内网限制判定（nil=不限制，server.go 注入）
	mu      sync.Mutex
	busy    map[int64]bool
	onFlip  func([]Flip)
}

// Flip 一次目标状态翻转（通过↔失败；未检测态 -1 的首轮检测不算）。
type Flip struct {
	ID     int64
	Name   string
	URL    string
	Up     bool
	ErrMsg string
}

// New 构造（notify 装配用 SetNotify，server.go 注入）。blocked 为出站内网限制
// 判定（config.OutboundHostBlocked，nil=不限制）：目标是用户可控 URL 的服务端
// 抓取（2026-11-03 漏洞修：与 net-check/日历订阅同口径接 outbound_block_private，
// 否则开关开启时本模块仍可探内网，missing 清单/HTTP 状态可当内容侧信道）。
func New(st *store.Store, blocked func(host string) bool) *Manager {
	m := &Manager{
		st:      st,
		blocked: blocked,
		busy:    map[int64]bool{},
	}
	m.hc = &http.Client{
		Timeout: HTTPTimeout,
		// 跳过 TLS 证书校验：盯的是页面内容而非证书（证书另有 certs 模块），
		// 内网自签不误报（同 svcmon CheckHTTP 口径）
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		// 重定向逐跳校验（2026-11-03）：公网 URL 30x 跳内网同样要拦；默认跟随
		// 10 跳的上限保持
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("重定向过多")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("重定向到非 http(s) 地址")
			}
			if m.blocked != nil && m.blocked(req.URL.Hostname()) {
				return errors.New("重定向目标解析到内网，已被出站内网限制拦截")
			}
			return nil
		},
	}
	return m
}

// SetNotify 注册翻转回调（server.go 装配推送与站内消息；nil=不通知）。
func (m *Manager) SetNotify(fn func([]Flip)) {
	m.mu.Lock()
	m.onFlip = fn
	m.mu.Unlock()
}

// emit 把一批翻转交给回调（异步跑，推送的 HTTP 往返不拖慢调度；recover 防静默）。
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
				log.Printf("网站检查翻转回调 panic（已隔离不影响调度）: %v", r)
			}
		}()
		fn(flips)
	}()
}

// InQuietHours 报告 t 是否处于夜间静默时段（本地时间 0:00–6:00 不检查）。
func InQuietHours(t time.Time) bool { return t.Hour() < 6 }

// Run 常驻调度（ctx 取消退出）：启动先查一轮把状态快速填上；每 CheckInterval
// 一轮，静默时段整轮跳过（下一轮出静默窗口自然补上，不为补查打乱节拍）。
func (m *Manager) Run(ctx context.Context) {
	m.tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(CheckInterval):
			m.tick()
		}
	}
}

// tick 一轮：静默时段直接跳过；并发检查全部启用目标，翻转合批后一次通知。
func (m *Manager) tick() {
	if InQuietHours(time.Now()) {
		return
	}
	m.checkAll()
}

// checkAll 并发检查全部启用目标（小清单，并发 4 足够；单目标失败只记
// last_error 不影响其他目标），翻转合批 emit。
func (m *Manager) checkAll() {
	rows, err := m.st.ListWebChecks()
	if err != nil {
		log.Printf("webcheck 读目标清单失败: %v", err)
		return
	}
	var due []*store.WebCheck
	m.mu.Lock()
	for i := range rows {
		w := &rows[i]
		if !w.Enabled || m.busy[w.ID] {
			continue
		}
		m.busy[w.ID] = true
		due = append(due, w)
	}
	m.mu.Unlock()
	if len(due) == 0 {
		return
	}
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	var flipsMu sync.Mutex
	var flips []Flip
	for _, w := range due {
		wg.Add(1)
		go func(w *store.WebCheck) {
			defer wg.Done()
			defer func() { // busy 标记无论成败（含 panic）都清，否则该目标不再被调度
				m.mu.Lock()
				delete(m.busy, w.ID)
				m.mu.Unlock()
			}()
			sem <- struct{}{}
			defer func() { <-sem }()
			if f := m.checkOne(w); f != nil {
				flipsMu.Lock()
				flips = append(flips, *f)
				flipsMu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(flips) > 0 {
		m.emit(flips)
	}
}

// checkOne 检查单目标并写库；发生状态翻转返回 Flip 供批量推送，其余返回 nil
// （原态未检测 -1 或未翻转）。panic 在此统一隔离（checkAll 子协程 / CheckNow /
// KickAll 三处调用方共用）。
func (m *Manager) checkOne(w *store.WebCheck) (flip *Flip) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("网站检查 %s panic（已隔离，本轮按未翻转处理）: %v", w.URL, p)
		}
	}()
	res := m.CheckURL(w.URL, w.RuleList())
	flipped, err := m.st.RecordWebCheck(w.ID, res.OK, res.ErrMsg)
	if err != nil || !flipped {
		return nil
	}
	return &Flip{ID: w.ID, Name: w.Name, URL: w.URL, Up: res.OK, ErrMsg: res.ErrMsg}
}

// CheckNow 手动检查单目标（同步，静默时段也执行——用户点名查就查），返回刷新
// 后的完整行。同目标已在检查中等它结束直接读库（不重复打对端）；翻转照常通知
// （状态变了就是真变化，同 svcmon 手动检测口径）。
func (m *Manager) CheckNow(id int64) (*store.WebCheck, error) {
	w, err := m.st.GetWebCheck(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.busy[w.ID] {
		m.mu.Unlock()
		m.waitIdle(w.ID)
		return m.st.GetWebCheck(id)
	}
	m.busy[w.ID] = true
	m.mu.Unlock()
	if f := m.checkOne(w); f != nil {
		m.emit([]Flip{*f})
	}
	m.mu.Lock()
	delete(m.busy, w.ID)
	m.mu.Unlock()
	return m.st.GetWebCheck(id)
}

// KickAll 全部启用目标立即重查（后台异步跑一轮，前端靠轮询看到结果）。
func (m *Manager) KickAll() {
	go m.checkAll()
}

// waitIdle 等目标的进行中检查结束（忙标记消失）。
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

// Drop 删除目标时清调度状态。
func (m *Manager) Drop(id int64) {
	m.mu.Lock()
	delete(m.busy, id)
	m.mu.Unlock()
}

// Result 一次检查的结论。
type Result struct {
	OK          bool
	ErrMsg      string     // 失败原因（请求失败/非 2xx/逐规则缺失清单）
	RuleMissing [][]string // 与规则序对齐：各规则缺失的关键字（全部规则未通过才有）
	MatchedRule int        // 通过的规则序号（1 起；0=无）
}

// ParseCheckURL 规整用户输入为完整 URL：无 scheme 默认补 http://（IP+端口形态
// 友好）；仅接受 http/https；host 必填；path 为「/」时剥掉统一形态（查重即按
// 此规整后的串 UNIQUE）。超长报错。
func ParseCheckURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("请填写网址")
	}
	if len(s) > 512 {
		return "", errors.New("网址最长 512 字")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("网址解析失败")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("仅支持 http/https 网址")
	}
	if u.Host == "" {
		return "", errors.New("网址缺少主机名（域名或 IP）")
	}
	// 主机名统一小写（域名大小写不敏感，查重按规整后的串 UNIQUE）
	u.Host = strings.ToLower(u.Host)
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
}

// CheckURL 抓取页面并做多规则判定（2026-11-03 改版）：规则内关键字全部包含才算
// 该规则通过（且），任一规则通过即目标通过（或）；全部规则都不满足才算失败
// （逐规则列缺失清单）。关键字不区分大小写包含匹配。
func (m *Manager) CheckURL(rawURL string, rules [][]string) Result {
	if len(rules) == 0 {
		return Result{ErrMsg: "未配置规则"}
	}
	// 内网拦截取抓取时点判定（与日历订阅同口径：开关联动存量目标即时生效）
	if m.blocked != nil {
		if u, perr := url.Parse(rawURL); perr == nil && m.blocked(u.Hostname()) {
			return Result{ErrMsg: "目标地址解析到内网，已被出站内网限制拦截（outbound_block_private）"}
		}
	}
	resp, err := m.hc.Get(rawURL)
	if err != nil {
		// 剥掉 url.Error 的 "Get \"URL\":" 前缀，只留底层原因（refused/timeout 等）
		msg := err.Error()
		var ue *url.Error
		if errors.As(err, &ue) {
			msg = ue.Err.Error()
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return Result{ErrMsg: "请求失败：" + msg}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{ErrMsg: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	// 只按内容判定：非 2xx 已挡掉错误页；读全页面再统一小写比对
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Result{ErrMsg: "读取页面失败：" + err.Error()}
	}
	lower := strings.ToLower(string(body))
	var ruleMissing [][]string
	for i, rule := range rules {
		var missing []string
		for _, k := range rule {
			if !strings.Contains(lower, strings.ToLower(k)) {
				missing = append(missing, k)
			}
		}
		if len(missing) == 0 {
			return Result{OK: true, MatchedRule: i + 1}
		}
		ruleMissing = append(ruleMissing, missing)
	}
	return Result{RuleMissing: ruleMissing, ErrMsg: missingMsg(rules, ruleMissing)}
}

// missingMsg 全部规则未通过的人话：逐规则列缺失（「规则 1 缺…；规则 2 缺…」），
// 按 rune 截断防超长。
func missingMsg(rules [][]string, ruleMissing [][]string) string {
	var parts []string
	for i, missing := range ruleMissing {
		quoted := make([]string, 0, len(missing))
		for _, k := range missing {
			quoted = append(quoted, "「"+k+"」")
		}
		if len(rules) == 1 {
			parts = append(parts, "缺少关键字 "+strings.Join(quoted, " "))
		} else {
			parts = append(parts, fmt.Sprintf("规则 %d 缺少 %s", i+1, strings.Join(quoted, " ")))
		}
	}
	msg := strings.Join(parts, "；")
	if runes := []rune(msg); len(runes) > 300 {
		msg = string(runes[:300])
	}
	return msg
}
