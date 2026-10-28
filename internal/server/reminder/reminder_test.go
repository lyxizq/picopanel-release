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
	p, err := s.ListMessages("reminder", false, 1, 50)
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
		got, err := s.GetReminder(r.ID)
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
