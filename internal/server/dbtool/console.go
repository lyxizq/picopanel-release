package dbtool

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// stmtKind 语句类别：read 可直接执行；write 数据变更；ddl 结构变更；
// txn 手动事务（一律拒绝）。
type stmtKind int

const (
	kindRead stmtKind = iota
	kindWrite
	kindDDL
	kindTxn
)

// classified 分类结果。
type classified struct {
	verb        string
	kind        stmtKind
	needConfirm bool
	reason      string
}

// ConsoleMaxRows 控制台默认行数上限；绝对上限。
const (
	ConsoleMaxRows    = 200
	ConsoleMaxRowsAbs = 1000
	consoleTimeout    = 30 * time.Second
)

// classify 语句分类：只读连接的白名单判断与危险语句 needConfirm 判定共用。
// 剥掉注释与字符串字面量后看顶层关键字（引号内出现 WHERE/分号不影响判断）。
// 扫描口径按方言传（见 stripToTopLevel）。
func classify(d Dialect, text string) (classified, error) {
	// MySQL 版本定向注释 /*!50000 ...*/：对白名单扫描是不可见注释、对服务端是
	// 有效 SQL（SELECT ... /*!50000INTO OUTFILE*/ 走私写文件）——直接拒绝，
	// 正常查询不会用到该语法
	if strings.Contains(text, "/*!") {
		return classified{}, ErrMultiStmt
	}
	clean, multi, err := stripToTopLevel(text, stringsBackslashEscape(d))
	if err != nil {
		return classified{}, err
	}
	if multi {
		return classified{}, ErrMultiStmt
	}
	fields := topWords(clean)
	if len(fields) == 0 {
		return classified{}, ErrMultiStmt // 空语句/纯注释
	}
	verb := strings.ToUpper(fields[0])
	st := classified{verb: verb}
	switch verb {
	case "BEGIN", "START", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE":
		return st, ErrTxnNotAllowed
	case "SELECT", "SHOW", "DESC", "DESCRIBE", "EXPLAIN":
		st.kind = kindRead
		// 「读语句」的写副作用补刀：
		// - INTO：MySQL SELECT ... INTO OUTFILE/DUMPFILE 在库服务器上写文件，
		//   PG SELECT ... INTO 建表——统一按写拒绝（宁可误杀 MySQL 的 INTO @var）。
		// - EXPLAIN ANALYZE（PG）：真实执行语句，数据修改 CTE 借道完成写入。
		// - 文件读/外联函数：读语句内可读库主机文件或发起外联，按写拒绝。
		if hasTopWord(clean, "INTO") || hasTopWord(clean, "OUTFILE") || hasTopWord(clean, "DUMPFILE") ||
			hasReadFunc(clean) ||
			(verb == "EXPLAIN" && hasTopWord(clean, "ANALYZE")) {
			st.kind = kindWrite
		}
	case "WITH":
		// CTE：无顶层写动词才视为读（WITH ... INSERT 等归写）
		st.kind = kindRead
		for _, w := range fields {
			switch w {
			case "INSERT", "UPDATE", "DELETE", "REPLACE", "MERGE", "TRUNCATE", "COPY", "VACUUM", "CREATE", "DROP", "ALTER",
				"INTO", "OUTFILE", "DUMPFILE": // CTE 内 SELECT ... INTO 在 PG 也是写
				st.kind = kindWrite
			}
		}
		if hasReadFunc(clean) {
			st.kind = kindWrite
		}
	case "UPDATE", "DELETE":
		st.kind = kindWrite
		if !hasTopWord(clean, "WHERE") {
			st.needConfirm = true
			st.reason = "该 " + verb + " 语句没有 WHERE 条件，将影响全部行"
		}
	case "DROP", "TRUNCATE", "ALTER", "RENAME", "GRANT", "REVOKE", "VACUUM", "ANALYZE", "CREATE":
		st.kind = kindDDL
		st.needConfirm = true
		st.reason = "结构/权限变更语句（" + verb + "），确认执行"
	default:
		st.kind = kindWrite
	}
	return st, nil
}

// readFuncs 读语句内的库主机文件读/外联函数（MySQL/PG 并集）：LOAD_FILE、
// pg_read_file 族读任意文件、dblink 发起外联。列名/别名撞上属罕见，宁误杀。
var readFuncs = []string{"load_file", "pg_read_file", "pg_read_binary_file", "pg_ls_dir", "dblink"}

func hasReadFunc(clean string) bool {
	low := strings.ToLower(clean)
	for _, f := range readFuncs {
		if strings.Contains(low, f) {
			return true
		}
	}
	return false
}

// RunConsole 执行控制台语句：只读连接拒绝非读；危险语句带 needConfirm 返回
// （调用方拿到用户确认后带 confirmed=true 重入）。读语句限行、全量 30s 超时。
// schema 非空且方言有切库语句时，从池里钉住一条连接先切库再执行（连接池下
// 会话级语句只影响所拿到的单条连接，与只读改判同坑，必须钉住复用同一条）。
func RunConsole(ctx context.Context, db *sql.DB, d Dialect, readOnly bool, query string, maxRows int, confirmed bool, schema string) (*ConsoleResult, error) {
	st, err := classify(d, query)
	if err != nil {
		return nil, err
	}
	if readOnly && st.kind != kindRead {
		return nil, ErrReadOnly
	}
	if st.needConfirm && !confirmed {
		return &ConsoleResult{NeedConfirm: true, Reason: st.reason}, nil
	}
	if maxRows <= 0 || maxRows > ConsoleMaxRowsAbs {
		maxRows = ConsoleMaxRows
	}
	cctx, cancel := context.WithTimeout(ctx, consoleTimeout)
	defer cancel()
	var ex dbtx = db
	if schema != "" {
		if use := d.UseSchema(schema); use != "" {
			conn, err := db.Conn(cctx)
			if err != nil {
				return nil, err
			}
			defer conn.Close()
			if _, err := conn.ExecContext(cctx, use); err != nil {
				return nil, fmt.Errorf("切换库失败: %w", err)
			}
			ex = conn
		}
	}
	start := time.Now()
	res := &ConsoleResult{}
	if st.kind == kindRead {
		rows, err := ex.QueryContext(cctx, query)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		res.Columns = cols
		for rows.Next() {
			if len(res.Rows) >= maxRows {
				res.Truncated = true
				break
			}
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
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		r, err := ex.ExecContext(cctx, query)
		if err != nil {
			return nil, err
		}
		if n, err := r.RowsAffected(); err == nil {
			res.Affected = n
		}
	}
	res.ElapsedMS = time.Since(start).Milliseconds()
	return res, nil
}

// stripToTopLevel 剥注释与字符串/引号标识符（内容以空格占位，不进顶层文本），
// 返回顶层文本与是否含多条语句（顶层分号且不在句尾）。单引号串内反斜杠是否
// 当转义符按方言传：MySQL 是；SQLite 与 PG（standard_conforming_strings=on）
// 不是——按转义扫会把关闭引号吃掉、其后的分号整段「藏进未结束的字符串」
// 绕过多语句/只读判断（2026-10-04 安全审查实锤：modernc 驱动整串执行全部
// 语句，DELETE 借道跑掉）。
func stripToTopLevel(text string, backslashEscape bool) (string, bool, error) {
	var b strings.Builder
	depth := 0
	semi := false
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '\'':
			b.WriteByte(' ') // 字符串内容不进顶层文本：关键字只认顶层词
			for i+1 < len(runes) {
				i++
				if backslashEscape && runes[i] == '\\' && i+1 < len(runes) { // 反斜杠转义跳下一字符
					i++
					continue
				}
				if runes[i] == '\'' {
					if i+1 < len(runes) && runes[i+1] == '\'' { // '' 双写
						i++
						continue
					}
					break
				}
			}
		case '"', '`':
			q := r
			b.WriteByte(' ')
			for i+1 < len(runes) {
				i++
				if runes[i] == q {
					if q == '"' && i+1 < len(runes) && runes[i+1] == '"' { // "" 双写
						i++
						continue
					}
					break
				}
			}
		case '-':
			if i+1 < len(runes) && runes[i+1] == '-' { // 行注释
				for i+1 < len(runes) && runes[i] != '\n' {
					i++
				}
				b.WriteRune('\n')
			} else {
				b.WriteRune(r)
			}
		case '/':
			if i+1 < len(runes) && runes[i+1] == '*' { // 块注释
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
			rest := strings.TrimLeftFunc(string(runes[i+1:]), unicode.IsSpace)
			if rest != "" {
				semi = true
			}
			b.WriteRune(' ')
		case '(':
			depth++
			b.WriteRune(r)
		case ')':
			if depth > 0 {
				depth--
			}
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	if depth != 0 {
		return "", false, ErrMultiStmt // 括号不配对当多语句错误处理（口径：拒绝执行）
	}
	return b.String(), semi, nil
}

// topWords 顶层文本里的全部单词（大写）。
func topWords(clean string) []string {
	return strings.FieldsFunc(strings.ToUpper(clean), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
}

// hasTopWord 顶层（括号深度 0）文本中是否出现某关键字。
// 简化口径：strip 后子查询在括号里，直接在整体单词流中查即近似顶层；
// UPDATE/DELETE 缺 WHERE 的高危判定宁可误报（弹确认）不可漏报。
func hasTopWord(clean, word string) bool {
	for _, w := range topWords(clean) {
		if w == word {
			return true
		}
	}
	return false
}
