package api

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// newDocsRouter 搭文档模块路由测试服务（免鉴权，只测 DocHandler 端点行为）。
func newDocsRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "docs-api.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h := &DocHandler{st: s}
	r := gin.New()
	grp := r.Group("/api/v1")
	grp.POST("/docs", h.Create)
	grp.GET("/docs/:id", h.Get)
	grp.PUT("/docs/:id", h.Update)
	grp.DELETE("/docs/:id", h.Delete)
	grp.GET("/docs-trash", h.ListTrash)
	grp.POST("/docs-trash/:id/restore", h.RestoreTrash)
	grp.DELETE("/docs-trash/:id", h.DeleteTrash)
	grp.DELETE("/docs-trash", h.EmptyTrash)
	grp.GET("/docs/:id/versions", h.ListVersions)
	grp.GET("/docs/:id/versions/:vid", h.GetVersion)
	return r, s
}

func doDocs(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(w, req)
	return w
}

// TestDocsTrashAndVersionsAPI 端点冒烟：保存留版本 → 软删进回收站 → 恢复 →
// 彻底删连带版本；未软删的文档不能直接彻底删。
func TestDocsTrashAndVersionsAPI(t *testing.T) {
	r, s := newDocsRouter(t)

	// 建文档并保存两个版本的内容（第二版与第一版间隔不足 600s，只留第一版快照）
	w := doDocs(r, http.MethodPost, "/api/v1/docs", `{"title":"测试文档","format":"md"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":1`) {
		t.Fatalf("创建文档: %d %s", w.Code, w.Body.String())
	}
	w = doDocs(r, http.MethodPut, "/api/v1/docs/1", `{"content":"第一版内容"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("保存 v1: %d %s", w.Code, w.Body.String())
	}
	w = doDocs(r, http.MethodPut, "/api/v1/docs/1", `{"content":"第二版内容"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("保存 v2: %d %s", w.Code, w.Body.String())
	}

	// 版本列表：1 版（第一版内容快照，节流掉了 v2 保存）
	w = doDocs(r, http.MethodGet, "/api/v1/docs/1/versions", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"size":`) {
		t.Fatalf("版本列表: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"content":""`) && strings.Contains(w.Body.String(), "第一版内容") {
		t.Fatalf("版本列表不应带 content: %s", w.Body.String())
	}
	// 版本详情带 content（前端恢复用）
	if !strings.Contains(doDocs(r, http.MethodGet, "/api/v1/docs/1/versions/1", "").Body.String(), "第一版内容") {
		t.Fatalf("版本详情应带 content")
	}

	// 未软删不能直接彻底删
	if w = doDocs(r, http.MethodDelete, "/api/v1/docs-trash/1", ""); w.Code != http.StatusNotFound {
		t.Fatalf("未软删时彻底删应 404: %d %s", w.Code, w.Body.String())
	}

	// 软删 → 回收站可见 → 恢复 → 回收站空
	if w = doDocs(r, http.MethodDelete, "/api/v1/docs/1", ""); w.Code != http.StatusOK {
		t.Fatalf("软删: %d %s", w.Code, w.Body.String())
	}
	if w = doDocs(r, http.MethodGet, "/api/v1/docs-trash", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "测试文档") {
		t.Fatalf("回收站应含该文档: %d %s", w.Code, w.Body.String())
	}
	if w = doDocs(r, http.MethodPost, "/api/v1/docs-trash/1/restore", ""); w.Code != http.StatusOK {
		t.Fatalf("恢复: %d %s", w.Code, w.Body.String())
	}
	if w = doDocs(r, http.MethodGet, "/api/v1/docs-trash", ""); !strings.Contains(w.Body.String(), `"docs":[]`) {
		t.Fatalf("恢复后回收站应空: %s", w.Body.String())
	}
	// 恢复后文档仍在（含 content）
	if w = doDocs(r, http.MethodGet, "/api/v1/docs/1", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "第二版内容") {
		t.Fatalf("恢复后文档应可取: %d %s", w.Code, w.Body.String())
	}

	// 软删 → 彻底删：docs 与 doc_versions 一并消失，版本端点随后 404
	if w = doDocs(r, http.MethodDelete, "/api/v1/docs/1", ""); w.Code != http.StatusOK {
		t.Fatalf("再次软删: %d %s", w.Code, w.Body.String())
	}
	if w = doDocs(r, http.MethodDelete, "/api/v1/docs-trash/1", ""); w.Code != http.StatusOK {
		t.Fatalf("彻底删: %d %s", w.Code, w.Body.String())
	}
	if w = doDocs(r, http.MethodGet, "/api/v1/docs/1", ""); w.Code != http.StatusNotFound {
		t.Fatalf("彻底删后应 404: %d", w.Code)
	}
	if w = doDocs(r, http.MethodGet, "/api/v1/docs/1/versions/1", ""); w.Code != http.StatusNotFound {
		t.Fatalf("彻底删后版本应 404: %d", w.Code)
	}
	var vc int
	s.DB().QueryRow(`SELECT COUNT(*) FROM doc_versions`).Scan(&vc)
	if vc != 0 {
		t.Fatalf("彻底删应连带清版本: %d", vc)
	}
}

// TestDocsEmptyTrashAPI 清空回收站端点：软删文档连同版本全删、返回删除数。
func TestDocsEmptyTrashAPI(t *testing.T) {
	r, s := newDocsRouter(t)

	w := doDocs(r, http.MethodPost, "/api/v1/docs", `{"title":"A","format":"md"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("创建 A: %d %s", w.Code, w.Body.String())
	}
	if w = doDocs(r, http.MethodPost, "/api/v1/docs", `{"title":"B","format":"md"}`); w.Code != http.StatusOK {
		t.Fatalf("创建 B: %d %s", w.Code, w.Body.String())
	}
	// A 软删进回收站（B 保持正常）；B 两轮保存留一版历史（首存空→非空不留快照）
	doDocs(r, http.MethodPut, "/api/v1/docs/1", `{"content":"a1"}`)
	doDocs(r, http.MethodPut, "/api/v1/docs/2", `{"content":"b1"}`)
	doDocs(r, http.MethodPut, "/api/v1/docs/2", `{"content":"b2"}`)
	if w = doDocs(r, http.MethodDelete, "/api/v1/docs/1", ""); w.Code != http.StatusOK {
		t.Fatalf("软删 A: %d %s", w.Code, w.Body.String())
	}

	if w = doDocs(r, http.MethodDelete, "/api/v1/docs-trash", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":1`) {
		t.Fatalf("清空回收站: %d %s", w.Code, w.Body.String())
	}
	if w = doDocs(r, http.MethodGet, "/api/v1/docs/1", ""); w.Code != http.StatusNotFound {
		t.Fatalf("清空后 A 应 404: %d", w.Code)
	}
	var deadVer, aliveVer int
	s.DB().QueryRow(`SELECT COUNT(*) FROM doc_versions WHERE doc_id = 1`).Scan(&deadVer)
	s.DB().QueryRow(`SELECT COUNT(*) FROM doc_versions WHERE doc_id = 2`).Scan(&aliveVer)
	if deadVer != 0 || aliveVer != 1 {
		t.Fatalf("A 版本应连带删、B 版本保留: dead=%d alive=%d", deadVer, aliveVer)
	}
	// 空回收站再清不报错
	if w = doDocs(r, http.MethodDelete, "/api/v1/docs-trash", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":0`) {
		t.Fatalf("空回收站清空: %d %s", w.Code, w.Body.String())
	}
}

// TestDocsRouterNoConflict 完整路由树（Register 真实挂载）下回收站与版本
// 端点不与 /docs/:id 冲突（gin 静态/参数同层 panic 在启动即炸，这里兜底验证）。
func TestDocsRouterNoConflict(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "docs-router.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, s, nil, &config.Config{}, "test", nil, nil, nil, nil, nil, nil, nil, nil, nil) // 全部端点都应路由命中（免鉴权请求 401 也证明路由挂载成功，404 才是未挂上）
	for _, p := range []string{
		"/api/v1/docs", "/api/v1/docs/1", "/api/v1/docs/1/versions", "/api/v1/docs/1/versions/2",
		"/api/v1/docs-trash",
	} {
		if w := doDocs(r, http.MethodGet, p, ""); w.Code == http.StatusNotFound {
			t.Fatalf("路由未挂载: %s", p)
		}
	}
}

// TestDocSaveFlow 保存链路回归：自动保存带 base_updated_at 的 CAS 校验——
// 内容未被他窗口改过时放行；base 落后（其他窗口已保存）返回 409 由前端弹
// 覆盖/载入；不带 base = 强制覆盖。updated_at 为秒级，保存前隔 1 秒推进。
func TestDocSaveFlow(t *testing.T) {
	r, s := newDocsRouter(t)
	w := doDocs(r, "POST", "/api/v1/docs", `{"title":"保存链路","format":"richtext"}`)
	if w.Code != 200 {
		t.Fatalf("建文档: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	path := "/api/v1/docs/" + strconv.FormatInt(created.ID, 10)
	doc, err := s.GetDoc(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := doc.UpdatedAt

	time.Sleep(1100 * time.Millisecond)
	w = doDocs(r, "PUT", path, fmt.Sprintf(`{"content":"第一版","base_updated_at":%d}`, base))
	if w.Code != 200 {
		t.Fatalf("首次保存应 200: %d %s", w.Code, w.Body.String())
	}
	// 旧 base（模拟另一窗口已保存）→ 409 拒绝
	w = doDocs(r, "PUT", path, fmt.Sprintf(`{"content":"旧窗口内容","base_updated_at":%d}`, base))
	if w.Code != 409 {
		t.Fatalf("旧 base 应 409: %d %s", w.Code, w.Body.String())
	}
	// 不带 base = 冲突弹窗「覆盖远端」链路
	w = doDocs(r, "PUT", path, `{"content":"覆盖内容"}`)
	if w.Code != 200 {
		t.Fatalf("强制覆盖应 200: %d %s", w.Code, w.Body.String())
	}
	doc, _ = s.GetDoc(created.ID)
	if doc.Content != "覆盖内容" {
		t.Fatalf("覆盖后内容不符: %q", doc.Content)
	}
	// 新 base 正常保存标题
	w = doDocs(r, "PUT", path, fmt.Sprintf(`{"title":"改名","base_updated_at":%d}`, doc.UpdatedAt))
	if w.Code != 200 {
		t.Fatalf("保存标题应 200: %d %s", w.Code, w.Body.String())
	}
}

// TestDocSaveSizeLimit 单篇内容上限 20MB：超出返回 400「文档过大」（放行上限内的由
// TestDocSaveFlow 覆盖，这里只打边界外一行）。
func TestDocSaveSizeLimit(t *testing.T) {
	r, _ := newDocsRouter(t)
	w := doDocs(r, "POST", "/api/v1/docs", `{"title":"大小上限","format":"md"}`)
	if w.Code != 200 {
		t.Fatalf("建文档: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	var body bytes.Buffer
	body.WriteString(`{"content":"`)
	for i := 0; i < maxDocContent+1; i += 4096 {
		end := i + 4096
		if end > maxDocContent+1 {
			end = maxDocContent + 1
		}
		body.WriteString(strings.Repeat("a", end-i))
	}
	body.WriteString(`"}`)
	w = doDocs(r, "PUT", "/api/v1/docs/"+strconv.FormatInt(created.ID, 10), body.String())
	if w.Code != 400 || !strings.Contains(w.Body.String(), "文档过大") {
		t.Fatalf("超限应 400: %d %s", w.Code, w.Body.String())
	}
}

// 53 编辑器可选分组：PUT /docs/:id 带可选 group_id 换组（0=未分组合法），响应
// 回 move 后的新 updated_at（编辑器回填 CAS 基准防 409）；目标组不存在 400；
// 不带 group_id 的普通保存不动组。
func TestUpdateDocGroupChange(t *testing.T) {
	r, s := newDocsRouter(t)
	ga, err := s.CreateDocGroup("A")
	if err != nil {
		t.Fatal(err)
	}
	gb, err := s.CreateDocGroup("B")
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateDoc(ga.ID, "t", "richtext")
	if err != nil {
		t.Fatal(err)
	}
	before := created.UpdatedAt

	// 换到 B：200，组变、updated_at 前进（MoveDoc 刷新）
	w := doDocs(r, "PUT", "/api/v1/docs/"+strconv.FormatInt(created.ID, 10), `{"group_id":`+strconv.FormatInt(gb.ID, 10)+`}`)
	if w.Code != 200 {
		t.Fatalf("换组应 200: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		Code      int   `json:"code"`
		UpdatedAt int64 `json:"updated_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDoc(created.ID)
	if err != nil || d.GroupID != gb.ID {
		t.Fatalf("应换到 B 组: group=%d err=%v", d.GroupID, err)
	}
	if res.UpdatedAt != d.UpdatedAt || res.UpdatedAt < before {
		t.Fatalf("响应应回 move 后新 updated_at: res=%d doc=%d before=%d", res.UpdatedAt, d.UpdatedAt, before)
	}

	// 换到未分组（0 合法）
	w = doDocs(r, "PUT", "/api/v1/docs/"+strconv.FormatInt(created.ID, 10), `{"group_id":0}`)
	if w.Code != 200 {
		t.Fatalf("换未分组应 200: %d %s", w.Code, w.Body.String())
	}
	if d, err := s.GetDoc(created.ID); err != nil || d.GroupID != 0 {
		t.Fatalf("应回到未分组: group=%d err=%v", d.GroupID, err)
	}

	// 目标组不存在 400，组保持未分组
	w = doDocs(r, "PUT", "/api/v1/docs/"+strconv.FormatInt(created.ID, 10), `{"group_id":999999}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "目标分组不存在") {
		t.Fatalf("不存在组应 400: %d %s", w.Code, w.Body.String())
	}
	if d, _ := s.GetDoc(created.ID); d.GroupID != 0 {
		t.Fatalf("失败换组不应动组: %d", d.GroupID)
	}

	// 普通内容保存（不带 group_id）不动组：先移回 A，再纯内容保存
	if err := s.MoveDoc(created.ID, ga.ID); err != nil {
		t.Fatal(err)
	}
	w = doDocs(r, "PUT", "/api/v1/docs/"+strconv.FormatInt(created.ID, 10), `{"content":"hello"}`)
	if w.Code != 200 {
		t.Fatalf("内容保存应 200: %d %s", w.Code, w.Body.String())
	}
	if d, _ := s.GetDoc(created.ID); d.GroupID != ga.ID {
		t.Fatalf("不带 group_id 不应动组: %d", d.GroupID)
	}
}
