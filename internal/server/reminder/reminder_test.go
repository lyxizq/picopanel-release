package reminder

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// 567 合并口径：同窗扫描中「同一天同一时间」到期的多条事项合并成一条消息
// （标题计数控 + 首行汇总 + 逐行「· 标题」，不带单条 ref 关联，三渠道一次，
// 事项逐条打标）；单条保持原形态（ref_id 关联，事项完成后消息中心红字复原）；
// 不同时刻到期的不合并，各推各的。

func openTest(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func createReminder(t *testing.T, s *store.Store, title, note, date, hm string) *store.Reminder {
	t.Helper()
	r := &store.Reminder{Notify: true, Title: title, Note: note, DueDate: date, DueTime: hm}
	if err := s.CreateReminder(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func reminderMessages(t *testing.T, s *store.Store) []store.Message {
	t.Helper()
	p, err := s.ListMessages(0, "reminder", false, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	return p.Items
}

func TestSweepMergesSameSlot(t *testing.T) {
	s := openTest(t)
	slot := time.Now().Add(-30 * time.Minute) // 落在到期段窗口 [now-1h, now]
	date, hm := slot.Format("2006-01-02"), slot.Format("15:04")
	r1 := createReminder(t, s, "交房租", "", date, hm)
	r2 := createReminder(t, s, "还书", "图书馆闭馆前", date, hm)

	sweep(s)
	msgs := reminderMessages(t, s)
	if len(msgs) != 1 {
		t.Fatalf("同刻 2 条应合并成 1 条消息，got %d 条", len(msgs))
	}
	m := msgs[0]
	if m.Title != "提醒：2 条事项已到到期时间" {
		t.Fatalf("合并标题不符: %q", m.Title)
	}
	for _, want := range []string{"以下 2 条事项已到到期时间", "· 交房租", "· 还书（备注：图书馆闭馆前）"} {
		if !strings.Contains(m.Content, want) {
			t.Fatalf("合并正文缺 %q:\n%s", want, m.Content)
		}
	}
	if m.RefType != "" || m.RefID != 0 {
		t.Fatalf("合并消息不应带单条 ref 关联: ref_type=%q ref_id=%d", m.RefType, m.RefID)
	}

	// 逐条打标：再扫一轮不重复发
	sweep(s)
	if msgs = reminderMessages(t, s); len(msgs) != 1 {
		t.Fatalf("复扫不应产生新消息，got %d 条", len(msgs)-1)
	}
	for _, r := range []*store.Reminder{r1, r2} {
		got, err := s.GetReminder(r.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got.NotifyDueAt == nil {
			t.Fatalf("reminder=%d 到期段未打标", r.ID)
		}
	}
}

func TestSweepOneHourSegmentMergesSameSlot(t *testing.T) {
	s := openTest(t)
	slot := time.Now().Add(20 * time.Minute) // 落在前 1 小时段窗口 [now, now+1h]
	date, hm := slot.Format("2006-01-02"), slot.Format("15:04")
	createReminder(t, s, "取快递", "", date, hm)
	createReminder(t, s, "浇花", "", date, hm)

	sweep(s)
	msgs := reminderMessages(t, s)
	if len(msgs) != 1 {
		t.Fatalf("1h 段同刻 2 条应合并成 1 条消息，got %d 条", len(msgs))
	}
	m := msgs[0]
	if m.Title != "提醒：2 条事项即将到期" {
		t.Fatalf("1h 段合并标题不符: %q", m.Title)
	}
	if !strings.Contains(m.Content, "以下 2 条事项将于") || !strings.Contains(m.Content, "· 取快递") || !strings.Contains(m.Content, "· 浇花") {
		t.Fatalf("1h 段合并正文不符:\n%s", m.Content)
	}
}

func TestSweepDifferentSlotsStaySeparate(t *testing.T) {
	s := openTest(t)
	a := time.Now().Add(-50 * time.Minute)
	b := time.Now().Add(-10 * time.Minute)
	r1 := createReminder(t, s, "交房租", "", a.Format("2006-01-02"), a.Format("15:04"))
	r2 := createReminder(t, s, "还书", "图书馆闭馆前", b.Format("2006-01-02"), b.Format("15:04"))

	sweep(s)
	msgs := reminderMessages(t, s)
	if len(msgs) != 2 {
		t.Fatalf("不同时刻应各推一条，got %d 条", len(msgs))
	}
	byRef := map[int64]store.Message{}
	for _, m := range msgs {
		byRef[m.RefID] = m
	}
	m1, ok1 := byRef[r1.ID]
	m2, ok2 := byRef[r2.ID]
	if !ok1 || !ok2 {
		t.Fatalf("消息应各带对应事项 ref 关联: %+v", msgs)
	}
	if m1.Title != "提醒：交房租" || !strings.Contains(m1.Content, "已到到期时间") {
		t.Fatalf("单条形态不符: %q %q", m1.Title, m1.Content)
	}
	if m2.Title != "提醒：还书" || !strings.Contains(m2.Content, "备注：图书馆闭馆前") {
		t.Fatalf("单条备注行不符: %q %q", m2.Title, m2.Content)
	}
}

// 家庭成员数据隔离（2026-11-03）：提醒推送写站内消息时按事项归属空间打 UserID
// （admin 数据→0，成员数据→其 uid）；不同成员同刻到期不合并成一条消息（互相
// 不可见），各自推送并各带自己的空间标记。
func TestSweepTagsMessageUserID(t *testing.T) {
	s := openTest(t)
	const famUID = 7
	slot := time.Now().Add(-30 * time.Minute) // 落在到期段窗口 [now-1h, now]
	date, hm := slot.Format("2006-01-02"), slot.Format("15:04")

	// admin(0) 与成员各一条同刻到期事项：同刻但不同空间，不得合并
	ra := createReminder(t, s, "管理员事项", "", date, hm)
	ru := &store.Reminder{UserID: famUID, Notify: true, Title: "成员事项", DueDate: date, DueTime: hm}
	if err := s.CreateReminder(ru); err != nil {
		t.Fatal(err)
	}

	sweep(s)
	// 消息中心按空间过滤（messages 模块同批隔离）：两空间各查各的，
	// 同刻跨空间不合并（各得一条、互不可见）
	adminMsgs, err := s.ListMessages(0, "reminder", false, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	famMsgs, err := s.ListMessages(famUID, "reminder", false, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminMsgs.Items) != 1 || len(famMsgs.Items) != 1 {
		t.Fatalf("两空间应各得一条消息（同刻跨空间不合并）: admin=%d member=%d",
			len(adminMsgs.Items), len(famMsgs.Items))
	}
	if adminMsgs.Items[0].RefID != ra.ID || famMsgs.Items[0].RefID != ru.ID {
		t.Fatalf("消息应各带本空间事项 ref: admin=%d member=%d",
			adminMsgs.Items[0].RefID, famMsgs.Items[0].RefID)
	}
	// 落库行 UserID 打标（ListMessages 不回传该列，直接查库断言）
	var adminUID, famMUID int64
	if err := s.DB().QueryRow(`SELECT user_id FROM messages WHERE ref_id = ? AND type = 'reminder'`, ra.ID).Scan(&adminUID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT user_id FROM messages WHERE ref_id = ? AND type = 'reminder'`, ru.ID).Scan(&famMUID); err != nil {
		t.Fatal(err)
	}
	if adminUID != 0 || famMUID != famUID {
		t.Fatalf("消息应按事项归属空间打 UserID: admin=%d member=%d", adminUID, famMUID)
	}
}

// ---- 收租提醒（房屋管理 2026-11-04）：提醒逻辑同事项提醒三段制 ----

func createRentHouse(t *testing.T, s *store.Store, name, rent string, day int, hm string) int64 {
	t.Helper()
	h := &store.House{Name: name, RentDueDay: day, RentDueTime: hm}
	if err := s.CreateHouse(h); err != nil {
		t.Fatal(err)
	}
	l := &store.Lease{HouseID: h.ID, TenantName: "租客" + name, StartDate: "2026-01-01", EndDate: "2030-12-31"}
	l.MonthlyRent = 3500
	if rent == "2800" {
		l.MonthlyRent = 2800
	}
	if err := s.CreateLease(l); err != nil {
		t.Fatal(err)
	}
	return h.ID
}

func houseMessages(t *testing.T, s *store.Store) []store.Message {
	t.Helper()
	p, err := s.ListMessages(0, "house", false, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	return p.Items
}

// TestSweepHouseRents 收租三段推送：同刻多房合并成一条（567 口径）、逐段
// 打标只发一次、改收租安排/换段各自触发。真实时钟推到期时刻，跨月边界整段跳过。
func TestSweepHouseRents(t *testing.T) {
	s := openTest(t)

	// ① 1h 段：当日 +30min，两房同刻 → 合并一条；重扫不重发
	pre := time.Now().Add(30 * time.Minute)
	if pre.Month() != time.Now().Month() {
		t.Skip("跨月边界，跳过 1h 段用例")
	}
	day, hm := pre.Day(), pre.Format("15:04")
	createRentHouse(t, s, "甲屋", "3500", day, hm)
	createRentHouse(t, s, "乙屋", "2800", day, hm)
	sweepHouseRents(s)
	msgs := houseMessages(t, s)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Title, "2 套房屋") ||
		!strings.Contains(msgs[0].Content, "甲屋") || !strings.Contains(msgs[0].Content, "乙屋") {
		t.Fatalf("同刻两房应合并一条: %+v", msgs)
	}
	sweepHouseRents(s)
	if msgs = houseMessages(t, s); len(msgs) != 1 {
		t.Fatalf("打标后重扫不应重发: %d", len(msgs))
	}

	// ② 到期段：改到当日 -30min（UpdateHouse 改收租安排自动重置打标）
	post := time.Now().Add(-30 * time.Minute)
	if post.Month() != time.Now().Month() {
		t.Skip("跨月边界，跳过到期段用例")
	}
	h1 := &store.House{ID: 1, Name: "甲屋", RentDueDay: post.Day(), RentDueTime: post.Format("15:04")}
	h2 := &store.House{ID: 2, Name: "乙屋", RentDueDay: post.Day(), RentDueTime: post.Format("15:04")}
	if err := s.UpdateHouse(h1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateHouse(h2); err != nil {
		t.Fatal(err)
	}
	sweepHouseRents(s)
	msgs = houseMessages(t, s)
	if len(msgs) != 2 || !strings.Contains(msgs[0].Title, "2 套房屋") || !strings.Contains(msgs[0].Content, "今日") {
		t.Fatalf("到期段应合并推一条: %+v", msgs)
	}

	// ③ 超期段：改到 30h 前（24~48h 窗口内；跨月由期次回退逻辑兜住）
	ago := time.Now().Add(-30 * time.Hour)
	h1.RentDueDay, h1.RentDueTime = ago.Day(), ago.Format("15:04")
	h2.RentDueDay, h2.RentDueTime = ago.Day(), ago.Format("15:04")
	if err := s.UpdateHouse(h1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateHouse(h2); err != nil {
		t.Fatal(err)
	}
	sweepHouseRents(s)
	msgs = houseMessages(t, s)
	if len(msgs) != 3 || !strings.Contains(msgs[0].Content, "尚未收讫") {
		t.Fatalf("超期段应合并推一条: %+v", msgs)
	}
	// 重扫不重发
	sweepHouseRents(s)
	if msgs = houseMessages(t, s); len(msgs) != 3 {
		t.Fatalf("超期段打标后不应重发: %d", len(msgs))
	}
}
