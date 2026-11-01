package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// DocGroup 文档分组。
type DocGroup struct {
	ID        int64  `json:"id"`
	CreatedAt int64  `json:"created_at"`
	Name      string `json:"name"`
}

// Doc 文档。列表与详情共用：列表不填 Content。
type Doc struct {
	ID        int64  `json:"id"`
	GroupID   int64  `json:"group_id"`
	Title     string `json:"title"`
	Format    string `json:"format"` // richtext | md
	Content   string `json:"content,omitempty"`
	Size      int    `json:"size"` // 正文字节数（列表展示用；列表查询不带 content，由 LENGTH(content) 单独算）
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	DeletedAt int64  `json:"deleted_at"` // 0=正常；>0=删除时间戳（回收站）
}

// DocVersion 文档历史版本（覆盖 content 前的旧内容快照）。列表不带 Content。
type DocVersion struct {
	ID        int64  `json:"id"`
	DocID     int64  `json:"doc_id"`
	Title     string `json:"title"`
	Content   string `json:"content,omitempty"`
	Size      int    `json:"size"` // 字节数（列表展示用）
	CreatedAt int64  `json:"created_at"`
}

// ErrDocGroupNameExists 文档分组重名（UNIQUE 约束），api 层转 400。
var ErrDocGroupNameExists = errors.New("分组名称已存在")

// ListDocTree 返回全部分组 + 全部文档元数据（不含 content，不含回收站），
// 手动拖拽序（sort>0）在前按其升序，未排序（sort=0）在后按更新时间倒序
// （前端按 group_id 归并）。
func (s *Store) ListDocTree() ([]DocGroup, []Doc, error) {
	groups, err := s.ListDocGroups()
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.Query(`SELECT id, group_id, title, format, LENGTH(content), created_at, updated_at
		FROM docs WHERE deleted_at = 0 ORDER BY sort > 0 DESC, sort ASC, updated_at DESC, id DESC`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var docs []Doc
	for rows.Next() {
		var d Doc
		if err := rows.Scan(&d.ID, &d.GroupID, &d.Title, &d.Format, &d.Size, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, nil, err
		}
		docs = append(docs, d)
	}
	return groups, docs, rows.Err()
}

// ReorderDocs 按传入 id 顺序重写组内拖拽序（i+1，从 1 起）。
func (s *Store) ReorderDocs(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE docs SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListDocGroups 返回全部分组，按创建顺序。
func (s *Store) ListDocGroups() ([]DocGroup, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at FROM doc_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []DocGroup
	for rows.Next() {
		var g DocGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

// CreateDocGroup 新建文档分组；重名返回 ErrDocGroupNameExists。
func (s *Store) CreateDocGroup(name string) (*DocGroup, error) {
	res, err := s.db.Exec(`INSERT INTO doc_groups (name, created_at) VALUES (?, ?)`, name, time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrDocGroupNameExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	var g DocGroup
	err = s.db.QueryRow(`SELECT id, name, created_at FROM doc_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &g.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// RenameDocGroup 改分组名；重名返回 ErrDocGroupNameExists。
func (s *Store) RenameDocGroup(id int64, name string) error {
	res, err := s.db.Exec(`UPDATE doc_groups SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrDocGroupNameExists
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteDocGroup 删除分组；组内正常文档移入回收站（软删，单事务），返回受影响文档数。
// 已在回收站的文档保持原样（其 group_id 仍指已删的组，恢复时会回落未分组）。
func (s *Store) DeleteDocGroup(id int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE docs SET deleted_at = ? WHERE group_id = ? AND deleted_at = 0`, time.Now().Unix(), id)
	if err != nil {
		return 0, err
	}
	deleted, _ := res.RowsAffected()
	res, err = tx.Exec(`DELETE FROM doc_groups WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, sql.ErrNoRows
	}
	return deleted, tx.Commit()
}

// CreateDoc 新建文档（content 可为空，之后编辑保存）。
func (s *Store) CreateDoc(groupID int64, title, format string) (*Doc, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO docs (group_id, title, format, content, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		groupID, title, format, "", now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetDoc(id)
}

// GetDoc 按 ID 查文档（含 content 与软删标记，回收站中的文档也可取），未命中返回 (nil, nil)。
func (s *Store) GetDoc(id int64) (*Doc, error) {
	var d Doc
	err := s.db.QueryRow(`SELECT id, group_id, title, format, content, created_at, updated_at, deleted_at
		FROM docs WHERE id = ?`, id).
		Scan(&d.ID, &d.GroupID, &d.Title, &d.Format, &d.Content, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// docVersionKeep 每篇文档保留的历史版本数。
const docVersionKeep = 10

// docVersionGapSec 版本快照节流间隔：距上一版本不足该秒数时不留新快照。
// 前端是 2s 防抖自动保存，不节流的话 10 个名额几秒就被同一次编辑的中间态
// 刷光；节流后每段「编辑会话」开始前的原内容各占一版，恢复粒度=会话边界。
const docVersionGapSec = 600

// UpdateDoc 保存文档：title 与 content 为 nil 表示不改，否则更新（空串=清空）。
// content 变化时先把旧内容快照进 doc_versions（按 docVersionGapSec 节流；
// 旧内容为空串不留——首次保存前没有可恢复的东西），随后修剪至最近
// docVersionKeep 版，与更新同事务。
// ErrDocConflict 乐观锁冲突：base_updated_at 与库中当前 updated_at 不一致
// （文档已在其他窗口被保存过）。
var ErrDocConflict = errors.New("doc conflict")

// UpdateDoc 保存文档：title 与 content 为 nil 表示不改，否则更新（空串=清空）。
// baseUpdatedAt 为乐观锁基准：>=0 时要求库中当前 updated_at 与之一致才保存——
// 多窗口同开一篇文档时后保存者不再静默覆盖先保存者，不一致返回 ErrDocConflict
// （由调用方转 409）；负数 = 跳过校验。返回保存后的新 updated_at。
func (s *Store) UpdateDoc(id int64, title, content *string, baseUpdatedAt int64) (int64, error) {
	if title == nil && content == nil {
		return 0, nil
	}
	now := time.Now().Unix()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if baseUpdatedAt >= 0 {
		var cur int64
		err = tx.QueryRow(`SELECT updated_at FROM docs WHERE id = ?`, id).Scan(&cur)
		if err != nil {
			return 0, err // 含 ErrNoRows：文档不存在
		}
		if cur != baseUpdatedAt {
			return 0, ErrDocConflict
		}
	}

	if content != nil {
		var oldTitle, oldContent string
		var lastVerAt int64
		err = tx.QueryRow(`SELECT title, content FROM docs WHERE id = ?`, id).Scan(&oldTitle, &oldContent)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, sql.ErrNoRows
		}
		if err != nil {
			return 0, err
		}
		if oldContent != *content && oldContent != "" {
			_ = tx.QueryRow(`SELECT MAX(created_at) FROM doc_versions WHERE doc_id = ?`, id).Scan(&lastVerAt)
			if now-lastVerAt >= docVersionGapSec {
				if _, err = tx.Exec(`INSERT INTO doc_versions (doc_id, title, content, created_at) VALUES (?,?,?,?)`,
					id, oldTitle, oldContent, now); err != nil {
					return 0, err
				}
				if _, err = tx.Exec(`DELETE FROM doc_versions WHERE doc_id = ? AND id NOT IN
					(SELECT id FROM doc_versions WHERE doc_id = ? ORDER BY id DESC LIMIT ?)`, id, id, docVersionKeep); err != nil {
					return 0, err
				}
			}
		}
	}

	var res sql.Result
	switch {
	case title != nil && content != nil:
		res, err = tx.Exec(`UPDATE docs SET title = ?, content = ?, updated_at = ? WHERE id = ?`, *title, *content, now, id)
	case title != nil:
		res, err = tx.Exec(`UPDATE docs SET title = ?, updated_at = ? WHERE id = ?`, *title, now, id)
	default:
		res, err = tx.Exec(`UPDATE docs SET content = ?, updated_at = ? WHERE id = ?`, *content, now, id)
	}
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, sql.ErrNoRows
	}
	return now, tx.Commit()
}

// MoveDoc 文档移到另一分组（跨分组拖拽）：group_id 改写、sort 清零
// （落入目标组的未排序区，按更新时间倒序与其他未排序文档排在一起）。
func (s *Store) MoveDoc(id, groupID int64) error {
	res, err := s.db.Exec(`UPDATE docs SET group_id = ?, sort = 0, updated_at = ? WHERE id = ? AND deleted_at = 0`,
		groupID, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ---------- 回收站 ----------

// SoftDeleteDoc 文档移入回收站（deleted_at 打时间戳），可恢复。
func (s *Store) SoftDeleteDoc(id int64) error {
	res, err := s.db.Exec(`UPDATE docs SET deleted_at = ? WHERE id = ? AND deleted_at = 0`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 区分「不存在」与「已在回收站」都按未命中处理，api 层统一 404
		return sql.ErrNoRows
	}
	return nil
}

// ListTrashDocs 回收站文档列表（不含 content），按删除时间倒序。
func (s *Store) ListTrashDocs() ([]Doc, error) {
	rows, err := s.db.Query(`SELECT id, group_id, title, format, created_at, updated_at, deleted_at
		FROM docs WHERE deleted_at > 0 ORDER BY deleted_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Doc
	for rows.Next() {
		var d Doc
		if err := rows.Scan(&d.ID, &d.GroupID, &d.Title, &d.Format, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// RestoreDoc 从回收站恢复文档：原分组已不存在（含组被删）时回落未分组。
func (s *Store) RestoreDoc(id int64) (*Doc, error) {
	res, err := s.db.Exec(`UPDATE docs SET deleted_at = 0,
		group_id = CASE WHEN group_id > 0 AND group_id NOT IN (SELECT id FROM doc_groups) THEN 0 ELSE group_id END
		WHERE id = ? AND deleted_at > 0`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetDoc(id)
}

// HardDeleteDoc 彻底删除（仅限回收站中的文档）：连同历史版本一并物理删除。
func (s *Store) HardDeleteDoc(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM docs WHERE id = ? AND deleted_at > 0`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if _, err = tx.Exec(`DELETE FROM doc_versions WHERE doc_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// EmptyTrashDocs 清空回收站：全部软删文档连同其历史版本物理删除（单事务），
// 返回删除的文档数；正常文档与其版本不受影响。
func (s *Store) EmptyTrashDocs() (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM docs WHERE deleted_at > 0`)
	if err != nil {
		return 0, err
	}
	deleted, _ := res.RowsAffected()
	// docs 行删完后，版本表中 doc_id 不再对应的即回收站文档的历史版本
	if _, err = tx.Exec(`DELETE FROM doc_versions WHERE doc_id NOT IN (SELECT id FROM docs)`); err != nil {
		return 0, err
	}
	return deleted, tx.Commit()
}

// ---------- 历史版本 ----------

// ListDocVersions 版本列表（元数据 + 字节数，不含 content），最近在前。
func (s *Store) ListDocVersions(docID int64) ([]DocVersion, error) {
	rows, err := s.db.Query(`SELECT id, doc_id, title, length(content), created_at
		FROM doc_versions WHERE doc_id = ? ORDER BY id DESC`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []DocVersion
	for rows.Next() {
		var v DocVersion
		if err := rows.Scan(&v.ID, &v.DocID, &v.Title, &v.Size, &v.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}

// GetDocVersion 取单个版本（含 content），未命中返回 (nil, nil)。
func (s *Store) GetDocVersion(docID, vid int64) (*DocVersion, error) {
	var v DocVersion
	err := s.db.QueryRow(`SELECT id, doc_id, title, content, created_at
		FROM doc_versions WHERE doc_id = ? AND id = ?`, docID, vid).
		Scan(&v.ID, &v.DocID, &v.Title, &v.Content, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// DocAsset 文档附件（富文本编辑器上传的图片/视频）。
type DocAsset struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Mime      string `json:"mime"`
	Size      int    `json:"size"`
	CreatedAt int64  `json:"created_at"`
}

// CreateDocAsset 保存一个附件，返回 ID。
func (s *Store) CreateDocAsset(name, mime string, data []byte) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO doc_assets (name, mime, size, data, created_at)
		VALUES (?, ?, ?, ?, ?)`, name, mime, len(data), data, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetDocAsset 取附件元数据与内容，未命中返回 (nil, nil, nil)。
func (s *Store) GetDocAsset(id int64) (*DocAsset, []byte, error) {
	var a DocAsset
	var data []byte
	err := s.db.QueryRow(`SELECT id, name, mime, size, data, created_at
		FROM doc_assets WHERE id = ?`, id).
		Scan(&a.ID, &a.Name, &a.Mime, &a.Size, &data, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &a, data, nil
}
