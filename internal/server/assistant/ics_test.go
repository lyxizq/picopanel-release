package assistant

import (
	"strings"
	"testing"
	"time"
)

var sh = time.FixedZone("CST", 8*3600)

func date(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, sh) }

func TestBuildICSEscapeFoldAllDay(t *testing.T) {
	long := strings.Repeat("很长的标题", 20) // 远超 75 字节
	out := string(BuildICS("PicoPanel", []FeedEvent{
		{UID: "a@x", Summary: "a, b; c\\d\nline2", Start: date(2026, 10, 27), AllDay: true},
		{UID: "b@x", Summary: long, Start: time.Date(2026, 10, 27, 9, 0, 0, 0, sh), End: time.Date(2026, 10, 27, 9, 30, 0, 0, sh), RRule: "FREQ=YEARLY"},
	}, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)))
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n", "X-WR-CALNAME:PicoPanel\r\n",
		"DTSTART;VALUE=DATE:20261027\r\n", "DTEND;VALUE=DATE:20261028\r\n",
		`SUMMARY:a\, b\; c\\d\nline2` + "\r\n",
		"RRULE:FREQ=YEARLY\r\n", "END:VCALENDAR\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("缺少 %q\n%s", want, out)
		}
	}
	// 定时事件无 IANA 时区名（FixedZone）→ 浮动时间
	if !strings.Contains(out, "DTSTART:20261027T090000\r\n") {
		t.Fatalf("浮动时间形态缺失\n%s", out)
	}
	// 折行：每行 ≤75 字节且续行以空格起、UTF-8 不被切坏
	for _, l := range strings.Split(out, "\r\n") {
		if len(l) > 75 {
			t.Fatalf("行超 75 字节: %d %q", len(l), l)
		}
	}
	joined := strings.ReplaceAll(out, "\r\n ", "")
	if !strings.Contains(joined, "SUMMARY:"+long) {
		t.Fatal("折行还原后标题不一致（UTF-8 被切坏）")
	}
}

const sampleICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:test\r\n" +
	"BEGIN:VTIMEZONE\r\nTZID:Asia/Shanghai\r\nEND:VTIMEZONE\r\n" +
	"BEGIN:VEVENT\r\nUID:weekly@t\r\nDTSTART;TZID=Asia/Shanghai:20261005T100000\r\nDTEND;TZID=Asia/Shanghai:20261005T110000\r\n" +
	"RRULE:FREQ=WEEKLY;BYDAY=MO,WE;COUNT=5\r\nEXDATE;TZID=Asia/Shanghai:20261012T100000\r\n" +
	"SUMMARY:周会\\, 第一\\n行\r\nBEGIN:VALARM\r\nTRIGGER:-PT10M\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:trip@t\r\nDTSTART;VALUE=DATE:20261101\r\nDTEND;VALUE=DATE:20261104\r\nSUMMARY:出差\r\nLOCATION:上海\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:monthly@t\r\nDTSTART:20260131T200000\r\nDURATION:PT1H\r\nRRULE:FREQ=MONTHLY;UNTIL=20260531T000000Z\r\nSUMMARY:月末\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:cancel@t\r\nDTSTART;VALUE=DATE:20261110\r\nSTATUS:CANCELLED\r\nSUMMARY:取消的\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:weekly@t\r\nRECURRENCE-ID;TZID=Asia/Shanghai:20261014T100000\r\nDTSTART;TZID=Asia/Shanghai:20261014T150000\r\nDTEND;TZID=Asia/Shanghai:20261014T160000\r\nSUMMARY:周会（改期）\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseICS(t *testing.T) {
	evs, err := ParseICS([]byte(sampleICS))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 5 {
		t.Fatalf("期望 5 个 VEVENT，得 %d", len(evs))
	}
	w := evs[0]
	if w.Summary != "周会, 第一\n行" || w.RRule != "FREQ=WEEKLY;BYDAY=MO,WE;COUNT=5" || len(w.ExDates) != 1 {
		t.Fatalf("周会解析不符: %+v", w)
	}
	if w.Start.Hour() != 10 || w.Start.Location().String() != "Asia/Shanghai" {
		t.Fatalf("TZID 未生效: %v", w.Start)
	}
	trip := evs[1]
	if !trip.AllDay || trip.Location != "上海" || trip.End.Sub(trip.Start) != 72*time.Hour {
		t.Fatalf("全天多日事件不符: %+v", trip)
	}
	if !evs[3].Cancelled {
		t.Fatal("STATUS:CANCELLED 未识别")
	}
	if evs[4].RecurrenceID.IsZero() {
		t.Fatal("RECURRENCE-ID 未识别")
	}
	if _, err := ParseICS([]byte("hello")); err == nil {
		t.Fatal("非 ICS 文本应报错")
	}
}

func TestExpandRules(t *testing.T) {
	evs, _ := ParseICS([]byte(sampleICS))
	loc, _ := time.LoadLocation("Asia/Shanghai")
	win0, win1 := time.Date(2026, 9, 1, 0, 0, 0, 0, loc), time.Date(2027, 1, 1, 0, 0, 0, 0, loc)

	// WEEKLY BYDAY=MO,WE COUNT=5 从周一 10/5 起：10/5 10/7 10/12 10/14 10/19；EXDATE 去 10/12；
	// 10/14 被 RECURRENCE-ID 覆盖实例替代 → 主规则剩 10/5 10/7 10/19
	ov := map[int64]bool{evs[4].RecurrenceID.Unix(): true}
	occ := Expand(evs[0], win0, win1, ov)
	var got []string
	for _, o := range occ {
		got = append(got, o.Start.Format("01-02 15:04"))
	}
	if strings.Join(got, ",") != "10-05 10:00,10-07 10:00,10-19 10:00" {
		t.Fatalf("WEEKLY 展开不符: %v", got)
	}
	// 全天多日：一条，End=11/04 0 点
	trip := Expand(evs[1], win0, win1, nil)
	if len(trip) != 1 || !trip[0].AllDay || trip[0].End.Day() != 4 {
		t.Fatalf("多日事件展开不符: %+v", trip)
	}
	// MONTHLY 起 1/31、UNTIL 5/31：2 月没有 31 日跳过 → 1/31 3/31 5/31（5/31 20:00 > UNTIL 5/31 00:00Z=08:00 本地，故不含）→ 1/31 3/31
	m := Expand(evs[2], time.Date(2026, 1, 1, 0, 0, 0, 0, loc), time.Date(2026, 12, 31, 0, 0, 0, 0, loc), nil)
	got = got[:0]
	for _, o := range m {
		got = append(got, o.Start.Format("01-02"))
	}
	if strings.Join(got, ",") != "01-31,03-31" {
		t.Fatalf("MONTHLY/UNTIL 展开不符: %v", got)
	}
	// 窗口外不出
	if n := len(Expand(evs[1], time.Date(2027, 1, 1, 0, 0, 0, 0, loc), time.Date(2027, 2, 1, 0, 0, 0, 0, loc), nil)); n != 0 {
		t.Fatalf("窗口外事件应为 0，得 %d", n)
	}
}

func TestExpandYearlyAndMonthlyByDay(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	ev := VEvent{UID: "y", Start: time.Date(2024, 2, 29, 0, 0, 0, 0, loc), AllDay: true, RRule: "FREQ=YEARLY"}
	occ := Expand(ev, time.Date(2024, 1, 1, 0, 0, 0, 0, loc), time.Date(2029, 1, 1, 0, 0, 0, 0, loc), nil)
	if len(occ) != 2 || occ[1].Start.Year() != 2028 {
		t.Fatalf("闰日每年应只在闰年出现（2024/2028），得 %d 个: %+v", len(occ), occ)
	}
	// 每月第二个周二
	ev2 := VEvent{UID: "m", Start: time.Date(2026, 1, 13, 9, 0, 0, 0, loc), RRule: "FREQ=MONTHLY;BYDAY=2TU;COUNT=3"}
	occ = Expand(ev2, time.Date(2026, 1, 1, 0, 0, 0, 0, loc), time.Date(2027, 1, 1, 0, 0, 0, 0, loc), nil)
	var got []string
	for _, o := range occ {
		got = append(got, o.Start.Format("01-02"))
	}
	if strings.Join(got, ",") != "01-13,02-10,03-10" {
		t.Fatalf("MONTHLY BYDAY=2TU 不符: %v", got)
	}
	// 每两天、无 COUNT：快进后窗口内数量正确（窗口 10 天 → 5 次）
	ev3 := VEvent{UID: "d", Start: time.Date(2020, 1, 1, 8, 0, 0, 0, loc), RRule: "FREQ=DAILY;INTERVAL=2"}
	occ = Expand(ev3, time.Date(2026, 3, 1, 0, 0, 0, 0, loc), time.Date(2026, 3, 11, 0, 0, 0, 0, loc), nil)
	if len(occ) != 5 {
		t.Fatalf("DAILY;INTERVAL=2 十天窗口应 5 次，得 %d", len(occ))
	}
	for _, o := range occ {
		if daysBetween(ev3.Start, o.Start)%2 != 0 {
			t.Fatalf("隔天规则相位错: %v", o.Start)
		}
	}
}

func TestParseICSDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"PT1H30M": 90 * time.Minute, "P1D": 24 * time.Hour, "P1W": 7 * 24 * time.Hour, "PT45S": 45 * time.Second, "-PT10M": -10 * time.Minute} {
		d, err := parseICSDuration(in)
		if err != nil || d != want {
			t.Fatalf("%s → %v (%v)，期望 %v", in, d, err, want)
		}
	}
	if _, err := parseICSDuration("1H"); err == nil {
		t.Fatal("非法 DURATION 应报错")
	}
}
