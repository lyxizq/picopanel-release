// Package integrations 提供第三方服务数据集成（Homepage widget 同口径）：
// Home Assistant / qBittorrent / Emby。与 Homepage 浏览器直连不同，这里由
// 服务端代理轮询 + 内存缓存——一次拿掉 CORS、令牌暴露、内网地址泄露三个问题。
//
// 配置存 integrations 表（凭据 AES-GCM 加密）；Manager 常驻 30s 轮询全部启用
// 集成，主页/接口读缓存，缓存缺失（新建/刚重启）时同步补拉一次防空窗。
// 健康态（last_ok_at/last_error）变化才回写库，供管理列表展示。
package integrations

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

// PollInterval 常驻轮询间隔；缓存新鲜度阈值同值。
const PollInterval = 30 * time.Second

// fetchTimeout 单次上游请求总超时（登录+取数各自独立请求，各享此超时）。
const fetchTimeout = 10 * time.Second

// Credentials 凭据 JSON（integrations.auth 列明文结构，落库前整体加密）。
// Token 按类型复用：HA=长效访问令牌、Emby=API Key、qB=≥5.2 API Key（配了则免登录）。
type Credentials struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

// ParseCredentials 解析凭据 JSON（空串/坏 JSON 返回零值）。
func ParseCredentials(auth string) Credentials {
	var c Credentials
	if auth != "" {
		_ = json.Unmarshal([]byte(auth), &c)
	}
	return c
}

// MarshalCredentials 序列化凭据。
func MarshalCredentials(c Credentials) string {
	b, _ := json.Marshal(c)
	return string(b)
}

// Payload 一次抓取的快照：OK=成功；Data 为类型相关的键值（前端按类型渲染）。
type Payload struct {
	OK    bool
	Error string
	Data  map[string]any
}

// Snapshot 主页/接口消费的集成快照（含元信息）。
type Snapshot struct {
	ID      int64          `json:"id"`
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	URL     string         `json:"url"`
	OpenURL string         `json:"open_url,omitempty"` // 打开地址（464，config JSON open_url 键；空=用 URL）
	Enabled bool           `json:"enabled"`
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// configOpenURL 从集成 config JSON 解出打开地址（464）；坏 JSON/无键返回空。
func configOpenURL(config string) string {
	var c struct {
		OpenURL string `json:"open_url"`
	}
	_ = json.Unmarshal([]byte(config), &c)
	return c.OpenURL
}

// entry 单个集成的缓存槽：entry 锁兼顾并发防击穿（同集成的并发请求等第一次抓完共用）。
type entry struct {
	mu      sync.Mutex
	p       Payload
	fetched time.Time
}

// Manager 集成缓存管理器：常驻轮询 + 惰性补拉。
type Manager struct {
	st       *store.Store
	mu       sync.Mutex
	es       map[int64]*entry
	haMu     sync.Mutex
	haPrev   map[int64]map[string]string // HA 开合基线（511）：源 ID → 实体 → 归一状态（on/off）
	embyMu   sync.Mutex
	embyPrev map[int64]map[string]embyActive // Emby 播放差分基线（528）：集成 ID → 差分键 → 进行中
}

// embyActive 一条进行中的播放（开始时已落 emby_events，结束时回填）。
type embyActive struct {
	rowID int64 // emby_events 行 ID
	last  int64 // 最近一帧观测到的播放位置（>1s 变化即回写 DB，534；结束时终值回填）
}

// New 创建管理器。
func New(st *store.Store) *Manager {
	return &Manager{st: st, es: map[int64]*entry{}, haPrev: map[int64]map[string]string{},
		embyPrev: map[int64]map[string]embyActive{}}
}

// Run 常驻轮询：启动立即一轮，之后每 PollInterval 一轮，直到 ctx 取消。
func (m *Manager) Run(ctx context.Context) {
	m.RefreshAll()
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.RefreshAll()
		}
	}
}

// RefreshAll 抓取全部启用集成（顺序执行：≤8 个集成 × 10s 超时，最坏 80s 一轮，
// 但单集成失败不影响其他，且各自缓存独立）。停用/删除的缓存槽顺带清理。
func (m *Manager) RefreshAll() {
	list, err := m.st.ListIntegrations()
	if err != nil {
		log.Printf("集成轮询读取清单失败: %v", err)
		return
	}
	live := map[int64]bool{}
	for _, in := range list {
		if !in.Enabled {
			continue
		}
		live[in.ID] = true
		m.refreshOne(in)
	}
	m.mu.Lock()
	var dropped []int64
	for id := range m.es {
		// ID≤0 = 合成集成槽（475 智能家居总配置，不在集成清单里）：保留——逐出会
		// 让 HA 页 5s 轮询每请求都同步打上游，失去 30s 缓存节流
		if id > 0 && !live[id] {
			delete(m.es, id)
			dropped = append(dropped, id)
		}
	}
	m.mu.Unlock()
	if len(dropped) > 0 {
		m.haMu.Lock()
		for _, id := range dropped {
			delete(m.haPrev, id) // 开合基线随缓存槽一起清，防删除源后常驻滞留
		}
		m.haMu.Unlock()
		m.embyMu.Lock()
		for _, id := range dropped {
			delete(m.embyPrev, id) // 播放差分基线同口径清理（528）
		}
		m.embyMu.Unlock()
	}
}

// Drop 清除单个集成的缓存槽与 HA 开合基线（删除集成后调用）。
func (m *Manager) Drop(id int64) {
	m.mu.Lock()
	delete(m.es, id)
	m.mu.Unlock()
	m.haMu.Lock()
	delete(m.haPrev, id)
	m.haMu.Unlock()
	m.embyMu.Lock()
	delete(m.embyPrev, id)
	m.embyMu.Unlock()
}

// Kick 立即后台重抓一个集成（创建/编辑后调用，让卡片尽快出新数据）。
func (m *Manager) Kick(in store.Integration) {
	if !in.Enabled {
		return
	}
	go m.refreshOne(in)
}

// All 主页轮询消费：全部集成（含停用）的当前快照。启用且缓存过期时同步补拉
// （正常情况下常驻轮询已保热，这里只兜刚创建/刚重启的空窗）。
// 清单走轻量元信息（不解密），缓存热时零解密；仅缓存冷了的启用集成按 id 取
// 明文凭据补拉一次。
func (m *Manager) All() []Snapshot {
	list, err := m.st.ListIntegrationMetas()
	if err != nil {
		return nil
	}
	out := make([]Snapshot, 0, len(list))
	for _, in := range list {
		s := Snapshot{ID: in.ID, Type: in.Type, Name: in.Name, URL: in.URL, OpenURL: configOpenURL(in.Config), Enabled: in.Enabled}
		if in.Enabled {
			full := in
			if !m.fresh(in.ID) {
				if one, err := m.st.GetIntegration(in.ID); err == nil {
					full = *one
				}
			}
			p := m.current(full)
			s.OK = p.OK
			s.Error = p.Error
			s.Data = p.Data
		}
		out = append(out, s)
	}
	return out
}

// fresh 集成缓存是否仍热（PollInterval 内拉过）。
func (m *Manager) fresh(id int64) bool {
	e := m.slot(id)
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fetched != (time.Time{}) && time.Since(e.fetched) < PollInterval
}

// Current 单个集成的当前快照（/ha 设备页消费，355）：启用且缓存过期时同步补拉，
// 正常情况下常驻轮询已保热。
func (m *Manager) Current(in store.Integration) Payload {
	return m.current(in)
}

// current 取单个集成快照：缓存新鲜直接返回，否则同步抓一次。
func (m *Manager) current(in store.Integration) Payload {
	e := m.slot(in.ID)
	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Since(e.fetched) < PollInterval && e.fetched != (time.Time{}) {
		return e.p
	}
	p := fetchWithError(in)
	e.p, e.fetched = p, time.Now()
	m.recordStatus(in, p)
	m.observeHAFrame(in, p)   // HA 开关日志/数值采样（511/512）
	m.observeEmbyFrame(in, p) // Emby 播放记录（528）
	return p
}

func (m *Manager) refreshOne(in store.Integration) {
	e := m.slot(in.ID)
	e.mu.Lock()
	p := fetchWithError(in)
	e.p, e.fetched = p, time.Now()
	e.mu.Unlock()
	m.recordStatus(in, p)
	m.observeHAFrame(in, p)   // HA 开关日志/数值采样（511/512）
	m.observeEmbyFrame(in, p) // Emby 播放记录（528）
}

func (m *Manager) slot(id int64) *entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.es[id]
	if !ok {
		e = &entry{}
		m.es[id] = e
	}
	return e
}

// recordStatus 健康态回写（变化才写库，避免每 30s 白写一轮）。
// ID≤0 = 合成集成（475 智能家居总配置，非库内行）：跳过——UPDATE 落空无害，
// 但健康态无处可写且源结构体 LastOKAt 恒 0 会让短路失效、变成每轮无效写库。
func (m *Manager) recordStatus(in store.Integration, p Payload) {
	if in.ID <= 0 {
		return
	}
	if p.OK && in.LastError == "" && in.LastOKAt > 0 {
		return
	}
	if !p.OK && p.Error == in.LastError {
		return
	}
	if err := m.st.SaveIntegrationStatus(in.ID, p.OK, p.Error); err != nil {
		log.Printf("回写集成健康态失败 (integration=%d): %v", in.ID, err)
	}
}

// TestInput 用给定输入做一次连通性测试（不入库，管理弹窗「测试连接」用）。
func TestInput(typ, url, auth, config string) error {
	in := store.Integration{Type: typ, URL: url, Auth: auth, Config: config}
	p := fetchWithError(in)
	if !p.OK {
		return fmt.Errorf("%s", p.Error)
	}
	return nil
}

func fetchWithError(in store.Integration) Payload {
	var (
		data map[string]any
		err  error
	)
	switch in.Type {
	case store.IntegrationQBit:
		data, err = fetchQBit(in)
	case store.IntegrationEmby:
		data, err = fetchEmby(in)
	case store.IntegrationHA:
		data, err = fetchHA(in)
	case store.IntegrationAdGuard:
		data, err = fetchAdGuard(in)
	case store.IntegrationPihole:
		data, err = fetchPihole(in)
	case store.IntegrationTransmission:
		data, err = fetchTransmission(in)
	case store.IntegrationDeluge:
		data, err = fetchDeluge(in)
	case store.IntegrationAria2:
		data, err = fetchAria2(in)
	case store.IntegrationNavidrome:
		data, err = fetchNavidrome(in)
	case store.IntegrationNextcloud:
		data, err = fetchNextcloud(in)
	case store.IntegrationProxmox:
		data, err = fetchProxmox(in)
	case store.IntegrationDocker:
		data, err = fetchDocker(in)
	case store.IntegrationPortainer:
		data, err = fetchPortainer(in)
	case store.IntegrationDSM:
		data, err = fetchDSM(in)
	case store.IntegrationTrueNAS:
		data, err = fetchTrueNAS(in)
	default:
		err = fmt.Errorf("未知集成类型 %q", in.Type)
	}
	if err != nil {
		return Payload{OK: false, Error: err.Error()}
	}
	return Payload{OK: true, Data: data}
}

// httpClientFor 共享客户端；insecure=true 时跳过 TLS 校验（自签 HTTPS 内网服务）。
func httpClientFor(insecure bool) *http.Client {
	if !insecure {
		return &http.Client{Timeout: fetchTimeout}
	}
	return &http.Client{Timeout: fetchTimeout, Transport: insecureTransport()}
}

// jsonUnmarshal 便捷字符串反序列化。
func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// configBool 通用配置布尔读取（缺省/坏 JSON 均为 false）：qB {insecure:bool}。
func configBool(config, key string) bool {
	return configBoolDefault(config, key, false)
}

// configBoolDefault 同上，缺省值可指定：Emby {now_playing:bool} 默认 true。
func configBoolDefault(config, key string, def bool) bool {
	if config == "" {
		return def
	}
	var m map[string]any
	if json.Unmarshal([]byte(config), &m) != nil {
		return def
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

// configString 读 config 字符串键（缺省/坏 JSON 返回 def）：Docker {sock}。
func configString(config, key, def string) string {
	if config == "" {
		return def
	}
	var m map[string]any
	if json.Unmarshal([]byte(config), &m) != nil {
		return def
	}
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

// basicAuth 生成 Basic 认证头值。
func basicAuth(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

// postJSON 发送 JSON POST 并解析应答（2xx 校验，非 2xx 转错误并保留状态码语义由调用方处理）。
func postJSON(cl *http.Client, url string, headers map[string]string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &httpStatusError{Code: resp.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// httpStatusError 带 HTTP 状态码的错误（调用方按状态码分支，如 Pi-hole v5 回落）。
type httpStatusError struct{ Code int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// httpStatus 取错误的 HTTP 状态码（非状态码错误返回 0）。
func httpStatus(err error) int {
	var se *httpStatusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

// asF64 宽容数值转换：float64/int64/bool 之外的 string/json.Number 也尽力解析
// （Pi-hole v5 的 summary 全是字符串值）。不可解析返回 ok=false。
func asF64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}
