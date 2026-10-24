package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/aiusage"
	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/dbtool"
	"lz-monitor/internal/server/store"
)

// newAIRouter 真实链路：真实 Register（含鉴权组）+ 假上游（智谱/DeepSeek 同端口
// 按路径区分，key=good-key 放行）+ session token。
func newAIRouter(t *testing.T) (*gin.Engine, string, string) {
	r, token, upstream, _ := newAIRouterAll(t)
	return r, token, upstream
}

// newAIRouterAll 同 newAIRouter，另返回 store（和风系统行等直接经 store 造数）。
func newAIRouterAll(t *testing.T) (*gin.Engine, string, string, *store.Store) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// EdgeOne（393）：TEO 协议按 X-TC-Action 路由，无 Authorization Bearer；
		// Credential 里出现 badsid 模拟密钥错误
		if act := r.Header.Get("X-TC-Action"); act != "" {
			if strings.Contains(r.Header.Get("Authorization"), "badsid") {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"Response":{"Error":{"Code":"AuthFailure.SignatureFailure","Message":"签名错误"}}}`))
				return
			}
			switch act {
			case "DescribeZones":
				_, _ = w.Write([]byte(`{"Response":{"TotalCount":1,"Zones":[{"ZoneId":"zone-t1","ZoneName":"t.com"}]}}`))
			case "DescribeTimingL7AnalysisData":
				// 缓存命中单独查询（398 详情块）：假上游回空 Data 模拟指标缺失
				var req struct {
					MetricNames []string `json:"MetricNames"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				if len(req.MetricNames) > 0 && req.MetricNames[0] == "l7Flow_hit_outFlux" {
					_, _ = w.Write([]byte(`{"Response":{"Data":[]}}`))
					return
				}
				_, _ = w.Write([]byte(`{"Response":{"Data":[{"TypeKey":"zone-t1","TypeValue":[
					{"MetricName":"l7Flow_flux","Sum":5560000000},{"MetricName":"l7Flow_bandwidth","Max":24190000},{"MetricName":"l7Flow_request","Sum":64200}]}]}}`))
			case "DescribeWebProtectionData":
				_, _ = w.Write([]byte(`{"Response":{"Data":[{"TypeKey":"zone-t1","TypeValue":[{"MetricName":"WebProtectionHit","Sum":1}]}]}}`))
			}
			return
		}
		// 和风控制台 API（3）：X-QW-Api-Key 头路由；stats 的 asOf 用无秒动态整点
		// （真实形态+账本桶落当月，跨月不翻车）
		if r.Header.Get("X-QW-Api-Key") != "" {
			switch r.URL.Path {
			case "/finance/v1/summary":
				_, _ = w.Write([]byte(`{"asOf":"2026-10-23T09:59:00+08:00","currency":"CNY","balance":0,"accruedCharges":{}}`))
			case "/metrics/v1/stats":
				asOf := time.Now().Truncate(time.Hour).Format("2006-01-02T15:04Z07:00")
				_, _ = w.Write([]byte(fmt.Sprintf(`{"asOf":%q,"success":[{"api":"Weather","hours":[10,20,30]}]}`, asOf)))
			default:
				http.NotFound(w, r)
			}
			return
		}
		if r.Header.Get("Authorization") != "Bearer good-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
			return
		}
		switch r.URL.Path {
		case "/api/monitor/usage/quota/limit":
			_, _ = w.Write([]byte(`{"data":{"level":"max","limits":[
				{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":6,"nextResetTime":1797000000000},
				{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":27,"nextResetTime":1797600000000},
				{"type":"TIME_LIMIT","usage":4000,"currentValue":120,"remaining":3880}
			]}}`))
		case "/user/balance":
			_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[
				{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}
			]}`))
		case "/api/monitor/usage/model-usage":
			_, _ = w.Write([]byte(`{"code":200,"success":true,"data":{"x_time":["10-04 00:00"],
				"modelDataList":[{"modelName":"glm-5.3","tokensUsage":[12345]}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	r := gin.New()
	mg := dbtool.New(s, filepath.Join(t.TempDir(), "dbbackups"))
	t.Cleanup(func() { mg.Close() })
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, nil, aiusage.New(s), mg, nil, nil, nil, nil); err != nil {
		t.Fatal(err, nil)
	}
	return r, token, upstream.URL, s
}

// aiReq 带会话发起请求并断言状态码。
func aiReq(t *testing.T, r *gin.Engine, token, method, path, body string, wantCode int) string {
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

// TestAiUsageFullChain 全链路（356）：服务商预设 → 建智谱账户（先测后存+首刷快照
// 即刻可读）→ 建DeepSeek 账户 → 列表带最新用量且 Key 不外泄 → 手动刷新（单/全部）
// → 编辑（Key 留空沿用/换坏 Key 拒存）→ 测试 → 排序 → 删除。
func TestAiUsageFullChain(t *testing.T) {
	r, token, upstream := newAIRouter(t)
	auth := fmt.Sprintf("/api/v1")

	// 预设清单
	res := aiReq(t, r, token, "GET", auth+"/aiusage/presets", "", http.StatusOK)
	if !strings.Contains(res, "zhipu") || !strings.Contains(res, "deepseek") {
		t.Fatalf("预设缺服务商: %s", res)
	}

	// 坏 Key 建户：先测后存，502 拒绝
	aiReq(t, r, token, "POST", auth+"/aiusage/accounts",
		fmt.Sprintf(`{"provider":"zhipu","base_url":%q,"api_key":"bad"}`, upstream), http.StatusBadGateway)

	// 未知服务商：400
	aiReq(t, r, token, "POST", auth+"/aiusage/accounts",
		`{"provider":"openai","api_key":"x"}`, http.StatusBadRequest)

	// 跨商主机混填：智谱账户填 deepseek 地址 → 400 明确报错
	aiReq(t, r, token, "POST", auth+"/aiusage/accounts",
		`{"provider":"zhipu","base_url":"https://api.deepseek.com/anthropic","api_key":"good-key"}`, http.StatusBadRequest)

	// 建智谱账户：响应自带首刷快照（5h/周窗口 + 工具月配额）
	res = aiReq(t, r, token, "POST", auth+"/aiusage/accounts",
		fmt.Sprintf(`{"provider":"zhipu","name":"智谱主力","base_url":%q,"api_key":"good-key"}`, upstream), http.StatusOK)
	if !strings.Contains(res, `"used_pct":27`) || !strings.Contains(res, `"current":120`) {
		t.Fatalf("建户响应缺首刷快照: %s", res)
	}
	if !strings.Contains(res, `"id"`) {
		t.Fatalf("建户响应缺 id: %s", res)
	}
	var id1 int64
	fmt.Sscanf(res, `{"code":0,"id":%d`, &id1)
	if id1 == 0 {
		t.Fatalf("解析建户 id 失败: %s", res)
	}

	// 建 DeepSeek 账户（名称留空回落服务商名）
	res = aiReq(t, r, token, "POST", auth+"/aiusage/accounts",
		fmt.Sprintf(`{"provider":"deepseek","base_url":%q,"api_key":"good-key"}`, upstream), http.StatusOK)
	var id2 int64
	fmt.Sscanf(res, `{"code":0,"id":%d`, &id2)
	if id2 == 0 {
		t.Fatalf("解析 DeepSeek 建户 id 失败: %s", res)
	}

	// 列表：两账户各带最新用量；密钥绝不外泄
	res = aiReq(t, r, token, "GET", auth+"/aiusage/accounts", "", http.StatusOK)
	if !strings.Contains(res, `"kind":"quota"`) || !strings.Contains(res, `"kind":"balance"`) {
		t.Fatalf("列表应含两类快照: %s", res)
	}
	if !strings.Contains(res, "DeepSeek") { // 名称留空回落服务商 Label
		t.Fatalf("DeepSeek 账户名称应回落服务商名: %s", res)
	}
	if strings.Contains(res, "good-key") || strings.Contains(res, "api_key") {
		t.Fatalf("列表不得外泄 API Key: %s", res)
	}
	if !strings.Contains(res, `"has_key":true`) {
		t.Fatalf("列表应带 has_key: %s", res)
	}

	// 手动刷新单账户：返回整份账户视图（usage_at 已更新）
	res = aiReq(t, r, token, "POST", fmt.Sprintf("%s/aiusage/accounts/%d/refresh", auth, id1), "", http.StatusOK)
	if !strings.Contains(res, `"usage_at":`) || !strings.Contains(res, `"windows":[`) {
		t.Fatalf("单账户刷新响应不符: %s", res)
	}

	// 全部刷新
	aiReq(t, r, token, "POST", auth+"/aiusage/refresh", "", http.StatusOK)

	// 编辑：Key 留空沿用（base_url 保留测试上游——清空会回落智谱官方站点），
	// 再刷新仍成功=旧 Key 还在用
	aiReq(t, r, token, "PUT", fmt.Sprintf("%s/aiusage/accounts/%d", auth, id1),
		fmt.Sprintf(`{"provider":"zhipu","name":"智谱改名","base_url":%q}`, upstream), http.StatusOK)
	res = aiReq(t, r, token, "POST", fmt.Sprintf("%s/aiusage/accounts/%d/refresh", auth, id1), "", http.StatusOK)
	if !strings.Contains(res, "智谱改名") || !strings.Contains(res, `"windows":[`) {
		t.Fatalf("Key 留空编辑后应沿用旧 Key 正常拉取: %s", res)
	}

	// 编辑换坏 Key：先测后存拒绝
	aiReq(t, r, token, "PUT", fmt.Sprintf("%s/aiusage/accounts/%d", auth, id1),
		`{"provider":"zhipu","name":"智谱改名","api_key":"bad"}`, http.StatusBadGateway)

	// 测试接口：给另一把 Key 试连（不落库）
	aiReq(t, r, token, "POST", fmt.Sprintf("%s/aiusage/accounts/%d/test", auth, id1),
		`{"api_key":"good-key"}`, http.StatusOK)
	aiReq(t, r, token, "POST", fmt.Sprintf("%s/aiusage/accounts/%d/test", auth, id1),
		`{"api_key":"wrong"}`, http.StatusBadGateway)

	// 删除
	aiReq(t, r, token, "DELETE", fmt.Sprintf("%s/aiusage/accounts/%d", auth, id1), "", http.StatusOK)
	res = aiReq(t, r, token, "GET", auth+"/aiusage/accounts", "", http.StatusOK)
	if strings.Contains(res, "智谱改名") {
		t.Fatalf("删除后不应再出现: %s", res)
	}

	// 未登录：401
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", auth+"/aiusage/accounts", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，实际 %d", w.Code)
	}
}

// TestAiEdgeOneFullChain EdgeOne 账户全链路（393）：预设含 edgeone → 缺密钥对
// 拒建/非法端点拒绝 → 正常建户（先测后存 + 首刷四指标快照）→ 列表密钥不外泄、
// zone_ids 归一 → 编辑密钥留空沿用 → 测试接口换密钥试连 → 删除。
func TestAiEdgeOneFullChain(t *testing.T) {
	r, token, upstream := newAIRouter(t)
	auth := "/api/v1"

	res := aiReq(t, r, token, "GET", auth+"/aiusage/presets", "", http.StatusOK)
	if !strings.Contains(res, "edgeone") {
		t.Fatalf("预设应含 edgeone: %s", res)
	}

	// 缺 SecretKey：400
	aiReq(t, r, token, "POST", auth+"/aiusage/accounts",
		fmt.Sprintf(`{"provider":"edgeone","base_url":%q,"secret_id":"sid"}`, upstream), http.StatusBadRequest)

	// 端点既非国际站也非国内站：400
	aiReq(t, r, token, "POST", auth+"/aiusage/accounts",
		`{"provider":"edgeone","base_url":"https://console.example.com","secret_id":"sid","secret_key":"skey"}`, http.StatusBadRequest)

	// 正常建户（站点过滤带重复项，应归一去重）：响应自带首刷快照
	res = aiReq(t, r, token, "POST", auth+"/aiusage/accounts",
		fmt.Sprintf(`{"provider":"edgeone","name":"EdgeOne站","base_url":%q,"secret_id":"sid","secret_key":"skey","zone_ids":"zone-t1, zone-t1"}`, upstream), http.StatusOK)
	if !strings.Contains(res, `"kind":"edgeone"`) || !strings.Contains(res, `"peak_bandwidth":24190000`) ||
		!strings.Contains(res, `"protect_hits":1`) {
		t.Fatalf("建户响应缺 EdgeOne 首刷快照: %s", res)
	}
	// 假上游未实现详情端点（398/410）→ 回源/TopN/流量排行/DDoS 块独立降级缺省，
	// 缓存命中与响应流量收到主数据空指标落 0（支持但无数据语义），主指标不受影响
	if !strings.Contains(res, `"detail":{"cache_hit_flux":0,"out_flux":0}`) {
		t.Fatalf("详情块应各块独立降级: %s", res)
	}
	var id int64
	fmt.Sscanf(res, `{"code":0,"id":%d`, &id)
	if id == 0 {
		t.Fatalf("解析建户 id 失败: %s", res)
	}

	// 列表：zone_ids 归一、has_key 依密钥对判定、密钥绝不外泄
	res = aiReq(t, r, token, "GET", auth+"/aiusage/accounts", "", http.StatusOK)
	if !strings.Contains(res, `"zone_ids":"zone-t1"`) {
		t.Fatalf("zone_ids 应归一去重: %s", res)
	}
	if strings.Contains(res, `"secret_id"`) || strings.Contains(res, `"secret_key"`) || strings.Contains(res, "skey") {
		t.Fatalf("列表不得外泄密钥字段: %s", res)
	}
	if !strings.Contains(res, `"has_key":true`) {
		t.Fatalf("EdgeOne 账户应 has_key: %s", res)
	}

	// 编辑：密钥留空沿用（base_url 保留假上游），刷新仍成功
	aiReq(t, r, token, "PUT", fmt.Sprintf("%s/aiusage/accounts/%d", auth, id),
		fmt.Sprintf(`{"provider":"edgeone","name":"EdgeOne改名","base_url":%q,"zone_ids":"zone-t1"}`, upstream), http.StatusOK)
	res = aiReq(t, r, token, "POST", fmt.Sprintf("%s/aiusage/accounts/%d/refresh", auth, id), "", http.StatusOK)
	if !strings.Contains(res, "EdgeOne改名") || !strings.Contains(res, `"kind":"edgeone"`) {
		t.Fatalf("密钥留空编辑后应沿用旧密钥正常拉取: %s", res)
	}

	// 测试接口：坏 SecretId 试连 502、原密钥对 200
	aiReq(t, r, token, "POST", fmt.Sprintf("%s/aiusage/accounts/%d/test", auth, id),
		`{"secret_id":"badsid"}`, http.StatusBadGateway)
	aiReq(t, r, token, "POST", fmt.Sprintf("%s/aiusage/accounts/%d/test", auth, id),
		`{"secret_id":"sid","secret_key":"skey"}`, http.StatusOK)

	// 删除
	aiReq(t, r, token, "DELETE", fmt.Sprintf("%s/aiusage/accounts/%d", auth, id), "", http.StatusOK)
	res = aiReq(t, r, token, "GET", auth+"/aiusage/accounts", "", http.StatusOK)
	if strings.Contains(res, "EdgeOne改名") {
		t.Fatalf("删除后不应再出现: %s", res)
	}
}

// TestAiUsageQWeatherSystemGuard 和风系统账户行（3）：手动创建/删除均被拒，
// 经天气配置同步出的行列表可见。
func TestAiUsageQWeatherSystemGuard(t *testing.T) {
	r, token, upstream, s := newAIRouterAll(t)
	auth := "/api/v1"

	// 前台天气配了和风 → 同步出系统行
	if err := s.SyncQWeatherSystemAccount(&store.PageSettings{WeatherEnabled: true, WeatherProvider: "qweather", WeatherKey: "qw-key", WeatherHost: "demo.qweatherapi.com"}); err != nil {
		t.Fatal(err)
	}

	// 手动创建 provider=qweather 被拒（不在服务商白名单）
	res := aiReq(t, r, token, "POST", auth+"/aiusage/accounts", `{"provider":"qweather","api_key":"x"}`, http.StatusBadRequest)
	if !strings.Contains(res, "自动接入") {
		t.Fatalf("创建应提示自动接入: %s", res)
	}

	// 列表含系统行；base_url 改指假上游（同步按真实形态写专属 Host，刷新断言要走假上游）
	res = aiReq(t, r, token, "GET", auth+"/aiusage/accounts", "", http.StatusOK)
	var list struct {
		Accounts []struct {
			ID       int64  `json:"id"`
			Provider string `json:"provider"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal([]byte(res), &list); err != nil {
		t.Fatal(err)
	}
	var qid int64
	for _, a := range list.Accounts {
		if a.Provider == "qweather" {
			qid = a.ID
		}
	}
	if qid == 0 {
		t.Fatalf("列表应含和风系统行: %s", res)
	}
	if err := s.UpdateAiAccount(&store.AiAccount{ID: qid, Name: "和风天气", Provider: "qweather", BaseURL: upstream}); err != nil {
		t.Fatal(err)
	}

	// 删除被拒（该行由天气配置自动维护）
	res = aiReq(t, r, token, "DELETE", fmt.Sprintf("%s/aiusage/accounts/%d", auth, qid), "", http.StatusBadRequest)
	if !strings.Contains(res, "自动管理") {
		t.Fatalf("删除应被拒: %s", res)
	}

	// 手动刷新：完整桶落账本 + 快照回填当月合计（3 当月请求量口径）
	res = aiReq(t, r, token, "POST", fmt.Sprintf("%s/aiusage/accounts/%d/refresh", auth, qid), "", http.StatusOK)
	nowH := time.Now().Truncate(time.Hour)
	var want int64
	for i, v := range []int64{10, 20, 30} { // 桶起点=asOf-2h..asOf；末桶=进行中小时不入账（<asOf 才完整）
		bt := nowH.Add(time.Duration(i-2) * time.Hour)
		if bt.Before(nowH) && bt.Format("2006-01") == nowH.Format("2006-01") {
			want += v
		}
	}
	var rr struct {
		Account struct {
			Usage *struct {
				Kind string `json:"kind"`
				Wx   *struct {
					MonthSuccess *int64 `json:"month_success"`
				} `json:"wx"`
			} `json:"usage"`
		} `json:"account"`
	}
	if err := json.Unmarshal([]byte(res), &rr); err != nil {
		t.Fatal(err)
	}
	if rr.Account.Usage == nil || rr.Account.Usage.Kind != "qweather" || !strings.Contains(res, `"ts":`) {
		t.Fatalf("刷新应回和风快照且逐小时带桶时间戳: %s", res)
	}
	if want > 0 && (rr.Account.Usage.Wx == nil || rr.Account.Usage.Wx.MonthSuccess == nil || *rr.Account.Usage.Wx.MonthSuccess != want) {
		t.Fatalf("当月合计应回填 %d: %s", want, res)
	}
}
