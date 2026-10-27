package weather

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// Open-Meteo 空气质量兜底（个人助理批）：高德/Open-Meteo 天气源本身没有 AQI，
// 和风预警/空气接口偶发失败时也会缺——此处用 Open-Meteo 空气质量 API（免 key）
// 的 PM2.5/PM10 小时实况按国标 IAQI 分段线性换算出 AQI 与等级。GB 3095/HJ 633
// 的分级表是 24 小时均值口径，套用小时值属近似，Air.Approx=true 让前端标「估算」。
// airQualityURL 上游端点（包级变量：测试注入 httptest 地址，避免真打 Open-Meteo）。
var airQualityURL = "https://air-quality-api.open-meteo.com/v1/air-quality?latitude=%.4f&longitude=%.4f&current=pm2_5,pm10&timezone=auto"

var (
	airMu    sync.Mutex
	airCache = map[string]struct {
		at  time.Time
		air *Air
	}{}
)

// 国标分段：浓度断点 ↔ IAQI 断点（PM2.5/PM10 单位 µg/m³）。
var (
	iaqiBP = []float64{0, 50, 100, 150, 200, 300, 400, 500}
	pm25BP = []float64{0, 35, 75, 115, 150, 250, 350, 500}
	pm10BP = []float64{0, 50, 150, 250, 350, 420, 500, 600}
)

// fillAir Resp 无空气质量时补 Open-Meteo 估算值（失败静默，Air 保持 nil）。
func fillAir(r *Resp) {
	if r == nil || r.Air != nil {
		return
	}
	r.Air = airFallback(r.Lat, r.Lon)
}

func airFallback(lat, lon float64) *Air {
	key := fmt.Sprintf("%.2f,%.2f", lat, lon)
	airMu.Lock()
	if c, ok := airCache[key]; ok && cacheFresh(c.at) {
		airMu.Unlock()
		return c.air
	}
	airMu.Unlock()
	var resp struct {
		Current struct {
			PM25 *float64 `json:"pm2_5"`
			PM10 *float64 `json:"pm10"`
		} `json:"current"`
	}
	var air *Air
	if err := getJSON(fmt.Sprintf(airQualityURL, lat, lon), &resp); err == nil {
		air = airFromPM(resp.Current.PM25, resp.Current.PM10)
	}
	airMu.Lock()
	airCache[key] = struct {
		at  time.Time
		air *Air
	}{nowFn(), air}
	airMu.Unlock()
	return air
}

// airFromPM 由 PM2.5/PM10 浓度算 AQI（取两者 IAQI 最大值；两者皆缺返回 nil）。
func airFromPM(pm25, pm10 *float64) *Air {
	if pm25 == nil && pm10 == nil {
		return nil
	}
	aqi, primary := 0, ""
	if pm25 != nil {
		if v := iaqi(*pm25, pm25BP); v > aqi {
			aqi, primary = v, "PM2.5"
		}
	}
	if pm10 != nil {
		if v := iaqi(*pm10, pm10BP); v > aqi {
			aqi, primary = v, "PM10"
		}
	}
	if aqi <= 0 {
		return nil
	}
	if aqi <= 50 {
		primary = "" // 空气优时无首要污染物（和风同口径）
	}
	return &Air{Aqi: aqi, Category: AqiCategory(aqi), Primary: primary, Approx: true}
}

// iaqi 分段线性插值；超出最高断点按最高档线性外推到 500 封顶。
func iaqi(c float64, bp []float64) int {
	if c <= 0 {
		return 0
	}
	for i := 1; i < len(bp); i++ {
		if c <= bp[i] {
			v := (iaqiBP[i]-iaqiBP[i-1])/(bp[i]-bp[i-1])*(c-bp[i-1]) + iaqiBP[i-1]
			return int(math.Ceil(v))
		}
	}
	return 500
}

// AqiCategory 国标六级文案。
func AqiCategory(aqi int) string {
	switch {
	case aqi <= 50:
		return "优"
	case aqi <= 100:
		return "良"
	case aqi <= 150:
		return "轻度污染"
	case aqi <= 200:
		return "中度污染"
	case aqi <= 300:
		return "重度污染"
	default:
		return "严重污染"
	}
}
