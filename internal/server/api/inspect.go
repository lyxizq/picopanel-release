package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/inspect"
	"lz-monitor/internal/server/mail"
	"lz-monitor/internal/server/store"
)

// InspectHandler 一键巡检报告：现查现算生成 JSON 报告 + 手动推送。
type InspectHandler struct {
	st           *store.Store
	mg           *mail.Manager // 邮箱未读快照来源（巡检报告的未读邮件项）
	offlineAfter int64         // 离线判定窗口（秒），与服务器列表口径一致
}

// unreadFn 未读快照取自 Manager 轮询缓存（与侧栏角标同口径，不现连 IMAP）。
func (h *InspectHandler) unreadFn() func() map[int64]int {
	return h.mg.UnreadAll
}

// Get 生成当前巡检报告（不落库）。
func (h *InspectHandler) Get(c *gin.Context) {
	r, err := inspect.Build(h.st, h.offlineAfter, h.unreadFn())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成巡检报告失败"})
		return
	}
	c.JSON(http.StatusOK, r)
}

// Push 立即把巡检报告推送到已启用的通知渠道（WxPusher/Webhook）。
func (h *InspectHandler) Push(c *gin.Context) {
	if err := inspect.PushNow(h.st, h.offlineAfter, h.unreadFn()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
