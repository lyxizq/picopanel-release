package tvfollow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

// TMDB API v3：搜索/详情两个端点，zh-CN 元数据，api_key 走 query（中转任务
// URL 加密落库，不落日志）。追更口径：
//   - 剧集 detail：status（Ended/Canceled=完结）+ last_episode_to_air（最新
//     已播 S/E，跨季取季号）+ next_episode_to_air（下次更新时间）；
//   - 电影 detail：status（Released=已上映）+ release_date。
const (
	tmdbBase   = "https://api.themoviedb.org/3"
	tmdbLang   = "zh-CN"
	searchPage = 1
)

// Candidate 搜索候选（添加弹层列表项）。
type Candidate struct {
	ID           int64   `json:"id"`
	Type         string  `json:"type"`
	Name         string  `json:"name"`
	OriginalName string  `json:"original_name"`
	Year         string  `json:"year"` // 首播/上映年份（前 4 位）
	PosterPath   string  `json:"poster_path"`
	Overview     string  `json:"overview"`
	Rating       float64 `json:"rating"`
	Cast         string  `json:"cast,omitempty"` // 主要演员（前 5 条候选拉 credits 补齐，550）
	// Emby 在库预检（551，server 直连；未配 Emby 或查询失败时 emby_checked=false）
	EmbyChecked  bool   `json:"emby_checked,omitempty"`
	InEmby       bool   `json:"in_emby,omitempty"`
	EmbyItemName string `json:"emby_item_name,omitempty"`
}

// PosterURL 海报完整地址（浏览器直连 image.tmdb.org，不经 server/agent）。
func PosterURL(path string) string {
	if path == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/w185" + path
}

// SearchURL 构造搜索请求 URL（type=tv|movie）。
func SearchURL(typ, query, apiKey string) string {
	v := url.Values{}
	v.Set("api_key", apiKey)
	v.Set("query", query)
	v.Set("language", tmdbLang)
	v.Set("page", strconv.Itoa(searchPage))
	v.Set("include_adult", "false")
	return tmdbBase + "/search/" + typ + "?" + v.Encode()
}

// DetailURL 构造详情请求 URL（append_to_response 顺带取主要演员 credits 与
// 播出平台 watch/providers——550/564：一次中转任务全拿，零额外任务）。
// append_to_response 手动拼 query：url.Values.Encode 会把 watch/providers 的
// 「/」编码成 %2F，TMDB 不识别该段就整段忽略（564 事故：主演有、平台恒空）。
func DetailURL(typ string, tmdbID int64, apiKey string) string {
	return fmt.Sprintf("%s/%s/%d?api_key=%s&language=%s&append_to_response=credits,watch/providers",
		tmdbBase, typ, tmdbID, url.QueryEscape(apiKey), tmdbLang)
}

// CreditsURL 单独取演职员表（搜索候选拉主演用：search 端点不支持 append）。
func CreditsURL(typ string, tmdbID int64, apiKey string) string {
	v := url.Values{}
	v.Set("api_key", apiKey)
	v.Set("language", tmdbLang)
	return fmt.Sprintf("%s/%s/%d/credits?%s", tmdbBase, typ, tmdbID, v.Encode())
}

// ParseCredits 解析演职员表响应，取前 4 位主演名（zh-CN 下 TMDB 返回已译名，
// 未译回落原名）。
func ParseCredits(body string) string {
	var raw struct {
		Cast []struct {
			Name string `json:"name"`
		} `json:"cast"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return ""
	}
	names := make([]string, 0, castLimit)
	for _, c := range raw.Cast {
		if c.Name == "" {
			continue
		}
		names = append(names, c.Name)
		if len(names) >= castLimit {
			break
		}
	}
	return strings.Join(names, "/")
}

// ParseSearch 解析搜索响应（tv 与 movie 的字段名不同：name/title、
// first_air_date/release_date）。
func ParseSearch(typ, body string) ([]Candidate, error) {
	var raw struct {
		Results []struct {
			ID            int64   `json:"id"`
			Name          string  `json:"name"`
			Title         string  `json:"title"`
			OriginalName  string  `json:"original_name"`
			OriginalTitle string  `json:"original_title"`
			FirstAirDate  string  `json:"first_air_date"`
			ReleaseDate   string  `json:"release_date"`
			PosterPath    string  `json:"poster_path"`
			Overview      string  `json:"overview"`
			VoteAverage   float64 `json:"vote_average"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	out := make([]Candidate, 0, len(raw.Results))
	for _, r := range raw.Results {
		name := r.Name
		orig := r.OriginalName
		date := r.FirstAirDate
		if typ == store.TVFollowMovie {
			name, orig, date = r.Title, r.OriginalTitle, r.ReleaseDate
		}
		if name == "" {
			name = orig
		}
		out = append(out, Candidate{
			ID: r.ID, Type: typ, Name: name, OriginalName: orig,
			Year:       yearOf(date),
			PosterPath: r.PosterPath, Overview: r.Overview, Rating: r.VoteAverage,
		})
	}
	return out, nil
}

func yearOf(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return date
}

// tmdbEpisode 一集（S/E + 播出日期）。
type tmdbEpisode struct {
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	AirDate       string `json:"air_date"`
}

type tmdbTVDetail struct {
	Status           string       `json:"status"`
	Name             string       `json:"name"`
	OriginalName     string       `json:"original_name"`
	Overview         string       `json:"overview"`
	NumberOfSeasons  int          `json:"number_of_seasons"`
	NumberOfEpisodes int          `json:"number_of_episodes"`
	LastEpisodeToAir *tmdbEpisode `json:"last_episode_to_air"`
	NextEpisodeToAir *tmdbEpisode `json:"next_episode_to_air"`
}

type tmdbMovieDetail struct {
	Status        string `json:"status"`
	Title         string `json:"title"`
	ReleaseDate   string `json:"release_date"`
	Runtime       int    `json:"runtime"`
	OriginalTitle string `json:"original_title"`
	Overview      string `json:"overview"`
}

// tvEnded TMDB 状态码里「不再更新」的两态（Canceled 也是终局，一并算完结）。
func tvEnded(status string) bool {
	return status == "Ended" || status == "Canceled"
}

// castLimit 主要演员位数（550）。
const castLimit = 4

// watchProviderGroup watch/providers 里一组平台：flatrate=订阅 / free=免费 /
// rent=租借 / buy=购买（564 只取订阅免费会漏租买平台，全收）。
type watchProviderGroup struct {
	Flatrate []struct {
		ProviderName string `json:"provider_name"`
	} `json:"flatrate"`
	Free []struct {
		ProviderName string `json:"provider_name"`
	} `json:"free"`
	Rent []struct {
		ProviderName string `json:"provider_name"`
	} `json:"rent"`
	Buy []struct {
		ProviderName string `json:"provider_name"`
	} `json:"buy"`
}

// extractWatchProviders 从 detail 响应抽出播出平台串（564）：CN 区优先、
// US 区兜底，flatrate+free 平台名去重 "/" 连接；都没有返回空串（国产剧在
// JustWatch 覆盖一般，空缺是常态，前端不显示该标签）。
// 区回退再遍历其余区（575：JustWatch 偶尔把国内平台挂在非 CN 区码下——
// 网页按访问者地区能看到、API 只取 CN/US 时 results 全空，醉梦即此例）。
func extractWatchProviders(body string) string {
	var raw struct {
		WatchProviders struct {
			Results map[string]watchProviderGroup `json:"results"`
		} `json:"watch/providers"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return ""
	}
	region, ok := raw.WatchProviders.Results["CN"]
	if !ok {
		region, ok = raw.WatchProviders.Results["US"]
	}
	if !ok {
		keys := make([]string, 0, len(raw.WatchProviders.Results))
		for k := range raw.WatchProviders.Results {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			g := raw.WatchProviders.Results[k]
			if len(g.Flatrate) > 0 || len(g.Free) > 0 || len(g.Rent) > 0 || len(g.Buy) > 0 {
				region, ok = g, true
				break
			}
		}
	}
	if !ok {
		return ""
	}
	names := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, p := range append(append(region.Flatrate, region.Free...), append(region.Rent, region.Buy...)...) {
		if p.ProviderName == "" || seen[p.ProviderName] {
			continue
		}
		seen[p.ProviderName] = true
		names = append(names, p.ProviderName)
	}
	return strings.Join(names, "/")
}

// extractCast 从 detail 响应（append_to_response=credits）抽出主演串。
func extractCast(body string) string {
	var raw struct {
		Credits struct {
			Cast []struct {
				Name string `json:"name"`
			} `json:"cast"`
		} `json:"credits"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return ""
	}
	names := make([]string, 0, castLimit)
	for _, c := range raw.Credits.Cast {
		if c.Name == "" {
			continue
		}
		names = append(names, c.Name)
		if len(names) >= castLimit {
			break
		}
	}
	return strings.Join(names, "/")
}

// episodeKey 进度去重键："S2E5"；无进度返回空串。
func episodeKey(season, episode int) string {
	if season <= 0 && episode <= 0 {
		return ""
	}
	return fmt.Sprintf("S%dE%d", season, episode)
}

// ParseTVDetail 解析剧集详情为回写载荷（不含 Emby 部分）。
func ParseTVDetail(body string) (*store.TVFollowDetailUpdate, error) {
	var raw tmdbTVDetail
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	d := &store.TVFollowDetailUpdate{
		Status:   raw.Status,
		Seasons:  raw.NumberOfSeasons,
		Episodes: raw.NumberOfEpisodes,
	}
	if e := raw.LastEpisodeToAir; e != nil {
		d.LastSeason, d.LastEpisode, d.LastAirDate = e.SeasonNumber, e.EpisodeNumber, e.AirDate
		d.EpisodeName = e.Name
	}
	if e := raw.NextEpisodeToAir; e != nil {
		d.NextSeason, d.NextEpisode, d.NextAirDate = e.SeasonNumber, e.EpisodeNumber, e.AirDate
	}
	d.NotifyEpisodeKey = episodeKey(d.LastSeason, d.LastEpisode)
	d.Cast = extractCast(body)
	d.Overview = raw.Overview
	d.Platforms = extractWatchProviders(body)
	return d, nil
}

// ParseMovieDetail 解析电影详情：状态 + 上映日期（借用 LastAirDate 字段承载，
// 前端按类型区分标签），不产出集数推送。
func ParseMovieDetail(body string) (*store.TVFollowDetailUpdate, error) {
	var raw tmdbMovieDetail
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	return &store.TVFollowDetailUpdate{
		Status:      raw.Status,
		LastAirDate: raw.ReleaseDate,
		Cast:        extractCast(body),
		Overview:    raw.Overview,
		Platforms:   extractWatchProviders(body),
	}, nil
}

// ErrNoConfig 追剧全局配置不全（key 或出口未配）。
var ErrNoConfig = errors.New("请先在系统设置配置 TMDB API Key 与追剧出口")

// Ready 配置是否齐备（key + 出口 agent）。
func Ready(st *store.Store) bool {
	return TmdbKey(st) != "" && EgressServerID(st) > 0
}

// Search 判搜索关键词合法性并返回去重键。
func SearchRefKey(typ, query string) string {
	return typ + ":" + strings.TrimSpace(query)
}

// waitPoll 轮询间隔与上限（同步等 agent 上报领取执行：默认 10s 一报 + 执行
// 秒级，22s 上限覆盖两次上报周期）。
const (
	waitStep = 300 * time.Millisecond
)
