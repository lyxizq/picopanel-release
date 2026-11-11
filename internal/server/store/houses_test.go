package store

import (
	"testing"
	"time"
)

// TestHousesCRUDAndStats 房屋管理三表（2026-11-03）：房屋/家具电器/租约 CRUD、
// 在租租约同房排他、总览统计现算（出租中/月总租金/年化）、退租回落手工状态、
// 删房级联与在租保护、到期调度取数与打标。
func TestHousesCRUDAndStats(t *testing.T) {
	s := openTestStore(t)

	// ---- 房屋 CRUD：名必填、状态规整 ----
	h := &House{Name: " 望江府 1201 ", Status: "bogus", WaterCard: " W-001 "}
	if err := s.CreateHouse(h); err != nil {
		t.Fatalf("建房失败: %v", err)
	}
	if h.ID == 0 || h.Name != "望江府 1201" {
		t.Fatalf("建房回填异常: %+v", h)
	}
	if h.Status != "vacant" {
		t.Fatalf("非法状态应规整为 vacant: %q", h.Status)
	}
	h.Status = "selfuse"
	h.ElectricCard = "E-001"
	if err := s.UpdateHouse(h); err != nil {
		t.Fatalf("编辑房屋失败: %v", err)
	}
	if got, _ := s.GetHouse(h.ID); got.Status != "selfuse" || got.ElectricCard != "E-001" {
		t.Fatalf("编辑未生效: %+v", got)
	}
	if err := s.CreateHouse(&House{Name: ""}); err == nil {
		t.Fatal("空名称应拒绝")
	}

	// ---- 家具电器：归属校验、数量夹取、状态规整 ----
	it := &HouseItem{HouseID: h.ID, Category: "appliance", Name: "空调", BrandModel: "格力 KFR-35", Qty: 0}
	if err := s.CreateHouseItem(it); err != nil {
		t.Fatalf("建家具失败: %v", err)
	}
	if it.Qty != 1 {
		t.Fatalf("数量 0 应夹到 1: %d", it.Qty)
	}
	if err := s.CreateHouseItem(&HouseItem{HouseID: 9999, Name: "孤儿"}); err == nil {
		t.Fatal("不存在房屋的家具应拒绝")
	}
	it2 := &HouseItem{HouseID: h.ID, Category: "appliance", Name: "洗衣机", Status: "weird"}
	if err := s.CreateHouseItem(it2); err != nil {
		t.Fatalf("建家具失败: %v", err)
	}
	if it2.Status != "active" {
		t.Fatalf("非法条目状态应规整为 active: %q", it2.Status)
	}
	items, _ := s.ListHouseItems(h.ID)
	if len(items) != 2 {
		t.Fatalf("应查到 2 条家具: %d", len(items))
	}
	it.Status = "removed"
	if err := s.UpdateHouseItem(it); err != nil {
		t.Fatalf("编辑家具失败: %v", err)
	}

	// ---- 租约：同房在租排他 + 统计现算 ----
	l := &Lease{HouseID: h.ID, TenantName: "张三", StartDate: "2026-01-01", EndDate: "2026-12-31", MonthlyRent: 3500, Deposit: 7000}
	if err := s.CreateLease(l); err != nil {
		t.Fatalf("建租约失败: %v", err)
	}
	if l.Status != "active" {
		t.Fatalf("新建租约应为在租: %q", l.Status)
	}
	if err := s.CreateLease(&Lease{HouseID: h.ID, TenantName: "李四", StartDate: "2026-02-01", EndDate: "2027-01-31"}); err == nil {
		t.Fatal("同房第二条在租租约应拒绝")
	}
	if err := s.CreateLease(&Lease{HouseID: h.ID, TenantName: "李四", StartDate: "2027-01-01", EndDate: "2026-01-01"}); err == nil {
		t.Fatal("开始晚于结束应拒绝")
	}
	if err := s.CreateLease(&Lease{HouseID: h.ID, TenantName: "李四", StartDate: "2026-01-01", EndDate: "2026-01-01", MonthlyRent: -1}); err == nil {
		t.Fatal("负租金应拒绝")
	}

	list, stats, err := s.ListHouses()
	if err != nil {
		t.Fatalf("总览查询失败: %v", err)
	}
	if stats.Total != 1 || stats.Rented != 1 || stats.SelfUse != 0 || stats.Vacant != 0 {
		t.Fatalf("统计口径异常: %+v", stats)
	}
	if stats.MonthRent != 3500 || stats.YearRent != 42000 {
		t.Fatalf("租金统计异常: %+v", stats)
	}
	if len(list) != 1 || list[0].DisplayStatus != "rented" || list[0].ActiveLease == nil {
		t.Fatalf("房屋联查在租租约异常: %+v", list[0])
	}
	if list[0].ActiveLease.TenantName != "张三" {
		t.Fatalf("在租租约联查错误: %+v", list[0].ActiveLease)
	}
	if list[0].ItemsCount != 1 {
		t.Fatalf("items_count 应排除已移除（1 条）: %d", list[0].ItemsCount)
	}

	// ---- 退租：出统计、回落手工状态；恢复在租走排他 ----
	if err := s.SetLeaseStatus(l.ID, "ended"); err != nil {
		t.Fatalf("退租失败: %v", err)
	}
	if _, stats, _ := s.ListHouses(); stats.Rented != 0 || stats.MonthRent != 0 || stats.SelfUse != 1 {
		t.Fatalf("退租后统计应回落手工状态: %+v", stats)
	}
	// 退租后允许新的在租；到期日落 30 天窗口内，供调度取数断言
	l2 := &Lease{HouseID: h.ID, TenantName: "李四", StartDate: "2026-01-01",
		EndDate: time.Now().AddDate(0, 0, 10).Format("2006-01-02"), MonthlyRent: 3600}
	if err := s.CreateLease(l2); err != nil {
		t.Fatalf("退租后新建在租应允许: %v", err)
	}
	if err := s.SetLeaseStatus(l.ID, "active"); err == nil {
		t.Fatal("已有在租时恢复另一条应拒绝")
	}

	// ---- 到期调度取数与打标 ----
	due, err := s.DueExpiringLeases()
	if err != nil {
		t.Fatalf("调度取数失败: %v", err)
	}
	if len(due) != 1 || due[0].TenantName != "李四" || due[0].HouseName != "望江府 1201" {
		t.Fatalf("调度取数应含在租租约并带房屋名: %+v", due)
	}
	if err := s.MarkLeaseNotified(l2.ID, 12345); err != nil {
		t.Fatalf("打标失败: %v", err)
	}
	if due, _ = s.DueExpiringLeases(); len(due) != 0 {
		t.Fatalf("打标后不应再取到: %+v", due)
	}
	// 改到期日重置标记（仍在窗口内）
	l2.EndDate = time.Now().AddDate(0, 0, 20).Format("2006-01-02")
	if err := s.UpdateLease(l2); err != nil {
		t.Fatalf("改期失败: %v", err)
	}
	if due, _ = s.DueExpiringLeases(); len(due) != 1 {
		t.Fatal("改到期日应重置提醒标记")
	}

	// ---- 删房：在租保护 + 级联 ----
	if err := s.DeleteHouse(h.ID); err == nil {
		t.Fatal("有在租租约删房应拒绝")
	}
	if err := s.SetLeaseStatus(l2.ID, "ended"); err != nil {
		t.Fatalf("退租失败: %v", err)
	}
	if err := s.DeleteHouse(h.ID); err != nil {
		t.Fatalf("删房失败: %v", err)
	}
	if items, _ := s.ListHouseItems(h.ID); len(items) != 0 {
		t.Fatal("删房应级联清家具电器")
	}
	if ls, _ := s.ListLeases(h.ID); len(ls) != 0 {
		t.Fatal("删房应级联清租约")
	}
	if hs, _, _ := s.ListHouses(); len(hs) != 0 {
		t.Fatal("删房后列表应为空")
	}
}

// TestHouseRentReminder 收租提醒（2026-11-04）：字段校验规整、调度取数（仅
// 有在租租约的房屋）、期次计算（月末收敛/上月回退）、三段打标读写与改收租
// 安排重置。
func TestHouseRentReminder(t *testing.T) {
	s := openTestStore(t)

	// 字段规整：日越界归零、坏时刻回落 10:00
	h := &House{Name: "收租房", RentDueDay: 99, RentDueTime: "25:99"}
	if err := s.CreateHouse(h); err != nil {
		t.Fatalf("建房失败: %v", err)
	}
	if h.RentDueDay != 0 || h.RentDueTime != "10:00" {
		t.Fatalf("越界字段应规整: day=%d time=%q", h.RentDueDay, h.RentDueTime)
	}
	// 0=不提醒：不应进调度取数
	h.RentDueDay = 0
	if err := s.UpdateHouse(h); err != nil {
		t.Fatalf("编辑失败: %v", err)
	}
	if due, _ := s.RentDueHouses(); len(due) != 0 {
		t.Fatal("无在租租约或未开提醒都不应进调度取数")
	}

	// 开提醒（31 日，验证月末收敛）+ 在租租约 → 进取数
	h.RentDueDay = 31
	h.RentDueTime = "09:30"
	if err := s.UpdateHouse(h); err != nil {
		t.Fatalf("编辑失败: %v", err)
	}
	if err := s.CreateLease(&Lease{HouseID: h.ID, TenantName: "张三", StartDate: "2026-01-01", EndDate: "2030-12-31", MonthlyRent: 3500}); err != nil {
		t.Fatalf("建租约失败: %v", err)
	}
	due, err := s.RentDueHouses()
	if err != nil || len(due) != 1 || due[0].Tenant != "张三" || due[0].MonthRent != 3500 || due[0].DueDay != 31 {
		t.Fatalf("调度取数异常: %+v err=%v", due, err)
	}

	// 期次计算（固定日期避免依赖真实日历）：
	// ① 月末收敛：11 月只有 30 天，31 日 → 11-30 09:30；now=11-30 09:00 时落在
	//    now+1h 窗内 → 取当月期次
	now := time.Date(2026, 11, 30, 9, 0, 0, 0, time.Local)
	occ, month := HouseRentOccurrence(31, "09:30", now)
	if want := time.Date(2026, 11, 30, 9, 30, 0, 0, time.Local); !occ.Equal(want) || month != "2026-11" {
		t.Fatalf("月末收敛异常: occ=%v month=%q want=%v", occ, month, want)
	}
	// ② 当月期次未到（超出 now+1h）→ 回退上月期次（10 月有 31 日）
	now = time.Date(2026, 11, 20, 10, 0, 0, 0, time.Local)
	occ, month = HouseRentOccurrence(31, "09:30", now)
	if want := time.Date(2026, 10, 31, 9, 30, 0, 0, time.Local); !occ.Equal(want) || month != "2026-10" {
		t.Fatalf("上月回退异常: occ=%v month=%q want=%v", occ, month, want)
	}
	// ③ 当月期次落在 now+1h 内 → 取当月
	occ, month = HouseRentOccurrence(20, "10:30", now)
	if want := time.Date(2026, 11, 20, 10, 30, 0, 0, time.Local); !occ.Equal(want) || month != "2026-11" {
		t.Fatalf("当月期次异常: occ=%v month=%q want=%v", occ, month, want)
	}
	// ④ 上月回退的月末溢出（2026-11-06 审计批）：3 月 30/31 日收租的「上月」
	//    应是 2 月收敛日；旧 AddDate(0,-1,0) 从 3/30 归一化下溢到 3/2，取月
	//    又回 3 月，会算出 3/31 的未来时刻当"上月期次"
	now = time.Date(2026, 3, 30, 12, 0, 0, 0, time.Local)
	occ, month = HouseRentOccurrence(31, "10:00", now)
	if want := time.Date(2026, 2, 28, 10, 0, 0, 0, time.Local); !occ.Equal(want) || month != "2026-02" {
		t.Fatalf("月末溢出的上月期次异常: occ=%v month=%q want=%v", occ, month, want)
	}

	// 三段打标：换月重置 + 分段打标互不影响
	if err := s.ResetHouseRentFlags(h.ID, "2026-01"); err != nil {
		t.Fatalf("重置失败: %v", err)
	}
	row, _ := s.RentDueHouses()
	if row[0].Month != "2026-01" || row[0].N1h != nil || row[0].NDue != nil || row[0].NNextday != nil {
		t.Fatalf("重置后应为空打标: %+v", row[0])
	}
	if err := s.MarkHouseRentNotified(h.ID, "2026-01", "due", 123); err != nil {
		t.Fatalf("打标失败: %v", err)
	}
	if err := s.MarkHouseRentNotified(h.ID, "2026-01", "bogus", 1); err == nil {
		t.Fatal("非法段应拒绝")
	}
	row, _ = s.RentDueHouses()
	if row[0].NDue == nil || *row[0].NDue != 123 || row[0].N1h != nil {
		t.Fatalf("分段打标异常: %+v", row[0])
	}
	// 改收租安排 → 清打标重新武装
	h.RentDueTime = "11:00"
	if err := s.UpdateHouse(h); err != nil {
		t.Fatalf("编辑失败: %v", err)
	}
	row, _ = s.RentDueHouses()
	if row[0].NDue != nil || row[0].Month != "" {
		t.Fatalf("改收租安排应清打标: %+v", row[0])
	}
}

// TestHouseExpensesAndIncome 消耗费用清单与收支统计（2026-11-04）：费用 CRUD
// 校验、总收入按租期覆盖天数折算（整年/月中起租/含已退租历史租约/期间外排除）、
// 消耗费用按期间合计、实际收入=总收入−消耗费用。
func TestHouseExpensesAndIncome(t *testing.T) {
	s := openTestStore(t)

	h := &House{Name: "收支房"}
	if err := s.CreateHouse(h); err != nil {
		t.Fatal(err)
	}
	h2 := &House{Name: "短租房"}
	if err := s.CreateHouse(h2); err != nil {
		t.Fatal(err)
	}
	// 全年整租：2026 全年 3500/月 → 年总 42000，Q1 10500
	if err := s.CreateLease(&Lease{HouseID: h.ID, TenantName: "全年", StartDate: "2026-01-01", EndDate: "2026-12-31", MonthlyRent: 3500}); err != nil {
		t.Fatal(err)
	}
	// 已退租历史租约（1/15~2/28，3000/月）也应计入应收：Jan 17/31×3000 + Feb 3000 = 4645.16
	//（在租排他按房计，历史租约放第二套房，退租留档）
	if err := s.CreateLease(&Lease{HouseID: h2.ID, TenantName: "短租", StartDate: "2026-01-15", EndDate: "2026-02-28", MonthlyRent: 3000}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLeaseStatus(2, "ended"); err != nil {
		t.Fatal(err)
	}
	// 期间外租约不应计入（退租后的同一套房可再签）
	if err := s.CreateLease(&Lease{HouseID: h2.ID, TenantName: "明年", StartDate: "2027-01-01", EndDate: "2027-12-31", MonthlyRent: 9999}); err != nil {
		t.Fatal(err)
	}

	// 费用：期内两笔 + 期外一笔
	for _, e := range []HouseExpense{
		{HouseID: h.ID, Name: "桌椅置换", Amount: 2000, SpendDate: "2026-03-05"},
		{HouseID: h.ID, Name: "取暖费", Amount: 1500.5, SpendDate: "2026-11-10"},
		{HouseID: h.ID, Name: "期外", Amount: 999, SpendDate: "2025-12-31"},
	} {
		ee := e
		if err := s.CreateHouseExpense(&ee); err != nil {
			t.Fatalf("建费用失败: %v", err)
		}
	}
	if err := s.CreateHouseExpense(&HouseExpense{HouseID: 9999, Name: "孤儿", SpendDate: "2026-01-01"}); err == nil {
		t.Fatal("不存在房屋的费用应拒绝")
	}
	if err := s.CreateHouseExpense(&HouseExpense{HouseID: h.ID, Name: "", SpendDate: "2026-01-01"}); err == nil {
		t.Fatal("空名称应拒绝")
	}
	if err := s.CreateHouseExpense(&HouseExpense{HouseID: h.ID, Name: "坏日期", SpendDate: "2026/01/01"}); err == nil {
		t.Fatal("坏日期应拒绝")
	}
	if err := s.CreateHouseExpense(&HouseExpense{HouseID: h.ID, Name: "负数", Amount: -5, SpendDate: "2026-01-01"}); err != nil {
		t.Fatalf("负金额应规整为 0 而非拒绝: %v", err)
	}
	if es, _ := s.ListHouseExpenses(h.ID); len(es) != 4 || es[0].SpendDate != "2026-11-10" {
		t.Fatalf("费用清单应日期倒序共 4 条: %+v", es)
	}

	// 年度收支：总 42000+4645.16=46645.16，费 3500.5，净 43144.66
	inc, err := s.HouseIncomeRange("2026-01-01", "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if inc.Gross != 46645.16 || inc.Expense != 3500.5 || inc.Net != 43144.66 {
		t.Fatalf("年度收支口径异常: %+v", inc)
	}
	// 季度：Q1 租金 10500+4645.16=15145.16、费 2000、净 13145.16；Q4 费 1500.5
	q1, err := s.HouseIncomeRange("2026-01-01", "2026-03-31")
	if err != nil {
		t.Fatal(err)
	}
	if q1.Gross != 15145.16 || q1.Net != 13145.16 {
		t.Fatalf("Q1 收支异常: %+v", q1)
	}
	q4, _ := s.HouseIncomeRange("2026-10-01", "2026-12-31")
	if q4.Gross != 10500 || q4.Expense != 1500.5 || q4.Net != 8999.5 {
		t.Fatalf("Q4 收支异常: %+v", q4)
	}

	// 费用编辑/删除
	up := &HouseExpense{ID: 1, Name: "桌椅置换（含运费）", Amount: 2200, SpendDate: "2026-03-06"}
	if err := s.UpdateHouseExpense(up); err != nil {
		t.Fatalf("编辑费用失败: %v", err)
	}
	if err := s.DeleteHouseExpense(2); err != nil {
		t.Fatalf("删费用失败: %v", err)
	}
	inc, _ = s.HouseIncomeRange("2026-01-01", "2026-12-31")
	if inc.Gross != 46645.16 || inc.Expense != 2200 || inc.Net != 44445.16 {
		t.Fatalf("费用变更后收支未联动: %+v", inc)
	}
}

// TestLeaseRenewAndFields 租约续租与新字段（2026-11-05）：出租方三项/水电气
// 读数随租约存储校验；续租 = 旧约退租留档 + 复制生成新约（起始=旧截止次日、
// 默认一年、押金/租客/出租方/读数沿用），单事务原子。
func TestLeaseRenewAndFields(t *testing.T) {
	s := openTestStore(t)
	h := &House{Name: "续租房"}
	if err := s.CreateHouse(h); err != nil {
		t.Fatal(err)
	}
	l := &Lease{HouseID: h.ID, TenantName: "张三", TenantPhone: "138", OwnerName: "李房东", OwnerPhone: "139",
		WaterReading: 12.5, ElectricReading: 100, GasReading: 50,
		StartDate: "2026-01-01", EndDate: "2026-12-31", MonthlyRent: 3500, Deposit: 7000, PaymentCycle: "押一付三"}
	if err := s.CreateLease(l); err != nil {
		t.Fatalf("建租约失败: %v", err)
	}
	got, _ := s.GetLease(l.ID)
	if got.OwnerName != "李房东" || got.WaterReading != 12.5 || got.ElectricReading != 100 || got.GasReading != 50 {
		t.Fatalf("新字段未落库: %+v", got)
	}
	if err := s.CreateLease(&Lease{HouseID: h.ID, TenantName: "李四", StartDate: "2026-01-01", EndDate: "2027-01-01", WaterReading: -1}); err == nil {
		t.Fatal("负读数应拒绝")
	}

	// 续租：旧约退租、新约复制并顺延
	nl, err := s.RenewLease(l.ID)
	if err != nil {
		t.Fatalf("续租失败: %v", err)
	}
	if nl.ID == l.ID || nl.Status != "active" {
		t.Fatalf("应生成新在租租约: %+v", nl)
	}
	if nl.StartDate != "2027-01-01" || nl.EndDate != "2028-01-01" {
		t.Fatalf("新租期应=旧截止次日+一年: %s ~ %s", nl.StartDate, nl.EndDate)
	}
	if nl.TenantName != "张三" || nl.OwnerName != "李房东" || nl.Deposit != 7000 || nl.WaterReading != 12.5 {
		t.Fatalf("续租应复制租客/出租方/押金/读数: %+v", nl)
	}
	old, _ := s.GetLease(l.ID)
	if old.Status != "ended" {
		t.Fatalf("旧约应退租留档: %+v", old)
	}
	// 已退租的不可再续
	if _, err := s.RenewLease(l.ID); err == nil {
		t.Fatal("退租租约续租应拒绝")
	}
	// 退租后的新约可正常退租再续（排他不误伤）
	if err := s.SetLeaseStatus(nl.ID, "ended"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenewLease(nl.ID); err == nil {
		t.Fatal("已退租租约续租应拒绝")
	}
}
