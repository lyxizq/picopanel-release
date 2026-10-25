package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestHAHistory 开关事件与数值采样的落库/查询/归档/清理（511/512）。
func TestHAHistory(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "hahist.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	now := time.Now().Unix()

	// 事件落库与新→旧查询
	if err := s.RecordHAFrame([]HAEvent{
		{EntityID: "light.a", Name: "客厅灯", From: "off", To: "on", TS: now - 30},
		{EntityID: "light.a", Name: "客厅灯", From: "on", To: "off", TS: now - 10},
		{EntityID: "binary_sensor.door", Name: "大门", From: "off", To: "on", TS: now - 20},
	}, nil); err != nil {
		t.Fatalf("record events: %v", err)
	}
	evs, err := s.HAEntityEvents("light.a", 10)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(evs) != 2 || evs[0].To != "off" || evs[1].To != "on" {
		t.Fatalf("事件顺序/内容不对: %+v", evs)
	}
	if evs, _ := s.HAEntityEvents("light.none", 10); len(evs) != 0 {
		t.Fatalf("无事件实体应返回空: %+v", evs)
	}

	// 采样明细查询（step>1 走分桶、<=1 走原样，两路都要有数）
	frames := []HASample{
		{EntityID: "sensor.temp", Value: 23.5, TS: now - 600},
		{EntityID: "sensor.temp", Value: 24.5, TS: now - 300},
		{EntityID: "sensor.temp", Value: 25.5, TS: now - 60},
	}
	if err := s.RecordHAFrame(nil, frames); err != nil {
		t.Fatalf("record samples: %v", err)
	}
	pts, err := s.HASeries("sensor.temp", now-3600, now, 1)
	if err != nil {
		t.Fatalf("series raw: %v", err)
	}
	if len(pts) != 3 || pts[0][1] != 23.5 || pts[2][1] != 25.5 {
		t.Fatalf("原样点不对: %v", pts)
	}
	pts, err = s.HASeries("sensor.temp", now-3600, now, 300)
	if err != nil {
		t.Fatalf("series bucketed: %v", err)
	}
	// 桶数随墙钟对齐浮动 2~3 个：断言桶沿 300s 对齐且值都在原数据范围内
	if len(pts) < 2 || len(pts) > 3 {
		t.Fatalf("分桶点数不对: %v", pts)
	}
	for _, p := range pts {
		if int64(p[0])%300 != 0 || p[1] < 23.5 || p[1] > 25.5 {
			t.Fatalf("分桶点不对: %v", pts)
		}
	}

	// 归档：8 天前旧明细降采样进归档并删明细；7 天内明细不动
	//（old 对齐 300s 桶沿：前 6 点落同桶、第 7 点落第二桶，桶数确定性为 2）
	old := ((now - 8*86400) / 300) * 300
	var oldRows []HASample
	for i := 0; i < 6; i++ { // 同一个 5 分钟桶内 6 点 → 归档 1 桶均值 20.5
		oldRows = append(oldRows, HASample{EntityID: "sensor.old", Value: float64(20 + i%2), TS: old + int64(i)*30})
	}
	oldRows = append(oldRows, HASample{EntityID: "sensor.old", Value: 99, TS: old + 80000}) // 同窗第二桶
	if err := s.RecordHAFrame(nil, oldRows); err != nil {
		t.Fatalf("record old: %v", err)
	}
	n, err := s.ArchiveHASamples(now - 7*86400)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if n != 2 {
		t.Fatalf("归档桶数应为 2: %d", n)
	}
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ha_samples WHERE entity_id='sensor.old'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("归档后旧明细应删光: %d", cnt)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ha_samples WHERE entity_id='sensor.temp'`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 3 {
		t.Fatalf("7 天内明细不应被归档: %d", cnt)
	}

	// 跨界序列：归档段（旧）+ 明细段（新）无缝拼接
	pts, err = s.HASeries("sensor.old", old-60, now, 600)
	if err != nil {
		t.Fatalf("series across: %v", err)
	}
	if len(pts) == 0 || pts[0][1] != 20.5 { // (20+21+20+21+20+21)/6
		t.Fatalf("归档段均值不对: %v", pts)
	}

	// 清理：事件与归档按保留期删除
	if n, _ := s.DeleteHAEventsBefore(now); n != 3 {
		t.Fatalf("事件清理数不对: %d", n)
	}
	if n, _ := s.DeleteHAArchiveBefore(now); n != 2 {
		t.Fatalf("归档清理数不对: %d", n)
	}
	if n, _ := s.DeleteHASamplesBefore(now); n != 3 {
		t.Fatalf("明细清理数不对: %d", n)
	}
}
