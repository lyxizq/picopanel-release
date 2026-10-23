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
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

// TestSettingsMailSwitchRoundtrip 542：三渠道新邮件开关 PUT/GET 往返与
// 渠道包 getter 一致（缺省全关）。
func TestSettingsMailSwitchRoundtrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	setH := &SettingsHandler{st: s}
	r := gin.New()
	r.GET("/settings", setH.Get)
	r.PUT("/settings", setH.Put)

	get := func() map[string]any {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/settings", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET /settings = %d", w.Code)
		}
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	put := func(body string) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT /settings %s = %d: %s", body, w.Code, w.Body.String())
		}
	}

	keys := []string{"mail_wx_on", "mail_wh_on", "mail_webpush_on"}
	for _, k := range keys {
		if v := get()[k]; v != false {
			t.Fatalf("%s 缺省应为 false: %v", k, v)
		}
	}

	put(`{"mail_wx_on":true,"mail_wh_on":true,"mail_webpush_on":true}`)
	view := get()
	for _, k := range keys {
		if v := view[k]; v != true {
			t.Fatalf("%s 开启后应为 true: %v", k, v)
		}
	}
	if !wxpusher.MailWXOn(s) || !webhook.MailOn(s) || !webpush.MailOn(s) {
		t.Fatal("渠道包 getter 与 settings 视图不一致")
	}

	put(`{"mail_wx_on":false,"mail_wh_on":false,"mail_webpush_on":false}`)
	for _, k := range keys {
		if v := get()[k]; v != false {
			t.Fatalf("%s 关闭后应为 false: %v", k, v)
		}
	}
}
