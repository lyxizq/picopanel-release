package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// memberships 表：会员到期提醒（492）。名称 + 会员名 + 起止日期自由记录，
// 到期前 15 天由提醒调度推一次（notify_15d_at 已发标记，改到期日自动重置）；
// 进度条=剩余占比：有 start_date 按 开始→到期 区间算，留空回落 剩余/period_days
// （period_days 保留作回落基准，默认 365）。sort 为拖拽排序（drop 即存 1..n；
// 0=未排过按到期日序兜底，迁移回填一次后新建行都带 max+1 追加尾部）。
const membershipsDDL = `CREATE TABLE IF NOT EXISTS memberships (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  biz           TEXT    NOT NULL,
  name          TEXT    NOT NULL DEFAULT '',
  start_date    TEXT    NOT NULL DEFAULT '',
  expire_date   TEXT    NOT NULL,
  period_days   INTEGER NOT NULL DEFAULT 365,
  notify_15d_at INTEGER,
  sort          INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memberships_expire ON memberships(expire_date);`

// Membership 一条会员到期记录。RemainingDays/Expired/Progress 为查询时按本地
// 时区现算的展示字段（RemainingDays 负数 = 已过期天数；Progress 剩余占比 0~100）。
type Membership struct {
	ID          int64  `json:"id"`
	Biz         string `json:"biz"`
	Name        string `json:"name"`
	StartDate   string `json:"start_date"`  // YYYY-MM-DD，空 = 进度条回落剩余/周期
	ExpireDate  string `json:"expire_date"` // YYYY-MM-DD
	PeriodDays  int    `json:"period_days"`
	Notify15dAt *int64 `json:"notify_15d_at"`
	Sort        int64  `json:"sort"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	// 展示用现算字段
	RemainingDays int  `json:"remaining_days"`
	Expired       bool `json:"expired"`
	Progress      int  `json:"progress"`
}

// membershipPeriodBounds 周期天数合法区间（进度条基准，上限 10 年）。
const (
	membershipPeriodDefault = 365
	membershipPeriodMax     = 3650
)

// ListMemberships 全部会员记录（拖拽排序序（sort）优先，未排过的按创建序；
// 存量已回填为到期日序）。
func (s *Store) ListMemberships() ([]Membership, error) {
	rows, err := s.db.Query(membershipCols + ` FROM memberships ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		m, err := scanMembership(func(dst ...any) error { return rows.Scan(dst...) })
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// membershipCols 查询列清单（List/Get/Due 三处共用，列序与 scanMembership 对齐）。
const membershipCols = `SELECT id, biz, name, start_date, expire_date, period_days, notify_15d_at, sort, created_at, updated_at`

// BackfillMembershipSort 存量回填（幂等，只命中 sort=0 的行）：按到期日+id 顺序
// 编 1..n。计数不能带 m2.sort=0 谓词——自引用 UPDATE 逐行生效，已更新的行会脱离
// 谓词导致计数缩水、序号打结（2026-10-08 实测）；全表计数在本回填的唯一场景
// （启动迁移，存量行全为 0）下即到期日稠密序。拖过序的行 sort≠0 永不重排；
// 新建行走 max+1 也不会落 0，回填天然收敛。
func BackfillMembershipSort(db *sql.DB) error {
	_, err := db.Exec(`UPDATE memberships SET sort = (
		SELECT COUNT(*) + 1 FROM memberships m2
		WHERE m2.expire_date < memberships.expire_date
			OR (m2.expire_date = memberships.expire_date AND m2.id < memberships.id)
	) WHERE sort = 0`)
	return err
}

// GetMembership 单条会员记录。
func (s *Store) GetMembership(id int64) (*Membership, error) {
	m, err := scanMembership(func(dst ...any) error {
		return s.db.QueryRow(membershipCols+` FROM memberships WHERE id = ?`, id).Scan(dst...)
	})
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("会员记录不存在")
	}
	return m, err
}

func scanMembership(scan func(...any) error) (*Membership, error) {
	var m Membership
	if err := scan(&m.ID, &m.Biz, &m.Name, &m.StartDate, &m.ExpireDate, &m.PeriodDays, &m.Notify15dAt, &m.Sort, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.RemainingDays, m.Expired = membershipRemaining(m.ExpireDate, time.Now())
	m.Progress = membershipProgress(&m)
	return &m, nil
}

// validateMembership 新增/编辑共用校验与规整；周期天数越界回落默认 365；
// 开始时间可选（空 = 进度条回落剩余/周期口径），填了必须 ≤ 到期日期。
func validateMembership(m *Membership) error {
	m.Biz = strings.TrimSpace(m.Biz)
	m.Name = strings.TrimSpace(m.Name)
	m.StartDate = strings.TrimSpace(m.StartDate)
	if m.Biz == "" {
		return fmt.Errorf("请填写名称")
	}
	if _, err := time.ParseInLocation("2006-01-02", m.ExpireDate, time.Local); err != nil {
		return fmt.Errorf("到期日期格式应为 YYYY-MM-DD")
	}
	if m.StartDate != "" {
		if _, err := time.ParseInLocation("2006-01-02", m.StartDate, time.Local); err != nil {
			return fmt.Errorf("开始时间格式应为 YYYY-MM-DD")
		}
		if m.StartDate > m.ExpireDate {
			return fmt.Errorf("开始时间不能晚于到期日期")
		}
	}
	if m.PeriodDays <= 0 || m.PeriodDays > membershipPeriodMax {
		m.PeriodDays = membershipPeriodDefault
	}
	if len(m.Biz) > 50 || len(m.Name) > 50 {
		return fmt.Errorf("名称/会员名最长 50 字")
	}
	return nil
}

// CreateMembership 新建会员记录。
func (s *Store) CreateMembership(m *Membership) error {
	if err := validateMembership(m); err != nil {
		return err
	}
	now := time.Now().Unix()
	var sort int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(sort), 0) + 1 FROM memberships`).Scan(&sort); err != nil {
		return err
	}
	res, err := s.db.Exec(`INSERT INTO memberships (biz, name, start_date, expire_date, period_days, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Biz, m.Name, m.StartDate, m.ExpireDate, m.PeriodDays, sort, now, now)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.Sort = sort
	m.CreatedAt, m.UpdatedAt = now, now
	return nil
}

// UpdateMembership 编辑会员记录；到期日有变动时重置 15 天提醒标记
// （续期改日期后，新日期进 15 天窗口能再次提醒）。
func (s *Store) UpdateMembership(m *Membership) error {
	if err := validateMembership(m); err != nil {
		return err
	}
	cur, err := s.GetMembership(m.ID)
	if err != nil {
		return err
	}
	if cur.ExpireDate != m.ExpireDate {
		m.Notify15dAt = nil
	} else {
		m.Notify15dAt = cur.Notify15dAt
	}
	res, err := s.db.Exec(`UPDATE memberships SET biz = ?, name = ?, start_date = ?, expire_date = ?, period_days = ?, notify_15d_at = ?, updated_at = ? WHERE id = ?`,
		m.Biz, m.Name, m.StartDate, m.ExpireDate, m.PeriodDays, m.Notify15dAt, time.Now().Unix(), m.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会员记录不存在")
	}
	return nil
}

// DeleteMembership 删除会员记录（物理删除，会员是配置型数据不进回收站）。
func (s *Store) DeleteMembership(id int64) error {
	res, err := s.db.Exec(`DELETE FROM memberships WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会员记录不存在")
	}
	return nil
}

// ReorderMemberships 按传入 id 顺序写入 sort 1..n（到期提醒列表拖拽排序保存，
// 磁贴/重点指标同款口径）。不存在的 id 影响零行，无副作用；单事务保证原子。
func (s *Store) ReorderMemberships(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE memberships SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// membershipRemaining 剩余天数（到期日 − 今天，按本地时区自然日；负 = 已过期天数）
// 与是否已过期。
func membershipRemaining(expireDate string, now time.Time) (int, bool) {
	expire, err := time.ParseInLocation("2006-01-02", expireDate, time.Local)
	if err != nil {
		return 0, true
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	days := int(expire.Sub(today).Hours() / 24)
	return days, days < 0
}

// membershipProgress 进度条剩余占比（0~100）：有合法开始时间（≤ 到期日）按
// 起止区间算 剩余/区间，区间为零（开始=到期）直接 0；开始时间空/坏回落
// 剩余/period_days 旧口径（492 首版行为，存量行兼容）。
func membershipProgress(m *Membership) int {
	remaining, _ := membershipRemaining(m.ExpireDate, time.Now())
	total := 0
	if m.StartDate != "" {
		start, err1 := time.ParseInLocation("2006-01-02", m.StartDate, time.Local)
		expire, err2 := time.ParseInLocation("2006-01-02", m.ExpireDate, time.Local)
		if err1 == nil && err2 == nil && !expire.Before(start) {
			total = int(expire.Sub(start).Hours() / 24)
		}
	}
	if total == 0 {
		if m.StartDate != "" {
			return 0 // 有开始时间的零区间（开始=到期）：无剩余
		}
		total = m.PeriodDays
	}
	if total <= 0 {
		return 0
	}
	pct := remaining * 100 / total
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// MembershipStats 会员到期统计（主页概览卡）：Total=全部条数；
// Expiring=15 日内到期（含已过期未删的，都需要处理）。
func (s *Store) MembershipStats() (total, expiring int, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM memberships`).Scan(&total); err != nil {
		return 0, 0, err
	}
	limit := time.Now().AddDate(0, 0, 15).Format("2006-01-02")
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM memberships WHERE expire_date <= ?`, limit).Scan(&expiring); err != nil {
		return 0, 0, err
	}
	return total, expiring, nil
}

// DueExpiringMemberships 15 天提醒调度取数：进窗（到期日 ≤ 今天+15，含已过期）
// 且未发过提醒的记录。提醒标记在发送成功后写入；改到期日会重置标记重新武装。
func (s *Store) DueExpiringMemberships() ([]Membership, error) {
	limit := time.Now().AddDate(0, 0, 15).Format("2006-01-02")
	rows, err := s.db.Query(membershipCols+` FROM memberships
		WHERE notify_15d_at IS NULL AND expire_date <= ? ORDER BY expire_date, id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		m, err := scanMembership(func(dst ...any) error { return rows.Scan(dst...) })
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// MarkMembershipNotified 记录 15 天提醒已发送。
func (s *Store) MarkMembershipNotified(id int64, at int64) error {
	_, err := s.db.Exec(`UPDATE memberships SET notify_15d_at = ? WHERE id = ?`, at, id)
	return err
}
