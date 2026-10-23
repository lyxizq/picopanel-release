package mail

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
)

// fakeMsg 假 INBOX 里的一封信。
type fakeMsg struct {
	uid     int64
	subject string
	from    string
	seen    bool
}

func fm(uid int64, subject string) fakeMsg {
	return fakeMsg{uid: uid, subject: subject, from: "sender" + strconv.FormatInt(uid, 10) + "@b.c"}
}

func mSeen(uid int64, subject string) fakeMsg {
	mm := fm(uid, subject)
	mm.seen = true
	return mm
}

// fakeMailIMAP 支撑 Unread 与 List 两条链路的最小 IMAP 服务器（334/542 全链路
// 测试）：信箱内容可整组替换；FETCH 回 ENVELOPE/FLAGS/UID（List 解析够用，
// BODYSTRUCTURE 缺省=无附件）。
type fakeMailIMAP struct {
	ln   net.Listener
	mu   sync.Mutex
	msgs []fakeMsg
}

func (f *fakeMailIMAP) setMsgs(msgs ...fakeMsg) {
	f.mu.Lock()
	f.msgs = msgs
	f.mu.Unlock()
}

func (f *fakeMailIMAP) snapshot() []fakeMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeMsg, len(f.msgs))
	copy(out, f.msgs)
	return out
}

func (f *fakeMailIMAP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeMailIMAP) handle(conn net.Conn) {
	defer conn.Close()
	_, _ = conn.Write([]byte("* OK fake ready\r\n"))
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		tag, rest, _ := strings.Cut(line, " ")
		cmd := strings.ToUpper(rest)
		msgs := f.snapshot()
		switch {
		case strings.Contains(cmd, "CAPABILITY"):
			_, _ = conn.Write([]byte("* CAPABILITY IMAP4rev1\r\n" + tag + " OK\r\n"))
		case strings.Contains(cmd, "STARTTLS"):
			_, _ = conn.Write([]byte(tag + " NO\r\n")) // DialIMAP 明文回退
		case strings.Contains(cmd, "LOGIN"):
			_, _ = conn.Write([]byte(tag + " OK\r\n"))
		case strings.Contains(cmd, "SELECT"), strings.Contains(cmd, "EXAMINE"):
			// List 链路只读打开发 EXAMINE，必须同样回 EXISTS（缺了会被当成空夹）
			_, _ = conn.Write([]byte(fmt.Sprintf("* %d EXISTS\r\n* OK [UIDVALIDITY 1]\r\n", len(msgs)) + tag + " OK\r\n"))
		case strings.Contains(cmd, "SEARCH"):
			var seqs []string
			for i, mm := range msgs {
				if !mm.seen {
					seqs = append(seqs, strconv.Itoa(i+1))
				}
			}
			_, _ = conn.Write([]byte("* SEARCH " + strings.Join(seqs, " ") + "\r\n" + tag + " OK\r\n"))
		case strings.Contains(cmd, "FETCH"):
			set := ""
			if i := strings.Index(cmd, "FETCH "); i >= 0 {
				set = strings.TrimSpace(cmd[i+len("FETCH "):])
				if j := strings.Index(set, " "); j >= 0 {
					set = set[:j]
				}
			}
			// Unread 链路只请求 (UID)：只回请求过的项（多余项 go-imap 解析报错）
			full := strings.Contains(cmd, "ENVELOPE")
			var b strings.Builder
			for _, seq := range expandSeqSet(set) {
				if seq < 1 || seq > len(msgs) {
					continue
				}
				mm := msgs[seq-1]
				if !full {
					fmt.Fprintf(&b, "* %d FETCH (UID %d)\r\n", seq, mm.uid)
					continue
				}
				flags := ""
				if mm.seen {
					flags = "\\Seen"
				}
				fmt.Fprintf(&b,
					"* %d FETCH (UID %d FLAGS (%s) RFC822.SIZE 1024 ENVELOPE (NIL \"%s\" ((NIL NIL \"sender\" \"%s\")) NIL NIL NIL NIL NIL NIL NIL))\r\n",
					seq, mm.uid, flags, mm.subject, mm.from)
			}
			_, _ = conn.Write([]byte(b.String() + tag + " OK\r\n"))
		case strings.Contains(cmd, "LOGOUT"):
			_, _ = conn.Write([]byte("* BYE\r\n" + tag + " OK\r\n"))
			return
		default:
			_, _ = conn.Write([]byte(tag + " OK\r\n"))
		}
	}
}

// expandSeqSet 展开 "1:3" / "1,3" 混合序号集。
func expandSeqSet(set string) []int {
	var out []int
	for _, part := range strings.Split(set, ",") {
		a, b, isRange := strings.Cut(part, ":")
		lo, err := strconv.Atoi(a)
		if err != nil {
			continue
		}
		if !isRange {
			out = append(out, lo)
			continue
		}
		hi, err := strconv.Atoi(b)
		if err != nil {
			continue
		}
		for i := lo; i <= hi; i++ {
			out = append(out, i)
		}
	}
	return out
}

// newMailTestEnv 真 store + 假 IMAP 全链路环境，返回 Manager/账户/消息计数与最新一条。
func newMailTestEnv(t *testing.T, msgs ...fakeMsg) (*Manager, *store.MailAccount, *fakeMailIMAP, func() int, func() string) {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	fake := &fakeMailIMAP{ln: ln, msgs: msgs}
	go fake.serve()
	port, _ := strconv.Atoi(strings.Split(ln.Addr().String(), ":")[1])

	id, err := s.CreateMailAccount(&store.MailAccount{
		Name: "主邮箱", Email: "me@qq.com", Provider: "custom",
		IMAPHost: "127.0.0.1", IMAPPort: port, SMTPHost: "127.0.0.1", SMTPPort: port,
		Password: "authcode", PollSec: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.GetMailAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	count := func() int {
		p, err := s.ListMessages("mail", false, 1, 50)
		if err != nil {
			t.Fatal(err)
		}
		return p.Total
	}
	last := func() string {
		p, _ := s.ListMessages("mail", false, 1, 1)
		if len(p.Items) == 0 {
			return ""
		}
		return p.Items[0].Title + "|" + p.Items[0].Content
	}
	return New(s), a, fake, count, last
}

// TestAdjustUnread 未读缓存增量修正（372）语义：正负增量、负向下限 0、
// 零增量不动；无记录（尚未轮询）时保持缺省不插入合成值（防假到达提醒）。
func TestAdjustUnread(t *testing.T) {
	m := New(nil)
	m.setUnread(1, 3)

	m.AdjustUnread(1, -1)
	if got := m.Unread(1); got != 2 {
		t.Fatalf("-1 应得 2: %d", got)
	}
	m.AdjustUnread(1, -10) // 下限 0
	if got := m.Unread(1); got != 0 {
		t.Fatalf("负向应钳在 0: %d", got)
	}
	m.AdjustUnread(1, 2)
	if got := m.Unread(1); got != 2 {
		t.Fatalf("+2 应得 2: %d", got)
	}
	m.AdjustUnread(1, 0)
	if got := m.Unread(1); got != 2 {
		t.Fatalf("0 增量不应变动: %d", got)
	}

	m.AdjustUnread(99, 5) // 尚未轮询过的账户：不插入
	if got := m.Unread(99); got != -2 {
		t.Fatalf("无记录账户应保持 -2 缺省: %d", got)
	}
}

// TestManagerNewMailNotification 542 到达检测全链路（UID 差分口径）：首刷只立
// 基线；新增即报（含已被其他客户端先读掉的——旧「未读数上升」口径在此恒漏报，
// 10 分钟轮询下正是用户报「不推了」的主因）；持平/窗口平移（纯删除）不报；
// 一轮多封合并一条；失败恢复首帧只立基线不轰炸。
func TestManagerNewMailNotification(t *testing.T) {
	m, a, fake, count, last := newMailTestEnv(t,
		fm(1, "welcome"), fm(2, "invoice"), fm(3, "news"))

	// 首刷：只立基线不提醒
	m.refreshOne(*a)
	if got := count(); got != 0 {
		t.Fatalf("首刷不应提醒，实际 %d 条", got)
	}

	// 新增 uid4：提醒一条，文案含账户与主题
	fake.setMsgs(fm(1, "welcome"), fm(2, "invoice"), fm(3, "news"), fm(4, "hello"))
	m.refreshOne(*a)
	if got := count(); got != 1 {
		t.Fatalf("新增应提醒 1 条，实际 %d 条", got)
	}
	if msg := last(); !strings.Contains(msg, "主邮箱") || !strings.Contains(msg, "hello") || !strings.Contains(msg, "sender4@b.c") {
		t.Fatalf("提醒文案不对: %q", msg)
	}

	// 新增 uid5 且已被其他客户端先读：不提醒（542 返工口径：已读不推）
	fake.setMsgs(fm(1, "welcome"), fm(2, "invoice"), fm(3, "news"), fm(4, "hello"), mSeen(5, "read already"))
	m.refreshOne(*a)
	if got := count(); got != 1 {
		t.Fatalf("已读新邮件不应提醒，实际 %d 条", got)
	}

	// 持平：不报
	m.refreshOne(*a)
	if got := count(); got != 1 {
		t.Fatalf("持平不应提醒，实际 %d 条", got)
	}

	// 窗口平移（最老一封被删，无新增）：不误报
	fake.setMsgs(fm(2, "invoice"), fm(3, "news"), fm(4, "hello"), mSeen(5, "read already"))
	m.refreshOne(*a)
	if got := count(); got != 1 {
		t.Fatalf("窗口平移不应提醒，实际 %d 条", got)
	}

	// 混合一轮（uid6 未读 + uid7 已读）：只推未读那封，文案不含已读主题
	fake.setMsgs(fm(2, "invoice"), fm(3, "news"), fm(4, "hello"), mSeen(5, "read already"),
		fm(6, "alpha"), mSeen(7, "secret read"))
	m.refreshOne(*a)
	if got := count(); got != 2 {
		t.Fatalf("混合轮应只推未读 1 条，实际新增 %d 条", got-1)
	}
	msg := last()
	if !strings.Contains(msg, "alpha") || strings.Contains(msg, "secret read") {
		t.Fatalf("混合轮文案应只含未读: %q", msg)
	}

	// 一轮两封未读：合并一条，逐行列明细
	fake.setMsgs(fm(2, "invoice"), fm(3, "news"), fm(4, "hello"), mSeen(5, "read already"),
		fm(6, "alpha"), mSeen(7, "secret read"), fm(8, "beta"), fm(9, "gamma"))
	m.refreshOne(*a)
	if got := count(); got != 3 {
		t.Fatalf("多封应合并一条，实际新增 %d 条", got-2)
	}
	msg = last()
	if !strings.Contains(msg, "新邮件提醒：主邮箱（2 封）") || !strings.Contains(msg, "beta") || !strings.Contains(msg, "gamma") {
		t.Fatalf("多封合并文案不对: %q", msg)
	}

	// 失败恢复：清基线后首帧只立基线不轰炸（帧里有新信也不报）；之后真新增正常报
	m.pollFailed(a.ID)
	fake.setMsgs(fm(2, "invoice"), fm(3, "news"), fm(4, "hello"), mSeen(5, "read already"),
		fm(6, "alpha"), mSeen(7, "secret read"), fm(8, "beta"), fm(9, "gamma"), fm(10, "after fail"))
	m.refreshOne(*a)
	if got := count(); got != 3 {
		t.Fatalf("失败恢复首帧不应提醒，实际 %d 条", got)
	}
	fake.setMsgs(fm(2, "invoice"), fm(3, "news"), fm(4, "hello"), mSeen(5, "read already"),
		fm(6, "alpha"), mSeen(7, "secret read"), fm(8, "beta"), fm(9, "gamma"), fm(10, "after fail"), fm(11, "next one"))
	m.refreshOne(*a)
	if got := count(); got != 4 {
		t.Fatalf("恢复后新增应提醒，实际 %d 条", got)
	}
	if msg := last(); !strings.Contains(msg, "next one") {
		t.Fatalf("恢复后提醒文案不对: %q", msg)
	}
}

// TestManagerMailWebhookPush 542 三渠道外推：webhook 开关开启时到达提醒推送到
// 配置 URL；开关关闭不推；站内消息恒写。
func TestManagerMailWebhookPush(t *testing.T) {
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	hits := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		select {
		case hits <- string(b):
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	if err := webhook.SaveConfig(s, webhook.Config{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	m := New(s)
	news := []Header{{UID: 9, Subject: "push mail", FromAddr: "s@b.c"}}
	a := store.MailAccount{Name: "推邮箱", Email: "push@qq.com"}

	// 开关缺省关：不推
	m.notifyNewMail(a, news, 3)
	select {
	case b := <-hits:
		t.Fatalf("开关关闭不应推送: %q", b)
	case <-time.After(100 * time.Millisecond):
	}

	// 开关开启：推送 + 站内
	if err := webhook.SetMailOn(s, true); err != nil {
		t.Fatal(err)
	}
	m.notifyNewMail(a, news, 3)
	select {
	case b := <-hits:
		if !strings.Contains(b, "[PicoPanel]") || !strings.Contains(b, "push mail") {
			t.Fatalf("推送内容不对: %q", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("开关开启应收到 webhook 推送")
	}
	p, err := s.ListMessages("mail", false, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || !strings.Contains(p.Items[0].Title, "推邮箱") {
		t.Fatalf("站内消息应恒写: %+v", p.Items)
	}

	// 再关：不推
	if err := webhook.SetMailOn(s, false); err != nil {
		t.Fatal(err)
	}
	m.notifyNewMail(a, news, 3)
	select {
	case b := <-hits:
		t.Fatalf("开关再关不应推送: %q", b)
	case <-time.After(100 * time.Millisecond):
	}
}
