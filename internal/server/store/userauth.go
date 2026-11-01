package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// 用户级 settings 键（2026-10-20 用户指定，TODO 44/45）：头像与登录两步验证
// 按用户隔离——键名 = 旧全局键 + :<userID> 后缀。按会话用户 ID 读写，改名
// 不掉配置；删用户由 DeleteUser 连带清理。
func AvatarSettingKey(userID int64) string { return fmt.Sprintf("user_avatar:%d", userID) }

func TOTPSecretSettingKey(userID int64) string  { return fmt.Sprintf("totp_secret:%d", userID) }
func TOTPPendingSettingKey(userID int64) string { return fmt.Sprintf("totp_pending_secret:%d", userID) }

// 隔离前的旧全局键，仅存量迁移用，迁移后全库不再出现。
const (
	legacyAvatarKey   = "user_avatar"
	legacyTotpSecret  = "totp_secret"
	legacyTotpPending = "totp_pending_secret"
)

// migratePerUserAuthKeys 把旧全局头像/两步验证配置过户给首个用户（MIN(id)：
// 历史上系统单管理员，全局键即其本人配置）。每个旧键独立处理：值非空才过户，
// 空值/无此键直接清掉旧键，保证迁移只跑一次（幂等）。用户表为空时（理论上
// 不发生：启动先建表后到此处）无过户对象，仅清旧键。
// 受限用户不继承任何旧配置 → 升级后所有受限用户默认关闭登录两步验证。
func (s *Store) migratePerUserAuthKeys() error {
	pairs := []struct {
		legacy string
		key    func(int64) string
	}{
		{legacyAvatarKey, AvatarSettingKey},
		{legacyTotpSecret, TOTPSecretSettingKey},
		{legacyTotpPending, TOTPPendingSettingKey},
	}
	for _, p := range pairs {
		v, ok, err := s.GetSetting(p.legacy)
		if err != nil {
			return err
		}
		if ok && v != "" {
			var first sql.NullInt64
			if err := s.db.QueryRow(`SELECT MIN(id) FROM users`).Scan(&first); err != nil {
				return err
			}
			if first.Valid {
				if err := s.SetSetting(p.key(first.Int64), v); err != nil {
					return err
				}
			}
		}
		if err := s.DeleteSetting(p.legacy); err != nil {
			return err
		}
	}
	return nil
}

// migrateTOTPSealed 把存量库里明文存放的两步验证密钥加密（2026-10-23 安全
// 加固：密钥明文入库，库泄露可直接克隆验证器；改走 SetSettingSealed 后补迁移，
// 幂等——已带 enc 前缀的行不动）。头像为 data URL 非机密，不在其列。
func (s *Store) migrateTOTPSealed() error {
	rows, err := s.db.Query(`SELECT key, value FROM settings
		WHERE (key LIKE 'totp_secret:%' OR key LIKE 'totp_pending_secret:%') AND value != ''`)
	if err != nil {
		return err
	}
	type plainKey struct{ k, v string }
	var plains []plainKey
	for rows.Next() {
		var p plainKey
		if err := rows.Scan(&p.k, &p.v); err != nil {
			rows.Close()
			return err
		}
		if !strings.HasPrefix(p.v, encPrefix) {
			plains = append(plains, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range plains {
		if err := s.SetSetting(p.k, s.seal(p.v)); err != nil {
			return err
		}
	}
	return nil
}
