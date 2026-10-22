package ptfetch

import (
	"os"
	"strings"
	"testing"
)

// 真实站点联调探针（默认跳过，不进常规测试面）：
//
//	MTEAM_LIVE=1 MTEAM_API_KEY='...' go test ./internal/ptfetch/ -run TestMTeamLive -v
//
// 只打三个只读 API（profile/myPeerStatistics/mybonus），与每日轮同量。
// 另有无 key 冒烟：MTEAM_LIVE=1 不带 MTEAM_API_KEY 时用占位 key，
// 断言打到 api. 子域的 JSON 包络（「key無效」）而非 Web 域名的 nginx 403。
func TestMTeamLive(t *testing.T) {
	if os.Getenv("MTEAM_LIVE") != "1" {
		t.Skip("需要 MTEAM_LIVE=1（可选 MTEAM_API_KEY 传真实存取令牌）")
	}
	key := os.Getenv("MTEAM_API_KEY")
	if key == "" {
		key = strings.Repeat("0", 64)
		t.Log("未传 MTEAM_API_KEY，用占位 key 冒烟（应报 key無效）")
	}
	snap, err := FetchSite(Site{SiteKey: "mteam", BaseURL: "", Cookie: key})
	if key == strings.Repeat("0", 64) {
		if err == nil {
			t.Fatal("占位 key 不应成功")
		}
		if strings.Contains(err.Error(), "403") {
			t.Fatalf("仍在打 Web 域名吃 nginx 403: %v", err)
		}
		if !strings.Contains(err.Error(), "key") {
			t.Fatalf("应带出上游 key 报错: %v", err)
		}
		t.Logf("冒烟通过: %v", err)
		return
	}
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	t.Logf("快照: username=%q level=%q up=%d down=%d bonus=%v seeding=%d bonusPH=%v",
		snap.Username, snap.Level, snap.Uploaded, snap.Downloaded, snap.Bonus, snap.Seeding, snap.BonusPH)
}
