package api

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/dbtool"
	"lz-monitor/internal/server/mail"
	"lz-monitor/internal/server/store"
)

// fakeIMAP 最小 IMAP 服务器（只覆盖附件下载/读信/删除/未读链路用到的命令：LOGIN/
// SELECT/LIST/SEARCH/UID FETCH/UID MOVE/UID STORE/EXPUNGE/LOGOUT），canned 一封
// multipart/mixed 邮件：part 1=正文，part 2=附件。
// delay 模拟慢 IMAP（验证附件接口解除全局写超时，328 回归）。
// cmds 记录全部命令行（删除链路 SELECT 先于 MOVE 的顺序断言，328 附带发现）。
// unseen 配 SEARCH UNSEEN 的条数（372 未读缓存修正链路：FLAGS FETCH 按序号集展开）。
type fakeIMAP struct {
	ln      net.Listener
	raw     []byte
	delay   time.Duration
	verbose bool
	unseen  int
	mu      sync.Mutex
	cmds    []string
}

func (f *fakeIMAP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeIMAP) handle(conn net.Conn) {
	defer conn.Close()
	if f.verbose {
		fmt.Println("FAKE: conn open")
	}
	_, _ = conn.Write([]byte("* OK fake ready\r\n"))
	br := bufio.NewReader(conn)
	selected := false // 本连接选中态（IMAP 协议口径：SELECT 是连接级状态）
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if f.verbose {
				fmt.Println("FAKE: conn read end:", err)
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if f.verbose {
			fmt.Println("FAKE:", line)
		}
		f.mu.Lock()
		f.cmds = append(f.cmds, line)
		f.mu.Unlock()
		upper := strings.ToUpper(line)
		if strings.Contains(upper, " SELECT ") || strings.Contains(upper, "EXAMINE ") {
			selected = true
		}
		tag, _, _ := strings.Cut(line, " ")
		cmd := upper
		switch {
		case strings.Contains(cmd, "CAPABILITY"):
			_, _ = conn.Write([]byte("* CAPABILITY IMAP4rev1\r\n" + tag + " OK\r\n"))
		case strings.Contains(cmd, "STARTTLS"):
			// 拒绝升级：DialIMAP 对非 993 端口先试 STARTTLS，回 NO 走明文回退
			_, _ = conn.Write([]byte(tag + " NO\r\n"))
		case strings.Contains(cmd, "LOGIN"):
			_, _ = conn.Write([]byte(tag + " OK\r\n"))
		case strings.Contains(cmd, " LIST "):
			_, _ = conn.Write([]byte("* LIST (\\HasNoChildren \\Trash) \"/\" \"Deleted Messages\"\r\n" + tag + " OK\r\n"))
		case (strings.Contains(cmd, "UID MOVE") || strings.Contains(cmd, "UID COPY")) && !selected:
			_, _ = conn.Write([]byte(tag + " NO Need to SELECT first!\r\n"))
		case strings.Contains(cmd, "UID STORE") && !selected:
			_, _ = conn.Write([]byte(tag + " NO Need to SELECT first!\r\n"))
		case strings.Contains(cmd, "SELECT") || strings.Contains(cmd, "EXAMINE"):
			_, _ = conn.Write([]byte("* FLAGS (\\Answered \\Flagged \\Deleted \\Seen \\Draft)\r\n" +
				"* 1 EXISTS\r\n* 0 RECENT\r\n* OK [UIDVALIDITY 1] UIDs valid\r\n" +
				"* OK [UIDNEXT 2] Predicted next UID\r\n" + tag + " OK [READ-ONLY]\r\n"))
		case strings.Contains(cmd, "SEARCH"):
			// Unread() 链路（372）：回 unseen 个序号，FETCH FLAGS 分支按序号展开
			var b strings.Builder
			if f.unseen > 0 {
				seqs := make([]string, f.unseen)
				for i := range seqs {
					seqs[i] = strconv.Itoa(i + 1)
				}
				b.WriteString("* SEARCH " + strings.Join(seqs, " ") + "\r\n")
			} else {
				b.WriteString("* SEARCH\r\n")
			}
			b.WriteString(tag + " OK\r\n")
			_, _ = conn.Write([]byte(b.String()))
		case strings.Contains(cmd, "UID FETCH") && strings.Contains(cmd, "BODY.PEEK[]"):
			if f.delay > 0 {
				time.Sleep(f.delay)
			}
			_, _ = conn.Write([]byte("* 1 FETCH (UID 1 BODY[] {" + strconv.Itoa(len(f.raw)) + "}\r\n"))
			_, _ = conn.Write(f.raw)
			_, _ = conn.Write([]byte(")\r\n" + tag + " OK\r\n"))
		case strings.Contains(cmd, "FETCH"):
			// 信头列表与 Unread() 计数共用（BODY.PEEK 已在上方分支拦截）：对序号集
			// 里每个序号回一行 FETCH——单序号时与旧 canned 行为一致，多序号行数即
			// Unread() 的计数结果
			n := 0
			for _, field := range strings.Fields(line) {
				field = strings.Trim(field, "()")
				if field == "" || !strings.ContainsAny(field, "0123456789") {
					continue
				}
				for _, p := range strings.Split(field, ",") {
					if a, b, isRange := strings.Cut(p, ":"); isRange {
						lo, err1 := strconv.Atoi(a)
						hi, err2 := strconv.Atoi(b)
						if err1 == nil && err2 == nil && hi >= lo {
							n += hi - lo + 1
						}
						continue
					}
					if _, err := strconv.Atoi(p); err == nil {
						n++
					}
				}
			}
			var b strings.Builder
			for i := 1; i <= n; i++ {
				fmt.Fprintf(&b, "* %d FETCH (UID %d FLAGS (\\Seen))\r\n", i, i)
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

// testRawMail canned 邮件（multipart/mixed：text/plain 正文 + pdf 附件）。
func testRawMail() []byte {
	sb := base64.StdEncoding.EncodeToString([]byte("附件测试"))
	return []byte("From: Sender <sender@qq.com>\r\n" +
		"To: me@qq.com, other@qq.com\r\n" +
		"Cc: cc1@qq.com, me@qq.com\r\n" +
		"Subject: =?utf-8?B?" + sb + "?=\r\n" +
		"Date: Fri, 02 Oct 2026 12:00:00 +0800\r\n" +
		"Message-ID: <att@qq.com>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B1\"\r\n" +
		"\r\n" +
		"--B1\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"body text\r\n" +
		"--B1\r\n" +
		"Content-Type: application/pdf; name=\"report.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"report.pdf\"\r\n" +
		"\r\n" +
		"PDF-FAKE-CONTENT-0123456789\r\n" +
		"--B1--\r\n")
}

// newMailRouter 真实链路：真实 Register（含鉴权组）+ 假 IMAP + 入库账户指向它。
// 返回引擎、session token、账户 id、store（缓存直出/刷新测试要种缓存行）。
func newMailRouter(t *testing.T, delay time.Duration) (*gin.Engine, string, int64, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal("查管理员失败")
	}
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeIMAP{ln: ln, raw: testRawMail(), delay: delay, verbose: os.Getenv("FAKE_VERBOSE") != ""}
	go fake.serve()
	t.Cleanup(func() { ln.Close() })
	port, _ := strconv.Atoi(strings.Split(ln.Addr().String(), ":")[1])
	id, err := s.CreateMailAccount(&store.MailAccount{
		Name: "测试", Email: "me@qq.com", Provider: "custom",
		IMAPHost: "127.0.0.1", IMAPPort: port, SMTPHost: "127.0.0.1", SMTPPort: port, SMTPSSL: false,
		Password: "authcode", PollSec: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	mg := dbtool.New(s, filepath.Join(t.TempDir(), "dbbackups"))
	t.Cleanup(func() { mg.Close() })
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, mail.New(s), nil, mg, nil, nil, nil, nil); err != nil {
		t.Fatal(err, nil)
	}
	return r, token, id, s
}

// TestBuildAccountEmailControl 账户 email 校验回归（2026-10-04 安全审查）：
// 控制字符/空格曾可混入 email 并经发信 From: 头外发（信头注入）。
func TestBuildAccountEmailControl(t *testing.T) {
	for _, bad := range []string{"a@b.com\r\nBcc: x@y.z", "a b@x.com", "中@文.com"} { // 尾部 \n 会被 TrimSpace 剥掉不算
		if _, err := buildAccount(&mailAccountReq{Email: bad, Provider: "custom", IMAPHost: "h", SMTPHost: "h"}); err == nil {
			t.Fatalf("%q 应拒绝", bad)
		}
	}
	a, err := buildAccount(&mailAccountReq{Email: "user@qq.com", Provider: "custom", IMAPHost: "imap.qq.com", SMTPHost: "smtp.qq.com"})
	if err != nil || a.Email != "user@qq.com" || a.IMAPPort != 993 {
		t.Fatalf("正常邮箱不应拒绝: %+v %v", a, err)
	}
}

// TestMailAttachmentDownload 附件下载全链路（328）：假 IMAP → 整封拉回 → part 抽取 →
// 原样回传，Content-Disposition 带文件名。
func TestMailAttachmentDownload(t *testing.T) {
	r, token, id, _ := newMailRouter(t, 0)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/mail/accounts/%d/attachment?folder=INBOX&uid=1&part=2", id), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("状态 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "PDF-FAKE-CONTENT") {
		t.Fatalf("附件字节不对: %q", w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "report.pdf") {
		t.Fatalf("Content-Disposition 缺文件名: %q", cd)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("Content-Type 不对: %q", ct)
	}
}

// TestMailAttachmentSlowNoWriteTimeout 328 回归：全局 30s WriteTimeout 会把「先连
// IMAP 拉整封再回传」的慢附件请求掐断连接（浏览器 fetch 报 Failed to fetch）。
// 这里用紧超时（100ms）+ 延迟应答（400ms）的假 IMAP 证明接口已解除写超时：
// 处理时长超过 WriteTimeout 仍完整送达（未解除时连接会被服务端直接关闭）。
func TestMailAttachmentSlowNoWriteTimeout(t *testing.T) {
	r, token, id, _ := newMailRouter(t, 400*time.Millisecond)
	ts := httptest.NewUnstartedServer(r)
	ts.Config.WriteTimeout = 100 * time.Millisecond
	ts.Config.ReadTimeout = 100 * time.Millisecond
	ts.Config.ErrorLog = log.New(os.Stderr, "HTTPSRV: ", log.Lmicroseconds)
	ts.Start()
	defer ts.Close()

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v1/mail/accounts/%d/attachment?folder=INBOX&uid=1&part=2", ts.URL, id), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败（写超时未解除的表现）: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "PDF-FAKE-CONTENT") {
		t.Fatalf("响应不对: %d %q", resp.StatusCode, string(body))
	}
}

// TestMailReadCc 329：读信接口透出 Cc 头（全部回复取址用）。
func TestMailReadCc(t *testing.T) {
	r, token, id, _ := newMailRouter(t, 0)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/mail/accounts/%d/message?folder=INBOX&uid=1", id), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("状态 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"cc":"cc1@qq.com, me@qq.com"`) {
		t.Fatalf("响应缺 cc 字段: %s", w.Body.String())
	}
}

// TestMailDeleteSelectsBeforeMove 328 附带发现回归：删除邮件走 MOVE 前必须先
// SELECT（严格服务器回 NO Need to SELECT first!，真实事故）。假 IMAP 对未选中
// 连接的 UID MOVE 严格回 NO：修复前 Move 在 Select 之前发出会失败。
func TestMailDeleteSelectsBeforeMove(t *testing.T) {
	r, token, id, _ := newMailRouter(t, 0)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/mail/accounts/%d/delete-msgs", id), strings.NewReader(`{"folder":"INBOX","uids":[1]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":0`) {
		t.Fatalf("删除应成功: %d %s", w.Code, w.Body.String())
	}
}

// TestMailSeenAdjustsUnread 372：seen/删除接口即时修正 Manager 未读缓存——
// 真实 Register + 假 IMAP（SEARCH 报 2 封未读建立基线）全链路：标已读 -1、
// 标未读 +1、无本地缓存行不动、非收件箱不动、删除未读递减/已读不递减。
func TestMailSeenAdjustsUnread(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatal("查管理员失败")
	}
	token, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	fake := &fakeIMAP{ln: ln, raw: testRawMail(), unseen: 2}
	go fake.serve()
	port, _ := strconv.Atoi(strings.Split(ln.Addr().String(), ":")[1])
	id, err := s.CreateMailAccount(&store.MailAccount{
		Name: "测试", Email: "me@qq.com", Provider: "custom",
		IMAPHost: "127.0.0.1", IMAPPort: port, SMTPHost: "127.0.0.1", SMTPPort: port, SMTPSSL: false,
		Password: "authcode", PollSec: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr := mail.New(s)
	r := gin.New()
	mg := dbtool.New(s, filepath.Join(t.TempDir(), "dbbackups"))
	t.Cleanup(func() { mg.Close() })
	if err := Register(r, s, nil, &config.Config{}, "test", nil, nil, mgr, nil, mg, nil, nil, nil, nil); err != nil {
		t.Fatal(err, nil)
	}

	post := func(path, body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("POST %s 状态 %d（期望 %d）: %s", path, w.Code, want, w.Body.String())
		}
	}

	// 轮询一次建立未读缓存基线（fake 报 2 封未读）
	mgr.RefreshAll()
	if got := mgr.Unread(id); got != 2 {
		t.Fatalf("轮询基线应 2: %d", got)
	}
	if err := s.UpsertMailHeaders(id, "INBOX", []store.MailHeader{
		{UID: 5, Subject: "未读甲"}, {UID: 6, Subject: "已读乙", Seen: true},
	}); err != nil {
		t.Fatal(err)
	}

	seen := func(folder string, uid int64, to bool) {
		t.Helper()
		post(fmt.Sprintf("/api/v1/mail/accounts/%d/seen", id),
			fmt.Sprintf(`{"folder":%q,"uid":%d,"seen":%v}`, folder, uid, to), http.StatusOK)
	}

	// 标已读（旧态未读→翻转）：2-1=1
	seen("INBOX", 5, true)
	if got := mgr.Unread(id); got != 1 {
		t.Fatalf("标已读后应 1: %d", got)
	}
	// 无本地缓存行（uid99）：状态无从判定，不动
	seen("INBOX", 99, true)
	if got := mgr.Unread(id); got != 1 {
		t.Fatalf("无缓存行不应修正，仍应 1: %d", got)
	}
	// 非收件箱：不在 INBOX 口径内，不动
	seen("Archive", 5, true)
	if got := mgr.Unread(id); got != 1 {
		t.Fatalf("非收件箱不应修正，仍应 1: %d", got)
	}
	// 标未读（旧态已读→翻转）：1+1=2
	seen("INBOX", 6, false)
	if got := mgr.Unread(id); got != 2 {
		t.Fatalf("标未读后应 2: %d", got)
	}

	// 删除：已读行不递减（uid5 已被标已读），未读行递减（uid6 上一步已标未读）
	post(fmt.Sprintf("/api/v1/mail/accounts/%d/delete-msgs", id), `{"folder":"INBOX","uids":[5]}`, http.StatusOK)
	if got := mgr.Unread(id); got != 2 {
		t.Fatalf("删除已读行不应修正，仍应 2: %d", got)
	}
	post(fmt.Sprintf("/api/v1/mail/accounts/%d/delete-msgs", id), `{"folder":"INBOX","uids":[6]}`, http.StatusOK)
	if got := mgr.Unread(id); got != 1 {
		t.Fatalf("删除未读行应递减到 1: %d", got)
	}

	// 侧栏角标端点读同一份缓存
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mail/badge", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), fmt.Sprintf(`"%d":1`, id)) {
		t.Fatalf("badge 应为 1: %d %s", w.Code, w.Body.String())
	}
}

// TestMailMessagesCacheFirst 缓存直出链路（2026-10-04 用户反馈两项）：
// ① 列表默认路径读本地缓存，has_more 按整页口径——缓存窗口只含已同步的最新
//
//	一段，取满 limit 就得给 true，否则「查看更多」按钮在整窗缓存下永不出现；
//
// ② refresh=1 走 IMAP 并整夹替换缓存（服务器为准），响应带回服务器数据。
func TestMailMessagesCacheFirst(t *testing.T) {
	r, token, id, st := newMailRouter(t, 0)
	seed := make([]store.MailHeader, 0, 60)
	for i := 0; i < 60; i++ {
		seed = append(seed, store.MailHeader{UID: int64(100 - i), FromAddr: "a@b.com",
			Subject: fmt.Sprintf("第%d封", i), DateUnix: int64(3000 - i)})
	}
	if err := st.UpsertMailHeaders(id, "INBOX", seed); err != nil {
		t.Fatal(err)
	}
	get := func(q string) (int, string) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/mail/accounts/%d/messages?folder=INBOX&limit=50%s", id, q), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	// ① 默认路径：60 行缓存取 50，整页 → has_more=true（修复前 false，按钮消失）
	code, body := get("")
	if code != http.StatusOK || !strings.Contains(body, `"has_more":true`) {
		t.Fatalf("整页缓存应给 has_more=true: %d %s", code, body)
	}
	if !strings.Contains(body, `"subject":"第49封"`) || strings.Contains(body, `"subject":"第50封"`) {
		t.Fatalf("应恰好返回前 50 封: %s", body)
	}

	// ② refresh=1：连假 IMAP（回 canned 1 封 UID 1），整夹替换缓存后原样返回
	code, body = get("&refresh=1")
	if code != http.StatusOK {
		t.Fatalf("刷新应成功: %d %s", code, body)
	}
	if !strings.Contains(body, `"uid":1`) || strings.Contains(body, `"subject":"第`) {
		t.Fatalf("刷新应返回 IMAP 数据: %s", body)
	}

	// 刷新后默认路径读到替换后的缓存（1 封，非整页 has_more=false）
	code, body = get("")
	if code != http.StatusOK || !strings.Contains(body, `"has_more":false`) || !strings.Contains(body, `"uid":1`) {
		t.Fatalf("刷新后应读替换后的缓存: %d %s", code, body)
	}
}

// TestMailContactsSignatureAPI 通讯录与签名的 HTTP 链路（506/507）：空列表→增→
// 判重/非法邮箱 400→改→删（幂等）；签名默认空→PUT 存→GET 读回。
func TestMailContactsSignatureAPI(t *testing.T) {
	r, token, _, _ := newMailRouter(t, 0)
	do := func(method, path, body string) (int, string) {
		w := httptest.NewRecorder()
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	// 空列表
	code, body := do("GET", "/api/v1/mail/contacts", "")
	if code != http.StatusOK || !strings.Contains(body, `"contacts":[]`) {
		t.Fatalf("初始应为空列表: %d %s", code, body)
	}

	// 新增（store 已归一小写，接口回显原样小写）
	code, body = do("POST", "/api/v1/mail/contacts", `{"name":"张三","email":"zhang@x.com"}`)
	if code != http.StatusOK || !strings.Contains(body, `"email":"zhang@x.com"`) {
		t.Fatalf("新增应成功: %d %s", code, body)
	}
	// 重复（不同大小写）与非法邮箱都 400
	if code, _ = do("POST", "/api/v1/mail/contacts", `{"email":"ZHANG@X.com"}`); code != http.StatusBadRequest {
		t.Fatalf("重复邮箱应 400: %d", code)
	}
	if code, _ = do("POST", "/api/v1/mail/contacts", `{"email":"a b@x.com"}`); code != http.StatusBadRequest {
		t.Fatalf("非法邮箱应 400: %d", code)
	}

	if code, _ = do("PUT", "/api/v1/mail/contacts/1", `{"name":"张三丰","email":"zsf@x.com"}`); code != http.StatusOK {
		t.Fatalf("编辑应成功: %d", code)
	}
	if code, body = do("GET", "/api/v1/mail/contacts", ""); code != http.StatusOK || !strings.Contains(body, "zsf@x.com") {
		t.Fatalf("编辑应落库: %d %s", code, body)
	}
	if code, _ = do("DELETE", "/api/v1/mail/contacts/1", ""); code != http.StatusOK {
		t.Fatalf("删除应成功: %d", code)
	}
	if code, _ = do("DELETE", "/api/v1/mail/contacts/1", ""); code != http.StatusOK {
		t.Fatalf("重复删除应幂等 200: %d", code)
	}

	// 签名：默认空 → PUT 存 → GET 读回
	code, body = do("GET", "/api/v1/mail/signature", "")
	if code != http.StatusOK || !strings.Contains(body, `"signature":""`) {
		t.Fatalf("签名默认空: %d %s", code, body)
	}
	if code, _ = do("PUT", "/api/v1/mail/signature", `{"signature":"张三 · 13800000000"}`); code != http.StatusOK {
		t.Fatalf("存签名应成功: %d", code)
	}
	if code, body = do("GET", "/api/v1/mail/signature", ""); code != http.StatusOK || !strings.Contains(body, "13800000000") {
		t.Fatalf("签名应读回: %d %s", code, body)
	}
}
