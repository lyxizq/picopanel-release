package store

import (
	"database/sql"
	"errors"
	"time"
)

// Rule 对应 alert_rules 表一行。ServerID 为 nil 表示全局规则。
type Rule struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Metric      string  `json:"metric"`
	Op          string  `json:"op"`
	Threshold   float64 `json:"threshold"`
	DurationSec int64   `json:"duration_sec"`
	ServerID    *int64  `json:"server_id"`
	Param       string  `json:"param"`
	Enabled     bool    `json:"enabled"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

const ruleCols = `id, name, metric, op, threshold, duration_sec, server_id, param, enabled, created_at, updated_at`

func scanRule(row interface{ Scan(...any) error }) (*Rule, error) {
	var r Rule
	var serverID sql.NullInt64
	err := row.Scan(&r.ID, &r.Name, &r.Metric, &r.Op, &r.Threshold, &r.DurationSec,
		&serverID, &r.Param, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if serverID.Valid {
		v := serverID.Int64
		r.ServerID = &v
	}
	return &r, nil
}

// ListRules 返回全部规则，按 id 排序。
func (s *Store) ListRules() ([]Rule, error) {
	rows, err := s.db.Query(`SELECT ` + ruleCols + ` FROM alert_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CreateRule 创建规则。
func (s *Store) CreateRule(r *Rule) error {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO alert_rules
		(name, metric, op, threshold, duration_sec, server_id, param, enabled, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		r.Name, r.Metric, r.Op, r.Threshold, r.DurationSec, r.ServerID, r.Param, r.Enabled, now, now)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	r.CreatedAt, r.UpdatedAt = now, now
	return nil
}

// UpdateRule 更新规则全部业务字段。
func (s *Store) UpdateRule(r *Rule) error {
	_, err := s.db.Exec(`UPDATE alert_rules SET
		name=?, metric=?, op=?, threshold=?, duration_sec=?, server_id=?, param=?, enabled=?, updated_at=?
		WHERE id=?`,
		r.Name, r.Metric, r.Op, r.Threshold, r.DurationSec, r.ServerID, r.Param, r.Enabled,
		time.Now().Unix(), r.ID)
	return err
}

// DeleteRule 删除规则（历史告警保留，rule_id 引用不再展开）。
func (s *Store) DeleteRule(id int64) error {
	_, err := s.db.Exec(`DELETE FROM alert_rules WHERE id = ?`, id)
	return err
}

// Alert 对应 alerts 表一行。
type Alert struct {
	ID          int64   `json:"id"`
	RuleID      int64   `json:"rule_id"`
	ServerID    int64   `json:"server_id"`
	Metric      string  `json:"metric"`
	Op          string  `json:"op"`
	Threshold   float64 `json:"threshold"`
	Value       float64 `json:"value"`
	Detail      string  `json:"detail"`
	Status      string  `json:"status"`
	TriggeredAt int64   `json:"triggered_at"`
	ResolvedAt  *int64  `json:"resolved_at"`
	UpdatedAt   int64   `json:"updated_at"`
	// 展示用联表字段
	ServerName string  `json:"server_name"`
	RuleName   *string `json:"rule_name"`
}

const alertCols = `a.id, a.rule_id, a.server_id, a.metric, a.op, a.threshold, a.value,
	a.detail, a.status, a.triggered_at, a.resolved_at, a.updated_at,
	s.name, r.name`

func scanAlert(row interface{ Scan(...any) error }) (*Alert, error) {
	var a Alert
	var resolvedAt sql.NullInt64
	var ruleName sql.NullString
	err := row.Scan(&a.ID, &a.RuleID, &a.ServerID, &a.Metric, &a.Op, &a.Threshold, &a.Value,
		&a.Detail, &a.Status, &a.TriggeredAt, &resolvedAt, &a.UpdatedAt,
		&a.ServerName, &ruleName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if resolvedAt.Valid {
		v := resolvedAt.Int64
		a.ResolvedAt = &v
	}
	if ruleName.Valid {
		v := ruleName.String
		a.RuleName = &v
	}
	return &a, nil
}

// FindOpenAlert 查某 (规则,服务器) 当前 firing 的告警，无则返回 (nil, nil)。
func (s *Store) FindOpenAlert(ruleID, serverID int64) (*Alert, error) {
	return scanAlert(s.db.QueryRow(`SELECT `+alertCols+` FROM alerts a
		JOIN servers s ON s.id = a.server_id
		LEFT JOIN alert_rules r ON r.id = a.rule_id
		WHERE a.rule_id = ? AND a.server_id = ? AND a.status = 'firing'`, ruleID, serverID))
}

// OpenAlertID 判断某 (规则,服务器) 是否有 firing 告警，返回告警 ID。
// 告警评估热路径（每报告 × 每规则一次）只需要存在性与 ID，
// 走 (rule_id, server_id) firing 部分唯一索引点查，省掉两表 JOIN。
func (s *Store) OpenAlertID(ruleID, serverID int64) (int64, bool, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM alerts
		WHERE rule_id = ? AND server_id = ? AND status = 'firing' LIMIT 1`, ruleID, serverID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// AlertByID 按 ID 查完整告警（含服务器名/规则名），无则返回 (nil, nil)。
// 恢复通知需要全行，但恢复是告警生命周期里的低频事件。
func (s *Store) AlertByID(id int64) (*Alert, error) {
	return scanAlert(s.db.QueryRow(`SELECT `+alertCols+` FROM alerts a
		JOIN servers s ON s.id = a.server_id
		LEFT JOIN alert_rules r ON r.id = a.rule_id
		WHERE a.id = ?`, id))
}

// CreateAlert 写入一条 firing 告警（部分唯一索引兜底防重复）。
func (s *Store) CreateAlert(a *Alert) error {
	res, err := s.db.Exec(`INSERT INTO alerts
		(rule_id, server_id, metric, op, threshold, value, detail, status, triggered_at, resolved_at, updated_at)
		VALUES (?,?,?,?,?,?,?,'firing',?,NULL,?)`,
		a.RuleID, a.ServerID, a.Metric, a.Op, a.Threshold, a.Value, a.Detail,
		a.TriggeredAt, a.UpdatedAt)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

// TouchAlert 更新 firing 告警的观测值与时间。
func (s *Store) TouchAlert(id int64, value float64, now int64) error {
	_, err := s.db.Exec(`UPDATE alerts SET value = ?, updated_at = ? WHERE id = ?`, value, now, id)
	return err
}

// ResolveAlert 恢复告警。
func (s *Store) ResolveAlert(id int64, now int64) error {
	_, err := s.db.Exec(`UPDATE alerts SET status = 'resolved', resolved_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id)
	return err
}

// ListAlerts 分页查询告警（triggered_at 倒序）。status 为 all 时不过滤。
func (s *Store) ListAlerts(status string, serverID int64, page, pageSize int) ([]Alert, int, error) {
	where := ` FROM alerts a JOIN servers s ON s.id = a.server_id
		LEFT JOIN alert_rules r ON r.id = a.rule_id WHERE 1=1`
	args := []any{}
	if status == "firing" || status == "resolved" {
		where += ` AND a.status = ?`
		args = append(args, status)
	}
	if serverID > 0 {
		where += ` AND a.server_id = ?`
		args = append(args, serverID)
	}

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*)`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	q := `SELECT ` + alertCols + where + ` ORDER BY a.triggered_at DESC, a.id DESC LIMIT ? OFFSET `
	rows, err := s.db.Query(q+`?`, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *a)
	}
	return out, total, rows.Err()
}

// FiringCountByServer 返回各服务器当前 firing 告警数。
func (s *Store) FiringCountByServer() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT server_id, COUNT(*) FROM alerts
		WHERE status = 'firing' GROUP BY server_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]int)
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// DeleteResolvedBefore 删除已恢复且早于 ts 的告警，返回删除行数。
func (s *Store) DeleteResolvedBefore(ts int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM alerts WHERE status = 'resolved' AND triggered_at < ?`, ts)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
