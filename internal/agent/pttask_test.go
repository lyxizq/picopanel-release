package agent

import (
	"strings"
	"testing"

	"lz-monitor/internal/ptfetch"
)

// 站点级隔离（443）：单任务执行 panic 就地转成任务失败回传，不带崩 agent、
// 不影响后续任务。
func TestRunTaskPanicIsolated(t *testing.T) {
	_, err := runTask(&PtTask{TaskID: 1, SiteKey: "hdsky"}, func(ptfetch.Site) (*ptfetch.Snapshot, error) {
		panic("解析爆炸")
	})
	if err == nil || !strings.Contains(err.Error(), "内部错误") {
		t.Fatalf("panic 应转为任务错误回传，得到 %v", err)
	}

	snap, err := runTask(&PtTask{TaskID: 2, SiteKey: "pter"}, func(ptfetch.Site) (*ptfetch.Snapshot, error) {
		return &ptfetch.Snapshot{Username: "u", Seeding: -1}, nil
	})
	if err != nil || snap == nil || snap.Username != "u" {
		t.Fatalf("正常任务不应受影响: %v/%v", snap, err)
	}
}
