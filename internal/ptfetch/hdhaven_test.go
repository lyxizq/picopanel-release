package ptfetch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// HDHaven 链路测试（432）：桩出 JSON API 三个端点，覆盖用户解析（积分双形态/
// 等级回退）、自动签到分支（普通/赌狗/已签 409/仅查询不签）与错误翻译
//（登录态失效/频控验证码），并断言请求头形态与 Cookie 播种。

// hdhStub HDHaven API 桩：记录请求头与签到调用，回放用户数据。
type hdhStub struct {
	mu          sync.Mutex
	meCalls     int
	mePoints    any    // int 或 {available:N} 双形态
	meSigned    bool   // checkin_today
	nickname    string // 空 = 回退 username 断言
	levelName   string // 空 = 回退「普通用户」断言
	isMember    bool
	checkinReward float64 // checkin_reward（453 当日签到得分）
	signinCalls int
	gambleCalls int
	lastCookie  string
	lastOrigin  string
	failAuth    bool // /me 回 401（测登录态失效文案）
	challengeMe bool // /me 回 200 信封 SCRAPE_CHALLENGE_REQUIRED
	conflict    bool // 签到回 409
	earned      any  // 签到 earned（int；nil=不带）
}

func newHdhStub(t *testing.T) (*httptest.Server, *hdhStub) {
	t.Helper()
	s := &hdhStub{mePoints: 2823}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.meCalls++
		s.lastCookie = r.Header.Get("Cookie")
		if s.failAuth {
			writeJSON(w, 401, map[string]any{"success": false, "message": "请先登录"})
			return
		}
		if s.challengeMe {
			writeJSON(w, 200, map[string]any{"success": false, "code": "SCRAPE_CHALLENGE_REQUIRED", "message": "访问过于频繁"})
			return
		}
		s.lastOrigin = r.Header.Get("Origin")
		level := map[string]any{}
		if s.levelName != "" {
			level["name"] = s.levelName
		}
		writeJSON(w, 200, map[string]any{"success": true, "data": map[string]any{
			"id": "u1", "nickname": s.nickname, "username": "lyxizq", "points": s.mePoints,
			"level": level, "is_member": s.isMember, "checkin_today": s.meSigned,
			"checkin_reward": s.checkinReward,
			"last_checkin": "2026-10-04T06:00:00Z", "user_number": "1024",
		}})
	})
	checkin := func(gamble bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if gamble {
				s.gambleCalls++
			} else {
				s.signinCalls++
			}
			// 409 也代表站点状态已是已签（只是本次请求撞了重），状态照样推进
			s.meSigned = true
			if s.conflict {
				writeJSON(w, 409, map[string]any{"success": false, "message": "今日已签到"})
				return
			}
			out := map[string]any{"success": true, "data": map[string]any{"outcome": ""}}
			if s.earned != nil {
				out["data"].(map[string]any)["earned"] = s.earned
			}
			writeJSON(w, 200, out)
		}
	}
	mux.HandleFunc("/api/account/checkin", checkin(false))
	mux.HandleFunc("/api/account/checkin/gamble", checkin(true))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, s
}

func TestFetchHDHaven(t *testing.T) {
	srv, s := newHdhStub(t)
	s.nickname = "栖影用户"
	s.levelName = "Lv.2"
	s.checkinReward = 5
	snap, err := FetchSite(Site{SiteKey: "hdhaven", BaseURL: srv.URL, Cookie: "hdh_session=abc; other=1"})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if snap.Username != "栖影用户" {
		t.Fatalf("username=%q", snap.Username)
	}
	if snap.Level != "Lv.2" {
		t.Fatalf("level=%q", snap.Level)
	}
	if snap.Bonus != 2823 {
		t.Fatalf("bonus=%v", snap.Bonus)
	}
	if snap.Hdh == nil || snap.Hdh.SignedToday || snap.Hdh.VIP {
		t.Fatalf("hdh=%+v", snap.Hdh)
	}
	// 453：成长等级名与当日签到得分进扩展段（Level 折叠不动，卡片取 Hdh.Level）
	if snap.Hdh.Level != "Lv.2" || snap.Hdh.CheckinReward != 5 {
		t.Fatalf("hdh 扩展段: %+v", snap.Hdh)
	}
	s.mu.Lock()
	calls := s.signinCalls
	origin := s.lastOrigin
	cookie := s.lastCookie
	s.mu.Unlock()
	if calls != 0 {
		t.Fatal("默认仅查询，不应签到")
	}
	if !strings.Contains(cookie, "hdh_session=abc") {
		t.Fatalf("cookie 未播种: %q", cookie)
	}
	if origin != srv.URL {
		t.Fatalf("origin=%q", origin)
	}
}

func TestHDHavenUserFallbacks(t *testing.T) {
	srv, s := newHdhStub(t)
	// nickname 空 → 回退 username；level 空 → 普通用户；积分 {available} 形态
	s.nickname = ""
	s.levelName = ""
	s.mePoints = map[string]any{"available": 123}
	snap, err := FetchSite(Site{SiteKey: "hdhaven", BaseURL: srv.URL, Cookie: "hdh_session=abc"})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if snap.Username != "lyxizq" {
		t.Fatalf("username=%q", snap.Username)
	}
	if snap.Level != "普通用户" {
		t.Fatalf("level=%q", snap.Level)
	}
	if snap.Bonus != 123 {
		t.Fatalf("bonus=%v", snap.Bonus)
	}
}

func TestHDHavenAutoSigninNormal(t *testing.T) {
	srv, s := newHdhStub(t)
	s.nickname = "n1"
	s.earned = 8
	snap, err := FetchSite(Site{SiteKey: "hdhaven", BaseURL: srv.URL, Cookie: "hdh_session=abc", SigninMode: "normal"})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	s.mu.Lock()
	calls := s.signinCalls
	s.mu.Unlock()
	if calls != 1 {
		t.Fatalf("signinCalls=%d", calls)
	}
	if !snap.Hdh.SignedToday {
		t.Fatal("签到后快照应已签")
	}
	// me 两次：签到前判重 + 签到后取到账余额
	s.mu.Lock()
	meCalls := s.meCalls
	s.mu.Unlock()
	if meCalls != 2 {
		t.Fatalf("meCalls=%d", meCalls)
	}
}

func TestHDHavenSigninGamble(t *testing.T) {
	srv, s := newHdhStub(t)
	s.earned = -3
	_, msg, err := SigninHDHaven(Site{SiteKey: "hdhaven", BaseURL: srv.URL, Cookie: "hdh_session=abc", SigninMode: "lucky"})
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	if !strings.Contains(msg, "倒扣 3") || !strings.Contains(msg, "赌狗") {
		t.Fatalf("msg=%q", msg)
	}
	s.mu.Lock()
	gamble, normal := s.gambleCalls, s.signinCalls
	s.mu.Unlock()
	if gamble != 1 || normal != 0 {
		t.Fatalf("gamble=%d normal=%d", gamble, normal)
	}
}

func TestHDHavenSigninAlreadyDone(t *testing.T) {
	srv, s := newHdhStub(t)
	s.meSigned = true // 首次 me 即已签：不应发签到请求
	snap, _, err := SigninHDHaven(Site{SiteKey: "hdhaven", BaseURL: srv.URL, Cookie: "hdh_session=abc"})
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	if !snap.Hdh.SignedToday {
		t.Fatal("应已签")
	}
	s.mu.Lock()
	calls := s.signinCalls
	s.mu.Unlock()
	if calls != 0 {
		t.Fatalf("已签还发签到: %d", calls)
	}
}

func TestHDHavenSigninConflict409(t *testing.T) {
	srv, s := newHdhStub(t)
	s.conflict = true // 并发竞态：签到请求发出但站点回 409
	snap, msg, err := SigninHDHaven(Site{SiteKey: "hdhaven", BaseURL: srv.URL, Cookie: "hdh_session=abc"})
	if err != nil {
		t.Fatalf("409 应容忍: %v", err)
	}
	if msg != "今日已签到" {
		t.Fatalf("msg=%q", msg)
	}
	if !snap.Hdh.SignedToday {
		t.Fatal("重拉 me 后应已签")
	}
}

func TestHDHavenAuthExpired(t *testing.T) {
	srv, s := newHdhStub(t)
	s.failAuth = true
	_, err := FetchSite(Site{SiteKey: "hdhaven", BaseURL: srv.URL, Cookie: "hdh_session=stale"})
	if err == nil || !strings.Contains(err.Error(), "重新粘贴") {
		t.Fatalf("err=%v", err)
	}
}

func TestHDHavenChallenge(t *testing.T) {
	srv, s := newHdhStub(t)
	s.challengeMe = true
	_, err := FetchSite(Site{SiteKey: "hdhaven", BaseURL: srv.URL, Cookie: "hdh_session=abc"})
	if err == nil || !strings.Contains(err.Error(), "验证码") {
		t.Fatalf("err=%v", err)
	}
}

func TestHDHavenBaseURLFallback(t *testing.T) {
	// 空 base_url 回落目录默认域（416 同款坑的探针纪律：生产同款入参形态）
	if got := hdhBaseURL(Site{SiteKey: "hdhaven", BaseURL: ""}); got != "https://hdhaven.com" {
		t.Fatalf("fallback=%q", got)
	}
	if got := hdhBaseURL(Site{SiteKey: "hdhaven", BaseURL: "https://mirror.example.com/"}); got != "https://mirror.example.com" {
		t.Fatalf("custom=%q", got)
	}
}

func TestHDHavenEnvelopeDecode(t *testing.T) {
	// hdhPoints 双形态直接单测（服务端版本兼容的核心防御点）
	var p hdhPoints
	if err := json.Unmarshal([]byte(`42`), &p); err != nil || p != 42 {
		t.Fatalf("int 形态: %v %v", p, err)
	}
	if err := json.Unmarshal([]byte(`{"available": 7}`), &p); err != nil || p != 7 {
		t.Fatalf("dict 形态: %v %v", p, err)
	}
	if err := json.Unmarshal([]byte(`{"x":1}`), &p); err == nil {
		t.Fatal("未知形态应报错")
	}
}
