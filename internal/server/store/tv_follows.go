package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// tv_follows 追剧条目表（548）：TMDB 条目绑定 + 最新播出进度缓存 + 推送打标。
// 类型 tv（追踪集数更新）与 movie（只做在库展示，不追踪）；detail 查询经通用
// 中转任务（net_tasks，purpose=tvfollow_detail）由家里 push agent 代为执行，
// 回传后回写本表。in_emby 与 emby_item_name 为 Emby 在库检查（549）结果缓存。
const tvFollowsDDL = `CREATE TABLE IF NOT EXISTS tv_follows (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  type               TEXT    NOT NULL DEFAULT 'tv',
  tmdb_id            INTEGER NOT NULL,
  name               TEXT    NOT NULL DEFAULT '',
  original_name      TEXT    NOT NULL DEFAULT '',
  poster_path        TEXT    NOT NULL DEFAULT '',
  first_air_date     TEXT    NOT NULL DEFAULT '',
  status             TEXT    NOT NULL DEFAULT '',
  seasons            INTEGER NOT NULL DEFAULT 0,
  episodes           INTEGER NOT NULL DEFAULT 0,
  last_season        INTEGER NOT NULL DEFAULT 0,
  last_episode       INTEGER NOT NULL DEFAULT 0,
  last_air_date      TEXT    NOT NULL DEFAULT '',
  next_season        INTEGER NOT NULL DEFAULT 0,
  next_episode       INTEGER NOT NULL DEFAULT 0,
  next_air_date      TEXT    NOT NULL DEFAULT '',
  in_emby            INTEGER NOT NULL DEFAULT 0,
  emby_item_name     TEXT    NOT NULL DEFAULT '',
  emby_checked_at    INTEGER NOT NULL DEFAULT 0,
  actors            TEXT    NOT NULL DEFAULT '',
  overview          TEXT    NOT NULL DEFAULT '',
  episode_name      TEXT    NOT NULL DEFAULT '',
  platforms         TEXT    NOT NULL DEFAULT '',
  short_drama       INTEGER NOT NULL DEFAULT 0,
  done               INTEGER NOT NULL DEFAULT 0,
  enabled            INTEGER NOT NULL DEFAULT 1,
  notify_episode_key TEXT    NOT NULL DEFAULT '',
  last_queued_at     INTEGER NOT NULL DEFAULT 0,
  last_checked_at    INTEGER NOT NULL DEFAULT 0,
  last_error         TEXT    NOT NULL DEFAULT '',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL,
  UNIQUE(type, tmdb_id)
);`

// TVFollowType 剧/影两类。
const (
	TVFollowTV    = "tv"
	TVFollowMovie = "movie"
)

// TVFollow 一条追剧记录。LastSeries/LastEpisode 为 TMDB last_episode_to_air
// 快照（最新已播集），Next* 为 next_episode_to_air（未播下一集，可能为空）。
type TVFollow struct {
	ID               int64  `json:"id"`
	Type             string `json:"type"`
	TmdbID           int64  `json:"tmdb_id"`
	Name             string `json:"name"`
	OriginalName     string `json:"original_name"`
	PosterPath       string `json:"poster_path"`
	FirstAirDate     string `json:"first_air_date"`
	Status           string `json:"status"`
	Seasons          int    `json:"seasons"`
	Episodes         int    `json:"episodes"`
	LastSeason       int    `json:"last_season"`
	LastEpisode      int    `json:"last_episode"`
	LastAirDate      string `json:"last_air_date"`
	NextSeason       int    `json:"next_season"`
	NextEpisode      int    `json:"next_episode"`
	NextAirDate      string `json:"next_air_date"`
	InEmby           bool   `json:"in_emby"`
	EmbyItemName     string `json:"emby_item_name"`
	EmbyCheckedAt    int64  `json:"emby_checked_at"`
	Cast             string `json:"cast"`         // 主要演员（TMDB credits 前 4 位，"A/B/C/D"；550）
	Overview         string `json:"overview"`     // 简介（候选带出 + detail 回写刷新，554）
	EpisodeName      string `json:"episode_name"` // 最新集名（554）
	Platforms        string `json:"platforms"`    // 播出平台（TMDB watch/providers CN 优先 US 兜底，564）
	ShortDrama       bool   `json:"short_drama"`  // 短剧标记（58）：纯手动标注，TMDB detail 回写不覆盖
	Done             bool   `json:"done"`
	Enabled          bool   `json:"enabled"`
	NotifyEpisodeKey string `json:"-"`
	LastQueuedAt     int64  `json:"last_queued_at"`
	LastCheckedAt    int64  `json:"last_checked_at"`
	LastError        string `json:"last_error"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// ErrTVFollowExists / ErrTVFollowNotFound 追剧条目错误。
var (
	ErrTVFollowExists   = errors.New("该条目已在追剧列表中")
	ErrTVFollowNotFound = errors.New("追剧条目不存在")
)

const tvFollowCols = `id, type, tmdb_id, name, original_name, poster_path, first_air_date, status,
  seasons, episodes, last_season, last_episode, last_air_date, next_season, next_episode, next_air_date,
  in_emby, emby_item_name, emby_checked_at, actors, overview, episode_name, platforms, short_drama, done, enabled, notify_episode_key,
  last_queued_at, last_checked_at, last_error, created_at, updated_at`

func scanTVFollow(row interface{ Scan(...any) error }) (*TVFollow, error) {
	var f TVFollow
	var inEmby, shortDrama, done, enabled int
	err := row.Scan(&f.ID, &f.Type, &f.TmdbID, &f.Name, &f.OriginalName, &f.PosterPath, &f.FirstAirDate,
		&f.Status, &f.Seasons, &f.Episodes, &f.LastSeason, &f.LastEpisode, &f.LastAirDate,
		&f.NextSeason, &f.NextEpisode, &f.NextAirDate,
		&inEmby, &f.EmbyItemName, &f.EmbyCheckedAt, &f.Cast, &f.Overview, &f.EpisodeName, &f.Platforms, &shortDrama, &done, &enabled, &f.NotifyEpisodeKey,
		&f.LastQueuedAt, &f.LastCheckedAt, &f.LastError, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTVFollowNotFound
	}
	if err != nil {
		return nil, err
	}
	f.InEmby, f.ShortDrama, f.Done, f.Enabled = inEmby == 1, shortDrama == 1, done == 1, enabled == 1
	return &f, nil
}

// ListTVFollows 全部追剧条目（新添加在前）。
func (s *Store) ListTVFollows() ([]TVFollow, error) {
	rows, err := s.db.Query(`SELECT ` + tvFollowCols + ` FROM tv_follows ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TVFollow
	for rows.Next() {
		f, err := scanTVFollow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// GetTVFollow 单条。
func (s *Store) GetTVFollow(id int64) (*TVFollow, error) {
	return scanTVFollow(s.db.QueryRow(`SELECT `+tvFollowCols+` FROM tv_follows WHERE id = ?`, id))
}

// CreateTVFollow 新增：类型校验 + 去重（type+tmdb_id 唯一，并发撞唯一索引
// 同样视为已存在）。
func (s *Store) CreateTVFollow(f *TVFollow) error {
	f.Type = strings.TrimSpace(f.Type)
	f.Name = strings.TrimSpace(f.Name)
	if f.Type != TVFollowTV && f.Type != TVFollowMovie {
		return errors.New("类型只支持 tv / movie")
	}
	if f.TmdbID <= 0 {
		return errors.New("缺少 TMDB 条目 ID")
	}
	if f.Name == "" {
		return errors.New("缺少名称")
	}
	now := time.Now().Unix()
	f.CreatedAt, f.UpdatedAt = now, now
	res, err := s.db.Exec(`INSERT INTO tv_follows (type, tmdb_id, name, original_name, poster_path, first_air_date, status, actors, overview, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Type, f.TmdbID, f.Name, f.OriginalName, f.PosterPath, f.FirstAirDate, f.Status, f.Cast, f.Overview, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrTVFollowExists
		}
		return err
	}
	f.ID, _ = res.LastInsertId()
	return nil
}

// DeleteTVFollow 删除。
func (s *Store) DeleteTVFollow(id int64) error {
	res, err := s.db.Exec(`DELETE FROM tv_follows WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTVFollowNotFound
	}
	return nil
}

// SetTVFollowEnabled 启停（停用不参与每日轮询）。
func (s *Store) SetTVFollowEnabled(id int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	res, err := s.db.Exec(`UPDATE tv_follows SET enabled=?, updated_at=? WHERE id = ?`, v, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTVFollowNotFound
	}
	return nil
}

// DueTVFollows 到期待查条目：启用中、未完结，且上次入队早于 cutoff（563 起
// 传固定检查时刻窗口起点）。5 号口径起电影一并轮询（追踪流媒体上线；电影无
// 完结概念 done 恒 0），此前只查剧。
func (s *Store) DueTVFollows(cutoff int64) ([]TVFollow, error) {
	rows, err := s.db.Query(`SELECT `+tvFollowCols+` FROM tv_follows
		WHERE enabled = 1 AND done = 0 AND (last_queued_at = 0 OR last_queued_at < ?) ORDER BY id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TVFollow
	for rows.Next() {
		f, err := scanTVFollow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// MarkTVFollowQueued 入队打标（防同一条目每 tick 重复入队——活跃任务唯一索引
// 是第二道防线，打标让轮询查询直接跳过）。
func (s *Store) MarkTVFollowQueued(id int64) {
	s.db.Exec(`UPDATE tv_follows SET last_queued_at=?, updated_at=? WHERE id = ?`, time.Now().Unix(), time.Now().Unix(), id)
}

// ResetTVFollowQueue 清全部入队打标（换绑出口后立即重查一轮）。
func (s *Store) ResetTVFollowQueue() {
	s.db.Exec(`UPDATE tv_follows SET last_queued_at=0, updated_at=?`, time.Now().Unix())
}

// UpdateTVFollowDetail 回写 detail 查询结果（net_tasks 回传后消费）。
// 未做在库检查时调用方原样回填旧的 in_emby/emby_item_name/emby_checked_at。
func (s *Store) UpdateTVFollowDetail(id int64, d *TVFollowDetailUpdate) error {
	res, err := s.db.Exec(`UPDATE tv_follows SET status=?, seasons=?, episodes=?,
		last_season=?, last_episode=?, last_air_date=?, next_season=?, next_episode=?, next_air_date=?,
		in_emby=?, emby_item_name=?, emby_checked_at=?, actors=?, overview=?, episode_name=?, platforms=?,
		done=?, notify_episode_key=?, last_checked_at=?, last_error=?, updated_at=? WHERE id = ?`,
		d.Status, d.Seasons, d.Episodes,
		d.LastSeason, d.LastEpisode, d.LastAirDate, d.NextSeason, d.NextEpisode, d.NextAirDate,
		boolInt(d.InEmby), d.EmbyItemName, d.EmbyCheckedAt, d.Cast, d.Overview, d.EpisodeName, d.Platforms,
		boolInt(d.Done), d.NotifyEpisodeKey, d.LastCheckedAt, d.LastError, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTVFollowNotFound
	}
	return nil
}

// TVFollowDetailUpdate detail 回传的回写载荷（tvfollow 模块组装）。
type TVFollowDetailUpdate struct {
	Status                  string
	Seasons, Episodes       int
	LastSeason, LastEpisode int
	LastAirDate             string
	NextSeason, NextEpisode int
	NextAirDate             string
	EpisodeName             string // 最新集名（554 起落库 + 推送正文用）
	InEmbyChecked           bool
	InEmby                  bool
	EmbyItemName            string
	EmbyCheckedAt           int64
	Cast                    string // 主要演员（"A/B/C/D"，550；每次 detail 回写覆盖）
	Overview                string // 简介（554）
	Platforms               string // 播出平台（564，每次 detail 回写覆盖）
	Done                    bool
	NotifyEpisodeKey        string
	LastCheckedAt           int64
	LastError               string
}

// SetTVFollowError 只记错误（查询失败不动进度基线，防误报新集/完结）。
func (s *Store) SetTVFollowError(id int64, msg string) {
	s.db.Exec(`UPDATE tv_follows SET last_error=?, updated_at=? WHERE id = ?`, msg, time.Now().Unix(), id)
}

// SetTVFollowDone 完结打标/取消（566 PT 通道「全集」判定用）。
func (s *Store) SetTVFollowDone(id int64, done bool) {
	s.db.Exec(`UPDATE tv_follows SET done=?, updated_at=? WHERE id = ?`, boolInt(done), time.Now().Unix(), id)
}

// SetTVFollowShortDrama 手动标记短剧（58）。详情弹层开关触发；TMDB detail 回写
// 不含此列，标注持久不被轮询覆盖。
func (s *Store) SetTVFollowShortDrama(id int64, on bool) error {
	res, err := s.db.Exec(`UPDATE tv_follows SET short_drama=?, updated_at=? WHERE id = ?`, boolInt(on), time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTVFollowNotFound
	}
	return nil
}

// MergePTProgress PT 通道进度水位（566）：仅在 (season,episode) 严格大于当前
// 时推进（跨源只进不退，防 TMDB/PT 回传互踩），同步刷新 notify_episode_key。
func (s *Store) MergePTProgress(id int64, season, episode int) error {
	key := fmt.Sprintf("S%dE%d", season, episode)
	_, err := s.db.Exec(`UPDATE tv_follows SET last_season=?, last_episode=?, notify_episode_key=?, updated_at=? WHERE id = ?`,
		season, episode, key, time.Now().Unix(), id)
	return err
}

// SetTVFollowEmby 回写在库检查（549）结果。
func (s *Store) SetTVFollowEmby(id int64, inEmby bool, itemName string, checkedAt int64) {
	s.db.Exec(`UPDATE tv_follows SET in_emby=?, emby_item_name=?, emby_checked_at=?, updated_at=? WHERE id = ?`,
		boolInt(inEmby), itemName, checkedAt, time.Now().Unix(), id)
}
