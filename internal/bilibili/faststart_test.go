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

// TestRepairDirFastStartNoFFmpeg 无 ffmpeg 桩：纯 Go 重排照样修 plain 文件；
// 非 MP4 垃圾文件不动。
func TestRepairDirFastStartNoFFmpeg(t *testing.T) {
	withFFmpegStub(t, "")
	dir := t.TempDir()
	// 合成 plain 尾部 moov 文件（结构合法，可被纯 Go 重排）
	plain := filepath.Join(dir, "plain.m4a")
	if err := os.WriteFile(plain, tailMoovFixture(t), 0o644); err != nil {
		t.Fatal(err)
	}
	// 垃圾内容：无 moov，重排报错，保留原样
	junk := filepath.Join(dir, "junk.m4a")
	if err := os.WriteFile(junk, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fixed := RepairDirFastStart(dir); fixed != 1 {
		t.Fatalf("修复数=%d，want 1", fixed)
	}
	if ok, err := isFastStartMP4(plain); err != nil || !ok {
		t.Fatalf("纯 Go 修复后未达标: ok=%v err=%v", ok, err)
	}
	if data, err := os.ReadFile(junk); err != nil || string(data) != "junk" {
		t.Fatalf("垃圾文件被改动: %v %q", err, data)
	}
}

// ---- 纯 Go moov 前置重排 ----

// box 构造：递归容器包装 stco（entries 为采样绝对偏移）
func tailMoovFixture(t *testing.T) []byte {
	t.Helper()
	stcoBody := make([]byte, 8+4*2) // version/flags + entry_count + 2×uint32
	binary.BigEndian.PutUint32(stcoBody[4:8], 2)
	binary.BigEndian.PutUint32(stcoBody[8:12], 100) // 指向 mdat 内
	binary.BigEndian.PutUint32(stcoBody[12:16], 200)
	stco := atom("stco", stcoBody)
	moovBody := atom("trak", atom("mdia", atom("minf", atom("stbl", stco))))
	return append(append(append([]byte{},
		atom("ftyp", []byte("isomisol2"))...),
		atom("mdat", bytes.Repeat([]byte{0xAB}, 300))...),
		atom("moov", moovBody)...)
}

// findStcoBody 从完整 box 字节里取出 stco 的 body。
func findStcoBody(t *testing.T, boxBytes []byte) []byte {
	t.Helper()
	var found []byte
	var walk func(typ string, body []byte) error
	walk = func(typ string, body []byte) error {
		if typ == "stco" {
			found = body
			return nil
		}
		if moovContainers[typ] {
			return walkBoxes(body, walk)
		}
		return nil
	}
	if err := walkBoxes(boxBytes, walk); err != nil {
		t.Fatalf("walkBoxes: %v", err)
	}
	return found
}

// stcoEntries 读 stco body 里的偏移清单。
func stcoEntries(t *testing.T, body []byte) []uint64 {
	t.Helper()
	n := binary.BigEndian.Uint32(body[4:8])
	out := make([]uint64, n)
	for i := range out {
		out[i] = uint64(binary.BigEndian.Uint32(body[8+i*4:]))
	}
	return out
}

// TestRelocateMoovSynthetic 合成结构验证：moov 前置、stco 偏移精确 +delta、
// 总大小不变。
func TestRelocateMoovSynthetic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.m4a")
	dest := filepath.Join(dir, "out.m4a")
	data := tailMoovFixture(t)
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	moovSize := int64(len(data)) - // 顶层 moov 大小 = 总长 - ftyp - mdat
		(int64(8 + len("isomisol2")) + int64(8+300))
	if err := relocateMoov(src, dest); err != nil {
		t.Fatalf("relocateMoov: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(data) {
		t.Fatalf("产物大小 %d ≠ 原始 %d", len(got), len(data))
	}
	if ok, err := isFastStartMP4(dest); err != nil || !ok {
		t.Fatalf("产物未达标: ok=%v err=%v", ok, err)
	}
	// 从产物里取 moov atom 全字节，校验 stco 偏移 +delta
	var moovAtom []byte
	off := 0
	for off < len(got) {
		sz := int(binary.BigEndian.Uint32(got[off : off+4]))
		typ := string(got[off+4 : off+8])
		if typ == "moov" {
			moovAtom = got[off : off+sz]
			break
		}
		off += sz
	}
	wantDelta := int(moovSize)
	for i, v := range stcoEntries(t, findStcoBody(t, moovAtom)) {
		if v != uint64(100*(i+1)+wantDelta) {
			t.Fatalf("stco[%d]=%d，want %d（+delta %d）", i, v, 100*(i+1)+wantDelta, wantDelta)
		}
	}
}

// TestRelocateMoovFragmentedRefuse fragmented/带索引流：拒绝重排。
func TestRelocateMoovFragmentedRefuse(t *testing.T) {
	dir := t.TempDir()
	for _, typ := range []string{"moof", "sidx"} {
		src := filepath.Join(dir, typ+".m4a")
		data := append(append(append([]byte{},
			atom("ftyp", []byte("isomisol2"))...),
			atom("mdat", make([]byte, 64))...),
			atom(typ, make([]byte, 16))...)
		data = append(data, atom("moov", make([]byte, 32))...)
		if err := os.WriteFile(src, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := relocateMoov(src, filepath.Join(dir, typ+".out.m4a")); err == nil {
			t.Fatalf("%s 流应被拒绝，却成功了", typ)
		}
	}
}

// TestChineseSpacePaths 存量目录/文件名带中文、空格与特殊字符的全链路：
// 重排、ffmpeg 封装、目录级修复都不得被路径字符干扰（真实追更目录形态如
// 「up主名 合集名/20261002_标题 引号'(x) & 符号.m4a」）。
func TestChineseSpacePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "up主 名字 合集 2026")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "20261002_标题 带空格 引号'(x) & 符号 [25P].m4a"
	src := filepath.Join(dir, name)

	// 夹具：有 ffmpeg 造真音频（尾 moov，可解封装）；无则合成结构（仅结构合法）
	ff := ffmpegLookup()
	if ff != "" {
		gen := exec.Command(ff, "-y", "-loglevel", "error", "-f", "lavfi",
			"-i", "sine=frequency=440:duration=1", "-c:a", "aac", src)
		if out, err := gen.CombinedOutput(); err != nil {
			t.Fatalf("生成测试音频失败: %v\n%s", err, out)
		}
	} else {
		if err := os.WriteFile(src, tailMoovFixture(t), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// ①纯 Go 重排
	dest := filepath.Join(dir, "重排 产物.m4a")
	if err := relocateMoov(src, dest); err != nil {
		t.Fatalf("纯 Go 重排: %v", err)
	}
	if ok, _ := isFastStartMP4(dest); !ok {
		t.Fatal("重排产物未达标")
	}
	if ff != "" { // 真夹具才有可解码内容：解码完整性=stco 修正正确性的硬证
		dec := exec.Command(ff, "-v", "error", "-i", dest, "-f", "null", "-")
		if out, err := dec.CombinedOutput(); err != nil {
			t.Fatalf("重排后解码失败: %v\n%s", err, out)
		}
	}

	// ②ffmpeg 封装路径（argv 直传，中文空格路径不受 shell 干扰）
	if ff != "" {
		dest2 := filepath.Join(dir, "封装 产物.m4a")
		if err := remuxFastStart(ff, src, dest2); err != nil {
			t.Fatalf("ffmpeg 封装: %v", err)
		}
		if ok, _ := isFastStartMP4(dest2); !ok {
			t.Fatal("封装产物未达标")
		}
	}

	// ③目录级存量修复：目录名/文件名带中文空格一起过（dest/dest2 已达标跳过）
	if fixed := RepairDirFastStart(dir); fixed != 1 {
		t.Fatalf("修复数=%d，want 1", fixed)
	}
	if ok, err := isFastStartMP4(src); err != nil || !ok {
		t.Fatalf("修复后未达标: ok=%v err=%v", ok, err)
	}
}

// TestRemuxOrMovePureGoFallback ffmpeg 缺席桩 + 真实尾部 moov 输入：纯 Go
// 重排兜底成功。
func TestRemuxOrMovePureGoFallback(t *testing.T) {
	withFFmpegStub(t, "")
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.m4a")
	tmp := filepath.Join(dir, "raw.part")
	if err := os.WriteFile(tmp, tailMoovFixture(t), 0o644); err != nil {
		t.Fatal(err)
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
}

