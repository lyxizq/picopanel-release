package integrations

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"lz-monitor/internal/server/store"
)

// TestObserveHAFrame 抓帧观察：首帧立基线不记事件、翻转记事件、门窗传感器
// 同记、电池 % 不采样、离线帧不动基线（511/512）。
func TestObserveHAFrame(t *testing.T) {
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "obs.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	m := New(st)
	in := store.Integration{ID: 0, Type: "homeassistant"}

	frame := func(states map[string]HAState) {
		m.observeHAFrame(in, Payload{OK: true, Data: map[string]any{"states": states}})
	}
	count := func(table string) int {
		var n int
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}

	// 首帧：立基线，采样入表、事件为零
	frame(map[string]HAState{
		"light.a":            {EntityID: "light.a", Domain: "light", State: "on"},
		"sensor.temp":        {EntityID: "sensor.temp", Domain: "sensor", State: "23.5", Unit: "°C"},
		"sensor.batt":        {EntityID: "sensor.batt", Domain: "sensor", State: "88", Unit: "%", Name: "Battery"},
		"binary_sensor.door": {EntityID: "binary_sensor.door", Domain: "binary_sensor", State: "off", Name: "大门"},
		"sensor.note":        {EntityID: "sensor.note", Domain: "sensor", State: "hello"},
	})
	if n := count("ha_events"); n != 0 {
		t.Fatalf("首帧不应记事件: %d", n)
	}
	if n := count("ha_samples"); n != 2 { // 温度 + 电池 %（2026-10-09 起电量含电池）；非数值态不采
		t.Fatalf("采样应为温度+电池 2 条: %d", n)
	}

	// 二帧：门开 → 一条事件；灯保持开；温度变化 → 新采样
	frame(map[string]HAState{
		"light.a":            {EntityID: "light.a", Domain: "light", State: "on"},
		"sensor.temp":        {EntityID: "sensor.temp", Domain: "sensor", State: "24.0", Unit: "°C"},
		"binary_sensor.door": {EntityID: "binary_sensor.door", Domain: "binary_sensor", State: "on", Name: "大门"},
	})
	if n := count("ha_events"); n != 1 {
		t.Fatalf("翻转应记 1 条事件: %d", n)
	}
	if evs, _ := st.HAEntityEvents("binary_sensor.door", 10); len(evs) != 1 || evs[0].From != "off" || evs[0].To != "on" {
		t.Fatalf("门窗事件不对: %+v", evs)
	}

	// 三帧：灯离线 → 不记事件也不动基线
	frame(map[string]HAState{
		"light.a":     {EntityID: "light.a", Domain: "light", State: "unavailable"},
		"sensor.temp": {EntityID: "sensor.temp", Domain: "sensor", State: "unavailable", Unit: "°C"},
	})
	if n := count("ha_events"); n != 1 {
		t.Fatalf("离线帧不应记事件: %d", n)
	}

	// 四帧：灯恢复为 off（离线期间被物理关掉）→ 与离线前比对记 on→off；温度离线不采样
	frame(map[string]HAState{
		"light.a":     {EntityID: "light.a", Domain: "light", State: "off"},
		"sensor.temp": {EntityID: "sensor.temp", Domain: "sensor", State: "unavailable", Unit: "°C"},
	})
	if n := count("ha_events"); n != 2 {
		t.Fatalf("恢复后应与离线前比对记事件: %d", n)
	}
	evs, err := st.HAEntityEvents("light.a", 10)
	if err != nil || len(evs) != 1 || evs[0].From != "on" || evs[0].To != "off" {
		t.Fatalf("灯事件不对: %+v err=%v", evs, err)
	}
	if n := count("ha_samples"); n != 3 { // 二帧温度已采，离线帧不再新增
		t.Fatalf("离线温度不应采样: %d", n)
	}

	// 非 HA 集成/失败帧不进观察
	m.observeHAFrame(store.Integration{ID: 1, Type: "qbittorrent"}, Payload{OK: true, Data: map[string]any{}})
	m.observeHAFrame(in, Payload{OK: false})
	if n := count("ha_samples"); n != 3 {
		t.Fatalf("非 HA/失败帧不应采样: %d", n)
	}
}

// TestObserveHAFrameConcurrent 并发抓帧观察无数据竞争（基线锁修复的回归）：
// Kick 协程/轮询循环/页面补拉可各自独立触发 observe，-race 下必须干净，且
// 并发翻转不产生重复事件（比对与更新同临界区，旧基线不会被读两次）。
func TestObserveHAFrameConcurrent(t *testing.T) {
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "race.db"), filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	m := New(st)
	in := store.Integration{ID: 7, Type: "homeassistant"}
	base := map[string]HAState{}
	for i := 0; i < 60; i++ {
		id := "e" + strconv.Itoa(i)
		if i%2 == 0 {
			base[id] = HAState{EntityID: id, Domain: "light", State: "on"}
		} else {
			base[id] = HAState{EntityID: id, Domain: "sensor", State: "23", Unit: "°C"}
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(flip bool) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				s := map[string]HAState{}
				for k, v := range base {
					if flip && i%3 == 0 && v.Domain == "light" {
						v.State = "off"
					}
					s[k] = v
				}
				m.observeHAFrame(in, Payload{OK: true, Data: map[string]any{"states": s}})
			}
		}(g%2 == 0)
	}
	wg.Wait()
	// 串行不变量：单实体的翻转方向必须严格交替（新→旧）。竞争的病征恰是
	// 并发帧对同一旧基线各记一次同方向事件——出现连续同向即回归。
	evs, err := st.HAEntityEvents("e0", 1000)
	if err != nil {
		t.Fatalf("查事件失败: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("并发翻转应产生事件")
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].To == evs[i-1].To {
			t.Fatalf("连续同向事件（竞争回归）: #%d #%d 均 %s", i-1, i, evs[i].To)
		}
	}
}

// TestDropClearsHABaseline 删除集成时开合基线随缓存槽一起清（防滞留）。
func TestDropClearsHABaseline(t *testing.T) {
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "drop.db"), filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	m := New(st)
	in := store.Integration{ID: 9, Type: "homeassistant"}
	m.observeHAFrame(in, Payload{OK: true, Data: map[string]any{"states": map[string]HAState{
		"light.a": {EntityID: "light.a", Domain: "light", State: "on"},
	}}})
	m.Drop(9)
	m.haMu.Lock()
	_, ok := m.haPrev[9]
	m.haMu.Unlock()
	if ok {
		t.Fatal("Drop 后 HA 基线应被清理")
	}
}
