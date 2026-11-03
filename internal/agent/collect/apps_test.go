package collect

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/proto"
)

// AppsCollector（54）回归：Docker 直读 Engine API、virsh 输出解析、能力缺失
// 静默留空。

// fakeDockerEngine 起一个 TCP 测试服务假装 Docker Engine，并返回一个把
// "http://docker/" 流量都拨过去的客户端（绕开 macOS unix socket 104 字节
// 路径上限，测试临时目录层级太深）。
func fakeDockerEngine(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
			},
		},
	}
}

func newTestAppsCollector(client *http.Client, virsh func(...string) (string, error)) *AppsCollector {
	return &AppsCollector{sockPath: "/var/run/docker.sock", virsh: virsh, client: client}
}

func TestAppsCollectorDockerAndVMs(t *testing.T) {
	client := fakeDockerEngine(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.24/containers/json" || r.URL.Query().Get("all") != "1" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"Id": "abc123def45678901234", "Names": []string{"/web"}, "Image": "nginx:latest",
				"State": "running", "Status": "Up 3 days (healthy)", "Created": 1700000000},
			{"Id": "fff00011122233344455", "Names": []string{"/cache"}, "Image": "redis:7",
				"State": "exited", "Status": "Exited (0) 2 hours ago", "Created": 1700000001},
		})
	})
	virsh := func(args ...string) (string, error) {
		if strings.Join(args, " ") != "list --all" {
			t.Fatalf("virsh 参数应固定为 list --all，得到 %v", args)
		}
		return " Id   Name      State\n----------------------------\n 3    lvpmgjxz  running\n -    hy6dek9c  shut off\n", nil
	}
	c := newTestAppsCollector(client, virsh)
	r := &proto.Report{}
	if err := c.Collect(r); err != nil {
		t.Fatal(err)
	}
	if len(r.Containers) != 2 {
		t.Fatalf("应采到 2 个容器: %+v", r.Containers)
	}
	web := r.Containers[0]
	if web.ID != "abc123def456" || web.Name != "web" || web.Image != "nginx:latest" || web.State != "running" {
		t.Fatalf("容器字段不符: %+v", web)
	}
	if r.Containers[1].State != "exited" {
		t.Fatalf("第二容器应 exited: %+v", r.Containers[1])
	}
	if len(r.VMs) != 2 {
		t.Fatalf("应采到 2 台虚拟机: %+v", r.VMs)
	}
	if r.VMs[0].ID != "3" || r.VMs[0].Name != "lvpmgjxz" || r.VMs[0].State != "running" {
		t.Fatalf("运行中虚拟机不符: %+v", r.VMs[0])
	}
	if r.VMs[1].ID != "" || r.VMs[1].Name != "hy6dek9c" || r.VMs[1].State != "shut off" {
		t.Fatalf("关机虚拟机应为空 ID + shut off: %+v", r.VMs[1])
	}
}

func TestAppsCollectorCapabilityMissing(t *testing.T) {
	// Docker 不可达（拨号失败）→ 容器 nil；virsh 未安装（nil 入口）→ VMs nil；
	// 均不报错
	deadClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return nil, errors.New("no docker here")
			},
		},
	}
	c := newTestAppsCollector(deadClient, nil)
	r := &proto.Report{}
	if err := c.Collect(r); err != nil {
		t.Fatalf("能力缺失不该报错: %v", err)
	}
	if r.Containers != nil || r.VMs != nil {
		t.Fatalf("能力缺失应留空: %#v %#v", r.Containers, r.VMs)
	}

	// virsh 存在但 libvirtd 不可达 → 同样静默 nil
	c = newTestAppsCollector(deadClient, func(...string) (string, error) {
		return "", errors.New("libvirtd 不可达")
	})
	r = &proto.Report{}
	if err := c.Collect(r); err != nil {
		t.Fatalf("virsh 失败不该报错: %v", err)
	}
	if r.VMs != nil {
		t.Fatalf("virsh 失败应留空: %#v", r.VMs)
	}
}
