package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// 证书监控标签链路（2026-10-30）：创建带标签 → 清单透出 → PUT /certs/:id/label
// 修改/清空/超长按字截断 → 未知 id 404。

func newCertsRouter(t *testing.T) (*gin.Engine, *store.Store, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "certs.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	authH := &AuthHandler{st: s, guard: newLoginGuard(ipFails), userGuard: newLoginGuard(userFails)}
	r.POST("/api/v1/auth/login", authH.Login)
	w := loginWith(r, "admin", "admin123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("管理员登录应 200: %d %s", w.Code, w.Body.String())
	}
	token := bodyField(t, w, "token")
	certH := &CertHandler{st: s}
	grp := r.Group("/api/v1", authRequired(s), adminRequired())
	grp.GET("/certs", certH.List)
	grp.POST("/certs", certH.Create)
	grp.PUT("/certs/:id/label", certH.SetLabel)
	return r, s, token
}

func putCertJSON(r *gin.Engine, path, body, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	return w
}

type certLabelView struct {
	Cert struct {
		ID    int64  `json:"id"`
		Label string `json:"label"`
		State string `json:"state"`
	} `json:"cert"`
}

func TestCertLabelAPI(t *testing.T) {
	r, _, token := newCertsRouter(t)

	// 创建带标签（带首尾空格，应去空格落库）
	w := postJSON(r, "/api/v1/certs", `{"domain":"tagged.com","port":443,"label":" 生产 "}`, token)
	if w.Code != http.StatusOK {
		t.Fatalf("创建应 200: %d %s", w.Code, w.Body.String())
	}
	var created certLabelView
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Cert.Label != "生产" {
		t.Fatalf("创建应带去空格标签，得到 %q", created.Cert.Label)
	}

	// 清单透出标签
	w = getJSON(r, "/api/v1/certs", token)
	var list struct {
		Certs []struct {
			ID    int64  `json:"id"`
			Label string `json:"label"`
		} `json:"certs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Certs) != 1 || list.Certs[0].Label != "生产" {
		t.Fatalf("清单应透出标签: %+v", list.Certs)
	}

	// 修改标签（新库自增，首条 id=1）
	w = putCertJSON(r, "/api/v1/certs/1/label", `{"label":"测试"}`, token)
	if w.Code != http.StatusOK {
		t.Fatalf("改标签应 200: %d %s", w.Code, w.Body.String())
	}
	var upd certLabelView
	if err := json.Unmarshal(w.Body.Bytes(), &upd); err != nil || upd.Cert.Label != "测试" {
		t.Fatalf("改标签不符: %s", w.Body.String())
	}

	// 清空（空串=清除）
	w = putCertJSON(r, "/api/v1/certs/1/label", `{"label":""}`, token)
	if err := json.Unmarshal(w.Body.Bytes(), &upd); err != nil || upd.Cert.Label != "" {
		t.Fatalf("清空标签不符: %s", w.Body.String())
	}

	// 超长按字截断到 30（中文不打半个字）
	long := `{"label":"` + strings.Repeat("字", 35) + `"}`
	w = putCertJSON(r, "/api/v1/certs/1/label", long, token)
	if err := json.Unmarshal(w.Body.Bytes(), &upd); err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(upd.Cert.Label)); got != 30 {
		t.Fatalf("超长标签应截断到 30 字，得到 %d", got)
	}

	// 未知 id → 404
	w = putCertJSON(r, "/api/v1/certs/999/label", `{"label":"x"}`, token)
	if w.Code != http.StatusNotFound {
		t.Fatalf("未知 id 应 404: %d %s", w.Code, w.Body.String())
	}
}
