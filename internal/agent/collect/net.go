package collect

import (
	"io"
	"sort"
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
	return nil
}
