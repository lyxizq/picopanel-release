package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webpush"
)

// WebPushHandler Web Push 浏览器订阅管理：前端 PushManager.subscribe 成功后
// 把订阅对象 POST 上来落库；退订/换浏览器时按 endpoint 删除。
type WebPushHandler struct {
	st *store.Store
}

// NewWebPushHandler 创建订阅管理 handler。
func NewWebPushHandler(st *store.Store) *WebPushHandler {
	return &WebPushHandler{st: st}
}

type subscribeReq struct {
	Endpoint string `json:"endpoint" binding:"required"`
	Keys     struct {
		P256dh string `json:"p256dh" binding:"required"`
		Auth   string `json:"auth" binding:"required"`
	} `json:"keys" binding:"required"`
}

// Subscribe 落一条浏览器订阅（endpoint 唯一，重复订阅覆盖更新密钥），归属打
// 会话用户的空间（2026-11-03 家庭成员口径：admin→0，成员→本人 users.id；改密钥
// 重订阅时 upsert 同步改归属）。渠道总开关关闭时拒绝（关 = 不能配置）。
func (h *WebPushHandler) Subscribe(c *gin.Context) {
	if !webpush.On(h.st) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Web Push 渠道总开关未开启"})
		return
	}
	var req subscribeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "订阅数据格式错误"})
		return
	}
	if !strings.HasPrefix(req.Endpoint, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "订阅 endpoint 不合法"})
		return
	}
	// 直走 store.UpsertPushSub（webpush.SaveSubscription 不带归属参数），把
	// user_id 打成当前会话空间；推送定向本批不按订阅过滤（家庭广播是有意取舍，
	// 见 store.ListPushSubsFor 注释）
	if err := h.st.UpsertPushSub(&store.PushSub{
		Endpoint:  req.Endpoint,
		P256DH:    req.Keys.P256dh,
		Auth:      req.Keys.Auth,
		UserID:    scopeUID(c),
		UA:        c.Request.UserAgent(),
		CreatedAt: time.Now().Unix(),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存订阅失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Unsubscribe 按 endpoint 删除订阅（前端 pushManager.unsubscribe 后调用）。
func (h *WebPushHandler) Unsubscribe(c *gin.Context) {
	var req subscribeReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Endpoint == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 endpoint"})
		return
	}
	if err := webpush.RemoveSubscriptionFor(h.st, scopeUID(c), req.Endpoint); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除订阅失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteSub 按行 ID 删除订阅（设置页「已订阅的浏览器」列表删除按钮）。
// 只删服务端记录；若删的是本浏览器自己的订阅，前端按指纹识别后自行
// pushManager.unsubscribe 复位本机状态，删除后可随时重新订阅。
func (h *WebPushHandler) DeleteSub(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "订阅 ID 不合法"})
		return
	}
	if err := webpush.RemoveSubscriptionByIDFor(h.st, scopeUID(c), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除订阅失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
