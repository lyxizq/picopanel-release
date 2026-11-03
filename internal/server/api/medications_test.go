package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/store"
)

// newMedRouter 真实链路（同 memberships）：真实 Register（含鉴权组）+ 真实库 + 会话 token，
// 推送渠道全未配。用药提醒接口全链路回归（2026-11-02）。
func newMedRouter(t *testing.T) (*gin.Engine, string, *store.Store) {
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

// medJSON 从响应体解出对象取字段（测试断言用）。
func medJSON(t *testing.T, body string, key string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("响应非 JSON: %v: %s", err, body)
	}
	return m[key]
}

// TestMedicationsAPI 用药提醒接口（2026-11-02）：人员/药物 CRUD + 打卡 +
// 今日/月历/依从率视图 + 库存 + 重排静态前缀 + MsgTypes + 未登录 401。
func TestMedicationsAPI(t *testing.T) {
	r, token, _ := newMedRouter(t)
	base := "/api/v1"

	// MsgTypes 白名单已收编 medication（消息中心类型筛选与按类型已读要用）
	if !MsgTypes["medication"] {
		t.Fatal("MsgTypes 应含 medication")
	}

	// 未登录 401
	if w := memReq(r, "", "GET", base+"/med-persons", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得 %d", w.Code)
	}

	// 人员：校验 + 创建 + 重排
	if res := memReq(r, token, "POST", base+"/med-persons", `{"name":""}`); res.Code != http.StatusBadRequest {
		t.Fatalf("空姓名应 400: %d %s", res.Code, res.Body.String())
	}
	res := memReq(r, token, "POST", base+"/med-persons", `{"name":"张三","color":"#409eff"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("建人失败: %d %s", res.Code, res.Body.String())
	}
	p1 := int(medJSON(t, res.Body.String(), "id").(float64))
	res = memReq(r, token, "POST", base+"/med-persons", `{"name":"李四"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("建人2失败: %s", res.Body.String())
	}
	p2 := int(medJSON(t, res.Body.String(), "id").(float64))
	if res := memReq(r, token, "POST", base+"/med-persons-reorder", `{"ids":[`+i64(int64(p2))+`,`+i64(int64(p1))+`]}`); res.Code != http.StatusOK {
		t.Fatalf("人员重排失败: %s", res.Body.String())
	}
	list := memReq(r, token, "GET", base+"/med-persons", "")
	if !strings.Contains(list.Body.String(), `"med_count":0`) {
		t.Fatalf("人员列表应带药物数: %s", list.Body.String())
	}

	// 药物：校验 + 创建（创建即排当日剂量）
	if res := memReq(r, token, "POST", base+"/medications", `{"person_id":`+i64(int64(p1))+`,"name":"","kind":"daily","times":["08:00"]}`); res.Code != http.StatusBadRequest {
		t.Fatalf("缺药名应 400: %s", res.Body.String())
	}
	res = memReq(r, token, "POST", base+"/medications", `{"person_id":`+i64(int64(p1))+`,"name":"阿莫西林","kind":"daily","times":["08:00","20:00"],"dose_amount":1,"dose_unit":"粒","form":"capsule","stock_qty":12,"stock_alert":3}`)
	if res.Code != http.StatusOK {
		t.Fatalf("建药失败: %d %s", res.Code, res.Body.String())
	}
	medID := i64(int64(medJSON(t, res.Body.String(), "id").(float64)))
	if !strings.Contains(res.Body.String(), `"times":["08:00","20:00"]`) {
		t.Fatalf("建药响应应带解析时刻: %s", res.Body.String())
	}

	// 日结已建今天两剂，打卡一剂
	today := time.Now().Format("2006-01-02")
	if res := memReq(r, token, "POST", base+"/meds/log", `{"med_id":`+medID+`,"time":"08:00","status":"taken"}`); res.Code != http.StatusOK {
		t.Fatalf("打卡失败: %s", res.Body.String())
	}
	day := memReq(r, token, "GET", base+"/meds/day?date="+today, "")
	if !strings.Contains(day.Body.String(), `"status":"taken"`) || !strings.Contains(day.Body.String(), `"status":"pending"`) {
		t.Fatalf("今日视图应含 taken+pending: %s", day.Body.String())
	}
	if !strings.Contains(day.Body.String(), `"person_name":"张三"`) {
		t.Fatalf("今日视图应联查人名: %s", day.Body.String())
	}
	// 打卡后库存 12→11
	got := memReq(r, token, "GET", base+"/medications", "")
	if !strings.Contains(got.Body.String(), `"stock_qty":11`) {
		t.Fatalf("服用应扣库存 12→11: %s", got.Body.String())
	}
	// 缺 date 应回落今天
	if res := memReq(r, token, "GET", base+"/meds/day", ""); res.Code != http.StatusOK {
		t.Fatalf("缺 date 应回落今天: %d", res.Code)
	}

	// 补货：手动改库存（重置标记）
	res = memReq(r, token, "POST", base+"/medications/"+medID+"/stock", `{"qty":30}`)
	if res.Code != http.StatusOK {
		t.Fatalf("改库存失败: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"stock_qty":30`) {
		t.Fatalf("改库存响应应回读: %s", res.Body.String())
	}
	// prn 记一笔：非 prn 应 400
	if res := memReq(r, token, "POST", base+"/medications/"+medID+"/prn", `{"med_id":`+medID+`}`); res.Code != http.StatusBadRequest {
		t.Fatalf("非 prn 记一笔应 400: %s", res.Body.String())
	}
	res = memReq(r, token, "POST", base+"/medications", `{"person_id":`+i64(int64(p2))+`,"name":"退烧药","kind":"prn","stock_qty":5,"stock_alert":1}`)
	if res.Code != http.StatusOK {
		t.Fatalf("建 prn 药失败: %s", res.Body.String())
	}
	prnID := i64(int64(medJSON(t, res.Body.String(), "id").(float64)))
	if res := memReq(r, token, "POST", base+"/medications/"+prnID+"/prn", `{"med_id":`+prnID+`}`); res.Code != http.StatusOK {
		t.Fatalf("prn 记一笔失败: %s", res.Body.String())
	}

	// 月历 + 依从率
	if res := memReq(r, token, "GET", base+"/meds/month?month="+today[:7], ""); res.Code != http.StatusOK {
		t.Fatalf("月历失败: %s", res.Body.String())
	}
	if res := memReq(r, token, "GET", base+"/meds/adherence?days=7", ""); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"days":7`) {
		t.Fatalf("依从率失败: %s", res.Body.String())
	}

	// 药物重排（静态前缀）+ 归档（PUT archived）+ 删除
	if res := memReq(r, token, "POST", base+"/medications-reorder", `{"ids":[`+prnID+`,`+medID+`]}`); res.Code != http.StatusOK {
		t.Fatalf("药物重排失败: %s", res.Body.String())
	}
	if res := memReq(r, token, "PUT", base+"/medications/"+medID, `{"person_id":`+i64(int64(p1))+`,"name":"阿莫西林","kind":"daily","times":["08:00","20:00"],"dose_amount":1,"dose_unit":"粒","form":"capsule","archived":true}`); res.Code != http.StatusOK {
		t.Fatalf("归档失败: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"archived_at":`) {
		t.Fatalf("归档响应应带 archived_at: %s", res.Body.String())
	}
	if res := memReq(r, token, "DELETE", base+"/medications/"+prnID, ""); res.Code != http.StatusOK {
		t.Fatalf("删药失败: %s", res.Body.String())
	}

	// 删人员级联：删张三后其药物消失
	if res := memReq(r, token, "DELETE", base+"/med-persons/"+i64(int64(p1)), ""); res.Code != http.StatusOK {
		t.Fatalf("删人失败: %s", res.Body.String())
	}
	list2 := memReq(r, token, "GET", base+"/medications?archived=1", "")
	if strings.Contains(list2.Body.String(), "阿莫西林") {
		t.Fatal("删人员应级联删其药物")
	}

	// 重排参数缺失 400
	if res := memReq(r, token, "POST", base+"/med-persons-reorder", `{}`); res.Code != http.StatusBadRequest {
		t.Fatalf("缺 ids 应 400: %s", res.Body.String())
	}
}

// TestMedLogCompleteDirect 提醒窗口直达打卡（2026-11-03）：站内消息/Web Push
// 「完成」按钮按台账行 ID 置 taken——管理员代管全人员；成员只完成绑定人名下的
// 行（跨空间 404 不泄露存在性）。
func TestMedLogCompleteDirect(t *testing.T) {
	r, token, s := newMedRouter(t)
	base := "/api/v1"

	// 建人建药（admin 代管的张三）+ 日结生成 pending
	res := memReq(r, token, "POST", base+"/med-persons", `{"name":"张三"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("建人失败: %s", res.Body.String())
	}
	p1 := int(medJSON(t, res.Body.String(), "id").(float64))
	res = memReq(r, token, "POST", base+"/medications", `{"person_id":`+i64(int64(p1))+`,"name":"阿莫西林","kind":"daily","times":["08:00"],"dose_amount":1,"form":"capsule"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("建药失败: %d %s", res.Code, res.Body.String())
	}
	today := time.Now().Format("2006-01-02")
	day := memReq(r, token, "GET", base+"/meds/day?date="+today, "")
	if day.Code != http.StatusOK {
		t.Fatalf("日视图失败: %d", day.Code)
	}
	var view struct {
		Items []struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(day.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Items) != 1 || view.Items[0].Status != "pending" {
		t.Fatalf("应有 1 条 pending: %+v", view.Items)
	}
	logID := view.Items[0].ID

	// 管理员直达打卡：置 taken + 库存联动
	res = memReq(r, token, "POST", base+"/meds/log-complete/"+i64(logID), "")
	if res.Code != http.StatusOK {
		t.Fatalf("直达打卡失败: %d %s", res.Code, res.Body.String())
	}
	day = memReq(r, token, "GET", base+"/meds/day?date="+today, "")
	if !strings.Contains(day.Body.String(), `"status":"taken"`) {
		t.Fatalf("打卡后应为 taken: %s", day.Body.String())
	}

	// 成员越权：fam 绑定的是自己的人员，摸张三的台账行=404
	if _, err := s.CreateUser("fam", "pass1234", store.RoleUser); err != nil {
		t.Fatal(err)
	}
	fam, _ := s.UserByUsername("fam")
	famTok, err := s.CreateSession(fam.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if res := memReq(r, famTok, "POST", base+"/meds/log-complete/"+i64(logID), ""); res.Code != http.StatusNotFound {
		t.Fatalf("成员完成他人剂量应 404: %d", res.Code)
	}
}
