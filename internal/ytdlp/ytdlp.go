// Package ytdlp 视频下载执行器（TODO 4，追剧页「视频下载」tab）：封装 yt-dlp
// 命令行（外部依赖，执行机器需已安装 yt-dlp；合并音视频还需 ffmpeg），server
// 本机执行器与 agent 执行 worker 共用。输出约定：stdout 只承载本包模板打印的
// 三类标记行（标题/进度/最终路径），yt-dlp 自身日志全走 stderr——回传进度与
// 解析错误时按标记行前缀区分即可。
package ytdlp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Bin yt-dlp 可执行名（走 PATH）。
const Bin = "yt-dlp"

// Available 判执行机器是否装了 yt-dlp。
func Available() bool {
	_, err := exec.LookPath(Bin)
	return err == nil
}

// Progress 一次进度回调。
type Progress struct {
	Percent float64 // 0-100
	Speed   string  // 原始字节/秒数值串或 "NA"，展示层格式化
	ETA     string  // 秒数值串或 "NA"
}

// Events 下载过程回调（均可为 nil）。
type Events struct {
	OnTitle    func(title string)
	OnProgress func(p Progress)
}

// Result 单次下载结果。
type Result struct {
	FilePath string // 最终落盘路径（after_move 回传）
	FileSize int64  // 字节（落盘后 stat）
	Title    string
}

// Download 下载单个视频为 mp4（merge-output-format 强约束容器；B 站/抖音等
// DASH 站合并音视频依赖 ffmpeg，缺席时 yt-dlp 报错原样透出）。cookies 为
// Netscape 格式 Cookie 文件内容（可空），非空时落临时文件传 --cookies。
func Download(ctx context.Context, rawURL, dir, cookies string, ev *Events) (*Result, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("视频地址为空")
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("视频地址必须是 http(s) 链接")
	}
	args := []string{
		"--newline", "--progress", "--no-playlist", "--no-warnings",
		"--merge-output-format", "mp4",
		"--progress-template", "download:VDLP %(progress._percent_str)s %(progress.speed)s %(progress.eta)s",
		"--print", "before_dl:VDLT %(title)s",
		"--print", "after_move:VDLF %(filepath)s",
		"-P", dir,
	}
	if cookies != "" {
		f, err := os.CreateTemp("", "vdl-cookies-*.txt")
		if err != nil {
			return nil, fmt.Errorf("写 Cookie 临时文件失败: %w", err)
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(cookies); err != nil {
			f.Close()
			return nil, fmt.Errorf("写 Cookie 临时文件失败: %w", err)
		}
		f.Close()
		args = append(args, "--cookies", f.Name())
	}
	args = append(args, rawURL)

	cmd := exec.CommandContext(ctx, Bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderrTail strings.Builder // 错误时截尾部透出（yt-dlp 报错原因都在 stderr）
	cmd.Stderr = &limitTail{b: &stderrTail, cap: 8 << 10}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("已取消")
		}
		return nil, fmt.Errorf("启动 %s 失败（执行机器需已安装 yt-dlp）: %w", Bin, err)
	}

	res := &Result{}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20) // 标题/路径单行上限 1MB 富余
	for sc.Scan() {
		kind, body := splitMark(sc.Text())
		switch kind {
		case "VDLT":
			res.Title = body
			if ev != nil && ev.OnTitle != nil {
				ev.OnTitle(body)
			}
		case "VDLP":
			if p, ok := parseProgress(body); ok && ev != nil && ev.OnProgress != nil {
				ev.OnProgress(p)
			}
		case "VDLF":
			res.FilePath = body
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("已手动停止")
		}
		msg := strings.TrimSpace(stderrTail.String())
		if msg == "" {
			msg = err.Error()
		}
		return res, fmt.Errorf("%s", lastLines(msg, 4))
	}
	if res.FilePath != "" {
		if st, err := os.Stat(res.FilePath); err == nil {
			res.FileSize = st.Size()
		}
	}
	return res, nil
}

// splitMark 拆标记行："VDLT 标题" → ("VDLT","标题")；非标记行 → ("","")。
// 标题/路径部分原样保留（可能含任意字符），进度行按空白切字段。
func splitMark(line string) (string, string) {
	for _, m := range []string{"VDLP ", "VDLT ", "VDLF "} {
		if strings.HasPrefix(line, m) {
			return m[:4], strings.TrimSpace(line[len(m)-1:])
		}
	}
	return "", ""
}

// parseProgress 解析进度行剩余部分：" 42.3% 2345678.0 12" → (42.3, "2345678.0", "12")。
// _percent_str 带对齐空格与百分号，speed/eta 为原始数值或 NA；解析不出整行放弃
//（下一次回调自然补上，不值得报错）。
func parseProgress(body string) (Progress, bool) {
	fields := strings.Fields(body)
	if len(fields) < 2 {
		return Progress{}, false
	}
	pct, err := strconv.ParseFloat(strings.TrimSuffix(fields[0], "%"), 64)
	if err != nil {
		return Progress{}, false
	}
	speed, eta := "NA", "NA"
	if len(fields) > 1 {
		speed = fields[1]
	}
	if len(fields) > 2 {
		eta = fields[2]
	}
	return Progress{Percent: pct, Speed: speed, ETA: eta}, true
}

// lastLines 取尾部 n 行（错误透出精简口径）。
func lastLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var out []string
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return strings.Join(out, "；")
}

// limitTail 只留尾部 cap 字节的 io.Writer（stderr 兜底缓冲）。
type limitTail struct {
	b   *strings.Builder
	cap int
}

func (w *limitTail) Write(p []byte) (int, error) {
	w.b.Write(p)
	if s := w.b.String(); len(s) > w.cap {
		w.b.Reset()
		w.b.WriteString(s[len(s)-w.cap:])
	}
	return len(p), nil
}

// FormatSpeed 原始字节/秒数值串转人类可读（NA/解析失败回空串不展示）。
func FormatSpeed(raw string) string {
	var n float64
	if _, err := fmt.Sscanf(raw, "%f", &n); err != nil || n <= 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return strconv.FormatFloat(n, 'f', 0, 64) + "B/s"
	}
	for _, u := range []string{"KiB", "MiB", "GiB"} {
		n /= unit
		if n < unit {
			return strconv.FormatFloat(n, 'f', 1, 64) + u + "/s"
		}
	}
	return strconv.FormatFloat(n, 'f', 1, 64) + "TiB/s"
}
