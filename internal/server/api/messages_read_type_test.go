package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// newMsgTypeRouter 搭真实登录 + 消息中心路由（in-process httptest），
// read-all 含 type 参数分支（452）。
func newMsgTypeRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "msg.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	authH := &AuthHandler{st: s, guard: newLoginGuard()}
	msgH := &MessagesHandler{st: s}
	r := gin.New()
	r.POST("/api/v1/auth/login", authH.Login)
	grp := r.Group("/api/v1", authRequired(s))
	grp.GET("/messages", msgH.List)
	grp.GET("/messages/unread", msgH.Unread)
	grp.POST("/messages/read-all", msgH.MarkAllRead)
	return r, s
}

func mustLoginToken(t *testing.T, r *gin.Engine) string {
	t.Helper()
	w := postJSON(r, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("登录应 200: %d %s", w.Code, w.Body.String())
	}
	var lr struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("登录应返回 token: %v %s", err, w.Body.String())
	}
	return lr.Token
}

// TestMarkAllReadByType 452 数据链路：read-all 带 type 只清该类型未读
//（进邮件菜单清 mail、进提醒事项菜单清 reminder），不带 type 仍全量清，
// 非白名单类型 400。
func TestMarkAllReadByType(t *testing.T) {
	r, s := newMsgTypeRouter(t)
	token := mustLoginToken(t, r)

	// 种子：2 封邮件提醒未读 + 1 条待办提醒未读 + 1 条告警未读 + 1 封邮件已读
	seed := []struct {
		typ  string
		read bool
	}{
		{"mail", false}, {"mail", false}, {"mail", true},
		{"reminder", false}, {"alert", false},
	}
	for i, m := range seed {
		if err := s.CreateMessage(&store.Message{Type: m.typ, Title: "t", CreatedAt: time.Now().Unix() + int64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	// 登录链路本身落一条未读「登录」消息（TestLoginLandsInMessageCenter 口径）
	p, err := s.ListMessages("mail", false, 1, 100)
	if err != nil || p.Total != 3 {
		t.Fatalf("邮件种子应 3 条: total=%d err=%v", p.Total, err)
	}
	// 把 id 最大的 mail 标已读（种子第 3 条；created_at 递增，id 随插入序）
	var readMailID int64
	for _, it := range p.Items {
		if it.ID > readMailID {
			readMailID = it.ID
		}
	}
	if err := s.MarkMessageRead(readMailID); err != nil {
		t.Fatal(err)
	}
	// 未读 = 邮件 2 + 待办 1 + 告警 1 + 登录 1
	if n, err := s.UnreadCount(); err != nil || n != 5 {
		t.Fatalf("前置未读应 5: n=%d err=%v", n, err)
	}

	// type=mail：只清邮件未读（2 条），待办/告警/登录不受牵连
	w := postJSON(r, "/api/v1/messages/read-all?type=mail", "", token)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"marked":2`) {
		t.Fatalf("type=mail 应标记 2: %d %s", w.Code, w.Body.String())
	}
	if n, _ := s.UnreadCount(); n != 3 {
		t.Fatalf("清邮件后未读应剩 3: %d", n)
	}
	p, err = s.ListMessages("mail", true, 1, 10)
	if err != nil || p.Total != 0 {
		t.Fatalf("邮件未读应清空: total=%d err=%v", p.Total, err)
	}
	p, err = s.ListMessages("reminder", true, 1, 10)
	if err != nil || p.Total != 1 {
		t.Fatalf("待办未读应保留: total=%d err=%v", p.Total, err)
	}

	// type=reminder：只清待办未读
	w = postJSON(r, "/api/v1/messages/read-all?type=reminder", "", token)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"marked":1`) {
		t.Fatalf("type=reminder 应标记 1: %d %s", w.Code, w.Body.String())
	}
	if n, _ := s.UnreadCount(); n != 2 {
		t.Fatalf("清待办后未读应剩 2（告警+登录）: %d", n)
	}

	// 非白名单类型：400 且不动库
	w = postJSON(r, "/api/v1/messages/read-all?type=everything", "", token)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未知类型应 400: %d %s", w.Code, w.Body.String())
	}
	if n, _ := s.UnreadCount(); n != 2 {
		t.Fatalf("400 不应动库: %d", n)
	}

	// 不带 type：兼容原口径全量清（剩告警+登录 2 条未读）
	w = postJSON(r, "/api/v1/messages/read-all", "", token)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"marked":2`) {
		t.Fatalf("全量应标记 2: %d %s", w.Code, w.Body.String())
	}
	if n, _ := s.UnreadCount(); n != 0 {
		t.Fatalf("全量清后未读应 0: %d", n)
	}
}
