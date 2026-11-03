package api

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/dbtool"
	"lz-monitor/internal/server/store"

	_ "modernc.org/sqlite"
)

// newDBToolRouter 真实链路：真实 Register（含鉴权组）+ dbtool Manager + 临时备份目录。
func newDBToolRouter(t *testing.T) (*gin.Engine, *store.Store, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal("查管理员失败")
	}
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mg := dbtool.New(s, filepath.Join(t.TempDir(), "dbbackups"))
	t.Cleanup(func() { mg.Close() })
	r := gin.New()
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, nil, nil, mg, nil, nil, nil, nil); err != nil {
		t.Fatal(err, nil)
	}
	return r, s, token
}

// doDBReq JSON 请求（路径带 /api/v1 前缀），返回响应与解析体。
func doDBReq(t *testing.T, r *gin.Engine, token, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// doDBImport multipart 文件导入请求。
func doDBImport(t *testing.T, r *gin.Engine, token, path, filename, content string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/api/v1"+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// seedSQLiteFile 造一个被管理的 SQLite 源库。
func seedSQLiteFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, age INTEGER)`,
		`CREATE TABLE nopk (a TEXT, b TEXT)`,
		`INSERT INTO users (name, age) VALUES ('张三', 30), ('李四', 25)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func num(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

// TestDBToolAPI 端到端：建连（先测后存）→浏览→数据行→控制台→只读→备份→导入→删除。
func TestDBToolAPI(t *testing.T) {
	r, _, token := newDBToolRouter(t)
	src := seedSQLiteFile(t)

	// 建连：文件不存在先测后存应 502
	w, _ := doDBReq(t, r, token, "POST", "/dbs", map[string]any{
		"name": "坏连接", "type": "sqlite", "database": filepath.Join(t.TempDir(), "nope.db")})
	if w.Code != http.StatusBadGateway {
		t.Fatalf("不存在的库文件应测连失败: %d %s", w.Code, w.Body.String())
	}
	// 正常建连
	w, _ = doDBReq(t, r, token, "POST", "/dbs", map[string]any{
		"name": "本地库", "type": "sqlite", "database": src})
	if w.Code != http.StatusOK {
		t.Fatalf("建连失败: %d %s", w.Code, w.Body.String())
	}

	// 清单：无凭据泄露形状
	w, out := doDBReq(t, r, token, "GET", "/dbs", nil)
	if w.Code != http.StatusOK || num(out["max"]) != 16 {
		t.Fatalf("清单: %d", w.Code)
	}
	conns := out["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["has_auth"] != false {
		t.Fatalf("连接形状: %+v", conns)
	}

	// 浏览：schemas/tables/table
	w, out = doDBReq(t, r, token, "GET", "/dbs/1/schemas", nil)
	if w.Code != http.StatusOK || out["schemas"].([]any)[0] != "main" {
		t.Fatalf("schemas: %s", w.Body.String())
	}
	w, out = doDBReq(t, r, token, "GET", "/dbs/1/tables?schema=main", nil)
	if w.Code != http.StatusOK || len(out["tables"].([]any)) != 2 {
		t.Fatalf("tables: %s", w.Body.String())
	}
	w, out = doDBReq(t, r, token, "GET", "/dbs/1/table?schema=main&name=users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("table: %s", w.Body.String())
	}
	if pk := out["pk"].([]any); len(pk) != 1 || pk[0] != "id" {
		t.Fatalf("主键: %+v", out["pk"])
	}
	if num(out["row_count"]) != 2 {
		t.Fatalf("行数: %v", out["row_count"])
	}
	// 无主键表：pk 归一为空数组而非 null（346——null 会让前端 detail.pk.length 炸渲染）
	w, out = doDBReq(t, r, token, "GET", "/dbs/1/table?schema=main&name=nopk", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("nopk table: %s", w.Body.String())
	}
	if pks, ok := out["pk"].([]any); !ok || len(pks) != 0 {
		t.Fatalf("无主键表 pk 应为空数组: %v", out["pk"])
	}

	// 数据页
	w, out = doDBReq(t, r, token, "GET", "/dbs/1/rows?schema=main&table=users&page=1&size=50", nil)
	if w.Code != http.StatusOK || num(out["total"]) != 2 {
		t.Fatalf("rows: %d %s", w.Code, w.Body.String())
	}

	// 行级写：2026-10-03 全工具屏蔽写入口（TODO 330，仅查询），一律 400
	w, out = doDBReq(t, r, token, "POST", "/dbs/1/rows", map[string]any{
		"table": "users", "values": map[string]any{"name": "王五", "age": 22}})
	if w.Code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "无写入操作") {
		t.Fatalf("行插入应屏蔽: %d %s", w.Code, w.Body.String())
	}
	w, _ = doDBReq(t, r, token, "POST", "/dbs/1/rows/update", map[string]any{
		"table": "users", "pk": map[string]any{"id": 1}, "values": map[string]any{"age": 23}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("行更新应屏蔽: %d %s", w.Code, w.Body.String())
	}
	w, _ = doDBReq(t, r, token, "POST", "/dbs/1/rows/delete", map[string]any{
		"table": "users", "pk": map[string]any{"id": 1}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("行删除应屏蔽: %d %s", w.Code, w.Body.String())
	}

	// 控制台：读放行；写与多语句一律拒绝（只读位硬编码 true，不再有 need_confirm）
	w, out = doDBReq(t, r, token, "POST", "/dbs/1/query", map[string]any{"sql": "SELECT COUNT(*) AS n FROM users"})
	if w.Code != http.StatusOK || num(out["rows"].([]any)[0].([]any)[0]) != 2 {
		t.Fatalf("查询: %s", w.Body.String())
	}
	// 带 schema 的查询（339 库上下文；SQLite 无库概念，直通）
	w, out = doDBReq(t, r, token, "POST", "/dbs/1/query", map[string]any{"sql": "SELECT COUNT(*) AS n FROM users", "schema": "main"})
	if w.Code != http.StatusOK || num(out["rows"].([]any)[0].([]any)[0]) != 2 {
		t.Fatalf("带 schema 查询: %s", w.Body.String())
	}
	w, _ = doDBReq(t, r, token, "POST", "/dbs/1/query", map[string]any{"sql": "UPDATE users SET age = 0"})
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "无写入操作") {
		t.Fatalf("控制台写应拒绝并报只读口径: %d %s", w.Code, w.Body.String())
	}
	w, _ = doDBReq(t, r, token, "POST", "/dbs/1/query", map[string]any{"sql": "SELECT 1; SELECT 2"})
	if w.Code != http.StatusBadGateway {
		t.Fatalf("多语句应拒绝: %d", w.Code)
	}

	// 全库列元数据（340 SQL 补全）
	w, out = doDBReq(t, r, token, "GET", "/dbs/1/columns?schema=main", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("columns: %s", w.Body.String())
	}
	cols := out["columns"].(map[string]any)
	if uc, ok := cols["users"].([]any); !ok || len(uc) < 2 || uc[0] != "id" {
		t.Fatalf("columns users: %v", cols["users"])
	}

	// 只读连接：建连 + 写拒绝
	w, _ = doDBReq(t, r, token, "POST", "/dbs", map[string]any{
		"name": "只读库", "type": "sqlite", "database": src, "readonly": true})
	if w.Code != http.StatusOK {
		t.Fatalf("只读建连: %s", w.Body.String())
	}
	w, _ = doDBReq(t, r, token, "POST", "/dbs/2/rows", map[string]any{
		"table": "users", "values": map[string]any{"name": "x"}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("只读写入应 400: %d", w.Code)
	}
	w, _ = doDBReq(t, r, token, "POST", "/dbs/2/query", map[string]any{"sql": "DELETE FROM users"})
	if w.Code != http.StatusBadGateway {
		t.Fatalf("只读控制台写应拒绝: %d %s", w.Code, w.Body.String())
	}

	// 备份：触发→清单→下载（gzip 内容校验）→删除
	w, out = doDBReq(t, r, token, "POST", "/dbs/1/backups", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("备份: %s", w.Body.String())
	}
	name := out["file"].(map[string]any)["name"].(string)
	w, out = doDBReq(t, r, token, "GET", "/dbs/1/backups", nil)
	if w.Code != http.StatusOK || len(out["files"].([]any)) != 1 {
		t.Fatalf("备份清单: %s", w.Body.String())
	}
	w2 := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/dbs/backups/download?conn=1&name="+name, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK || !strings.HasPrefix(w2.Header().Get("Content-Type"), "application/gzip") {
		t.Fatalf("下载: %d", w2.Code)
	}
	gz, err := gzip.NewReader(bytes.NewReader(w2.Body.Bytes()))
	if err != nil {
		t.Fatalf("下载非 gzip: %v", err)
	}
	content, _ := io.ReadAll(gz)
	if !bytes.Contains(content, []byte("CREATE TABLE users")) {
		t.Fatal("备份内容缺 DDL")
	}
	w, _ = doDBReq(t, r, token, "POST", "/dbs/1/backups/delete", map[string]any{"name": name})
	if w.Code != http.StatusOK {
		t.Fatalf("删除备份: %s", w.Body.String())
	}

	// 导入：写入口屏蔽应 400（不再执行语句）
	w, _ = doDBImport(t, r, token, "/dbs/1/import", "build.sql",
		"-- 建临时表\nCREATE TABLE tmp_t (a INT);\nINSERT INTO tmp_t VALUES (1);\n")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("导入应屏蔽: %d %s", w.Code, w.Body.String())
	}

	// 删除连接（连带备份目录）
	w, _ = doDBReq(t, r, token, "DELETE", "/dbs/1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("删除连接: %s", w.Body.String())
	}
	w, out = doDBReq(t, r, token, "GET", "/dbs", nil)
	if len(out["connections"].([]any)) != 1 {
		t.Fatal("删除后应剩 1 个连接")
	}
}

// TestBuildConnUsernameOnly 只改用户名不改密码：新用户名配旧密码重封；
// 用户名未变整份沿用旧密文（343——原逻辑会把新填用户名静默丢掉）。
func TestBuildConnUsernameOnly(t *testing.T) {
	old := &store.DBConnection{Type: "mysql", Host: "h", Port: 3306, Name: "x", Auth: "SEALED-OLD"}
	c, err := buildConn(&dbConnReq{Name: "x", Type: "mysql", Host: "h", Username: "u2"}, old, "u1", "p1")
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(c.Auth), &a); err != nil {
		t.Fatalf("应重封为凭据 JSON（落库时再加密）: %q", c.Auth)
	}
	if a.Username != "u2" || a.Password != "p1" {
		t.Fatalf("新用户名+旧密码: %+v", a)
	}
	c2, err := buildConn(&dbConnReq{Name: "x", Type: "mysql", Host: "h", Username: "u1"}, old, "u1", "p1")
	if err != nil || c2.Auth != "SEALED-OLD" {
		t.Fatalf("用户名未变应整份沿用: %q %v", c2.Auth, err)
	}
}

// TestDBToolAPIValidation 参数校验：非法类型/端口/凭据缺失。
func TestDBToolAPIValidation(t *testing.T) {
	r, _, token := newDBToolRouter(t)
	cases := []struct {
		body map[string]any
		msg  string
	}{
		{map[string]any{"name": "x", "type": "oracle"}, "不支持的数据库类型"},
		{map[string]any{"name": "", "type": "sqlite", "database": "/tmp/a.db"}, "连接名称"},
		{map[string]any{"name": "x", "type": "sqlite", "database": "relative/path.db"}, "绝对路径"},
		{map[string]any{"name": "x", "type": "mysql", "host": "h"}, "用户名"},
	}
	for i, c := range cases {
		w, out := doDBReq(t, r, token, "POST", "/dbs", c.body)
		if w.Code == http.StatusOK {
			t.Errorf("case %d (%s): 应拒绝", i, c.msg)
			continue
		}
		if !strings.Contains(fmt.Sprint(out["error"]), c.msg) {
			t.Errorf("case %d: 错误文案 %q 不含 %q", i, out["error"], c.msg)
		}
	}
}
