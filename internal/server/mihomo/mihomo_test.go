package mihomo

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"lz-monitor/internal/server/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestConfigSealedRoundtrip(t *testing.T) {
	st := testStore(t)
	cfg := &Config{ServerID: 3, BaseURL: "http://127.0.0.1:9090", Secret: "s3cret", TestURL: ""}
	if err := SaveConfig(st, cfg, true); err != nil {
		t.Fatal(err)
	}
	got := LoadConfig(st)
	if got.ServerID != 3 || got.BaseURL != "http://127.0.0.1:9090" || got.Secret != "s3cret" {
		t.Fatalf("回读不符: %+v", got)
	}
	if got.TestURL != DefaultTestURL {
		t.Fatalf("空测延迟 URL 应回落默认: %q", got.TestURL)
	}
	// secret 不覆盖语义：secret 空串 + replaceSecret=false 时沿用现值
	if err := SaveConfig(st, &Config{ServerID: 3, BaseURL: "http://127.0.0.1:9090", Secret: "", TestURL: "http://9/204"}, false); err != nil {
		t.Fatal(err)
	}
	if got = LoadConfig(st); got.Secret != "s3cret" || got.TestURL != "http://9/204" {
		t.Fatalf("secret 应沿用、test_url 应更新: %+v", got)
	}
}

func TestOpComposition(t *testing.T) {
	if op := SwitchOp("🚀 节点选择", "香港 01"); op.RefKey != "ops:switch:🚀 节点选择" || op.Method != "PUT" ||
		op.Path != "/proxies/%F0%9F%9A%80%20%E8%8A%82%E7%82%B9%E9%80%89%E6%8B%A9" || op.Body != `{"name":"香港 01"}` {
		t.Fatalf("SwitchOp 不符: %+v", op)
	}
	if op := DelayOp("香港 01", ""); op.Path != "/proxies/%E9%A6%99%E6%B8%AF%2001/delay" ||
		op.Query.Get("timeout") != "5000" || op.Query.Get("url") != DefaultTestURL {
		t.Fatalf("DelayOp 不符: %+v", op)
	}
	if op := GroupDelayOp("Auto", "http://9/204"); op.Path != "/group/Auto/delay" || op.Query.Get("url") != "http://9/204" {
		t.Fatalf("GroupDelayOp 不符: %+v", op)
	}
	if op := ReloadOp(); op.Query.Get("force") != "true" || op.Timeout != 25 {
		t.Fatalf("ReloadOp 不符: %+v", op)
	}
	if op := ModeOp("global"); op.Body != `{"mode":"global"}` {
		t.Fatalf("ModeOp 不符: %+v", op)
	}
}

func TestEnqueueComposesURLAndAuth(t *testing.T) {
	st := testStore(t)
	cfg := &Config{ServerID: 7, BaseURL: "http://192.168.50.10:9090/", Secret: "tok"}
	id, err := Enqueue(st, cfg, OverviewOp())
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNetTasks(7, 4)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("领取失败: %v %v", claimed, err)
	}
	task := claimed[0]
	if task.URL != "http://192.168.50.10:9090/configs" || task.Purpose != Purpose {
		t.Fatalf("URL/purpose 不符: %+v", task)
	}
	if task.Headers != "Authorization: Bearer tok\n" {
		t.Fatalf("Authorization 头未注入: %q", task.Headers)
	}
	_ = id
}

func TestParseResultReads(t *testing.T) {
	if out, err := ParseResult("version", 200, `{"meta":true,"version":"v1.19.2"}`, ""); err != nil || out.(ginH)["version"] != "v1.19.2" {
		t.Fatalf("version: %v %v", out, err)
	}

	out, err := ParseResult("overview", 200, `{"mode":"rule","log-level":"info","allow-lan":false,"mixed-port":7890,"tun":{"enable":true}}`, "")
	if err != nil {
		t.Fatal(err)
	}
	ov := out.(ginH)
	if ov["mode"] != "rule" || ov["mixed_port"] != 7890 || ov["tun"] != true {
		t.Fatalf("overview: %v", ov)
	}

	proxiesBody := `{"proxies":{
		"🚀 节点选择":{"type":"Selector","now":"香港 01","all":["香港 01","美国 01","DIRECT"],"history":[]},
		"香港 01":{"type":"Shadowsocks","udp":true,"alive":true,"history":[{"time":"t1","delay":120},{"time":"t2","delay":156}]},
		"美国 01":{"type":"Shadowsocks","udp":false,"alive":false,"history":[{"time":"t1","delay":0}]}
	}}`
	out, err = ParseResult("proxies", 200, proxiesBody, "")
	if err != nil {
		t.Fatal(err)
	}
	p := out.(ginH)
	groups := p["groups"].([]proxiesGroup)
	if len(groups) != 1 || groups[0].Name != "🚀 节点选择" || groups[0].Now != "香港 01" || len(groups[0].All) != 3 {
		t.Fatalf("groups: %+v", groups)
	}
	nodes := p["nodes"].(ginH)
	hk := nodes["香港 01"].(proxiesNode)
	if hk.Delay != 156 || !hk.Alive || !hk.UDP {
		t.Fatalf("香港 01: %+v", hk)
	}
	if nodes["美国 01"].(proxiesNode).Delay != 0 {
		t.Fatalf("美国 01 延迟应为 0")
	}

	connBody := `{"downloadTotal":1024,"uploadTotal":2048,"memory":8388608,"connections":[
		{"id":"abc","upload":10,"download":20,"start":"2026-10-18T10:00:00+08:00","chains":["香港 01","🚀 节点选择"],
		 "rule":"DomainSuffix","rulePayload":"example.com",
		 "metadata":{"network":"tcp","host":"www.example.com","destinationIP":"1.2.3.4","destinationPort":"443","sourceIP":"192.168.50.3","sourcePort":"51000","processPath":"/usr/bin/curl"}},
		{"id":"def","upload":1,"download":2,"start":"2026-10-18T10:00:01+08:00","chains":["DIRECT"],
		 "rule":"GeoIP","rulePayload":"cn",
		 "metadata":{"network":"tcp","host":"page.xawan.top","destinationIP":"5.6.7.8","destinationPort":"443","sourceIP":"","sourcePort":"","processPath":""}}
	]}`
	out, err = ParseResult("connections", 200, connBody, "")
	if err != nil {
		t.Fatal(err)
	}
	conn := out.(ginH)
	if conn["download_total"] != int64(1024) || conn["memory"] != int64(8388608) || conn["total"] != 2 {
		t.Fatalf("connections 头部: %v", conn)
	}
	list := conn["connections"].([]ginH)
	c0 := list[0]
	if c0["host"] != "www.example.com" || c0["dest"] != "1.2.3.4:443" || c0["process"] != "curl" ||
		c0["rule"] != "DomainSuffix example.com" {
		t.Fatalf("connection 裁剪: %v", c0)
	}
	// 空 processPath/source（394）：process 不得回 path.Base("") 的 "."，source 回空串
	c1 := list[1]
	if c1["process"] != "" || c1["source"] != "" {
		t.Fatalf("空 processPath/source 应回空串: %v", c1)
	}
	if chains := c0["chains"].([]string); len(chains) != 2 || chains[0] != "🚀 节点选择" || chains[1] != "香港 01" {
		t.Fatalf("chains 应反转: %v", chains)
	}

	out, err = ParseResult("rules", 200, `{"rules":[{"type":"DOMAIN-SUFFIX","payload":"example.com","proxy":"DIRECT","size":3}]}`, "")
	if err != nil {
		t.Fatal(err)
	}
	r := out.(ginH)
	if r["total"] != 1 || len(r["rules"].([]struct {
		Type    string `json:"type"`
		Payload string `json:"payload"`
		Proxy   string `json:"proxy"`
		Size    int64  `json:"size"`
	})) != 1 {
		t.Fatalf("rules: %v", r)
	}
}

func TestParseResultOps(t *testing.T) {
	if out, err := ParseResult("ops:switch:Auto", 204, "", ""); err != nil || out.(ginH)["ok"] != true {
		t.Fatalf("switch 204 应成功: %v %v", out, err)
	}
	if _, err := ParseResult("ops:mode", 400, `{"message":"mode: invalid"}`, ""); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("400 应带 message: %v", err)
	}
	out, err := ParseResult("ops:delay:香港 01", 200, `{"delay":88}`, "")
	if err != nil || out.(ginH)["delay"] != int64(88) {
		t.Fatalf("delay: %v %v", out, err)
	}
	if _, err := ParseResult("ops:delay:美国 01", http.StatusGatewayTimeout, `{"message":"timeout"}`, ""); err == nil {
		t.Fatal("504 应报错")
	}
	if _, err := ParseResult("ops:delay:美国 01", 200, `{"delay":0}`, ""); err == nil {
		t.Fatal("delay 0 应按超时报错")
	}
	out, err = ParseResult("ops:group_delay:Auto", 200, `{"香港 01":100,"美国 01":{"message":"An error occurred"}}`, "")
	if err != nil {
		t.Fatal(err)
	}
	dm := out.(ginH)["delays"].(map[string]int64)
	if dm["香港 01"] != 100 || dm["美国 01"] != 0 {
		t.Fatalf("group_delay: %v", dm)
	}
	if _, err := ParseResult("proxies", 401, `{"message":"Unauthorized"}`, ""); err == nil {
		t.Fatal("401 读面板应报错")
	}
	if _, err := ParseResult("proxies", 0, "", "dial tcp: connect refused"); err == nil || err.Error() != "dial tcp: connect refused" {
		t.Fatalf("agent 错误应原样透出: %v", err)
	}
}
