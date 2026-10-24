package collect

import (
	"errors"
	"io"
	"strings"

	"lz-monitor/internal/proto"
)

// cpuTimes 是 /proc/stat 首行聚合计数（单位：jiffies，通常 10ms）。
type cpuTimes struct {
	busy  uint64 // user+nice+system+irq+softirq+steal
	total uint64 // busy+idle+iowait
}

// parseCPUStat 从 /proc/stat 内容中找 "cpu " 汇总行并解析
// （解析逻辑在 proto.ParseCPUStatLine，与 SSH 采集共享）。
func parseCPUStat(r io.Reader) (cpuTimes, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return cpuTimes{}, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		busy, total, err := proto.ParseCPUStatLine(line)
		if err != nil {
			return cpuTimes{}, err
		}
		return cpuTimes{busy: busy, total: total}, nil
	}
	return cpuTimes{}, errors.New("/proc/stat 中未找到 cpu 汇总行")
}

// cpuPercent 由两次采样差值计算使用率（0~100）。
func cpuPercent(prev, cur cpuTimes) float64 {
	dt := cur.total - prev.total
	if dt == 0 {
		return 0
	}
	p := float64(cur.busy-prev.busy) / float64(dt) * 100
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p
}

// CPUCollector 通过两次采样 /proc/stat 差值计算 CPU 使用率。
// 首次调用只建立基线（上报 0）。
type CPUCollector struct {
	prev *cpuTimes
}

// NewCPUCollector 返回 CPU 采集器。
func NewCPUCollector() *CPUCollector { return &CPUCollector{} }

// Name 实现 Collector。
func (c *CPUCollector) Name() string { return "cpu" }

// Collect 实现 Collector。
func (c *CPUCollector) Collect(rep *proto.Report) error {
	f, err := open("/proc/stat")
	if err != nil {
		return err
	}
	defer f.Close()
	cur, err := parseCPUStat(f)
	if err != nil {
		return err
	}
	prev := c.prev
	c.prev = &cur
	if prev == nil {
		return nil // 首次仅建立基线
	}
	rep.CPU = cpuPercent(*prev, cur)
	return nil
}
