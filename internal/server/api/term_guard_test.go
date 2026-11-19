package api

import (
	"archive/tar"
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

// 打包/复制递归经过密钥上级目录（如打包 /etc）时逐条目跳过密钥：archiveAdd
// 的守卫在触达 SFTP 之前（首行），nil client 即可验证。
func TestArchiveAddSkipsProtected(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	ctr := 0
	if err := archiveAdd(nil, tw, store.KeyFilePath(), "lm.idc", &ctr); err != nil {
		t.Fatalf("密钥条目应被静默跳过: %v", err)
	}
	if n := buf.Len(); n != 0 { // Close 之前判定：tar.Close 会写 1024 字节归档结束符
		t.Errorf("密钥条目不得写入 tar（实写 %d 字节）", n)
	}
	tw.Close()
}

// FileOp 的 oneof 白名单必须放行 times（属性页访问时间）：传密钥路径应命中
// 守卫 403 而非绑定 400——此前 oneof 漏了 times，该操作被拦成恒 400。
func TestFileOpTimesAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &TermHandler{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/term/files/op?server_id=1",
		bytes.NewReader([]byte(`{"op":"times","path":"/etc/lm.idc"}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	h.FileOp(c)
	if w.Code != http.StatusForbidden {
		t.Errorf("op=times 应通过绑定并命中密钥守卫（期望 403，实得 %d）", w.Code)
	}
}

// rfc5987 中文文件名按 UTF-8 字节转义（按 rune 转义会产出 4+ 位十六进制的乱码）。
func TestRFC5987(t *testing.T) {
	if got := rfc5987("报告.tar.gz"); got != "%E6%8A%A5%E5%91%8A.tar.gz" {
		t.Errorf("中文文件名应按 UTF-8 字节转义，实得 %q", got)
	}
}
