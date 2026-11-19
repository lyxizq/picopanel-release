package logutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotationCapsTotal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	perFile := int64(1000)
	w, err := open(path, perFile, 2)
	if err != nil {
		t.Fatal(err)
	}

	line := []byte("0123456789\n") // 11 字节
	// 写 ~5 个文件量，远超 perFile×(backups+1)
	for i := 0; i < 500; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatal(err)
		}
	}

	// 断言：只剩 path、path.1、path.2，且总大小 ≤ perFile×3 + 尾差
	var total int64
	for _, p := range []string{path, path + ".1", path + ".2"} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s 应存在: %v", p, err)
		}
		total += st.Size()
	}
	// 每次写 11 字节、阈值 1000：单文件最多 1000+10 溢出
	if total > perFile*3+3*int64(len(line)) {
		t.Fatalf("总大小 %d 超过上限 %d", total, perFile*3)
	}
	// path.3 及更老的备份不应存在
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Fatal("path.3 不应存在（备份数=2）")
	}
	// 当前文件仍在写入（最后内容非空）
	if w.size == 0 {
		t.Fatal("轮转后当前文件应有内容")
	}
}

func TestNewEmptyPathStderrOnly(t *testing.T) {
	if w := New("", 1<<20, 2); w == nil {
		t.Fatal("空路径应返回 stderr writer")
	}
}
