// notify.go 状态翻转出站装配方：网站检查是告警体系新成员——翻转经 Manager
// 回调到这里，三渠道各按自己的「告警」开关推送（复用告警渠道不另设开关，同
// svcmon 翻转告警口径），站内消息始终落库（Type=alert，RefType=webcheck，不受
// 渠道开关影响）。一轮内多个目标同时翻转合成一条通知防刷屏；只有失败→恢复也
// 通知（用户只知道「修好了」），未失败不报平安。
package webcheck

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
				log.Printf("网站检查告警 WxPusher 推送失败: %v", err)
			}
		}
		if webhook.On(st) && webhook.AlertOn(st) {
			if err := webhook.SendFromStore(st, subject, body); err != nil {
				log.Printf("网站检查告警 Webhook 推送失败: %v", err)
			}
		}
		if webpush.On(st) && webpush.AlertOn(st) {
			if err := webpush.SendFromStore(st, subject, body); err != nil {
				log.Printf("网站检查告警 Web Push 推送失败: %v", err)
			}
		}
		m := &store.Message{
			Type:      "alert",
			Title:     title,
			Content:   body,
			RefType:   "webcheck",
			RefID:     flips[0].ID,
			CreatedAt: time.Now().Unix(),
		}
		if err := st.CreateMessage(m); err != nil {
			log.Printf("网站检查告警消息入站失败: %v", err)
		}
	}
}

// flipMessage 把一批翻转合成一条通知的 推送标题/正文/站内标题。
// 单条：失败/恢复各一套文案；多条：逐行列出（封顶 20 行，同告警聚合口径）。
func flipMessage(flips []Flip) (subject, body, title string) {
	now := time.Now().Format("2006-01-02 15:04:05")
	if len(flips) == 1 {
		f := flips[0]
		name := f.Name
		if name == "" {
			name = f.URL
		}
		if f.Up {
			subject = fmt.Sprintf("[PicoPanel] 网站检查恢复：%s", name)
			title = fmt.Sprintf("网站检查恢复：%s", name)
			body = fmt.Sprintf("目标：%s\n地址：%s\n状态：恢复（页面包含全部关键字）\n时间：%s\n", name, f.URL, now)
			return subject, body, title
		}
		reason := f.ErrMsg
		if reason == "" {
			reason = "未知原因"
		}
		subject = fmt.Sprintf("[PicoPanel] 网站检查异常：%s", name)
		title = fmt.Sprintf("网站检查异常：%s", name)
		body = fmt.Sprintf("目标：%s\n地址：%s\n状态：检查未通过\n原因：%s\n时间：%s\n", name, f.URL, reason, now)
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
		name := f.Name
		if name == "" {
			name = f.URL
		}
		state := "恢复"
		if !f.Up {
			state = "异常"
		}
		fmt.Fprintf(&lines, "· %s（%s）%s", name, f.URL, state)
		if !f.Up && f.ErrMsg != "" {
			fmt.Fprintf(&lines, "：%s", f.ErrMsg)
		}
		lines.WriteString("\n")
	}
	subject = fmt.Sprintf("[PicoPanel] 网站检查：%d 项状态变化（%d 项异常）", len(flips), nDown)
	title = fmt.Sprintf("网站检查状态变化 %d 项（%d 项异常）", len(flips), nDown)
	body = fmt.Sprintf("共 %d 项状态变化：\n%s时间：%s\n", len(flips), lines.String(), now)
	return subject, body, title
}
