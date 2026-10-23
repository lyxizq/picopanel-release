package ytdlp

import "testing"

// 标记行解析：标题/进度/最终路径三类 stdout 输出（ytdlp.go 模板约定）。
func TestSplitMark(t *testing.T) {
	cases := []struct {
		line, kind, body string
	}{
		{"VDLT 某合集 第1话【4K】| 60帧", "VDLT", "某合集 第1话【4K】| 60帧"},
		{"VDLT  spaced title", "VDLT", "spaced title"},
		{"VDLP  42.3% 2345678.0 12", "VDLP", "42.3% 2345678.0 12"},
		{"VDLF /data/v/某视频 [BV1xx].mp4", "VDLF", "/data/v/某视频 [BV1xx].mp4"},
		{"[download] Destination: x.mp4", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		kind, body := splitMark(c.line)
		if kind != c.kind || body != c.body {
			t.Fatalf("splitMark(%q) = (%q,%q)，期望 (%q,%q)", c.line, kind, body, c.kind, c.body)
		}
	}
}

func TestParseProgress(t *testing.T) {
	p, ok := parseProgress("42.3% 2345678.0 12")
	if !ok || p.Percent != 42.3 || p.Speed != "2345678.0" || p.ETA != "12" {
		t.Fatalf("parseProgress = %+v ok=%v", p, ok)
	}
	// _percent_str 带对齐空格（splitMark 已 TrimSpace，这里再兜一层 Fields）
	if p, ok := parseProgress("  7.9% NA NA"); !ok || p.Percent != 7.9 || p.Speed != "NA" {
		t.Fatalf("parseProgress NA = %+v ok=%v", p, ok)
	}
	if _, ok := parseProgress("oops"); ok {
		t.Fatal("残缺行应放弃")
	}
	if _, ok := parseProgress(""); ok {
		t.Fatal("空行应放弃")
	}
}

func TestFormatSpeed(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"2345678.0", "2.2MiB/s"},
		{"512.0", "512B/s"},
		{"NA", ""},
		{"", ""},
		{"junk", ""},
		{"-1.0", ""},
	}
	for _, c := range cases {
		if got := FormatSpeed(c.raw); got != c.want {
			t.Fatalf("FormatSpeed(%q) = %q，期望 %q", c.raw, got, c.want)
		}
	}
}

func TestLastLines(t *testing.T) {
	errText := "line1\r\nline2\n\nline3"
	if got := lastLines(errText, 2); got != "line2；line3" {
		t.Fatalf("lastLines = %q", got)
	}
	if got := lastLines("only", 4); got != "only" {
		t.Fatalf("lastLines 单行 = %q", got)
	}
}
