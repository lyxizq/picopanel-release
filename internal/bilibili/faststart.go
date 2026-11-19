package bilibili

// faststart 保障与存量修复：MP4 的 moov 索引在文件尾部时，流式播放要先跳到
// 文件尾读索引才能开播，部分播放器干脆整段拉完才出声。三层保障：
// ①ffmpeg -movflags +faststart 无损封装（首选，兼容 fragmented 流）并读头校验；
// ②纯 Go moov 前置重排（ffmpeg 缺席时的兜底：搬 moov 到 mdat 前并修正
// stco/co64 绝对偏移——不依赖任何外部命令，agent 机 PATH 里有没有 ffmpeg 都能修）；
// ③存量非达标 m4a 在每次追更任务收尾时重封装修复（幂等，达标的跳过）。

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ffmpegCandidates PATH 找不到 ffmpeg 时依次探测的常见位置。飞牛 fnOS 的
// mediasrv 版 ffmpeg 在 /usr/trim——agent 守护进程的默认 PATH 不含它，曾致
// LookPath 失败静默回退直存（存量 moov 在尾部问题的根因）。纯 Go 重排兜底
// 后，ffmpeg 找不到只影响 fragmented 流的罕见场景。
var ffmpegCandidates = []string{
	"/usr/trim/bin/ffmpeg",
	"/usr/trim/lib/mediasrv/bin/ffmpeg",
	"/usr/local/bin/ffmpeg",
	"/usr/bin/ffmpeg",
	"/opt/bin/ffmpeg",
	"/opt/homebrew/bin/ffmpeg",
}

// findFFmpeg 定位 ffmpeg：先 PATH，再候选路径探测；都无返回空串。
func findFFmpeg() string {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	for _, p := range ffmpegCandidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// ffmpegLookup 可注入（测试桩）。
var ffmpegLookup = findFFmpeg

// mp4Atom 顶层 atom 索引项。
type mp4Atom struct {
	typ  string
	off  int64 // atom 起始（含头）
	hdr  int64 // 头长度（8 或 largesize 的 16）
	size int64 // 含头的总大小
}

// indexTopAtoms 顺序扫顶层 atom 索引（上限 128 个防御损坏文件）。
func indexTopAtoms(f *os.File) ([]mp4Atom, error) {
	var atoms []mp4Atom
	var hdr [8]byte
	off := int64(0)
	for i := 0; i < 128; i++ {
		if _, err := f.ReadAt(hdr[:], off); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		a := mp4Atom{typ: string(hdr[4:8]), off: off, hdr: 8}
		a.size = int64(binary.BigEndian.Uint32(hdr[:4]))
		if a.size == 1 { // largesize：真 64 位大小在后 8 字节
			var l [8]byte
			if _, err := f.ReadAt(l[:], off+8); err != nil {
				return nil, err
			}
			a.size = int64(binary.BigEndian.Uint64(l[:]))
			a.hdr = 16
		}
		if a.size < a.hdr { // size==0（延伸到文件尾）或损坏：到头了，索引到此前为止
			break
		}
		atoms = append(atoms, a)
		off += a.size
	}
	return atoms, nil
}

// isFastStartMP4 只读文件头判断 moov 是否在 mdat 之前（faststart）。不解析
// 采样表：非 MP4/损坏文件按不达标处理（err 非 nil 仅 IO 异常）。moov 在而
// mdat 无（异常但无害）视为达标；mdat 在而 moov 无（如半截下载）不达标。
func isFastStartMP4(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	atoms, err := indexTopAtoms(f)
	if err != nil {
		return false, err
	}
	var moovOff, mdatOff int64 = -1, -1
	for _, a := range atoms {
		if a.typ == "moov" && moovOff < 0 {
			moovOff = a.off
		}
		if a.typ == "mdat" && mdatOff < 0 {
			mdatOff = a.off
		}
	}
	return moovOff >= 0 && (mdatOff < 0 || moovOff < mdatOff), nil
}

// remuxFastStart 用 ffmpeg 把 src 无损封装（-c copy 不重编码）为 faststart
// 标准 m4a 写到 dest，并读头校验产物确实 moov 前置——err=nil 才算 dest 达标。
func remuxFastStart(ff, src, dest string) error {
	cmd := exec.Command(ff, "-y", "-loglevel", "error", "-i", src,
		"-map", "0:a", "-c:a", "copy", "-movflags", "+faststart", dest)
	if err := cmd.Run(); err != nil {
		return err
	}
	ok, err := isFastStartMP4(dest)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("封装产物校验未过（moov 未前置）")
	}
	return nil
}

// ensureFastStartFile 把 src 转成 faststart m4a 写到 dest：先 ffmpeg（兼容
// fragmented 流），缺席/失败回退纯 Go moov 重排。err=nil 保证 dest 达标。
func ensureFastStartFile(src, dest string) error {
	if ff := ffmpegLookup(); ff != "" {
		if err := remuxFastStart(ff, src, dest); err == nil {
			return nil
		}
	}
	return relocateMoov(src, dest)
}

// moov 内需要递归下钻的容器 box（stco/co64 固定在 moov/trak/mdia/minf/stbl 下）。
var moovContainers = map[string]bool{
	"moov": true, "trak": true, "mdia": true, "minf": true, "stbl": true,
}

// walkBoxes 遍历一层 box，对每个子 box 调 visit（body=去头的 box 体）。
func walkBoxes(b []byte, visit func(typ string, body []byte) error) error {
	off := 0
	for off+8 <= len(b) {
		size := int64(binary.BigEndian.Uint32(b[off : off+4]))
		typ := string(b[off+4 : off+8])
		hdr := 8
		if size == 1 {
			if off+16 > len(b) {
				return io.ErrUnexpectedEOF
			}
			size = int64(binary.BigEndian.Uint64(b[off+8 : off+16]))
			hdr = 16
		} else if size == 0 {
			size = int64(len(b) - off)
		}
		if size < int64(hdr) || off+int(size) > len(b) {
			return fmt.Errorf("box %q 越界（size=%d 剩余=%d）", typ, size, len(b)-off)
		}
		if err := visit(typ, b[off+hdr:off+int(size)]); err != nil {
			return err
		}
		off += int(size)
	}
	return nil
}

// patchBox 递归修 moov 树：stco/co64 的采样绝对偏移 + delta（mdat 整体后移
// delta 后的新位置）。
func patchBox(typ string, body []byte, delta int64) error {
	if moovContainers[typ] {
		return walkBoxes(body, func(t string, b []byte) error { return patchBox(t, b, delta) })
	}
	switch typ {
	case "stco": // version/flags(4) + entry_count(4) + uint32 偏移×n
		if len(body) < 8 {
			return errors.New("stco 过短")
		}
		n := int(binary.BigEndian.Uint32(body[4:8]))
		for i := 0; i < n; i++ {
			p := 8 + i*4
			if p+4 > len(body) {
				return errors.New("stco 偏移表越界")
			}
			v := int64(binary.BigEndian.Uint32(body[p : p+4]))
			if v+delta < 0 || v+delta > 0xFFFFFFFF {
				return fmt.Errorf("stco 偏移溢出（%d+%d）", v, delta)
			}
			binary.BigEndian.PutUint32(body[p:p+4], uint32(v+delta))
		}
	case "co64": // version/flags(4) + entry_count(4) + uint64 偏移×n
		if len(body) < 8 {
			return errors.New("co64 过短")
		}
		n := int(binary.BigEndian.Uint32(body[4:8]))
		for i := 0; i < n; i++ {
			p := 8 + i*8
			if p+8 > len(body) {
				return errors.New("co64 偏移表越界")
			}
			v := int64(binary.BigEndian.Uint64(body[p : p+8]))
			if v+delta < 0 {
				return fmt.Errorf("co64 偏移溢出（%d+%d）", v, delta)
			}
			binary.BigEndian.PutUint64(body[p:p+8], uint64(v+delta))
		}
	}
	return nil
}

// relocateMoov 纯 Go moov 前置：把 src（非 fragmented 标准 MP4，moov 在 mdat
// 之后——B 站音频直存流的形态）重排为 [ftyp][moov'][其余 atom 原序] 写到
// dest。moov 搬到 mdat 前使 mdat 整体后移 moov 大小，故树内全部 stco/co64
// 偏移 +delta。fragmented（moof）/带 sidx 索引的流无法这样重排，直接报错
// 交给上层 ffmpeg 路径。err=nil 保证 dest 已达标（读头复核）。
func relocateMoov(src, dest string) error {
	const maxMoovBytes = 16 << 20 // 音频 moov 实际几 KB，上限防御异常文件
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()
	atoms, err := indexTopAtoms(sf)
	if err != nil {
		return err
	}
	var moov *mp4Atom
	var firstMDat = int64(-1)
	for i := range atoms {
		a := &atoms[i]
		switch {
		case a.typ == "moov":
			if moov != nil {
				return errors.New("多个 moov，不重排")
			}
			moov = a
		case a.typ == "mdat" && firstMDat < 0:
			firstMDat = a.off
		case a.typ == "moof" || a.typ == "sidx":
			return errors.New("fragmented/带索引流，纯 Go 重排不适用")
		}
	}
	if moov == nil || moov.size > maxMoovBytes {
		return errors.New("无 moov 或过大")
	}
	if firstMDat >= 0 && moov.off < firstMDat {
		return errors.New("moov 已在 mdat 前")
	}
	delta := moov.size

	buf := make([]byte, moov.size)
	if _, err := sf.ReadAt(buf, moov.off); err != nil {
		return err
	}
	if err := patchBox("moov", buf[moov.hdr:], delta); err != nil {
		return fmt.Errorf("moov 偏移修正失败: %w", err)
	}

	df, err := os.Create(dest)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(df, 1<<20)
	writeAtom := func(a *mp4Atom) error {
		if _, err := sf.Seek(a.off, io.SeekStart); err != nil {
			return err
		}
		_, err := io.CopyN(w, sf, a.size)
		return err
	}
	// moov 插到 ftyp 之后（无 ftyp 则置首），其余 atom 保持原序
	inserted := false
	fail := func(err error) error { df.Close(); os.Remove(dest); return err }
	for i := range atoms {
		a := &atoms[i]
		if a.typ == "moov" {
			continue
		}
		// 插入点 = ftyp 之后（无 ftyp 则首个 atom 之前）
		if !inserted && a.typ != "ftyp" && (i == 0 || atoms[i-1].typ == "ftyp") {
			if _, err := w.Write(buf); err != nil {
				return fail(err)
			}
			inserted = true
		}
		if err := writeAtom(a); err != nil {
			return fail(err)
		}
	}
	if !inserted {
		return fail(errors.New("未找到 moov 插入点"))
	}
	if err := w.Flush(); err != nil {
		return fail(err)
	}
	if err := df.Close(); err != nil {
		os.Remove(dest)
		return err
	}
	if ok, err := isFastStartMP4(dest); err != nil || !ok {
		os.Remove(dest)
		return errors.New("重排产物校验未过")
	}
	return nil
}

// RepairDirFastStart 修复目录内的存量 m4a：逐文件读头检查，非 faststart 的
// 重封装替换（临时文件+改名，失败保留原样不破坏文件）。新下载已由封装校验
// 保证达标，本函数只在追更任务收尾跑一遍兜底。返回修复文件数。
func RepairDirFastStart(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	fixed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".m4a") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if ok, err := isFastStartMP4(p); err != nil || ok {
			continue // 读不了的不动；已达标跳过
		}
		// 临时文件须 .m4a 结尾（ffmpeg 按输出扩展名猜封装器）；点前缀隐藏
		tmp := filepath.Join(dir, "."+e.Name()+".fs.m4a")
		if err := ensureFastStartFile(p, tmp); err != nil {
			os.Remove(tmp)
			log.Printf("B站追更存量修复失败（%s）: %v", e.Name(), err)
			continue
		}
		if err := os.Rename(tmp, p); err != nil {
			os.Remove(tmp)
			log.Printf("B站追更存量修复替换失败（%s）: %v", e.Name(), err)
			continue
		}
		log.Printf("B站追更存量修复：%s 已重封装为 faststart", e.Name())
		fixed++
	}
	return fixed
}
