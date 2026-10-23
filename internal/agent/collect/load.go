package collect

import (
	"io"
	"strings"

	"lz-monitor/internal/proto"
)

// parseLoadAvg 解析 /proc/loadavg 首行："0.52 0.58 0.59 1/467 12345"。
func parseLoadAvg(r io.Reader) (l1, l5, l15 float64, err error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, 0, 0, err
	}
	return proto.ParseLoadAvgLine(strings.TrimSpace(string(b)))
}

// LoadCollector 采集系统负载。
type LoadCollector struct{}

// NewLoadCollector 返回负载采集器。
func NewLoadCollector() *LoadCollector { return &LoadCollector{} }

// Name 实现 Collector。
func (c *LoadCollector) Name() string { return "load" }

// Collect 实现 Collector。
func (c *LoadCollector) Collect(rep *proto.Report) error {
	f, err := open("/proc/loadavg")
	if err != nil {
		return err
	}
	defer f.Close()
	rep.Load1, rep.Load5, rep.Load15, err = parseLoadAvg(f)
	return err
}
