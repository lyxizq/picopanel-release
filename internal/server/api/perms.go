package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// 家庭成员口径（2026-11-03 用户指定）：多用户仅框定家庭成员——**主页与智能
// 家居也按成员配置**（连同后台菜单六模块，一并由管理员勾选；登录落点另配
// landing）；各模块数据按 user_id 严格个人空间隔离（互不可见）。
//
//	home 主页  ha 智能家居（使用端点；配置收归管理员）
//	docs 文档  reminders 提醒事项（含会员到期）  meds 用药提醒
//	mail 邮件  messages 消息中心（含浏览器推送订阅）  toolbox 工具箱
//
// 监控总览/终端/文件/数据库/PT/服务监控/追剧/服务器管理/系统设置/用户管理/
// HA 配置/日历/告警历史等**全部管理员专属**：不在下表 = 受限用户一律 403
// （deny by default，新增端点忘了标注也不会裸奔）。前端 ROUTE_PERMS
// （web/src/utils/perms.js）与此对齐。
const (
	PermHome      = "home"
	PermHA        = "ha"
	PermDocs      = "docs"
	PermReminders = "reminders"
	PermMeds      = "meds"
	PermMail      = "mail"
	PermMessages  = "messages"
	PermToolbox   = "toolbox"
)

// allPerms 页面集候选（用户管理弹层候选；sanitizePerms 白名单）。
var allPerms = []string{PermHome, PermHA, PermDocs, PermReminders, PermMeds, PermMail, PermMessages, PermToolbox}

// familyRoutes API 路径前缀（gin FullPath，含 /api/v1）→ 后台模块 key；key 为
// 空串 = 成员恒放行（主页域，不随菜单配置）。method 非空时还须匹配请求方法。
// 未列入的路径对受限用户一律 403（deny by default）。
var familyRoutes = []struct {
	method string
	prefix string
	key    string
}{
	// 主页（按成员配置）：壳配置读、天气、自配标题、自己的链接分组/倒数日、
	// Emby 播放记录/统计（主页弹层）
	{"GET", "/api/v1/page/config", PermHome},
	{"PUT", "/api/v1/page/title", PermHome}, // 成员自配主页标题（按空间隔离，不读管理员的）
	{"GET", "/api/v1/page/weather", PermHome},
	{"", "/api/v1/page/groups", PermHome},
	{"", "/api/v1/page/links", PermHome},
	{"", "/api/v1/countdowns", PermHome},
	{"GET", "/api/v1/emby/", PermHome},
	// 智能家居（按成员配置）：使用端点（2026-11-03 自 openGrp 移入；HA 配置端点不进表=admin）
	{"GET", "/api/v1/ha/sources", PermHA},
	{"GET", "/api/v1/ha/devices", PermHA},
	{"POST", "/api/v1/ha/call", PermHA},
	{"GET", "/api/v1/ha/entity/", PermHA},
	// 后台菜单项（管理员可按成员配置）
	{"", "/api/v1/docs", PermDocs},           // 前缀同时覆盖 /docs-assets 与 /docs-trash
	{"", "/api/v1/reminders", PermReminders}, // 前缀覆盖 /reminders-trash
	{"", "/api/v1/reminder-groups", PermReminders},
	{"", "/api/v1/memberships", PermReminders},
	{"", "/api/v1/mail", PermMail},         // 账户/签名/附件下载是个人数据；通讯录为全家共享簿——刻意取舍
	{"", "/api/v1/messages", PermMessages}, // 个人消息；/alerts 告警历史属监控域，不进表
	{"", "/api/v1/webpush/", PermMessages},
	{"", "/api/v1/net-check", PermToolbox},
	{"", "/api/v1/med-persons", PermMeds}, // 受限用户自动绑定自己的家庭成员，见 api/medications.go
	{"", "/api/v1/medications", PermMeds},
	{"", "/api/v1/meds/", PermMeds},
}

// userAllowed 受限用户是否开放某页面：permissions 空=nil=默认全量八键
// （存量友好，升级后主页/HA 照旧可用）；配置过则严格按勾选集。
func userAllowed(u *store.User, key string) bool {
	if key == "" {
		return true
	}
	if len(u.Permissions) == 0 {
		return true // 默认口径：后台模块全量
	}
	for _, p := range u.Permissions {
		if p == key {
			return true
		}
	}
	return false
}

// familyRequired 家庭成员闸：authRequired 之后挂。admin 全过；受限用户按请求
// 路由（gin FullPath，防 query/编码绕过）查 familyRoutes 前缀映射（method 非空
// 时须同方法），命中且模块已开放才放行（数据可见性由各模块的 user_id 隔离保证），
// 未命中 403。
func familyRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		uAny, _ := c.Get("user")
		u, _ := uAny.(*store.User)
		if u == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "无权限访问"})
			return
		}
		if u.Role == store.RoleAdmin {
			c.Next()
			return
		}
		fp := c.FullPath()
		for _, r := range familyRoutes {
			if r.method != "" && r.method != c.Request.Method {
				continue
			}
			if len(fp) >= len(r.prefix) && fp[:len(r.prefix)] == r.prefix {
				if userAllowed(u, r.key) {
					c.Next()
					return
				}
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "无权限访问"})
				return
			}
		}
		// 未进表的端点对受限用户一律拒绝（监控等管理员专属/新端点未标注）
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "无权限访问"})
	}
}
