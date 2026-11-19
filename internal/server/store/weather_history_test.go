package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestWXHistory 天气采样的落库/查询/归档/清理（522，口径同 TestHAHistory）。
func TestWXHistory(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "wxhist.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	now := time.Now().Unix()

	// 明细查询：step<=1 原样、step>1 分桶（桶沿 300s 对齐、值域不越界）
	frames := []WXSample{
		{Loc: "30.27,120.15", Temp: 23.5, Humidity: 60, Wind: 10, Pressure: 1010, TS: now - 600},
		{Loc: "30.27,120.15", Temp: 24.5, Humidity: 62, Wind: 12, Pressure: 1011, TS: now - 300},
		{Loc: "30.27,120.15", Temp: 25.5, Humidity: 64, Wind: 14, Pressure: 1012, TS: now - 60},
		{Loc: "31.13,121.29", Temp: 30, Humidity: 70, Wind: 8, Pressure: 1008, TS: now - 60}, // 别的位置不串
	}
	for _, f := range frames {
		if err := s.RecordWXSample(f); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	pts, err := s.WXSeries("30.27,120.15", now-3600, now, 1)
	if err != nil {
		t.Fatalf("series raw: %v", err)
	}
	if len(pts) != 3 || pts[0].Temp != 23.5 || pts[2].Pressure != 1012 {
		t.Fatalf("原样点不对: %+v", pts)
	}
	pts, err = s.WXSeries("30.27,120.15", now-3600, now, 300)
	if err != nil {
		t.Fatalf("series bucketed: %v", err)
	}
	if len(pts) < 2 || len(pts) > 3 {
		t.Fatalf("分桶点数不对: %+v", pts)
	}
	for _, p := range pts {
		if p.TS%300 != 0 || p.Temp < 23.5 || p.Temp > 25.5 {
			t.Fatalf("分桶点不对: %+v", pts)
		}
	}

	// 归档：8 天前旧明细降采样进归档并删明细；7 天内明细不动
	//（old 对齐 300s 桶沿：前 6 点同桶均值 24.5、第 7 点同窗第二桶，桶数确定性为 2）
	old := ((now - 8*86400) / 300) * 300
	for i := 0; i < 6; i++ {
		if err := s.RecordWXSample(WXSample{Loc: "30.27,120.15", Temp: float64(24 + i%2), Humidity: 60, Wind: 10, Pressure: 1010, TS: old + int64(i)*30}); err != nil {
			t.Fatalf("record old: %v", err)
		}
	}
	if err := s.RecordWXSample(WXSample{Loc: "30.27,120.15", Temp: 99, Humidity: 10, Wind: 1, Pressure: 900, TS: old + 80000}); err != nil {
		t.Fatalf("record old2: %v", err)
	}
	n, err := s.ArchiveWXSamples(now - 7*86400)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if n != 2 {
		t.Fatalf("归档桶数应为 2: %d", n)
	}
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM wx_samples WHERE ts < ?`, now-7*86400).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("归档后旧明细应删光: %d", cnt)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM wx_samples`).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 4 {
		t.Fatalf("7 天内明细不应被归档: %d", cnt)
	}

	// 跨界序列：归档段（旧）+ 明细段（新）无缝拼接，归档段首点=桶均值 24.5
	pts, err = s.WXSeries("30.27,120.15", old-60, now, 600)
	if err != nil {
		t.Fatalf("series across: %v", err)
	}
	if len(pts) == 0 || pts[0].Temp != 24.5 { // (24+25+24+25+24+25)/6
		t.Fatalf("归档段均值不对: %+v", pts)
	}

	// 清理：归档与残留明细按保留期删除
	if n, _ := s.DeleteWXArchiveBefore(now); n != 2 {
		t.Fatalf("归档清理数不对: %d", n)
	}
	if n, _ := s.DeleteWXSamplesBefore(now); n != 4 {
		t.Fatalf("明细清理数不对: %d", n)
	}
}

// TestWXLatestSample LatestWXSample（56，HA 胶囊读库）：只回指定位置最新一条带
// data 的采样；空 data（升级前旧采样）跳过，他位置不串，无符合行回 nil。
func TestWXLatestSample(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "wxlatest.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	now := time.Now().Unix()
	for _, f := range []WXSample{
		{Loc: "30.27,120.15", Temp: 23.5, Humidity: 60, Wind: 10, Pressure: 1010, TS: now - 600},
		{Loc: "30.27,120.15", Temp: 24.5, Humidity: 62, Wind: 12, Pressure: 1011, TS: now - 120},
		{Loc: "30.27,120.15", Temp: 25.5, Humidity: 64, Wind: 14, Pressure: 1012, TS: now - 60, Data: `{"city":"杭州","current":{"temp":25.5}}`},
		{Loc: "31.13,121.29", Temp: 30, Humidity: 70, Wind: 8, Pressure: 1008, TS: now - 30, Data: `{"city":"上海"}`},
	} {
		if err := s.RecordWXSample(f); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	sm, err := s.LatestWXSample("30.27,120.15")
	if err != nil {
		t.Fatal(err)
	}
	if sm == nil || sm.TS != now-60 || sm.Data != `{"city":"杭州","current":{"temp":25.5}}` {
		t.Fatalf("应回本位置最新带 data 采样: %+v", sm)
	}
	if sm, _ := s.LatestWXSample("99.99,99.99"); sm != nil {
		t.Fatalf("无符合采样应 nil: %+v", sm)
	}
}
