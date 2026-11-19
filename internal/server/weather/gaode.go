// 高德天气服务商（314）：中国气象局合作数据，国内实况/预报准确度优于全球模型插值。
// 实况 extensions=base + 未来 4 日预报 extensions=all（取前 3 日）；定位 auto 走高德
// IP 定位（直出 adcode，城市级）、manual 按城市名 geocode 换 adcode（24h 缓存）。
// 高德无降水概率 → Day.Precip=-1（前端 >0 才显示，自动隐藏）；现象文本映射 WMO 码
// 复用前端图标/文案体系；无 Key 或上游失败由调用方回落 Open-Meteo。
package weather

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

// 上游端点为包级变量：测试注入 httptest 地址用。
var (
	gaodeWeatherURL = "https://restapi.amap.com/v3/weather/weatherInfo"
	gaodeGeoURL     = "https://restapi.amap.com/v3/geocode/geo"
	gaodeIPURL      = "https://restapi.amap.com/v3/ip"
)

func urlQueryEscape(s string) string { return url.QueryEscape(s) }

// ---- 高德 adcode 解析（24h 缓存，manual 按城市名 / auto 按访客 IP） ----

type adEntry struct {
	adcode string
	city   string
	lat    float64
	lon    float64
	at     time.Time
}

var (
	adMu    sync.Mutex
	adCache = map[string]adEntry{}
)

func adcodeFor(ps *store.PageSettings, clientIP, apiKey string) (adEntry, error) {
	key := "manual:" + ps.WeatherCity
	hasManual := ps.WeatherLat != 0 || ps.WeatherLon != 0
	if !(ps.WeatherMode == "manual" && hasManual) {
		// auto：优先高德 IP 定位（直出 adcode），本机/内网直接跳过
		if p := net.ParseIP(clientIP); p != nil && !p.IsLoopback() && !p.IsPrivate() && !p.IsLinkLocalUnicast() {
			key = "ip:" + clientIP
		} else if hasManual {
			key = "manual:" + ps.WeatherCity
		} else {
			return adEntry{}, fmt.Errorf("高德定位：无法获取访客公网 IP 且未配置手动城市")
		}
	}
	adMu.Lock()
	if e, ok := adCache[key]; ok && time.Since(e.at) < geoTTL {
		adMu.Unlock()
		return e, nil
	}
	adMu.Unlock()

	var e adEntry
	var err error
	if strings.HasPrefix(key, "ip:") {
		e, err = gaodeIPToAdcode(strings.TrimPrefix(key, "ip:"), apiKey)
	} else {
		e, err = gaodeCityToAdcode(ps.WeatherCity, apiKey)
	}
	if err != nil {
		return adEntry{}, err
	}
	adMu.Lock()
	if len(adCache) > 1000 {
		adCache = map[string]adEntry{}
	}
	adCache[key] = e
	adMu.Unlock()
	return e, nil
}

// gaodeCityToAdcode 城市名 → adcode（geocode/geo 首条，含经纬度中心）。
func gaodeCityToAdcode(city, apiKey string) (adEntry, error) {
	var r struct {
		Status   string `json:"status"`
		Geocodes []struct {
			Adcode  string `json:"adcode"`
			City    string `json:"city"`
			Center  string `json:"location"`
			Formatt string `json:"formatted_address"`
		} `json:"geocodes"`
	}
	if err := getJSON(fmt.Sprintf("%s?key=%s&address=%s", gaodeGeoURL, urlQueryEscape(apiKey), urlQueryEscape(city)), &r); err != nil {
		return adEntry{}, fmt.Errorf("高德地理编码失败: %w", err)
	}
	if r.Status != "1" || len(r.Geocodes) == 0 || r.Geocodes[0].Adcode == "" {
		return adEntry{}, fmt.Errorf("高德未找到城市「%s」的 adcode", city)
	}
	g := r.Geocodes[0]
	lat, lon := parseLngLat(g.Center)
	name := strings.TrimSuffix(g.City, "市")
	if name == "" {
		name = city
	}
	return adEntry{adcode: g.Adcode, city: name, lat: lat, lon: lon}, nil
}

// gaodeIPToAdcode 访客 IP → adcode（高德 IP 定位，城市级；矩形对角取中点当经纬度）。
func gaodeIPToAdcode(ip, apiKey string) (adEntry, error) {
	var r struct {
		Status    string `json:"status"`
		Province  string `json:"province"`
		City      string `json:"city"`
		Adcode    string `json:"adcode"`
		Rectangle string `json:"rectangle"`
	}
	if err := getJSON(fmt.Sprintf("%s?key=%s&ip=%s", gaodeIPURL, urlQueryEscape(apiKey), urlQueryEscape(ip)), &r); err != nil {
		return adEntry{}, fmt.Errorf("高德 IP 定位失败: %w", err)
	}
	if r.Status != "1" || r.Adcode == "" {
		return adEntry{}, fmt.Errorf("高德 IP 定位无结果")
	}
	city := strings.TrimSuffix(r.City, "市")
	lat, lon := 0.0, 0.0
	if pair := strings.SplitN(r.Rectangle, ";", 2); len(pair) == 2 {
		lng1, lat1 := parseLngLat(pair[0])
		lng2, lat2 := parseLngLat(pair[1])
		lat, lon = (lat1+lat2)/2, (lng1+lng2)/2
	}
	return adEntry{adcode: r.Adcode, city: city, lat: lat, lon: lon}, nil
}

// ---- 高德天气（结果缓存 10 分钟（523 建/56 改，静默期不判过期），key=adcode） ----

type gdEntry struct {
	cur   *Current
	daily []Day
	at    time.Time
}

var (
	gdMu    sync.Mutex
	gdCache = map[string]gdEntry{}
)

type gdResp struct {
	Status string `json:"status"`
	Info   string `json:"info"`
	Lives  []struct {
		Weather       string `json:"weather"`
		Temperature   string `json:"temperature"`
		WindDirection string `json:"winddirection"`
		WindPower     string `json:"windpower"`
		Humidity      string `json:"humidity"`
		ReportTime    string `json:"reporttime"`
	} `json:"lives"`
	Forecasts []struct {
		City  string `json:"city"`
		Casts []struct {
			Date         string `json:"date"`
			DayWeather   string `json:"dayweather"`
			NightWeather string `json:"nightweather"`
			DayTemp      string `json:"daytemp"`
			NightTemp    string `json:"nighttemp"`
			DayPower     string `json:"daypower"`
		} `json:"casts"`
	} `json:"forecasts"`
}

func gaodeFetch(key, adcode string) (*Current, []Day, error) {
	gdMu.Lock()
	if e, ok := gdCache[adcode]; ok && cacheFresh(e.at) {
		gdMu.Unlock()
		return e.cur, e.daily, nil
	}
	gdMu.Unlock()

	live, err := gdQuery(key, adcode, "base")
	if err != nil {
		return nil, nil, err
	}
	all, err := gdQuery(key, adcode, "all")
	if err != nil {
		return nil, nil, err
	}
	var cur *Current
	if len(live.Lives) > 0 {
		l := live.Lives[0]
		t, _ := strconv.ParseFloat(l.Temperature, 64)
		cur = &Current{
			Temp:     t,
			Feels:    t, // 高德实况无体感
			Humidity: atoiDef(l.Humidity),
			Wind:     beaufortToKmh(l.WindPower),
			Code:     gaodeToWMO(l.Weather),
			IsDay:    isDaytime(l.ReportTime),
		}
	}
	var daily []Day
	if len(all.Forecasts) > 0 {
		casts := all.Forecasts[0].Casts
		daily = make([]Day, 0, len(casts))
		for i, c := range casts {
			if i >= 7 { // 弹窗 7 日（上游 extensions=all 最多 4 日，按返回给）
				break
			}
			day := Day{
				Date:   c.Date,
				Code:   gaodeToWMO(c.DayWeather),
				Max:    atofDef(c.DayTemp),
				Min:    atofDef(c.NightTemp),
				Precip: -1, // 高德不提供降水概率
			}
			// 极端天气提示（536）：现象定性——含雪判降雪；大风/台风现象或白天
			// 风力 ≥6 级算大风（daypower 形如 "3-4"/"≤3"，取数字最大值）
			wtxt := c.DayWeather + c.NightWeather
			if strings.Contains(wtxt, "雪") {
				day.Snow = "有雪"
			}
			if strings.Contains(wtxt, "大风") || strings.Contains(wtxt, "台风") || strings.Contains(wtxt, "飓风") {
				day.Wind = "注意大风"
			} else if lv := gdWindMaxLevel(c.DayPower); lv >= 6 {
				day.Wind = c.DayPower + "级"
			}
			daily = append(daily, day)
		}
	}
	if cur == nil || len(daily) == 0 {
		return nil, nil, fmt.Errorf("高德天气返回为空")
	}
	gdMu.Lock()
	if len(gdCache) > 200 {
		gdCache = map[string]gdEntry{}
	}
	gdCache[adcode] = gdEntry{cur: cur, daily: daily, at: nowFn()}
	gdMu.Unlock()
	return cur, daily, nil
}

func gdQuery(key, adcode, ext string) (*gdResp, error) {
	var r gdResp
	u := fmt.Sprintf("%s?key=%s&city=%s&extensions=%s", gaodeWeatherURL, urlQueryEscape(key), urlQueryEscape(adcode), ext)
	if err := getJSON(u, &r); err != nil {
		return nil, fmt.Errorf("高德天气请求失败: %w", err)
	}
	if r.Status != "1" {
		return nil, fmt.Errorf("高德天气接口报错: %s", r.Info)
	}
	return &r, nil
}

// GetGaode 高德路径：解析 adcode → 实况+预报（定位失败/上游失败由调用方回落 Open-Meteo）。
func GetGaode(ps *store.PageSettings, clientIP, key string) (*Resp, error) {
	e, err := adcodeFor(ps, clientIP, key)
	if err != nil {
		return nil, err
	}
	cur, daily, err := gaodeFetch(key, e.adcode)
	if err != nil {
		return nil, err
	}
	city := e.city
	if city == "" {
		city = ps.WeatherCity
	}
	return &Resp{City: city, Lat: e.lat, Lon: e.lon,
		Source: sourceOf(ps), Current: cur, Daily: daily}, nil
}

func sourceOf(ps *store.PageSettings) string {
	if ps.WeatherMode == "manual" && (ps.WeatherLat != 0 || ps.WeatherLon != 0) {
		return "manual"
	}
	return "ip"
}

// ---- 工具 ----

// SearchGaodeCities 高德城市搜索（geocode/geo，返回带 adcode 的候选，≤5 条）。
func SearchGaodeCities(name, key string) ([]City, error) {
	var r struct {
		Status   string `json:"status"`
		Info     string `json:"info"`
		Geocodes []struct {
			Adcode   string `json:"adcode"`
			District string `json:"district"`
			City     string `json:"city"`
			Formatt  string `json:"formatted_address"`
			Location string `json:"location"`
		} `json:"geocodes"`
	}
	if err := getJSON(gaodeGeoURL+"?key="+urlQueryEscape(key)+"&address="+urlQueryEscape(name), &r); err != nil {
		return nil, fmt.Errorf("高德城市搜索失败: %w", err)
	}
	if r.Status != "1" {
		return nil, fmt.Errorf("高德城市搜索报错: %s", r.Info)
	}
	out := make([]City, 0, len(r.Geocodes))
	for _, g := range r.Geocodes {
		if g.Adcode == "" {
			continue
		}
		lat, lon := parseLngLat(g.Location)
		if lat == 0 && lon == 0 {
			continue
		}
		nm := g.District
		if nm == "" {
			nm = strings.TrimSuffix(g.City, "市")
		}
		if nm == "" {
			nm = g.Formatt
		}
		out = append(out, City{Name: nm, Label: g.Formatt, Lat: lat, Lon: lon, Adcode: g.Adcode})
		if len(out) >= 5 {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("高德未找到匹配城市")
	}
	return out, nil
}

// gaodeToWMO 高德现象文本 → WMO 天气码（前端 wmoIcon/wmoText 直接复用）。
// 按关键词优先级匹配（兼容「小雨-中雨」这类组合串），未识别回落阴（3）。
func gaodeToWMO(s string) int {
	switch {
	case strings.Contains(s, "雷") && strings.Contains(s, "雹"):
		return 99
	case strings.Contains(s, "雷"):
		return 95
	case strings.Contains(s, "暴雪"):
		return 75
	case strings.Contains(s, "雪"):
		if strings.Contains(s, "阵") {
			return 85
		}
		if strings.Contains(s, "小") {
			return 71
		}
		if strings.Contains(s, "中") {
			return 73
		}
		return 75
	case strings.Contains(s, "冻雨"):
		return 56
	case strings.Contains(s, "雨夹雪"):
		return 66
	case strings.Contains(s, "暴雨"):
		return 65
	case strings.Contains(s, "阵雨"):
		return 80
	case strings.Contains(s, "雨"):
		if strings.Contains(s, "小") {
			return 51
		}
		if strings.Contains(s, "中") {
			return 63
		}
		return 65
	case strings.Contains(s, "沙尘") || strings.Contains(s, "沙") || strings.Contains(s, "尘"):
		return 45
	case strings.Contains(s, "雾"):
		return 45
	case strings.Contains(s, "霾"):
		return 45
	case strings.Contains(s, "阴"):
		return 3
	case strings.Contains(s, "云"):
		return 2
	case strings.Contains(s, "晴"):
		return 0
	}
	return 3
}

// beaufortToKmh 高德风级（"≤3"/"1-3"/"5"）→ 近似 km/h（蒲福级中值）。
func beaufortToKmh(s string) float64 {
	s = strings.TrimPrefix(s, "≤")
	levels := []float64{1, 3, 8, 15, 24, 34, 44, 55, 67, 81, 95, 110, 125}
	var nums []float64
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '~' || r == ' ' }) {
		if v, err := strconv.ParseFloat(strings.TrimSpace(part), 64); err == nil {
			nums = append(nums, v)
		}
	}
	if len(nums) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range nums {
		i := int(v)
		if i < 0 {
			i = 0
		}
		if i > 12 {
			i = 12
		}
		sum += levels[i]
	}
	return sum / float64(len(nums))
}

func parseLngLat(s string) (lat, lon float64) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return 0, 0
	}
	lon, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lat, _ = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	return lat, lon
}

func atoiDef(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

func atofDef(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// gdWindMaxLevel daypower 风力串取最大级数（536）：形如 "3-4"/"≤3"/"1-3"，
// 抽出全部数字取最大；无数字返回 0。
func gdWindMaxLevel(s string) int {
	max := 0
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return r < '0' || r > '9'
	}) {
		if v, err := strconv.Atoi(f); err == nil && v > max {
			max = v
		}
	}
	return max
}

// isDaytime 按高德 reporttime（服务器时区）的小时判断昼夜（6-19 点算白天）。
func isDaytime(reporttime string) bool {
	t, err := time.Parse("2006-01-02 15:04:05", reporttime)
	if err != nil {
		t = time.Now()
	}
	return t.Hour() >= 6 && t.Hour() < 19
}
