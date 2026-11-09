// Package proto 定义 agent 与 server 之间的上报数据契约，两端共用。
//
// 约定：内存/磁盘容量单位为字节，网络速率单位为 B/s（字节/秒，四种采集
// 模式统一口径；比特换算 ×8 一律在展示端完成），使用率的单位为百分比
// （0~100）。人读格式化一律在前端完成。
package proto

// SysInfo 是启动时采集一次、随每份上报携带的系统静态信息（约 150 字节）。
// 随报携带使得服务端 upsert 无状态：agent 重启、服务端重启都能自愈，
// 不需要单独的"首次注册"端点。
type SysInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`     // /etc/os-release 的 PRETTY_NAME
	Kernel   string `json:"kernel"` // /proc/sys/kernel/osrelease
	Arch     string `json:"arch"`   // runtime.GOARCH
	CPUModel string `json:"cpu_model"`
	CPUCores int    `json:"cpu_cores"`
	// AgentVersion 是采集端自身版本串（396 在线更新用；旧版 agent 无此字段，
	// 服务端显示 - 并对「未带版本的机器」跳过升级判定，不误报）。
	AgentVersion string `json:"agent_version,omitempty"`
}

// DiskInfo 是单个挂载点的磁盘使用情况。
type DiskInfo struct {
	Mount   string  `json:"mount"`
	FSType  string  `json:"fstype"`
	Total   uint64  `json:"total"` // 字节
	Used    uint64  `json:"used"`  // 字节
	Percent float64 `json:"percent"`
	Device  string  `json:"device,omitempty"` // 底层块设备名（/proc/diskstats 用，如 sda1、dm-0）
	// IO 三字段不带 omitempty、空闲时也要上报 0——否则字段缺失会被前端当成
	// 「旧版 agent 无此能力」显示 -，新 agent 空闲盘被误判成读不到数据
	IOUtil  float64 `json:"io_util"`      // IO 使用率（iostat %util 口径，0~100）；旧版 agent 无此字段
	IoRead  float64 `json:"io_read_bps"`  // 读速率 B/s；旧版 agent 无此字段
	IoWrite float64 `json:"io_write_bps"` // 写速率 B/s；旧版 agent 无此字段
}

// NetInfo 是单个网卡的流量情况。Bytes 为 /proc/net/dev 的累计计数器，
// Bps 为 agent 侧两次采样差值算出的速率。
type NetInfo struct {
	Iface   string  `json:"iface"`
	RxBytes uint64  `json:"rx_bytes"`
	TxBytes uint64  `json:"tx_bytes"`
	RxBps   float64 `json:"rx_bps"`
	TxBps   float64 `json:"tx_bps"`
}

// ListenPort 是一个监听中的端口（TCP LISTEN / UDP 已绑定未连接）。
// 同协议同端口的多个监听地址合并为一条（如 0.0.0.0 与 ::）。
type ListenPort struct {
	Proto string   `json:"proto"` // "tcp" / "udp"
	Port  uint16   `json:"port"`
	Addrs []string `json:"addrs"` // 监听地址（0.0.0.0 / 127.0.0.1 / :: …）
}

// TempPoint 是一个温度传感器采样（℃）。
type TempPoint struct {
	Sensor string  `json:"sensor"` // 标签：CPU / 块设备名（sda） / 主板芯片名
	Temp   float64 `json:"temp"`
}

// FanPoint 是一个风扇转速采样。
type FanPoint struct {
	Sensor string `json:"sensor"` // 标签：芯片名+风扇号（如 nct6798/fan1）
	RPM    int    `json:"rpm"`
}

// ContainerInfo 是一个 Docker 容器的当前状态快照（54 应用面板；只读自
// /var/run/docker.sock，不含统计）。State 为 Docker 原生小写态（running/
// exited/paused/created…），Status 为人读文案（如 "Up 3 days (healthy)"）。
type ContainerInfo struct {
	ID      string `json:"id"`      // 短 ID（12 位）
	Name    string `json:"name"`    // 首个容器名（去前导 /）
	Image   string `json:"image"`   // 镜像：标签
	State   string `json:"state"`   // running / exited / paused / created / restarting…
	Status  string `json:"status"`  // Docker 的人读状态文案
	Created int64  `json:"created"` // 创建时间（unix 秒）
}

// VMInfo 是一台虚拟机的当前状态快照（virsh list --all 口径；fnOS 官方
// 虚拟机 app 即 KVM/libvirt，域名是 libvirt 域名）。ID 为 virsh 的数字域
// ID，关机态为空。
type VMInfo struct {
	ID    string `json:"id"`    // 运行中=数字域 ID；关机=空
	Name  string `json:"name"`  // libvirt 域名
	State string `json:"state"` // virsh 原生文案（running / shut off / paused …）
}

// Report 是一份完整的上报。TS 由 agent 填写仅供排查，
// 服务端存储与告警判定一律使用服务端接收时间。
type Report struct {
	TS          int64           `json:"ts"`
	CPU         float64         `json:"cpu_percent"`
	MemTotal    uint64          `json:"mem_total"`
	MemUsed     uint64          `json:"mem_used"`
	MemPercent  float64         `json:"mem_percent"`
	SwapPercent float64         `json:"swap_percent"`
	Load1       float64         `json:"load1"`
	Load5       float64         `json:"load5"`
	Load15      float64         `json:"load15"`
	Uptime      uint64          `json:"uptime"`
	TCPEst      int             `json:"tcp_established"`
	Disks       []DiskInfo      `json:"disks"`
	Nets        []NetInfo       `json:"nets"`
	WanIfaces   []string        `json:"wan_ifaces,omitempty"` // 默认路由出口网口（外网流量账本缺省统计对象）；旧版 agent / node 模式无此字段（nil）
	Listens     []ListenPort    `json:"listens"`              // 旧版 agent 无此字段（nil），端口告警对旧 agent 自动跳过
	Temps       []TempPoint     `json:"temps,omitempty"`      // 旧版 agent 无此字段（nil）；温度 ℃
	Fans        []FanPoint      `json:"fans,omitempty"`       // 旧版 agent 无此字段（nil）；转速 RPM
	Containers  []ContainerInfo `json:"containers"`           // Docker 容器快照（54）；nil=本机无 Docker（旧版 agent 同为 null）
	VMs         []VMInfo        `json:"vms"`                  // 虚拟机快照（54）；nil=本机无 virsh/libvirt
	Sys         *SysInfo        `json:"sys,omitempty"`
}
