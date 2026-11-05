package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestSvcServerPortCRUD 两级模型全链路：建服务器→加端口→读回→删（连带端口）。
func TestSvcServerPortCRUD(t *testing.T) {
	s := openTestStore(t)

	sid, err := s.CreateSvcServer(&SvcServer{Name: "NAS", Host: "192.168.1.10", Interval: 60, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	sid2, err := s.CreateSvcServer(&SvcServer{Name: "", Host: "10.0.0.2", Interval: 0, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if sid == sid2 {
		t.Fatal("服务器 id 不应重复")
	}

	sv, err := s.GetSvcServer(sid)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Name != "NAS" || sv.Host != "192.168.1.10" || sv.Interval != 60 || sv.Enabled {
		t.Fatalf("服务器回读不符: %+v", sv)
	}
	// 空间隔回落默认在 API 层做，store 层按传入值存
	if sv2, _ := s.GetSvcServer(sid2); sv2.Interval != 0 || !sv2.Enabled {
		t.Fatalf("第二台服务器回读不符: %+v", sv2)
	}

	pid, err := s.CreateSvcPort(&SvcPort{ServerID: sid, Name: "DSM 面板", Port: 5000})
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.GetSvcPortRow(pid)
	if err != nil {
		t.Fatal(err)
	}
	if row.ServerID != sid || row.Name != "DSM 面板" || row.Port != 5000 {
		t.Fatalf("端口回读不符: %+v", row)
	}
	if row.Host != "192.168.1.10" || row.Interval != 60 || row.Enabled {
		t.Fatalf("端口行应带出服务器参数: %+v", row)
	}
	if row.Up != -1 || row.CheckedAt != 0 || row.Since != 0 {
		t.Fatalf("新建端口应为未检测态: %+v", row)
	}

	if n, _ := s.CountSvcPorts(sid); n != 1 {
		t.Fatalf("端口计数应 1: %d", n)
	}

	// 删服务器连带端口
	if err := s.DeleteSvcServer(sid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSvcServer(sid); !errors.Is(err, ErrSvcServerNotFound) {
		t.Fatalf("删后应报服务器不存在: %v", err)
	}
	if _, err := s.GetSvcPortRow(pid); !errors.Is(err, ErrSvcPortNotFound) {
		t.Fatalf("删服务器应连带删端口: %v", err)
	}
	// 删端口：不存在报错
	if err := s.DeleteSvcPort(99999); !errors.Is(err, ErrSvcPortNotFound) {
		t.Fatalf("删不存在端口应报错: %v", err)
	}
}

// TestSvcUpdateReset 服务器换址重置其全部端口态；端口换端口号重置自身；
// 只改名/间隔/启停状态原样保留。
func TestSvcUpdateReset(t *testing.T) {
	s := openTestStore(t)
	sid, err := s.CreateSvcServer(&SvcServer{Name: "web", Host: "10.0.0.2", Interval: 30, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p1, err := s.CreateSvcPort(&SvcPort{ServerID: sid, Name: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.CreateSvcPort(&SvcPort{ServerID: sid, Name: "ssh", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{p1, p2} {
		if _, err := s.RecordSvcCheck(id, true, 12, ""); err != nil {
			t.Fatal(err)
		}
	}

	// 只改名称/间隔/启停：两端口状态保留
	if err := s.UpdateSvcServer(&SvcServer{ID: sid, Name: "web2", Host: "10.0.0.2", Interval: 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetSvcPortRow(p1)
	if a.Name != "http" || a.Interval != 60 || a.Up != 1 || a.LatencyMs != 12 {
		t.Fatalf("仅改服务器名/间隔不应重置端口态: %+v", a)
	}

	// 服务器换址：全部端口态重置
	if err := s.UpdateSvcServer(&SvcServer{ID: sid, Name: "web2", Host: "10.0.0.3", Interval: 60, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{p1, p2} {
		a, _ := s.GetSvcPortRow(id)
		if a.Up != -1 || a.CheckedAt != 0 || a.Since != 0 || a.LastOKAt != 0 || a.LastError != "" {
			t.Fatalf("换址应重置端口态: %+v", a)
		}
	}

	// 端口改业务名：状态保留；换端口号：仅该端口重置
	if _, err := s.RecordSvcCheck(p1, true, 12, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSvcCheck(p2, true, 12, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSvcPort(&SvcPort{ID: p1, Name: "http-alt", Port: 80}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.GetSvcPortRow(p1); a.Name != "http-alt" || a.Up != 1 {
		t.Fatalf("仅改端口名不应重置: %+v", a)
	}
	if err := s.UpdateSvcPort(&SvcPort{ID: p1, Name: "http-alt", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetSvcPortRow(p1)
	if a.Up != -1 || a.LastOKAt != 0 {
		t.Fatalf("换端口号应重置: %+v", a)
	}
	if b, _ := s.GetSvcPortRow(p2); b.Up != 1 {
		t.Fatalf("其它端口不应被连带重置: %+v", b)
	}
}

// TestRecordSvcCheckSince 拨测结果写入语义：up 不变 since 不动，状态翻转
// since 推进为本次时刻；last_ok_at 只在成功时推进。
func TestRecordSvcCheckSince(t *testing.T) {
	s := openTestStore(t)
	sid, err := s.CreateSvcServer(&SvcServer{Name: "db", Host: "10.0.0.4", Interval: 30, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := s.CreateSvcPort(&SvcPort{ServerID: sid, Name: "mysql", Port: 3306})
	if err != nil {
		t.Fatal(err)
	}

	// 首检成功：-1 → 1，since 推进；未检测态不算翻转、不落事件
	flipped, err := s.RecordSvcCheck(pid, true, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if flipped {
		t.Fatal("未检测态首检不算翻转")
	}
	a, _ := s.GetSvcPortRow(pid)
	if a.Up != 1 || a.LatencyMs != 5 || a.Since == 0 || a.LastOKAt == 0 {
		t.Fatalf("首检成功态不符: %+v", a)
	}
	since1 := a.Since

	time.Sleep(1100 * time.Millisecond) // 秒级时间戳，隔 1s 才能观察到 since 推进

	// 仍通：since 保持、无翻转无事件
	flipped, err = s.RecordSvcCheck(pid, true, 6, "")
	if err != nil || flipped {
		t.Fatalf("状态未变不应翻转: flipped=%v err=%v", flipped, err)
	}
	a, _ = s.GetSvcPortRow(pid)
	if a.Since != since1 {
		t.Fatalf("状态未变 since 不应推进: %d → %d", since1, a.Since)
	}
	if evs, _ := s.ListSvcEvents(10); len(evs) != 0 {
		t.Fatalf("未翻转不应落事件: %d", len(evs))
	}

	// 翻红：since 推进、last_error 落库、last_ok_at 保留；翻转返回 true 且
	// 事件与状态同事务落库（同一行 up/latency/error/时刻）
	errMsg := "dial tcp: connection refused"
	flipped, err = s.RecordSvcCheck(pid, false, 3000, errMsg)
	if err != nil || !flipped {
		t.Fatalf("翻转应返回 true: flipped=%v err=%v", flipped, err)
	}
	a, _ = s.GetSvcPortRow(pid)
	if a.Up != 0 || a.LatencyMs != 3000 || a.Since == since1 || a.LastError != errMsg {
		t.Fatalf("翻红态不符: %+v", a)
	}
	if a.LastOKAt == 0 {
		t.Fatal("翻红不应清掉 last_ok_at")
	}
	evs, err := s.ListSvcEvents(10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("翻转应恰好落 1 条事件: %d %v", len(evs), err)
	}
	if evs[0].PortID != pid || evs[0].ServerID != sid || evs[0].Up != 0 ||
		evs[0].LatencyMs != 3000 || evs[0].Error != errMsg || evs[0].OccurredAt != a.CheckedAt {
		t.Fatalf("事件字段应与状态行同拍: %+v vs %+v", evs[0], a)
	}

	// 行不存在（刚被删）：返回 false 不炸
	if _, err := s.RecordSvcCheck(999999, true, 5, ""); err == nil {
		t.Fatal("不存在的端口应报错")
	}
}

// TestMigrateSvcMonV2 旧 svc_monitors 平铺表迁移：同 host 端口归一台服务器、
// 端口业务名/拨测状态随迁、迁完旧表删除。
func TestMigrateSvcMonV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mig.db")
	key := filepath.Join(t.TempDir(), "key")
	s, err := OpenWithKey(path, key) // 先按新结构初始化
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 手工造旧表（358 结构）+ 三行：同 host 两端口、另一 host 一行
	oldDDL := `CREATE TABLE svc_monitors (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, host TEXT NOT NULL,
		port INTEGER NOT NULL, interval_sec INTEGER NOT NULL DEFAULT 30,
		enabled INTEGER NOT NULL DEFAULT 1, sort INTEGER NOT NULL DEFAULT 0,
		up INTEGER NOT NULL DEFAULT -1, latency_ms INTEGER NOT NULL DEFAULT 0,
		checked_at INTEGER NOT NULL DEFAULT 0, since INTEGER NOT NULL DEFAULT 0,
		last_ok_at INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(oldDDL); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO svc_monitors (name, host, port, interval_sec, enabled, up, latency_ms, checked_at, since, last_ok_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1)`
	if _, err := raw.Exec(ins, "后台", "10.0.0.9", 8080, 30, 1, 1, 7, 111, 111, 111, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(ins, "SSH", "10.0.0.9", 22, 30, 1, 0, 5000, 222, 222, 0, "refused"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(ins, "NAS", "192.168.1.10", 5000, 60, 0, -1, 0, 0, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s2, err := OpenWithKey(path, key) // 重开触发迁移
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })

	servers, err := s2.ListSvcServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("同 host 应合并为一台，实得 %d 台: %+v", len(servers), servers)
	}
	if servers[0].Host != "10.0.0.9" || servers[0].Name != "10.0.0.9" || servers[0].Interval != 30 || !servers[0].Enabled {
		t.Fatalf("第一台服务器应取该组第一行参数: %+v", servers[0])
	}
	if servers[1].Host != "192.168.1.10" || servers[1].Interval != 60 || servers[1].Enabled {
		t.Fatalf("第二台服务器参数不符: %+v", servers[1])
	}

	rows, err := s2.ListSvcPortRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("应迁出 3 个端口: %+v", rows)
	}
	// 顺序：按服务器序 + 端口录入序；业务名与状态随迁
	if rows[0].Name != "后台" || rows[0].Port != 8080 || rows[0].Up != 1 || rows[0].LatencyMs != 7 || rows[0].Host != "10.0.0.9" {
		t.Fatalf("端口 1 迁移不符: %+v", rows[0])
	}
	if rows[1].Name != "SSH" || rows[1].Port != 22 || rows[1].Up != 0 || rows[1].LastError != "refused" {
		t.Fatalf("端口 2 迁移不符: %+v", rows[1])
	}
	if rows[2].Name != "NAS" || rows[2].Port != 5000 || rows[2].Up != -1 || rows[2].Interval != 60 || rows[2].Enabled {
		t.Fatalf("端口 3 迁移不符: %+v", rows[2])
	}

	// 旧表应已删除
	s2.Close()
	raw2, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw2.Close()
	var n int
	if err := raw2.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='svc_monitors'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("迁移后旧表应已 DROP")
	}
}

// TestSvcPortSchemeDomain 请求检测端口行（495）：空 scheme 规范成 tcp 落库；
// scheme/domain 读回；port/协议/域名任一变更重置检测态；EffectiveDomain 空
// 域名回落服务器 host。
func TestSvcPortSchemeDomain(t *testing.T) {
	s := openTestStore(t)
	sid, err := s.CreateSvcServer(&SvcServer{Name: "web", Host: "10.0.0.9", Interval: 30, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := s.CreateSvcPort(&SvcPort{ServerID: sid, Name: "web", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.GetSvcPortRow(pid)
	if err != nil {
		t.Fatal(err)
	}
	if row.Scheme != SvcPortSchemeTCP {
		t.Fatalf("空 scheme 应规范成 tcp: %q", row.Scheme)
	}
	if _, err := s.RecordSvcCheck(pid, true, 5, ""); err != nil {
		t.Fatal(err)
	}

	// 改成 http+域名 → 状态重置回未检测；EffectiveDomain 用端口行域名
	if err := s.UpdateSvcPort(&SvcPort{ID: pid, ServerID: sid, Name: "web", Port: 8080, Scheme: "http", Domain: " a.example.com "}); err != nil {
		t.Fatal(err)
	}
	row, err = s.GetSvcPortRow(pid)
	if err != nil {
		t.Fatal(err)
	}
	if row.Scheme != "http" || row.Domain != "a.example.com" || row.Port != 8080 {
		t.Fatalf("scheme/domain 读回不符: %+v", row)
	}
	if row.Up != -1 {
		t.Fatalf("目标变更应重置检测态: up=%d", row.Up)
	}
	if row.EffectiveDomain("10.0.0.9") != "a.example.com" {
		t.Fatalf("EffectiveDomain 应用端口行域名: %q", row.EffectiveDomain("10.0.0.9"))
	}

	// 清掉域名 → EffectiveDomain 回落服务器 host
	if err := s.UpdateSvcPort(&SvcPort{ID: pid, ServerID: sid, Name: "web", Port: 8080, Scheme: "https"}); err != nil {
		t.Fatal(err)
	}
	row, _ = s.GetSvcPortRow(pid)
	if row.EffectiveDomain("10.0.0.9") != "10.0.0.9" {
		t.Fatalf("空域名应回落服务器 host: %q", row.EffectiveDomain("10.0.0.9"))
	}
}

// TestSvcEventsAvailability 翻转事件与可用率（500）：事件落表与最近列表（带
// JOIN 展示字段）、窗口可用率（down 区间拼接、窗口前初态整段计不通）、换探测
// 目标/删行连带清事件、保留清理。
func TestSvcEventsAvailability(t *testing.T) {
	s := openTestStore(t)
	sid, err := s.CreateSvcServer(&SvcServer{Name: "NAS", Host: "192.168.1.10", Interval: 600, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := s.CreateSvcPort(&SvcPort{ServerID: sid, Name: "Web", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	pid2, err := s.CreateSvcPort(&SvcPort{ServerID: sid, Name: "DB", Port: 3306})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	start := now - 7*86400 + 1000 // 窗口起点后 1000s
	// 造事件走原生 SQL：生产入口只有 RecordSvcCheck 的单事务路径（带任意时间戳
	// 的事件只能直插），可用率/清理逻辑消费这张表本身
	mk := func(portID int64, up int, at int64, errMsg string) {
		if _, err := s.db.Exec(`INSERT INTO svc_events (port_id, server_id, up, latency_ms, error, occurred_at)
			VALUES (?, ?, ?, ?, ?, ?)`, portID, sid, up, 12, errMsg, at); err != nil {
			t.Fatal(err)
		}
	}
	// port1：窗口内 down(start+2000)→up(start+5000)（断 3000s）→down(start+6000)（至今）
	mk(pid, 0, start+2000, "refused")
	mk(pid, 1, start+5000, "")
	mk(pid, 0, start+6000, "timeout")
	// port2：窗口前最后一条是 down → 初态不通，整段计 down
	mk(pid2, 0, start-86400, "old")

	evs, err := s.ListSvcEvents(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 4 {
		t.Fatalf("应有 4 条事件: %d", len(evs))
	}
	first := evs[0]
	if first.Up != 0 || first.PortID != pid || first.ServerName != "NAS" || first.PortName != "Web" ||
		first.Host != "192.168.1.10" || first.Port != 80 || first.Error != "timeout" {
		t.Fatalf("最新事件字段不符: %+v", first)
	}

	av, err := s.SvcAvailability(7)
	if err != nil {
		t.Fatal(err)
	}
	byPort := map[int64]SvcPortAvail{}
	for _, a := range av {
		byPort[a.PortID] = a
	}
	total := int64(7 * 86400)
	a1 := byPort[pid]
	wantDown := int64(3000) + (now - (start + 6000))
	if a1.DownSec != wantDown || a1.TotalSec != total {
		t.Fatalf("port1 可用率不符: want down=%d total=%d, got %+v", wantDown, total, a1)
	}
	wantPct := 100 * (1 - float64(wantDown)/float64(total))
	if diff := a1.Pct - wantPct; diff > 0.01 || diff < -0.01 {
		t.Fatalf("port1 pct 不符: want %.2f got %.2f", wantPct, a1.Pct)
	}
	if a2 := byPort[pid2]; a2.DownSec != total || a2.Pct > 0.01 {
		t.Fatalf("port2 初态不通应整段计 down: %+v", a2)
	}

	// 换端口探测目标 → 该端口事件清空；换服务器地址 → 该台剩余事件清空
	if err := s.UpdateSvcPort(&SvcPort{ID: pid, ServerID: sid, Name: "Web", Port: 8080}); err != nil {
		t.Fatal(err)
	}
	if evs, _ = s.ListSvcEvents(50); len(evs) != 1 {
		t.Fatalf("换端口目标后应剩 1 条: %d", len(evs))
	}
	if err := s.UpdateSvcServer(&SvcServer{ID: sid, Name: "NAS", Host: "192.168.1.11", Interval: 600, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if evs, _ = s.ListSvcEvents(50); len(evs) != 0 {
		t.Fatalf("换服务器地址后应清空: %d", len(evs))
	}

	// 保留清理：按 occurred_at 滚动删
	mk(pid, 0, now-100, "x")
	if n, err := s.DeleteSvcEventsBefore(now); err != nil || n != 1 {
		t.Fatalf("清理应删 1 条: n=%d err=%v", n, err)
	}

	// 删端口连带清事件
	mk(pid, 1, now, "")
	if err := s.DeleteSvcPort(pid); err != nil {
		t.Fatal(err)
	}
	if evs, _ = s.ListSvcEvents(50); len(evs) != 0 {
		t.Fatalf("删端口应连带清事件: %d", len(evs))
	}
}
