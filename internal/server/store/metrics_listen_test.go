package store

import (
	"database/sql"
	"encoding/json"
	"testing"

	"lz-monitor/internal/proto"
)

func tcpPorts(ports ...int) []proto.ListenPort {
	out := make([]proto.ListenPort, 0, len(ports))
	for _, p := range ports {
		out = append(out, proto.ListenPort{Proto: "tcp", Port: uint16(p), Addrs: []string{"0.0.0.0"}})
	}
	return out
}

func listenReport(ts int64, listens []proto.ListenPort) *proto.Report {
	return &proto.Report{TS: ts, CPU: 1, Listens: listens}
}

// rawListens 直接读 server_listens 单行（区分 NULL 与文本），验证变化才写。
func rawListens(t *testing.T, s *Store, serverID int64) sql.NullString {
	t.Helper()
	var v sql.NullString
	if err := s.DB().QueryRow(`SELECT listens FROM server_listens WHERE server_id = ?`, serverID).Scan(&v); err != nil {
		t.Fatalf("读 server_listens: %v", err)
	}
	return v
}

func listenJSON(t *testing.T, ports []proto.ListenPort) string {
	t.Helper()
	b, err := json.Marshal(ports)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestListensStoreOnChange v2 语义：监听端口仅存当前值（server_listens 单行），
// 端口集合变化才更新该行（ts 随之推进）；LatestMetric/CurrentListens 取当前集合，
// WindowRows 的行不再携带 listens 历史（port 规则由引擎用 CurrentListens 评估）。
func TestListensStoreOnChange(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	p1 := tcpPorts(22)
	p2 := tcpPorts(22, 443)

	// t=100 首次上报 → 落库
	if err := s.InsertMetric(a.ID, 100, listenReport(100, p1)); err != nil {
		t.Fatal(err)
	}
	// t=110 端口集合未变 → 不更新（ts 保持 100）
	if err := s.InsertMetric(a.ID, 110, listenReport(110, p1)); err != nil {
		t.Fatal(err)
	}
	// t=120 端口变化（多监听 443）→ 更新
	if err := s.InsertMetric(a.ID, 120, listenReport(120, p2)); err != nil {
		t.Fatal(err)
	}

	// 单行当前值 = 最近一次变化（p2 @120）
	if v := rawListens(t, s, a.ID); !v.Valid || v.String != listenJSON(t, p2) {
		t.Fatalf("server_listens 应为最近变化的集合: %+v", v)
	}
	var ts int64
	if err := s.DB().QueryRow(`SELECT ts FROM server_listens WHERE server_id = ?`, a.ID).Scan(&ts); err != nil || ts != 120 {
		t.Fatalf("未变化不应推进 ts: ts=%d err=%v", ts, err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM server_listens WHERE server_id = ?`, a.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("server_listens 应单行: n=%d err=%v", n, err)
	}

	// LatestMetric 合到当前端口集合
	m, err := s.LatestMetric(a.ID)
	if err != nil || m == nil {
		t.Fatalf("LatestMetric: %v %v", m, err)
	}
	if len(m.Listens) != 2 || m.Listens[1].Port != 443 {
		t.Fatalf("最新 listens 应为 2 端口: %+v", m.Listens)
	}

	// CurrentListens 与 WindowRows（行本身不带 listens）
	cur, err := s.CurrentListens(a.ID)
	if err != nil || len(cur) != 2 {
		t.Fatalf("CurrentListens: %+v err=%v", cur, err)
	}
	rows, err := s.WindowRows(a.ID, 110, 130, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("窗口内应 2 行: %d", len(rows))
	}
	for _, r := range rows {
		if r.Listens != nil {
			t.Fatalf("窗口行不再携带 listens 历史: ts=%d", r.TS)
		}
	}

	// t=140 上报无端口信息（node 模式 / 旧 agent / 关闭 collect_listens）→
	// 旧端口快照删除（展示与端口告警随即清空，不让过期端口滞留）
	if err := s.InsertMetric(a.ID, 140, listenReport(140, nil)); err != nil {
		t.Fatal(err)
	}
	var n2 int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM server_listens WHERE server_id = ?`, a.ID).Scan(&n2); err != nil || n2 != 0 {
		t.Fatalf("nil 上报后 server_listens 行应删除: n=%d err=%v", n2, err)
	}
	cur2, err := s.CurrentListens(a.ID)
	if err != nil || cur2 != nil {
		t.Fatalf("nil 上报后 CurrentListens 应为 nil: %+v err=%v", cur2, err)
	}
}

// TestListensEmptySlice 端口清空（nil 与空切片语义不同）：全部下线存 "[]"，
// port 规则按 0 个监听评估而不是跳过。
func TestListensEmptySlice(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMetric(a.ID, 100, listenReport(100, tcpPorts(22))); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMetric(a.ID, 110, listenReport(110, []proto.ListenPort{})); err != nil {
		t.Fatal(err)
	}
	if v := rawListens(t, s, a.ID); !v.Valid || v.String != "[]" {
		t.Fatalf("清空端口应存 [] 而非 NULL: %+v", v)
	}
	m, _ := s.LatestMetric(a.ID)
	if m == nil || m.Listens == nil || len(m.Listens) != 0 {
		t.Fatalf("LatestMetric 应为非 nil 空切片: %+v", m.Listens)
	}
}

// TestListensOldAgentOnly 从未上报过 listens 的机器：server_listens 无行、
// CurrentListens 返回 nil（port 告警跳过评估不误报）。
func TestListensOldAgentOnly(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ts := range []int64{100, 110, 120} {
		if err := s.InsertMetric(a.ID, ts, listenReport(ts, nil)); err != nil {
			t.Fatal(err)
		}
	}
	cur, err := s.CurrentListens(a.ID)
	if err != nil || cur != nil {
		t.Fatalf("从未上报 listens 应返回 nil: %+v err=%v", cur, err)
	}
	m, _ := s.LatestMetric(a.ID)
	if m == nil || m.Listens != nil {
		t.Fatalf("从未上报 listens 应为 nil: %+v", m.Listens)
	}
}
