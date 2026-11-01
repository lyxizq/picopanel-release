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

// MembershipHandler 会员到期提醒（492）：业务/会员名/到期日期 CRUD + 统计。
type MembershipHandler struct {
	st *store.Store
}

// NewMembershipHandler 创建 handler。
func NewMembershipHandler(st *store.Store) *MembershipHandler { return &MembershipHandler{st: st} }

// List 全部会员记录（到期日升序，带现算剩余天数）。
func (h *MembershipHandler) List(c *gin.Context) {
	list, err := h.st.ListMemberships()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询会员记录失败"})
		return
	}
	total, expiring, err := h.st.MembershipStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询会员统计失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list, "total": total, "expiring": expiring})
}

type membershipReq struct {
	Biz        string `json:"biz" binding:"required"`
	Name       string `json:"name"`
	StartDate  string `json:"start_date"` // 可选：进度条按起止区间算，空 = 回落剩余/周期
	ExpireDate string `json:"expire_date" binding:"required"`
	PeriodDays int    `json:"period_days"`
}

// Create 新建会员记录。
func (h *MembershipHandler) Create(c *gin.Context) {
	var req membershipReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（名称与到期日期必填）"})
		return
	}
	m := &store.Membership{Biz: req.Biz, Name: req.Name, StartDate: req.StartDate, ExpireDate: req.ExpireDate, PeriodDays: req.PeriodDays}
	if err := h.st.CreateMembership(m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, _ := h.st.GetMembership(m.ID) // 回读带现算展示字段（剩余天数/进度）
	if out == nil {
		out = m
	}
	c.JSON(http.StatusOK, out)
}

// Update 编辑会员记录（到期日变动自动重置 15 天提醒标记）。
func (h *MembershipHandler) Update(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req membershipReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（名称与到期日期必填）"})
		return
	}
	m := &store.Membership{ID: id, Biz: req.Biz, Name: req.Name, StartDate: req.StartDate, ExpireDate: req.ExpireDate, PeriodDays: req.PeriodDays}
	if err := h.st.UpdateMembership(m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := h.st.GetMembership(id)
	if err != nil {
		c.JSON(http.StatusOK, m)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Delete 删除会员记录（物理删除）。
func (h *MembershipHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.st.DeleteMembership(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Reorder 拖拽排序保存（按 ids 顺序写 sort 1..n）。路由用 /memberships-reorder
// 前缀：与 /memberships/:id 参数段同层的静态段会让 gin 路由树 panic（回收站同款）。
func (h *MembershipHandler) Reorder(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数不完整（ids 必填）"})
		return
	}
	if err := h.st.ReorderMemberships(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存排序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// TestPush 立即按会员到期文案发一次测试推送：站内消息必发，三渠道按
// 「提醒事项」推送开关分发（off = 开关未开，ok = 已推送，其余为失败原因）。
func (h *MembershipHandler) TestPush(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	m, err := h.st.GetMembership(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "会员记录不存在"})
		return
	}
	subject := "到期提醒：" + m.Biz
	body := fmt.Sprintf("「%s · %s」剩余 %d 天（%s 到期）。〔测试推送〕", m.Biz, m.Name, m.RemainingDays, m.ExpireDate)
	msg := &store.Message{
		Type: "membership", Title: subject, Content: body,
		RefType: "membership", RefID: m.ID, CreatedAt: time.Now().Unix(),
	}
	if err := h.st.CreateMessage(msg); err != nil {
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
