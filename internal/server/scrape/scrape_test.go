package scrape

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
)

// 拉取链路端到端：agent /metrics 的 sys（含 agent_version）必须经解码入库
// 写进 servers 行（2026-10-06 现场排查钉链路用——机器侧 JSON 正常而列表版本
// 空时，此测试可证明/排除服务端这一段）。
func TestScrapeAgentWritesAgentVersion(t *testing.T) {
	rep := &proto.Report{TS: time.Now().Unix(), CPU: 1.5, MemTotal: 100, MemUsed: 50}
	rep.Sys = &proto.SysInfo{Hostname: "h", OS: "Ubuntu", Kernel: "5.15", Arch: "amd64", CPUCores: 4, AgentVersion: "v1.0.7"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Server-Token") != "tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rep)
	}))
	defer srv.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row, err := s.CreateServer("pull", srv.URL, "tok", 0)
	if err != nil {
		t.Fatal(err)
	}
	m := New(s, engine.New(s, engine.Config{}), Config{Interval: time.Minute, Timeout: 5 * time.Second})
	if err := m.scrapeAgent(*row); err != nil {
		t.Fatalf("scrapeAgent: %v", err)
	}
	got, err := s.GetServer(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentVersion != "v1.0.7" {
		t.Fatalf("拉取入库应写入 agent_version，得到 %q（hostname=%q）", got.AgentVersion, got.Hostname)
	}
	if got.Hostname != "h" {
		t.Fatalf("hostname 应同步更新: %q", got.Hostname)
	}
}
