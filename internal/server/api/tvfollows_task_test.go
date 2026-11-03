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

// TaskStatus 搜索任务轮询（573 返工口径钉死）：done 任务立即返回基础候选
// （增强在后台 goroutine，绝不阻塞轮询请求——首版同步 AttachCast 挂死 25s 撞
// ESA 524 致「新增剧搜不到」）；增强完成后回写包装 JSON（enhanced=true），
// 再轮询返回增强候选。

func taskStatusTestEnv(t *testing.T) (*store.Store, *TVFollowHandler, *gin.Engine) {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h := &TVFollowHandler{st: s}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/tvfollows-task/:id", h.TaskStatus)
	return s, h, r
}

// TestTVFollowShortDrama 短剧手动标记（58）：开关落库回读、List 带出 short_drama
// 供卡片/详情标签渲染，缺 on 参数 400、不存在条目 404。
func TestTVFollowShortDrama(t *testing.T) {
	s, h, _ := taskStatusTestEnv(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/tvfollows/:id/short-drama", h.ShortDrama)
	r.GET("/tvfollows", h.List)
	f := &store.TVFollow{Type: store.TVFollowTV, TmdbID: 100, Name: "测试剧"}
	if err := s.CreateTVFollow(f); err != nil {
		t.Fatal(err)
	}

	call := func(id string, body string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/tvfollows/"+id+"/short-drama", strings.NewReader(body))
		r.ServeHTTP(w, req)
		return w.Code
	}

	if code := call(strconv.FormatInt(f.ID, 10), `{"on":true}`); code != http.StatusOK {
		t.Fatalf("标记应成功: %d", code)
	}
	got, err := s.GetTVFollow(f.ID)
	if err != nil || !got.ShortDrama {
		t.Fatalf("应已标记短剧: %v %+v", err, got)
	}
	if code := call(strconv.FormatInt(f.ID, 10), `{"on":false}`); code != http.StatusOK {
		t.Fatalf("取消应成功: %d", code)
	}
	if got, _ = s.GetTVFollow(f.ID); got.ShortDrama {
		t.Fatal("应已取消标记")
	}
	if code := call(strconv.FormatInt(f.ID, 10), `{}`); code != http.StatusBadRequest {
		t.Fatalf("缺 on 应 400: %d", code)
	}
	if code := call("9999", `{"on":true}`); code != http.StatusNotFound {
		t.Fatalf("不存在条目应 404: %d", code)
	}

	// 再标回 + List 视图带出（scanTVFollow 新列口径）
	if code := call(strconv.FormatInt(f.ID, 10), `{"on":true}`); code != http.StatusOK {
		t.Fatalf("重标应成功: %d", code)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tvfollows", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"short_drama":true`) {
		t.Fatalf("List 应带 short_drama: %d %s", w.Code, w.Body.String())
	}
}

func getTaskStatus(r *gin.Engine, taskID int64) (int, map[string]any) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tvfollows-task/"+strconv.FormatInt(taskID, 10), nil)
	r.ServeHTTP(w, req)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestTaskStatusSearchDoneReturnsCandidates(t *testing.T) {
	st, _, r := taskStatusTestEnv(t)
	const tmdbBody = `{"page":1,"results":[{"id":100,"name":"兰香如故","original_name":"Lan Xiang Ru Gu","first_air_date":"2026-10-16","poster_path":"/x.jpg","overview":"简介","vote_average":7.5}]}`
	id, err := st.CreateNetTask(tvfollow.PurposeAPI, "tv:兰香如故", 1, "https://api.themoviedb.org/3/search/tv", "GET", nil, "", 20, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNetTasks(1, 10); err != nil {
		t.Fatal(err)
	}
	if first, err := st.FinishNetTask(id, 200, tmdbBody, ""); err != nil || !first {
		t.Fatalf("回传应成功（err=%v first=%v）", err, first)
	}

	// 第一轮：done 立即返回基础候选（enhanced=false），不阻塞不 524
	code, out := getTaskStatus(r, id)
	if code != http.StatusOK || out["state"] != "done" {
		t.Fatalf("状态不符： code=%d out=%v", code, out)
	}
	if out["enhanced"] != false {
		t.Fatalf("首轮应为基础候选： %v", out["enhanced"])
	}
	cands, _ := out["candidates"].([]any)
	if len(cands) != 1 {
		t.Fatalf("应返回 1 条候选： %v", out["candidates"])
	}

	// 模拟增强回写后：再轮询返回增强候选（enhanced=true）
	enhanced, _ := json.Marshal(map[string]any{"enhanced": true, "candidates": []map[string]any{
		{"id": 100, "name": "兰香如故", "cast": "甲/乙", "emby_checked": true, "in_emby": true},
	}})
	st.UpdateNetTaskResult(id, string(enhanced))
	code, out = getTaskStatus(r, id)
	if code != http.StatusOK || out["enhanced"] != true {
		t.Fatalf("增强后应返回 enhanced=true： code=%d %v", code, out)
	}
	if cands, _ := out["candidates"].([]any); len(cands) != 1 {
		t.Fatalf("增强候选应返回： %v", out["candidates"])
	}
}

func TestTaskStatusRunningAndErrors(t *testing.T) {
	st, _, r := taskStatusTestEnv(t)

	// pending：running=true
	id, _ := st.CreateNetTask(tvfollow.PurposeAPI, "tv:x", 1, "https://api.themoviedb.org/3/x", "GET", nil, "", 15, false)
	code, out := getTaskStatus(r, id)
	if out["running"] != true {
		t.Fatalf("pending 应 running： code=%d %v", code, out)
	}

	// 失败：error 文案
	st.ClaimNetTasks(1, 10)
	st.FinishNetTask(id, 0, "", "超时")
	_, out = getTaskStatus(r, id)
	if s, _ := out["error"].(string); s == "" {
		t.Fatalf("失败任务应带 error： %v", out)
	}

	// detail 任务 done：消费并返回 follow 行（无条目时只回 done）
	id2, _ := st.CreateNetTask(tvfollow.PurposeDetail, "999", 1, "https://api.themoviedb.org/3/tv/x", "GET", nil, "", 20, false)
	st.ClaimNetTasks(1, 10)
	st.FinishNetTask(id2, 200, `{"status":"Returning Series"}`, "")
	code, out = getTaskStatus(r, id2)
	if code != http.StatusOK || out["state"] != "done" {
		t.Fatalf("detail 消费应 done： code=%d %v", code, out)
	}
}
