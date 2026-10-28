// notify.go 状态翻转出站装配方（500）：服务监控是全站告警体系里最后一个接
// 推送的模块——翻转经 Manager.SetNotify 回调到这里，三渠道各按自己的「告警」
// 开关推送（复用告警渠道，不另设开关，同 492 沿用先例），站内消息始终落库
//（Type=alert，与指标告警同口径、不受渠道开关影响）。一轮内多个端口同时翻转
//（路由/交换机故障场景）由 Manager 合并成一批，这里合成一条通知防刷屏。
package svcmon

import (
	"fmt"
	"log"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

// NotifyFunc 返回注入 Manager.SetNotify 的回调：三渠道按开关推送 + 站内消息。
func NotifyFunc(st *store.Store) func([]Flip) {
	return func(flips []Flip) {
		subject, body, title := flipMessage(flips)
		if wxpusher.On(st) && wxpusher.AlertWXOn(st) {
			if err := wxpusher.SendFromStore(st, subject, body); err != nil {
				log.Printf("服务监控告警 WxPusher 推送失败: %v", err)
			}
		}
		if webhook.On(st) && webhook.AlertOn(st) {
			if err := webhook.SendFromStore(st, subject, body); err != nil {
				log.Printf("服务监控告警 Webhook 推送失败: %v", err)
			}
		}
		if webpush.On(st) && webpush.AlertOn(st) {
			if err := webpush.SendFromStore(st, subject, body); err != nil {
				log.Printf("服务监控告警 Web Push 推送失败: %v", err)
			}
		}
		m := &store.Message{
			Type:      "alert",
			Title:     title,
			Content:   body,
			RefType:   "svcport",
			RefID:     flips[0].PortID,
			CreatedAt: time.Now().Unix(),
		}
		if err := st.CreateMessage(m); err != nil {
			log.Printf("服务监控告警消息入站失败: %v", err)
		}
	}
}

// flipMessage 把一批翻转合成一条通知的 推送标题/正文/站内标题。
// 单条：不通/恢复各一套文案；多条：逐行列出（封顶 20 行，同告警聚合口径）。
func flipMessage(flips []Flip) (subject, body, title string) {
	now := time.Now().Format("2006-01-02 15:04:05")
	if len(flips) == 1 {
		f := flips[0]
		where := f.ServerName
		if where == "" {
			where = f.Host
		}
		head := fmt.Sprintf("%s · %s", where, f.label())
		if f.Up {
			subject = fmt.Sprintf("[PicoPanel] 服务恢复：%s", head)
			title = fmt.Sprintf("服务恢复：%s", head)
			body = fmt.Sprintf("服务：%s（%s）\n目标：%s\n状态：恢复（检测连通）\n延迟：%d ms\n时间：%s\n",
				where, f.Host, f.target(), f.LatencyMs, now)
			return subject, body, title
		}
		reason := f.ErrMsg
		if reason == "" {
			reason = "未知原因"
		}
		subject = fmt.Sprintf("[PicoPanel] 服务不通：%s", head)
		title = fmt.Sprintf("服务告警：%s 不通", head)
		body = fmt.Sprintf("服务：%s（%s）\n目标：%s\n状态：不通\n原因：%s\n时间：%s\n",
			where, f.Host, f.target(), reason, now)
		return subject, body, title
	}
	nDown := 0
	var lines strings.Builder
	for i, f := range flips {
		if !f.Up {
			nDown++
		}
		if i >= 20 {
			fmt.Fprintf(&lines, "… 其余 %d 项略\n", len(flips)-i)
			break
		}
		state := "恢复"
		if !f.Up {
			state = "不通"
		}
		where := f.ServerName
		if where == "" {
			where = f.Host
		}
		fmt.Fprintf(&lines, "· %s %s %s", where, f.label(), state)
		if !f.Up && f.ErrMsg != "" {
			fmt.Fprintf(&lines, "（%s）", f.ErrMsg)
		}
		lines.WriteString("\n")
	}
	subject = fmt.Sprintf("[PicoPanel] 服务监控：%d 项状态变化（%d 项不通）", len(flips), nDown)
	title = fmt.Sprintf("服务状态变化 %d 项（%d 项不通）", len(flips), nDown)
	body = fmt.Sprintf("共 %d 项状态变化：\n%s时间：%s\n", len(flips), lines.String(), now)
	return subject, body, title
}
