// Package push 提供面板单条 WebSocket 长连接通道（24h 常开页面零 HTTP 轮询）：
// 客户端 JSON 请求经服务端进程内回环（loopback）分发到现有 gin handler——
// 鉴权、家庭闸、限流、滑动续期全部原样复用零重复；服务端事件（消息未读/
// 邮件角标/天气采样）反向推送。纯传输层，不含业务判断。
package push

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"lz-monitor/internal/server/store"
)

const (
	// sendBuffer 每连接发送缓冲（条数）：事件推送满载即丢（客户端低频 HTTP
	// 对账兜底）；回环应答不走丢弃（客户端在等），满载阻塞短超时后断线重连。
	sendBuffer = 64
	// dispatchConcurrency 单连接并发回环分发上限：一个连接涌进超量请求按序
	// 排队，超出并发部分的请求排队等待，防单连接打满 handler 池。
	dispatchConcurrency = 4
	// dispatchQueueWait 排队等待上限：超时按 busy 拒（429），常态瞬时慢
	// handler 不至于让客户端轮询直接报错。
	dispatchQueueWait = 5 * time.Second
	// maxConnsPerUser 单用户并发连接上限（多标签页各一条，16 远超正常使用；
	// 超出按 429 拒握手）。
	maxConnsPerUser = 16
	// pingInterval 协议级 ping 周期：浏览器自动回 pong（不算 HTTP 请求）。
	// 空闲窗口远小于边缘节点 300s 空闲断线阈值（EdgeOne），NAT 也不会掐。
	pingInterval = 30 * time.Second
	// readTimeout 读超时（pong 续期）：两个 ping 周期内无任何字节视为死链。
	readTimeout = 60 * time.Second
	// sessionRevalidate 会话存活重验周期：登出/改密踢会话后长连接在下一个
	// 周期内收敛关闭（4001），不再收到推送。
	sessionRevalidate = 5 * time.Minute
	// maxMessage 单帧入站上限（JSON 请求体；上传类 multipart 不走本通道）。
	maxMessage = 8 << 20
	// writeTimeout 单帧写超时：阻塞超过即断线（慢客户端收敛）。
	writeTimeout = 5 * time.Second
	// closeSessionExpired 服务端判会话失效的关闭码（前端据此清会话跳登录）。
	closeSessionExpired = 4001
)

// inbound 客户端→服务端帧（t=req）。
type inbound struct {
	T      string          `json:"t"`
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}

// outbound 服务端→客户端帧：t=res（回环应答，ID 对应）/ t=event（反向推送）。
type outbound struct {
	T      string          `json:"t"`
	ID     int64           `json:"id,omitempty"`
	Status int             `json:"status,omitempty"`
	Topic  string          `json:"topic,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
	B64    bool            `json:"b64,omitempty"` // body 非 JSON 时 base64（兜底，常规轮询端点全 JSON）
}

// Hub 连接注册表：userID → 连接集合（同用户多标签页各一条，事件全量送达）。
type Hub struct {
	mu    sync.RWMutex
	conns map[int64]map[*Conn]struct{}
}

// NewHub 构造。
func NewHub() *Hub {
	return &Hub{conns: map[int64]map[*Conn]struct{}{}}
}

// Add 注册连接（WS 握手鉴权通过后）；超出单用户连接数上限返回 false（握手
// 层据此拒绝——防同账号无限开连接耗资源）。
func (h *Hub) Add(c *Conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.conns[c.userID]
	if set == nil {
		set = map[*Conn]struct{}{}
		h.conns[c.userID] = set
	}
	if len(set) >= maxConnsPerUser {
		return false
	}
	set[c] = struct{}{}
	return true
}

// Remove 注销连接（断开时）。
func (h *Hub) Remove(c *Conn) {
	h.mu.Lock()
	if set, ok := h.conns[c.userID]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.conns, c.userID)
		}
	}
	h.mu.Unlock()
}

// PublishToUser 给指定用户的全部连接推送事件（payload 为该用户私有数据，
// 如未读数）。JSON 只编码一次，慢客户端丢弃不阻塞。
func (h *Hub) PublishToUser(userID int64, topic string, data interface{}) {
	h.publish(h.snapshotUser(userID), topic, data)
}

// PublishAll 给全部连接推送事件（全局数据，如邮件角标/天气采样通知）。
func (h *Hub) PublishAll(topic string, data interface{}) {
	h.mu.RLock()
	all := make([]*Conn, 0, len(h.conns))
	for _, set := range h.conns {
		for c := range set {
			all = append(all, c)
		}
	}
	h.mu.RUnlock()
	h.publish(all, topic, data)
}

func (h *Hub) snapshotUser(userID int64) []*Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	set := h.conns[userID]
	out := make([]*Conn, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	return out
}

func (h *Hub) publish(conns []*Conn, topic string, data interface{}) {
	if len(conns) == 0 {
		return
	}
	body, err := json.Marshal(data)
	if err != nil {
		return
	}
	frame, err := json.Marshal(outbound{T: "event", Topic: topic, Body: body})
	if err != nil {
		return
	}
	for _, c := range conns {
		c.sendEvent(frame)
	}
}

// HandshakeMeta 握手请求的寻址三元组：回环请求原样继承，让按 Host/来源 IP
// 判定的中间件（web_allow 白名单、real_ip 头采信）看到与真实浏览器完全一致
// 的视图。字段是服务端进程内赋值（不经网络传输，外部不可伪造）；real-ip 头
// 的采信仍按中间件自身的「对端是否可信代理」校验走，语义不变。
type HandshakeMeta struct {
	Host         string // 握手请求 Host（web_allow 域名条目判定）
	RemoteAddr   string // 握手请求 TCP 对端（web_allow IP 条目判定）
	RealIPHeader string // real_ip_header 配置名（空=未配置）
	RealIPValue  string // 握手请求携带的真实 IP 头值（空=未携带）
}

// Conn 单条 WS 长连接：loopback 回环分发器（gin Engine）+ 会话令牌（握手时
// 从 Cookie/Bearer 取出，回环请求以它过 authRequired 中间件——鉴权/续期/家庭
// 闸与普通 HTTP 请求完全同路）。
type Conn struct {
	hub    *Hub
	userID int64
	token  string
	st     *store.Store
	loop   http.Handler
	ws     *websocket.Conn
	meta   HandshakeMeta

	send         chan []byte // 已编码帧（事件/应答共用；写协程独占写权）
	sem          chan struct{}
	closed       atomic.Bool
	closeMu      sync.Once
	readPumpDone chan struct{}
}

// NewConn 装配（不启动泵；Run 阻塞至连接断开）。
func (h *Hub) NewConn(userID int64, token string, st *store.Store, loop http.Handler, ws *websocket.Conn, meta HandshakeMeta) *Conn {
	return &Conn{
		hub: h, userID: userID, token: token, st: st, loop: loop, ws: ws, meta: meta,
		send:         make(chan []byte, sendBuffer),
		sem:          make(chan struct{}, dispatchConcurrency),
		readPumpDone: make(chan struct{}),
	}
}

// Run 阻塞运行读写两泵，断开后注销并等待读泵退出（写泵随读泵退出信号收摊）。
// 连接须已由调用方 Add（握手层先做单用户上限判定）。
func (c *Conn) Run() {
	defer c.hub.Remove(c)
	go c.writePump()
	c.readPump()
	<-c.readPumpDone
}

// close 断开连接（幂等）：读泵的 deadline 立刻到期退出，写泵随 readPumpDone
// 收摊。刻意不 close(c.send)——close 与 sendEvent/sendReply 的 check-then-send
// 存在竞态窗口（401 踢线瞬间有在途回答），关通道会 panic send-on-closed；
// 通道留给 GC 随 Conn 整体回收，连接结束后多缓冲的帧无人消费、无害。
func (c *Conn) close(code int, reason string) {
	c.closeMu.Do(func() {
		c.closed.Store(true)
		c.ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
		c.ws.Close()
	})
}

// sendEvent 事件推送（丢弃式）：缓冲满=慢客户端，事件丢了还有低频 HTTP 对账
// 与下一条事件兜底，绝不阻塞发布方。
func (c *Conn) sendEvent(frame []byte) {
	if c.closed.Load() {
		return
	}
	select {
	case c.send <- frame:
	default:
		log.Printf("ws push: 用户 %d 连接慢，丢弃事件帧", c.userID)
	}
}

// sendReply 回环应答（不丢弃——客户端在等）：短阻塞后仍满则断线让前端重连。
func (c *Conn) sendReply(frame []byte) {
	if c.closed.Load() {
		return
	}
	select {
	case c.send <- frame:
	default:
		select {
		case c.send <- frame:
		case <-time.After(writeTimeout):
			log.Printf("ws push: 用户 %d 应答通道阻塞，断线重连", c.userID)
			c.close(websocket.CloseInternalServerErr, "slow")
		case <-c.readPumpDone:
		}
	}
}

// writePump 唯一写协程：应用帧 + 协议 ping 同源串行（gorilla 单写者约束）。
// ping 间隙顺带做会话存活重验（登出/踢会话的收敛点）。
func (c *Conn) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	revalidated := time.Now()
	for {
		select {
		case frame := <-c.send:
			c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.ws.WriteMessage(websocket.TextMessage, frame); err != nil {
				c.close(websocket.CloseInternalServerErr, "write")
				return
			}
		case <-c.readPumpDone:
			return // 连接结束（readPump 退出即定论；send 不关闭，无消费方）
		case <-ticker.C:
			// 每 sessionRevalidate 重验一次会话（ping 已保活传输层，这里保活
			// 授权层）；会话确已失效 → 4001 让前端清会话跳登录；查询自身失败
			//（DB 抖动）≠ 会话失效，按内部错误断开，前端照常重连不清会话
			if time.Since(revalidated) >= sessionRevalidate {
				revalidated = time.Now()
				u, _, err := c.st.SessionUserWithExpiry(c.token)
				if err != nil {
					c.close(websocket.CloseInternalServerErr, "revalidate")
					return
				}
				if u == nil {
					c.close(closeSessionExpired, "session")
					return
				}
			}
			c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close(websocket.CloseInternalServerErr, "ping")
				return
			}
		}
	}
}

// readPump 读泵：收 req 帧即回环分发（ goroutine 内执行，读循环不被慢 handler
// 拖住）；读超时=死链（ping/pong 保活失败）退出。
func (c *Conn) readPump() {
	defer close(c.readPumpDone)
	c.ws.SetReadLimit(maxMessage)
	c.ws.SetReadDeadline(time.Now().Add(readTimeout))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(readTimeout))
		return nil
	})
	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			c.close(websocket.CloseNormalClosure, "read")
			return
		}
		c.ws.SetReadDeadline(time.Now().Add(readTimeout))
		var in inbound
		if err := json.Unmarshal(raw, &in); err != nil || in.T != "req" || in.ID <= 0 {
			continue // 帧损坏/非请求帧：忽略（协议内无致命错误，等下一帧）
		}
		go c.dispatch(in)
	}
}

// dispatch 回环分发：按帧内 method/path 在本进程内走完整 gin 中间件链
// （authRequired→家庭闸→handler），应答原样回传。401 说明会话已失效（登出/
// 改密/过期），应答后立即 4001 断线收敛。
func (c *Conn) dispatch(in inbound) {
	if c.closed.Load() {
		return
	}
	// 并发上限内排队（前端轮询节拍下 4 路并发绰绰有余；某 handler 打上游慢时
	// 后续请求最多等 5s），超时才拒——常态下避免瞬时慢 handler 引发客户端报错
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-time.After(dispatchQueueWait):
		c.reply(in.ID, http.StatusTooManyRequests, mustJSON(map[string]string{"error": "busy"}), false)
		return
	case <-c.readPumpDone:
		return // 连接已结束
	}
	method := strings.ToUpper(in.Method)
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
	default:
		c.reply(in.ID, http.StatusBadRequest, mustJSON(map[string]string{"error": "bad method"}), false)
		return
	}
	// path 强制站内相对路径（防 SSRF 式滥用：本通道只回环到自家路由）。
	// "//host/x" 形态经 url.Parse 会产出 authority 组件，同样必须拒
	if !strings.HasPrefix(in.Path, "/") || strings.HasPrefix(in.Path, "//") || strings.Contains(in.Path, "://") {
		c.reply(in.ID, http.StatusBadRequest, mustJSON(map[string]string{"error": "bad path"}), false)
		return
	}
	full := in.Path
	if !strings.HasPrefix(full, "/api/v1/") {
		full = "/api/v1" + full
	}
	u, err := url.Parse(full)
	if err != nil {
		c.reply(in.ID, http.StatusBadRequest, mustJSON(map[string]string{"error": "bad path"}), false)
		return
	}
	// "//evil.com/x" 形态经 Parse 会产出 Host 组件——站内路径不允许带 authority
	if u.Host != "" {
		c.reply(in.ID, http.StatusBadRequest, mustJSON(map[string]string{"error": "bad path"}), false)
		return
	}
	// JSON.stringify 对 undefined 字段直接省略，但显式 body:null 会以 "null"
	// 进帧——按无体处理，避免无体请求被拼出 Content-Length: 4 的 JSON null 体
	hasBody := len(in.Body) > 0 && !bytes.Equal(in.Body, []byte("null"))
	var bodyReader io.Reader
	if hasBody {
		bodyReader = bytes.NewReader(in.Body)
	}
	req := httptest.NewRequest(method, u.String(), bodyReader)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	// 寻址三元组继承握手请求（httptest.NewRequest 的默认 Host=example.com、
	// RemoteAddr=192.0.2.1 会让 web_allow 白名单判定 403——见 2026-11-05 部署
	// 实测）。进程内赋值不经网络，外部不可伪造
	if c.meta.Host != "" {
		req.Host = c.meta.Host
	}
	if c.meta.RemoteAddr != "" {
		req.RemoteAddr = c.meta.RemoteAddr
	}
	if c.meta.RealIPHeader != "" && c.meta.RealIPValue != "" {
		req.Header.Set(c.meta.RealIPHeader, c.meta.RealIPValue)
	}
	rec := httptest.NewRecorder()
	c.loop.ServeHTTP(rec, req)

	body := rec.Body.Bytes()
	b64 := false
	if len(body) > 0 && !json.Valid(body) {
		// 非 JSON 应答兜底走 base64（常规轮询端点全 JSON 不入此支）。注意
		// Body 列是 json.RawMessage——只收合法 JSON，base64 裸文本塞进来会
		// 序列化失败（实测发出无体的 500 帧），必须编码成 JSON 字符串字面量
		body = mustJSON(base64.StdEncoding.EncodeToString(body))
		b64 = true
	}
	c.reply(in.ID, rec.Code, body, b64)
	if rec.Code == http.StatusUnauthorized {
		// 会话失效：同步踢连接（写泵的重验是兜底收敛）
		c.close(closeSessionExpired, "session")
	}
}

// reply 组装应答帧（编码失败降级为无体应答，不让单帧损坏挂死请求）。
func (c *Conn) reply(id int64, status int, body []byte, b64 bool) {
	if len(body) == 0 {
		body = nil
	}
	frame, err := json.Marshal(outbound{T: "res", ID: id, Status: status, Body: body, B64: b64})
	if err != nil {
		frame, _ = json.Marshal(outbound{T: "res", ID: id, Status: http.StatusInternalServerError})
	}
	c.sendReply(frame)
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
