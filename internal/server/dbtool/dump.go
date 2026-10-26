package dbtool

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// BackupFile 一个备份文件（列表/下载/删除用）。
type BackupFile struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

// dumpBatchRows 单条多值 INSERT 聚合的行数（兼顾单语句体积与还原速度）。
const dumpBatchRows = 100

// Dump 备份导出：一致性事务内（MySQL/PG 只读事务，SQLite 单连接天然快照）
// 流式写 DDL + 批量 INSERT 到 w（gzip 由调用方包装）。tables 空=全部基础表
// （视图不导——数据编辑与导出都以基础表为准）。
func Dump(ctx context.Context, db *sql.DB, d Dialect, schema string, tables []string, w io.Writer, label string) error {
	var tx *sql.Tx
	var err error
	if d.TxReadOnly() {
		if tx, err = db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true}); err != nil {
			tx, err = db.BeginTx(ctx, nil)
		}
	} else {
		tx, err = db.BeginTx(ctx, nil)
	}
	if err != nil {
		return err
	}
	defer tx.Rollback()

	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "-- PicoPanel 数据库备份（%s）\n-- 生成时间: %s\n", label, time.Now().Format("2006-01-02 15:04:05"))
	for _, line := range d.DumpPreamble() {
		bw.WriteString(line + "\n")
	}

	list := tables
	if len(list) == 0 {
		infos, err := d.Tables(ctx, tx, schema)
		if err != nil {
			return err
		}
		for _, t := range infos {
			if !t.View {
				list = append(list, t.Name)
			}
		}
	}
	sort.Strings(list)

	for _, table := range list {
		det, err := d.Table(ctx, tx, schema, table)
		if err != nil {
			return fmt.Errorf("内省表 %s: %w", table, err)
		}
		if det.View {
			continue
		}
		if err := dumpTable(ctx, tx, bw, d, det, schema); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	for _, line := range d.DumpPostamble() {
		bw.WriteString(line + "\n")
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return tx.Commit()
}

// dumpTable 单表：DDL + 非约束索引 + 数据（多值 INSERT 聚合，流式不驻留全表）。
func dumpTable(ctx context.Context, q dbtx, bw *bufio.Writer, d Dialect, det *TableDetail, schema string) error {
	fmt.Fprintf(bw, "\n-- ---------------- 表 %s ----------------\n", det.Name)
	if ddl := strings.TrimRight(strings.TrimSpace(det.DDL), ";"); ddl != "" {
		bw.WriteString(ddl + ";\n")
	}
	for _, idx := range det.Indexes {
		if idx.Auto || idx.Primary {
			continue
		}
		switch d.Name() {
		case "postgres", "sqlite":
			if idx.Def != "" {
				bw.WriteString(strings.TrimRight(idx.Def, ";") + ";\n")
			}
		}
		// MySQL 的 SHOW CREATE TABLE 已含全部索引，无需补
	}

	qual := d.Qualify(schema, det.Name)
	cols := make([]string, len(det.Columns))
	for i, c := range det.Columns {
		cols[i] = d.Quote(c.Name)
	}
	rows, err := q.QueryContext(ctx, "SELECT "+strings.Join(cols, ", ")+" FROM "+qual)
	if err != nil {
		return fmt.Errorf("读取表 %s: %w", det.Name, err)
	}
	defer rows.Close()

	scan := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range scan {
		ptrs[i] = &scan[i]
	}
	batch := make([]string, 0, dumpBatchRows)
	head := "INSERT INTO " + qual + " (" + strings.Join(cols, ", ") + ") VALUES "
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if _, err := bw.WriteString(head + strings.Join(batch, ",\n") + ";\n"); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		vals := make([]string, len(scan))
		for i, v := range scan {
			vals[i] = d.Literal(v)
		}
		batch = append(batch, "("+strings.Join(vals, ", ")+")")
		if len(batch) >= dumpBatchRows {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return flush()
}

// ---- 备份文件管理（落 <备份根目录>/<连接ID>/，gzip 写临时文件后改名）----

// backupNameFmt 文件名白名单（防路径穿越）。
var backupNameFmt = regexp.MustCompile(`^[\w\-.\[\]()]+$`)

// CreateBackup 触发一次备份。schema 空走方言默认；tables 空=整库。
func (m *Manager) CreateBackup(ctx context.Context, connID int64, schema string, tables []string) (*BackupFile, error) {
	db, cfg, err := m.Conn(ctx, connID)
	if err != nil {
		return nil, err
	}
	d, err := DialectFor(cfg.Type)
	if err != nil {
		return nil, err
	}
	if schema == "" {
		schema = cfg.Database
		if cfg.Type == "postgres" {
			schema = "public"
		}
	}
	label := fmt.Sprintf("%s %s %s", cfg.Name, cfg.Type, schema)
	dir := filepath.Join(m.dir, fmt.Sprintf("%d", connID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	stem := time.Now().Format("20060102-150405")
	if schema != "" && cfg.Type != "postgres" {
		stem += "-" + sanitizeStem(schema)
	}
	tmp := filepath.Join(dir, stem+".sql.gz.tmp")
	final := filepath.Join(dir, stem+".sql.gz")
	defer os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	gz := gzip.NewWriter(f)
	err = Dump(ctx, db, d, schema, tables, gz, label)
	if err == nil {
		err = gz.Close()
	}
	if err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	return &BackupFile{Name: filepath.Base(final), Size: fileSize(final), MTime: time.Now().Unix()}, nil
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// sanitizeStem 文件名里的库名只留安全字符。
func sanitizeStem(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		out = "db"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

// ListBackups 备份清单。
func (m *Manager) ListBackups(connID int64) ([]BackupFile, error) {
	dir := filepath.Join(m.dir, fmt.Sprintf("%d", connID))
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupFile{}, nil
		}
		return nil, err
	}
	out := []BackupFile{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupFile{Name: e.Name(), Size: info.Size(), MTime: info.ModTime().Unix()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// BackupPath 校验文件名后给出备份文件全路径（下载用）。
func (m *Manager) BackupPath(connID int64, name string) (string, error) {
	if !backupNameFmt.MatchString(name) || !strings.HasSuffix(name, ".sql.gz") {
		return "", fmt.Errorf("非法备份文件名")
	}
	p := filepath.Join(m.dir, fmt.Sprintf("%d", connID), name)
	if filepath.Dir(p) != filepath.Join(m.dir, fmt.Sprintf("%d", connID)) {
		return "", fmt.Errorf("非法备份文件名")
	}
	return p, nil
}

// DeleteBackup 删除一个备份文件。
func (m *Manager) DeleteBackup(connID int64, name string) error {
	p, err := m.BackupPath(connID, name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// DeleteConnBackups 删除连接时连带清备份目录。
func (m *Manager) DeleteConnBackups(connID int64) {
	_ = os.RemoveAll(filepath.Join(m.dir, fmt.Sprintf("%d", connID)))
}

// ---- SQL 导入 ----

// Import 逐条执行导入 SQL（自动跳过纯注释片段；出错即停并带语句序号）。
// 事务口径：不包整体事务（大文件内存/日志不可控），每条自动提交——导入前
// 请先备份（工具自身有备份功能，形成闭环）。
func Import(ctx context.Context, db *sql.DB, d Dialect, content string, onProgress func(executed int)) (int, error) {
	esc := stringsBackslashEscape(d)
	stmts := SplitStatements(content, esc)
	executed := 0
	for i, s := range stmts {
		clean, multi, err := stripToTopLevel(s, esc)
		if err != nil || multi {
			return executed, fmt.Errorf("第 %d 条语句解析失败（空语句/多语句）", i+1)
		}
		if len(topWords(clean)) == 0 {
			continue // 纯注释/空白
		}
		if _, err := db.ExecContext(ctx, s); err != nil {
			return executed, fmt.Errorf("第 %d 条语句执行失败: %w", i+1, err)
		}
		executed++
		if onProgress != nil && executed%200 == 0 {
			onProgress(executed)
		}
	}
	return executed, nil
}

// SplitStatements 顶层分号切分（引号/注释感知，切出的片段保留原文本）。
// backslashEscape 口径同 stripToTopLevel。
func SplitStatements(content string, backslashEscape bool) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		s := strings.TrimSpace(b.String())
		b.Reset()
		if s != "" {
			out = append(out, s)
		}
	}
	runes := []rune(content)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '\'':
			b.WriteRune(r)
			for i+1 < len(runes) {
				i++
				b.WriteRune(runes[i])
				if backslashEscape && runes[i] == '\\' && i+1 < len(runes) {
					i++
					b.WriteRune(runes[i])
					continue
				}
				if runes[i] == '\'' {
					if i+1 < len(runes) && runes[i+1] == '\'' {
						i++
						b.WriteRune(runes[i])
						continue
					}
					break
				}
			}
		case '"', '`':
			q := r
			b.WriteRune(r)
			for i+1 < len(runes) {
				i++
				b.WriteRune(runes[i])
				if runes[i] == q {
					if q == '"' && i+1 < len(runes) && runes[i+1] == '"' {
						i++
						b.WriteRune(runes[i])
						continue
					}
					break
				}
			}
		case '-':
			if i+1 < len(runes) && runes[i+1] == '-' {
				for i+1 < len(runes) && runes[i] != '\n' {
					i++
				}
				b.WriteRune('\n')
			} else {
				b.WriteRune(r)
			}
		case '/':
			if i+1 < len(runes) && runes[i+1] == '*' {
				i += 2
				for i+1 < len(runes) && !(runes[i] == '*' && runes[i+1] == '/') {
					i++
				}
				i++
				b.WriteRune(' ')
			} else {
				b.WriteRune(r)
			}
		case ';':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}
