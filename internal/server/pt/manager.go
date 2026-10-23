package pt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"lz-monitor/internal/ptfetch"
	"lz-monitor/internal/server/store"
)

// PT 站点抓取调度（391，2026-10-04 用户指定每天 06:00 一次；2026-10-05 扩展
// 出口中转）：站点按 proxy_server_id 分流——0=面板直连（本机网络直接抓），
// >0=经该监控服务器的 push agent 中转（任务队列：server 入队 → agent 上报时
// 领取 → 回传结果；家里 NAS 等零暴露机器）。两种出口共用 06:00 每日轮 +
// 启动补拉 + 手动刷新的节奏。防死循环纪律见 store/pt_task.go 注释。

// catchupDelay 启动后补拉前的等待（避开服务启动风暴）。
const catchupDelay = 2 * time.Minute

// pollWorkers 手动/定时整轮抓取的并发度（站点间并行、站内串行）。
const pollWorkers = 4

// ErrQueued 中转站点刷新请求已转为队列任务（agent 下轮上报时领取），非错误。
var ErrQueued = errors.New("已下发到出口服务器，稍候自动更新")

// Manager 站点抓取调度：常驻 06:00 定时 + 手动刷新，仿 aiusage/mail Manager。
type Manager struct {
	st        *store.Store
	mu        sync.Mutex
	busy      map[int64]bool
	roundBusy bool // 直连整轮抓取进行中（定时与手动共用一把，防叠加轮次）

	lastDailyDay string // 已跑过每日轮的日期（Run 循环内独占，无需加锁）

	// fetcher 单站抓取入口（New 指到 fetchOne）：测试注入 panic 验证隔离用。
	fetcher func(store.PtSite) (*store.PtSnapshot, error)
}

// New 构造。
func New(st *store.Store) *Manager {
	m := &Manager{st: st, busy: map[int64]bool{}}
	m.fetcher = m.fetchOne
	return m
}

// nextRunAt 下一个本地 06:00。
func nextRunAt(now time.Time) time.Time {
	n := time.Date(now.Year(), now.Month(), now.Day(), 6, 0, 0, 0, now.Location())
	if !n.After(now) {
		n = n.AddDate(0, 0, 1)
	}
	return n
}

// Run 常驻调度（ctx 取消退出）：启动 2 分钟后补拉错过最近 06:00 轮的站点；
// 此后每分钟醒一次做 ①中转任务租约超时回收 ②跨过本地 06:00 时跑每日轮
// （运行中跨天才触发，启动时已过 06:00 的当天不补跑——由启动补拉兜底）。
func (m *Manager) Run(ctx context.Context) {
	log.Printf("PT 调度启动：每日 06:00 轮 + 启动 2 分钟后补拉 + 中转任务租约回收")
	timer := time.AfterFunc(catchupDelay, m.catchUpStale)
	defer timer.Stop()
	// 启动初始化当日轮水位：6 点后起进程=今天这轮视为已过（过期站点由启动
	// 补拉兜底）；6 点前起进程=记昨天，跨过 06:00 正常触发今天的轮
	if n := time.Now(); n.After(todaySix(n)) {
		m.lastDailyDay = n.Format("2006-01-02")
	} else {
		m.lastDailyDay = n.AddDate(0, 0, -1).Format("2006-01-02")
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if dead := m.st.ReclaimExpiredPtTasks(); dead > 0 {
				log.Printf("PT 中转任务租约超时：%d 个已达重派上限转失败", dead)
			}
			if now.After(todaySix(now)) {
				day := now.Format("2006-01-02")
				if m.lastDailyDay != day {
					// 读库失败不吞当天轮次：水位不动，下一分钟 tick 重试
					if m.DailyRound() {
						m.lastDailyDay = day
					}
				}
			}
		}
	}
}

// todaySix 当天本地 06:00。
func todaySix(n time.Time) time.Time {
	return time.Date(n.Year(), n.Month(), n.Day(), 6, 0, 0, 0, n.Location())
}

// lastPastSix 最近一个已过去的本地 06:00（抓取日锚点，与中转任务频控同口径）。
func lastPastSix(now time.Time) time.Time {
	n := todaySix(now)
	if n.After(now) {
		n = n.AddDate(0, 0, -1)
	}
	return n
}

// missedRound 站点是否错过最近一轮 06:00（上次成功早于它）。启动补拉按这个
// 口径兜底：重启跨过 06:00 后，当天还没成功过的站一律补抓——原「24h 无成功」
// 判定下，前晚刚更过的站重启后要再等一整天（2026-10-06 用户报障 442）。
func missedRound(lastOKAt int64, now time.Time) bool {
	return lastOKAt == 0 || lastOKAt < lastPastSix(now).Unix()
}

// splitSites 按出口分流：直连 / 中转（出口服务器不在或站点停用的不抓）。
func splitSites(sites []store.PtSite) (direct, proxied []store.PtSite) {
	for _, s := range sites {
		if !s.Enabled {
			continue
		}
		if s.ProxyServerID > 0 {
			proxied = append(proxied, s)
		} else {
			direct = append(direct, s)
		}
	}
	return
}

// beginRound 直连整轮开闸：未在轮中则置位并启动抓取 goroutine（空列表也启动，
// 让复位路径唯一）。返回 false=上一轮未收尾，本轮放弃（调用方自行记日志）。
// 461 教训：len(direct)==0 时不启动 goroutine 会把 roundBusy 永久卡 true——
// 此后每日轮静默跳过、手动整轮恒被拒。
func (m *Manager) beginRound(direct []store.PtSite) bool {
	m.mu.Lock()
	busy := m.roundBusy
	if !busy {
		m.roundBusy = true
	}
	m.mu.Unlock()
	if busy {
		return false
	}
	go func() {
		m.refreshList(direct)
		m.mu.Lock()
		m.roundBusy = false
		m.mu.Unlock()
	}()
	return true
}

// DailyRound 每日轮（06:00 与启动补拉共用节奏）：直连站并发抓一轮，中转站
// 逐个入队（去重/频控由任务表兜底，失败仅记日志——下个轮次或手动刷新兜底）。
// 返回 false 仅当站点清单读库失败（Run 下一分钟重试，当天轮次不因瞬时抖动
// 丢失）；站点级失败/panic 由 fetchSiteSafe/enqueueSites 逐站隔离。
func (m *Manager) DailyRound() bool {
	sites, err := m.st.ListPtSites()
	if err != nil {
		log.Printf("PT 每日轮读站点清单失败（下一分钟重试）: %v", err)
		return false
	}
	direct, proxied := splitSites(sites)
	if m.beginRound(direct) {
		log.Printf("PT 每日轮开跑：直连 %d 站并发抓取 + 中转 %d 站入队", len(direct), len(proxied))
	} else {
		log.Printf("PT 每日轮跳过：上一轮直连抓取仍未收尾")
	}
	m.enqueueSites(proxied)
	return true
}

// enqueueSites 中转站点入队（防死循环的入队查重/频控在 store 层）。
func (m *Manager) enqueueSites(sites []store.PtSite) {
	for _, s := range sites {
		if _, err := m.st.CreatePtTask(&s, s.ProxyServerID); err != nil {
			if errors.Is(err, store.ErrPtTaskActive) || errors.Is(err, store.ErrPtTaskRateLimit) {
				continue // 已有任务在途/频控拦截：静默跳过（正常路径）
			}
			log.Printf("PT 中转任务入队失败（%s）: %v", s.Name, err)
		}
	}
}

// catchUpStale 启动补拉：启用站点里错过最近一个 06:00 轮的（上次成功早于它，
// 服务停机跨 06:00 / 06:00 后重启部署都算），补拉保当天有数据——直连直接抓、
// 中转入队。
func (m *Manager) catchUpStale() {
	sites, err := m.st.ListPtSites()
	if err != nil {
		log.Printf("PT 启动补拉读站点清单失败: %v", err)
		return
	}
	var stale []store.PtSite
	for _, s := range sites {
		if s.Enabled && missedRound(s.LastOKAt, time.Now()) {
			stale = append(stale, s)
		}
	}
	if len(stale) == 0 {
		return
	}
	direct, proxied := splitSites(stale)
	log.Printf("PT 站点启动补拉：%d 个站点数据过期（直连 %d + 中转 %d）", len(stale), len(direct), len(proxied))
	if !m.beginRound(direct) {
		log.Printf("PT 启动补拉跳过直连：整轮抓取仍在进行")
	}
	m.enqueueSites(proxied)
}

// RefreshAllAsync 手动「全部刷新」：直连站异步整轮（进行中则拒绝防叠加），
// 中转站入队（幂等，永远安全）。返回 false 仅表示直连轮被拒。
func (m *Manager) RefreshAllAsync() bool {
	m.mu.Lock()
	if m.roundBusy {
		m.mu.Unlock()
		return false
	}
	m.roundBusy = true
	m.mu.Unlock()
	sites, err := m.st.ListPtSites()
	if err != nil {
		m.mu.Lock()
		m.roundBusy = false
		m.mu.Unlock()
		return false
	}
	direct, proxied := splitSites(sites)
	m.enqueueSites(proxied)
	go func() {
		m.refreshList(direct)
		m.mu.Lock()
		m.roundBusy = false
		m.mu.Unlock()
	}()
	return true
}

// RefreshAllSync 同步整轮（单测用，语义同异步但等抓完）。
func (m *Manager) RefreshAllSync() bool {
	m.mu.Lock()
	if m.roundBusy {
		m.mu.Unlock()
		return false
	}
	m.roundBusy = true
	m.mu.Unlock()
	sites, err := m.st.ListPtSites()
	if err != nil {
		m.mu.Lock()
		m.roundBusy = false
		m.mu.Unlock()
		return false
	}
	direct, proxied := splitSites(sites)
	m.enqueueSites(proxied)
	m.refreshList(direct)
	m.mu.Lock()
	m.roundBusy = false
	m.mu.Unlock()
	return true
}

// refreshList 并发抓一轮（pollWorkers 路；同站点 busy 防与手动刷新重复打）。
func (m *Manager) refreshList(sites []store.PtSite) {
	sem := make(chan struct{}, pollWorkers)
	var wg sync.WaitGroup
	for _, s := range sites {
		s := s
		m.mu.Lock()
		if m.busy[s.ID] {
			m.mu.Unlock()
			continue
		}
		m.busy[s.ID] = true
		m.mu.Unlock()
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() {
				<-sem
				wg.Done()
				m.mu.Lock()
				delete(m.busy, s.ID)
				m.mu.Unlock()
			}()
			m.fetchSiteSafe(s)
		}()
	}
	wg.Wait()
}

// fetchSiteSafe 单站抓取的隔离壳：站点链路里任何未预期 panic（站点返回怪异
// 内容触发解析 bug 等）就地回收、记进该站健康态，绝不带崩整轮/整个进程
// （2026-10-06 用户要求：任一站点报错不影响其他站点继续）。
func (m *Manager) fetchSiteSafe(s store.PtSite) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PT 站点抓取内部错误（已隔离，不影响其他站点）（%s/%s）: %v", s.Name, s.SiteKey, r)
			_ = m.st.RecordPtSiteStatus(s.ID, false, fmt.Sprintf("内部错误: %v", r))
		}
	}()
	if _, err := m.fetcher(s); err != nil {
		log.Printf("PT 站点抓取失败（%s/%s）: %v", s.Name, s.SiteKey, err)
	}
}

// RefreshNow 手动刷新单站点：直连同步返回最新快照供页面原位替换；中转站
// 入队（返回 ErrQueued 哨兵，页面提示后轮询列表）。
func (m *Manager) RefreshNow(id int64) (*store.PtSnapshot, error) {
	s, err := m.st.GetPtSite(id)
	if err != nil {
		return nil, err
	}
	if s.ProxyServerID > 0 {
		if _, err := m.st.CreatePtTask(s, s.ProxyServerID); err != nil {
			if errors.Is(err, store.ErrPtTaskActive) {
				return nil, ErrQueued // 已有任务在途：对页面是「稍候自动更新」
			}
			if errors.Is(err, store.ErrPtTaskRateLimit) {
				return nil, fmt.Errorf("该站点今日抓取次数已达上限（防频繁请求站点的硬性保险），下一个 06:00 自动恢复")
			}
			return nil, err
		}
		return nil, ErrQueued
	}
	m.mu.Lock()
	if m.busy[s.ID] {
		m.mu.Unlock()
		return nil, fmt.Errorf("该站点正在抓取中，请稍候")
	}
	m.busy[s.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, s.ID)
		m.mu.Unlock()
	}()
	snap, err := m.fetchOne(*s)
	if err != nil {
		// 手动刷新失败只有 502 状态行，原因落一条服务日志便于定位
		log.Printf("PT 站点手动刷新失败（%s/%s）: %v", s.Name, s.SiteKey, err)
	}
	return snap, err
}

// SigninNow 手动签到（416，dian115/hdhaven 类站）：直连立即「签到+抓取」并
// 覆盖写库，返回结果文案；中转站入队（agent 执行时按站点配置的签到模式带签），
// 返回 ErrQueued。手动点按是显式动作，站点未配自动签到时回落 normal 档。
// 已签今日的站点走快照短路（445）：不发任何对站请求、不入队，直接提示
// 今日已签到（刷新链路同样不打签到接口——ptfetch 层按站点已签状态跳过，
// 有测试钉死）。
func (m *Manager) SigninNow(id int64) (*store.PtSnapshot, string, error) {
	s, err := m.st.GetPtSite(id)
	if err != nil {
		return nil, "", err
	}
	if msg, ok := signedTodayMsg(s); ok {
		return nil, msg, nil
	}
	if s.ProxyServerID > 0 {
		if _, err := m.st.CreatePtTask(s, s.ProxyServerID); err != nil {
			if errors.Is(err, store.ErrPtTaskActive) || errors.Is(err, store.ErrPtTaskRateLimit) {
				return nil, "", ErrQueued
			}
			return nil, "", err
		}
		return nil, "", ErrQueued
	}
	m.mu.Lock()
	if m.busy[s.ID] {
		m.mu.Unlock()
		return nil, "", fmt.Errorf("该站点正在抓取中，请稍候")
	}
	m.busy[s.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, s.ID)
		m.mu.Unlock()
	}()
	snap, msg, err := fetchWithSignin(*s)
	if err != nil {
		_ = m.st.RecordPtSiteStatus(s.ID, false, err.Error())
		return nil, "", err
	}
	if err := m.st.SetPtSiteData(s.ID, snap); err != nil {
		_ = m.st.RecordPtSiteStatus(s.ID, false, err.Error())
		return nil, "", err
	}
	_ = m.st.RecordPtSiteStatus(s.ID, true, "")
	return snap, msg, nil
}

// signedTodayMsg 站点现有快照「今天抓到的 + 已签」→ 可短路签到。已签状态信
// 快照（dian115 东八区 / hdhaven 站点口径，抓取时已算好）；「今天抓的」按服务器
// 本地日界粗判——判轻（跨时区临界）走正常链路由站点 already_signed 兜底，
// 判重最多省一次查询，两侧都无害。
func signedTodayMsg(s *store.PtSite) (string, bool) {
	if s.LastOKAt == 0 || time.Unix(s.LastOKAt, 0).Format("2006-01-02") != time.Now().Format("2006-01-02") {
		return "", false
	}
	snap, err := DecodeSnapshot(s.DataJSON)
	if err != nil || snap == nil {
		return "", false
	}
	if (snap.Dian != nil && snap.Dian.SignedToday) || (snap.Hdh != nil && snap.Hdh.SignedToday) ||
		(snap.Fc != nil && snap.Fc.SignedToday) {
		return "今日已签到", true
	}
	return "", false
}

// fetchOne 抓单站点并覆盖写库（快照 + 健康态）。
func (m *Manager) fetchOne(s store.PtSite) (*store.PtSnapshot, error) {
	snap, err := FetchSite(s)
	if err != nil {
		_ = m.st.RecordPtSiteStatus(s.ID, false, err.Error())
		return nil, err
	}
	if err := m.st.SetPtSiteData(s.ID, snap); err != nil {
		_ = m.st.RecordPtSiteStatus(s.ID, false, err.Error())
		return nil, err
	}
	_ = m.st.RecordPtSiteStatus(s.ID, true, "")
	return snap, nil
}

// FetchSite 抓取分发（ptfetch 叶子包的封装：store 行 → 最小入参）。agent 中转
// 执行直接调 ptfetch.FetchSite（不带 store，agent 二进制不链 sqlite）。
func FetchSite(s store.PtSite) (*store.PtSnapshot, error) {
	return ptfetch.FetchSite(ptfetch.Site{SiteKey: s.SiteKey, BaseURL: s.BaseURL, Cookie: s.Cookie, UA: s.UA, SigninMode: s.SigninMode})
}

// fetchWithSignin 强制签到 + 抓取（手动签到按钮用；签到模式空配置回落 normal）。
// 无签到语义的站点退化为普通抓取。
func fetchWithSignin(s store.PtSite) (*store.PtSnapshot, string, error) {
	def := SiteDefByKey(s.SiteKey)
	in := ptfetch.Site{SiteKey: s.SiteKey, BaseURL: s.BaseURL, Cookie: s.Cookie, UA: s.UA, SigninMode: s.SigninMode}
	if def != nil && def.Kind == "dian115" {
		return ptfetch.SigninDian115(in)
	}
	if def != nil && def.Kind == "hdhaven" {
		return ptfetch.SigninHDHaven(in)
	}
	if def != nil && def.Kind == "fengchao" {
		return ptfetch.SigninFengchao(in)
	}
	snap, err := FetchSite(s)
	return snap, "", err
}

// 目录与工具函数转发（api 层经本包使用，保持既有调用面不变）。
func Catalog() []ptfetch.SiteDef             { return ptfetch.Catalog() }
func SiteDefByKey(k string) *ptfetch.SiteDef { return ptfetch.SiteDefByKey(k) }
func CleanCookie(raw string) string          { return ptfetch.CleanCookie(raw) }

// Test 连接测试（连接测试按钮用；不写库）。
func Test(s store.PtSite) error {
	_, err := TestFetch(s)
	return err
}

// TestFetch 连接测试并带回快照：先测后存成功后由调用方直接落库这份快照，
// 省掉保存后再 RefreshNow 的第二轮抓取（同站请求量减半——对 PT 站少打扰）。
func TestFetch(s store.PtSite) (*store.PtSnapshot, error) {
	snap, err := FetchSite(s)
	if err != nil {
		return nil, err
	}
	if snap.Username == "" {
		return nil, fmt.Errorf("已连通但未取到用户信息（凭据可能失效）")
	}
	return snap, nil
}

// Drop 删除站点时清调度状态与其全部任务。
func (m *Manager) Drop(id int64) {
	m.mu.Lock()
	delete(m.busy, id)
	m.mu.Unlock()
	m.st.DropPtTasksBySite(id)
}

// DecodeSnapshot 库内快照 JSON → 结构体（空串返回 nil，页面按「尚未抓取」展示）。
func DecodeSnapshot(raw string) (*store.PtSnapshot, error) {
	if raw == "" {
		return nil, nil
	}
	var v store.PtSnapshot
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err
	}
	return &v, nil
}
