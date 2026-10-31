package store

import (
	"reflect"
	"testing"
	"time"

	"lz-monitor/internal/proto"
)

// 外网流量账本差分口径：首报只建基线；正常递增取差；计数器回退按归零处理
// （当前值全算新增）；超出可信上限的差值丢弃且重建基线；Nets 为 nil 的上报
// 不动基线；统计网口之外的网口（docker0）不入账。
func TestNetTrafficLedger(t *testing.T) {
	s := openTestStore(t)
	srv, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	t0 := base.Unix()
	day := base.Format("2006-01-02")

	report := func(ts int64, rx, tx uint64) {
		t.Helper()
		r := &proto.Report{
			TS: ts,
			Nets: []proto.NetInfo{
				{Iface: "docker0", RxBytes: 99999999, TxBytes: 99999999},
				{Iface: "eth0", RxBytes: rx, TxBytes: tx},
			},
			WanIfaces: []string{"eth0"},
		}
		if err := s.InsertMetric(srv.ID, ts, r); err != nil {
			t.Fatal(err)
		}
	}

	report(t0, 1000, 500) // 首报：只建基线
	sum, err := s.NetTrafficSummary(srv.ID, base, 30)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Since != "" || len(sum.Days) != 0 || !reflect.DeepEqual(sum.Ifaces, []string{"eth0"}) {
		t.Fatalf("首报后应只有基线无账目: %+v", sum)
	}

	report(t0+10, 1600, 700) // +600 / +200
	report(t0+20, 50, 20)    // 归零：当前值全算新增 +50 / +20
	huge := uint64(200e9)    // 10 秒内 200 GB 超出 100 Gbps 上限 → 丢弃、重建基线
	report(t0+30, 50+huge, 20)
	report(t0+40, 50+huge+100, 20) // 基线已重建，正常 +100 / +0

	// Nets 为 nil（采集失败）：不动基线、不入账
	if err := s.InsertMetric(srv.ID, t0+50, &proto.Report{TS: t0 + 50}); err != nil {
		t.Fatal(err)
	}

	sum, err = s.NetTrafficSummary(srv.ID, base.Add(50*time.Second), 30)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Since != day {
		t.Fatalf("since = %q, 期望 %q", sum.Since, day)
	}
	if sum.MonthRx != 750 || sum.MonthTx != 220 {
		t.Fatalf("本月合计 rx=%d tx=%d, 期望 750/220", sum.MonthRx, sum.MonthTx)
	}
	if len(sum.Days) != 1 || sum.Days[0] != (NetTrafficDay{Day: day, Rx: 750, Tx: 220}) {
		t.Fatalf("逐日 = %+v", sum.Days)
	}
	if len(sum.Months) != 1 || sum.Months[0] != (NetTrafficMonth{Month: base.Format("2006-01"), Rx: 750, Tx: 220}) {
		t.Fatalf("逐月 = %+v", sum.Months)
	}

	// 删服务器级联清账本与基线
	if err := s.DeleteServer(srv.ID); err != nil {
		t.Fatal(err)
	}
	sum, err = s.NetTrafficSummary(srv.ID, base, 30)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Since != "" || len(sum.Ifaces) != 0 {
		t.Fatalf("删服务器后账本应清空: %+v", sum)
	}
}

// 统计网口三级口径：展示网口配置 > agent 默认路由出口 > 全部剔虚拟口；前两级
// 显式口径不在场时返回空而不下落。
func TestSelectTrafficIfaces(t *testing.T) {
	nets := []proto.NetInfo{
		{Iface: "bond0"}, {Iface: "br-1a2b"}, {Iface: "docker0"}, {Iface: "eth0"}, {Iface: "eth1"},
		{Iface: "tailscale0"}, {Iface: "veth9f"}, {Iface: "vmbr0"}, {Iface: "wg0"},
	}
	cases := []struct {
		cfg  string
		wan  []string
		want []string
	}{
		{"eth1", []string{"eth0"}, []string{"eth1"}},         // 配置优先于 agent 出口
		{" eth1 , eth0,eth1", nil, []string{"eth1", "eth0"}}, // 配置顺序、去空白、去重
		{"eth9", []string{"eth0"}, nil},                      // 配置了但不在场：不下落
		{"", []string{"eth0", "vmbr0"}, []string{"eth0", "vmbr0"}},
		{"", []string{"ppp0"}, nil},                           // 出口不在场：不下落
		{"", nil, []string{"bond0", "eth0", "eth1", "vmbr0"}}, // 第 3 级：虚拟口剔除、排序
	}
	for _, c := range cases {
		got := selectTrafficIfaces(c.cfg, c.wan, nets)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("cfg=%q wan=%v: got %v, want %v", c.cfg, c.wan, got, c.want)
		}
	}
}

// 服务器配置了展示网口时账本只算所列，即使 agent 报了别的出口。
func TestNetTrafficConfigOverridesWan(t *testing.T) {
	s := openTestStore(t)
	srv, err := s.CreateServer("B", "10.0.0.2:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	eth1 := "eth1"
	if err := s.UpdateServer(srv.ID, ServerUpdate{NetIfaces: &eth1}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	for i, v := range []uint64{1000, 1300} {
		r := &proto.Report{
			Nets:      []proto.NetInfo{{Iface: "eth0", RxBytes: v * 10, TxBytes: v * 10}, {Iface: "eth1", RxBytes: v, TxBytes: v}},
			WanIfaces: []string{"eth0"},
		}
		if err := s.InsertMetric(srv.ID, base.Unix()+int64(i*10), r); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := s.NetTrafficSummary(srv.ID, base, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sum.Ifaces, []string{"eth1"}) || sum.MonthRx != 300 || sum.MonthTx != 300 {
		t.Fatalf("应只算 eth1 的 +300: %+v", sum)
	}
}

// 汇总窗口：逐日只取近 N 天、逐月只取当年、本月合计取当前自然月、多网口求和。
func TestNetTrafficSummaryWindows(t *testing.T) {
	s := openTestStore(t)
	srv, err := s.CreateServer("C", "10.0.0.3:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		day, iface string
		rx, tx     int64
	}{
		{"2025-12-31", "eth0", 1, 1}, // 去年：逐月不含、逐日窗口外
		{"2026-09-10", "eth0", 10, 10},
		{"2026-09-30", "eth0", 100, 100},
		{"2026-10-01", "eth0", 1000, 1000},
		{"2026-10-15", "eth0", 5000, 2000},
		{"2026-10-15", "eth1", 7000, 3000}, // 同日两网口求和
	}
	for _, r := range rows {
		if _, err := s.db.Exec(`INSERT INTO net_traffic_days (server_id, day, iface, rx_bytes, tx_bytes) VALUES (?,?,?,?,?)`,
			srv.ID, r.day, r.iface, r.rx, r.tx); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 15, 20, 0, 0, 0, time.Local)
	sum, err := s.NetTrafficSummary(srv.ID, now, 30) // 窗口 09-16 ~ 10-15
	if err != nil {
		t.Fatal(err)
	}
	if sum.Since != "2025-12-31" {
		t.Fatalf("since = %q", sum.Since)
	}
	wantDays := []NetTrafficDay{{"2026-09-30", 100, 100}, {"2026-10-01", 1000, 1000}, {"2026-10-15", 12000, 5000}}
	if !reflect.DeepEqual(sum.Days, wantDays) {
		t.Fatalf("逐日 = %+v", sum.Days)
	}
	wantMonths := []NetTrafficMonth{{"2026-09", 110, 110}, {"2026-10", 13000, 6000}}
	if !reflect.DeepEqual(sum.Months, wantMonths) {
		t.Fatalf("逐月 = %+v", sum.Months)
	}
	if sum.MonthRx != 13000 || sum.MonthTx != 6000 {
		t.Fatalf("本月 rx=%d tx=%d", sum.MonthRx, sum.MonthTx)
	}
}
