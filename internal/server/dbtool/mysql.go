package dbtool

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// mysqlDialect MySQL 8.x（MariaDB 兼容）。
type mysqlDialect struct{}

func (mysqlDialect) Name() string { return "mysql" }

// Quote 反引号内双写反引号。
func (mysqlDialect) Quote(ident string) string {
	if err := idword(ident); err != nil {
		panic(err)
	}
	return "`" + strings.ReplaceAll(ident, "`", "``") + "`"
}

func (mysqlDialect) Ph(int) string { return "?" }

func (mysqlDialect) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SHOW DATABASES`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sys := systemSchemas("mysql")
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		if !sys[s] {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

func (d mysqlDialect) Tables(ctx context.Context, q dbtx, schema string) ([]TableInfo, error) {
	rows, err := q.QueryContext(ctx, `SELECT table_name, COALESCE(table_rows,0), COALESCE(table_comment,''), table_type
		FROM information_schema.tables WHERE table_schema = ? ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableInfo
	for rows.Next() {
		var t TableInfo
		var typ string
		if err := rows.Scan(&t.Name, &t.Rows, &t.Comment, &typ); err != nil {
			return nil, err
		}
		t.View = typ == "VIEW"
		if t.View {
			t.NoCount = true
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d mysqlDialect) Table(ctx context.Context, q dbtx, schema, table string) (*TableDetail, error) {
	det := &TableDetail{Schema: schema, Name: table}
	rows, err := q.QueryContext(ctx, `SELECT column_name, column_type, IF(is_nullable='YES',1,0),
		COALESCE(column_default,''), COALESCE(extra,''), COALESCE(column_comment,'')
		FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ColumnInfo
		var extra string
		var nn int
		if err := rows.Scan(&c.Name, &c.Type, &nn, &c.Default, &extra, &c.Comment); err != nil {
			return nil, err
		}
		c.Nullable = nn != 0
		c.AutoInc = strings.Contains(extra, "auto_increment")
		det.Columns = append(det.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(det.Columns) == 0 {
		return nil, ErrUnknownTable
	}
	rows.Close()

	pkRows, err := q.QueryContext(ctx, `SELECT column_name FROM information_schema.key_column_usage
		WHERE table_schema = ? AND table_name = ? AND constraint_name = 'PRIMARY' ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	for pkRows.Next() {
		var col string
		if err := pkRows.Scan(&col); err != nil {
			pkRows.Close()
			return nil, err
		}
		det.PK = append(det.PK, col)
	}
	pkRows.Close()
	if err := pkRows.Err(); err != nil {
		return nil, err
	}

	// 索引经 information_schema.statistics 取（SHOW INDEX 返回列太多，只挑四列够用）
	idxRows, err := q.QueryContext(ctx, `SELECT index_name, non_unique, column_name, seq_in_index
		FROM information_schema.statistics WHERE table_schema = ? AND table_name = ?
		ORDER BY index_name, seq_in_index`, schema, table)
	if err != nil {
		return nil, err
	}
	idx := map[string]*IndexInfo{}
	var order []string
	for idxRows.Next() {
		var keyName, col string
		var nonUnique, seq int
		if err := idxRows.Scan(&keyName, &nonUnique, &col, &seq); err != nil {
			idxRows.Close()
			return nil, err
		}
		info := idx[keyName]
		if info == nil {
			info = &IndexInfo{Name: keyName, Unique: nonUnique == 0, Primary: keyName == "PRIMARY",
				Auto: keyName == "PRIMARY"}
			idx[keyName] = info
			order = append(order, keyName)
		}
		info.Columns = append(info.Columns, col)
	}
	idxRows.Close()
	if err := idxRows.Err(); err != nil {
		return nil, err
	}
	for _, k := range order {
		det.Indexes = append(det.Indexes, *idx[k])
	}

	var ddlName, ddl string
	if err := q.QueryRowContext(ctx, "SHOW CREATE TABLE "+d.Qualify(schema, table)).Scan(&ddlName, &ddl); err != nil {
		return nil, fmt.Errorf("SHOW CREATE TABLE: %w", err)
	}
	det.DDL = ddl
	det.RowCount, det.NoCount = -1, true // 行数用列表阶段 information_schema 的近似统计
	return det, nil
}

func (d mysqlDialect) Qualify(schema, table string) string {
	if schema == "" {
		return d.Quote(table)
	}
	return d.Quote(schema) + "." + d.Quote(table)
}

func (d mysqlDialect) UseSchema(schema string) string { return "USE " + d.Quote(schema) }

// Columns 一次 information_schema 查询取全库列名（含视图），SQL 补全用。
func (d mysqlDialect) Columns(ctx context.Context, q dbtx, schema string) (map[string][]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = ? ORDER BY table_name, ordinal_position`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return nil, err
		}
		out[t] = append(out[t], c)
	}
	return out, rows.Err()
}

func (mysqlDialect) DumpPreamble() []string {
	return []string{
		"SET NAMES utf8mb4;",
		"SET FOREIGN_KEY_CHECKS = 0;",
		"SET SQL_MODE = 'NO_AUTO_VALUE_ON_ZERO';",
	}
}

func (mysqlDialect) DumpPostamble() []string {
	return []string{"SET FOREIGN_KEY_CHECKS = 1;"}
}

// Literal 备份导出用字面量：字符串反斜杠+单引号转义（mysqldump 同风格），
// 二进制 X'hex'，时间 '2006-01-02 15:04:05.999999'。
func (mysqlDialect) Literal(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if t {
			return "1"
		}
		return "0"
	case []byte:
		if len(t) == 0 {
			return "''"
		}
		return "X'" + hexEncode(t) + "'"
	case time.Time:
		return "'" + t.Format("2006-01-02 15:04:05.999999") + "'"
	case string:
		return "'" + mysqlEscape(t) + "'"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(v)
	default:
		return "'" + mysqlEscape(fmt.Sprint(v)) + "'"
	}
}

// mysqlEscape 串内转义（不含外层引号）：反斜杠与单引号，控制字节给转义序列。
func mysqlEscape(s string) string {
	s = strings.ReplaceAll(s, "\x00", `\0`)
	s = strings.ReplaceAll(s, "\x1a", `\Z`)
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "'", `''`)
}

func (mysqlDialect) TxReadOnly() bool { return true }

// OpenMySQL 组装 MySQL 连接池（NewConnector 免全局注册、凭据免 DSN 拼串）。
func OpenMySQL(cfg *ConnConfig) (*sql.DB, error) {
	c := mysql.NewConfig()
	c.Net = "tcp"
	c.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	c.User, c.Passwd = cfg.User, cfg.Pass
	c.DBName = cfg.Database
	// 不发 charset 连接参数：MySQL 8.0.3+/新版 MariaDB 已移除该系统变量
	// （Unknown system variable 'charset'），驱动默认字符集即 utf8mb4。
	c.ParseTime = true
	c.Loc = time.Local
	c.Timeout = cfg.Timeout
	c.ReadTimeout = cfg.ReadTimeout
	c.WriteTimeout = cfg.ReadTimeout
	switch cfg.SSL {
	case "on":
		c.TLSConfig = "true"
	case "skip-verify":
		c.TLSConfig = "skip-verify"
	}
	if cfg.Dial != nil {
		c.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return cfg.Dial(ctx, network, addr)
		}
	}
	conn, err := mysql.NewConnector(c)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(conn), nil
}
