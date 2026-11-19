package store

import (
	"testing"

	"lz-monitor/internal/proto"
)

// TestDisksLatestOnly 磁盘占用仅存最新：metrics 行的 disks 列不再保留历史（NULL），
// 当前状态单行 upsert 到 server_disks，LatestMetric 读取时合并最新一份。
func TestDisksLatestOnly(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	d1 := []proto.DiskInfo{{Mount: "/", FSType: "ext4", Total: 100, Used: 50, Percent: 50}}
	d2 := []proto.DiskInfo{{Mount: "/", FSType: "ext4", Total: 100, Used: 90, Percent: 90}}
	rep := func(ts int64, disks []proto.DiskInfo) *proto.Report {
		return &proto.Report{TS: ts, CPU: 1, Disks: disks}
	}

	if err := s.InsertMetric(a.ID, 100, rep(100, d1)); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMetric(a.ID, 110, rep(110, d2)); err != nil {
		t.Fatal(err)
	}

	// v2 起 metrics 是纯标量表，disks 列已不存在
	if hasMetricColumn(s.DB(), "disks") {
		t.Fatal("metrics 不应再有 disks 列")
	}

	// LatestMetric 合到最新状态（第二份，90%）
	m, err := s.LatestMetric(a.ID)
	if err != nil || m == nil {
		t.Fatalf("LatestMetric: %v %v", m, err)
	}
	if len(m.Disks) != 1 || m.Disks[0].Percent != 90 {
		t.Fatalf("最新磁盘状态应为 d2: %+v", m.Disks)
	}

	// server_disks 单行（两次上报只留最新）
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM server_disks WHERE server_id = ?`, a.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("server_disks 应单行: n=%d err=%v", n, err)
	}

	// 删除服务器连带清理
	if err := s.DeleteServer(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM server_disks`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("删除服务器后 server_disks 应清空: n=%d err=%v", n, err)
	}
}
