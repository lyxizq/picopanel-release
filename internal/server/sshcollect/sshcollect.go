// Package sshcollect 实现 SSH 直连采集：服务端通过 SSH 登录目标机执行只读命令
// （cat /proc、df、awk 计数），解析输出组装成与 agent 上报相同的 proto.Report，
// 下游存储/告警/展示无差别复用。
//
// 模型（Ansible 式，与 agent 拉取模式并存）：
//   - 连接只建一次，长期持有；每个采集周期在同一连接上开一个 session channel
//     执行命令（无重复握手/KEX，目标机不产生认证日志洪水）
//   - 命令失败即废弃连接，下个周期自动重连（exec 即探活，无需独立心跳）
//   - CPU jiffies 与网卡字节是累计计数器，差值由服务端计算（Prometheus 处理
//     counter 的方式），因此目标机不需要常驻任何进程
//   - 主机指纹 TOFU：首次连接记录（OnHostKey 回调），之后变更即拒绝（防中间人）
package sshcollect

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"lz-monitor/internal/proto"
)

// Options 描述一台 SSH 采集目标。
type Options struct {
	Addr      string // host:port
	User      string
	Password  string                    // 密码认证；非空 = 密码优先（兼容只开 kbd-int 的主机），私钥可用则按序兜底
	KeyPath   string                    // 私钥路径；空 = 依次尝试 ~/.ssh/id_ed25519、id_ecdsa、id_rsa（配了密码时允许都找不到）
	HostKey   string                    // 已记录指纹（SHA256:...）；空 = 首次连接，学到的指纹经 OnHostKey 上报
	OnHostKey func(id int64, fp string) // 指纹学习回调（首次/重置后触发一次）
	ServerID  int64
	Timeout   time.Duration // 单次执行超时（含建连）
}

// Client 是一台目标机的采集客户端（连接 + 上次计数器样本）。
type Client struct {
	opts Options

	mu   sync.Mutex
	conn *ssh.Client
	sys  *proto.SysInfo // 静态信息，首次成功连接取一次
	prev *sample        // 上一份累计计数器，用于差值

	// 测试注入：自定义拨号（生产为 nil，走 ssh.Dial）
	dial func(network, addr string) (net.Conn, error)
}

// sample 是差值计算所需的上次累计值。
type sample struct {
	at                time.Time
	cpuBusy, cpuTotal float64
	netRx, netTx      map[string]uint64
	ioStats           map[string]proto.DiskIOStat // 设备名 → IO 累计计数器（差值用）
}

// NewClient 创建采集客户端（不建连，首次 Collect 时懒连接）。
func NewClient(opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	return &Client{opts: opts}
}

// Close 立即断开连接（Manager 淘汰客户端或进程退出时调用）。
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeConn()
}

func (c *Client) closeConn() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// Collect 执行一次采集，返回可直接入库的 Report。
// 连接类错误会废弃连接（下轮自动重连）；解析类错误保留连接（重连也解决不了）。
func (c *Client) Collect() (*proto.Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		if err := c.connect(); err != nil {
			return nil, err
		}
	}
	if c.sys == nil {
		out, err := c.run(staticCmd)
		if err != nil {
			c.closeConn()
			return nil, err
		}
		sys, err := parseStatic(out)
		if err != nil {
			return nil, err
		}
		c.sys = sys
	}
	out, err := c.run(dynamicCmd)
	if err != nil {
		c.closeConn()
		return nil, err
	}
	rep, cpuBusy, cpuTotal, ioTicks, err := parseDynamic(out)
	if err != nil {
		return nil, err
	}
	rep.Sys = c.sys
	rep.TS = time.Now().Unix()
	c.applyDelta(rep, cpuBusy, cpuTotal, ioTicks)
	return rep, nil
}

// connect 建立 SSH 连接（鉴权 + TOFU 校验）。
func (c *Client) connect() error {
	conn, err := dialSSH(c.opts, c.dial, func(key ssh.PublicKey) error {
		return c.checkHostKey(key)
	})
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

// Dial 按配置建立一条独立 SSH 连接（Web 终端/SFTP 用，与采集连接无关）。
// TOFU 指纹校验/学习逻辑与采集完全一致；OnHostKey 置空时首连不记录指纹。
func Dial(opts Options) (*ssh.Client, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	return dialSSH(opts, nil, func(key ssh.PublicKey) error { return checkHostKeyOpts(opts, key) })
}

// dialSSH 建连公共路径：组装认证方式 → TCP 拨号 → SSH 握手。
// dial 为测试注入（nil = net.DialTimeout）；checkKey 由调用方实现 TOFU 语义。
func dialSSH(opts Options, dial func(network, addr string) (net.Conn, error), checkKey func(ssh.PublicKey) error) (*ssh.Client, error) {
	auth, keyDesc, err := buildAuth(opts)
	if err != nil {
		return nil, err
	}
	cfg := ssh.ClientConfig{
		User: opts.User,
		Auth: auth,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return checkKey(key)
		},
		Timeout: opts.Timeout,
	}
	if dial == nil {
		dial = func(network, addr string) (net.Conn, error) { return net.DialTimeout(network, addr, opts.Timeout) }
	}
	nc, err := dial("tcp", opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("连接 %s: %w", opts.Addr, err)
	}
	conn, chans, reqs, err := ssh.NewClientConn(nc, opts.Addr, &cfg)
	if err != nil {
		_ = nc.Close()
		return nil, fmt.Errorf("连接 %s（%s）: %w", opts.Addr, keyDesc, err)
	}
	return ssh.NewClient(conn, chans, reqs), nil
}

// buildAuth 组装认证方式：密码非空 → Password + KeyboardInteractive
// （不少主机禁 password 只开 kbd-int，固定应答密码兜底）；私钥可用 → PublicKeys
// 按序追加。显式配置了 KeyPath 必须加载成功；未配置时尝试默认路径，配了密码
// 则允许都找不到（纯密码主机），否则报错（与旧版行为一致）。
func buildAuth(opts Options) ([]ssh.AuthMethod, string, error) {
	var methods []ssh.AuthMethod
	var descs []string
	if opts.Password != "" {
		methods = append(methods,
			ssh.Password(opts.Password),
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = opts.Password
				}
				return answers, nil
			}))
		descs = append(descs, "密码")
	}
	if opts.KeyPath != "" {
		signer, _, err := loadSigner(opts.KeyPath)
		if err != nil {
			return nil, "", err
		}
		methods = append(methods, ssh.PublicKeys(signer))
		descs = append(descs, "私钥 "+opts.KeyPath)
	} else if signer, f, err := loadSigner(""); err == nil {
		methods = append(methods, ssh.PublicKeys(signer))
		descs = append(descs, "私钥 "+f)
	}
	if len(methods) == 0 {
		return nil, "", fmt.Errorf("未配置 SSH 认证方式：请填写密码或指定私钥路径")
	}
	return methods, strings.Join(descs, " + "), nil
}

// checkHostKey 实现 TOFU：未记录→学习并回调；已记录→必须一致，否则拒绝。
func (c *Client) checkHostKey(key ssh.PublicKey) error {
	return checkHostKeyOpts(c.opts, key)
}

// ErrHostKeyMismatch 主机指纹与 TOFU 记录不一致（疑似中间人攻击或目标机重装）。
// 终端链路用 errors.Is 识别后给前端下发 host_key_mismatch 错误码，
// 前端据此在连接失败遮罩上提供「重置指纹并重连」入口（376）。
var ErrHostKeyMismatch = errors.New("主机指纹不匹配")

// checkHostKeyOpts TOFU 公共实现（Client.checkHostKey 与 Dial 共用）。
func checkHostKeyOpts(opts Options, key ssh.PublicKey) error {
	fp := ssh.FingerprintSHA256(key)
	if opts.HostKey == "" {
		if opts.OnHostKey != nil {
			opts.OnHostKey(opts.ServerID, fp)
		}
		return nil
	}
	if opts.HostKey != fp {
		return fmt.Errorf("%w（已记录 %s，实际 %s），疑似中间人攻击或目标机重装；确认无误后可重置指纹后重连",
			ErrHostKeyMismatch, opts.HostKey, fp)
	}
	return nil
}

// run 在当前连接上开一个 session 执行命令，带超时兜底（超时强关 session）。
func (c *Client) run(cmd string) ([]byte, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("打开会话: %w", err)
	}
	defer sess.Close()

	timer := time.AfterFunc(c.opts.Timeout, func() { _ = sess.Close() })
	defer timer.Stop()
	out, err := sess.CombinedOutput(cmd)
	if err != nil {
		return nil, fmt.Errorf("执行命令: %w", err)
	}
	return out, nil
}

// applyDelta 用上次累计值计算 CPU 使用率、网卡速率与磁盘 IO（使用率 + 读/写速率，
// 首次采集为 0，下个周期起有效）。
func (c *Client) applyDelta(rep *proto.Report, cpuBusy, cpuTotal float64, ioStats map[string]proto.DiskIOStat) {
	now := time.Now()
	cur := &sample{at: now, netRx: make(map[string]uint64, len(rep.Nets)), netTx: make(map[string]uint64, len(rep.Nets)), ioStats: ioStats}
	for _, n := range rep.Nets {
		cur.netRx[n.Iface] = n.RxBytes
		cur.netTx[n.Iface] = n.TxBytes
	}
	if p := c.prev; p != nil {
		dt := now.Sub(p.at).Seconds()
		if dt >= 1 {
			if cpuTotal > p.cpuTotal {
				cpu := (cpuBusy - p.cpuBusy) / (cpuTotal - p.cpuTotal) * 100
				if cpu < 0 {
					cpu = 0
				} else if cpu > 100 {
					cpu = 100 // 计数器回绕/虚拟机迁移等异常时夹紧
				}
				rep.CPU = cpu
				for i := range rep.Nets {
					n := &rep.Nets[i]
					if v, ok := p.netRx[n.Iface]; ok && n.RxBytes >= v {
						n.RxBps = float64(n.RxBytes-v) / dt
					}
					if v, ok := p.netTx[n.Iface]; ok && n.TxBytes >= v {
						n.TxBps = float64(n.TxBytes-v) / dt
					}
				}
			}
			// 磁盘 IO：Δio_ticks(ms)/Δ时长(ms)×100 即 iostat %util；
			// Δ扇区 ×512B/Δ时长 即读/写速率（块设备扇区固定 512 字节）
			for i := range rep.Disks {
				d := &rep.Disks[i]
				if d.Device == "" {
					continue
				}
				t, ok := ioStats[d.Device]
				pt, pok := p.ioStats[d.Device]
				if ok && pok && t.Ticks >= pt.Ticks {
					util := float64(t.Ticks-pt.Ticks) / (dt * 1000) * 100
					if util > 100 {
						util = 100 // 多队列设备理论上可超 100，夹紧
					}
					d.IOUtil = util
					if t.ReadSectors >= pt.ReadSectors {
						d.IoRead = float64(t.ReadSectors-pt.ReadSectors) * 512 / dt
					}
					if t.WriteSectors >= pt.WriteSectors {
						d.IoWrite = float64(t.WriteSectors-pt.WriteSectors) * 512 / dt
					}
				}
			}
		}
	}
	c.prev = cur
}

// defaultKeyPaths 返回未配置 ssh_key_path 时的私钥尝试顺序。
func defaultKeyPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	names := []string{"id_ed25519", "id_ecdsa", "id_rsa"}
	paths := make([]string, 0, len(names))
	for _, n := range names {
		paths = append(paths, filepath.Join(home, ".ssh", n))
	}
	return paths
}

// loadSigner 读取并解析私钥：指定路径优先，未指定则尝试默认 ~/.ssh/id_*。
// 返回实际使用的文件名用于日志/报错。
func loadSigner(path string) (ssh.Signer, string, error) {
	paths := []string{path}
	if path == "" {
		paths = defaultKeyPaths()
	}
	var lastErr error
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			lastErr = err
			continue // 默认路径逐个尝试，都不存在再报错
		}
		signer, err := ssh.ParsePrivateKey(b)
		if err != nil {
			return nil, p, fmt.Errorf("解析私钥 %s: %w", p, err)
		}
		return signer, p, nil
	}
	if path == "" {
		return nil, "", fmt.Errorf("未找到 SSH 私钥（尝试了 ~/.ssh/id_ed25519、id_ecdsa、id_rsa），请在服务器配置里指定 ssh_key_path")
	}
	return nil, path, fmt.Errorf("读取私钥 %s: %w", path, lastErr)
}
