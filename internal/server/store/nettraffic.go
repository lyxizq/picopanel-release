package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"lz-monitor/internal/proto"
)

// 外网流量账本：按日累计每台服务器"外网口"的收发字节。数据源是 agent 上报里
// /proc/net/dev 的累计计数器（rx_bytes/tx_bytes），服务端对相邻两次上报做差分
// 入账——计数器是内核累计值，中间漏报/离线多久差值都仍然准确，只有计数器归零
// （重启/网卡重建）才会丢掉归零前未上报的那一段。这与 metrics_nets 的速率曲线
// 无关：速率是展示用的 10 秒均值，对它积分既漏离线时段又受降采样影响，不能当
// 用量看。
//
// 统计网口三级口径（selectTrafficIfaces）：服务器「展示网口」配置 > agent 上报的
// 默认路由出口（wan_ifaces）> 全部网口剔除常见虚拟口。日期按服务端本地时区，
// 长时间离线后恢复的差值整笔记在恢复当日（无法得知中间如何分布）。
// 账本不做留存清理：每台每天每网口一行，量级可忽略。

const netTrafficDaysDDL = `CREATE TABLE IF NOT EXISTS net_traffic_days (
  server_id INTEGER NOT NULL,
  day       TEXT    NOT NULL,            -- 服务端本地日期 YYYY-MM-DD
  iface     TEXT    NOT NULL,
  rx_bytes  INTEGER NOT NULL DEFAULT 0,
  tx_bytes  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (server_id, day, iface)
)`

const netTrafficStateDDL = `CREATE TABLE IF NOT EXISTS net_traffic_state (
  server_id INTEGER PRIMARY KEY,
  counters  TEXT    NOT NULL,            -- JSON {iface:{"rx":..,"tx":..}} 上次上报的累计计数器（仅统计网口）
  ifaces    TEXT    NOT NULL DEFAULT '', -- 上次入账的统计网口（逗号分隔，展示用）
  ts        INTEGER NOT NULL             -- 上次入账时刻（秒）
)`

// netTrafficMaxBps 单网口可信带宽上限（100 Gbps 折字节/秒）：差值超过
// 「间隔 × 上限」视为计数器异常（驱动重置为垃圾值等），本次不入账仅重建基线。
const netTrafficMaxBps = 100e9 / 8

type trafficCounter struct {
	RX uint64 `json:"rx"`
	TX uint64 `json:"tx"`
}

// recordNetTraffic 在 InsertMetric 事务内把本次上报的计数器差值记入当日账本并
// 更新基线。本轮无网卡数据（Nets 为 nil，采集失败）不动基线；首次见到的网口只
// 建基线不入账。
func recordNetTraffic(tx *sql.Tx, serverID, ts int64, r *proto.Report) error {
	if r.Nets == nil {
		return nil
	}
	var cfg string
	if err := tx.QueryRow(`SELECT net_ifaces FROM servers WHERE id = ?`, serverID).Scan(&cfg); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	sel := selectTrafficIfaces(cfg, r.WanIfaces, r.Nets)
	if len(sel) == 0 {
		return nil
	}
	want := make(map[string]bool, len(sel))
	for _, w := range sel {
		want[w] = true
	}
	cur := make(map[string]trafficCounter, len(sel))
	for _, n := range r.Nets {
		if want[n.Iface] {
			cur[n.Iface] = trafficCounter{RX: n.RxBytes, TX: n.TxBytes}
		}
	}

	var prevJSON string
	var prevTS int64
	err := tx.QueryRow(`SELECT counters, ts FROM net_traffic_state WHERE server_id = ?`, serverID).Scan(&prevJSON, &prevTS)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		prev := map[string]trafficCounter{}
		_ = json.Unmarshal([]byte(prevJSON), &prev) // 脏基线按空处理：本轮只重建
		elapsed := ts - prevTS
		if elapsed < 1 {
			elapsed = 1
		}
		limit := uint64(float64(elapsed) * netTrafficMaxBps)
		day := time.Unix(ts, 0).Format("2006-01-02")
		for _, iface := range sel {
			p, ok := prev[iface]
			if !ok {
				continue
			}
			c := cur[iface]
			drx, dtx := counterDelta(c.RX, p.RX, limit), counterDelta(c.TX, p.TX, limit)
			if drx == 0 && dtx == 0 {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO net_traffic_days (server_id, day, iface, rx_bytes, tx_bytes) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(server_id, day, iface) DO UPDATE SET
				  rx_bytes = rx_bytes + excluded.rx_bytes, tx_bytes = tx_bytes + excluded.tx_bytes`,
				serverID, day, iface, int64(drx), int64(dtx)); err != nil {
				return err
			}
		}
	}

	b, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO net_traffic_state (server_id, counters, ifaces, ts) VALUES (?, ?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET counters = excluded.counters, ifaces = excluded.ifaces, ts = excluded.ts`,
		serverID, string(b), strings.Join(sel, ","), ts)
	return err
}

// counterDelta 单向计数器差值：单调递增取差；回退视为计数器归零（重启/网卡
// 重建——归零必发生在上次上报之后，当前值即全部新增流量）；超出可信上限按
// 异常丢弃返回 0（调用方仍以当前值重建基线）。
func counterDelta(cur, prev, limit uint64) uint64 {
	d := cur
	if cur >= prev {
		d = cur - prev
	}
	if d > limit {
		return 0
	}
	return d
}

// selectTrafficIfaces 决定本次上报计入账本的网口（仅限本次上报里实际存在的）：
//  1. 服务器「展示网口」配置非空 → 只算所列（与卡片/详情页合计口径一致）；
//  2. 否则 agent 报了默认路由出口 → 只算所列；
//  3. 都没有（旧版 agent / node_exporter 模式）→ 全部网口剔除常见虚拟口。
//
// 前两级是显式口径，配置/上报了但本次都不在场就返回空、不往下落——落到第 3 级
// 会把内网口算进去，宁可这一轮不记。
func selectTrafficIfaces(netIfaces string, wan []string, nets []proto.NetInfo) []string {
	present := make(map[string]bool, len(nets))
	for _, n := range nets {
		present[n.Iface] = true
	}
	pick := func(list []string) []string {
		var out []string
		seen := map[string]bool{}
		for _, w := range list {
			w = strings.TrimSpace(w)
			if w != "" && present[w] && !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
		return out
	}
	if strings.TrimSpace(netIfaces) != "" {
		return pick(strings.Split(netIfaces, ","))
	}
	if len(wan) > 0 {
		return pick(wan)
	}
	var out []string
	for _, n := range nets {
		if !isVirtualIface(n.Iface) {
			out = append(out, n.Iface)
		}
	}
	sort.Strings(out)
	return out
}

// virtualIfacePrefixes 第 3 级口径剔除的虚拟网口前缀：容器/虚拟机侧网口与桥
// （veth/docker/br-/virbr/vnet/tap/fwbr…）、VPN 隧道（tun/wg/tailscale/zt…，隧道流量
// 同时会经物理口出去，一起算等于重复计）、CNI 插件（flannel/cni/cali）。bond、
// vmbr、ens/eth/enp 等可能承载真实出口的名字不在此列。
var virtualIfacePrefixes = []string{
	"lo", "veth", "docker", "br-", "virbr", "vnet", "vmnet", "lxcbr", "lxdbr",
	"tap", "tun", "macvtap", "macvlan", "ifb", "dummy",
	"wg", "zt", "tailscale", "flannel", "cni", "cali", "fwbr", "fwpr", "fwln",
}

func isVirtualIface(name string) bool {
	for _, p := range virtualIfacePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// NetTrafficDay 一天的收发字节合计（统计网口求和）。
type NetTrafficDay struct {
	Day string `json:"day"`
	Rx  int64  `json:"rx"`
	Tx  int64  `json:"tx"`
}

// NetTrafficMonth 一个自然月的收发字节合计。
type NetTrafficMonth struct {
	Month string `json:"month"` // YYYY-MM
	Rx    int64  `json:"rx"`
	Tx    int64  `json:"tx"`
}

// NetTrafficSummary 详情弹层外网流量卡一次取齐的数据。
type NetTrafficSummary struct {
	Ifaces  []string          `json:"ifaces"`   // 上次入账的统计网口
	Since   string            `json:"since"`    // 最早有记录的日期；空=尚无任何入账
	MonthRx int64             `json:"month_rx"` // 当前自然月合计
	MonthTx int64             `json:"month_tx"`
	Days    []NetTrafficDay   `json:"days"`   // 近 N 天逐日（仅有记录的日期，前端补零）
	Months  []NetTrafficMonth `json:"months"` // 当年逐月（仅有记录的月份）
}

// NetTrafficSummary 汇总一台服务器的账本：统计网口、起始日、本月合计、近 days 天
// 逐日、当年逐月。now 为口径基准时刻（测试可钉）。
func (s *Store) NetTrafficSummary(serverID int64, now time.Time, days int) (*NetTrafficSummary, error) {
	out := &NetTrafficSummary{Ifaces: []string{}, Days: []NetTrafficDay{}, Months: []NetTrafficMonth{}}
	var ifaces string
	if err := s.db.QueryRow(`SELECT ifaces FROM net_traffic_state WHERE server_id = ?`, serverID).Scan(&ifaces); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	for _, w := range strings.Split(ifaces, ",") {
		if w = strings.TrimSpace(w); w != "" {
			out.Ifaces = append(out.Ifaces, w)
		}
	}
	var since sql.NullString
	if err := s.db.QueryRow(`SELECT MIN(day) FROM net_traffic_days WHERE server_id = ?`, serverID).Scan(&since); err != nil {
		return nil, err
	}
	if !since.Valid {
		return out, nil
	}
	out.Since = since.String

	if days < 1 {
		days = 1
	}
	from := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	rows, err := s.db.Query(`SELECT day, SUM(rx_bytes), SUM(tx_bytes) FROM net_traffic_days
		WHERE server_id = ? AND day >= ? GROUP BY day ORDER BY day`, serverID, from)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d NetTrafficDay
		if err := rows.Scan(&d.Day, &d.Rx, &d.Tx); err != nil {
			rows.Close()
			return nil, err
		}
		out.Days = append(out.Days, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	yearFrom := now.Format("2006") + "-01-01"
	rows, err = s.db.Query(`SELECT substr(day, 1, 7) AS m, SUM(rx_bytes), SUM(tx_bytes) FROM net_traffic_days
		WHERE server_id = ? AND day >= ? GROUP BY m ORDER BY m`, serverID, yearFrom)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	curMonth := now.Format("2006-01")
	for rows.Next() {
		var m NetTrafficMonth
		if err := rows.Scan(&m.Month, &m.Rx, &m.Tx); err != nil {
			return nil, err
		}
		out.Months = append(out.Months, m)
		if m.Month == curMonth {
			out.MonthRx, out.MonthTx = m.Rx, m.Tx
		}
	}
	return out, rows.Err()
}
