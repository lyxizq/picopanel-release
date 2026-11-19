package store

import "testing"

// TestDeletePushSubByID 按行 ID 删除订阅：删后列表为空、同 endpoint 可重新订阅。
func TestDeletePushSubByID(t *testing.T) {
	s := openTestStore(t)

	sub := &PushSub{Endpoint: "https://push.example.com/a", P256DH: "k1", Auth: "v1", UA: "UA-1", CreatedAt: 100}
	if err := s.UpsertPushSub(sub); err != nil {
		t.Fatal(err)
	}
	sub2 := &PushSub{Endpoint: "https://push.example.com/b", P256DH: "k2", Auth: "v2", UA: "UA-2", CreatedAt: 200}
	if err := s.UpsertPushSub(sub2); err != nil {
		t.Fatal(err)
	}

	subs, err := s.ListPushSubs()
	if err != nil || len(subs) != 2 {
		t.Fatalf("应有 2 条: %d err=%v", len(subs), err)
	}

	// 按行 ID 删第一条
	if err := s.DeletePushSubByID(subs[0].ID); err != nil {
		t.Fatal(err)
	}
	subs, err = s.ListPushSubs()
	if err != nil || len(subs) != 1 || subs[0].Endpoint != "https://push.example.com/b" {
		t.Fatalf("删后应剩 b: %+v err=%v", subs, err)
	}
	var na int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions WHERE endpoint = ?`, "https://push.example.com/a").Scan(&na); err != nil || na != 0 {
		t.Fatalf("a 应已删除: n=%d err=%v", na, err)
	}

	// 删不存在的 ID 不报错
	if err := s.DeletePushSubByID(9999); err != nil {
		t.Fatalf("删不存在 ID 应幂等: %v", err)
	}

	// 同 endpoint 可重新订阅（覆盖写入恢复）
	sub.CreatedAt = 300
	if err := s.UpsertPushSub(sub); err != nil {
		t.Fatal(err)
	}
	subs, _ = s.ListPushSubs()
	if len(subs) != 2 {
		t.Fatalf("重新订阅后应 2 条: %d", len(subs))
	}
}
