// Package weather 提供首页天气：双服务商——高德（配 Key 启用，中国区实况/预报更准，
// 直出 adcode；详见 gaode.go）与 Open-Meteo 免 key 数据源（默认/兜底，含地理编码，
// 支持中文城市搜索）+ 自动定位（服务端按访客真实 IP：高德 IP 定位或 ip-api.com
// 免费档，备选 HTTPS 的 ipwho.is）+ 手动城市兜底。全惰性缓存：预报按位置缓存
// 10 分钟（523/56：与全链路 10 分钟采集节拍对齐）、IP/adcode 定位按 IP 或城市
// 缓存 24 小时；凌晨 0:00–5:30 夜间静默（56）：缓存不判过期直接回旧值、采样跳过，
// 不打任何上游（冷启动空缓存仍放行一次自愈）。高德失败自动回落 Open-Meteo。
// 另有常驻采样协程（StartSampler，522）：每 10 分钟把当前天气落库供历史曲线
// （56 起同时存完整 Resp JSON 供 HA 胶囊读库），7 天外归档清理走 store（挂
// runRetentionLoop）。
package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

// 上游端点为包级变量：测试注入 httptest 地址用。
var (
	forecastURL = "https://api.open-meteo.com/v1/forecast"
	geocodeURL  = "https://geocoding-api.open-meteo.com/v1/search"
	ipAPIURL    = "http://ip-api.com/json/" // 免费档限非商用、仅 HTTP（服务端调用）
	ipWhoisURL  = "https://ipwho.is/"       // 备选（HTTPS）
)

const (
	fetchTimeout = 10 * time.Second
	forecastTTL  = 10 * time.Minute // 523 建 5 分钟、56 改 10 分钟：页面轮询/采样协程统一 10 分钟节拍，上游调用量 ≈6 次/小时/位置
	geoTTL       = 24 * time.Hour
	quietFrom    = 0         // 夜间静默（56）：0:00 起
	quietTo      = 5*60 + 30 // 到 5:30（分钟数，含头不含尾）——静默期内缓存不判过期、采样跳过，零上游调用
)

var cl = &http.Client{Timeout: fetchTimeout}

// nowFn 可注入时钟（测试静默期用）；inQuietHours 判 t 是否在 [0:00, 5:30)。
var nowFn = time.Now

func inQuietHours(t time.Time) bool {
	m := t.Hour()*60 + t.Minute()
	return m >= quietFrom && m < quietTo
}

// cacheFresh 缓存命中判定（三服务商共用）：TTL 内算新鲜；夜间静默期已有缓存一律
// 视为新鲜（不判过期直接回旧值，零上游调用），空缓存不豁免——冷启动照常取一次自愈。
func cacheFresh(at time.Time) bool {
	now := nowFn()
	return now.Sub(at) < forecastTTL || inQuietHours(now)
}

// Current 实况。
type Current struct {
	Temp       float64 `json:"temp"`     // 气温 ℃
	Feels      float64 `json:"feels"`    // 体感 ℃
	Humidity   int     `json:"humidity"` // 湿度 %
	Wind       float64 `json:"wind"`     // 风 km/h
	Code       int     `json:"code"`     // WMO 天气码（前端映射文案/图标）
	IsDay      bool    `json:"is_day"`
	WindDir    string  `json:"wind_dir,omitempty"`   // 风向（北/东南风…；服务商直出或 deg 换算，无则空）
	Pressure   int     `json:"pressure,omitempty"`   // 海平面气压 hPa
	Visibility float64 `json:"visibility,omitempty"` // 能见度 km
	Uv         float64 `json:"uv,omitempty"`         // 今日紫外线指数
	Sunrise    string  `json:"sunrise,omitempty"`    // 今日日出 HH:MM
	Sunset     string  `json:"sunset,omitempty"`     // 今日日落 HH:MM
}

// Day 单日预报。
type Day struct {
	Date   string  `json:"date"` // YYYY-MM-DD（本地时区，timezone=auto）
	Code   int     `json:"code"`
	Max    float64 `json:"max"`
	Min    float64 `json:"min"`
	Precip int     `json:"precip"`              // 降水概率 %
	Snow   string  `json:"snow,omitempty"`      // 降雪提示（536）：Open-Meteo 雪量 cm / 现象判定「有雪」，空=无
	Wind   string  `json:"wind,omitempty"`      // 大风提示（536）：风速/阵风/风力级文案，空=未达阈值
}

// Warning 天气预警（524，和风专属）。
type Warning struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Level string `json:"level,omitempty"`
	Type  string `json:"type,omitempty"`
}

// IndexItem 生活指数（524，和风专属）。
type IndexItem struct {
	Name  string `json:"name"`
	Level string `json:"level"`
	Text  string `json:"text,omitempty"`
}

// Air 空气质量实况（524，和风专属）。
type Air struct {
	Aqi      int    `json:"aqi"`
	Category string `json:"category,omitempty"` // 优/良/轻度污染…
	Primary  string `json:"primary,omitempty"`  // 首要污染物
}

// Resp /page/weather 应答。
type Resp struct {
	City     string     `json:"city"`
	Lat      float64    `json:"lat"`
	Lon      float64    `json:"lon"`
	Source   string     `json:"source"`   // ip=自动定位 / manual=手动城市
	Provider string     `json:"provider"` // 实际出数服务商（519）：gaode/qweather/openmeteo，回落也如实标注
	Current  *Current   `json:"current"`
	Daily    []Day      `json:"daily"`
	Minutely string     `json:"minutely,omitempty"`  // 未来两小时分钟级降水描述（524）
	Warnings []Warning  `json:"warnings,omitempty"`  // 天气预警（524）
	Indices  []IndexItem `json:"indices,omitempty"`  // 生活指数（524）
	Air      *Air       `json:"air,omitempty"`       // 空气质量实况（524）
}

// City 城市搜索候选。
type City struct {
	Name   string  `json:"name"`
	Label  string  `json:"label"` // 名称 · 省州 · 国家（展示用）
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Adcode string  `json:"adcode,omitempty"` // 高德候选带 adcode（取数直用）
}

// Get 定位 + 取预报（页面配置由调用方读库传入）。
// provider=gaode/qweather 且配了 Key 走对应服务商（中国区更准），失败自动回落 Open-Meteo。
func Get(ps *store.PageSettings, clientIP string) (*Resp, error) {
	if ps.WeatherProvider == "gaode" && strings.TrimSpace(ps.WeatherKey) != "" {
		if r, err := GetGaode(ps, clientIP, ps.WeatherKey); err == nil {
			r.Provider = "gaode"
			return r, nil
		}
		// 高德失败回落 Open-Meteo（经纬度路径仍可用）
	}
	if ps.WeatherProvider == "qweather" && strings.TrimSpace(ps.WeatherKey) != "" {
		if r, err := GetQWeather(ps, clientIP, ps.WeatherKey); err == nil {
			r.Provider = "qweather"
			return r, nil
		}
		// 和风失败回落 Open-Meteo
	}
	city, lat, lon, source, err := locate(ps, clientIP)
	if err != nil {
		return nil, err
	}
	cur, daily, err := forecast(lat, lon)
	if err != nil {
		return nil, err
	}
	return &Resp{City: city, Lat: lat, Lon: lon, Source: source, Provider: "openmeteo", Current: cur, Daily: daily}, nil
}

// SampleLocKey 采样与历史查询共用的位置键（"纬度,经度" 两位小数，522）：
// manual 配置优先；auto 模式无访客 IP（服务端采样场景）回落 manual 配置，
// 两者皆无报错——此时不采样也无历史可查。
func SampleLocKey(ps *store.PageSettings) (string, error) {
	_, lat, lon, _, err := locate(ps, "")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%.2f,%.2f", lat, lon), nil
}

// StartSampler 天气采样循环（522）：每 tick 读页面设置，天气开启且能定位才取
// 一次当前天气落库——成功才记，定位/上游失败这轮跳过（历史宁缺勿假）。取数走
// Get（与页面共用 5 分钟预报缓存，命中即不再打上游）。位置键与 SampleLocKey
// 同源（Resp 的 Lat/Lon 即 locate 结果）。
func StartSampler(ctx context.Context, st *store.Store, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sampleOnce(st)
			}
		}
	}()
}

// sampleOnce 一轮采样：设置读失败/天气未开启/夜间静默/无法定位/上游失败均静默
// 跳过（天气未开是常态而非错误，不打日志）。
func sampleOnce(st *store.Store) {
	if inQuietHours(nowFn()) {
		return // 夜间静默（56）：0:00–5:30 不采集不落库
	}
	ps, err := st.GetPageSettings()
	if err != nil || !ps.WeatherEnabled {
		return
	}
	r, err := Get(ps, "")
	if err != nil || r == nil || r.Current == nil {
		return
	}
	data, _ := json.Marshal(r) // 完整 Resp 存 data 列（56）：HA 胶囊读库直出，不打上游
	_ = st.RecordWXSample(store.WXSample{
		Loc:      fmt.Sprintf("%.2f,%.2f", r.Lat, r.Lon),
		Temp:     r.Current.Temp,
		Humidity: float64(r.Current.Humidity),
		Wind:     r.Current.Wind,
		Pressure: float64(r.Current.Pressure),
		TS:       time.Now().Unix(),
		Data:     string(data),
	})
}

// locate 解析定位：manual 配置优先；auto 按 IP（失败回落 manual 配置，再失败报错）。
func locate(ps *store.PageSettings, clientIP string) (city string, lat, lon float64, source string, err error) {
	hasManual := ps.WeatherLat != 0 || ps.WeatherLon != 0
	if ps.WeatherMode == "manual" && hasManual {
		return ps.WeatherCity, ps.WeatherLat, ps.WeatherLon, "manual", nil
	}
	if c, la, lo, ok := ipLocate(clientIP); ok {
		return c, la, lo, "ip", nil
	}
	if hasManual {
		return ps.WeatherCity, ps.WeatherLat, ps.WeatherLon, "manual", nil
	}
	return "", 0, 0, "", errors.New("无法定位（IP 定位失败且未配置手动城市）")
}

// ---- IP 定位（24h 缓存） ----

type geoEntry struct {
	city string
	lat  float64
	lon  float64
	at   time.Time
}

var (
	geoMu    sync.Mutex
	geoCache = map[string]geoEntry{}
)

// ipLocate 查 IP 对应城市；本机/内网地址直接失败（公网库无意义）。
func ipLocate(ip string) (string, float64, float64, bool) {
	p := net.ParseIP(ip)
	if p == nil || p.IsLoopback() || p.IsPrivate() || p.IsLinkLocalUnicast() {
		return "", 0, 0, false
	}
	geoMu.Lock()
	if e, ok := geoCache[ip]; ok && time.Since(e.at) < geoTTL {
		geoMu.Unlock()
		return e.city, e.lat, e.lon, true
	}
	geoMu.Unlock()

	city, la, lo, ok := ipQuery(ip)
	if !ok {
		return "", 0, 0, false
	}
	geoMu.Lock()
	// 简单防膨胀：超 1000 条整体重来（24h 过期足够收敛）
	if len(geoCache) > 1000 {
		geoCache = map[string]geoEntry{}
	}
	geoCache[ip] = geoEntry{city: city, lat: la, lon: lo, at: time.Now()}
	geoMu.Unlock()
	return city, la, lo, true
}

func ipQuery(ip string) (string, float64, float64, bool) {
	// 主：ip-api.com（fields 裁剪 + 中文城市名）
	var r1 struct {
		Status string  `json:"status"`
		City   string  `json:"city"`
		Lat    float64 `json:"lat"`
		Lon    float64 `json:"lon"`
	}
	if getJSON(ipAPIURL+ip+"?fields=status,city,lat,lon&lang=zh-CN", &r1) == nil &&
		r1.Status == "success" && (r1.Lat != 0 || r1.Lon != 0) {
		return r1.City, r1.Lat, r1.Lon, true
	}
	// 备：ipwho.is（HTTPS，免费档无中文——实测返 "Hangzhou"；国内服务器上
	// ip-api.com 可达性差时会长期落到这里，385 起取到经纬度后经 BigDataCloud
	// 免费逆地理换中文城市名，失败退回其英文名保底有城市可显）
	var r2 struct {
		Success   bool    `json:"success"`
		City      string  `json:"city"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if getJSON(ipWhoisURL+ip, &r2) == nil && r2.Success && (r2.Latitude != 0 || r2.Longitude != 0) {
		city := r2.City
		var r3 struct {
			City string `json:"city"`
		}
		bdcc := fmt.Sprintf("https://api.bigdatacloud.net/data/reverse-geocode-client?latitude=%v&longitude=%v&localityLanguage=zh",
			r2.Latitude, r2.Longitude)
		if getJSON(bdcc, &r3) == nil && strings.TrimSpace(r3.City) != "" {
			city = strings.TrimSpace(r3.City)
		}
		return city, r2.Latitude, r2.Longitude, true
	}
	return "", 0, 0, false
}

// ---- Open-Meteo 预报（5 分钟缓存） ----

type fcEntry struct {
	cur   *Current
	daily []Day
	at    time.Time
}

var (
	fcMu    sync.Mutex
	fcCache = map[string]fcEntry{}
)

func forecast(lat, lon float64) (*Current, []Day, error) {
	key := fmt.Sprintf("%.2f,%.2f", lat, lon)
	fcMu.Lock()
	if e, ok := fcCache[key]; ok && cacheFresh(e.at) {
		fcMu.Unlock()
		return e.cur, e.daily, nil
	}
	fcMu.Unlock()

	q := url.Values{}
	q.Set("latitude", fmt.Sprintf("%.4f", lat))
	q.Set("longitude", fmt.Sprintf("%.4f", lon))
	q.Set("current", "temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m,wind_direction_10m,pressure_msl,is_day")
	q.Set("daily", "weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,uv_index_max,sunrise,sunset,snowfall_sum,wind_gusts_10m_max")
	q.Set("hourly", "visibility") // 无实况能见度字段，取当前小时插值
	q.Set("timezone", "auto")
	q.Set("forecast_days", "7")
	var r struct {
		Error   string `json:"reason"`
		HasErr  bool   `json:"error"`
		Current struct {
			Temperature2m       float64 `json:"temperature_2m"`
			ApparentTemperature float64 `json:"apparent_temperature"`
			RelativeHumidity2m  float64 `json:"relative_humidity_2m"`
			WeatherCode         int     `json:"weather_code"`
			WindSpeed10m        float64 `json:"wind_speed_10m"`
			WindDirection10m    float64 `json:"wind_direction_10m"`
			PressureMSL         float64 `json:"pressure_msl"`
			IsDay               int     `json:"is_day"`
		} `json:"current"`
		Hourly struct {
			Visibility []float64 `json:"visibility"` // 米，今日 0 点起逐小时
		} `json:"hourly"`
		Daily struct {
			Time        []string  `json:"time"`
			WeatherCode []int     `json:"weather_code"`
			Max         []float64 `json:"temperature_2m_max"`
			Min         []float64 `json:"temperature_2m_min"`
			Precip      []int     `json:"precipitation_probability_max"`
			UVMax       []float64 `json:"uv_index_max"`
			Sunrise     []string  `json:"sunrise"`
			Sunset      []string  `json:"sunset"`
			Snowfall    []float64 `json:"snowfall_sum"`      // 日降雪量 cm（536）
			Gusts       []float64 `json:"wind_gusts_10m_max"` // 日最大阵风 km/h（536）
		} `json:"daily"`
	}
	if err := getJSON(forecastURL+"?"+q.Encode(), &r); err != nil {
		return nil, nil, fmt.Errorf("获取天气数据失败: %w", err)
	}
	if r.HasErr {
		return nil, nil, fmt.Errorf("天气接口报错: %s", r.Error)
	}
	cur := &Current{
		Temp: r.Current.Temperature2m, Feels: r.Current.ApparentTemperature,
		Humidity: int(r.Current.RelativeHumidity2m), Wind: r.Current.WindSpeed10m,
		Code: r.Current.WeatherCode, IsDay: r.Current.IsDay != 0,
		WindDir:  degToDir(r.Current.WindDirection10m),
		Pressure: int(r.Current.PressureMSL + 0.5),
	}
	if len(r.Hourly.Visibility) > 0 {
		i := time.Now().Hour()
		if i >= len(r.Hourly.Visibility) {
			i = len(r.Hourly.Visibility) - 1
		}
		cur.Visibility = r.Hourly.Visibility[i] / 1000 // 米 → km
	}
	if len(r.Daily.UVMax) > 0 {
		cur.Uv = r.Daily.UVMax[0]
	}
	if len(r.Daily.Sunrise) > 0 {
		cur.Sunrise = hhmm(r.Daily.Sunrise[0])
	}
	if len(r.Daily.Sunset) > 0 {
		cur.Sunset = hhmm(r.Daily.Sunset[0])
	}
	daily := make([]Day, 0, len(r.Daily.Time))
	for i := range r.Daily.Time {
		d := Day{Date: r.Daily.Time[i], Code: r.Daily.WeatherCode[i],
			Max: r.Daily.Max[i], Min: r.Daily.Min[i]}
		if i < len(r.Daily.Precip) {
			d.Precip = r.Daily.Precip[i]
		}
		if i < len(r.Daily.Snowfall) && r.Daily.Snowfall[i] > 0 {
			d.Snow = fmt.Sprintf("%.1f cm", r.Daily.Snowfall[i])
		}
		if i < len(r.Daily.Gusts) && r.Daily.Gusts[i] >= 40 { // ≈6 级起算大风
			d.Wind = fmt.Sprintf("阵风 %.0f km/h", r.Daily.Gusts[i])
		}
		daily = append(daily, d)
	}
	fcMu.Lock()
	if len(fcCache) > 200 {
		fcCache = map[string]fcEntry{}
	}
	fcCache[key] = fcEntry{cur: cur, daily: daily, at: nowFn()} // at 走 nowFn：与 cacheFresh 同钟（测试注入一致）
	fcMu.Unlock()
	return cur, daily, nil
}

// ---- 城市搜索（按服务商：高德 geocode 带 adcode / Open-Meteo geocoding 中文） ----

// SearchCity 按名称搜城市（最多 5 个候选）。配了高德/和风 Key 优先走对应服务商
//（和风走设置的专属 API Host，未配 host 报错回落），失败回落 Open-Meteo。
func SearchCity(name string, ps *store.PageSettings) ([]City, error) {
	provider, key, host := ps.WeatherProvider, ps.WeatherKey, ps.WeatherHost
	if provider == "gaode" && strings.TrimSpace(key) != "" {
		if cities, err := SearchGaodeCities(name, key); err == nil {
			return cities, nil
		}
	}
	if provider == "qweather" && strings.TrimSpace(key) != "" {
		if cities, err := SearchQWeatherCities(host, name, key); err == nil {
			return cities, nil
		}
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("count", "5")
	q.Set("language", "zh")
	q.Set("format", "json")
	var r struct {
		Results []struct {
			Name    string  `json:"name"`
			Lat     float64 `json:"latitude"`
			Lon     float64 `json:"longitude"`
			Country string  `json:"country"`
			Admin1  string  `json:"admin1"`
		} `json:"results"`
	}
	if err := getJSON(geocodeURL+"?"+q.Encode(), &r); err != nil {
		return nil, fmt.Errorf("城市搜索失败: %w", err)
	}
	out := make([]City, 0, len(r.Results))
	for _, it := range r.Results {
		label := it.Name
		var extra []string
		if it.Admin1 != "" && it.Admin1 != it.Name {
			extra = append(extra, it.Admin1)
		}
		if it.Country != "" {
			extra = append(extra, it.Country)
		}
		if len(extra) > 0 {
			label = it.Name + " · " + strings.Join(extra, " · ")
		}
		out = append(out, City{Name: it.Name, Label: label, Lat: it.Lat, Lon: it.Lon})
	}
	return out, nil
}

// ---- 公共 ----

// degToDir 风向角度（Open-Meteo deg）→ 八方位中文。
func degToDir(deg float64) string {
	names := []string{"北", "东北", "东", "东南", "南", "西南", "西", "西北"}
	if deg < 0 {
		deg += 360
	}
	return names[int(deg/45+0.5)%8]
}

// hhmm Open-Meteo 时刻（"2026-10-03T06:12"）→ "06:12"。
func hhmm(s string) string {
	if len(s) >= 16 {
		return s[11:16]
	}
	return s
}

// SetUpstreams 覆盖上游端点（仅供测试注入 httptest 地址）；返回恢复函数。
func SetUpstreams(forecast, geocode, ipAPI, ipWhois string) (restore func()) {
	of, og, oi, ow := forecastURL, geocodeURL, ipAPIURL, ipWhoisURL
	forecastURL, geocodeURL, ipAPIURL, ipWhoisURL = forecast, geocode, ipAPI, ipWhois
	return func() { forecastURL, geocodeURL, ipAPIURL, ipWhoisURL = of, og, oi, ow }
}

// SetUpGaode 覆盖高德上游端点（仅供测试注入）；返回恢复函数。
func SetUpGaode(weather, geo, ip string) (restore func()) {
	ow, og, oi := gaodeWeatherURL, gaodeGeoURL, gaodeIPURL
	gaodeWeatherURL, gaodeGeoURL, gaodeIPURL = weather, geo, ip
	return func() { gaodeWeatherURL, gaodeGeoURL, gaodeIPURL = ow, og, oi }
}

// SetUpQWeather 覆盖和风上游 base（仅供测试注入）；返回恢复函数。
func SetUpQWeather(now, daily, geo string) (restore func()) {
	on, od, og := qwCurOverride, qwDailyOverride, qwGeoOverride
	qwCurOverride, qwDailyOverride, qwGeoOverride = now, daily, geo
	return func() { qwCurOverride, qwDailyOverride, qwGeoOverride = on, od, og }
}

// ClearGaodeCaches 清空高德/和风/Open-Meteo 预报缓存（测试隔离用；fcCache 不清
// 会让 -count 重跑命中上轮缓存，mock 收不到请求、参数断言空串失败）。
func ClearGaodeCaches() {
	adMu.Lock()
	adCache = map[string]adEntry{}
	adMu.Unlock()
	gdMu.Lock()
	gdCache = map[string]gdEntry{}
	gdMu.Unlock()
	qwMu.Lock()
	qwCache = map[string]qwEntry{}
	qwMu.Unlock()
	fcMu.Lock()
	fcCache = map[string]fcEntry{}
	fcMu.Unlock()
}

// getJSONKeyed 带 X-QW-Api-Key 请求头的 GET（和风鉴权口径，key= 参数已废弃）。
func getJSONKeyed(url, key string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-QW-Api-Key", key)
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func getJSON(url string, out any) error {
	resp, err := cl.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
