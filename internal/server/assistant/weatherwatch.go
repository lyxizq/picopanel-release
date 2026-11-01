package assistant

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/weather"
)

// 天气侧提醒：新预警即推（按 id 去重）、带伞（每日定时看明日降水概率）、
// 空气污染（AQI 过阈值当日一次，恶化一档再提醒）。天气数据走 weather.Get 共用
// 10 分钟缓存，助理自己 5 分钟看一眼，不额外打上游。

// weatherNow 读页面设置取天气；天气未开启/无法定位/上游失败返回 nil（常态，不打日志）。
func weatherNow(st *store.Store) *weather.Resp {
	ps, err := st.GetPageSettings()
	if err != nil || !ps.WeatherEnabled {
		return nil
	}
	r, err := weather.Get(ps, "")
	if err != nil || r == nil {
		return nil
	}
	return r
}

// ---- 预警 ----

const warnSeenKeepDays = 10

type seenMap map[string]int64

func loadSeen(st *store.Store) seenMap {
	m := seenMap{}
	v, ok, err := st.GetSetting(keyWarnSeen)
	if err == nil && ok && v != "" {
		_ = json.Unmarshal([]byte(v), &m)
	}
	return m
}

func saveSeen(st *store.Store, m seenMap, now time.Time) {
	cut := now.AddDate(0, 0, -warnSeenKeepDays).Unix()
	for k, at := range m {
		if at < cut {
			delete(m, k)
		}
	}
	b, _ := json.Marshal(m)
	_ = st.SetSetting(keyWarnSeen, string(b))
}

// warnKey 去重键：和风给 id 用 id；缺 id 按标题+类型+等级+正文前 64 字哈希。
func warnKey(w weather.Warning) string {
	if w.ID != "" {
		return w.ID
	}
	h := sha1.Sum([]byte(w.Title + "|" + w.Type + "|" + w.Level + "|" + truncateRunes(w.Text, 64)))
	return "h:" + hex.EncodeToString(h[:8])
}

// checkWarnings 新出现的预警推送（多条合成一则），已见过的跳过。
func checkWarnings(st *store.Store, r *weather.Resp, now time.Time) {
	if r == nil {
		return
	}
	seen := loadSeen(st)
	var fresh []weather.Warning
	for _, w := range r.Warnings {
		k := warnKey(w)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = now.Unix()
		fresh = append(fresh, w)
	}
	if len(fresh) == 0 {
		return
	}
	saveSeen(st, seen, now)
	title := "天气预警：" + fresh[0].Title
	if len(fresh) > 1 {
		title = fmt.Sprintf("天气预警 %d 条：%s 等", len(fresh), fresh[0].Title)
	}
	var b strings.Builder
	if r.City != "" {
		b.WriteString(r.City + "\n")
	}
	for i, w := range fresh {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(w.Title)
		meta := []string{}
		if w.Level != "" {
			meta = append(meta, w.Level)
		}
		if w.Type != "" && !strings.Contains(w.Title, w.Type) {
			meta = append(meta, w.Type)
		}
		if len(meta) > 0 {
			b.WriteString("（" + strings.Join(meta, " · ") + "）")
		}
		b.WriteString("\n")
		if w.Text != "" {
			b.WriteString(truncateRunes(strings.TrimSpace(w.Text), 400) + "\n")
		}
		if w.Start != "" || w.End != "" {
			b.WriteString("生效：" + fmtWarnTime(w.Start) + " ~ " + fmtWarnTime(w.End) + "\n")
		}
		if w.Sender != "" {
			b.WriteString("发布：" + w.Sender + "\n")
		}
	}
	deliver(st, title, strings.TrimRight(b.String(), "\n"))
}

// fmtWarnTime 和风 ISO 时间（2026-10-26T08:00+08:00）压成「10-26 08:00」；解析不了原样。
func fmtWarnTime(s string) string {
	if s == "" {
		return "?"
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05Z", "2006-01-02T15:04Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.In(time.Local).Format("01-02 15:04")
		}
	}
	return s
}

// ---- 带伞 ----

// checkUmbrella 到点看明天预报：降水概率 ≥ 阈值（或预报有雪）推一条；当日只判一次。
func checkUmbrella(st *store.Store, cfg Config, r *weather.Resp, now time.Time) {
	if !fireDue(now, cfg.UmbrellaTime, strSetting(st, keyUmbLast, "")) {
		return
	}
	_ = st.SetSetting(keyUmbLast, ymd(now)) // 无论是否推送都记日期（无预报也不整天重试）
	if r == nil {
		return
	}
	tomorrow := ymd(now.AddDate(0, 0, 1))
	for _, d := range r.Daily {
		if d.Date != tomorrow {
			continue
		}
		rain := d.Precip >= cfg.UmbrellaThreshold
		snow := d.Snow != ""
		if !rain && !snow {
			return
		}
		what := "记得带伞"
		if snow {
			what = "可能降雪，注意保暖出行"
		}
		title := "明天" + what
		body := fmt.Sprintf("%s 明天（%s %s）%s，降水概率 %d%%，气温 %.0f~%.0f℃。",
			r.City, now.AddDate(0, 0, 1).Format("1月2日"), weekdayCN(now.AddDate(0, 0, 1)), wmoText(d.Code), d.Precip, d.Min, d.Max)
		if d.Snow != "" {
			body += " " + d.Snow + "。"
		}
		if d.Wind != "" {
			body += " " + d.Wind + "。"
		}
		deliver(st, title, strings.TrimSpace(body))
		return
	}
}

// ---- 空气 ----

// checkAir AQI ≥ 阈值：当日首次推送；同日若等级再升一档（如中度→重度）再推一次。
func checkAir(st *store.Store, cfg Config, r *weather.Resp, now time.Time) {
	if r == nil || r.Air == nil || r.Air.Aqi < cfg.AirThreshold {
		return
	}
	today := ymd(now)
	lastDay := strSetting(st, keyAirLast, "")
	lastAqi := intSetting(st, keyAirLastAqi, 0)
	if lastDay == today && aqiLevel(r.Air.Aqi) <= aqiLevel(lastAqi) {
		return
	}
	_ = st.SetSetting(keyAirLast, today)
	_ = st.SetSetting(keyAirLastAqi, fmt.Sprint(r.Air.Aqi))
	cat := r.Air.Category
	if cat == "" {
		cat = weather.AqiCategory(r.Air.Aqi)
	}
	title := "空气质量：" + cat
	body := fmt.Sprintf("%s 当前 AQI %d（%s）", r.City, r.Air.Aqi, cat)
	if r.Air.Primary != "" {
		body += "，首要污染物 " + r.Air.Primary
	}
	if r.Air.Approx {
		body += "（估算值）"
	}
	body += "。建议减少户外活动，外出佩戴口罩。"
	deliver(st, title, body)
}

// aqiLevel 国标六级序号（用于「恶化一档」比较）。
func aqiLevel(aqi int) int {
	switch {
	case aqi <= 50:
		return 1
	case aqi <= 100:
		return 2
	case aqi <= 150:
		return 3
	case aqi <= 200:
		return 4
	case aqi <= 300:
		return 5
	default:
		return 6
	}
}

// wmoText WMO 天气码中文（与前端 utils/weather.js 同口径的后端精简版，推送正文用）。
func wmoText(code int) string {
	switch {
	case code == 0:
		return "晴"
	case code == 1:
		return "大部晴朗"
	case code == 2:
		return "多云"
	case code == 3:
		return "阴"
	case code == 45 || code == 48:
		return "雾"
	case code >= 51 && code <= 57:
		return "小雨"
	case code == 61 || code == 80:
		return "小雨"
	case code == 63 || code == 81:
		return "中雨"
	case code == 65 || code == 82:
		return "大雨"
	case code == 66 || code == 67:
		return "冻雨"
	case code == 71 || code == 85:
		return "小雪"
	case code == 73:
		return "中雪"
	case code == 75 || code == 86:
		return "大雪"
	case code == 77:
		return "雪粒"
	case code >= 95:
		return "雷暴"
	default:
		return "多云"
	}
}
