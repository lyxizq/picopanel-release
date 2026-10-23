package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestVideoDlDispatchOnReport 下发链路回归（TODO 4；B 站追更翻车教训同款）：
// 任务入队后必须在 agent 上报响应里捎带下发——漏挂 resp["video_dl_tasks"] 会
// 任务永久 pending、agent 空等。另覆盖版本守卫（旧版不下发）与归属校验。
func TestVideoDlDispatchOnReport(t *testing.T) {
	r, token, _, s := newPtRouter(t)

	if _, err := s.CreatePushServer("vdl-agent", "agent-token-vdl", 0); err != nil {
		t.Fatal(err)
	}
	agentID := int64(0)
	if list, err := s.ListServers(); err == nil {
		for _, srv := range list {
			if srv.CollectMode == "push" {
				agentID = srv.ID
			}
		}
	}
	if agentID == 0 {
		t.Fatal("push 机器未建成")
	}

	// 创建任务（agent 流派）
	res := ptReq(t, r, token, "POST", "/api/v1/video-dls",
		`{"url":"https://www.bilibili.com/video/BV1test","save_dir":"/data/v","executor":"agent","server_id":`+strconv.FormatInt(agentID, 10)+`}`, 200)
	var created struct {
		TaskID int64 `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(res), &created); err != nil || created.TaskID <= 0 {
		t.Fatalf("创建应回 task_id: %s", res)
	}

	// 旧版 agent（v1.0.40 无执行器）上报：不下发（防旧版静默吞任务）
	reportRes := ptAgentReq(t, r, "agent-token-vdl", "POST", "/api/v1/agent/report", `{"sys":{"hostname":"debian","agent_version":"v1.0.40"}}`)
	if json.Valid([]byte(reportRes)) && hasField(reportRes, "video_dl_tasks") {
		t.Fatalf("旧版 agent 不应领到任务: %s", reportRes)
	}
	if row, _ := s.GetVideoDlTaskState(created.TaskID); row.State != "pending" {
		t.Fatalf("旧版守卫应保持 pending: %+v", row)
	}

	// 新版 agent 上报：响应必须捎带该任务（回归点）且任务转 leased
	reportRes = ptAgentReq(t, r, "agent-token-vdl", "POST", "/api/v1/agent/report", `{"sys":{"hostname":"debian","agent_version":"v1.0.41"}}`)
	var payload struct {
		VideoDlTasks []struct {
			TaskID  int64  `json:"task_id"`
			URL     string `json:"url"`
			SaveDir string `json:"save_dir"`
		} `json:"video_dl_tasks"`
	}
	if err := json.Unmarshal([]byte(reportRes), &payload); err != nil {
		t.Fatalf("上报响应解析: %v %s", err, reportRes)
	}
	if len(payload.VideoDlTasks) != 1 || payload.VideoDlTasks[0].TaskID != created.TaskID ||
		payload.VideoDlTasks[0].URL != "https://www.bilibili.com/video/BV1test" || payload.VideoDlTasks[0].SaveDir != "/data/v" {
		t.Fatalf("上报响应未捎带任务或载荷不符: %s", reportRes)
	}
	if row, _ := s.GetVideoDlTaskState(created.TaskID); row.State != "leased" {
		t.Fatalf("领取后应转 leased: %+v", row)
	}

	// 进度心跳：归属机器可写，他机 403
	ptAgentReq(t, r, "agent-token-vdl", "POST", "/api/v1/agent/video-dl-progress", `{"task_id":`+ptItoa(created.TaskID)+`,"percent":33.5,"speed":"2345678.0","title":"测试视频"}`, 200)
	if row, _ := s.GetVideoDlTaskState(created.TaskID); row.Progress != 33 || row.Title != "测试视频" || row.Speed != "2.2MiB/s" {
		t.Fatalf("进度回写不符: %+v", row)
	}
	if _, err := s.CreatePushServer("other", "agent-token-other", 0); err != nil {
		t.Fatal(err)
	}
	ptAgentReq(t, r, "agent-token-other", "POST", "/api/v1/agent/video-dl-progress", `{"task_id":`+ptItoa(created.TaskID)+`,"percent":90}`, 403)

	// 停止链路：置标记 → 该机上报响应捎带停止命令（回归点）
	stopRes := ptReq(t, r, token, "POST", "/api/v1/video-dls/"+ptItoa(created.TaskID)+"/stop", `{}`, 200)
	if !hasField(stopRes, `"requested":true`) {
		t.Fatalf("leased 停止应回 requested: %s", stopRes)
	}
	reportRes = ptAgentReq(t, r, "agent-token-vdl", "POST", "/api/v1/agent/report", `{"sys":{"hostname":"debian","agent_version":"v1.0.41"}}`)
	if !hasField(reportRes, "video_dl_stops") {
		t.Fatalf("上报响应未捎带停止命令: %s", reportRes)
	}

	// 结果回传：他机 403；本机回传失败终态 → 列表可见错误
	ptAgentReq(t, r, "agent-token-other", "POST", "/api/v1/agent/video-dl-result", `{"task_id":`+ptItoa(created.TaskID)+`,"result":{"ok":false,"error":"手动停止"}}`, 403)
	ptAgentReq(t, r, "agent-token-vdl", "POST", "/api/v1/agent/video-dl-result", `{"task_id":`+ptItoa(created.TaskID)+`,"result":{"ok":false,"error":"已手动停止"}}`, 200)
	if row, _ := s.GetVideoDlTaskState(created.TaskID); row.State != "failed" || row.Error != "已手动停止" {
		t.Fatalf("终态不符: %+v", row)
	}
	// Finish 清停止标记后，响应不再捎带停止命令
	reportRes = ptAgentReq(t, r, "agent-token-vdl", "POST", "/api/v1/agent/report", `{"sys":{"hostname":"debian","agent_version":"v1.0.41"}}`)
	if hasField(reportRes, "video_dl_stops") {
		t.Fatalf("停止命令应已被消费: %s", reportRes)
	}

	// 列表带机器名；server 流派任务由本机执行器领取（这里只验入队校验拒绝非法执行器）
	ptReq(t, r, token, "POST", "/api/v1/video-dls", `{"url":"ftp://bad","save_dir":"/x","executor":"server"}`, 400)
	ptReq(t, r, token, "POST", "/api/v1/video-dls", `{"url":"https://ok.example/v","save_dir":"/x","executor":"pull"}`, 400)
	listRes := ptReq(t, r, token, "GET", "/api/v1/video-dls", "", 200)
	if !hasField(listRes, "server_ytdlp") || !hasField(listRes, `"server_name":"vdl-agent"`) {
		t.Fatalf("列表缺 server_ytdlp/机器名: %s", listRes)
	}
}

// hasField 裸字符串子串判断（避免为断言逐层建结构体）。
func hasField(s, sub string) bool {
	return strings.Contains(s, sub)
}

// TestVideoDlCreateAndDelete 创建校验与删除：同 URL 活跃 409、终态删除。
func TestVideoDlCreateAndDelete(t *testing.T) {
	r, token, _, s := newPtRouter(t)
	if _, err := s.CreatePushServer("vdl2", "agent-token-vdl2", 0); err != nil {
		t.Fatal(err)
	}
	agentID := int64(0)
	if list, err := s.ListServers(); err == nil {
		for _, srv := range list {
			if srv.CollectMode == "push" {
				agentID = srv.ID
			}
		}
	}
	res := ptReq(t, r, token, "POST", "/api/v1/video-dls",
		`{"url":"https://v.douyin.com/x1","save_dir":"/data/d","executor":"agent","server_id":`+strconv.FormatInt(agentID, 10)+`}`, 200)
	var created struct {
		TaskID int64 `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(res), &created); err != nil || created.TaskID <= 0 {
		t.Fatalf("创建失败: %s", res)
	}
	// 同 URL 活跃 409
	ptReq(t, r, token, "POST", "/api/v1/video-dls",
		`{"url":"https://v.douyin.com/x1","save_dir":"/data/d","executor":"agent","server_id":`+strconv.FormatInt(agentID, 10)+`}`, 409)
	// pull 模式机器拒绝
	if list, err := s.ListServers(); err == nil {
		for _, srv := range list {
			if srv.CollectMode != "push" {
				ptReq(t, r, token, "POST", "/api/v1/video-dls",
					`{"url":"https://v.douyin.com/x2","save_dir":"/data/d","executor":"agent","server_id":`+ptItoa(srv.ID)+`}`, 400)
			}
		}
	}
	// pending 停止=作废 → 可删
	ptReq(t, r, token, "POST", "/api/v1/video-dls/"+ptItoa(created.TaskID)+"/stop", `{}`, 200)
	ptReq(t, r, token, "DELETE", "/api/v1/video-dls/"+ptItoa(created.TaskID), "", 200)
}
