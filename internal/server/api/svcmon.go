package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/svcmon"
)

// SvcMonHandler 服务监控接口（366 两级重构，登录组）：服务器 CRUD、端口 CRUD、
// 未入库配置测试、单端口同步检测、整台/全部异步检测。状态由 svcmon.Manager
// 轮询维护，列表读库直出（服务器 + 端口两级嵌套）。
type SvcMonHandler struct {
	st *store.Store
	mg *svcmon.Manager
}

// NewSvcMonHandler 构造。
func NewSvcMonHandler(st *store.Store, mg *svcmon.Manager) *SvcMonHandler {
	return &SvcMonHandler{st: st, mg: mg}
}

// svcServerReq 服务器增改请求。
type svcServerReq struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Interval int    `json:"interval_sec"`
	Enabled  *bool  `json:"enabled"`
}

// svcPortReq 端口增改请求（业务名可空，前端以端口数兜底展示）。Scheme 空=tcp；
// http/https 时 Port 传 0 自动补默认（80/443，495）。Domain 仅 http/https 有意义，
// 空=用服务器 host。
type svcPortReq struct {
	Name   string `json:"name"`
	Port   int    `json:"port"`
	Scheme string `json:"scheme"`
	Domain string `json:"domain"`
}

// normalizeSvcPort 端口增改共用的协议/端口/域名校验与规整：
//   - scheme 白名单 tcp/http/https（空按 tcp）；
//   - http/https 端口缺省补 80/443，tcp 端口必填；
//   - domain 剥掉误带的 scheme:// 前缀与路径（留 host），http(s) ≤255 字。
func normalizeSvcPort(req *svcPortReq) (string, int, string, error) {
	scheme := strings.ToLower(strings.TrimSpace(req.Scheme))
	if scheme == "" {
		scheme = store.SvcPortSchemeTCP
	}
	switch scheme {
	case store.SvcPortSchemeTCP:
		if req.Port < 1 || req.Port > 65535 {
			return "", 0, "", errBadPort
		}
	case "http", "https":
		port := req.Port
		if port == 0 {
			if scheme == "http" {
				port = 80
			} else {
				port = 443
			}
		}
		if port < 1 || port > 65535 {
			return "", 0, "", errBadPort
		}
		req.Port = port
		domain := strings.TrimSpace(req.Domain)
		if i := strings.Index(domain, "://"); i >= 0 {
			domain = domain[i+3:]
		}
		if i := strings.IndexAny(domain, "/?#"); i >= 0 {
			domain = domain[:i]
		}
		domain = strings.TrimSuffix(domain, ":") // 只剩「host:」形态时去掉冒号
		if len(domain) > 255 {
			return "", 0, "", errors.New("域名最长 255 字")
		}
		req.Domain = domain
		if domain == "" {
			req.Domain = "" // 剥完为空按「用服务器 host」处理
		}
	default:
		return "", 0, "", errors.New("协议仅支持 tcp/http/https")
	}
	return scheme, req.Port, req.Domain, nil
}

func (h *SvcMonHandler) portView(p *store.SvcPort) gin.H {
	return gin.H{"id": p.ID, "server_id": p.ServerID, "name": p.Name, "port": p.Port,
		"scheme": p.Scheme, "domain": p.Domain,
		"up": p.Up, "latency_ms": p.LatencyMs, "checked_at": p.CheckedAt,
		"since": p.Since, "last_ok_at": p.LastOKAt, "last_error": p.LastError}
}

func (h *SvcMonHandler) portRowView(r *store.SvcPortRow) gin.H {
	v := h.portView(&r.SvcPort)
	v["host"] = r.Host
	return v
}

func (h *SvcMonHandler) list(c *gin.Context) {
	servers, err := h.st.ListSvcServers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询监控服务器失败"})
		return
	}
	rows, err := h.st.ListSvcPortRows()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询监控端口失败"})
		return
	}
	ports := map[int64][]gin.H{}
	for i := range rows {
		ports[rows[i].ServerID] = append(ports[rows[i].ServerID], h.portRowView(&rows[i]))
	}
	out := make([]gin.H, 0, len(servers))
	for i := range servers {
		s := &servers[i]
		out = append(out, gin.H{"id": s.ID, "name": s.Name, "host": s.Host,
			"interval_sec": s.Interval, "enabled": s.Enabled, "ports": ports[s.ID]})
	}
	c.JSON(http.StatusOK, gin.H{"servers": out,
		"max_servers": store.MaxSvcServers, "max_ports": store.MaxSvcPortsPerServer})
}

// buildSvcServer 请求 → 服务器（host 去空白校验、间隔越界回落默认）。
func buildSvcServer(req *svcServerReq) (*store.SvcServer, error) {
	req.Host = strings.TrimSpace(req.Host)
	if len(req.Host) == 0 || len(req.Host) > 255 || strings.ContainsAny(req.Host, " \t\r\n") {
		return nil, errBadHost
	}
	interval := req.Interval
	if interval == 0 {
		interval = store.SvcIntervalDefault
	}
	if interval < store.SvcIntervalMin {
		interval = store.SvcIntervalMin
	}
	if interval > store.SvcIntervalMax {
		interval = store.SvcIntervalMax
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return &store.SvcServer{Name: strings.TrimSpace(req.Name), Host: req.Host,
		Interval: interval, Enabled: enabled}, nil
}

var (
	errBadHost = &svcErr{"地址需为 1~255 字符的 ip / 域名（不含空格）"}
	errBadPort = &svcErr{"端口应在 1~65535 之间"}
)

type svcErr struct{ msg string }

func (e *svcErr) Error() string { return e.msg }

func (h *SvcMonHandler) serverID(c *gin.Context) *int64 {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return nil
	}
	return &id
}

func (h *SvcMonHandler) portID(c *gin.Context) *int64 {
	id, err := strconv.ParseInt(c.Param("pid"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return nil
	}
	return &id
}

func (h *SvcMonHandler) createServer(c *gin.Context) {
	var req svcServerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	sv, err := buildSvcServer(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	list, _ := h.st.ListSvcServers()
	if len(list) >= store.MaxSvcServers {
		c.JSON(http.StatusBadRequest, gin.H{"error": "最多 16 台监控服务器"})
		return
	}
	id, err := h.st.CreateSvcServer(sv)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	fresh, _ := h.st.GetSvcServer(id)
	c.JSON(http.StatusOK, gin.H{"code": 0, "id": id, "server": h.serverView(fresh, nil)})
}

func (h *SvcMonHandler) serverView(sv *store.SvcServer, ports []gin.H) gin.H {
	if ports == nil {
		ports = []gin.H{}
	}
	return gin.H{"id": sv.ID, "name": sv.Name, "host": sv.Host,
		"interval_sec": sv.Interval, "enabled": sv.Enabled, "ports": ports}
}

func (h *SvcMonHandler) updateServer(c *gin.Context) {
	sid := h.serverID(c)
	if sid == nil {
		return
	}
	old, err := h.st.GetSvcServer(*sid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控服务器不存在"})
		return
	}
	var req svcServerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	nsv, err := buildSvcServer(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	nsv.ID = old.ID
	if err := h.st.UpdateSvcServer(nsv); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	// 换址（store 层已重置端口态）或重新启用都立即重查一轮，不等轮询
	if old.Host != nsv.Host || (req.Enabled != nil && *req.Enabled && !old.Enabled) {
		h.mg.KickServer(old.ID)
	}
	fresh, _ := h.st.GetSvcServer(old.ID)
	c.JSON(http.StatusOK, gin.H{"code": 0, "server": h.serverView(fresh, nil)})
}

func (h *SvcMonHandler) removeServer(c *gin.Context) {
	sid := h.serverID(c)
	if sid == nil {
		return
	}
	if _, err := h.st.GetSvcServer(*sid); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控服务器不存在"})
		return
	}
	rows, _ := h.st.ListSvcPortRows()
	var portIDs []int64
	for _, r := range rows {
		if r.ServerID == *sid {
			portIDs = append(portIDs, r.ID)
		}
	}
	if err := h.st.DeleteSvcServer(*sid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	for _, id := range portIDs {
		h.mg.Drop(id)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// createPort 服务器下新增监控端口：建行后立即同步拨测一次，首态即刻入库。
func (h *SvcMonHandler) createPort(c *gin.Context) {
	sid := h.serverID(c)
	if sid == nil {
		return
	}
	if _, err := h.st.GetSvcServer(*sid); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控服务器不存在"})
		return
	}
	var req svcPortReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	scheme, port, domain, err := normalizeSvcPort(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if n, _ := h.st.CountSvcPorts(*sid); n >= store.MaxSvcPortsPerServer {
		c.JSON(http.StatusBadRequest, gin.H{"error": "每台服务器最多 16 个监控端口"})
		return
	}
	p := &store.SvcPort{ServerID: *sid, Name: strings.TrimSpace(req.Name), Port: port, Scheme: scheme, Domain: domain}
	id, err := h.st.CreateSvcPort(p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	fresh, _ := h.mg.CheckNow(id) // 首次状态即刻入库，不用等轮询
	c.JSON(http.StatusOK, gin.H{"code": 0, "id": id, "port": h.mustPortView(fresh, id)})
}

// mustPortView CheckNow 失败（理论上只可能是读库错）时回落构造未检测态视图。
func (h *SvcMonHandler) mustPortView(r *store.SvcPortRow, id int64) gin.H {
	if r == nil {
		return gin.H{"id": id, "up": -1}
	}
	return h.portRowView(r)
}

func (h *SvcMonHandler) updatePort(c *gin.Context) {
	pid := h.portID(c)
	if pid == nil {
		return
	}
	old, err := h.st.GetSvcPortRow(*pid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控端口不存在"})
		return
	}
	var req svcPortReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	scheme, port, domain, err := normalizeSvcPort(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	np := &store.SvcPort{ID: old.ID, ServerID: old.ServerID, Name: strings.TrimSpace(req.Name), Port: port, Scheme: scheme, Domain: domain}
	if err := h.st.UpdateSvcPort(np); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	// 换了端口/协议/域名（store 层已重置该行状态）立即重查一次，状态不等轮询
	fresh, _ := h.mg.CheckNow(old.ID)
	c.JSON(http.StatusOK, gin.H{"code": 0, "port": h.mustPortView(fresh, old.ID)})
}

func (h *SvcMonHandler) removePort(c *gin.Context) {
	pid := h.portID(c)
	if pid == nil {
		return
	}
	if _, err := h.st.GetSvcPortRow(*pid); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控端口不存在"})
		return
	}
	if err := h.st.DeleteSvcPort(*pid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	h.mg.Drop(*pid)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// test 未入库配置的连通测试（新增端口弹窗「先测后存」按钮用，不落库）。
func (h *SvcMonHandler) test(c *gin.Context) {
	var req struct {
		Host   string `json:"host"`
		Port   int    `json:"port"`
		Scheme string `json:"scheme"`
		Domain string `json:"domain"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if strings.TrimSpace(req.Host) == "" || req.Port < 1 || req.Port > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "地址与端口（1~65535）必填"})
		return
	}
	probeReq := svcPortReq{Port: req.Port, Scheme: req.Scheme, Domain: req.Domain}
	scheme, port, domain, err := normalizeSvcPort(&probeReq)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if domain == "" {
		domain = strings.TrimSpace(req.Host) // 域名空回落目标地址
	}
	var up bool
	var ms int64
	var errMsg string
	if scheme == "http" || scheme == "https" {
		up, ms, errMsg = svcmon.CheckHTTP(scheme, domain, strconv.Itoa(port))
	} else {
		up, ms, errMsg = svcmon.CheckPort(strings.TrimSpace(req.Host), port)
	}
	res := gin.H{"ok": up, "ms": ms}
	if !up {
		res["error"] = errMsg
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "result": res})
}

// checkPort 手动检测单端口（同步；连通与否都算成功应答，状态翻红看 last_error）。
func (h *SvcMonHandler) checkPort(c *gin.Context) {
	pid := h.portID(c)
	if pid == nil {
		return
	}
	if _, err := h.st.GetSvcPortRow(*pid); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控端口不存在"})
		return
	}
	fresh, err := h.mg.CheckNow(*pid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "检测失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "port": h.portRowView(fresh)})
}

// checkServer 整台服务器的启用端口立即重查（后台异步，前端靠轮询取结果）。
func (h *SvcMonHandler) checkServer(c *gin.Context) {
	sid := h.serverID(c)
	if sid == nil {
		return
	}
	if _, err := h.st.GetSvcServer(*sid); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控服务器不存在"})
		return
	}
	h.mg.KickServer(*sid)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// refreshAll 全部启用端口立即重查（后台异步，前端靠轮询取结果）。
func (h *SvcMonHandler) refreshAll(c *gin.Context) {
	h.mg.KickAll()
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// events 最近状态翻转事件（500，新→旧）：limit 夹紧 1~200，缺省 50。
func (h *SvcMonHandler) events(c *gin.Context) {
	limit := 50
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n >= 1 && n <= 200 {
		limit = n
	}
	rows, err := h.st.ListSvcEvents(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询翻转事件失败"})
		return
	}
	if rows == nil {
		rows = []store.SvcEventRow{}
	}
	c.JSON(http.StatusOK, gin.H{"events": rows})
}

// availability 逐端口窗口可用率（500）：days 缺省 7，夹紧 1~90；无事件且
// 初态按通的端口不出行，前端显示 '-'。
func (h *SvcMonHandler) availability(c *gin.Context) {
	days := 7
	if n, err := strconv.Atoi(c.Query("days")); err == nil && n >= 1 {
		days = n
	}
	if days > 90 {
		days = 90
	}
	list, err := h.st.SvcAvailability(days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "计算可用率失败"})
		return
	}
	if list == nil {
		list = []store.SvcPortAvail{}
	}
	c.JSON(http.StatusOK, gin.H{"days": days, "availability": list})
}
