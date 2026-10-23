package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// 密钥单点守卫：所有用户可控路径入口（含 FileOp/打包/任务）必须拦下密钥
// 文件——此前只挂 ListFiles/Download/Upload 三处，op=read、archive 全可绕过。
func TestIsProtectedPath(t *testing.T) {
	key := store.KeyFilePath()
	if !isProtectedPath(key) || !isProtectedPath("/etc/./lm.idc") || !isProtectedPath("/etc/etc/../lm.idc") {
		t.Error("密钥文件各路径形态都应命中守卫")
	}
	if isProtectedPath("/etc/lm.idc.bak") || isProtectedPath("/etc/lm.idc2") || isProtectedPath("/home/u/lm.idc") {
		t.Error("邻近路径不得误伤")
	}
}

// StartTask 的 archive/unarchive 分支在触达 SFTP 之前就要拒绝受保护路径与
// 非法产物名（handler 校验失败路径不依赖任何连接，nil 依赖即可测）。
func TestStartTaskRejects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	post := func(body string) int {
		h := &TermHandler{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/term/files/task?server_id=1",
			bytes.NewReader([]byte(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		h.StartTask(c)
		return w.Code
	}
	if code := post(`{"type":"archive","paths":["/etc/lm.idc"],"name":"x"}`); code != http.StatusForbidden {
		t.Errorf("打包密钥文件应 403，实得 %d", code)
	}
	if code := post(`{"type":"unarchive","path":"/etc/lm.idc"}`); code != http.StatusForbidden {
		t.Errorf("unarchive 密钥文件应 403（守卫先于格式校验），实得 %d", code)
	}
	if code := post(`{"type":"unarchive","path":"/tmp/foo.txt"}`); code != http.StatusBadRequest {
		t.Errorf("非压缩包路径应 400，实得 %d", code)
	}
	if code := post(`{"type":"archive","paths":["/tmp/a"],"name":"../../etc/x"}`); code != http.StatusBadRequest {
		t.Errorf("产物名含路径穿越应 400，实得 %d", code)
	}
}

// FileOp 在触达 SFTP 之前拒绝受保护路径（read/write/rename/remove 任一参数命中）。
func TestFileOpRejectsProtected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &TermHandler{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/term/files/op?server_id=1",
		bytes.NewReader([]byte(`{"op":"read","path":"/etc/lm.idc"}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	h.FileOp(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("op=read 密钥文件应 403，实得 %d", w.Code)
	}
}
