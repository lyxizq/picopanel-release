package store

import (
	"database/sql"
	"errors"
	"testing"
)

// TestCountdownsIsolation 家庭成员数据隔离（2026-11-03）：倒数日按 user_id 空间
// 隔离，admin(0) 与成员互不可见；跨空间编辑按「不存在」（ErrNoRows）处理；
// 同名在两空间可各自存在（UNIQUE(user_id,name)）。
func TestCountdownsIsolation(t *testing.T) {
	s := openTestStore(t)
	const famUID = 7

	// 各建各的：admin 与成员各一条同名倒数日
	cid, err := s.CreateCountdown(&Countdown{Name: "生日", Kind: "yearly", Date: "2030-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	fid, err := s.CreateCountdown(&Countdown{Name: "生日", Kind: "once", Date: "2030-05-01", UserID: famUID})
	if err != nil {
		t.Fatalf("成员空间同名倒数日应允许: %v", err)
	}

	// 列表互不可见
	if cs, _ := s.ListCountdownsFor(0); len(cs) != 1 || cs[0].Name != "生日" {
		t.Fatalf("admin 空间应只看到自己的倒数日: %+v", cs)
	}
	if cs, err := s.ListCountdownsFor(famUID); err != nil || len(cs) != 1 || cs[0].Date != "2030-05-01" {
		t.Fatalf("成员空间应只看到自己的倒数日: %+v %v", cs, err)
	}

	// 跨空间编辑按不存在处理
	if err := s.UpdateCountdown(&Countdown{ID: cid, UserID: famUID, Name: "劫持", Kind: "once", Date: "2030-01-01"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("跨空间编辑应报 ErrNoRows: %v", err)
	}
	if err := s.UpdateCountdown(&Countdown{ID: fid, UserID: 0, Name: "劫持", Kind: "once", Date: "2030-05-01"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("admin 请求改成员倒数日应报 ErrNoRows: %v", err)
	}
	// 空间内编辑正常
	if err := s.UpdateCountdown(&Countdown{ID: fid, UserID: famUID, Name: "生日改", Kind: "once", Date: "2030-05-01"}); err != nil {
		t.Fatalf("空间内编辑应成功: %v", err)
	}
	if cs, _ := s.ListCountdownsFor(famUID); cs[0].Name != "生日改" {
		t.Fatalf("空间内改名应生效: %+v", cs)
	}

	// 跨空间删除不生效（静默成功，不泄露存在性）
	if err := s.DeleteCountdown(cid, famUID); err != nil {
		t.Fatal(err)
	}
	if cs, _ := s.ListCountdownsFor(0); len(cs) != 1 {
		t.Fatalf("跨空间删除不应生效: %+v", cs)
	}
	// 本空间删除生效
	if err := s.DeleteCountdown(fid, famUID); err != nil {
		t.Fatal(err)
	}
	if cs, _ := s.ListCountdownsFor(famUID); len(cs) != 0 {
		t.Fatalf("本空间删除应生效: %+v", cs)
	}

	// 上限按空间各算各的（100 条/空间，此处只验证计数隔离）
	if n, _ := s.CountCountdowns(0); n != 1 {
		t.Fatalf("admin 空间计数应 1: %d", n)
	}
	if n, _ := s.CountCountdowns(famUID); n != 0 {
		t.Fatalf("成员空间计数应 0: %d", n)
	}
}
