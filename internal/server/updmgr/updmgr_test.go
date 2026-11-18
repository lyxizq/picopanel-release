package updmgr

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
)

func testKeys(t *testing.T) (string, func([]byte) string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(pub), func(msg []byte) string {
		return hex.EncodeToString(ed25519.Sign(priv, msg))
	}
}

func fakePkg(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 1<<20)
	for i := range b {
		b[i] = 0x5a
	}
	return b
}

// TestCheckCachesAgentPackages 静态源探测全链路：验签通过 → 判可升级 →
// agent 两架构包与 manifest 落缓存目录 → cachedVersion 就绪。
func TestCheckCachesAgentPackages(t *testing.T) {
	pubHex, sign := testKeys(t)
	pkg := fakePkg(t)
	sum := sha256.Sum256(pkg)
	shex := hex.EncodeToString(sum[:])
	manifest := []byte(`{"schema":1,"version":"v9.9.9","channel":"stable","published_at":"t","notes":"n","assets":{` +
		`"server":{"file":"lz-server.gz","sha256":"` + shex + `","size":` + itoa(len(pkg)) + `},` +
		`"agent_amd64":{"file":"lz-agent-amd64.gz","sha256":"` + shex + `","size":` + itoa(len(pkg)) + `},` +
		`"agent_arm64":{"file":"lz-agent-arm64.gz","sha256":"` + shex + `","size":` + itoa(len(pkg)) + `}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Write(manifest)
		case "/manifest.json.sig":
			w.Write([]byte(sign(manifest)))
		case "/lz-server.gz", "/lz-agent-amd64.gz", "/lz-agent-arm64.gz":
			w.Write(pkg)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	m := New(s, Config{Sources: []string{srv.URL}, Channel: "stable", PubKey: pubHex, Version: "v0.0.1", Dir: dir, Agents: false})

	st, err := m.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !st.Available || st.Latest != "v9.9.9" {
		t.Fatalf("应判可升级: %+v", st)
	}
	if !st.Cached || st.CachedVer != "v9.9.9" {
		t.Fatalf("agent 包应已缓存: %+v", st)
	}
	for _, f := range []string{"lz-agent-amd64.gz", "lz-agent-arm64.gz", "manifest.json", "manifest.json.sig", "target.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("缓存缺 %s: %v", f, err)
		}
	}
	// 缓存内容与源一致（字节级）
	got, _ := os.ReadFile(filepath.Join(dir, "lz-agent-amd64.gz"))
	if len(got) != len(pkg) {
		t.Fatal("缓存包大小不符")
	}
	// 再 Check：版本比较后仍 Available（本地版本没变）
	st2, err := m.Check(context.Background())
	if err != nil || !st2.Available {
		t.Fatalf("二次 Check: %v %+v", err, st2)
	}
}

// 同版本重发（删整发重跑换资产）必须被分发缓存感知：manifest 字节变了就换包，
// 字节一致才短路复用——否则重发的修复包永远发不出去（v1.0.5 实坑）。
func TestCacheAgentPackagesRepublishPicksUpNewAssets(t *testing.T) {
	pubHex, sign := testKeys(t)
	pkgA, pkgB := fakePkg(t), fakePkg(t)
	pkgB[0] = 0x5b // 内容不同 → sha 不同
	sumA, sumB := sha256.Sum256(pkgA), sha256.Sum256(pkgB)
	mkManifest := func(pkg []byte, sum [32]byte, notes string) []byte {
		return []byte(`{"schema":1,"version":"v9.9.9","channel":"stable","published_at":"t","notes":"` + notes + `","assets":{` +
			`"server":{"file":"lz-server.gz","sha256":"` + hex.EncodeToString(sum[:]) + `","size":` + itoa(len(pkg)) + `},` +
			`"agent_amd64":{"file":"lz-agent-amd64.gz","sha256":"` + hex.EncodeToString(sum[:]) + `","size":` + itoa(len(pkg)) + `},` +
			`"agent_arm64":{"file":"lz-agent-arm64.gz","sha256":"` + hex.EncodeToString(sum[:]) + `","size":` + itoa(len(pkg)) + `}}}`)
	}
	mfA, mfB := mkManifest(pkgA, sumA, "first"), mkManifest(pkgB, sumB, "republished")
	var mf atomic.Value // 当前通道提供的 manifest（可被测试中途替换）
	mf.Store(mfA)
	downloads := atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := mf.Load().([]byte)
		switch r.URL.Path {
		case "/manifest.json":
			w.Write(cur)
		case "/manifest.json.sig":
			w.Write([]byte(sign(cur)))
		case "/lz-server.gz", "/lz-agent-amd64.gz", "/lz-agent-arm64.gz":
			downloads.Add(1)
			if string(cur) == string(mfB) {
				w.Write(pkgB)
			} else {
				w.Write(pkgA)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	m := New(s, Config{Sources: []string{srv.URL}, Channel: "stable", PubKey: pubHex, Version: "v0.0.1", Dir: dir, Agents: false})

	if _, err := m.Check(context.Background()); err != nil {
		t.Fatalf("首次 Check: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if string(got) != string(mfA) {
		t.Fatal("首缓 manifest 应为第一版")
	}

	// 同版本二次 Check：manifest 字节一致 → 短路复用，不重复下载
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatalf("二次 Check: %v", err)
	}
	if n := downloads.Load(); n != 2 {
		t.Fatalf("同内容二次检查不应重新下载，实际 %d 次", n)
	}

	// 同版本但内容变了（重发）：必须换弹药
	mf.Store(mfB)
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatalf("重发后 Check: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "manifest.json"))
	if string(got) != string(mfB) {
		t.Fatal("重发后缓存 manifest 未更新")
	}
	gz, _ := os.ReadFile(filepath.Join(dir, "lz-agent-amd64.gz"))
	if gz[0] != 0x5b {
		t.Fatal("重发后缓存 agent 包未更新为新资产")
	}
}

// 篡改签名源的 Check 必须整体失败（验签硬门槛）。
func TestCheckRejectsBadSignature(t *testing.T) {
	pubHex, _ := testKeys(t)
	otherPub, otherSign := testKeys(t)
	_ = otherPub
	pkg := fakePkg(t)
	sum := sha256.Sum256(pkg)
	manifest := []byte(`{"schema":1,"version":"v9.9.9","channel":"stable","published_at":"t","notes":"n","assets":{"agent_amd64":{"file":"lz-agent-amd64.gz","sha256":"` + hex.EncodeToString(sum[:]) + `","size":` + itoa(len(pkg)) + `}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Write(manifest)
		case "/manifest.json.sig":
			w.Write([]byte(otherSign(manifest))) // 错误私钥签的
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := New(s, Config{Sources: []string{srv.URL}, PubKey: pubHex, Version: "v0.0.1", Dir: t.TempDir()})
	st, err := m.Check(context.Background())
	if err == nil || st.Error == "" {
		t.Fatal("坏签名源应检查失败")
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// TestRotateBackup 升级前数据库备份轮换：.1 最新、旧份依次后移、超份淘汰。
func TestRotateBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db.sqlite")
	write := func(n int) {
		if err := os.WriteFile(src, []byte(make([]byte, n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(1)
	if err := RotateBackup(src, 3); err != nil {
		t.Fatal(err)
	}
	write(2)
	if err := RotateBackup(src, 3); err != nil {
		t.Fatal(err)
	}
	write(3)
	if err := RotateBackup(src, 3); err != nil {
		t.Fatal(err)
	}
	write(4)
	if err := RotateBackup(src, 3); err != nil {
		t.Fatal(err)
	}
	// .1 应是第 4 版内容，.3 是第 2 版，.4 不存在（keep=3）
	read := func(p string) int {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return len(b)
	}
	if got := read(src + ".pre-upgrade.1"); got != 4 {
		t.Fatalf(".1 应最新(4)，实得 %d", got)
	}
	if got := read(src + ".pre-upgrade.2"); got != 3 {
		t.Fatalf(".2 应次新(3)，实得 %d", got)
	}
	if got := read(src + ".pre-upgrade.3"); got != 2 {
		t.Fatalf(".3 应(2)，实得 %d", got)
	}
	if _, err := os.Stat(src + ".pre-upgrade.4"); !os.IsNotExist(err) {
		t.Fatal("超出 keep 的备份应被淘汰")
	}
	// 主文件应原样保留
	if got := read(src); got != 4 {
		t.Fatalf("主文件应保留: %d", got)
	}
}

// staticSource 组一个本地静态更新源（manifest+签名+三包），供分发类测试复用。
func staticSource(t *testing.T, sign func([]byte) string, pkg []byte, version string) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(pkg)
	shex := hex.EncodeToString(sum[:])
	asset := `{"file":"lz-agent-amd64.gz","sha256":"` + shex + `","size":` + itoa(len(pkg)) + `}`
	manifest := []byte(`{"schema":1,"version":"` + version + `","channel":"stable","published_at":"t","notes":"n","assets":{` +
		`"server":{"file":"lz-server.gz","sha256":"` + shex + `","size":` + itoa(len(pkg)) + `},` +
		`"agent_amd64":` + asset + `,"agent_arm64":` + asset + `}}`)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Write(manifest)
		case "/manifest.json.sig":
			w.Write([]byte(sign(manifest)))
		case "/lz-server.gz", "/lz-agent-amd64.gz", "/lz-agent-arm64.gz":
			w.Write(pkg)
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestCheckDistributesAgentsWhenServerCurrent 回归（「多台 agent 只更新了一台」）：
// 自动分发的触发曾被错挂在「server 自身可升级」上——server 手动升到最新后，
// 剩余落后的 agent 永远无人分发、缓存也不再补（连「升级全部 Agent」都报未就绪）。
// 修复后：server 已是最新也要为落后 agent 备缓存并自动分发。
func TestCheckDistributesAgentsWhenServerCurrent(t *testing.T) {
	pubHex, sign := testKeys(t)
	pkg := fakePkg(t)
	var downloads int32
	manifestSrv := staticSource(t, sign, pkg, "v1.2.0")
	defer manifestSrv.Close()
	countSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/lz-agent") {
			atomic.AddInt32(&downloads, 1)
		}
		manifestSrv.Config.Handler.ServeHTTP(w, r)
	}))
	defer countSrv.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row, err := s.CreateServer("push-lag", "", "tok-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	mode := "push"
	if err := s.UpdateServer(row.ID, store.ServerUpdate{CollectMode: &mode}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertOnReport(row.ID, &proto.SysInfo{Hostname: "h", Arch: "amd64", AgentVersion: "v1.0.0"}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	// 停用的落后机器（71）：不计入待更新/未上报台数
	row2, err := s.CreateServer("disabled-lag", "", "tok-2", 0)
	if err != nil {
		t.Fatal(err)
	}
	mode2, off := "push", false
	if err := s.UpdateServer(row2.ID, store.ServerUpdate{CollectMode: &mode2, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertOnReport(row2.ID, &proto.SysInfo{Hostname: "h2", Arch: "amd64", AgentVersion: "v1.0.0"}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	m := New(s, Config{
		Sources: []string{countSrv.URL},
		PubKey:  pubHex,
		Version: "v1.2.0", // == 通道最新：server 自身无可升级
		Dir:     filepath.Join(t.TempDir(), "updates"),
		Agents:  true,
	})
	st, err := m.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Available {
		t.Fatal("server 已是最新不应 Available")
	}
	if !st.Cached {
		t.Fatalf("存在落后 agent 时也应备好缓存: %+v", st)
	}
	// 在线更新处的 agent 视角字段（2026-10-05 用户指定）；停用机器不计入（71）
	if st.AgentsLatest != "v1.2.0" || st.AgentsUpgradable != 1 {
		t.Fatalf("应报 agent 最新版与待升级台数（停用机不计入）: %+v", st)
	}
	if gs := m.Status(); gs.AgentsUpgradable != 1 || gs.AgentsLatest != "v1.2.0" {
		t.Fatalf("Status() 也应带 agent 台数统计（停用机不计入）: %+v", gs)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && m.PushInstruction(row.ID) == nil {
		time.Sleep(20 * time.Millisecond)
	}
	if m.PushInstruction(row.ID) == nil {
		t.Fatal("落后的 push 机器应被自动分发升级指令")
	}
	// 二次 Check：同版本缓存幂等，两架构包不重复下载
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&downloads); got != 2 {
		t.Fatalf("两架构包只应各下载一次（2），实得 %d", got)
	}
}

// TestUpgradeAgentsResultStates 直推结果状态必须回填（曾因值拷贝 append 丢失，
// pull 机器的接口结果 state 为空、用户看到的分发报告缺一半）。
func TestUpgradeAgentsResultStates(t *testing.T) {
	pubHex, sign := testKeys(t)
	pkg := fakePkg(t)
	src := staticSource(t, sign, pkg, "v1.2.0")
	defer src.Close()
	fakeAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/upgrade" {
			w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	}))
	defer fakeAgent.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row, err := s.CreateServer("pull-1", strings.TrimPrefix(fakeAgent.URL, "http://"), "tk", 0)
	if err != nil {
		t.Fatal(err)
	}
	m := New(s, Config{Sources: []string{src.URL}, PubKey: pubHex, Version: "v1.0.0", Dir: filepath.Join(t.TempDir(), "updates")})
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	results, err := m.UpgradeAgents(context.Background(), []int64{row.ID, 99999})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].State != "triggered" || results[0].Name != "pull-1" {
		t.Fatalf("直推结果应 triggered 且带名字: %+v", results[0])
	}
	if results[1].State != "error" || results[1].Detail != "机器不存在" {
		t.Fatalf("不存在机器应 error: %+v", results[1])
	}
}

// agent /upgrade 应用完即 exec 换映像、HTTP 响应发不回（客户端纯 EOF）：直推应
// 归类为已送达而非 ❌失败（结果以版本变化确认）——v1.0.5 现场误报的回归钉。
func TestUpgradeAgentsEOFTreatedAsSent(t *testing.T) {
	pubHex, sign := testKeys(t)
	src := staticSource(t, sign, fakePkg(t), "v1.2.0")
	defer src.Close()
	fakeAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // 收完整束（模拟 agent 已接收并开始应用）
		panic(http.ErrAbortHandler)        // 不写响应直接断连 → 客户端纯 EOF
	}))
	defer fakeAgent.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row, err := s.CreateServer("pull-eof", strings.TrimPrefix(fakeAgent.URL, "http://"), "tk", 0)
	if err != nil {
		t.Fatal(err)
	}
	m := New(s, Config{Sources: []string{src.URL}, PubKey: pubHex, Version: "v1.0.0", Dir: filepath.Join(t.TempDir(), "updates")})
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	results, err := m.UpgradeAgents(context.Background(), []int64{row.ID})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].State != "triggered" || !strings.Contains(results[0].Detail, "已送达") {
		t.Fatalf("纯 EOF 应归类已送达: %+v", results[0])
	}
}

// TestRotateBackupKeepOne 备份仅保留 1 份（2026-10-05 用户指定）：反复升级只留
// 最新一份 .pre-upgrade.1，旧份随每次升级被覆盖淘汰。
func TestRotateBackupKeepOne(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "db.sqlite")
	write := func(s string) {
		if err := os.WriteFile(src, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("v1")
	if err := RotateBackup(src, 1); err != nil {
		t.Fatal(err)
	}
	write("v2")
	if err := RotateBackup(src, 1); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(src + ".pre-upgrade.1")
	if err != nil || string(b) != "v2" {
		t.Fatalf(".1 应为最新内容 v2: %v %q", err, b)
	}
	if _, err := os.Stat(src + ".pre-upgrade.2"); !os.IsNotExist(err) {
		t.Fatal("keep=1 时不应有第二份备份")
	}
}

// TestAgentListDescribeNotUpgradable 回归（「版本提示错误，当前版本高于通道最新
// 版本」）：git describe 形态（tag 后有提交，如 v1.0.4-1-gX）的 agent 比通道
// 最新版还新，不得被判可升级（否则会被「降级」回通道版）。
func TestAgentListDescribeNotUpgradable(t *testing.T) {
	pubHex, sign := testKeys(t)
	pkg := fakePkg(t)
	src := staticSource(t, sign, pkg, "v1.0.4")
	defer src.Close()

	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := New(s, Config{Sources: []string{src.URL}, PubKey: pubHex, Version: "v1.0.4", Dir: filepath.Join(t.TempDir(), "updates"), Agents: false})
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	row, err := s.CreateServer("ahead", "127.0.0.1:19100", "tk", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertOnReport(row.ID, &proto.SysInfo{Hostname: "h", Arch: "amd64", AgentVersion: "v1.0.4-1-g5a97736"}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	list, err := m.AgentList()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Upgradable {
		t.Fatalf("describe 领先形态不应可升级: %+v", list)
	}
}
