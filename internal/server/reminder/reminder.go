// Package reminder 实现提醒事项的三段到期推送（站内消息，类型=提醒）：
// 到期前 1 小时提醒一次、到期时提醒一次、到期超过一天仍未完成再最后提醒
// 一次（之后不再提醒）。每段各自打标（notify_1h_at/notify_due_at/
// notify_nextday_at）保证只发一次；完成事项（done_at 非空）自然退出所有段。
// 消息与提醒事项经 ref_id 关联，事项完成后消息中心红字自动复原。
//
// 会员到期提醒（492）也挂在本调度循环：到期日前 15 天内推一次
// （notify_15d_at 打标，改到期日重置），站内消息类型=membership。
// 用药提醒（2026-11-02）同挂本循环：日结生成/到点推送/漏服催推/低库存四动作
// 每分钟扫一遍（medication.Sweep）。
package reminder

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"lz-monitor/internal/server/medication"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

const (
	tick       = time.Minute
	hour       = int64(3600)
	day        = int64(86400)
	segLabel1h = "1h"
	segDue     = "due"
	segNextday = "nextday"
)

// Run 阻塞循环：启动先跑一轮，之后每 1 分钟扫一遍三段窗口（用药提醒 Sweep 同轮）。
func Run(ctx context.Context, st *store.Store) {
	sweep(st)
	sweepMemberships(st)
	sweepHouseLeases(st)
	sweepHouseRents(st)
	medication.Sweep(st)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep(st)
			sweepMemberships(st)
			sweepHouseLeases(st)
			sweepHouseRents(st)
			medication.Sweep(st)
		}
	}
}

// segDef 一段推送窗口的定义：title/body 为单条形态的文案（多组共用），
// groupTitle/groupBody 为同刻多条合并成一条时的文案（567，照 558 合并口径）。
type segDef struct {
	kind       string
	from, to   int64
	title      func(r *store.Reminder) string
	body       func(r *store.Reminder) string
	groupTitle func(n int) string
	groupBody  func(first *store.Reminder, n int, now int64) string
}

// sweep 扫三段窗口并发消息。窗口语义（错过补推但有限度，服务重启不轰炸）：
// 前 1 小时段 = 到期时刻落在 [now, now+1h]；到期段 = [now-1h, now]；
// 次日段 = 到期后 24~48 小时之间仍未完成。
// 同窗内「同一天同一时间」到期的多条事项合并成一条消息推送（567）。
func sweep(st *store.Store) {
	now := time.Now().Unix()
	segments := []segDef{
		{
			kind: segLabel1h, from: now, to: now + hour,
			title: func(r *store.Reminder) string { return "提醒：" + r.Title },
			body: func(r *store.Reminder) string {
				return fmt.Sprintf("「%s」将于 %s %s 到期（约 %d 分钟后），别忘记了。", r.Title, r.DueDate, r.EffectiveDueTime(),
					(r.DueUnix()-now)/60+1)
			},
			groupTitle: func(n int) string { return fmt.Sprintf("提醒：%d 条事项即将到期", n) },
			groupBody: func(first *store.Reminder, n int, now int64) string {
				return fmt.Sprintf("以下 %d 条事项将于 %s %s 到期（约 %d 分钟后），别忘记了：", n, first.DueDate, first.EffectiveDueTime(),
					(first.DueUnix()-now)/60+1)
			},
		},
		{
			kind: segDue, from: now - hour, to: now,
			title: func(r *store.Reminder) string { return "提醒：" + r.Title },
			body: func(r *store.Reminder) string {
				return fmt.Sprintf("「%s」已到到期时间（%s %s），记得处理。", r.Title, r.DueDate, r.EffectiveDueTime())
			},
			groupTitle: func(n int) string { return fmt.Sprintf("提醒：%d 条事项已到到期时间", n) },
			groupBody: func(first *store.Reminder, n int, now int64) string {
				return fmt.Sprintf("以下 %d 条事项已到到期时间（%s %s），记得处理：", n, first.DueDate, first.EffectiveDueTime())
			},
		},
		{
			kind: segNextday, from: now - 2*day, to: now - day,
			title: func(r *store.Reminder) string { return "提醒：" + r.Title },
			body: func(r *store.Reminder) string {
				return fmt.Sprintf("「%s」（%s %s 到期）已超过一天仍未完成，这是最后一次提醒。", r.Title, r.DueDate, r.EffectiveDueTime())
			},
			groupTitle: func(n int) string { return fmt.Sprintf("提醒：%d 条事项超期未完成", n) },
			groupBody: func(first *store.Reminder, n int, now int64) string {
				return fmt.Sprintf("以下 %d 条事项（%s %s 到期）已超过一天仍未完成，这是最后一次提醒：", n, first.DueDate, first.EffectiveDueTime())
			},
		},
	}
	for _, seg := range segments {
		list, err := st.DueRemindersWindow(seg.from, seg.to, seg.kind)
		if err != nil {
			log.Printf("提醒推送扫描失败 (%s): %v", seg.kind, err)
			return
		}
		for _, grp := range groupByDueSlot(list) {
			emitGroup(st, now, &seg, grp)
		}
	}
}

// groupByDueSlot 按「同一天同一时间」（到期日 + 到期时刻）分组，组序保持
// 窗口扫描的首现顺序；单条组走原形态，多条组合并成一条消息。
// 合并只在同一数据空间内进行（2026-11-03 家庭成员口径：不同成员的事项
// 不并进同一条消息，避免互相泄露）。
func groupByDueSlot(list []store.Reminder) [][]store.Reminder {
	index := map[string]int{}
	var groups [][]store.Reminder
	for _, r := range list {
		key := fmt.Sprintf("%d|%s %s", r.UserID, r.DueDate, r.EffectiveDueTime())
		if i, ok := index[key]; ok {
			groups[i] = append(groups[i], r)
		} else {
			index[key] = len(groups)
			groups = append(groups, []store.Reminder{r})
		}
	}
	return groups
}

// emitGroup 推送一组同刻提醒：单条保持原形态（消息带 ref_id 关联，事项完成
// 后消息中心红字复原）；多条合并成一条消息（标题计数控、正文首行汇总+逐行
// 「· 标题」），不带单条 ref 关联（558 同款），三渠道只推一次，事项逐条打标。
// 站内消息按事项归属空间打 UserID（2026-11-03 家庭成员口径：admin 数据→0 全局，
// 成员数据→其个人消息）。
func emitGroup(st *store.Store, now int64, seg *segDef, grp []store.Reminder) {
	first := &grp[0]
	title, content := seg.title(first), seg.body(first)
	if first.Note != "" {
		content += "\n备注：" + first.Note
	}
	m := &store.Message{Type: "reminder", Title: title, Content: content,
		RefType: "reminder", RefID: first.ID, UserID: first.UserID, CreatedAt: now}
	if len(grp) > 1 {
		m.RefType, m.RefID = "", 0
		m.Refs = make([]int64, 0, len(grp))
		for i := range grp {
			m.Refs = append(m.Refs, grp[i].ID)
		}
		m.Title = seg.groupTitle(len(grp))
		m.Content = seg.groupBody(first, len(grp), now)
		for i := range grp {
			line := "· " + grp[i].Title
			if grp[i].Note != "" {
				line += "（备注：" + grp[i].Note + "）"
			}
			m.Content += "\n" + line
		}
	}
	if err := st.CreateMessage(m); err != nil {
		log.Printf("提醒消息写入失败 (reminder=%d): %v", first.ID, err)
		return
	}
	if len(grp) == 1 {
		// 单条：Web Push 通知附「完成」action（2026-11-03 提醒窗口直达完成，
		// 走既有 /reminders/:id/complete）；url 直达提醒事项页（iOS 无按钮场景）
		pushChannelsDone(st, m.Title, m.Content, "rem", first.ID, "/console/reminders")
	} else {
		pushChannelsDone(st, m.Title, m.Content, "", 0, "/console/reminders")
	}
	// 打标在消息写入之后：失败下轮重发风险可接受，反过来会丢提醒
	for i := range grp {
		if err := st.MarkReminderNotified(grp[i].ID, seg.kind, now); err != nil {
			log.Printf("提醒段标记失败 (reminder=%d %s): %v", grp[i].ID, seg.kind, err)
		}
	}
}

// pushChannels 按各渠道总开关 + 事项提醒细分开关分发（开关每次现读即时生效；
// 站内消息已在调用前落库，渠道失败只记日志不影响提醒标记）。
func pushChannels(st *store.Store, subject, body string) {
	pushChannelsDone(st, subject, body, "", 0, "")
}

// pushChannelsDone 同 pushChannels；kind/ref 非空时 Web Push 通道 payload 附加
// done 交互数据（SW 通知「完成」按钮直达完成）；url 非空时点通知主体直达该页
// （iOS 无按钮场景）。WxPusher/Webhook 无通知交互能力不带，照常发文本。
func pushChannelsDone(st *store.Store, subject, body, kind string, ref int64, url string) {
	if wxpusher.On(st) && wxpusher.ReminderWXOn(st) {
		if err := wxpusher.SendFromStore(st, subject, body); err != nil {
			log.Printf("提醒事项 WxPusher 推送失败: %v", err)
		}
	}
	if webhook.On(st) && webhook.ReminderOn(st) {
		if err := webhook.SendFromStore(st, subject, body); err != nil {
			log.Printf("提醒事项 Webhook 推送失败: %v", err)
		}
	}
	if webpush.On(st) && webpush.ReminderOn(st) {
		var err error
		if kind != "" || url != "" {
			err = webpush.SendFromStoreAction(st, subject, body, kind, ref, url)
		} else {
			err = webpush.SendFromStore(st, subject, body)
		}
		if err != nil {
			log.Printf("提醒事项 Web Push 推送失败: %v", err)
		}
	}
}

// sweepMemberships 会员到期 15 天提醒（492）：进窗（到期日 ≤ 今天+15，含已过期）
// 且未发过的记录各推一次；站内消息类型 membership（不入 reminder 红字联查），
// 三渠道沿用「提醒事项」推送开关。发送落库成功才打标，失败下轮重试；
// 到期日改动会重置标记，续期后新日期进窗能再次提醒。
func sweepMemberships(st *store.Store) {
	list, err := st.DueExpiringMemberships()
	if err != nil {
		log.Printf("会员到期提醒扫描失败: %v", err)
		return
	}
	now := time.Now().Unix()
	for i := range list {
		m := &list[i]
		var body string
		if m.RemainingDays >= 0 {
			body = fmt.Sprintf("「%s · %s」将于 %d 天后（%s）到期，记得续费。", m.Biz, m.Name, m.RemainingDays, m.ExpireDate)
		} else {
			body = fmt.Sprintf("「%s · %s」已过期 %d 天（%s 到期）。", m.Biz, m.Name, -m.RemainingDays, m.ExpireDate)
		}
		subject := "到期提醒：" + m.Biz
		msg := &store.Message{
			Type: "membership", Title: subject, Content: body,
			RefType: "membership", RefID: m.ID, UserID: m.UserID, CreatedAt: now,
		}
		if err := st.CreateMessage(msg); err != nil {
			log.Printf("会员到期消息写入失败 (membership=%d): %v", m.ID, err)
			continue
		}
		pushChannels(st, subject, body)
		if err := st.MarkMembershipNotified(m.ID, now); err != nil {
			log.Printf("会员到期提醒标记失败 (membership=%d): %v", m.ID, err)
		}
	}
}

// sweepHouseLeases 租约到期 30 天提醒（房屋管理 2026-11-03）：在租租约进窗
//（到期日 ≤ 今天+30，含已过期未退租的）且未发过的各推一次；站内消息类型
// house（不入 reminder 红字联查），三渠道沿用「提醒事项」推送开关，点通知
// 主体直达房屋管理页。发送落库成功才打标；到期日改动重置标记（续租改期后
// 重新武装），退租后状态出窗自然静默。
func sweepHouseLeases(st *store.Store) {
	list, err := st.DueExpiringLeases()
	if err != nil {
		log.Printf("租约到期提醒扫描失败: %v", err)
		return
	}
	now := time.Now().Unix()
	for i := range list {
		l := &list[i]
		var body string
		if l.RemainingDays >= 0 {
			body = fmt.Sprintf("「%s」租客 %s 的租约将于 %d 天后（%s）到期，记得续签或安排退租。", l.HouseName, l.TenantName, l.RemainingDays, l.EndDate)
		} else {
			body = fmt.Sprintf("「%s」租客 %s 的租约已到期 %d 天（%s 到期），请确认续签或退租。", l.HouseName, l.TenantName, -l.RemainingDays, l.EndDate)
		}
		subject := "租约到期提醒：" + l.HouseName
		msg := &store.Message{
			Type: "house", Title: subject, Content: body,
			RefType: "house_lease", RefID: l.ID, UserID: 0, CreatedAt: now,
		}
		if err := st.CreateMessage(msg); err != nil {
			log.Printf("租约到期消息写入失败 (lease=%d): %v", l.ID, err)
			continue
		}
		pushChannelsDone(st, subject, body, "", 0, "/console/houses")
		if err := st.MarkLeaseNotified(l.ID, now); err != nil {
			log.Printf("租约到期提醒标记失败 (lease=%d): %v", l.ID, err)
		}
	}
}

// sweepHouseRents 收租提醒（房屋管理 2026-11-04）：提醒逻辑同事项提醒三段制
// ——到期时刻前 1 小时、到期时（错过补推）、超期 24~48h 最后一次，各段独立
// 打标只发一次；期次按月滚动（rent_notify_month 换月重置打标，改收租安排
// 同样重置），收租日大于当月天数收敛到当月最后一日；同一到期时刻的多套房
// 合并成一条消息（567 口径）。仅提醒有在租租约的房屋（无租可收不推）；站内
// 消息类型 house（RefType house_rent，不入 reminder 红字联查），三渠道沿用
// 「提醒事项」推送开关。
func sweepHouseRents(st *store.Store) {
	list, err := st.RentDueHouses()
	if err != nil {
		log.Printf("收租提醒扫描失败: %v", err)
		return
	}
	now := time.Now()
	nowU := now.Unix()
	type rentHit struct {
		r   store.HouseRentDue
		occ time.Time
	}
	// 换月滚动重置先于各段窗口统一做一轮（不能放进窗口循环：窗口间共用同一份
	// list 快照，后一个窗口会拿陈旧打标把前一个窗口刚写的标记又清掉 → 重发）
	prepared := make([]rentHit, 0, len(list))
	for _, r := range list {
		occ, month := store.HouseRentOccurrence(r.DueDay, r.DueTime, now)
		if r.Month != month {
			// 换月滚动：清打标重新武装（幂等，扫到即写一次生效）
			if err := st.ResetHouseRentFlags(r.HouseID, month); err != nil {
				log.Printf("收租提醒打标重置失败 (house=%d): %v", r.HouseID, err)
				continue
			}
			r.Month = month
			r.N1h, r.NDue, r.NNextday = nil, nil, nil
		}
		prepared = append(prepared, rentHit{r, occ})
	}
	windows := []struct {
		seg   string
		match func(occ time.Time) bool
	}{
		{"1h", func(occ time.Time) bool { return occ.After(now) && occ.Sub(now) <= time.Hour }},
		{"due", func(occ time.Time) bool { return !occ.After(now) && now.Sub(occ) <= time.Hour }},
		{"nextday", func(occ time.Time) bool { d := now.Sub(occ); return d >= 24*time.Hour && d <= 48*time.Hour }},
	}
	segFlag := func(r store.HouseRentDue, seg string) *int64 {
		if seg == "1h" {
			return r.N1h
		}
		if seg == "due" {
			return r.NDue
		}
		return r.NNextday
	}
	rentStr := func(v float64) string { return fmt.Sprintf("%g", v) }
	for _, w := range windows {
		var hits []rentHit
		for _, hit := range prepared {
			if !w.match(hit.occ) || segFlag(hit.r, w.seg) != nil {
				continue
			}
			hits = append(hits, hit)
		}
		if len(hits) == 0 {
			continue
		}
		// 同刻（分钟粒度）分组合并；排序保证分组稳定与消息行序稳定
		sort.Slice(hits, func(i, j int) bool {
			ki, kj := hits[i].occ.Format("2006-01-02 15:04"), hits[j].occ.Format("2006-01-02 15:04")
			if ki != kj {
				return ki < kj
			}
			return hits[i].r.HouseID < hits[j].r.HouseID
		})
		for start := 0; start < len(hits); {
			end := start + 1
			key := hits[start].occ.Format("2006-01-02 15:04")
			for end < len(hits) && hits[end].occ.Format("2006-01-02 15:04") == key {
				end++
			}
			grp := hits[start:end]
			start = end
			var subject, body string
			refID := grp[0].r.HouseID
			if len(grp) == 1 {
				h := grp[0].r
				occ := grp[0].occ
				subject = "收租提醒：" + h.Name
				switch w.seg {
				case "1h":
					body = fmt.Sprintf("「%s」将于 1 小时内（%s）到期收租 %s 元（租客 %s），记得收租。", h.Name, occ.Format("1月2日 15:04"), rentStr(h.MonthRent), h.Tenant)
				case "due":
					body = fmt.Sprintf("「%s」今日（%s）应收租金 %s 元（租客 %s），记得收租。", h.Name, occ.Format("1月2日 15:04"), rentStr(h.MonthRent), h.Tenant)
				default:
					body = fmt.Sprintf("「%s」%s 应收的租金 %s 元（租客 %s）尚未收讫，请确认。", h.Name, occ.Format("1月2日 15:04"), rentStr(h.MonthRent), h.Tenant)
				}
			} else {
				refID = 0
				occ := grp[0].occ
				head := map[string]string{
					"1h":      fmt.Sprintf("以下 %d 套房屋将于 1 小时内（%s）到期收租：", len(grp), occ.Format("1月2日 15:04")),
					"due":     fmt.Sprintf("以下 %d 套房屋今日（%s）到期收租：", len(grp), occ.Format("1月2日 15:04")),
					"nextday": fmt.Sprintf("以下 %d 套房屋 %s 到期收租尚未收讫：", len(grp), occ.Format("1月2日 15:04")),
				}[w.seg]
				subject = fmt.Sprintf("收租提醒：%d 套房屋", len(grp))
				body = head
				for _, hit := range grp {
					body += fmt.Sprintf("\n· %s %s 元（租客 %s）", hit.r.Name, rentStr(hit.r.MonthRent), hit.r.Tenant)
				}
			}
			msg := &store.Message{
				Type: "house", Title: subject, Content: body,
				RefType: "house_rent", RefID: refID, UserID: 0, CreatedAt: nowU,
			}
			if err := st.CreateMessage(msg); err != nil {
				log.Printf("收租提醒消息写入失败 (house=%d): %v", grp[0].r.HouseID, err)
				continue // 消息写入失败不打标，下轮重试
			}
			// 与租约到期同口径：Web Push 附 url，点通知主体直达房屋管理页
			pushChannelsDone(st, subject, body, "", 0, "/console/houses")
			for _, hit := range grp {
				if err := st.MarkHouseRentNotified(hit.r.HouseID, hit.r.Month, w.seg, nowU); err != nil {
					log.Printf("收租提醒标记失败 (house=%d): %v", hit.r.HouseID, err)
				}
			}
		}
	}
}
