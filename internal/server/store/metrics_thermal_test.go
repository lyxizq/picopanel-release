package store

import (
	"testing"

	"lz-monitor/internal/proto"
)

// TestThermalRoundTrip 温度/风扇落 metrics_thermal：最新视图、历史采样、
// 归档桶均值、窗口清理、删除服务器连带清理全链路。
func TestThermalRoundTrip(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	temps := []proto.TempPoint{{Sensor: "CPU", Temp: 45.5}, {Sensor: "sda", Temp: 33}}
	fans := []proto.FanPoint{{Sensor: "nct6798", RPM: 1200}}

	// 无传感器数据（旧版 agent）不写行
	if err := s.InsertMetric(a.ID, 100, &proto.Report{TS: 100, CPU: 1}); err != nil {
		t.Fatal(err)
	}
	m, err := s.LatestMetric(a.ID)
	if err != nil || m == nil {
		t.Fatalf("LatestMetric: %v %v", m, err)
	}
	if m.Temps != nil || m.Fans != nil {
		t.Fatalf("旧版 agent 上报不应有温度: %+v %+v", m.Temps, m.Fans)
	}

	// 带温度的两份采样
	rep := &proto.Report{TS: 200, CPU: 2, Temps: temps, Fans: fans}
	if err := s.InsertMetric(a.ID, 200, rep); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMetric(a.ID, 250, &proto.Report{TS: 250, CPU: 3,
		Temps: []proto.TempPoint{{Sensor: "CPU", Temp: 51}}, Fans: fans}); err != nil {
		t.Fatal(err)
	}
	m, err = s.LatestMetric(a.ID)
	if err != nil || m == nil {
		t.Fatalf("LatestMetric: %v %v", m, err)
	}
	if len(m.Temps) != 1 || m.Temps[0].Sensor != "CPU" || m.Temps[0].Temp != 51 {
		t.Fatalf("最新温度应为 51: %+v", m.Temps)
	}
	if len(m.Fans) != 1 || m.Fans[0].RPM != 1200 {
		t.Fatalf("风扇: %+v", m.Fans)
	}

	// LatestMetrics 批量视图（attachAux 组装）
	latest, err := s.LatestMetrics([]int64{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if lm := latest[a.ID]; lm == nil || len(lm.Temps) != 1 || lm.Temps[0].Temp != 51 {
		t.Fatalf("LatestMetrics 温度: %+v", lm)
	}

	// SampledRows 历史采样（明细段 JOIN thermal）
	rows, err := s.SampledRows(a.ID, 100, 250, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("应 3 个采样行, got %d", len(rows))
	}
	if len(rows[0].Temps) != 0 {
		t.Fatalf("首行无温度: %+v", rows[0].Temps)
	}
	if len(rows[1].Temps) != 2 || rows[1].Temps[1].Sensor != "sda" || rows[1].Temps[1].Temp != 33 {
		t.Fatalf("第二行温度: %+v", rows[1].Temps)
	}

	// 归档：250 落入桶 0（200 与 250 同桶，均值 (51+45.5)/2=48.25），窗口内明细删除
	if _, err := s.ArchiveBefore(1000); err != nil {
		t.Fatal(err)
	}
	rows, err = s.SampledRows(a.ID, 0, 1000, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0].Temps) != 2 {
		t.Fatalf("归档后应剩 1 桶 2 路温度: %+v", rows)
	}
	if got := rows[0].Temps[0].Temp; got < 48.24 || got > 48.26 { // (51+45.5)/2
		t.Fatalf("CPU 桶均值应 48.25: %v", got)
	}
	if rows[0].Temps[1].Sensor != "sda" || rows[0].Temps[1].Temp != 33 {
		t.Fatalf("sda 桶均值: %+v", rows[0].Temps[1])
	}
	if rows[0].Fans[0].RPM != 1200 {
		t.Fatalf("归档风扇: %+v", rows[0].Fans)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM metrics_thermal`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("归档窗口后子表应清空: n=%d err=%v", n, err)
	}

	// 删除服务器连带清理（先补一行再删）
	if err := s.InsertMetric(a.ID, 2000, rep); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteServer(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM metrics_thermal`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("删除服务器后 metrics_thermal 应清空: n=%d err=%v", n, err)
	}
}
