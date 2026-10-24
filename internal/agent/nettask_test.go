package agent

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 通用中转任务执行器（548）：host 白名单外直接拒绝（不触网）；白名单内按
// method/headers/timeout 执行并回传状态码与响应体。
func TestRunNetTasks(t *testing.T) {
	// 白名单外：不触网即失败回传
	res := doNetTask(&NetTask{TaskID: 1, URL: "https://evil.example.com/steal", TimeoutSec: 3})
	if res.Error == "" || !strings.Contains(res.Error, "白名单") {
		t.Fatalf("白名单外应拒绝，得到 %+v", res)
	}
	if res.StatusCode != 0 || res.Body != "" {
		t.Fatalf("拒绝任务不应有响应： %+v", res)
	}

	// 白名单内：真实执行 GET（测试服务器 host 临时入白名单）
	var gotPath string
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		gotHeader = r.Header.Get("Accept")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h // host() 按主机名判定，不带端口
	}
	orig := netTaskAllowedHosts
	netTaskAllowedHosts = map[string]bool{host: true}
	defer func() { netTaskAllowedHosts = orig }()

	res = doNetTask(&NetTask{TaskID: 2, URL: srv.URL + "/3/search/tv?query=test", Headers: map[string]string{"Accept": "application/json"}, TimeoutSec: 5})
	if res.Error != "" {
		t.Fatalf("白名单内应执行成功，得到 %v", res.Error)
	}
	if res.StatusCode != 200 || res.Body != `{"ok":true}` {
		t.Fatalf("结果不符： %+v", res)
	}
	if gotPath != "/3/search/tv?query=test" || gotHeader != "application/json" {
		t.Fatalf("请求路径/头不符： %s %q", gotPath, gotHeader)
	}
}

// mihomo 类 purpose（TODO 7）：私网/回环 IP 放行、公网与域名不放行、其他
// purpose 不受影响（purpose 级白名单，不走 AllowAnyHost 免吃 3s 限速）。
func TestNetTaskMihomoPurposeHostAllow(t *testing.T) {
	cases := []struct {
		url, purpose string
		want         bool
	}{
		{"http://127.0.0.1:9090/configs", "mihomo", true},
		{"http://192.168.50.10:9090/proxies", "mihomo", true},
		{"http://10.0.0.2:9090/version", "mihomo", true},
		{"http://[::1]:9090/configs", "mihomo", true},
		{"http://1.2.3.4:9090/configs", "mihomo", false},
		{"http://router.lan:9090/configs", "mihomo", false}, // 域名不解析不判定
		{"http://1.2.3.4:9090/x", "tvfollow_api", false},    // 其他 purpose 不放行
	}
	for _, c := range cases {
		res := doNetTask(&NetTask{TaskID: 1, Purpose: c.purpose, URL: c.url, TimeoutSec: 1})
		blocked := res.Error != "" && strings.Contains(res.Error, "白名单")
		if blocked == c.want {
			t.Fatalf("%s (purpose=%s): want allowed=%v, got error=%q", c.url, c.purpose, c.want, res.Error)
		}
	}
}
