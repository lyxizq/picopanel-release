package collect

import (
	"strings"
	"testing"
)

func TestParseMemInfo(t *testing.T) {
	fixture := strings.NewReader(
		"MemTotal:       16384000 kB\n" +
			"MemFree:         2000000 kB\n" +
			"MemAvailable:    8000000 kB\n" +
			"Buffers:          500000 kB\n" +
			"Cached:          6000000 kB\n" +
			"SwapTotal:       2000000 kB\n" +
			"SwapFree:        1500000 kB\n")
	m, err := parseMemInfo(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if m["MemTotal"] != 16384000*1024 {
		t.Fatalf("MemTotal = %d", m["MemTotal"])
	}
	if m["MemAvailable"] != 8000000*1024 {
		t.Fatalf("MemAvailable = %d", m["MemAvailable"])
	}
}

// TestUsedMem 与 Prometheus（node_exporter）标准面板同口径：
// 有 MemAvailable 时 used = MemTotal - MemAvailable；
// 旧内核（<3.14）无该字段时回退 free 命令口径
// used = total - MemFree - Buffers - Cached - SReclaimable（page cache 不计入已用）。
func TestUsedMem(t *testing.T) {
	m := map[string]uint64{
		"MemTotal": 1000 * 1024, "MemFree": 100 * 1024, "MemAvailable": 300 * 1024,
		"Buffers": 100 * 1024, "Cached": 300 * 1024, "SReclaimable": 50 * 1024,
	}
	// MemAvailable 优先：used = 1000-300，不受 free/cache 影响
	if got := usedMem(m["MemTotal"], m); got != 700*1024 {
		t.Fatalf("used = %d, 期望 700*1024", got)
	}
	// MemAvailable 超总量（异常数据）：按 0 处理，不发生下溢
	m["MemAvailable"] = 1200 * 1024
	if got := usedMem(m["MemTotal"], m); got != 0 {
		t.Fatalf("超总量 used = %d, 期望 0", got)
	}
	// 旧内核无 MemAvailable：回退 free 命令口径
	delete(m, "MemAvailable")
	if got := usedMem(m["MemTotal"], m); got != 450*1024 {
		t.Fatalf("used = %d, 期望 450*1024", got)
	}
	// 旧内核无 SReclaimable 字段：按 0 参与计算
	delete(m, "SReclaimable")
	if got := usedMem(m["MemTotal"], m); got != 500*1024 {
		t.Fatalf("无 SReclaimable used = %d, 期望 500*1024", got)
	}
	// MemFree 与 cache 之和超过总量（异常虚拟化环境）：按 0 处理，不发生下溢
	m["MemFree"] = 800 * 1024
	if got := usedMem(m["MemTotal"], m); got != 0 {
		t.Fatalf("超总量 used = %d, 期望 0", got)
	}
}
