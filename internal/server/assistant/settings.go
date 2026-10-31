package assistant

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

// 助理设置全部落 settings 表散键（口径同巡检报告 inspect_* 键）；布尔键缺省视为开。
const (
	keyBriefOn    = "assist_brief_on"
	keyBriefTime  = "assist_brief_time"
	keyBriefLast  = "assist_brief_last" // YYYY-MM-DD 当日防重
	keyWarnOn     = "assist_warn_on"
	keyWarnSeen   = "assist_warn_seen" // JSON：预警 id → 首见时间
	keyUmbOn      = "assist_umbrella_on"
	keyUmbTime    = "assist_umbrella_time"
	keyUmbThresh  = "assist_umbrella_threshold"
	keyUmbLast    = "assist_umbrella_last"
	keyAirOn      = "assist_air_on"
	keyAirThresh  = "assist_air_threshold"
	keyAirLast    = "assist_air_last"
	keyAirLastAqi = "assist_air_last_aqi" // 当日已提醒时的 AQI（恶化一档再提醒）

	defBriefTime = "08:00"
	defUmbTime   = "20:00"
	defUmbThresh = 50  // 明日降水概率 ≥ 50% 提醒带伞
	defAirThresh = 150 // AQI ≥ 150（中度污染）提醒

	// MessageType 站内消息类型（消息中心按类型筛选/配色）。
	MessageType = "assist"
)

// Config 助理配置视图。
type Config struct {
	BriefOn           bool   `json:"brief_on"`
	BriefTime         string `json:"brief_time"`
	WarnOn            bool   `json:"warn_on"`
	UmbrellaOn        bool   `json:"umbrella_on"`
	UmbrellaTime      string `json:"umbrella_time"`
	UmbrellaThreshold int    `json:"umbrella_threshold"`
	AirOn             bool   `json:"air_on"`
	AirThreshold      int    `json:"air_threshold"`
}

func onDefault(st *store.Store, key string) bool {
	v, ok, err := st.GetSetting(key)
	if err != nil {
		return false
	}
	return !ok || v != "0"
}

func strSetting(st *store.Store, key, def string) string {
	v, ok, err := st.GetSetting(key)
	if err != nil || !ok || strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func intSetting(st *store.Store, key string, def int) int {
	n, err := strconv.Atoi(strSetting(st, key, ""))
	if err != nil {
		return def
	}
	return n
}

// LoadConfig 读配置（缺省：全开，早报 08:00，带伞 20:00/50%，空气 150）。
func LoadConfig(st *store.Store) Config {
	return Config{
		BriefOn:           onDefault(st, keyBriefOn),
		BriefTime:         normHHMM(strSetting(st, keyBriefTime, defBriefTime), defBriefTime),
		WarnOn:            onDefault(st, keyWarnOn),
		UmbrellaOn:        onDefault(st, keyUmbOn),
		UmbrellaTime:      normHHMM(strSetting(st, keyUmbTime, defUmbTime), defUmbTime),
		UmbrellaThreshold: clampInt(intSetting(st, keyUmbThresh, defUmbThresh), 1, 100),
		AirOn:             onDefault(st, keyAirOn),
		AirThreshold:      clampInt(intSetting(st, keyAirThresh, defAirThresh), 50, 500),
	}
}

// SaveConfig 校验并写回。
func SaveConfig(st *store.Store, c Config) error {
	if _, _, ok := parseHHMM(c.BriefTime); !ok {
		return errors.New("早报时间格式须为 HH:MM")
	}
	if _, _, ok := parseHHMM(c.UmbrellaTime); !ok {
		return errors.New("带伞提醒时间格式须为 HH:MM")
	}
	if c.UmbrellaThreshold < 1 || c.UmbrellaThreshold > 100 {
		return errors.New("降水概率阈值须在 1~100 之间")
	}
	if c.AirThreshold < 50 || c.AirThreshold > 500 {
		return errors.New("空气质量阈值须在 50~500 之间")
	}
	for k, v := range map[string]string{
		keyBriefOn: boolStr(c.BriefOn), keyBriefTime: c.BriefTime,
		keyWarnOn: boolStr(c.WarnOn),
		keyUmbOn: boolStr(c.UmbrellaOn), keyUmbTime: c.UmbrellaTime, keyUmbThresh: strconv.Itoa(c.UmbrellaThreshold),
		keyAirOn: boolStr(c.AirOn), keyAirThresh: strconv.Itoa(c.AirThreshold),
	} {
		if err := st.SetSetting(k, v); err != nil {
			return err
		}
	}
	return nil
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// parseHHMM 解析 "H:MM"/"HH:MM"。
func parseHHMM(s string) (h, m int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func normHHMM(s, def string) string {
	h, m, ok := parseHHMM(s)
	if !ok {
		return def
	}
	return fmt.Sprintf("%02d:%02d", h, m)
}

// deliver 站内消息必落（渠道全关/全败也不丢），再按三渠道「总开关 + 助理开关」推送；
// 渠道推送失败只记日志（与提醒事项同口径）。
func deliver(st *store.Store, title, body string) {
	if err := st.CreateMessage(&store.Message{
		Type: MessageType, Title: title, Content: body, RefType: "assist", CreatedAt: time.Now().Unix(),
	}); err != nil {
		log.Printf("助理站内消息写入失败: %v", err)
	}
	if wxpusher.On(st) && wxpusher.AssistWXOn(st) {
		if err := wxpusher.SendFromStore(st, title, body); err != nil {
			log.Printf("助理 WxPusher 推送失败: %v", err)
		}
	}
	if webhook.On(st) && webhook.AssistOn(st) {
		if err := webhook.SendFromStore(st, title, body); err != nil {
			log.Printf("助理 Webhook 推送失败: %v", err)
		}
	}
	if webpush.On(st) && webpush.AssistOn(st) {
		if err := webpush.SendFromStore(st, title, body); err != nil {
			log.Printf("助理 Web Push 推送失败: %v", err)
		}
	}
}

// fireDue 「HH:MM 到点且今天未推」判定（巡检报告同款：错过时刻顺延补推、当日只一次）。
func fireDue(now time.Time, hhmm, lastDay string) bool {
	h, m, ok := parseHHMM(hhmm)
	if !ok {
		return false
	}
	fire := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	return !now.Before(fire) && lastDay != ymd(now)
}
