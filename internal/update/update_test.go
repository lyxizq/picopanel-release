package update

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// genTestKeys 生成一对测试密钥，返回公钥 hex 与签名函数。
func genTestKeys(t *testing.T) (pubHex string, sign func([]byte) string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(pub), func(msg []byte) string {
		return hex.EncodeToString(ed25519.Sign(priv, msg))
	}
}

func TestParseManifestSchemaGate(t *testing.T) {
	base := Manifest{Schema: ManifestSchema, Version: "v1.2.0", Channel: "stable",
		Assets: map[string]Asset{AssetServer: {File: fileServer, SHA256: "ab"}}}
	b, _ := json.Marshal(base)
	m, err := ParseManifest(b)
	if err != nil {
		t.Fatalf("合法 manifest 解析失败: %v", err)
	}
	if m.Version != "v1.2.0" {
		t.Fatalf("version 解析错: %s", m.Version)
	}

	future := base
	future.Schema = ManifestSchema + 1
	b2, _ := json.Marshal(future)
	if _, err := ParseManifest(b2); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("高版本 schema 应报 ErrUnsupportedSchema，实得 %v", err)
	}

	noVersion := base
	noVersion.Version = ""
	b3, _ := json.Marshal(noVersion)
	if _, err := ParseManifest(b3); err == nil {
		t.Fatal("缺 version 应报错")
	}
}

func TestVerifyManifest(t *testing.T) {
	pubHex, sign := genTestKeys(t)
	raw := []byte(`{"schema":1,"version":"v1.0.1"}`)
	sig := sign(raw)
	if err := VerifyManifest(pubHex, raw, sig); err != nil {
		t.Fatalf("正确签名应通过: %v", err)
	}
	if err := VerifyManifest(pubHex, []byte(`{"schema":1,"version":"v1.0.2"}`), sig); err == nil {
		t.Fatal("内容被改的验签应失败")
	}
	otherPub, otherSign := genTestKeys(t)
	_ = otherSign
	if err := VerifyManifest(otherPub, raw, sig); err == nil {
		t.Fatal("换公钥的验签应失败")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.1", "v1.0.0", false},
		{"v1.9.9", "v2.0.0", true},  // 跨中版本
		{"v1.9.9", "v2.0.0", true},  //
		{"v0.9.0", "v1.0.0", true},  // 跨大版本
		{"v1.0.0", "v1.0.0", false}, // 相同
		{"dev", "v1.0.0", false},    // 非 semver（dev/短哈希）不可比：不自动升级（防通道重放旧签名版本降级）
		{"v1.0.0", "v1.0.0", false},
		{"1.2", "1.2.0", false}, // 段数不齐按 0 补
		{"1.2", "1.2.1", true},
		// git describe 形态（2026-10-05 用户报「当前版本高于通道最新版本」）：
		{"v1.0.4-1-g5a97736", "v1.0.4", false}, // 比 tag 新 1 个提交 ≠ 落后
		{"v1.0.4-2-gabc", "v1.0.4", false},
		{"v1.0.4-1-gabc", "v1.0.5", true}, // 通道已越过本地基线 → 可升
		{"v1.0.4-dirty", "v1.0.4", false}, // 本地改动不算领先也不算落后
		{"v1.0.3-5-gabc", "v1.0.4", true}, // 本地基线低于通道 → 可升
		// 未提交构建的 server 版本串（Makefile SERVER_VERSION，2026-10-27）：同基线不落后、通道越过基线可升
		{"v1.1.2-dirty.20261027.1240", "v1.1.2", false},
		{"v1.1.2-dirty.20261027.1240", "v1.1.3", true},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q,%q)=%v 期望 %v", c.cur, c.latest, got, c.want)
		}
	}
}

// fakePackage 造一个合法的"更新包"：gz 流内容 ≥1MB（gunzipTo 的损坏判定线），
// 明文内容可校验。
func fakePackage(t *testing.T, fill byte) []byte {
	t.Helper()
	plain := bytes.Repeat([]byte{fill}, 1<<20)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestApplyChainAndRollback(t *testing.T) {
	dir := t.TempDir()
	oldBin := filepath.Join(dir, "lz-server")
	if err := os.WriteFile(oldBin, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "upgrade.json")
	pkg := fakePackage(t, 0xab)

	var execedPath string
	execed := false
	origExec := execFn
	execFn = func(path string, args []string) error {
		execed = true
		execedPath = path
		return nil
	}
	t.Cleanup(func() { execFn = origExec })

	err := Apply(ApplyOptions{
		PackageGZ:   pkg,
		BinPath:     oldBin,
		MarkerPath:  marker,
		FromVersion: "v1.0.0",
		Exec: func(path string, args []string) error {
			execed = true
			execedPath = path
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}
	// 替换后：主文件是新内容、旧版保 .old、标记在位、exec 被调到主文件
	got, _ := os.ReadFile(oldBin)
	if len(got) != 1<<20 || got[0] != 0xab {
		t.Fatal("主文件应为新包明文")
	}
	oldContent, err := os.ReadFile(oldBin + ".old")
	if err != nil || string(oldContent) != "old-binary" {
		t.Fatalf(".old 应保留旧版内容: %v", err)
	}
	m, err := LoadMarker(marker)
	if err != nil || m == nil || m.OldBin != oldBin+".old" || m.From != "v1.0.0" {
		t.Fatalf("升级标记应写入: %v %+v", err, m)
	}
	if !execed || execedPath != oldBin {
		t.Fatalf("应 exec 新主文件，实得 %q (execed=%v)", execedPath, execed)
	}

	// sha256 不匹配必须拒绝（在动手之前）
	bad := append([]byte{}, pkg...)
	bad[len(bad)-1] ^= 0xff
	err = Apply(ApplyOptions{PackageGZ: bad, ExpectSHA256: "00", BinPath: oldBin, MarkerPath: marker,
		Exec: func(string, []string) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("sha256 不符应拒绝: %v", err)
	}

	// 坏 gz（非 gzip 流）必须拒绝且不动主文件
	err = Apply(ApplyOptions{PackageGZ: []byte("not-a-gzip-but-long-enough-to-pass-size-guard-xxxxxxxxxxxxxxxxxxxx"),
		BinPath: oldBin, MarkerPath: marker, Exec: func(string, []string) error { return nil }})
	if err == nil {
		t.Fatal("坏 gz 应报错")
	}
	if got, _ := os.ReadFile(oldBin); len(got) != 1<<20 {
		t.Fatal("失败的 Apply 不得动主文件")
	}

	// 回滚：Rollback exec 旧版并累加 Attempts；第二次必须拒绝（防打转）
	rbCalled := ""
	origExec2 := execFn
	execFn = func(path string, args []string) error { rbCalled = path; return nil }
	t.Cleanup(func() { execFn = origExec2 })
	if err := Rollback(marker); err != nil {
		t.Fatalf("首次回滚应成功: %v", err)
	}
	if rbCalled != oldBin+".old" {
		t.Fatalf("回滚应 exec .old，实得 %q", rbCalled)
	}
	if err := Rollback(marker); err == nil {
		t.Fatal("第二次回滚必须拒绝（防无限打转）")
	}
}

func TestRollbackWithoutMarker(t *testing.T) {
	if err := Rollback(filepath.Join(t.TempDir(), "none.json")); err == nil {
		t.Fatal("无标记时回滚应报错")
	}
}

func TestBundleRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := WriteBundle(mw, []byte(`{"schema":1}`), []byte("SIG"), []byte("package-bytes")); err != nil {
		t.Fatal(err)
	}
	mr, err := NewBundleReader(mw.FormDataContentType(), bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	raw, sig, pkg, err := ParseBundle(mr, 0)
	if err != nil {
		t.Fatalf("解析分发束: %v", err)
	}
	if string(raw) != `{"schema":1}` || sig != "SIG" || !bytes.Equal(pkg, []byte("package-bytes")) {
		t.Fatalf("往返不一致: %q %q %q", raw, sig, pkg)
	}

	// 缺段必须报错（只写 manifest 一段）
	var half bytes.Buffer
	mw2 := multipart.NewWriter(&half)
	part, _ := mw2.CreateFormFile("manifest", "manifest.json")
	part.Write([]byte(`{"schema":1}`))
	mw2.Close()
	mr2, err := NewBundleReader(mw2.FormDataContentType(), bytes.NewReader(half.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ParseBundle(mr2, 0); err == nil {
		t.Fatal("缺段分发束应报错")
	}
}

func TestResolveLatestStatic(t *testing.T) {
	pubHex, sign := genTestKeys(t)
	pkgSum := sha256.Sum256([]byte("abc"))
	manifest := Manifest{Schema: ManifestSchema, Version: "v2.0.0", Channel: "stable",
		Assets: map[string]Asset{AssetServer: {File: fileServer, SHA256: hex.EncodeToString(pkgSum[:]), Size: 3}}}
	raw, _ := json.Marshal(manifest)
	sig := sign(raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stable/manifest.json":
			w.Write(raw)
		case "/stable/manifest.json.sig":
			w.Write([]byte(sig))
		case "/stable/lz-server.gz":
			w.Write([]byte("abc"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, err := ResolveLatest(context.Background(), srv.Client(), []string{srv.URL + "/stable"}, "stable")
	if err != nil {
		t.Fatalf("静态源探测失败: %v", err)
	}
	if res.Manifest.Version != "v2.0.0" {
		t.Fatalf("版本不符: %s", res.Manifest.Version)
	}
	if err := VerifyManifest(pubHex, res.Raw, res.Sig); err != nil {
		t.Fatalf("验签: %v", err)
	}
	pkg, err := DownloadAsset(context.Background(), srv.Client(), res, AssetServer)
	if err != nil || string(pkg) != "abc" {
		t.Fatalf("下载资产: %v %q", err, pkg)
	}
	// 依次回退：第一个源不可用走第二个
	res2, err := ResolveLatest(context.Background(), srv.Client(),
		[]string{srv.URL + "/nope", srv.URL + "/stable"}, "stable")
	if err != nil || res2.Manifest.Version != "v2.0.0" {
		t.Fatalf("源回退失败: %v", err)
	}
	// 全部不可用报错
	if _, err := ResolveLatest(context.Background(), srv.Client(), []string{srv.URL + "/nope"}, "stable"); err == nil {
		t.Fatal("全源不可用应报错")
	}
}

// TestResolveLatestGitHub github: 源解析回归（曾因 strings.Cut 误用——它返回
// 分隔符前后两段，前缀在开头时前段为空串被当成非法 owner——把 github: 源错当
// 静态目录，报 unsupported protocol scheme "github"）。
func TestResolveLatestGitHub(t *testing.T) {
	pubHex, sign := genTestKeys(t)
	pkgSum := sha256.Sum256([]byte("abc"))
	manifest := []byte(`{"schema":1,"version":"v3.0.0","channel":"stable","published_at":"t","notes":"n","assets":{` +
		`"server":{"file":"lz-server.gz","sha256":"` + hex.EncodeToString(pkgSum[:]) + `","size":3}}}`)
	sig := sign(manifest)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	relJSON, _ := json.Marshal(map[string]any{
		"tag_name": "v3.0.0",
		"assets": []map[string]string{
			{"name": "manifest.json", "browser_download_url": srv.URL + "/manifest.json"},
			{"name": "manifest.json.sig", "browser_download_url": srv.URL + "/manifest.json.sig"},
			{"name": "lz-server.gz", "browser_download_url": srv.URL + "/lz-server.gz"},
		},
	})
	mux.HandleFunc("/repos/me/picopanel-release/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Write(relJSON)
	})
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) { w.Write(manifest) })
	mux.HandleFunc("/manifest.json.sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sig)) })
	mux.HandleFunc("/lz-server.gz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("abc")) })

	origAPI := githubAPI
	githubAPI = srv.URL
	t.Cleanup(func() { githubAPI = origAPI })

	res, err := ResolveLatest(context.Background(), srv.Client(), []string{"github:me/picopanel-release"}, "stable")
	if err != nil {
		t.Fatalf("github 源解析失败: %v", err)
	}
	if res.Manifest.Version != "v3.0.0" {
		t.Fatalf("版本不符: %s", res.Manifest.Version)
	}
	if err := VerifyManifest(pubHex, res.Raw, res.Sig); err != nil {
		t.Fatalf("验签: %v", err)
	}
	pkg, err := DownloadAsset(context.Background(), srv.Client(), res, AssetServer)
	if err != nil || string(pkg) != "abc" {
		t.Fatalf("下载资产: %v %q", err, pkg)
	}
	// 缺 owner/repo 的残缺 github: 源必须报错而不是落到静态分支
	if _, err := ResolveLatest(context.Background(), srv.Client(), []string{"github:"}, "stable"); err == nil {
		t.Fatal("github: 空源应报错")
	}
}

// WriteVersionFile（2026-10-06 用户要求）：首写创建、同版本不重写（mtime 即
// 升级时间）、换版本覆盖。
func TestWriteVersionFile(t *testing.T) {
	dir := t.TempDir()
	vpath := filepath.Join(dir, "VERSION")
	if err := WriteVersionFile(dir, "v1.0.6"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(vpath)
	if err != nil || string(b) != "v1.0.6\n" {
		t.Fatalf("首写内容不符: %q %v", b, err)
	}
	st1, _ := os.Stat(vpath)
	time.Sleep(1100 * time.Millisecond) // 跨过 mtime 粒度
	if err := WriteVersionFile(dir, "v1.0.6"); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(vpath)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("同版本不应重写（mtime 应保持为升级时间）")
	}
	if err := WriteVersionFile(dir, "v1.0.7"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(vpath)
	if string(b) != "v1.0.7\n" {
		t.Fatalf("换版本应覆盖: %q", b)
	}
}
