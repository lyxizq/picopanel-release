package store

import "time"

// NetTrafficNow 一台服务器当日与当月的收发合计（数据监控「外网流量」格子用）。
type NetTrafficNow struct {
	TodayRx int64 `json:"today_rx"`
	TodayTx int64 `json:"today_tx"`
	MonthRx int64 `json:"month_rx"`
	MonthTx int64 `json:"month_tx"`
}

// NetTrafficTodayMonth 全部服务器的当日/当月收发合计（一条分组查询；无账本的
// 服务器不在返回里）。日期口径同入账：服务端本地时区。
func (s *Store) NetTrafficTodayMonth(now time.Time) (map[int64]NetTrafficNow, error) {
	today := now.Format("2006-01-02")
	monthFrom := now.Format("2006-01") + "-01"
	rows, err := s.db.Query(`SELECT server_id,
		SUM(CASE WHEN day = ? THEN rx_bytes ELSE 0 END), SUM(CASE WHEN day = ? THEN tx_bytes ELSE 0 END),
		SUM(rx_bytes), SUM(tx_bytes)
		FROM net_traffic_days WHERE day >= ? GROUP BY server_id`, today, today, monthFrom)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]NetTrafficNow{}
	for rows.Next() {
		var id int64
		var v NetTrafficNow
		if err := rows.Scan(&id, &v.TodayRx, &v.TodayTx, &v.MonthRx, &v.MonthTx); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}
