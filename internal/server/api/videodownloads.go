package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/videodl"
	"lz-monitor/internal/ytdlp"
)

// VideoDlHandler 视频下载（TODO 4，追剧页「视频下载」tab）：单发下载任务
// CRUD + 停止 + Cookie 设置。执行流派：server=server 本机执行器（videodl
// Manager 单 worker）；agent=所选 push 机器上报捎带领取（yt-dlp 在该机跑）。
type VideoDlHandler struct {
	st *store.Store
}

// minVideoDlAgentVersion 支持视频下载任务（VideoDlTasks 捎带通道）的最低
// agent 版本（v1.0.41 引入，执行器随版首发）。旧版 agent 收到任务会静默丢弃
//（PostResult 解码忽略未知字段），任务只能干等 6h 租约超时——下发前守卫 +
// 行上明示，机器升级后自动领取。
const minVideoDlAgentVersion = "1.0.41"

// agentSupportsVideoDl 判 agent 版本是否支持视频下载执行器。
func agentSupportsVideoDl(ver string) bool {
	return agentVersionAtLeast(ver, minVideoDlAgentVersion)
}

// videoDlView 列表项：任务行 + 执行机器名与版本守卫标记（agent 流派才有）。
type videoDlView struct {
	store.VideoDlTask
	ServerName string `json:"server_name"`
	StaleAgent bool   `json:"stale_agent"`
}

// List 下载历史（最新 200 条）+ server 本机 yt-dlp 可用性。
func (h *VideoDlHandler) List(c *gin.Context) {
	list, err := h.st.ListVideoDlTasks(200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询下载列表失败"})
		return
	}
	servers := map[int64]struct {
		name  string
		ver   string
		valid bool
	}{}
	if rows, err := h.st.ListServers(); err == nil {
		for _, s := range rows {
			servers[s.ID] = struct {
				name  string
				ver   string
				valid bool
			}{s.Name, s.AgentVersion, true}
		}
	}
	out := make([]videoDlView, 0, len(list))
	for i := range list {
		v := videoDlView{VideoDlTask: list[i]}
		if list[i].Executor == "agent" {
			if s, ok := servers[list[i].ServerID]; ok {
				v.ServerName = s.name
				v.StaleAgent = !agentSupportsVideoDl(s.ver)
			}
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{
		"tasks":        out,
		"server_ytdlp": ytdlp.Available(),
	})
}

type videoDlCreateReq struct {
	URL      string `json:"url"`
	SaveDir  string `json:"save_dir"`
	Executor string `json:"executor"`
	ServerID int64  `json:"server_id"`
}

// Create 新建下载任务：URL/目录校验 → 入队（同 URL 活跃唯一）。server 流派
// 由 videodl Manager 3s 内领取；agent 流派等该机下一轮上报捎带。
func (h *VideoDlHandler) Create(c *gin.Context) {
	var req videoDlCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填 http(s) 视频页面链接"})
		return
	}
	if len(req.URL) > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "视频地址过长"})
		return
	}
	req.SaveDir = strings.TrimSpace(req.SaveDir)
	if req.SaveDir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填保存目录（执行机器上的绝对路径）"})
		return
	}
	executor := req.Executor
	if executor != "server" && executor != "agent" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "下载位置必须是本服务器或 agent 机器"})
		return
	}
	if executor == "agent" {
		s, err := h.st.GetServer(req.ServerID)
		if err != nil || s == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "执行机器不存在"})
			return
		}
		if s.CollectMode != "push" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "执行机器必须是 Agent 上报（push）模式的机器"})
			return
		}
	} else {
		req.ServerID = 0
	}
	id, err := h.st.CreateVideoDlTask(req.URL, req.SaveDir, executor, req.ServerID, videodl.Cookies(h.st))
	if errors.Is(err, store.ErrVideoDlActive) {
		c.JSON(http.StatusConflict, gin.H{"error": "该视频已有进行中的下载任务"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "下载任务入队失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": id})
}

// Stop 停止：pending 即刻作废；leased 打停止标记——server 流派由本机执行器
// 2s 内感知杀进程，agent 流派随下一轮上报捎带停止命令。
func (h *VideoDlHandler) Stop(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	t, err := h.st.GetVideoDlTaskState(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "下载任务不存在"})
		return
	}
	switch t.State {
	case "pending":
		h.st.ExpirePendingVideoDlTask(id)
		c.JSON(http.StatusOK, gin.H{"code": 0, "stopped": true})
	case "leased":
		h.st.RequestVideoDlStop(id)
		c.JSON(http.StatusOK, gin.H{"code": 0, "stopped": false, "requested": true})
	default:
		c.JSON(http.StatusOK, gin.H{"code": 0, "stopped": false})
	}
}

// Delete 删除一条终态记录（活跃任务先停止；已落盘文件不动——文件归用户）。
func (h *VideoDlHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.DeleteVideoDlTask(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "任务进行中，请先停止再删除"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// GetSettings Cookie 设置（不回显，只回 has）。
func (h *VideoDlHandler) GetSettings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"cookies_has": videodl.Cookies(h.st) != ""})
}

type videoDlSettingsReq struct {
	Cookies string `json:"cookies"`
}

// PutSettings 写 Cookie（Netscape 格式文件内容；空串=清除）。
func (h *VideoDlHandler) PutSettings(c *gin.Context) {
	var req videoDlSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	if err := videodl.SetCookies(h.st, req.Cookies); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "设置保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// videoDlTasksFor 取该机器名下待领视频下载任务（每轮至多 2 个）。agent 版本
// 过旧（v1.0.41 前无执行器）不下发——旧版收到会静默丢弃、任务白白挂到租约
// 超时；留在 pending，机器升级上报新版本后下一轮自动领取。
func (h *AgentReportHandler) videoDlTasksFor(serverID int64) []proto.VideoDlTask {
	s, err := h.st.GetServer(serverID)
	if err != nil || s == nil || !agentSupportsVideoDl(s.AgentVersion) {
		return nil
	}
	tasks, err := h.st.ClaimVideoDlTasks("agent", serverID, 2)
	if err != nil || len(tasks) == 0 {
		return nil
	}
	out := make([]proto.VideoDlTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, proto.VideoDlTask{TaskID: t.ID, URL: t.URL, SaveDir: t.SaveDir, Cookies: t.Cookies})
	}
	return out
}

// videoDlStopsFor 该机器名下请求停止的下载任务 id（agent 捎带领取置中断标志；
// agent 杀进程回传失败后 Finish 清标记）。
func (h *AgentReportHandler) videoDlStopsFor(serverID int64) []int64 {
	ids, err := h.st.StoppedVideoDlTasks(serverID)
	if err != nil || len(ids) == 0 {
		return nil
	}
	return ids
}

// maxVideoDlResultBody 结果体上限（路径/标题级文本，1MB 富余）。
const maxVideoDlResultBody = 1 << 20

// VideoDlResult agent 回传视频下载任务结果：Finish 幂等（成败皆终态）。
func (h *AgentReportHandler) VideoDlResult(c *gin.Context) {
	id := h.agentID(c)
	if id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未知的上报密钥"})
		return
	}
	var req struct {
		TaskID int64                   `json:"task_id"`
		Result proto.VideoDlTaskResult `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, maxVideoDlResultBody)).Decode(&req); err != nil || req.TaskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	t, err := h.st.GetVideoDlTaskState(req.TaskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if t.Executor != "agent" || t.ServerID != id {
		c.JSON(http.StatusForbidden, gin.H{"error": "任务不属于该执行机器"})
		return
	}
	fileName, fileSize := req.Result.FileName, req.Result.FileSize
	if _, err := h.st.FinishVideoDlTask(t.ID, req.Result.Title, fileName, fileSize, req.Result.Error); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务收尾失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// VideoDlProgress agent 回传下载进度心跳：校验归属 → 回写进度（仅 leased）。
func (h *AgentReportHandler) VideoDlProgress(c *gin.Context) {
	id := h.agentID(c)
	if id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未知的上报密钥"})
		return
	}
	var req proto.VideoDlProgress
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, 64<<10)).Decode(&req); err != nil || req.TaskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	t, err := h.st.GetVideoDlTaskState(req.TaskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if t.Executor != "agent" || t.ServerID != id {
		c.JSON(http.StatusForbidden, gin.H{"error": "任务不属于该执行机器"})
		return
	}
	pct := int(req.Percent)
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	if err := h.st.UpdateVideoDlProgress(t.ID, pct, ytdlp.FormatSpeed(req.Speed), req.Title); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "进度写入失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
