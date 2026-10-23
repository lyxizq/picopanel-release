package engine

import (
	"context"
	"fmt"
	"log"
	"time"

	"lz-monitor/internal/server/store"
)

// RunOfflineCheck 启动离线检测循环（独立于上报触发——离线的机器恰恰没有上报）。
func (e *Engine) RunOfflineCheck(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.checkOffline(time.Now().Unix())
		}
	}
}

func (e *Engine) checkOffline(now int64) {
	// 轻量行：离线判定只需 id/enabled/last_report_at，不解密敏感字段（30s 一轮）
	servers, err := e.store.ListServerLites(true)
	if err != nil {
		log.Printf("离线检测查询服务器失败: %v", err)
		return
	}
	threshold := int64(e.cfg.OfflineAfter / time.Second)
	for _, srv := range servers {
		// 从未上报过的机器不告警（尚在部署中），仅页面显示离线
		if srv.LastReportAt == 0 {
			continue
		}
		offline := now-srv.LastReportAt > threshold
		open, err := e.store.FindOpenAlert(0, srv.ID)
		if err != nil {
			log.Printf("离线检测查询告警失败 (server=%d): %v", srv.ID, err)
			continue
		}
		switch {
		case offline && open == nil:
			a := newOfflineAlert(srv, now, threshold)
			if err := e.store.CreateAlert(a); err != nil {
				log.Printf("创建离线告警失败 (server=%d): %v", srv.ID, err)
				continue
			}
			e.notify(AlertEvent{Action: "triggered", Alert: *a})
		case !offline && open != nil:
			if err := e.store.ResolveAlert(open.ID, now); err != nil {
				log.Printf("恢复离线告警失败 (server=%d): %v", srv.ID, err)
				continue
			}
			open.Status = "resolved"
			open.ResolvedAt = &now
			e.notify(AlertEvent{Action: "resolved", Alert: *open})
		}
	}
}

// newOfflineAlert 构造内置离线告警（rule_id=0）。
func newOfflineAlert(srv store.ServerLite, now, threshold int64) *store.Alert {
	return &store.Alert{
		RuleID:      0,
		ServerID:    srv.ID,
		Metric:      "offline",
		Value:       float64(now - srv.LastReportAt),
		Detail:      fmt.Sprintf("超过 %s 无数据上报", time.Duration(threshold)*time.Second),
		Status:      "firing",
		TriggeredAt: now,
		UpdatedAt:   now,
	}
}
