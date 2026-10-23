package ptfetch

import (
	"os"
	"strings"
	"testing"
)

// 真实站点联调探针（432，默认跳过，不进常规测试面）：
//
//	HDHAVEN_LIVE=1 HDHAVEN_COOKIE='hdh_session=...' go test ./internal/ptfetch/ -run TestHDHavenLive -v
//
// 只读 /api/auth/me 一次 + 一次签到尝试（今日已签则直接跳过），与每日轮同量。
// cookie 取浏览器 F12 → 应用 → Cookie 里的 hdh_session=...（整串粘贴亦可）。
func TestHDHavenLive(t *testing.T) {
	cookie := os.Getenv("HDHAVEN_COOKIE")
	if os.Getenv("HDHAVEN_LIVE") != "1" || cookie == "" {
		t.Skip("需要 HDHAVEN_LIVE=1 与 HDHAVEN_COOKIE（真实 hdh_session）")
	}
	s := Site{SiteKey: "hdhaven", BaseURL: "", Cookie: cookie} // 空 base_url = 目录默认域（生产同款入参形态）
	snap, err := FetchSite(s)
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	t.Logf("快照: username=%q level=%q bonus=%v hdh=%+v", snap.Username, snap.Level, snap.Bonus, snap.Hdh)
	if snap.Hdh != nil && snap.Hdh.SignedToday {
		t.Log("今日已签到，跳过签到尝试")
		return
	}
	snap2, msg, err := SigninHDHaven(s)
	if err != nil {
		if strings.Contains(err.Error(), "409") || strings.Contains(err.Error(), "已签到") {
			t.Logf("签到并发撞签（视为正常）: %v", err)
			return
		}
		t.Fatalf("签到失败: %v", err)
	}
	t.Logf("签到: %s（积分 %v → %v）", msg, snap.Bonus, snap2.Bonus)
}
