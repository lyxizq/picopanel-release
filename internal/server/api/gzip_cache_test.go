package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestGzipStaticCache 静态资源压缩结果缓存：同名文件第二次请求直接回缓存
// 字节（传入不同数据也不再重压——embed 产物在二进制内恒定，键即文件名）。
func TestGzipStaticCache(t *testing.T) {
	gzCache.Delete("assets/app-test.js")
	defer gzCache.Delete("assets/app-test.js")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	big := []byte(strings.Repeat("const x=1;", 300)) // >1KB 才走压缩
	gz1, ok1 := gzipStatic(c, "assets/app-test.js", big)
	if !ok1 || len(gz1) == 0 || len(gz1) >= len(big) {
		t.Fatalf("首次压缩失败: ok=%v len=%d", ok1, len(gz1))
	}

	// 同名传不同数据：命中缓存应返回首次的压缩字节
	other := []byte(strings.Repeat("DIFFERENT", 300))
	gz2, ok2 := gzipStatic(c, "assets/app-test.js", other)
	if !ok2 {
		t.Fatal("缓存未命中")
	}
	if !bytes.Equal(gz1, gz2) {
		t.Fatal("第二次未走缓存（重压了新数据）")
	}

	// 小文件/不支持 gzip 不压缩也不缓存
	small := []byte("tiny")
	if _, ok := gzipStatic(c, "assets/small.js", small); ok {
		t.Fatal("小于 1KB 不应压缩")
	}
	reqNoGz := httptest.NewRequest(http.MethodGet, "/", nil)
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = reqNoGz
	if _, ok := gzipStatic(c2, "assets/app-test2.js", big); ok {
		t.Fatal("客户端不支持 gzip 不应压缩")
	}
	if _, loaded := gzCache.Load("assets/app-test2.js"); loaded {
		t.Fatal("不支持 gzip 的请求不应写入缓存")
	}
}
