package bilibili

// fMP4 → 标准 MP4 纯 Go 合并器：B 站 DASH 音频原始流是 fragmented MP4
// （ftyp+moov(mvex)+sidx+[moof+mdat]*），流式播放器支持差、部分播放器要整段
// 拉完才播。本文件把分片合并为非 fragmented 标准 MP4：init moov 填充采样表
// （stts/stsc/stsz/stco，剔 mvex/sidx），全部采样拼接进单一 mdat——语义同
// ffmpeg -c copy -movflags +faststart，仅限单音轨（B 站音频恒为单轨 AAC）。

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// fragTables 合并产物的新采样表。
type fragTables struct {
	sizes     []uint32
	durations []uint32
}

// patchStcoInMoov 在建好的 moov 字节里定位唯一的 stco 并回填 chunk 偏移
// （stco 值依赖 moov 总长，故构建后统一回填）。
func patchStcoInMoov(moov []byte, val uint32) error {
	var patch func(typ string, body []byte) error
	found := false
	patch = func(typ string, body []byte) error {
		if typ == "stco" {
			if found || len(body) < 8 {
				return errors.New("stco 异常")
			}
			if binary.BigEndian.Uint32(body[0:4]) != 0 || binary.BigEndian.Uint32(body[4:8]) != 1 {
				return errors.New("stco 结构与预期不符")
			}
			binary.BigEndian.PutUint32(body[8:12], val)
			found = true
			return nil
		}
		if moovContainers[typ] || typ == "moov" {
			return walkBoxes(body, patch)
		}
		return nil
	}
	if err := walkBoxes(moov, patch); err != nil {
		return err
	}
	if !found {
		return errors.New("产物 moov 无 stco")
	}
	return nil
}

func buildBox(typ string, body []byte) []byte {
	out := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(8+len(body)))
	copy(out[4:8], typ)
	copy(out[8:], body)
	return out
}

func buildFullBox(typ string, version uint8, flags uint32, payload []byte) []byte {
	head := []byte{0, 0, 0, 0}
	binary.BigEndian.PutUint32(head, flags&0xffffff|uint32(version)<<24)
	return buildBox(typ, append(head, payload...))
}

// rebuildStbl 重填采样表：保留 stsd 与未知 box（原序），丢弃旧 stts/stsc/
// stsz/stco，末尾按规范序追加新表。
func rebuildStbl(body []byte, tbl *fragTables) ([]byte, error) {
	var out []byte
	err := walkBoxes(body, func(typ string, b []byte) error {
		switch typ {
		case "stts", "stsc", "stsz", "stco":
			return nil // 旧空表丢弃，末尾统一重建
		case "stsd":
			out = append(out, buildBox(typ, b)...)
			return nil
		default:
			out = append(out, buildBox(typ, b)...)
			return nil
		}
	})
	if err != nil {
		return nil, err
	}
	// stts：等时长段合并为 (count, delta) 游程
	var runs [][2]uint32
	for _, d := range tbl.durations {
		if n := len(runs); n > 0 && runs[n-1][1] == d {
			runs[n-1][0]++
		} else {
			runs = append(runs, [2]uint32{1, d})
		}
	}
	var sttsPayload []byte
	sttsPayload = binary.BigEndian.AppendUint32(sttsPayload, uint32(len(runs)))
	for _, r := range runs {
		sttsPayload = binary.BigEndian.AppendUint32(sttsPayload, r[0])
		sttsPayload = binary.BigEndian.AppendUint32(sttsPayload, r[1])
	}
	out = append(out, buildFullBox("stts", 0, 0, sttsPayload)...)
	// stsc：单一 chunk 装下全部采样
	var stscPayload []byte
	stscPayload = binary.BigEndian.AppendUint32(stscPayload, 1) // entry_count
	stscPayload = binary.BigEndian.AppendUint32(stscPayload, 1) // first_chunk
	stscPayload = binary.BigEndian.AppendUint32(stscPayload, uint32(len(tbl.sizes)))
	stscPayload = binary.BigEndian.AppendUint32(stscPayload, 1) // sample_description_index
	out = append(out, buildFullBox("stsc", 0, 0, stscPayload)...)
	// stsz：变长采样（sample_size=0）
	var stszPayload []byte
	stszPayload = binary.BigEndian.AppendUint32(stszPayload, 0)
	stszPayload = binary.BigEndian.AppendUint32(stszPayload, uint32(len(tbl.sizes)))
	for _, s := range tbl.sizes {
		stszPayload = binary.BigEndian.AppendUint32(stszPayload, s)
	}
	out = append(out, buildFullBox("stsz", 0, 0, stszPayload)...)
	// stco：单 chunk 偏移占位（依赖 moov 总长，buildMoov 后统一回填）
	var stcoPayload []byte
	stcoPayload = binary.BigEndian.AppendUint32(stcoPayload, 1)
	stcoPayload = binary.BigEndian.AppendUint32(stcoPayload, 0) // 占位
	out = append(out, buildFullBox("stco", 0, 0, stcoPayload)...)
	return out, nil
}

// rebuildMoovBody 递归重建 moov 体：剔 mvex 与 edts（fragmented init 的
// elst segment_duration=0 会让 demuxer 把整轨判 discard 只吐 1 包；media_time
// 恒为 0，剔掉即等效无编辑列表），stbl 重填。
func rebuildMoovBody(body []byte, tbl *fragTables) ([]byte, error) {
	var out []byte
	err := walkBoxes(body, func(typ string, b []byte) error {
		switch typ {
		case "mvex", "edts":
			return nil
		case "trak", "mdia", "minf":
			inner, err := rebuildMoovBody(b, tbl)
			if err != nil {
				return err
			}
			out = append(out, buildBox(typ, inner)...)
			return nil
		case "stbl":
			inner, err := rebuildStbl(b, tbl)
			if err != nil {
				return err
			}
			out = append(out, buildBox("stbl", inner)...)
			return nil
		default:
			out = append(out, buildBox(typ, b)...)
			return nil
		}
	})
	return out, err
}

// parseFragments 扫全部 moof，收集采样尺寸/时长与各采样在 src 内的绝对偏移
// 基准。返回每片 (采样区起点, 采样字节总长) 供流式拷贝。
func parseFragments(f *os.File, atoms []mp4Atom, fileSize int64) (starts []int64, lengths []int64, tbl *fragTables, err error) {
	// trex 默认值（mvex 在场时）
	var defDur, defSize uint32
	var moovSeen bool
	for i := range atoms {
		a := &atoms[i]
		if a.typ != "moov" {
			continue
		}
		if moovSeen {
			return nil, nil, nil, errors.New("多个 moov")
		}
		moovSeen = true
		buf := make([]byte, a.size-a.hdr)
		if _, err := f.ReadAt(buf, a.off+a.hdr); err != nil {
			return nil, nil, nil, err
		}
		var traks int
		err := walkBoxes(buf, func(typ string, b []byte) error {
			if typ == "trak" {
				traks++
			}
			if typ == "trex" && len(b) >= 24 {
				defDur = binary.BigEndian.Uint32(b[12:16])
				defSize = binary.BigEndian.Uint32(b[16:20])
			}
			return nil
		})
		if err != nil {
			return nil, nil, nil, err
		}
		if traks != 1 {
			return nil, nil, nil, fmt.Errorf("音轨数 %d ≠ 1，纯 Go 合并不适用", traks)
		}
	}
	if !moovSeen {
		return nil, nil, nil, errors.New("缺 init moov")
	}

	tbl = &fragTables{}
	for i := range atoms {
		a := &atoms[i]
		if a.typ != "moof" {
			continue
		}
		buf := make([]byte, a.size)
		if _, err := f.ReadAt(buf, a.off); err != nil {
			return nil, nil, nil, err
		}
		base := a.off // default-base-is-moof 缺省基准
		var tfDur, tfSize uint32
		var tfDurSet, tfSizeSet bool
		var fragStart, fragLen int64 = -1, 0
		err := walkBoxes(buf[8:], func(typ string, b []byte) error {
			switch typ {
			case "traf":
				return walkBoxes(b, func(t2 string, b2 []byte) error {
					switch t2 {
					case "tfhd":
						if len(b2) < 8 {
							return errors.New("tfhd 过短")
						}
						flags := binary.BigEndian.Uint32(b2[0:4]) & 0xffffff
						p := 4
						p += 4 // track_id
						if flags&0x1 != 0 {
							if len(b2) < p+8 {
								return errors.New("tfhd 截断")
							}
							base = int64(binary.BigEndian.Uint64(b2[p : p+8]))
							p += 8
						}
						if flags&0x2 != 0 {
							p += 4
						}
						if flags&0x8 != 0 {
							if len(b2) < p+4 {
								return errors.New("tfhd 截断")
							}
							tfDur, tfDurSet = binary.BigEndian.Uint32(b2[p:p+4]), true
							p += 4
						}
						if flags&0x10 != 0 {
							if len(b2) < p+4 {
								return errors.New("tfhd 截断")
							}
							tfSize, tfSizeSet = binary.BigEndian.Uint32(b2[p:p+4]), true
							p += 4
						}
						if flags&0x20 != 0 {
							p += 4
						}
					case "trun":
						if len(b2) < 8 {
							return errors.New("trun 截断")
						}
						flags := binary.BigEndian.Uint32(b2[0:4]) & 0xffffff
						n := int(binary.BigEndian.Uint32(b2[4:8]))
						p := 8
						var dataOff int64
						if flags&0x1 != 0 {
							if len(b2) < p+4 {
								return errors.New("trun 截断")
							}
							dataOff = int64(int32(binary.BigEndian.Uint32(b2[p : p+4])))
							p += 4
						} else {
							return errors.New("trun 无 data_offset")
						}
						if flags&0x4 != 0 {
							p += 4
						}
						sampleStart := base + dataOff
						for k := 0; k < n; k++ {
							var dur, size uint32
							if flags&0x100 != 0 {
								if len(b2) < p+4 {
									return errors.New("trun 截断")
								}
								dur = binary.BigEndian.Uint32(b2[p : p+4])
								p += 4
							}
							if flags&0x200 != 0 {
								if len(b2) < p+4 {
									return errors.New("trun 截断")
								}
								size = binary.BigEndian.Uint32(b2[p : p+4])
								p += 4
							}
							if flags&0x400 != 0 {
								p += 4
							}
							if flags&0x800 != 0 {
								p += 4
							}
							if size == 0 {
								if tfSizeSet {
									size = tfSize
								} else if defSize != 0 {
									size = defSize
								} else {
									return errors.New("采样尺寸无从确定")
								}
							}
							if dur == 0 {
								if tfDurSet {
									dur = tfDur
								} else if defDur != 0 {
									dur = defDur
								} else {
									return errors.New("采样时长无从确定")
								}
							}
							tbl.sizes = append(tbl.sizes, size)
							tbl.durations = append(tbl.durations, dur)
							fragLen += int64(size)
						}
						if fragStart < 0 {
							fragStart = sampleStart
						}
					}
					return nil
				})
			}
			return nil
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("moof@%d: %w", a.off, err)
		}
		if fragStart < 0 {
			continue // 空片
		}
		if fragStart+fragLen > fileSize {
			return nil, nil, nil, fmt.Errorf("moof@%d 采样区越界（%d+%d > %d）", a.off, fragStart, fragLen, fileSize)
		}
		starts = append(starts, fragStart)
		lengths = append(lengths, fragLen)
	}
	if len(starts) == 0 {
		return nil, nil, nil, errors.New("无有效分片")
	}
	return starts, lengths, tbl, nil
}

// fillDurations 回填时长：fragmented init 的 mvhd/tkhd/mdhd duration 恒为
// 0，不填则 stream duration=0（网页播放器无法拖进度）。mediaTicks=采样总
// 时长（媒体时基），movieTicks 按 mvhd 时基折算。
func fillDurations(moovBody []byte, mediaTicks uint64) error {
	var mvhdTS, mdhdTS uint64
	walkRead := func(typ string, body []byte) {
		if len(body) < 4 {
			return
		}
		ver := body[0]
		switch typ {
		case "mvhd": // v0: vf4 cre4 mod4 ts4 dur4；v1: vf4 cre8 mod8 ts4 dur8
			if ver == 0 && len(body) >= 20 {
				mvhdTS = uint64(binary.BigEndian.Uint32(body[12:16]))
			} else if ver == 1 && len(body) >= 32 {
				mvhdTS = uint64(binary.BigEndian.Uint32(body[20:24]))
			}
		case "mdhd":
			if ver == 0 && len(body) >= 20 {
				mdhdTS = uint64(binary.BigEndian.Uint32(body[12:16]))
			} else if ver == 1 && len(body) >= 32 {
				mdhdTS = uint64(binary.BigEndian.Uint32(body[20:24]))
			}
		}
	}
	var walk1, walk2 func(typ string, body []byte) error
	walk1 = func(typ string, body []byte) error {
		if len(body) < 4 {
			return nil
		}
		walkRead(typ, body)
		if typ == "trak" || typ == "mdia" || typ == "minf" {
			return walkBoxes(body, walk1)
		}
		return nil
	}
	if err := walkBoxes(moovBody, walk1); err != nil {
		return err
	}
	if mdhdTS == 0 || mvhdTS == 0 {
		return errors.New("时基缺失")
	}
	movieTicks := mediaTicks * mvhdTS / mdhdTS
	write := func(typ string, body []byte) {
		if len(body) < 4 {
			return
		}
		ver := body[0]
		switch typ {
		case "mvhd":
			if ver == 0 && len(body) >= 20 {
				binary.BigEndian.PutUint32(body[16:20], uint32(movieTicks))
			} else if ver == 1 && len(body) >= 32 {
				binary.BigEndian.PutUint64(body[24:32], movieTicks)
			}
		case "tkhd": // v0: vf4 cre4 mod4 track4 res4 dur4；v1: vf4 cre8 mod8 track4 res4 dur8
			if ver == 0 && len(body) >= 24 {
				binary.BigEndian.PutUint32(body[20:24], uint32(movieTicks))
			} else if ver == 1 && len(body) >= 36 {
				binary.BigEndian.PutUint64(body[28:36], movieTicks)
			}
		case "mdhd":
			if ver == 0 && len(body) >= 20 {
				binary.BigEndian.PutUint32(body[16:20], uint32(mediaTicks))
			} else if ver == 1 && len(body) >= 32 {
				binary.BigEndian.PutUint64(body[24:32], mediaTicks)
			}
		}
	}
	walk2 = func(typ string, body []byte) error {
		if len(body) < 4 {
			return nil
		}
		write(typ, body)
		if typ == "trak" || typ == "mdia" || typ == "minf" {
			return walkBoxes(body, walk2)
		}
		return nil
	}
	return walkBoxes(moovBody, walk2)
}

// mergeFragments 把 fragmented MP4（B 站 DASH 音频直存形态）合并为非
// fragmented 标准 MP4：ftyp + 重填采样表的 moov + 单一 mdat（全部采样按序
// 拼接）。err=nil 保证 dest 达标（读头复核）。
func mergeFragments(src, dest string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()
	st, err := sf.Stat()
	if err != nil {
		return err
	}
	atoms, err := indexTopAtoms(sf)
	if err != nil {
		return err
	}
	var ftyp *mp4Atom
	var moov *mp4Atom
	hasFrag := false
	for i := range atoms {
		switch atoms[i].typ {
		case "ftyp":
			if ftyp == nil {
				ftyp = &atoms[i]
			}
		case "moov":
			if moov == nil {
				moov = &atoms[i]
			}
		case "moof":
			hasFrag = true
		}
	}
	if moov == nil || !hasFrag {
		return errors.New("非 fragmented MP4")
	}
	starts, lengths, tbl, err := parseFragments(sf, atoms, st.Size())
	if err != nil {
		return err
	}

	// init moov 体重建（先按采样总时长回填 mvhd/tkhd/mdhd，再剔盒重填表）
	var mediaTicks uint64
	for _, d := range tbl.durations {
		mediaTicks += uint64(d)
	}
	moovBody := make([]byte, moov.size-moov.hdr)
	if _, err := sf.ReadAt(moovBody, moov.off+moov.hdr); err != nil {
		return err
	}
	if err := fillDurations(moovBody, mediaTicks); err != nil {
		return err
	}
	newBody, err := rebuildMoovBody(moovBody, tbl)
	if err != nil {
		return err
	}
	newMoov := buildBox("moov", newBody)

	// 组装：ftyp + moov + mdat；mdat 绝对偏移 = ftyp+moov+8
	var ftypBytes []byte
	if ftyp != nil {
		ftypBytes = make([]byte, ftyp.size)
		if _, err := sf.ReadAt(ftypBytes, ftyp.off); err != nil {
			return err
		}
	}
	mdatOff := int64(len(ftypBytes)) + int64(len(newMoov)) + 8
	var total int64
	for _, l := range lengths {
		total += l
	}
	if total == 0 {
		return errors.New("采样总长为 0")
	}
	if total+8 > 0xFFFFFFFF {
		return errors.New("合并后 mdat 超过 32 位上限")
	}
	if err := patchStcoInMoov(newMoov, uint32(mdatOff)); err != nil {
		return err
	}

	df, err := os.Create(dest)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(df, 1<<20)
	fail := func(err error) error { df.Close(); os.Remove(dest); return err }
	if len(ftypBytes) > 0 {
		if _, err := w.Write(ftypBytes); err != nil {
			return fail(err)
		}
	}
	if _, err := w.Write(newMoov); err != nil {
		return fail(err)
	}
	mdatHead := make([]byte, 8)
	binary.BigEndian.PutUint32(mdatHead[:4], uint32(total+8))
	copy(mdatHead[4:8], "mdat")
	if _, err := w.Write(mdatHead); err != nil {
		return fail(err)
	}
	for i := range starts {
		if _, err := sf.Seek(starts[i], io.SeekStart); err != nil {
			return fail(err)
		}
		if _, err := io.CopyN(w, sf, lengths[i]); err != nil {
			return fail(err)
		}
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
		return errors.New("合并产物校验未过")
	}
	return nil
}
