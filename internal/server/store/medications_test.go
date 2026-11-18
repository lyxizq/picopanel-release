package store

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

// 用药提醒（2026-11-02，分家庭成员）口径钉死：人员/药物校验与级联、四种时间表
// 当日判定（每天/每周几/每隔 N 天锚定/按需）、启用窗口（开始日+连续 N 天+归档）、
// 日结生成幂等与漏服日结、打卡状态机与库存扣减、低库存取数与改库重置、分人统计
// 与月历/依从率聚合、两处重排。
func TestMedications(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// ---- 人员：校验 + 色板兜底 + 重排 ----
	if err := s.CreateMedPerson(&MedPerson{Name: "  "}); err == nil {
		t.Fatal("空姓名应报错")
	}
	pa := &MedPerson{Name: " 张三 ", Color: "red"}
	if err := s.CreateMedPerson(pa); err == nil {
		t.Fatal("坏色值应报错")
	}
	pa = &MedPerson{Name: "张三"}
	if err := s.CreateMedPerson(pa); err != nil {
		t.Fatal(err)
	}
	pb := &MedPerson{Name: "李四"}
	if err := s.CreateMedPerson(pb); err != nil {
		t.Fatal(err)
	}
	if pb.Color == "" || pb.Color[0] != '#' {
		t.Fatalf("空色值应落色板兜底: %q", pb.Color)
	}
	if err := s.ReorderMedPersons([]int64{pb.ID, pa.ID}); err != nil {
		t.Fatal(err)
	}
	plist, err := s.ListMedPersons()
	if err != nil {
		t.Fatal(err)
	}
	if len(plist) != 2 || plist[0].Name != "李四" || plist[0].Sort != 1 {
		t.Fatalf("人员重排口径错: %+v", plist)
	}

	// ---- 药物：校验与规整 ----
	bad := &Medication{PersonID: pa.ID, Name: "", Kind: "daily", Times: []string{"08:00"}}
	if err := s.CreateMedication(bad); err == nil {
		t.Fatal("缺药名应报错")
	}
	if err := s.CreateMedication(&Medication{PersonID: 999, Name: "x", Kind: "daily", Times: []string{"08:00"}}); err == nil {
		t.Fatal("不存在的人员应报错")
	}
	if err := s.CreateMedication(&Medication{PersonID: pa.ID, Name: "x", Kind: "daily", Times: []string{"bad", "25:00", "08:0"}}); err == nil {
		t.Fatal("规整后无有效时刻应报错")
	}
	if err := s.CreateMedication(&Medication{PersonID: pa.ID, Name: "x", Kind: "interval", Times: []string{"08:00"}, IntervalDays: 1}); err == nil {
		t.Fatal("间隔<2 应报错")
	}
	if err := s.CreateMedication(&Medication{PersonID: pa.ID, Name: "x", Kind: "interval", Times: []string{"08:00"}, IntervalDays: 2}); err == nil {
		t.Fatal("interval 缺锚点应报错")
	}
	if err := s.CreateMedication(&Medication{PersonID: pa.ID, Name: "x", Kind: "weekdays", Times: []string{"08:00"}, Weekdays: []int{9}}); err == nil {
		t.Fatal("weekdays 无有效星期应报错")
	}

	today := time.Now()
	todayStr := today.Format("2006-01-02")
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := today.AddDate(0, 0, 1).Format("2006-01-02")

	// 每天 08:00/20:00 各一剂
	daily := &Medication{PersonID: pa.ID, Name: "阿莫西林", Form: "capsule", DoseAmount: 2, DoseUnit: "粒",
		Kind: "daily", Times: []string{"20:00", "08:00"}, StockQty: f64(10), StockAlert: 2}
	if err := s.CreateMedication(daily); err != nil {
		t.Fatal(err)
	}
	if len(daily.Times) != 2 || daily.Times[0] != "08:00" {
		t.Fatalf("时刻应排序去重: %v", daily.Times)
	}
	// 每周一 09:00（若今天非周一，今天不排）
	wd := &Medication{PersonID: pa.ID, Name: "维生素", Kind: "weekdays", Times: []string{"09:00"}, Weekdays: []int{1}}
	if err := s.CreateMedication(wd); err != nil {
		t.Fatal(err)
	}
	// 每隔 2 天 10:00，锚今天（今天排、明天不排）
	iv := &Medication{PersonID: pb.ID, Name: "钙片", Kind: "interval", Times: []string{"10:00"},
		IntervalDays: 2, StartDate: todayStr}
	if err := s.CreateMedication(iv); err != nil {
		t.Fatal(err)
	}
	// 按需
	prn := &Medication{PersonID: pb.ID, Name: "退烧药", Kind: "prn", StockQty: f64(5), StockAlert: 1}
	if err := s.CreateMedication(prn); err != nil {
		t.Fatal(err)
	}

	// medDosesOn 四形态当日判定
	if got := medDosesOn(daily, todayStr); len(got) != 2 || got[0] != "08:00" || got[1] != "20:00" {
		t.Fatalf("daily 当日剂量错: %v", got)
	}
	if got := medDosesOn(wd, todayStr); len(got) > 0 && weekdayOf(todayStr) != 1 {
		t.Fatalf("非周一不应排程: %v", got)
	}
	if got := medDosesOn(wd, tomorrow); len(got) > 0 && weekdayOf(tomorrow) != 1 {
		t.Fatalf("非周一不应排程: %v", got)
	}
	if got := medDosesOn(iv, todayStr); len(got) != 1 {
		t.Fatalf("interval 锚点日应排程: %v", got)
	}
	if got := medDosesOn(iv, tomorrow); len(got) != 0 {
		t.Fatalf("interval 间日不应排程: %v", got)
	}
	if got := medDosesOn(prn, todayStr); len(got) != 0 {
		t.Fatalf("prn 不应排程: %v", got)
	}
	archived := *daily
	now := time.Now().Unix()
	archived.ArchivedAt = &now
	if got := medDosesOn(&archived, todayStr); len(got) != 0 {
		t.Fatalf("归档不应排程: %v", got)
	}
	// 启用窗口：未到开始日 / 连续 N 天服满
	notStarted := *daily
	notStarted.StartDate = tomorrow
	if got := medDosesOn(&notStarted, todayStr); len(got) != 0 {
		t.Fatalf("未到开始日不应排程: %v", got)
	}
	finite := *daily
	finite.StartDate = yesterday
	finite.DurationDays = 2
	if got := medDosesOn(&finite, todayStr); len(got) != 2 {
		t.Fatalf("连续 2 天（昨起）今天仍在窗口内: %v", got)
	}
	if got := medDosesOn(&finite, tomorrow); len(got) != 0 {
		t.Fatalf("连续 2 天（昨起）明天应已结束: %v", got)
	}

	// ---- 日结生成 + 幂等 ----
	// 回拨建药时刻：2026-11-03 起 EnsureMedDay 不回填早于药物最近改动时刻的
	// 历史槽位（建药即回推当天已过时刻会造成误推送，已修）；本节要验证全天
	// 槽位生成与状态机，回拨到 0 后照旧全量排程
	for _, id := range []int64{daily.ID, wd.ID, iv.ID} {
		if _, err := s.db.Exec(`UPDATE medications SET created_at = 0, updated_at = 0 WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.EnsureMedDay(todayStr); err != nil {
		t.Fatal(err)
	}
	day1, err := s.MedDosesView(todayStr, 0)
	if err != nil {
		t.Fatal(err)
	}
	// daily 2 剂 + interval 1 剂 + （若今天周一）weekdays 1 剂
	want := 3
	if weekdayOf(todayStr) == 1 {
		want = 4
	}
	if len(day1) != want {
		t.Fatalf("日结剂量数应 %d 得 %d: %+v", want, len(day1), day1)
	}
	if err := s.EnsureMedDay(todayStr); err != nil {
		t.Fatal(err)
	}
	day2, _ := s.MedDosesView(todayStr, 0)
	if len(day2) != len(day1) {
		t.Fatalf("日结应幂等: %d vs %d", len(day1), len(day2))
	}
	// 按人过滤
	dayPa, _ := s.MedDosesView(todayStr, pa.ID)
	for _, d := range dayPa {
		if d.PersonID != pa.ID {
			t.Fatalf("按人过滤失效: %+v", d)
		}
	}

	// ---- 打卡状态机 + 库存扣减/回冲 ----
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "bad", now); err == nil {
		t.Fatal("非法状态应报错")
	}
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "taken", now); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMedication(daily.ID)
	if got.StockQty == nil || *got.StockQty != 8 { // 10 - 2
		t.Fatalf("服用应扣库存 10→8: %v", got.StockQty)
	}
	// 撤销 → 库存回冲；missed 补卡 → 再扣；taken 重复打卡 → 不重复扣
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "pending", now); err != nil {
		t.Fatal("撤销打卡应允许")
	}
	got, _ = s.GetMedication(daily.ID)
	if got.StockQty == nil || *got.StockQty != 10 {
		t.Fatalf("撤销应回冲库存 8→10: %v", got.StockQty)
	}
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "taken", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "taken", now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetMedication(daily.ID)
	if got.StockQty == nil || *got.StockQty != 8 {
		t.Fatalf("重复 taken 不应重复扣库存: %v", got.StockQty)
	}
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "missed", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "taken", now); err != nil {
		t.Fatal("missed 补卡应允许")
	}
	got, _ = s.GetMedication(daily.ID)
	if got.StockQty == nil || *got.StockQty != 8 { // missed 回冲 10 → 补卡再扣 8
		t.Fatalf("补卡后库存应 8: %v", got.StockQty)
	}

	// ---- 库存不足剂量的往返净零（2026-11-06 审计批）----
	// 旧实现扣减钳零 + 回冲全额不对称：0.3 服一剂（剂量 2）钳到 0、撤销回冲 +2
	// 变 2.0，往返净增库存；改为透支记账（允许负值），往返严格净零
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "pending", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMedStock(daily.ID, 0.3); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "taken", now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetMedication(daily.ID)
	if got.StockQty == nil || math.Abs(*got.StockQty-(-1.7)) > 1e-9 {
		t.Fatalf("不足剂量服用应记负库存 0.3-2=-1.7: %v", got.StockQty)
	}
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "pending", now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetMedication(daily.ID)
	if got.StockQty == nil || math.Abs(*got.StockQty-0.3) > 1e-9 {
		t.Fatalf("撤销应精确回冲到 0.3（旧钳零+全额回冲会得 2.0）: %v", got.StockQty)
	}
	if err := s.SetMedLogStatus(daily.ID, todayStr, "08:00", "taken", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMedStock(daily.ID, 8); err != nil { // 恢复后续断言的库存轨迹
		t.Fatal(err)
	}

	// ---- 漏服日结：昨天 pending → missed ----
	if _, err := s.db.Exec("INSERT INTO med_logs (med_id, day, time, status, created_at) VALUES (?, ?, '08:00', 'pending', ?)",
		daily.ID, yesterday, now); err != nil {
		t.Fatal(err)
	}
	n, err := s.RollMissedMedLogs(todayStr)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("漏服日结应命中 1 行得 %d", n)
	}

	// ---- 到点/催推取数 ----
	due, err := s.DueMedLogs(todayStr, "23:59")
	if err != nil {
		t.Fatal(err)
	}
	if len(due) < 1 {
		t.Fatal("到点取数不应为空（已到时刻的 pending）")
	}
	for _, d := range due {
		if d.Status != "pending" || d.Time > "23:59" {
			t.Fatalf("到点取数口径错: %+v", d)
		}
	}
	if err := s.MarkMedLogNotified(due[0].ID, now); err != nil {
		t.Fatal(err)
	}
	esc, err := s.EscalatableMedLogs(todayStr, "23:59")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range esc {
		if d.ID == due[0].ID {
			found = true
		}
	}
	if !found {
		t.Fatal("已推未催的行应进催推取数")
	}

	// ---- 低库存：跌破警戒线进取数，改库存重置武装 ----
	if err := s.SetMedLogStatus(daily.ID, todayStr, "20:00", "taken", now); err != nil {
		t.Fatal(err)
	}
	low, err := s.LowStockMeds()
	if err != nil {
		t.Fatal(err)
	}
	// 库存 6-2=4 仍在警戒线 2 上，不应进低库存取数
	if len(low) != 0 {
		t.Fatalf("库存 4 > 警戒 2 不应进低库存取数: %+v", low)
	}
	// 改库存到线上验证武装/重置闭环
	if err := s.UpdateMedStock(daily.ID, 2); err != nil {
		t.Fatal(err)
	}
	if low, err = s.LowStockMeds(); err != nil {
		t.Fatal(err)
	}
	if len(low) != 1 || low[0].ID != daily.ID {
		t.Fatalf("库存=警戒线应进低库存取数: %+v", low)
	}
	if err := s.MarkMedStockNotified(daily.ID, now); err != nil {
		t.Fatal(err)
	}
	if low, _ = s.LowStockMeds(); len(low) != 0 {
		t.Fatal("打标后应退出取数")
	}
	if err := s.UpdateMedStock(daily.ID, 5); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetMedication(daily.ID)
	if got.StockNotifiedAt != nil {
		t.Fatal("改库存应重置补货标记")
	}
	if low, _ = s.LowStockMeds(); len(low) != 0 {
		t.Fatal("库存回线上应不进取数")
	}

	// ---- prn 记一笔 ----
	if err := s.RecordMedPrn(daily.ID, now); err == nil {
		t.Fatal("非 prn 记一笔应报错")
	}
	if err := s.RecordMedPrn(prn.ID, now); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetMedication(prn.ID)
	if got.StockQty == nil || *got.StockQty != 4 { // 5-1
		t.Fatalf("prn 服用应扣库存 5→4: %v", got.StockQty)
	}

	// ---- 分人统计 + 月历 + 依从率 ----
	stats, err := s.MedTodayStats(todayStr)
	if err != nil {
		t.Fatal(err)
	}
	byPerson := map[int64]MedPersonStat{}
	for _, st := range stats {
		byPerson[st.PersonID] = st
	}
	// pa 计划剂量数：daily 2 剂 + 周一维生素 1 剂；已服 2（08:00 补卡 + 20:00 服用）
	paWant := 2
	if weekdayOf(todayStr) == 1 {
		paWant = 3
	}
	if byPerson[pa.ID].Total != paWant {
		t.Fatalf("张三统计口径错: %+v（want %d）", byPerson[pa.ID], paWant)
	}
	if byPerson[pa.ID].Taken != 2 {
		t.Fatalf("张三已服应 2: %+v", byPerson[pa.ID])
	}
	if byPerson[pb.ID].Total != 2 || byPerson[pb.ID].Taken != 1 {
		t.Fatalf("李四统计口径错: %+v", byPerson[pb.ID]) // interval pending 1 剂 + prn 记一笔 1 = 2 行，已服 1
	}
	// 下一剂：张三 daily 两剂已处理，周一还有维生素 pending；李四 interval pending 是 next
	if weekdayOf(todayStr) == 1 {
		if byPerson[pa.ID].NextTime != "09:00" || byPerson[pa.ID].NextName != "维生素" {
			t.Fatalf("张三 next 应 09:00 维生素: %+v", byPerson[pa.ID])
		}
	} else if byPerson[pa.ID].NextTime != "" {
		t.Fatalf("张三无待处理不应有 next: %+v", byPerson[pa.ID])
	}
	if byPerson[pb.ID].NextTime != "10:00" || byPerson[pb.ID].NextName != "钙片" {
		t.Fatalf("李四 next 应 10:00 钙片: %+v", byPerson[pb.ID])
	}

	month, err := s.MedMonthSummary(todayStr[:7], 0)
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, d := range month {
		if d.Day == todayStr {
			sum = d.Total + d.Taken
		}
	}
	if sum == 0 {
		t.Fatal("月历应含今日聚合")
	}
	total, taken, skipped, missed, err := s.MedAdherence(yesterday, tomorrow, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total < 4 || taken < 2 || missed < 1 {
		t.Fatalf("依从率聚合口径错: total=%d taken=%d skipped=%d missed=%d", total, taken, skipped, missed)
	}

	// ---- 药物重排 + 级联删除 ----
	if err := s.ReorderMedications([]int64{wd.ID, daily.ID}); err != nil {
		t.Fatal(err)
	}
	// 列表按 人员序 → 药序：张三名下 wd（sort1）应排 daily（sort2）前
	list, _ := s.ListMedications(pa.ID, false)
	if len(list) < 2 || list[0].ID != wd.ID || list[1].ID != daily.ID {
		t.Fatalf("药物重排口径错: %+v", list)
	}
	if err := s.DeleteMedPerson(pa.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMedication(daily.ID); err == nil {
		t.Fatal("删人员应级联删药物")
	}
	if _, err := s.GetMedPerson(pa.ID); err == nil {
		t.Fatal("人员应已删除")
	}
	if err := s.DeleteMedication(prn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMedication(prn.ID); err == nil {
		t.Fatal("药物应已删除")
	}
}

// f64 浮点指针helper（库存字段 *float64）。
func f64(v float64) *float64 { return &v }


// TestMedPersonByUser 用药人员↔登录账号绑定（2026-11-03 家庭成员口径）：首次取
// 自动建人（姓名=用户名、user_id=uid、色板轮转），再取返回同一人；管理员（0）
// 不走绑定；不存在的账号报错。
func TestMedPersonByUser(t *testing.T) {
	s := openTestStore(t)
	uid, err := s.CreateUser("bob", "pw-bob-123", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	p1, err := s.MedPersonByUser(uid)
	if err != nil {
		t.Fatal(err)
	}
	if p1.UserID != uid || p1.Name != "bob" || p1.Color == "" {
		t.Fatalf("自动建人口径错: %+v", p1)
	}
	p2, err := s.MedPersonByUser(uid)
	if err != nil || p2.ID != p1.ID {
		t.Fatalf("再取应返回同一绑定人: %+v vs %+v err=%v", p2, p1, err)
	}
	all, err := s.ListMedPersons()
	if err != nil || len(all) != 1 {
		t.Fatalf("只应自动建一个人: %+v err=%v", all, err)
	}
	if _, err := s.MedPersonByUser(999); err == nil {
		t.Fatal("不存在的账号应报错")
	}

	// 按 person 过滤的台账口径：绑定人（bob）与管理员代管人各建一药，日结建
	// pending 后，月历聚合 personID>0 只算该人的，0=全部
	pa := &MedPerson{Name: "代管家人"}
	if err := s.CreateMedPerson(pa); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("2006-01-02")
	if err := s.CreateMedication(&Medication{PersonID: p1.ID, Name: "bob药", Kind: "daily", Times: []string{"08:00"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMedication(&Medication{PersonID: pa.ID, Name: "代管药", Kind: "daily", Times: []string{"09:00"}}); err != nil {
		t.Fatal(err)
	}
	// 回拨建药时刻绕过「早于改动时刻的历史槽位不回填」新语义，全天槽位照建
	if _, err := s.db.Exec(`UPDATE medications SET created_at = 0, updated_at = 0`); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureMedDay(today); err != nil {
		t.Fatal(err)
	}
	month, err := s.MedMonthSummary(today[:7], p1.ID)
	if err != nil || len(month) != 1 || month[0].Total != 1 {
		t.Fatalf("按绑定人过滤月历应只 1 天 1 剂: %+v err=%v", month, err)
	}
	if monthAll, err := s.MedMonthSummary(today[:7], 0); err != nil || len(monthAll) != 1 || monthAll[0].Total != 2 {
		t.Fatalf("不过滤应全天 2 剂: %+v err=%v", monthAll, err)
	}
	if tot, _, _, _, err := s.MedAdherence(today, today, p1.ID); err != nil || tot != 1 {
		t.Fatalf("按绑定人过滤依从率应 1: %d err=%v", tot, err)
	}
}

// TestEnsureMedDayBackfillCutoff 历史槽位不回填 + 同日只全量跑一次（2026-11-03）：
// 建药晚于当天已过时刻的，EnsureMedDay 不补建已过槽位（防建药即补推旧剂量
// 误推送）；未来槽位照建；回拨建药时刻的老药不受影响（服务重启补推口径不变）。
// EnsureMedDayOnce 同日记账后不再全量跑（后建的药走 API 路径补排）。
func TestEnsureMedDayBackfillCutoff(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := &MedPerson{Name: "张三"}
	if err := s.CreateMedPerson(p); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	today := now.Format("2006-01-02")
	fresh := &Medication{PersonID: p.ID, Name: "新药", Kind: "daily", Times: []string{"00:01", "23:58"}}
	if err := s.CreateMedication(fresh); err != nil {
		t.Fatal(err)
	}
	old := &Medication{PersonID: p.ID, Name: "老药", Kind: "daily", Times: []string{"00:01", "23:58"}}
	if err := s.CreateMedication(old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE medications SET created_at = 0, updated_at = 0 WHERE id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureMedDay(today); err != nil {
		t.Fatal(err)
	}
	// 新药只建严格晚于当前时刻的槽位（同分钟槽按已过算，与实现口径一致）
	nowHM := now.Format("15:04")
	var want []string
	for _, tm := range []string{"00:01", "23:58"} {
		if tm > nowHM {
			want = append(want, tm)
		}
	}
	view, err := s.MedDosesView(today, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range view {
		if d.MedID == fresh.ID {
			got = append(got, d.Time)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("新药应只建未来槽位 %v，得 %v", want, got)
	}
	// 老药全天两槽照建（回拨时刻不受裁剪）
	n := 0
	for _, d := range view {
		if d.MedID == old.ID {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("老药应全天 2 槽，得 %d", n)
	}

	// EnsureMedDayOnce 同日记账：先记账（当天已全量跑过），再建的老药不会被
	// Once 补排，全量入口才排
	if err := s.EnsureMedDayOnce(today); err != nil {
		t.Fatal(err)
	}
	late := &Medication{PersonID: p.ID, Name: "迟到药", Kind: "daily", Times: []string{"00:01"}}
	if err := s.CreateMedication(late); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE medications SET created_at = 0, updated_at = 0 WHERE id = ?`, late.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureMedDayOnce(today); err != nil {
		t.Fatal(err)
	}
	view, err = s.MedDosesView(today, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range view {
		if d.MedID == late.ID {
			t.Fatal("同日已记账，Once 不应再补排")
		}
	}
	if err := s.EnsureMedDay(today); err != nil {
		t.Fatal(err)
	}
	view, err = s.MedDosesView(today, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range view {
		if d.MedID == late.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("全量入口应补排")
	}
}
