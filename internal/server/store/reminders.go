package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// reminder_groups + reminders 表：提醒事项（分组 + 看板 + 重复规则）。
//
// reminders 关键列：
//
//	group_id    所属分组（0 = 未分组）
//	parent_id   父任务 ID（0 = 顶级；子任务最多 5 级，每个子任务是独立待办）
//	notify      是否提醒（0 = 仅记录：不推送、不进站内消息；1 = 三段提醒，缺省 1）
//	due_date    到期日 YYYY-MM-DD；due_time 到期时刻 HH:MM（空 = 09:00）
//	repeat_json 重复规则 JSON（RepeatRule）
//	series_id   重复系列 ID（同系列共享；完成一次生成下一条时沿用）
//	done_at     完成时刻（NULL = 未完成；站内消息红字联查依据）
//	deleted_at  软删时间戳（0 = 正常；>0 = 在回收站，看板/调度/联查均排除）
//	notify_1h_at / notify_due_at / notify_nextday_at
//	            三段提醒（到期前 1 小时 / 到期时 / 次日最后提醒）已发送标记，
//	            NULL = 未发；次日提醒发过即不再提醒
const reminderGroupsDDL = `CREATE TABLE IF NOT EXISTS reminder_groups (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL,
  sort       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);`

const remindersDDL = `CREATE TABLE IF NOT EXISTS reminders (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  group_id         INTEGER NOT NULL DEFAULT 0,
  parent_id        INTEGER NOT NULL DEFAULT 0,
  notify           INTEGER NOT NULL DEFAULT 1,
  title            TEXT    NOT NULL,
  note             TEXT    NOT NULL DEFAULT '',
  due_date         TEXT    NOT NULL,
  due_time         TEXT    NOT NULL DEFAULT '',
  repeat_json      TEXT    NOT NULL DEFAULT '',
  series_id        INTEGER NOT NULL DEFAULT 0,
  prev_id          INTEGER NOT NULL DEFAULT 0,
  important        INTEGER NOT NULL DEFAULT 0,
  my_day           INTEGER NOT NULL DEFAULT 0,
  done_at          INTEGER,
  notify_1h_at     INTEGER,
  notify_due_at    INTEGER,
  notify_nextday_at INTEGER,
  deleted_at       INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_reminders_due    ON reminders(due_date, due_time);
CREATE INDEX IF NOT EXISTS idx_reminders_series ON reminders(series_id);`

// 注意：idx_reminders_parent 不能放在这里——parent_id 是老库后补列（store.go
// 幂等 ALTER），建索引先于补列执行会在已部署库上炸「no such column: parent_id」；
// 该索引在补列之后单独建（见 store.go Open）。

// ReminderGroup 提醒事项分组。
type ReminderGroup struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Sort      int    `json:"sort"`
	CreatedAt int64  `json:"created_at"`
}

// Reminder 提醒事项。due_time 空 = 09:00（默认提醒时刻）；
// repeat 为 RepeatRule 的 JSON 原文；done_at 非空即已完成；
// important 星标（重要视图聚合）；my_day 加入「我的一天」（完成后自动移出）；
// DeletedAt >0 即在回收站（软删，可恢复）；ParentID 父任务（0 = 顶级，
// 子任务最多 5 级，各自独立的待办）；Notify=false 仅记录不做任何提醒。
type Reminder struct {
	ID              int64  `json:"id"`
	GroupID         int64  `json:"group_id"`
	ParentID        int64  `json:"parent_id"`
	Notify          bool   `json:"notify"`
	Title           string `json:"title"`
	Note            string `json:"note"`
	DueDate         string `json:"due_date"` // YYYY-MM-DD
	DueTime         string `json:"due_time"` // HH:MM，空 = 09:00
	Repeat          string `json:"repeat"`   // RepeatRule JSON 原文
	SeriesID        int64  `json:"series_id"`
	PrevID          int64  `json:"prev_id"` // 本条由哪条完成生成（完成时写入；恢复时据此撤回下一条）
	Important       bool   `json:"important"`
	MyDay           bool   `json:"my_day"`
	DoneAt          *int64 `json:"done_at"`
	Notify1hAt      *int64 `json:"notify_1h_at"`
	NotifyDueAt     *int64 `json:"notify_due_at"`
	NotifyNextdayAt *int64 `json:"notify_nextday_at"`
	DeletedAt       int64  `json:"deleted_at"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	// 展示用联表字段：GroupName 分组名；Overdue 已逾期（未完成且到期日早于今天）
	GroupName string `json:"group_name"`
	Overdue   bool   `json:"overdue"`
}

// RepeatRule 重复规则（存 reminders.repeat_json）：
//
//	Type none=不重复 daily/weekly/monthly/yearly=按自然周期 custom=自定义每 N 个单位
//	Every/Unit 仅 custom 用；Until 结束日期（YYYY-MM-DD，空 = 不限），
//	下一条到期日晚于 Until 则不再生成。
type RepeatRule struct {
	Type  string `json:"type"`
	Every int    `json:"every"`
	Unit  string `json:"unit"`
	Until string `json:"until"`
}

// Valid 规则是否可用（non-none 时 custom 必须带合法单位与正数间隔）。
func (r RepeatRule) Valid() bool {
	switch r.Type {
	case "", "none":
		return true
	case "daily", "weekly", "monthly", "yearly":
		return true
	case "custom":
		if r.Every < 1 {
			return false
		}
		switch r.Unit {
		case "day", "week", "month", "year":
			return true
		}
		return false
	}
	return false
}

// EffectiveDueTime 到期时刻（空回落 09:00），返回 HH:MM。
func (r *Reminder) EffectiveDueTime() string {
	if t := strings.TrimSpace(r.DueTime); t != "" {
		return t
	}
	return "09:00"
}

// DueUnix 到期时刻的 Unix 时间戳（本地时区）。
func (r *Reminder) DueUnix() int64 {
	t, err := time.ParseInLocation("2006-01-02 15:04", r.DueDate+" "+r.EffectiveDueTime(), time.Local)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// ParseRepeat 解析 repeat JSON（空/坏值 = 不重复）。
func ParseRepeat(s string) RepeatRule {
	var r RepeatRule
	if s == "" {
		return r
	}
	_ = json.Unmarshal([]byte(s), &r)
	return r
}

// ListReminderGroups 全部分组（sort 升序，同序按创建先后）。
func (s *Store) ListReminderGroups() ([]ReminderGroup, error) {
	rows, err := s.db.Query(`SELECT id, name, sort, created_at FROM reminder_groups ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReminderGroup
	for rows.Next() {
		var g ReminderGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Sort, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreateReminderGroup 新建分组，返回 ID。
func (s *Store) CreateReminderGroup(name string, sort int) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO reminder_groups (name, sort, created_at) VALUES (?, ?, ?)`,
		name, sort, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateReminderGroup 改分组名/排序。
func (s *Store) UpdateReminderGroup(id int64, name string, sort int) error {
	_, err := s.db.Exec(`UPDATE reminder_groups SET name = ?, sort = ? WHERE id = ?`, name, sort, id)
	return err
}

// DeleteReminderGroup 删分组：组内提醒事项落回「未分组」（不删事项）；
// 回收站中的条目保持原样（group_id 仍指已删的组，恢复时回落未分组）。
func (s *Store) DeleteReminderGroup(id int64) error {
	if _, err := s.db.Exec(`UPDATE reminders SET group_id = 0 WHERE group_id = ? AND deleted_at = 0`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM reminder_groups WHERE id = ?`, id)
	return err
}

const reminderCols = `id, group_id, parent_id, notify, title, note, due_date, due_time, repeat_json, series_id, prev_id,
	important, my_day, done_at, notify_1h_at, notify_due_at, notify_nextday_at, deleted_at, created_at, updated_at`

func scanReminder(scan func(...any) error) (*Reminder, error) {
	var r Reminder
	var important, myDay, notify int
	if err := scan(&r.ID, &r.GroupID, &r.ParentID, &notify, &r.Title, &r.Note, &r.DueDate, &r.DueTime, &r.Repeat, &r.SeriesID, &r.PrevID,
		&important, &myDay, &r.DoneAt, &r.Notify1hAt, &r.NotifyDueAt, &r.NotifyNextdayAt, &r.DeletedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Important = important == 1
	r.MyDay = myDay == 1
	r.Notify = notify == 1
	return &r, nil
}

// GetReminder 单条提醒。
func (s *Store) GetReminder(id int64) (*Reminder, error) {
	r, err := scanReminder(func(dst ...any) error {
		return s.db.QueryRow(`SELECT `+reminderCols+` FROM reminders WHERE id = ?`, id).Scan(dst...)
	})
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("提醒事项不存在")
	}
	return r, err
}

// validateReminder 新增/编辑共用的字段校验与规整。
func validateReminder(r *Reminder) error {
	if strings.TrimSpace(r.Title) == "" {
		return fmt.Errorf("请填写任务标题")
	}
	if _, err := time.ParseInLocation("2006-01-02", r.DueDate, time.Local); err != nil {
		return fmt.Errorf("到期日期格式应为 YYYY-MM-DD")
	}
	r.Title = strings.TrimSpace(r.Title)
	if t := strings.TrimSpace(r.DueTime); t != "" {
		if _, err := time.ParseInLocation("15:04", t, time.Local); err != nil {
			return fmt.Errorf("提醒时间格式应为 HH:MM")
		}
		r.DueTime = t
	}
	if !ParseRepeat(r.Repeat).Valid() {
		return fmt.Errorf("重复规则不合法")
	}
	return nil
}

// reminderDepth 返回指定父任务自身的层级（顶级 = 1，parent_id = 0 返回 0），
// 子任务层级 = 返回值 + 1。父任务不存在/在回收站/成环时返回错误。
func (s *Store) reminderDepth(parentID int64) (int, error) {
	depth := 0
	seen := map[int64]bool{}
	for id := parentID; id != 0; {
		if seen[id] {
			return 0, fmt.Errorf("父任务链成环")
		}
		seen[id] = true
		var next int64
		err := s.db.QueryRow(`SELECT parent_id FROM reminders WHERE id = ? AND deleted_at = 0`, id).Scan(&next)
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("父任务不存在或已在回收站")
		}
		if err != nil {
			return 0, err
		}
		depth++
		id = next
	}
	return depth, nil
}

// reminderDescendants 收集 id 的全部在世后代（不含自身；含子代的子代…），
// 只收 deleted_at = 0 的（回收站中的保持原样）。
func (s *Store) reminderDescendants(id int64) ([]int64, error) {
	var out []int64
	frontier := []int64{id}
	for len(frontier) > 0 {
		rows, err := s.db.Query(`SELECT id FROM reminders WHERE parent_id = ? AND deleted_at = 0`, frontier[0])
		if err != nil {
			return nil, err
		}
		var kids []int64
		for rows.Next() {
			var k int64
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return nil, err
			}
			kids = append(kids, k)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		out = append(out, kids...)
		frontier = append(frontier[1:], kids...)
	}
	return out, nil
}

// CreateReminder 新建提醒事项。ParentID 非 0 时校验父任务存在且层级
// （父层级 + 1 ≤ 5）；子任务默认继承父任务分组（未显式指定时）。
func (s *Store) CreateReminder(r *Reminder) error {
	if err := validateReminder(r); err != nil {
		return err
	}
	if r.ParentID != 0 {
		depth, err := s.reminderDepth(r.ParentID)
		if err != nil {
			return err
		}
		if depth >= 5 { // 父任务已是第 5 级，子任务将是第 6 级
			return fmt.Errorf("子任务最多 5 级")
		}
		if r.GroupID == 0 {
			if p, err := s.GetReminder(r.ParentID); err == nil {
				r.GroupID = p.GroupID
			}
		}
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO reminders (group_id, parent_id, notify, title, note, due_date, due_time, repeat_json, series_id, prev_id, important, my_day, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.GroupID, r.ParentID, boolInt(r.Notify), r.Title, r.Note, r.DueDate, r.DueTime, r.Repeat, r.SeriesID, r.PrevID, boolInt(r.Important), boolInt(r.MyDay), now, now)
	if err != nil {
		return err
	}
	r.ID, r.CreatedAt, r.UpdatedAt = 0, now, now
	r.ID, _ = res.LastInsertId()
	return nil
}

// UpdateReminder 编辑提醒事项（全量字段，不含完成态/提醒标记/父任务
// ——parent_id 只能建时指定；回收站中的不可编辑）。
func (s *Store) UpdateReminder(r *Reminder) error {
	if err := validateReminder(r); err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE reminders SET group_id = ?, notify = ?, title = ?, note = ?, due_date = ?, due_time = ?, repeat_json = ?, important = ?, my_day = ?, updated_at = ? WHERE id = ? AND deleted_at = 0`,
		r.GroupID, boolInt(r.Notify), r.Title, r.Note, r.DueDate, r.DueTime, r.Repeat, boolInt(r.Important), boolInt(r.MyDay), time.Now().Unix(), r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("提醒事项不存在")
	}
	return nil
}

// DeleteReminder 删除提醒事项（软删进回收站，可恢复）；同系列（series_id 相同）
// 未完成的后续提醒一并进回收站（对齐 MS To Do 语义：删掉重复任务 = 之后的都删），
// 已完成的历史记录保留；子任务（全部后代）一并进回收站。
// 本条 series_id 为 0（从未生成过下一条）时只删自身与后代。
func (s *Store) DeleteReminder(id int64) error {
	r, err := s.GetReminder(id)
	if err != nil {
		return err
	}
	if r.DeletedAt > 0 {
		return nil // 已在回收站，幂等
	}
	now := time.Now().Unix()
	kids, err := s.reminderDescendants(id)
	if err != nil {
		return err
	}
	// 本体 + 系列后续 + 全部后代在一个事务里软删：中途失败不留半删断链
	// （RestoreReminder 靠 deleted_at 链恢复，断链会漏恢复）
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if r.SeriesID == 0 {
		if _, err = tx.Exec(`UPDATE reminders SET deleted_at = ?, updated_at = ? WHERE deleted_at = 0 AND id = ?`, now, now, id); err != nil {
			return err
		}
	} else {
		if _, err = tx.Exec(`UPDATE reminders SET deleted_at = ?, updated_at = ? WHERE deleted_at = 0 AND (id = ? OR (series_id = ? AND done_at IS NULL))`, now, now, id, r.SeriesID); err != nil {
			return err
		}
	}
	for _, k := range kids {
		if _, err := tx.Exec(`UPDATE reminders SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at = 0`, now, now, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------- 回收站 ----------

// ListReminderTrash 回收站列表（按删除时间倒序，带分组名；含已完成条目）。
func (s *Store) ListReminderTrash() ([]Reminder, error) {
	rows, err := s.db.Query(`SELECT r.id, r.group_id, r.parent_id, r.notify, r.title, r.note, r.due_date, r.due_time, r.repeat_json, r.series_id,
		r.important, r.my_day,
		r.done_at, r.notify_1h_at, r.notify_due_at, r.notify_nextday_at, r.deleted_at, r.created_at, r.updated_at,
		COALESCE(g.name, '')
		FROM reminders r LEFT JOIN reminder_groups g ON g.id = r.group_id
		WHERE r.deleted_at > 0 ORDER BY r.deleted_at DESC, r.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reminder
	for rows.Next() {
		var r Reminder
		var important, myDay, notify int
		if err := rows.Scan(&r.ID, &r.GroupID, &r.ParentID, &notify, &r.Title, &r.Note, &r.DueDate, &r.DueTime, &r.Repeat, &r.SeriesID,
			&important, &myDay,
			&r.DoneAt, &r.Notify1hAt, &r.NotifyDueAt, &r.NotifyNextdayAt, &r.DeletedAt, &r.CreatedAt, &r.UpdatedAt,
			&r.GroupName); err != nil {
			return nil, err
		}
		r.Important = important == 1
		r.MyDay = myDay == 1
		r.Notify = notify == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// RestoreReminder 从回收站恢复，整链联动：
//   - 恢复任务时，其全部后代（多级子任务，均在回收站）一并恢复；
//   - 恢复子任务时，其全部上级（多级，在回收站的）一并恢复——父在世即停
//     （更上层必然在世，链已通）；
//   - 上级链中途被彻底删除（行不存在）处停下，断点以下条目的父不在世，
//     兜底升为顶级（见恢复后的 parent 回退 UPDATE）。
//
// 原分组已不存在（含组被删）时回落未分组（同文档回收站口径）。
func (s *Store) RestoreReminder(id int64) (*Reminder, error) {
	r, err := s.GetReminder(id)
	if err != nil {
		return nil, err
	}
	if r.DeletedAt == 0 {
		return nil, fmt.Errorf("该事项不在回收站中")
	}
	ids := map[int64]bool{id: true}
	// 上级链：父在回收站则纳入并继续向上，父在世/已彻底删（查无此行）即停
	for pid := r.ParentID; pid > 0 && !ids[pid]; {
		p, err := s.GetReminder(pid)
		if err != nil {
			break // 已彻底删除：链断
		}
		ids[pid] = true
		if p.DeletedAt == 0 {
			break // 父在世：更上层必在世，链已通
		}
		pid = p.ParentID
	}
	// 后代：回收站中的全部后代（多级）一并纳入
	frontier := []int64{id}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		rows, err := s.db.Query(`SELECT id FROM reminders WHERE parent_id = ? AND deleted_at > 0`, cur)
		if err != nil {
			return nil, err
		}
		var kids []int64
		for rows.Next() {
			var k int64
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return nil, err
			}
			kids = append(kids, k)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, k := range kids {
			if !ids[k] {
				ids[k] = true
				frontier = append(frontier, k)
			}
		}
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	for k := range ids {
		args = append(args, k)
	}
	now := time.Now().Unix()
	// 先整链解除软删（链内条目全部在世，彼此父子关系原样保留）……
	if _, err := s.db.Exec(`UPDATE reminders SET deleted_at = 0, updated_at = ? WHERE id IN (`+ph+`) AND deleted_at > 0`,
		append([]any{now}, args...)...); err != nil {
		return nil, err
	}
	// ……再统一兜底：组已删回落未分组；父不在世（链断点/父被彻底删）升顶级
	if _, err := s.db.Exec(`UPDATE reminders SET updated_at = ?,
		group_id = CASE WHEN group_id > 0 AND group_id NOT IN (SELECT id FROM reminder_groups) THEN 0 ELSE group_id END,
		parent_id = CASE WHEN parent_id > 0 AND parent_id NOT IN (SELECT id FROM reminders WHERE deleted_at = 0) THEN 0 ELSE parent_id END
		WHERE id IN (`+ph+`)`, append([]any{now}, args...)...); err != nil {
		return nil, err
	}
	return s.GetReminder(id)
}

// HardDeleteReminder 彻底删除单条（仅限回收站中的条目）。
func (s *Store) HardDeleteReminder(id int64) error {
	res, err := s.db.Exec(`DELETE FROM reminders WHERE id = ? AND deleted_at > 0`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("该事项不在回收站中")
	}
	return nil
}

// EmptyReminderTrash 清空回收站（全部软删条目物理删除），返回删除条数。
func (s *Store) EmptyReminderTrash() (int, error) {
	res, err := s.db.Exec(`DELETE FROM reminders WHERE deleted_at > 0`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// nextDueDate 按规则算下一条到期日（YYYY-MM-DD）：daily/weekly 即自然日/周
// 步进；monthly 加 N 个月并把月末收敛（1 月 31 日 → 2 月 28/29 日）；
// yearly 加 N 年（2 月 29 日平年收敛为 2 月 28 日）。无下一条返回 ""。
func nextDueDate(dueDate string, rule RepeatRule) string {
	base, err := time.ParseInLocation("2006-01-02", dueDate, time.Local)
	if err != nil {
		return ""
	}
	var next time.Time
	switch rule.Type {
	case "daily":
		next = base.AddDate(0, 0, 1)
	case "weekly":
		next = base.AddDate(0, 0, 7)
	case "monthly":
		next = addMonthsClamp(base, 1)
	case "yearly":
		next = addMonthsClamp(base, 12)
	case "custom":
		switch rule.Unit {
		case "day":
			next = base.AddDate(0, 0, rule.Every)
		case "week":
			next = base.AddDate(0, 0, 7*rule.Every)
		case "month":
			next = addMonthsClamp(base, rule.Every)
		case "year":
			next = addMonthsClamp(base, 12*rule.Every)
		default:
			return ""
		}
	default:
		return ""
	}
	ns := next.Format("2006-01-02")
	if rule.Until != "" && ns > rule.Until {
		return ""
	}
	return ns
}

// addMonthsClamp 加 n 个月，日期超出目标月天数时收敛到月末（苹果同款行为）。
func addMonthsClamp(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, t.Location())
	lastDay := first.AddDate(0, 1, -1).Day()
	if d > lastDay {
		d = lastDay
	}
	return time.Date(first.Year(), first.Month(), d, 0, 0, 0, 0, t.Location())
}

// CompleteReminder 完成提醒：打上 done_at；重复事项按规则生成下一条
// （同组同内容，series_id 沿用，到期日=下个周期，提醒标记全部复位，prev_id
// 记录被完成条目便于恢复时撤回），返回生成的下一条（不重复/已过结束日期返回 nil）。
// 同系列已有未完成条目时不再生成（恢复过的旧条目再完成，不会重复造出下一条）。
func (s *Store) CompleteReminder(id int64) (*Reminder, error) {
	r, err := s.GetReminder(id)
	if err != nil {
		return nil, err
	}
	if r.DoneAt != nil {
		return nil, nil // 已完成，幂等
	}
	if r.DeletedAt > 0 {
		return nil, fmt.Errorf("提醒事项已在回收站")
	}
	now := time.Now().Unix()
	if _, err := s.db.Exec(`UPDATE reminders SET done_at = ?, updated_at = ? WHERE id = ?`, now, now, id); err != nil {
		return nil, err
	}
	rule := ParseRepeat(r.Repeat)
	if rule.Type == "" || rule.Type == "none" {
		return nil, nil
	}
	nextDate := nextDueDate(r.DueDate, rule)
	if nextDate == "" {
		return nil, nil
	}
	series := r.SeriesID
	if series == 0 {
		series = r.ID // 老数据没有系列 ID，从本条起算
	}
	var siblings int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM reminders WHERE series_id = ? AND done_at IS NULL AND deleted_at = 0 AND id != ?`, series, r.ID).Scan(&siblings); err != nil {
		return nil, err
	}
	if siblings > 0 {
		return nil, nil
	}
	next := &Reminder{
		GroupID: r.GroupID, ParentID: r.ParentID, Notify: r.Notify, Title: r.Title, Note: r.Note,
		DueDate: nextDate, DueTime: r.DueTime, Repeat: r.Repeat, SeriesID: series, PrevID: r.ID,
	}
	if err := s.CreateReminder(next); err != nil {
		return nil, fmt.Errorf("生成下一条重复提醒失败: %w", err)
	}
	return next, nil
}

// ReopenReminder 恢复已完成事项为未完成（清 done_at）。周期任务完成时曾自动
// 生成过下一条：恢复时把仍未完成的该条撤回，系列还原到完成前状态——再次完成
// 只会重新生成一条，不会越积越多。返回被撤回的下一条（没有则 nil）。
func (s *Store) ReopenReminder(id int64) (*Reminder, error) {
	r, err := s.GetReminder(id)
	if err != nil {
		return nil, err
	}
	if r.DoneAt == nil {
		return nil, nil // 未完成，幂等
	}
	if r.DeletedAt > 0 {
		return nil, fmt.Errorf("提醒事项已在回收站")
	}
	now := time.Now().Unix()
	if _, err := s.db.Exec(`UPDATE reminders SET done_at = NULL, updated_at = ? WHERE id = ?`, now, id); err != nil {
		return nil, err
	}
	series := r.SeriesID
	if series == 0 {
		series = r.ID
	}
	// 自动生成的下一条：优先按 prev_id 精确匹配；升级前的旧数据没有 prev_id，
	// 退化为同系列 id 更大的最小未完成条目（系列内条目恒按生成顺序递增）
	next, err := scanReminder(func(dst ...any) error {
		return s.db.QueryRow(`SELECT `+reminderCols+` FROM reminders
			WHERE done_at IS NULL AND deleted_at = 0 AND (prev_id = ? OR (prev_id = 0 AND series_id = ? AND id > ?))
			ORDER BY (prev_id = ?) DESC, id LIMIT 1`, id, series, r.ID, id).Scan(dst...)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`DELETE FROM reminders WHERE id = ?`, next.ID); err != nil {
		return nil, err
	}
	return next, nil
}

// boolInt 布尔转 SQLite 整型列值。
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ReminderBoard 看板数据：today=未完成且到期日 ≤ 今天（逾期也归今天，按日期
// 升序）；plan=未完成且到期日在今天之后（按日期升序）；done=最近完成 50 条。
func (s *Store) ReminderBoard() (today, plan, done []Reminder, err error) {
	todayStr := time.Now().Format("2006-01-02")
	load := func(where, order string, args ...any) ([]Reminder, error) {
		q := `SELECT r.id, r.group_id, r.parent_id, r.notify, r.title, r.note, r.due_date, r.due_time, r.repeat_json, r.series_id,
			r.important, r.my_day,
			r.done_at, r.notify_1h_at, r.notify_due_at, r.notify_nextday_at, r.created_at, r.updated_at,
			COALESCE(g.name, '')` +
			// Overdue：未完成且到期日早于今天
			`, CASE WHEN r.done_at IS NULL AND r.due_date < ? THEN 1 ELSE 0 END
			FROM reminders r LEFT JOIN reminder_groups g ON g.id = r.group_id ` + where + order
		rows, err := s.db.Query(q, append([]any{todayStr}, args...)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Reminder
		for rows.Next() {
			var r Reminder
			var important, myDay, notify, overdue int
			if err := rows.Scan(&r.ID, &r.GroupID, &r.ParentID, &notify, &r.Title, &r.Note, &r.DueDate, &r.DueTime, &r.Repeat, &r.SeriesID,
				&important, &myDay,
				&r.DoneAt, &r.Notify1hAt, &r.NotifyDueAt, &r.NotifyNextdayAt, &r.CreatedAt, &r.UpdatedAt,
				&r.GroupName, &overdue); err != nil {
				return nil, err
			}
			r.Important = important == 1
			r.MyDay = myDay == 1
			r.Notify = notify == 1
			r.Overdue = overdue == 1
			out = append(out, r)
		}
		return out, rows.Err()
	}
	if today, err = load(`WHERE r.deleted_at = 0 AND r.done_at IS NULL AND r.due_date <= ?`, ` ORDER BY r.due_date, r.due_time, r.id`, todayStr); err != nil {
		return nil, nil, nil, err
	}
	if plan, err = load(`WHERE r.deleted_at = 0 AND r.done_at IS NULL AND r.due_date > ?`, ` ORDER BY r.due_date, r.due_time, r.id LIMIT 500`, todayStr); err != nil {
		return nil, nil, nil, err
	}
	if done, err = load(`WHERE r.deleted_at = 0 AND r.done_at IS NOT NULL`, ` ORDER BY r.done_at DESC LIMIT 50`); err != nil {
		return nil, nil, nil, err
	}
	return today, plan, done, nil
}

// DueRemindersWindow 取到期时刻落在 [fromUnix, toUnix] 窗口内、未完成、开了
// 「是否提醒」（notify=1）、且指定提醒段未发过的提醒（提醒调度用）：
// kind = 1h|due|nextday 对应三段标记列。notify=0 的事项仅记录，不进任何
// 推送与站内消息，在这里就被排除。
// 到期时刻在 Go 侧按本地时区算（SQL strftime 会把本地时间串当 UTC，差一个时区）。
func (s *Store) DueRemindersWindow(fromUnix, toUnix int64, kind string) ([]Reminder, error) {
	col := ""
	switch kind {
	case "1h":
		col = "notify_1h_at"
	case "due":
		col = "notify_due_at"
	case "nextday":
		col = "notify_nextday_at"
	default:
		return nil, fmt.Errorf("未知提醒段: %s", kind)
	}
	rows, err := s.db.Query(`SELECT ` + reminderCols + ` FROM reminders WHERE deleted_at = 0 AND done_at IS NULL AND notify = 1 AND ` + col + ` IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reminder
	for rows.Next() {
		r, err := scanReminder(func(dst ...any) error { return rows.Scan(dst...) })
		if err != nil {
			return nil, err
		}
		due := r.DueUnix()
		if due >= fromUnix && due <= toUnix {
			out = append(out, *r)
		}
	}
	return out, rows.Err()
}

// MarkReminderNotified 记录某提醒段已发送（kind 同上）。
func (s *Store) MarkReminderNotified(id int64, kind string, at int64) error {
	col := ""
	switch kind {
	case "1h":
		col = "notify_1h_at"
	case "due":
		col = "notify_due_at"
	case "nextday":
		col = "notify_nextday_at"
	default:
		return fmt.Errorf("未知提醒段: %s", kind)
	}
	_, err := s.db.Exec(`UPDATE reminders SET `+col+` = ? WHERE id = ?`, at, id)
	return err
}
