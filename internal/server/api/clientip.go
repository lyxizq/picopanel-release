// 真实客户端 IP 解析：部署在边缘代理（阿里 ESA 托管转换等）后面时，
// TCP 对端是回源节点，登录日志/登录限流/Web 白名单须读代理注入的请求头
// 才能拿到访客真实 IP。
package api

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

var (
	realIPHeader   string
	trustedProxies []*net.IPNet
)

// configureRealIP 装配真实 IP 请求头：header 为空=功能关闭（一律取 TCP 对端）；
// cidrs 为可信代理网段（IP 或 CIDR 条目），为空时该头一律不采信（fail-closed）——
// 任何知道源站地址的直连方都能伪造该头，不绑定可信网段等于把限流/白名单/
// 登录日志的来源 IP 交给攻击者改写。
func configureRealIP(header string, cidrs []string) {
	realIPHeader = strings.TrimSpace(header)
	for _, s := range cidrs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			// 裸 IP 条目按单机网段处理（区分 v4/v6 掩码）
			if ip := net.ParseIP(s); ip != nil {
				if ip.To4() != nil {
					s += "/32"
				} else {
					s += "/128"
				}
			}
		}
		if _, n, err := net.ParseCIDR(s); err == nil {
			trustedProxies = append(trustedProxies, n)
		}
	}
}

// clientIP 返回访客真实 IP：配置了 real_ip_header 且直连对端在可信代理
// 网段内（网段未配则一律不采信，防直连伪造）时，头值须为合法 IP 才采信；
// 否则回落 Gin 标准 ClientIP（engine 已 SetTrustedProxies(nil)，
// 即 TCP 对端地址）。X-Forwarded-For 一律不采信——客户端可伪造。
func clientIP(c *gin.Context) string {
	if realIPHeader != "" {
		if peer := net.ParseIP(c.RemoteIP()); peer != nil && peerTrusted(peer) {
			if v := strings.TrimSpace(c.GetHeader(realIPHeader)); v != "" {
				if ip := net.ParseIP(v); ip != nil {
					return ip.String()
				}
			}
		}
	}
	return c.ClientIP()
}

func peerTrusted(peer net.IP) bool {
	if len(trustedProxies) == 0 {
		return false // fail-closed：未绑定可信网段时不采信注入头
	}
	for _, n := range trustedProxies {
		if n.Contains(peer) {
			return true
		}
	}
	return false
}
