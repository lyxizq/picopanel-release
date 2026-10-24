package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"lz-monitor/internal/proto"
)

// Server 对应 servers 表一行。NetIfaces 为 Dashboard 展示的网口（逗号分隔，空=全部）；
// AgentAddr 为 agent 指标端口（如 192.168.1.5:19100），ssh 模式下为 SSH 地址（host[:22]）。
// Token/AgentAddr/SSHUser/SSHAddr/SSHPassword 五个敏感字段在库中 AES-256-GCM 加密
// （见 secret.go），本结构持有的是解密后明文；API 层对 Token/SSHPassword 打码不回显。
type Server struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Token        string `json:"token"`
	AgentAddr    string `json:"agent_addr"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Kernel       string `json:"kernel"`
	Arch         string `json:"arch"`
	CPUModel     string `json:"cpu_model"`
	CPUCores     int    `json:"cpu_cores"`
	Enabled      bool   `json:"enabled"`
	NetIfaces    string `json:"net_ifaces"`
	LastReportAt int64  `json:"last_report_at"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	CollectMode  string `json:"collect_mode"` // 'agent' | 'ssh' | 'push' | 'node'（node=node_exporter 拉取）
	SSHUser      string `json:"ssh_user"`
	SSHKeyPath   string `json:"ssh_key_path"`  // 服务端本机私钥路径，非密钥内容
	SSHAddr      string `json:"ssh_addr"`      // Web 终端/SFTP 专用 SSH 地址（空=ssh 模式回退 AgentAddr）
	SSHPassword  string `json:"ssh_password"`  // 密码认证（空=私钥认证；库中加密，API 不回显）
	SSHHostKey   string `json:"-"`             // TOFU 指纹，不回显（见 SetHostKey）
	GroupID      int64  `json:"group_id"`      // 所属分组（server_groups.id；0=未分组）
	PurchasedOn  string `json:"purchased_on"`  // 购买日期（YYYY-MM-DD；空=未配置）
	ExpiresOn    string `json:"expires_on"`    // 到期日期（YYYY-MM-DD；空=未配置，前端展示「永久」）
	Remark       string `json:"remark"`        // 备注（用途/位置等；悬停服务器名时第二行展示）
	Icon         string `json:"icon"`          // 图标 URL（名称前展示；空=不显示）
	AgentVersion string `json:"agent_version"` // agent 版本串（396 随 Sys 上报；空=旧版 agent 未带）
}

func scanServer(row interface{ Scan(...any) error }, sec *secretBox) (*Server, error) {
	var s Server
	err := row.Scan(&s.ID, &s.Name, &s.Token, &s.AgentAddr, &s.Hostname, &s.OS, &s.Kernel, &s.Arch,
		&s.CPUModel, &s.CPUCores, &s.Enabled, &s.NetIfaces, &s.LastReportAt, &s.CreatedAt, &s.UpdatedAt,
		&s.CollectMode, &s.SSHUser, &s.SSHKeyPath, &s.SSHAddr, &s.SSHPassword, &s.SSHHostKey, &s.GroupID,
		&s.PurchasedOn, &s.ExpiresOn, &s.Remark, &s.Icon, &s.AgentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// 敏感字段解密（历史明文原样返回；sec 为 nil 时不处理，兼容测试直连）
	if sec != nil {
		s.Token = sec.open(s.Token)
		s.AgentAddr = sec.open(s.AgentAddr)
		s.SSHUser = sec.open(s.SSHUser)
		s.SSHAddr = sec.open(s.SSHAddr)
		s.SSHPassword = sec.open(s.SSHPassword)
	}
	return &s, nil
}

// serverCols 中 token 用 COALESCE：旧库（v0.1 UNIQUE 约束）存 NULL 表示"未设置"，
// SQLite 允许多个 NULL，统一读成空串。
const serverCols = `id, name, COALESCE(token,'') AS token, agent_addr, hostname,
	os, kernel, arch, cpu_model, cpu_cores,
	enabled, net_ifaces, last_report_at, created_at, updated_at,
	collect_mode, ssh_user, ssh_key_path, ssh_addr, ssh_password, ssh_host_key, group_id, purchased_on, expires_on, remark, icon, agent_version`

// GetServer 按 ID 查服务器，未命中返回 (nil, nil)。
func (s *Store) GetServer(id int64) (*Server, error) {
	return scanServer(s.db.QueryRow(`SELECT `+serverCols+` FROM servers WHERE id = ?`, id), s.sec)
}

// ListServers 返回全部服务器（含停用），按 id 排序。
func (s *Store) ListServers() ([]Server, error) {
	rows, err := s.db.Query(`SELECT ` + serverCols + ` FROM servers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Server
	for rows.Next() {
		srv, err := scanServer(rows, s.sec)
		if err != nil {
			return nil, err
		}
		list = append(list, *srv)
	}
	return list, rows.Err()
}

// EnabledServers 返回启用中的服务器（离线检测用）。
func (s *Store) EnabledServers() ([]Server, error) {
	rows, err := s.db.Query(`SELECT ` + serverCols + ` FROM servers WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Server
	for rows.Next() {
		srv, err := scanServer(rows, s.sec)
		if err != nil {
			return nil, err
		}
		list = append(list, *srv)
	}
	return list, rows.Err()
}

// ServerLite 服务器行的运行态轻量视图：不含任何解密字段，统计/调度/巡检
// 等高频只读路径用（此前这些路径走 ListServers，每行 5 个敏感字段全部解密
// 纯属浪费）。TokenSet 由密文非空判定（seal 空串原样返回 + 启动全量加密
// 迁移，密文非空 ⇔ 已配置，见 secret.go）。
type ServerLite struct {
	ID           int64
	Name         string
	Enabled      bool
	CollectMode  string
	AgentVersion string
	LastReportAt int64
	TokenSet     bool // 已配置抓取/上报密钥（密文非空判定，不解密）
	HasAddr      bool // 已配置指标端口地址（密文非空判定，不解密）
}

// ListServerLites 全部（enabledOnly=true 仅启用中）服务器的运行态轻量行，按 id 排序。
func (s *Store) ListServerLites(enabledOnly bool) ([]ServerLite, error) {
	q := `SELECT id, name, enabled, collect_mode, agent_version, last_report_at,
		COALESCE(token,'') != '', COALESCE(agent_addr,'') != '' FROM servers`
	if enabledOnly {
		q += ` WHERE enabled = 1`
	}
	rows, err := s.db.Query(q + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []ServerLite
	for rows.Next() {
		var l ServerLite
		var enabled, tokenSet, hasAddr int
		if err := rows.Scan(&l.ID, &l.Name, &enabled, &l.CollectMode, &l.AgentVersion, &l.LastReportAt, &tokenSet, &hasAddr); err != nil {
			return nil, err
		}
		l.Enabled, l.TokenSet, l.HasAddr = enabled != 0, tokenSet != 0, hasAddr != 0
		list = append(list, l)
	}
	return list, rows.Err()
}

// GetServerByName 按名称查服务器（名称建表有 UNIQUE 约束），未命中返回 (nil, nil)。
func (s *Store) GetServerByName(name string) (*Server, error) {
	return scanServer(s.db.QueryRow(`SELECT `+serverCols+` FROM servers WHERE name = ?`, name), s.sec)
}

// CreateServer 创建 agent 拉取模式的服务器条目。agentAddr 为必填的指标端口地址；
// token 为可选共享密钥（留空则不鉴权，与 agent.yaml 保持一致；可多台服务器共用同一密钥）；
// groupID 为所属分组（0=未分组），调用方负责校验分组存在。
func (s *Store) CreateServer(name, agentAddr, token string, groupID int64) (*Server, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO servers (name, agent_addr, token, group_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		name, s.seal(agentAddr), tokenOrNull(s.seal(token)), groupID, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetServer(id)
}

// CreateNodeServer 创建 node_exporter 拉取模式的服务器条目。addr 为 exporter
// 地址（host[:10200]），token 为可选密钥（经 X-Server-Token 发送，语义与
// agent 拉取模式相同：exporter 本体不校验，供前置反代校验）。
func (s *Store) CreateNodeServer(name, addr, token string, groupID int64) (*Server, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO servers (name, agent_addr, token, collect_mode, group_id, created_at, updated_at)
		 VALUES (?, ?, ?, 'node', ?, ?, ?)`,
		name, s.seal(addr), tokenOrNull(s.seal(token)), groupID, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetServer(id)
}

// ErrTokenUsed 上报密钥已被另一台 push 服务器占用（一对一约束）。
var ErrTokenUsed = errors.New("token already used")

// CreatePushServer 创建上报模式的服务器条目：无地址（服务端不主动连接），
// 由 agent 携带 token POST 上报。token 是身份标识、与服务器一对一，
// 已被其他 push 服务器使用时返回 ErrTokenUsed。
func (s *Store) CreatePushServer(name, token string, groupID int64) (*Server, error) {
	used, err := s.pushTokenUsed(token, 0)
	if err != nil {
		return nil, err
	}
	if used {
		return nil, ErrTokenUsed
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO servers (name, agent_addr, token, collect_mode, group_id, created_at, updated_at)
		 VALUES (?, '', ?, 'push', ?, ?, ?)`,
		name, tokenOrNull(s.seal(token)), groupID, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetServer(id)
}

// pushTokenUsed 检查 token 是否已被其他 push 服务器占用（空串不占用；
// agent 拉取模式的共享抓取密钥不算——两模式互不影响）。
// token 加密存储（同明文每次密文不同），无法 SQL 等值查找，改为全量读出
// 解密后内存比较——push 服务器数量级极小，开销可忽略。
func (s *Store) pushTokenUsed(token string, excludeID int64) (bool, error) {
	if token == "" {
		return false, nil
	}
	rows, err := s.db.Query(`SELECT id, COALESCE(token,'') FROM servers
		WHERE collect_mode = 'push' AND COALESCE(token,'') != ''`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var stored string
		if err := rows.Scan(&id, &stored); err != nil {
			return false, err
		}
		if id != excludeID && s.unseal(stored) == token {
			return true, nil
		}
	}
	return false, rows.Err()
}

// PushToken 上报密钥与其所属服务器 ID。
type PushToken struct {
	ID    int64
	Token string
}

// PushTokenList 列出全部 push 模式服务器的上报密钥，供上报端点在内存中做
// 恒时比较（DB 等值查找的耗时随匹配程度变化，理论上可被时序侧信道逐字节
// 探测；ConstantTimeCompare 抹平）。push 服务器数量级极小，每次上报全量
// 查询一遍可忽略。
func (s *Store) PushTokenList() ([]PushToken, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(token,'') FROM servers
		WHERE collect_mode = 'push' AND COALESCE(token,'') != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushToken
	for rows.Next() {
		var t PushToken
		if err := rows.Scan(&t.ID, &t.Token); err != nil {
			return nil, err
		}
		t.Token = s.unseal(t.Token)
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetHostKey 记录/更新 SSH 主机指纹（TOFU：首次连接回填；用户重置后重新学习）。
func (s *Store) SetHostKey(id int64, fingerprint string) error {
	_, err := s.db.Exec(`UPDATE servers SET ssh_host_key = ?, updated_at = ? WHERE id = ?`,
		fingerprint, time.Now().Unix(), id)
	return err
}

// tokenOrNull 空密钥写 NULL：既表达"未设置"，又规避旧库 token 列的 UNIQUE 约束
// （SQLite 中多个 NULL 不冲突）。
func tokenOrNull(token string) any {
	if token == "" {
		return nil
	}
	return token
}

// UpsertOnReport 收到上报/抓取结果时更新系统信息与 last_report_at。
// agent_version 用 NULLIF 忽略空串（2026-10-06 Fnos 现场教训）：同机多进程
// 老新混跑时，老 agent（无版本字段，空串）的上报会把新进程刚写入的版本抹回
// 空，列表版本列翻烙饼——空=「本次上报者不知道版本」，保留已知值不回退。
func (s *Store) UpsertOnReport(id int64, sys *proto.SysInfo, now int64) error {
	if sys == nil {
		_, err := s.db.Exec(`UPDATE servers SET last_report_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE servers SET
		hostname = ?, os = ?, kernel = ?, arch = ?, cpu_model = ?, cpu_cores = ?,
		agent_version = COALESCE(NULLIF(?, ''), agent_version),
		last_report_at = ?, updated_at = ?
		WHERE id = ?`,
		sys.Hostname, sys.OS, sys.Kernel, sys.Arch, sys.CPUModel, sys.CPUCores,
		sys.AgentVersion, now, now, id)
	return err
}

// ServerUpdate 是 UpdateServer 的局部更新参数（指针为 nil 表示不修改）。
// ResetHostKey 置 true 时清空已记录的 SSH 主机指纹，下次连接重新学习（目标机重装后用）。
type ServerUpdate struct {
	Name         *string
	Enabled      *bool
	NetIfaces    *string
	AgentAddr    *string
	Token        *string
	CollectMode  *string // 'agent' | 'ssh'
	SSHUser      *string
	SSHKeyPath   *string
	SSHAddr      *string // Web 终端 SSH 地址；空串=清空（ssh 模式回退 AgentAddr）
	SSHPassword  *string // 密码认证；空串=清空（回退私钥）
	ResetHostKey *bool
	GroupID      *int64 // 0=移到未分组
	PurchasedOn  *string
	ExpiresOn    *string // YYYY-MM-DD；空串=清空（到期倒计时随之隐藏）
	Remark       *string // 备注；空串=清空
	Icon         *string // 图标 URL；空串=清空（不显示）
}

// UpdateServer 按 ServerUpdate 局部更新。
func (s *Store) UpdateServer(id int64, u ServerUpdate) error {
	srv, err := s.GetServer(id)
	if err != nil {
		return err
	}
	if srv == nil {
		return sql.ErrNoRows
	}
	if u.Name != nil {
		srv.Name = *u.Name
	}
	if u.Enabled != nil {
		srv.Enabled = *u.Enabled
	}
	if u.NetIfaces != nil {
		srv.NetIfaces = *u.NetIfaces
	}
	if u.AgentAddr != nil {
		srv.AgentAddr = *u.AgentAddr
	}
	if u.Token != nil {
		srv.Token = *u.Token
	}
	if u.CollectMode != nil {
		srv.CollectMode = *u.CollectMode
	}
	if u.SSHUser != nil {
		srv.SSHUser = *u.SSHUser
	}
	if u.SSHKeyPath != nil {
		srv.SSHKeyPath = *u.SSHKeyPath
	}
	if u.SSHAddr != nil {
		srv.SSHAddr = *u.SSHAddr
	}
	if u.SSHPassword != nil {
		srv.SSHPassword = *u.SSHPassword
	}
	if u.GroupID != nil {
		srv.GroupID = *u.GroupID
	}
	if u.PurchasedOn != nil {
		srv.PurchasedOn = *u.PurchasedOn
	}
	if u.ExpiresOn != nil {
		srv.ExpiresOn = *u.ExpiresOn
	}
	if u.Remark != nil {
		srv.Remark = *u.Remark
	}
	if u.Icon != nil {
		srv.Icon = *u.Icon
	}
	hostKey := srv.SSHHostKey
	if u.ResetHostKey != nil && *u.ResetHostKey {
		hostKey = ""
	}
	// push 密钥一对一：更新后若为 push 模式，新密钥不得与其他 push 服务器撞车
	//（agent 拉取模式的共享密钥不受限制）
	if srv.CollectMode == "push" {
		used, err := s.pushTokenUsed(srv.Token, id)
		if err != nil {
			return err
		}
		if used {
			return ErrTokenUsed
		}
	}
	_, err = s.db.Exec(`UPDATE servers SET name = ?, enabled = ?, net_ifaces = ?, agent_addr = ?, token = ?,
		collect_mode = ?, ssh_user = ?, ssh_key_path = ?, ssh_addr = ?, ssh_password = ?, ssh_host_key = ?, group_id = ?,
		purchased_on = ?, expires_on = ?, remark = ?, icon = ?, updated_at = ? WHERE id = ?`,
		srv.Name, srv.Enabled, srv.NetIfaces, s.seal(srv.AgentAddr), tokenOrNull(s.seal(srv.Token)),
		srv.CollectMode, s.seal(srv.SSHUser), srv.SSHKeyPath, s.seal(srv.SSHAddr), s.seal(srv.SSHPassword), hostKey, srv.GroupID,
		srv.PurchasedOn, srv.ExpiresOn, srv.Remark, srv.Icon, time.Now().Unix(), id)
	return err
}

// DeleteServer 删除服务器及其全部关联数据（单事务）。
func (s *Store) DeleteServer(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM metrics WHERE server_id = ?`,
		`DELETE FROM metrics_nets WHERE server_id = ?`,
		`DELETE FROM metrics_disks WHERE server_id = ?`,
		`DELETE FROM metrics_thermal WHERE server_id = ?`,
		`DELETE FROM metrics_archive WHERE server_id = ?`,
		`DELETE FROM server_disks WHERE server_id = ?`,
		`DELETE FROM server_listens WHERE server_id = ?`,
		`DELETE FROM server_apps WHERE server_id = ?`,
		`DELETE FROM alerts WHERE server_id = ?`,
		`DELETE FROM alert_rules WHERE server_id = ?`,
		`DELETE FROM dashboard_charts WHERE server_id = ?`,
		`DELETE FROM dashboard_metrics WHERE server_id = ?`,
		`DELETE FROM page_metrics WHERE server_id = ?`,
		`DELETE FROM term_scripts WHERE server_id = ?`,
		`DELETE FROM file_favs WHERE server_id = ?`,
		`DELETE FROM servers WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return fmt.Errorf("删除服务器 %d: %w", id, err)
		}
	}
	return tx.Commit()
}
