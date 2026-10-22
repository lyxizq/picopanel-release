package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/integrations"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/weather"
)

// newIntegrationRouter 真实链路测试服务：真实 Register（含鉴权组）+ 集成管理器 +
// mock 上游，返回引擎与登录 token。upstream 为 qBittorrent 假上游地址。
func newIntegrationRouter(t *testing.T) (*gin.Engine, *store.Store, string, string, *atomic.Int64) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal("查管理员失败")
	}
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	hits := &atomic.Int64{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = r.ParseForm()
		if r.PostFormValue("username") == "admin" && r.PostFormValue("password") == "pw" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		_, _ = w.Write([]byte("Fails."))
	})
	mux.HandleFunc("/api/v2/transfer/info", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"dl_info_speed":1024,"up_info_speed":128}`))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"state":"downloading"},{"state":"uploading"}]`))
	})
	up := httptest.NewServer(mux)
	t.Cleanup(up.Close)

	r := gin.New()
	if err := Register(r, s, nil, &config.Config{}, "test", nil, integrations.New(s), nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err, nil)
	}
	return r, s, token, up.URL, hits
}

func doJSON(r *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	return w
}

func idPath(v int64) string { return strconv.FormatInt(v, 10) }

// TestIntegrationAPIChain 真实链路：创建（校验/凭据不回显）→ 列表 → 编辑留空沿用
// → data 快照 → 测试 → 排序 → 删除。
func TestIntegrationAPIChain(t *testing.T) {
	r, s, token, up, _ := newIntegrationRouter(t)

	// 类型非法 400
	w := doJSON(r, http.MethodPost, "/api/v1/integrations", token,
		`{"type":"ftp","name":"x","url":"http://x","auth":{"username":"a","password":"b"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法类型应 400: %d %s", w.Code, w.Body.String())
	}

	// 地址非法 400
	w = doJSON(r, http.MethodPost, "/api/v1/integrations", token,
		`{"type":"qbittorrent","name":"x","url":"ftp://x","auth":{"username":"a","password":"b"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法地址应 400: %d", w.Code)
	}

	// 缺凭据 400
	w = doJSON(r, http.MethodPost, "/api/v1/integrations", token,
		`{"type":"qbittorrent","name":"x","url":"`+up+`"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "账号密码") {
		t.Fatalf("缺凭据应 400: %d %s", w.Code, w.Body.String())
	}

	// 正常创建
	body := `{"type":"qbittorrent","name":"PT保种","url":"` + up + `","auth":{"username":"admin","password":"pw"},"config":{"insecure":false}}`
	w = doJSON(r, http.MethodPost, "/api/v1/integrations", token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("创建应成功: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Integration integrationView `json:"integration"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	in := created.Integration
	if in.ID == 0 || !in.HasAuth || in.Enabled != true {
		t.Fatalf("创建视图不对: %+v", in)
	}

	// 凭据绝不出现在任何响应里
	for _, bad := range []string{`"pw"`, `"password"`, `"username"`} {
		if strings.Contains(w.Body.String(), bad) {
			t.Fatalf("创建响应泄露凭据字段 %q: %s", bad, w.Body.String())
		}
	}

	// 打开地址（464）：非法 scheme 拒绝，合法的随 config 落库
	w = doJSON(r, http.MethodPost, "/api/v1/integrations", token,
		`{"type":"docker","name":"dk","config":{"sock":"/tmp/x.sock","open_url":"javascript:alert(1)"}}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "打开地址") {
		t.Fatalf("非法打开地址应 400: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(r, http.MethodPost, "/api/v1/integrations", token,
		`{"type":"docker","name":"dk","config":{"sock":"/tmp/x.sock","open_url":"https://dk.example.com"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("合法打开地址应过: %d %s", w.Code, w.Body.String())
	}
	var dk struct {
		Integration integrationView `json:"integration"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dk); err != nil || dk.Integration.ID == 0 {
		t.Fatalf("docker 创建响应解析失败: %v %s", err, w.Body.String())
	}
	w = doJSON(r, http.MethodDelete, "/api/v1/integrations/"+idPath(dk.Integration.ID), token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("清理 docker 桩失败: %d", w.Code)
	}

	// 列表：config 原样、has_auth=true、无凭据
	w = doJSON(r, http.MethodGet, "/api/v1/integrations", token, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"has_auth":true`) {
		t.Fatalf("列表不对: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"pw"`) {
		t.Fatal("列表响应泄露凭据")
	}

	// 编辑：凭据留空沿用（库内仍是 admin/pw，data 快照能登录成功即证明）
	w = doJSON(r, http.MethodPut, "/api/v1/integrations/"+idPath(in.ID), token,
		`{"type":"qbittorrent","name":"PT","url":"`+up+`","enabled":true,"config":{}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("编辑应成功: %d %s", w.Code, w.Body.String())
	}
	old, _ := s.GetIntegration(in.ID)
	if old.Auth != `{"username":"admin","password":"pw"}` {
		t.Fatalf("留空凭据应沿用: %q", old.Auth)
	}
	if old.Name != "PT" {
		t.Fatalf("名称应更新: %+v", old)
	}

	// data 快照（Kick 异步抓取可能未完成，All() 的 current 会同步补拉兜底）
	w = doJSON(r, http.MethodGet, "/api/v1/integrations/data", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("data 应成功: %d %s", w.Code, w.Body.String())
	}
	var dataRes struct {
		Integrations []struct {
			ID   int64          `json:"id"`
			OK   bool           `json:"ok"`
			Data map[string]any `json:"data"`
		} `json:"integrations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dataRes); err != nil {
		t.Fatal(err)
	}
	if len(dataRes.Integrations) != 1 || !dataRes.Integrations[0].OK {
		t.Fatalf("快照应成功: %s", w.Body.String())
	}
	if dataRes.Integrations[0].Data["download_bps"].(float64) != 1024 {
		t.Fatalf("速率不一致: %v", dataRes.Integrations[0].Data)
	}

	// 测试端点：好凭据 ok；坏凭据 400
	w = doJSON(r, http.MethodPost, "/api/v1/integrations/test", token,
		`{"type":"qbittorrent","url":"`+up+`","auth":{"username":"admin","password":"pw"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("测试应成功: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(r, http.MethodPost, "/api/v1/integrations/test", token,
		`{"type":"qbittorrent","url":"`+up+`","auth":{"username":"nope","password":"nope"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("坏凭据测试应 400: %d %s", w.Code, w.Body.String())
	}

	// 排序 + 删除
	w = doJSON(r, http.MethodPut, "/api/v1/integrations/order", token, `{"ids":[`+idPath(in.ID)+`]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("排序应成功: %d", w.Code)
	}
	w = doJSON(r, http.MethodDelete, "/api/v1/integrations/"+idPath(in.ID), token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("删除应成功: %d", w.Code)
	}
	w = doJSON(r, http.MethodGet, "/api/v1/integrations/data", token, "")
	if strings.Contains(w.Body.String(), `"id":`+idPath(in.ID)) {
		t.Fatal("删除后 data 不应再含该集成")
	}
}

// TestIntegrationHAConfigKeysPreserved（473 报障）：服务抽屉编辑 HA 集成时新
// config 只带 entities/open_url，/ha 页面维护的 rooms/devices/sensors 必须原样
// 保留（355 只保 devices、rooms/sensors 被冲掉=房间丢失根因）；显式传入的键
// 仍以传入为准（全量保存可删），空 config 同样保留。
func TestIntegrationHAConfigKeysPreserved(t *testing.T) {
	r, s, token, _, _ := newIntegrationRouter(t)

	w := doJSON(r, http.MethodPost, "/api/v1/integrations", token,
		`{"type":"homeassistant","name":"HA","url":"http://127.0.0.1:8123","auth":{"token":"tok"},
		"config":{"entities":[{"entity":"light.a"}],"rooms":[{"id":"r1","name":"客厅"}],
		"devices":[{"entity":"light.a","room":"r1"}],"sensors":[{"entity":"sensor.t"}]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("创建 HA 应成功: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Integration integrationView `json:"integration"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := idPath(created.Integration.ID)

	// 服务抽屉形态：config 只带 entities + open_url
	w = doJSON(r, http.MethodPut, "/api/v1/integrations/"+id, token,
		`{"type":"homeassistant","name":"HA","url":"http://127.0.0.1:8123","enabled":true,
		"config":{"entities":[{"entity":"light.b"}],"open_url":"https://ha.example.com"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("编辑应成功: %d %s", w.Code, w.Body.String())
	}
	in, err := s.GetIntegration(created.Integration.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"devices":[{"entity":"light.a","room":"r1"}],"entities":[{"entity":"light.b"}],"open_url":"https://ha.example.com","rooms":[{"id":"r1","name":"客厅"}],"sensors":[{"entity":"sensor.t"}]}`
	if in.Config != want {
		t.Fatalf("三键应保留: got %s", in.Config)
	}

	// 全量保存形态：显式 rooms:[] 删除生效（不再复活）
	w = doJSON(r, http.MethodPut, "/api/v1/integrations/"+id, token,
		`{"type":"homeassistant","name":"HA","url":"http://127.0.0.1:8123","enabled":true,
		"config":{"entities":[],"rooms":[]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("全量保存应成功: %d %s", w.Code, w.Body.String())
	}
	in, _ = s.GetIntegration(created.Integration.ID)
	if !strings.Contains(in.Config, `"rooms":[]`) {
		t.Fatalf("显式清空 rooms 应生效: %s", in.Config)
	}
	if !strings.Contains(in.Config, `"devices"`) || !strings.Contains(in.Config, `"sensors"`) {
		t.Fatalf("未传的 devices/sensors 仍应保留: %s", in.Config)
	}
}

// TestIntegrationWeatherEndpoints 天气端点挂载与关闭分支：未开启时 400；
// 手动城市 + mock 上游出数；城市搜索走真实 handler。
func TestIntegrationWeatherEndpoints(t *testing.T) {
	fc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":25,"weather_code":0,"is_day":1},
			"daily":{"time":["2026-10-02"],"weather_code":[0],"temperature_2m_max":[28],
			"temperature_2m_min":[19],"precipitation_probability_max":[0]}}`))
	}))
	defer fc.Close()
	gc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"name":"北京市","latitude":39.9,"longitude":116.4,"country":"中国","admin1":"北京市"}]}`))
	}))
	defer gc.Close()
	restore := weather.SetUpstreams(fc.URL, gc.URL+"/", "http://127.0.0.1:1", "http://127.0.0.1:1")
	defer restore()

	r, s, token, _, _ := newIntegrationRouter(t)

	// 未开启 400
	w := doJSON(r, http.MethodGet, "/api/v1/page/weather", token, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("天气未开启应 400: %d %s", w.Code, w.Body.String())
	}

	// 开启（手动城市）后出数
	p, _ := s.GetPageSettings()
	p.WeatherEnabled = true
	p.WeatherMode = "manual"
	p.WeatherCity = "北京"
	p.WeatherLat, p.WeatherLon = 39.9, 116.4
	if err := s.SavePageSettings(p); err != nil {
		t.Fatal(err)
	}
	w = doJSON(r, http.MethodGet, "/api/v1/page/weather", token, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"city":"北京"`) {
		t.Fatalf("天气应成功: %d %s", w.Code, w.Body.String())
	}

	// 城市搜索（上游挂空地址会失败，仅验证链路与参数校验分支）
	w = doJSON(r, http.MethodGet, "/api/v1/page/weather/city", token, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺 name 应 400: %d", w.Code)
	}
	w = doJSON(r, http.MethodGet, "/api/v1/page/weather/city?name=%E5%8C%97%E4%BA%AC", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("搜索应 200（geocode 指向可 mock 地址才成；此处验证非 5xx 链路）: %d %s", w.Code, w.Body.String())
	}

	// 历史曲线（522）：手动位置落两条采样（另一位置一条防串键），全链路断言
	// 路由→handler→store 拼接；hours 超界钳到 90 天
	now := time.Now().Unix()
	for _, sm := range []store.WXSample{
		{Loc: "39.90,116.40", Temp: 25.5, Humidity: 60, Wind: 10, Pressure: 1010, TS: now - 600},
		{Loc: "39.90,116.40", Temp: 26.5, Humidity: 62, Wind: 12, Pressure: 1011, TS: now - 60},
		{Loc: "31.13,121.29", Temp: 30, Humidity: 70, Wind: 8, Pressure: 1008, TS: now - 60},
	} {
		if err := s.RecordWXSample(sm); err != nil {
			t.Fatal(err)
		}
	}
	// 小区间（step≤1s 不分桶）应出本位置 2 点升序；超界区间钳 90 天后均桶只
	// 断言 200（两点落同桶成均值，桶数随墙钟浮动不硬断言）
	w = doJSON(r, http.MethodGet, "/api/v1/page/weather/history?hours=1", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("历史应 200: %d %s", w.Code, w.Body.String())
	}
	var hist struct {
		Points []store.WXPoint `json:"points"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &hist); err != nil {
		t.Fatal(err)
	}
	if len(hist.Points) != 2 || hist.Points[0].Temp != 25.5 || hist.Points[1].Temp != 26.5 {
		t.Fatalf("历史点不对（应只含本位置 2 点升序）: %+v", hist.Points)
	}
	w = doJSON(r, http.MethodGet, "/api/v1/page/weather/history?hours=99999", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("超界钳 90 天后仍应 200: %d %s", w.Code, w.Body.String())
	}
}

// TestWeatherLatestEndpoint GET /page/weather/latest（56，HA 胶囊读库直出）：
// 未开启 400；无可用采样回 weather:null；只回当前位置最新一条带 Resp JSON 的采样，
// 旧版空 data 采样与他位置采样不串。
func TestWeatherLatestEndpoint(t *testing.T) {
	r, s, token, _, _ := newIntegrationRouter(t)

	// 未开启 400
	w := doJSON(r, http.MethodGet, "/api/v1/page/weather/latest", token, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("天气未开启应 400: %d %s", w.Code, w.Body.String())
	}

	p, _ := s.GetPageSettings()
	p.WeatherEnabled = true
	p.WeatherMode = "manual"
	p.WeatherCity = "北京"
	p.WeatherLat, p.WeatherLon = 39.9, 116.4
	if err := s.SavePageSettings(p); err != nil {
		t.Fatal(err)
	}

	// 无采样 → weather:null
	w = doJSON(r, http.MethodGet, "/api/v1/page/weather/latest", token, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"weather":null`) {
		t.Fatalf("无采样应 200+null: %d %s", w.Code, w.Body.String())
	}

	// 升级前旧采样（无 data）+ 他位置采样 + 本位置带 data 的最新采样
	now := time.Now().Unix()
	for _, sm := range []store.WXSample{
		{Loc: "39.90,116.40", Temp: 20, Humidity: 50, Wind: 5, Pressure: 1005, TS: now - 1200},
		{Loc: "31.13,121.29", Temp: 30, Humidity: 70, Wind: 8, Pressure: 1008, TS: now - 60,
			Data: `{"city":"上海","current":{"temp":30}}`},
		{Loc: "39.90,116.40", Temp: 26.5, Humidity: 62, Wind: 12, Pressure: 1011, TS: now - 60,
			Data: `{"city":"北京","provider":"qweather","current":{"temp":26.5,"wind_dir":"西北"}}`},
	} {
		if err := s.RecordWXSample(sm); err != nil {
			t.Fatal(err)
		}
	}
	w = doJSON(r, http.MethodGet, "/api/v1/page/weather/latest", token, "")
	if w.Code != http.StatusOK {
		t.Fatalf("latest 应 200: %d %s", w.Code, w.Body.String())
	}
	var lr struct {
		TS      int64           `json:"ts"`
		Weather json.RawMessage `json:"weather"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil {
		t.Fatal(err)
	}
	if lr.TS != now-60 {
		t.Fatalf("ts 应为最新采样落库时刻: %d", lr.TS)
	}
	body := string(lr.Weather)
	if !strings.Contains(body, `"city":"北京"`) || !strings.Contains(body, `"wind_dir":"西北"`) {
		t.Fatalf("weather 应为本位置最新采样原样透传: %s", body)
	}
	if strings.Contains(body, "上海") {
		t.Fatalf("他位置采样不应串键: %s", body)
	}
}
