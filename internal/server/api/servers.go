package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/certcheck"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/term"
)

// ServerHandler 处理服务器管理端点。
type ServerHandler struct {
	st           *store.Store
	offlineAfter int64         // 离线判定秒数
	term         *term.Manager // SSH 配置变更/删除后丢弃池化 SFTP 连接（测试可 nil）
}

// serverView 是 /servers 列表与详情的响应条目（含最新快照与在线状态）。
// 敏感字段打码：token / ssh_password 不回显明文，改以 *_set 布尔告知是否已配置
// （编辑时留空=不变，清除走 clear_* 参数）。
// 注意：字段为 store.Server 的手工同步副本——store.Server 新增非敏感字段时
// 需要同步加进来（TestServersMasked 断言响应不含明文键防回归）。
type serverView struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	AgentAddr    string `json:"agent_addr"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Kernel       string `json:"kernel"`
	Arch         string `json:"arch"`
	CPUModel     string `json:"cpu_model"`
	CPUCores     int    `json:"cpu_cores"`
	Enabled      bool   `json:"enabled"`
	NetIfaces    string `json:"net_ifaces"`
	LastReportAt int64  `json:"last_report_at"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	CollectMode  string `json:"collect_mode"`
	SSHUser      string `json:"ssh_user"`
	SSHKeyPath   string `json:"ssh_key_path"`
	SSHAddr      string `json:"ssh_addr"`
	GroupID      int64  `json:"group_id"`
	PurchasedOn  string `json:"purchased_on"`
	ExpiresOn    string `json:"expires_on"`
	Remark       string `json:"remark"`
	Icon         string `json:"icon"`
	AgentVersion string `json:"agent_version"` // 396：agent 自报版本串（2026-10-06 补视图——列上线起漏带，版本列恒显示「旧版」）

	TokenSet       bool `json:"token_set"`        // 是否已配置 agent/push 密钥（明文不回显）
	SSHPasswordSet bool `json:"ssh_password_set"` // 是否已配置 SSH 密码（明文不回显；false=走私钥）
	HasHostKey     bool `json:"has_host_key"`     // ssh 模式：是否已记录主机指纹（TOFU）

	Online        bool        `json:"online"`
	Firing        int         `json:"firing"`
	TermAvailable bool        `json:"term_available"` // Web 终端可用：除 push 外任意模式（地址回退采集地址 host:22）或配了专用终端地址
	Latest        *latestView `json:"latest,omitempty"`
}

// newServerView 组装视图（列表/详情共用）。
func newServerView(srv store.Server) serverView {
	return serverView{
		ID: srv.ID, Name: srv.Name, AgentAddr: srv.AgentAddr,
		Hostname: srv.Hostname, OS: srv.OS, Kernel: srv.Kernel, Arch: srv.Arch,
		CPUModel: srv.CPUModel, CPUCores: srv.CPUCores, Enabled: srv.Enabled,
		NetIfaces: srv.NetIfaces, LastReportAt: srv.LastReportAt,
		CreatedAt: srv.CreatedAt, UpdatedAt: srv.UpdatedAt, CollectMode: srv.CollectMode,
		SSHUser: srv.SSHUser, SSHKeyPath: srv.SSHKeyPath, SSHAddr: srv.SSHAddr,
		GroupID: srv.GroupID, PurchasedOn: srv.PurchasedOn, ExpiresOn: srv.ExpiresOn,
		Remark: srv.Remark, Icon: srv.Icon, AgentVersion: srv.AgentVersion,
		TokenSet:       srv.Token != "",
		SSHPasswordSet: srv.SSHPassword != "",
		HasHostKey:     srv.SSHHostKey != "",
		Firing:         0,
		// 383：push 机器也进终端/文件列表——无专用地址时由凭据弹窗补填（连接前落库）
		TermAvailable: true,
	}
}

// dropTermConns 服务器 SSH 配置变更/删除后，丢弃该机的池化 SFTP 连接与临时
// 凭据——旧连接建立在旧凭据/旧地址上，不清会继续用到 10 分钟空闲回收为止。
func (h *ServerHandler) dropTermConns(id int64) {
	if h.term == nil {
		return
	}
	h.term.DropSFTP(id)
	h.term.ClearEphemeral(id)
}

// latestView 是最新一份指标的展示结构。
type latestView struct {
	TS       int64             `json:"ts"`
	CPU      float64           `json:"cpu_percent"`
	MemUsed  int64             `json:"mem_used"`
	MemTotal int64             `json:"mem_total"`
	Mem      float64           `json:"mem_percent"`
	Swap     float64           `json:"swap_percent"`
	Load1    float64           `json:"load1"`
	Load5    float64           `json:"load5"`
	Load15   float64           `json:"load15"`
	Uptime   int64             `json:"uptime"`
	TCPEst   int64             `json:"tcp_established"`
	Disks    []diskView        `json:"disks"`
	Nets     []netView         `json:"nets"`
	Listens  []listenView      `json:"listens"`
	Temps    []proto.TempPoint `json:"temps,omitempty"` // 旧版 agent 无此字段（缺省）；无传感器也不下发
	Fans     []proto.FanPoint  `json:"fans,omitempty"`  // 同上
	// 应用负载当前快照（54 飞牛集成）：容器与虚拟机列表；缺省=旧版 agent 或
	// 本机无 Docker/libvirt（前端隐藏对应卡片）
	Containers []proto.ContainerInfo `json:"containers,omitempty"`
	VMs        []proto.VMInfo        `json:"vms,omitempty"`
}

type diskView struct {
	Mount   string  `json:"mount"`
	Total   int64   `json:"total"`
	Used    int64   `json:"used"`
	Percent float64 `json:"percent"`
	Device  string  `json:"device,omitempty"` // 底层块设备名（如 sda1、dm-0）
	// IO 三字段不带 omitempty：空闲磁盘为 0 也要透传给前端（0 = 磁盘空闲，
	// 字段缺失 = 旧版 agent/旧数据无此能力），吞掉 0 会被误显示成 -
	IOUtil  float64 `json:"io_util"`      // IO 使用率（iostat %util 口径）；旧数据无此字段
	IORead  float64 `json:"io_read_bps"`  // 读速率 B/s；旧数据无此字段
	IOWrite float64 `json:"io_write_bps"` // 写速率 B/s；旧数据无此字段
}

type netView struct {
	Iface string  `json:"iface"`
	RxBps float64 `json:"rx_bps"`
	TxBps float64 `json:"tx_bps"`
}

type listenView struct {
	Proto string   `json:"proto"`
	Port  int      `json:"port"`
	Addrs []string `json:"addrs"`
}

func toLatestView(m *store.MetricRow) *latestView {
	if m == nil {
		return nil
	}
	v := &latestView{
		TS: m.TS, CPU: m.CPU, MemUsed: m.MemUsed, MemTotal: m.MemTotal, Mem: m.Mem,
		Swap: m.Swap, Load1: m.Load1, Load5: m.Load5, Load15: m.Load15,
		Uptime: m.Uptime, TCPEst: m.TCPEst,
	}
	for _, d := range m.Disks {
		v.Disks = append(v.Disks, diskView{Mount: d.Mount, Total: int64(d.Total), Used: int64(d.Used),
			Percent: d.Percent, Device: d.Device, IOUtil: d.IOUtil, IORead: d.IoRead, IOWrite: d.IoWrite})
	}
	for _, n := range m.Nets {
		v.Nets = append(v.Nets, netView{Iface: n.Iface, RxBps: n.RxBps, TxBps: n.TxBps})
	}
	for _, l := range m.Listens {
		v.Listens = append(v.Listens, listenView{Proto: l.Proto, Port: int(l.Port), Addrs: l.Addrs})
	}
	v.Temps = m.Temps
	v.Fans = m.Fans
	v.Containers = m.Containers
	v.VMs = m.VMs
	return v
}

// List 返回全部服务器 + 最新快照 + 在线状态 + firing 数。
// 最新快照一次批量查（每台 3 条 SQL 并成 3 条），机器多时避免 N+1 把
// 单连接查询队列拖长、卡住采集写入。
func (h *ServerHandler) List(c *gin.Context) {
	servers, err := h.st.ListServers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务器失败"})
		return
	}
	firing, err := h.st.FiringCountByServer()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询告警数失败"})
		return
	}
	ids := make([]int64, len(servers))
	for i, srv := range servers {
		ids[i] = srv.ID
	}
	latest, err := h.st.LatestMetrics(ids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询最新指标失败"})
		return
	}
	now := time.Now().Unix()
	out := make([]serverView, 0, len(servers))
	for _, srv := range servers {
		v := newServerView(srv)
		v.Firing = firing[srv.ID]
		v.Online = srv.Enabled && srv.LastReportAt > 0 && now-srv.LastReportAt <= h.offlineAfter
		v.Latest = toLatestView(latest[srv.ID])
		out = append(out, v)
	}
	c.JSON(http.StatusOK, gin.H{"servers": out})
}

// Stats 返回全局统计（顶栏四格用）：总数/在线/离线/告警中。只查 servers
// 表与 firing 计数、不取最新快照——顶栏 30s 轮询由全量 /servers 改走这里，
// 不再与总览刷新叠着反复触发整套快照查询与大响应。
func (h *ServerHandler) Stats(c *gin.Context) {
	servers, err := h.st.ListServerLites(false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务器失败"})
		return
	}
	firing, err := h.st.FiringCountByServer()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询告警数失败"})
		return
	}
	now := time.Now().Unix()
	st := struct {
		Total        int `json:"total"`
		Online       int `json:"online"`
		Offline      int `json:"offline"`
		Firing       int `json:"firing"`
		CertExpiring int `json:"cert_expiring"` // 15 天内到期（含已过期）的证书数
	}{}
	for _, srv := range servers {
		st.Total++
		if srv.Enabled && srv.LastReportAt > 0 && now-srv.LastReportAt <= h.offlineAfter {
			st.Online++
		} else if srv.Enabled {
			st.Offline++
		}
		st.Firing += firing[srv.ID]
	}
	if n, err := h.st.ExpiringCertCount(certcheck.RemindDays); err == nil {
		st.CertExpiring = n
	}
	c.JSON(http.StatusOK, st)
}

type createServerReq struct {
	Name        string `json:"name" binding:"required,max=100"`
	AgentAddr   string `json:"agent_addr" binding:"omitempty,max=200"`             // host[:port]，缺端口按模式补 :19100 / :22；push 模式不需要
	Token       string `json:"token" binding:"omitempty,max=100"`                  // agent 模式=可选共享密钥；push 模式=一对一上报密钥
	Mode        string `json:"mode" binding:"omitempty,oneof=agent ssh push node"` // 采集方式，缺省 agent
	SSHUser     string `json:"ssh_user" binding:"omitempty,max=100"`               // ssh 模式：登录用户
	SSHKeyPath  string `json:"ssh_key_path" binding:"omitempty,max=500"`           // ssh 模式：服务端私钥路径
	SSHPassword string `json:"ssh_password" binding:"omitempty,max=200"`           // ssh 模式：密码认证（空=私钥）
	SSHAddr     string `json:"ssh_addr" binding:"omitempty,max=200"`               // Web 终端/SFTP 专用地址（可选，任意模式；缺端口补 :22）
	GroupID     int64  `json:"group_id"`                                           // 所属分组；0=未分组
	Remark      string `json:"remark" binding:"omitempty,max=500"`                 // 备注（悬停服务器名展示）
}

// genToken 生成上报模式的接入密钥（32 字节随机数的 hex，与 v0.1 一致）。
func genToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Create 创建服务器条目。agent 模式记录指标端口由服务端拉取；ssh 模式直连执行；
// push 模式无地址，携带上报密钥（请求未带时服务端生成）。上报密钥与服务器
// 一对一（撞车返回 409）；agent 模式的抓取密钥可多台共用。
func (h *ServerHandler) Create(c *gin.Context) {
	var req createServerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称必填"})
		return
	}
	defaultPort := ":19100"
	switch req.Mode {
	case "ssh":
		// SSH 直连采集已下线（375）：显式拒绝，防静默落成 agent
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH 直连采集已下线，请改用 Agent 拉取或 node_exporter"})
		return
	case "push":
		// 上报模式无地址
	case "node":
		defaultPort = ":10200" // 本系统约定的 exporter 缺省端口（标准 node_exporter 为 :9100，按实际填写）
	default:
		req.Mode = "agent"
	}
	var addr string
	if req.Mode != "push" {
		addr = normalizeAddr(req.AgentAddr, defaultPort)
		if addr == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "地址必填"})
			return
		}
	}
	if req.GroupID > 0 {
		g, err := h.st.GetGroup(req.GroupID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分组失败"})
			return
		}
		if g == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "分组不存在"})
			return
		}
	}
	var srv *store.Server
	var err error
	if req.Mode == "node" {
		srv, err = h.st.CreateNodeServer(req.Name, addr, strings.TrimSpace(req.Token), req.GroupID)
	} else if req.Mode == "push" {
		token := strings.TrimSpace(req.Token)
		if token == "" {
			// 请求未携带密钥：服务端代生成一个（管理端随时可改）
			token, err = genToken()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "生成密钥失败"})
				return
			}
		}
		srv, err = h.st.CreatePushServer(req.Name, token, req.GroupID)
		if err == nil {
			h.setRemarkIfAny(srv.ID, req.Remark)
			h.setSSHAddrIfAny(srv.ID, req.SSHAddr)
			c.JSON(http.StatusOK, gin.H{"id": srv.ID, "name": srv.Name, "token": token})
			return
		}
		if errors.Is(err, store.ErrTokenUsed) {
			c.JSON(http.StatusConflict, gin.H{"error": "该上报密钥已被其他服务器使用（上报密钥需一对一，请点「生成」换一个）"})
			return
		}
	} else {
		srv, err = h.st.CreateServer(req.Name, addr, strings.TrimSpace(req.Token), req.GroupID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	h.setRemarkIfAny(srv.ID, req.Remark)
	h.setSSHAddrIfAny(srv.ID, req.SSHAddr)
	c.JSON(http.StatusOK, gin.H{"id": srv.ID, "name": srv.Name, "agent_addr": srv.AgentAddr})
}

// setRemarkIfAny 创建请求带了备注时补写（备注走 UpdateServer；失败不影响创建结果，
// 但必须留日志——否则用户的输入静默丢失无从排查）。
func (h *ServerHandler) setRemarkIfAny(id int64, remark string) {
	remark = strings.TrimSpace(remark)
	if remark == "" {
		return
	}
	if err := h.st.UpdateServer(id, store.ServerUpdate{Remark: &remark}); err != nil {
		log.Printf("服务器 #%d 补写备注失败: %v", id, err)
	}
}

// setSSHAddrIfAny 创建请求带了终端专用地址时补写（任意采集模式均可配，
// 规范化补 :22 端口；失败不影响创建结果，但必须留日志）。
func (h *ServerHandler) setSSHAddrIfAny(id int64, sshAddr string) {
	sshAddr = strings.TrimSpace(sshAddr)
	if sshAddr == "" {
		return
	}
	v := normalizeAddr(sshAddr, ":22")
	if v == "" {
		return
	}
	if err := h.st.UpdateServer(id, store.ServerUpdate{SSHAddr: &v}); err != nil {
		log.Printf("服务器 #%d 补写终端地址失败: %v", id, err)
	}
}

// normalizeAddr 规范化地址：去 scheme、去尾部路径与斜杠；
// 没有端口时补默认端口（agent 模式 :19100，node_exporter 惯例；ssh 模式 :22）。
func normalizeAddr(s, defaultPort string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "ssh://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return ""
	}
	if !strings.Contains(s, ":") {
		s += defaultPort
	}
	return s
}

func (h *ServerHandler) serverID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的服务器 ID"})
		return 0, false
	}
	return id, true
}

// Get 返回单台服务器详情（含最新快照）。
func (h *ServerHandler) Get(c *gin.Context) {
	id, ok := h.serverID(c)
	if !ok {
		return
	}
	srv, err := h.st.GetServer(id)
	if err != nil || srv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "服务器不存在"})
		return
	}
	v := newServerView(*srv)
	v.Online = srv.Enabled && srv.LastReportAt > 0 && time.Now().Unix()-srv.LastReportAt <= h.offlineAfter
	if m, err := h.st.LatestMetric(id); err == nil {
		v.Latest = toLatestView(m)
	}
	c.JSON(http.StatusOK, gin.H{"server": v})
}

type updateServerReq struct {
	Name      *string `json:"name" binding:"omitempty,max=100"`
	Enabled   *bool   `json:"enabled"`
	NetIfaces *string `json:"net_ifaces" binding:"omitempty,max=200"` // 逗号分隔；空串=展示全部
	AgentAddr *string `json:"agent_addr" binding:"omitempty,max=200"` // 传空串表示清空
	// Token/SSHPassword 打码配套语义：非空=更新；空串/缺省=不变；清除走 Clear* 字段
	Token            *string `json:"token" binding:"omitempty,max=100"` // agent=共享抓取密钥；push=一对一上报密钥
	Mode             *string `json:"mode" binding:"omitempty,oneof=agent ssh push node"`
	SSHUser          *string `json:"ssh_user" binding:"omitempty,max=100"`
	SSHKeyPath       *string `json:"ssh_key_path" binding:"omitempty,max=500"`
	SSHPassword      *string `json:"ssh_password" binding:"omitempty,max=200"` // 密码认证
	SSHAddr          *string `json:"ssh_addr" binding:"omitempty,max=200"`     // Web 终端/SFTP 专用地址；传空串=清空（回退采集地址）
	ClearSSHPassword *bool   `json:"clear_ssh_password"`                       // 清空已存密码（回退私钥认证）；与 ssh_password 同交时 clear 优先
	ClearToken       *bool   `json:"clear_token"`                              // 清空密钥（push 模式禁止——没有密钥的 push 服务器无法上报）
	ResetHostKey     *bool   `json:"reset_host_key"`                           // 清空 TOFU 指纹，下次连接重新学习
	GroupID          *int64  `json:"group_id"`                                 // 所属分组；传 0=移到未分组
	PurchasedOn      *string `json:"purchased_on"`                             // 购买日期 YYYY-MM-DD；传空串=清空
	ExpiresOn        *string `json:"expires_on"`                               // 到期日期 YYYY-MM-DD；传空串=清空
	Remark           *string `json:"remark" binding:"omitempty,max=500"`       // 备注；传空串=清空
	Icon             *string `json:"icon" binding:"omitempty,max=500"`         // 图标 URL；传空串=清空（不显示）
}

// normalizeIfaces 规范化网口列表：按逗号/空白切分，去空去重后逗号连接。
func normalizeIfaces(s string) string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '，'
	}) {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, ",")
}

// Update 改名/启停/展示网口/地址与密钥/切换采集方式。
func (h *ServerHandler) Update(c *gin.Context) {
	id, ok := h.serverID(c)
	if !ok {
		return
	}
	var req updateServerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	// 按目标（或当前）模式确定缺省端口
	cur, _ := h.st.GetServer(id)
	mode := req.Mode
	if mode == nil && cur != nil {
		mode = &cur.CollectMode
	}
	// SSH 直连采集已下线（375）：存量 ssh 机器保持原模式可继续编辑其他字段，
	// 但不允许把别的机器改成 ssh 模式
	if req.Mode != nil && *req.Mode == "ssh" && (cur == nil || cur.CollectMode != "ssh") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "SSH 直连采集已下线，请改用 Agent 拉取或 node_exporter"})
		return
	}
	// 购买/到期日期：先与当前值合并（指针 nil=不改），再做格式与先后顺序校验
	purch, exp := "", ""
	if cur != nil {
		purch, exp = cur.PurchasedOn, cur.ExpiresOn
	}
	if req.PurchasedOn != nil {
		purch = strings.TrimSpace(*req.PurchasedOn)
		req.PurchasedOn = &purch
	}
	if req.ExpiresOn != nil {
		exp = strings.TrimSpace(*req.ExpiresOn)
		req.ExpiresOn = &exp
	}
	for _, d := range []struct{ name, val string }{{"购买日期", purch}, {"到期日期", exp}} {
		if d.val == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d.val); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": d.name + "格式应为 YYYY-MM-DD"})
			return
		}
	}
	if purch != "" && exp != "" && exp < purch {
		c.JSON(http.StatusBadRequest, gin.H{"error": "到期日期不能早于购买日期"})
		return
	}
	defaultPort := ":19100"
	switch {
	case mode != nil && *mode == "ssh":
		defaultPort = ":22"
	case mode != nil && *mode == "node":
		defaultPort = ":10200" // 与 Create 同口径
	}
	if req.NetIfaces != nil {
		v := normalizeIfaces(*req.NetIfaces)
		req.NetIfaces = &v
	}
	// push 模式服务端不主动连接，没有地址概念，跳过规范化
	if req.AgentAddr != nil && (mode == nil || *mode != "push") {
		v := normalizeAddr(*req.AgentAddr, defaultPort)
		if v == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "地址无效"})
			return
		}
		req.AgentAddr = &v
	}
	// 终端专用地址独立规范化：固定 :22 缺省端口；空串=清空（回退采集地址）
	if req.SSHAddr != nil {
		v := strings.TrimSpace(*req.SSHAddr)
		if v != "" {
			v = normalizeAddr(v, ":22")
			if v == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "终端 SSH 地址无效"})
				return
			}
		}
		req.SSHAddr = &v
	}
	if req.GroupID != nil && *req.GroupID > 0 {
		g, err := h.st.GetGroup(*req.GroupID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分组失败"})
			return
		}
		if g == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "分组不存在"})
			return
		}
	}
	// 打码配套：密钥/密码只在非空时透传（空=不变），Clear* 翻译为写空串
	if req.SSHPassword != nil && *req.SSHPassword == "" {
		req.SSHPassword = nil
	}
	if req.Token != nil && *req.Token == "" {
		req.Token = nil
	}
	if req.ClearSSHPassword != nil && *req.ClearSSHPassword {
		v := ""
		req.SSHPassword = &v
	}
	if req.ClearToken != nil && *req.ClearToken {
		if mode != nil && *mode == "push" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "上报密钥不可清空（没有密钥的上报服务器无法上报），请改用「生成新密钥」"})
			return
		}
		v := ""
		req.Token = &v
	}
	upd := store.ServerUpdate{
		Name: req.Name, Enabled: req.Enabled, NetIfaces: req.NetIfaces,
		AgentAddr: req.AgentAddr, Token: req.Token,
		CollectMode: req.Mode, SSHUser: req.SSHUser, SSHKeyPath: req.SSHKeyPath,
		SSHPassword: req.SSHPassword, SSHAddr: req.SSHAddr,
		ResetHostKey: req.ResetHostKey, GroupID: req.GroupID,
		PurchasedOn: req.PurchasedOn, ExpiresOn: req.ExpiresOn, Remark: req.Remark, Icon: req.Icon,
	}
	if err := h.st.UpdateServer(id, upd); err != nil {
		if errors.Is(err, store.ErrTokenUsed) {
			c.JSON(http.StatusConflict, gin.H{"error": "该上报密钥已被其他服务器使用（上报密钥需一对一）"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	// SSH 相关配置变更：丢弃池化 SFTP 连接与临时凭据，下次操作用新配置重建
	if req.Mode != nil || req.AgentAddr != nil || req.SSHUser != nil || req.SSHKeyPath != nil ||
		req.SSHAddr != nil || req.ResetHostKey != nil || req.SSHPassword != nil {
		h.dropTermConns(id)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Delete 删除服务器及其关联数据。
func (h *ServerHandler) Delete(c *gin.Context) {
	id, ok := h.serverID(c)
	if !ok {
		return
	}
	h.st.ExpirePtTasksByServer(id)   // 该机中转的 PT 抓取任务一并作废（391）
	h.st.ExpireNetTasksByServer(id)  // 该机中转的通用中转任务一并作废（548）
	h.st.ExpireBiliTasksByServer(id) // 该机执行的 B 站追更任务一并作废（TODO 6）
	h.st.ExpireAppTasksByServer(id)  // 该机的容器/虚拟机操作任务一并作废（54）
	if err := h.st.DeleteServer(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	h.dropTermConns(id)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// History 查询历史曲线，一次返回详情页全部序列：
//
//	{"from":..,"to":..,"step":60,"series":[{"key":"cpu","points":[[t,v],..]},..]}
//
// key 取值：cpu/mem/swap/load1/load5/load15/tcp_est/mem_used/mem_total、
// disk.<mount>、net.<iface>.rx|tx、diskio.<device>.read|write。
func (h *ServerHandler) History(c *gin.Context) {
	id, ok := h.serverID(c)
	if !ok {
		return
	}
	from, to, step, err := parseRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	series, err := historySeries(h.st, id, from, to, step, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询历史失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"from": from, "to": to, "step": step, "series": series})
}

// historySeries 查询一台服务器一段时间的全部曲线序列（详情页 History 与
// 总览自定义图表共用）。时间戳转为毫秒（ECharts time 轴）。
// needNets=false 时跳过 SampledRows（每桶采样 + nets JSON 解析的第二次范围
// 扫描）——总览图表全是标量时省掉，只有详情页与网卡图需要网络序列。
func historySeries(st *store.Store, id, from, to, step int64, needNets bool) ([]seriesOut, error) {
	series := make([]seriesOut, 0, 16)
	// 标量指标：SQL 分桶平均
	buckets, err := st.AggregateScalars(id, from, to, step)
	if err != nil {
		return nil, err
	}
	addScalar := func(key string, pick func(b store.Bucket) float64) {
		s := seriesOut{Key: key}
		for _, b := range buckets {
			s.Points = append(s.Points, [2]any{b.TS * 1000, pick(b)})
		}
		s.Points = insertGaps(s.Points, step*1500)
		series = append(series, s)
	}
	addScalar("cpu", func(b store.Bucket) float64 { return b.CPU })
	addScalar("mem", func(b store.Bucket) float64 { return b.Mem })
	addScalar("swap", func(b store.Bucket) float64 { return b.Swap })
	addScalar("load1", func(b store.Bucket) float64 { return b.Load1 })
	addScalar("load5", func(b store.Bucket) float64 { return b.Load5 })
	addScalar("load15", func(b store.Bucket) float64 { return b.Load15 })
	addScalar("tcp_est", func(b store.Bucket) float64 { return b.TCPEst })
	addScalar("mem_used", func(b store.Bucket) float64 { return b.MemUsed })
	addScalar("mem_total", func(b store.Bucket) float64 { return b.MemTotal })

	// 网络/磁盘 IO：每桶取最后一个样本（磁盘容量仅存最新、无历史曲线，不产出序列）
	if !needNets {
		return series, nil
	}
	rows, err := st.SampledRows(id, from, to, step)
	if err != nil {
		return nil, err
	}
	netSeries := map[string][][2]any{}
	ioSeries := map[string][][2]any{}
	tempSeries := map[string][][2]any{}
	fanSeries := map[string][][2]any{}
	for _, m := range rows {
		for _, n := range m.Nets {
			netSeries["net."+n.Iface+".rx"] = append(netSeries["net."+n.Iface+".rx"], [2]any{m.TS * 1000, n.RxBps})
			netSeries["net."+n.Iface+".tx"] = append(netSeries["net."+n.Iface+".tx"], [2]any{m.TS * 1000, n.TxBps})
		}
		for _, d := range m.DiskIOs {
			ioSeries["diskio."+d.Device+".read"] = append(ioSeries["diskio."+d.Device+".read"], [2]any{m.TS * 1000, d.ReadBps})
			ioSeries["diskio."+d.Device+".write"] = append(ioSeries["diskio."+d.Device+".write"], [2]any{m.TS * 1000, d.WriteBps})
		}
		for _, t := range m.Temps {
			tempSeries["temp."+t.Sensor] = append(tempSeries["temp."+t.Sensor], [2]any{m.TS * 1000, t.Temp})
		}
		for _, f := range m.Fans {
			fanSeries["fan."+f.Sensor] = append(fanSeries["fan."+f.Sensor], [2]any{m.TS * 1000, f.RPM})
		}
	}
	appendSorted := func(m map[string][][2]any) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			series = append(series, seriesOut{Key: k, Points: insertGaps(m[k], step*1500)})
		}
	}
	appendSorted(netSeries)
	appendSorted(ioSeries)
	appendSorted(tempSeries)
	appendSorted(fanSeries)
	return series, nil
}

// insertGaps 数据缺口归零：相邻点间隔超过 1.5×step（缺了至少一个桶）时，
// 在缺口两端插 0 值点——折线垂直掉到 0 走平线再回升，而不是斜线平滑跨过
// 缺口（服务器离线时段在图上一目了然，与 Prometheus 的缺口行为语义一致）。
// 时间轴已是毫秒（ECharts time 轴），阈值由调用方换算。
func insertGaps(points [][2]any, gapMs int64) [][2]any {
	if len(points) < 2 || gapMs <= 0 {
		return points
	}
	out := make([][2]any, 0, len(points)+8)
	prev, _ := points[0][0].(int64)
	out = append(out, points[0])
	for _, p := range points[1:] {
		t, _ := p[0].(int64)
		if t-prev >= gapMs {
			out = append(out, [2]any{prev + 1, 0}, [2]any{t - 1, 0})
		}
		out = append(out, p)
		prev = t
	}
	return out
}

type seriesOut struct {
	Key    string   `json:"key"`
	Points [][2]any `json:"points"`
}

var stepTiers = []int64{10, 30, 60, 300, 900, 1800, 3600, 7200, 21600, 86400}

// parseRange 解析 from/to/step 查询参数，缺省最近 1 小时；
// step 未指定时按 range/500 自动取档。
func parseRange(c *gin.Context) (from, to, step int64, err error) {
	now := time.Now().Unix()
	to = now
	from = now - 3600
	if v := c.Query("to"); v != "" {
		if to, err = strconv.ParseInt(v, 10, 64); err != nil {
			return 0, 0, 0, fmt.Errorf("to 参数无效")
		}
	}
	if v := c.Query("from"); v != "" {
		if from, err = strconv.ParseInt(v, 10, 64); err != nil {
			return 0, 0, 0, fmt.Errorf("from 参数无效")
		}
	}
	if from >= to {
		return 0, 0, 0, fmt.Errorf("from 必须早于 to")
	}
	if to-now > 300 { // 允许 5 分钟时钟偏差
		return 0, 0, 0, fmt.Errorf("to 不能超过当前时间")
	}
	if v := c.Query("step"); v != "" {
		if step, err = strconv.ParseInt(v, 10, 64); err != nil || step < 10 {
			return 0, 0, 0, fmt.Errorf("step 参数无效（最小 10 秒）")
		}
		return from, to, step, nil
	}
	rangeSec := to - from
	want := rangeSec / 500
	for _, t := range stepTiers {
		if t >= want {
			return from, to, t, nil
		}
	}
	return from, to, stepTiers[len(stepTiers)-1], nil
}
