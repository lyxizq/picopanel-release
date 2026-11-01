package tvfollow

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// 追剧消费链路口径钉死（548；5 号口径改版 2026-10-18）：TMDB 解析字段映射；
// 首次查询只建基线不推送；剧集新集不推送（进度照常回写）；Ended/Canceled 推
// 一次完结后 done 停追；电影平台从无到有推一条上线；查询失败只记 last_error
// 不动基线；消费幂等（重复消费不重复推送）。

func openTest(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	initNotify(s)        // 558：绑定聚合出口（不绑则 queueNotify 退化为直发不落库）
	resetNotifyForTest() // 清全局缓冲：上一用例残留的行会污染本用例的 flush
	return s
}

const tvDetailBody = `{
  "status": "Returning Series", "name": "风骚律师", "original_name": "Better Call Saul",
  "number_of_seasons": 6, "number_of_episodes": 63,
  "last_episode_to_air": {"season_number": 6, "episode_number": 10, "name": "Nippy", "air_date": "2022-08-01"},
  "next_episode_to_air": {"season_number": 6, "episode_number": 11, "air_date": "2022-08-08"},
  "credits": {"cast": [
    {"name": "鲍勃·奥登科克"}, {"name": "蕾亚·塞洪"}, {"name": "乔纳森·班克斯"},
    {"name": "冈娜·陈"}, {"name": "第五位不该出现"}
  ]},
  "watch/providers": {"results": {
    "US": {"flatrate": [{"provider_name": "Netflix"}]},
    "CN": {"flatrate": [{"provider_name": "爱奇艺"}, {"provider_name": "腾讯视频"}, {"provider_name": "重复不该出现"}, {"provider_name": "爱奇艺"}], "free": [{"provider_name": "哔哩哔哩"}], "rent": [{"provider_name": "优酷"}], "buy": [{"provider_name": "腾讯视频"}]}
  }}
}`

const endedBody = `{
  "status": "Ended", "number_of_seasons": 6, "number_of_episodes": 63,
  "last_episode_to_air": {"season_number": 6, "episode_number": 13, "name": "Saul Gone", "air_date": "2022-08-15"}
}`

const movieBodyNoPlatforms = `{"status":"Released","title":"醉梦","release_date":"2026-01-01","runtime":125,"watch/providers":{"results":{}}}`
const movieBodyPlatforms = `{"status":"Released","title":"醉梦","release_date":"2026-01-01","runtime":125,"watch/providers":{"results":{"CN":{"flatrate":[{"provider_name":"爱奇艺"}],"free":[{"provider_name":"哔哩哔哩"}]}}}}`

func makeFollow(t *testing.T, st *store.Store) *store.TVFollow {
	t.Helper()
	f := &store.TVFollow{Type: store.TVFollowTV, TmdbID: 60059, Name: "风骚律师", OriginalName: "Better Call Saul"}
	if err := st.CreateTVFollow(f); err != nil {
		t.Fatal(err)
	}
	return f
}

// makeDoneTask 造一个已回传的 detail 任务（refKey=条目 ID）。
func makeDoneTask(t *testing.T, st *store.Store, followID int64, body string) *store.NetTask {
	t.Helper()
	id, err := st.CreateNetTask(PurposeDetail, strconv.FormatInt(followID, 10), 1, "https://api.themoviedb.org/3/tv/x", "GET", nil, "", 20, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNetTasks(1, 10); err != nil {
		t.Fatal(err)
	}
	if first, err := st.FinishNetTask(id, 200, body, ""); err != nil || !first {
		t.Fatalf("回传应成功（err=%v first=%v）", err, first)
	}
	task, err := st.GetNetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func countMessages(st *store.Store, typ string) int {
	page, err := st.ListMessages(typ, false, 1, 50)
	if err != nil {
		return -1
	}
	return page.Total
}

func TestConsumeFirstCheckBaseline(t *testing.T) {
	st := openTest(t)
	f := makeFollow(t, st)
	task := makeDoneTask(t, st, f.ID, tvDetailBody)

	if err := ConsumeDetailResult(st, task); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetTVFollow(f.ID)
	if got.LastSeason != 6 || got.LastEpisode != 10 || got.Status != "Returning Series" {
		t.Fatalf("基线回写不符： %+v", got)
	}
	if got.Cast != "鲍勃·奥登科克/蕾亚·塞洪/乔纳森·班克斯/冈娜·陈" {
		t.Fatalf("主演应取前 4 位： %q", got.Cast)
	}
	if got.Platforms != "爱奇艺/腾讯视频/重复不该出现/哔哩哔哩/优酷" {
		t.Fatalf("平台应随基线回写： %q", got.Platforms)
	}
	if got.NotifyEpisodeKey != "S6E10" || got.LastCheckedAt == 0 {
		t.Fatalf("打标不符：key=%q checked=%d", got.NotifyEpisodeKey, got.LastCheckedAt)
	}
	if n := countMessages(st, MessageType); n != 0 {
		t.Fatalf("首次查询只建基线不推送，得到 %d 条", n)
	}
	if got.InEmby || got.EmbyCheckedAt != 0 {
		t.Fatalf("未配 Emby 不应在库检查： %+v", got)
	}
}

func TestConsumeNewEpisodeNoPush(t *testing.T) {
	st := openTest(t)
	f := makeFollow(t, st)
	ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, tvDetailBody)) // 基线 S6E10

	task := makeDoneTask(t, st, f.ID, `{"status":"Returning Series","number_of_seasons":6,"number_of_episodes":64,
		"last_episode_to_air":{"season_number":6,"episode_number":11,"name":"Breaking Bad","air_date":"2022-08-08"}}`)
	if err := ConsumeDetailResult(st, task); err != nil {
		t.Fatal(err)
	}
	flushNotify()
	if n := countMessages(st, MessageType); n != 0 {
		t.Fatalf("新集不应推送（5 号口径），得到 %d 条", n)
	}
	got, _ := st.GetTVFollow(f.ID)
	if got.LastEpisode != 11 || got.NotifyEpisodeKey != "S6E11" {
		t.Fatalf("不推送但进度仍应更新到 S6E11： %+v", got)
	}

	// 同集重复回传（重复消费）：同样不推送
	again := makeDoneTask(t, st, f.ID, `{"status":"Returning Series","number_of_seasons":6,"number_of_episodes":64,
		"last_episode_to_air":{"season_number":6,"episode_number":11,"air_date":"2022-08-08"}}`)
	if err := ConsumeDetailResult(st, again); err != nil {
		t.Fatal(err)
	}
	flushNotify()
	if n := countMessages(st, MessageType); n != 0 {
		t.Fatalf("同进度重复消费不应推送，得到 %d 条", n)
	}
}

func TestConsumeEndedPush(t *testing.T) {
	st := openTest(t)
	f := makeFollow(t, st)
	ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, tvDetailBody)) // 基线

	if err := ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, endedBody)); err != nil {
		t.Fatal(err)
	}
	flushNotify()
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("完结应推 1 条（合并最终集，不另推新集），得到 %d 条", n)
	}
	msgs, _ := st.ListMessages(MessageType, false, 1, 10)
	if msgs.Items[0].Title != "追剧完结：风骚律师" {
		t.Fatalf("完结标题不符： %s", msgs.Items[0].Title)
	}
	got, _ := st.GetTVFollow(f.ID)
	if !got.Done {
		t.Fatal("完结应打 done 停追")
	}
	due, _ := st.DueTVFollows(checkWindowStart(time.Now()))
	if len(due) != 0 {
		t.Fatalf("done 条目不应再进待查窗口，得到 %d", len(due))
	}
}

func TestConsumeFailureKeepsBaseline(t *testing.T) {
	st := openTest(t)
	f := makeFollow(t, st)
	ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, tvDetailBody)) // 基线 S6E10

	// 查询失败：只记 last_error，不动基线、不推送
	if err := ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, "")); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetTVFollow(f.ID)
	if got.LastEpisode != 10 || got.LastError == "" {
		t.Fatalf("失败应记错误不动基线： %+v", got)
	}
	if n := countMessages(st, MessageType); n != 0 {
		t.Fatalf("失败不应推送，得到 %d 条", n)
	}
}

func TestAggregatedNotify(t *testing.T) {
	st := openTest(t)
	// 剧完结 + 电影上线各一行：回传逐个到达（consume 两次），提醒进聚合缓冲不落库
	f1 := makeFollow(t, st)
	ConsumeDetailResult(st, makeDoneTask(t, st, f1.ID, tvDetailBody)) // 基线
	ConsumeDetailResult(st, makeDoneTask(t, st, f1.ID, endedBody))    // 完结行

	f2 := makeMovieFollow(t, st, 99001, "醉梦")
	ConsumeDetailResult(st, makeDoneTask(t, st, f2.ID, movieBodyNoPlatforms)) // 基线（平台空）
	ConsumeDetailResult(st, makeDoneTask(t, st, f2.ID, movieBodyPlatforms))   // 上线行

	if n := countMessages(st, MessageType); n != 0 {
		t.Fatalf("聚合窗内不应落库，得到 %d 条", n)
	}
	flushNotify()
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("flush 后应合成 1 条，得到 %d 条", n)
	}
	page, _ := st.ListMessages(MessageType, false, 1, 10)
	if page.Items[0].Title != "追剧提醒：1 部完结、1 部上线" {
		t.Fatalf("聚合标题不符： %s", page.Items[0].Title)
	}
	body := page.Items[0].Content
	if !strings.Contains(body, "「风骚律师」已完结") || !strings.Contains(body, "「醉梦」已上线流媒体：爱奇艺/哔哩哔哩") {
		t.Fatalf("聚合正文缺剧行： %s", body)
	}
	if !strings.HasPrefix(body, "· ") {
		t.Fatalf("聚合正文应逐行列表： %s", body)
	}

	// 再次 flush 幂等（空缓冲不发）
	flushNotify()
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("空 flush 不应新增，得到 %d 条", n)
	}
}

// makeMovieFollow 建电影条目（上线推送链路用）。
func makeMovieFollow(t *testing.T, st *store.Store, tmdbID int64, name string) *store.TVFollow {
	t.Helper()
	f := &store.TVFollow{Type: store.TVFollowMovie, TmdbID: tmdbID, Name: name}
	if err := st.CreateTVFollow(f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestConsumeMovieStreamingPush(t *testing.T) {
	st := openTest(t)
	f := makeMovieFollow(t, st, 12, "卧虎藏龙")

	// 基线首查无平台（未上线）：不推，基线落空
	ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, movieBodyNoPlatforms))
	flushNotify()
	if n := countMessages(st, MessageType); n != 0 {
		t.Fatalf("首查建基线不应推送，得到 %d 条", n)
	}

	// 平台从无到有：推一条上线（正文带平台清单）
	ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, movieBodyPlatforms))
	flushNotify()
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("上线应推 1 条，得到 %d 条", n)
	}
	msgs, _ := st.ListMessages(MessageType, false, 1, 10)
	if msgs.Items[0].Title != "追剧上线：卧虎藏龙" {
		t.Fatalf("上线标题不符： %s", msgs.Items[0].Title)
	}
	if !strings.Contains(msgs.Items[0].Content, "爱奇艺/哔哩哔哩") {
		t.Fatalf("上线正文应带平台： %s", msgs.Items[0].Content)
	}

	// 推送后平台基线非空：持续轮询不重推
	ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, movieBodyPlatforms))
	flushNotify()
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("平台无非空跳变不应再推，得到 %d 条", n)
	}
	got, _ := st.GetTVFollow(f.ID)
	if got.Done {
		t.Fatal("电影不打 done，应保持轮询")
	}
	due, _ := st.DueTVFollows(checkWindowStart(time.Now()))
	if len(due) != 1 || due[0].Type != store.TVFollowMovie {
		t.Fatalf("电影应保持进待查窗口，得到 %d", len(due))
	}

	// 添加时已上线的电影（首查基线即非空）：永不推上线
	f2 := makeMovieFollow(t, st, 13, "已上线电影")
	ConsumeDetailResult(st, makeDoneTask(t, st, f2.ID, movieBodyPlatforms))
	flushNotify()
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("首查平台已非空不应推送，得到 %d 条", n)
	}
}

func TestParseSearchAndDetail(t *testing.T) {
	tvBody := `{"results":[{"id":60059,"name":"风骚律师","original_name":"Better Call Saul",
		"first_air_date":"2015-02-08","poster_path":"/a.jpg","overview":"d","vote_average":8.7}]}`
	cs, err := ParseSearch(store.TVFollowTV, tvBody)
	if err != nil || len(cs) != 1 {
		t.Fatalf("tv 搜索解析失败： %v", err)
	}
	if cs[0].Name != "风骚律师" || cs[0].Year != "2015" || cs[0].Rating != 8.7 {
		t.Fatalf("tv 候选字段不符： %+v", cs[0])
	}
	mvBody := `{"results":[{"id":12,"title":"卧虎藏龙","original_title":"Crouching Tiger","release_date":"2000-07-06"}]}`
	ms, err := ParseSearch(store.TVFollowMovie, mvBody)
	if err != nil || len(ms) != 1 || ms[0].Name != "卧虎藏龙" || ms[0].Year != "2000" {
		t.Fatalf("movie 搜索解析不符： %+v（%v）", ms, err)
	}

	d, err := ParseTVDetail(tvDetailBody)
	if err != nil {
		t.Fatal(err)
	}
	if d.LastSeason != 6 || d.LastEpisode != 10 || d.NextEpisode != 11 || d.NotifyEpisodeKey != "S6E10" || d.EpisodeName != "Nippy" {
		t.Fatalf("detail 解析不符： %+v", d)
	}
	if d.Cast != "鲍勃·奥登科克/蕾亚·塞洪/乔纳森·班克斯/冈娜·陈" {
		t.Fatalf("detail 主演解析不符： %q", d.Cast)
	}
	if d.Platforms != "爱奇艺/腾讯视频/重复不该出现/哔哩哔哩/优酷" {
		t.Fatalf("播出平台应 CN 优先四类去重： %q", d.Platforms)
	}
	if c := ParseCredits(`{"cast":[{"name":"张三"},{"name":""},{"name":"李四"}]}`); c != "张三/李四" {
		t.Fatalf("credits 解析应跳过空名： %q", c)
	}
	// 575：CN/US 都空时遍历其余区（JustWatch 把国内平台挂在非 CN 区码下的醉梦案例）
	if p := extractWatchProviders(`{"watch/providers":{"results":{"DE":{"flatrate":[{"provider_name":"腾讯视频"}]}}}}`); p != "腾讯视频" {
		t.Fatalf("区回退应取到非 CN/US 区的平台： %q", p)
	}
	if p := extractWatchProviders(`{"watch/providers":{"results":{}}}`); p != "" {
		t.Fatalf("全空应返回空串： %q", p)
	}
	if c := ParseCredits(`not json`); c != "" {
		t.Fatalf("坏响应应得空串： %q", c)
	}
	if u := DetailURL("tv", 60059, "k"); !strings.Contains(u, "append_to_response=credits") {
		t.Fatalf("detail 应附 credits： %s", u)
	}
	if u := CreditsURL("movie", 12, "k"); !strings.Contains(u, "/movie/12/credits?") {
		t.Fatalf("credits URL 不符： %s", u)
	}
	if !tvEnded("Ended") || !tvEnded("Canceled") || tvEnded("Returning Series") {
		t.Fatal("完结判定不符")
	}
	if _, err := ParseTVDetail(`not json`); err == nil {
		t.Fatal("坏响应应报错")
	}
}

func TestSearchRefKeyReadyAndNoConfig(t *testing.T) {
	st := openTest(t)
	if Ready(st) {
		t.Fatal("未配置不应 Ready")
	}
	if err := SetTmdbKey(st, "k-test"); err != nil {
		t.Fatal(err)
	}
	if err := SetEgressServerID(st, 3); err != nil {
		t.Fatal(err)
	}
	if !Ready(st) || EgressServerID(st) != 3 || TmdbKey(st) != "k-test" {
		t.Fatal("设置读写不符")
	}
	if SearchRefKey("tv", " 权游 ") != "tv:权游" {
		t.Fatal("ref key 应含类型并去空白")
	}
	// 配置不齐时 sweep 不入队（不报错即可）
	sweep(st)
	if _, err := st.ActiveNetTask(PurposeDetail, "1"); err != nil && !errors.Is(err, store.ErrNetTaskNotFound) {
		t.Fatal(err)
	}
}

// TestLastSweepAtStampedOnDueSweep 定时抓取时刻（63）：只有真正入队了待查条目
// 的轮次才打 tv_last_sweep_at，空轮与未配置不打标。
func TestLastSweepAtStampedOnDueSweep(t *testing.T) {
	st := openTest(t)
	if err := SetTmdbKey(st, "k-test"); err != nil {
		t.Fatal(err)
	}
	if err := SetEgressServerID(st, 3); err != nil {
		t.Fatal(err)
	}
	sweep(st) // 无待查条目：不打标
	if LastSweepAt(st) != 0 {
		t.Fatalf("无待查不应打标: %d", LastSweepAt(st))
	}
	f := &store.TVFollow{Type: store.TVFollowTV, TmdbID: 100, Name: "测试剧"}
	if err := st.CreateTVFollow(f); err != nil {
		t.Fatal(err)
	}
	sweep(st) // 有待查：入队并打标
	if LastSweepAt(st) <= 0 {
		t.Fatalf("入队轮应打标: %d", LastSweepAt(st))
	}
}
