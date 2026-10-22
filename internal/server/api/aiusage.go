package api

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/aiusage"
	"lz-monitor/internal/server/store"
)

// AiUsageHandler 用量查询接口（356/393，登录组）：账户 CRUD/测试/排序、
// 手动刷新（单账户/全部）。用量快照由 Manager 轮询维护（20 分钟），页面读库直出。
type AiUsageHandler struct {
	st *store.Store
	mg *aiusage.Manager
}

// NewAiUsageHandler 构造。
func NewAiUsageHandler(st *store.Store, mg *aiusage.Manager) *AiUsageHandler {
	return &AiUsageHandler{st: st, mg: mg}
}

// aiAccountReq 账户增改请求（api_key / secret_id+secret_key 空=编辑沿用原值，
// 均不回显）。edgeone 用腾讯云密钥对，zhipu/deepseek 用 api_key。
type aiAccountReq struct {
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key"`
	SecretID  string `json:"secret_id"`
	SecretKey string `json:"secret_key"`
	ZoneIDs   string `json:"zone_ids"`
}

func (h *AiUsageHandler) view(a *store.AiAccount) gin.H {
	snap, _ := aiusage.DecodeSnapshot(a.UsageJSON)
	var usage any
	if snap != nil {
		usage = snap
	}
	return gin.H{"id": a.ID, "name": a.Name, "provider": a.Provider,
		"base_url": a.BaseURL, "has_key": a.HasKey, "zone_ids": a.ZoneIDs,
		"usage": usage, "usage_at": a.UsageAt,
		"last_ok_at": a.LastOKAt, "last_error": a.LastError}
}

func (h *AiUsageHandler) presets(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"presets": store.AiProviderPresets()})
}

func (h *AiUsageHandler) accounts(c *gin.Context) {
	list, err := h.st.ListAiAccountMetas()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用量账户失败"})
		return
	}
	out := make([]gin.H, 0, len(list))
	for i := range list {
		out = append(out, h.view(&list[i]))
	}
	c.JSON(http.StatusOK, gin.H{"accounts": out, "max": store.MaxAIAccounts})
}

// buildAiAccount 请求 → 账户（服务商白名单；base_url 空=预设默认站点）。
func buildAiAccount(req *aiAccountReq) (*store.AiAccount, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.BaseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	p := store.AiProviderByKey(req.Provider)
	if p == nil {
		return nil, fmt.Errorf("不支持的服务商（当前支持智谱 GLM、DeepSeek、腾讯 EdgeOne）")
	}
	if req.Name == "" {
		req.Name = p.Label
	}
	if req.BaseURL != "" && !strings.HasPrefix(req.BaseURL, "http://") && !strings.HasPrefix(req.BaseURL, "https://") {
		return nil, fmt.Errorf("URL 地址需以 http:// 或 https:// 开头")
	}
	// 跨商主机混填拦截（2026-10-04 用户实测把 deepseek 地址填进智谱账户）：
	// 各家官方主机写死判断，比前缀剥除更早、更明确地指出填串了
	if p.Key == "zhipu" && strings.Contains(req.BaseURL, "deepseek.com") {
		return nil, fmt.Errorf("智谱账户的 URL 填了 DeepSeek 地址——请留空（默认 open.bigmodel.cn）")
	}
	if p.Key == "deepseek" && strings.Contains(req.BaseURL, "bigmodel.cn") {
		return nil, fmt.Errorf("DeepSeek 账户的 URL 填了智谱地址——请留空（默认 api.deepseek.com）")
	}
	a := &store.AiAccount{Name: req.Name, Provider: p.Key, BaseURL: req.BaseURL}
	if p.Key == "edgeone" {
		// 端点只能是腾讯云 teo 的两个官方主机（国际站预设/国内站手改），
		// 防止把控制台地址或别家域名填进来浪费一轮排障；IP/localhost 放行
		// （测试假上游/内网代理）
		host := strings.TrimPrefix(strings.TrimPrefix(req.BaseURL, "https://"), "http://")
		if i := strings.Index(host, ":"); i >= 0 {
			host = host[:i]
		}
		if req.BaseURL != "" && host != "teo.tencentcloudapi.com" && host != "teo.intl.tencentcloudapi.com" &&
			net.ParseIP(host) == nil && host != "localhost" {
			return nil, fmt.Errorf("EdgeOne 的 URL 需为 https://teo.intl.tencentcloudapi.com（国际站默认）或 https://teo.tencentcloudapi.com（国内站）")
		}
		a.SecretID = strings.TrimSpace(req.SecretID)
		a.SecretKey = strings.TrimSpace(req.SecretKey)
		a.ZoneIDs = strings.Join(aiusage.ParseZoneIDs(req.ZoneIDs), ",")
		if len(aiusage.ParseZoneIDs(req.ZoneIDs)) > 100 {
			return nil, fmt.Errorf("站点 ID 最多 100 个")
		}
	}
	return a, nil
}

func (h *AiUsageHandler) create(c *gin.Context) {
	var req aiAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if n, _ := h.st.CountAiAccounts(); n >= store.MaxAIAccounts {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("最多 %d 个用量账户", store.MaxAIAccounts)})
		return
	}
	// 先测后存：拉不到用量不给存（省得存下一把死 Key）。凭据按服务商各判各的
	if p := store.AiProviderByKey(req.Provider); p != nil && p.Key == "edgeone" {
		if strings.TrimSpace(req.SecretID) == "" || strings.TrimSpace(req.SecretKey) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请填写 SecretId 和 SecretKey"})
			return
		}
	} else if strings.TrimSpace(req.APIKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写 API Key"})
		return
	}
	a, err := buildAiAccount(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a.APIKey = strings.TrimSpace(req.APIKey)
	// 先测后存：拉不到用量不给存（省得存下一把死 Key）
	if err := aiusage.Test(*a); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "用量查询测试失败: " + err.Error()})
		return
	}
	id, err := h.st.CreateAiAccount(a)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	snap, _ := h.mg.RefreshNow(id) // 首份快照即刻入库，卡片不用等轮询
	c.JSON(http.StatusOK, gin.H{"code": 0, "id": id, "usage": snap})
}

func (h *AiUsageHandler) account(c *gin.Context) *store.AiAccount {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return nil
	}
	a, err := h.st.GetAiAccount(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用量账户不存在"})
		return nil
	}
	return a
}

func (h *AiUsageHandler) update(c *gin.Context) {
	var req aiAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	a, err := buildAiAccount(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a.ID, err = strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || a.ID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	old, err := h.st.GetAiAccount(a.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用量账户不存在"})
		return
	}
	if key := strings.TrimSpace(req.APIKey); key != "" {
		a.APIKey = key // 新 Key 先测再存
		if err := aiusage.Test(*a); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "用量查询测试失败: " + err.Error()})
			return
		}
	} else if half := strings.TrimSpace(req.SecretKey); a.Provider == "edgeone" && (half != "" || strings.TrimSpace(req.SecretID) != "") {
		// EdgeOne 换密钥对：给了哪半就用哪半、缺半沿用旧值，凑齐一对再测
		try := *a
		if try.SecretKey == "" {
			try.SecretKey = old.SecretKey
		}
		if try.SecretID == "" {
			try.SecretID = old.SecretID
		}
		if err := aiusage.Test(try); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "用量查询测试失败: " + err.Error()})
			return
		}
	} else {
		a.APIKey = old.APIKey
	}
	if err := h.st.UpdateAiAccount(a); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	snap, _ := h.mg.RefreshNow(a.ID)
	c.JSON(http.StatusOK, gin.H{"code": 0, "usage": snap})
}

func (h *AiUsageHandler) remove(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	if err := h.st.DeleteAiAccount(a.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	h.mg.Drop(a.ID)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// aiTestBody 测试请求（编辑态可另给凭据试连，不落库）：api_key 给 zhipu/deepseek
// 换 Key 试，secret_id/secret_key 给 edgeone 换密钥对试（缺半沿用已存值）。
type aiTestBody struct {
	APIKey    string `json:"api_key"`
	SecretID  string `json:"secret_id"`
	SecretKey string `json:"secret_key"`
}

func (h *AiUsageHandler) test(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	var body aiTestBody
	_ = c.ShouldBindJSON(&body)
	if key := strings.TrimSpace(body.APIKey); key != "" {
		try := *a
		try.APIKey = key
		a = &try
	}
	if a.Provider == "edgeone" && (strings.TrimSpace(body.SecretKey) != "" || strings.TrimSpace(body.SecretID) != "") {
		try := *a
		if v := strings.TrimSpace(body.SecretID); v != "" {
			try.SecretID = v
		}
		if v := strings.TrimSpace(body.SecretKey); v != "" {
			try.SecretKey = v
		}
		if try.SecretID == "" || try.SecretKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "SecretId 与 SecretKey 需成对（缺省半边沿用已存值）"})
			return
		}
		a = &try
	}
	if err := aiusage.Test(*a); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	_ = h.st.RecordAiAccountStatus(a.ID, true, "")
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// refresh 手动刷新单账户（立即拉一次，返回整份账户视图供页面原位替换）。
func (h *AiUsageHandler) refresh(c *gin.Context) {
	a := h.account(c)
	if a == nil {
		return
	}
	if _, err := h.mg.RefreshNow(a.ID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	fresh, err := h.st.GetAiAccount(a.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取刷新结果失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "account": h.view(fresh)})
}

// refreshAll 手动刷新全部账户。
func (h *AiUsageHandler) refreshAll(c *gin.Context) {
	h.mg.RefreshAll()
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
