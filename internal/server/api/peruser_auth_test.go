package api

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// 44/45/46 多用户自助能力回归：头像与两步验证按会话用户隔离（受限用户同样
// 可配置、管理员开启不牵连他人），受限用户登录照发提醒并计入登录日志。

// newPerUserAuthRouter 搭真实登录 + 自助组（totp/avatar/profile）路由，含
// admin 与受限用户 alice 两个账号，返回 admin 的登录 token 备用。
func newPerUserAuthRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "pu.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("alice", "alice123", store.RoleUser); err != nil {
		t.Fatal(err)
	}
	authH := &AuthHandler{st: s, guard: newLoginGuard(ipFails), userGuard: newLoginGuard(userFails)}
	r := gin.New()
	r.POST("/api/v1/auth/login", authH.Login)
	grp := r.Group("/api/v1", authRequired(s))
	grp.GET("/auth/totp-status", authH.TOTPStatus)
	grp.POST("/auth/totp-setup", authH.TOTPSetup)
	grp.POST("/auth/totp-enable", authH.TOTPEnable)
	grp.POST("/auth/totp-disable", authH.TOTPDisable)
	grp.GET("/auth/avatar", authH.Avatar)
	grp.PUT("/auth/avatar", authH.UpdateAvatar)
	return r, s
}

// totpCodeAt 按测试密钥现算 6 位验证码（与 totp 包同参数：SHA-1/30s/6 位）。
func totpCodeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha1.New, key)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(at.Unix())/30)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (uint32(sum[off]&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])) % 1000000
	return fmt.Sprintf("%06d", v)
}

// loginWith 指定验证码登录，返回响应 recorder。
func loginWith(r *gin.Engine, username, password, code string) *httptest.ResponseRecorder {
	return postJSON(r, "/api/v1/auth/login",
		fmt.Sprintf(`{"username":%q,"password":%q,"code":%q}`, username, password, code), "")
}

// TestLoginPerUserTOTP 两步验证按用户隔离（45）：管理员开启后受限用户登录
// 依旧免验码（默认关闭）；各自开启只约束本人，开启/关闭全走 /auth/totp-*。
func TestLoginPerUserTOTP(t *testing.T) {
	r, _ := newPerUserAuthRouter(t)

	// alice（受限用户）先登录拿 token：此时尚无任何人开启
	w := loginWith(r, "alice", "alice123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("受限用户登录应 200: %d %s", w.Code, w.Body.String())
	}
	aliceToken := bodyField(t, w, "token")

	// 管理员自助开启两步验证
	w = loginWith(r, "admin", "admin123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("管理员登录应 200: %d %s", w.Code, w.Body.String())
	}
	adminToken := bodyField(t, w, "token")
	w = postJSON(r, "/api/v1/auth/totp-setup", "{}", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("totp-setup 应 200: %d %s", w.Code, w.Body.String())
	}
	secret := bodyField(t, w, "secret")
	w = postJSON(r, "/api/v1/auth/totp-enable",
		fmt.Sprintf(`{"code":%q}`, totpCodeAt(t, secret, time.Now())), adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("totp-enable 应 200: %d %s", w.Code, w.Body.String())
	}

	// 核心钉：管理员开了两步验证，受限用户不带验证码照常登录（默认关闭）
	w = loginWith(r, "alice", "alice123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("管理员开启后受限用户登录应免验码 200: %d %s", w.Code, w.Body.String())
	}

	// 管理员本人：无码 401 need_code、错码 401、对码 200
	w = loginWith(r, "admin", "admin123", "")
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "need_code") {
		t.Fatalf("管理员无码登录应 401 need_code: %d %s", w.Code, w.Body.String())
	}
	w = loginWith(r, "admin", "admin123", "000000")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("管理员错码登录应 401: %d %s", w.Code, w.Body.String())
	}
	w = loginWith(r, "admin", "admin123", totpCodeAt(t, secret, time.Now()))
	if w.Code != http.StatusOK {
		t.Fatalf("管理员对码登录应 200: %d %s", w.Code, w.Body.String())
	}

	// alice 也开启自己的：只约束本人；关闭同样自助（验码后清除）
	w = postJSON(r, "/api/v1/auth/totp-setup", "{}", aliceToken)
	if w.Code != http.StatusOK {
		t.Fatalf("受限用户 totp-setup 应 200: %d %s", w.Code, w.Body.String())
	}
	aliceSecret := bodyField(t, w, "secret")
	w = postJSON(r, "/api/v1/auth/totp-enable",
		fmt.Sprintf(`{"code":%q}`, totpCodeAt(t, aliceSecret, time.Now())), aliceToken)
	if w.Code != http.StatusOK {
		t.Fatalf("受限用户 totp-enable 应 200: %d %s", w.Code, w.Body.String())
	}
	w = loginWith(r, "alice", "alice123", "")
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "need_code") {
		t.Fatalf("alice 开启后无码登录应 401 need_code: %d %s", w.Code, w.Body.String())
	}
	w = loginWith(r, "alice", "alice123", totpCodeAt(t, aliceSecret, time.Now()))
	if w.Code != http.StatusOK {
		t.Fatalf("alice 对码登录应 200: %d %s", w.Code, w.Body.String())
	}
	aliceToken2 := bodyField(t, w, "token")
	w = postJSON(r, "/api/v1/auth/totp-disable", `{"code":"000000"}`, aliceToken2)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("错码关闭应 400: %d %s", w.Code, w.Body.String())
	}
	w = postJSON(r, "/api/v1/auth/totp-disable",
		fmt.Sprintf(`{"code":%q}`, totpCodeAt(t, aliceSecret, time.Now().Add(31*time.Second))), aliceToken2)
	if w.Code != http.StatusOK {
		t.Fatalf("对码关闭应 200: %d %s", w.Code, w.Body.String())
	}
	w = loginWith(r, "alice", "alice123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("关闭后应恢复免验码登录: %d %s", w.Code, w.Body.String())
	}
}

// TestAvatarPerUser 头像按用户隔离（44）：两人各自上传互不串；受限用户同样
// 可上传/移除（PUT 在登录组）；未设置返回空串。
func TestAvatarPerUser(t *testing.T) {
	r, _ := newPerUserAuthRouter(t)

	w := loginWith(r, "admin", "admin123", "")
	adminToken := bodyField(t, w, "token")
	w = loginWith(r, "alice", "alice123", "")
	aliceToken := bodyField(t, w, "token")

	// 两人各传各的
	for _, tc := range []struct{ token, data string }{
		{adminToken, "data:image/png;base64,AAAA"},
		{aliceToken, "data:image/png;base64,BBBB"},
	} {
		w = putAvatar(r, tc.token, tc.data)
		if w.Code != http.StatusOK {
			t.Fatalf("上传头像应 200: %d %s", w.Code, w.Body.String())
		}
	}
	// 各自读到自己的
	if got := avatarOf(t, r, adminToken); got != "data:image/png;base64,AAAA" {
		t.Fatalf("admin 头像不应串: %q", got)
	}
	if got := avatarOf(t, r, aliceToken); got != "data:image/png;base64,BBBB" {
		t.Fatalf("alice 头像不应串: %q", got)
	}
	// 受限用户移除自己的不影响管理员
	w = putAvatar(r, aliceToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("受限用户移除头像应 200: %d %s", w.Code, w.Body.String())
	}
	if got := avatarOf(t, r, aliceToken); got != "" {
		t.Fatalf("alice 移除后应为空: %q", got)
	}
	if got := avatarOf(t, r, adminToken); got != "data:image/png;base64,AAAA" {
		t.Fatalf("admin 头像应不受影响: %q", got)
	}
}

func putAvatar(r *gin.Engine, token, data string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/avatar",
		strings.NewReader(fmt.Sprintf(`{"avatar":%q}`, data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	return w
}

func avatarOf(t *testing.T, r *gin.Engine, token string) string {
	t.Helper()
	w := getJSON(r, "/api/v1/auth/avatar", token)
	if w.Code != http.StatusOK {
		t.Fatalf("avatar 应 200: %d %s", w.Code, w.Body.String())
	}
	return bodyField(t, w, "avatar")
}

// bodyField 从 JSON 响应取字符串字段（解析失败即 Fatal，缺键回空串）。
func bodyField(t *testing.T, w *httptest.ResponseRecorder, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析响应失败: %v %s", err, w.Body.String())
	}
	v, _ := m[key].(string)
	return v
}

// TestRestrictedUserLoginLoggedAndNotified 受限用户登录同样计入登录日志并落
// 「登录」站内消息（46：与管理员同口径，不因 role=user 缺席）。
func TestRestrictedUserLoginLoggedAndNotified(t *testing.T) {
	r, s := newPerUserAuthRouter(t)

	w := loginWith(r, "alice", "alice123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("受限用户登录应 200: %d %s", w.Code, w.Body.String())
	}

	logs, err := s.ListLoginLog()
	if err != nil || len(logs) == 0 {
		t.Fatalf("受限用户登录应计入登录日志: n=%d err=%v", len(logs), err)
	}
	if logs[0].Username != "alice" || logs[0].IP == "" {
		t.Fatalf("日志应记 alice 与来源 IP: %+v", logs[0])
	}
	msgs, err := s.ListMessages("", false, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs.Items {
		if m.Type == "login" && strings.Contains(m.Title, "alice") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("受限用户登录应落「登录」站内消息: %+v", msgs)
	}
}
