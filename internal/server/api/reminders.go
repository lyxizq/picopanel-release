package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

// ReminderHandler 提醒事项：分组 CRUD + 事项 CRUD + 完成看板。
type ReminderHandler struct {
	st *store.Store
}

// NewReminderHandler 创建 handler。
func NewReminderHandler(st *store.Store) *ReminderHandler { return &ReminderHandler{st: st} }

// Board 看板数据：今天（未完成且到期 ≤ 今天，逾期归此）/ 计划（未来，按日期升序）/
// 最近已完成 / 分组清单 / 回收站，一次拉全。
func (h *ReminderHandler) Board(c *gin.Context) {
	today, plan, done, err := h.st.ReminderBoard()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询提醒事项失败"})
		return
	}
	groups, err := h.st.ListReminderGroups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分组失败"})
		return
	}
	trash, err := h.st.ListReminderTrash()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询回收站失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"today": today, "plan": plan, "done": done, "groups": groups, "trash": trash})
}

type reminderReq struct {
	GroupID   int64  `json:"group_id"`
	ParentID  int64  `json:"parent_id"` // 父任务（仅新建时生效；0 = 顶级）
	Title     string `json:"title" binding:"required"`
	Note      string `json:"note"`
	DueDate   string `json:"due_date" binding:"required"`
	DueTime   string `json:"due_time"`
	Repeat    string `json:"repeat"`
	Important bool   `json:"important"`
	MyDay     bool   `json:"my_day"`
	Notify    *bool  `json:"notify"` // 是否提醒：nil = 新建默认开、编辑保持原值
}

func (h *ReminderHandler) reminderFromReq(c *gin.Context) (*store.Reminder, bool) {
	var req reminderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（标题与提醒日期必填）"})
		return nil, false
	}
	notify := true
	if req.Notify != nil {
		notify = *req.Notify
	}
	return &store.Reminder{
		GroupID: req.GroupID, ParentID: req.ParentID, Notify: notify,
		Title: req.Title, Note: req.Note,
		DueDate: req.DueDate, DueTime: req.DueTime, Repeat: req.Repeat,
		Important: req.Important, MyDay: req.MyDay,
	}, true
}

// Create 新建提醒事项。
func (h *ReminderHandler) Create(c *gin.Context) {
	r, ok := h.reminderFromReq(c)
	if !ok {
		return
	}
	if err := h.st.CreateReminder(r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, _ := h.st.GetReminder(r.ID)
	c.JSON(http.StatusOK, out)
}

// Update 编辑提醒事项（parent_id 不可改；notify 未传时保持原值）。
func (h *ReminderHandler) Update(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req reminderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（标题与提醒日期必填）"})
		return
	}
	cur, err := h.st.GetReminder(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "提醒事项不存在"})
		return
	}
	notify := cur.Notify
	if req.Notify != nil {
		notify = *req.Notify
	}
	r := &store.Reminder{
		ID: id, GroupID: req.GroupID, ParentID: cur.ParentID, Notify: notify,
		Title: req.Title, Note: req.Note,
		DueDate: req.DueDate, DueTime: req.DueTime, Repeat: req.Repeat,
		Important: req.Important, MyDay: req.MyDay,
	}
	if err := h.st.UpdateReminder(r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, _ := h.st.GetReminder(id)
	c.JSON(http.StatusOK, out)
}

// Delete 删除提醒事项（软删进回收站，同系列未完成后续一并）。
func (h *ReminderHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.st.DeleteReminder(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 回收站（路由前缀 /reminders-trash，与 /reminders/:id 参数段分层） ----

// RestoreTrash 从回收站恢复单条（原分组已删回落未分组）。
func (h *ReminderHandler) RestoreTrash(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	r, err := h.st.RestoreReminder(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "reminder": r})
}

// DeleteTrash 彻底删除回收站中的单条（物理删除，不可恢复）。
func (h *ReminderHandler) DeleteTrash(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.st.HardDeleteReminder(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// EmptyTrash 清空回收站（全部软删条目物理删除）。
func (h *ReminderHandler) EmptyTrash(c *gin.Context) {
	n, err := h.st.EmptyReminderTrash()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清空回收站失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "removed": n})
}

// Complete 完成提醒：打完成标记；重复事项自动生成下一条（响应里带 next）。
func (h *ReminderHandler) Complete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	next, err := h.st.CompleteReminder(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "next": next})
}

// Reopen 恢复已完成事项为未完成；周期任务恢复时撤回自动生成的下一条
// （removed_next 告知前端是否撤回过，再次完成不会重复生成）。
func (h *ReminderHandler) Reopen(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	removed, err := h.st.ReopenReminder(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "removed_next": removed != nil})
}

// TestPush 立即按「到期提醒」文案给该事项发一次测试推送：站内消息必发；
// WxPusher/Webhook/Web Push 各按总开关 + 事项提醒细分开关分发（off = 开关未开，
// ok = 已推送，其余为失败原因）。用于验证推送链路，不影响提醒标记。
func (h *ReminderHandler) TestPush(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	r, err := h.st.GetReminder(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "提醒事项不存在"})
		return
	}
	subject := "提醒：" + r.Title
	body := fmt.Sprintf("「%s」已到到期时间（%s %s），记得处理。〔测试推送〕", r.Title, r.DueDate, r.EffectiveDueTime())
	if r.Note != "" {
		body += "\n备注：" + r.Note
	}
	m := &store.Message{
		Type: "reminder", Title: subject, Content: body,
		RefType: "reminder", RefID: r.ID, CreatedAt: time.Now().Unix(),
	}
	if err := h.st.CreateMessage(m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "站内消息写入失败"})
		return
	}
	res := gin.H{}
	if wxpusher.On(h.st) && wxpusher.ReminderWXOn(h.st) {
		res["wxpusher"] = "ok"
		if err := wxpusher.SendFromStore(h.st, subject, body); err != nil {
			res["wxpusher"] = err.Error()
		}
	} else {
		res["wxpusher"] = "off"
	}
	if webhook.On(h.st) && webhook.ReminderOn(h.st) {
		res["webhook"] = "ok"
		if err := webhook.SendFromStore(h.st, subject, body); err != nil {
			res["webhook"] = err.Error()
		}
	} else {
		res["webhook"] = "off"
	}
	if webpush.On(h.st) && webpush.ReminderOn(h.st) {
		res["webpush"] = "ok"
		if err := webpush.SendFromStore(h.st, subject, body); err != nil {
			res["webpush"] = err.Error()
		}
	} else {
		res["webpush"] = "off"
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "results": res})
}

// ---- 分组 ----

type reminderGroupReq struct {
	Name string `json:"name" binding:"required"`
	Sort int    `json:"sort"`
}

// CreateGroup 新建分组。
func (h *ReminderHandler) CreateGroup(c *gin.Context) {
	var req reminderGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写分组名"})
		return
	}
	id, err := h.st.CreateReminderGroup(req.Name, req.Sort)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建分组失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "id": id})
}

// UpdateGroup 改分组名/排序。
func (h *ReminderHandler) UpdateGroup(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req reminderGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写分组名"})
		return
	}
	if err := h.st.UpdateReminderGroup(id, req.Name, req.Sort); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存分组失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteGroup 删分组（组内事项落回未分组）。
func (h *ReminderHandler) DeleteGroup(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.st.DeleteReminderGroup(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除分组失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
