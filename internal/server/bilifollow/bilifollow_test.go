package bilifollow

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
)

func openTest(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestEnqueueRunMode 未种子化走 seed10，种子化后走全量。
func TestEnqueueRunMode(t *testing.T) {
	st := openTest(t)
	f := &store.BiliFollow{Enabled: true, UpName: "某UP", Address: "合集A", AgentServerID: 7,
		SaveDir: "/data/bili", AutoEnabled: true}
	if err := st.CreateBiliFollow(f); err != nil {
		t.Fatal(err)
	}
	// 解析字段由 probe 回填（Create 不落），测试同口径先回填再入队
	if err := st.UpdateBiliFollowResolved(f.ID, 42, "season", 100, "合集A", 5, "某UP 合集A"); err != nil {
		t.Fatal(err)
	}
	id1, err := EnqueueRun(st, f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimBiliTasks(7, 4); err != nil { // 租出后回传才生效
		t.Fatal(err)
	}
	task1, _ := st.GetBiliTask(id1)
	if task1.Mode != proto.BiliTaskSeed10 {
		t.Fatalf("未种子化应 seed10: %s", task1.Mode)
	}
	if task1.Payload == "" {
		t.Fatal("payload 不应为空")
	}
	st.FinishBiliTask(id1, `{"ok":true,"file_count":10,"downloaded":10}`, "")
	if err := ConsumeTask(st, task1); err != nil {
		t.Fatal(err)
	}
	fresh, _ := st.GetBiliFollow(f.ID)
	if !fresh.Seeded {
		t.Fatal("seed10 成功后应转种子化")
	}
	// 种子化后全量
	id2, err := EnqueueRun(st, fresh)
	if err != nil {
		t.Fatal(err)
	}
	task2, _ := st.GetBiliTask(id2)
	if task2.Mode != proto.BiliTaskAll {
		t.Fatalf("种子化后应 all: %s", task2.Mode)
	}
}

// TestConsumeRunResult 记账与统计回写（TODO 3 口径）：downloaded+exists 都入
// bili_items、item_count=服务端记录数、last_new_count=本次 downloaded 数、
// 幂等重复消费不再变。
func TestConsumeRunResult(t *testing.T) {
	st := openTest(t)
	f := &store.BiliFollow{Enabled: true, UpName: "某UP", Address: "合集A", AgentServerID: 7,
		SaveDir: "/data/bili"}
	if err := st.CreateBiliFollow(f); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateBiliFollowResolved(f.ID, 42, "season", 100, "合集A", 22, "某UP 合集A"); err != nil {
		t.Fatal(err)
	}
	// 预置一条服务端已抓记录（payload 清单口径验证用）
	st.AddBiliItems(f.ID, []store.BiliItemIn{{BVID: "BV0", Title: "旧的", FileName: "20260101_旧的.m4a"}})
	fresh, _ := st.GetBiliFollow(f.ID)
	taskID, err := EnqueueRun(st, fresh) // 已回填未种子化 → seed10，payload 携带清单
	if err != nil {
		t.Fatal(err)
	}
	tasks, _ := st.ClaimBiliTasks(7, 4)
	if len(tasks) != 1 {
		t.Fatal("前置领取失败")
	}
	if !strings.Contains(tasks[0].Payload, `"fetched_bvids":["BV0"]`) {
		t.Fatalf("payload 应携带服务端已抓清单: %s", tasks[0].Payload)
	}
	st.FinishBiliTask(taskID, `{"ok":true,"total":22,"file_count":22,"downloaded":1,"items":[
		{"bvid":"BV1","title":"新视频","pubdate":1767225600,"file_name":"20260102_新视频.m4a","status":"downloaded"},
		{"bvid":"BV2","title":"补记的","pubdate":1767139200,"file_name":"20260101_补记的.m4a","status":"exists"}]}`, "")
	task, _ := st.GetBiliTask(taskID)
	if err := ConsumeTask(st, task); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetBiliFollow(f.ID)
	if got.ItemCount != 3 || got.LastNewCount != 1 || got.LastError != "" {
		t.Fatalf("统计回写异常（应 3=1 旧记录+downloaded+exists 记账, 新增 1）: %+v", got)
	}
	if bvids, _ := st.FetchedBVIDs(f.ID); len(bvids) != 3 {
		t.Fatalf("记账后清单应 3 条: %v", bvids)
	}
	records, _ := st.ListBiliItems(f.ID, 0)
	// 16 起 ListBiliItems 按 pubdate 新→旧：BV1（最新发布）在前；补记账意图由 len==3 覆盖（缺 BV2 补记则只有 2 条）
	if len(records) != 3 || records[0].BVID != "BV1" || records[0].FileName != "20260102_新视频.m4a" {
		t.Fatalf("exists 事实应补记账: %+v", records)
	}
	// 重复消费：统计不再变（幂等）
	if err := ConsumeTask(st, task); err != nil {
		t.Fatal(err)
	}
	if n := st.CountBiliItems(f.ID); n != 3 {
		t.Fatalf("重复消费不应重复记账: %d", n)
	}
	// 水位推进（7）：成功条目（downloaded+exists）的最大 pubdate
	got, _ = st.GetBiliFollow(f.ID)
	if got.Watermark != 1767225600 {
		t.Fatalf("水位应推进到成功条目最大 pubdate: %d", got.Watermark)
	}
	// payload 应携带已推进的水位（下次只抓比它新的）
	idN, _ := EnqueueRun(st, got)
	nt, _ := st.GetBiliTask(idN)
	if !strings.Contains(nt.Payload, `"watermark":1767225600`) {
		t.Fatalf("payload 应携带水位: %s", nt.Payload)
	}
}

// TestConsumeStopped 手动停止回传：已完成部分正常记账、清停止标记、不算失败。
func TestConsumeStopped(t *testing.T) {
	st := openTest(t)
	f := &store.BiliFollow{Enabled: true, UpName: "某UP", Address: "合集A", AgentServerID: 7, SaveDir: "/d"}
	if err := st.CreateBiliFollow(f); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateBiliFollowResolved(f.ID, 42, "season", 100, "合集A", 22, "某UP 合集A"); err != nil {
		t.Fatal(err)
	}
	st.SetBiliStopFlag(f.ID, true)
	id, _ := EnqueueRun(st, mustFollow(t, st, f.ID))
	st.ClaimBiliTasks(7, 4)
	st.FinishBiliTask(id, `{"ok":true,"stopped":true,"total":22,"downloaded":1,"file_count":1,"items":[{"bvid":"BV1","title":"完成的一条","pubdate":1767225600,"file_name":"x.m4a","status":"downloaded"}]}`, "")
	task, _ := st.GetBiliTask(id)
	if err := ConsumeTask(st, task); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetBiliFollow(f.ID)
	if got.LastError != "" || got.ItemCount != 1 {
		t.Fatalf("停止不应算失败且应记账: %+v", got)
	}
	if got.StopRequested {
		t.Fatal("回传 stopped 后应清停止标记")
	}
	if got.Watermark != 1767225600 {
		t.Fatalf("水位应按已完成条目推进: %d", got.Watermark)
	}
}

func mustFollow(t *testing.T, st *store.Store, id int64) *store.BiliFollow {
	t.Helper()
	f, err := st.GetBiliFollow(id)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestConsumeProbe 轮询/调度消费 probe：解析字段回填；失败落错。
func TestConsumeProbe(t *testing.T) {
	st := openTest(t)
	f := &store.BiliFollow{Enabled: true, UpName: "某UP", Address: "合集A", AgentServerID: 7, SaveDir: "/data/bili"}
	if err := st.CreateBiliFollow(f); err != nil {
		t.Fatal(err)
	}
	taskID, err := EnqueueProbe(st, f)
	if err != nil {
		t.Fatal(err)
	}
	st.ClaimBiliTasks(7, 4)
	st.FinishBiliTask(taskID, `{"ok":true,"target":{"mid":42,"kind":"season","list_id":100,"title":"合集A","total":5,"up_name":"真名UP"}}`, "")
	task, _ := st.GetBiliTask(taskID)
	// 轮询端消费
	if err := ConsumeTask(st, task); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetBiliFollow(f.ID)
	if got.Mid != 42 || got.ListID != 100 || got.ListTitle != "合集A" || got.SubDir == "" {
		t.Fatalf("解析回填异常: %+v", got)
	}
	// 失败路径：新行 + 错误回传 → 落 last_error
	f2 := &store.BiliFollow{Enabled: true, UpName: "别UP", Address: "没这合集", AgentServerID: 7, SaveDir: "/d"}
	st.CreateBiliFollow(f2)
	id2, _ := EnqueueProbe(st, f2)
	st.ClaimBiliTasks(7, 4)
	st.FinishBiliTask(id2, "", "没找到名为「没这合集」的合集")
	task2, _ := st.GetBiliTask(id2)
	if err := ConsumeTask(st, task2); err != nil {
		t.Fatal(err)
	}
	got2, _ := st.GetBiliFollow(f2.ID)
	if got2.LastError == "" || got2.Mid != 0 {
		t.Fatalf("解析失败应落错且保持未解析: %+v", got2)
	}
}

// TestSweepAuto 每日 4 点对表：4 点前不跑、过后当日一次、打标后当日不再。
func TestSweepAuto(t *testing.T) {
	st := openTest(t)
	f := &store.BiliFollow{Enabled: true, UpName: "某UP", Address: "合集A", AgentServerID: 7,
		SaveDir: "/data/bili", AutoEnabled: true}
	if err := st.CreateBiliFollow(f); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateBiliFollowResolved(f.ID, 42, "season", 100, "合集A", 22, "某UP 合集A"); err != nil {
		t.Fatal(err)
	}
	at11 := time.Date(2026, 10, 18, 11, 0, 0, 0, time.Local)
	sweepAuto(st, at11)
	tasks, _ := st.ClaimBiliTasks(7, 4)
	if len(tasks) != 1 || tasks[0].Mode != proto.BiliTaskSeed10 {
		t.Fatalf("11 点应入队 seed10 任务: %d", len(tasks))
	}
	// 打标后同日不再入队（清掉领取态再扫也无新任务）
	st.FinishBiliTask(tasks[0].ID, `{"ok":true}`, "")
	sweepAuto(st, at11)
	if more, _ := st.ClaimBiliTasks(7, 4); len(more) != 0 {
		t.Fatalf("同日不应重复入队: %d", len(more))
	}
	// 次日 3 点：不跑；次日 5 点：补跑
	sweepAuto(st, at11.Add(16*time.Hour)) // 次日 03:00
	if more, _ := st.ClaimBiliTasks(7, 4); len(more) != 0 {
		t.Fatal("4 点前不应入队")
	}
	sweepAuto(st, at11.Add(18*time.Hour)) // 次日 05:00
	if more, _ := st.ClaimBiliTasks(7, 4); len(more) != 1 {
		t.Fatal("次日应再入队一次")
	}
}

// TestSweepProbes 孤儿 probe 补消费：没轮询的解析任务由调度器落库。
func TestSweepProbes(t *testing.T) {
	st := openTest(t)
	f := &store.BiliFollow{Enabled: true, UpName: "某UP", Address: "合集A", AgentServerID: 7, SaveDir: "/d"}
	if err := st.CreateBiliFollow(f); err != nil {
		t.Fatal(err)
	}
	id, _ := EnqueueProbe(st, f)
	tasks, _ := st.ClaimBiliTasks(7, 4)
	st.FinishBiliTask(id, `{"ok":true,"target":{"mid":9,"kind":"series","list_id":8,"title":"列表","total":3}}`, "")
	sweepProbes(st)
	got, _ := st.GetBiliFollow(f.ID)
	if got.Mid != 9 || got.Kind != "series" || got.ListID != 8 {
		t.Fatalf("孤儿 probe 未被调度器消费: %+v", got)
	}
	if len(tasks) != 1 {
		t.Fatal("前置领取失败")
	}
}
