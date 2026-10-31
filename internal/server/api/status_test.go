package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// TestStatusGet 自监控字段：模式计数、服务器统计、近似行数、运行时长。
func TestStatusGet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.CreateServer("agent-1", "10.0.0.1:19100", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNodeServer("node-1", "10.0.0.2:10200", "", 0); err != nil {
		t.Fatal(err)
	}
	h := &StatusHandler{st: s, dbPath: "/nonexistent/lz.db", version: "vtest",
		startedAt: time.Now().Add(-90 * time.Second), offlineAfter: 120}
	r := gin.New()
	r.GET("/status", h.Get)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Runtime struct {
			Version string `json:"version"`
			Uptime  int64  `json:"uptime"`
			Now     int64  `json:"now"`
		} `json:"runtime"`
		DB struct {
			Retention  int   `json:"retention"`
			RowsApprox int64 `json:"rows_approx"`
		} `json:"db"`
		Overview struct {
			Servers   int            `json:"servers"`
			Online    int            `json:"online"`
			Offline   int            `json:"offline"`
			Disabled  int            `json:"disabled"`
			ModeCount map[string]int `json:"mode_count"`
		} `json:"overview"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Runtime.Version != "vtest" {
		t.Fatalf("version: %s", res.Runtime.Version)
	}
	if res.Runtime.Uptime < 89 || res.Runtime.Uptime > 95 {
		t.Fatalf("uptime 应约 90s: %d", res.Runtime.Uptime)
	}
	if res.DB.Retention != 90 {
		t.Fatalf("默认保留 90 天: %d", res.DB.Retention)
	}
	if res.Overview.Servers != 2 || res.Overview.ModeCount["agent"] != 1 || res.Overview.ModeCount["node"] != 1 {
		t.Fatalf("模式计数错误: servers=%d modes=%v", res.Overview.Servers, res.Overview.ModeCount)
	}
	// 两台从未上报：均为离线（未停用）
	if res.Overview.Online != 0 || res.Overview.Offline != 2 || res.Overview.Disabled != 0 {
		t.Fatalf("在线统计错误: %+v", res.Overview)
	}
	// 库文件不存在时 size 为 0，行数近似仍可查（无指标 ≥0）
	if res.DB.RowsApprox < 0 {
		t.Fatalf("rows_approx: %d", res.DB.RowsApprox)
	}
}
