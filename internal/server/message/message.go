// Package message 把告警事件落成站内消息（messages 表），供顶栏消息中心
// 展示：所有发送的告警通知（触发/恢复）均入站为「告警」类消息，与推送
// 渠道开关无关——推送开关只控制是否外发，站内记录始终保留。
// 证书临期提醒与提醒事项推送由各自业务方直接写 store.CreateMessage。
package message

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
)

// Sink 实现 engine.Notifier：每条告警事件写一条站内消息（静默，无外发）。
type Sink struct{ st *store.Store }

// NewSink 创建消息落库 sink（装配时注入 engine.New）。
func NewSink(st *store.Store) *Sink { return &Sink{st: st} }

// Notify 将一条告警触发/恢复事件写入 messages 表；失败只打日志。
func (s *Sink) Notify(ev engine.AlertEvent) {
	name := ev.Alert.ServerName
	if name == "" {
		if srv, err := s.st.GetServer(ev.Alert.ServerID); err == nil {
			name = srv.Name
		} else {
			name = "#" + strconv.FormatInt(int64(ev.Alert.ServerID), 10)
		}
	}
	action := "触发"
	if ev.Action == "resolved" {
		action = "恢复"
	}
	m := &store.Message{
		Type:      "alert",
		Title:     fmt.Sprintf("告警%s：%s", action, name),
		Content:   ev.Alert.Detail,
		RefType:   "alert",
		RefID:     int64(ev.Alert.ID),
		CreatedAt: time.Now().Unix(),
	}
	if err := s.st.CreateMessage(m); err != nil {
		log.Printf("告警消息入站失败: %v", err)
	}
}
