package bilibili

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// atom 合成顶层 atom：8 字节头（大小+类型）+ payload。
func atom(typ string, payload []byte) []byte {
	buf := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(buf[:4], uint32(8+len(payload)))
	copy(buf[4:8], typ)
	copy(buf[8:], payload)
	return buf
}

// TestIsFastStartMP4 读头判定：moov/mdat 顺序与缺失的各种组合。
func TestIsFastStartMP4(t *testing.T) {
	ftyp := atom("ftyp", []byte("isomisol2"))
	mdat := atom("mdat", bytes.Repeat([]byte{0}, 32))
	moov := atom("moov", bytes.Repeat([]byte{0}, 24))

	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"moov 在尾部（存量回退产物）", append(append(append([]byte{}, ftyp...), mdat...), moov...), false},
		{"moov 前置（faststart）", append(append(append([]byte{}, ftyp...), moov...), mdat...), true},
		{"无 moov（半截/损坏）", append(append([]byte{}, ftyp...), mdat...), false},
		{"无 mdat", append(append([]byte{}, ftyp...), moov...), true},
		{"非 MP4 垃圾内容", []byte("not an mp4 file at all...."), false},
		{"空文件", nil, false},
	}
	for _, tc := range cases {
		p := filepath.Join(t.TempDir(), "a.m4a")
		if err := os.WriteFile(p, tc.data, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := isFastStartMP4(p)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// withFFmpegStub 临时替换 ffmpegLookup，测毕还原。
func withFFmpegStub(t *testing.T, path string) {
	t.Helper()
	old := ffmpegLookup
	ffmpegLookup = func() string { return path }
	t.Cleanup(func() { ffmpegLookup = old })
}

// TestRemuxOrMoveWithFFmpeg 真 ffmpeg 封装：产物达标、临时流被清掉。
func TestRemuxOrMoveWithFFmpeg(t *testing.T) {
	ff := ffmpegLookup()
	if ff == "" {
		t.Skip("本机无 ffmpeg")
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.m4a")
	// 造一个 moov 在尾部的输入（默认 mov 封装即尾部 moov）；ffmpeg 输出按
	// 扩展名猜封装器，先生成 .m4a 再改名为下载临时流形态的 .part
	tmp := filepath.Join(dir, "raw.m4a")
	gen := exec.Command(ff, "-y", "-loglevel", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration=1", "-c:a", "aac", tmp)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("生成测试音频失败: %v\n%s", err, out)
	}
	if err := os.Rename(tmp, tmp+".part"); err != nil {
		t.Fatal(err)
	}
	tmp = tmp + ".part"
	if ok, _ := isFastStartMP4(tmp); ok {
		t.Skip("输入意外达标，无法验证封装路径")
	}
	if err := remuxOrMove(tmp, dest); err != nil {
		t.Fatalf("remuxOrMove: %v", err)
	}
	if ok, err := isFastStartMP4(dest); err != nil || !ok {
		t.Fatalf("产物未达标: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("临时流未清理: %v", err)
	}
	// 完整性：解码走通无报错（chunk offset 未被破坏）
	dec := exec.Command(ff, "-v", "error", "-i", dest, "-f", "null", "-")
	if out, err := dec.CombinedOutput(); err != nil {
		t.Fatalf("修复后文件解码失败: %v\n%s", err, out)
	}
}

// TestRemuxOrMoveWithoutFFmpeg ffmpeg 缺席桩：回退直存（改名），且不静默。
func TestRemuxOrMoveWithoutFFmpeg(t *testing.T) {
	withFFmpegStub(t, "")
	dir := t.TempDir()
	tmp := filepath.Join(dir, "raw.part")
	dest := filepath.Join(dir, "out.m4a")
	if err := os.WriteFile(tmp, []byte("fake-audio-stream"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := remuxOrMove(tmp, dest); err != nil {
		t.Fatalf("remuxOrMove: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("临时流未改名: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("直存文件缺失: %v", err)
	}
}

// TestRepairDirFastStart 存量修复端到端：尾部 moov 文件被重封装达标、达标
// 的跳过（幂等）、修复后文件可完整解码。
func TestRepairDirFastStart(t *testing.T) {
	ff := ffmpegLookup()
	if ff == "" {
		t.Skip("本机无 ffmpeg")
	}
	dir := t.TempDir()
	tail := filepath.Join(dir, "20261002_旧文件.m4a")
	gen := exec.Command(ff, "-y", "-loglevel", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration=1", "-c:a", "aac", tail)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("生成测试音频失败: %v\n%s", err, out)
	}
	if ok, _ := isFastStartMP4(tail); ok {
		t.Skip("输入意外达标，无法验证修复路径")
	}
	// 一个已达标文件 + 一个非 m4a 文件，验证跳过逻辑
	okFile := filepath.Join(dir, "20261003_已达标.m4a")
	if err := remuxFastStart(ff, tail, okFile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if fixed := RepairDirFastStart(dir); fixed != 1 {
		t.Fatalf("修复数=%d，want 1", fixed)
	}
	if ok, err := isFastStartMP4(tail); err != nil || !ok {
		t.Fatalf("修复后未达标: ok=%v err=%v", ok, err)
	}
	dec := exec.Command(ff, "-v", "error", "-i", tail, "-f", "null", "-")
	if out, err := dec.CombinedOutput(); err != nil {
		t.Fatalf("修复后解码失败: %v\n%s", err, out)
	}
	// 幂等：第二轮全达标，零修复
	if fixed := RepairDirFastStart(dir); fixed != 0 {
		t.Fatalf("第二轮修复数=%d，want 0（应幂等跳过）", fixed)
	}
}

// TestRepairDirFastStartNoFFmpeg 无 ffmpeg：整体跳过不动文件。
func TestRepairDirFastStartNoFFmpeg(t *testing.T) {
	withFFmpegStub(t, "")
	dir := t.TempDir()
	p := filepath.Join(dir, "a.m4a")
	if err := os.WriteFile(p, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fixed := RepairDirFastStart(dir); fixed != 0 {
		t.Fatalf("修复数=%d，want 0", fixed)
	}
	if data, err := os.ReadFile(p); err != nil || string(data) != "junk" {
		t.Fatalf("文件被改动: %v %q", err, data)
	}
}
