package assistant

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------- ICS 生成（RFC 5545 子集：VCALENDAR + VEVENT，供手机日历订阅） ----------

// FeedEvent 导出事件：AllDay 用 VALUE=DATE（End 为不含端点的次日）；定时事件带
// 服务端时区 TZID（取不到 IANA 名时写浮动时间，由订阅端按本地解释）。
type FeedEvent struct {
	UID         string
	Summary     string
	Description string
	Start       time.Time
	End         time.Time
	AllDay      bool
	RRule       string // 如 FREQ=YEARLY；空=单次
}

// BuildICS 生成日历文本（CRLF 行尾、75 字节折行、文本转义）。
func BuildICS(calName string, events []FeedEvent, now time.Time) []byte {
	var b bytes.Buffer
	w := func(line string) {
		b.WriteString(foldICSLine(line))
		b.WriteString("\r\n")
	}
	w("BEGIN:VCALENDAR")
	w("VERSION:2.0")
	w("PRODID:-//PicoPanel//Calendar//CN")
	w("CALSCALE:GREGORIAN")
	w("METHOD:PUBLISH")
	w("X-WR-CALNAME:" + icsEscape(calName))
	stamp := now.UTC().Format("20060102T150405Z")
	for _, e := range events {
		w("BEGIN:VEVENT")
		w("UID:" + icsEscape(e.UID))
		w("DTSTAMP:" + stamp)
		if e.AllDay {
			w("DTSTART;VALUE=DATE:" + e.Start.Format("20060102"))
			end := e.End
			if end.IsZero() || !end.After(e.Start) {
				end = e.Start.AddDate(0, 0, 1)
			}
			w("DTEND;VALUE=DATE:" + end.Format("20060102"))
		} else {
			w(icsDateTimeProp("DTSTART", e.Start))
			end := e.End
			if end.IsZero() || end.Before(e.Start) {
				end = e.Start
			}
			w(icsDateTimeProp("DTEND", end))
		}
		w("SUMMARY:" + icsEscape(e.Summary))
		if e.Description != "" {
			w("DESCRIPTION:" + icsEscape(e.Description))
		}
		if e.RRule != "" {
			w("RRULE:" + e.RRule)
		}
		w("END:VEVENT")
	}
	w("END:VCALENDAR")
	return b.Bytes()
}

// icsDateTimeProp 定时属性：IANA 时区名可用时带 TZID，否则浮动时间。
func icsDateTimeProp(name string, t time.Time) string {
	if tz := ianaZone(t.Location()); tz != "" {
		return name + ";TZID=" + tz + ":" + t.Format("20060102T150405")
	}
	return name + ":" + t.Format("20060102T150405")
}

// ianaZone 时区名是否像 IANA 标识（含 "/"，排除 Local/UTC 这类订阅端认不出的名字）。
func ianaZone(loc *time.Location) string {
	if loc == nil {
		return ""
	}
	n := loc.String()
	if strings.Contains(n, "/") {
		return n
	}
	return ""
}

func icsEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func icsUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// foldICSLine 超过 75 字节的行按 RFC 5545 折行（续行以空格起），在 UTF-8 字符边界切。
func foldICSLine(line string) string {
	const limit = 75
	if len(line) <= limit {
		return line
	}
	var b strings.Builder
	cur := 0
	for _, r := range line {
		n := utf8.RuneLen(r)
		if cur+n > limit {
			b.WriteString("\r\n ")
			cur = 1
		}
		b.WriteRune(r)
		cur += n
	}
	return b.String()
}

// ---------- ICS 解析（订阅导入） ----------

// VEvent 解析出的一个事件（重复规则未展开）。
type VEvent struct {
	UID          string
	Summary      string
	Description  string
	Location     string
	Start        time.Time
	End          time.Time // 零值=按 DURATION/缺省推算
	Duration     time.Duration
	HasDuration  bool
	AllDay       bool
	RRule        string
	ExDates      []time.Time
	RecurrenceID time.Time // 非零=某次发生的覆盖实例
	Cancelled    bool
}

type icsProp struct {
	name   string
	params map[string]string
	value  string
}

// ParseICS 解析 VCALENDAR 文本里的全部 VEVENT（VTODO/VJOURNAL/VTIMEZONE 跳过）。
func ParseICS(data []byte) ([]VEvent, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	raw := strings.Split(text, "\n")
	// 展开折行
	var lines []string
	for _, l := range raw {
		if len(l) > 0 && (l[0] == ' ' || l[0] == '\t') && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(lines[0])), "BEGIN:VCALENDAR") {
		return nil, errors.New("不是 iCalendar 文本（缺 BEGIN:VCALENDAR）")
	}
	var out []VEvent
	var cur *VEvent
	depth := 0 // VEVENT 内嵌套（VALARM）层级
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		p, ok := parseICSProp(l)
		if !ok {
			continue
		}
		switch p.name {
		case "BEGIN":
			if strings.EqualFold(p.value, "VEVENT") && cur == nil {
				cur = &VEvent{}
				depth = 0
				continue
			}
			if cur != nil {
				depth++
			}
			continue
		case "END":
			if cur != nil {
				if depth > 0 {
					depth--
					continue
				}
				if strings.EqualFold(p.value, "VEVENT") {
					if !cur.Start.IsZero() {
						out = append(out, *cur)
					}
					cur = nil
				}
			}
			continue
		}
		if cur == nil || depth > 0 {
			continue
		}
		switch p.name {
		case "UID":
			cur.UID = p.value
		case "SUMMARY":
			cur.Summary = icsUnescape(p.value)
		case "DESCRIPTION":
			cur.Description = icsUnescape(p.value)
		case "LOCATION":
			cur.Location = icsUnescape(p.value)
		case "DTSTART":
			if t, allDay, err := parseICSTime(p.value, p.params); err == nil {
				cur.Start, cur.AllDay = t, allDay
			}
		case "DTEND":
			if t, _, err := parseICSTime(p.value, p.params); err == nil {
				cur.End = t
			}
		case "DURATION":
			if d, err := parseICSDuration(p.value); err == nil {
				cur.Duration, cur.HasDuration = d, true
			}
		case "RRULE":
			cur.RRule = p.value
		case "EXDATE":
			for _, v := range strings.Split(p.value, ",") {
				if t, _, err := parseICSTime(strings.TrimSpace(v), p.params); err == nil {
					cur.ExDates = append(cur.ExDates, t)
				}
			}
		case "RECURRENCE-ID":
			if t, _, err := parseICSTime(p.value, p.params); err == nil {
				cur.RecurrenceID = t
			}
		case "STATUS":
			cur.Cancelled = strings.EqualFold(strings.TrimSpace(p.value), "CANCELLED")
		}
	}
	return out, nil
}

// parseICSProp 拆 NAME;PARAM=V;...:VALUE（冒号定位跳过双引号内）。
func parseICSProp(line string) (icsProp, bool) {
	inQ := false
	colon := -1
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQ = !inQ
		case ':':
			if !inQ {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon <= 0 {
		return icsProp{}, false
	}
	head, val := line[:colon], line[colon+1:]
	parts := strings.Split(head, ";")
	p := icsProp{name: strings.ToUpper(strings.TrimSpace(parts[0])), params: map[string]string{}, value: val}
	for _, kv := range parts[1:] {
		if i := strings.IndexByte(kv, '='); i > 0 {
			p.params[strings.ToUpper(strings.TrimSpace(kv[:i]))] = strings.Trim(strings.TrimSpace(kv[i+1:]), `"`)
		}
	}
	return p, true
}

// parseICSTime 三形态：YYYYMMDD（全天，本地 0 点）/ YYYYMMDDTHHMMSS（浮动=本地或 TZID）/
// 末尾 Z（UTC）。TZID 非 IANA 名（Windows 名）回落本地。
func parseICSTime(v string, params map[string]string) (time.Time, bool, error) {
	v = strings.TrimSpace(v)
	if len(v) == 8 || strings.EqualFold(params["VALUE"], "DATE") {
		t, err := time.ParseInLocation("20060102", v[:minInt(8, len(v))], time.Local)
		return t, true, err
	}
	loc := time.Local
	if tz := params["TZID"]; tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	if strings.HasSuffix(v, "Z") {
		t, err := time.Parse("20060102T150405Z", v)
		return t.In(time.Local), false, err
	}
	t, err := time.ParseInLocation("20060102T150405", v, loc)
	if err != nil {
		// 容忍无秒形态 YYYYMMDDTHHMM
		t, err = time.ParseInLocation("20060102T1504", v, loc)
	}
	return t, false, err
}

// parseICSDuration 解析 P[nW][nD][T[nH][nM][nS]]（可带负号）。
func parseICSDuration(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	neg := false
	if strings.HasPrefix(v, "-") {
		neg, v = true, v[1:]
	} else if strings.HasPrefix(v, "+") {
		v = v[1:]
	}
	if !strings.HasPrefix(strings.ToUpper(v), "P") {
		return 0, fmt.Errorf("非法 DURATION: %s", v)
	}
	v = strings.ToUpper(v[1:])
	var d time.Duration
	num := ""
	inTime := false
	for _, c := range v {
		switch {
		case c >= '0' && c <= '9':
			num += string(c)
		case c == 'T':
			inTime = true
		default:
			n, _ := strconv.Atoi(num)
			num = ""
			switch c {
			case 'W':
				d += time.Duration(n) * 7 * 24 * time.Hour
			case 'D':
				d += time.Duration(n) * 24 * time.Hour
			case 'H':
				d += time.Duration(n) * time.Hour
			case 'M':
				if inTime {
					d += time.Duration(n) * time.Minute
				}
			case 'S':
				d += time.Duration(n) * time.Second
			default:
				return 0, fmt.Errorf("非法 DURATION: %s", v)
			}
		}
	}
	if neg {
		d = -d
	}
	return d, nil
}

// ---------- 重复规则展开 ----------

// Occurrence 一次发生。
type Occurrence struct {
	Start  time.Time
	End    time.Time
	AllDay bool
}

type rrule struct {
	freq       string
	interval   int
	count      int
	until      time.Time
	hasUntil   bool
	byDay      []byDayRule
	byMonthDay []int
	byMonth    []int
}

type byDayRule struct {
	ord int // 0=每个该星期几；±n=第 n 个/倒数第 n 个
	wd  time.Weekday
}

var icsWeekdays = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

func parseRRule(s string, loc *time.Location) (rrule, error) {
	r := rrule{interval: 1}
	for _, kv := range strings.Split(s, ";") {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k, v := strings.ToUpper(strings.TrimSpace(kv[:i])), strings.TrimSpace(kv[i+1:])
		switch k {
		case "FREQ":
			r.freq = strings.ToUpper(v)
		case "INTERVAL":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				r.interval = n
			}
		case "COUNT":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				r.count = n
			}
		case "UNTIL":
			if t, allDay, err := parseICSTime(v, map[string]string{}); err == nil {
				if allDay {
					t = t.AddDate(0, 0, 1).Add(-time.Second) // 全天 UNTIL 含当日
				}
				r.until, r.hasUntil = t.In(loc), true
			}
		case "BYDAY":
			for _, d := range strings.Split(v, ",") {
				d = strings.ToUpper(strings.TrimSpace(d))
				if len(d) < 2 {
					continue
				}
				wd, ok := icsWeekdays[d[len(d)-2:]]
				if !ok {
					continue
				}
				ord := 0
				if len(d) > 2 {
					ord, _ = strconv.Atoi(d[:len(d)-2])
				}
				r.byDay = append(r.byDay, byDayRule{ord: ord, wd: wd})
			}
		case "BYMONTHDAY":
			for _, d := range strings.Split(v, ",") {
				if n, err := strconv.Atoi(strings.TrimSpace(d)); err == nil && n != 0 && n >= -31 && n <= 31 {
					r.byMonthDay = append(r.byMonthDay, n)
				}
			}
		case "BYMONTH":
			for _, d := range strings.Split(v, ",") {
				if n, err := strconv.Atoi(strings.TrimSpace(d)); err == nil && n >= 1 && n <= 12 {
					r.byMonth = append(r.byMonth, n)
				}
			}
		}
	}
	switch r.freq {
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
		return r, nil
	}
	return r, fmt.Errorf("不支持的 FREQ: %q", r.freq)
}

const (
	expandMaxPeriods     = 5000
	expandMaxOccurrences = 2000
)

// eventDuration 事件时长：DTEND 优先，其次 DURATION，缺省全天 1 天 / 定时 0。
func eventDuration(ev VEvent) time.Duration {
	if !ev.End.IsZero() && ev.End.After(ev.Start) {
		return ev.End.Sub(ev.Start)
	}
	if ev.HasDuration && ev.Duration > 0 {
		return ev.Duration
	}
	if ev.AllDay {
		return 24 * time.Hour
	}
	return 0
}

// Expand 把事件展开到 [winFrom, winTo) 窗口内的全部发生；overrides 为同 UID 其他
// VEVENT 的 RECURRENCE-ID（这些时刻由覆盖实例代替、主规则跳过）。非重复事件
// 只要与窗口相交即返回自身。
func Expand(ev VEvent, winFrom, winTo time.Time, overrides map[int64]bool) []Occurrence {
	dur := eventDuration(ev)
	inWin := func(s time.Time) bool {
		e := s.Add(dur)
		if dur == 0 {
			e = s.Add(time.Second)
		}
		return s.Before(winTo) && e.After(winFrom)
	}
	if ev.RRule == "" {
		if inWin(ev.Start) {
			return []Occurrence{{Start: ev.Start, End: ev.Start.Add(dur), AllDay: ev.AllDay}}
		}
		return nil
	}
	rule, err := parseRRule(ev.RRule, ev.Start.Location())
	if err != nil {
		if inWin(ev.Start) {
			return []Occurrence{{Start: ev.Start, End: ev.Start.Add(dur), AllDay: ev.AllDay}}
		}
		return nil
	}
	ex := map[int64]bool{}
	for _, x := range ev.ExDates {
		ex[x.Unix()] = true
		ex[dateOnly(x).Unix()] = true // 全天事件 EXDATE 常只给日期
	}
	skip := func(t time.Time) bool {
		if ex[t.Unix()] || (ev.AllDay && ex[dateOnly(t).Unix()]) {
			return true
		}
		return overrides != nil && overrides[t.Unix()]
	}
	var out []Occurrence
	produced := 0 // 含窗口外的发生计数（COUNT 语义）
	emit := func(t time.Time) bool {
		if rule.hasUntil && t.After(rule.until) {
			return false
		}
		produced++
		if rule.count > 0 && produced > rule.count {
			return false
		}
		if !skip(t) && inWin(t) {
			out = append(out, Occurrence{Start: t, End: t.Add(dur), AllDay: ev.AllDay})
		}
		return len(out) < expandMaxOccurrences
	}
	start := ev.Start
	loc := start.Location()
	h, mi, s := start.Clock()
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, h, mi, s, 0, loc) }
	// 起始周期：无 COUNT 时可从窗口附近快进（COUNT 必须从头数）
	first := 0
	if rule.count == 0 && winFrom.After(start) {
		switch rule.freq {
		case "DAILY":
			first = daysBetween(start, winFrom) / rule.interval
		case "WEEKLY":
			first = daysBetween(start, winFrom) / 7 / rule.interval
		case "MONTHLY":
			first = ((winFrom.Year()-start.Year())*12 + int(winFrom.Month()) - int(start.Month())) / rule.interval
		case "YEARLY":
			first = (winFrom.Year() - start.Year()) / rule.interval
		}
		if first > 0 {
			first-- // 留一个周期余量覆盖跨窗口边界的长事件
		}
		if first < 0 {
			first = 0
		}
	}
	for i := first; i < first+expandMaxPeriods; i++ {
		var cands []time.Time
		var periodStart time.Time
		switch rule.freq {
		case "DAILY":
			d := dateOnly(start).AddDate(0, 0, i*rule.interval)
			periodStart = d
			if len(rule.byMonth) > 0 && !containsInt(rule.byMonth, int(d.Month())) {
				break
			}
			if len(rule.byDay) > 0 && !hasWeekday(rule.byDay, d.Weekday()) {
				break
			}
			cands = append(cands, at(d.Year(), d.Month(), d.Day()))
		case "WEEKLY":
			ws := startOfWeek(dateOnly(start)).AddDate(0, 0, i*7*rule.interval)
			periodStart = ws
			days := rule.byDay
			if len(days) == 0 {
				days = []byDayRule{{wd: start.Weekday()}}
			}
			for _, bd := range days {
				off := (int(bd.wd) - int(time.Monday) + 7) % 7
				d := ws.AddDate(0, 0, off)
				cands = append(cands, at(d.Year(), d.Month(), d.Day()))
			}
		case "MONTHLY":
			y, m := addMonths(start.Year(), start.Month(), i*rule.interval)
			periodStart = time.Date(y, m, 1, 0, 0, 0, 0, loc)
			if len(rule.byMonth) > 0 && !containsInt(rule.byMonth, int(m)) {
				break
			}
			cands = monthCandidates(y, m, start.Day(), rule, at)
		case "YEARLY":
			y := start.Year() + i*rule.interval
			periodStart = time.Date(y, 1, 1, 0, 0, 0, 0, loc)
			months := rule.byMonth
			if len(months) == 0 {
				months = []int{int(start.Month())}
			}
			for _, mm := range months {
				cands = append(cands, monthCandidates(y, time.Month(mm), start.Day(), rule, at)...)
			}
		}
		if periodStart.After(winTo) && rule.count == 0 {
			break
		}
		sortTimes(cands)
		stop := false
		for _, c := range cands {
			if c.Before(start) {
				continue
			}
			if !emit(c) {
				stop = true
				break
			}
		}
		if stop {
			break
		}
		if rule.hasUntil && periodStart.After(rule.until) {
			break
		}
	}
	return out
}

// monthCandidates 某月内按 BYMONTHDAY / BYDAY(序数) / 起始日 生成候选。
func monthCandidates(y int, m time.Month, startDay int, rule rrule, at func(int, time.Month, int) time.Time) []time.Time {
	dim := daysIn(y, m)
	var out []time.Time
	switch {
	case len(rule.byMonthDay) > 0:
		for _, md := range rule.byMonthDay {
			d := md
			if d < 0 {
				d = dim + 1 + md
			}
			if d < 1 || d > dim {
				continue
			}
			t := at(y, m, d)
			if len(rule.byDay) > 0 && !hasWeekday(rule.byDay, t.Weekday()) {
				continue
			}
			out = append(out, t)
		}
	case len(rule.byDay) > 0:
		for _, bd := range rule.byDay {
			if bd.ord == 0 {
				for d := 1; d <= dim; d++ {
					if t := at(y, m, d); t.Weekday() == bd.wd {
						out = append(out, t)
					}
				}
				continue
			}
			if d := nthWeekday(y, m, bd.wd, bd.ord); d > 0 {
				out = append(out, at(y, m, d))
			}
		}
	default:
		if startDay <= dim {
			out = append(out, at(y, m, startDay))
		}
	}
	return out
}

// nthWeekday 某月第 n 个（n<0 倒数第 |n| 个）星期 wd 的日号，不存在返回 0。
func nthWeekday(y int, m time.Month, wd time.Weekday, n int) int {
	dim := daysIn(y, m)
	var days []int
	for d := 1; d <= dim; d++ {
		if time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Weekday() == wd {
			days = append(days, d)
		}
	}
	if n > 0 && n <= len(days) {
		return days[n-1]
	}
	if n < 0 && -n <= len(days) {
		return days[len(days)+n]
	}
	return 0
}

func daysIn(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func addMonths(y int, m time.Month, n int) (int, time.Month) {
	total := y*12 + int(m) - 1 + n
	return total / 12, time.Month(total%12 + 1)
}

func startOfWeek(d time.Time) time.Time {
	off := (int(d.Weekday()) - int(time.Monday) + 7) % 7
	return d.AddDate(0, 0, -off)
}

func hasWeekday(rules []byDayRule, wd time.Weekday) bool {
	for _, r := range rules {
		if r.wd == wd {
			return true
		}
	}
	return false
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func sortTimes(ts []time.Time) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].Before(ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}
