package aiusage

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// TestFetchQWeather 控制台 API 两端点契约（3）：X-QW-Api-Key 头鉴权、财务汇总
// （余额/应计/待付账单/资源包）与请求量统计（分 API 合计降序、逐小时跨 API
// 聚合、标签自 asOf 向前推）解析，Kind=qweather。
func TestFetchQWeather(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-QW-Api-Key") != "qw-key" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/finance/v1/summary":
			_, _ = w.Write([]byte(`{"metadata":{"tag":"t1"},"asOf":"2026-10-23T09:59+08:00","currency":"CNY","balance":-17.54,
				"accruedCharges":{"previousDay":0.5,"thisMonth":12.3,"sinceLastBill":8.1},
				"pendingBills":[{"number":"B1","date":"2026-09-30T00:00:00+08:00","type":"PayAsYouGo","status":"unpaid","amount":7.6,"amountDue":7.6,"dueDate":"2026-10-30T00:00:00+08:00"}],
				"availableResourcePlans":[{"billNumber":"B2","status":"active","requests":50000,"utilized":12000,"commitments":0,"effectiveTime":"2026-10-01T00:00:00+08:00"}]}`))
		case "/metrics/v1/stats":
			// asOf 用无秒形态（文档示例 2025-05-12T02:59Z 同款）：钉死多格式解析
			_, _ = w.Write([]byte(`{"metadata":{"tag":"t2"},"asOf":"2026-10-23T09:59+08:00",
				"success":[{"api":"Weather","hours":[10,20,30]},{"api":"AirQuality","hours":[5,5,5]}],
				"errors":[{"api":"Weather","hours":[0,1,0]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a := store.AiAccount{Name: "和风天气", Provider: store.QWeatherProvider, BaseURL: srv.URL, APIKey: "qw-key"}
	snap, err := fetchQWeather(a)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != "qweather" || snap.Wx == nil || snap.Wx.Finance == nil || snap.Wx.Stats == nil {
		t.Fatalf("快照结构不符: %+v", snap)
	}
	f := snap.Wx.Finance
	if f.Currency != "CNY" || f.Balance == nil || *f.Balance != -17.54 || f.ThisMonth == nil || *f.ThisMonth != 12.3 || f.PrevDay == nil || *f.PrevDay != 0.5 {
		t.Fatalf("财务解析不符: %+v", f)
	}
	if len(f.PendingBills) != 1 || f.PendingBills[0].AmountDue != 7.6 || f.PendingBills[0].Status != "unpaid" {
		t.Fatalf("待付账单解析不符: %+v", f.PendingBills)
	}
	if len(f.ResourcePlans) != 1 || f.ResourcePlans[0].Requests == nil || *f.ResourcePlans[0].Requests != 50000 || *f.ResourcePlans[0].Utilized != 12000 {
		t.Fatalf("资源包解析不符: %+v", f.ResourcePlans)
	}
	st := snap.Wx.Stats
	if st.Success != 75 || st.Errors != 1 {
		t.Fatalf("请求量合计不符: success=%d errors=%d", st.Success, st.Errors)
	}
	if len(st.PerAPI) != 2 || st.PerAPI[0].API != "Weather" || st.PerAPI[0].Success != 60 || st.PerAPI[0].Errors != 1 || st.PerAPI[1].Success != 15 {
		t.Fatalf("分 API 排序/合计不符: %+v", st.PerAPI)
	}
	// 逐小时：合计 [15,25,35]，标签自 asOf（09:59+08:00 截整点）桶向前推 →
	// 07:00/08:00/09:00，Ts=各桶起始秒级（账本用）
	loc := time.FixedZone("CST", 8*3600)
	wantTs := func(hour int) int64 { return time.Date(2026, 10, 23, hour, 0, 0, 0, loc).Unix() }
	if len(st.Hours) != 3 || st.Hours[0].Label != "07:00" || st.Hours[2].Label != "09:00" ||
		st.Hours[2].Success != 35 || st.Hours[1].Success != 25 || st.Hours[1].Errors != 1 {
		t.Fatalf("逐小时聚合不符: %+v", st.Hours)
	}
	if st.Hours[0].Ts != wantTs(7) || st.Hours[1].Ts != wantTs(8) || st.Hours[2].Ts != wantTs(9) {
		t.Fatalf("桶起始时间戳不符: %+v", st.Hours)
	}
}

// TestFetchQWeatherDegraded 单块失败不连累另一块（best-effort 独立降级），双失败才报错；
// 请求量空数组是合法态（新账号/无流量），落全 0 不报错。
func TestFetchQWeatherDegraded(t *testing.T) {
	mode := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "both" || (mode == "finance" && r.URL.Path == "/finance/v1/summary") || (mode == "stats" && r.URL.Path == "/metrics/v1/stats") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		switch r.URL.Path {
		case "/finance/v1/summary":
			_, _ = w.Write([]byte(`{"asOf":"2026-10-23T09:59:00+08:00","currency":"CNY","balance":0,"accruedCharges":{}}`))
		case "/metrics/v1/stats":
			_, _ = w.Write([]byte(`{"asOf":"2026-10-23T09:59:00+08:00"}`))
		}
	}))
	defer srv.Close()
	a := store.AiAccount{Name: "和风天气", Provider: store.QWeatherProvider, BaseURL: srv.URL, APIKey: "qw-key"}

	mode = "finance"
	snap, err := fetchQWeather(a)
	if err != nil || snap.Wx.Finance != nil || snap.Wx.Stats == nil || snap.Wx.Stats.Success != 0 {
		t.Fatalf("财务失败应降级只留请求量: err=%v snap=%+v", err, snap.Wx)
	}
	mode = "stats"
	snap, err = fetchQWeather(a)
	if err != nil || snap.Wx.Stats != nil || snap.Wx.Finance == nil {
		t.Fatalf("请求量失败应降级只留财务: err=%v snap=%+v", err, snap.Wx)
	}
	mode = "both"
	if _, err = fetchQWeather(a); err == nil {
		t.Fatal("双失败应整体报错")
	}

	// 凭据缺失直接报错，不打上游
	bare := store.AiAccount{Provider: store.QWeatherProvider, BaseURL: srv.URL, APIKey: ""}
	if _, err := fetchQWeather(bare); err == nil || !strings.Contains(err.Error(), "API Key") {
		t.Fatalf("缺 Key 应报错: %v", err)
	}
}
