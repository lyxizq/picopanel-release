package api

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// TestCacheControlDefaults 462 缓存兜底：一切 API 响应默认 no-store（生产在
// EdgeOne/ESA 边缘节点后，不依赖「源站无缓存头走边缘默认策略」的口子），
// 有意可缓存的 handler 用自己的缓存头覆盖——文档附件 private 长缓存口径
// （浏览器可缓存、边缘共享缓存不存）。
func TestCacheControlDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "cache.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	authH := &AuthHandler{st: s, guard: newLoginGuard(ipFails), userGuard: newLoginGuard(userFails)}
	docH := &DocHandler{st: s}
	msgH := &MessagesHandler{st: s}
	r := gin.New()
	r.Use(noStoreMiddleware())
	r.POST("/api/v1/auth/login", authH.Login)
	grp := r.Group("/api/v1", authRequired(s))
	grp.GET("/messages", msgH.List)
	grp.GET("/docs-assets/:id", docH.GetAsset)

	// 无会话的登录接口也在默认禁缓存覆盖内
	w := postJSON(r, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("登录应 200: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("登录响应应默认 no-store: %q", got)
	}
	token := mustLoginToken(t, r)

	// 业务接口（消息列表）：默认 no-store
	w = getJSON(r, "/api/v1/messages", token)
	if w.Code != http.StatusOK {
		t.Fatalf("消息列表应 200: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("业务接口应默认 no-store: %q", got)
	}

	// 文档附件：handler 覆盖为 private 长缓存
	id, err := s.CreateDocAsset("a.png", "image/png", []byte("png-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	w = getJSON(r, fmt.Sprintf("/api/v1/docs-assets/%d", id), token)
	if w.Code != http.StatusOK {
		t.Fatalf("附件应 200: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("附件应覆盖为 private 长缓存: %q", got)
	}
}
