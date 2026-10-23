// Package videodl 视频下载（TODO 4，追剧页「视频下载」tab）server 侧调度与
// 本机执行器：executor='server' 的任务由本包单 worker 串行领取执行（yt-dlp
// 本机落盘），executor='agent' 的任务只入队、由 push agent 上报捎带领取。
// 队列纪律见 store/video_dl.go（6h 长租约，视频分钟级到小时级）。
package videodl

import (
	"context"
	"log"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/ytdlp"
)

const (
	settingCookies = "video_dl_cookies" // Netscape Cookie 文件内容（加密落库，B 站高清等需登录场景）
	claimLimit     = 1                  // 单 worker 串行：下载是带宽密集型，并发无益
	scanInterval   = 3 * time.Second
	stopWatchEvery = 2 * time.Second
	cleanupEvery   = time.Hour
)

// Cookies 读下载 Cookie（settings 存储；空=未配置）。
func Cookies(st *store.Store) string {
	v, _, err := st.GetSettingSealed(settingCookies)
	if err != nil {
		return ""
	}
	return v
}

// SetCookies 写下载 Cookie（空串=清除）。
func SetCookies(st *store.Store, v string) error {
	return st.SetSettingSealed(settingCookies, v)
}

// manager server 本机执行器：单 worker 串行消费 executor='server' 的任务。
type manager struct {
	st *store.Store

	mu       sync.Mutex
	curID    int64
	curCanc  context.CancelFunc
	stopSeen bool // 当前任务已观察到停止请求（防重复取消）
}

// Run 阻塞循环：扫队领取执行 + 停止感知 + 租约回收 + 终态清理。每台 server
// 进程一个 goroutine（server.go 启动）。
func Run(ctx context.Context, st *store.Store) {
	m := &manager{st: st}
	st.CleanupVideoDlTasks(store.VideoDlKeepDone)
	lastCleanup := time.Now()
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if n := st.ReclaimExpiredVideoDlTasks(); n > 0 {
			log.Printf("视频下载租约超时回收 %d 条", n)
		}
		if time.Since(lastCleanup) >= cleanupEvery {
			st.CleanupVideoDlTasks(store.VideoDlKeepDone)
			lastCleanup = time.Now()
		}
		m.sweep(ctx)
	}
}

// sweep 一轮：感知停止 → 空闲则领取一条执行（同步跑完再回）。
func (m *manager) sweep(ctx context.Context) {
	if id, ok := m.current(); ok {
		m.watchStop(id)
		return
	}
	tasks, err := m.st.ClaimVideoDlTasks("server", 0, claimLimit)
	if err != nil || len(tasks) == 0 {
		return
	}
	m.runTask(ctx, &tasks[0])
}

// current 当前在跑的任务 id（无则 ok=false）。
func (m *manager) current() (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.curID == 0 {
		return 0, false
	}
	return m.curID, true
}

// watchStop 停止感知：stop_requested 置位即取消当前进程（API Stop 只打库标记，
// 进程杀在本包——分属不同 goroutine，不能直接调）。
func (m *manager) watchStop(id int64) {
	m.mu.Lock()
	stopSeen := m.stopSeen
	m.mu.Unlock()
	if stopSeen {
		return
	}
	req, err := m.st.VideoDlStopRequested(id)
	if err != nil || !req {
		return
	}
	m.mu.Lock()
	m.stopSeen = true
	if m.curCanc != nil && m.curID == id {
		m.curCanc()
	}
	m.mu.Unlock()
	log.Printf("视频下载已请求停止 (task=%d)，正在中止 yt-dlp", id)
}

// runTask 执行单条任务（同步，跑完才回 sweep）：进度/标题回写 + 停止看护 +
// 终态落库。进度回调里不再查库（看护协程专职盯停止标记，回调只管写进度）。
func (m *manager) runTask(parent context.Context, t *store.VideoDlTask) {
	log.Printf("视频下载开始 (task=%d) → %s", t.ID, t.URL)
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.curID, m.curCanc, m.stopSeen = t.ID, cancel, false
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.curID, m.curCanc = 0, nil
		m.mu.Unlock()
	}()

	watchDone := make(chan struct{})
	go func() { // 停止看护：下载停滞（无进度回调）时也能响应停止请求
		defer close(watchDone)
		tk := time.NewTicker(stopWatchEvery)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
			}
			req, err := m.st.VideoDlStopRequested(t.ID)
			if err == nil && req {
				cancel()
				return
			}
		}
	}()

	percent, speed, title := 0, "", ""
	ev := &ytdlp.Events{
		OnTitle: func(tl string) {
			title = tl
			m.st.UpdateVideoDlProgress(t.ID, percent, speed, title)
		},
		OnProgress: func(p ytdlp.Progress) {
			if v := int(p.Percent); v > percent {
				percent = v
			}
			speed = ytdlp.FormatSpeed(p.Speed)
			m.st.UpdateVideoDlProgress(t.ID, percent, speed, "")
		},
	}
	res, err := ytdlp.Download(ctx, t.URL, t.SaveDir, t.Cookies, ev)
	cancel() // 先撤上下文唤醒看护协程退出（ctx 取消是其唯一出口），再等它收尾
	<-watchDone
	if err != nil {
		log.Printf("视频下载失败 (task=%d): %v", t.ID, err)
	} else {
		log.Printf("视频下载完成 (task=%d): %s", t.ID, res.FilePath)
	}
	if _, ferr := m.st.FinishVideoDlTask(t.ID, res.Title, res.FilePath, res.FileSize, errText(err)); ferr != nil {
		log.Printf("视频下载终态落库失败 (task=%d): %v", t.ID, ferr)
	}
}

// errText 成功时错误为空。
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
