package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/term"
)

// newTermFilesRouter 搭只挂 SFTP 文件路由的测试服务（无真实 SSH 目标，
// 用于参数校验路径；连接失败路径返回 502 也在断言范围）。
func newTermFilesRouter(t *testing.T) (*gin.Engine, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "files.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	h := &TermHandler{term: term.NewManager(s)}
	r := gin.New()
	r.GET("/files", h.ListFiles)
	r.POST("/op", h.FileOp)
	r.GET("/download", h.Download)
	r.POST("/sftp-auth", h.SFTPAuth)
	return r, s
}

// TestTermFilesParamCheck 参数校验：server_id/path 缺失或相对路径直接 400，
// 不触发任何 SSH 连接。
func TestTermFilesParamCheck(t *testing.T) {
	r, _ := newTermFilesRouter(t)
	for _, target := range []string{
		"/files",                          // 无参数
		"/files?server_id=1",              // 无 path
		"/files?server_id=x&path=/tmp",    // 非法 id
		"/files?server_id=1&path=tmp",     // 相对路径
		"/download?server_id=x&path=/tmp", // download 同口径（非法 id）
		"/download?server_id=1&path=tmp",  // download 相对路径
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: 状态码 %d: %s", target, w.Code, w.Body.String())
		}
	}
}

// TestTermFilesOpWhitelist op 白名单：非法 op 直接 400。
func TestTermFilesOpWhitelist(t *testing.T) {
	r, _ := newTermFilesRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/op", strings.NewReader(`{"op":"rm-rf","path":"/tmp"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 op 应 400: %d %s", w.Code, w.Body.String())
	}
	// rename 缺 to：service 端 to 未校验时也不应崩——本例中 SSH 不可达返回 502
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/op", strings.NewReader(`{"op":"rename","path":"/tmp/a"}`))
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest && w2.Code != http.StatusBadGateway {
		t.Fatalf("rename 缺 to: %d %s", w2.Code, w2.Body.String())
	}
}

// TestTermFilesNoAddr push 模式服务器（无目标机地址）：SFTP 建连前即拒绝
// （502 + 明确原因）。agent/node 模式现已回退采集地址 host:22，不再落入此分支。
func TestTermFilesNoAddr(t *testing.T) {
	r, s := newTermFilesRouter(t)
	srv, err := s.CreatePushServer("push-01", "tok", 0)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/files?server_id="+strconv.FormatInt(srv.ID, 10)+"&path=/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "终端 SSH 地址") {
		t.Fatalf("应明确提示未配终端地址: %d %s", w.Code, w.Body.String())
	}
}

// TestSFTPAuthParamCheck sftp-auth 参数校验：缺 server_id 直接 400，不触发连接。
func TestSFTPAuthParamCheck(t *testing.T) {
	r, _ := newTermFilesRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sftp-auth", strings.NewReader(`{"username":"root","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺 server_id 应 400: %d %s", w.Code, w.Body.String())
	}
}

// serveSFTPOnly 最小 mock SSH：密码认证 + sftp subsystem（服务测试进程真实
// 文件系统，只在 TempDir 读写）。终端会话相关请求一律拒答。
func serveSFTPOnly(ln net.Listener, signer ssh.Signer, password string) {
	cfg := ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) != password {
				return nil, fmt.Errorf("密码错误")
			}
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			sconn, chans, reqs, err := ssh.NewServerConn(conn, &cfg)
			if err != nil {
				conn.Close()
				return
			}
			go ssh.DiscardRequests(reqs)
			for newCh := range chans {
				if newCh.ChannelType() != "session" {
					newCh.Reject(ssh.UnknownChannelType, "unsupported")
					continue
				}
				ch, chReqs, _ := newCh.Accept()
				go func(ch ssh.Channel, chReqs <-chan *ssh.Request) {
					for req := range chReqs {
						var payload struct{ Subsystem string }
						if req.Type == "subsystem" &&
							ssh.Unmarshal(req.Payload, &payload) == nil && payload.Subsystem == "sftp" {
							req.Reply(true, nil)
							if sv, err := sftp.NewServer(ch); err == nil {
								_ = sv.Serve()
								_ = ch.Close()
							}
							return
						}
						if req.WantReply {
							req.Reply(false, nil)
						}
					}
				}(ch, chReqs)
			}
			_ = sconn.Close()
		}()
	}
}

// newSFTPRouter 端到端环境：mock SSH（密码 pw123 + sftp subsystem）+ 全部文件路由。
// 返回的路由打 server_id 查询参数（回归：op 请求漏带 server_id 曾致一律 400）。
func newSFTPRouter(t *testing.T) (*gin.Engine, *store.Store, int64) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "sftp.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	go serveSFTPOnly(ln, signer, "pw123")
	srv := newSSHServer(t, s, "fs-mock", ln.Addr().String(), "root", "", "pw123", 0)
	h := &TermHandler{term: term.NewManager(s)}
	r := gin.New()
	r.GET("/files", h.ListFiles)
	r.POST("/files/upload", h.Upload)
	r.GET("/files/download", h.Download)
	r.POST("/op", h.FileOp)
	r.POST("/sftp-auth", h.SFTPAuth)
	r.POST("/count", h.StartCount)
	r.GET("/count", h.CountStatus)
	r.POST("/count-cancel", h.CancelCount)
	return r, s, srv.ID
}

func doFiles(t *testing.T, r *gin.Engine, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r.ServeHTTP(w, req)
	return w
}

// TestFileOpsEndToEnd 文件操作端到端（走真实 sftp subsystem + TempDir）：
// 列表 / touch（含已存在报错）/ rename 覆盖语义 / count 递归统计 / remove 递归删除。
func TestFileOpsEndToEnd(t *testing.T) {
	r, _, id := newSFTPRouter(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "d1", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d1", "b.txt"), []byte("hello-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d1", "sub", "c.txt"), []byte("ccc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := "server_id=" + strconv.FormatInt(id, 10) + "&path="

	// 列表：目录在前，含 a.txt 与 d1
	w := doFiles(t, r, http.MethodGet, "/files?"+q+urlPathEscape(root), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列表: %d %s", w.Code, w.Body.String())
	}
	var ls struct {
		Files []struct {
			Name  string `json:"name"`
			IsDir bool   `json:"is_dir"`
			Owner string `json:"owner"`
		} `json:"files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ls); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	owners := map[string]string{}
	for _, f := range ls.Files {
		names[f.Name] = f.IsDir
		owners[f.Name] = f.Owner
	}
	if isDir, ok := names["a.txt"]; !ok || isDir {
		t.Fatalf("列表应含文件 a.txt: %s", w.Body.String())
	}
	if isDir, ok := names["d1"]; !ok || !isDir {
		t.Fatalf("列表应含目录 d1: %s", w.Body.String())
	}
	// 所有者列：mock 服务本机 FS，/etc/passwd 可读（解析为用户名，
	// 失败也应回落数字 uid），两种情况都非空
	if owners["a.txt"] == "" {
		t.Fatalf("列表应带所有者: %s", w.Body.String())
	}

	// touch：新建成功；再 touch 同名报错
	if w := doFiles(t, r, http.MethodPost, "/op?"+q+filepath.Join(root, "new.txt"),
		strings.NewReader(`{"op":"touch","path":"`+filepath.Join(root, "new.txt")+`"}`)); w.Code != http.StatusOK {
		t.Fatalf("touch: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); err != nil {
		t.Fatalf("new.txt 应存在: %v", err)
	}
	w2 := doFiles(t, r, http.MethodPost, "/op?"+q+filepath.Join(root, "new.txt"),
		strings.NewReader(`{"op":"touch","path":"`+filepath.Join(root, "new.txt")+`"}`))
	if w2.Code != http.StatusBadGateway || !strings.Contains(w2.Body.String(), "已存在") {
		t.Fatalf("重复 touch 应报已存在: %d %s", w2.Code, w2.Body.String())
	}

	// rename 覆盖语义：a.txt → 已存在的 new.txt，内容被替换
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("zzz"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := doFiles(t, r, http.MethodPost, "/op?"+q+root,
		strings.NewReader(`{"op":"rename","path":"`+filepath.Join(root, "a.txt")+`","to":"`+filepath.Join(root, "new.txt")+`"}`)); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil || string(data) != "aaa" {
		t.Fatalf("rename 覆盖后内容应来自 a.txt: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("a.txt 应已不存在: %v", err)
	}

	// count：递归统计 d1（2 文件 / 1 子目录 / 10 字节）
	w3 := doFiles(t, r, http.MethodPost, "/op?"+q+root,
		strings.NewReader(`{"op":"count","path":"`+filepath.Join(root, "d1")+`"}`))
	if w3.Code != http.StatusOK {
		t.Fatalf("count: %d %s", w3.Code, w3.Body.String())
	}
	var cnt struct {
		Files int   `json:"files"`
		Dirs  int   `json:"dirs"`
		Size  int64 `json:"size"`
	}
	if err := json.Unmarshal(w3.Body.Bytes(), &cnt); err != nil {
		t.Fatal(err)
	}
	if cnt.Files != 2 || cnt.Dirs != 1 || cnt.Size != 10 {
		t.Fatalf("count = %d/%d/%d, want 2/1/10", cnt.Files, cnt.Dirs, cnt.Size)
	}

	// remove：递归删除非空目录 d1
	if w := doFiles(t, r, http.MethodPost, "/op?"+q+root,
		strings.NewReader(`{"op":"remove","path":"`+filepath.Join(root, "d1")+`"}`)); w.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "d1")); !os.IsNotExist(err) {
		t.Fatalf("d1 应已递归删除: %v", err)
	}
}

// urlPathEscape 路径段的查询参数转义（TempDir 在 macOS 含 /var/folders/…，无
// 特殊字符；Windows 风格反斜杠不存在于此环境，简单替换 %2F 兜底）。
func urlPathEscape(p string) string {
	return strings.ReplaceAll(p, "?", "%3F")
}

// TestUploadDownloadRoundtrip 上传/下载端到端：multipart 上传写盘、下载回读
// 内容一致且带 Content-Length。
func TestUploadDownloadRoundtrip(t *testing.T) {
	r, _, id := newSFTPRouter(t)
	dir := t.TempDir()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "up-测试.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("uploaded-内容")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/files/upload?server_id="+strconv.FormatInt(id, 10)+"&path="+dir, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("上传: %d %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "up-测试.txt"))
	if err != nil || string(data) != "uploaded-内容" {
		t.Fatalf("上传落盘内容: %q %v", data, err)
	}

	w2 := doFiles(t, r, http.MethodGet,
		"/files/download?server_id="+strconv.FormatInt(id, 10)+"&path="+dir+"/up-测试.txt", nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("下载: %d %s", w2.Code, w2.Body.String())
	}
	if w2.Body.String() != "uploaded-内容" {
		t.Fatalf("下载内容不一致: %q", w2.Body.String())
	}
	if w2.Header().Get("Content-Length") != strconv.Itoa(len("uploaded-内容")) {
		t.Fatalf("下载应带 Content-Length: %q", w2.Header().Get("Content-Length"))
	}
}

// TestSFTPAuthEndpoint 端点级行为：错密码 502；正确密码 save=true 后 DB 密码更新。
func TestSFTPAuthEndpoint(t *testing.T) {
	r, s, id := newSFTPRouter(t)
	// DB 密码改错，模拟凭据失效后页面重输
	bad := "stale"
	if err := s.UpdateServer(id, store.ServerUpdate{SSHPassword: &bad}); err != nil {
		t.Fatal(err)
	}
	// 错密码：拒绝
	w := doFiles(t, r, http.MethodPost, "/sftp-auth",
		strings.NewReader(`{"server_id":`+strconv.FormatInt(id, 10)+`,"username":"root","password":"nope"}`))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("错密码应 502: %d %s", w.Code, w.Body.String())
	}
	// 正确密码 + save：DB 密码更新，后续文件接口直接可用
	w2 := doFiles(t, r, http.MethodPost, "/sftp-auth",
		strings.NewReader(`{"server_id":`+strconv.FormatInt(id, 10)+`,"username":"root","password":"pw123","save":true}`))
	if w2.Code != http.StatusOK {
		t.Fatalf("正确密码应 200: %d %s", w2.Code, w2.Body.String())
	}
	got, _ := s.GetServer(id)
	if got == nil || got.SSHPassword != "pw123" {
		t.Fatalf("save=true 后 DB 密码应更新: %+v", got)
	}
	dir := t.TempDir()
	w3 := doFiles(t, r, http.MethodGet,
		"/files?server_id="+strconv.FormatInt(id, 10)+"&path="+dir, nil)
	if w3.Code != http.StatusOK {
		t.Fatalf("认证后列表应可用: %d %s", w3.Code, w3.Body.String())
	}
}

// TestCountTaskEndToEnd 目录递归统计后台任务（走真实 sftp subsystem）：
// 启动 → 轮询到终态 → 校验文件数/目录数/总字节；单文件路径直出 1/0；
// 相对路径 400。此前后台化是因为同步 count 在超大目录必超 HTTP 30s 超时。
func TestCountTaskEndToEnd(t *testing.T) {
	r, _, id := newSFTPRouter(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "d1", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	b1, b2, b3 := []byte("hello-b"), []byte("ccc"), []byte("aaa")
	if err := os.WriteFile(filepath.Join(root, "d1", "b.txt"), b1, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "d1", "sub", "c.txt"), b2, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), b3, 0o644); err != nil {
		t.Fatal(err)
	}
	q := "server_id=" + strconv.FormatInt(id, 10)

	// 相对路径直接 400
	if w := doFiles(t, r, http.MethodPost, "/count?"+q, strings.NewReader(`{"path":"tmp"}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("相对路径应 400: %d %s", w.Code, w.Body.String())
	}

	countTask := func(t *testing.T, path string, wantFiles, wantDirs int64, wantSize int64) {
		t.Helper()
		w := doFiles(t, r, http.MethodPost, "/count?"+q,
			strings.NewReader(`{"path":"`+path+`"}`))
		if w.Code != http.StatusOK {
			t.Fatalf("启动统计: %d %s", w.Code, w.Body.String())
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			ws := doFiles(t, r, http.MethodGet, "/count?"+q, nil)
			if ws.Code != http.StatusOK {
				t.Fatalf("查询统计: %d %s", ws.Code, ws.Body.String())
			}
			var res struct {
				Task *struct {
					Status string `json:"status"`
					Error  string `json:"error"`
					Files  int64  `json:"files"`
					Dirs   int64  `json:"dirs"`
					Size   int64  `json:"size"`
				} `json:"task"`
			}
			if err := json.Unmarshal(ws.Body.Bytes(), &res); err != nil {
				t.Fatal(err)
			}
			if res.Task == nil {
				t.Fatal("应返回任务状态")
			}
			if res.Task.Status == "running" {
				if time.Now().After(deadline) {
					t.Fatal("统计未在期限内完成")
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if res.Task.Status != "done" {
				t.Fatalf("应 done: %s %s", res.Task.Status, res.Task.Error)
			}
			if res.Task.Files != wantFiles || res.Task.Dirs != wantDirs || res.Task.Size != wantSize {
				t.Fatalf("统计结果 files=%d dirs=%d size=%d, 期望 %d/%d/%d",
					res.Task.Files, res.Task.Dirs, res.Task.Size, wantFiles, wantDirs, wantSize)
			}
			return
		}
	}

	// d1 递归：2 文件 1 目录，大小 = b1 + b2
	countTask(t, filepath.Join(root, "d1"), 2, 1, int64(len(b1)+len(b2)))
	// 单文件：1/0/自身大小
	countTask(t, filepath.Join(root, "a.txt"), 1, 0, int64(len(b3)))
	// 根：3 文件 2 目录（d1、sub）
	countTask(t, root, 3, 2, int64(len(b1)+len(b2)+len(b3)))
}
