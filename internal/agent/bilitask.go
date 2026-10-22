package agent

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/bilibili"
	"lz-monitor/internal/proto"
)

// B 站追更任务执行（TODO 6）：server 入队（probe 解析 / seed10 首抓最新 10 条 /
// all 全量增量），agent 机器上下载音频转 m4a 落盘。与中转任务的关键差别：
// 一单要下几分钟，绝不能在上报循环里同步跑（心跳会被拖死、服务端长租约也没
// 意义）——单 worker goroutine 串行消费队列，结果独立回传；agent 重启丢任务
// 由服务端 2h 租约超时转 failed，文件断点续抓语义下重试自然续传。

// biliStopFlags 服务端请求停止的 follow_id 集合（8）：上报响应捎带置位，
// run 下载在条目边界检查中止；任务收尾清除。
var biliStopFlags sync.Map

// SetBiliStops 记录服务端请求停止的追更（上报响应捎带调用）。
func SetBiliStops(followIDs []int64) {
	for _, id := range followIDs {
		biliStopFlags.Store(id, true)
	}
}

func biliStopRequested(followID int64) bool {
	_, ok := biliStopFlags.Load(followID)
	return ok
}

func clearBiliStop(followID int64) {
	biliStopFlags.Delete(followID)
}

// biliQueueItem 队列元素：任务 + 回传通道（worker 循环里要用）。
type biliQueueItem struct {
	rep  *Reporter
	task proto.BiliTask
}

// biliQueue 待执行任务队列（容量 8 足够——服务端每轮至多捎带 2 个）。
var (
	biliQueue    chan biliQueueItem
	biliWorker   sync.Once
	sleepBetween = 1500 * time.Millisecond // 条间冷静期（API 元数据请求另受客户端 1s 全局节流；此处覆盖「下载完成→下一条」的空档）
)

// EnqueueBiliTasks 任务入队并确保 worker 在跑（push 上报循环调用，非阻塞）。
func EnqueueBiliTasks(rep *Reporter, tasks []proto.BiliTask) {
	biliWorker.Do(func() {
		biliQueue = make(chan biliQueueItem, 8)
		go biliWorkerLoop()
	})
	for _, t := range tasks {
		select {
		case biliQueue <- biliQueueItem{rep: rep, task: t}:
		default:
			// 队列满：丢弃并回传失败（服务端任务态由租约超时兜底转 failed）
			log.Printf("B站追更队列已满，丢弃任务 (task=%d)", t.TaskID)
			go func() {
				_ = rep.PostBiliTaskResult(t.TaskID, proto.BiliTaskResult{Error: "agent 队列已满"})
			}()
		}
	}
}

// biliWorkerLoop 单 worker 串行执行；单任务 panic 隔离（隔离壳同中转任务口径）。
func biliWorkerLoop() {
	for it := range biliQueue {
		res := runBiliTask(&it.task)
		if res.Error != "" {
			log.Printf("B站追更任务失败（task %d %s·%s）: %s", it.task.TaskID, it.task.UpName, it.task.ListTitle, res.Error)
		} else {
			log.Printf("B站追更任务完成（task %d %s·%s）：新增 %d / 目录已存 %d", it.task.TaskID, it.task.UpName, it.task.ListTitle, res.Downloaded, res.FileCount)
		}
		if err := it.rep.PostBiliTaskResult(it.task.TaskID, res); err != nil {
			log.Printf("B站追更结果回传失败（task %d）: %v（服务端租约超时机制兜底）", it.task.TaskID, err)
		}
		clearBiliStop(it.task.FollowID) // 停止指令消费完毕（无论是否命中）
	}
}

// runBiliTask 单任务隔离壳。
func runBiliTask(t *proto.BiliTask) (res proto.BiliTaskResult) {
	defer func() {
		if r := recover(); r != nil {
			res = proto.BiliTaskResult{Error: "内部异常: 未知错误"}
			log.Printf("B站追更任务 panic（task %d）: %v", t.TaskID, r)
		}
	}()
	return doBiliTask(t)
}

// doBiliTask 按形态分派。
func doBiliTask(t *proto.BiliTask) proto.BiliTaskResult {
	c := bilibili.NewClient(t.Cookie)
	switch t.Mode {
	case proto.BiliTaskProbe:
		tg, err := c.ResolveTarget(t.UpName, t.Address)
		if err != nil {
			return proto.BiliTaskResult{Error: err.Error()}
		}
		return proto.BiliTaskResult{OK: true, Total: tg.Total, Target: &proto.BiliTarget{
			Mid: tg.Mid, Kind: tg.Kind, ListID: tg.ListID, Title: tg.Title, Total: tg.Total, UpName: tg.UpName,
		}}
	case proto.BiliTaskSeed10:
		return runDownload(c, t, 10)
	case proto.BiliTaskAll:
		return runDownload(c, t, 0)
	}
	return proto.BiliTaskResult{Error: "未知任务形态: " + t.Mode}
}

// runDownload 增量下载：列清单 → 按服务端已抓清单（payload 携带，TODO 3 判
// 重权威在服务端）筛出未抓条目 → 逐条下载转 m4a（目标文件已存在由写保护跳
// 过并按 exists 回传，服务端补记账——任务中断重跑自愈）。
// maxItems>0 只抓最新 N 条（首次种子），否则全量补缺。
func runDownload(c *bilibili.Client, t *proto.BiliTask, maxItems int) proto.BiliTaskResult {
	if t.SaveDir == "" {
		return proto.BiliTaskResult{Error: "保存目录未配置"}
	}
	tg := &bilibili.Target{Mid: t.Mid, Kind: t.Kind, ListID: t.ListID, Title: t.ListTitle}
	if err := validateRunTarget(tg); err != nil {
		return proto.BiliTaskResult{Error: err.Error()}
	}
	sub := t.SubDir
	if sub == "" {
		sub = bilibili.SubDirName(t.UpName, t.ListTitle)
	}
	dir := filepath.Join(t.SaveDir, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return proto.BiliTaskResult{Error: "创建保存目录失败: " + err.Error()}
	}
	wm := int64(0)
	if t.Mode == proto.BiliTaskAll {
		wm = t.Watermark // seed10 首抓无视水位（watermark 彼时尚未建立）
	}
	items, err := c.ListItems(tg, maxItems, wm)
	if err != nil {
		return proto.BiliTaskResult{Error: "获取合集列表失败: " + err.Error()}
	}
	res := proto.BiliTaskResult{Total: len(items)}
	fetched := make(map[string]bool, len(t.FetchedBVIDs))
	for _, bv := range t.FetchedBVIDs {
		fetched[bv] = true
	}
	stopReq := biliStopRequested(t.FollowID) // 入任务时可能已在停：直接空跑收尾
	for _, it := range items {
		if stopReq {
			res.Stopped = true
			log.Printf("B站追更已被手动停止（follow %d），已完成部分正常记账", t.FollowID)
			break
		}
		if t.Mode == proto.BiliTaskAll && it.PubDate <= t.Watermark {
			continue // 水位增量（7）：比已见最新的更旧（含未抓旧视频）不处理
		}
		if fetched[it.BVID] {
			continue // 服务端已记抓过：不下载也不回传
		}
		name, status, err := c.DownloadAudio(dir, it)
		if err != nil {
			log.Printf("B站追更单条下载失败（%s %s）: %v（跳过继续）", it.BVID, it.Title, err)
			continue
		}
		if len(res.Items) < 200 { // 回传清单上限（exists 补记账需要全量，200 条对单轮增量远够）
			res.Items = append(res.Items, proto.BiliItem{BVID: it.BVID, Title: it.Title, PubDate: it.PubDate, FileName: name, Status: status})
		}
		if status == "downloaded" {
			res.Downloaded++
		}
		// 停止请求在下载中途到达：当前条目完成（文件已落盘）后从下一条中止
		if biliStopRequested(t.FollowID) {
			stopReq = true
		}
		time.Sleep(sleepBetween)
	}
	// 目录实况仅作日志参考；已抓条目数以服务端 bili_items 记录数为准（消费侧回写）
	if n, err := countAudioFiles(dir); err == nil {
		res.FileCount = n
	}
	res.OK = true
	return res
}

// validateRunTarget 下载任务参数完整性：mid 必带；合集/视频列表形态需列表 id，
// 投稿形态（uploads）无列表 id（现场误拦修复：ListID=0 校验没豁免投稿形态）。
func validateRunTarget(tg *bilibili.Target) error {
	if tg == nil || tg.Mid == 0 {
		return protoErr("任务缺少已解析的 up 主信息")
	}
	if tg.Kind != "uploads" && tg.ListID == 0 {
		return protoErr("任务缺少已解析的合集信息")
	}
	return nil
}

// protoErr 构造任务错误。
func protoErr(msg string) error { return errors.New(msg) }

// countAudioFiles 目录内 m4a 文件数（目录实况参考值）。
func countAudioFiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".m4a") {
			n++
		}
	}
	return n, nil
}
