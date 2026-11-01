package integrations

import (
	"path/filepath"
	"sync"
	"testing"

	"lz-monitor/internal/server/store"
)

// embyRawRows 直读原始行（546 起 EmbyHistory 为 2 小时合并显示视图，观察测试
// 要对账的是落库行本身，显示归并有 TestEmbyHistoryMerge 专测）。
func embyRawRows(t *testing.T, st *store.Store, integrationID int64) []store.EmbyEvent {
	t.Helper()
	rows, err := st.DB().Query(`SELECT id, user, device, item_id, title, sub, item_type, season, episode,
		started_at, ended_at, pos_ticks, runtime_ticks FROM emby_events
		WHERE integration_id=? ORDER BY started_at DESC, id DESC`, integrationID)
	if err != nil {
		t.Fatalf("raw rows: %v", err)
	}
	defer rows.Close()
	var out []store.EmbyEvent
	for rows.Next() {
		var e store.EmbyEvent
		if err := rows.Scan(&e.ID, &e.User, &e.Device, &e.ItemID, &e.Title, &e.Sub, &e.Type,
			&e.Season, &e.Episode, &e.StartedAt, &e.EndedAt, &e.PosTicks, &e.RuntimeTicks); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, e)
	}
	return out
}

// TestObserveEmbyFrame 播放差分观察（528）：出现=开始落行（进行中）、持续不重复
// 落、消失=回填结束与最后位置、会话拉取失败不动基线、非 Emby/失败帧不进观察。
func TestObserveEmbyFrame(t *testing.T) {
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "emobs.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	m := New(st)
	in := store.Integration{ID: 5, Type: store.IntegrationEmby}

	frame := func(plays []EmbyPlay, drop bool) {
		data := map[string]any{}
		if !drop {
			data["plays"] = plays
		}
		m.observeEmbyFrame(in, Payload{OK: true, Data: data})
	}
	alice := EmbyPlay{Key: "dev1|100", User: "alice", Device: "Emby Web · Chrome",
		ItemID: "100", Title: "第三集", Sub: "好剧", Type: "Episode", Season: 2, Episode: 7,
		Pos: 3_000_000_000, Runtime: 30_000_000_000}
	bob := EmbyPlay{Key: "dev2|200", User: "bob", Device: "Android TV",
		ItemID: "200", Title: "电影X", Type: "Movie", Pos: 1_200_000_000, Runtime: 72_000_000_000}

	// 首帧：两个会话 → 两行进行中
	frame([]EmbyPlay{alice, bob}, false)
	evs := embyRawRows(t, st, 5)
	if err != nil || len(evs) != 2 {
		t.Fatalf("首帧应 2 行进行中: %+v", evs)
	}
	if evs[0].EndedAt != 0 || evs[1].EndedAt != 0 || evs[0].User != "bob" || evs[1].User != "alice" {
		t.Fatalf("进行中行不对: %+v", evs)
	}
	// 季集号（70）随开始落行入库（电影行两列为 0）
	if evs[1].Season != 2 || evs[1].Episode != 7 || evs[0].Season != 0 || evs[0].Episode != 0 {
		t.Fatalf("季集号落库不对: %+v", evs)
	}

	// 二帧：alice 进度前进 >1s → 不新增行，且进行中行位置实时回写 DB（534）
	alice.Pos = 5_000_000_000
	frame([]EmbyPlay{alice, bob}, false)
	evs = embyRawRows(t, st, 5)
	if len(evs) != 2 {
		t.Fatalf("持续播放不应新增行: %+v", evs)
	}
	if evs[1].PosTicks != 5_000_000_000 || evs[1].EndedAt != 0 {
		t.Fatalf("进行中行应实时回写位置: %+v", evs[1])
	}

	// 三帧：会话拉取失败（plays 未下发）→ 不动基线不误记结束
	frame(nil, true)
	if evs = embyRawRows(t, st, 5); len(evs) != 2 || evs[0].EndedAt != 0 || evs[1].EndedAt != 0 {
		t.Fatalf("拉取失败不应误记结束: %+v", evs)
	}

	// 四帧：alice 消失 → 回填结束与最后观测位置；bob 仍进行中
	frame([]EmbyPlay{bob}, false)
	evs = embyRawRows(t, st, 5)
	if len(evs) != 2 {
		t.Fatalf("结束不新增行: %+v", evs)
	}
	aliceRow, bobRow := evs[1], evs[0]
	if aliceRow.EndedAt == 0 || aliceRow.PosTicks != 5_000_000_000 {
		t.Fatalf("alice 应回填最终观测位置: %+v", aliceRow)
	}
	if bobRow.EndedAt != 0 {
		t.Fatalf("bob 应仍进行中: %+v", bobRow)
	}

	// 五帧：bob 也消失 → 全闭合
	frame(nil, false)
	evs = embyRawRows(t, st, 5)
	if len(evs) != 2 {
		t.Fatalf("行数不应变: %+v", evs)
	}
	for _, e := range evs {
		if e.EndedAt == 0 {
			t.Fatalf("应全部闭合: %+v", e)
		}
	}

	// 六帧：bob 重启会话（同键再现）= 新一行进行中
	frame([]EmbyPlay{bob}, false)
	if evs = embyRawRows(t, st, 5); len(evs) != 3 || evs[0].EndedAt != 0 {
		t.Fatalf("同键再现应新行: %+v", evs)
	}

	// 七帧：全空 → 全闭合
	frame(nil, false)
	evs = embyRawRows(t, st, 5)
	if len(evs) != 3 {
		t.Fatalf("行数不应变: %+v", evs)
	}
	for _, e := range evs {
		if e.EndedAt == 0 {
			t.Fatalf("应全部闭合: %+v", e)
		}
	}

	// 非 Emby 集成/失败帧不进观察
	m.observeEmbyFrame(store.Integration{ID: 6, Type: store.IntegrationHA},
		Payload{OK: true, Data: map[string]any{"plays": []EmbyPlay{alice}}})
	m.observeEmbyFrame(in, Payload{OK: false})
	if evs = embyRawRows(t, st, 5); len(evs) != 3 {
		t.Fatalf("非 Emby/失败帧不应记录: %+v", evs)
	}
}

// TestObserveEmbyFrameConcurrent 并发抓帧观察无数据竞争且同键不产生孤儿行：
// 多 goroutine 交错出现/消失同一批会话，串行不变量=每一行最终都能闭合（竞争
// 病征=并发对同一空基线各插一行、prev 只留一个，另一行永远进行中）。
func TestObserveEmbyFrameConcurrent(t *testing.T) {
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "emrace.db"), filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	m := New(st)
	in := store.Integration{ID: 7, Type: store.IntegrationEmby}
	base := []EmbyPlay{}
	for i := 0; i < 20; i++ {
		base = append(base, EmbyPlay{Key: "d|" + string(rune('A'+i)), User: "u", Title: "t", Pos: 10, Runtime: 100})
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(present bool) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				plays := base
				if !present && i%2 == 0 {
					plays = nil
				}
				data := map[string]any{"plays": plays}
				m.observeEmbyFrame(in, Payload{OK: true, Data: data})
			}
		}(g%2 == 0)
	}
	wg.Wait()
	// 落定帧：全空闭合一切
	m.observeEmbyFrame(in, Payload{OK: true, Data: map[string]any{"plays": []EmbyPlay{}}})
	evs := embyRawRows(t, st, 7)
	if len(evs) == 0 {
		t.Fatal("并发观察应产生记录")
	}
	for _, e := range evs {
		if e.EndedAt == 0 {
			t.Fatalf("落定后仍有未闭合行（竞争回归）: %+v", e)
		}
	}
}

// TestDropClearsEmbyBaseline 删除集成时播放差分基线随缓存槽一起清（防滞留）。
func TestDropClearsEmbyBaseline(t *testing.T) {
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "emdrop.db"), filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	m := New(st)
	in := store.Integration{ID: 9, Type: store.IntegrationEmby}
	m.observeEmbyFrame(in, Payload{OK: true, Data: map[string]any{
		"plays": []EmbyPlay{{Key: "d|1", User: "u", Title: "t"}},
	}})
	m.Drop(9)
	m.embyMu.Lock()
	_, ok := m.embyPrev[9]
	m.embyMu.Unlock()
	if ok {
		t.Fatal("Drop 后 Emby 基线应被清理")
	}
}
