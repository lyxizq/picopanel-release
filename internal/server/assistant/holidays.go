package assistant

import (
	"fmt"
	"sort"
	"time"
)

// 法定节假日与调休（国务院办公厅年度通知）。按年份内置，每年通知发布后在此
// 追加一组即可（通常上一年 11 月发布）；未内置年份日历格不标休/班、早报不提假期。
//
// 口径：off 段为连续放假日期（含周末）；work 为调休上班的周末日。
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

// holidayPeriod 一段假期（展开后的日期列表）。
type holidayPeriod struct {
	name  string
	days  []string // YYYY-MM-DD 升序
	start time.Time
}

var (
	holidayByDate map[string]HolidayMark
	holidayList   []holidayPeriod // 按开始日升序
)

func init() {
	holidayByDate = map[string]HolidayMark{}
	for year, specs := range holidayYears {
		for _, sp := range specs {
			from, _ := time.Parse("2006-01-02", fmt.Sprintf("%d-%s", year, sp.off[0]))
			to, _ := time.Parse("2006-01-02", fmt.Sprintf("%d-%s", year, sp.off[1]))
			p := holidayPeriod{name: sp.name, start: from}
			for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
				p.days = append(p.days, d.Format("2006-01-02"))
			}
			for i, ds := range p.days {
				holidayByDate[ds] = HolidayMark{Name: sp.name, Off: true, Index: i + 1, Total: len(p.days)}
			}
			for _, w := range sp.work {
				holidayByDate[fmt.Sprintf("%d-%s", year, w)] = HolidayMark{Name: sp.name, Off: false}
			}
			holidayList = append(holidayList, p)
		}
	}
	sort.Slice(holidayList, func(i, j int) bool { return holidayList[i].start.Before(holidayList[j].start) })
}

// HolidayOf 查某日（YYYY-MM-DD）标记；非节假日/调休日返回 (zero, false)。
func HolidayOf(date string) (HolidayMark, bool) {
	m, ok := holidayByDate[date]
	return m, ok
}

// HolidayKnownYear 该年是否已内置节假日表。
func HolidayKnownYear(year int) bool {
	_, ok := holidayYears[year]
	return ok
}

// HolidayKnownYears 已内置节假日表的年份（升序；前端按当前浏览年份判断是否提示「暂无该年数据」）。
func HolidayKnownYears() []int {
	ys := make([]int, 0, len(holidayYears))
	for y := range holidayYears {
		ys = append(ys, y)
	}
	sort.Ints(ys)
	return ys
}

// nextHoliday 返回开始日 > today 的最近一段假期（today 本身在假期内不算下一段）。
func nextHoliday(today time.Time) (holidayPeriod, bool) {
	ts := ymd(today)
	for _, p := range holidayList {
		if p.days[0] > ts {
			return p, true
		}
	}
	return holidayPeriod{}, false
}

// HolidayLine 早报里的假期一行：假期中→「国庆节假期第 3 天 / 共 7 天」；
// 调休上班→「今天调休上班（国庆节）」；明天起放假→「明天起 X 放假 N 天」；
// 其余→「距 X 还有 N 天（M月D日起放假 n 天）」；无内置数据返回空。
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
