package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// newProfileRouter 搭带会话鉴权的 profile 路由测试服务，返回登录 token。
func newProfileRouter(t *testing.T) (*gin.Engine, *store.Store, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "auth.db"), filepath.Join(t.TempDir(), "key"))
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
	h := &AuthHandler{st: s, guard: newLoginGuard(ipFails), userGuard: newLoginGuard(userFails)}
	r := gin.New()
	grp := r.Group("/api/v1", authRequired(s))
	grp.PUT("/auth/profile", h.UpdateProfile)
	return r, s, token
}

func putProfile(r *gin.Engine, token, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/profile", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	return w
}

// TestUpdateProfile 旧密码错拒改；改用户名成功且会话不掉、返回新用户名；
// 用户名+密码一起改；重名 400。
func TestUpdateProfile(t *testing.T) {
	r, s, token := newProfileRouter(t)

	// 旧密码错 → 400
	w := putProfile(r, token, `{"username":"admin","old_password":"wrong","new_password":""}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "旧密码错误") {
		t.Fatalf("旧密码错应 400: %d %s", w.Code, w.Body.String())
	}

	// 改用户名（不动密码）
	w = putProfile(r, token, `{"username":"zhangsan","old_password":"admin123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("改用户名: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"username":"zhangsan"`) {
		t.Fatalf("应返回新用户名: %s", w.Body.String())
	}
	// 当前会话仍有效（token 不因改名失效）
	if u, err := s.SessionUser(token); err != nil || u == nil || u.Username != "zhangsan" {
		t.Fatalf("改名后当前会话应有效: %+v %v", u, err)
	}
	// 新用户名 + 新密码一起改
	w = putProfile(r, token, `{"username":"lisi","old_password":"admin123","new_password":"newpass9"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("用户名密码一起改: %d %s", w.Code, w.Body.String())
	}
	u, err := s.UserByUsername("lisi")
	if err != nil || u == nil {
		t.Fatalf("新用户名应命中: %v %v", u, err)
	}

	// 重名（改成已存在的其他用户名）→ 400
	if _, err := s.DB().Exec(`INSERT INTO users (username, password_hash, created_at) VALUES (?,?,?)`,
		"taken", "$2a$04$placeholder-hash-not-for-login", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	w = putProfile(r, token, `{"username":"taken","old_password":"newpass9"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "已存在") {
		t.Fatalf("重名应 400: %d %s", w.Code, w.Body.String())
	}
}

// 会话哈希落库防回归（2026-10-23 安全加固）：库内存储的哈希串本身当 token
// 提交必须失败——旧版明文回退路径会让「拿到库文件=接管活会话」。
func TestSessionStoredHashNotUsableAsToken(t *testing.T) {
	dir := t.TempDir()
	s, err := store.OpenWithKey(filepath.Join(dir, "t.db"), filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	uid, err := s.CreateUser("admin", "password123", "admin")
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.CreateSession(uid, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.SessionUser(token); err != nil || u == nil || u.Username != "admin" {
		t.Fatalf("明文 token 应命中: u=%v err=%v", u, err)
	}
	// 从库里读出存储值（哈希），当 token 原样提交必须失败
	var stored string
	if err := s.DB().QueryRow(`SELECT token FROM sessions LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("库内应存哈希而非明文")
	}
	if u, err := s.SessionUser(stored); err != nil || u != nil {
		t.Fatalf("库内哈希串不得当作有效 token: u=%v err=%v", u, err)
	}
}
