package dbtool

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"lz-monitor/internal/server/store"
)

// newSQLiteFile 建一个带两张表（含自增主键/NULL/中文/二进制）的临时 SQLite 库。
func newSQLiteFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, age INTEGER, bio TEXT)`,
		`CREATE TABLE files (id INTEGER PRIMARY KEY, data BLOB, note TEXT)`,
		`CREATE INDEX idx_users_name ON users(name)`,
		`INSERT INTO users (name, age, bio) VALUES ('张三', 30, '工程师'),
			('李四', NULL, NULL), ("O'Brien", 41, 'it''s')`,
		`INSERT INTO files (id, data, note) VALUES (1, X'DEADBEEF', '二进制')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return p
}

func openFile(t *testing.T, path string, readonly bool) *sql.DB {
	t.Helper()
	dsn := "file:" + path
	if readonly {
		dsn += "?mode=ro"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// openStore 临时主库（存 db_connections 配置用）。
func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestSQLiteBrowseBrowse SQLite 全链路：schema/表/详情（列/主键/索引/DDL/行数）。
func TestSQLiteBrowse(t *testing.T) {
	path := newSQLiteFile(t)
	db := openFile(t, path, false)
	d := sqliteDialect{}
	ctx := context.Background()

	schemas, err := d.Schemas(ctx, db)
	if err != nil || len(schemas) != 1 || schemas[0] != "main" {
		t.Fatalf("schemas: %v %v", schemas, err)
	}
	ts, err := d.Tables(ctx, db, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 {
		t.Fatalf("应有 2 张表: %+v", ts)
	}
	det, err := d.Table(ctx, db, "main", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(det.Columns) != 4 || len(det.PK) == 0 || det.PK[0] != "id" {
		t.Fatalf("列/主键: %+v", det)
	}
	if !det.Columns[0].AutoInc {
		t.Fatal("id 应识别为自增")
	}
	if det.RowCount != 3 {
		t.Fatalf("行数应为 3: %d", det.RowCount)
	}
	foundIdx := false
	for _, ix := range det.Indexes {
		if ix.Name == "idx_users_name" && !ix.Auto && !ix.Primary {
			foundIdx = true
		}
	}
	if !foundIdx {
		t.Fatalf("用户索引缺失: %+v", det.Indexes)
	}
	if !strings.Contains(det.DDL, "CREATE TABLE users") {
		t.Fatalf("DDL 异常: %q", det.DDL)
	}
}

// TestSQLiteRowCRUD 数据页与行级增删改。
func TestSQLiteRowCRUD(t *testing.T) {
	path := newSQLiteFile(t)
	db := openFile(t, path, false)
	d := sqliteDialect{}
	ctx := context.Background()

	res, err := PageRows(ctx, db, d, "main", "users", nil, "age", "desc", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 || len(res.Rows) != 2 {
		t.Fatalf("分页: %+v", res)
	}
	// age desc：NULL 在 SQLite 排最后（NULLS LAST 默认 desc 时 NULL 在前? SQLite DESC 把 NULL 放最后）
	// 首行应为 41（O'Brien），且文本引号内容原样
	if res.Rows[0][1] != "O'Brien" {
		t.Fatalf("排序/取值异常: %v", res.Rows[0])
	}
	// 过滤
	res2, err := PageRows(ctx, db, d, "main", "users",
		[]Filter{{Col: "name", Op: "like", Val: "张"}}, "", "", 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Total != 1 || res2.Rows[0][1] != "张三" {
		t.Fatalf("过滤: %+v", res2)
	}
	// 非法列名拒绝
	if _, err := PageRows(ctx, db, d, "main", "users",
		[]Filter{{Col: "name; DROP TABLE users", Op: "eq", Val: "x"}}, "", "", 1, 50); err == nil {
		t.Fatal("非法列名应拒绝")
	}

	// 插入 → 更新 → 删除
	n, id, err := InsertRow(ctx, db, d, "main", "users", map[string]any{"name": "王五", "age": int64(22)})
	if err != nil || n != 1 || id == 0 {
		t.Fatalf("插入: %d %d %v", n, id, err)
	}
	n, err = UpdateRow(ctx, db, d, "main", "users", map[string]any{"id": id}, map[string]any{"age": int64(23)})
	if err != nil || n != 1 {
		t.Fatalf("更新: %d %v", n, err)
	}
	n, err = DeleteRow(ctx, db, d, "main", "users", map[string]any{"id": id})
	if err != nil || n != 1 {
		t.Fatalf("删除: %d %v", n, err)
	}
}

// TestSQLiteDumpImportRoundtrip 备份导出 → 新库导入 → 数据一致。
func TestSQLiteDumpImportRoundtrip(t *testing.T) {
	path := newSQLiteFile(t)
	src := openFile(t, path, false)
	d := sqliteDialect{}
	ctx := context.Background()

	var buf bytes.Buffer
	if err := Dump(ctx, src, d, "main", nil, &buf, "测试库"); err != nil {
		t.Fatal(err)
	}
	dump := buf.String()
	for _, want := range []string{"CREATE TABLE users", "INSERT INTO", "张三", "X'DEADBEEF'", "idx_users_name", "PRAGMA foreign_keys=OFF"} {
		if !strings.Contains(dump, want) {
			t.Fatalf("备份缺 %q", want)
		}
	}

	dstPath := filepath.Join(t.TempDir(), "dst.db")
	dst := openFile(t, dstPath, false)
	n, err := Import(ctx, dst, d, dump, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n < 6 { // 2 DDL + 1 索引 + 2 INSERT + PRAGMA/BEGIN/COMMIT 部分
		t.Fatalf("执行语句过少: %d", n)
	}
	var cnt int
	if err := dst.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&cnt); err != nil || cnt != 3 {
		t.Fatalf("导入后行数: %d %v", cnt, err)
	}
	var blob []byte
	if err := dst.QueryRow(`SELECT data FROM files WHERE id = 1`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(blob, []byte{0xDE, 0xAD, 0xBE, 0xEF}) {
		t.Fatalf("二进制不一致: %x", blob)
	}
	// 自增序号延续（sqlite_sequence 随 DDL AUTOINCREMENT 定义迁移）
	if _, err := dst.Exec(`INSERT INTO users (name) VALUES ('新行')`); err != nil {
		t.Fatal(err)
	}
	var maxID int
	if err := dst.QueryRow(`SELECT MAX(id) FROM users`).Scan(&maxID); err != nil || maxID != 4 {
		t.Fatalf("自增未延续: %d %v", maxID, err)
	}
}

// TestGzipBackupFile 备份走 gzip 包装后可解压还原。
func TestGzipBackupFile(t *testing.T) {
	path := newSQLiteFile(t)
	src := openFile(t, path, false)
	d := sqliteDialect{}
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	if err := Dump(context.Background(), src, d, "main", []string{"users"}, gz, "x"); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(&raw)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "张三") {
		t.Fatal("解压后应含数据")
	}
}

// TestClassify 语句分类与危险判定（d 为空 = sqlite 口径：字符串里 \ 非转义）。
func TestClassify(t *testing.T) {
	cases := []struct {
		sql      string
		d        Dialect
		kind     stmtKind
		confirm  bool
		multiErr bool
		txnErr   bool
	}{
		{sql: "SELECT * FROM t", kind: kindRead},
		{sql: "  -- 查一下\nSELECT 'it''s; ok' FROM t WHERE x = 1;", kind: kindRead},
		{sql: "SHOW TABLES", kind: kindRead},
		{sql: "EXPLAIN SELECT 1", kind: kindRead},
		{sql: "WITH x AS (SELECT 1) SELECT * FROM x", kind: kindRead},
		{sql: "WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x", kind: kindWrite},
		// 写副作用补刀（2026-10-04 安全审查）：INTO/OUTFILE/EXPLAIN ANALYZE
		{sql: "SELECT * FROM t INTO OUTFILE '/tmp/x'", kind: kindWrite},
		{sql: "SELECT * INTO newt FROM t", kind: kindWrite},
		{sql: "WITH x AS (SELECT 1 INTO o) SELECT * FROM x", kind: kindWrite},
		{sql: "EXPLAIN ANALYZE WITH d AS (DELETE FROM t) SELECT * FROM d", kind: kindWrite},
		{sql: "SELECT 'INTO OUTFILE inside string' FROM t", kind: kindRead}, // 字符串里出现不算
		// 反斜杠口径按方言：SQLite 的 \' 不转义 → 分号藏不住（P1 实锤修复回归）；
		// MySQL 的 \' 转义 → 同一文本是单条 SELECT
		{sql: "SELECT 'a\\'; DELETE FROM t; --'", multiErr: true},
		{sql: "SELECT 'a\\'; DELETE FROM t; --'", d: mysqlDialect{}, kind: kindRead},
		{sql: "UPDATE t SET a = 1 WHERE id = 2", kind: kindWrite},
		{sql: "UPDATE t SET a = 1", kind: kindWrite, confirm: true},
		{sql: "DELETE FROM t", kind: kindWrite, confirm: true},
		{sql: "DELETE FROM t /* 注释里的 WHERE */", kind: kindWrite, confirm: true},
		{sql: "DROP TABLE t", kind: kindDDL, confirm: true},
		{sql: "TRUNCATE TABLE t", kind: kindDDL, confirm: true},
		{sql: "BEGIN", txnErr: true},
		{sql: "COMMIT", txnErr: true},
		{sql: "SELECT 1; SELECT 2", multiErr: true},
		{sql: "", multiErr: true},
		{sql: "-- 纯注释", multiErr: true},
	}
	for _, c := range cases {
		d := c.d
		if d == nil {
			d = sqliteDialect{}
		}
		st, err := classify(d, c.sql)
		if c.multiErr {
			if err == nil {
				t.Errorf("%q: 应拒绝", c.sql)
			}
			continue
		}
		if c.txnErr {
			if err == nil {
				t.Errorf("%q: 事务语句应拒绝", c.sql)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.sql, err)
			continue
		}
		if st.kind != c.kind || st.needConfirm != c.confirm {
			t.Errorf("%q: got kind=%d confirm=%v want kind=%d confirm=%v", c.sql, st.kind, st.needConfirm, c.kind, c.confirm)
		}
	}
}

// TestRunConsoleSQLiteHiddenMultiStmt P1 回归（2026-10-04 安全审查）：SQLite
// 字符串里 \ 不是转义，按 MySQL 口径扫描会把分号藏进「未结束的字符串」，
// 分类成单条 SELECT 放行——而 modernc 驱动整串执行全部语句，DELETE 实际跑掉。
// 修复后应按多语句拒绝且数据无损。
func TestRunConsoleSQLiteHiddenMultiStmt(t *testing.T) {
	path := newSQLiteFile(t)
	db := openFile(t, path, false)
	d := sqliteDialect{}
	ctx := context.Background()

	_, err := RunConsole(ctx, db, d, true, "SELECT 'a\\'; DELETE FROM users; --'", 0, false, "")
	if err == nil {
		t.Fatal("藏分号的 SELECT 应被拒绝")
	}
	var cnt int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&cnt); err != nil || cnt != 3 {
		t.Fatalf("数据不应被改动: %d %v", cnt, err)
	}
}

// TestSQLiteDSNEscape SQLite DSN 经 URL 组装并转义：路径含 ?/# 时按 %HH 交给
// SQLite URI 解析（已实证解码），? 不再串进参数区；mode=ro 生效拒写。
func TestSQLiteDSNEscape(t *testing.T) {
	dir := t.TempDir()
	// 目标文件名带 DSN 特殊字符 ?/#：先建普通名再改名，绕开建库时的 DSN 切分
	seed := filepath.Join(dir, "seed.db")
	db0, err := sql.Open("sqlite", seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db0.Exec(`CREATE TABLE t (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	db0.Close()
	p := filepath.Join(dir, "weird?name#x.db")
	if err := os.Rename(seed, p); err != nil {
		t.Fatal(err)
	}

	u := url.URL{Scheme: "file", OmitHost: true, Path: p, RawQuery: "mode=ro&_pragma=busy_timeout%3d5000"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("转义路径应可打开: %d %v", n, err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (1)`); err == nil {
		t.Fatal("mode=ro 应拒绝写入")
	}
}

// TestRunConsoleReadonly 只读连接写语句拒绝、读语句可执行、needConfirm 流转。
func TestRunConsoleReadonly(t *testing.T) {
	path := newSQLiteFile(t)
	ro := openFile(t, path, true)
	d := sqliteDialect{}
	ctx := context.Background()

	if _, err := RunConsole(ctx, ro, d, true, "DELETE FROM users", 0, false, ""); err != ErrReadOnly {
		t.Fatalf("只读应拒绝: %v", err)
	}
	res, err := RunConsole(ctx, ro, d, true, "SELECT COUNT(*) AS n FROM users", 0, false, "")
	if err != nil || res.Rows[0][0] != int64(3) {
		t.Fatalf("只读查询: %+v %v", res, err)
	}
	// 限行
	res2, err := RunConsole(ctx, ro, d, true, "SELECT id FROM users", 2, false, "")
	if err != nil || !res2.Truncated || len(res2.Rows) != 2 {
		t.Fatalf("限行: %+v %v", res2, err)
	}
	// 可写连接：无 WHERE 的 UPDATE 首答 needConfirm，确认后放行
	rw := openFile(t, path, false)
	res3, err := RunConsole(ctx, rw, d, false, "UPDATE users SET bio = 'x'", 0, false, "")
	if err != nil || !res3.NeedConfirm || res3.Reason == "" {
		t.Fatalf("needConfirm: %+v %v", res3, err)
	}
	var cnt int
	_ = rw.QueryRow(`SELECT COUNT(*) FROM users WHERE bio = 'x'`).Scan(&cnt)
	if cnt != 0 {
		t.Fatal("未确认前不应执行")
	}
	res4, err := RunConsole(ctx, rw, d, false, "UPDATE users SET bio = 'x'", 0, true, "")
	if err != nil || res4.NeedConfirm || res4.Affected != 3 {
		t.Fatalf("确认后执行: %+v %v", res4, err)
	}
}

// useFakeDialect 模拟有库概念的方言（切库语句对 SQLite 合法即可），
// 用于真实验证 RunConsole 的钉连接切库路径。
type useFakeDialect struct{ sqliteDialect }

func (useFakeDialect) UseSchema(string) string { return "SELECT 1" }

// TestRunConsoleSchema 切库路径：钉住一条连接先跑 UseSchema 语句再执行主语句，
// 结果不受影响；Columns 元数据（补全）与 UseSchema 语句形状。
func TestRunConsoleSchema(t *testing.T) {
	path := newSQLiteFile(t)
	db := openFile(t, path, false)
	ctx := context.Background()

	res, err := RunConsole(ctx, db, useFakeDialect{}, true, "SELECT COUNT(*) AS n FROM users", 0, false, "main")
	if err != nil || res.Rows[0][0] != int64(3) {
		t.Fatalf("带 schema 执行: %+v %v", res, err)
	}
	// SQLite 无库概念：schema 照传但切库语句为空，走原路
	if s := (sqliteDialect{}).UseSchema("main"); s != "" {
		t.Fatalf("sqlite UseSchema 应为空: %q", s)
	}

	cols, err := (sqliteDialect{}).Columns(ctx, db, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(cols["users"]) != 4 || cols["users"][0] != "id" || cols["users"][3] != "bio" || len(cols["files"]) != 3 {
		t.Fatalf("列元数据: %+v", cols)
	}
}

// TestBackupFiles 备份文件落盘/清单/路径校验/删除。
func TestBackupFiles(t *testing.T) {
	st := openStore(t)
	path := newSQLiteFile(t)
	id, err := st.CreateDBConnection(&store.DBConnection{Name: "本地", Type: "sqlite", Database: path, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	m := New(st, dir)
	defer m.Close()
	ctx := context.Background()

	f, err := m.CreateBackup(ctx, id, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(f.Name, ".sql.gz") || f.Size == 0 {
		t.Fatalf("备份文件: %+v", f)
	}
	files, err := m.ListBackups(id)
	if err != nil || len(files) != 1 {
		t.Fatalf("清单: %+v %v", files, err)
	}
	p, err := m.BackupPath(id, files[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BackupPath(id, "../../etc/passwd.sql.gz"); err == nil {
		t.Fatal("路径穿越应拒绝")
	}
	if _, err := m.BackupPath(id, "a/b.sql.gz"); err == nil {
		t.Fatal("子路径应拒绝")
	}
	if err := m.DeleteBackup(id, files[0].Name); err != nil {
		t.Fatal(err)
	}
	if files, _ := m.ListBackups(id); len(files) != 0 {
		t.Fatal("删除后应为空")
	}
	// 连接删除连带清理
	if _, err := m.CreateBackup(ctx, id, "", nil); err != nil {
		t.Fatal(err)
	}
	m.DeleteConnBackups(id)
	if files, _ := m.ListBackups(id); len(files) != 0 {
		t.Fatal("连带清理失败")
	}
}

// TestPGMySQLShapes 不连库的形状单测：引号/占位符/字面量/ssl 映射。
func TestPGMySQLShapes(t *testing.T) {
	pg := pgDialect{}
	if pg.Quote(`we"ird`) != `"we""ird"` || pg.Ph(3) != "$3" {
		t.Fatal("pg 引号/占位符")
	}
	if pg.Literal("it's") != `'it''s'` || pg.Literal(nil) != "NULL" || pg.Literal(true) != "TRUE" {
		t.Fatal("pg 字面量")
	}
	if pg.Literal([]byte{0xDE, 0xAD}) != "decode('DEAD','hex')" {
		t.Fatal("pg 二进制字面量")
	}
	if got := pg.Qualify("", "t"); got != `"public"."t"` {
		t.Fatal("pg 默认 schema")
	}
	if got := pg.UseSchema(`we"ird`); got != `SET search_path TO "we""ird"` {
		t.Fatal("pg 切库语句")
	}
	if pgSSLMode("on") != "verify-full" || pgSSLMode("skip-verify") != "require" || pgSSLMode("") != "disable" {
		t.Fatal("pg sslmode 映射")
	}
	my := mysqlDialect{}
	if my.Quote("a`b") != "`a``b`" || my.Ph(1) != "?" {
		t.Fatal("mysql 引号/占位符")
	}
	if my.Literal("it's\\ok") != `'it''s\\ok'` || my.Literal([]byte{0xDE}) != "X'DE'" {
		t.Fatal("mysql 字面量")
	}
	if got := my.Qualify("db", "t"); got != "`db`.`t`" {
		t.Fatal("mysql 全名")
	}
	if got := my.UseSchema("a`b"); got != "USE `a``b`" {
		t.Fatal("mysql 切库语句")
	}
	// indexdef 列解析
	cols, ok := pgIndexColumns(`CREATE UNIQUE INDEX t_uk ON t USING btree ("a", b)`)
	if !ok || len(cols) != 2 || cols[0] != "a" || cols[1] != "b" {
		t.Fatalf("indexdef 列: %v %v", cols, ok)
	}
	if _, ok := pgIndexColumns(`CREATE INDEX t_e ON t ((lower(a)))`); ok {
		t.Fatal("表达式索引应放弃解析")
	}
}
