package collect

import (
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/proto"
)

type netCounter struct {
	rx, tx uint64
}

// parseNetDev 解析 /proc/net/dev，返回 iface → 累计字节数。
// 行格式："  eth0: rx_bytes rx_packets ... tx_bytes ..."（第 1、9 个数值）。
func parseNetDev(r io.Reader) map[string]netCounter {
	m := make(map[string]netCounter)
	b, _ := io.ReadAll(r) // 读错误按部分数据处理（与旧 scanner 行为一致）
	for _, n := range proto.ParseNetDevLines(strings.Split(string(b), "\n")) {
		m[n.Iface] = netCounter{rx: n.RxBytes, tx: n.TxBytes}
	}
	return m
}

type netSample struct {
	counter netCounter
	at      time.Time
}

// NetCollector 通过两次采样 /proc/net/dev 差值计算网卡速率。
type NetCollector struct {
	prev map[string]netSample
}

// NewNetCollector 返回网络采集器。
func NewNetCollector() *NetCollector {
	return &NetCollector{prev: make(map[string]netSample)}
}

// Name 实现 Collector。
func (c *NetCollector) Name() string { return "net" }

// Collect 实现 Collector。计数器回绕/网卡重启导致负差值时速率置 0 并重置基线。
func (c *NetCollector) Collect(rep *proto.Report) error {
	f, err := open("/proc/net/dev")
	if err != nil {
		return err
	}
	defer f.Close()

	cur := parseNetDev(f)
	now := time.Now()
	next := make(map[string]netSample, len(cur))
	for iface, cnt := range cur {
		next[iface] = netSample{counter: cnt, at: now}
		p, ok := c.prev[iface]
		if !ok {
			continue // 新出现的网卡，本次仅建立基线
		}
		dt := now.Sub(p.at).Seconds()
		info := proto.NetInfo{
			Iface:   iface,
			RxBytes: cnt.rx,
			TxBytes: cnt.tx,
		}
		if dt > 0 && cnt.rx >= p.counter.rx && cnt.tx >= p.counter.tx {
			info.RxBps = float64(cnt.rx-p.counter.rx) / dt
			info.TxBps = float64(cnt.tx-p.counter.tx) / dt
		}
		rep.Nets = append(rep.Nets, info)
	}
	c.prev = next
	sort.Slice(rep.Nets, func(i, j int) bool { return rep.Nets[i].Iface < rep.Nets[j].Iface })
	rep.WanIfaces = wanIfaces()
	return nil
}

// wanIfaces 返回持有默认路由的网口（IPv4 与 IPv6 合并去重、排序；无默认路由
// 返回 nil）。服务端外网流量账本以它为缺省统计对象：云主机内网口、Docker 桥、
// veth、VPN 隧道等都不会持有默认路由，天然排除；而 PVE 的 vmbr0、OpenWrt 的
// pppoe-wan 这类"名字像虚拟口的真实出口"也能正确识别——按名字猜做不到这一点。
func wanIfaces() []string {
	set := map[string]bool{}
	if f, err := open("/proc/net/route"); err == nil {
		for _, d := range parseDefaultRoutes4(f) {
			set[d] = true
		}
		f.Close()
	}
	if f, err := open("/proc/net/ipv6_route"); err == nil {
		for _, d := range parseDefaultRoutes6(f) {
			set[d] = true
		}
		f.Close()
	}
	delete(set, "lo")
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// 路由标志位（linux/route.h）：RTF_UP 路由可用；RTF_REJECT 为 "unreachable
// default" 这类拒绝路由，并非真实出口。
const (
	rtfUp     = 0x0001
	rtfReject = 0x0200
)

// parseDefaultRoutes4 解析 /proc/net/route，返回默认路由（目的 0.0.0.0/0）的出口网口。
// 列：Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT（十六进制）。
func parseDefaultRoutes4(r io.Reader) []string {
	var out []string
	b, _ := io.ReadAll(r)
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue // 表头或非默认路由
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&rtfUp == 0 || flags&rtfReject != 0 {
			continue
		}
		out = append(out, f[0])
	}
	return out
}

// parseDefaultRoutes6 解析 /proc/net/ipv6_route，返回默认路由（::/0）的出口网口。
// 列：Dest(32 hex) DestPrefix Src(32) SrcPrefix NextHop(32) Metric RefCnt Use Flags Iface。
func parseDefaultRoutes6(r io.Reader) []string {
	const zero128 = "00000000000000000000000000000000"
	var out []string
	b, _ := io.ReadAll(r)
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[0] != zero128 || f[1] != "00" {
			continue
		}
		flags, err := strconv.ParseUint(f[8], 16, 32)
		if err != nil || flags&rtfUp == 0 || flags&rtfReject != 0 {
			continue
		}
		out = append(out, f[9])
	}
	return out
}
