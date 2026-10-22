// Package reminder 实现提醒事项的三段到期推送（站内消息，类型=提醒）：
// 到期前 1 小时提醒一次、到期时提醒一次、到期超过一天仍未完成再最后提醒
// 一次（之后不再提醒）。每段各自打标（notify_1h_at/notify_due_at/
// notify_nextday_at）保证只发一次；完成事项（done_at 非空）自然退出所有段。
// 消息与提醒事项经 ref_id 关联，事项完成后消息中心红字自动复原。
//
// 会员到期提醒（492）也挂在本调度循环：到期日前 15 天内推一次
// （notify_15d_at 打标，改到期日重置），站内消息类型=membership。
package reminder

import (
	"context"
	"fmt"
	"log"
	"time"

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

// Run 阻塞循环：启动先跑一轮，之后每 1 分钟扫一遍三段窗口。
func Run(ctx context.Context, st *store.Store) {
	sweep(st)
	sweepMemberships(st)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep(st)
			sweepMemberships(st)
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
func groupByDueSlot(list []store.Reminder) [][]store.Reminder {
	index := map[string]int{}
	var groups [][]store.Reminder
	for _, r := range list {
		key := r.DueDate + " " + r.EffectiveDueTime()
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
func emitGroup(st *store.Store, now int64, seg *segDef, grp []store.Reminder) {
	first := &grp[0]
	title, content := seg.title(first), seg.body(first)
	if first.Note != "" {
		content += "\n备注：" + first.Note
	}
	m := &store.Message{Type: "reminder", Title: title, Content: content,
		RefType: "reminder", RefID: first.ID, CreatedAt: now}
	if len(grp) > 1 {
		m.RefType, m.RefID = "", 0
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
	pushChannels(st, m.Title, m.Content)
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
		if err := webpush.SendFromStore(st, subject, body); err != nil {
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
			RefType: "membership", RefID: m.ID, CreatedAt: now,
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
