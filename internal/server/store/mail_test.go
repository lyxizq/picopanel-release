package store

import (
	"errors"
	"strings"
	"testing"
)

// TestMailAccountCRUD 邮件账户全链路：建（密码加密落库）→读（必须解密还原明文，
// 漏 unseal 会把密文当密码去登录——真实事故回归）→改（留空沿用原文）→排序→删。
func TestMailAccountCRUD(t *testing.T) {
	s := openTestStore(t)

	id, err := s.CreateMailAccount(&MailAccount{
		Name: "主邮箱", Email: "me@163.com", Provider: "163",
		IMAPHost: "imap.163.com", IMAPPort: 993, SMTPHost: "smtp.163.com", SMTPPort: 465, SMTPSSL: true,
		Password: "abcd1234efgh5678", PollSec: 300,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 库内必须密文（enc:v1: 前缀）
	var dbPwd string
	if err := s.db.QueryRow(`SELECT password FROM mail_accounts WHERE id = ?`, id).Scan(&dbPwd); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dbPwd, "enc:v1:") {
		t.Fatalf("密码应加密落库，实际: %q", dbPwd)
	}
	if dbPwd == "abcd1234efgh5678" {
		t.Fatal("密码明文入库")
	}

	// 读回必须解密成原文（含列表与单查两条路径）
	a, err := s.GetMailAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	if a.Password != "abcd1234efgh5678" {
		t.Fatalf("Get 读回应为明文原文，实际: %q", a.Password)
	}
	if !a.HasAuth {
		t.Fatal("has_auth 应为 true")
	}
	if a.PollSec != 300 {
		t.Fatalf("poll_sec 应为 300: %d", a.PollSec)
	}
	// 越界回落默认 600（clamp）
	a.PollSec = 5
	if err := s.UpdateMailAccount(a); err != nil {
		t.Fatal(err)
	}
	a3b, _ := s.GetMailAccount(id)
	if a3b.PollSec != 600 {
		t.Fatalf("越界 poll_sec 应回落 600: %d", a3b.PollSec)
	}
	// 未配置读回回落 600
	if _, err := s.db.Exec(`UPDATE mail_accounts SET poll_sec = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	a3c, _ := s.GetMailAccount(id)
	if a3c.PollSec != 600 {
		t.Fatalf("未配置 poll_sec 读回应回落 600: %d", a3c.PollSec)
	}
	list, err := s.ListMailAccounts()
	if err != nil || len(list) != 1 {
		t.Fatalf("列表异常: %v %d", err, len(list))
	}
	if list[0].Password != "abcd1234efgh5678" {
		t.Fatalf("List 读回应为明文原文，实际: %q", list[0].Password)
	}

	// 更新留空 = 沿用原密码（不得双重加密/还原不出）
	a.Name = "改名"
	a.Password = ""
	if err := s.UpdateMailAccount(a); err != nil {
		t.Fatal(err)
	}
	a2, _ := s.GetMailAccount(id)
	if a2.Password != "abcd1234efgh5678" {
		t.Fatalf("留空沿用后应仍为明文原文，实际: %q", a2.Password)
	}

	// 更新带新密码
	a2.Password = "newpass88888888"
	if err := s.UpdateMailAccount(a2); err != nil {
		t.Fatal(err)
	}
	a3, _ := s.GetMailAccount(id)
	if a3.Password != "newpass88888888" {
		t.Fatalf("改密后应读回新明文，实际: %q", a3.Password)
	}

	// 删除（含不存在分支）
	if err := s.DeleteMailAccount(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMailAccount(id); err != ErrMailAccountNotFound {
		t.Fatalf("删除后应 ErrMailAccountNotFound，实际: %v", err)
	}
}

// TestMailHeadersCache 信头缓存：整夹覆盖→按日期倒序列出→已读同步。
func TestMailHeadersCache(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateMailAccount(&MailAccount{Email: "x@qq.com", IMAPHost: "imap.qq.com", IMAPPort: 993,
		SMTPHost: "smtp.qq.com", SMTPPort: 465, SMTPSSL: true, Password: "p"})

	rows := []MailHeader{
		{UID: 11, FromName: "甲", Subject: "早", DateUnix: 100, Seen: false, HasAtt: true, Size: 2048},
		{UID: 12, FromName: "乙", Subject: "晚", DateUnix: 200, Seen: true},
	}
	if err := s.ReplaceMailHeaders(id, "INBOX", rows); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListMailHeaders(id, "INBOX", 50)
	if err != nil || len(got) != 2 {
		t.Fatalf("列表异常: %v %d", err, len(got))
	}
	if got[0].UID != 12 || got[1].UID != 11 {
		t.Fatalf("应按日期倒序: %+v", got)
	}
	if !got[1].HasAtt || got[1].Size != 2048 {
		t.Fatalf("附件/大小字段丢失: %+v", got[1])
	}

	// 已读状态同步 + 覆盖式重写（旧行不残留）
	if err := s.SetMailHeaderSeen(id, "INBOX", 11, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListMailHeaders(id, "INBOX", 50)
	for _, h := range got {
		if h.UID == 11 && !h.Seen {
			t.Fatal("已读同步失败")
		}
	}
	if err := s.ReplaceMailHeaders(id, "INBOX", rows[:1]); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListMailHeaders(id, "INBOX", 50)
	if len(got) != 1 || got[0].UID != 11 {
		t.Fatalf("覆盖式重写应清旧行: %+v", got)
	}

	// 删账户连带清缓存
	_ = s.DeleteMailAccount(id)
	got, _ = s.ListMailHeaders(id, "INBOX", 50)
	if len(got) != 0 {
		t.Fatalf("删账户后缓存应清空: %+v", got)
	}
}

// TestMailHeadersUpsertPageDelete 轮询入库三件套：增量合并不清既有行、
// before_uid 分页、按 uid 删行（2026-10-03 列表本地库直出链路）。
func TestMailHeadersUpsertPageDelete(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.CreateMailAccount(&MailAccount{Email: "y@qq.com", IMAPHost: "imap.qq.com", IMAPPort: 993,
		SMTPHost: "smtp.qq.com", SMTPPort: 465, SMTPSSL: true, Password: "p"})

	// 刷新拉过两封（uid 11/12）
	if err := s.ReplaceMailHeaders(id, "INBOX", []MailHeader{
		{UID: 11, Subject: "早", DateUnix: 100},
		{UID: 12, Subject: "晚", DateUnix: 200},
	}); err != nil {
		t.Fatal(err)
	}
	// 轮询增量合并：新邮件 13 + 已知 11 的已读变化；不含 12
	if err := s.UpsertMailHeaders(id, "INBOX", []MailHeader{
		{UID: 13, Subject: "新", DateUnix: 300, Seen: false},
		{UID: 11, Subject: "早", DateUnix: 100, Seen: true},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListMailHeaders(id, "INBOX", 50)
	if err != nil || len(got) != 3 {
		t.Fatalf("增量合并应保留未提及行: %v %d", err, len(got))
	}
	if got[0].UID != 13 || got[2].UID != 11 || !got[2].Seen {
		t.Fatalf("合并结果异常（新邮件置顶+已读更新）: %+v", got)
	}

	// 分页：before_uid=13 取更早，倒序不变
	page, err := s.ListMailHeadersPage(id, "INBOX", 50, 13)
	if err != nil || len(page) != 2 || page[0].UID != 12 || page[1].UID != 11 {
		t.Fatalf("before_uid 分页异常: %v %+v", err, page)
	}
	// limit+1 探测 has_more
	page, _ = s.ListMailHeadersPage(id, "INBOX", 2, 0)
	if len(page) != 2 || page[0].UID != 13 {
		t.Fatalf("首页分页异常: %+v", page)
	}

	// 删除同步：删 13 后列表不再出现
	if err := s.DeleteMailHeaders(id, "INBOX", []int64{13}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListMailHeaders(id, "INBOX", 50)
	if len(got) != 2 {
		t.Fatalf("删行后应剩 2 封: %+v", got)
	}
}

// TestMailAccountOrderAndLimit 排序保存与预设口径。
func TestMailAccountOrderAndLimit(t *testing.T) {
	s := openTestStore(t)
	i1, _ := s.CreateMailAccount(&MailAccount{Email: "a@x.com", IMAPHost: "h", IMAPPort: 993, SMTPHost: "h", SMTPPort: 465, Password: "p"})
	i2, _ := s.CreateMailAccount(&MailAccount{Email: "b@x.com", IMAPHost: "h", IMAPPort: 993, SMTPHost: "h", SMTPPort: 465, Password: "p"})
	if err := s.UpdateMailAccountsOrder([]int64{i2, i1}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListMailAccounts()
	if list[0].ID != i2 || list[1].ID != i1 {
		t.Fatalf("排序未生效: %+v", list)
	}
	if MailProviderByKey("163").IMAPHost != "imap.163.com" || MailProviderByKey("163").SMTPPort != 465 {
		t.Fatal("163 预设口径不对")
	}
}

// TestMailContactCRUD 通讯录（506）：增（email 归一小写）→查→改→撞 UNIQUE→删（幂等）。
func TestMailContactCRUD(t *testing.T) {
	s := openTestStore(t)

	ct, err := s.CreateMailContact("张三", "  Zhang@Example.COM ")
	if err != nil {
		t.Fatal(err)
	}
	if ct.Email != "zhang@example.com" {
		t.Fatalf("email 应归一小写: %q", ct.Email)
	}
	if ct.Name != "张三" || ct.ID <= 0 || ct.CreatedAt <= 0 {
		t.Fatalf("字段应完整: %+v", ct)
	}

	// 同邮箱（不同大小写）判重
	if _, err := s.CreateMailContact("李四", "ZHANG@example.com"); !errors.Is(err, ErrMailContactExists) {
		t.Fatalf("重复邮箱应报 ErrMailContactExists: %v", err)
	}

	list, err := s.ListMailContacts()
	if err != nil || len(list) != 1 || list[0].ID != ct.ID {
		t.Fatalf("列表应 1 条: %+v %v", list, err)
	}

	if up, err := s.UpdateMailContact(ct.ID, "张三丰", "zsf@example.com"); err != nil || up.Name != "张三丰" || up.Email != "zsf@example.com" {
		t.Fatalf("更新失败: %+v %v", up, err)
	}
	// 改成别人的邮箱也判重
	other, err := s.CreateMailContact("李四", "lisi@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMailContact(other.ID, "李四", "zsf@example.com"); !errors.Is(err, ErrMailContactExists) {
		t.Fatalf("改成已占用邮箱应报错: %v", err)
	}
	if _, err := s.UpdateMailContact(9999, "无", "no@x.com"); !errors.Is(err, ErrMailContactNotFound) {
		t.Fatalf("改不存在应报 ErrMailContactNotFound: %v", err)
	}
	if err := s.DeleteMailContact(other.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMailContact(other.ID); err != nil { // 幂等
		t.Fatalf("重复删除应幂等: %v", err)
	}
	list, _ = s.ListMailContacts()
	if len(list) != 1 {
		t.Fatalf("删除后应剩 1 条: %d", len(list))
	}
}

// TestMailSignature 个人签名 kv（507）：未配置空串 → 写入读回 → 覆盖。
func TestMailSignature(t *testing.T) {
	s := openTestStore(t)
	sig, err := s.MailSignature()
	if err != nil || sig != "" {
		t.Fatalf("未配置应为空串: %q %v", sig, err)
	}
	if err := s.SetMailSignature("张三 · 13800000000"); err != nil {
		t.Fatal(err)
	}
	if sig, err = s.MailSignature(); err != nil || sig != "张三 · 13800000000" {
		t.Fatalf("读回不符: %q %v", sig, err)
	}
	if err := s.SetMailSignature("v2"); err != nil {
		t.Fatal(err)
	}
	if sig, _ = s.MailSignature(); sig != "v2" {
		t.Fatalf("应覆盖: %q", sig)
	}
}
