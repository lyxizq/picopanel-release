package store

import (
	"path/filepath"
	"testing"

	"lz-monitor/internal/proto"
)

// openTestStore 打开临时库。
func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenWithKey(filepath.Join(t.TempDir(), "test.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatalf("打开测试库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// findPushToken 按密钥在 PushTokenList（上报端点恒时比较用的数据源）中
// 定位服务器，返回 ID（0=未命中）。
func findPushToken(t *testing.T, s *Store, token string) int64 {
	t.Helper()
	toks, err := s.PushTokenList()
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range toks {
		if tk.Token == token {
			return tk.ID
		}
	}
	return 0
}

// TestPushTokenOneToOne 上报密钥一对一：定位只看 push 行，
// 撞车在创建与更新两处都被拒。
func TestPushTokenOneToOne(t *testing.T) {
	s := openTestStore(t)

	a, err := s.CreatePushServer("A", "tok-a", 0)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 一对一定位：密钥命中对应 push 行
	if id := findPushToken(t, s, "tok-a"); id != a.ID {
		t.Fatalf("tok-a 应命中 A: got %d", id)
	}

	// 2. 拉取模式的共享密钥撞到同值不影响上报归属（只查 push 行）
	if _, err := s.CreateServer("pull-1", "10.0.0.1:19100", "tok-a", 0); err != nil {
		t.Fatal(err)
	}
	if id := findPushToken(t, s, "tok-a"); id != a.ID {
		t.Fatalf("拉取密钥同值不应干扰 push 归属: got %d", id)
	}

	// 3. 创建撞车 → ErrTokenUsed
	if _, err := s.CreatePushServer("B", "tok-a", 0); err != ErrTokenUsed {
		t.Fatalf("重复密钥应返回 ErrTokenUsed, got %v", err)
	}

	// 4. 改密钥撞车 → ErrTokenUsed；改成新值成功
	b, err := s.CreatePushServer("B", "tok-b", 0)
	if err != nil {
		t.Fatal(err)
	}
	bad := "tok-a"
	if err := s.UpdateServer(b.ID, ServerUpdate{Token: &bad}); err != ErrTokenUsed {
		t.Fatalf("更新撞车应返回 ErrTokenUsed, got %v", err)
	}
	fresh := "tok-b2"
	if err := s.UpdateServer(b.ID, ServerUpdate{Token: &fresh}); err != nil {
		t.Fatal(err)
	}

	// 5. 拉取模式共用密钥不受限
	if _, err := s.CreateServer("pull-2", "10.0.0.2:19100", "shared-pull", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServer("pull-3", "10.0.0.3:19100", "shared-pull", 0); err != nil {
		t.Fatalf("拉取模式共用密钥应允许: %v", err)
	}

	// 6. 未知密钥 → 未命中
	if id := findPushToken(t, s, "no-such-token"); id != 0 {
		t.Fatalf("未知密钥应未命中, got %d", id)
	}
}

// TestCreateNodeServer node 模式落库带 collect_mode='node'（此前误走
// CreateServer 被存成 agent，抓取端按 agent JSON 拉取必然失败）。
func TestCreateNodeServer(t *testing.T) {
	s := openTestStore(t)
	srv, err := s.CreateNodeServer("exp-1", "10.0.0.1:10200", "tok", 0)
	if err != nil {
		t.Fatal(err)
	}
	if srv.CollectMode != "node" || srv.AgentAddr != "10.0.0.1:10200" || srv.Token != "tok" {
		t.Fatalf("node 服务器落库错误: %+v", srv)
	}
	got, _ := s.GetServer(srv.ID)
	if got == nil || got.CollectMode != "node" {
		t.Fatalf("读回应保持 node 模式: %+v", got)
	}
	// 密钥留空 → NULL（未设置），同样正常
	srv2, err := s.CreateNodeServer("exp-2", "10.0.0.2", "", 0)
	if err != nil || srv2.CollectMode != "node" || srv2.Token != "" {
		t.Fatalf("无密钥 node 服务器落库错误: %+v err %v", srv2, err)
	}
}

// TestServerExpiryDates 购买/到期日期的写入、读取与清空（局部更新不影响另一字段）。
func TestServerExpiryDates(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.PurchasedOn != "" || a.ExpiresOn != "" {
		t.Fatalf("新服务器日期应为空: %+v", a)
	}
	p, e := "2026-01-01", "2027-01-01"
	if err := s.UpdateServer(a.ID, ServerUpdate{PurchasedOn: &p, ExpiresOn: &e}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetServer(a.ID)
	if got == nil || got.PurchasedOn != p || got.ExpiresOn != e {
		t.Fatalf("日期存取错误: %+v", got)
	}
	empty := ""
	if err := s.UpdateServer(a.ID, ServerUpdate{ExpiresOn: &empty}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetServer(a.ID)
	if got == nil || got.ExpiresOn != "" || got.PurchasedOn != p {
		t.Fatalf("清空到期日期后购买日期应保留: %+v", got)
	}
}

// TestServerRemark 备注字段的写入、读取与清空。
func TestServerRemark(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreateServer("A", "10.0.0.1:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.Remark != "" {
		t.Fatalf("新服务器备注应为空: %+v", a)
	}
	r := "官网前端机，机房 A 柜 3 层"
	if err := s.UpdateServer(a.ID, ServerUpdate{Remark: &r}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetServer(a.ID)
	if got == nil || got.Remark != r {
		t.Fatalf("备注存取错误: %+v", got)
	}
	empty := ""
	if err := s.UpdateServer(a.ID, ServerUpdate{Remark: &empty}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetServer(a.ID)
	if got == nil || got.Remark != "" {
		t.Fatalf("备注应已清空: %+v", got)
	}
}

// TestServerTermSSH Web 终端专用字段：newSSHServer 夹具密码落库、
// UpdateServer 写入/清空 ssh_addr 与 ssh_password（agent 模式机器补配终端）。
func TestServerTermSSH(t *testing.T) {
	s := openTestStore(t)

	// ssh 模式创建时密码直接落库
	a, err := newSSHServer(s, "A", "10.0.0.1:22", "root", "", "s3cret", 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.SSHPassword != "s3cret" || a.SSHAddr != "" {
		t.Fatalf("ssh 模式创建：密码应落库、终端地址为空（回退采集地址）: %+v", a)
	}

	// agent 模式机器补配终端地址与密码
	b, err := s.CreateServer("B", "10.0.0.2:19100", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	addr, pwd := "10.0.0.2:2222", "passw0rd"
	if err := s.UpdateServer(b.ID, ServerUpdate{SSHAddr: &addr, SSHPassword: &pwd}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetServer(b.ID)
	if got == nil || got.SSHAddr != addr || got.SSHPassword != pwd {
		t.Fatalf("终端地址/密码存取错误: %+v", got)
	}

	// 清空：回退私钥认证 / ssh 模式回退采集地址
	empty := ""
	if err := s.UpdateServer(b.ID, ServerUpdate{SSHAddr: &empty, SSHPassword: &empty}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetServer(b.ID)
	if got == nil || got.SSHAddr != "" || got.SSHPassword != "" {
		t.Fatalf("终端地址/密码应已清空: %+v", got)
	}

	// 其他字段更新不吞终端字段（非指针不修改）
	name := "B2"
	if err := s.UpdateServer(b.ID, ServerUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetServer(b.ID)
	if got == nil || got.Name != "B2" {
		t.Fatalf("改名失败: %+v", got)
	}
}

// 版本串入库语义（2026-10-06 Fnos 现场教训）：空串（老 agent 无版本字段的
// 上报）不得抹掉已知版本；非空正常更新。
func TestUpsertOnReportVersionKeepOnEmpty(t *testing.T) {
	s := openTestStore(t)
	a, err := s.CreatePushServer("A", "tok-ver", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertOnReport(a.ID, &proto.SysInfo{Hostname: "h", AgentVersion: "v1.0.7"}, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertOnReport(a.ID, &proto.SysInfo{Hostname: "h", AgentVersion: ""}, 200); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetServer(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentVersion != "v1.0.7" {
		t.Fatalf("空版本上报不应抹掉已知版本: %q", got.AgentVersion)
	}
	if err := s.UpsertOnReport(a.ID, &proto.SysInfo{Hostname: "h", AgentVersion: "v1.0.8"}, 300); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetServer(a.ID); got.AgentVersion != "v1.0.8" {
		t.Fatalf("非空版本应正常更新: %q", got.AgentVersion)
	}
}
