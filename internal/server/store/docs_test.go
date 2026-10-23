package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openDocTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "docs.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestDocGroupsCRUD 文档分组：建/查/改名/重名/删除。
func TestDocGroupsCRUD(t *testing.T) {
	s := openDocTestStore(t)
	g, err := s.CreateDocGroup("运维笔记")
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "运维笔记" {
		t.Fatalf("名称: %q", g.Name)
	}
	if _, err := s.CreateDocGroup("运维笔记"); !errors.Is(err, ErrDocGroupNameExists) {
		t.Fatalf("重名应报 ErrDocGroupNameExists: %v", err)
	}
	if err := s.RenameDocGroup(g.ID, "部署手册"); err != nil {
		t.Fatal(err)
	}
	groups, err := s.ListDocGroups()
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Name != "部署手册" {
		t.Fatalf("改名后应为部署手册: %+v", groups)
	}
	if err := s.RenameDocGroup(999, "不存在"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("不存在的分组应报 ErrNoRows: %v", err)
	}
	if n, err := s.DeleteDocGroup(g.ID); err != nil || n != 0 {
		t.Fatalf("删除空组: %d %v", n, err)
	}
	if _, err := s.DeleteDocGroup(g.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("重复删除应报 ErrNoRows: %v", err)
	}
}

// TestUpdateDocCAS 乐观锁：base_updated_at 与当前一致才允许保存，
// 不一致返回 ErrDocConflict；负数基准跳过校验。
func TestUpdateDocCAS(t *testing.T) {
	s := openDocTestStore(t)
	g, err := s.CreateDocGroup("g")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDoc(g.ID, "t", "md")
	if err != nil {
		t.Fatal(err)
	}

	// 基准一致 → 保存成功
	c1 := "win-A"
	newAt, err := s.UpdateDoc(d.ID, nil, &c1, d.UpdatedAt)
	if err != nil {
		t.Fatalf("基准一致应保存成功: %v", err)
	}
	// 模拟另一窗口先保存：推快 updated_at（+10s 避免同秒相等的不确定性）
	if _, err := s.DB().Exec(`UPDATE docs SET updated_at = updated_at + 10 WHERE id = ?`, d.ID); err != nil {
		t.Fatal(err)
	}
	// 旧基准已过期 → 冲突
	c2 := "win-B"
	if _, err := s.UpdateDoc(d.ID, nil, &c2, newAt); err != ErrDocConflict {
		t.Fatalf("过期基准应返回 ErrDocConflict, got %v", err)
	}
	// 负数基准 = 跳过校验（强制覆盖）
	c3 := "win-B-force"
	if _, err := s.UpdateDoc(d.ID, nil, &c3, -1); err != nil {
		t.Fatalf("负数基准=跳过校验: %v", err)
	}
	// 不存在的文档应报错
	if _, err := s.UpdateDoc(99999, nil, &c3, -1); err == nil {
		t.Fatal("不存在的文档应报错")
	}
}

// TestDocsCRUD 文档：建/取/改标题/改内容/删除 + 元数据列表不含 content。
func TestDocsCRUD(t *testing.T) {
	s := openDocTestStore(t)
	g, err := s.CreateDocGroup("g1")
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDoc(g.ID, "Nginx 配置", "md")
	if err != nil {
		t.Fatal(err)
	}
	if d.Format != "md" || d.GroupID != g.ID || d.Content != "" {
		t.Fatalf("初始字段: %+v", d)
	}

	title := "Nginx 反代"
	if _, err := s.UpdateDoc(d.ID, &title, nil, -1); err != nil {
		t.Fatal(err)
	}
	content := "# 标题\n正文"
	if _, err := s.UpdateDoc(d.ID, nil, &content, -1); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDoc(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != title || got.Content != content {
		t.Fatalf("保存后回读: %+v", got)
	}
	if got.UpdatedAt < got.CreatedAt {
		t.Fatalf("updated_at 应推进: %d < %d", got.UpdatedAt, got.CreatedAt)
	}

	_, docs, err := s.ListDocTree()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Content != "" || docs[0].Title != title {
		t.Fatalf("列表应为元数据（无 content）: %+v", docs)
	}

	if err := s.SoftDeleteDoc(d.ID); err != nil {
		t.Fatal(err)
	}
	// 软删后：树里消失、回收站里出现、GetDoc 带 deleted_at 标记
	_, docs, err = s.ListDocTree()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("软删后树应为空: %+v", docs)
	}
	trash, err := s.ListTrashDocs()
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 1 || trash[0].ID != d.ID || trash[0].DeletedAt == 0 {
		t.Fatalf("回收站应有该文档: %+v", trash)
	}
	after, err := s.GetDoc(d.ID)
	if err != nil || after == nil || after.DeletedAt == 0 {
		t.Fatalf("GetDoc 应带软删标记: %+v %v", after, err)
	}
}

// TestDeleteDocGroupCascade 删分组：组内文档移入回收站（软删），其他组不受影响。
func TestDeleteDocGroupCascade(t *testing.T) {
	s := openDocTestStore(t)
	g1, _ := s.CreateDocGroup("g1")
	g2, _ := s.CreateDocGroup("g2")
	for i := 0; i < 3; i++ {
		if _, err := s.CreateDoc(g1.ID, "d", "richtext"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateDoc(g2.ID, "keep", "md"); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteDocGroup(g1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("应级联软删 3 篇: %d", n)
	}
	_, docs, err := s.ListDocTree()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Title != "keep" {
		t.Fatalf("树里只剩 g2 的文档: %+v", docs)
	}
	trash, err := s.ListTrashDocs()
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 3 {
		t.Fatalf("回收站应有 3 篇: %+v", trash)
	}
	// 恢复一篇：原组已删，回落未分组
	r, err := s.RestoreDoc(trash[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.GroupID != 0 || r.DeletedAt != 0 {
		t.Fatalf("恢复后应未分组且标记清零: %+v", r)
	}
}

// TestDocTrash 回收站：恢复不存在/彻底删除连带版本/重复软删报错。
func TestDocTrash(t *testing.T) {
	s := openDocTestStore(t)
	d, err := s.CreateDoc(0, "t", "md")
	if err != nil {
		t.Fatal(err)
	}
	// 未软删的文档不能直接彻底删（必须先进回收站）
	if err := s.HardDeleteDoc(d.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("未软删时彻底删应报 ErrNoRows: %v", err)
	}
	if err := s.SoftDeleteDoc(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDeleteDoc(d.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("重复软删应报 ErrNoRows: %v", err)
	}
	if _, err := s.RestoreDoc(d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreDoc(d.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("重复恢复应报 ErrNoRows: %v", err)
	}
	// 软删 → 彻底删：docs 与 doc_versions 一并消失
	if err := s.SoftDeleteDoc(d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.HardDeleteDoc(d.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetDoc(d.ID); err != nil || got != nil {
		t.Fatalf("彻底删后应查无: %+v %v", got, err)
	}
}

// TestEmptyTrash 清空回收站：软删文档连同版本全删、正常文档与版本不受影响、空回收站返回 0。
func TestEmptyTrash(t *testing.T) {
	s := openDocTestStore(t)
	dead, err := s.CreateDoc(0, "待清空", "md")
	if err != nil {
		t.Fatal(err)
	}
	// 给待清空文档留一版历史（写内容产生快照前先把旧空内容跳过：直接改库造版本最快）
	if _, err := s.DB().Exec(`INSERT INTO doc_versions (doc_id, title, content, created_at)
		VALUES (?, '旧', 'old', 1)`, dead.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDeleteDoc(dead.ID); err != nil {
		t.Fatal(err)
	}
	alive, err := s.CreateDoc(0, "正常", "md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO doc_versions (doc_id, title, content, created_at)
		VALUES (?, '正常旧', 'old-alive', 1)`, alive.ID); err != nil {
		t.Fatal(err)
	}

	n, err := s.EmptyTrashDocs()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("清空应删 1 篇, got %d", n)
	}
	if got, _ := s.ListTrashDocs(); len(got) != 0 {
		t.Fatalf("回收站应已空: %+v", got)
	}
	if got, err := s.GetDoc(dead.ID); err != nil || got != nil {
		t.Fatalf("清空后软删文档应查无: %+v %v", got, err)
	}
	var deadVer int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM doc_versions WHERE doc_id = ?`, dead.ID).Scan(&deadVer); err != nil {
		t.Fatal(err)
	}
	if deadVer != 0 {
		t.Fatalf("软删文档的历史版本应连带删除, 残留 %d", deadVer)
	}
	if got, err := s.GetDoc(alive.ID); err != nil || got == nil {
		t.Fatalf("正常文档不应受影响: %+v %v", got, err)
	}
	var aliveVer int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM doc_versions WHERE doc_id = ?`, alive.ID).Scan(&aliveVer); err != nil {
		t.Fatal(err)
	}
	if aliveVer != 1 {
		t.Fatalf("正常文档的历史版本不应受影响, got %d", aliveVer)
	}
	// 空回收站再清：0 篇，不报错
	if n, err := s.EmptyTrashDocs(); err != nil || n != 0 {
		t.Fatalf("空回收站清空应返回 0: %d %v", n, err)
	}
}

// TestDocVersions 版本快照：内容变化留旧版、600s 内节流不留、修剪保留 10 版。
func TestDocVersions(t *testing.T) {
	s := openDocTestStore(t)
	d, err := s.CreateDoc(0, "t", "md")
	if err != nil {
		t.Fatal(err)
	}
	v1 := "v1"
	if _, err := s.UpdateDoc(d.ID, nil, &v1, -1); err != nil {
		t.Fatal(err)
	}
	v2 := "v2"
	if _, err := s.UpdateDoc(d.ID, nil, &v2, -1); err != nil {
		t.Fatal(err)
	}
	v3 := "v3"
	if _, err := s.UpdateDoc(d.ID, nil, &v3, -1); err != nil {
		t.Fatal(err)
	}
	versions, err := s.ListDocVersions(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	// v1→v2 留了 v1 快照；v2→v3 距上版本不足 600s 被节流
	if len(versions) != 1 || versions[0].Content != "" || versions[0].Title != "t" {
		t.Fatalf("节流后应只留 1 版（v1 内容）: %+v", versions)
	}
	got, err := s.GetDocVersion(d.ID, versions[0].ID)
	if err != nil || got == nil || got.Content != v1 {
		t.Fatalf("版本详情应为 v1 内容: %+v %v", got, err)
	}

	// 伪造 25 条时间分散的旧版本（先把已有 v1 快照时间拉老，否则它是 MAX(created_at)，
	// v4 的保存会因「距上一版本不足 600s」被节流），再保存触发快照 + 修剪到 10
	now := time.Now().Unix()
	if _, err := s.db.Exec(`UPDATE doc_versions SET created_at = ? WHERE doc_id = ?`, now-26*3600, d.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if _, err := s.db.Exec(`INSERT INTO doc_versions (doc_id, title, content, created_at) VALUES (?,?,?,?)`,
			d.ID, "t", "old", now-int64(3600*(25-i))); err != nil {
			t.Fatal(err)
		}
	}
	v4 := "v4"
	if _, err := s.UpdateDoc(d.ID, nil, &v4, -1); err != nil {
		t.Fatal(err)
	}
	versions, err = s.ListDocVersions(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != docVersionKeep {
		t.Fatalf("修剪后应保留 10 版: %d", len(versions))
	}
	// 最近一版应是本次新留的快照（v3 内容；列表不带 content，走详情取）
	latest, err := s.GetDocVersion(d.ID, versions[0].ID)
	if err != nil || latest == nil || latest.Content != v3 {
		t.Fatalf("最新快照应为 v3: %+v %v", latest, err)
	}
	// 只改标题不留快照
	title := "新标题"
	if _, err := s.UpdateDoc(d.ID, &title, nil, -1); err != nil {
		t.Fatal(err)
	}
	if versions, _ = s.ListDocVersions(d.ID); len(versions) != docVersionKeep {
		t.Fatalf("改标题不应留版本: %d", len(versions))
	}
}
