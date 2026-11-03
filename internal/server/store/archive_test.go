package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"lz-monitor/internal/proto"
)

// insertRows 给指定机器插 10s 间隔明细行：CPU 按序号线性增长（桶均值可精确
// 断言），nets 固定速率 eth0 100/200。
func insertRows(t *testing.T, s *Store, id, from, n int64) {
	t.Helper()
	for i := int64(0); i < n; i++ {
		ts := from + i*10
		rep := &proto.Report{
			TS: ts, CPU: float64(i), MemPercent: 50,
			MemUsed: 500, MemTotal: 1000, TCPEst: 10,
			Nets: []proto.NetInfo{{Iface: "eth0", RxBps: 100, TxBps: 200, RxBytes: 999}},
		}
		if err := s.InsertMetric(id, ts, rep); err != nil {
			t.Fatal(err)
		}
	}
}

func countRows(t *testing.T, s *Store, table string, args ...any) int64 {
	t.Helper()
	var n int64
	q := `SELECT COUNT(*) FROM ` + table
	var row *sql.Row
	if len(args) > 0 {
		row = s.DB().QueryRow(q+` WHERE server_id = ?`, args[0])
	} else {
		row = s.DB().QueryRow(q)
	}
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestArchiveMerge 归档降采样 + 跨边界查询：前 1 小时归档成 12 个 5 分钟桶，
// AggregateScalars/SampledRows 应无缝拼出归档段与明细段；重跑归档幂等。
func TestArchiveMerge(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	// t0 对齐 5 分钟；2 小时 = 720 行
	const t0 int64 = 1700000100 // 已是 300 的倍数
	const n = 720
	insertRows(t, s, a.ID, t0, n)

	// 归档 [t0, t0+3600)：360 行 → 12 桶
	got, err := s.ArchiveBefore(t0 + 3600)
	if err != nil {
		t.Fatal(err)
	}
	if got != 12 {
		t.Fatalf("应归档 12 桶: %d", got)
	}
	if n := countRows(t, s, "metrics", a.ID); n != 360 {
		t.Fatalf("明细应剩 360 行: %d", n)
	}
	if n := countRows(t, s, "metrics_nets", a.ID); n != 360 {
		t.Fatalf("nets 子表应剩 360 行: %d", n)
	}
	if n := countRows(t, s, "metrics_archive", a.ID); n != 12 {
		t.Fatalf("归档应 12 桶: %d", n)
	}

	// 幂等：重跑同一边界不产生新桶
	got, err = s.ArchiveBefore(t0 + 3600)
	if err != nil || got != 0 {
		t.Fatalf("重跑应 0 桶: %d err=%v", got, err)
	}

	// AggregateScalars 跨边界（step=300）：24 桶无缝
	buckets, err := s.AggregateScalars(a.ID, t0, t0+7200, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 24 {
		t.Fatalf("跨边界应 24 桶: %d", len(buckets))
	}
	for i := 1; i < len(buckets); i++ {
		if buckets[i].TS <= buckets[i-1].TS {
			t.Fatalf("桶应升序无缝: [%d]=%d", i, buckets[i].TS)
		}
	}
	// 首桶（归档段）：行 0~29 的 CPU 均值 = (0+29)/2 = 14.5
	if buckets[0].TS != t0 || buckets[0].CPU != 14.5 {
		t.Fatalf("首桶应 t0/14.5: ts=%d cpu=%v", buckets[0].TS, buckets[0].CPU)
	}
	// 末桶（明细段）：行 690~719 的均值
	if buckets[23].CPU != 704.5 {
		t.Fatalf("末桶 CPU 应 704.5: %v", buckets[23].CPU)
	}

	// SampledRows 跨边界：归档段桶行带 nets 均值，明细段桶末采样
	rows, err := s.SampledRows(a.ID, t0, t0+7200, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 24 {
		t.Fatalf("SampledRows 应 24 行: %d", len(rows))
	}
	for i, r := range rows {
		if len(r.Nets) != 1 || r.Nets[0].Iface != "eth0" ||
			r.Nets[0].RxBps != 100 || r.Nets[0].TxBps != 200 {
			t.Fatalf("行 %d nets 应为 eth0 100/200: %+v", i, r.Nets)
		}
	}
	// 归档段行的 mem_used/mem_total 均值还原整型
	if rows[0].MemUsed != 500 || rows[0].MemTotal != 1000 {
		t.Fatalf("归档行 mem 均值应还原 500/1000: %d/%d", rows[0].MemUsed, rows[0].MemTotal)
	}

	// NetAvgSince 只查明细段（窗口落在后半）
	avg, err := s.NetAvgSince(a.ID, t0+3600)
	if err != nil || avg["eth0"] == nil || avg["eth0"].Rx != 100 {
		t.Fatalf("NetAvgSince: %+v err=%v", avg, err)
	}
}

// TestNetsSlim nets 瘦身存储：子表 JSON 只有 iface/速率短键（无累计字节），
// 读取还原为 proto.NetInfo。
func TestNetsSlim(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	rep := &proto.Report{TS: 100, CPU: 1,
		Nets: []proto.NetInfo{{Iface: "eth0", RxBytes: 111, TxBytes: 222, RxBps: 100, TxBps: 200}}}
	if err := s.InsertMetric(a.ID, 100, rep); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.DB().QueryRow(`SELECT nets FROM metrics_nets WHERE server_id = ? AND ts = 100`, a.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"i":"eth0"`) || strings.Contains(raw, "rx_bytes") {
		t.Fatalf("瘦身 JSON 应只有短键: %s", raw)
	}
	m, _ := s.LatestMetric(a.ID)
	if m == nil || len(m.Nets) != 1 || m.Nets[0].RxBps != 100 || m.Nets[0].TxBps != 200 {
		t.Fatalf("读取应还原速率: %+v", m.Nets)
	}
}

// TestMigrationV2 旧结构库迁移：nets 拆表瘦身、listens 回填单行、metrics
// 重建为纯标量且行数不变；迁移幂等（重开不重复拆）。
func TestMigrationV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mig.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// 旧 v0.3 结构：disks/nets/listens 全在 metrics 表内
	if _, err := db.Exec(`CREATE TABLE metrics (
		server_id INTEGER NOT NULL, ts INTEGER NOT NULL,
		cpu REAL NOT NULL DEFAULT 0, mem REAL, mem_used INTEGER, mem_total INTEGER,
		swap REAL, load1 REAL, load5 REAL, load15 REAL, uptime INTEGER, tcp_est INTEGER,
		disks TEXT, nets TEXT, listens TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	ins := func(ts int64, nets, listens any) {
		t.Helper()
		// 标量列全写值（与旧版 InsertMetric 行为一致，生产库无 NULL 标量）
		if _, err := db.Exec(`INSERT INTO metrics
			(server_id, ts, cpu, mem, mem_used, mem_total, swap, load1, load5, load15, uptime, tcp_est, nets, listens)
			VALUES (1, ?, 10, 50, 500, 1000, 0, 1, 1, 1, 100, 10, ?, ?)`, ts, nets, listens); err != nil {
			t.Fatal(err)
		}
	}
	ins(100, `[{"iface":"eth0","rx_bytes":1,"tx_bytes":2,"rx_bps":100,"tx_bps":200}]`, `[{"proto":"tcp","port":443,"addrs":["0.0.0.0"]}]`)
	ins(110, `[]`, nil) // 空 nets / listens 未变化（NULL）
	ins(120, ``, nil)   // 空 nets
	db.Close()

	s, err := OpenWithKey(path, filepath.Join(t.TempDir(), "key")) // 触发 v2 迁移
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	if hasMetricColumn(s.DB(), "nets") || hasMetricColumn(s.DB(), "listens") {
		t.Fatal("迁移后 metrics 不应再有 nets/listens 列")
	}
	if n := countRows(t, s, "metrics", 1); n != 3 {
		t.Fatalf("标量行数应保持 3: %d", n)
	}
	// 仅非空 nets 行拆入子表（2 行空）
	if n := countRows(t, s, "metrics_nets", 1); n != 1 {
		t.Fatalf("metrics_nets 应 1 行: %d", n)
	}
	var raw string
	if err := s.DB().QueryRow(`SELECT nets FROM metrics_nets WHERE server_id = 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"i":"eth0"`) || strings.Contains(raw, "rx_bytes") {
		t.Fatalf("拆表应同时瘦身: %s", raw)
	}
	// listens 回填最近非空值
	cur, err := s.CurrentListens(1)
	if err != nil || len(cur) != 1 || cur[0].Port != 443 {
		t.Fatalf("server_listens 应回填 443: %+v err=%v", cur, err)
	}
	// LatestMetric 组装出迁移后的完整快照（nets 取子表最新行 ts=100）
	m, err := s.LatestMetric(1)
	if err != nil || m == nil {
		t.Fatal(err)
	}
	if m.TS != 120 || m.CPU != 10 || len(m.Nets) != 1 || m.Nets[0].RxBps != 100 || len(m.Listens) != 1 {
		t.Fatalf("迁移后快照: ts=%d cpu=%v nets=%+v listens=%d", m.TS, m.CPU, m.Nets, len(m.Listens))
	}

	// 幂等：重开不再迁移（user_version=2 且新结构）
	s.Close()
	s2, err := OpenWithKey(path, filepath.Join(t.TempDir(), "key2"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if n := countRows(t, s2, "metrics_nets", 1); n != 1 {
		t.Fatalf("重开后 metrics_nets 应仍 1 行: %d", n)
	}
}
