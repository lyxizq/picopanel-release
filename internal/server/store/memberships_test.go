package store

import (
	"path/filepath"
	"testing"
	"time"
)

// 会员到期（492）口径钉死：业务必填/日期格式/周期越界回落 365；剩余天数按本地
// 自然日（负 = 已过期天数）、列表按到期日升序；改到期日重置 15 天提醒标记
// （未改沿用）；统计与调度同窗（今天+15，含已过期未删）；发过打标退出调度。
func TestMemberships(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 校验与规整
	if err := s.CreateMembership(&Membership{Name: "x", ExpireDate: "2030-01-01"}); err == nil {
		t.Fatal("缺业务应报错")
	}
	if err := s.CreateMembership(&Membership{Biz: "a", ExpireDate: "2030/01/01"}); err == nil {
		t.Fatal("坏日期格式应报错")
	}
	m := &Membership{Biz: " Netflix ", Name: " 年费 ", ExpireDate: "2030-01-01", PeriodDays: 99999}
	if err := s.CreateMembership(m); err != nil {
		t.Fatal(err)
	}
	if m.Biz != "Netflix" || m.Name != "年费" || m.PeriodDays != 365 {
		t.Fatalf("规整口径错: %+v", m)
	}
	if m.ID == 0 || m.CreatedAt == 0 {
		t.Fatalf("创建应回填 ID/时间: %+v", m)
	}

	// 剩余天数与排序：过期(-3) → 10 天 → 200 天 → 2030 固定日
	past := &Membership{Biz: "b", ExpireDate: time.Now().AddDate(0, 0, -3).Format("2006-01-02")}
	soon := &Membership{Biz: "c", ExpireDate: time.Now().AddDate(0, 0, 10).Format("2006-01-02")}
	far := &Membership{Biz: "d", ExpireDate: time.Now().AddDate(0, 0, 200).Format("2006-01-02")}
	for _, x := range []*Membership{past, soon, far} {
		if err := s.CreateMembership(x); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListMemberships()
	if err != nil {
		t.Fatal(err)
	}
	// 列表序 = sort 优先（新库行全部走新建追加 = 创建序；到期日序只来自存量回填）
	if len(list) != 4 || list[0].Biz != "Netflix" || list[1].Biz != "b" || list[2].Biz != "c" || list[3].Biz != "d" {
		t.Fatalf("列表应为创建序追加: %+v", list)
	}
	if !list[1].Expired || list[1].RemainingDays != -3 {
		t.Fatalf("过期剩余天数应为 -3: %+v", list[1])
	}
	if list[2].Expired || list[2].RemainingDays != 10 {
		t.Fatalf("未来剩余天数应为 10: %+v", list[2])
	}
	if list[0].Expired {
		t.Fatalf("2030 不应判过期: %+v", list[0])
	}

	// 统计：total=4；expiring = 到期 ≤ 今天+15（含过期）= b + c = 2
	total, expiring, err := s.MembershipStats()
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || expiring != 2 {
		t.Fatalf("统计应为 total 4 expiring 2，得 %d/%d", total, expiring)
	}

	// 调度取数：进窗（≤ 今天+15）且未打标 = b + c；打标后退出
	due, err := s.DueExpiringMemberships()
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 || due[0].Biz != "b" || due[1].Biz != "c" {
		t.Fatalf("调度进窗应为 b/c: %+v", due)
	}
	if err := s.MarkMembershipNotified(soon.ID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	due, err = s.DueExpiringMemberships()
	if err != nil || len(due) != 1 || due[0].Biz != "b" {
		t.Fatalf("打标后调度应只剩 b: %+v, %v", due, err)
	}

	// 编辑：改到期日重置提醒标记；沿用日期保留标记
	soon.Biz = "c2"
	if err := s.UpdateMembership(soon); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMembership(soon.ID)
	if err != nil || got.Notify15dAt == nil {
		t.Fatalf("未改日期应保留标记: %+v, %v", got, err)
	}
	got.ExpireDate = time.Now().AddDate(0, 0, 5).Format("2006-01-02")
	if err := s.UpdateMembership(got); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetMembership(soon.ID)
	if err != nil || got.Notify15dAt != nil {
		t.Fatalf("改日期应重置标记: %+v, %v", got, err)
	}
	due, err = s.DueExpiringMemberships()
	if err != nil || len(due) != 2 {
		t.Fatalf("重置后 b/c2 都应进窗: %+v, %v", due, err)
	}

	// 开始时间（492 返工）：进度条按起止区间算剩余占比；留空回落剩余/周期；
	// 开始晚于到期报错
	if err := s.CreateMembership(&Membership{Biz: "e", StartDate: time.Now().AddDate(0, 0, 1).Format("2006-01-02"), ExpireDate: time.Now().AddDate(0, 0, -1).Format("2006-01-02")}); err == nil {
		t.Fatal("开始晚于到期应报错")
	}
	span := &Membership{Biz: "f", StartDate: time.Now().AddDate(0, 0, -90).Format("2006-01-02"), ExpireDate: time.Now().AddDate(0, 0, 90).Format("2006-01-02"), PeriodDays: 0}
	if err := s.CreateMembership(span); err != nil {
		t.Fatal(err)
	}
	if span.PeriodDays != 365 {
		t.Fatalf("周期缺省应回落 365: %+v", span)
	}
	// 区间 180 天、剩 90 天 → 50%；total=0 的退化区间（开始=到期）→ 0
	// （Create 不回填现算字段，断言前回读）
	span, err = s.GetMembership(span.ID)
	if err != nil {
		t.Fatal(err)
	}
	if span.Progress != 50 {
		t.Fatalf("区间口径 Progress 应为 50: %+v", span)
	}
	zero := &Membership{Biz: "g", StartDate: time.Now().Format("2006-01-02"), ExpireDate: time.Now().Format("2006-01-02")}
	if err := s.CreateMembership(zero); err != nil {
		t.Fatal(err)
	}
	if zero, err = s.GetMembership(zero.ID); err != nil {
		t.Fatal(err)
	}
	if zero.Progress != 0 {
		t.Fatalf("零区间 Progress 应为 0: %+v", zero)
	}
	fall := &Membership{Biz: "h", ExpireDate: time.Now().AddDate(0, 0, 10).Format("2006-01-02")}
	if err := s.CreateMembership(fall); err != nil {
		t.Fatal(err)
	}
	fall, err = s.GetMembership(fall.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := 10 * 100 / 365; fall.Progress != want {
		t.Fatalf("回落口径 Progress 应为 %d（剩 10/365）: %+v", want, fall.Progress)
	}

	// 删除 + 不存在报错
	if err := s.DeleteMembership(far.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMembership(far.ID); err == nil {
		t.Fatal("重复删除应报不存在")
	}
	// 7 条（e 校验失败未入库）删 far 后剩 6
	if total, _, err := s.MembershipStats(); err != nil || total != 6 {
		t.Fatalf("删除后 total 应为 6: %d, %v", total, err)
	}

	// 拖拽排序：整表重排写 1..n（前端恒传全量 ids，同磁贴口径）；新建追加尾部
	if err := s.ReorderMemberships([]int64{past.ID, m.ID, soon.ID, span.ID, zero.ID, fall.ID}); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListMemberships()
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"b", "Netflix", "c2", "f", "g", "h"}
	for i, w := range wantOrder {
		if list[i].Biz != w {
			t.Fatalf("重排后顺序不符（第 %d 应为 %s）: %+v", i, w, list)
		}
	}
	last := &Membership{Biz: "i", ExpireDate: time.Now().AddDate(0, 0, 1).Format("2006-01-02")}
	if err := s.CreateMembership(last); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListMemberships()
	if err != nil || list[len(list)-1].Biz != "i" {
		t.Fatalf("新建应追加尾部: %+v, %v", list, err)
	}

	// 回填（幂等）：全部置 0 后按到期日+id 序重编，重复执行不漂移
	if _, err := s.db.Exec(`UPDATE memberships SET sort = 0`); err != nil {
		t.Fatal(err)
	}
	if err := BackfillMembershipSort(s.db); err != nil {
		t.Fatal(err)
	}
	if err := BackfillMembershipSort(s.db); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListMemberships()
	if err != nil {
		t.Fatal(err)
	}
	// 到期日序：b(-3) → g(今天) → i(+1) → c2(+5) → h(+10) → f(+90) → Netflix(2030)
	wantBackfill := []string{"b", "g", "i", "c2", "h", "f", "Netflix"}
	for i, w := range wantBackfill {
		if list[i].Biz != w {
			t.Fatalf("回填后到期日序不符（第 %d 应为 %s）: %+v", i, w, list)
		}
	}
}
