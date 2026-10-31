package store

import (
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
  kind        TEXT    NOT NULL DEFAULT 'once',  -- once/yearly/since
  lunar       INTEGER NOT NULL DEFAULT 0,
  date        TEXT    NOT NULL,                 -- YYYY-MM-DD（公历锚点）
  lunar_month INTEGER NOT NULL DEFAULT 0,       -- lunar=1 时冗余：农历月（1-12，正数）
  lunar_day   INTEGER NOT NULL DEFAULT 0,       -- lunar=1 时冗余：农历日（1-30）
  color       TEXT    NOT NULL DEFAULT '',
  sort        INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  UNIQUE(name)
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
	// 展示用现算
	NextDate   string `json:"next_date"`   // 下一个发生日 YYYY-MM-DD（once 已过=原日期）
	Days       int    `json:"days"`        // once/yearly=距下一发生日天数（今天=0，已过=负）；since=已累计天数
	LunarLabel string `json:"lunar_label"` // lunar=1 时「农历九月初七」样式，否则空
}

// ListCountdowns 全量按 sort、id 序。
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

// CreateCountdown 新增（名称全局唯一；上限 100 条）。
func (s *Store) CreateCountdown(c *Countdown) (int64, error) {
	n, err := s.CountCountdowns()
	if err != nil {
		return 0, err
	}
	if n >= maxCountdowns {
		return 0, fmt.Errorf("倒数日最多 %d 条", maxCountdowns)
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO countdowns (name, kind, lunar, date, lunar_month, lunar_day, color, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, normalizeCountdownKind(c.Kind), boolInt(c.Lunar), c.Date, c.LunarMonth, c.LunarDay,
		c.Color, c.Sort, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateCountdown 编辑（名称唯一冲突原样上抛）。
func (s *Store) UpdateCountdown(c *Countdown) error {
	_, err := s.db.Exec(`UPDATE countdowns SET name = ?, kind = ?, lunar = ?, date = ?, lunar_month = ?,
		lunar_day = ?, color = ?, sort = ?, updated_at = ? WHERE id = ?`,
		c.Name, normalizeCountdownKind(c.Kind), boolInt(c.Lunar), c.Date, c.LunarMonth, c.LunarDay,
		c.Color, c.Sort, time.Now().Unix(), c.ID)
	return err
}

// DeleteCountdown 删除（不存在时静默成功，同 DeleteSetting 口径）。
func (s *Store) DeleteCountdown(id int64) error {
	_, err := s.db.Exec(`DELETE FROM countdowns WHERE id = ?`, id)
	return err
}

// CountCountdowns 当前条数。
func (s *Store) CountCountdowns() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM countdowns`).Scan(&n)
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
