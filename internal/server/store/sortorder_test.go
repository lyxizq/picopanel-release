package store

import "testing"

// TestReorderSortOrder 服务监控页四类模块的拖拽排序：新建追加尾部（MAX+1）、Reorder 按 ids
// 写 1..n、列表按 sort 序；证书未排过的按域名回落。
func TestReorderSortOrder(t *testing.T) {
	s := openTestStore(t)

	var sv []int64
	for _, n := range []string{"a", "b", "c"} {
		id, err := s.CreateSvcServer(&SvcServer{Name: n, Host: n + ".local", Interval: 600, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		sv = append(sv, id)
	}
	if err := s.ReorderSvcServers([]int64{sv[2], sv[0], sv[1]}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSvcServers()
	if err != nil || len(list) != 3 || list[0].ID != sv[2] || list[1].ID != sv[0] || list[2].ID != sv[1] {
		t.Fatalf("服务器排序不符: %v %+v", err, list)
	}

	var gh []int64
	for _, n := range []string{"x", "y"} {
		id, err := s.CreateGhRepo(&GhRepo{Owner: "o", Repo: n, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		gh = append(gh, id)
	}
	if err := s.ReorderGhRepos([]int64{gh[1], gh[0]}); err != nil {
		t.Fatal(err)
	}
	repos, err := s.ListGhRepos()
	if err != nil || len(repos) != 2 || repos[0].ID != gh[1] {
		t.Fatalf("仓库排序不符: %v %+v", err, repos)
	}

	c1, _ := s.CreateCert("b.example.com", 443, "")
	c2, _ := s.CreateCert("a.example.com", 443, "")
	certs, _ := s.ListCerts()
	if len(certs) != 2 || certs[0].ID != c1 { // 新建追加尾部：先建的在前（不再按域名）
		t.Fatalf("证书默认序应为创建序: %+v", certs)
	}
	if err := s.ReorderCerts([]int64{c2, c1}); err != nil {
		t.Fatal(err)
	}
	certs, _ = s.ListCerts()
	if certs[0].ID != c2 {
		t.Fatalf("证书重排不符: %+v", certs)
	}

	// 存量回填幂等：把 sort 清零再回填 → 按 id 序 1..n，再次回填不动
	if _, err := s.db.Exec(`UPDATE svc_servers SET sort = 0`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := backfillSort(s.db, "svc_servers"); err != nil {
			t.Fatal(err)
		}
	}
	list, _ = s.ListSvcServers()
	if list[0].ID != sv[0] || list[2].ID != sv[2] {
		t.Fatalf("回填后应为创建序: %+v", list)
	}
	var mx int
	_ = s.db.QueryRow(`SELECT MAX(sort) FROM svc_servers`).Scan(&mx)
	if mx != 3 {
		t.Fatalf("回填应编 1..3，MAX=%d", mx)
	}
}
