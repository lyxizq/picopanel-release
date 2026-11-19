package svcmon

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// listenEcho 起一个真实 TCP 监听（接受即关），返回地址。
func listenEcho(t *testing.T) (string, int) {
	t.Helper()
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
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// TestCheckUpDown Check 对真实监听端口报通（带耗时）、拒绝端口报不通（带原因）。
func TestCheckUpDown(t *testing.T) {
	host, port := listenEcho(t)

	up, ms, errMsg := CheckPort(host, port)
	if !up || errMsg != "" {
		t.Fatalf("监听端口应报通: up=%v err=%q", up, errMsg)
	}
	if ms < 0 {
		t.Fatalf("耗时不应为负: %d", ms)
	}

	// 关闭的端口（复用同地址另一端口大概率无人听；用拨向已关端口的固定探测）
	down, _, errMsg2 := CheckPort(host, 1)
	if down {
		t.Fatal("端口 1 不应报通（本机无服务）")
	}
	if errMsg2 == "" {
		t.Fatal("失败应带原因")
	}
}

// TestCheckNow 轮询管理器：手动检测写库翻绿/翻红，换端口（store 重置）后翻红，
// 删除后 Drop 不留调度残留。
func TestCheckNow(t *testing.T) {
	s := openStore(t)
	host, port := listenEcho(t)

	m := New(s)
	sid, err := s.CreateSvcServer(&store.SvcServer{Name: "本机", Host: host, Interval: 30, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := s.CreateSvcPort(&store.SvcPort{ServerID: sid, Name: "echo", Port: port})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.CheckNow(pid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Up != 1 || got.CheckedAt == 0 || got.Since == 0 || got.LastError != "" || got.Host != host {
		t.Fatalf("手动检测应翻绿且带服务器参数: %+v", got)
	}

	// 同端口改指向无人监听的端口（Store 层换端口会重置），手动检测翻红
	if err := s.UpdateSvcPort(&store.SvcPort{ID: pid, Name: "echo", Port: port + 1}); err != nil {
		t.Fatal(err)
	}
	got, err = m.CheckNow(pid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Up != 0 || !strings.Contains(got.LastError, host) {
		t.Fatalf("无人监听应翻红且带原因: %+v", got)
	}
	// 换端口时 Store 层已把 last_ok_at 归零（重置口径，见 store 测试）
	if got.LastOKAt != 0 {
		t.Fatalf("换端口后 last_ok_at 应已随重置清零: %+v", got)
	}

	if err := s.DeleteSvcPort(pid); err != nil {
		t.Fatal(err)
	}
	m.Drop(pid)
	if _, err := m.CheckNow(pid); err == nil {
		t.Fatal("删除后检测应报端口不存在")
	}
}

// TestKickAll KickAll 触发一轮后台全量重查（异步），启用端口入库、停用服务器跳过。
func TestKickAll(t *testing.T) {
	s := openStore(t)
	host, port := listenEcho(t)

	sid, err := s.CreateSvcServer(&store.SvcServer{Name: "kick", Host: host, Interval: 3600, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := s.CreateSvcPort(&store.SvcPort{ServerID: sid, Name: "echo", Port: port})
	if err != nil {
		t.Fatal(err)
	}
	offSid, err := s.CreateSvcServer(&store.SvcServer{Name: "停用机", Host: host, Interval: 3600, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	offPid, err := s.CreateSvcPort(&store.SvcPort{ServerID: offSid, Name: "off", Port: port})
	if err != nil {
		t.Fatal(err)
	}

	m := New(s)
	m.KickAll()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, err := s.GetSvcPortRow(pid)
		if err != nil {
			t.Fatal(err)
		}
		if a.Up == 1 {
			off, _ := s.GetSvcPortRow(offPid)
			if off.Up != -1 {
				t.Fatalf("停用服务器的端口不应被拨测: %+v", off)
			}
			return // 后台轮次已把状态写上
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("KickAll 后 3s 内未见到拨测结果入库")
}

// TestKickServer 整台检测只加速该服务器：A 台 KickServer 后翻绿，B 台
// （刚手动检测过、间隔 3600s 未到期）不被动。
func TestKickServer(t *testing.T) {
	s := openStore(t)
	host, port := listenEcho(t)

	mk := func(name string) (int64, int64) {
		sid, err := s.CreateSvcServer(&store.SvcServer{Name: name, Host: host, Interval: 3600, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		pid, err := s.CreateSvcPort(&store.SvcPort{ServerID: sid, Name: name, Port: port})
		if err != nil {
			t.Fatal(err)
		}
		return sid, pid
	}
	sidA, pidA := mk("A")
	_, pidB := mk("B")

	m := New(s)
	// B 先手动检测一次（lastCheck 记到当前、间隔 3600s，不会被 A 的轮次波及）
	if _, err := m.CheckNow(pidB); err != nil {
		t.Fatal(err)
	}
	b1, _ := s.GetSvcPortRow(pidB)

	m.KickServer(sidA)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a, _ := s.GetSvcPortRow(pidA)
		if a.Up == 1 {
			b2, _ := s.GetSvcPortRow(pidB)
			if b2.CheckedAt != b1.CheckedAt {
				t.Fatalf("B 台不应被整台检测波及: checked_at %d → %d", b1.CheckedAt, b2.CheckedAt)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("KickServer 后 3s 内未见到 A 台拨测结果入库")
}

// TestCheckHTTP 请求检测（495）：2xx 通；4xx/5xx 失败带状态码；nginx 式跳转
// 跟随到错误终态也算失败；连接拒绝失败原因可读；https 自签证书不误报。
func TestCheckHTTP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/lost", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	mux.HandleFunc("/jump", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/lost", http.StatusFound) // nginx 跳转落到错误页
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port := u.Port()

	if ok, _, err := CheckHTTP("http", "127.0.0.1", port); !ok || err != "" {
		t.Fatalf("200 应通: ok=%v err=%q", ok, err)
	}
	if ok, _, err := probePath("http", "127.0.0.1", port, "/lost"); ok || !strings.Contains(err, "503") {
		t.Fatalf("503 应失败带状态码: ok=%v err=%q", ok, err)
	}
	if ok, _, err := probePath("http", "127.0.0.1", port, "/jump"); ok || !strings.Contains(err, "503") {
		t.Fatalf("跳转落 503 应失败: ok=%v err=%q", ok, err)
	}
	if ok, _, err := CheckHTTP("http", "127.0.0.1", "1"); ok || err == "" {
		t.Fatalf("连接拒绝应失败: ok=%v err=%q", ok, err)
	}

	// https：自签证书（httptest 默认证书）不误报
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	t.Cleanup(tlsSrv.Close)
	tu, _ := url.Parse(tlsSrv.URL)
	if ok, _, err := CheckHTTP("https", "127.0.0.1", tu.Port()); !ok {
		t.Fatalf("https 自签应通（跳证书校验）: %v", err)
	}
}

// probePath 测试辅助：带路径的请求检测（产品路径固定为 /）。
func probePath(scheme, domain, port, path string) (bool, int64, string) {
	start := time.Now()
	resp, err := probeHTTPClient.Get(scheme + "://" + net.JoinHostPort(domain, port) + path)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return false, ms, err.Error()
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, ms, ""
	}
	return false, ms, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

// TestFlipNotifyBatch 状态翻转回调（500）：未检测态首轮不回调、两端口同时
// 翻转合一批回调、无变化不回调、单端口再翻转单条回调；事件表随翻转落行。
func TestFlipNotifyBatch(t *testing.T) {
	st := openStore(t)
	m := New(st)
	hook := make(chan []Flip, 8)
	m.SetNotify(func(fs []Flip) { hook <- fs })

	// 两个「无人监听」端口（先 Listen 占位拿端口号再关）
	closed := func() int {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		return p
	}
	cp1, cp2 := closed(), closed()
	sid, err := st.CreateSvcServer(&store.SvcServer{Name: "NAS", Host: "127.0.0.1", Interval: 600, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p1, err := st.CreateSvcPort(&store.SvcPort{ServerID: sid, Name: "a", Port: cp1})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := st.CreateSvcPort(&store.SvcPort{ServerID: sid, Name: "b", Port: cp2})
	if err != nil {
		t.Fatal(err)
	}

	// 首轮（未检测态 -1 → 不通）不算翻转：无回调、无事件
	if _, err := m.CheckNow(p1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CheckNow(p2); err != nil {
		t.Fatal(err)
	}
	select {
	case fs := <-hook:
		t.Fatalf("首轮未检测态不应回调: %+v", fs)
	default:
	}
	if evs, _ := st.ListSvcEvents(10); len(evs) != 0 {
		t.Fatalf("首轮不应落事件: %d", len(evs))
	}

	// 两端口同时恢复（各自起监听）→ KickAll 一轮 → 合并成一批回调
	ln1, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cp1))
	if err != nil {
		t.Fatal(err)
	}
	defer ln1.Close()
	ln2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cp2))
	if err != nil {
		t.Fatal(err)
	}
	defer ln2.Close()
	m.KickAll()
	select {
	case fs := <-hook:
		if len(fs) != 2 {
			t.Fatalf("两端口同时翻转应合一批: %d", len(fs))
		}
		ups := map[int64]bool{}
		for _, f := range fs {
			ups[f.PortID] = f.Up
			if f.ServerName != "NAS" {
				t.Fatalf("回调应带服务器业务名: %+v", f)
			}
		}
		if !ups[p1] || !ups[p2] {
			t.Fatalf("批内端口不符: %+v", fs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待批量翻转回调超时")
	}
	if evs, _ := st.ListSvcEvents(10); len(evs) != 2 {
		t.Fatalf("翻转应落 2 条事件: %d", len(evs))
	}

	// 无变化的整轮不回调
	m.KickAll()
	select {
	case fs := <-hook:
		t.Fatalf("无变化不应回调: %+v", fs)
	case <-time.After(1500 * time.Millisecond):
	}

	// 单端口再翻下去（关监听，手动同步检测）→ 单条回调 + 事件表 +1
	ln1.Close()
	if _, err := m.CheckNow(p1); err != nil {
		t.Fatal(err)
	}
	select {
	case fs := <-hook:
		if len(fs) != 1 || fs[0].PortID != p1 || fs[0].Up {
			t.Fatalf("单端口翻转回调不符: %+v", fs)
		}
		if fs[0].ErrMsg == "" {
			t.Fatalf("不通翻转应带原因: %+v", fs[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待单端口翻转回调超时")
	}
	if evs, _ := st.ListSvcEvents(10); len(evs) != 3 {
		t.Fatalf("事件表应 3 条: %d", len(evs))
	}
}
