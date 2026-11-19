package ptfetch

import (
	"os"
	"testing"
)

// 真实站点联调探针（416，默认跳过，不进常规测试面）：
//
//	DIAN115_LIVE=1 DIAN115_COOKIE='portal_token=...' go test ./internal/ptfetch/ -run TestDian115Live -v
//
// 只读 /me 一次 + 一次签到尝试（今日已签则返回 already_signed 文案），不碰日历/
// 流水等只读附加接口，保持对站点的请求量与每日轮一致。
func TestDian115Live(t *testing.T) {
	cookie := os.Getenv("DIAN115_COOKIE")
	if os.Getenv("DIAN115_LIVE") != "1" || cookie == "" {
		t.Skip("需要 DIAN115_LIVE=1 与 DIAN115_COOKIE（真实 portal_token）")
	}
	s := Site{SiteKey: "dian115", BaseURL: "", Cookie: cookie} // 空 base_url = 目录默认域（回归 416 真站报错路径）
	snap, err := FetchSite(s)
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	t.Logf("快照: username=%q bonus=%v joined=%d dian=%+v", snap.Username, snap.Bonus, snap.JoinedAt, snap.Dian)
	if snap.Dian != nil && snap.Dian.SignedToday {
		t.Log("今日已签到，跳过签到尝试")
		return
	}
	snap2, msg, err := SigninDian115(s)
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	t.Logf("签到: %s（积分 %v → %v）", msg, snap.Bonus, snap2.Bonus)
}
