package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	client *http.Client // 上报/任务回传用（短超时）
	bundle *http.Client // 升级分发束下载用（长超时：4MB 在慢链路 VPN 下 10s 必掐线）
}

// NewReporter 创建上报客户端，timeout 为单次上报超时。
func NewReporter(serverURL, token string, timeout time.Duration) *Reporter {
	return &Reporter{
		base:   strings.TrimRight(serverURL, "/"),
		token:  token,
		client: &http.Client{Timeout: timeout},
		bundle: &http.Client{Timeout: 5 * time.Minute},
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

// batchBodyBudget 批量信封的字节预算：服务端单请求上限 1MB（maxReportBody），
// 而 pull 路径单份快照的合法上限同样是 1MB——重 Docker 宿主的单份报告可达
// 数百 KB，攒满 6 份必然超限，超限会被服务端 400 拒收且 agent 无从恢复
// （新样本进来还是大）。超预算丢最旧样本重编，至少保住最新一份（在线判定/
// 告警/服务器信息都只看它）；被丢样本等同断流截断口径。
const batchBodyBudget = 950 << 10

// Post 提交一批上报：reps 按时间升序（≥1 份，末位=最新）。攒样周期内多个
// 采样合并为信封（proto.Report.Samples）一次发出——顶层标量字段始终是最新
// 样本，老服务端按单份最新样本处理，两端升级顺序任意。interval 为采样间隔
// （服务端回填各样本时间戳用，≤0 不携带）。失败返回错误（调用方只记日志、
// 样本保留，下个上报点重试；401 单独提示核对密钥）。响应体捎带服务端下发
// 的任务/指令，返回给调用方处理。
func (r *Reporter) Post(reps []*proto.Report, interval time.Duration) (PostResult, error) {
	var out PostResult
	if len(reps) == 0 {
		return out, errors.New("空上报")
	}
	env := *reps[len(reps)-1]
	for _, rep := range reps {
		env.Samples = append(env.Samples, *rep)
	}
	if sec := int(interval / time.Second); sec >= 1 {
		env.IntervalSec = sec
	}
	body, err := json.Marshal(&env)
	for err == nil && len(body) > batchBodyBudget && len(env.Samples) > 1 {
		env.Samples = env.Samples[1:] // 超字节预算：丢最旧样本重编，保住最新一份
		body, err = json.Marshal(&env)
	}
	if err != nil {
		return out, fmt.Errorf("序列化上报: %w", err)
	}
	if len(env.Samples) <= 1 {
		// 只剩一份（单样本上报/预算裁剪到底）：退化纯单份线格式——顶层本就是
		// 该样本的完整内容，再放 Samples 会凭空翻倍体积，单份 600KB 的合法
		// 报告会因此超服务端 1MB 上限被拒（今天单份 push 能跑的机器不能弄坏）
		env.Samples = nil
		env.IntervalSec = 0
		body, err = json.Marshal(&env)
		if err != nil {
			return out, fmt.Errorf("序列化上报: %w", err)
		}
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
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10)) // 排空少量响应体，连接可回池复用
		return out, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		// 解析失败不影响上报本身（旧版服务端无任务字段），但要留痕——
		// 捎带的任务/升级指令在这里静默蒸发的话只剩租约超时兜底
		log.Printf("上报响应解析失败（本次捎带的任务/升级指令丢弃）: %v", err)
		return PostResult{}, nil
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
	resp, err := r.bundle.Do(req) // 分发束走长超时 client，不吃 10s 上报超时
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
