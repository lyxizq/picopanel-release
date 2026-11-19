package store

import (
	"testing"
)

// TestRecordAiStatusLastOKAt aiusage 休眠 bug 回归（503）：失败只写 last_error、
// 绝不推进 last_ok_at（它专指最近成功——失败也推进会让账户看着新鲜实际一直
// 失败）；成功推进并清错误；同错误不重复写。
func TestRecordAiStatusLastOKAt(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateAiAccount(&AiAccount{Name: "智谱", Provider: "zhipu", APIKey: "zk-x"})
	if err != nil {
		t.Fatal(err)
	}

	// 失败：last_ok_at 保持 0，只落 last_error
	if err := s.RecordAiAccountStatus(id, false, "boom"); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetAiAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	if a.LastOKAt != 0 || a.LastError != "boom" {
		t.Fatalf("失败不应推进 last_ok_at: ok=%d err=%q", a.LastOKAt, a.LastError)
	}

	// 同错误重复失败：不重写（last_ok_at 仍 0）
	if err := s.RecordAiAccountStatus(id, false, "boom"); err != nil {
		t.Fatal(err)
	}
	if a, _ = s.GetAiAccount(id); a.LastOKAt != 0 {
		t.Fatalf("重复失败也不应推进: %d", a.LastOKAt)
	}

	// 成功：推进 last_ok_at 并清错误
	if err := s.RecordAiAccountStatus(id, true, ""); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAiAccount(id)
	if a.LastOKAt == 0 || a.LastError != "" {
		t.Fatalf("成功应推进并清错误: ok=%d err=%q", a.LastOKAt, a.LastError)
	}

	// 成功后再失败：last_ok_at 停在成功时刻不回退
	okAt := a.LastOKAt
	if err := s.RecordAiAccountStatus(id, false, "down"); err != nil {
		t.Fatal(err)
	}
	if a, _ = s.GetAiAccount(id); a.LastOKAt != okAt || a.LastError != "down" {
		t.Fatalf("失败不应动 last_ok_at: ok=%d err=%q", a.LastOKAt, a.LastError)
	}
}

// TestRecordMailStatusLastOKAt mail 同款回归（503），口径同上。
func TestRecordMailStatusLastOKAt(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateMailAccount(&MailAccount{
		Name: "主邮箱", Email: "me@163.com", Provider: "163",
		IMAPHost: "imap.163.com", IMAPPort: 993, SMTPHost: "smtp.163.com", SMTPPort: 465, SMTPSSL: true,
		Password: "abcd1234efgh5678", PollSec: 300,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RecordMailAccountStatus(id, false, "auth failed"); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetMailAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	if a.LastOKAt != 0 || a.LastError != "auth failed" {
		t.Fatalf("失败不应推进 last_ok_at: ok=%d err=%q", a.LastOKAt, a.LastError)
	}

	if err := s.RecordMailAccountStatus(id, true, ""); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetMailAccount(id)
	if a.LastOKAt == 0 || a.LastError != "" {
		t.Fatalf("成功应推进并清错误: ok=%d err=%q", a.LastOKAt, a.LastError)
	}
	okAt := a.LastOKAt
	if err := s.RecordMailAccountStatus(id, false, "imap down"); err != nil {
		t.Fatal(err)
	}
	if a, _ = s.GetMailAccount(id); a.LastOKAt != okAt || a.LastError != "imap down" {
		t.Fatalf("失败不应动 last_ok_at: ok=%d err=%q", a.LastOKAt, a.LastError)
	}
}
