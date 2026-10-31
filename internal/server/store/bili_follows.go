package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// bili_follows B 站追更配置表（TODO 6，随追剧页「追更」tab）：up 主 + 合集/
// 视频列表绑定 + agent 机器与保存目录 + 最近一次追更统计。mid/list_id 由
// probe 任务（agent 侧解析地址）回填，未解析完成前不参与自动追更。
const biliFollowsDDL = `CREATE TABLE IF NOT EXISTS bili_follows (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  enabled         INTEGER NOT NULL DEFAULT 1,
  site            TEXT    NOT NULL DEFAULT 'bilibili',
  up_name         TEXT    NOT NULL DEFAULT '',
  address         TEXT    NOT NULL DEFAULT '',
  mid             INTEGER NOT NULL DEFAULT 0,
  kind            TEXT    NOT NULL DEFAULT '',   -- season=合集 / series=视频列表
  list_id         INTEGER NOT NULL DEFAULT 0,
  list_title      TEXT    NOT NULL DEFAULT '',
  list_total      INTEGER NOT NULL DEFAULT 0,
  agent_server_id INTEGER NOT NULL DEFAULT 0,
  save_dir        TEXT    NOT NULL DEFAULT '',   -- agent 机器上的根目录
  sub_dir         TEXT    NOT NULL DEFAULT '',   -- up主名+合集名子目录（server 算好）
  auto_enabled    INTEGER NOT NULL DEFAULT 1,    -- 每天凌晨 4 点自动追更
  seeded          INTEGER NOT NULL DEFAULT 0,    -- 首次抓最新 10 条已完成后转全量
  last_run_at     INTEGER NOT NULL DEFAULT 0,
  last_ok_at      INTEGER NOT NULL DEFAULT 0,
  item_count      INTEGER NOT NULL DEFAULT 0,    -- 已抓条目数（agent 回报的目录文件数）
  last_new_count  INTEGER NOT NULL DEFAULT 0,    -- 最后一次新增条数
  last_items      TEXT    NOT NULL DEFAULT '',   -- 最后一次新增条目 JSON（[{bvid,title}]）
  last_error      TEXT    NOT NULL DEFAULT '',
  last_auto_date  TEXT    NOT NULL DEFAULT '',   -- 最近自动追更日期（YYYY-MM-DD，防当日重复）
  watermark       INTEGER NOT NULL DEFAULT 0,    -- 已见最新 pubdate（7：只追比它新的，不回填旧视频）
  stop_flag       INTEGER NOT NULL DEFAULT 0,    -- 手动停止标记（8：agent 捎带领取，回传后清除）
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL
);`

const biliFollowsIdxDDL = `CREATE INDEX IF NOT EXISTS idx_bili_follows_server ON bili_follows (agent_server_id);`

// BiliFollow 一条 B 站追更配置。
type BiliFollow struct {
	ID            int64  `json:"id"`
	Enabled       bool   `json:"enabled"`
	Site          string `json:"site"`
	UpName        string `json:"up_name"`
	Address       string `json:"address"`
	Mid           int64  `json:"mid"`
	Kind          string `json:"kind"`
	ListID        int64  `json:"list_id"`
	ListTitle     string `json:"list_title"`
	ListTotal     int    `json:"list_total"`
	AgentServerID int64  `json:"agent_server_id"`
	SaveDir       string `json:"save_dir"`
	SubDir        string `json:"sub_dir"`
	AutoEnabled   bool   `json:"auto_enabled"`
	Seeded        bool   `json:"seeded"`
	LastRunAt     int64  `json:"last_run_at"`
	LastOKAt      int64  `json:"last_ok_at"`
	ItemCount     int    `json:"item_count"`
	LastNewCount  int    `json:"last_new_count"`
	LastItems     string `json:"last_items"`
	LastError     string `json:"last_error"`
	LastAutoDate  string `json:"last_auto_date"`
	Watermark     int64  `json:"watermark"`
	StopRequested bool   `json:"stop_requested"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

// ErrBiliFollowNotFound 追更配置不存在。
var ErrBiliFollowNotFound = errors.New("追更配置不存在")

const biliFollowCols = `id, enabled, site, up_name, address, mid, kind, list_id, list_title, list_total,
  agent_server_id, save_dir, sub_dir, auto_enabled, seeded, last_run_at, last_ok_at,
  item_count, last_new_count, last_items, last_error, last_auto_date, watermark, stop_flag, created_at, updated_at`

func scanBiliFollow(row interface{ Scan(...any) error }) (*BiliFollow, error) {
	var f BiliFollow
	var enabled, autoEnabled, seeded int
	var stopFlag int
	err := row.Scan(&f.ID, &enabled, &f.Site, &f.UpName, &f.Address, &f.Mid, &f.Kind, &f.ListID,
		&f.ListTitle, &f.ListTotal, &f.AgentServerID, &f.SaveDir, &f.SubDir, &autoEnabled, &seeded,
		&f.LastRunAt, &f.LastOKAt, &f.ItemCount, &f.LastNewCount, &f.LastItems, &f.LastError,
		&f.LastAutoDate, &f.Watermark, &stopFlag, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBiliFollowNotFound
	}
	if err != nil {
		return nil, err
	}
	f.Enabled, f.AutoEnabled, f.Seeded = enabled == 1, autoEnabled == 1, seeded == 1
	f.StopRequested = stopFlag == 1
	return &f, nil
}

// ListBiliFollows 全部追更配置（新添加在前）。
func (s *Store) ListBiliFollows() ([]BiliFollow, error) {
	rows, err := s.db.Query(`SELECT ` + biliFollowCols + ` FROM bili_follows ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BiliFollow
	for rows.Next() {
		f, err := scanBiliFollow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// GetBiliFollow 单条。
func (s *Store) GetBiliFollow(id int64) (*BiliFollow, error) {
	return scanBiliFollow(s.db.QueryRow(`SELECT `+biliFollowCols+` FROM bili_follows WHERE id = ?`, id))
}

// CreateBiliFollow 新增（probe 前先落行，解析字段为零值）。up 主名字在地址
// 是链接时可不填（链接自带 mid；名字仅用于展示与子目录命名）。
func (s *Store) CreateBiliFollow(f *BiliFollow) error {
	f.Address = strings.TrimSpace(f.Address)
	if strings.TrimSpace(f.UpName) == "" && !isBiliURL(f.Address) {
		return errors.New("缺少 up 主名字（地址不是链接时必填）")
	}
	if f.Address == "" {
		return errors.New("缺少追更地址")
	}
	if f.AgentServerID <= 0 {
		return errors.New("缺少执行机器")
	}
	if strings.TrimSpace(f.SaveDir) == "" {
		return errors.New("缺少保存目录")
	}
	now := time.Now().Unix()
	f.CreatedAt, f.UpdatedAt = now, now
	res, err := s.db.Exec(`INSERT INTO bili_follows (enabled, site, up_name, address, agent_server_id, save_dir, auto_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		boolInt(f.Enabled), "bilibili", strings.TrimSpace(f.UpName), f.Address,
		f.AgentServerID, strings.TrimSpace(f.SaveDir), boolInt(f.AutoEnabled), now, now)
	if err != nil {
		return err
	}
	f.ID, _ = res.LastInsertId()
	return nil
}

// isBiliURL 粗判地址是否 B 站链接（与 internal/bilibili 的判定同口径，避免
// store 反向依赖业务包）。
func isBiliURL(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "://") || strings.HasPrefix(s, "space.bilibili.com") ||
		strings.HasPrefix(s, "www.bilibili.com") || strings.HasPrefix(s, "bilibili.com") ||
		strings.HasPrefix(s, "m.bilibili.com")
}

// DeleteBiliFollow 删除（已抓记录一并清理；agent 机器上已下载的文件不删——文件归用户）。
// 单事务：items 与 follow 同生共死，避免删一半失败留下 item_count 与记录表不一致。
func (s *Store) DeleteBiliFollow(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM bili_items WHERE follow_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM bili_follows WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBiliFollowNotFound
	}
	return tx.Commit()
}

// BiliFollowUpdate 编辑载荷（指针 nil = 不改）。
type BiliFollowUpdate struct {
	UpName        *string
	Address       *string
	AgentServerID *int64
	SaveDir       *string
	AutoEnabled   *bool
	Enabled       *bool
}

// UpdateBiliFollow 编辑基础字段（地址/up 名变更时由调用方清解析字段重走 probe）。
func (s *Store) UpdateBiliFollow(id int64, u *BiliFollowUpdate) error {
	sets, args := []string{"updated_at=?"}, []any{time.Now().Unix()}
	if u.UpName != nil {
		sets, args = append(sets, "up_name=?"), append(args, strings.TrimSpace(*u.UpName))
	}
	if u.Address != nil {
		sets, args = append(sets, "address=?"), append(args, strings.TrimSpace(*u.Address))
	}
	if u.AgentServerID != nil {
		sets, args = append(sets, "agent_server_id=?"), append(args, *u.AgentServerID)
	}
	if u.SaveDir != nil {
		sets, args = append(sets, "save_dir=?"), append(args, strings.TrimSpace(*u.SaveDir))
	}
	if u.AutoEnabled != nil {
		sets, args = append(sets, "auto_enabled=?"), append(args, boolInt(*u.AutoEnabled))
	}
	if u.Enabled != nil {
		sets, args = append(sets, "enabled=?"), append(args, boolInt(*u.Enabled))
	}
	args = append(args, id)
	res, err := s.db.Exec(`UPDATE bili_follows SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBiliFollowNotFound
	}
	return nil
}

// BiliResolved probe 解析结果回填（清 last_error 与旧种子态无关字段）。
func (s *Store) UpdateBiliFollowResolved(id, mid int64, kind string, listID int64, title string, total int, subDir string) error {
	_, err := s.db.Exec(`UPDATE bili_follows SET mid=?, kind=?, list_id=?, list_title=?, list_total=?, sub_dir=?, last_error='', updated_at=? WHERE id = ?`,
		mid, kind, listID, title, total, subDir, time.Now().Unix(), id)
	return err
}

// SetBiliFollowResolveError 解析失败落错（保留行，用户改地址重试）。
func (s *Store) SetBiliFollowResolveError(id int64, msg string) {
	s.db.Exec(`UPDATE bili_follows SET last_error=?, updated_at=? WHERE id = ?`, msg, time.Now().Unix(), id)
}

// ResetBiliResolve 清解析字段（改地址重走 probe 前），行进入待解析态。
func (s *Store) ResetBiliResolve(id int64) error {
	_, err := s.db.Exec(`UPDATE bili_follows SET mid=0, kind='', list_id=0, list_title='', list_total=0, sub_dir='',
		last_error='等待解析追更地址…', seeded=0, watermark=0, updated_at=? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// UnresolvedBiliFollows 还没解析出目标（mid=0）的行（调度器补消费孤儿 probe 用）。
func (s *Store) UnresolvedBiliFollows() ([]BiliFollow, error) {
	rows, err := s.db.Query(`SELECT ` + biliFollowCols + ` FROM bili_follows WHERE mid = 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BiliFollow
	for rows.Next() {
		f, err := scanBiliFollow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// DueBiliAutoRuns 到期该自动追更的行：启用 ∧ 自动开 ∧ 已解析 ∧ 最近自动日期早于今天。
func (s *Store) DueBiliAutoRuns(today string) ([]BiliFollow, error) {
	rows, err := s.db.Query(`SELECT `+biliFollowCols+` FROM bili_follows
		WHERE enabled = 1 AND auto_enabled = 1 AND mid > 0 AND last_auto_date < ? ORDER BY id`, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BiliFollow
	for rows.Next() {
		f, err := scanBiliFollow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// SetBiliAutoDate 打自动追更日期标（入队即打，防当日重复）。
func (s *Store) SetBiliAutoDate(id int64, date string) {
	s.db.Exec(`UPDATE bili_follows SET last_auto_date=?, updated_at=? WHERE id = ?`, date, time.Now().Unix(), id)
}

// BiliRunStats 一次追更回传统计（bilifollow 模块组装）。
type BiliRunStats struct {
	OK         bool
	ItemCount  int    // 目录内 m4a 文件数（已抓条目数）
	NewCount   int    // 本次新增条数
	ItemsJSON  string // 本次新增条目 JSON
	ErrorMsg   string
	RunAt      int64
	FinishSeed bool // seed10 首抓完成，转全量模式
}

// UpdateBiliRunStats 回写追更统计。OK 时清 last_error；失败时保留旧统计只记错。
func (s *Store) UpdateBiliRunStats(id int64, st *BiliRunStats) error {
	if st.OK {
		_, err := s.db.Exec(`UPDATE bili_follows SET last_run_at=?, last_ok_at=?, item_count=?, last_new_count=?, last_items=?, last_error='', seeded=MAX(seeded,?), updated_at=? WHERE id = ?`,
			st.RunAt, st.RunAt, st.ItemCount, st.NewCount, st.ItemsJSON, boolInt(st.FinishSeed), time.Now().Unix(), id)
		return err
	}
	_, err := s.db.Exec(`UPDATE bili_follows SET last_run_at=?, last_error=?, updated_at=? WHERE id = ?`,
		st.RunAt, st.ErrorMsg, time.Now().Unix(), id)
	return err
}

// UpdateBiliWatermark 推进追更水位（已见最新 pubdate；只进不退）。
func (s *Store) UpdateBiliWatermark(id, pubdate int64) {
	s.db.Exec(`UPDATE bili_follows SET watermark=MAX(watermark,?), updated_at=? WHERE id = ?`, pubdate, time.Now().Unix(), id)
}

// SetBiliStopFlag 手动停止标记（8）：置位后随 agent 上报响应捎带；任务回传后清。
func (s *Store) SetBiliStopFlag(id int64, on bool) {
	s.db.Exec(`UPDATE bili_follows SET stop_flag=?, updated_at=? WHERE id = ?`, boolInt(on), time.Now().Unix(), id)
}

// StopRequestedBiliFollows 停止标记在位的追更 id 清单（下发捎带用）。
func (s *Store) StopRequestedBiliFollows() ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM bili_follows WHERE stop_flag = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetBiliFollowEnabled 启停。
func (s *Store) SetBiliFollowEnabled(id int64, on bool) error {
	res, err := s.db.Exec(`UPDATE bili_follows SET enabled=?, updated_at=? WHERE id = ?`, boolInt(on), time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBiliFollowNotFound
	}
	return nil
}
