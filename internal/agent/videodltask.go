package agent

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/proto"
	"lz-monitor/internal/ytdlp"
)

// 视频下载任务执行（TODO 4，追剧页「视频下载」tab）：server 入队，agent 机器
// 上跑 yt-dlp 落盘 mp4。与 B 站追更同架构：一单要下几分钟到几小时，绝不能在
// 上报循环里同步跑——单 worker goroutine 串行消费，结果独立回传；agent 重启丢
// 任务由服务端 6h 租约超时转 failed 兜底。执行机器需已安装 yt-dlp（合并音视频
// 还需 ffmpeg），缺席时任务失败并回传安装提示。

const videoDlStopWatchEvery = 2 * time.Second // 停止标记轮询周期

// videoDlStopFlags 服务端请求停止的任务集合：上报响应捎带置位，看护协程感知
// 后中止 yt-dlp 进程；任务收尾清除。
var videoDlStopFlags sync.Map

// SetVideoDlStops 记录服务端请求停止的下载任务（上报响应捎带调用）。
func SetVideoDlStops(taskIDs []int64) {
	for _, id := range taskIDs {
		videoDlStopFlags.Store(id, true)
	}
}

func clearVideoDlStop(taskID int64) {
	videoDlStopFlags.Delete(taskID)
}

// videoDlQueueItem 队列元素：任务 + 回传通道。
type videoDlQueueItem struct {
	rep  *Reporter
	task proto.VideoDlTask
}

var (
	videoDlQueue  chan videoDlQueueItem
	videoDlWorker sync.Once
	// videoDlProgressEvery 进度回传节流：服务端列表轮询 5s 级，3s 一跳足够顺滑
	videoDlProgressEvery = 3 * time.Second
)

// EnqueueVideoDlTasks 任务入队并确保 worker 在跑（push 上报循环调用，非阻塞）。
func EnqueueVideoDlTasks(rep *Reporter, tasks []proto.VideoDlTask) {
	videoDlWorker.Do(func() {
		videoDlQueue = make(chan videoDlQueueItem, 4)
		go videoDlWorkerLoop()
	})
	for _, t := range tasks {
		select {
		case videoDlQueue <- videoDlQueueItem{rep: rep, task: t}:
		default:
			// 队列满：丢弃并回传失败（服务端任务态由租约超时兜底转 failed）
			log.Printf("视频下载队列已满，丢弃任务 (task=%d)", t.TaskID)
			go func() {
				_ = rep.PostVideoDlResult(t.TaskID, proto.VideoDlTaskResult{Error: "agent 队列已满"})
			}()
		}
	}
}

// videoDlWorkerLoop 单 worker 串行执行；单任务 panic 隔离（同 B 站追更口径）。
func videoDlWorkerLoop() {
	for it := range videoDlQueue {
		res := runVideoDlTask(it.rep, &it.task)
		if res.Error != "" {
			log.Printf("视频下载任务失败（task %d）: %s", it.task.TaskID, res.Error)
		} else {
			log.Printf("视频下载任务完成（task %d）: %s", it.task.TaskID, res.FileName)
		}
		if err := it.rep.PostVideoDlResult(it.task.TaskID, res); err != nil {
			log.Printf("视频下载结果回传失败（task %d）: %v（服务端租约超时机制兜底）", it.task.TaskID, err)
		}
		clearVideoDlStop(it.task.TaskID) // 停止指令消费完毕（无论是否命中）
	}
}

// runVideoDlTask 单任务隔离壳。
func runVideoDlTask(rep *Reporter, t *proto.VideoDlTask) (res proto.VideoDlTaskResult) {
	defer func() {
		if r := recover(); r != nil {
			res = proto.VideoDlTaskResult{Error: "内部异常: 未知错误"}
			log.Printf("视频下载任务 panic（task %d）: %v", t.TaskID, r)
		}
	}()
	return doVideoDlTask(rep, t)
}

// doVideoDlTask 执行下载：停止看护协程轮询停止标记（下载停滞无进度回调时也能
// 响应中止）；进度回调按 3s 节流独立回传，标题解析出后随下一次进度捎带。
func doVideoDlTask(rep *Reporter, t *proto.VideoDlTask) proto.VideoDlTaskResult {
	if strings.TrimSpace(t.SaveDir) == "" {
		return proto.VideoDlTaskResult{Error: "保存目录未配置"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		tk := time.NewTicker(videoDlStopWatchEvery)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
			}
			if _, ok := videoDlStopFlags.Load(t.TaskID); ok {
				cancel()
				return
			}
		}
	}()

	var (
		mu       sync.Mutex
		title    string
		speed    string
		lastPost time.Time
	)
	postProgress := func(force bool, pct float64, sp string) {
		mu.Lock()
		now := time.Now()
		if !force && now.Sub(lastPost) < videoDlProgressEvery {
			mu.Unlock()
			return
		}
		lastPost = now
		tl := title
		mu.Unlock()
		if sp != "" {
			speed = sp
		}
		if err := rep.PostVideoDlProgress(t.TaskID, pct, speed, tl); err != nil {
			log.Printf("视频下载进度回传失败（task %d）: %v", t.TaskID, err)
		}
	}

	ev := &ytdlp.Events{
		OnTitle: func(tl string) {
			mu.Lock()
			title = tl
			mu.Unlock()
		},
		OnProgress: func(p ytdlp.Progress) {
			postProgress(false, p.Percent, p.Speed)
		},
	}
	res, err := ytdlp.Download(ctx, t.URL, t.SaveDir, t.Cookies, ev)
	cancel() // 先撤上下文唤醒看护协程退出（ctx 取消是其唯一出口），再等它收尾
	<-watchDone
	out := proto.VideoDlTaskResult{Title: res.Title}
	if res.FilePath != "" {
		out.FileName = res.FilePath
	}
	if res.FileSize > 0 {
		out.FileSize = res.FileSize
	}
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.OK = true
	return out
}
