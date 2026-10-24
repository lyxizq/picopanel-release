package api

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// gzipPool 复用 gzip.Writer（NewWriter 每次要分配压缩状态与滑动窗口缓冲，
// 总览页 30s 轮询下避免反复分配）。
var gzipPool = sync.Pool{
	New: func() any { return gzip.NewWriter(nil) },
}

// gzipWriter 包装 gin.ResponseWriter：响应头仍走原 writer，body 进压缩流。
// 嵌入原接口使 Flusher/Hijacker 等断言自动透传。
type gzipWriter struct {
	gin.ResponseWriter
	gw *gzip.Writer
}

func (w *gzipWriter) Write(b []byte) (int, error)       { return w.gw.Write(b) }
func (w *gzipWriter) WriteString(s string) (int, error) { return w.gw.Write([]byte(s)) }

// Unwrap 让 http.ResponseController（SetWriteDeadline/SetReadDeadline 等连接级
// 控制）穿透 gzip 包装触达底层连接。不实现它，gzip 组里 handler 清超时会静默
// 失效——慢请求（发信大附件上传）被全局 30s 超时掐断、前端报 Failed to fetch。
func (w *gzipWriter) Unwrap() http.ResponseWriter {
	if u, ok := w.ResponseWriter.(interface{ Unwrap() http.ResponseWriter }); ok {
		return u.Unwrap()
	}
	return w.ResponseWriter // gin.ResponseWriter 内嵌 http.ResponseWriter，兜底直出
}

// gzipEncode 对 API JSON 响应做 gzip 压缩：/servers（每台最新快照的
// disks/nets/listens JSON）与图表曲线数据可缩到 1/10 上下，公网带宽下
// 直接决定总览页的可见加载速度。只挂 /api/v1 组——gin render 不预设
// Content-Length，包 writer 安全；静态资源走 http.ServeContent（有
// Range/Content-Length 语义），不在此压缩。
func gzipEncode() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.Contains(c.Request.Header.Get("Accept-Encoding"), "gzip") {
			return // 客户端不支持（如未带 --compressed 的 curl），原样直出
		}
		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(c.Writer)
		c.Writer = &gzipWriter{ResponseWriter: c.Writer, gw: gz}
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")
		c.Writer.Header().Del("Content-Length") // 实际字节数变了，走 chunked
		c.Next()
		gz.Close() // 写出 gzip 尾块（handler 没输出 body 也只是空流）
		gzipPool.Put(gz)
	}
}
