package api

import (
	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// scopeUID 取当前会话的数据空间（2026-11-03 家庭成员口径：严格个人空间）：
// admin→0（管理员空间），家庭成员→自己的 users.id。store 层各模块查询/写入按该值
// 过滤 user_id 列，两个空间互不可见。
// 取值用安全 Get 而非 MustGet：单测直挂 handler 无 authRequired 中间件时回
// 0（管理员空间），生产行为由 authRequired + familyRequired 保证必有会话用户。
func scopeUID(c *gin.Context) int64 {
	v, ok := c.Get("user")
	u, _ := v.(*store.User)
	if !ok || u == nil {
		return 0
	}
	if u.Role == store.RoleAdmin {
		return 0
	}
	return u.ID
}
