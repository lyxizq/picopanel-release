package updmgr

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/update"
)

// TestApplyServerChain 服务端自应用全链路（395，exec 桩掉不真重启）：
// 本地静态源 → Check 语义的解析验签 → 下载服务端包 → 备库钩子 →
// update.Apply（真试运行 -check-config + 原子替换 + 写升级标记）→ 到 exec 前。
// 试运行对象是真实构建的 lz-server（go build 命中构建缓存，秒级）。
func TestApplyServerChain(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("无 go 命令，跳过真链路")
	}
	// 构建新版 server 二进制（版本串 v0.0.2）
	newBin := filepath.Join(t.TempDir(), "lz-server-new")
	cmd := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-s -w -X main.version=v0.0.2",
		"-o", newBin, "../../../cmd/lz-server")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建新版失败: %v: %s", err, out)
	}
	orig, err := os.ReadFile(newBin)
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(orig); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(gz.Bytes())

	// 测试密钥 + 本地静态更新源
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schema":1,"version":"v0.0.2","channel":"stable","published_at":"t","notes":"链路","assets":{"server":{"file":"lz-server.gz","sha256":"` + hex.EncodeToString(sum[:]) + `","size":` + itoa(gz.Len()) + `}}}`)
	sig := hex.EncodeToString(ed25519.Sign(priv, manifest))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Write(manifest)
		case "/manifest.json.sig":
			w.Write([]byte(sig))
		case "/lz-server.gz":
			w.Write(gz.Bytes())
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

	// 备库钩子指向一个假库文件（真库由 server.go 装配）
	fakeDB := filepath.Join(t.TempDir(), "db.sqlite")
	if err := os.WriteFile(fakeDB, bytes.Repeat([]byte{1}, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	shutdownCalled := false
	execed := ""
	m := New(s, Config{Sources: []string{srv.URL}, Channel: "stable",
		PubKey: hex.EncodeToString(pub), Version: "v0.0.1", Dir: filepath.Join(t.TempDir(), "updates")})
	// exec 桩：记录目标路径，不真换映像（真 exec 已被 agent 真机链路验证）
	m.SetHooks(Hooks{
		BackupDB: func() error {
			if err := os.WriteFile(fakeDB, bytes.Repeat([]byte{2}, 4096), 0o644); err != nil {
				return err
			}
			return RotateBackup(fakeDB, 3)
		},
		Shutdown: func() { shutdownCalled = true },
		Exec: func(path string, args []string) error {
			execed = path
			return nil
		},
	})

	if err := m.ApplyServer(context.Background()); err != nil {
		t.Fatalf("ApplyServer: %v", err)
	}
	if !shutdownCalled {
		t.Fatal("PreRestart 应触发 Shutdown 钩子")
	}
	if _, err := os.Stat(fakeDB + ".pre-upgrade.1"); err != nil {
		t.Fatalf("升级前备份应在位: %v", err)
	}
	bk, _ := os.ReadFile(fakeDB + ".pre-upgrade.1")
	if bk[0] != 2 {
		t.Fatal("备份应是最新内容（hook 写入后轮换）")
	}
	// 升级标记写在「被替换二进制」同目录（本测试 = 测试二进制自身，正是自更新语义）
	marker := update.DefaultMarkerPath()
	mk, err := update.LoadMarker(marker)
	if err != nil || mk == nil || mk.From != "v0.0.1" {
		t.Fatalf("升级标记应写入: %v %+v", err, mk)
	}
	t.Cleanup(func() {
		os.Remove(marker)
	})
	if execed == "" {
		t.Fatal("应走到 exec（被桩拦下）")
	}
	// 主文件应已被换成新包内容
	now, err := os.ReadFile(execed)
	if err != nil || !bytes.Equal(now, orig) {
		t.Fatalf("主文件应为新版内容: %v", err)
	}
	// 旧版保留 .old
	if _, err := os.Stat(execed + ".old"); err != nil {
		t.Fatalf(".old 应保留: %v", err)
	}
}
