package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/bilifollow"
	"lz-monitor/internal/server/store"
)

// BiliFollowHandler B 站追更（TODO 6，追剧页「追更」tab）：配置 CRUD +
// 手动/自动追更任务。抓取在所选 push agent 机器上执行（SESSDATA 可选提音质），
// server 只入队/消费。
type BiliFollowHandler struct {
	st *store.Store
}

// biliFollowView 列表项：配置行 + 活跃任务态 + 执行机器名。
type biliFollowView struct {
	store.BiliFollow
	TaskState  string `json:"task_state"` // pending/leased=进行中
	ServerName string `json:"server_name"`
	StaleAgent bool   `json:"stale_agent"` // 执行机器 agent 版本过旧（不含追更执行器，任务下发会被旧版忽略——用户指定场景修复）
}

// minBiliAgentVersion 支持追更任务（BiliTasks 捎带通道）的最低 agent 版本
// （v1.0.28 引入，追更执行器随版首发）。旧版 agent 收到任务会静默丢弃，任务
// 只能干等 2h 租约超时——下发前守卫 + 行上明示。
const minBiliAgentVersion = "1.0.28"

// agentVersionAtLeast 判 agent 版本串是否 ≥ 最低版本（vX.Y.Z[-N-gHASH] 取前缀
// 逐段比；空串=旧版无版本上报，视为不支持）。视频下载守卫同款（videodownloads.go）。
func agentVersionAtLeast(ver, minVer string) bool {
	ver = strings.TrimPrefix(strings.TrimSpace(ver), "v")
	ver = strings.SplitN(ver, "-", 2)[0]
	if ver == "" {
		return false
	}
	minParts := strings.Split(minVer, ".")
	verParts := strings.Split(ver, ".")
	for i := 0; i < len(minParts); i++ {
		vp, mp := 0, 0
		if i < len(verParts) {
			fmt.Sscanf(verParts[i], "%d", &vp)
		}
		fmt.Sscanf(minParts[i], "%d", &mp)
		if vp != mp {
			return vp > mp
		}
	}
	return true
}

// agentSupportsBili 判 agent 版本串是否 ≥ 最低追更版本。
func agentSupportsBili(ver string) bool {
	return agentVersionAtLeast(ver, minBiliAgentVersion)
}

func (h *BiliFollowHandler) view(f *store.BiliFollow) biliFollowView {
	v := biliFollowView{BiliFollow: *f}
	if t, err := h.st.ActiveBiliTaskByFollow(f.ID); err == nil && t != nil {
		v.TaskState = t.State
	}
	if s, err := h.st.GetServer(f.AgentServerID); err == nil && s != nil {
		v.ServerName = s.Name
		v.StaleAgent = !agentSupportsBili(s.AgentVersion)
	}
	return v
}

// List 全部追更配置。
func (h *BiliFollowHandler) List(c *gin.Context) {
	list, err := h.st.ListBiliFollows()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询追更列表失败"})
		return
	}
	out := make([]biliFollowView, 0, len(list))
	for i := range list {
		out = append(out, h.view(&list[i]))
	}
	c.JSON(http.StatusOK, gin.H{"follows": out})
}

type biliFollowCreateReq struct {
	UpName        string `json:"up_name"`
	Address       string `json:"address"`
	AgentServerID int64  `json:"agent_server_id"`
	SaveDir       string `json:"save_dir"`
	AutoEnabled   *bool  `json:"auto_enabled"`
}

// Create 新增追更：先落行（未解析态）→ 入队 probe 任务异步解析（574 口径：
// API 秒回 task_id，同步等会撞边缘 524）→ 前端轮询 /bili-follows-task/:id。
func (h *BiliFollowHandler) Create(c *gin.Context) {
	var req biliFollowCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	f := &store.BiliFollow{
		Enabled: true, UpName: req.UpName, Address: req.Address,
		AgentServerID: req.AgentServerID, SaveDir: req.SaveDir, AutoEnabled: true,
	}
	if req.AutoEnabled != nil {
		f.AutoEnabled = *req.AutoEnabled
	}
	if err := h.st.CreateBiliFollow(f); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.checkAgentServer(f.AgentServerID); err != nil {
		h.st.DeleteBiliFollow(f.ID)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	taskID, err := bilifollow.EnqueueProbe(h.st, f)
	if err != nil {
		h.st.DeleteBiliFollow(f.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "解析任务入队失败：" + err.Error()})
		return
	}
	fresh, _ := h.st.GetBiliFollow(f.ID)
	if fresh == nil {
		fresh = f
	}
	v := h.view(fresh)
	c.JSON(http.StatusOK, gin.H{"follow": v, "task_id": taskID})
}

// checkAgentServer 执行机器校验：存在且为 push 上报模式（任务捎带通道仅 push 有）。
func (h *BiliFollowHandler) checkAgentServer(id int64) error {
	s, err := h.st.GetServer(id)
	if err != nil || s == nil {
		return errors.New("执行机器不存在")
	}
	if s.CollectMode != "push" {
		return errors.New("执行机器必须是 Agent 上报（push）模式的机器")
	}
	return nil
}

type biliFollowUpdateReq struct {
	UpName        *string `json:"up_name"`
	Address       *string `json:"address"`
	AgentServerID *int64  `json:"agent_server_id"`
	SaveDir       *string `json:"save_dir"`
	AutoEnabled   *bool   `json:"auto_enabled"`
	Enabled       *bool   `json:"enabled"`
}

// Update 编辑：地址/up 主名字变更会清解析字段并重新入队 probe（重解析子目录名
// 也可能变化）；其余字段直接更新。
func (h *BiliFollowHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	f, err := h.st.GetBiliFollow(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追更配置不存在"})
		return
	}
	var req biliFollowUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	if req.AgentServerID != nil && *req.AgentServerID != f.AgentServerID {
		if err := h.checkAgentServer(*req.AgentServerID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	reprobe := false
	if req.Address != nil && *req.Address != f.Address {
		reprobe = true
	}
	if req.UpName != nil && *req.UpName != f.UpName && f.Kind == "" {
		reprobe = true // 未解析成功的行改 up 名也重试解析；已解析行 up 名仅展示
	}
	u := &store.BiliFollowUpdate{
		UpName: req.UpName, Address: req.Address,
		AgentServerID: req.AgentServerID, SaveDir: req.SaveDir,
		AutoEnabled: req.AutoEnabled, Enabled: req.Enabled,
	}
	if err := h.st.UpdateBiliFollow(id, u); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追更配置不存在"})
		return
	}
	taskID := int64(0)
	if reprobe {
		if err := h.st.ResetBiliResolve(id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "重置解析状态失败"})
			return
		}
		fresh, _ := h.st.GetBiliFollow(id)
		if fresh != nil {
			f = fresh
		}
		if taskID, err = bilifollow.EnqueueProbe(h.st, f); err != nil && !errors.Is(err, store.ErrBiliTaskActive) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "解析任务入队失败：" + err.Error()})
			return
		}
	}
	out, _ := h.st.GetBiliFollow(id)
	if out == nil {
		out = f
	}
	c.JSON(http.StatusOK, gin.H{"follow": h.view(out), "task_id": taskID})
}

// Delete 删除配置（任务一并作废；agent 机器上已下载的文件不删——文件归用户）。
func (h *BiliFollowHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	h.st.ExpireBiliTasksByFollow(id)
	if err := h.st.DeleteBiliFollow(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追更配置不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Run 手动追更：入队（未种子化=首抓最新 10 条，之后全量增量），返回 task_id
// 供前端轮询；已有活跃任务时 409。
func (h *BiliFollowHandler) Run(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	f, err := h.st.GetBiliFollow(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追更配置不存在"})
		return
	}
	if f.Mid == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "地址尚未解析成功，等解析完成后再追更"})
		return
	}
	taskID, err := bilifollow.EnqueueRun(h.st, f)
	if errors.Is(err, store.ErrBiliTaskActive) {
		c.JSON(http.StatusConflict, gin.H{"error": "该追更已有进行中的任务"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "追更任务入队失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task_id": taskID})
}

// Items 已抓记录最近 10 条（16：详情弹层只看最近，权威台账仍在 bili_items 表；
// total 供前端标题「最后 N 条 / 共 M 条」）。
func (h *BiliFollowHandler) Items(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	items, err := h.st.ListBiliItems(id, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询已抓记录失败"})
		return
	}
	total := h.st.CountBiliItems(id)
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

// Stop 手动停止追更（8）：无活跃任务=无事可停直接清标记返回；pending 任务
// 即刻作废（agent 还没拿走，立即生效）；leased 任务置停止标记、随 agent 上报
// 响应捎带停止命令，agent 完成当前条目后中止并回传 stopped。
func (h *BiliFollowHandler) Stop(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if _, err := h.st.GetBiliFollow(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追更配置不存在"})
		return
	}
	active, _ := h.st.ActiveBiliTaskByFollow(id)
	if active == nil {
		h.st.SetBiliStopFlag(id, false)
		c.JSON(http.StatusOK, gin.H{"code": 0, "stopped": false})
		return
	}
	if active.State == "pending" {
		h.st.ExpirePendingBiliTask(id)
		h.st.SetBiliStopFlag(id, false)
		c.JSON(http.StatusOK, gin.H{"code": 0, "stopped": true})
		return
	}
	h.st.SetBiliStopFlag(id, true)
	c.JSON(http.StatusOK, gin.H{"code": 0, "stopped": false, "requested": true})
}

// biliStopsFor 该机器名下请求停止的追更 id（agent 捎带领取置中断标志；agent
// 回传 stopped 或任务离场时由消费/调度清标记）。
func (h *AgentReportHandler) biliStopsFor(serverID int64) []int64 {
	ids, err := h.st.StopRequestedBiliFollows()
	if err != nil || len(ids) == 0 {
		return nil
	}
	// 只发给标记所属的执行机器（follow_id → agent_server_id 校验）
	var out []int64
	for _, id := range ids {
		if f, err := h.st.GetBiliFollow(id); err == nil && f.AgentServerID == serverID {
			out = append(out, id)
		}
	}
	return out
}

// TaskStatus 前端轮询（pending/leased=running；终态即消费并回最新行——probe
// 回填解析字段、run 回写统计，ConsumeTask 幂等）。
func (h *BiliFollowHandler) TaskStatus(c *gin.Context) {
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	task, err := h.st.GetBiliTaskState(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	switch task.State {
	case "pending", "leased":
		c.JSON(http.StatusOK, gin.H{"state": task.State, "running": true})
		return
	case "failed", "expired":
		msg := task.Error
		if msg == "" {
			msg = "执行机器未能完成任务"
		}
		c.JSON(http.StatusOK, gin.H{"state": task.State, "error": msg})
		return
	}
	full, err := h.st.GetBiliTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if err := bilifollow.ConsumeTask(h.st, full); err != nil {
		c.JSON(http.StatusOK, gin.H{"state": "done", "error": err.Error()})
		return
	}
	fresh, ferr := h.st.GetBiliFollow(full.FollowID)
	if ferr != nil {
		c.JSON(http.StatusOK, gin.H{"state": "done"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"state": "done", "follow": h.view(fresh)})
}

// GetSettings 追更设置（SESSDATA 不回显，只回 has）。
func (h *BiliFollowHandler) GetSettings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"sessdata_has": bilifollow.Sessdata(h.st) != ""})
}

type biliSettingsReq struct {
	Sessdata string `json:"sessdata"`
}

// PutSettings 写 SESSDATA（空串=清除）。
func (h *BiliFollowHandler) PutSettings(c *gin.Context) {
	var req biliSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	if err := bilifollow.SetSessdata(h.st, req.Sessdata); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "设置保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// BiliTaskResult agent 回传追更任务结果：Finish 幂等 → 首次即消费（probe 回填
// 解析字段 / run 回写统计）。与中转任务同纪律：非 200 丢弃，租约超时兜底。
func (h *AgentReportHandler) BiliTaskResult(c *gin.Context) {
	id := h.agentID(c)
	if id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未知的上报密钥"})
		return
	}
	var req struct {
		TaskID int64             `json:"task_id"`
		Result proto.BiliTaskResult `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, maxBiliResultBody)).Decode(&req); err != nil || req.TaskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	task, err := h.st.GetBiliTask(req.TaskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if task.ServerID != id {
		c.JSON(http.StatusForbidden, gin.H{"error": "任务不属于该执行机器"})
		return
	}
	resBody, _ := json.Marshal(req.Result)
	errMsg := req.Result.Error
	first, err := h.st.FinishBiliTask(task.ID, string(resBody), errMsg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "任务收尾失败"})
		return
	}
	if first {
		fresh, _ := h.st.GetBiliTask(task.ID)
		if fresh != nil {
			if cerr := bilifollow.ConsumeTask(h.st, fresh); cerr != nil {
				log.Printf("B站追更结果消费失败 (task=%d): %v", task.ID, cerr)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// biliTasksFor 取该机器名下待领追更任务（每轮至多 2 个——单条要下几分钟）。
// agent 版本过旧（v1.0.28 前无追更执行器）不下发——旧版收到会静默丢弃、任务
// 白白挂到租约超时；留在 pending，机器升级上报新版本后下一轮自动领取。
func (h *AgentReportHandler) biliTasksFor(serverID int64) []proto.BiliTask {
	if s, err := h.st.GetServer(serverID); err != nil || s == nil || !agentSupportsBili(s.AgentVersion) {
		return nil
	}
	tasks, err := h.st.ClaimBiliTasks(serverID, 2)
	if err != nil || len(tasks) == 0 {
		return nil
	}
	out := make([]proto.BiliTask, 0, len(tasks))
	for _, t := range tasks {
		var p proto.BiliTask
		if json.Unmarshal([]byte(t.Payload), &p) != nil {
			continue
		}
		p.TaskID = t.ID
		out = append(out, p)
	}
	return out
}

// maxBiliResultBody 结果体上限（条目清单最多几百行标题，1MB 富余）。
const maxBiliResultBody = 1 << 20
