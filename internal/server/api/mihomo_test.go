package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/tvfollow"
)

// mihomo 控制台 API（TODO 7）：配置校验（push 机器 + 私网地址 + secret 不回显）、
// 读面板/写操作入队与轮询解析、跨用途任务隔离。

func mihomoTestEnv(t *testing.T) (*store.Store, *gin.Engine) {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &MihomoHandler{st: s}
	r.GET("/mihomo/config", h.Config)
	r.PUT("/mihomo/config", h.SaveConfig)
	r.POST("/mihomo/query", h.Query)
	r.POST("/mihomo/op", h.Op)
	r.GET("/mihomo/task/:id", h.Task)
	return s, r
}

func mihJSON(t *testing.T, r *gin.Engine, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, strings.NewReader(string(b)))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// pushServer 建一台 push 模式机器（默认 CreateServer 是 agent 拉取）。
func pushServer(t *testing.T, s *store.Store) *store.Server {
	t.Helper()
	srv, err := s.CreateServer("家庭路由", "192.168.50.10:19100", "tok", 0)
	if err != nil {
		t.Fatal(err)
	}
	mode := "push"
	if err := s.UpdateServer(srv.ID, store.ServerUpdate{CollectMode: &mode}); err != nil {
		t.Fatal(err)
	}
	fresh, _ := s.GetServer(srv.ID)
	return fresh
}

func TestMihomoConfigValidation(t *testing.T) {
	s, r := mihomoTestEnv(t)
	srv := pushServer(t, s)

	// 公网地址拒绝（agent 白名单口径对齐）
	code, out := mihJSON(t, r, http.MethodPut, "/mihomo/config", map[string]any{
		"server_id": srv.ID, "base_url": "http://1.2.3.4:9090"})
	if code != http.StatusBadRequest {
		t.Fatalf("公网地址应拒绝: %d %v", code, out)
	}
	// 域名拒绝
	code, _ = mihJSON(t, r, http.MethodPut, "/mihomo/config", map[string]any{
		"server_id": srv.ID, "base_url": "http://router.lan:9090"})
	if code != http.StatusBadRequest {
		t.Fatalf("域名地址应拒绝")
	}
	// 非 push 机器拒绝
	plain, _ := s.CreateServer("普通机", "10.0.0.9:19100", "", 0)
	code, _ = mihJSON(t, r, http.MethodPut, "/mihomo/config", map[string]any{
		"server_id": plain.ID, "base_url": "http://127.0.0.1:9090"})
	if code != http.StatusBadRequest {
		t.Fatalf("非 push 机器应拒绝")
	}

	// 正确保存：secret 落库但不回显
	code, out = mihJSON(t, r, http.MethodPut, "/mihomo/config", map[string]any{
		"server_id": srv.ID, "base_url": "http://127.0.0.1:9090", "secret": "abc", "test_url": ""})
	if code != http.StatusOK || out["ready"] != true || out["has_secret"] != true || out["server_name"] != "家庭路由" {
		t.Fatalf("保存应成功: %d %v", code, out)
	}
	if _, exists := out["secret"]; exists {
		t.Fatalf("secret 不得回显: %v", out)
	}
	// secret 留空沿用
	code, out = mihJSON(t, r, http.MethodPut, "/mihomo/config", map[string]any{
		"server_id": srv.ID, "base_url": "http://127.0.0.1:9090", "secret": ""})
	if code != http.StatusOK || out["has_secret"] != true {
		t.Fatalf("secret 留空应沿用: %v", out)
	}
	// GET 回显口径
	code, out = mihJSON(t, r, http.MethodGet, "/mihomo/config", nil)
	if code != http.StatusOK || out["ready"] != true || out["has_secret"] != true {
		t.Fatalf("回读: %v", out)
	}
}

func TestMihomoQueryAndTaskFlow(t *testing.T) {
	s, r := mihomoTestEnv(t)
	srv := pushServer(t, s)
	if _, out := mihJSON(t, r, http.MethodPut, "/mihomo/config", map[string]any{
		"server_id": srv.ID, "base_url": "http://127.0.0.1:9090", "secret": "sk"}); out["ready"] != true {
		t.Fatalf("配置失败: %v", out)
	}

	// 未配置前 query 拒绝（另开空库）
	// 入队读面板
	code, out := mihJSON(t, r, http.MethodPost, "/mihomo/query", map[string]any{"op": "proxies"})
	if code != http.StatusOK || out["task_id"] == nil {
		t.Fatalf("入队失败: %d %v", code, out)
	}
	id := int64(out["task_id"].(float64))

	// 进行中态
	code, out = mihJSON(t, r, http.MethodGet, "/mihomo/task/"+mi64(id), nil)
	if code != http.StatusOK || out["running"] != true {
		t.Fatalf("应 running: %v", out)
	}

	// 模拟 agent 领取 + 执行 + 回传
	claimed, err := s.ClaimNetTasks(srv.ID, 4)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("领取: %v %v", claimed, err)
	}
	task := claimed[0]
	if task.URL != "http://127.0.0.1:9090/proxies" || task.Headers != "Authorization: Bearer sk\n" {
		t.Fatalf("任务组装: %+v", task)
	}
	if _, err := s.FinishNetTask(task.ID, 200, `{"proxies":{"🚀 节点选择":{"type":"Selector","now":"DIRECT","all":["DIRECT"]}}}`, ""); err != nil {
		t.Fatal(err)
	}

	code, out = mihJSON(t, r, http.MethodGet, "/mihomo/task/"+mi64(id), nil)
	if code != http.StatusOK || out["state"] != "done" || out["error"] != nil {
		t.Fatalf("应 done: %v", out)
	}
	data := out["data"].(map[string]any)
	if len(data["groups"].([]any)) != 1 {
		t.Fatalf("应解析出 1 个组: %v", data)
	}

	// 同 ref_key 活跃去重：回传后再入队是新任务；入队后立即再入队复用
	code, out = mihJSON(t, r, http.MethodPost, "/mihomo/query", map[string]any{"op": "proxies"})
	if code != http.StatusOK {
		t.Fatal("重新入队应成功（旧任务已终态）")
	}
	first := int64(out["task_id"].(float64))
	_, out = mihJSON(t, r, http.MethodPost, "/mihomo/query", map[string]any{"op": "proxies"})
	if int64(out["task_id"].(float64)) != first {
		t.Fatalf("活跃任务应复用: %v vs %v", out["task_id"], first)
	}

	// 未知 op
	if code, _ = mihJSON(t, r, http.MethodPost, "/mihomo/query", map[string]any{"op": "nope"}); code != http.StatusBadRequest {
		t.Fatal("未知 op 应 400")
	}
}

func TestMihomoOpAndIsolation(t *testing.T) {
	s, r := mihomoTestEnv(t)
	srv := pushServer(t, s)
	mihJSON(t, r, http.MethodPut, "/mihomo/config", map[string]any{
		"server_id": srv.ID, "base_url": "http://192.168.50.10:9090"})

	// 非法 mode
	if code, _ := mihJSON(t, r, http.MethodPost, "/mihomo/op", map[string]any{"action": "mode", "mode": "hack"}); code != http.StatusBadRequest {
		t.Fatal("非法 mode 应 400")
	}
	// 切节点入队
	code, out := mihJSON(t, r, http.MethodPost, "/mihomo/op", map[string]any{"action": "switch", "group": "Auto", "name": "香港 01"})
	if code != http.StatusOK || out["task_id"] == nil {
		t.Fatalf("switch 入队失败: %v", out)
	}
	claimed, _ := s.ClaimNetTasks(srv.ID, 4)
	if len(claimed) != 1 || claimed[0].Method != "PUT" || claimed[0].Body != `{"name":"香港 01"}` ||
		claimed[0].URL != "http://192.168.50.10:9090/proxies/Auto" {
		t.Fatalf("switch 任务组装: %+v", claimed)
	}
	s.FinishNetTask(claimed[0].ID, 204, "", "")

	// 组测延迟回传解析（失败节点对象形态 → 0）
	code, out = mihJSON(t, r, http.MethodPost, "/mihomo/op", map[string]any{"action": "group_delay", "group": "Auto"})
	if code != http.StatusOK {
		t.Fatal("group_delay 入队失败")
	}
	claimed, _ = s.ClaimNetTasks(srv.ID, 4)
	if len(claimed) != 1 || claimed[0].URL != "http://192.168.50.10:9090/group/Auto/delay?timeout=5000&url=http%3A%2F%2Fwww.gstatic.com%2Fgenerate_204" {
		t.Fatalf("group_delay 组装: %+v", claimed)
	}
	s.FinishNetTask(claimed[0].ID, 200, `{"香港 01":88,"美国 01":{"message":"timeout"}}`, "")
	code, out = mihJSON(t, r, http.MethodGet, "/mihomo/task/"+mi64(claimed[0].ID), nil)
	dm := out["data"].(map[string]any)["delays"].(map[string]any)
	if dm["香港 01"] != float64(88) || dm["美国 01"] != float64(0) {
		t.Fatalf("组测延迟解析: %v", dm)
	}

	// 跨用途隔离：追剧用途任务不得经 mihomo 轮询端点读取
	tid, err := s.CreateNetTask(tvfollow.PurposeAPI, "tv:x", 1, "https://api.themoviedb.org/x", "GET", nil, "", 15, false)
	if err != nil {
		t.Fatal(err)
	}
	s.ClaimNetTasks(1, 4)
	s.FinishNetTask(tid, 200, `{"secret":"payload"}`, "")
	if code, _ = mihJSON(t, r, http.MethodGet, "/mihomo/task/"+mi64(tid), nil); code != http.StatusNotFound {
		t.Fatalf("跨用途任务应 404: %d", code)
	}
}

func TestMihomoUnreadyQueryRejected(t *testing.T) {
	_, r := mihomoTestEnv(t)
	code, out := mihJSON(t, r, http.MethodPost, "/mihomo/query", map[string]any{"op": "overview"})
	if code != http.StatusBadRequest {
		t.Fatalf("未配置应 400: %d %v", code, out)
	}
}

func mi64(v int64) string {
	return strconv.FormatInt(v, 10)
}
