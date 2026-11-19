package dbtool

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"time"
)

// DialFunc 数据库底层网络拨号（直连为 net.Dialer；SSH 隧道为 client.Dial）。
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// dbtx 语句执行器：*sql.DB 与 *sql.Tx 都满足（备份在内省与取数同事务进行）。
type dbtx interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Dialect 一类数据库的方言实现：结构内省 + 带引号标识符 + 值字面量。
// 所有标识符必须经 Quote/Qualify 输出（引号内转义引号），值必须参数化或经
// Literal（仅备份导出用）。
type Dialect interface {
	Name() string
	Quote(ident string) string
	Ph(i int) string // 占位符：MySQL/SQLite 用 ?，PG 用 $n（i 从 1 起）
	// Schemas 可浏览的库名清单（MySQL=数据库，PG=schema，SQLite 固定 main）。
	Schemas(ctx context.Context, db *sql.DB) ([]string, error)
	Tables(ctx context.Context, q dbtx, schema string) ([]TableInfo, error)
	Table(ctx context.Context, q dbtx, schema, table string) (*TableDetail, error)
	Qualify(schema, table string) string
	// UseSchema 控制台按库执行时先跑的切库语句（MySQL=USE、PG=SET search_path；
	// 空串=无库概念不需要切，调用方据此跳过钉连接）。
	UseSchema(schema string) string
	// Columns 全库列清单（表名→列名按序，SQL 编辑器补全元数据用；含视图）。
	Columns(ctx context.Context, q dbtx, schema string) (map[string][]string, error)
	// DumpPreamble/Postamble 备份文件首尾的会话语句（外键约束等）。
	DumpPreamble() []string
	DumpPostamble() []string
	// Literal 值 → SQL 字面量（仅备份导出拼 INSERT 用，绝不经用户输入）。
	Literal(v any) string
	// TxReadOnly 备份一致性事务能否以只读模式开（MySQL InnoDB/PG 可以）。
	TxReadOnly() bool
}

// DialectFor 类型名 → 方言（未注册类型报错）。
func DialectFor(t string) (Dialect, error) {
	switch t {
	case "mysql":
		return mysqlDialect{}, nil
	case "postgres":
		return pgDialect{}, nil
	case "sqlite":
		return sqliteDialect{}, nil
	}
	return nil, fmt.Errorf("不支持的数据库类型: %s", t)
}

// stringsBackslashEscape 方言的单引号字符串里反斜杠是否为转义符：MySQL 默认是；
// SQLite 与 PG（standard_conforming_strings=on）不是。语句扫描器据此决定
// 「\x」是否吞掉下一个字符——口径错了会把分号藏进字符串绕过分类（见
// stripToTopLevel）。
func stringsBackslashEscape(d Dialect) bool { return d.Name() == "mysql" }

// systemSchemas 各驱动不展示的系统库（MySQL 内置四库不进浏览树）。
func systemSchemas(kind string) map[string]bool {
	switch kind {
	case "mysql":
		return map[string]bool{"information_schema": true, "mysql": true, "performance_schema": true, "sys": true}
	}
	return nil
}

// normalizeValue 驱动扫描值 → JSON 友好值：[]byte 按 UTF-8 尽量转字符串
// （MySQL 文本列多走 []byte），乱码/二进制给 hex 预览；时间转本地格式串。
func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		s := string(t)
		if isPrintableUTF8(s) {
			return s
		}
		return "0x" + strings.ToUpper(hexEncode(t))
	case time.Time:
		if t.Nanosecond() == 0 {
			return t.Format("2006-01-02 15:04:05")
		}
		return t.Format("2006-01-02 15:04:05.999999")
	default:
		return v
	}
}
