package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
)

// TestServerTraffic 外网流量端点契约：无入账时 since 空且列表为空数组（非 null）；
// 两轮上报后本月合计 = 计数器差值、ifaces 为 agent 报的默认路由出口；非法 ID 400。
func TestServerTraffic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "tr.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	r := gin.New()
	h := &ServerHandler{st: s, offlineAfter: 60}
	r.GET("/servers/:id/traffic", h.Traffic)

	srv, err := s.CreateServer("web-01", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) (int, map[string]json.RawMessage) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}

	if code, _ := get("/servers/abc/traffic"); code != http.StatusBadRequest {
		t.Fatalf("非法 ID 应 400, got %d", code)
	}

	code, body := get("/servers/1/traffic")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if string(body["since"]) != `""` || string(body["days"]) != `[]` || string(body["months"]) != `[]` || string(body["ifaces"]) != `[]` {
		t.Fatalf("无入账形态应为空串/空数组: %s", body)
	}

	now := time.Now().Unix()
	for i, rx := range []uint64{1_000_000, 1_750_000} {
		rep := &proto.Report{
			Nets:      []proto.NetInfo{{Iface: "eth0", RxBytes: rx, TxBytes: rx / 2}, {Iface: "docker0", RxBytes: 1 << 40}},
			WanIfaces: []string{"eth0"},
		}
		if err := s.InsertMetric(srv.ID, now+int64(i*10), rep); err != nil {
			t.Fatal(err)
		}
	}
	code, body = get("/servers/1/traffic")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if string(body["month_rx"]) != "750000" || string(body["month_tx"]) != "375000" || string(body["ifaces"]) != `["eth0"]` {
		t.Fatalf("两轮上报后本月合计应为 750000/375000、ifaces=[eth0]: %s", body)
	}
	var days []store.NetTrafficDay
	if err := json.Unmarshal(body["days"], &days); err != nil || len(days) != 1 || days[0].Rx != 750000 {
		t.Fatalf("逐日 = %s (%v)", body["days"], err)
	}
}
