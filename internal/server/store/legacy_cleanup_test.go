package store

import (
	"path/filepath"
	"testing"
	"time"
)

// 视频下载模块下线清理（2026-10-23 用户指定）：模拟 v1.0.41 部署库遗留的
// video_dl_tasks 表与 video_dl_cookies* 设置键，升级版本重开库后应被幂等清空，
// 其余设置键不受影响。
func TestLegacyVideoDlCleanupOnOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	keyPath := filepath.Join(t.TempDir(), "key")
	s, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	// 伪造 v1.0.41 遗留物
	if _, err := s.db.Exec(`CREATE TABLE video_dl_tasks (id INTEGER PRIMARY KEY, url TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO video_dl_tasks (url) VALUES ('https://example.com/v')`); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"video_dl_cookies", "video_dl_cookies_bilibili", "video_dl_cookies_douyin"} {
		if err := s.SetSetting(k, "legacy"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetSetting("bili_sessdata", "keep-me"); err != nil { // 邻近前缀的无关键必须保留
		t.Fatal(err)
	}
	s.Close()

	// 升级版本重开：启动迁移应清掉表与键
	s2, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatalf("重开遗留库: %v", err)
	}
	var n int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='video_dl_tasks'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("video_dl_tasks 表应已被 DROP")
	}
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'video_dl_cookies' OR key LIKE 'video_dl_cookies_%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("video_dl_cookies* 设置键应已清空，剩 %d", n)
	}
	if v, ok, err := s2.GetSetting("bili_sessdata"); err != nil || !ok || v != "keep-me" {
		t.Fatalf("无关设置键不应被误删: v=%q ok=%v err=%v", v, ok, err)
	}
	// 幂等：再开一次不报错
	s2.Close()
	s3, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatalf("三开遗留库（幂等）: %v", err)
	}
	s3.Close()
}

// 历史遗留全面清理（TODO 5）：模拟 ALTER 时代升级上来的存量库——https_certs/ai_accounts
// 多出已下线功能的列、cert_renewals 表仍在、settings 残留续签与 SMTP 邮件渠道键、
// ai_accounts 留有电信账户及其总览/主页格子——重开库后应被幂等清空，在役列/键/行
// 一律保留（DDL 时代建表自带续签列的形态由 TestCertsLegacyColumnsStillWork 覆盖）。
func TestLegacySchemaCleanupOnOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	keyPath := filepath.Join(t.TempDir(), "key")
	s, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for _, q := range []string{
		`ALTER TABLE https_certs ADD COLUMN auto_renew INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE https_certs ADD COLUMN renew_on TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE https_certs ADD COLUMN renew_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE https_certs ADD COLUMN challenge TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ai_accounts ADD COLUMN auth_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ai_accounts ADD COLUMN device_id TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE cert_renewals (cert_id INTEGER PRIMARY KEY, cert_pem TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO cert_renewals (cert_id, cert_pem) VALUES (1, 'pem')`,
		`INSERT INTO https_certs (domain, auto_renew, challenge) VALUES ('keep.example', 1, 'http')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// 电信账户（已移除服务商）与在役账户各一，各挂一格总览/主页格子
	var ids = map[string]int64{}
	for _, a := range []struct{ name, provider, token string }{{"电信", "telecom", "tok"}, {"智谱", "zhipu", ""}} {
		res, err := s.db.Exec(`INSERT INTO ai_accounts (name, provider, auth_token, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			a.name, a.provider, a.token, now, now)
		if err != nil {
			t.Fatal(err)
		}
		ids[a.provider], _ = res.LastInsertId()
		for _, tbl := range []string{"dashboard_metrics", "page_metrics"} {
			if _, err := s.db.Exec(`INSERT INTO `+tbl+` (server_id, ai_id, metric, label, created_at) VALUES (0, ?, 'ai_balance', ?, ?)`,
				ids[a.provider], a.name, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	legacyKeys := []string{"acme_account_key", "acme_email", "acme_http_port", "mail_config", "alert_email_on", "cert_email_on"}
	keepKeys := []string{"mail_signature", "alert_wx_on", "cert_wx_on"} // 同前缀/同后缀的在役键必须保留
	for _, k := range append(append([]string{}, legacyKeys...), keepKeys...) {
		if err := s.SetSetting(k, "v-"+k); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	// 升级版本重开：启动清理应删列/删表/删行/删键，在役对象原样
	s2, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatalf("重开遗留库: %v", err)
	}
	for _, c := range []struct{ table, col string }{
		{"https_certs", "auto_renew"}, {"https_certs", "renew_on"}, {"https_certs", "renew_error"},
		{"https_certs", "challenge"}, {"ai_accounts", "auth_token"}, {"ai_accounts", "device_id"},
	} {
		if hasColumn(s2.db, c.table, c.col) {
			t.Fatalf("遗留列 %s.%s 应已被 DROP", c.table, c.col)
		}
	}
	for _, c := range []struct{ table, col string }{{"https_certs", "notified_on"}, {"ai_accounts", "zone_ids"}} {
		if !hasColumn(s2.db, c.table, c.col) {
			t.Fatalf("在役列 %s.%s 不应被误删", c.table, c.col)
		}
	}
	count := func(q string, args ...any) int {
		var n int
		if err := s2.db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if count(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='cert_renewals'`) != 0 {
		t.Fatal("cert_renewals 表应已被 DROP")
	}
	if count(`SELECT COUNT(*) FROM https_certs WHERE domain = 'keep.example'`) != 1 {
		t.Fatal("删列不得丢行：https_certs 既有行应保留")
	}
	if count(`SELECT COUNT(*) FROM ai_accounts WHERE provider = 'telecom'`) != 0 {
		t.Fatal("电信账户行应已删除")
	}
	if count(`SELECT COUNT(*) FROM ai_accounts WHERE id = ?`, ids["zhipu"]) != 1 {
		t.Fatal("在役用量账户不应被误删")
	}
	for _, tbl := range []string{"dashboard_metrics", "page_metrics"} {
		if count(`SELECT COUNT(*) FROM `+tbl+` WHERE ai_id = ?`, ids["telecom"]) != 0 {
			t.Fatalf("%s 引用电信账户的格子应级联删除", tbl)
		}
		if count(`SELECT COUNT(*) FROM `+tbl+` WHERE ai_id = ?`, ids["zhipu"]) != 1 {
			t.Fatalf("%s 引用在役账户的格子不应被误删", tbl)
		}
	}
	for _, k := range legacyKeys {
		if _, ok, _ := s2.GetSetting(k); ok {
			t.Fatalf("遗留设置键 %s 应已清空", k)
		}
	}
	for _, k := range keepKeys {
		if v, ok, err := s2.GetSetting(k); err != nil || !ok || v != "v-"+k {
			t.Fatalf("在役设置键 %s 不应被误删: v=%q ok=%v err=%v", k, v, ok, err)
		}
	}
	// 幂等：再开一次不报错（列/表已不在，全部跳过）
	s2.Close()
	s3, err := OpenWithKey(dbPath, keyPath)
	if err != nil {
		t.Fatalf("三开遗留库（幂等）: %v", err)
	}
	s3.Close()
}
