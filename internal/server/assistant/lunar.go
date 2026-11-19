// Package assistant 个人助理（2026-10-26 批）：每日早报、天气预警/带伞/空气提醒、
// 日历聚合（农历/节假日/ICS 订阅与导出）、倒数日换算。全部复用现有数据源与
// 三渠道推送，自身只持有 settings 表里的少量键与两张订阅表。
package assistant

import (
	"fmt"
	"math"
	"time"

	"github.com/6tail/lunar-go/calendar"
)

// DayLunar 某公历日的农历信息（供日历格与早报用）。
type DayLunar struct {
	Month     string   // 「九月」（闰月为「闰九月」）
	Day       string   // 「初七」
	MonthNum  int      // 农历月 1-12（闰月仍为正数）
	DayNum    int      // 农历日 1-30
	Leap      bool     // 是否闰月
	YearGZ    string   // 「丙午」
	ShengXiao string   // 「马」
	JieQi     string   // 当日节气名（非节气日为空）
	Festivals []string // 农历节日 + 公历节日（春节/元宵/端午/中秋/元旦/劳动节…），无则空
}

func solarOf(t time.Time) *calendar.Solar {
	return calendar.NewSolarFromYmd(t.Year(), int(t.Month()), t.Day())
}

// LunarOf 公历日 → 农历信息。
func LunarOf(t time.Time) DayLunar {
	sol := solarOf(t)
	lu := sol.GetLunar()
	m := lu.GetMonth()
	d := DayLunar{
		Month:     lu.GetMonthInChinese() + "月",
		Day:       lu.GetDayInChinese(),
		MonthNum:  absInt(m),
		DayNum:    lu.GetDay(),
		Leap:      m < 0,
		YearGZ:    lu.GetYearInGanZhi(),
		ShengXiao: lu.GetYearShengXiao(),
		JieQi:     lu.GetJieQi(),
	}
	for e := lu.GetFestivals().Front(); e != nil; e = e.Next() {
		if s, ok := e.Value.(string); ok && s != "" {
			d.Festivals = append(d.Festivals, s)
		}
	}
	for e := sol.GetFestivals().Front(); e != nil; e = e.Next() {
		if s, ok := e.Value.(string); ok && s != "" {
			d.Festivals = append(d.Festivals, s)
		}
	}
	return d
}

// LunarMonthDay 公历日对应的农历月/日（闰月按正数月记，纪念日口径：闰月生日
// 按当月非闰月过）。
func LunarMonthDay(t time.Time) (month, day int) {
	lu := solarOf(t).GetLunar()
	return absInt(lu.GetMonth()), lu.GetDay()
}

// LunarLabel 「农历九月初七」样式标签。
func LunarLabel(month, day int) string {
	if month < 1 || month > 12 || day < 1 || day > 30 {
		return ""
	}
	// 借任意一个含该月的农历年取中文名（月名/日名不随年变）
	lu := calendar.NewLunarFromYmd(2025, month, minInt(day, 29))
	dayName := lu.GetDayInChinese()
	if day == 30 {
		dayName = "三十"
	}
	return "农历" + lu.GetMonthInChinese() + "月" + dayName
}

// lunarDateIn 农历年 ly 的 month/day 对应公历日（当年该月天数不足时取月末；
// 农历年不存在该月——理论上 1-12 都存在——返回 false）。
func lunarDateIn(ly, month, day int, loc *time.Location) (time.Time, bool) {
	lm := calendar.NewLunarMonthFromYm(ly, month)
	if lm == nil {
		return time.Time{}, false
	}
	if day > lm.GetDayCount() {
		day = lm.GetDayCount()
	}
	sol := calendar.NewLunarFromYmd(ly, month, day).GetSolar()
	return time.Date(sol.GetYear(), time.Month(sol.GetMonth()), sol.GetDay(), 0, 0, 0, 0, loc), true
}

// NextLunarYearly 农历 month/day 每年重复，返回 ≥ from（按日）的最近一次公历日。
func NextLunarYearly(month, day int, from time.Time) (time.Time, bool) {
	from = dateOnly(from)
	ly := solarOf(from).GetLunar().GetYear()
	for i := -1; i <= 2; i++ {
		if d, ok := lunarDateIn(ly+i, month, day, from.Location()); ok && !d.Before(from) {
			return d, true
		}
	}
	return time.Time{}, false
}

// LunarYearlyBetween 农历 month/day 每年重复在 [from, to] 内的全部公历日。
func LunarYearlyBetween(month, day int, from, to time.Time) []time.Time {
	from, to = dateOnly(from), dateOnly(to)
	var out []time.Time
	ly := solarOf(from).GetLunar().GetYear()
	for i := -1; i <= 3; i++ {
		d, ok := lunarDateIn(ly+i, month, day, from.Location())
		if !ok || d.Before(from) || d.After(to) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// dateOnly 抹掉时分秒（保留时区）。
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// daysBetween b-a 的整天数（均按日历日，忽略时分；四舍五入抹掉夏令时偏差）。
func daysBetween(a, b time.Time) int {
	a, b = dateOnly(a), dateOnly(b)
	return int(math.Round(b.Sub(a).Hours() / 24))
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ymd YYYY-MM-DD。
func ymd(t time.Time) string { return t.Format("2006-01-02") }

// parseYMD 解析 YYYY-MM-DD 为本地 0 点。
func parseYMD(s string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("日期格式须为 YYYY-MM-DD")
	}
	return t, nil
}

// weekdayCN 周一…周日。
func weekdayCN(t time.Time) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[t.Weekday()]
}
