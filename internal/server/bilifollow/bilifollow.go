// Package bilifollow B 站追更（TODO 6）：server 侧调度与消费。抓取全程跑在
// push agent 机器上（用户指定）——本包只负责：SESSDATA 设置、任务载荷组装
// 入队、每日 4 点自动追更对表（错过补跑）、probe/run 结果消费回写。任务队列
// 纪律见 store/bili_task.go（2h 长租约，下载分钟级）。
package bilifollow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"lz-monitor/internal/bilibili"
	"lz-monitor/internal/proto"
	"lz-monitor/internal/server/store"
)

const (
	tickInterval = 30 * time.Second
	autoHour     = 4 // 每天凌晨 4 点自动追更（用户指定）
	keepTasks    = 7 * 24 * time.Hour
)

// Sessdata 读 SESSDATA Cookie（settings 存储；空=未登录音质）。
func Sessdata(st *store.Store) string {
	v, ok, err := st.GetSetting("bili_sessdata")
	if err != nil || !ok {
		return ""
	}
	return v
}

// SetSessdata 写 SESSDATA（空串=清除）。
func SetSessdata(st *store.Store, v string) error {
	return st.SetSetting("bili_sessdata", v)
}

// payload 组任务载荷（含 SESSDATA，落库加密）；run 任务携带服务端已抓 bvid
// 清单（TODO 3：判重权威在服务端，agent 不扫目录）。清单过千条约 20KB，加密
// 落库无压力。
func payload(st *store.Store, f *store.BiliFollow, mode, sessdata string) string {
	t := proto.BiliTask{
		FollowID: f.ID, Mode: mode,
		Mid: f.Mid, Kind: f.Kind, ListID: f.ListID,
		UpName: f.UpName, ListTitle: f.ListTitle,
		Address: f.Address, SaveDir: f.SaveDir, SubDir: f.SubDir,
		Cookie: sessdata,
	}
	if mode != proto.BiliTaskProbe {
		t.Watermark = f.Watermark // 水位增量（7：只抓比已见最新的更新的）
		if bvids, err := st.FetchedBVIDs(f.ID); err == nil {
			t.FetchedBVIDs = bvids
		}
	}
	b, _ := json.Marshal(t)
	return string(b)
}

// EnqueueProbe 地址解析任务（添加/改地址时调用；返回 task_id 供前端轮询）。
func EnqueueProbe(st *store.Store, f *store.BiliFollow) (int64, error) {
	return st.CreateBiliTask(f.ID, proto.BiliTaskProbe, f.AgentServerID, payload(st, f, proto.BiliTaskProbe, ""))
}

// EnqueueRun 追更任务：未种子化走 seed10（首抓最新 10 条），之后全量增量。
func EnqueueRun(st *store.Store, f *store.BiliFollow) (int64, error) {
	mode := proto.BiliTaskAll
	if !f.Seeded {
		mode = proto.BiliTaskSeed10
	}
	return st.CreateBiliTask(f.ID, mode, f.AgentServerID, payload(st, f, mode, Sessdata(st)))
}

// Run 阻塞循环：租约回收 + 每日 4 点自动追更对表 + 孤儿 probe 补消费，
// 终态历史任务每小时清一次。
func Run(ctx context.Context, st *store.Store) {
	sweepAuto(st, time.Now())
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	cleanups := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if requeued, failed := st.ReclaimExpiredBiliTasks(); requeued > 0 || failed > 0 {
				log.Printf("B站追更任务租约回收：回队重派 %d 个、转终态 %d 个", requeued, failed)
			}
			sweepAuto(st, time.Now())
			sweepProbes(st)
			cleanups++
			if cleanups >= 120 { // 30s × 120 ≈ 每小时
				cleanups = 0
				st.CleanupBiliTasks(keepTasks)
			}
		}
	}
}

// sweepAuto 自动追更对表（巡检推送同口径：越过当天 4 点且当日未跑才跑；
// 服务器错过 4 点、稍后启动也能补上）。入队即打日期标，撞活跃任务不重试
//（下一 tick last_auto_date 已打标，当日不再入队）。now 注入便于单测。
func sweepAuto(st *store.Store, now time.Time) {
	if now.Hour() < autoHour {
		return
	}
	today := now.Format("2006-01-02")
	due, err := st.DueBiliAutoRuns(today)
	if err != nil {
		log.Printf("B站追更到期扫描失败: %v", err)
		return
	}
	for i := range due {
		f := &due[i]
		st.SetBiliAutoDate(f.ID, today)
		if _, err := EnqueueRun(st, f); err != nil {
			if !errors.Is(err, store.ErrBiliTaskActive) {
				log.Printf("B站追更自动任务入队失败 (follow=%d %s): %v", f.ID, f.ListTitle, err)
			}
		} else {
			log.Printf("B站追更自动任务已入队 (follow=%d %s·%s)", f.ID, f.UpName, f.ListTitle)
		}
	}
}

// sweepProbes 孤儿 probe 补消费：添加弹层没等解析完成就关掉时，行一直
// mid=0——调度器每轮把已完成未消费的 probe 解析落库。
func sweepProbes(st *store.Store) {
	rows, err := st.UnresolvedBiliFollows()
	if err != nil {
		return
	}
	for i := range rows {
		f := &rows[i]
		task, err := st.ActiveBiliTaskByFollow(f.ID)
		if err != nil || task != nil {
			continue // 有活跃任务：等回传
		}
		done, err := st.DoneBiliTaskByFollow(f.ID)
		if err != nil || done == nil || done.Mode != proto.BiliTaskProbe {
			continue
		}
		consumeProbe(st, done)
	}
}

// consumeProbe probe 结果消费（幂等）：解析字段回填行或落错。任务行按 ID
// 重取终态行（调用方快照可能取于回传前，result 为空）。
func consumeProbe(st *store.Store, task *store.BiliTask) {
	first, err := st.ConsumeBiliTask(task.ID)
	if err != nil || !first {
		return
	}
	full, err := st.GetBiliTask(task.ID)
	if err != nil {
		return
	}
	task = full
	var res proto.BiliTaskResult
	if json.Unmarshal([]byte(task.Result), &res) != nil || res.Target == nil {
		msg := "解析失败：" + taskMessage(task)
		log.Printf("B站追更地址解析失败 (follow=%d): %s", task.FollowID, msg)
		st.SetBiliFollowResolveError(task.FollowID, msg)
		return
	}
	tg := res.Target
	if err := st.UpdateBiliFollowResolved(task.FollowID, tg.Mid, tg.Kind, tg.ListID, tg.Title, tg.Total, subDirFor(st, task.FollowID, tg)); err != nil {
		log.Printf("B站追更解析回写失败 (follow=%d): %v", task.FollowID, err)
	}
}

// subDirFor 子目录名 = up 主名 + 合集名（用户口径；up 名优先用解析到的精确名）。
func subDirFor(st *store.Store, followID int64, tg *proto.BiliTarget) string {
	up := tg.UpName
	if up == "" {
		if f, err := st.GetBiliFollow(followID); err == nil {
			up = f.UpName
		}
	}
	return bilibili.SubDirName(up, tg.Title)
}

// taskMessage 回传错误摘要。
func taskMessage(t *store.BiliTask) string {
	if t.Error != "" {
		return t.Error
	}
	return "执行机器无返回"
}

// ConsumeResult 追更结果消费（幂等，回传端点与轮询端点共用）：条目记账入
// bili_items（downloaded+exists 都记——exists 是中断重跑时文件已存在跳过的
// 事实，补记防每轮空转重试；TODO 3 判重权威在服务端），统计回写行；失败只记
// last_error 不动旧统计。任务行按 ID 重取终态行（调用方快照可能取于回传前，
// result 为空）。
func ConsumeResult(st *store.Store, task *store.BiliTask) error {
	first, err := st.ConsumeBiliTask(task.ID)
	if err != nil || !first {
		return err
	}
	full, err := st.GetBiliTask(task.ID)
	if err != nil {
		return err
	}
	task = full
	var res proto.BiliTaskResult
	if json.Unmarshal([]byte(task.Result), &res) != nil {
		res = proto.BiliTaskResult{OK: false, Error: taskMessage(task)}
	}
	if task.Error != "" && res.Error == "" {
		res.Error = task.Error
	}
	// 记账：downloaded 与 exists 都入 bili_items（幂等，UNIQUE 忽略重复）；
	// 水位只按成功条目（downloaded/exists）推进——失败条目比水位新的下次自动
	// 重试、比水位旧的按「不补旧」口径放弃（7）
	recorded := 0
	watermark := int64(0)
	if res.OK {
		items := make([]store.BiliItemIn, 0, len(res.Items))
		for _, it := range res.Items {
			if it.Status != "" && it.Status != "downloaded" && it.Status != "exists" {
				continue
			}
			items = append(items, store.BiliItemIn{BVID: it.BVID, Title: it.Title, PubDate: it.PubDate, FileName: it.FileName, Status: it.Status})
			if it.PubDate > watermark {
				watermark = it.PubDate
			}
		}
		recorded = st.AddBiliItems(task.FollowID, items)
	}
	downloaded := 0
	latest := make([]proto.BiliItem, 0, len(res.Items))
	for _, it := range res.Items {
		if it.Status == "downloaded" {
			downloaded++
			if len(latest) < 50 {
				latest = append(latest, it)
			}
		}
	}
	if res.OK && watermark > 0 {
		st.UpdateBiliWatermark(task.FollowID, watermark)
	}
	if res.Stopped {
		st.SetBiliStopFlag(task.FollowID, false) // 停止指令已消费（8）
	}
	itemsJSON := ""
	if b, merr := json.Marshal(latest); merr == nil {
		itemsJSON = string(b)
	}
	stats := &store.BiliRunStats{
		OK:        res.OK && res.Error == "",
		ItemCount: st.CountBiliItems(task.FollowID), // 已抓条目数=服务端记录数（TODO 3 口径）
		NewCount:  downloaded,
		ItemsJSON: itemsJSON,
		RunAt:     time.Now().Unix(),
	}
	if res.Stopped {
		stats.ErrorMsg = "" // 手动停止不算失败（已完成部分已记账，下次追更继续）
	} else if !stats.OK {
		stats.ErrorMsg = "追更失败：" + res.Error
	}
	stats.FinishSeed = task.Mode == proto.BiliTaskSeed10 && stats.OK
	if err := st.UpdateBiliRunStats(task.FollowID, stats); err != nil {
		return fmt.Errorf("追更统计回写失败: %w", err)
	}
	log.Printf("B站追更记账 (follow=%d): 回传 %d 条、新记 %d 条", task.FollowID, len(res.Items), recorded)
	return nil
}

// ConsumeTask 统一消费入口（api 层调用）：按任务形态分派 probe/run。
func ConsumeTask(st *store.Store, task *store.BiliTask) error {
	if task.Mode == proto.BiliTaskProbe {
		consumeProbe(st, task)
		return nil
	}
	return ConsumeResult(st, task)
}
