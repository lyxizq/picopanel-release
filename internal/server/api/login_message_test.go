package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// newLoginMessageRouter 搭真实登录 + 消息中心路由（in-process httptest）。
func newLoginMessageRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "lm.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	authH := &AuthHandler{st: s, guard: newLoginGuard(ipFails), userGuard: newLoginGuard(userFails)}
	msgH := &MessagesHandler{st: s}
	r := gin.New()
	r.POST("/api/v1/auth/login", authH.Login)
	grp := r.Group("/api/v1", authRequired(s))
	grp.GET("/messages", msgH.List)
	grp.GET("/messages/unread", msgH.Unread)
	return r, s
}

func postJSON(r *gin.Engine, path, body, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r.ServeHTTP(w, req)
	return w
}

func getJSON(r *gin.Engine, path, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	return w
}

// TestLoginLandsInMessageCenter 真实登录请求链路：成功登录后消息中心查询
// 能拿到「登录」类消息（账号/时间/IP），未读角标计入；失败登录不产生消息。
func TestLoginLandsInMessageCenter(t *testing.T) {
	r, s := newLoginMessageRouter(t)

	// 先来一次失败登录：不应产生任何登录消息
	w := postJSON(r, "/api/v1/auth/login", `{"username":"admin","password":"wrong"}`, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("密码错应 401: %d %s", w.Code, w.Body.String())
	}
	p, err := s.ListMessages("login", false, 1, 10)
	if err != nil || p.Total != 0 {
		t.Fatalf("失败登录不应入站: total=%d err=%v", p.Total, err)
	}

	// 成功登录
	w = postJSON(r, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("登录应 200: %d %s", w.Code, w.Body.String())
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("登录应返回 token: %v %s", err, w.Body.String())
	}

	// 消息中心：按「登录」类型筛选能查到这条消息，字段与推送口径一致
	w = getJSON(r, "/api/v1/messages?type=login", lr.Token)
	if w.Code != http.StatusOK {
		t.Fatalf("消息查询应 200: %d %s", w.Code, w.Body.String())
	}
	var mp struct {
		Items []struct {
			Type      string `json:"type"`
			Title     string `json:"title"`
			Content   string `json:"content"`
			ReadAt    *int64 `json:"read_at"`
			CreatedAt int64  `json:"created_at"`
		} `json:"items"`
		Total  int `json:"total"`
		Unread int `json:"unread"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &mp); err != nil {
		t.Fatalf("解析消息列表: %v", err)
	}
	if mp.Total != 1 || len(mp.Items) != 1 {
		t.Fatalf("应有 1 条登录消息: %+v", mp)
	}
	m := mp.Items[0]
	if m.Type != "login" || m.Title != "登录提醒：admin" || m.ReadAt != nil {
		t.Fatalf("消息字段: %+v", m)
	}
	if !strings.Contains(m.Content, "账号 admin 于 ") || !strings.Contains(m.Content, "成功登录，来源 IP：") {
		t.Fatalf("消息正文口径: %q", m.Content)
	}
	if m.CreatedAt < time.Now().Add(-time.Minute).Unix() {
		t.Fatalf("消息时间应为刚刚: %d", m.CreatedAt)
	}
	if mp.Unread != 1 {
		t.Fatalf("未读数应 1: %d", mp.Unread)
	}

	// 未读角标接口
	w = getJSON(r, "/api/v1/messages/unread", lr.Token)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"count":1`) {
		t.Fatalf("未读角标应 1: %d %s", w.Code, w.Body.String())
	}

	// 类型隔离：告警筛选查不到
	w = getJSON(r, "/api/v1/messages?type=alert", lr.Token)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("告警筛选应 0: %d %s", w.Code, w.Body.String())
	}
}
