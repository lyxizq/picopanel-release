// Package engine 实现告警引擎：规则缓存、阈值评估、持续判定与状态机。
//
// 评估由每份上报同步触发（写入 metrics 之后），离线检测由独立 ticker 驱动。
// "持续 N 分钟"采用无状态判定：直接查 metrics 表时间窗口内样本比例，
// 不在内存记录"条件首次满足时间"——agent/服务端重启都能自愈。
package engine

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
)

// Config 是引擎配置。
type Config struct {
	OfflineAfter time.Duration // 离线判定时长
}

// touchInterval firing 告警观测值刷新的最小间隔：告警中每次上报都 TouchAlert
// 是纯写放大（面板展示的「当前值」60s 内更新足够），2026-11-03 性能修。
const touchInterval = 60

// firingKey (规则,服务器) 二元组——firing 告警缓存与刷新节流的键。
type firingKey struct {
	rule, server int64
}

// AlertEvent 是通知渠道收到的事件。Action 为 "triggered" 或 "resolved"。
type AlertEvent struct {
	Action string
	Alert  store.Alert
}

// Notifier 是告警通知渠道扩展点（钉钉/邮件等后续在此新增实现）。
type Notifier interface {
	Notify(ev AlertEvent)
}

// ConsoleNotifier 基础通知渠道：写日志（Web 告警列表由引擎直接写 alerts 表）。
// 与邮件通知器等并列注入 New，多个渠道并存。
type ConsoleNotifier struct{}

func (ConsoleNotifier) Notify(ev AlertEvent) {
	log.Printf("[告警%s] server=%d %s", evActionCN(ev.Action), ev.Alert.ServerID, ev.Alert.Detail)
}

func evActionCN(action string) string {
	if action == "resolved" {
		return "恢复"
	}
	return "触发"
}

// Engine 是告警引擎。
type Engine struct {
	store     *store.Store
	cfg       Config
	mu        sync.RWMutex
	rules     []store.Rule
	notifiers []Notifier
	merger    *merger // 通知聚合器（EnableMerge 开启；nil=事件直发渠道）

	// firing 告警内存缓存（2026-11-03 性能修）：Evaluate 是最高频路径（每份
	// 上报 × 每条规则），原先每次都点查一次 alerts 表判 firing——常态（不命中）
	// 也要在单连接上排一次查询。缓存 key=(规则,服务器)，建/解告警时增删；
	// 行被外部删除（Touch 零行）时自动失效回源重查。
	firingMu sync.Mutex
	firing   map[firingKey]int64
	touchAt  map[firingKey]int64
}

// New 创建引擎并加载规则缓存。notifiers 为通知渠道（未传时退回日志渠道）。
func New(st *store.Store, cfg Config, notifiers ...Notifier) *Engine {
	if len(notifiers) == 0 {
		notifiers = append(notifiers, ConsoleNotifier{})
	}
	e := &Engine{store: st, cfg: cfg, notifiers: notifiers,
		firing: map[firingKey]int64{}, touchAt: map[firingKey]int64{}}
	if err := e.ReloadRules(); err != nil {
		log.Printf("加载告警规则失败: %v", err)
	}
	return e
}

// ReloadRules 重新加载规则缓存（规则 CRUD 后调用；启动时 New 内已调用）。
func (e *Engine) ReloadRules() error {
	rules, err := e.store.ListRules()
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.rules = rules
	e.mu.Unlock()
	return nil
}

// Evaluate 对一份刚落库的上报评估全部适用规则。
func (e *Engine) Evaluate(serverID int64, r *proto.Report, now int64) {
	e.mu.RLock()
	rules := make([]store.Rule, len(e.rules))
	copy(rules, e.rules)
	e.mu.RUnlock()

	for i := range rules {
		rule := &rules[i]
		if !rule.Enabled {
			continue
		}
		if rule.ServerID != nil && *rule.ServerID != serverID {
			continue
		}
		e.evalRule(rule, serverID, r, now)
	}
}

func (e *Engine) evalRule(rule *store.Rule, serverID int64, r *proto.Report, now int64) {
	val, ctx, ok := extract(rule, r)
	if !ok {
		return // 如规则指定的 mount/iface 在本机不存在
	}
	hit := compare(rule.Op, val, rule.Threshold)
	key := firingKey{rule.ID, serverID}
	openID, hasOpen := e.cachedFiring(key)
	if !hasOpen {
		var err error
		openID, hasOpen, err = e.store.OpenAlertID(rule.ID, serverID)
		if err != nil {
			log.Printf("查询 firing 告警失败 (rule=%d server=%d): %v", rule.ID, serverID, err)
			return
		}
		if hasOpen {
			e.setCachedFiring(key, openID)
		}
	}

	switch {
	case hit && !hasOpen:
		if !e.sustained(rule, serverID, now) {
			return
		}
		a := &store.Alert{
			RuleID: rule.ID, ServerID: serverID,
			Metric: rule.Metric, Op: rule.Op, Threshold: rule.Threshold,
			Value: val, Detail: detailMsg(rule, val, ctx),
			Status: "firing", TriggeredAt: now, UpdatedAt: now,
		}
		if err := e.store.CreateAlert(a); err != nil {
			log.Printf("创建告警失败 (rule=%d server=%d): %v", rule.ID, serverID, err)
			return
		}
		e.setCachedFiring(key, a.ID)
		e.notify(AlertEvent{Action: "triggered", Alert: *a})
	case hit && hasOpen:
		// 已在告警中：按 touchInterval 节流刷新观测值（写放大抑制）
		if !e.touchDue(key, now) {
			return
		}
		n, err := e.store.TouchAlert(openID, val, now)
		if err != nil {
			log.Printf("刷新告警失败 (alert=%d): %v", openID, err)
			return
		}
		if n == 0 {
			// 行已不在（保留期清理/外部删除）：缓存过期，回源重查
			e.dropCachedFiring(key)
		}
	case !hit && hasOpen:
		// 恢复是低频事件，这里才取全行供通知用
		open, err := e.store.AlertByID(openID)
		if err != nil || open == nil {
			log.Printf("查询告警全行失败 (alert=%d): %v", openID, err)
			e.dropCachedFiring(key)
			if err := e.store.ResolveAlert(openID, now); err != nil {
				log.Printf("恢复告警失败 (alert=%d): %v", openID, err)
			}
			return
		}
		if err := e.store.ResolveAlert(openID, now); err != nil {
			log.Printf("恢复告警失败 (alert=%d): %v", open.ID, err)
			return
		}
		e.dropCachedFiring(key)
		open.Status = "resolved"
		open.ResolvedAt = &now
		e.notify(AlertEvent{Action: "resolved", Alert: *open})
	}
}

// cachedFiring 查 firing 缓存（无命中时由调用方回源并 setCachedFiring）。
func (e *Engine) cachedFiring(key firingKey) (id int64, has bool) {
	e.firingMu.Lock()
	id, has = e.firing[key]
	e.firingMu.Unlock()
	return id, has
}

// setCachedFiring 记录/覆盖 firing 告警 ID。
func (e *Engine) setCachedFiring(key firingKey, id int64) {
	e.firingMu.Lock()
	e.firing[key] = id
	e.firingMu.Unlock()
}

// dropCachedFiring 解除告警/缓存失效时清除记录。
func (e *Engine) dropCachedFiring(key firingKey) {
	e.firingMu.Lock()
	delete(e.firing, key)
	delete(e.touchAt, key)
	e.firingMu.Unlock()
}

// touchDue 判定本报告是否到达刷新间隔（到达则记账并返回 true）。
func (e *Engine) touchDue(key firingKey, now int64) bool {
	e.firingMu.Lock()
	defer e.firingMu.Unlock()
	if now-e.touchAt[key] < touchInterval {
		return false
	}
	e.touchAt[key] = now
	return true
}

// sustained 判断时间窗口内样本是否持续超限：
//  1. 窗口已覆盖足够时长（最早样本距今 ≥ duration - 25% 容忍，
//     否则刚超标 20s 也会因"窗口内 100% 超限"提前触发 5 分钟规则）；
//  2. ≥80% 样本超限且至少 2 个样本（容忍偶尔丢点）。
//
// duration=0 立即成立。
func (e *Engine) sustained(rule *store.Rule, serverID int64, now int64) bool {
	if rule.DurationSec <= 0 {
		return true
	}
	rows, err := e.store.WindowRows(serverID, now-rule.DurationSec, now, rule.Metric == "net_rx" || rule.Metric == "net_tx")
	if err != nil {
		log.Printf("查询持续窗口失败 (rule=%d server=%d): %v", rule.ID, serverID, err)
		return false
	}
	if len(rows) < 2 {
		return false
	}
	// 窗口覆盖检查：样本尚未铺满 duration（去掉容忍），说明持续时长还不够
	tolerance := rule.DurationSec / 4
	if rows[0].TS > now-rule.DurationSec+tolerance {
		return false
	}
	// port 规则：窗口行不再携带 listens 历史（仅存当前值），
	// 用当前集合评估——集合变化不频繁，与逐刻历史语义等价
	var curListens []proto.ListenPort
	if rule.Metric == "port" {
		if l, err := e.store.CurrentListens(serverID); err == nil {
			curListens = l
		}
	}
	n, m := 0, 0
	for i := range rows {
		rep := rows[i].Report()
		if rule.Metric == "port" {
			rep.Listens = curListens
		}
		if v, _, ok := extract(rule, &rep); ok {
			n++
			if compare(rule.Op, v, rule.Threshold) {
				m++
			}
		}
	}
	return m >= 2 && m*10 >= n*8
}

func (e *Engine) notify(ev AlertEvent) {
	if e.merger != nil {
		e.merger.add(ev)
		return
	}
	for _, n := range e.notifiers {
		n.Notify(ev)
	}
}

// EnableMerge 开启通知聚合：此后触发/恢复事件先进聚合器，由 RunMerge
// 驱动按窗口合并外发（多台设备同时告警合成一条，防推送刷屏）。
func (e *Engine) EnableMerge() {
	e.merger = newMerger(e.store, e.notifiers)
}

// RunMerge 阻塞运行聚合 flush 循环（未 EnableMerge 时直接返回），
// 与 RunOfflineCheck 同款由调用方拉起 goroutine。
func (e *Engine) RunMerge(ctx context.Context) {
	if e.merger != nil {
		e.merger.run(ctx)
	}
}

func compare(op string, v, threshold float64) bool {
	if op == "<" {
		return v < threshold
	}
	return v > threshold
}

// extract 从上报中取规则关注的值。多实例指标（磁盘/网卡）在 param 为空时
// 取最大值（"某块盘满了"语义），并返回触发实例（mount/iface）作为上下文。
// port 规则取监听匹配数（0/1）："< 1" = 未监听告警，"> 0" = 出现监听告警；
// 旧版 agent 上报无 listens 字段（nil）时跳过评估，不误报。
func extract(rule *store.Rule, r *proto.Report) (val float64, ctx string, ok bool) {
	switch rule.Metric {
	case "cpu":
		return r.CPU, "", true
	case "mem":
		return r.MemPercent, "", true
	case "swap":
		return r.SwapPercent, "", true
	case "load1":
		return r.Load1, "", true
	case "load5":
		return r.Load5, "", true
	case "load15":
		return r.Load15, "", true
	case "tcp_est":
		return float64(r.TCPEst), "", true
	case "port":
		if r.Listens == nil {
			return 0, "", false
		}
		protoName, port := ParsePortParam(rule.Param)
		if port == 0 {
			return 0, "", false
		}
		n := 0
		for _, l := range r.Listens {
			if l.Port == port && (protoName == "" || l.Proto == protoName) {
				n++
			}
		}
		return float64(n), rule.Param, true
	case "disk":
		for _, d := range r.Disks {
			if rule.Param != "" && d.Mount != rule.Param {
				continue
			}
			if !ok || d.Percent > val {
				val, ctx, ok = d.Percent, d.Mount, true
			}
		}
		return val, ctx, ok
	case "net_rx", "net_tx":
		for _, n := range r.Nets {
			if rule.Param != "" && n.Iface != rule.Param {
				continue
			}
			v := n.RxBps
			if rule.Metric == "net_tx" {
				v = n.TxBps
			}
			if !ok || v > val {
				val, ctx, ok = v, n.Iface, true
			}
		}
		return val, ctx, ok
	}
	return 0, "", false
}

// ParsePortParam 解析端口规则参数："tcp/443"、"udp/53"，纯数字默认 tcp。
// 无效返回 port=0（调用方按不可评估处理）。
func ParsePortParam(s string) (protoName string, port uint16) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		protoName = strings.ToLower(strings.TrimSpace(s[:i]))
		s = strings.TrimSpace(s[i+1:])
		if protoName != "tcp" && protoName != "udp" {
			return "", 0
		}
	} else {
		protoName = "tcp"
	}
	p, err := strconv.ParseUint(s, 10, 32)
	if err != nil || p == 0 || p > 65535 {
		return "", 0
	}
	return protoName, uint16(p)
}

// detailMsg 生成人读告警消息，如 "磁盘 /data 使用率 92.1% > 90%，持续 ≥ 5 分钟"。
func detailMsg(rule *store.Rule, val float64, ctx string) string {
	labels := map[string]string{
		"cpu": "CPU 使用率", "mem": "内存使用率", "swap": "Swap 使用率",
		"load1": "1 分钟负载", "load5": "5 分钟负载", "load15": "15 分钟负载",
		"tcp_est": "TCP 连接数", "disk": "磁盘", "net_rx": "网卡下行速率", "net_tx": "网卡上行速率",
		"port": "端口",
	}
	if rule.Metric == "port" {
		state := "未监听"
		if val > 0 {
			state = "正在监听"
		}
		dur := ""
		if rule.DurationSec > 0 {
			dur = fmt.Sprintf("，持续 ≥ %s", time.Duration(rule.DurationSec)*time.Second)
		}
		return fmt.Sprintf("端口 %s %s%s", rule.Param, state, dur)
	}
	label := labels[rule.Metric]
	if label == "" {
		label = rule.Metric
	}
	if ctx != "" {
		label += " " + ctx
	}
	unit := ""
	switch rule.Metric {
	case "cpu", "mem", "swap", "disk":
		unit = "%"
	case "net_rx", "net_tx":
		unit = " B/s"
	}
	dur := ""
	if rule.DurationSec > 0 {
		dur = fmt.Sprintf("，持续 ≥ %s", time.Duration(rule.DurationSec)*time.Second)
	}
	valStr := fmt.Sprintf("%.1f", val)
	if rule.Metric == "tcp_est" {
		valStr = fmt.Sprintf("%.0f", val)
	}
	return fmt.Sprintf("%s %s%s %s %v%s%s", label, valStr, unit, rule.Op, rule.Threshold, unit, dur)
}
