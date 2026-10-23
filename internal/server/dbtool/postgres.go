package dbtool

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// pgDialect PostgreSQL（经 pgx stdlib 接 database/sql）。
type pgDialect struct{}

func (pgDialect) Name() string { return "postgres" }

// Quote 双引号内双写双引号。
func (pgDialect) Quote(ident string) string {
	if err := idword(ident); err != nil {
		panic(err)
	}
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (pgDialect) Ph(i int) string { return "$" + strconv.Itoa(i) }

func (pgDialect) Schemas(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT nspname FROM pg_namespace
		WHERE nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema' ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (pgDialect) Tables(ctx context.Context, q dbtx, schema string) ([]TableInfo, error) {
	// relkind: r=普通表 p=分区表 v=视图 m=物化视图；reltuples=-1=从未 ANALYZE
	rows, err := q.QueryContext(ctx, `SELECT c.relname, CASE WHEN c.reltuples >= 0 THEN c.reltuples::bigint ELSE 0 END,
		COALESCE(obj_description(c.oid),''), c.relkind IN ('v','m'), c.reltuples < 0
		FROM pg_class c JOIN pg_namespace n ON c.relnamespace = n.oid
		WHERE n.nspname = $1 AND c.relkind IN ('r','p','v','m') ORDER BY c.relname`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableInfo
	for rows.Next() {
		var t TableInfo
		var view, noCount bool
		if err := rows.Scan(&t.Name, &t.Rows, &t.Comment, &view, &noCount); err != nil {
			return nil, err
		}
		t.View = view
		t.NoCount = noCount || view
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d pgDialect) Table(ctx context.Context, q dbtx, schema, table string) (*TableDetail, error) {
	det := &TableDetail{Schema: schema, Name: table}
	rows, err := q.QueryContext(ctx, `SELECT column_name, data_type, character_maximum_length, udt_name,
		is_nullable = 'YES', COALESCE(column_default,''), column_default LIKE 'nextval(%'
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c ColumnInfo
		var dataType, udt string
		var charLen sql.NullInt64
		if err := rows.Scan(&c.Name, &dataType, &charLen, &udt, &c.Nullable, &c.Default, &c.AutoInc); err != nil {
			rows.Close()
			return nil, err
		}
		c.Type = pgTypeString(dataType, charLen, udt)
		det.Columns = append(det.Columns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(det.Columns) == 0 {
		return nil, ErrUnknownTable
	}

	pkRows, err := q.QueryContext(ctx, `SELECT kcu.column_name FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name AND tc.constraint_schema = kcu.constraint_schema
		WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = 'PRIMARY KEY'
		ORDER BY kcu.ordinal_position`, schema, table)
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

	idxRows, err := q.QueryContext(ctx, `SELECT indexname, indexdef FROM pg_indexes
		WHERE schemaname = $1 AND tablename = $2 ORDER BY indexname`, schema, table)
	if err != nil {
		return nil, err
	}
	for idxRows.Next() {
		var name, def string
		if err := idxRows.Scan(&name, &def); err != nil {
			idxRows.Close()
			return nil, err
		}
		info := IndexInfo{Name: name, Unique: strings.HasPrefix(def, "CREATE UNIQUE"), Def: def,
			Primary: name == table+"_pkey", Auto: name == table+"_pkey"}
		if cols, ok := pgIndexColumns(def); ok {
			info.Columns = cols
		}
		det.Indexes = append(det.Indexes, info)
	}
	idxRows.Close()
	if err := idxRows.Err(); err != nil {
		return nil, err
	}

	if err := q.QueryRowContext(ctx, `SELECT COALESCE(reltuples,0)::bigint FROM pg_class c
		JOIN pg_namespace n ON c.relnamespace = n.oid
		WHERE n.nspname = $1 AND c.relname = $2`, schema, table).Scan(&det.RowCount); err != nil {
		det.RowCount, det.NoCount = 0, true
	}
	det.DDL = d.buildDDL(det)
	return det, nil
}

// pgTypeString 列类型展示串：变长字符拼长度，自定义类型回 udt_name。
func pgTypeString(dataType string, charLen sql.NullInt64, udt string) string {
	switch dataType {
	case "character varying", "character":
		if charLen.Valid {
			return fmt.Sprintf("%s(%d)", dataType, charLen.Int64)
		}
		return dataType
	case "USER-DEFINED":
		return udt
	}
	return dataType
}

func (d pgDialect) buildDDL(det *TableDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "-- DDL 由 information_schema 生成（PG 无 SHOW CREATE TABLE）\nCREATE TABLE %s (\n", d.Qualify(det.Schema, det.Name))
	for i, c := range det.Columns {
		b.WriteString("  " + d.Quote(c.Name) + " " + c.Type)
		if c.Default != "" {
			b.WriteString(" DEFAULT " + c.Default)
		}
		if !c.Nullable {
			b.WriteString(" NOT NULL")
		}
		if i < len(det.Columns)-1 || len(det.PK) > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	if len(det.PK) > 0 {
		cols := make([]string, len(det.PK))
		for i, c := range det.PK {
			cols[i] = d.Quote(c)
		}
		fmt.Fprintf(&b, "  PRIMARY KEY (%s)\n", strings.Join(cols, ", "))
	}
	b.WriteString(");")
	return b.String()
}

func (d pgDialect) Qualify(schema, table string) string {
	if schema == "" {
		schema = "public"
	}
	return d.Quote(schema) + "." + d.Quote(table)
}

func (d pgDialect) UseSchema(schema string) string { return "SET search_path TO " + d.Quote(schema) }

// Columns 一次 information_schema 查询取全 schema 列名（含视图），SQL 补全用。
func (d pgDialect) Columns(ctx context.Context, q dbtx, schema string) (map[string][]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT table_name, column_name FROM information_schema.columns
		WHERE table_schema = $1 ORDER BY table_name, ordinal_position`, schema)
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

func (pgDialect) DumpPreamble() []string {
	return []string{"SET standard_conforming_strings = on;"}
}

func (pgDialect) DumpPostamble() []string { return nil }

// Literal 备份导出用字面量：” 双写转义（standard_conforming_strings=on 下
// 反斜杠是普通字符），二进制 decode('hex','hex') 免转义歧义。
func (pgDialect) Literal(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if t {
			return "TRUE"
		}
		return "FALSE"
	case []byte:
		return "decode('" + hexEncode(t) + "','hex')"
	case time.Time:
		return "'" + t.Format("2006-01-02 15:04:05.999999") + "'"
	case string:
		return "'" + strings.ReplaceAll(t, "'", "''") + "'"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(v)
	default:
		return "'" + strings.ReplaceAll(fmt.Sprint(v), "'", "''") + "'"
	}
}

func (pgDialect) TxReadOnly() bool { return true }

// pgIndexColumns 从 indexdef 的 USING <方法> (…) 段抠出列名清单（含嵌套括号
// 的表达式索引解析失败静默放弃，仅影响展示不影响功能）。
func pgIndexColumns(def string) ([]string, bool) {
	uso := strings.Index(def, " USING ")
	if uso < 0 {
		return nil, false
	}
	open := strings.Index(def[uso:], "(")
	if open < 0 {
		return nil, false
	}
	open += uso
	depth, closer := 0, -1
	for i := open; i < len(def); i++ {
		switch def[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				closer = i
			}
		}
		if closer >= 0 {
			break
		}
	}
	if closer < 0 {
		return nil, false
	}
	inner := def[open+1 : closer]
	if strings.Contains(inner, "(") {
		return nil, false // 表达式索引，不硬解
	}
	parts := strings.Split(inner, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, false
		}
		out = append(out, strings.Trim(p, `"`))
	}
	return out, true
}

// OpenPG 组装 PG 连接池（pgx stdlib connector；只读连接以运行时参数硬约束）。
func OpenPG(cfg *ConnConfig) (*sql.DB, error) {
	kv := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d",
		kwArg(cfg.Host), cfg.Port, kwArg(cfg.User), kwArg(cfg.Pass), kwArg(cfg.Database), cfg.SSLMode, int(cfg.Timeout.Seconds()))
	pgcfg, err := pgx.ParseConfig(kv)
	if err != nil {
		return nil, err
	}
	if cfg.Dial != nil {
		pgcfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return cfg.Dial(ctx, network, addr)
		}
	}
	if cfg.ReadOnly {
		pgcfg.RuntimeParams["default_transaction_read_only"] = "on"
	}
	return sql.OpenDB(stdlib.GetConnector(*pgcfg)), nil
}

// kwArg keyword/value 串的单引号转义。
func kwArg(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `'`, `\'`) + "'"
}
