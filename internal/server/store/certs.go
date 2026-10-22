package store

import (
	"errors"
	"strings"
	"time"
)

// certsDDL HTTPS 证书监控清单：用户配置域名（+端口），certcheck 每 12 小时
// 拨号取证回写检查结果。notified_on 记录最近一次发提醒的日期（YYYY-MM-DD，
// 当日去重——每天最多一条/域名）；域名唯一，重复添加报错由 API 层转友好提示。
// 注：续签功能已整体下线（2026-10-19，TODO 25），存量库遗留的 auto_renew/
// renew_on/renew_error/challenge 列与 cert_renewals 表不再引用、不做迁移清理。
const certsDDL = `CREATE TABLE IF NOT EXISTS https_certs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	domain TEXT NOT NULL UNIQUE,
	port INTEGER NOT NULL DEFAULT 443,
	not_before INTEGER NOT NULL DEFAULT 0,
	not_after INTEGER NOT NULL DEFAULT 0,
	issuer TEXT NOT NULL DEFAULT '',
	issuer_org TEXT NOT NULL DEFAULT '',
	issued_to TEXT NOT NULL DEFAULT '',
	subject TEXT NOT NULL DEFAULT '',
	last_check INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	notified_on TEXT NOT NULL DEFAULT ''
)`

// Cert 是一张受监控的 HTTPS 证书。
type Cert struct {
	ID         int64  `json:"id"`
	Domain     string `json:"domain"`
	Port       int    `json:"port"`
	NotBefore  int64  `json:"not_before"`
	NotAfter   int64  `json:"not_after"`
	Issuer     string `json:"issuer"`
	IssuerOrg  string `json:"issuer_org"`
	IssuedTo   string `json:"issued_to"`
	Subject    string `json:"subject"`
	LastCheck  int64  `json:"last_check"`
	LastError  string `json:"last_error"`
	NotifiedOn string `json:"notified_on"`
}

// CertResult 一次证书检查的结果（SaveCertCheck 入参，检查失败时字段留零值）。
type CertResult struct {
	NotBefore int64
	NotAfter  int64
	Issuer    string
	IssuerOrg string
	IssuedTo  string
	Subject   string
}

// ErrCertExists 域名已存在（UNIQUE 冲突）。
var ErrCertExists = errors.New("该域名已存在")

// certCols https_certs 的全列清单（ListCerts 与 GetCert 共用，避免两处漂移）。
const certCols = `c.id, c.domain, c.port, c.not_before, c.not_after,
		c.issuer, c.issuer_org, c.issued_to, c.subject, c.last_check, c.last_error,
		c.notified_on`

// scanCert 按 certCols 的列序扫描一行。
func scanCert(scan func(dest ...any) error) (Cert, error) {
	var c Cert
	if err := scan(&c.ID, &c.Domain, &c.Port, &c.NotBefore, &c.NotAfter,
		&c.Issuer, &c.IssuerOrg, &c.IssuedTo, &c.Subject, &c.LastCheck, &c.LastError,
		&c.NotifiedOn); err != nil {
		return Cert{}, err
	}
	return c, nil
}

// ListCerts 全量证书清单（按域名排序）。
func (s *Store) ListCerts() ([]Cert, error) {
	rows, err := s.db.Query(`SELECT ` + certCols + ` FROM https_certs c ORDER BY c.domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cert
	for rows.Next() {
		c, err := scanCert(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCert 单条证书；不存在时 Scan 返回 sql.ErrNoRows，由调用方判定。
func (s *Store) GetCert(id int64) (Cert, error) {
	return scanCert(s.db.QueryRow(`SELECT `+certCols+` FROM https_certs c WHERE c.id = ?`, id).Scan)
}

// CreateCert 新增监控域名（port 为 0 时默认 443）。
func (s *Store) CreateCert(domain string, port int) (int64, error) {
	if port == 0 {
		port = 443
	}
	res, err := s.db.Exec(`INSERT INTO https_certs (domain, port) VALUES (?, ?)`, domain, port)
	if err != nil {
		// modernc.org/sqlite 的唯一约束错误信息含 "UNIQUE constraint failed"
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, ErrCertExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteCert 删除一条监控。
func (s *Store) DeleteCert(id int64) error {
	_, err := s.db.Exec(`DELETE FROM https_certs WHERE id = ?`, id)
	return err
}

// SaveCertCheck 回写一次拨号检查结果（checkErr 非空 = 检查失败，清空结果字段）。
func (s *Store) SaveCertCheck(id int64, r CertResult, checkErr string) error {
	_, err := s.db.Exec(`UPDATE https_certs SET not_before=?, not_after=?, issuer=?, issuer_org=?,
		issued_to=?, subject=?, last_check=?, last_error=? WHERE id = ?`,
		r.NotBefore, r.NotAfter, r.Issuer, r.IssuerOrg, r.IssuedTo, r.Subject,
		time.Now().Unix(), checkErr, id)
	return err
}

// MarkCertNotified 记录今日已发提醒（当日去重）。
func (s *Store) MarkCertNotified(id int64, day string) error {
	_, err := s.db.Exec(`UPDATE https_certs SET notified_on=? WHERE id = ?`, day, id)
	return err
}

// ExpiringCertCount 即将到期（≤ days 天，含已过期）的证书数——顶栏统计条展示。
// 从最后一次成功检查的结果取数；从未检查成功过的不计（last_error 非空或
// not_after=0 时没有可信到期时间）。
func (s *Store) ExpiringCertCount(days int) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM https_certs
		WHERE last_error = '' AND not_after > 0 AND not_after - ? <= ?`,
		time.Now().Unix(), int64(days)*86400).Scan(&n)
	return n, err
}
