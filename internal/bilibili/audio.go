package bilibili

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// audioPickPreference 首选音质档 id（320k → 132k → 64k）；杜比/Hi-Res（30250/
// 30251）要大会员且常伴 403，仅在别无选择时兜底。实际取 audio 流里带宽最高者，
// 该清单只用来剔除会员档。
var audioPickPreference = map[int64]bool{30280: true, 30232: true, 30216: true}

// AudioStream 取一条稿件的最佳音频流地址（含备用地址，下载时逐个回退）。
// 未登录最高 64k；服务端配置 SESSDATA 后可拿到 132k/320k。
func (c *Client) AudioStream(bvid string) (cid int64, urls []string, err error) {
	params := map[string]string{"bvid": bvid}
	var view struct {
		CID   int64  `json:"cid"`
		Title string `json:"title"`
	}
	if err := c.fetchData("/x/web-interface/view", params, &view); err != nil {
		return 0, nil, fmt.Errorf("稿件信息获取失败: %w", err)
	}
	if view.CID == 0 {
		return 0, nil, errors.New("稿件缺少 cid（可能不可见）")
	}
	play := map[string]string{
		"bvid":  bvid,
		"cid":   fmt.Sprint(view.CID),
		"qn":    "64",
		"fnval": "16", // DASH
		"fnver": "0",
		"fourk": "1",
	}
	var data struct {
		Dash struct {
			Audio []struct {
				ID        int64    `json:"id"`
				BaseURL   string   `json:"base_url"`
				BackupURL []string `json:"backup_url"`
				Bandwidth int64    `json:"bandwidth"`
			} `json:"audio"`
		} `json:"dash"`
		DURL []struct {
			URL       string `json:"url"`
			BackupURL []string `json:"backup_url"`
		} `json:"durl"`
	}
	if err := c.fetchWBI("/x/player/wbi/playurl", play, &data); err != nil {
		return view.CID, nil, fmt.Errorf("播放地址获取失败: %w", err)
	}
	// 先剔会员档，再按带宽取最高；一条都没有时看 durl（老接口直链=音视混流，无法抽音轨）
	audios := data.Dash.Audio
	if len(audios) == 0 {
		if len(data.DURL) > 0 {
			return view.CID, nil, errors.New("该视频无独立音频流（durl 混流），无法转 m4a")
		}
		return view.CID, nil, errors.New("响应里没有音频流")
	}
	best := -1
	for i, a := range audios {
		if !audioPickPreference[a.ID] {
			continue
		}
		if best < 0 || a.Bandwidth > audios[best].Bandwidth {
			best = i
		}
	}
	if best < 0 { // 只有会员档：兜底取带宽最高（有 SESSDATA 大会员时可用）
		for i, a := range audios {
			if best < 0 || a.Bandwidth > audios[best].Bandwidth {
				best = i
			}
		}
	}
	urls = append([]string{audios[best].BaseURL}, audios[best].BackupURL...)
	return view.CID, urls, nil
}

// fetchData 无签名 GET 并解析 Data 到 out。
func (c *Client) fetchData(path string, params map[string]string, out any) error {
	raw, err := c.getJSON(path, toValues(params))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// fetchWBI 带 WBI 签名 GET 并解析 Data 到 out。
func (c *Client) fetchWBI(path string, params map[string]string, out any) error {
	raw, err := c.getWBIJSON(path, toValues(params))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// DownloadAudio 下载一条稿件音频到目录，返回（文件名, 状态, 错误）。状态
// downloaded=本次下载 / exists=目标文件已存在跳过（写保护防同名覆盖，非增量
// 判断——增量由服务端已抓清单决定）。文件名=发布日期_标题.m4a（TODO 4 用户
// 口径）；有 ffmpeg 时无损封装成标准 m4a（moov 前置），没有则音频流直存。
func (c *Client) DownloadAudio(dir string, it Item) (string, string, error) {
	dest := filepath.Join(dir, FileNameFor(it.PubDate, it.Title))
	if _, err := os.Stat(dest); err == nil {
		return filepath.Base(dest), "exists", nil
	}
	cid, urls, err := c.AudioStream(it.BVID)
	if err != nil {
		return "", "", err
	}
	_ = cid
	tmp := dest + ".part"
	var lastErr error
	for _, u := range urls {
		if u == "" {
			continue
		}
		if err := c.fetchToFile(u, tmp); err != nil {
			lastErr = err
			continue
		}
		if err := remuxOrMove(tmp, dest); err != nil {
			lastErr = err
			continue
		}
		return filepath.Base(dest), "downloaded", nil
	}
	os.Remove(tmp)
	if lastErr == nil {
		lastErr = errors.New("无可用音频地址")
	}
	return "", "", lastErr
}

// fetchToFile CDN 下载（Referer 必带，B 站 CDN 校验）。
func (c *Client) fetchToFile(url, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	if c.Cookie != "" {
		req.Header.Set("Cookie", c.Cookie)
	}
	resp, err := c.DL.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CDN HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(dest)
		return fmt.Errorf("音频下载中断: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(dest)
		return closeErr
	}
	return nil
}

// remuxOrMove ffmpeg 在场就无损封装成标准 m4a（-c copy 秒级，不重编码），
// 失败/缺席回退为流直存。
func remuxOrMove(tmp, dest string) error {
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		cmd := exec.Command(ff, "-y", "-loglevel", "error", "-i", tmp,
			"-map", "0:a", "-c:a", "copy", "-movflags", "+faststart", dest)
		if err := cmd.Run(); err == nil {
			os.Remove(tmp)
			return nil
		}
		// 封装失败不致命：音频流本身就是合法 fMP4，退回直存
	}
	return os.Rename(tmp, dest)
}

// FileNameFor 稿件落盘文件名：发布日期_标题.m4a（TODO 4 用户口径，如
// 20261002_标题）；pubdate 非法时日期段为 unknown。同日同名极罕见——目标
// 文件已存在时由调用方写保护跳过。
func FileNameFor(pubdate int64, title string) string {
	date := "unknown"
	if pubdate > 0 {
		date = time.Unix(pubdate, 0).Format("20060102")
	}
	return date + "_" + SanitizeName(title) + ".m4a"
}

// SanitizeName 清洗为安全的文件/目录名：路径分隔与 Windows 非法字符换空格、
// 去控制字符、压空白、限 80 字符、去首尾点空（Windows 目录名不以点结尾）。
func SanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			continue
		case strings.ContainsRune(`\/:*?"<>|`, r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.TrimSpace(out)
	out = strings.Trim(out, ". ")
	runes := []rune(out)
	if len(runes) > 80 {
		out = string(runes[:80])
	}
	if out == "" {
		out = "unnamed"
	}
	return out
}

// SubDirName 追更子目录名：up 主名 + 合集名（server 算好存库，agent 直接拼）。
func SubDirName(upName, listTitle string) string {
	return SanitizeName(upName + " " + listTitle)
}

// sleepBetweenDL 逐条下载间隔（对 B 站接口的礼貌节流）。
var sleepBetweenDL = 1500 * time.Millisecond
