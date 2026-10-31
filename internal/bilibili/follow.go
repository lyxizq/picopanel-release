package bilibili

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Target 解析后的追更目标：mid + 合集（season）或视频列表（series）或全部
// 投稿（uploads，TODO 8 用户口径：/upload/video 投稿页链接）。
type Target struct {
	Mid    int64  `json:"mid"`
	Kind   string `json:"kind"` // season | series | uploads
	ListID int64  `json:"list_id"`
	Title  string `json:"title"`
	Total  int    `json:"total"`
	UpName string `json:"up_name"`
}

// Item 合集/视频列表内的一条稿件。
type Item struct {
	BVID   string `json:"bvid"`
	Title  string `json:"title"`
	PubDate int64 `json:"pubdate"`
	CID    int64  `json:"cid,omitempty"` // series 接口自带；season 走 view 现取
}

var (
	midRe   = regexp.MustCompile(`space\.bilibili\.com/(\d+)`)
	sidRe   = regexp.MustCompile(`[?&]sid=(\d+)`)
	listsRe = regexp.MustCompile(`/lists/(\d+)`)
)

// ErrNoList 地址里没有合集/视频列表信息的提示错误。
var ErrNoList = errors.New("地址未包含合集或视频列表：支持合集页/视频列表页/up 主投稿页链接，或直接填合集名字")

// ResolveTarget 把用户输入解析为追更目标：
//   - 地址是链接：取 mid + 列表 id（/lists/<id>、collectiondetail?sid=、
//     seriesdetail?sid=），/lists/ 的新统一 id 先按合集试、再按视频列表试；
//     /upload/video（全部投稿）走动态流列表；up 主名字仅在需要搜索时使用
//     （链接自带 mid 就不搜）；
//   - 地址是纯文本：视为合集名字，按 up 主名字搜人 → 拉其合集/视频列表清单
//     按名字匹配（精确优先，其次包含），未命中报错并附候选名。
func (c *Client) ResolveTarget(upName, address string) (*Target, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, errors.New("追更地址为空")
	}
	if isURL(address) {
		return c.resolveURL(address)
	}
	if strings.TrimSpace(upName) == "" {
		return nil, errors.New("填合集名字时必须同时填 up 主名字")
	}
	mid, uname, err := c.SearchUser(strings.TrimSpace(upName))
	if err != nil {
		return nil, err
	}
	lists, err := c.UserLists(mid)
	if err != nil {
		return nil, err
	}
	want := strings.TrimSpace(address)
	var hit *listMeta
	for i := range lists {
		if lists[i].Name == want {
			hit = &lists[i]
			break
		}
	}
	if hit == nil {
		lower := strings.ToLower(want)
		for i := range lists {
			if strings.Contains(strings.ToLower(lists[i].Name), lower) {
				hit = &lists[i]
				break
			}
		}
	}
	if hit == nil {
		names := make([]string, 0, len(lists))
		for _, l := range lists {
			names = append(names, l.Name)
		}
		sort.Strings(names)
		if len(names) > 8 {
			names = names[:8]
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("up 主「%s」名下没有合集或视频列表", uname)
		}
		return nil, fmt.Errorf("没找到名为「%s」的合集，该 up 主的合集有：%s", want, strings.Join(names, "、"))
	}
	t := &Target{Mid: mid, Kind: hit.Kind, ListID: hit.ID, Title: hit.Name, Total: hit.Total, UpName: uname}
	if err := c.verifyTarget(t); err != nil {
		return nil, err
	}
	return t, nil
}

// resolveURL 从链接解析。确认列表存在并取名称/总数（顺带校验权限）。
func (c *Client) resolveURL(address string) (*Target, error) {
	m := midRe.FindStringSubmatch(address)
	if m == nil {
		return nil, errors.New("链接里没有 space.bilibili.com/<mid>，无法确定 up 主")
	}
	var mid int64
	fmt.Sscanf(m[1], "%d", &mid)
	var sid int64
	if mm := listsRe.FindStringSubmatch(address); mm != nil {
		fmt.Sscanf(mm[1], "%d", &sid)
	} else if mm := sidRe.FindStringSubmatch(address); mm != nil {
		fmt.Sscanf(mm[1], "%d", &sid)
	}
	if sid == 0 {
		// /upload/video（全部投稿）：动态流作列表（投稿必发动态，B 站投稿页
		// 同源数据；投稿列表接口 recArchivesByKeywords 已被 w_webid 风控锁死）
		if strings.Contains(address, "/upload/") {
			return c.targetUploads(mid)
		}
		return nil, ErrNoList
	}
	if strings.Contains(address, "seriesdetail") {
		return c.targetByKind(mid, "series", sid)
	}
	if strings.Contains(address, "collectiondetail") {
		return c.targetByKind(mid, "season", sid)
	}
	// /lists/<id> 新统一页：类型未知，先合集后视频列表
	if t, err := c.targetByKind(mid, "season", sid); err == nil {
		return t, nil
	}
	return c.targetByKind(mid, "series", sid)
}

func (c *Client) targetByKind(mid int64, kind string, listID int64) (*Target, error) {
	items, meta, err := c.listPage(mid, kind, listID, 1, 1)
	if err != nil {
		return nil, err
	}
	if meta.Name == "" {
		return nil, fmt.Errorf("列表 %d 不存在或为空", listID)
	}
	return &Target{Mid: mid, Kind: kind, ListID: listID, Title: meta.Name, Total: max(meta.Total, len(items)), UpName: meta.OwnerName}, nil
}

// verifyTarget 拉第一页校验目标可访问（纯名字匹配路径用；链接路径已验）。
func (c *Client) verifyTarget(t *Target) error {
	_, meta, err := c.listPage(t.Mid, t.Kind, t.ListID, 1, 1)
	if err != nil {
		return err
	}
	if meta.Total > t.Total {
		t.Total = meta.Total
	}
	if meta.OwnerName != "" && (t.UpName == "" || t.UpName == t.Title) {
		t.UpName = meta.OwnerName
	}
	return nil
}

// listMeta 列表页元信息。
type listMeta struct {
	Kind      string
	ID        int64
	Name      string
	Total     int
	OwnerName string
}

// listPage 拉一页稿件列表。season 走 seasons_archives_list（页号 page_num/页
// 大小 page_size），series 走 series/archives（pn/ps）；顺带回 meta（名称/总数）。
func (c *Client) listPage(mid int64, kind string, listID int64, page, size int) ([]Item, listMeta, error) {
	var raw json.RawMessage
	var err error
	meta := listMeta{Kind: kind, ID: listID}
	if kind == "series" {
		params := url.Values{}
		params.Set("mid", fmt.Sprint(mid))
		params.Set("series_id", fmt.Sprint(listID))
		params.Set("only_normal", "true")
		params.Set("sort", "desc")
		params.Set("pn", fmt.Sprint(page))
		params.Set("ps", fmt.Sprint(size))
		if raw, err = c.getJSON("/x/series/archives", params); err != nil {
			return nil, meta, err
		}
		var data struct {
			Archives []struct {
				BVID    string `json:"bvid"`
				Title   string `json:"title"`
				PubDate int64  `json:"pubdate"`
				CID     int64  `json:"cid"`
			} `json:"archives"`
			Meta struct {
				Name  string `json:"name"`
				Total int    `json:"total"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, meta, fmt.Errorf("视频列表解析失败: %w", err)
		}
		meta.Name, meta.Total = data.Meta.Name, data.Meta.Total
		items := make([]Item, 0, len(data.Archives))
		for _, a := range data.Archives {
			items = append(items, Item{BVID: a.BVID, Title: a.Title, PubDate: a.PubDate, CID: a.CID})
		}
		return items, meta, nil
	}
	params := url.Values{}
	params.Set("mid", fmt.Sprint(mid))
	params.Set("season_id", fmt.Sprint(listID))
	params.Set("page_num", fmt.Sprint(page))
	params.Set("page_size", fmt.Sprint(size))
	if raw, err = c.getJSON("/x/polymer/web-space/seasons_archives_list", params); err != nil {
		return nil, meta, err
	}
	var data struct {
		Archives []struct {
			BVID    string `json:"bvid"`
			Title   string `json:"title"`
			PubDate int64  `json:"pubdate"`
		} `json:"archives"`
		Meta struct {
			Name  string `json:"name"`
			Total int    `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, meta, fmt.Errorf("合集解析失败: %w", err)
	}
	meta.Name, meta.Total = data.Meta.Name, data.Meta.Total
	items := make([]Item, 0, len(data.Archives))
	for _, a := range data.Archives {
		items = append(items, Item{BVID: a.BVID, Title: a.Title, PubDate: a.PubDate})
	}
	return items, meta, nil
}

// targetUploads 全部投稿形态：拉首页验证可达（动态流偶尔抖动返回空，重试后
// 仍空报错）。无合集名概念，Title 固定「投稿」（子目录=up 主名 投稿）。
func (c *Client) targetUploads(mid int64) (*Target, error) {
	t := &Target{Mid: mid, Kind: "uploads", Title: "投稿"}
	page, err := c.feedSpacePage(mid, "")
	if err != nil {
		return nil, err
	}
	if len(page.Items) == 0 && !page.HasMore {
		// 重试后仍全空：真没投稿的风控空页难分辨，按风控口径提示（用户重试
		// 或配置 SESSDATA 即可恢复，误伤真 0 投稿的提示成本可接受）
		return nil, errors.New("B 站风控返回了空列表（未登录请求偶发），请稍后重试，或在设置里配置 SESSDATA 后再试")
	}
	return t, nil
}

// feedPage 动态流一页的解析结果。
type feedPage struct {
	Items   []Item
	HasMore bool
	Offset  string
}

// feedSpacePage 拉一页 up 主投稿动态（未登录偶发空页抖动：首页空且无下一页
// 视为抖动而非真没投稿，重试至多 3 次）。
func (c *Client) feedSpacePage(mid int64, offset string) (*feedPage, error) {
	const maxTries = 3
	for try := 0; try < maxTries; try++ {
		params := url.Values{}
		params.Set("host_mid", fmt.Sprint(mid))
		params.Set("offset", offset)
		raw, err := c.getWBIJSON("/x/polymer/web-dynamic/v1/feed/space", params)
		if err != nil {
			return nil, err
		}
		var data struct {
			Items []struct {
				Type string `json:"type"`
				Modules struct {
					ModuleAuthor struct {
						PubTS json.Number `json:"pub_ts"`
					} `json:"module_author"`
					ModuleDynamic struct {
						Major struct {
							Archive *struct {
								BVID  string `json:"bvid"`
								Title string `json:"title"`
							} `json:"archive"`
						} `json:"major"`
					} `json:"module_dynamic"`
				} `json:"modules"`
			} `json:"items"`
			HasMore bool   `json:"has_more"`
			Offset  string `json:"offset"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("投稿动态解析失败: %w", err)
		}
		page := &feedPage{HasMore: data.HasMore, Offset: data.Offset}
		for _, it := range data.Items {
			if it.Type != "DYNAMIC_TYPE_AV" || it.Modules.ModuleDynamic.Major.Archive == nil {
				continue // 置顶/转发/图文等非视频动态不进列表
			}
			a := it.Modules.ModuleDynamic.Major.Archive
			pub, _ := it.Modules.ModuleAuthor.PubTS.Int64()
			page.Items = append(page.Items, Item{BVID: a.BVID, Title: a.Title, PubDate: pub})
		}
		// 抖动判定：未登录偶发空页（无条目、无下一页）——重试而不是当作投稿为空。
		// 重试前强制换新 buvid 指纹（实测同一指纹连试大概率连败，换指纹明显
		// 提高通过率）；仍全空则按投稿为空返回
		if len(page.Items) == 0 && !page.HasMore && offset == "" && try < maxTries-1 {
			c.rotateBuvid()
			time.Sleep(time.Second)
			continue
		}
		return page, nil
	}
	return &feedPage{}, nil
}

// listUploads 按动态流翻全量投稿（offset 语义）。maxItems 截断在翻完后做——
// 分页不保证时间序，提前停页会漏掉后面页里更新的条目（单测实测）。页间隔
// 300ms、页数上限 300（约 3600 条兜底）。
func (c *Client) listUploads(t *Target, maxItems int, watermark int64) ([]Item, error) {
	var out []Item
	offset := ""
	for page := 0; page < 300; page++ {
		p, err := c.feedSpacePage(t.Mid, offset)
		if err != nil {
			if len(out) == 0 {
				return nil, err
			}
			break // 中途页失败：已拿到的够判增量
		}
		stale := watermark > 0
		for _, it := range p.Items {
			if it.PubDate > watermark {
				stale = false // 页内有比水位新的：不能停
			}
		}
		out = append(out, p.Items...)
		if stale {
			break // 整页都比水位旧（动态流新→旧单调）：之后全是旧视频（7 不回填）
		}
		if !p.HasMore || p.Offset == "" || p.Offset == offset {
			break
		}
		offset = p.Offset
	}
	sortByPubDate(out)
	if maxItems > 0 && len(out) > maxItems {
		out = out[:maxItems]
	}
	return out, nil
}

// userLists up 主名下的合集与视频列表清单（seasons_series_list 一次拿全；
// 真实结构 data.items_lists.{seasons_list,series_list}，meta 里各带 id/名称/总数）。
func (c *Client) UserLists(mid int64) ([]listMeta, error) {
	var out []listMeta
	for page := 1; page <= 5; page++ { // 每页 20，最多 5 页（100 个清单兜底）
		params := url.Values{}
		params.Set("mid", fmt.Sprint(mid))
		params.Set("page_num", fmt.Sprint(page))
		params.Set("page_size", "20")
		raw, err := c.getJSON("/x/polymer/web-space/seasons_series_list", params)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		var data struct {
			ItemsLists struct {
				Page struct {
					Total int `json:"total"`
				} `json:"page"`
				SeasonsList []struct {
					Meta struct {
						SeasonID int64  `json:"season_id"`
						Name     string `json:"name"`
						Total    int    `json:"total"`
					} `json:"meta"`
				} `json:"seasons_list"`
				SeriesList []struct {
					Meta struct {
						SeriesID int64  `json:"series_id"`
						Name     string `json:"name"`
						Total    int    `json:"total"`
					} `json:"meta"`
				} `json:"series_list"`
			} `json:"items_lists"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("合集清单解析失败: %w", err)
		}
		for _, it := range data.ItemsLists.SeasonsList {
			if it.Meta.SeasonID != 0 && it.Meta.Name != "" {
				out = append(out, listMeta{Kind: "season", ID: it.Meta.SeasonID, Name: it.Meta.Name, Total: it.Meta.Total})
			}
		}
		for _, it := range data.ItemsLists.SeriesList {
			if it.Meta.SeriesID != 0 && it.Meta.Name != "" {
				out = append(out, listMeta{Kind: "series", ID: it.Meta.SeriesID, Name: it.Meta.Name, Total: it.Meta.Total})
			}
		}
		got := len(data.ItemsLists.SeasonsList) + len(data.ItemsLists.SeriesList)
		if got < 20 || len(out) >= data.ItemsLists.Page.Total {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	return out, nil
}

// SearchUser 按 up 主名字搜人：精确同名优先，否则取第一个结果。
func (c *Client) SearchUser(name string) (int64, string, error) {
	params := url.Values{}
	params.Set("search_type", "bili_user")
	params.Set("keyword", name)
	raw, err := c.getWBIJSON("/x/web-interface/search/type", params)
	if err != nil {
		return 0, "", err
	}
	var data struct {
		Result []struct {
			Mid   int64  `json:"mid"`
			Uname string `json:"uname"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return 0, "", fmt.Errorf("搜索结果解析失败: %w", err)
	}
	if len(data.Result) == 0 {
		return 0, "", fmt.Errorf("没搜到 up 主「%s」", name)
	}
	for _, r := range data.Result {
		if r.Uname == name {
			return r.Mid, r.Uname, nil
		}
	}
	return data.Result[0].Mid, data.Result[0].Uname, nil
}

// ListItems 拉全列表稿件（按发布时间新→旧排序）。maxItems>0 时截断——因为
// 分页不保证按时间序，必须全量翻完排序后才能可靠取「最新 N 条」（页大小 30、
// 页间隔 300ms，页数上限 50 = 1500 条兜底）。uploads 形态走动态流专用分页。
// watermark>0 时 uploads 可在「整页都比水位旧」处提前停页（动态流新→旧单调；
// season/series 分页序不保证，仍全量翻、水位过滤在 agent 侧逐条做）。
func (c *Client) ListItems(t *Target, maxItems int, watermark int64) ([]Item, error) {
	if t.Kind == "uploads" {
		return c.listUploads(t, maxItems, watermark)
	}
	const pageSize = 30
	var out []Item
	total := t.Total
	for page := 1; page <= 50; page++ {
		items, meta, err := c.listPage(t.Mid, t.Kind, t.ListID, page, pageSize)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break // 中途页失败：已拿到的够判增量，不整单作废
		}
		out = append(out, items...)
		if meta.Total > total {
			total = meta.Total
		}
		if len(items) < pageSize {
			break
		}
		if total > 0 && len(out) >= total {
			break
		}
	}
	sortByPubDate(out)
	if maxItems > 0 && len(out) > maxItems {
		out = out[:maxItems]
	}
	return out, nil
}

// sortByPubDate 原地按发布时间新→旧排序（稳定序）。
func sortByPubDate(items []Item) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].PubDate > items[j].PubDate })
}

// isURL 粗判地址是否链接（带 scheme 或以 bilibili 域名开头）。
func isURL(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "://") || strings.HasPrefix(s, "space.bilibili.com") ||
		strings.HasPrefix(s, "www.bilibili.com") || strings.HasPrefix(s, "bilibili.com") ||
		strings.HasPrefix(s, "m.bilibili.com")
}
