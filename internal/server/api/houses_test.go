package api

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/store"
)

// idBody 建/查响应的 id 字段提取。
type idBody struct {
	ID int64 `json:"id"`
}

// newHouseRouter 真实链路：真实 Register（含鉴权与家庭闸）+ 真实库 + 管理员
// 会话；另建一名家庭成员账号验证房屋管理 403（管理员专属 deny-by-default）。
func newHouseRouter(t *testing.T) (*gin.Engine, string, string, *store.Store) {
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
	if _, err := s.CreateUser("fam", "pass1234", "user"); err != nil {
		t.Fatal(err)
	}
	fu, err := s.UserByUsername("fam")
	if err != nil || fu == nil {
		t.Fatal("查成员失败")
	}
	famTok, err := s.CreateSession(fu.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	return r, token, famTok, s
}

func hReq(r *gin.Engine, tok, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestHousesAPI 房屋管理接口（2026-11-03）：房屋/家具电器/租约全链路 +
// 在租排他 + 合同下载（attachment/docx）+ 测试推送 + 家庭成员 403 + 未登录 401。
func TestHousesAPI(t *testing.T) {
	r, tok, famTok, s := newHouseRouter(t)

	// 未登录 401；家庭成员 403（/houses 组不在 familyRoutes）
	if w := hReq(r, "", "GET", "/api/v1/houses", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401: %d", w.Code)
	}
	if w := hReq(r, famTok, "GET", "/api/v1/houses", ""); w.Code != http.StatusForbidden {
		t.Fatalf("成员访问房屋管理应 403: %d", w.Code)
	}

	// 建房
	w := hReq(r, tok, "POST", "/api/v1/houses", `{"name":"望江府 1201","address":"江城 A&B 街 1 号","area":"89㎡","layout":"两室一厅","status":"selfuse","owner_name":"李房东","water_card":"W-001","electric_card":"E-001","gas_card":"G-001"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("建房失败: %d %s", w.Code, w.Body.String())
	}
	hid := func() string {
		var b idBody
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.ID == 0 {
			t.Fatalf("建房响应缺 id: %s", w.Body.String())
		}
		return fmt.Sprint(b.ID)
	}()

	// 家具电器
	if w = hReq(r, tok, "POST", "/api/v1/house-items", `{"house_id":`+hid+`,"category":"appliance","name":"空调","qty":2}`); w.Code != http.StatusOK {
		t.Fatalf("建家具失败: %d %s", w.Code, w.Body.String())
	}

	// 租约 + 同房排他
	w = hReq(r, tok, "POST", "/api/v1/house-leases", `{"house_id":`+hid+`,"tenant_name":"张三","start_date":"2026-01-01","end_date":"2026-12-31","monthly_rent":3500,"deposit":7000,"payment_cycle":"押一付三"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("建租约失败: %d %s", w.Code, w.Body.String())
	}
	lid := func() string {
		var b idBody
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.ID == 0 {
			t.Fatalf("建租约响应缺 id: %s", w.Body.String())
		}
		return fmt.Sprint(b.ID)
	}()
	if w = hReq(r, tok, "POST", "/api/v1/house-leases", `{"house_id":`+hid+`,"tenant_name":"李四","start_date":"2026-02-01","end_date":"2027-01-31"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("同房第二条在租应 400: %d", w.Code)
	}

	// 总览：统计现算（出租中 1、月租 3500、年化 42000）
	w = hReq(r, tok, "GET", "/api/v1/houses", "")
	if w.Code != http.StatusOK {
		t.Fatalf("总览失败: %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`"total":1`, `"rented":1`, `"month_rent":3500`, `"year_rent":42000`, `"display_status":"rented"`, `"tenant_name":"张三"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("总览缺 %s: %s", want, body)
		}
	}

	// 详情：房屋 + 家具电器 + 租约一次取齐
	if w = hReq(r, tok, "GET", "/api/v1/houses/"+hid, ""); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"leases"`) || !strings.Contains(w.Body.String(), `"items"`) {
		t.Fatalf("详情异常: %d %s", w.Code, w.Body.String())
	}

	// 合同下载：docx（zip 魔数）+ attachment 头（中文文件名 RFC 2231）
	req := httptest.NewRequest("GET", "/api/v1/house-leases/"+lid+"/contract", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("合同下载失败: %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "wordprocessingml.document") {
		t.Fatalf("Content-Type 应为 docx: %q", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "filename*") {
		t.Fatalf("Content-Disposition 异常: %q", cd)
	}
	if len(w.Body.Bytes()) < 2 || w.Body.Bytes()[0] != 'P' || w.Body.Bytes()[1] != 'K' {
		t.Fatal("合同产物不是 zip/docx")
	}

	// 测试推送：渠道全关 = off，站内消息必落库
	if w = hReq(r, tok, "POST", "/api/v1/house-leases/"+lid+"/test-push", ""); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"wxpusher":"off"`) {
		t.Fatalf("测试推送异常: %d %s", w.Code, w.Body.String())
	}
	msgs, err := s.ListMessages(0, "house", false, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs.Items {
		if m.Type == "house" && strings.Contains(m.Title, "望江府 1201") {
			found = true
		}
	}
	if !found {
		t.Fatal("测试推送应落 house 类型站内消息")
	}

	// 退租 → 统计回落；再删房（级联）
	if w = hReq(r, tok, "POST", "/api/v1/house-leases/"+lid+"/status", `{"status":"ended"}`); w.Code != http.StatusOK {
		t.Fatalf("退租失败: %d %s", w.Code, w.Body.String())
	}
	if w = hReq(r, tok, "GET", "/api/v1/houses", ""); !strings.Contains(w.Body.String(), `"month_rent":0`) {
		t.Fatalf("退租后月总租金应为 0: %s", w.Body.String())
	}
	if w = hReq(r, tok, "DELETE", "/api/v1/houses/"+hid, ""); w.Code != http.StatusOK {
		t.Fatalf("删房失败: %d %s", w.Code, w.Body.String())
	}
	if w = hReq(r, tok, "GET", "/api/v1/houses", ""); !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("删房后应为空: %s", w.Body.String())
	}
}

// TestHouseExpensesAPI 消耗费用与收支统计端点（2026-11-04）：费用 CRUD、详情
// 带 expenses、/house-income 三键口径（总/耗/净）、成员 403。
func TestHouseExpensesAPI(t *testing.T) {
	r, tok, famTok, _ := newHouseRouter(t)

	// 收支统计同属房屋管理模块：成员 403
	if w := hReq(r, famTok, "GET", "/api/v1/house-income?year=2026", ""); w.Code != http.StatusForbidden {
		t.Fatalf("成员访问收支统计应 403: %d", w.Code)
	}

	// 建房 + 全年租约 + 费用
	w := hReq(r, tok, "POST", "/api/v1/houses", `{"name":"收支房"}`)
	hid := func() string {
		var b idBody
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.ID == 0 {
			t.Fatalf("建房响应缺 id: %s", w.Body.String())
		}
		return fmt.Sprint(b.ID)
	}()
	if w = hReq(r, tok, "POST", "/api/v1/house-leases", `{"house_id":`+hid+`,"tenant_name":"张三","start_date":"2026-01-01","end_date":"2026-12-31","monthly_rent":3500}`); w.Code != http.StatusOK {
		t.Fatalf("建租约失败: %d %s", w.Code, w.Body.String())
	}
	w = hReq(r, tok, "POST", "/api/v1/house-expenses", `{"house_id":`+hid+`,"name":"取暖费","amount":1500,"spend_date":"2026-11-10"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("建费用失败: %d %s", w.Code, w.Body.String())
	}
	var eBody struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &eBody); err != nil || eBody.ID == 0 {
		t.Fatalf("建费用响应缺 id: %s", w.Body.String())
	}
	eid := eBody.ID

	// 详情带 expenses
	if w = hReq(r, tok, "GET", "/api/v1/houses/"+hid, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"expenses"`) {
		t.Fatalf("详情应带 expenses: %d %s", w.Code, w.Body.String())
	}

	// 收支统计：年 42000-1500=40500，四季度齐全
	w = hReq(r, tok, "GET", "/api/v1/house-income?year=2026", "")
	if w.Code != http.StatusOK {
		t.Fatalf("收支统计失败: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"year":2026`, `"gross":42000`, `"expense":1500`, `"net":40500`, `"q":1`, `"q":4`} {
		if !strings.Contains(body, want) {
			t.Fatalf("收支统计缺 %s: %s", want, body)
		}
	}
	// 非法年参回落当年
	if w = hReq(r, tok, "GET", "/api/v1/house-income?year=abc", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"year_income"`) {
		t.Fatalf("非法年参应回落当年: %d %s", w.Code, w.Body.String())
	}

	// 费用编辑/删除联动
	if w = hReq(r, tok, "PUT", "/api/v1/house-expenses/"+fmt.Sprint(eid), `{"house_id":`+hid+`,"name":"取暖费","amount":1800,"spend_date":"2026-11-10"}`); w.Code != http.StatusOK {
		t.Fatalf("编辑费用失败: %d %s", w.Code, w.Body.String())
	}
	if w = hReq(r, tok, "GET", "/api/v1/house-income?year=2026", ""); !strings.Contains(w.Body.String(), `"expense":1800`) || !strings.Contains(w.Body.String(), `"net":40200`) {
		t.Fatalf("费用编辑后收支未联动: %s", w.Body.String())
	}
	if w = hReq(r, tok, "DELETE", "/api/v1/house-expenses/"+fmt.Sprint(eid), ""); w.Code != http.StatusOK {
		t.Fatalf("删费用失败: %d", w.Code)
	}
	if w = hReq(r, tok, "GET", "/api/v1/house-income?year=2026", ""); !strings.Contains(w.Body.String(), `"expense":0`) || !strings.Contains(w.Body.String(), `"net":42000`) {
		t.Fatalf("删费用后收支未联动: %s", w.Body.String())
	}
}

// buildTplDocx 内存组装最小 docx（自定义模板测试）。
func buildTplDocx(t *testing.T, documentXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"[Content_Types].xml": `<Types/>`,
		"word/document.xml":   documentXML,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestHouseTemplateAPI 合同模板管理（2026-11-04）：上传/查询/恢复、坏模板拒绝、
// 上传后合同按自定义模板生成、家具家电 in_contract 勾选过滤。
func TestHouseTemplateAPI(t *testing.T) {
	r, tok, _, s := newHouseRouter(t)
	var w *httptest.ResponseRecorder

	// 初始：内置模板
	if w = hReq(r, tok, "GET", "/api/v1/house-contract-template", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"custom":false`) {
		t.Fatalf("初始应无自定义模板: %d %s", w.Code, w.Body.String())
	}
	// 坏数据拒绝
	if w := hReq(r, tok, "POST", "/api/v1/house-contract-template", `{"filename":"t.docx","data":"bm90IGEgemlw"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("非 docx 应 400: %d", w.Code)
	}
	// 上传自定义模板（{{乙方姓名}} 拆不拆 run 都行，这里单 run）
	tpl := base64.StdEncoding.EncodeToString(buildTplDocx(t,
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>租约乙方：{{乙方姓名}}</w:t></w:r></w:p><w:sectPr/></w:body></w:document>`))
	if w = hReq(r, tok, "POST", "/api/v1/house-contract-template", `{"filename":"我的模板.docx","data":"`+tpl+`"}`); w.Code != http.StatusOK {
		t.Fatalf("上传模板失败: %d %s", w.Code, w.Body.String())
	}
	if w = hReq(r, tok, "GET", "/api/v1/house-contract-template", ""); !strings.Contains(w.Body.String(), `"custom":true`) || !strings.Contains(w.Body.String(), "我的模板.docx") {
		t.Fatalf("模板状态异常: %s", w.Body.String())
	}

	// 建房+家具（默认进清单）+ 一个不进清单的 + 租约 → 下载按自定义模板生成且勾选过滤生效
	w = hReq(r, tok, "POST", "/api/v1/houses", `{"name":"模板房"}`)
	hid := func() string {
		var b idBody
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.ID == 0 {
			t.Fatalf("建房响应缺 id: %s", w.Body.String())
		}
		return fmt.Sprint(b.ID)
	}()
	if w = hReq(r, tok, "POST", "/api/v1/house-items", `{"house_id":`+hid+`,"name":"空调"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"in_contract":true`) {
		t.Fatalf("家具默认应进清单: %d %s", w.Code, w.Body.String())
	}
	if w = hReq(r, tok, "POST", "/api/v1/house-items", `{"house_id":`+hid+`,"name":"旧沙发","in_contract":false}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"in_contract":false`) {
		t.Fatalf("取消勾选未生效: %d %s", w.Code, w.Body.String())
	}
	if w = hReq(r, tok, "POST", "/api/v1/house-leases", `{"house_id":`+hid+`,"tenant_name":"张三","start_date":"2026-01-01","end_date":"2026-12-31"}`); w.Code != http.StatusOK {
		t.Fatalf("建租约失败: %d", w.Code)
	}
	lid := func() string {
		var b idBody
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.ID == 0 {
			t.Fatalf("建租约响应缺 id: %s", w.Body.String())
		}
		return fmt.Sprint(b.ID)
	}()
	req := httptest.NewRequest("GET", "/api/v1/house-leases/"+lid+"/contract", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("下载合同失败: %d", w.Code)
	}
	var out bytes.Buffer
	if _, err := io.Copy(&out, w.Body); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var docXML string
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			docXML = string(b)
		}
	}
	if !strings.Contains(docXML, "租约乙方：张三") {
		t.Fatalf("应按自定义模板生成: %s", docXML)
	}
	if !strings.Contains(docXML, "房屋物品交接清单") || !strings.Contains(docXML, "家具：空调") || strings.Contains(docXML, "旧沙发") {
		t.Fatal("物品清单页勾选过滤异常")
	}

	// 恢复内置
	if w = hReq(r, tok, "DELETE", "/api/v1/house-contract-template", ""); w.Code != http.StatusOK {
		t.Fatalf("恢复内置失败: %d", w.Code)
	}
	if w = hReq(r, tok, "GET", "/api/v1/house-contract-template", ""); !strings.Contains(w.Body.String(), `"custom":false`) {
		t.Fatalf("恢复后应无自定义模板: %s", w.Body.String())
	}
	if _, ok, _ := s.GetSetting("house_contract_template"); ok {
		t.Fatal("settings 键应已清空")
	}
}
