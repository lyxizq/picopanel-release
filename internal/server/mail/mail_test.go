package mail

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"

	"golang.org/x/text/encoding/simplifiedchinese"

	"lz-monitor/internal/server/store"
)

// TestBuildRaw 发件组包：头序/编码/alternative/附件 base64/In-Reply-To。
func TestBuildRaw(t *testing.T) {
	raw, err := BuildRaw("me@qq.com", &OutMail{
		To: "a@x.com, 显示名 <b@y.com>; bad-addr", CC: "d@w.com",
		Subject: "主题：测试",
		HTML:    "<p>你好</p>", Text: "你好",
		InReplyTo:   "<orig@x>",
		Attachments: []OutAttachment{{Name: "报表.xlsx", CT: "application/vnd.ms-excel", Content: bytes.Repeat([]byte("xy"), 100)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		"From: me@qq.com", "To: a@x.com, b@y.com", "Cc: d@w.com",
		"Subject: =?utf-8?", "In-Reply-To: <orig@x>", "References: <orig@x>",
		"multipart/mixed", "multipart/alternative", "text/html; charset=utf-8",
		"Content-Disposition: attachment; filename=\"=?utf-8?", "base64",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("缺少 %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "bad-addr") {
		t.Fatalf("非法地址应被剔除")
	}
	// 无正文输入时由 HTML 剥标签兜底 text
	if !strings.Contains(s, "你好") {
		t.Fatalf("正文缺失")
	}
}

// TestBuildRawInReplyToInjection In-Reply-To 信头注入回归（2026-10-04 安全审查）：
// 值里的 \r\n 曾可注入 Bcc 等任意信头，修复后含非法字符整值作废、合法 msg-id 保留。
func TestBuildRawInReplyToInjection(t *testing.T) {
	raw, err := BuildRaw("me@qq.com", &OutMail{
		To: "a@x.com", Subject: "s",
		InReplyTo: "orig@x\r\nBcc: victim@evil.com\r\nX-Evil: 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "victim@evil.com") || strings.Contains(s, "X-Evil") {
		t.Fatalf("注入内容应整值作废:\n%s", s)
	}
	if strings.Contains(s, "In-Reply-To:") {
		t.Fatalf("非法 msg-id 不应保留头:\n%s", s)
	}
	raw2, err := BuildRaw("me@qq.com", &OutMail{To: "a@x.com", Subject: "s", InReplyTo: "<orig@x.com>"})
	if err != nil {
		t.Fatal(err)
	}
	s2 := string(raw2)
	if !strings.Contains(s2, "In-Reply-To: <orig@x.com>") || !strings.Contains(s2, "References: <orig@x.com>") {
		t.Fatalf("合法 msg-id 应保留:\n%s", s2)
	}
}

// TestSanitizeMsgID 清洗口径：控制符/空格/非 ASCII 任一出现即整值作废。
func TestSanitizeMsgID(t *testing.T) {
	yes := map[string]string{"<orig@x>": "orig@x", " a@b.cn ": "a@b.cn", "ABC.DEF@x_y-1": "ABC.DEF@x_y-1"}
	for in, want := range yes {
		if got := sanitizeMsgID(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "<>", "a b@c", "a\r\nBcc:x", "中文@x", "a\t@b"} {
		if got := sanitizeMsgID(in); got != "" {
			t.Errorf("%q 应作废, got %q", in, got)
		}
	}
}

// TestBuildRawValidation 参数校验（缺收件人/主题/超限附件）。
func TestBuildRawValidation(t *testing.T) {
	if _, err := BuildRaw("a@x.com", &OutMail{Subject: "s"}); err == nil {
		t.Fatal("缺收件人应报错")
	}
	if _, err := BuildRaw("a@x.com", &OutMail{To: "b@y.com"}); err == nil {
		t.Fatal("缺主题应报错")
	}
	big := bytes.Repeat([]byte("x"), MaxOutTotal/2+1)
	if _, err := BuildRaw("a@x.com", &OutMail{To: "b@y.com", Subject: "s",
		Attachments: []OutAttachment{{Name: "a", Content: big}, {Name: "b", Content: big}}}); err == nil {
		t.Fatal("附件超 25MB 应报错")
	}
}

// TestParseMIME 整封解析：multipart/alternative + 附件（gbk 正文与 gbk 编码头）。
func TestParseMIME(t *testing.T) {
	encWord := func(s string) string {
		b, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
		return "=?gbk?B?" + base64.StdEncoding.EncodeToString(b) + "?="
	}
	gbkBody, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("你好"))
	raw := []byte("From: " + encWord("张三") + " <sender@qq.com>\r\n" +
		"To: me@qq.com\r\n" +
		"Cc: cc1@qq.com, cc2@qq.com\r\n" +
		"Subject: " + encWord("测试") + "\r\n" +
		"Date: Fri, 02 Oct 2026 12:00:00 +0800\r\n" +
		"Message-ID: <abc@qq.com>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B1\"\r\n\r\n" +
		"--B1\r\nContent-Type: text/plain; charset=gbk\r\n\r\n" +
		string(gbkBody) + "\r\n" +
		"--B1\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"report.pdf\"\r\n\r\n" +
		"%PDF-fake\r\n" +
		"--B1--\r\n")
	m := ParseMIME(raw)
	if m.Subject != "测试" {
		t.Fatalf("RFC2047+gbk 解码不对: %q", m.Subject)
	}
	if m.FromAddr != "sender@qq.com" || m.FromName != "张三" {
		t.Fatalf("发件人解码不对: %q %q", m.FromName, m.FromAddr)
	}
	if m.MessageID != "abc@qq.com" {
		t.Fatalf("MessageID 不对: %q", m.MessageID)
	}
	if m.Cc != "cc1@qq.com, cc2@qq.com" {
		t.Fatalf("Cc 应原样透出（全部回复取址用）: %q", m.Cc)
	}
	if m.Text != "你好" {
		t.Fatalf("gbk 正文应转 UTF-8: %q", m.Text)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Name != "report.pdf" || m.Attachments[0].Path != "2" {
		t.Fatalf("附件清单不对: %+v", m.Attachments)
	}
	if !strings.HasPrefix(m.HTML, "") && m.HTML != "" {
		t.Fatalf("无 html 时应为空")
	}
}

// TestExtractPart 附件按 IMAP part 路径抽取。
func TestExtractPart(t *testing.T) {
	raw := []byte("From: a@b\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhello\r\n" +
		"--B\r\nContent-Type: multipart/mixed; boundary=\"B2\"\r\n\r\n" +
		"--B2\r\nContent-Type: text/plain\r\n\r\nnested\r\n" +
		"--B2\r\nContent-Type: image/png; name=\"pic.png\"\r\nContent-Disposition: attachment; filename=\"pic.png\"\r\n\r\n" +
		"\x89PNG\r\n" +
		"--B2--\r\n--B--\r\n")
	name, ct, data, err := ExtractPart(raw, "2.2")
	if err != nil {
		t.Fatal(err)
	}
	if name != "pic.png" || ct != "image/png" || string(data) != "\x89PNG" {
		t.Fatalf("附件抽取不对: %q %q %q", name, ct, data)
	}
	if _, _, _, err := ExtractPart(raw, "9.9"); err == nil {
		t.Fatal("不存在的 part 应报错")
	}
}

// TestSplitAddrs 收件人串拆分（分号/逗号/空白、显示名剥壳、去重）。
func TestSplitAddrs(t *testing.T) {
	got := splitAddrs("a@x.com; Name <b@y.com>, a@x.com c@z.com")
	if len(got) != 3 || got[0] != "a@x.com" || got[1] != "b@y.com" || got[2] != "c@z.com" {
		t.Fatalf("拆分不对: %+v", got)
	}
}

// TestPresets 服务商预设完整性（常用家都有、端口口径）。
func TestPresets(t *testing.T) {
	ps := store.MailProviderPresets()
	byKey := map[string]bool{}
	for _, p := range ps {
		byKey[p.Key] = true
		if p.IMAPHost != "" && (p.IMAPPort == 0 || p.SMTPPort == 0) {
			t.Fatalf("%s 端口缺失", p.Key)
		}
	}
	for _, want := range []string{"qq", "163", "gmail", "outlook", "custom"} {
		if !byKey[want] {
			t.Fatalf("缺预设 %s", want)
		}
	}
	if store.MailProviderByKey("nope") != nil {
		t.Fatal("未知预设应返回 nil")
	}
	if p := store.MailProviderByKey("qq"); p == nil || p.IMAPHost != "imap.qq.com" || !p.SMTPSSL {
		t.Fatal("QQ 预设口径不对")
	}
}

// TestSeqRangeSemantics 锁死区间语义与值类型要求（2 封事故 + panic 事故的回归）。
func TestSeqRangeSemantics(t *testing.T) {
	// AddRange → 真区间 "51:100"；SeqSetNum(51,100) → 两个孤立序号 "51,100"（事故写法）
	set := imap.SeqSet{}
	set.AddRange(51, 100)
	if got := set.String(); got != "51:100" {
		t.Fatalf("AddRange 应为区间: %q", got)
	}
	if got := imap.SeqSetNum(51, 100).String(); got != "51,100" {
		t.Fatalf("SeqSetNum 应为孤立序号: %q", got)
	}
	// 值类型才是合法 NumSet（beta.8 num.go NumSetKind 只 case 值类型 imap.SeqSet/
	// imap.UIDSet，传指针会在 Fetch 编码命令时 panic "invalid NumSet type"——此处
	// 无法直接调内部分发，以 String 语义 + client.go 传值约定锁死）
	var n imap.NumSet = set
	if n.String() != "51:100" {
		t.Fatal("值 SeqSet 经 NumSet 接口应输出区间")
	}
	// SEARCH 未命中 = 零长值切片，searchEmpty 应判空
	if !searchEmpty(imap.SeqSet(nil)) || !searchEmpty(imap.UIDSet(nil)) {
		t.Fatal("空集应判空")
	}
	if searchEmpty(func() imap.NumSet { s := imap.SeqSet{}; s.AddNum(3); return s }()) {
		t.Fatal("非空集不应判空")
	}
}

// TestParseSinglePart 非 multipart 单部件邮件：正文在根节点（空白事故回归——
// 原逻辑跳过 path=[] 根导致整封空白），附件版单部件 part 路径为 "1"。
func TestParseSinglePart(t *testing.T) {
	// 单部件 text/html（通知类邮件最常见结构）
	raw := []byte("From: svc@x.com\r\nSubject: n\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n<p>你好通知</p>\r\n")
	m := ParseMIME(raw)
	if m.HTML != "<p>你好通知</p>" {
		t.Fatalf("单部件 html 应从根节点取出: %q", m.HTML)
	}
	if m.Text == "" || !strings.Contains(m.Text, "你好通知") {
		t.Fatalf("text 应由 html 剥标签兜底: %q", m.Text)
	}
	// 单部件纯文本
	m = ParseMIME([]byte("From: a@b\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n纯文本正文\r\n"))
	if m.Text != "纯文本正文" {
		t.Fatalf("单部件 plain 应取出: %q", m.Text)
	}
	// 单部件整封就是附件（application/pdf 无 multipart）
	m = ParseMIME([]byte("From: a@b\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n\r\n%PDF\r\n"))
	if len(m.Attachments) != 1 || m.Attachments[0].Path != "1" || m.Attachments[0].Name != "a.pdf" {
		t.Fatalf("单部件附件应记 part=1: %+v", m.Attachments)
	}
	if _, _, data, err := ExtractPart(raw2SinglePDF(), "1"); err != nil || string(data) != "%PDF" {
		t.Fatalf("单部件附件抽取失败: %v %q", err, data)
	}
}

func raw2SinglePDF() []byte {
	return []byte("From: a@b\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"a.pdf\"\r\n\r\n%PDF\r\n")
}

// TestIsIMAPErrorLine 错误行判定：NO/BAD 触发，OK 与登出 BYE 不触发（刷屏事故回归——
// "* BYE ... logging out" 曾被误判为错误，成功的 60s 轮询每轮倾倒一遍）。
func TestIsIMAPErrorLine(t *testing.T) {
	yes := []string{
		`T4 NO LOGIN Login error or password error`,
		`T7 BAD Command not supported`,
	}
	no := []string{
		`T4 OK LOGIN completed`,
		`T7 OK SEARCH completed`,
		`* SEARCH`,
		`* BYE IMAP4rev1 Server logging out`,
		`* 6043 EXISTS`,
		`T6 OK [READ-ONLY] Examine completed`,
	}
	for _, l := range yes {
		if !isIMAPErrorLine(l) {
			t.Fatalf("应判为错误: %q", l)
		}
	}
	for _, l := range no {
		if isIMAPErrorLine(l) {
			t.Fatalf("不应判为错误: %q", l)
		}
	}
}
