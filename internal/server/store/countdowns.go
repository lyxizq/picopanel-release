package store

import (
	"database/sql"
	"fmt"
	"time"
)

// countdowns 表：倒数日 / 纪念日（个人助理批）。kind 三种口径——
// once=一次性倒计时（到某天还剩 N 天，可负=已过）；yearly=每年纪念日
// （公历或农历重复，农历换算走 assistant 包的农历工具）；since=从某天起
// 累计天数（已 N 天）。lunar=1 时 date 列存的是创建时的公历锚点，
// 农历月/日据此反推并冗余存储（lunar_month/lunar_day），避免每次换算漂移。
const countdownsDDL = `CREATE TABLE IF NOT EXISTS countdowns (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL,
  user_id     INTEGER NOT NULL DEFAULT 0,      -- 家庭成员数据隔离（2026-11-03：0=管理员空间）
  kind        TEXT    NOT NULL DEFAULT 'once',  -- once/yearly/since
  lunar       INTEGER NOT NULL DEFAULT 0,
  date        TEXT    NOT NULL,                 -- YYYY-MM-DD（公历锚点）
  lunar_month INTEGER NOT NULL DEFAULT 0,       -- lunar=1 时冗余：农历月（1-12，正数）
  lunar_day   INTEGER NOT NULL DEFAULT 0,       -- lunar=1 时冗余：农历日（1-30）
  color       TEXT    NOT NULL DEFAULT '',
  sort        INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  UNIQUE(user_id, name)
);`

const maxCountdowns = 100

// Countdown 倒数日一行。NextDate/Days/LunarLabel 为展示用现算字段
// （assistant 包填充；store 层只落库不换算）。
type Countdown struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // once/yearly/since
	Lunar      bool   `json:"lunar"`
	Date       string `json:"date"` // YYYY-MM-DD
	LunarMonth int    `json:"-"`
	LunarDay   int    `json:"-"`
	Color      string `json:"color"`
	Sort       int64  `json:"sort"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	// UserID 归属空间（2026-11-03 家庭成员口径：0=管理员空间，>0=成员个人空间，
	// 互不可见）。json 不外露；Create/Update 落库时以该字段打空间。
	UserID int64 `json:"-"`
	// 展示用现算
	NextDate   string `json:"next_date"`   // 下一个发生日 YYYY-MM-DD（once 已过=原日期）
	Days       int    `json:"days"`        // once/yearly=距下一发生日天数（今天=0，已过=负）；since=已累计天数
	LunarLabel string `json:"lunar_label"` // lunar=1 时「农历九月初七」样式，否则空
}

// ListCountdowns 全量按 sort、id 序（不隔离：早报/日历/ICS 订阅等全局产物的
// 聚合取数口径）。按会话空间过滤用 ListCountdownsFor。
func (s *Store) ListCountdowns() ([]Countdown, error) {
	rows, err := s.db.Query(`SELECT id, name, kind, lunar, date, lunar_month, lunar_day, color, sort,
		created_at, updated_at FROM countdowns ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Countdown
	for rows.Next() {
		var c Countdown
		var lunar int
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &lunar, &c.Date, &c.LunarMonth, &c.LunarDay,
			&c.Color, &c.Sort, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Lunar = lunar != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListCountdownsFor 某空间的倒数日（uid=0 管理员空间），按 sort、id 序；
// 接口层取数口径，严格个人空间互不可见（2026-11-03 家庭成员口径）。
func (s *Store) ListCountdownsFor(uid int64) ([]Countdown, error) {
	rows, err := s.db.Query(`SELECT id, name, kind, lunar, date, lunar_month, lunar_day, color, sort,
		created_at, updated_at FROM countdowns WHERE user_id = ? ORDER BY sort, id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCountdowns(rows)
}

// scanCountdowns List/ListCountdownsFor 共用的行扫描（列序一致）。
func scanCountdowns(rows *sql.Rows) ([]Countdown, error) {
	var out []Countdown
	for rows.Next() {
		var c Countdown
		var lunar int
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &lunar, &c.Date, &c.LunarMonth, &c.LunarDay,
			&c.Color, &c.Sort, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Lunar = lunar != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCountdown 新增到 c.UserID 空间（名称空间内唯一；上限 100 条按空间各算各的）。
func (s *Store) CreateCountdown(c *Countdown) (int64, error) {
	n, err := s.CountCountdowns(c.UserID)
	if err != nil {
		return 0, err
	}
	if n >= maxCountdowns {
		return 0, fmt.Errorf("倒数日最多 %d 条", maxCountdowns)
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO countdowns (name, user_id, kind, lunar, date, lunar_month, lunar_day, color, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.UserID, normalizeCountdownKind(c.Kind), boolInt(c.Lunar), c.Date, c.LunarMonth, c.LunarDay,
		c.Color, c.Sort, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateCountdown 编辑（名称唯一冲突原样上抛）；跨空间/不存在返回 sql.ErrNoRows。
func (s *Store) UpdateCountdown(c *Countdown) error {
	res, err := s.db.Exec(`UPDATE countdowns SET name = ?, kind = ?, lunar = ?, date = ?, lunar_month = ?,
		lunar_day = ?, color = ?, sort = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		c.Name, normalizeCountdownKind(c.Kind), boolInt(c.Lunar), c.Date, c.LunarMonth, c.LunarDay,
		c.Color, c.Sort, time.Now().Unix(), c.ID, c.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteCountdown 删除（不存在/跨空间时静默成功，同 DeleteSetting 口径，不泄露存在性）。
func (s *Store) DeleteCountdown(id, uid int64) error {
	_, err := s.db.Exec(`DELETE FROM countdowns WHERE id = ? AND user_id = ?`, id, uid)
	return err
}

// CountCountdowns 某空间当前条数。
func (s *Store) CountCountdowns(uid int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM countdowns WHERE user_id = ?`, uid).Scan(&n)
	return n, err
}

func normalizeCountdownKind(k string) string {
	switch k {
	case "yearly", "since":
		return k
	default:
		return "once"
	}
}
