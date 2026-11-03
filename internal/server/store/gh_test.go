package store

import (
	"errors"
	"path/filepath"
	"testing"
)

// GitHub 发布监控存储（466）：slug 查重/唯一约束、快照整组重建（最多保留条数
// 与旧组清理）、启停保留快照、删除连带、轮询结果落账。
func TestGhReposAndReleases(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	id, err := s.CreateGhRepo(&GhRepo{Owner: "cli", Repo: "cli", DisplayName: "GitHub CLI", Enabled: true})
	if err != nil || id == 0 {
		t.Fatalf("建仓失败: %v", err)
	}
	// 撞 slug 唯一约束
	if _, err := s.CreateGhRepo(&GhRepo{Owner: "cli", Repo: "cli", Enabled: true}); !errors.Is(err, ErrGhRepoExists) {
		t.Fatalf("重复 slug 应回 ErrGhRepoExists: %v", err)
	}
	if _, err := s.CreateGhRepo(&GhRepo{Owner: "gohugoio", Repo: "hugo", Enabled: false}); err != nil {
		t.Fatalf("建仓 2 失败: %v", err)
	}
	bySlug, err := s.GetGhRepoBySlug("cli", "cli")
	if err != nil || bySlug.ID != id || bySlug.DisplayName != "GitHub CLI" {
		t.Fatalf("slug 查询不符: %+v %v", bySlug, err)
	}
	if all, _ := s.ListGhRepos(); len(all) != 2 || all[1].DisplayName != "" {
		t.Fatalf("清单/默认空显示名不符: %+v", all)
	}
	if _, err := s.GetGhRepoBySlug("cli", "nope"); !errors.Is(err, ErrGhRepoNotFound) {
		t.Fatalf("未存在 slug 应 NotFound: %v", err)
	}

	// 快照整组重建：先写 3 条，再以 5 条新组覆盖 → 旧组清空、新组在位
	seed := []GhRelease{
		{TagName: "v0.9.0", PublishedAt: 1000},
		{TagName: "v0.8.0", Prerelease: true, PublishedAt: 900},
		{TagName: "v0.7.0", PublishedAt: 800},
	}
	if err := s.ReplaceGhReleases(id, seed); err != nil {
		t.Fatalf("首组写入失败: %v", err)
	}
	fresh := []GhRelease{
		{TagName: "v1.0.1", Name: "patch", PublishedAt: 2000, HTMLURL: "https://github.com/cli/cli/releases/v1.0.1", Body: "fix"},
		{TagName: "v1.0.0", Name: "major", PublishedAt: 1900},
		{TagName: "v0.9.1", Name: "beta", Prerelease: true, PublishedAt: 1800},
		{TagName: "v0.9.0", Name: "重名覆盖", PublishedAt: 1000},
		{TagName: "v0.8.5", Name: "old", PublishedAt: 950},
	}
	if err := s.ReplaceGhReleases(id, fresh); err != nil {
		t.Fatalf("整组重建失败: %v", err)
	}
	rels, err := s.ListGhReleases(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 5 {
		t.Fatalf("应留 5 条: %d", len(rels))
	}
	if rels[0].TagName != "v1.0.1" || rels[0].Body != "fix" || !rels[2].Prerelease || rels[3].Name != "重名覆盖" {
		t.Fatalf("新组内容/排序不符: %+v", rels)
	}
	// v0.7.0（旧组遗留）必须被清掉
	for _, r := range rels {
		if r.TagName == "v0.7.0" {
			t.Fatalf("旧组残留: %+v", rels)
		}
	}

	// 启停：停用后快照仍在；再删仓连带清快照
	if err := s.UpdateGhRepoEnabled(id, false); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	if r, _ := s.GetGhRepo(id); r.Enabled {
		t.Fatal("应已停用")
	}
	if n := len(mustRels(t, s, id)); n != 5 {
		t.Fatalf("停用不应清快照: %d", n)
	}
	if err := s.MarkGhRepoPoll(id, "GitHub API HTTP 403"); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetGhRepo(id); r.LastError == "" || r.LastOKAt != 0 {
		t.Fatalf("失败落账不符: %+v", r)
	}
	if err := s.MarkGhRepoPoll(id, ""); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetGhRepo(id); r.LastError != "" || r.LastOKAt == 0 {
		t.Fatalf("成功落账不符: %+v", r)
	}
	if err := s.DeleteGhRepo(id); err != nil {
		t.Fatalf("删仓失败: %v", err)
	}
	if n := len(mustRels(t, s, id)); n != 0 {
		t.Fatalf("删仓应连带清快照: %d", n)
	}
	if _, err := s.GetGhRepo(id); !errors.Is(err, ErrGhRepoNotFound) {
		t.Fatalf("删后应 NotFound: %v", err)
	}
}

// 轮询间隔（466 续）：缺省 4 小时、写入生效、脏数据回默认。
func TestGhPollSeconds(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	if got := s.GhPollSeconds(); got != GhPollDefault {
		t.Fatalf("缺省应 4h(%d): %d", GhPollDefault, got)
	}
	if err := s.SetGhPollSeconds(1800); err != nil {
		t.Fatal(err)
	}
	if got := s.GhPollSeconds(); got != 1800 {
		t.Fatalf("应读到 1800: %d", got)
	}
	for _, dirty := range []string{"9999", "-1", "abc"} {
		if err := s.SetSetting(settingGhPollSec, dirty); err != nil {
			t.Fatal(err)
		}
		if got := s.GhPollSeconds(); got != GhPollDefault {
			t.Fatalf("脏数据 %q 应回默认: %d", dirty, got)
		}
	}
}

func mustRels(t *testing.T, s *Store, repoID int64) []GhRelease {
	t.Helper()
	rels, err := s.ListGhReleases(repoID)
	if err != nil {
		t.Fatal(err)
	}
	return rels
}
