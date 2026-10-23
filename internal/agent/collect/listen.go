package collect

import (
	"bufio"
	"io"
	"strings"

	"lz-monitor/internal/proto"
)

// maxListens 监听端口上报上限（正常机器几十个以内，截断防异常刷爆存储）。
const maxListens = 256

// ListenCollector 采集监听端口：TCP 取 /proc/net/tcp{,6} 中 st=0A（LISTEN）
// 的行，UDP 取未连接（remote 为 0.0.0.0:0，即已绑定端口）的行。
// 同协议同端口的多个地址由 proto.BuildListenList 合并（环回地址不采集）。
// 文件缺失（如禁用 IPv6）不计。
type ListenCollector struct{}

// NewListenCollector 返回监听端口采集器。
func NewListenCollector() *ListenCollector { return &ListenCollector{} }

// Name 实现 Collector。
func (c *ListenCollector) Name() string { return "listen" }

// Collect 实现 Collector。
func (c *ListenCollector) Collect(rep *proto.Report) error {
	var entries []proto.ListenEntry
	for _, p := range []struct {
		path string
		name string
		v6   bool
		tcp  bool
	}{
		{"/proc/net/tcp", "tcp", false, true},
		{"/proc/net/tcp6", "tcp", true, true},
		{"/proc/net/udp", "udp", false, false},
		{"/proc/net/udp6", "udp", true, false},
	} {
		if f, err := open(p.path); err == nil {
			entries = append(entries, parseListenRows(f, p.name, p.v6, p.tcp)...)
			f.Close()
		}
	}
	rep.Listens = proto.BuildListenList(entries, maxListens)
	return nil
}

// parseListenRows 解析 /proc/net/{tcp,tcp6,udp,udp6} 输出为原始条目。
// tcp=true 只取 LISTEN（st=0A）行；否则按 UDP 语义取未连接（remote 全零，
// 即已绑定端口）的行。
func parseListenRows(r io.Reader, protoName string, v6, tcp bool) []proto.ListenEntry {
	var entries []proto.ListenEntry
	sc := bufio.NewScanner(r)
	sc.Scan() // 跳过表头
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		if tcp {
			if f[3] != "0A" { // LISTEN
				continue
			}
		} else if f[2] != "00000000:0000" { // UDP：未连接=已绑定
			continue
		}
		if addr, port, ok := proto.ParseHexSockAddr(f[1], v6); ok {
			entries = append(entries, proto.ListenEntry{Proto: protoName, Addr: addr, Port: port})
		}
	}
	return entries
}
