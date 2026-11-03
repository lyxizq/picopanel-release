package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// med_logs 表：用药剂量台账（2026-11-02，状态机设计）。每个计划剂量一行
// （唯一索引 med_id+day+time），status 状态机：pending（日结预建，推送打标
// 落在行上 notified_at/escalated_at，两段推送失败下轮重试）→ taken/skipped
// （用户打卡，可补卡改 missed）；跨天仍未处理由日结批量改 missed（漏服）。
// prn 按需服药直接插 taken 行（time=实际时刻）。历史/依从率/主页卡全部由本表聚合。
const medLogsDDL = `CREATE TABLE IF NOT EXISTS med_logs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  med_id       INTEGER NOT NULL,
  day          TEXT    NOT NULL,
  time         TEXT    NOT NULL,
  status       TEXT    NOT NULL DEFAULT 'pending',
  notified_at  INTEGER,
  escalated_at INTEGER,
  acted_at     INTEGER,
  created_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_med_logs_slot ON med_logs(med_id, day, time);
CREATE INDEX IF NOT EXISTS idx_med_logs_day ON med_logs(day);`

// MedLogStatus 剂量状态白名单。
var MedLogStatuses = map[string]bool{"pending": true, "taken": true, "skipped": true, "missed": true}

// MedDose 一行剂量视图（台账联药物与人员，今日时间轴/推送取数共用）。
type MedDose struct {
	ID          int64  `json:"id"`
	MedID       int64  `json:"med_id"`
	Day         string `json:"day"`
	Time        string `json:"time"`
	Status      string `json:"status"`
	NotifiedAt  *int64 `json:"notified_at"`
	EscalatedAt *int64 `json:"escalated_at"`
	ActedAt     *int64 `json:"acted_at"`
	// 联查展示字段
	MedName      string   `json:"med_name"`
	Form         string   `json:"form"`
	Note         string   `json:"note"`
	DoseAmount   float64  `json:"dose_amount"`
	DoseUnit     string   `json:"dose_unit"`
	StockQty     *float64 `json:"stock_qty"`
	PersonID     int64    `json:"person_id"`
	PersonName   string   `json:"person_name"`
	PersonColor  string   `json:"person_color"`
	PersonUserID int64    `json:"-"` // 人员绑定的登录账号（2026-11-03：站内消息归属用，0=未绑定/管理员空间）
}

const medDoseCols = `SELECT l.id, l.med_id, l.day, l.time, l.status, l.notified_at, l.escalated_at, l.acted_at,
	m.name, m.form, m.note, m.dose_amount, m.dose_unit, m.stock_qty,
	p.id, p.name, p.color, p.user_id`

func scanMedDose(scan func(...any) error) (*MedDose, error) {
	var d MedDose
	if err := scan(&d.ID, &d.MedID, &d.Day, &d.Time, &d.Status, &d.NotifiedAt, &d.EscalatedAt, &d.ActedAt,
		&d.MedName, &d.Form, &d.Note, &d.DoseAmount, &d.DoseUnit, &d.StockQty,
		&d.PersonID, &d.PersonName, &d.PersonColor, &d.PersonUserID); err != nil {
		return nil, err
	}
	return &d, nil
}

// EnsureMedDay 日结生成（幂等）：为某天所有活跃药的每个计划时刻预建 pending 行
// （唯一索引兜底重复 INSERT OR IGNORE）。调度每分钟 tick 走 EnsureMedDayOnce（一天
// 只全量跑一次），当天编辑药物后 API 也会补调一次，保证 pending 与最新时间表一致。
// 早于药物最近改动（updated_at/created_at 较大者）的时刻不回填（2026-11-03 修）：
// 新建/编辑药物不当场补建已过的历史槽位——否则状态机把回填行当当日新剂量，建药
// 立即补推到点消息、60 分钟后又追发漏服催推。存量药不受影响（改动时刻在今天
// 之前，全天时刻照建，服务重启当天旧 pending 照旧补推，语义不变）。
func (s *Store) EnsureMedDay(day string) error {
	meds, err := s.ListMedications(0, false)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	for i := range meds {
		cutoff := meds[i].UpdatedAt
		if meds[i].CreatedAt > cutoff {
			cutoff = meds[i].CreatedAt
		}
		for _, t := range medDosesOn(&meds[i], day) {
			if due := slotUnix(day, t); due > 0 && due < cutoff {
				continue
			}
			if _, err := s.db.Exec(`INSERT OR IGNORE INTO med_logs (med_id, day, time, status, created_at)
				VALUES (?, ?, ?, 'pending', ?)`, meds[i].ID, day, t, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// EnsureMedDayOnce 调度日结入口：一天只在首个 tick 全量跑一次（进程内记账）。
// 每分钟重复全量 INSERT OR IGNORE 是纯写噪声（命中唯一约束回滚也要抢写锁，
// 2026-11-03 性能修）。失败不记账下轮重试；跨天自然重置；API 编辑路径仍走
// EnsureMedDay 全量（编辑后必须重排，不受记账影响）。
func (s *Store) EnsureMedDayOnce(day string) error {
	s.medDayMu.Lock()
	done := s.medDayDone == day
	s.medDayMu.Unlock()
	if done {
		return nil
	}
	if err := s.EnsureMedDay(day); err != nil {
		return err
	}
	s.medDayMu.Lock()
	s.medDayDone = day
	s.medDayMu.Unlock()
	return nil
}

// slotUnix 计划时刻的 Unix 时间戳（本地时区）；解析失败返回 0（恒建行兜底，
// 与既有行为一致——时刻在建药时已按 HH:MM 校验）。
func slotUnix(day, hhmm string) int64 {
	t, err := time.ParseInLocation("2006-01-02 15:04", day+" "+hhmm, time.Local)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// ResetMedDayOnce 清当日日结记账（API 排程失败兜底，2026-11-03 复审补）：建药/
// 编辑后的 EnsureMedDay 若遇 DB 瞬时错误，该药当天槽位会缺——清记账让下一分钟
// 调度轮的 EnsureMedDayOnce 重跑全量补上（Once 已记账则调度当天不再全量跑）。
func (s *Store) ResetMedDayOnce() {
	s.medDayMu.Lock()
	s.medDayDone = ""
	s.medDayMu.Unlock()
}

// RollMissedMedLogs 漏服日结：昨天及以前仍 pending 的批量改 missed（每分钟
// tick 调用，跨天即漏；missed 不再推送，页面标红，历史保留）。
func (s *Store) RollMissedMedLogs(today string) (int64, error) {
	res, err := s.db.Exec(`UPDATE med_logs SET status = 'missed' WHERE status = 'pending' AND day < ?`, today)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// MedDosesView 某天剂量视图（时间轴/日历点天明细共用），personID > 0 只看该人。
func (s *Store) MedDosesView(day string, personID int64) ([]MedDose, error) {
	q := medDoseCols + ` FROM med_logs l
		JOIN medications m ON m.id = l.med_id
		JOIN med_persons p ON p.id = m.person_id
		WHERE l.day = ?`
	if personID > 0 {
		q = fmt.Sprintf("%s AND m.person_id = %d", q, personID)
	}
	q += ` ORDER BY l.time, p.sort, p.id, m.sort, m.id`
	rows, err := s.db.Query(q, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MedDose
	for rows.Next() {
		d, err := scanMedDose(func(dst ...any) error { return rows.Scan(dst...) })
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// SetMedLogStatus 打卡状态机：任意状态 → taken|skipped（missed 可补卡），或 →
// pending（撤销打卡）。行不存在（当天补建的药/手工场景）则插入（pending 不落行）。
// 库存联动：→taken 扣一剂（原状态已是 taken 不重复扣）；离开 taken（撤销/改跳过）
// 回冲一剂——撤销往返净零，误触可无损恢复。
// 读行-写行-库存联动三步包一个事务（2026-11-03 修）：并发打卡同一槽位（两端
// 同时点 Web Push「完成」）原先会双双读到 pending 扣双份库存，补插路径并发还会
// 撞唯一索引报错。
func (s *Store) SetMedLogStatus(medID int64, day, tm, status string, actedAt int64) error {
	if !MedLogStatuses[status] {
		return fmt.Errorf("打卡状态不合法")
	}
	if !isValidHHMM(tm) {
		return fmt.Errorf("时刻格式不合法")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var curStatus string
	err = tx.QueryRow(`SELECT status FROM med_logs WHERE med_id = ? AND day = ? AND time = ?`, medID, day, tm).Scan(&curStatus)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !exists {
		if status == "pending" {
			return nil // 无行=pending，撤销到无行即空操作
		}
		if _, err := tx.Exec(`INSERT INTO med_logs (med_id, day, time, status, acted_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, medID, day, tm, status, actedAt, actedAt); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`UPDATE med_logs SET status = ?, acted_at = ? WHERE med_id = ? AND day = ? AND time = ?`,
			status, actedAt, medID, day, tm); err != nil {
			return err
		}
	}
	// 库存联动：进入 taken 扣一剂（原已 taken 不重复扣），离开 taken 回冲一剂
	if exists && curStatus != status {
		if curStatus == "taken" && status != "taken" {
			if _, err := tx.Exec(`UPDATE medications SET stock_qty = stock_qty + dose_amount, updated_at = ?
				WHERE id = ? AND stock_qty IS NOT NULL`, actedAt, medID); err != nil {
				return err
			}
		}
		if status == "taken" && curStatus != "taken" {
			if _, err := tx.Exec(`UPDATE medications SET stock_qty = MAX(0, stock_qty - dose_amount), updated_at = ?
				WHERE id = ? AND stock_qty IS NOT NULL`, actedAt, medID); err != nil {
				return err
			}
		}
	} else if !exists && status == "taken" {
		if _, err := tx.Exec(`UPDATE medications SET stock_qty = MAX(0, stock_qty - dose_amount), updated_at = ?
			WHERE id = ? AND stock_qty IS NOT NULL`, actedAt, medID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordMedPrn 按需药记一笔：以当前时刻落一条 taken 台账行（time=实际时刻），
// 复用打卡状态机扣库存（同一分钟连点不重复扣——同槽行已 taken 时状态机空操作）。
func (s *Store) RecordMedPrn(medID int64, at int64) error {
	m, err := s.GetMedication(medID)
	if err != nil {
		return err
	}
	if m.Kind != "prn" {
		return fmt.Errorf("该药不是按需药")
	}
	t := time.Unix(at, 0)
	return s.SetMedLogStatus(medID, t.Format("2006-01-02"), t.Format("15:04"), "taken", at)
}

// DueMedLogs 到点推送取数：今天已到时刻、pending、未推过的行（窗口语义=到点
// 即推；服务重启漏推的当天旧 pending 也会被扫到补推一次，错过推不轰炸——
// 跨天已被 RollMissedMedLogs 收走）。
func (s *Store) DueMedLogs(day, nowHHMM string) ([]MedDose, error) {
	list, err := s.medDosesByStatus(day, `l.status = 'pending' AND l.notified_at IS NULL`)
	if err != nil {
		return nil, err
	}
	var out []MedDose
	for _, d := range list {
		if d.Time <= nowHHMM {
			out = append(out, d)
		}
	}
	return out, nil
}

// EscalatableMedLogs 漏服催推取数：今天已推过、到点超 60 分钟（nowHHMMMinus1h
// =当前时刻回拨 1 小时，比较「计划时刻 ≤ 它」即超时满一小时）、未催过的 pending。
// 2026-11-03 修方向反了的存量 bug：原实现传 now+1h，把「还有不到一小时才到点」
// 的未到时剂量也判成漏服提前催推。
func (s *Store) EscalatableMedLogs(day, nowHHMMMinus1h string) ([]MedDose, error) {
	list, err := s.medDosesByStatus(day, `l.status = 'pending' AND l.notified_at IS NOT NULL AND l.escalated_at IS NULL`)
	if err != nil {
		return nil, err
	}
	var out []MedDose
	for _, d := range list {
		if d.Time <= nowHHMMMinus1h {
			out = append(out, d)
		}
	}
	return out, nil
}

// MedLogByID 按台账行 ID 取单行（提醒窗口直达完成用：消息/Web Push 通知的
// 「完成」按钮按 ref_id=行 ID 打卡）。未命中返回 (nil, nil)。
func (s *Store) MedLogByID(id int64) (*MedDose, error) {
	rows, err := s.db.Query(medDoseCols+` FROM med_logs l
		JOIN medications m ON m.id = l.med_id
		JOIN med_persons p ON p.id = m.person_id
		WHERE l.id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return scanMedDose(func(dst ...any) error { return rows.Scan(dst...) })
}

func (s *Store) medDosesByStatus(day, cond string) ([]MedDose, error) {
	rows, err := s.db.Query(medDoseCols+` FROM med_logs l
		JOIN medications m ON m.id = l.med_id
		JOIN med_persons p ON p.id = m.person_id
		WHERE l.day = ? AND `+cond, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MedDose
	for rows.Next() {
		d, err := scanMedDose(func(dst ...any) error { return rows.Scan(dst...) })
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// MarkMedLogNotified 记录到点提醒已发送（站内落库成功后才调用，失败下轮重试）。
func (s *Store) MarkMedLogNotified(id, at int64) error {
	_, err := s.db.Exec(`UPDATE med_logs SET notified_at = ? WHERE id = ?`, at, id)
	return err
}

// MarkMedLogEscalated 记录漏服催推已发送。
func (s *Store) MarkMedLogEscalated(id, at int64) error {
	_, err := s.db.Exec(`UPDATE med_logs SET escalated_at = ? WHERE id = ?`, at, id)
	return err
}

// LowStockMeds 低库存取数：启用库存、设了警戒线、已跌破且未提醒过、未归档。
func (s *Store) LowStockMeds() ([]Medication, error) {
	return s.queryMedications(medicationCols + ` FROM medications m JOIN med_persons p ON p.id = m.person_id
		WHERE m.stock_qty IS NOT NULL AND m.stock_alert > 0 AND m.stock_qty <= m.stock_alert
		AND m.stock_notified_at IS NULL AND m.archived_at IS NULL
		ORDER BY p.sort, p.id, m.sort, m.id`)
}

// MarkMedStockNotified 记录补货提醒已发送。
func (s *Store) MarkMedStockNotified(id, at int64) error {
	_, err := s.db.Exec(`UPDATE medications SET stock_notified_at = ? WHERE id = ?`, at, id)
	return err
}

// MedPersonStat 主页卡/早报的分人今日统计（Total 含 pending/taken/skipped/
// missed 全部计划剂量；Next 取该人今天最早的待处理剂量）。
type MedPersonStat struct {
	PersonID int64  `json:"person_id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Total    int    `json:"total"`
	Taken    int    `json:"taken"`
	Missed   int    `json:"missed"`
	NextTime string `json:"next_time"`
	NextName string `json:"next_name"`
}

// MedTodayStats 分人今日用药统计（主页卡/早报共用；无台账的人员不出现）。
func (s *Store) MedTodayStats(day string) ([]MedPersonStat, error) {
	rows, err := s.db.Query(`SELECT p.id, p.name, p.color, COUNT(l.id),
		COALESCE(SUM(CASE WHEN l.status = 'taken' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN l.status = 'missed' THEN 1 ELSE 0 END), 0)
		FROM med_logs l
		JOIN medications m ON m.id = l.med_id
		JOIN med_persons p ON p.id = m.person_id
		WHERE l.day = ? GROUP BY p.id ORDER BY p.sort, p.id`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MedPersonStat
	index := map[int64]int{}
	for rows.Next() {
		var st MedPersonStat
		if err := rows.Scan(&st.PersonID, &st.Name, &st.Color, &st.Total, &st.Taken, &st.Missed); err != nil {
			return nil, err
		}
		index[st.PersonID] = len(out)
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	next, err := s.medNextDoses(day)
	if err != nil {
		return nil, err
	}
	for _, d := range next {
		if i, ok := index[d.PersonID]; ok {
			out[i].NextTime, out[i].NextName = d.Time, d.MedName
		}
	}
	return out, nil
}

// medNextDoses 今天各人员最早的待处理剂量（时间升序，Go 侧取每人首条）。
func (s *Store) medNextDoses(day string) ([]MedDose, error) {
	rows, err := s.db.Query(medDoseCols+` FROM med_logs l
		JOIN medications m ON m.id = l.med_id
		JOIN med_persons p ON p.id = m.person_id
		WHERE l.day = ? AND l.status = 'pending' ORDER BY l.time`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MedDose
	for rows.Next() {
		d, err := scanMedDose(func(dst ...any) error { return rows.Scan(dst...) })
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// MedDaySummary 月历视图的一天汇总。
type MedDaySummary struct {
	Day     string `json:"day"`
	Total   int    `json:"total"`
	Taken   int    `json:"taken"`
	Skipped int    `json:"skipped"`
	Missed  int    `json:"missed"`
}

// MedMonthSummary 月历聚合（month=YYYY-MM；自然月内逐天）。personID>0 只算
// 该人员（2026-11-03 家庭成员口径：受限用户的月历只含自己绑定的人）。
func (s *Store) MedMonthSummary(month string, personID int64) ([]MedDaySummary, error) {
	q := `SELECT l.day, COUNT(l.id),
		COALESCE(SUM(CASE WHEN l.status = 'taken' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN l.status = 'skipped' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN l.status = 'missed' THEN 1 ELSE 0 END), 0)
		FROM med_logs l JOIN medications m ON m.id = l.med_id
		WHERE l.day LIKE ?`
	args := []any{month + "-%"}
	if personID > 0 {
		q += ` AND m.person_id = ?`
		args = append(args, personID)
	}
	q += ` GROUP BY l.day ORDER BY l.day`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MedDaySummary
	for rows.Next() {
		var d MedDaySummary
		if err := rows.Scan(&d.Day, &d.Total, &d.Taken, &d.Skipped, &d.Missed); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MedAdherence 依从率区间聚合（from/to 含两端；personID>0 只算该人员；
// prn 记录也计 taken 侧但不进 Total——只按计划口径算：Total=计划剂量数）。
func (s *Store) MedAdherence(fromDay, toDay string, personID int64) (total, taken, skipped, missed int, err error) {
	q := `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN l.status = 'taken' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN l.status = 'skipped' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN l.status = 'missed' THEN 1 ELSE 0 END), 0)
		FROM med_logs l JOIN medications m ON m.id = l.med_id
		WHERE l.day >= ? AND l.day <= ? AND m.kind != 'prn'`
	args := []any{fromDay, toDay}
	if personID > 0 {
		q += ` AND m.person_id = ?`
		args = append(args, personID)
	}
	err = s.db.QueryRow(q, args...).
		Scan(&total, &taken, &skipped, &missed)
	return
}
