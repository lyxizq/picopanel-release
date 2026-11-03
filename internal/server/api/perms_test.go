package api

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/store"
)

// newPermsRouter 真实链路：admin + 家庭成员（role=user）两账号。
func newPermsRouter(t *testing.T) (*gin.Engine, *store.Store, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "perm.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("fam", "pass1234", "user"); err != nil {
		t.Fatal(err)
	}
	admin, _ := s.UserByUsername("admin")
	fam, _ := s.UserByUsername("fam")
	adminTok, err := s.CreateSession(admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	famTok, err := s.CreateSession(fam.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return r, s, adminTok, famTok
}

// TestFamilyGateAPI 家庭成员闸（2026-11-03 口径：多用户仅框定家庭成员，无可配
// 页面权限）：固定开放主页/文档/用药/邮件/提醒/消息/工具箱与共享 HA 使用端点，
// 其余（监控/HA 配置/日历/告警/用户管理/系统设置等）一律 403（deny by default）。
func TestFamilyGateAPI(t *testing.T) {
	r, s, adminTok, famTok := newPermsRouter(t)
	base := "/api/v1"

	// ---- 家庭成员固定可用集（无需任何配置）----
	for _, c := range [][2]string{
		{"GET", base + "/auth/me"},            // openGrp 自助
		{"GET", base + "/reminders"},          // 提醒事项
		{"GET", base + "/reminder-groups"},    // 提醒分组
		{"GET", base + "/memberships"},        // 会员到期
		{"GET", base + "/messages"},           // 消息中心（个人）
		{"GET", base + "/docs"},               // 文档
		{"GET", base + "/med-persons"},        // 用药（个人绑定）
		{"GET", base + "/medications"},        // 用药
		{"GET", base + "/meds/day"},           // 用药日视图
		{"GET", base + "/mail/accounts"},      // 邮件
		{"GET", base + "/mail/signature"},     // 邮件个人签名
		{"GET", base + "/page/config"},        // 主页壳配置读
		{"GET", base + "/page/weather"},       // 主页天气
		{"GET", base + "/page/links"},         // 主页链接（个人）
		{"GET", base + "/page/groups"},        // 主页链接分组（个人）
		{"GET", base + "/countdowns"},         // 倒数日（个人，主页卡共用）
		{"GET", base + "/ha/sources"},         // 共享 HA：来源清单
		{"POST", base + "/net-check"},         // 工具箱连通检测
		{"POST", base + "/webpush/subscribe"}, // 浏览器推送订阅
	} {
		if w := memReq(r, famTok, c[0], c[1], ""); w.Code == http.StatusForbidden {
			t.Fatalf("家庭成员应放行 %s %s，得 403", c[0], c[1])
		}
	}

	// 管理员专属（未进 familyRoutes 一律 403；deny by default）
	for _, c := range []struct{ method, path, body string }{
		{"GET", base + "/servers", ""},
		{"GET", base + "/stats", ""},
		{"GET", base + "/groups", ""}, // 服务器分组（监控域，2026-11-03 审计修正）
		{"GET", base + "/alerts", ""}, // 告警历史（监控域）
		{"GET", base + "/alert-rules", ""},
		{"GET", base + "/integrations", ""},
		{"GET", base + "/integrations/data", ""},
		{"GET", base + "/page/stats", ""},        // 主页统计概览（监控聚合）
		{"GET", base + "/page/metrics", ""},      // 主页指标格
		{"PUT", base + "/page/settings", "{}"},   // 主页壳配置写
		{"GET", base + "/ha/master", ""},         // HA 配置收归管理员（共享=只用不改）
		{"PUT", base + "/ha/master", `{"url":"http://x.local"}`},
		{"GET", base + "/ha/catalog?source=0", ""},
		{"PUT", base + "/ha/devices-config?source=0", `{"devices":[]}`},
		{"GET", base + "/calendar", ""},          // 日历页不做多人
		{"GET", base + "/calendar/subs", ""},     // ICS 订阅管理
		{"POST", base + "/calendar/feed-token", ""},
		{"GET", base + "/pt/sites", ""},
		{"GET", base + "/tvfollows", ""},
		{"GET", base + "/svcmon", ""},
		{"GET", base + "/gh/repos", ""},
		{"GET", base + "/dbs", ""},
		{"GET", base + "/aiusage/accounts", ""},
		{"GET", base + "/term/scripts", ""},
		{"GET", base + "/term/files?server_id=1&path=/", ""},
		{"POST", base + "/settings/cleanup", `{}`},
		{"GET", base + "/inspection", ""},
		{"GET", base + "/certs", ""},
		{"GET", base + "/auth/users", ""}, // 用户管理永远管理员专属
		{"GET", base + "/status", ""},
	} {
		if w := memReq(r, famTok, c.method, c.path, c.body); w.Code != http.StatusForbidden {
			t.Fatalf("家庭成员访问管理员端点应 403：%s %s，得 %d", c.method, c.path, w.Code)
		}
	}

	// method 感知：主页壳配置只读（PUT /page/settings 在上面已断言 403）
	if w := memReq(r, famTok, "GET", base+"/page/config", ""); w.Code != http.StatusOK {
		t.Fatalf("主页壳配置读应 200，得 %d", w.Code)
	}

	// 倒数日读写全放（个人数据，隔离在 store 层）
	if w := memReq(r, famTok, "POST", base+"/countdowns", `{"name":"测试","kind":"once","date":"2026-12-01"}`); w.Code != http.StatusOK {
		t.Fatalf("倒数日写应放行（个人数据），得 %d %s", w.Code, w.Body.String())
	}

	// 共享 HA 使用端点：家庭成员可看收藏设备清单（未配置连接时业务层 400，但绝
	// 不是 403——「共享」= 用全局配置开关设备，配置端点才收归管理员）
	if w := memReq(r, famTok, "GET", base+"/ha/devices?source=0", ""); w.Code == http.StatusForbidden {
		t.Fatalf("HA 使用端点对家庭成员不应 403，得 403")
	}
	if w := memReq(r, famTok, "POST", base+"/ha/call", `{"entity":"light.x","action":"toggle"}`); w.Code == http.StatusForbidden {
		t.Fatalf("HA 开关设备对家庭成员不应 403，得 403")
	}

	// admin 不受闸影响全过
	if w := memReq(r, adminTok, "GET", base+"/servers", ""); w.Code != http.StatusOK {
		t.Fatalf("admin 应放行服务器列表，得 %d", w.Code)
	}
	if w := memReq(r, adminTok, "GET", base+"/auth/users", ""); w.Code != http.StatusOK {
		t.Fatalf("admin 用户管理应放行，得 %d", w.Code)
	}

	// 建号缺省角色=家庭成员（2026-11-03 修正 Create 空 role 兜 admin 的存量坑）
	if w := memReq(r, adminTok, "POST", base+"/auth/users", `{"username":"fam2","password":"pass1234"}`); w.Code != http.StatusOK {
		t.Fatalf("建号失败: %d %s", w.Code, w.Body.String())
	}
	u, _ := s.UserByUsername("fam2")
	if u == nil || u.Role != store.RoleUser {
		t.Fatalf("缺省建号应为家庭成员角色: %+v", u)
	}

	// /auth/me 带 permissions 字段（默认未配置=null）
	if w := memReq(r, famTok, "GET", base+"/auth/me", ""); !strings.Contains(w.Body.String(), "permissions") {
		t.Fatalf("/auth/me 应返回 permissions 字段: %s", w.Body.String())
	}
}

// TestFamilyModulePermsConfig 管理员按成员配置后台菜单项（2026-11-03）：白名单
// 过滤、即时生效（会话现读）、清空回落默认全量；未勾选模块的 API 一律 403。
func TestFamilyModulePermsConfig(t *testing.T) {
	r, s, adminTok, famTok := newPermsRouter(t)
	base := "/api/v1"

	// 裁剪 fam 只留 文档+消息中心
	if w := memReq(r, adminTok, "PUT", base+"/auth/users/2",
		`{"permissions":["docs","messages"],"perms_set":true}`); w.Code != http.StatusOK {
		t.Fatalf("配置菜单项失败: %d %s", w.Code, w.Body.String())
	}
	// 即时生效：勾选的放行（含前缀连带 /docs-trash、/webpush/）
	for _, c := range [][2]string{
		{"GET", base + "/docs"},
		{"GET", base + "/docs-trash"},
		{"GET", base + "/messages"},
		{"POST", base + "/webpush/subscribe"},
	} {
		if w := memReq(r, famTok, c[0], c[1], ""); w.Code == http.StatusForbidden {
			t.Fatalf("勾选模块应放行 %s %s，得 403", c[0], c[1])
		}
	}
	// 未勾选的一律 403（用药/提醒/邮件/工具箱）
	for _, c := range [][2]string{
		{"GET", base + "/med-persons"},
		{"GET", base + "/medications"},
		{"GET", base + "/reminders"},
		{"GET", base + "/memberships"},
		{"GET", base + "/mail/accounts"},
		{"POST", base + "/net-check"},
	} {
		if w := memReq(r, famTok, c[0], c[1], ""); w.Code != http.StatusForbidden {
			t.Fatalf("未勾选模块应 403：%s %s，得 %d", c[0], c[1], w.Code)
		}
	}
	// 主页/HA 同为可配置页面：未勾选 home 时主页域端点一并 403
	if w := memReq(r, famTok, "GET", base+"/page/config", ""); w.Code != http.StatusForbidden {
		t.Fatalf("未勾选 home 主页壳配置应 403，得 %d", w.Code)
	}
	if w := memReq(r, famTok, "GET", base+"/ha/devices?source=0", ""); w.Code != http.StatusForbidden {
		t.Fatalf("未勾选 ha 设备清单应 403，得 %d", w.Code)
	}
	if w := memReq(r, famTok, "GET", base+"/emby/history", ""); w.Code != http.StatusForbidden {
		t.Fatalf("未勾选 home Emby 记录应 403，得 %d", w.Code)
	}

	// 勾选 home+ha：主页域与 HA 使用端点放行
	if w := memReq(r, adminTok, "PUT", base+"/auth/users/2",
		`{"permissions":["home","ha","docs"],"perms_set":true}`); w.Code != http.StatusOK {
		t.Fatalf("配置失败: %s", w.Body.String())
	}
	if w := memReq(r, famTok, "GET", base+"/page/config", ""); w.Code != http.StatusOK {
		t.Fatalf("勾选 home 主页壳配置应放行，得 %d", w.Code)
	}
	if w := memReq(r, famTok, "POST", base+"/page/title", `{"title":"x"}`); w.Code == http.StatusForbidden {
		t.Fatalf("勾选 home 主页标题应放行，得 403")
	}
	if w := memReq(r, famTok, "GET", base+"/ha/devices?source=0", ""); w.Code == http.StatusForbidden {
		t.Fatalf("勾选 ha 设备清单应放行，得 403")
	}
	if w := memReq(r, famTok, "POST", base+"/ha/call", `{"entity":"light.x"}`); w.Code == http.StatusForbidden {
		t.Fatalf("勾选 ha 开关设备应放行，得 403")
	}

	// 白名单过滤：未知 key 丢弃
	if w := memReq(r, adminTok, "PUT", base+"/auth/users/2",
		`{"permissions":["docs","pt","term"],"perms_set":true}`); w.Code != http.StatusOK {
		t.Fatalf("配置失败: %s", w.Body.String())
	}
	u, _ := s.UserByID(2)
	if u == nil || len(u.Permissions) != 1 || u.Permissions[0] != "docs" {
		t.Fatalf("未知 key 应被丢弃: %+v", u)
	}
	if w := memReq(r, famTok, "GET", base+"/messages", ""); w.Code != http.StatusForbidden {
		t.Fatalf("裁剪后 messages 应 403，得 %d", w.Code)
	}

	// 清空（空数组+perms_set）→ 回落默认全量
	if w := memReq(r, adminTok, "PUT", base+"/auth/users/2", `{"permissions":[],"perms_set":true}`); w.Code != http.StatusOK {
		t.Fatalf("清空失败: %s", w.Body.String())
	}
	for _, p := range []string{"/docs", "/medications", "/reminders", "/mail/accounts", "/messages"} {
		if w := memReq(r, famTok, "GET", base+p, ""); w.Code == http.StatusForbidden {
			t.Fatalf("清空后应回落默认全量 %s，得 403", p)
		}
	}
}

// TestPageTitlePerUser 成员自配主页标题（2026-11-03）：成员 GET /page/config
// 拿不到管理员的 title（空=前端回落系统名）；PUT /page/title 写自己的，管理员
// 全局标题不受影响；管理员走同端点=改全局。
func TestPageTitlePerUser(t *testing.T) {
	r, s, adminTok, famTok := newPermsRouter(t)
	base := "/api/v1"

	// 管理员先配全局标题（store 直写全局壳配置）
	if err := s.SavePageTitleFor(0, "管理员的主页"); err != nil {
		t.Fatal(err)
	}

	// 成员读取：title 必须为空（不读管理员的）
	if w := memReq(r, famTok, "GET", base+"/page/config", ""); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "管理员的主页") {
		t.Fatalf("成员不应读到管理员标题: %d %s", w.Code, w.Body.String())
	}

	// 成员自配标题 → 读回自己的；管理员全局不受影响
	if w := memReq(r, famTok, "PUT", base+"/page/title", `{"title":"我的小家"}`); w.Code != http.StatusOK {
		t.Fatalf("成员保存标题失败: %d %s", w.Code, w.Body.String())
	}
	if w := memReq(r, famTok, "GET", base+"/page/config", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "我的小家") {
		t.Fatalf("成员应读到自己的标题: %d %s", w.Code, w.Body.String())
	}
	if w := memReq(r, adminTok, "GET", base+"/page/config", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "管理员的主页") {
		t.Fatalf("管理员标题不应被成员改动: %d %s", w.Code, w.Body.String())
	}

	// 管理员走同端点=改全局；成员标题仍独立
	if w := memReq(r, adminTok, "PUT", base+"/page/title", `{"title":"全家主页"}`); w.Code != http.StatusOK {
		t.Fatalf("管理员保存标题失败: %d %s", w.Code, w.Body.String())
	}
	if w := memReq(r, adminTok, "GET", base+"/page/config", ""); !strings.Contains(w.Body.String(), "全家主页") {
		t.Fatalf("管理员应读到全局标题: %s", w.Body.String())
	}
	if w := memReq(r, famTok, "GET", base+"/page/config", ""); strings.Contains(w.Body.String(), "全家主页") {
		t.Fatalf("成员不应读到管理员的新标题: %s", w.Body.String())
	}
}

// TestUserTheme 主题按用户独立（2026-11-03）：自助保存（PUT /auth/theme）后
// /auth/me 下发账号主题；白名单外主题 400；各账号互不串。
func TestUserTheme(t *testing.T) {
	r, _, adminTok, famTok := newPermsRouter(t)
	base := "/api/v1"

	if w := memReq(r, famTok, "PUT", base+"/auth/theme", `{"theme":"abyss"}`); w.Code != http.StatusOK {
		t.Fatalf("保存主题失败: %d %s", w.Code, w.Body.String())
	}
	if w := memReq(r, famTok, "GET", base+"/auth/me", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "abyss") {
		t.Fatalf("me 应下发账号主题: %d %s", w.Code, w.Body.String())
	}
	if w := memReq(r, adminTok, "GET", base+"/auth/me", ""); strings.Contains(w.Body.String(), "abyss") {
		t.Fatalf("管理员主题不应被成员改动: %s", w.Body.String())
	}
	if w := memReq(r, famTok, "PUT", base+"/auth/theme", `{"theme":"macos"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("未知主题应 400，得 %d", w.Code)
	}
}


// TestMessageItemsEndpoint 合并消息逐项清单端点（2026-11-03）：本空间 200 带清单，
// 跨空间 404 不泄露。
func TestMessageItemsEndpoint(t *testing.T) {
	r, s, adminTok, famTok := newPermsRouter(t)
	base := "/api/v1"

	if err := s.CreateMessage(&store.Message{Type: "medication", Title: "用药提醒：1 个剂量到点",
		RefType: "medication", RefID: 99, Refs: []int64{99}, UserID: 0, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	// 管理员空间消息：管理员的逐项清单（ref 指向不存在的台账行=空清单但 200）
	if w := memReq(r, adminTok, "GET", base+"/messages/1/items", ""); w.Code != http.StatusOK {
		t.Fatalf("本空间逐项清单应 200，得 %d", w.Code)
	}
	// 成员跨空间：404 不泄露
	if w := memReq(r, famTok, "GET", base+"/messages/1/items", ""); w.Code != http.StatusNotFound {
		t.Fatalf("跨空间逐项清单应 404，得 %d", w.Code)
	}
}
