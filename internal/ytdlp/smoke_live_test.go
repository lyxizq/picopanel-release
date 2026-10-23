package ytdlp

import (
	"context"
	"os"
	"testing"
)

// 真实栈冒烟（仅 LZ_YTDLP_SMOKE=1 时运行）：下载秒级公开小视频，验证参数
// 组合（--progress-template/--print 标记行）与解析链路真实可用。
func TestLiveDownloadSmoke(t *testing.T) {
	if os.Getenv("LZ_YTDLP_SMOKE") != "1" {
		t.Skip("需 LZ_YTDLP_SMOKE=1")
	}
	dir := t.TempDir()
	var title string
	var lastPct float64
	res, err := Download(context.Background(),
		"https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/360/Big_Buck_Bunny_360_10s_1MB.mp4",
		dir, "",
		&Events{
			OnTitle:    func(s string) { title = s },
			OnProgress: func(p Progress) { lastPct = p.Percent },
		})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	t.Logf("title=%q pct=%.1f file=%s size=%d", title, lastPct, res.FilePath, res.FileSize)
	if res.FilePath == "" || res.FileSize == 0 {
		t.Fatalf("缺产物: %+v", res)
	}
	if st, err := os.Stat(res.FilePath); err != nil || st.Size() != res.FileSize {
		t.Fatalf("文件落盘异常: %v", err)
	}
}
