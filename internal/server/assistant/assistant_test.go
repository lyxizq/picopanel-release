package assistant

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/weather"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.OpenWithKey(filepath.Join(dir, "a.db"), filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLunarOf(t *testing.T) {
	cny := LunarOf(date(2026, 2, 17)) // 2026 春节
	if cny.Month != "正月" || cny.Day != "初一" || !contains(cny.Festivals, "春节") || cny.ShengXiao != "马" {
		t.Fatalf("2026-02-17 农历不符: %+v", cny)
	}
	if lu := LunarOf(date(2025, 1, 29)); lu.Month != "正月" || lu.Day != "初一" {
		t.Fatalf("2025-01-29 应为正月初一: %+v", lu)
	}
	if lu := LunarOf(date(2026, 4, 5)); lu.JieQi != "清明" {
		t.Fatalf("2026-04-05 应为清明: %+v", lu)
	}
	if lu := LunarOf(date(2026, 9, 25)); lu.Month != "八月" || lu.Day != "十五" || !contains(lu.Festivals, "中秋节") {
		t.Fatalf("2026-09-25 应为八月十五中秋: %+v", lu)
	}
	if lu := LunarOf(date(2026, 1, 1)); !contains(lu.Festivals, "元旦节") && !contains(lu.Festivals, "元旦") {
		t.Fatalf("公历节日元旦缺失: %+v", lu)
	}
}

func TestLunarYearly(t *testing.T) {
	m, d := LunarMonthDay(date(2025, 10, 6)) // 2025 中秋
	if m != 8 || d != 15 {
		t.Fatalf("2025-10-06 应为八月十五，得 %d/%d", m, d)
	}
	next, ok := NextLunarYearly(8, 15, date(2026, 1, 1))
	if !ok || ymd(next) != "2026-09-25" {
		t.Fatalf("下一个八月十五应为 2026-09-25，得 %v %v", next, ok)
	}
	// 当天即算
	next, _ = NextLunarYearly(8, 15, date(2026, 9, 25))
	if ymd(next) != "2026-09-25" {
		t.Fatalf("当天应命中当天，得 %v", next)
	}
	all := LunarYearlyBetween(8, 15, date(2025, 1, 1), date(2026, 12, 31))
	if len(all) != 2 || ymd(all[0]) != "2025-10-06" || ymd(all[1]) != "2026-09-25" {
		t.Fatalf("区间内中秋应两次: %v", all)
	}
	if LunarLabel(8, 15) != "农历八月十五" || LunarLabel(1, 1) != "农历正月初一" || LunarLabel(12, 30) != "农历腊月三十" {
		t.Fatalf("农历标签不符: %q %q %q", LunarLabel(8, 15), LunarLabel(1, 1), LunarLabel(12, 30))
	}
}

func TestHolidays(t *testing.T) {
	if m, ok := HolidayOf("2026-10-01"); !ok || !m.Off || m.Index != 1 || m.Total != 7 || m.Name != "国庆节" {
		t.Fatalf("2026-10-01 标记不符: %+v %v", m, ok)
	}
	if m, ok := HolidayOf("2026-09-20"); !ok || m.Off || m.Name != "国庆节" {
		t.Fatalf("2026-09-20 应为国庆调休上班: %+v %v", m, ok)
	}
	if m, ok := HolidayOf("2026-02-28"); !ok || m.Off {
		t.Fatalf("2026-02-28 应为春节调休上班: %+v %v", m, ok)
	}
	if m, ok := HolidayOf("2025-02-08"); !ok || m.Off {
		t.Fatalf("2025-02-08 应为春节调休上班: %+v %v", m, ok)
	}
	if _, ok := HolidayOf("2026-10-20"); ok {
		t.Fatal("普通日不应有标记")
	}
	if !HolidayKnownYear(2026) || HolidayKnownYear(2031) {
		t.Fatal("年份内置判定错")
	}
	if ys := HolidayKnownYears(); len(ys) < 2 || ys[0] != 2025 || ys[1] != 2026 {
		t.Fatalf("已内置年份列表不符: %v", ys)
	}
	cases := map[string]string{
		"2026-10-03": "国庆节假期第 3 天（共 7 天）",
		"2026-09-20": "今天调休上班（国庆节）",
		"2026-09-19": "明天调休上班（国庆节）",
		"2026-09-30": "明天起国庆节放假 7 天",
		"2026-08-01": "距中秋节还有 55 天（9月25日起放假 3 天）",
		"2026-12-01": "", // 2027 未内置
	}
	for ds, want := range cases {
		d, _ := time.ParseInLocation("2006-01-02", ds, sh)
		if got := HolidayLine(d); got != want {
			t.Fatalf("%s: 得 %q 期望 %q", ds, got, want)
		}
	}
}

func TestCountdownEnrich(t *testing.T) {
	today := date(2026, 1, 1)
	cs := []store.Countdown{
		{ID: 1, Name: "闰日", Kind: "yearly", Date: "2000-02-29"},
		{ID: 2, Name: "中秋", Kind: "yearly", Lunar: true, Date: "2025-10-06"},
		{ID: 3, Name: "考试", Kind: "once", Date: "2026-01-11"},
		{ID: 4, Name: "在一起", Kind: "since", Date: "2025-12-25"},
		{ID: 5, Name: "过期", Kind: "once", Date: "2025-12-30"},
	}
	for i := range cs {
		if err := NormalizeCountdown(&cs[i], sh); err != nil {
			t.Fatal(err)
		}
	}
	if cs[1].LunarMonth != 8 || cs[1].LunarDay != 15 {
		t.Fatalf("农历月日未反推: %+v", cs[1])
	}
	Enrich(cs, today)
	want := map[int64][2]any{1: {"2026-02-28", 58}, 2: {"2026-09-25", 267}, 3: {"2026-01-11", 10}, 4: {"2025-12-25", 7}, 5: {"2025-12-30", -2}}
	for _, c := range cs {
		w := want[c.ID]
		if c.NextDate != w[0] || c.Days != w[1] {
			t.Fatalf("%s: next=%s days=%d，期望 %v", c.Name, c.NextDate, c.Days, w)
		}
	}
	if cs[1].LunarLabel != "农历八月十五" {
		t.Fatalf("农历标签: %q", cs[1].LunarLabel)
	}
	SortForCard(cs)
	var order []string
	for _, c := range cs {
		order = append(order, c.Name)
	}
	if strings.Join(order, ",") != "考试,闰日,中秋,在一起,过期" {
		t.Fatalf("卡片排序不符: %v", order)
	}
	occ := CountdownOccurrences(cs[1], date(2025, 1, 1), date(2026, 12, 31))
	if len(occ) != 2 {
		t.Fatalf("农历每年两年内应 2 次: %v", occ)
	}
	bad := store.Countdown{Name: "", Date: "2026-01-01"}
	if NormalizeCountdown(&bad, sh) == nil {
		t.Fatal("空名应报错")
	}
	bad = store.Countdown{Name: "x", Date: "2026/01/01"}
	if NormalizeCountdown(&bad, sh) == nil {
		t.Fatal("非法日期应报错")
	}
}

func TestFireDue(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 5, 0, 0, sh)
	if !fireDue(now, "08:00", "") || !fireDue(now, "8:00", "2026-10-25") {
		t.Fatal("到点且今日未推应触发")
	}
	if fireDue(now, "08:00", "2026-10-26") || fireDue(now, "08:10", "") || fireDue(now, "bad", "") {
		t.Fatal("已推/未到点/非法时间不应触发")
	}
	if _, _, ok := parseHHMM("24:00"); ok {
		t.Fatal("24:00 非法")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	st := newStore(t)
	def := LoadConfig(st)
	if !def.BriefOn || def.BriefTime != "08:00" || def.UmbrellaThreshold != 50 || def.AirThreshold != 150 || !def.WarnOn {
		t.Fatalf("默认配置不符: %+v", def)
	}
	c := Config{BriefOn: false, BriefTime: "7:30", WarnOn: true, UmbrellaOn: true, UmbrellaTime: "21:00", UmbrellaThreshold: 60, AirOn: false, AirThreshold: 200}
	if err := SaveConfig(st, c); err != nil {
		t.Fatal(err)
	}
	got := LoadConfig(st)
	if got.BriefOn || got.BriefTime != "07:30" || got.UmbrellaThreshold != 60 || got.AirOn || got.AirThreshold != 200 {
		t.Fatalf("回读不符: %+v", got)
	}
	if SaveConfig(st, Config{BriefTime: "x", UmbrellaTime: "20:00", UmbrellaThreshold: 50, AirThreshold: 150}) == nil {
		t.Fatal("非法时间应拒")
	}
}

func TestBuildBriefAndDeliver(t *testing.T) {
	st := newStore(t)
	today := time.Now()
	ts := today.Format("2006-01-02")
	if err := st.CreateReminder(&store.Reminder{Title: "交电费", DueDate: ts, DueTime: "10:00", Notify: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCountdown(&store.Countdown{Name: "纪念日", Kind: "once", Date: today.AddDate(0, 0, 3).Format("2006-01-02")}); err != nil {
		t.Fatal(err)
	}
	// 一台从未上报的服务器 → 巡检段判离线，标题带台数
	if _, err := st.CreateServer("机房A", "10.0.0.1:19100", "", 0); err != nil {
		t.Fatal(err)
	}
	title, body := BuildBrief(st, 90, func() map[int64]int { return map[int64]int{1: 4, 2: -1} }, today)
	if !strings.HasPrefix(title, "每日早报 ") || !strings.HasSuffix(title, " · 在线 0 台 / 离线 1 台") {
		t.Fatalf("标题: %q", title)
	}
	for _, want := range []string{"农历", "待办：今天 1 项——交电费", "邮件：未读 4 封", "倒数日：纪念日 还有 3 天", "服务器：共 1 台，在线 0 / 离线 1，离线：机房A"} {
		if !strings.Contains(body, want) {
			t.Fatalf("早报缺 %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "天气：") {
		t.Fatal("天气未开启不应有天气行")
	}
	if strings.Contains(body, "资源 TOP") {
		t.Fatal("无在线机器不应有资源 TOP 行")
	}
	BriefNow(st, 90, nil, today)
	page, err := st.ListMessages(MessageType, false, 1, 10)
	if err != nil || page.Total != 1 || page.Items[0].Type != MessageType {
		t.Fatalf("早报站内消息未落库: %+v %v", page, err)
	}
}

func TestBuildBriefNoServers(t *testing.T) {
	st := newStore(t)
	title, body := BuildBrief(st, 90, nil, time.Now())
	if strings.Contains(title, "在线") || strings.Contains(body, "服务器：") {
		t.Fatalf("无服务器时不应出巡检段: %q\n%s", title, body)
	}
}

// 资源 TOP 一台一行带排名（2026-10-26 用户指定），按 CPU 降序。
func TestBuildBriefResourceTopLines(t *testing.T) {
	st := newStore(t)
	now := time.Now()
	for _, m := range []struct {
		name string
		cpu  float64
	}{{"主服务器", 2}, {"游戏服", 16}} {
		sv, err := st.CreateServer(m.name, "10.0.0.1:19100", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertOnReport(sv.ID, nil, now.Unix()); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertMetric(sv.ID, now.Unix(), &proto.Report{CPU: m.cpu, MemPercent: 63, Disks: []proto.DiskInfo{{Mount: "/", Percent: 38}}}); err != nil {
			t.Fatal(err)
		}
	}
	_, body := BuildBrief(st, 90, nil, now)
	want := "资源 TOP：\n1. 游戏服 CPU 16% / 内存 63% / 磁盘 38%\n2. 主服务器 CPU 2% / 内存 63% / 磁盘 38%"
	if !strings.Contains(body, want) {
		t.Fatalf("资源 TOP 应标题行后一台一行按 CPU 降序:\n%s", body)
	}
}

func TestCheckWarningsDedupe(t *testing.T) {
	st := newStore(t)
	now := time.Now()
	r := &weather.Resp{City: "北京", Warnings: []weather.Warning{{ID: "w1", Title: "大风蓝色预警", Level: "蓝色", Type: "大风", Text: "注意防风"}}}
	checkWarnings(st, r, now)
	checkWarnings(st, r, now.Add(time.Minute)) // 同一预警不重推
	page, _ := st.ListMessages(MessageType, false, 1, 10)
	if page.Total != 1 || !strings.Contains(page.Items[0].Title, "大风蓝色预警") {
		t.Fatalf("应只推一次: %+v", page)
	}
	r.Warnings = append(r.Warnings, weather.Warning{Title: "暴雨黄色预警", Level: "黄色"}) // 无 id → 内容哈希
	checkWarnings(st, r, now.Add(2*time.Minute))
	checkWarnings(st, r, now.Add(3*time.Minute))
	page, _ = st.ListMessages(MessageType, false, 1, 10)
	if page.Total != 2 {
		t.Fatalf("新预警应再推一次且只一次: %d", page.Total)
	}
	// 空气：阈值 150，AQI 160 当日一次；升到 220（重度）再推；再查不推
	cfg := LoadConfig(st)
	air := &weather.Resp{City: "北京", Air: &weather.Air{Aqi: 160, Category: "中度污染", Primary: "PM2.5"}}
	checkAir(st, cfg, air, now)
	checkAir(st, cfg, air, now)
	air.Air.Aqi, air.Air.Category = 220, "重度污染"
	checkAir(st, cfg, air, now)
	checkAir(st, cfg, air, now)
	page, _ = st.ListMessages(MessageType, false, 1, 10)
	if page.Total != 4 {
		t.Fatalf("空气提醒应共推 2 次（合计 4 条消息），得 %d", page.Total)
	}
	// 带伞：到点、明日降水 70% ≥ 50 → 推一次；同日再调不推
	day := time.Date(now.Year(), now.Month(), now.Day(), 20, 30, 0, 0, now.Location())
	tomorrow := day.AddDate(0, 0, 1).Format("2006-01-02")
	wx := &weather.Resp{City: "北京", Daily: []weather.Day{{Date: tomorrow, Code: 61, Max: 18, Min: 12, Precip: 70}}}
	checkUmbrella(st, cfg, wx, day)
	checkUmbrella(st, cfg, wx, day.Add(time.Hour))
	page, _ = st.ListMessages(MessageType, false, 1, 10)
	if page.Total != 5 || !strings.Contains(page.Items[0].Title, "带伞") {
		t.Fatalf("带伞提醒应推一次: %d %+v", page.Total, page.Items[0])
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
