package agent

import (
	"os"
	"path/filepath"
	"testing"
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
		"http://8.8.8.8:8080", // 公网 IP
		"http://1.2.3.4",      // 公网 IP
	}
	for _, u := range bad {
		if err := requireHTTPSForPublic(u); err == nil {
			t.Errorf("%q 公网 http 应拒绝", u)
		}
	}
}
