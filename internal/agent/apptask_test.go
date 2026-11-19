package agent

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 应用操作任务（54）执行面回归：docker 经 Engine API 的动词/幂等/404 分支、
// virsh 白名单与假可执行注入、任务类型分发兜底。

func fakeDocker(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	// 固定短名 socket：macOS unix 路径 104 字节上限，t.TempDir() 层级太深
	sock := filepath.Join(os.TempDir(), "lzd-apptask-test.sock")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); _ = os.Remove(sock) })
	go http.Serve(ln, handler) //nolint:errcheck // 测试桩
	prev := dockerSockPath
	dockerSockPath = sock
	t.Cleanup(func() { dockerSockPath = prev })
}

func TestDockerActionVerbs(t *testing.T) {
	var gotPath, gotMethod string
	fakeDocker(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path+"?"+r.URL.RawQuery, r.Method
		switch r.URL.Path {
		case "/v1.24/containers/web/stop":
			w.WriteHeader(http.StatusNoContent)
		case "/v1.24/containers/web/start":
			w.WriteHeader(http.StatusNotModified) // 已在运行 → 幂等成功
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	if res := dockerAction("web", "stop"); !res.OK || res.Message != "操作成功" {
		t.Fatalf("stop 应成功: %+v", res)
	}
	if gotMethod != http.MethodPost || !strings.Contains(gotPath, "t=10") {
		t.Fatalf("stop 应为 POST 且带优雅宽限参数: %s %s", gotMethod, gotPath)
	}
	if res := dockerAction("web", "start"); !res.OK || res.Message != "已在目标状态" {
		t.Fatalf("304 应视为幂等成功: %+v", res)
	}
	if res := dockerAction("ghost", "start"); res.OK {
		t.Fatalf("404 应失败: %+v", res)
	}
}

func TestVirshActionAndWhitelist(t *testing.T) {
	// 假 virsh 可执行：printf 回显参数（echo -n 在 POSIX sh 下会原样打出 -n），
	// 便于断言动词与目标透传
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' \"$1 $2\"\n"
	if err := os.WriteFile(filepath.Join(dir, "virsh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if res := virshAction("lvpmgjxz", "start"); !res.OK || res.Message != "start lvpmgjxz" {
		t.Fatalf("virsh start 应透传动词与目标: %+v", res)
	}
	if res := virshAction("x", "destroy"); !res.OK {
		t.Fatalf("destroy 在白名单内应可执行: %+v", res)
	}

	// 动词白名单兜底（doAppTask 层）
	if res := doAppTask(&AppTask{Kind: "vm", Target: "x", Action: "resume"}); res.OK {
		t.Fatalf("vm 非法动词应拒绝: %+v", res)
	}
	if res := doAppTask(&AppTask{Kind: "docker", Target: "x", Action: "kill"}); res.OK {
		t.Fatalf("docker 非法动词应拒绝: %+v", res)
	}
	if res := doAppTask(&AppTask{Kind: "ssh", Target: "x", Action: "start"}); res.OK {
		t.Fatalf("未知类型应拒绝: %+v", res)
	}

	// virsh 不存在 → 明确失败信息（PATH 指向不含 virsh 的空目录）
	t.Setenv("PATH", t.TempDir())
	if res := virshAction("x", "start"); res.OK || !strings.Contains(res.Message, "virsh") {
		t.Fatalf("缺 virsh 应报错: %+v", res)
	}
}

func TestRunAppTasksIsolation(t *testing.T) {
	// 单任务 panic/失败不连累其余任务，全部有回传记录
	fakeDocker(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	var results []int64
	tasks := []AppTask{
		{TaskID: 1, Kind: "docker", Target: "a", Action: "start"},
		{TaskID: 2, Kind: "bogus", Target: "b", Action: "boom"}, // 失败任务
		{TaskID: 3, Kind: "docker", Target: "c", Action: "start"},
	}
	RunAppTasks(&recordingReporter{posted: &results}, tasks)
	if len(results) != 3 {
		t.Fatalf("三个任务都应有回传，得到 %d: %v", len(results), results)
	}
}

// recordingReporter 捕获结果回传（RunAppTasks 收 appTaskPoster 小接口）。
type recordingReporter struct {
	posted *[]int64
}

func (r *recordingReporter) PostAppTaskResult(taskID int64, res appTaskResult) error {
	*r.posted = append(*r.posted, taskID)
	return nil
}
