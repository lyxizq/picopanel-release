package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fresh：发布时间新鲜度窗口（防旧签名 manifest 重放降级）。
func TestFresh(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	fresh := &Manifest{Version: "v1.0.42", PublishedAt: now.Add(-24 * time.Hour).Format(time.RFC3339)}
	if err := Fresh(fresh, now); err != nil {
		t.Fatalf("24h 前发布的 manifest 应放行: %v", err)
	}
	stale := &Manifest{Version: "v1.0.10", PublishedAt: now.Add(-31 * 24 * time.Hour).Format(time.RFC3339)}
	if err := Fresh(stale, now); err == nil || !strings.Contains(err.Error(), "新鲜度") {
		t.Fatalf("31 天前发布的 manifest 应拒绝: %v", err)
	}
	// 字段缺失/不可解析放行：签名整体覆盖 manifest，字段不可剥离伪造；
	// 缺失只可能出自异常历史版本，Newer 版本比较兜底
	if err := Fresh(&Manifest{Version: "v1.0.42"}, now); err != nil {
		t.Fatalf("缺 published_at 应放行: %v", err)
	}
	if err := Fresh(&Manifest{Version: "v1.0.42", PublishedAt: "not-a-time"}, now); err != nil {
		t.Fatalf("published_at 不可解析应放行: %v", err)
	}
}

// 标记写失败必须中止升级并回退旧版：标记是断电自动回滚的凭据，带病升级
// 会在启动失败时无法回滚。
func TestApplyMarkerFailureAborts(t *testing.T) {
	dir := t.TempDir()
	oldBin := filepath.Join(dir, "lz-server")
	if err := os.WriteFile(oldBin, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "upgrade.json")
	if err := os.Mkdir(marker, 0o755); err != nil { // 标记路径是目录：writeMarker 必失败
		t.Fatal(err)
	}
	err := Apply(ApplyOptions{
		PackageGZ: fakePackage(t, 0xcd), BinPath: oldBin, MarkerPath: marker,
		FromVersion: "v1.0.0",
		Exec:        func(string, []string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "标记") {
		t.Fatalf("标记写失败应中止升级: %v", err)
	}
	got, rerr := os.ReadFile(oldBin)
	if rerr != nil || string(got) != "old-binary" {
		t.Fatalf("中止后主文件应回退为旧版: %v %q", rerr, got)
	}
	// 随机临时名不残留
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".upgrade-") {
			t.Errorf("临时文件残留: %s", e.Name())
		}
	}
}
