// 虚拟化与容器集成（439/440）：Proxmox VE（集群资源计数）、Docker（服务端所在
// 机器的 unix socket 直读，免服务地址）、Portainer（多环境容器汇总）。
package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"lz-monitor/internal/server/store"
)

// fetchProxmox 抓取 PVE 集群资源概览：GET /cluster/resources 按 type 统计。
// 鉴权二选一——API Token（整串 user@realm!tokenid=secret，配 Token 即用）或
// 账号密码（POST /access/ticket 换 PVEAuthCookie）。Token 需至少 PVEAuditor 级权限。
func fetchProxmox(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	jar, err := cookieJarNew()
	if err != nil {
		return nil, err
	}
	cl := &http.Client{Timeout: fetchTimeout, Jar: jar, Transport: transportFor(configBool(in.Config, "insecure"))}
	base := strings.TrimSuffix(in.URL, "/")
	headers := map[string]string{}

	if cred.Token != "" {
		headers["Authorization"] = "PVEAPIToken=" + cred.Token
	} else {
		if cred.Password == "" {
			return nil, fmt.Errorf("请填写 API Token（user@realm!tokenid=secret 整串）或账号密码")
		}
		form := urlValues{"username": {cred.Username}, "password": {cred.Password}}
		var ticket struct {
			Data struct {
				Ticket string `json:"ticket"`
			} `json:"data"`
		}
		if err := postFormJSON(cl, base+"/api2/json/access/ticket", nil, form, &ticket); err != nil {
			return nil, fmt.Errorf("PVE 登录失败: %w", err)
		}
		if ticket.Data.Ticket == "" {
			return nil, fmt.Errorf("PVE 登录失败（检查账号密码，注意用户名要带 realm，如 root@pam）")
		}
		headers["Cookie"] = "PVEAuthCookie=" + ticket.Data.Ticket
	}

	var res struct {
		Data []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if _, err := getJSON(cl, base+"/api2/json/cluster/resources", headers, &res); err != nil {
		return nil, fmt.Errorf("获取 PVE 集群资源失败: %w", err)
	}
	var nodesUp, nodesTotal, qemu, lxc int64
	for _, r := range res.Data {
		switch r.Type {
		case "node":
			nodesTotal++
			if r.Status == "online" {
				nodesUp++
			}
		case "qemu":
			if r.Status == "running" {
				qemu++
			}
		case "lxc":
			if r.Status == "running" {
				lxc++
			}
		}
	}
	return map[string]any{
		"nodes_up":     nodesUp,
		"nodes_total":  nodesTotal,
		"qemu_running": qemu,
		"lxc_running":  lxc,
		"vm_running":   qemu + lxc,
	}, nil
}

// fetchDocker 本机 Docker（服务端所在机器）：unix socket 直读 /version +
// /containers/json?all=1 + /images/json。免服务地址，socket 路径 config.sock
// （默认 /var/run/docker.sock，容器内部署需挂载）。
func fetchDocker(in store.Integration) (map[string]any, error) {
	sock := configString(in.Config, "sock", "/var/run/docker.sock")
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}
	cl := &http.Client{Timeout: fetchTimeout, Transport: tr}
	const host = "http://docker" // 占位主机名，实际走 DialContext

	var ver struct {
		Version string `json:"Version"`
	}
	if _, err := getJSON(cl, host+"/version", nil, &ver); err != nil {
		return nil, fmt.Errorf("连接 Docker socket 失败（%s）: %w", sock, err)
	}
	var containers []struct {
		State string `json:"State"`
	}
	if _, err := getJSON(cl, host+"/containers/json?all=1", nil, &containers); err != nil {
		return nil, fmt.Errorf("获取容器列表失败: %w", err)
	}
	var images []json.RawMessage
	if _, err := getJSON(cl, host+"/images/json", nil, &images); err != nil {
		return nil, fmt.Errorf("获取镜像列表失败: %w", err)
	}
	var running int64
	for _, c := range containers {
		if c.State == "running" {
			running++
		}
	}
	return map[string]any{
		"running": running,
		"stopped": int64(len(containers)) - running,
		"images":  int64(len(images)),
		"version": shortVersion(ver.Version),
	}, nil
}

// fetchPortainer 抓取 Portainer 多环境汇总：GET /api/endpoints（X-Api-Key），
// 环境自带 Snapshots 容器统计（配置了定时快照时）；缺快照的环境逐个兜底拉
// /docker/containers/json（封顶 8 个，防多环境拖慢轮询）。
func fetchPortainer(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Token == "" {
		return nil, fmt.Errorf("请填写 Portainer API Key（设置→账户→API 密钥）")
	}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")
	headers := map[string]string{"X-Api-Key": cred.Token}

	var endpoints []struct {
		ID        int64  `json:"Id"`
		Name      string `json:"Name"`
		Status    int    `json:"Status"`
		Type      int    `json:"Type"`
		Snapshots []struct {
			Running int64 `json:"RunningContainerCount"`
			Stopped int64 `json:"StoppedContainerCount"`
			Stacks  int64 `json:"StackCount"`
		} `json:"Snapshots"`
	}
	if _, err := getJSON(cl, base+"/api/endpoints", headers, &endpoints); err != nil {
		if httpStatus(err) == http.StatusUnauthorized {
			return nil, fmt.Errorf("Portainer API Key 无效")
		}
		return nil, fmt.Errorf("获取 Portainer 环境失败: %w", err)
	}
	var up, running, stopped, stacks int64
	for i, ep := range endpoints {
		if ep.Status == 1 {
			up++
		}
		if len(ep.Snapshots) > 0 {
			running += ep.Snapshots[0].Running
			stopped += ep.Snapshots[0].Stopped
			stacks += ep.Snapshots[0].Stacks
			continue
		}
		// 无快照兜底：直连该环境 docker（Portainer 边车代理；K8s 环境 Type=2 跳过）
		if ep.Type != 1 || i >= 8 {
			continue
		}
		var containers []struct {
			State string `json:"State"`
		}
		path := fmt.Sprintf("%s/api/endpoints/%d/docker/containers/json?all=1", base, ep.ID)
		if _, err := getJSON(cl, path, headers, &containers); err != nil {
			continue
		}
		for _, c := range containers {
			if c.State == "running" {
				running++
			} else {
				stopped++
			}
		}
	}
	return map[string]any{
		"endpoints_up":    up,
		"endpoints_total": int64(len(endpoints)),
		"running":         running,
		"stopped":         stopped,
		"stacks":          stacks,
	}, nil
}

// cookieJarNew cookiejar.New（参数 nil 不会失败，错误仅为满足签名）。
func cookieJarNew() (*cookiejar.Jar, error) { return cookiejar.New(nil) }

// urlValues 表单值别名（PVE ticket 登录）。
type urlValues = url.Values

// postFormJSON 表单 POST 并解析 JSON 应答（PVE ticket 登录）。
func postFormJSON(cl *http.Client, rawURL string, headers map[string]string, form urlValues, out any) error {
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return &httpStatusError{Code: resp.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}
