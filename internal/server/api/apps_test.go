package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
)

// 应用负载链路（54）回归：push 上报携带容器/虚拟机快照 → 明细 latest 透出 →
// 操作入队（仅 push 模式、白名单动词、活跃唯一 409）→ agent 上报捎带领取 →
// 结果回传收尾 → 状态轮询。

func newAppsRouter(t *testing.T) (*gin.Engine, *store.Store, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "apps.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	// fnos（id=1）push 模式 + 上报密钥；pull-machine（id=2）拉取模式
	if _, err := s.CreateServer("fnos", "", "push-token-54", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServer("pull-machine", "10.0.0.9:19100", "pull-token", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE servers SET collect_mode = 'push' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	authH := &AuthHandler{st: s, guard: newLoginGuard(ipFails), userGuard: newLoginGuard(userFails)}
	r.POST("/api/v1/auth/login", authH.Login)
	w := loginWith(r, "admin", "admin123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("管理员登录应 200: %d %s", w.Code, w.Body.String())
	}
	adminToken := bodyField(t, w, "token")
	agentH := &AgentReportHandler{st: s, eng: engine.New(s, engine.Config{})}
	srvH := &ServerHandler{st: s, offlineAfter: 60}
	r.POST("/api/v1/agent/report", agentH.Report)
	r.POST("/api/v1/agent/app-task-result", agentH.AppTaskResult)
	grp := r.Group("/api/v1", authRequired(s), adminRequired())
	grp.GET("/servers/:id", srvH.Get)
	grp.POST("/servers/:id/apps/action", srvH.CreateAppAction)
	grp.GET("/apps-task/:id", srvH.AppTaskState)
	return r, s, adminToken
}

// postAgent agent 侧请求（X-Agent-Token 鉴权，与 postJSON 同壳）。
func postAgent(r *gin.Engine, path, body, agentToken string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", agentToken)
	r.ServeHTTP(w, req)
	return w
}

func taskIDOf(t *testing.T, body string) int64 {
	t.Helper()
	var created struct {
		TaskID int64 `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.TaskID <= 0 {
		t.Fatalf("应返回 task_id: %s (%v)", body, err)
	}
	return created.TaskID
}

// TestAppsFullChain 上报 → 快照 → 下发 → 领取 → 回传 → 轮询全链路。
func TestAppsFullChain(t *testing.T) {
	r, _, adminToken := newAppsRouter(t)

	// push 上报带容器/虚拟机快照
	rep := proto.Report{CPU: 11, Containers: []proto.ContainerInfo{
		{ID: "abc123def456", Name: "web", Image: "nginx:latest", State: "running", Status: "Up 3 days"},
	}, VMs: []proto.VMInfo{
		{ID: "3", Name: "lvpmgjxz", State: "running"},
		{Name: "hy6dek9c", State: "shut off"},
	}}
	body, _ := json.Marshal(rep)
	if w := postAgent(r, "/api/v1/agent/report", string(body), "push-token-54"); w.Code != http.StatusOK {
		t.Fatalf("上报应 200: %d %s", w.Code, w.Body.String())
	}

	// 明细 latest 透出快照
	w := getJSON(r, "/api/v1/servers/1", adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("明细应 200: %d %s", w.Code, w.Body.String())
	}
	var view struct {
		Server struct {
			CollectMode string `json:"collect_mode"`
			Latest      struct {
				Containers []proto.ContainerInfo `json:"containers"`
				VMs        []proto.VMInfo        `json:"vms"`
			} `json:"latest"`
		} `json:"server"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Server.CollectMode != "push" {
		t.Fatalf("collect_mode 应为 push: %s", view.Server.CollectMode)
	}
	if len(view.Server.Latest.Containers) != 1 || view.Server.Latest.Containers[0].Name != "web" {
		t.Fatalf("latest 应带容器: %+v", view.Server.Latest.Containers)
	}
	if len(view.Server.Latest.VMs) != 2 || view.Server.Latest.VMs[1].State != "shut off" {
		t.Fatalf("latest 应带虚拟机: %+v", view.Server.Latest.VMs)
	}

	// 下发：非法动词 400；VM 关机入队 200；活跃唯一 409；非 push 机器 400
	w = postJSON(r, "/api/v1/servers/1/apps/action", `{"kind":"vm","target":"lvpmgjxz","action":"format"}`, adminToken)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法动词应 400: %d %s", w.Code, w.Body.String())
	}
	w = postJSON(r, "/api/v1/servers/1/apps/action", `{"kind":"vm","target":"lvpmgjxz","action":"shutdown"}`, adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("入队应 200: %d %s", w.Code, w.Body.String())
	}
	taskID := taskIDOf(t, w.Body.String())
	w = postJSON(r, "/api/v1/servers/1/apps/action", `{"kind":"vm","target":"lvpmgjxz","action":"start"}`, adminToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("同目标重复入队应 409: %d %s", w.Code, w.Body.String())
	}
	w = postJSON(r, "/api/v1/servers/2/apps/action", `{"kind":"docker","target":"web","action":"start"}`, adminToken)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "push") {
		t.Fatalf("pull 机器应 400 提示 push: %d %s", w.Code, w.Body.String())
	}

	// agent 上报捎带领取任务
	w = postAgent(r, "/api/v1/agent/report", `{"cpu":1}`, "push-token-54")
	if w.Code != http.StatusOK {
		t.Fatalf("领取上报应 200: %d %s", w.Code, w.Body.String())
	}
	var down struct {
		AppTasks []struct {
			TaskID int64  `json:"task_id"`
			Kind   string `json:"kind"`
			Target string `json:"target"`
			Action string `json:"action"`
		} `json:"app_tasks"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &down)
	if len(down.AppTasks) != 1 || down.AppTasks[0].TaskID != taskID ||
		down.AppTasks[0].Kind != "vm" || down.AppTasks[0].Action != "shutdown" {
		t.Fatalf("上报响应应捎带 1 个任务: %s", w.Body.String())
	}

	// 轮询执行中态
	w = getJSON(r, "/api/v1/apps-task/"+strconv.FormatInt(taskID, 10), adminToken)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"leased"`) {
		t.Fatalf("租出态应可轮询: %d %s", w.Code, w.Body.String())
	}

	// 结果回传 + 幂等重复回传
	w = postAgent(r, "/api/v1/agent/app-task-result",
		`{"task_id":`+strconv.FormatInt(taskID, 10)+`,"ok":true,"message":"已下达关机信号"}`, "push-token-54")
	if w.Code != http.StatusOK {
		t.Fatalf("回传应 200: %d %s", w.Code, w.Body.String())
	}
	w = postAgent(r, "/api/v1/agent/app-task-result",
		`{"task_id":`+strconv.FormatInt(taskID, 10)+`,"ok":false,"message":"dup"}`, "push-token-54")
	if w.Code != http.StatusOK {
		t.Fatalf("重复回传应幂等 200: %d %s", w.Code, w.Body.String())
	}

	// 终态轮询
	w = getJSON(r, "/api/v1/apps-task/"+strconv.FormatInt(taskID, 10), adminToken)
	var st struct {
		State   string `json:"state"`
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if st.State != "done" || !st.OK || st.Message != "已下达关机信号" {
		t.Fatalf("终态不符: %+v (%s)", st, w.Body.String())
	}

	// 错误回传 → failed 终态带原因
	w = postJSON(r, "/api/v1/servers/1/apps/action", `{"kind":"docker","target":"web","action":"stop"}`, adminToken)
	failedID := taskIDOf(t, w.Body.String())
	w = postAgent(r, "/api/v1/agent/report", `{"cpu":1}`, "push-token-54")
	w = postAgent(r, "/api/v1/agent/app-task-result",
		`{"task_id":`+strconv.FormatInt(failedID, 10)+`,"ok":false,"message":"容器不存在"}`, "push-token-54")
	if w.Code != http.StatusOK {
		t.Fatalf("失败回传应 200: %d %s", w.Code, w.Body.String())
	}
	w = getJSON(r, "/api/v1/apps-task/"+strconv.FormatInt(failedID, 10), adminToken)
	// 执行失败与执行成功同为 done 终态（ok 区分成败，对齐 net_tasks 恒 done 口径）
	if !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), "容器不存在") {
		t.Fatalf("失败回传应 done+ok=false 带原因: %s", w.Body.String())
	}
}

// TestAppsSnapshotCleanupOnLegacyReport 旧版 agent（无容器字段）上报后旧快照清除。
func TestAppsSnapshotCleanupOnLegacyReport(t *testing.T) {
	r, s, _ := newAppsRouter(t)
	body, _ := json.Marshal(proto.Report{CPU: 1, Containers: []proto.ContainerInfo{{ID: "x", Name: "web", State: "running"}}})
	if w := postAgent(r, "/api/v1/agent/report", string(body), "push-token-54"); w.Code != http.StatusOK {
		t.Fatalf("上报应 200: %d", w.Code)
	}
	if m, _ := s.LatestMetric(1); m == nil || len(m.Containers) != 1 {
		t.Fatalf("应有容器快照")
	}
	if w := postAgent(r, "/api/v1/agent/report", `{"cpu":2}`, "push-token-54"); w.Code != http.StatusOK {
		t.Fatalf("旧版上报应 200: %d", w.Code)
	}
	m, _ := s.LatestMetric(1)
	if m == nil || m.Containers != nil || m.VMs != nil {
		t.Fatalf("旧版上报应清掉旧快照: %+v", m)
	}
}
