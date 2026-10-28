package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
)

// TestInsertGaps 单元：缺口两端插 0 值点；点距不足阈值或点太少时原样返回。
// 时间戳与生产路径同为 int64（毫秒）。
func TestInsertGaps(t *testing.T) {
	cases := []struct {
		name   string
		points [][2]any
		gapMs  int64
		want   [][2]any
	}{
		{
			name:   "单点不处理",
			points: [][2]any{{int64(1000), 1.0}},
			gapMs:  1500,
			want:   [][2]any{{int64(1000), 1.0}},
		},
		{
			name:   "相邻点无缺口原样返回",
			points: [][2]any{{int64(1000), 1.0}, {int64(2000), 2.0}},
			gapMs:  1500,
			want:   [][2]any{{int64(1000), 1.0}, {int64(2000), 2.0}},
		},
		{
			name:   "缺口两端插零",
			points: [][2]any{{int64(1000), 1.0}, {int64(60000), 2.0}},
			gapMs:  1500,
			want:   [][2]any{{int64(1000), 1.0}, {int64(1001), 0}, {int64(59999), 0}, {int64(60000), 2.0}},
		},
		{
			name:   "恰好等于阈值也插",
			points: [][2]any{{int64(1000), 1.0}, {int64(2500), 2.0}},
			gapMs:  1500,
			want:   [][2]any{{int64(1000), 1.0}, {int64(1001), 0}, {int64(2499), 0}, {int64(2500), 2.0}},
		},
		{
			name:   "多点多个缺口各自插零",
			points: [][2]any{{int64(1000), 1.0}, {int64(2000), 2.0}, {int64(90000), 3.0}},
			gapMs:  1500,
			want:   [][2]any{{int64(1000), 1.0}, {int64(2000), 2.0}, {int64(2001), 0}, {int64(89999), 0}, {int64(90000), 3.0}},
		},
	}
	for _, c := range cases {
		got := insertGaps(c.points, c.gapMs)
		if len(got) != len(c.want) {
			t.Fatalf("%s: 点数 %d, want %d", c.name, len(got), len(c.want))
		}
		for i := range got {
			if got[i][0] != c.want[i][0] || got[i][1] != c.want[i][1] {
				t.Fatalf("%s: 点 %d = %v, want %v", c.name, i, got[i], c.want[i])
			}
		}
	}
	if insertGaps(nil, 100) != nil {
		t.Fatal("nil 入参应原样返回")
	}
}

// TestHistoryGapZero 端到端：两段数据之间缺了多个桶，CPU 序列在缺口
// 两端出现 0 值点——折线归零走平线，而不是斜线跨越离线时段。
func TestHistoryGapZero(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "gap.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	srv, err := s.CreateServer("gap-01", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	// 两条样本间隔 300s（step=60 → 缺 4 个桶），中间无数据
	for i, cpu := range []float64{12.5, 30} {
		if err := s.InsertMetric(srv.ID, now-int64(300*(1-i)), &proto.Report{CPU: cpu}); err != nil {
			t.Fatal(err)
		}
	}
	series, err := historySeries(s, srv.ID, now-3600, now, 60, false)
	if err != nil {
		t.Fatal(err)
	}
	var pts [][2]any
	for _, sr := range series {
		if sr.Key == "cpu" {
			pts = sr.Points
		}
	}
	if len(pts) != 4 {
		t.Fatalf("cpu 序列应为 2 数据点 + 2 归零点, got %d: %v", len(pts), pts)
	}
	// 中间两点必须为 0，且时间戳落在缺口内（首点之后、末点之前）
	if pts[1][1] != 0 || pts[2][1] != 0 {
		t.Fatalf("缺口两端应为 0 值点: %v", pts)
	}
	if pts[1][0].(int64) <= pts[0][0].(int64) || pts[2][0].(int64) >= pts[3][0].(int64) {
		t.Fatalf("归零点应落在缺口内: %v", pts)
	}
}

// newServersRouter 挂服务器管理端点的测试路由（鉴权中间件不在此层，直测 handler）。
// term 置 nil：dropTermConns 的 nil 分支即是生产外唯一调用形态之外的安全兜底。
func newServersRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "srv.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	h := &ServerHandler{st: s, offlineAfter: 60}
	r := gin.New()
	r.GET("/servers", h.List)
	r.GET("/servers/:id", h.Get)
	r.PUT("/servers/:id", h.Update)
	r.DELETE("/servers/:id", h.Delete)
	return r, s
}

func putServer(t *testing.T, r *gin.Engine, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/servers/"+strconv.FormatInt(id, 10), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// TestServersMasked 打码回归：列表/详情响应不含 token / ssh_password / 主机指纹
// 明文键与明文值，改以 *_set 布尔告知；term_available 新口径（push 无专用地址
// 才不可用）。store.Server 新增敏感字段若漏进 view，这里的明文值断言会拦下。
func TestServersMasked(t *testing.T) {
	r, s := newServersRouter(t)
	if _, err := s.CreateServer("agent-1", "10.0.0.1:19100", "shared-tok", 0); err != nil {
		t.Fatal(err)
	}
	newSSHServer(t, s, "ssh-1", "10.0.0.2:22", "root", "", "ssh-pw-明文", 0)
	pushSrv, err := s.CreatePushServer("push-1", "push-tok-一对一", 0)
	if err != nil {
		t.Fatal(err)
	}
	// push + 终端专用地址：终端可用
	v := "10.9.9.9:2222"
	if err := s.UpdateServer(pushSrv.ID, store.ServerUpdate{SSHAddr: &v}); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{"/servers", "/servers/1"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: 状态码 %d: %s", target, w.Code, w.Body.String())
		}
		body := w.Body.String()
		for _, leak := range []string{"shared-tok", "push-tok", "ssh-pw", `"token"`, `"ssh_password"`, `"ssh_host_key"`} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s: 响应泄漏敏感值 %s: %s", target, leak, body)
			}
		}
	}

	// 列表逐台断言 set 标志与 term_available 口径
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/servers", nil))
	var out struct {
		Servers []struct {
			Name           string `json:"name"`
			TokenSet       bool   `json:"token_set"`
			SSHPasswordSet bool   `json:"ssh_password_set"`
			TermAvailable  bool   `json:"term_available"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	want := map[string][3]bool{
		"agent-1": {true, false, true}, // agent：token_set；非 push 终端可用
		"ssh-1":   {false, true, true}, // ssh：密码 set；终端可用
		"push-1":  {true, false, true}, // push+ssh_addr：token_set；专用地址可用
	}
	if len(out.Servers) != len(want) {
		t.Fatalf("服务器数 %d: %s", len(out.Servers), w.Body.String())
	}
	for _, sv := range out.Servers {
		w2, ok := want[sv.Name]
		if !ok {
			t.Fatalf("意外的服务器 %s", sv.Name)
		}
		if sv.TokenSet != w2[0] || sv.SSHPasswordSet != w2[1] || sv.TermAvailable != w2[2] {
			t.Fatalf("%s: set/available = %v %v %v, want %v", sv.Name, sv.TokenSet, sv.SSHPasswordSet, sv.TermAvailable, w2)
		}
	}

	// push 无专用地址：383 起也进终端/文件列表（term_available=true，
	// 首连时凭据弹窗补填 SSH 地址），不再按模式排除
	push2, err := s.CreatePushServer("push-2", "tok2", 0)
	if err != nil {
		t.Fatal(err)
	}
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, httptest.NewRequest(http.MethodGet, "/servers/"+strconv.FormatInt(push2.ID, 10), nil))
	var one struct {
		Server struct {
			Name          string `json:"name"`
			TermAvailable bool   `json:"term_available"`
		} `json:"server"`
	}
	if err := json.Unmarshal(w3.Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if !one.Server.TermAvailable {
		t.Fatalf("push 无专用地址也应 term_available=true（383）: %s", w3.Body.String())
	}
}

// TestUpdateCredSemantics 打码配套 PUT 语义：密钥/密码非空=更新、空串/缺省=不变、
// clear_*=清空；push 模式 clear_token 拒绝（没有密钥的上报服务器无法上报）。
func TestUpdateCredSemantics(t *testing.T) {
	r, s := newServersRouter(t)
	srv := newSSHServer(t, s, "ssh-x", "10.0.0.3:22", "root", "", "old-pw", 0)
	assertPW := func(want string) {
		t.Helper()
		got, err := s.GetServer(srv.ID)
		if err != nil || got == nil {
			t.Fatalf("查服务器: %v %v", got, err)
		}
		if got.SSHPassword != want {
			t.Fatalf("DB 密码 = %q, want %q", got.SSHPassword, want)
		}
	}

	// 缺省（只改名）：密码不动
	if w := putServer(t, r, srv.ID, `{"name":"ssh-y"}`); w.Code != http.StatusOK {
		t.Fatalf("改名: %d %s", w.Code, w.Body.String())
	}
	assertPW("old-pw")
	// 空串：=不变（旧语义为清空，打码后前端不再回显，空串只是没填）
	if w := putServer(t, r, srv.ID, `{"ssh_password":""}`); w.Code != http.StatusOK {
		t.Fatalf("空串: %d %s", w.Code, w.Body.String())
	}
	assertPW("old-pw")
	// 非空：更新
	if w := putServer(t, r, srv.ID, `{"ssh_password":"new-pw"}`); w.Code != http.StatusOK {
		t.Fatalf("更新: %d %s", w.Code, w.Body.String())
	}
	assertPW("new-pw")
	// clear：清空（回退私钥认证）
	if w := putServer(t, r, srv.ID, `{"clear_ssh_password":true}`); w.Code != http.StatusOK {
		t.Fatalf("清空: %d %s", w.Code, w.Body.String())
	}
	assertPW("")

	// token：agent 模式可清空
	ag, err := s.CreateServer("ag-x", "10.0.0.4:19100", "t1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if w := putServer(t, r, ag.ID, `{"clear_token":true}`); w.Code != http.StatusOK {
		t.Fatalf("agent 清 token: %d %s", w.Code, w.Body.String())
	}
	got, _ := s.GetServer(ag.ID)
	if got == nil || got.Token != "" {
		t.Fatalf("agent token 应已清空: %+v", got)
	}
	// push 模式：clear_token 400
	push, err := s.CreatePushServer("push-x", "pt1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if w := putServer(t, r, push.ID, `{"clear_token":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("push 清 token 应 400: %d %s", w.Code, w.Body.String())
	}
	got2, _ := s.GetServer(push.ID)
	if got2 == nil || got2.Token != "pt1" {
		t.Fatalf("push token 不应被清: %+v", got2)
	}
}

// TestSSHModeRetired 375：SSH 直连采集下线——Create 收 mode=ssh 显式 400（防静默
// 落成 agent）；Update 把非 ssh 机器改成 ssh 拒绝；存量 ssh 行其他字段编辑不受限。
func TestSSHModeRetired(t *testing.T) {
	r, s := newServersRouter(t)
	h := &ServerHandler{st: s, offlineAfter: 60}
	r.POST("/servers", h.Create)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/servers", strings.NewReader(
		`{"name":"x","mode":"ssh","agent_addr":"10.0.0.9:22","ssh_user":"root","ssh_password":"p"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "已下线") {
		t.Fatalf("Create ssh 应 400 已下线: %d %s", w.Code, w.Body.String())
	}

	aid, err := s.CreateServer("agent-1", "10.0.0.1:19100", "tok", 0)
	if err != nil {
		t.Fatal(err)
	}
	w = putServer(t, r, aid.ID, `{"mode":"ssh","agent_addr":"10.0.0.1:22","ssh_user":"root","ssh_password":"p"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "已下线") {
		t.Fatalf("Update 改成 ssh 应 400 已下线: %d %s", w.Code, w.Body.String())
	}

	sid := newSSHServer(t, s, "ssh-1", "10.0.0.2:22", "root", "", "pw", 0)
	w = putServer(t, r, sid.ID, `{"mode":"ssh","name":"ssh-1","agent_addr":"10.0.0.2:22","ssh_user":"root","remark":"备注"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("存量 ssh 行编辑其他字段应放行: %d %s", w.Code, w.Body.String())
	}
}
