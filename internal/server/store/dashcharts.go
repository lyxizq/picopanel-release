package store

import (
	"time"
)

// DashChart 对应 dashboard_charts 一行：总览页自定义图表（单服务器单指标）。
// Metric 取值 cpu/mem/swap/load1/load5/load15/tcp_est/disk/net_rx/net_tx；
// Param 仅磁盘（挂载点）与网卡（网口）需要，其余为空。
type DashChart struct {
	ID       int64  `json:"id"`
	ServerID int64  `json:"server_id"`
	Metric   string `json:"metric"`
	Param    string `json:"param"`
}

// ListDashCharts 返回全部自定义图表，按拖拽排序（sort，未排过的按创建序兜底）。
func (s *Store) ListDashCharts() ([]DashChart, error) {
	rows, err := s.db.Query(`SELECT id, server_id, metric, param FROM dashboard_charts ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []DashChart
	for rows.Next() {
		var c DashChart
		if err := rows.Scan(&c.ID, &c.ServerID, &c.Metric, &c.Param); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// CreateDashChart 新增一个自定义图表（数量上限由 API 层校验）。
func (s *Store) CreateDashChart(serverID int64, metric, param string) (*DashChart, error) {
	res, err := s.db.Exec(
		`INSERT INTO dashboard_charts (server_id, metric, param, created_at) VALUES (?, ?, ?, ?)`,
		serverID, metric, param, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &DashChart{ID: id, ServerID: serverID, Metric: metric, Param: param}, nil
}

// CountDashCharts 当前图表数（上限校验用）。
func (s *Store) CountDashCharts() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM dashboard_charts`).Scan(&n)
	return n, err
}

// DeleteDashChart 删除一个自定义图表。
func (s *Store) DeleteDashChart(id int64) error {
	_, err := s.db.Exec(`DELETE FROM dashboard_charts WHERE id = ?`, id)
	return err
}

// ReorderDashCharts 按传入 id 顺序写入 sort 1..n（拖拽排序保存）。
// 不存在的 id 影响零行，无副作用；单事务保证原子。
func (s *Store) ReorderDashCharts(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE dashboard_charts SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
