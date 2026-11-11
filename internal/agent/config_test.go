package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadConfigCollectListens 端口采集开关：未配置缺省开启（兼容旧配置），
// collect_listens: false 显式关闭。
func TestLoadConfigCollectListens(t *testing.T) {
	load := func(t *testing.T, yaml string) *Config {
		t.Helper()
		p := filepath.Join(t.TempDir(), "agent.yaml")
		if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig([]string{"-config", p})
		if err != nil {
			t.Fatalf("加载配置: %v", err)
		}
		return cfg
	}

	if cfg := load(t, "mode: pull\nlisten: :19100\n"); !cfg.ListensEnabled() {
		t.Fatalf("未配置时应缺省开启端口采集")
	}
	if cfg := load(t, "mode: pull\nlisten: :19100\ncollect_listens: true\n"); !cfg.ListensEnabled() {
		t.Fatalf("collect_listens: true 应开启")
	}
	if cfg := load(t, "mode: pull\nlisten: :19100\ncollect_listens: false\n"); cfg.ListensEnabled() {
		t.Fatalf("collect_listens: false 应关闭")
	}
}

// TestLoadConfigPushInterval 上报周期（push 模式批量上报）：缺省 30s；
// 小于采集周期时钳到采集周期（上报再快不会比采集更细）；pull 模式不生效。
func TestLoadConfigPushInterval(t *testing.T) {
	load := func(t *testing.T, yaml string) *Config {
		t.Helper()
		p := filepath.Join(t.TempDir(), "agent.yaml")
		if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig([]string{"-config", p})
		if err != nil {
			t.Fatalf("加载配置: %v", err)
		}
		return cfg
	}

	if cfg := load(t, "mode: pull\nlisten: :19100\n"); cfg.PushInterval.D() != 0 {
		t.Fatalf("pull 模式不应设置 push_interval，got %v", cfg.PushInterval.D())
	}
	if cfg := load(t, "mode: push\nserver_url: http://10.0.0.1:8080\ntoken: t\n"); cfg.PushInterval.D() != 30*time.Second {
		t.Fatalf("push 模式缺省上报周期应 30s，got %v", cfg.PushInterval.D())
	}
	if cfg := load(t, "mode: push\nserver_url: http://10.0.0.1:8080\ntoken: t\npush_interval: 60s\ninterval: 10s\n"); cfg.PushInterval.D() != 60*time.Second {
		t.Fatalf("显式 push_interval 应生效，got %v", cfg.PushInterval.D())
	}
	if cfg := load(t, "mode: push\nserver_url: http://10.0.0.1:8080\ntoken: t\npush_interval: 5s\ninterval: 10s\n"); cfg.PushInterval.D() != 10*time.Second {
		t.Fatalf("push_interval 小于采集周期应钳到采集周期，got %v", cfg.PushInterval.D())
	}
}

// push 模式 server_url 公网强制 https 防回归（2026-10-23 安全加固）：
// http 仅允许私网/回环目标；公网 IP/域名一律拒绝。
func TestRequireHTTPSForPublic(t *testing.T) {
	ok := []string{
		"https://example.com",      // https 恒放行
		"http://10.0.0.1:8080",     // 私网 IP
		"http://127.0.0.1:8080",    // 回环
		"http://192.168.1.10:8080", // 私网
		"http://[::1]:8080",        // IPv6 回环
		"http://[fe80::1]:8080",    // 链路本地（带 zone 的由 ParseIP 兜底失败路径覆盖）
	}
	for _, u := range ok {
		if err := requireHTTPSForPublic(u); err != nil {
			t.Errorf("%q 应放行: %v", u, err)
		}
	}
	bad := []string{
		"http://8.8.8.8:8080",  // 公网 IP
		"http://1.2.3.4",       // 公网 IP
		"example.com:8080",     // 无 scheme：Parse 拆成 scheme=example.com，须启动期拒绝
		"ftp://10.0.0.1:8080",  // 显式非 http/https scheme
	}
	for _, u := range bad {
		if err := requireHTTPSForPublic(u); err == nil {
			t.Errorf("%q 公网 http 应拒绝", u)
		}
	}
}
