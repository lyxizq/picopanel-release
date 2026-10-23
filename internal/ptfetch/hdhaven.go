package ptfetch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

// HDHaven（hdhaven.com，栖影）专用链路（432，2026-10-05 接入）。该站是 Next.js
// 自研社区（TMDB 影视资料 + 网盘资源分享），非 NexusPHP：整站登录墙，JSON API
// 走 {success, data, message, code} 信封，会话 = hdh_session Cookie（same-origin）。
// 登录有 Cloudflare Turnstile 人机验证 + 可选 TOTP（缺它 428），自动化登录不
// 现实——只支持 Cookie 回放（绝不 POST 登录，同模块行为红线）。
//
// 接口与解析口径参考 MoviePilot 社区插件（github.com/odomu/MoviePilot-Plugins
// plugins.v2/cloudsubscribe/search/hdhaven，2026-10-05 快照）：
//
//	GET  /api/auth/me                 用户信息（points 可能是数字或 {available:N}）
//	POST /api/account/checkin         普通签到（409 = 今日已签；data.earned 可空）
//	POST /api/account/checkin/gamble  赌狗签到（earned 可为负 = 倒扣）
//
// 频控：访问过频时信封回 code=SCRAPE_CHALLENGE_REQUIRED（站点弹图片验证码，
// 原操作不执行），429 = 明确限流。签到一天一次 + 无重试的量级碰不到，命中即
// 报错不硬闯（过验证码需要人，这不是客户端能补的事）。

const (
	hdhPathMe      = "/api/auth/me"
	hdhPathCheckin = "/api/account/checkin"
	hdhPathGamble  = "/api/account/checkin/gamble"
)

// HdhExtra HDHaven 扩展快照段（挂在 Snapshot.Hdh，其余站点 nil）。
type HdhExtra struct {
	SignedToday   bool    `json:"signed_today"`   // 今日已签到（站点口径，信其 API 不本地算日界）
	LastCheckin   string  `json:"last_checkin"`   // 站点原样返回（暂不展示，留档）
	VIP           bool    `json:"vip"`            // 会员（is_member）
	Level         string  `json:"level"`          // 成长等级名（level.name，453；空=站点未给）
	CheckinReward float64 `json:"checkin_reward"` // 当日签到得分（checkin_reward，453；未签/未给=0）
}

// hdhPoints 积分字段兼容：服务端版本间返回过数字与 {available:N} 两种形态，
// 一并吃下（MoviePilot 插件同款防御）。
type hdhPoints float64

func (p *hdhPoints) UnmarshalJSON(b []byte) error {
	var num float64
	if err := json.Unmarshal(b, &num); err == nil {
		*p = hdhPoints(num)
		return nil
	}
	var obj struct {
		Available *float64 `json:"available"`
	}
	if err := json.Unmarshal(b, &obj); err == nil && obj.Available != nil {
		*p = hdhPoints(*obj.Available)
		return nil
	}
	return fmt.Errorf("无法识别的积分形态: %s", string(b))
}

// hdhUser /api/auth/me 的 data 对象（只取展示需要的字段）。
type hdhUser struct {
	ID            string    `json:"id"`
	Nickname      string    `json:"nickname"`
	Username      string    `json:"username"`
	Points        hdhPoints `json:"points"`
	LevelName     string    `json:"-"`
	IsMember      bool      `json:"is_member"`
	CheckinToday  bool      `json:"checkin_today"`
	CheckinReward float64   `json:"checkin_reward"`
	LastCheckin   string    `json:"last_checkin"`
}

func (u *hdhUser) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID       string    `json:"id"`
		Nickname string    `json:"nickname"`
		Username string    `json:"username"`
		Points   hdhPoints `json:"points"`
		Level    *struct {
			Name string `json:"name"`
		} `json:"level"`
		IsMember      bool    `json:"is_member"`
		CheckinToday  bool    `json:"checkin_today"`
		CheckinReward float64 `json:"checkin_reward"`
		LastCheckin   string  `json:"last_checkin"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*u = hdhUser{ID: raw.ID, Nickname: raw.Nickname, Username: raw.Username,
		Points: raw.Points, IsMember: raw.IsMember, CheckinToday: raw.CheckinToday,
		CheckinReward: raw.CheckinReward, LastCheckin: raw.LastCheckin}
	if raw.Level != nil {
		u.LevelName = raw.Level.Name
	}
	return nil
}

// hdhResp 通用响应壳（业务错误可能藏 HTTP 200 的 success=false 里，message 带出）。
type hdhResp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

// hdhErr 带状态码的业务错误（签到 409 已签的容忍判断用）。
type hdhErr struct {
	msg    string
	status int
	code   string
}

func (e *hdhErr) Error() string { return e.msg }

// hdhClient 单次抓取/签到的会话（一次性用完即弃；jar 只为在同一轮内跟随服务端
// 可能的 Set-Cookie 会话轮换，凭据不入持久层）。
type hdhClient struct {
	hc   *http.Client
	base string
	ua   string
}

func newHDHavenClient(base, cookie, ua string) (*hdhClient, error) {
	if ua == "" {
		ua = DefaultUA
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	if cu, err := url.Parse(base + "/"); err == nil {
		var seeds []*http.Cookie
		for _, kv := range strings.Split(CleanCookie(cookie), "; ") {
			name, val, ok := strings.Cut(kv, "=")
			if !ok || strings.TrimSpace(name) == "" {
				continue
			}
			seeds = append(seeds, &http.Cookie{Name: strings.TrimSpace(name), Value: strings.TrimSpace(val), Path: "/"})
		}
		jar.SetCookies(cu, seeds)
	}
	// jar 必须挂上（dian115 同款坑）：既发送用户凭据，也跟随本轮内服务端可能的
	// Set-Cookie 会话轮换
	hc := sameHostClient()
	hc.Jar = jar
	return &hdhClient{hc: hc, base: base, ua: ua}, nil
}

// setHeaders JSON API 请求头。2026-10-11 起站点对网页接口做浏览器判定，缺
// Sec-Fetch-*/Sec-CH-UA 头直接 403（「网页接口仅供浏览器访问」）；头集合对齐
// odomu 插件同款 Chrome ajax 形态（TLS 指纹 Go 标准库模拟不了，头是必要条件
// 不保证充分——彻底解法是站点的 OPENAPI 授权，见 TODO 533）。
func (c *hdhClient) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.6")
	req.Header.Set("Referer", c.base+"/")
	req.Header.Set("Origin", c.base)
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-CH-UA", `"Chromium";v="128", "Not;A=Brand";v="24", "Google Chrome";v="128"`)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
}

// do 发请求（cookie 走 jar）并读响应体。非 200 也返回体——401 已签 409 等语义
// 都在状态码/体里。
func (c *hdhClient) do(method, path string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	c.setHeaders(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if errors.Is(err, errCrossHostRedirect) {
			return 0, nil, errCrossHostRedirect
		}
		return 0, nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取响应失败: %w", err)
	}
	return resp.StatusCode, b, nil
}

// wrapErr 统一错误翻译：401=登录态失效，挑战码=频控验证码，其余带 HTTP 状态。
func hdhWrapErr(st int, b []byte, what string) error {
	var er hdhResp
	_ = json.Unmarshal(b, &er)
	switch {
	case st == http.StatusUnauthorized:
		return &hdhErr{msg: "登录态已失效（hdh_session 过期或已退出登录），请重新粘贴 Cookie", status: st, code: er.Code}
	case er.Code == "SCRAPE_CHALLENGE_REQUIRED":
		return &hdhErr{msg: "触发站点频控验证码，请稍后在网页端完成验证后再试", status: st, code: er.Code}
	case er.Message != "":
		return &hdhErr{msg: fmt.Sprintf("%s失败（HTTP %d）: %s", what, st, er.Message), status: st, code: er.Code}
	default:
		return &hdhErr{msg: fmt.Sprintf("%s失败（HTTP %d）", what, st), status: st}
	}
}

// me 拉当前用户信息。
func (c *hdhClient) me() (*hdhUser, error) {
	st, b, err := c.do(http.MethodGet, hdhPathMe, nil)
	if err != nil {
		return nil, err
	}
	if st != http.StatusOK {
		return nil, hdhWrapErr(st, b, "获取用户信息")
	}
	var out struct {
		hdhResp
		Data *hdhUser `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("解析用户信息失败（HTTP %d）: %w", st, err)
	}
	if !out.Success || out.Data == nil {
		return nil, hdhWrapErr(st, b, "获取用户信息")
	}
	return out.Data, nil
}

// checkinPost 执行签到，返回结果文案。409 已签视为正常结果（自动签到的并发
// 兜底路径），不报错；赌狗签到 earned 可为负。
func (c *hdhClient) checkinPost(gamble bool) (string, error) {
	path := hdhPathCheckin
	name := "签到"
	if gamble {
		path = hdhPathGamble
		name = "赌狗签到"
	}
	st, b, err := c.do(http.MethodPost, path, []byte("{}"))
	if err != nil {
		return "", err
	}
	if st == http.StatusConflict {
		return "今日已签到", nil
	}
	if st != http.StatusOK && st != http.StatusCreated {
		return "", hdhWrapErr(st, b, name)
	}
	var out struct {
		hdhResp
		Data struct {
			Earned  *float64 `json:"earned"`
			Points  *float64 `json:"points"`
			Outcome string   `json:"outcome"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("解析签到结果失败（HTTP %d）: %w", st, err)
	}
	if !out.Success {
		return "", hdhWrapErr(st, b, name)
	}
	switch {
	case out.Data.Earned == nil:
		return name + "完成", nil
	case *out.Data.Earned > 0:
		return fmt.Sprintf("%s成功 +%d 积分", name, int64(*out.Data.Earned)), nil
	case *out.Data.Earned == 0:
		return name + "完成，空签", nil
	default:
		return fmt.Sprintf("%s完成，倒扣 %d 积分", name, int64(-*out.Data.Earned)), nil
	}
}

// hdhBaseURL 站点基址：配置优先，空回落目录默认域（口径同 dianBaseURL）。
func hdhBaseURL(s Site) string {
	if b := normalizeBaseURL(s.BaseURL); b != "" {
		return b
	}
	if def := SiteDefByKey(s.SiteKey); def != nil && def.URL != "" {
		return normalizeBaseURL(def.URL)
	}
	return ""
}

// fetchHDHaven 拉快照；SigninMode 配置了 normal/lucky 且今日未签时顺带自动签到
// （每日 06:00 轮与手动刷新共用；lucky=dian115 运气签 / hdhaven 赌狗签）。
// 签到后重拉一次 /me 让积分反映到账后余额。
func fetchHDHaven(s Site) (*Snapshot, error) {
	snap, _, err := hdhRun(s, false)
	return snap, err
}

// SigninHDHaven 手动签到（强制执行，模式取配置，空配置回落 normal 普通档），
// 返回签到结果文案供界面提示。
func SigninHDHaven(s Site) (*Snapshot, string, error) {
	return hdhRun(s, true)
}

func hdhRun(s Site, forceSignin bool) (*Snapshot, string, error) {
	mode := s.SigninMode
	if forceSignin && mode != "normal" && mode != "lucky" {
		mode = "normal"
	}
	autoSign := !forceSignin && (mode == "normal" || mode == "lucky")

	base := hdhBaseURL(s)
	if base == "" {
		return nil, "", fmt.Errorf("缺少站点地址（base_url 为空且站点不在目录内）")
	}
	c, err := newHDHavenClient(base, s.Cookie, s.UA)
	if err != nil {
		return nil, "", err
	}
	u, err := c.me()
	if err != nil {
		return nil, "", err
	}
	msg := ""
	if autoSign || forceSignin {
		if u.CheckinToday {
			msg = "今日已签到"
		} else {
			if msg, err = c.checkinPost(mode == "lucky"); err != nil {
				return nil, "", err
			}
			if u2, err := c.me(); err == nil {
				u = u2 // 积分/已签状态取到账后的
			}
		}
	}
	snap := &Snapshot{
		Username: u.Nickname,
		Bonus:    float64(u.Points),
		Hdh: &HdhExtra{
			SignedToday:   u.CheckinToday,
			LastCheckin:   u.LastCheckin,
			VIP:           u.IsMember,
			Level:         u.LevelName,
			CheckinReward: u.CheckinReward,
		},
	}
	switch {
	case u.IsMember:
		snap.Level = "VIP"
	case u.LevelName != "":
		snap.Level = u.LevelName
	default:
		snap.Level = "普通用户"
	}
	if snap.Username == "" {
		snap.Username = u.Username
	}
	return snap, msg, nil
}
