package api

import (
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// webAllowList Web 访问白名单：static 为 IP/CIDR 条目（按来源 IP 匹配），
// names 为域名条目（按请求 Host 头匹配——只拦「不知道域名」的 IP 扫描器；
// Host 可被非浏览器客户端伪造，不能作为唯一防线，登录与限流仍然兜底）。
type webAllowList struct {
	static []*net.IPNet
	names  []string // 统一小写、去结尾点号
}

// newWebAllowList 解析白名单条目：单个 IP、CIDR、域名（字母数字点横线，
// 结尾点号忽略）。既不是 IP/CIDR 也不像域名的条目（多为手误）打日志忽略。
func newWebAllowList(entries []string) *webAllowList {
	a := &webAllowList{}
	for _, e := range entries {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			a.static = append(a.static, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		if _, ipnet, err := net.ParseCIDR(e); err == nil {
			a.static = append(a.static, ipnet)
			continue
		}
		e = strings.TrimSuffix(e, ".") // 结尾点号的 FQDN 写法
		if isDomainName(e) {
			a.names = append(a.names, e)
			continue
		}
		log.Printf("web_allow 忽略无法识别的条目 %q（支持 IP / CIDR / 域名）", e)
	}
	return a
}

// isDomainName 粗判条目是否像域名（非空、仅字母数字点横线、首尾非点/横线）。
func isDomainName(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") ||
		strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

// allowed 来源 IP 命中 IP/CIDR 条目，或请求 Host（域名部分）命中域名条目。
func (a *webAllowList) allowed(clientIP net.IP, host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host != "" {
		for _, n := range a.names {
			if n == host {
				return true
			}
		}
	}
	if clientIP == nil {
		return false
	}
	for _, n := range a.static {
		if n.Contains(clientIP) {
			return true
		}
	}
	return false
}

// webAllowMiddleware 只放行白名单访问：来源 IP 默认取 TCP 对端地址
// （SetTrustedProxies(nil)，X-Forwarded-For 可伪造不采信）；配置了
// real_ip_header（边缘代理部署）时优先取代理注入的真实 IP 头。例外：
// /api/v1/agent/ 前缀整组（上报/任务回传/升级束回源，X-Agent-Token 自鉴权，
// 被监控机的 IP 不该受 Web 白名单约束）。
func webAllowMiddleware(a *webAllowList) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/agent/") {
			c.Next()
			return
		}
		if a.allowed(net.ParseIP(clientIP(c)), c.Request.Host) {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "不在访问白名单（IP 或域名未命中）"})
			return
		}
		c.String(http.StatusForbidden, "403 Forbidden：不在访问白名单（IP 或域名未命中）")
		c.Abort()
	}
}
