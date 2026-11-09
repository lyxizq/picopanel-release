package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"lz-monitor/internal/proto"
)

// app_tasks 应用操作任务（54）口径钉死：同机同目标活跃唯一、领取原子租出、
// 回传幂等（终态后重复丢弃）、lease 超时回收 attempt≤2、删机作废 pending、
// 终态清理；server_apps 快照随 InsertMetric 单行 upsert / 能力缺失删除，
// LatestMetric 组装回读。
func TestAppTasks(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 入队白名单：非法类型/动词拒绝
	if _, err := s.CreateAppTask(1, "docker", "abc", "rm"); err == nil {
		t.Fatal("docker 非法动词应拒绝")
	}
	if _, err := s.CreateAppTask(1, "ssh", "abc", "start"); err == nil {
		t.Fatal("未知类型应拒绝")
	}

	id, err := s.CreateAppTask(1, "vm", "lvpmgjxz", "shutdown")
	if err != nil {
		t.Fatal(err)
	}
	// 同机同目标活跃唯一（防连点堆指令）；其他机器同名目标不受影响
	if _, err := s.CreateAppTask(1, "vm", "lvpmgjxz", "start"); !errors.Is(err, ErrAppTaskActive) {
		t.Fatalf("同目标重复入队应报 ErrAppTaskActive，得到 %v", err)
	}
	if _, err := s.CreateAppTask(2, "vm", "lvpmgjxz", "start"); err != nil {
		t.Fatalf("其他机器同名目标入队应放行: %v", err)
	}

	// 领取：只发本机名下，原子租出
	tasks, err := s.ClaimAppTasks(1, 4)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("机器 1 应领到 1 个任务: %v %d", err, len(tasks))
	}
	if tasks[0].ID != id || tasks[0].State != "leased" || tasks[0].Action != "shutdown" {
		t.Fatalf("领取态不符: %+v", tasks[0])
	}
	if more, _ := s.ClaimAppTasks(1, 4); len(more) != 0 {
		t.Fatalf("pending 已租出不应重复领取，得到 %d", len(more))
	}
	if other, _ := s.ClaimAppTasks(9, 4); len(other) != 0 {
		t.Fatalf("他机不应领到: %d", len(other))
	}
	// 活跃约束只锁同目标：此刻补入另一目标可再入队
	if _, err := s.CreateAppTask(1, "vm", "hy6dek9c", "start"); err != nil {
		t.Fatalf("其他目标入队应放行: %v", err)
	}

	// 回传幂等：首次生效，重复丢弃
	if first, err := s.FinishAppTask(id, true, "已下达关机信号"); err != nil || !first {
		t.Fatalf("首次回传应生效: %v %v", first, err)
	}
	if again, _ := s.FinishAppTask(id, false, "dup"); again {
		t.Fatal("终态后重复回传应丢弃")
	}
	got, err := s.GetAppTaskState(id)
	if err != nil || got.State != "done" || !got.OK || got.Message != "已下达关机信号" {
		t.Fatalf("终态行不符: %+v %v", got, err)
	}
	if got.ServerID != 1 {
		t.Fatalf("轻量行应带 server_id 供归属校验: %+v", got)
	}

	// 终态后活跃约束解除，可再次入队
	if _, err := s.CreateAppTask(1, "vm", "lvpmgjxz", "start"); err != nil {
		t.Fatalf("终态后应可再次入队: %v", err)
	}

	// lease 超时回收：attempt<2 回 pending，again 后第二次超时转 failed
	t2, _ := s.CreateAppTask(1, "docker", "web", "stop")
	s.ClaimAppTasks(1, 10)
	s.db.Exec(`UPDATE app_tasks SET leased_at=? WHERE id=?`, time.Now().Add(-2*time.Minute).Unix(), t2)
	if dead := s.ReclaimExpiredAppTasks(); dead != 0 {
		t.Fatalf("首次超时应回 pending 而非终态，得到 %d", dead)
	}
	got2, _ := s.GetAppTaskState(t2)
	if got2.State != "pending" {
		t.Fatalf("首次超时应回 pending，得到 %s", got2.State)
	}
	s.ClaimAppTasks(1, 10)
	s.db.Exec(`UPDATE app_tasks SET leased_at=? WHERE id=?`, time.Now().Add(-2*time.Minute).Unix(), t2)
	if dead := s.ReclaimExpiredAppTasks(); dead != 1 {
		t.Fatalf("二次超时应转 failed，得到 %d", dead)
	}
	got2, _ = s.GetAppTaskState(t2)
	if got2.State != "failed" {
		t.Fatalf("二次超时应 failed，得到 %s", got2.State)
	}

	// 删机作废 pending + 终态清理
	t3, _ := s.CreateAppTask(3, "vm", "x", "start")
	s.ExpireAppTasksByServer(3)
	got3, _ := s.GetAppTaskState(t3)
	if got3.State != "expired" {
		t.Fatalf("删机作废应 expired，得到 %s", got3.State)
	}
	s.CleanupAppTasks(-time.Hour) // recently 为负 → 全清终态
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM app_tasks WHERE state IN ('done','failed','expired')`).Scan(&n)
	if n != 0 {
		t.Fatalf("终态应清空，剩 %d", n)
	}
}

func TestServerAppsSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithKey(filepath.Join(dir, "test.db"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 带容器/虚拟机的上报 → 单行 upsert → LatestMetric 回读
	ctrs := []proto.ContainerInfo{{ID: "abc123def456", Name: "web", Image: "nginx:latest", State: "running", Status: "Up 3 days", Created: 1700000000}}
	vms := []proto.VMInfo{{ID: "3", Name: "lvpmgjxz", State: "running"}, {Name: "hy6dek9c", State: "shut off"}}
	if err := s.InsertMetric(7, 1000, &proto.Report{CPU: 1, Containers: ctrs, VMs: vms}); err != nil {
		t.Fatal(err)
	}
	m, err := s.LatestMetric(7)
	if err != nil || m == nil {
		t.Fatalf("LatestMetric 不应为空: %v", err)
	}
	if len(m.Containers) != 1 || m.Containers[0].Name != "web" || m.Containers[0].ID != "abc123def456" {
		t.Fatalf("容器快照不符: %+v", m.Containers)
	}
	if len(m.VMs) != 2 || m.VMs[1].State != "shut off" || m.VMs[1].ID != "" {
		t.Fatalf("虚拟机快照不符: %+v", m.VMs)
	}

	// 后续上报覆盖（容器停止）
	ctrs[0].State = "exited"
	if err := s.InsertMetric(7, 2000, &proto.Report{CPU: 2, Containers: ctrs, VMs: vms}); err != nil {
		t.Fatal(err)
	}
	m, _ = s.LatestMetric(7)
	if m.Containers[0].State != "exited" {
		t.Fatalf("快照应覆盖为最新: %+v", m.Containers)
	}

	// 仅有 Docker 无容器（空数组非 nil）也应保留行
	if err := s.InsertMetric(7, 3000, &proto.Report{CPU: 3, Containers: []proto.ContainerInfo{}, VMs: vms}); err != nil {
		t.Fatal(err)
	}
	m, _ = s.LatestMetric(7)
	if m.Containers == nil || len(m.Containers) != 0 {
		t.Fatalf("空数组应保留为空切片: %#v", m.Containers)
	}

	// 双 nil（旧版 agent / 能力缺失）→ 删除旧行
	if err := s.InsertMetric(7, 4000, &proto.Report{CPU: 4}); err != nil {
		t.Fatal(err)
	}
	m, _ = s.LatestMetric(7)
	if m.Containers != nil || m.VMs != nil {
		t.Fatalf("双 nil 上报应清掉旧快照: %#v %#v", m.Containers, m.VMs)
	}

	// 服务器删除连带清理
	if err := s.InsertMetric(7, 5000, &proto.Report{CPU: 5, Containers: ctrs, VMs: vms}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServer("fnos", "", "token-x", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteServer(7); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM server_apps`).Scan(&n)
	if n != 0 {
		t.Fatalf("删机应连带清快照，剩 %d", n)
	}
}
