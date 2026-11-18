package collect

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lz-monitor/internal/proto"
)

// ThermalCollector 采集 CPU 温度 / 硬盘温度 / 风扇转速。
//
// 数据源：/sys/class/hwmon 内核接口（coretemp/k10temp/nvme/drivetemp 等，
// 绝大多数发行版开箱即读，无需安装 lm-sensors）。drivetemp/nvme 模块已加载
// 时硬盘温度同样来自 hwmon（块设备名标签）；未加载的机器没有盘温（历史上的
// smartctl 兜底路线经 v1.0.34/35 两轮发布现场反复后于 59 整体拆除，勿复活）。
// 全部读不到（常见于无传感器的虚拟机）时留空，服务端按「无传感器数据」展示，
// 不报错不告警。
//
// 采集器无状态（每轮全量快照），顺序主循环调用无需加锁。
type ThermalCollector struct {
	sysRoot string // 生产=/sys；测试注入 fixture 目录
}

func NewThermalCollector() *ThermalCollector {
	return &ThermalCollector{sysRoot: "/sys"}
}

func (c *ThermalCollector) Name() string { return "thermal" }

func (c *ThermalCollector) Collect(r *proto.Report) error {
	r.Temps, r.Fans = c.readHwmon()
	return nil
}

// readHwmon 扫描 /sys/class/hwmon/hwmon*/，按芯片归类：
//   - CPU 温度：coretemp（Intel）/ k10temp、k8temp、zenpower（AMD）/ cpu_thermal、
//     soc_thermal（ARM）整芯片，及其他芯片里 label 以 CPU/Package/Core/Tctl/Tdie
//     开头的通道；CPU 芯片多核多通道收敛为最热一路，标签统一 CPU
//   - 硬盘温度：drivetemp（SATA）与 nvme*，经 device 符号链接对应到块设备名
//     （sda / nvme0n1），对应不上回落芯片名
//   - 其余（acpitz / nct67xx / pch_* 等）按「芯片名 + 通道标签」保留为主板温度
//   - fanN_input 归风扇（RPM>0 才收，停转不报）
func (c *ThermalCollector) readHwmon() ([]proto.TempPoint, []proto.FanPoint) {
	var temps []proto.TempPoint
	var fans []proto.FanPoint
	chips, err := os.ReadDir(filepath.Join(c.sysRoot, "class", "hwmon"))
	if err != nil {
		return nil, nil
	}
	for _, ch := range chips {
		dir := filepath.Join(c.sysRoot, "class", "hwmon", ch.Name())
		chip := strings.TrimSpace(readFirst(filepath.Join(dir, "name")))
		if chip == "" {
			chip = ch.Name()
		}
		isCPU := isCPUChip(chip)
		isDisk := chip == "drivetemp" || strings.HasPrefix(chip, "nvme")
		devName := ""
		if isDisk {
			devName = c.blockNameFor(dir)
		}
		for n := 1; ; n++ {
			raw := readFirst(filepath.Join(dir, "temp"+strconv.Itoa(n)+"_input"))
			if raw == "" {
				break
			}
			milli, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil || milli <= -60000 || milli >= 200000 { // -60~200℃ 合理域
				continue
			}
			label := readFirst(filepath.Join(dir, "temp"+strconv.Itoa(n)+"_label"))
			temps = append(temps, proto.TempPoint{
				Sensor: classifyTemp(chip, label, isCPU, isDisk, devName),
				Temp:   round1(milli / 1000),
			})
		}
		for n := 1; ; n++ {
			raw := readFirst(filepath.Join(dir, "fan"+strconv.Itoa(n)+"_input"))
			if raw == "" {
				break
			}
			rpm, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil || rpm <= 0 {
				continue
			}
			label := readFirst(filepath.Join(dir, "fan"+strconv.Itoa(n)+"_label"))
			name := chip
			if label != "" {
				name = label
			} else if n > 1 {
				name = chip + "/fan" + strconv.Itoa(n)
			}
			fans = append(fans, proto.FanPoint{Sensor: name, RPM: rpm})
		}
	}
	temps = dedupeTemps(temps)
	sort.Slice(temps, func(i, j int) bool { return temps[i].Sensor < temps[j].Sensor })
	return temps, fans
}

// classifyTemp 给一个 hwmon 温度通道定展示标签。
func classifyTemp(chip, label string, isCPU, isDisk bool, devName string) string {
	switch {
	case isDisk && devName != "":
		return devName // sda / nvme0n1 …
	case isDisk:
		return chip // 对应不上块设备时回落芯片名（drivetemp / nvme0）
	case isCPU, isCPUTempLabel(chip, label):
		return "CPU"
	case label != "":
		return chip + " " + label
	default:
		return chip
	}
}

// dedupeTemps 同名传感器取最高值（CPU 芯片多核多通道已统一标签为 CPU，
// 收敛成 Package/Tctl 峰值一路，对告警更有意义）。
func dedupeTemps(temps []proto.TempPoint) []proto.TempPoint {
	best := map[string]proto.TempPoint{}
	for _, t := range temps {
		if old, ok := best[t.Sensor]; !ok || t.Temp > old.Temp {
			best[t.Sensor] = t
		}
	}
	out := make([]proto.TempPoint, 0, len(best))
	for _, t := range best {
		out = append(out, t)
	}
	return out
}

func isCPUChip(chip string) bool {
	switch chip {
	case "coretemp", "k10temp", "k8temp", "zenpower", "cpu_thermal", "soc_thermal", "x86_pkg_temp":
		return true
	}
	return false
}

// TempLabel 按芯片名与温度通道名（tempN）定展示标签，agent 与 node_exporter
// 两种采集模式共用同一口径：CPU 芯片统一 CPU，硬盘/其余芯片用芯片名
// （node_exporter 拿不到通道 label 文本与块设备对应关系，细粒度到此为止）。
func TempLabel(chip, channel string) string {
	switch {
	case isCPUChip(chip), isCPUTempLabel(chip, channel):
		return "CPU"
	default:
		return chip
	}
}

// isCPUTempLabel 非专用 CPU 芯片里按通道标签识别 CPU 温度：
// coretemp「Package id 0」/「Core 0」、k10temp「Tctl/Tdie」、ARM「CPU」。
func isCPUTempLabel(chip, label string) bool {
	fields := strings.Fields(label) // 纯空白标签 Fields 为空，直接取 [0] 会越界
	if len(fields) == 0 {
		return false
	}
	head := strings.ToLower(fields[0])
	return head == "cpu" || head == "package" || head == "core" || head == "tctl" || head == "tdie"
}

// blockNameFor 解析 hwmon 芯片对应块设备名（sda / nvme0n1 …），匹配不上返回空串。
//
// drivetemp：hwmon 的 device 与 /sys/block/sda/device 指向同一 scsi_device 目录，
// realpath 直配；NVMe：hwmon 挂在控制器（nvme0）上，而 /sys/block/nvme0n1 的
// device 指向命名空间目录自身，直配不上，退一步按「块设备位于 hwmon 设备
// 目录之下」的前缀关系匹配。
func (c *ThermalCollector) blockNameFor(hwmonDir string) string {
	real, err := filepath.EvalSymlinks(filepath.Join(hwmonDir, "device"))
	if err != nil {
		return ""
	}
	blocks, err := os.ReadDir(filepath.Join(c.sysRoot, "block"))
	if err != nil {
		return ""
	}
	for _, b := range blocks {
		bd := filepath.Join(c.sysRoot, "block", b.Name())
		if breal, err := filepath.EvalSymlinks(filepath.Join(bd, "device")); err == nil && breal == real {
			return b.Name()
		}
		breal, err := filepath.EvalSymlinks(bd)
		if err == nil && strings.HasPrefix(breal, real+string(filepath.Separator)) {
			return b.Name()
		}
	}
	return ""
}

func readFirst(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
