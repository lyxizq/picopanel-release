package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/totp"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

const sessionTTL = 7 * 24 * time.Hour

// dummyHash 用户不存在时也做一次同代价的 bcrypt 比较用（内容无关紧要，
// 固定盐不影响时序抹平）——「用户存在走 bcrypt / 不存在立即返回」的耗时差
// 可被统计出用户名是否注册，这里让两条路径耗时一致。
const dummyHash = "$2a$10$ySbCTwiT7FjYzXKBxcfYi.YPunXupcdx1keJRzqPitiYv1lReX8Ki"

// sessionCookie 登录会话的 Cookie 载体：与 Authorization 头同一 token。
// 浏览器「原生下载」（<a> 导航）带不了 JS 设置的请求头——文件管理的大文件
// 下载交给浏览器下载器落盘（.crdownload 临时文件 + 断点续传都由浏览器管理），
// 靠这条 HttpOnly Cookie 自动过鉴权。SameSite=Strict 跨站请求不带 Cookie
// （CSRF 免疫）；HTTPS/反代 TLS 部署配置 cookie_secure: true 补 Secure 属性，
// 防 HTTP 明文降级时被嗅探。
const sessionCookie = "lz_session"

// cookieSecure 会话 Cookie 是否带 Secure 属性（cookie_secure 配置项，
// router.go 装配；默认 false 兼容纯 HTTP 内网部署）。
var cookieSecure bool

func setSessionCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(sessionCookie, token, maxAge, "/api/v1", "", cookieSecure, true)
}

// AuthHandler 处理登录/登出/改密。
type AuthHandler struct {
	st        *store.Store
	guard     *loginGuard // 按 IP（紧阈值：单源爆破）
	userGuard *loginGuard // 按用户名（宽阈值：轮换 IP 的慢速爆破与自助端点爆破）
}

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Code     string `json:"code"` // 两步验证码（开启 TOTP 后必填）
}

// maxLoginBody 登录请求体上限：三个短字符串字段绰绰有余，挡匿名超大 JSON 打点。
const maxLoginBody = 4 << 10

// Login 校验账号密码并签发会话 token。连续失败触发按 IP 与按账号双维度锁定。
func (h *AuthHandler) Login(c *gin.Context) {
	if !h.guard.check(c, clientIP(c)) || !h.userGuard.check(c, reqUsername(c)) {
		return
	}
	c.Request.Body = io.NopCloser(io.LimitReader(c.Request.Body, maxLoginBody))
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入用户名和密码"})
		return
	}
	u, err := h.st.UserByUsername(req.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用户失败"})
		return
	}
	// 无论用户是否存在都跑一次 bcrypt 比较（不存在时比 dummy hash），
	// 避免时序侧信道探测用户名；两条失败路径的响应完全一致
	ok := false
	if u != nil {
		ok = bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) == nil
	} else {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(req.Password))
	}
	if !ok {
		h.failBoth(c, req.Username)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	// 两步验证（45 按用户隔离）：仅验本人密钥——管理员开启不再牵连受限用户
	//（升级后受限用户一律默认关闭）；错误码计入双维度失败锁定（与密码同强度防爆破）
	if totp.Enabled(h.st, u.ID) {
		if strings.TrimSpace(req.Code) == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "已开启两步验证，请输入验证码", "need_code": true})
			return
		}
		if !totp.VerifySecret(h.st, u.ID, req.Code) {
			h.failBoth(c, req.Username)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "两步验证码错误", "need_code": true})
			return
		}
	}
	h.successBoth(c, req.Username)
	token, err := h.st.CreateSession(u.ID, sessionTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建会话失败"})
		return
	}
	if err := h.st.DeleteExpiredSessions(); err != nil {
		log.Printf("清理过期会话失败: %v", err)
	}
	// 成功登录留痕（最近 20 条，服务状态页查看）；失败不影响登录流程，但要留日志
	if err := h.st.RecordLogin(u.Username, clientIP(c)); err != nil {
		log.Printf("记录登录日志失败: %v", err)
	}
	// 登录成功推送（各渠道总开关 + 登录提醒细分开关都开才发）：账号名/时间/IP，
	// 异步发送不阻塞登录响应；推送失败只留日志
	go loginNotify(h.st, u.Username, clientIP(c))
	// 同步种 HttpOnly Cookie：浏览器原生下载（文件管理大文件）靠它过鉴权
	setSessionCookie(c, token, int(sessionTTL.Seconds()))
	// role 随登录返回：受限用户（361）前端落 /ha，管理员进主页/控制台
	c.JSON(http.StatusOK, gin.H{"token": token, "username": u.Username, "role": u.Role})
}

// loginNotify 登录成功后向已开启的推送渠道发一条安全提醒（账号/时间/IP）。
// 与站内登录日志同信息口径；逐渠道独立容错，失败互不影响。
func loginNotify(st *store.Store, username, ip string) {
	subject := "PicoPanel登录提醒"
	body := fmt.Sprintf("账号 %s 于 %s 成功登录，来源 IP：%s",
		username, time.Now().Format("2006-01-02 15:04:05"), ip)
	if wxpusher.On(st) && wxpusher.LoginWXOn(st) {
		if err := wxpusher.SendFromStore(st, subject, body); err != nil {
			log.Printf("登录提醒 WxPusher 推送失败: %v", err)
		}
	}
	if webhook.On(st) && webhook.LoginOn(st) {
		if err := webhook.SendFromStore(st, subject, body); err != nil {
			log.Printf("登录提醒 Webhook 推送失败: %v", err)
		}
	}
	if webpush.On(st) && webpush.LoginOn(st) {
		if err := webpush.SendFromStore(st, subject, body); err != nil {
			log.Printf("登录提醒 Web Push 推送失败: %v", err)
		}
	}
}

func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}

// failBoth 双维度记一次失败（IP + 用户名）。
func (h *AuthHandler) failBoth(c *gin.Context, username string) {
	h.guard.fail(clientIP(c))
	h.userGuard.fail("u:" + username)
}

// successBoth 双维度清失败记录。
func (h *AuthHandler) successBoth(c *gin.Context, username string) {
	h.guard.success(clientIP(c))
	h.userGuard.success("u:" + username)
}

// reqUsername 从请求体预读用户名做账号维度限流的键（读不出用空串占位，
// 空键只占一个桶位，无放大）。绑定前 body 只读 4KB，与 maxLoginBody 一致。
func reqUsername(c *gin.Context) string {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxLoginBody))
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	var probe struct {
		Username string `json:"username"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Username
}

// Logout 注销当前会话。
func (h *AuthHandler) Logout(c *gin.Context) {
	if err := h.st.DeleteSession(bearerToken(c)); err != nil {
		// 注销对用户已生效（Cookie 已清），但会话残留等于没注销，必须留痕
		log.Printf("注销会话失败: %v", err)
	}
	setSessionCookie(c, "", -1) // 清 Cookie（原生下载通道一并失效）
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// Me 返回当前登录用户（含角色）。头像不再随包（6）：data URL 可达 300KB，
// 每次进页面全量传输是首页 me 请求 17s 的主因，改走独立 /auth/avatar 短缓存。
func (h *AuthHandler) Me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"username": c.GetString("username"),
		"role": c.GetString("role")})
}

// Avatar 返回当前会话用户的头像 data URL（独立于 /auth/me）：private 短缓存
// 让后续页面加载不再回源；前端上传/清除头像时递增 avatarVer 版本参数变 URL
// 破缓存。44 起按用户隔离，键名带用户 ID。
func (h *AuthHandler) Avatar(c *gin.Context) {
	u := c.MustGet("user").(*store.User)
	avatar, _, _ := h.st.GetSetting(store.AvatarSettingKey(u.ID))
	c.Header("Cache-Control", "private, max-age=300")
	c.JSON(http.StatusOK, gin.H{"avatar": avatar})
}

type updateAvatarReq struct {
	Avatar string `json:"avatar"` // data:image/...;base64,...（前端压缩 ≤300KB）；空串 = 清除
}

// UpdateAvatar 设置/清除当前会话用户的头像：data URL 存 settings 用户级键
// （44 每用户独立，登录组放行——受限用户同样可配置；随库备份走）。
func (h *AuthHandler) UpdateAvatar(c *gin.Context) {
	var req updateAvatarReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	u := c.MustGet("user").(*store.User)
	key := store.AvatarSettingKey(u.ID)
	v := strings.TrimSpace(req.Avatar)
	if v == "" {
		if err := h.st.DeleteSetting(key); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "清除头像失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0})
		return
	}
	if len(v) > 400*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "头像图片过大，请换小一点的图片"})
		return
	}
	if !strings.HasPrefix(v, "data:image/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "头像须为图片 data URL"})
		return
	}
	if err := h.st.SetSetting(key, v); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存头像失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// totpView 自助两步验证状态（45 按用户隔离，操作对象恒为当前会话用户）。
func (h *AuthHandler) TOTPStatus(c *gin.Context) {
	u := c.MustGet("user").(*store.User)
	c.JSON(http.StatusOK, gin.H{
		"enabled": totp.Enabled(h.st, u.ID),
		"pending": totp.Pending(h.st, u.ID),
	})
}

// TOTPSetup 生成当前用户的两步验证待确认密钥：返回 Base32 密钥、otpauth URI
// 与二维码 data URL（前端展示，用户扫码/手输后用验证码确认启用）。
func (h *AuthHandler) TOTPSetup(c *gin.Context) {
	u := c.MustGet("user").(*store.User)
	secret, uri, err := totp.StartEnrollment(h.st, u.ID, u.Username)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	png, err := qrcode.Encode(uri, qrcode.Medium, 220)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成二维码失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"secret": secret, "uri": uri,
		"qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

type totpCodeReq struct {
	Code string `json:"code" binding:"required"`
}

// TOTPEnable 校验当前用户待确认密钥的验证码，通过即启用两步验证。
// 验码失败计入账号维度限速（会话被盗者可借此端点无限速爆破验证码）。
func (h *AuthHandler) TOTPEnable(c *gin.Context) {
	u := c.MustGet("user").(*store.User)
	if !h.userGuard.check(c, "u:"+u.Username) {
		return
	}
	var req totpCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入验证码"})
		return
	}
	if err := totp.ConfirmEnrollment(h.st, u.ID, req.Code); err != nil {
		h.userGuard.fail("u:" + u.Username)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.userGuard.success("u:" + u.Username)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// TOTPDisable 关闭当前用户的两步验证（需验证码确认，防止拿到已登录会话的人直接关掉）。
func (h *AuthHandler) TOTPDisable(c *gin.Context) {
	u := c.MustGet("user").(*store.User)
	if !h.userGuard.check(c, "u:"+u.Username) {
		return
	}
	var req totpCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入验证码"})
		return
	}
	if err := totp.Disable(h.st, u.ID, req.Code); err != nil {
		h.userGuard.fail("u:" + u.Username)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.userGuard.success("u:" + u.Username)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

type updateProfileReq struct {
	Username    string `json:"username" binding:"required,min=1,max=100"` // 新用户名（可与当前相同=不改）
	OldPassword string `json:"old_password" binding:"required"`           // 旧密码：任何修改都要验
	NewPassword string `json:"new_password" binding:"omitempty,max=72"`   // 新密码；空=只改用户名（bcrypt 只取前 72 字节）
}

// UpdateProfile 修改个人信息（用户名/密码）：验证旧密码后按需更新。
// 改用户名不掉当前会话（会话按 user_id 关联）；改密码保留当前会话、踢出其他端。
// 旧密码校验失败计入账号维度限速（防会话被盗者无限速爆破旧密码）。
// 响应回传最终用户名，前端更新本地显示。
func (h *AuthHandler) UpdateProfile(c *gin.Context) {
	uAny, _ := c.Get("user")
	u, _ := uAny.(*store.User)
	if !h.userGuard.check(c, "u:"+u.Username) {
		return
	}
	var req updateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（用户名必填，最长 100 字）"})
		return
	}
	if req.NewPassword != "" && len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码至少 8 位"})
		return
	}

	cur, err := h.st.UserByID(u.ID)
	if err != nil || cur == nil ||
		bcrypt.CompareHashAndPassword([]byte(cur.PasswordHash), []byte(req.OldPassword)) != nil {
		h.userGuard.fail("u:" + u.Username)
		c.JSON(http.StatusBadRequest, gin.H{"error": "旧密码错误"})
		return
	}
	h.userGuard.success("u:" + u.Username)
	username := strings.TrimSpace(req.Username)
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名不能为空"})
		return
	}
	if username != cur.Username {
		if err := h.st.UpdateUsername(cur.ID, username); err != nil {
			if errors.Is(err, store.ErrUsernameExists) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "用户名已存在"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新用户名失败"})
			return
		}
	}
	if req.NewPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "生成密码哈希失败"})
			return
		}
		if err := h.st.UpdatePassword(cur.ID, string(hash)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新密码失败"})
			return
		}
		// 改密后踢出其他端，保留当前会话（失败=其他端还拿着旧密码的会话，必须留痕）
		if err := h.st.DeleteOtherSessions(cur.ID, bearerToken(c)); err != nil {
			log.Printf("改密后清理其他会话失败: %v", err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "username": username})
}

// authRequired 校验会话：Authorization 头优先（XHR/fetch），HttpOnly Cookie
// 兜底（浏览器原生下载的导航请求带不了头）。两个通道验的是同一张会话表，
// 登出/改密踢会话对两者同时生效。
func authRequired(st *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			if ck, err := c.Cookie(sessionCookie); err == nil {
				token = ck
			}
		}
		u, err := st.SessionUser(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "会话校验失败"})
			return
		}
		if u == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "未登录或会话已过期"})
			return
		}
		c.Set("user", u)
		c.Set("username", u.Username)
		c.Set("role", u.Role)
		c.Next()
	}
}
