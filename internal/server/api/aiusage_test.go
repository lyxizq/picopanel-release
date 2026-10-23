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
	return r, token, upstream.URL
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
