package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// MessagesHandler 站内消息中心：告警触发/恢复、证书临期、提醒事项推送均落
// messages 表，这里提供分页查询（类型/未读筛选）、未读角标数与已读标记。
type MessagesHandler struct {
	st *store.Store
}

// List 分页查询消息。query: type(alert|reminder)、unread=1、page、page_size（默认 10）。
func (h *MessagesHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	p, err := h.st.ListMessages(c.Query("type"), c.Query("unread") == "1", page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询消息失败"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Unread 未读消息数（顶栏铃铛角标轮询）。
func (h *MessagesHandler) Unread(c *gin.Context) {
	n, err := h.st.UnreadCount()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询未读数失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": n})
}

// MarkRead 单条标已读。
func (h *MessagesHandler) MarkRead(c *gin.Context) {
	var req struct {
		ID int64 `json:"id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少消息 ID"})
		return
	}
	if err := h.st.MarkMessageRead(req.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "标记已读失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// MsgTypes 消息类型白名单（messages.type 取值；read-all 按类型标已读校验用，
// List 的 type 筛选沿用任意值不校验——筛错值只是空列表，无副作用）。
var MsgTypes = map[string]bool{"alert": true, "reminder": true, "inspect": true, "login": true, "mail": true, "membership": true, "tv": true}

// MarkAllRead 全部标已读，返回标记条数。query: type（可选，452：按类型标
// 已读——进邮件/提醒事项菜单自动清对应未读提醒），仅收白名单内的类型。
func (h *MessagesHandler) MarkAllRead(c *gin.Context) {
	msgType := c.Query("type")
	if msgType != "" && !MsgTypes[msgType] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未知消息类型"})
		return
	}
	var (
		n   int64
		err error
	)
	if msgType == "" {
		n, err = h.st.MarkAllMessagesRead()
	} else {
		n, err = h.st.MarkMessagesReadByType(msgType)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "标记已读失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "marked": n})
}

// Delete 删除单条消息（物理删除）。path: id。
func (h *MessagesHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少消息 ID"})
		return
	}
	if err := h.st.DeleteMessage(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除消息失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Clear 清空全部消息（物理删除），返回删除条数。
func (h *MessagesHandler) Clear(c *gin.Context) {
	n, err := h.st.ClearMessages()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清空消息失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "cleared": n})
}
