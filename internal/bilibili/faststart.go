package bilibili

// faststart 保障与存量修复：MP4 的 moov 索引在文件尾部时，流式播放要先跳到
// 文件尾读索引才能开播，部分播放器干脆整段拉完才出声。音频下载用 ffmpeg
// -movflags +faststart 封装并读头校验产物；存量非达标 m4a 在每次追更任务
// 收尾时重封装修复（幂等，达标的跳过）。

import (
	"encoding/binary"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ffmpegCandidates PATH 找不到 ffmpeg 时依次探测的常见位置。飞牛 fnOS 的
// mediasrv 版 ffmpeg 在 /usr/trim/bin——agent 守护进程的默认 PATH 不含它，
// LookPath 失败曾导致静默回退直存（存量 moov 在尾部问题的根因）。
var ffmpegCandidates = []string{
	"/usr/trim/bin/ffmpeg",
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

// isFastStartMP4 只读文件头判断 moov 是否在 mdat 之前（faststart）。不解析
// 采样表：非 MP4/损坏文件按不达标处理（err 非 nil 仅 IO 异常）。moov 在而
// mdat 无（异常但无害）视为达标；mdat 在而 moov 无（如半截下载）不达标。
func isFastStartMP4(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var moovOff, mdatOff int64 = -1, -1
	var hdr [8]byte
	off := int64(0)
	for i := 0; i < 128; i++ { // 上限防御：正常文件头几个 atom 内必有结论
		if _, err := f.ReadAt(hdr[:], off); err != nil {
			break // EOF/短读：顶部 atom 扫完
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		if size == 1 { // largesize：真 64 位大小在后 8 字节
			var l [8]byte
			if _, err := f.ReadAt(l[:], off+8); err != nil {
				break
			}
			size = int64(binary.BigEndian.Uint64(l[:]))
		}
		if size <= 0 { // size==0（延伸到文件尾）或损坏：到头了
			break
		}
		switch typ {
		case "moov":
			if moovOff < 0 {
				moovOff = off
			}
		case "mdat":
			if mdatOff < 0 {
				mdatOff = off
			}
		}
		if moovOff >= 0 && mdatOff >= 0 {
			break
		}
		off += size
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

// RepairDirFastStart 修复目录内的存量 m4a：逐文件读头检查，非 faststart 的
// 重封装替换（临时文件+改名，失败保留原样不破坏文件）。新下载已由封装校验
// 保证达标，本函数只在追更任务收尾跑一遍兜底。返回修复文件数。
func RepairDirFastStart(dir string) int {
	ff := ffmpegLookup()
	if ff == "" {
		log.Printf("B站追更存量修复：本机未找到 ffmpeg，跳过（装好后下次追更自动重试）")
		return 0
	}
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
		// ffmpeg 按输出扩展名猜封装器，临时文件必须 .m4a 结尾；点前缀隐藏
		tmp := filepath.Join(dir, "."+e.Name()+".fs.m4a")
		if err := remuxFastStart(ff, p, tmp); err != nil {
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
