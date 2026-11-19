package ptfetch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// 蜂巢链路测试（457/465 Cookie 版，字段按真站 2026-10-07 实测形态）：桩出论坛
// 会话三接口，覆盖 会话解析（user+surface：balance 对象{amount}/checkedInToday/
// currentCheckInStreak）、签到面板信息（GET /api/check-in：registeredOn 入站/
// 连签权威）、签到顺带（POST 同款动作 + 已签容忍 + 得分入快照）、仅查询不碰
// POST、登录墙翻译（401 与 user:null）、legacy points 兜底、空 Cookie 拦截。

// fcStub 蜂巢会话桩。
type fcStub struct {
	mu            sync.Mutex
	meCalls       int
	infoCalls     int
	checkinCalls  int
	lastCookie    string
	cookie        string // 桩校验的 Cookie（空=不校验）
	meStatus      int    // /api/auth/me 状态码（默认 200）
	meData        any    // me data（nil=默认）
	infoData      any    // GET check-in 信息（nil=默认）
	checkinData   any    // POST check-in 响应（nil=默认成功）
	checkinStatus int    // POST check-in 状态码（默认 200）
}

// fcWrite 信封直写（{code,message,data}，论坛会话接口真站同款壳）。
func fcWrite(w http.ResponseWriter, st int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(st)
	b, _ := json.Marshal(map[string]any{"code": 0, "message": "", "data": data})
	_, _ = w.Write(b)
}

func fcDefaultMe() map[string]any {
	return map[string]any{
		"user": map[string]any{
			"id": 819, "username": "lyxizq", "displayName": "lyxizq",
			"levelName": "初入蜂巢", "level": 1, "vipLevel": 0, "points": 0,
		},
		"surface": map[string]any{
			"balance":        map[string]any{"source": "credit", "status": "available", "amount": 344},
			"checkedInToday": true, "currentCheckInStreak": 7, "maxCheckInStreak": 29,
		},
	}
}

func fcDefaultInfo() map[string]any {
	return map[string]any{
		"month": "2026-10", "pointName": "蜂蜜", "registeredOn": "2025-04-07",
		"currentStreak": 7, "maxStreak": 29, "checkInReward": 5,
	}
}

func newFcStub(t *testing.T) (*httptest.Server, *fcStub) {
	t.Helper()
	s := &fcStub{}
	mux := http.NewServeMux()
	ok := func(r *http.Request) bool {
		s.lastCookie = r.Header.Get("Cookie")
		return s.cookie == "" || s.lastCookie == s.cookie
	}
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.meCalls++
		if !ok(r) || s.meStatus == http.StatusUnauthorized {
			writeJSON(w, 401, map[string]any{"message": "unauthorized"})
			return
		}
		data := s.meData
		if data == nil {
			data = fcDefaultMe()
		}
		fcWrite(w, 200, data)
	})
	// GET /api/check-in = 签到面板信息（只读）；POST = 签到动作
	mux.HandleFunc("/api/check-in", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !ok(r) {
			writeJSON(w, 401, map[string]any{"message": "unauthorized"})
			return
		}
		if r.Method == http.MethodGet {
			s.infoCalls++
			data := s.infoData
			if data == nil {
				data = fcDefaultInfo()
			}
			fcWrite(w, 200, data)
			return
		}
		s.checkinCalls++
		st := s.checkinStatus
		if st == 0 {
			st = 200
		}
		data := s.checkinData
		if data == nil {
			data = map[string]any{"message": "签到成功", "data": map[string]any{
				"alreadyCheckedIn": false, "reward": 6, "currentStreak": 8,
			}}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		b, _ := json.Marshal(data)
		_, _ = w.Write(b)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, s
}

func TestFetchFengchao(t *testing.T) {
	srv, s := newFcStub(t)
	s.cookie = "session=abc; other=1"
	// 仅查询（无签到模式）：me + 签到信息两个只读 GET，不碰 POST
	snap, err := FetchSite(Site{SiteKey: "fengchao", BaseURL: srv.URL, Cookie: "session=abc; other=1"})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	s.mu.Lock()
	me, info, ci := s.meCalls, s.infoCalls, s.checkinCalls
	s.mu.Unlock()
	if me != 1 || info != 1 || ci != 0 {
		t.Fatalf("仅查询应 me1/info1/POST0，得到 %d/%d/%d", me, info, ci)
	}
	if snap.Username != "lyxizq" || snap.Level != "初入蜂巢" {
		t.Fatalf("user 解析错: %+v", snap)
	}
	// 余额=balance 对象的 amount（蜂蜜）
	if snap.Bonus != 344 {
		t.Fatalf("bonus=%v（应取 balance.amount 344）", snap.Bonus)
	}
	// 入站=签到信息 registeredOn；连签=info.currentStreak
	if snap.JoinedAt == 0 {
		t.Fatalf("registeredOn 未解析")
	}
	if snap.Fc == nil || !snap.Fc.SignedToday || snap.Fc.Streak != 7 || snap.Fc.VIP {
		t.Fatalf("fc=%+v", snap.Fc)
	}
	if snap.Fc.LastCheckin == "" {
		t.Fatalf("已签日应写 last_checkin（460 判定锚点）")
	}
}

func TestSigninFengchao(t *testing.T) {
	srv, s := newFcStub(t)
	snap, msg, err := SigninFengchao(Site{SiteKey: "fengchao", BaseURL: srv.URL, Cookie: "session=abc"})
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	if !strings.Contains(msg, "签到成功") || !strings.Contains(msg, "+6") {
		t.Fatalf("msg=%q", msg)
	}
	s.mu.Lock()
	ci := s.checkinCalls
	s.mu.Unlock()
	if ci != 1 {
		t.Fatalf("应 POST check-in 恰 1 次，实际 %d", ci)
	}
	// 面板签到那次的 reward=6 入快照 award；余额/已签以会话面为权威
	if snap.Fc == nil || !snap.Fc.SignedToday || snap.Fc.Award != 6 || snap.Bonus != 344 {
		t.Fatalf("snap=%+v bonus=%v", snap.Fc, snap.Bonus)
	}
}

func TestSigninFengchaoAlreadyChecked(t *testing.T) {
	srv, s := newFcStub(t)
	s.checkinData = map[string]any{"message": "今日已签到", "data": map[string]any{"alreadyCheckedIn": true}}
	snap, msg, err := SigninFengchao(Site{SiteKey: "fengchao", BaseURL: srv.URL, Cookie: "session=abc"})
	if err != nil {
		t.Fatalf("已签不应报错: %v", err)
	}
	if msg != "今日已签到" {
		t.Fatalf("msg=%q", msg)
	}
	// 已签重放不得覆盖 award（0 不显）
	if snap.Fc == nil || !snap.Fc.SignedToday || snap.Fc.Award != 0 {
		t.Fatalf("fc=%+v", snap.Fc)
	}
}

// TestFengchaoLegacyPoints surface 无 balance 的老账号：points 即余额。
func TestFengchaoLegacyPoints(t *testing.T) {
	srv, s := newFcStub(t)
	s.meData = map[string]any{
		"user":    map[string]any{"username": "old", "displayName": "老账号", "points": 55},
		"surface": map[string]any{"checkedInToday": false},
	}
	s.infoData = map[string]any{"pointName": "蜂蜜", "registeredOn": "2024-01-01", "currentStreak": 0}
	snap, err := FetchSite(Site{SiteKey: "fengchao", BaseURL: srv.URL, Cookie: "c=1"})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if snap.Bonus != 55 {
		t.Fatalf("bonus=%v（legacy points 兜底）", snap.Bonus)
	}
	if snap.Fc == nil || snap.Fc.SignedToday || snap.Fc.LastCheckin != "" || snap.Username != "老账号" {
		t.Fatalf("snap=%+v", snap)
	}
}

// TestFengchaoLoggedOut 两种失效形态都要翻译：HTTP 401 与 200 但 user:null。
func TestFengchaoLoggedOut(t *testing.T) {
	srv, s := newFcStub(t)
	s.meStatus = 401
	if _, err := FetchSite(Site{SiteKey: "fengchao", BaseURL: srv.URL, Cookie: "stale=1"}); err == nil || !strings.Contains(err.Error(), "登录态") {
		t.Fatalf("401 应报登录态: %v", err)
	}
	s2, s := newFcStub(t)
	s.meData = map[string]any{"user": nil, "surface": nil}
	if _, err := FetchSite(Site{SiteKey: "fengchao", BaseURL: s2.URL, Cookie: "stale=1"}); err == nil || !strings.Contains(err.Error(), "未登录") {
		t.Fatalf("user:null 应报未登录: %v", err)
	}
}

func TestFengchaoErrors(t *testing.T) {
	// 空 Cookie 提前拦
	if _, _, err := SigninFengchao(Site{SiteKey: "fengchao", BaseURL: "https://fengchao.chat", Cookie: "  "}); err == nil || !strings.Contains(err.Error(), "Cookie") {
		t.Fatalf("空 Cookie 应提前报错: %v", err)
	}
	// 空 base_url 回落目录默认域
	if got := defBaseURL(Site{SiteKey: "fengchao"}); got != "https://fengchao.chat" {
		t.Fatalf("defBaseURL=%q", got)
	}
	// 目录凭据类型为 cookie
	if def := SiteDefByKey("fengchao"); def == nil || def.Auth != "cookie" {
		t.Fatalf("fengchao auth=%v", def)
	}
}
