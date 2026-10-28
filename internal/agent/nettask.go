package agent

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 通用中转任务（548）：server 出口到不了的外网请求（TMDB 等）经 push agent
// 在家庭网络代为执行——391 PT 中转抓取的泛化版。与 PT 同纪律：零重试（失败
// 原样回传错误，绝不自行重打）、结果只过内存不落盘、回传失败不补投（server
// 端租约超时机制兜底）。
//
// host 白名单是安全底线：中转通道本质是「服务端指挥 agent 发请求」，白名单
// 把爆炸半径封死在已知目标，防服务器被拿后把家里 agent 当开放代理用。需要
// 新目标时在这里追加（与 server 一起发版的内建清单）。
//
// AllowAnyHost 任务（566，仅 PT 进度/资源类 purpose 由 server 设置）：跳过
// 白名单——server 指定任意站点抓取与 391 PT 中转任务同水位（391 本就无白名
// 单），但强制 per-host 限速（同站点请求间隔 netHostRateLimit，用户指定 3s，
// 防连坐）。
//
// mihomo 类任务（TODO 7，purpose 前缀 mihomo）：不走 AllowAnyHost（会吃 3s
// 限速卡轮询），走 purpose 级放行——目标限私网/回环 IP 字面量（external-
// controller 是用户内网自建服务，同机 127.0.0.1 或 LAN IP），公网/域名一律不
// 放行，爆炸半径仍封死在内网。
var netTaskAllowedHosts = map[string]bool{
	"api.themoviedb.org": true,
	"api.tmdb.org":       true,
}

// netTaskHostAllowed 白名单判定：内建清单 → mihomo 类 purpose 的私网/回环放行。
func netTaskHostAllowed(purpose, host string) bool {
	if netTaskAllowedHosts[host] {
		return true
	}
	if strings.HasPrefix(purpose, "mihomo") {
		ip := net.ParseIP(host)
		return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
	}
	return false
}

// netHostRateLimit AllowAnyHost 任务的同 host 最小请求间隔（566 用户指定 3s）。
const netHostRateLimit = 3 * time.Second

// netHostLastAt per-host 上次请求时刻（仅 AllowAnyHost 任务记账；进程内即可，
// agent 单实例）。
var (
	netHostMu     sync.Mutex
	netHostLastAt = map[string]time.Time{}
)

// NetTask 从 report 响应捎带的通用中转请求。
type NetTask struct {
	TaskID       int64             `json:"task_id"`
	Purpose      string            `json:"purpose"` // 用途（mihomo 类 purpose 级内网放行判定用）
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	Headers      map[string]string `json:"headers"`
	Body         string            `json:"body"`
	TimeoutSec   int               `json:"timeout_sec"`
	AllowAnyHost bool              `json:"allow_any_host"` // 跳过白名单（server 仅对 PT 类 purpose 设置），仍限速
}

// netTaskResult 单任务执行结果：状态码 + 响应体（UTF-8 文本）或错误。
type netTaskResult struct {
	StatusCode int    `json:"status_code"`
	Body       string `json:"body"`
	Error      string `json:"error"`
}

// RunNetTasks 顺序执行领取到的任务，每个任务独立回传，单个失败/panic 不影响
// 其余（隔离壳同 PT 口径）。
func RunNetTasks(rep *Reporter, tasks []NetTask) {
	for _, t := range tasks {
		t := t
		res := runNetTask(&t)
		if res.Error != "" {
			log.Printf("中转任务执行失败（task %d %s）: %v", t.TaskID, t.host(), res.Error)
		} else {
			log.Printf("中转任务完成（task %d，HTTP %d）", t.TaskID, res.StatusCode)
		}
		if err := rep.PostNetTaskResult(t.TaskID, res); err != nil {
			log.Printf("中转任务回传失败（task %d）: %v（服务端租约超时机制兜底）", t.TaskID, err)
		}
	}
}

// runNetTask 单任务执行的隔离壳：任何未预期 panic 就地回收转成任务失败回传，
// 绝不带崩 agent 进程连累上报主循环。
func runNetTask(t *NetTask) (res netTaskResult) {
	defer func() {
		if r := recover(); r != nil {
			res = netTaskResult{Error: fmt.Sprintf("内部异常: %v", r)}
		}
	}()
	return doNetTask(t)
}

// gateHost AllowAnyHost 任务的 per-host 限速：与该 host 上次请求间隔不足
// netHostRateLimit 时睡足差值（同一 PT 站接口调用间隔 3s，566 用户指定）。
func gateHost(host string) {
	netHostMu.Lock()
	last := netHostLastAt[host]
	wait := netHostRateLimit - time.Since(last)
	if wait > 0 {
		netHostLastAt[host] = last.Add(netHostRateLimit)
		netHostMu.Unlock()
		time.Sleep(wait)
		return
	}
	netHostLastAt[host] = time.Now()
	netHostMu.Unlock()
}

// doNetTask 执行一次中转请求：host 白名单（内建清单 + mihomo 类 purpose 私网
// 放行；AllowAnyHost 跳过）→ 限速 → 限时 HTTP → 体上限截断。
func doNetTask(t *NetTask) netTaskResult {
	host := t.host()
	if !t.AllowAnyHost && !netTaskHostAllowed(t.Purpose, host) {
		return netTaskResult{Error: "目标 host 不在白名单: " + host}
	}
	if t.AllowAnyHost {
		gateHost(host)
	}
	timeout := time.Duration(t.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	method := t.Method
	if method == "" {
		method = http.MethodGet
	}
	var reader io.Reader
	if t.Body != "" {
		reader = strings.NewReader(t.Body)
	}
	req, err := http.NewRequest(method, t.URL, reader)
	if err != nil {
		return netTaskResult{Error: "构造请求失败: " + err.Error()}
	}
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	// 重定向逐跳复查：白名单模式下重定向目标 host 必须仍在白名单内——否则
	// 白名单域上的开放跳转（302）可把请求绕出到任意目标
	cl := &http.Client{Timeout: timeout}
	if !t.AllowAnyHost {
		cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			h := req.URL.Hostname()
			if !netTaskHostAllowed(t.Purpose, h) {
				return fmt.Errorf("重定向目标不在白名单: %s", h)
			}
			return nil
		}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return netTaskResult{Error: err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNetTaskBody))
	if err != nil {
		return netTaskResult{Error: "读取响应失败: " + err.Error()}
	}
	return netTaskResult{StatusCode: resp.StatusCode, Body: string(body)}
}

// host 取目标主机名（白名单判定用；解析失败返回原串必不命中白名单）。
func (t *NetTask) host() string {
	u, err := url.Parse(t.URL)
	if err != nil {
		return t.URL
	}
	return u.Hostname()
}

// maxNetTaskBody 响应体上限：白名单内目标均为 JSON API，几十 KB 量级，4MB 兜底。
const maxNetTaskBody = 4 << 20
