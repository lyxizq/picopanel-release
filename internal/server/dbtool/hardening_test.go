package dbtool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lz-monitor/internal/server/store"
)

// 只读兜底第二道锁：mode=ro 只锁主库，ATTACH 挂上可写库后写与 VACUUM INTO
// 照样落盘（曾实测）；query_only(1) 上线后两条路都必须被 readonly 挡死。
func TestSQLiteQueryOnlyBlocksAttachWrite(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.db")
	aux := filepath.Join(dir, "aux.db")
	for _, p := range []string{main, aux} { // mode=ro 打不开不存在的库文件，预建空库
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, _, err := openDialect(&store.DBConnection{Type: "sqlite", Database: main},
		&ConnConfig{Database: main})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Conn(ctx) // ATTACH 是连接级：三条语句必须钉同一条连接
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE '"+aux+"' AS aux"); err != nil {
		t.Fatalf("ATTACH 本身不写库，应成功: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CREATE TABLE aux.t2(a)"); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "readonly") {
		t.Fatalf("query_only 应挡住 ATTACH 库写入，实得 %v", err)
	}
	if _, err := conn.ExecContext(ctx, "VACUUM INTO '"+filepath.Join(dir, "out.db")+"'"); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "readonly") {
		t.Fatalf("query_only 应挡住 VACUUM INTO，实得 %v", err)
	}
}

// classify：读语句内的落盘/外联/会话参数函数按写拒绝（宁误杀口径）。
func TestClassifyReadFuncsReject(t *testing.T) {
	for _, q := range []string{
		"SELECT lo_export(123, '/tmp/x')",
		"SELECT lo_import('/tmp/x')",
		"SELECT set_config('search_path', 'public', false)",
		"SELECT load_file('/etc/passwd')",
	} {
		st, err := classify(mysqlDialect{}, q)
		if err != nil {
			t.Fatalf("%q classify 报错: %v", q, err)
		}
		if st.kind != kindWrite {
			t.Errorf("%q 应判 kindWrite，实得 %v", q, st.kind)
		}
	}
}

// ValidIdent：空串/NUL 必须 API 层拦下（Quote 的 panic 面不许被用户输入触达）。
func TestValidIdent(t *testing.T) {
	if !ValidIdent("users") || !ValidIdent("order_1") {
		t.Error("合法标识符应通过")
	}
	for _, s := range []string{"", "a\x00b"} {
		if ValidIdent(s) {
			t.Errorf("%q 应拒绝", s)
		}
	}
}
