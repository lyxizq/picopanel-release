package medication

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

func openMedTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "med.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestEmitDoseGroupMergedPushSplitMessages 同刻多药合并口径（2026-11-03 用户指定）：
// 站内消息按归属空间拆分（成员只看自己的），三渠道家庭广播整组只推一条（渠道
// 开关全关时 pushChannels 为空操作，测试安全）；剂量行全部打标不再重推。
func TestEmitDoseGroupMergedPushSplitMessages(t *testing.T) {
	s := openMedTestStore(t)
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	famID, err := s.CreateUser("fam", "pass1234", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	// 人员：张三未绑定（管理员空间 0）；fam 绑定成员账号
	p1 := &store.MedPerson{Name: "张三"}
	if err := s.CreateMedPerson(p1); err != nil {
		t.Fatal(err)
	}
	p2, err := s.MedPersonByUser(famID)
	if err != nil {
		t.Fatal(err)
	}
	mkMed := func(personID int64, name string) {
		if err := s.CreateMedication(&store.Medication{PersonID: personID, Name: name,
			Form: "tablet", DoseAmount: 1, Kind: "daily", Times: []string{"08:00"}}); err != nil {
			t.Fatal(err)
		}
	}
	mkMed(p1.ID, "钙片")
	mkMed(p1.ID, "鱼油") // 同人同刻第二味
	mkMed(p2.ID, "维C")

	// 为明天排程（2026-11-03 起 EnsureMedDay 不回填早于药物最近改动时刻的历史
	// 槽位——建药即回推当天已过时刻的误推送已修）：明天的槽位恒为未来，
	// 任何时刻运行测试都会建行
	day := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	if err := s.EnsureMedDay(day); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueMedLogs(day, "23:59")
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 3 {
		t.Fatalf("应预建 3 条 pending: %d", len(due))
	}
	groups := groupBySlot(due)
	if len(groups) != 1 {
		t.Fatalf("同刻应合成一组: %d", len(groups))
	}
	emitDoseGroup(s, time.Now().Unix(), groups[0], false)

	// 站内消息按空间拆分：管理员空间 1 条（同人两味合并）、fam 空间 1 条
	pageAdmin, err := s.ListMessages(0, "medication", false, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if pageAdmin.Total != 1 || !strings.Contains(pageAdmin.Items[0].Title, "2 个剂量到点") {
		t.Fatalf("管理员空间应 1 条合并消息: %+v", pageAdmin.Items)
	}
	pageFam, err := s.ListMessages(famID, "medication", false, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if pageFam.Total != 1 || !strings.Contains(pageFam.Items[0].Title, "维C") {
		t.Fatalf("成员空间应 1 条自己的消息: %+v", pageFam.Items)
	}
	// 跨空间不可见
	if pageAdmin.Items[0].UserID != 0 || pageFam.Items[0].UserID != famID {
		t.Fatalf("消息归属空间不符: admin=%d fam=%d", pageAdmin.Items[0].UserID, pageFam.Items[0].UserID)
	}

	// 全部打标：再次扫描无到期剂量（不再重推）
	due2, err := s.DueMedLogs(day, "23:59")
	if err != nil {
		t.Fatal(err)
	}
	if len(due2) != 0 {
		t.Fatalf("推送后应无到期未打标剂量: %d", len(due2))
	}
}

// TestDoseTextsMergedTitle 整组（跨人员）合并文案：计数标题+逐条清单。
func TestDoseTextsMergedTitle(t *testing.T) {
	grp := []store.MedDose{
		{PersonName: "张三", MedName: "钙片", Time: "08:00", DoseAmount: 1},
		{PersonName: "李四", MedName: "维C", Time: "08:00", DoseAmount: 2, DoseUnit: "片"},
	}
	title, content := doseTexts(grp, false)
	if title != "用药提醒：2 个剂量到点" {
		t.Fatalf("合并标题不符: %q", title)
	}
	if !strings.Contains(content, "张三 · 钙片") || !strings.Contains(content, "李四 · 维C") {
		t.Fatalf("合并正文应逐条列人名药名: %q", content)
	}
	lateTitle, _ := doseTexts(grp, true)
	if lateTitle != "可能漏服：2 个剂量未打卡" {
		t.Fatalf("催推合并标题不符: %q", lateTitle)
	}
}

// TestSweepLowStockNoRef 低库存消息不带 ref（2026-11-03 修）：m.ID 是 medications
// 行 ID，与消息中心「完成」按钮消费的 med_logs 行 ID 分属两张独立自增表——
// 错挂后 ref_done 联查碰巧命中无关剂量行会误打卡误扣库存，补货消息一律 ref_id=0。
func TestSweepLowStockNoRef(t *testing.T) {
	s := openMedTestStore(t)
	p := &store.MedPerson{Name: "张三"}
	if err := s.CreateMedPerson(p); err != nil {
		t.Fatal(err)
	}
	low := 0.5
	m := &store.Medication{PersonID: p.ID, Name: "阿莫西林", Kind: "daily", Times: []string{"08:00"},
		StockQty: &low, StockAlert: 2}
	if err := s.CreateMedication(m); err != nil {
		t.Fatal(err)
	}
	sweepLowStock(s, time.Now().Unix())
	page, err := s.ListMessages(0, "medication", false, 1, 10)
	if err != nil || page.Total != 1 {
		t.Fatalf("应 1 条补货消息: %d %v", page.Total, err)
	}
	msg := page.Items[0]
	if msg.RefID != 0 || msg.RefType != "" {
		t.Fatalf("补货消息不应带 ref（防误打卡无关剂量）: %+v", msg)
	}
	if !msg.RefDone {
		t.Fatal("无 ref 的补货消息应视为已办结（不显示完成按钮）")
	}
	if !strings.Contains(msg.Title, "补货") {
		t.Fatalf("应为补货消息: %q", msg.Title)
	}
}
