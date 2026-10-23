package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webpush"
)

// TestDeleteWebPushSubAPI 删除订阅接口：DELETE /webpush/subs/:id 删对应行，
// settings 列表回传 fp 指纹（同 endpoint 指纹一致、可识别本浏览器），非法 ID 400。
func TestDeleteWebPushSubAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "lm.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := webpush.SetOn(s, true); err != nil {
		t.Fatal(err)
	}
	if err := webpush.SaveSubscription(s, "https://push.example.com/x", "k", "v", "UA-X"); err != nil {
		t.Fatal(err)
	}

	pushH := NewWebPushHandler(s)
	setH := &SettingsHandler{st: s}
	r := gin.New()
	grp := r.Group("/api/v1")
	grp.DELETE("/webpush/subs/:id", pushH.DeleteSub)
	grp.GET("/settings", setH.Get)

	// settings 列表带 fp，且指纹 = webpush.Fingerprint(endpoint)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("settings 应 200: %d", w.Code)
	}
	var view struct {
		WebPush struct {
			Subs []struct {
				ID int64  `json:"id"`
				Fp string `json:"fp"`
			} `json:"subs"`
		} `json:"webpush"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.WebPush.Subs) != 1 || view.WebPush.Subs[0].Fp != webpush.Fingerprint("https://push.example.com/x") {
		t.Fatalf("订阅列表/指纹不符: %+v want=%s", view.WebPush.Subs, webpush.Fingerprint("https://push.example.com/x"))
	}
	id := view.WebPush.Subs[0].ID

	// 删除
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/webpush/subs/"+itoa(id), nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("删除应 200: %d %s", w.Code, w.Body.String())
	}
	if n, err := s.CountPushSubs(); err != nil || n != 0 {
		t.Fatalf("删除后订阅应不存在（可重新订阅）: n=%d err=%v", n, err)
	}

	// 非法 ID 400；不存在 ID 幂等 200
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/webpush/subs/abc", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 ID 应 400: %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/webpush/subs/999", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("不存在 ID 应 200: %d", w.Code)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
