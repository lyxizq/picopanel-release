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
	g, err := s.CreateDocGroup("运维笔记", 0)
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "运维笔记" {
		t.Fatalf("名称: %q", g.Name)
	}
	if _, err := s.CreateDocGroup("运维笔记", 0); !errors.Is(err, ErrDocGroupNameExists) {
		t.Fatalf("重名应报 ErrDocGroupNameExists: %v", err)
	}
	if err := s.RenameDocGroup(g.ID, 0, "部署手册"); err != nil {
		t.Fatal(err)
	}
	groups, err := s.ListDocGroups(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Name != "部署手册" {
		t.Fatalf("改名后应为部署手册: %+v", groups)
	}
	if err := s.RenameDocGroup(999, 0, "不存在"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("不存在的分组应报 ErrNoRows: %v", err)
	}
	if n, err := s.DeleteDocGroup(g.ID, 0); err != nil || n != 0 {
		t.Fatalf("删除空组: %d %v", n, err)
	}
	if _, err := s.DeleteDocGroup(g.ID, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("重复删除应报 ErrNoRows: %v", err)
	}
}

// TestUpdateDocCAS 乐观锁：base_updated_at 与当前一致才允许保存，
// 不一致返回 ErrDocConflict；负数基准跳过校验。
func TestUpdateDocCAS(t *testing.T) {
	s := openDocTestStore(t)
	g, err := s.CreateDocGroup("g", 0)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDoc(g.ID, 0, "t", "md")
	if err != nil {
		t.Fatal(err)
	}

	// 基准一致 → 保存成功
	c1 := "win-A"
	newAt, err := s.UpdateDoc(d.ID, 0, nil, &c1, d.UpdatedAt)
	if err != nil {
		t.Fatalf("基准一致应保存成功: %v", err)
	}
	// 模拟另一窗口先保存：推快 updated_at（+10s 避免同秒相等的不确定性）
	if _, err := s.DB().Exec(`UPDATE docs SET updated_at = updated_at + 10 WHERE id = ?`, d.ID); err != nil {
		t.Fatal(err)
	}
	// 旧基准已过期 → 冲突
	c2 := "win-B"
	if _, err := s.UpdateDoc(d.ID, 0, nil, &c2, newAt); err != ErrDocConflict {
		t.Fatalf("过期基准应返回 ErrDocConflict, got %v", err)
	}
	// 负数基准 = 跳过校验（强制覆盖）
	c3 := "win-B-force"
	if _, err := s.UpdateDoc(d.ID, 0, nil, &c3, -1); err != nil {
		t.Fatalf("负数基准=跳过校验: %v", err)
	}
	// 不存在的文档应报错
	if _, err := s.UpdateDoc(99999, 0, nil, &c3, -1); err == nil {
		t.Fatal("不存在的文档应报错")
	}
}

// TestDocsCRUD 文档：建/取/改标题/改内容/删除 + 元数据列表不含 content。
func TestDocsCRUD(t *testing.T) {
	s := openDocTestStore(t)
	g, err := s.CreateDocGroup("g1", 0)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDoc(g.ID, 0, "Nginx 配置", "md")
	if err != nil {
		t.Fatal(err)
	}
	if d.Format != "md" || d.GroupID != g.ID || d.Content != "" {
		t.Fatalf("初始字段: %+v", d)
	}

	title := "Nginx 反代"
	if _, err := s.UpdateDoc(d.ID, 0, &title, nil, -1); err != nil {
		t.Fatal(err)
	}
	content := "# 标题\n正文"
	if _, err := s.UpdateDoc(d.ID, 0, nil, &content, -1); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDoc(d.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != title || got.Content != content {
		t.Fatalf("保存后回读: %+v", got)
	}
	if got.UpdatedAt < got.CreatedAt {
		t.Fatalf("updated_at 应推进: %d < %d", got.UpdatedAt, got.CreatedAt)
	}

	_, docs, err := s.ListDocTree(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Content != "" || docs[0].Title != title {
		t.Fatalf("列表应为元数据（无 content）: %+v", docs)
	}

	if err := s.SoftDeleteDoc(d.ID, 0); err != nil {
		t.Fatal(err)
	}
	// 软删后：树里消失、回收站里出现、GetDoc 带 deleted_at 标记
	_, docs, err = s.ListDocTree(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("软删后树应为空: %+v", docs)
	}
	trash, err := s.ListTrashDocs(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 1 || trash[0].ID != d.ID || trash[0].DeletedAt == 0 {
		t.Fatalf("回收站应有该文档: %+v", trash)
	}
	after, err := s.GetDoc(d.ID, 0)
	if err != nil || after == nil || after.DeletedAt == 0 {
		t.Fatalf("GetDoc 应带软删标记: %+v %v", after, err)
	}
}

// TestDeleteDocGroupCascade 删分组：组内文档移入回收站（软删），其他组不受影响。
func TestDeleteDocGroupCascade(t *testing.T) {
	s := openDocTestStore(t)
	g1, _ := s.CreateDocGroup("g1", 0)
	g2, _ := s.CreateDocGroup("g2", 0)
	for i := 0; i < 3; i++ {
		if _, err := s.CreateDoc(g1.ID, 0, "d", "richtext"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateDoc(g2.ID, 0, "keep", "md"); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteDocGroup(g1.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("应级联软删 3 篇: %d", n)
	}
	_, docs, err := s.ListDocTree(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Title != "keep" {
		t.Fatalf("树里只剩 g2 的文档: %+v", docs)
	}
	trash, err := s.ListTrashDocs(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 3 {
		t.Fatalf("回收站应有 3 篇: %+v", trash)
	}
	// 恢复一篇：原组已删，回落未分组
	r, err := s.RestoreDoc(trash[0].ID, 0)
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
	d, err := s.CreateDoc(0, 0, "t", "md")
	if err != nil {
		t.Fatal(err)
	}
	// 未软删的文档不能直接彻底删（必须先进回收站）
	if err := s.HardDeleteDoc(d.ID, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("未软删时彻底删应报 ErrNoRows: %v", err)
	}
	if err := s.SoftDeleteDoc(d.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDeleteDoc(d.ID, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("重复软删应报 ErrNoRows: %v", err)
	}
	if _, err := s.RestoreDoc(d.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreDoc(d.ID, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("重复恢复应报 ErrNoRows: %v", err)
	}
	// 软删 → 彻底删：docs 与 doc_versions 一并消失
	if err := s.SoftDeleteDoc(d.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.HardDeleteDoc(d.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetDoc(d.ID, 0); err != nil || got != nil {
		t.Fatalf("彻底删后应查无: %+v %v", got, err)
	}
}

// TestEmptyTrash 清空回收站：软删文档连同版本全删、正常文档与版本不受影响、空回收站返回 0。
func TestEmptyTrash(t *testing.T) {
	s := openDocTestStore(t)
	dead, err := s.CreateDoc(0, 0, "待清空", "md")
	if err != nil {
		t.Fatal(err)
	}
	// 给待清空文档留一版历史（写内容产生快照前先把旧空内容跳过：直接改库造版本最快）
	if _, err := s.DB().Exec(`INSERT INTO doc_versions (doc_id, title, content, created_at)
		VALUES (?, '旧', 'old', 1)`, dead.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDeleteDoc(dead.ID, 0); err != nil {
		t.Fatal(err)
	}
	alive, err := s.CreateDoc(0, 0, "正常", "md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO doc_versions (doc_id, title, content, created_at)
		VALUES (?, '正常旧', 'old-alive', 1)`, alive.ID); err != nil {
		t.Fatal(err)
	}

	n, err := s.EmptyTrashDocs(0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("清空应删 1 篇, got %d", n)
	}
	if got, _ := s.ListTrashDocs(0); len(got) != 0 {
		t.Fatalf("回收站应已空: %+v", got)
	}
	if got, err := s.GetDoc(dead.ID, 0); err != nil || got != nil {
		t.Fatalf("清空后软删文档应查无: %+v %v", got, err)
	}
	var deadVer int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM doc_versions WHERE doc_id = ?`, dead.ID).Scan(&deadVer); err != nil {
		t.Fatal(err)
	}
	if deadVer != 0 {
		t.Fatalf("软删文档的历史版本应连带删除, 残留 %d", deadVer)
	}
	if got, err := s.GetDoc(alive.ID, 0); err != nil || got == nil {
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
	if n, err := s.EmptyTrashDocs(0); err != nil || n != 0 {
		t.Fatalf("空回收站清空应返回 0: %d %v", n, err)
	}
}

// TestDocVersions 版本快照：内容变化留旧版、600s 内节流不留、修剪保留 10 版。
func TestDocVersions(t *testing.T) {
	s := openDocTestStore(t)
	d, err := s.CreateDoc(0, 0, "t", "md")
	if err != nil {
		t.Fatal(err)
	}
	v1 := "v1"
	if _, err := s.UpdateDoc(d.ID, 0, nil, &v1, -1); err != nil {
		t.Fatal(err)
	}
	v2 := "v2"
	if _, err := s.UpdateDoc(d.ID, 0, nil, &v2, -1); err != nil {
		t.Fatal(err)
	}
	v3 := "v3"
	if _, err := s.UpdateDoc(d.ID, 0, nil, &v3, -1); err != nil {
		t.Fatal(err)
	}
	versions, err := s.ListDocVersions(d.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	// v1→v2 留了 v1 快照；v2→v3 距上版本不足 600s 被节流
	if len(versions) != 1 || versions[0].Content != "" || versions[0].Title != "t" {
		t.Fatalf("节流后应只留 1 版（v1 内容）: %+v", versions)
	}
	got, err := s.GetDocVersion(d.ID, versions[0].ID, 0)
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
	if _, err := s.UpdateDoc(d.ID, 0, nil, &v4, -1); err != nil {
		t.Fatal(err)
	}
	versions, err = s.ListDocVersions(d.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != docVersionKeep {
		t.Fatalf("修剪后应保留 10 版: %d", len(versions))
	}
	// 最近一版应是本次新留的快照（v3 内容；列表不带 content，走详情取）
	latest, err := s.GetDocVersion(d.ID, versions[0].ID, 0)
	if err != nil || latest == nil || latest.Content != v3 {
		t.Fatalf("最新快照应为 v3: %+v %v", latest, err)
	}
	// 只改标题不留快照
	title := "新标题"
	if _, err := s.UpdateDoc(d.ID, 0, &title, nil, -1); err != nil {
		t.Fatal(err)
	}
	if versions, _ = s.ListDocVersions(d.ID, 0); len(versions) != docVersionKeep {
		t.Fatalf("改标题不应留版本: %d", len(versions))
	}
}

// TestDocsIsolation 家庭成员数据隔离（2026-11-03）：文档/分组按 user_id 空间隔离，
// admin(0) 与成员互不可见；跨空间读写按「不存在」（ErrNoRows / 空列表 / nil）处理，
// 不泄露存在性；回收站、历史版本同样按空间过滤。
func TestDocsIsolation(t *testing.T) {
	s := openDocTestStore(t)
	const famUID = 7

	// 分组与文档各建各的，同名跨空间合法（UNIQUE(user_id,name)）
	ga, err := s.CreateDocGroup("资料", 0)
	if err != nil {
		t.Fatal(err)
	}
	gu, err := s.CreateDocGroup("资料", famUID)
	if err != nil {
		t.Fatalf("成员空间同名分组应允许: %v", err)
	}
	da, err := s.CreateDoc(ga.ID, 0, "管理员文档", "md")
	if err != nil {
		t.Fatal(err)
	}
	du, err := s.CreateDoc(gu.ID, famUID, "成员文档", "md")
	if err != nil {
		t.Fatal(err)
	}

	// 列表互不可见
	if _, docs, _ := s.ListDocTree(0); len(docs) != 1 || docs[0].ID != da.ID {
		t.Fatalf("admin 树不应含成员文档: %+v", docs)
	}
	if _, docs, _ := s.ListDocTree(famUID); len(docs) != 1 || docs[0].ID != du.ID {
		t.Fatalf("成员树不应含 admin 文档: %+v", docs)
	}
	// 详情互不可见（跨空间返回 nil，不泄露存在性）
	if d, _ := s.GetDoc(du.ID, 0); d != nil {
		t.Fatalf("admin 取成员文档应查无: %+v", d)
	}
	if d, _ := s.GetDoc(da.ID, famUID); d != nil {
		t.Fatalf("成员取 admin 文档应查无: %+v", d)
	}

	// 跨空间写：ErrNoRows
	title := "劫持"
	if _, err := s.UpdateDoc(du.ID, 0, &title, nil, -1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨空间保存应报 ErrNoRows: %v", err)
	}
	if err := s.MoveDoc(du.ID, ga.ID, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨空间移动应报 ErrNoRows: %v", err)
	}
	if err := s.RenameDocGroup(gu.ID, 0, "劫持"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨空间改组名应报 ErrNoRows: %v", err)
	}
	if err := s.ReorderDocs([]int64{da.ID}, famUID); err != nil {
		t.Fatal(err)
	}
	// 跨空间删：分组级联也只动自己空间
	if _, err := s.DeleteDocGroup(gu.ID, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨空间删组应报 ErrNoRows: %v", err)
	}
	if err := s.SoftDeleteDoc(da.ID, famUID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨空间软删应报 ErrNoRows: %v", err)
	}

	// 回收站与历史版本按空间过滤：成员软删自己的文档，admin 回收站应仍为空
	if err := s.SoftDeleteDoc(du.ID, famUID); err != nil {
		t.Fatal(err)
	}
	if trash, _ := s.ListTrashDocs(0); len(trash) != 0 {
		t.Fatalf("admin 回收站不应看到成员软删文档: %+v", trash)
	}
	if trash, _ := s.ListTrashDocs(famUID); len(trash) != 1 || trash[0].ID != du.ID {
		t.Fatalf("成员回收站应有自己的软删文档: %+v", trash)
	}
	// admin 彻底清空自己回收站不影响成员的（反之亦然）
	if n, err := s.EmptyTrashDocs(0); err != nil || n != 0 {
		t.Fatalf("清空 admin 回收站应 0 篇: %d %v", n, err)
	}
	if got, _ := s.GetDoc(du.ID, famUID); got == nil || got.DeletedAt == 0 {
		t.Fatalf("成员回收站文档不应被 admin 清空波及: %+v", got)
	}
	// 历史版本跨空间不可见（du 尚无版本，断言空列表即可）
	if vs, _ := s.ListDocVersions(du.ID, 0); len(vs) != 0 {
		t.Fatalf("admin 查成员文档版本应为空: %+v", vs)
	}
	if v, _ := s.GetDocVersion(da.ID, 999, famUID); v != nil {
		t.Fatalf("成员查 admin 版本应查无: %+v", v)
	}
	// 恢复/彻底删同样按空间
	if _, err := s.RestoreDoc(du.ID, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨空间恢复应报 ErrNoRows: %v", err)
	}
	if err := s.HardDeleteDoc(du.ID, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨空间彻底删应报 ErrNoRows: %v", err)
	}
	if err := s.HardDeleteDoc(du.ID, famUID); err != nil {
		t.Fatalf("本空间彻底删应成功: %v", err)
	}
}
