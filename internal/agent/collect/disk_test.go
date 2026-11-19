package collect

import (
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/proto"
)

func TestParseDiskStats(t *testing.T) {
	// 列依次：major minor name 读写完成/合并/扇节/耗时×2 in-flight io_ticks weighted …
	// f[5]=读扇区 f[9]=写扇区 f[12]=io_ticks
	fixture := "   8       0 sda 1 2 3 4 5 6 7 8 9 5000 6000 0 0 0 0\n" +
		" 252       0 dm-0 1 2 3 4 5 6 7 8 9 2000 3000 0 0 0 0\n" +
		" 253       1 vda1 1 2 3 4 5 6 7 8 9 100 200 0 0 0 0\n" +
		"   7       0 loop0 0 0 0 0 0 0 0 0 0\n" // 不足 14 列，跳过
	m := parseDiskStats(strings.NewReader(fixture))
	if len(m) != 3 {
		t.Fatalf("diskstats 解析错误: %+v", m)
	}
	if m["sda"].Ticks != 5000 || m["sda"].ReadSectors != 3 || m["sda"].WriteSectors != 7 {
		t.Fatalf("sda 解析错误: %+v", m["sda"])
	}
	if m["dm-0"].Ticks != 2000 || m["vda1"].Ticks != 100 {
		t.Fatalf("diskstats 解析错误: %+v", m)
	}
}

func TestApplyIOUtil(t *testing.T) {
	disks := []proto.DiskInfo{
		{Mount: "/", Device: "sda1"},
		{Mount: "/data", Device: "sdb1"},
		{Mount: "/boot", Device: ""}, // 无设备名跳过
	}
	// 10s 内 sda1 io_ticks 增 2000ms → 20%，读扇区 +2000×512B → 102400 B/s；
	// sdb1 io_ticks 增 20000ms → 夹紧 100%
	cur := map[string]proto.DiskIOStat{
		"sda1": {Ticks: 3000, ReadSectors: 12000, WriteSectors: 6000},
		"sdb1": {Ticks: 21000},
	}
	prev := map[string]proto.DiskIOStat{
		"sda1": {Ticks: 1000, ReadSectors: 10000, WriteSectors: 5000},
		"sdb1": {Ticks: 1000},
	}
	applyIOUtil(disks, cur, prev, 10*1000)
	if disks[0].IOUtil < 19.9 || disks[0].IOUtil > 20.1 {
		t.Fatalf("sda1 IO = %v, want 20", disks[0].IOUtil)
	}
	if disks[1].IOUtil != 100 {
		t.Fatalf("sdb1 IO 应夹紧 100, got %v", disks[1].IOUtil)
	}
	if disks[2].IOUtil != 0 {
		t.Fatalf("无设备名的挂载点不应有 IO 值: %v", disks[2].IOUtil)
	}
	if d := disks[0].IoRead - 102400; d < -1 || d > 1 {
		t.Fatalf("sda1 读速率 = %v, want ≈102400", disks[0].IoRead)
	}
	if d := disks[0].IoWrite - 51200; d < -1 || d > 1 {
		t.Fatalf("sda1 写速率 = %v, want ≈51200", disks[0].IoWrite)
	}

	// 首次采样（prev 无该设备）与计数器回绕（t < p）均不写入，保持 0
	fresh := []proto.DiskInfo{{Mount: "/", Device: "sda1"}}
	applyIOUtil(fresh, map[string]proto.DiskIOStat{"sda1": {Ticks: 900}}, map[string]proto.DiskIOStat{"sda1": {Ticks: 1000}}, 10000)
	if fresh[0].IOUtil != 0 || fresh[0].IoRead != 0 || fresh[0].IoWrite != 0 {
		t.Fatalf("回绕时 IO 应为 0, got %+v", fresh[0])
	}
	applyIOUtil(fresh, map[string]proto.DiskIOStat{"sdb1": {Ticks: 1}}, nil, 10000)
	if fresh[0].IOUtil != 0 {
		t.Fatalf("首次采样 IO 应为 0, got %v", fresh[0].IOUtil)
	}
}

// TestResolveDevice 验证挂载点到 diskstats 设备名的映射
// （macOS 测试机上 /sys/dev/block 与 /dev/mapper 不存在，覆盖解析失败回退分支）。
func TestResolveDevice(t *testing.T) {
	c := NewDiskCollector()
	if got := c.resolveDevice("/mnt/a", "/dev/sda1"); got != "sda1" {
		t.Fatalf("sda1 = %q", got)
	}
	if got := c.resolveDevice("/mnt/b", "/dev/mapper/vg--data"); got != "vg--data" {
		t.Fatalf("mapper 解析失败应回退原名, got %q", got)
	}
	// 缓存生效：同挂载点+设备第二次直接命中
	if got := c.resolveDevice("/mnt/a", "/dev/sda1"); got != "sda1" {
		t.Fatalf("缓存命中 = %q", got)
	}
	if c.prevAt.After(time.Time{}) {
		t.Fatal("新采集器不应有上次采样")
	}
}
