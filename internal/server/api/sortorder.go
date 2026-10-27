package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 服务监控页四类模块的拖拽排序（2026-10-27）：用量账户 / 服务监控服务器 / GitHub 仓库 / 证书。
// 请求体 {ids:[...]} 为拖后的完整顺序，服务端按序写 sort=1..n（会员到期先例）。
// 路由用 /xxx/order 静态段、注册在 /xxx/:id 之前（docs/order 先例）。

func bindOrderIDs(c *gin.Context) ([]int64, bool) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（ids 必填）"})
		return nil, false
	}
	return req.IDs, true
}

func respondOrder(c *gin.Context, err error) {
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存排序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// order 用量账户卡顺序。
func (h *AiUsageHandler) order(c *gin.Context) {
	if ids, ok := bindOrderIDs(c); ok {
		respondOrder(c, h.st.ReorderAiAccounts(ids))
	}
}

// orderServers 服务监控服务器卡顺序。
func (h *SvcMonHandler) orderServers(c *gin.Context) {
	if ids, ok := bindOrderIDs(c); ok {
		respondOrder(c, h.st.ReorderSvcServers(ids))
	}
}

// order GitHub 仓库卡顺序。
func (h *GhHandler) order(c *gin.Context) {
	if ids, ok := bindOrderIDs(c); ok {
		respondOrder(c, h.st.ReorderGhRepos(ids))
	}
}

// Order 证书卡顺序。
func (h *CertHandler) Order(c *gin.Context) {
	if ids, ok := bindOrderIDs(c); ok {
		respondOrder(c, h.st.ReorderCerts(ids))
	}
}
