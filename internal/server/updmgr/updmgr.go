// Package updmgr 实现服务端在线更新管理（TODO 395/396）：
//   - 检查：按 sources 依次探测更新通道（静态目录 / GitHub Releases），
//     验 manifest 签名（构建注入公钥），比对版本得「可升级」结论并缓存
//     agent 两架构包到部署目录 updates/（服务端作 agent 升级的分发枢纽）；
//   - 自应用：下载服务端包 → 升级前备库（checkpoint+文件轮换）→
//     internal/update.Apply（试运行/原子替换/exec 同 PID 重启）；
//   - 分发：pull 机器经 POST <agent>/upgrade 直推分发束（token 必配），
//     push 机器在上报响应里捎带指令、agent 凭 X-Agent-Token 回源下载；
//     结果以「版本串变化」确认，超时由管理页状态呈现。
package updmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/update"
)

// Config 管理器配置（server.go 装配注入）。
type Config struct {
	Sources []string // 更新源列表（空=功能关闭）
	Channel string   // stable | beta
	PubKey  string   // 构建注入的验签公钥 hex
	Version string   // 当前服务端版本串
	Dir     string   // 分发缓存目录（部署目录/updates）
	Agents  bool     // 检出新版后自动分发升级 agent（396：缺省开）
}

// Status 检查结果（API 直出）。
type Status struct {
	Configured  bool   `json:"configured"`
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	Channel     string `json:"channel"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"published_at"`
	Available   bool   `json:"available"`
	Cached      bool   `json:"cached"`         // agent 分发包是否已缓存就绪
	CachedVer   string `json:"cached_version"` // 缓存对应的通道版本
	CheckedAt   int64  `json:"checked_at"`
	Error       string `json:"error,omitempty"`
	// agent 视角（2026-10-05 用户指定：在线更新处展示渠道最新 agent 版本与待升级台数）
	AgentsLatest     string `json:"agents_latest"`     // 渠道最新的 agent 版本（=缓存版本）
	AgentsUpgradable int    `json:"agents_upgradable"` // 版本落后于渠道的 agent 台数
	AgentsUnknown    int    `json:"agents_unknown"`    // 未上报版本（旧版 agent）台数
}

// Hooks 运行期注入的重启相关钩子（server.go 装配后调 SetHooks）。
type Hooks struct {
	BackupDB func() error                           // 升级前备份：WAL checkpoint + 主库文件轮换拷贝
	Shutdown func()                                 // 停 HTTP 与后台任务（cancel ctx + srv.Shutdown）
	Exec     func(path string, args []string) error // 自替换出口（测试桩用；nil=真 exec）
}

// Manager 更新管理器。Check/Apply 幂等可重入；ApplyServer 有防重入锁。
type Manager struct {
	st     *store.Store
	cfg    Config
	cli    *http.Client
	hooks  Hooks
	status Status

	mu       sync.Mutex
	pending  map[int64]pushPending // push 模式机器的待升级标记（id → 目标版本+入队时刻）
	busy     atomic.Bool           // ApplyServer 防重入
	applying atomic.Bool
	rootCtx  context.Context // 应用根 ctx（Run 注入）：停机后不再起新的自动分发轮
}

// pushPending 一条待领的升级指令。at 用于过期：目标机器升级一直失败（磁盘满
// 等）时若永不过期，每个上报周期都会重拉约 8MB 分发束，pending 也永不收敛。
type pushPending struct {
	version string
	at      time.Time
}

// pendingTTL 待领指令的有效期：过期后不再随上报下发（下次手动/自动分发重打标）。
const pendingTTL = 24 * time.Hour

func New(st *store.Store, cfg Config) *Manager {
	if cfg.Channel == "" {
		cfg.Channel = "stable"
	}
	return &Manager{
		st:      st,
		cfg:     cfg,
		cli:     &http.Client{Timeout: 120 * time.Second},
		status:  Status{Configured: len(cfg.Sources) > 0, Current: cfg.Version, Channel: cfg.Channel},
		pending: map[int64]pushPending{},
		rootCtx: context.Background(),
	}
}

// SetHooks 注入重启相关钩子（须在 ApplyServer 可被触发前完成）。
func (m *Manager) SetHooks(h Hooks) { m.hooks = h }

// CacheDir 分发缓存目录（测试与运维排查用）。
func (m *Manager) CacheDir() string { return m.cfg.Dir }

// Run 周期自动检查（check_interval>0 时由 server.go 起协程）。
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	m.mu.Lock()
	m.rootCtx = ctx
	m.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s, err := m.Check(ctx); err != nil {
				log.Printf("[update] 自动检查失败: %v", err)
			} else if s.Available {
				log.Printf("[update] 发现新版本 %s（当前 %s），agent 自动分发已触发",
					s.Latest, s.Current)
			}
		}
	}
}

// 缓存目录四件套：manifest + 签名 + 版本标记 + 两架构包。
func (m *Manager) cachedManifestPath() string { return filepath.Join(m.cfg.Dir, "manifest.json") }
func (m *Manager) cachedSigPath() string      { return filepath.Join(m.cfg.Dir, "manifest.json.sig") }

// cachedVerPath 缓存版本标记文件。
func (m *Manager) cachedVerPath() string { return filepath.Join(m.cfg.Dir, "target.json") }

// Check 探测更新通道并返回最新状态。检出新版时：缓存 agent 两架构包
// （best-effort，失败不挡检查结论），按配置自动分发升级 agent。
func (m *Manager) Check(ctx context.Context) (Status, error) {
	if len(m.cfg.Sources) == 0 {
		st := Status{Configured: false, Current: m.cfg.Version, Channel: m.cfg.Channel,
			CheckedAt: time.Now().Unix()}
		m.setStatus(st)
		return st, nil
	}
	res, err := update.ResolveLatest(ctx, m.cli, m.cfg.Sources, m.cfg.Channel)
	if err != nil {
		st := Status{Configured: true, Current: m.cfg.Version, Channel: m.cfg.Channel,
			CheckedAt: time.Now().Unix(), Error: err.Error()}
		m.setStatus(st)
		return st, err
	}
	// 验签是硬门槛：通道内容与发布方不符一律按检查失败处理
	if err := update.VerifyManifest(m.cfg.PubKey, res.Raw, res.Sig); err != nil {
		st := Status{Configured: true, Current: m.cfg.Version, Channel: m.cfg.Channel,
			CheckedAt: time.Now().Unix(), Error: err.Error()}
		m.setStatus(st)
		return st, err
	}
	mf := res.Manifest
	st := Status{
		Configured:  true,
		Current:     m.cfg.Version,
		Latest:      mf.Version,
		Channel:     mf.Channel,
		Notes:       mf.Notes,
		PublishedAt: mf.PublishedAt,
		Available:   update.Newer(m.cfg.Version, mf.Version),
		CheckedAt:   time.Now().Unix(),
		CachedVer:   m.cachedVersion(),
	}
	// 缓存 agent 两架构包：只要通道可达就备好弹药——server 自身已是最新时
	// 名下 agent 仍可能落后（server 手动升的、上次分发失败的机器），自动分发
	// 与「升级全部 Agent」按钮都依赖这份缓存（幂等：同版本已缓存直接复用）。
	// 故此处不得以 server 自身 Available 为前置条件。
	if err := m.cacheAgentPackages(ctx, res); err != nil {
		st.Error = "agent 包缓存失败: " + err.Error()
	} else {
		st.Cached = true
		st.CachedVer = mf.Version
		st.AgentsLatest = mf.Version
	}
	st.AgentsUpgradable, st.AgentsUnknown = m.agentCounts()
	m.setStatus(st)
	// agent 自动分发（396：缺省开）——只要存在落后机器就分发（版本已最新的
	// 机器天然被过滤，周期检查反复跑是无害的空转+自愈），异步执行不挡接口
	if st.Cached && m.cfg.Agents {
		m.mu.Lock()
		root := m.rootCtx
		m.mu.Unlock()
		if root != nil && root.Err() == nil { // 停机后不再起分发轮（goroutine 不在 wg 里，须受根 ctx 约束）
			go func() {
				defer func() { // panic 只丢本轮分发，下个周期重试，不带崩进程
					if p := recover(); p != nil {
						log.Printf("[update] agent 分发 panic（已隔离，下周期重试）: %v\n%s", p, debug.Stack())
					}
				}()
				bgCtx, cancel := context.WithTimeout(root, 10*time.Minute)
				defer cancel()
				if n := m.upgradeAllEligible(bgCtx); n > 0 {
					log.Printf("[update] 已自动分发升级指令到 %d 台 agent（目标 %s）", n, st.Latest)
				}
			}()
		}
	}
	return st, nil
}

func (m *Manager) setStatus(st Status) {
	m.mu.Lock()
	m.status = st
	m.mu.Unlock()
}

// Status 返回最近一次检查结果（不发网络请求；agent 台数统计现查库，表极小）。
func (m *Manager) Status() Status {
	m.mu.Lock()
	st := m.status
	m.mu.Unlock()
	if st.CachedVer == "" {
		st.CachedVer = m.cachedVersion()
	}
	st.AgentsLatest = st.CachedVer
	st.AgentsUpgradable, st.AgentsUnknown = m.agentCounts()
	return st
}

// agentCounts 统计落后/未上报版本的 agent 台数（在线更新处提示用）；
// 停用（enabled=0）的机器不参与统计（71 用户指定）——停用即暂停监控，
// 其版本落后与否不是待办。
func (m *Manager) agentCounts() (upgradable, unknown int) {
	list, err := m.AgentList()
	if err != nil {
		return 0, 0
	}
	for _, a := range list {
		if !a.Enabled {
			continue
		}
		if a.Version == "" {
			unknown++
		} else if a.Upgradable {
			upgradable++
		}
	}
	return upgradable, unknown
}

// cachedVersion 读缓存目录里 target.json 的版本串（无缓存=空）。
func (m *Manager) cachedVersion() string {
	b, err := os.ReadFile(m.cachedVerPath())
	if err != nil {
		return ""
	}
	var t struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &t) != nil {
		return ""
	}
	return t.Version
}

// cacheAgentPackages 下载并校验 agent 两架构包，原子落进缓存目录。
// 幂等：同版本已缓存且文件齐全时直接复用（周期检查不重复下载约 8MB）。
func (m *Manager) cacheAgentPackages(ctx context.Context, res *update.Resolved) error {
	if err := os.MkdirAll(m.cfg.Dir, 0o755); err != nil {
		return err
	}
	if m.cachedVersion() == res.Manifest.Version {
		// 同版本不必然同内容（发布侧删整发重跑会换资产）：缓存 manifest 与通道
		// manifest 字节一致才算命中缓存——不一致必须换弹药，否则重发的修复包
		// 永远发不出去（v1.0.5 实坑：版本号没变，缓存把坏 agent 包供到底）
		if cached, err := os.ReadFile(m.cachedManifestPath()); err == nil && bytes.Equal(cached, res.Raw) {
			complete := true
			for _, f := range []string{"manifest.json", "manifest.json.sig", "lz-agent-amd64.gz", "lz-agent-arm64.gz"} {
				if _, err := os.Stat(filepath.Join(m.cfg.Dir, f)); err != nil {
					complete = false
					break
				}
			}
			if complete {
				return nil
			}
		}
	}
	for _, key := range []string{update.AssetAgentAmd, update.AssetAgentArm} {
		file, err := update.AssetFile(key)
		if err != nil {
			return err
		}
		pkg, err := update.DownloadAsset(ctx, m.cli, res, key)
		if err != nil {
			return err
		}
		tmp := filepath.Join(m.cfg.Dir, file+".tmp")
		if err := os.WriteFile(tmp, pkg, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(m.cfg.Dir, file)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(m.cachedManifestPath(), res.Raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(m.cachedSigPath(), []byte(res.Sig), 0o644); err != nil {
		return err
	}
	tb, _ := json.Marshal(struct {
		Version string `json:"version"`
	}{res.Manifest.Version})
	return os.WriteFile(m.cachedVerPath(), tb, 0o644)
}

// ApplyServer 下载服务端包并自替换重启（成功时本调用不返回）。防重入。
func (m *Manager) ApplyServer(ctx context.Context) error {
	if !m.applying.CompareAndSwap(false, true) {
		return errors.New("已有一次更新在进行中")
	}
	defer m.applying.Store(false)
	if m.cfg.PubKey == "" || m.cfg.PubKey == "empty" {
		return errors.New("本程序构建未注入验签公钥，无法安全在线更新")
	}
	res, err := update.ResolveLatest(ctx, m.cli, m.cfg.Sources, m.cfg.Channel)
	if err != nil {
		return err
	}
	if err := update.VerifyManifest(m.cfg.PubKey, res.Raw, res.Sig); err != nil {
		return err
	}
	if !update.Newer(m.cfg.Version, res.Manifest.Version) && res.Manifest.Version != m.cfg.Version {
		return fmt.Errorf("通道版本 %s 不高于当前 %s，无需更新", res.Manifest.Version, m.cfg.Version)
	}
	if err := update.Fresh(res.Manifest, time.Now()); err != nil {
		return err
	}
	pkg, err := update.DownloadAsset(ctx, m.cli, res, update.AssetServer)
	if err != nil {
		return err
	}
	if m.hooks.BackupDB != nil {
		if err := m.hooks.BackupDB(); err != nil {
			return fmt.Errorf("升级前备份失败（已中止，旧版未动）: %w", err)
		}
	}
	log.Printf("[update] 开始应用服务端更新：%s → %s（成功后进程自动重启）", m.cfg.Version, res.Manifest.Version)
	return update.Apply(update.ApplyOptions{
		PackageGZ:   pkg,
		CheckArgs:   []string{"-check-config"},
		FromVersion: m.cfg.Version,
		Exec:        m.hooks.Exec,
		PreRestart: func() error {
			if m.hooks.Shutdown != nil {
				m.hooks.Shutdown()
			}
			return nil
		},
	})
}

// ── agent 分发（396）───────────────────────────────────────────────────

// AgentUpd 单台机器的升级视角（API 直出）。
type AgentUpd struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Mode       string `json:"mode"`
	Version    string `json:"version"`
	Latest     string `json:"latest"`
	Upgradable bool   `json:"upgradable"`
	Enabled    bool   `json:"enabled"` // 服务器启用态（false=停用；台数统计排除，71）
	TokenSet   bool   `json:"token_set"`
	Note       string `json:"note,omitempty"`
}

// AgentList 全部 agent/push 机器的版本与可升级状态（ssh/node 模式无 agent 不参与）。
// 轻量行足够：密钥只判有无（TokenSet），不解密；enabled 供台数统计排除停用机（71）。
func (m *Manager) AgentList() ([]AgentUpd, error) {
	servers, err := m.st.ListServerLites(false)
	if err != nil {
		return nil, err
	}
	latest := m.cachedVersion()
	out := make([]AgentUpd, 0, len(servers))
	for _, s := range servers {
		if s.CollectMode != "agent" && s.CollectMode != "push" {
			continue
		}
		u := AgentUpd{ID: s.ID, Name: s.Name, Mode: s.CollectMode, Enabled: s.Enabled,
			Version: s.AgentVersion, Latest: latest, TokenSet: s.TokenSet}
		if u.Version == "" {
			u.Note = "旧版 agent 未上报版本"
		} else {
			u.Upgradable = latest != "" && update.Newer(u.Version, latest) // describe 形态（如 v1.0.4-1-gX）不算落后
		}
		if s.CollectMode == "agent" && !s.TokenSet {
			u.Note = "未配置抓取密钥，拒绝升级（安全口径）"
		}
		out = append(out, u)
	}
	return out, nil
}

// UpgradeResult 单台分发结果（API 直出）。
type UpgradeResult struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"` // triggered=已直推 / queued=已标记待领 / skipped / error
	Detail string `json:"detail,omitempty"`
}

// UpgradeAgents 对指定机器触发升级（pull 直推、push 打标），两路并发限 2。
func (m *Manager) UpgradeAgents(ctx context.Context, ids []int64) ([]UpgradeResult, error) {
	if m.cachedVersion() == "" {
		if _, err := m.Check(ctx); err != nil {
			return nil, fmt.Errorf("更新通道不可用: %w", err)
		}
	}
	if m.cachedVersion() == "" {
		return nil, errors.New("agent 分发包未就绪：请先检查更新（会自动缓存两架构包）")
	}
	servers, err := m.st.ListServers()
	if err != nil {
		return nil, err
	}
	byID := map[int64]store.Server{}
	for _, s := range servers {
		byID[s.ID] = s
	}
	// 结果按索引预分配：直推结果在 goroutine 里回填，值拷贝 append 会把
	// 并发回填丢在切片外（曾致 pull 机器的接口结果 state 为空）
	results := make([]UpgradeResult, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 2)
	for i, id := range ids {
		r := &results[i]
		r.ID = id
		s, ok := byID[id]
		if !ok {
			r.State = "error"
			r.Detail = "机器不存在"
			continue
		}
		r.Name = s.Name
		switch s.CollectMode {
		case "push":
			m.mu.Lock()
			m.pending[s.ID] = pushPending{version: m.cachedVersion(), at: time.Now()}
			m.mu.Unlock()
			r.State = "queued"
			r.Detail = "push 机器：下次上报时领取升级指令"
		case "agent":
			if s.Token == "" {
				r.State = "skipped"
				r.Detail = "未配置抓取密钥（X-Server-Token），拒绝远程升级——先在机器的 agent.yaml 配置 token"
				break
			}
			if s.AgentAddr == "" {
				r.State = "skipped"
				r.Detail = "未配置指标端口地址"
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(s store.Server, r *UpgradeResult) {
				defer wg.Done()
				defer func() { <-sem }()
				ctx2, cancel := context.WithTimeout(ctx, 90*time.Second)
				defer cancel()
				if err := m.pushBundle(ctx2, s); err != nil {
					// agent /upgrade 应用完即 exec 换映像，HTTP 响应永远发不回：
					// 纯 EOF=响应期断连，是成功路径的副作用，按已送达对待（结果
					// 仍以版本变化确认，超时照旧标失败）；传输中断（UnexpectedEOF）
					// 才是真失败
					if errors.Is(err, io.EOF) {
						r.State = "triggered"
						r.Detail = "已送达（agent 应用升级换映像断开响应，属正常）；以列表版本变化确认结果"
						return
					}
					r.State = "error"
					r.Detail = err.Error()
					return
				}
				r.State = "triggered"
				r.Detail = "已推送，agent 将自动替换重启（版本变化即成功）"
			}(s, r)
		default:
			r.State = "skipped"
			r.Detail = "该采集方式无 agent，不参与在线升级"
		}
	}
	wg.Wait()
	return results, nil
}

// upgradeAllEligible 自动分发：全部可升级（版本不等且具备条件）的机器逐台触发。
func (m *Manager) upgradeAllEligible(ctx context.Context) int {
	latest := m.cachedVersion()
	// 先轻量筛可升级机器（不解密），仅 agent/push 的升级触发才按 id 取全行
	lites, err := m.st.ListServerLites(false)
	if err != nil {
		return 0
	}
	var ids []int64
	for _, s := range lites {
		if s.AgentVersion == "" || !update.Newer(s.AgentVersion, latest) { // 非落后（含 describe 领先形态）不碰
			continue // 旧版未报版本的机器不自动碰（人工确认后再点）
		}
		if s.CollectMode == "agent" && (!s.TokenSet || !s.HasAddr) {
			continue
		}
		ids = append(ids, s.ID)
	}
	if len(ids) == 0 {
		return 0
	}
	results, err := m.UpgradeAgents(ctx, ids)
	if err != nil {
		return 0
	}
	n := 0
	for _, r := range results {
		if r.State == "triggered" || r.State == "queued" {
			n++
			log.Printf("[update] agent 自动分发 %s（%s）: %s", r.Name, r.State, r.Detail)
		}
	}
	return n
}

// pushBundle 向一台 pull 机器直推分发束。
func (m *Manager) pushBundle(ctx context.Context, s store.Server) error {
	body, ct, err := m.buildBundle(s.Arch)
	if err != nil {
		return err
	}
	url := s.AgentAddr + "/upgrade"
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	if strings.HasPrefix(url, "http://") {
		// 明文通道上 token 与分发束均可被链路中间人观测/回放（签名保证内容
		// 不被篡改、新鲜度窗口防降级重放，但不加密）——提示而非阻断，内网
		// 部署属常态
		log.Printf("[update] 向 %s 推送升级走明文 HTTP（建议配 https 或确保内网可信）", s.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Server-Token", s.Token)
	resp, err := m.cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// buildBundle 从缓存目录组分发束（按目标机架构选包，未知架构按编译机默认）。
func (m *Manager) buildBundle(arch string) (*bytes.Buffer, string, error) {
	if arch != "amd64" && arch != "arm64" {
		arch = runtime.GOARCH
	}
	file := "lz-agent-" + arch + ".gz"
	pkg, err := os.ReadFile(filepath.Join(m.cfg.Dir, file))
	if err != nil {
		return nil, "", fmt.Errorf("缓存缺 %s（先检查更新）: %w", file, err)
	}
	raw, err := os.ReadFile(m.cachedManifestPath())
	if err != nil {
		return nil, "", fmt.Errorf("缓存缺 manifest: %w", err)
	}
	sigB, err := os.ReadFile(m.cachedSigPath())
	if err != nil {
		return nil, "", fmt.Errorf("缓存缺签名: %w", err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := update.WriteBundle(mw, raw, sigB, pkg); err != nil {
		return nil, "", err
	}
	return &body, mw.FormDataContentType(), nil
}

// BundleFor 供 push 模式 agent 回源下载分发束（API 层调用）：
// arch 取查询参数，缺省回落该机器上报记录里的架构。
func (m *Manager) BundleFor(w http.ResponseWriter, arch string, rowArch string) {
	if arch != "amd64" && arch != "arm64" {
		arch = rowArch
	}
	if arch != "amd64" && arch != "arm64" {
		arch = runtime.GOARCH
	}
	body, ct, err := m.buildBundle(arch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = io.Copy(w, body)
}

// PushInstruction 该 push 机器是否有待领的升级指令（上报响应捎带用）。
// 超过 pendingTTL 的条目就地作废（懒清理）：指令过旧说明机器一直没能升级成功，
// 继续下发只会让 agent 每个上报周期白拉一次约 8MB 分发束。
func (m *Manager) PushInstruction(id int64) any {
	m.mu.Lock()
	p, ok := m.pending[id]
	if ok && time.Since(p.at) > pendingTTL {
		delete(m.pending, id)
		ok = false
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return map[string]string{"version": p.version}
}

// ConfirmPushAgent push 机器上报带回了新版本串 → 清它的待升级标记。
func (m *Manager) ConfirmPushAgent(id int64, reportedVersion string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if target, ok := m.pending[id]; ok && reportedVersion == target.version {
		delete(m.pending, id)
	}
}
