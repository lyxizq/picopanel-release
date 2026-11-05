package api

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"lz-monitor/internal/server/ui"
)

// TestCSPInlineScriptHashes 内嵌 index.html 里的每个内联脚本，其内容哈希必须
// 在 cspHeader 白名单内：script-src 'self' 下白名单外的内联脚本会被浏览器
// 静默拦截（首屏主题脚本失效只闪一下，肉眼难察觉）。改 web/index.html 的
// 内联脚本而忘了同步 cspHeader 时，本测试在构建期红灯。
func TestCSPInlineScriptHashes(t *testing.T) {
	data, err := fs.ReadFile(ui.Dist(), "dist/index.html")
	if err != nil {
		t.Skipf("前端产物未构建（make web 后重跑）：%v", err)
	}
	re := regexp.MustCompile(`(?s)<script([^>]*)>(.*?)</script>`)
	n := 0
	for _, m := range re.FindAllSubmatch(data, -1) {
		attrs := string(m[1])
		if strings.Contains(attrs, "src=") {
			continue // 外链脚本不受内联哈希约束
		}
		n++
		sum := sha256.Sum256(m[2])
		hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !strings.Contains(cspHeader, hash) {
			t.Errorf("index.html 内联脚本哈希 %s 不在 cspHeader 白名单内：更新 web/index.html 后须同步 router.go 的 cspHeader", hash)
		}
	}
	if n == 0 {
		t.Error("index.html 未找到内联脚本（构建产物形态变化？），CSP 白名单校验落空")
	}
}
