package webcheck

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// TestParseCheckURL 规整口径：补默认 scheme、剥「/」尾径、拒绝坏形态。
func TestParseCheckURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"example.com", "http://example.com", false},
		{"192.168.1.10:8080", "http://192.168.1.10:8080", false},
		{"https://Example.com:8443/app", "https://example.com:8443/app", false},
		{"http://a.com/", "http://a.com", false},
		{" http://a.com/x?y=1 ", "http://a.com/x?y=1", false},
		{"", "", true},
		{"ftp://a.com", "", true},
		{"http://", "", true},
		{strings.Repeat("a", 513), "", true},
	}
	for _, c := range cases {
		got, err := ParseCheckURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("%q 应报错，得到 %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 不应报错: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("%q 规整为 %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestInQuietHours 静默窗口=本地 0:00–6:00（6:00 整点起恢复检查）。
func TestInQuietHours(t *testing.T) {
	cases := []struct {
		hour int
		want bool
	}{
		{0, true}, {5, true}, {5, true},
		{6, false}, {12, false}, {23, false},
	}
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.Local)
	for _, c := range cases {
		if got := InQuietHours(time.Date(base.Year(), base.Month(), base.Day(), c.hour, 0, 0, 0, time.Local)); got != c.want {
			t.Fatalf("%d 点静默应为 %v，得到 %v", c.hour, c.want, got)
		}
	}
}

// newPageServer httptest 起一个返回固定页面的站点（并发安全读 body）。
func newPageServer(t *testing.T, body func() string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write([]byte(body()))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCheckURLRules 多规则判定（2026-11-03 改版）：规则内「且」（全部包含才算
// 该规则通过）、规则间「或」（任一通过即通过，MatchedRule 记命中序号）、全部
// 规则不满足才失败并逐规则列缺失；单规则文案不带规则序号；大小写不敏感。
func TestCheckURLRules(t *testing.T) {
	m := New(nil, nil)
	srv := newPageServer(t, func() string { return "<html>Welcome to PicoPanel 服务正常 Running</html>" })
	u := srv.URL

	// 规则 1 不满足但规则 2 满足：或逻辑整体通过，命中规则 2
	res := m.CheckURL(u, [][]string{{"登录", "用户名"}, {"picopanel", "服务正常", "RUNNING"}})
	if !res.OK || res.MatchedRule != 2 {
		t.Fatalf("任一规则满足应通过: %+v", res)
	}
	// 规则 1 满足：短路通过，命中规则 1
	res = m.CheckURL(u, [][]string{{"服务正常"}, {"不存在的词"}})
	if !res.OK || res.MatchedRule != 1 {
		t.Fatalf("首条规则满足应通过: %+v", res)
	}
	// 全部规则不满足：失败，逐规则缺失对齐
	res = m.CheckURL(u, [][]string{{"picopanel", "不存在甲"}, {"不存在乙"}})
	if res.OK || len(res.RuleMissing) != 2 || len(res.RuleMissing[0]) != 1 || res.RuleMissing[0][0] != "不存在甲" || res.RuleMissing[1][0] != "不存在乙" {
		t.Fatalf("全不满足应失败且逐规则列缺失: %+v", res)
	}
	if !strings.Contains(res.ErrMsg, "规则 1 缺少") || !strings.Contains(res.ErrMsg, "规则 2 缺少") {
		t.Fatalf("多规则失败文案应带规则序号: %s", res.ErrMsg)
	}
	// 单规则失败：文案不点规则序号（同旧版口径）
	res = m.CheckURL(u, [][]string{{"picopanel", "不存在乙"}})
	if res.OK || strings.Contains(res.ErrMsg, "规则") || !strings.Contains(res.ErrMsg, "缺少关键字") {
		t.Fatalf("单规则失败文案不应带规则序号: %s", res.ErrMsg)
	}
	// 无规则
	if res := m.CheckURL(u, nil); res.OK || res.ErrMsg == "" {
		t.Fatalf("无规则应失败: %+v", res)
	}

	// 非 2xx 终态
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(failSrv.Close)
	if res := m.CheckURL(failSrv.URL, [][]string{{"x"}}); res.OK || !strings.Contains(res.ErrMsg, "503") {
		t.Fatalf("非 2xx 应失败带状态码: %+v", res)
	}

	// 请求失败（关掉的端口）
	res = m.CheckURL("http://127.0.0.1:1/x", [][]string{{"x"}})
	if res.OK || !strings.Contains(res.ErrMsg, "请求失败") {
		t.Fatalf("请求失败应失败: %+v", res)
	}
}

// TestFlipNotifyChain 翻转链（真实库 + SetNotify 桩）：首轮不通知（-1 未检测）、
// 通过→失败与失败→恢复各通知一次、原态复检不通知；通知走一轮合批。
func TestFlipNotifyChain(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	body := "hello keyword world"
	srv := newPageServer(t, func() string { return body })
	m := New(s, nil)

	var mu sync.Mutex
	var batches [][]Flip
	m.SetNotify(func(flips []Flip) {
		mu.Lock()
		defer mu.Unlock()
		cp := make([]Flip, len(flips))
		copy(cp, flips)
		batches = append(batches, cp)
	})

	id, err := s.CreateWebCheck(&store.WebCheck{Name: "演示", URL: srv.URL, Rules: `[["hello","world"]]`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := s.GetWebCheck(id)
	if rules := w.RuleList(); len(rules) != 1 || len(rules[0]) != 2 {
		t.Fatalf("JSON 规则组解析不对: %v", rules)
	}

	// 首轮通过：未检测 -1 不算翻转，不通知
	if f := m.checkOne(w); f != nil {
		t.Fatalf("首轮不应翻转: %+v", f)
	}
	// 原态复检：不通知
	w, _ = s.GetWebCheck(id)
	if f := m.checkOne(w); f != nil {
		t.Fatalf("原态复检不应翻转: %+v", f)
	}
	// 页面改动后关键字缺失：通过→失败，翻转一次
	body = "hello only"
	w, _ = s.GetWebCheck(id)
	fDown := m.checkOne(w)
	if fDown == nil || fDown.Up {
		t.Fatalf("变失败应翻转: %+v", fDown)
	}
	// 恢复：失败→通过，翻转一次
	body = "hello keyword world"
	w, _ = s.GetWebCheck(id)
	fUp := m.checkOne(w)
	if fUp == nil || !fUp.Up {
		t.Fatalf("恢复应翻转: %+v", fUp)
	}

	// emit/SetNotify 装配链：两次翻转合批异步送达回调
	m.emit([]Flip{*fDown, *fUp})
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(batches)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("emit 应把翻转送达 SetNotify 回调")
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	if len(batches[0]) != 2 {
		t.Fatalf("合批应整批送达: %+v", batches[0])
	}
	mu.Unlock()

	// 状态行落库正确
	w, _ = s.GetWebCheck(id)
	if w.Up != 1 || w.LastError != "" || w.LastOKAt == 0 || w.Since == 0 {
		t.Fatalf("恢复后状态行不对: %+v", w)
	}

	// flipMessage 单条/多条文案
	subject, bd, title := flipMessage([]Flip{{ID: id, Name: "演示", URL: srv.URL, Up: false, ErrMsg: "缺少关键字 「world」"}})
	if !strings.Contains(subject, "网站检查异常") || !strings.Contains(title, "演示") || !strings.Contains(bd, "缺少关键字") {
		t.Fatalf("失败通知文案不对: %s | %s | %s", subject, title, bd)
	}
	subject, _, _ = flipMessage([]Flip{{ID: id, Name: "a", URL: "http://a", Up: false}, {ID: id + 1, Name: "b", URL: "http://b", Up: true}})
	if !strings.Contains(subject, "2 项状态变化") || !strings.Contains(subject, "1 项异常") {
		t.Fatalf("合批通知文案不对: %s", subject)
	}
}

// TestCheckURLBlocked 出站内网限制（2026-11-03 漏洞修）：开关开启后目标主机
// 解析到内网（IP 字面量或域名）即拒查，不发起请求；放行主机照常抓取。
// 拦截名单避开 127.0.0.1——httptest 本地服务器就在环回上（放行路径要用）。
func TestCheckURLBlocked(t *testing.T) {
	blocked := func(host string) bool {
		return host == "10.255.255.1" || host == "intranet.example"
	}
	m := New(nil, blocked)
	res := m.CheckURL("http://10.255.255.1:8080/panel", [][]string{{"admin"}})
	if res.OK || !strings.Contains(res.ErrMsg, "内网") {
		t.Fatalf("私网地址应被拦截: %+v", res)
	}
	res = m.CheckURL("http://intranet.example/", [][]string{{"x"}})
	if res.OK || !strings.Contains(res.ErrMsg, "内网") {
		t.Fatalf("内网域名应被拦截: %+v", res)
	}
	// 放行主机走真实抓取（本地测试服务器不在拦截名单）
	srv := newPageServer(t, func() string { return "<html>ok fine</html>" })
	res = m.CheckURL(srv.URL, [][]string{{"fine"}})
	if !res.OK || res.MatchedRule != 1 {
		t.Fatalf("放行主机应正常判定: %+v", res)
	}
	// 不注入判定（开关关闭）时环回照查——本地自检场景
	m2 := New(nil, nil)
	res = m2.CheckURL(srv.URL, [][]string{{"fine"}})
	if !res.OK {
		t.Fatalf("未开启限制时应照常判定: %+v", res)
	}
}
