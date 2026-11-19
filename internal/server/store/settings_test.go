package store

import "testing"

// TestSettings 参数设置 key-value：写入/读取/覆盖；保留天数缺省 90、非法值回退 90。
func TestSettings(t *testing.T) {
	s := openTestStore(t)

	// 未写入时读取：不存在
	if v, ok, err := s.GetSetting("foo"); err != nil || ok || v != "" {
		t.Fatalf("未写入的 key 应返回不存在: v=%q ok=%v err=%v", v, ok, err)
	}
	// 写入与覆盖
	if err := s.SetSetting("foo", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("foo", "2"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.GetSetting("foo"); err != nil || !ok || v != "2" {
		t.Fatalf("覆盖后应为 2: v=%q ok=%v err=%v", v, ok, err)
	}

	// 保留天数：缺省 90 → 改 30 生效 → 直写非法值回退 90
	if d, err := s.RetentionDays(); err != nil || d != 90 {
		t.Fatalf("缺省保留天数应 90: d=%d err=%v", d, err)
	}
	if err := s.SetRetentionDays(30); err != nil {
		t.Fatal(err)
	}
	if d, err := s.RetentionDays(); err != nil || d != 30 {
		t.Fatalf("保留天数应 30: d=%d err=%v", d, err)
	}
	if _, err := s.DB().Exec(`INSERT INTO settings (key, value) VALUES ('retention_days', 'abc')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatal(err)
	}
	if d, err := s.RetentionDays(); err != nil || d != 90 {
		t.Fatalf("非法值应回退 90: d=%d err=%v", d, err)
	}
}

// TestDashMetrics 重点指标配置 CRUD 与数量统计。
func TestDashMetrics(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateDashMetric(a.ID, 0, "cpu", "", "主页 CPU")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDashMetric(a.ID, 0, "net_rx", "eth0", "出口下行"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListDashMetrics()
	if err != nil || len(list) != 2 {
		t.Fatalf("应有 2 个重点指标: n=%d err=%v", len(list), err)
	}
	if list[0].ID != m.ID || list[0].Label != "主页 CPU" || list[1].Param != "eth0" {
		t.Fatalf("字段错误: %+v", list)
	}
	if n, err := s.CountDashMetrics(); err != nil || n != 2 {
		t.Fatalf("计数应 2: n=%d err=%v", n, err)
	}
	if err := s.DeleteDashMetric(m.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountDashMetrics(); err != nil || n != 1 {
		t.Fatalf("删除后计数应 1: n=%d err=%v", n, err)
	}
	// 删除服务器级联清理
	if err := s.DeleteServer(a.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountDashMetrics(); err != nil || n != 0 {
		t.Fatalf("删除服务器后应清空: n=%d err=%v", n, err)
	}
}

// 拖拽排序：Reorder 写入 sort 后 List 按新顺序返回，图表同。
func TestDashReorder(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	m1, err := s.CreateDashMetric(a.ID, 0, "cpu", "", "一")
	if err != nil {
		t.Fatal(err)
	}
	m2, err := s.CreateDashMetric(a.ID, 0, "mem", "", "二")
	if err != nil {
		t.Fatal(err)
	}
	m3, err := s.CreateDashMetric(a.ID, 0, "load1", "", "三")
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.ListDashMetrics()
	if err != nil || len(l) != 3 || l[0].ID != m1.ID || l[2].ID != m3.ID {
		t.Fatalf("未排序前应按创建序: %+v err=%v", l, err)
	}
	// 重排为 3,1,2
	if err := s.ReorderDashMetrics([]int64{m3.ID, m1.ID, m2.ID}); err != nil {
		t.Fatal(err)
	}
	l, err = s.ListDashMetrics()
	if err != nil || len(l) != 3 || l[0].ID != m3.ID || l[1].ID != m1.ID || l[2].ID != m2.ID {
		t.Fatalf("重排后应为 3,1,2: %+v err=%v", l, err)
	}
	// 图表同款重排（单元素排序无变化，验证接口可用）
	c1, err := s.CreateDashChart(a.ID, "cpu", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReorderDashCharts([]int64{c1.ID}); err != nil {
		t.Fatal(err)
	}
	cl, err := s.ListDashCharts()
	if err != nil || len(cl) != 1 || cl[0].ID != c1.ID {
		t.Fatalf("图表重排后应保留: %+v err=%v", cl, err)
	}
}
