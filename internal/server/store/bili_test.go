package store

import (
	"fmt"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// bili_tasks 队列纪律（TODO 6）：follow_id 活跃唯一、领取原子租出+payload
// 解密、回传幂等、2h 租约超时不重派直接 failed、消费打标只允许首次、
// 按追更/按机器作废、终态清理。
func TestBiliTasks(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f := &BiliFollow{Enabled: true, UpName: "某UP", Address: "合集A", AgentServerID: 7, SaveDir: "/data/bili", AutoEnabled: true}
	if err := s.CreateBiliFollow(f); err != nil {
		t.Fatal(err)
	}
	taskID, err := s.CreateBiliTask(f.ID, "probe", 7, `{"mode":"probe","cookie":"SESSDATA=secret"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBiliTask(f.ID, "all", 7, "{}"); !errors.Is(err, ErrBiliTaskActive) {
		t.Fatalf("同追更重复入队应报 ErrBiliTaskActive，得到 %v", err)
	}

	// 别的机器领不到
	if tasks, _ := s.ClaimBiliTasks(8, 4); len(tasks) != 0 {
		t.Fatalf("非归属机器不应领到任务: %d", len(tasks))
	}
	// 领取：原子租出 + payload 解密
	tasks, err := s.ClaimBiliTasks(7, 4)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("领取失败: %v %d", err, len(tasks))
	}
	if tasks[0].Payload != `{"mode":"probe","cookie":"SESSDATA=secret"}` {
		t.Fatalf("payload 应已解密: %q", tasks[0].Payload)
	}
	if tasks[0].Attempt != 1 || tasks[0].State != "leased" {
		t.Fatalf("租出态异常: %+v", tasks[0])
	}

	// 回传幂等
	first, err := s.FinishBiliTask(taskID, `{"ok":true}`, "")
	if err != nil || !first {
		t.Fatalf("首次回传应成功: %v %v", first, err)
	}
	if first, _ = s.FinishBiliTask(taskID, `{"ok":false}`, "重复"); first {
		t.Fatal("done 后重复回传应丢弃")
	}

	// 消费打标只允许首次
	if ok, _ := s.ConsumeBiliTask(taskID); !ok {
		t.Fatal("首次消费应为真")
	}
	if ok, _ := s.ConsumeBiliTask(taskID); ok {
		t.Fatal("重复消费应为假")
	}

	// 回传后活跃槽已释放：可再入队
	idX, err := s.CreateBiliTask(f.ID, "all", 7, "{}")
	if err != nil {
		t.Fatalf("终态后应可再入队: %v", err)
	}
	s.ExpireBiliTasksByFollow(f.ID) // 作废，给后续租约分流派测试腾活跃槽
	if tsk, _ := s.GetBiliTaskState(idX); tsk.State != "expired" {
		t.Fatalf("应可作废: %s", tsk.State)
	}

	// 租约超时分流派（TODO 4）：probe 秒级任务 3 分钟租约、超时回 pending 重派
	//（旧 agent 领走丢弃的自愈窗口）；run 下载任务 2h 租约、超时直接 failed。
	// —— 先验 probe：
	idP, _ := s.CreateBiliTask(f.ID, "probe", 7, "{}")
	s.ClaimBiliTasks(7, 4)
	s.db.Exec(`UPDATE bili_tasks SET leased_at=? WHERE id=?`, time.Now().Add(-4*time.Minute).Unix(), idP)
	requeued, failed := s.ReclaimExpiredBiliTasks()
	if requeued != 1 || failed != 0 {
		t.Fatalf("probe 超时应回队重派: requeued=%d failed=%d", requeued, failed)
	}
	if tsk, _ := s.GetBiliTask(idP); tsk.State != "pending" || tsk.Attempt != 1 {
		t.Fatalf("probe 应回 pending（attempt 保留 1）: %+v", tsk)
	}
	// 再租出、再超时、直到 attempt 达 3 → failed
	for i := 0; i < 3; i++ {
		s.ClaimBiliTasks(7, 4)
		s.db.Exec(`UPDATE bili_tasks SET leased_at=? WHERE id=? AND state='leased'`, time.Now().Add(-4*time.Minute).Unix(), idP)
		s.ReclaimExpiredBiliTasks()
	}
	if tsk, _ := s.GetBiliTaskState(idP); tsk.State != "failed" {
		t.Fatalf("probe 达重派上限应 failed: %+v", tsk)
	}
	// —— 再验 run：超时不重派直接 failed
	idR, _ := s.CreateBiliTask(f.ID, "all", 7, "{}")
	s.ClaimBiliTasks(7, 4)
	s.db.Exec(`UPDATE bili_tasks SET leased_at=? WHERE id=?`, time.Now().Add(-3*time.Hour).Unix(), idR)
	requeued, failed = s.ReclaimExpiredBiliTasks()
	if requeued != 0 || failed != 1 {
		t.Fatalf("run 超时应直接 failed: requeued=%d failed=%d", requeued, failed)
	}
	if tsk, _ := s.GetBiliTaskState(idR); tsk.State != "failed" {
		t.Fatalf("run 超时应 failed: %s", tsk.State)
	}

	// 按追更作废 + 按机器作废 + 清理
	id2, _ := s.CreateBiliTask(f.ID, "all", 7, "{}")
	s.ExpireBiliTasksByFollow(f.ID)
	if tsk, _ := s.GetBiliTaskState(id2); tsk.State != "expired" {
		t.Fatalf("删除追更应作废任务: %s", tsk.State)
	}
	id3, _ := s.CreateBiliTask(f.ID, "all", 9, "{}")
	s.ExpireBiliTasksByServer(9)
	if tsk, _ := s.GetBiliTaskState(id3); tsk.State != "expired" {
		t.Fatalf("换绑机器应作废任务: %s", tsk.State)
	}
	s.db.Exec(`UPDATE bili_tasks SET updated_at=? WHERE id=?`, time.Now().Add(-8*24*time.Hour).Unix(), id3)
	s.CleanupBiliTasks(7 * 24 * time.Hour)
	if _, err := s.GetBiliTaskState(id3); !errors.Is(err, ErrBiliTaskNotFound) {
		t.Fatalf("终态历史应被清理: %v", err)
	}
}

// bili_follows CRUD 与统计回写：解析回填、自动到期判定、失败只记错不动统计。
func TestBiliFollows(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f := &BiliFollow{Enabled: true, UpName: "某UP", Address: "合集A", AgentServerID: 7, SaveDir: "/data/bili", AutoEnabled: true}
	if err := s.CreateBiliFollow(f); err != nil {
		t.Fatal(err)
	}
	if f.ID == 0 {
		t.Fatal("应回填 ID")
	}
	if err := s.CreateBiliFollow(&BiliFollow{UpName: "x", Address: "", AgentServerID: 7, SaveDir: "/d"}); err == nil {
		t.Fatal("缺地址应报错")
	}

	// 解析回填 + 未解析清单
	if err := s.UpdateBiliFollowResolved(f.ID, 42, "season", 100, "合集A", 5, "某UP 合集A"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetBiliFollow(f.ID)
	if got.Mid != 42 || got.Kind != "season" || got.SubDir != "某UP 合集A" || got.LastError != "" {
		t.Fatalf("解析回填异常: %+v", got)
	}
	if un, _ := s.UnresolvedBiliFollows(); len(un) != 0 {
		t.Fatal("解析后不应在未解析清单")
	}

	// 自动到期：last_auto_date 早于今天才到期；入队打标后不再到期
	today := time.Now().Format("2006-01-02")
	due, _ := s.DueBiliAutoRuns(today)
	if len(due) != 1 {
		t.Fatalf("今天未跑应到期: %d", len(due))
	}
	s.SetBiliAutoDate(f.ID, today)
	if due, _ = s.DueBiliAutoRuns(today); len(due) != 0 {
		t.Fatal("打标后不应再到期")
	}

	// 统计回写：成功带种子完成；失败只记错不动统计
	if err := s.UpdateBiliRunStats(f.ID, &BiliRunStats{OK: true, ItemCount: 10, NewCount: 10, RunAt: 1000, FinishSeed: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetBiliFollow(f.ID)
	if got.ItemCount != 10 || got.LastNewCount != 10 || !got.Seeded || got.LastOKAt != 1000 {
		t.Fatalf("统计回写异常: %+v", got)
	}
	if err := s.UpdateBiliRunStats(f.ID, &BiliRunStats{OK: false, ErrorMsg: "网络炸了", RunAt: 2000}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetBiliFollow(f.ID)
	if got.ItemCount != 10 || got.LastError != "网络炸了" || got.LastOKAt != 1000 {
		t.Fatalf("失败不应动旧统计: %+v", got)
	}

	// 水位推进（只进不退）与停止标记（7/8）
	s.UpdateBiliWatermark(f.ID, 1767225600)
	s.UpdateBiliWatermark(f.ID, 1767225500) // 更小的值不回退
	got, _ = s.GetBiliFollow(f.ID)
	if got.Watermark != 1767225600 {
		t.Fatalf("水位应只进不退: %d", got.Watermark)
	}
	s.SetBiliStopFlag(f.ID, true)
	got, _ = s.GetBiliFollow(f.ID)
	if !got.StopRequested {
		t.Fatal("停止标记应置位")
	}
	if ids, _ := s.StopRequestedBiliFollows(); len(ids) != 1 || ids[0] != f.ID {
		t.Fatalf("停止清单应含该追更: %v", ids)
	}
	s.SetBiliStopFlag(f.ID, false)
	if ids, _ := s.StopRequestedBiliFollows(); len(ids) != 0 {
		t.Fatalf("清除后清单应空: %v", ids)
	}

	// 手动停止作废 pending 任务（8）
	idS, _ := s.CreateBiliTask(f.ID, "all", 7, "{}")
	s.ExpirePendingBiliTask(f.ID)
	if tsk, _ := s.GetBiliTaskState(idS); tsk.State != "expired" {
		t.Fatalf("pending 任务应被停止作废: %s", tsk.State)
	}

	// 编辑 + 启停 + 删除（删除后任务查询按 not found）
	addr := "合集B"
	auto := false
	if err := s.UpdateBiliFollow(f.ID, &BiliFollowUpdate{Address: &addr, AutoEnabled: &auto}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetBiliFollow(f.ID)
	if got.Address != "合集B" || got.AutoEnabled {
		t.Fatalf("编辑异常: %+v", got)
	}
	if err := s.SetBiliFollowEnabled(f.ID, false); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.DueBiliAutoRuns("1999-01-01"); len(due) != 0 {
		t.Fatal("停用不应到期")
	}
	if err := s.DeleteBiliFollow(f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetBiliFollow(f.ID); !errors.Is(err, ErrBiliFollowNotFound) {
		t.Fatalf("删除后应 not found: %v", err)
	}
}

// 16：详情弹层只显最后 10 条——ListBiliItems 按 pubdate 新→旧截取（首抓按清单
// 新→旧插入、id 序与时间序相反，按 id 排会错拿最旧段）；total 由 CountBiliItems 出。
func TestListBiliItemsRecentLimit(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f := &BiliFollow{Enabled: true, UpName: "UP", Address: "合集", AgentServerID: 7, SaveDir: "/d"}
	if err := s.CreateBiliFollow(f); err != nil {
		t.Fatal(err)
	}
	// 模拟首抓：agent 按清单新→旧插入 12 条（pubdate 递减、id 递增）
	items := make([]BiliItemIn, 0, 12)
	for i := 0; i < 12; i++ {
		items = append(items, BiliItemIn{BVID: fmt.Sprintf("BV%012d", i), Title: fmt.Sprintf("视频%d", i), PubDate: int64(1000 - i), FileName: fmt.Sprintf("%d.m4a", i)})
	}
	if n := s.AddBiliItems(f.ID, items); n != 12 {
		t.Fatalf("应新记 12 条，实际 %d", n)
	}
	got, err := s.ListBiliItems(f.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Fatalf("应只回 10 条，实际 %d", len(got))
	}
	if got[0].PubDate != 1000 || got[9].PubDate != 991 {
		t.Fatalf("应按 pubdate 新→旧取最近 10 条，首尾不符: %d..%d", got[0].PubDate, got[9].PubDate)
	}
	if n := s.CountBiliItems(f.ID); n != 12 {
		t.Fatalf("台账总数应 12，实际 %d", n)
	}
}
