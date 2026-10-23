package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// tv_follows 追剧（548）口径钉死：类型校验、type+tmdb_id 唯一、待查窗口
// （启用+未完结，5 号口径起电影一并轮询）、进度回写与在库回写互不覆盖。
func TestTVFollows(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 校验
	if err := s.CreateTVFollow(&TVFollow{Type: "anime", TmdbID: 1, Name: "x"}); err == nil {
		t.Fatal("非法类型应报错")
	}
	if err := s.CreateTVFollow(&TVFollow{Type: TVFollowTV, TmdbID: 0, Name: "x"}); err == nil {
		t.Fatal("缺 TMDB ID 应报错")
	}
	if err := s.CreateTVFollow(&TVFollow{Type: TVFollowTV, TmdbID: 1399, Name: " 权力的游戏 "}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTVFollow(&TVFollow{Type: TVFollowTV, TmdbID: 1399, Name: "权游"}); !errors.Is(err, ErrTVFollowExists) {
		t.Fatalf("同 type+tmdb_id 重复应报 ErrTVFollowExists，得到 %v", err)
	}
	if err := s.CreateTVFollow(&TVFollow{Type: TVFollowMovie, TmdbID: 1399, Name: "同名 ID 电影"}); err != nil {
		t.Fatalf("电影与剧 ID 空间独立，不冲突（%v）", err)
	}

	list, err := s.ListTVFollows()
	if err != nil || len(list) != 2 {
		t.Fatalf("应有 2 条，得到 %d（err=%v）", len(list), err)
	}
	if list[0].Name != "同名 ID 电影" {
		t.Fatal("新添加应排前（id DESC）")
	}
	tv := &list[1]
	if tv.Name != "权力的游戏" {
		t.Fatal("名称应去首尾空白")
	}

	// 待查窗口（563 固定时刻）：新条目 last_queued_at=0 立即可查；5 号口径起
	// 电影一并轮询（追踪流媒体上线）
	due, err := s.DueTVFollows(time.Now().Unix())
	if err != nil || len(due) != 2 {
		t.Fatalf("剧与电影都应进待查窗口，得到 %d（err=%v）", len(due), err)
	}
	s.MarkTVFollowQueued(list[0].ID)
	s.MarkTVFollowQueued(tv.ID)
	due, _ = s.DueTVFollows(time.Now().Unix())
	if len(due) != 0 {
		t.Fatal("入队打标后本窗口不应再查")
	}

	// 进度回写：在库字段原样回填（本次未检查）
	err = s.UpdateTVFollowDetail(tv.ID, &TVFollowDetailUpdate{
		Status: "Ended", Seasons: 8, Episodes: 73,
		LastSeason: 8, LastEpisode: 6, LastAirDate: "2019-05-19",
		InEmbyChecked: true, InEmby: true, EmbyItemName: "Game of Thrones", EmbyCheckedAt: 1000,
		Done: true, NotifyEpisodeKey: "S8E6", LastCheckedAt: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTVFollow(tv.ID)
	if !got.Done || !got.InEmby || got.EmbyItemName != "Game of Thrones" || got.LastEpisode != 6 {
		t.Fatalf("回写不符： %+v", got)
	}

	// 错误只记 last_error，不动进度基线
	s.SetTVFollowError(tv.ID, "出口超时")
	got, _ = s.GetTVFollow(tv.ID)
	if got.LastError != "出口超时" || got.LastEpisode != 6 || got.Done != true {
		t.Fatalf("错误不应覆盖进度： %+v", got)
	}

	// 在库单独回写（549 手动检查）
	s.SetTVFollowEmby(tv.ID, false, "", 3000)
	got, _ = s.GetTVFollow(tv.ID)
	if got.InEmby || got.EmbyCheckedAt != 3000 {
		t.Fatalf("在库回写不符： %+v", got)
	}

	// 启停与删除
	if err := s.SetTVFollowEnabled(tv.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTVFollow(tv.ID)
	if got.Enabled {
		t.Fatal("应已停用")
	}
	if err := s.SetTVFollowEnabled(999, true); !errors.Is(err, ErrTVFollowNotFound) {
		t.Fatalf("不存在应报 ErrTVFollowNotFound，得到 %v", err)
	}
	if err := s.DeleteTVFollow(list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTVFollow(list[0].ID); !errors.Is(err, ErrTVFollowNotFound) {
		t.Fatal("删除后应不存在")
	}
}
