// Emby 客户端（Homepage widget 同口径）：X-Emby-Token 鉴权 →
// /emby/Items/Counts 媒体库计数 + /emby/Sessions 正在播放会话。
// Jellyfin 同一套 API（/emby 路径兼容），天然可用。
package integrations

import (
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

// embyUnescape 部分客户端上报的 Client/DeviceName 带 URL 百分号编码
// （如 Plezy%20Android%20TV，537），展示前解码；坏序列保持原样。
func embyUnescape(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	if d, err := url.QueryUnescape(s); err == nil {
		return d
	}
	return s
}

// maxStreams 正在播放列表封顶条数。
const maxStreams = 5

// EmbyPlay 一个正在播放的会话（528）：manager 差分观测记播放起止用，
// 同时是前端「正在播放」流水的数据源。
type EmbyPlay struct {
	Key     string `json:"key"` // 差分键 DeviceId|ItemId（DeviceId 缺失回落 Client+DeviceName）
	User    string `json:"user"`
	Device  string `json:"device"` // 播放设备：Client · DeviceName
	ItemID  string `json:"item_id"`
	Title   string `json:"title"`
	Sub     string `json:"sub"` // 剧名（电影为空）
	Type    string `json:"type"`
	Season  int64  `json:"season"`  // 季号（ParentIndexNumber，仅剧集；70 清单显示 SxxExx）
	Episode int64  `json:"episode"` // 集号（IndexNumber）
	Pos     int64  `json:"pos"`     // PositionTicks
	Runtime int64  `json:"runtime"` // RunTimeTicks
}

// fetchEmby 抓取一帧 Emby 快照：媒体库四格计数 + 会话（正在播放）。
// Sessions 恒拉——528 播放记录差分观测依赖它，config now_playing 只控制
// 前端「正在播放」行是否随数据下发。
func fetchEmby(in store.Integration) (map[string]any, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Token == "" {
		return nil, fmt.Errorf("未配置 API Key（Emby 后台「高级 → API 密钥」生成）")
	}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	base := strings.TrimSuffix(in.URL, "/") + "/emby"
	headers := map[string]string{"X-Emby-Token": cred.Token}

	var counts struct {
		MovieCount   int64 `json:"MovieCount"`
		SeriesCount  int64 `json:"SeriesCount"`
		EpisodeCount int64 `json:"EpisodeCount"`
		SongCount    int64 `json:"SongCount"`
	}
	if _, err := getJSON(cl, base+"/Items/Counts", headers, &counts); err != nil {
		return nil, fmt.Errorf("获取媒体库计数失败: %w", err)
	}
	data := map[string]any{
		"movies":   counts.MovieCount,
		"series":   counts.SeriesCount,
		"episodes": counts.EpisodeCount,
		"songs":    counts.SongCount,
	}

	// 会话：取含 NowPlayingItem 的（播放记录观测 528 + 正在播放展示）。
	// PlayState（播放位置）是会话级字段、与 NowPlayingItem 平级（537：原解析
	// 嵌在 NowPlayingItem 内恒取不到，进度恒 0%；兼容个别版本嵌套返回，两处取非空）
	var sessions []struct {
		UserName   string `json:"UserName"`
		Client     string `json:"Client"`
		DeviceName string `json:"DeviceName"`
		DeviceId   string `json:"DeviceId"`
		PlayState  *struct {
			PositionTicks int64 `json:"PositionTicks"`
		} `json:"PlayState"`
		NowPlayingItem *struct {
			ID                string `json:"Id"`
			Name              string `json:"Name"`
			SeriesName        string `json:"SeriesName"`
			Type              string `json:"Type"`
			ParentIndexNumber int64  `json:"ParentIndexNumber"`
			IndexNumber       int64  `json:"IndexNumber"`
			RunTimeTicks      int64  `json:"RunTimeTicks"`
			PlayState         *struct {
				PositionTicks int64 `json:"PositionTicks"`
			} `json:"PlayState"`
		} `json:"NowPlayingItem"`
	}
	if _, err := getJSON(cl, base+"/Sessions", headers, &sessions); err != nil {
		// 计数已到手：会话失败不整体判死（部分老版本权限差异）——plays 不下发
		// 且标错，观测侧据此不动基线（拉取失败 ≠ 播放结束，防误记）
		data["streams"] = []any{}
		data["streams_error"] = true
		return data, nil
	}
	plays := make([]EmbyPlay, 0, len(sessions))
	for _, s := range sessions {
		np := s.NowPlayingItem
		if np == nil {
			continue
		}
		user := s.UserName
		if user == "" {
			user = embyUnescape(s.Client)
		}
		key := s.DeviceId + "|" + np.ID
		if s.DeviceId == "" { // 老客户端无 DeviceId：客户端+设备名兜底
			key = s.Client + "/" + s.DeviceName + "|" + np.ID
		}
		pos := int64(0)
		if s.PlayState != nil { // 会话级为准（标准形态）
			pos = s.PlayState.PositionTicks
		} else if np.PlayState != nil { // 个别版本嵌在条目内（兼容）
			pos = np.PlayState.PositionTicks
		}
		plays = append(plays, EmbyPlay{
			Key: key, User: user,
			Device: strings.TrimSpace(embyUnescape(s.Client) + " · " + embyUnescape(s.DeviceName)),
			ItemID: np.ID, Title: np.Name, Sub: np.SeriesName, Type: np.Type,
			Season: np.ParentIndexNumber, Episode: np.IndexNumber,
			Pos: pos, Runtime: np.RunTimeTicks,
		})
	}
	data["plays"] = plays

	// 正在播放（config {now_playing:true} 默认开）：只控展示，不再影响会话拉取
	if !configBoolDefault(in.Config, "now_playing", true) {
		return data, nil
	}
	streams := make([]any, 0, maxStreams)
	for _, p := range plays {
		pct := 0.0
		if p.Runtime > 0 {
			pct = float64(p.Pos) / float64(p.Runtime) * 100
		}
		streams = append(streams, map[string]any{
			"user":  p.User,
			"title": p.Title,
			"sub":   p.Sub,
			"pct":   pct,
		})
		if len(streams) >= maxStreams {
			break
		}
	}
	data["streams"] = streams
	return data, nil
}

// observeEmbyFrame Emby 抓帧后的增量观察（528）：与上一帧基线差分记播放起止
// （emby_events）。会话新出现=播放开始立即落行（ended_at=0 进行中），消失=回填
// 结束时间与最后观测位置；差分键 DeviceId|ItemId。会话拉取失败（plays 未下发）
// 或整体抓取失败不动基线——拉取失败 ≠ 播放结束，防误记。观测全程持锁（口径同
// observeHAFrame：Kick 协程/轮询循环/页面补拉并发进入时基线读写与落库必须同
// 临界区，否则并发写 map 是进程级 fatal、起止可能与插入顺序颠倒）。
func (m *Manager) observeEmbyFrame(in store.Integration, p Payload) {
	if !p.OK || in.Type != store.IntegrationEmby {
		return
	}
	plays, ok := p.Data["plays"].([]EmbyPlay)
	if !ok { // 会话拉取失败：plays 未下发，不动基线
		return
	}
	now := time.Now().Unix()
	m.embyMu.Lock()
	defer m.embyMu.Unlock()
	prev := m.embyPrev[in.ID]
	if prev == nil {
		prev = map[string]embyActive{}
		m.embyPrev[in.ID] = prev
	}
	seen := make(map[string]bool, len(plays))
	for _, pl := range plays {
		if pl.Key == "" {
			continue
		}
		seen[pl.Key] = true
		act, exists := prev[pl.Key]
		if !exists {
			rowID, err := m.st.InsertEmbyStart(store.EmbyEvent{
				IntegrationID: in.ID, User: pl.User, Device: pl.Device, ItemID: pl.ItemID,
				Title: pl.Title, Sub: pl.Sub, Type: pl.Type,
				Season: pl.Season, Episode: pl.Episode,
				StartedAt: now, PosTicks: pl.Pos, RuntimeTicks: pl.Runtime,
			})
			if err != nil {
				log.Printf("记录 Emby 播放开始失败: %v", err)
				continue
			}
			prev[pl.Key] = embyActive{rowID: rowID, last: pl.Pos}
			continue
		}
		// 位置变化 >1s（1e7 ticks）即回写 DB（534）：进行中的行进度实时准，
		// 不再等结束时一次回填；失败只记日志下帧再写
		if d := pl.Pos - act.last; d > 1e7 || d < -1e7 {
			if err := m.st.UpdateEmbyPos(act.rowID, pl.Pos, pl.Runtime); err != nil {
				log.Printf("回写 Emby 播放位置失败: %v", err)
			}
		}
		act.last = pl.Pos
		prev[pl.Key] = act
	}
	for key, act := range prev {
		if seen[key] {
			continue
		}
		// 会话消失=播放结束：回填结束时间与最后观测位置（30s 轮询粒度，误差同档）
		if err := m.st.FinishEmbyPlay(act.rowID, now, act.last); err != nil {
			log.Printf("回填 Emby 播放结束失败: %v", err)
			continue // 落库失败保留基线，下帧重试回填（幂等 UPDATE）
		}
		delete(prev, key)
	}
}

// EmbyMatch 在库检查结果（549）：命中条目名供展示。
type EmbyMatch struct {
	Found    bool
	ItemName string
}

// FindEmbyByTMDB 按 TMDB ID 查条目是否在库（549 追剧联动）：/Items 按名称
// 搜索 + ProviderIds 精确比对——AnyProviderIdEquals 参数在 Jellyfin 旧版
// 支持不齐，名称搜索 Emby/Jellyfin 通用，ID 比对消除同名误报。多个候选名
// （中文名/原名）依次试，命中即返回。
func FindEmbyByTMDB(in store.Integration, itemType string, tmdbID int64, names []string) (EmbyMatch, error) {
	cred := ParseCredentials(in.Auth)
	if cred.Token == "" {
		return EmbyMatch{}, fmt.Errorf("未配置 API Key（Emby 后台「高级 → API 密钥」生成）")
	}
	wantType := "Series"
	if itemType == store.TVFollowMovie {
		wantType = "Movie"
	}
	base := strings.TrimSuffix(in.URL, "/") + "/emby"
	headers := map[string]string{"X-Emby-Token": cred.Token}
	cl := httpClientFor(configBool(in.Config, "insecure"))
	for _, name := range names {
		if name == "" {
			continue
		}
		q := url.Values{}
		q.Set("SearchTerm", name)
		q.Set("IncludeItemTypes", wantType)
		q.Set("Recursive", "true")
		q.Set("Fields", "ProviderIds")
		q.Set("Limit", "20")
		var res struct {
			Items []struct {
				Name        string            `json:"Name"`
				Type        string            `json:"Type"`
				ProviderIds map[string]string `json:"ProviderIds"`
			} `json:"Items"`
		}
		if _, err := getJSON(cl, base+"/Items?"+q.Encode(), headers, &res); err != nil {
			return EmbyMatch{}, err
		}
		for _, it := range res.Items {
			if it.Type != wantType && it.Type != "" {
				continue
			}
			if it.ProviderIds["Tmdb"] == strconv.FormatInt(tmdbID, 10) {
				return EmbyMatch{Found: true, ItemName: it.Name}, nil
			}
		}
	}
	return EmbyMatch{}, nil
}
