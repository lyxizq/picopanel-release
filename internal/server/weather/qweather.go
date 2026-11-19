// 和风天气服务商（320，518 迁 v1）：中国区数据。v1 起走账户专属 API Host
// （console.qweather.com 设置页查看，host 本身参与鉴权；旧公共域名 devapi/geoapi
// 2026 年起逐步停服，新账号打到 devapi 全被网关拒——控制台呈「请求量 0 错误率 100%」）。
// 鉴权沿用 X-QW-Api-Key 请求头（v1 两种凭据里 API Key 仍支持；JWT 面板不采用）。
// 实况 /weather/v1/current/{纬度}/{经度} + 7 日预报 /weather/v1/daily/...（路径纬度在
// 前、两位小数，daily localTime=true 按当地时区切日），城市搜索 /geo/v2/city/lookup。
// 现象文本复用高德关键词映射 WMO；结果 5 分钟缓存，上游失败由调用方回落 Open-Meteo。
package weather

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

// v1 路径（专属 API Host 下，v7 老路径 2027-08-01 停服）。
const (
	qwCurPath   = "/weather/v1/current"
	qwDailyPath = "/weather/v1/daily"
	qwGeoPath   = "/geo/v2/city/lookup"
	qwAlertPath = "/weatheralert/v1/current" // 实时天气预警（v7 warning/now 2026-10-01 已停服）
	qwAirPath   = "/airquality/v1/current"   // 实时空气质量（v7 air/now 同批停服）
)

// 上游 base 为包级变量：仅供测试注入 httptest 地址（生产恒空，URL 由设置的 API Host 构造）。
var (
	qwCurOverride   string
	qwDailyOverride string
	qwGeoOverride   string
)

// normalizeQwHost 设置里存的 API Host：剥 scheme/路径/尾部斜杠（用户可能整段粘贴 URL）。
func normalizeQwHost(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// qwBase 请求 base：测试注入优先；生产按设置的 API Host 构造，未配置给明确错误
// （Get 会回落 Open-Meteo，但提示要能说清原因）。
func qwBase(override, host string) (string, error) {
	if override != "" {
		return strings.TrimSuffix(override, "/"), nil
	}
	h := normalizeQwHost(host)
	if h == "" {
		return "", errors.New("和风需配置 API Host（console.qweather.com 设置页查看）")
	}
	return "https://" + h, nil
}

// ---- 和风天气（结果缓存 10 分钟（523 建/56 改，静默期不判过期），key=经度,纬度） ----

type qwEntry struct {
	cur    *Current
	daily  []Day
	minute string
	warns  []Warning
	idx    []IndexItem
	air    *Air
	at     time.Time
}

var (
	qwMu    sync.Mutex
	qwCache = map[string]qwEntry{}
)

// GetQWeather 和风路径：定位复用 locate（manual 优先/访客 IP）→ current+daily 按经纬度。
func GetQWeather(ps *store.PageSettings, clientIP, key string) (*Resp, error) {
	curBase, err := qwBase(qwCurOverride, ps.WeatherHost)
	if err != nil {
		return nil, err
	}
	dailyBase, err := qwBase(qwDailyOverride, ps.WeatherHost)
	if err != nil {
		return nil, err
	}
	city, lat, lon, source, err := locate(ps, clientIP)
	if err != nil {
		return nil, err
	}
	cur, daily, minute, warns, idx, air, err := qweatherFetch(curBase, dailyBase, key, lat, lon)
	if err != nil {
		return nil, err
	}
	if city == "" {
		city = ps.WeatherCity
	}
	return &Resp{City: city, Lat: lat, Lon: lon, Source: source, Current: cur, Daily: daily,
		Minutely: minute, Warnings: warns, Indices: idx, Air: air}, nil
}

// qwVal v1 数值字段兼容：裸数字或 {"value":n} 两种形态都收（文档示例混用）。
type qwVal struct{ V float64 }

func (v *qwVal) UnmarshalJSON(b []byte) error {
	var n float64
	if err := json.Unmarshal(b, &n); err == nil {
		v.V = n
		return nil
	}
	var o struct {
		Value float64 `json:"value"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("和风数值字段不识别: %s", string(b))
	}
	v.V = o.Value
	return nil
}

func qweatherFetch(curBase, dailyBase, key string, lat, lon float64) (*Current, []Day, string, []Warning, []IndexItem, *Air, error) {
	cacheKey := fmt.Sprintf("%.2f,%.2f", lon, lat)
	qwMu.Lock()
	if e, ok := qwCache[cacheKey]; ok && cacheFresh(e.at) {
		qwMu.Unlock()
		return e.cur, e.daily, e.minute, e.warns, e.idx, e.air, nil
	}
	qwMu.Unlock()

	// v1 路径口径：纬度在前、经度在后，各最多两位小数
	locPath := fmt.Sprintf("%.2f/%.2f", lat, lon)

	// 实况：温度/体感 ℃、湿度 0-1、风速 m/s、气压 hPa、能见度 m、紫外线 0-15；
	// v1 无昼夜与观测时刻字段，白天判定按服务器时间（qwIsDay 兜底口径）
	var curR struct {
		Condition struct {
			Text string `json:"text"`
		} `json:"condition"`
		Temperature qwVal   `json:"temperature"`
		FeelsLike   qwVal   `json:"feelsLike"`
		Humidity    float64 `json:"humidity"`
		Wind        struct {
			Direction struct {
				Degree  *float64 `json:"degree"` // 指针：区分缺省与 0°（正北是有效风向）
				Compass string   `json:"compass"`
			} `json:"direction"`
			Speed qwVal `json:"speed"`
		} `json:"wind"`
		Pressure   qwVal  `json:"pressure"`
		Visibility qwVal  `json:"visibility"`
		UvIndex    float64 `json:"uvIndex"`
	}
	if err := getJSONKeyed(curBase+qwCurPath+"/"+locPath+"?localTime=true&lang=zh", key, &curR); err != nil {
		return nil, nil, "", nil, nil, nil, fmt.Errorf("和风实况请求失败: %w", err)
	}

	// 逐日：days[] 拆白天/夜间两段，取白天现象与降水概率；日出日落在天级 astro
	var dR struct {
		Days []struct {
			StartTime string `json:"forecastStartTime"`
			Max       qwVal  `json:"temperatureMax"`
			Min       qwVal  `json:"temperatureMin"`
			Daytime   struct {
				Condition struct {
					Text string `json:"text"`
				} `json:"condition"`
				Precip struct {
					Probability qwVal `json:"probability"` // 部分区域可能缺省
				} `json:"precipitation"`
				Wind struct {
					Speed qwVal `json:"speed"` // m/s（与实况同族单位，536）
				} `json:"wind"`
			} `json:"daytime"`
			Astro struct {
				Sunrise string `json:"sunrise"`
				Sunset  string `json:"sunset"`
			} `json:"astro"`
		} `json:"days"`
	}
	if err := getJSONKeyed(dailyBase+qwDailyPath+"/"+locPath+"?days=7&localTime=true&lang=zh", key, &dR); err != nil {
		return nil, nil, "", nil, nil, nil, fmt.Errorf("和风预报请求失败: %w", err)
	}
	if len(dR.Days) == 0 {
		return nil, nil, "", nil, nil, nil, fmt.Errorf("和风天气返回为空")
	}

	hum := curR.Humidity
	if hum >= 0 && hum <= 1 {
		hum *= 100 // v1 是 0-1 占比
	}
	cur := &Current{
		Temp:  curR.Temperature.V,
		Feels: curR.FeelsLike.V,
		Wind:  curR.Wind.Speed.V * 3.6, // m/s → km/h（前端统一 km/h 口径）
		Code:  gaodeToWMO(curR.Condition.Text),
		IsDay: qwIsDay(),
		WindDir: qwDirCN(curR.Wind.Direction.Degree, curR.Wind.Direction.Compass),
	}
	if h := int(hum + 0.5); h >= 0 && h <= 100 {
		cur.Humidity = h
	}
	if p := curR.Pressure.V; p > 0 {
		cur.Pressure = int(p + 0.5)
	}
	if v := curR.Visibility.V / 1000; v > 0 { // m → km
		cur.Visibility = v
	}
	cur.Uv = curR.UvIndex
	d0 := dR.Days[0]
	cur.Sunrise, cur.Sunset = qwHHMM(d0.Astro.Sunrise), qwHHMM(d0.Astro.Sunset)

	daily := make([]Day, 0, len(dR.Days))
	for _, d := range dR.Days {
		day := Day{
			Date: qwDate(d.StartTime),
			Code: gaodeToWMO(d.Daytime.Condition.Text),
			Max:  d.Max.V,
			Min:  d.Min.V,
		}
		if p := d.Daytime.Precip.Probability.V; p > 0 {
			day.Precip = int(p)
		} else {
			day.Precip = -1 // 无降水概率：前端隐藏（同高德口径）
		}
		// 极端天气提示（536）：现象含雪判降雪；白天风速 ≥40km/h（≈6 级）算大风
		if strings.Contains(d.Daytime.Condition.Text, "雪") {
			day.Snow = "有雪"
		}
		if v := d.Daytime.Wind.Speed.V; v > 0 {
			if kmh := v * 3.6; kmh >= 40 {
				day.Wind = fmt.Sprintf("%.0f km/h", kmh)
			}
		}
		daily = append(daily, day)
	}
	// 降水概率兜底（539）：和风对部分位置不下发 probability（恒缺省→-1，
	// 实测用户位置雨日也无数值），用 Open-Meteo 概率按日期对齐补缺——只补
	// <0 的日子，失败静默（定性 雨/雪 兜得住）。forecast() 自带缓存，不多打。
	qwFillPrecip(lat, lon, daily)
	// 扩展数据（524）：分钟级降水/预警/生活指数/空气质量，全部 best-effort：
	// 单项失败静默跳过，不影响主天气（字段缺省前端不渲染）。预警/空气质量走
	// v1 路径（2026-10-10 用户报控制台两 API 100% 报错——v7 warning/air 已于
	// 2026-10-01 在专属 Host 停服）；分钟降水/生活指数的 v1 等价未出、v7 仍通，
	// 维持 v7（经纬度 v7 口径=经度在前、v1=纬度在前）。
	var extras qwExtras
	fetchExtras(curBase, key, lat, lon, &extras) // base 同实况（测试注入/专属 Host 同源）
	qwMu.Lock()
	if len(qwCache) > 200 {
		qwCache = map[string]qwEntry{}
	}
	qwCache[cacheKey] = qwEntry{cur: cur, daily: daily, minute: extras.minute, warns: extras.warns, idx: extras.idx, air: extras.air, at: nowFn()}
	qwMu.Unlock()
	return cur, daily, extras.minute, extras.warns, extras.idx, extras.air, nil
}

// IDX_DROP_KW 面板不展示的生活指数关键词（2026-10-09 用户指定；2026-10-11 追加运动/防晒）。
var IDX_DROP_KW = regexp.MustCompile(`钓鱼|旅游|空气污染|空调|太阳镜|化妆|晾晒|运动|防晒`)

// qwExtras 524 扩展数据容器。
type qwExtras struct {
	minute string
	warns  []Warning
	idx    []IndexItem
	air    *Air
}

// fetchExtras 四路扩展并行拉取（best-effort，单路失败静默跳过）。串行时单次
// 冷缓存最坏 4×10s 超时挂住页面请求，并行压到单路上限；各 goroutine 只写
// 自己的字段、共享仅只读的 base/key/坐标，无锁也安全。
// qwFillPrecip Open-Meteo 降水概率按日期对齐兜底（539）：和风 probability
// 缺发（-1）的日子用 OM 的 precipitation_probability_max 补，两边日期同为
// YYYY-MM-DD（OM timezone=auto、和风 localTime 切日均按当地）。OM 失败或无
// 该日数据保持 -1（前端回落定性 雨/雪）。高德 adcode 无经纬度不适用。
func qwFillPrecip(lat, lon float64, daily []Day) {
	missing := false
	for i := range daily {
		if daily[i].Precip < 0 {
			missing = true
			break
		}
	}
	if !missing {
		return
	}
	_, omDaily, err := forecast(lat, lon)
	if err != nil {
		return
	}
	om := make(map[string]int, len(omDaily))
	for _, d := range omDaily {
		om[d.Date] = d.Precip
	}
	for i := range daily {
		if daily[i].Precip < 0 {
			if v, ok := om[daily[i].Date]; ok && v >= 0 {
				daily[i].Precip = v
			}
		}
	}
}

func fetchExtras(base, key string, lat, lon float64, out *qwExtras) {
	locV7 := fmt.Sprintf("%.2f,%.2f", lon, lat)
	locV1 := fmt.Sprintf("%.2f/%.2f", lat, lon)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		defer func() {
			if p := recover(); p != nil {
				log.Printf("和风分钟级降水拉取 panic（本轮缺失）: %v", p)
			}
		}()
		var mR struct {
			Summary string `json:"summary"`
		}
		if err := getJSONKeyed(base+"/v7/minutely/5m?location="+url.QueryEscape(locV7)+"&lang=zh", key, &mR); err == nil && mR.Summary != "" {
			out.minute = mR.Summary
		}
	}()
	go func() {
		defer wg.Done()
		defer func() {
			if p := recover(); p != nil {
				log.Printf("和风预警拉取 panic（本轮缺失）: %v", p)
			}
		}()
		// v1 预警：alerts[] 按生命周期字段给全量，title=headline、text=description
		//（防御指南 instruction 未含则补一段）；等级 color 优先、缺色回落 severity 档。
		var wR struct {
			Alerts []struct {
				ID          string `json:"id"`
				Sender      string `json:"sender"`
				Onset       string `json:"onset"`
				Expires     string `json:"expires"`
				StartTime   string `json:"startTime"`
				EndTime     string `json:"endTime"`
				Headline    string `json:"headline"`
				Description string `json:"description"`
				Instruction string `json:"instruction"`
				Severity    string `json:"severity"`
				EventType   struct {
					Code string `json:"code"`
					Name string `json:"name"`
				} `json:"eventType"`
				Color *struct {
					Code string `json:"code"`
					Name string `json:"name"`
				} `json:"color"`
			} `json:"alerts"`
		}
		if err := getJSONKeyed(base+qwAlertPath+"/"+locV1+"?lang=zh", key, &wR); err != nil {
			return
		}
		for _, a := range wR.Alerts {
			text := a.Description
			if a.Instruction != "" && !strings.Contains(text, a.Instruction) {
				if text != "" {
					text += "\n"
				}
				text += a.Instruction
			}
			ccode := ""
			if a.Color != nil {
				ccode = a.Color.Code
			}
			start, end := a.Onset, a.Expires
			if start == "" {
				start = a.StartTime
			}
			if end == "" {
				end = a.EndTime
			}
			out.warns = append(out.warns, Warning{
				Title: a.Headline, Text: text, Type: a.EventType.Name,
				Level: qwAlertLevel(ccode, a.Severity),
				ID: a.ID, Sender: a.Sender, Start: start, End: end,
			})
		}
	}()
	go func() {
		defer wg.Done()
		defer func() {
			if p := recover(); p != nil {
				log.Printf("和风生活指数拉取 panic（本轮缺失）: %v", p)
			}
		}()
		var iR struct {
			Daily []struct {
				Name     string `json:"name"`
				Level    string `json:"level"`
				Category string `json:"category"`
				Text     string `json:"text"`
			} `json:"daily"`
		}
		if err := getJSONKeyed(base+"/v7/indices/1d?type=0&location="+url.QueryEscape(locV7)+"&lang=zh", key, &iR); err == nil {
			for _, it := range iR.Daily {
				// 用户点名不要的指数（2026-10-09）：钓鱼/旅游/空气污染扩散/空调开启/太阳镜/化妆/晾晒
				if it.Name == "" || IDX_DROP_KW.MatchString(it.Name) {
					continue
				}
				out.idx = append(out.idx, IndexItem{Name: it.Name, Level: it.Category, Text: it.Text})
			}
		}
	}()
	go func() {
		defer wg.Done()
		defer func() {
			if p := recover(); p != nil {
				log.Printf("和风空气质量拉取 panic（本轮缺失）: %v", p)
			}
		}()
		// v1 空气：indexes[] 按各国/地区标准一行一个，中国场景 cn 优先、回落
		// 和风通用 qaqi；aqi 为数值（v7 是字符串已废）；空气优时无首要污染物。
		var aR struct {
			Indexes []struct {
				Code             string `json:"code"`
				Aqi              int    `json:"aqi"`
				Category         string `json:"category"`
				PrimaryPollutant *struct {
					Code string `json:"code"`
					Name string `json:"name"`
				} `json:"primaryPollutant"`
			} `json:"indexes"`
		}
		if err := getJSONKeyed(base+qwAirPath+"/"+locV1+"?lang=zh", key, &aR); err != nil {
			return
		}
		pick, fallback := -1, -1
		for i, ix := range aR.Indexes {
			switch ix.Code {
			case "cn":
				pick = i
			case "qaqi":
				fallback = i
			}
		}
		if pick < 0 {
			pick = fallback
		}
		if pick < 0 || aR.Indexes[pick].Aqi <= 0 {
			return
		}
		ix := aR.Indexes[pick]
		primary := ""
		if ix.PrimaryPollutant != nil {
			primary = ix.PrimaryPollutant.Name
		}
		out.air = &Air{Aqi: ix.Aqi, Category: ix.Category, Primary: primary}
	}()
	wg.Wait()
}

// 预警等级中文化（v1 无 v7 的中文 level 字段）：色码蓝白黄橙红优先，
// 缺色按 severity 四档映射同级颜色，都识别不了原样返回 severity。
var qwColorCN = map[string]string{"blue": "蓝色", "white": "白色", "yellow": "黄色", "orange": "橙色", "red": "红色"}
var qwSevCN = map[string]string{"minor": "蓝色", "moderate": "黄色", "severe": "橙色", "extreme": "红色"}

func qwAlertLevel(colorCode, severity string) string {
	if v, ok := qwColorCN[strings.ToLower(strings.TrimSpace(colorCode))]; ok {
		return v
	}
	if v, ok := qwSevCN[strings.ToLower(strings.TrimSpace(severity))]; ok {
		return v
	}
	return severity
}

// qwIsDay 和风 v1 实况无昼夜字段：按服务器当前小时判断，6-19 点算白天
// （部署在国内时与目标城市时区一致；面板中国区场景够用）。
func qwIsDay() bool {
	t := time.Now()
	return t.Hour() >= 6 && t.Hour() < 19
}

// qwHHMM v1 日出日落兼容 "HH:MM" 与 ISO8601（取时间部分前 5 字符）。
func qwHHMM(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, 'T'); i >= 0 && len(s) >= i+6 {
		return s[i+1 : i+6]
	}
	return s
}

// qwDate forecastStartTime（ISO 或 YYYY-MM-DD）取日期段。
func qwDate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// qwDirCN v1 风向无中文文本：compass 缩写/degree 换算八方位中文（北风/东南风…）。
// degree 为指针：nil=上游缺省（返回空），0° 是有效风向（正北）不能当无数据。
func qwDirCN(deg *float64, compass string) string {
	m := map[string]string{"n": "北", "ne": "东北", "e": "东", "se": "东南",
		"s": "南", "sw": "西南", "w": "西", "nw": "西北"}
	if compass != "" {
		if v, ok := m[strings.ToLower(strings.TrimSpace(compass))]; ok {
			return v + "风"
		}
	}
	if deg == nil {
		return ""
	}
	names := []string{"北", "东北", "东", "东南", "南", "西南", "西", "西北"}
	i := int(math.Mod(*deg+22.5, 360) / 45)
	if i < 0 || i > 7 {
		return ""
	}
	return names[i] + "风"
}

// ---- 和风城市搜索 ----

// SearchQWeatherCities 和风城市搜索（GeoAPI city/lookup 走同一专属 Host，候选带经纬度，≤5 条）。
func SearchQWeatherCities(host, name, key string) ([]City, error) {
	base, err := qwBase(qwGeoOverride, host)
	if err != nil {
		return nil, err
	}
	var r struct {
		Code     string `json:"code"`
		Location []struct {
			Name    string `json:"name"`
			Adm1    string `json:"adm1"`
			Country string `json:"country"`
			Lat     string `json:"lat"`
			Lon     string `json:"lon"`
		} `json:"location"`
	}
	u := base + qwGeoPath + "?location=" + url.QueryEscape(name) + "&number=5&lang=zh"
	if err := getJSONKeyed(u, key, &r); err != nil {
		return nil, fmt.Errorf("和风城市搜索失败: %w", err)
	}
	if r.Code != "" && r.Code != "200" {
		return nil, fmt.Errorf("和风城市搜索报错: code=%s", r.Code)
	}
	out := make([]City, 0, len(r.Location))
	for _, it := range r.Location {
		lat, _ := strconv.ParseFloat(strings.TrimSpace(it.Lat), 64)
		lon, _ := strconv.ParseFloat(strings.TrimSpace(it.Lon), 64)
		if lat == 0 && lon == 0 {
			continue
		}
		label := it.Name
		var extra []string
		if it.Adm1 != "" && it.Adm1 != it.Name {
			extra = append(extra, it.Adm1)
		}
		if it.Country != "" {
			extra = append(extra, it.Country)
		}
		if len(extra) > 0 {
			label = it.Name + " · " + strings.Join(extra, " · ")
		}
		out = append(out, City{Name: it.Name, Label: label, Lat: lat, Lon: lon})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("和风未找到匹配城市")
	}
	return out, nil
}
