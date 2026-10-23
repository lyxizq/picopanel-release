package mail

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

// OutMail 待发邮件：收件人/抄送逗号分隔；HTML 与 Text 至少一项（都有则 alternative）；
// 附件内容已在内存（浏览器上传即组包发送后即弃，不落盘），总量上限 25MB。
type OutMail struct {
	To          string
	CC          string
	Subject     string
	HTML        string
	Text        string
	InReplyTo   string // 回复时原 Message-ID（不带头括号）
	Attachments []OutAttachment
}

// OutAttachment 待发附件。
type OutAttachment struct {
	Name    string
	CT      string
	Content []byte
}

// MaxOutTotal 发件附件总量上限 25MB。
const MaxOutTotal = 25 << 20

// SendMail SMTP 发送（465 隐式 TLS / 587 STARTTLS），成功后 IMAP APPEND 同步到 Sent。
// password 为空沿用账户存的密码。
func SendMail(a store.MailAccount, password string, m *OutMail) error {
	if password == "" {
		password = a.Password
	}
	raw, err := BuildRaw(a.Email, m)
	if err != nil {
		return err
	}
	if err := smtpSubmit(a, password, m, raw); err != nil {
		return err
	}
	// 发送成功 → IMAP APPEND 回 Sent（其他客户端可见已发邮件；找不到 Sent 夹静默跳过）
	if cl, e := DialIMAP(a); e == nil {
		defer cl.Close()
		_ = cl.AppendSent(raw)
	}
	return nil
}

// BuildRaw 组完整 RFC822 报文（发送与测试共用）。
func BuildRaw(from string, m *OutMail) ([]byte, error) {
	tos := splitAddrs(m.To)
	if len(tos) == 0 {
		return nil, fmt.Errorf("请填写收件人")
	}
	if strings.TrimSpace(m.Subject) == "" {
		return nil, fmt.Errorf("请填写主题")
	}
	total := 0
	for _, a := range m.Attachments {
		total += len(a.Content)
	}
	if total > MaxOutTotal {
		return nil, fmt.Errorf("附件总量超 25MB")
	}

	var b strings.Builder
	w := func(format string, args ...any) { b.WriteString(fmt.Sprintf(format, args...)) }
	w("From: %s\r\n", from)
	w("To: %s\r\n", strings.Join(tos, ", "))
	if cc := splitAddrs(m.CC); len(cc) > 0 {
		w("Cc: %s\r\n", strings.Join(cc, ", "))
	}
	w("Subject: %s\r\n", mime.QEncoding.Encode("utf-8", strings.TrimSpace(m.Subject)))
	w("Date: %s\r\n", time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	w("MIME-Version: 1.0\r\n")
	fromAddr := addrOfRFC(from)
	if i := strings.Index(fromAddr, "@"); i > 0 {
		w("Message-ID: <%d.mail@%s>\r\n", time.Now().UnixNano(), fromAddr[i+1:])
	}
	if id := sanitizeMsgID(m.InReplyTo); id != "" {
		w("In-Reply-To: <%s>\r\nReferences: <%s>\r\n", id, id)
	}
	w("Content-Transfer-Encoding: 8bit\r\n")

	text := m.Text
	if text == "" && m.HTML != "" {
		text = tagRe.ReplaceAllString(m.HTML, " ")
	}
	hasAtt := len(m.Attachments) > 0

	boundaryAlt := fmt.Sprintf("=_alt_%d", time.Now().UnixNano())
	boundaryMix := fmt.Sprintf("=_mix_%d", time.Now().UnixNano())
	if hasAtt {
		w("Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundaryMix)
		w("--%s\r\n", boundaryMix)
	}
	if m.HTML != "" {
		w("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundaryAlt)
		mimeText(&b, "text/plain; charset=utf-8", text)
		w("--%s\r\n", boundaryAlt)
		mimeText(&b, "text/html; charset=utf-8", m.HTML)
		w("--%s--\r\n", boundaryAlt)
	} else {
		mimeText(&b, "text/plain; charset=utf-8", text)
	}
	if hasAtt {
		for _, a := range m.Attachments {
			ct := a.CT
			if ct == "" || !strings.Contains(ct, "/") {
				ct = "application/octet-stream"
			}
			w("--%s\r\n", boundaryMix)
			w("Content-Type: %s\r\n", ct)
			w("Content-Disposition: attachment; filename=\"%s\"\r\n", mime.QEncoding.Encode("utf-8", a.Name))
			w("Content-Transfer-Encoding: base64\r\n\r\n")
			enc := base64.StdEncoding.EncodeToString(a.Content)
			for i := 0; i < len(enc); i += 76 {
				end := i + 76
				if end > len(enc) {
					end = len(enc)
				}
				w("%s\r\n", enc[i:end])
			}
		}
		w("--%s--\r\n", boundaryMix)
	}
	return []byte(b.String()), nil
}

func mimeText(b *strings.Builder, ct, content string) {
	b.WriteString("Content-Type: " + ct + "\r\n\r\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\r\n") {
		b.WriteString("\r\n")
	}
}

// smtpSubmit SMTP 提交（登录、RCPT、DATA）。
func smtpSubmit(a store.MailAccount, password string, m *OutMail, raw []byte) error {
	addr := net.JoinHostPort(a.SMTPHost, strconv.Itoa(a.SMTPPort))
	tos := splitAddrs(m.To)
	if cc := splitAddrs(m.CC); len(cc) > 0 {
		tos = append(tos, cc...)
	}
	var conn net.Conn
	var err error
	d := net.Dialer{Timeout: dialTimeout}
	if a.SMTPSSL {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: a.SMTPHost})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("连接 SMTP %s 失败: %w", addr, err)
	}
	cl, err := smtp.NewClient(conn, a.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP 握手失败: %w", err)
	}
	defer conn.Close()
	if !a.SMTPSSL {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(&tls.Config{ServerName: a.SMTPHost}); err != nil {
				return fmt.Errorf("STARTTLS 升级失败: %w", err)
			}
		}
	}
	if err := cl.Auth(smtp.PlainAuth("", a.Email, password, a.SMTPHost)); err != nil {
		return fmt.Errorf("SMTP 认证失败（QQ/163 等需填授权码而非登录密码）: %w", err)
	}
	if err := cl.Mail(a.Email); err != nil {
		return fmt.Errorf("SMTP MAIL 失败: %w", err)
	}
	for _, to := range tos {
		if err := cl.Rcpt(to); err != nil {
			return fmt.Errorf("SMTP RCPT %s 失败: %w", to, err)
		}
	}
	wr, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := wr.Write(raw); err != nil {
		wr.Close()
		return err
	}
	if err := wr.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

// TestSMTP SMTP 认证连通测试（NOOP 后 QUIT，不发信）。
func TestSMTP(a store.MailAccount, password string) error {
	if password == "" {
		password = a.Password
	}
	addr := net.JoinHostPort(a.SMTPHost, strconv.Itoa(a.SMTPPort))
	var conn net.Conn
	var err error
	d := net.Dialer{Timeout: dialTimeout}
	if a.SMTPSSL {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: a.SMTPHost})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("连接 SMTP %s 失败: %w", addr, err)
	}
	cl, err := smtp.NewClient(conn, a.SMTPHost)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP 握手失败: %w", err)
	}
	defer conn.Close()
	if !a.SMTPSSL {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(&tls.Config{ServerName: a.SMTPHost}); err != nil {
				return fmt.Errorf("STARTTLS 失败: %w", err)
			}
		}
	}
	if err := cl.Auth(smtp.PlainAuth("", a.Email, password, a.SMTPHost)); err != nil {
		return fmt.Errorf("SMTP 认证失败（QQ/163 等需填授权码而非登录密码）: %w", err)
	}
	_ = cl.Noop()
	return cl.Quit()
}

// splitAddrs 拆收件人串（逗号/分号/空白分隔，剥显示名取纯地址、去重）。
func splitAddrs(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if a, err := mail.ParseAddress(part); err == nil {
			part = a.Address
		}
		part = strings.Trim(part, "<>")
		if part == "" || seen[part] || !strings.Contains(part, "@") {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func addrOfRFC(s string) string {
	if a, err := mail.ParseAddress(s); err == nil {
		return a.Address
	}
	return strings.Trim(s, "<>")
}

// sanitizeMsgID Message-ID 清洗：剥尖括号后只留可打印 ASCII，出现任何其他
// 字符（控制符/空格/高位码点）整值作废。In-Reply-To/References 是信头注入
// 的口子——值里的 \r\n 曾可注入 Bcc 等任意信头（2026-10-04 安全审查），
// 宁可丢线程关系不放注入。
func sanitizeMsgID(s string) string {
	s = strings.Trim(s, "<> ")
	if s == "" {
		return ""
	}
	for _, r := range s {
		if r <= 32 || r >= 127 {
			return ""
		}
	}
	return s
}
