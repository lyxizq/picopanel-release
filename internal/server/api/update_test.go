package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/updmgr"
	"lz-monitor/internal/update"
)

// newUpdRouter 真实链路：真实 Register + 更新管理器（测试密钥对 + 临时缓存目录）。
// 返回路由、管理员会话 token、store、更新管理器、manifest 签名函数。
func newUpdRouter(t *testing.T) (*gin.Engine, string, *store.Store, *updmgr.Manager, func([]byte) string) {
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
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(msg []byte) string { return hex.EncodeToString(ed25519.Sign(priv, msg)) }
	mgr := updmgr.New(s, updmgr.Config{PubKey: hex.EncodeToString(pub), Version: "v0.0.1", Dir: filepath.Join(t.TempDir(), "updates")})
	r := gin.New()
	if err := Register(r, s, engine.New(s, engine.Config{}), &config.Config{}, "test", nil, nil, nil, nil, nil, nil, nil, mgr, nil); err != nil {
		t.Fatal(err)
	}
	return r, token, s, mgr, sign
}

// seedCache 往管理器缓存目录放一套 agent 分发包（合法签名的 manifest + 假包
// 1MB），返回包内容。
func seedCache(t *testing.T, dir string, sign func([]byte) string) []byte {
	t.Helper()
	pkg := bytes.Repeat([]byte{0xcd}, 1<<20)
	sum := sha256.Sum256(pkg)
	manifest := []byte(`{"schema":1,"version":"v0.0.2","channel":"stable","published_at":"2026-10-05T00:00:00Z","notes":"t","assets":{"agent_amd64":{"file":"lz-agent-amd64.gz","sha256":"` + hex.EncodeToString(sum[:]) + `","size":` + strconv.Itoa(len(pkg)) + `}}}`)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string][]byte{
		"manifest.json":     manifest,
		"manifest.json.sig": []byte(sign(manifest)),
		"lz-agent-amd64.gz": pkg,
		"lz-agent-arm64.gz": pkg,
		"target.json":       []byte(`{"version":"v0.0.2"}`),
	} {
		if err := os.WriteFile(filepath.Join(dir, file), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return pkg
}

func adminReq(method, path, token string, body []byte) *http.Request {
	req, _ := http.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// TestUpdateAgentsUpgradeAndBundle push 机器升级全链路（396）：
// 打标记（queued）→ 上报旧版本领到指令 → 回源下载分发束（结构完整）→
// 上报新版本确认清除标记。
func TestUpdateAgentsUpgradeAndBundle(t *testing.T) {
	r, token, s, mgr, sign := newUpdRouter(t)
	seedCache(t, mgr.CacheDir(), sign)

	// 建 push 服务器（CreateServer 后改采集模式）
	row, err := s.CreateServer("push-1", "", "agent-tok-456", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := row.ID
	mode := "push"
	if err := s.UpdateServer(id, store.ServerUpdate{CollectMode: &mode}); err != nil {
		t.Fatal(err)
	}

	// ① 打升级标记 → queued
	w := httptest.NewRecorder()
	req := adminReq("POST", "/api/v1/update/agents/upgrade", token,
		[]byte(`{"ids":[`+strconv.FormatInt(id, 10)+`]}`))
	r.ServeHTTP(w, req)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"queued"`)) {
		t.Fatalf("打标应 queued: %d %s", w.Code, w.Body.String())
	}

	// ② 上报旧版本：响应必须捎带升级指令
	reportWith := func(ver string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		body, _ := json.Marshal(&proto.Report{Sys: &proto.SysInfo{Hostname: "h", AgentVersion: ver}})
		req, _ := http.NewRequest("POST", "/api/v1/agent/report", bytes.NewReader(body))
		req.Header.Set("X-Agent-Token", "agent-tok-456")
		r.ServeHTTP(w, req)
		return w
	}
	w = reportWith("v0.0.1")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"upgrade"`)) {
		t.Fatalf("旧版本上报应带升级指令: %d %s", w.Code, w.Body.String())
	}

	// ③ 回源下载分发束：200 + 三段齐全 + manifest 可解析出目标版本
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/v1/agent/upgrade-bundle?arch=amd64", nil)
	req.Header.Set("X-Agent-Token", "agent-tok-456")
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("分发束应 200: %d %s", w.Code, w.Body.String())
	}
	mr, err := update.NewBundleReader(w.Header().Get("Content-Type"), w.Body)
	if err != nil {
		t.Fatal(err)
	}
	mraw, _, gotPkg, err := update.ParseBundle(mr, 0)
	if err != nil {
		t.Fatalf("解析分发束: %v", err)
	}
	mf, err := update.ParseManifest(mraw)
	if err != nil || mf.Version != "v0.0.2" {
		t.Fatalf("束内 manifest 异常: %v %+v", err, mf)
	}
	if len(gotPkg) != 1<<20 {
		t.Fatalf("束内更新包大小异常: %d", len(gotPkg))
	}

	// ④ 上报新版本：确认清除标记，此后响应不再带指令
	if w = reportWith("v0.0.2"); bytes.Contains(w.Body.Bytes(), []byte(`"upgrade"`)) {
		t.Fatalf("升级确认上报不应再带指令: %s", w.Body.String())
	}
	if w = reportWith("v0.0.2"); bytes.Contains(w.Body.Bytes(), []byte(`"upgrade"`)) {
		t.Fatalf("标记已清，不应再有指令: %s", w.Body.String())
	}
}

// TestUpdateStatusUnconfigured 未配置 sources 时状态体明确 configured=false，
// 且 agent 列表端点可用。
func TestUpdateStatusUnconfigured(t *testing.T) {
	r, token, _, _, _ := newUpdRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, adminReq("GET", "/api/v1/update/status", token, nil))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"configured":false`)) {
		t.Fatalf("未配置源应 configured=false: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, adminReq("GET", "/api/v1/update/agents", token, nil))
	if w.Code != 200 {
		t.Fatalf("agent 列表应 200: %d", w.Code)
	}
}

// TestUpgradeAgentsRefusesPullWithoutToken 安全红线：pull 机器未配 token 必须
// 拒绝远程升级（二进制替换=任意代码执行）。
func TestUpgradeAgentsRefusesPullWithoutToken(t *testing.T) {
	r, token, s, mgr, sign := newUpdRouter(t)
	seedCache(t, mgr.CacheDir(), sign)
	row, err := s.CreateServer("pull-notoken", "127.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := row.ID
	w := httptest.NewRecorder()
	r.ServeHTTP(w, adminReq("POST", "/api/v1/update/agents/upgrade", token,
		[]byte(`{"ids":[`+strconv.FormatInt(id, 10)+`]}`)))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"skipped"`)) {
		t.Fatalf("无 token pull 机应 skipped: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("抓取密钥")) {
		t.Fatalf("拒绝理由应说明 token 口径: %s", w.Body.String())
	}
}
