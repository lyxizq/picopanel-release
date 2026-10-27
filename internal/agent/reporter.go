package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lz-monitor/internal/proto"
)

// Reporter 是 push 模式的上报客户端：POST <server_url>/api/v1/agent/report，
// 头 X-Agent-Token 携带服务端创建该服务器时生成的接入密钥。
type Reporter struct {
	base   string // 服务端根地址，如 http://10.0.0.1:8080（已去尾部斜杠）
	token  string
	client *http.Client
}

// NewReporter 创建上报客户端，timeout 为单次上报超时。
func NewReporter(serverURL, token string, timeout time.Duration) *Reporter {
	return &Reporter{
		base:   strings.TrimRight(serverURL, "/"),
		token:  token,
		client: &http.Client{Timeout: timeout},
	}
}

// UpgradeHint 是上报响应捎带的升级指令（396）：Version 为目标版本串，
// agent 收到后凭自身身份回源 FetchBundle 下载分发束。
type UpgradeHint struct {
	Version string `json:"version"`
}

// PostResult 上报响应捎带的服务端下发载荷：PT 中转抓取任务（391）、通用中转
// 请求任务（548）、B 站追更任务（TODO 6）、应用操作任务（54）与在线升级指令（396）。
type PostResult struct {
	PtTasks   []PtTask         `json:"pt_tasks"`
	NetTasks  []NetTask        `json:"net_tasks"`
	BiliTasks []proto.BiliTask `json:"bili_tasks"`
	BiliStops []int64          `json:"bili_stops"` // 追更手动停止命令（8，follow_id 列表）
	AppTasks  []AppTask        `json:"app_tasks"`  // Docker/虚拟机启停操作（54）
	Upgrade   *UpgradeHint     `json:"upgrade"`
}

// Post 提交一份上报。失败返回错误（调用方只记日志，下周期自然重试；
// 401 单独提示核对密钥）。响应体捎带服务端下发的任务/指令，返回给调用方处理。
func (r *Reporter) Post(rep *proto.Report) (PostResult, error) {
	var out PostResult
	body, err := json.Marshal(rep)
	if err != nil {
		return out, fmt.Errorf("序列化上报: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, r.base+"/api/v1/agent/report", bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", r.token)

	resp, err := r.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return out, errors.New("服务端拒绝（401）：token 与服务端配置不一致，请在服务端核对该服务器的访问密钥")
	case resp.StatusCode != http.StatusOK:
		return out, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return PostResult{}, nil // 响应体解析失败不影响上报本身（旧版服务端无任务字段）
	}
	return out, nil
}

// PostNetTaskResult 回传通用中转任务执行结果（结果/错误二选一，任何失败
// 都不要求 agent 重试——server 端租约超时机制兜底回收）。
func (r *Reporter) PostNetTaskResult(taskID int64, res netTaskResult) error {
	payload := struct {
		TaskID     int64  `json:"task_id"`
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
		Error      string `json:"error"`
	}{TaskID: taskID, StatusCode: res.StatusCode, Body: res.Body, Error: res.Error}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, r.base+"/api/v1/agent/net-task-result", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// PostBiliTaskResult 回传 B 站追更任务结果（成功失败都回——结果里带 ok/error，
// server 端租约超时机制兜底收尾）。
func (r *Reporter) PostBiliTaskResult(taskID int64, res proto.BiliTaskResult) error {
	payload := struct {
		TaskID int64                `json:"task_id"`
		Result proto.BiliTaskResult `json:"result"`
	}{TaskID: taskID, Result: res}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, r.base+"/api/v1/agent/bili-task-result", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// PostAppTaskResult 回传应用操作任务结果（54；成功失败都回，server 端租约
// 超时机制兜底收尾）。
func (r *Reporter) PostAppTaskResult(taskID int64, res appTaskResult) error {
	payload := struct {
		TaskID  int64  `json:"task_id"`
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}{TaskID: taskID, OK: res.OK, Message: res.Message}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, r.base+"/api/v1/agent/app-task-result", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// FetchBundle 下载升级分发束（manifest+签名+更新包多部流整读，约几 MB），
// 返回体与 Content-Type（boundary 在其中，NewBundleReader 需要）。
func (r *Reporter) FetchBundle(arch string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, r.base+"/api/v1/agent/upgrade-bundle?arch="+url.QueryEscape(arch), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("X-Agent-Token", r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}
