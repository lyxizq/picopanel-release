package store

import (
	"database/sql"
	"fmt"
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
