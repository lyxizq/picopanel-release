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
