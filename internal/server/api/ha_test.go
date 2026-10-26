package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/integrations"
	"lz-monitor/internal/server/store"
)

// TestHADevicesSensors 状态实体（368）全链路：SaveDevices 保存 devices+sensors
// 两列（entities 等其他键原样保留）→ Devices 响应带 sensors 行（state/unit 取
// 快照、label 回落友好名）→ 校验（重复/坏实体/超上限 400）。快照来自 httptest
// mock HA 上游（Manager.Current 冷缓存同步补拉）。
func TestHADevicesSensors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "ha.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	// mock HA 上游：可控灯 + 温度/电量传感器
	statesJSON := `[
		{"entity_id":"light.bedroom","state":"on","attributes":{"friendly_name":"卧室灯"}},
		{"entity_id":"sensor.temperature","state":"23.5","attributes":{"friendly_name":"温度","unit_of_measurement":"°C"}},
		{"entity_id":"sensor.battery","state":"85","attributes":{"unit_of_measurement":"%"}}
	]`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/states" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(statesJSON))
	}))
	t.Cleanup(upstream.Close)

	// 智能家居总配置连接（475）；另建服务集成 HA 行（前台卡配置，验证互不影响）
	if err := s.SaveHAMasterConnection(upstream.URL, "t", false); err != nil {
		t.Fatal(err)
	}
	inID, err := s.CreateIntegration(store.IntegrationHA, "前台卡", upstream.URL, `{"token":"t"}`, `{"entities":[{"label":"客厅","entity":"light.bedroom"}]}`)
	if err != nil {
		t.Fatal(err)
	}

	h := &HAHandler{st: s, mgr: integrations.New(s)}
	r := gin.New()
	grp := r.Group("/api/v1")
	grp.GET("/ha/devices", h.Devices)
	grp.PUT("/ha/devices-config", h.SaveDevices)

	do := func(t *testing.T, method, path, body string, want int) string {
		t.Helper()
		w := httptest.NewRecorder()
		var rd *strings.Reader
		if body != "" {
			rd = strings.NewReader(body)
		} else {
			rd = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, rd)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s 状态 %d（期望 %d）: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.String()
	}

	// 保存：1 房间 + 1 可控设备（归房）+ 2 状态实体（其一归房）
	do(t, http.MethodPut, "/api/v1/ha/devices-config",
		`{"source":0,"rooms":[{"id":"r_living","name":"客厅"}],"devices":[{"entity":"light.bedroom","label":"主卧灯","room":"r_living"}],"sensors":[{"entity":"sensor.temperature","label":"客厅温度","room":"r_living"},{"entity":"sensor.battery"}]}`,
		http.StatusOK)

	// 475 互不影响：HA 页保存只进总配置，服务集成 config 不被注入 rooms/devices/sensors
	in, _ := s.GetIntegration(inID)
	if strings.Contains(in.Config, `"rooms"`) || strings.Contains(in.Config, `"devices"`) || strings.Contains(in.Config, `"sensors"`) {
		t.Fatalf("服务集成 config 应不被 HA 页写动: %s", in.Config)
	}

	// Devices：devices 行 + sensors 行（状态/单位/label 回落/房间透传）
	res := do(t, http.MethodGet, "/api/v1/ha/devices?source=0", "", http.StatusOK)
	var view struct {
		OK      bool `json:"ok"`
		Devices []struct {
			Entity string `json:"entity"`
			State  string `json:"state"`
			On     bool   `json:"on"`
			Room   string `json:"room"`
		} `json:"devices"`
		Sensors []struct {
			Entity string `json:"entity"`
			Label  string `json:"label"`
			State  string `json:"state"`
			Unit   string `json:"unit"`
			Room   string `json:"room"`
		} `json:"sensors"`
		Rooms []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal([]byte(res), &view); err != nil {
		t.Fatal(err)
	}
	if !view.OK || len(view.Devices) != 1 || view.Devices[0].Entity != "light.bedroom" || !view.Devices[0].On || view.Devices[0].Room != "r_living" {
		t.Fatalf("设备行不符: %+v", view)
	}
	if len(view.Rooms) != 1 || view.Rooms[0].ID != "r_living" || view.Rooms[0].Name != "客厅" {
		t.Fatalf("房间清单不符: %+v", view.Rooms)
	}
	if len(view.Sensors) != 2 {
		t.Fatalf("应两个状态实体: %+v", view.Sensors)
	}
	temp, bat := view.Sensors[0], view.Sensors[1]
	if temp.Entity != "sensor.temperature" || temp.State != "23.5" || temp.Unit != "°C" || temp.Label != "客厅温度" || temp.Room != "r_living" {
		t.Fatalf("温度实体行不符: %+v", temp)
	}
	// 无 label 的实体回落上游友好名（battery 无 friendly_name → 回落 entity_id），room 空
	if bat.Entity != "sensor.battery" || bat.State != "85" || bat.Unit != "%" || bat.Label == "" || bat.Room != "" {
		t.Fatalf("电量实体行不符: %+v", bat)
	}

	// 校验：重复 / 坏实体 / 超上限 / 房间
	do(t, http.MethodPut, "/api/v1/ha/devices-config",
		`{"source":0,"sensors":[{"entity":"sensor.a"},{"entity":"sensor.a"}]}`,
		http.StatusBadRequest)
	do(t, http.MethodPut, "/api/v1/ha/devices-config",
		`{"source":0,"sensors":[{"entity":"no-dot"}]}`,
		http.StatusBadRequest)
	do(t, http.MethodPut, "/api/v1/ha/devices-config",
		`{"source":0,"rooms":[{"id":"r_a","name":"A"},{"id":"r_a","name":"B"}]}`,
		http.StatusBadRequest)
	do(t, http.MethodPut, "/api/v1/ha/devices-config",
		`{"source":0,"rooms":[{"id":"r_a","name":"A"},{"id":"r_b","name":"A"}]}`,
		http.StatusBadRequest)
	do(t, http.MethodPut, "/api/v1/ha/devices-config",
		`{"source":0,"devices":[{"entity":"light.bedroom","room":"r_none"}]}`,
		http.StatusBadRequest)
	do(t, http.MethodPut, "/api/v1/ha/devices-config",
		`{"source":0,"sensors":[{"entity":"sensor.a","room":"r_none"}]}`,
		http.StatusBadRequest)
	many := make([]string, 0, 33)
	for i := 0; i < 33; i++ {
		many = append(many, fmt.Sprintf(`{"entity":"sensor.s%d"}`, i))
	}
	do(t, http.MethodPut, "/api/v1/ha/devices-config",
		`{"source":0,"sensors":[`+strings.Join(many, ",")+`]}`,
		http.StatusBadRequest)

	// sensors 传空数组 = 清空状态实体，devices 不动
	do(t, http.MethodPut, "/api/v1/ha/devices-config",
		`{"source":0,"devices":[{"entity":"light.bedroom"}],"sensors":[]}`, http.StatusOK)
	res = do(t, http.MethodGet, "/api/v1/ha/devices?source=0", "", http.StatusOK)
	if !strings.Contains(res, `"sensors":[]`) || !strings.Contains(res, "light.bedroom") {
		t.Fatalf("清空 sensors 后 devices 应保留: %s", res)
	}
}

// TestHAEnvAndSetCall 环境电器（388）全链路：Devices 行带 env 属性 → call
// action=set 调目标温度读回新值 → 非法域/缺值 400。
func TestHAEnvAndSetCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "ha.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	tgt := 26.0
	mu := &sync.Mutex{}
	statesJSON := func() string {
		b, _ := json.Marshal([]map[string]any{
			{"entity_id": "climate.ac", "state": "cool", "attributes": map[string]any{
				"friendly_name": "客厅空调", "current_temperature": 27.5, "temperature": tgt,
				"current_humidity": 52, "min_temp": 16, "max_temp": 30,
			}},
			{"entity_id": "light.bedroom", "state": "on", "attributes": map[string]any{"friendly_name": "卧室灯"}},
		})
		return string(b)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/states", "/api/states/climate.ac", "/api/states/light.bedroom":
			if r.URL.Path == "/api/states/climate.ac" {
				var one []map[string]any
				_ = json.Unmarshal([]byte(statesJSON()), &one)
				for _, e := range one {
					if e["entity_id"] == "climate.ac" {
						b, _ := json.Marshal(e)
						_, _ = w.Write(b)
						return
					}
				}
			}
			_, _ = w.Write([]byte(statesJSON()))
		case "/api/services/climate/set_temperature":
			mu.Lock()
			tgt += 0.5
			mu.Unlock()
			_, _ = w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	if err := s.SaveHAMasterConnection(upstream.URL, "t", false); err != nil {
		t.Fatal(err)
	}
	inID := int64(0) // 475：来源恒总配置（id=0），body 仍带 source 兼容旧端
	h := &HAHandler{st: s, mgr: integrations.New(s)}
	r := gin.New()
	grp := r.Group("/api/v1")
	grp.GET("/ha/devices", h.Devices)
	grp.POST("/ha/call", h.Call)
	grp.PUT("/ha/devices-config", h.SaveDevices)

	do := func(method, path, body string, want int) string {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s 状态 %d（期望 %d）: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.String()
	}
	do(http.MethodPut, "/api/v1/ha/devices-config",
		fmt.Sprintf(`{"source":%d,"devices":[{"entity":"climate.ac","label":"空调"},{"entity":"light.bedroom"}]}`, inID),
		http.StatusOK)

	// Devices：climate 行带 env（当前/目标温度、湿度、范围），light 行无 env
	res := do(http.MethodGet, "/api/v1/ha/devices?source=0", "", http.StatusOK)
	var view struct {
		Devices []struct {
			Entity string `json:"entity"`
			Env    *struct {
				CurTemp *float64 `json:"cur_temp"`
				TgtTemp *float64 `json:"tgt_temp"`
				CurHum  *float64 `json:"cur_hum"`
				MaxTemp *float64 `json:"max_temp"`
			} `json:"env"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(res), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Devices) != 2 {
		t.Fatalf("应两台设备: %s", res)
	}
	ac, li := view.Devices[0], view.Devices[1]
	if ac.Entity != "climate.ac" || ac.Env == nil || ac.Env.CurTemp == nil || *ac.Env.CurTemp != 27.5 ||
		ac.Env.TgtTemp == nil || *ac.Env.TgtTemp != 26 || ac.Env.CurHum == nil || ac.Env.MaxTemp == nil {
		t.Fatalf("climate 行 env 不符: %s", res)
	}
	if li.Entity != "light.bedroom" && li.Entity != "climate.ac" {
		t.Fatalf("设备行不符: %s", res)
	}
	if li.Entity == "light.bedroom" && li.Env != nil {
		t.Fatalf("light 行不应带 env: %s", res)
	}

	// call set：调高 0.5 度 → 读回新目标
	res = do(http.MethodPost, "/api/v1/ha/call",
		fmt.Sprintf(`{"source":%d,"entity_id":"climate.ac","action":"set","value":26.5}`, inID), http.StatusOK)
	if !strings.Contains(res, `"tgt_temp":26.5`) {
		t.Fatalf("set 读回应带新目标: %s", res)
	}

	// 非法：缺 value / 不支持的域 / 非收藏实体
	do(http.MethodPost, "/api/v1/ha/call",
		fmt.Sprintf(`{"source":%d,"entity_id":"climate.ac","action":"set"}`, inID), http.StatusBadRequest)
	do(http.MethodPost, "/api/v1/ha/call",
		fmt.Sprintf(`{"source":%d,"entity_id":"light.bedroom","action":"set","value":20}`, inID), http.StatusBadRequest)
	do(http.MethodPost, "/api/v1/ha/call",
		fmt.Sprintf(`{"source":%d,"entity_id":"climate.hall","action":"set","value":20}`, inID), http.StatusBadRequest)
}

// TestHAMasterAPI 连接配置端点（475）：token 不回显、留空沿用、非法地址 400。
func TestHAMasterAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "hm.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h := &HAHandler{st: s, mgr: integrations.New(s)}
	r := gin.New()
	grp := r.Group("/api/v1")
	grp.GET("/ha/master", h.GetMaster)
	grp.PUT("/ha/master", h.SaveMaster)
	do := func(method, path, body string, want int) string {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s 状态 %d（期望 %d）: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.String()
	}

	// 初始未配置
	if res := do(http.MethodGet, "/api/v1/ha/master", "", http.StatusOK); !strings.Contains(res, `"has_token":false`) {
		t.Fatalf("初始应无 token: %s", res)
	}
	// 非法地址拒绝
	do(http.MethodPut, "/api/v1/ha/master", `{"url":"javascript:alert(1)"}`, http.StatusBadRequest)
	// 正常保存：token 不回显
	do(http.MethodPut, "/api/v1/ha/master", `{"url":"http://192.168.1.2:8123","token":"sekret","insecure":true}`, http.StatusOK)
	res := do(http.MethodGet, "/api/v1/ha/master", "", http.StatusOK)
	if !strings.Contains(res, `"has_token":true`) || strings.Contains(res, "sekret") {
		t.Fatalf("token 不得回显: %s", res)
	}
	// 留空沿用 + insecure 缺省不动
	do(http.MethodPut, "/api/v1/ha/master", `{"url":"http://10.0.0.8:8123"}`, http.StatusOK)
	res = do(http.MethodGet, "/api/v1/ha/master", "", http.StatusOK)
	if !strings.Contains(res, `"has_token":true`) || !strings.Contains(res, `"insecure":true`) || !strings.Contains(res, "10.0.0.8") {
		t.Fatalf("token 应沿用、insecure 应保留: %s", res)
	}
	// 保存后 HA 页来源可达（合成集成走 master）
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ha/sources", nil)
	r2 := gin.New()
	g2 := r2.Group("/api/v1")
	g2.GET("/ha/sources", h.Sources)
	r2.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":0`) {
		t.Fatalf("sources 应返回单一来源 id=0: %d %s", w.Code, w.Body.String())
	}
}
