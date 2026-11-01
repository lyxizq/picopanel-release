package assistant

import (
	"fmt"
	"log"
	"strings"
	"time"

	"lz-monitor/internal/server/inspect"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/weather"
)

// 每日早报：把天气/假期/待办/会员/追剧/B 站/邮件/证书/倒数日各取一行拼成一则，尾部
// 并入巡检段（服务器在线/离线、告警中、磁盘紧张、资源 TOP——原巡检报告每日定时推送
// 已合并至此，inspect 包只保留手动生成/推送）。标题附当日温度区间、降水概率与在线/
// 离线台数，通知栏一眼可见。
// 每段 best-effort——某张表查失败只少一行并落日志，不影响整份早报。

// BuildBrief 生成早报标题与正文（offlineAfter 为离线判定窗口秒数，与服务器列表口径
// 一致；unreadFn 为邮件管理器的未读统计，可为 nil）。
func BuildBrief(st *store.Store, offlineAfter int64, unreadFn func() map[int64]int, now time.Time) (title, body string) {
	today := dateOnly(now)
	ts := ymd(today)
	loc := now.Location()
	var lines []string
	add := func(s string) {
		if strings.TrimSpace(s) != "" {
			lines = append(lines, s)
		}
	}
	var titleParts []string // 标题附加段：天气、服务器台数

	// 巡检数据（服务器/告警/磁盘/邮件明细），失败只少巡检段
	var rep *inspect.Report
	if r, err := inspect.Build(st, offlineAfter, unreadFn); err != nil {
		log.Printf("早报生成巡检段失败: %v", err)
	} else {
		rep = r
	}

	// 日期行：10月26日 周一 · 农历九月初七 · 霜降 · 重阳节
	lu := LunarOf(today)
	date := fmt.Sprintf("%s %s · 农历%s%s", today.Format("1月2日"), weekdayCN(today), lu.Month, lu.Day)
	if lu.JieQi != "" {
		date += " · " + lu.JieQi
	}
	if len(lu.Festivals) > 0 {
		date += " · " + strings.Join(lu.Festivals[:minInt(2, len(lu.Festivals))], " ")
	}
	add(date)
	add(HolidayLine(today))

	// 天气（标题附温度区间与降水概率；无当日预报退现报温度）
	if r := weatherNow(st); r != nil {
		var todayDay string
		for _, d := range r.Daily {
			if d.Date == ts {
				todayDay = fmt.Sprintf("%s %.0f~%.0f℃，降水概率 %d%%", wmoText(d.Code), d.Min, d.Max, d.Precip)
				if d.Snow != "" {
					todayDay += "，" + d.Snow
				}
				titleParts = append(titleParts, fmt.Sprintf("%.0f~%.0f℃ 降水 %d%%", d.Min, d.Max, d.Precip))
				break
			}
		}
		if todayDay == "" && r.Current != nil {
			todayDay = fmt.Sprintf("%s %.0f℃", wmoText(r.Current.Code), r.Current.Temp)
			titleParts = append(titleParts, fmt.Sprintf("%.0f℃", r.Current.Temp))
		}
		w := "天气：" + strings.TrimSpace(r.City+" "+todayDay)
		if r.Air != nil && r.Air.Aqi > 0 {
			cat := r.Air.Category
			if cat == "" {
				cat = aqiCategoryOf(r.Air.Aqi)
			}
			w += fmt.Sprintf("，空气%s（AQI %d）", cat, r.Air.Aqi)
		}
		add(w)
		if len(r.Warnings) > 0 {
			var ts []string
			for _, x := range r.Warnings {
				ts = append(ts, x.Title)
			}
			add("预警：" + strings.Join(dedupe(ts), "；"))
		}
	}

	// 待办：今天到期未完成 + 逾期未完成条数
	if rs, err := st.RemindersDueBetween(ts, ts); err != nil {
		log.Printf("早报读取今日待办失败: %v", err)
	} else {
		var names []string
		for _, r := range rs {
			if r.DoneAt == nil {
				names = append(names, r.Title)
			}
		}
		overdue := 0
		if ov, err := st.RemindersDueBetween(ymd(today.AddDate(-1, 0, 0)), ymd(today.AddDate(0, 0, -1))); err == nil {
			for _, r := range ov {
				if r.DoneAt == nil {
					overdue++
				}
			}
		}
		if len(names) > 0 || overdue > 0 {
			s := fmt.Sprintf("待办：今天 %d 项", len(names))
			if len(names) > 0 {
				s += "——" + joinCap(names, 5)
			}
			if overdue > 0 {
				s += fmt.Sprintf("（另有逾期 %d 项）", overdue)
			}
			add(s)
		}
	}

	// 会员 15 天内到期
	if ms, err := st.ListMemberships(); err != nil {
		log.Printf("早报读取会员失败: %v", err)
	} else {
		var items []string
		for _, m := range ms {
			if m.RemainingDays < 0 || m.RemainingDays > 15 {
				continue
			}
			when := fmt.Sprintf("还有 %d 天", m.RemainingDays)
			if m.RemainingDays == 0 {
				when = "今天到期"
			}
			items = append(items, m.Biz+" "+when)
		}
		if len(items) > 0 {
			add("会员到期：" + joinCap(items, 5))
		}
	}

	// 追剧今日更新
	if tvs, err := st.ListTVFollows(); err != nil {
		log.Printf("早报读取追剧失败: %v", err)
	} else {
		var items []string
		for _, t := range tvs {
			if !t.Enabled {
				continue
			}
			switch {
			case t.NextAirDate == ts && t.NextEpisode > 0:
				items = append(items, fmt.Sprintf("《%s》第 %d 集", t.Name, t.NextEpisode))
			case t.LastAirDate == ts && t.LastEpisode > 0:
				items = append(items, fmt.Sprintf("《%s》第 %d 集", t.Name, t.LastEpisode))
			case t.NextAirDate == ts || t.LastAirDate == ts:
				items = append(items, "《"+t.Name+"》")
			}
		}
		if len(items) > 0 {
			add("追剧更新：" + joinCap(dedupe(items), 5))
		}
	}

	// B 站近 24 小时新集
	if bs, err := st.BiliItemsBetween(now.Add(-24*time.Hour).Unix(), now.Unix()+1); err != nil {
		log.Printf("早报读取 B 站更新失败: %v", err)
	} else if len(bs) > 0 {
		var items []string
		for _, b := range bs {
			items = append(items, b.Title)
		}
		add(fmt.Sprintf("B 站更新 %d 条：%s", len(bs), joinCap(items, 3)))
	}

	// 未读邮件（多账户时附各账户明细，口径同巡检报告）
	if unreadFn != nil {
		total := 0
		for _, n := range unreadFn() {
			if n > 0 {
				total += n
			}
		}
		if total > 0 {
			s := fmt.Sprintf("邮件：未读 %d 封", total)
			if rep != nil && len(rep.MailLines) > 1 {
				s += "——" + joinCap(rep.MailLines, 3)
			}
			add(s)
		}
	}

	// 证书 15 天内到期
	if certs, err := st.ListCerts(); err != nil {
		log.Printf("早报读取证书失败: %v", err)
	} else {
		var items []string
		for _, c := range certs {
			if c.NotAfter <= 0 || c.LastError != "" {
				continue
			}
			days := daysBetween(today, time.Unix(c.NotAfter, 0).In(loc))
			if days < 0 {
				items = append(items, c.Domain+" 已过期")
			} else if days <= 15 {
				items = append(items, fmt.Sprintf("%s 还有 %d 天", c.Domain, days))
			}
		}
		if len(items) > 0 {
			add("证书到期：" + joinCap(items, 5))
		}
	}

	// 倒数日：30 天内即将到来的
	if cs, err := st.ListCountdowns(); err != nil {
		log.Printf("早报读取倒数日失败: %v", err)
	} else if len(cs) > 0 {
		Enrich(cs, today)
		SortForCard(cs)
		var items []string
		for _, c := range cs {
			if c.Kind == "since" || c.Days < 0 || c.Days > 30 || c.NextDate == "" {
				continue
			}
			switch c.Days {
			case 0:
				items = append(items, c.Name+" 就是今天")
			case 1:
				items = append(items, c.Name+" 明天")
			default:
				items = append(items, fmt.Sprintf("%s 还有 %d 天", c.Name, c.Days))
			}
		}
		if len(items) > 0 {
			add("倒数日：" + joinCap(items, 4))
		}
	}

	// 巡检段（原巡检报告每日推送）；一台服务器都没有时整段与标题台数都不出
	if rep != nil && rep.Total > 0 {
		titleParts = append(titleParts, fmt.Sprintf("在线 %d 台 / 离线 %d 台", rep.Online, rep.Offline))
		for _, s := range inspectLines(rep) {
			add(s)
		}
	}

	title = fmt.Sprintf("每日早报 %s %s", today.Format("1月2日"), weekdayCN(today))
	if len(titleParts) > 0 {
		title += " · " + strings.Join(titleParts, " · ")
	}
	return title, strings.Join(lines, "\n")
}

// inspectLines 巡检段：服务器汇总（离线点名）、告警中、磁盘紧张、在线机资源 TOP3
// （标题行后一台一行、带排名序号）。证书临期与未读邮件早报已有同口径行，此处不重复。
func inspectLines(rep *inspect.Report) []string {
	var out []string
	s := fmt.Sprintf("服务器：共 %d 台，在线 %d / 离线 %d", rep.Total, rep.Online, rep.Offline)
	if rep.Disabled > 0 {
		s += fmt.Sprintf(" / 停用 %d", rep.Disabled)
	}
	if rep.Offline > 0 {
		var off []string
		for _, x := range rep.Servers {
			if x.Enabled && !x.Online {
				off = append(off, x.Name)
			}
		}
		s += "，离线：" + joinCap(off, 5)
	}
	out = append(out, s)
	if n := len(rep.FiringAlert); n > 0 {
		out = append(out, fmt.Sprintf("告警中 %d 条：%s", n, joinCap(rep.FiringAlert, 3)))
	}
	if len(rep.DiskWarn) > 0 {
		out = append(out, "磁盘紧张："+joinCap(rep.DiskWarn, 3))
	}
	var top []string
	for _, x := range rep.Servers { // Build 已按离线在前、CPU 降序排好
		if !x.Online {
			continue
		}
		item := fmt.Sprintf("%d. %s CPU %.0f%% / 内存 %.0f%%", len(top)+1, x.Name, x.CPU, x.Mem)
		if x.DiskMount != "" {
			item += fmt.Sprintf(" / 磁盘 %.0f%%", x.Disk)
		}
		if top = append(top, item); len(top) == 3 {
			break
		}
	}
	if len(top) > 0 {
		out = append(out, "资源 TOP：")
		out = append(out, top...)
	}
	return out
}

// BriefNow 生成并投递一份早报（定时与「立即推送」按钮共用）。
func BriefNow(st *store.Store, offlineAfter int64, unreadFn func() map[int64]int, now time.Time) {
	title, body := BuildBrief(st, offlineAfter, unreadFn, now)
	deliver(st, title, body)
}

func aqiCategoryOf(aqi int) string { return weather.AqiCategory(aqi) }

// joinCap 最多列 n 项，其余折成「等 N 项」。
func joinCap(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, "、")
	}
	return strings.Join(items[:n], "、") + fmt.Sprintf(" 等 %d 项", len(items))
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
