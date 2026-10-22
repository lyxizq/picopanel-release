package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// 应用操作任务（54 飞牛 fnOS 集成）：server 下发 Docker 容器 / 虚拟机启停
// 指令，push agent 上报捎带领取、本机执行、结果回传。与 net_tasks 同纪律：
// 零重试（失败原样回传，绝不自行重打）、结果只过内存不落盘、回传失败不补投
// （server 端租约超时机制兜底）。
//
// 执行面白名单是安全底线：Docker 走 /var/run/docker.sock 的 Engine API 固定
// 三个动词（start/stop/restart）；虚拟机走 virsh 固定四个动词（start/shutdown/
// reboot/destroy），目标与动词全部由 server 下发、agent 不接受任何自由文本
// 命令——延续「无 shell 任意命令」纪律。

// AppTask 从 report 响应捎带的应用操作任务。
type AppTask struct {
	TaskID int64  `json:"task_id"`
	Kind   string `json:"kind"`   // docker | vm
	Target string `json:"target"` // 容器短 ID/名 或 libvirt 域名
	Action string `json:"action"` // docker: start/stop/restart；vm: start/shutdown/reboot/destroy
}

// dockerVerbs / vmVerbs 动词白名单（agent 侧兜底，server 侧 Create 时同样校验）。
var (
	dockerVerbs = map[string]bool{"start": true, "stop": true, "restart": true}
	vmVerbs     = map[string]bool{"start": true, "shutdown": true, "reboot": true, "destroy": true}
)

// appTaskResult 单任务执行结果。
type appTaskResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// appTaskPoster 结果回传面（*Reporter 实现；收窄成接口便于测试桩捕获回传）。
type appTaskPoster interface {
	PostAppTaskResult(taskID int64, res appTaskResult) error
}

// RunAppTasks 顺序执行领取到的任务，每个任务独立回传，单个失败/panic 不影响
// 其余（隔离壳同 net_tasks 口径）。
func RunAppTasks(rep appTaskPoster, tasks []AppTask) {
	for _, t := range tasks {
		t := t
		res := runAppTask(&t)
		if res.OK {
			log.Printf("应用操作完成（task %d %s %s %s）: %s", t.TaskID, t.Kind, t.Target, t.Action, res.Message)
		} else {
			log.Printf("应用操作失败（task %d %s %s %s）: %s", t.TaskID, t.Kind, t.Target, t.Action, res.Message)
		}
		if err := rep.PostAppTaskResult(t.TaskID, res); err != nil {
			log.Printf("应用操作回传失败（task %d）: %v（服务端租约超时机制兜底）", t.TaskID, err)
		}
	}
}

// runAppTask 单任务执行的隔离壳：任何未预期 panic 就地回收转成任务失败回传，
// 绝不带崩 agent 进程连累上报主循环。
func runAppTask(t *AppTask) (res appTaskResult) {
	defer func() {
		if r := recover(); r != nil {
			res = appTaskResult{Message: fmt.Sprintf("内部异常: %v", r)}
		}
	}()
	return doAppTask(t)
}

// doAppTask 按类型分发执行。
func doAppTask(t *AppTask) appTaskResult {
	switch t.Kind {
	case "docker":
		if !dockerVerbs[t.Action] {
			return appTaskResult{Message: "不支持的操作: " + t.Action}
		}
		return dockerAction(t.Target, t.Action)
	case "vm":
		if !vmVerbs[t.Action] {
			return appTaskResult{Message: "不支持的操作: " + t.Action}
		}
		return virshAction(t.Target, t.Action)
	default:
		return appTaskResult{Message: "未知任务类型: " + t.Kind}
	}
}

// dockerSockPath Docker Engine socket（包级变量，测试注入临时 socket 用）。
var dockerSockPath = "/var/run/docker.sock"

// dockerAction 经 /var/run/docker.sock 调 Engine API（同步返回：start/stop
// 完成才应答）。stop/restart 附 t=10 给 10s 优雅停机宽限；304=已在目标状态
// 也视为成功（幂等重放无害）。
func dockerAction(target, action string) appTaskResult {
	client := &http.Client{
		Timeout: 35 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.DialTimeout("unix", dockerSockPath, 3*time.Second)
			},
		},
	}
	url := fmt.Sprintf("http://docker/v1.24/containers/%s/%s", target, action)
	if action == "stop" || action == "restart" {
		url += "?t=10"
	}
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return appTaskResult{Message: "构造请求失败: " + err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return appTaskResult{Message: "Docker 不可达（socket 权限/daemon 未运行）: " + err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	switch {
	case resp.StatusCode == http.StatusNoContent:
		return appTaskResult{OK: true, Message: "操作成功"}
	case resp.StatusCode == http.StatusNotModified:
		return appTaskResult{OK: true, Message: "已在目标状态"}
	case resp.StatusCode == http.StatusNotFound:
		return appTaskResult{Message: "容器不存在: " + target}
	default:
		return appTaskResult{Message: fmt.Sprintf("Docker 返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}
}

// virshAction 执行 virsh 固定动词（exec 直调、无 shell，参数只有域名）。
// virsh start 同步等待域拉起，shutdown/reboot/destroy 只下达信号即返回
// （状态变化由下一轮采集自然反映）。
func virshAction(target, action string) appTaskResult {
	path, err := exec.LookPath("virsh")
	if err != nil {
		return appTaskResult{Message: "本机未安装 virsh"}
	}
	out, err := exec.Command(path, action, target).CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		if msg == "" {
			msg = err.Error()
		}
		return appTaskResult{Message: fmt.Sprintf("virsh %s 失败: %s", action, msg)}
	}
	if msg == "" {
		msg = "操作成功"
	}
	return appTaskResult{OK: true, Message: msg}
}
