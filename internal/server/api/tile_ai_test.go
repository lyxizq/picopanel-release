package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// newTileAIRouter 数据监控用量格子（486）API 测试：预置一台服务器与一个
// 智谱账户，挂 dashboard-metrics 与 page/metrics 的创建/列表路由。
func newTileAIRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.CreateServer("web-01", "10.0.0.1:19100", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAiAccount(&store.AiAccount{Name: "智谱主账号", Provider: "zhipu", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	tileH := &DashMetricHandler{st: s}
	r.POST("/dashboard-metrics", tileH.Create)
	r.GET("/dashboard-metrics", tileH.List)
	pageH := &PageHandler{st: s}
	r.POST("/page/metrics", pageH.CreateMetric)
	r.GET("/page/metrics", pageH.ListMetrics)
	return r, s
}

func postTile(t *testing.T, r *gin.Engine, url, body string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func listTiles(t *testing.T, r *gin.Engine, url string) []map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("列表状态码 %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Metrics []map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res.Metrics
}

// TestTileAISourceValidation 用量指标校验：账户必选、存在、服务商匹配；
// 服务器指标与用量指标互斥归一（双方都传时按指标类型取一方归零另一方）。
func TestTileAISourceValidation(t *testing.T) {
	r, s := newTileAIRouter(t)
	acc, err := s.ListAiAccounts()
	if err != nil || len(acc) != 1 {
		t.Fatalf("预置账户缺失: %v", err)
	}
	accID := acc[0].ID
	srv, _ := s.ListServers()
	srvID := srv[0].ID

	// 服务器格子照旧（回归）
	if code, body := postTile(t, r, "/dashboard-metrics", `{"server_id":`+i64s(srvID)+`,"metric":"cpu","label":"CPU"}`); code != 200 {
		t.Fatalf("服务器格子应创建成功 %d: %s", code, body)
	}
	// 用量格子：合法创建
	if code, body := postTile(t, r, "/dashboard-metrics", `{"ai_id":`+i64s(accID)+`,"metric":"ai_zhipu_week","label":"智谱周额度"}`); code != 200 {
		t.Fatalf("用量格子应创建成功 %d: %s", code, body)
	}
	// 用量指标缺账户
	if code, body := postTile(t, r, "/dashboard-metrics", `{"metric":"ai_zhipu_5h","label":"x"}`); code != 400 || !strings.Contains(body, "请选择用量账户") {
		t.Fatalf("缺账户应 400 请选择用量账户 %d: %s", code, body)
	}
	// 账户不存在
	if code, body := postTile(t, r, "/dashboard-metrics", `{"ai_id":999,"metric":"ai_zhipu_5h","label":"x"}`); code != 400 || !strings.Contains(body, "用量账户不存在") {
		t.Fatalf("账户不存在应 400: %d %s", code, body)
	}
	// 服务商不匹配（智谱账户配 EdgeOne 指标）
	if code, body := postTile(t, r, "/dashboard-metrics", `{"ai_id":`+i64s(accID)+`,"metric":"ai_eo_traffic","label":"x"}`); code != 400 || !strings.Contains(body, "不匹配") {
		t.Fatalf("服务商不匹配应 400: %d %s", code, body)
	}
	// 服务器指标缺服务器
	if code, body := postTile(t, r, "/dashboard-metrics", `{"metric":"cpu","label":"x"}`); code != 400 || !strings.Contains(body, "请选择服务器") {
		t.Fatalf("缺服务器应 400: %d %s", code, body)
	}

	// 列表：用量格子回 ai_id/ai_name 且 server_id 归 0；服务器格子不受影响
	metrics := listTiles(t, r, "/dashboard-metrics")
	if len(metrics) != 2 {
		t.Fatalf("应有 2 个格子: %s", jbody(metrics))
	}
	var aiTile, srvTile map[string]any
	for _, m := range metrics {
		if m["metric"] == "ai_zhipu_week" {
			aiTile = m
		} else {
			srvTile = m
		}
	}
	if aiTile == nil || aiTile["ai_id"].(float64) != float64(accID) || aiTile["ai_name"] != "智谱主账号" ||
		aiTile["server_id"].(float64) != 0 || aiTile["server_name"] != "" {
		t.Fatalf("用量格子视图字段不对: %v", aiTile)
	}
	if srvTile["server_id"].(float64) != float64(srvID) || srvTile["server_name"] != "web-01" || srvTile["ai_id"].(float64) != 0 {
		t.Fatalf("服务器格子视图字段不对: %v", srvTile)
	}

	// 主页模块同口径：创建用量格子 + 列表回 ai_name
	if code, body := postTile(t, r, "/page/metrics", `{"ai_id":`+i64s(accID)+`,"metric":"ai_zhipu_week","label":"智谱周额度"}`); code != 200 {
		t.Fatalf("主页用量格子应创建成功 %d: %s", code, body)
	}
	if code, body := postTile(t, r, "/page/metrics", `{"ai_id":`+i64s(accID)+`,"metric":"ai_eo_traffic","label":"x"}`); code != 400 || !strings.Contains(body, "不匹配") {
		t.Fatalf("主页服务商不匹配应 400: %d %s", code, body)
	}
	pms := listTiles(t, r, "/page/metrics")
	if len(pms) != 1 || pms[0]["ai_name"] != "智谱主账号" {
		t.Fatalf("主页列表应回 ai_name: %s", jbody(pms))
	}

	// 删账户：引用它的格子级联清理（两表）
	if err := s.DeleteAiAccount(accID); err != nil {
		t.Fatal(err)
	}
	if ms := listTiles(t, r, "/dashboard-metrics"); len(ms) != 1 {
		t.Fatalf("删账户后总览应剩服务器格子: %s", jbody(ms))
	}
	if pms := listTiles(t, r, "/page/metrics"); len(pms) != 0 {
		t.Fatalf("删账户后主页格子应级联清理: %s", jbody(pms))
	}
}

func i64s(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func jbody(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
