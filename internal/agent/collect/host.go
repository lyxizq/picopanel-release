package collect

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"

	"lz-monitor/internal/proto"
)

// HostCollector 采集系统静态信息（启动时一次，随每份上报携带）
// 与 uptime（每次上报，兼作心跳）。
type HostCollector struct {
	sys *proto.SysInfo
}

// NewHostCollector 在启动时采集一次静态信息并缓存。
func NewHostCollector() *HostCollector {
	return &HostCollector{sys: readSysInfo()}
}

// Name 实现 Collector。
func (c *HostCollector) Name() string { return "host" }

// Collect 实现 Collector。
func (c *HostCollector) Collect(rep *proto.Report) error {
	rep.Sys = c.sys
	rep.Uptime = readUptime()
	return nil
}

// readSysInfo 尽力采集系统静态信息，单项失败留空不致命。
func readSysInfo() *proto.SysInfo {
	sys := &proto.SysInfo{Arch: runtime.GOARCH}
	if hn, err := os.Hostname(); err == nil {
		sys.Hostname = hn
	}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		sys.OS = parseOSRelease(string(b))
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		sys.Kernel = strings.TrimSpace(string(b))
	}
	model, cores := parseCPUInfo()
	sys.CPUModel, sys.CPUCores = model, cores
	return sys
}

// parseOSRelease 提取 PRETTY_NAME（去掉引号），失败回退 NAME，再失败返回空。
func parseOSRelease(content string) string {
	var name string
	for _, line := range strings.Split(content, "\n") {
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key, val := line[:idx], strings.Trim(line[idx+1:], `"`)
		if key == "PRETTY_NAME" {
			return val
		}
		if key == "NAME" {
			name = val
		}
	}
	return name
}

// parseCPUInfo 从 /proc/cpuinfo 提取 CPU 型号与逻辑核数。
// 核数用 processor 行数统计（比 runtime.NumCPU() 稳，不受亲和性影响）。
func parseCPUInfo() (model string, cores int) {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "", runtime.NumCPU()
	}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "model name") {
			idx := strings.Index(line, ":")
			if idx >= 0 && model == "" {
				model = strings.TrimSpace(line[idx+1:])
			}
		}
		if strings.HasPrefix(line, "processor") {
			cores++
		}
	}
	if cores == 0 {
		cores = runtime.NumCPU()
	}
	return model, cores
}

// readUptime 读取 /proc/uptime 首字段（秒）。
func readUptime() uint64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	up, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return uint64(up)
}
