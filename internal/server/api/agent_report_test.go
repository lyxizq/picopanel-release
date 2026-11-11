package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
)

// newReportRouter 建一个含 agent 上报路由的最小路由器 + 一台 push 机器。
func newReportRouter(t *testing.T) (*gin.Engine, *store.Store, int64, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	row, err := s.CreateServer("push-batch", "", "agent-tok-batch", 0)
	if err != nil {
		t.Fatal(err)
	}
	mode := "push"
	if err := s.UpdateServer(row.ID, store.ServerUpdate{CollectMode: &mode}); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	if err := Register(r, s, engine.New(s, engine.Config{}), &config.Config{}, "test", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return r, s, row.ID, "agent-tok-batch"
}

// postReport 以指定 token POST 一份上报体。
func postReport(r *gin.Engine, token string, body []byte) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/agent/report", bytes.NewReader(body))
	req.Header.Set("X-Agent-Token", token)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// TestAgentReportBatchIngest 批量上报信封：3 份样本按 interval_sec 从接收时刻
// 等距回填入库（服务端时钟权威）、告警只评最新、服务器信息取最新样本。
func TestAgentReportBatchIngest(t *testing.T) {
	r, s, id, tok := newReportRouter(t)

	mk := func(cpu float64, uptime uint64) proto.Report {
		return proto.Report{TS: time.Now().Unix(), CPU: cpu, Uptime: uptime,
			Sys: &proto.SysInfo{Hostname: "batch-host", AgentVersion: "v9.9"}}
	}
	env := mk(3.5, 300) // 顶层=最新样本（新 agent 的线格式）
	env.IntervalSec = 10
	env.Samples = []proto.Report{mk(1.5, 280), mk(2.5, 290), mk(3.5, 300)}
	body, _ := json.Marshal(&env)

	before := time.Now().Unix()
	w := postReport(r, tok, body)
	after := time.Now().Unix()
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"code":0`)) {
		t.Fatalf("批量上报应 200 code=0: %d %s", w.Code, w.Body.String())
	}

	// 三份样本各占一行、时间戳等距回填（最新=服务端接收秒）
	rows, err := s.WindowRows(id, before-120, after+1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("应落 3 行指标，实际 %d 行（%v）", len(rows), rows)
	}
	latestTS := rows[len(rows)-1].TS
	if latestTS < before || latestTS > after {
		t.Fatalf("最新样本应取服务端接收时刻，got %d want [%d,%d]", latestTS, before, after)
	}
	for i, row := range rows {
		wantCPU := []float64{1.5, 2.5, 3.5}[i]
		if row.CPU != wantCPU {
			t.Fatalf("第 %d 行 CPU 应 %v got %v", i, wantCPU, row.CPU)
		}
	}
	if rows[1].TS-latestTS != -10 || rows[0].TS-latestTS != -20 {
		t.Fatalf("样本应按 10s 等距回填: %v %v %v", rows[0].TS, rows[1].TS, latestTS)
	}
	if rows[0].Uptime != 280 || rows[len(rows)-1].Uptime != 300 {
		t.Fatalf("各行 uptime 应与样本一致: %v", rows)
	}

	// 服务器信息按最新样本 upsert
	got, err := s.GetServer(id)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Hostname != "batch-host" || got.AgentVersion != "v9.9" {
		t.Fatalf("服务器主机名/版本应取最新样本 sys: %+v", got)
	}
}

// TestAgentReportBatchClampInterval interval_sec 缺省/越界钳制：缺省按最小
// 间隔紧排不丢样本，上限防历史倒灌。
func TestAgentReportBatchClampInterval(t *testing.T) {
	mk := func(cpu float64) proto.Report { return proto.Report{CPU: cpu} }

	// 缺省（无 interval_sec）：按最小间隔 1s 紧排，3 份都入库不互相覆盖
	r, s, id, tok := newReportRouter(t)
	env := mk(9)
	env.Samples = []proto.Report{mk(1), mk(2), mk(3)}
	body, _ := json.Marshal(&env)
	if w := postReport(r, tok, body); w.Code != 200 {
		t.Fatalf("缺省 interval 批量上报应 200: %d %s", w.Code, w.Body.String())
	}
	rows, err := s.WindowRows(id, time.Now().Unix()-60, time.Now().Unix()+1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("缺省 interval 应落 3 行不覆盖，实际 %d 行", len(rows))
	}
	if rows[2].CPU != 3 {
		t.Fatalf("末位（最新）样本 CPU 应为 3，got %v", rows[2].CPU)
	}
	if rows[2].TS-rows[0].TS != 2 {
		t.Fatalf("缺省应按 1s 紧排，跨度应 2s: %v %v", rows[0].TS, rows[2].TS)
	}

	// 越界大值钳到 600：时间戳不早于 now-600×(n-1)。独立路由器隔离上段落库
	r2, s2, id2, tok2 := newReportRouter(t)
	env2 := mk(0)
	env2.IntervalSec = 100000
	env2.Samples = []proto.Report{mk(1), mk(2)}
	body2, _ := json.Marshal(&env2)
	if w := postReport(r2, tok2, body2); w.Code != 200 {
		t.Fatalf("越界 interval 上报应 200: %d %s", w.Code, w.Body.String())
	}
	floor := time.Now().Unix() - 600 // (n-1)×clamp = 600
	rows2, err := s2.WindowRows(id2, floor-2, time.Now().Unix()+1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows2) != 2 || rows2[0].TS < floor-1 {
		t.Fatalf("越界 interval 应钳到 600s（最早样本 ≥ %d）: %+v", floor-1, rows2)
	}
}

// TestAgentReportSingleStillWorks 单份上报（老 agent 线格式）走原路径：
// 一行指标 ts=接收时刻，无批量分支的额外行。
func TestAgentReportSingleStillWorks(t *testing.T) {
	r, s, id, tok := newReportRouter(t)

	body, _ := json.Marshal(&proto.Report{CPU: 42, Sys: &proto.SysInfo{Hostname: "old-agent"}})
	if w := postReport(r, tok, body); w.Code != 200 {
		t.Fatalf("单份上报应 200: %d %s", w.Code, w.Body.String())
	}
	rows, err := s.WindowRows(id, time.Now().Unix()-60, time.Now().Unix()+1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CPU != 42 {
		t.Fatalf("单份上报应落 1 行 CPU=42，got %+v", rows)
	}
}
