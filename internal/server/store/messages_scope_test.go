package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestMessagesUserScope 消息中心空间隔离（2026-11-03 家庭成员口径）：0=管理员
// 空间（含全局消息），>0=成员个人消息；列表/角标/已读/删除/清空全部按空间过滤，
// 跨空间操作等于不存在（静默成功不泄露）。
func TestMessagesUserScope(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().Unix()
	mk := func(uid int64, typ string) {
		if err := s.CreateMessage(&Message{Type: typ, Title: "t", UserID: uid, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	mk(0, "alert")  // 管理员空间的全局告警
	mk(0, "login")  // 管理员自己的登录消息
	mk(5, "login")  // 成员 5 的登录消息
	mk(5, "mail")   // 成员 5 的邮件提醒
	mk(6, "login")  // 成员 6 的消息

	// 列表互不可见：管理员只看 0 空间 2 条，成员 5 只看自己 2 条，看不到全局/他人
	pa, err := s.ListMessages(0, "", false, 1, 100)
	if err != nil || pa.Total != 2 || pa.Unread != 2 {
		t.Fatalf("管理员空间应 2 条: total=%d unread=%d err=%v", pa.Total, pa.Unread, err)
	}
	p5, err := s.ListMessages(5, "", false, 1, 100)
	if err != nil || p5.Total != 2 {
		t.Fatalf("成员 5 空间应 2 条: total=%d err=%v", p5.Total, err)
	}
	for _, m := range p5.Items {
		if m.Type == "alert" {
			t.Fatal("成员不应看到管理员空间的全局告警")
		}
	}
	p6, _ := s.ListMessages(6, "", false, 1, 100)
	if p6.Total != 1 {
		t.Fatalf("成员 6 空间应 1 条: %d", p6.Total)
	}

	// 角标按空间：成员 5 的角标只含自己
	if n, _ := s.UnreadCount(5); n != 2 {
		t.Fatalf("成员 5 未读应 2: %d", n)
	}
	if n, _ := s.UnreadCount(0); n != 2 {
		t.Fatalf("管理员未读应 2: %d", n)
	}

	// 单条标已读：成员 5 标自己的一条；跨空间标管理员的 id 静默不动
	var mine, theirs int64
	for _, m := range p5.Items {
		if m.Type == "mail" {
			mine = m.ID
		} else {
			theirs = m.ID
		}
	}
	adminAlert := pa.Items[0].ID
	if err := s.MarkMessageRead(5, mine); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount(5); n != 1 {
		t.Fatalf("标已读后成员 5 未读应 1: %d", n)
	}
	if err := s.MarkMessageRead(5, adminAlert); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount(0); n != 2 {
		t.Fatalf("跨空间标已读不应生效，管理员未读应仍 2: %d", n)
	}

	// 全部标已读按空间
	if n, err := s.MarkAllMessagesRead(5); err != nil || n != 1 {
		t.Fatalf("成员 5 全量标已读应 1 条: n=%d err=%v", n, err)
	}
	if n, _ := s.UnreadCount(0); n != 2 {
		t.Fatalf("成员标已读不应波及管理员空间: %d", n)
	}

	// 按类型标已读按空间：管理员只清自己空间的 login，不动成员 6 的
	if n, err := s.MarkMessagesReadByType(0, "login"); err != nil || n != 1 {
		t.Fatalf("管理员按类型应清 1 条: n=%d err=%v", n, err)
	}
	if n, _ := s.UnreadCount(6); n != 1 {
		t.Fatalf("成员 6 的登录消息不应被管理员清掉: %d", n)
	}

	// 删除跨空间等于不存在：成员 5 删管理员的消息，管理员列表不动
	if err := s.DeleteMessage(5, adminAlert); err != nil {
		t.Fatal(err)
	}
	if pa2, _ := s.ListMessages(0, "", false, 1, 100); pa2.Total != 2 {
		t.Fatalf("跨空间删除不应生效: %d", pa2.Total)
	}
	if err := s.DeleteMessage(5, theirs); err != nil {
		t.Fatal(err)
	}
	if p5b, _ := s.ListMessages(5, "", false, 1, 100); p5b.Total != 1 {
		t.Fatalf("删自己消息应生效: %d", p5b.Total)
	}

	// 清空按空间：成员 5 清空只动自己，管理员与成员 6 不受牵连
	if n, err := s.ClearMessages(5); err != nil || n != 1 {
		t.Fatalf("成员 5 清空应删 1 条: n=%d err=%v", n, err)
	}
	if n, _ := s.UnreadCount(6); n != 1 {
		t.Fatalf("清空不应波及成员 6: %d", n)
	}
	if n, _ := s.UnreadCount(0); n != 1 {
		t.Fatalf("清空不应波及管理员空间: %d", n)
	}
}

// TestMedicationMessageRefDone 用药消息 ref_done 联查（2026-11-03）：剂量未打卡
// （pending）=未办结（消息中心显示「完成」按钮）；打卡后（taken）=已办结（按钮
// 消失，防重复点）；台账行被删视为已办结。
func TestMedicationMessageRefDone(t *testing.T) {
	s := openUserTestStore(t)
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	p := &MedPerson{Name: "张三"}
	if err := s.CreateMedPerson(p); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMedication(&Medication{PersonID: p.ID, Name: "钙片", Form: "tablet",
		DoseAmount: 1, Kind: "daily", Times: []string{"08:00"}}); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("2006-01-02")
	// 回拨建药时刻绕过「早于改动时刻的历史槽位不回填」新语义，全天槽位照建
	if _, err := s.db.Exec(`UPDATE medications SET created_at = 0, updated_at = 0`); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureMedDay(today); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueMedLogs(today, "23:59")
	if err != nil || len(due) != 1 {
		t.Fatalf("应预建 1 条 pending: %d %v", len(due), err)
	}
	logID := due[0].ID

	if err := s.CreateMessage(&Message{Type: "medication", Title: "用药提醒", RefType: "medication",
		RefID: logID, UserID: 0, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListMessages(0, "medication", false, 1, 10)
	if err != nil || page.Total != 1 || page.Items[0].RefDone {
		t.Fatalf("pending 剂量应视为未办结: %+v %v", page.Items, err)
	}

	if err := s.SetMedLogStatus(due[0].MedID, today, "08:00", "taken", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListMessages(0, "medication", false, 1, 10)
	if err != nil || page.Total != 1 || !page.Items[0].RefDone {
		t.Fatalf("打卡后应视为已办结: %+v %v", page.Items, err)
	}
}

// TestMergedMessageRefs 合并消息逐项引用（2026-11-03）：refs 全办结才算行级
// 已办结（ref_done）；MessageItems 按 refs 顺序返回逐项状态。
func TestMergedMessageRefs(t *testing.T) {
	s := openUserTestStore(t)
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	p := &MedPerson{Name: "张三"}
	if err := s.CreateMedPerson(p); err != nil {
		t.Fatal(err)
	}
	mkMed := func(name string) {
		if err := s.CreateMedication(&Medication{PersonID: p.ID, Name: name, Form: "tablet",
			DoseAmount: 1, Kind: "daily", Times: []string{"08:00"}}); err != nil {
			t.Fatal(err)
		}
	}
	mkMed("钙片")
	mkMed("鱼油")
	today := time.Now().Format("2006-01-02")
	// 回拨建药时刻绕过「早于改动时刻的历史槽位不回填」新语义，全天槽位照建
	if _, err := s.db.Exec(`UPDATE medications SET created_at = 0, updated_at = 0`); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureMedDay(today); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueMedLogs(today, "23:59")
	if err != nil || len(due) != 2 {
		t.Fatalf("应 2 条 pending: %d %v", len(due), err)
	}
	if err := s.CreateMessage(&Message{Type: "medication", Title: "用药提醒：2 个剂量到点",
		RefID: 0, Refs: []int64{due[0].ID, due[1].ID}, UserID: 0, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}

	// 两剂都未打：行级未办结
	page, _ := s.ListMessages(0, "medication", false, 1, 10)
	if page.Total != 1 || page.Items[0].RefDone {
		t.Fatalf("合并消息两剂未打应未办结: %+v", page.Items)
	}
	// 逐项清单：按 refs 顺序、未打卡
	items, err := s.MessageItems(&page.Items[0])
	if err != nil || len(items) != 2 || items[0].Title != "张三 · 钙片" || items[0].Done {
		t.Fatalf("逐项清单不符: %+v %v", items, err)
	}

	// 打卡第一剂：行级仍未办结（另一剂 pending），对应项 done
	if err := s.SetMedLogStatus(due[0].MedID, today, "08:00", "taken", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	page, _ = s.ListMessages(0, "medication", false, 1, 10)
	if page.Items[0].RefDone {
		t.Fatal("一剂已打一剂未打，行级应未办结")
	}
	items, _ = s.MessageItems(&page.Items[0])
	if !items[0].Done || items[1].Done {
		t.Fatalf("逐项状态不符: %+v", items)
	}

	// 两剂全打：行级已办结
	if err := s.SetMedLogStatus(due[1].MedID, today, "08:00", "taken", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	page, _ = s.ListMessages(0, "medication", false, 1, 10)
	if !page.Items[0].RefDone {
		t.Fatal("全部办结后行级应为已办结")
	}
}

// TestMedMessageRefFixMigration 低库存消息 ref 命名空间修正迁移（2026-11-03）：
// type=medication 且 ref_id 不指向任何 med_logs 行的（曾错存 medications 行 ID
// 的低库存消息）启动迁移归零；正确的 med_logs 引用不受影响。
func TestMedMessageRefFixMigration(t *testing.T) {
	dir := t.TempDir()
	dbPath, keyPath := filepath.Join(dir, "t.db"), filepath.Join(dir, "key")
	s, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	p := &MedPerson{Name: "张三"}
	if err := s.CreateMedPerson(p); err != nil {
		t.Fatal(err)
	}
	m := &Medication{PersonID: p.ID, Name: "钙片", Kind: "daily", Times: []string{"08:00"}}
	if err := s.CreateMedication(m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE medications SET created_at = 0, updated_at = 0 WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("2006-01-02")
	if err := s.EnsureMedDay(today); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueMedLogs(today, "23:59")
	if err != nil || len(due) != 1 {
		t.Fatalf("应预建 1 条 pending: %d %v", len(due), err)
	}
	// 正确消息（ref=med_logs 行）+ 错挂消息（ref=不存在的 med_logs ID）
	if err := s.CreateMessage(&Message{Type: "medication", Title: "到点", RefType: "medication",
		RefID: due[0].ID, UserID: 0, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMessage(&Message{Type: "medication", Title: "补货", RefType: "medication",
		RefID: 999999, UserID: 0, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 重开触发启动迁移
	s2, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	page, err := s2.ListMessages(0, "medication", false, 1, 10)
	if err != nil || page.Total != 2 {
		t.Fatalf("应 2 条消息: %d %v", page.Total, err)
	}
	for _, msg := range page.Items {
		switch msg.Title {
		case "到点":
			if msg.RefID != due[0].ID || msg.RefDone {
				t.Fatalf("正确引用不应被迁移改写: %+v", msg)
			}
		case "补货":
			if msg.RefID != 0 {
				t.Fatalf("错挂引用应归零: %+v", msg)
			}
		}
	}
}
