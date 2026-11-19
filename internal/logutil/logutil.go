// Package logutil 提供按大小滚动的日志输出：当前文件写满后顺次轮转备份，
// 总占用（当前文件 + 备份）不超过设定上限，防止监控自身把磁盘写爆。
package logutil

import (
	"io"
	"os"
	"strconv"
	"sync"
)

// Backups 是保留的备份文件数（总占用 = Backups+1 个文件均摊上限）。
const Backups = 2

// 单文件滚动阈值下限：防止配置了极小上限时高频轮转。
const minPerFile = 64 << 10

// Writer 是可滚动的日志文件写入器，并发安全。
type Writer struct {
	mu       sync.Mutex
	path     string
	maxBytes int64 // 单文件滚动阈值
	backups  int
	file     *os.File
	size     int64
}

// New 返回"标准错误 + 滚动日志文件"的复合输出。
//   - path 为空或打开失败：仅标准错误（日志问题绝不能弄挂监控本身）
//   - path 与标准错误指向同一文件（如 nohup 重定向到同名文件）：仅标准错误，避免双写错乱
//
// totalBytes 是日志总占用上限，均摊到当前文件与备份。
func New(path string, totalBytes int64, backups int) io.Writer {
	if backups < 0 {
		backups = 0
	}
	perFile := totalBytes / int64(backups+1)
	if perFile < minPerFile {
		perFile = minPerFile
	}
	var w io.Writer = os.Stderr
	if path != "" {
		if fw, err := open(path, perFile, backups); err == nil {
			if sameAsStderr(fw.file) {
				_ = fw.file.Close()
			} else {
				w = io.MultiWriter(os.Stderr, fw)
			}
		}
	}
	return w
}

func open(path string, perFile int64, backups int) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w := &Writer{path: path, maxBytes: perFile, backups: backups, file: f}
	if st, err := f.Stat(); err == nil {
		w.size = st.Size()
	}
	return w, nil
}

// sameAsStderr 判断日志文件是否就是标准错误指向的文件（nohup 重定向同名文件的场景）。
func sameAsStderr(f *os.File) bool {
	a, err1 := f.Stat()
	if err1 != nil {
		return false
	}
	b, err2 := os.Stderr.Stat()
	return err2 == nil && os.SameFile(a, b)
}

// Write 写入并在超阈值时轮转：path.1→path.2，path→path.1，重开新文件。
// 轮转失败时截断当前文件继续写，保证日志不中断。
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil || w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateOrTruncate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *Writer) rotateOrTruncate() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	// 备份链顺移：删除最旧，path.1→path.2 … path→path.1
	for i := w.backups; i >= 2; i-- {
		_ = os.Rename(backupPath(w.path, i-1), backupPath(w.path, i))
	}
	if w.backups >= 1 {
		if err := os.Rename(w.path, backupPath(w.path, 1)); err != nil {
			return w.truncateFallback()
		}
	} else {
		_ = os.Remove(w.path)
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return w.truncateFallback()
	}
	w.file = f
	w.size = 0
	return nil
}

// truncateFallback 轮转失败（如目录只读）时截断原文件继续用。
func (w *Writer) truncateFallback() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err // 彻底写不了：上层 Write 返回错误，log 包会忽略
	}
	w.file = f
	w.size = 0
	return nil
}

func backupPath(path string, i int) string {
	return path + "." + strconv.Itoa(i)
}
