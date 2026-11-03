package api

// 每服务器专属脚本（SSH 终端页「专属脚本」按钮）：脚本内容存服务端库，
// 终端页新增/编辑/复制/删除。语言仅作前端 Monaco 高亮提示（默认 shell）。

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// maxTermScriptBytes 单个脚本内容上限（脚本都是短文本，1MB 足够且防滥用）。
const maxTermScriptBytes = 1 << 20

// maxTermScriptName 脚本名称上限（字符）。
const maxTermScriptName = 100

// termScriptLangRe 语言标识白名单（Monaco 语言 id 形如 shell/python/yaml/cpp/csharp）。
var termScriptLangRe = regexp.MustCompile(`^[a-z0-9+#-]{1,20}$`)

// errScriptAbort 仅作「校验失败已写好响应」的收尾信号，不代表具体错误。
var errScriptAbort = errors.New("term script validation aborted")

type termScriptReq struct {
	ServerID int64  `json:"server_id"`
	Name     string `json:"name"`
	Lang     string `json:"lang"`
	Content  string `json:"content"`
}

// ListScripts GET /term/scripts?server_id=：该服务器全部脚本（含内容）。
func (h *TermHandler) ListScripts(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	scripts, err := h.st.ListTermScripts(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询脚本失败"})
		return
	}
	if scripts == nil {
		scripts = []store.TermScript{}
	}
	c.JSON(http.StatusOK, gin.H{"scripts": scripts})
}

// CreateScript POST /term/scripts：新建脚本（复制=前端带副本名再走本接口）。
func (h *TermHandler) CreateScript(c *gin.Context) {
	var req termScriptReq
	if err := c.ShouldBindJSON(&req); err != nil || req.ServerID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（server_id 必填）"})
		return
	}
	name, lang, ok := h.cleanTermScript(c, &req)
	if !ok {
		return
	}
	sc, err := h.st.CreateTermScript(req.ServerID, name, lang, req.Content)
	if errors.Is(err, store.ErrTermScriptNameExists) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该服务器已有同名脚本"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存脚本失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "script": sc})
}

// UpdateScript PUT /term/scripts/:id?server_id=：保存编辑（名称/语言/内容整份提交）。
func (h *TermHandler) UpdateScript(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	sid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || sid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的脚本 ID"})
		return
	}
	var req termScriptReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	req.ServerID = id // server_id 以查询参数为准，body 不作数
	name, lang, ok := h.cleanTermScript(c, &req)
	if !ok {
		return
	}
	err = h.st.UpdateTermScript(sid, id, name, lang, req.Content)
	if errors.Is(err, store.ErrTermScriptNameExists) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该服务器已有同名脚本"})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "脚本不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存脚本失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteScript DELETE /term/scripts/:id?server_id=。
func (h *TermHandler) DeleteScript(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	sid, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || sid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的脚本 ID"})
		return
	}
	if err := h.st.DeleteTermScript(sid, id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "脚本不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// termHistoryReq 终端补全历史保存请求（items 新→旧）。
type termHistoryReq struct {
	ServerID int64    `json:"server_id"`
	Items    []string `json:"items" binding:"required"`
}

// GetHistory GET /term/history?server_id=：该服务器补全历史（新→旧）。
func (h *TermHandler) GetHistory(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	items, err := h.st.GetTermHistory(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询历史失败"})
		return
	}
	if items == nil {
		items = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// SaveHistory PUT /term/history：覆盖保存补全历史（服务端裁剪至上限）。
func (h *TermHandler) SaveHistory(c *gin.Context) {
	var req termHistoryReq
	if err := c.ShouldBindJSON(&req); err != nil || req.ServerID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（server_id、items 必填）"})
		return
	}
	if len(req.Items) > store.TermHistoryMax {
		req.Items = req.Items[:store.TermHistoryMax]
	}
	for _, it := range req.Items {
		if len(it) > maxTermScriptBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "单条命令过长"})
			return
		}
	}
	if srv, _ := h.st.GetServer(req.ServerID); srv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "服务器不存在"})
		return
	}
	if err := h.st.SaveTermHistory(req.ServerID, req.Items); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存历史失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// fileFavsReq 文件收藏保存请求（整份覆盖，前端按服务器一份）。
type fileFavsReq struct {
	ServerID int64           `json:"server_id"`
	Items    []store.FileFav `json:"items" binding:"required"`
}

// GetFavs GET /term/favs?server_id=：该服务器收藏列表（跨设备同步）。
func (h *TermHandler) GetFavs(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	items, err := h.st.GetFileFavs(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询收藏失败"})
		return
	}
	if items == nil {
		items = []store.FileFav{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// SaveFavs PUT /term/favs：覆盖保存收藏（服务端裁剪至上限）。
func (h *TermHandler) SaveFavs(c *gin.Context) {
	var req fileFavsReq
	if err := c.ShouldBindJSON(&req); err != nil || req.ServerID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（server_id、items 必填）"})
		return
	}
	if len(req.Items) > store.FileFavsMax {
		req.Items = req.Items[:store.FileFavsMax]
	}
	for _, it := range req.Items {
		if it.Path == "" || len(it.Path) > 4096 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "收藏路径不合法"})
			return
		}
	}
	if srv, _ := h.st.GetServer(req.ServerID); srv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "服务器不存在"})
		return
	}
	if err := h.st.SaveFileFavs(req.ServerID, req.Items); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存收藏失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// cleanTermScript 清洗并校验名称/语言/内容（服务器存在性一并查），
// 返回 (name, lang, true)；校验失败时已写好 4xx 岔响并返回 false。
func (h *TermHandler) cleanTermScript(c *gin.Context, req *termScriptReq) (string, string, bool) {
	if srv, _ := h.st.GetServer(req.ServerID); srv == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "服务器不存在"})
		return "", "", false
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "脚本名称不能为空"})
		return "", "", false
	}
	if utf8.RuneCountInString(name) > maxTermScriptName {
		c.JSON(http.StatusBadRequest, gin.H{"error": "脚本名称过长（≤100 字符）"})
		return "", "", false
	}
	lang := req.Lang
	if lang == "" {
		lang = "shell" // 默认 shell 格式展示
	}
	if !termScriptLangRe.MatchString(lang) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的语言标识"})
		return "", "", false
	}
	if len(req.Content) > maxTermScriptBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "脚本内容超过 1MB"})
		return "", "", false
	}
	return name, lang, true
}
