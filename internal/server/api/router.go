// Package api 提供 Web/管理 REST API 与前端静态资源托管。
package api

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/aiusage"
	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/dbtool"
	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/ghwatch"
	"lz-monitor/internal/server/integrations"
	"lz-monitor/internal/server/mail"
	"lz-monitor/internal/server/pt"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/svcmon"
	"lz-monitor/internal/server/term"
	"lz-monitor/internal/server/ui"
	"lz-monitor/internal/server/updmgr"
)

// noStoreMiddleware 给所有响应默认打 Cache-Control: no-store（462）。中间件
// 先写，handler 有意可缓存的随后用自己的 c.Header 覆盖（Gin Header 是 Set
// 语义）。
func noStoreMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

// Register 装配全部路由。version/startupAt 供自监控页展示（构建版本与启动时刻）。
// intMgr 为服务集成缓存管理器、mailMgr 为邮件未读轮询管理器、aiMgr 为 AI 用量
// 轮询管理器、dbMgr 为数据库管理工具连接池管理器、svcMgr 为服务监控拨测
// 管理器、updMgr 为在线更新管理器、ghMgr 为 GitHub 发布轮询管理器（server.go
// 装配；intMgr/mailMgr/aiMgr/svcMgr/ghMgr 常驻轮询，dbMgr 惰性建池，
// updMgr 按配置周期检查）。
func Register(r *gin.Engine, st *store.Store, eng *engine.Engine, cfg *config.Config, version string, termMgr *term.Manager, intMgr *integrations.Manager, mailMgr *mail.Manager, aiMgr *aiusage.Manager, dbMgr *dbtool.Manager, svcMgr *svcmon.Manager, ptMgr *pt.Manager, updMgr *updmgr.Manager, ghMgr *ghwatch.Manager) error {
	startupAt := time.Now()
	// 真实客户端 IP 请求头（边缘代理部署用，如阿里 ESA 托管转换注入的
	// Ali-Real-Client-Ip）：登录日志/登录限流/Web 白名单的来源 IP 优先取它
	if cfg.RealIPHeader != "" {
		configureRealIP(cfg.RealIPHeader, cfg.TrustedProxies)
		if len(cfg.TrustedProxies) == 0 {
			log.Printf("真实 IP 请求头 %s 已配置但未配 trusted_proxies：该头一律不采信（直连可伪造，fail-closed）；边缘代理部署须把回源网段加进 trusted_proxies 才生效", cfg.RealIPHeader)
		} else {
			log.Printf("真实 IP 请求头 %s 已启用，仅采信 %d 个可信代理网段的对端", cfg.RealIPHeader, len(cfg.TrustedProxies))
		}
	}
	// 基础安全响应头：nosniff 防 MIME 嗅探，SAMEORIGIN 禁外站 iframe 嵌面板
	// （防点击劫持），Referrer-Policy 收敛外链带出的来源信息
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	})
	// 一切响应默认禁缓存（462，边缘缓存兜底）：生产在 EdgeOne/ESA 这类边缘
	// 节点后，业务接口（文档正文/待办/消息等）不发缓存头时边缘走自家默认
	// 策略，控制台规则一改就会误缓存跨用户数据——显式 no-store 不留口子；
	// 有意可缓存的 handler 用自己的 c.Header 覆盖（文档附件 private 长缓存、
	// 静态资源 immutable、index.html no-cache）
	r.Use(noStoreMiddleware())
	// 会话 Cookie Secure 属性（HTTPS/反代 TLS 部署在 yaml 打开）
	cookieSecure = cfg.CookieSecure
	// Web 访问白名单（web_allow 非空时）：来源 IP 命中 IP/CIDR 条目、或访问
	// 域名（Host 头）命中域名条目才放行，其余 403（含登录与静态页面），仅
	// agent 上报接口例外。须在全部路由注册前 Use。
	if len(cfg.WebAllow) > 0 {
		al := newWebAllowList(cfg.WebAllow)
		log.Printf("web_allow 白名单已启用：%d 个 IP/CIDR 条目，%d 个域名条目",
			len(al.static), len(al.names))
		r.Use(webAllowMiddleware(al))
	}
	// 登录无需 token（内置失败限流）
	authH := &AuthHandler{st: st, guard: newLoginGuard()}
	r.POST("/api/v1/auth/login", authH.Login)

	// agent 主动上报（push 模式）：X-Agent-Token 自带身份，不走登录会话
	agentH := &AgentReportHandler{st: st, eng: eng, updMgr: updMgr}
	r.POST("/api/v1/agent/report", agentH.Report)
	r.POST("/api/v1/agent/task-result", agentH.TaskResult)          // PT 中转抓取结果回传（X-Agent-Token）
	r.POST("/api/v1/agent/net-task-result", agentH.NetTaskResult)   // 通用中转任务结果回传（548，X-Agent-Token）
	r.POST("/api/v1/agent/bili-task-result", agentH.BiliTaskResult) // B站追更任务结果回传（X-Agent-Token）
	r.POST("/api/v1/agent/app-task-result", agentH.AppTaskResult)   // 容器/虚拟机操作结果回传（54，X-Agent-Token）
	r.POST("/api/v1/agent/video-dl-result", agentH.VideoDlResult)     // 视频下载任务结果回传（TODO 4，X-Agent-Token）
	r.POST("/api/v1/agent/video-dl-progress", agentH.VideoDlProgress) // 视频下载进度心跳回传（TODO 4，X-Agent-Token）
	r.GET("/api/v1/agent/upgrade-bundle", agentH.UpgradeBundle)       // 升级分发束回源下载（396，X-Agent-Token）

	// Web 终端 WebSocket：组外注册（gzip/JSON 中间件不适配长连接），
	// 鉴权在连上后的首帧 auth 内完成（浏览器 WS 带不了 Authorization 头）
	termH := &TermHandler{term: termMgr, st: st}
	r.GET("/api/v1/term/ws", termH.WS)

	// SFTP 文件管理：只挂 authRequired 的子组——gzip 会让下载被实时压缩
	//（压缩包/媒体文件本已压缩，白烧 CPU），上传的 multipart 也不该被解压干扰
	docH := &DocHandler{st: st}
	filesGrp := r.Group("/api/v1", authRequired(st), adminRequired()) // 361：终端/文件属管理员能力
	filesGrp.GET("/term/files", termH.ListFiles)
	filesGrp.POST("/term/files/upload", termH.Upload)
	filesGrp.GET("/term/files/download", termH.Download)
	filesGrp.GET("/term/files/archive", termH.ArchiveDownload)
	filesGrp.POST("/term/files/op", termH.FileOp)
	filesGrp.POST("/term/files/task", termH.StartTask)
	filesGrp.GET("/term/files/task", termH.TaskStatus)
	filesGrp.POST("/term/files/unarchive-check", termH.UnarchiveCheck)
	filesGrp.POST("/term/files/search", termH.SearchFiles)
	filesGrp.POST("/term/files/find", termH.FindFiles)
	filesGrp.POST("/term/files/task-cancel", termH.CancelTask)
	filesGrp.POST("/term/files/count", termH.StartCount)
	filesGrp.GET("/term/files/count", termH.CountStatus)
	filesGrp.POST("/term/files/count-cancel", termH.CancelCount)
	filesGrp.POST("/term/sftp-auth", termH.SFTPAuth)
	filesGrp.GET("/term/scripts", termH.ListScripts)
	filesGrp.POST("/term/scripts", termH.CreateScript)
	filesGrp.PUT("/term/scripts/:id", termH.UpdateScript)
	filesGrp.DELETE("/term/scripts/:id", termH.DeleteScript)
	filesGrp.GET("/term/history", termH.GetHistory)
	filesGrp.PUT("/term/history", termH.SaveHistory)
	filesGrp.GET("/term/favs", termH.GetFavs)
	filesGrp.PUT("/term/favs", termH.SaveFavs)
	// 文档附件：二进制内容经 gzip 中间件无收益还烧 CPU，挂本组（无 gzip）；
	// <img>/video 标签带不了 Authorization 头，靠同源 HttpOnly Cookie 鉴权
	filesGrp.POST("/docs-assets", docH.UploadAsset)
	filesGrp.GET("/docs-assets/:id", docH.GetAsset)
	// 数据库备份文件下载（二进制，免 gzip；登录鉴权同文件管理组）
	dbH := &DBToolHandler{st: st, mg: dbMgr}
	filesGrp.GET("/dbs/backups/download", dbH.backupDownload)

	// 只读快照与即时查询（Homepage 等常驻面板轮询）：长期令牌鉴权，未配置令牌则不开通
	if cfg.SnapshotToken != "" {
		snapH := &SnapshotHandler{st: st, token: cfg.SnapshotToken,
			offlineAfter: int64(cfg.OfflineAfter.D().Seconds())}
		r.GET("/api/v1/snapshot", snapH.Get)
		promH := &PromQueryHandler{st: st, token: cfg.SnapshotToken,
			offlineAfter: int64(cfg.OfflineAfter.D().Seconds())}
		r.GET("/api/v1/query", promH.Instant)
	}

	// 任意登录用户可访问的端点（361：受限用户 role=user 仅开放这一组——
	// 个人信息自助 + HA 设备页面；gzip 同管理组口径）
	haH := &HAHandler{st: st, mgr: intMgr}
	openGrp := r.Group("/api/v1")
	openGrp.Use(authRequired(st), gzipEncode())
	{
		openGrp.POST("/auth/logout", authH.Logout)
		openGrp.GET("/auth/me", authH.Me)
		openGrp.GET("/auth/avatar", authH.Avatar)       // 头像独立端点（6，me 不再背 300KB data URL）
		openGrp.PUT("/auth/avatar", authH.UpdateAvatar) // 44 头像按会话用户读写，受限用户同样可配置
		openGrp.PUT("/auth/profile", authH.UpdateProfile)
		// 自助两步验证（45 按用户隔离）：操作对象恒为当前会话用户，受限用户同样可开
		openGrp.GET("/auth/totp-status", authH.TOTPStatus)
		openGrp.POST("/auth/totp-setup", authH.TOTPSetup)
		openGrp.POST("/auth/totp-enable", authH.TOTPEnable)
		openGrp.POST("/auth/totp-disable", authH.TOTPDisable)

		openGrp.GET("/ha/sources", haH.Sources)                                 // HA 集成来源清单（仅 id/name）
		openGrp.GET("/ha/devices", haH.Devices)                                 // 收藏设备 + 实时状态
		openGrp.POST("/ha/call", haH.Call)                                      // 开关设备（仅收藏清单内实体）
		openGrp.GET("/ha/entity/events", haH.EntityEvents)                      // 单实体开合翻转清单（511 详情弹层）
		openGrp.GET("/ha/entity/history", haH.EntityHistory)                    // 数值传感器曲线（512 详情弹层）
		openGrp.GET("/emby/history", (&IntegrationHandler{st: st}).EmbyHistory) // Emby 播放记录分页清单（528/530 主页弹层）
		openGrp.GET("/emby/stats", (&IntegrationHandler{st: st}).EmbyStats)     // Emby 播放统计报表（530 主页弹层）
	}

	// 其余 API 需要管理员会话（361 受限用户一律 403；gzip 压缩 JSON 响应：
	// /servers 等大响应缩到 1/10 上下，总览页公网加载的主要耗时在传输）
	grp := r.Group("/api/v1")
	grp.Use(authRequired(st), adminRequired(), gzipEncode())
	{
		userH := &UserHandler{st: st}
		grp.GET("/auth/users", userH.List)
		grp.POST("/auth/users", userH.Create)
		grp.PUT("/auth/users/:id", userH.Update)
		grp.DELETE("/auth/users/:id", userH.Delete)

		srvH := &ServerHandler{st: st, offlineAfter: int64(cfg.OfflineAfter.D().Seconds()), term: termMgr}
		grp.GET("/servers", srvH.List)
		// 顶栏全局统计的轻量端点（不取快照；路径不并到 /servers/stats——
		// 会与 GET /servers/:id 的参数路由冲突）
		grp.GET("/stats", srvH.Stats)
		grp.POST("/servers", srvH.Create)
		grp.GET("/servers/:id", srvH.Get)
		grp.PUT("/servers/:id", srvH.Update)
		grp.DELETE("/servers/:id", srvH.Delete)
		grp.GET("/servers/:id/history", srvH.History)
		// 容器/虚拟机操作（54 飞牛集成）：入队由 push agent 捎带执行；状态
		// 轮询走顶层 /apps-task（与 /servers/:id 参数段同层放静态段 gin 会 panic）
		grp.POST("/servers/:id/apps/action", srvH.CreateAppAction)
		grp.GET("/apps-task/:id", srvH.AppTaskState)

		groupH := &GroupHandler{st: st}
		grp.GET("/groups", groupH.List)
		grp.POST("/groups", groupH.Create)
		grp.PUT("/groups/:id", groupH.Update)
		grp.DELETE("/groups/:id", groupH.Delete)

		grp.GET("/docs", docH.List)
		grp.POST("/docs/groups", docH.CreateGroup)
		grp.PUT("/docs/groups/:id", docH.UpdateGroup)
		grp.DELETE("/docs/groups/:id", docH.DeleteGroup)
		grp.POST("/docs", docH.Create)
		grp.PUT("/docs/order", docH.Order) // 组内拖拽排序（静态段与 :id 共存，见 dashboard-metrics/order 先例）
		grp.PUT("/docs/move", docH.Move)   // 跨分组拖拽（同上静态段先例）
		grp.GET("/docs/:id", docH.Get)
		grp.PUT("/docs/:id", docH.Update)
		grp.DELETE("/docs/:id", docH.Delete)
		// 回收站用 /docs-trash 前缀：与 /docs/:id 参数段同层的静态段会让 gin 路由树 panic
		grp.GET("/docs-trash", docH.ListTrash)
		grp.POST("/docs-trash/:id/restore", docH.RestoreTrash)
		grp.DELETE("/docs-trash/:id", docH.DeleteTrash)
		grp.DELETE("/docs-trash", docH.EmptyTrash)
		grp.GET("/docs/:id/versions", docH.ListVersions)
		grp.GET("/docs/:id/versions/:vid", docH.GetVersion)

		pageH := &PageHandler{st: st, mail: mailMgr, offlineAfter: int64(cfg.OfflineAfter.D().Seconds())}
		grp.GET("/page/config", pageH.Get)
		grp.GET("/page/stats", pageH.Stats) // 统计概览（459；静态段先注册，同 docs/order 先例）
		grp.PUT("/page/settings", pageH.SaveSettings)
		grp.POST("/page/groups", pageH.CreateGroup)
		grp.PUT("/page/groups/order", pageH.OrderGroups) // 静态段 order 优先（同 docs/order 先例）
		grp.PUT("/page/groups/:id", pageH.RenameGroup)
		grp.DELETE("/page/groups/:id", pageH.DeleteGroup)
		grp.POST("/page/links", pageH.CreateLink)
		grp.PUT("/page/links/order", pageH.OrderLinks)
		grp.PUT("/page/links/:id", pageH.UpdateLink)
		grp.DELETE("/page/links/:id", pageH.DeleteLink)
		grp.GET("/page/metrics", pageH.ListMetrics)
		grp.POST("/page/metrics", pageH.CreateMetric)
		grp.PUT("/page/metrics/order", pageH.OrderMetrics)
		grp.PUT("/page/metrics/:id", pageH.UpdateMetric)
		grp.DELETE("/page/metrics/:id", pageH.DeleteMetric)

		dashH := &DashChartHandler{st: st}
		grp.GET("/dashboard-charts", dashH.List)
		grp.POST("/dashboard-charts", dashH.Create)
		grp.DELETE("/dashboard-charts/:id", dashH.Delete)
		grp.GET("/dashboard-charts/data", dashH.Data)
		grp.PUT("/dashboard-charts/order", dashH.Order)

		tileH := &DashMetricHandler{st: st}
		grp.GET("/dashboard-metrics", tileH.List)
		grp.POST("/dashboard-metrics", tileH.Create)
		grp.DELETE("/dashboard-metrics/:id", tileH.Delete)
		grp.PUT("/dashboard-metrics/order", tileH.Order)
		grp.PUT("/dashboard-metrics/:id", tileH.Update) // 静态段 order 优先，与 :id 共存（同 docs/order 先例）

		setH := &SettingsHandler{st: st, alertKeepDays: cfg.AlertKeepDays, dbPath: cfg.DBPath}
		grp.GET("/settings", setH.Get)
		grp.PUT("/settings", setH.Put)
		grp.POST("/settings/cleanup", setH.Cleanup)
		grp.POST("/settings/db-backup", setH.DBBackup)
		grp.POST("/settings/wxpusher-test", setH.WXTest)
		grp.POST("/settings/webhook-test", setH.WebhookTest)
		grp.POST("/settings/webpush-test", setH.WebPushTest)

		insH := &InspectHandler{st: st, mg: mailMgr, offlineAfter: int64(cfg.OfflineAfter.D() / time.Second)}
		grp.GET("/inspection", insH.Get)
		grp.POST("/inspection/push", insH.Push)

		certH := &CertHandler{st: st}
		grp.GET("/certs", certH.List)
		grp.POST("/certs", certH.Create)
		grp.DELETE("/certs/:id", certH.Delete)
		grp.POST("/certs/check", certH.Check)

		intH := &IntegrationHandler{st: st, mgr: intMgr}
		grp.GET("/integrations", intH.List)
		grp.POST("/integrations", intH.Create)
		grp.PUT("/integrations/order", intH.Order) // 静态段优先（同 docs/order 先例）
		grp.POST("/integrations/test", intH.Test)
		grp.GET("/integrations/data", intH.Data)
		grp.PUT("/integrations/:id", intH.Update)
		grp.DELETE("/integrations/:id", intH.Delete)
		grp.GET("/page/weather", pageH.Weather)                // 首页天气（auto/manual 同端点）
		grp.GET("/page/weather/latest", pageH.WeatherLatest)   // 最新采样入库天气（56，HA 胶囊读库不触发上游）
		grp.GET("/page/weather/history", pageH.WeatherHistory) // 历史曲线（522，采样明细+归档拼接）
		grp.GET("/page/weather/city", pageH.SearchCity)        // 手动城市候选搜索
		grp.GET("/ha/catalog", haH.Catalog)                    // 可控实体目录（配置收藏设备用）
		grp.PUT("/ha/devices-config", haH.SaveDevices)         // 保存收藏设备清单（475 起=智能家居总配置）
		grp.GET("/ha/master", haH.GetMaster)                   // 智能家居总配置·连接参数（token 不回显）
		grp.PUT("/ha/master", haH.SaveMaster)                  // 保存连接参数（token 留空沿用）

		mailH := NewMailHandler(st, mailMgr)
		grp.GET("/mail/presets", mailH.presets)                      // 服务商预设清单
		grp.GET("/mail/accounts", mailH.accounts)                    // 账户列表（含未读角标）
		grp.POST("/mail/accounts", mailH.create)                     // 新建（先测后存）
		grp.PUT("/mail/accounts/order", mailH.order)                 // 排序（静态段优先）
		grp.POST("/mail/accounts/:id/test", mailH.test)              // 连接测试
		grp.PUT("/mail/accounts/:id", mailH.update)                  // 编辑（密码留空沿用）
		grp.DELETE("/mail/accounts/:id", mailH.remove)               // 删除
		grp.GET("/mail/accounts/:id/folders", mailH.folders)         // 文件夹清单
		grp.GET("/mail/accounts/:id/messages", mailH.messages)       // 信头列表/搜索
		grp.GET("/mail/accounts/:id/cached", mailH.cached)           // 缓存兜底列表
		grp.GET("/mail/accounts/:id/message", mailH.read)            // 阅读一封
		grp.POST("/mail/accounts/:id/seen", mailH.seen)              // 已读/未读
		grp.POST("/mail/accounts/:id/delete-msgs", mailH.removeMsgs) // 删除
		grp.POST("/mail/accounts/:id/send", mailH.send)              // 写信/回复/转发（按 :id 取账户）
		grp.GET("/mail/badge", mailH.badge)                          // 侧栏角标（Manager 缓存）
		grp.GET("/mail/contacts", mailH.contacts)                    // 通讯录（506：写信关键字带出）
		grp.POST("/mail/contacts", mailH.addContact)                 // 新增联系人
		grp.PUT("/mail/contacts/:id", mailH.editContact)             // 编辑联系人
		grp.DELETE("/mail/contacts/:id", mailH.delContact)           // 删除联系人
		grp.GET("/mail/signature", mailH.getSignature)               // 个人签名（507）
		grp.PUT("/mail/signature", mailH.setSignature)               // 保存签名
		// 附件下载挂免 gzip 的 filesGrp（二进制压缩无收益白烧 CPU），且 handler
		// 内要清连接写超时——经 gzip 包装的 Writer 穿不透，挂本组最稳
		filesGrp.GET("/mail/accounts/:id/attachment", mailH.attachment)

		// AI 用量查询（356）
		aiH := NewAiUsageHandler(st, aiMgr)
		grp.GET("/aiusage/presets", aiH.presets)               // 服务商预设清单
		grp.GET("/aiusage/accounts", aiH.accounts)             // 账户列表（含最新用量快照）
		grp.POST("/aiusage/accounts", aiH.create)              // 新建（先测后存）
		grp.POST("/aiusage/accounts/:id/test", aiH.test)       // 连接测试
		grp.POST("/aiusage/accounts/:id/refresh", aiH.refresh) // 手动刷新单账户
		grp.PUT("/aiusage/accounts/:id", aiH.update)           // 编辑（Key 留空沿用）
		grp.DELETE("/aiusage/accounts/:id", aiH.remove)        // 删除
		grp.POST("/aiusage/refresh", aiH.refreshAll)           // 手动刷新全部

		// PT 站点信息（391）
		ptH := NewPtHandler(st, ptMgr)
		grp.GET("/pt/catalog", ptH.catalog)            // 内置站点目录（添加下拉用）
		grp.GET("/pt/sites", ptH.sites)                // 站点列表（含最新快照）
		grp.POST("/pt/sites", ptH.create)              // 新建（先测后存）
		grp.PUT("/pt/sites/order", ptH.order)          // 排序（静态段优先）
		grp.POST("/pt/test", ptH.test)                 // 凭据测试（不落库）
		grp.PUT("/pt/sites/:id", ptH.update)           // 编辑（凭据留空沿用）
		grp.DELETE("/pt/sites/:id", ptH.remove)        // 删除
		grp.POST("/pt/sites/:id/refresh", ptH.refresh) // 手动刷新单站点
		grp.POST("/pt/sites/:id/signin", ptH.signin)   // 手动签到（416，dian115 类站）
		grp.POST("/pt/refresh", ptH.refreshAll)        // 手动刷新全部（异步整轮）

		// 服务监控（366 两级重构）：服务器 → 监控端口 TCP 拨测
		svcH := NewSvcMonHandler(st, svcMgr)
		grp.GET("/svcmon", svcH.list)
		grp.POST("/svcmon/servers", svcH.createServer)
		grp.PUT("/svcmon/servers/:id", svcH.updateServer)
		grp.DELETE("/svcmon/servers/:id", svcH.removeServer)
		grp.POST("/svcmon/servers/:id/ports", svcH.createPort)
		grp.POST("/svcmon/servers/:id/check", svcH.checkServer)
		grp.PUT("/svcmon/ports/:pid", svcH.updatePort)
		grp.DELETE("/svcmon/ports/:pid", svcH.removePort)
		grp.POST("/svcmon/ports/:pid/check", svcH.checkPort)
		grp.POST("/svcmon/test", svcH.test) // 静态段优先（同 docs/order 先例）
		grp.POST("/svcmon/refresh", svcH.refreshAll)
		grp.GET("/svcmon/events", svcH.events)             // 最近状态翻转事件（500）
		grp.GET("/svcmon/availability", svcH.availability) // 逐端口窗口可用率（500）

		// GitHub 仓库发布监控（466）：监控仓库 CRUD + 最近 5 条 Release 快照
		ghH := &GhHandler{st: st, mg: ghMgr}
		grp.GET("/gh/repos", ghH.list)
		grp.POST("/gh/repos", ghH.create)
		grp.POST("/gh/test", ghH.test)            // 静态段优先（同 docs/order 先例）
		grp.POST("/gh/refresh", ghH.refreshAll)   // 静态段优先
		grp.POST("/gh/repos/:id/seen", ghH.seen)  // 单仓新更新已看记账（489）
		grp.PUT("/gh/settings", ghH.saveSettings) // 轮询间隔设置（静态段，与 /gh/repos/:id 不冲突）
		grp.PUT("/gh/repos/:id", ghH.update)
		grp.DELETE("/gh/repos/:id", ghH.remove)
		grp.POST("/gh/repos/:id/refresh", ghH.refresh)

		// 数据库管理工具（323-325）
		grp.GET("/dbs", dbH.list)                             // 连接清单
		grp.POST("/dbs", dbH.create)                          // 新建（先测后存）
		grp.POST("/dbs/test", dbH.testInput)                  // 未入库配置测试
		grp.PUT("/dbs/:id", dbH.update)                       // 编辑（密码留空沿用）
		grp.DELETE("/dbs/:id", dbH.remove)                    // 删除（连带备份目录）
		grp.POST("/dbs/:id/test", dbH.testStored)             // 手动测试
		grp.GET("/dbs/:id/schemas", dbH.schemas)              // 库/schema 清单
		grp.GET("/dbs/:id/tables", dbH.tables)                // 表清单
		grp.GET("/dbs/:id/table", dbH.table)                  // 表详情（列/索引/DDL/主键/行数）
		grp.GET("/dbs/:id/columns", dbH.columns)              // 全库列元数据（SQL 补全）
		grp.GET("/dbs/:id/rows", dbH.rows)                    // 数据分页
		grp.POST("/dbs/:id/rows", dbH.insertRow)              // 插入行
		grp.POST("/dbs/:id/rows/update", dbH.updateRow)       // 按主键更新
		grp.POST("/dbs/:id/rows/delete", dbH.deleteRow)       // 按主键删除
		grp.POST("/dbs/:id/query", dbH.query)                 // 查询（SQL 控制台）
		grp.GET("/dbs/:id/backups", dbH.backups)              // 备份清单
		grp.POST("/dbs/:id/backups", dbH.backupCreate)        // 触发备份
		grp.POST("/dbs/:id/backups/delete", dbH.backupDelete) // 删除备份
		grp.POST("/dbs/:id/import", dbH.importSQL)            // SQL 文件导入

		// 在线更新（395/396）：检查/应用自更新 + agent 版本与分发
		updH := &UpdateHandler{mgr: updMgr}
		grp.GET("/update/status", updH.Status)
		grp.POST("/update/check", updH.Check)
		grp.POST("/update/apply", updH.Apply)
		grp.GET("/update/agents", updH.Agents)
		grp.POST("/update/agents/upgrade", updH.UpgradeAgents)

		sysH := &SystemHandler{}
		grp.POST("/system/restart", sysH.Restart)

		statusH := &StatusHandler{st: st, dbPath: cfg.DBPath, version: version,
			startedAt: startupAt, offlineAfter: int64(cfg.OfflineAfter.D().Seconds())}
		grp.GET("/status", statusH.Get)

		ruleH := &RuleHandler{st: st, eng: eng}
		grp.GET("/alert-rules", ruleH.List)
		grp.POST("/alert-rules", ruleH.Create)
		grp.PUT("/alert-rules/:id", ruleH.Update)
		grp.DELETE("/alert-rules/:id", ruleH.Delete)

		alertH := &AlertHandler{st: st}
		grp.GET("/alerts", alertH.List)

		pushH := NewWebPushHandler(st)
		grp.POST("/webpush/subscribe", pushH.Subscribe)
		grp.POST("/webpush/unsubscribe", pushH.Unsubscribe)
		grp.DELETE("/webpush/subs/:id", pushH.DeleteSub)

		netH := NewNetCheckHandler()
		grp.POST("/net-check", netH.Check)

		msgH := &MessagesHandler{st: st}
		grp.GET("/messages", msgH.List)
		grp.GET("/messages/unread", msgH.Unread)
		grp.POST("/messages/read", msgH.MarkRead)
		grp.POST("/messages/read-all", msgH.MarkAllRead)
		grp.DELETE("/messages", msgH.Clear)
		grp.DELETE("/messages/:id", msgH.Delete)

		remH := NewReminderHandler(st)
		grp.GET("/reminders", remH.Board)
		grp.POST("/reminders", remH.Create)
		grp.PUT("/reminders/:id", remH.Update)
		grp.DELETE("/reminders/:id", remH.Delete)
		grp.POST("/reminders/:id/complete", remH.Complete)
		grp.POST("/reminders/:id/reopen", remH.Reopen)
		grp.POST("/reminders/:id/test-push", remH.TestPush)
		// 回收站用 /reminders-trash 前缀：与 /reminders/:id 参数段同层的静态段会让 gin 路由树 panic
		grp.POST("/reminders-trash/:id/restore", remH.RestoreTrash)
		grp.DELETE("/reminders-trash/:id", remH.DeleteTrash)
		grp.DELETE("/reminders-trash", remH.EmptyTrash)
		grp.POST("/reminder-groups", remH.CreateGroup)
		grp.PUT("/reminder-groups/:id", remH.UpdateGroup)
		grp.DELETE("/reminder-groups/:id", remH.DeleteGroup)

		// 会员到期提醒（492）：提醒事项模块下的独立配置数据
		memH := NewMembershipHandler(st)
		grp.GET("/memberships", memH.List)
		grp.POST("/memberships", memH.Create)
		grp.PUT("/memberships/:id", memH.Update)
		grp.DELETE("/memberships/:id", memH.Delete)
		// 拖拽排序保存用 /memberships-reorder 前缀：与 /memberships/:id 参数段同层的静态段会让 gin 路由树 panic
		grp.POST("/memberships-reorder", memH.Reorder)
		grp.POST("/memberships/:id/test-push", memH.TestPush)

		// 追剧（548）：搜索用 /tvfollows-search 前缀（同上 gin 路由树约束）
		tvH := &TVFollowHandler{st: st}
		grp.GET("/tvfollows", tvH.List)
		grp.POST("/tvfollows", tvH.Create)
		grp.GET("/tvfollows-search", tvH.Search)
		grp.GET("/tvfollows-task/:id", tvH.TaskStatus)          // 前端轮询中转任务状态（573，前缀避 gin 路由树约束）
		grp.GET("/tvfollows-resource-links", tvH.ResourceLinks) // 资源站搜索链接（565，前缀避 gin 路由树约束）
		grp.POST("/tvfollows/:id/check", tvH.Check)
		grp.POST("/tvfollows/:id/emby-check", tvH.EmbyCheck)
		grp.POST("/tvfollows/:id/enable", tvH.Enable)
		grp.POST("/tvfollows/:id/short-drama", tvH.ShortDrama) // 手动标记短剧（58）
		grp.DELETE("/tvfollows/:id", tvH.Delete)

		// B 站追更（TODO 6，追剧页「追更」tab）：/bili-follows-task 与
		// /bili-follows-settings 用前缀段避 gin 路由树约束（tvfollows-search 同款）
		biliH := &BiliFollowHandler{st: st}
		grp.GET("/bili-follows", biliH.List)
		grp.POST("/bili-follows", biliH.Create)
		grp.PUT("/bili-follows/:id", biliH.Update)
		grp.DELETE("/bili-follows/:id", biliH.Delete)
		grp.POST("/bili-follows/:id/run", biliH.Run)
		grp.POST("/bili-follows/:id/stop", biliH.Stop)
		grp.GET("/bili-follows/:id/items", biliH.Items) // 已抓记录最近 10 条（16，权威台账在库）
		grp.GET("/bili-follows-task/:id", biliH.TaskStatus)
		grp.GET("/bili-follows-settings", biliH.GetSettings)
		grp.PUT("/bili-follows-settings", biliH.PutSettings)

		// 视频下载（TODO 4，追剧页「视频下载」tab）：/video-dls-settings 前缀段
		// 避 gin 路由树约束（bili-follows-settings 同款）
		vdH := &VideoDlHandler{st: st}
		grp.GET("/video-dls", vdH.List)
		grp.POST("/video-dls", vdH.Create)
		grp.POST("/video-dls/:id/stop", vdH.Stop)
		grp.DELETE("/video-dls/:id", vdH.Delete)
		grp.GET("/video-dls-settings", vdH.GetSettings)
		grp.PUT("/video-dls-settings", vdH.PutSettings)

		// mihomo 控制台（TODO 7，服务监控页 mihomo tab）：读面板/写操作全走
		// net_tasks 中转（purpose=mihomo，push agent 内网代执行）
		mihH := &MihomoHandler{st: st}
		grp.GET("/mihomo/config", mihH.Config)
		grp.PUT("/mihomo/config", mihH.SaveConfig)
		grp.POST("/mihomo/query", mihH.Query)
		grp.POST("/mihomo/op", mihH.Op)
		grp.GET("/mihomo/task/:id", mihH.Task)
	}

	// 前端静态资源（embed），非文件路径回落 index.html 支持 SPA history 路由。
	// 注意不能用 gin FileFromFS("index.html")：http.FileServer 会把 */index.html
	// 301 重定向到 ./，与 SPA 回落组合成重定向循环，这里用 ServeContent 直出。
	// /favicon.ico：书签/聊天预览等不解析 HTML 的工具按惯例直接请求该路径，重定向到实际图标
	r.GET("/favicon.ico", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/favicon-32.png")
	})
	dist, err := fs.Sub(ui.Dist(), "dist")
	if err != nil {
		return fmt.Errorf("嵌入前端资源失败: %w", err)
	}
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
			return
		}
		p := strings.TrimPrefix(c.Request.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if serveStatic(c, p, dist) {
			return
		}
		if !serveStatic(c, "index.html", dist) {
			c.String(http.StatusNotFound, "前端资源缺失，请先执行 make web")
		}
	})
	return nil
}

// serveStatic 从 embed FS 输出单个文件；文件不存在/为目录返回 false。
// 缓存策略：assets/ 下是带内容哈希的构建产物，可永久缓存；index.html 与
// sw.js 必须每次回源（no-cache），否则发版后浏览器可能仍用旧入口页引用
// 旧资源、SW 更新被拖住；其余无哈希根文件只随发版变化，给 1 天缓存。
// 文本类资源（JS/CSS/HTML/SVG/JSON/WASM）按需 gzip：前端构建产物与
// vditor 本地化资源（lute/mermaid/highlight 共 8MB+）明文传输是「编辑器
// 首次打开特别慢」的主因；浏览器缓存（immutable）后不再走到这里。
func serveStatic(c *gin.Context, name string, fsys fs.FS) bool {
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return false
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return false
	}
	if strings.HasPrefix(name, "assets/") {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else if name == "index.html" || name == "sw.js" {
		// 入口页与 Service Worker 必须每次回源（no-cache）：发版后浏览器可能
		// 仍用旧入口页引用旧资源；SW 过期会拖住 PWA 更新。响应无 ETag/Last-Modified
		// 可供 304，no-cache 等于每次整文件重拉，只留给这两个必然极小的文件
		c.Header("Cache-Control", "no-cache")
	} else {
		// 其余无哈希根文件（favicon/manifest/pwa 图标/vendor 编辑器库/pt 图标）：
		// 内容只随发版变化，给 1 天缓存——否则公网/边缘链路下每次进页面都整量重拉
		//（实测 favicon/manifest 也各背 2~3s RTT）
		c.Header("Cache-Control", "public, max-age=86400")
	}
	if gz, ok := gzipStatic(c, name, data); ok {
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")
		c.Header("Content-Type", contentTypeByExt(name))
		c.Header("Content-Length", strconv.Itoa(len(gz)))
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write(gz)
		return true
	}
	http.ServeContent(c.Writer, c.Request, st.Name(), time.Time{}, bytes.NewReader(data))
	return true
}

// gzCache 静态资源压缩结果缓存（name → 压缩字节）：embed 产物在二进制
// 生命周期内字节恒定（assets/ 带内容哈希、vendor 文件名固定），压缩一次
// 驻留复用。全量大文件（ts.worker/mermaid/lute/主包等）压缩后共约 6MB，
// 换来冷访问不再每次重压（大文件单次 15~40ms CPU，弱网首开/多客户端
// 并发拉资源时直接拖慢页面加载）。
var gzCache sync.Map

// gzipStatic 判定并压缩单个静态响应：客户端接受 gzip、非 Range 请求、
// 文本类且足够大（小文件压缩反而亏）。压缩结果进 gzCache，命中直接
// 返回缓存字节。
func gzipStatic(c *gin.Context, name string, data []byte) ([]byte, bool) {
	if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") ||
		c.GetHeader("Range") != "" || len(data) < 1024 {
		return nil, false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs", ".css", ".html", ".svg", ".json", ".map", ".txt", ".wasm", ".xml":
	default:
		return nil, false
	}
	if gz, ok := gzCache.Load(name); ok {
		return gz.([]byte), true
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, false
	}
	if err := w.Close(); err != nil {
		return nil, false
	}
	gz := buf.Bytes()
	gzCache.Store(name, gz)
	return gz, true
}

// contentTypeByExt 静态资源 Content-Type（gzip 分支绕过 ServeContent 后需自给）。
func contentTypeByExt(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return http.DetectContentType([]byte(name))
}
