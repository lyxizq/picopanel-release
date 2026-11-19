package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestGzipEncode API 组 gzip 中间件：客户端带 Accept-Encoding 时响应压缩
// （可解压还原），不带时原样直出且无 Content-Encoding 头。
func TestGzipEncode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	grp := r.Group("/api")
	grp.Use(gzipEncode())
	grp.GET("/j", func(c *gin.Context) {
		c.JSON(200, gin.H{"data": strings.Repeat("a", 4096)})
	})

	do := func(accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/j", nil)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// 带 gzip：响应标记压缩，解压后内容与原始 JSON 一致
	w := do("gzip, deflate, br")
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("应返回 Content-Encoding: gzip")
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("响应体应为合法 gzip 流: %v", err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"data"`) || w.Body.Len() >= 4096 {
		t.Fatalf("解压后应含原始 JSON 且压缩体积应明显小于原文: raw=%d gz=%d", len(body), w.Body.Len())
	}

	// 不带 Accept-Encoding：不压缩、无头
	w2 := do("")
	if w2.Header().Get("Content-Encoding") != "" {
		t.Fatalf("客户端不支持时不应压缩")
	}
	if !strings.Contains(w2.Body.String(), `"data"`) {
		t.Fatalf("未压缩响应应直接是 JSON")
	}
}

// TestGzipWriterUnwrap ResponseController 穿透 gzip 包装（328 事故根因回归）：
// gzipWriter 不实现 Unwrap 时 SetWriteDeadline 报错且被调用方 `_ =` 忽略——
// 慢请求被全局 30s 写超时掐断、浏览器报 Failed to fetch。httptest.ResponseRecorder
// 不支持 deadline（非连接），必须走真 socket 服务器才能验证穿透。
func TestGzipWriterUnwrap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", gzipEncode(), func(c *gin.Context) {
		if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{}); err != nil {
			t.Fatalf("SetWriteDeadline 应穿透 gzip 包装生效: %v", err)
		}
		c.String(200, "ok")
	})
	ts := httptest.NewServer(r)
	defer ts.Close()
	req, _ := http.NewRequest("GET", ts.URL+"/x", nil)
	req.Header.Set("Accept-Encoding", "gzip") // 逼出 gzipWriter 包装路径
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("应走 gzip 输出: %d %q", resp.StatusCode, resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("响应体应为合法 gzip 流: %v", err)
	}
	body, _ := io.ReadAll(zr)
	if string(body) != "ok" {
		t.Fatalf("解压后内容不对: %q", string(body))
	}
}
