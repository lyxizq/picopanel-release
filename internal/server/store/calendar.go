package store

import (
	"database/sql"
	"fmt"
	"time"
)

// 日历（个人助理批）：外部 ICS 订阅表 + 展开后的事件表。
// calendar_subs 一行一个订阅源（URL 由管理员填写）；calendar_events 存该订阅
// 在窗口内展开后的事件（含重复规则展开的每次发生），每次刷新整订阅替换，
// 面板自有数据（提醒/会员/倒数日/追剧/证书）不进这张表、查询时现算合并。
const calendarSubsDDL = `CREATE TABLE IF NOT EXISTS calendar_subs (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT    NOT NULL,
  url           TEXT    NOT NULL,
  color         TEXT    NOT NULL DEFAULT '',
  enabled       INTEGER NOT NULL DEFAULT 1,
  last_fetch_at INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT    NOT NULL DEFAULT '',
  event_count   INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);`

const calendarEventsDDL = `CREATE TABLE IF NOT EXISTS calendar_events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  sub_id      INTEGER NOT NULL,
  uid         TEXT    NOT NULL DEFAULT '',
  title       TEXT    NOT NULL DEFAULT '',
  start_at    INTEGER NOT NULL,
  end_at      INTEGER NOT NULL DEFAULT 0,
  all_day     INTEGER NOT NULL DEFAULT 0,
  location    TEXT    NOT NULL DEFAULT '',
  description TEXT    NOT NULL DEFAULT ''
);`

const calendarEventsIdxDDL = `CREATE INDEX IF NOT EXISTS idx_calendar_events_start ON calendar_events (start_at);`

const maxCalendarSubs = 20

// CalendarSub 订阅源一行。
type CalendarSub struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Color       string `json:"color"`
	Enabled     bool   `json:"enabled"`
	LastFetchAt int64  `json:"last_fetch_at"`
	LastError   string `json:"last_error"`
	EventCount  int    `json:"event_count"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// CalendarEvent 展开后的一次事件发生（all_day 时 start_at/end_at 为本地 0 点，
// end_at 按 iCalendar 口径为不含端点的次日 0 点）。
type CalendarEvent struct {
	ID          int64  `json:"id"`
	SubID       int64  `json:"sub_id"`
	UID         string `json:"uid"`
	Title       string `json:"title"`
	StartAt     int64  `json:"start_at"`
	EndAt       int64  `json:"end_at"`
	AllDay      bool   `json:"all_day"`
	Location    string `json:"location"`
	Description string `json:"description"`
}

func scanCalendarSub(sc interface{ Scan(...any) error }) (CalendarSub, error) {
	var c CalendarSub
	var en int
	err := sc.Scan(&c.ID, &c.Name, &c.URL, &c.Color, &en, &c.LastFetchAt, &c.LastError, &c.EventCount, &c.CreatedAt, &c.UpdatedAt)
	c.Enabled = en != 0
	return c, err
}

const calendarSubCols = `id, name, url, color, enabled, last_fetch_at, last_error, event_count, created_at, updated_at`

// ListCalendarSubs 全部订阅按 id 序。
func (s *Store) ListCalendarSubs() ([]CalendarSub, error) {
	rows, err := s.db.Query(`SELECT ` + calendarSubCols + ` FROM calendar_subs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarSub
	for rows.Next() {
		c, err := scanCalendarSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCalendarSub 单条；不存在返回 (nil, nil)。
func (s *Store) GetCalendarSub(id int64) (*CalendarSub, error) {
	c, err := scanCalendarSub(s.db.QueryRow(`SELECT `+calendarSubCols+` FROM calendar_subs WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CreateCalendarSub 新增订阅（上限 20）。
func (s *Store) CreateCalendarSub(c *CalendarSub) (int64, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM calendar_subs`).Scan(&n); err != nil {
		return 0, err
	}
	if n >= maxCalendarSubs {
		return 0, fmt.Errorf("日历订阅最多 %d 个", maxCalendarSubs)
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO calendar_subs (name, url, color, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, c.Name, c.URL, c.Color, boolInt(c.Enabled), now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateCalendarSub 编辑名称/地址/颜色/启停（抓取状态列不动）。
func (s *Store) UpdateCalendarSub(c *CalendarSub) error {
	_, err := s.db.Exec(`UPDATE calendar_subs SET name = ?, url = ?, color = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		c.Name, c.URL, c.Color, boolInt(c.Enabled), time.Now().Unix(), c.ID)
	return err
}

// DeleteCalendarSub 删除订阅及其全部事件。
func (s *Store) DeleteCalendarSub(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM calendar_events WHERE sub_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM calendar_subs WHERE id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ReplaceCalendarEvents 一次成功抓取后整订阅替换事件并记录抓取状态（事务）。
func (s *Store) ReplaceCalendarEvents(subID int64, events []CalendarEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM calendar_events WHERE sub_id = ?`, subID); err != nil {
		tx.Rollback()
		return err
	}
	for _, e := range events {
		if _, err := tx.Exec(`INSERT INTO calendar_events (sub_id, uid, title, start_at, end_at, all_day, location, description)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, subID, e.UID, e.Title, e.StartAt, e.EndAt, boolInt(e.AllDay), e.Location, e.Description); err != nil {
			tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE calendar_subs SET last_fetch_at = ?, last_error = '', event_count = ? WHERE id = ?`,
		time.Now().Unix(), len(events), subID); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// MarkCalendarSubError 抓取失败只记错误与时间，已有事件保留（宁旧勿空）。
func (s *Store) MarkCalendarSubError(subID int64, msg string) error {
	_, err := s.db.Exec(`UPDATE calendar_subs SET last_fetch_at = ?, last_error = ? WHERE id = ?`,
		time.Now().Unix(), msg, subID)
	return err
}

// CalendarEventsBetween 启用订阅在 [from, to) 内有交集的事件（按开始时间序）。
func (s *Store) CalendarEventsBetween(from, to int64) ([]CalendarEvent, error) {
	rows, err := s.db.Query(`SELECT e.id, e.sub_id, e.uid, e.title, e.start_at, e.end_at, e.all_day, e.location, e.description
		FROM calendar_events e JOIN calendar_subs s ON s.id = e.sub_id
		WHERE s.enabled = 1 AND e.start_at < ? AND (CASE WHEN e.end_at > e.start_at THEN e.end_at ELSE e.start_at + 1 END) > ?
		ORDER BY e.start_at, e.id`, to, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarEvent
	for rows.Next() {
		var e CalendarEvent
		var ad int
		if err := rows.Scan(&e.ID, &e.SubID, &e.UID, &e.Title, &e.StartAt, &e.EndAt, &ad, &e.Location, &e.Description); err != nil {
			return nil, err
		}
		e.AllDay = ad != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// RemindersDueBetween 到期日在 [from, to]（YYYY-MM-DD 闭区间）内、未进回收站的事项
// （含已完成，调用方按 DoneAt 判定展示样式）。
func (s *Store) RemindersDueBetween(from, to string) ([]Reminder, error) {
	rows, err := s.db.Query(`SELECT r.id, r.group_id, r.parent_id, r.notify, r.title, r.note, r.due_date, r.due_time,
		r.important, r.done_at, COALESCE(g.name, '')
		FROM reminders r LEFT JOIN reminder_groups g ON g.id = r.group_id
		WHERE r.deleted_at = 0 AND r.due_date != '' AND r.due_date >= ? AND r.due_date <= ?
		ORDER BY r.due_date, r.due_time, r.id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reminder
	for rows.Next() {
		var r Reminder
		var notify, important int
		if err := rows.Scan(&r.ID, &r.GroupID, &r.ParentID, &notify, &r.Title, &r.Note, &r.DueDate, &r.DueTime,
			&important, &r.DoneAt, &r.GroupName); err != nil {
			return nil, err
		}
		r.Notify, r.Important = notify != 0, important != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// BiliItemDay B 站追更条目的日历视图（发布日 + 标题 + up 名）。
type BiliItemDay struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	UpName  string `json:"up_name"`
	Pubdate int64  `json:"pubdate"`
}

// BiliItemsBetween 发布时间在 [from, to) 的追更条目（按发布时间序）。
func (s *Store) BiliItemsBetween(from, to int64) ([]BiliItemDay, error) {
	rows, err := s.db.Query(`SELECT i.id, i.title, COALESCE(f.up_name, ''), i.pubdate
		FROM bili_items i LEFT JOIN bili_follows f ON f.id = i.follow_id
		WHERE i.pubdate >= ? AND i.pubdate < ? ORDER BY i.pubdate, i.id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BiliItemDay
	for rows.Next() {
		var b BiliItemDay
		if err := rows.Scan(&b.ID, &b.Title, &b.UpName, &b.Pubdate); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
