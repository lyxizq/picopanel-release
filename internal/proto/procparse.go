// /proc 文本行级解析的共享实现。
// agent 直读 /proc 文件、SSH 采集解析远端 cat 输出，两侧必须严格同口径
// （历史上各自实现出现过字段数与边界条件的漂移），故纯解析统一收在这里，
// 两侧只保留各自的 I/O 适配（文件流 / 哨兵分块文本）。
package proto

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseCPUStatLine 解析 /proc/stat 的 "cpu" 汇总行，返回 busy 与 total 累计 jiffies。
// 行格式：cpu user nice system idle iowait irq softirq steal …
// （guest 已计入 user/nice，不重复累加）。
func ParseCPUStatLine(line string) (busy, total uint64, err error) {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, fmt.Errorf("/proc/stat 行无法解析: %q", line)
	}
	vals := make([]uint64, len(f)-1)
	for i, s := range f[1:] {
		v, e := strconv.ParseUint(s, 10, 64)
		if e != nil {
			return 0, 0, fmt.Errorf("/proc/stat 字段无法解析: %q", s)
		}
		vals[i] = v
	}
	get := func(i int) uint64 {
		if i < len(vals) {
			return vals[i]
		}
		return 0
	}
	idle := get(3) + get(4) // idle + iowait
	busy = get(0) + get(1) + get(2) + get(5) + get(6) + get(7)
	return busy, busy + idle, nil
}

// ParseMeminfoKV 解析 /proc/meminfo 为 key → 字节数（原文件数值单位为 kB）。
func ParseMeminfoKV(lines []string) map[string]uint64 {
	kv := make(map[string]uint64)
	for _, line := range lines {
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		f := strings.Fields(line[idx+1:])
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue // 跳过非数值行（如 VmallocChunk 在部分内核为 huge）
		}
		kv[strings.TrimSuffix(line[:idx], ":")] = v * 1024
	}
	return kv
}

// MemUsed 计算已用内存，与 Prometheus（node_exporter）标准面板同口径：
// used = MemTotal - MemAvailable（内核对可回收内存的保守估计）；
// 旧内核（<3.14）无 MemAvailable 字段时回退 free 命令口径：
// used = 总量 - MemFree - Buffers - Cached - SReclaimable。
// 异常数据（字段超总量、各字段之和超总量）按 0 处理，避免无符号下溢。
func MemUsed(total uint64, kv map[string]uint64) uint64 {
	if avail, ok := kv["MemAvailable"]; ok {
		if avail > total {
			return 0
		}
		return total - avail
	}
	cache := kv["Buffers"] + kv["Cached"] + kv["SReclaimable"] // 旧内核无 SReclaimable 时为 0
	free := kv["MemFree"]
	if free+cache >= total {
		return 0
	}
	return total - free - cache
}

// SwapPercent 计算 Swap 使用率；无 Swap 或数据异常（SwapFree 超总量）返回 0。
func SwapPercent(kv map[string]uint64) float64 {
	total := kv["SwapTotal"]
	if total == 0 || kv["SwapFree"] > total {
		return 0
	}
	return float64(total-kv["SwapFree"]) / float64(total) * 100
}

// ParseNetDevLines 解析 /proc/net/dev：iface: rx_bytes … tx_bytes …
// （第 1、9 个数值为累计收发字节），跳过表头行与 lo。
func ParseNetDevLines(lines []string) []NetInfo {
	var nets []NetInfo
	for _, line := range lines {
		i := strings.Index(line, ":")
		if i < 0 {
			continue // 表头
		}
		iface := strings.TrimSpace(line[:i])
		if iface == "" || iface == "lo" {
			continue
		}
		f := strings.Fields(line[i+1:])
		if len(f) < 9 {
			continue
		}
		rx, e1 := strconv.ParseUint(f[0], 10, 64)
		tx, e2 := strconv.ParseUint(f[8], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		nets = append(nets, NetInfo{Iface: iface, RxBytes: rx, TxBytes: tx})
	}
	return nets
}

// ParseLoadAvgLine 解析 /proc/loadavg 首行："0.52 0.58 0.59 1/467 12345"。
func ParseLoadAvgLine(line string) (l1, l5, l15 float64, err error) {
	f := strings.Fields(line)
	if len(f) < 3 {
		return 0, 0, 0, fmt.Errorf("/proc/loadavg 行无法解析: %q", line)
	}
	if l1, err = strconv.ParseFloat(f[0], 64); err != nil {
		return
	}
	if l5, err = strconv.ParseFloat(f[1], 64); err != nil {
		return
	}
	l15, err = strconv.ParseFloat(f[2], 64)
	return
}

// DiskIOStat 是一个设备在 /proc/diskstats 里的累计计数器（差值算速率用）。
type DiskIOStat struct {
	ReadSectors  uint64 // 累计读扇区数（每扇区 512 字节）
	WriteSectors uint64 // 累计写扇区数
	Ticks        uint64 // 累计 io_ticks（毫秒），差值/时长即 iostat %util
}

// ParseDiskStatsLines 解析 /proc/diskstats，返回 设备名 → 累计计数器。
// 列：… read sectors(f[5]) … write sectors(f[9]) … 第 13 列 io_ticks(f[12])
// 为设备有 IO 在途的累计毫秒数。老内核分区行不足 14 列（无 io_ticks）跳过。
func ParseDiskStatsLines(lines []string) map[string]DiskIOStat {
	m := make(map[string]DiskIOStat)
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 14 {
			continue
		}
		var st DiskIOStat
		var err error
		if st.ReadSectors, err = strconv.ParseUint(f[5], 10, 64); err != nil {
			continue
		}
		if st.WriteSectors, err = strconv.ParseUint(f[9], 10, 64); err != nil {
			continue
		}
		if st.Ticks, err = strconv.ParseUint(f[12], 10, 64); err != nil {
			continue
		}
		m[f[2]] = st
	}
	return m
}
