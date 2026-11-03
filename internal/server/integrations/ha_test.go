package integrations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"lz-monitor/internal/server/store"
)

// TestHAStateOn 开态语义（355）：各域判定不同——cover 看 open/opening、lock 看
// unlocked、vacuum 看 cleaning、climate/media_player 非 off 类、其余看 on/off；
// unavailable/unknown 一律离线。
func TestHAStateOn(t *testing.T) {
	cases := []struct {
		domain, state string
		on            bool
		unavailable   bool
	}{
		{"light", "on", true, false},
		{"light", "off", false, false},
		{"switch", "on", true, false},
		{"cover", "open", true, false},
		{"cover", "opening", true, false},
		{"cover", "closed", false, false},
		{"lock", "unlocked", true, false},
		{"lock", "locked", false, false},
		{"lock", "jammed", false, false},
		{"vacuum", "cleaning", true, false},
		{"vacuum", "docked", false, false},
		{"vacuum", "returning", true, false},
		{"climate", "cool", true, false},
		{"climate", "off", false, false},
		{"media_player", "playing", true, false},
		{"media_player", "paused", true, false},
		{"media_player", "standby", false, false},
		{"fan", "on", true, false},
		{"light", "unavailable", false, true},
		{"switch", "unknown", false, true},
		{"fan", "", false, true},
	}
	for _, c := range cases {
		s := HAState{EntityID: c.domain + ".x", Domain: c.domain, State: c.state}
		if s.On() != c.on || s.Unavailable() != c.unavailable {
			t.Fatalf("%s/%s: on=%v(want %v) unavail=%v(want %v)",
				c.domain, c.state, s.On(), c.on, s.Unavailable(), c.unavailable)
		}
	}
}

// TestHACall 服务调用（355）：动作映射到域内真服务（lock/cover/vacuum 无
// turn_on/turn_off）、toggle 先读回判向、非可控域拒绝、返回读回状态。
func TestHACall(t *testing.T) {
	mu := sync.Mutex{}
	lightState := "off"
	var lastService string

	mux := http.NewServeMux()
	entityJSON := func(id, state string) string {
		b, _ := json.Marshal(map[string]any{
			"entity_id": id, "state": state,
			"attributes": map[string]string{"friendly_name": "测试"},
		})
		return string(b)
	}
	mux.HandleFunc("/api/states/light.ceiling", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write([]byte(entityJSON("light.ceiling", lightState)))
	})
	mux.HandleFunc("/api/states/lock.door", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(entityJSON("lock.door", "locked")))
	})
	mux.HandleFunc("/api/states/cover.win", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(entityJSON("cover.win", "closed")))
	})
	mux.HandleFunc("/api/services/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastService = r.URL.Path
		lightState = "on"
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	mux.HandleFunc("/api/states", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		s := entityJSON("light.ceiling", lightState)
		mu.Unlock()
		_, _ = w.Write([]byte("[" + s + "]"))
	})
	up := httptest.NewServer(mux)
	defer up.Close()

	in := store.Integration{Type: store.IntegrationHA, URL: up.URL, Auth: `{"token":"t"}`}

	// turn_on → light/turn_on，读回 on
	st, err := HACall(in, "light.ceiling", "turn_on")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	svc := lastService
	mu.Unlock()
	if svc != "/api/services/light/turn_on" {
		t.Fatalf("应调用 light/turn_on: %s", svc)
	}
	if !st.On() || st.State != "on" {
		t.Fatalf("读回应 on: %+v", st)
	}

	// toggle：当前 on → 应转 turn_off
	if _, err := HACall(in, "light.ceiling", "toggle"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	svc = lastService
	mu.Unlock()
	if svc != "/api/services/light/turn_off" {
		t.Fatalf("toggle 应判向为 turn_off: %s", svc)
	}

	// lock/cover 域映射真服务
	if _, err := HACall(in, "lock.door", "turn_on"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	svc = lastService
	mu.Unlock()
	if svc != "/api/services/lock/lock" {
		t.Fatalf("lock turn_on 应映射 lock/lock: %s", svc)
	}
	if _, err := HACall(in, "cover.win", "turn_off"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	svc = lastService
	mu.Unlock()
	if svc != "/api/services/cover/close_cover" {
		t.Fatalf("cover turn_off 应映射 cover/close_cover: %s", svc)
	}

	// 非可控域与非法动作拒绝
	if _, err := HACall(in, "sensor.temp", "turn_on"); err == nil || !strings.Contains(err.Error(), "实体域") {
		t.Fatalf("sensor 应被拒: %v", err)
	}
	if _, err := HACall(in, "light.ceiling", "self_destruct"); err == nil {
		t.Fatal("非法动作应被拒")
	}
}

// TestFetchHAStates 快照 states 键含全部实体（368 扩为全量，原 355 只收可控域）：
// sensor 等只读实体进状态表供 /ha 状态实体与目录候选，可控域实体供设备行，
// 主页 items 口径不受影响。
func TestFetchHAStates(t *testing.T) {
	list := []map[string]any{
		{"entity_id": "light.ceiling", "state": "on", "attributes": map[string]string{"friendly_name": "客厅灯"}},
		{"entity_id": "sensor.temp", "state": "22.5", "attributes": map[string]string{"unit_of_measurement": "°C"}},
		{"entity_id": "binary_sensor.door", "state": "off", "attributes": map[string]string{}},
		{"entity_id": "switch.plug", "state": "off", "attributes": map[string]string{}},
	}
	b, _ := json.Marshal(list)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/states", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(b)
	})
	up := httptest.NewServer(mux)
	defer up.Close()

	in := store.Integration{Type: store.IntegrationHA, URL: up.URL,
		Auth: `{"token":"t"}`, Config: `{"entities":[{"entity":"sensor.temp","label":"温度"}]}`}
	data, err := fetchHA(in)
	if err != nil {
		t.Fatal(err)
	}
	states, ok := data["states"].(map[string]HAState)
	if !ok {
		t.Fatalf("states 类型不对: %T", data["states"])
	}
	if len(states) != 4 {
		t.Fatalf("应含全部 4 个实体: %v", states)
	}
	if states["light.ceiling"].Name != "客厅灯" || states["switch.plug"].State != "off" {
		t.Fatalf("states 内容不对: %v", states)
	}
	// 只读实体（sensor/binary_sensor）进表，带单位与域
	st := states["sensor.temp"]
	if st.State != "22.5" || st.Unit != "°C" || st.Domain != "sensor" {
		t.Fatalf("sensor 实体状态不符: %+v", st)
	}
	if _, has := states["binary_sensor.door"]; !has {
		t.Fatal("binary_sensor 应出现在状态表")
	}
	// 主页 items 口径不变
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items 应 1 行: %v", items)
	}
}

// TestHAEnvAttrs 环境电器属性透传（388）：climate/water_heater 带温度组、
// humidifier 带湿度组、fan 带风速百分比，普通域（light）不带任何环境字段。
func TestHAEnvAttrs(t *testing.T) {
	list := []map[string]any{
		{"entity_id": "climate.ac", "state": "cool", "attributes": map[string]any{
			"friendly_name": "客厅空调", "current_temperature": 27.5, "temperature": 26.0,
			"current_humidity": 52.0, "hvac_action": "cooling", "fan_mode": "low",
			"min_temp": 16.0, "max_temp": 30.0,
		}},
		{"entity_id": "humidifier.bed", "state": "on", "attributes": map[string]any{
			"friendly_name": "卧室加湿器", "current_humidity": 41.0, "humidity": 55.0,
			"mode": "auto", "min_humidity": 30.0, "max_humidity": 80.0,
		}},
		{"entity_id": "fan.purifier", "state": "on", "attributes": map[string]any{
			"friendly_name": "净化器", "percentage": 62.0, "preset_mode": "auto",
		}},
		{"entity_id": "light.ceiling", "state": "on", "attributes": map[string]any{"friendly_name": "灯"}},
	}
	b, _ := json.Marshal(list)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/states", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(b)
	})
	up := httptest.NewServer(mux)
	defer up.Close()

	in := store.Integration{Type: store.IntegrationHA, URL: up.URL, Auth: `{"token":"t"}`}
	data, err := fetchHA(in)
	if err != nil {
		t.Fatal(err)
	}
	states := data["states"].(map[string]HAState)

	ac := states["climate.ac"]
	if ac.CurTemp == nil || *ac.CurTemp != 27.5 || ac.TgtTemp == nil || *ac.TgtTemp != 26 ||
		ac.CurHum == nil || *ac.CurHum != 52 || ac.HvacAction != "cooling" || ac.FanMode != "low" ||
		ac.MinTemp == nil || *ac.MinTemp != 16 || ac.MaxTemp == nil || *ac.MaxTemp != 30 {
		t.Fatalf("climate 属性不符: %+v", ac)
	}
	hu := states["humidifier.bed"]
	if hu.CurHum == nil || *hu.CurHum != 41 || hu.TgtHum == nil || *hu.TgtHum != 55 ||
		hu.Preset != "auto" || hu.MinHum == nil || *hu.MinHum != 30 || hu.TgtTemp != nil {
		t.Fatalf("humidifier 属性不符: %+v", hu)
	}
	pu := states["fan.purifier"]
	if pu.Pct == nil || *pu.Pct != 62 || pu.Preset != "auto" || pu.CurTemp != nil {
		t.Fatalf("fan 属性不符: %+v", pu)
	}
	li := states["light.ceiling"]
	if li.CurTemp != nil || li.TgtTemp != nil || li.CurHum != nil || li.TgtHum != nil ||
		li.Pct != nil || li.Preset != "" || li.HvacAction != "" {
		t.Fatalf("light 不应带环境属性: %+v", li)
	}
}

// TestHASetValue 目标值调节（388）：climate → set_temperature 带目标值、读回
// 带新目标；humidifier → set_humidity；不支持的域拒绝。
func TestHASetValue(t *testing.T) {
	mu := sync.Mutex{}
	tgt := 26.0
	var lastPath, lastBody string

	mux := http.NewServeMux()
	entityJSON := func() string {
		b, _ := json.Marshal(map[string]any{
			"entity_id": "climate.ac", "state": "cool",
			"attributes": map[string]any{"current_temperature": 27.5, "temperature": tgt},
		})
		return string(b)
	}
	mux.HandleFunc("/api/states/climate.ac", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write([]byte(entityJSON()))
	})
	mux.HandleFunc("/api/services/climate/set_temperature", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		lastPath = r.URL.Path
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		lastBody = string(buf[:n])
		tgt = 27.5
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	up := httptest.NewServer(mux)
	defer up.Close()

	in := store.Integration{Type: store.IntegrationHA, URL: up.URL, Auth: `{"token":"t"}`}
	st, err := HASetValue(in, "climate.ac", 27.5)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	path, body := lastPath, lastBody
	mu.Unlock()
	if path != "/api/services/climate/set_temperature" {
		t.Fatalf("应调用 climate/set_temperature: %s", path)
	}
	if !strings.Contains(body, `"temperature":27.5`) {
		t.Fatalf("请求体应带目标温度: %s", body)
	}
	if st.TgtTemp == nil || *st.TgtTemp != 27.5 {
		t.Fatalf("读回应带新目标温度: %+v", st)
	}
	if !HASettableDomain("humidifier.x") || HASettableDomain("light.x") {
		t.Fatal("HASettableDomain 判定不对")
	}
	if _, err := HASetValue(in, "light.ceiling", 20); err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("light 应被拒: %v", err)
	}
}
