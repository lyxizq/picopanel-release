package store

// push_subscriptions 表：Web Push 浏览器订阅（endpoint 唯一，同一浏览器重复
// 订阅覆盖更新）。推送失败返回 404/410 时由 webpush 包删除对应行。
const pushSubsDDL = `CREATE TABLE IF NOT EXISTS push_subscriptions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  endpoint   TEXT    NOT NULL,
  p256dh     TEXT    NOT NULL,
  auth       TEXT    NOT NULL,
  ua         TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  UNIQUE(endpoint)
);`

// PushSub 是一条 Web Push 订阅。
type PushSub struct {
	ID        int64  `json:"id"`
	Endpoint  string `json:"endpoint"`
	P256DH    string `json:"p256dh"`
	Auth      string `json:"auth"`
	UA        string `json:"ua"`
	CreatedAt int64  `json:"created_at"`
}

// UpsertPushSub 按 endpoint 覆盖写入订阅（重复订阅同一浏览器 = 更新密钥）。
func (s *Store) UpsertPushSub(sub *PushSub) error {
	_, err := s.db.Exec(`INSERT INTO push_subscriptions (endpoint, p256dh, auth, ua, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth,
			ua = excluded.ua, created_at = excluded.created_at`,
		sub.Endpoint, sub.P256DH, sub.Auth, sub.UA, sub.CreatedAt)
	return err
}

// ListPushSubs 全部订阅（推送时遍历）。
func (s *Store) ListPushSubs() ([]PushSub, error) {
	rows, err := s.db.Query(`SELECT id, endpoint, p256dh, auth, ua, created_at FROM push_subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSub
	for rows.Next() {
		var sub PushSub
		if err := rows.Scan(&sub.ID, &sub.Endpoint, &sub.P256DH, &sub.Auth, &sub.UA, &sub.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// CountPushSubs 订阅数（设置页展示）。
func (s *Store) CountPushSubs() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions`).Scan(&n)
	return n, err
}

// DeletePushSub 按 endpoint 删除（退订 / 推送失败 404/410 清理）。
func (s *Store) DeletePushSub(endpoint string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// DeletePushSubByID 按行 ID 删除（设置页订阅列表的删除按钮；删后该浏览器可重新订阅）。
func (s *Store) DeletePushSubByID(id int64) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE id = ?`, id)
	return err
}
