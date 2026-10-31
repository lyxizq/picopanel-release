package store

import (
	"path/filepath"
	"testing"
	"time"
)

// 主页统计概览（459/473/474）聚合口径钉死：服务器在线/离线/禁用/告警、
// GitHub 启用仓与「新更新」（seen_at 基准）、待办 未完成/今日（含逾期）/逾期、
// 证书 到期、PT 应签/已签（data_json 扩展段）。
func TestHomePageStats(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().Unix()
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	// 服务器：2 在线 / 1 离线 / 1 禁用（不计离线）
	for i, srv := range []struct {
		enabled bool
		report  int64
	}{{true, now}, {true, now - 60}, {true, now - 86400}, {false, now - 86400}} {
		sv, err := s.CreateServer(string(rune('a'+i)), "", "", 0)
		if err != nil {
			t.Fatal(err)
		}
		en := 1
		if !srv.enabled {
			en = 0
		}
		if _, err := s.db.Exec(`UPDATE servers SET enabled = ?, last_report_at = ? WHERE id = ?`, en, srv.report, sv.ID); err != nil {
			t.Fatal(err)
		}
	}

	// 消息：2 未读 + 1 已读
	for i := 0; i < 3; i++ {
		m := &Message{Type: "alert", Title: "m", CreatedAt: now}
		if err := s.CreateMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE messages SET read_at = ? WHERE id = (SELECT MAX(id) FROM messages)`, now); err != nil {
		t.Fatal(err)
	}

	// 待办：未完成 4（今日 2 = 今天1+逾期1，逾期 1，明日 1）+ 已完成 1
	for _, r := range []Reminder{
		{Title: "今天", DueDate: today},
		{Title: "逾期", DueDate: yesterday},
		{Title: "明日", DueDate: tomorrow},
		{Title: "无期兜底", DueDate: "2030-01-01"},
	} {
		if err := s.CreateReminder(&r); err != nil {
			t.Fatal(err)
		}
	}
	done := &Reminder{Title: "已完成", DueDate: today}
	if err := s.CreateReminder(done); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteReminder(done.ID); err != nil {
		t.Fatal(err)
	}

	// 证书：3 条，1 条 10 天后到期
	for i := 0; i < 3; i++ {
		if _, err := s.CreateCert(string(rune('a'+i))+".example.com", 443, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE https_certs SET last_error = '', not_after = ? WHERE domain = 'a.example.com'`,
		now+10*86400); err != nil {
		t.Fatal(err)
	}

	// GitHub 发布监控（474）：A/B 启用、C 停用；仅 A 在 seen_at 之后有新 Release
	ghA, err := s.CreateGhRepo(&GhRepo{Owner: "o", Repo: "a", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ghB, err := s.CreateGhRepo(&GhRepo{Owner: "o", Repo: "b", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ghC, err := s.CreateGhRepo(&GhRepo{Owner: "o", Repo: "c", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceGhReleases(ghA, []GhRelease{{TagName: "v2.0.0", PublishedAt: now}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceGhReleases(ghB, []GhRelease{{TagName: "v1.0.0", PublishedAt: now - 86400}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceGhReleases(ghC, []GhRelease{{TagName: "v9.9.9", PublishedAt: now + 600}}); err != nil {
		t.Fatal(err)
	}
	// A 回填 seen_at 为一小时前：其 now 时刻发布的 Release 算「新更新」
	//（建仓即 seen 的默认形态下存量 Release 不算新，这里模拟老用户已看过旧版）
	if _, err := s.db.Exec(`UPDATE gh_repos SET seen_at = ? WHERE id = ?`, now-3600, ghA); err != nil {
		t.Fatal(err)
	}

	// PT：应签 3（enabled+mode），其中 2 已签（dian/hdh 扩展段）+ 1 未签 + 1 不应签（无 mode）
	for _, p := range []struct {
		key, mode, data string
	}{
		{"dian115", "normal", `{"dian":{"signed_today":true}}`},
		{"hdhaven", "normal", `{"hdh":{"signed_today":true}}`},
		{"mteam", "", `{}`},
		{"pterclub", "lucky", `{"bonus":1}`},
	} {
		id, err := s.CreatePtSite(&PtSite{SiteKey: p.key, Name: p.key, BaseURL: "https://x", SigninMode: p.mode})
		if err != nil {
			t.Fatal(err)
		}
		if p.data != "{}" || p.key == "mteam" {
			if p.data != "" {
				if _, err := s.db.Exec(`UPDATE pt_sites SET data_json = ? WHERE id = ?`, p.data, id); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	// 会员到期（492）：过期 1 + 15 日内 1 + 远期 1 → total 3 / expiring 2
	for _, d := range []int{-2, 10, 200} {
		if err := s.CreateMembership(&Membership{Biz: "biz", ExpireDate: time.Now().AddDate(0, 0, d).Format("2006-01-02")}); err != nil {
			t.Fatal(err)
		}
	}

	st, err := s.HomePageStats(600)
	if err != nil {
		t.Fatal(err)
	}
	if st.MessagesUnread != 2 {
		t.Fatalf("MessagesUnread = %d, want 2", st.MessagesUnread)
	}
	if st.Servers.Total != 4 || st.Servers.Online != 2 || st.Servers.Offline != 1 {
		t.Fatalf("Servers = %+v, want total 4 online 2 offline 1", st.Servers)
	}
	if st.Gh.Repos != 2 || st.Gh.News != 1 {
		t.Fatalf("Gh = %+v, want repos 2 news 1", st.Gh)
	}
	// 已看记账（489，单仓）：看过 A 后 News 清零；停用仓/旧 Release 从不计新
	if err := s.MarkGhRepoSeen(ghA); err != nil {
		t.Fatal(err)
	}
	if st2, err := s.HomePageStats(600); err != nil || st2.Gh.News != 0 {
		t.Fatalf("MarkGhRepoSeen 后 News 应清零: %+v, %v", st2, err)
	}
	if err := s.MarkGhRepoSeen(99999); err == nil {
		t.Fatal("记账未知仓库应报 ErrGhRepoNotFound")
	}
	if st.Reminders.Open != 4 || st.Reminders.Today != 2 || st.Reminders.Overdue != 1 {
		t.Fatalf("Reminders = %+v, want open 4 today 2 overdue 1", st.Reminders)
	}
	if st.Certs.Total != 3 || st.Certs.Expiring != 1 {
		t.Fatalf("Certs = %+v, want total 3 expiring 1", st.Certs)
	}
	if st.PT.Due != 3 || st.PT.Signed != 2 {
		t.Fatalf("PT = %+v, want due 3 signed 2", st.PT)
	}
	if st.Memberships.Total != 3 || st.Memberships.Expiring != 2 {
		t.Fatalf("Memberships = %+v, want total 3 expiring 2", st.Memberships)
	}
}
