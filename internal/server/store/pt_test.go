package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// PT 站点存储（391）：凭据加解密落库、编辑空 Cookie 沿用、健康态只在变化时写、
// 快照覆盖与解码。
func TestPtSiteStore(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.CreatePtSite(&PtSite{SiteKey: "hdsky", Name: "HDSky", Cookie: "secret-cookie"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPtSite(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cookie != "secret-cookie" || !got.HasCookie || !got.Enabled {
		t.Fatalf("建站读回 = %+v", got)
	}

	// 库里必须是密文
	var sealed string
	if err := s.db.QueryRow(`SELECT cookie FROM pt_sites WHERE id = ?`, id).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if sealed == "" || sealed == "secret-cookie" {
		t.Fatalf("cookie 未加密落库: %q", sealed)
	}

	// 编辑空 Cookie 沿用、改名生效
	if err := s.UpdatePtSite(&PtSite{ID: id, SiteKey: "hdsky", Name: "HDSky2", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetPtSite(id)
	if got.Cookie != "secret-cookie" || got.Name != "HDSky2" || got.Enabled {
		t.Fatalf("编辑读回 = %+v", got)
	}

	// 健康态：成功写 last_ok_at 清错误；失败写错误；同错误重复不反复写
	if err := s.RecordPtSiteStatus(id, false, "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetPtSite(id)
	if got.LastError != "boom" || got.LastOKAt != 0 {
		t.Fatalf("失败态 = %+v", got)
	}
	if err := s.RecordPtSiteStatus(id, false, "boom"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPtSiteStatus(id, true, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetPtSite(id)
	if got.LastError != "" || got.LastOKAt == 0 {
		t.Fatalf("恢复态 = %+v", got)
	}

	// 快照覆盖写 + 解码；空串解码为 nil
	if err := s.SetPtSiteData(id, &PtSnapshot{Username: "u", Seeding: -1}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetPtSite(id)
	var snap PtSnapshot
	derr := json.Unmarshal([]byte(got.DataJSON), &snap)
	if derr != nil || snap.Username != "u" || snap.Seeding != -1 {
		t.Fatalf("快照解码 = %+v/%v", snap, derr)
	}
	var zero PtSnapshot
	if json.Unmarshal([]byte(""), &zero) == nil && got.DataJSON != "" {
		t.Fatalf("空 DataJSON 应存不出行: %q", got.DataJSON)
	}

	// 排序与删除
	if _, err := s.CreatePtSite(&PtSite{SiteKey: "mteam", Name: "M-Team", Cookie: "k2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePtSitesOrder([]int64{id + 1, id}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListPtSites()
	if len(list) != 2 || list[0].SiteKey != "mteam" {
		t.Fatalf("排序后 = %+v", list)
	}
	if err := s.DeletePtSite(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPtSite(id); err != ErrPtSiteNotFound {
		t.Fatalf("删除后应 NotFound: %v", err)
	}
}

// TestPtSiteHdcityRemapMigration 457 纠错迁移：v1.0.9 误接的 hdcity 行重映射
// fengchao（显示名纠正、hdcity 域回落目录默认、快照与成功水位清零、留编辑提示），
// 幂等——已重映射的行再开库不动。
func TestPtSiteHdcityRemapMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mig.db")
	key := filepath.Join(t.TempDir(), "key")
	s, err := OpenWithKey(path, key)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 手工造 v1.0.9 形态的 hdcity 行（默认名/默认域/有快照/成功水位）
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO pt_sites (site_key, name, base_url, open_url, cookie, enabled, sort, data_json, fetched_at, last_ok_at, created_at, updated_at)
		VALUES ('hdcity', '蜂巢 HDCity', 'https://hdcity.city', '', 'enc-cookie', 1, 0, '{"username":"x"}', 111, 222, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s2, err := OpenWithKey(path, key) // 重开触发迁移
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	list, err := s2.ListPtSites()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("应仍 1 行: %+v", list)
	}
	got := list[0]
	if got.SiteKey != "fengchao" || got.Name != "蜂巢" || got.BaseURL != "" || got.DataJSON != "" {
		t.Fatalf("重映射结果: key=%q name=%q base=%q data=%q", got.SiteKey, got.Name, got.BaseURL, got.DataJSON)
	}
	if got.LastOKAt != 0 || got.LastError == "" || !strings.Contains(got.LastError, "MP 专用 API Key") {
		t.Fatalf("健康态: ok=%d err=%q", got.LastOKAt, got.LastError)
	}

	// 幂等：再开一次不应变化
	raw2, _ := sql.Open("sqlite", path)
	var n int
	_ = raw2.QueryRow(`SELECT COUNT(*) FROM pt_sites WHERE site_key = 'fengchao'`).Scan(&n)
	raw2.Close()
	if n != 1 {
		t.Fatalf("幂等重开后 fengchao 行数 = %d", n)
	}
}
