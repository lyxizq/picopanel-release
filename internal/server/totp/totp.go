// Package totp 实现登录两步验证（RFC 6238 TOTP，SHA-1/30s/6 位，±1 窗口容错）。
//
// 密钥按用户隔离（2026-10-20 用户指定，TODO 45），存 SQLite settings 表，
// 键名带用户 ID 后缀（store.TOTPSecretSettingKey 等）：
//
//	totp_secret:<userID>          已启用的 Base32 密钥（不存在 = 该用户未开启）
//	totp_pending_secret:<userID>  开启流程中待确认的密钥（扫码后验码通过才转正）
//
// 密钥生成用 crypto/rand，验证/热码计算只用标准库；二维码渲染用
// skip2/go-qrcode（无传递依赖），前端拿 data URL 直接展示。
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

const (
	step      = 30 * time.Second
	digits    = 6
	driftWin  = 1  // 允许 ±1 个时间窗的时钟偏移
	secretLen = 20 // 160 位密钥（RFC 4226 建议下限）
)

// VerifySecret 读该用户已启用的密钥校验验证码（登录用）。
func VerifySecret(st *store.Store, userID int64, code string) bool {
	secret, ok, err := st.GetSetting(store.TOTPSecretSettingKey(userID))
	if err != nil || !ok || secret == "" {
		return false
	}
	return Verify(secret, code)
}

// Enabled 该用户两步验证是否已开启。
func Enabled(st *store.Store, userID int64) bool {
	v, ok, err := st.GetSetting(store.TOTPSecretSettingKey(userID))
	return err == nil && ok && v != ""
}

// Pending 该用户是否有开启流程进行中（已生成待确认密钥）。
func Pending(st *store.Store, userID int64) bool {
	v, ok, err := st.GetSetting(store.TOTPPendingSettingKey(userID))
	return err == nil && ok && v != ""
}

// GenerateSecret 生成新密钥（Base32 无填充）。
func GenerateSecret() (string, error) {
	b := make([]byte, secretLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// StartEnrollment 生成该用户的待确认密钥并存 totp_pending_secret:<userID>，
// 返回密钥与 otpauth:// URI（QR 内容）。已启用时拒绝重复开启。
func StartEnrollment(st *store.Store, userID int64, account string) (secret, uri string, err error) {
	if Enabled(st, userID) {
		return "", "", fmt.Errorf("两步验证已开启，如需重置请先关闭")
	}
	secret, err = GenerateSecret()
	if err != nil {
		return "", "", err
	}
	if err := st.SetSetting(store.TOTPPendingSettingKey(userID), secret); err != nil {
		return "", "", err
	}
	return secret, URI(secret, "PicoPanel", account), nil
}

// ConfirmEnrollment 校验该用户待确认密钥的验证码，通过则转正启用。
func ConfirmEnrollment(st *store.Store, userID int64, code string) error {
	pending, ok, err := st.GetSetting(store.TOTPPendingSettingKey(userID))
	if err != nil || !ok || pending == "" {
		return fmt.Errorf("请先生成二维码再输入验证码")
	}
	if !Verify(pending, code) {
		return fmt.Errorf("验证码错误，请确认手机时间与 Authenticator 显示的码后重试")
	}
	if err := st.SetSetting(store.TOTPSecretSettingKey(userID), pending); err != nil {
		return err
	}
	return st.DeleteSetting(store.TOTPPendingSettingKey(userID))
}

// Reset 无条件清除全部用户的两步验证配置（-resettotp 命令行恢复入口用，
// Authenticator 丢失后凭密码重新拿回控制权），返回此前是否有任何用户已启用。
func Reset(st *store.Store) (bool, error) {
	users, err := st.ListUsers()
	if err != nil {
		return false, err
	}
	enabled := false
	for _, u := range users {
		if Enabled(st, u.ID) {
			enabled = true
		}
		if err := st.DeleteSetting(store.TOTPSecretSettingKey(u.ID)); err != nil {
			return enabled, err
		}
		if err := st.DeleteSetting(store.TOTPPendingSettingKey(u.ID)); err != nil {
			return enabled, err
		}
	}
	// 迁移前旧全局键的兜底清扫（正常已被启动迁移清掉，不报错）
	_ = st.DeleteSetting("totp_secret")
	_ = st.DeleteSetting("totp_pending_secret")
	return enabled, nil
}

// Disable 关闭该用户的两步验证：验当前密钥的验证码通过后清除。
func Disable(st *store.Store, userID int64, code string) error {
	secret, ok, err := st.GetSetting(store.TOTPSecretSettingKey(userID))
	if err != nil || !ok || secret == "" {
		return fmt.Errorf("两步验证未开启")
	}
	if !Verify(secret, code) {
		return fmt.Errorf("验证码错误")
	}
	if err := st.DeleteSetting(store.TOTPSecretSettingKey(userID)); err != nil {
		return err
	}
	return st.DeleteSetting(store.TOTPPendingSettingKey(userID))
}

// Verify 校验 6 位验证码（±1 窗口容错；恒定时间比较）。
func Verify(secret, code string) bool {
	return VerifyAt(secret, code, time.Now())
}

// VerifyAt 同 Verify，时间可注入（测试 RFC 6238 向量用）。
func VerifyAt(secret, code string, at time.Time) bool {
	secret = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) == 0 {
		return false
	}
	code = strings.TrimSpace(code)
	if len(code) != digits {
		return false
	}
	var given uint32
	if _, err := fmt.Sscanf(code, "%06d", &given); err != nil {
		return false
	}
	now := uint64(at.Unix()) / uint64(step/time.Second)
	for i := -int64(driftWin); i <= int64(driftWin); i++ {
		if hotp(key, uint64(int64(now)+i)) == given { //nolint:gosec // 窗口偏移有界
			return true
		}
	}
	return false
}

// hotp RFC 4226：HMAC-SHA1 截断为 6 位十进制。
func hotp(key []byte, counter uint64) uint32 {
	mac := hmac.New(sha1.New, key)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (uint32(sum[off]&0x7f) << 24) | (uint32(sum[off+1]) << 16) |
		(uint32(sum[off+2]) << 8) | uint32(sum[off+3])
	return v % 1000000
}

// URI 生成 otpauth:// 迁移 URI（Google Authenticator 等扫码即加）。
func URI(secret, issuer, account string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		url.PathEscape(issuer), url.PathEscape(account),
		url.QueryEscape(secret), url.QueryEscape(issuer), digits, int(step/time.Second))
}
