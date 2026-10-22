package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestEmbyHistory 播放记录的开始落行/进行中态/结束回填/分页筛选/选项清单（528/530）。
func TestEmbyHistory(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "embyhist.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	now := time.Now().Unix()

	// 三行（两用户两类型）+ 别的集成一行
	id1, err := s.InsertEmbyStart(EmbyEvent{
		IntegrationID: 3, User: "alice", Device: "Emby Web · Chrome", ItemID: "100",
		Title: "第三集", Sub: "好剧", Type: "Episode", Season: 1, Episode: 3,
		StartedAt: now - 1800, RuntimeTicks: 3000000000,
	})
	if err != nil {
		t.Fatalf("insert start: %v", err)
	}
	id2, err := s.InsertEmbyStart(EmbyEvent{
		IntegrationID: 3, User: "bob", Device: "Android TV", ItemID: "200",
		Title: "电影X", Type: "Movie", StartedAt: now - 60, RuntimeTicks: 7200000000,
	})
	if err != nil {
		t.Fatalf("insert start 2: %v", err)
	}
	if _, err := s.InsertEmbyStart(EmbyEvent{
		IntegrationID: 3, User: "alice", Title: "老片", Type: "Movie", StartedAt: now - 86400, RuntimeTicks: 1,
	}); err != nil {
		t.Fatalf("insert start 3: %v", err)
	}
	if _, err := s.InsertEmbyStart(EmbyEvent{
		IntegrationID: 4, User: "carol", Title: "别家的", StartedAt: now, RuntimeTicks: 1,
	}); err != nil {
		t.Fatalf("insert start 4: %v", err)
	}
	if err := s.FinishEmbyPlay(id1, now, 1500000000); err != nil {
		t.Fatalf("finish: %v", err)
	}

	// 全量：total=3、新→旧
	evs, total, err := s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 3, Limit: 100})
	if err != nil || total != 3 || len(evs) != 3 {
		t.Fatalf("应 3 条 total=3: %+v total=%d err=%v", evs, total, err)
	}
	if evs[0].ID != id2 || evs[1].ID != id1 || evs[2].Title != "老片" {
		t.Fatalf("应新→旧: %+v", evs)
	}
	// 季集号（70）随清单透传（电影行两列为 0）
	if evs[1].Season != 1 || evs[1].Episode != 3 || evs[0].Season != 0 || evs[0].Episode != 0 {
		t.Fatalf("季集号透传不对: %+v", evs)
	}

	// 分页：limit=2 第一页、offset=2 第二页各 1 条
	if evs, total, _ = s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 3, Limit: 2}); total != 3 || len(evs) != 2 {
		t.Fatalf("limit=2 应首页 2 条: %d/%d", len(evs), total)
	}
	if evs, _, _ = s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 3, Limit: 2, Offset: 2}); len(evs) != 1 || evs[0].Title != "老片" {
		t.Fatalf("offset=2 应剩 1 条老片: %+v", evs)
	}

	// 筛选：user + type 与计数口径一致
	if evs, total, _ = s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 3, User: "alice"}); total != 2 || len(evs) != 2 {
		t.Fatalf("alice 应 2 条: %d/%d", len(evs), total)
	}
	if evs, total, _ = s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 3, Type: "Movie"}); total != 2 || evs[0].ID != id2 {
		t.Fatalf("Movie 应 2 条且最新在前: %d %+v", total, evs)
	}
	if evs, total, _ = s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 3, User: "alice", Type: "Episode"}); total != 1 || evs[0].ID != id1 {
		t.Fatalf("alice+Episode 应 1 条: %d %+v", total, evs)
	}

	// 日期范围（3）：From/To 按 started_at 左闭右开过滤
	if evs, total, _ = s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 3, From: now - 3600, To: now + 1}); total != 2 ||
		len(evs) != 2 || evs[0].ID != id2 || evs[1].ID != id1 {
		t.Fatalf("近 1 小时应 2 条新→旧: %d/%d %+v", len(evs), total, evs)
	}
	if _, total, _ = s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 3, To: now - 3600}); total != 1 {
		t.Fatalf("只 To 应剩老片 1 条: %d", total)
	}
	if _, total, _ = s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 3, From: now - 60, To: now - 30}); total != 1 {
		t.Fatalf("窗口夹出中间一条应 1 条: %d", total)
	}

	// 选项清单（筛选下拉数据源）
	users, types, err := s.EmbyFilterOptions(3)
	if err != nil || len(users) != 2 || users[0] != "alice" || users[1] != "bob" || len(types) != 2 || types[0] != "Episode" || types[1] != "Movie" {
		t.Fatalf("选项清单不对: users=%v types=%v err=%v", users, types, err)
	}
	if users, _, _ := s.EmbyFilterOptions(4); len(users) != 1 || users[0] != "carol" {
		t.Fatalf("集成 4 选项不对: %v", users)
	}
}

// TestEmbyStats 统计聚合（530）：按用户次数+时长排行、按类型次数、按日期切日；
// 进行中只计次数不计时长；since 过滤。
func TestEmbyStats(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "embystats.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// 两天各固定本地正午（date(...,'localtime') 按本地时区切日，正午避开日界）
	d1 := time.Date(2026, 10, 10, 12, 0, 0, 0, time.Local).Unix()
	d2 := time.Date(2026, 10, 11, 12, 0, 0, 0, time.Local).Unix()
	seed := []EmbyEvent{
		// day1：alice 电影 1000 秒、bob 剧集 200 秒
		{IntegrationID: 8, User: "alice", Type: "Movie", StartedAt: d1, EndedAt: d1 + 1000},
		{IntegrationID: 8, User: "bob", Type: "Episode", StartedAt: d1 + 2000, EndedAt: d1 + 2200},
		// day2：alice 剧集进行中（ended_at=0，只计次数）
		{IntegrationID: 8, User: "alice", Type: "Episode", StartedAt: d2},
	}
	for _, e := range seed {
		if _, err := s.InsertEmbyStart(e); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if e.EndedAt > 0 {
			if err := s.FinishEmbyPlay(mustID(t, s, e), e.EndedAt, 0); err != nil {
				t.Fatalf("finish: %v", err)
			}
		}
	}

	users, types, days, err := s.EmbyStats(8, 0)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	// 用户：alice 2 次 1000 秒居首（进行中不计时长），bob 1 次 200 秒
	if len(users) != 2 || users[0].User != "alice" || users[0].Plays != 2 || users[0].Seconds != 1000 ||
		users[1].User != "bob" || users[1].Seconds != 200 {
		t.Fatalf("按用户聚合不对: %+v", users)
	}
	// 类型：Episode 2、Movie 1（次数降序）
	if len(types) != 2 || types[0].Type != "Episode" || types[0].Plays != 2 || types[1].Type != "Movie" {
		t.Fatalf("按类型聚合不对: %+v", types)
	}
	// 日期：两天升序，day2 只计次数时长 0
	if len(days) != 2 || days[0].Plays != 2 || days[0].Seconds != 1200 ||
		days[1].Plays != 1 || days[1].Seconds != 0 {
		t.Fatalf("按日期聚合不对: %+v", days)
	}
	if days[0].Date != "2026-10-10" || days[1].Date != "2026-10-11" {
		t.Fatalf("日期切日不对: %+v", days)
	}

	// since=day2：只剩 day2 的 alice 一条
	users, _, days, err = s.EmbyStats(8, d2)
	if err != nil {
		t.Fatalf("stats since: %v", err)
	}
	if len(users) != 1 || users[0].User != "alice" || len(days) != 1 || days[0].Date != "2026-10-11" {
		t.Fatalf("since 过滤不对: %+v %+v", users, days)
	}
}

// TestEmbyHistoryMerge 清单 2 小时归并（546）：同一用户同一 item 相邻间隔 ≤2h
// 的多段播放链式并成一条（开始取最早、结束/进度/设备取最近），跨用户不并、
// 空 item 不并、超窗不并；total/翻页/筛选按合并后计；统计仍按原始行。
func TestEmbyHistoryMerge(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "embymerge.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	now := time.Now().Unix()
	ins := func(e EmbyEvent) int64 {
		id, err := s.InsertEmbyStart(e)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		return id
	}
	// alice「长片」500 三段链式：15m/30m 间隔，末段进行中 → 1 条
	seg1 := ins(EmbyEvent{IntegrationID: 5, User: "alice", Device: "客厅电视", ItemID: "500",
		Title: "长片", Type: "Movie", StartedAt: now - 9000, RuntimeTicks: 72000000000})
	s.FinishEmbyPlay(seg1, now-8100, 36000000000)
	seg2 := ins(EmbyEvent{IntegrationID: 5, User: "alice", Device: "客厅电视", ItemID: "500",
		Title: "长片", Type: "Movie", StartedAt: now - 7200, PosTicks: 36000000000, RuntimeTicks: 72000000000})
	s.FinishEmbyPlay(seg2, now-3600, 40000000000)
	seg3 := ins(EmbyEvent{IntegrationID: 5, User: "alice", Device: "平板", ItemID: "500",
		Title: "长片", Type: "Movie", StartedAt: now - 1800, PosTicks: 50000000000, RuntimeTicks: 72000000000})
	// bob 同一部片紧挨 alice 段前 500s → 跨用户不并
	ins(EmbyEvent{IntegrationID: 5, User: "bob", Device: "手机", ItemID: "500",
		Title: "长片", Type: "Movie", StartedAt: now - 9500, RuntimeTicks: 72000000000})
	// alice「旧片」501 两段间隔 11200s > 2h → 不并
	oldA := ins(EmbyEvent{IntegrationID: 5, User: "alice", Device: "客厅电视", ItemID: "501",
		Title: "旧片", Type: "Movie", StartedAt: now - 40000, RuntimeTicks: 50000000000})
	s.FinishEmbyPlay(oldA, now-39600, 1000000000)
	oldB := ins(EmbyEvent{IntegrationID: 5, User: "alice", Device: "客厅电视", ItemID: "501",
		Title: "旧片", Type: "Movie", StartedAt: now - 28400, RuntimeTicks: 50000000000})
	s.FinishEmbyPlay(oldB, now-27800, 2000000000)
	// alice 空 item 两行紧挨 → 不并（item_id 空不参与归并）
	ins(EmbyEvent{IntegrationID: 5, User: "alice", Title: "老片", Type: "Episode", StartedAt: now - 1000})
	ins(EmbyEvent{IntegrationID: 5, User: "alice", Title: "老片", Type: "Episode", StartedAt: now - 800})

	// 全量 6 组：合并行=seg3 字段+seg1 开始时间
	evs, total, err := s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 5, Limit: 100})
	if err != nil || total != 6 || len(evs) != 6 {
		t.Fatalf("应 6 组 total=6: %d/%d err=%v", len(evs), total, err)
	}
	m := evs[2]
	if m.ID != seg3 || m.StartedAt != now-9000 || m.EndedAt != 0 ||
		m.PosTicks != 50000000000 || m.RuntimeTicks != 72000000000 ||
		m.Device != "平板" || m.User != "alice" || m.Title != "长片" {
		t.Fatalf("合并行应取 seg3 最近字段+最早开始: %+v", m)
	}
	if evs[3].User != "bob" || evs[4].Title != "旧片" || evs[4].StartedAt != now-28400 || evs[5].StartedAt != now-40000 {
		t.Fatalf("bob 不并/旧片超窗两段应保持: %+v", evs[3:])
	}

	// 筛选与翻页按合并后口径
	if _, total, _ := s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 5, User: "bob"}); total != 1 {
		t.Fatalf("bob 应 1 组: %d", total)
	}
	if evs, total, _ := s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 5, Type: "Movie"}); total != 4 || len(evs) != 4 {
		t.Fatalf("Movie 应 4 组（长片合并+ bob +旧片两段）: %d/%d", len(evs), total)
	}
	if evs, total, _ := s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 5, Limit: 2, Offset: 4}); total != 6 || len(evs) != 2 ||
		evs[0].Title != "旧片" || evs[1].StartedAt != now-40000 {
		t.Fatalf("翻页按合并后计数: %d/%d %+v", len(evs), total, evs)
	}

	// 日期边界会打断归并链（3）：From=now-8000 切掉 seg1（now-9000），seg2+seg3
	// 间隔 1800s 仍并 1 组（开始取界内最早 now-7200），界外 bob/旧片不出现；
	// 新→旧排序两条空 item 老片在前、长片合并组第三
	if evs, total, _ := s.EmbyHistory(EmbyHistoryQuery{IntegrationID: 5, From: now - 8000, Limit: 100}); total != 3 || len(evs) != 3 ||
		evs[0].Title != "老片" || evs[1].Title != "老片" ||
		evs[2].ID != seg3 || evs[2].StartedAt != now-7200 || evs[2].EndedAt != 0 {
		t.Fatalf("From 切界应 3 组且 seg2+3 并 1: %d/%d %+v", len(evs), total, evs)
	}

	// 统计报表不受归并影响：alice 7 次原始行 5500 秒（900+3600+400+600，进行中/未回填计 0），bob 1 次 0 秒
	users, _, _, err := s.EmbyStats(5, 0)
	if err != nil || len(users) != 2 ||
		users[0].User != "alice" || users[0].Plays != 7 || users[0].Seconds != 5500 ||
		users[1].User != "bob" || users[1].Plays != 1 || users[1].Seconds != 0 {
		t.Fatalf("统计应按原始行聚合: %+v err=%v", users, err)
	}
}

// TestEmbyUserItems 每用户看过的剧影清单（1）：剧集按剧名归并（同剧多集合并一条、
// 不列集数明细）、电影按片名，多次播放合并且 LastAt=最后播放；其他类型/空 user
// 不计，since 过滤，sub 空的剧集回落片名。
func TestEmbyUserItems(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "embyitems.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	now := time.Now().Unix()
	seed := []EmbyEvent{
		// alice 同剧两集（不同 item/title）→ 合并 1 条 plays=2，last_at=较晚的结束
		{IntegrationID: 6, User: "alice", Type: "Episode", Title: "第一集", Sub: "好剧", ItemID: "1", StartedAt: now - 7200, EndedAt: now - 7000},
		{IntegrationID: 6, User: "alice", Type: "Episode", Title: "第二集", Sub: "好剧", ItemID: "2", StartedAt: now - 3600, EndedAt: now - 3500},
		// alice 一部电影（进行中，ended_at=0 → last_at=started_at）
		{IntegrationID: 6, User: "alice", Type: "Movie", Title: "电影X", ItemID: "3", StartedAt: now - 60},
		// bob 同名不同用户 → 各自一组；再一集同名电影（剧影同名各一条）
		{IntegrationID: 6, User: "bob", Type: "Episode", Title: "第三集", Sub: "好剧", ItemID: "4", StartedAt: now - 100, EndedAt: now - 90},
		{IntegrationID: 6, User: "bob", Type: "Movie", Title: "好剧", ItemID: "5", StartedAt: now - 50, EndedAt: now - 40},
		// 歌曲与其他类型、空 user 均不计；sub 空剧集回落片名
		{IntegrationID: 6, User: "alice", Type: "Song", Title: "歌", ItemID: "6", StartedAt: now - 30},
		{IntegrationID: 6, User: "", Type: "Movie", Title: "无名者看的", ItemID: "7", StartedAt: now - 20},
		{IntegrationID: 6, User: "alice", Type: "Episode", Title: "裸剧名", Sub: "", ItemID: "8", StartedAt: now - 5000, EndedAt: now - 4900},
	}
	for _, e := range seed {
		if _, err := s.InsertEmbyStart(e); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if e.EndedAt > 0 {
			if err := s.FinishEmbyPlay(mustID(t, s, e), e.EndedAt, 0); err != nil {
				t.Fatalf("finish: %v", err)
			}
		}
	}

	items, err := s.EmbyUserItems(6, 0)
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	// alice 3 条（好剧合并/电影X/裸剧名）+ bob 2 条（好剧剧、好剧电影）
	if len(items) != 5 {
		t.Fatalf("应 5 条: %+v", items)
	}
	// 全量按 last_at 新→旧：电影X(进行中 now-60) 在 alice 组内最新
	byName := map[string]EmbyUserItemStat{}
	for _, it := range items {
		byName[it.User+"/"+it.Name+"/"+it.Type] = it
	}
	good := byName["alice/好剧/Episode"]
	if good.Plays != 2 || good.LastAt != now-3500 {
		t.Fatalf("同剧两集应合并且 last_at=最晚结束: %+v", good)
	}
	mov := byName["alice/电影X/Movie"]
	if mov.Plays != 1 || mov.LastAt != now-60 {
		t.Fatalf("进行中电影 last_at 应=started_at: %+v", mov)
	}
	if byName["bob/好剧/Episode"].Plays != 1 || byName["bob/好剧/Movie"].Plays != 1 {
		t.Fatalf("同名剧影应各一条、跨用户不并: %+v", items)
	}
	bare := byName["alice/裸剧名/Episode"]
	if bare.Name != "裸剧名" {
		t.Fatalf("sub 空应回落片名: %+v", bare)
	}

	// since=now-4000：裸剧名(now-5000 起)被滤掉，剩 4 条
	if items, err = s.EmbyUserItems(6, now-4000); err != nil || len(items) != 4 {
		t.Fatalf("since 过滤应剩 4 条: %d %+v err=%v", len(items), items, err)
	}
}

// mustID 取刚插入行（seed 里拿 LastInsertId 麻烦，按 started_at 定位）。
func mustID(t *testing.T, s *Store, e EmbyEvent) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM emby_events WHERE started_at=? LIMIT 1`, e.StartedAt).Scan(&id); err != nil {
		t.Fatalf("mustID: %v", err)
	}
	return id
}
