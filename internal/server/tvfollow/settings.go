// Package tvfollow 追剧（548）：TMDB 剧集更新追踪 + 新集/完结推送。
// server 出口到不了 TMDB 时经通用中转任务（net_tasks，purpose=tvfollow_*）
// 由 push agent（家里 NAS）在家庭网络代查——入队/领取/回传复用 391 PT 中转
// 抓取的通道与防死循环纪律。本包负责：全局设置（出口 agent / TMDB key）、
// TMDB 请求构造与解析、每日轮询入队、回传消费（进度回写 + 新集/完结推送）。
package tvfollow

import (
	"log"
	"strconv"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

// net_tasks purpose 取值：搜索（一次性，结果留在任务里给 API 取）与
// detail（进度回写 tv_follows + 推送）。
const (
	PurposeAPI    = "tvfollow_api"
	PurposeDetail = "tvfollow_detail"
)

// 站内消息类型：追剧（独立类型，勿混 reminder 红字联查——492 membership 先例）。
const MessageType = "tv"

const (
	keyTmdbKey   = "tv_tmdb_key"
	keyEgress    = "tv_egress_server_id"
	keyLastSweep = "tv_last_sweep_at"
)

// TmdbKey 读 TMDB API key（settings 存储，v3 auth key）。
func TmdbKey(st *store.Store) string {
	v, ok, err := st.GetSetting(keyTmdbKey)
	if err != nil || !ok {
		return ""
	}
	return v
}

// SetTmdbKey 写 TMDB API key（空串=清除）。
func SetTmdbKey(st *store.Store, key string) error {
	return st.SetSetting(keyTmdbKey, key)
}

// LastSweepAt 最近一轮定时抓取（0/6/12/18 点检查）的入队时刻（63，秒；0=从未）。
// 只记 sweep 真正入队了待查条目的轮次，手动「立即检查」不打这个标。
func LastSweepAt(st *store.Store) int64 {
	v, ok, err := st.GetSetting(keyLastSweep)
	if err != nil || !ok {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

func setLastSweepAt(st *store.Store, ts int64) {
	_ = st.SetSetting(keyLastSweep, strconv.FormatInt(ts, 10))
}

// EgressServerID 读出口 push agent 的服务器 ID（0=未配置）。
func EgressServerID(st *store.Store) int64 {
	v, ok, err := st.GetSetting(keyEgress)
	if err != nil || !ok {
		return 0
	}
	id, _ := strconv.ParseInt(v, 10, 64)
	return id
}

// SetEgressServerID 写出口。
func SetEgressServerID(st *store.Store, id int64) error {
	return st.SetSetting(keyEgress, strconv.FormatInt(id, 10))
}

// pushChannels 按各渠道总开关 + 追剧细分开关分发（开关每次现读即时生效；
// 缺省关——推送量虽小，口径与邮件提醒一致，用户显式开）。
func pushChannels(st *store.Store, subject, body string) {
	if wxpusher.On(st) && wxpusher.TvWXOn(st) {
		if err := wxpusher.SendFromStore(st, subject, body); err != nil {
			log.Printf("追剧 WxPusher 推送失败: %v", err)
		}
	}
	if webhook.On(st) && webhook.TvOn(st) {
		if err := webhook.SendFromStore(st, subject, body); err != nil {
			log.Printf("追剧 Webhook 推送失败: %v", err)
		}
	}
	if webpush.On(st) && webpush.TvOn(st) {
		if err := webpush.SendFromStore(st, subject, body); err != nil {
			log.Printf("追剧 Web Push 推送失败: %v", err)
		}
	}
}
