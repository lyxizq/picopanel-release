// qBittorrent WebUI 客户端（Homepage widget 同口径）：登录（或 ≥5.2 API Key
// 免登录）→ /api/v2/transfer/info 传输速率 + /api/v2/torrents/info 任务计数。
// 快照四格：下载中(leech) / 下载速率 / 做种(seed) / 上传速率。
package integrations

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"

	"lz-monitor/internal/server/store"
)

// fetchQBit 抓取一帧 qBittorrent 快照。鉴权两选一：
//   - Token（qB ≥5.2 API Key）：请求带 X-Api-Key 头，无状态免登录；
//   - Username/Password：POST /api/v2/auth/login 取 SID cookie（cookiejar 自动携带）。
//
// 注意：qB 对连续登录失败有 IP 封禁（默认 5 次失败封 3600s），登录只在
// 无凭据会话时进行一次，失败即返回错误不重试。
func fetchQBit(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	insecure := configBool(in.Config, "insecure")
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	cl := &http.Client{Timeout: fetchTimeout, Jar: jar, Transport: transportFor(insecure)}
	base := strings.TrimSuffix(in.URL, "/")

	var headers map[string]string
	if cred.Token != "" {
		headers = map[string]string{"X-Api-Key": cred.Token}
	} else {
		if cred.Username == "" && cred.Password == "" {
			return nil, fmt.Errorf("未配置凭据（账号密码或 ≥5.2 API Key）")
		}
		if err := qbitLogin(cl, base, cred); err != nil {
			return nil, err
		}
	}

	var transfer struct {
		DLSpeed int64 `json:"dl_info_speed"`
		UPSpeed int64 `json:"up_info_speed"`
	}
	if _, err := getJSON(cl, base+"/api/v2/transfer/info", headers, &transfer); err != nil {
		return nil, fmt.Errorf("获取传输信息失败: %w", err)
	}
	var torrents []struct {
		State string `json:"state"`
	}
	if _, err := getJSON(cl, base+"/api/v2/torrents/info", headers, &torrents); err != nil {
		return nil, fmt.Errorf("获取任务列表失败: %w", err)
	}
	leech, seed := 0, 0
	for _, t := range torrents {
		switch t.State {
		case "downloading", "metadl", "forcedDL", "stalledDL", "checkingDL", "checkingResumeData", "allocating", "queuedDL":
			leech++
		case "uploading", "stalledUP", "forcedUP", "checkingUP", "queuedUP":
			seed++
		}
	}
	return map[string]any{
		"download_bps": transfer.DLSpeed,
		"upload_bps":   transfer.UPSpeed,
		"leech":        leech,
		"seed":         seed,
	}, nil
}

// qbitLogin 登录换取 SID cookie；qB 成功返回 200 + "Ok."，失败多为 200 +
// "Fails."（账号密码错）。
func qbitLogin(cl *http.Client, base string, cred Credentials) error {
	form := url.Values{"username": {cred.Username}, "password": {cred.Password}}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v2/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("连接 qBittorrent 失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Ok") {
		return fmt.Errorf("qBittorrent 登录失败（检查账号密码；连续失败会触发 IP 封禁）")
	}
	return nil
}

// transportFor 自签跳过传输层（nil=默认）。
func transportFor(insecure bool) http.RoundTripper {
	if insecure {
		return insecureTransport()
	}
	return nil
}

// insecureTransport 跳过 TLS 校验的传输层（用户显式开启的自签跳过选项）。
// 包级单例复用：每 30s 一轮的轮询若每次新建 Transport，连接零复用、每轮
// 重做 TLS 握手，旧 Transport 的空闲连接只能靠 GC 兜底。
var (
	insecureTransportOnce sync.Once
	insecureTransportInst http.RoundTripper
)

func insecureTransport() http.RoundTripper {
	insecureTransportOnce.Do(func() {
		insecureTransportInst = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- 用户显式开启
	})
	return insecureTransportInst
}

// getJSON GET 请求并解析 JSON 应答（2xx 校验，4xx/5xx 转错误）。
func getJSON(cl *http.Client, url string, headers map[string]string, out any) (http.Header, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{Code: resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return nil, fmt.Errorf("应答解析失败: %w", err)
	}
	return resp.Header, nil
}
