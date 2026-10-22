package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/updmgr"
)

// UpdateHandler 在线更新（395/396）：服务端自更新 + agent 分发。
// 全部挂在管理员会话组（更新=改动整机软件，受限用户不可见）。
type UpdateHandler struct {
	mgr *updmgr.Manager
}

// Status 最近一次检查结果（不发网络请求，页面进入即显）。
func (h *UpdateHandler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, h.mgr.Status())
}

// Check 立即探测更新通道（含 agent 包缓存与自动分发）。
func (h *UpdateHandler) Check(c *gin.Context) {
	st, err := h.mgr.Check(c.Request.Context())
	if err != nil {
		// 检查失败也回状态体（Error 字段承载原因），前端展示而非炸请求
		c.JSON(http.StatusOK, st)
		return
	}
	c.JSON(http.StatusOK, st)
}

// Apply 应用服务端更新：响应先返回，替换与 exec 在后台执行——成功时进程
// 被同 PID 换映像自动重启（页面断连数秒后重载即新版本），失败则旧版继续跑。
func (h *UpdateHandler) Apply(c *gin.Context) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := h.mgr.ApplyServer(ctx); err != nil {
			log.Printf("[update] 服务端更新未完成: %v", err)
		}
	}()
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "正在应用更新：校验通过后服务端将自动重启（约数秒），页面稍后自动恢复；任何失败都会保留当前版本继续运行"})
}

// Agents 全部 agent/push 机器的版本与可升级状态。
func (h *UpdateHandler) Agents(c *gin.Context) {
	list, err := h.mgr.AgentList()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	c.JSON(http.StatusOK, list)
}

type upgradeReq struct {
	IDs []int64 `json:"ids" binding:"required"`
}

// UpgradeAgents 对指定机器触发升级（pull 直推 / push 打标）。
func (h *UpdateHandler) UpgradeAgents(c *gin.Context) {
	var req upgradeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误：需 ids 数组"})
		return
	}
	results, err := h.mgr.UpgradeAgents(c.Request.Context(), req.IDs)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, results)
}
