package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/mail"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/weather"
)

// 上限：分组数、组内链接数与主页监控模块数（防误增泛滥；收藏夹同量级口径）。
const (
	maxPageGroups     = 20
	maxPageLinksInGrp = 50
	maxPageMetrics    = 16
	maxPageTitleLen   = 60
	maxPageNameLen    = 60
	maxPageURLLen     = 2048
	maxPageIconLen    = 2048
)

// PageHandler 个人主页（/page 独立页）：配置 + 链接分组 + 链接 + 统计概览。
type PageHandler struct {
	st           *store.Store
	mail         *mail.Manager // 统计概览的邮件未读数（内存轮询缓存）
	offlineAfter int64         // 服务器离线判定秒数（同 /servers 列表口径）
}

// Get 一次拉全主页渲染所需数据：配置、分组、链接、监控模块（页面数据量小，无需分端点）。
func (h *PageHandler) Get(c *gin.Context) {
	cfg, err := h.st.GetPageSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询主页配置失败"})
		return
	}
	groups, err := h.st.ListPageGroups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询链接分组失败"})
		return
	}
	links, err := h.st.ListPageLinks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询链接失败"})
		return
	}
	metrics, err := h.st.ListPageMetrics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询数据监控模块失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"title":            cfg.Title,
		"show_search":      cfg.ShowSearch,
		"link_mode":        cfg.LinkMode,
		"show_avatar":      cfg.ShowAvatar,
		"show_ha":          cfg.ShowHA,
		"show_stats":       cfg.ShowStats,
		"refresh_sec":      cfg.RefreshSec,
		"weather_enabled":  cfg.WeatherEnabled,
		"weather_mode":     cfg.WeatherMode,
		"weather_city":     cfg.WeatherCity,
		"weather_lat":      cfg.WeatherLat,
		"weather_lon":      cfg.WeatherLon,
		"weather_provider": cfg.WeatherProvider,
		"has_weather_key":  cfg.WeatherKey != "", // Key 不回显（全站凭据同口径），前端以 has 标记展示「已配置」
		"weather_api_host": cfg.WeatherHost,
		"show_countdown":   cfg.ShowCountdown,
		"groups":           groups,
		"links":            links,
		"metrics":          metrics,
	})
}

type pageSettingsReq struct {
	Title      string `json:"title"`
	ShowSearch *bool  `json:"show_search"`
	ShowAvatar *bool  `json:"show_avatar"`
	ShowHA     *bool  `json:"show_ha"`
	ShowStats  *bool  `json:"show_stats"`
	LinkMode   string `json:"link_mode"`
	RefreshSec *int   `json:"refresh_sec"`
	// 天气配置（整表单一起保存）
	WeatherEnabled  *bool    `json:"weather_enabled"`
	WeatherMode     string   `json:"weather_mode"`
	WeatherCity     string   `json:"weather_city"`
	WeatherLat      *float64 `json:"weather_lat"`
	WeatherLon      *float64 `json:"weather_lon"`
	WeatherProvider string   `json:"weather_provider"` // 空=Open-Meteo / gaode=高德
	WeatherKey      string   `json:"weather_key"`      // 服务商 API Key
	WeatherHost     string   `json:"weather_api_host"` // 和风专属 API Host（v1 起必配）
	ShowCountdown   *bool    `json:"show_countdown"` // 倒数日卡显隐（可选：nil=沿用现值，老表单不带该字段不报错）
}

// SaveSettings 保存标题、搜索框显隐、链接分组展示模式、头像显隐、监控自动刷新间隔与天气配置。
func (h *PageHandler) SaveSettings(c *gin.Context) {
	var req pageSettingsReq
	if err := c.ShouldBindJSON(&req); err != nil || req.ShowSearch == nil || req.ShowAvatar == nil || req.ShowHA == nil || req.ShowStats == nil || req.RefreshSec == nil || req.WeatherEnabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if req.LinkMode != "horizontal" {
		req.LinkMode = "vertical" // 缺省/非法值兜底为竖向
	}
	refreshSec := *req.RefreshSec
	if refreshSec != 0 && (refreshSec < 5 || refreshSec > 86400) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "刷新间隔须为 0（不刷新）或 5~86400 秒"})
		return
	}
	title := strings.TrimSpace(req.Title)
	if len(title) > maxPageTitleLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("标题最长 %d 字", maxPageTitleLen)})
		return
	}
	// 天气：模式合法域 + 手动城市必须有经纬度（自动定位模式忽略城市字段值，但原样保存以便切换）
	wxMode := req.WeatherMode
	if wxMode != "manual" {
		wxMode = "auto"
	}
	wxLat, wxLon := 0.0, 0.0
	if req.WeatherLat != nil {
		wxLat = *req.WeatherLat
	}
	if req.WeatherLon != nil {
		wxLon = *req.WeatherLon
	}
	if wxLat < -90 || wxLat > 90 || wxLon < -180 || wxLon > 180 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "经纬度超出合法范围"})
		return
	}
	wxCity := strings.TrimSpace(req.WeatherCity)
	if len(wxCity) > 60 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "城市名最长 60 字"})
		return
	}
	wxProvider := req.WeatherProvider
	if wxProvider != "gaode" && wxProvider != "qweather" {
		wxProvider = "" // 空=Open-Meteo
	}
	wxKey := strings.TrimSpace(req.WeatherKey)
	if len(wxKey) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "天气 Key 最长 128 字"})
		return
	}
	// 和风专属 API Host：剥 scheme/路径只留 host（允许整段粘贴），空=未配置（运行时回落 Open-Meteo）
	wxHost := req.WeatherHost
	if i := strings.Index(wxHost, "://"); i >= 0 {
		wxHost = wxHost[i+3:]
	}
	if i := strings.IndexByte(wxHost, '/'); i >= 0 {
		wxHost = wxHost[:i]
	}
	wxHost = strings.TrimSpace(wxHost)
	if len(wxHost) > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "API Host 最长 120 字"})
		return
	}
	if *req.WeatherEnabled && wxMode == "manual" && wxLat == 0 && wxLon == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "手动城市模式请先搜索并选择城市"})
		return
	}
	p, err := h.st.GetPageSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询主页配置失败"})
		return
	}
	p.Title, p.ShowSearch, p.ShowAvatar, p.ShowHA, p.ShowStats, p.LinkMode, p.RefreshSec = title, *req.ShowSearch, *req.ShowAvatar, *req.ShowHA, *req.ShowStats, req.LinkMode, refreshSec
	p.WeatherEnabled, p.WeatherMode, p.WeatherCity, p.WeatherLat, p.WeatherLon = *req.WeatherEnabled, wxMode, wxCity, wxLat, wxLon
	p.WeatherProvider, p.WeatherHost = wxProvider, wxHost
	if wxKey != "" { // Key 留空沿用：不回显给前端的凭据统一「空=不改」口径（同 mihomo secret）
		p.WeatherKey = wxKey
	}
	if req.ShowCountdown != nil {
		p.ShowCountdown = *req.ShowCountdown
	}
	if err := h.st.SavePageSettings(p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	// 和风系统账户行随天气配置联动（3）：失败不连累设置保存，落日志排查
	if err := h.st.SyncQWeatherSystemAccount(p); err != nil {
		log.Printf("同步和风用量账户失败: %v", err)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Weather 首页天气：auto=按访客 IP 定位 / manual=配置的城市（weather 包内缓存）。
func (h *PageHandler) Weather(c *gin.Context) {
	ps, err := h.st.GetPageSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询主页配置失败"})
		return
	}
	if !ps.WeatherEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "天气未开启"})
		return
	}
	r, err := weather.Get(ps, clientIP(c))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

// WeatherLatest GET /page/weather/latest：最新一条采样入库的天气（56）。HA 胶囊
// 专用——只读 wx_samples，不触发任何上游取数；weather=采样时存的完整 Resp 原样
// 透传，ts=采样落库时刻（秒）；无可用采样（天气刚开/升级前旧采样无 JSON）回
// weather:null，前端胶囊不显示。自动定位模式与采样同键（SampleLocKey）。
func (h *PageHandler) WeatherLatest(c *gin.Context) {
	ps, err := h.st.GetPageSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询主页配置失败"})
		return
	}
	if !ps.WeatherEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "天气未开启"})
		return
	}
	loc, err := weather.SampleLocKey(ps)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	sm, err := h.st.LatestWXSample(loc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询天气采样失败"})
		return
	}
	if sm == nil {
		c.JSON(http.StatusOK, gin.H{"ts": 0, "weather": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ts": sm.TS, "weather": json.RawMessage(sm.Data)})
}

// WeatherHistory GET /page/weather/history?hours=168：当前配置位置的历史曲线
//（522）。位置键与采样同源（weather.SampleLocKey：manual 优先，auto 回落 manual
// 配置，无法定位报错）；服务端选桶宽（区间/500，下限 1s）：≤7 天落在采样明细、
// 更早落在 5 分钟归档桶，跨界自动拼接（留存口径同 HA/服务器监控）。
func (h *PageHandler) WeatherHistory(c *gin.Context) {
	ps, err := h.st.GetPageSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询主页配置失败"})
		return
	}
	loc, err := weather.SampleLocKey(ps)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	hours, _ := strconv.ParseFloat(c.DefaultQuery("hours", "168"), 64)
	if !(hours > 0) {
		hours = 168
	}
	if hours > 90*24 {
		hours = 90 * 24
	}
	secs := int64(hours*3600 + 0.5)
	if secs < 3600 {
		secs = 3600
	}
	now := time.Now().Unix()
	step := secs / 500
	if step < 1 {
		step = 1
	}
	points, err := h.st.WXSeries(loc, now-secs, now, step)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询天气历史失败"})
		return
	}
	if points == nil {
		points = []store.WXPoint{}
	}
	c.JSON(http.StatusOK, gin.H{"points": points})
}

// SearchCity 手动城市的候选搜索（按配置的服务商：高德 geocode / Open-Meteo geocoding，中文）。
func (h *PageHandler) SearchCity(c *gin.Context) {
	name := strings.TrimSpace(c.Query("name"))
	if name == "" || len(name) > 60 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入城市名"})
		return
	}
	ps, err := h.st.GetPageSettings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询主页配置失败"})
		return
	}
	cities, err := weather.SearchCity(name, ps)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"cities": cities})
}

// Stats 主页统计概览（459）：消息未读/服务器状态/文档分组/邮件未读/待办/证书/PT 签到
// 一次拉齐。小 COUNT 聚合，前端随快照同节拍轮询。
func (h *PageHandler) Stats(c *gin.Context) {
	s, err := h.st.HomePageStats(h.offlineAfter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询统计失败"})
		return
	}
	// 邮件未读：mail.Manager 内存缓存求和；-1 = 该账号上次查询失败，不计入
	mailUnread := 0
	if h.mail != nil {
		for _, n := range h.mail.UnreadAll() {
			if n > 0 {
				mailUnread += n
			}
		}
	}
	s.MailUnread = mailUnread
	s.GeneratedAt = time.Now().Unix()
	c.JSON(http.StatusOK, s)
}

type pageGroupReq struct {
	Name string `json:"name"`
}

func (h *PageHandler) checkGroupName(c *gin.Context, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxPageNameLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分组名必填且不超过 60 字"})
		return false
	}
	return true
}

// CreateGroup 新增分组（数量上限内）。
func (h *PageHandler) CreateGroup(c *gin.Context) {
	var req pageGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if !h.checkGroupName(c, req.Name) {
		return
	}
	if n, err := h.st.CountPageGroups(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分组数失败"})
		return
	} else if n >= maxPageGroups {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多 %d 个分组", maxPageGroups)})
		return
	}
	g, err := h.st.CreatePageGroup(strings.TrimSpace(req.Name))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": g})
}

// RenameGroup 分组改名。
func (h *PageHandler) RenameGroup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分组 ID"})
		return
	}
	var req pageGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if !h.checkGroupName(c, req.Name) {
		return
	}
	if err := h.st.RenamePageGroup(id, strings.TrimSpace(req.Name)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteGroup 删除分组（连带组内链接）。
func (h *PageHandler) DeleteGroup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分组 ID"})
		return
	}
	if err := h.st.DeletePageGroup(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// OrderGroups 保存分组拖拽顺序。
func (h *PageHandler) OrderGroups(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if len(req.IDs) > maxPageGroups {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量超出上限"})
		return
	}
	if err := h.st.ReorderPageGroups(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存顺序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

type pageLinkReq struct {
	GroupID int64  `json:"group_id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Icon    string `json:"icon"`
}

func (h *PageHandler) checkLink(c *gin.Context, req pageLinkReq) bool {
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	req.Icon = strings.TrimSpace(req.Icon)
	if req.Name == "" || len(req.Name) > maxPageNameLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "显示文字必填且不超过 60 字"})
		return false
	}
	if req.URL == "" || len(req.URL) > maxPageURLLen || !(strings.HasPrefix(req.URL, "http://") || strings.HasPrefix(req.URL, "https://")) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "链接地址须为 http(s):// 开头"})
		return false
	}
	if len(req.Icon) > maxPageIconLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "图标地址过长"})
		return false
	}
	return true
}

// CreateLink 新增链接到分组。
func (h *PageHandler) CreateLink(c *gin.Context) {
	var req pageLinkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if !h.checkLink(c, req) {
		return
	}
	if n, err := h.st.CountPageLinksInGroup(req.GroupID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询链接数失败"})
		return
	} else if n >= maxPageLinksInGrp {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("每组最多 %d 个链接", maxPageLinksInGrp)})
		return
	}
	l, err := h.st.CreatePageLink(req.GroupID, strings.TrimSpace(req.Name), strings.TrimSpace(req.URL), strings.TrimSpace(req.Icon))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"link": l})
}

// UpdateLink 编辑链接（名称/地址/图标）。
func (h *PageHandler) UpdateLink(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的链接 ID"})
		return
	}
	var req pageLinkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if !h.checkLink(c, req) {
		return
	}
	if err := h.st.UpdatePageLink(id, strings.TrimSpace(req.Name), strings.TrimSpace(req.URL), strings.TrimSpace(req.Icon)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteLink 删除一个链接。
func (h *PageHandler) DeleteLink(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的链接 ID"})
		return
	}
	if err := h.st.DeletePageLink(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// OrderLinks 保存组内/跨组拖拽顺序；group_id > 0 = 这批链接同时归入该组（跨组拖拽）。
func (h *PageHandler) OrderLinks(c *gin.Context) {
	var req struct {
		IDs     []int64 `json:"ids" binding:"required"`
		GroupID int64   `json:"group_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if len(req.IDs) > maxPageLinksInGrp {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量超出上限"})
		return
	}
	if req.GroupID > 0 {
		grps, err := h.st.ListPageGroups()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存顺序失败"})
			return
		}
		found := false
		for _, g := range grps {
			if g.ID == req.GroupID {
				found = true
				break
			}
		}
		if !found {
			c.JSON(http.StatusBadRequest, gin.H{"error": "目标分组不存在"})
			return
		}
		// 每组上限：目标组现有数（不含这批要移入的）+ 这批 ≤ 上限
		links, err := h.st.ListPageLinks()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存顺序失败"})
			return
		}
		idSet := map[int64]bool{}
		for _, id := range req.IDs {
			idSet[id] = true
		}
		n := 0
		for _, l := range links {
			if l.GroupID == req.GroupID && !idSet[l.ID] {
				n++
			}
		}
		if n+len(req.IDs) > maxPageLinksInGrp {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("每组最多 %d 个链接", maxPageLinksInGrp)})
			return
		}
	}
	if err := h.st.ReorderPageLinks(req.IDs, req.GroupID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存顺序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 主页数据监控模块（与总览「数据监控」同构、独立配置；数值前端从 /servers
// 快照或用量账户快照（486，ai_id>0）计算）----

type pageMetricView struct {
	ID         int64  `json:"id"`
	ServerID   int64  `json:"server_id"`
	AIID       int64  `json:"ai_id"`
	AIName     string `json:"ai_name"`
	ServerName string `json:"server_name"`
	Metric     string `json:"metric"`
	Param      string `json:"param"`
	Label      string `json:"label"`
	Link       string `json:"link"`
	Icon       string `json:"icon"`
	Hidden bool `json:"hidden"`
}

func (h *PageHandler) metricView(m store.PageMetric, srvName, aiName string) pageMetricView {
	return pageMetricView{
		ID: m.ID, ServerID: m.ServerID, AIID: m.AIID, AIName: aiName, ServerName: srvName,
		Metric: m.Metric, Param: m.Param, Label: m.Label, Link: m.Link, Icon: m.Icon,
		Hidden: m.Hidden,
	}
}

// ListMetrics 返回全部主页监控模块（附服务器名/用量账户名）。
func (h *PageHandler) ListMetrics(c *gin.Context) {
	list, err := h.st.ListPageMetrics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询数据监控模块失败"})
		return
	}
	srvNames := map[int64]string{}
	aiNames := map[int64]string{}
	out := make([]pageMetricView, 0, len(list))
	for _, m := range list {
		var srvName, aiName string
		if m.AIID > 0 {
			name, ok := aiNames[m.AIID]
			if !ok {
				if acc, err := h.st.GetAiAccount(m.AIID); err == nil && acc != nil {
					name = acc.Name
				}
				aiNames[m.AIID] = name
			}
			aiName = name
		} else {
			name, ok := srvNames[m.ServerID]
			if !ok {
				if srv, err := h.st.GetServer(m.ServerID); err == nil && srv != nil {
					name = srv.Name
				}
				srvNames[m.ServerID] = name
			}
			srvName = name
		}
		out = append(out, h.metricView(m, srvName, aiName))
	}
	c.JSON(http.StatusOK, gin.H{"metrics": out})
}

type pageMetricReq struct {
	ServerID int64  `json:"server_id"`
	AIID     int64  `json:"ai_id"`
	Metric   string `json:"metric"`
	Param    string `json:"param"`
	Label    string `json:"label"`
	Link     string `json:"link"`
	Icon     string `json:"icon"`
}

// checkMetricLink 跳转链接校验：空=不跳转；填了须 http(s) 开头且不超长。
func checkMetricLink(link string) bool {
	return link == "" || (len(link) <= maxPageURLLen && (strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://")))
}

// checkMetricIcon 图标地址校验：空或长度上限内（任意串均可，前端保存时已做 favicon 解析）。
func checkMetricIcon(icon string) bool {
	return len(icon) <= maxPageIconLen
}

// CreateMetric 新增主页监控模块：校验同总览数据监控（指标合法/服务器存在/标签必填/上限）。
func (h *PageHandler) CreateMetric(c *gin.Context) {
	var req pageMetricReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if _, ok := tileMetrics[req.Metric]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的指标"})
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写展示标签"})
		return
	}
	req.Link = strings.TrimSpace(req.Link)
	if !checkMetricLink(req.Link) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "跳转链接须为 http(s):// 开头"})
		return
	}
	req.Icon = strings.TrimSpace(req.Icon)
	if !checkMetricIcon(req.Icon) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("图标地址最长 %d 字", maxPageIconLen)})
		return
	}
	srvID, aiID, msg := checkTileSource(h.st, req.Metric, req.ServerID, req.AIID)
	if msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if n, err := h.st.CountPageMetrics(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模块数失败"})
		return
	} else if n >= maxPageMetrics {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多配置 %d 个模块", maxPageMetrics)})
		return
	}
	m, err := h.st.CreatePageMetric(srvID, aiID, req.Metric, strings.TrimSpace(req.Param), req.Label, req.Link, req.Icon)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"metric": h.metricView(*m, "", "")})
}

// UpdateMetric 编辑主页监控模块。
func (h *PageHandler) UpdateMetric(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的模块 ID"})
		return
	}
	var req pageMetricReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if _, ok := tileMetrics[req.Metric]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的指标"})
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写展示标签"})
		return
	}
	req.Link = strings.TrimSpace(req.Link)
	if !checkMetricLink(req.Link) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "跳转链接须为 http(s):// 开头"})
		return
	}
	req.Icon = strings.TrimSpace(req.Icon)
	if !checkMetricIcon(req.Icon) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("图标地址最长 %d 字", maxPageIconLen)})
		return
	}
	serverID, aiID, msg := checkTileSource(h.st, req.Metric, req.ServerID, req.AIID)
	if msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	if err := h.st.UpdatePageMetric(id, serverID, aiID, req.Metric, strings.TrimSpace(req.Param), req.Label, req.Link, req.Icon); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteMetric 删除一个主页监控模块。
func (h *PageHandler) DeleteMetric(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的模块 ID"})
		return
	}
	if err := h.st.DeletePageMetric(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// OrderMetrics 保存主页监控模块拖拽顺序。
func (h *PageHandler) OrderMetrics(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if len(req.IDs) > maxPageMetrics {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量超出上限"})
		return
	}
	if err := h.st.ReorderPageMetrics(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存顺序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// setHiddenReq 显隐切换请求体。
type setHiddenReq struct {
	Hidden *bool `json:"hidden" binding:"required"`
}

// SetMetricHidden 单独切换一个主页监控格子的显隐（不连累编辑表单）。
func (h *PageHandler) SetMetricHidden(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的模块 ID"})
		return
	}
	var req setHiddenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.SetPageMetricHidden(id, *req.Hidden); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// SetLinkHidden 单独切换一条链接的主页显隐。
func (h *PageHandler) SetLinkHidden(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的链接 ID"})
		return
	}
	var req setHiddenReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.SetPageLinkHidden(id, *req.Hidden); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
