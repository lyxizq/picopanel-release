package weather

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

func withUpstreams(t *testing.T, forecast, geocode, ipAPI, ipWhois string) {
	t.Helper()
	restore := SetUpstreams(forecast, geocode, ipAPI, ipWhois)
	t.Cleanup(restore)
	ClearGaodeCaches() // 含 Open-Meteo 预报缓存（-count 重跑隔离）
}

func openWeatherStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(t.TempDir()+"/w.db", t.TempDir()+"/key")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustSaveSettings(t *testing.T, s *store.Store, p *store.PageSettings) {
	t.Helper()
	if err := s.SavePageSettings(p); err != nil {
		t.Fatal(err)
	}
}

// TestGetManual 手动城市：取库内经纬度 + Open-Meteo 解析（实况/7 日+扩展字段）。
func TestGetManual(t *testing.T) {
	var q string
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":24.5,"apparent_temperature":23.1,
			"relative_humidity_2m":60,"weather_code":3,"wind_speed_10m":12.5,"wind_direction_10m":135,
			"pressure_msl":1013.2,"is_day":1},
			"hourly":{"visibility":[12000,12000,12000,12000,12000,12000,12000,12000,12000,12000,12000,12000,
				12000,12000,12000,12000,12000,12000,12000,12000,12000,12000,12000,12000]},
			"daily":{"time":["2026-10-02","2026-10-03","2026-10-04","2026-10-05","2026-10-06","2026-10-07","2026-10-08"],
			"weather_code":[61,80,0,3,61,95,2],
			"temperature_2m_max":[26,28,29,27,26,25,27],"temperature_2m_min":[18,19,20,19,18,17,18],
			"precipitation_probability_max":[70,20,0,0,10,30,0],
			"snowfall_sum":[0,0,0,0,0,0,1.5],
			"wind_gusts_10m_max":[20,45,15,10,18,22,52],
			"uv_index_max":[6.5,7,6.8,5,4,3.5,6],
			"sunrise":["2026-10-02T06:12","2026-10-03T06:12","2026-10-04T06:12","2026-10-05T06:12","2026-10-06T06:12","2026-10-07T06:12","2026-10-08T06:12"],
			"sunset":["2026-10-02T17:45","2026-10-03T17:45","2026-10-04T17:45","2026-10-05T17:45","2026-10-06T17:45","2026-10-07T17:45","2026-10-08T17:45"]}}`))
	}))
	defer fc.Close()
	withUpstreams(t, fc.URL, "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")

	s := openWeatherStore(t)
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "北京", WeatherLat: 39.9, WeatherLon: 116.4})
	ps, _ := s.GetPageSettings()
	r, err := Get(ps, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if r.City != "北京" || r.Source != "manual" {
		t.Fatalf("定位口径不对: %+v", r)
	}
	if r.Current.Temp != 24.5 || r.Current.Code != 3 || !r.Current.IsDay {
		t.Fatalf("实况不一致: %+v", r.Current)
	}
	if r.Current.WindDir != "东南" || r.Current.Pressure != 1013 || r.Current.Visibility != 12 ||
		r.Current.Uv != 6.5 || r.Current.Sunrise != "06:12" || r.Current.Sunset != "17:45" {
		t.Fatalf("实况扩展字段不一致: %+v", r.Current) // 135°→东南；能见度取当前小时 m→km
	}
	if len(r.Daily) != 7 || r.Daily[0].Max != 26 || r.Daily[0].Precip != 70 || r.Daily[6].Date != "2026-10-08" {
		t.Fatalf("预报不一致: %+v", r.Daily)
	}
	// 极端天气（536）：次日阵风 45 过阈值、末日雪 1.5cm + 阵风 52、首日无雪无风
	if r.Daily[1].Wind != "阵风 45 km/h" || r.Daily[1].Snow != "" ||
		r.Daily[6].Snow != "1.5 cm" || r.Daily[6].Wind != "阵风 52 km/h" || r.Daily[0].Snow != "" || r.Daily[0].Wind != "" {
		t.Fatalf("降雪/大风判定不对: %+v", r.Daily)
	}
	if !strings.Contains(q, "latitude=39.9000") || !strings.Contains(q, "timezone=auto") || !strings.Contains(q, "forecast_days=7") {
		t.Fatalf("请求参数不对: %s", q)
	}
}

// TestLocateFallback 自动定位失败（本机/内网 IP 短路，不打公网）回落手动配置；
// 两者皆无报错。
func TestLocateFallback(t *testing.T) {
	withUpstreams(t, "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	s := openWeatherStore(t)

	// auto 模式 + 本机 IP + 无手动配置 → 报错
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "auto"})
	ps, _ := s.GetPageSettings()
	if _, _, _, _, err := locate(ps, "127.0.0.1"); err == nil {
		t.Fatal("无任何定位来源应报错")
	}
	// auto 失败回落手动配置
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "auto",
		WeatherCity: "上海", WeatherLat: 31.2, WeatherLon: 121.5})
	ps, _ = s.GetPageSettings()
	city, lat, lon, source, err := locate(ps, "192.168.1.5")
	if err != nil || city != "上海" || source != "manual" || lat != 31.2 || lon != 121.5 {
		t.Fatalf("应回落手动配置: %v %v %v %v %v", city, lat, lon, source, err)
	}
	// manual 模式直接用配置
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "上海", WeatherLat: 31.2, WeatherLon: 121.5})
	ps, _ = s.GetPageSettings()
	if _, _, _, source, err = locate(ps, "1.2.3.4"); err != nil || source != "manual" {
		t.Fatalf("manual 模式应直接用配置: %v %v", source, err)
	}
}

// TestIPLocate 缓存行为与上游解析（ip-api 主 + ipwho.is 备）。
func TestIPLocate(t *testing.T) {
	hits := 0
	ipAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"status":"success","city":"杭州市","lat":30.27,"lon":120.16}`))
	}))
	defer ipAPI.Close()
	withUpstreams(t, "http://127.0.0.1:1", "http://127.0.0.1:1", ipAPI.URL+"/", "http://127.0.0.1:1")

	geoMu.Lock()
	geoCache = map[string]geoEntry{}
	geoMu.Unlock()

	city, lat, lon, ok := ipLocate("114.114.114.114")
	if !ok || city != "杭州市" || lat != 30.27 || lon != 120.16 {
		t.Fatalf("ip-api 解析不一致: %v %v %v %v", city, lat, lon, ok)
	}
	ipLocate("114.114.114.114") // 命中缓存
	if hits != 1 {
		t.Fatalf("第二次应走缓存，实际打上游 %d 次", hits)
	}
	// 内网地址短路
	if _, _, _, ok := ipLocate("10.0.0.1"); ok {
		t.Fatal("内网 IP 不应查询")
	}
}

// TestSearchCity 城市搜索候选与 Label 拼装。
func TestSearchCity(t *testing.T) {
	gc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") != "zh" || r.URL.Query().Get("name") != "北京" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"results":[
			{"name":"北京市","latitude":39.9,"longitude":116.4,"country":"中国","admin1":"北京市"},
			{"name":"北京","latitude":44.5,"longitude":-73.2,"country":"美国","admin1":"佛蒙特州"}
		]}`))
	}))
	defer gc.Close()
	withUpstreams(t, "http://127.0.0.1:1", gc.URL, "http://127.0.0.1:1", "http://127.0.0.1:1")

	cities, err := SearchCity("北京", &store.PageSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cities) != 2 || cities[0].Name != "北京市" || cities[0].Lat != 39.9 {
		t.Fatalf("候选不一致: %+v", cities)
	}
	if cities[0].Label != "北京市 · 中国" { // admin1 与同名不重复拼
		t.Fatalf("Label 拼装不对: %q", cities[0].Label)
	}
	if cities[1].Label != "北京 · 佛蒙特州 · 美国" {
		t.Fatalf("Label 拼装不对: %q", cities[1].Label)
	}
}

// TestUpstreamError 上游 5xx/坏 JSON 报错不 panic。
func TestUpstreamError(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	withUpstreams(t, bad.URL, bad.URL, bad.URL, bad.URL)
	s := openWeatherStore(t)
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "北京", WeatherLat: 10.1, WeatherLon: 20.2}) // 与 TestGetManual 不同坐标，避开预报缓存
	ps, _ := s.GetPageSettings()
	if _, err := Get(ps, "8.8.8.8"); err == nil {
		t.Fatal("上游 5xx 应报错")
	}
}

// ---- 高德服务商（314） ----

func withGaode(t *testing.T, weather, geo, ip string) {
	t.Helper()
	restore := SetUpGaode(weather, geo, ip)
	t.Cleanup(restore)
	ClearGaodeCaches()
}

// gaodeUpstreams 起 高德 weather/geocode/ip 三个 mock：weather 校验 key+adcode，
// 按 extensions 返回实况/4 日预报；geocode 城市→adcode+中心点；ip 返回 adcode+矩形。
func gaodeUpstreams(t *testing.T) (wx, geo, ipSrv *httptest.Server) {
	wx = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qv := r.URL.Query()
		if qv.Get("key") != "test-key" || qv.Get("city") == "" || qv.Get("extensions") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if qv.Get("extensions") == "base" {
			_, _ = w.Write([]byte(`{"status":"1","info":"OK","lives":[{"weather":"多云","temperature":"23",
				"winddirection":"东","windpower":"≤3","humidity":"72","reporttime":"2026-10-02 14:00:00"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"1","info":"OK","forecasts":[{"city":"杭州市","casts":[
			{"date":"2026-10-02","dayweather":"小雨","nightweather":"阴","daytemp":"24","nighttemp":"18","daypower":"1-3"},
			{"date":"2026-10-03","dayweather":"雷阵雨","nightweather":"雷阵雨","daytemp":"27","nighttemp":"20","daypower":"7-8"},
			{"date":"2026-10-04","dayweather":"晴","nightweather":"晴","daytemp":"29","nighttemp":"21","daypower":"≤3"},
			{"date":"2026-10-05","dayweather":"雨夹雪","nightweather":"多云","daytemp":"28","nighttemp":"20","daypower":"≤3"}]}]}`))
	}))
	geo = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qv := r.URL.Query()
		if qv.Get("key") != "test-key" || qv.Get("address") != "杭州" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"status":"1","geocodes":[{"adcode":"330100","city":"杭州市",
			"location":"120.15,30.27","formatted_address":"浙江省杭州市"}]}`))
	}))
	ipSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qv := r.URL.Query()
		if qv.Get("key") != "test-key" || qv.Get("ip") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"status":"1","province":"浙江省","city":"杭州市","adcode":"330101",
			"rectangle":"119.90,30.10;120.40,30.40"}`))
	}))
	t.Cleanup(func() { wx.Close(); geo.Close(); ipSrv.Close() })
	return
}

// TestGaodeManual 高德 manual：城市名 geocode 换 adcode → 实况+预报（现象映射 WMO、
// 风级转 km/h、无降水概率=-1、只取 3 日）。
func TestGaodeManual(t *testing.T) {
	wx, geo, ipSrv := gaodeUpstreams(t)
	withGaode(t, wx.URL, geo.URL, ipSrv.URL)

	s := openWeatherStore(t)
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "杭州", WeatherLat: 30.27, WeatherLon: 120.15,
		WeatherProvider: "gaode", WeatherKey: "test-key"})
	ps, _ := s.GetPageSettings()
	r, err := Get(ps, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if r.City != "杭州" || r.Current == nil || len(r.Daily) != 4 {
		t.Fatalf("高德返回不对: %+v", r) // 上游 4 日全量返回
	}
	if r.Current.Temp != 23 || r.Current.Code != 2 || r.Current.Humidity != 72 || r.Current.Wind != 15 {
		t.Fatalf("实况映射不对: %+v", r.Current) // 多云→2；≤3 级→15km/h
	}
	if r.Daily[0].Code != 51 || r.Daily[1].Code != 95 || r.Daily[2].Code != 0 {
		t.Fatalf("预报现象映射不对: %+v", r.Daily) // 小雨→51/雷阵雨→95/晴→0
	}
	// 极端天气（536）：daypower 7-8 级过阈值；雨夹雪→有雪；首日 1-3 级不算
	if r.Daily[1].Wind != "7-8级" || r.Daily[3].Snow != "有雪" || r.Daily[0].Wind != "" {
		t.Fatalf("高德降雪/大风判定不对: %+v", r.Daily)
	}
	if r.Daily[0].Max != 24 || r.Daily[0].Min != 18 || r.Daily[0].Precip != -1 {
		t.Fatalf("预报温区/降水概率占位不对: %+v", r.Daily[0])
	}
}

// TestGaodeAutoIP 高德 auto：公网 IP 走高德 IP 定位直出 adcode（城市名去「市」尾）。
func TestGaodeAutoIP(t *testing.T) {
	_, geo, ipSrv := gaodeUpstreams(t)
	var askedAdcode string
	wx2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qv := r.URL.Query()
		askedAdcode = qv.Get("city")
		w.Header().Set("Content-Type", "application/json")
		if qv.Get("extensions") == "base" {
			_, _ = w.Write([]byte(`{"status":"1","lives":[{"weather":"晴","temperature":"20",
				"windpower":"≤3","humidity":"50","reporttime":"2026-10-02 08:00:00"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"1","forecasts":[{"city":"杭州市","casts":[
			{"date":"2026-10-02","dayweather":"晴","nightweather":"晴","daytemp":"26","nighttemp":"17","daypower":"≤3"}]}]}`))
	}))
	t.Cleanup(wx2.Close)
	withGaode(t, wx2.URL, geo.URL, ipSrv.URL)

	s := openWeatherStore(t)
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "auto",
		WeatherProvider: "gaode", WeatherKey: "test-key"})
	ps, _ := s.GetPageSettings()
	r, err := Get(ps, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if askedAdcode != "330101" {
		t.Fatalf("应按 IP 定位的 adcode 取数: %q", askedAdcode)
	}
	if r.City != "杭州" {
		t.Fatalf("IP 定位城市去「市」尾: %q", r.City)
	}
}

// TestGaodeFallback 高德上游失败 / 未配 Key → 回落 Open-Meteo。
func TestGaodeFallback(t *testing.T) {
	_, geo, ipSrv := gaodeUpstreams(t)
	wxErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(wxErr.Close)
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":11,"weather_code":61,"is_day":1},
			"daily":{"time":["2026-10-02"],"weather_code":[61],"temperature_2m_max":[15],
			"temperature_2m_min":[8],"precipitation_probability_max":[80]}}`))
	}))
	t.Cleanup(fc.Close)
	withUpstreams(t, fc.URL, "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	withGaode(t, wxErr.URL, geo.URL, ipSrv.URL)

	s := openWeatherStore(t)
	// 坐标用上海（避开 TestGetManual 北京坐标的预报缓存）
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "上海", WeatherLat: 31.13, WeatherLon: 121.29,
		WeatherProvider: "gaode", WeatherKey: "test-key"})
	ps, _ := s.GetPageSettings()
	r, err := Get(ps, "8.8.8.8") // 高德 5xx → 回落 Open-Meteo
	if err != nil {
		t.Fatal(err)
	}
	if r.Current.Code != 61 || r.Current.Temp != 11 {
		t.Fatalf("应回落 Open-Meteo 数据: %+v", r.Current)
	}

	// 未配 Key：不进高德路径，直接 Open-Meteo
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "上海", WeatherLat: 31.13, WeatherLon: 121.29, WeatherProvider: "gaode"})
	ps, _ = s.GetPageSettings()
	if r, err = Get(ps, "8.8.8.8"); err != nil || r.Current.Code != 61 {
		t.Fatalf("无 Key 应走 Open-Meteo: %+v err=%v", r, err)
	}
}

// TestGaodeSearchCities 高德城市搜索：候选带 adcode 与全称 Label。
func TestGaodeSearchCities(t *testing.T) {
	withUpstreams(t, "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1") // 封掉 Open-Meteo 真网
	_, geo, _ := gaodeUpstreams(t)
	withGaode(t, "http://127.0.0.1:1", geo.URL, "http://127.0.0.1:1")
	cities, err := SearchCity("杭州", &store.PageSettings{WeatherProvider: "gaode", WeatherKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cities) != 1 || cities[0].Adcode != "330100" || cities[0].Name != "杭州" || cities[0].Lat != 30.27 {
		t.Fatalf("候选不对: %+v", cities)
	}
	if cities[0].Label != "浙江省杭州市" {
		t.Fatalf("Label 应为全称: %q", cities[0].Label)
	}
	// 高德失败回落 Open-Meteo（此处上游不通 → 报错即可，不 panic）
	if _, err := SearchCity("北京", &store.PageSettings{WeatherProvider: "gaode", WeatherKey: "test-key"}); err == nil {
		t.Fatal("高德搜索失败且无 Open-Meteo 上游时应报错")
	}
}

// ---- 和风服务商（320） ----

func withQWeather(t *testing.T, now, daily, geo string) {
	t.Helper()
	restore := SetUpQWeather(now, daily, geo)
	t.Cleanup(restore)
	ClearGaodeCaches() // 含和风缓存
}

// qweatherUpstreams 起 和风 current/daily/geo 三个 mock（518 v1 口径，校验 X-QW-Api-Key
// 请求头与「纬度/经度」路径序）：current 返回实况（数值混用 裸数字/{value} 两形态），
// daily 返回 7 日（首日带降水概率/次日缺省、ISO 日出日落与日期段混排），geo 城市搜索。
func qweatherUpstreams(t *testing.T) (now, d7, geo *httptest.Server) {
	now = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-QW-Api-Key") != "qw-key" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/weather/v1/current/30.27/120.15"):
			_, _ = w.Write([]byte(`{"condition":{"text":"多云"},"temperature":{"value":23},"feelsLike":{"value":22},
				"humidity":0.72,"wind":{"direction":{"degree":135,"compass":"se"},"speed":{"value":5}},
				"pressure":{"value":1008},"visibility":{"value":25000},"uvIndex":5}`))
		case strings.HasSuffix(r.URL.Path, "/weatheralert/v1/current/30.27/120.15"):
			_, _ = w.Write([]byte(`{"alerts":[{"headline":"市气象台发布大风蓝色预警信号","description":"预计未来6小时有5-6级大风。",
				"instruction":"妥善安置阳台物品。","severity":"minor","eventType":{"code":"wind","name":"大风"},
				"color":{"code":"blue","name":"蓝色"}}]}`))
		case strings.HasSuffix(r.URL.Path, "/airquality/v1/current/30.27/120.15"):
			_, _ = w.Write([]byte(`{"indexes":[{"code":"cn","aqi":37,"category":"优","primaryPollutant":null},
				{"code":"qaqi","aqi":29,"category":"Excellent","primaryPollutant":{"code":"pm10","name":"PM10"}}]}`))
		case r.URL.Path == "/v7/minutely/5m":
			_, _ = w.Write([]byte(`{"summary":"未来两小时无降雨，放心出门吧。"}`))
		case r.URL.Path == "/v7/indices/1d":
			_, _ = w.Write([]byte(`{"daily":[
				{"name":"运动","level":"1","category":"适宜","text":"天气不错，适合运动。"},
				{"name":"钓鱼","level":"3","category":"一般","text":"不宜钓鱼。"},
				{"name":"防晒","level":"3","category":"一般","text":"涂擦防晒护肤品。"},
				{"name":"洗车","level":"2","category":"较适宜","text":"洗车后可保持两天。"}]}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	d7 = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-QW-Api-Key") != "qw-key" || !strings.HasSuffix(r.URL.Path, "/weather/v1/daily/30.27/120.15") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"days":[
			{"forecastStartTime":"2026-10-03T00:00+08:00","temperatureMax":{"value":24},"temperatureMin":{"value":18},
			 "daytime":{"condition":{"text":"小雨"},"precipitation":{"probability":{"value":60}}},"astro":{"sunrise":"2026-10-03T06:05+08:00","sunset":"2026-10-03T17:45+08:00"}},
			{"forecastStartTime":"2026-10-04","temperatureMax":27,"temperatureMin":19,
			 "daytime":{"condition":{"text":"小雪"},"precipitation":{"amount":{"value":0}}},"astro":{"sunrise":"06:06","sunset":"17:44"}},
			{"forecastStartTime":"2026-10-05","temperatureMax":26,"temperatureMin":19,"daytime":{"condition":{"text":"多云"},"wind":{"speed":{"value":12}}}},
			{"forecastStartTime":"2026-10-06","temperatureMax":24,"temperatureMin":18,"daytime":{"condition":{"text":"阴"},"wind":{"speed":{"value":3}}}},
			{"forecastStartTime":"2026-10-07","temperatureMax":23,"temperatureMin":17,"daytime":{"condition":{"text":"雷阵雨"}}},
			{"forecastStartTime":"2026-10-08","temperatureMax":22,"temperatureMin":17,"daytime":{"condition":{"text":"小雨"}}},
			{"forecastStartTime":"2026-10-09","temperatureMax":25,"temperatureMin":18,"daytime":{"condition":{"text":"晴"}}}]}`))
	}))
	geo = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-QW-Api-Key") != "qw-key" || r.URL.Query().Get("location") != "杭州" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"code":"200","location":[
			{"name":"杭州","id":"101210101","lat":"30.27408","lon":"120.15507","adm2":"杭州","adm1":"浙江省","country":"中国"}]}`))
	}))
	t.Cleanup(func() { now.Close(); d7.Close(); geo.Close() })
	return
}

// TestQWeatherManual 和风 manual：定位复用（manual 城市）→ v1 current+daily 按
// 「纬度/经度」直查，实况映射（风速 m/s→km/h、湿度 0-1→%、能见度 m→km、compass
// →中文风向）/ISO 日出日落切片/无降水概率置 -1/7 日全量。
func TestQWeatherManual(t *testing.T) {
	now, d7, geo := qweatherUpstreams(t)
	withQWeather(t, now.URL, d7.URL, geo.URL)
	// OM 概率兜底上游（539）：本地 mock，10-02..10-08 概率 [70,20,0,0,10,30,0]
	//（和风 daily 从 10-03 起：10-04→0/10-05→0/10-06→10/10-07→30/10-08→0，
	// 10-09 无对齐保持 -1）；daily 各数组必须齐全——forecast 解析直接下标访问
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":23,"apparent_temperature":22,"relative_humidity_2m":72,
			"weather_code":2,"wind_speed_10m":18,"wind_direction_10m":135,"pressure_msl":1013.2,"is_day":1},
			"daily":{"time":["2026-10-02","2026-10-03","2026-10-04","2026-10-05","2026-10-06","2026-10-07","2026-10-08"],
			"weather_code":[61,80,0,3,61,95,2],
			"temperature_2m_max":[26,28,29,27,26,25,27],"temperature_2m_min":[18,19,20,19,18,17,18],
			"precipitation_probability_max":[70,20,0,0,10,30,0],
			"snowfall_sum":[0,0,0,0,0,0,1.5],
			"wind_gusts_10m_max":[20,45,15,10,18,22,52]}}`))
	}))
	t.Cleanup(fc.Close)
	withUpstreams(t, fc.URL, "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")

	s := openWeatherStore(t)
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "杭州", WeatherLat: 30.27, WeatherLon: 120.15,
		WeatherProvider: "qweather", WeatherKey: "qw-key", WeatherHost: "h2demo.qweatherapi.com"})
	ps, _ := s.GetPageSettings()
	r, err := Get(ps, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if r.City != "杭州" || r.Source != "manual" || r.Current == nil || len(r.Daily) != 7 {
		t.Fatalf("和风返回不对: %+v", r)
	}
	c := r.Current
	wantDay := time.Now().Hour() >= 6 && time.Now().Hour() < 19 // v1 无观测时刻，按服务器时间判昼夜
	if c.Temp != 23 || c.Feels != 22 || c.Code != 2 || c.IsDay != wantDay || c.Wind != 18 {
		t.Fatalf("实况映射不对: %+v", c) // 多云→2；5 m/s→18 km/h
	}
	if c.WindDir != "东南风" || c.Pressure != 1008 || c.Visibility != 25 ||
		c.Uv != 5 || c.Sunrise != "06:05" || c.Sunset != "17:45" {
		t.Fatalf("扩展字段不对: %+v", c)
	}
	// 539 概率兜底：和风 -1 的日子按日期对齐填 OM 值（10-04→0/10-05→0/
	// 10-06→10/10-07→30/10-08→0；10-09 OM 无该日保持 -1）
	if r.Daily[0].Precip != 60 || r.Daily[1].Precip != 0 || r.Daily[4].Precip != 30 ||
		r.Daily[5].Precip != 0 || r.Daily[6].Precip != -1 || r.Daily[1].Code != 71 || r.Daily[6].Date != "2026-10-09" {
		t.Fatalf("7 日预报不对: %+v", r.Daily) // 60% 保留；小雪→71；OM 对齐补概率
	}
	// 极端天气（536）：次日小雪→有雪；第三日风 12m/s=43.2km/h 过阈值；第四日 3m/s 不算
	if r.Daily[1].Snow != "有雪" || r.Daily[2].Wind != "43 km/h" || r.Daily[3].Wind != "" {
		t.Fatalf("降雪/大风判定不对: %+v", r.Daily)
	}
	// 四路扩展（526 起 warning/air 走 v1）：预警映射与等级中文化、空气 cn 标准优先、
	// 指数过滤钓鱼、分钟降水透传
	if len(r.Warnings) != 1 {
		t.Fatalf("预警应 1 条: %+v", r.Warnings)
	}
	if wl := r.Warnings[0]; wl.Title != "市气象台发布大风蓝色预警信号" || wl.Type != "大风" || wl.Level != "蓝色" ||
		!strings.Contains(wl.Text, "5-6级大风") || !strings.Contains(wl.Text, "妥善安置") {
		t.Fatalf("预警映射不对: %+v", wl)
	}
	if r.Air == nil || r.Air.Aqi != 37 || r.Air.Category != "优" || r.Air.Primary != "" {
		t.Fatalf("空气应取 cn 标准（aqi=37、无首要污染物）: %+v", r.Air)
	}
	if len(r.Indices) != 1 || r.Indices[0].Name != "洗车" {
		t.Fatalf("生活指数应过滤钓鱼/运动/防晒留洗车: %+v", r.Indices)
	}
	if !strings.Contains(r.Minutely, "无降雨") {
		t.Fatalf("分钟降水应透传: %q", r.Minutely)
	}
}

// TestQWeatherFallback 和风上游 5xx 回落 Open-Meteo；未配 Key 不走和风。
func TestQWeatherFallback(t *testing.T) {
	nowErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(nowErr.Close)
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":20,"weather_code":0,"is_day":1},
			"daily":{"time":["2026-10-03"],"weather_code":[0],"temperature_2m_max":[26],"temperature_2m_min":[17]}}`))
	}))
	t.Cleanup(fc.Close)
	withUpstreams(t, fc.URL, "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	withQWeather(t, nowErr.URL, "http://127.0.0.1:1", "http://127.0.0.1:1")

	s := openWeatherStore(t)
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "西安", WeatherLat: 34.26, WeatherLon: 108.94, // 独立坐标避开 open-meteo 缓存
		WeatherProvider: "qweather", WeatherKey: "qw-key"})
	ps, _ := s.GetPageSettings()
	r, err := Get(ps, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if r.Current == nil || r.Current.Temp != 20 || r.Current.Code != 0 {
		t.Fatalf("和风失败应回落 Open-Meteo: %+v", r)
	}
	// 未配 Key：直接走 Open-Meteo（坐标缓存命中同一 mock）
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "西安", WeatherLat: 34.26, WeatherLon: 108.94, WeatherProvider: "qweather"})
	ps, _ = s.GetPageSettings()
	if r, err = Get(ps, "8.8.8.8"); err != nil || r.Current == nil || r.Current.Temp != 20 {
		t.Fatalf("无 Key 应直接 Open-Meteo: %+v %v", r, err)
	}
}

// TestQWeatherSearch 和风城市搜索：候选带经纬度与全称 Label。
func TestQWeatherSearch(t *testing.T) {
	withUpstreams(t, "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	_, _, geo := qweatherUpstreams(t)
	withQWeather(t, "http://127.0.0.1:1", "http://127.0.0.1:1", geo.URL)
	cities, err := SearchCity("杭州", &store.PageSettings{WeatherProvider: "qweather", WeatherKey: "qw-key", WeatherHost: "h2demo.qweatherapi.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cities) != 1 || cities[0].Name != "杭州" || cities[0].Lat != 30.27408 || cities[0].Lon != 120.15507 {
		t.Fatalf("候选不对: %+v", cities)
	}
	if cities[0].Label != "杭州 · 浙江省 · 中国" {
		t.Fatalf("Label 不对: %q", cities[0].Label)
	}
	// 和风失败回落 Open-Meteo（此处上游不通 → 报错即可，不 panic）
	if _, err := SearchCity("北京", &store.PageSettings{WeatherProvider: "qweather", WeatherKey: "qw-key", WeatherHost: "h2demo.qweatherapi.com"}); err == nil {
		t.Fatal("和风搜索失败且无 Open-Meteo 上游时应报错")
	}
}

// TestQWeatherNoHost 未配 API Host（override 也为空）：直接报明确错误——host 参与
// 鉴权，缺失属配置问题，不能静默回落 Open-Meteo 混淆排查；顺带回归 normalizeQwHost
// 整段粘贴 URL 取裸 host。
func TestQWeatherNoHost(t *testing.T) {
	if _, err := SearchQWeatherCities("", "北京", "qw-key"); err == nil || !strings.Contains(err.Error(), "API Host") {
		t.Fatalf("未配 API Host 应报错: %v", err)
	}
	ps := &store.PageSettings{WeatherProvider: "qweather", WeatherKey: "qw-key", WeatherMode: "manual",
		WeatherCity: "杭州", WeatherLat: 30.27, WeatherLon: 120.15}
	if _, err := GetQWeather(ps, "8.8.8.8", "qw-key"); err == nil || !strings.Contains(err.Error(), "API Host") {
		t.Fatalf("取数未配 API Host 应报错: %v", err)
	}
	if h := normalizeQwHost(" https://h2demo.qweatherapi.com/some/path/ "); h != "h2demo.qweatherapi.com" {
		t.Fatalf("normalizeQwHost 不对: %q", h)
	}
}

// TestQwDirCN 风向换算：0° 是有效风向（正北）不能当无数据，nil/compass 缺失
// 各走各的分支。
func TestQwDirCN(t *testing.T) {
	p := func(f float64) *float64 { return &f }
	cases := []struct {
		deg     *float64
		compass string
		want    string
	}{
		{nil, "", ""},
		{p(0), "", "北风"},
		{p(350), "", "北风"},
		{p(135), "", "东南风"},
		{p(202.5), "", "西南风"},
		{nil, "se", "东南风"},
		{p(0), "n", "北风"},
	}
	for _, c := range cases {
		if got := qwDirCN(c.deg, c.compass); got != c.want {
			t.Fatalf("qwDirCN(%v,%q)=%q, want %q", c.deg, c.compass, got, c.want)
		}
	}
}

// TestQuietHours 夜间静默（56）：边界含头不含尾；静默期冷启动放行一次、过期缓存
// 不判过期直接回旧值（上游打爆也零调用）、5:30 起恢复取数；采样静默期跳过、
// 白天落库且带完整 Resp JSON。
func TestQuietHours(t *testing.T) {
	for _, c := range []struct {
		h, m  int
		quiet bool
	}{
		{0, 0, true}, {5, 29, true}, {5, 30, false}, {12, 0, false}, {23, 59, false},
	} {
		if got := inQuietHours(time.Date(2026, 10, 20, c.h, c.m, 0, 0, time.Local)); got != c.quiet {
			t.Fatalf("inQuietHours %02d:%02d = %v, want %v", c.h, c.m, got, c.quiet)
		}
	}

	restoreNow := nowFn
	t.Cleanup(func() { nowFn = restoreNow })
	var hits int
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":25,"apparent_temperature":24,
			"relative_humidity_2m":60,"weather_code":0,"wind_speed_10m":10,"wind_direction_10m":315,
			"pressure_msl":1010,"is_day":1},"hourly":{"visibility":[10000]},
			"daily":{"time":["2026-10-20"],"weather_code":[0],"temperature_2m_max":[28],
			"temperature_2m_min":[19],"precipitation_probability_max":[10],"uv_index_max":[3],
			"sunrise":["2026-10-20T06:12"],"sunset":["2026-10-20T17:30"],
			"snowfall_sum":[0],"wind_gusts_10m_max":[20]}}`))
	}))
	defer fc.Close()
	withUpstreams(t, fc.URL, "http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	s := openWeatherStore(t)
	mustSaveSettings(t, s, &store.PageSettings{WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "北京", WeatherLat: 39.9, WeatherLon: 116.4})
	ps, _ := s.GetPageSettings()

	// 凌晨 4:00（静默期）冷启动：空缓存放行一次自愈
	nowFn = func() time.Time { return time.Date(2026, 10, 20, 4, 0, 0, 0, time.Local) }
	r, err := Get(ps, "")
	if err != nil || r.Current.Temp != 25 {
		t.Fatalf("静默期冷启动应放行取数: %v %+v", err, r)
	}
	if hits != 1 {
		t.Fatalf("冷启动应恰好打一次上游: %d", hits)
	}

	// 4:11（已过 TTL 且仍在静默期）：回旧缓存零上游
	nowFn = func() time.Time { return time.Date(2026, 10, 20, 4, 11, 0, 0, time.Local) }
	hits = 0
	if r, err = Get(ps, ""); err != nil || r.Current.Temp != 25 {
		t.Fatalf("静默期应回旧缓存: %v %+v", err, r)
	}
	if hits != 0 {
		t.Fatalf("静默期过期缓存不应打上游: %d", hits)
	}

	// 5:30 静默结束：重新取数
	nowFn = func() time.Time { return time.Date(2026, 10, 20, 5, 30, 0, 0, time.Local) }
	if r, err = Get(ps, ""); err != nil || r.Current.Temp != 25 {
		t.Fatalf("静默结束应恢复取数: %v %+v", err, r)
	}
	if hits != 1 {
		t.Fatalf("静默结束应重打上游: %d", hits)
	}

	// 采样：静默期跳过不落库，白天落库且带完整 Resp JSON（HA 胶囊读库口粮）
	nowFn = func() time.Time { return time.Date(2026, 10, 20, 3, 0, 0, 0, time.Local) }
	sampleOnce(s)
	if sm, _ := s.LatestWXSample("39.90,116.40"); sm != nil {
		t.Fatalf("静默期采样应跳过: %+v", sm)
	}
	nowFn = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local) }
	sampleOnce(s)
	sm, err := s.LatestWXSample("39.90,116.40")
	if err != nil || sm == nil {
		t.Fatalf("白天采样应落库: %v %+v", err, sm)
	}
	if sm.Temp != 25 || !strings.Contains(sm.Data, `"temp":25`) || !strings.Contains(sm.Data, `"city":"北京"`) {
		t.Fatalf("采样应带完整 Resp JSON: %+v", sm)
	}
}
