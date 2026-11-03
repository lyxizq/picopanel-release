package bilibili

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestGetMixinKey 重排表推导：img+sub 拼接按固定表重排取前 32 位；推导值经
// 真实接口（search/type 带 w_rid 调用返回 code=0）验证。
func TestGetMixinKey(t *testing.T) {
	got := getMixinKey("7cd084941338484aae1ad9425b84077c", "4932ca06026ee967735629675208361b")
	if got != "671d9124a53c00924e43936a70464028" {
		t.Fatalf("mixin key 锚定值不符: %s", got)
	}
}

// TestSignWBIParams 文档示例参数 {foo:"one value", bar:" ", baz:42,
// wts:1702204800} 的签名结果（真实接口验证通过后的锚定值，防重构回退：
// 值过滤/排序/空格 %20/md5 构造任何一处变都会失配）。
func TestSignWBIParams(t *testing.T) {
	params := url.Values{}
	params.Set("foo", "one value")
	params.Set("bar", " ")
	params.Set("baz", "42")
	signWBIParams(params, "7cd084941338484aae1ad9425b84077c", "4932ca06026ee967735629675208361b", 1702204800)
	if params.Get("w_rid") != "b4a4de6682267fb387e467e0ec819891" {
		t.Fatalf("w_rid 锚定值不符: %s", params.Get("w_rid"))
	}
}

// newTestClient 起一个模拟 B 站接口的 httptest（注册 handler）。
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient("")
	c.base = srv.URL
	c.throttle = time.Millisecond // 节流注入：单测不打真 B 站，免被 1s 间隔拖慢
	return c
}

func envOK(data any) string {
	b, _ := json.Marshal(data)
	return `{"code":0,"message":"0","data":` + string(b) + `}`
}

// TestResolveTargetByName 地址=合集名字：搜人 → 清单匹配 → 首页校验。
func TestResolveTargetByName(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/frontend/finger/spi":
			fmt.Fprint(w, envOK(map[string]string{"b_3": "buvid3-x", "b_4": "buvid4-x"}))
		case "/x/web-interface/nav":
			fmt.Fprint(w, envOK(map[string]any{"wbi_img": map[string]string{
				"img_url": "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
				"sub_url": "https://i0.hdslb.com/bfs/wbi/4932ca06026ee967735629675208361b.png",
			}}))
		case "/x/web-interface/search/type":
			if r.URL.Query().Get("w_rid") == "" {
				t.Error("搜索请求缺 WBI 签名")
			}
			if r.Header.Get("Cookie") == "" || !strings.Contains(r.Header.Get("Cookie"), "buvid3=buvid3-x") {
				t.Errorf("搜索请求缺 buvid Cookie: %q", r.Header.Get("Cookie"))
			}
			fmt.Fprint(w, envOK(map[string]any{"result": []map[string]any{
				{"mid": 42, "uname": "别的UP"},
				{"mid": 7, "uname": "某UP"},
			}}))
		case "/x/polymer/web-space/seasons_series_list":
			fmt.Fprint(w, envOK(map[string]any{"items_lists": map[string]any{
				"page": map[string]any{"page_num": 1, "page_size": 20, "total": 2},
				"seasons_list": []map[string]any{
					{"meta": map[string]any{"season_id": 100, "name": "合集A", "total": 5}},
				},
				"series_list": []map[string]any{
					{"meta": map[string]any{"series_id": 200, "name": "视频列表B", "total": 12}},
				},
			}}))
		case "/x/polymer/web-space/seasons_archives_list":
			fmt.Fprint(w, envOK(map[string]any{
				"meta": map[string]any{"season_id": 100, "name": "合集A", "total": 5},
				"archives": []map[string]any{{"bvid": "BV1", "title": "第1话", "pubdate": 1700000000}},
			}))
		case "/x/series/archives":
			fmt.Fprint(w, envOK(map[string]any{
				"meta": map[string]any{"name": "视频列表B", "total": 12},
				"archives": []map[string]any{{"bvid": "BV2", "title": "s1", "pubdate": 1700000001, "cid": 9}},
			}))
		default:
			t.Errorf("未预期的接口: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	tg, err := c.ResolveTarget("某UP", "合集A")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Mid != 7 || tg.Kind != "season" || tg.ListID != 100 || tg.Title != "合集A" || tg.Total != 5 {
		t.Fatalf("解析结果异常: %+v", tg)
	}
	// 名字包含匹配 + 未命中报错带候选
	if _, err := c.ResolveTarget("某UP", "视频列表"); err != nil {
		t.Fatalf("包含匹配失败: %v", err)
	}
	_, err = c.ResolveTarget("某UP", "不存在的合集")
	if err == nil || !strings.Contains(err.Error(), "合集A") || !strings.Contains(err.Error(), "视频列表B") {
		t.Fatalf("未命中应报候选清单: %v", err)
	}
}

// TestResolveTargetByURL 链接形态：collectiondetail/seriesdetail 直取类型；
// /lists/<id> 先合集后视频列表回退。
func TestResolveTargetByURL(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/frontend/finger/spi":
			fmt.Fprint(w, envOK(map[string]string{"b_3": "b3", "b_4": "b4"}))
		case "/x/web-interface/nav":
			fmt.Fprint(w, envOK(map[string]any{"wbi_img": map[string]string{
				"img_url": "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
				"sub_url": "https://i0.hdslb.com/bfs/wbi/4932ca06026ee967735629675208361b.png",
			}}))
		case "/x/polymer/web-space/seasons_archives_list":
			if r.URL.Query().Get("season_id") == "77" {
				http.Error(w, `{"code":-404,"message":"啥都木有","data":null}`, 200)
				return
			}
			fmt.Fprint(w, envOK(map[string]any{
				"meta": map[string]any{"name": "新合集", "total": 3},
				"archives": []map[string]any{{"bvid": "BV9", "title": "x", "pubdate": 1}},
			}))
		case "/x/series/archives":
			fmt.Fprint(w, envOK(map[string]any{
				"meta": map[string]any{"name": "老列表", "total": 9},
				"archives": []map[string]any{{"bvid": "BV8", "title": "y", "pubdate": 2, "cid": 555}},
			}))
		default:
			t.Errorf("未预期的接口: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	tg, err := c.ResolveTarget("", "https://space.bilibili.com/123/channel/collectiondetail?sid=66")
	if err != nil || tg.Kind != "season" || tg.Title != "新合集" {
		t.Fatalf("collectiondetail 解析异常: %+v err=%v", tg, err)
	}
	tg, err = c.ResolveTarget("", "https://space.bilibili.com/123/channel/seriesdetail?sid=88&seid=1")
	if err != nil || tg.Kind != "series" || tg.ListID != 88 || tg.Title != "老列表" {
		t.Fatalf("seriesdetail 解析异常: %+v err=%v", tg, err)
	}
	tg, err = c.ResolveTarget("", "https://space.bilibili.com/123/lists/77")
	if err != nil || tg.Kind != "series" || tg.ListID != 77 {
		t.Fatalf("lists 回退到视频列表失败: %+v err=%v", tg, err)
	}
	if _, err := c.ResolveTarget("", "https://space.bilibili.com/123/video"); err != ErrNoList {
		t.Fatalf("无列表 id 链接应报 ErrNoList: %v", err)
	}
}

// TestListItemsSeed 翻页 + 发布时间新→旧排序 + maxItems 截断。
func TestListItemsSeed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/x/polymer/web-space/seasons_archives_list" {
			http.NotFound(w, r)
			return
		}
		page := r.URL.Query().Get("page_num")
		arch := []map[string]any{}
		base := 1700000000
		if page == "1" {
			for i := 0; i < 30; i++ { // 第一页故意给旧→新，验证客户端排序
				arch = append(arch, map[string]any{"bvid": fmt.Sprintf("BV1p1%02d", i), "title": fmt.Sprintf("v%d", i), "pubdate": base + i})
			}
		} else if page == "2" {
			for i := 30; i < 45; i++ {
				arch = append(arch, map[string]any{"bvid": fmt.Sprintf("BV1p1%02d", i), "title": fmt.Sprintf("v%d", i), "pubdate": base + i})
			}
		}
		fmt.Fprint(w, envOK(map[string]any{"meta": map[string]any{"name": "合集", "total": 45}, "archives": arch}))
	})
	tg := &Target{Mid: 1, Kind: "season", ListID: 2, Total: 45}
	// 首次抓 10 条 = 最新 10 条
	items, err := c.ListItems(tg, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 10 {
		t.Fatalf("应截断到 10 条: %d", len(items))
	}
	if items[0].BVID != "BV1p144" || items[9].BVID != "BV1p135" {
		t.Fatalf("应取发布最新的 10 条: %+v", items)
	}
	// 全量：跨页
	all, err := c.ListItems(tg, 0, 0)
	if err != nil || len(all) != 45 {
		t.Fatalf("全量应 45 条: %d err=%v", len(all), err)
	}
	if all[0].PubDate < all[len(all)-1].PubDate {
		t.Fatal("排序应新→旧")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		`a/b\c:d*e?f"g<h>i|j`: "a b c d e f g h i j",
		"  多  空格 ":           "多 空格",
		"...危险..":             "危险",
		"":                    "unnamed",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Fatalf("SanitizeName(%q)=%q want %q", in, got, want)
		}
	}
	if n := SanitizeName(strings.Repeat("长", 100)); len([]rune(n)) != 80 {
		t.Fatalf("超长未截断: %d", len([]rune(n)))
	}
}

// TestFileNameFor 文件名=发布日期_标题（TODO 4 用户口径，日期取本地时区）；
// 非法 pubdate 回落 unknown。
func TestFileNameFor(t *testing.T) {
	if got := FileNameFor(1767225600, `第1话:出发/上路`); got != "20260101_第1话 出发 上路.m4a" {
		t.Fatalf("日期命名异常: %q", got)
	}
	if got := FileNameFor(0, "无日期"); got != "unknown_无日期.m4a" {
		t.Fatalf("pubdate 非法应 unknown: %q", got)
	}
}

// TestResolveTargetUploads 投稿页链接（/upload/video）解析为 uploads 形态：
// 首页动态可达即通过；合集行不受影响。
func TestResolveTargetUploads(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/frontend/finger/spi":
			fmt.Fprint(w, envOK(map[string]string{"b_3": "b3", "b_4": "b4"}))
		case "/x/web-interface/nav":
			fmt.Fprint(w, envOK(map[string]any{"wbi_img": map[string]string{
				"img_url": "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
				"sub_url": "https://i0.hdslb.com/bfs/wbi/4932ca06026ee967735629675208361b.png",
			}}))
		case "/x/polymer/web-dynamic/v1/feed/space":
			if r.URL.Query().Get("host_mid") != "24490535" {
				t.Errorf("host_mid 参数异常: %s", r.URL.Query().Get("host_mid"))
			}
			if r.URL.Query().Get("offset") == "123" { // 已到 mock 末页：终止
				fmt.Fprint(w, envOK(map[string]any{"items": []any{}, "has_more": false, "offset": ""}))
				return
			}
			fmt.Fprint(w, envOK(map[string]any{
				"items": []map[string]any{
					{"type": "DYNAMIC_TYPE_AV", "modules": map[string]any{
						"module_author": map[string]any{"pub_ts": "1767225600"},
						"module_dynamic": map[string]any{"major": map[string]any{
							"archive": map[string]any{"bvid": "BV1up1", "title": "投稿1"}},
						},
					}},
					{"type": "DYNAMIC_TYPE_FORWARD", "modules": map[string]any{
						"module_dynamic": map[string]any{"major": map[string]any{}},
					}},
				},
				"has_more": true, "offset": "123",
			}))
		default:
			t.Errorf("未预期的接口: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	tg, err := c.ResolveTarget("勾手老大爷邓肯", "https://space.bilibili.com/24490535/upload/video")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Kind != "uploads" || tg.Mid != 24490535 || tg.ListID != 0 || tg.Title != "投稿" {
		t.Fatalf("uploads 解析异常: %+v", tg)
	}
	// ListItems（uploads 分流）：过滤非视频动态、发布时间落库
	items, err := c.ListItems(tg, 10, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("uploads 列表应过滤非视频动态得 1 条: %d err=%v", len(items), err)
	}
	if items[0].BVID != "BV1up1" || items[0].PubDate != 1767225600 {
		t.Fatalf("条目解析异常: %+v", items[0])
	}
}

// TestListUploadsPaging 动态流翻页（offset 语义）+ 首页空抖动重试 + maxItems 截断。
func TestListUploadsPaging(t *testing.T) {
	emptyFirst := 0 // 置 1 时首页返回一次空页模拟抖动
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/frontend/finger/spi":
			fmt.Fprint(w, envOK(map[string]string{"b_3": "b3", "b_4": "b4"}))
		case "/x/web-interface/nav":
			fmt.Fprint(w, envOK(map[string]any{"wbi_img": map[string]string{
				"img_url": "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
				"sub_url": "https://i0.hdslb.com/bfs/wbi/4932ca06026ee967735629675208361b.png",
			}}))
		case "/x/polymer/web-dynamic/v1/feed/space":
			off := r.URL.Query().Get("offset")
			if emptyFirst > 0 && off == "" {
				emptyFirst--
				fmt.Fprint(w, envOK(map[string]any{"items": []any{}, "has_more": false, "offset": ""}))
				return
			}
			arch := func(i int, bv string) map[string]any {
				return map[string]any{"type": "DYNAMIC_TYPE_AV", "modules": map[string]any{
					"module_author": map[string]any{"pub_ts": fmt.Sprint(1767225600 + i)},
					"module_dynamic": map[string]any{"major": map[string]any{
						"archive": map[string]any{"bvid": bv, "title": fmt.Sprintf("v%d", i)}},
					},
				}}
			}
			if off == "" { // 第一页 3 条，旧→新
				fmt.Fprint(w, envOK(map[string]any{"items": []any{arch(0, "BVA"), arch(1, "BVB"), arch(2, "BVC")}, "has_more": true, "offset": "o2"}))
				return
			}
			// 第二页 2 条 + 一条转发
			fwd := map[string]any{"type": "DYNAMIC_TYPE_FORWARD", "modules": map[string]any{
				"module_dynamic": map[string]any{"major": map[string]any{}},
			}}
			fmt.Fprint(w, envOK(map[string]any{"items": []any{arch(3, "BVD"), arch(4, "BVE"), fwd}, "has_more": false, "offset": ""}))
		default:
			t.Errorf("未预期的接口: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	tg := &Target{Mid: 7, Kind: "uploads", Title: "投稿"}

	// 抖动重试：首页先空一次，重试后正常全量 5 条（转发已滤）
	emptyFirst = 1
	all, err := c.listUploads(tg, 0, 0)
	if err != nil || len(all) != 5 {
		t.Fatalf("翻页全量应 5 条（抖动重试后）: %d err=%v", len(all), err)
	}
	if all[0].BVID != "BVE" || all[4].BVID != "BVA" {
		t.Fatalf("应按发布时间新→旧: %+v", all)
	}
	// 首抓 10 条语义 = 全量截断到最新 3 条
	seed, err := c.listUploads(tg, 3, 0)
	if err != nil || len(seed) != 3 || seed[0].BVID != "BVE" {
		t.Fatalf("maxItems 应取最新 3 条: %+v err=%v", seed, err)
	}
	// 无抖动路径不受影响
	emptyFirst = 0
	if all, err := c.listUploads(tg, 0, 0); err != nil || len(all) != 5 {
		t.Fatalf("无抖动全量应 5 条: %d err=%v", len(all), err)
	}
}

// TestListUploadsWatermark 水位停页（7）：整页都比水位旧即不再翻后续页——
// 不回填旧视频，也不为旧视频白打请求。
func TestListUploadsWatermark(t *testing.T) {
	page3Hits := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/frontend/finger/spi":
			fmt.Fprint(w, envOK(map[string]string{"b_3": "b3", "b_4": "b4"}))
		case "/x/web-interface/nav":
			fmt.Fprint(w, envOK(map[string]any{"wbi_img": map[string]string{
				"img_url": "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png",
				"sub_url": "https://i0.hdslb.com/bfs/wbi/4932ca06026ee967735629675208361b.png",
			}}))
		case "/x/polymer/web-dynamic/v1/feed/space":
			arch := func(i int, bv string) map[string]any {
				return map[string]any{"type": "DYNAMIC_TYPE_AV", "modules": map[string]any{
					"module_author": map[string]any{"pub_ts": fmt.Sprint(1767225600 + i)},
					"module_dynamic": map[string]any{"major": map[string]any{
						"archive": map[string]any{"bvid": bv, "title": fmt.Sprintf("v%d", i)}},
					},
				}}
			}
			switch r.URL.Query().Get("offset") {
			case "":
				fmt.Fprint(w, envOK(map[string]any{"items": []any{arch(40, "BVN1"), arch(39, "BVN2")}, "has_more": true, "offset": "o2"}))
			case "o2": // 新旧交界：一条新、一条旧
				fmt.Fprint(w, envOK(map[string]any{"items": []any{arch(30, "BVN3"), arch(10, "BVO1")}, "has_more": true, "offset": "o3"}))
			case "o3":
				page3Hits++ // 收到才知全旧：请求一次必然发生，断言点在之后不再翻
				fmt.Fprint(w, envOK(map[string]any{"items": []any{arch(5, "BVO2"), arch(4, "BVO3")}, "has_more": true, "offset": "o4"}))
			default:
				page3Hits += 100 // o4 及以后不应被请求
				fmt.Fprint(w, envOK(map[string]any{"items": []any{}, "has_more": false, "offset": ""}))
			}
		default:
			t.Errorf("未预期的接口: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	tg := &Target{Mid: 7, Kind: "uploads", Title: "投稿"}
	// 水位=1767225600+10（BVO1 同值）：o2 页含 ≤水位 条目（全页无更新的？N3=30>10 继续）……
	// o2 页有 BVN3(30)>水位 → 不停；o3 页全旧(5,4<10) → 停，第三页不应被请求
	items, err := c.listUploads(tg, 0, 1767225600+10)
	if err != nil {
		t.Fatal(err)
	}
	if page3Hits != 1 {
		t.Fatalf("过水位页请求一次后应停页（不再翻 o4）: hits=%d", page3Hits)
	}
	// 返回含水位之上的条目；停页页条目可随页返回（水位逐条过滤在 agent 侧做），
	// 关键是 o3 之后不再白翻
	bvs := map[string]bool{}
	for _, it := range items {
		bvs[it.BVID] = true
	}
	if !bvs["BVN1"] || !bvs["BVN2"] || !bvs["BVN3"] {
		t.Fatalf("应含水位之上的条目: %v", bvs)
	}
}
