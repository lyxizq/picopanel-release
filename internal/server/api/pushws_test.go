package api

// pushws 集成测试：真 WS 握手（Cookie 鉴权）→ 回环请求/应答 → store 消息钩子
// → messages.unread 事件推送。覆盖注册链路（Register 末尾 installPushChannel）。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/dbtool"
	"lz-monitor/internal/server/mail"
	"lz-monitor/internal/server/store"
)

// newPushEnv 起一套带 WS 通道的最小环境：临时库 + admin 会话 + Register 全路由。
func newPushEnv(t *testing.T) (*gin.Engine, *store.Store, int64, string) {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "lm.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal("查管理员失败")
	}
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	mg := dbtool.New(s, filepath.Join(t.TempDir(), "dbbackups"))
	t.Cleanup(func() { mg.Close() })
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, mail.New(s), nil, mg, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return r, s, u.ID, token
}

// dialPushWS 携会话 Cookie 拨 WS（同源 Origin；空 Origin 同样被 CheckOrigin 放行）。
func dialPushWS(t *testing.T, r *gin.Engine, token string) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"
	header := http.Header{}
	header.Set("Cookie", "lz_session="+token)
	header.Set("Origin", strings.Replace(wsURL, "ws", "http", 1))
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("拨 WS: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func wsReadFrame(t *testing.T, ws *websocket.Conn) map[string]interface{} {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("读 WS 帧: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("帧非 JSON: %v", err)
	}
	return m
}

// TestPushWSRequestReplyEvent 全链路：回环请求应答 → 服务端写消息 → 钩子推送
// 未读事件（顺带验证 store 消息钩子装配）。
func TestPushWSRequestReplyEvent(t *testing.T) {
	r, s, uid, token := newPushEnv(t)
	ws := dialPushWS(t, r, token)

	// 回环应答：GET /messages/unread 走完整中间件链（鉴权+no-store）返回真实计数
	if err := ws.WriteJSON(map[string]interface{}{"t": "req", "id": 1, "method": "GET", "path": "/messages/unread"}); err != nil {
		t.Fatal(err)
	}
	m := wsReadFrame(t, ws)
	if m["t"] != "res" || m["id"].(float64) != 1 || m["status"].(float64) != 200 {
		t.Fatalf("应答帧不符: %v", m)
	}
	body, _ := m["body"].(map[string]interface{})
	if body == nil || body["count"].(float64) != 0 {
		t.Fatalf("未读计数应为 0: %v", m)
	}

	// 事件推送：store 层 CreateMessage → 钩子 → messages.unread（count=1）
	if err := s.CreateMessage(&store.Message{Type: "reminder", Title: "推送测试", UserID: uid}); err != nil {
		t.Fatal(err)
	}
	m = wsReadFrame(t, ws)
	if m["t"] != "event" || m["topic"] != "messages.unread" {
		t.Fatalf("应推送未读事件: %v", m)
	}
	body, _ = m["body"].(map[string]interface{})
	if body == nil || body["count"].(float64) != 1 {
		t.Fatalf("未读事件计数应为 1: %v", m)
	}
}

// TestPushWSUnauthorized 无会话握手被拒（401，Upgrade 前拦截）。
func TestPushWSUnauthorized(t *testing.T) {
	r, _, _, _ := newPushEnv(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws"
	if _, _, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil {
		t.Fatal("无会话拨 WS 应被拒")
	}
}

// TestPushWSBadPathRejected 外链 path 拒绝（回环只到自家路由）。
func TestPushWSBadPathRejected(t *testing.T) {
	r, _, _, token := newPushEnv(t)
	ws := dialPushWS(t, r, token)
	if err := ws.WriteJSON(map[string]interface{}{"t": "req", "id": 2, "method": "GET", "path": "http://evil.example/x"}); err != nil {
		t.Fatal(err)
	}
	m := wsReadFrame(t, ws)
	if m["status"].(float64) != http.StatusBadRequest {
		t.Fatalf("外链 path 应 400: %v", m)
	}
}

// TestPushWSWebAllow 白名单场景回归（2026-11-05 部署实测）：web_allow 只放行
// 本机回环 IP 时，握手（对端 127.0.0.1）命中放行；回环请求继承握手寻址三元组
// 后同样命中放行——不继承会被 httptest 默认假值 192.0.2.1 拒 403。
func TestPushWSWebAllow(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "lm.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, _ := s.UserByUsername("admin")
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	cfg := &config.Config{WebAllow: []string{"127.0.0.1/32", "::1/128"}}
	if err := Register(r, s, nil, cfg, "test", nil, nil, mail.New(s), nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ws := dialPushWS(t, r, token)
	if err := ws.WriteJSON(map[string]interface{}{"t": "req", "id": 1, "method": "GET", "path": "/messages/unread"}); err != nil {
		t.Fatal(err)
	}
	m := wsReadFrame(t, ws)
	if m["status"].(float64) != http.StatusOK {
		t.Fatalf("白名单放行的连接回环请求应 200: %v", m)
	}
}

// TestPushWSUserIsolation 用户隔离：成员连接收不到管理员空间的事件。
func TestPushWSUserIsolation(t *testing.T) {
	r, s, adminID, adminToken := newPushEnv(t)
	famID, err := s.CreateUser("fam", "pass1234", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	famToken, err := s.CreateSession(famID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	wsAdmin := dialPushWS(t, r, adminToken)
	wsFam := dialPushWS(t, r, famToken)

	// 管理员空间写消息：只有管理员连接收到事件，成员连接静默
	if err := s.CreateMessage(&store.Message{Type: "reminder", Title: "隔离测试", UserID: adminID}); err != nil {
		t.Fatal(err)
	}
	m := wsReadFrame(t, wsAdmin)
	if m["topic"] != "messages.unread" {
		t.Fatalf("管理员连接应收到事件: %v", m)
	}
	wsFam.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := wsFam.ReadMessage(); err == nil {
		t.Fatal("成员连接不应收到管理员空间事件")
	}
}
