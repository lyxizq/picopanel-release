package api

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
)

// RuleHandler 处理告警规则 CRUD。
type RuleHandler struct {
	st  *store.Store
	eng *engine.Engine
}

var validMetrics = map[string]bool{
	"cpu": true, "mem": true, "swap": true,
	"load1": true, "load5": true, "load15": true,
	"tcp_est": true, "disk": true, "net_rx": true, "net_tx": true,
	"port": true,
}

// MetricLabels 指标中文名，用于告警规则等处的展示与校验。
var MetricLabels = map[string]string{
	"cpu": "CPU 使用率", "mem": "内存使用率", "swap": "Swap 使用率",
	"load1": "1 分钟负载", "load5": "5 分钟负载", "load15": "15 分钟负载",
	"tcp_est": "TCP 连接数", "disk": "磁盘使用率", "net_rx": "网卡下行速率", "net_tx": "网卡上行速率",
	"port": "端口监听",
}

// List 返回全部规则。
func (h *RuleHandler) List(c *gin.Context) {
	rules, err := h.st.ListRules()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询规则失败"})
		return
	}
	if rules == nil {
		rules = []store.Rule{}
	}
	c.JSON(http.StatusOK, gin.H{"rules": rules})
}

type ruleReq struct {
	Name        string  `json:"name" binding:"required,max=100"`
	Metric      string  `json:"metric" binding:"required"`
	Op          string  `json:"op" binding:"required,oneof=> <"`
	Threshold   float64 `json:"threshold"`
	DurationMin int     `json:"duration_min"` // 分钟，0=满足即触发
	ServerID    *int64  `json:"server_id"`    // null=全局
	Param       string  `json:"param"`        // disk=mount / net=iface，空=取最大
	Enabled     *bool   `json:"enabled"`
}

func (r *ruleReq) validate() string {
	if !validMetrics[r.Metric] {
		return "metric 无效"
	}
	if _, ok := MetricLabels[r.Metric]; !ok {
		return "metric 无效"
	}
	if r.Metric == "port" {
		if _, port := engine.ParsePortParam(r.Param); port == 0 {
			return "端口规则参数无效（写法如 tcp/443、udp/53 或纯数字端口）"
		}
	}
	if r.Metric == "disk" && r.DurationMin > 0 {
		return "磁盘使用率不再保留历史曲线，仅支持「满足即触发」（持续时长须为 0）"
	}
	if r.DurationMin < 0 || r.DurationMin > 60 {
		return "持续时长需在 0~60 分钟之间"
	}
	if r.ServerID != nil && *r.ServerID <= 0 {
		return "server_id 无效"
	}
	return ""
}

func (r *ruleReq) toRule() *store.Rule {
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return &store.Rule{
		Name: r.Name, Metric: r.Metric, Op: r.Op, Threshold: r.Threshold,
		DurationSec: int64(r.DurationMin) * 60,
		ServerID:    r.ServerID, Param: r.Param, Enabled: enabled,
	}
}

// Create 创建规则并刷新引擎缓存。
func (h *RuleHandler) Create(c *gin.Context) {
	var req ruleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}
	if msg := req.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	rule := req.toRule()
	if err := h.st.CreateRule(rule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	// 引擎还在用旧规则评估的话，改规则等于没生效——必须让调用方知道
	if err := h.eng.ReloadRules(); err != nil {
		log.Printf("刷新规则引擎失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则已保存但刷新引擎失败，请稍后刷新规则页或重启服务端"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rule": rule})
}

func (h *RuleHandler) ruleID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的规则 ID"})
		return 0, false
	}
	return id, true
}

// Update 更新规则并刷新引擎缓存。
func (h *RuleHandler) Update(c *gin.Context) {
	id, ok := h.ruleID(c)
	if !ok {
		return
	}
	var req ruleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
		return
	}
	if msg := req.validate(); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	rule := req.toRule()
	rule.ID = id
	if err := h.st.UpdateRule(rule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	// 引擎还在用旧规则评估的话，改规则等于没生效——必须让调用方知道
	if err := h.eng.ReloadRules(); err != nil {
		log.Printf("刷新规则引擎失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则已保存但刷新引擎失败，请稍后刷新规则页或重启服务端"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rule": rule})
}

// Delete 删除规则并刷新引擎缓存。
func (h *RuleHandler) Delete(c *gin.Context) {
	id, ok := h.ruleID(c)
	if !ok {
		return
	}
	if err := h.st.DeleteRule(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	// 引擎还在用旧规则评估的话，改规则等于没生效——必须让调用方知道
	if err := h.eng.ReloadRules(); err != nil {
		log.Printf("刷新规则引擎失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "规则已保存但刷新引擎失败，请稍后刷新规则页或重启服务端"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
