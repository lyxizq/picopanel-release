package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webcheck"
)

// WebCheckHandler 网站检查接口（服务监控页新模块，登录组）：目标 CRUD、未入库
// 测试（先测后存）、单目标同步检测、全部异步检测、拖拽排序。状态由
// webcheck.Manager 每 2 小时轮询维护（0:00–6:00 静默）。
type WebCheckHandler struct {
	st *store.Store
	mg *webcheck.Manager
}

// webCheckReq 目标增改请求。Rules 传规则组（[][]string：规则内「且」、规则间
// 「或」），入库 JSON 序列化。
type webCheckReq struct {
	Name    string     `json:"name"`
	URL     string     `json:"url"`
	Rules   [][]string `json:"rules"`
	Enabled *bool      `json:"enabled"`
}

// buildWebCheck 请求 → 目标行：URL 规整（无 scheme 补 http:// 等，见
// ParseCheckURL）、规则清洗（逐关键字去空白，规则内 ≤20 个关键字、单关键字
// ≤100 字、规则 ≤10 条；空关键字/空规则丢弃）。返回行与是否应重置检测态由
// 调用方按旧值比对。
func buildWebCheck(req *webCheckReq) (*store.WebCheck, error) {
	u, err := webcheck.ParseCheckURL(req.URL)
	if err != nil {
		return nil, err
	}
	var rules [][]string
	for _, rule := range req.Rules {
		var kws []string
		for _, k := range rule {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if runes := []rune(k); len(runes) > store.MaxWebKeywordLen {
				return nil, errWebKeywordLong
			}
			kws = append(kws, k)
		}
		if len(kws) == 0 {
			continue // 空规则丢弃
		}
		if len(kws) > store.MaxWebKeywords {
			return nil, errWebKeywordMany
		}
		rules = append(rules, kws)
	}
	if len(rules) == 0 {
		return nil, errWebRuleEmpty
	}
	if len(rules) > store.MaxWebRules {
		return nil, errWebRuleMany
	}
	name := strings.TrimSpace(req.Name)
	if runes := []rune(name); len(runes) > 64 {
		name = string(runes[:64])
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	b, err := json.Marshal(rules)
	if err != nil {
		return nil, err
	}
	return &store.WebCheck{Name: name, URL: u, Rules: string(b), Enabled: enabled}, nil
}

var (
	errWebRuleEmpty   = &webErr{"至少配置一条规则（每条规则至少一个关键字）"}
	errWebRuleMany    = &webErr{"规则最多 10 条"}
	errWebKeywordMany = &webErr{"单条规则关键字最多 20 个"}
	errWebKeywordLong = &webErr{"单个关键字最长 100 字"}
)

type webErr struct{ msg string }

func (e *webErr) Error() string { return e.msg }

func (h *WebCheckHandler) view(w *store.WebCheck) gin.H {
	return gin.H{"id": w.ID, "name": w.Name, "url": w.URL, "rules": w.RuleList(),
		"enabled": w.Enabled, "up": w.Up, "last_error": w.LastError, "checked_at": w.CheckedAt,
		"last_ok_at": w.LastOKAt, "since": w.Since}
}

// list 全部监控目标（按拖拽序）。
func (h *WebCheckHandler) list(c *gin.Context) {
	rows, err := h.st.ListWebChecks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询网站检查目标失败"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, h.view(&rows[i]))
	}
	c.JSON(http.StatusOK, gin.H{"checks": out, "max": store.MaxWebChecks})
}

// create 新增监控目标：查重 → 建行 → 同步首检一次（失败不拦入库，错误落
// last_error 前端可见）。
func (h *WebCheckHandler) create(c *gin.Context) {
	var req webCheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	w, err := buildWebCheck(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 查重只把「查到了行」当重复（2026-11-03 修）：查询故障与不存在是两回事，
	// 此前 err!=nil 一律当无重复放行，DB 故障时最终撞 UNIQUE 回 500 而非 400 提示
	if _, err := h.st.GetWebCheckByUrl(w.URL); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": store.ErrWebCheckExists.Error()})
		return
	}
	if n, err := h.st.CountWebChecks(); err == nil && n >= store.MaxWebChecks {
		c.JSON(http.StatusBadRequest, gin.H{"error": "最多 16 个网站检查目标"})
		return
	}
	id, err := h.st.CreateWebCheck(w)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	fresh, _ := h.mg.CheckNow(id) // 首次状态即刻入库，不用等轮询
	if fresh == nil {
		fresh, _ = h.st.GetWebCheck(id)
	}
	if fresh == nil { // 双查皆空（DB 故障/并发删行）不带着 nil 进 view（防 panic）
		c.JSON(http.StatusInternalServerError, gin.H{"error": "检测失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "id": id, "check": h.view(fresh)})
}

// update 编辑目标：地址/关键字任一变更即重置检测态并立即重检一次（store 层重置，
// 旧结论对新口径有误导）；只改名称/启停保留状态。
func (h *WebCheckHandler) update(c *gin.Context) {
	id := webCheckID(c)
	if id == nil {
		return
	}
	old, err := h.st.GetWebCheck(*id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "网站检查目标不存在"})
		return
	}
	var req webCheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	w, err := buildWebCheck(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if w.URL != old.URL {
		if dup, err := h.st.GetWebCheckByUrl(w.URL); err == nil && dup.ID != old.ID {
			c.JSON(http.StatusBadRequest, gin.H{"error": store.ErrWebCheckExists.Error()})
			return
		}
	}
	w.ID = old.ID
	reset := w.URL != old.URL || w.Rules != old.Rules
	if err := h.st.UpdateWebCheck(w, reset); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	fresh, _ := h.mg.CheckNow(old.ID)
	if fresh == nil {
		fresh, _ = h.st.GetWebCheck(old.ID)
	}
	if fresh == nil { // 双查皆空不带着 nil 进 view（防 panic，同 create）
		c.JSON(http.StatusInternalServerError, gin.H{"error": "检测失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "check": h.view(fresh)})
}

// remove 删除监控目标。
func (h *WebCheckHandler) remove(c *gin.Context) {
	id := webCheckID(c)
	if id == nil {
		return
	}
	if _, err := h.st.GetWebCheck(*id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "网站检查目标不存在"})
		return
	}
	if err := h.st.DeleteWebCheck(*id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	h.mg.Drop(*id)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// check 手动检测单目标（同步；通过与失败都算成功应答，失败原因看 last_error；
// 静默时段手动检测照常执行）。
func (h *WebCheckHandler) check(c *gin.Context) {
	id := webCheckID(c)
	if id == nil {
		return
	}
	if _, err := h.st.GetWebCheck(*id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "网站检查目标不存在"})
		return
	}
	fresh, err := h.mg.CheckNow(*id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "检测失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "check": h.view(fresh)})
}

// order 拖拽排序（静态段先注册）。
func (h *WebCheckHandler) order(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.ReorderWebChecks(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存排序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// refreshAll 全部启用目标立即重查（后台异步，前端靠轮询取结果）。
func (h *WebCheckHandler) refreshAll(c *gin.Context) {
	h.mg.KickAll()
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// test 未入库目标的检查测试（新增/编辑弹窗「先测后存」用，不落库）：返回
// 通过与否 + 命中规则序号 + 逐规则缺失清单（与规则序对齐）。
func (h *WebCheckHandler) test(c *gin.Context) {
	var req webCheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	w, err := buildWebCheck(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res := h.mg.CheckURL(w.URL, w.RuleList())
	out := gin.H{"ok": res.OK, "url": w.URL}
	if res.OK {
		out["matched_rule"] = res.MatchedRule
		out["missing"] = [][]string{}
	} else {
		out["error"] = res.ErrMsg
		if res.RuleMissing == nil {
			res.RuleMissing = [][]string{}
		}
		out["missing"] = res.RuleMissing
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "result": out})
}

func webCheckID(c *gin.Context) *int64 {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return nil
	}
	return &id
}
