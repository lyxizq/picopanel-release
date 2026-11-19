package nodecollect

import (
	"strings"
	"testing"
	"time"
)

// 两份相隔 10s 的 node_exporter 输出片段（数值精心构造，差值结果好算）。
// Δcpu：total +220、idle+iowait +200 → busy 20/220 ≈ 9.09%；
// eth0：rx +10000 B、tx +50000 B → 1000 / 5000 B/s；
// sda1 io_time +1s、sdb1 +2s → 10% / 20%。
const sampleA = `# HELP node_cpu_seconds_total Seconds the CPUs spent in each mode.
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} 100000
node_cpu_seconds_total{cpu="0",mode="iowait"} 1000
node_cpu_seconds_total{cpu="0",mode="user"} 20000
node_cpu_seconds_total{cpu="0",mode="system"} 10000
node_cpu_seconds_total{cpu="1",mode="idle"} 100000
node_cpu_seconds_total{cpu="1",mode="iowait"} 1000
node_cpu_seconds_total{cpu="1",mode="user"} 20000
node_cpu_seconds_total{cpu="1",mode="system"} 10000
# HELP node_load1 1m load average.
# TYPE node_load1 gauge
node_load1 0.52
node_load5 1.1
node_load15 0.9
node_memory_MemTotal_bytes 8.0e+09
node_memory_MemAvailable_bytes 3.0e+09
node_memory_SwapTotal_bytes 2000000000
node_memory_SwapFree_bytes 500000000
node_boot_time_seconds 1600000000
node_netstat_Tcp_CurrEstab 42
node_filesystem_size_bytes{device="/dev/sda1",fstype="ext4",mountpoint="/"} 80000000000
node_filesystem_avail_bytes{device="/dev/sda1",fstype="ext4",mountpoint="/"} 30000000000
node_filesystem_size_bytes{device="/dev/sdb1",fstype="xfs",mountpoint="/data"} 50000000000
node_filesystem_avail_bytes{device="/dev/sdb1",fstype="xfs",mountpoint="/data"} 10000000000
node_filesystem_size_bytes{device="tmpfs",fstype="tmpfs",mountpoint="/run"} 999999
node_filesystem_size_bytes{device="/dev/sda1",fstype="ext4",mountpoint="/var"} 80000000000
node_disk_io_time_seconds_total{device="sda1"} 12000.5
node_disk_io_time_seconds_total{device="sdb1"} 500.0
node_network_receive_bytes_total{device="eth0"} 1000000
node_network_transmit_bytes_total{device="eth0"} 2000000
node_network_receive_bytes_total{device="lo"} 99999999
node_uname_info{domainname="(none)",machine="x86_64",nodename="web-01",release="5.15.0-91-generic",sysname="Linux",version="#101-Ubuntu"} 1
node_os_info{id="ubuntu",pretty_name="Ubuntu 22.04.3 LTS",version_id="22.04"} 1
node_hwmon_temp_celsius{chip="coretemp",sensor="temp1"} 45.5
node_hwmon_temp_celsius{chip="coretemp",sensor="temp2"} 52
node_hwmon_temp_celsius{chip="drivetemp",sensor="temp1"} 33
node_hwmon_temp_celsius{chip="acpitz",sensor="temp1"} 46
`

const sampleB = `node_cpu_seconds_total{cpu="0",mode="idle"} 100090
node_cpu_seconds_total{cpu="0",mode="iowait"} 1010
node_cpu_seconds_total{cpu="0",mode="user"} 20008
node_cpu_seconds_total{cpu="0",mode="system"} 10002
node_cpu_seconds_total{cpu="1",mode="idle"} 100090
node_cpu_seconds_total{cpu="1",mode="iowait"} 1010
node_cpu_seconds_total{cpu="1",mode="user"} 20008
node_cpu_seconds_total{cpu="1",mode="system"} 10002
node_load1 0.6
node_load5 1.05
node_load15 0.92
node_memory_MemTotal_bytes 8000000000
node_memory_MemAvailable_bytes 2800000000
node_memory_SwapTotal_bytes 2000000000
node_memory_SwapFree_bytes 500000000
node_boot_time_seconds 1600000000
node_netstat_Tcp_CurrEstab 43 1700000000
node_filesystem_size_bytes{device="/dev/sda1",fstype="ext4",mountpoint="/"} 80000000000
node_filesystem_avail_bytes{device="/dev/sda1",fstype="ext4",mountpoint="/"} 29000000000
node_filesystem_size_bytes{device="/dev/sdb1",fstype="xfs",mountpoint="/data"} 50000000000
node_filesystem_avail_bytes{device="/dev/sdb1",fstype="xfs",mountpoint="/data"} 9000000000
node_disk_io_time_seconds_total{device="sda1"} 12001.5
node_disk_io_time_seconds_total{device="sdb1"} 502.0
node_network_receive_bytes_total{device="eth0"} 1010000
node_network_transmit_bytes_total{device="eth0"} 2050000
node_uname_info{machine="x86_64",nodename="web-01",release="5.15.0-91-generic",sysname="Linux"} 1
node_hwmon_temp_celsius{chip="coretemp",sensor="temp1"} 46
node_hwmon_temp_celsius{chip="coretemp",sensor="temp2"} 51
node_hwmon_temp_celsius{chip="drivetemp",sensor="temp1"} 34
node_hwmon_temp_celsius{chip="acpitz",sensor="temp1"} 46
`

// TestNodeCollect 覆盖：白名单解析、直接映射（内存/负载/盘/Sys）、
// 差值映射（CPU/网卡/IO）、fstype 过滤、同设备多挂载点去重、lo 跳过、
// Listens 为 nil、首轮差值为 0。
func TestNodeCollect(t *testing.T) {
	a, err := Parse(strings.NewReader(sampleA))
	if err != nil {
		t.Fatalf("解析样本 A: %v", err)
	}
	t0 := time.Now()
	a.At = t0

	c := &Client{}
	r0 := c.Report(a, t0)
	// 首轮：差值指标为 0
	if r0.CPU != 0 {
		t.Fatalf("首轮 CPU 应为 0: %v", r0.CPU)
	}
	// 内存：used = 8e9-3e9 = 5e9（62.5%）
	if r0.MemTotal != 8000000000 || r0.MemUsed != 5000000000 || r0.MemPercent != 62.5 {
		t.Fatalf("内存映射错误: total=%d used=%d pct=%v", r0.MemTotal, r0.MemUsed, r0.MemPercent)
	}
	if r0.SwapPercent != 75 {
		t.Fatalf("Swap 应 75%%: %v", r0.SwapPercent)
	}
	if r0.Load1 != 0.52 || r0.Load5 != 1.1 || r0.Load15 != 0.9 {
		t.Fatalf("负载映射错误: %v %v %v", r0.Load1, r0.Load5, r0.Load15)
	}
	if r0.TCPEst != 42 {
		t.Fatalf("TCP 连接数应 42: %d", r0.TCPEst)
	}
	if r0.Uptime != uint64(t0.Unix())-1600000000 {
		t.Fatalf("uptime 应为 now-boot: %d", r0.Uptime)
	}
	// 磁盘：/ 与 /data 保留，tmpfs /run 过滤，/var（同 sda1）去重
	if len(r0.Disks) != 2 || r0.Disks[0].Mount != "/" || r0.Disks[1].Mount != "/data" {
		t.Fatalf("磁盘应只剩 / 与 /data: %+v", r0.Disks)
	}
	if d := r0.Disks[0]; d.Percent != 62.5 || d.Device != "sda1" || d.FSType != "ext4" {
		t.Fatalf("/ 映射错误: %+v", d)
	}
	if d := r0.Disks[1]; d.Percent != 80 || d.Device != "sdb1" {
		t.Fatalf("/data 映射错误: %+v", d)
	}
	// 网卡：eth0 保留（counter 落地、速率为 0），lo 跳过
	if len(r0.Nets) != 1 || r0.Nets[0].Iface != "eth0" || r0.Nets[0].RxBytes != 1000000 {
		t.Fatalf("网卡映射错误: %+v", r0.Nets)
	}
	if r0.Nets[0].RxBps != 0 || r0.Nets[0].TxBps != 0 {
		t.Fatalf("首轮网卡速率应为 0: %+v", r0.Nets[0])
	}
	// Listens 无来源保持 nil；SysInfo 填充
	if r0.Listens != nil {
		t.Fatalf("node_exporter 无端口指标，Listens 应为 nil")
	}
	// 温度：hwmon 通道按 agent 口径打标签，CPU 多通道收敛最热一路，排序展示
	if len(r0.Temps) != 3 ||
		r0.Temps[0].Sensor != "CPU" || r0.Temps[0].Temp != 52 ||
		r0.Temps[1].Sensor != "acpitz" || r0.Temps[2].Sensor != "drivetemp" || r0.Temps[2].Temp != 33 {
		t.Fatalf("温度映射错误: %+v", r0.Temps)
	}
	if r0.Fans != nil {
		t.Fatalf("node_exporter 无风扇指标，Fans 应为 nil")
	}
	if r0.Sys == nil || r0.Sys.Hostname != "web-01" || r0.Sys.Kernel != "5.15.0-91-generic" ||
		r0.Sys.Arch != "x86_64" || r0.Sys.CPUCores != 2 || r0.Sys.OS != "Ubuntu 22.04.3 LTS" {
		t.Fatalf("SysInfo 映射错误: %+v", r0.Sys)
	}

	// 第二轮：差值生效
	b, err := Parse(strings.NewReader(sampleB))
	if err != nil {
		t.Fatalf("解析样本 B: %v", err)
	}
	t1 := t0.Add(10 * time.Second)
	b.At = t1
	r1 := c.Report(b, t1)
	// Δtotal=220、Δbusy=20 → 20/220*100 ≈ 9.0909
	if d := r1.CPU - 20.0/220.0*100; d > 1e-9 || d < -1e-9 {
		t.Fatalf("CPU 差值应 ≈9.09: %v", r1.CPU)
	}
	// eth0：rx +10000B/10s → 1000 B/s；tx +50000B/10s → 5000 B/s（存储口径
	// 为字节/秒，×8 转比特在展示端做）
	if len(r1.Nets) != 1 || r1.Nets[0].RxBps != 1000 || r1.Nets[0].TxBps != 5000 {
		t.Fatalf("网卡速率差值错误: %+v", r1.Nets)
	}
	// IO：sda1 Δ1s/10s → 10%；sdb1 Δ2s/10s → 20%
	if r1.Disks[0].IOUtil != 10 || r1.Disks[1].IOUtil != 20 {
		t.Fatalf("IO 差值应 10%%/20%%: %+v %+v", r1.Disks[0], r1.Disks[1])
	}
	// 带时间戳后缀的行（CurrEstab 43 ...）正常取值
	if r1.TCPEst != 43 {
		t.Fatalf("带时间戳行应取首个值: %d", r1.TCPEst)
	}
	// 样本 B 无 node_os_info：Sys.OS 为空（字段级缺失可容忍）
	if r1.Sys == nil || r1.Sys.OS != "" || r1.Sys.Hostname != "web-01" {
		t.Fatalf("样本 B SysInfo 应保留基础字段: %+v", r1.Sys)
	}
	// 温度是 gauge 直取（非差值）：样本 B 的值
	if len(r1.Temps) != 3 || r1.Temps[0].Temp != 51 || r1.Temps[2].Temp != 34 {
		t.Fatalf("样本 B 温度错误: %+v", r1.Temps)
	}
}

// TestNodeCollectIOMatch 覆盖设备名匹配链：NVMe 分区挂载回退整盘条目
// （/proc/diskstats 无 nvme 分区行是常态）；LVM 的 mapper 卷名与 dm-N
// 内核名在 /metrics 里无对应关系，IO 保持 0（已知取舍）。
func TestNodeCollectIOMatch(t *testing.T) {
	const ioA = `node_memory_MemTotal_bytes 8.0e+09
node_filesystem_size_bytes{device="/dev/nvme0n1p1",fstype="ext4",mountpoint="/"} 80000000000
node_filesystem_avail_bytes{device="/dev/nvme0n1p1",fstype="ext4",mountpoint="/"} 30000000000
node_filesystem_size_bytes{device="/dev/mapper/vg-lv",fstype="ext4",mountpoint="/data"} 50000000000
node_filesystem_avail_bytes{device="/dev/mapper/vg-lv",fstype="ext4",mountpoint="/data"} 10000000000
node_disk_io_time_seconds_total{device="nvme0n1"} 600.0
node_disk_io_time_seconds_total{device="dm-0"} 100.0
`
	const ioB = `node_memory_MemTotal_bytes 8.0e+09
node_filesystem_size_bytes{device="/dev/nvme0n1p1",fstype="ext4",mountpoint="/"} 80000000000
node_filesystem_avail_bytes{device="/dev/nvme0n1p1",fstype="ext4",mountpoint="/"} 30000000000
node_filesystem_size_bytes{device="/dev/mapper/vg-lv",fstype="ext4",mountpoint="/data"} 50000000000
node_filesystem_avail_bytes{device="/dev/mapper/vg-lv",fstype="ext4",mountpoint="/data"} 10000000000
node_disk_io_time_seconds_total{device="nvme0n1"} 605.0
node_disk_io_time_seconds_total{device="dm-0"} 103.0
`
	a, err := Parse(strings.NewReader(ioA))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(strings.NewReader(ioB))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	a.At = t0
	b.At = t0.Add(10 * time.Second)
	c := &Client{}
	c.Report(a, t0)
	r := c.Report(b, t0.Add(10*time.Second))
	if len(r.Disks) != 2 {
		t.Fatalf("应有两个挂载点: %+v", r.Disks)
	}
	// Device 字段规范化：/dev/ 与 mapper/ 前缀都剥掉
	if r.Disks[0].Device != "nvme0n1p1" || r.Disks[1].Device != "vg-lv" {
		t.Fatalf("Device 规范化错误: %q %q", r.Disks[0].Device, r.Disks[1].Device)
	}
	// / 挂在 nvme0n1p1，io 指标只有整盘 nvme0n1：回退命中，Δ5s/10s → 50%
	if r.Disks[0].IOUtil != 50 {
		t.Fatalf("NVMe 分区应回退整盘条目得 50%%: %v", r.Disks[0].IOUtil)
	}
	// /data 是 LVM 卷（vg-lv），io 指标叫 dm-0：无从对应，保持 0
	if r.Disks[1].IOUtil != 0 {
		t.Fatalf("LVM 卷名与 dm-N 无对应关系，IO 应为 0: %v", r.Disks[1].IOUtil)
	}
}

// TestParseMemFallback MemAvailable 缺失（旧内核）回退 MemFree+Buffers+Cached。
func TestParseMemFallback(t *testing.T) {
	s, err := Parse(strings.NewReader(`node_memory_MemTotal_bytes 1000
node_memory_MemFree_bytes 200
node_memory_Buffers_bytes 50
node_memory_Cached_bytes 150
`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r := (&Client{}).Report(s, now)
	if r.MemUsed != 600 || r.MemPercent != 60 {
		t.Fatalf("回退口径 used=600/60%%: used=%d pct=%v", r.MemUsed, r.MemPercent)
	}
}

// TestParseReject 非法/空输出报错，白名单外指标被忽略不致错。
func TestParseReject(t *testing.T) {
	if _, err := Parse(strings.NewReader("# 只有一行注释\n")); err == nil {
		t.Fatalf("无可识别指标应报错")
	}
	if _, err := Parse(strings.NewReader("some_other_metric 1\n2 3\n")); err == nil {
		t.Fatalf("无 node_* 指标应报错")
	}
}
