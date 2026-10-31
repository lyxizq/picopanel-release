package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/integrations"
	"lz-monitor/internal/server/store"
)

// maxHADevices /ha 页面收藏设备上限（磁贴一屏量级，防误增泛滥）。
const maxHADevices = 32

// maxHASensors /ha 页面状态实体上限（368）。
const maxHASensors = 32

// maxHARooms /ha 页面房间上限（371）。
const maxHARooms = 12

// HAHandler Home Assistant 设备控制（355 建；475 拆分）：/ha 独立页面消费。
// sources/devices/call 挂任意登录用户组（361 受限用户开放的全部功能）；catalog/
// devices-config/master 挂管理员组。475 起设备清单与连接参数存独立总配置
// （settings 键 ha_master），与服务集成的 homeassistant 行完全互不影响——
// 集成行只服务前台卡片（entities）。
type HAHandler struct {
	st  *store.Store
	mgr *integrations.Manager
}

// haSource HA 来源（475 起恒为单一总配置来源；保留结构与 localStorage 记忆兼容）。
type haSource struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Sources 来源清单：475 起固定单一来源 id=0（前端切换器单源自动隐藏）。
func (h *HAHandler) Sources(c *gin.Context) {
	m, err := h.st.GetHAMaster()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询智能家居配置失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sources": []haSource{{ID: 0, Name: "智能家居", Enabled: m.URL != ""}}})
}

// haTarget 智能家居总配置 → 合成 Integration（475）：mgr.Current/Kick 按 ID 缓存，
// ID 恒 0（真集成行自 1 起自增不撞）；Auth 组装成凭据 JSON 供 ParseCredentials，
// Config 组装 rooms/devices/sensors 供 ParseHAConfig（无 entities 段，fetchHA 的
// items 为空不影响 HA 页消费 states）。未配置连接时已写出 400。
func (h *HAHandler) haTarget(c *gin.Context) *store.Integration {
	m, err := h.st.GetHAMaster()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询智能家居配置失败"})
		return nil
	}
	if m.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "智能家居尚未配置连接（管理设备 → 连接配置）"})
		return nil
	}
	cfg, err := json.Marshal(map[string]any{"rooms": m.Rooms, "devices": m.Devices, "sensors": m.Sensors})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "组装配置失败"})
		return nil
	}
	auth, _ := json.Marshal(map[string]string{"token": m.Token})
	return &store.Integration{ID: 0, Type: store.IntegrationHA, URL: m.URL,
		Auth: string(auth), Config: string(cfg), Enabled: true}
}

// haEnvInfo 环境电器信息（388）：climate/humidifier/water_heater/fan 的属性
// 透传（当前/目标温湿度、运行动作、风速、预设、可调范围），仅这些域有值。
type haEnvInfo struct {
	CurTemp    *float64 `json:"cur_temp,omitempty"`
	TgtTemp    *float64 `json:"tgt_temp,omitempty"`
	CurHum     *float64 `json:"cur_hum,omitempty"`
	TgtHum     *float64 `json:"tgt_hum,omitempty"`
	HvacAction string   `json:"action,omitempty"`
	FanMode    string   `json:"fan,omitempty"`
	Pct        *float64 `json:"pct,omitempty"`
	Preset     string   `json:"preset,omitempty"`
	MinTemp    *float64 `json:"min_temp,omitempty"`
	MaxTemp    *float64 `json:"max_temp,omitempty"`
	MinHum     *float64 `json:"min_hum,omitempty"`
	MaxHum     *float64 `json:"max_hum,omitempty"`
}

// haEnvFromState HAState → haEnvInfo（无任何环境属性时返回 nil，行内不带 env 键）。
func haEnvFromState(st integrations.HAState) *haEnvInfo {
	e := &haEnvInfo{
		CurTemp: st.CurTemp, TgtTemp: st.TgtTemp, CurHum: st.CurHum, TgtHum: st.TgtHum,
		HvacAction: st.HvacAction, FanMode: st.FanMode, Pct: st.Pct, Preset: st.Preset,
		MinTemp: st.MinTemp, MaxTemp: st.MaxTemp, MinHum: st.MinHum, MaxHum: st.MaxHum,
	}
	if e.CurTemp == nil && e.TgtTemp == nil && e.CurHum == nil && e.TgtHum == nil &&
		e.HvacAction == "" && e.FanMode == "" && e.Pct == nil && e.Preset == "" {
		return nil
	}
	return e
}

// haDeviceRow /ha 页面一行设备：收藏配置 + 实时状态装饰（on/unavailable 后端
// 判好，前端只管渲染与开关）。
type haDeviceRow struct {
	Entity      string     `json:"entity"`
	Label       string     `json:"label"`
	Domain      string     `json:"domain"`
	Room        string     `json:"room,omitempty"`
	State       string     `json:"state"`
	Friendly    string     `json:"friendly,omitempty"`
	On          bool       `json:"on"`
	Unavailable bool       `json:"unavailable"`
	IconOn      string     `json:"icon_on,omitempty"`
	IconOff     string     `json:"icon_off,omitempty"`
	Missing     bool       `json:"missing,omitempty"` // 上游查无此实体（已删除/离线剔除）
	Env         *haEnvInfo `json:"env,omitempty"`     // 环境电器属性（388，仅环境域）
}

// haSensorRow /ha 页面一行状态实体（368）：只读展示，state+单位取快照。
type haSensorRow struct {
	Entity      string `json:"entity"`
	Label       string `json:"label"`
	Room        string `json:"room,omitempty"`
	State       string `json:"state"`
	Unit        string `json:"unit,omitempty"`
	Friendly    string `json:"friendly,omitempty"`
	Unavailable bool   `json:"unavailable"`
	Missing     bool   `json:"missing,omitempty"` // 上游查无此实体（已删除/上游不可达）
}

// Devices 收藏设备 + 状态实体 + 实时状态（Manager 缓存快照，缺时同步补拉）。
func (h *HAHandler) Devices(c *gin.Context) {
	in := h.haTarget(c)
	if in == nil {
		return
	}
	cfg := integrations.ParseHAConfig(in.Config)
	p := h.mgr.Current(*in)
	states, _ := p.Data["states"].(map[string]integrations.HAState)
	if states == nil {
		states = map[string]integrations.HAState{} // 上游不可达也返回收藏清单（全标离线），页面不至于空白
	}
	rows := make([]haDeviceRow, 0, len(cfg.Devices))
	for _, d := range cfg.Devices {
		domain, _, _ := strings.Cut(d.Entity, ".")
		row := haDeviceRow{Entity: d.Entity, Label: d.Label, Domain: domain, Room: d.Room,
			IconOn: d.IconOn, IconOff: d.IconOff, Unavailable: true}
		if st, ok := states[d.Entity]; ok {
			row.State = st.State
			row.Friendly = st.Name
			row.On = st.On()
			row.Unavailable = st.Unavailable()
			row.Env = haEnvFromState(st)
			if row.Label == "" {
				row.Label = st.Name
			}
		} else {
			row.Missing = true // 上游状态表查无此实体（已删除/不可控域/上游不可达）
		}
		if row.Label == "" {
			row.Label = d.Entity
		}
		rows = append(rows, row)
	}
	srows := make([]haSensorRow, 0, len(cfg.Sensors))
	for _, sn := range cfg.Sensors {
		row := haSensorRow{Entity: sn.Entity, Label: sn.Label, Room: sn.Room, Unavailable: true}
		if st, ok := states[sn.Entity]; ok {
			row.State = st.State
			row.Unit = st.Unit
			row.Friendly = st.Name
			row.Unavailable = st.Unavailable()
			if row.Label == "" {
				row.Label = st.Name
			}
		} else {
			row.Missing = true
		}
		if row.Label == "" {
			row.Label = sn.Entity
		}
		srows = append(srows, row)
	}
	out := gin.H{"ok": p.OK, "devices": rows, "sensors": srows, "rooms": cfg.Rooms}
	if !p.OK {
		out["error"] = p.Error
	}
	c.JSON(http.StatusOK, out)
}

type haCallReq struct {
	Source   int64    `json:"source"` // 475 起忽略（单一总配置来源，兼容旧端发 0）
	EntityID string   `json:"entity_id" binding:"required"`
	Action   string   `json:"action" binding:"required"`
	Value    *float64 `json:"value,omitempty"` // action=set 的目标温度/湿度（388）
}

// Call 开关一台设备 / 调节环境电器目标值。仅允许操作收藏清单内的实体（受限
// 用户可访问本端点，收藏清单即白名单——管理员没配置的设备对任何人都不可控）。
// action=turn_on/turn_off/toggle 映射域内真服务（lock/cover/vacuum）由
// integrations 层转换；action=set 调目标温度（climate/water_heater）或目标
// 湿度（humidifier）。返回读回后的最新状态。
func (h *HAHandler) Call(c *gin.Context) {
	var req haCallReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if req.Action != "turn_on" && req.Action != "turn_off" && req.Action != "toggle" && req.Action != "set" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的动作"})
		return
	}
	if req.Action == "set" && req.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少目标值"})
		return
	}
	if req.Action == "set" && !integrations.HASettableDomain(req.EntityID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该实体不支持目标值调节"})
		return
	}
	in := h.haTarget(c)
	if in == nil {
		return
	}
	cfg := integrations.ParseHAConfig(in.Config)
	found := false
	for _, d := range cfg.Devices {
		if d.Entity == req.EntityID {
			found = true
			break
		}
	}
	if !found {
		c.JSON(http.StatusBadRequest, gin.H{"error": "设备不在收藏清单中"})
		return
	}
	var st *integrations.HAState
	var err error
	if req.Action == "set" {
		st, err = integrations.HASetValue(*in, req.EntityID, *req.Value)
	} else {
		st, err = integrations.HACall(*in, req.EntityID, req.Action)
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	h.mgr.Kick(*in) // 后台同步缓存，30s 轮询口径外的窗口尽快收敛
	domain, _, _ := strings.Cut(st.EntityID, ".")
	c.JSON(http.StatusOK, gin.H{"device": haDeviceRow{
		Entity: st.EntityID, Label: st.Name, Domain: domain, State: st.State,
		Friendly: st.Name, On: st.On(), Unavailable: st.Unavailable(), Env: haEnvFromState(*st),
	}})
}

// Catalog 可控实体目录（管理员配置收藏设备用）：快照里可控域白名单内全部
// 实体按域分组，域名字典序。
func (h *HAHandler) Catalog(c *gin.Context) {
	in := h.haTarget(c)
	if in == nil {
		return
	}
	p := h.mgr.Current(*in)
	if !p.OK {
		c.JSON(http.StatusBadGateway, gin.H{"error": p.Error})
		return
	}
	states, _ := p.Data["states"].(map[string]integrations.HAState)
	byDomain := map[string][]gin.H{}
	for _, st := range states {
		name := st.Name
		if name == "" {
			name = st.EntityID
		}
		byDomain[st.Domain] = append(byDomain[st.Domain], gin.H{
			"entity": st.EntityID, "label": name, "state": st.State,
		})
	}
	domains := make([]string, 0, len(byDomain))
	for d := range byDomain {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	out := make([]gin.H, 0, len(domains))
	for _, d := range domains {
		ents := byDomain[d]
		sort.Slice(ents, func(i, j int) bool { return ents[i]["entity"].(string) < ents[j]["entity"].(string) })
		out = append(out, gin.H{"domain": d, "entities": ents})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "catalog": out})
}

type haDevicesReq struct {
	Source  int64                   `json:"source"` // 475 起忽略（同上）
	Rooms   []integrations.HARoom   `json:"rooms"`
	Devices []integrations.HADevice `json:"devices"`
	Sensors []integrations.HASensor `json:"sensors"`
}

// SaveDevices 保存房间/收藏设备/状态实体清单（管理员）：合并进集成 config JSON
// （entities/insecure 等其他键原样保留），设备校验域白名单、状态实体校验
// entity_id 形态，三列各自去重限长；device/sensor 的 room 必须引用存在的房间。
func (h *HAHandler) SaveDevices(c *gin.Context) {
	var req haDevicesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if len(req.Rooms) > maxHARooms {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多 %d 个房间", maxHARooms)})
		return
	}
	if len(req.Devices) > maxHADevices {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多 %d 台设备", maxHADevices)})
		return
	}
	if len(req.Sensors) > maxHASensors {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多 %d 个状态实体", maxHASensors)})
		return
	}
	rids := map[string]bool{}
	rnames := map[string]bool{}
	for _, rm := range req.Rooms {
		id, name := strings.TrimSpace(rm.ID), strings.TrimSpace(rm.Name)
		if id == "" || len(id) > 40 || strings.ContainsAny(id, " \t\r\n") || name == "" || len([]rune(name)) > 20 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "房间配置无效（名称 1~20 字）"})
			return
		}
		if rids[id] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "房间重复: " + id})
			return
		}
		if rnames[name] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "房间名称重复: " + name})
			return
		}
		rids[id] = true
		rnames[name] = true
	}
	seen := map[string]bool{}
	for _, d := range req.Devices {
		if !integrations.HAControllableDomain(d.Entity) || len(d.Entity) > 120 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "设备实体无效或属于不可控域: " + d.Entity})
			return
		}
		if len(d.Label) > 60 || len(d.IconOn) > 500 || len(d.IconOff) > 500 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "设备名称/图标地址超长"})
			return
		}
		if d.Room != "" && !rids[d.Room] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "设备引用了不存在的房间: " + d.Room})
			return
		}
		if seen[d.Entity] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "设备重复: " + d.Entity})
			return
		}
		seen[d.Entity] = true
	}
	sseen := map[string]bool{}
	for _, sn := range req.Sensors {
		domain, obj, ok := strings.Cut(sn.Entity, ".")
		if !ok || domain == "" || obj == "" || domain != strings.ToLower(domain) ||
			strings.ContainsAny(sn.Entity, " \t\r\n") || len(sn.Entity) > 120 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "状态实体无效: " + sn.Entity})
			return
		}
		if len(sn.Label) > 60 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "状态实体名称超长"})
			return
		}
		if sn.Room != "" && !rids[sn.Room] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "状态实体引用了不存在的房间: " + sn.Room})
			return
		}
		if sseen[sn.Entity] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "状态实体重复: " + sn.Entity})
			return
		}
		sseen[sn.Entity] = true
	}
	// 保存进智能家居总配置（475）：rooms/devices/sensors 三段覆盖、连接参数
	// 由 /ha/master 单独维护（SaveHAMasterLists 原样保留）
	rooms, err := json.Marshal(req.Rooms)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "序列化清单失败"})
		return
	}
	devices, err := json.Marshal(req.Devices)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "序列化清单失败"})
		return
	}
	sensors, err := json.Marshal(req.Sensors)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "序列化清单失败"})
		return
	}
	if err := h.st.SaveHAMasterLists(rooms, devices, sensors); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- 智能家居总配置·连接参数（475）----

// GetMaster 连接配置（管理员）：token 不回显只回 has_token。
func (h *HAHandler) GetMaster(c *gin.Context) {
	m, err := h.st.GetHAMaster()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询智能家居配置失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": m.URL, "insecure": m.Insecure, "has_token": m.Token != ""})
}

type haMasterReq struct {
	URL      string `json:"url"`
	Token    string `json:"token"`    // 空=沿用库内值
	Insecure *bool  `json:"insecure"` // nil=沿用
}

// SaveMaster 保存连接参数（管理员）：地址须 http(s):// 或空（空=未配置态）。
func (h *HAHandler) SaveMaster(c *gin.Context) {
	var req haMasterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	url := strings.TrimSpace(req.URL)
	if url != "" && (len(url) > 2048 || !(strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://"))) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "服务地址须为 http(s):// 开头"})
		return
	}
	m, err := h.st.GetHAMaster()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询智能家居配置失败"})
		return
	}
	insecure := m.Insecure
	if req.Insecure != nil {
		insecure = *req.Insecure
	}
	if err := h.st.SaveHAMasterConnection(url, strings.TrimSpace(req.Token), insecure); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---- HA 历史数据（511/512，2026-10-09）：开关日志清单 + 数值曲线 ----

// haEntityAllowed 校验查询的实体在收藏清单内（设备 + 传感器）。管理员放开
//（配置者视角），受限用户（361 仅开放 HA 页面）限收藏实体——与 /ha/call 同
// 口径，防枚举库内任意实体拉他人数据。
func (h *HAHandler) haEntityAllowed(c *gin.Context, entities ...string) bool {
	if c.GetString("role") != "user" {
		return true
	}
	in := h.haTarget(c)
	if in == nil {
		return false
	}
	cfg := integrations.ParseHAConfig(in.Config)
	allowed := make(map[string]bool, len(cfg.Devices)+len(cfg.Sensors))
	for _, d := range cfg.Devices {
		allowed[d.Entity] = true
	}
	for _, s := range cfg.Sensors {
		allowed[s.Entity] = true
	}
	for _, e := range entities {
		if !allowed[e] {
			return false
		}
	}
	return true
}

// EntityEvents GET /ha/entity/events?entity=light.x&limit=200：单实体开合翻转
// 清单（新→旧）。/ha 页磁贴/门窗芯片点详情弹层消费。
func (h *HAHandler) EntityEvents(c *gin.Context) {
	entity := strings.TrimSpace(c.Query("entity"))
	if entity == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 entity 参数"})
		return
	}
	if !h.haEntityAllowed(c, entity) {
		c.JSON(http.StatusForbidden, gin.H{"error": "实体不在收藏清单中"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	events, err := h.st.HAEntityEvents(entity, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if events == nil {
		events = []store.HAEvent{}
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

// EntityHistory GET /ha/entity/history?entities=a,b&hours=24：数值传感器曲线
// 序列（最多 6 个实体）。服务端选桶宽（区间/500，下限 1s）：≤7 天落在采样
// 明细、更早落在 5 分钟归档桶，跨界自动拼接（留存口径同服务器监控）。
func (h *HAHandler) EntityHistory(c *gin.Context) {
	var ents []string
	for _, e := range strings.Split(c.Query("entities"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			ents = append(ents, e)
		}
	}
	if len(ents) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 entities 参数"})
		return
	}
	if len(ents) > 6 {
		ents = ents[:6]
	}
	if !h.haEntityAllowed(c, ents...) {
		c.JSON(http.StatusForbidden, gin.H{"error": "实体不在收藏清单中"})
		return
	}
	hours, _ := strconv.ParseFloat(c.DefaultQuery("hours", "24"), 64)
	if !(hours > 0) {
		hours = 24
	}
	if hours > 90*24 {
		hours = 90 * 24
	}
	secs := int64(hours*3600 + 0.5) // 四舍五入：前端 5/60 这类小数 hours 换算回整秒
	if secs < 60 {
		secs = 60
	}
	now := time.Now().Unix()
	from := now - secs
	step := secs / 500
	if step < 1 {
		step = 1
	}
	type series struct {
		Entity string        `json:"entity"`
		Points [][2]float64  `json:"points"`
	}
	out := make([]series, 0, len(ents))
	for _, e := range ents {
		points, err := h.st.HASeries(e, from, now, step)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if points == nil {
			points = [][2]float64{}
		}
		out = append(out, series{Entity: e, Points: points})
	}
	c.JSON(http.StatusOK, gin.H{"series": out})
}
