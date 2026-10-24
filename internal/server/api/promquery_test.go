package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// newPromRouter 起一个只挂即时查询路由的测试服务，数据种子复用 snapshot 测试的 seedServer。
func newPromRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	r := gin.New()
	h := &PromQueryHandler{st: s, token: "tok123", offlineAfter: 120}
	r.GET("/api/v1/query", h.Instant)
	return r, s
}

// promQuery 发一次即时查询（令牌走 X-Snapshot-Token 头；token 写进 query
// 会落访问/反代日志，标签携带通道已移除），返回 HTTP 码与解析后的 vector。
func promQuery(t *testing.T, r *gin.Engine, lzq string) (int, promVector) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/query", nil)
	req.Header.Set("X-Snapshot-Token", "tok123")
	q := req.URL.Query()
	q.Set("query", lzq)
	req.URL.RawQuery = q.Encode()
	r.ServeHTTP(w, req)
	var v promVector
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return w.Code, v
}

// sampleValue 取唯一样本的数值。
func sampleValue(t *testing.T, v promVector) float64 {
	t.Helper()
	if len(v.Data.Result) != 1 {
		t.Fatalf("样本数 = %d, 期望 1", len(v.Data.Result))
	}
	s, _ := v.Data.Result[0].Value[1].(string)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("样本值无法解析: %v", v.Data.Result[0].Value[1])
	}
	return f
}

// TestPromQueryAuth 令牌两通道：查询串标签（小部件唯一可用）与请求头；
// token 查询参数已移除（会落访问日志）。
func TestPromQueryAuth(t *testing.T) {
	r, s := newPromRouter(t)
	seedServer(t, s)

	// 无令牌 / 错误令牌 → 401（裸请求，不带 header）
	for _, hdr := range []string{"", "wrong"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/query", nil)
		if hdr != "" {
			req.Header.Set("X-Snapshot-Token", hdr)
		}
		q := req.URL.Query()
		q.Set("query", `lz_cpu{server="web-01"}`)
		req.URL.RawQuery = q.Encode()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("令牌 %q 应 401, got %d", hdr, w.Code)
		}
	}
	// 令牌写在查询串标签里：通道已移除（token 会随 query 落访问/反代日志）
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/query", nil)
	q := req.URL.Query()
	q.Set("query", `lz_cpu{server="web-01"}`)
	req.URL.RawQuery = q.Encode()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("标签携带令牌应 401（通道已移除）: %d", w.Code)
	}

	// 令牌走请求头
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/query", nil)
	req.Header.Set("X-Snapshot-Token", "tok123")
	q = req.URL.Query()
	q.Set("query", `lz_cpu{server="web-01"}`)
	req.URL.RawQuery = q.Encode()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("请求头携带令牌应 200, got %d: %s", w.Code, w.Body.String())
	}

	// token 查询参数已不支持（原来走 ?token= 的调用方改用头）
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/query?token=tok123&query="+url.QueryEscape(`lz_cpu{server="web-01"}`), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("查询参数携带令牌应 401（通道已移除）, got %d", w.Code)
	}
}

// TestPromQueryValues 各指标取值（返回原始值不四舍五入，与快照接口的 round1 口径不同）。
func TestPromQueryValues(t *testing.T) {
	r, s := newPromRouter(t)
	seedServer(t, s)

	cases := []struct {
		q    string
		want float64
	}{
		{`lz_cpu{server="web-01"}`, 12.34},
		{`lz_mem{server="web-01"}`, 56.78},
		{`lz_mem_used{server="web-01"}`, 2 << 30},
		{`lz_mem_total{server="web-01"}`, 4 << 30},
		{`lz_swap{server="web-01"}`, 0},
		{`lz_load1{server="web-01"}`, 0.5},
		{`lz_uptime{server="web-01"}`, 90061},
		{`lz_tcp_est{server="web-01"}`, 42},
		{`lz_disk{server="web-01"}`, 93.4},            // 缺省取最大
		{`lz_disk{server="web-01",mount="/"}`, 71.25}, // 指定挂载点
		{`lz_io{server="web-01"}`, 18.2},
		{`lz_net_rx{server="web-01"}`, 1536}, // 仅 eth0（服务器配置）
		{`lz_net_tx{server="web-01"}`, 512},
		{`lz_net_rx{server="web-01",device="eth1"}`, 99999}, // device 直选单网卡
		{`lz_online{server="web-01"}`, 1},
		{`lz_firing{server="web-01"}`, 0},
		{`lz_cpu{server="1"}`, 12.34}, // 数字 ID 同样可查
	}
	for _, c := range cases {
		code, v := promQuery(t, r, c.q)
		if code != http.StatusOK {
			t.Fatalf("%s 应 200, got %d", c.q, code)
		}
		if got := sampleValue(t, v); got != c.want {
			t.Fatalf("%s = %v, 期望 %v", c.q, got, c.want)
		}
	}
}

// TestPromQueryEmptyAndErrors 无匹配返回空 vector；语法错误与未知指标返回 400。
func TestPromQueryEmptyAndErrors(t *testing.T) {
	r, s := newPromRouter(t)
	seedServer(t, s)

	// 服务器不存在 / 未指定 server / 挂载点不存在 → 200 空 result（无匹配语义）
	for _, q := range []string{
		`lz_cpu{server="nope"}`,
		`lz_cpu`,
		`lz_disk{server="web-01",mount="/nope"}`,
	} {
		if code, v := promQuery(t, r, q); code != http.StatusOK || len(v.Data.Result) != 0 {
			t.Fatalf("%s 应 200 空 result, got %d %+v", q, code, v)
		}
	}
	// 非 lz_ 指标 / 缺花括号 / 未定义指标 / 值未加引号 / 空 query → 400
	for _, q := range []string{
		`node_cpu{server="web-01"}`,
		`lz_cpu server="web-01"`,
		`lz_nope{server="web-01"}`,
		`lz_cpu{server=web-01}`,
		``,
	} {
		if code, _ := promQuery(t, r, q); code != http.StatusBadRequest {
			t.Fatalf("%q 应 400, got %d", q, code)
		}
	}
}
