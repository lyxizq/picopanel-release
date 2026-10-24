package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// bili_tasks B 站追更任务队列（TODO 6）：probe=解析地址 / seed10=首抓最新 10
// 条 / all=全量增量。与 net_tasks 同纪律（任务一次性、Finish 幂等、活跃唯一、
// 回传按状态机去重），差别只在租约——追更一单要下几分钟的音频，60s 的中转
// 租约会误杀重跑，单独 2 小时；重派上限 1（下载按目录文件断点续抓，失败后
// 由每日调度/手动重试自然续）。
const biliTasksDDL = `CREATE TABLE IF NOT EXISTS bili_tasks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  follow_id   INTEGER NOT NULL,
  mode        TEXT    NOT NULL DEFAULT 'probe',
  server_id   INTEGER NOT NULL DEFAULT 0,
  payload     TEXT    NOT NULL DEFAULT '',   -- 任务载荷 JSON（含 SESSDATA，加密落库）
  attempt     INTEGER NOT NULL DEFAULT 0,
  state       TEXT    NOT NULL DEFAULT 'pending',
  leased_at   INTEGER NOT NULL DEFAULT 0,
  result      TEXT    NOT NULL DEFAULT '',
  error       TEXT    NOT NULL DEFAULT '',
  consumed_at INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);`

const biliTasksIdxDDL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_bili_tasks_active ON bili_tasks (follow_id) WHERE state IN ('pending','leased');
CREATE INDEX IF NOT EXISTS idx_bili_tasks_server ON bili_tasks (server_id, state);`

// BiliTaskMode 任务三形态。
const (
	BiliTaskProbe  = "probe"
	BiliTaskSeed10 = "seed10"
	BiliTaskAll    = "all"
)

// 租约分流派（TODO 4 追更启动卡死修复）：probe 是秒级 API 调用（解析地址），
// 3 分钟租约+超时回队重派（attempt≤3）——覆盖「旧版 agent 领走后丢弃」的
// 自愈窗口，机器升级后最多 3 分钟自动重跑，无需人工干预；run（下载）分钟级
// 保持 2h 长租约、超时不重派直接 failed（幂等性已由服务端清单+文件写保护
// 保证，重派交给每日调度/手动）。
const (
	BiliTaskLeaseTimeout   = 2 * time.Hour
	BiliProbeLeaseTimeout  = 3 * time.Minute
	BiliTaskMaxAttempts    = 1
	BiliProbeMaxAttempts   = 3
)

// BiliTask 对应 bili_tasks 一行。Payload 加密落库，领取时解密。
type BiliTask struct {
	ID         int64  `json:"id"`
	FollowID   int64  `json:"follow_id"`
	Mode       string `json:"mode"`
	ServerID   int64  `json:"server_id"`
	Payload    string `json:"payload"`
	Attempt    int    `json:"attempt"`
	State      string `json:"state"`
	LeasedAt   int64  `json:"leased_at"`
	Result     string `json:"result"`
	Error      string `json:"error"`
	ConsumedAt int64  `json:"consumed_at"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// ErrBiliTaskActive / ErrBiliTaskNotFound 任务队列错误。
var (
	ErrBiliTaskActive   = errors.New("该追更已有进行中的任务")
	ErrBiliTaskNotFound = errors.New("追更任务不存在")
)

const biliTaskCols = `id, follow_id, mode, server_id, payload, attempt, state, leased_at, result, error, consumed_at, created_at, updated_at`

func scanBiliTask(row interface{ Scan(...any) error }) (*BiliTask, error) {
	var t BiliTask
	err := row.Scan(&t.ID, &t.FollowID, &t.Mode, &t.ServerID, &t.Payload, &t.Attempt, &t.State,
		&t.LeasedAt, &t.Result, &t.Error, &t.ConsumedAt, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBiliTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateBiliTask 入队：同一追更同时只允许一个活跃任务（部分唯一索引兜底并发）。
func (s *Store) CreateBiliTask(followID int64, mode string, serverID int64, payload string) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO bili_tasks (follow_id, mode, server_id, payload, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'pending', ?, ?)`,
		followID, mode, serverID, s.seal(payload), now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrBiliTaskActive
		}
		return 0, err
	}
	return res.LastInsertId()
}

// ClaimBiliTasks agent 领取：该机器名下 pending 任务原子租出（attempt+1），
// payload 已解密供直接执行。
func (s *Store) ClaimBiliTasks(serverID int64, limit int) ([]BiliTask, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT `+biliTaskCols+` FROM bili_tasks WHERE state = 'pending' AND server_id = ? ORDER BY id LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, err
	}
	var claimed []BiliTask
	for rows.Next() {
		t, err := scanBiliTask(rows)
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
	var out []BiliTask
	for i := range claimed {
		t := &claimed[i]
		res, err := s.db.Exec(`UPDATE bili_tasks SET state='leased', leased_at=?, attempt=attempt+1, updated_at=? WHERE id=? AND state='pending'`, now, now, t.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		t.Payload = s.unseal(t.Payload)
		t.State, t.LeasedAt, t.Attempt = "leased", now, t.Attempt+1
		out = append(out, *t)
	}
	return out, nil
}

// FinishBiliTask 回传结果（幂等）：仅 leased 状态接受，done 后重复回传丢弃，
// 返回是否本次真正完成。成败皆终态，绝不自动重派。
func (s *Store) FinishBiliTask(taskID int64, result, errMsg string) (bool, error) {
	var state string
	if err := s.db.QueryRow(`SELECT state FROM bili_tasks WHERE id = ?`, taskID).Scan(&state); err != nil {
		return false, err
	}
	if state != "leased" {
		return false, nil
	}
	res, err := s.db.Exec(`UPDATE bili_tasks SET state='done', result=?, error=?, updated_at=? WHERE id=? AND state='leased'`,
		result, strings.TrimSpace(errMsg), time.Now().Unix(), taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReclaimExpiredBiliTasks 租约超时回收，按形态分流派：probe 未达重派上限的
// 回 pending 重派（旧 agent 领走丢弃的自愈路径），其余转 failed。返回
// (回队数, 转failed数)。
func (s *Store) ReclaimExpiredBiliTasks() (int, int) {
	now := time.Now().Unix()
	runCutoff := time.Now().Add(-BiliTaskLeaseTimeout).Unix()
	probeCutoff := time.Now().Add(-BiliProbeLeaseTimeout).Unix()
	// run：超时即 failed（attempt 已 ≥1，不重派）
	res, err := s.db.Exec(`UPDATE bili_tasks SET state='failed', error='执行机器租约超时（agent 中断或离线）', updated_at=? WHERE mode != 'probe' AND state='leased' AND leased_at < ?`, now, runCutoff)
	if err != nil {
		return 0, 0
	}
	failed, _ := res.RowsAffected()
	// probe：超时按 attempt 分流
	rows, err := s.db.Query(`SELECT id, attempt FROM bili_tasks WHERE mode = 'probe' AND state='leased' AND leased_at < ?`, probeCutoff)
	if err != nil {
		return 0, int(failed)
	}
	var retryIDs, deadIDs []int64
	for rows.Next() {
		var id, attempt int64
		if rows.Scan(&id, &attempt) == nil {
			if attempt < BiliProbeMaxAttempts {
				retryIDs = append(retryIDs, id)
			} else {
				deadIDs = append(deadIDs, id)
			}
		}
	}
	rows.Close()
	for _, id := range retryIDs {
		s.db.Exec(`UPDATE bili_tasks SET state='pending', leased_at=0, updated_at=? WHERE id=? AND state='leased' AND leased_at < ?`, now, id, probeCutoff)
	}
	for _, id := range deadIDs {
		s.db.Exec(`UPDATE bili_tasks SET state='failed', error='执行机器多次未完成解析（租约超时达重派上限）', updated_at=? WHERE id=? AND state='leased' AND leased_at < ?`, now, id, probeCutoff)
	}
	return len(retryIDs), len(deadIDs) + int(failed)
}

// GetBiliTaskState 轮询轻量行（不取 payload/result 大列）。
func (s *Store) GetBiliTaskState(id int64) (*BiliTask, error) {
	var t BiliTask
	err := s.db.QueryRow(`SELECT id, follow_id, mode, state, error FROM bili_tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.FollowID, &t.Mode, &t.State, &t.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBiliTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetBiliTask 单任务全行（payload 解密）。
func (s *Store) GetBiliTask(id int64) (*BiliTask, error) {
	t, err := scanBiliTask(s.db.QueryRow(`SELECT `+biliTaskCols+` FROM bili_tasks WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	t.Payload = s.unseal(t.Payload)
	return t, nil
}

// ActiveBiliTaskByFollow 该追更当前活跃任务（无则 nil）。
func (s *Store) ActiveBiliTaskByFollow(followID int64) (*BiliTask, error) {
	t, err := scanBiliTask(s.db.QueryRow(`SELECT `+biliTaskCols+` FROM bili_tasks WHERE follow_id = ? AND state IN ('pending','leased') ORDER BY id DESC LIMIT 1`, followID))
	if errors.Is(err, ErrBiliTaskNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Payload = s.unseal(t.Payload)
	return t, nil
}

// DoneBiliTaskByFollow 该追更最近一个终态且未消费的任务（probe 孤儿补消费用）。
func (s *Store) DoneBiliTaskByFollow(followID int64) (*BiliTask, error) {
	t, err := scanBiliTask(s.db.QueryRow(`SELECT `+biliTaskCols+` FROM bili_tasks WHERE follow_id = ? AND state = 'done' AND consumed_at = 0 ORDER BY id DESC LIMIT 1`, followID))
	if errors.Is(err, ErrBiliTaskNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Payload = s.unseal(t.Payload)
	return t, nil
}

// ConsumeBiliTask 打标消费（幂等）：consumed_at 0→now 条件更新，返回是否首次。
func (s *Store) ConsumeBiliTask(taskID int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE bili_tasks SET consumed_at=?, updated_at=? WHERE id=? AND state='done' AND consumed_at=0`,
		time.Now().Unix(), time.Now().Unix(), taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ExpirePendingBiliTask 手动停止时作废该追更的 pending 任务（即刻生效；
// leased 的不动作废——agent 正在跑，走停止标记中断路径）。
func (s *Store) ExpirePendingBiliTask(followID int64) {
	s.db.Exec(`UPDATE bili_tasks SET state='expired', error='用户手动停止', updated_at=? WHERE follow_id=? AND state='pending'`,
		time.Now().Unix(), followID)
}

// ExpireBiliTasksByFollow 删除追更时作废其任务。
func (s *Store) ExpireBiliTasksByFollow(followID int64) {
	s.db.Exec(`UPDATE bili_tasks SET state='expired', error='任务已作废（追更已删除）', updated_at=? WHERE follow_id=? AND state IN ('pending','leased')`,
		time.Now().Unix(), followID)
}

// ExpireBiliTasksByServer 执行机器被删除时作废其 pending 任务（leased 等租约兜底）。
func (s *Store) ExpireBiliTasksByServer(serverID int64) {
	s.db.Exec(`UPDATE bili_tasks SET state='expired', error='任务已作废（执行机器已变更）', updated_at=? WHERE server_id=? AND state='pending'`,
		time.Now().Unix(), serverID)
}

// CleanupBiliTasks 清理终态历史任务。
func (s *Store) CleanupBiliTasks(recently time.Duration) {
	cutoff := time.Now().Add(-recently).Unix()
	s.db.Exec(`DELETE FROM bili_tasks WHERE state IN ('done','failed','expired') AND updated_at < ?`, cutoff)
}
