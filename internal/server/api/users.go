package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"lz-monitor/internal/server/store"
)

// UserHandler 用户管理（361 多用户）：管理员在系统设置页维护其他登录账号。
// 2026-11-03 起多用户仅框定家庭成员（role=user 固定开放家庭页面集 +
// 共享 HA，数据按 user_id 隔离），无可配置页面权限。
type UserHandler struct {
	st *store.Store
}

// userView 列表/响应视图：哈希绝不外发；permissions 为家庭成员的后台菜单项
// （2026-11-03，null/空=默认六模块全量）。
type userView struct {
	ID          int64    `json:"id"`
	Username    string   `json:"username"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	Landing     string   `json:"landing"`
	CreatedAt   int64    `json:"created_at"`
}

func userViewOf(u store.User) userView {
	return userView{ID: u.ID, Username: u.Username, Role: u.Role, Permissions: u.Permissions,
		Landing: store.NormalizeLanding(u.Landing), CreatedAt: u.CreatedAt}
}

// List 全部用户（按创建序）。
func (h *UserHandler) List(c *gin.Context) {
	list, err := h.st.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}
	out := make([]userView, 0, len(list))
	for _, u := range list {
		out = append(out, userViewOf(u))
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

type createUserReq struct {
	Username    string   `json:"username" binding:"required,min=1,max=100"`
	Password    string   `json:"password" binding:"required,min=8,max=72"` // 8 位下限；bcrypt 只取前 72 字节
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"` // 页面集（可选，缺省=默认全量）
	Landing     string   `json:"landing"`     // 登录落点（home/ha，缺省=主页优先）
}

// Create 新建用户（缺省角色=家庭成员；显式传 admin 才建管理员）。
func (h *UserHandler) Create(c *gin.Context) {
	var req createUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（用户名必填，密码 8-72 位）"})
		return
	}
	// 角色缺省兜家庭成员（store 层 NormalizeRole 空= admin 的存量口径只服务
	// EnsureAdmin 建号，不走这里）
	role := req.Role
	if role == "" {
		role = store.RoleUser
	}
	id, err := h.st.CreateUser(strings.TrimSpace(req.Username), req.Password, role)
	if err != nil {
		if errors.Is(err, store.ErrUsernameExists) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "用户名已存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建用户失败"})
		return
	}
	if len(req.Permissions) > 0 {
		if err := h.st.UpdateUserPermissions(id, sanitizePerms(req.Permissions)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存页面配置失败"})
			return
		}
	}
	if err := h.st.UpdateUserLanding(id, req.Landing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存登录落点失败"})
		return
	}
	u, err := h.st.UserByID(id)
	if err != nil || u == nil {
		c.JSON(http.StatusOK, gin.H{"code": 0})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": userViewOf(*u)})
}

type updateUserReq struct {
	Role        string   `json:"role"`
	Password    string   `json:"password"`    // 空=不改密码
	Permissions []string `json:"permissions"` // nil=不动；空数组=清成默认口径（全量）
	PermsSet    bool     `json:"perms_set"`   // 前端显式携带权限字段的标记（区分「没传」与「清空」）
	Landing     *string  `json:"landing"`     // nil=不动；home/ha=登录落点（成员配置必带）
}

// Update 改角色/重置密码。降级最后一个管理员直接拒绝（必须先提别人或先删建）。
// 角色变更即时生效（会话每请求现读用户行）；重置密码踢掉该用户全部其他会话。
func (h *UserHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的用户 ID"})
		return
	}
	target, err := h.st.UserByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}
	if target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}
	var req updateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	// 角色仅显式携带时才改（空串不动，防 NormalizeRole 把空兜成 admin）
	if req.Role != "" {
		newRole := store.NormalizeRole(req.Role)
		if newRole != target.Role {
			if target.Role == store.RoleAdmin {
				if n, err := h.st.CountAdmins(); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "统计管理员数失败"})
					return
				} else if n <= 1 {
					c.JSON(http.StatusBadRequest, gin.H{"error": "至少保留一个管理员"})
					return
				}
			}
			if err := h.st.UpdateUserRole(id, newRole); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "更新角色失败"})
				return
			}
		}
	}
	if req.Password != "" {
		// 与 Create 对齐 8~72（bcrypt 只取前 72 字节，超长静默截断）
		if len(req.Password) < 8 || len(req.Password) > 72 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "密码至少 8 位，最长 72 位"})
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "生成密码哈希失败"})
			return
		}
		if err := h.st.UpdatePassword(id, string(hash)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新密码失败"})
			return
		}
		// 密码被管理员重置：该用户其他端全部踢下线（当前管理员会话不受影响）
		if err := h.st.DeleteOtherSessions(id, ""); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "清理该用户会话失败"})
			return
		}
	}
	// 后台菜单项：PermsSet 显式标记才动（区分「没传」与「清空成默认」）；
	// 升为 admin 的场景不必配（admin 忽略该列）
	if req.PermsSet {
		if err := h.st.UpdateUserPermissions(id, sanitizePerms(req.Permissions)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存页面配置失败"})
			return
		}
	}
	if req.Landing != nil {
		if err := h.st.UpdateUserLanding(id, *req.Landing); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存登录落点失败"})
			return
		}
	}
	if u, err := h.st.UserByID(id); err == nil && u != nil {
		c.JSON(http.StatusOK, gin.H{"user": userViewOf(*u)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// sanitizePerms 后台菜单项 key 白名单过滤（未知 key 丢弃，前端/手放都防一层）。
func sanitizePerms(in []string) []string {
	ok := map[string]bool{}
	for _, p := range allPerms {
		ok[p] = true
	}
	out := make([]string, 0, len(in))
	for _, p := range in {
		if ok[p] {
			out = append(out, p)
		}
	}
	return out
}

// Delete 删除用户：不能删自己；最后一个管理员不可删。连带清掉其全部会话。
func (h *UserHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的用户 ID"})
		return
	}
	uAny, _ := c.Get("user")
	me, _ := uAny.(*store.User)
	if me != nil && me.ID == id {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能删除当前登录账号"})
		return
	}
	target, err := h.st.UserByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}
	if target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}
	if target.Role == store.RoleAdmin {
		if n, err := h.st.CountAdmins(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "统计管理员数失败"})
			return
		} else if n <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少保留一个管理员"})
			return
		}
	}
	if err := h.st.DeleteUser(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除用户失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// adminRequired 管理员门槛（361）：authRequired 之后挂；受限用户一律 403。
func adminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		uAny, _ := c.Get("user")
		u, _ := uAny.(*store.User)
		if u == nil || u.Role != store.RoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "无权限访问"})
			return
		}
		c.Next()
	}
}
