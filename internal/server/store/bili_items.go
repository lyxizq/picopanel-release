package store

import (
	"strings"
	"time"
)

// bili_items B 站追更已抓记录表（TODO 3）：每条追更已下载的条目（follow_id+
// bvid 唯一）——增量判重的权威依据在服务端，agent 不再扫目录文件（任务
// payload 携带本表 bvid 清单，agent 只对清单外条目下载）。任务中断未回传时
// 记录缺失，重跑会再次下载同名文件被写保护跳过并按 exists 回传补记，自愈。
const biliItemsDDL = `CREATE TABLE IF NOT EXISTS bili_items (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  follow_id  INTEGER NOT NULL,
  bvid       TEXT    NOT NULL,
  title      TEXT    NOT NULL DEFAULT '',
  pubdate    INTEGER NOT NULL DEFAULT 0,
  file_name  TEXT    NOT NULL DEFAULT '',
  status     TEXT    NOT NULL DEFAULT 'downloaded', -- downloaded/exists/failed（7）
  created_at INTEGER NOT NULL,
  UNIQUE(follow_id, bvid)
);`

const biliItemsIdxDDL = `CREATE INDEX IF NOT EXISTS idx_bili_items_follow ON bili_items (follow_id, id);`

// BiliItemRow 一条已抓记录。
type BiliItemRow struct {
	ID        int64  `json:"id"`
	FollowID  int64  `json:"follow_id"`
	BVID      string `json:"bvid"`
	Title     string `json:"title"`
	PubDate   int64  `json:"pubdate"`
	FileName  string `json:"file_name"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
}

// BiliItemIn 回传记账的条目载荷（proto.BiliItem 的存储侧投影）。
type BiliItemIn struct {
	BVID     string
	Title    string
	PubDate  int64
	FileName string
	Status   string // downloaded/exists/failed；空=downloaded
}

// AddBiliItems 记账（幂等）：UNIQUE 冲突忽略（重复回传/exists 补记不重复），
// 返回本次实际新记条数。删除追更时行随 bili_follows 级联手删（DeleteBiliFollow
// 内一并清理）。
func (s *Store) AddBiliItems(followID int64, items []BiliItemIn) int {
	now := time.Now().Unix()
	n := 0
	for _, it := range items {
		if strings.TrimSpace(it.BVID) == "" {
			continue
		}
		st := it.Status
		if st == "" {
			st = "downloaded"
		}
		res, err := s.db.Exec(`INSERT OR IGNORE INTO bili_items (follow_id, bvid, title, pubdate, file_name, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, followID, it.BVID, it.Title, it.PubDate, it.FileName, st, now)
		if err != nil {
			continue
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			n++
		}
	}
	return n
}

// CountBiliItems 已抓记录数（item_count 的权威来源）。
func (s *Store) CountBiliItems(followID int64) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM bili_items WHERE follow_id = ?`, followID).Scan(&n)
	return n
}

// FetchedBVIDs 已抓 bvid 清单（任务 payload 组装用）。
func (s *Store) FetchedBVIDs(followID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT bvid FROM bili_items WHERE follow_id = ? ORDER BY id`, followID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var bv string
		if err := rows.Scan(&bv); err != nil {
			return nil, err
		}
		out = append(out, bv)
	}
	return out, rows.Err()
}

// ListBiliItems 已抓记录最近清单（16：详情弹层只显最后 10 条）。按 pubdate
// 新→旧——首抓按清单新→旧序插入、id 序与时间序相反，按 id 排会错拿最旧段；
// limit<=0 = 全量。
func (s *Store) ListBiliItems(followID int64, limit int) ([]BiliItemRow, error) {
	q := `SELECT id, follow_id, bvid, title, pubdate, file_name, status, created_at FROM bili_items WHERE follow_id = ? ORDER BY pubdate DESC, id DESC`
	args := []any{followID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BiliItemRow
	for rows.Next() {
		var r BiliItemRow
		if err := rows.Scan(&r.ID, &r.FollowID, &r.BVID, &r.Title, &r.PubDate, &r.FileName, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteBiliItemsByFollow 删除追更时清记录（供 DeleteBiliFollow 调用）。
func (s *Store) DeleteBiliItemsByFollow(followID int64) {
	s.db.Exec(`DELETE FROM bili_items WHERE follow_id = ?`, followID)
}
