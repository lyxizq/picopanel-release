package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// 应用负载操作（54 飞牛 fnOS 集成）：机器明细弹层里对 Docker 容器/虚拟机
// 下发启停指令。列表数据随 /servers/:id 的 latest 快照下发（无独立端点），
// 这里只有「创建操作任务」与「轮询任务状态」两个端点；执行链路 = 入队 →
// push agent 上报捎带领取 → agent 本机执行（docker.sock / virsh，白名单
// 动词）→ 结果回传。仅 push 模式机器可操作（pull/ssh/node 无下发通道）。

// CreateAppAction 创建一条容器/虚拟机操作任务：POST /servers/:id/apps/action，
// 体 {"kind":"docker|vm","target":"...","action":"start|stop|..."}。
// 类型+动词白名单 server/agent 双侧校验；同机同目标同时最多 1 个活跃任务。
func (h *ServerHandler) CreateAppAction(c *gin.Context) {
	id, ok := h.serverID(c)
	if !ok {
		return
	}
	srv, err := h.st.GetServer(id)
	if err != nil || srv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "服务器不存在"})
		return
	}
	if srv.CollectMode != "push" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "仅 Agent 上报（push）模式的机器支持远程操作"})
		return
	}
	var req struct {
		Kind   string `json:"kind"`
		Target string `json:"target"`
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Target == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if !store.ValidAppTaskAction(req.Kind, req.Action) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的操作"})
		return
	}
	taskID, err := h.st.CreateAppTask(id, req.Kind, req.Target, req.Action)
	if err != nil {
		if errors.Is(err, store.ErrAppTaskActive) {
			c.JSON(http.StatusConflict, gin.H{"error": "该目标已有进行中的操作，请稍候"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建操作任务失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": taskID})
}

// AppTaskState 轮询操作任务状态：GET /apps-task/:id（顶层路径，避免与
// /servers/:id 参数路由冲突）。done=执行完成（ok 区分成败），failed/expired
// 均按失败呈现，pending/leased=执行中。
func (h *ServerHandler) AppTaskState(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	t, err := h.st.GetAppTaskState(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"state": t.State, "ok": t.OK, "message": t.Message})
}
