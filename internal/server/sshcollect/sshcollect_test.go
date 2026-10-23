package sshcollect

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"lz-monitor/internal/proto"
)

const staticFixture = `=H=
web-01
=K=
5.15.0-91-generic
=O=
"Ubuntu 22.04.5 LTS"
=A=
x86_64
=M=
Intel(R) Xeon(R) Platinum 8269CY CPU @ 2.50GHz
=C=
8
`

const dynamicFixture = `=T=
123456.78 234567.89
=S=
cpu  74608 2520 24482 14670868 106056 0 17823 0 0 0
=MI=
MemTotal:       16384000 kB
MemFree:         1000000 kB
MemAvailable:    9000000 kB
Buffers:          200000 kB
Cached:          7000000 kB
SwapTotal:       2048000 kB
SwapFree:        1024000 kB
=L=
0.52 0.58 0.59 1/487 12345
=ND=
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999999 1000 0 0 0 0 0 0 999999 1000 0 0 0 0 0 0
  eth0: 1000000 2000 0 0 0 0 0 0 500000 1500 0 0 0 0 0 0
=DF=
Filesystem     1024-blocks      Used Available Capacity Mounted on
/dev/vda1       41152812  12345678  26684932      32% /
tmpfs             8192000      1024   8190976       1% /dev/shm
/dev/mapper/vg--data 103080888 52428800 48003632  53% /data
overlay          41152812  12345678  26684932      32% /var/lib/docker/overlay2/abc/merged
=DS=
   8       0 sda 1 2 3 4 5 6 7 8 9 5000 6000 0 0 0 0
 252       0 dm-0 1 2 3 4 5 6 7 8 9 2000 3000 0 0 0 0
 253       1 vda1 1 2 3 4 5 6 7 8 9 100 200 0 0 0 0
=DM=
lrwxrwxrwx 1 0 0 7 Sep  1 12:00 vg--data -> ../dm-0
=TCP=
42
=LS=
tcp 00000000:0016
tcp 0B00000A:1F90
tcp 00000000000000000000000000000000:0016
udp 00000000:0035
udp 00000000000000000000000001000000:0035
=CG=
0::/
---
0::/
---
max
---
536870912
---

`

func TestParseStatic(t *testing.T) {
	sys, err := parseStatic([]byte(staticFixture))
	if err != nil {
		t.Fatal(err)
	}
	if sys.Hostname != "web-01" || sys.Kernel != "5.15.0-91-generic" || sys.Arch != "x86_64" {
		t.Fatalf("静态信息解析错误: %+v", sys)
	}
	if sys.OS != "Ubuntu 22.04.5 LTS" { // 引号应被剥掉
		t.Fatalf("OS 引号未剥离: %q", sys.OS)
	}
	if sys.CPUCores != 8 {
		t.Fatalf("核数错误: %d", sys.CPUCores)
	}
}

func TestParseDynamic(t *testing.T) {
	rep, busy, total, ioStats, err := parseDynamic([]byte(dynamicFixture))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Uptime != 123456 {
		t.Fatalf("uptime = %d", rep.Uptime)
	}
	// busy = 74608+2520+24482+0+17823+0；idle = 14670868+106056
	if want := 119433.0; busy != want {
		t.Fatalf("cpuBusy = %v, want %v", busy, want)
	}
	if want := 14896357.0; total != want {
		t.Fatalf("cpuTotal = %v, want %v", total, want)
	}
	// used = 16384000-9000000 kB（Prometheus/node_exporter 口径 MemTotal-MemAvailable）
	if rep.MemUsed != 7384000*1024 || rep.MemTotal != 16384000*1024 {
		t.Fatalf("内存错误: used=%d total=%d", rep.MemUsed, rep.MemTotal)
	}
	if rep.MemPercent < 45.06 || rep.MemPercent > 45.08 {
		t.Fatalf("内存使用率 = %v", rep.MemPercent)
	}
	if rep.SwapPercent != 50 {
		t.Fatalf("swap = %v", rep.SwapPercent)
	}
	if rep.Load1 != 0.52 || rep.Load5 != 0.58 || rep.Load15 != 0.59 {
		t.Fatalf("负载错误: %v/%v/%v", rep.Load1, rep.Load5, rep.Load15)
	}
	if len(rep.Nets) != 1 || rep.Nets[0].Iface != "eth0" ||
		rep.Nets[0].RxBytes != 1000000 || rep.Nets[0].TxBytes != 500000 {
		t.Fatalf("网卡解析错误（lo 应被跳过）: %+v", rep.Nets)
	}
	// 磁盘：/ 与 /data 保留；tmpfs(/dev/shm) 与 overlay(容器merged) 应被过滤
	if len(rep.Disks) != 2 || rep.Disks[0].Mount != "/" || rep.Disks[1].Mount != "/data" {
		t.Fatalf("磁盘解析错误: %+v", rep.Disks)
	}
	if rep.Disks[1].Total != (52428800+48003632)*1024 {
		t.Fatalf("/data total = %d", rep.Disks[1].Total)
	}
	// 设备名：df 设备列 → diskstats 名（mapper 软链接经 =DM= 解析为 dm-0）
	if rep.Disks[0].Device != "vda1" || rep.Disks[1].Device != "dm-0" {
		t.Fatalf("磁盘设备名解析错误: %q / %q", rep.Disks[0].Device, rep.Disks[1].Device)
	}
	if ioStats["vda1"].Ticks != 100 || ioStats["dm-0"].Ticks != 2000 {
		t.Fatalf("diskstats 解析错误: %+v", ioStats)
	}
	if rep.TCPEst != 42 {
		t.Fatalf("tcp = %d", rep.TCPEst)
	}
	// 监听端口：tcp 22 双栈地址合并、tcp 8080 绑定 10.0.0.11、udp 53（环回 ::1 被滤）
	if len(rep.Listens) != 3 {
		t.Fatalf("监听端口数 = %d, 期望 3: %+v", len(rep.Listens), rep.Listens)
	}
	if rep.Listens[0].Port != 22 || len(rep.Listens[0].Addrs) != 2 ||
		rep.Listens[0].Addrs[0] != "0.0.0.0" || rep.Listens[0].Addrs[1] != "::" {
		t.Fatalf("tcp/22 合并错误: %+v", rep.Listens[0])
	}
	if rep.Listens[1].Port != 8080 || rep.Listens[1].Addrs[0] != "10.0.0.11" {
		t.Fatalf("tcp/8080 错误: %+v", rep.Listens[1])
	}
	if rep.Listens[2].Proto != "udp" || rep.Listens[2].Port != 53 || len(rep.Listens[2].Addrs) != 1 {
		t.Fatalf("udp/53 错误（环回应被过滤）: %+v", rep.Listens[2])
	}
}

// TestParseDynamicContainerMem 容器环境：meminfo 为宿主机口径时改用 cgroup v2 限额。
func TestParseDynamicContainerMem(t *testing.T) {
	out := `=T=
1.0 2.0
=S=
cpu  1 1 1 10 1 1 1 1 0 0
=MI=
MemTotal: 16384000 kB
MemAvailable: 9000000 kB
=CG=
0::/
---
0::/
---
1073741824
---
536870912
---

`
	rep, _, _, _, err := parseDynamic([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	// 限额 1GB、cgroup 用量 512MB → total=1GB used=512MB（而非宿主机的 16G/7G）
	if rep.MemTotal != 1073741824 || rep.MemUsed != 536870912 {
		t.Fatalf("容器内存修正错误: total=%d used=%d", rep.MemTotal, rep.MemUsed)
	}
	if rep.MemPercent != 50 {
		t.Fatalf("容器内存使用率 = %v, want 50", rep.MemPercent)
	}
}

func TestParseDFEscapedMount(t *testing.T) {
	out := `=T=
1.0 2.0
=S=
cpu  1 1 1 10 1 1 1 1 0 0
=MI=
MemTotal: 1000 kB
MemAvailable: 500 kB
=DF=
Filesystem     1024-blocks   Used Available Capacity Mounted on
/dev/vda1        41152812 1234567  26684932      32% /Volumes/My\040Data
`
	rep, _, _, _, err := parseDynamic([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Disks) != 1 || rep.Disks[0].Mount != "/Volumes/My Data" {
		t.Fatalf("\\040 未还原: %+v", rep.Disks)
	}
}

// TestApplyDelta 验证服务端差值计算：CPU jiffies、网卡字节速率与磁盘 IO（使用率 + 读/写速率）。
func TestApplyDelta(t *testing.T) {
	c := &Client{prev: &sample{
		at:      time.Now().Add(-10 * time.Second),
		cpuBusy: 1000, cpuTotal: 4000,
		netRx:   map[string]uint64{"eth0": 100000},
		netTx:   map[string]uint64{"eth0": 50000},
		ioStats: map[string]proto.DiskIOStat{"sda1": {Ticks: 1000, ReadSectors: 10000, WriteSectors: 5000}},
	}}
	rep := &proto.Report{
		Nets:  []proto.NetInfo{{Iface: "eth0", RxBytes: 110000, TxBytes: 55000}},
		Disks: []proto.DiskInfo{{Mount: "/", Device: "sda1"}},
	}
	// 10s 内 sda1 io_ticks 增 2000ms → 20%；读扇区 +2000×512B → 102400 B/s；写扇区 +1000 → 51200 B/s
	c.applyDelta(rep, 1250, 5000, map[string]proto.DiskIOStat{"sda1": {Ticks: 3000, ReadSectors: 12000, WriteSectors: 6000}}) // 10s 内 busy +250 / total +1000 → 25%
	if rep.CPU < 24.9 || rep.CPU > 25.1 {
		t.Fatalf("cpu = %v, want 25", rep.CPU)
	}
	// rx 10s 增 10000B → 1000 B/s；tx 增 5000B → 500 B/s（存储口径为字节/秒，
	// ×8 转比特在展示端做；dt 取真实耗时，留容差）
	if d := rep.Nets[0].RxBps - 1000; d < -1 || d > 1 {
		t.Fatalf("rx_bps = %v, want ≈1000", rep.Nets[0].RxBps)
	}
	if d := rep.Nets[0].TxBps - 500; d < -1 || d > 1 {
		t.Fatalf("tx_bps = %v, want ≈500", rep.Nets[0].TxBps)
	}
	if rep.Disks[0].IOUtil < 19.9 || rep.Disks[0].IOUtil > 20.1 {
		t.Fatalf("sda1 IO = %v, want ≈20", rep.Disks[0].IOUtil)
	}
	if d := rep.Disks[0].IoRead - 102400; d < -1 || d > 1 {
		t.Fatalf("sda1 读速率 = %v, want ≈102400", rep.Disks[0].IoRead)
	}
	if d := rep.Disks[0].IoWrite - 51200; d < -1 || d > 1 {
		t.Fatalf("sda1 写速率 = %v, want ≈51200", rep.Disks[0].IoWrite)
	}

	// 计数器回绕（本次 < 上次）不应产生负值/panic
	c2 := &Client{prev: &sample{at: time.Now().Add(-10 * time.Second), cpuBusy: 4000, cpuTotal: 5000,
		netRx: map[string]uint64{"eth0": 900000}, netTx: map[string]uint64{"eth0": 900000},
		ioStats: map[string]proto.DiskIOStat{"sda1": {Ticks: 5000, ReadSectors: 9000, WriteSectors: 9000}}}}
	rep2 := &proto.Report{
		Nets:  []proto.NetInfo{{Iface: "eth0", RxBytes: 1, TxBytes: 1}},
		Disks: []proto.DiskInfo{{Mount: "/", Device: "sda1"}},
	}
	c2.applyDelta(rep2, 100, 200, map[string]proto.DiskIOStat{"sda1": {Ticks: 1000, ReadSectors: 1000, WriteSectors: 1000}})
	if rep2.CPU != 0 {
		t.Fatalf("回绕时 cpu 应保持 0，got %v", rep2.CPU)
	}
	if rep2.Disks[0].IOUtil != 0 {
		t.Fatalf("回绕时 IO 应保持 0，got %v", rep2.Disks[0].IOUtil)
	}
	if rep2.Disks[0].IoRead != 0 || rep2.Disks[0].IoWrite != 0 {
		t.Fatalf("回绕时读/写速率应保持 0，got %v/%v", rep2.Disks[0].IoRead, rep2.Disks[0].IoWrite)
	}
}

// TestCheckHostKeyTOFU 验证指纹首连学习与变更拒绝。
func TestCheckHostKeyTOFU(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	fp := ssh.FingerprintSHA256(signer.PublicKey())

	var learned string
	c := &Client{opts: Options{OnHostKey: func(id int64, f string) { learned = f }}}
	if err := c.checkHostKey(signer.PublicKey()); err != nil || learned != fp {
		t.Fatalf("首连应学习指纹: err=%v learned=%q", err, learned)
	}

	c2 := &Client{opts: Options{HostKey: fp}}
	if err := c2.checkHostKey(signer.PublicKey()); err != nil {
		t.Fatalf("指纹一致应放行: %v", err)
	}

	c3 := &Client{opts: Options{HostKey: "SHA256:AAAAdifferent"}}
	if err := c3.checkHostKey(signer.PublicKey()); err == nil {
		t.Fatal("指纹变更应拒绝")
	}
}

// TestCollectOverSSH 用内存 SSH 服务器验证 Collect 全链路（建连/鉴权/执行/解析/TOFU 回调）。
func TestCollectOverSSH(t *testing.T) {
	hostSigner := newTestSigner(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go serveSSH(t, ln, hostSigner, staticFixture, dynamicFixture)

	keyPath := writeTestKey(t)
	var learnedFP string
	c := NewClient(Options{
		Addr:      ln.Addr().String(),
		User:      "monitor",
		KeyPath:   keyPath,
		OnHostKey: func(id int64, fp string) { learnedFP = fp },
		Timeout:   5 * time.Second,
	})
	defer c.Close()

	rep, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if learnedFP == "" || !strings.HasPrefix(learnedFP, "SHA256:") {
		t.Fatalf("TOFU 回调未触发: %q", learnedFP)
	}
	if rep.Sys == nil || rep.Sys.Hostname != "web-01" || rep.Sys.CPUCores != 8 {
		t.Fatalf("静态信息错误: %+v", rep.Sys)
	}
	if rep.Uptime != 123456 || rep.TCPEst != 42 || len(rep.Disks) != 2 {
		t.Fatalf("动态指标错误: uptime=%d tcp=%d disks=%d", rep.Uptime, rep.TCPEst, len(rep.Disks))
	}

	// 第二次采集：复用同一连接（fixture 不变 → 速率为 0，但不应报错）
	rep2, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Nets[0].RxBps != 0 {
		t.Fatalf("固定 fixture 下速率应为 0, got %v", rep2.Nets[0].RxBps)
	}
}

// newTestSigner 生成 SSH 服务器主机密钥。
func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// writeTestKey 生成客户端私钥文件（OpenSSH PEM）。
func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test_key")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// serveSSH 起一个最小 SSH 服务器：接受任意公钥，按命令内容返回 fixture。
func serveSSH(t *testing.T, ln net.Listener, hostSigner ssh.Signer, staticOut, dynOut string) {
	t.Helper()
	cfg := ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(hostSigner)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			sconn, chans, reqs, err := ssh.NewServerConn(conn, &cfg)
			if err != nil {
				conn.Close()
				return
			}
			go ssh.DiscardRequests(reqs)
			for newCh := range chans {
				if newCh.ChannelType() != "session" {
					newCh.Reject(ssh.UnknownChannelType, "unsupported")
					continue
				}
				ch, chReqs, _ := newCh.Accept()
				go func(ch ssh.Channel, chReqs <-chan *ssh.Request) {
					for req := range chReqs {
						if req.Type != "exec" {
							if req.WantReply {
								req.Reply(false, nil)
							}
							continue
						}
						var payload struct{ Command string }
						_ = ssh.Unmarshal(req.Payload, &payload)
						req.Reply(true, nil) // exec 请求必须应答，客户端等待确认后才收输出
						out := staticOut
						if strings.Contains(payload.Command, "=T=") {
							out = dynOut
						}
						_, _ = ch.Write([]byte(out))
						status := struct{ Status uint32 }{}
						_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(&status))
						_ = ch.Close()
					}
				}(ch, chReqs)
			}
			_ = sconn.Close()
		}()
	}
}
