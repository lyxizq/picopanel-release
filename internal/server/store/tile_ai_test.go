package store

import (
	"testing"
)

// TestTileAISourceRoundTrip 用量格子数据源（486）：dashboard_metrics 与
// page_metrics 的 ai_id 往返；删用量账户级联清理两表引用行（口径同删服务器）。
func TestTileAISourceRoundTrip(t *testing.T) {
	s := openScriptTestStore(t)

	id, err := s.CreateAiAccount(&AiAccount{Name: "智谱主账号", Provider: "zhipu", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	dm, err := s.CreateDashMetric(0, id, "ai_zhipu_week", "", "智谱周额度")
	if err != nil {
		t.Fatal(err)
	}
	pm, err := s.CreatePageMetric(0, id, "ai_zhipu_week", "", "智谱周额度", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if pm.AIID != id || pm.ServerID != 0 {
		t.Fatalf("主页用量格子创建值不对: %+v", pm)
	}
	// 编辑互转：用量格子改回服务器格子（ai_id 归 0）再改回
	if err := s.UpdateDashMetric(dm.ID, 7, 0, "cpu", "", "官网 CPU"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListDashMetrics()
	if err != nil || len(list) != 1 || list[0].AIID != 0 || list[0].ServerID != 7 || list[0].Metric != "cpu" {
		t.Fatalf("改回服务器格子往返不一致: %+v %v", list, err)
	}
	if err := s.UpdateDashMetric(dm.ID, 0, id, "ai_zhipu_5h", "", "智谱5h"); err != nil {
		t.Fatal(err)
	}
	plist, err := s.ListPageMetrics()
	if err != nil || len(plist) != 1 || plist[0].AIID != id || plist[0].ServerID != 0 || plist[0].Metric != "ai_zhipu_week" {
		t.Fatalf("主页用量格子往返不一致: %+v %v", plist, err)
	}

	// 删账户：两表 ai_id 引用行连带清理
	if err := s.DeleteAiAccount(id); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.ListDashMetrics(); len(list) != 0 {
		t.Fatalf("删账户后总览用量格子应级联清理: %+v", list)
	}
	if plist, _ = s.ListPageMetrics(); len(plist) != 0 {
		t.Fatalf("删账户后主页用量格子应级联清理: %+v", plist)
	}
	// 再删同 id 报不存在
	if err := s.DeleteAiAccount(id); err != ErrAiAccountNotFound {
		t.Fatalf("删不存在账户应报 ErrAiAccountNotFound: %v", err)
	}
}
