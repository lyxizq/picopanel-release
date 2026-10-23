package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newWebAllowRouter 挂白名单中间件的测试路由：页面 / API / agent 上报三条路径。
func newWebAllowRouter(entries ...string) (*gin.Engine, *webAllowList) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil) // 与生产一致：ClientIP 只取 TCP 对端
	al := newWebAllowList(entries)
	r.Use(webAllowMiddleware(al))
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "page") })
	r.GET("/api/v1/auth/me", func(c *gin.Context) { c.String(http.StatusOK, "api") })
	r.POST("/api/v1/agent/report", func(c *gin.Context) { c.String(http.StatusOK, "report") })
	return r, al
}

func webAllowReq(r *gin.Engine, method, path, remote, host string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	if host != "" {
		req.Host = host
	}
	// 伪造的 XFF 不应绕过白名单（生产 SetTrustedProxies(nil) 不采信）
	req.Header.Set("X-Forwarded-For", "10.1.2.3")
	r.ServeHTTP(w, req)
	return w
}

// TestWebAllowStatic IP/CIDR 条目按来源 IP 匹配：命中放行（页面与 API 一致），未命中 403。
func TestWebAllowStatic(t *testing.T) {
	r, _ := newWebAllowRouter("10.1.2.3", "192.168.0.0/16")

	if w := webAllowReq(r, "GET", "/", "10.1.2.3:9000", ""); w.Code != http.StatusOK {
		t.Fatalf("单 IP 命中应放行: %d", w.Code)
	}
	if w := webAllowReq(r, "GET", "/api/v1/auth/me", "192.168.5.5:9000", ""); w.Code != http.StatusOK {
		t.Fatalf("CIDR 命中应放行: %d", w.Code)
	}
	// 未命中：页面 403 文本、API 403 JSON
	if w := webAllowReq(r, "GET", "/", "10.1.2.4:9000", ""); w.Code != http.StatusForbidden {
		t.Fatalf("未命中页面应 403: %d", w.Code)
	}
	if w := webAllowReq(r, "GET", "/api/v1/auth/me", "203.0.113.9:9000", ""); w.Code != http.StatusForbidden {
		t.Fatalf("未命中 API 应 403: %d", w.Code)
	}
	// XFF 伪造的 10.1.2.3 不能放行真实来源 203.0.113.9（上面已覆盖）
}

// TestWebAllowHost 域名条目按请求 Host 匹配：命中域名放行（任意来源 IP），
// Host 带端口/大小写/结尾点号均视为同一域名，未命中 403。
func TestWebAllowHost(t *testing.T) {
	r, _ := newWebAllowRouter("Monitor.Example.Com")

	if w := webAllowReq(r, "GET", "/", "203.0.113.9:9000", "monitor.example.com"); w.Code != http.StatusOK {
		t.Fatalf("Host 命中应放行: %d", w.Code)
	}
	if w := webAllowReq(r, "GET", "/api/v1/auth/me", "203.0.113.9:9000", "monitor.example.com:8080"); w.Code != http.StatusOK {
		t.Fatalf("Host 带端口应命中: %d", w.Code)
	}
	if w := webAllowReq(r, "GET", "/", "203.0.113.9:9000", "MONITOR.example.com."); w.Code != http.StatusOK {
		t.Fatalf("Host 大小写/结尾点号应命中: %d", w.Code)
	}
	if w := webAllowReq(r, "GET", "/", "203.0.113.9:9000", "other.example.com"); w.Code != http.StatusForbidden {
		t.Fatalf("Host 未命中应 403: %d", w.Code)
	}
	// 直接 IP 访问（Host 是 IP）不命中域名条目
	if w := webAllowReq(r, "GET", "/", "203.0.113.9:9000", "203.0.113.9:8080"); w.Code != http.StatusForbidden {
		t.Fatalf("IP 直连 Host 不应命中域名条目: %d", w.Code)
	}
}

// TestWebAllowAgentReportExempt agent 上报接口不受白名单限制（被监控机来源）。
func TestWebAllowAgentReportExempt(t *testing.T) {
	r, _ := newWebAllowRouter("10.1.2.3")
	if w := webAllowReq(r, "POST", "/api/v1/agent/report", "203.0.113.9:9000", ""); w.Code != http.StatusOK {
		t.Fatalf("agent 上报应豁免: %d", w.Code)
	}
}

// TestWebAllowParse 条目解析：单 IP / CIDR / 域名归位，非法条目忽略，IPv6 支持。
func TestWebAllowParse(t *testing.T) {
	al := newWebAllowList([]string{
		" 10.1.2.3 ", "fd00::/8", "Monitor.Example.Com.", "monitor2.example.com",
		"192.168.1.5/33", "bad host!", "",
	})
	if len(al.static) != 2 || len(al.names) != 2 {
		t.Fatalf("解析归类错误: static=%d names=%d", len(al.static), len(al.names))
	}
	if al.names[0] != "monitor.example.com" || al.names[1] != "monitor2.example.com" {
		t.Fatalf("域名应统一小写并去结尾点号: %v", al.names)
	}
	if !al.allowed(net.ParseIP("fd00::1"), "") {
		t.Fatal("IPv6 CIDR 应命中")
	}
	if !al.allowed(net.ParseIP("10.1.2.3"), "") {
		t.Fatal("单 IP 应命中")
	}
	if al.allowed(net.ParseIP("fe80::1"), "") {
		t.Fatal("网段外 IPv6 不应命中")
	}
	if !al.allowed(nil, "monitor.example.com:8080") {
		t.Fatal("Host 命中与来源 IP 无关")
	}
	if al.allowed(nil, "nope.example.com") {
		t.Fatal("Host 未命中不应放行")
	}
}
