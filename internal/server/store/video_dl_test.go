package store

import (
	"errors"
	"testing"
	"time"
)

// 视频下载任务队列纪律（TODO 4）：任务一次性、领取原子租出、Finish 幂等、
// 进度仅 leased 可写、同 URL 活跃唯一、租约超时 failed、终态清理与删除。
func TestVideoDlTaskQueue(t *testing.T) {
	s := openTestStore(t)

	// server 流派领取：serverID=0、executor=server
	id1, err := s.CreateVideoDlTask("https://www.bilibili.com/video/BV1xx", "/data/v", "server", 0, "")
	if err != nil || id1 <= 0 {
		t.Fatalf("server 任务入队: id=%d err=%v", id1, err)
	}
	// 同 URL 活跃唯一
	if _, err := s.CreateVideoDlTask("https://www.bilibili.com/video/BV1xx", "/data/v", "server", 0, ""); !errors.Is(err, ErrVideoDlActive) {
		t.Fatalf("同 URL 活跃应拒绝，got %v", err)
	}
	// agent 流派领取：serverID 归属校验
	id2, err := s.CreateVideoDlTask("https://v.douyin.com/abc", "/data/d", "agent", 7, "SET-COOKIE-CONTENT")
	if err != nil {
		t.Fatalf("agent 任务入队: %v", err)
	}

	// 领取流派隔离：server 执行器领不到 agent 任务，反之亦然
	if got, _ := s.ClaimVideoDlTasks("server", 0, 10); len(got) != 1 || got[0].ID != id1 {
		t.Fatalf("server 领取应得 id1，got %+v", got)
	}
	claimed, err := s.ClaimVideoDlTasks("agent", 7, 10)
	if err != nil || len(claimed) != 1 || claimed[0].ID != id2 {
		t.Fatalf("agent 领取应得 id2: %v %+v", err, claimed)
	}
	if claimed[0].Cookies != "SET-COOKIE-CONTENT" {
		t.Fatalf("领取应解密 Cookies，got %q", claimed[0].Cookies)
	}
	// 别的机器领不到
	if got, _ := s.ClaimVideoDlTasks("agent", 8, 10); len(got) != 0 {
		t.Fatalf("他机不应领取到任务: %+v", got)
	}
	// 重复领取拿不到（已 leased）
	if got, _ := s.ClaimVideoDlTasks("agent", 7, 10); len(got) != 0 {
		t.Fatalf("leased 任务不应被重复领取: %+v", got)
	}

	// 进度回写：leased 可写、标题随心跳补齐；终态后拒绝
	if err := s.UpdateVideoDlProgress(id2, 42, "1.5MiB/s", "测试视频"); err != nil {
		t.Fatalf("进度回写: %v", err)
	}
	row, _ := s.GetVideoDlTaskState(id2)
	if row.Progress != 42 || row.Title != "测试视频" || row.Speed != "1.5MiB/s" {
		t.Fatalf("进度行不符: %+v", row)
	}
	if err := s.UpdateVideoDlProgress(id2, 50, "", ""); err != nil {
		t.Fatalf("二次进度回写: %v", err)
	}

	// 停止链路：置标记 → 查询命中 → Finish 清标记
	if !s.RequestVideoDlStop(id2) {
		t.Fatal("leased 任务应可置停止标记")
	}
	if ids, _ := s.StoppedVideoDlTasks(7); len(ids) != 1 || ids[0] != id2 {
		t.Fatalf("StoppedVideoDlTasks 应命中: %v", ids)
	}
	if ok, _ := s.VideoDlStopRequested(id2); !ok {
		t.Fatal("停止标记应已置位")
	}

	// Finish 幂等：仅 leased 接受，首回 true；成功置 done/100%/标题补齐/清停止标记
	first, err := s.FinishVideoDlTask(id2, "", "/data/d/测试视频.mp4", 12345, "")
	if err != nil || !first {
		t.Fatalf("首次 Finish 应成功: %v %v", first, err)
	}
	again, _ := s.FinishVideoDlTask(id2, "", "/data/d/测试视频.mp4", 12345, "")
	if again {
		t.Fatal("重复 Finish 应丢弃")
	}
	row, _ = s.GetVideoDlTaskState(id2)
	if row.State != "done" || row.Progress != 100 || row.FileName != "/data/d/测试视频.mp4" || row.FileSize != 12345 || row.Title != "测试视频" {
		t.Fatalf("终态行不符: %+v", row)
	}
	if ok, _ := s.VideoDlStopRequested(id2); ok {
		t.Fatal("Finish 应清停止标记")
	}
	// 终态后进度不可再写（leased 条件不命中，静默无效）
	_ = s.UpdateVideoDlProgress(id2, 90, "", "")
	if row2, _ := s.GetVideoDlTaskState(id2); row2.Progress != 100 {
		t.Fatalf("终态后进度不应可写: %+v", row2)
	}

	// 失败终态：错误信息落库
	if _, err := s.CreateVideoDlTask("https://example.com/v.mp4", "/data/v", "server", 0, ""); err != nil {
		t.Fatalf("第二条入队: %v", err)
	}
	list, _ := s.ListVideoDlTasks(10)
	var id3 int64
	for _, r := range list {
		if r.URL == "https://example.com/v.mp4" {
			id3 = r.ID
		}
	}
	if _, err := s.ClaimVideoDlTasks("server", 0, 10); err != nil {
		t.Fatalf("server 再领取: %v", err)
	}
	if ok, _ := s.FinishVideoDlTask(id3, "", "", 0, "执行机器未安装 yt-dlp"); !ok {
		t.Fatal("失败 Finish 应首次成功")
	}
	row3, _ := s.GetVideoDlTaskState(id3)
	if row3.State != "failed" || row3.Error != "执行机器未安装 yt-dlp" || row3.Progress != 0 {
		t.Fatalf("failed 行不符: %+v", row3)
	}

	// 停止 pending：作废终态
	id4, _ := s.CreateVideoDlTask("https://example.com/p4", "/data/v", "server", 0, "")
	s.ExpirePendingVideoDlTask(id4)
	if row4, _ := s.GetVideoDlTaskState(id4); row4.State != "expired" {
		t.Fatalf("pending 停止应作废: %+v", row4)
	}

	// 活跃任务删除应拒绝；终态可删；删后同 URL 可重新入队
	if err := s.DeleteVideoDlTask(id1); err == nil {
		t.Fatal("leased 任务不应可删")
	}
	if err := s.DeleteVideoDlTask(id2); err != nil {
		t.Fatalf("终态删除: %v", err)
	}
	if _, err := s.CreateVideoDlTask("https://v.douyin.com/abc", "/data/d", "agent", 7, ""); err != nil {
		t.Fatalf("终态删除后同 URL 应可重入队: %v", err)
	}

	// 机器删除作废 pending（agent 流派）
	id5, _ := s.CreateVideoDlTask("https://example.com/p5", "/data/v", "agent", 7, "")
	s.ExpireVideoDlTasksByServer(7)
	if row5, _ := s.GetVideoDlTaskState(id5); row5.State != "expired" {
		t.Fatalf("机器删除应作废 pending: %+v", row5)
	}

	// 终态清理：只清终态行（负窗口=全部清理，规避同秒 updated_at 边界）；
	// 残留的活跃行先手动推终态
	if _, err := s.db.Exec(`UPDATE video_dl_tasks SET state='done' WHERE state IN ('pending','leased')`); err != nil {
		t.Fatal(err)
	}
	s.CleanupVideoDlTasks(-time.Hour)
	if list, _ := s.ListVideoDlTasks(10); len(list) != 0 {
		t.Fatalf("清理后应剩 0 条: %d", len(list))
	}
}

// 租约超时回收：leased 超 6h 直接 failed（不重派——下载重派无幂等收益）。
func TestVideoDlReclaimExpired(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateVideoDlTask("https://example.com/slow.mp4", "/data/v", "server", 0, "")
	if _, err := s.ClaimVideoDlTasks("server", 0, 10); err != nil {
		t.Fatal(err)
	}
	// 未超时不动
	if n := s.ReclaimExpiredVideoDlTasks(); n != 0 {
		t.Fatalf("未超时不应回收: %d", n)
	}
	if row, _ := s.GetVideoDlTaskState(id); row.State != "leased" {
		t.Fatalf("应保持 leased: %+v", row)
	}
	// 拨快租约时钟到超时线外
	if _, err := s.db.Exec(`UPDATE video_dl_tasks SET leased_at = leased_at - 21601 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if n := s.ReclaimExpiredVideoDlTasks(); n != 1 {
		t.Fatalf("超时应回收 1 条: %d", n)
	}
	row, _ := s.GetVideoDlTaskState(id)
	if row.State != "failed" || row.Error == "" {
		t.Fatalf("回收应转 failed: %+v", row)
	}
}
