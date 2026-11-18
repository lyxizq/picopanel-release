package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// TermScript 每服务器专属脚本（SSH 终端页管理与查看，内容入库）。
type TermScript struct {
	ID        int64  `json:"id"`
	ServerID  int64  `json:"server_id"`
	Name      string `json:"name"`
	Lang      string `json:"lang"` // 前端高亮语言，默认 shell
	Content   string `json:"content"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// ErrTermScriptNameExists 同一服务器内脚本重名（UNIQUE 约束），api 层转 400。
var ErrTermScriptNameExists = errors.New("脚本名称已存在")

// ListTermScripts 一台服务器的全部脚本，按更新时间倒序。
func (s *Store) ListTermScripts(serverID int64) ([]TermScript, error) {
	rows, err := s.db.Query(`SELECT id, server_id, name, lang, content, created_at, updated_at
		FROM term_scripts WHERE server_id = ? ORDER BY updated_at DESC, id DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []TermScript
	for rows.Next() {
		var t TermScript
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.Lang, &t.Content, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// GetTermScript 按 ID 取脚本（serverID 一并校验，防跨服务器越权），未命中返回 (nil, nil)。
func (s *Store) GetTermScript(id, serverID int64) (*TermScript, error) {
	var t TermScript
	err := s.db.QueryRow(`SELECT id, server_id, name, lang, content, created_at, updated_at
		FROM term_scripts WHERE id = ? AND server_id = ?`, id, serverID).
		Scan(&t.ID, &t.ServerID, &t.Name, &t.Lang, &t.Content, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateTermScript 新建脚本；同服务器重名返回 ErrTermScriptNameExists。
// lang 为空时落默认 shell（列默认值只管 INSERT 缺列，这里显式兜住空串）。
func (s *Store) CreateTermScript(serverID int64, name, lang, content string) (*TermScript, error) {
	if lang == "" {
		lang = "shell"
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO term_scripts (server_id, name, lang, content, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		serverID, name, lang, content, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrTermScriptNameExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetTermScript(id, serverID)
}

// UpdateTermScript 保存脚本（编辑页整份提交：名称/语言/内容一次更新）。
// 同服务器重名返回 ErrTermScriptNameExists；lang 空串回落 shell。
func (s *Store) UpdateTermScript(id, serverID int64, name, lang, content string) error {
	if lang == "" {
		lang = "shell"
	}
	res, err := s.db.Exec(`UPDATE term_scripts SET name = ?, lang = ?, content = ?, updated_at = ?
		WHERE id = ? AND server_id = ?`, name, lang, content, time.Now().Unix(), id, serverID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrTermScriptNameExists
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteTermScript 删除脚本（serverID 一并校验）。
func (s *Store) DeleteTermScript(id, serverID int64) error {
	res, err := s.db.Exec(`DELETE FROM term_scripts WHERE id = ? AND server_id = ?`, id, serverID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TermHistoryMax 服务端保留的补全历史上限（与前端 HIST_MAX 一致）。
const TermHistoryMax = 100

// GetTermHistory 返回某服务器的补全历史（新→旧），无记录时返回空。
func (s *Store) GetTermHistory(serverID int64) ([]string, error) {
	var raw string
	err := s.db.QueryRow(`SELECT items FROM term_history WHERE server_id = ?`, serverID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, nil // 坏数据视同无历史
	}
	return items, nil
}

// SaveTermHistory 覆盖保存补全历史，服务端裁剪至上限。
func (s *Store) SaveTermHistory(serverID int64, items []string) error {
	if len(items) > TermHistoryMax {
		items = items[:TermHistoryMax]
	}
	buf, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO term_history (server_id, items, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET items = excluded.items, updated_at = excluded.updated_at`,
		serverID, string(buf), time.Now().Unix())
	return err
}

// FileFav 文件管理收藏条目（path 绝对路径，name 展示名=路径末段）。
type FileFav struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// FileFavsMax 每服务器收藏上限（收藏都是常用目录短列表，50 条足够）。
const FileFavsMax = 50

// GetFileFavs 返回某服务器的收藏列表，无记录时返回空。
func (s *Store) GetFileFavs(serverID int64) ([]FileFav, error) {
	var raw string
	err := s.db.QueryRow(`SELECT items FROM file_favs WHERE server_id = ?`, serverID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []FileFav
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, nil // 坏数据视同无收藏
	}
	return items, nil
}

// SaveFileFavs 覆盖保存收藏，服务端裁剪至上限。
func (s *Store) SaveFileFavs(serverID int64, items []FileFav) error {
	if len(items) > FileFavsMax {
		items = items[:FileFavsMax]
	}
	buf, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO file_favs (server_id, items, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET items = excluded.items, updated_at = excluded.updated_at`,
		serverID, string(buf), time.Now().Unix())
	return err
}
