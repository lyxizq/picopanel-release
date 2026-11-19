package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// AlertHandler 处理告警历史查询。
type AlertHandler struct {
	st *store.Store
}

// List 分页查询告警：?status=firing|resolved|all&server_id=&page=&page_size=
func (h *AlertHandler) List(c *gin.Context) {
	status := c.Query("status")
	if status == "" {
		status = "all"
	}
	if status != "all" && status != "firing" && status != "resolved" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "status 参数无效"})
		return
	}
	var serverID int64
	if v := c.Query("server_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "server_id 参数无效"})
			return
		}
		serverID = id
	}
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	alerts, total, err := h.st.ListAlerts(status, serverID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询告警失败"})
		return
	}
	if alerts == nil {
		alerts = []store.Alert{}
	}
	c.JSON(http.StatusOK, gin.H{"alerts": alerts, "total": total})
}
