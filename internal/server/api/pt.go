package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/pt"
	"lz-monitor/internal/server/store"
)

// PtHandler PT 站点信息接口（391，管理组）：站点目录、CRUD/排序/凭据测试、
// 手动刷新（单站/全部）。数据由 Manager 每天 06:00 抓取维护，页面读库直出；
// Cookie（M-Team 为 x-api-key）AES-GCM 加密存库，任何接口不回显。
type PtHandler struct {
	st *store.Store
	mg *pt.Manager
}

// NewPtHandler 构造。
func NewPtHandler(st *store.Store, mg *pt.Manager) *PtHandler {
	return &PtHandler{st: st, mg: mg}
}

// view 站点行 → 响应（凭据只出 has_cookie）。site_url 为点击站点名实际打开
// 的地址：配置的 open_url 优先，回落 base_url（镜像），再回落目录默认站点。
func (h *PtHandler) view(s *store.PtSite) gin.H {
	snap, _ := pt.DecodeSnapshot(s.DataJSON)
	var data any
	if snap != nil {
		data = snap
	}
	siteURL := normalizePtURL(s.OpenURL)
	if siteURL == "" {
		siteURL = normalizePtURL(s.BaseURL)
	}
	if siteURL == "" {
		if def := pt.SiteDefByKey(s.SiteKey); def != nil {
			siteURL = def.URL
		}
	}
	return gin.H{"id": s.ID, "site_key": s.SiteKey, "name": s.Name,
		"base_url": s.BaseURL, "open_url": s.OpenURL, "site_url": siteURL,
		"proxy_server_id": s.ProxyServerID, "icon": s.Icon, "signin_mode": s.SigninMode,
		"ua": s.UA, "enabled": s.Enabled,
		"has_cookie": s.HasCookie, "data": data,
		"fetched_at": s.FetchedAt, "last_ok_at": s.LastOKAt, "last_error": s.LastError}
}

func (h *PtHandler) catalog(c *gin.Context) {
	existing := map[string]bool{}
	if list, err := h.st.ListPtSiteMetas(); err == nil {
		for _, s := range list {
			existing[s.SiteKey] = true
		}
	}
	catalog := pt.Catalog()
	out := make([]gin.H, 0, len(catalog))
	for _, d := range catalog {
		out = append(out, gin.H{"key": d.Key, "name": d.Name, "url": d.URL,
			"kind": d.Kind, "auth": d.Auth, "added": existing[d.Key]})
	}
	c.JSON(http.StatusOK, gin.H{"catalog": out, "max": store.MaxPtSites})
}

func (h *PtHandler) sites(c *gin.Context) {
	list, err := h.st.ListPtSiteMetas()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询 PT 站点失败"})
		return
	}
	out := make([]gin.H, 0, len(list))
	for i := range list {
		out = append(out, h.view(&list[i]))
	}
	c.JSON(http.StatusOK, gin.H{"sites": out})
}

// ptSiteReq 站点增改请求（cookie 空=编辑沿用原值，不回显）。
type ptSiteReq struct {
	SiteKey       string `json:"site_key"`
	Name          string `json:"name"`
	BaseURL       string `json:"base_url"`
	OpenURL       string `json:"open_url"`        // 点击站点名打开的地址（空=站点首页）
	ProxyServerID int64  `json:"proxy_server_id"` // 抓取出口（0=直连，>0=经该 push agent 中转）
	SigninMode    string `json:"signin_mode"`     // 签到模式（dian115/hdhaven：空=仅查询/normal/lucky）
	Icon          string `json:"icon"`            // 自定义站点图标 URL（空=内置目录图标）
	Cookie        string `json:"cookie"`
	UA            string `json:"ua"`
	Enabled       *bool  `json:"enabled"`
}

// buildPtSite 请求 → 站点（目录白名单；base_url 空=目录默认站点）。
func buildPtSite(req *ptSiteReq) (*store.PtSite, error) {
	def := pt.SiteDefByKey(strings.TrimSpace(req.SiteKey))
	if def == nil {
		return nil, fmt.Errorf("不支持的站点（仅开放内置站点目录内的站点）")
	}
	base := normalizePtURL(req.BaseURL)
	// 镜像/备用域名允许改（btschool.club/.net 这类官方多域也跨 SLD），不做同域
	// 强校验——防填串站由先测后存兜底：错的地址抓不到用户信息，存不进去
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = def.Name
	}
	openURL := normalizePtURL(req.OpenURL)
	if openURL != "" && !strings.HasPrefix(openURL, "http") {
		return nil, fmt.Errorf("打开地址需以 http:// 或 https:// 开头")
	}
	mode := strings.TrimSpace(req.SigninMode)
	if mode != "" && mode != "normal" && mode != "lucky" {
		return nil, fmt.Errorf("签到模式仅支持 normal（普通）或 lucky（运气）")
	}
	icon := strings.TrimSpace(req.Icon)
	if icon != "" && !strings.HasPrefix(icon, "http://") && !strings.HasPrefix(icon, "https://") && !strings.HasPrefix(icon, "data:image/") {
		return nil, fmt.Errorf("图标地址需以 http(s):// 或 data:image/ 开头")
	}
	return &store.PtSite{SiteKey: def.Key, Name: name, BaseURL: strings.TrimRight(base, "/"),
		OpenURL: openURL, ProxyServerID: req.ProxyServerID, SigninMode: mode, Icon: icon, UA: strings.TrimSpace(req.UA)}, nil
}

func normalizePtURL(u string) string {
	u = strings.TrimSpace(u)
	if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	return strings.TrimRight(u, "/")
}

// sanitizeCredential 按站点类型清洗凭据：Cookie 类站点按 cookie 串语义清洗；
// apikey 类（mteam）的凭据是裸 API Key（无 '=' 的 UUID 形态），CleanCookie
// 的「丢没有 = 的段」规则会把它整段洗掉——只去空白（v1.0.4 中转建站曾把
// key 洗空落库，抓取恒报 Full authentication is required）。
func sanitizeCredential(siteKey, raw string) string {
	if d := pt.SiteDefByKey(strings.TrimSpace(siteKey)); d != nil && d.Auth == "apikey" {
		return strings.TrimSpace(raw)
	}
	return pt.CleanCookie(raw)
}

func (h *PtHandler) create(c *gin.Context) {
	var req ptSiteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if n, _ := h.st.CountPtSites(); n >= store.MaxPtSites {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多 %d 个站点", store.MaxPtSites)})
		return
	}
	if exists, _ := h.st.PtSiteKeyExists(strings.TrimSpace(req.SiteKey)); exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该站点已添加"})
		return
	}
	if strings.TrimSpace(req.Cookie) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写凭据（Cookie 或 API Key）"})
		return
	}
	s, err := buildPtSite(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.Cookie = sanitizeCredential(req.SiteKey, req.Cookie)
	if s.ProxyServerID > 0 {
		// 中转出口：面板直连多半不通，同步先测后存不适用——直接落库并入队
		// 首抓任务（agent 回传后数据出现；凭据有错会体现在站点 last_error）
		srv, err := h.st.GetServer(s.ProxyServerID)
		if err != nil || srv == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "出口服务器不存在"})
			return
		}
		if srv.CollectMode != "push" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "抓取出口仅支持推送模式（push）的 agent 服务器——拉取模式无任务下发通道"})
			return
		}
		id, err := h.st.CreatePtSite(s)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
			return
		}
		queued := true
		if _, err := h.mg.RefreshNow(id); err != nil && !errors.Is(err, pt.ErrQueued) {
			queued = false // 入队失败不回滚建站（下个轮次/手动刷新兜底）
		}
		fresh, err := h.st.GetPtSite(id)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"code": 0, "id": id, "queued": queued})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "id": id, "queued": queued, "site": h.view(fresh)})
		return
	}
	// 先测后存：抓不到用户信息不给存（省得存下一把死 Cookie）；测试带回的
	// 快照直接落库，省掉保存后再刷一轮（对 PT 站请求量减半）
	snap, err := pt.TestFetch(*s)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "连接测试失败: " + err.Error()})
		return
	}
	id, err := h.st.CreatePtSite(s)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	_ = h.st.SetPtSiteData(id, snap)
	_ = h.st.RecordPtSiteStatus(id, true, "")
	fresh, err := h.st.GetPtSite(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "id": id})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "id": id, "site": h.view(fresh)})
}

func (h *PtHandler) site(c *gin.Context) *store.PtSite {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return nil
	}
	s, err := h.st.GetPtSite(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "PT 站点不存在"})
		return nil
	}
	return s
}

func (h *PtHandler) update(c *gin.Context) {
	var req ptSiteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	old := h.site(c)
	if old == nil {
		return
	}
	s, err := buildPtSite(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.ID = old.ID
	// 编辑语义：空字段=沿用原值（否则清空 base_url 会回落到目录默认域，
	// 打到真站点去；名称同理会被改名回目录默认名）
	if strings.TrimSpace(req.Name) == "" {
		s.Name = old.Name
	}
	if strings.TrimSpace(req.BaseURL) == "" {
		s.BaseURL = old.BaseURL
	}
	// 打开地址不做空值沿用：传空即清空（回落站点首页），与「默认首页」语义一致
	if strings.TrimSpace(req.UA) == "" {
		s.UA = old.UA
	}
	s.Enabled = old.Enabled
	if req.Enabled != nil {
		s.Enabled = *req.Enabled
	}
	// 抓取出口：显式校验存在；换绑时作废旧出口的 pending 任务（任务绑定创建
	// 时的出口不迁移，防旧 agent 领走后新出口永远等不到）
	if s.ProxyServerID > 0 {
		srv, err := h.st.GetServer(s.ProxyServerID)
		if err != nil || srv == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "出口服务器不存在"})
			return
		}
		if srv.CollectMode != "push" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "抓取出口仅支持推送模式（push）的 agent 服务器——拉取模式无任务下发通道"})
			return
		}
	}
	if s.ProxyServerID != old.ProxyServerID {
		h.st.ExpirePtTasksBySite(s.ID)
	}
	var snap *store.PtSnapshot
	queued := false
	if cookie := strings.TrimSpace(req.Cookie); cookie != "" {
		// 新凭据两种出口都要落值（中转分支漏赋值曾把新 key 静默丢掉，store
		// 的空沿用只会兜住空串）
		s.Cookie = sanitizeCredential(req.SiteKey, cookie)
		if s.ProxyServerID == 0 {
			// 直连出口：新凭据先测再存（坏凭据 502 不落库），快照直接复用
			var err error
			snap, err = pt.TestFetch(*s)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "连接测试失败: " + err.Error()})
				return
			}
		}
	}
	if err := h.st.UpdatePtSite(s); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	if snap != nil {
		_ = h.st.SetPtSiteData(s.ID, snap)
		_ = h.st.RecordPtSiteStatus(s.ID, true, "")
	}
	// 中转出口：先落库再入队（任务快照凭据取自库行——入队早于落库会抓着旧
	// Cookie 打站）；凭据/地址/UA 任一变更才入队首抓验证
	if s.ProxyServerID > 0 && (strings.TrimSpace(req.Cookie) != "" || s.BaseURL != old.BaseURL || s.UA != old.UA) {
		if _, err := h.mg.RefreshNow(s.ID); err == nil || errors.Is(err, pt.ErrQueued) {
			queued = true
		}
	}
	fresh, err := h.st.GetPtSite(s.ID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "queued": queued})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "queued": queued, "site": h.view(fresh)})
}

func (h *PtHandler) remove(c *gin.Context) {
	s := h.site(c)
	if s == nil {
		return
	}
	if err := h.st.DeletePtSite(s.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	h.mg.Drop(s.ID)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

func (h *PtHandler) order(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.UpdatePtSitesOrder(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存排序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ptTestBody 测试请求（保存前试凭据，不落库）。
type ptTestBody struct {
	SiteKey string `json:"site_key"`
	BaseURL string `json:"base_url"`
	Cookie  string `json:"cookie"`
	UA      string `json:"ua"`
}

func (h *PtHandler) test(c *gin.Context) {
	var req ptTestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	s, err := buildPtSite(&ptSiteReq{SiteKey: req.SiteKey, BaseURL: req.BaseURL, UA: req.UA})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.Cookie = sanitizeCredential(req.SiteKey, req.Cookie)
	if s.Cookie == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写凭据（Cookie 或 API Key）"})
		return
	}
	if err := pt.Test(*s); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// refresh 手动刷新单站点（立即抓一次，返回整份站点视图供页面原位替换）。
// refresh 手动刷新单站点：直连立即抓一次返回整份视图；中转入队返回 queued
// （agent 下轮上报领取，页面稍候拉列表）。
func (h *PtHandler) refresh(c *gin.Context) {
	s := h.site(c)
	if s == nil {
		return
	}
	if _, err := h.mg.RefreshNow(s.ID); err != nil {
		if errors.Is(err, pt.ErrQueued) {
			c.JSON(http.StatusOK, gin.H{"code": 0, "queued": true})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	fresh, err := h.st.GetPtSite(s.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取刷新结果失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "site": h.view(fresh)})
}

// refreshAll 手动刷新全部启用站点（异步整轮，页面轮询列表看进度）。整轮
// 进行中时不再叠加新一轮（防连点造成 26 站高频突发），返回 already 供前端提示。
func (h *PtHandler) refreshAll(c *gin.Context) {
	started := h.mg.RefreshAllAsync()
	c.JSON(http.StatusOK, gin.H{"code": 0, "already": !started})
}

// signin 手动签到（416 dian115 / 432 hdhaven）：直连立即「签到+抓取」返回结果
// 文案与最新站点视图；中转出口入队（agent 按站点配置的签到模式执行），返回
// queued。已签站点由 Manager 短路（零请求直接回「今日已签到」）。
func (h *PtHandler) signin(c *gin.Context) {
	s := h.site(c)
	if s == nil {
		return
	}
	// 签到类站点按 kind 放行（445 修复：432 接入 hdhaven 时门禁漏改还锁
	// site_key==dian115，栖影签到按钮一直被 400 拒）
	kind := ""
	if def := pt.SiteDefByKey(s.SiteKey); def != nil {
		kind = def.Kind
	}
	if kind != "dian115" && kind != "hdhaven" && kind != "fengchao" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该站点不支持签到"})
		return
	}
	_, msg, err := h.mg.SigninNow(s.ID)
	if err != nil {
		if errors.Is(err, pt.ErrQueued) {
			c.JSON(http.StatusOK, gin.H{"code": 0, "queued": true})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if msg == "" {
		msg = "已完成"
	}
	fresh, err := h.st.GetPtSite(s.ID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "msg": msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": msg, "site": h.view(fresh)})
}
