package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"lz-monitor/internal/proto"
)

// AppsCollector 采集本机应用负载快照（54 飞牛 fnOS 集成，任意 Linux 通用）：
//   - Docker 容器：/var/run/docker.sock 直读 Engine API（containers/json?all=1，
//     v1.24 版本路径保兼容），socket 不存在 = 本机无 Docker，静默留空；
//   - 虚拟机：virsh list --all（fnOS 官方虚拟机 app 即 KVM/libvirt），二进制
//     不存在或 libvirtd 不可达同样静默留空。
//
// 两者都是「能力探测失败 = 无此能力」而非错误：缺 Docker/libvirt 的机器上
// 不产生任何日志噪音，服务端按无数据展示。采集器无状态（每轮全量快照）。
type AppsCollector struct {
	sockPath string                               // Docker socket（生产 /var/run/docker.sock；测试注入临时 socket）
	virsh    func(args ...string) (string, error) // virsh 执行入口；nil=未安装禁用
	client   *http.Client                         // unix socket HTTP 客户端（测试可换）
}

// apiVersion 固定请求 Docker Engine API v1.24（能支持的最低版本，字段够用，
// 新版 daemon 长期向后兼容）。
const dockerAPIVersion = "v1.24"

func NewAppsCollector() *AppsCollector {
	c := &AppsCollector{sockPath: "/var/run/docker.sock"}
	if path, err := exec.LookPath("virsh"); err == nil {
		vp := path
		c.virsh = func(args ...string) (string, error) {
			out, err := exec.Command(vp, args...).Output()
			if err != nil {
				// virsh 失败细节在 stderr（libvirtd 未跑/socket 权限等），带上便于排查
				if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
					return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
				}
				return "", err
			}
			return string(out), nil
		}
	}
	c.client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", c.sockPath, 3*time.Second)
			},
		},
	}
	return c
}

func (c *AppsCollector) Name() string { return "apps" }

func (c *AppsCollector) Collect(r *proto.Report) error {
	r.Containers = c.dockerContainers()
	r.VMs = c.virshVMs()
	return nil
}

// dockerContainers 直读 Engine API 容器清单（含已停止）。任何失败（socket
// 不存在/权限不足/daemon 未跑）都按「无 Docker」处理返回 nil。
func (c *AppsCollector) dockerContainers() []proto.ContainerInfo {
	req, err := http.NewRequest(http.MethodGet, "http://docker/"+dockerAPIVersion+"/containers/json?all=1", nil)
	if err != nil {
		return nil
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil
	}
	var rows []struct {
		ID      string   `json:"Id"`
		Names   []string `json:"Names"`
		Image   string   `json:"Image"`
		State   string   `json:"State"`
		Status  string   `json:"Status"`
		Created int64    `json:"Created"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil
	}
	out := make([]proto.ContainerInfo, 0, len(rows))
	for _, row := range rows {
		name := ""
		if len(row.Names) > 0 {
			name = strings.TrimPrefix(row.Names[0], "/")
		}
		id := row.ID
		if len(id) > 12 {
			id = id[:12]
		}
		out = append(out, proto.ContainerInfo{ID: id, Name: name, Image: row.Image,
			State: row.State, Status: row.Status, Created: row.Created})
	}
	return out
}

// virshVMs 解析 virsh list --all 输出：表头 + 分隔线之后每行
// 「Id Name State…」（Id 为 - 表示关机态）。任何失败按「无虚拟机」返回 nil。
func (c *AppsCollector) virshVMs() []proto.VMInfo {
	if c.virsh == nil {
		return nil
	}
	outRaw, err := c.virsh("list", "--all")
	if err != nil {
		return nil
	}
	var out []proto.VMInfo
	for _, line := range strings.Split(outRaw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Id") {
			continue // 表头 / 空行
		}
		if strings.Trim(line, "- ") == "" {
			continue // 分隔线（纯 - 与空格；Id 列为 "-" 的数据行 Trim 后非空，保留）
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		id := ""
		if fields[0] != "-" {
			id = fields[0]
		}
		state := "unknown"
		if len(fields) >= 3 {
			state = strings.Join(fields[2:], " ") // virsh 状态可含空格（shut off）
		}
		out = append(out, proto.VMInfo{ID: id, Name: fields[1], State: state})
	}
	return out
}
