package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/term"
)

// TermHandler Web 终端 WebSocket 与 SFTP 文件管理端点。
type TermHandler struct {
	term   *term.Manager
	st     *store.Store  // 专属脚本存取（term_scripts.go）
	tasks  fileTaskState // 文件后台任务（压缩/解压）登记表，见 term_files_task.go
	counts fileTaskState // 目录递归统计（属性弹窗「计算」）登记表，见 term_files_task.go
}

// WS 处理终端 WebSocket：升级后交给 term 管理器（首帧 auth 鉴权、
// PTY/Shell 桥接、空闲断开）。注册在中间件组外——gzip 与 JSON 超时
// 语义都不适配长连接；浏览器 WebSocket 无法携带自定义 Authorization
// 头，token 不能进 URL（会落访问日志），只能在首帧内传输。
func (h *TermHandler) WS(c *gin.Context) {
	id, err := strconv.ParseInt(c.Query("server_id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的服务器 ID"})
		return
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		// CheckOrigin 保持默认同源校验：其他源 的页面拿不到
		// 有效 token，也防止跨站页面直接开终端
	}
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade 已写完 HTTP 错误响应
	}
	_ = h.term.Serve(ws, id) // 阻塞至会话结束（失败原因已用 error 帧发给前端）
}
