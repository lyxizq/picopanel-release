package api

// 文件管理的后台任务与搜索：创建压缩包/解压是长操作（大目录打包、大包
// 解压可能跑几分钟），HTTP 请求内同步做既超时也无进度——改为 POST 启动
// goroutine 后台跑，前端 500ms 轮询 GET 进度，完毕自动关进度窗。
// 每台服务器同时只允许一个任务（SFTP 连接是池化单连接，串行更稳）。
// 文件内容搜索同文件（同步请求，靠扫描上限保证时长可控）。

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/pkg/sftp"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// ---------- 任务登记表 ----------

// errTaskCanceled 协作式取消哨兵：任务在读写/条目边界感知取消后返回它，
// runFileTask 据此落 canceled 状态（区别于失败）。
var errTaskCanceled = errors.New("任务已取消")

// fileTask 后台任务状态（前端轮询展示）。
type fileTask struct {
	ID       string `json:"id"`
	ServerID int64  `json:"server_id"`
	Type     string `json:"type"`           // archive | unarchive | count
	Total    int64  `json:"total"`          // 总量：archive/解包 tar=字节；zip=条目数
	Done     int64  `json:"done"`           // 已完成量（口径同 Total）
	Unit     string `json:"unit,omitempty"` // total/done 单位：B=字节，项=条目数（空=未知）
	Status   string `json:"status"`         // running | done | canceled | error
	Error    string `json:"error,omitempty"`
	Result   string `json:"result,omitempty"` // archive：最终压缩包文件名
	// count 型任务的实时统计（文件数/目录数/总字节/读不动被跳过的目录数）
	Files   int64 `json:"files,omitempty"`
	Dirs    int64 `json:"dirs,omitempty"`
	Size    int64 `json:"size,omitempty"`
	Skipped int64 `json:"skipped,omitempty"`

	// canceled 取消标志（指针：get() 会拷贝任务快照给前端，atomic 值类型
	// 拷贝有锁竞争问题；指针两处共享同一个标志）。不参与 JSON 序列化。
	canceled *atomic.Bool
}

// stopFn 供 progWriter/progReader/循环边界使用的取消检查。
func (t *fileTask) stopFn() func() bool {
	return func() bool { return t.canceled != nil && t.canceled.Load() }
}

// fileTaskState 挂在 TermHandler 上的登记表：每服务器一个槽位。
// Done/Total 由任务 goroutine 持续写、HTTP goroutine 读，全部经 mu 串行。
type fileTaskState struct {
	mu       sync.Mutex
	byServer map[int64]*fileTask
}

func (s *fileTaskState) get(serverID int64) *fileTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byServer == nil {
		return nil
	}
	if t := s.byServer[serverID]; t != nil {
		cp := *t
		return &cp
	}
	return nil
}

// start 登记新任务；该服务器已有任务（无论新旧）时返回 false 拒绝——
// 上一任务的收尾由前端轮询感知，这里不排队。
func (s *fileTaskState) start(serverID int64, t *fileTask) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byServer == nil {
		s.byServer = map[int64]*fileTask{}
	}
	if cur := s.byServer[serverID]; cur != nil && cur.Status == "running" {
		return false
	}
	s.byServer[serverID] = t
	return true
}

func (s *fileTaskState) progress(serverID, done, total int64) {
	s.mu.Lock()
	if t := s.byServer[serverID]; t != nil {
		t.Done, t.Total = done, total
	}
	s.mu.Unlock()
}

// setUnit 进度单位（B/项）由任务 goroutine 在分发时写、HTTP 轮询读，走 mu
// 串行（同 Done/Total 口径；get() 的整结构体拷贝在锁内）。
func (s *fileTaskState) setUnit(serverID int64, unit string) {
	s.mu.Lock()
	if t := s.byServer[serverID]; t != nil {
		t.Unit = unit
	}
	s.mu.Unlock()
}

// counters 更新 count 型任务的实时统计（属性弹窗轮询展示）。
func (s *fileTaskState) counters(serverID, files, dirs, size, skipped int64) {
	s.mu.Lock()
	if t := s.byServer[serverID]; t != nil {
		t.Files, t.Dirs, t.Size, t.Skipped = files, dirs, size, skipped
	}
	s.mu.Unlock()
}

func (s *fileTaskState) finish(serverID int64, status, errMsg, result string) {
	s.mu.Lock()
	if t := s.byServer[serverID]; t != nil {
		t.Status = status
		t.Error = errMsg
		t.Result = result
	}
	s.mu.Unlock()
}

// requestCancel 置位取消标志（协作式：任务自身在下一个读写/条目边界退出）。
// 仅对该 id 的运行中任务生效，已结束/已被顶替返回 false。
func (s *fileTaskState) requestCancel(serverID int64, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.byServer[serverID]
	if t == nil || t.ID != id || t.Status != "running" || t.canceled == nil {
		return false
	}
	t.canceled.Store(true)
	return true
}

// ---------- HTTP 端点 ----------

type fileTaskReq struct {
	Type  string   `json:"type" binding:"required,oneof=archive unarchive"`
	Paths []string `json:"paths"` // archive：打包成员（绝对路径）
	Name  string   `json:"name"`  // archive：压缩包基础名（不含扩展名）
	Path  string   `json:"path"`  // unarchive：压缩包路径（绝对路径）
}

// StartTask POST /term/files/task?server_id=：启动后台压缩/解压任务。
func (h *TermHandler) StartTask(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	var req fileTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（type 必填：archive/unarchive）"})
		return
	}
	switch req.Type {
	case "archive":
		if len(req.Paths) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未选择要打包的项目"})
			return
		}
		for i := range req.Paths {
			pp := strings.TrimSpace(req.Paths[i])
			if pp == "" || !strings.HasPrefix(pp, "/") {
				c.JSON(http.StatusBadRequest, gin.H{"error": "打包路径必须是绝对路径"})
				return
			}
			req.Paths[i] = path.Clean(pp)
			if isProtectedPath(req.Paths[i]) {
				rejectProtected(c)
				return
			}
		}
		req.Name = strings.TrimSpace(req.Name)
		// 产物名只取基础名语义：含路径分隔符或 .. 段时 path.Join 归一会把
		// 压缩包落点带出目标目录
		if req.Name != path.Base(req.Name) || strings.Contains(req.Name, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "压缩包名称无效（只填基础名，不含路径与扩展名）"})
			return
		}
	case "unarchive":
		p := strings.TrimSpace(req.Path)
		if p == "" || !strings.HasPrefix(p, "/") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "path 必须是绝对路径"})
			return
		}
		req.Path = path.Clean(p)
		if isProtectedPath(req.Path) {
			rejectProtected(c)
			return
		}
		if !isArchiveName(path.Base(req.Path)) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的压缩包格式（支持 tar/tar.gz/tgz/zip）"})
			return
		}
	}
	t := &fileTask{
		ID:       strconv.FormatInt(time.Now().UnixNano(), 36),
		ServerID: id,
		Type:     req.Type,
		Status:   "running",
		canceled: &atomic.Bool{},
	}
	if !h.tasks.start(id, t) {
		c.JSON(http.StatusConflict, gin.H{"error": "该服务器已有文件任务进行中，请稍候"})
		return
	}
	go h.runFileTask(t, req)
	c.JSON(http.StatusOK, gin.H{"code": 0, "task": t})
}

// TaskStatus GET /term/files/task?server_id=：查询该服务器当前/最近任务。
func (h *TermHandler) TaskStatus(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": h.tasks.get(id)})
}

type taskCancelReq struct {
	ID string `json:"id" binding:"required"`
}

// CancelTask POST /term/files/task-cancel?server_id=：请求取消运行中的任务。
// 取消是协作式的——置位标志后任务在当前读写的边界自行退出（秒级延迟），
// 打包半成品照常清理；任务已结束（或 id 不匹配）返回 409。
func (h *TermHandler) CancelTask(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	var req taskCancelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（id 必填）"})
		return
	}
	if !h.tasks.requestCancel(id, req.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "任务已结束"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// ---------- 目录递归统计（属性弹窗「计算」） ----------

// 超大目录的同步 count 走 HTTP 请求必超时（WriteTimeout 30s），且个别
// 无权限子目录会让整个统计失败——改为独立后台任务：进度实时回传、可取消，
// 与压缩/解压任务分表登记互不占用。只读操作不设条目数上限。
type countReq struct {
	Path string `json:"path" binding:"required"`
}

// StartCount POST /term/files/count?server_id=：启动目录递归统计任务。
func (h *TermHandler) StartCount(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	var req countReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（path 必填）"})
		return
	}
	p := path.Clean(strings.TrimSpace(req.Path))
	if !strings.HasPrefix(p, "/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path 必须是绝对路径"})
		return
	}
	if isProtectedPath(p) { // 统计会读密钥文件大小/归属，同样纳入单点守卫
		rejectProtected(c)
		return
	}
	t := &fileTask{
		ID:       strconv.FormatInt(time.Now().UnixNano(), 36),
		ServerID: id,
		Type:     "count",
		Status:   "running",
		Unit:     "项",
		canceled: &atomic.Bool{},
	}
	if !h.counts.start(id, t) {
		c.JSON(http.StatusConflict, gin.H{"error": "该服务器正在统计中，请稍候"})
		return
	}
	go h.runCount(t, p)
	c.JSON(http.StatusOK, gin.H{"code": 0, "task": t})
}

// CountStatus GET /term/files/count?server_id=：查询该服务器当前/最近统计任务。
func (h *TermHandler) CountStatus(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": h.counts.get(id)})
}

// CancelCount POST /term/files/count-cancel?server_id=：请求取消统计
// （属性弹窗关闭时调用；协作式，任务在下一个目录边界退出）。
func (h *TermHandler) CancelCount(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	var req taskCancelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（id 必填）"})
		return
	}
	if !h.counts.requestCancel(id, req.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "统计已结束"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// runCount 统计主体：显式栈迭代（目录树深也不爆 goroutine 栈）；无权限等
// 读不动的子目录跳过并计入 skipped（不再让整个统计失败）；根路径读不动
// 才算失败。文件数/目录数/总字节经 counters 实时回传。
func (h *TermHandler) runCount(t *fileTask, p string) {
	cl, release, err := h.term.SFTPHold(t.ServerID)
	if err != nil {
		h.counts.finish(t.ServerID, "error", err.Error(), "")
		return
	}
	defer release()
	stop := t.stopFn()
	var files, dirs, size, skipped int64
	report := func() {
		h.counts.counters(t.ServerID, files, dirs, size, skipped)
		h.counts.progress(t.ServerID, files+dirs, 0) // Total=0 表示总量未知，Done=已扫描条目
	}
	err = func() error {
		st, err := cl.Stat(p)
		if err != nil {
			return err
		}
		if !st.IsDir() {
			files, size = 1, st.Size()
			report()
			return nil
		}
		pending := []string{p}
		for len(pending) > 0 {
			if stop() {
				return errTaskCanceled
			}
			dir := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			infos, err := cl.ReadDir(dir)
			if err != nil {
				skipped++ // 权限等读不动：跳过该子目录，统计继续
				continue
			}
			for _, fi := range infos {
				if fi.IsDir() {
					dirs++
					if fi.Mode()&os.ModeSymlink == 0 { // 符号链接目录不下钻（防环）
						pending = append(pending, path.Join(dir, fi.Name()))
					}
				} else {
					files++
					size += fi.Size()
				}
			}
			report()
		}
		return nil
	}()
	switch {
	case err == nil:
		h.counts.finish(t.ServerID, "done", "", "")
	case errors.Is(err, errTaskCanceled):
		h.counts.finish(t.ServerID, "canceled", "", "")
	default:
		if isSFTPConnErr(err) {
			h.term.DropSFTP(t.ServerID)
		}
		h.counts.finish(t.ServerID, "error", err.Error(), "")
	}
}

// runFileTask 任务主体：连接失败直接落任务错误（前端轮询可见）。
// 整个任务期间 SFTPHold 持有连接——大目录打包/大包解压可能跑十几分钟，
// 不登记在用会被空闲回收器按 10 分钟空闲线中途掐断。
func (h *TermHandler) runFileTask(t *fileTask, req fileTaskReq) {
	cl, release, err := h.term.SFTPHold(t.ServerID)
	if err != nil {
		h.tasks.finish(t.ServerID, "error", err.Error(), "")
		return
	}
	defer release()
	if t.Type == "archive" {
		h.tasks.setUnit(t.ServerID, "B")
		name, err := h.runArchive(cl, t, req)
		switch {
		case err == nil:
			h.tasks.finish(t.ServerID, "done", "", name)
		case errors.Is(err, errTaskCanceled):
			h.tasks.finish(t.ServerID, "canceled", "", "")
		default:
			h.failTask(t, err)
		}
		return
	}
	if err := h.runUnarchive(cl, t, req.Path); err != nil {
		if errors.Is(err, errTaskCanceled) {
			h.tasks.finish(t.ServerID, "canceled", "", "")
			return
		}
		h.failTask(t, err)
		return
	}
	h.tasks.finish(t.ServerID, "done", "", "")
}

// failTask 收尾：连接级错误丢弃池化连接（下次操作重连），错误落任务。
func (h *TermHandler) failTask(t *fileTask, err error) {
	if isSFTPConnErr(err) {
		h.term.DropSFTP(t.ServerID)
	}
	h.tasks.finish(t.ServerID, "error", err.Error(), "")
}

// runArchive 打包：先递归预估总字节，再边写边报进度（progWriter 计的是
// 写入 tar 的字节数，含少量头开销，超预估时钳到 total）。取消检查贯穿
// 预估扫描与打包写入（stop 回调）。
func (h *TermHandler) runArchive(cl *sftp.Client, t *fileTask, req fileTaskReq) (string, error) {
	stop := t.stopFn()
	total, err := sftpSizeOf(cl, req.Paths, stop)
	if err != nil {
		return "", err
	}
	if total <= 0 {
		total = 1
	}
	return sftpArchive(cl, req.Paths, req.Name, func(done int64) {
		if done > total {
			done = total
		}
		h.tasks.progress(t.ServerID, done, total)
	}, stop)
}

// ---------- 解压 ----------

// maxUnarchiveBytes 解压总解压量上限（防压缩包炸弹撑爆磁盘），与单文件上传上限对齐。
const maxUnarchiveBytes = 5 << 30 // 5GB

func isArchiveName(name string) bool {
	l := strings.ToLower(name)
	return strings.HasSuffix(l, ".tar") || strings.HasSuffix(l, ".tar.gz") ||
		strings.HasSuffix(l, ".tgz") || strings.HasSuffix(l, ".zip")
}

// runUnarchive 按后缀分派 tar/tar.gz/tgz/zip，解到压缩包所在目录。
func (h *TermHandler) runUnarchive(cl *sftp.Client, t *fileTask, ap string) error {
	st, err := cl.Lstat(ap)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("目标是目录")
	}
	dest := path.Dir(ap)
	l := strings.ToLower(path.Base(ap))
	switch {
	case strings.HasSuffix(l, ".tar.gz") || strings.HasSuffix(l, ".tgz"):
		h.tasks.setUnit(t.ServerID, "B")
		return h.unarchiveTar(cl, t, ap, dest, true)
	case strings.HasSuffix(l, ".tar"):
		h.tasks.setUnit(t.ServerID, "B")
		return h.unarchiveTar(cl, t, ap, dest, false)
	default:
		h.tasks.setUnit(t.ServerID, "项")
		return h.unarchiveZip(cl, t, ap, dest)
	}
}

// unarchiveCheckReq 解压冲突预检入参。
type unarchiveCheckReq struct {
	Path string `json:"path" binding:"required"`
}

// UnarchiveCheck POST /term/files/unarchive-check：解压前预检——枚举压缩包
// 顶层条目名，与解压目标目录（压缩包所在目录）现有条目比对，返回同名冲突
// 列表（前端据此弹「是否覆盖」确认）。zip 只读 central directory；tar 系
// 流式读头并丢弃数据体（tgz 需解压整个流，成本≈解压本身，可接受）。
func (h *TermHandler) UnarchiveCheck(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	var req unarchiveCheckReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（path 必填）"})
		return
	}
	ap := path.Clean(strings.TrimSpace(req.Path))
	if !strings.HasPrefix(ap, "/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path 必须是绝对路径"})
		return
	}
	if isProtectedPath(ap) {
		rejectProtected(c)
		return
	}
	if !isArchiveName(path.Base(ap)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的压缩包格式（支持 tar/tar.gz/tgz/zip）"})
		return
	}
	h.withSFTP(c, id, func(cl *sftp.Client) error {
		tops, err := archiveTopLevels(cl, ap)
		if err != nil {
			return err
		}
		dest := path.Dir(ap)
		conflicts := []string{}
		for _, name := range tops {
			if _, err := cl.Lstat(path.Join(dest, name)); err == nil {
				conflicts = append(conflicts, name)
				if len(conflicts) >= 20 { // 超长截断，确认框放得下
					break
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "entries": len(tops), "conflicts": conflicts})
		return nil
	})
}

// archiveTopLevels 枚举压缩包顶层条目名（去重、排序稳定）。
func archiveTopLevels(cl *sftp.Client, ap string) ([]string, error) {
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" {
			return
		}
		seg := name
		if i := strings.IndexByte(seg, '/'); i >= 0 {
			seg = seg[:i]
		}
		if seg != "" {
			seen[seg] = true
		}
	}
	l := strings.ToLower(path.Base(ap))
	switch {
	case strings.HasSuffix(l, ".zip"):
		f, err := cl.Open(ap)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return nil, fmt.Errorf("zip 打开失败: %w", err)
		}
		ctr := 0
		for _, zf := range zr.File {
			ctr++
			if ctr > sftpCountMax {
				return nil, fmt.Errorf("压缩包含条目超过 %d，请手动处理", sftpCountMax)
			}
			if name, ok := safeArchiveName(zipName(zf.Name)); ok {
				add(name)
			}
		}
	default: // tar / tar.gz / tgz：流式读头，数据体丢弃
		f, err := cl.Open(ap)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		var src io.Reader = f
		if strings.HasSuffix(l, ".tar.gz") || strings.HasSuffix(l, ".tgz") {
			gzr, err := gzip.NewReader(f)
			if err != nil {
				return nil, fmt.Errorf("gzip 解压失败: %w", err)
			}
			defer gzr.Close()
			src = gzr
		}
		tr := tar.NewReader(src)
		ctr := 0
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			ctr++
			if ctr > sftpCountMax {
				return nil, fmt.Errorf("压缩包含条目超过 %d，请手动处理", sftpCountMax)
			}
			if name, ok := safeArchiveName(hdr.Name); ok {
				add(name)
			}
			if hdr.Size > 0 { // 跳过数据体（读出丢弃，tar 头是流内交错的）
				if _, err := io.CopyN(io.Discard, tr, hdr.Size); err != nil {
					return nil, err
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// unarchiveTar 流式解包 tar（gz=true 时过 gzip）。进度=远端源文件已读
// 字节/文件大小；tar 头声明 Size 后可用余额校验兜住虚报。安全：条目名
// 经 safeArchiveName 清洗（.. 逃逸条目直接跳过），符号链接指向包外原样
// 重建（不跟随内容），硬链接回落为复制已解出的目标。取消：读侧 progReader
// 边界 + 条目边界检查。
func (h *TermHandler) unarchiveTar(cl *sftp.Client, t *fileTask, ap, dest string, gz bool) error {
	st, err := cl.Lstat(ap)
	if err != nil {
		return err
	}
	f, err := cl.Open(ap)
	if err != nil {
		return err
	}
	defer f.Close()
	h.tasks.progress(t.ServerID, 0, st.Size()) // Total 走锁内 setter（直写与轮询拷贝竞态）
	stop := t.stopFn()
	pr := &progReader{r: f, stop: stop, fn: func(n int64) {
		if n > t.Total {
			n = t.Total
		}
		h.tasks.progress(t.ServerID, n, t.Total)
	}}
	var src io.Reader = pr
	if gz {
		gzr, err := gzip.NewReader(pr)
		if err != nil {
			return fmt.Errorf("gzip 解压失败: %w", err)
		}
		defer gzr.Close()
		src = gzr
	}
	tr := tar.NewReader(src)
	ctr := 0
	var written int64
	verified := map[string]bool{}
	for {
		if stop() { // 条目边界响应取消
			return errTaskCanceled
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if errors.Is(err, errTaskCanceled) {
			return errTaskCanceled
		}
		if err != nil {
			return err
		}
		name, ok := safeArchiveName(hdr.Name)
		if !ok {
			continue
		}
		ctr++
		if ctr > sftpCountMax {
			return fmt.Errorf("压缩包含条目超过 %d，请手动处理", sftpCountMax)
		}
		written += hdr.Size
		if written > maxUnarchiveBytes {
			return fmt.Errorf("解压总量超过 %dGB 上限，已中止", maxUnarchiveBytes>>30)
		}
		target := path.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if !archiveSafeTarget(cl, dest, target, verified) {
				continue // 落点父链上有符号链接：跳过，不往解压目录外写
			}
			if err := cl.MkdirAll(target); err != nil {
				return err
			}
			verified[target] = true
		case tar.TypeReg:
			if !archiveSafeTarget(cl, dest, target, verified) {
				continue
			}
			if err := extractFile(cl, target, io.LimitReader(tr, hdr.Size), os.FileMode(hdr.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// 拒绝指向解压目录之外的链接（绝对目标或 .. 逃逸）：否则解压出
			// 「k → /etc/lm.idc」再经面板读 /tmp/x/k，密钥守卫被链接追踪绕过
			if path.IsAbs(hdr.Linkname) ||
				!strings.HasPrefix(path.Clean(path.Join(path.Dir(target), hdr.Linkname)), dest+"/") {
				continue
			}
			if !archiveSafeTarget(cl, dest, target, verified) {
				continue
			}
			_ = cl.Remove(target) // 同名已存在时先摘掉
			if err := cl.Symlink(hdr.Linkname, target); err != nil {
				continue // 链接建不上（权限等）不算失败，跳过
			}
			resetVerified(verified) // 链接可能顶掉已验证的目录前缀，整体失效
		case tar.TypeLink:
			ln, ok := safeArchiveName(hdr.Linkname)
			if !ok {
				continue
			}
			srcPath := path.Join(dest, ln)
			if fi, err := cl.Lstat(srcPath); err == nil && fi.Mode().IsRegular() {
				if !archiveSafeTarget(cl, dest, target, verified) {
					continue
				}
				_ = cl.Remove(target)
				ctr2 := 0
				if err := sftpCopy(cl, srcPath, target, &ctr2); err != nil {
					return err
				}
			}
		}
	}
	h.tasks.progress(t.ServerID, t.Total, t.Total)
	return nil
}

// unarchiveZip 走 central directory（zip.ReaderAt 只拉目录，不用整包下载）。
// 进度=已解条目数/总条目数。取消：条目边界 + 单条目读侧 progReader 边界。
func (h *TermHandler) unarchiveZip(cl *sftp.Client, t *fileTask, ap, dest string) error {
	st, err := cl.Lstat(ap)
	if err != nil {
		return err
	}
	f, err := cl.Open(ap)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return fmt.Errorf("zip 打开失败: %w", err)
	}
	h.tasks.progress(t.ServerID, 0, int64(len(zr.File)))
	stop := t.stopFn()
	var written int64
	verified := map[string]bool{}
	for i, zf := range zr.File {
		if stop() { // 条目边界响应取消
			return errTaskCanceled
		}
		name, ok := safeArchiveName(zipName(zf.Name))
		if !ok {
			continue
		}
		written += int64(zf.UncompressedSize64)
		if written > maxUnarchiveBytes {
			return fmt.Errorf("解压总量超过 %dGB 上限，已中止", maxUnarchiveBytes>>30)
		}
		target := path.Join(dest, name)
		if zf.FileInfo().IsDir() {
			if !archiveSafeTarget(cl, dest, target, verified) {
				continue // 落点父链上有符号链接：跳过，不往解压目录外写
			}
			if err := cl.MkdirAll(target); err != nil {
				return err
			}
			verified[target] = true
		} else {
			if !archiveSafeTarget(cl, dest, target, verified) {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				return err
			}
			err = extractFile(cl, target, &progReader{r: rc, stop: stop}, zf.Mode().Perm())
			rc.Close()
			if err != nil {
				return err
			}
		}
		h.tasks.progress(t.ServerID, int64(i+1), t.Total)
	}
	h.tasks.progress(t.ServerID, t.Total, t.Total)
	return nil
}

// archiveSafeTarget 解包条目落点安全核查：自 dest 向下逐分量 Lstat，任一
// 已存在分量是符号链接（或被普通文件占位）即返回 false，调用方跳过该条目。
// SFTP 的 OpenFile/MkdirAll/Symlink 都会跟随路径中的既有链接解析——包内先
// 声明 lnk→/etc 再跟 lnk/x 条目，就能把内容写到解压目录之外（Zip Slip 变体，
// 仅查末端分量挡不住父链穿透）。首个不存在的分量以下整条链必然尚不存在
// （POSIX 语义），extractFile 补建属全新创建，无链接可跟随。verified 缓存
// 已确认安全的分量（包内条目高度共享父链，逐条全链 Lstat 是纯 RPC 浪费）；
// 符号链接条目落盘后整体失效——先解出的真目录可被同名空目录链接顶掉。
func archiveSafeTarget(cl *sftp.Client, dest, target string, verified map[string]bool) bool {
	rel := strings.TrimPrefix(strings.TrimPrefix(target, dest), "/")
	cur := dest
	for _, comp := range strings.Split(rel, "/") {
		if comp == "" {
			continue
		}
		cur = path.Join(cur, comp)
		if verified[cur] {
			continue
		}
		fi, err := cl.Lstat(cur)
		if err != nil {
			return true
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return false
		}
		verified[cur] = true
	}
	return true
}

// resetVerified 清空分量核查缓存。
func resetVerified(verified map[string]bool) {
	for k := range verified {
		delete(verified, k)
	}
}

// extractFile 解出一个普通文件（按包内权限），写完即关闭。
// 包内无显式目录条目时（zip 常见：只存 dir/a.txt 全路径、无 dir/ 条目），
// 嵌套文件的父目录不存在，OpenFile 报 file does not exist——补建后重试。
func extractFile(cl *sftp.Client, target string, r io.Reader, perm os.FileMode) error {
	dst, err := cl.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		if mkErr := cl.MkdirAll(path.Dir(target)); mkErr != nil {
			return err
		}
		if dst, err = cl.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC); err != nil {
			return err
		}
	}
	_, err = io.Copy(dst, r)
	if err == nil && perm != 0 {
		err = dst.Chmod(perm)
	}
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return err
}

// safeArchiveName 清洗压缩包条目名：统一斜杠、剥前导 / 与盘符语义、
// 经 Clean 解析后天然无 .. 逃逸。返回 ok=false 表示空名/可疑条目，跳过。
func safeArchiveName(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimLeft(name, "/")
	name = path.Clean(name)
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	return name, true
}

// zipName 修正 zip 条目名编码：规范 EFS 位未置位时文件名编码不定，Windows
// 中文系统压缩的 zip 用 GBK——名字非法 UTF-8 时按 GBK 解码（乱码治理），
// 合法 UTF-8（Linux/macOS 制作）原样返回。
func zipName(name string) string {
	if utf8.ValidString(name) {
		return name
	}
	if g, err := simplifiedchinese.GBK.NewDecoder().Bytes([]byte(name)); err == nil {
		return string(g)
	}
	return name
}

// ---------- 进度计数包装 ----------

// progWriter 计经手字节数并回调（tar 进度）。stop 非空时每次写前置检查，
// 取消已请求则返回 errTaskCanceled 中断 io.Copy（协作式取消的写侧边界）。
type progWriter struct {
	w    io.Writer
	n    int64
	fn   func(n int64)
	stop func() bool
}

func (p *progWriter) Write(b []byte) (int, error) {
	if p.stop != nil && p.stop() {
		return 0, errTaskCanceled
	}
	n, err := p.w.Write(b)
	if n > 0 {
		p.n += int64(n)
		if p.fn != nil {
			p.fn(p.n)
		}
	}
	return n, err
}

// progReader 计经手字节数并回调（tar/tgz 解包/单条目写远端进度）。
// stop 语义同 progWriter（读侧边界）。
type progReader struct {
	r    io.Reader
	n    int64
	fn   func(n int64)
	stop func() bool
}

func (p *progReader) Read(b []byte) (int, error) {
	if p.stop != nil && p.stop() {
		return 0, errTaskCanceled
	}
	n, err := p.r.Read(b)
	if n > 0 {
		p.n += int64(n)
		if p.fn != nil {
			p.fn(p.n)
		}
	}
	return n, err
}

// sftpSizeOf 递归累加若干路径的文件字节（打包前预估总量，条目数防呆）。
// stop 非空时逐条目检查取消（预估扫描可能在大目录上跑很久）。
func sftpSizeOf(cl *sftp.Client, paths []string, stop func() bool) (int64, error) {
	var total int64
	ctr := 0
	var add func(p string, fi os.FileInfo) error
	add = func(p string, fi os.FileInfo) error {
		if stop != nil && stop() {
			return errTaskCanceled
		}
		ctr++
		if ctr > sftpCountMax {
			return fmt.Errorf("目录条目超过 %d，请分批操作", sftpCountMax)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if fi.IsDir() {
			infos, err := cl.ReadDir(p)
			if err != nil {
				return err
			}
			for _, ci := range infos {
				if err := add(path.Join(p, ci.Name()), ci); err != nil {
					return err
				}
			}
			return nil
		}
		total += fi.Size()
		return nil
	}
	for _, p := range paths {
		fi, err := cl.Lstat(p)
		if err != nil {
			return 0, err
		}
		if err := add(p, fi); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// ---------- 文件内容搜索 ----------

// 扫描上限：保证单次搜索时长可控（SFTP 串行读，几秒内返回）。
const (
	maxSearchFiles   = 3000    // 最多扫描文件数
	maxSearchResults = 300     // 最多返回的命中文件数
	maxSearchBytes   = 2 << 20 // 单文件读取上限，超出跳过
	maxSearchLines   = 20      // 单文件保留的匹配行数
	maxSearchLineRun = 300     // 匹配行截断长度（字符）
	maxSearchQuery   = 200     // 搜索词/正则最大长度
	maxSearchDepth   = 16      // 递归目录深度上限
)

type fileSearchReq struct {
	Path      string `json:"path"`
	Query     string `json:"query" binding:"required"`
	Regex     bool   `json:"regex"`
	CaseSense bool   `json:"case_sensitive"`
	Recursive bool   `json:"recursive"`
	Ext       string `json:"ext"` // 逗号/空格分隔后缀，如 "log,txt"（点可省）
}

type searchLine struct {
	No   int    `json:"no"`
	Text string `json:"text"`
}

type searchFile struct {
	Path  string       `json:"path"`
	Name  string       `json:"name"`
	Count int          `json:"count"` // 命中行数（含未展示部分）
	Lines []searchLine `json:"lines"`
}

// SearchFiles POST /term/files/search：目录下按内容搜索文件。文本文件
// （含 \x00 判为二进制跳过）逐行匹配，返回命中行（前端自己高亮）。
// 目录读不动/文件读不动一律跳过（权限受限环境照常出结果）。
func (h *TermHandler) SearchFiles(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	var req fileSearchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（搜索内容必填）"})
		return
	}
	dir := strings.TrimSpace(req.Path)
	if dir == "" {
		dir = "/"
	}
	if !strings.HasPrefix(dir, "/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path 必须是绝对路径"})
		return
	}
	dir = path.Clean(dir)
	if isProtectedPath(dir) { // 内容搜索会读文件正文，密钥必须挡在搜索根上
		rejectProtected(c)
		return
	}
	q := req.Query
	if len(q) > maxSearchQuery {
		c.JSON(http.StatusBadRequest, gin.H{"error": "搜索内容过长（≤200 字符）"})
		return
	}
	var re *regexp.Regexp
	if req.Regex {
		var err error
		re, err = regexp.Compile(q)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "正则表达式无效: " + err.Error()})
			return
		}
	}
	exts := map[string]bool{}
	for _, e := range strings.FieldsFunc(req.Ext, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';' || r == '，' || r == '；'
	}) {
		e = strings.ToLower(strings.TrimSpace(e))
		e = strings.TrimPrefix(e, ".")
		if e != "" {
			exts[e] = true
		}
	}

	h.withSFTP(c, id, func(cl *sftp.Client) error {
		st, err := cl.Stat(dir)
		if err != nil {
			return err
		}
		if !st.IsDir() {
			return fmt.Errorf("搜索目录无效: %s", dir)
		}
		files := []searchFile{}
		scanned := 0
		truncated := false
		var walk func(d string, depth int)
		walk = func(d string, depth int) {
			if truncated {
				return
			}
			if depth > maxSearchDepth {
				return
			}
			infos, err := cl.ReadDir(d)
			if err != nil {
				return // 目录读不动（权限等）：跳过
			}
			for _, fi := range infos {
				if truncated {
					return
				}
				if fi.IsDir() {
					if req.Recursive {
						walk(path.Join(d, fi.Name()), depth+1)
					}
					continue
				}
				if fi.Mode()&os.ModeSymlink != 0 {
					continue
				}
				if isProtectedPath(path.Join(d, fi.Name())) {
					continue // 搜索根在密钥上级目录时遍历也不得读其正文
				}
				if scanned >= maxSearchFiles {
					truncated = true
					return
				}
				name := fi.Name()
				if len(exts) > 0 {
					e := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
					if !exts[e] {
						continue
					}
				}
				if fi.Size() > maxSearchBytes {
					continue
				}
				f, err := cl.Open(path.Join(d, name))
				if err != nil {
					continue
				}
				data, err := io.ReadAll(io.LimitReader(f, maxSearchBytes))
				f.Close()
				if err != nil {
					continue
				}
				scanned++
				if len(data) == 0 || bytes.IndexByte(data, 0) >= 0 {
					continue // 空文件/二进制
				}
				sf, count := scanContent(string(data), q, re, req.CaseSense)
				if count == 0 {
					continue
				}
				sf.Path = path.Join(d, name)
				sf.Name = name
				files = append(files, sf)
				if len(files) >= maxSearchResults {
					truncated = true
					return
				}
			}
		}
		walk(dir, 0)
		c.JSON(http.StatusOK, gin.H{
			"code": 0, "files": files, "scanned": scanned, "truncated": truncated,
		})
		return nil
	})
}

// scanContent 逐行匹配：返回命中文件（最多 maxSearchLines 行样本）与总命中行数。
func scanContent(content, q string, re *regexp.Regexp, caseSense bool) (searchFile, int) {
	var sf searchFile
	count := 0
	for i, line := range strings.Split(content, "\n") {
		var hit bool
		switch {
		case re != nil:
			hit = re.MatchString(line)
		case caseSense:
			hit = strings.Contains(line, q)
		default:
			hit = strings.Contains(strings.ToLower(line), strings.ToLower(q))
		}
		if !hit {
			continue
		}
		count++
		if len(sf.Lines) < maxSearchLines {
			t := strings.TrimRight(line, "\r")
			if r := []rune(t); len(r) > maxSearchLineRun {
				t = string(r[:maxSearchLineRun]) + "…"
			}
			sf.Lines = append(sf.Lines, searchLine{No: i + 1, Text: t})
		}
	}
	sf.Count = count
	return sf, count
}

// ---------- 名称搜索（文件/文件夹按名递归找，区别于上面的内容搜索） ----------

// findEntry 名称搜索结果条目：fileEntry 字段外再带绝对路径（前端表格展示
// 相对路径，双击进入/编辑/删除等操作按绝对路径）。
type findEntry struct {
	fileEntry
	Path string `json:"path"`
}

// 名称搜索上限：深度/访问目录数/结果数/耗时四面封顶，超大目录树不至于拖死连接。
const (
	maxFindDepth   = 10   // 递归深度上限
	maxFindDirs    = 5000 // 最多访问目录数
	maxFindResults = 500  // 最多返回条目数
	maxFindTime    = 15 * time.Second
)

// FindFiles POST /term/files/find：目录下按名称搜索文件/文件夹（不区分
// 大小写子串匹配，含子目录）。BFS 遍历；符号链接参与匹配但不下钻（防环）；
// 目录读不动（权限等）跳过。任一上限触及时 truncated=true（结果可能不全）。
func (h *TermHandler) FindFiles(c *gin.Context) {
	id, ok := termID(c)
	if !ok {
		return
	}
	var req fileSearchReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误（搜索名称必填）"})
		return
	}
	dir := strings.TrimSpace(req.Path)
	if dir == "" {
		dir = "/"
	}
	if !strings.HasPrefix(dir, "/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path 必须是绝对路径"})
		return
	}
	dir = path.Clean(dir)
	if isProtectedPath(dir) { // 内容搜索会读文件正文，密钥必须挡在搜索根上
		rejectProtected(c)
		return
	}
	q := req.Query
	if len(q) > maxSearchQuery {
		c.JSON(http.StatusBadRequest, gin.H{"error": "搜索名称过长（≤200 字符）"})
		return
	}
	lq := strings.ToLower(q)

	h.withSFTP(c, id, func(cl *sftp.Client) error {
		if st, err := cl.Stat(dir); err != nil {
			return err
		} else if !st.IsDir() {
			return fmt.Errorf("搜索目录无效: %s", dir)
		}
		names := h.term.UserNames(id)
		out := []findEntry{}
		truncated := false
		deadline := time.Now().Add(maxFindTime)
		type qitem struct {
			dir   string
			depth int
		}
		queue := []qitem{{dir, 0}}
		visited := 0
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			visited++
			if visited > maxFindDirs || time.Now().After(deadline) || len(out) >= maxFindResults {
				truncated = true
				break
			}
			infos, err := cl.ReadDir(cur.dir)
			if err != nil {
				continue // 目录读不动（权限等）：跳过
			}
			for _, fi := range infos {
				name := fi.Name()
				if name == "" {
					continue
				}
				if strings.Contains(strings.ToLower(name), lq) {
					e := findEntry{
						fileEntry: fileEntry{
							Name:  name,
							Size:  fi.Size(),
							Mode:  fi.Mode().String(),
							Perm:  fmt.Sprintf("%o", fi.Mode().Perm()),
							Mtime: fi.ModTime().Unix(),
							IsDir: fi.IsDir(),
						},
						Path: path.Join(cur.dir, name),
					}
					if st, ok := fi.Sys().(*sftp.FileStat); ok {
						if n := names[st.UID]; n != "" {
							e.Owner = n
						} else {
							e.Owner = strconv.FormatUint(uint64(st.UID), 10)
						}
					}
					out = append(out, e)
				}
				if fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 && cur.depth < maxFindDepth {
					queue = append(queue, qitem{path.Join(cur.dir, name), cur.depth + 1})
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "files": out, "truncated": truncated})
		return nil
	})
}
