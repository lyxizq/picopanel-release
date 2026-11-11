package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// net_tasks 通用中转任务（548）口径钉死：purpose+ref_key 活跃唯一、领取原子
// 租出+URL 解密、回传幂等（done 后重复丢弃）、lease 超时回收 attempt≤2、
// consumed_at 打标只允许首次消费、换绑/删除出口作废 pending、终态清理。
func TestNetTasks(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 入队 + 活跃查重
	id, err := s.CreateNetTask("tvfollow_api", "q=权游", 7, "https://api.themoviedb.org/3/search/tv?query=x", "GET", map[string]string{"Accept": "application/json"}, "", 15, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNetTask("tvfollow_api", "q=权游", 7, "https://api.themoviedib.org/x", "", nil, "", 0, false); !errors.Is(err, ErrNetTaskActive) {
		t.Fatalf("同 purpose+ref_key 重复入队应报 ErrNetTaskActive，得到 %v", err)
	}
	// timeout/method 缺省回落
	id2, err := s.CreateNetTask("tvfollow_detail", "1", 7, "https://api.themoviedb.org/3/tv/1", "", nil, "", 0, false)
	if err != nil {
		t.Fatal(err)
	}

	// 领取：原子租出 + URL 解密 + headers 还原
	tasks, err := s.ClaimNetTasks(7, 10)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("领取应得 2 个任务，得到 %d 个（err=%v）", len(tasks), err)
	}
	if tasks[0].URL == "" || tasks[0].State != "leased" || tasks[0].Attempt != 1 {
		t.Fatalf("领取后应为 leased/attempt=1/URL 明文，得到 %+v", tasks[0])
	}
	if tasks[0].Headers == "" {
		t.Fatal("headers 应随任务下发")
	}
	if tasks[1].TimeoutSec != 15 || tasks[1].Method != "GET" {
		t.Fatalf("缺省 timeout_sec=15/method=GET，得到 %d/%s", tasks[1].TimeoutSec, tasks[1].Method)
	}
	if more, _ := s.ClaimNetTasks(7, 10); len(more) != 0 {
		t.Fatalf("pending 已租空，不应再领到任务")
	}
	if more, _ := s.ClaimNetTasks(8, 10); len(more) != 0 {
		t.Fatal("其他出口不应领到任务")
	}
	_ = id2

	// 回传幂等：首次成功，重复丢弃
	first, err := s.FinishNetTask(id, 200, `{"results":[]}`, "")
	if err != nil || !first {
		t.Fatalf("首次回传应成功（err=%v first=%v）", err, first)
	}
	if again, _ := s.FinishNetTask(id, 200, `dup`, ""); again {
		t.Fatal("done 后重复回传应被丢弃")
	}
	got, err := s.GetNetTask(id)
	if err != nil || got.Result != `{"results":[]}` || got.StatusCode != 200 {
		t.Fatalf("结果应原样落库，得到 %+v（err=%v）", got, err)
	}

	// consumed_at 打标：只有首次返回 true
	if ok, err := s.ConsumeNetTask(id); err != nil || !ok {
		t.Fatalf("首次消费应成功（err=%v ok=%v）", err, ok)
	}
	if ok, _ := s.ConsumeNetTask(id); ok {
		t.Fatal("重复消费应被拒绝")
	}

	// 失败回传也是终态
	if _, err := s.CreateNetTask("tvfollow_api", "q=另外一部", 7, "https://api.themoviedb.org/3/search/tv?query=y", "", nil, "", 15, false); err != nil {
		t.Fatal(err)
	}
	tsk, _ := s.ActiveNetTask("tvfollow_api", "q=另外一部")
	s.ClaimNetTasks(7, 10)
	if first, err := s.FinishNetTask(tsk.ID, 0, "", "超时"); err != nil || !first {
		t.Fatalf("失败回传应计一次完成（err=%v first=%v）", err, first)
	}

	// lease 超时回收：attempt<2 回 pending，再超时转 failed
	if _, err := s.CreateNetTask("tvfollow_api", "q=回收", 7, "https://api.themoviedb.org/3/search/tv?query=z", "", nil, "", 15, false); err != nil {
		t.Fatal(err)
	}
	t3, _ := s.ActiveNetTask("tvfollow_api", "q=回收")
	s.ClaimNetTasks(7, 10)
	s.db.Exec(`UPDATE net_tasks SET leased_at=? WHERE id=?`, time.Now().Add(-2*time.Minute).Unix(), t3.ID)
	if dead := s.ReclaimExpiredNetTasks(); dead != 0 {
		t.Fatalf("首次超时应回 pending 而非 failed，得到 %d", dead)
	}
	got3, _ := s.GetNetTask(t3.ID)
	if got3.State != "pending" || got3.Attempt != 1 {
		t.Fatalf("超时回收应回 pending/attempt=1，得到 %s/%d", got3.State, got3.Attempt)
	}
	s.ClaimNetTasks(7, 10)
	s.db.Exec(`UPDATE net_tasks SET leased_at=? WHERE id=?`, time.Now().Add(-2*time.Minute).Unix(), t3.ID)
	if dead := s.ReclaimExpiredNetTasks(); dead != 1 {
		t.Fatalf("二次超时应转 failed，得到 %d", dead)
	}

	// 换绑/删除出口：pending 作废，leased 不动
	if _, err := s.CreateNetTask("tvfollow_api", "q=作废", 9, "https://api.themoviedb.org/3/search/tv?query=w", "", nil, "", 15, false); err != nil {
		t.Fatal(err)
	}
	s.ExpireNetTasksByServer(9)
	t4, _ := s.ActiveNetTask("tvfollow_api", "q=作废")
	if t4 != nil {
		t.Fatal("作废后不应再有活跃任务")
	}

	// 终态清理
	s.CleanupNetTasks(-time.Hour) // recently 为负 → 全清终态
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM net_tasks WHERE state IN ('done','failed','expired')`).Scan(&n)
	if n != 0 {
		t.Fatalf("终态任务应被清空，剩 %d", n)
	}
}

// 存量库迁移回归（571 事故）：v1.0.21 建的 net_tasks 无 allow_any_host 列，
// 升级路径必须自动补列（否则手动检查 INSERT 报 no column named allow_any_host）。
func TestNetTasksMigrateAllowAnyHost(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := OpenWithKey(dbPath, filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	// 模拟旧结构：去掉 allow_any_host 列重建表
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE net_tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE net_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		purpose TEXT NOT NULL DEFAULT '',
		ref_key TEXT NOT NULL DEFAULT '',
		server_id INTEGER NOT NULL DEFAULT 0,
		url TEXT NOT NULL DEFAULT '',
		method TEXT NOT NULL DEFAULT 'GET',
		headers TEXT NOT NULL DEFAULT '',
		body TEXT NOT NULL DEFAULT '',
		timeout_sec INTEGER NOT NULL DEFAULT 15,
		attempt INTEGER NOT NULL DEFAULT 0,
		state TEXT NOT NULL DEFAULT 'pending',
		leased_at INTEGER NOT NULL DEFAULT 0,
		status_code INTEGER NOT NULL DEFAULT 0,
		result TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		consumed_at INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// 重新打开：迁移应补列且任务链路可用（含 allowAnyHost=true 的 PT 类入队）
	s2, err := OpenWithKey(dbPath, filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	id, err := s2.CreateNetTask("tvfollow_pt", "x:1", 1, "https://pt.example/x", "GET", nil, "", 15, true)
	if err != nil {
		t.Fatalf("迁移后 PT 类任务入队应成功（allow_any_host 列已补）： %v", err)
	}
	tasks, _ := s2.ClaimNetTasks(1, 10)
	if len(tasks) != 1 || !tasks[0].AllowAnyHost {
		t.Fatalf("allow_any_host 应随任务下发： %+v", tasks)
	}
	_ = id
}
