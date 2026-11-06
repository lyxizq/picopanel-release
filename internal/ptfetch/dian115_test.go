package ptfetch

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// 癫影链路测试（416）：桩出自研网关四个端点，服务端视角校验 proof/请求签名
// 链路（ECDSA P-256 验签）+ 自动签到分支与重试语义。

// dianStub 癫影网关桩：记录注册的公钥与签到调用，回放用户数据。
type dianStub struct {
	mu             sync.Mutex
	pub            *ecdsa.PublicKey
	proof          string
	signed         bool // 签名请求是否到过（proof 换发后仍应到）
	signinCalls    int
	lastSigninDate string
	points         float64
	streak         int64
	failProofOnce  bool // 首个签名请求回 403 browser_proof_invalid（测重注册重试）
	badToken       bool // /me 回 401 no_token（测登录态失效文案）
	signinAward    *int // 签到 award（nil=默认 +10）
	signinConflict bool // 签到回 409 already_signed
	noSecCh        bool // 断言用：缺 sec-ch-ua 头时置位
}

func newDianStub(t *testing.T) (*httptest.Server, *dianStub) {
	t.Helper()
	s := &dianStub{lastSigninDate: time.Now().In(dianTZ).AddDate(0, 0, -1).Format("2006-01-02"), points: 2823, streak: 36}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/portal/auth/browser-challenge", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("sec-ch-ua") == "" {
			s.mu.Lock()
			s.noSecCh = true
			s.mu.Unlock()
		}
		s.mu.Lock()
		s.proof = fmt.Sprintf("stub-proof-%d", time.Now().UnixNano())
		proof := s.proof
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"code": "ok", "enabled": true, "proof": proof, "ttl": 600})
	})
	mux.HandleFunc("/api/portal/auth/browser-session", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		proof := s.proof
		s.mu.Unlock()
		if r.Header.Get("X-Portal-Browser-Proof") != proof {
			writeJSON(w, 403, map[string]any{"code": "browser_proof_required", "msg": "browser proof required"})
			return
		}
		var body struct {
			PublicJWK struct {
				Kty string `json:"kty"`
				Crv string `json:"crv"`
				X   string `json:"x"`
				Y   string `json:"y"`
			} `json:"public_jwk"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		x, _ := base64.RawURLEncoding.DecodeString(body.PublicJWK.X)
		y, _ := base64.RawURLEncoding.DecodeString(body.PublicJWK.Y)
		s.mu.Lock()
		s.pub = &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"code": "ok", "enabled": true, "ttl": 1800, "server_time_ms": time.Now().UnixMilli()})
	})
	mux.HandleFunc("/api/portal/me", func(w http.ResponseWriter, r *http.Request) {
		if !s.verifySigned(t, r) {
			writeJSON(w, 403, map[string]any{"code": "browser_proof_invalid", "msg": "browser request signature invalid"})
			return
		}
		s.mu.Lock()
		bad := s.badToken
		date, points, streak := s.lastSigninDate, s.points, s.streak
		s.mu.Unlock()
		if bad {
			writeJSON(w, 401, map[string]any{"code": "no_token", "msg": "缺少令牌"})
			return
		}
		writeJSON(w, 200, map[string]any{"code": "ok", "user": map[string]any{
			"nickname": "lyxizq", "points": points, "consecutive_signin": streak,
			"last_signin_date": date, "vip": false, "created_at": "2026-08-10T23:15:16.655486+08:00",
		}})
	})
	mux.HandleFunc("/api/portal/signin", func(w http.ResponseWriter, r *http.Request) {
		if !s.verifySigned(t, r) {
			writeJSON(w, 403, map[string]any{"code": "browser_proof_invalid", "msg": "browser request signature invalid"})
			return
		}
		s.mu.Lock()
		s.signinCalls++
		conflict, award := s.signinConflict, 10
		if s.signinAward != nil {
			award = *s.signinAward
		}
		if !conflict {
			s.lastSigninDate = time.Now().In(dianTZ).Format("2006-01-02")
			s.points += float64(award)
			s.streak++
		}
		s.mu.Unlock()
		if conflict {
			writeJSON(w, 409, map[string]any{"code": "already_signed", "msg": "今日已签到"})
			return
		}
		writeJSON(w, 200, map[string]any{"code": "ok", "award": award})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, s
}

// verifySigned 服务端视角验证请求签名（与站方校验同构：proof/TS/Nonce/Sig 四头
// 齐全 + 对 portal-browser-request/v1 串的 ECDSA P-256 验签通过）。
func (s *dianStub) verifySigned(t *testing.T, r *http.Request) bool {
	t.Helper()
	ts, nonce, sigB64 := r.Header.Get("X-Portal-Browser-TS"), r.Header.Get("X-Portal-Browser-Nonce"), r.Header.Get("X-Portal-Browser-Sig")
	s.mu.Lock()
	proof, pub, fail := s.proof, s.pub, s.failProofOnce
	s.failProofOnce = false // 只拦首个签名请求（客户端重注册后放行）
	s.mu.Unlock()
	if r.Header.Get("X-Portal-Browser-Proof") != proof || ts == "" || nonce == "" || sigB64 == "" || pub == nil {
		return false
	}
	if fail { // 桩要求本轮签名必败（模拟服务端吊销），验签环节直接不通过
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != 64 {
		return false
	}
	payload := fmt.Sprintf("portal-browser-request/v1\n%s\n%s\n%s\n%s", r.Method, r.URL.Path, ts, nonce)
	sum := sha256.Sum256([]byte(payload))
	ok := ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
	if ok {
		s.mu.Lock()
		s.signed = true
		s.mu.Unlock()
	}
	return ok
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func dianSite(base, mode string) Site {
	return Site{SiteKey: "dian115", BaseURL: base, Cookie: "portal_token=stub.jwt.token; __Host-portal_browser=seeded-id; junk", UA: "", SigninMode: mode}
}

func TestFetchDian115AutoSignin(t *testing.T) {
	srv, stub := newDianStub(t)
	snap, err := FetchSite(dianSite(srv.URL, "normal"))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if stub.noSecCh {
		t.Fatal("请求缺少 sec-ch-ua 头（过 Cloudflare 的关键头未带）")
	}
	if !stub.signed || stub.signinCalls != 1 {
		t.Fatalf("签名请求/自动签到不符：signed=%v signin=%d", stub.signed, stub.signinCalls)
	}
	if snap.Username != "lyxizq" || snap.Bonus != 2833 {
		t.Fatalf("快照用户/积分不符: %+v", snap)
	}
	if snap.Dian == nil || !snap.Dian.SignedToday || snap.Dian.Streak != 37 || snap.Dian.VIP {
		t.Fatalf("癫影扩展段不符: %+v", snap.Dian)
	}
	// 454：面板签到那次把响应 award 记进快照（+10）
	if snap.Dian.Award != 10 {
		t.Fatalf("签到得分应入快照: %+v", snap.Dian)
	}
	if snap.Level != "会员" || snap.JoinedAt == 0 {
		t.Fatalf("等级/入站时间不符: level=%q joined=%d", snap.Level, snap.JoinedAt)
	}
	if snap.Seeding != 0 || snap.Uploaded != 0 {
		t.Fatalf("非 PT 指标应保持零值: %+v", snap)
	}
}

func TestFetchDian115AlreadySignedSkips(t *testing.T) {
	srv, stub := newDianStub(t)
	stub.mu.Lock()
	stub.lastSigninDate = time.Now().In(dianTZ).Format("2006-01-02")
	stub.mu.Unlock()
	snap, err := FetchSite(dianSite(srv.URL, "lucky"))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if stub.signinCalls != 0 {
		t.Fatalf("今日已签不应再打签到接口: %d 次", stub.signinCalls)
	}
	if snap.Dian == nil || !snap.Dian.SignedToday {
		t.Fatalf("今日已签状态不符: %+v", snap.Dian)
	}
}

func TestFetchDian115NoModeQueryOnly(t *testing.T) {
	srv, stub := newDianStub(t)
	if _, err := FetchSite(dianSite(srv.URL, "")); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if stub.signinCalls != 0 {
		t.Fatalf("未配签到模式不应签到: %d 次", stub.signinCalls)
	}
}

func TestSigninDian115ManualForce(t *testing.T) {
	srv, _ := newDianStub(t)
	// 手动签到不挑配置：SigninMode 为空也执行（回落 normal）
	snap, msg, err := SigninDian115(dianSite(srv.URL, ""))
	if err != nil {
		t.Fatalf("signin: %v", err)
	}
	if msg != "签到成功 +10 积分" || snap.Bonus != 2833 || !snap.Dian.SignedToday || snap.Dian.Award != 10 {
		t.Fatalf("手动签到结果不符: msg=%q bonus=%v dian=%+v", msg, snap.Bonus, snap.Dian)
	}
	// 再点一次：服务端已签，容忍 already_signed 不报错（award 不产生，保持 0）
	snap2, msg2, err := SigninDian115(dianSite(srv.URL, ""))
	if err != nil || msg2 != "今日已签到" {
		t.Fatalf("重复签到应容忍 already_signed: err=%v msg=%q", err, msg2)
	}
	if snap2.Dian.Award != 0 {
		t.Fatalf("already_signed 不应产生得分: %+v", snap2.Dian)
	}
	if snap2.Bonus != 2833 {
		t.Fatalf("重复签到不应重复加分: %v", snap2.Bonus)
	}
}

func TestSigninDian115AwardVariants(t *testing.T) {
	srv, stub := newDianStub(t)
	stub.mu.Lock()
	stub.signinConflict = true
	stub.mu.Unlock()
	if _, msg, err := SigninDian115(dianSite(srv.URL, "")); err != nil || msg != "今日已签到" {
		t.Fatalf("冲突签到文案不符: err=%v msg=%q", err, msg)
	}

	srv2, stub2 := newDianStub(t)
	zero := 0
	stub2.signinAward = &zero
	if _, msg, err := SigninDian115(dianSite(srv2.URL, "lucky")); err != nil || msg != "签到完成，空签" {
		t.Fatalf("空签文案不符: err=%v msg=%q", err, msg)
	}

	srv3, stub3 := newDianStub(t)
	neg := -10
	stub3.signinAward = &neg
	if _, msg, err := SigninDian115(dianSite(srv3.URL, "lucky")); err != nil || msg != "签到完成，倒霉签 -10 积分" {
		t.Fatalf("倒霉签文案不符: err=%v msg=%q", err, msg)
	}
}

func TestFetchDian115ProofRetry(t *testing.T) {
	srv, stub := newDianStub(t)
	stub.mu.Lock()
	stub.failProofOnce = true // 首个签名请求判签名无效 → 客户端应重注册密钥重试
	stub.mu.Unlock()
	if _, err := FetchSite(dianSite(srv.URL, "")); err != nil {
		t.Fatalf("proof 失效重试后应成功: %v", err)
	}
	if !stub.signed {
		t.Fatal("重试请求未通过服务端验签")
	}
}

func TestFetchDian115BadToken(t *testing.T) {
	srv, stub := newDianStub(t)
	stub.mu.Lock()
	stub.badToken = true
	stub.mu.Unlock()
	_, err := FetchSite(dianSite(srv.URL, ""))
	if err == nil || !contains(err.Error(), "登录态已失效") {
		t.Fatalf("登录态失效应给重贴 Cookie 提示: %v", err)
	}
}

// 回归（真站暴露）：base_url 留空（用目录默认域）时不得打出无 scheme 的 URL。
// 直接对解析函数断言（目录默认域是真站，不能真打）；桩链路再验配置优先。
func TestDian115BaseURLFallback(t *testing.T) {
	if got := dianBaseURL(Site{SiteKey: "dian115", BaseURL: ""}); got != "https://m.dian115.com" {
		t.Fatalf("空 base_url 应回落目录默认域: %q", got)
	}
	if got := dianBaseURL(Site{SiteKey: "dian115", BaseURL: "http://127.0.0.1:1/"}); got != "http://127.0.0.1:1" {
		t.Fatalf("配置地址应优先且规范去尾斜杠: %q", got)
	}
	if got := dianBaseURL(Site{SiteKey: "no-such", BaseURL: ""}); got != "" {
		t.Fatalf("未知站点应返回空串: %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
