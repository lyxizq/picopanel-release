package agent

import (
	"testing"

	"lz-monitor/internal/bilibili"
)

// validateRunTarget 参数校验（现场误拦修复回归）：投稿形态无列表 id 必须放行。
func TestValidateRunTarget(t *testing.T) {
	if err := validateRunTarget(&bilibili.Target{Mid: 24490535, Kind: "uploads", Title: "投稿"}); err != nil {
		t.Fatalf("uploads 形态不应被拦: %v", err)
	}
	if err := validateRunTarget(&bilibili.Target{Mid: 42, Kind: "season", ListID: 100, Title: "合集"}); err != nil {
		t.Fatalf("合集形态应放行: %v", err)
	}
	if err := validateRunTarget(&bilibili.Target{Mid: 42, Kind: "series", ListID: 0, Title: "x"}); err == nil {
		t.Fatal("非投稿形态缺列表 id 应拦")
	}
	if err := validateRunTarget(&bilibili.Target{Kind: "season", ListID: 1}); err == nil {
		t.Fatal("缺 mid 应拦")
	}
}

// 服务端下发 SubDir 的本地清洗防回归（2026-10-23 安全加固）：多段/上跳/
// 绝对路径一律压成单段或回落空（由调用方本地生成）。
func TestSanitizeSubDir(t *testing.T) {
	cases := map[string]string{
		"合集名":         "合集名",
		"正常名":         "正常名",
		"etc/cron.d":  "cron.d",
		"/etc/cron.d": "cron.d",
		"../../etc":   "etc",
		"..":          "",
		".":           "",
		"":            "",
		"a/../..":     "",
		"up主\\清单名":    "up主\\清单名",
	}
	for in, want := range cases {
		if got := sanitizeSubDir(in); got != want {
			t.Errorf("sanitizeSubDir(%q)=%q want %q", in, got, want)
		}
	}
}
