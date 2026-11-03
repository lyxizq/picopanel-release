// Package nodecollect 是第四种采集方式的采集端：拉取 node_exporter 的
// /metrics（Prometheus 文本格式）并映射为本项目 proto.Report，指标范围与
// 本项目 agent 完全同口径（白名单解析，不引入多余指标）。
//
// CPU/网卡/磁盘 IO 在 node_exporter 里是累计 counter（*_seconds_total /
// *_bytes_total），由本包保存上一份样本在服务端算差值——与 SSH 直连模式
// 同一先例；服务端重启后首轮差值指标为 0，下周期起有效。
// 监听端口 node_exporter 无对应指标，Listens 保持 nil（端口告警自动跳过，
// 与关闭 collect_listens 的 agent 同语义）。
package nodecollect

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/agent/collect"
	"lz-monitor/internal/proto"
)

// fsBlacklist 过滤伪文件系统与容器运行时挂载（与 agent collect/disk.go 同一清单）。
var fsBlacklist = map[string]bool{
	"sysfs": true, "proc": true, "devtmpfs": true, "devpts": true,
	"tmpfs": true, "squashfs": true, "overlay": true, "cgroup": true,
	"cgroup2": true, "efivarfs": true, "fusectl": true, "mqueue": true,
	"hugetlbfs": true, "debugfs": true, "tracefs": true, "configfs": true,
	"securityfs": true, "pstore": true, "bpf": true, "autofs": true,
	"binfmt_misc": true, "rpc_pipefs": true, "fuse.snapfuse": true,
	"iso9660": true, "udf": true, "ramfs": true,
}

// fsRow 是一个挂载点的容量样本（node_filesystem_* 两条指标的合并）。
type fsRow struct {
	device string // /dev/sda1 形式（对齐 io 指标的 device 时剥掉 /dev/ 前缀）
	fstype string
	size   float64
	avail  float64
}

// Sample 是一次 /metrics 解析结果：只保留白名单指标映射所需字段。
type Sample struct {
	At time.Time

	cpuTotal float64            // 全核全 mode 秒数总和
	cpuIdle  float64            // idle+iowait（非 busy），busy = total-idle
	cpuCores int                // distinct cpu label 数（SysInfo 用）
	cpuSeen  map[string]bool    // cpu label 去重
	mem      map[string]float64 // node_memory_*_bytes → 值
	fs       map[string]*fsRow  // mountpoint → 行
	netRx    map[string]float64 // device → receive_bytes_total
	netTx    map[string]float64 // device → transmit_bytes_total
	ioTime   map[string]float64 // device → disk_io_time_seconds_total
	ioRead   map[string]float64 // device → disk_read_bytes_total
	ioWrite  map[string]float64 // device → disk_written_bytes_total

	load1, load5, load15 float64
	bootTime             float64
	tcpEst               float64
	uname                map[string]string  // node_uname_info labels
	osPretty             string             // node_os_info 的 pretty_name（有则填）
	hwmonTemp            map[string]float64 // "chip\x00sensor" → 温度 ℃（node_hwmon_temp_celsius）
}

// Client 保存上一份样本用于差值计算（每台 node 模式服务器一个，由 scrape
// 管理器持有；同一服务器同一时刻只有一个抓取 goroutine，无需加锁）。
type Client struct {
	prev *Sample
}

// Parse 解析 /metrics 文本（白名单外的行直接跳过，含全部 # HELP/# TYPE 注释）。
func Parse(r io.Reader) (*Sample, error) {
	s := &Sample{
		At:        time.Now(),
		cpuSeen:   map[string]bool{},
		mem:       map[string]float64{},
		fs:        map[string]*fsRow{},
		netRx:     map[string]float64{},
		netTx:     map[string]float64{},
		ioTime:    map[string]float64{},
		ioRead:    map[string]float64{},
		ioWrite:   map[string]float64{},
		uname:     map[string]string{},
		hwmonTemp: map[string]float64{},
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		name, labels, val, ok := splitMetric(line)
		if !ok {
			continue
		}
		switch name {
		case "node_cpu_seconds_total":
			mode := labels["mode"]
			s.cpuTotal += val
			if mode == "idle" || mode == "iowait" {
				s.cpuIdle += val
			}
			if c := labels["cpu"]; !s.cpuSeen[c] {
				s.cpuSeen[c] = true
				s.cpuCores++
			}
		case "node_load1":
			s.load1 = val
		case "node_load5":
			s.load5 = val
		case "node_load15":
			s.load15 = val
		case "node_boot_time_seconds":
			s.bootTime = val
		case "node_netstat_Tcp_CurrEstab":
			s.tcpEst = val
		case "node_memory_MemTotal_bytes", "node_memory_MemAvailable_bytes",
			"node_memory_MemFree_bytes", "node_memory_Buffers_bytes",
			"node_memory_Cached_bytes", "node_memory_SwapTotal_bytes",
			"node_memory_SwapFree_bytes":
			s.mem[name] = val
		case "node_filesystem_size_bytes":
			row := s.fsRow(labels["mountpoint"], labels["device"], labels["fstype"])
			if row != nil {
				row.size = val
			}
		case "node_filesystem_avail_bytes":
			row := s.fsRow(labels["mountpoint"], labels["device"], labels["fstype"])
			if row != nil {
				row.avail = val
			}
		case "node_disk_io_time_seconds_total":
			s.ioTime[diskDevKey(labels["device"])] = val
		case "node_disk_read_bytes_total":
			s.ioRead[diskDevKey(labels["device"])] = val
		case "node_disk_written_bytes_total":
			s.ioWrite[diskDevKey(labels["device"])] = val
		case "node_network_receive_bytes_total":
			if d := labels["device"]; d != "lo" {
				s.netRx[d] = val
			}
		case "node_network_transmit_bytes_total":
			if d := labels["device"]; d != "lo" {
				s.netTx[d] = val
			}
		case "node_uname_info":
			for k, v := range labels {
				s.uname[k] = v
			}
		case "node_os_info":
			if p := labels["pretty_name"]; p != "" {
				s.osPretty = p
			}
		case "node_hwmon_temp_celsius":
			if chip := labels["chip"]; chip != "" {
				s.hwmonTemp[chip+"\x00"+labels["sensor"]] = val
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取 /metrics: %w", err)
	}
	if len(s.mem) == 0 && s.cpuTotal == 0 && len(s.fs) == 0 {
		return nil, fmt.Errorf("响应里没有可识别的 node_* 指标")
	}
	return s, nil
}

// fsRow 取挂载点行（fstype 命中黑名单或空挂载点返回 nil）。
func (s *Sample) fsRow(mount, device, fstype string) *fsRow {
	if mount == "" || fsBlacklist[fstype] {
		return nil
	}
	row := s.fs[mount]
	if row == nil {
		row = &fsRow{device: device, fstype: fstype}
		s.fs[mount] = row
	}
	return row
}

// splitMetric 拆出一行 `name{labels} value [ts]`；返回 name、label 表与数值。
// 有 label 时先定位 {} 整段再取其后的 value——label 值本身可含空格
// （如 pretty_name="Ubuntu 22.04.3 LTS"），不能按首个空白切分。
func splitMetric(line string) (name string, labels map[string]string, val float64, ok bool) {
	labels = map[string]string{}
	var rest string
	if i := strings.IndexByte(line, '{'); i > 0 {
		j := strings.IndexByte(line, '}')
		if j < i {
			return "", nil, 0, false
		}
		name, labels, rest = line[:i], parseLabels(line[i+1:j]), strings.TrimSpace(line[j+1:])
	} else {
		sp := strings.IndexAny(line, " \t")
		if sp < 0 {
			return "", nil, 0, false
		}
		name, rest = line[:sp], strings.TrimSpace(line[sp+1:])
	}
	if rest == "" {
		return "", nil, 0, false
	}
	// value 是首个空白分隔 token（其后可能有可选时间戳，忽略）
	tok := rest
	if i := strings.IndexAny(tok, " \t"); i >= 0 {
		tok = tok[:i]
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil || math.IsNaN(f) {
		return "", nil, 0, false
	}
	return name, labels, f, true
}

// parseLabels 解析 `k="v",k2="v2"`（值做 \" \\ \n 反转义；实际 label 值
// 极少含转义，保守处理即可）。
func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(part[:eq])
		v := strings.TrimSpace(part[eq+1:])
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
			v = strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n").Replace(v)
		}
		out[k] = v
	}
	return out
}

// Report 把样本映射为 proto.Report 并对 counter 类指标做差值
// （防护口径与 sshcollect.applyDelta 一致：dt≥1s 才算、回绕守卫、夹紧 0~100）。
func (c *Client) Report(s *Sample, now time.Time) *proto.Report {
	rep := &proto.Report{TS: now.Unix()}

	// 内存：used = total − available（缺失回退 MemFree+Buffers+Cached，同 agent 口径）
	total := s.mem["node_memory_MemTotal_bytes"]
	avail, ok := s.mem["node_memory_MemAvailable_bytes"]
	if !ok {
		avail = s.mem["node_memory_MemFree_bytes"] +
			s.mem["node_memory_Buffers_bytes"] + s.mem["node_memory_Cached_bytes"]
	}
	if total > 0 {
		rep.MemTotal = uint64(total)
		if used := total - avail; used > 0 {
			rep.MemUsed = uint64(used)
		}
		rep.MemPercent = (total - avail) / total * 100
	}
	if st := s.mem["node_memory_SwapTotal_bytes"]; st > 0 {
		rep.SwapPercent = (st - s.mem["node_memory_SwapFree_bytes"]) / st * 100
	}
	rep.Load1, rep.Load5, rep.Load15 = s.load1, s.load5, s.load15
	if s.bootTime > 0 && now.Unix() > int64(s.bootTime) {
		rep.Uptime = uint64(float64(now.Unix()) - s.bootTime)
	}
	rep.TCPEst = int(s.tcpEst)

	// 磁盘：percent = used/(used+avail)（root 保留区不算满，同 agent 口径）；
	// 同一底层设备多挂载点只留第一个（同 agent）。
	seenDev := map[string]bool{}
	for _, mount := range sortedKeys(s.fs) {
		row := s.fs[mount]
		used := row.size - row.avail
		if row.size <= 0 || used < 0 {
			continue
		}
		if seenDev[row.device] {
			continue
		}
		seenDev[row.device] = true
		d := proto.DiskInfo{
			Mount: mount, FSType: row.fstype,
			Total: uint64(row.size), Used: uint64(used),
			Percent: used / (used + row.avail) * 100,
			Device:  diskDevKey(row.device),
		}
		rep.Disks = append(rep.Disks, d)
	}

	// 网卡：累计 counter 先落到条目，速率由差值补
	for _, dev := range sortedKeys(s.netRx) {
		n := proto.NetInfo{Iface: dev, RxBytes: uint64(s.netRx[dev])}
		if v, ok := s.netTx[dev]; ok {
			n.TxBytes = uint64(v)
		}
		rep.Nets = append(rep.Nets, n)
	}

	// 差值：CPU 使用率、网卡速率、磁盘 IO 使用率（首轮为 0）
	if p := c.prev; p != nil {
		dt := s.At.Sub(p.At).Seconds()
		if dt >= 1 {
			if dTotal := s.cpuTotal - p.cpuTotal; dTotal > 0 {
				cpu := (s.cpuTotal - s.cpuIdle - (p.cpuTotal - p.cpuIdle)) / dTotal * 100
				if cpu < 0 {
					cpu = 0
				} else if cpu > 100 {
					cpu = 100 // 计数器回绕/热插核等异常时夹紧
				}
				rep.CPU = cpu
			}
			for i := range rep.Nets {
				n := &rep.Nets[i]
				if pv, ok := p.netRx[n.Iface]; ok && s.netRx[n.Iface] >= pv {
					n.RxBps = (s.netRx[n.Iface] - pv) / dt
				}
				if pv, ok := p.netTx[n.Iface]; ok && s.netTx[n.Iface] >= pv {
					n.TxBps = (s.netTx[n.Iface] - pv) / dt
				}
			}
			// IO：%util = Δio_time(秒) / Δ时长(秒) × 100（iostat 口径）；
			// 读/写速率 = Δread/written_bytes / Δ时长（B/s）。
			// 挂载点在分区上而 io 指标只有整盘条目时回退整盘（NVMe 分区不进
			// /proc/diskstats，node_exporter 透传后只剩 nvme0n1 整盘）。
			for i := range rep.Disks {
				d := &rep.Disks[i]
				ck := ioKey(d.Device, s.ioTime)
				pv, pok := p.ioTime[ck]
				if ck == "" || !pok {
					continue
				}
				if cur := s.ioTime[ck]; cur >= pv {
					util := (cur - pv) / dt * 100
					if util > 100 {
						util = 100 // 多队列设备理论上可超 100，夹紧
					}
					d.IOUtil = util
				}
				if pk := ioKey(d.Device, s.ioRead); pk != "" {
					if pv, ok := p.ioRead[pk]; ok && s.ioRead[pk] >= pv {
						d.IoRead = (s.ioRead[pk] - pv) / dt
					}
				}
				if pk := ioKey(d.Device, s.ioWrite); pk != "" {
					if pv, ok := p.ioWrite[pk]; ok && s.ioWrite[pk] >= pv {
						d.IoWrite = (s.ioWrite[pk] - pv) / dt
					}
				}
			}
		}
	}
	c.prev = s

	// 温度：hwmon 通道按 agent 同款口径打标签（CPU 芯片统一 CPU），同标签
	// 取最高（coretemp 每核一路 → Package 峰值一路）；无样本保持 nil。
	// node_exporter 无风扇指标，Fans 恒 nil。
	if len(s.hwmonTemp) > 0 {
		best := map[string]float64{}
		for key, v := range s.hwmonTemp {
			chip, sensor, _ := strings.Cut(key, "\x00")
			label := collect.TempLabel(chip, sensor)
			if v > best[label] {
				best[label] = v
			}
		}
		for _, label := range sortedKeys(best) {
			rep.Temps = append(rep.Temps, proto.TempPoint{Sensor: label, Temp: best[label]})
		}
	}

	rep.Sys = &proto.SysInfo{
		Hostname: s.uname["nodename"],
		OS:       s.osPretty,
		Kernel:   s.uname["release"],
		Arch:     s.uname["machine"],
		CPUCores: s.cpuCores,
	}
	return rep
}

// diskDevKey 规范化文件系统指标里的设备名（/dev/sda1 → sda1）。
// /dev/mapper/vg-lv 只能剥到卷名 vg-lv：mapper 软链接指向的内核名（dm-0）
// 需要在目标机 readlink 才能得知，/metrics 文本里没有对应关系——此类
// 挂载点的 IO 匹配不到条目、保持 0（agent 模式无此限制，见 README 取舍说明）。
func diskDevKey(dev string) string {
	return strings.TrimPrefix(strings.TrimPrefix(dev, "/dev/"), "mapper/")
}

// ioKey 找挂载点设备在 io 表中的累计时间键：先精确匹配（sda1=sda1），
// 再剥分区号回退整盘（nvme0n1p1→nvme0n1、sda1→sda、mmcblk0p2→mmcblk0）。
// NVMe 分区不进 /proc/diskstats，node_exporter 的 io 指标常只有整盘条目；
// dm-0/md0 这类剥完查不到的维持空（不会错配到别的设备）。找不到返回 ""。
func ioKey(dev string, io map[string]float64) string {
	if _, ok := io[dev]; ok {
		return dev
	}
	i := len(dev)
	for i > 0 && dev[i-1] >= '0' && dev[i-1] <= '9' {
		i--
	}
	if i == 0 || i == len(dev) {
		return ""
	}
	if dev[i-1] == 'p' {
		i-- // nvme0n1p1 / mmcblk0p2 的分区号带 p 前缀，连 p 一起剥
		if i == 0 {
			return ""
		}
	}
	if base := dev[:i]; base != "" {
		if _, ok := io[base]; ok {
			return base
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
