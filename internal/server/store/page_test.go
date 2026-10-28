package store

import (
	"testing"
)

// TestPageSettingsDefaults 首次使用（无配置行）返回默认值；保存后读回一致（含天气字段）。
func TestPageSettingsDefaults(t *testing.T) {
	s := openScriptTestStore(t)

	cfg, err := s.GetPageSettings()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Title != "" || !cfg.ShowSearch || cfg.LinkMode != "vertical" || !cfg.ShowAvatar || cfg.RefreshSec != 30 ||
		cfg.WeatherEnabled || cfg.WeatherMode != "auto" || cfg.WeatherCity != "" || cfg.WeatherLat != 0 || cfg.WeatherLon != 0 {
		t.Fatalf("默认配置应为空标题+显示搜索框+竖向+显示头像+30s 刷新+天气关: %+v", cfg)
	}

	if err := s.SavePageSettings(&PageSettings{Title: "我的面板", ShowSearch: false, ShowAvatar: false,
		LinkMode: "horizontal", RefreshSec: 0}); err != nil {
		t.Fatal(err)
	}
	cfg, err = s.GetPageSettings()
	if err != nil || cfg.Title != "我的面板" || cfg.ShowSearch || cfg.LinkMode != "horizontal" || cfg.ShowAvatar || cfg.RefreshSec != 0 {
		t.Fatalf("往返不一致: %+v %v", cfg, err)
	}
	// 再存一次走 upsert 更新分支（含手动城市天气配置）
	if err := s.SavePageSettings(&PageSettings{Title: "观澜", ShowSearch: true, ShowAvatar: true,
		LinkMode: "", RefreshSec: 60, WeatherEnabled: true, WeatherMode: "manual",
		WeatherCity: "北京", WeatherLat: 39.9042, WeatherLon: 116.4074}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = s.GetPageSettings()
	if cfg.Title != "观澜" || !cfg.ShowSearch || cfg.LinkMode != "vertical" || !cfg.ShowAvatar || cfg.RefreshSec != 60 ||
		!cfg.WeatherEnabled || cfg.WeatherMode != "manual" || cfg.WeatherCity != "北京" || cfg.WeatherLat != 39.9042 || cfg.WeatherLon != 116.4074 {
		t.Fatalf("二次保存未覆盖: %+v", cfg)
	}
	// manual 模式存库后兜底：非法 mode 归 auto
	if err := s.SavePageSettings(&PageSettings{WeatherMode: "bogus"}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = s.GetPageSettings(); cfg.WeatherMode != "auto" {
		t.Fatalf("非法天气模式应兜底 auto: %+v", cfg)
	}
}

// TestPageMetricsCRUD 主页监控模块：建/列/改/删/排序。
func TestPageMetricsCRUD(t *testing.T) {
	s := openScriptTestStore(t)

	m1, err := s.CreatePageMetric(1, 0, "cpu", "", "CPU", "https://example.com", "https://example.com/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	m2, err := s.CreatePageMetric(2, 0, "net", "eth0", "网卡", "", "")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListPageMetrics()
	if err != nil || len(list) != 2 || list[0].Link != "https://example.com" || list[0].Icon != "https://example.com/favicon.ico" {
		t.Fatalf("列表不一致: %v %v", list, err)
	}
	if err := s.UpdatePageMetric(m1.ID, 1, 0, "mem", "", "内存", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ReorderPageMetrics([]int64{m2.ID, m1.ID}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListPageMetrics()
	if list[0].ID != m2.ID || list[1].Label != "内存" {
		t.Fatalf("改排未生效: %v", list)
	}
	if n, _ := s.CountPageMetrics(); n != 2 {
		t.Fatalf("计数应 2: %d", n)
	}
	if err := s.DeletePageMetric(m2.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.ListPageMetrics(); len(list) != 1 {
		t.Fatalf("删除失败: %v", list)
	}
}

// TestPageCreateAppendsToTail 新增项排序到末尾：拖拽排序后存量 sort≥1，
// 新建 链接（组内）/分组/磁贴 须排最后，不得凭默认 sort=0 插队最前（用户 2026-10-03 指定）。
func TestPageCreateAppendsToTail(t *testing.T) {
	s := openScriptTestStore(t)

	g1, err := s.CreatePageGroup("常用")
	if err != nil {
		t.Fatal(err)
	}
	g2, err := s.CreatePageGroup("开发")
	if err != nil {
		t.Fatal(err)
	}
	l1, err := s.CreatePageLink(g1.ID, "GitHub", "https://github.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreatePageLink(g1.ID, "百度", "https://baidu.com", ""); err != nil {
		t.Fatal(err)
	}
	m1, err := s.CreatePageMetric(1, 0, "cpu", "", "CPU", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreatePageMetric(2, 0, "net", "eth0", "网卡", "", ""); err != nil {
		t.Fatal(err)
	}

	// 拖拽排序：链接 百度→GitHub、分组 开发→常用、磁贴 网卡→CPU（sort 写 1..n）
	if err := s.ReorderPageLinks([]int64{l1.ID + 1, l1.ID}, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ReorderPageGroups([]int64{g2.ID, g1.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReorderPageMetrics([]int64{m1.ID + 1, m1.ID}); err != nil {
		t.Fatal(err)
	}

	// 新增三项：都应追加到末尾
	if _, err = s.CreatePageLink(g1.ID, "Grafana", "https://grafana.example.com", ""); err != nil {
		t.Fatal(err)
	}
	g3, err := s.CreatePageGroup("娱乐")
	if err != nil {
		t.Fatal(err)
	}
	if g3.ID != g2.ID+1 {
		t.Fatalf("新组 id 异常: %d", g3.ID)
	}
	if _, err = s.CreatePageMetric(3, 0, "mem", "", "内存", "", ""); err != nil {
		t.Fatal(err)
	}

	links, err := s.ListPageLinks()
	if err != nil || len(links) != 3 {
		t.Fatalf("应有 3 条链接: %v %v", links, err)
	}
	if links[0].Name != "百度" || links[1].Name != "GitHub" || links[2].Name != "Grafana" {
		t.Fatalf("新链接应排组尾: %v", links)
	}
	groups, err := s.ListPageGroups()
	if err != nil || len(groups) != 3 {
		t.Fatalf("应有 3 个分组: %v %v", groups, err)
	}
	if groups[0].ID != g2.ID || groups[1].ID != g1.ID || groups[2].ID != g3.ID {
		t.Fatalf("新分组应排末尾: %v", groups)
	}
	metrics, err := s.ListPageMetrics()
	if err != nil || len(metrics) != 3 {
		t.Fatalf("应有 3 个磁贴: %v %v", metrics, err)
	}
	if metrics[0].Label != "网卡" || metrics[1].Label != "CPU" || metrics[2].Label != "内存" {
		t.Fatalf("新磁贴应排末尾: %v", metrics)
	}
}

// TestPageGroupsLinksRoundTrip 分组与链接全链路：建组→建链→改→排序→删组连带清链。
func TestPageGroupsLinksRoundTrip(t *testing.T) {
	s := openScriptTestStore(t)

	g1, err := s.CreatePageGroup("常用")
	if err != nil {
		t.Fatal(err)
	}
	g2, err := s.CreatePageGroup("开发")
	if err != nil {
		t.Fatal(err)
	}

	l1, err := s.CreatePageLink(g1.ID, "GitHub", "https://github.com", "https://github.com/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePageLink(g1.ID, "百度", "https://baidu.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePageLink(g2.ID, "Grafana", "https://grafana.example.com", ""); err != nil {
		t.Fatal(err)
	}

	links, err := s.ListPageLinks()
	if err != nil || len(links) != 3 {
		t.Fatalf("应有 3 条链接: %v %v", links, err)
	}

	if err := s.UpdatePageLink(l1.ID, "GitHub 加速", "https://gh.example.com", ""); err != nil {
		t.Fatal(err)
	}

	// 分组排序：g2 排到最前
	if err := s.ReorderPageGroups([]int64{g2.ID, g1.ID}); err != nil {
		t.Fatal(err)
	}
	groups, err := s.ListPageGroups()
	if err != nil || len(groups) != 2 || groups[0].ID != g2.ID {
		t.Fatalf("排序未生效: %v %v", groups, err)
	}

	// 链接排序
	if err := s.ReorderPageLinks([]int64{l1.ID}, 0); err != nil {
		t.Fatal(err)
	}

	// 删组连带清组内链接
	if err := s.DeletePageGroup(g1.ID); err != nil {
		t.Fatal(err)
	}
	links, err = s.ListPageLinks()
	if err != nil || len(links) != 1 || links[0].GroupID != g2.ID {
		t.Fatalf("删组未连带清链: %v %v", links, err)
	}

	if err := s.DeletePageLink(links[0].ID); err != nil {
		t.Fatal(err)
	}
	if links, _ = s.ListPageLinks(); len(links) != 0 {
		t.Fatalf("删链接后应为空: %v", links)
	}
}
