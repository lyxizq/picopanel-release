package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestFamilyScopeMigration 家庭成员口径迁移回归（2026-11-03）：老库
// （doc_groups/countdowns 带 UNIQUE(name)、无 user_id 列）升级后补列/重建唯一
// 约束/存量行归管理员空间（user_id=0）且数据不丢，同空间同名仍拒、跨空间同名可建。
func TestFamilyScopeMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mig.db")
	s, err := OpenWithKey(path, filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 打回旧形态：UNIQUE(name)、无 user_id
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE doc_groups`,
		`CREATE TABLE doc_groups (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL)`,
		`INSERT INTO doc_groups (name, created_at) VALUES ('工作', 100)`,
		`DROP TABLE countdowns`,
		`CREATE TABLE countdowns (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL,
  kind        TEXT    NOT NULL DEFAULT 'once',
  lunar       INTEGER NOT NULL DEFAULT 0,
  date        TEXT    NOT NULL,
  lunar_month INTEGER NOT NULL DEFAULT 0,
  lunar_day   INTEGER NOT NULL DEFAULT 0,
  color       TEXT    NOT NULL DEFAULT '',
  sort        INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  UNIQUE(name)
)`,
		`INSERT INTO countdowns (name, kind, date, created_at, updated_at) VALUES ('生日', 'yearly', '2026-01-01', 100, 100)`,
		// 旧库也没有其余 user_id 列（messages/docs/mail_accounts 等），全部模拟掉
				`DROP TABLE docs`,
		`CREATE TABLE docs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  group_id   INTEGER NOT NULL DEFAULT 0,
  title      TEXT    NOT NULL,
  format     TEXT    NOT NULL DEFAULT 'richtext',
  content    TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER NOT NULL DEFAULT 0,
  sort       INTEGER NOT NULL DEFAULT 0
)`,
		`INSERT INTO docs (title, content, created_at, updated_at) VALUES ('存量文档', 'hello', 100, 100)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("准备旧形态失败 %q: %v", q, err)
		}
	}
	db.Close()

	// 重新打开：迁移应补列/重建/搬行
	s2, err := OpenWithKey(path, filepath.Join(dir, "k"))
	if err != nil {
		t.Fatalf("老库升级失败: %v", err)
	}
	defer s2.Close()

	// doc_groups：行还在、归管理员空间、同空间同名仍拒、跨空间同名可建
	g, err := s2.ListDocGroups(0)
	if err != nil || len(g) != 1 || g[0].Name != "工作" {
		t.Fatalf("doc_groups 存量行应保留: %+v %v", g, err)
	}
	if _, err := s2.CreateDocGroup("工作", 0); err == nil {
		t.Fatal("同空间同名分组应被拒")
	}
	if _, err := s2.CreateDocGroup("工作", 7); err != nil {
		t.Fatalf("跨空间同名分组应可建: %v", err)
	}

	// countdowns：行还在、唯一约束已按空间放开
	cs, err := s2.ListCountdownsFor(0)
	if err != nil || len(cs) != 1 || cs[0].Name != "生日" {
		t.Fatalf("countdowns 存量行应保留: %+v %v", cs, err)
	}
	if _, err := s2.CreateCountdown(&Countdown{Name: "生日", Kind: "once", Date: "2026-12-01", UserID: 7, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("跨空间同名倒数日应可建: %v", err)
	}

	// docs：行还在（docs 树接口按空间取）
	_, docs, err := s2.ListDocTree(0)
	if err != nil || len(docs) == 0 {
		t.Fatalf("docs 存量行应保留: %v %v", docs, err)
	}
}

// TestDeleteUserCascadesFamilyScope 删除成员连带清其个人空间数据（2026-11-03）：
// 空间行全删、调度扫描源移除（提醒/会员/邮件账户不再向家庭渠道广播）、
// 用药人员仅解绑（历史归管理员代管）、通讯录共享簿不动。
func TestDeleteUserCascadesFamilyScope(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "del.db"), filepath.Join(dir, "kdel"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	uid, err := s.CreateUser("fam", "pass1234", RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	// 各空间造一行数据 + 绑定用药人员 + 共享通讯录一条
	mk := []struct {
		q string
	}{
		{`INSERT INTO page_groups (user_id, name, created_at) VALUES (?, 'g', 1)`},
		{`INSERT INTO page_links (group_id, user_id, name, url, created_at) VALUES (1, ?, 'l', 'https://x', 1)`},
		{`INSERT INTO countdowns (name, user_id, date, created_at, updated_at) VALUES ('c', ?, '2026-01-01', 1, 1)`},
		{`INSERT INTO docs (user_id, title, created_at, updated_at) VALUES (?, 'd', 1, 1)`},
		{`INSERT INTO doc_groups (user_id, name, created_at) VALUES (?, 'dg', 1)`},
		{`INSERT INTO doc_assets (name, mime, size, user_id, data, created_at) VALUES ('a', 'image/png', 1, ?, x'00', 1)`},
		{`INSERT INTO reminders (user_id, title, due_date, created_at, updated_at) VALUES (?, 'r', '2026-01-01', 1, 1)`},
		{`INSERT INTO reminder_groups (user_id, name, created_at) VALUES (?, 'rg', 1)`},
		{`INSERT INTO memberships (user_id, biz, expire_date, created_at, updated_at) VALUES (?, 'b', '2026-01-01', 1, 1)`},
		{`INSERT INTO messages (type, title, user_id, created_at) VALUES ('reminder', 'm', ?, 1)`},
		{`INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_id, created_at) VALUES ('https://ep/1', 'k', 'a', ?, 1)`},
		{`INSERT INTO mail_accounts (name, email, user_id, created_at, updated_at) VALUES ('acc', 'a@x', ?, 1, 1)`},
		{`INSERT INTO mail_contacts (email, name, created_at) VALUES ('shared@x', 's', 1)`},
	}
	for _, m := range mk {
		if _, err := s.db.Exec(m.q, uid); err != nil {
			t.Fatalf("造数失败 %q: %v", m.q, err)
		}
	}
	var pid int64
	if err := s.db.QueryRow(`INSERT INTO med_persons (name, user_id, created_at, updated_at) VALUES ('绑定人', ?, 1, 1) RETURNING id`, uid).Scan(&pid); err != nil {
		t.Fatalf("建绑定人员失败: %v", err)
	}

	if err := s.DeleteUser(uid); err != nil {
		t.Fatalf("删除用户失败: %v", err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM page_groups WHERE user_id = ?`,
		`SELECT COUNT(*) FROM page_links WHERE user_id = ?`,
		`SELECT COUNT(*) FROM countdowns WHERE user_id = ?`,
		`SELECT COUNT(*) FROM docs WHERE user_id = ?`,
		`SELECT COUNT(*) FROM doc_groups WHERE user_id = ?`,
		`SELECT COUNT(*) FROM doc_assets WHERE user_id = ?`,
		`SELECT COUNT(*) FROM reminders WHERE user_id = ?`,
		`SELECT COUNT(*) FROM reminder_groups WHERE user_id = ?`,
		`SELECT COUNT(*) FROM memberships WHERE user_id = ?`,
		`SELECT COUNT(*) FROM messages WHERE user_id = ?`,
		`SELECT COUNT(*) FROM push_subscriptions WHERE user_id = ?`,
		`SELECT COUNT(*) FROM mail_accounts WHERE user_id = ?`,
	} {
		var n int
		if err := s.db.QueryRow(q, uid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("个人空间数据应随删号清理: %s → %d", q, n)
		}
	}
	// 用药人员仅解绑；共享通讯录不动
	var bound int64
	if err := s.db.QueryRow(`SELECT user_id FROM med_persons WHERE id = ?`, pid).Scan(&bound); err != nil || bound != 0 {
		t.Fatalf("用药人员应解绑保留: %v %v", bound, err)
	}
	var contacts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM mail_contacts WHERE email = 'shared@x'`).Scan(&contacts); err != nil || contacts != 1 {
		t.Fatalf("共享通讯录应保留: %d %v", contacts, err)
	}
}
