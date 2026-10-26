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
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/integrations"
	"lz-monitor/internal/server/store"
)

// newUsersHARouter 真实链路（361/355）：真实 Register + HA mock 上游 + 两个账号
// （admin / 受限用户 fam）。返回引擎、store、两个会话 token、HA 集成 ID、上游地址。
func newUsersHARouter(t *testing.T) (*gin.Engine, *store.Store, string, string, int64, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "mu.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("fam", "pass1234", "user"); err != nil {
		t.Fatal(err)
	}
	admin, _ := s.UserByUsername("admin")
	fam, _ := s.UserByUsername("fam")
	adminTok, err := s.CreateSession(admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	famTok, err := s.CreateSession(fam.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// HA mock 上游：全量 states（含可控域与只读域）+ light 服务调用翻状态
	mu := sync.Mutex{}
	lightState := "off"
	mux := http.NewServeMux()
	ent := func(id, state, name string) string {
		b, _ := json.Marshal(map[string]any{"entity_id": id, "state": state,
			"attributes": map[string]string{"friendly_name": name}})
		return string(b)
	}
	mux.HandleFunc("/api/states/light.ceiling", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write([]byte(ent("light.ceiling", lightState, "客厅灯")))
	})
	mux.HandleFunc("/api/services/light/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if strings.HasSuffix(r.URL.Path, "/turn_on") {
			lightState = "on"
		} else {
			lightState = "off"
		}
		mu.Unlock()
		_, _ = w.Write([]byte("[]"))
	})
	mux.HandleFunc("/api/states", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		s := ent("light.ceiling", lightState, "客厅灯")
		mu.Unlock()
		_, _ = w.Write([]byte("[" + s + "," +
			ent("lock.door", "locked", "大门") + "," +
			ent("sensor.temp", "22.5", "温度") + "]"))
	})
	up := httptest.NewServer(mux)
	t.Cleanup(up.Close)

	// 475：设备/连接进智能家居总配置；服务集成行不再承载 HA 页
	if err := s.SaveHAMasterConnection(up.URL, "t", false); err != nil {
		t.Fatal(err)
	}
	devs, _ := json.Marshal([]integrations.HADevice{{Entity: "light.ceiling", Label: "客厅灯"}})
	if err := s.SaveHAMasterLists(nil, devs, nil); err != nil {
		t.Fatal(err)
	}
	haID := int64(0)

	r := gin.New()
	if err := Register(r, s, nil, &config.Config{}, "test", nil, integrations.New(s), nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err, nil)
	}
	return r, s, adminTok, famTok, haID, up.URL
}

// TestMultiUserHAChain 一条链打通 361 权限收敛 + 355 设备控制 + 配置保留。
func TestMultiUserHAChain(t *testing.T) {
	r, s, adminTok, famTok, haID, upURL := newUsersHARouter(t)
	haPath := fmt.Sprintf("?source=%d", haID)

	// ---- 受限用户：管理面 API 一律 403 ----
	for _, c := range [][3]string{
		{http.MethodGet, "/api/v1/servers", ""},
		{http.MethodGet, "/api/v1/auth/users", ""},
		{http.MethodGet, "/api/v1/ha/catalog" + haPath, ""},
		{http.MethodPut, "/api/v1/ha/devices-config", `{"source":1,"devices":[]}`},
		{http.MethodGet, "/api/v1/term/files", ""},
		{http.MethodPost, "/api/v1/settings/cleanup", `{}`},
	} {
		w := doJSON(r, c[0], c[1], famTok, c[2])
		if w.Code != http.StatusForbidden {
			t.Fatalf("受限用户 %s %s 应 403: %d %s", c[0], c[1], w.Code, w.Body.String())
		}
	}

	// ---- 受限用户：HA 页面端点开放 ----
	w := doJSON(r, http.MethodGet, "/api/v1/ha/sources", famTok, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "智能家居") {
		t.Fatalf("sources 应 200: %d %s", w.Code, w.Body.String())
	}
	// 来源视图不得泄露上游地址与令牌
	if strings.Contains(w.Body.String(), "http://") || strings.Contains(w.Body.String(), "token") {
		t.Fatalf("sources 泄露 url/token: %s", w.Body.String())
	}

	w = doJSON(r, http.MethodGet, "/api/v1/ha/devices"+haPath, famTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("devices 应 200: %d %s", w.Code, w.Body.String())
	}
	var devRes struct {
		OK      bool `json:"ok"`
		Devices []struct {
			Entity string `json:"entity"`
			State  string `json:"state"`
			On     bool   `json:"on"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &devRes); err != nil {
		t.Fatal(err)
	}
	if !devRes.OK || len(devRes.Devices) != 1 || devRes.Devices[0].Entity != "light.ceiling" ||
		devRes.Devices[0].State != "off" || devRes.Devices[0].On {
		t.Fatalf("devices 内容不对: %s", w.Body.String())
	}

	// 开灯：turn_on → 读回 on=true
	w = doJSON(r, http.MethodPost, "/api/v1/ha/call", famTok,
		fmt.Sprintf(`{"source":%d,"entity_id":"light.ceiling","action":"turn_on"}`, haID))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"on":true`) {
		t.Fatalf("call 应 200 且读回 on: %d %s", w.Code, w.Body.String())
	}
	// 不在收藏清单的实体不可控（即使属于可控域）
	w = doJSON(r, http.MethodPost, "/api/v1/ha/call", famTok,
		fmt.Sprintf(`{"source":%d,"entity_id":"lock.door","action":"turn_on"}`, haID))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("清单外实体应 400: %d %s", w.Code, w.Body.String())
	}

	// 登录响应带 role
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"fam","password":"pass1234"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"role":"user"`) {
		t.Fatalf("登录应 200 且 role=user: %d %s", w.Code, w.Body.String())
	}

	// ---- 用户管理 API（管理员） ----
	w = doJSON(r, http.MethodGet, "/api/v1/auth/users", adminTok, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "fam") {
		t.Fatalf("用户列表应 200: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "$2a$") {
		t.Fatal("用户列表泄露密码哈希")
	}

	// 建用户 / 重名
	w = doJSON(r, http.MethodPost, "/api/v1/auth/users", adminTok,
		`{"username":"viewer","password":"pass1234","role":"user"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("建用户应 200: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(r, http.MethodPost, "/api/v1/auth/users", adminTok,
		`{"username":"viewer","password":"pass1234","role":"user"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("重名应 400: %d", w.Code)
	}

	admin, _ := s.UserByUsername("admin")
	fam, _ := s.UserByUsername("fam")
	viewer, _ := s.UserByUsername("viewer")

	// 最后一个管理员不可降级；不能删自己
	w = doJSON(r, http.MethodPut, "/api/v1/auth/users/"+idPath(admin.ID), adminTok, `{"role":"user"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("降级最后一个管理员应 400: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(r, http.MethodDelete, "/api/v1/auth/users/"+idPath(admin.ID), adminTok, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("删除自己应 400: %d %s", w.Code, w.Body.String())
	}

	// 提升第二个管理员后，原管理员可降级——旧会话即时变为受限（角色每请求现读）
	w = doJSON(r, http.MethodPut, "/api/v1/auth/users/"+idPath(viewer.ID), adminTok, `{"role":"admin"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("提升 viewer 应 200: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(r, http.MethodPut, "/api/v1/auth/users/"+idPath(admin.ID), adminTok, `{"role":"user"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("降级原管理员应 200: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(r, http.MethodGet, "/api/v1/servers", adminTok, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("降级后旧会话访问管理面应立即 403: %d", w.Code)
	}
	w = doJSON(r, http.MethodGet, "/api/v1/ha/sources", adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("降级后 HA 端点应仍可用: %d", w.Code)
	}

	// 新管理员接管：重置 fam 密码 → fam 既有会话全部被踢
	viewerTok, err := s.CreateSession(viewer.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w = doJSON(r, http.MethodPut, "/api/v1/auth/users/"+idPath(fam.ID), viewerTok,
		`{"role":"user","password":"newpass99"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("重置密码应 200: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(r, http.MethodGet, "/api/v1/ha/devices"+haPath, famTok, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("被重置密码的旧会话应 401: %d", w.Code)
	}

	// 删用户连带清会话
	w = doJSON(r, http.MethodDelete, "/api/v1/auth/users/"+idPath(fam.ID), viewerTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("删用户应 200: %d %s", w.Code, w.Body.String())
	}
	if u, _ := s.SessionUser(famTok); u != nil {
		t.Fatal("删用户后会话应失效")
	}

	// ---- 475 拆分：服务集成行（前台卡配置）与智能家居总配置互不影响 ----
	svcID, err := s.CreateIntegration(store.IntegrationHA, "前台卡", upURL, `{"token":"t"}`, `{"entities":[{"entity":"sensor.temp","label":"温度"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	w = doJSON(r, http.MethodPut, "/api/v1/integrations/"+idPath(svcID), viewerTok,
		`{"type":"homeassistant","name":"前台卡","url":"`+upURL+`","enabled":true,`+
			`"config":{"entities":[{"entity":"sensor.temp","label":"温度"}],"open_url":"https://ha.example.com"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("编辑集成应 200: %d %s", w.Code, w.Body.String())
	}
	in, _ := s.GetIntegration(svcID)
	if !strings.Contains(in.Config, "sensor.temp") || !strings.Contains(in.Config, "open_url") ||
		strings.Contains(in.Config, `"devices"`) || strings.Contains(in.Config, `"rooms"`) {
		t.Fatalf("服务集成 config 应只含前台卡配置: %s", in.Config)
	}
	w = doJSON(r, http.MethodPut, "/api/v1/ha/devices-config", viewerTok,
		`{"source":0,"devices":[{"entity":"lock.door","label":"大门"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("保存设备清单应 200: %d %s", w.Code, w.Body.String())
	}
	in, _ = s.GetIntegration(svcID)
	if strings.Contains(in.Config, "lock.door") {
		t.Fatalf("总配置清单不得写进服务集成: %s", in.Config)
	}
	m, _ := s.GetHAMaster()
	if !strings.Contains(string(m.Devices), "lock.door") {
		t.Fatalf("总配置应收到新清单: %s", m.Devices)
	}
	// 不可控域拒绝入库
	w = doJSON(r, http.MethodPut, "/api/v1/ha/devices-config", viewerTok,
		`{"source":0,"devices":[{"entity":"sensor.temp","label":"温度"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("只读域入设备清单应 400: %d %s", w.Code, w.Body.String())
	}
	// 更新后的清单即时反映到 devices 端点（states 缓存已含 lock.door）
	w = doJSON(r, http.MethodGet, "/api/v1/ha/devices"+haPath, viewerTok, "")
	if !strings.Contains(w.Body.String(), "lock.door") || strings.Contains(w.Body.String(), "light.ceiling") {
		t.Fatalf("devices 应反映新清单: %s", w.Body.String())
	}
}
