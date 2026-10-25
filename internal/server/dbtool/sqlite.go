package dbtool

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 纯 Go 驱动，注册 "sqlite"，与主库同款无 CGO
)

// sqliteDialect SQLite（服务器本机库文件）。
type sqliteDialect struct{}

func (sqliteDialect) Name() string { return "sqlite" }

func (sqliteDialect) Quote(ident string) string {
	if err := idword(ident); err != nil {
		panic(err)
	}
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

func (sqliteDialect) Ph(int) string { return "?" }

func (sqliteDialect) Schemas(context.Context, *sql.DB) ([]string, error) {
	return []string{"main"}, nil
}

func (sqliteDialect) Tables(ctx context.Context, q dbtx, _ string) ([]TableInfo, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, type FROM sqlite_master
		WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableInfo
	for rows.Next() {
		var t TableInfo
		var typ string
		if err := rows.Scan(&t.Name, &typ); err != nil {
			return nil, err
		}
		t.View = typ == "view"
		t.NoCount = true // 列表阶段不 COUNT，进详情再取精确值
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d sqliteDialect) Table(ctx context.Context, q dbtx, schema, table string) (*TableDetail, error) {
	det := &TableDetail{Schema: schema, Name: table}
	rows, err := q.QueryContext(ctx, `PRAGMA table_info(`+d.Quote(table)+`)`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c ColumnInfo
		var cid, notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &c.Name, &c.Type, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return nil, err
		}
		c.Nullable = notNull == 0
		c.Default = dflt.String
		if pk > 0 {
			det.PK = append(det.PK, c.Name)
		}
		det.Columns = append(det.Columns, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(det.Columns) == 0 {
		return nil, ErrUnknownTable
	}

	// DDL 原文（含视图定义）；AUTOINCREMENT 只在表级定义里出现，借它识别自增列
	var typ, ddl sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT type, COALESCE(sql,'') FROM sqlite_master WHERE name = ?`, table).
		Scan(&typ, &ddl); err != nil {
		return nil, ErrUnknownTable
	}
	det.DDL = ddl.String
	det.View = typ.String == "view"
	if det.View {
		det.PK = nil
	}
	ddlUpper := strings.ToUpper(det.DDL)
	for i := range det.Columns {
		if det.Columns[i].Type == "" {
			continue
		}
		det.Columns[i].AutoInc = strings.Contains(ddlUpper, "AUTOINCREMENT")
		break
	}

	idxRows, err := q.QueryContext(ctx, `PRAGMA index_list(`+d.Quote(table)+`)`)
	if err != nil {
		return nil, err
	}
	type idxRow struct {
		name   string
		unique bool
		origin string // c=用户索引；pk/u=约束索引（建表 DDL 已含，导出时跳过）
	}
	var idxList []idxRow
	for idxRows.Next() {
		var r idxRow
		var seq, unique, partial int
		if err := idxRows.Scan(&seq, &r.name, &unique, &r.origin, &partial); err != nil {
			idxRows.Close()
			return nil, err
		}
		r.unique = unique != 0
		idxList = append(idxList, r)
	}
	idxRows.Close()
	if err := idxRows.Err(); err != nil {
		return nil, err
	}
	for _, r := range idxList {
		info := IndexInfo{Name: r.name, Unique: r.unique,
			Primary: r.origin == "pk", Auto: r.origin == "pk" || r.origin == "u"}
		colRows, err := q.QueryContext(ctx, `PRAGMA index_info(`+d.Quote(r.name)+`)`)
		if err != nil {
			return nil, err
		}
		for colRows.Next() {
			var seqno int
			var name string
			var origin sql.NullString
			if err := colRows.Scan(&seqno, &name, &origin); err != nil {
				colRows.Close()
				return nil, err
			}
			info.Columns = append(info.Columns, name)
		}
		colRows.Close()
		info.Def = fmt.Sprintf("CREATE %s INDEX %s ON %s (%s)",
			map[bool]string{true: "UNIQUE", false: ""}[r.unique], d.Quote(r.name), d.Quote(table), strings.Join(mapCols(d, info.Columns), ", "))
		det.Indexes = append(det.Indexes, info)
	}

	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+d.Qualify(schema, table)).Scan(&det.RowCount); err != nil {
		det.RowCount, det.NoCount = 0, true
	}
	return det, nil
}

func mapCols(d sqliteDialect, cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = d.Quote(c)
	}
	return out
}

func (d sqliteDialect) Qualify(_, table string) string { return d.Quote(table) }

// UseSchema SQLite 无多库概念，返回空串让控制台跳过切库。
func (sqliteDialect) UseSchema(string) string { return "" }

// Columns 一次 pragma 表值函数联查取全库列名（含视图），SQL 补全用。
func (d sqliteDialect) Columns(ctx context.Context, q dbtx, _ string) (map[string][]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT m.name, p.name FROM sqlite_master m
		JOIN pragma_table_info(m.name) p
		WHERE m.type IN ('table','view') AND m.name NOT LIKE 'sqlite_%'
		ORDER BY m.name, p.cid`)
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

func (sqliteDialect) DumpPreamble() []string {
	return []string{"PRAGMA foreign_keys=OFF;", "BEGIN TRANSACTION;"}
}

func (sqliteDialect) DumpPostamble() []string {
	return []string{"COMMIT;", "PRAGMA foreign_keys=ON;"}
}

// Literal 备份导出用字面量：” 双写；二进制 X'hex'；布尔 1/0。
func (sqliteDialect) Literal(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case bool:
		if t {
			return "1"
		}
		return "0"
	case []byte:
		return "X'" + hexEncode(t) + "'"
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

func (sqliteDialect) TxReadOnly() bool { return false }
