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

// newSnapshotRouter 起一个只挂快照路由的测试服务。
func newSnapshotRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	r := gin.New()
	h := &SnapshotHandler{st: s, token: "tok123", offlineAfter: 120}
	r.GET("/api/v1/snapshot", h.Get)
	return r, s
}

// seedServer 建一台 push 服务器（net_ifaces=eth0）、标记刚上报并写入一份指标。
func seedServer(t *testing.T, s *store.Store) {
	t.Helper()
	srv, err := s.CreatePushServer("web-01", "push-tok", 0)
	if err != nil {
		t.Fatal(err)
	}
	eth0 := "eth0"
	if err := s.UpdateServer(srv.ID, store.ServerUpdate{NetIfaces: &eth0}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if err := s.UpsertOnReport(srv.ID, &proto.SysInfo{Hostname: "web-01"}, now); err != nil {
		t.Fatal(err)
	}
	rep := &proto.Report{
		TS: now, CPU: 12.34, MemPercent: 56.78, MemTotal: 4 << 30, MemUsed: 2 << 30,
		Load1: 0.5, Uptime: 90061, TCPEst: 42,
		Disks: []proto.DiskInfo{
			{Mount: "/", Percent: 71.25, IOUtil: 18.2},
			{Mount: "/data", Percent: 93.4},
		},
		Nets: []proto.NetInfo{
			{Iface: "eth0", RxBps: 1536, TxBps: 512},
			{Iface: "eth1", RxBps: 99999, TxBps: 99999}, // 未配置的网口不应计入
		},
	}
	if err := s.InsertMetric(srv.ID, now, rep); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotAuthAndLookup(t *testing.T) {
	r, s := newSnapshotRouter(t)
	seedServer(t, s)

	// 令牌错误 / 缺失 → 401（令牌只认 X-Snapshot-Token 头；查询参数通道已移除）
	for _, tc := range []struct {
		url    string
		header string
	}{{"/api/v1/snapshot?server=web-01", "wrong"}, {"/api/v1/snapshot?server=web-01", ""}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", tc.url, nil)
		if tc.header != "" {
			req.Header.Set("X-Snapshot-Token", tc.header)
		}
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s 应 401, got %d", tc.url, w.Code)
		}
	}
	// 缺 server / 未知 server
	for _, url := range []string{
		"/api/v1/snapshot",
		"/api/v1/snapshot?server=nope",
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", url, nil)
		req.Header.Set("X-Snapshot-Token", "tok123")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Fatalf("%s 应 400/404, got %d", url, w.Code)
		}
	}

	// 按名称查 + 头携带令牌
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/snapshot?server=web-01", nil)
	req.Header.Set("X-Snapshot-Token", "tok123")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("按名称查应 200, got %d: %s", w.Code, w.Body.String())
	}
	var v snapshotResp
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, v)

	// 按 ID 查结果一致
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v1/snapshot?server=1", nil)
	req2.Header.Set("X-Snapshot-Token", "tok123")
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("按 ID 查应 200, got %d", w2.Code)
	}
	var v2 snapshotResp
	_ = json.Unmarshal(w2.Body.Bytes(), &v2)
	checkSnapshot(t, v2)
}

// checkSnapshot 校验 seedServer 数据加工出的快照字段。
func checkSnapshot(t *testing.T, v snapshotResp) {
	t.Helper()
	if !v.Online || v.Name != "web-01" || v.Hostname != "web-01" {
		t.Fatalf("基础字段错误: %+v", v)
	}
	if v.CPU != 12.3 || v.Mem != 56.8 { // round1
		t.Fatalf("cpu/mem = %v/%v", v.CPU, v.Mem)
	}
	if v.NetRx != 1536 || v.NetTx != 512 { // 仅 eth0
		t.Fatalf("net = %v/%v, 期望只合计 eth0", v.NetRx, v.NetTx)
	}
	if v.NetRxH != "12.3 Kbps" || v.NetTxH != "4.1 Kbps" {
		t.Fatalf("net_h = %q/%q", v.NetRxH, v.NetTxH)
	}
	if v.Disk != 93.4 || v.DiskRoot != 71.3 || v.IOUtil != 18.2 {
		t.Fatalf("disk/root/io = %v/%v/%v", v.Disk, v.DiskRoot, v.IOUtil)
	}
	if v.TCPEst != 42 || v.Uptime != 90061 {
		t.Fatalf("tcp/uptime = %v/%v", v.TCPEst, v.Uptime)
	}
	if v.UptimeH != "1 天 1 小时" {
		t.Fatalf("uptime_h = %q", v.UptimeH)
	}
	if v.MemH != "2.0 GB / 4.0 GB" {
		t.Fatalf("mem_h = %q", v.MemH)
	}
}

// TestSnapshotHelpers 纯格式化函数边界。
func TestSnapshotHelpers(t *testing.T) {
	if got := humanBps(0); got != "0.0 Kbps" {
		t.Fatalf("humanBps(0) = %q", got)
	}
	if got := humanBps(1048576); got != "8.4 Mbps" {
		t.Fatalf("humanBps(1MiB/s) = %q", got)
	}
	if got := humanBytes(512); got != "512.0 B" {
		t.Fatalf("humanBytes(512) = %q", got)
	}
	if got := humanUptime(59); got != "59 秒" {
		t.Fatalf("humanUptime(59) = %q", got)
	}
	if got := humanUptime(3600); got != "1 小时 0 分" {
		t.Fatalf("humanUptime(3600) = %q", got)
	}
	// net_ifaces 为空 = 全部网口合计
	m := &store.MetricRow{Nets: []proto.NetInfo{
		{Iface: "eth0", RxBps: 100}, {Iface: "eth1", RxBps: 50},
	}}
	if rx, _ := sumNet(m, ""); rx != 150 {
		t.Fatalf("空配置应全合计, got %v", rx)
	}
	if rx, _ := sumNet(m, " eth1 , eth0 "); rx != 150 {
		t.Fatalf("带空格配置应命中, got %v", rx)
	}
}

// TestSnapshotNoMetric 未上报过的服务器：数值零值但不报错，在线=false。
func TestSnapshotNoMetric(t *testing.T) {
	r, s := newSnapshotRouter(t)
	if _, err := s.CreatePushServer("empty", "tok-e", 0); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/snapshot?server=empty", nil)
	req.Header.Set("X-Snapshot-Token", "tok123")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("应 200, got %d", w.Code)
	}
	var v snapshotResp
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v.Online || v.CPU != 0 || v.TS != 0 {
		t.Fatalf("无数据服务器应为零值: %+v", v)
	}
}
