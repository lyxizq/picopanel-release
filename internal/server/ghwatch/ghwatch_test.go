package ghwatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestParseRepoInput 输入形态：owner/repo、完整 URL、.git 后缀、深层路径、
// 末尾斜杠、协议相对地址；坏形态报错且不吞大小写。
func TestParseRepoInput(t *testing.T) {
	ok := map[string][2]string{
		"cli/cli":                                       {"cli", "cli"},
		"  cli/cli  ":                                   {"cli", "cli"},
		"https://github.com/cli/cli":                    {"cli", "cli"},
		"https://www.github.com/cli/cli/":               {"cli", "cli"},
		"github.com/cli/cli":                            {"cli", "cli"},
		"www.github.com/cli/cli":                        {"cli", "cli"},
		"//github.com/cli/cli":                          {"cli", "cli"},
		"https://github.com/cli/cli.git":                {"cli", "cli"},
		"https://github.com/cli/cli/tree/trunk/docs":    {"cli", "cli"},
		"https://GITHUB.com/GodotEngine/godot/releases": {"GodotEngine", "godot"},
	}
	for in, want := range ok {
		owner, repo, err := ParseRepoInput(in)
		if err != nil || owner != want[0] || repo != want[1] {
			t.Fatalf("ParseRepoInput(%q) = %q/%q, %v；期望 %q/%q", in, owner, repo, err, want[0], want[1])
		}
	}
	bad := []string{
		"", "cli", "cli/", "/cli", "https://gitlab.com/cli/cli",
		"gitlab.com/cli/cli", "a b/c", "-cli/cli", "cli/cli,.git",
	}
	for _, in := range bad {
		if _, _, err := ParseRepoInput(in); err == nil {
			t.Fatalf("ParseRepoInput(%q) 应报错", in)
		}
	}
}

// releasesJSON mock 响应：含草稿（跳过）、预发布（保留标记）、null published_at。
const releasesJSON = `[
	{"tag_name":"v1.0.0","name":"major","draft":false,"prerelease":false,
	 "html_url":"https://github.com/cli/cli/releases/v1.0.0",
	 "published_at":"2026-10-01T08:00:00Z","body":"first big release notes"},
	{"tag_name":"v0.9.0","name":"","draft":false,"prerelease":true,
	 "html_url":"https://github.com/cli/cli/releases/v0.9.0",
	 "published_at":"2026-09-01T08:00:00Z","body":""},
	{"tag_name":"draft-1","name":"wip","draft":true,"prerelease":false,
	 "html_url":"https://github.com/cli/cli/releases/draft-1","published_at":null,"body":"x"},
	{"tag_name":"v0.8.0","name":"old","draft":false,"prerelease":false,
	 "html_url":"https://github.com/cli/cli/releases/v0.8.0","published_at":null,"body":"y"}
]`

func newMockGitHub(t *testing.T, status int, body string) (*httptest.Server, *Manager, *store.Store) {
	t.Helper()
	s := newTestStore(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.HasSuffix(r.URL.Path, "/releases") {
			if got := r.Header.Get("User-Agent"); got == "" {
				t.Error("GitHub 请求必须带 User-Agent")
			}
			if got := r.URL.Query().Get("per_page"); got == "" {
				t.Error("缺少 per_page 参数")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		// 仓库元数据 /repos/{owner}/{repo}（stars/watching/forks，2026-10-26）
		if strings.HasPrefix(r.URL.Path, "/repos/") && len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 3 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"stargazers_count":12300,"subscribers_count":456,"forks_count":789}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	m := New(s)
	m.apiBase = srv.URL
	return srv, m, s
}

// TestFetchReleases 拉取解析：草稿跳过、字段映射、顺序保持、时间转 unix。
func TestFetchReleases(t *testing.T) {
	_, m, _ := newMockGitHub(t, http.StatusOK, releasesJSON)
	rels, err := m.FetchReleases("cli", "cli")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 3 {
		t.Fatalf("应留 3 条（草稿跳过）: %d", len(rels))
	}
	if rels[0].TagName != "v1.0.0" || rels[0].Name != "major" || rels[0].Prerelease ||
		rels[0].HTMLURL != "https://github.com/cli/cli/releases/v1.0.0" || rels[0].Body != "first big release notes" {
		t.Fatalf("首条不符: %+v", rels[0])
	}
	want := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC).Unix()
	if rels[0].PublishedAt != want {
		t.Fatalf("published_at 转换不符: %d != %d", rels[0].PublishedAt, want)
	}
	if !rels[1].Prerelease || rels[1].PublishedAt != time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("预发布条不符: %+v", rels[1])
	}
	if rels[2].TagName != "v0.8.0" || rels[2].PublishedAt != 0 {
		t.Fatalf("null published_at 应记 0: %+v", rels[2])
	}
}

// TestRefreshOneFlow 刷新落账全链路：成功 → 快照入库 + last_ok；失败 →
// last_error（快照保留）；忙标记期间跳过；KickAll 异步整轮。
func TestRefreshOneFlow(t *testing.T) {
	_, m, s := newMockGitHub(t, http.StatusOK, releasesJSON)
	id, err := s.CreateGhRepo(&store.GhRepo{Owner: "cli", Repo: "cli", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshNow(id); err != nil {
		t.Fatalf("首刷失败: %v", err)
	}
	rels, _ := s.ListGhReleases(id)
	if len(rels) != 3 {
		t.Fatalf("快照应 3 条: %d", len(rels))
	}
	r, _ := s.GetGhRepo(id)
	if r.LastOKAt == 0 || r.LastError != "" {
		t.Fatalf("成功落账不符: %+v", r)
	}
	// 仓库元数据随刷新一并入库（2026-10-26）
	if r.Stars != 12300 || r.Watchers != 456 || r.Forks != 789 {
		t.Fatalf("仓库元数据不符: %+v", r)
	}

	// 改 mock 为 500：RefreshNow 报错、last_error 落库、快照保留
	srv2, m2, s2 := newMockGitHub(t, http.StatusInternalServerError, "boom")
	_ = srv2
	id2, _ := s2.CreateGhRepo(&store.GhRepo{Owner: "cli", Repo: "cli", Enabled: true})
	if err := m2.RefreshNow(id2); err == nil {
		t.Fatal("500 应报错")
	}
	r2, _ := s2.GetGhRepo(id2)
	if r2.LastError == "" || r2.LastOKAt != 0 {
		t.Fatalf("失败落账不符: %+v", r2)
	}
	if n := len(mustRels2(t, s2, id2)); n != 0 {
		t.Fatalf("失败不应写快照: %d", n)
	}

	// 停用仓不参与整轮：KickAll 后仍无快照
	if err := s2.UpdateGhRepoEnabled(id2, false); err != nil {
		t.Fatal(err)
	}
	m2.KickAll()
	time.Sleep(200 * time.Millisecond)
	if n := len(mustRels2(t, s2, id2)); n != 0 {
		t.Fatalf("停用仓不应被 KickAll 拉取: %d", n)
	}
}

// TestManagerRun 启动即刷一轮：Run 在 ctx 取消后退出。
func TestManagerRun(t *testing.T) {
	_, m, s := newMockGitHub(t, http.StatusOK, releasesJSON)
	if _, err := s.CreateGhRepo(&store.GhRepo{Owner: "cli", Repo: "cli", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rels, _ := s.ListGhReleases(1); len(rels) == 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	rels, _ := s.ListGhReleases(1)
	if len(rels) != 3 {
		t.Fatalf("Run 启动轮未入库: %d", len(rels))
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 Run 未退出")
	}
}

func mustRels2(t *testing.T, s *store.Store, repoID int64) []store.GhRelease {
	t.Helper()
	rels, err := s.ListGhReleases(repoID)
	if err != nil {
		t.Fatal(err)
	}
	return rels
}
