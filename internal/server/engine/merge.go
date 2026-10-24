package engine

// 通知聚合：多台设备短时间内同时告警/恢复（如交换机故障导致一批机器离线）
// 时合并成一条通知——站内消息与推送渠道同口径，避免服务器多时手机被刷屏。
//
// 语义：事件进入缓冲后，静默 mergeQuiet 秒（无新事件）即认为本轮结束、
// 立即合并外发；持续有新事件则最迟 mergeMax 秒强制外发（防止慢漏斗无限拖延）。
// 触发与恢复分开合成（各自一条），共用同一窗口计时。单条事件同样经过
// 静默等待，即独立告警的推送会延迟约 mergeQuiet 秒——合并的固有代价。

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

const (
	mergeQuiet = 60 * time.Second // 静默多久认为本轮事件结束
	mergeMax   = 10 * time.Minute // 聚合窗上限（同一时间窗内的事件合并成一条）
	mergeTick  = 10 * time.Second // flush 检查周期
)

// merger 事件聚合器：add 进缓冲，满足窗口条件后合成单条事件发下游渠道。
type merger struct {
	notifiers []Notifier
	st        *store.Store // 补服务器名（引擎构造的 Alert 不带 name）

	mu      sync.Mutex
	trig    []AlertEvent
	res     []AlertEvent
	firstAt time.Time
	lastAt  time.Time
}

func newMerger(st *store.Store, notifiers []Notifier) *merger {
	return &merger{notifiers: notifiers, st: st}
}

func (m *merger) add(ev AlertEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.firstAt.IsZero() {
		m.firstAt = now
	}
	m.lastAt = now
	if ev.Action == "resolved" {
		m.res = append(m.res, ev)
	} else {
		m.trig = append(m.trig, ev)
	}
}

// shouldFlush 窗口结束判定：缓冲非空且（静默超时 或 达到聚合上限）。
func (m *merger) shouldFlush(now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.firstAt.IsZero() {
		return false
	}
	return now.Sub(m.lastAt) >= mergeQuiet || now.Sub(m.firstAt) >= mergeMax
}

// flush 取走全部缓冲并合成外发（触发/恢复各一条）。
func (m *merger) flush() {
	m.mu.Lock()
	trig, res := m.trig, m.res
	m.trig, m.res = nil, nil
	m.firstAt, m.lastAt = time.Time{}, time.Time{}
	m.mu.Unlock()
	if ev, ok := mergeEvents(m.st, "triggered", trig); ok {
		m.emit(ev)
	}
	if ev, ok := mergeEvents(m.st, "resolved", res); ok {
		m.emit(ev)
	}
}

func (m *merger) emit(ev AlertEvent) {
	for _, n := range m.notifiers {
		n.Notify(ev)
	}
}

// run 启动 flush 循环（由 Engine.RunMerge 拉起，与 RunOfflineCheck 同款装配）。
func (m *merger) run(ctx context.Context) {
	ticker := time.NewTicker(mergeTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// 退出前把缓冲里的事件发出去，尽量不丢通知
			m.flush()
			return
		case <-ticker.C:
			if m.shouldFlush(time.Now()) {
				m.flush()
			}
		}
	}
}

// mergeEvents 把一组同类事件合成为一条：ServerName 概括台数，Detail 逐台
// 列出（服务器名 + 详情），首条告警字段兜底（渠道文案里的规则号/时间等）。
func mergeEvents(st *store.Store, action string, evs []AlertEvent) (AlertEvent, bool) {
	if len(evs) == 0 {
		return AlertEvent{}, false
	}
	first := evs[0].Alert
	names := make(map[int64]string, len(evs))
	if st != nil {
		for _, ev := range evs {
			if _, ok := names[ev.Alert.ServerID]; !ok {
				if srv, err := st.GetServer(ev.Alert.ServerID); err == nil && srv != nil {
					names[ev.Alert.ServerID] = srv.Name
				}
			}
		}
	}
	srvName := func(id int64) string {
		if n := names[id]; n != "" {
			return n
		}
		return "#" + strconv.FormatInt(id, 10)
	}

	name := fmt.Sprintf("%d 台服务器", len(evs))
	if len(evs) == 1 {
		name = srvName(first.ServerID)
	}
	detail := fmt.Sprintf("共 %d 项%s：", len(evs), evActionCN(action))
	for i, ev := range evs {
		if i >= 20 { // 详情封顶 20 行，更多折叠成计数（推送体积保护）
			detail += fmt.Sprintf("\n… 其余 %d 项略", len(evs)-i)
			break
		}
		detail += fmt.Sprintf("\n· %s：%s", srvName(ev.Alert.ServerID), ev.Alert.Detail)
	}
	first.ServerName = name
	first.Detail = detail
	return AlertEvent{Action: action, Alert: first}, true
}
