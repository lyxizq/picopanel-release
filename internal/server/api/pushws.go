package api

// pushws 单条 WebSocket 长连接端点（/api/v1/ws）：面板 24h 常开页面的全部
// 轮询改经此通道（服务端进程内回环复用现有 handler），另有三类事件反向推送
//（消息未读/邮件角标/天气采样）。组外注册（同 term/ws：gzip/JSON 中间件不
// 适配长连接），鉴权在握手时完成——浏览器 WS 握手自动携带同源 HttpOnly
// 会话 Cookie（路径 /api/v1），Authorization 头亦可（同等价 token）。

import (
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"lz-monitor/internal/server/mail"
	"lz-monitor/internal/server/push"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/weather"
)

// pushWSHandler WS 端点：st 鉴权、hub 连接注册表、loop 回环分发器（gin Engine）。
type pushWSHandler struct {
	st   *store.Store
	hub  *push.Hub
	loop http.Handler
}

// WS 握手：鉴权（Cookie 优先、Bearer 兜底）→ Upgrade → 连接入 hub 长驻。
func (h *pushWSHandler) WS(c *gin.Context) {
	token := bearerToken(c)
	if token == "" {
		if ck, err := c.Cookie(sessionCookie); err == nil {
			token = ck
		}
	}
	u, _, err := h.st.SessionUserWithExpiry(token)
	if err != nil {
		http.Error(c.Writer, "session check failed", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(c.Writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	// 同源校验（CSWSH 防护）：跨站页面发起的 WS 握手不带本站 Cookie
	//（SameSite=Strict），此处再按 Origin 头兜底一道
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			return r.Header.Get("Origin") == "" || sameOrigin(r.Host, r.Header.Get("Origin"))
		},
	}
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade 已写完 HTTP 错误响应
	}
	// 抄录握手请求的寻址三元组：回环请求继承（web_allow 白名单按 Host/来源 IP
	// 判定，不继承会把 httptest 默认假值 example.com/192.0.2.1 拿去判定必 403
	// ——2026-11-05 部署实测）
	meta := push.HandshakeMeta{
		Host:       c.Request.Host,
		RemoteAddr: c.Request.RemoteAddr,
	}
	if realIPHeader != "" {
		meta.RealIPHeader = realIPHeader
		meta.RealIPValue = c.GetHeader(realIPHeader)
	}
	conn := h.hub.NewConn(u.ID, u.Role, token, h.st, h.loop, ws, meta)
	if !h.hub.Add(conn) {
		// 单用户连接数超限（16）：按 429 拒绝握手后的连接
		ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(1013, "too many connections"), time.Now().Add(time.Second))
		ws.Close()
		return
	}
	conn.Run() // 阻塞至断开（写泵/回环分发全在内部协程）
}

// sameOrigin Origin 头与 Host 是否同源（scheme 无关：wss 握手 Origin 带
// https、ws 带 http，按 URL 解析后只比对 host[:port]）。
func sameOrigin(host, origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == host
}

// installPushChannel 装配 WS 通道与三类事件钩子（Register 末尾调用；零新参数
// ——全部依赖已在手：r 作回环分发器、st 消息钩子、mailMgr 角标钩子、weather
// 包级采样钩子）。
func installPushChannel(r *gin.Engine, st *store.Store, mailMgr *mail.Manager) {
	h := &pushWSHandler{st: st, hub: push.NewHub(), loop: r}
	r.GET("/api/v1/ws", h.WS)

	// 消息空间变更 → 补推该用户未读数（提醒/告警入站、标已读/删除同路收敛；
	// 多标签页各一份）。低频写路径上的一条本地点查，开销可忽略
	st.SetMessagesChangedHook(func(uid int64) {
		if n, err := st.UnreadCount(uid); err == nil {
			h.hub.PublishToUser(uid, "messages.unread", gin.H{"count": n})
		}
	})

	// 邮件角标变更 → 通知（无 payload：客户端按需拉 /mail/badge，含个人空间
	// 归属，逐用户组装反而重复）；无邮件权限的成员由前端 hasPerm 闸跳过
	if mailMgr != nil {
		mailMgr.SetOnChange(func() {
			h.hub.PublishAll("mail.badge", nil)
		})
	}

	// 天气采样落库成功 → 通知（客户端拉 /page/weather 刷新胶囊；10 分钟节拍
	// 由采样协程持有，页面不再自转轮询）
	weather.OnSample = func() {
		h.hub.PublishAll("weather.changed", nil)
	}

	log.Printf("WS 推送通道已装配：/api/v1/ws（回环复用 handler，3 类事件推送）")
}
