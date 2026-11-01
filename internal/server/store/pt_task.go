package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// PT 抓取任务队列（391 扩展，2026-10-05 用户指定：每天 06:00 自动 + 手动刷新；
// 家里 NAS 为 push 模式 agent，无监听端口——中转抓取走「server 入队 → agent
// 上报时领任务 → 回传结果」的既有上报通道，零公网暴露）。
//
// 防死循环纪律（设计评审定稿，勿破）：
//   - 任务一次性：执行过一轮（无论成败）即终态，绝不自动重入队；重抓只有
//     下一个 06:00 轮次或用户手动两个入口；
//   - 重试不触网：仅「未确认执行」的任务（lease 超时）允许一次重派
//     （attempt≤2）；agent 已回传的结果消费失败只重解析，绝不重派；
//   - 全链路幂等：入队查重（每站最多 1 个活跃任务）+ 领取原子租出 +
//     回传按 task_id+state 去重（done 后重复结果丢弃）；
//   - 频控兜底：每站单个抓取日（06:00 起算）任务总数硬上限，任何上层 bug
//     的爆炸半径被封死。

// PtTaskState 任务状态机：pending → leased → done/failed；lease 超时回收回
// pending（attempt<2）或转 failed。failed/expired 为终态，永不复活。
// pending/done 仅以 SQL 字面量出现（见下方查询），不设常量。
const (
	PtTaskLeased  = "leased"
	PtTaskFailed  = "failed"
	PtTaskExpired = "expired"
)

// PtTask 防封硬指标：每站单个抓取日任务上限（抓取日=本地 06:00 起算，与每日
// 轮对齐——新的一天配额自动清零，06:00 定时轮永远有配额，不会被前一天的手动
// 刷新挤掉；正常用量每天 1~3 个任务远够用。20=用户指定 2026-10-07，仍远低于
// 构成刷站的量级）。
const PtTaskDailyLimit = 20

// PtTaskLeaseTimeout 租约超时：单站执行最长约 1 分钟（4 请求×0.4s 间隔+网络），
// 取 5 倍余量；超时回收计 attempt。
const PtTaskLeaseTimeout = 5 * time.Minute

// PtTaskMaxAttempts 重派上限：只有 lease 超时（未确认执行）才可能走到 2。
const PtTaskMaxAttempts = 2

// PtTask 对应 pt_tasks 一行。Cookie 加密存库（同主表口径），领取时解密下发。
type PtTask struct {
	ID        int64  `json:"id"`
	SiteID    int64  `json:"site_id"`
	ServerID  int64  `json:"server_id"`
	SiteKey   string `json:"site_key"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Cookie    string `json:"-"`
	UA        string `json:"ua"`
	Attempt   int64  `json:"attempt"`
	State     string `json:"state"`
	LeasedAt  int64  `json:"leased_at"`
	Error     string `json:"error"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// PtTaskResult agent 回传的执行结果：快照或错误二选一。
type PtTaskResult struct {
	Snapshot *PtSnapshot `json:"snapshot,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// ErrPtTaskActive / ErrPtTaskRateLimit / ErrPtTaskNotFound 任务队列错误。
var (
	ErrPtTaskActive    = errors.New("该站点已有进行中的抓取任务")
	ErrPtTaskRateLimit = errors.New("该站点当日抓取任务数已达上限，为防频繁请求已拦截")
	ErrPtTaskNotFound  = errors.New("抓取任务不存在")
)

const ptTaskCols = `id, site_id, server_id, site_key, name, base_url, cookie, ua, attempt, state, leased_at, error, created_at, updated_at`

func scanPtTask(row interface{ Scan(...any) error }) (*PtTask, error) {
	var t PtTask
	err := row.Scan(&t.ID, &t.SiteID, &t.ServerID, &t.SiteKey, &t.Name, &t.BaseURL, &t.Cookie, &t.UA,
		&t.Attempt, &t.State, &t.LeasedAt, &t.Error, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPtTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ptTaskWindowStart 当前抓取日的起点：最近一个已过去的本地 06:00。频控按
// 抓取日计数而非滚动 24h——滚动窗口下前一天傍晚用满配额会连今天 06:00 的
// 定时轮一起拦掉，且与「明天 06:00 恢复」的用户承诺对不上（2026-10-06 435）。
func ptTaskWindowStart(now time.Time) int64 {
	w := time.Date(now.Year(), now.Month(), now.Day(), 6, 0, 0, 0, now.Location())
	if w.After(now) {
		w = w.AddDate(0, 0, -1)
	}
	return w.Unix()
}

// CreatePtTask 入队：查重（活跃任务唯一）+ 当日（06:00 起算）频控后写入。
// Cookie 加密落库。部分唯一索引 idx_pt_tasks_active 兜底并发入队。
func (s *Store) CreatePtTask(site *PtSite, serverID int64) (int64, error) {
	var active int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pt_tasks WHERE site_id = ? AND state IN ('pending','leased')`, site.ID).Scan(&active); err != nil {
		return 0, err
	}
	if active > 0 {
		return 0, ErrPtTaskActive
	}
	var recent int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pt_tasks WHERE site_id = ? AND created_at > ?`,
		site.ID, ptTaskWindowStart(time.Now())).Scan(&recent); err != nil {
		return 0, err
	}
	if recent >= PtTaskDailyLimit {
		return 0, ErrPtTaskRateLimit
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO pt_tasks (site_id, server_id, site_key, name, base_url, cookie, ua, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		site.ID, serverID, site.SiteKey, site.Name, site.BaseURL, s.seal(site.Cookie), site.UA, now, now)
	if err != nil {
		// 并发入队撞部分唯一索引 → 视为已有活跃任务
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrPtTaskActive
		}
		return 0, err
	}
	return res.LastInsertId()
}

// ClaimPtTasks agent 领取：该出口服务器名下 pending 任务原子租出（attempt+1），
// 返回租出的任务（Cookie 已解密，供直接执行）。
func (s *Store) ClaimPtTasks(serverID int64, limit int) ([]PtTask, error) {
	now := time.Now().Unix()
	rows, err := s.db.Query(`SELECT `+ptTaskCols+` FROM pt_tasks WHERE state = 'pending' AND server_id = ? ORDER BY id LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, err
	}
	var claimed []PtTask
	for rows.Next() {
		t, err := scanPtTask(rows)
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
	var out []PtTask
	for i := range claimed {
		t := &claimed[i]
		res, err := s.db.Exec(`UPDATE pt_tasks SET state='leased', leased_at=?, attempt=attempt+1, updated_at=? WHERE id=? AND state='pending'`, now, now, t.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // 已被其他领取者租出（防并发，理论罕见）
		}
		t.Cookie = s.unseal(t.Cookie)
		t.State, t.LeasedAt, t.Attempt = PtTaskLeased, now, t.Attempt+1
		out = append(out, *t)
	}
	return out, nil
}

// FinishPtTask 回传结果（幂等）：仅 leased 状态接受，done 后重复回传丢弃；
// 返回是否本次真正完成（首次）——只有首次才消费结果。执行成败都是终态，
// 绝不因失败重派（防死循环核心纪律）。
func (s *Store) FinishPtTask(taskID int64, result *PtTaskResult) (bool, error) {
	var state string
	if err := s.db.QueryRow(`SELECT state FROM pt_tasks WHERE id = ?`, taskID).Scan(&state); err != nil {
		return false, err
	}
	if state != PtTaskLeased {
		return false, nil // 已终态：重复回传/过期回收，幂等丢弃
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	errMsg := strings.TrimSpace(result.Error)
	res, err := s.db.Exec(`UPDATE pt_tasks SET state='done', result_json=?, error=?, updated_at=? WHERE id=? AND state='leased'`,
		string(payload), errMsg, time.Now().Unix(), taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReclaimExpiredPtTasks 租约超时回收：未确认执行（leased 超时）的任务按
// attempt 分流——attempt<2 回 pending 重派一次，否则 failed 终态。返回被回收
// 转入 failed 的任务数（供日志）。
func (s *Store) ReclaimExpiredPtTasks() int {
	cutoff := time.Now().Add(-PtTaskLeaseTimeout).Unix()
	now := time.Now().Unix()
	// 先把超期任务分入组
	rows, err := s.db.Query(`SELECT id, attempt FROM pt_tasks WHERE state='leased' AND leased_at < ?`, cutoff)
	if err != nil {
		return 0
	}
	var retryIDs, deadIDs []int64
	for rows.Next() {
		var id, attempt int64
		if rows.Scan(&id, &attempt) == nil {
			if attempt < PtTaskMaxAttempts {
				retryIDs = append(retryIDs, id)
			} else {
				deadIDs = append(deadIDs, id)
			}
		}
	}
	rows.Close()
	for _, id := range retryIDs {
		s.db.Exec(`UPDATE pt_tasks SET state='pending', leased_at=0, updated_at=? WHERE id=? AND state='leased' AND leased_at < ?`, now, id, cutoff)
	}
	for _, id := range deadIDs {
		s.db.Exec(`UPDATE pt_tasks SET state='failed', error='出口服务器租约超时且已达重派上限', updated_at=? WHERE id=? AND state='leased' AND leased_at < ?`, now, id, cutoff)
	}
	return len(deadIDs)
}

// GetPtTask 单任务。
func (s *Store) GetPtTask(id int64) (*PtTask, error) {
	return scanPtTask(s.db.QueryRow(`SELECT `+ptTaskCols+` FROM pt_tasks WHERE id = ?`, id))
}

// ExpirePtTasksBySite 站点停用/换绑出口时作废其 pending 任务（leased 的等租约
// 超时自然回收——agent 可能正拿着在跑，强改状态反而破坏幂等）。
func (s *Store) ExpirePtTasksBySite(siteID int64) {
	s.db.Exec(`UPDATE pt_tasks SET state='expired', error='任务已作废（站点停用或换绑出口）', updated_at=? WHERE site_id=? AND state='pending'`, time.Now().Unix(), siteID)
}

// ExpirePtTasksByServer 出口服务器被删除时作废其 pending 任务（leased 的等
// 租约超时回收——agent 可能正拿着在跑）。
func (s *Store) ExpirePtTasksByServer(serverID int64) {
	s.db.Exec(`UPDATE pt_tasks SET state='expired', error='任务已作废（出口服务器已删除）', updated_at=? WHERE server_id=? AND state='pending'`, time.Now().Unix(), serverID)
}

// DropPtTasksBySite 删除站点时清全部任务（含历史）。
func (s *Store) DropPtTasksBySite(siteID int64) {
	s.db.Exec(`DELETE FROM pt_tasks WHERE site_id = ?`, siteID)
}

// CleanupPtTasks 清理终态历史任务（保留 recently 之内，防表无限膨胀；
// 同 CleanupNetTasks 口径——每日 06:00 轮 + 手动刷新持续新增终态行）。
func (s *Store) CleanupPtTasks(recently time.Duration) {
	cutoff := time.Now().Add(-recently).Unix()
	s.db.Exec(`DELETE FROM pt_tasks WHERE state IN ('done','failed','expired') AND updated_at < ?`, cutoff)
}

// ConsumePtTaskResult 消费回传结果：成功把快照写进站点主表（覆盖式）+ 记健康态。
// 只动库不触网——解析失败重试的是这一步，绝不会让 agent 重跑。
func (s *Store) ConsumePtTaskResult(siteID int64, result *PtTaskResult) error {
	if result.Error != "" || result.Snapshot == nil {
		return s.RecordPtSiteStatus(siteID, false, result.Error)
	}
	if err := s.SetPtSiteData(siteID, result.Snapshot); err != nil {
		return err
	}
	return s.RecordPtSiteStatus(siteID, true, "")
}
