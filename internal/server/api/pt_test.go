package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/pt"
	"lz-monitor/internal/server/store"
)

// PT 站点全链路（391）：真实 Register（含鉴权组）+ 桩 NexusPHP 站点，覆盖
// 目录/先测后存/列表不泄凭据/编辑沿用/排序/手动刷新/删除。

// newPtUpstream 桩 NexusPHP 站点（登录态看 Cookie；坏 Cookie 回登录页）。
func newPtUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	index := `<!DOCTYPE html><html><body><div id="info_block">欢迎 <a href="userdetails.php?id=88"><b>pttester</b></a></div></body></html>`
	details := `<!DOCTYPE html><html><body><table>
<tr><td class="rowhead">传输:</td><td>上传量：1.50 TiB<br>下载量：500.00 GiB<br>分享率：3.07</td></tr>
<tr><td class="rowhead">等级:</td><td><img src="c.gif" title="Elite User"></td></tr>
<tr><td class="rowhead">魔力值:</td><td>12,345.67</td></tr>
<tr><td class="rowhead">加入日期:</td><td>2023-01-04</td></tr></table></body></html>`
	mux.HandleFunc("/index.php", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "good=cookie" {
			w.Write([]byte("<html><body><form>please login</form></body></html>"))
			return
		}
		w.Write([]byte(index))
	})
	mux.HandleFunc("/userdetails.php", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(details))
	})
	mux.HandleFunc("/getusertorrentlistajax.php", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div>7 | 21.00 GiB</div><table><tr><td>t</td><td>1 GiB</td></tr></table>`))
	})
	mux.HandleFunc("/mybonus.php", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><div>你当前每小时能获取 12.5 个魔力值</div></body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newPtRouter(t *testing.T) (*gin.Engine, string, string, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal("查管理员失败")
	}
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	// report 路径要过告警评估引擎（stub agent 上报会走到 Evaluate），给个无通知器的实例
	if err := Register(r, s, engine.New(s, engine.Config{}), &config.Config{}, "test", nil, nil, nil, nil, nil, nil, pt.New(s), nil, nil); err != nil {
		t.Fatal(err)
	}
	return r, token, newPtUpstream(t).URL, s
}

func ptReq(t *testing.T, r *gin.Engine, token, method, path, body string, wantCode int) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(w, req)
	if w.Code != wantCode {
		t.Fatalf("%s %s 状态 %d（期望 %d）: %s", method, path, w.Code, wantCode, w.Body.String())
	}
	return w.Body.String()
}

func TestPtFullChain(t *testing.T) {
	r, token, upstream, _ := newPtRouter(t)

	// 目录：29 站齐全（416 起含癫影，432 起含栖影 hdhaven，457 起含蜂巢 fengchao），未添加
	res := ptReq(t, r, token, "GET", "/api/v1/pt/catalog", "", 200)
	if !strings.Contains(res, `"mteam"`) || !strings.Contains(res, `"hdsky"`) || !strings.Contains(res, `"dian115"`) || !strings.Contains(res, `"hdhaven"`) || !strings.Contains(res, `"fengchao"`) {
		t.Fatalf("目录缺站点: %s", res)
	}
	if strings.Count(res, `"key"`) != 29 {
		t.Fatalf("目录站点数 != 29: %d", strings.Count(res, `"key"`))
	}

	// 未登录态凭据先测：502 且不入库
	ptReq(t, r, token, "POST", "/api/v1/pt/test", `{"site_key":"hdsky","base_url":"`+upstream+`","cookie":"bad=cookie"}`, http.StatusBadGateway)

	// 建站：先测后存 + 首份快照即刻入库
	res = ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"hdsky","base_url":"`+upstream+`","cookie":"good=cookie"}`, 200)
	if !strings.Contains(res, `"username":"pttester"`) || !strings.Contains(res, "Elite User") {
		t.Fatalf("建站快照缺身份: %s", res)
	}
	if !strings.Contains(res, `"has_cookie":true`) {
		t.Fatalf("建站响应缺 has_cookie: %s", res)
	}

	// 重复添加同站 → 400
	ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"hdsky","base_url":"`+upstream+`","cookie":"good=cookie"}`, http.StatusBadRequest)

	// 未知站点 → 400
	ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"not-in-catalog","cookie":"good=cookie"}`, http.StatusBadRequest)

	// 列表：快照齐、凭据不回显；做种/时魔值/入站时间到位
	res = ptReq(t, r, token, "GET", "/api/v1/pt/sites", "", 200)
	for _, want := range []string{`"seeding":7`, `"bonus_ph":12.5`, `"joined_at":`, `"seeding_sz":`, `"level":"Elite User"`} {
		if !strings.Contains(res, want) {
			t.Fatalf("列表缺 %s: %s", want, res)
		}
	}
	if strings.Contains(res, "good=cookie") {
		t.Fatalf("列表泄漏凭据: %s", res)
	}
	if !strings.Contains(res, `"id":1`) {
		t.Fatalf("列表缺 id: %s", res)
	}

	// 编辑：凭据留空沿用（不再触发测试，直接保存并刷新）；打开地址自定义后
	// site_url 生效
	res = ptReq(t, r, token, "PUT", "/api/v1/pt/sites/1", `{"site_key":"hdsky","name":"HDSky 改名","cookie":"","open_url":"https://hdsky.me/torrents.php"}`, 200)
	if !strings.Contains(res, `"name":"HDSky 改名"`) {
		t.Fatalf("编辑改名未生效: %s", res)
	}
	if !strings.Contains(res, `"site_url":"https://hdsky.me/torrents.php"`) {
		t.Fatalf("编辑后 site_url 未生效: %s", res)
	}
	// 打开地址传空即清空 → site_url 回落 base_url
	res = ptReq(t, r, token, "PUT", "/api/v1/pt/sites/1", `{"site_key":"hdsky","cookie":"","open_url":""}`, 200)
	if !strings.Contains(res, `"site_url":"`+upstream+`"`) {
		t.Fatalf("清空打开地址应回落 base_url: %s", res)
	}

	// 编辑：新凭据是死的一律 502 且不落库（改名不生效）
	ptReq(t, r, token, "PUT", "/api/v1/pt/sites/1", `{"site_key":"hdsky","name":"不该生效","cookie":"dead=cookie"}`, http.StatusBadGateway)
	res = ptReq(t, r, token, "GET", "/api/v1/pt/sites", "", 200)
	if strings.Contains(res, "不该生效") {
		t.Fatalf("失败编辑不应落库: %s", res)
	}

	// 排序、手动刷新
	ptReq(t, r, token, "PUT", "/api/v1/pt/sites/order", `{"ids":[1]}`, 200)
	res = ptReq(t, r, token, "POST", "/api/v1/pt/sites/1/refresh", "", 200)
	if !strings.Contains(res, `"username":"pttester"`) {
		t.Fatalf("手动刷新缺快照: %s", res)
	}
	ptReq(t, r, token, "POST", "/api/v1/pt/refresh", "", 200)

	// 删除
	ptReq(t, r, token, "DELETE", "/api/v1/pt/sites/1", "", 200)
	res = ptReq(t, r, token, "GET", "/api/v1/pt/sites", "", 200)
	if !strings.Contains(res, `"sites":[]`) {
		t.Fatalf("删除后列表应空: %s", res)
	}
}

// 中转抓取全链路（391 扩展）：出口服务器上建 push 服务器 → 建中转站点（入队
// 首抓，无同步测试）→ stub agent 上报领任务 → 回传快照 → 数据落库；覆盖
// queued 刷新语义、出口校验、换绑作废、重复回传幂等。
func TestPtProxyTaskChain(t *testing.T) {
	r, token, upstream, s := newPtRouter(t)

	// 建出口服务器（push 模式带 token，身份=服务器 id）
	if _, err := s.CreatePushServer("nas", "agent-token-1", 0); err != nil {
		t.Fatal(err)
	}

	// 建中转站点：无同步先测后存（面板直连不通的场景），保存即入队首抓
	res := ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"hdsky","base_url":"`+upstream+`","cookie":"good=cookie","proxy_server_id":1}`, 200)
	if !strings.Contains(res, `"queued":true`) {
		t.Fatalf("中转建站应返回 queued: %s", res)
	}
	if strings.Contains(res, `"data":{`) && !strings.Contains(res, `"data":null`) {
		t.Fatalf("中转建站不应有同步快照: %s", res)
	}

	// 出口服务器不存在 → 400
	ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"ubits","base_url":"`+upstream+`","cookie":"good=cookie","proxy_server_id":99}`, http.StatusBadRequest)

	// 出口非 push 模式（拉取 agent）→ 400（拉取无上报通道，任务发不下去）
	if _, err := s.CreateServer("pull-srv", "127.0.0.1:19100", "", 0); err != nil {
		t.Fatal(err)
	}
	pullID := int64(0)
	if list, err := s.ListServers(); err == nil {
		for _, srv := range list {
			if srv.CollectMode == "agent" {
				pullID = srv.ID
			}
		}
	}
	ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"ubits","base_url":"`+upstream+`","cookie":"good=cookie","proxy_server_id":`+ptItoa(pullID)+`}`, http.StatusBadRequest)

	// stub agent：上报领任务（X-Agent-Token）→ 响应带任务 → 回传快照
	reportRes := ptAgentReq(t, r, "agent-token-1", "POST", "/api/v1/agent/report", `{"sys":{"hostname":"nas"}}`)
	var payload struct {
		PtTasks []struct {
			TaskID  int64  `json:"task_id"`
			SiteID  int64  `json:"site_id"`
			SiteKey string `json:"site_key"`
			BaseURL string `json:"base_url"`
			Cookie  string `json:"cookie"`
		} `json:"pt_tasks"`
	}
	if err := json.Unmarshal([]byte(reportRes), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.PtTasks) != 1 || payload.PtTasks[0].SiteKey != "hdsky" || payload.PtTasks[0].Cookie != "good=cookie" {
		t.Fatalf("上报应捎带 1 个任务（Cookie 解密下发）: %s", reportRes)
	}
	task := payload.PtTasks[0]
	snap := `{"username":"pttester","level":"Elite User","uploaded":1,"downloaded":2,"ratio":0.5}`
	result := `{"task_id":` + ptItoa(task.TaskID) + `,"snapshot":` + snap + `}`
	ptAgentReq(t, r, "agent-token-1", "POST", "/api/v1/agent/task-result", result)

	// 回传后列表出现数据
	res = ptReq(t, r, token, "GET", "/api/v1/pt/sites", "", 200)
	if !strings.Contains(res, `"username":"pttester"`) {
		t.Fatalf("回传后列表应有快照: %s", res)
	}

	// 重复回传幂等（done 后丢弃，不报错）
	ptAgentReq(t, r, "agent-token-1", "POST", "/api/v1/agent/task-result", result)

	// 别的 agent 冒领/错报任务 → 403
	ptAgentReq(t, r, "agent-token-other", "POST", "/api/v1/agent/task-result", result, http.StatusUnauthorized)

	// 手动刷新单站：入队语义（queued:true，不再同步打站点）
	res = ptReq(t, r, token, "POST", "/api/v1/pt/sites/1/refresh", "", 200)
	if !strings.Contains(res, `"queued":true`) {
		t.Fatalf("中转手动刷新应 queued: %s", res)
	}
	// 在途任务期间再刷新：仍是 queued（任务去重）
	res = ptReq(t, r, token, "POST", "/api/v1/pt/sites/1/refresh", "", 200)
	if !strings.Contains(res, `"queued":true`) {
		t.Fatalf("在途任务再刷新应仍 queued: %s", res)
	}
}

// ptAgentReq agent 侧请求（X-Agent-Token 鉴权）。
func ptAgentReq(t *testing.T, r *gin.Engine, agentToken, method, path, body string, wantCode ...int) string {
	t.Helper()
	want := 200
	if len(wantCode) > 0 {
		want = wantCode[0]
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Agent-Token", agentToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s 状态 %d（期望 %d）: %s", method, path, w.Code, want, w.Body.String())
	}
	return w.Body.String()
}

func ptItoa(n int64) string { return strconv.FormatInt(n, 10) }

// 癫影链路（416）：宽桩（不验签，只看签到分支与配置透传；签名正确性在
// ptfetch 包里服务端视角验过）覆盖 建站signin_mode 校验/直连签到端点/非癫影
// 站拒绝/中转任务捎带 signin_mode。

func newDianUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	lastSignin := time.Now().AddDate(0, 0, -1).In(time.FixedZone("UTC+8", 8*3600)).Format("2006-01-02")
	points := 2823.0
	mux.HandleFunc("/api/portal/auth/browser-challenge", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":"ok","enabled":true,"proof":"stub-proof","ttl":600}`))
	})
	mux.HandleFunc("/api/portal/auth/browser-session", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":"ok","enabled":true,"ttl":1800,"server_time_ms":0}`))
	})
	mux.HandleFunc("/api/portal/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Portal-Browser-Sig") == "" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"code":"browser_proof_required","msg":"browser proof required"}`))
			return
		}
		w.Write([]byte(`{"code":"ok","user":{"nickname":"diantester","points":` + strconv.FormatFloat(points, 'f', -1, 64) + `,"consecutive_signin":36,"last_signin_date":"` + lastSignin + `","vip":false,"created_at":"2026-08-10T23:15:16.655486+08:00"}}`))
	})
	mux.HandleFunc("/api/portal/signin", func(w http.ResponseWriter, r *http.Request) {
		lastSignin = time.Now().In(time.FixedZone("UTC+8", 8*3600)).Format("2006-01-02")
		points += 10
		w.Write([]byte(`{"code":"ok","award":10}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// apikey 类站点（mteam）凭据是裸 API Key：不得被 CleanCookie 按 cookie 语义
// 洗掉（v1.0.4 中转建站曾洗成空串落库，抓取恒报 Full authentication is
// required）；中转编辑换凭据也必须真落库（update 曾只给直连分支赋值，中转
// 新凭据被 store 的空沿用静默吞掉）。
func TestPtAPIKeyCredentialKept(t *testing.T) {
	r, token, _, s := newPtRouter(t)
	if _, err := s.CreatePushServer("nas", "agent-token-1", 0); err != nil {
		t.Fatal(err)
	}
	key := "3f2b9c6e1a4d4e8f9b2c7d5e1f0a2b3c"
	res := ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"mteam","cookie":"`+key+`","proxy_server_id":1}`, 200)
	if !strings.Contains(res, `"queued":true`) {
		t.Fatalf("mteam 中转建站应 queued: %s", res)
	}
	if site, err := s.GetPtSite(1); err != nil || site.Cookie != key {
		t.Fatalf("mteam 裸 key 应原样落库: %v %q", err, site.Cookie)
	}

	// 编辑换 key：中转分支同样落值；cookie 类站点清洗语义不变（坏段照丢）
	key2 := "aaaaaaaa-bbbb-cccc-dddd-eeeeffff0000"
	ptReq(t, r, token, "PUT", "/api/v1/pt/sites/1",
		`{"site_key":"mteam","cookie":"`+key2+`","proxy_server_id":1}`, 200)
	if site, err := s.GetPtSite(1); err != nil || site.Cookie != key2 {
		t.Fatalf("中转编辑换 key 应落库: %v %q", err, site.Cookie)
	}
	ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"hdsky","cookie":"good=cookie; 垃圾段; a=b","proxy_server_id":1}`, 200)
	if site, err := s.GetPtSite(2); err != nil || site.Cookie != "good=cookie; a=b" {
		t.Fatalf("cookie 类站点仍应按 cookie 语义清洗: %v %q", err, site.Cookie)
	}
}

func TestPtDian115Chain(t *testing.T) {
	r, token, _, s := newPtRouter(t)
	dian := newDianUpstream(t).URL

	// 签到模式白名单校验
	ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"dian115","base_url":"`+dian+`","cookie":"portal_token=abc","signin_mode":"greedy"}`, http.StatusBadRequest)

	// 建站（signin_mode=normal）：先测后存的抓取顺带自动签到（昨日未签 → 加分+已签）
	res := ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"dian115","base_url":"`+dian+`","cookie":"portal_token=abc","signin_mode":"normal"}`, 200)
	if !strings.Contains(res, `"signin_mode":"normal"`) || !strings.Contains(res, `"username":"diantester"`) {
		t.Fatalf("癫影建站快照/配置不符: %s", res)
	}
	if !strings.Contains(res, `"signed_today":true`) || !strings.Contains(res, `"bonus":2833`) {
		t.Fatalf("建站抓取应顺带自动签到: %s", res)
	}

	// 手动签到：今天已签（建站时签过），容忍并返回文案与最新站点视图
	res = ptReq(t, r, token, "POST", "/api/v1/pt/sites/1/signin", "", 200)
	if !strings.Contains(res, `"msg":"今日已签到"`) || !strings.Contains(res, `"signed_today":true`) {
		t.Fatalf("手动签到响应不符: %s", res)
	}

	// 非签到类站拒绝签到：建 hdsky 后对它调 signin → 400（445 起按 kind 门禁）
	upstream := newPtUpstream(t).URL
	ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"hdsky","base_url":"`+upstream+`","cookie":"good=cookie"}`, 200)
	res = ptReq(t, r, token, "POST", "/api/v1/pt/sites/2/signin", "", http.StatusBadRequest)
	if !strings.Contains(res, "不支持签到") {
		t.Fatalf("非签到类站应拒绝签到: %s", res)
	}

	// hdhaven（432）签到按钮同样放行（445 修复：门禁曾锁 site_key==dian115，
	// 栖影签到一直 400）；站点侧已签（checkin_today=true）时签到不得打签到接口
	checkinHits := 0
	hdhMux := http.NewServeMux()
	hdhMux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"nickname":"hdhtester","points":100,"is_member":false,"checkin_today":true,"last_checkin":"x","level":{"name":"Member"}}}`))
	})
	hdhMux.HandleFunc("/api/account/checkin", func(w http.ResponseWriter, r *http.Request) {
		checkinHits++
		w.Write([]byte(`{"success":true,"data":{"earned":5}}`))
	})
	hdhSrv := httptest.NewServer(hdhMux)
	t.Cleanup(hdhSrv.Close)
	res = ptReq(t, r, token, "POST", "/api/v1/pt/sites",
		`{"site_key":"hdhaven","base_url":"`+hdhSrv.URL+`","cookie":"hdh_session=x"}`, 200)
	if !strings.Contains(res, `"username":"hdhtester"`) {
		t.Fatalf("栖影建站快照不符: %s", res)
	}
	res = ptReq(t, r, token, "POST", "/api/v1/pt/sites/3/signin", "", 200)
	if !strings.Contains(res, `"msg":"今日已签到"`) {
		t.Fatalf("栖影已签签到应回今日已签到: %s", res)
	}
	if checkinHits != 0 {
		t.Fatalf("已签站点点签到不应打签到接口: %d 次", checkinHits)
	}

	// 中转任务捎带 signin_mode（从站点行现取）：1 号站改绑出口 + 换凭据（触发
	// 首抓入队），签到模式同时改 lucky
	if _, err := s.CreatePushServer("nas2", "agent-token-dian", 0); err != nil {
		t.Fatal(err)
	}
	res = ptReq(t, r, token, "PUT", "/api/v1/pt/sites/1",
		`{"site_key":"dian115","signin_mode":"lucky","cookie":"portal_token=refreshed","proxy_server_id":1}`, 200)
	reportRes := ptAgentReq(t, r, "agent-token-dian", "POST", "/api/v1/agent/report", `{"sys":{"hostname":"nas2"}}`)
	if !strings.Contains(reportRes, `"signin_mode":"lucky"`) {
		t.Fatalf("任务应捎带站点签到模式: %s", reportRes)
	}
}
