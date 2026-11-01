package store

import (
	"strings"
	"testing"
)

// TestLoginLog 登录日志：只保留最近 20 条，新→旧返回。
func TestLoginLog(t *testing.T) {
	s := openTestStore(t)

	// 空库无记录
	logs, err := s.ListLoginLog()
	if err != nil || len(logs) != 0 {
		t.Fatalf("空库应无记录: %v err=%v", logs, err)
	}

	// 25 次登录 → 只剩最近 20 条
	for i := 0; i < 25; i++ {
		if err := s.RecordLogin("admin", "192.168.1.100"); err != nil {
			t.Fatal(err)
		}
	}
	logs, err = s.ListLoginLog()
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 20 {
		t.Fatalf("应保留 20 条: %d", len(logs))
	}

	// 记录字段与倒序（id 最大在最前）
	var count int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM login_log`).Scan(&count); err != nil || count != 20 {
		t.Fatalf("表内应 20 行: n=%d err=%v", count, err)
	}
	if logs[0].ID <= logs[19].ID {
		t.Fatalf("应新→旧排序: 首=%d 末=%d", logs[0].ID, logs[19].ID)
	}
	if logs[0].Username != "admin" || logs[0].IP != "192.168.1.100" || logs[0].TS == 0 {
		t.Fatalf("记录字段: %+v", logs[0])
	}
}

// TestLoginMessage 登录同时落站内消息：类型 login、未读、文案同推送口径，
// 且不随登录日志 20 条裁剪。
func TestLoginMessage(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 25; i++ {
		if err := s.RecordLogin("admin", "10.0.0.9"); err != nil {
			t.Fatal(err)
		}
	}

	// 25 次登录 → 25 条 login 消息（不裁）
	p, err := s.ListMessages("login", false, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 25 || len(p.Items) != 25 {
		t.Fatalf("应有 25 条登录消息: total=%d len=%d", p.Total, len(p.Items))
	}

	// 未读角标计入
	n, err := s.UnreadCount()
	if err != nil || n != 25 {
		t.Fatalf("未读应 25: n=%d err=%v", n, err)
	}

	// 文案与字段：标题「登录提醒：用户名」，正文含账号/时间/IP
	m := p.Items[0]
	if m.Type != "login" || m.Title != "登录提醒：admin" || m.ReadAt != nil || m.CreatedAt == 0 {
		t.Fatalf("消息字段: %+v", m)
	}
	if !strings.Contains(m.Content, "账号 admin 于 ") ||
		!strings.Contains(m.Content, "成功登录，来源 IP：10.0.0.9") {
		t.Fatalf("消息正文口径: %q", m.Content)
	}

	// 类型筛选隔离：按告警查不到登录消息
	pa, err := s.ListMessages("alert", false, 1, 100)
	if err != nil || pa.Total != 0 {
		t.Fatalf("告警筛选应 0: total=%d err=%v", pa.Total, err)
	}
}
