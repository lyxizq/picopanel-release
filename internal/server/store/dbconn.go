package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// MaxDBConnections 数据库连接上限。
const MaxDBConnections = 16

// DBConnTypes 支持的数据库类型（323）。
var DBConnTypes = []string{"mysql", "postgres", "sqlite"}

// DBConnParams 连接扩展参数（params 列 JSON）。
type DBConnParams struct {
	SSL            string `json:"ssl"`              // off（默认）/ skip-verify / on
	TimeoutSec     int    `json:"timeout_s"`        // 建连超时秒，0=默认 10
	TunnelServerID int64  `json:"tunnel_server_id"` // >0 = 经该监控服务器的 SSH 隧道连库
}

// DBConnection 对应 db_connections 一行：一个外部数据库连接。Auth 为
// {username,password} JSON 的 AES-GCM 密文（secret.go），store 层出入密文、
// 经 DBConnCredentials 解出明文（内部消费），API 层不得原样外发。
type DBConnection struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // mysql / postgres / sqlite
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"` // sqlite = 库文件绝对路径
	Auth     string `json:"-"`        // 密文 JSON，查询时由 store 填 HasAuth
	Params   DBConnParams
	Readonly bool  `json:"readonly"`
	Enabled  bool  `json:"enabled"`
	Sort     int64 `json:"sort"`

	LastOKAt  int64  `json:"last_ok_at"`
	LastError string `json:"last_error"`
	HasAuth   bool   `json:"has_auth"` // 查询时由 store 填充
}

// ErrDBConnNotFound 连接不存在。
var ErrDBConnNotFound = errors.New("数据库连接不存在")

const dbConnCols = `id, name, type, host, port, database, auth, params, readonly, enabled, sort, last_ok_at, last_error`

func scanDBConnection(row interface{ Scan(...any) error }) (*DBConnection, error) {
	var c DBConnection
	var ro, en int
	var params string
	err := row.Scan(&c.ID, &c.Name, &c.Type, &c.Host, &c.Port, &c.Database, &c.Auth,
		&params, &ro, &en, &c.Sort, &c.LastOKAt, &c.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDBConnNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Params = DBConnParams{SSL: "off", TimeoutSec: 10}
	_ = json.Unmarshal([]byte(params), &c.Params)
	c.Readonly, c.Enabled = ro != 0, en != 0
	c.HasAuth = c.Auth != ""
	return &c, nil
}

// ListDBConnections 全部连接，按手动排序。
func (s *Store) ListDBConnections() ([]DBConnection, error) {
	rows, err := s.db.Query(`SELECT ` + dbConnCols + ` FROM db_connections ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DBConnection
	for rows.Next() {
		c, err := scanDBConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetDBConnection 单连接。
func (s *Store) GetDBConnection(id int64) (*DBConnection, error) {
	return scanDBConnection(s.db.QueryRow(`SELECT `+dbConnCols+` FROM db_connections WHERE id = ?`, id))
}

// CreateDBConnection 新建（凭据明文入参、库内加密）。
func (s *Store) CreateDBConnection(c *DBConnection) (int64, error) {
	c.Name = strings.TrimSpace(c.Name)
	params, _ := json.Marshal(c.Params)
	res, err := s.db.Exec(`INSERT INTO db_connections (name, type, host, port, database, auth, params, readonly, enabled, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(sort),0)+1 FROM db_connections), ?, ?)`,
		c.Name, c.Type, c.Host, c.Port, c.Database, s.seal(c.Auth), string(params),
		boolInt(c.Readonly), boolInt(c.Enabled), time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateDBConnection 更新（Auth 空串=沿用原凭据）。
func (s *Store) UpdateDBConnection(c *DBConnection) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Auth == "" {
		old, err := s.GetDBConnection(c.ID)
		if err != nil {
			return err
		}
		c.Auth = old.Auth
	}
	params, _ := json.Marshal(c.Params)
	_, err := s.db.Exec(`UPDATE db_connections SET name=?, type=?, host=?, port=?, database=?, auth=?, params=?,
		readonly=?, enabled=?, updated_at=? WHERE id=?`,
		c.Name, c.Type, c.Host, c.Port, c.Database, s.seal(c.Auth), string(params),
		boolInt(c.Readonly), boolInt(c.Enabled), time.Now().Unix(), c.ID)
	return err
}

// DeleteDBConnection 删除连接。
func (s *Store) DeleteDBConnection(id int64) error {
	res, err := s.db.Exec(`DELETE FROM db_connections WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrDBConnNotFound
	}
	return nil
}

// RecordDBConnStatus 记录连接健康态：成功才推进 last_ok_at 并清错误（内容
// 变化才写，同 RecordMailAccountStatus 口径）；失败只写最近错误，不碰
// last_ok_at（它专指最近成功，503 修复：原失败路径也推进 last_ok_at 的休眠 bug）。
func (s *Store) RecordDBConnStatus(id int64, ok bool, errMsg string) error {
	now := time.Now().Unix()
	if ok {
		_, err := s.db.Exec(`UPDATE db_connections SET last_ok_at = ?, last_error = '' WHERE id = ? AND (last_ok_at != ? OR last_error != '')`, now, id, now)
		return err
	}
	_, err := s.db.Exec(`UPDATE db_connections SET last_error = ? WHERE id = ? AND last_error != ?`, errMsg, id, errMsg)
	return err
}

// DBConnCredentials 解出连接凭据（store 内部密文 → 明文 JSON → 字段）。
func (s *Store) DBConnCredentials(c *DBConnection) (user, pass string, err error) {
	raw := s.unseal(c.Auth)
	if raw == "" {
		return "", "", nil
	}
	var a struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return "", "", err
	}
	return a.Username, a.Password, nil
}
