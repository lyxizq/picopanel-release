package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/ghwatch"
	"lz-monitor/internal/server/store"
)

// GhHandler GitHub 仓库发布监控接口（466，登录组）：仓库增删改查（列表直读
// 快照、带 has_new 新更新标记）、未入库地址测试、单仓同步刷新、全部异步刷新、
// 单仓已看记账（489）。快照由 ghwatch.Manager 常驻轮询维护。
type GhHandler struct {
	st *store.Store
	mg *ghwatch.Manager
}

// repoView 单仓视图：rels 为 nil 时回落查库（启停/首刷失败等路径也带全量快照
// 与 has_new）；has_new（489）=存在 seen_at 之后发布的 Release，卡片 new 角标
// 与主页「有更新」提醒同基准，published_at=0（未定档）永不算新。
func (h *GhHandler) repoView(r *store.GhRepo, rels []store.GhRelease) gin.H {
	if rels == nil {
		rels, _ = h.st.ListGhReleases(r.ID)
	}
	if rels == nil {
		rels = []store.GhRelease{}
	}
	hasNew := false
	for _, rel := range rels {
		if rel.PublishedAt > r.SeenAt {
			hasNew = true
			break
		}
	}
	return gin.H{"id": r.ID, "owner": r.Owner, "repo": r.Repo, "full_name": r.FullName(),
		"display_name": r.DisplayName, "url": "https://github.com/" + r.FullName(), "enabled": r.Enabled,
		"last_ok_at": r.LastOKAt, "last_error": r.LastError, "has_new": hasNew, "releases": rels,
		"stars": r.Stars, "watchers": r.Watchers, "forks": r.Forks}
}

func ghID(c *gin.Context) *int64 {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return nil
	}
	return &id
}

// list 全部监控仓库 + 各仓 Release 快照（按创建序，快照发布时间新→旧）。
func (h *GhHandler) list(c *gin.Context) {
	repos, err := h.st.ListGhRepos()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询监控仓库失败"})
		return
	}
	out := make([]gin.H, 0, len(repos))
	for i := range repos {
		r := &repos[i]
		rels, err := h.st.ListGhReleases(r.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询 Release 快照失败"})
			return
		}
		out = append(out, h.repoView(r, rels))
	}
	c.JSON(http.StatusOK, gin.H{"repos": out, "max": store.MaxGhRepos, "kept": store.GhReleasesKept,
		"poll_sec": h.st.GhPollSeconds(), "poll_options": store.GhPollOptions})
}

// saveSettings 轮询间隔设置（{poll_sec} 限七档白名单，改后下一轮生效）。
func (h *GhHandler) saveSettings(c *gin.Context) {
	var req struct {
		PollSec int `json:"poll_sec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	ok := false
	for _, o := range store.GhPollOptions {
		if req.PollSec == o {
			ok = true
			break
		}
	}
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "轮询间隔不在可选档位内"})
		return
	}
	if err := h.st.SetGhPollSeconds(req.PollSec); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "poll_sec": req.PollSec})
}

// create 添加监控仓库（{repo: "owner/repo" 或 GitHub URL, display_name: 可选显示名}）：
// 查重 → 建行 → 同步首刷填快照（拉取失败不拦入库，错误落 last_error 前端可见）。
func (h *GhHandler) create(c *gin.Context) {
	var req struct {
		Repo        string `json:"repo"`
		DisplayName string `json:"display_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	owner, repo, err := ghwatch.ParseRepoInput(req.Repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	if runes := []rune(name); len(runes) > 64 {
		name = string(runes[:64])
	}
	if _, err := h.st.GetGhRepoBySlug(owner, repo); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": store.ErrGhRepoExists.Error()})
		return
	} else if !errors.Is(err, store.ErrGhRepoNotFound) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
		return
	}
	if n, _ := h.st.CountGhRepos(); n >= store.MaxGhRepos {
		c.JSON(http.StatusBadRequest, gin.H{"error": "最多监控 16 个仓库"})
		return
	}
	id, err := h.st.CreateGhRepo(&store.GhRepo{Owner: owner, Repo: repo, DisplayName: name, Enabled: true})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	fresh, _ := h.st.GetGhRepo(id)
	if errMsg := h.mg.RefreshNow(id); errMsg != nil {
		// 首刷失败不算添加失败：仓库已入库，错误展示在卡片上
		c.JSON(http.StatusOK, gin.H{"code": 0, "warn": errMsg.Error(), "repo": h.repoView(fresh, nil)})
		return
	}
	rels, _ := h.st.ListGhReleases(id)
	c.JSON(http.StatusOK, gin.H{"code": 0, "repo": h.repoView(fresh, rels)})
}

// update 启停仓库（快照保留；重新启用立即补刷一轮，不等轮询）。
func (h *GhHandler) update(c *gin.Context) {
	id := ghID(c)
	if id == nil {
		return
	}
	if _, err := h.st.GetGhRepo(*id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控仓库不存在"})
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := h.st.UpdateGhRepoEnabled(*id, *req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存失败"})
		return
	}
	if *req.Enabled {
		go func() { _ = h.mg.RefreshNow(*id) }()
	}
	fresh, _ := h.st.GetGhRepo(*id)
	c.JSON(http.StatusOK, gin.H{"code": 0, "repo": h.repoView(fresh, nil)})
}

// remove 删除监控仓库及快照。
func (h *GhHandler) remove(c *gin.Context) {
	id := ghID(c)
	if id == nil {
		return
	}
	if _, err := h.st.GetGhRepo(*id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控仓库不存在"})
		return
	}
	if err := h.st.DeleteGhRepo(*id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
		return
	}
	h.mg.Drop(*id)
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// refresh 手动刷新单仓（同步，结果即时入库返回）。
func (h *GhHandler) refresh(c *gin.Context) {
	id := ghID(c)
	if id == nil {
		return
	}
	if _, err := h.st.GetGhRepo(*id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "监控仓库不存在"})
		return
	}
	if err := h.mg.RefreshNow(*id); err != nil {
		fresh, _ := h.st.GetGhRepo(*id)
		c.JSON(http.StatusOK, gin.H{"code": 0, "warn": err.Error(), "repo": h.repoView(fresh, nil)})
		return
	}
	fresh, _ := h.st.GetGhRepo(*id)
	rels, _ := h.st.ListGhReleases(*id)
	c.JSON(http.StatusOK, gin.H{"code": 0, "repo": h.repoView(fresh, rels)})
}

// refreshAll 全部启用仓库立即重刷（后台异步，前端靠列表刷新看到结果）。
func (h *GhHandler) refreshAll(c *gin.Context) {
	h.mg.KickAll()
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// seen 单仓记账「已看过」（489）：GhView 卡片上查看更多弹层/点开最新 Release/
// 点角标任一即调用，清该卡的 new 角标与主页统计卡的「有更新」提醒。
func (h *GhHandler) seen(c *gin.Context) {
	id := ghID(c)
	if id == nil {
		return
	}
	if err := h.st.MarkGhRepoSeen(*id); err != nil {
		if errors.Is(err, store.ErrGhRepoNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "监控仓库不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "记账失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0})
}

// test 未入库地址测试（添加弹窗「先测后存」用）：解析 + 实拉 Releases，不落库。
func (h *GhHandler) test(c *gin.Context) {
	var req struct {
		Repo string `json:"repo"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	owner, repo, err := ghwatch.ParseRepoInput(req.Repo)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "result": gin.H{"ok": false, "error": err.Error()}})
		return
	}
	rels, err := h.mg.FetchReleases(owner, repo)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 0, "result": gin.H{"ok": false, "error": err.Error()}})
		return
	}
	if rels == nil {
		rels = []store.GhRelease{}
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "result": gin.H{"ok": true, "owner": owner, "repo": repo, "releases": rels}})
}
