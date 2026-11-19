package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"lz-monitor/internal/conf"
	"lz-monitor/internal/proto"
)

func confDuration(d time.Duration) conf.Duration { return conf.Duration(d) }

// TestReporterPostEnvelope 批量信封线格式：顶层标量=最新样本（老服务端按
// 单份处理不炸）、Samples 升序全量、IntervalSec 携带采样间隔。
func TestReporterPostEnvelope(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Agent-Token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	rep := NewReporter(srv.URL, "tok", time.Second)
	reps := []*proto.Report{
		{TS: 1, CPU: 1.5},
		{TS: 2, CPU: 2.5},
		{TS: 3, CPU: 3.5},
	}
	if _, err := rep.Post(reps, 10*time.Second); err != nil {
		t.Fatalf("Post: %v", err)
	}

	var env proto.Report
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("信封解析: %v", err)
	}
	if env.CPU != 3.5 || env.TS != 3 {
		t.Fatalf("顶层应为最新样本，got cpu=%v ts=%d", env.CPU, env.TS)
	}
	if len(env.Samples) != 3 || env.Samples[0].CPU != 1.5 || env.Samples[2].CPU != 3.5 {
		t.Fatalf("Samples 应按时间升序含全部样本: %+v", env.Samples)
	}
	if env.IntervalSec != 10 {
		t.Fatalf("IntervalSec 应为 10，got %d", env.IntervalSec)
	}
}

// TestReporterPostByteBudget 字节预算：超预算丢最旧样本重编；裁到只剩 1 份
// 时退化为纯单份线格式（不带 Samples）——单份报告的合法体积上限与服务端
// 请求上限同为 1MB，Samples 里再放一份会凭空翻倍、把今天能跑的机器弄挂。
func TestReporterPostByteBudget(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	pad := strings.Repeat("x", 350<<10) // 每份 ~350KB（Sys.Hostname 携带填充）
	mk := func(i int64) *proto.Report {
		return &proto.Report{TS: i, CPU: float64(i), Sys: &proto.SysInfo{Hostname: pad}}
	}
	rep := NewReporter(srv.URL, "tok", time.Second)
	if _, err := rep.Post([]*proto.Report{mk(1), mk(2), mk(3)}, 10*time.Second); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if len(gotBody) > 1<<20 {
		t.Fatalf("裁剪后请求体仍超服务端 1MB 上限: %d", len(gotBody))
	}
	var env proto.Report
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("信封解析: %v", err)
	}
	if env.TS != 3 || env.CPU != 3 {
		t.Fatalf("顶层应始终是最新样本, got ts=%d cpu=%v", env.TS, env.CPU)
	}
	if len(env.Samples) > 1 {
		t.Fatalf("350KB×3 应裁到 ≤1 份, got %d", len(env.Samples))
	}
}

// TestReporterPostSingleNoSamples 单样本上报退化纯单份线格式：Samples 为空、
// 体积≈单份报告本身（服务端走原单份路径）。
func TestReporterPostSingleNoSamples(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	rep := NewReporter(srv.URL, "tok", time.Second)
	if _, err := rep.Post([]*proto.Report{{TS: 7, CPU: 1.5}}, 10*time.Second); err != nil {
		t.Fatalf("Post: %v", err)
	}
	var env proto.Report
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if env.Samples != nil || env.IntervalSec != 0 {
		t.Fatalf("单样本应不带信封字段: samples=%v interval=%d", env.Samples, env.IntervalSec)
	}
	if env.TS != 7 || env.CPU != 1.5 {
		t.Fatalf("单份字段应完整: %+v", env)
	}
}

// recordedPost 记录批量上报测试期间收到的每个请求体（含首请求失败注入）。
type recordedPost struct {
	mu     sync.Mutex
	bodies []proto.Report
	failN  int // 前 N 个请求返回 500
}

func (rp *recordedPost) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rp.mu.Lock()
		i := len(rp.bodies)
		var env proto.Report
		_ = json.NewDecoder(r.Body).Decode(&env)
		rp.bodies = append(rp.bodies, env)
		rp.mu.Unlock()
		if i < rp.failN {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{}`))
	}
}

func (rp *recordedPost) snapshot() []proto.Report {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	out := make([]proto.Report, len(rp.bodies))
	copy(out, rp.bodies)
	return out
}

// TestRunPushBatching 攒样批量上报端到端：启动立即单份上报（首请求注入 500
// 模拟链路故障）→ 采集按 interval 持续攒样 → 到上报点批量发出，失败期间
// 样本保留续攒（后续请求捎带此前的留存样本），批量规模不超 backlog 上限。
func TestRunPushBatching(t *testing.T) {
	rp := &recordedPost{failN: 1} // 首个（启动立即）请求失败
	srv := httptest.NewServer(rp.handler())
	defer srv.Close()

	cfg := &Config{
		Mode:         "push",
		ServerURL:    srv.URL,
		Token:        "tok",
		Interval:     confDuration(10 * time.Millisecond),
		PushInterval: confDuration(30 * time.Millisecond),
	}
	counter := 0
	collectOnce := func() *proto.Report {
		counter++
		return &proto.Report{TS: int64(counter), CPU: float64(counter)}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = runPush(ctx, cfg, Runtime{Version: "test"}, collectOnce)
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	reqs := rp.snapshot()
	if len(reqs) < 3 {
		t.Fatalf("应有多次上报尝试（启动+批量），实际 %d 次", len(reqs))
	}
	if len(reqs[0].Samples) > 1 || reqs[0].CPU != 1 {
		t.Fatalf("启动首次上报应为单份样本（退化纯单份线格式），got %+v", reqs[0])
	}
	// 首次失败后留存样本必须出现在后续批量里（断流不丢数据）
	retained := false
	for _, req := range reqs[1:] {
		if len(req.Samples) >= 2 {
			retained = true
		}
		if len(req.Samples) > pushMaxBacklog {
			t.Fatalf("批量规模不应超 backlog 上限 %d，got %d", pushMaxBacklog, len(req.Samples))
		}
		// 顶层=最新样本
		if len(req.Samples) > 0 && req.Samples[len(req.Samples)-1].CPU != req.CPU {
			t.Fatalf("顶层应等于末位样本: top=%v last=%v", req.CPU, req.Samples[len(req.Samples)-1].CPU)
		}
	}
	if !retained {
		t.Fatalf("失败后留存样本未随下次批量发出: %+v", reqs)
	}
}
