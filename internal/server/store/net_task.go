package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// net_tasks 通用中转任务队列（548）：server 出口到不了的外网请求（TMDB 等）
// 入队后由 push agent（家里 NAS）上报时捎带领取、本机网络执行、结果回传——
// 391 PT 中转抓取的泛化版，防死循环纪律与其完全一致：
//   - 任务一次性：执行过一轮（无论成败）即终态，绝不自动重入队；
//   - 重试不触网：仅「未确认执行」的任务（lease 超时）允许一次重派
//     （attempt≤2）；已回传的结果绝不重派；
//   - 全链路幂等：入队查重（purpose+ref_key 最多 1 个活跃任务）+ 领取原子
//     租出 + 回传按状态机去重（done 后重复回传丢弃）；
//   - URL 与 headers 加密落库（常携带 API key / Cookie），agent 侧目标 host 白名单兜底。
//
// 结果消费只动库不触网（consumed_at 打标防重复推送），由 purpose 对应的
// 功能模块自行解析——本表对业务无感知。
const netTasksDDL = `CREATE TABLE IF NOT EXISTS net_tasks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  purpose     TEXT    NOT NULL DEFAULT '',
  ref_key     TEXT    NOT NULL DEFAULT '',
  server_id   INTEGER NOT NULL DEFAULT 0,
  url         TEXT    NOT NULL DEFAULT '',
  method      TEXT    NOT NULL DEFAULT 'GET',
  headers     TEXT    NOT NULL DEFAULT '',
  body        TEXT    NOT NULL DEFAULT '',
  timeout_sec INTEGER NOT NULL DEFAULT 15,
  allow_any_host INTEGER NOT NULL DEFAULT 0,
  attempt     INTEGER NOT NULL DEFAULT 0,
  state       TEXT    NOT NULL DEFAULT 'pending',
  leased_at   INTEGER NOT NULL DEFAULT 0,
  status_code INTEGER NOT NULL DEFAULT 0,
  result      TEXT    NOT NULL DEFAULT '',
  error       TEXT    NOT NULL DEFAULT '',
  consumed_at INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);`

const netTasksIdxDDL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_net_tasks_active ON net_tasks (purpose, ref_key) WHERE state IN ('pending','leased');
CREATE INDEX IF NOT EXISTS idx_net_tasks_server ON net_tasks (server_id, state);
CREATE INDEX IF NOT EXISTS idx_net_tasks_state_updated ON net_tasks (state, updated_at);`

// NetTaskLeaseTimeout 租约超时：单任务执行上限约 30s（timeout_sec 缺省 15），
// 取 2 倍余量；超时回收计 attempt。
const NetTaskLeaseTimeout = 60 * time.Second

// NetTaskMaxAttempts 重派上限：只有 lease 超时（未确认执行）才可能走到 2。
const NetTaskMaxAttempts = 2

// NetTask 对应 net_tasks 一行。URL 与 headers 加密存库，领取时解密下发；
// Result 为原始响应体（UTF-8 文本，二进制不适用本通道）。
type NetTask struct {
	ID           int64  `json:"id"`
	Purpose      string `json:"purpose"`
	RefKey       string `json:"ref_key"`
	ServerID     int64  `json:"server_id"`
	URL          string `json:"url"`
	Method       string `json:"method"`
	Headers      string `json:"headers"`
	Body         string `json:"body"`
	TimeoutSec   int    `json:"timeout_sec"`
	AllowAnyHost bool   `json:"allow_any_host"`
	Attempt      int    `json:"attempt"`
	State        string `json:"state"`
	LeasedAt     int64  `json:"leased_at"`
	StatusCode   int    `json:"status_code"`
	Result       string `json:"result"`
	Error        string `json:"error"`
	ConsumedAt   int64  `json:"consumed_at"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// ErrNetTaskActive / ErrNetTaskNotFound 任务队列错误。
var (
	ErrNetTaskActive   = errors.New("该用途已有进行中的中转任务")
	ErrNetTaskNotFound = errors.New("中转任务不存在")
)

const netTaskCols = `id, purpose, ref_key, server_id, url, method, headers, body, timeout_sec, allow_any_host,
  attempt, state, leased_at, status_code, result, error, consumed_at, created_at, updated_at`

func scanNetTask(row interface{ Scan(...any) error }) (*NetTask, error) {
	var t NetTask
	var allowAnyHost int
	err := row.Scan(&t.ID, &t.Purpose, &t.RefKey, &t.ServerID, &t.URL, &t.Method, &t.Headers, &t.Body,
		&t.TimeoutSec, &allowAnyHost, &t.Attempt, &t.State, &t.LeasedAt, &t.StatusCode, &t.Result, &t.Error,
		&t.ConsumedAt, &t.CreatedAt, &t.UpdatedAt)
	return scanNetTaskFinish(&t, allowAnyHost, err)
}

// scanNetTaskFinish 收尾公共段（ErrNoRows 归一/AllowAnyHost 还原）。
func scanNetTaskFinish(t *NetTask, allowAnyHost int, err error) (*NetTask, error) {
	if err == nil {
		t.AllowAnyHost = allowAnyHost == 1
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNetTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// GetNetTaskState 任务状态轮询轻量行：只取判态所需列，不取 result/headers/body
// 大列（result 最大 4MB，前端 2s 一跳的轮询期纯浪费；终态需要结果时再按 id 取全行）。
func (s *Store) GetNetTaskState(id int64) (*NetTask, error) {
	var t NetTask
	return scanNetTaskFinish(&t, 0,
		s.db.QueryRow(`SELECT id, purpose, ref_key, state, status_code, error FROM net_tasks WHERE id = ?`, id).
			Scan(&t.ID, &t.Purpose, &t.RefKey, &t.State, &t.StatusCode, &t.Error))
}

// CreateNetTask 入队：查重（purpose+ref_key 活跃任务唯一，并发撞部分唯一索引
// 视为 ErrNetTaskActive）后写入。URL 加密落库。allowAnyHost=true 时 agent 跳过
// host 白名单（仅 PT 类任务由 server 设置，强制 agent 侧 per-host 限速兜底）。
func (s *Store) CreateNetTask(purpose, refKey string, serverID int64, url, method string, headers map[string]string, body string, timeoutSec int, allowAnyHost bool) (int64, error) {
	if timeoutSec <= 0 {
		timeoutSec = 15
	}
	if method == "" {
		method = "GET"
	}
	hdr := ""
	if len(headers) > 0 {
		// headers 整体加密落库：PT 类任务带站点 Cookie、mihomo 类带 Bearer
		// secret，与 URL/payload 同口径（终态任务保留 7 天，明文落库=库泄露即凭据泄露）
		hdr = s.seal(marshalSimpleHeaders(headers))
	}
	anyHost := 0
	if allowAnyHost {
		anyHost = 1
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO net_tasks (purpose, ref_key, server_id, url, method, headers, body, timeout_sec, allow_any_host, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		purpose, refKey, serverID, s.seal(url), method, hdr, body, timeoutSec, anyHost, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrNetTaskActive
		}
		return 0, err
	}
	return res.LastInsertId()
}

// marshalSimpleHeaders 请求头序列化（换行分隔 k:v，值不含换行的简单头）。
func marshalSimpleHeaders(headers map[string]string) string {
	var b strings.Builder
	for k, v := range headers {
		if k == "" || strings.ContainsAny(k, ":\r\n") || strings.ContainsAny(v, "\r\n") {
			continue
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteByte('\n')
	}
	return b.String()
}

// ClaimNetTasks agent 领取：该出口服务器名下 pending 任务原子租出（attempt+1），
// URL/headers 已解密供直接执行。
func (s *Store) ClaimNetTasks(serverID int64, limit int) ([]NetTask, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT `+netTaskCols+` FROM net_tasks WHERE state = 'pending' AND server_id = ? ORDER BY id LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, err
	}
	var claimed []NetTask
	for rows.Next() {
		t, err := scanNetTask(rows)
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
	var out []NetTask
	for i := range claimed {
		t := &claimed[i]
		res, err := s.db.Exec(`UPDATE net_tasks SET state='leased', leased_at=?, attempt=attempt+1, updated_at=? WHERE id=? AND state='pending'`, now, now, t.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // 已被其他领取者租出（防并发，理论罕见）
		}
		t.URL = s.unseal(t.URL)
		t.Headers = s.unseal(t.Headers) // 存量明文行不带前缀原样返回，无缝过渡
		t.State, t.LeasedAt, t.Attempt = "leased", now, t.Attempt+1
		out = append(out, *t)
	}
	return out, nil
}

// FinishNetTask 回传结果（幂等）：仅 leased 状态接受，done 后重复回传丢弃，
// 返回是否本次真正完成（首次）。执行成败都是终态，绝不因失败重派。
func (s *Store) FinishNetTask(taskID int64, statusCode int, result, errMsg string) (bool, error) {
	var state string
	if err := s.db.QueryRow(`SELECT state FROM net_tasks WHERE id = ?`, taskID).Scan(&state); err != nil {
		return false, err
	}
	if state != "leased" {
		return false, nil
	}
	res, err := s.db.Exec(`UPDATE net_tasks SET state='done', status_code=?, result=?, error=?, updated_at=? WHERE id=? AND state='leased'`,
		statusCode, result, strings.TrimSpace(errMsg), time.Now().Unix(), taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReclaimExpiredNetTasks 租约超时回收：未确认执行（leased 超时）的任务按
// attempt 分流——attempt<2 回 pending 重派一次，否则 failed 终态。返回被回收
// 转入 failed 的任务数（供日志）。
func (s *Store) ReclaimExpiredNetTasks() int {
	cutoff := time.Now().Add(-NetTaskLeaseTimeout).Unix()
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT id, attempt FROM net_tasks WHERE state='leased' AND leased_at < ?`, cutoff)
	if err != nil {
		return 0
	}
	var retryIDs, deadIDs []int64
	for rows.Next() {
		var id, attempt int64
		if rows.Scan(&id, &attempt) == nil {
			if attempt < NetTaskMaxAttempts {
				retryIDs = append(retryIDs, id)
			} else {
				deadIDs = append(deadIDs, id)
			}
		}
	}
	rows.Close()
	for _, id := range retryIDs {
		s.db.Exec(`UPDATE net_tasks SET state='pending', leased_at=0, updated_at=? WHERE id=? AND state='leased' AND leased_at < ?`, now, id, cutoff)
	}
	for _, id := range deadIDs {
		s.db.Exec(`UPDATE net_tasks SET state='failed', error='出口服务器租约超时且已达重派上限', updated_at=? WHERE id=? AND state='leased' AND leased_at < ?`, now, id, cutoff)
	}
	return len(deadIDs)
}

// GetNetTask 单任务。
func (s *Store) GetNetTask(id int64) (*NetTask, error) {
	return scanNetTask(s.db.QueryRow(`SELECT `+netTaskCols+` FROM net_tasks WHERE id = ?`, id))
}

// ActiveNetTask purpose+ref_key 当前活跃任务（无则 nil）。
func (s *Store) ActiveNetTask(purpose, refKey string) (*NetTask, error) {
	t, err := scanNetTask(s.db.QueryRow(`SELECT `+netTaskCols+` FROM net_tasks WHERE purpose = ? AND ref_key = ? AND state IN ('pending','leased') ORDER BY id DESC LIMIT 1`, purpose, refKey))
	if errors.Is(err, ErrNetTaskNotFound) {
		return nil, nil
	}
	return t, err
}

// UpdateNetTaskResult 覆盖任务结果体（573：搜索任务 done 后由轮询端点后台
// 增强——主演/Emby 附加完成时回写增强 JSON，轮询端据标记判断是否再取）。
func (s *Store) UpdateNetTaskResult(taskID int64, result string) {
	s.db.Exec(`UPDATE net_tasks SET result=?, updated_at=? WHERE id = ?`, result, time.Now().Unix(), taskID)
}

// ConsumeNetTask 打标消费（幂等）：consumed_at 0→now 条件更新，返回是否本次
// 首次消费——只有首次才允许解析/推送。
func (s *Store) ConsumeNetTask(taskID int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE net_tasks SET consumed_at=?, updated_at=? WHERE id=? AND state='done' AND consumed_at=0`,
		time.Now().Unix(), time.Now().Unix(), taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ExpireNetTasksByServer 出口服务器被删除/换绑时作废其 pending 任务（leased
// 的等租约超时回收——agent 可能正拿着在跑）。
func (s *Store) ExpireNetTasksByServer(serverID int64) {
	s.db.Exec(`UPDATE net_tasks SET state='expired', error='任务已作废（出口服务器已变更）', updated_at=? WHERE server_id=? AND state='pending'`, time.Now().Unix(), serverID)
}

// CleanupNetTasks 清理终态历史任务（保留 recently 之内，防表无限膨胀）。
func (s *Store) CleanupNetTasks(recently time.Duration) {
	cutoff := time.Now().Add(-recently).Unix()
	s.db.Exec(`DELETE FROM net_tasks WHERE state IN ('done','failed','expired') AND updated_at < ?`, cutoff)
}
