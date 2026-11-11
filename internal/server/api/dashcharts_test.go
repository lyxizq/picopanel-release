package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
)

// newDashChartRouter 起一个只挂总览图表数据路由的测试服务，并预置
// 一台服务器（web-01）与两条 30s 间隔的 CPU 样本。
func newDashChartRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv, err := s.CreateServer("web-01", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for i, cpu := range []float64{10, 20} {
		if err := s.InsertMetric(srv.ID, now-int64(30*(2-i)), &proto.Report{CPU: cpu}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateDashChart(srv.ID, "cpu", ""); err != nil {
		t.Fatal(err)
	}
	h := &DashChartHandler{st: s}
	r := gin.New()
	r.GET("/data", h.Data)
	return r, s
}

func getData(t *testing.T, r *gin.Engine, rangeSec string) (from, to int64, n int) {
	t.Helper()
	url := "/data"
	if rangeSec != "" {
		url += "?range=" + rangeSec
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("data 状态码 %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		From   int64 `json:"from"`
		To     int64 `json:"to"`
		Charts []struct {
			Series []struct {
				Points [][2]any `json:"points"`
			} `json:"series"`
		} `json:"charts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if len(res.Charts) == 0 || len(res.Charts[0].Series) == 0 {
		t.Fatalf("应至少返回 1 张图且有序列, got %d", len(res.Charts))
	}
	return res.From, res.To, len(res.Charts[0].Series[0].Points)
}

// TestDashChartDataRanges 新增时段全部在白名单内；非法 range 落回缺省 1 小时。
func TestDashChartDataRanges(t *testing.T) {
	r, _ := newDashChartRouter(t)
	for _, sec := range []string{"300", "900", "1800", "172800", "259200", "604800", "1209600"} {
		want, _ := strconv.ParseInt(sec, 10, 64)
		from, to, n := getData(t, r, sec)
		if to-from != want {
			t.Fatalf("range=%s 区间错误: from=%d to=%d", sec, from, to)
		}
		if n == 0 {
			t.Fatalf("range=%s 应有数据点", sec)
		}
		invalidateDashChartCache() // 每档独立真查，不吃缓存
	}
	_, to, _ := getData(t, r, "999")
	invalidateDashChartCache()
	_, to2, _ := getData(t, r, "")
	invalidateDashChartCache()
	if to-3600 != to2-3600 {
		t.Fatalf("非法/缺省 range 都应回退 1 小时")
	}
}

// TestDashChartDataCache 同 range 10s 内命中缓存（from/to 原样复用、不重查库），
// 换 range 未命中；配置变更后失效、重查。
func TestDashChartDataCache(t *testing.T) {
	// 包级缓存可能已被同包其他测试写过，计数器归零再断言
	dashChartCache.mu.Lock()
	dashChartCache.key = 0
	dashChartCache.queried, dashChartCache.served = 0, 0
	dashChartCache.mu.Unlock()
	r, s := newDashChartRouter(t)
	from1, to1, _ := getData(t, r, "3600")
	if dashChartCache.queried != 1 {
		t.Fatalf("首次应真查库: queried=%d", dashChartCache.queried)
	}
	from2, to2, _ := getData(t, r, "3600")
	if dashChartCache.queried != 1 || dashChartCache.served != 1 {
		t.Fatalf("第二次应命中缓存: queried=%d served=%d", dashChartCache.queried, dashChartCache.served)
	}
	if from1 != from2 || to1 != to2 {
		t.Fatalf("缓存应原样复用 from/to")
	}
	// 换 range：key 不同，重新真查
	getData(t, r, "86400")
	if dashChartCache.queried != 2 {
		t.Fatalf("换 range 应重查: queried=%d", dashChartCache.queried)
	}
	// 配置变更（增删/重排走 invalidateDashChartCache）后同 range 重查
	srv, err := s.CreateServer("web-02", "10.0.0.2:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDashChart(srv.ID, "cpu", ""); err != nil {
		t.Fatal(err)
	}
	invalidateDashChartCache()
	getData(t, r, "86400")
	if dashChartCache.queried != 3 {
		t.Fatalf("失效后应重查: queried=%d", dashChartCache.queried)
	}
}
