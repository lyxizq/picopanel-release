package mail

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

const (
	// DefaultPollSec 默认轮询间隔（2026-10-03 用户指定：60s 太频繁，改 10 分钟，
	// 且按账户单独配置 poll_sec 60~86400）。
	DefaultPollSec = 600
	// schedTick 调度器节拍（检查各账户是否到期的最小粒度）。
	schedTick = 15 * time.Second
)

// Manager 按账户保活轮询 INBOX 未读数（侧栏角标 + 新邮件提醒），仿 integrations
// Manager：常驻调度 + 互斥缓存 + Drop（删账户即清）。轮询顺带把 INBOX 最新
// 信头增量合并进 mail_headers（列表本地库直出的数据源，2026-10-03）；
// 正文不缓存（按需直取）。
// 调度=每账户独立到期时间（poll_sec 秒），到期才连；账户级失败不影响其他账户。
type Manager struct {
	st       *store.Store
	mu       sync.RWMutex
	unread   map[int64]int              // account_id → INBOX 未读数（-1 = 上次查询失败）
	lastPoll map[int64]time.Time        // account_id → 上次轮询时刻
	arrived  map[int64]map[int64]Header // account_id → 到达检测基线（UID→信头，最新窗口）
}

// New 构造。
func New(st *store.Store) *Manager {
	return &Manager{st: st, unread: map[int64]int{}, lastPoll: map[int64]time.Time{},
		arrived: map[int64]map[int64]Header{}}
}

// Run 常驻调度（ctx 取消退出）：节拍检查各账户到期即刷。
func (m *Manager) Run(ctx context.Context) {
	m.RefreshAll() // 启动先刷一轮，角标即刻可用
	t := time.NewTicker(schedTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.pollDue(ctx, now)
		}
	}
}

// pollDue 刷所有到期的账户。清单走轻量元信息（不解密），仅到期账户按 id
// 取明文凭据——15s 一拍的调度不再每拍解密全部账户密码。
func (m *Manager) pollDue(ctx context.Context, now time.Time) {
	accounts, err := m.st.ListMailAccountMetas()
	if err != nil {
		return
	}
	m.mu.Lock()
	var dueIDs []int64
	for _, a := range accounts {
		interval := time.Duration(a.PollSec) * time.Second
		if last, ok := m.lastPoll[a.ID]; ok && now.Sub(last) < interval {
			continue
		}
		m.lastPoll[a.ID] = now
		dueIDs = append(dueIDs, a.ID)
	}
	m.mu.Unlock()
	for _, id := range dueIDs {
		select { // ctx 取消即停（在途账户等自身超时收尾）——停机不被整轮串行拨号拖住
		case <-ctx.Done():
			return
		default:
		}
		a, err := m.st.GetMailAccount(id)
		if err != nil {
			continue
		}
		m.refreshOne(*a)
	}
	m.gc(accounts)
}

// RefreshAll 立即刷全部账户（新建/编辑账户后调用，角标即时生效）。
func (m *Manager) RefreshAll() {
	accounts, err := m.st.ListMailAccountMetas()
	if err != nil {
		return
	}
	now := time.Now()
	m.mu.Lock()
	for _, a := range accounts {
		m.lastPoll[a.ID] = now
	}
	m.mu.Unlock()
	for _, a := range accounts {
		one, err := m.st.GetMailAccount(a.ID)
		if err != nil {
			continue
		}
		m.refreshOne(*one)
	}
	m.gc(accounts)
}

func (m *Manager) refreshOne(a store.MailAccount) {
	cl, err := DialIMAP(a)
	if err != nil {
		m.pollFailed(a.ID)
		_ = m.st.RecordMailAccountStatus(a.ID, false, err.Error())
		return
	}
	defer cl.Close()
	n, err := cl.Unread("INBOX")
	if err != nil {
		m.pollFailed(a.ID)
		_ = m.st.RecordMailAccountStatus(a.ID, false, err.Error())
		return
	}
	m.setUnread(a.ID, n)
	// 到达检测（542）：INBOX 最新窗口信头 UID 与基线差分。只对到达且【仍未读】
	// 的信提醒（542 同日返工用户指定：已被其他客户端先读掉的不打扰）；信头
	// 拉取失败不动基线本轮跳过（同 Emby Sessions 口径），未读角标照常已更新。
	headers, err := cl.List("INBOX", "", pollSyncCount, 0)
	if err != nil {
		log.Printf("邮件到达检测拉取信头失败（%s）: %v", a.Email, err)
		_ = m.st.RecordMailAccountStatus(a.ID, true, "")
		return
	}
	cur := make(map[int64]Header, len(headers))
	for _, h := range headers {
		cur[h.UID] = h
	}
	if prev, ok := m.arrivedSnapshot(a.ID); ok {
		var news []Header
		for uid, h := range cur {
			if _, old := prev[uid]; !old && !h.Seen {
				news = append(news, h)
			}
		}
		if len(news) > 0 {
			m.notifyNewMail(a, news, n)
		}
	}
	m.setArrived(a.ID, cur)
	// 信头同步入库（2026-10-03 用户指定：列表改本地库直出，轮询即增量合并
	// 最新 50 封；只合并不清空，手动刷新才是整夹以服务器为准）
	_ = m.st.UpsertMailHeaders(a.ID, "INBOX", toStoreHeaders(headers))
	_ = m.st.RecordMailAccountStatus(a.ID, true, "")
}

// pollFailed 轮询失败：未读记 -1 并清到达基线——恢复后的第一帧只重新立基线
// 不提醒（断连一天恢复时窗口内全是「新 UID」，不清会轰炸）。
func (m *Manager) pollFailed(id int64) {
	m.setUnread(id, -1)
	m.mu.Lock()
	delete(m.arrived, id)
	m.mu.Unlock()
}

// pollSyncCount 轮询同步进缓存的信头条数（与列表首屏 limit 同量级）。
const pollSyncCount = 50

func toStoreHeaders(list []Header) []store.MailHeader {
	out := make([]store.MailHeader, 0, len(list))
	for _, h := range list {
		out = append(out, store.MailHeader{UID: h.UID, FromName: h.FromName, FromAddr: h.FromAddr,
			Subject: h.Subject, DateUnix: h.DateUnix, Seen: h.Seen, HasAtt: h.HasAtt, Size: h.Size})
	}
	return out
}

// notifyNewMail 邮件到达提醒（334 站内 + 542 三渠道外推）：一轮内多封合并一条
// （逐行列明细封顶 5 行防轰炸）；站内消息恒写，三渠道各按 渠道总开关×新邮件
// 开关 推送（开关每次现读，改后即时生效）。
func (m *Manager) notifyNewMail(a store.MailAccount, news []Header, unread int) {
	sort.Slice(news, func(i, j int) bool { return news[i].UID > news[j].UID })
	name := a.Name
	if name == "" {
		name = a.Email
	}
	subject, body, title := mailMessage(name, a.Email, news, unread)
	for _, ch := range []struct {
		on    func(*store.Store) bool
		send  func(*store.Store, string, string) error
		label string
	}{
		{func(s *store.Store) bool { return wxpusher.On(s) && wxpusher.MailWXOn(s) },
			wxpusher.SendFromStore, "WxPusher"},
		{func(s *store.Store) bool { return webhook.On(s) && webhook.MailOn(s) },
			webhook.SendFromStore, "Webhook"},
		{func(s *store.Store) bool { return webpush.On(s) && webpush.MailOn(s) },
			webpush.SendFromStore, "Web Push"},
	} {
		if !ch.on(m.st) {
			continue
		}
		if err := ch.send(m.st, subject, body); err != nil {
			log.Printf("新邮件提醒 %s 推送失败（%s）: %v", ch.label, a.Email, err)
		}
	}
	if err := m.st.CreateMessage(&store.Message{
		Type:    "mail",
		Title:   title,
		Content: body,
	}); err != nil {
		log.Printf("邮件到达提醒写入消息中心失败（%s）: %v", a.Email, err)
	}
}

// mailMessage 合成新邮件提醒的 推送标题/正文/站内标题。
func mailMessage(name, email string, news []Header, unread int) (subject, body, title string) {
	from := func(h Header) string {
		if h.FromName != "" {
			return h.FromName
		}
		if h.FromAddr != "" {
			return h.FromAddr
		}
		return "未知发件人"
	}
	subj := func(h Header) string {
		if strings.TrimSpace(h.Subject) != "" {
			return h.Subject
		}
		return "（无主题）"
	}
	if len(news) == 1 {
		h := news[0]
		subject = "[PicoPanel] 新邮件：" + name
		title = "新邮件提醒：" + name
		body = fmt.Sprintf("%s（%s）收到新邮件：\n主题：%s\n发件人：%s\n当前 INBOX 未读 %d 封，请注意查收。",
			name, email, subj(h), from(h), unread)
		return subject, body, title
	}
	var lines strings.Builder
	for i, h := range news {
		if i >= 5 {
			fmt.Fprintf(&lines, "… 其余 %d 封略\n", len(news)-i)
			break
		}
		fmt.Fprintf(&lines, "· %s（%s）\n", subj(h), from(h))
	}
	subject = fmt.Sprintf("[PicoPanel] 新邮件：%s（%d 封）", name, len(news))
	title = fmt.Sprintf("新邮件提醒：%s（%d 封）", name, len(news))
	body = fmt.Sprintf("%s（%s）收到 %d 封新邮件：\n%s当前 INBOX 未读 %d 封，请注意查收。",
		name, email, len(news), lines.String(), unread)
	return subject, body, title
}

func (m *Manager) setUnread(id int64, n int) {
	m.mu.Lock()
	m.unread[id] = n
	m.mu.Unlock()
}

// setArrived / arrivedSnapshot 到达检测基线的存取（UID→信头，最新窗口）。
func (m *Manager) setArrived(id int64, cur map[int64]Header) {
	m.mu.Lock()
	m.arrived[id] = cur
	m.mu.Unlock()
}

func (m *Manager) arrivedSnapshot(id int64) (map[int64]Header, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	prev, ok := m.arrived[id]
	return prev, ok
}

func (m *Manager) gc(accounts []store.MailAccount) {
	alive := map[int64]bool{}
	for _, a := range accounts {
		alive[a.ID] = true
	}
	m.mu.Lock()
	for id := range m.unread {
		if !alive[id] {
			delete(m.unread, id)
			delete(m.lastPoll, id)
			delete(m.arrived, id)
		}
	}
	m.mu.Unlock()
}

// Drop 删除账户时清缓存。
func (m *Manager) Drop(id int64) {
	m.mu.Lock()
	delete(m.unread, id)
	delete(m.lastPoll, id)
	delete(m.arrived, id)
	m.mu.Unlock()
}

// UnreadAll 未读快照（account_id → INBOX 未读数）。
func (m *Manager) UnreadAll() map[int64]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[int64]int, len(m.unread))
	for k, v := range m.unread {
		out[k] = v
	}
	return out
}

// AdjustUnread 未读缓存增量修正（372：seen/删除接口即时同步用）。仅对已轮询过
// 的账户生效——无记录时插入合成值会让下轮轮询把真实值误判成「从 0 上涨」触发
// 假到达提醒，保持缺省语义；负向修正下限 0。
func (m *Manager) AdjustUnread(id int64, delta int) {
	if delta == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.unread[id]
	if !ok {
		return
	}
	v += delta
	if v < 0 {
		v = 0
	}
	m.unread[id] = v
}

// Unread 单账户未读（无记录 -2 = 尚未轮询；map 缺省值 0 会把首刷误判成
// 「从 0 上涨」触发假到达提醒，必须显式判存在）。
func (m *Manager) Unread(id int64) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.unread[id]
	if !ok {
		return -2
	}
	return v
}
