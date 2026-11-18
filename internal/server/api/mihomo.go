package api

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/mihomo"
	"lz-monitor/internal/server/store"
)

// MihomoHandler mihomo 控制台（TODO 7）：配置存取 + 读面板/写操作入队 + 任务
// 轮询。数据面全走 net_tasks 中转（purpose=mihomo，用户选定的 push agent 内网
// 代执行）；secret 加密落库、只在派发时注入 Authorization 头，接口一律不回显。
type MihomoHandler struct {
	st *store.Store
}

// mihomoConfigView 配置回显（secret 只给 has_secret）。
type mihomoConfigView struct {
	ServerID   int64  `json:"server_id"`
	ServerName string `json:"server_name"`
	BaseURL    string `json:"base_url"`
	TestURL    string `json:"test_url"`
	HasSecret  bool   `json:"has_secret"`
	Ready      bool   `json:"ready"`
}

func (h *MihomoHandler) view(cfg *mihomo.Config) mihomoConfigView {
	v := mihomoConfigView{
		ServerID: cfg.ServerID, BaseURL: cfg.BaseURL, TestURL: cfg.TestURL,
		HasSecret: cfg.Secret != "", Ready: mihomo.Ready(cfg),
	}
	if s, err := h.st.GetServer(cfg.ServerID); err == nil && s != nil {
		v.ServerName = s.Name
		if s.CollectMode != "push" {
			v.Ready = false // 出口机器换成了非 push 模式：任务无人领取
		}
	} else {
		v.Ready = false
	}
	return v
}

// Config 当前配置。
func (h *MihomoHandler) Config(c *gin.Context) {
	c.JSON(http.StatusOK, h.view(mihomo.LoadConfig(h.st)))
}

type mihomoConfigReq struct {
	ServerID int64  `json:"server_id"`
	BaseURL  string `json:"base_url"`
	Secret   string `json:"secret"` // 空=沿用现值（口径同 B 站 SESSDATA 不回显留空沿用）
	TestURL  string `json:"test_url"`
}

// validateBaseURL 控制器地址校验：http(s) + 私网/回环 IP 字面量（对齐 agent
// 侧 mihomo purpose 白名单口径，域名/公网地址在 agent 会被拒）。
func validateBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("地址格式不对（示例 http://127.0.0.1:9090）")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || (!ip.IsLoopback() && !ip.IsPrivate()) {
		return "", errors.New("控制器地址须为私网/回环 IP（agent 白名单只放行内网目标），如 http://127.0.0.1:9090")
	}
	return strings.TrimSpace(raw), nil
}

// SaveConfig 保存设置。
func (h *MihomoHandler) SaveConfig(c *gin.Context) {
	var req mihomoConfigReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	srv, err := h.st.GetServer(req.ServerID)
	if err != nil || srv == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "执行机器不存在"})
		return
	}
	if srv.CollectMode != "push" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "执行机器须为 Agent 上报（push）模式——中转任务由其上报捎带领取"})
		return
	}
	base, err := validateBaseURL(req.BaseURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	testURL := strings.TrimSpace(req.TestURL)
	if testURL != "" && !strings.HasPrefix(testURL, "http://") && !strings.HasPrefix(testURL, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "测延迟地址须为 http(s) URL，留空用默认"})
		return
	}
	cfg := &mihomo.Config{ServerID: req.ServerID, BaseURL: base, Secret: req.Secret, TestURL: testURL}
	if err := mihomo.SaveConfig(h.st, cfg, req.Secret != ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, h.view(mihomo.LoadConfig(h.st)))
}

// mihomoEnqueueResp 入队响应（前端 2s 轮询 /mihomo/task/:id）。
func (h *MihomoHandler) enqueue(c *gin.Context, op mihomo.Op) {
	cfg := mihomo.LoadConfig(h.st)
	if !mihomo.Ready(cfg) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先完成 mihomo 设置（出口机器与控制器地址）"})
		return
	}
	taskID, err := mihomo.Enqueue(h.st, cfg, op)
	if errors.Is(err, store.ErrNetTaskActive) {
		// 同 ref_key 任务进行中（如概览轮询未收尾）：轮询它，不重复入队
		if active, aerr := h.st.ActiveNetTask(mihomo.Purpose, op.RefKey); aerr == nil && active != nil {
			taskID = active.ID
		}
	}
	if taskID <= 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务入队失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": taskID})
}

// Query 读面板入队：op = version|overview|proxies|connections|rules。
func (h *MihomoHandler) Query(c *gin.Context) {
	var req struct {
		Op string `json:"op"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	ops := map[string]mihomo.Op{
		"version":     mihomo.VersionOp(),
		"overview":    mihomo.OverviewOp(),
		"proxies":     mihomo.ProxiesOp(),
		"connections": mihomo.ConnectionsOp(),
		"rules":       mihomo.RulesOp(),
	}
	op, ok := ops[req.Op]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未知读取面: " + req.Op})
		return
	}
	h.enqueue(c, op)
}

// Op 写操作入队：action = switch|delay|group_delay|close|close_all|mode|reload。
func (h *MihomoHandler) Op(c *gin.Context) {
	var req struct {
		Action string `json:"action"`
		Group  string `json:"group"`
		Name   string `json:"name"`
		ID     string `json:"id"`
		Mode   string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	cfg := mihomo.LoadConfig(h.st)
	var op mihomo.Op
	switch req.Action {
	case "switch":
		if req.Group == "" || req.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少组名或节点名"})
			return
		}
		op = mihomo.SwitchOp(req.Group, req.Name)
	case "delay":
		if req.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少节点名"})
			return
		}
		op = mihomo.DelayOp(req.Name, cfg.TestURL)
	case "group_delay":
		if req.Group == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少组名"})
			return
		}
		op = mihomo.GroupDelayOp(req.Group, cfg.TestURL)
	case "close":
		if req.ID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少连接 ID"})
			return
		}
		op = mihomo.CloseConnOp(req.ID)
	case "close_all":
		op = mihomo.CloseAllConnsOp()
	case "mode":
		if req.Mode != "rule" && req.Mode != "global" && req.Mode != "direct" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "模式只支持 rule/global/direct"})
			return
		}
		op = mihomo.ModeOp(req.Mode)
	case "reload":
		op = mihomo.ReloadOp()
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "未知操作: " + req.Action})
		return
	}
	h.enqueue(c, op)
}

// Task 前端轮询任务状态（2s 一跳，轻量判态先行；done 才取全行解析）。
// 仅接受 mihomo 用途任务，防跨用途串台。
func (h *MihomoHandler) Task(c *gin.Context) {
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	task, err := h.st.GetNetTaskState(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	out := gin.H{"state": task.State}
	switch task.State {
	case "pending", "leased":
		out["running"] = true
		c.JSON(http.StatusOK, out)
		return
	case "failed", "expired":
		msg := task.Error
		if msg == "" {
			msg = "出口 agent 未能完成请求（任务 " + task.State + "）"
		}
		out["error"] = msg
		c.JSON(http.StatusOK, out)
		return
	}
	full, err := h.st.GetNetTask(taskID)
	if err != nil || full.Purpose != mihomo.Purpose {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	data, perr := mihomo.ParseResult(full.RefKey, full.StatusCode, full.Result, full.Error)
	if perr != nil {
		out["error"] = perr.Error()
		c.JSON(http.StatusOK, out)
		return
	}
	out["data"] = data
	c.JSON(http.StatusOK, out)
}
