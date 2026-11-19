package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// captureNotifier 记录收到的事件（测试用）。
type captureNotifier struct {
	events []AlertEvent
}

func (c *captureNotifier) Notify(ev AlertEvent) {
	c.events = append(c.events, ev)
}

func newMergeTestEngine(t *testing.T) (*Engine, *store.Store, *captureNotifier) {
	t.Helper()
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "merge.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cap := &captureNotifier{}
	eng := New(st, Config{}, cap)
	eng.EnableMerge()
	return eng, st, cap
}

func mergeEvent(action string, serverID int64, detail string) AlertEvent {
	return AlertEvent{Action: action, Alert: store.Alert{ServerID: serverID, Detail: detail}}
}

// TestMergeBuffersUntilQuiet 多台设备同时告警/恢复：事件进缓冲不外发，
// 静默后 flush 合成触发/恢复各一条，逐台列出服务器名与详情。
func TestMergeBuffersUntilQuiet(t *testing.T) {
	eng, st, cap := newMergeTestEngine(t)
	s1, _ := st.CreateServer("web-1", "", "", 0)
	s2, _ := st.CreateServer("web-2", "", "", 0)
	s3, _ := st.CreateServer("db-1", "", "", 0)

	eng.notify(mergeEvent("triggered", s1.ID, "CPU 92% > 90%"))
	eng.notify(mergeEvent("triggered", s2.ID, "CPU 95% > 90%"))
	eng.notify(mergeEvent("triggered", s3.ID, "内存 93% > 90%"))
	eng.notify(mergeEvent("resolved", s2.ID, "CPU 恢复"))
	if len(cap.events) != 0 {
		t.Fatalf("窗口内不应外发: %+v", cap.events)
	}
	if eng.merger.shouldFlush(time.Now()) {
		t.Fatal("刚进事件不应 flush")
	}

	// 模拟静默 60s 到期
	eng.merger.mu.Lock()
	eng.merger.lastAt = time.Now().Add(-mergeQuiet - time.Second)
	eng.merger.mu.Unlock()
	if !eng.merger.shouldFlush(time.Now()) {
		t.Fatal("静默超时应 flush")
	}
	eng.merger.flush()

	if len(cap.events) != 2 {
		t.Fatalf("应合成触发/恢复各一条, got %d: %+v", len(cap.events), cap.events)
	}
	trig, res := cap.events[0], cap.events[1]
	if trig.Action != "triggered" || res.Action != "resolved" {
		t.Fatalf("顺序应为触发在前恢复在后: %+v", cap.events)
	}
	if trig.Alert.ServerName != "3 台服务器" {
		t.Fatalf("触发概括名: %q", trig.Alert.ServerName)
	}
	if res.Alert.ServerName != "web-2" {
		t.Fatalf("单台恢复应直用服务器名: %q", res.Alert.ServerName)
	}
	for _, want := range []string{"web-1：CPU 92%", "web-2：CPU 95%", "db-1：内存 93%"} {
		if !strings.Contains(trig.Alert.Detail, want) {
			t.Fatalf("触发详情缺 %q: %q", want, trig.Alert.Detail)
		}
	}

	// flush 后缓冲清空，新事件重新开窗
	eng.notify(mergeEvent("triggered", s1.ID, "again"))
	eng.merger.mu.Lock()
	eng.merger.lastAt = time.Now().Add(-mergeQuiet - time.Second)
	eng.merger.mu.Unlock()
	eng.merger.flush()
	if len(cap.events) != 3 {
		t.Fatalf("第二轮窗口应再发一条: %d", len(cap.events))
	}
}

// TestMergeMaxWindow 持续有新事件（静默永不满足）时，窗口满 10 分钟强制 flush。
func TestMergeMaxWindow(t *testing.T) {
	eng, _, cap := newMergeTestEngine(t)
	eng.notify(mergeEvent("triggered", 1, "a"))
	eng.merger.mu.Lock()
	eng.merger.firstAt = time.Now().Add(-mergeMax - time.Second)
	eng.merger.mu.Unlock()
	if !eng.merger.shouldFlush(time.Now()) {
		t.Fatal("窗口达上限应强制 flush")
	}
	eng.merger.flush()
	if len(cap.events) != 1 || cap.events[0].Action != "triggered" {
		t.Fatalf("应外发一条触发: %+v", cap.events)
	}
}

// TestMergeDetailCap 详情逐台列出封顶 20 行，更多折叠为计数。
func TestMergeDetailCap(t *testing.T) {
	_, st, _ := newMergeTestEngine(t) // 只借用建库；合成逻辑直接测 mergeEvents
	_ = st
	evs := make([]AlertEvent, 25)
	for i := range evs {
		evs[i] = mergeEvent("triggered", int64(i+1), "offline")
	}
	out, ok := mergeEvents(nil, "triggered", evs)
	if !ok {
		t.Fatal("非空应合成")
	}
	if out.Alert.ServerName != "25 台服务器" {
		t.Fatalf("概括名: %q", out.Alert.ServerName)
	}
	if !strings.Contains(out.Alert.Detail, "其余 5 项略") {
		t.Fatalf("超 20 行应折叠: %q", out.Alert.Detail)
	}
	if lines := strings.Count(out.Alert.Detail, "\n· "); lines != 20 {
		t.Fatalf("应只列 20 行, got %d", lines)
	}
}

// TestNotifyWithoutMerge 未 EnableMerge 时事件直发渠道（既有行为不变）。
func TestNotifyWithoutMerge(t *testing.T) {
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "plain.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cap := &captureNotifier{}
	eng := New(st, Config{}, cap)
	eng.notify(mergeEvent("triggered", 7, "x"))
	if len(cap.events) != 1 {
		t.Fatalf("应直发一条: %+v", cap.events)
	}
}

// TestMergeFlushOnShutdown 服务退出时缓冲里的事件也要发出去，不静默丢弃。
func TestMergeFlushOnShutdown(t *testing.T) {
	eng, _, cap := newMergeTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { eng.RunMerge(ctx); close(done) }()
	eng.notify(mergeEvent("triggered", 3, "offline"))
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunMerge 未随 ctx 退出")
	}
	if len(cap.events) != 1 {
		t.Fatalf("退出前应 flush 缓冲事件: %+v", cap.events)
	}
}
