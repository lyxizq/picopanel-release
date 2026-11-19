// Package dbtool 数据库管理工具（323-325）：外部数据库连接的池化管理、
// 结构浏览、数据行增删改查、SQL 控制台与纯 Go 备份导入。
//
// 三驱动（mysql/postgres/sqlite）经 Dialect 抽象统一：标识符一律带引号
// 防注入，值一律占位符参数化；只读连接在服务端硬约束（SQLite mode=ro、
// PG default_transaction_read_only、MySQL 语句白名单）。
package dbtool

import "errors"

// 表信息（Tables 列表行）。
type TableInfo struct {
	Name    string `json:"name"`
	View    bool   `json:"view"`
	Rows    int64  `json:"rows"`     // 近似值（InnoDB/PG 统计）；NoCount=true 时无意义
	NoCount bool   `json:"no_count"` // 无法廉价取行数（SQLite 列表阶段不 COUNT）
	Comment string `json:"comment"`
}

// 列信息。
type ColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default"`
	AutoInc  bool   `json:"auto_inc"`
	Comment  string `json:"comment"`
}

// 索引信息。
type IndexInfo struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	Auto    bool     `json:"-"` // 约束自带索引（建表 DDL 已含，备份导出时跳过）
	Columns []string `json:"columns"`
	Def     string   `json:"def"` // 原始定义（PG indexdef / 其余为拼装）
}

// 表详情（结构/索引/DDL/主键/行数）。
type TableDetail struct {
	Schema   string       `json:"schema"`
	Name     string       `json:"name"`
	Columns  []ColumnInfo `json:"columns"`
	Indexes  []IndexInfo  `json:"indexes"`
	PK       []string     `json:"pk"`
	DDL      string       `json:"ddl"`
	RowCount int64        `json:"row_count"`
	NoCount  bool         `json:"no_count"`
	View     bool         `json:"view"`
}

// 数据页查询结果。
type PageResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Total   int64    `json:"total"`
	Page    int      `json:"page"`
	Size    int      `json:"size"`
	PK      []string `json:"pk"`
}

// 控制台执行结果。NeedConfirm=true 时其余字段无意义（危险语句待确认）。
type ConsoleResult struct {
	Columns     []string `json:"columns"`
	Rows        [][]any  `json:"rows"`
	Affected    int64    `json:"affected"`
	ElapsedMS   int64    `json:"elapsed_ms"`
	Truncated   bool     `json:"truncated"`
	NeedConfirm bool     `json:"need_confirm"`
	Reason      string   `json:"reason"`
}

// 领域错误（API 层转 4xx 提示，不落 500）。
var (
	ErrReadOnly      = errors.New("连接为只读，不允许写操作")
	ErrNoPK          = errors.New("该表没有主键，无法行级编辑")
	ErrMultiStmt     = errors.New("控制台一次只能执行一条语句")
	ErrTxnNotAllowed = errors.New("控制台不支持手动事务（BEGIN/COMMIT/ROLLBACK）")
	ErrUnknownTable  = errors.New("表不存在")
)
