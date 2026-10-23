package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// video_dl_tasks 视频下载任务队列（TODO 4，追剧页「视频下载」tab）：一行一条
// 下载任务，executor 分流派——server=server 本机执行器领取（videodl.Manager），
// agent=push agent 上报响应捎带领取。与 bili_tasks 同纪律（任务一次性、Finish
// 幂等、领取原子租出、终态定期清理），差别：无 follow 归属（单发任务无活跃唯一
// 约束，同 URL 活跃去重在 Create 里查一次）；视频文件更大更慢，租约 6h。
// payload 含 Cookie（加密落库）；进度/标题/速度在下载中持续回写（progress 列）。
const videoDlTasksDDL = `CREATE TABLE IF NOT EXISTS video_dl_tasks (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  url            TEXT    NOT NULL,
  title          TEXT    NOT NULL DEFAULT '',
  save_dir       TEXT    NOT NULL DEFAULT '',
  executor       TEXT    NOT NULL DEFAULT 'server',  -- server | agent
  server_id      INTEGER NOT NULL DEFAULT 0,          -- executor=agent 时的执行机器
  cookies        TEXT    NOT NULL DEFAULT '',         -- Netscape Cookie 文件内容（加密落库）
  state          TEXT    NOT NULL DEFAULT 'pending',  -- pending/leased/done/failed/expired
  progress       INTEGER NOT NULL DEFAULT 0,          -- 0-100
  speed          TEXT    NOT NULL DEFAULT '',
  file_name      TEXT    NOT NULL DEFAULT '',         -- 最终落盘路径
  file_size      INTEGER NOT NULL DEFAULT 0,
  stop_requested INTEGER NOT NULL DEFAULT 0,          -- 用户请求停止（leased 时经上报响应捎带）
  attempt        INTEGER NOT NULL DEFAULT 0,
  leased_at      INTEGER NOT NULL DEFAULT 0,
  error          TEXT    NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);`

const videoDlTasksIdxDDL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_video_dl_tasks_active_url ON video_dl_tasks (url) WHERE state IN ('pending','leased');
CREATE INDEX IF NOT EXISTS idx_video_dl_tasks_server ON video_dl_tasks (executor, server_id, state);`

// 租约与清理口径：视频分钟级到小时级，租约 6h（超过即判执行端失联，不重派——
// 文件落盘语义重派无益）；终态历史保留 7 天（下载历史页要看）。
const (
	VideoDlLeaseTimeout = 6 * time.Hour
	VideoDlKeepDone     = 7 * 24 * time.Hour
)

// VideoDlTask 对应 video_dl_tasks 一行。Cookies 加密落库，领取时解密。
type VideoDlTask struct {
	ID            int64  `json:"id"`
	URL           string `json:"url"`
	Title         string `json:"title"`
	SaveDir       string `json:"save_dir"`
	Executor      string `json:"executor"`
	ServerID      int64  `json:"server_id"`
	Cookies       string `json:"cookies,omitempty"`
	State         string `json:"state"`
	Progress      int    `json:"progress"`
	Speed         string `json:"speed"`
	FileName      string `json:"file_name"`
	FileSize      int64  `json:"file_size"`
	StopRequested int    `json:"-"`
	Attempt       int    `json:"attempt"`
	LeasedAt      int64  `json:"leased_at"`
	Error         string `json:"error"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

// ErrVideoDlActive / ErrVideoDlNotFound 任务队列错误。
var (
	ErrVideoDlActive   = errors.New("该视频已有进行中的下载任务")
	ErrVideoDlNotFound = errors.New("下载任务不存在")
)

const videoDlCols = `id, url, title, save_dir, executor, server_id, cookies, state, progress, speed, file_name, file_size, stop_requested, attempt, leased_at, error, created_at, updated_at`

func scanVideoDlTask(row interface{ Scan(...any) error }) (*VideoDlTask, error) {
	var t VideoDlTask
	err := row.Scan(&t.ID, &t.URL, &t.Title, &t.SaveDir, &t.Executor, &t.ServerID, &t.Cookies,
		&t.State, &t.Progress, &t.Speed, &t.FileName, &t.FileSize, &t.StopRequested,
		&t.Attempt, &t.LeasedAt, &t.Error, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVideoDlNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateVideoDlTask 入队：同 URL 同时只允许一个活跃任务（部分唯一索引兜底并发）。
func (s *Store) CreateVideoDlTask(rawURL, saveDir, executor string, serverID int64, cookies string) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO video_dl_tasks (url, save_dir, executor, server_id, cookies, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)`,
		rawURL, saveDir, executor, serverID, s.seal(cookies), now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrVideoDlActive
		}
		return 0, err
	}
	return res.LastInsertId()
}

// ListVideoDlTasks 下载历史（最新在前，limit 封顶）。Cookies 不解密不出户。
func (s *Store) ListVideoDlTasks(limit int) ([]VideoDlTask, error) {
	rows, err := s.db.Query(`SELECT `+videoDlCols+` FROM video_dl_tasks ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VideoDlTask
	for rows.Next() {
		t, err := scanVideoDlTask(rows)
		if err != nil {
			return nil, err
		}
		t.Cookies = ""
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ClaimVideoDlTasks 领取：executor+机器名下 pending 任务原子租出（attempt+1），
// Cookies 解密供直接执行。serverID=0 即 server 本机执行器（executor='server'）。
func (s *Store) ClaimVideoDlTasks(executor string, serverID int64, limit int) ([]VideoDlTask, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT `+videoDlCols+` FROM video_dl_tasks
		WHERE state='pending' AND executor=? AND server_id=? ORDER BY id LIMIT ?`, executor, serverID, limit)
	if err != nil {
		return nil, err
	}
	var claimed []VideoDlTask
	for rows.Next() {
		t, err := scanVideoDlTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		claimed = append(claimed, *t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []VideoDlTask
	for i := range claimed {
		t := &claimed[i]
		res, err := s.db.Exec(`UPDATE video_dl_tasks SET state='leased', leased_at=?, attempt=attempt+1, updated_at=? WHERE id=? AND state='pending'`, now, now, t.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		t.Cookies = s.unseal(t.Cookies)
		t.State, t.LeasedAt, t.Attempt = "leased", now, t.Attempt+1
		out = append(out, *t)
	}
	return out, nil
}

// GetVideoDlTaskState 轻量行（进度轮询/归属校验用，不带 Cookies）。
func (s *Store) GetVideoDlTaskState(id int64) (*VideoDlTask, error) {
	var t VideoDlTask
	err := s.db.QueryRow(`SELECT id, url, title, executor, server_id, state, progress, speed, file_name, file_size, error FROM video_dl_tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.URL, &t.Title, &t.Executor, &t.ServerID, &t.State, &t.Progress, &t.Speed, &t.FileName, &t.FileSize, &t.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVideoDlNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateVideoDlProgress 下载中进度回写（仅 leased 接受；标题随首次回调补齐）。
func (s *Store) UpdateVideoDlProgress(id int64, percent int, speed, title string) error {
	now := time.Now().Unix()
	if title != "" {
		_, err := s.db.Exec(`UPDATE video_dl_tasks SET progress=?, speed=?, title=?, updated_at=? WHERE id=? AND state='leased'`,
			percent, speed, title, now, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE video_dl_tasks SET progress=?, speed=?, updated_at=? WHERE id=? AND state='leased'`,
		percent, speed, now, id)
	return err
}

// FinishVideoDlTask 回传结果（幂等）：仅 leased 状态接受，成败皆终态，绝不自动
// 重派（下载重派无幂等收益，失败由用户手动重试）。title 非空时补齐（快速完成
// 时进度心跳来不及带标题的兜底）。返回是否本次真正完成。
func (s *Store) FinishVideoDlTask(taskID int64, title, fileName string, fileSize int64, errMsg string) (bool, error) {
	var state string
	if err := s.db.QueryRow(`SELECT state FROM video_dl_tasks WHERE id = ?`, taskID).Scan(&state); err != nil {
		return false, err
	}
	if state != "leased" {
		return false, nil
	}
	state = "done"
	if strings.TrimSpace(errMsg) != "" {
		state = "failed"
	}
	var progress int
	if state == "done" {
		progress = 100
	}
	res, err := s.db.Exec(`UPDATE video_dl_tasks SET state=?, progress=?,
		title=CASE WHEN ? != '' THEN ? ELSE title END,
		file_name=?, file_size=?, stop_requested=0, error=?, updated_at=? WHERE id=? AND state='leased'`,
		state, progress, strings.TrimSpace(title), strings.TrimSpace(title),
		strings.TrimSpace(fileName), fileSize, strings.TrimSpace(errMsg), time.Now().Unix(), taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ExpirePendingVideoDlTask 手动停止 pending 任务（即刻作废，执行端还没拿走）。
func (s *Store) ExpirePendingVideoDlTask(id int64) {
	s.db.Exec(`UPDATE video_dl_tasks SET state='expired', error='用户手动停止', updated_at=? WHERE id=? AND state='pending'`,
		time.Now().Unix(), id)
}

// RequestVideoDlStop 请求停止 leased 任务：server 本机执行器由 Manager tick 感知
// 后杀进程；agent 执行经上报响应捎带 stop 命令。返回是否有 leased 任务被标记。
func (s *Store) RequestVideoDlStop(id int64) bool {
	res, err := s.db.Exec(`UPDATE video_dl_tasks SET stop_requested=1, updated_at=? WHERE id=? AND state='leased'`,
		time.Now().Unix(), id)
	n, _ := res.RowsAffected()
	return err == nil && n > 0
}

// VideoDlStopRequested 查单任务停止标记（执行端看护协程轮询用）。
func (s *Store) VideoDlStopRequested(id int64) (bool, error) {
	var req int
	err := s.db.QueryRow(`SELECT stop_requested FROM video_dl_tasks WHERE id = ?`, id).Scan(&req)
	if err != nil {
		return false, err
	}
	return req == 1, nil
}

// StoppedVideoDlTasks 该机器名下请求停止的 leased 任务 id（上报响应捎带调用；
// agent 杀进程回传失败后 Finish 清标记）。
func (s *Store) StoppedVideoDlTasks(serverID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM video_dl_tasks WHERE executor='agent' AND server_id=? AND state='leased' AND stop_requested=1`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// StopRequestedServerVideoDlTasks server 本机执行器名下请求停止的 leased 任务 id
//（Manager tick 感知后取消进程上下文）。
func (s *Store) StopRequestedServerVideoDlTasks() ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM video_dl_tasks WHERE executor='server' AND state='leased' AND stop_requested=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// ReclaimExpiredVideoDlTasks 租约超时回收：leased 超 6h 视为执行端失联，直接
// failed（不重派）。返回转 failed 数。
func (s *Store) ReclaimExpiredVideoDlTasks() int {
	cutoff := time.Now().Add(-VideoDlLeaseTimeout).Unix()
	res, err := s.db.Exec(`UPDATE video_dl_tasks SET state='failed', error='执行机器租约超时（agent 中断或离线）', updated_at=? WHERE state='leased' AND leased_at < ?`,
		time.Now().Unix(), cutoff)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return int(n)
}

// DeleteVideoDlTask 删除单条历史（仅终态可删，活跃任务先停止）。
func (s *Store) DeleteVideoDlTask(id int64) error {
	res, err := s.db.Exec(`DELETE FROM video_dl_tasks WHERE id=? AND state IN ('done','failed','expired')`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrVideoDlNotFound
	}
	return nil
}

// ExpireVideoDlTasksByServer 执行机器被删除时作废其 pending 任务（leased 等租约兜底）。
func (s *Store) ExpireVideoDlTasksByServer(serverID int64) {
	s.db.Exec(`UPDATE video_dl_tasks SET state='expired', error='任务已作废（执行机器已删除）', updated_at=? WHERE executor='agent' AND server_id=? AND state='pending'`,
		time.Now().Unix(), serverID)
}

// CleanupVideoDlTasks 清理终态历史任务。
func (s *Store) CleanupVideoDlTasks(recently time.Duration) {
	cutoff := time.Now().Add(-recently).Unix()
	s.db.Exec(`DELETE FROM video_dl_tasks WHERE state IN ('done','failed','expired') AND updated_at < ?`, cutoff)
}
