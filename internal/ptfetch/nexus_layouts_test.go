package ptfetch

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 456 改版站布局回归：audiences/憨憨/ubits/HD Dolby/pttime 的魔力值与入站
// 时间抓取错误——行头容器不止 td（憨憨 span、audiences div 内联）、行头叫法
// 各异（憨豆/鲸币/U币）、行头带前后缀（exact 落空）、值与标签同格内联。
// 每站用 PT-depiler 同款结构桩出代表形态，钉死解析口径。

func newLayoutStub(t *testing.T, details string) *httptest.Server {
	t.Helper()
	return newNexusStub(t, details, seedingSummary, mybonusPage)
}

// 憨憨新版：span 行头 + 相邻 span/div 值（PT-depiler：span:contains('憨豆')+div、
// span:contains('加入日期')+span），主货币叫「憨豆」；等级行不存在，挂在
// userdetails 链接的 class（PT-depiler a[class*='_Name']，如 Elite_User_Name）。
func TestFetchNexusHhanSpanLayout(t *testing.T) {
	details := `<!DOCTYPE html><html><body>
<div class="panel"><span>憨豆</span> <div>12,345.67</div></div>
<div class="panel"><span>做种积分</span> <span>800</span></div>
<div class="panel"><span>加入日期</span> <span>2020-05-06 (5 年前)</span></div>
<div class="top-bar"><a href="userdetails.php?id=7" class="Elite_User_Name">测试者</a></div>
</body></html>`
	srv := newLayoutStub(t, details)
	snap, err := fetchNexus(srv.URL, "c_secure_uid=1", DefaultUA, "hhanclub")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Bonus != 12345.67 {
		t.Fatalf("憨豆(魔力值) = %v", snap.Bonus)
	}
	if got := time.Unix(snap.JoinedAt, 0).Format("2006-01-02"); got != "2020-05-06" {
		t.Fatalf("入站时间 = %q", got)
	}
	if snap.Level != "Elite User" {
		t.Fatalf("等级 = %q", snap.Level)
	}
}

// UBits：行头叫「U币」且值同格内联（PT-depiler rowfollow:contains + 正则取数）。
func TestFetchNexusUbitsInlineCoin(t *testing.T) {
	details := `<!DOCTYPE html><html><body><table>
<tr><td class="rowfollow">U币: 1,234.56</td></tr>
<tr><td class="rowhead">注册日期:</td><td>2021-01-02 10:00:00</td></tr>
</table></body></html>`
	srv := newLayoutStub(t, details)
	snap, err := fetchNexus(srv.URL, "c_secure_uid=1", DefaultUA, "ubits")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Bonus != 1234.56 {
		t.Fatalf("U币(魔力值) = %v", snap.Bonus)
	}
	if got := time.Unix(snap.JoinedAt, 0).Format("2006-01-02"); got != "2021-01-02" {
		t.Fatalf("入站时间 = %q", got)
	}
}

// HD Dolby：主货币叫「鲸币」（exact 新标签）；入站行头带双语前后缀
//（exact 落空 → contains 兜）。
func TestFetchNexusHDDolbyDecoratedJoin(t *testing.T) {
	details := `<!DOCTYPE html><html><body><table>
<tr><td class="rowhead">鲸币:</td><td class="rowfollow">8,888.88</td></tr>
<tr><td class="rowhead">加入日期 / Join date:</td><td>2019-03-04</td></tr>
</table></body></html>`
	srv := newLayoutStub(t, details)
	snap, err := fetchNexus(srv.URL, "c_secure_uid=1", DefaultUA, "hddolby")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Bonus != 8888.88 {
		t.Fatalf("鲸币(魔力值) = %v", snap.Bonus)
	}
	if got := time.Unix(snap.JoinedAt, 0).Format("2006-01-02"); got != "2019-03-04" {
		t.Fatalf("入站时间 = %q", got)
	}
}

// audiences 新版：积分在 userbar 紧凑指标（类名直取），加入日期在 div 内联
//（PT-depiler：div:contains('加入日期：')）；等级在 hero 的 ud-class 里
//（PT-depiler span.ud-class > b），b 文本是「(别名)英文名」双语形态——
// 文本优先 + 剥前缀括号别名（487，chip 宽度有限双语必截断）。
func TestFetchNexusAudiencesUserbarAndInlineJoin(t *testing.T) {
	details := `<!DOCTYPE html><html><body>
<div class="site-userbar site-userbar--compact">
  <div class="site-userbar__compact-metric--bonus">65,432</div>
</div>
<div class="ud-hero"><span class="ud-class"><b class="Crazy_User_Name">(头号玩家)Crazy User</b></span></div>
<div class="profile-meta"><div>加入日期：2023-06-07</div></div>
</body></html>`
	srv := newLayoutStub(t, details)
	snap, err := fetchNexus(srv.URL, "c_secure_uid=1", DefaultUA, "audiences")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Bonus != 65432 {
		t.Fatalf("userbar 魔力值 = %v", snap.Bonus)
	}
	if got := time.Unix(snap.JoinedAt, 0).Format("2006-01-02"); got != "2023-06-07" {
		t.Fatalf("入站时间 = %q", got)
	}
	if snap.Level != "Crazy User" {
		t.Fatalf("等级 = %q", snap.Level)
	}
}

// audiences b 无文本时退 class 推导（484 口径回归：等级名只挂 class 的形态）。
func TestClassBLevelFromClassOnly(t *testing.T) {
	doc := parseHTML(`<html><body><span class="ud-class"><b class="Power_User_Name"></b></span></body></html>`)
	el := findByClass(doc, "ud-class")
	if el == nil {
		t.Fatal("ud-class 未命中")
	}
	if got := classBLevel(el); got != "Power User" {
		t.Fatalf("class 推导等级 = %q", got)
	}
}

// pttime（经典布局但行头带图标等装饰文本）：特称 contains 兜底 rescued，
// 泛称「积分」依旧只能末位兜（433 纪律在扩容器版上保持）；等级行头同样带
// 装饰、值是 <b title> 属性形态（PT-depiler attr:title，格内无文本）。
func TestFetchNexusDecoratedMagicRow(t *testing.T) {
	details := `<!DOCTYPE html><html><body><table>
<tr><td class="rowhead"><img src="i.gif">魔力值 (Karma):</td><td class="rowfollow">777.7</td></tr>
<tr><td class="rowhead"><img src="c.gif">等级 (Class):</td><td class="rowfollow"><b title="Peasant">婴儿</b></td></tr>
<tr><td class="rowhead">积分商城:</td><td class="rowfollow">入口</td></tr>
<tr><td class="rowhead">加入日期:</td><td class="rowfollow">2022-09-22 (2 年前)</td></tr>
</table></body></html>`
	srv := newLayoutStub(t, details)
	snap, err := fetchNexus(srv.URL, "c_secure_uid=1", DefaultUA, "pttime")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Bonus != 777.7 {
		t.Fatalf("装饰行头魔力值 = %v", snap.Bonus)
	}
	if snap.Level != "Peasant" {
		t.Fatalf("等级 = %q", snap.Level)
	}
}

// longpt（496）：传输行是「分享率先行 + 双语括注实际值 + 实际上/下载量」
// 的真实形态（2026-10-08 实抓）——「实际上传量/实际下载量」内嵌
// 「上传量/下载量」子串且在后，正则首match必须命中正式值而非实际值；
// 分享率取官方值（括注前的首个数字）。
func TestFetchNexusLongPTTransfersLayout(t *testing.T) {
	details := `<!DOCTYPE html><html><body><table>
<tr><td class="rowhead">传输</td><td class="rowfollow">分享率: 181.421（实际分享率：6.594）&nbsp;&nbsp;上传量: 17.717 TB&nbsp;&nbsp;下载量: 100.00 GB&nbsp;&nbsp;实际上传量: 795.43 GB&nbsp;&nbsp;实际下载量: 120.62 GB&nbsp;&nbsp;实际上传/下载量 (仅用于记录, 不参与分享率计算)</td></tr>
<tr><td class="rowhead">等级:</td><td class="rowfollow">贵宾</td></tr>
<tr><td class="rowhead">魔力值:</td><td class="rowfollow">4,373,451.0</td></tr>
</table></body></html>`
	srv := newLayoutStub(t, details)
	snap, err := fetchNexus(srv.URL, "c=1", DefaultUA, "longpt")
	if err != nil {
		t.Fatal(err)
	}
	tb := float64(1 << 40)
	if snap.Uploaded != int64(17.717*tb) {
		t.Fatalf("上传量 = %d", snap.Uploaded)
	}
	if snap.Downloaded != 100<<30 {
		t.Fatalf("下载量 = %d（欲 100GB）", snap.Downloaded)
	}
	if snap.Ratio != 181.421 {
		t.Fatalf("分享率 = %v（欲 181.421，实际分享率不得遮蔽官方值）", snap.Ratio)
	}
}

// 「无限」形态的无穷分享率（longpt 信息条/传输行 0 下载量除零显示，496）。
func TestRatioOfChineseUnlimited(t *testing.T) {
	for _, s := range []string{"分享率: 无限", "分享率：無限", "分享率: 无限 上传量: 1.00 TB"} {
		if v := ratioOf(s); v != -1 {
			t.Errorf("ratioOf(%q) = %v，欲 -1(∞)", s, v)
		}
	}
}

// 496 体检与重试：详情页位置被站点塞了首页/挑战页（无任何传输标记）时，
// 吸收会话 Cookie 重试一次；重试仍错则报错暴露，绝不静默解析错页存垃圾。
func TestFetchNexusDetailsWrongPageRetry(t *testing.T) {
	idxPage := `<!DOCTYPE html><html><body><div id="info">
	<a href="userdetails.php?id=9">测试者</a>
	<span>分享率: 无限</span><span>上传量: 1.00 TB</span><span>下载量: 0.00 KB</span>
	</div></body></html>`
	good := `<!DOCTYPE html><html><body><table>
	<tr><td class="rowhead">传输</td><td class="rowfollow">上传量: 2.00 TB 下载量: 64.00 GiB 分享率: 32.0</td></tr>
	</table></body></html>`
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/index.php", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(idxPage)) })
	mux.HandleFunc("/userdetails.php", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(idxPage)) // 首次给错页（防刷新跳转落首页形态）
			return
		}
		if r.Header.Get("Cookie") == "" {
			t.Errorf("重试未携带 Cookie")
		}
		w.Write([]byte(good))
	})
	mux.HandleFunc("/getusertorrentlistajax.php", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(seedingSummary))
	})
	mux.HandleFunc("/mybonus.php", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(mybonusPage))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	snap, err := fetchNexus(srv.URL, "c=1", DefaultUA, "longpt")
	if err != nil {
		t.Fatalf("错页后重试应成功: %v", err)
	}
	if snap.Downloaded != 64<<30 || snap.Ratio != 32 {
		t.Fatalf("重试后快照不对: downloaded=%d ratio=%v", snap.Downloaded, snap.Ratio)
	}

	// 恒错页：必须报错而不是产出半截快照
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/index.php", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(idxPage)) })
	mux2.HandleFunc("/userdetails.php", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(idxPage)) })
	mux2.HandleFunc("/getusertorrentlistajax.php", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(seedingSummary)) })
	mux2.HandleFunc("/mybonus.php", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(mybonusPage)) })
	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()
	if _, err := fetchNexus(srv2.URL, "c=1", DefaultUA, "longpt"); err == nil {
		t.Fatal("恒错页应报错暴露")
	}
}

// 496：重定向跨跳 Set-Cookie 合并——同名以站点新签的覆盖旧值，其余保留。
func TestMergeSetCookie(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x/userdetails.php", nil)
	req.Header.Set("Cookie", "server_name_session=OLD; c_secure_pass=KEEP")
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "server_name_session=NEW; Path=/")
	resp.Header.Add("Set-Cookie", "fresh=1; Path=/")
	mergeSetCookie(req, resp)
	if got, want := req.Header.Get("Cookie"), "server_name_session=NEW; c_secure_pass=KEEP; fresh=1"; got != want {
		t.Errorf("合并结果 = %q，欲 %q", got, want)
	}
}
