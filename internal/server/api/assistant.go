package api

import (
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/assistant"
	"lz-monitor/internal/server/store"
)

// AssistantHandler 个人助理（2026-10-26 批）：助理设置、日历聚合与 ICS 订阅/导出、倒数日。
type AssistantHandler struct {
	st           *store.Store
	offlineAfter int64                // 离线判定窗口（秒），早报巡检段与服务器列表同口径
	unreadFn     func() map[int64]int // 邮件未读统计（早报用），可为 nil
}

// NewAssistantHandler 创建 handler。
func NewAssistantHandler(st *store.Store, offlineAfter int64, unreadFn func() map[int64]int) *AssistantHandler {
	return &AssistantHandler{st: st, offlineAfter: offlineAfter, unreadFn: unreadFn}
}

// ---- 助理设置 ----

// GetSettings 读助理配置。
func (h *AssistantHandler) GetSettings(c *gin.Context) {
	c.JSON(http.StatusOK, assistant.LoadConfig(h.st))
}

// PutSettings 写助理配置（整组提交）。
func (h *AssistantHandler) PutSettings(c *gin.Context) {
	var cfg assistant.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := assistant.SaveConfig(h.st, cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, assistant.LoadConfig(h.st))
}

// BriefNow 立即生成并推送一份早报（设置页「立即推送」按钮）。
func (h *AssistantHandler) BriefNow(c *gin.Context) {
	assistant.BriefNow(h.st, h.offlineAfter, h.unreadFn, time.Now())
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// BriefPreview 只生成不推送（设置页预览）。
func (h *AssistantHandler) BriefPreview(c *gin.Context) {
	title, body := assistant.BuildBrief(h.st, h.offlineAfter, h.unreadFn, time.Now())
	c.JSON(http.StatusOK, gin.H{"title": title, "body": body})
}

// ---- 日历 ----

// Holidays 休息日设置弹层数据：远程同步时刻/最近错误 + 各年份（内置/远程）清单。
func (h *AssistantHandler) Holidays(c *gin.Context) {
	c.JSON(http.StatusOK, assistant.GetHolidayStatus(h.st))
}

// SyncHolidays 立即拉取当年与下一年远程数据（同步执行，失败原因随状态返回而非报错码，
// 弹层照常展示现有清单）。
func (h *AssistantHandler) SyncHolidays(c *gin.Context) {
	if err := assistant.SyncHolidayCN(h.st, time.Now()); err != nil {
		log.Printf("手动同步节假日数据失败: %v", err)
	}
	c.JSON(http.StatusOK, assistant.GetHolidayStatus(h.st))
}

// Calendar 聚合 [from, to] 的事件与日标记（?from=YYYY-MM-DD&to=YYYY-MM-DD，最多 62 天）。
func (h *AssistantHandler) Calendar(c *gin.Context) {
	loc := time.Now().Location()
	from, err1 := time.ParseInLocation("2006-01-02", c.Query("from"), loc)
	to, err2 := time.ParseInLocation("2006-01-02", c.Query("to"), loc)
	if err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from/to 须为 YYYY-MM-DD"})
		return
	}
	events, days, err := assistant.Range(h.st, from, to)
	if err != nil {
		log.Printf("日历聚合部分来源失败: %v", err)
	}
	if events == nil {
		events = []assistant.Event{}
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "days": days, "known_years": assistant.HolidayKnownYears()})
}

// ListSubs 日历订阅清单。
func (h *AssistantHandler) ListSubs(c *gin.Context) {
	subs, err := h.st.ListCalendarSubs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询订阅失败"})
		return
	}
	if subs == nil {
		subs = []store.CalendarSub{}
	}
	c.JSON(http.StatusOK, gin.H{"items": subs})
}

type calendarSubReq struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Color   string `json:"color"`
	Enabled *bool  `json:"enabled"`
}

func (r *calendarSubReq) normalize() (store.CalendarSub, error) {
	u, err := assistant.ValidateSubURL(r.URL)
	if err != nil {
		return store.CalendarSub{}, err
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = "日历订阅"
	}
	if len([]rune(name)) > 40 {
		return store.CalendarSub{}, errors.New("名称最长 40 字")
	}
	color := strings.TrimSpace(r.Color)
	if len(color) > 20 {
		color = ""
	}
	en := true
	if r.Enabled != nil {
		en = *r.Enabled
	}
	return store.CalendarSub{Name: name, URL: u, Color: color, Enabled: en}, nil
}

// CreateSub 新增订阅并立即抓一次（抓取失败不阻断创建，错误记在 last_error）。
func (h *AssistantHandler) CreateSub(c *gin.Context) {
	var req calendarSubReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	sub, err := req.normalize()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id, err := h.st.CreateCalendarSub(&sub)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sub.ID = id
	if err := assistant.RefreshSub(h.st, sub, time.Now()); err != nil {
		log.Printf("日历订阅「%s」首次抓取失败: %v", sub.Name, err)
	}
	out, _ := h.st.GetCalendarSub(id)
	c.JSON(http.StatusOK, out)
}

// UpdateSub 编辑订阅（地址变了重新抓取）。
func (h *AssistantHandler) UpdateSub(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	old, err := h.st.GetCalendarSub(id)
	if err != nil || old == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "订阅不存在"})
		return
	}
	var req calendarSubReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	sub, err := req.normalize()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sub.ID = id
	if err := h.st.UpdateCalendarSub(&sub); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	if sub.URL != old.URL && sub.Enabled {
		if err := assistant.RefreshSub(h.st, sub, time.Now()); err != nil {
			log.Printf("日历订阅「%s」改址后抓取失败: %v", sub.Name, err)
		}
	}
	out, _ := h.st.GetCalendarSub(id)
	c.JSON(http.StatusOK, out)
}

// DeleteSub 删除订阅及事件。
func (h *AssistantHandler) DeleteSub(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	if err := h.st.DeleteCalendarSub(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// RefreshSub 手动刷新一个订阅（同步抓取，失败 502 并带原因）。
func (h *AssistantHandler) RefreshSub(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	sub, err := h.st.GetCalendarSub(id)
	if err != nil || sub == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "订阅不存在"})
		return
	}
	if err := assistant.RefreshSub(h.st, *sub, time.Now()); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	out, _ := h.st.GetCalendarSub(id)
	c.JSON(http.StatusOK, out)
}

// FeedInfo 导出订阅信息（无令牌则生成）：路径 + 令牌 + 来源开关。
func (h *AssistantHandler) FeedInfo(c *gin.Context) {
	tok, err := assistant.EnsureFeedToken(h.st)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成令牌失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": "/api/v1/calendar/feed.ics", "token": tok, "sources": assistant.LoadFeedSources(h.st)})
}

// RotateFeedToken 重置令牌（旧订阅地址立即失效）。
func (h *AssistantHandler) RotateFeedToken(c *gin.Context) {
	tok, err := assistant.RotateFeedToken(h.st)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "重置令牌失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": "/api/v1/calendar/feed.ics", "token": tok, "sources": assistant.LoadFeedSources(h.st)})
}

// PutFeedSources 保存导出来源开关。
func (h *AssistantHandler) PutFeedSources(c *gin.Context) {
	var fs assistant.FeedSources
	if err := c.ShouldBindJSON(&fs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := assistant.SaveFeedSources(h.st, fs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, fs)
}

// Feed 公开订阅端点（免登录，?token= 校验，常量时间比较）：无令牌/不匹配一律 404
// 不暴露端点存在；响应 text/calendar 且 no-store。
func (h *AssistantHandler) Feed(c *gin.Context) {
	want := assistant.FeedToken(h.st)
	got := c.Query("token")
	if want == "" || got == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		c.String(http.StatusNotFound, "not found")
		return
	}
	data, err := assistant.BuildFeed(h.st, assistant.LoadFeedSources(h.st), time.Now())
	if err != nil {
		log.Printf("生成日历订阅失败: %v", err)
		c.String(http.StatusInternalServerError, "calendar build failed")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", `inline; filename="picopanel.ics"`)
	c.Data(http.StatusOK, "text/calendar; charset=utf-8", data)
}

// ---- 倒数日 ----

// ListCountdowns 全部倒数日（带现算字段，按卡片顺序）。
func (h *AssistantHandler) ListCountdowns(c *gin.Context) {
	cs, err := h.st.ListCountdowns()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询倒数日失败"})
		return
	}
	if cs == nil {
		cs = []store.Countdown{}
	}
	assistant.Enrich(cs, time.Now())
	assistant.SortForCard(cs)
	c.JSON(http.StatusOK, gin.H{"items": cs})
}

type countdownReq struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Lunar bool   `json:"lunar"`
	Date  string `json:"date"`
	Color string `json:"color"`
}

func (h *AssistantHandler) bindCountdown(c *gin.Context) (*store.Countdown, bool) {
	var req countdownReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return nil, false
	}
	cd := &store.Countdown{Name: req.Name, Kind: req.Kind, Lunar: req.Lunar, Date: req.Date, Color: req.Color}
	if err := assistant.NormalizeCountdown(cd, time.Now().Location()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	return cd, true
}

// CreateCountdown 新增倒数日。
func (h *AssistantHandler) CreateCountdown(c *gin.Context) {
	cd, ok := h.bindCountdown(c)
	if !ok {
		return
	}
	id, err := h.st.CreateCountdown(cd)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": friendlyCountdownErr(err)})
		return
	}
	cd.ID = id
	assistant.Enrich([]store.Countdown{*cd}, time.Now())
	h.ListCountdowns(c)
}

// UpdateCountdown 编辑倒数日。
func (h *AssistantHandler) UpdateCountdown(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	cd, ok := h.bindCountdown(c)
	if !ok {
		return
	}
	cd.ID = id
	if err := h.st.UpdateCountdown(cd); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": friendlyCountdownErr(err)})
		return
	}
	h.ListCountdowns(c)
}

// DeleteCountdown 删除倒数日。
func (h *AssistantHandler) DeleteCountdown(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	if err := h.st.DeleteCountdown(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

func friendlyCountdownErr(err error) string {
	if strings.Contains(err.Error(), "UNIQUE") {
		return "已有同名倒数日"
	}
	return err.Error()
}
