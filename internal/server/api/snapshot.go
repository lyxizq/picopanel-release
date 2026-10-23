package api

import (
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// SnapshotHandler 提供只读快照接口：GET /api/v1/snapshot?server=<名称或ID>。
// 面向 Homepage（gethomepage.dev）这类常驻面板——登录会话 7 天过期不适合轮询，
// 改用配置文件里的长期令牌（snapshot_token，空=不开通本接口）。令牌只能放
// X-Snapshot-Token 头——查询参数会原样落访问/反代日志，等于把密钥写进日志。
// 返回扁平 JSON，数值字段供 format 映射，*_h 为服务端预格式化好的人类可读串。
type SnapshotHandler struct {
	st           *store.Store
	token        string
	offlineAfter int64
}

// snapshotResp 扁平结构：Homepage customapi 用点路径取字段，扁平最简单。
type snapshotResp struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	Online   bool   `json:"online"`
	Enabled  bool   `json:"enabled"`
	Firing   int    `json:"firing"`

	CPU      float64 `json:"cpu"`       // 使用率 %
	Mem      float64 `json:"mem"`       // 使用率 %
	MemUsed  int64   `json:"mem_used"`  // 字节
	MemTotal int64   `json:"mem_total"` // 字节
	MemH     string  `json:"mem_h"`     // "1.5 GB / 4.0 GB"
	Swap     float64 `json:"swap"`      // 使用率 %
	Load1    float64 `json:"load1"`
	Load5    float64 `json:"load5"`
	Load15   float64 `json:"load15"`

	Disk     float64 `json:"disk"`      // 各挂载点最大使用率 %
	DiskRoot float64 `json:"disk_root"` // 根分区使用率 %（无根分区记录时 0）
	IOUtil   float64 `json:"io"`        // 各盘最大 IO 使用率 %

	NetRx  float64 `json:"net_rx"` // 合计下行 B/s（仅服务器设置里配置的网口，空=全部）
	NetTx  float64 `json:"net_tx"` // 合计上行 B/s
	NetRxH string  `json:"net_rx_h"`
	NetTxH string  `json:"net_tx_h"`

	TCPEst  int64  `json:"tcp_est"`
	Uptime  int64  `json:"uptime"` // 秒
	UptimeH string `json:"uptime_h"`
	TS      int64  `json:"ts"` // 最新指标时间（Unix 秒）
}

// Get 处理一次快照查询。
func (h *SnapshotHandler) Get(c *gin.Context) {
	got := c.GetHeader("X-Snapshot-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "令牌无效"})
		return
	}

	q := strings.TrimSpace(c.Query("server"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 server 参数（名称或 ID）"})
		return
	}
	srv := findServer(h.st, q)
	if srv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "服务器不存在: " + q})
		return
	}

	m, _ := h.st.LatestMetric(srv.ID) // 无数据时字段为零值，在线状态仍可判
	firing, _ := h.st.FiringCountByServer()

	resp := snapshotResp{
		ID: srv.ID, Name: srv.Name, Hostname: srv.Hostname,
		Online:  srv.Enabled && srv.LastReportAt > 0 && time.Now().Unix()-srv.LastReportAt <= h.offlineAfter,
		Enabled: srv.Enabled,
	}
	if f := firing[srv.ID]; f > 0 { // firing 为 map[int64]int，直接下标取
		resp.Firing = f
	}
	if m != nil {
		resp.CPU = round1(m.CPU)
		resp.Mem = round1(m.Mem)
		resp.MemUsed, resp.MemTotal = m.MemUsed, m.MemTotal
		resp.MemH = humanBytes(m.MemUsed) + " / " + humanBytes(m.MemTotal)
		resp.Swap = round1(m.Swap)
		resp.Load1, resp.Load5, resp.Load15 = m.Load1, m.Load5, m.Load15
		resp.TCPEst, resp.Uptime, resp.TS = m.TCPEst, m.Uptime, m.TS
		resp.UptimeH = humanUptime(m.Uptime)
		for _, d := range m.Disks {
			if d.Percent > resp.Disk {
				resp.Disk = d.Percent
			}
			if d.Mount == "/" {
				resp.DiskRoot = d.Percent
			}
			if d.IOUtil > resp.IOUtil {
				resp.IOUtil = d.IOUtil
			}
		}
		resp.Disk, resp.DiskRoot, resp.IOUtil = round1(resp.Disk), round1(resp.DiskRoot), round1(resp.IOUtil)
		resp.NetRx, resp.NetTx = sumNet(m, srv.NetIfaces)
		resp.NetRxH, resp.NetTxH = humanBps(resp.NetRx), humanBps(resp.NetTx)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// findServer 按数字 ID 或名称定位服务器（名称唯一，建表有 UNIQUE 约束）。
// 快照与即时查询两个只读接口共用。
func findServer(st *store.Store, q string) *store.Server {
	if id, err := strconv.ParseInt(q, 10, 64); err == nil {
		srv, _ := st.GetServer(id)
		return srv
	}
	srv, err := st.GetServerByName(q) // name 有 UNIQUE 约束，SQL 点查替代全表解密后线性找
	if err != nil {
		return nil
	}
	return srv
}

// sumNet 合计网卡速率（B/s）：配置了 net_ifaces 只合计所列网口，空=全部
// （lo 已在采集侧排除），与前端卡片/详情页的过滤口径一致。
func sumNet(m *store.MetricRow, netIfaces string) (rx, tx float64) {
	want := map[string]bool{}
	for _, w := range strings.Split(netIfaces, ",") {
		if w = strings.TrimSpace(w); w != "" {
			want[w] = true
		}
	}
	for _, n := range m.Nets {
		if len(want) > 0 && !want[n.Iface] {
			continue
		}
		rx += n.RxBps
		tx += n.TxBps
	}
	return rx, tx
}

// round1 保留 1 位小数，与前端展示精度一致。
func round1(v float64) float64 { return mathRound(v*10) / 10 }

func mathRound(v float64) float64 {
	if v < 0 {
		return float64(int64(v - 0.5))
	}
	return float64(int64(v + 0.5))
}

// humanBytes 字节数 → "1.5 GB"（1024 进制，与前端 fmtBytes 一致）。
func humanBytes(v int64) string {
	f := float64(v)
	for _, u := range [...]string{"B", "KB", "MB", "GB", "TB", "PB"} {
		if f < 1024 || u == "PB" {
			return strconv.FormatFloat(f, 'f', 1, 64) + " " + u
		}
		f /= 1024
	}
	return ""
}

// humanBps B/s → Kbps 规格（×8 转比特、1000 进制）："12.0 Kbps" / "1.2 Mbps"。
func humanBps(v float64) string {
	k := v * 8 / 1000
	for _, u := range [...]string{"Kbps", "Mbps", "Gbps"} {
		if k < 1000 || u == "Gbps" {
			return strconv.FormatFloat(k, 'f', 1, 64) + " " + u
		}
		k /= 1000
	}
	return ""
}

// humanUptime 秒 → "12 天 3 小时" / "5 小时 20 分" / "45 秒"。
func humanUptime(sec int64) string {
	if sec < 60 {
		return strconv.FormatInt(sec, 10) + " 秒"
	}
	if sec < 3600 {
		return strconv.FormatInt(sec/60, 10) + " 分"
	}
	if sec < 86400 {
		return strconv.FormatInt(sec/3600, 10) + " 小时 " + strconv.FormatInt(sec%3600/60, 10) + " 分"
	}
	return strconv.FormatInt(sec/86400, 10) + " 天 " + strconv.FormatInt(sec%86400/3600, 10) + " 小时"
}
