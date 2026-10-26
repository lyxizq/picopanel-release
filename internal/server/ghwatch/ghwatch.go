// Package ghwatch GitHub 仓库发布监控（466）：按仓库轮询 GitHub Releases 公开
// API，把最近若干条已发布版本（含 prerelease，草稿不显示）整组写库，前端直读
// 快照。仿 svcmon Manager：常驻调度 + 单仓失败互不影响 + 忙标记防重入。
package ghwatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

const (
	// HTTPTimeout 单次 GitHub API 请求超时。
	HTTPTimeout = 15 * time.Second
	// pollFloor 轮询间隔防呆下限（正常值由 store 白名单保证，脏数据兜底）。
	pollFloor = 30 * time.Second
	// maxBody Release 更新说明入库截断（纯文本展示，无需全文）。
	maxBody = 16 << 10
	// userAgent GitHub API 必带 UA。
	userAgent = "PicoPanel"
)

var (
	// owner/repo 形态校验：GitHub owner（用户/组织）仅限字母数字与连字符、
	// 不允许点（借以拒绝 gitlab.com/x 这类误把域名当 owner 的输入，域名会
	// 走到 owner 段含点而报错）；repo 宽松兼容点/下划线。长度按 GitHub 上限
	//（owner 39 / repo 100）。
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	repoRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
)

// ParseRepoInput 解析用户输入为 owner/repo：接受 owner/repo 或 GitHub 仓库
// URL（含 .git 后缀、/tree/xx 等深层路径、末尾斜杠），其余形态报错。
func ParseRepoInput(s string) (owner, repo string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", errors.New("请填写仓库地址或 owner/repo")
	}
	if i := strings.Index(s, "://"); i >= 0 {
		u, perr := url.Parse(s)
		if perr != nil {
			return "", "", errors.New("地址解析失败")
		}
		host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		if host != "github.com" {
			return "", "", errors.New("仅支持 github.com 仓库地址")
		}
		s = strings.Trim(u.Path, "/")
	} else {
		// 无 scheme 形态：剥协议相对前缀与主机段（大小写不敏感匹配，保留原样）
		lower := strings.ToLower(s)
		if strings.HasPrefix(lower, "//") {
			s, lower = s[2:], lower[2:]
		}
		for _, host := range []string{"www.github.com/", "github.com/"} {
			if strings.HasPrefix(lower, host) {
				s, lower = s[len(host):], lower[len(host):]
				break
			}
		}
		s = strings.Trim(s, "/")
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("格式应为 owner/repo 或 GitHub 仓库 URL")
	}
	owner, repo = parts[0], parts[1]
	repo = strings.TrimSuffix(repo, ".git")
	if !ownerRe.MatchString(owner) {
		return "", "", errors.New("仓库名 owner 形态不合法")
	}
	if !repoRe.MatchString(repo) {
		return "", "", errors.New("仓库名 repo 形态不合法")
	}
	return owner, repo, nil
}

// Manager 常驻调度：按可调间隔（store.GhPollSeconds，默认 4 小时）全量刷新
// 启用仓库，单仓忙标记防轮询与手动刷新重入。apiBase 测试可替换。
type Manager struct {
	st      *store.Store
	apiBase string
	hc      *http.Client
	mu      sync.Mutex
	busy    map[int64]bool
}

// New 构造。
func New(st *store.Store) *Manager {
	return &Manager{
		st:      st,
		apiBase: "https://api.github.com",
		hc:      &http.Client{Timeout: HTTPTimeout},
		busy:    map[int64]bool{},
	}
}

// SetAPIBase 换 API 基址（测试注入 httptest mock 用）。
func (m *Manager) SetAPIBase(base string) { m.apiBase = base }

// Run 常驻调度（ctx 取消退出）：启动先刷一轮把快照快速填上；轮询间隔每轮
// 重读（store.GhPollSeconds，七档白名单、默认 4 小时），改后下一轮生效。
func (m *Manager) Run(ctx context.Context) {
	for {
		m.refreshEnabled()
		interval := time.Duration(m.st.GhPollSeconds()) * time.Second
		if interval < pollFloor {
			interval = pollFloor
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// refreshEnabled 并发拉取全部启用仓库（小清单，并发 4 足够；单仓失败只记
// last_error 不影响其他仓）。
func (m *Manager) refreshEnabled() {
	repos, err := m.st.ListGhRepos()
	if err != nil {
		log.Printf("ghwatch 读仓库清单失败: %v", err)
		return
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range repos {
		r := repos[i]
		if !r.Enabled {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			m.refreshOne(&r)
			<-sem
		}()
	}
	wg.Wait()
	m.gc(repos)
}

// refreshOne 刷新单仓：抢忙标记（已在轮询/手动刷新中则跳过本轮）→ 拉取 →
// 整组重建快照 → 记轮询结果。返回错误同时已落 last_error。
func (m *Manager) refreshOne(r *store.GhRepo) error {
	m.mu.Lock()
	if m.busy[r.ID] {
		m.mu.Unlock()
		return nil
	}
	m.busy[r.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, r.ID)
		m.mu.Unlock()
	}()
	rels, err := m.FetchReleases(r.Owner, r.Repo)
	if err != nil {
		_ = m.st.MarkGhRepoPoll(r.ID, err.Error())
		log.Printf("ghwatch 刷新 %s 失败: %v", r.FullName(), err)
		return err
	}
	if err := m.st.ReplaceGhReleases(r.ID, rels); err != nil {
		_ = m.st.MarkGhRepoPoll(r.ID, "写库失败: "+err.Error())
		return err
	}
	_ = m.st.MarkGhRepoPoll(r.ID, "")
	return nil
}

// RefreshNow 手动刷新单仓（同步，返回错误给调用方展示）。已在刷新中等它结束
// 直接返回 nil（结果以库内最新为准）。
func (m *Manager) RefreshNow(id int64) error {
	r, err := m.st.GetGhRepo(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	busy := m.busy[r.ID]
	m.mu.Unlock()
	if busy {
		return nil
	}
	return m.refreshOne(r)
}

// KickAll 全部启用仓库立即重刷（后台异步，前端靠列表轮询看到结果）。
func (m *Manager) KickAll() {
	repos, err := m.st.ListGhRepos()
	if err != nil {
		return
	}
	go func() {
		for i := range repos {
			if repos[i].Enabled {
				_ = m.refreshOne(&repos[i])
			}
		}
	}()
}

// Drop 删除仓库时清调度状态。
func (m *Manager) Drop(id int64) {
	m.mu.Lock()
	delete(m.busy, id)
	m.mu.Unlock()
}

func (m *Manager) gc(repos []store.GhRepo) {
	alive := map[int64]bool{}
	for i := range repos {
		alive[repos[i].ID] = true
	}
	m.mu.Lock()
	for id := range m.busy {
		if !alive[id] {
			delete(m.busy, id)
		}
	}
	m.mu.Unlock()
}

// ghReleaseJSON GitHub Releases API 条目（只取展示所需字段）。
type ghReleaseJSON struct {
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name"`
	Body        string     `json:"body"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	HTMLURL     string     `json:"html_url"`
	PublishedAt *time.Time `json:"published_at"`
}

// FetchReleases 拉取单仓最近 store.GhReleasesKept 条已发布 Release（草稿跳过）。
func (m *Manager) FetchReleases(owner, repo string) ([]store.GhRelease, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d", m.apiBase, owner, repo, store.GhReleasesKept*2)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitHub 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		if resp.StatusCode == http.StatusForbidden && strings.Contains(strings.ToLower(string(body)), "rate limit") {
			return nil, errors.New("GitHub API 配额用尽，稍后自动重试")
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, errors.New("仓库不存在或不可公开访问")
		}
		return nil, fmt.Errorf("GitHub API HTTP %d", resp.StatusCode)
	}
	var raw []ghReleaseJSON
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析 GitHub 响应失败: %w", err)
	}
	out := make([]store.GhRelease, 0, store.GhReleasesKept)
	for _, j := range raw {
		if j.Draft {
			continue
		}
		r := store.GhRelease{
			TagName:    j.TagName,
			Name:       j.Name,
			Prerelease: j.Prerelease,
			HTMLURL:    j.HTMLURL,
			Body:       strings.TrimSuffix(j.Body[:min(len(j.Body), maxBody)], "\n"),
		}
		if j.PublishedAt != nil {
			r.PublishedAt = j.PublishedAt.Unix()
		}
		out = append(out, r)
		if len(out) >= store.GhReleasesKept {
			break
		}
	}
	return out, nil
}
