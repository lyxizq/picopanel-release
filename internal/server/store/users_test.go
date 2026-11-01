package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func openUserTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "users.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestUpdateUsername 改用户名：改名可登录、会话不掉、重名报错。
func TestUpdateUsername(t *testing.T) {
	s := openUserTestStore(t)
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatalf("查 admin: %v %v", u, err)
	}
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateUsername(u.ID, "zhangsan"); err != nil {
		t.Fatal(err)
	}
	// 旧用户名不再命中，新用户名命中；会话仍有效（按 user_id 关联）
	if old, err := s.UserByUsername("admin"); err != nil || old != nil {
		t.Fatalf("旧用户名应查无: %+v %v", old, err)
	}
	nu, err := s.UserByUsername("zhangsan")
	if err != nil || nu == nil {
		t.Fatalf("新用户名应命中: %v %v", nu, err)
	}
	su, err := s.SessionUser(token)
	if err != nil || su == nil || su.Username != "zhangsan" {
		t.Fatalf("改名后会话应有效且带新用户名: %+v %v", su, err)
	}
	// 按 ID 重查拿到新用户名
	byID, err := s.UserByID(u.ID)
	if err != nil || byID == nil || byID.Username != "zhangsan" {
		t.Fatalf("UserByID: %+v %v", byID, err)
	}

	// 重名：先造第二个用户再改过去
	hash, _ := bcrypt.GenerateFromPassword([]byte("x2password"), bcrypt.MinCost)
	if _, err := s.db.Exec(`INSERT INTO users (username, password_hash, created_at) VALUES (?,?,?)`,
		"other", string(hash), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUsername(u.ID, "other"); !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("重名应报 ErrUsernameExists: %v", err)
	}
}

// TestSessionTokenHashedAtRest 会话 token 哈希落库（65）：库里存 SHA-256 而
// 非明文；存量明文行在 SessionUser 命中时原位升级；注销/踢出对双形态生效。
func TestSessionTokenHashedAtRest(t *testing.T) {
	s := openUserTestStore(t)
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, _ := s.UserByUsername("admin")

	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// 库里是哈希：明文不落库，但按哈希形态查得到、校验通过
	var stored string
	if err := s.db.QueryRow(`SELECT token FROM sessions WHERE user_id = ?`, u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("会话 token 不应明文落库")
	}
	if stored != hashSessionToken(token) {
		t.Fatalf("库内应为 token 的 SHA-256: %s", stored)
	}
	if su, err := s.SessionUser(token); err != nil || su == nil {
		t.Fatalf("哈希会话应校验通过: %+v %v", su, err)
	}

	// 明文回退已删除（2026-10-23 安全加固）：token 明文与哈希同为 64 位 hex，
	// 库内存储值本身可当 token 走明文等值命中=库泄露即接管会话；哈希化迁移
	// 上线已久，存量明文会话（7 天 TTL）早已过期——手工造一行明文 token
	// 必须不再可用（既不命中也不升级，行原样躺着等过期清理）
	legacy := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := s.db.Exec(`INSERT INTO sessions (token, user_id, created_at, expires_at) VALUES (?,?,?,?)`,
		legacy, u.ID, time.Now().Unix(), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if su, err := s.SessionUser(legacy); err != nil || su != nil {
		t.Fatalf("明文 token 不得命中: %+v %v", su, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token = ?`, legacy).Scan(&n); err != nil || n != 1 {
		t.Fatalf("明文行应原样保留（不升级不删除）: n=%d %v", n, err)
	}

	// 注销对存量明文形态也生效（DeleteSession 双形态）
	if err := s.DeleteSession(legacy); err != nil {
		t.Fatal(err)
	}
	if su, _ := s.SessionUser(legacy); su != nil {
		t.Fatal("注销后明文形态会话应失效")
	}

	// DeleteOtherSessions：空 keepToken=全清（哈希化与存量形态都清不掉非本行——此处验证全清语义）
	if _, err := s.CreateSession(u.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOtherSessions(u.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, u.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("空 keepToken 应全清: n=%d %v", n, err)
	}
}

// TestResetFirstAdmin -resetuser 路径：改名+改密后的管理员重置回默认，
// 会话全清，原密码失效、默认密码可过。
func TestResetFirstAdmin(t *testing.T) {
	s := openUserTestStore(t)
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, _ := s.UserByUsername("admin")
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// 用户改名 + 改密（模拟忘记密码前的状态）
	if err := s.UpdateUsername(u.ID, "someone"); err != nil {
		t.Fatal(err)
	}
	newHash, _ := bcrypt.GenerateFromPassword([]byte("forgotten-pw"), bcrypt.MinCost)
	if err := s.UpdatePassword(u.ID, string(newHash)); err != nil {
		t.Fatal(err)
	}

	resetHash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.MinCost)
	reset, err := s.ResetFirstAdmin("admin", string(resetHash))
	if err != nil || !reset {
		t.Fatalf("重置应成功: %v %v", reset, err)
	}
	after, _ := s.UserByUsername("admin")
	if after == nil {
		t.Fatal("应能以 admin 查到用户")
	}
	if bcrypt.CompareHashAndPassword([]byte(after.PasswordHash), []byte("admin123")) != nil {
		t.Fatal("密码应已重置为 admin123")
	}
	if su, err := s.SessionUser(token); err != nil || su != nil {
		t.Fatalf("重置后会话应全部失效: %+v %v", su, err)
	}
}
