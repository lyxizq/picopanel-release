// Package medication 实现用药提醒的调度推送（2026-11-02，参考 Apple Health 药物，
// 分家庭成员多账号）。挂在 reminder.Run 同一循环里每分钟 tick 一次，四个动作全幂等：
//
//  1. 日结生成：为当天预建 pending 剂量行（唯一索引兜底幂等），并把昨天及以前
//     仍 pending 的批量改 missed（漏服）；
//  2. 到点推送：已到时刻未推过的 pending 推一次（notified_at 打标，站内落库成功
//     才打标，失败下轮重试不轰炸）；同刻多药合并——站内消息按归属拆分（成员只看
//     自己的），三渠道家庭广播整组合并成一条（2026-11-03 用户指定）；
//  3. 漏服催推：到点 60 分钟后仍未打卡再催一次（escalated_at 打标，只催一次）；
//  4. 低库存：跌破警戒线的药各推一次补货提醒（stock_notified_at 打标，手动改
//     库存即重置）。
//
// 站内消息 Type/RefType=medication（不复用 reminder：消息中心红字联查按
// reminders.done_at 查，混入会永红——会员模块同款避坑）；三渠道沿用「提醒事项」
// 推送细分开关（ReminderWXOn/ReminderOn 家族）。
package medication

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

// Sweep 调度单轮（由 reminder.Run 每分钟调一次）。
func Sweep(st *store.Store) {
	now := time.Now()
	today, hhmm := now.Format("2006-01-02"), now.Format("15:04")
	if err := st.EnsureMedDay(today); err != nil {
		log.Printf("用药日结生成失败: %v", err)
		return
	}
	if _, err := st.RollMissedMedLogs(today); err != nil {
		log.Printf("用药漏服日结失败: %v", err)
	}
	sweepDoses(st, today, hhmm, now.Unix())
	sweepLowStock(st, now.Unix())
}

// sweepDoses 到点推送 + 漏服催推（两段各自打标）。
func sweepDoses(st *store.Store, today, hhmm string, now int64) {
	if due, err := st.DueMedLogs(today, hhmm); err != nil {
		log.Printf("用药到点扫描失败: %v", err)
	} else {
		for _, grp := range groupBySlot(due) {
			emitDoseGroup(st, now, grp, false)
		}
	}
	if late, err := st.EscalatableMedLogs(today, hhmmBefore(hhmm)); err != nil {
		log.Printf("用药漏服扫描失败: %v", err)
	} else {
		for _, grp := range groupBySlot(late) {
			emitDoseGroup(st, now, grp, true)
		}
	}
}

// hhmmBefore 当前时刻回拨 1 小时的 HH:MM（字符串比较即钟点比较）——漏服催推
// 窗口用：计划时刻 ≤ now-1h 即「到点已超 60 分钟」。2026-11-03 修：原实现
// 推后 1 小时（hhmmAfter），比较方向写反会把一小时后才到点的剂量提前判漏服。
// 解析失败/跨天回绕（0 点后回拨到前一天）返回恒不命中的 "99:99"——零点后一
// 小时内的催推交给次日漏服日结。
func hhmmBefore(hhmm string) string {
	t, err := time.ParseInLocation("15:04", hhmm, time.Local)
	if err != nil {
		return "99:99"
	}
	before := t.Add(-time.Hour)
	if before.Day() != t.Day() {
		return "99:99"
	}
	return before.Format("15:04")
}

// groupBySlot 按计划时刻分组（同刻剂量合并一条推送），组序保持首现顺序。
func groupBySlot(list []store.MedDose) [][]store.MedDose {
	index := map[string]int{}
	var groups [][]store.MedDose
	for _, d := range list {
		if i, ok := index[d.Time]; ok {
			groups[i] = append(groups[i], d)
		} else {
			index[d.Time] = len(groups)
			groups = append(groups, []store.MedDose{d})
		}
	}
	return groups
}

// doseLabel 单剂量展示（人名 · 药名）。
func doseLabel(d *store.MedDose) string {
	return d.PersonName + " · " + d.MedName
}

// doseDesc 剂量描述（数量+单位，单位空只报数量）。
func doseDesc(d *store.MedDose) string {
	if d.DoseUnit == "" {
		return fmtFloat(d.DoseAmount)
	}
	return fmtFloat(d.DoseAmount) + " " + d.DoseUnit
}

// fmtFloat 库存/剂量的紧凑数字（1.0→1、0.5→0.5）。
func fmtFloat(q float64) string {
	return strconv.FormatFloat(q, 'f', -1, 64)
}

// emitDoseGroup 推送一组同刻剂量。站内消息按人员绑定的登录账号（PersonUserID）
// 拆分子组各自落库（2026-11-03 家庭成员口径：成员只在自己空间看到自己的提醒，
// 未绑定=0 落管理员空间）；三渠道家庭广播**整组合并成一条**（2026-11-03 用户指定：
// 同一时间多重药物只推一条，不再按归属拆成多条轰炸渠道）。late=true 为漏服催推段。
func emitDoseGroup(st *store.Store, now int64, grp []store.MedDose, late bool) {
	allOK := true
	for _, sub := range splitByOwner(grp) {
		title, content := doseTexts(sub, late)
		m := &store.Message{Type: "medication", Title: title, Content: content,
			UserID: sub[0].PersonUserID, CreatedAt: now}
		if len(sub) == 1 {
			// RefID=med_logs 行 ID（2026-11-03 提醒窗口直达完成：消息/Web Push
			// 通知的「完成」按钮按此 ID 走 /meds/log-complete 打卡）
			m.RefType, m.RefID = "medication", sub[0].ID
		} else {
			// 同人多剂量合并：refs 带各行 ID，详情弹层逐项完成（防重复点同款）
			m.Refs = make([]int64, 0, len(sub))
			for i := range sub {
				m.Refs = append(m.Refs, sub[i].ID)
			}
		}
		if err := st.CreateMessage(m); err != nil {
			// 消息未落库不打标，下轮重试该子组（不轰炸口径）；本轮渠道推送一并
			// 跳过——避免半组内容先行广播、下轮又补推造成双重打扰
			log.Printf("用药消息写入失败 (med=%d): %v", sub[0].MedID, err)
			allOK = false
			continue
		}
		for i := range sub {
			var err error
			if late {
				err = st.MarkMedLogEscalated(sub[i].ID, now)
			} else {
				err = st.MarkMedLogNotified(sub[i].ID, now)
			}
			if err != nil {
				log.Printf("用药推送标记失败 (log=%d): %v", sub[i].ID, err)
			}
		}
	}
	if allOK {
		title, content := doseTexts(grp, late)
		if len(grp) == 1 {
			pushChannelsDone(st, title, content, "med", grp[0].ID, "/console/messages?type=medication")
		} else {
			// 合并推送无单一 ref（无完成按钮），点通知主体同样落消息中心用药页签
			pushChannelsDone(st, title, content, "", 0, "/console/messages?type=medication")
		}
	}
}

// splitByOwner 按剂量所属人员的绑定账号分组（组序保持首现顺序）——同刻跨人员
// 的合并剂量拆开各自落消息，保证成员只在自己空间看到自己的提醒。
func splitByOwner(grp []store.MedDose) [][]store.MedDose {
	index := map[int64]int{}
	var groups [][]store.MedDose
	for _, d := range grp {
		if i, ok := index[d.PersonUserID]; ok {
			groups[i] = append(groups[i], d)
		} else {
			index[d.PersonUserID] = len(groups)
			groups = append(groups, []store.MedDose{d})
		}
	}
	return groups
}

// doseTexts 一组同刻剂量的标题与正文：单条带人名药名形态，多条合并成计数+清单
// （558 合并口径）。子组（按归属拆分后）与整组（渠道合并推送）共用。
func doseTexts(grp []store.MedDose, late bool) (title, content string) {
	first := &grp[0]
	if late {
		if len(grp) == 1 {
			return "可能漏服：" + doseLabel(first),
				fmt.Sprintf("%s 的「%s」（%s）已过 60 分钟仍未打卡，记得确认是否漏服。",
					first.PersonName, first.MedName, first.Time)
		}
		title = fmt.Sprintf("可能漏服：%d 个剂量未打卡", len(grp))
		content = fmt.Sprintf("以下 %d 个剂量已过 60 分钟仍未打卡，记得确认：", len(grp))
		for i := range grp {
			content += fmt.Sprintf("\n· %s（%s）", doseLabel(&grp[i]), grp[i].Time)
		}
		return title, content
	}
	if len(grp) == 1 {
		title = "用药提醒：" + doseLabel(first)
		content = fmt.Sprintf("%s 的「%s」已到服药时间（%s），剂量 %s。",
			first.PersonName, first.MedName, first.Time, doseDesc(first))
		if first.Note != "" {
			content += "\n备注：" + first.Note
		}
		return title, content
	}
	title = fmt.Sprintf("用药提醒：%d 个剂量到点", len(grp))
	content = fmt.Sprintf("以下 %d 个剂量已到服药时间：", len(grp))
	for i := range grp {
		content += fmt.Sprintf("\n· %s（%s，剂量 %s）", doseLabel(&grp[i]), grp[i].Time, doseDesc(&grp[i]))
	}
	return title, content
}

// sweepLowStock 低库存补货提醒：每味进线药物各推一次（阈值各异不合并不打标复用）。
func sweepLowStock(st *store.Store, now int64) {
	list, err := st.LowStockMeds()
	if err != nil {
		log.Printf("用药低库存扫描失败: %v", err)
		return
	}
	for i := range list {
		m := &list[i]
		qty, alert := 0.0, m.StockAlert
		if m.StockQty != nil {
			qty = *m.StockQty
		}
		subject := "补货提醒：" + m.PersonName + " · " + m.Name
		body := fmt.Sprintf("%s 的「%s」库存已剩 %s（警戒线 %s），记得补货。",
			m.PersonName, m.Name, fmtFloat(qty), fmtFloat(alert))
		msg := &store.Message{Type: "medication", Title: subject, Content: body,
			RefType: "medication", RefID: m.ID, UserID: m.PersonUserID, CreatedAt: now} // 归属=人员绑定空间（2026-11-03；未绑定=0 管理员）
		if err := st.CreateMessage(msg); err != nil {
			log.Printf("补货消息写入失败 (med=%d): %v", m.ID, err)
			continue
		}
		pushChannels(st, subject, body)
		if err := st.MarkMedStockNotified(m.ID, now); err != nil {
			log.Printf("补货提醒标记失败 (med=%d): %v", m.ID, err)
		}
	}
}

// pushChannels 按各渠道总开关 + 提醒事项细分开关分发（reminder 同款口径：开关
// 每次现读即时生效；站内消息已在调用前落库，渠道失败只记日志）。
func pushChannels(st *store.Store, subject, body string) {
	pushChannelsDone(st, subject, body, "", 0, "")
}

// pushChannelsDone 同 pushChannels；kind/ref 非空时 Web Push 通道 payload 附加
// done 交互数据（SW 通知「完成」按钮直达打卡；WxPusher/Webhook 无通知交互能力
// 不带，照常发文本）；url 非空时点通知主体直达该页（iOS 无按钮场景）。
func pushChannelsDone(st *store.Store, subject, body, kind string, ref int64, url string) {
	if wxpusher.On(st) && wxpusher.ReminderWXOn(st) {
		if err := wxpusher.SendFromStore(st, subject, body); err != nil {
			log.Printf("用药 WxPusher 推送失败: %v", err)
		}
	}
	if webhook.On(st) && webhook.ReminderOn(st) {
		if err := webhook.SendFromStore(st, subject, body); err != nil {
			log.Printf("用药 Webhook 推送失败: %v", err)
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
			log.Printf("用药 Web Push 推送失败: %v", err)
		}
	}
}
