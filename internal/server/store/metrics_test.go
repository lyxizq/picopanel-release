package store

import (
	"testing"

	"lz-monitor/internal/proto"
)

// TestLatestMetrics 批量最新快照：每台取 ts 最大行、磁盘合自 server_disks、
// 最新上报无端口信息（Listens 为 nil）时旧端口清除、无数据的机器不在 map 中。
func TestLatestMetrics(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateServer("B", "10.0.0.2:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateServer("C", "10.0.0.3:19100", "", 0) // 无任何指标
	if err != nil {
		t.Fatal(err)
	}

	listens := []proto.ListenPort{{Proto: "tcp", Port: 443, Addrs: []string{"0.0.0.0"}}}
	older := &proto.Report{TS: 100, CPU: 11, Listens: listens,
		Nets: []proto.NetInfo{{Iface: "eth0", RxBps: 1, TxBps: 2}}}
	newer := &proto.Report{TS: 200, CPU: 22, // Listens 为 nil（node 模式/旧 agent）→ 旧端口清除
		Disks: []proto.DiskInfo{{Mount: "/", Percent: 55.5, IOUtil: 9.9}},
		Nets:  []proto.NetInfo{{Iface: "eth0", RxBps: 3, TxBps: 4}}}
	for _, rep := range []*proto.Report{older, newer} {
		if err := s.InsertMetric(a.ID, rep.TS, rep); err != nil {
			t.Fatal(err)
		}
	}
	// B 只有一行，listens 直接非空
	bRep := &proto.Report{TS: 150, CPU: 33, Listens: listens}
	if err := s.InsertMetric(b.ID, bRep.TS, bRep); err != nil {
		t.Fatal(err)
	}

	m, err := s.LatestMetrics([]int64{a.ID, b.ID, c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("无数据的 C 不应在 map 中: n=%d", len(m))
	}
	ma := m[a.ID]
	if ma == nil || ma.TS != 200 || ma.CPU != 22 {
		t.Fatalf("A 应取最新行 ts=200: %+v", ma)
	}
	if len(ma.Disks) != 1 || ma.Disks[0].Percent != 55.5 {
		t.Fatalf("A 磁盘应合自 server_disks: %+v", ma.Disks)
	}
	if ma.Listens != nil {
		t.Fatalf("A 最新上报无端口信息，旧端口应清除: %+v", ma.Listens)
	}
	if mb := m[b.ID]; mb == nil || mb.TS != 150 || len(mb.Listens) != 1 {
		t.Fatalf("B 最新行应带非空 listens: %+v", mb)
	}

	// 空列表直接空 map，不报错
	if m, err := s.LatestMetrics(nil); err != nil || len(m) != 0 {
		t.Fatalf("空列表应返回空 map: n=%d err=%v", len(m), err)
	}
}
