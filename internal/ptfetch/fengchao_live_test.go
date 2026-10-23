package ptfetch

import (
	"encoding/json"
	"os"
	"testing"
)

// 真实站点联调探针（465 Cookie 版，默认跳过，不进常规测试面）：
//
//	FENGCHAO_LIVE=1 FENGCHAO_COOKIE='浏览器 F12 整串 Cookie' \
//	  go test ./internal/ptfetch/ -run TestFengchaoLive -v
//
// GET /api/auth/me 一次（user/surface 转储，剔除大段）+ 一次 POST /api/check-in
// （幂等，已签日安全）。BaseURL 留空走目录默认域回落（生产同款入参形态）。
func TestFengchaoLive(t *testing.T) {
	cookie := os.Getenv("FENGCHAO_COOKIE")
	if os.Getenv("FENGCHAO_LIVE") != "1" || cookie == "" {
		t.Skip("需要 FENGCHAO_LIVE=1 与 FENGCHAO_COOKIE（浏览器整串 Cookie）")
	}
	s := Site{SiteKey: "fengchao", BaseURL: "", Cookie: cookie}

	snap, err := FetchSite(s)
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	t.Logf("快照: username=%q level=%q bonus=%v joined=%d fc=%+v", snap.Username, snap.Level, snap.Bonus, snap.JoinedAt, snap.Fc)

	snap2, msg, err := SigninFengchao(s)
	if err != nil {
		t.Fatalf("签到失败: %v", err)
	}
	t.Logf("签到: %s（积分 %v → %v）", msg, snap.Bonus, snap2.Bonus)

	// 会话原始面转储（诊断用：余额/签到字段名实锤）
	_, b, err := fcReq("GET", "https://fengchao.chat"+fcPathSession, cookie, DefaultUA, nil)
	if err == nil {
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(b, &env) == nil {
			var obj map[string]json.RawMessage
			if json.Unmarshal(env.Data, &obj) == nil {
				for _, k := range []string{"preferences", "features"} {
					delete(obj, k)
				}
				if out, err := json.Marshal(obj); err == nil {
					if len(out) > 2500 {
						out = out[:2500]
					}
					t.Logf("会话 data: %s", string(out))
				}
			}
		}
	}
}
