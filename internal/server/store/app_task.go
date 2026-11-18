package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// app_tasks 应用操作任务队列（54 飞牛 fnOS 集成）：Docker 容器/虚拟机启停
// 指令入队后由 push agent 上报捎带领取、本机执行、结果回传。与 net_tasks
// 同纪律（54 定稿）：
//   - 任务一次性：执行过一轮（无论成败）即终态，绝不自动重入队；
//   - 重试不触「机」：仅「未确认执行」的任务（lease 超时）允许一次重派
//     （attempt≤2）；已回传的结果绝不重派；
//   - 全链路幂等：入队查重（同机同目标最多 1 个活跃任务，防按钮连点堆指令）、
//     领取原子租出、回传按状态机去重；
//   - 无凭据可泄：任务体只有类型/目标/动词三要素，明文落库。
const appTasksDDL = `CREATE TABLE IF NOT EXISTS app_tasks (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id  INTEGER NOT NULL DEFAULT 0,
  kind       TEXT    NOT NULL DEFAULT '',   -- docker | vm
  target     TEXT    NOT NULL DEFAULT '',   -- 容器短 ID/名 或 libvirt 域名
  action     TEXT    NOT NULL DEFAULT '',   -- start/stop/restart | start/shutdown/reboot/destroy
  attempt    INTEGER NOT NULL DEFAULT 0,
  state      TEXT    NOT NULL DEFAULT 'pending',
  leased_at  INTEGER NOT NULL DEFAULT 0,
  ok         INTEGER NOT NULL DEFAULT 0,
  message    TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`

const appTasksIdxDDL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_app_tasks_active ON app_tasks (server_id, kind, target) WHERE state IN ('pending','leased');
CREATE INDEX IF NOT EXISTS idx_app_tasks_state_updated ON app_tasks (state, updated_at);`

// AppTaskLeaseTimeout 租约超时：单任务执行上限约 35s（docker stop 宽限 10s+
// 网络余量、virsh start 域拉起），取约 2 倍；超时回收计 attempt。
const AppTaskLeaseTimeout = 90 * time.Second

// AppTaskMaxAttempts 重派上限：只有 lease 超时（未确认执行）才可能走到 2。
const AppTaskMaxAttempts = 2

// 应用操作动词白名单（server 侧入队校验，agent 侧另有同款兜底）。
var appTaskVerbs = map[string]map[string]bool{
	"docker": {"start": true, "stop": true, "restart": true},
	"vm":     {"start": true, "shutdown": true, "reboot": true, "destroy": true},
}

// ValidAppTaskAction 入队前的类型+动词白名单校验（api 层入口调用）。
func ValidAppTaskAction(kind, action string) bool {
	return appTaskVerbs[kind] != nil && appTaskVerbs[kind][action]
}

// AppTask 对应 app_tasks 一行。
type AppTask struct {
	ID        int64  `json:"id"`
	ServerID  int64  `json:"server_id"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Action    string `json:"action"`
	Attempt   int    `json:"attempt"`
	State     string `json:"state"` // pending / leased / done / failed / expired
	LeasedAt  int64  `json:"leased_at"`
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// ErrAppTaskActive / ErrAppTaskNotFound 任务队列错误。
var (
	ErrAppTaskActive   = errors.New("该目标已有进行中的操作")
	ErrAppTaskNotFound = errors.New("操作任务不存在")
)

const appTaskCols = `id, server_id, kind, target, action, attempt, state, leased_at, ok, message, created_at, updated_at`

func scanAppTask(row interface{ Scan(...any) error }) (*AppTask, error) {
	var t AppTask
	var ok int
	err := row.Scan(&t.ID, &t.ServerID, &t.Kind, &t.Target, &t.Action, &t.Attempt, &t.State,
		&t.LeasedAt, &ok, &t.Message, &t.CreatedAt, &t.UpdatedAt)
	if err == nil {
		t.OK = ok == 1
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAppTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateAppTask 入队：类型+动词白名单校验 → 查重（同机同目标最多 1 个活跃
// 任务，并发撞部分唯一索引视为 ErrAppTaskActive）后写入。
func (s *Store) CreateAppTask(serverID int64, kind, target, action string) (int64, error) {
	if !ValidAppTaskAction(kind, action) {
		return 0, errors.New("不支持的操作")
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO app_tasks (server_id, kind, target, action, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'pending', ?, ?)`, serverID, kind, target, action, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrAppTaskActive
		}
		return 0, err
	}
	return res.LastInsertId()
}

// ClaimAppTasks agent 领取：该机名下 pending 任务原子租出（attempt+1）。
func (s *Store) ClaimAppTasks(serverID int64, limit int) ([]AppTask, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT `+appTaskCols+` FROM app_tasks WHERE state = 'pending' AND server_id = ? ORDER BY id LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, err
	}
	var claimed []AppTask
	for rows.Next() {
		t, err := scanAppTask(rows)
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
	var out []AppTask
	for i := range claimed {
		t := &claimed[i]
		res, err := s.db.Exec(`UPDATE app_tasks SET state='leased', leased_at=?, attempt=attempt+1, updated_at=? WHERE id=? AND state='pending'`, now, now, t.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // 已被其他领取者租出（防并发，理论罕见）
		}
		t.State, t.LeasedAt, t.Attempt = "leased", now, t.Attempt+1
		out = append(out, *t)
	}
	return out, nil
}

// FinishAppTask 回传结果（幂等）：仅 leased 状态接受，终态后重复回传丢弃，
// 返回是否本次真正完成（首次）。执行成败都是终态，绝不因失败重派。
func (s *Store) FinishAppTask(taskID int64, ok bool, message string) (bool, error) {
	var state string
	if err := s.db.QueryRow(`SELECT state FROM app_tasks WHERE id = ?`, taskID).Scan(&state); err != nil {
		return false, err
	}
	if state != "leased" {
		return false, nil
	}
	flag := 0
	if ok {
		flag = 1
	}
	res, err := s.db.Exec(`UPDATE app_tasks SET state='done', ok=?, message=?, updated_at=? WHERE id=? AND state='leased'`,
		flag, strings.TrimSpace(message), time.Now().Unix(), taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReclaimExpiredAppTasks 租约超时回收：未确认执行（leased 超时）的任务按
// attempt 分流——attempt<2 回 pending 重派一次，否则 failed 终态。
func (s *Store) ReclaimExpiredAppTasks() int {
	cutoff := time.Now().Add(-AppTaskLeaseTimeout).Unix()
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT id, attempt FROM app_tasks WHERE state='leased' AND leased_at < ?`, cutoff)
	if err != nil {
		return 0
	}
	var retryIDs, deadIDs []int64
	for rows.Next() {
		var id, attempt int64
		if rows.Scan(&id, &attempt) == nil {
			if attempt < AppTaskMaxAttempts {
				retryIDs = append(retryIDs, id)
			} else {
				deadIDs = append(deadIDs, id)
			}
		}
	}
	rows.Close()
	for _, id := range retryIDs {
		s.db.Exec(`UPDATE app_tasks SET state='pending', leased_at=0, updated_at=? WHERE id=? AND state='leased' AND leased_at < ?`, now, id, cutoff)
	}
	for _, id := range deadIDs {
		s.db.Exec(`UPDATE app_tasks SET state='failed', message='出口服务器租约超时且已达重派上限', updated_at=? WHERE id=? AND state='leased' AND leased_at < ?`, now, id, cutoff)
	}
	return len(deadIDs)
}

// GetAppTaskState 任务状态轮询轻量行：只取判态所需列（含 server_id 供结果
// 回传时的归属校验）。
func (s *Store) GetAppTaskState(id int64) (*AppTask, error) {
	var t AppTask
	var ok int
	err := s.db.QueryRow(`SELECT id, server_id, kind, target, state, ok, message FROM app_tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.ServerID, &t.Kind, &t.Target, &t.State, &ok, &t.Message)
	t.OK = ok == 1
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAppTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ExpireAppTasksByServer 机器被删除/换绑时作废其 pending 任务（leased 的等
// 租约超时回收——agent 可能正拿着在跑）。
func (s *Store) ExpireAppTasksByServer(serverID int64) {
	s.db.Exec(`UPDATE app_tasks SET state='expired', message='任务已作废（服务器已变更）', updated_at=? WHERE server_id=? AND state='pending'`, time.Now().Unix(), serverID)
}

// CleanupAppTasks 清理终态历史任务（保留 recently 之内，防表无限膨胀；
// 前端只在操作后短窗口内轮询，24h 足够）。
func (s *Store) CleanupAppTasks(recently time.Duration) {
	cutoff := time.Now().Add(-recently).Unix()
	s.db.Exec(`DELETE FROM app_tasks WHERE state IN ('done','failed','expired') AND updated_at < ?`, cutoff)
}
