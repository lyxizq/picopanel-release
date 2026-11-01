package collect

import (
	"bufio"
	"io"
	"strings"

	"lz-monitor/internal/proto"
)

// countEstablished 统计 /proc/net/tcp{,6} 中状态为 01（ESTABLISHED）的行。
func countEstablished(r io.Reader) int {
	n := 0
	sc := bufio.NewScanner(r)
	sc.Scan() // 跳过表头
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 4 && fields[3] == "01" {
			n++
		}
	}
	return n
}

// TCPCollector 采集 TCP ESTABLISHED 连接数。文件缺失（如禁用 IPv6）不计。
type TCPCollector struct{}

// NewTCPCollector 返回 TCP 连接数采集器。
func NewTCPCollector() *TCPCollector { return &TCPCollector{} }

// Name 实现 Collector。
func (c *TCPCollector) Name() string { return "tcp" }

// Collect 实现 Collector。
func (c *TCPCollector) Collect(rep *proto.Report) error {
	total := 0
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := open(path)
		if err != nil {
			continue
		}
		total += countEstablished(f)
		f.Close()
	}
	rep.TCPEst = total
	return nil
}
