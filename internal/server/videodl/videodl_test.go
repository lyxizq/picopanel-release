package videodl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lz-monitor/internal/server/store"
)

// 真实栈全链路冒烟（仅 LZ_YTDLP_SMOKE=1 时运行）：server 本机执行器领取 →
// yt-dlp 下载 → 终态落库（done/100%/文件路径与大小）。
func TestServerExecutorLiveE2E(t *testing.T) {
	if os.Getenv("LZ_YTDLP_SMOKE") != "1" {
		t.Skip("需 LZ_YTDLP_SMOKE=1")
	}
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "e2e.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	id, err := s.CreateVideoDlTask("https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/360/Big_Buck_Bunny_360_10s_1MB.mp4",
		t.TempDir(), "server", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	m := &manager{st: s}
	m.sweep(context.Background()) // 领取并同步执行
	row, err := s.GetVideoDlTaskState(id)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "done" || row.Progress != 100 || !strings.HasSuffix(row.FileName, ".mp4") || row.FileSize == 0 {
		t.Fatalf("终态不符: %+v", row)
	}
	if row.Title == "" {
		t.Fatalf("标题未回写: %+v", row)
	}
}

// 停止链路（纯库口径，无外网依赖）：停止标记 → 看护查询命中。
func TestStopRequestedQuery(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "stop.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	id, _ := s.CreateVideoDlTask("https://example.com/v.mp4", "/data/v", "server", 0, "")
	if ok, _ := s.VideoDlStopRequested(id); ok {
		t.Fatal("初始不应有停止标记")
	}
	if s.RequestVideoDlStop(id) {
		t.Fatal("pending 置停止标记应失败（仅 leased 可停，pending 走作废路径）")
	}
	if _, err := s.ClaimVideoDlTasks("server", 0, 1); err != nil {
		t.Fatal(err)
	}
	if !s.RequestVideoDlStop(id) {
		t.Fatal("leased 置标记应成功")
	}
	if ok, _ := s.VideoDlStopRequested(id); !ok {
		t.Fatal("停止标记应已置位")
	}
}
