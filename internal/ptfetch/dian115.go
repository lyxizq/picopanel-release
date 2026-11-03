package ptfetch

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 癫影（m.dian115.com，MADOW）专用链路（416，2026-10-05 逆向实测后实现）。
// 该站不是 NexusPHP：自研网关对所有 /api/portal/* 请求强制两层校验——
//  1. X-Portal-Browser-Proof：GET /auth/browser-challenge 免登录领取（TTL 600s，透传即可）；
//  2. ECDSA P-256 请求签名：本地生成密钥对，POST /auth/browser-session 提交公钥
//     注册，之后每个请求对 "portal-browser-request/v1\nMETHOD\n路径\n时间戳ms\nnonce"
//     做 SHA-256 摘要签名（只签路径，query/body 不参与），经 X-Portal-Browser-TS /
//     -Nonce / -Sig 三头携带。密钥绑定定位靠服务端 Set-Cookie 下发的
//     __Host-portal_browser——客户端必须带 cookie jar，否则注册的公钥找不到落点，
//     签名恒判无效（实测踩坑）。
//
// Cloudflare 侧拦「声称 Chrome 却不带 sec-ch-ua 头」的客户端，Go 默认 TLS +
// Chrome UA + 全套 sec-ch/Sec-Fetch 头即可通过（2026-10-05 矩阵实测，无需 uTLS；
// 模块其余站刻意不带 Sec-Fetch-* 的红线不适用于本站）。
//
// 凭据：portal_token（JWT）。用户粘整串浏览器 Cookie 亦可，全部播种进 jar，
// 匿名浏览器 ID 由服务端下发接管。

const (
	dianPathChallenge = "/api/portal/auth/browser-challenge"
	dianPathSession   = "/api/portal/auth/browser-session"
	dianPathMe        = "/api/portal/me"
	dianPathSignin    = "/api/portal/signin"
)

// dianTZ 站点业务时区（签到日界按东八区；created_at/last_signin_date 均 +08:00）。
var dianTZ = time.FixedZone("UTC+8", 8*3600)

// dianExtra 癫影扩展快照段（挂在 Snapshot.Dian，其余站点为 nil）。
type DianExtra struct {
	Streak      int64   `json:"streak"`       // 连续签到天数（0=未知）
	LastSignin  string  `json:"last_signin"`  // 最近签到日期（YYYY-MM-DD，空=未知）
	SignedToday bool    `json:"signed_today"` // 今日（东八区）已签到
	VIP         bool    `json:"vip"`
	Award       float64 `json:"award"` // 当日签到得分（454：面板签到那次响应的 award，负数=倒霉签；网页手动签/未签=0，me 不回传该信息）
}

// dianUser /api/portal/me 的 user 对象（只取展示需要的字段）。
type dianUser struct {
	Nickname          string  `json:"nickname"`
	Points            float64 `json:"points"`
	ConsecutiveSignin int64   `json:"consecutive_signin"`
	LastSigninDate    string  `json:"last_signin_date"`
	VIP               bool    `json:"vip"`
	CreatedAt         string  `json:"created_at"`
}

// dianResp 通用响应壳（业务错误藏 HTTP 200/403 的 code 字段里，须带出 msg）。
type dianResp struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

// dianClient 单次抓取/签到的签名会话（一次性用完即弃，密钥不入持久层）。
type dianClient struct {
	hc    *http.Client
	base  string
	ua    string
	proof string
	key   *ecdsa.PrivateKey
	skew  int64 // 服务端时钟 - 本机时钟（毫秒）
}

func newDianClient(base, cookie, ua string) (*dianClient, error) {
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
	// jar 必须挂上：portal_token 是身份，服务端下发的 __Host-portal_browser 是
	// 密钥绑定定位键——没有 jar 两者都发不出去，签名恒判无效
	hc := sameHostClient()
	hc.Jar = jar
	return &dianClient{hc: hc, base: base, ua: ua}, nil
}

// setHeaders 浏览器一致性请求头（过 Cloudflare 的关键：声称 Chrome 就得带全套
// sec-ch-ua / Sec-Fetch 头）。
func (c *dianClient) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.6")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("sec-ch-ua", `"Not/A)Brand";v="8", "Chromium";v="126", "Google Chrome";v="126"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"macOS"`)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Referer", c.base+"/")
}

// do 发请求（自动带上 jar 里的 cookie）并读响应体。非 200 也返回体——网关的
// browser_proof_* 业务错误就在 403 体里。
func (c *dianClient) do(method, path string, body []byte) (int, []byte, error) {
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

// bootstrap 领 proof → 生成密钥 → 注册 browser-session（校服务端时钟偏移）。
func (c *dianClient) bootstrap() error {
	st, b, err := c.do(http.MethodGet, dianPathChallenge, nil)
	if err != nil {
		return err
	}
	var ch struct {
		dianResp
		Enabled bool   `json:"enabled"`
		Proof   string `json:"proof"`
	}
	if err := json.Unmarshal(b, &ch); err != nil {
		return fmt.Errorf("解析 challenge 响应失败（HTTP %d）: %w", st, err)
	}
	if st != http.StatusOK || ch.Code != "ok" || ch.Proof == "" {
		return fmt.Errorf("获取浏览器证明失败（HTTP %d code=%s）: %s", st, ch.Code, ch.Msg)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	b64 := func(v *big.Int) string { return base64.RawURLEncoding.EncodeToString(v.FillBytes(make([]byte, 32))) }
	body, _ := json.Marshal(map[string]any{"public_jwk": map[string]string{
		"kty": "EC", "crv": "P-256", "x": b64(key.X), "y": b64(key.Y),
	}})
	req, err := http.NewRequest(http.MethodPost, c.base+dianPathSession, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("X-Portal-Browser-Proof", ch.Proof)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("注册浏览器会话失败: %w", err)
	}
	defer resp.Body.Close()
	sb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var sess struct {
		dianResp
		Enabled      bool   `json:"enabled"`
		ServerTimeMs int64  `json:"server_time_ms"`
		ExpiresAt    string `json:"expires_at"`
	}
	_ = json.Unmarshal(sb, &sess)
	if resp.StatusCode != http.StatusOK || sess.Code != "ok" {
		return fmt.Errorf("注册浏览器会话失败（HTTP %d code=%s）: %s", resp.StatusCode, sess.Code, sess.Msg)
	}
	if sess.ServerTimeMs > 0 {
		c.skew = sess.ServerTimeMs - time.Now().UnixMilli()
	}
	c.proof, c.key = ch.Proof, key
	return nil
}

// signHeaders 构造请求签名四件套（证明 + 时间戳 + nonce + ECDSA 签名）。
// 签名串与前端 xa()/ba() 完全一致：只覆盖「/api/portal 前缀的完整路径」，
// query 与 body 不参与；时间戳为毫秒并按 server_time_ms 校偏。
func (c *dianClient) signHeaders(method, path string) http.Header {
	ts := strconv.FormatInt(time.Now().UnixMilli()+c.skew, 10)
	nonceRaw := make([]byte, 24)
	_, _ = rand.Read(nonceRaw)
	nonce := base64.RawURLEncoding.EncodeToString(nonceRaw)
	payload := fmt.Sprintf("portal-browser-request/v1\n%s\n%s\n%s\n%s", strings.ToUpper(method), path, ts, nonce)
	sum := sha256.Sum256([]byte(payload))
	r, s, err := ecdsa.Sign(rand.Reader, c.key, sum[:])
	if err != nil {
		return nil
	}
	sig := make([]byte, 64) // WebCrypto 同款 IEEE P1363 r||s 定宽格式
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	h := http.Header{}
	h.Set("X-Portal-Browser-Proof", c.proof)
	h.Set("X-Portal-Browser-TS", ts)
	h.Set("X-Portal-Browser-Nonce", nonce)
	h.Set("X-Portal-Browser-Sig", base64.RawURLEncoding.EncodeToString(sig))
	return h
}

// signed 需签名请求：proof/签名失效（服务端吊销/过期）时自动重注册重试一次，
// 口径同站方前端拦截器。
func (c *dianClient) signed(method, path string, body []byte) (int, []byte, error) {
	for attempt := 0; ; attempt++ {
		if attempt == 1 {
			if err := c.bootstrap(); err != nil {
				return 0, nil, err
			}
		}
		hdr := c.signHeaders(method, path)
		if hdr == nil {
			return 0, nil, errors.New("生成请求签名失败")
		}
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, c.base+path, rd)
		if err != nil {
			return 0, nil, err
		}
		c.setHeaders(req)
		for k, vs := range hdr {
			for _, v := range vs {
				req.Header.Set(k, v)
			}
		}
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
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return resp.StatusCode, nil, fmt.Errorf("读取响应失败: %w", err)
		}
		if resp.StatusCode == http.StatusForbidden {
			var er dianResp
			if json.Unmarshal(b, &er) == nil && (er.Code == "browser_proof_required" || er.Code == "browser_proof_invalid") && attempt == 0 {
				continue
			}
		}
		return resp.StatusCode, b, nil
	}
}

// me 拉当前用户信息。
func (c *dianClient) me() (*dianUser, error) {
	st, b, err := c.signed(http.MethodGet, dianPathMe, nil)
	if err != nil {
		return nil, err
	}
	if st == http.StatusUnauthorized {
		return nil, errors.New("登录态已失效（portal_token 过期或被吊销），请重新粘贴 Cookie")
	}
	var out struct {
		dianResp
		User *dianUser `json:"user"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("解析用户信息失败（HTTP %d）: %w", st, err)
	}
	if st != http.StatusOK || out.Code != "ok" || out.User == nil {
		return nil, fmt.Errorf("获取用户信息失败（HTTP %d code=%s）: %s", st, out.Code, out.Msg)
	}
	return out.User, nil
}

// signinPost 执行签到，返回结果文案与本次得分 award（454 进快照上卡）。
// already_signed 视为正常结果（自动签到的并发兜底路径），不报错。
func (c *dianClient) signinPost(mode string) (string, float64, error) {
	body, _ := json.Marshal(map[string]string{"mode": mode})
	st, b, err := c.signed(http.MethodPost, dianPathSignin, body)
	if err != nil {
		return "", 0, err
	}
	var out struct {
		dianResp
		Award *float64 `json:"award"`
	}
	_ = json.Unmarshal(b, &out)
	if st == http.StatusConflict || out.Code == "already_signed" {
		return "今日已签到", 0, nil
	}
	if st != http.StatusOK || out.Code != "ok" {
		return "", 0, fmt.Errorf("签到失败（HTTP %d code=%s）: %s", st, out.Code, out.Msg)
	}
	switch {
	case out.Award == nil:
		return "签到完成", 0, nil
	case *out.Award > 0:
		return fmt.Sprintf("签到成功 +%d 积分", int64(*out.Award)), *out.Award, nil
	case *out.Award == 0:
		return "签到完成，空签", 0, nil
	default:
		return fmt.Sprintf("签到完成，倒霉签 %d 积分", int64(*out.Award)), *out.Award, nil
	}
}

// dianToday 站点业务时区的今天（签到日界）。
func dianToday() string { return time.Now().In(dianTZ).Format("2006-01-02") }

// dianBaseURL 站点基址：配置优先，空回落目录默认域（与 FetchSite 其他 kind 的
// 口径一致——此前 dian 链路自行取 s.BaseURL，空配置会打出无 scheme 的 URL）。
func dianBaseURL(s Site) string {
	if b := normalizeBaseURL(s.BaseURL); b != "" {
		return b
	}
	if def := SiteDefByKey(s.SiteKey); def != nil && def.URL != "" {
		return normalizeBaseURL(def.URL)
	}
	return ""
}

// fetchDian115 拉快照；SigninMode 配置了 normal/lucky 且今日未签时顺带自动签到
// （每日 06:00 轮与手动刷新共用）。签到后重拉一次 /me 让积分反映到账后余额。
func fetchDian115(s Site) (*Snapshot, error) {
	snap, _, err := dianRun(s, false)
	return snap, err
}

// SigninDian115 手动签到（强制执行，模式取配置，空配置回落 normal 普通档），
// 返回签到结果文案供界面提示。
func SigninDian115(s Site) (*Snapshot, string, error) {
	return dianRun(s, true)
}

func dianRun(s Site, forceSignin bool) (*Snapshot, string, error) {
	mode := s.SigninMode
	if forceSignin && mode != "normal" && mode != "lucky" {
		mode = "normal"
	}
	autoSign := !forceSignin && (mode == "normal" || mode == "lucky")

	base := dianBaseURL(s)
	if base == "" {
		return nil, "", fmt.Errorf("缺少站点地址（base_url 为空且站点不在目录内）")
	}
	c, err := newDianClient(base, s.Cookie, s.UA)
	if err != nil {
		return nil, "", err
	}
	if err := c.bootstrap(); err != nil {
		return nil, "", err
	}
	u, err := c.me()
	if err != nil {
		return nil, "", err
	}
	msg := ""
	award := float64(0)
	if autoSign || forceSignin {
		if u.LastSigninDate != "" && u.LastSigninDate == dianToday() {
			msg = "今日已签到"
		} else {
			if msg, award, err = c.signinPost(mode); err != nil {
				return nil, "", err
			}
			if u2, err := c.me(); err == nil {
				u = u2 // 积分/连签状态取到账后的
			}
		}
	}
	snap := &Snapshot{
		Username: u.Nickname,
		Level:    "会员",
		Bonus:    u.Points,
		Dian: &DianExtra{
			Streak:      u.ConsecutiveSignin,
			LastSignin:  u.LastSigninDate,
			SignedToday: u.LastSigninDate == dianToday(),
			VIP:         u.VIP,
			Award:       award,
		},
	}
	if snap.Dian.VIP {
		snap.Level = "VIP"
	}
	if t, err := time.Parse(time.RFC3339, u.CreatedAt); err == nil {
		snap.JoinedAt = t.Unix()
	}
	return snap, msg, nil
}
