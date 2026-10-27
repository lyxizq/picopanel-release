package aiusage

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// TestFetchZhipu 智谱额度拉取与归一化：5 小时/周窗口百分比、重置时间毫秒转秒、
// TIME_LIMIT 工具月配额（含重置时间）、周维度按账务日分布、鉴权头带 Bearer Key。
func TestFetchZhipu(t *testing.T) {
	var gotAuth string
	// 周窗口重置点=现在+3 天 → 账务期起点=现在-4 天；两个桶分别落在第 0 天与第 3 天
	now := time.Now()
	resetMs := now.Add(3*24*time.Hour).Unix() * 1000
	b0 := now.Add(-4 * 24 * time.Hour).Add(time.Hour)
	b1 := now.Add(-time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/monitor/usage/quota/limit":
			_, _ = w.Write([]byte(`{"data":{"level":"max","limits":[
				{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":6,"nextResetTime":1755000000000},
				{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":27.5,"nextResetTime":` + strconv.FormatInt(resetMs, 10) + `,
				 "usage":2253000000,"currentValue":609000000,"remaining":1644000000},
				{"type":"TIME_LIMIT","usage":4000,"currentValue":120,"remaining":3880,"nextResetTime":1797600000000}
			]}}`))
		case "/api/monitor/usage/model-usage":
			stamp := func(t time.Time) string { return t.Format("2006-01-02 15:04:05") }
			_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"x_time":["` + stamp(b0) + `","` + stamp(b1) + `"],
				"modelDataList":[
					{"modelName":"glm-5.3","tokensUsage":[1000,2000],"callsUsage":[100,200]},
					{"modelName":"glm-flash","tokensUsage":[500,-3,0]}
				]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	snap, err := Fetch(store.AiAccount{Provider: "zhipu", BaseURL: srv.URL, APIKey: "zk-test"})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer zk-test" {
		t.Fatalf("鉴权头不符: %q", gotAuth)
	}
	if snap.Kind != "quota" || snap.Level != "max" {
		t.Fatalf("kind/level 不符: %+v", snap)
	}
	if len(snap.Windows) != 2 {
		t.Fatalf("应归一化出 2 个 TOKENS_LIMIT 窗口，实际 %d", len(snap.Windows))
	}
	w5, wk := snap.Windows[0], snap.Windows[1]
	if w5.Key != "5h" || w5.Label != "5 小时窗口" || w5.UsedPct != 6 {
		t.Fatalf("5 小时窗口不符: %+v", w5)
	}
	if w5.Used != nil || w5.Total != nil || w5.Remaining != nil {
		t.Fatalf("无绝对计数的窗口不应带 Used/Total/Remaining: %+v", w5)
	}
	// 周窗口带绝对计数（2026-10-04 用户要求展示周额度上限）
	if wk.Key != "week" || wk.UsedPct != 27.5 || wk.ResetAt != resetMs/1000 {
		t.Fatalf("周窗口不符: %+v", wk)
	}
	if wk.Used == nil || *wk.Used != 609000000 || wk.Total == nil || *wk.Total != 2253000000 || wk.Remaining == nil || *wk.Remaining != 1644000000 {
		t.Fatalf("周窗口绝对计数不符: %+v", wk)
	}
	// TOKENS_LIMIT 已用本就是 tokens：不重复带 tokens_used（2026-10-26 口径）
	if w5.TokensUsed != nil || wk.TokensUsed != nil {
		t.Fatalf("TOKENS_LIMIT 窗口不应带 tokens_used: %+v %+v", w5, wk)
	}
	if snap.Tools == nil || snap.Tools.Usage != 4000 || snap.Tools.Current != 120 || snap.Tools.Remaining != 3880 || snap.Tools.ResetAt != 1797600000 {
		t.Fatalf("工具月配额不符: %+v", snap.Tools)
	}
	// 周维度分析（401）：统计区间 + 按账务日分布（第 0/3 天有数据，其余空行补位）
	if snap.WeekStart != wk.ResetAt-7*86400 {
		t.Fatalf("周起点应=重置点-7 天: %v", snap.WeekStart)
	}
	if snap.WeekFrom == "" || snap.WeekTo == "" {
		t.Fatalf("统计区间文案缺失: %q → %q", snap.WeekFrom, snap.WeekTo)
	}
	if len(snap.WeekDaily) != 4 {
		t.Fatalf("应 4 行账务日（0..3）: %+v", snap.WeekDaily)
	}
	d0, d3 := snap.WeekDaily[0], snap.WeekDaily[3]
	if d0.Tokens != 1500 || len(d0.Models) != 2 || d0.Models[0].Name != "glm-5.3" || d0.Models[0].Tokens != 1000 || d0.Calls != 100 {
		t.Fatalf("第 0 天聚合不符: %+v", d0)
	}
	if d3.Tokens != 2000 || d3.Models[0].Tokens != 2000 || d3.Calls != 200 || !d3.Partial {
		t.Fatalf("第 3 天（当天未走完）不符: %+v", d3)
	}
	if snap.WeekDaily[1].Tokens != 0 || snap.WeekDaily[2].Tokens != 0 {
		t.Fatalf("中间空日应补零行: %+v", snap.WeekDaily)
	}
	// 账务日标签：起点日+第 3 天日期对齐
	start := time.Unix(snap.WeekStart, 0)
	wantLabel := start.Format("01-02") + " 周" + []string{"日", "一", "二", "三", "四", "五", "六"}[int(start.Weekday())]
	if d0.Label != wantLabel {
		t.Fatalf("标签不符: %q（want %q）", d0.Label, wantLabel)
	}
}

// TestFetchZhipuCreditLimit GLM Coding Plan V3 积分制（CREDIT_LIMIT）解析：
// 2026-10 起 V3 套餐 quota/limit 的 limits[].type 由 TOKENS_LIMIT 变
// CREDIT_LIMIT（原实现只认 TOKENS_LIMIT，200 响应解析不出窗口报「上游未返回
// 额度数据」）。字段同构 usage=总量/currentValue=已用/remaining=剩余；
// percentage 缺失（0）且有计数时按 currentValue/usage 现算。
func TestFetchZhipuCreditLimit(t *testing.T) {
	now := time.Now()
	resetMs := now.Add(3*24*time.Hour).Unix() * 1000
	fiveMs := now.Add(2*time.Hour).Unix() * 1000 // 5h 窗口重置点=现在+2h → token 求和起点=现在-3h
	// token 桶两只：b0=账务期第 0 天（窗口起点之前）、b1=现在-1h（窗口之内）
	b0 := now.Add(-4 * 24 * time.Hour).Add(time.Hour)
	b1 := now.Add(-time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/monitor/usage/quota/limit":
			_, _ = w.Write([]byte(`{"code":200,"msg":"操作成功","data":{"limits":[
				{"type":"CREDIT_LIMIT","unit":3,"number":5,"usage":12000,"currentValue":0,"remaining":12000,"percentage":0,"nextResetTime":` + strconv.FormatInt(fiveMs, 10) + `},
				{"type":"CREDIT_LIMIT","unit":6,"number":1,"usage":30000,"currentValue":4500,"remaining":25500,"nextResetTime":` + strconv.FormatInt(resetMs, 10) + `},
				{"type":"TIME_LIMIT","usage":4000,"currentValue":10,"remaining":3990,"nextResetTime":1797600000000}
			]}}`))
		case "/api/monitor/usage/model-usage":
			stamp := func(t time.Time) string { return t.Format("2006-01-02 15:04:05") }
			_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"x_time":["` + stamp(b0) + `","` + stamp(b1) + `"],
				"modelDataList":[{"modelName":"glm-5.3","tokensUsage":[1000,2000]}]}}`))
		default:
			http.NotFound(w, r) // 其余路径不存在
		}
	}))
	defer srv.Close()

	snap, err := Fetch(store.AiAccount{Provider: "zhipu", BaseURL: srv.URL, APIKey: "zk-test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Windows) != 2 {
		t.Fatalf("应解析出 2 个 CREDIT_LIMIT 窗口，实际 %d", len(snap.Windows))
	}
	w5, wk := snap.Windows[0], snap.Windows[1]
	if w5.Key != "5h" || w5.UsedPct != 0 || !w5.Credit {
		t.Fatalf("5 小时积分窗口百分比/积分标记不符: %+v", w5)
	}
	if w5.Used == nil || *w5.Used != 0 || w5.Total == nil || *w5.Total != 12000 || w5.Remaining == nil || *w5.Remaining != 12000 {
		t.Fatalf("5 小时积分窗口计数不符: %+v", w5)
	}
	// 周窗口 percentage 缺失：4500/30000=15% 现算兜底
	if wk.Key != "week" || wk.UsedPct != 15 || wk.ResetAt != resetMs/1000 || !wk.Credit {
		t.Fatalf("周积分窗口不符: %+v", wk)
	}
	if wk.Used == nil || *wk.Used != 4500 || wk.Total == nil || *wk.Total != 30000 || wk.Remaining == nil || *wk.Remaining != 25500 {
		t.Fatalf("周积分窗口绝对计数不符: %+v", wk)
	}
	// token 用量（2026-10-26 用户指定卡片展示）：401 桶求和——5h=窗口起点以来 2000、周=全期 3000
	if w5.TokensUsed == nil || *w5.TokensUsed != 2000 {
		t.Fatalf("5h 窗口 tokens 应为窗口起点以来的 2000: %+v", w5)
	}
	if wk.TokensUsed == nil || *wk.TokensUsed != 3000 {
		t.Fatalf("周窗口 tokens 应为全期合计 3000: %+v", wk)
	}
	if snap.Tools == nil || snap.Tools.Usage != 4000 || snap.Tools.Current != 10 {
		t.Fatalf("TIME_LIMIT 并存解析不符: %+v", snap.Tools)
	}
}

// TestFetchDeepSeek DeepSeek 余额拉取：多币种逐条归一化、可用状态随行。
func TestFetchDeepSeek(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[
			{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}
		]}`))
	}))
	defer srv.Close()

	snap, err := Fetch(store.AiAccount{Provider: "deepseek", BaseURL: srv.URL, APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != "balance" || len(snap.Balances) != 1 {
		t.Fatalf("余额快照不符: %+v", snap)
	}
	b := snap.Balances[0]
	if b.Currency != "CNY" || b.Total != "110.00" || b.Granted != "10.00" || b.ToppedUp != "100.00" || !b.Available {
		t.Fatalf("余额字段不符: %+v", b)
	}
}

// TestFetchError 上游非 200 → 带状态码报错；未知服务商报错。
func TestFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid api key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := Fetch(store.AiAccount{Provider: "zhipu", BaseURL: srv.URL, APIKey: "bad"}); err == nil {
		t.Fatal("非 200 应报错")
	}
	if _, err := Fetch(store.AiAccount{Provider: "openai", APIKey: "x"}); err == nil {
		t.Fatal("未知服务商应报错")
	}
}

// TestManagerRefresh Manager 全链路：Create（密钥）→ RefreshNow 快照入库 +
// 健康态同步 → 上游改数据后再刷整份覆盖 → Drop 后仍可从库读旧快照。
func TestManagerRefresh(t *testing.T) {
	payload := `{"data":{"level":"lite","limits":[{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":10,"nextResetTime":1755600000000}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.CreateAiAccount(&store.AiAccount{Name: "测试智谱", Provider: "zhipu", BaseURL: srv.URL, APIKey: "zk-it"})
	if err != nil {
		t.Fatal(err)
	}
	m := New(s)
	snap, err := m.RefreshNow(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil || len(snap.Windows) != 1 || snap.Windows[0].UsedPct != 10 {
		t.Fatalf("首次刷新快照不符: %+v", snap)
	}

	// 上游数据变化 → 再刷整份覆盖（仅最新一份）
	payload = `{"data":{"level":"lite","limits":[{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":88,"nextResetTime":1755600000000}]}}`
	if _, err := m.RefreshNow(id); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAiAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSnapshot(a.UsageJSON)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Windows[0].UsedPct != 88 {
		t.Fatalf("应覆盖为最新百分比 88，实际: %v", decoded.Windows[0].UsedPct)
	}
	if a.LastError != "" || a.LastOKAt == 0 || a.UsageAt == 0 {
		t.Fatalf("健康态/快照时间不符: %+v", a)
	}

	// 上游挂掉 → 健康态记错、旧快照保留（页面仍显示最近一次数据）
	srv.Close()
	if _, err := m.RefreshNow(id); err == nil {
		t.Fatal("上游挂掉应报错")
	}
	a, _ = s.GetAiAccount(id)
	if a.LastError == "" {
		t.Fatal("失败应记 last_error")
	}
	if decoded2, _ := DecodeSnapshot(a.UsageJSON); decoded2 == nil || decoded2.Windows[0].UsedPct != 88 {
		t.Fatal("失败不应动旧快照")
	}
}

// TestPollDueSkipsBusy 手动刷新进行中的账户，定时轮询本轮跳过不重复打上游
// （407 互斥收口）；busy 释放后下一轮正常补拉。
func TestPollDueSkipsBusy(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/monitor/usage/quota/limit" { // 只计额度主请求（辅助的 model-usage 不代表一次轮询）
			hits++
		}
		_, _ = w.Write([]byte(`{"data":{"level":"lite","limits":[{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":10,"nextResetTime":1797600000000}]}}`))
	}))
	defer srv.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	aid, err := s.CreateAiAccount(&store.AiAccount{Name: "互斥测试", Provider: "zhipu", BaseURL: srv.URL, APIKey: "zk-busy"})
	if err != nil {
		t.Fatal(err)
	}
	m := New(s)

	// busy 中：pollDue 跳过（既不打上游、也不推进 lastPoll）
	m.mu.Lock()
	m.busy[aid] = true
	m.mu.Unlock()
	m.pollDue(time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local))
	if hits != 0 {
		t.Fatalf("busy 中不应请求上游，实际 %d 次", hits)
	}
	m.mu.Lock()
	_, polled := m.lastPoll[aid]
	m.mu.Unlock()
	if polled {
		t.Fatal("busy 跳过时不应推进 lastPoll")
	}

	// busy 释放：下一轮立即正常拉取
	m.mu.Lock()
	delete(m.busy, aid)
	m.mu.Unlock()
	m.pollDue(time.Date(2026, 10, 5, 12, 0, 15, 0, time.Local))
	if hits != 1 {
		t.Fatalf("释放后应补拉 1 次，实际 %d 次", hits)
	}
}

// TestQuietHours 静默窗（2026-10-04）：本地 01:00–05:30 定时轮询停止（上游零
// 请求），05:30 恢复后到期立即补拉；手动 RefreshNow 不受静默窗限制。
func TestQuietHours(t *testing.T) {
	if !inQuietHours(time.Date(2026, 10, 4, 1, 0, 0, 0, time.Local)) ||
		!inQuietHours(time.Date(2026, 10, 4, 3, 27, 0, 0, time.Local)) ||
		!inQuietHours(time.Date(2026, 10, 4, 5, 29, 59, 0, time.Local)) {
		t.Fatal("01:00/03:27/05:29:59 应在静默窗内")
	}
	if inQuietHours(time.Date(2026, 10, 4, 0, 59, 0, 0, time.Local)) ||
		inQuietHours(time.Date(2026, 10, 4, 5, 30, 0, 0, time.Local)) {
		t.Fatal("00:59/05:30 不应在静默窗内")
	}

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/monitor/usage/quota/limit" { // 只计额度主请求（辅助的 model-usage 不代表一次轮询）
			hits++
		}
		_, _ = w.Write([]byte(`{"data":{"level":"lite","limits":[{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":10,"nextResetTime":1797600000000}]}}`))
	}))
	defer srv.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	aid, err := s.CreateAiAccount(&store.AiAccount{Name: "静默测试", Provider: "zhipu", BaseURL: srv.URL, APIKey: "zk-q"})
	if err != nil {
		t.Fatal(err)
	}
	m := New(s)

	// 静默窗内 pollDue：不碰上游、也不推进 lastPoll（恢复后立即补拉）
	m.pollDue(time.Date(2026, 10, 4, 2, 0, 0, 0, time.Local))
	if hits != 0 {
		t.Fatalf("静默窗内不应请求上游，实际 %d 次", hits)
	}
	m.mu.Lock()
	_, polled := m.lastPoll[aid]
	m.mu.Unlock()
	if polled {
		t.Fatal("静默窗内不应推进 lastPoll")
	}

	// 05:30 恢复：到期立即补拉
	m.pollDue(time.Date(2026, 10, 4, 5, 30, 0, 0, time.Local))
	if hits != 1 {
		t.Fatalf("恢复后应补拉 1 次，实际 %d 次", hits)
	}

	// 手动刷新不受静默窗限制
	if _, err := m.RefreshNow(aid); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("手动刷新应打上游，实际 %d 次", hits)
	}
}

// TestZhipuBusinessError 智谱业务错误回归（2026-10-04 用户实测）：该接口对无效
// Key 也回 HTTP 200，错误在 JSON 里（{"code":401,"msg":"令牌已过期…"}）——必须
// 把 msg 带出来而不是吞成「未返回额度数据」；空 limits 带原始响应；code 为字符
// 串形态（"200"）兼容。
func TestZhipuBusinessError(t *testing.T) {
	// 业务错误：msg 直出
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":401,"msg":"令牌已过期或验证不正确","success":false}`))
	}))
	defer srv.Close()
	_, err := Fetch(store.AiAccount{Provider: "zhipu", BaseURL: srv.URL, APIKey: "bad"})
	if err == nil || !strings.Contains(err.Error(), "业务错误 401") || !strings.Contains(err.Error(), "令牌已过期") {
		t.Fatalf("业务错误应带出 code 与 msg，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "请求地址") || !strings.Contains(err.Error(), "/api/monitor/usage/quota/limit") {
		t.Fatalf("业务错误应带请求地址，实际: %v", err)
	}

	// 200/成功空数据：报错带原始响应（Key 无 Coding Plan / 响应结构变化时可查真因）
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"level":"","limits":[]}}`))
	}))
	defer srv2.Close()
	_, err = Fetch(store.AiAccount{Provider: "zhipu", BaseURL: srv2.URL, APIKey: "k"})
	if err == nil || !strings.Contains(err.Error(), "原始响应") || !strings.Contains(err.Error(), `"limits":[]`) {
		t.Fatalf("空数据报错应带原始响应，实际: %v", err)
	}

	// code 为字符串 "200"：正常解析不误报
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"200","success":true,"data":{"level":"max","limits":[
			{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":27,"nextResetTime":1797600000000}
		]}}`))
	}))
	defer srv3.Close()
	snap, err := Fetch(store.AiAccount{Provider: "zhipu", BaseURL: srv3.URL, APIKey: "k"})
	if err != nil {
		t.Fatalf("字符串 code=200 不应报错: %v", err)
	}
	if len(snap.Windows) != 1 || snap.Windows[0].UsedPct != 27 {
		t.Fatalf("字符串 code 形态解析不符: %+v", snap)
	}
}

// TestDeepSeekV1Prefix deepseek base_url 带 /v1 前缀（聊天补全习惯误填）自动
// 剥除，请求落到站点根的 /user/balance；404 报错带请求地址。
func TestDeepSeekV1Prefix(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"1.00","granted_balance":"0.00","topped_up_balance":"1.00"}]}`))
	}))
	defer srv.Close()
	if _, err := Fetch(store.AiAccount{Provider: "deepseek", BaseURL: srv.URL + "/v1", APIKey: "sk"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/user/balance" {
		t.Fatalf("/v1 前缀应被剥除直落根路径，实际打到: %s", gotPath)
	}

	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv404.Close()
	_, err := Fetch(store.AiAccount{Provider: "deepseek", BaseURL: srv404.URL, APIKey: "sk"})
	if err == nil || !strings.Contains(err.Error(), "请求地址") {
		t.Fatalf("404 报错应带请求地址，实际: %v", err)
	}
}

// TestPollDueQWeatherInterval 和风控制台账户单独降频（2026-10-23 用户指定 1 小时
// 一拍）：20 分钟复检不到期不重拉，满 1 小时正常补拉；其余账户不受影响仍 20 分钟。
func TestPollDueQWeatherInterval(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics/v1/stats" { // 只计统计主请求，财务块不代表一次轮询
			hits++
			asOf := time.Now().Truncate(time.Hour).Format("2006-01-02T15:04Z07:00")
			_, _ = w.Write([]byte(`{"asOf":"` + asOf + `","success":[{"api":"Weather","hours":[10,20,30]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"asOf":"2026-10-23T09:59+08:00","currency":"CNY","balance":0,"accruedCharges":{}}`))
	}))
	defer srv.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateAiAccount(&store.AiAccount{Name: "和风天气", Provider: store.QWeatherProvider, BaseURL: srv.URL, APIKey: "qw-key"}); err != nil {
		t.Fatal(err)
	}
	m := New(s)
	t0 := time.Date(2026, 10, 23, 12, 0, 0, 0, time.Local) // 静默窗外
	m.pollDue(t0)
	if hits != 1 {
		t.Fatalf("首轮应拉 1 次，实际 %d", hits)
	}
	m.pollDue(t0.Add(20 * time.Minute))
	if hits != 1 {
		t.Fatalf("20 分钟复检不到期（1h 拍）不应重拉，实际 %d", hits)
	}
	m.pollDue(t0.Add(time.Hour + 15*time.Second))
	if hits != 2 {
		t.Fatalf("满 1 小时应补拉，实际 %d", hits)
	}
}
