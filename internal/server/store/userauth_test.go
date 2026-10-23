package store

import (
	"path/filepath"
	"testing"
)

// 44/45 存量迁移：全局单份头像/两步验证密钥在打开库时过户给首个用户（MIN(id)）
// 并删旧键；受限用户不继承 → 升级后受限用户默认关闭两步验证。迁移幂等（旧键
// 清掉后重开不再变动）。
func TestMigratePerUserAuthKeys(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mig.db")
	keyPath := filepath.Join(t.TempDir(), "key")

	// 造一个带旧全局键的库：admin（首用户）+ 一个受限用户
	s, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	uid, err := s.CreateUser("alice", "alice123", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if uid <= 0 {
		t.Fatalf("受限用户 id 异常: %d", uid)
	}
	for _, kv := range [][2]string{
		{"user_avatar", "data:image/png;base64,OLD"},
		{"totp_secret", "OLDSECRET32CHARSAABBCCDDEEFF0011"},
		{"totp_pending_secret", "OLDPENDING32CHARSAABBCCDDEEFF"},
	} {
		if err := s.SetSetting(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	aid := int64(1)
	if u, err := s.UserByUsername("admin"); err != nil || u == nil {
		t.Fatal(err)
	} else {
		aid = u.ID
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// 重开触发迁移：旧键过户给 admin、旧键删除；alice 名下无任何配置
	s2, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })

	if v, ok, _ := s2.GetSetting(AvatarSettingKey(aid)); !ok || v != "data:image/png;base64,OLD" {
		t.Fatalf("头像应过户给首个用户: v=%q ok=%v", v, ok)
	}
	if v, ok, _ := s2.GetSetting(TOTPSecretSettingKey(aid)); !ok || v != "OLDSECRET32CHARSAABBCCDDEEFF0011" {
		t.Fatalf("TOTP 密钥应过户给首个用户: v=%q ok=%v", v, ok)
	}
	if v, ok, _ := s2.GetSetting(TOTPPendingSettingKey(aid)); !ok || v != "OLDPENDING32CHARSAABBCCDDEEFF" {
		t.Fatalf("TOTP 待确认密钥应过户: v=%q ok=%v", v, ok)
	}
	for _, legacy := range []string{"user_avatar", "totp_secret", "totp_pending_secret"} {
		if _, ok, _ := s2.GetSetting(legacy); ok {
			t.Fatalf("旧键 %s 应已删除", legacy)
		}
	}
	if _, ok, _ := s2.GetSetting(TOTPSecretSettingKey(uid)); ok {
		t.Fatal("受限用户不应继承两步验证（默认关闭）")
	}
	if _, ok, _ := s2.GetSetting(AvatarSettingKey(uid)); ok {
		t.Fatal("受限用户不应继承旧头像")
	}

	// 幂等：再改再开，无旧键可迁，用户级键不被回写
	if err := s2.SetSetting(AvatarSettingKey(aid), "data:image/png;base64,NEW"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}
	s3, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s3.Close() })
	if v, _, _ := s3.GetSetting(AvatarSettingKey(aid)); v != "data:image/png;base64,NEW" {
		t.Fatalf("迁移不应回写用户级键: %q", v)
	}
}

// 44/45 删用户连带清理其头像与两步验证配置（用户级 settings 键不残留）。
func TestDeleteUserCleansUserKeys(t *testing.T) {
	s := openTestStore(t)
	uid, err := s.CreateUser("alice", "alice123", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{
		{AvatarSettingKey(uid), "data:image/png;base64,AAA"},
		{TOTPSecretSettingKey(uid), "SECRET"},
		{TOTPPendingSettingKey(uid), "PENDING"},
	} {
		if err := s.SetSetting(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteUser(uid); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{AvatarSettingKey(uid), TOTPSecretSettingKey(uid), TOTPPendingSettingKey(uid)} {
		if _, ok, _ := s.GetSetting(k); ok {
			t.Fatalf("删用户后 %s 应清理", k)
		}
	}
}
