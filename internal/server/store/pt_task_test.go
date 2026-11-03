package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// PT 中转抓取任务队列（391 扩展）存储层测试：入队查重/频控/领取原子性/回传
// 幂等/租约回收/换绑作废——防死循环矩阵的每一环都有断言。
func TestPtTaskQueue(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	siteID, err := s.CreatePtSite(&PtSite{SiteKey: "hdsky", Name: "HDSky", Cookie: "c=secret"})
	if err != nil {
		t.Fatal(err)
	}

	// 入队：pending 任务创建成功，重复入队被查重拦截
	if _, err := s.CreatePtTask(&PtSite{ID: siteID, SiteKey: "hdsky", Name: "HDSky", Cookie: "c=secret"}, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePtTask(&PtSite{ID: siteID, SiteKey: "hdsky", Name: "HDSky", Cookie: "c=secret"}, 7); !errors.Is(err, ErrPtTaskActive) {
		t.Fatalf("重复入队应 ErrPtTaskActive，得到 %v", err)
	}

	// 领取：只发该出口服务器的任务，租出后 attempt+1、Cookie 解密
	tasks, err := s.ClaimPtTasks(7, 4)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("领取 = %v/%v", tasks, err)
	}
	if tasks[0].State != PtTaskLeased || tasks[0].Attempt != 1 || tasks[0].Cookie != "c=secret" {
		t.Fatalf("租出态 = %+v", tasks[0])
	}
	if others, _ := s.ClaimPtTasks(8, 4); len(others) != 0 {
		t.Fatalf("别的出口不应领到任务: %v", others)
	}
	if again, _ := s.ClaimPtTasks(7, 4); len(again) != 0 {
		t.Fatalf("leased 任务不应重复领取: %v", again)
	}

	// 回传：首次消费成功、重复回传幂等丢弃
	res := &PtTaskResult{Snapshot: &PtSnapshot{Username: "u", Seeding: -1}}
	if first, err := s.FinishPtTask(tasks[0].ID, res); err != nil || !first {
		t.Fatalf("首次回传 = %v/%v", first, err)
	}
	if first, _ := s.FinishPtTask(tasks[0].ID, res); first {
		t.Fatal("重复回传应幂等丢弃")
	}
	if err := s.ConsumePtTaskResult(siteID, res); err != nil {
		t.Fatal(err)
	}
	site, _ := s.GetPtSite(siteID)
	if site.LastOKAt == 0 || site.LastError != "" {
		t.Fatalf("健康态未更新: %+v", site)
	}

	// 失败回传：终态不复活
	if _, err := s.CreatePtTask(&PtSite{ID: siteID, SiteKey: "hdsky", Name: "HDSky", Cookie: "c=secret"}, 7); err != nil {
		t.Fatal(err)
	}
	t2, _ := s.ClaimPtTasks(7, 4)
	if first, err := s.FinishPtTask(t2[0].ID, &PtTaskResult{Error: "HTTP 403"}); err != nil || !first {
		t.Fatalf("失败回传 = %v/%v", first, err)
	}
	if _, err := s.CreatePtTask(&PtSite{ID: siteID, SiteKey: "hdsky", Name: "HDSky", Cookie: "c=secret"}, 7); err != nil {
		t.Fatalf("失败即终态后应可再入队: %v", err)
	}

	// 频控：把 24h 内任务数顶到上限后拦截（先清掉上一步留在途的任务；
	// 此前已入队 3 个，再成功 3 个即达 PtTaskDailyLimit=6）
	t4, _ := s.ClaimPtTasks(7, 4)
	s.FinishPtTask(t4[0].ID, &PtTaskResult{Snapshot: &PtSnapshot{Username: "u"}})
	for i := 0; i < PtTaskDailyLimit-3; i++ {
		if _, err := s.CreatePtTask(&PtSite{ID: siteID, SiteKey: "hdsky", Name: "HDSky", Cookie: "c"}, 7); err != nil {
			t.Fatalf("第 %d 次入队不应拦截: %v", i, err)
		}
		t3, _ := s.ClaimPtTasks(7, 4)
		s.FinishPtTask(t3[0].ID, &PtTaskResult{Snapshot: &PtSnapshot{Username: "u"}})
	}
	if _, err := s.CreatePtTask(&PtSite{ID: siteID, SiteKey: "hdsky", Name: "HDSky", Cookie: "c"}, 7); !errors.Is(err, ErrPtTaskRateLimit) {
		t.Fatalf("超频应 ErrPtTaskRateLimit，得到 %v", err)
	}
}

// 频控窗口（435）：按抓取日（本地 06:00 起算）计数而非滚动 24h——窗口起点
// 之前的任务（前一天用满的配额）不挡新的一天，窗口内照常顶满拦截。
func TestPtTaskRateWindowReset(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	insertAt := func(siteID int64, at int64) {
		s.db.Exec(`INSERT INTO pt_tasks (site_id, server_id, site_key, name, base_url, cookie, ua, state, created_at, updated_at)
			VALUES (?,0,'x','x','','','','done',?,?)`, siteID, at, at)
	}
	win := ptTaskWindowStart(time.Now())

	// ①起点之前已用满配额（前一天傍晚的形态）：今天照常可入队
	site1, err := s.CreatePtSite(&PtSite{SiteKey: "dian115", Name: "癫影", Cookie: "c=secret"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < PtTaskDailyLimit; i++ {
		insertAt(site1, win-3600)
	}
	if _, err := s.CreatePtTask(&PtSite{ID: site1, SiteKey: "dian115", Name: "癫影", Cookie: "c"}, 7); err != nil {
		t.Fatalf("跨过 06:00 后旧任务不应计入频控: %v", err)
	}

	// ②边界：窗口起点刚过的任务计入今天，顶满即拦
	site2, err := s.CreatePtSite(&PtSite{SiteKey: "hdsky", Name: "HDSky", Cookie: "c=secret"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < PtTaskDailyLimit; i++ {
		insertAt(site2, win+1)
	}
	if _, err := s.CreatePtTask(&PtSite{ID: site2, SiteKey: "hdsky", Name: "HDSky", Cookie: "c"}, 7); !errors.Is(err, ErrPtTaskRateLimit) {
		t.Fatalf("窗口内顶满应 ErrPtTaskRateLimit，得到 %v", err)
	}
}

// 租约回收：超时未确认执行的任务 attempt<2 重派一次、到顶转 failed；已执行
// 任务的失败永不重派。
func TestPtTaskLeaseReclaim(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	siteID, _ := s.CreatePtSite(&PtSite{SiteKey: "hdsky", Name: "HDSky", Cookie: "c"})
	if _, err := s.CreatePtTask(&PtSite{ID: siteID, SiteKey: "hdsky", Name: "HDSky", Cookie: "c"}, 7); err != nil {
		t.Fatal(err)
	}
	t1, _ := s.ClaimPtTasks(7, 4)

	// 把租约时间改旧模拟超时
	if _, err := s.db.Exec(`UPDATE pt_tasks SET leased_at = leased_at - 3600 WHERE id = ?`, t1[0].ID); err != nil {
		t.Fatal(err)
	}
	if n := s.ReclaimExpiredPtTasks(); n != 0 {
		t.Fatalf("attempt=1 的回收不应产生终态: %d", n)
	}
	again, _ := s.ClaimPtTasks(7, 4)
	if len(again) != 1 || again[0].ID != t1[0].ID || again[0].Attempt != 2 {
		t.Fatalf("超时应重派一次: %+v", again)
	}
	// 第二次超时 → 达重派上限转 failed
	s.db.Exec(`UPDATE pt_tasks SET leased_at = leased_at - 3600 WHERE id = ?`, t1[0].ID)
	if n := s.ReclaimExpiredPtTasks(); n != 1 {
		t.Fatalf("attempt=2 超时应转 failed: %d", n)
	}
	row, _ := s.GetPtTask(t1[0].ID)
	if row.State != PtTaskFailed {
		t.Fatalf("终态 = %s", row.State)
	}
}

// 换绑/删服务器/删站点的任务作废口径。
func TestPtTaskInvalidation(t *testing.T) {
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	siteID, _ := s.CreatePtSite(&PtSite{SiteKey: "hdsky", Name: "HDSky", Cookie: "c"})
	if _, err := s.CreatePtTask(&PtSite{ID: siteID, SiteKey: "hdsky", Name: "HDSky", Cookie: "c"}, 7); err != nil {
		t.Fatal(err)
	}
	s.ExpirePtTasksByServer(7)
	row, _ := s.GetPtTask(1)
	if row.State != PtTaskExpired {
		t.Fatalf("删服务器后 pending 应作废: %s", row.State)
	}

	if _, err := s.CreatePtTask(&PtSite{ID: siteID, SiteKey: "hdsky", Name: "HDSky", Cookie: "c"}, 8); err != nil {
		t.Fatal(err)
	}
	s.DropPtTasksBySite(siteID)
	var active int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pt_tasks WHERE site_id = ? AND state IN ('pending','leased')`, siteID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("删站后不应有活跃任务: n=%d err=%v", active, err)
	}
}
