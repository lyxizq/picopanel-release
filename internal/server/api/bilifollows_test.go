package api

import (
	"encoding/json"
	"strconv"
	"testing"
)

// agentSupportsBili 版本守卫（旧版 agent 收到追更任务会静默丢弃——v1.0.28 起
// 下发前守卫，等升级后自动继续）。
func TestAgentSupportsBili(t *testing.T) {
	cases := map[string]bool{
		"v1.0.28":         true,
		"v1.0.29":         true,
		"v1.1.0":          true,
		"v2.0.0":          true,
		"v1.0.28-3-gabc":  true, // describe 脏串取前缀
		"v1.0.27":         false,
		"v1.0.9":          false,
		"":                false, // 旧版无版本上报
		" v1.0.28 ":       true,
	}
	for ver, want := range cases {
		if got := agentSupportsBili(ver); got != want {
			t.Fatalf("agentSupportsBili(%q)=%v want %v", ver, got, want)
		}
	}
}

// TestBiliTaskDispatchOnReport 下发链路回归（现场翻车修复）：任务入队后必须在
// agent 上报响应里捎带下发——曾漏挂 resp["bili_tasks"]，任务永久 pending、
// agent 端空等「一直解析中」。
func TestBiliTaskDispatchOnReport(t *testing.T) {
	r, token, _, s := newPtRouter(t)

	// push 执行机器（身份=token）
	if _, err := s.CreatePushServer("bili-agent", "agent-token-bili", 0); err != nil {
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

	// 创建追更（地址带链接形态跳过搜索；probe 任务应立即入队）
	res := ptReq(t, r, token, "POST", "/api/v1/bili-follows",
		`{"up_name":"某UP","address":"https://space.bilibili.com/123/lists/456","agent_server_id":`+strconv.FormatInt(agentID, 10)+`,"save_dir":"/data/bili"}`, 200)
	var created struct {
		Follow struct {
			ID int64 `json:"id"`
		} `json:"follow"`
		TaskID int64 `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(res), &created); err != nil || created.TaskID <= 0 {
		t.Fatalf("创建应回 task_id: %s", res)
	}

	// agent 上报（带版本串，过版本守卫）：响应必须捎带该任务（回归点）
	reportRes := ptAgentReq(t, r, "agent-token-bili", "POST", "/api/v1/agent/report", `{"sys":{"hostname":"debian","agent_version":"v1.0.29"}}`)
	var payload struct {
		BiliTasks []struct {
			TaskID int64  `json:"task_id"`
			Mode   string `json:"mode"`
		} `json:"bili_tasks"`
	}
	if err := json.Unmarshal([]byte(reportRes), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.BiliTasks) != 1 || payload.BiliTasks[0].TaskID != created.TaskID || payload.BiliTasks[0].Mode != "probe" {
		t.Fatalf("上报响应应捎带 probe 任务: %s", reportRes)
	}
	// 任务已租出
	tk, err := s.GetBiliTaskState(created.TaskID)
	if err != nil || tk.State != "leased" {
		t.Fatalf("下发后任务应 leased: %+v %v", tk, err)
	}
}
