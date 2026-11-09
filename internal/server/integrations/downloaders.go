// 下载器集成（437）：Transmission（RPC 409 会话握手）、Deluge（Web JSON-RPC）、
// aria2（JSON-RPC token 认证）。qB 同款四格口径：任务计数 + 上/下行速率。
package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"

	"lz-monitor/internal/server/store"
)

// fetchTransmission 抓取 Transmission session-stats：POST /transmission/rpc，
// 无会话 ID 时上游回 409 并在 X-Transmission-Session-Id 头下发，携带重试即可
// （整个进程生命期内每次抓取都重握手也无妨，RPC 幂等且轮询间隔 30s）。
func fetchTransmission(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	cl := &http.Client{Timeout: fetchTimeout, Jar: jar, Transport: transportFor(configBool(in.Config, "insecure"))}
	base := strings.TrimSuffix(in.URL, "/")
	rpcURL := base + "/transmission/rpc"
	body := []byte(`{"method":"session-stats"}`)

	post := func(sid string) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodPost, rpcURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if cred.Username != "" || cred.Password != "" {
			req.SetBasicAuth(cred.Username, cred.Password)
		}
		if sid != "" {
			req.Header.Set("X-Transmission-Session-Id", sid)
		}
		resp, err := cl.Do(req)
		if err != nil {
			return nil, fmt.Errorf("连接 Transmission 失败: %w", err)
		}
		return resp, nil
	}

	resp, err := post("")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusConflict {
		sid := resp.Header.Get("X-Transmission-Session-Id")
		resp.Body.Close()
		resp, err = post(sid)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("Transmission RPC 失败: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Result    string `json:"result"`
		Arguments struct {
			Active int64 `json:"activeTorrentCount"`
			Paused int64 `json:"pausedTorrentCount"`
			Total  int64 `json:"torrentCount"`
			DL     int64 `json:"downloadSpeed"`
			UP     int64 `json:"uploadSpeed"`
		} `json:"arguments"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("应答解析失败: %w", err)
	}
	if out.Result != "success" {
		return nil, fmt.Errorf("Transmission RPC 返回 %s", out.Result)
	}
	return map[string]any{
		"active":       out.Arguments.Active,
		"paused":       out.Arguments.Paused,
		"total":        out.Arguments.Total,
		"download_bps": out.Arguments.DL,
		"upload_bps":   out.Arguments.UP,
	}, nil
}

// fetchDeluge 抓取 Deluge Web 状态：/json 单请求 JSON-RPC——auth.login 换会话
// cookie（cookiejar 自动携带），web.update_ui 全量任务按 state 计数并汇总速率。
func fetchDeluge(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Password == "" {
		return nil, fmt.Errorf("请填写 Deluge Web UI 密码")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	cl := &http.Client{Timeout: fetchTimeout, Jar: jar, Transport: transportFor(configBool(in.Config, "insecure"))}
	base := strings.TrimSuffix(in.URL, "/")

	delugeRPC := func(method string, params []any, out any) error {
		payload := map[string]any{"method": method, "params": params, "id": 1}
		return postJSON(cl, base+"/json", nil, payload, out)
	}
	var login struct {
		Result bool `json:"result"`
		Error  any  `json:"error"`
	}
	if err := delugeRPC("auth.login", []any{cred.Password}, &login); err != nil {
		return nil, fmt.Errorf("连接 Deluge 失败: %w", err)
	}
	if !login.Result {
		return nil, fmt.Errorf("Deluge 登录失败（检查 Web UI 密码）")
	}
	var ui struct {
		Result struct {
			Torrents map[string]struct {
				State string  `json:"state"`
				DL    float64 `json:"download_payload_rate"`
				UP    float64 `json:"upload_payload_rate"`
			} `json:"torrents"`
			Error any `json:"error"`
		} `json:"result"`
	}
	if err := delugeRPC("web.update_ui", []any{
		[]string{"state", "download_payload_rate", "upload_payload_rate"}, map[string]any{},
	}, &ui); err != nil {
		return nil, fmt.Errorf("获取 Deluge 状态失败: %w", err)
	}
	if ui.Result.Error != nil {
		return nil, fmt.Errorf("Deluge Web 未连接到守护进程（先在 Web UI 里连上本地 daemon）")
	}
	var leech, seed, paused int64
	var dl, up float64
	for _, t := range ui.Result.Torrents {
		switch t.State {
		case "Downloading", "Checking", "Moving", "Allocating", "Metadata":
			leech++
		case "Seeding":
			seed++
		default:
			paused++
		}
		dl += t.DL
		up += t.UP
	}
	return map[string]any{
		"leech":        leech,
		"seed":         seed,
		"paused":       paused,
		"download_bps": int64(dl),
		"upload_bps":   int64(up),
	}, nil
}

// fetchAria2 抓取 aria2 全局统计：JSON-RPC system.getGlobalStat，密钥以
// "token:SECRET" 首参携带（可选）。/jsonrpc 404 时回落 /rpc（两种常见暴露路径）。
func fetchAria2(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	cl := &http.Client{Timeout: fetchTimeout, Transport: transportFor(configBool(in.Config, "insecure"))}
	base := strings.TrimSuffix(in.URL, "/")
	params := []any{}
	if cred.Token != "" {
		params = []any{"token:" + cred.Token}
	}
	rpc := func(endpoint string) (map[string]any, error) {
		var out struct {
			Result map[string]any `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		err := postJSON(cl, base+endpoint, nil, map[string]any{
			"jsonrpc": "2.0", "id": "picopanel", "method": "system.getGlobalStat", "params": params,
		}, &out)
		if err != nil {
			return nil, err
		}
		if out.Error != nil {
			return nil, fmt.Errorf("aria2: %s", out.Error.Message)
		}
		return out.Result, nil
	}
	res, err := rpc("/jsonrpc")
	if err != nil && httpStatus(err) == http.StatusNotFound {
		res, err = rpc("/rpc")
	}
	if err != nil {
		return nil, fmt.Errorf("获取 aria2 统计失败: %w", err)
	}
	return map[string]any{
		"active":       int64(numAt(res, "activeCount")),
		"waiting":      int64(numAt(res, "waitingCount")),
		"stopped":      int64(numAt(res, "stoppedCount")),
		"download_bps": int64(numAt(res, "downloadSpeed")),
		"upload_bps":   int64(numAt(res, "uploadSpeed")),
	}, nil
}
