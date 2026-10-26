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

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/store"
)

// newMemRouter 真实链路（492）：真实 Register（含鉴权组）+ 真实库 + 会话 token，
// 推送渠道全部未配（测试推送应汇总为 off）。
func newMemRouter(t *testing.T) (*gin.Engine, string, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal("查管理员失败")
	}
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return r, token, s
}

// TestMembershipsAPI 会员到期接口（492）：CRUD 全链路 + 参数校验 +
// 测试推送（渠道全关 = off，站内消息必落库）+ 未登录 401。
func TestMembershipsAPI(t *testing.T) {
	r, token, s := newMemRouter(t)
	base := "/api/v1"

	// 未登录 401
	if w := memReq(r, "", "GET", base+"/memberships", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得 %d", w.Code)
	}

	// 校验：缺业务 400
	if res := memReq(r, token, "POST", base+"/memberships", `{"expire_date":"2030-01-01"}`); res.Code != http.StatusBadRequest {
		t.Fatalf("缺业务应 400: %s", res.Body.String())
	}
	// 坏日期 400
	if res := memReq(r, token, "POST", base+"/memberships", `{"biz":"a","expire_date":"2030/01/01"}`); res.Code != http.StatusBadRequest {
		t.Fatalf("坏日期应 400: %s", res.Body.String())
	}

	// 新建（到期日 10 天后 → 进 15 天窗，expiring=1；开始时间 10 天前 → 进度 50%）
	expire := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	start := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	res := memReq(r, token, "POST", base+"/memberships", `{"biz":"Netflix","name":"年费","start_date":"`+start+`","expire_date":"`+expire+`","period_days":365}`)
	if res.Code != http.StatusOK {
		t.Fatalf("新建应 200: %s", res.Body.String())
	}
	var created struct {
		ID         int64  `json:"id"`
		Biz        string `json:"biz"`
		StartDate  string `json:"start_date"`
		ExpireDate string `json:"expire_date"`
		PeriodDays int    `json:"period_days"`
		Expired    bool   `json:"expired"`
		Progress   int    `json:"progress"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil || created.ID == 0 {
		t.Fatalf("新建应回记录: %s", res.Body.String())
	}
	if created.Biz != "Netflix" || created.StartDate != start || created.ExpireDate != expire || created.PeriodDays != 365 || created.Expired || created.Progress != 50 {
		t.Fatalf("新建回显口径错: %+v", created)
	}

	// 列表：items + total/expiring 统计
	res = memReq(r, token, "GET", base+"/memberships", "")
	var listRes struct {
		Items    []store.Membership `json:"items"`
		Total    int                `json:"total"`
		Expiring int                `json:"expiring"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &listRes); err != nil {
		t.Fatalf("列表解析失败: %s", res.Body.String())
	}
	if len(listRes.Items) != 1 || listRes.Total != 1 || listRes.Expiring != 1 {
		t.Fatalf("列表/统计口径错: %+v", listRes)
	}

	// 编辑：改到期日到 200 天后 → 退出 15 日窗
	res = memReq(r, token, "PUT", base+"/memberships/"+i64(created.ID),
		`{"biz":"Netflix","name":"年费","expire_date":"`+time.Now().AddDate(0, 0, 200).Format("2006-01-02")+`","period_days":0}`)
	if res.Code != http.StatusOK {
		t.Fatalf("编辑应 200: %s", res.Body.String())
	}
	if res = memReq(r, token, "GET", base+"/memberships", ""); !strings.Contains(res.Body.String(), `"expiring":0`) {
		t.Fatalf("改远期后 expiring 应为 0: %s", res.Body.String())
	}

	// 测试推送：渠道全关 → 站内消息必落库 + 各渠道 off
	res = memReq(r, token, "POST", base+"/memberships/"+i64(created.ID)+"/test-push", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"wxpusher":"off"`) {
		t.Fatalf("测试推送应回 off 汇总: %s", res.Body.String())
	}
	if n, err := s.UnreadCount(); err != nil || n != 1 {
		t.Fatalf("测试推送应落 1 条站内消息: %d, %v", n, err)
	}

	// 删除 + 404 兜底
	// 拖拽排序端点：缺 ids 400；全量 ids 重排后列表按新序
	if res := memReq(r, token, "POST", base+"/memberships-reorder", `{}`); res.Code != http.StatusBadRequest {
		t.Fatalf("缺 ids 应 400: %s", res.Body.String())
	}
	res = memReq(r, token, "POST", base+"/memberships-reorder", `{"ids":[`+i64(created.ID)+`]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("重排应 200: %s", res.Body.String())
	}
	res = memReq(r, token, "GET", base+"/memberships", "")
	if err := json.Unmarshal(res.Body.Bytes(), &listRes); err != nil || len(listRes.Items) != 1 || listRes.Items[0].ID != created.ID {
		t.Fatalf("重排后列表不符: %s", res.Body.String())
	}

	// 删除 + 404 兜底
	if res := memReq(r, token, "DELETE", base+"/memberships/"+i64(created.ID), ""); res.Code != http.StatusOK {
		t.Fatalf("删除应 200: %s", res.Body.String())
	}
	if res := memReq(r, token, "DELETE", base+"/memberships/"+i64(created.ID), ""); res.Code != http.StatusBadRequest {
		t.Fatalf("重复删除应 400: %s", res.Body.String())
	}
}

// memReq 带 Bearer 会话的接口请求（不校验状态码，断言留在用例内）。
func memReq(r *gin.Engine, token, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(w, req)
	return w
}

func i64(v int64) string {
	return strconv.FormatInt(v, 10)
}
