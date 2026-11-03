package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/ghwatch"
	"lz-monitor/internal/server/store"
)

// newGhRouter 真实链路：真实 Register（含鉴权组）+ 真实库 + 会话 token，
// GitHub API 打到 httptest mock；返回的 setStatus 供中途改 mock 状态码。
func newGhRouter(t *testing.T) (*gin.Engine, string, func(int)) {
	return newGhRouterAt(t, "2026-10-01T08:00:00Z")
}

// newGhRouterAt 同 newGhRouter，mock 的 Release 发布时刻可指定（489 用未来
// 时刻构造「seen_at 之后发布」的新更新场景）。
func newGhRouterAt(t *testing.T, publishedAt string) (*gin.Engine, string, func(int)) {
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
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`[
			{"tag_name":"v1.0.0","name":"major","draft":false,"prerelease":false,
			 "html_url":"https://github.com/cli/cli/releases/v1.0.0",
			 "published_at":"` + publishedAt + `","body":"notes"},
			{"tag_name":"v0.9.0","draft":false,"prerelease":true,
			 "html_url":"https://github.com/cli/cli/releases/v0.9.0","published_at":null}
		]`))
	}))
	t.Cleanup(srv.Close)
	m := ghwatch.New(s)
	m.SetAPIBase(srv.URL)
	r := gin.New()
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, nil, nil, nil, nil, nil, nil, m); err != nil {
		t.Fatal(err)
	}
	return r, token, func(code int) { status = code }
}

func ghReq(t *testing.T, r *gin.Engine, token, method, path, body string, wantCode int) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(w, req)
	if w.Code != wantCode {
		t.Fatalf("%s %s 状态 %d（期望 %d）: %s", method, path, w.Code, wantCode, w.Body.String())
	}
	return w.Body.String()
}

// TestGhFullChain 全链路（466）：未入库测试 → 添加（创建即首刷入库）→ 查重 →
// 列表 → 单仓刷新（失败带 warn 且错误落库）→ 启停 → 删除 → 上限 → 401/404。
func TestGhFullChain(t *testing.T) {
	r, token, setStatus := newGhRouter(t)
	base := "/api/v1"

	// 未入库测试端点：好形态回 releases；坏形态 ok:false
	res := ghReq(t, r, token, "POST", base+"/gh/test", `{"repo":"https://github.com/cli/cli"}`, http.StatusOK)
	if !strings.Contains(res, `"ok":true`) || !strings.Contains(res, "v1.0.0") {
		t.Fatalf("test 应报通并带 releases: %s", res)
	}
	res = ghReq(t, r, token, "POST", base+"/gh/test", `{"repo":"gitlab.com/cli/cli"}`, http.StatusOK)
	if !strings.Contains(res, `"ok":false`) {
		t.Fatalf("test 坏形态应 ok:false: %s", res)
	}

	// 添加（创建即首刷，快照入库；显示名称随行入库）
	res = ghReq(t, r, token, "POST", base+"/gh/repos", `{"repo":"cli/cli","display_name":"GitHub CLI"}`, http.StatusOK)
	var created struct {
		Repo struct {
			ID          int64  `json:"id"`
			FullName    string `json:"full_name"`
			DisplayName string `json:"display_name"`
			URL         string `json:"url"`
			Enabled     bool   `json:"enabled"`
			Releases    []struct {
				TagName    string `json:"tag_name"`
				Prerelease bool   `json:"prerelease"`
			} `json:"releases"`
		} `json:"repo"`
	}
	if err := json.Unmarshal([]byte(res), &created); err != nil || created.Repo.ID == 0 {
		t.Fatalf("建仓应回 repo: %s", res)
	}
	if created.Repo.FullName != "cli/cli" || created.Repo.DisplayName != "GitHub CLI" || created.Repo.URL != "https://github.com/cli/cli" || !created.Repo.Enabled ||
		len(created.Repo.Releases) != 2 || created.Repo.Releases[1].Prerelease != true {
		t.Fatalf("首刷快照不符: %+v", created.Repo)
	}
	id := created.Repo.ID

	// 重复添加 400；坏形态 400
	ghReq(t, r, token, "POST", base+"/gh/repos", `{"repo":"https://github.com/cli/cli.git"}`, http.StatusBadRequest)
	ghReq(t, r, token, "POST", base+"/gh/repos", `{"repo":"not a repo"}`, http.StatusBadRequest)

	// 列表嵌套
	res = ghReq(t, r, token, "GET", base+"/gh/repos", "", http.StatusOK)
	var list struct {
		Repos []struct {
			ID   int64 `json:"id"`
			Snap []struct {
				TagName string `json:"tag_name"`
			} `json:"releases"`
		} `json:"repos"`
		Max int `json:"max"`
	}
	if err := json.Unmarshal([]byte(res), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Repos) != 1 || list.Repos[0].ID != id || len(list.Repos[0].Snap) != 2 || list.Max != store.MaxGhRepos {
		t.Fatalf("列表不符: %s", res)
	}
	if !strings.Contains(res, `"poll_sec":14400`) || !strings.Contains(res, "1800") {
		t.Fatalf("列表应带默认轮询间隔与档位: %s", res)
	}

	// 轮询间隔设置：改 30 分生效；非白名单 400；脏档位不落库
	ghReq(t, r, token, "PUT", base+"/gh/settings", `{"poll_sec":1800}`, http.StatusOK)
	res = ghReq(t, r, token, "GET", base+"/gh/repos", "", http.StatusOK)
	if !strings.Contains(res, `"poll_sec":1800`) {
		t.Fatalf("改后列表应回 1800: %s", res)
	}
	ghReq(t, r, token, "PUT", base+"/gh/settings", `{"poll_sec":999}`, http.StatusBadRequest)
	res = ghReq(t, r, token, "GET", base+"/gh/repos", "", http.StatusOK)
	if !strings.Contains(res, `"poll_sec":1800`) {
		t.Fatalf("非白名单不应改值: %s", res)
	}

	// 停用 → 快照保留；列表 enabled=false
	ghReq(t, r, token, "PUT", base+fmt.Sprintf("/gh/repos/%d", id), `{"enabled":false}`, http.StatusOK)
	res = ghReq(t, r, token, "GET", base+"/gh/repos", "", http.StatusOK)
	if !strings.Contains(res, `"enabled":false`) || !strings.Contains(res, "v1.0.0") {
		t.Fatalf("停用应保留快照: %s", res)
	}

	// mock 翻 500：单仓刷新回 warn，错误落 last_error
	setStatus(http.StatusInternalServerError)
	res = ghReq(t, r, token, "POST", base+fmt.Sprintf("/gh/repos/%d/refresh", id), "", http.StatusOK)
	if !strings.Contains(res, `"warn"`) {
		t.Fatalf("刷新失败应带 warn: %s", res)
	}
	res = ghReq(t, r, token, "GET", base+"/gh/repos", "", http.StatusOK)
	if !strings.Contains(res, "GitHub API HTTP 500") {
		t.Fatalf("last_error 应落库: %s", res)
	}

	// 删除连带；二次删除 404
	ghReq(t, r, token, "DELETE", base+fmt.Sprintf("/gh/repos/%d", id), "", http.StatusOK)
	ghReq(t, r, token, "DELETE", base+fmt.Sprintf("/gh/repos/%d", id), "", http.StatusNotFound)
	res = ghReq(t, r, token, "GET", base+"/gh/repos", "", http.StatusOK)
	if strings.Contains(res, "v1.0.0") {
		t.Fatalf("删仓应连带清快照: %s", res)
	}

	// 上限：补到 16 个后第 17 个 400
	for i := 0; i < store.MaxGhRepos; i++ {
		ghReq(t, r, token, "POST", base+"/gh/repos",
			fmt.Sprintf(`{"repo":"owner%d/repo%d"}`, i, i), http.StatusOK)
	}
	ghReq(t, r, token, "POST", base+"/gh/repos", `{"repo":"overflow/repo"}`, http.StatusBadRequest)

	// 全部刷新（异步通路）+ 404 语义 + 未登录 401
	ghReq(t, r, token, "POST", base+"/gh/refresh", "", http.StatusOK)
	ghReq(t, r, token, "POST", base+"/gh/repos/999/refresh", "", http.StatusNotFound)
	ghReq(t, r, token, "PUT", base+"/gh/repos/999", `{"enabled":true}`, http.StatusNotFound)
	ghReq(t, r, token, "POST", base+"/gh/repos/999/seen", "", http.StatusNotFound)
	ghReq(t, r, "", "GET", base+"/gh/repos", "", http.StatusUnauthorized)
}

// TestGhHasNewSeen（489）：has_new 按 seen_at 基准判定（seen_at 之后发布的
// Release 算新更新，建仓首刷带回的存量不算），列表/建仓响应都带；单仓 seen
// 记账后消除。mock 发布时刻取未来，免依赖真实时钟。
func TestGhHasNewSeen(t *testing.T) {
	r, token, _ := newGhRouterAt(t, "2999-01-01T08:00:00Z")
	base := "/api/v1"

	// 建仓即首刷：Release 定档在未来（> seen_at=建仓时刻）→ has_new=true
	res := ghReq(t, r, token, "POST", base+"/gh/repos", `{"repo":"cli/cli"}`, http.StatusOK)
	var created struct {
		Repo struct {
			ID     int64 `json:"id"`
			HasNew bool  `json:"has_new"`
		} `json:"repo"`
	}
	if err := json.Unmarshal([]byte(res), &created); err != nil || created.Repo.ID == 0 {
		t.Fatalf("建仓应回 repo: %s", res)
	}
	if !created.Repo.HasNew {
		t.Fatalf("seen_at 之后发布的 Release 应 has_new=true: %s", res)
	}

	res = ghReq(t, r, token, "GET", base+"/gh/repos", "", http.StatusOK)
	if !strings.Contains(res, `"has_new":true`) {
		t.Fatalf("列表应带 has_new=true: %s", res)
	}

	// 单仓 seen 记账 → has_new 消除；未知仓库 404
	ghReq(t, r, token, "POST", base+fmt.Sprintf("/gh/repos/%d/seen", created.Repo.ID), "", http.StatusOK)
	res = ghReq(t, r, token, "GET", base+"/gh/repos", "", http.StatusOK)
	if strings.Contains(res, `"has_new":true`) {
		t.Fatalf("seen 记账后 has_new 应消除: %s", res)
	}
	ghReq(t, r, token, "POST", base+"/gh/repos/999/seen", "", http.StatusNotFound)
	ghReq(t, r, "", "POST", base+"/gh/repos/1/seen", "", http.StatusUnauthorized)
}
