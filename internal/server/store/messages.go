package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// messages 表：站内消息中心（告警触发/恢复、证书临期提醒、提醒事项推送、
// 巡检报告、登录提醒、新邮件到达均落此，类型 type 区分）。已读状态 read_at
// 为空即未读；ref_type/ref_id 关联来源（alert/reminder），提醒类消息渲染时
// 按关联事项当前完成态变红/复原。
// user_id 家庭成员数据隔离（2026-11-03）：0=全局消息（告警/巡检等监控域，
// 仅管理员可见），>0=该成员的个人消息（登录/提醒/邮件/用药/会员通知）。
// refs 合并消息的逐项引用 JSON（2026-11-03：med_logs/reminders 行 ID 数组），
// 供详情弹层逐项完成；单条消息仍走 ref_type/ref_id。
const messagesDDL = `CREATE TABLE IF NOT EXISTS messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  type       TEXT    NOT NULL DEFAULT 'alert',  -- alert | reminder | inspect | login | mail
  title      TEXT    NOT NULL,
  content    TEXT    NOT NULL DEFAULT '',
  ref_type   TEXT    NOT NULL DEFAULT '',
  ref_id     INTEGER NOT NULL DEFAULT 0,
  user_id    INTEGER NOT NULL DEFAULT 0,
  refs       TEXT    NOT NULL DEFAULT '',
  read_at    INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_unread  ON messages(read_at);`

// Message 是一条站内消息。
type Message struct {
	ID        int64   `json:"id"`
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Content   string  `json:"content"`
	RefType   string  `json:"ref_type"`
	RefID     int64   `json:"ref_id"`
	Refs      []int64 `json:"refs,omitempty"` // 合并消息逐项引用（详情弹层逐项完成用）
	UserID    int64   `json:"-"`              // 归属空间（0=全局/管理员，>0=家庭成员个人消息）
	ReadAt    *int64  `json:"read_at"`
	CreatedAt int64   `json:"created_at"`
	// RefDone 关联来源当前是否已办结（列表查询时联查回填）：提醒事项=未完成且
	// 不在回收站（未完成=前端红字+可点完成）；用药=剂量已打卡（taken/skipped，
	// pending/missed 视为未完成——消息中心保留「完成」按钮，漏服行可补卡）；
	// 来源行已删=视为已办结。
	RefDone bool `json:"ref_done"`
}

// CreateMessage 写入一条消息（UserID 0=全局；Refs 非空=合并消息逐项引用）。
func (s *Store) CreateMessage(m *Message) error {
	refs := ""
	if len(m.Refs) > 0 {
		if b, err := json.Marshal(m.Refs); err == nil {
			refs = string(b)
		}
	}
	_, err := s.db.Exec(`INSERT INTO messages (type, title, content, ref_type, ref_id, user_id, refs, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Type, m.Title, m.Content, m.RefType, m.RefID, m.UserID, refs, m.CreatedAt)
	if err == nil {
		s.notifyMessagesChanged(m.UserID)
	}
	return err
}

// GetMessageByID 按行 ID 取单条（合并消息逐项清单端点用），含归属空间校验数据。
// 未命中/跨空间（uid 不符且 uid!=0 不成立——调用方先按 scope 过滤）返回 (nil,nil)。
func (s *Store) GetMessageByID(uid, id int64) (*Message, error) {
	var m Message
	var refs string
	var readAt sql.NullInt64
	err := s.db.QueryRow(`SELECT id, type, title, content, ref_type, ref_id, user_id, refs, read_at, created_at
		FROM messages WHERE id = ? AND user_id = ?`, id, uid).
		Scan(&m.ID, &m.Type, &m.Title, &m.Content, &m.RefType, &m.RefID, &m.UserID, &refs, &readAt, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if readAt.Valid {
		m.ReadAt = &readAt.Int64
	}
	m.Refs = ParseRefs(refs)
	return &m, nil
}

// ParseRefs 解析合并消息 refs JSON（坏值/空返回 nil）。
func ParseRefs(raw string) []int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var arr []int64
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	return arr
}

// MessageItem 合并消息的逐项清单条目（详情弹层逐项完成用）。
type MessageItem struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Sub   string `json:"sub"`
	Done  bool   `json:"done"`
}

// MessageItems 合并消息逐项清单（type=medication→med_logs 剂量行；reminder→事项），
// 按 refs 顺序返回。非合并消息返回空。
func (s *Store) MessageItems(m *Message) ([]MessageItem, error) {
	if len(m.Refs) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(m.Refs))
	for _, id := range m.Refs {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	in := strings.Join(ids, ",")
	switch m.Type {
	case "medication":
		rows, err := s.db.Query(`SELECT l.id, p.name, m.name, l.time,
			COALESCE(l.status NOT IN ('pending', 'missed'), 1)
			FROM med_logs l
			JOIN medications m ON m.id = l.med_id
			JOIN med_persons p ON p.id = m.person_id
			WHERE l.id IN (` + in + `)`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		byID := map[int64]MessageItem{}
		for rows.Next() {
			var it MessageItem
			var person, med string
			var done int
			if err := rows.Scan(&it.ID, &person, &med, &it.Sub, &done); err != nil {
				return nil, err
			}
			it.Title = person + " · " + med
			it.Done = done == 1
			byID[it.ID] = it
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return orderItems(m.Refs, byID), nil
	case "reminder":
		rows, err := s.db.Query(`SELECT id, title, COALESCE(note, ''),
			COALESCE((done_at IS NOT NULL OR deleted_at != 0), 1)
			FROM reminders WHERE id IN (` + in + `)`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		byID := map[int64]MessageItem{}
		for rows.Next() {
			var it MessageItem
			var done int
			if err := rows.Scan(&it.ID, &it.Title, &it.Sub, &done); err != nil {
				return nil, err
			}
			it.Done = done == 1
			byID[it.ID] = it
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return orderItems(m.Refs, byID), nil
	}
	return nil, nil
}

// orderItems 按 refs 顺序重排（SQL IN 不保序）。
func orderItems(refs []int64, byID map[int64]MessageItem) []MessageItem {
	out := make([]MessageItem, 0, len(refs))
	for _, id := range refs {
		if it, ok := byID[id]; ok {
			out = append(out, it)
		}
	}
	return out
}

// MessagePage 是消息列表的一页（含未读总数与筛选后总数，供分页）。
type MessagePage struct {
	Items  []Message `json:"items"`
	Total  int       `json:"total"`  // 当前筛选条件下的总条数
	Unread int       `json:"unread"` // 全量未读数（角标用，不受筛选影响）
}

// ListMessages 分页查询本空间（uid）消息。msgType/unreadOnly 为筛选（空值/
// false = 不筛）；提醒类联查 reminders 回填 RefDone（未完成且不在回收站才红字；
// reminders 表不存在时视为已完成，触发顺序保证建表先于首条提醒消息）。
// uid=0 管理员空间（含告警/巡检等全局消息），>0 成员个人空间（2026-11-03）。
func (s *Store) ListMessages(uid int64, msgType string, unreadOnly bool, page, pageSize int) (*MessagePage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	where := "WHERE user_id = ?"
	args := []any{uid}
	if msgType != "" {
		where += " AND type = ?"
		args = append(args, msgType)
	}
	if unreadOnly {
		where += " AND read_at IS NULL"
	}

	// total（按筛选）与 unread（角标口径，不吃筛选）一条语句一次往返
	var total, unread int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM messages `+where+`),
		(SELECT COUNT(*) FROM messages WHERE user_id = ? AND read_at IS NULL)`,
		append(args, uid)...).Scan(&total, &unread); err != nil {
		return nil, err
	}

	q := `SELECT m.id, m.type, m.title, m.content, m.ref_type, m.ref_id, m.user_id, m.refs, m.read_at, m.created_at,
		CASE WHEN m.type = 'reminder' AND m.ref_id > 0 THEN
			COALESCE((SELECT done_at IS NULL AND deleted_at = 0 FROM reminders WHERE id = m.ref_id), 0)
		WHEN m.type = 'medication' AND m.ref_id > 0 THEN
			COALESCE((SELECT status NOT IN ('pending', 'missed') FROM med_logs WHERE id = m.ref_id), 1)
		WHEN m.type = 'reminder' AND COALESCE(m.refs, '') != '' THEN
			NOT EXISTS (SELECT 1 FROM json_each(m.refs) je JOIN reminders r ON r.id = je.value
				WHERE r.done_at IS NULL AND r.deleted_at = 0)
		WHEN m.type = 'medication' AND COALESCE(m.refs, '') != '' THEN
			NOT EXISTS (SELECT 1 FROM json_each(m.refs) je JOIN med_logs ml ON ml.id = je.value
				WHERE ml.status IN ('pending', 'missed'))
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
		var refs string
		if err := rows.Scan(&m.ID, &m.Type, &m.Title, &m.Content, &m.RefType, &m.RefID, &m.UserID, &refs, &m.ReadAt, &m.CreatedAt, &refDone); err != nil {
			return nil, err
		}
		m.Refs = ParseRefs(refs)
		m.RefDone = refDone == 1
		p.Items = append(p.Items, m)
	}
	return p, rows.Err()
}

// UnreadCount 本空间（uid）未读消息数（顶栏角标轮询）。
func (s *Store) UnreadCount(uid int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE user_id = ? AND read_at IS NULL`, uid).Scan(&n)
	return n, err
}

// MarkMessageRead 单条标已读（仅本空间内的消息，跨空间等于不存在）。
func (s *Store) MarkMessageRead(uid, id int64) error {
	_, err := s.db.Exec(`UPDATE messages SET read_at = strftime('%s','now') WHERE user_id = ? AND id = ? AND read_at IS NULL`, uid, id)
	if err == nil {
		s.notifyMessagesChanged(uid)
	}
	return err
}

// MarkAllMessagesRead 本空间全部标已读，返回标记条数。
func (s *Store) MarkAllMessagesRead(uid int64) (int64, error) {
	res, err := s.db.Exec(`UPDATE messages SET read_at = strftime('%s','now') WHERE user_id = ? AND read_at IS NULL`, uid)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.notifyMessagesChanged(uid)
	}
	return res.RowsAffected()
}

// MarkMessagesReadByType 本空间按类型标已读（452：进邮件/待办事项页自动清对应
// 未读提醒——mail=邮件提醒、reminder=待办提醒），返回标记条数。
func (s *Store) MarkMessagesReadByType(uid int64, msgType string) (int64, error) {
	res, err := s.db.Exec(`UPDATE messages SET read_at = strftime('%s','now') WHERE user_id = ? AND type = ? AND read_at IS NULL`, uid, msgType)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.notifyMessagesChanged(uid)
	}
	return res.RowsAffected()
}

// DeleteMessage 删除单条消息（物理删除；仅本空间内的消息，跨空间等于不存在）。
func (s *Store) DeleteMessage(uid, id int64) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE user_id = ? AND id = ?`, uid, id)
	if err == nil {
		s.notifyMessagesChanged(uid)
	}
	return err
}

// ClearMessages 清空本空间全部消息（物理删除），返回删除条数。
func (s *Store) ClearMessages(uid int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM messages WHERE user_id = ?`, uid)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.notifyMessagesChanged(uid)
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
