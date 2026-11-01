package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// DocHandler 处理文档模块端点。
type DocHandler struct {
	st *store.Store
}

// maxDocContent 单篇文档 content 上限（富文本 HTML / Markdown 源码共用）。
const maxDocContent = 20 << 20 // 20MB

// List 返回分组 + 全部文档元数据（不含 content，前端按 group_id 归并）。
func (h *DocHandler) List(c *gin.Context) {
	groups, docs, err := h.st.ListDocTree()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询文档失败"})
		return
	}
	if groups == nil {
		groups = []store.DocGroup{}
	}
	if docs == nil {
		docs = []store.Doc{}
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups, "docs": docs})
}

type createDocGroupReq struct {
	Name string `json:"name" binding:"required,max=50"`
}

// CreateGroup 新建文档分组。
func (h *DocHandler) CreateGroup(c *gin.Context) {
	var req createDocGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分组名称必填（最长 50 字）"})
		return
	}
	g, err := h.st.CreateDocGroup(strings.TrimSpace(req.Name))
	if err != nil {
		if errors.Is(err, store.ErrDocGroupNameExists) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "分组名称已存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": g.ID, "name": g.Name})
}

type updateDocGroupReq struct {
	Name string `json:"name" binding:"required,max=50"`
}

// UpdateGroup 改分组名。
func (h *DocHandler) UpdateGroup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分组 ID"})
		return
	}
	var req updateDocGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分组名称必填（最长 50 字）"})
		return
	}
	if err := h.st.RenameDocGroup(id, strings.TrimSpace(req.Name)); err != nil {
		if errors.Is(err, store.ErrDocGroupNameExists) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "分组名称已存在"})
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "分组不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// DeleteGroup 删除分组及组内全部文档（前端先确认并展示文档数）。
func (h *DocHandler) DeleteGroup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分组 ID"})
		return
	}
	n, err := h.st.DeleteDocGroup(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "分组不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "deleted_docs": n})
}

type createDocReq struct {
	GroupID int64  `json:"group_id"`                                     // 所属分组；0=未分组
	Title   string `json:"title" binding:"required,max=200"`             // 标题
	Format  string `json:"format" binding:"omitempty,oneof=richtext md"` // 内容格式，缺省 richtext
}

// Create 新建文档（内容为空，编辑后保存）。
func (h *DocHandler) Create(c *gin.Context) {
	var req createDocReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题必填（最长 200 字）"})
		return
	}
	if req.Format == "" {
		req.Format = "richtext"
	}
	if req.GroupID > 0 {
		groups, err := h.st.ListDocGroups()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分组失败"})
			return
		}
		found := false
		for _, g := range groups {
			if g.ID == req.GroupID {
				found = true
				break
			}
		}
		if !found {
			c.JSON(http.StatusBadRequest, gin.H{"error": "分组不存在"})
			return
		}
	}
	d, err := h.st.CreateDoc(req.GroupID, strings.TrimSpace(req.Title), req.Format)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": d.ID, "title": d.Title, "format": d.Format, "group_id": d.GroupID})
}

// Get 返回单篇文档（含 content）。
func (h *DocHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文档 ID"})
		return
	}
	d, err := h.st.GetDoc(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询文档失败"})
		return
	}
	if d == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"doc": d})
}

type updateDocReq struct {
	Title         *string `json:"title" binding:"omitempty,max=200"` // 改标题；nil=不改
	Content       *string `json:"content" binding:"omitempty"`       // 改内容；nil=不改（编辑器自动保存；上限 maxDocContent 字节）
	BaseUpdatedAt *int64  `json:"base_updated_at"`                   // 乐观锁基准：带上时与库中 updated_at 比对，不一致返回 409（多窗口防覆盖）
	GroupID       *int64  `json:"group_id"`                          // 改分组；nil=不改（53 编辑器可选分组，0=未分组合法）
}

// Update 保存文档标题/内容（至少带一项），自动保存高频调用。带
// base_updated_at 时做 CAS 校验：文档已在其他窗口被保存过则拒绝（409），
// 由前端决定覆盖还是载入最新版。
func (h *DocHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文档 ID"})
		return
	}
	var req updateDocReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（标题最长 200 字）"})
		return
	}
	if req.Content != nil && len(*req.Content) > maxDocContent {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("文档过大，超出 %dMB", maxDocContent>>20)})
		return
	}
	if req.Title != nil {
		v := strings.TrimSpace(*req.Title)
		if v == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空"})
			return
		}
		req.Title = &v
	}
	base := int64(-1)
	if req.BaseUpdatedAt != nil {
		base = *req.BaseUpdatedAt
	}
	newAt, err := h.st.UpdateDoc(id, req.Title, req.Content, base)
	if errors.Is(err, store.ErrDocConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "文档已在其他窗口被修改，保存被拒绝"})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	// 53 换组（可选）：组存在性校验同 /docs/move（0=未分组合法）；MoveDoc 会刷
	// updated_at，响应回最新值——编辑器拿它回填乐观锁基准，否则下次自动保存必 409
	if req.GroupID != nil {
		if *req.GroupID != 0 {
			gs, err := h.st.ListDocGroups()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分组失败"})
				return
			}
			found := false
			for _, g := range gs {
				if g.ID == *req.GroupID {
					found = true
					break
				}
			}
			if !found {
				c.JSON(http.StatusBadRequest, gin.H{"error": "目标分组不存在"})
				return
			}
		}
		if err := h.st.MoveDoc(id, *req.GroupID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "移动分组失败"})
			return
		}
		if d, err := h.st.GetDoc(id); err == nil && d != nil {
			newAt = d.UpdatedAt
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "updated_at": newAt})
}

// Order 保存分组内文档拖拽后的顺序（ids 按新顺序传入，写入 sort 1..n）。
func (h *DocHandler) Order(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if len(req.IDs) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "数量超出上限"})
		return
	}
	if err := h.st.ReorderDocs(req.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存顺序失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Move 文档移到另一分组（左栏跨分组拖拽）。
func (h *DocHandler) Move(c *gin.Context) {
	var req struct {
		ID      int64 `json:"id" binding:"required"`
		GroupID int64 `json:"group_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if req.GroupID != 0 {
		gs, err := h.st.ListDocGroups()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分组失败"})
			return
		}
		found := false
		for _, g := range gs {
			if g.ID == req.GroupID {
				found = true
				break
			}
		}
		if !found {
			c.JSON(http.StatusBadRequest, gin.H{"error": "目标分组不存在"})
			return
		}
	}
	if err := h.st.MoveDoc(req.ID, req.GroupID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "移动分组失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Delete 删除文档（移入回收站，可恢复）。
func (h *DocHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文档 ID"})
		return
	}
	if err := h.st.SoftDeleteDoc(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---------- 回收站（路由前缀 /docs-trash，避免与 /docs/:id 参数段同层冲突） ----------

// ListTrash 回收站文档列表（不含 content）。
func (h *DocHandler) ListTrash(c *gin.Context) {
	docs, err := h.st.ListTrashDocs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询回收站失败"})
		return
	}
	if docs == nil {
		docs = []store.Doc{}
	}
	c.JSON(http.StatusOK, gin.H{"docs": docs})
}

// RestoreTrash 从回收站恢复文档。
func (h *DocHandler) RestoreTrash(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文档 ID"})
		return
	}
	d, err := h.st.RestoreDoc(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "回收站中不存在该文档"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "恢复失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"doc": d})
}

// DeleteTrash 彻底删除回收站文档（连同历史版本）。
func (h *DocHandler) DeleteTrash(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文档 ID"})
		return
	}
	if err := h.st.HardDeleteDoc(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "回收站中不存在该文档"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// EmptyTrash 清空回收站：全部软删文档连同历史版本彻底删除（前端带数量二次确认）。
func (h *DocHandler) EmptyTrash(c *gin.Context) {
	n, err := h.st.EmptyTrashDocs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "清空失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "deleted": n})
}

// ---------- 历史版本 ----------

// ListVersions 版本列表（元数据 + 大小，不含 content）。
func (h *DocHandler) ListVersions(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文档 ID"})
		return
	}
	versions, err := h.st.ListDocVersions(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询版本失败"})
		return
	}
	if versions == nil {
		versions = []store.DocVersion{}
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

// GetVersion 单个版本详情（含 content，恢复用）。
func (h *DocHandler) GetVersion(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的文档 ID"})
		return
	}
	vid, err := strconv.ParseInt(c.Param("vid"), 10, 64)
	if err != nil || vid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的版本 ID"})
		return
	}
	v, err := h.st.GetDocVersion(id, vid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询版本失败"})
		return
	}
	if v == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "版本不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": v})
}

// ---------- 附件（富文本编辑器上传的图片/视频） ----------

// maxDocAssetBytes 单个附件上限：图片/视频经 BLOB 直存 SQLite，太大既拖
// 库体积也占内存。路由挂 filesGrp（无 gzip 中间件组）：二进制内容压不出
// 收益还烧 CPU。
const maxDocAssetBytes = 20 << 20

// sanitizeAssetMime 附件 MIME 白名单：只有浏览器确认无法执行的类型
// （图片/音频/视频/text/plain/pdf）允许内联输出，其余一律按
// application/octet-stream 附件下载——防 text/html、image/svg+xml 等
// 上传后经 /docs-assets/:id 同源执行脚本（存储型 XSS）。
func sanitizeAssetMime(ctype string) string {
	mt := strings.TrimSpace(strings.ToLower(ctype))
	if i := strings.Index(mt, ";"); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	switch {
	case mt == "image/svg+xml": // SVG 可携带脚本，同源内联=存储型 XSS，强制附件下载
		return "application/octet-stream"
	case strings.HasPrefix(mt, "image/"), strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "audio/"),
		mt == "application/pdf", mt == "text/plain":
		return mt
	}
	return "application/octet-stream"
}

// UploadAsset POST /docs-assets（multipart 单文件 file）：存库后按
// wangeditor 约定返回 {errno:0, data:{url,alt,href}}，前端插入即可展示。
func (h *DocHandler) UploadAsset(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少文件字段 file"})
		return
	}
	if fh.Size > maxDocAssetBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "附件超过 20MB"})
		return
	}
	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取上传内容失败"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxDocAssetBytes+1))
	f.Close()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取上传内容失败"})
		return
	}
	if len(data) > maxDocAssetBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "附件超过 20MB"})
		return
	}
	name := path.Base(strings.ReplaceAll(fh.Filename, "\\", "/"))
	if name == "" || name == "." || name == "/" {
		name = "attachment"
	}
	ctype := sanitizeAssetMime(fh.Header.Get("Content-Type"))
	if ctype == "application/octet-stream" {
		if m := mime.TypeByExtension(path.Ext(name)); m != "" {
			ctype = sanitizeAssetMime(m)
		} else {
			ctype = sanitizeAssetMime(http.DetectContentType(data))
		}
	}
	id, err := h.st.CreateDocAsset(name, ctype, data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存附件失败"})
		return
	}
	url := fmt.Sprintf("/api/v1/docs-assets/%d", id)
	c.JSON(http.StatusOK, gin.H{"errno": 0, "data": gin.H{"url": url, "alt": name, "href": ""}})
}

// GetAsset GET /docs-assets/:id：内联输出附件。内容永不变化，长缓存；
// <img>/video 标签带不了 Authorization 头，靠同源 HttpOnly Cookie 鉴权。
func (h *DocHandler) GetAsset(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的附件 ID"})
		return
	}
	a, data, err := h.st.GetDocAsset(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询附件失败"})
		return
	}
	if a == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "附件不存在"})
		return
	}
	// private：边缘缓存（EdgeOne/ESA 等）不存——缓存 key 不含 Cookie，一旦
	// 边缘缓存此头会被绕过（自增 id 可遍历）；浏览器本地长缓存保留（462）
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	ctype := sanitizeAssetMime(a.Mime)
	if ctype == "application/octet-stream" {
		// 非白名单类型（含存量库里已存的）不内联，强制附件下载
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	}
	c.Data(http.StatusOK, ctype, data)
}
