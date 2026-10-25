package store

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// GitHub 仓库发布监控（466）：gh_repos 记监控的仓库（owner/repo 唯一、启停），
// gh_releases 存各仓最近若干条 Release 快照（tag/名称/预发布/发布时间/外链/
// 更新说明），每轮刷新整组重建（最新 N 条覆盖写，库内不积历史）。

const (
	// MaxGhRepos 监控仓库上限（对齐服务监控服务器上限）。
	MaxGhRepos = 16
	// GhReleasesKept 每仓保留的最近 Release 条数。
	GhReleasesKept = 5
)

// GhPollSeconds 轮询间隔（秒）七档白名单与默认值（4 小时）。settings 存
// gh_poll_sec，每轮轮询前重读，改后下一轮生效。
const (
	settingGhPollSec = "gh_poll_sec"
	// GhPollDefault 默认轮询间隔秒（4 小时）。
	GhPollDefault = 4 * 3600
)

// GhPollOptions 轮询间隔可选档位（秒）：30 分 / 1 / 2 / 4 / 6 / 12 / 24 小时。
var GhPollOptions = []int{1800, 3600, 7200, GhPollDefault, 6 * 3600, 12 * 3600, 24 * 3600}

// GhPollSeconds 读轮询间隔：缺省或值不在白名单（含脏数据）回默认 4 小时。
func (s *Store) GhPollSeconds() int {
	v, ok, err := s.GetSetting(settingGhPollSec)
	if err != nil || !ok {
		return GhPollDefault
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return GhPollDefault
	}
	for _, o := range GhPollOptions {
		if n == o {
			return n
		}
	}
	return GhPollDefault
}

// SetGhPollSeconds 写轮询间隔（API 层已按白名单校验）。
func (s *Store) SetGhPollSeconds(sec int) error {
	return s.SetSetting(settingGhPollSec, strconv.Itoa(sec))
}

// GhRepo 对应 gh_repos 一行。LastOKAt/LastError 为最近一次轮询结果；DisplayName
// 为可选显示名（472，空=前端回落 owner/repo）；SeenAt 为已看时刻（489，之后
// 发布的 Release 算「新更新」，卡片 new 角标与主页提醒同基准）。
type GhRepo struct {
	ID          int64  `json:"id"`
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
	LastOKAt    int64  `json:"last_ok_at"`
	LastError   string `json:"last_error"`
	SeenAt      int64  `json:"seen_at"`
}

// FullName owner/repo。
func (r *GhRepo) FullName() string { return r.Owner + "/" + r.Repo }

// GhRelease 一条 Release 快照（不含 repo_id，列表按仓库分组取）。
type GhRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt int64  `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
}

// ErrGhRepoNotFound 监控仓库不存在。
var ErrGhRepoNotFound = errors.New("GitHub 监控仓库不存在")

const ghReposDDL = `CREATE TABLE IF NOT EXISTS gh_repos (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	owner TEXT NOT NULL,
	repo TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	last_ok_at INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	display_name TEXT NOT NULL DEFAULT '',
	seen_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	UNIQUE(owner, repo)
)`

const ghReleasesDDL = `CREATE TABLE IF NOT EXISTS gh_releases (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	repo_id INTEGER NOT NULL,
	tag_name TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	prerelease INTEGER NOT NULL DEFAULT 0,
	published_at INTEGER NOT NULL DEFAULT 0,
	html_url TEXT NOT NULL DEFAULT '',
	body TEXT NOT NULL DEFAULT '',
	fetched_at INTEGER NOT NULL DEFAULT 0,
	UNIQUE(repo_id, tag_name)
)`

const ghReleasesIdxDDL = `CREATE INDEX IF NOT EXISTS idx_gh_releases_repo ON gh_releases (repo_id, published_at DESC)`

const ghRepoCols = `id, owner, repo, enabled, last_ok_at, last_error, display_name, seen_at`

func scanGhRepo(row interface{ Scan(...any) error }) (*GhRepo, error) {
	var (
		r       GhRepo
		enabled int
	)
	if err := row.Scan(&r.ID, &r.Owner, &r.Repo, &enabled, &r.LastOKAt, &r.LastError, &r.DisplayName, &r.SeenAt); err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	return &r, nil
}

// ListGhRepos 全部监控仓库，按创建顺序。
func (s *Store) ListGhRepos() ([]GhRepo, error) {
	rows, err := s.db.Query(`SELECT ` + ghRepoCols + ` FROM gh_repos ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GhRepo
	for rows.Next() {
		r, err := scanGhRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CountGhRepos 监控仓库数（上限校验用）。
func (s *Store) CountGhRepos() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM gh_repos`).Scan(&n)
	return n, err
}

// GetGhRepo 单个监控仓库。
func (s *Store) GetGhRepo(id int64) (*GhRepo, error) {
	r, err := scanGhRepo(s.db.QueryRow(`SELECT `+ghRepoCols+` FROM gh_repos WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGhRepoNotFound
	}
	return r, err
}

// GetGhRepoBySlug 按 owner/repo 查重（不存在返回 ErrGhRepoNotFound）。
func (s *Store) GetGhRepoBySlug(owner, repo string) (*GhRepo, error) {
	r, err := scanGhRepo(s.db.QueryRow(`SELECT `+ghRepoCols+` FROM gh_repos WHERE owner = ? AND repo = ?`, owner, repo))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrGhRepoNotFound
	}
	return r, err
}

// CreateGhRepo 新建监控仓库（slug 唯一，撞唯一约束时回 ErrGhRepoExists）。
// seen_at=建仓时刻（474）：首刷带回的存量 Release 都不算「新更新」，只有之后
// 发布的才在主页提醒。
func (s *Store) CreateGhRepo(r *GhRepo) (int64, error) {
	r.Owner, r.Repo = strings.TrimSpace(r.Owner), strings.TrimSpace(r.Repo)
	res, err := s.db.Exec(`INSERT INTO gh_repos (owner, repo, enabled, display_name, seen_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Owner, r.Repo, boolInt(r.Enabled), r.DisplayName, time.Now().Unix(), time.Now().Unix(), time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrGhRepoExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

// ErrGhRepoExists 仓库已在监控清单中。
var ErrGhRepoExists = errors.New("该仓库已在监控清单中")

// UpdateGhRepoEnabled 启停仓库（快照保留，停用只是不再轮询）。
func (s *Store) UpdateGhRepoEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE gh_repos SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolInt(enabled), time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrGhRepoNotFound
	}
	return nil
}

// DeleteGhRepo 删除仓库及其全部 Release 快照。
func (s *Store) DeleteGhRepo(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM gh_releases WHERE repo_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	res, err := tx.Exec(`DELETE FROM gh_repos WHERE id = ?`, id)
	if err != nil {
		tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		return ErrGhRepoNotFound
	}
	return tx.Commit()
}

// MarkGhRepoPoll 记一次轮询结果：成功推进 last_ok_at 并清错误，失败只记错误。
func (s *Store) MarkGhRepoPoll(id int64, errMsg string) error {
	now := time.Now().Unix()
	if errMsg == "" {
		_, err := s.db.Exec(`UPDATE gh_repos SET last_ok_at = ?, last_error = '', updated_at = ? WHERE id = ?`, now, now, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE gh_repos SET last_error = ?, updated_at = ? WHERE id = ?`, errMsg, now, id)
	return err
}

// MarkGhRepoSeen 单仓记账「已看过」（489）：GhView 卡片上查看更多弹层/点开
// 最新 Release/点角标任一即调用，清该卡的 new 角标与主页统计卡的「有更新」
// 提醒（seen_at 之后的 Release 不再算新）。seen_at 取 now 与该仓当前快照里
// 最大发布时刻的较大者——未来定档（计划发布）的 Release 也能一次清掉，
// 不会永久挂在角标上。
func (s *Store) MarkGhRepoSeen(id int64) error {
	res, err := s.db.Exec(`UPDATE gh_repos SET seen_at = MAX(?, COALESCE(
		(SELECT MAX(published_at) FROM gh_releases WHERE repo_id = gh_repos.id), 0))
		WHERE id = ?`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrGhRepoNotFound
	}
	return nil
}

// ReplaceGhReleases 一轮抓取结果整组重建：删该仓旧快照后写入最新一组
//（单事务，失败不留半截）。rels 由调用方截到 GhReleasesKept 条内。
func (s *Store) ReplaceGhReleases(repoID int64, rels []GhRelease) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM gh_releases WHERE repo_id = ?`, repoID); err != nil {
		return err
	}
	now := time.Now().Unix()
	for _, r := range rels {
		if _, err := tx.Exec(`INSERT INTO gh_releases (repo_id, tag_name, name, prerelease, published_at, html_url, body, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			repoID, r.TagName, r.Name, boolInt(r.Prerelease), r.PublishedAt, r.HTMLURL, r.Body, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListGhReleases 单仓 Release 快照，发布时间新→旧（同刻按入库 id 兜底）。
func (s *Store) ListGhReleases(repoID int64) ([]GhRelease, error) {
	rows, err := s.db.Query(`SELECT tag_name, name, prerelease, published_at, html_url, body
		FROM gh_releases WHERE repo_id = ? ORDER BY published_at DESC, id DESC`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GhRelease
	for rows.Next() {
		var (
			r      GhRelease
			pre    int
		)
		if err := rows.Scan(&r.TagName, &r.Name, &pre, &r.PublishedAt, &r.HTMLURL, &r.Body); err != nil {
			return nil, err
		}
		r.Prerelease = pre != 0
		out = append(out, r)
	}
	return out, rows.Err()
}
