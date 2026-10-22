package store

import (
	"database/sql"
	"strconv"
)

// settings 表：Web「参数设置」页的运行期参数（key-value），修改即时生效，无需重启。
const settingRetentionDays = "retention_days"

// GetSetting 读取一个参数；不存在返回 ("", false, nil)。
func (s *Store) GetSetting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting 写入（upsert）一个参数。
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// DeleteSetting 删除一个参数（不存在时静默成功）。
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// GetSettingSealed 读加密参数（seal 口径同 net_tasks URL，mihomo secret 等敏感
// 配置）。无则 ("", false, nil)；库内意外出现明文（历史残留）按原样返回。
func (s *Store) GetSettingSealed(key string) (string, bool, error) {
	v, ok, err := s.GetSetting(key)
	if err != nil || !ok || v == "" {
		return v, ok, err
	}
	return s.unseal(v), true, nil
}

// SetSettingSealed 写加密参数（seal 后落库；空值原样存=清除语义）。
func (s *Store) SetSettingSealed(key, value string) error {
	if value == "" {
		return s.SetSetting(key, "")
	}
	return s.SetSetting(key, s.seal(value))
}

// RetentionDays 数据保留天数：缺省 90，值非法（非数字/小于 1）同样回退 90。
// 每次清理前都重新读取，参数页修改后下一轮自动生效。
func (s *Store) RetentionDays() (int, error) {
	v, ok, err := s.GetSetting(settingRetentionDays)
	if err != nil || !ok {
		return 90, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 90, nil
	}
	return n, nil
}

// SetRetentionDays 设置数据保留天数（API 层已校验 1~3650）。
func (s *Store) SetRetentionDays(days int) error {
	return s.SetSetting(settingRetentionDays, strconv.Itoa(days))
}
