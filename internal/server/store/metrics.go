package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"lz-monitor/internal/proto"
)

// MetricRow 是 metrics 一行（标量）加上组装出的 JSON 部分（nets 取自
// metrics_nets、disks 取自 server_disks、listens 取自 server_listens、
// 磁盘 IO 速率取自 metrics_disks）。
type MetricRow struct {
	TS       int64
	CPU      float64
	Mem      float64
	Swap     float64
	Load1    float64
	Load5    float64
	Load15   float64
	MemUsed  int64
	MemTotal int64
	TCPEst   int64
	Uptime   int64
	Disks    []proto.DiskInfo
	Nets     []proto.NetInfo
	Listens  []proto.ListenPort
	DiskIOs  []DiskIOPoint
	Temps    []proto.TempPoint
	Fans     []proto.FanPoint
	// 应用负载当前快照（54，server_apps 单行）：容器与虚拟机列表
	Containers []proto.ContainerInfo
	VMs        []proto.VMInfo
}

// DiskIOPoint 是一个设备的磁盘 IO 速率采样（历史曲线用）。
type DiskIOPoint struct {
	Device   string
	ReadBps  float64
	WriteBps float64
}

// Report 把存储行还原为等价 Report（告警引擎对实时上报与历史行统一取值）。
func (m *MetricRow) Report() proto.Report {
	return proto.Report{
		TS: m.TS, CPU: m.CPU,
		MemTotal: uint64(m.MemTotal), MemUsed: uint64(m.MemUsed), MemPercent: m.Mem,
		SwapPercent: m.Swap,
		Load1:       m.Load1, Load5: m.Load5, Load15: m.Load15,
		Uptime: uint64(m.Uptime), TCPEst: int(m.TCPEst),
		Disks: m.Disks, Nets: m.Nets, Listens: m.Listens,
		Temps: m.Temps, Fans: m.Fans,
	}
}

const metricCols = `ts, cpu, mem, swap, load1, load5, load15, mem_used, mem_total, tcp_est, uptime`

// 归档粒度与单窗时长（归档逐台逐天分窗、单窗单事务）。
const (
	archiveBucket int64 = 300   // 5 分钟桶
	archiveWindow int64 = 86400 // 一天
)

// netSlim 网卡速率的瘦身存储格式（metrics_nets 与 metrics_archive 共用）：
// 短键省空间（千万行级），只保留服务端/前端使用的 iface 与上下行速率；
// rx_bytes/tx_bytes 累计计数器仅采集端算速率用，从不落库。
type netSlim struct {
	Iface string  `json:"i"`
	RxBps float64 `json:"r"`
	TxBps float64 `json:"t"`
}

func encodeNets(nets []proto.NetInfo) (string, error) {
	if nets == nil {
		nets = []proto.NetInfo{}
	}
	slim := make([]netSlim, len(nets))
	for i, n := range nets {
		slim[i] = netSlim{Iface: n.Iface, RxBps: n.RxBps, TxBps: n.TxBps}
	}
	b, err := json.Marshal(slim)
	return string(b), err
}

// decodeNets 解析瘦身格式；空串/空数组返回 nil（等价「无网卡」）。
func decodeNets(txt string) ([]proto.NetInfo, error) {
	if txt == "" || txt == "null" || txt == "[]" {
		return nil, nil
	}
	var slim []netSlim
	if err := json.Unmarshal([]byte(txt), &slim); err != nil {
		return nil, err
	}
	if len(slim) == 0 {
		return nil, nil
	}
	out := make([]proto.NetInfo, len(slim))
	for i, n := range slim {
		out[i] = proto.NetInfo{Iface: n.Iface, RxBps: n.RxBps, TxBps: n.TxBps}
	}
	return out, nil
}

// diskSlim 磁盘 IO 速率的瘦身存储格式（metrics_disks 与 metrics_archive 共用），
// 键义与 netSlim 一致：d=设备名、r=读速率、w=写速率。
type diskSlim struct {
	Device string  `json:"d"`
	Read   float64 `json:"r"`
	Write  float64 `json:"w"`
}

// encodeDiskIOs 把磁盘 IO 采样编码为瘦身 JSON。容量字段不落库（当前值在
// server_disks），只有采集到速率的设备才编码。
func encodeDiskIOs(ios []DiskIOPoint) (string, error) {
	if len(ios) == 0 {
		ios = []DiskIOPoint{}
	}
	slim := make([]diskSlim, len(ios))
	for i, d := range ios {
		slim[i] = diskSlim{Device: d.Device, Read: d.ReadBps, Write: d.WriteBps}
	}
	b, err := json.Marshal(slim)
	return string(b), err
}

// decodeDiskIOs 解析瘦身格式；空串/空数组返回 nil（等价「无 IO 数据」，
// 旧版 agent 不产生该子表行）。
func decodeDiskIOs(txt string) ([]DiskIOPoint, error) {
	if txt == "" || txt == "null" || txt == "[]" {
		return nil, nil
	}
	var slim []diskSlim
	if err := json.Unmarshal([]byte(txt), &slim); err != nil {
		return nil, err
	}
	if len(slim) == 0 {
		return nil, nil
	}
	out := make([]DiskIOPoint, len(slim))
	for i, d := range slim {
		out[i] = DiskIOPoint{Device: d.Device, ReadBps: d.Read, WriteBps: d.Write}
	}
	return out, nil
}

// thermalSlim 温度/风扇的瘦身存储格式（metrics_thermal 与 metrics_archive 共用）：
// t=温度数组（CPU/硬盘/主板同一数组，sensor 标签区分，前端按标签分卡）、
// f=风扇数组；键义 t.s=传感器、t.t=℃、f.s=传感器、f.r=RPM。
type thermalSlim struct {
	T []tempSlim `json:"t"`
	F []fanSlim  `json:"f,omitempty"`
}

type tempSlim struct {
	Sensor string  `json:"s"`
	Temp   float64 `json:"t"`
}

type fanSlim struct {
	Sensor string `json:"s"`
	RPM    int    `json:"r"`
}

func encodeThermal(temps []proto.TempPoint, fans []proto.FanPoint) (string, error) {
	if temps == nil {
		temps = []proto.TempPoint{}
	}
	slim := thermalSlim{T: make([]tempSlim, len(temps))}
	for i, t := range temps {
		slim.T[i] = tempSlim{Sensor: t.Sensor, Temp: t.Temp}
	}
	for _, f := range fans {
		slim.F = append(slim.F, fanSlim{Sensor: f.Sensor, RPM: f.RPM})
	}
	b, err := json.Marshal(slim)
	return string(b), err
}

// decodeThermal 解析瘦身格式；空串/空数组返回 nil, nil（等价「无传感器数据」，
// 旧版 agent 不产生该子表行）。
func decodeThermal(txt string) ([]proto.TempPoint, []proto.FanPoint) {
	if txt == "" || txt == "null" || txt == "[]" {
		return nil, nil
	}
	var slim thermalSlim
	if err := json.Unmarshal([]byte(txt), &slim); err != nil {
		return nil, nil // 脏数据按无传感器处理，不阻塞查询
	}
	var temps []proto.TempPoint
	var fans []proto.FanPoint
	if len(slim.T) > 0 {
		temps = make([]proto.TempPoint, len(slim.T))
		for i, t := range slim.T {
			temps[i] = proto.TempPoint{Sensor: t.Sensor, Temp: t.Temp}
		}
	}
	if len(slim.F) > 0 {
		fans = make([]proto.FanPoint, len(slim.F))
		for i, f := range slim.F {
			fans[i] = proto.FanPoint{Sensor: f.Sensor, RPM: f.RPM}
		}
	}
	return temps, fans
}

func scanMetric(row interface{ Scan(...any) error }) (*MetricRow, error) {
	var m MetricRow
	err := row.Scan(&m.TS, &m.CPU, &m.Mem, &m.Swap, &m.Load1, &m.Load5, &m.Load15,
		&m.MemUsed, &m.MemTotal, &m.TCPEst, &m.Uptime)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// scanMetricNets 扫描标量列 + 末尾一列 nets JSON（JOIN metrics_nets 的查询）。
func scanMetricNets(row interface{ Scan(...any) error }) (*MetricRow, error) {
	var m MetricRow
	var nets sql.NullString
	err := row.Scan(&m.TS, &m.CPU, &m.Mem, &m.Swap, &m.Load1, &m.Load5, &m.Load15,
		&m.MemUsed, &m.MemTotal, &m.TCPEst, &m.Uptime, &nets)
	if err != nil {
		return nil, err
	}
	m.Nets, _ = decodeNets(nets.String) // 脏数据按无网卡处理，不阻塞查询
	return &m, nil
}

// InsertMetric 写入一条指标（同秒重复上报幂等覆盖）。
// v2 存储布局：metrics 纯标量；网卡速率瘦身进 metrics_nets；磁盘/监听端口
// 仅存当前值（server_disks / server_listens 单行 upsert，端口变化才写；
// 上报无端口信息（Listens == nil）则删除旧端口快照——展示与端口告警随即清空）。
// 磁盘 IO 速率瘦身进 metrics_disks：仅当本轮采样含速率数据（任一设备非零，
// 旧版 agent 无该字段恒为零）才写行——完全空闲周期缺行，曲线按缺口归零处理。
// 整份报告包进单事务：每次上报原本 5-8 条 autocommit 语句，机器多时
// WAL 提交与锁往返线性放大，单事务缩到 1 次提交。
func (s *Store) InsertMetric(serverID int64, ts int64, r *proto.Report) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) error {
		_, err := tx.Exec(q, args...)
		return err
	}
	if r.Disks != nil {
		b, err := json.Marshal(r.Disks)
		if err != nil {
			return err
		}
		if err := exec(`INSERT INTO server_disks (server_id, disks, ts) VALUES (?, ?, ?)
			ON CONFLICT(server_id) DO UPDATE SET disks = excluded.disks, ts = excluded.ts`,
			serverID, string(b), ts); err != nil {
			return err
		}
	}
	if r.Listens == nil {
		// 新数据无端口信息（node 模式 / 旧版 agent / 关闭 collect_listens）：
		// 旧端口快照随即失效，删除（下次带端口的上报会重新写入）
		if err := exec(`DELETE FROM server_listens WHERE server_id = ?`, serverID); err != nil {
			return err
		}
	} else {
		b, err := json.Marshal(r.Listens)
		if err != nil {
			return err
		}
		var cur sql.NullString // 与当前值相同则不写（端口集合几天才变一次）
		_ = tx.QueryRow(`SELECT listens FROM server_listens WHERE server_id = ?`, serverID).Scan(&cur)
		if !cur.Valid || cur.String != string(b) {
			if err := exec(`INSERT INTO server_listens (server_id, listens, ts) VALUES (?, ?, ?)
				ON CONFLICT(server_id) DO UPDATE SET listens = excluded.listens, ts = excluded.ts`,
				serverID, string(b), ts); err != nil {
				return err
			}
		}
	}
	// 应用负载快照（54）：单行 upsert，与 server_disks 同款只存最新。两字段
	// 全 nil（旧版 agent / node 模式 / 本机无 Docker 与 libvirt 的旧上报）视为
	// 能力缺失删除旧行；任一非 nil（空数组=有 Docker 但零容器）即覆盖
	if r.Containers == nil && r.VMs == nil {
		if err := exec(`DELETE FROM server_apps WHERE server_id = ?`, serverID); err != nil {
			return err
		}
	} else {
		contJSON := ""
		if r.Containers != nil {
			b, err := json.Marshal(r.Containers)
			if err != nil {
				return err
			}
			contJSON = string(b)
		}
		vmJSON := ""
		if r.VMs != nil {
			b, err := json.Marshal(r.VMs)
			if err != nil {
				return err
			}
			vmJSON = string(b)
		}
		if err := exec(`INSERT INTO server_apps (server_id, containers, vms, ts) VALUES (?, ?, ?, ?)
			ON CONFLICT(server_id) DO UPDATE SET containers = excluded.containers, vms = excluded.vms, ts = excluded.ts`,
			serverID, contJSON, vmJSON, ts); err != nil {
			return err
		}
	}
	if err := exec(`INSERT OR REPLACE INTO metrics
		(server_id, ts, cpu, mem, mem_used, mem_total, swap, load1, load5, load15, uptime, tcp_est)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		serverID, ts, r.CPU, r.MemPercent, r.MemUsed, r.MemTotal, r.SwapPercent,
		r.Load1, r.Load5, r.Load15, r.Uptime, r.TCPEst); err != nil {
		return err
	}
	nets, err := encodeNets(r.Nets)
	if err != nil {
		return err
	}
	if err := exec(`INSERT OR REPLACE INTO metrics_nets (server_id, ts, nets) VALUES (?, ?, ?)`,
		serverID, ts, nets); err != nil {
		return err
	}
	// 磁盘 IO 速率：同设备多挂载点已在采集端去重；无速率数据（旧版 agent /
	// 完全空闲周期）不写行
	var ios []DiskIOPoint
	for _, d := range r.Disks {
		if d.Device == "" || (d.IoRead == 0 && d.IoWrite == 0) {
			continue
		}
		ios = append(ios, DiskIOPoint{Device: d.Device, ReadBps: d.IoRead, WriteBps: d.IoWrite})
	}
	if len(ios) > 0 {
		slim, err := encodeDiskIOs(ios)
		if err != nil {
			return err
		}
		if err := exec(`INSERT OR REPLACE INTO metrics_disks (server_id, ts, disks) VALUES (?, ?, ?)`,
			serverID, ts, slim); err != nil {
			return err
		}
	}
	// 温度/风扇：本采样无任何传感器数据（旧版 agent / 无传感器虚拟机）不写行
	// ——子表缺行=无数据，不留全零垃圾
	if len(r.Temps) > 0 || len(r.Fans) > 0 {
		slim, err := encodeThermal(r.Temps, r.Fans)
		if err != nil {
			return err
		}
		if err := exec(`INSERT OR REPLACE INTO metrics_thermal (server_id, ts, thermal) VALUES (?, ?, ?)`,
			serverID, ts, slim); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CurrentListens 返回该机当前监听端口集合（server_listens 单行；无记录 nil）。
func (s *Store) CurrentListens(serverID int64) ([]proto.ListenPort, error) {
	var raw sql.NullString
	if err := s.db.QueryRow(`SELECT listens FROM server_listens WHERE server_id = ?`, serverID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !raw.Valid || raw.String == "" || raw.String == "null" {
		return nil, nil
	}
	var out []proto.ListenPort
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Bucket 是标量指标按时间桶聚合的结果，TS 为桶起始时间。
type Bucket struct {
	TS       int64
	CPU      float64
	Mem      float64
	Swap     float64
	Load1    float64
	Load5    float64
	Load15   float64
	MemUsed  float64
	MemTotal float64
	TCPEst   float64
}

// detailMinTS 返回该机明细表（metrics）最早 ts；无明细返回 (0, false)。
// 归档/明细两段查询的分割点：归档段取 [from, detailMin)，明细段取
// [max(from, detailMin), to]——无缝无重叠；明细删除只发生在归档写入
// 成功之后，不会出现两侧都缺的桶。
func (s *Store) detailMinTS(serverID int64) (int64, bool, error) {
	var ts sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(ts) FROM metrics WHERE server_id = ?`, serverID).Scan(&ts); err != nil {
		return 0, false, err
	}
	return ts.Int64, ts.Valid, nil
}

// AggregateScalars 对标量指标做分桶平均（跨归档边界时自动合并：
// 归档段读 metrics_archive——已是 5 分钟桶，等宽桶可直接再平均，桶宽
// 不低于归档粒度；明细段现算。跨边界的大范围查询 step=range/500 恒 ≥300）。
func (s *Store) AggregateScalars(serverID, from, to, step int64) ([]Bucket, error) {
	detailMin, hasDetail, err := s.detailMinTS(serverID)
	if err != nil {
		return nil, err
	}
	var out []Bucket
	archHi := to
	if hasDetail && detailMin-1 < archHi {
		archHi = detailMin - 1
	}
	if archHi >= from {
		archStep := step
		if archStep < archiveBucket {
			archStep = archiveBucket
		}
		rows, err := s.db.Query(`
			SELECT (ts/?)*? AS bucket,
			       AVG(cpu), AVG(mem), AVG(swap),
			       AVG(load1), AVG(load5), AVG(load15),
			       AVG(mem_used), AVG(mem_total), AVG(tcp_est)
			FROM metrics_archive WHERE server_id = ? AND ts BETWEEN ? AND ?
			GROUP BY bucket ORDER BY bucket`, archStep, archStep, serverID, from, archHi)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var b Bucket
			if err := rows.Scan(&b.TS, &b.CPU, &b.Mem, &b.Swap, &b.Load1, &b.Load5, &b.Load15,
				&b.MemUsed, &b.MemTotal, &b.TCPEst); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if !hasDetail {
		return out, nil
	}
	lo := from
	if detailMin > lo {
		lo = detailMin
	}
	if lo > to {
		return out, nil
	}
	rows, err := s.db.Query(`
		SELECT (ts/?)*? AS bucket,
		       AVG(cpu), AVG(mem), AVG(swap),
		       AVG(load1), AVG(load5), AVG(load15),
		       AVG(mem_used), AVG(mem_total), AVG(tcp_est)
		FROM metrics WHERE server_id = ? AND ts BETWEEN ? AND ?
		GROUP BY bucket ORDER BY bucket`, step, step, serverID, lo, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.TS, &b.CPU, &b.Mem, &b.Swap, &b.Load1, &b.Load5, &b.Load15,
			&b.MemUsed, &b.MemTotal, &b.TCPEst); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// WindowRows 取时间窗口内全部原始行（告警 sustained 判定用；
// 窗口最长 1h、10s 间隔下 ≤360 行）。needNets 时才 JOIN metrics_nets 并解码
// nets JSON（net_rx/net_tx 规则需要）；cpu/mem 等标量规则传 false 省掉
// 每行一次的 JSON 解码与 JOIN。listens 不在行内——port 规则用
// CurrentListens 当前值。
func (s *Store) WindowRows(serverID, from, to int64, needNets bool) ([]MetricRow, error) {
	q := `SELECT m.` + metricCols
	if needNets {
		q += `, n.nets FROM metrics m
		LEFT JOIN metrics_nets n ON n.server_id = m.server_id AND n.ts = m.ts`
	} else {
		q += ` FROM metrics m`
	}
	q += ` WHERE m.server_id = ? AND m.ts >= ? AND m.ts <= ? ORDER BY m.ts`
	rows, err := s.db.Query(q, serverID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricRow
	for rows.Next() {
		var m *MetricRow
		if needNets {
			m, err = scanMetricNets(rows)
		} else {
			m, err = scanMetric(rows)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// SampledRows 每个时间桶取最后一个样本（磁盘/网络历史曲线用）。
// 跨归档边界时归档段与明细段都按桶末采样（归档本身是 5 分钟桶均值，
// step≥300s 时每桶再取末行，step<300s 时退化为全量归档行）。
func (s *Store) SampledRows(serverID, from, to, step int64) ([]MetricRow, error) {
	detailMin, hasDetail, err := s.detailMinTS(serverID)
	if err != nil {
		return nil, err
	}
	var out []MetricRow
	archHi := to
	if hasDetail && detailMin-1 < archHi {
		archHi = detailMin - 1
	}
	if archHi >= from {
		// 归档段同样桶末采样（与明细段同构）：step≥归档粒度 300s 时走
		// (server_id, ts) 索引精确取桶末行，90 天档从全段 ~26k 行降到 ~90 行；
		// step<300s 时桶宽小于归档粒度，每行自成一桶，行为与全量返回一致
		rows, err := s.db.Query(`SELECT ts, cpu, mem, swap, load1, load5, load15,
			mem_used, mem_total, tcp_est, nets, disks, thermal FROM metrics_archive
			WHERE server_id = ? AND ts BETWEEN ? AND ?
			  AND ts IN (SELECT MAX(ts) FROM metrics_archive
			             WHERE server_id = ? AND ts BETWEEN ? AND ?
			             GROUP BY (ts/?)*?)
			ORDER BY ts`, serverID, from, archHi, serverID, from, archHi, step, step)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var m MetricRow
			var nets, disks, thermal sql.NullString
			var memUsed, memTotal float64 // 归档列是 REAL 均值，四舍五入回整型展示
			if err := rows.Scan(&m.TS, &m.CPU, &m.Mem, &m.Swap, &m.Load1, &m.Load5, &m.Load15,
				&memUsed, &memTotal, &m.TCPEst, &nets, &disks, &thermal); err != nil {
				rows.Close()
				return nil, err
			}
			m.MemUsed = int64(memUsed + 0.5)
			m.MemTotal = int64(memTotal + 0.5)
			m.Nets, _ = decodeNets(nets.String)
			m.DiskIOs, _ = decodeDiskIOs(disks.String)
			m.Temps, m.Fans = decodeThermal(thermal.String)
			out = append(out, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if !hasDetail {
		return out, nil
	}
	lo := from
	if detailMin > lo {
		lo = detailMin
	}
	if lo > to {
		return out, nil
	}
	// 桶末样本用 IN 子查询而非 JOIN：JOIN 写法外层 m 拿不到 ts 约束（SQLite
	// 只按 server_id 扫全服务器明细再逐行探测子查询，明细上万行时详情弹层
	// 秒级卡顿），IN 列表则走 (server_id, ts) 区间索引精确取桶末行
	rows, err := s.db.Query(`
		SELECT m.`+metricCols+`, n.nets, d.disks, h.thermal FROM metrics m
		LEFT JOIN metrics_nets n ON n.server_id = m.server_id AND n.ts = m.ts
		LEFT JOIN metrics_disks d ON d.server_id = m.server_id AND d.ts = m.ts
		LEFT JOIN metrics_thermal h ON h.server_id = m.server_id AND h.ts = m.ts
		WHERE m.server_id = ? AND m.ts BETWEEN ? AND ?
		  AND m.ts IN (SELECT MAX(ts) FROM metrics
		               WHERE server_id = ? AND ts BETWEEN ? AND ?
		               GROUP BY (ts/?)*?)
		ORDER BY m.ts`, serverID, lo, to, serverID, lo, to, step, step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m MetricRow
		var nets, disks, thermal sql.NullString
		if err := rows.Scan(&m.TS, &m.CPU, &m.Mem, &m.Swap, &m.Load1, &m.Load5, &m.Load15,
			&m.MemUsed, &m.MemTotal, &m.TCPEst, &m.Uptime, &nets, &disks, &thermal); err != nil {
			return nil, err
		}
		m.Nets, _ = decodeNets(nets.String)
		m.DiskIOs, _ = decodeDiskIOs(disks.String)
		m.Temps, m.Fans = decodeThermal(thermal.String)
		out = append(out, m)
	}
	return out, rows.Err()
}

// LatestMetric 取某服务器最新一条指标（标量自 metrics，nets/disks/listens
// 分别自各自表组装），无数据返回 (nil, nil)。
func (s *Store) LatestMetric(serverID int64) (*MetricRow, error) {
	row := s.db.QueryRow(`SELECT `+metricCols+` FROM metrics
		WHERE server_id = ? ORDER BY ts DESC LIMIT 1`, serverID)
	m, err := scanMetric(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var netsRaw sql.NullString
	if err := s.db.QueryRow(`SELECT nets FROM metrics_nets
		WHERE server_id = ? ORDER BY ts DESC LIMIT 1`, serverID).Scan(&netsRaw); err == nil {
		m.Nets, _ = decodeNets(netsRaw.String)
	}
	var diskJSON string
	if err := s.db.QueryRow(`SELECT disks FROM server_disks WHERE server_id = ?`, serverID).Scan(&diskJSON); err == nil {
		if diskJSON != "" && diskJSON != "null" {
			_ = json.Unmarshal([]byte(diskJSON), &m.Disks)
		}
	}
	var thermalRaw sql.NullString
	if err := s.db.QueryRow(`SELECT thermal FROM metrics_thermal
		WHERE server_id = ? ORDER BY ts DESC LIMIT 1`, serverID).Scan(&thermalRaw); err == nil {
		m.Temps, m.Fans = decodeThermal(thermalRaw.String)
	}
	m.Listens, _ = s.CurrentListens(serverID) // 失败按无端口处理（与旧容错一致）
	var contJSON, vmJSON string
	if err := s.db.QueryRow(`SELECT containers, vms FROM server_apps WHERE server_id = ?`, serverID).Scan(&contJSON, &vmJSON); err == nil {
		m.Containers = decodeContainers(contJSON)
		m.VMs = decodeVMs(vmJSON)
	}
	return m, nil
}

// decodeContainers / decodeVMs 还原应用负载快照 JSON；空串/"null"/脏数据按
// 无数据处理，不阻塞查询。
func decodeContainers(txt string) []proto.ContainerInfo {
	if txt == "" || txt == "null" {
		return nil
	}
	var out []proto.ContainerInfo
	if err := json.Unmarshal([]byte(txt), &out); err != nil {
		return nil
	}
	return out
}

func decodeVMs(txt string) []proto.VMInfo {
	if txt == "" || txt == "null" {
		return nil
	}
	var out []proto.VMInfo
	if err := json.Unmarshal([]byte(txt), &out); err != nil {
		return nil
	}
	return out
}

// scanJSONColumn 扫 (server_id, json_text) 两列行集，把文本交给 set 写进 out
// 中对应行。NULL/空串/"null" 一律跳过（不覆盖已有值）。
func scanJSONColumn(rows *sql.Rows, out map[int64]*MetricRow, set func(*MetricRow, string)) error {
	defer rows.Close()
	for rows.Next() {
		var id int64
		var raw sql.NullString
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		m := out[id]
		if m == nil || !raw.Valid || raw.String == "" || raw.String == "null" {
			continue
		}
		set(m, raw.String)
	}
	return rows.Err()
}

// attachAux 把 nets（metrics_nets 最新行）、disks（server_disks）、
// listens（server_listens）组装进 map 中的行（并发查询的最小集：
// 每台一次子表 seek + 两张单行小表全表）。
func (s *Store) attachAux(ids []int64, out map[int64]*MetricRow) error {
	if len(ids) == 0 {
		return nil
	}
	parts := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		parts[i] = `SELECT * FROM (SELECT server_id, nets FROM metrics_nets
			WHERE server_id = ? ORDER BY ts DESC LIMIT 1)`
		args[i] = id
	}
	q, err := s.db.Query(strings.Join(parts, " UNION ALL "), args...)
	if err != nil {
		return err
	}
	if err := scanJSONColumn(q, out, func(m *MetricRow, txt string) { m.Nets, _ = decodeNets(txt) }); err != nil {
		return err
	}

	parts = parts[:len(ids)]
	args = args[:len(ids)]
	for i, id := range ids {
		parts[i] = `SELECT * FROM (SELECT server_id, thermal FROM metrics_thermal
			WHERE server_id = ? ORDER BY ts DESC LIMIT 1)`
		args[i] = id
	}
	q, err = s.db.Query(strings.Join(parts, " UNION ALL "), args...)
	if err != nil {
		return err
	}
	if err := scanJSONColumn(q, out, func(m *MetricRow, txt string) {
		m.Temps, m.Fans = decodeThermal(txt)
	}); err != nil {
		return err
	}

	diskRows, err := s.db.Query(`SELECT server_id, disks FROM server_disks`)
	if err != nil {
		return err
	}
	if err := scanJSONColumn(diskRows, out, func(m *MetricRow, txt string) {
		_ = json.Unmarshal([]byte(txt), &m.Disks)
	}); err != nil {
		return err
	}

	listenRows, err := s.db.Query(`SELECT server_id, listens FROM server_listens`)
	if err != nil {
		return err
	}
	if err := scanJSONColumn(listenRows, out, func(m *MetricRow, txt string) {
		_ = json.Unmarshal([]byte(txt), &m.Listens)
	}); err != nil {
		return err
	}

	// 应用负载快照（54）：单行小表全查，同 server_disks 口径
	appRows, err := s.db.Query(`SELECT server_id, containers, vms FROM server_apps`)
	if err != nil {
		return err
	}
	defer appRows.Close()
	for appRows.Next() {
		var id int64
		var contJSON, vmJSON string
		if err := appRows.Scan(&id, &contJSON, &vmJSON); err != nil {
			return err
		}
		if m := out[id]; m != nil {
			m.Containers = decodeContainers(contJSON)
			m.VMs = decodeVMs(vmJSON)
		}
	}
	return appRows.Err()
}

// LatestMetrics 批量取多台服务器最新一条指标（/servers 列表专用，替代逐台
// LatestMetric 的 N+1）。主表最新行与 metrics_nets 最新行各自 UNION ALL
// （每段每台一次索引倒序 seek，不扫历史），disks/listens 两张单行小表全查。
// 无数据的服务器不在返回 map 中。
func (s *Store) LatestMetrics(serverIDs []int64) (map[int64]*MetricRow, error) {
	out := make(map[int64]*MetricRow, len(serverIDs))
	if len(serverIDs) == 0 {
		return out, nil
	}
	sel := make([]string, len(serverIDs))
	args := make([]any, len(serverIDs))
	for i, id := range serverIDs {
		sel[i] = `SELECT * FROM (SELECT server_id, ` + metricCols + ` FROM metrics
			WHERE server_id = ? ORDER BY ts DESC LIMIT 1)`
		args[i] = id
	}
	q, err := s.db.Query(strings.Join(sel, " UNION ALL "), args...)
	if err != nil {
		return nil, err
	}
	for q.Next() {
		var id int64
		var m MetricRow
		if err := q.Scan(&id, &m.TS, &m.CPU, &m.Mem, &m.Swap, &m.Load1, &m.Load5, &m.Load15,
			&m.MemUsed, &m.MemTotal, &m.TCPEst, &m.Uptime); err != nil {
			q.Close()
			return nil, err
		}
		out[id] = &m
	}
	q.Close()
	if err := q.Err(); err != nil {
		return nil, err
	}
	if err := s.attachAux(serverIDs, out); err != nil {
		return nil, err
	}
	return out, nil
}

// NetAvg 一网口窗口内的平均上下行速率（B/s）。
type NetAvg struct {
	Rx float64
	Tx float64
}

// NetAvgSince 统计自 from 起每个网口的平均上下行速率（总览自定义图表
// 「近5分钟均值」用；窗口短、原始行少，直接逐行累加）。无数据返回空 map。
func (s *Store) NetAvgSince(serverID, from int64) (map[string]*NetAvg, error) {
	rows, err := s.db.Query(`SELECT nets FROM metrics_nets
		WHERE server_id = ? AND ts >= ?`, serverID, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		rx, tx float64
		n      int
	}
	accs := map[string]*acc{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		nets, err := decodeNets(raw)
		if err != nil {
			continue
		}
		for _, n := range nets {
			a := accs[n.Iface]
			if a == nil {
				a = &acc{}
				accs[n.Iface] = a
			}
			a.rx += n.RxBps
			a.tx += n.TxBps
			a.n++
		}
	}
	out := make(map[string]*NetAvg, len(accs))
	for iface, a := range accs {
		out[iface] = &NetAvg{Rx: a.rx / float64(a.n), Tx: a.tx / float64(a.n)}
	}
	return out, rows.Err()
}

// MetricRowApprox 指标明细表近似行数：MAX(rowid) O(1) 取索引末行（清理删除后
// rowid 不复用会偏高，仅供量级参考；COUNT(*) 在千万行级要全表扫不可用）。
func (s *Store) MetricRowApprox() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT MAX(rowid) FROM metrics`).Scan(&n)
	return n, err
}

// ArchiveRowsApprox 归档表近似行数（口径同 MetricRowApprox）。
func (s *Store) ArchiveRowsApprox() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT MAX(rowid) FROM metrics_archive`).Scan(&n)
	return n, err
}

// ArchiveBefore 把 ts 之前的明细按 5 分钟桶降采样进 metrics_archive，
// 写入成功后删除对应明细（metrics 与 metrics_nets 同窗）。逐台逐天分窗、
// 单窗单事务：中途崩溃重跑当前窗即可（INSERT OR REPLACE 幂等）。
// 返回归档桶数。
func (s *Store) ArchiveBefore(ts int64) (int64, error) {
	rows, err := s.db.Query(`SELECT id FROM servers`)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var total int64
	for _, id := range ids {
		for {
			lo, has, err := s.detailMinTS(id)
			if err != nil {
				return total, err
			}
			if !has || lo >= ts {
				break
			}
			hi := lo + archiveWindow
			if hi > ts {
				hi = ts
			}
			n, err := s.archiveOneWindow(id, lo, hi)
			if err != nil {
				return total, fmt.Errorf("归档 server=%d [%d,%d): %w", id, lo, hi, err)
			}
			total += n
		}
	}
	return total, nil
}

// archiveOneWindow 归档一台机器 [lo, hi) 的明细：标量桶均值一条 SQL，
// nets 桶均值 Go 端累加（JSON 列 SQL 聚合不了），随后删除该窗明细。
func (s *Store) archiveOneWindow(serverID, lo, hi int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR REPLACE INTO metrics_archive
		(server_id, ts, cpu, mem, mem_used, mem_total, swap, load1, load5, load15, tcp_est, nets)
		SELECT server_id, (ts/?)*?, AVG(cpu), AVG(mem), AVG(mem_used), AVG(mem_total),
		       AVG(swap), AVG(load1), AVG(load5), AVG(load15), AVG(tcp_est), ''
		FROM metrics WHERE server_id = ? AND ts >= ? AND ts < ?
		GROUP BY server_id, (ts/?)*?`,
		archiveBucket, archiveBucket, serverID, lo, hi, archiveBucket, archiveBucket)
	if err != nil {
		return 0, err
	}
	// nets 桶均值：读子表累加后逐桶 UPDATE（rows 关闭后才允许同事务写）
	rows, err := tx.Query(`SELECT ts, nets FROM metrics_nets
		WHERE server_id = ? AND ts >= ? AND ts < ? ORDER BY ts`, serverID, lo, hi)
	if err != nil {
		return 0, err
	}
	type netAcc struct {
		rx, tx float64
		n      int
	}
	buckets := map[int64]map[string]*netAcc{}
	for rows.Next() {
		var ts int64
		var raw string
		if err := rows.Scan(&ts, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		nets, err := decodeNets(raw)
		if err != nil {
			continue
		}
		b := ts / archiveBucket * archiveBucket
		m := buckets[b]
		if m == nil {
			m = map[string]*netAcc{}
			buckets[b] = m
		}
		for _, nt := range nets {
			a := m[nt.Iface]
			if a == nil {
				a = &netAcc{}
				m[nt.Iface] = a
			}
			a.rx += nt.RxBps
			a.tx += nt.TxBps
			a.n++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for bts, m := range buckets {
		avg := make([]proto.NetInfo, 0, len(m))
		for iface, a := range m {
			avg = append(avg, proto.NetInfo{Iface: iface,
				RxBps: a.rx / float64(a.n), TxBps: a.tx / float64(a.n)})
		}
		slim, err := encodeNets(avg)
		if err != nil {
			continue
		}
		if _, err := tx.Exec(`UPDATE metrics_archive SET nets = ?
			WHERE server_id = ? AND ts = ?`, slim, serverID, bts); err != nil {
			return 0, err
		}
	}
	// 磁盘 IO 桶均值：与 nets 同款（读子表累加后逐桶 UPDATE）
	rows, err = tx.Query(`SELECT ts, disks FROM metrics_disks
		WHERE server_id = ? AND ts >= ? AND ts < ? ORDER BY ts`, serverID, lo, hi)
	if err != nil {
		return 0, err
	}
	type ioAcc struct {
		r, w float64
		n    int
	}
	ioBuckets := map[int64]map[string]*ioAcc{}
	for rows.Next() {
		var ts int64
		var raw string
		if err := rows.Scan(&ts, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		ios, err := decodeDiskIOs(raw)
		if err != nil {
			continue
		}
		b := ts / archiveBucket * archiveBucket
		m := ioBuckets[b]
		if m == nil {
			m = map[string]*ioAcc{}
			ioBuckets[b] = m
		}
		for _, d := range ios {
			a := m[d.Device]
			if a == nil {
				a = &ioAcc{}
				m[d.Device] = a
			}
			a.r += d.ReadBps
			a.w += d.WriteBps
			a.n++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for bts, m := range ioBuckets {
		avg := make([]DiskIOPoint, 0, len(m))
		for dev, a := range m {
			avg = append(avg, DiskIOPoint{Device: dev,
				ReadBps: a.r / float64(a.n), WriteBps: a.w / float64(a.n)})
		}
		slim, err := encodeDiskIOs(avg)
		if err != nil {
			continue
		}
		if _, err := tx.Exec(`UPDATE metrics_archive SET disks = ?
			WHERE server_id = ? AND ts = ?`, slim, serverID, bts); err != nil {
			return 0, err
		}
	}
	// 温度/风扇桶均值：与 nets/disks 同款（读子表逐传感器累加后逐桶 UPDATE）
	rows, err = tx.Query(`SELECT ts, thermal FROM metrics_thermal
		WHERE server_id = ? AND ts >= ? AND ts < ? ORDER BY ts`, serverID, lo, hi)
	if err != nil {
		return 0, err
	}
	type tempAcc struct {
		t float64
		n int
	}
	type fanAcc struct {
		r float64
		n int
	}
	thBuckets := map[int64]struct {
		temps map[string]*tempAcc
		fans  map[string]*fanAcc
	}{}
	for rows.Next() {
		var ts int64
		var raw string
		if err := rows.Scan(&ts, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		temps, fans := decodeThermal(raw)
		if len(temps) == 0 && len(fans) == 0 {
			continue
		}
		b := ts / archiveBucket * archiveBucket
		bk := thBuckets[b]
		if bk.temps == nil {
			bk.temps = map[string]*tempAcc{}
			bk.fans = map[string]*fanAcc{}
			thBuckets[b] = bk
		}
		for _, t := range temps {
			a := bk.temps[t.Sensor]
			if a == nil {
				a = &tempAcc{}
				bk.temps[t.Sensor] = a
			}
			a.t += t.Temp
			a.n++
		}
		for _, f := range fans {
			a := bk.fans[f.Sensor]
			if a == nil {
				a = &fanAcc{}
				bk.fans[f.Sensor] = a
			}
			a.r += float64(f.RPM)
			a.n++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for bts, bk := range thBuckets {
		temps := make([]proto.TempPoint, 0, len(bk.temps))
		for sensor, a := range bk.temps {
			temps = append(temps, proto.TempPoint{Sensor: sensor, Temp: a.t / float64(a.n)})
		}
		fans := make([]proto.FanPoint, 0, len(bk.fans))
		for sensor, a := range bk.fans {
			fans = append(fans, proto.FanPoint{Sensor: sensor, RPM: int(a.r/float64(a.n) + 0.5)})
		}
		// 桶内按传感器名排序：map 遍历序随机，不排序则归档桶内顺序抖动
		//（TestThermalRoundTrip 偶发 CPU 槽位拿到 sda 值即此）
		sort.Slice(temps, func(i, j int) bool { return temps[i].Sensor < temps[j].Sensor })
		sort.Slice(fans, func(i, j int) bool { return fans[i].Sensor < fans[j].Sensor })
		slim, err := encodeThermal(temps, fans)
		if err != nil {
			continue
		}
		if _, err := tx.Exec(`UPDATE metrics_archive SET thermal = ?
			WHERE server_id = ? AND ts = ?`, slim, serverID, bts); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM metrics WHERE server_id = ? AND ts >= ? AND ts < ?`,
		serverID, lo, hi); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM metrics_nets WHERE server_id = ? AND ts >= ? AND ts < ?`,
		serverID, lo, hi); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM metrics_disks WHERE server_id = ? AND ts >= ? AND ts < ?`,
		serverID, lo, hi); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM metrics_thermal WHERE server_id = ? AND ts >= ? AND ts < ?`,
		serverID, lo, hi); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// deleteBatched 按批删除 table 中 ts 之前的行（table 必须来自包内字面量，
// 不得拼接外部输入），返回删除总行数。批量 + 循环避免大事务长时间持锁。
func (s *Store) deleteBatched(table string, ts int64, batch int) (int64, error) {
	if batch <= 0 {
		batch = 5000
	}
	var total int64
	for {
		res, err := s.db.Exec(`DELETE FROM `+table+` WHERE rowid IN
			(SELECT rowid FROM `+table+` WHERE ts < ? LIMIT ?)`, ts, batch)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(batch) {
			return total, nil
		}
	}
}

// DeleteArchiveBefore 删除保留期外的归档桶（分批，返回删除行数）。
func (s *Store) DeleteArchiveBefore(ts int64) (int64, error) {
	return s.deleteBatched("metrics_archive", ts, 5000)
}

// DeleteMetricsBefore 分批删除 ts 之前的指标明细（主表与 nets/disks/thermal
// 子表），返回删除行数。正常运行时归档先删（7 天边界），这里只兜底保留期外
// 的残留。
func (s *Store) DeleteMetricsBefore(ts int64, batch int) (int64, error) {
	n, err := s.deleteBatched("metrics", ts, batch)
	if err != nil {
		return n, err
	}
	nets, err := s.deleteBatched("metrics_nets", ts, batch)
	if err != nil {
		return n + nets, err
	}
	disks, err := s.deleteBatched("metrics_disks", ts, batch)
	if err != nil {
		return n + nets + disks, err
	}
	thermal, err := s.deleteBatched("metrics_thermal", ts, batch)
	return n + nets + disks + thermal, err
}

// Vacuum 回收已删除数据占用的空间（配合 auto_vacuum=INCREMENTAL）。
func (s *Store) Vacuum() error {
	_, err := s.db.Exec(`PRAGMA incremental_vacuum`)
	return err
}
