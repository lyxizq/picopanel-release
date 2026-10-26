package collect

import (
	"strings"
	"testing"
	"time"
)

const netDevFixture = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 123456 1000    0    0    0     0          0         0  123456   1000    0    0    0     0       0          0
  eth0: 1000000 2000    0    0    0     0          0         0  500000   1500    0    0    0     0       0          0
`

func TestParseNetDev(t *testing.T) {
	m := parseNetDev(strings.NewReader(netDevFixture))
	if _, ok := m["lo"]; ok {
		t.Fatal("lo 应被跳过")
	}
	c := m["eth0"]
	if c.rx != 1000000 || c.tx != 500000 {
		t.Fatalf("eth0 = %+v", c)
	}
}

func TestNetRate(t *testing.T) {
	col := NewNetCollector()
	// 第一次：仅基线
	r1 := parseNetDev(strings.NewReader(netDevFixture))
	now := time.Now()
	for iface, cnt := range r1 {
		col.prev[iface] = netSample{counter: cnt, at: now.Add(-10 * time.Second)}
	}
	// 第二次：10s 后 rx +100000 字节 → 10000 B/s
	r2 := parseNetDev(strings.NewReader(
		"  eth0: 1100000 2100    0    0    0     0          0         0  510000   1600    0    0    0     0       0          0\n"))
	for iface, cnt := range r2 {
		p := col.prev[iface]
		dt := 10.0
		rxbps := float64(cnt.rx-p.counter.rx) / dt
		if iface == "eth0" && rxbps != 10000 {
			t.Fatalf("rx 速率 = %v, 期望 10000", rxbps)
		}
	}
}

func TestNetCounterWrap(t *testing.T) {
	// 计数器回绕（新值 < 旧值）：速率应为 0
	old := netSample{counter: netCounter{rx: 900, tx: 900}, at: time.Now().Add(-5 * time.Second)}
	cur := netCounter{rx: 100, tx: 100}
	dt := 5.0
	rx := 0.0
	if dt > 0 && cur.rx >= old.counter.rx {
		rx = float64(cur.rx-old.counter.rx) / dt
	}
	if rx != 0 {
		t.Fatalf("回绕时速率应为 0, got %v", rx)
	}
}

// 默认路由出口解析：IPv4 只认目的/掩码全零且 UP 的行（非默认路由、拒绝路由、
// 表头都跳过），IPv6 只认 ::/0 且 UP 非 REJECT 的行（内核常给 lo 挂一条
// unreachable default，必须滤掉）。
func TestParseDefaultRoutes(t *testing.T) {
	route4 := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0101A8C0	0003	0	0	100	00000000	0	0	0
eth0	0001A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
docker0	000011AC	00000000	0001	0	0	0	0000FFFF	0	0	0
wg0	00000000	00000000	0201	0	0	0	00000000	0	0	0
`
	got := parseDefaultRoutes4(strings.NewReader(route4))
	if len(got) != 1 || got[0] != "eth0" {
		t.Fatalf("IPv4 默认路由出口 = %v, 期望 [eth0]", got)
	}

	route6 := `fe800000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001 eth0
00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000400 00000002 00000000 00000003 eth1
00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200 lo
`
	got = parseDefaultRoutes6(strings.NewReader(route6))
	if len(got) != 1 || got[0] != "eth1" {
		t.Fatalf("IPv6 默认路由出口 = %v, 期望 [eth1]", got)
	}
}
