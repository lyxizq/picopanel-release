package store

// messages 表：站内消息中心（告警触发/恢复、证书临期提醒、提醒事项推送、
// 巡检报告、登录提醒、新邮件到达均落此，类型 type 区分）。已读状态 read_at
// 为空即未读；ref_type/ref_id 关联来源（alert/reminder），提醒类消息渲染时
// 按关联事项当前完成态变红/复原。
const messagesDDL = `CREATE TABLE IF NOT EXISTS messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  type       TEXT    NOT NULL DEFAULT 'alert',  -- alert | reminder | inspect | login | mail
  title      TEXT    NOT NULL,
  content    TEXT    NOT NULL DEFAULT '',
  ref_type   TEXT    NOT NULL DEFAULT '',
  ref_id     INTEGER NOT NULL DEFAULT 0,
  read_at    INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_unread  ON messages(read_at);`

// Message 是一条站内消息。
type Message struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	RefType   string `json:"ref_type"`
	RefID     int64  `json:"ref_id"`
	ReadAt    *int64 `json:"read_at"`
	CreatedAt int64  `json:"created_at"`
	// RefDone 提醒类消息的关联事项当前是否已完成（列表查询时联查回填，
	// 未完成 = 前端红色字体）；非提醒类恒 true。
	RefDone bool `json:"ref_done"`
}

// CreateMessage 写入一条消息。
func (s *Store) CreateMessage(m *Message) error {
	_, err := s.db.Exec(`INSERT INTO messages (type, title, content, ref_type, ref_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		m.Type, m.Title, m.Content, m.RefType, m.RefID, m.CreatedAt)
	return err
}

// MessagePage 是消息列表的一页（含未读总数与筛选后总数，供分页）。
type MessagePage struct {
	Items  []Message `json:"items"`
	Total  int       `json:"total"`  // 当前筛选条件下的总条数
	Unread int       `json:"unread"` // 全量未读数（角标用，不受筛选影响）
}

// ListMessages 分页查询消息。msgType/unreadOnly 为筛选（空值/false = 不筛）；
// 提醒类联查 reminders 回填 RefDone（未完成且不在回收站才红字；reminders 表
// 不存在时视为已完成，触发顺序保证建表先于首条提醒消息）。
func (s *Store) ListMessages(msgType string, unreadOnly bool, page, pageSize int) (*MessagePage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	where := "WHERE 1=1"
	args := []any{}
	if msgType != "" {
		where += " AND type = ?"
		args = append(args, msgType)
	}
	if unreadOnly {
		where += " AND read_at IS NULL"
	}

	// total（按筛选）与 unread（全局角标口径，不吃筛选）一条语句一次往返
	var total, unread int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM messages `+where+`),
		(SELECT COUNT(*) FROM messages WHERE read_at IS NULL)`, args...).Scan(&total, &unread); err != nil {
		return nil, err
	}

	q := `SELECT m.id, m.type, m.title, m.content, m.ref_type, m.ref_id, m.read_at, m.created_at,
		CASE WHEN m.type = 'reminder' AND m.ref_id > 0 THEN
			COALESCE((SELECT done_at IS NULL AND deleted_at = 0 FROM reminders WHERE id = m.ref_id), 0)
		ELSE 1 END
		FROM messages m ` + where + ` ORDER BY m.id DESC LIMIT ? OFFSET ?`
	rows, err := s.db.Query(q, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p := &MessagePage{Items: []Message{}, Total: total, Unread: unread}
	for rows.Next() {
		var m Message
		var refDone int
		if err := rows.Scan(&m.ID, &m.Type, &m.Title, &m.Content, &m.RefType, &m.RefID, &m.ReadAt, &m.CreatedAt, &refDone); err != nil {
			return nil, err
		}
		m.RefDone = refDone == 1
		p.Items = append(p.Items, m)
	}
	return p, rows.Err()
}

// UnreadCount 未读消息数（顶栏角标轮询）。
func (s *Store) UnreadCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE read_at IS NULL`).Scan(&n)
	return n, err
}

// MarkMessageRead 单条标已读。
func (s *Store) MarkMessageRead(id int64) error {
	_, err := s.db.Exec(`UPDATE messages SET read_at = strftime('%s','now') WHERE id = ? AND read_at IS NULL`, id)
	return err
}

// MarkAllMessagesRead 全部标已读，返回标记条数。
func (s *Store) MarkAllMessagesRead() (int64, error) {
	res, err := s.db.Exec(`UPDATE messages SET read_at = strftime('%s','now') WHERE read_at IS NULL`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkMessagesReadByType 按类型标已读（452：进邮件/待办事项页自动清对应
// 未读提醒——mail=邮件提醒、reminder=待办提醒），返回标记条数。
func (s *Store) MarkMessagesReadByType(msgType string) (int64, error) {
	res, err := s.db.Exec(`UPDATE messages SET read_at = strftime('%s','now') WHERE type = ? AND read_at IS NULL`, msgType)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteMessage 删除单条消息（物理删除）。
func (s *Store) DeleteMessage(id int64) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	return err
}

// ClearMessages 清空全部消息（物理删除），返回删除条数。
func (s *Store) ClearMessages() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM messages`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteMessagesBefore 清理 N 天前的消息（保留期外的历史提醒/告警消息），
// 返回删除行数；供数据保留清理循环复用。
func (s *Store) DeleteMessagesBefore(ts int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM messages WHERE created_at < ?`, ts)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
