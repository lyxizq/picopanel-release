package dbtool

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Filter 数据页过滤条件（值一律占位符参数化）。
type Filter struct {
	Col string `json:"col"`
	Op  string `json:"op"` // eq/ne/gt/ge/lt/le/like/isnull/isnotnull
	Val any    `json:"val"`
}

// FilterOps 合法操作符。
var FilterOps = map[string]bool{
	"eq": true, "ne": true, "gt": true, "ge": true, "lt": true, "le": true,
	"like": true, "isnull": true, "isnotnull": true,
}

// PageRows 分页取数据行：标识符经方言引号、值占位符、列名/操作符白名单校验。
func PageRows(ctx context.Context, db *sql.DB, d Dialect, schema, table string,
	filters []Filter, sortCol, sortDir string, page, size int) (*PageResult, error) {
	det, err := d.Table(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	colset := map[string]bool{}
	for _, c := range det.Columns {
		colset[strings.ToLower(c.Name)] = true
	}
	where, args, err := buildWhere(d, colset, filters)
	if err != nil {
		return nil, err
	}
	qual := d.Qualify(schema, table)

	totalArgs := append([]any{}, args...)
	var total int64
	if !det.View {
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+qual+where, totalArgs...).Scan(&total); err != nil {
			return nil, err
		}
	}

	order := ""
	if sortCol != "" {
		if !colset[strings.ToLower(sortCol)] {
			return nil, fmt.Errorf("排序字段不存在: %s", sortCol)
		}
		dir := "ASC"
		if strings.EqualFold(sortDir, "desc") {
			dir = "DESC"
		}
		order = " ORDER BY " + d.Quote(sortCol) + " " + dir
	}
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	q := "SELECT * FROM " + qual + where + order
	n := len(args)
	q += " LIMIT " + d.Ph(n+1) + " OFFSET " + d.Ph(n+2)
	args = append(args, size, (page-1)*size)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &PageResult{Columns: cols, Total: total, Page: page, Size: size, PK: det.PK}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out := make([]any, len(vals))
		for i, v := range vals {
			out[i] = normalizeValue(v)
		}
		res.Rows = append(res.Rows, out)
	}
	res.Total = total
	return res, rows.Err()
}

// buildWhere 拼 WHERE 子句（含前导 " WHERE"，可能为空串）。
func buildWhere(d Dialect, colset map[string]bool, filters []Filter) (string, []any, error) {
	var b strings.Builder
	var args []any
	ph := 0
	for _, f := range filters {
		if !colset[strings.ToLower(f.Col)] {
			return "", nil, fmt.Errorf("过滤字段不存在: %s", f.Col)
		}
		if !FilterOps[f.Op] {
			return "", nil, fmt.Errorf("不支持的操作符: %s", f.Op)
		}
		if b.Len() == 0 {
			b.WriteString(" WHERE ")
		} else {
			b.WriteString(" AND ")
		}
		switch f.Op {
		case "isnull":
			b.WriteString(d.Quote(f.Col) + " IS NULL")
		case "isnotnull":
			b.WriteString(d.Quote(f.Col) + " IS NOT NULL")
		case "like":
			ph++
			// 值包 %..%：通配符转义，防 % _ 注入语义
			v := likeEscape(fmt.Sprint(f.Val))
			b.WriteString(d.Quote(f.Col) + " LIKE " + d.Ph(ph))
			args = append(args, "%"+v+"%")
		default:
			ph++
			op := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "ge": ">=", "lt": "<", "le": "<="}[f.Op]
			b.WriteString(d.Quote(f.Col) + " " + op + " " + d.Ph(ph))
			args = append(args, f.Val)
		}
	}
	return b.String(), args, nil
}

func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	return strings.ReplaceAll(s, "_", `\_`)
}

// InsertRow 插入一行（values 列名 → 值；nil=NULL）。返回受影响行数与自增主键
// （PG 无 LastInsertId 返回 0）。
func InsertRow(ctx context.Context, db *sql.DB, d Dialect, schema, table string, values map[string]any) (int64, int64, error) {
	det, err := d.Table(ctx, db, schema, table)
	if err != nil {
		return 0, 0, err
	}
	if len(det.PK) == 0 {
		return 0, 0, ErrNoPK
	}
	colset := map[string]bool{}
	for _, c := range det.Columns {
		colset[strings.ToLower(c.Name)] = true
	}
	var cols []string
	var vals []any
	for name, v := range values {
		if !colset[strings.ToLower(name)] {
			return 0, 0, fmt.Errorf("字段不存在: %s", name)
		}
		cols = append(cols, d.Quote(name))
		vals = append(vals, v)
	}
	if len(cols) == 0 {
		return 0, 0, fmt.Errorf("没有可插入的字段")
	}
	phs := make([]string, len(cols))
	for i := range phs {
		phs[i] = d.Ph(i + 1)
	}
	q := "INSERT INTO " + d.Qualify(schema, table) + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(phs, ", ") + ")"
	r, err := db.ExecContext(ctx, q, vals...)
	if err != nil {
		return 0, 0, err
	}
	n, _ := r.RowsAffected()
	id, _ := r.LastInsertId()
	return n, id, nil
}

// UpdateRow 按主键更新一行。
func UpdateRow(ctx context.Context, db *sql.DB, d Dialect, schema, table string, pk, values map[string]any) (int64, error) {
	det, err := d.Table(ctx, db, schema, table)
	if err != nil {
		return 0, err
	}
	if len(det.PK) == 0 {
		return 0, ErrNoPK
	}
	colset := map[string]bool{}
	for _, c := range det.Columns {
		colset[strings.ToLower(c.Name)] = true
	}
	sets := make([]string, 0, len(values))
	var args []any
	ph := 0
	for name, v := range values {
		if !colset[strings.ToLower(name)] {
			return 0, fmt.Errorf("字段不存在: %s", name)
		}
		ph++
		sets = append(sets, d.Quote(name)+" = "+d.Ph(ph))
		args = append(args, v)
	}
	if len(sets) == 0 {
		return 0, fmt.Errorf("没有要更新的字段")
	}
	where, wargs, err := pkWhere(d, det, pk, ph)
	if err != nil {
		return 0, err
	}
	args = append(args, wargs...)
	r, err := db.ExecContext(ctx, "UPDATE "+d.Qualify(schema, table)+" SET "+strings.Join(sets, ", ")+where, args...)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	return n, nil
}

// DeleteRow 按主键删除行。
func DeleteRow(ctx context.Context, db *sql.DB, d Dialect, schema, table string, pk map[string]any) (int64, error) {
	det, err := d.Table(ctx, db, schema, table)
	if err != nil {
		return 0, err
	}
	if len(det.PK) == 0 {
		return 0, ErrNoPK
	}
	where, args, err := pkWhere(d, det, pk, 0)
	if err != nil {
		return 0, err
	}
	r, err := db.ExecContext(ctx, "DELETE FROM "+d.Qualify(schema, table)+where, args...)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	return n, nil
}

// pkWhere 主键条件（引用列白名单即 PK 自身；值占位符）。
func pkWhere(d Dialect, det *TableDetail, pk map[string]any, phBase int) (string, []any, error) {
	var conds []string
	var args []any
	ph := phBase
	for _, col := range det.PK {
		v, ok := pk[col]
		if !ok {
			return "", nil, fmt.Errorf("缺少主键值: %s", col)
		}
		ph++
		conds = append(conds, d.Quote(col)+" = "+d.Ph(ph))
		args = append(args, v)
	}
	return " WHERE " + strings.Join(conds, " AND "), args, nil
}
