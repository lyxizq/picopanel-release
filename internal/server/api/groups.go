package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// GroupHandler 处理服务器分组端点（Dashboard 手风琴展示用）。
type GroupHandler struct {
	st *store.Store
}

// List 返回全部分组。
func (h *GroupHandler) List(c *gin.Context) {
	groups, err := h.st.ListGroups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询分组失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

type createGroupReq struct {
	Name string `json:"name" binding:"required,max=50"`
}

// Create 新建分组。
func (h *GroupHandler) Create(c *gin.Context) {
	var req createGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分组名称必填（最长 50 字）"})
		return
	}
	g, err := h.st.CreateGroup(strings.TrimSpace(req.Name))
	if err != nil {
		if errors.Is(err, store.ErrGroupNameExists) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "分组名称已存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": g.ID, "name": g.Name})
}

type updateGroupReq struct {
	Name string `json:"name" binding:"required,max=50"`
}

// Update 改分组名。
func (h *GroupHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分组 ID"})
		return
	}
	var req updateGroupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分组名称必填（最长 50 字）"})
		return
	}
	if err := h.st.RenameGroup(id, strings.TrimSpace(req.Name)); err != nil {
		if errors.Is(err, store.ErrGroupNameExists) {
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

// Delete 删除分组：组内服务器自动移回未分组（服务器本身不受影响）。
func (h *GroupHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的分组 ID"})
		return
	}
	if err := h.st.DeleteGroup(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "分组不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}
