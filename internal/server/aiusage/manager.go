package aiusage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

const (
	// PollInterval 用量轮询间隔（2026-10-03 用户指定每 20 分钟一次，不配置化）。
	PollInterval = 20 * time.Minute
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
			m.pollDue(now)
		}
	}
}

// pollDue 刷所有到期的账户（PollInterval 固定 20 分钟；静默窗内整体跳过，
// 恢复后因 lastPoll 未推进而立即补拉）。账户间并发执行，逐账户抢 busy 互斥——
// 手动刷新（RefreshNow）进行中的账户本轮跳过，不并发重复拉同一账户（407）。
func (m *Manager) pollDue(now time.Time) {
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
		if last, ok := m.lastPoll[a.ID]; ok && now.Sub(last) < PollInterval {
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
	m.refreshAccounts(due)
	m.gc(accounts)
}

// refreshAccounts 并发（上限 pollWorkers）拉取一组账户。每账户在锁内
// check-and-set busy 再开拉：抢不到（手动刷新刚抢先开始）就直接跳过，
// 杜绝同账户并发重复打上游。
func (m *Manager) refreshAccounts(accounts []store.AiAccount) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, pollWorkers)
	for _, a := range accounts {
		a := a
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
			defer func() { <-sem }()
			if _, err := m.refreshOne(a); err != nil {
				log.Printf("用量轮询失败（%s/%s）: %v", a.Name, a.Provider, err)
			}
			m.mu.Lock()
			delete(m.busy, a.ID)
			m.mu.Unlock()
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
	m.refreshAccounts(full)
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

// refreshOne 拉单账户并覆盖写库（快照 + 健康态）。
func (m *Manager) refreshOne(a store.AiAccount) (*store.AiUsageSnapshot, error) {
	snap, err := fetch(a)
	if err != nil {
		_ = m.st.RecordAiAccountStatus(a.ID, false, err.Error())
		return nil, err
	}
	if err := m.st.SetAiAccountUsage(a.ID, snap); err != nil {
		_ = m.st.RecordAiAccountStatus(a.ID, false, err.Error())
		return nil, err
	}
	_ = m.st.RecordAiAccountStatus(a.ID, true, "")
	return snap, nil
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
