package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretBoxRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	box, err := newSecretBox(key)
	if err != nil {
		t.Fatal(err)
	}
	const plain = "192.168.1.5:19100"
	ct := box.seal(plain)
	if !strings.HasPrefix(ct, encPrefix) {
		t.Fatalf("密文应有前缀: %q", ct)
	}
	if ct == plain {
		t.Fatal("密文不应等于明文")
	}
	if got := box.open(ct); got != plain {
		t.Fatalf("round-trip = %q, want %q", got, plain)
	}
	// 同一明文两次加密密文不同（随机 nonce），但都能解开
	ct2 := box.seal(plain)
	if ct2 == ct {
		t.Fatal("随机 nonce 下两次密文不应相同")
	}
	if box.open(ct2) != plain {
		t.Fatalf("第二个密文解不开: %q", ct2)
	}
	// 空串与幂等
	if box.seal("") != "" || box.open("") != "" {
		t.Fatal("空串应原样返回")
	}
	if box.seal(ct) != ct {
		t.Fatal("对密文再加密应幂等")
	}
	// 历史明文（无前缀）open 原样返回（迁移前兼容）
	if got := box.open("root"); got != "root" {
		t.Fatalf("无前缀明文应原样返回, got %q", got)
	}
	// 错误密钥/损坏数据解不出内容
	other := make([]byte, 32)
	box2, _ := newSecretBox(other)
	if got := box2.open(ct); got != "" {
		t.Fatalf("错误密钥应返回空串, got %q", got)
	}
	if got := box.open(encPrefix + "!!!not-base64!!!"); got != "" {
		t.Fatalf("损坏密文应返回空串, got %q", got)
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "lm.idc") // 目录不存在，应自动创建
	k1, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 {
		t.Fatalf("密钥长度 = %d, want 32", len(k1))
	}
	k2, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("二次读取应得到同一密钥")
	}
	// 内容非法时报错而不是静默用错密钥
	_ = os.WriteFile(path, []byte("short"), 0o600)
	if _, err := loadOrCreateKey(path); err == nil {
		t.Fatal("无效内容应报错")
	}
}

// TestMigrateEncryptPlaintext 验证旧明文数据启动时自动加密、读取透明还原。
func TestMigrateEncryptPlaintext(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "t.db"), filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv, err := newSSHServer(s, "web", "10.0.0.8:22", "root", "", "pass123", 0)
	if err != nil {
		t.Fatal(err)
	}
	// 直连库模拟旧明文数据：改为未加密的字面量
	if _, err := s.db.Exec(`UPDATE servers SET agent_addr = '10.0.0.8:22', ssh_user = 'root',
		ssh_password = 'pass123' WHERE id = ?`, srv.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateEncryptPlaintext(); err != nil {
		t.Fatal(err)
	}
	// 库里不再有明文
	var addr, user, pwd, tok string
	if err := s.db.QueryRow(`SELECT agent_addr, ssh_user, ssh_password, COALESCE(token,'')
		FROM servers WHERE id = ?`, srv.ID).Scan(&addr, &user, &pwd, &tok); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"agent_addr": addr, "ssh_user": user, "ssh_password": pwd} {
		if !strings.HasPrefix(v, encPrefix) {
			t.Fatalf("%s 迁移后应加密, got %q", name, v)
		}
	}
	// 读取透明还原为明文；二次迁移幂等
	got, err := s.GetServer(srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentAddr != "10.0.0.8:22" || got.SSHUser != "root" || got.SSHPassword != "pass123" {
		t.Fatalf("解密还原错误: %+v", got)
	}
	if err := s.migrateEncryptPlaintext(); err != nil {
		t.Fatal(err)
	}
}

// TestPushTokenUniqueEncrypted 加密存储下 push 密钥一对一约束仍生效
// （密文不可 SQL 等值查找，靠解密后内存比较）。
func TestPushTokenUniqueEncrypted(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "t.db"), filepath.Join(t.TempDir(), "k"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreatePushServer("a", "secret-token", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePushServer("b", "secret-token", 0); err != ErrTokenUsed {
		t.Fatalf("重复密钥应返回 ErrTokenUsed, got %v", err)
	}
	// 上报密钥清单可解密出明文
	list, err := s.PushTokenList()
	if err != nil || len(list) != 1 || list[0].Token != "secret-token" {
		t.Fatalf("PushTokenList = %+v, err=%v", list, err)
	}
}
