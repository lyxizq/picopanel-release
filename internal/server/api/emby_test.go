package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// TestEmbyHistoryAPI GET /emby/history（528/530）：缺省回落第一个 emby 集成、
// 新→旧、total/users/types 回显、筛选与 offset 翻页、空态。
func TestEmbyHistoryAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "emby.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	h := &IntegrationHandler{st: s}
	r := gin.New()
	r.GET("/api/v1/emby/history", h.EmbyHistory)
	r.GET("/api/v1/emby/stats", h.EmbyStats)
	get := func(path string) (int, string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code, w.Body.String()
	}

	// 无任何 emby 集成：空清单不报错
	code, body := get("/api/v1/emby/history")
	if code != http.StatusOK || !json.Valid([]byte(body)) || body == `{"events":[]}` {
		t.Fatalf("无集成应空清单（含 total/users/types）: %d %s", code, body)
	}

	// 建 emby 集成与两行记录（一行进行中、一行已结束）
	if _, err := s.CreateIntegration(store.IntegrationEmby, "家庭库", "http://127.0.0.1:8096", `{"token":"k"}`, `{}`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	id1, _ := s.InsertEmbyStart(store.EmbyEvent{IntegrationID: 1, User: "alice", Title: "第三集", Sub: "好剧", Type: "Episode", StartedAt: now - 3600, RuntimeTicks: 3000})
	id2, _ := s.InsertEmbyStart(store.EmbyEvent{IntegrationID: 1, User: "bob", Title: "电影X", Type: "Movie", StartedAt: now - 60, RuntimeTicks: 7200})
	if err := s.FinishEmbyPlay(id1, now-3000, 1500); err != nil {
		t.Fatal(err)
	}

	var res struct {
		Events []store.EmbyEvent `json:"events"`
		Total  int              `json:"total"`
		Users  []string         `json:"users"`
		Types  []string         `json:"types"`
	}
	code, body = get("/api/v1/emby/history")
	if code != http.StatusOK {
		t.Fatalf("状态 %d: %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 || len(res.Events) != 2 || res.Events[0].ID != id2 || res.Events[1].ID != id1 {
		t.Fatalf("应新→旧两行 total=2: %+v", res)
	}
	if len(res.Users) != 2 || res.Users[0] != "alice" || len(res.Types) != 2 || res.Types[0] != "Episode" {
		t.Fatalf("筛选选项应回显: %+v", res)
	}

	// 筛选 + 翻页：user=alice 只剩 1；offset 跳过最新行
	if code, body = get("/api/v1/emby/history?user=alice"); code != http.StatusOK {
		t.Fatalf("筛选应 200: %d", code)
	}
	_ = json.Unmarshal([]byte(body), &res)
	if res.Total != 1 || len(res.Events) != 1 || res.Events[0].User != "alice" {
		t.Fatalf("user 筛选不对: %+v", res)
	}
	if code, body = get("/api/v1/emby/history?limit=1&offset=1"); code != http.StatusOK {
		t.Fatalf("翻页应 200: %d", code)
	}
	_ = json.Unmarshal([]byte(body), &res)
	if res.Total != 2 || len(res.Events) != 1 || res.Events[0].ID != id1 {
		t.Fatalf("offset=1 应取旧行: %+v", res)
	}

	// 日期范围（3）：from/to 按 started_at 左闭右开过滤（from 取 now-3000
	// 避开 id1 恰在 now-3600 的左闭命中，只留 id2）
	if code, body = get(fmt.Sprintf("/api/v1/emby/history?from=%d&to=%d", now-3000, now+1)); code != http.StatusOK {
		t.Fatalf("日期筛选应 200: %d", code)
	}
	_ = json.Unmarshal([]byte(body), &res)
	if res.Total != 1 || len(res.Events) != 1 || res.Events[0].ID != id2 {
		t.Fatalf("from/to 应只留近 1 小时: %+v", res)
	}
}

// TestEmbyStatsAPI GET /emby/stats（530）：days=0 全量聚合、days 负值回落 30。
func TestEmbyStatsAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "embys.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	h := &IntegrationHandler{st: s}
	r := gin.New()
	r.GET("/api/v1/emby/stats", h.EmbyStats)
	get := func(path string) (int, string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code, w.Body.String()
	}

	// 无集成：空聚合不报错
	if code, body := get("/api/v1/emby/stats"); code != http.StatusOK || !json.Valid([]byte(body)) {
		t.Fatalf("无集成应空聚合: %d %s", code, body)
	}

	if _, err := s.CreateIntegration(store.IntegrationEmby, "家庭库", "http://127.0.0.1:8096", `{"token":"k"}`, `{}`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := s.InsertEmbyStart(store.EmbyEvent{IntegrationID: 1, User: "alice", Type: "Movie", StartedAt: now - 7200}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.DB().QueryRow(`SELECT id FROM emby_events LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishEmbyPlay(id, now-3600, 0); err != nil {
		t.Fatal(err)
	}

	var res struct {
		Users []store.EmbyUserStat `json:"users"`
		Types []store.EmbyTypeStat `json:"types"`
		Days  []store.EmbyDayStat  `json:"days"`
	}
	code, body := get("/api/v1/emby/stats?days=0")
	if code != http.StatusOK {
		t.Fatalf("状态 %d: %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Users) != 1 || res.Users[0].User != "alice" || res.Users[0].Plays != 1 || res.Users[0].Seconds != 3600 {
		t.Fatalf("用户聚合不对: %+v", res.Users)
	}
	if len(res.Types) != 1 || res.Types[0].Type != "Movie" || len(res.Days) != 1 || res.Days[0].Plays != 1 {
		t.Fatalf("类型/日期聚合不对: %+v %+v", res.Types, res.Days)
	}
	// days 负值回落 30：数据都在 30 天内，结果一致
	if code, body = get("/api/v1/emby/stats?days=-5"); code != http.StatusOK {
		t.Fatalf("负 days 应 200: %d", code)
	}
	_ = json.Unmarshal([]byte(body), &res)
	if len(res.Users) != 1 {
		t.Fatalf("负 days 应回落 30 仍有数据: %+v", res.Users)
	}
}
