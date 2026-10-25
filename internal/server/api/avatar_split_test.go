package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// 6 头像拆出 /auth/me：data URL 可达 300KB，随 me 每次进页面全量传输是
// 首页 me 请求 17s 的主因——me 只留 username/role，头像走独立 /auth/avatar
// private 短缓存（前端以 avatarVer 版本参数破缓存）。44 起按会话用户读写。
func TestMeDropsAvatarAndAvatarEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "a.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal(err)
	}
	h := &AuthHandler{st: s, guard: newLoginGuard(ipFails), userGuard: newLoginGuard(userFails)}
	withUser := func(c *gin.Context) { c.Set("user", u); c.Next() }
	r := gin.New()
	r.GET("/api/v1/auth/me", func(c *gin.Context) {
		c.Set("username", "admin")
		c.Set("role", "admin")
		h.Me(c)
	})
	r.GET("/api/v1/auth/avatar", withUser, h.Avatar)

	if err := s.SetSetting(store.AvatarSettingKey(u.ID), "data:image/png;base64,AAAA"); err != nil {
		t.Fatal(err)
	}

	// me：不再含 avatar 字段
	w := getJSON(r, "/api/v1/auth/me", "")
	if w.Code != http.StatusOK {
		t.Fatalf("me 应 200: %d %s", w.Code, w.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["avatar"]; ok {
		t.Fatalf("me 不应再返回 avatar: %s", w.Body.String())
	}
	if m["username"] != "admin" || m["role"] != "admin" {
		t.Fatalf("me 应保留 username/role: %s", w.Body.String())
	}

	// avatar：返回 data URL 且 private 短缓存
	w = getJSON(r, "/api/v1/auth/avatar", "")
	if w.Code != http.StatusOK {
		t.Fatalf("avatar 应 200: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "private, max-age=300" {
		t.Fatalf("avatar 应 private 短缓存: %q", got)
	}
	var av struct {
		Avatar string `json:"avatar"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &av); err != nil {
		t.Fatal(err)
	}
	if av.Avatar != "data:image/png;base64,AAAA" {
		t.Fatalf("avatar 应返回设置的 data URL: %q", av.Avatar)
	}

	// 未设置头像时返回空串而非 null
	if err := s.DeleteSetting(store.AvatarSettingKey(u.ID)); err != nil {
		t.Fatal(err)
	}
	w = getJSON(r, "/api/v1/auth/avatar", "")
	if err := json.Unmarshal(w.Body.Bytes(), &av); err != nil {
		t.Fatal(err)
	}
	if av.Avatar != "" {
		t.Fatalf("未设置头像应返回空串: %q", av.Avatar)
	}
}

// 6 静态缓存分级：assets/ 带哈希永久缓存；index.html 与 sw.js 每次回源；
// 其余无哈希根文件（favicon/manifest/pwa 图标/vendor）1 天缓存——响应无
// ETag 可供 304，no-cache 等于每次整文件重拉，只留给入口页与 SW。
func TestServeStaticCacheClasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fsys := fstest.MapFS{
		"assets/app-abc123.js": {Data: []byte("console.log(1)")},
		"index.html":           {Data: []byte("<html></html>")},
		"sw.js":                {Data: []byte("self.addEventListener('push',()=>{})")},
		"favicon.svg":          {Data: []byte("<svg/>")},
	}
	for _, tc := range []struct{ name, want string }{
		{"assets/app-abc123.js", "public, max-age=31536000, immutable"},
		{"index.html", "no-cache"},
		{"sw.js", "no-cache"},
		{"favicon.svg", "public, max-age=86400"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/"+tc.name, nil)
		if !serveStatic(c, tc.name, fsys) {
			t.Fatalf("%s 应可服务", tc.name)
		}
		if got := w.Header().Get("Cache-Control"); got != tc.want {
			t.Fatalf("%s 缓存头应 %q: %q", tc.name, tc.want, got)
		}
	}
}
