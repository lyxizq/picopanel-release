package store

import (
	"testing"
)

// TestDashChartCRUD 图表配置增删查 + 删除服务器时级联清理。
func TestDashChartCRUD(t *testing.T) {
	s := openTestStore(t)

	srv, err := s.CreateServer("web-1", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.CreateDashChart(srv.ID, "net", "eth0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDashChart(srv.ID, "cpu", ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountDashCharts(); n != 2 {
		t.Fatalf("应有 2 个图表, got %d", n)
	}
	list, err := s.ListDashCharts()
	if err != nil || len(list) != 2 {
		t.Fatalf("列表应有 2 条: %v %v", list, err)
	}

	// 删除单个图表
	if err := s.DeleteDashChart(ch.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountDashCharts(); n != 1 {
		t.Fatalf("删除后应剩 1 个, got %d", n)
	}

	// 删除服务器：其图表一并清理
	if err := s.DeleteServer(srv.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountDashCharts(); n != 0 {
		t.Fatalf("删除服务器后图表应级联清理, 剩 %d", n)
	}
}
