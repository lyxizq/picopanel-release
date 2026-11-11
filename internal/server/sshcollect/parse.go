package sshcollect

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"lz-monitor/internal/proto"
)

// staticCmd 采集静态信息（hostname/内核/发行版/架构/CPU 型号核数），
// 每个连接只执行一次，结果缓存在 Client.sys。
// sed/grep/head 均为 BusyBox 兼容写法；输出用 =X= 哨兵行分块。
const staticCmd = `echo =H=; cat /proc/sys/kernel/hostname; ` +
	`echo =K=; cat /proc/sys/kernel/osrelease; ` +
	`echo =O=; sed -n 's/^PRETTY_NAME=//p' /etc/os-release 2>/dev/null; ` +
	`echo =A=; uname -m; ` +
	`echo =M=; sed -n 's/^model name[^:]*:[[:space:]]*//p' /proc/cpuinfo 2>/dev/null | head -n1; ` +
	`echo =C=; grep -c ^processor /proc/cpuinfo`

// dynamicCmd 每个采集周期执行一次，全部为只读命令：
// 累计计数器（/proc/stat 首行 jiffies、/proc/net/dev 字节）的差值在服务端计算。
// TCP 连接数用 awk 在远端计数（/proc/net/tcp 在高连接数机器上可达数 MB，拉回不划算；
// awk 的精确字段比较规避 grep 子串误匹配，BusyBox 自带 awk）。
// 监听端口同理只拉回监听行（st=0A / UDP 未连接），环回地址（0100007F=127.0.0.1、
// …01000000=::1）在远端直接排除不回传；地址保持十六进制由服务端解析。
const dynamicCmd = `echo =T=; cat /proc/uptime; ` +
	`echo =S=; head -n1 /proc/stat; ` +
	`echo =MI=; cat /proc/meminfo; ` +
	`echo =L=; cat /proc/loadavg; ` +
	`echo =ND=; cat /proc/net/dev; ` +
	`echo =DF=; LC_ALL=C df -kP; ` +
	`echo =DS=; cat /proc/diskstats; ` +
	`echo =DM=; ls -ln /dev/mapper 2>/dev/null; ` +
	`echo =TCP=; awk '$4=="01"{c++}END{print c+0}' /proc/net/tcp /proc/net/tcp6 2>/dev/null; ` +
	`echo =LS=; awk '$4=="0A" && $2 !~ /^(0100007F|00000000000000000000000001000000):/{print "tcp",$2}' /proc/net/tcp /proc/net/tcp6 2>/dev/null; ` +
	`awk 'FNR>1 && $3=="00000000:0000" && $2 !~ /^(0100007F|00000000000000000000000001000000):/{print "udp",$2}' /proc/net/udp /proc/net/udp6 2>/dev/null; ` +
	`echo =CG=; cat /proc/self/cgroup; echo ---; cat /proc/1/cgroup; echo ---; ` +
	`cat /sys/fs/cgroup/memory.max 2>/dev/null; echo ---; ` +
	`cat /sys/fs/cgroup/memory.current 2>/dev/null; echo ---; ` +
	`cat /sys/fs/cgroup/memory/memory.limit_in_bytes 2>/dev/null; echo ---; ` +
	`cat /sys/fs/cgroup/memory/memory.usage_in_bytes 2>/dev/null`

// splitBlocks 把哨兵分块输出切成 map[key]内容。哨兵行形如 "=MI="（独占一行）。
func splitBlocks(out []byte) map[string]string {
	m := make(map[string]string)
	cur := ""
	var b strings.Builder
	for _, line := range strings.Split(string(out), "\n") {
		if k := blockKey(line); k != "" {
			if cur != "" {
				m[cur] = b.String()
			}
			cur, b = k, strings.Builder{}
			continue
		}
		if cur != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(line)
		}
	}
	if cur != "" {
		m[cur] = b.String()
	}
	return m
}

// blockKey 识别 "=X=" 形式的哨兵行，返回键名；非哨兵行返回空串。
func blockKey(line string) string {
	if len(line) < 3 || line[0] != '=' || line[len(line)-1] != '=' {
		return ""
	}
	k := line[1 : len(line)-1]
	for _, r := range k {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return "" // 内容行不以 "=词=" 独占，容忍 /proc 内容里出现 '='
		}
	}
	return k
}

// parseStatic 解析 staticCmd 输出为 SysInfo。
func parseStatic(out []byte) (*proto.SysInfo, error) {
	b := splitBlocks(out)
	sys := &proto.SysInfo{
		Hostname: firstLine(b["H"]),
		Kernel:   firstLine(b["K"]),
		OS:       strings.Trim(firstLine(b["O"]), `"'`),
		Arch:     firstLine(b["A"]),
		CPUModel: firstLine(b["M"]),
	}
	sys.CPUCores, _ = strconv.Atoi(firstLine(b["C"]))
	if sys.Hostname == "" {
		return nil, fmt.Errorf("静态信息输出无法解析（hostname 为空）")
	}
	return sys, nil
}

// parseDynamic 解析 dynamicCmd 输出。返回 Report（CPU、网卡速率与磁盘 IO
// 留 0，由 applyDelta 用累计差值补齐）、CPU jiffies 与各设备 IO 累计计数器。
func parseDynamic(out []byte) (rep *proto.Report, cpuBusy, cpuTotal float64, ioStats map[string]proto.DiskIOStat, err error) {
	b := splitBlocks(out)
	rep = &proto.Report{}
	if b["T"] == "" || b["S"] == "" || b["MI"] == "" {
		return nil, 0, 0, nil, fmt.Errorf("采集输出缺少必要分块（T/S/MI）")
	}

	// uptime
	if f, e := strconv.ParseFloat(fields(b["T"])[0], 64); e == nil && f > 0 {
		rep.Uptime = uint64(f)
	}

	// CPU 累计 jiffies：cpu  user nice system idle iowait irq softirq steal ...
	cpuBusy, cpuTotal, err = parseCPUStat(b["S"])
	if err != nil {
		return nil, 0, 0, nil, err
	}

	// 内存 / Swap
	parseMeminfo(b["MI"], rep)

	// 容器环境修正（与 agent 一致）：meminfo 可能是宿主机口径，cgroup 限额更贴近实际
	slots := splitCG(b["CG"])
	rep.MemTotal, rep.MemUsed = proto.AdjustContainerMem(rep.MemTotal, rep.MemUsed,
		slots[0], slots[1], slots[2], slots[3], slots[4], slots[5])
	if rep.MemTotal > 0 {
		rep.MemPercent = float64(rep.MemUsed) / float64(rep.MemTotal) * 100
	}

	// 负载
	if l1, l5, l15, e := proto.ParseLoadAvgLine(strings.TrimSpace(b["L"])); e == nil {
		rep.Load1, rep.Load5, rep.Load15 = l1, l5, l15
	}

	// 网卡累计字节（跳过 lo；速率由差值补齐）
	rep.Nets = parseNetDev(b["ND"])

	// 磁盘（df -kP，1K 块）；Device 由 df 设备列 + mapper 软链接解析
	rep.Disks = parseDF(b["DF"])
	resolveDiskDevices(rep.Disks, b["DM"])

	// 磁盘 IO：累计计数器（差值在 applyDelta 计算为 %util 与读/写速率）
	ioStats = parseDiskStats(b["DS"])

	// TCP established
	rep.TCPEst, _ = strconv.Atoi(strings.TrimSpace(b["TCP"]))

	// 监听端口（LS 块为 "tcp 0100007F:1F90" 行，IPv6 地址列 32 位十六进制）
	rep.Listens = parseListen(b["LS"])
	return rep, cpuBusy, cpuTotal, ioStats, nil
}

// parseDiskStats 解析 =DS= 块（/proc/diskstats），返回 设备名 → 累计计数器。
func parseDiskStats(s string) map[string]proto.DiskIOStat {
	return proto.ParseDiskStatsLines(strings.Split(s, "\n"))
}

// resolveDiskDevices 把 parseDF 存的原始设备列（/dev/sda1、/dev/mapper/vg--lv）
// 解析为 /proc/diskstats 的设备名（sda1、dm-0）；mapper 软链接经 =DM= 块
// （ls -ln /dev/mapper 输出）解析。
func resolveDiskDevices(disks []proto.DiskInfo, mapperOut string) {
	dm := make(map[string]string) // mapper 名 → dm-N
	for _, line := range strings.Split(mapperOut, "\n") {
		i := strings.Index(line, "->")
		if i < 0 {
			continue
		}
		left := fields(line[:i])
		if len(left) == 0 {
			continue
		}
		dm[strings.TrimPrefix(left[len(left)-1], "/dev/mapper/")] = filepath.Base(strings.TrimSpace(line[i+2:]))
	}
	for i := range disks {
		name := strings.TrimPrefix(disks[i].Device, "/dev/")
		if strings.HasPrefix(name, "mapper/") {
			name = strings.TrimPrefix(name, "mapper/")
			if v, ok := dm[name]; ok {
				name = v
			}
		}
		disks[i].Device = name
	}
}

// parseListen 解析 =LS= 块（远端 awk 输出的 "proto HEXADDR" 行）。
func parseListen(s string) []proto.ListenPort {
	var entries []proto.ListenEntry
	for _, line := range strings.Split(s, "\n") {
		f := fields(line)
		if len(f) != 2 || (f[0] != "tcp" && f[0] != "udp") {
			continue
		}
		addr, port, ok := proto.ParseHexSockAddr(f[1], strings.IndexByte(f[1], ':') == 32)
		if ok {
			entries = append(entries, proto.ListenEntry{Proto: f[0], Addr: addr, Port: port})
		}
	}
	return proto.BuildListenList(entries, 256)
}

// parseCPUStat 解析 /proc/stat 首行，返回 busy 与 total 累计 jiffies
// （SSH 侧用 float64 做跨周期差值，jiffies 远小于 2^53，转换无损）。
func parseCPUStat(line string) (busy, total float64, err error) {
	b, t, err := proto.ParseCPUStatLine(line)
	return float64(b), float64(t), err
}

// parseMeminfo 解析 /proc/meminfo，填内存与 Swap 字段（口径见 proto.MemUsed）。
func parseMeminfo(s string, rep *proto.Report) {
	kv := proto.ParseMeminfoKV(strings.Split(s, "\n"))
	rep.MemTotal = kv["MemTotal"]
	rep.MemUsed = proto.MemUsed(kv["MemTotal"], kv)
	if rep.MemTotal > 0 {
		rep.MemPercent = float64(rep.MemUsed) / float64(rep.MemTotal) * 100
	}
	rep.SwapPercent = proto.SwapPercent(kv)
}

// splitCG 解析 =CG= 块：六个片段依次为 self/init 的 cgroup 路径与 cgroup v2/v1 的
// 限额、用量文件内容，以 "---" 行分隔；目标机没有的文件为空片段。
func splitCG(s string) [6]string {
	var slots [6]string
	i := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "---" {
			i++
			continue
		}
		if i < len(slots) {
			slots[i] += line + "\n"
		}
	}
	return slots
}

// parseNetDev 解析 /proc/net/dev 块：iface: rx_bytes ... tx_bytes ...（跳过 lo）。
func parseNetDev(s string) []proto.NetInfo {
	return proto.ParseNetDevLines(strings.Split(s, "\n"))
}

// dfSkipPrefixes df 结果中跳过的挂载点前缀（伪文件系统，与 agent 的 fstype 黑名单对应；
// df -P 看不到 fstype，overlay 由 /var/lib/docker 前缀兜住）。
var dfSkipPrefixes = []string{"/dev", "/proc", "/sys", "/run", "/snap", "/var/lib/docker"}

// parseDF 解析 df -kP 输出（1K 块，表头行因第二字段非数字自然跳过）。
// percent 用 used/(used+avail) 与 agent 的 statfs 语义一致（不含 root 保留区）。
func parseDF(s string) []proto.DiskInfo {
	var disks []proto.DiskInfo
	seen := make(map[string]bool)
	for _, line := range strings.Split(s, "\n") {
		f := fields(line)
		if len(f) < 6 {
			continue
		}
		blocks, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue // 表头（Filesystem 1024-blocks ...）
		}
		used, e1 := strconv.ParseUint(f[2], 10, 64)
		avail, e2 := strconv.ParseUint(f[3], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		mount := strings.Join(f[5:], " ")
		mount = strings.ReplaceAll(mount, `\040`, " ") // df -P 转义空格
		if blocks == 0 || mount == "" || seen[mount] {
			continue
		}
		skip := false
		for _, p := range dfSkipPrefixes {
			if mount == p || strings.HasPrefix(mount, p+"/") {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		seen[mount] = true
		// total = used+avail，与 agent 的 statfs 语义一致（不含 root 保留区）；
		// Device 先存 df 设备列原文，随后由 resolveDiskDevices 解析
		d := proto.DiskInfo{Mount: mount, Total: (used + avail) * 1024, Used: used * 1024, Device: f[0]}
		if d.Total > 0 {
			d.Percent = float64(d.Used) / float64(d.Total) * 100
		}
		disks = append(disks, d)
	}
	return disks
}

// fields 按任意空白切分（strings.Fields 别名，语义明确）。
func fields(s string) []string { return strings.Fields(s) }

// firstLine 取 trimmed 首行。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
