package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Group 服务器分组：Dashboard 按分组手风琴展示（列头含总数/在线/告警数）。
type Group struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// ErrGroupNameExists 分组重名（UNIQUE 约束），api 层转 400。
var ErrGroupNameExists = errors.New("分组名称已存在")

// ListGroups 返回全部分组，按创建顺序。
func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at FROM server_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

// GetGroup 按 ID 查分组，未命中返回 (nil, nil)。
func (s *Store) GetGroup(id int64) (*Group, error) {
	var g Group
	err := s.db.QueryRow(`SELECT id, name, created_at FROM server_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// CreateGroup 新建分组；重名返回 ErrGroupNameExists。
func (s *Store) CreateGroup(name string) (*Group, error) {
	res, err := s.db.Exec(
		`INSERT INTO server_groups (name, created_at) VALUES (?, ?)`, name, time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrGroupNameExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetGroup(id)
}

// RenameGroup 改分组名；重名返回 ErrGroupNameExists。
func (s *Store) RenameGroup(id int64, name string) error {
	res, err := s.db.Exec(`UPDATE server_groups SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrGroupNameExists
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteGroup 删除分组（单事务）：组内服务器先移回未分组（group_id=0），再删分组。
func (s *Store) DeleteGroup(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE servers SET group_id = 0 WHERE group_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM server_groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}
