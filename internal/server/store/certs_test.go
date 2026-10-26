package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openCertTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "certs.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestCertCRUD 证书清单增删查：创建默认端口、回写检查结果、唯一约束、删除。
func TestCertCRUD(t *testing.T) {
	s := openCertTestStore(t)

	id, err := s.CreateCert("example.com", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCert("example.com", 0); !errors.Is(err, ErrCertExists) {
		t.Fatalf("重复域名应报 ErrCertExists，得到 %v", err)
	}

	certs, err := s.ListCerts()
	if err != nil || len(certs) != 1 {
		t.Fatalf("查询清单: %v %d", err, len(certs))
	}
	if certs[0].Domain != "example.com" || certs[0].Port != 443 {
		t.Fatalf("默认端口应为 443，得到 %+v", certs[0])
	}

	// 回写一次成功检查 + 标记今日已提醒
	now := time.Now().Unix()
	if err := s.SaveCertCheck(id, CertResult{
		NotBefore: now - 90*86400, NotAfter: now + 20*86400,
		Issuer: "R11", IssuerOrg: "Let's Encrypt", IssuedTo: "example.com", Subject: "example.com",
	}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkCertNotified(id, time.Now().Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	certs, _ = s.ListCerts()
	c := certs[0]
	if c.Issuer != "R11" || c.IssuerOrg != "Let's Encrypt" || c.IssuedTo != "example.com" || c.NotifiedOn == "" {
		t.Fatalf("回写结果不符: %+v", c)
	}

	// 20 天后到期：不在 15 天窗口内，不计入到期数
	if n, err := s.ExpiringCertCount(15); err != nil || n != 0 {
		t.Fatalf("15 天窗口应计 0: %d %v", n, err)
	}

	// 检查失败（last_error）与未检查（not_after=0）的不计入
	id2, _ := s.CreateCert("expired.com", 8443)
	if err := s.SaveCertCheck(id2, CertResult{
		NotBefore: now - 90*86400, NotAfter: now - 1*86400,
		Issuer: "R11", IssuedTo: "expired.com", Subject: "expired.com",
	}, ""); err != nil {
		t.Fatal(err)
	}
	id3, _ := s.CreateCert("unchecked.com", 0)
	_ = id3
	if n, err := s.ExpiringCertCount(15); err != nil || n != 1 {
		t.Fatalf("已过期应计 1: %d %v", n, err)
	}

	if err := s.DeleteCert(id2); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ExpiringCertCount(15); n != 0 {
		t.Fatalf("删除后应计 0，得到 %d", n)
	}
}

// 存量库兼容回归（续签下线 TODO 25 + 遗留清理 TODO 5）：已部署库的 https_certs 带续签
// 四列（auto_renew/renew_on/renew_error/challenge）与 cert_renewals 表——新代码打开
// 时应把四列与续签表删掉、保留列的数据与读写不受影响。
func TestCertsLegacyColumnsStillWork(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := OpenWithKey(dbPath, filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	// 模拟续签时代的旧结构：保留列 + 续签表齐全
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE https_certs`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE https_certs (
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
		notified_on TEXT NOT NULL DEFAULT '',
		auto_renew INTEGER NOT NULL DEFAULT 0,
		renew_on TEXT NOT NULL DEFAULT '',
		renew_error TEXT NOT NULL DEFAULT '',
		challenge TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO https_certs (domain, auto_renew, challenge) VALUES ('legacy.com', 1, 'dns')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE cert_renewals (
		cert_id INTEGER PRIMARY KEY,
		cert_pem TEXT NOT NULL DEFAULT '',
		key_pem TEXT NOT NULL DEFAULT '',
		issued_at INTEGER NOT NULL DEFAULT 0,
		not_after INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// 新代码打开旧库：清单/检查回写/删除全部可用
	s2, err := OpenWithKey(dbPath, filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	certs, err := s2.ListCerts()
	if err != nil {
		t.Fatalf("旧结构（含续签列）清单查询应可用: %v", err)
	}
	if len(certs) != 1 || certs[0].Domain != "legacy.com" {
		t.Fatalf("清单不符: %+v", certs)
	}
	// 启动清理：续签四列与 cert_renewals 表应已删除，保留列仍在
	for _, col := range []string{"auto_renew", "renew_on", "renew_error", "challenge"} {
		if hasColumn(s2.db, "https_certs", col) {
			t.Fatalf("遗留列 https_certs.%s 应已被 DROP", col)
		}
	}
	if !hasColumn(s2.db, "https_certs", "notified_on") {
		t.Fatal("保留列 https_certs.notified_on 不应被误删")
	}
	var n int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='cert_renewals'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("cert_renewals 表应已被 DROP")
	}
	if err := s2.SaveCertCheck(certs[0].ID, CertResult{NotAfter: time.Now().Unix() + 86400, Issuer: "R11"}, ""); err != nil {
		t.Fatalf("旧结构检查回写应可用: %v", err)
	}
	if err := s2.DeleteCert(certs[0].ID); err != nil {
		t.Fatalf("旧结构删除应可用: %v", err)
	}
}
