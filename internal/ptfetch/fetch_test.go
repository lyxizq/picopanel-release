package ptfetch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 解析与抓取测试（391）：桩出 NexusPHP 站点四页与 M-Team 三个 API 端点，
// 断言通用解析口径（含 TTG 变体、做种三形态、时魔值、清理 Cookie）。

// 字节换算基准（运行期算，常量带小数进不了 int64 转换）。
var (
	giB = float64(int64(1) << 30)
	tiB = float64(int64(1) << 40)
)

const nphpIndex = `<!DOCTYPE html><html><body>
<div id="info_block">欢迎 <a href="userdetails.php?id=1234"><b>testuser</b></a>
<a href="logout.php">登出</a></div></body></html>`

const nphpDetails = `<!DOCTYPE html><html><body><table>
<tr><td class="rowhead">传输:</td><td class="rowfollow">上传量：22.85 TiB<br>下载量：9.57 TiB<br>分享率：8.46</td></tr>
<tr><td class="rowhead">等级:</td><td><img class="ClassName" src="pic/class.gif" title="Extreme User" alt="Extreme User"></td></tr>
<tr><td class="rowhead">魔力值:</td><td class="rowfollow">1,628,548.90</td></tr>
<tr><td class="rowhead">做种积分:</td><td class="rowfollow">362,093.90</td></tr>
<tr><td class="rowhead">加入日期:</td><td class="rowfollow">2022-09-22 (2 年前)</td></tr>
</table></body></html>`

const seedingSummary = `<div class="at">123 | 45.67 GiB</div><table><tr><td>a.torrent</td><td>1.00 GiB</td></tr></table>`

const mybonusPage = `<!DOCTYPE html><html><body><div>你当前每小时能获取 56.78 个魔力值</div></body></html>`

func newNexusStub(t *testing.T, details, seeding, mybonus string, extra ...func(*http.ServeMux)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/index.php", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "" {
			w.Write([]byte("<html><body><form>login</form></body></html>"))
			return
		}
		w.Write([]byte(nphpIndex))
	})
	mux.HandleFunc("/userdetails.php", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "1234" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(details))
	})
	mux.HandleFunc("/getusertorrentlistajax.php", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") == "" {
			http.Error(w, "no referer", http.StatusForbidden)
			return
		}
		w.Write([]byte(seeding))
	})
	mux.HandleFunc("/mybonus.php", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(mybonus))
	})
	for _, f := range extra {
		f(mux)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchNexus(t *testing.T) {
	srv := newNexusStub(t, nphpDetails, seedingSummary, mybonusPage)
	snap, err := fetchNexus(srv.URL, "c_secure_uid=1; c_secure_pass=x", DefaultUA, "")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Username != "testuser" {
		t.Fatalf("用户名 = %q", snap.Username)
	}
	if snap.Uploaded != int64(22.85*tiB) || snap.Downloaded != int64(9.57*tiB) {
		t.Fatalf("传输量 = %d/%d", snap.Uploaded, snap.Downloaded)
	}
	if snap.Ratio != 8.46 {
		t.Fatalf("分享率 = %v", snap.Ratio)
	}
	if snap.Level != "Extreme User" {
		t.Fatalf("等级 = %q", snap.Level)
	}
	if snap.Bonus != 1628548.90 || snap.Bonus2 != 362093.90 || !snap.HasBonus2 {
		t.Fatalf("货币 = %v/%v/%v", snap.Bonus, snap.Bonus2, snap.HasBonus2)
	}
	if snap.Seeding != 123 || snap.SeedingSz != int64(45.67*giB) {
		t.Fatalf("做种 = %d/%d", snap.Seeding, snap.SeedingSz)
	}
	if snap.BonusPH == nil || *snap.BonusPH != 56.78 {
		t.Fatalf("时魔值 = %v", snap.BonusPH)
	}
	if snap.JoinedAt == 0 {
		t.Fatal("入站时间没解析到")
	}
	if got := time.Unix(snap.JoinedAt, 0).Format("2006-01-02"); got != "2022-09-22" {
		t.Fatalf("入站时间 = %q", got)
	}
}

// TTG 变体：行标签不同（上传量/积分/注册日期）、等级纯文本、做种在详情页 div#ka2、
// ajax 端点不存在（404）。
func TestFetchNexusTTGVariants(t *testing.T) {
	details := `<!DOCTYPE html><html><body><table>
<tr><td class="rowhead">上传量:</td><td>43.95 TiB</td></tr>
<tr><td class="rowhead">下载量:</td><td>4.40 TiB</td></tr>
<tr><td class="rowhead">分享率:</td><td>9.99</td></tr>
<tr><td class="rowhead">等级:</td><td>BrontoByte</td></tr>
<tr><td class="rowhead">积分:</td><td>1,234,567.89</td></tr>
<tr><td class="rowhead">注册日期:</td><td>2021-08-11 10:00:00</td></tr>
</table>
<div id="ka2"><table><tr><th>名</th><th>评论</th><th>时间</th><th>大小</th></tr>
<tr><td>t1</td><td>0</td><td>x</td><td>1.50 GiB</td></tr>
<tr><td>t2</td><td>0</td><td>x</td><td>0.50 GiB</td></tr>
</table></div></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.php":
			w.Write([]byte(nphpIndex))
		case "/userdetails.php":
			w.Write([]byte(details))
		default:
			http.NotFound(w, r) // 无 ajax/无 mybonus
		}
	}))
	t.Cleanup(srv.Close)

	snap, err := fetchNexus(srv.URL, "tt=1", DefaultUA, "")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Uploaded != int64(43.95*tiB) || snap.Downloaded != int64(4.40*tiB) {
		t.Fatalf("传输量 = %d/%d", snap.Uploaded, snap.Downloaded)
	}
	if snap.Ratio != 9.99 {
		t.Fatalf("分享率 = %v", snap.Ratio)
	}
	if snap.Level != "BrontoByte" {
		t.Fatalf("等级 = %q", snap.Level)
	}
	if snap.Bonus != 1234567.89 {
		t.Fatalf("积分 = %v", snap.Bonus)
	}
	if snap.Seeding != 2 || snap.SeedingSz != 2*int64(giB) {
		t.Fatalf("做种(#ka2) = %d/%d", snap.Seeding, snap.SeedingSz)
	}
	if snap.BonusPH != nil {
		t.Fatalf("无 mybonus 页时时魔值应为 nil，得到 %v", *snap.BonusPH)
	}
}

// 登出态：index 拿不到用户链接要明确报 Cookie 失效，而不是静默半份数据。
func TestFetchNexusLoggedOut(t *testing.T) {
	srv := newNexusStub(t, nphpDetails, seedingSummary, mybonusPage)
	_, err := fetchNexus(srv.URL, "", DefaultUA, "")
	if err == nil || !strings.Contains(err.Error(), "Cookie") {
		t.Fatalf("登出态应报 Cookie 失效，得到 %v", err)
	}
}

// PterClub：主货币行头是「猫粮」，页面另有更靠前的泛称「积分」行——特称必须
// 优先于泛称，否则按文档序先命中积分行取到别行的数（2026-10-06 报障形态）。
func TestFetchNexusPterBonus(t *testing.T) {
	details := `<!DOCTYPE html><html><body><table>
<tr><td class="rowhead">积分:</td><td>999.00</td></tr>
<tr><td class="rowhead">传输:</td><td>上传量：22.85 TiB<br>下载量：9.57 TiB<br>分享率：8.46</td></tr>
<tr><td class="rowhead">等级:</td><td><img src="pic/class.gif" title="Extreme User" alt="Extreme User"></td></tr>
<tr><td class="rowhead">猫粮:</td><td class="rowfollow">1,234.56</td></tr>
</table></body></html>`
	srv := newNexusStub(t, details, seedingSummary, mybonusPage)
	snap, err := fetchNexus(srv.URL, "c_secure_uid=1", DefaultUA, "pter")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Bonus != 1234.56 {
		t.Fatalf("魔力值应取猫粮行 1234.56，得到 %v", snap.Bonus)
	}
}

// keepfrds 新版：/api/userdetails.php 的 user.bonus 是权威魔力值（覆盖网页
// 行解析）；接口不通时回落网页值。
func TestFetchNexusKeepfrdsBonus(t *testing.T) {
	t.Run("api覆盖网页值", func(t *testing.T) {
		details := strings.Replace(nphpDetails, "1,628,548.90", "777.00", 1)
		srv := newNexusStub(t, details, seedingSummary, mybonusPage, func(mux *http.ServeMux) {
			mux.HandleFunc("/api/userdetails.php", func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") == "" {
					http.Error(w, "no cookie", http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"version":2,"user":{"id":1234,"username":"frds","class":"user",
					"joinedAt":"2020-01-02","uploadedBytes":1,"downloadedBytes":1,"bonus":12345.67},
					"torrentStats":{"seeding":8}}`))
			})
		})
		snap, err := fetchNexus(srv.URL, "k=1", DefaultUA, "keepfrds")
		if err != nil {
			t.Fatal(err)
		}
		if snap.Bonus != 12345.67 {
			t.Fatalf("魔力值应取 API 12345.67，得到 %v", snap.Bonus)
		}
	})
	t.Run("api失败回落网页值", func(t *testing.T) {
		srv := newNexusStub(t, nphpDetails, seedingSummary, mybonusPage) // 无 /api 路由=404
		snap, err := fetchNexus(srv.URL, "k=1", DefaultUA, "keepfrds")
		if err != nil {
			t.Fatal(err)
		}
		if snap.Bonus != 1628548.90 {
			t.Fatalf("API 失效应沿用网页值 1628548.90，得到 %v", snap.Bonus)
		}
	})
}

func newMTeamStub(t *testing.T, key string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srvURL string
	handle := func(w http.ResponseWriter, r *http.Request, data string) {
		if r.Header.Get("x-api-key") != key {
			w.Write([]byte(`{"success":"false","message":"api key 非法"}`))
			return
		}
		if r.Header.Get("Origin") != srvURL {
			w.Write([]byte(`{"success":"false","message":"origin 校验失败"}`))
			return
		}
		w.Write([]byte(data))
	}
	mux.HandleFunc("/api/member/profile", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, `{"message":"","data":{"id":100,"username":"mtuser","createdDate":"2021-11-20 10:00:00",
			"role":"6","memberCount":{"uploaded":85000000000000,"downloaded":4800000000000,"bonus":123456.78}}}`)
	})
	mux.HandleFunc("/api/tracker/myPeerStatistics", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, `{"message":"","data":{"seederCount":42,"seederSize":1099511627776,"uploadCount":3}}`)
	})
	mux.HandleFunc("/api/tracker/mybonus", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, `{"message":"","data":{"formulaParams":{"finalBs":"88.5"}}}`)
	})
	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchMTeam(t *testing.T) {
	srv := newMTeamStub(t, "good-key")
	snap, err := fetchMTeam(srv.URL, "good-key", DefaultUA)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Username != "mtuser" || snap.Level != "Veteran User" {
		t.Fatalf("身份 = %q/%q", snap.Username, snap.Level)
	}
	if snap.Uploaded != 85000000000000 || snap.Downloaded != 4800000000000 {
		t.Fatalf("传输量 = %d/%d", snap.Uploaded, snap.Downloaded)
	}
	if snap.Bonus != 123456.78 {
		t.Fatalf("魔力 = %v", snap.Bonus)
	}
	if snap.Seeding != 42 || snap.SeedingSz != 1<<40 {
		t.Fatalf("做种 = %d/%d", snap.Seeding, snap.SeedingSz)
	}
	if snap.BonusPH == nil || *snap.BonusPH != 88.5 {
		t.Fatalf("时魔值 = %v", snap.BonusPH)
	}
	if snap.JoinedAt == 0 {
		t.Fatal("入站时间没解析到")
	}

	if _, err := fetchMTeam(srv.URL, "bad-key", DefaultUA); err == nil || !strings.Contains(err.Error(), "api key 非法") {
		t.Fatalf("坏 key 应带出上游 message，得到 %v", err)
	}
	if _, err := fetchMTeam(srv.URL, "   ", DefaultUA); err == nil || !strings.Contains(err.Error(), "未配置 API Key") {
		t.Fatalf("空 key 应直接报未配置（上游文案难懂），得到 %v", err)
	}
}

// 2024-06 起官方 API 统一走 api. 子域，Web 域名（kp.）的 /api 由源站 nginx
// 直接 403——改写只对 m-team 系域名生效，httptest 桩等其他基址原样保留。
func TestMTeamAPIBase(t *testing.T) {
	cases := map[string]string{
		"https://kp.m-team.cc":   "https://api.m-team.cc",
		"https://kp.m-team.cc/":  "https://api.m-team.cc",
		"https://api.m-team.cc":  "https://api.m-team.cc",
		"https://mz.m-team.io":   "https://api.m-team.io",
		"https://example.com":    "https://example.com",
		"http://127.0.0.1:37121": "http://127.0.0.1:37121",
		"not a url":              "not a url",
	}
	for in, want := range cases {
		if got := mteamAPIBase(in); got != want {
			t.Errorf("mteamAPIBase(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestParseSizeBytes(t *testing.T) {
	cases := map[string]int64{
		"22.85 TiB":  int64(22.85 * tiB),
		"502.02 GiB": int64(502.02 * giB),
		"0.00 B":     0,
		"1,234.5 KB": int64(1234.5 * float64(1<<10)),
		"1.5 GiB":    int64(1.5 * giB),
		"plain":      0,
	}
	for in, want := range cases {
		if got := parseSizeBytes(in); got != want {
			t.Errorf("parseSizeBytes(%q) = %d，期望 %d", in, got, want)
		}
	}
}

func TestParseNumStr(t *testing.T) {
	if v := parseNumStr("1,628,548.90"); v != 1628548.90 {
		t.Errorf("parseNumStr 千分位 = %v", v)
	}
	if v := parseNumStr("你当前每小时能获取 56.78 个"); v != 56.78 {
		t.Errorf("parseNumStr 混合文本 = %v", v)
	}
	if v := parseNumStr("没有数字"); v != 0 {
		t.Errorf("parseNumStr 无数字 = %v", v)
	}
}

func TestParseTimeStr(t *testing.T) {
	cases := map[string]bool{
		"2022-09-22":          true,
		"2022-09-22 10:11:12": true,
		"2022/09/22 10:11":    true,
		"2022-09-22 (2 年前)":   true,
		"乱七八糟":                false,
		"":                    false,
	}
	for in, ok := range cases {
		got := parseTimeStr(in)
		if ok != (got != 0) {
			t.Errorf("parseTimeStr(%q) = %d", in, got)
		}
	}
}

func TestRatioOf(t *testing.T) {
	if v := ratioOf("上传量：1.00 TiB 下载量：2.00 TiB 分享率：8.46"); v != 8.46 {
		t.Errorf("ratioOf 数值 = %v", v)
	}
	if v := ratioOf("分享率：---"); v != -1 {
		t.Errorf("ratioOf 无限 = %v", v)
	}
	if v := ratioOf("分享率：Inf."); v != -1 {
		t.Errorf("ratioOf Inf = %v", v)
	}
}

func TestCleanCookie(t *testing.T) {
	got := CleanCookie(" a=1; \n b=2 ;c_secure_pass=x;")
	want := "a=1; b=2; c_secure_pass=x"
	if got != want {
		t.Errorf("CleanCookie = %q，期望 %q", got, want)
	}
	if got := CleanCookie("Cookie: k=v"); got != "k=v" {
		t.Errorf("CleanCookie 前缀 = %q", got)
	}
	if got := CleanCookie(";;"); got != "" {
		t.Errorf("CleanCookie 空串 = %q", got)
	}
}

func TestFetchSiteDispatch(t *testing.T) {
	// 目录分发：mteam 走 API、其余走页面解析（拿桩各验一次）
	mt := newMTeamStub(t, "k")
	s, err := FetchSite(Site{SiteKey: "mteam", BaseURL: mt.URL, Cookie: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Username != "mtuser" {
		t.Fatalf("分发 mteam 失败: %v", s.Username)
	}
	nx := newNexusStub(t, nphpDetails, seedingSummary, mybonusPage)
	s, err = FetchSite(Site{SiteKey: "hdsky", BaseURL: nx.URL, Cookie: "c=1"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Username != "testuser" {
		t.Fatalf("分发 nexusphp 失败: %v", s.Username)
	}
	if _, err := FetchSite(Site{SiteKey: "unknown"}); err == nil {
		t.Fatal("未知站点应报错")
	}
}

// 防封号/防泄密（2026-10-05 用户要求复查）：跨域重定向必须熔断且 Cookie 绝不
// 发到第二个主机；同域重定向正常跟随。
func TestCrossHostRedirectBlocked(t *testing.T) {
	var otherGotCookie string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherGotCookie = r.Header.Get("Cookie")
		w.Write([]byte("<html>parked</html>"))
	}))
	defer other.Close()
	main := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer main.Close()

	_, err := fetchPage(main.URL+"/index.php", "c_secure_pass=secret", DefaultUA)
	if err == nil || !strings.Contains(err.Error(), "重定向到其他域名") {
		t.Fatalf("跨域重定向应熔断，得到 %v", err)
	}
	if otherGotCookie != "" {
		t.Fatalf("Cookie 泄漏到了重定向目标: %q", otherGotCookie)
	}
}

func TestSameHostRedirectFollowed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/b", http.StatusFound)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>ok</html>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	body, err := fetchPage(srv.URL+"/a", "", DefaultUA)
	if err != nil || !strings.Contains(body, "ok") {
		t.Fatalf("同域重定向应正常跟随: %v/%q", err, body)
	}
}
