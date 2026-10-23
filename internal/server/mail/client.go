// Package mail 提供 Foxmail 式邮件收发（316-318）：IMAP 收件（文件夹/信头列表/
// 按需取正文/已读标记/删除/搜索）+ SMTP 发件（含附件、APPEND 同步 Sent）。
// 账户与凭据存 mail_accounts（密码 AES-GCM 加密）；IMAP 用 go-imap/v2，
// MIME 解析用 go-message（正文 charset 经 x/text 兼容 gbk/big5）。
// 设计口径：列表只取信头+BODYSTRUCTURE（不出正文）；正文/附件整封拉回本地解析
// （≤10MB 防护），附件按 IMAP part 路径从同一封报文内抽取。
package mail

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"lz-monitor/internal/server/store"
)

const (
	dialTimeout   = 15 * time.Second
	maxMessageLen = 10 << 20 // 整封拉取上限 10MB

	// maxDebugLine 会话日志单行长度上限：APPEND 字面量（发信原文最大 25MB）
	// 也流经 DebugWriter，不设限会整段驻留内存、出错时倾倒进日志
	//（2026-10-04 安全审查）。超限行丢弃到下一个换行。
	maxDebugLine = 8 << 10
)

// Folder 文件夹（含常用特殊夹标记，前端置图标/发件默认 Sent 用）。
type Folder struct {
	Name   string `json:"name"`
	Sent   bool   `json:"sent"`
	Trash  bool   `json:"trash"`
	Drafts bool   `json:"drafts"`
	Junk   bool   `json:"junk"`
}

// Header 列表行（信头级）。
type Header struct {
	UID      int64  `json:"uid"`
	FromName string `json:"from_name"`
	FromAddr string `json:"from_addr"`
	ToName   string `json:"to_name"`
	Subject  string `json:"subject"`
	DateUnix int64  `json:"date_unix"`
	Seen     bool   `json:"seen"`
	HasAtt   bool   `json:"has_att"`
	Size     int64  `json:"size"`
}

// Attachment 附件描述（阅读页附件条 + 下载索引）。
type Attachment struct {
	Idx  int    `json:"idx"`  // 本封内附件序号（1 起，下载端点按此抽取）
	Path string `json:"path"` // IMAP part 路径（如 "2.1"）
	Name string `json:"name"`
	CT   string `json:"ct"`
	Size int64  `json:"size"`
}

// Message 阅读页正文。
type Message struct {
	UID         int64        `json:"uid"`
	Subject     string       `json:"subject"`
	FromName    string       `json:"from_name"`
	FromAddr    string       `json:"from_addr"`
	To          string       `json:"to"`
	Cc          string       `json:"cc"` // 抄送（全部回复取址用，329）
	MessageID   string       `json:"message_id"`
	DateUnix    int64        `json:"date_unix"`
	Seen        bool         `json:"seen"`
	Text        string       `json:"text"` // 纯文本版（html 缺失时由 html 剥标签兜底）
	HTML        string       `json:"html"` // HTML 版（iframe sandbox 渲染）
	Attachments []Attachment `json:"attachments"`
}

// Client 一个已登录的 IMAP 会话（用完 Close）。
type Client struct {
	c *imapclient.Client
	a store.MailAccount
}

// mailDebug IMAP 会话日志（脱敏：密码只记长度）：平时静默，仅缓存最近 60 行，
// 遇到服务器 NO/BAD（登录被拒/命令被拒）才整段倾倒到日志——正常流量零日志，
// 出错时一次给全现场（2026-10-03 用户要求精简：只输出错误信息）。
type mailDebug struct {
	acct string // 账户邮箱（同账户错误现场 10 分钟只倾倒一次，持续失败不刷屏）
	mu   sync.Mutex
	buf  []byte
	ring []string
	drop bool // 超长行丢弃中（吞到下一个换行为止）
}

const mailDebugRing = 60

// lastErrDump 同账户上次倾倒时间（包级：每个 IMAP 会话一个 mailDebug 实例）。
var (
	errDumpMu    sync.Mutex
	lastErrDumps = map[string]time.Time{}
)

const errDumpInterval = 10 * time.Minute

func (d *mailDebug) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.buf = append(d.buf, p...)
	for {
		i := bytes.IndexByte(d.buf, '\n')
		if i < 0 {
			if len(d.buf) > maxDebugLine { // 超长行（二进制/正文流）：整行放弃
				d.buf = d.buf[:0]
				d.drop = true
			}
			break
		}
		if d.drop { // 丢弃中：吞掉这行换行后恢复
			d.buf = d.buf[i+1:]
			d.drop = false
			continue
		}
		line := redactLogin(string(d.buf[:i]))
		d.buf = d.buf[i+1:]
		if line == "" {
			continue
		}
		if isIMAPErrorLine(line) {
			errDumpMu.Lock()
			if since := time.Since(lastErrDumps[d.acct]); since >= errDumpInterval {
				lastErrDumps[d.acct] = time.Now()
				errDumpMu.Unlock()
				log.Printf("imap-debug 会话异常（%s，同账户 10 分钟内只记一次），最近 %d 行现场:\n%s\n%s",
					d.acct, len(d.ring), strings.Join(d.ring, "\n"), line)
			} else {
				errDumpMu.Unlock()
			}
			d.ring = d.ring[:0]
			continue
		}
		d.ring = append(d.ring, line)
		if len(d.ring) > mailDebugRing {
			d.ring = d.ring[len(d.ring)-mailDebugRing:]
		}
	}
	return len(p), nil
}

// isIMAPErrorLine tagged NO/BAD（登录/命令被拒）。正常 OK、登出告别 * BYE
// （* BYE ... logging out）不触发——曾把 BYE 误判为错误导致每轮轮询都倾倒（事故）。
func isIMAPErrorLine(line string) bool {
	for _, tag := range []string{" NO ", " BAD "} {
		if i := strings.Index(line, tag); i > 0 {
			return true
		}
	}
	return false
}

var loginCmdRe = regexp.MustCompile(`(?i)^(T\d+) LOGIN (.+)$`)

// redactLogin 把客户端 LOGIN 命令行里的密码替换为 <pass len=N>（服务器响应行
// 「T4 NO LOGIN …」不匹配命令模式，原样保留）。
func redactLogin(line string) string {
	line = strings.TrimRight(line, "\r")
	m := loginCmdRe.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	rest := m[2]
	var userEnd int
	if strings.HasPrefix(rest, `"`) {
		j := strings.Index(rest[1:], `"`)
		if j < 0 {
			return line
		}
		userEnd = j + 2
	} else {
		j := strings.IndexByte(rest, ' ')
		if j < 0 {
			return line
		}
		userEnd = j
	}
	user := rest[:userEnd]
	pass := strings.Trim(strings.TrimRight(rest[userEnd:], " "), ` "`)
	return m[1] + " LOGIN " + user + fmt.Sprintf(" <pass len=%d>", len(pass))
}

// DialIMAP 建立并登录 IMAP 会话。
func DialIMAP(a store.MailAccount) (*Client, error) {
	addr := net.JoinHostPort(a.IMAPHost, strconv.Itoa(a.IMAPPort))
	var conn net.Conn
	var err error
	d := net.Dialer{Timeout: dialTimeout}
	tlsCfg := &tls.Config{ServerName: a.IMAPHost}
	if a.IMAPPort == 993 {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("连接 IMAP %s 失败: %w", addr, err)
		}
		return login(a, imapclient.New(conn, &imapclient.Options{TLSConfig: tlsCfg, DebugWriter: &mailDebug{acct: a.Email}}))
	}
	// 非 993：先明文连，再 STARTTLS 升级；升级失败退明文（本地面板场景容忍）
	conn, err = d.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 IMAP %s 失败: %w", addr, err)
	}
	if c2, e2 := imapclient.NewStartTLS(conn, &imapclient.Options{TLSConfig: tlsCfg, DebugWriter: &mailDebug{acct: a.Email}}); e2 == nil {
		return login(a, c2)
	}
	// NewStartTLS 失败时会关闭原连接，明文回退必须重拨（复用旧连接必报
	// use of closed network connection——真实事故）
	warnPlaintext(a.Email, a.IMAPPort)
	conn2, err := d.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 IMAP %s 失败: %w", addr, err)
	}
	return login(a, imapclient.New(conn2, &imapclient.Options{DebugWriter: &mailDebug{acct: a.Email}}))
}

// plainWarnInterval 明文回退提醒限频（同账户 10 分钟一次，轮询场景不刷屏）。
const plainWarnInterval = 10 * time.Minute

var (
	plainWarnMu    sync.Mutex
	lastPlainWarns = map[string]time.Time{}
)

// warnPlaintext 非 993 端口 STARTTLS 不可用回退明文时告警：凭据与邮件内容
// 未加密传输，不再静默（2026-10-04 安全审查）。
func warnPlaintext(acct string, port int) {
	plainWarnMu.Lock()
	if since := time.Since(lastPlainWarns[acct]); since < plainWarnInterval {
		plainWarnMu.Unlock()
		return
	}
	lastPlainWarns[acct] = time.Now()
	plainWarnMu.Unlock()
	log.Printf("邮箱 %s 的 IMAP 端口 %d STARTTLS 不可用，已回退明文连接：凭据与邮件内容未加密传输，建议改用 993 端口", acct, port)
}

func login(a store.MailAccount, c *imapclient.Client) (*Client, error) {
	// 认证前自报客户端 ID（RFC 2971）：163/126（Coremail）风控对未发 ID 的客户端
	// 在 LOGIN 阶段直接拒绝——报「Login error or password error」误导为密码错误
	// （实测：本机/服务器先发 ID 后 LOGIN 均 OK，go-imap 默认不发自报被拒）；
	// Foxmail 等客户端即先发 ID。先 CAPABILITY 探能力，有 ID 就发，尽力而为。
	if _, err := c.Capability().Wait(); err == nil && c.Caps().Has("ID") {
		_, _ = c.ID(&imap.IDData{Name: "PicoPanel", Version: "1.0"}).Wait()
	}
	if err := c.Login(a.Email, a.Password).Wait(); err != nil {
		_ = c.Logout()
		return nil, fmt.Errorf("IMAP 登录失败（%s）: %w。请确认：①填的是授权码而非登录密码；②已在邮箱设置开启 IMAP/SMTP 服务（QQ：设置-账户-开启服务并生成授权码；163：设置-POP3/SMTP/IMAP-新增授权码）", a.Email, err)
	}
	return &Client{c: c, a: a}, nil
}

// Close 登出（尽力而为）。
func (cl *Client) Close() { _ = cl.c.Logout() }

// Folders 文件夹清单：INBOX 最前，特殊夹次之，其余按名称。
func (cl *Client) Folders() ([]Folder, error) {
	cmd := cl.c.List("", "*", &imap.ListOptions{})
	data, err := cmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("获取文件夹失败: %w", err)
	}
	out := make([]Folder, 0, len(data))
	for _, d := range data {
		f := Folder{Name: d.Mailbox}
		for _, at := range d.Attrs {
			switch at {
			case imap.MailboxAttrSent:
				f.Sent = true
			case imap.MailboxAttrTrash:
				f.Trash = true
			case imap.MailboxAttrDrafts:
				f.Drafts = true
			case imap.MailboxAttrJunk:
				f.Junk = true
			}
		}
		out = append(out, f)
	}
	rank := func(f Folder) int {
		if f.Name == "INBOX" {
			return 0
		}
		if f.Sent || f.Trash || f.Drafts || f.Junk {
			return 1
		}
		return 2
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// findSpecial 特殊夹名称（\Sent/\Trash 属性优先，常见英文名兜底）。
func (cl *Client) findSpecial(sent bool) (string, bool) {
	folders, err := cl.Folders()
	if err != nil {
		return "", false
	}
	for _, f := range folders {
		if sent && f.Sent {
			return f.Name, true
		}
		if !sent && f.Trash {
			return f.Name, true
		}
	}
	names := []string{"Sent", "Sent Messages", "已发送"}
	if !sent {
		names = []string{"Trash", "Deleted Messages", "Deleted", "已删除"}
	}
	for _, want := range names {
		for _, f := range folders {
			if strings.EqualFold(f.Name, want) {
				return f.Name, true
			}
		}
	}
	return "", false
}

// List 信头列表：limit 条最新（uid 降序）；term 非空走 IMAP SEARCH（主题/发件人）；
// beforeUID>0 时向前翻页（取 UID 小于它的更早 limit 条，「加载更多」用）。
func (cl *Client) List(folder, term string, limit int, beforeUID int64) ([]Header, error) {
	sel, err := cl.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("打开文件夹 %s 失败: %w", folder, err)
	}
	var numSet imap.NumSet
	if beforeUID > 0 {
		// 翻页：UID SEARCH UID 1:<before-1>（可叠加搜索词），取尾部 limit 个 UID 的连续区间
		crit := &imap.SearchCriteria{UID: []imap.UIDSet{{{Start: 1, Stop: imap.UID(beforeUID - 1)}}}}
		if term = strings.TrimSpace(term); term != "" {
			crit.Or = [][2]imap.SearchCriteria{{
				{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: term}}},
				{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: term}}},
			}}
		}
		data, err := cl.c.UIDSearch(crit, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("翻页查询失败: %w", err)
		}
		ranges := imap.UIDSet(data.All.(imap.UIDSet)) // 值类型：UID 区间切片（升序）
		if len(ranges) == 0 {
			return []Header{}, nil
		}
		// 取尾部 limit 个 UID：拍平后截尾，再包回首尾区间
		var uids []imap.UID
		for _, r := range ranges {
			for u := r.Start; u <= r.Stop; u++ { // 区间由 SEARCH 条件限定 < beforeUID
				uids = append(uids, u)
			}
		}
		if len(uids) == 0 {
			return []Header{}, nil
		}
		if len(uids) > limit {
			uids = uids[len(uids)-limit:]
		}
		set := imap.UIDSet{}
		set.AddRange(uids[0], uids[len(uids)-1])
		numSet = set
	} else if term = strings.TrimSpace(term); term != "" {
		criteria := &imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{
			{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: term}}},
			{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: term}}},
		}}}
		data, err := cl.c.Search(criteria, nil).Wait()
		if err != nil {
			return nil, fmt.Errorf("搜索失败: %w", err)
		}
		numSet = data.All
		if numSet == nil || searchEmpty(data.All) {
			return []Header{}, nil
		}
	} else {
		n := sel.NumMessages
		if n == 0 {
			return []Header{}, nil
		}
		from := uint32(1)
		if int(n) > limit {
			from = n - uint32(limit) + 1
		}
		// 真区间（SeqSetNum(a,b) 是两个孤立序号=只取到 2 封的事故）。
		// 注意 NumSet 的具体类型必须是【值】imap.SeqSet——go-imap beta.8 的
		// num.go 只 case 值类型，传指针会 panic "invalid NumSet type"
		set := imap.SeqSet{}
		set.AddRange(from, n)
		numSet = set
	}
	opts := &imap.FetchOptions{Envelope: true, Flags: true, UID: true, RFC822Size: true,
		BodyStructure: &imap.FetchItemBodyStructure{Extended: true}}
	msgs, err := cl.c.Fetch(numSet, opts).Collect()
	if err != nil {
		return nil, fmt.Errorf("拉取信头失败: %w", err)
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].UID > msgs[j].UID })
	if len(msgs) > limit {
		msgs = msgs[:limit]
	}
	out := make([]Header, 0, len(msgs))
	for _, m := range msgs {
		h := Header{UID: int64(m.UID), Size: m.RFC822Size}
		if m.Envelope != nil {
			h.Subject = m.Envelope.Subject
			h.DateUnix = m.Envelope.Date.Unix()
			h.FromName, h.FromAddr = addrOf(m.Envelope.From)
			h.ToName, _ = addrOf(m.Envelope.To)
		}
		for _, f := range m.Flags {
			if f == imap.FlagSeen {
				h.Seen = true
			}
		}
		h.HasAtt = bodyHasAttachment(m.BodyStructure)
		out = append(out, h)
	}
	return out, nil
}

// FetchRaw 整封原始报文（BODY.PEEK[]，≤maxMessageLen）。
func (cl *Client) FetchRaw(folder string, uid int64) ([]byte, error) {
	if _, err := cl.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("打开文件夹失败: %w", err)
	}
	sec := &imap.FetchItemBodySection{Peek: true} // 零值=整个 BODY，PEEK 不动已读态
	msgs, err := cl.c.Fetch(imap.UIDSetNum(imap.UID(uid)),
		&imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{sec}}).Collect()
	if err != nil {
		return nil, fmt.Errorf("拉取邮件失败: %w", err)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("邮件不存在（可能已被删除）")
	}
	buf := msgs[0].FindBodySection(sec)
	if buf == nil {
		return nil, fmt.Errorf("邮件内容为空")
	}
	if len(buf) > maxMessageLen {
		return nil, fmt.Errorf("邮件过大（>%dMB），暂不支持在线阅读", maxMessageLen>>20)
	}
	return buf, nil
}

// ReadMessage 拉整封并解析出 正文(text/html)+附件清单（318）。
func (cl *Client) ReadMessage(folder string, uid int64) (*Message, error) {
	raw, err := cl.FetchRaw(folder, uid)
	if err != nil {
		return nil, err
	}
	m := ParseMIME(raw)
	m.UID = uid
	// 已读态补查（轻量：单封 flags）
	opts := &imap.FetchOptions{UID: true, Flags: true}
	if msgs, err := cl.c.Fetch(imap.UIDSetNum(imap.UID(uid)), opts).Collect(); err == nil && len(msgs) == 1 {
		for _, f := range msgs[0].Flags {
			if f == imap.FlagSeen {
				m.Seen = true
			}
		}
	}
	return m, nil
}

// FetchAttachment 按 IMAP part 路径抽取附件字节（重新拉整封后定位，读多附件少）。
func (cl *Client) FetchAttachment(folder string, uid int64, partPath string) (name, ct string, data []byte, err error) {
	raw, err := cl.FetchRaw(folder, uid)
	if err != nil {
		return "", "", nil, err
	}
	return ExtractPart(raw, partPath)
}

// MarkSeen 设置/取消 \Seen。
func (cl *Client) MarkSeen(folder string, uid int64, seen bool) error {
	if _, err := cl.c.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("打开文件夹失败: %w", err)
	}
	st := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}, Silent: false}
	if !seen {
		st = &imap.StoreFlags{Op: imap.StoreFlagsDel, Flags: []imap.Flag{imap.FlagSeen}, Silent: false}
	}
	if _, err := cl.c.Store(imap.UIDSetNum(imap.UID(uid)), st, nil).Collect(); err != nil {
		return fmt.Errorf("更新已读状态失败: %w", err)
	}
	return nil
}

// DeleteMessages 删除：有垃圾桶移入垃圾桶（MOVE，不支持则复制+标记删除），
// 本就在垃圾桶则直接标记删除并清掉。
func (cl *Client) DeleteMessages(folder string, uids []int64) error {
	if len(uids) == 0 {
		return nil
	}
	set := imap.UIDSetNum(toUIDs(uids)...)
	if folder == "" {
		return fmt.Errorf("文件夹为空")
	}
	// MOVE/COPY/STORE 都要求先选中邮箱：不 SELECT 直接发，严格的服务器
	// 回 NO Need to SELECT first!（真实事故），宽容的也属未定义行为
	if _, err := cl.c.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("打开文件夹失败: %w", err)
	}
	if trash, ok := cl.findSpecial(false); ok && trash != folder {
		if _, err := cl.c.Move(set, trash).Wait(); err == nil {
			return nil
		}
		// MOVE 不支持：复制到垃圾桶 + 原地标记删除
		if _, err := cl.c.Copy(set, trash).Wait(); err != nil {
			return fmt.Errorf("移入垃圾桶失败: %w", err)
		}
	}
	st := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true}
	if _, err := cl.c.Store(set, st, nil).Collect(); err != nil {
		return fmt.Errorf("标记删除失败: %w", err)
	}
	exp := cl.c.Expunge()
	_, err := exp.Collect()
	return err
}

// Unread 文件夹未读数（SEARCH UNSEEN 计数）。
func (cl *Client) Unread(folder string) (int, error) {
	if _, err := cl.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return 0, err
	}
	data, err := cl.c.Search(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		return 0, err
	}
	if data.All == nil || searchEmpty(data.All) { // 空集喂 Fetch 会发出畸形命令（服务器 BAD→解析器报 tag 错）
		return 0, nil
	}
	msgs, err := cl.c.Fetch(data.All, &imap.FetchOptions{UID: true}).Collect()
	if err != nil {
		return 0, err
	}
	return len(msgs), nil
}

// AppendSent 把已发送原文塞回 Sent（找不到 Sent 夹静默跳过）。
func (cl *Client) AppendSent(raw []byte) error {
	sent, ok := cl.findSpecial(true)
	if !ok {
		return nil
	}
	cmd := cl.c.Append(sent, int64(len(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}})
	if _, err := cmd.Write(raw); err != nil {
		_ = cmd.Close()
		return err
	}
	return cmd.Close()
}

// TestAccount 连通性测试：IMAP 登录 + 列文件夹 + SMTP 认证（send.go）。
func TestAccount(a store.MailAccount, smtpPassword string) error {
	cl, err := DialIMAP(a)
	if err != nil {
		return err
	}
	cl.Close()
	return TestSMTP(a, smtpPassword)
}

// ---- 小工具 ----

// searchEmpty 判 SEARCH 结果集是否为空。SearchData.All 是【值】类型 SeqSet/UIDSet
// （beta.8 初始化为 SeqSet(nil)，未命中即零长切片）。
func searchEmpty(n imap.NumSet) bool {
	switch v := n.(type) {
	case imap.SeqSet:
		return len(v) == 0
	case imap.UIDSet:
		return len(v) == 0
	}
	return true
}

func toUIDs(ids []int64) []imap.UID {
	out := make([]imap.UID, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			out = append(out, imap.UID(id))
		}
	}
	return out
}

func addrOf(list []imap.Address) (name, addr string) {
	if len(list) == 0 {
		return "", ""
	}
	a := list[0]
	return a.Name, fmt.Sprintf("%s@%s", a.Mailbox, a.Host)
}

// bodyHasAttachment BODYSTRUCTURE 树里找带 filename 的叶子（扩展字段缺失时尽力）。
func bodyHasAttachment(bs imap.BodyStructure) bool {
	if bs == nil {
		return false
	}
	has := false
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		if d := part.Disposition(); d != nil && strings.EqualFold(d.Value, "attachment") {
			has = true
			return false
		}
		if !has {
			if mt := part.MediaType(); strings.HasPrefix(mt, "application/") {
				if len(path) > 0 { // 顶层 application/*（如整封就是个 pdf）也算
					has = true
					return false
				}
			}
		}
		return true
	})
	return has
}
