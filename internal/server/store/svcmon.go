package store

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
)

// 服务监控（366 两级重构，原 358 平铺）：按「服务器 → 监控端口」组织 TCP 拨测。
// svc_servers 记服务器（业务名/地址/检查间隔/启停），svc_ports 记该服务器下要
// 拨测的端口（业务名+端口号）及其最新联通状态。状态仅存最新一份（up/latency/
// checked_at/since 原位覆盖），since 记当前状态的起始时刻，前端据此展示「已持续」；
// 无历史表，拨测历史不留存。状态翻转（通↔不通边界）另落 svc_events（500）：
// 可用率与最近事件由它算；事件属于「当前探测目标」，换服务器地址/换端口
// 协议端口域名即清对应事件，删服务器/端口连带清理，每日保留清理循环滚动删。

const (
	// MaxSvcServers 监控服务器上限。
	MaxSvcServers = 16
	// MaxSvcPortsPerServer 单台服务器监控端口上限。
	MaxSvcPortsPerServer = 16
)

// SvcIntervalMin/Max/Default 服务器级检查间隔边界（秒）。
const (
	SvcIntervalMin     = 10
	SvcIntervalMax     = 3600
	SvcIntervalDefault = 600 // 默认 10 分钟（2026-10-08 用户指定，原 30 秒）
)

// SvcServer 对应 svc_servers 一行。
type SvcServer struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Interval int    `json:"interval_sec"`
	Enabled  bool   `json:"enabled"`
}

// SvcPort 对应 svc_ports 一行。Scheme：''/'tcp'=TCP 握手、'http'/'https'=请求
// 检测（GET 跟随重定向，终态 2xx 算通）；Domain 请求检测域名，空=服务器 host。
type SvcPort struct {
	ID        int64  `json:"id"`
	ServerID  int64  `json:"server_id"`
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Scheme    string `json:"scheme"`
	Domain    string `json:"domain"`
	Up        int    `json:"up"`
	LatencyMs int64  `json:"latency_ms"`
	CheckedAt int64  `json:"checked_at"`
	Since     int64  `json:"since"`
	LastOKAt  int64  `json:"last_ok_at"`
	LastError string `json:"last_error"`
}

// SvcPortSchemeTCP 等协议取值（'' 读出等价 TCP，写侧一律规范化存 'tcp'）。
const SvcPortSchemeTCP = "tcp"

// EffectiveDomain 请求检测的目标域名（端口行域名优先，空回落服务器 host）。
func (p *SvcPort) EffectiveDomain(serverHost string) string {
	if d := strings.TrimSpace(p.Domain); d != "" {
		return d
	}
	return serverHost
}

// SvcPortRow 端口行 + 所属服务器的拨测参数（JOIN 出的调度/检测视图）。
type SvcPortRow struct {
	SvcPort
	SrvName  string // 所属服务器业务名（翻转推送文案用）
	Host     string
	Interval int
	Enabled  bool
}

// ErrSvcServerNotFound 服务器不存在。
var ErrSvcServerNotFound = errors.New("服务监控服务器不存在")

// ErrSvcPortNotFound 端口不存在。
var ErrSvcPortNotFound = errors.New("服务监控端口不存在")

const svcServerCols = `id, name, host, interval_sec, enabled`

func scanSvcServer(row interface{ Scan(...any) error }) (*SvcServer, error) {
	var (
		s       SvcServer
		enabled int
	)
	if err := row.Scan(&s.ID, &s.Name, &s.Host, &s.Interval, &enabled); err != nil {
		return nil, err
	}
	s.Enabled = enabled != 0
	return &s, nil
}

const svcPortCols = `p.id, p.server_id, p.name, p.port, p.scheme, p.domain, p.up, p.latency_ms, p.checked_at, p.since, p.last_ok_at, p.last_error`

// ListSvcServers 全部服务器，按创建顺序。
func (s *Store) ListSvcServers() ([]SvcServer, error) {
	rows, err := s.db.Query(`SELECT ` + svcServerCols + ` FROM svc_servers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SvcServer
	for rows.Next() {
		sv, err := scanSvcServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sv)
	}
	return out, rows.Err()
}

// GetSvcServer 单台服务器。
func (s *Store) GetSvcServer(id int64) (*SvcServer, error) {
	sv, err := scanSvcServer(s.db.QueryRow(`SELECT `+svcServerCols+` FROM svc_servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSvcServerNotFound
	}
	return sv, err
}

// CreateSvcServer 新建服务器。
func (s *Store) CreateSvcServer(sv *SvcServer) (int64, error) {
	sv.Name, sv.Host = strings.TrimSpace(sv.Name), strings.TrimSpace(sv.Host)
	res, err := s.db.Exec(`INSERT INTO svc_servers (name, host, interval_sec, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sv.Name, sv.Host, sv.Interval, boolInt(sv.Enabled), time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSvcServer 更新服务器；host 变更时旧端口状态失真（拨的对端换了机器），
// 其全部端口检测态重置回「未检测」，由下一轮拨测重建。
func (s *Store) UpdateSvcServer(sv *SvcServer) error {
	sv.Name, sv.Host = strings.TrimSpace(sv.Name), strings.TrimSpace(sv.Host)
	old, err := s.GetSvcServer(sv.ID)
	if err != nil {
		return err
	}
	if old.Host != sv.Host {
		if err := s.resetSvcServerStates(sv.ID); err != nil {
			return err
		}
		// 换址=全部端口的探测目标都变了，翻转事件一并清空（事件只描述当前目标）
		if _, err := s.db.Exec(`DELETE FROM svc_events WHERE server_id = ?`, sv.ID); err != nil {
			return err
		}
	}
	_, err = s.db.Exec(`UPDATE svc_servers SET name=?, host=?, interval_sec=?, enabled=?, updated_at=? WHERE id=?`,
		sv.Name, sv.Host, sv.Interval, boolInt(sv.Enabled), time.Now().Unix(), sv.ID)
	return err
}

// DeleteSvcServer 删除服务器及其全部端口行（状态随行删除）。
func (s *Store) DeleteSvcServer(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM svc_events WHERE server_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM svc_ports WHERE server_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	res, err := tx.Exec(`DELETE FROM svc_servers WHERE id = ?`, id)
	if err != nil {
		tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		return ErrSvcServerNotFound
	}
	return tx.Commit()
}

// resetSvcServerStates 整台服务器的端口检测态归「未检测」。
func (s *Store) resetSvcServerStates(serverID int64) error {
	_, err := s.db.Exec(`UPDATE svc_ports SET up=-1, latency_ms=0, checked_at=0, since=0, last_ok_at=0, last_error='', updated_at=? WHERE server_id=?`,
		time.Now().Unix(), serverID)
	return err
}

// ListSvcPortRows 全部端口行 JOIN 服务器参数，按服务器创建序 + 端口录入序。
func (s *Store) ListSvcPortRows() ([]SvcPortRow, error) {
	rows, err := s.db.Query(`SELECT ` + svcPortCols + `, s.name, s.host, s.interval_sec, s.enabled
		FROM svc_ports p JOIN svc_servers s ON p.server_id = s.id
		ORDER BY s.id, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SvcPortRow
	for rows.Next() {
		var r SvcPortRow
		var enabled int
		if err := rows.Scan(&r.ID, &r.ServerID, &r.Name, &r.Port, &r.Scheme, &r.Domain, &r.Up, &r.LatencyMs, &r.CheckedAt, &r.Since, &r.LastOKAt, &r.LastError,
			&r.SrvName, &r.Host, &r.Interval, &enabled); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetSvcPortRow 单端口行（带服务器参数）。
func (s *Store) GetSvcPortRow(id int64) (*SvcPortRow, error) {
	var r SvcPortRow
	var enabled int
	err := s.db.QueryRow(`SELECT `+svcPortCols+`, s.name, s.host, s.interval_sec, s.enabled
		FROM svc_ports p JOIN svc_servers s ON p.server_id = s.id WHERE p.id = ?`, id).
		Scan(&r.ID, &r.ServerID, &r.Name, &r.Port, &r.Scheme, &r.Domain, &r.Up, &r.LatencyMs, &r.CheckedAt, &r.Since, &r.LastOKAt, &r.LastError,
			&r.SrvName, &r.Host, &r.Interval, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSvcPortNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	return &r, nil
}

// CreateSvcPort 新建端口行（初始未检测态，状态由首轮检测写入；协议缺省存 tcp）。
func (s *Store) CreateSvcPort(p *SvcPort) (int64, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Scheme == "" {
		p.Scheme = SvcPortSchemeTCP
	}
	p.Domain = strings.TrimSpace(p.Domain)
	res, err := s.db.Exec(`INSERT INTO svc_ports (server_id, name, port, scheme, domain, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, p.ServerID, p.Name, p.Port, p.Scheme, p.Domain, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateSvcPort 更新端口行；port/协议/域名任一变更都会使旧检测态失真，
// 重置回「未检测」（域名变更=换了探测目标，同口径）。
func (s *Store) UpdateSvcPort(p *SvcPort) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Scheme == "" {
		p.Scheme = SvcPortSchemeTCP
	}
	p.Domain = strings.TrimSpace(p.Domain)
	old, err := s.GetSvcPortRow(p.ID)
	if err != nil {
		return err
	}
	if old.Port != p.Port || old.Scheme != p.Scheme || old.Domain != p.Domain {
		if _, err := s.db.Exec(`UPDATE svc_ports SET up=-1, latency_ms=0, checked_at=0, since=0, last_ok_at=0, last_error='', updated_at=? WHERE id=?`,
			time.Now().Unix(), p.ID); err != nil {
			return err
		}
		// 换探测目标（端口/协议/域名任一）翻转事件一并清空（同换址口径）
		if _, err := s.db.Exec(`DELETE FROM svc_events WHERE port_id = ?`, p.ID); err != nil {
			return err
		}
	}
	_, err = s.db.Exec(`UPDATE svc_ports SET name=?, port=?, scheme=?, domain=?, updated_at=? WHERE id=?`,
		p.Name, p.Port, p.Scheme, p.Domain, time.Now().Unix(), p.ID)
	return err
}

// DeleteSvcPort 删除端口行（翻转事件连带清理）。
func (s *Store) DeleteSvcPort(id int64) error {
	res, err := s.db.Exec(`DELETE FROM svc_ports WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSvcPortNotFound
	}
	_, err = s.db.Exec(`DELETE FROM svc_events WHERE port_id = ?`, id)
	return err
}

// CountSvcPorts 服务器下端口数（上限校验用）。
func (s *Store) CountSvcPorts(serverID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM svc_ports WHERE server_id = ?`, serverID).Scan(&n)
	return n, err
}

// RecordSvcCheck 写入一次拨测结果：延迟/checked_at 每轮覆盖；last_ok_at 只在
// 成功时推进；since 仅状态变化时重置为本次时刻（UPDATE 内引用旧 up 比较）。
// 状态行与翻转事件（500）同一事务落库——拆两条写崩溃时可能只写一半（状态翻
// 了没事件→可用率曲线缺口）。返回本次是否发生状态翻转（旧态 -1 未检测不算，
// 行不存在=刚被删，无状态可写也无事件可落）。翻转判定以事务内读到的旧值为准。
func (s *Store) RecordSvcCheck(id int64, up bool, latencyMs int64, errMsg string) (bool, error) {
	now := time.Now().Unix()
	upVal := 0
	if up {
		upVal = 1
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var old, serverID int64
	if err := tx.QueryRow(`SELECT up, server_id FROM svc_ports WHERE id = ?`, id).Scan(&old, &serverID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE svc_ports SET
		up = ?, latency_ms = ?, checked_at = ?,
		last_ok_at = CASE WHEN ? = 1 THEN ? ELSE last_ok_at END,
		last_error = ?,
		since = CASE WHEN up != ? THEN ? ELSE since END
		WHERE id = ?`, upVal, latencyMs, now, upVal, now, errMsg, upVal, now, id); err != nil {
		return false, err
	}
	flipped := old != -1 && old != int64(upVal)
	if flipped {
		if _, err := tx.Exec(`INSERT INTO svc_events (port_id, server_id, up, latency_ms, error, occurred_at)
			VALUES (?, ?, ?, ?, ?, ?)`, id, serverID, upVal, latencyMs, errMsg, now); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return flipped, nil
}

// SvcEvent 是一次端口状态翻转（500）：只在 通↔不通 边界落行（未检测态 -1 的
// 首轮检测不算），可用率与「最近事件」都由这张表算出。事件由 RecordSvcCheck
// 与状态行同事务写入，无独立写入入口。
type SvcEvent struct {
	ID         int64  `json:"id"`
	PortID     int64  `json:"port_id"`
	ServerID   int64  `json:"server_id"`
	Up         int    `json:"up"`
	LatencyMs  int64  `json:"latency_ms"`
	Error      string `json:"error"`
	OccurredAt int64  `json:"occurred_at"`
}

// SvcEventRow 翻转事件 + 服务器/端口展示字段（JOIN 出）。
type SvcEventRow struct {
	SvcEvent
	ServerName string `json:"server_name"`
	PortName   string `json:"port_name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Scheme     string `json:"scheme"`
	Domain     string `json:"domain"`
}

const svcEventRowCols = `e.id, e.port_id, e.server_id, e.up, e.latency_ms, e.error, e.occurred_at,
	s.name, p.name, s.host, p.port, p.scheme, p.domain`

func scanSvcEventRow(row interface{ Scan(...any) error }) (*SvcEventRow, error) {
	var r SvcEventRow
	if err := row.Scan(&r.ID, &r.PortID, &r.ServerID, &r.Up, &r.LatencyMs, &r.Error, &r.OccurredAt,
		&r.ServerName, &r.PortName, &r.Host, &r.Port, &r.Scheme, &r.Domain); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListSvcEvents 最近翻转事件（新→旧，limit 夹紧 1~200）；JOIN 现存服务器/端口，
// 级联清理没接上的残留行自动不出现。
func (s *Store) ListSvcEvents(limit int) ([]SvcEventRow, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT ` + svcEventRowCols + `
		FROM svc_events e
		JOIN svc_servers s ON e.server_id = s.id
		JOIN svc_ports p ON e.port_id = p.id
		ORDER BY e.occurred_at DESC, e.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SvcEventRow
	for rows.Next() {
		r, err := scanSvcEventRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// SvcPortAvail 单端口窗口可用率。
type SvcPortAvail struct {
	PortID   int64   `json:"port_id"`
	TotalSec int64   `json:"total_sec"`
	DownSec  int64   `json:"down_sec"`
	Pct      float64 `json:"pct"`
}

// SvcAvailability 逐端口计算窗口（days 天，1~90）内可用率：down 区间=不通事件
// 起到该端口下一条事件（或 now）止，跨界初态用窗口前最后一条事件定（无记录按通）。
// 只对现存端口出数；窗口内无事件且初态按通的端口不出行（前端显示 '-'）。
func (s *Store) SvcAvailability(days int) ([]SvcPortAvail, error) {
	if days < 1 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	now := time.Now().Unix()
	winStart := now - int64(days)*86400
	rows, err := s.db.Query(`SELECT e.port_id, e.up, e.occurred_at
		FROM svc_events e JOIN svc_ports p ON e.port_id = p.id
		ORDER BY e.port_id, e.occurred_at, e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type ev struct {
		up int
		at int64
	}
	perPort := map[int64][]ev{}
	var order []int64
	for rows.Next() {
		var portID, at int64
		var up int
		if err := rows.Scan(&portID, &up, &at); err != nil {
			return nil, err
		}
		if _, ok := perPort[portID]; !ok {
			order = append(order, portID)
		}
		perPort[portID] = append(perPort[portID], ev{up, at})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	total := now - winStart
	out := make([]SvcPortAvail, 0, len(order))
	for _, portID := range order {
		evs := perPort[portID]
		// 初态：窗口前最后一条事件（无记录按通）；窗口前的事件只用来定初态
		state := 1
		segStart := winStart
		i := 0
		for ; i < len(evs) && evs[i].at < winStart; i++ {
			state = evs[i].up
		}
		var down int64
		clipped := false // 窗口内是否真的有过事件（纯历史态也给出可用率口径）
		for ; i < len(evs); i++ {
			clipped = true
			start := segStart
			if start < winStart {
				start = winStart
			}
			if state == 0 && evs[i].at > start {
				down += evs[i].at - start
			}
			state = evs[i].up
			segStart = evs[i].at
		}
		if !clipped {
			// 窗口内无事件：初态由窗口前记录推出，按「整段延续该状态」计
			if state != 0 {
				continue // 一直通：满可用率无信息量，不出行（前端显示 '-'）
			}
		}
		// 尾段（含窗口内全程无事件时的整段）
		start := segStart
		if start < winStart {
			start = winStart
		}
		if state == 0 && now > start {
			down += now - start
		}
		if total <= 0 {
			continue
		}
		pct := 100 * (1 - float64(down)/float64(total))
		out = append(out, SvcPortAvail{PortID: portID, TotalSec: total, DownSec: down, Pct: math.Round(pct*100) / 100})
	}
	return out, nil
}

// DeleteSvcEventsBefore 清理 ts 之前的翻转事件（数据保留清理循环用），返回删除行数。
func (s *Store) DeleteSvcEventsBefore(ts int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM svc_events WHERE occurred_at < ?`, ts)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
