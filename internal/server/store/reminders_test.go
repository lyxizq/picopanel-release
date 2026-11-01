package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNextDueDate(t *testing.T) {
	cases := []struct {
		name string
		due  string
		rule RepeatRule
		want string
	}{
		{"每天", "2026-09-29", RepeatRule{Type: "daily"}, "2026-09-30"},
		{"每周", "2026-09-29", RepeatRule{Type: "weekly"}, "2026-10-06"},
		{"每月", "2026-09-29", RepeatRule{Type: "monthly"}, "2026-10-29"},
		{"每年", "2024-02-29", RepeatRule{Type: "yearly"}, "2025-02-28"}, // 闰日平年收敛
		{"月末收敛", "2026-01-31", RepeatRule{Type: "monthly"}, "2026-02-28"},
		{"月末再到大月回弹", "2026-02-28", RepeatRule{Type: "monthly"}, "2026-03-28"},
		{"自定义每3天", "2026-09-29", RepeatRule{Type: "custom", Every: 3, Unit: "day"}, "2026-10-02"},
		{"自定义每2月收敛", "2026-01-31", RepeatRule{Type: "custom", Every: 2, Unit: "month"}, "2026-03-31"},
		{"自定义每2月收敛到月末", "2026-11-30", RepeatRule{Type: "custom", Every: 3, Unit: "month"}, "2027-02-28"},
		{"自定义每2周", "2026-09-29", RepeatRule{Type: "custom", Every: 2, Unit: "week"}, "2026-10-13"},
		{"自定义每2年", "2026-09-29", RepeatRule{Type: "custom", Every: 2, Unit: "year"}, "2028-09-29"},
		{"结束日期前", "2026-09-29", RepeatRule{Type: "daily", Until: "2026-10-01"}, "2026-09-30"},
		{"结束日期截停", "2026-09-30", RepeatRule{Type: "daily", Until: "2026-09-30"}, ""},
		{"不重复", "2026-09-29", RepeatRule{Type: "none"}, ""},
		{"空规则", "2026-09-29", RepeatRule{}, ""},
		{"坏日期", "not-a-date", RepeatRule{Type: "daily"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nextDueDate(c.due, c.rule); got != c.want {
				t.Fatalf("nextDueDate(%q, %+v) = %q, want %q", c.due, c.rule, got, c.want)
			}
		})
	}
}

func TestRepeatRuleValid(t *testing.T) {
	if !(RepeatRule{Type: "none"}).Valid() {
		t.Fatal("none 应合法")
	}
	if (RepeatRule{Type: "custom", Every: 0, Unit: "day"}).Valid() {
		t.Fatal("custom every=0 应不合法")
	}
	if (RepeatRule{Type: "custom", Every: 2, Unit: "hour"}).Valid() {
		t.Fatal("custom unit=hour 应不合法")
	}
	if !(RepeatRule{Type: "custom", Every: 2, Unit: "week"}).Valid() {
		t.Fatal("custom 每2周 应合法")
	}
	if (RepeatRule{Type: "bogus"}).Valid() {
		t.Fatal("未知类型应不合法")
	}
}

func TestEffectiveDueTime(t *testing.T) {
	r := Reminder{DueDate: "2026-09-29"}
	if r.EffectiveDueTime() != "09:00" {
		t.Fatalf("空时间应回落 09:00，got %q", r.EffectiveDueTime())
	}
	r.DueTime = "14:30"
	if r.EffectiveDueTime() != "14:30" {
		t.Fatalf("应保留填写时间，got %q", r.EffectiveDueTime())
	}
}

// 完成→恢复→再完成：周期任务恢复时撤回自动生成的下一条，再次完成不重复生成。
// 老库升级回归：子任务功能（parent_id/notify 列）之前建的 reminders 表，
// 启动迁移必须补列成功而不是在建索引时炸「no such column: parent_id」
// （2026-09-29 发布后启动失败的根因，索引须在补列之后建）。
func TestRemindersMigrateOldDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "old.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// 还原子任务功能之前的表结构（无 important/my_day/prev_id/deleted_at/
	// parent_id/notify——这六列全是后补；其余列与第八十五/八十六批一致）
	if _, err := db.Exec(`CREATE TABLE reminders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_id INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL, note TEXT NOT NULL DEFAULT '',
		due_date TEXT NOT NULL, due_time TEXT NOT NULL DEFAULT '',
		repeat_json TEXT NOT NULL DEFAULT '', series_id INTEGER NOT NULL DEFAULT 0,
		done_at INTEGER, notify_1h_at INTEGER, notify_due_at INTEGER, notify_nextday_at INTEGER,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO reminders (group_id, title, due_date, created_at, updated_at)
		VALUES (0, '旧数据', '2026-09-29', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := OpenWithKey(dbPath, filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("老库打开/迁移失败（本次修复的回归场景）: %v", err)
	}
	defer s.Close()
	// 迁移后旧数据可读、新功能列可用（建子任务走 parent_id + 索引）
	board, _, _, err := s.ReminderBoard()
	if err != nil || len(board) == 0 || board[0].Title != "旧数据" {
		t.Fatalf("迁移后看板异常: items=%d err=%v", len(board), err)
	}
	parent := &Reminder{Title: "父任务", DueDate: "2026-09-30"}
	if err := s.CreateReminder(parent); err != nil {
		t.Fatal(err)
	}
	child := &Reminder{Title: "子任务", DueDate: "2026-09-30", ParentID: parent.ID}
	if err := s.CreateReminder(child); err != nil {
		t.Fatalf("迁移后建子任务: %v", err)
	}
	if _, err := s.db.Exec(`SELECT 1 FROM reminders WHERE parent_id = ?`, parent.ID); err != nil {
		t.Fatalf("parent_id 列/索引不可用: %v", err)
	}
}

func TestReminderCompleteReopenCycle(t *testing.T) {
	s := openTestStore(t)
	r := &Reminder{Title: "每天站会", DueDate: "2026-09-29", Repeat: `{"type":"daily"}`}
	if err := s.CreateReminder(r); err != nil {
		t.Fatal(err)
	}
	// 完成一次：生成下一条（prev_id 指向本条），系列内未完成恒 1 条
	next, err := s.CompleteReminder(r.ID)
	if err != nil || next == nil {
		t.Fatalf("完成应生成下一条: next=%v err=%v", next, err)
	}
	if next.PrevID != r.ID {
		t.Fatalf("下一条 prev_id = %d, want %d", next.PrevID, r.ID)
	}
	// 恢复完成的那条：撤回下一条，回到完成前状态
	removed, err := s.ReopenReminder(r.ID)
	if err != nil || removed == nil || removed.ID != next.ID {
		t.Fatalf("恢复应撤回下一条: removed=%v err=%v", removed, err)
	}
	back, err := s.GetReminder(r.ID)
	if err != nil || back.DoneAt != nil {
		t.Fatalf("恢复后应未完成: %+v err=%v", back, err)
	}
	if _, err := s.GetReminder(next.ID); err == nil {
		t.Fatal("撤回的下一条应已删除")
	}
	// 再次完成：重新生成恰好一条，不越积越多
	next2, err := s.CompleteReminder(r.ID)
	if err != nil || next2 == nil {
		t.Fatalf("再次完成应生成下一条: next=%v err=%v", next2, err)
	}
	var opens int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM reminders WHERE series_id = ? AND done_at IS NULL`, next2.SeriesID).Scan(&opens); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("系列内未完成应 1 条, got %d", opens)
	}
	// 不重复任务：恢复只清完成标记，无下一条可撤
	r2 := &Reminder{Title: "一次性", DueDate: "2026-09-29"}
	if err := s.CreateReminder(r2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteReminder(r2.ID); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.ReopenReminder(r2.ID); err != nil || removed != nil {
		t.Fatalf("不重复任务恢复: removed=%v err=%v", removed, err)
	}
	if back2, _ := s.GetReminder(r2.ID); back2.DoneAt != nil {
		t.Fatal("不重复任务恢复后应未完成")
	}
}

// 回收站：删除软删（同系列未完成后续一并，已完成历史保留）→ 看板与到期调度排除
// → 恢复（原分组已删回落未分组）→ 单条彻底删除 / 清空。
func TestReminderTrash(t *testing.T) {
	s := openTestStore(t)
	gid, err := s.CreateReminderGroup("工作", 0)
	if err != nil {
		t.Fatal(err)
	}
	r1 := &Reminder{GroupID: gid, Title: "每天站会", DueDate: "2026-09-29", Repeat: `{"type":"daily"}`}
	if err := s.CreateReminder(r1); err != nil {
		t.Fatal(err)
	}
	next, err := s.CompleteReminder(r1.ID)
	if err != nil || next == nil {
		t.Fatalf("完成应生成下一条: err=%v", err)
	}
	r3 := &Reminder{Title: "一次性", DueDate: "2030-01-01"}
	if err := s.CreateReminder(r3); err != nil {
		t.Fatal(err)
	}

	// 删除系列里未完成的那条：本条进回收站，已完成的 r1 保留在已完成列表
	if err := s.DeleteReminder(next.ID); err != nil {
		t.Fatal(err)
	}
	trash, err := s.ListReminderTrash()
	if err != nil || len(trash) != 1 || trash[0].ID != next.ID {
		t.Fatalf("回收站应只有被删的下一条: len=%d err=%v", len(trash), err)
	}
	_, plan, done, err := s.ReminderBoard()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].ID != r3.ID {
		t.Fatalf("计划内应只剩一次性任务: len=%d", len(plan))
	}
	if len(done) != 1 || done[0].ID != r1.ID {
		t.Fatalf("已完成历史应保留: len=%d", len(done))
	}
	// 删除不重复任务：同样进回收站
	if err := s.DeleteReminder(r3.ID); err != nil {
		t.Fatal(err)
	}
	if trash, _ = s.ListReminderTrash(); len(trash) != 2 {
		t.Fatalf("回收站应 2 条, got %d", len(trash))
	}
	// 回收站中的条目不进到期提醒窗口
	due, err := s.DueRemindersWindow(0, 1<<40, "due")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range due {
		if r.DeletedAt > 0 {
			t.Fatal("回收站条目不应进入提醒调度窗口")
		}
	}
	// 删组后恢复：原分组已不存在，回落未分组
	if err := s.DeleteReminderGroup(gid); err != nil {
		t.Fatal(err)
	}
	back, err := s.RestoreReminder(next.ID)
	if err != nil || back == nil || back.GroupID != 0 || back.DeletedAt != 0 {
		t.Fatalf("恢复应回落未分组: back=%+v err=%v", back, err)
	}
	// 不在回收站的条目恢复应报错
	if _, err := s.RestoreReminder(r1.ID); err == nil {
		t.Fatal("不在回收站的条目恢复应报错")
	}
	// 单条彻底删除 + 清空
	if err := s.HardDeleteReminder(r3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetReminder(r3.ID); err == nil {
		t.Fatal("彻底删除后应查不到")
	}
	n, err := s.EmptyReminderTrash()
	if err != nil || n != 0 {
		t.Fatalf("清空后应无可删条目: n=%d err=%v", n, err)
	}
	if trash, _ = s.ListReminderTrash(); len(trash) != 0 {
		t.Fatalf("回收站应已清空, got %d", len(trash))
	}
}

// 是否提醒开关：notify=0 的事项仅记录——不进到期提醒调度窗口；开关可随时改。
func TestReminderNotifyToggle(t *testing.T) {
	s := openTestStore(t)
	off := &Reminder{Title: "仅记录", DueDate: "2030-01-01", Notify: false}
	if err := s.CreateReminder(off); err != nil {
		t.Fatal(err)
	}
	on := &Reminder{Title: "要提醒", DueDate: "2030-01-01", Notify: true}
	if err := s.CreateReminder(on); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"1h", "due", "nextday"} {
		got, err := s.DueRemindersWindow(0, 1<<40, kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range got {
			if r.ID == off.ID {
				t.Fatalf("notify=0 的条目不应进入 %s 提醒窗口", kind)
			}
		}
		if len(got) != 1 || got[0].ID != on.ID {
			t.Fatalf("%s 窗口应只有开启提醒的条目, got %d 条", kind, len(got))
		}
	}
	// 开关改回开：重新进调度
	off.Notify = true
	if err := s.UpdateReminder(off); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.DueRemindersWindow(0, 1<<40, "due"); len(got) != 2 {
		t.Fatalf("重新开启提醒后窗口应 2 条, got %d", len(got))
	}
}

// 子任务：最多 5 级、子任务继承父分组、删除父级全部后代一并进回收站、
// 恢复时父级不在（升顶级）、周期子任务完成后下一条沿用父任务。
func TestReminderSubtasks(t *testing.T) {
	s := openTestStore(t)
	gid, err := s.CreateReminderGroup("项目", 0)
	if err != nil {
		t.Fatal(err)
	}
	root := &Reminder{GroupID: gid, Title: "L1", DueDate: "2030-01-01"}
	if err := s.CreateReminder(root); err != nil {
		t.Fatal(err)
	}
	// 逐级建到第 5 级（子任务未显式给分组时继承父任务）
	chain := []*Reminder{root}
	for i := 2; i <= 5; i++ {
		kid := &Reminder{ParentID: chain[len(chain)-1].ID, Title: "L" + string(rune('0'+i)), DueDate: "2030-01-01"}
		if err := s.CreateReminder(kid); err != nil {
			t.Fatalf("建第 %d 级子任务: %v", i, err)
		}
		if kid.GroupID != gid {
			t.Fatalf("第 %d 级子任务应继承父分组, got group=%d", i, kid.GroupID)
		}
		chain = append(chain, kid)
	}
	// 第 6 级被拒
	six := &Reminder{ParentID: chain[len(chain)-1].ID, Title: "L6", DueDate: "2030-01-01"}
	if err := s.CreateReminder(six); err == nil || err.Error() != "子任务最多 5 级" {
		t.Fatalf("第 6 级应被拒绝, got err=%v", err)
	}
	// 父任务不存在/在回收站时不能挂子任务
	ghost := &Reminder{ParentID: 99999, Title: "孤儿", DueDate: "2030-01-01"}
	if err := s.CreateReminder(ghost); err == nil {
		t.Fatal("父任务不存在应报错")
	}
	// 删除 L2：L3~L5 全部后代一并进回收站，L1 保留
	if err := s.DeleteReminder(chain[1].ID); err != nil {
		t.Fatal(err)
	}
	for _, r := range chain[2:] {
		got, err := s.GetReminder(r.ID)
		if err != nil || got.DeletedAt == 0 {
			t.Fatalf("后代 %s 应一并进回收站: %+v err=%v", r.Title, got, err)
		}
	}
	if got, _ := s.GetReminder(root.ID); got.DeletedAt != 0 {
		t.Fatal("L1 不应进回收站")
	}
	// 恢复 L4：整链联动——上级 L3/L2 与后代 L5 一并恢复，链条完整不升级
	back, err := s.RestoreReminder(chain[3].ID)
	if err != nil || back == nil || back.ParentID != chain[2].ID {
		t.Fatalf("恢复 L4 应整链恢复且链完整: back=%+v err=%v", back, err)
	}
	for i, r := range chain {
		got, err := s.GetReminder(r.ID)
		if err != nil || got.DeletedAt != 0 {
			t.Fatalf("整链恢复后 L%d 应在世: %+v err=%v", i+1, got, err)
		}
	}
	if got, _ := s.GetReminder(root.ID); got.DeletedAt != 0 {
		t.Fatal("L1 本就未删，不受影响")
	}
	// 断链：再删 L2（L3~L5 进回收站），彻底删 L4，恢复 L5——上级 L4 已无行，
	// 链断：仅 L5 恢复并升顶级，L2/L3 留在回收站
	if err := s.DeleteReminder(chain[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.HardDeleteReminder(chain[3].ID); err != nil {
		t.Fatal(err)
	}
	back5, err := s.RestoreReminder(chain[4].ID)
	if err != nil || back5 == nil || back5.ParentID != 0 {
		t.Fatalf("断链恢复 L5 应升顶级: back=%+v err=%v", back5, err)
	}
	for _, i := range []int{1, 2} {
		if got, _ := s.GetReminder(chain[i].ID); got.DeletedAt == 0 {
			t.Fatalf("L%d 未在恢复集内，应留在回收站", i+1)
		}
	}
	// 周期子任务完成：下一条沿用 parent_id
	leaf := &Reminder{ParentID: root.ID, Title: "每日检查", DueDate: "2030-01-01", Repeat: `{"type":"daily"}`}
	if err := s.CreateReminder(leaf); err != nil {
		t.Fatal(err)
	}
	next, err := s.CompleteReminder(leaf.ID)
	if err != nil || next == nil {
		t.Fatalf("周期子任务完成应生成下一条: err=%v", err)
	}
	if next.ParentID != root.ID {
		t.Fatalf("下一条应沿用父任务, got parent=%d", next.ParentID)
	}
}
