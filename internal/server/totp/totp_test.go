package totp

import (
	"encoding/base32"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// Reset 清除全部用户启用/待确认密钥的往返（-resettotp 恢复入口语义），
// 且按用户隔离——一个用户开启不影响另一个（45）。
func TestReset(t *testing.T) {
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	aid, err := st.CreateUser("admin", "admin123", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := st.CreateUser("alice", "alice123", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}

	if Enabled(st, aid) {
		t.Fatal("新用户不应已启用")
	}
	if had, err := Reset(st); err != nil || had {
		t.Fatalf("无人启用时 Reset 应返回 false: had=%v err=%v", had, err)
	}
	if err := st.SetSetting(store.TOTPPendingSettingKey(aid), "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err != nil {
		t.Fatal(err)
	}
	secret, _, err := StartEnrollment(st, aid, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(store.TOTPSecretSettingKey(aid), secret); err != nil { // 模拟已启用
		t.Fatal(err)
	}
	// 隔离：admin 已启用，alice 不受牵连
	if !Enabled(st, aid) || Enabled(st, uid) || Pending(st, uid) {
		t.Fatal("两步验证应按用户隔离：admin 开启、alice 默认关闭")
	}
	had, err := Reset(st)
	if err != nil || !had {
		t.Fatalf("已启用时 Reset 应返回 true: had=%v err=%v", had, err)
	}
	if Enabled(st, aid) || Pending(st, aid) {
		t.Fatal("Reset 后启用/待确认密钥都应被清除")
	}
}

func testKey(t *testing.T, secret string) []byte {
	t.Helper()
	k, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// RFC 6238 附录 B 官方测试向量（SHA-1；截断函数相同，8 位码取后 6 位与本项目口径一致）。
func TestVerifyRFC6238Vectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // base32("12345678901234567890")
	cases := []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, c := range cases {
		key := testKey(t, secret)
		got := fmt.Sprintf("%06d", hotp(key, uint64(c.unix)/30))
		if got != c.code {
			t.Errorf("t=%d 应为 %s，实得 %s", c.unix, c.code, got)
		}
		if !VerifyAt(secret, c.code, time.Unix(c.unix, 0)) {
			t.Errorf("t=%d 码 %s 应通过", c.unix, c.code)
		}
	}
	// 错误码 / 漂移窗外拒绝，±1 窗口内通过
	if VerifyAt(secret, "000001", time.Unix(59, 0)) {
		t.Error("错误码不应通过")
	}
	if VerifyAt(secret, "287082", time.Unix(59+4*30, 0)) {
		t.Error("超出 ±1 窗口不应通过")
	}
	if !VerifyAt(secret, "287082", time.Unix(59+30, 0)) {
		t.Error("+1 窗口内应通过")
	}
}

func TestGenerateSecret(t *testing.T) {
	s1, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := GenerateSecret()
	if len(s1) != 32 { // 160 位 → base32 32 字符
		t.Fatalf("密钥长度应为 32: %d", len(s1))
	}
	if s1 == s2 {
		t.Fatal("两次生成的密钥不应相同")
	}
	key := testKey(t, s1)
	now := uint64(time.Now().Unix()) / 30
	if !Verify(s1, fmt.Sprintf("%06d", hotp(key, now))) {
		t.Fatal("当前窗口码应通过 Verify")
	}
}

// 验证码重放防护防回归（2026-10-23 安全加固）：同一时间窗的码只可消费一次，
// 更早窗口（±1 容错内）同样拒绝；新窗口的码正常放行。
func TestConsumeWindowReplay(t *testing.T) {
	now := time.Now()
	w0, ok := verifyWindow(replayTestSecret, testCodeAt(now), now)
	if !ok {
		t.Fatal("当前窗口码应验证通过")
	}
	if !consumeWindow(1, w0) {
		t.Fatal("首次消费应放行")
	}
	if consumeWindow(1, w0) {
		t.Fatal("同窗重放应拒绝")
	}
	if consumeWindow(1, w0-1) {
		t.Fatal("更早容错窗重放应拒绝")
	}
	w1, ok := verifyWindow(replayTestSecret, testCodeAt(now.Add(31*time.Second)), now.Add(31*time.Second))
	if !ok || !consumeWindow(1, w1) {
		t.Fatal("下一窗口的码应正常放行")
	}
}

// replayTestSecret RFC 4226 测试密钥（同上方向量用）。
const replayTestSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// testCodeAt 用与 RFC 向量测试相同的生成方式产码（时间注入）。
func testCodeAt(at time.Time) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(replayTestSecret)
	return fmt.Sprintf("%06d", hotp(key, uint64(at.Unix())/30))
}
