package aiusage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// TestDegradedDedupe 降级日志去重器（407）：连续相同失败只在首次记录，报错
// 变化与恢复（空 msg）都记录，持续正常静音，不同账户独立。
func TestDegradedDedupe(t *testing.T) {
	id := int64(424242)
	t.Cleanup(func() { clearDegraded(id) })
	if !shouldLogDegraded(id, "回源", "boom") {
		t.Fatal("首次失败应记录")
	}
	if shouldLogDegraded(id, "回源", "boom") {
		t.Fatal("连续相同失败应静音")
	}
	if !shouldLogDegraded(id, "回源", "changed") {
		t.Fatal("报错变化应记录")
	}
	if !shouldLogDegraded(id, "回源", "") {
		t.Fatal("恢复应记录")
	}
	if shouldLogDegraded(id, "回源", "") {
		t.Fatal("持续正常应静音")
	}
	if !shouldLogDegraded(id+1, "回源", "boom") {
		t.Fatal("不同账户应独立记录")
	}
}

// TestTC3Sign TC3-HMAC-SHA256 签名金标准（与独立 Python 实现交叉验证过的向量，
// 含规范请求串的换行布局与 Credential 日期取 UTC）。
func TestTC3Sign(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	got := tc3Sign("AKIDEXAMPLE12345678", "Gu5t9xGARNpq86q98mKbeE1HmGgXSKcB",
		"teo.intl.tencentcloudapi.com", "DescribeZones", `{"Limit":1,"Offset":0}`, now)
	want := "TC3-HMAC-SHA256 Credential=AKIDEXAMPLE12345678/2026-10-04/teo/tc3_request, " +
		"SignedHeaders=content-type;host;x-tc-action, " +
		"Signature=20aa26b26ec782916d017be8d92581c23485384fa0dcfe88318db3be23b7c9f5"
	if got != want {
		t.Fatalf("签名不符:\n got %s\nwant %s", got, want)
	}
}

// newEdgeOneUpstream 假 TEO 端点：按 X-TC-Action 路由（根路径 POST），记录各
// Action 命中次数与最后一次请求参数。protectStatus 置 >0 时防护接口回该 HTTP
// 状态码（模拟国内站端点无此接口），负数按正常返回防护数据。
func newEdgeOneUpstream(t *testing.T) (*httptest.Server, map[string]int, func(action string) []map[string]any, *int) {
	t.Helper()
	var mu sync.Mutex // 详情块并发拉取（407），假上游的记账 map 必须加锁
	hits := map[string]int{}
	lastBody := map[string][]map[string]any{}
	protectStatus := -1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.Header.Get("X-TC-Action")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		hits[action]++
		lastBody[action] = append(lastBody[action], body)
		mu.Unlock()
		var out string
		switch action {
		case "DescribeZones":
			out = `{"Response":{"TotalCount":2,"Zones":[{"ZoneId":"zone-a","ZoneName":"a.com"},{"ZoneId":"zone-b","ZoneName":"b.com"}],"RequestId":"r"}}`
		case "DescribeTimingL7AnalysisData":
			// 主查询（3 指标）与单指标查询（缓存命中/响应流量）按入参分流
			mns, _ := body["MetricNames"].([]any)
			if len(mns) > 0 && mns[0] == "l7Flow_hit_outFlux" {
				out = `{"Response":{"Data":[{"TypeKey":"zone-a","TypeValue":[{"MetricName":"l7Flow_hit_outFlux","Sum":77}]}]}}`
				break
			}
			if len(mns) > 0 && mns[0] == "l7Flow_outFlux" {
				out = `{"Response":{"Data":[{"TypeKey":"zone-a","TypeValue":[{"MetricName":"l7Flow_outFlux","Sum":900}]}]}}`
				break
			}
			out = `{"Response":{"Data":[
				{"TypeKey":"zone-a","TypeValue":[
					{"MetricName":"l7Flow_flux","Sum":1000},{"MetricName":"l7Flow_bandwidth","Max":500},{"MetricName":"l7Flow_request","Sum":10}]},
				{"TypeKey":"zone-b","TypeValue":[
					{"MetricName":"l7Flow_flux","Sum":250},{"MetricName":"l7Flow_bandwidth","Max":700},{"MetricName":"l7Flow_request","Sum":5}]}]}}`
		case "DescribeTimingL7OriginPullData":
			out = `{"Response":{"Data":[{"TypeKey":"zone-a","TypeValue":[
				{"MetricName":"l7Flow_request_hy","Sum":60},{"MetricName":"l7Flow_outFlux_hy","Sum":1200}]}]}}`
		case "DescribeTopL7AnalysisData":
			// MetricName 单值：域名/URL/状态码各回一组多站点明细（验证合并累加）
			switch body["MetricName"] {
			case "l7Flow_request_domain":
				out = `{"Response":{"Data":[
					{"TypeKey":"zone-a","DetailData":[{"Key":"a.com","Value":7},{"Key":"a.com","Value":3},{"Key":"cdn.com","Value":1}]},
					{"TypeKey":"zone-b","DetailData":[{"Key":"b.com","Value":5}]}]}}`
			case "l7Flow_request_url":
				out = `{"Response":{"Data":[{"TypeKey":"zone-a","DetailData":[{"Key":"/index.html","Value":8},{"Key":"/a.js","Value":2}]}]}}`
			case "l7Flow_request_statusCode":
				out = `{"Response":{"Data":[{"TypeKey":"zone-a","DetailData":[{"Key":"200","Value":9},{"Key":"404","Value":3},{"Key":"502","Value":1}]}]}}`
			case "l7Flow_outFlux_domain":
				out = `{"Response":{"Data":[{"TypeKey":"zone-a","DetailData":[{"Key":"dog.com","Value":830},{"Key":"web.com","Value":70}]}]}}`
			case "l7Flow_outFlux_sip":
				out = `{"Response":{"Data":[{"TypeKey":"zone-a","DetailData":[{"Key":"1.2.3.4","Value":450},{"Key":"5.6.7.8","Value":260}]}]}}`
			case "l7Flow_outFlux_referers":
				out = `{"Response":{"Data":[{"TypeKey":"zone-a","DetailData":[{"Key":"http://r/1","Value":120}]}]}}`
			case "l7Flow_outFlux_url":
				out = `{"Response":{"Data":[{"TypeKey":"zone-a","DetailData":[{"Key":"/game.js","Value":204}]}]}}`
			}
		case "DescribeDDoSAttackData":
			out = `{"Response":{"Data":[{"TypeKey":"zone-a","TypeValue":[
				{"MetricName":"ddos_attackMaxBandwidth","Max":100000},{"MetricName":"ddos_attackMaxPackageRate","Max":2000}]}]}}`
		case "DescribeWebProtectionData":
			if protectStatus > 0 {
				w.WriteHeader(protectStatus)
				_, _ = w.Write([]byte(`404 page not found`))
				return
			}
			out = `{"Response":{"Data":[{"TypeKey":"zone-a","TypeValue":[{"MetricName":"WebProtectionHit","Sum":1}]}]}}`
		default:
			out = `{"Response":{"Error":{"Code":"InvalidAction","Message":"unknown"}}}`
		}
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	return srv, hits, func(action string) []map[string]any { return lastBody[action] }, &protectStatus
}

// TestFetchEdgeOne 全链路解析：DescribeZones 自动拉站点 → 时序四指标逐站点聚合
// （流量/请求累加、峰值取大）→ 前窗口环比基期齐备；请求参数校验 5min 粒度、
// 站点清单与指标名。
func TestFetchEdgeOne(t *testing.T) {
	srv, hits, lastBody, _ := newEdgeOneUpstream(t)
	snap, err := Fetch(store.AiAccount{Provider: "edgeone", BaseURL: srv.URL, SecretID: "sid", SecretKey: "skey"})
	if err != nil {
		t.Fatal(err)
	}
	if hits["DescribeZones"] != 1 {
		t.Fatalf("无站点过滤应 DescribeZones 一次，实际 %d", hits["DescribeZones"])
	}
	if snap.Kind != "edgeone" || snap.EdgeOne == nil {
		t.Fatalf("快照 kind 不符: %+v", snap)
	}
	eo := snap.EdgeOne
	if eo.ZoneCount != 2 {
		t.Fatalf("站点数应为 2: %+v", eo)
	}
	if eo.TrafficBytes != 1250 || eo.PeakBandwidth != 700 || eo.Requests != 15 {
		t.Fatalf("四指标聚合不符（流量/请求累加、峰值取大）: %+v", eo)
	}
	if eo.ProtectHits == nil || *eo.ProtectHits != 1 {
		t.Fatalf("防护命中应为 1: %+v", eo)
	}
	// 前窗口（环比基期）：假上游两窗口同数据，四项都应在
	if eo.PrevTraffic == nil || *eo.PrevTraffic != 1250 || eo.PrevPeak == nil || eo.PrevRequests == nil || eo.PrevHits == nil {
		t.Fatalf("环比基期应齐备: %+v", eo)
	}
	// 请求参数：主查询带 3 指标 + 5min 粒度 + 全部站点（hit 为同 Action 的另一次调用）
	var body map[string]any
	for _, b := range lastBody("DescribeTimingL7AnalysisData") {
		if m, _ := b["MetricNames"].([]any); len(m) == 3 {
			body = b
		}
	}
	if body == nil {
		t.Fatal("未找到 L7 主查询调用")
	}
	if body["Interval"] != "5min" {
		t.Fatalf("Interval 应为 5min: %v", body["Interval"])
	}
	zids, _ := body["ZoneIds"].([]any)
	if len(zids) != 2 {
		t.Fatalf("ZoneIds 应含两个站点: %v", zids)
	}

	// 详情块（398）：缓存命中/回源/状态码/TopN/DDoS 全齐且口径正确
	d := eo.Detail
	if d == nil || d.CacheHitFlux == nil || *d.CacheHitFlux != 77 {
		t.Fatalf("缓存命中流量不符: %+v", d)
	}
	if d.OriginReq == nil || *d.OriginReq != 60 || d.OriginFlux == nil || *d.OriginFlux != 1200 {
		t.Fatalf("回源数据不符: %+v", d)
	}
	wantTop := []store.EdgeOneTopItem{{Key: "a.com", Count: 10}, {Key: "b.com", Count: 5}, {Key: "cdn.com", Count: 1}}
	if !reflect.DeepEqual(d.TopDomains, wantTop) {
		t.Fatalf("Top 域名应跨站点合并同名累加并降序: %+v", d.TopDomains)
	}
	if len(d.TopURLs) != 2 || d.TopURLs[0].Key != "/index.html" || d.TopURLs[0].Count != 8 {
		t.Fatalf("Top URL 不符: %+v", d.TopURLs)
	}
	if len(d.StatusCodes) != 3 || d.StatusCodes[0].Key != "200" {
		t.Fatalf("状态码分布不符: %+v", d.StatusCodes)
	}
	if d.DDoSPeakBps == nil || *d.DDoSPeakBps != 100000 || d.DDoSPeakPps == nil || *d.DDoSPeakPps != 2000 {
		t.Fatalf("DDoS 峰值不符: %+v", d)
	}
	// 响应流量视角（410）：总值 + 四个流量排行
	if d.OutFlux == nil || *d.OutFlux != 900 {
		t.Fatalf("响应流量总值不符: %+v", d)
	}
	if len(d.FluxHosts) != 2 || d.FluxHosts[0].Key != "dog.com" || d.FluxHosts[0].Count != 830 {
		t.Fatalf("Host 排行不符: %+v", d.FluxHosts)
	}
	if len(d.FluxIPs) != 2 || d.FluxIPs[0].Key != "1.2.3.4" {
		t.Fatalf("客户端 IP 排行不符: %+v", d.FluxIPs)
	}
	if len(d.FluxReferers) != 1 || d.FluxReferers[0].Key != "http://r/1" {
		t.Fatalf("Referer 排行不符: %+v", d.FluxReferers)
	}
	if len(d.FluxURLs) != 1 || d.FluxURLs[0].Key != "/game.js" || d.FluxURLs[0].Count != 204 {
		t.Fatalf("URL Path 流量排行不符: %+v", d.FluxURLs)
	}
}

// TestFetchEdgeOneZoneFilter 配了站点 ID 过滤：不再 DescribeZones，按清洗后的
// 去重清单查询。
func TestFetchEdgeOneZoneFilter(t *testing.T) {
	srv, hits, _, _ := newEdgeOneUpstream(t)
	snap, err := Fetch(store.AiAccount{Provider: "edgeone", BaseURL: srv.URL,
		SecretID: "sid", SecretKey: "skey", ZoneIDs: " zone-a, zone-b; zone-a "})
	if err != nil {
		t.Fatal(err)
	}
	if hits["DescribeZones"] != 0 {
		t.Fatalf("配置站点过滤后不应 DescribeZones，实际 %d", hits["DescribeZones"])
	}
	if snap.EdgeOne.ZoneCount != 2 {
		t.Fatalf("应按去重后的 2 个站点查询: %+v", snap.EdgeOne)
	}
}

// TestFetchEdgeOneProtectionDown 防护接口 404（国内站端点无此接口）：防护命中
// 降级缺省，其余三项与环比照常。
func TestFetchEdgeOneProtectionDown(t *testing.T) {
	srv, _, _, protectStatus := newEdgeOneUpstream(t)
	*protectStatus = http.StatusNotFound
	snap, err := Fetch(store.AiAccount{Provider: "edgeone", BaseURL: srv.URL, SecretID: "sid", SecretKey: "skey"})
	if err != nil {
		t.Fatal(err)
	}
	eo := snap.EdgeOne
	if eo.ProtectHits != nil || eo.TrafficBytes != 1250 || eo.PeakBandwidth != 700 || eo.Requests != 15 {
		t.Fatalf("防护降级后其余三项应照常: %+v", eo)
	}
	if eo.PrevTraffic == nil || eo.PrevHits != nil {
		t.Fatalf("环比流量应在、命中应缺省: %+v", eo)
	}
}

// TestEdgeOneErrors 业务错误（Response.Error）带出 Code/Message；账号无站点报错；
// 缺密钥对直接拒绝。
func TestEdgeOneErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Response":{"Error":{"Code":"AuthFailure.SignatureFailure","Message":"签名错误"},"RequestId":"r"}}`))
	}))
	defer srv.Close()
	_, err := Fetch(store.AiAccount{Provider: "edgeone", BaseURL: srv.URL, SecretID: "sid", SecretKey: "skey"})
	if err == nil || !strings.Contains(err.Error(), "AuthFailure.SignatureFailure") {
		t.Fatalf("业务错误应带出 Code: %v", err)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Response":{"TotalCount":0,"Zones":[]}}`))
	}))
	defer srv2.Close()
	_, err = Fetch(store.AiAccount{Provider: "edgeone", BaseURL: srv2.URL, SecretID: "sid", SecretKey: "skey"})
	if err == nil || !strings.Contains(err.Error(), "没有可用站点") {
		t.Fatalf("空账号应报无站点: %v", err)
	}

	if _, err := Fetch(store.AiAccount{Provider: "edgeone", BaseURL: srv2.URL, SecretID: "sid"}); err == nil ||
		!strings.Contains(err.Error(), "SecretId/SecretKey") {
		t.Fatalf("缺密钥对应拒绝: %v", err)
	}
}
