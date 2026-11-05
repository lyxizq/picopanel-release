package assistant

import (
	"fmt"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

// 日历聚合：把面板自有数据（提醒/会员/倒数日/追剧/B 站/证书）与 ICS 订阅事件
// 合成一份按日期的事件清单，另给每天的农历/节气/节日/节假日标记。

// Event 日历视图里的一条事件。
type Event struct {
	ID      string `json:"id"`                 // 源前缀 + 原 id，如 rem-12
	Source  string `json:"source"`             // reminder/membership/countdown/tv/bili/cert/ics
	Title   string `json:"title"`
	Date    string `json:"date"`               // YYYY-MM-DD（开始日）
	EndDate string `json:"end_date,omitempty"` // 多日事件的结束日（含）
	Time    string `json:"time,omitempty"`     // HH:MM，全天为空
	AllDay  bool   `json:"all_day"`
	Color   string `json:"color,omitempty"` // ICS 订阅色 / 倒数日色
	RefID   int64  `json:"ref_id,omitempty"`
	Done    bool   `json:"done,omitempty"` // 提醒事项已完成
	Sub     string `json:"sub,omitempty"`  // 副标题：分组名/订阅名/集数
	Note    string `json:"note,omitempty"`
}

// DayMark 某天的标记。
type DayMark struct {
	Date       string       `json:"date"`
	LunarMonth string       `json:"lunar_month"`
	LunarDay   string       `json:"lunar_day"`
	JieQi      string       `json:"jieqi,omitempty"`
	Festival   string       `json:"festival,omitempty"`
	Holiday    *HolidayMark `json:"holiday,omitempty"`
}

// MaxRangeDays 单次查询最多覆盖的天数（6 周月视图 42 天留余量）。
const MaxRangeDays = 62

// Range 聚合 [from, to]（按日闭区间）内的事件与每日标记；单源失败只跳过该源
// （日历不因某张表故障整页空白），错误聚合到返回值末尾由调用方记日志。
func Range(st *store.Store, from, to time.Time) ([]Event, []DayMark, error) {
	from, to = dateOnly(from), dateOnly(to)
	if to.Before(from) {
		from, to = to, from
	}
	if daysBetween(from, to) > MaxRangeDays {
		to = from.AddDate(0, 0, MaxRangeDays)
	}
	fromS, toS := ymd(from), ymd(to)
	fromTs, toTs := from.Unix(), to.AddDate(0, 0, 1).Unix()
	var events []Event
	var errs []string

	if rs, err := st.RemindersDueBetween(fromS, toS); err != nil {
		errs = append(errs, "提醒事项: "+err.Error())
	} else {
		for _, r := range rs {
			tm := r.DueTime
			if tm == "" {
				tm = "09:00"
			}
			events = append(events, Event{ID: fmt.Sprintf("rem-%d", r.ID), Source: "reminder", Title: r.Title,
				Date: r.DueDate, Time: tm, RefID: r.ID, Done: r.DoneAt != nil, Sub: r.GroupName, Note: firstLine(r.Note)})
		}
	}
	if ms, err := st.ListMemberships(); err != nil {
		errs = append(errs, "会员: "+err.Error())
	} else {
		for _, m := range ms {
			if m.ExpireDate >= fromS && m.ExpireDate <= toS {
				events = append(events, Event{ID: fmt.Sprintf("mem-%d", m.ID), Source: "membership", Title: m.Biz,
					Date: m.ExpireDate, AllDay: true, RefID: m.ID, Sub: strings.TrimSpace(m.Name + " 到期")})
			}
		}
	}
	if cs, err := st.ListCountdowns(); err != nil {
		errs = append(errs, "倒数日: "+err.Error())
	} else {
		for _, c := range cs {
			for _, d := range CountdownOccurrences(c, from, to) {
				sub := ""
				if c.Lunar {
					sub = LunarLabel(c.LunarMonth, c.LunarDay)
				}
				events = append(events, Event{ID: fmt.Sprintf("cd-%d-%s", c.ID, ymd(d)), Source: "countdown", Title: c.Name,
					Date: ymd(d), AllDay: true, Color: c.Color, RefID: c.ID, Sub: sub})
			}
		}
	}
	if tvs, err := st.ListTVFollows(); err != nil {
		errs = append(errs, "追剧: "+err.Error())
	} else {
		for _, t := range tvs {
			if !t.Enabled {
				continue
			}
			seen := map[string]bool{}
			add := func(date string, ep int, aired bool) {
				if date < fromS || date > toS || seen[date] {
					return
				}
				seen[date] = true
				sub := ""
				if ep > 0 {
					sub = fmt.Sprintf("第 %d 集", ep)
				}
				if aired && sub != "" {
					sub += "（已播）"
				}
				events = append(events, Event{ID: fmt.Sprintf("tv-%d-%s", t.ID, date), Source: "tv", Title: t.Name,
					Date: date, AllDay: true, RefID: t.ID, Sub: sub})
			}
			add(t.NextAirDate, t.NextEpisode, false)
			add(t.LastAirDate, t.LastEpisode, true)
		}
	}
	if bs, err := st.BiliItemsBetween(fromTs, toTs); err != nil {
		errs = append(errs, "B 站: "+err.Error())
	} else {
		for _, b := range bs {
			pt := time.Unix(b.Pubdate, 0).In(from.Location())
			events = append(events, Event{ID: fmt.Sprintf("bili-%d", b.ID), Source: "bili", Title: b.Title,
				Date: ymd(pt), Time: pt.Format("15:04"), RefID: b.ID, Sub: b.UpName})
		}
	}
	if certs, err := st.ListCerts(); err != nil {
		errs = append(errs, "证书: "+err.Error())
	} else {
		for _, c := range certs {
			if c.NotAfter <= 0 {
				continue
			}
			d := ymd(time.Unix(c.NotAfter, 0).In(from.Location()))
			if d >= fromS && d <= toS {
				events = append(events, Event{ID: fmt.Sprintf("cert-%d", c.ID), Source: "cert", Title: c.Domain,
					Date: d, AllDay: true, RefID: c.ID, Sub: "证书到期"})
			}
		}
	}
	if subs, err := st.ListCalendarSubs(); err != nil {
		errs = append(errs, "日历订阅: "+err.Error())
	} else if len(subs) > 0 {
		meta := map[int64]store.CalendarSub{}
		for _, s := range subs {
			meta[s.ID] = s
		}
		if evs, err := st.CalendarEventsBetween(fromTs, toTs); err != nil {
			errs = append(errs, "日历事件: "+err.Error())
		} else {
			for _, e := range evs {
				startAt := time.Unix(e.StartAt, 0).In(from.Location())
				ev := Event{ID: fmt.Sprintf("ics-%d", e.ID), Source: "ics", Title: e.Title, Date: ymd(startAt),
					AllDay: e.AllDay, Color: meta[e.SubID].Color, RefID: e.SubID, Sub: meta[e.SubID].Name, Note: firstLine(e.Description)}
				if !e.AllDay {
					ev.Time = startAt.Format("15:04")
				}
				if e.EndAt > e.StartAt {
					end := time.Unix(e.EndAt, 0).In(from.Location())
					if e.AllDay {
						end = end.Add(-time.Second) // DTEND 不含端点
					}
					if ymd(end) != ev.Date {
						ev.EndDate = ymd(end)
					}
				}
				if e.Location != "" {
					if ev.Note != "" {
						ev.Note += " · "
					}
					ev.Note += e.Location
				}
				events = append(events, ev)
			}
		}
	}

	var marks []DayMark
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		lu := LunarOf(d)
		m := DayMark{Date: ymd(d), LunarMonth: lu.Month, LunarDay: lu.Day, JieQi: lu.JieQi}
		if len(lu.Festivals) > 0 {
			n := minInt(2, len(lu.Festivals))
			m.Festival = strings.Join(lu.Festivals[:n], " ")
		}
		if h, ok := HolidayOf(m.Date); ok {
			hh := h
			m.Holiday = &hh
		}
		marks = append(marks, m)
	}
	var err error
	if len(errs) > 0 {
		err = fmt.Errorf("%s", strings.Join(errs, "；"))
	}
	return events, marks, err
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}
