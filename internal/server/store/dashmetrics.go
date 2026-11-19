package store

import (
	"time"
)

// DashMetric 对应 dashboard_metrics 一行：总览页顶部重点指标格。
// 数据源二选一（486）：AIID=0 时 ServerID 指向服务器，Metric 取 cpu/mem/
// swap/load1/load5/load15/tcp_est/uptime/disk/io/net_rx/net_tx/ai_*，Param 仅
// 磁盘/IO（挂载点，空=各盘最大值）与网卡（网口，空=按展示网口合计）需要；
// AIID>0 时为用量查询格子（Metric 取 ai_ 前缀指标，ServerID 忽略存 0）。
type DashMetric struct {
	ID       int64  `json:"id"`
	ServerID int64  `json:"server_id"`
	AIID     int64  `json:"ai_id"`
	Metric   string `json:"metric"`
	Param    string `json:"param"`
	Label    string `json:"label"`
}

// ListDashMetrics 返回全部重点指标配置，按拖拽排序（sort，未排过的按创建序兜底）。
func (s *Store) ListDashMetrics() ([]DashMetric, error) {
	rows, err := s.db.Query(`SELECT id, server_id, ai_id, metric, param, label FROM dashboard_metrics ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []DashMetric
	for rows.Next() {
		var m DashMetric
		if err := rows.Scan(&m.ID, &m.ServerID, &m.AIID, &m.Metric, &m.Param, &m.Label); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// CreateDashMetric 新增一个重点指标（数量上限由 API 层校验；用量格子 ServerID 传 0）。
func (s *Store) CreateDashMetric(serverID, aiID int64, metric, param, label string) (*DashMetric, error) {
	res, err := s.db.Exec(
		`INSERT INTO dashboard_metrics (server_id, ai_id, metric, param, label, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		serverID, aiID, metric, param, label, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &DashMetric{ID: id, ServerID: serverID, AIID: aiID, Metric: metric, Param: param, Label: label}, nil
}

// UpdateDashMetric 覆盖一个重点指标的配置（编辑保存；影响 0 行由 API 层报不存在）。
func (s *Store) UpdateDashMetric(id, serverID, aiID int64, metric, param, label string) error {
	_, err := s.db.Exec(
		`UPDATE dashboard_metrics SET server_id = ?, ai_id = ?, metric = ?, param = ?, label = ? WHERE id = ?`,
		serverID, aiID, metric, param, label, id)
	return err
}

// CountDashMetrics 当前重点指标数（上限校验用）。
func (s *Store) CountDashMetrics() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM dashboard_metrics`).Scan(&n)
	return n, err
}

// DeleteDashMetric 删除一个重点指标。
func (s *Store) DeleteDashMetric(id int64) error {
	_, err := s.db.Exec(`DELETE FROM dashboard_metrics WHERE id = ?`, id)
	return err
}

// ReorderDashMetrics 按传入 id 顺序写入 sort 1..n（拖拽排序保存）。
// 不存在的 id 影响零行，无副作用；单事务保证原子。
func (s *Store) ReorderDashMetrics(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE dashboard_metrics SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
