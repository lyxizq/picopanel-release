package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/tvfollow"
)

// TVFollowHandler 追剧（548）：TMDB 条目追踪列表 CRUD + 搜索（经通用中转
// 任务由 push agent 代查）+ 手动立即检查 + Emby 在库检查（549，server 直连）。
type TVFollowHandler struct {
	st *store.Store
}

// tvFollowView 列表项：条目 + 海报完整地址（浏览器直连 image.tmdb.org）。
type tvFollowView struct {
	store.TVFollow
	Poster   string `json:"poster"`
	Checking bool   `json:"checking"` // 有进行中的 detail 中转任务（前端转圈）
}

func (h *TVFollowHandler) view(f *store.TVFollow) tvFollowView {
	v := tvFollowView{TVFollow: *f, Poster: tvfollow.PosterURL(f.PosterPath)}
	if t, err := h.st.ActiveNetTask(tvfollow.PurposeDetail, strconv.FormatInt(f.ID, 10)); err == nil && t != nil {
		v.Checking = true
	}
	return v
}

// List 全部追剧条目（新添加在前）。
func (h *TVFollowHandler) List(c *gin.Context) {
	list, err := h.st.ListTVFollows()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询追剧列表失败"})
		return
	}
	out := make([]tvFollowView, 0, len(list))
	for i := range list {
		out = append(out, h.view(&list[i]))
	}
	c.JSON(http.StatusOK, gin.H{"follows": out, "last_sweep": tvfollow.LastSweepAt(h.st)})
}

type tvFollowCreateReq struct {
	Type         string `json:"type"`
	TmdbID       int64  `json:"tmdb_id"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	PosterPath   string `json:"poster_path"`
	FirstAirDate string `json:"first_air_date"`
	Status       string `json:"status"`
	Cast         string `json:"cast"`     // 候选卡带出的主要演员（550，后续随 detail 回写刷新）
	Overview     string `json:"overview"` // 候选卡带出的简介（554 详情弹层用）
}

// Create 添加追剧条目：落库 → Emby 在库检查（直连，未配则跳过）→ 配置齐备时
// 立即入队一次 detail 查询建立基线（首次消费只记进度不推送）。
func (h *TVFollowHandler) Create(c *gin.Context) {
	var req tvFollowCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	f := &store.TVFollow{
		Type: req.Type, TmdbID: req.TmdbID, Name: req.Name,
		OriginalName: req.OriginalName, PosterPath: req.PosterPath,
		FirstAirDate: req.FirstAirDate, Status: req.Status, Cast: req.Cast, Overview: req.Overview,
	}
	if err := h.st.CreateTVFollow(f); err != nil {
		if errors.Is(err, store.ErrTVFollowExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "该条目已在追剧列表中"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 549：添加即查一次 Emby 在库（查不到/未配不动标记，留给 detail 消费刷新）
	if in, name, ok := tvfollow.CheckEmbyFor(h.st, f); ok {
		h.st.SetTVFollowEmby(f.ID, in, name, time.Now().Unix())
	}
	// 基线查询：配置不全时留给每日轮询（配置好了自动开始查）
	if tvfollow.Ready(h.st) {
		if _, err := h.st.CreateNetTask(tvfollow.PurposeDetail, strconv.FormatInt(f.ID, 10),
			tvfollow.EgressServerID(h.st), tvfollow.DetailURL(f.Type, f.TmdbID, tvfollow.TmdbKey(h.st)),
			"GET", nil, "", 20, false); err == nil || errors.Is(err, store.ErrNetTaskActive) {
			h.st.MarkTVFollowQueued(f.ID)
		}
	}
	fresh, _ := h.st.GetTVFollow(f.ID)
	if fresh == nil {
		fresh = f
	}
	c.JSON(http.StatusOK, h.view(fresh))
}

// Search 剧名搜索（添加弹层候选）：经通用中转任务由出口 agent 代查 TMDB，
// 同步等回传（agent 默认 10s 一报）。
func (h *TVFollowHandler) Search(c *gin.Context) {
	typ := c.Query("type")
	if typ != store.TVFollowTV && typ != store.TVFollowMovie {
		typ = store.TVFollowTV
	}
	q := c.Query("q")
	if len([]rune(q)) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "剧名至少 2 个字"})
		return
	}
	if !tvfollow.Ready(h.st) {
		c.JSON(http.StatusBadRequest, gin.H{"error": tvfollow.ErrNoConfig.Error()})
		return
	}
	taskURL := tvfollow.SearchURL(typ, q, tvfollow.TmdbKey(h.st))
	refKey := tvfollow.SearchRefKey(typ, q)
	taskID, err := h.st.CreateNetTask(tvfollow.PurposeAPI, refKey, tvfollow.EgressServerID(h.st), taskURL, "GET", nil, "", 20, false)
	if errors.Is(err, store.ErrNetTaskActive) {
		if active, aerr := h.st.ActiveNetTask(tvfollow.PurposeAPI, refKey); aerr == nil && active != nil {
			taskID = active.ID // 同关键词任务进行中：轮询它，不重复入队
		}
	}
	if taskID <= 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "搜索任务入队失败"})
		return
	}
	// 异步模式（573）：入队即回 task_id，前端轮询 /tvfollows-task/:id 取结果——
	// 同步等回传的 22s 会撞 ESA 边缘回源超时（524），API 秒回永不超时
	c.JSON(http.StatusOK, gin.H{"task_id": taskID, "kind": "search", "search_type": typ, "keyword": q})
}

// TaskStatus 前端轮询中转任务状态（573）：pending/leased=进行中；终态按用途
// 回业务结果——search=候选列表（含主演/Emby 小标，Attach* 在此异步补齐），
// detail=消费后的最新行。state=failed/expired 带 error 文案。
func (h *TVFollowHandler) TaskStatus(c *gin.Context) {
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	// 先走轻量判态（不取 result/headers/body 大列——前端 2s 一跳，轮询期取
	// 4MB result 纯浪费）；仅终态需要业务结果时才按 id 取全行
	task, err := h.st.GetNetTaskState(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	out := gin.H{"state": task.State}
	switch task.State {
	case "done":
		out["state"] = "done"
	case "failed", "expired":
		msg := task.Error
		if msg == "" {
			msg = "出口 agent 未能完成查询（任务 " + task.State + "）"
		}
		out["error"] = msg
		c.JSON(http.StatusOK, out)
		return
	default: // pending / leased：进行中
		out["running"] = true
		c.JSON(http.StatusOK, out)
		return
	}
	full, err := h.st.GetNetTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	task = full

	switch task.Purpose {
	case tvfollow.PurposeAPI:
		if strings.HasPrefix(task.RefKey, "cast:") {
			c.JSON(http.StatusOK, gin.H{"state": "done"}) // 主演任务不在此轮询（防御）
			return
		}
		// 搜索任务：ref_key 形如 tv:关键词。基础候选立即返回；主演/Emby 小标的
		// 附加（AttachCast 同步等回传最长 25s）放后台 goroutine，完成后把增强
		// JSON 回写 task.result——在轮询请求里同步做会再次挂死请求撞 524（573 返工）
		parts := strings.SplitN(task.RefKey, ":", 2)
		typ := store.TVFollowTV
		if len(parts) == 2 && parts[1] != "" && parts[0] == store.TVFollowMovie {
			typ = store.TVFollowMovie
		}
		if task.Error != "" || task.StatusCode != 200 {
			c.JSON(http.StatusOK, gin.H{"state": "done", "error": "TMDB 查询失败：" + taskMessage(task)})
			return
		}
		var wrapper struct {
			Enhanced   bool            `json:"enhanced"`
			Candidates json.RawMessage `json:"candidates"`
		}
		if json.Unmarshal([]byte(task.Result), &wrapper) == nil && wrapper.Enhanced {
			c.JSON(http.StatusOK, gin.H{"state": "done", "enhanced": true, "candidates": json.RawMessage(wrapper.Candidates)})
			return
		}
		candidates, err := tvfollow.ParseSearch(typ, task.Result)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"state": "done", "error": err.Error()})
			return
		}
		if h.beginEnhance(task.ID) {
			candidates := candidates
			go func() {
				defer h.finishEnhance(task.ID) // panic 也要放行下一次增强，否则该任务永久卡「增强中」
				defer func() {
					if p := recover(); p != nil {
						log.Printf("追剧候选增强 panic（task=%d，已隔离）: %v", task.ID, p)
					}
				}()
				tvfollow.AttachCast(h.st, typ, candidates)  // 前 5 条补主要演员（550）
				tvfollow.AttachEmbyStatus(h.st, candidates) // 标题旁 Emby 在库小标（551）
				if enhanced, merr := json.Marshal(gin.H{"enhanced": true, "candidates": candidates}); merr == nil {
					h.st.UpdateNetTaskResult(task.ID, string(enhanced))
				}
			}()
		}
		c.JSON(http.StatusOK, gin.H{"state": "done", "enhanced": false, "candidates": candidates})
	case tvfollow.PurposeDetail:
		if err := tvfollow.ConsumeDetailResult(h.st, task); err != nil {
			log.Printf("追剧 detail 结果消费失败 (task=%d): %v", task.ID, err)
		}
		id, perr := strconv.ParseInt(task.RefKey, 10, 64)
		if perr != nil {
			c.JSON(http.StatusOK, gin.H{"state": "done"})
			return
		}
		if fresh, ferr := h.st.GetTVFollow(id); ferr == nil {
			c.JSON(http.StatusOK, gin.H{"state": "done", "follow": h.view(fresh)})
		} else {
			c.JSON(http.StatusOK, gin.H{"state": "done"})
		}
	default:
		c.JSON(http.StatusOK, gin.H{"state": "done"})
	}
}

// Check 手动立即检查：入队 detail（活跃则等它）→ 同步等回传 → 消费 → 回最新行。
func (h *TVFollowHandler) Check(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	follow, err := h.st.GetTVFollow(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追剧条目不存在"})
		return
	}
	if !tvfollow.Ready(h.st) {
		c.JSON(http.StatusBadRequest, gin.H{"error": tvfollow.ErrNoConfig.Error()})
		return
	}
	taskID, err := h.st.CreateNetTask(tvfollow.PurposeDetail, strconv.FormatInt(id, 10),
		tvfollow.EgressServerID(h.st), tvfollow.DetailURL(follow.Type, follow.TmdbID, tvfollow.TmdbKey(h.st)),
		"GET", nil, "", 20, false)
	if errors.Is(err, store.ErrNetTaskActive) {
		// 已有活跃任务（如刚被定时轮入队）：复用它等结果；撞索引后查不到（极短
		// 窗口）时稍候重试一次入队——上一任务恰好终态离开的并发态
		if active, aerr := h.st.ActiveNetTask(tvfollow.PurposeDetail, strconv.FormatInt(id, 10)); aerr == nil && active != nil {
			taskID = active.ID
		} else {
			time.Sleep(300 * time.Millisecond)
			taskID, err = h.st.CreateNetTask(tvfollow.PurposeDetail, strconv.FormatInt(id, 10),
				tvfollow.EgressServerID(h.st), tvfollow.DetailURL(follow.Type, follow.TmdbID, tvfollow.TmdbKey(h.st)),
				"GET", nil, "", 20, false)
		}
	}
	if err != nil && !errors.Is(err, store.ErrNetTaskActive) {
		log.Printf("追剧检查任务入队失败 (follow=%d): %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "检查任务入队失败：" + err.Error()})
		return
	}
	if taskID <= 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "检查任务入队失败（任务状态异常，请重试）"})
		return
	}
	h.st.MarkTVFollowQueued(id)
	// 异步模式（573）：同搜索，入队即回，前端轮询取消费后的行数据
	c.JSON(http.StatusOK, gin.H{"task_id": taskID, "kind": "detail"})
}

// EmbyCheck 手动刷新在库状态（549）：server 直连 Emby，不走过转通道。
func (h *TVFollowHandler) EmbyCheck(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	follow, err := h.st.GetTVFollow(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追剧条目不存在"})
		return
	}
	in, name, ok := tvfollow.CheckEmbyFor(h.st, follow)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Emby 未配置或查询失败"})
		return
	}
	h.st.SetTVFollowEmby(id, in, name, time.Now().Unix())
	fresh, _ := h.st.GetTVFollow(id)
	if fresh == nil {
		fresh = follow
	}
	c.JSON(http.StatusOK, h.view(fresh))
}

type tvFollowEnableReq struct {
	Enabled *bool `json:"enabled"`
}

// Enable 启停（停用退出每日轮询）。
func (h *TVFollowHandler) Enable(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	var req tvFollowEnableReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	if err := h.st.SetTVFollowEnabled(id, *req.Enabled); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追剧条目不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

type tvFollowShortDramaReq struct {
	On *bool `json:"on"`
}

// ShortDrama 手动标记短剧（58）：卡片标签与详情弹层显示「短剧」。纯人工标注，
// TMDB detail 回写不含此列不会被轮询覆盖。
func (h *TVFollowHandler) ShortDrama(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	var req tvFollowShortDramaReq
	if err := c.ShouldBindJSON(&req); err != nil || req.On == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数格式错误"})
		return
	}
	if err := h.st.SetTVFollowShortDrama(id, *req.On); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追剧条目不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Delete 删除条目（其进行中的 detail 任务回传后因条目不存在自然作废）。
func (h *TVFollowHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.DeleteTVFollow(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "追剧条目不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ResourceLinks 各资源站搜索页链接（565）：按关键词拼好，前端新窗打开。
// PT 三站仅列用户已配置的站点。
func (h *TVFollowHandler) ResourceLinks(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("keyword"))
	if keyword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少关键词"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"links": tvfollow.ResourceLinks(h.st, keyword)})
}

// enhanceInFlight 搜索增强去重（多路轮询同时见 done 只触发一次后台增强）。
var enhanceInFlight sync.Map

func (h *TVFollowHandler) beginEnhance(taskID int64) bool {
	if _, loaded := enhanceInFlight.LoadOrStore(taskID, true); loaded {
		return false
	}
	return true
}

func (h *TVFollowHandler) finishEnhance(taskID int64) {
	enhanceInFlight.Delete(taskID)
}

// taskMessage 回传错误摘要（HTTP 状态或 agent 错误）。
func taskMessage(t *store.NetTask) string {
	if t.Error != "" {
		return t.Error
	}
	return "HTTP " + strconv.Itoa(t.StatusCode)
}
