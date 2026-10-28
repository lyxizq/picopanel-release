package assistant

import (
	"errors"
	"sort"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

// 倒数日换算：三种 kind 的「下一发生日」与「天数」口径见 store.Countdown 注释。

// NormalizeCountdown 校验并补齐输入（名称/日期/kind/农历月日），供 API 创建与编辑共用。
func NormalizeCountdown(c *store.Countdown, loc *time.Location) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return errors.New("名称不能为空")
	}
	if len([]rune(c.Name)) > 40 {
		return errors.New("名称最长 40 字")
	}
	t, err := parseYMD(c.Date, loc)
	if err != nil {
		return err
	}
	if t.Year() < 1900 || t.Year() > 2100 {
		return errors.New("日期须在 1900-2100 年之间")
	}
	switch c.Kind {
	case "yearly", "since":
	default:
		c.Kind = "once"
	}
	if c.Lunar && c.Kind != "yearly" {
		c.Lunar = false // 农历只对每年纪念日有意义
	}
	if c.Lunar {
		c.LunarMonth, c.LunarDay = LunarMonthDay(t)
	} else {
		c.LunarMonth, c.LunarDay = 0, 0
	}
	c.Color = strings.TrimSpace(c.Color)
	if len(c.Color) > 20 {
		c.Color = ""
	}
	return nil
}

// Enrich 填充展示字段（NextDate/Days/LunarLabel）。
func Enrich(cs []store.Countdown, today time.Time) {
	today = dateOnly(today)
	for i := range cs {
		enrichOne(&cs[i], today)
	}
}

func enrichOne(c *store.Countdown, today time.Time) {
	anchor, err := parseYMD(c.Date, today.Location())
	if err != nil {
		return
	}
	switch c.Kind {
	case "since":
		c.NextDate = c.Date
		c.Days = daysBetween(anchor, today)
	case "yearly":
		next, ok := nextYearly(c, anchor, today)
		if !ok {
			return
		}
		c.NextDate = ymd(next)
		c.Days = daysBetween(today, next)
		if c.Lunar {
			c.LunarLabel = LunarLabel(c.LunarMonth, c.LunarDay)
		}
	default:
		c.NextDate = c.Date
		c.Days = daysBetween(today, anchor)
	}
}

// nextYearly 每年重复的下一发生日（≥ today）。公历 2 月 29 日非闰年落 2 月 28 日。
func nextYearly(c *store.Countdown, anchor, today time.Time) (time.Time, bool) {
	if c.Lunar {
		m, d := c.LunarMonth, c.LunarDay
		if m == 0 {
			m, d = LunarMonthDay(anchor)
		}
		return NextLunarYearly(m, d, today)
	}
	for y := today.Year(); y <= today.Year()+1; y++ {
		t := solarYearly(anchor, y, today.Location())
		if !t.Before(today) {
			return t, true
		}
	}
	return time.Time{}, false
}

func solarYearly(anchor time.Time, year int, loc *time.Location) time.Time {
	d := anchor.Day()
	if anchor.Month() == time.February && d == 29 && daysIn(year, time.February) == 28 {
		d = 28
	}
	return time.Date(year, anchor.Month(), d, 0, 0, 0, 0, loc)
}

// CountdownOccurrences 倒数日在 [from, to]（按日闭区间）内的发生日：once/since 只有
// 锚点日；yearly 每年一次（农历按农历换算）。
func CountdownOccurrences(c store.Countdown, from, to time.Time) []time.Time {
	from, to = dateOnly(from), dateOnly(to)
	anchor, err := parseYMD(c.Date, from.Location())
	if err != nil {
		return nil
	}
	if c.Kind != "yearly" {
		if !anchor.Before(from) && !anchor.After(to) {
			return []time.Time{anchor}
		}
		return nil
	}
	if c.Lunar {
		m, d := c.LunarMonth, c.LunarDay
		if m == 0 {
			m, d = LunarMonthDay(anchor)
		}
		return LunarYearlyBetween(m, d, from, to)
	}
	var out []time.Time
	for y := from.Year(); y <= to.Year(); y++ {
		t := solarYearly(anchor, y, from.Location())
		if !t.Before(from) && !t.After(to) {
			out = append(out, t)
		}
	}
	return out
}

// SortForCard 主页卡排序：未到的（once/yearly）按剩余天数升序在前，since 按累计天数
// 降序在后，已过的 once 最后。
func SortForCard(cs []store.Countdown) {
	rank := func(c store.Countdown) (int, int) {
		switch {
		case c.Kind == "since":
			return 1, -c.Days
		case c.Days < 0:
			return 2, -c.Days
		default:
			return 0, c.Days
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		ri, ki := rank(cs[i])
		rj, kj := rank(cs[j])
		if ri != rj {
			return ri < rj
		}
		if ki != kj {
			return ki < kj
		}
		return cs[i].Sort < cs[j].Sort
	})
}
