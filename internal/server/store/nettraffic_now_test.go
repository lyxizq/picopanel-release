package store

import (
	"testing"
	"time"
)

// TestNetTrafficTodayMonth 当日/当月合计：同日多网口求和、月初以前不计、无账本服务器不在返回里。
func TestNetTrafficTodayMonth(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServer("B", "10.0.0.2:19100", "", 0); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		day, iface string
		rx, tx     int64
	}{
		{"2026-09-30", "eth0", 1000, 1000}, // 上月：不计
		{"2026-10-01", "eth0", 100, 10},
		{"2026-10-14", "eth0", 200, 20},
		{"2026-10-15", "eth0", 300, 30}, // 今天两网口
		{"2026-10-15", "eth1", 400, 40},
	}
	for _, r := range rows {
		if _, err := s.db.Exec(`INSERT INTO net_traffic_days (server_id, day, iface, rx_bytes, tx_bytes) VALUES (?,?,?,?,?)`,
			a.ID, r.day, r.iface, r.rx, r.tx); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	m, err := s.NetTrafficTodayMonth(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 {
		t.Fatalf("只有 A 有账本，得 %d 台", len(m))
	}
	v := m[a.ID]
	if v.TodayRx != 700 || v.TodayTx != 70 || v.MonthRx != 1000 || v.MonthTx != 100 {
		t.Fatalf("合计不符: %+v", v)
	}
}
