package api

import (
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// NetCheckHandler 工具箱「TCP/UDP 连通检测」：浏览器无原生 TCP/UDP 能力，
// 由服务端发起探测。TCP 实测握手连通与耗时；UDP 无连接语义只能做发送级
// 检测（数据报成功发出；若对端有回应则附带响应，收不到不代表不通）。
type NetCheckHandler struct {
	mu sync.Mutex
	// hits 按 IP 记录滑动窗口时间戳，限 10 次/分钟（探测可被用来扫内网端口）
	hits map[string][]int64
	// blocked 出站内网限制判定（config.OutboundHostBlocked；nil=不限制）
	blocked func(host string) bool
}

// NewNetCheckHandler 创建 handler。blocked 为出站内网限制钩子（可 nil）。
func NewNetCheckHandler(blocked func(host string) bool) *NetCheckHandler {
	return &NetCheckHandler{hits: map[string][]int64{}, blocked: blocked}
}

const (
	netCheckLimit   = 10
	netCheckWindow  = int64(60)
	netCheckTimeout = 5 * time.Second
	netCheckUDPWait = 3 * time.Second
	netCheckMaxHost = 255
)

type netCheckReq struct {
	Proto string `json:"proto" binding:"required"` // tcp | udp
	Host  string `json:"host" binding:"required"`
	Port  int    `json:"port" binding:"required"`
}

// allow 滑动窗口限流（10 次/分钟/IP），超限返回 false。
func (h *NetCheckHandler) allow(ip string, now int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	keep := h.hits[ip][:0]
	for _, t := range h.hits[ip] {
		if now-t < netCheckWindow {
			keep = append(keep, t)
		}
	}
	h.hits[ip] = keep
	if len(keep) >= netCheckLimit {
		return false
	}
	h.hits[ip] = append(keep, now)
	return true
}

// Check 执行一次连通检测。
func (h *NetCheckHandler) Check(c *gin.Context) {
	var req netCheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不完整（协议、主机、端口必填）"})
		return
	}
	if req.Proto != "tcp" && req.Proto != "udp" {
		c.JSON(400, gin.H{"error": "协议仅支持 tcp / udp"})
		return
	}
	host := req.Host
	if l := len(host); l == 0 || l > netCheckMaxHost {
		c.JSON(400, gin.H{"error": "主机名长度应为 1~255"})
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		c.JSON(400, gin.H{"error": "端口应在 1~65535 之间"})
		return
	}
	ip := clientIP(c)
	if !h.allow(ip, time.Now().Unix()) {
		c.JSON(429, gin.H{"error": "检测太频繁，每分钟最多 10 次，请稍后再试"})
		return
	}

	if h.blocked != nil && h.blocked(host) {
		c.JSON(403, gin.H{"error": "出站内网限制已开启（outbound_block_private），该目标解析到内网地址，已拒绝探测"})
		return
	}

	addr := net.JoinHostPort(host, strconv.Itoa(req.Port))
	if req.Proto == "tcp" {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, netCheckTimeout)
		ms := time.Since(start).Milliseconds()
		if err != nil {
			c.JSON(200, gin.H{"proto": "tcp", "ok": false, "ms": ms, "detail": "连接失败：" + err.Error()})
			return
		}
		remote := conn.RemoteAddr().String()
		conn.Close()
		c.JSON(200, gin.H{"proto": "tcp", "ok": true, "ms": ms, "remote": remote,
			"detail": "TCP 握手成功，端口开放"})
		return
	}

	// UDP：Dial 只做本地套接字准备（无握手），发送一个探测数据报；
	// 3 秒内收到回应 = 端口有服务在应答；收不到只说明「未收到响应」，
	// UDP 协议本身无法据此判定端口不通。
	conn, err := net.DialTimeout("udp", addr, netCheckTimeout)
	if err != nil {
		c.JSON(200, gin.H{"proto": "udp", "ok": false, "detail": "本地套接字准备失败：" + err.Error()})
		return
	}
	defer conn.Close()
	start := time.Now()
	if _, err := conn.Write([]byte("lz-monitor-probe")); err != nil {
		c.JSON(200, gin.H{"proto": "udp", "ok": false, "detail": "数据报发送失败：" + err.Error()})
		return
	}
	buf := make([]byte, 512)
	_ = conn.SetReadDeadline(time.Now().Add(netCheckUDPWait))
	n, err := conn.Read(buf)
	ms := time.Since(start).Milliseconds()
	if n > 0 {
		c.JSON(200, gin.H{"proto": "udp", "ok": true, "ms": ms,
			"detail": "数据报已发送，收到对端响应（端口有服务在应答）"})
		return
	}
	_ = err
	c.JSON(200, gin.H{"proto": "udp", "ok": true, "ms": ms,
		"detail": "数据报已成功发出（3 秒内未收到响应——UDP 无连接，这不代表端口不通）"})
}
