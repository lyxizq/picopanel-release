package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/svcmon"
)

// newSvcRouter 真实链路：真实 Register（含鉴权组）+ 真实库 + 会话 token，
// 另起一个真实 TCP 监听供「通」方向拨测。顺带回库句柄（事件用例直接落数）。
func newSvcRouter(t *testing.T) (*gin.Engine, string, *net.TCPAddr, *store.Store) {
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	r := gin.New()
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, nil, nil, nil, svcmon.New(s), nil, nil, nil); err != nil {
		t.Fatal(err, nil)
	}
	return r, token, ln.Addr().(*net.TCPAddr), s
}

// svcReq 带会话发起请求并断言状态码。
func svcReq(t *testing.T, r *gin.Engine, token, method, path, body string, wantCode int) string {
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

type svcPortView struct {
	ID        int64  `json:"id"`
	ServerID  int64  `json:"server_id"`
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Up        int    `json:"up"`
	LatencyMs int64  `json:"latency_ms"`
	CheckedAt int64  `json:"checked_at"`
	Since     int64  `json:"since"`
	LastError string `json:"last_error"`
}

func parsePort(t *testing.T, body string) svcPortView {
	t.Helper()
	var res struct {
		Port svcPortView `json:"port"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("解析 port 失败: %v: %s", err, body)
	}
	return res.Port
}

// TestSvcMonFullChain 两级全链路（366）：建服务器 → 加端口（创建即同步检测
// 翻绿/翻红）→ 列表嵌套 → 改端口（换端口号重置重查）→ 手动检测 → 未入库测试
// 端点 → 服务器/端口上限 → 删除连带 → 404/401 语义。
func TestSvcMonFullChain(t *testing.T) {
	r, token, addr, _ := newSvcRouter(t)
	base := "/api/v1"
	upPort := addr.Port
	downPort := addr.Port + 1 // 同机紧邻端口，无监听即拒连
	host := "127.0.0.1"

	// 建服务器
	res := svcReq(t, r, token, "POST", base+"/svcmon/servers",
		fmt.Sprintf(`{"name":"本地机","host":%q,"interval_sec":30}`, host), http.StatusOK)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(res), &created); err != nil || created.ID == 0 {
		t.Fatalf("建服务器应回 id: %s", res)
	}
	sid := created.ID

	// 参数校验：坏 host / 服务器不存在加端口
	svcReq(t, r, token, "POST", base+"/svcmon/servers", `{"host":"","port":80}`, http.StatusBadRequest)
	svcReq(t, r, token, "POST", base+"/svcmon/servers", `{"host":"a b.c"}`, http.StatusBadRequest)
	svcReq(t, r, token, "POST", base+"/svcmon/servers/999/ports", `{"port":80}`, http.StatusNotFound)

	// 加端口（指向真实监听端口）：create 即刻检测，应直接翻绿
	res = svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sid),
		fmt.Sprintf(`{"name":"本地服务","port":%d}`, upPort), http.StatusOK)
	pt := parsePort(t, res)
	if pt.ID == 0 || pt.Up != 1 || pt.CheckedAt == 0 || pt.Since == 0 || pt.LastError != "" || pt.ServerID != sid {
		t.Fatalf("加端口应即时检测翻绿: %+v（%s）", pt, res)
	}
	pid := pt.ID

	// 加不通端口：翻红带原因
	res = svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sid),
		fmt.Sprintf(`{"name":"死端口","port":%d}`, downPort), http.StatusOK)
	dead := parsePort(t, res)
	if dead.Up != 0 || dead.LastError == "" {
		t.Fatalf("关闭端口应翻红带原因: %+v（%s）", dead, res)
	}
	deadID := dead.ID

	// 端口校验：越界 / 重复端口不校验（业务名可空）
	svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sid), `{"port":70000}`, http.StatusBadRequest)
	svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sid), `{"port":0}`, http.StatusBadRequest)

	// 列表嵌套形状
	res = svcReq(t, r, token, "GET", base+"/svcmon", "", http.StatusOK)
	var list struct {
		Servers []struct {
			ID      int64         `json:"id"`
			Name    string        `json:"name"`
			Host    string        `json:"host"`
			Enabled bool          `json:"enabled"`
			Ports   []svcPortView `json:"ports"`
			Max     int           `json:"interval_sec"`
		} `json:"servers"`
		MaxServers int `json:"max_servers"`
		MaxPorts   int `json:"max_ports"`
	}
	if err := json.Unmarshal([]byte(res), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Servers) != 1 || list.Servers[0].ID != sid || list.Servers[0].Host != host ||
		len(list.Servers[0].Ports) != 2 || list.MaxServers != store.MaxSvcServers {
		t.Fatalf("列表嵌套不符: %s", res)
	}

	// 改死端口为通端口：换端口号重置后即时重查翻绿
	res = svcReq(t, r, token, "PUT", base+fmt.Sprintf("/svcmon/ports/%d", deadID),
		fmt.Sprintf(`{"name":"复活","port":%d}`, upPort), http.StatusOK)
	pt = parsePort(t, res)
	if pt.Up != 1 || pt.LastError != "" || pt.Name != "复活" || pt.Since == 0 {
		t.Fatalf("换端口应重查翻绿: %+v（%s）", pt, res)
	}
	greenSince := pt.Since

	// 手动检测（不变目标）：仍绿、since 不动
	svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/ports/%d/check", deadID), "", http.StatusOK)
	res = svcReq(t, r, token, "GET", base+"/svcmon", "", http.StatusOK)
	if err := json.Unmarshal([]byte(res), &list); err != nil {
		t.Fatal(err)
	}
	deadAfter := list.Servers[0].Ports[1]
	if deadAfter.ID != deadID || deadAfter.Up != 1 || deadAfter.Since != greenSince {
		t.Fatalf("手动检测后应仍绿且 since 不动: %+v", list.Servers[0].Ports)
	}

	// 未入库测试端点：通/不通两向 + 越界
	res = svcReq(t, r, token, "POST", base+"/svcmon/test",
		fmt.Sprintf(`{"host":%q,"port":%d}`, host, upPort), http.StatusOK)
	if !strings.Contains(res, `"ok":true`) {
		t.Fatalf("test 应报通: %s", res)
	}
	res = svcReq(t, r, token, "POST", base+"/svcmon/test",
		fmt.Sprintf(`{"host":%q,"port":%d}`, host, downPort), http.StatusOK)
	if !strings.Contains(res, `"ok":false`) {
		t.Fatalf("test 应报不通: %s", res)
	}
	svcReq(t, r, token, "POST", base+"/svcmon/test", `{"host":"127.0.0.1","port":99999}`, http.StatusBadRequest)

	// 整台检测（异步）与全部检测：通路即可
	svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/check", sid), "", http.StatusOK)
	svcReq(t, r, token, "POST", base+"/svcmon/refresh", "", http.StatusOK)
	svcReq(t, r, token, "POST", base+"/svcmon/servers/999/check", "", http.StatusNotFound)

	// 每台端口上限：再补到 16 个后第 17 个 400（拨不通端口立即拒连，不耗时）
	for i := 0; i < store.MaxSvcPortsPerServer-2; i++ {
		svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sid),
			fmt.Sprintf(`{"port":%d}`, 40000+i), http.StatusOK)
	}
	svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sid), `{"port":50000}`, http.StatusBadRequest)

	// 服务器上限：另建 15 台后第 17 台 400
	for i := 0; i < store.MaxSvcServers-1; i++ {
		svcReq(t, r, token, "POST", base+"/svcmon/servers",
			fmt.Sprintf(`{"host":"10.0.0.%d"}`, i+1), http.StatusOK)
	}
	svcReq(t, r, token, "POST", base+"/svcmon/servers", `{"host":"10.9.9.9"}`, http.StatusBadRequest)

	// 删除服务器连带端口；二次删除 404；端口级 404
	svcReq(t, r, token, "DELETE", base+fmt.Sprintf("/svcmon/servers/%d", sid), "", http.StatusOK)
	svcReq(t, r, token, "DELETE", base+fmt.Sprintf("/svcmon/servers/%d", sid), "", http.StatusNotFound)
	res = svcReq(t, r, token, "GET", base+"/svcmon", "", http.StatusOK)
	if strings.Contains(res, `"ports":[{`) {
		t.Fatalf("删服务器应连带清端口: %s", res)
	}
	svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/ports/%d/check", pid), "", http.StatusNotFound)
	svcReq(t, r, token, "PUT", base+fmt.Sprintf("/svcmon/ports/%d", pid), `{"port":80}`, http.StatusNotFound)
	svcReq(t, r, token, "DELETE", base+fmt.Sprintf("/svcmon/ports/%d", pid), "", http.StatusNotFound)
	svcReq(t, r, token, "PUT", base+"/svcmon/servers/999", `{"host":"a.example.com"}`, http.StatusNotFound)

	// 未登录一律 401
	svcReq(t, r, "", "GET", base+"/svcmon", "", http.StatusUnauthorized)
}

// TestSvcMonHTTPPort 请求检测端口（495）：http 不传端口自动补 80；
// 坏协议 400；test 端点按协议探测（200 通/404 失败带状态码）。
func TestSvcMonHTTPPort(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/lost", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	r, token, _, _ := newSvcRouter(t)
	base := "/api/v1"

	// http 不传端口 → 自动补 80
	res := svcReq(t, r, token, "POST", base+"/svcmon/servers",
		`{"name":"web","host":"127.0.0.1","interval_sec":10,"enabled":true}`, http.StatusOK)
	var sv struct {
		Server struct {
			ID int64 `json:"id"`
		} `json:"server"`
	}
	if err := json.Unmarshal([]byte(res), &sv); err != nil || sv.Server.ID == 0 {
		t.Fatalf("建服务器应回 server: %s", res)
	}
	res = svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sv.Server.ID),
		`{"name":"首页","scheme":"http","port":0,"domain":"127.0.0.1"}`, http.StatusOK)
	var created struct {
		Port struct {
			ID     int64  `json:"id"`
			Scheme string `json:"scheme"`
			Domain string `json:"domain"`
			Port   int    `json:"port"`
			Up     int    `json:"up"`
		} `json:"port"`
	}
	if err := json.Unmarshal([]byte(res), &created); err != nil {
		t.Fatalf("建端口解析失败: %s", res)
	}
	// 默认端口补 80（本机 80 无监听，Up=0 恰证检测真跑了）
	if created.Port.Scheme != "http" || created.Port.Port != 80 || created.Port.Domain != "127.0.0.1" || created.Port.Up != 0 {
		t.Fatalf("http 默认端口应补 80 且检测失败: %+v", created.Port)
	}
	// 显式端口指到 httptest → 即时检测 200 通过
	res = svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sv.Server.ID),
		fmt.Sprintf(`{"name":"首页","scheme":"http","port":%d,"domain":"127.0.0.1"}`, port), http.StatusOK)
	if err := json.Unmarshal([]byte(res), &created); err != nil {
		t.Fatalf("建端口解析失败: %s", res)
	}
	if created.Port.Up != 1 {
		t.Fatalf("httptest 200 应即时检测通过: %+v", created.Port)
	}

	// 坏协议 400
	svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sv.Server.ID),
		`{"scheme":"ftp","port":21}`, http.StatusBadRequest)

	// test 端点：http 200 通 / 404 失败带状态码
	res = svcReq(t, r, token, "POST", base+"/svcmon/test",
		fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"scheme":"http","domain":"127.0.0.1"}`, port), http.StatusOK)
	if !strings.Contains(res, `"ok":true`) {
		t.Fatalf("test http 200 应通: %s", res)
	}
	res = svcReq(t, r, token, "POST", base+"/svcmon/test",
		`{"host":"127.0.0.1","port":81,"scheme":"http","domain":"127.0.0.1"}`, http.StatusOK)
	if !strings.Contains(res, `"ok":false`) {
		t.Fatalf("test http 拒连应失败: %s", res)
	}
}

// TestSvcMonEventsAvailability 翻转事件与可用率端点（500）：真实链路造翻转
// （起监听→关→再起，走创建/手动检测的 CheckNow 事务落事件）——空态返回空数组、
// 事件列表带 JOIN 展示字段（新→旧）、可用率逐端口出数、days 异常回落 7/夹紧 90。
func TestSvcMonEventsAvailability(t *testing.T) {
	r, token, _, _ := newSvcRouter(t)
	base := "/api/v1"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenPort := ln.Addr().(*net.TCPAddr).Port

	res := svcReq(t, r, token, "POST", base+"/svcmon/servers",
		fmt.Sprintf(`{"name":"NAS","host":"127.0.0.1","interval_sec":600,"enabled":true}`), http.StatusOK)
	var sv struct {
		Server struct {
			ID int64 `json:"id"`
		} `json:"server"`
	}
	if err := json.Unmarshal([]byte(res), &sv); err != nil || sv.Server.ID == 0 {
		t.Fatalf("建服务器应回 server: %s", res)
	}
	// 建端口即同步首检：监听在线 → up=1（未检测态首检不算翻转、不落事件）
	res = svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/servers/%d/ports", sv.Server.ID),
		fmt.Sprintf(`{"name":"Web","port":%d,"scheme":"tcp"}`, listenPort), http.StatusOK)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(res), &created); err != nil || created.ID == 0 {
		t.Fatalf("建端口应回 id: %s", res)
	}

	// 空态：events/availability 都返回空数组（非 null）
	res = svcReq(t, r, token, "GET", base+"/svcmon/events", "", http.StatusOK)
	if !strings.Contains(res, `"events":[]`) {
		t.Fatalf("事件空态应为空数组: %s", res)
	}
	res = svcReq(t, r, token, "GET", base+"/svcmon/availability?days=7", "", http.StatusOK)
	if !strings.Contains(res, `"availability":[]`) {
		t.Fatalf("可用率空态应为空数组: %s", res)
	}

	// 真实翻转链：关监听手动检测（翻红落事件）→ 重开监听再检（翻绿落事件）
	ln.Close()
	svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/ports/%d/check", created.ID), "", http.StatusOK)
	ln2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatal(err)
	}
	defer ln2.Close()
	svcReq(t, r, token, "POST", base+fmt.Sprintf("/svcmon/ports/%d/check", created.ID), "", http.StatusOK)

	res = svcReq(t, r, token, "GET", base+"/svcmon/events?limit=50", "", http.StatusOK)
	var evRes struct {
		Events []struct {
			PortID     int64  `json:"port_id"`
			Up         int    `json:"up"`
			Error      string `json:"error"`
			ServerName string `json:"server_name"`
			PortName   string `json:"port_name"`
			Host       string `json:"host"`
			Port       int    `json:"port"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(res), &evRes); err != nil || len(evRes.Events) != 2 {
		t.Fatalf("事件列表应恰好 2 条（首检不算翻转）: %s", res)
	}
	latest := evRes.Events[0]
	if latest.Up != 1 || latest.ServerName != "NAS" || latest.PortName != "Web" || latest.Host != "127.0.0.1" || latest.Port != listenPort {
		t.Fatalf("最新事件展示字段不符: %+v", latest)
	}
	if evRes.Events[1].Up != 0 || !strings.Contains(evRes.Events[1].Error, "refused") {
		t.Fatalf("次新事件应为翻红带 refused 原因: %+v", evRes.Events[1])
	}

	res = svcReq(t, r, token, "GET", base+"/svcmon/availability?days=7", "", http.StatusOK)
	var avRes struct {
		Days         int `json:"days"`
		Availability []struct {
			PortID   int64   `json:"port_id"`
			TotalSec int64   `json:"total_sec"`
			DownSec  int64   `json:"down_sec"`
			Pct      float64 `json:"pct"`
		} `json:"availability"`
	}
	if err := json.Unmarshal([]byte(res), &avRes); err != nil || len(avRes.Availability) != 1 {
		t.Fatalf("可用率应 1 行: %s", res)
	}
	a := avRes.Availability[0]
	// 翻红→翻绿间隔毫秒级（unix 秒粒度 down 0~1s），只断言口径与量级
	if a.PortID != created.ID || a.TotalSec != 7*86400 || a.DownSec > 1 || a.Pct < 99.99 {
		t.Fatalf("可用率口径不符: %+v", a)
	}

	// days 异常值回落默认 7；夹紧上限 90
	res = svcReq(t, r, token, "GET", base+"/svcmon/availability?days=abc", "", http.StatusOK)
	if !strings.Contains(res, `"days":7`) {
		t.Fatalf("days 非法应回落 7: %s", res)
	}
	res = svcReq(t, r, token, "GET", base+"/svcmon/availability?days=9999", "", http.StatusOK)
	if !strings.Contains(res, `"days":90`) {
		t.Fatalf("days 应夹紧 90: %s", res)
	}
}
