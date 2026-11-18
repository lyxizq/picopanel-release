package aiusage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

const (
	// PollInterval 用量轮询间隔（2026-10-03 用户指定每 20 分钟一次，不配置化）。
	PollInterval = 20 * time.Minute
	// WxPollInterval 和风控制台账户单独降频（2026-10-23 用户指定 1 小时一次）：
	// 控制台数据延迟 ≥1 小时，20 分钟一拍没有信息增益，1 小时一拍约 39 次/天。
	WxPollInterval = time.Hour
	// schedTick 调度器节拍（检查到期账户的最小粒度）。
	schedTick = 15 * time.Second
	// pollWorkers 账户间并发上限（407：慢上游不再拖慢其他账户的轮询节拍）。
	pollWorkers = 4
)

// inQuietHours 轮询静默窗（2026-10-04 用户指定）：本地时间 01:00–05:30 定时
// 轮询停止（手动刷新不受限）；05:30 一过即恢复，到期账户立即补拉。
func inQuietHours(t time.Time) bool {
	mins := t.Hour()*60 + t.Minute()
	return mins >= 60 && mins < 5*60+30
}

// Manager 按账户轮询 AI 用量（仅存最新一份快照，覆盖式），仿 mail Manager：
// 常驻调度 + 账户级失败互不影响。页面与角标读库直出，不在内存另存一份。
type Manager struct {
	st       *store.Store
	mu       sync.Mutex
	lastPoll map[int64]time.Time
	busy     map[int64]bool // 手动刷新与轮询防同账户并发重复拉
}

// New 构造。
func New(st *store.Store) *Manager {
	return &Manager{st: st, lastPoll: map[int64]time.Time{}, busy: map[int64]bool{}}
}

// Run 常驻调度（ctx 取消退出）：启动先刷一轮（静默窗内则跳过，等恢复后补拉），
// 此后每 20 分钟到期即刷。
func (m *Manager) Run(ctx context.Context) {
	if !inQuietHours(time.Now()) {
		m.RefreshAll()
	}
	t := time.NewTicker(schedTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.pollDue(ctx, now)
		}
	}
}

// pollDue 刷所有到期的账户（PollInterval 固定 20 分钟；静默窗内整体跳过，
// 恢复后因 lastPoll 未推进而立即补拉）。账户间并发执行，逐账户抢 busy 互斥——
// 手动刷新（RefreshNow）进行中的账户本轮跳过，不并发重复拉同一账户（407）。
func (m *Manager) pollDue(ctx context.Context, now time.Time) {
	if inQuietHours(now) {
		return
	}
	// 清单走轻量元信息（不解密），仅到期账户按 id 取明文凭据——15s 一拍的
	// 调度不再每拍解密全部账户凭据
	accounts, err := m.st.ListAiAccountMetas()
	if err != nil {
		return
	}
	var dueIDs []int64
	m.mu.Lock()
	for _, a := range accounts {
		if m.busy[a.ID] { // 手动刷新进行中：本轮跳过（其 lastPoll 已由 RefreshNow 推进）
			continue
		}
		interval := PollInterval
		if a.Provider == store.QWeatherProvider { // 和风控制台数据延迟 ≥1h，单独降频
			interval = WxPollInterval
		}
		if last, ok := m.lastPoll[a.ID]; ok && now.Sub(last) < interval {
			continue
		}
		m.lastPoll[a.ID] = now
		dueIDs = append(dueIDs, a.ID)
	}
	m.mu.Unlock()
	var due []store.AiAccount
	for _, id := range dueIDs {
		if a, err := m.st.GetAiAccount(id); err == nil {
			due = append(due, *a)
		}
	}
	m.refreshAccounts(ctx, due)
	m.gc(accounts)
}

// refreshAccounts 并发（上限 pollWorkers）拉取一组账户。每账户在锁内
// check-and-set busy 再开拉：抢不到（手动刷新刚抢先开始）就直接跳过，
// 杜绝同账户并发重复打上游。ctx 取消后停止派发新账户（在途的几个等各自的
// HTTP 超时自然收尾）——停机不再被整轮上游拖住。
func (m *Manager) refreshAccounts(ctx context.Context, accounts []store.AiAccount) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, pollWorkers)
	for _, a := range accounts {
		a := a
		select {
		case <-ctx.Done():
			return // 停止派发新账户（select 里 break 只跳出 select，勿改回）
		default:
		}
		m.mu.Lock()
		if m.busy[a.ID] {
			m.mu.Unlock()
			continue
		}
		m.busy[a.ID] = true
		m.mu.Unlock()
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { // busy 标记无论成败（含 panic 隔离）都要清，否则该账户不再轮询
				m.mu.Lock()
				delete(m.busy, a.ID)
				m.mu.Unlock()
			}()
			defer func() {
				if p := recover(); p != nil {
					log.Printf("用量轮询 panic（%s/%s，已隔离）: %v\n%s", a.Name, a.Provider, p, debug.Stack())
				}
			}()
			defer func() { <-sem }()
			if _, err := m.refreshOne(a); err != nil {
				log.Printf("用量轮询失败（%s/%s）: %v", a.Name, a.Provider, err)
			}
		}()
	}
	wg.Wait()
}

// RefreshAll 立即刷全部账户（启动/新建/编辑后调用）。
func (m *Manager) RefreshAll() {
	accounts, err := m.st.ListAiAccountMetas()
	if err != nil {
		return
	}
	now := time.Now()
	m.mu.Lock()
	for _, a := range accounts {
		m.lastPoll[a.ID] = now
	}
	m.mu.Unlock()
	var full []store.AiAccount
	for _, a := range accounts {
		if one, err := m.st.GetAiAccount(a.ID); err == nil {
			full = append(full, *one)
		}
	}
	m.refreshAccounts(context.Background(), full)
	m.gc(accounts)
}

// RefreshNow 手动刷单账户（API 直接返回快照，页面即时更新），返回账户的
// 最新快照。同账户并发调用时等待的一方拿已有结果即可（不重复打上游）。
func (m *Manager) RefreshNow(id int64) (*store.AiUsageSnapshot, error) {
	a, err := m.st.GetAiAccount(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.busy[a.ID] {
		m.mu.Unlock()
		m.waitIdle(a.ID)
		fresh, err := m.st.GetAiAccount(id)
		if err != nil {
			return nil, err
		}
		return DecodeSnapshot(fresh.UsageJSON)
	}
	m.busy[a.ID] = true
	m.lastPoll[a.ID] = time.Now()
	m.mu.Unlock()
	snap, err := m.refreshOne(*a)
	m.mu.Lock()
	delete(m.busy, a.ID)
	m.mu.Unlock()
	return snap, err
}

// refreshOne 拉单账户并覆盖写库（快照 + 健康态）。和风账户顺带把完整小时桶
// 幂等落请求量账本、并把当月合计与近 30 天日均回填进快照（3 的当月请求量 +
// 9 的日均口径，官方均无此统计）。
func (m *Manager) refreshOne(a store.AiAccount) (*store.AiUsageSnapshot, error) {
	snap, err := fetch(a)
	if err != nil {
		_ = m.st.RecordAiAccountStatus(a.ID, false, err.Error())
		return nil, err
	}
	if snap.Wx != nil && snap.Wx.Stats != nil {
		m.recordWxHours(snap.Wx.Stats)
	}
	if snap.Wx != nil {
		if ms, me, ok, err := m.st.WxMonthUsage(); err != nil {
			log.Printf("和风当月请求量合计失败: %v", err)
		} else if ok {
			snap.Wx.MonthSuccess, snap.Wx.MonthErrors = &ms, &me
		}
		if ds, de, ok, err := m.st.WxDailyAvg(); err != nil {
			log.Printf("和风日均请求量合计失败: %v", err)
		} else if ok {
			snap.Wx.DayAvgSuccess, snap.Wx.DayAvgErrors = &ds, &de
		}
	}
	if err := m.st.SetAiAccountUsage(a.ID, snap); err != nil {
		_ = m.st.RecordAiAccountStatus(a.ID, false, err.Error())
		return nil, err
	}
	_ = m.st.RecordAiAccountStatus(a.ID, true, "")
	return snap, nil
}

// recordWxHours 只把「完整」小时桶交给账本：桶起点早于 asOf 桶的才完整，
// 进行中的当前小时是半程值，落库会被 INSERT OR IGNORE 钉死造成永久少计；
// asOf 不可解析时宁缺勿错，整轮不入账。
func (m *Manager) recordWxHours(st *store.WxStats) {
	cutoff := qwParseTime(st.AsOf)
	if cutoff.IsZero() {
		return
	}
	complete := make([]store.WxHour, 0, len(st.Hours))
	for _, h := range st.Hours {
		if h.Ts > 0 && h.Ts < cutoff.Unix() {
			complete = append(complete, h)
		}
	}
	if len(complete) == 0 {
		return
	}
	if err := m.st.RecordWxHours(complete); err != nil {
		log.Printf("和风请求量账本写入失败: %v", err)
	}
}

// fetch 按服务商分发。
func fetch(a store.AiAccount) (*store.AiUsageSnapshot, error) {
	switch a.Provider {
	case "zhipu":
		return fetchZhipu(a)
	case "deepseek":
		return fetchDeepSeek(a)
	case "edgeone":
		return fetchEdgeOne(a)
	case store.QWeatherProvider: // 系统账户行（3），凭据来自前台页面天气配置
		return fetchQWeather(a)
	}
	return nil, fmt.Errorf("未知服务商 %q", a.Provider)
}

// Fetch 拉取账户最新用量快照（不写库，API 手动刷新用）。
func Fetch(a store.AiAccount) (*store.AiUsageSnapshot, error) { return fetch(a) }

// Test 连接测试（先测后存用；不写库）。
func Test(a store.AiAccount) error {
	_, err := fetch(a)
	return err
}

// waitIdle 等同账户的进行中拉取结束（忙标记消失）。
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

func (m *Manager) gc(accounts []store.AiAccount) {
	alive := map[int64]bool{}
	for _, a := range accounts {
		alive[a.ID] = true
	}
	m.mu.Lock()
	for id := range m.lastPoll {
		if !alive[id] {
			delete(m.lastPoll, id)
			delete(m.busy, id)
		}
	}
	m.mu.Unlock()
}

// Drop 删除账户时清调度状态与降级日志去重状态。
func (m *Manager) Drop(id int64) {
	m.mu.Lock()
	delete(m.lastPoll, id)
	delete(m.busy, id)
	m.mu.Unlock()
	clearDegraded(id)
}

// DecodeSnapshot 库内快照 JSON → 结构体（空串返回 nil，页面按「尚未拉取」展示）。
func DecodeSnapshot(raw string) (*store.AiUsageSnapshot, error) {
	if raw == "" {
		return nil, nil
	}
	var s store.AiUsageSnapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, err
	}
	return &s, nil
}
