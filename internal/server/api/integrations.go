package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/integrations"
	"lz-monitor/internal/server/store"
)

// 上限：集成个数与 HA 实体行数（防误增泛滥，收藏夹同量级口径）。
const (
	maxIntegrations = 8
	maxHAEntities   = 16
)

// IntegrationHandler 服务集成（Homepage widget 同口径）：CRUD + 测试 + 主页快照。
type IntegrationHandler struct {
	st  *store.Store
	mgr *integrations.Manager
}

// integrationView 列表视图：凭据绝不外发，只回 has_auth 标记。
type integrationView struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	URL       string          `json:"url"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config"`
	HasAuth   bool            `json:"has_auth"`
	LastOKAt  int64           `json:"last_ok_at"`
	LastError string          `json:"last_error"`
}

func (h *IntegrationHandler) view(in *store.Integration) integrationView {
	return integrationView{
		ID: in.ID, Type: in.Type, Name: in.Name, URL: in.URL, Enabled: in.Enabled,
		Config: json.RawMessage(in.Config), HasAuth: in.HasAuth,
		LastOKAt: in.LastOKAt, LastError: in.LastError,
	}
}

// List 全部集成（管理列表）。
func (h *IntegrationHandler) List(c *gin.Context) {
	list, err := h.st.ListIntegrationMetas()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询服务集成失败"})
		return
	}
	out := make([]integrationView, 0, len(list))
	for i := range list {
		out = append(out, h.view(&list[i]))
	}
	c.JSON(http.StatusOK, gin.H{"integrations": out})
}

type integrationAuthReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

type integrationReq struct {
	ID      *int64              `json:"id"` // Test 用：编辑已保存集成时留空凭据沿用库内值
	Type    string              `json:"type"`
	Name    string              `json:"name"`
	URL     string              `json:"url"`
	Enabled *bool               `json:"enabled"`
	Config  json.RawMessage     `json:"config"`
	Auth    *integrationAuthReq `json:"auth"`
}

// check 校验公共字段，返回清洗后的值；失败时已写出 400。
func (h *IntegrationHandler) check(c *gin.Context, req *integrationReq) (name, url string, ok bool) {
	if !store.IntegrationTypes()[req.Type] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的集成类型"})
		return "", "", false
	}
	name = strings.TrimSpace(req.Name)
	if name == "" || len(name) > 60 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "名称必填且不超过 60 字"})
		return "", "", false
	}
	url = strings.TrimSpace(req.URL)
	if url != "" && (len(url) > 2048 || !(strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://"))) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "服务地址须为 http(s):// 开头"})
		return "", "", false
	}
	// Docker 本机集成（440）走 unix socket，无服务地址
	if url == "" && req.Type != store.IntegrationDocker {
		c.JSON(http.StatusBadRequest, gin.H{"error": "服务地址须为 http(s):// 开头"})
		return "", "", false
	}
	// 打开地址（464）公共校验：选填，填了须 http(s):// 且不超长（存 config JSON
	// open_url 键，卡片点击优先于服务地址）
	var commonCfg struct {
		OpenURL string `json:"open_url"`
	}
	if len(req.Config) > 0 && json.Unmarshal(req.Config, &commonCfg) == nil {
		openURL := strings.TrimSpace(commonCfg.OpenURL)
		if openURL != "" && (len(openURL) > 2048 || !(strings.HasPrefix(openURL, "http://") || strings.HasPrefix(openURL, "https://"))) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "打开地址须为 http(s):// 开头"})
			return "", "", false
		}
	}
	// config 按类型校验形状（坏 JSON / 超限实体直接拒绝）
	if err := validateConfig(req.Type, req.Config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return "", "", false
	}
	return name, url, true
}

func validateConfig(typ string, raw json.RawMessage) error {
	cfg := "{}"
	if len(raw) > 0 {
		cfg = string(raw)
	}
	switch typ {
	case store.IntegrationHA:
		var c struct {
			Entities []struct {
				Label  string `json:"label"`
				Entity string `json:"entity"`
				Unit   string `json:"unit"`
			} `json:"entities"`
			Devices []integrations.HADevice `json:"devices"`
		}
		if err := json.Unmarshal([]byte(cfg), &c); err != nil {
			return fmt.Errorf("配置 JSON 无效")
		}
		if len(c.Entities) > maxHAEntities {
			return fmt.Errorf("最多 %d 个实体", maxHAEntities)
		}
		for _, e := range c.Entities {
			if len(e.Label) > 60 || len(e.Entity) > 120 || len(e.Unit) > 30 {
				return fmt.Errorf("实体行的名称/实体 ID/单位超长")
			}
		}
		// 收藏设备（355）：管理抽屉表单不送该键（由 /ha/devices-config 维护），
		// 送了也照校验形状（域白名单在 SaveDevices 已挡），防脏数据入库
		if len(c.Devices) > maxHADevices {
			return fmt.Errorf("最多 %d 台设备", maxHADevices)
		}
		for _, d := range c.Devices {
			if len(d.Entity) > 120 || len(d.Label) > 60 || len(d.IconOn) > 500 || len(d.IconOff) > 500 {
				return fmt.Errorf("设备行的实体 ID/名称/图标地址超长")
			}
		}
	case store.IntegrationQBit, store.IntegrationEmby:
		var c map[string]any
		if json.Unmarshal([]byte(cfg), &c) != nil {
			return fmt.Errorf("配置 JSON 无效")
		}
	default:
		// 436-441 扩容类型：均为自由对象；Docker 额外校验 socket 路径长度
		var c map[string]any
		if json.Unmarshal([]byte(cfg), &c) != nil {
			return fmt.Errorf("配置 JSON 无效")
		}
		if typ == store.IntegrationDocker {
			if s, ok := c["sock"].(string); ok && len(s) > 500 {
				return fmt.Errorf("socket 路径超长")
			}
		}
	}
	return nil
}

// mergeAuth 合并凭据：请求里非空的字段覆盖旧值，空字段沿用（服务器 SSH 密码同款口径，
// 编辑时不回显旧凭据，留空=不改）。create 时不传旧值。
func mergeAuth(old string, req *integrationAuthReq) string {
	cred := integrations.ParseCredentials(old)
	if req != nil {
		if v := strings.TrimSpace(req.Username); v != "" {
			cred.Username = v
		}
		if v := strings.TrimSpace(req.Password); v != "" {
			cred.Password = v
		}
		if v := strings.TrimSpace(req.Token); v != "" {
			cred.Token = v
		}
	}
	return integrations.MarshalCredentials(cred)
}

// checkAuth 必填凭据校验（创建时）。凭据可选的类型（开放部署）
// 不在此列：adguard / pihole / transmission / aria2 / docker。
func checkAuth(typ string, auth string) error {
	cred := integrations.ParseCredentials(auth)
	switch typ {
	case store.IntegrationHA:
		if cred.Token == "" {
			return fmt.Errorf("请填写长效访问令牌")
		}
	case store.IntegrationEmby:
		if cred.Token == "" {
			return fmt.Errorf("请填写 API Key")
		}
	case store.IntegrationQBit:
		if cred.Token == "" && (cred.Username == "" || cred.Password == "") {
			return fmt.Errorf("请填写账号密码（或 ≥5.2 的 API Key）")
		}
	case store.IntegrationDeluge:
		if cred.Password == "" {
			return fmt.Errorf("请填写 Deluge Web UI 密码")
		}
	case store.IntegrationNavidrome:
		if cred.Username == "" || cred.Password == "" {
			return fmt.Errorf("请填写 Navidrome 账号密码")
		}
	case store.IntegrationNextcloud:
		if cred.Username == "" || cred.Password == "" {
			return fmt.Errorf("请填写账号与应用密码")
		}
	case store.IntegrationProxmox:
		if cred.Token == "" && cred.Password == "" {
			return fmt.Errorf("请填写 API Token 或账号密码")
		}
	case store.IntegrationPortainer:
		if cred.Token == "" {
			return fmt.Errorf("请填写 Portainer API Key")
		}
	case store.IntegrationDSM:
		if cred.Username == "" || cred.Password == "" {
			return fmt.Errorf("请填写 DSM 账号密码")
		}
	case store.IntegrationTrueNAS:
		if cred.Token == "" {
			return fmt.Errorf("请填写 TrueNAS API Key")
		}
	}
	return nil
}

// Create 新增集成；成功后立即后台抓一轮，卡片尽快出数据。
func (h *IntegrationHandler) Create(c *gin.Context) {
	var req integrationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	name, url, ok := h.check(c, &req)
	if !ok {
		return
	}
	auth := mergeAuth("", req.Auth)
	if err := checkAuth(req.Type, auth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if n, err := h.st.CountIntegrations(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询集成数失败"})
		return
	} else if n >= maxIntegrations {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多 %d 个服务集成", maxIntegrations)})
		return
	}
	config := configJSON(req.Config)
	id, err := h.st.CreateIntegration(req.Type, name, url, auth, config)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	in, err := h.st.GetIntegration(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询创建结果失败"})
		return
	}
	h.mgr.Kick(*in)
	c.JSON(http.StatusOK, gin.H{"integration": h.view(in)})
}

// Update 编辑集成（凭据留空沿用；类型不可改）。
func (h *IntegrationHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的集成 ID"})
		return
	}
	old, err := h.st.GetIntegration(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "集成不存在"})
		return
	}
	var req integrationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if req.Type == "" {
		req.Type = old.Type
	}
	if req.Type != old.Type {
		c.JSON(http.StatusBadRequest, gin.H{"error": "集成类型不可修改"})
		return
	}
	name, url, ok := h.check(c, &req)
	if !ok {
		return
	}
	enabled := old.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	auth := mergeAuth(old.Auth, req.Auth)
	if err := checkAuth(req.Type, auth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	config := configJSON(req.Config)
	// /ha 页面独占维护的 config 键（473：355 devices + 394 rooms/sensors）由
	// /ha/devices-config 单独保存，服务抽屉表单的 config 不带这些键——编辑集成时
	// 原样保留库内值，避免保存实体/打开地址配置把房间与状态实体清单冲掉；
	// 显式传入的键仍以传入为准（全量保存可删）
	if req.Type == store.IntegrationHA {
		config = preserveHAKeys(old.Config, config)
	}
	if err := h.st.UpdateIntegration(id, name, url, auth, config, enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	in, err := h.st.GetIntegration(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询保存结果失败"})
		return
	}
	h.mgr.Kick(*in)
	c.JSON(http.StatusOK, gin.H{"integration": h.view(in)})
}

// Delete 删除集成（连带清缓存）。
func (h *IntegrationHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的集成 ID"})
		return
	}
	if err := h.st.DeleteIntegration(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	h.mgr.Drop(id)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Order 保存上下移顺序。
func (h *IntegrationHandler) Order(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if len(req.IDs) > maxIntegrations {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量超出上限"})
		return
	}
	if err := h.st.ReorderIntegrations(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存顺序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Test 用表单当前值做连通性测试（不入库；编辑时留空凭据沿用库内值）。
func (h *IntegrationHandler) Test(c *gin.Context) {
	var req integrationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	auth := ""
	if req.ID != nil && *req.ID > 0 {
		if old, err := h.st.GetIntegration(*req.ID); err == nil {
			auth = old.Auth
		}
	}
	auth = mergeAuth(auth, req.Auth)
	if err := checkAuth(req.Type, auth); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := integrations.TestInput(req.Type, strings.TrimSpace(req.URL), auth, configJSON(req.Config)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Data 主页轮询消费：全部集成当前快照（缓存保热，缺时同步补拉）。
func (h *IntegrationHandler) Data(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"integrations": h.mgr.All()})
}

// embyIntegrationID 解析 integration 参数（<=0 回落第一个 emby 类型集成；无则 0）。
func (h *IntegrationHandler) embyIntegrationID(c *gin.Context) int64 {
	id, _ := strconv.ParseInt(c.Query("integration"), 10, 64)
	if id > 0 {
		return id
	}
	return h.st.FirstIntegrationIDByType(store.IntegrationEmby)
}

// EmbyHistory GET /emby/history?integration=1&limit=50&offset=0&user=&type=&from=&to=（528/530）：
// 播放记录分页清单（新→旧）。主页 Emby 服务卡「记录」弹层清单视图消费；
// total 供页码分页，users/types 去重清单供筛选下拉；from/to 为 started_at
// unix 秒区间（左闭右开，缺省/非法不过滤，前端按本地时区换算）。
func (h *IntegrationHandler) EmbyHistory(c *gin.Context) {
	id := h.embyIntegrationID(c)
	if id <= 0 {
		c.JSON(http.StatusOK, gin.H{"events": []store.EmbyEvent{}, "total": 0, "users": []string{}, "types": []string{}})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	events, total, err := h.st.EmbyHistory(store.EmbyHistoryQuery{
		IntegrationID: id,
		User:          strings.TrimSpace(c.Query("user")),
		Type:          strings.TrimSpace(c.Query("type")),
		From:          from,
		To:            to,
		Limit:         limit,
		Offset:        offset,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	users, types, err := h.st.EmbyFilterOptions(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if events == nil {
		events = []store.EmbyEvent{}
	}
	if users == nil {
		users = []string{}
	}
	if types == nil {
		types = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "total": total, "users": users, "types": types})
}

// EmbyStats GET /emby/stats?integration=1&days=30（530）：播放统计报表——按用户
// （次数+时长排行）/按类型（次数）/按日期（次数+时长）+每用户看过的剧影清单
// （items，1：剧集按剧名/电影按片名归并、多次播放合并一条、时间=最后播放）；
// days 缺省 30、0=全部。
func (h *IntegrationHandler) EmbyStats(c *gin.Context) {
	id := h.embyIntegrationID(c)
	if id <= 0 {
		c.JSON(http.StatusOK, gin.H{"users": []store.EmbyUserStat{}, "types": []store.EmbyTypeStat{}, "days": []store.EmbyDayStat{}, "items": []store.EmbyUserItemStat{}})
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days < 0 {
		days = 30
	}
	var since int64
	if days > 0 {
		since = time.Now().Unix() - int64(days)*86400
	}
	users, types, daysRows, err := h.st.EmbyStats(id, since)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 每用户看过的剧影清单（1）：随同范围下发，前端统计视图按用户分组展示
	items, err := h.st.EmbyUserItems(id, since)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if users == nil {
		users = []store.EmbyUserStat{}
	}
	if types == nil {
		types = []store.EmbyTypeStat{}
	}
	if daysRows == nil {
		daysRows = []store.EmbyDayStat{}
	}
	if items == nil {
		items = []store.EmbyUserItemStat{}
	}
	c.JSON(http.StatusOK, gin.H{"users": users, "types": types, "days": daysRows, "items": items})
}

// haOwnedKeys /ha 页面独占维护、服务抽屉 config 不携带的键：devices=收藏设备
// （355）、rooms=房间（394）、sensors=状态实体（368）——PUT /integrations 编辑
// HA 集成时这些键在旧 config 有而新 config 无，一律原样保留（473 报障根因）。
var haOwnedKeys = []string{"devices", "rooms", "sensors"}

// preserveHAKeys 把旧 config 里 /ha 页面维护的键搬到新 config（新 config 显式
// 带了该键则以传入为准——/ha/devices-config 全量保存可正常删除）。
func preserveHAKeys(oldConfig, newConfig string) string {
	var om, nm map[string]json.RawMessage
	_ = json.Unmarshal([]byte(oldConfig), &om)
	_ = json.Unmarshal([]byte(newConfig), &nm)
	if nm == nil {
		nm = map[string]json.RawMessage{}
	}
	for _, k := range haOwnedKeys {
		if _, has := nm[k]; has {
			continue
		}
		if v, ok := om[k]; ok {
			nm[k] = v
		}
	}
	out, err := json.Marshal(nm)
	if err != nil {
		return newConfig
	}
	return string(out)
}

// configJSON RawMessage 落库为字符串（空给 "{}"）。
func configJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
