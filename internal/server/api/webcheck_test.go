package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webcheck"
)

// newWebCheckRouter 真实链路：真实 Register（含鉴权组）+ 真实库 + 会话 token，
// 被检页面由 httptest 站点扮演；返回 setBody 供中途改页面内容驱动状态翻转，
// siteURL 为被检站点地址（先测后存的通过用例直接打它）。
func newWebCheckRouter(t *testing.T) (*gin.Engine, string, func(string), string) {
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
	var mu sync.Mutex
	body := "PicoPanel running"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	m := webcheck.New(s, nil)
	r := gin.New()
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, nil, nil, nil, nil, nil, nil, nil, m); err != nil {
		t.Fatal(err)
	}
	return r, token, func(b string) { mu.Lock(); body = b; mu.Unlock() }, srv.URL
}

// TestWebCheckFullChain 全链路：坏参数 → 先测后存（通/缺）→ 添加（创建即首检
// 通过）→ 查重 → 列表 → 改关键字重检翻失败 → 排序 → 手动检测 → 删除 → 401/404。
func TestWebCheckFullChain(t *testing.T) {
	r, token, setBody, site := newWebCheckRouter(t)
	base := "/api/v1"

	// 坏参数：无 scheme 补 http:// 由服务端规整；无规则/坏地址 400
	res := wcReq(t, r, token, "POST", base+"/webchecks", `{"url":"","rules":[["x"]]}`, http.StatusBadRequest)
	if !strings.Contains(res, "请填写网址") {
		t.Fatalf("空地址应报错: %s", res)
	}
	res = wcReq(t, r, token, "POST", base+"/webchecks", `{"url":"a.com","rules":[]}`, http.StatusBadRequest)
	if !strings.Contains(res, "至少配置一条规则") {
		t.Fatalf("无规则应报错: %s", res)
	}

	// 先测后存：通过（打 mock 站点，回命中规则序号）/ 缺失（逐规则清单）
	var testRes struct {
		Result struct {
			OK          bool       `json:"ok"`
			URL         string     `json:"url"`
			MatchedRule int        `json:"matched_rule"`
			Missing     [][]string `json:"missing"`
		} `json:"result"`
	}
	res = wcReq(t, r, token, "POST", base+"/webchecks/test", `{"url":"`+site+`","rules":[["picopanel"]]}`, http.StatusOK)
	if err := json.Unmarshal([]byte(res), &testRes); err != nil || !testRes.Result.OK || testRes.Result.MatchedRule != 1 {
		t.Fatalf("test 应通过并回命中规则: %s", res)
	}
	if !strings.HasPrefix(testRes.Result.URL, "http://127.0.0.1") {
		t.Fatalf("test 应回规整后 URL: %s", res)
	}
	// 或逻辑：规则 1 不满足、规则 2 满足 → 整体通过命中规则 2
	res = wcReq(t, r, token, "POST", base+"/webchecks/test", `{"url":"`+site+`","rules":[["不存在甲"],["picopanel"]]}`, http.StatusOK)
	if err := json.Unmarshal([]byte(res), &testRes); err != nil || !testRes.Result.OK || testRes.Result.MatchedRule != 2 {
		t.Fatalf("或逻辑第二规则命中应通过: %s", res)
	}
	// 全部规则不满足：失败，missing 与规则序对齐
	res = wcReq(t, r, token, "POST", base+"/webchecks/test", `{"url":"`+site+`","rules":[["picopanel","不存在甲"],["不存在乙"]]}`, http.StatusOK)
	if err := json.Unmarshal([]byte(res), &testRes); err != nil || testRes.Result.OK ||
		len(testRes.Result.Missing) != 2 || len(testRes.Result.Missing[0]) != 1 || testRes.Result.Missing[0][0] != "不存在甲" || testRes.Result.Missing[1][0] != "不存在乙" {
		t.Fatalf("test 缺失应逐规则对齐: %s", res)
	}

	// 添加（创建即首检，页面含关键字 → up=1）；地址不带 scheme（剥掉站点前缀）
	// 顺带验证「默认按 http:// 处理」
	host := strings.TrimPrefix(site, "http://")
	res = wcReq(t, r, token, "POST", base+"/webchecks", `{"name":"主站","url":"`+host+`","rules":[["picopanel","running"]],"enabled":true}`, http.StatusOK)
	var created struct {
		ID    int64 `json:"id"`
		Check struct {
			URL   string     `json:"url"`
			Rules [][]string `json:"rules"`
			Up    int        `json:"up"`
		} `json:"check"`
	}
	if err := json.Unmarshal([]byte(res), &created); err != nil || created.ID == 0 {
		t.Fatalf("建目标应回行: %s", res)
	}
	if created.Check.URL != site || created.Check.Up != 1 || len(created.Check.Rules) != 1 || len(created.Check.Rules[0]) != 2 {
		t.Fatalf("首检应通过且规则随行: %+v", created.Check)
	}

	// 同址查重
	res = wcReq(t, r, token, "POST", base+"/webchecks", `{"url":"`+site+`","rules":[["x"]]}`, http.StatusBadRequest)
	if !strings.Contains(res, "已在网站检查清单中") {
		t.Fatalf("同址应报已存在: %s", res)
	}

	// 列表
	res = wcReq(t, r, token, "GET", base+"/webchecks", "", http.StatusOK)
	if !strings.Contains(res, site) || !strings.Contains(res, `"max":16`) {
		t.Fatalf("列表应含目标与上限: %s", res)
	}

	// 改规则为页面没有的词：重置态 + 重检 → up=0，错误落库（多规则文案带规则序号）
	res = wcReq(t, r, token, "PUT", base+"/webchecks/"+itoa(created.ID), `{"name":"主站","url":"`+site+`","rules":[["消失的词甲"],["消失的词乙"]],"enabled":true}`, http.StatusOK)
	if !strings.Contains(res, `"up":0`) || !strings.Contains(res, "规则 1 缺少") || !strings.Contains(res, "消失的词乙") {
		t.Fatalf("改规则后应重检并翻失败: %s", res)
	}

	// 手动检测（页面恢复 → 或逻辑任一规则满足 → up=1）
	setBody("PicoPanel running 消失的词甲")
	res = wcReq(t, r, token, "POST", base+"/webchecks/"+itoa(created.ID)+"/check", "", http.StatusOK)
	if !strings.Contains(res, `"up":1`) {
		t.Fatalf("手动检测应恢复通过: %s", res)
	}

	// 排序 + 不存在目标 404 + 免鉴权 401
	_ = wcReq(t, r, token, "PUT", base+"/webchecks/order", `{"ids":[`+itoa(created.ID)+`]}`, http.StatusOK)
	_ = wcReq(t, r, token, "POST", base+"/webchecks/999/check", "", http.StatusNotFound)
	wcReq(t, r, "", "GET", base+"/webchecks", "", http.StatusUnauthorized)

	// 删除
	_ = wcReq(t, r, token, "DELETE", base+"/webchecks/"+itoa(created.ID), "", http.StatusOK)
	res = wcReq(t, r, token, "GET", base+"/webchecks", "", http.StatusOK)
	if strings.Contains(res, site) {
		t.Fatalf("删除后列表应不含目标: %s", res)
	}
}

func wcReq(t *testing.T, r *gin.Engine, token, method, path, body string, wantCode int) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(w, req)
	if w.Code != wantCode {
		t.Fatalf("%s %s 状态 %d（期望 %d）: %s", method, path, w.Code, wantCode, w.Body.String())
	}
	return w.Body.String()
}
