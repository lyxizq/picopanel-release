package collect

import (
	"io"
	"os"
	"strings"

	"lz-monitor/internal/proto"
)

// parseMemInfo 解析 /proc/meminfo，返回 key → 字节数（原文件单位为 kB）。
func parseMemInfo(r io.Reader) (map[string]uint64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return proto.ParseMeminfoKV(strings.Split(string(b), "\n")), nil
}

// MemCollector 采集内存与 Swap 使用率。
type MemCollector struct{}

// NewMemCollector 返回内存采集器。
func NewMemCollector() *MemCollector { return &MemCollector{} }

// Name 实现 Collector。
func (c *MemCollector) Name() string { return "mem" }

// Collect 实现 Collector。
func (c *MemCollector) Collect(rep *proto.Report) error {
	f, err := open("/proc/meminfo")
	if err != nil {
		return err
	}
	defer f.Close()
	m, err := parseMemInfo(f)
	if err != nil {
		return err
	}
	total := m["MemTotal"]
	if total == 0 {
		return nil
	}
	used := usedMem(total, m)
	total, used = adjustContainer(total, used)
	rep.MemTotal = total
	rep.MemUsed = used
	rep.MemPercent = float64(used) / float64(total) * 100

	swapTotal := m["SwapTotal"]
	if swapTotal > 0 {
		rep.SwapPercent = proto.SwapPercent(m)
	}
	return nil
}

// usedMem 与 Prometheus（node_exporter）标准面板同口径（详见 proto.MemUsed）。
func usedMem(total uint64, m map[string]uint64) uint64 {
	return proto.MemUsed(total, m)
}

// adjustContainer 容器环境修正（详见 proto.AdjustContainerMem）：
// /proc/meminfo 可能是宿主机口径，cgroup 限额更贴近本机实际。
func adjustContainer(total, used uint64) (uint64, uint64) {
	return proto.AdjustContainerMem(total, used,
		readSmall("/proc/self/cgroup"), readSmall("/proc/1/cgroup"),
		readSmall("/sys/fs/cgroup/memory.max"), readSmall("/sys/fs/cgroup/memory.current"),
		readSmall("/sys/fs/cgroup/memory/memory.limit_in_bytes"),
		readSmall("/sys/fs/cgroup/memory/memory.usage_in_bytes"))
}

// readSmall 读取小文件内容（cgroup 相关文件均为几十字节；失败返回空串即自动跳过）。
func readSmall(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
