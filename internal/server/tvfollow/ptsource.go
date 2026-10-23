// PT 站点进度综合（566）：追剧每轮检查时对 CHDBits / 天空(HDSky) / 憨憨 三个
// NexusPHP 站各入队一个按剧名的搜索中转任务（Cookie 复用 PT 站点表凭据，
// agent 只取最新 5 条标题原样回传），解析与综合全部在 server 端：
//   - 标题解析最新 S/E（S01E05 / 第05集 / EP05 等形态，多命中取最大）；
//   - 任一标题含「全集」→ 判该剧完结（推完结提醒 + done 停追，用户口径）；
//   - 进度综合 = max(三站解析, TMDB 现值)，跨源只进不退（TMDB 消费侧同样
//     做了水位保护），推进即走聚合提醒。
// agent 侧对该类任务（allow_any_host）跳过白名单并按同站点 3s 间隔限速。
package tvfollow

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"lz-monitor/internal/ptfetch"
	"lz-monitor/internal/server/store"
)

// PurposePT PT 站点搜索任务用途（net_tasks.purpose）。
const PurposePT = "tvfollow_pt"

// ptSearchSites 参与进度综合的 PT 站 site_key（与 ptfetch.Catalog 对应；
// 「天空」= HDSky）。站点未配置/未启用/无 Cookie 时自动跳过。
var ptSearchSites = []string{"chdbits", "hdsky", "hhanclub"}

// 标题集数模式：S01E05（含 E05E06 连集取最后一个）→ 中文「第 05 集/话」→
// EP05 / E05（前面模式未命中才试，防吃掉 S01E05 的尾段）。
var (
	reTitleSE    = regexp.MustCompile(`(?i)S(\d{1,2})\s*E(\d{1,3})`)
	reTitleEnum  = regexp.MustCompile(`(?i)E(\d{1,3})`)
	reTitleCN    = regexp.MustCompile(`第\s*(\d{1,4})\s*[集话話]`)
	reTitleEOnly = regexp.MustCompile(`(?i)(?:^|[^A-Z0-9])E(?:P)?(\d{1,3})(?:[^0-9]|$)`)
)

// parseTitleProgress 解析单个种子标题的进度：season/episode（0=标题未指明季，
// 消费端回落当前季）与是否含「全集」（566 用户口径：含全集=该剧更新完毕）。
func parseTitleProgress(title string) (season, episode int, complete bool) {
	if strings.Contains(title, "全集") {
		complete = true
	}
	if m := reTitleSE.FindAllStringSubmatch(title, -1); len(m) > 0 {
		last := m[len(m)-1]
		season, _ = strconv.Atoi(last[1])
		episode, _ = strconv.Atoi(last[2])
		// 连集 S02E15E16：标题里所有 Enn 取最大（E16 > E15）
		for _, em := range reTitleEnum.FindAllStringSubmatch(strings.ToUpper(title), -1) {
			if e, err := strconv.Atoi(em[1]); err == nil && e > episode {
				episode = e
			}
		}
		return season, episode, complete
	}
	if m := reTitleCN.FindAllStringSubmatch(title, -1); len(m) > 0 {
		episode, _ = strconv.Atoi(m[len(m)-1][1])
		return 0, episode, complete
	}
	if m := reTitleEOnly.FindAllStringSubmatch(title, -1); len(m) > 0 {
		episode, _ = strconv.Atoi(m[len(m)-1][1])
		return 0, episode, complete
	}
	return 0, 0, complete
}

// ptSitesFor 从 PT 站点表取参与综合的站点（已启用且有 Cookie；BaseURL 空 =
// 目录默认域名）。
func ptSitesFor(st *store.Store) []store.PtSite {
	sites, err := st.ListPtSites()
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, k := range ptSearchSites {
		want[k] = true
	}
	var out []store.PtSite
	for _, s := range sites {
		if want[s.SiteKey] && s.Enabled && s.HasCookie {
			out = append(out, s)
		}
	}
	return out
}

// ptSearchURL 站点搜索地址（571 用户给定实际形态；PT 站实为 torrents.php 非
// tor.php——旧地址是错误页面导致 566 的 PT 进度综合一直解析空，一并修复）：
//   - chdbits / hdsky：torrents.php?search={kw}&notnewword=1
//   - hhanclub：torrents.php?search={kw}&search_all=1
func ptSearchURL(siteKey, base, keyword string) string {
	base = strings.TrimSuffix(base, "/")
	kw := url.QueryEscape(keyword)
	switch siteKey {
	case "chdbits", "hdsky":
		return base + "/torrents.php?search=" + kw + "&notnewword=1"
	case "hhanclub":
		return base + "/torrents.php?search=" + kw + "&search_all=1"
	}
	return base + "/torrents.php?search=" + kw
}

// sweepPT 对单个待查剧按站入队搜索任务（detail 任务入队成功后调用，随主任务
// 的打标节奏走）。站点未配置/未启用/无 Cookie 时静默跳过；Cookie 复用 PT 站点
// 表凭据（该表 List 已解密），UA 站点可覆盖（Cloudflare 类站需要）。
func sweepPT(st *store.Store, follow *store.TVFollow, egress int64) {
	sites := ptSitesFor(st)
	if len(sites) == 0 {
		return
	}
	keyword := follow.Name
	if keyword == "" {
		keyword = follow.OriginalName
	}
	if keyword == "" {
		return
	}
	refID := strconv.FormatInt(follow.ID, 10)
	for _, site := range sites {
		base := site.BaseURL
		if base == "" {
			if def := ptfetch.SiteDefByKey(site.SiteKey); def != nil {
				base = def.URL
			}
		}
		if base == "" {
			continue
		}
		headers := map[string]string{"Cookie": site.Cookie}
		if site.UA != "" {
			headers["User-Agent"] = site.UA
		}
		refKey := site.SiteKey + ":" + refID
		taskURL := ptSearchURL(site.SiteKey, base, keyword)
		if _, err := st.CreateNetTask(PurposePT, refKey, egress, taskURL, "GET", headers, "", 20, true); err != nil && !errors.Is(err, store.ErrNetTaskActive) {
			log.Printf("追剧 PT 搜索任务入队失败 (follow=%d %s %s): %v", follow.ID, follow.Name, site.SiteKey, err)
		}
	}
}

// ConsumePTResult 消费 PT 搜索回传（幂等）：取最新 5 条标题解析进度，综合
// 判定（max 水位 / 「全集」完结）。查询失败静默——PT 是补充源，不污染
// last_error 也不触发重试（下个固定时刻自然再来）。
func ConsumePTResult(st *store.Store, task *store.NetTask) error {
	first, err := st.ConsumeNetTask(task.ID)
	if err != nil || !first {
		return err
	}
	parts := strings.SplitN(task.RefKey, ":", 2)
	if len(parts) != 2 {
		return nil
	}
	siteKey := parts[0]
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil
	}
	follow, err := st.GetTVFollow(id)
	if err != nil {
		return nil // 条目已删除
	}
	if follow.Done || task.Error != "" || task.StatusCode != 200 || task.Result == "" {
		return nil
	}
	titles := ptfetch.ParseNexusSearchTitles(task.Result, 5)
	if len(titles) == 0 {
		return nil // 该站暂无该剧资源，正常态
	}

	maxSeason, maxEpisode := follow.LastSeason, follow.LastEpisode
	complete := false
	for _, t := range titles {
		s, e, comp := parseTitleProgress(t)
		if comp {
			complete = true
		}
		if s == 0 {
			s = maxSeason // 标题未指明季：按当前季理解（0=尚未开播 → 视作第 1 季）
			if s == 0 {
				s = 1
			}
		}
		if s > maxSeason || (s == maxSeason && e > maxEpisode) {
			maxSeason, maxEpisode = s, e
		}
	}

	// 完结优先：任一标题含「全集」即标记完结（用户口径），不再看进度推进
	if complete {
		st.SetTVFollowDone(id, true)
		queueNotify(notifyLine{
			title: "追剧完结：" + follow.Name,
			body:  fmt.Sprintf("「%s」PT 站点（%s）已出现全集资源，判断该剧更新完毕。", follow.Name, siteName(siteKey)),
		})
		return nil
	}

	// 进度推进：max 水位严格大于现值才提醒（TMDB 消费侧同样只进不退，跨源不互踩）
	if (maxSeason > follow.LastSeason) || (maxSeason == follow.LastSeason && maxEpisode > follow.LastEpisode) {
		if err := st.MergePTProgress(id, maxSeason, maxEpisode); err != nil {
			return err
		}
		body := fmt.Sprintf("「%s」更新至 S%dE%d（PT · %s）", follow.Name, maxSeason, maxEpisode, siteName(siteKey))
		queueNotify(notifyLine{title: "追剧更新：" + follow.Name, body: body})
	}
	return nil
}

// siteName 站点 key → 展示名（目录外回落 key 本身）。
func siteName(siteKey string) string {
	if def := ptfetch.SiteDefByKey(siteKey); def != nil {
		return def.Name
	}
	return siteKey
}

// 资源站搜索地址（571 用户提供实际形态）：
//   - 观影：www.xn--wcv59z.com（「观影」punycode 域名），/search?q={kw}&type=&mode=1
//   - SeekDuck：seeduck.cc，路径式 /s/{kw}/
//   （癫影 web 搜索页路径无留档、占位地址点开不对，571 用户指定去掉不列）
const (
	watchSiteGying    = "https://www.xn--wcv59z.com"
	watchSiteSeekDuck = "https://seeduck.cc"
)

// ResourceLink 资源站搜索链接（详情弹层「资源搜索」区）。
type ResourceLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ResourceLinks 按关键词拼各资源站的搜索页地址（浏览器新窗打开，服务端不抓）。
// PT 三站仅列用户已配置的（有账号才有意义）；观影/SeekDuck 恒列。
func ResourceLinks(st *store.Store, keyword string) []ResourceLink {
	var out []ResourceLink
	sites, err := st.ListPtSites()
	if err == nil {
		want := map[string]bool{}
		for _, k := range ptSearchSites {
			want[k] = true
		}
		for _, s := range sites {
			if !want[s.SiteKey] {
				continue
			}
			base := s.BaseURL
			if base == "" {
				if def := ptfetch.SiteDefByKey(s.SiteKey); def != nil {
					base = def.URL
				}
			}
			if base == "" {
				continue
			}
			name := s.Name
			if name == "" {
				name = siteName(s.SiteKey)
			}
			out = append(out, ResourceLink{Name: name, URL: ptSearchURL(s.SiteKey, base, keyword)})
		}
	}
	out = append(out,
		ResourceLink{Name: "观影", URL: watchSiteGying + "/search?q=" + url.QueryEscape(keyword) + "&type=&mode=1"},
		ResourceLink{Name: "SeekDuck", URL: watchSiteSeekDuck + "/s/" + url.PathEscape(keyword) + "/"},
	)
	return out
}
