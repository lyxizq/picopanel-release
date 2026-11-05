package pt

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lz-monitor/internal/ptfetch"
	"lz-monitor/internal/server/store"
)

func TestNextRunAt(t *testing.T) {
	loc := time.Local
	if n := nextRunAt(time.Date(2026, 10, 4, 5, 0, 0, 0, loc)); n != time.Date(2026, 10, 4, 6, 0, 0, 0, loc) {
		t.Errorf("05:00 的下次抓取 = %v", n)
	}
	if n := nextRunAt(time.Date(2026, 10, 4, 6, 0, 1, 0, loc)); n != time.Date(2026, 10, 5, 6, 0, 0, 0, loc) {
		t.Errorf("06:00:01 的下次抓取 = %v", n)
	}
}

// 启动补拉口径（442）：上次成功早于最近一个已过的 06:00 = 错过一轮要补；
// 06:00 前的重启只认昨天那轮（今天这轮还没到，不双抓）。
func TestMissedRound(t *testing.T) {
	loc := time.Local
	at := func(d int, h, m int) int64 {
		return time.Date(2026, 10, d, h, m, 0, 0, loc).Unix()
	}
	morning := time.Date(2026, 10, 6, 8, 47, 0, 0, loc) // 06:00 后重启（用户报障形态）
	if !missedRound(at(5, 20, 47), morning) {
		t.Fatal("昨晚更过、今早 06:00 后重启应视为错过当天轮")
	}
	if missedRound(at(6, 6, 30), morning) {
		t.Fatal("今天 06:00 轮已成功的不该补")
	}
	if !missedRound(0, morning) {
		t.Fatal("从未成功过的该补")
	}
	pre := time.Date(2026, 10, 6, 5, 0, 0, 0, loc) // 06:00 前重启
	if missedRound(at(5, 20, 47), pre) {
		t.Fatal("06:00 前重启：昨天轮已覆盖的不该补（等今天轮）")
	}
	if !missedRound(at(5, 5, 0), pre) {
		t.Fatal("连昨天 06:00 轮都没赶上的该补")
	}
}

// newStubNexus 最小 NexusPHP 桩（RoundGuard 测试用，fixture 同 ptfetch 测试）。
func newStubNexus(t *testing.T) *httptest.Server {
	t.Helper()
	index := `<html><body><div id="info_block">欢迎 <a href="userdetails.php?id=1"><b>u</b></a></div></body></html>`
	details := `<html><body><table>
<tr><td class="rowhead">传输:</td><td>上传量：1.00 TiB<br>下载量：1.00 TiB<br>分享率：1.00</td></tr></table></body></html>`
	seed := `<div>1 | 1.00 GiB</div>`
	mux := http.NewServeMux()
	mux.HandleFunc("/index.php", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(index)) })
	mux.HandleFunc("/userdetails.php", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(details)) })
	mux.HandleFunc("/getusertorrentlistajax.php", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(seed)) })
	mux.HandleFunc("/mybonus.php", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<html></html>`)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// 整轮熔断：一轮进行中再触发必须拒绝（防连点「全部刷新」叠加流量）。
func TestRefreshAllRoundGuard(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nx := newStubNexus(t)
	if _, err := s.CreatePtSite(&store.PtSite{SiteKey: "hdsky", Name: "HDSky", BaseURL: nx.URL, Cookie: "c=1"}); err != nil {
		t.Fatal(err)
	}
	m := New(s)
	if !m.RefreshAllSync() {
		t.Fatal("首轮应开跑")
	}
	if !m.RefreshAllSync() {
		t.Fatal("轮结束后应可再开")
	}
	// 异步轮进行中再触发：站内 3 步各有 400ms 间隔，窗口足够宽
	done := make(chan bool, 1)
	go func() { done <- m.RefreshAllAsync() }()
	time.Sleep(100 * time.Millisecond)
	if m.RefreshAllAsync() {
		t.Fatal("整轮进行中再触发应返回 false")
	}
	if !<-done {
		t.Fatal("首轮应成功开跑")
	}
}

// 站点级 panic 隔离（443）：单站抓取链路 panic 只记该站健康态，其余站点照常、
// 整轮正常收尾且可再开。
func TestSitePanicIsolation(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nx := newStubNexus(t)
	if _, err := s.CreatePtSite(&store.PtSite{SiteKey: "hdsky", Name: "炸点", BaseURL: nx.URL, Cookie: "c=1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePtSite(&store.PtSite{SiteKey: "pter", Name: "正常站", BaseURL: nx.URL, Cookie: "c=1"}); err != nil {
		t.Fatal(err)
	}
	m := New(s)
	real := m.fetcher
	m.fetcher = func(s store.PtSite) (*store.PtSnapshot, error) {
		if s.SiteKey == "hdsky" {
			panic("解析爆炸")
		}
		return real(s)
	}
	if !m.RefreshAllSync() {
		t.Fatal("含 panic 站点的整轮应正常收尾")
	}
	sites, _ := s.ListPtSites()
	for _, site := range sites {
		switch site.SiteKey {
		case "hdsky":
			if site.LastError == "" {
				t.Fatal("panic 应记进炸点站健康态")
			}
		case "pter":
			if site.LastOKAt == 0 || site.LastError != "" {
				t.Fatalf("正常站不应被连累: %+v", site)
			}
		}
	}
	if !m.RefreshAllSync() {
		t.Fatal("下一轮应可正常开跑（未被 panic 卡死）")
	}
}

// 每日轮读库失败不吞轮次（443）：返回 false 交给 Run 下一分钟重试。
func TestDailyRoundListFailure(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(s)
	if !m.DailyRound() {
		t.Fatal("正常库每日轮应返回 true")
	}
	s.Close()
	if m.DailyRound() {
		t.Fatal("读库失败应返回 false（下一分钟重试）")
	}
}

// 全中转（零直连站）形态每日轮收尾后 roundBusy 必须复位（461）：曾因
// len(direct)==0 不启动抓取 goroutine 把闸永久卡 true，此后每日轮全部静默
// 跳过、手动整轮恒被拒。2026-10-07 生产事故的主根因在 server.go 拉起顺序
// （ptMgr.Run 排在 aiMgr.Run 后从未启动，无法单测），本测试钉住 manager 侧
// 同形态隐患。
func TestDailyRoundAllProxiedResetsRoundBusy(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreatePtSite(&store.PtSite{SiteKey: "dian", Name: "癫影", BaseURL: "https://dian.example.com", Cookie: "c=1", ProxyServerID: 7}); err != nil {
		t.Fatal(err)
	}
	m := New(s)
	if !m.DailyRound() {
		t.Fatal("全中转形态每日轮应正常开跑")
	}
	for deadline := time.Now().Add(2 * time.Second); ; {
		m.mu.Lock()
		busy := m.roundBusy
		m.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("零直连站每日轮收尾后 roundBusy 未复位（闸卡死）")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 签到短路（445）：快照今天抓的且已签 → 签到按钮零对站请求、中转不入队，
// 直接提示今日已签到；健康态不是今天的走正常链路（失败即暴露）。
func TestSigninShortCircuitSignedToday(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// 桩是 NexusPHP 形态：dian/hdh 链路一旦真发请求必失败，天然防短路失效
	nx := newStubNexus(t)
	mkSite := func(name string, proxy int64) int64 {
		t.Helper()
		id, err := s.CreatePtSite(&store.PtSite{SiteKey: "dian115", Name: name, BaseURL: nx.URL, Cookie: "portal_token=x", ProxyServerID: proxy})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	seed := func(id int64) {
		t.Helper()
		if err := s.SetPtSiteData(id, &store.PtSnapshot{Dian: &ptfetch.DianExtra{SignedToday: true}}); err != nil {
			t.Fatal(err)
		}
	}

	// 负例：有已签快照但健康态不是今天（last_ok=0）→ 不短路，真出站（打桩必失败）
	idNeg := mkSite("负例", 0)
	seed(idNeg)
	m := New(s)
	if _, _, err := m.SigninNow(idNeg); err == nil {
		t.Fatal("非今天的已签快照不应短路（应对站查询今日状态）")
	}

	// 正例（直连）：同一站补录今天健康态 → 已签快照 + 健康态今天 → 零请求回文案
	if err := s.RecordPtSiteStatus(idNeg, true, ""); err != nil {
		t.Fatal(err)
	}
	if snap, msg, err := m.SigninNow(idNeg); err != nil || msg != "今日已签到" || snap != nil {
		t.Fatalf("直连已签短路不符: snap=%v msg=%q err=%v", snap, msg, err)
	}

	// 正例（中转）：site_key 唯一，删掉直连站后建中转站——同样短路，且不入队
	//（ErrQueued 都不该出现）
	if err := s.DeletePtSite(idNeg); err != nil {
		t.Fatal(err)
	}
	idPx := mkSite("中转", 1)
	seed(idPx)
	if err := s.RecordPtSiteStatus(idPx, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := m.SigninNow(idPx); err != nil || msg != "今日已签到" {
		t.Fatalf("中转已签短路不符: msg=%q err=%v", msg, err)
	}
	if tasks, terr := s.ClaimPtTasks(1, 10); terr != nil || len(tasks) != 0 {
		t.Fatalf("已签中转站不应再入队任务: n=%d err=%v", len(tasks), terr)
	}
}
