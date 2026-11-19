package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestWebCheckCRUD 建查改删 + 同址查重 + 拖拽排序。
func TestWebCheckCRUD(t *testing.T) {
	s := openTestStore(t)

	id1, err := s.CreateWebCheck(&WebCheck{Name: "主站", URL: "http://a.com", Rules: `[["ok","running"]]`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.CreateWebCheck(&WebCheck{URL: "http://b.com", Rules: `[["hi"]]`, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	// 同址查重
	if _, err := s.CreateWebCheck(&WebCheck{URL: "http://a.com", Rules: `[["x"]]`, Enabled: true}); err != ErrWebCheckExists {
		t.Fatalf("同址应报 ErrWebCheckExists: %v", err)
	}
	if _, err := s.GetWebCheckByUrl("http://a.com"); err != nil {
		t.Fatalf("按址查应有: %v", err)
	}
	if _, err := s.GetWebCheckByUrl("http://nope.com"); err != ErrWebCheckNotFound {
		t.Fatalf("按址查无应 ErrWebCheckNotFound: %v", err)
	}

	// 清单（按排序）与关键字拆分
	list, err := s.ListWebChecks()
	if err != nil || len(list) != 2 {
		t.Fatalf("清单应有 2 行: %v %d", err, len(list))
	}
	if rules := list[0].RuleList(); len(rules) != 1 || len(rules[0]) != 2 || rules[0][0] != "ok" || rules[0][1] != "running" {
		t.Fatalf("规则组解析不对: %v", rules)
	}
	if !list[0].Enabled || list[1].Enabled {
		t.Fatalf("启停位不对: %+v %+v", list[0], list[1])
	}

	// 编辑：只改名不重置状态；换址重置
	if _, err := s.RecordWebCheck(id1, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateWebCheck(&WebCheck{ID: id1, Name: "改名", URL: "http://a.com", Rules: `[["ok"]]`, Enabled: true}, false); err != nil {
		t.Fatal(err)
	}
	w, _ := s.GetWebCheck(id1)
	if w.Name != "改名" || w.Up != 1 || w.LastOKAt == 0 {
		t.Fatalf("改名不应重置状态: %+v", w)
	}
	if err := s.UpdateWebCheck(&WebCheck{ID: id1, Name: "改名", URL: "http://c.com", Rules: `[["ok"]]`, Enabled: true}, true); err != nil {
		t.Fatal(err)
	}
	w, _ = s.GetWebCheck(id1)
	if w.URL != "http://c.com" || w.Up != -1 || w.LastError != "" || w.LastOKAt != 0 {
		t.Fatalf("换址应重置检测态: %+v", w)
	}

	// 拖拽排序：b 提到第一
	if err := s.ReorderWebChecks([]int64{id2, id1}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListWebChecks()
	if list[0].ID != id2 || list[1].ID != id1 {
		t.Fatalf("排序不对: %d,%d", list[0].ID, list[1].ID)
	}

	// 删除
	if err := s.DeleteWebCheck(id2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetWebCheck(id2); err != ErrWebCheckNotFound {
		t.Fatalf("删除后应不存在: %v", err)
	}
	if err := s.DeleteWebCheck(id2); err != ErrWebCheckNotFound {
		t.Fatalf("重复删应报不存在: %v", err)
	}
}

// TestRecordWebCheckFlip 检测记账：since 只在翻转时推进、last_ok_at 只在成功时
// 推进、-1 未检测首轮不算翻转、checked_at 每轮推进。
func TestRecordWebCheckFlip(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateWebCheck(&WebCheck{URL: "http://a.com", Rules: `[["ok"]]`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// 首轮失败：-1 → 0，不算翻转
	flipped, err := s.RecordWebCheck(id, false, "HTTP 503")
	if err != nil || flipped {
		t.Fatalf("首轮不算翻转: flipped=%v err=%v", flipped, err)
	}
	w, _ := s.GetWebCheck(id)
	since0 := w.Since
	if w.Up != 0 || since0 == 0 || w.LastError != "HTTP 503" || w.LastOKAt != 0 {
		t.Fatalf("首轮失败状态不对: %+v", w)
	}

	// 原态复检：since 不动、checked_at 推进
	before := w.CheckedAt
	flipped, err = s.RecordWebCheck(id, false, "HTTP 503")
	if err != nil || flipped {
		t.Fatalf("原态复检不应翻转: %v %v", flipped, err)
	}
	w, _ = s.GetWebCheck(id)
	if w.Since != since0 || w.CheckedAt < before {
		t.Fatalf("原态复检 since 不应动 checked_at 不应回退: %+v", w)
	}

	// 失败→通过：翻转，since 重置为本轮时刻、last_ok_at 落（同秒写入下取 >=）
	flipped, err = s.RecordWebCheck(id, true, "")
	if err != nil || !flipped {
		t.Fatalf("失败→通过应翻转: %v %v", flipped, err)
	}
	w, _ = s.GetWebCheck(id)
	if w.Up != 1 || w.Since < since0 || w.Since != w.CheckedAt || w.LastOKAt == 0 || w.LastError != "" {
		t.Fatalf("恢复后状态不对: %+v", w)
	}

	// 通过→失败：翻转
	if flipped, _ := s.RecordWebCheck(id, false, "缺少关键字 「ok」"); !flipped {
		t.Fatal("通过→失败应翻转")
	}

	// 行不存在报错（刚被删）
	if err := s.DeleteWebCheck(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordWebCheck(id, true, ""); err == nil {
		t.Fatal("行不存在应报错")
	}
}

// TestWebCheckTableReopen 新库建表与重开幂等（web_checks 走建表 DDL，重开不重建不报错）。
func TestWebCheckTableReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "it.db")
	key := filepath.Join(t.TempDir(), "key")
	s, err := OpenWithKey(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWebCheck(&WebCheck{URL: "http://a.com", Rules: `[["ok"]]`, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := OpenWithKey(path, key)
	if err != nil {
		t.Fatalf("重开应幂等: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	list, err := s2.ListWebChecks()
	if err != nil || len(list) != 1 {
		t.Fatalf("重开后数据应在: %v %d", err, len(list))
	}
}

// TestWebCheckLegacyMigration 存量单规则迁移回归（2026-11-03 多规则改版）：
// 首版 keywords 列（换行分隔单组关键字）→ 换名 rules + 内容转 JSON 规则组；
// 新结构（无 keywords 列）跳过；重复跑幂等。
func TestWebCheckLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "it.db")
	key := filepath.Join(t.TempDir(), "key")
	// 先按当前结构初始化库，再把 rules 列改回首版形态并塞一行旧格式数据（模拟升级前库）
	s, err := OpenWithKey(path, key)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE web_checks RENAME COLUMN rules TO keywords`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO web_checks (name, url, keywords, enabled, sort, created_at, updated_at)
		VALUES ('旧格式', 'http://old.com', 'ok' || char(10) || 'running', 1, 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// 重开触发迁移：列换名 + 旧格式行转 JSON
	s2, err := OpenWithKey(path, key)
	if err != nil {
		t.Fatalf("迁移后重开应成功: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	if err := MigrateWebCheckRules(s2.db); err != nil { // 再跑一遍验幂等
		t.Fatalf("重复迁移应幂等: %v", err)
	}
	list, err := s2.ListWebChecks()
	if err != nil || len(list) != 1 {
		t.Fatalf("迁移后应留 1 行: %v %d", err, len(list))
	}
	w := list[0]
	if w.URL != "http://old.com" {
		t.Fatalf("迁移后 URL 不对: %+v", w)
	}
	rules := w.RuleList()
	if len(rules) != 1 || len(rules[0]) != 2 || rules[0][0] != "ok" || rules[0][1] != "running" {
		t.Fatalf("旧格式应转成单条规则 JSON: %q → %v", w.Rules, rules)
	}
}
