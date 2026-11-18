// 内容库集成（438）：Nextcloud（OCS serverinfo 用量统计）与 Navidrome
// （Subsonic API 媒体库规模）。两者都是「账号密码型」——Nextcloud 用应用密码
// 走 Basic 认证，Navidrome 走 Subsonic 明文参数认证。
package integrations

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"lz-monitor/internal/server/store"
)

// fetchNextcloud 抓取 Nextcloud 用量：GET /status.php（免认证，取版本）+
// GET /ocs/v2.php/apps/serverinfo/api/v1/serverinfo（Basic 认证 + OCS-APIRequest
// 头，JSON 应答）取 用户/文件/分享 计数。Serverinfo 应用未启用时 404，报错提示。
func fetchNextcloud(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Username == "" || cred.Password == "" {
		return nil, fmt.Errorf("请填写账号与应用密码（设置→安全→设备与 会话 创建）")
	}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")
	auth := map[string]string{"Authorization": basicAuth(cred.Username, cred.Password)}

	var status struct {
		Version string `json:"version"`
	}
	if _, err := getJSON(cl, base+"/status.php", auth, &status); err != nil {
		return nil, fmt.Errorf("连接 Nextcloud 失败: %w", err)
	}
	version := shortVersion(status.Version)

	headers := map[string]string{
		"Authorization":  auth["Authorization"],
		"OCS-APIRequest": "true",
		"Accept":         "application/json",
	}
	var ocs struct {
		Ocs struct {
			Meta struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"meta"`
			Data struct {
				Nextcloud struct {
					Storage struct {
						NumUsers int64 `json:"num_users"`
						NumFiles int64 `json:"num_files"`
					} `json:"storage"`
					Shares struct {
						NumShares int64 `json:"num_shares"`
					} `json:"shares"`
				} `json:"nextcloud"`
			} `json:"data"`
		} `json:"ocs"`
	}
	if _, err := getJSON(cl, base+"/ocs/v2.php/apps/serverinfo/api/v1/serverinfo", headers, &ocs); err != nil {
		if httpStatus(err) == http.StatusNotFound {
			return nil, fmt.Errorf("未找到 serverinfo 接口（需在 Nextcloud 应用管理中启用「Serverinfo」）")
		}
		return nil, fmt.Errorf("获取 Nextcloud 用量失败: %w（账号建议用「应用密码」）", err)
	}
	return map[string]any{
		"users":   ocs.Ocs.Data.Nextcloud.Storage.NumUsers,
		"files":   ocs.Ocs.Data.Nextcloud.Storage.NumFiles,
		"shares":  ocs.Ocs.Data.Nextcloud.Shares.NumShares,
		"version": version,
	}, nil
}

// fetchNavidrome 抓取 Navidrome 媒体库规模（Subsonic API，f=json）：
// ping 鉴权 → getArtists 汇总 艺术家/专辑/歌曲（songCount 为 OpenSubsonic
// 扩展，老版本缺失时回落 getScanStatus.count）→ serverVersion 出版本格。
func fetchNavidrome(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Username == "" || cred.Password == "" {
		return nil, fmt.Errorf("请填写 Navidrome 账号密码")
	}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/")
	sub := func(path string, out any) error {
		q := url.Values{
			"u": {cred.Username}, "p": {cred.Password},
			"v": {"1.16.1"}, "c": {"PicoPanel"}, "f": {"json"},
		}
		_, err := getJSON(cl, base+path+"?"+q.Encode(), nil, out)
		return err
	}

	var ping struct {
		Subsonic struct {
			Status        string `json:"status"`
			ServerVersion string `json:"serverVersion"`
			Error         *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"subsonic-response"`
	}
	if err := sub("/rest/ping", &ping); err != nil {
		return nil, fmt.Errorf("连接 Navidrome 失败: %w", err)
	}
	if ping.Subsonic.Status != "ok" {
		msg := "账号或密码错误"
		if ping.Subsonic.Error != nil {
			msg = ping.Subsonic.Error.Message
		}
		return nil, fmt.Errorf("Navidrome 认证失败: %s", msg)
	}

	var artists struct {
		Subsonic struct {
			Artists struct {
				Index []struct {
					Artist []struct {
						AlbumCount int64 `json:"albumCount"`
						SongCount  int64 `json:"songCount"`
					} `json:"artist"`
				} `json:"index"`
			} `json:"artists"`
		} `json:"subsonic-response"`
	}
	var nArtist, nAlbum, nSong int64
	songsKnown := true
	if err := sub("/rest/getArtists", &artists); err != nil {
		return nil, fmt.Errorf("获取 Navidrome 媒体库失败: %w", err)
	}
	for _, idx := range artists.Subsonic.Artists.Index {
		for _, a := range idx.Artist {
			nArtist++
			nAlbum += a.AlbumCount
			if a.SongCount > 0 {
				nSong += a.SongCount
			} else {
				songsKnown = false
			}
		}
	}
	if !songsKnown {
		// 老版本 artist 无 songCount：getScanStatus.count = 全库曲目数
		var scan struct {
			Subsonic struct {
				ScanStatus struct {
					Count int64 `json:"count"`
				} `json:"scanStatus"`
			} `json:"subsonic-response"`
		}
		if err := sub("/rest/getScanStatus", &scan); err == nil && scan.Subsonic.ScanStatus.Count > 0 {
			nSong = scan.Subsonic.ScanStatus.Count
		}
	}
	return map[string]any{
		"artists": nArtist,
		"albums":  nAlbum,
		"songs":   nSong,
		"version": shortVersion(ping.Subsonic.ServerVersion),
	}, nil
}

// shortVersion 取版本串首段并剥掉结尾多余位：'30.0.2.1'→'30.0.2'、
// '0.55.0 (8b30f96f)'→'0.55.0'、'TrueNAS-SCALE-24.04.2' 前缀由调用方先剥。
func shortVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if i := strings.IndexAny(v, " +-"); i > 0 && strings.Contains(v[:i], ".") {
		v = v[:i]
	}
	// 仅剩 build 位（四段以上）剥到最后三段
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		v = strings.Join(parts[:3], ".")
	}
	return v
}
