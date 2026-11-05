package push

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newTestConn 装配最小回环（一个 /api/v1/echo 回声路由）的测试连接。ws 传 nil：
// publish/回环应答路径不触碰 ws（只有 close 会写控制帧，用例避开 close 路径）。
func newTestConn(t *testing.T, h *Hub, uid int64) *Conn {
	t.Helper()
	r := gin.New()
	r.GET("/api/v1/echo", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/api/v1/text", func(c *gin.Context) { c.String(200, "plain text") })
	return h.NewConn(uid, "tok", nil, r, nil, HandshakeMeta{})
}

func readFrame(t *testing.T, c *Conn) map[string]interface{} {
	t.Helper()
	select {
	case raw := <-c.send:
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("帧非 JSON: %v", err)
		}
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("等帧超时")
		return nil
	}
}

// TestPublishToUser 定向推送：JSON 编码一次，落到目标用户连接缓冲。
func TestPublishToUser(t *testing.T) {
	h := NewHub()
	c := newTestConn(t, h, 7)
	h.Add(c)
	h.PublishToUser(7, "messages.unread", map[string]int{"count": 3})
	m := readFrame(t, c)
	if m["t"] != "event" || m["topic"] != "messages.unread" {
		t.Fatalf("事件帧不符: %v", m)
	}
	body, _ := m["body"].(map[string]interface{})
	if body == nil || body["count"].(float64) != 3 {
		t.Fatalf("事件体不符: %v", m)
	}
	// 其他用户的连接收不到
	c2 := newTestConn(t, h, 8)
	h.Add(c2)
	h.PublishToUser(7, "messages.unread", map[string]int{"count": 1})
	readFrame(t, c)
	select {
	case raw := <-c2.send:
		t.Fatalf("非目标连接收到帧: %s", raw)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestPublishAll 全员广播：多用户多连接各收一份，JSON 只编码一次。
func TestPublishAll(t *testing.T) {
	h := NewHub()
	c1 := newTestConn(t, h, 1)
	c2 := newTestConn(t, h, 2)
	h.Add(c1)
	h.Add(c2)
	h.PublishAll("mail.badge", nil)
	for _, c := range []*Conn{c1, c2} {
		m := readFrame(t, c)
		if m["topic"] != "mail.badge" {
			t.Fatalf("广播帧不符: %v", m)
		}
	}
}

// TestDispatchEcho 回环分发：短路径自动补 /api/v1 前缀，应答帧带状态与体。
func TestDispatchEcho(t *testing.T) {
	h := NewHub()
	c := newTestConn(t, h, 9)
	c.dispatch(inbound{T: "req", ID: 5, Method: "GET", Path: "/echo"})
	m := readFrame(t, c)
	if m["t"] != "res" || m["id"].(float64) != 5 || m["status"].(float64) != 200 {
		t.Fatalf("应答帧不符: %v", m)
	}
	if body, _ := m["body"].(map[string]interface{}); body == nil || body["ok"] != true {
		t.Fatalf("应答体不符: %v", m)
	}
}

// TestDispatchB64Body 非 JSON 应答兜底：base64 编码为 JSON 字符串字面量进帧
// （RawMessage 只收合法 JSON——裸文本塞入曾实测序列化失败发出无体 500 帧）。
func TestDispatchB64Body(t *testing.T) {
	h := NewHub()
	c := newTestConn(t, h, 11)
	c.dispatch(inbound{T: "req", ID: 7, Method: "GET", Path: "/text"})
	m := readFrame(t, c)
	if m["status"].(float64) != 200 || m["b64"] != true {
		t.Fatalf("非 JSON 应答应带 b64 标记: %v", m)
	}
	s, _ := m["body"].(string)
	if s == "" {
		t.Fatalf("b64 帧应含字符串体: %v", m)
	}
}

// TestDispatchMetaInherit 寻址三元组继承：回环请求的 Host/RemoteAddr/real-ip
// 头以握手值为准（web_allow 白名单按这三项判定，2026-11-05 部署实测 403 回归）。
func TestDispatchMetaInherit(t *testing.T) {
	r := gin.New()
	var gotHost, gotRemote, gotRealIP string
	r.GET("/api/v1/echo", func(c *gin.Context) {
		gotHost = c.Request.Host
		gotRemote = c.Request.RemoteAddr
		gotRealIP = c.GetHeader("Ali-Real-Client-Ip")
		c.JSON(200, gin.H{"ok": true})
	})
	h := NewHub()
	c := h.NewConn(1, "tok", nil, r, nil, HandshakeMeta{
		Host: "panel.example.com", RemoteAddr: "203.0.113.7:41000",
		RealIPHeader: "Ali-Real-Client-Ip", RealIPValue: "198.51.100.9",
	})
	c.dispatch(inbound{T: "req", ID: 1, Method: "GET", Path: "/echo"})
	readFrame(t, c)
	if gotHost != "panel.example.com" || gotRemote != "203.0.113.7:41000" || gotRealIP != "198.51.100.9" {
		t.Fatalf("三元组未继承: host=%q remote=%q realip=%q", gotHost, gotRemote, gotRealIP)
	}
}

// TestDispatchReject 非法方法与外链 path 拒绝（400 应答，不断连）。
func TestDispatchReject(t *testing.T) {
	h := NewHub()
	c := newTestConn(t, h, 9)
	c.dispatch(inbound{T: "req", ID: 1, Method: "PATCH", Path: "/echo"})
	if m := readFrame(t, c); m["status"].(float64) != 400 {
		t.Fatalf("非法方法应 400: %v", m)
	}
	c.dispatch(inbound{T: "req", ID: 2, Method: "GET", Path: "http://evil.example/x"})
	if m := readFrame(t, c); m["status"].(float64) != 400 {
		t.Fatalf("外链 path 应 400: %v", m)
	}
	// "//evil.com/x" 经 url.Parse 会产出 authority 组件，同样必须拒
	c.dispatch(inbound{T: "req", ID: 3, Method: "GET", Path: "//evil.com/api/v1/echo"})
	if m := readFrame(t, c); m["status"].(float64) != 400 {
		t.Fatalf("带 authority 的 path 应 400: %v", m)
	}
}

// TestSlowClientEventDrop 事件投递丢弃式：缓冲满不阻塞发布方（对账兜底语义）。
func TestSlowClientEventDrop(t *testing.T) {
	h := NewHub()
	c := newTestConn(t, h, 3)
	h.Add(c)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < sendBuffer*4; i++ {
			h.PublishToUser(3, "messages.unread", map[string]int{"count": i})
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("事件投递在缓冲满时阻塞了")
	}
}
