package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// 网站检查（服务监控页新模块，2026-11-03）：web_checks 记监控目标（URL + 关键字
// 清单换行分隔），管理器每 2 小时抓一次页面做「全部关键字都包含」判定（且逻辑），
// 状态只存最新一份（同服务监控端口行口径），翻转由管理器即时推送/站内通知。

const (
	// MaxWebChecks 监控目标上限（对齐服务监控服务器/GitHub 仓库上限）。
	MaxWebChecks = 16
	// MaxWebRules 单目标规则上限（规则间「或」）。
	MaxWebRules = 10
	// MaxWebKeywords 单条规则内关键字上限（规则内「且」）。
	MaxWebKeywords = 20
	// MaxWebKeywordLen 单个关键字长度上限（rune）。
	MaxWebKeywordLen = 100
)

// WebCheck 对应 web_checks 一行。Rules 为 JSON 规则组（[][]string：规则内关键字
// 全部包含才算该规则通过（且），任一规则通过即目标通过（或），2026-11-03 改版；
// 存量单规则由启动迁移 keywords 列换名+内容转 JSON）；Up 三态（1=有规则通过/
// 0=全部规则未通过/-1=未检测）；Since 为当前状态起始时刻。
type WebCheck struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Rules     string `json:"rules"`
	Enabled   bool   `json:"enabled"`
	Up        int    `json:"up"`
	LastError string `json:"last_error"`
	CheckedAt int64  `json:"checked_at"`
	LastOKAt  int64  `json:"last_ok_at"`
	Since     int64  `json:"since"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// RuleList 拆规则组：JSON 解析（[[k...],[k...]]）；解析失败按旧版单规则兜底
// （换行分隔关键字整体作一条规则，防御迁移前的行）。
func (w *WebCheck) RuleList() [][]string {
	var rules [][]string
	if err := json.Unmarshal([]byte(w.Rules), &rules); err == nil {
		return rules
	}
	if kw := SplitWebKeywords(w.Rules); len(kw) > 0 {
		return [][]string{kw}
	}
	return nil
}

// SplitWebKeywords 换行分隔的关键字串 → 清单（空行丢弃；旧版单规则迁移兜底用）。
func SplitWebKeywords(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		k := strings.TrimSpace(line)
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

// ErrWebCheckNotFound 监控目标不存在。
var ErrWebCheckNotFound = errors.New("网站检查目标不存在")

// ErrWebCheckExists 同地址已在监控清单中。
var ErrWebCheckExists = errors.New("该地址已在网站检查清单中")

const webChecksDDL = `CREATE TABLE IF NOT EXISTS web_checks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL,
	rules TEXT NOT NULL DEFAULT '[]',
	enabled INTEGER NOT NULL DEFAULT 1,
	up INTEGER NOT NULL DEFAULT -1,
	last_error TEXT NOT NULL DEFAULT '',
	checked_at INTEGER NOT NULL DEFAULT 0,
	last_ok_at INTEGER NOT NULL DEFAULT 0,
	since INTEGER NOT NULL DEFAULT 0,
	sort INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	UNIQUE(url)
)`

const webCheckCols = `id, name, url, rules, enabled, up, last_error, checked_at, last_ok_at, since, created_at, updated_at`

func scanWebCheck(row interface{ Scan(...any) error }) (*WebCheck, error) {
	var (
		w       WebCheck
		enabled int
	)
	if err := row.Scan(&w.ID, &w.Name, &w.URL, &w.Rules, &enabled, &w.Up, &w.LastError, &w.CheckedAt, &w.LastOKAt, &w.Since, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return nil, err
	}
	w.Enabled = enabled != 0
	return &w, nil
}

// ListWebChecks 全部监控目标，按排序（拖拽序，未排过按创建序）。
func (s *Store) ListWebChecks() ([]WebCheck, error) {
	rows, err := s.db.Query(`SELECT ` + webCheckCols + ` FROM web_checks ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebCheck
	for rows.Next() {
		w, err := scanWebCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// CountWebChecks 监控目标数（上限校验用）。
func (s *Store) CountWebChecks() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM web_checks`).Scan(&n)
	return n, err
}

// GetWebCheck 单个监控目标。
func (s *Store) GetWebCheck(id int64) (*WebCheck, error) {
	w, err := scanWebCheck(s.db.QueryRow(`SELECT `+webCheckCols+` FROM web_checks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebCheckNotFound
	}
	return w, err
}

// GetWebCheckByUrl 按地址查重（不存在返回 ErrWebCheckNotFound）。
func (s *Store) GetWebCheckByUrl(url string) (*WebCheck, error) {
	w, err := scanWebCheck(s.db.QueryRow(`SELECT `+webCheckCols+` FROM web_checks WHERE url = ?`, url))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebCheckNotFound
	}
	return w, err
}

// CreateWebCheck 新建监控目标（地址唯一，撞唯一约束回 ErrWebCheckExists）。
func (s *Store) CreateWebCheck(w *WebCheck) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO web_checks (name, url, rules, enabled, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(sort),0)+1 FROM web_checks), ?, ?)`,
		w.Name, w.URL, w.Rules, boolInt(w.Enabled), now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrWebCheckExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateWebCheck 编辑监控目标。地址/规则任一变更即重置检测态（新目标新口径，
// 带旧结论展示会误导）；只改名称/启停则保留。
func (s *Store) UpdateWebCheck(w *WebCheck, resetState bool) error {
	now := time.Now().Unix()
	var exec error
	if resetState {
		_, exec = s.db.Exec(`UPDATE web_checks SET name = ?, url = ?, rules = ?, enabled = ?,
			up = -1, last_error = '', checked_at = 0, last_ok_at = 0, since = 0, updated_at = ? WHERE id = ?`,
			w.Name, w.URL, w.Rules, boolInt(w.Enabled), now, w.ID)
	} else {
		_, exec = s.db.Exec(`UPDATE web_checks SET name = ?, url = ?, rules = ?, enabled = ?, updated_at = ? WHERE id = ?`,
			w.Name, w.URL, w.Rules, boolInt(w.Enabled), now, w.ID)
	}
	if exec != nil {
		return exec
	}
	return nil
}

// DeleteWebCheck 删除监控目标。
func (s *Store) DeleteWebCheck(id int64) error {
	res, err := s.db.Exec(`DELETE FROM web_checks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrWebCheckNotFound
	}
	return nil
}

// RecordWebCheck 记一次检测结果：up/checked_at 每轮覆盖；last_ok_at 只在成功时
// 推进；since 仅状态变化时重置（UPDATE 内引用旧 up 比较，同 RecordSvcCheck 口径）。
// 返回本次是否发生状态翻转（旧态 -1 未检测不算；行不存在=刚被删，报错）。
func (s *Store) RecordWebCheck(id int64, ok bool, errMsg string) (bool, error) {
	now := time.Now().Unix()
	upVal := 0
	if ok {
		upVal = 1
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var old int64
	if err := tx.QueryRow(`SELECT up FROM web_checks WHERE id = ?`, id).Scan(&old); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE web_checks SET
		up = ?, checked_at = ?,
		last_ok_at = CASE WHEN ? = 1 THEN ? ELSE last_ok_at END,
		last_error = ?,
		since = CASE WHEN up != ? THEN ? ELSE since END
		WHERE id = ?`, upVal, now, upVal, now, errMsg, upVal, now, id); err != nil {
		return false, err
	}
	flipped := old != -1 && old != int64(upVal)
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return flipped, nil
}

// ReorderWebChecks 按 ids 顺序落拖拽排序。
func (s *Store) ReorderWebChecks(ids []int64) error { return s.reorderRows("web_checks", ids) }

// MigrateWebCheckRules 多规则改版迁移（2026-11-03，幂等）：首版 keywords 列存
// 换行分隔的单组关键字，改版后为 rules 列存 JSON 规则组——存量库换列名并把每
// 行内容转成单条规则的 JSON（json.Unmarshal 失败才转，已转过的行不动）。
func MigrateWebCheckRules(db *sql.DB) error {
	if !hasColumn(db, "web_checks", "keywords") {
		return nil // 新结构（建表即 rules 列）或表不存在
	}
	if !hasColumn(db, "web_checks", "rules") {
		if _, err := db.Exec(`ALTER TABLE web_checks RENAME COLUMN keywords TO rules`); err != nil {
			return err
		}
	}
	rows, err := db.Query(`SELECT id, rules FROM web_checks`)
	if err != nil {
		return err
	}
	type fix struct {
		id    int64
		rules string
	}
	var todo []fix
	for rows.Next() {
		var f fix
		if err := rows.Scan(&f.id, &f.rules); err != nil {
			rows.Close()
			return err
		}
		var rules [][]string
		if err := json.Unmarshal([]byte(f.rules), &rules); err == nil {
			continue // 已是 JSON 规则组
		}
		if kw := SplitWebKeywords(f.rules); len(kw) > 0 {
			b, _ := json.Marshal([][]string{kw})
			f.rules = string(b)
			todo = append(todo, f)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, f := range todo {
		if _, err := db.Exec(`UPDATE web_checks SET rules = ? WHERE id = ?`, f.rules, f.id); err != nil {
			return err
		}
	}
	return nil
}
