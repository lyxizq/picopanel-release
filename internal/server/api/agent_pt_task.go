package api

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/tvfollow"
)

// PT 中转抓取任务（391 扩展，2026-10-05 用户指定：家里 NAS push 模式 agent
// 零暴露中转）：①report 响应捎带该 agent 名下的待抓任务（agent 每 interval
// 上报一次，顺路领取，零额外轮询）；②独立回传端点收结果。
//
// 防死循环纪律（store/pt_task.go 定稿）：任务一次性、失败即终态、回传幂等
// （done 后重复丢弃）、结果消费只动库不触网。agent 侧零重试。

// maxPtTaskResultBody 结果体上限：单页 HTML 峰值约 1~2MB，4 页不在结果里
// （结果只有解析后的快照），给足余量即可。
const maxPtTaskResultBody = 4 << 20

// agentID 鉴权：X-Agent-Token 恒时比对 push 密钥定位服务器 ID（与 Report 同口径）。
func (h *AgentReportHandler) agentID(c *gin.Context) int64 {
	token := c.GetHeader("X-Agent-Token")
	if token == "" {
		return 0
	}
	toks, err := h.st.PushTokenList()
	if err != nil {
		return 0
	}
	id := int64(0)
	for _, t := range toks {
		if subtle.ConstantTimeCompare([]byte(token), []byte(t.Token)) == 1 {
			id = t.ID
		}
	}
	return id
}

// TaskResult 回传执行结果：校验任务归属该 agent → FinishPtTask（幂等，仅首次
// 消费）→ 快照写站点主表。任何失败都不要求 agent 重试（防死循环：agent 收到
// 非 200 直接丢弃任务，任务由租约超时机制兜底回收）。
func (h *AgentReportHandler) TaskResult(c *gin.Context) {
	id := h.agentID(c)
	if id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未知的上报密钥"})
		return
	}
	var req struct {
		TaskID   int64           `json:"task_id"`
		Snapshot json.RawMessage `json:"snapshot"`
		Error    string          `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, maxPtTaskResultBody)).Decode(&req); err != nil || req.TaskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	task, err := h.st.GetPtTask(req.TaskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if task.ServerID != id {
		c.JSON(http.StatusForbidden, gin.H{"error": "任务不属于该出口服务器"})
		return
	}
	result := &store.PtTaskResult{Error: req.Error}
	if len(req.Snapshot) > 0 {
		var snap store.PtSnapshot
		if err := json.Unmarshal(req.Snapshot, &snap); err != nil {
			result.Error = "快照解析失败: " + err.Error()
		} else {
			result.Snapshot = &snap
		}
	}
	first, err := h.st.FinishPtTask(task.ID, result)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务收尾失败"})
		return
	}
	if first {
		if err := h.st.ConsumePtTaskResult(task.SiteID, result); err != nil {
			// 消费失败只影响数据落库（任务已终态），绝不重派执行
			c.JSON(http.StatusInternalServerError, gin.H{"error": "结果入库失败"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// maxNetTaskBodyLimit 中转请求响应体上限（agent 侧已限 4MB，这里对齐兜底）。
const maxNetTaskBodyLimit = 4 << 20

// NetTaskResult 通用中转任务（548）结果回传：校验任务归属该出口 →
// FinishNetTask（幂等）→ detail 用途即时消费（进度回写 + 推送）。
// 与 PT 同纪律：任何失败都不要求 agent 重试（非 200 直接丢弃，租约超时兜底）。
func (h *AgentReportHandler) NetTaskResult(c *gin.Context) {
	id := h.agentID(c)
	if id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未知的上报密钥"})
		return
	}
	var req struct {
		TaskID     int64  `json:"task_id"`
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
		Error      string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, maxNetTaskBodyLimit)).Decode(&req); err != nil || req.TaskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	task, err := h.st.GetNetTask(req.TaskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if task.ServerID != id {
		c.JSON(http.StatusForbidden, gin.H{"error": "任务不属于该出口服务器"})
		return
	}
	if len(req.Body) > maxNetTaskBodyLimit {
		req.Body = req.Body[:maxNetTaskBodyLimit]
	}
	first, err := h.st.FinishNetTask(task.ID, req.StatusCode, req.Body, req.Error)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务收尾失败"})
		return
	}
	if first && task.Purpose == tvfollow.PurposeDetail {
		fresh, _ := h.st.GetNetTask(task.ID) // 取终态行（含 result）
		if fresh != nil {
			if err := tvfollow.ConsumeDetailResult(h.st, fresh); err != nil {
				log.Printf("追剧 detail 结果消费失败 (task=%d): %v", task.ID, err)
			}
		}
	}
	if first && task.Purpose == tvfollow.PurposePT {
		fresh, _ := h.st.GetNetTask(task.ID)
		if fresh != nil {
			if err := tvfollow.ConsumePTResult(h.st, fresh); err != nil {
				log.Printf("追剧 PT 进度消费失败 (task=%d): %v", task.ID, err)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// netTasksFor 取该 agent 名下待领通用中转任务（至多 4 个防单轮上报拖太久；
// URL 已解密、目标 host 白名单在 agent 侧兜底）。
func (h *AgentReportHandler) netTasksFor(serverID int64) []gin.H {
	tasks, err := h.st.ClaimNetTasks(serverID, 4)
	if err != nil || len(tasks) == 0 {
		return nil
	}
	out := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, gin.H{
			"task_id":        t.ID,
			"purpose":        t.Purpose, // 用途（mihomo 类走 purpose 级内网放行，TODO 7）
			"url":            t.URL,
			"method":         t.Method,
			"headers":        parseNetTaskHeaders(t.Headers),
			"body":           t.Body,
			"timeout_sec":    t.TimeoutSec,
			"allow_any_host": t.AllowAnyHost, // PT 类任务跳过 agent 白名单（仍 per-host 限速）
		})
	}
	return out
}

// appTasksFor 取该机名下待领应用操作任务（54，至多 4 个防单轮上报拖太久；
// 动词白名单在 CreateAppTask 入队时已校验，agent 侧执行还有同款兜底）。
func (h *AgentReportHandler) appTasksFor(serverID int64) []gin.H {
	tasks, err := h.st.ClaimAppTasks(serverID, 4)
	if err != nil || len(tasks) == 0 {
		return nil
	}
	out := make([]gin.H, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, gin.H{"task_id": t.ID, "kind": t.Kind, "target": t.Target, "action": t.Action})
	}
	return out
}

// AppTaskResult 应用操作任务（54）结果回传：校验任务归属该机 → FinishAppTask
// （幂等，仅首次消费）。与 PT/中转同纪律：任何失败都不要求 agent 重试
// （非 200 直接丢弃，租约超时兜底）。
func (h *AgentReportHandler) AppTaskResult(c *gin.Context) {
	id := h.agentID(c)
	if id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未知的上报密钥"})
		return
	}
	var req struct {
		TaskID  int64  `json:"task_id"`
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, 64<<10)).Decode(&req); err != nil || req.TaskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	task, err := h.st.GetAppTaskState(req.TaskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if task.ServerID != id {
		c.JSON(http.StatusForbidden, gin.H{"error": "任务不属于该出口服务器"})
		return
	}
	if _, err := h.st.FinishAppTask(task.ID, req.OK, req.Message); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务收尾失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// parseNetTaskHeaders 还原 k:v\n 序列化的请求头。
func parseNetTaskHeaders(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, ": "); ok && k != "" {
			out[k] = v
		}
	}
	return out
}
