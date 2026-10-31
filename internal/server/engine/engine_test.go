package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
)

func newTestEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	eng := New(st, Config{OfflineAfter: 2 * time.Minute})
	return eng, st
}

func newRule(metric string, threshold float64, durationSec int64) *store.Rule {
	return &store.Rule{
		Name: "test", Metric: metric, Op: ">", Threshold: threshold,
		DurationSec: durationSec, Enabled: true,
	}
}

// 高 CPU 上报样本
func highCPUReport(cpu float64) *proto.Report {
	return &proto.Report{CPU: cpu, MemPercent: 50, Load1: 1}
}

func insertRows(t *testing.T, st *store.Store, serverID int64, from int64, count int, step int64, cpu float64) {
	t.Helper()
	for i := 0; i < count; i++ {
		ts := from + int64(i)*step
		r := highCPUReport(cpu)
		r.TS = ts
		if err := st.InsertMetric(serverID, ts, r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSustainedDurationNotElapsed(t *testing.T) {
	eng, st := newTestEngine(t)
	srv, _ := st.CreateServer("s1", "127.0.0.1:9100", "", 0)
	now := time.Now().Unix()

	// 持续 60s 的规则：只有最近 10s 内 3 个高 CPU 样本（覆盖率够但时长不够）→ 不触发
	insertRows(t, st, srv.ID, now-10, 3, 5, 95)
	if eng.sustained(newRule("cpu", 80, 60), srv.ID, now) {
		t.Fatal("窗口未覆盖 duration（容忍 15s）不应触发")
	}
}

func TestSustainedDurationElapsed(t *testing.T) {
	eng, st := newTestEngine(t)
	srv, _ := st.CreateServer("s1", "127.0.0.1:9100", "", 0)
	now := time.Now().Unix()

	// 60s 窗口内铺满高 CPU 样本（10s 间隔 ×7 个，最早距今 60s）→ 触发
	insertRows(t, st, srv.ID, now-60, 7, 10, 95)
	if !eng.sustained(newRule("cpu", 80, 60), srv.ID, now) {
		t.Fatal("满窗口高 CPU 应触发")
	}
}

func TestSustainedRatioFail(t *testing.T) {
	eng, st := newTestEngine(t)
	srv, _ := st.CreateServer("s1", "127.0.0.1:9100", "", 0)
	now := time.Now().Unix()

	// 60s 窗口 6 个样本只有 1 个超限（<80%）→ 不触发
	for i, cpu := range []float64{10, 10, 95, 10, 10, 10} {
		r := highCPUReport(cpu)
		ts := now - 60 + int64(i)*10
		r.TS = ts
		if err := st.InsertMetric(srv.ID, ts, r); err != nil {
			t.Fatal(err)
		}
	}
	if eng.sustained(newRule("cpu", 80, 60), srv.ID, now) {
		t.Fatal("超限样本占比 <80% 不应触发")
	}
}

func TestSustainedZeroDuration(t *testing.T) {
	eng, st := newTestEngine(t)
	srv, _ := st.CreateServer("s1", "127.0.0.1:9100", "", 0)
	if !eng.sustained(newRule("cpu", 80, 0), srv.ID, time.Now().Unix()) {
		t.Fatal("duration=0 应立即成立")
	}
}

func TestEvaluateStateMachine(t *testing.T) {
	eng, st := newTestEngine(t)
	srv, _ := st.CreateServer("s1", "127.0.0.1:9100", "", 0)

	rule := newRule("cpu", 80, 0)
	rule.Name = "CPU 过高"
	if err := st.CreateRule(rule); err != nil {
		t.Fatal(err)
	}
	if err := eng.ReloadRules(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	// 超限 → 触发
	eng.Evaluate(srv.ID, highCPUReport(95), now)
	open, err := st.FindOpenAlert(rule.ID, srv.ID)
	if err != nil || open == nil {
		t.Fatalf("应存在 firing 告警: %v %v", open, err)
	}
	firstID := open.ID
	// 持续超限 → 不重复创建，只刷新观测值
	eng.Evaluate(srv.ID, highCPUReport(97), now+10)
	firing, _ := st.FiringCountByServer()
	if firing[srv.ID] != 1 {
		t.Fatalf("不应出现重复 firing，当前 %d", firing[srv.ID])
	}
	open2, _ := st.FindOpenAlert(rule.ID, srv.ID)
	if open2.Value != 97 {
		t.Fatalf("观测值应刷新为 97，当前 %v", open2.Value)
	}
	// 恢复 → resolved
	eng.Evaluate(srv.ID, highCPUReport(50), now+20)
	open3, _ := st.FindOpenAlert(rule.ID, srv.ID)
	if open3 != nil {
		t.Fatal("恢复后不应存在 firing 告警")
	}
	_ = firstID
}

func TestEvaluateGlobalAndSingleScope(t *testing.T) {
	eng, st := newTestEngine(t)
	s1, _ := st.CreateServer("s1", "127.0.0.1:9100", "", 0)
	s2, _ := st.CreateServer("s2", "127.0.0.1:9101", "", 0)

	// 单机规则只作用于 s1
	rule := newRule("cpu", 80, 0)
	rule.ServerID = &s1.ID
	if err := st.CreateRule(rule); err != nil {
		t.Fatal(err)
	}
	_ = eng.ReloadRules()

	now := time.Now().Unix()
	eng.Evaluate(s2.ID, highCPUReport(99), now)
	if n, _ := st.FiringCountByServer(); n[s2.ID] != 0 {
		t.Fatal("单机规则不应作用于其他服务器")
	}
	eng.Evaluate(s1.ID, highCPUReport(99), now)
	if n, _ := st.FiringCountByServer(); n[s1.ID] != 1 {
		t.Fatal("单机规则应作用于目标服务器")
	}
}

func TestEvaluateDiskMaxParam(t *testing.T) {
	eng, st := newTestEngine(t)
	srv, _ := st.CreateServer("s1", "127.0.0.1:9100", "", 0)

	rule := newRule("disk", 90, 0) // param 空 = 任一盘超限
	if err := st.CreateRule(rule); err != nil {
		t.Fatal(err)
	}
	_ = eng.ReloadRules()

	r := &proto.Report{Disks: []proto.DiskInfo{
		{Mount: "/", Percent: 40},
		{Mount: "/data", Percent: 92},
	}}
	eng.Evaluate(srv.ID, r, time.Now().Unix())
	if n, _ := st.FiringCountByServer(); n[srv.ID] != 1 {
		t.Fatal("任一磁盘超限应触发")
	}
	alerts, _, _ := st.ListAlerts("firing", 0, 1, 10)
	if len(alerts) != 1 || !strings.Contains(alerts[0].Detail, "/data") {
		t.Fatalf("告警详情应包含触发磁盘 /data: %+v", alerts)
	}
}

func TestParsePortParam(t *testing.T) {
	if p, port := ParsePortParam("tcp/443"); p != "tcp" || port != 443 {
		t.Fatalf("tcp/443 = %q %d", p, port)
	}
	if p, port := ParsePortParam("udp/53"); p != "udp" || port != 53 {
		t.Fatalf("udp/53 = %q %d", p, port)
	}
	if p, port := ParsePortParam("22"); p != "tcp" || port != 22 {
		t.Fatalf("纯数字应默认 tcp: %q %d", p, port)
	}
	if _, port := ParsePortParam("icmp/1"); port != 0 {
		t.Fatal("非 tcp/udp 协议应无效")
	}
	if _, port := ParsePortParam("99999"); port != 0 {
		t.Fatal("超范围端口应无效")
	}
	if _, port := ParsePortParam(""); port != 0 {
		t.Fatal("空参数应无效")
	}
}

func TestEvaluatePortRule(t *testing.T) {
	eng, st := newTestEngine(t)
	srv, _ := st.CreateServer("s1", "127.0.0.1:9100", "", 0)

	// 未监听告警：监听数 < 1（端口 tcp/443）
	rule := &store.Rule{
		Name: "https", Metric: "port", Op: "<", Threshold: 1,
		Param: "tcp/443", Enabled: true,
	}
	if err := st.CreateRule(rule); err != nil {
		t.Fatal(err)
	}
	_ = eng.ReloadRules()

	now := time.Now().Unix()
	listening := &proto.Report{Listens: []proto.ListenPort{
		{Proto: "tcp", Port: 22, Addrs: []string{"0.0.0.0"}},
	}}
	// 旧版 agent：无 listens 字段（nil）→ 跳过评估，不误报
	eng.Evaluate(srv.ID, &proto.Report{}, now)
	if n, _ := st.FiringCountByServer(); n[srv.ID] != 0 {
		t.Fatal("旧版 agent（listens 为 nil）不应触发端口告警")
	}
	// 新版 agent 且 443 未监听 → 触发，详情为「端口 tcp/443 未监听」
	eng.Evaluate(srv.ID, listening, now)
	if n, _ := st.FiringCountByServer(); n[srv.ID] != 1 {
		t.Fatal("443 未监听应触发告警")
	}
	alerts, _, _ := st.ListAlerts("firing", 0, 1, 10)
	if len(alerts) != 1 || !strings.Contains(alerts[0].Detail, "tcp/443 未监听") {
		t.Fatalf("告警详情错误: %+v", alerts)
	}
	// 443 恢复监听 → 自动恢复
	listening.Listens = append(listening.Listens, proto.ListenPort{Proto: "tcp", Port: 443, Addrs: []string{"0.0.0.0"}})
	eng.Evaluate(srv.ID, listening, now+60)
	if n, _ := st.FiringCountByServer(); n[srv.ID] != 0 {
		t.Fatal("443 恢复监听后告警应恢复")
	}
}
