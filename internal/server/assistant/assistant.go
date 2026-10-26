package assistant

import (
	"context"
	"time"

	"lz-monitor/internal/server/store"
)

const (
	tick          = 30 * time.Second
	wxCheckEvery  = 5 * time.Minute // 预警/空气巡视节拍（天气本身 10 分钟缓存，不额外打上游）
	subFirstDelay = time.Minute     // 启动后稍等再刷订阅，避开开机抢网
)

// Run 助理主循环：早报到点推送（含并入的巡检段）、预警/空气巡视、带伞到点判定、ICS 订阅定期刷新。
// offlineAfter 为服务器离线判定窗口秒数；unreadFn 为邮件未读统计（mail.Manager.UnreadAll），可为 nil。
func Run(ctx context.Context, st *store.Store, offlineAfter int64, unreadFn func() map[int64]int) {
	var lastWx, lastSubs time.Time
	started := time.Now()
	for {
		now := time.Now()
		cfg := LoadConfig(st)
		if cfg.BriefOn && fireDue(now, cfg.BriefTime, strSetting(st, keyBriefLast, "")) {
			_ = st.SetSetting(keyBriefLast, ymd(now)) // 先记日期：生成/推送失败不整天重试
			BriefNow(st, offlineAfter, unreadFn, now)
		}
		needWx := cfg.WarnOn || cfg.AirOn
		umbDue := cfg.UmbrellaOn && fireDue(now, cfg.UmbrellaTime, strSetting(st, keyUmbLast, ""))
		if (needWx && now.Sub(lastWx) >= wxCheckEvery) || umbDue {
			r := weatherNow(st)
			if needWx && now.Sub(lastWx) >= wxCheckEvery {
				lastWx = now
				if cfg.WarnOn {
					checkWarnings(st, r, now)
				}
				if cfg.AirOn {
					checkAir(st, cfg, r, now)
				}
			}
			if umbDue {
				checkUmbrella(st, cfg, r, now)
			}
		}
		if (lastSubs.IsZero() && now.Sub(started) >= subFirstDelay) || (!lastSubs.IsZero() && now.Sub(lastSubs) >= subRefreshEvery) {
			lastSubs = now
			RefreshAllSubs(st, now)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(tick):
		}
	}
}
