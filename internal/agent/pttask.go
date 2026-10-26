package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"lz-monitor/internal/ptfetch"
)

// PT 中转抓取任务（391 扩展，2026-10-05）：push agent 零暴露中转——server 入队
// （每天 06:00 每日轮 / 手动刷新 / 启动补拉），agent 上报时从响应捎带领取，
// 在本机网络向 PT 站发请求（出口 IP = 家里宽带，与浏览器登录一致防连坐），
// 结果回传 server 解析入库。
//
// 纪律（与 store/pt_task.go 防死循环矩阵对齐）：
//   - 零重试：抓取失败原样回传错误，绝不自行重打（server 端也绝不重派已执行任务）；
//   - Cookie 只过内存：不落日志、不落盘；
//   - 回传失败不补投：server 端租约超时机制兜底回收。

// PtTask 从 report 响应捎带领取的中转抓取任务。
type PtTask struct {
	TaskID     int64  `json:"task_id"`
	SiteID     int64  `json:"site_id"`
	SiteKey    string `json:"site_key"`
	BaseURL    string `json:"base_url"`
	Cookie     string `json:"cookie"`
	UA         string `json:"ua"`
	SigninMode string `json:"signin_mode"` // dian115 类站签到模式（空=仅查询）
}

// RunPtTasks 顺序执行领取到的任务（任务内站间串行已由下发侧 ≤4 个限制），
// 每个任务独立回传，单个失败/内部 panic 都不影响其余。
func RunPtTasks(rep *Reporter, tasks []PtTask) {
	for _, t := range tasks {
		t := t
		snap, err := runTask(&t, ptfetch.FetchSite)
		if err != nil {
			log.Printf("PT 中转任务执行失败（%s/%s）: %v", t.SiteKey, t.BaseURL, err)
			if err := rep.PostTaskResult(t.TaskID, nil, err.Error()); err != nil {
				log.Printf("PT 中转任务回传失败（task %d）: %v（服务端租约超时机制兜底）", t.TaskID, err)
			}
			continue
		}
		if err := rep.PostTaskResult(t.TaskID, snap, ""); err != nil {
			log.Printf("PT 中转任务回传失败（task %d）: %v（服务端租约超时机制兜底）", t.TaskID, err)
		} else {
			log.Printf("PT 中转任务完成（%s，task %d）", t.SiteKey, t.TaskID)
		}
	}
}

// runTask 单任务执行的隔离壳：抓取链路任何未预期 panic（站点返回怪异内容
// 触发解析 bug 等）就地回收转成任务失败回传（服务端记该任务终态），绝不带崩
// agent 进程连累上报主循环与其余任务（2026-10-06 用户要求站点级隔离）。
// fetch 抽参供测试注入。
func runTask(t *PtTask, fetch func(ptfetch.Site) (*ptfetch.Snapshot, error)) (snap *ptfetch.Snapshot, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PT 中转任务内部错误（已隔离）：%s task %d: %v", t.SiteKey, t.TaskID, r)
			err = fmt.Errorf("内部错误: %v", r)
		}
	}()
	return fetch(ptfetch.Site{SiteKey: t.SiteKey, BaseURL: t.BaseURL, Cookie: t.Cookie, UA: t.UA, SigninMode: t.SigninMode})
}

// PostTaskResult 回传任务结果（快照或错误二选一）。非 200 直接放弃——server 端
// 任务已租出，租约超时自然回收，这里重试只会延长占用（结果幂等也在 server 端）。
func (r *Reporter) PostTaskResult(taskID int64, snap interface{}, errMsg string) error {
	body, err := json.Marshal(struct {
		TaskID   int64       `json:"task_id"`
		Snapshot interface{} `json:"snapshot"`
		Error    string      `json:"error"`
	}{TaskID: taskID, Snapshot: snap, Error: errMsg})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, r.base+"/api/v1/agent/task-result", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", r.token)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
