package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// maxDashMetrics 总览页顶部重点指标数量上限（PC 一行 8 个，最多两行）。
const maxDashMetrics = 16

// tileMetrics 允许的指标集（值全部取自最新快照，无需历史曲线）：
// disk/io/diskio/diskrw 的 param=挂载点（disk/io 空=各盘最大值，diskio/diskrw
// 读写空=各设备去重合计、diskrw 另带使用率进度条取各盘最大值）；
// net 的 param=网口（空=按展示网口合计），一个格子同时显示上下行；
// cpumem 资源占用组合格（CPU+内存上下两条进度条，无 param）。
// net_rx/net_tx 仅兼容存量单项配置，新建不再提供。
var tileMetrics = map[string]bool{
	"cpu": false, "mem": false, "swap": false,
	"load1": false, "load5": false, "load15": false,
	"tcp_est": false, "uptime": false,
	"disk": false, "io": false, "net": false, "diskio": false, "diskrw": false,
	"net_rx": false, "net_tx": false, "cpumem": false, "temp": false,
	// 用量查询数据源（486，指标键 ai_ 前缀）：zhipu=周额度/5小时窗口已用百分比、
	// deepseek=总余额、edgeone=近24h流量/带宽峰值/请求数
	"ai_zhipu_week": false, "ai_zhipu_5h": false, "ai_deepseek_balance": false,
	"ai_eo_traffic": false, "ai_eo_peak": false, "ai_eo_requests": false,
}

// aiTileProvider 用量指标 → 要求的账户服务商（486）：配置时校验所选账户
// provider 匹配，防张冠李戴（智谱指标配到 EdgeOne 账户上永远取不到数）。
var aiTileProvider = map[string]string{
	"ai_zhipu_week": "zhipu", "ai_zhipu_5h": "zhipu",
	"ai_deepseek_balance": "deepseek",
	"ai_eo_traffic":       "edgeone", "ai_eo_peak": "edgeone", "ai_eo_requests": "edgeone",
}

// checkTileSource 校验格子数据源（总览/主页数据监控共用，486）：用量指标→
// 用量账户存在且服务商匹配（serverID 归零）；服务器指标→服务器存在（aiID 归零）。
// 返回归一化后的 (serverID, aiID)，校验失败返回错误文案。
func checkTileSource(st *store.Store, metric string, serverID, aiID int64) (int64, int64, string) {
	if prov, ok := aiTileProvider[metric]; ok {
		if aiID <= 0 {
			return 0, 0, "请选择用量账户"
		}
		acc, err := st.GetAiAccount(aiID)
		if err != nil || acc == nil {
			return 0, 0, "用量账户不存在"
		}
		if acc.Provider != prov {
			return 0, 0, "账户服务商与指标不匹配"
		}
		return 0, aiID, ""
	}
	if serverID <= 0 {
		return 0, 0, "请选择服务器"
	}
	if srv, err := st.GetServer(serverID); err != nil || srv == nil {
		return 0, 0, "服务器不存在"
	}
	return serverID, 0, ""
}

// DashMetricHandler 总览页重点指标：配置 CRUD（数值由前端从 /servers 最新快照计算）。
type DashMetricHandler struct {
	st *store.Store
}

type dashMetricView struct {
	ID         int64  `json:"id"`
	ServerID   int64  `json:"server_id"`
	AIID       int64  `json:"ai_id"`
	AIName     string `json:"ai_name"`
	ServerName string `json:"server_name"`
	Metric     string `json:"metric"`
	Param      string `json:"param"`
	Label      string `json:"label"`
}

// List 返回全部重点指标配置（附服务器名/用量账户名；删除服务器或用量账户时
// 级联清理对应格子，正常不缺）。
func (h *DashMetricHandler) List(c *gin.Context) {
	list, err := h.st.ListDashMetrics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询重点指标失败"})
		return
	}
	srvNames := map[int64]string{}
	aiNames := map[int64]string{}
	out := make([]dashMetricView, 0, len(list))
	for _, m := range list {
		v := dashMetricView{ID: m.ID, AIID: m.AIID, Metric: m.Metric, Param: m.Param, Label: m.Label}
		if m.AIID > 0 {
			name, ok := aiNames[m.AIID]
			if !ok {
				if acc, err := h.st.GetAiAccount(m.AIID); err == nil && acc != nil {
					name = acc.Name
				}
				aiNames[m.AIID] = name
			}
			v.AIName = name
		} else {
			name, ok := srvNames[m.ServerID]
			if !ok {
				if srv, err := h.st.GetServer(m.ServerID); err == nil && srv != nil {
					name = srv.Name
				}
				srvNames[m.ServerID] = name
			}
			v.ServerID = m.ServerID
			v.ServerName = name
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{"metrics": out})
}

type createDashMetricReq struct {
	ServerID int64  `json:"server_id"`
	AIID     int64  `json:"ai_id"`
	Metric   string `json:"metric"`
	Param    string `json:"param"`
	Label    string `json:"label"`
}

// Create 新增重点指标：校验服务器存在、指标合法、标签必填、总数不超过上限。
func (h *DashMetricHandler) Create(c *gin.Context) {
	var req createDashMetricReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if _, ok := tileMetrics[req.Metric]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的指标"})
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写展示标签"})
		return
	}
	serverID, aiID, msg := checkTileSource(h.st, req.Metric, req.ServerID, req.AIID)
	if msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if n, err := h.st.CountDashMetrics(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询重点指标数失败"})
		return
	} else if n >= maxDashMetrics {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多配置 %d 个重点指标", maxDashMetrics)})
		return
	}
	m, err := h.st.CreateDashMetric(serverID, aiID, req.Metric, strings.TrimSpace(req.Param), req.Label)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"metric": dashMetricView{
		ID: m.ID, ServerID: m.ServerID, AIID: m.AIID, Metric: m.Metric, Param: m.Param, Label: m.Label,
	}})
}

// Update 编辑一个重点指标的配置（校验同 Create；不含排序）。
func (h *DashMetricHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的重点指标 ID"})
		return
	}
	var req createDashMetricReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if _, ok := tileMetrics[req.Metric]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的指标"})
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写展示标签"})
		return
	}
	serverID, aiID, msg := checkTileSource(h.st, req.Metric, req.ServerID, req.AIID)
	if msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if err := h.st.UpdateDashMetric(id, serverID, aiID, req.Metric, strings.TrimSpace(req.Param), req.Label); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Delete 删除一个重点指标。
func (h *DashMetricHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的重点指标 ID"})
		return
	}
	if err := h.st.DeleteDashMetric(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Order 保存拖拽后的重点指标顺序（ids 按新顺序传入，写入 sort 1..n）。
func (h *DashMetricHandler) Order(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if len(req.IDs) > maxDashMetrics {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量超出上限"})
		return
	}
	if err := h.st.ReorderDashMetrics(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存顺序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
