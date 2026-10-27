package assistant

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// 法定节假日与调休：两层数据——内置表（国务院办公厅年度通知，发布后在此追加）+
// 远程 holiday-cn（见 holiday_cn.go，每日同步、缓存进 settings）。同一年份远程数据
// 覆盖内置；查询一律走合并视图（holMarks / holPeriods），读写加锁。
//
// 内置口径：off 段为连续放假日期（含周末）；work 为调休上班的周末日。
// 2025：国办发明电〔2024〕12 号；2026：国办发明电 2025-11-04 通知
//（元旦 1.1-1.3 休、1.4 班；春节 2.15-2.23 休、2.14/2.28 班；清明 4.4-4.6；
// 劳动节 5.1-5.5 休、5.9 班；端午 6.19-6.21；中秋 9.25-9.27；国庆 10.1-10.7 休、9.20/10.10 班）。
type holidaySpec struct {
	name string
	off  [2]string // 起止 MM-DD（闭区间）
	work []string  // 调休上班 MM-DD
}

var holidayYears = map[int][]holidaySpec{
	2025: {
		{"元旦", [2]string{"01-01", "01-01"}, nil},
		{"春节", [2]string{"01-28", "02-04"}, []string{"01-26", "02-08"}},
		{"清明节", [2]string{"04-04", "04-06"}, nil},
		{"劳动节", [2]string{"05-01", "05-05"}, []string{"04-27"}},
		{"端午节", [2]string{"05-31", "06-02"}, nil},
		{"国庆节、中秋节", [2]string{"10-01", "10-08"}, []string{"09-28", "10-11"}},
	},
	2026: {
		{"元旦", [2]string{"01-01", "01-03"}, []string{"01-04"}},
		{"春节", [2]string{"02-15", "02-23"}, []string{"02-14", "02-28"}},
		{"清明节", [2]string{"04-04", "04-06"}, nil},
		{"劳动节", [2]string{"05-01", "05-05"}, []string{"05-09"}},
		{"端午节", [2]string{"06-19", "06-21"}, nil},
		{"中秋节", [2]string{"09-25", "09-27"}, nil},
		{"国庆节", [2]string{"10-01", "10-07"}, []string{"09-20", "10-10"}},
	},
}

// HolidayMark 某日的节假日标记：Off=true 放假（Name=节日名，Index/Total=第几天/共几天），
// Off=false 为调休上班日（Name 为所属节日）。
type HolidayMark struct {
	Name  string `json:"name"`
	Off   bool   `json:"off"`
	Index int    `json:"index,omitempty"`
	Total int    `json:"total,omitempty"`
}

// HolidayDay 休息日清单一行（设置弹层展示 / 远程缓存存储形态）。
type HolidayDay struct {
	Date string `json:"date"`
	Name string `json:"name"`
	Off  bool   `json:"off"`
}

// HolidayYearInfo 设置弹层里的一个年份：来源（builtin/remote）、拉取时间、全部条目。
type HolidayYearInfo struct {
	Year      int          `json:"year"`
	Source    string       `json:"source"`
	FetchedAt int64        `json:"fetched_at,omitempty"`
	Days      []HolidayDay `json:"days"`
}

// holidayPeriod 一段连续假期（展开后的日期列表）。
type holidayPeriod struct {
	name  string
	days  []string // YYYY-MM-DD 升序
	start time.Time
}

type holidayYearData struct {
	source    string
	fetchedAt int64
	days      []HolidayDay // 升序
	marks     map[string]HolidayMark
	periods   []holidayPeriod
}

var (
	holMu      sync.RWMutex
	holYears   = map[int]*holidayYearData{}
	holMarks   = map[string]HolidayMark{}
	holPeriods []holidayPeriod // 按开始日升序（合并视图）
)

func init() {
	for y, specs := range holidayYears {
		holYears[y] = builtinYearData(y, specs)
	}
	rebuildHolidayView()
}

// builtinYearData 内置规格展开为一年数据。
func builtinYearData(year int, specs []holidaySpec) *holidayYearData {
	d := &holidayYearData{source: "builtin", marks: map[string]HolidayMark{}}
	for _, sp := range specs {
		from, _ := time.Parse("2006-01-02", fmt.Sprintf("%d-%s", year, sp.off[0]))
		to, _ := time.Parse("2006-01-02", fmt.Sprintf("%d-%s", year, sp.off[1]))
		p := holidayPeriod{name: sp.name, start: from}
		for dt := from; !dt.After(to); dt = dt.AddDate(0, 0, 1) {
			ds := dt.Format("2006-01-02")
			p.days = append(p.days, ds)
			d.days = append(d.days, HolidayDay{Date: ds, Name: sp.name, Off: true})
		}
		for i, ds := range p.days {
			d.marks[ds] = HolidayMark{Name: sp.name, Off: true, Index: i + 1, Total: len(p.days)}
		}
		for _, w := range sp.work {
			ws := fmt.Sprintf("%d-%s", year, w)
			d.marks[ws] = HolidayMark{Name: sp.name, Off: false}
			d.days = append(d.days, HolidayDay{Date: ws, Name: sp.name + "调休上班", Off: false})
		}
		d.periods = append(d.periods, p)
	}
	sortHolidayDays(d.days)
	return d
}

// remoteYearData holiday-cn 条目（逐日 名称/日期/是否放假）整理为一年数据：
// 日期连续的放假日并成一段假期（段名取首日名称），调休上班日单独标记。
func remoteYearData(days []HolidayDay, fetchedAt int64) *holidayYearData {
	sortHolidayDays(days)
	d := &holidayYearData{source: "remote", fetchedAt: fetchedAt, days: days, marks: map[string]HolidayMark{}}
	var cur *holidayPeriod
	flush := func() {
		if cur == nil {
			return
		}
		for i, ds := range cur.days {
			d.marks[ds] = HolidayMark{Name: cur.name, Off: true, Index: i + 1, Total: len(cur.days)}
		}
		d.periods = append(d.periods, *cur)
		cur = nil
	}
	for _, day := range days {
		t, err := time.Parse("2006-01-02", day.Date)
		if err != nil {
			continue
		}
		if !day.Off {
			flush()
			d.marks[day.Date] = HolidayMark{Name: day.Name, Off: false}
			continue
		}
		if cur != nil {
			last, _ := time.Parse("2006-01-02", cur.days[len(cur.days)-1])
			if !last.AddDate(0, 0, 1).Equal(t) {
				flush()
			}
		}
		if cur == nil {
			cur = &holidayPeriod{name: day.Name, start: t}
		}
		cur.days = append(cur.days, day.Date)
	}
	flush()
	return d
}

func sortHolidayDays(days []HolidayDay) {
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
}

// rebuildHolidayView 由各年份数据重建合并视图（调用方须持写锁）。
func rebuildHolidayView() {
	holMarks = map[string]HolidayMark{}
	holPeriods = nil
	for _, d := range holYears {
		for k, v := range d.marks {
			holMarks[k] = v
		}
		holPeriods = append(holPeriods, d.periods...)
	}
	sort.Slice(holPeriods, func(i, j int) bool { return holPeriods[i].start.Before(holPeriods[j].start) })
}

// applyHolidayYear 放入（覆盖）一年数据并重建合并视图。
func applyHolidayYear(year int, d *holidayYearData) {
	holMu.Lock()
	holYears[year] = d
	rebuildHolidayView()
	holMu.Unlock()
}

// HolidayOf 查某日（YYYY-MM-DD）标记；非节假日/调休日返回 (zero, false)。
func HolidayOf(date string) (HolidayMark, bool) {
	holMu.RLock()
	defer holMu.RUnlock()
	m, ok := holMarks[date]
	return m, ok
}

// HolidayKnownYear 该年是否已有节假日数据（内置或远程）。
func HolidayKnownYear(year int) bool {
	holMu.RLock()
	defer holMu.RUnlock()
	_, ok := holYears[year]
	return ok
}

// HolidayKnownYears 已有数据的年份（升序；前端按当前浏览年份判断是否提示「暂无该年数据」）。
func HolidayKnownYears() []int {
	holMu.RLock()
	defer holMu.RUnlock()
	ys := make([]int, 0, len(holYears))
	for y := range holYears {
		ys = append(ys, y)
	}
	sort.Ints(ys)
	return ys
}

// HolidayYearsSnapshot 设置弹层用：各年份来源/拉取时间/全部条目（年份升序）。
func HolidayYearsSnapshot() []HolidayYearInfo {
	holMu.RLock()
	defer holMu.RUnlock()
	out := make([]HolidayYearInfo, 0, len(holYears))
	for y, d := range holYears {
		days := make([]HolidayDay, len(d.days))
		copy(days, d.days)
		out = append(out, HolidayYearInfo{Year: y, Source: d.source, FetchedAt: d.fetchedAt, Days: days})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Year < out[j].Year })
	return out
}

// nextHoliday 返回开始日 > today 的最近一段假期（today 本身在假期内不算下一段）。
func nextHoliday(today time.Time) (holidayPeriod, bool) {
	ts := ymd(today)
	holMu.RLock()
	defer holMu.RUnlock()
	for _, p := range holPeriods {
		if len(p.days) > 0 && p.days[0] > ts {
			return p, true
		}
	}
	return holidayPeriod{}, false
}

// HolidayLine 早报里的假期一行：假期中→「国庆节假期第 3 天 / 共 7 天」；
// 调休上班→「今天调休上班（国庆节）」；明天起放假→「明天起 X 放假 N 天」；
// 其余→「距 X 还有 N 天（M月D日起放假 n 天）」；无数据返回空。
func HolidayLine(today time.Time) string {
	ts := ymd(today)
	if m, ok := HolidayOf(ts); ok {
		if m.Off {
			return fmt.Sprintf("%s假期第 %d 天（共 %d 天）", m.Name, m.Index, m.Total)
		}
		return fmt.Sprintf("今天调休上班（%s）", m.Name)
	}
	tomorrow := ymd(today.AddDate(0, 0, 1))
	if m, ok := HolidayOf(tomorrow); ok && !m.Off {
		return fmt.Sprintf("明天调休上班（%s）", m.Name)
	}
	p, ok := nextHoliday(today)
	if !ok {
		return ""
	}
	start, _ := time.ParseInLocation("2006-01-02", p.days[0], today.Location())
	n := daysBetween(today, start)
	if n == 1 {
		return fmt.Sprintf("明天起%s放假 %d 天", p.name, len(p.days))
	}
	return fmt.Sprintf("距%s还有 %d 天（%d月%d日起放假 %d 天）", p.name, n, start.Month(), start.Day(), len(p.days))
}
