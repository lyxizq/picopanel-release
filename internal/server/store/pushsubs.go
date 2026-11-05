package store

// push_subscriptions 表：Web Push 浏览器订阅（endpoint 唯一，同一浏览器重复
// 订阅覆盖更新）。推送失败返回 404/410 时由 webpush 包删除对应行。
// user_id：订阅所属账号（2026-11-03 家庭成员口径，0=管理员）——个人定向推送
// 只发本人的订阅，全局推送发全部。
const pushSubsDDL = `CREATE TABLE IF NOT EXISTS push_subscriptions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  endpoint   TEXT    NOT NULL,
  p256dh     TEXT    NOT NULL,
  auth       TEXT    NOT NULL,
  user_id    INTEGER NOT NULL DEFAULT 0,
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
	UserID    int64  `json:"-"`
	UA        string `json:"ua"`
	CreatedAt int64  `json:"created_at"`
}

// UpsertPushSub 按 endpoint 覆盖写入订阅（重复订阅同一浏览器 = 更新密钥）。
func (s *Store) UpsertPushSub(sub *PushSub) error {
	_, err := s.db.Exec(`INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_id, ua, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth,
			user_id = excluded.user_id, ua = excluded.ua, created_at = excluded.created_at`,
		sub.Endpoint, sub.P256DH, sub.Auth, sub.UserID, sub.UA, sub.CreatedAt)
	return err
}

// ListPushSubs 全部订阅（全局推送时遍历）。
func (s *Store) ListPushSubs() ([]PushSub, error) {
	rows, err := s.db.Query(`SELECT id, endpoint, p256dh, auth, user_id, ua, created_at FROM push_subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSub
	for rows.Next() {
		var sub PushSub
		if err := rows.Scan(&sub.ID, &sub.Endpoint, &sub.P256DH, &sub.Auth, &sub.UserID, &sub.UA, &sub.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// ListPushSubsFor 指定账号的订阅（个人定向推送遍历；0=管理员订阅）。
func (s *Store) ListPushSubsFor(userID int64) ([]PushSub, error) {
	rows, err := s.db.Query(`SELECT id, endpoint, p256dh, auth, user_id, ua, created_at FROM push_subscriptions WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSub
	for rows.Next() {
		var sub PushSub
		if err := rows.Scan(&sub.ID, &sub.Endpoint, &sub.P256DH, &sub.Auth, &sub.UserID, &sub.UA, &sub.CreatedAt); err != nil {
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

// DeletePushSubFor 按空间限定删除（2026-11-03 复查补：成员只能删自己的订阅，
// admin（uid=0）不过滤可删任意）。
func (s *Store) DeletePushSubFor(uid int64, endpoint string) error {
	if uid == 0 {
		return s.DeletePushSub(endpoint)
	}
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ? AND user_id = ?`, endpoint, uid)
	return err
}

// DeletePushSubByIDFor 同上按行 ID（成员删非本人订阅 = 无影响零行，不泄露存在性）。
func (s *Store) DeletePushSubByIDFor(uid, id int64) error {
	if uid == 0 {
		return s.DeletePushSubByID(id)
	}
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE id = ? AND user_id = ?`, id, uid)
	return err
}
