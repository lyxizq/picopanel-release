package tvfollow

import (
	"strconv"
	"strings"
	"testing"

	"lz-monitor/internal/ptfetch"
	"lz-monitor/internal/server/store"
)

// PT 进度综合（566）口径钉死：标题解析各形态取最大；「全集」=完结推提醒+done；
// 进度 max 水位只进不退（无资源=无提醒）；agent 回传失败静默。

func TestParseTitleProgress(t *testing.T) {
	cases := []struct {
		title               string
		season, episode     int
		complete            bool
	}{
		{"蒸蒸日上 S01E08 2026 1080p WEB-DL H264", 1, 8, false},
		{"Show.Name.S02E15E16.1080p.WEB-DL", 2, 16, false},
		{"美人余 第28集 4K", 0, 28, false},
		{"Show EP12 1080p", 0, 12, false},
		{"把黑夜点燃 E03", 0, 3, false},
		{"玉簪秋 全集 合集打包", 0, 0, true},
		{"无可替代 S01E20 全集收录", 1, 20, true},
		{"随便一部没有集数信息的电影", 0, 0, false},
	}
	for _, c := range cases {
		s, e, comp := parseTitleProgress(c.title)
		if s != c.season || e != c.episode || comp != c.complete {
			t.Fatalf("%q → (%d,%d,%v)，期望 (%d,%d,%v)", c.title, s, e, comp, c.season, c.episode, c.complete)
		}
	}
}

func makePTTask(t *testing.T, st *store.Store, siteKey string, followID int64, body string) *store.NetTask {
	t.Helper()
	refKey := siteKey + ":" + strconv.FormatInt(followID, 10)
	id, err := st.CreateNetTask(PurposePT, refKey, 1, "https://pt.example/tor.php?search=x", "GET", nil, "", 20, true)
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



func TestConsumePTProgressAdvance(t *testing.T) {
	st := openTest(t)
	f := makeFollow(t, st)
	// 基线（TMDB S6E10）
	ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, tvDetailBody))

	// PT 站搜到 S6E12（大于 TMDB 基线）→ 推进 + 提醒
	task := makePTTask(t, st, "chdbits", f.ID, `<html>
	<a href="details.php?id=1" title="风骚律师 S06E12 1080p">x</a>
	<a href="details.php?id=2" title="风骚律师 S06E11 1080p">x</a>
	<a href="details.php?id=3" title="无关电影 2020">x</a></html>`)
	if err := ConsumePTResult(st, task); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetTVFollow(f.ID)
	if got.LastSeason != 6 || got.LastEpisode != 12 {
		t.Fatalf("PT 进度应推进到 S6E12： %+v", got)
	}
	flushNotify()
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("PT 推进应提醒 1 条，得到 %d 条", n)
	}

	// 同进度重复回传：不重复提醒
	if err := ConsumePTResult(st, makePTTask(t, st, "hdsky", f.ID, `<a href="details.php?id=9" title="风骚律师 S06E12">x</a>`)); err != nil {
		t.Fatal(err)
	}
	flushNotify()
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("同进度不应重复提醒，得到 %d 条", n)
	}

	// TMDB 回传旧进度（S6E10 时代的重复数据）：水位保护不回退、不提醒
	if err := ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, tvDetailBody)); err != nil {
		t.Fatal(err)
	}
	flushNotify()
	got, _ = st.GetTVFollow(f.ID)
	if got.LastEpisode != 12 {
		t.Fatalf("TMDB 回传不应回退 PT 水位： %+v", got)
	}
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("回退数据不应提醒，得到 %d 条", n)
	}
}

func TestConsumePTComplete(t *testing.T) {
	st := openTest(t)
	f := makeFollow(t, st)
	ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, tvDetailBody)) // 基线

	// 任一站标题含「全集」→ 完结 + done 停追
	if err := ConsumePTResult(st, makePTTask(t, st, "hhanclub", f.ID,
		`<a href="details.php?id=5" title="风骚律师 S01-S06 全集 收藏版">x</a>`)); err != nil {
		t.Fatal(err)
	}
	flushNotify()
	got, _ := st.GetTVFollow(f.ID)
	if !got.Done {
		t.Fatal("全集应标记完结")
	}
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("完结应提醒 1 条，得到 %d 条", n)
	}
	page, _ := st.ListMessages(MessageType, false, 1, 10)
	if !strings.Contains(page.Items[0].Content, "全集资源") {
		t.Fatalf("完结正文不符： %s", page.Items[0].Content)
	}
	// done 后 PT 回传不再处理（DueTVFollows 也已排除）
	if err := ConsumePTResult(st, makePTTask(t, st, "chdbits", f.ID,
		`<a href="details.php?id=6" title="风骚律师 S06E99">x</a>`)); err != nil {
		t.Fatal(err)
	}
	flushNotify()
	if n := countMessages(st, MessageType); n != 1 {
		t.Fatalf("done 后不应再提醒，得到 %d 条", n)
	}
}

func TestConsumePTNoResultAndFailure(t *testing.T) {
	st := openTest(t)
	f := makeFollow(t, st)
	ConsumeDetailResult(st, makeDoneTask(t, st, f.ID, tvDetailBody)) // 基线 S6E10

	// 无资源（空结果）：静默不提醒不动进度
	if err := ConsumePTResult(st, makePTTask(t, st, "chdbits", f.ID, `<html><body>没有找到匹配</body></html>`)); err != nil {
		t.Fatal(err)
	}
	// 回传失败：静默
	task := makeDoneTask(t, st, f.ID, "")
	_ = task
	failed, _ := st.CreateNetTask(PurposePT, "chdbits:"+strconv.FormatInt(f.ID, 10), 1, "https://pt.example/x", "GET", nil, "", 20, true)
	st.ClaimNetTasks(1, 10)
	st.FinishNetTask(failed, 0, "", "超时")
	ft, _ := st.GetNetTask(failed)
	if err := ConsumePTResult(st, ft); err != nil {
		t.Fatal(err)
	}
	flushNotify()
	if n := countMessages(st, MessageType); n != 0 {
		t.Fatalf("无资源/失败都不应提醒，得到 %d 条", n)
	}
	got, _ := st.GetTVFollow(f.ID)
	if got.LastEpisode != 10 || got.LastError != "" {
		t.Fatalf("PT 失败不应动基线或 last_error： %+v", got)
	}
}

func TestParseNexusSearchTitlesLimit(t *testing.T) {
	html := `<a href="details.php?id=1" title="Alpha-1">a</a><a href="details.php?id=2" title="Alpha-2">a</a>
	<a href="details.php?id=2" title="Alpha-2">a</a><a href="details.php?id=3">Beta-3</a>
	<a href="details.php?id=4" title="Alpha-4">a</a><a href="details.php?id=5" title="Alpha-5">a</a>
	<a href="details.php?id=6" title="Alpha-6">a</a>`
	got := ptfetch.ParseNexusSearchTitles(html, 5)
	if len(got) != 5 || got[0] != "Alpha-1" || got[1] != "Alpha-2" || got[2] != "Alpha-4" || strings.Contains(strings.Join(got, ","), "Beta-3") {
		t.Fatalf("应去重截前 5（重复丢弃，title 阶段已满不再走文本兜底）： %v", got)
	}
}
