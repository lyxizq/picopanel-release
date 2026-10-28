package store

import (
	"time"
)

// PageSettings 主页全局配置（单行，id=1）：标题、搜索框显隐、链接分组展示模式、
// 登录头像显隐、数据监控自动刷新间隔（RefreshSec 0=不自动刷新）与首页天气配置。
// LinkMode: horizontal=横向手风琴（同时展开一组）/ vertical=竖向手风琴（默认全展开）。
// WeatherMode: auto=按访客 IP 自动定位 / manual=手动城市（WeatherLat/WeatherLon 取数）。
type PageSettings struct {
	Title           string  `json:"title"`
	ShowSearch      bool    `json:"show_search"`
	LinkMode        string  `json:"link_mode"`
	ShowAvatar      bool    `json:"show_avatar"`
	ShowHA          bool    `json:"show_ha"`
	ShowStats       bool    `json:"show_stats"`
	RefreshSec      int     `json:"refresh_sec"`
	WeatherEnabled  bool    `json:"weather_enabled"`
	WeatherMode     string  `json:"weather_mode"`
	WeatherCity     string  `json:"weather_city"`
	WeatherLat      float64 `json:"weather_lat"`
	WeatherLon      float64 `json:"weather_lon"`
	WeatherProvider string  `json:"weather_provider"` // 空=Open-Meteo / gaode=高德 / qweather=和风
	WeatherKey      string  `json:"weather_key"`      // 服务商 API Key（高德 Web 服务 key / 和风 API Key）
	WeatherHost     string  `json:"weather_api_host"` // 和风专属 API Host（控制台设置页查看；v1 起必配，host 参与鉴权）
	ShowCountdown   bool    `json:"show_countdown"`   // 主页倒数日卡显隐（个人助理批，默认显示；无倒数日记录时卡自然不出）
}

// PageGroup 对应 page_groups 一行：主页链接分组（横向手风琴一个面板）。
type PageGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// PageLink 对应 page_links 一行：分组内的一个自定义链接。
type PageLink struct {
	ID      int64  `json:"id"`
	GroupID int64  `json:"group_id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Icon    string `json:"icon"`
	Hidden  bool   `json:"hidden"` // 主页不显示该链接（默认显示，管理抽屉逐条开关）
}

// GetPageSettings 读主页配置；行不存在（首次使用）返回默认值。
func (s *Store) GetPageSettings() (*PageSettings, error) {
	var title, linkMode, wxMode, wxCity, wxProvider, wxKey, wxHost string
	var show, avatar, ha, stats, refreshSec, wxEnabled, showCountdown int64
	var wxLat, wxLon float64
	err := s.db.QueryRow(`SELECT title, show_search, link_mode, show_avatar, show_ha, show_stats, refresh_sec,
		weather_enabled, weather_mode, weather_city, weather_lat, weather_lon, weather_provider, weather_key, weather_api_host, show_countdown
		FROM page_settings WHERE id = 1`).Scan(&title, &show, &linkMode, &avatar, &ha, &stats, &refreshSec,
		&wxEnabled, &wxMode, &wxCity, &wxLat, &wxLon, &wxProvider, &wxKey, &wxHost, &showCountdown)
	if err != nil {
		// 首次使用尚未写配置行：返回默认（空标题由前端兜底展示名，竖向手风琴，
		// 显示头像，显示智能家居钮，显示统计概览，30s 刷新，天气关、自动定位，显示版本号与倒数日卡）
		return &PageSettings{Title: "", ShowSearch: true, LinkMode: "vertical", ShowAvatar: true,
			ShowHA: true, ShowStats: true, RefreshSec: 30, WeatherMode: "auto", ShowCountdown: true}, nil
	}
	if linkMode != "horizontal" {
		linkMode = "vertical" // 旧值/空值兜底
	}
	if refreshSec < 0 || refreshSec > 86400 {
		refreshSec = 30
	}
	if wxMode != "manual" {
		wxMode = "auto"
	}
	if wxProvider != "gaode" && wxProvider != "qweather" {
		wxProvider = "" // 空=Open-Meteo
	}
	return &PageSettings{Title: title, ShowSearch: show != 0, LinkMode: linkMode, ShowAvatar: avatar != 0,
		ShowHA:     ha != 0,
		ShowStats:  stats != 0,
		RefreshSec: int(refreshSec), WeatherEnabled: wxEnabled != 0, WeatherMode: wxMode,
		WeatherCity: wxCity, WeatherLat: wxLat, WeatherLon: wxLon,
		WeatherProvider: wxProvider, WeatherKey: wxKey, WeatherHost: wxHost,
		ShowCountdown: showCountdown != 0}, nil
}

// SavePageSettings 覆盖保存主页配置（单行 upsert，含天气配置）。
func (s *Store) SavePageSettings(p *PageSettings) error {
	show, avatar, ha, stats := 0, 0, 0, 0
	if p.ShowSearch {
		show = 1
	}
	if p.ShowAvatar {
		avatar = 1
	}
	if p.ShowHA {
		ha = 1
	}
	if p.ShowStats {
		stats = 1
	}
	if p.LinkMode != "horizontal" {
		p.LinkMode = "vertical"
	}
	if p.RefreshSec < 0 || p.RefreshSec > 86400 {
		p.RefreshSec = 30
	}
	if p.WeatherMode != "manual" {
		p.WeatherMode = "auto"
	}
	if p.WeatherProvider != "gaode" && p.WeatherProvider != "qweather" {
		p.WeatherProvider = "" // 空=Open-Meteo
	}
	we := 0
	if p.WeatherEnabled {
		we = 1
	}
	sc := 0
	if p.ShowCountdown {
		sc = 1
	}
	_, err := s.db.Exec(`INSERT INTO page_settings (id, title, show_search, link_mode, show_avatar, show_ha, show_stats, refresh_sec,
		weather_enabled, weather_mode, weather_city, weather_lat, weather_lon, weather_provider, weather_key, weather_api_host, show_countdown, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET title = excluded.title, show_search = excluded.show_search,
		link_mode = excluded.link_mode, show_avatar = excluded.show_avatar, show_ha = excluded.show_ha,
		show_stats = excluded.show_stats, refresh_sec = excluded.refresh_sec,
		weather_enabled = excluded.weather_enabled, weather_mode = excluded.weather_mode,
		weather_city = excluded.weather_city, weather_lat = excluded.weather_lat, weather_lon = excluded.weather_lon,
		weather_provider = excluded.weather_provider, weather_key = excluded.weather_key,
		weather_api_host = excluded.weather_api_host,
		show_countdown = excluded.show_countdown, updated_at = excluded.updated_at`,
		p.Title, show, p.LinkMode, avatar, ha, stats, p.RefreshSec,
		we, p.WeatherMode, p.WeatherCity, p.WeatherLat, p.WeatherLon, p.WeatherProvider, p.WeatherKey, p.WeatherHost, sc, time.Now().Unix())
	return err
}

// ListPageGroups 全部分组，按拖拽排序（sort 未排过的按创建序兜底）。
func (s *Store) ListPageGroups() ([]PageGroup, error) {
	rows, err := s.db.Query(`SELECT id, name FROM page_groups ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []PageGroup
	for rows.Next() {
		var g PageGroup
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

// CreatePageGroup 新增分组（数量上限由 API 层校验）。
func (s *Store) CreatePageGroup(name string) (*PageGroup, error) {
	res, err := s.db.Exec(`INSERT INTO page_groups (name, sort, created_at) VALUES (?, COALESCE((SELECT MAX(sort) FROM page_groups), 0) + 1, ?)`, name, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &PageGroup{ID: id, Name: name}, nil
}

// RenamePageGroup 分组改名。
func (s *Store) RenamePageGroup(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE page_groups SET name = ? WHERE id = ?`, name, id)
	return err
}

// DeletePageGroup 删除分组并连带清理组内链接（单事务，同删服务器清理模式）。
func (s *Store) DeletePageGroup(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM page_links WHERE group_id = ?`,
		`DELETE FROM page_groups WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReorderPageGroups 按传入 id 顺序写入 sort 1..n（拖拽排序保存）。
func (s *Store) ReorderPageGroups(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE page_groups SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListPageLinks 全部链接（group_id 归组由前端消费；组内按拖拽序/创建序）。
func (s *Store) ListPageLinks() ([]PageLink, error) {
	rows, err := s.db.Query(`SELECT id, group_id, name, url, icon, hidden FROM page_links ORDER BY group_id, sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []PageLink
	for rows.Next() {
		var l PageLink
		var hidden int64
		if err := rows.Scan(&l.ID, &l.GroupID, &l.Name, &l.URL, &l.Icon, &hidden); err != nil {
			return nil, err
		}
		l.Hidden = hidden != 0
		list = append(list, l)
	}
	return list, rows.Err()
}

// SetPageLinkHidden 只改单条链接的主页显隐（逐条开关，不连累编辑表单）。
func (s *Store) SetPageLinkHidden(id int64, hidden bool) error {
	h := 0
	if hidden {
		h = 1
	}
	_, err := s.db.Exec(`UPDATE page_links SET hidden = ? WHERE id = ?`, h, id)
	return err
}

// CreatePageLink 新增链接（组存在与数量上限由 API 层校验；sort 追加到组尾，
// 拖拽排序后存量 sort≥1，不赋值会默认 0 排到最前）。
func (s *Store) CreatePageLink(groupID int64, name, url, icon string) (*PageLink, error) {
	res, err := s.db.Exec(`INSERT INTO page_links (group_id, name, url, icon, sort, created_at)
		VALUES (?, ?, ?, ?, COALESCE((SELECT MAX(sort) FROM page_links WHERE group_id = ?), 0) + 1, ?)`,
		groupID, name, url, icon, groupID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &PageLink{ID: id, GroupID: groupID, Name: name, URL: url, Icon: icon}, nil
}

// UpdatePageLink 编辑链接（不含归组变更——移动分组场景少，删了重加即可）。
func (s *Store) UpdatePageLink(id int64, name, url, icon string) error {
	_, err := s.db.Exec(`UPDATE page_links SET name = ?, url = ?, icon = ? WHERE id = ?`, name, url, icon, id)
	return err
}

// DeletePageLink 删除一个链接。
func (s *Store) DeletePageLink(id int64) error {
	_, err := s.db.Exec(`DELETE FROM page_links WHERE id = ?`, id)
	return err
}

// CountPageGroups 当前分组数（上限校验用）。
func (s *Store) CountPageGroups() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM page_groups`).Scan(&n)
	return n, err
}

// CountPageLinksInGroup 某组内链接数（上限校验用）。
func (s *Store) CountPageLinksInGroup(groupID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM page_links WHERE group_id = ?`, groupID).Scan(&n)
	return n, err
}

// ReorderPageLinks 按传入 id 顺序写入 sort 1..n（组内/跨组拖拽排序保存）；
// groupID > 0 时同时把这些链接归入该组（主页跨组拖拽）。
func (s *Store) ReorderPageLinks(ids []int64, groupID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if groupID > 0 {
		for _, id := range ids {
			if _, err := tx.Exec(`UPDATE page_links SET group_id = ? WHERE id = ?`, groupID, id); err != nil {
				return err
			}
		}
	}
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE page_links SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PageMetric 对应 page_metrics 一行：主页数据监控模块（与总览「数据监控」
// 同构但独立配置，Link 点击跳转、Icon 标签前小图标，均选填）。数据源二选一
// （486）：AIID=0 时 ServerID 指向服务器（数值取 /servers 最新快照）；
// AIID>0 时为用量查询格子（Metric 取 ai_ 前缀指标，数值取用量账户最新快照）。
type PageMetric struct {
	ID       int64  `json:"id"`
	ServerID int64  `json:"server_id"`
	AIID     int64  `json:"ai_id"`
	Metric   string `json:"metric"`
	Param    string `json:"param"`
	Label    string `json:"label"`
	Link     string `json:"link"`
	Icon     string `json:"icon"`
	Hidden   bool   `json:"hidden"` // 主页不显示该格子（默认显示，管理抽屉逐格开关）
}

// ListPageMetrics 全部主页监控模块，按拖拽排序（未排过的按创建序兜底）。
func (s *Store) ListPageMetrics() ([]PageMetric, error) {
	rows, err := s.db.Query(`SELECT id, server_id, ai_id, metric, param, label, link, icon, hidden FROM page_metrics ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []PageMetric
	for rows.Next() {
		var m PageMetric
		var hidden int64
		if err := rows.Scan(&m.ID, &m.ServerID, &m.AIID, &m.Metric, &m.Param, &m.Label, &m.Link, &m.Icon, &hidden); err != nil {
			return nil, err
		}
		m.Hidden = hidden != 0
		list = append(list, m)
	}
	return list, rows.Err()
}

// SetPageMetricHidden 只改单个格子的主页显隐（逐格开关，不连累编辑表单）。
func (s *Store) SetPageMetricHidden(id int64, hidden bool) error {
	h := 0
	if hidden {
		h = 1
	}
	_, err := s.db.Exec(`UPDATE page_metrics SET hidden = ? WHERE id = ?`, h, id)
	return err
}

// CreatePageMetric 新增主页监控模块（数量上限与指标合法性由 API 层校验；用量格子 ServerID 传 0）。
func (s *Store) CreatePageMetric(serverID, aiID int64, metric, param, label, link, icon string) (*PageMetric, error) {
	res, err := s.db.Exec(
		`INSERT INTO page_metrics (server_id, ai_id, metric, param, label, link, icon, sort, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT MAX(sort) FROM page_metrics), 0) + 1, ?)`,
		serverID, aiID, metric, param, label, link, icon, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &PageMetric{ID: id, ServerID: serverID, AIID: aiID, Metric: metric, Param: param, Label: label, Link: link, Icon: icon}, nil
}

// UpdatePageMetric 编辑一个主页监控模块（不含排序）。
func (s *Store) UpdatePageMetric(id, serverID, aiID int64, metric, param, label, link, icon string) error {
	_, err := s.db.Exec(`UPDATE page_metrics SET server_id = ?, ai_id = ?, metric = ?, param = ?, label = ?, link = ?, icon = ? WHERE id = ?`,
		serverID, aiID, metric, param, label, link, icon, id)
	return err
}

// DeletePageMetric 删除一个主页监控模块。
func (s *Store) DeletePageMetric(id int64) error {
	_, err := s.db.Exec(`DELETE FROM page_metrics WHERE id = ?`, id)
	return err
}

// CountPageMetrics 当前模块数（上限校验用）。
func (s *Store) CountPageMetrics() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM page_metrics`).Scan(&n)
	return n, err
}

// ReorderPageMetrics 按传入 id 顺序写入 sort 1..n（拖拽排序保存）。
func (s *Store) ReorderPageMetrics(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE page_metrics SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
