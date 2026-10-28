package collect

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"lz-monitor/internal/proto"
)

// fsBlacklist 过滤伪文件系统与容器运行时挂载，避免无意义的磁盘条目。
var fsBlacklist = map[string]bool{
	"sysfs": true, "proc": true, "devtmpfs": true, "devpts": true,
	"tmpfs": true, "squashfs": true, "overlay": true, "cgroup": true,
	"cgroup2": true, "efivarfs": true, "fusectl": true, "mqueue": true,
	"hugetlbfs": true, "debugfs": true, "tracefs": true, "configfs": true,
	"securityfs": true, "pstore": true, "bpf": true, "autofs": true,
	"binfmt_misc": true, "rpc_pipefs": true, "fuse.snapfuse": true,
	"iso9660": true, "udf": true, "ramfs": true,
}

// mountPrefixSkip 跳过这些前缀下的挂载点。
var mountPrefixSkip = []string{"/proc", "/sys", "/dev", "/run", "/snap", "/boot/efi", "/var/lib/docker"}

type mountEntry struct {
	device string
	mount  string
	fstype string
}

// parseMounts 解析 /proc/mounts。挂载点中的空格转义为 \040，需还原。
func parseMounts(r io.Reader) []mountEntry {
	var entries []mountEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		entries = append(entries, mountEntry{
			device: fields[0],
			mount:  strings.ReplaceAll(fields[1], "\\040", " "),
			fstype: fields[2],
		})
	}
	return entries
}

// DiskCollector 采集各真实挂载点的磁盘使用率与底层设备 IO（使用率 + 读/写速率）。
// IO 取 /proc/diskstats 累计计数器差值：io_ticks 与采样间隔相除即 iostat 的
// %util，读/写扇区数 ×512 字节与采样间隔相除即读/写速率，因此采集器需保存上次采样。
type DiskCollector struct {
	prevAt time.Time
	prevIO map[string]proto.DiskIOStat // 设备名 → 上次累计计数器
	devRes map[string]string           // 挂载点+设备列 → diskstats 设备名（静态，懒缓存）
}

// NewDiskCollector 返回磁盘采集器。
func NewDiskCollector() *DiskCollector {
	return &DiskCollector{devRes: make(map[string]string)}
}

// Name 实现 Collector。
func (c *DiskCollector) Name() string { return "disk" }

// Collect 实现 Collector。单个挂载点 statfs 失败（如已卸载）跳过不致命。
func (c *DiskCollector) Collect(rep *proto.Report) error {
	f, err := open("/proc/mounts")
	if err != nil {
		return err
	}
	defer f.Close()

	seen := make(map[string]bool) // 同一设备多挂载点去重
	for _, e := range parseMounts(f) {
		if fsBlacklist[e.fstype] || seen[e.device] {
			continue
		}
		skip := false
		for _, p := range mountPrefixSkip {
			if e.mount == p || strings.HasPrefix(e.mount, p+"/") {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		var st unix.Statfs_t
		if err := unix.Statfs(e.mount, &st); err != nil {
			continue
		}
		seen[e.device] = true
		total := uint64(st.Blocks) * uint64(st.Bsize)
		avail := uint64(st.Bavail) * uint64(st.Bsize) // Bavail=普通用户可用，不含 root 保留
		used := uint64(st.Blocks-st.Bfree) * uint64(st.Bsize)
		percent := 0.0
		if used+avail > 0 { // 用 used+avail 而非 total，避免 root 保留区把小盘显示成 100%
			percent = float64(used) / float64(used+avail) * 100
		}
		rep.Disks = append(rep.Disks, proto.DiskInfo{
			Mount:   e.mount,
			FSType:  e.fstype,
			Total:   total,
			Used:    used,
			Percent: percent,
			Device:  c.resolveDevice(e.mount, e.device),
		})
	}
	sort.Slice(rep.Disks, func(i, j int) bool { return rep.Disks[i].Mount < rep.Disks[j].Mount })
	c.fillIOUtil(rep.Disks, time.Now())
	return nil
}

// resolveDevice 把挂载点解析为 /proc/diskstats 里的设备名（sda1、dm-0）。
// 优先取挂载点的 st_dev（内核设备号 major:minor）读 /sys/dev/block/N:M 软链接
// 还原内核设备名——不依赖 /dev 命名，LVM 软链接缺失、容器内 /dev 不全、
// by-id 路径挂载都能解出；sysfs 读不到再按 /proc/mounts 设备列字面解析
// （mapper 软链接 → dm-0）。结果缓存（键=挂载点+设备列）。
func (c *DiskCollector) resolveDevice(mount, dev string) string {
	key := mount + "\x00" + dev
	if v, ok := c.devRes[key]; ok {
		return v
	}
	name := ""
	var st unix.Stat_t
	if err := unix.Stat(mount, &st); err == nil {
		link := fmt.Sprintf("/sys/dev/block/%d:%d", unix.Major(uint64(st.Dev)), unix.Minor(uint64(st.Dev)))
		if dst, err := os.Readlink(link); err == nil {
			name = filepath.Base(dst) // "../../devices/.../dm-0" → "dm-0"
		}
	}
	if name == "" {
		name = strings.TrimPrefix(dev, "/dev/")
		if strings.HasPrefix(name, "mapper/") {
			if dst, err := os.Readlink("/dev/" + name); err == nil {
				name = filepath.Base(dst) // "../dm-0" → "dm-0"
			} else {
				name = strings.TrimPrefix(name, "mapper/") // 解析失败退回原名（罕见）
			}
		}
	}
	c.devRes[key] = name
	return name
}

// fillIOUtil 读 /proc/diskstats 并用差值填各挂载点设备的 IO 使用率与读/写速率
// （首次采样为 0）。
func (c *DiskCollector) fillIOUtil(disks []proto.DiskInfo, now time.Time) {
	f, err := open("/proc/diskstats")
	if err != nil {
		return
	}
	cur := parseDiskStats(f)
	f.Close()
	applyIOUtil(disks, cur, c.prevIO, now.Sub(c.prevAt).Milliseconds())
	c.prevAt, c.prevIO = now, cur
}

// applyIOUtil 差值计算各设备 IO 使用率与读/写速率（纯函数便于单测）：
// %util = Δio_ticks(ms) / Δ时长(ms) × 100；读/写速率 = Δ扇区 × 512B / Δ时长。
// Linux 块设备扇区固定 512 字节（与物理扇区大小无关，iostat 同口径）。
func applyIOUtil(disks []proto.DiskInfo, cur, prev map[string]proto.DiskIOStat, dtMs int64) {
	for i := range disks {
		if disks[i].Device == "" {
			continue
		}
		t, ok := cur[disks[i].Device]
		if !ok {
			continue
		}
		p, seen := prev[disks[i].Device]
		if !seen || dtMs <= 0 || t.Ticks < p.Ticks {
			continue // 首次采样 / 计数器回绕
		}
		util := float64(t.Ticks-p.Ticks) / float64(dtMs) * 100
		if util > 100 {
			util = 100 // 多队列设备理论上可超 100，夹紧
		}
		disks[i].IOUtil = util
		dtSec := float64(dtMs) / 1000
		if t.ReadSectors >= p.ReadSectors {
			disks[i].IoRead = float64(t.ReadSectors-p.ReadSectors) * 512 / dtSec
		}
		if t.WriteSectors >= p.WriteSectors {
			disks[i].IoWrite = float64(t.WriteSectors-p.WriteSectors) * 512 / dtSec
		}
	}
}

// parseDiskStats 解析 /proc/diskstats，返回 设备名 → 累计计数器。
func parseDiskStats(r io.Reader) map[string]proto.DiskIOStat {
	b, _ := io.ReadAll(r) // 读错误按部分数据处理（与旧 scanner 行为一致）
	return proto.ParseDiskStatsLines(strings.Split(string(b), "\n"))
}
