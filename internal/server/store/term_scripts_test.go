package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openScriptTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "scripts.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestTermScriptCRUD 每服务器专属脚本：建/列/改/删 + 服务器内重名约束 +
// serverID 校验（跨服务器不可见、不可改删）。
func TestTermScriptCRUD(t *testing.T) {
	s := openScriptTestStore(t)

	sc, err := s.CreateTermScript(1, "清理日志", "", "#!/bin/bash\ntrue")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Lang != "shell" {
		t.Fatalf("lang 缺省应为 shell，得到 %q", sc.Lang)
	}
	if _, err := s.CreateTermScript(1, "清理日志", "shell", "x"); !errors.Is(err, ErrTermScriptNameExists) {
		t.Fatalf("同服务器重名应报 ErrTermScriptNameExists，得到 %v", err)
	}
	// 不同服务器同名不受约束
	if _, err := s.CreateTermScript(2, "清理日志", "python", "pass"); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListTermScripts(1)
	if err != nil || len(list) != 1 {
		t.Fatalf("列脚本: %v %d", err, len(list))
	}

	if err := s.UpdateTermScript(sc.ID, 1, "清日志", "shell", "echo ok"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTermScript(sc.ID, 1)
	if err != nil || got == nil {
		t.Fatalf("取脚本: %v %v", got, err)
	}
	if got.Name != "清日志" || got.Content != "echo ok" {
		t.Fatalf("更新未生效: %+v", got)
	}
	// 重成别的名字撞车也要报错
	if _, err := s.CreateTermScript(1, "占位", "shell", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTermScript(sc.ID, 1, "占位", "shell", ""); !errors.Is(err, ErrTermScriptNameExists) {
		t.Fatalf("更新重名应报 ErrTermScriptNameExists，得到 %v", err)
	}

	// serverID 不匹配：查/改/删一律未命中
	if g, _ := s.GetTermScript(sc.ID, 2); g != nil {
		t.Fatal("跨服务器不应查到脚本")
	}
	if err := s.UpdateTermScript(sc.ID, 2, "x", "shell", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨服务器更新应 ErrNoRows，得到 %v", err)
	}
	if err := s.DeleteTermScript(sc.ID, 2); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨服务器删除应 ErrNoRows，得到 %v", err)
	}

	if err := s.DeleteTermScript(sc.ID, 1); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.ListTermScripts(1); len(list) != 1 { // 只剩「占位」
		t.Fatalf("删除后应剩 1 条，得到 %d", len(list))
	}
}

// TestTermScriptServerCascade 删除服务器连带清理专属脚本。
func TestTermScriptServerCascade(t *testing.T) {
	s := openScriptTestStore(t)
	srv, err := newSSHServer(s, "s1", "127.0.0.1:22", "root", "", "pw", 0)
	if err != nil {
		t.Fatal(err)
	}
	sid := srv.ID
	if _, err := s.CreateTermScript(sid, "a", "shell", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteServer(sid); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListTermScripts(sid); len(list) != 0 {
		t.Fatalf("删服务器后脚本应被清理，剩 %d 条", len(list))
	}
}

// TestFileFavsRoundTrip 文件收藏：保存/读取往返、覆盖更新、条数上限裁剪、
// 删服务器连带清理；无记录时返回空。
func TestFileFavsRoundTrip(t *testing.T) {
	s := openScriptTestStore(t)

	items, err := s.GetFileFavs(1)
	if err != nil || items != nil {
		t.Fatalf("无记录应返回 (nil, nil)，得到 %v %v", items, err)
	}

	save := []FileFav{{Path: "/var/log", Name: "log"}, {Path: "/", Name: "/"}}
	if err := s.SaveFileFavs(1, save); err != nil {
		t.Fatal(err)
	}
	items, err = s.GetFileFavs(1)
	if err != nil || len(items) != 2 || items[0].Path != "/var/log" || items[1].Name != "/" {
		t.Fatalf("往返不一致: %v %v", items, err)
	}

	// 覆盖保存（取消收藏一条）
	if err := s.SaveFileFavs(1, []FileFav{{Path: "/etc", Name: "etc"}}); err != nil {
		t.Fatal(err)
	}
	if items, _ = s.GetFileFavs(1); len(items) != 1 || items[0].Path != "/etc" {
		t.Fatalf("覆盖保存未生效: %v", items)
	}

	// 超上限裁剪
	var over []FileFav
	for i := 0; i < FileFavsMax+10; i++ {
		over = append(over, FileFav{Path: "/d" + string(rune('a'+i%26)) + "/" + string(rune('0'+i/26)), Name: "d"})
	}
	if err := s.SaveFileFavs(1, over); err != nil {
		t.Fatal(err)
	}
	if items, _ = s.GetFileFavs(1); len(items) != FileFavsMax {
		t.Fatalf("应裁剪到 %d 条，得到 %d", FileFavsMax, len(items))
	}

	// 删服务器连带清理
	srv, err := newSSHServer(s, "fav", "127.0.0.1:22", "root", "", "pw", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFileFavs(srv.ID, []FileFav{{Path: "/x", Name: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteServer(srv.ID); err != nil {
		t.Fatal(err)
	}
	if items, _ = s.GetFileFavs(srv.ID); len(items) != 0 {
		t.Fatalf("删服务器后收藏应被清理，剩 %d 条", len(items))
	}
}
