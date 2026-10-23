// Package inspect 实现一键巡检报告：汇总全部服务器在线状态、资源占用 TOP、
// 告警中条目与临期证书，供状态页手动生成展示、手动推送，或每日定时推送
// （WxPusher/Webhook 双通道同证书提醒，各按开关）。
//
// 定时配置存 SQLite settings 表（Web「系统设置」页维护，改后即时生效）：
//
//	inspect_push_on      "1"/"0" 每日定时推送开关
//	inspect_push_time    "HH:MM"（本地时区，默认 09:00）
//	inspect_last_push    最近一次定时推送的日期（YYYY-MM-DD，防同日重复）
package inspect

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

const (
	keyPushOn   = "inspect_push_on"
	keyPushTime = "inspect_push_time"
	keyLastPush = "inspect_last_push"
	defaultTime = "09:00"
	topN        = 5 // 资源 TOP 展示条数
	diskWarnPct = 80.0
	diskFullPct = 90.0
)

// ServerLine 是一台服务器的巡检摘要行。
type ServerLine struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Mode      string  `json:"mode"`
	Online    bool    `json:"online"`
	Enabled   bool    `json:"enabled"`
	CPU       float64 `json:"cpu"`
	Mem       float64 `json:"mem"`
	Disk      float64 `json:"disk"`       // 挂载点中的最大使用率
	DiskMount string  `json:"disk_mount"` // 最大使用率对应的挂载点
	Firing    int     `json:"firing"`     // 该机告警中条数
}

// CertLine 是一张临期证书的摘要行。
type CertLine struct {
	Domain string `json:"domain"`
	Days   int64  `json:"days"` // 距到期天数（负数=已过期天数）
	NotAt  int64  `json:"not_at"`
}

// Report 是一份巡检报告（JSON 直出给前端展示）。
type Report struct {
	GeneratedAt int64        `json:"generated_at"`
	Total       int          `json:"total"`
	Online      int          `json:"online"`
	Offline     int          `json:"offline"`
	Disabled    int          `json:"disabled"`
	FiringCount int          `json:"firing_count"`
	CertDue     int          `json:"cert_due"`     // ≤15 天（含已过期）张数
	MailUnread  int          `json:"mail_unread"`  // 各邮箱账户未读合计（轮询缓存口径，同侧栏角标）
	MailLines   []string     `json:"mail_lines"`   // 未读 >0 的账户明细
	Servers     []ServerLine `json:"servers"`      // 离线在前，其余按资源占用降序
	FiringAlert []string     `json:"firing_alert"` // 告警中条目文案
	Certs       []CertLine   `json:"certs"`        // 临期证书（按剩余天数升序）
	DiskWarn    []string     `json:"disk_warn"`    // 磁盘紧张文案（≥80%）
}

// Build 汇总当前巡检报告（现查现算，无需落库）。unreadFn 返回邮箱账户未读
// 快照（account_id → INBOX 未读数，取自 mail.Manager 轮询缓存；nil=不含邮件项）。
func Build(st *store.Store, offlineAfter int64, unreadFn func() map[int64]int) (*Report, error) {
	now := time.Now().Unix()
	servers, err := st.ListServers()
	if err != nil {
		return nil, err
	}
	firingBy, _ := st.FiringCountByServer()
	alerts, _, err := st.ListAlerts("firing", 0, 1, 200)
	if err != nil {
		return nil, err
	}
	certs, err := st.ListCerts()
	if err != nil {
		return nil, err
	}

	r := &Report{GeneratedAt: now, FiringAlert: []string{}, Certs: []CertLine{}, DiskWarn: []string{}, MailLines: []string{}}
	r.Total = len(servers)

	// 未读邮件（2026-10-04 用户指定入巡检）：只列未读 >0 的账户，负数=查询失败不计
	if unreadFn != nil {
		if accounts, err := st.ListMailAccountMetas(); err == nil {
			unread := unreadFn()
			for _, a := range accounts {
				if n := unread[a.ID]; n > 0 {
					r.MailLines = append(r.MailLines, fmt.Sprintf("%s（%s）：%d 封", a.Name, a.Email, n))
					r.MailUnread += n
				}
			}
		}
	}
	for _, c := range certs {
		if c.NotAfter <= 0 {
			continue // 从未成功检查过（无到期时间），不进报告
		}
		days := (c.NotAfter - now) / 86400
		if days <= 15 {
			r.Certs = append(r.Certs, CertLine{Domain: c.Domain, Days: days, NotAt: c.NotAfter})
		}
	}
	sort.Slice(r.Certs, func(i, j int) bool { return r.Certs[i].Days < r.Certs[j].Days })
	r.CertDue = len(r.Certs)

	// 名称统一拼「 | 几C几G」规格后缀（与全站展示口径一致）；
	// specName 供下方告警条目文案复用（告警存量记录里存的是裸名）
	specName := make(map[int64]string, len(servers))
	for _, s := range servers {
		line := ServerLine{ID: s.ID, Name: s.Name, Mode: s.CollectMode, Enabled: s.Enabled}
		if !s.Enabled {
			r.Disabled++
		} else if s.LastReportAt > 0 && now-s.LastReportAt <= offlineAfter {
			line.Online = true
			r.Online++
		} else {
			r.Offline++
		}
		line.Firing = firingBy[s.ID]
		if m, err := st.LatestMetric(s.ID); err == nil && m != nil {
			line.Name = s.Name
			line.CPU = m.CPU
			line.Mem = m.Mem
			for _, d := range m.Disks {
				if d.Percent > line.Disk {
					line.Disk, line.DiskMount = d.Percent, d.Mount
				}
			}
			if line.Online && line.Disk >= diskWarnPct {
				level := "紧张"
				if line.Disk >= diskFullPct {
					level = "危急"
				}
				r.DiskWarn = append(r.DiskWarn,
					fmt.Sprintf("【%s】%s 已用 %.1f%%（%s）", line.Name, line.DiskMount, line.Disk, level))
			}
		}
		specName[s.ID] = line.Name
		r.Servers = append(r.Servers, line)
	}
	for _, a := range alerts {
		n := a.ServerName
		if sn := specName[a.ServerID]; sn != "" {
			n = sn
		}
		r.FiringAlert = append(r.FiringAlert,
			fmt.Sprintf("【%s】%s", n, a.Detail))
	}
	r.FiringCount = len(r.FiringAlert)
	// 展示顺序：离线/停用在前（需要关注），其余 CPU 降序
	sort.SliceStable(r.Servers, func(i, j int) bool {
		a, b := r.Servers[i], r.Servers[j]
		if a.Online != b.Online {
			return !a.Online
		}
		return a.CPU > b.CPU
	})
	return r, nil
}

// RenderText 渲染为推送文本（subject 首行摘要，body 明细）。
func (r *Report) RenderText() (string, string) {
	subject := fmt.Sprintf("[PicoPanel] 巡检报告：在线 %d / 离线 %d / 停用 %d，告警中 %d，证书临期 %d",
		r.Online, r.Offline, r.Disabled, r.FiringCount, r.CertDue)
	if r.MailUnread > 0 {
		subject += fmt.Sprintf("，未读邮件 %d", r.MailUnread)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "巡检时间：%s\n", time.Unix(r.GeneratedAt, 0).Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "服务器：共 %d 台，在线 %d / 离线 %d / 停用 %d\n", r.Total, r.Online, r.Offline, r.Disabled)
	if r.Offline > 0 {
		var off []string
		for _, s := range r.Servers {
			if s.Enabled && !s.Online {
				off = append(off, s.Name)
			}
		}
		fmt.Fprintf(&b, "离线服务器：%s\n", strings.Join(off, "、"))
	}
	if len(r.MailLines) > 0 {
		b.WriteString("\n未读邮件：\n")
		for _, line := range r.MailLines {
			b.WriteString("- " + line + "\n")
		}
	}
	if len(r.FiringAlert) > 0 {
		b.WriteString("\n告警中：\n")
		for i, line := range r.FiringAlert {
			if i >= 10 {
				fmt.Fprintf(&b, "…等共 %d 条\n", len(r.FiringAlert))
				break
			}
			fmt.Fprintf(&b, "%d. %s\n", i+1, line)
		}
	}
	if len(r.DiskWarn) > 0 {
		b.WriteString("\n磁盘紧张：\n")
		for _, line := range r.DiskWarn {
			b.WriteString(line + "\n")
		}
	}
	if len(r.Certs) > 0 {
		b.WriteString("\n证书临期（15 天内）：\n")
		for _, c := range r.Certs {
			state := fmt.Sprintf("还有 %d 天", c.Days)
			if c.Days < 0 {
				state = fmt.Sprintf("已过期 %d 天", -c.Days)
			}
			fmt.Fprintf(&b, "- %s（%s）\n", c.Domain, state)
		}
	}
	// 资源 TOP：仅统计在线机器
	b.WriteString("\n资源占用 TOP（CPU）：\n")
	n := 0
	for _, s := range r.Servers {
		if !s.Online {
			continue
		}
		n++
		if n > topN {
			break
		}
		disk := "-"
		if s.DiskMount != "" {
			disk = fmt.Sprintf("%s %.1f%%", s.DiskMount, s.Disk)
		}
		fmt.Fprintf(&b, "%d. %s：CPU %.1f%% / 内存 %.1f%% / 磁盘 %s\n",
			n, s.Name, s.CPU, s.Mem, disk)
	}
	return subject, b.String()
}

// GenerateAndDeliver 生成一份巡检报告：先写站内消息中心（巡检类型，始终落库，
// 渠道全关或全部失败时记录也不丢），再按各渠道开关推送。
// 返回值只反映渠道推送错误——渠道全关是正常态，返回 nil。
func GenerateAndDeliver(st *store.Store, offlineAfter int64, unreadFn func() map[int64]int) error {
	r, err := Build(st, offlineAfter, unreadFn)
	if err != nil {
		return fmt.Errorf("生成巡检报告失败: %w", err)
	}
	subject, body := r.RenderText()
	if err := st.CreateMessage(&store.Message{
		Type:      "inspect",
		Title:     "巡检报告（" + time.Now().Format("2006-01-02") + "）",
		Content:   body,
		RefType:   "inspect",
		CreatedAt: time.Now().Unix(),
	}); err != nil {
		log.Printf("巡检报告站内消息写入失败: %v", err)
	}
	return pushChannels(st, subject, body)
}

// PushNow 手动立即推送：生成报告入站内消息中心并推送渠道。
func PushNow(st *store.Store, offlineAfter int64, unreadFn func() map[int64]int) error {
	return GenerateAndDeliver(st, offlineAfter, unreadFn)
}

// pushChannels 推送到已开启的渠道（WxPusher/Webhook/Web Push 各按开关与细分开关，
// 至少一路成功才算成功；巡检跟随各渠道任一细分开关）。
func pushChannels(st *store.Store, subject, body string) error {
	wxOn := wxpusher.On(st) && (wxpusher.AlertWXOn(st) || wxpusher.CertWXOn(st))
	whOn := webhook.On(st) && (webhook.AlertOn(st) || webhook.CertOn(st))
	wpOn := webpush.On(st) && (webpush.AlertOn(st) || webpush.CertOn(st))
	if !wxOn && !whOn && !wpOn {
		return nil
	}
	var errs []string
	sent := false
	if wxOn {
		if err := wxpusher.SendFromStore(st, subject, body); err != nil {
			errs = append(errs, "WxPusher: "+err.Error())
		} else {
			sent = true
		}
	}
	if whOn {
		if err := webhook.SendFromStore(st, subject, body); err != nil {
			errs = append(errs, "Webhook: "+err.Error())
		} else {
			sent = true
		}
	}
	if wpOn {
		if err := webpush.SendFromStore(st, subject, body); err != nil {
			errs = append(errs, "Web Push: "+err.Error())
		} else {
			sent = true
		}
	}
	if !sent {
		return fmt.Errorf("推送失败：%s", strings.Join(errs, "；"))
	}
	return nil
}

// PushOn / PushTime / lastPush 读定时推送配置。
func PushOn(st *store.Store) bool {
	v, ok, err := st.GetSetting(keyPushOn)
	return err == nil && ok && v == "1"
}

func PushTime(st *store.Store) string {
	v, ok, err := st.GetSetting(keyPushTime)
	if err != nil || !ok {
		return defaultTime
	}
	return v
}

func lastPush(st *store.Store) string {
	v, ok, _ := st.GetSetting(keyLastPush)
	if !ok {
		return ""
	}
	return v
}

// SetPushConfig 写定时推送配置（time 为空保持不变/回退默认）。
func SetPushConfig(st *store.Store, on bool, pushTime string) error {
	if err := st.SetSetting(keyPushOn, boolStr(on)); err != nil {
		return err
	}
	if pushTime != "" {
		if !validHHMM(pushTime) {
			return fmt.Errorf("推送时间格式应为 HH:MM")
		}
		if err := st.SetSetting(keyPushTime, pushTime); err != nil {
			return err
		}
	}
	return nil
}

func validHHMM(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	return err1 == nil && err2 == nil && h >= 0 && h < 24 && m >= 0 && m < 60
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Run 每日定时推送循环：每 30s 醒一次，越过当日配置时刻且今天还没推过才推
// （服务端不在线的时刻会顺延到下次醒来的第一轮补推）。
func Run(ctx context.Context, st *store.Store, offlineAfter int64, unreadFn func() map[int64]int) {
	const tick = 30 * time.Second
	for {
		now := time.Now()
		if PushOn(st) {
			h, m := parseHHMM(PushTime(st))
			fire := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
			today := now.Format("2006-01-02")
			if !now.Before(fire) && lastPush(st) != today {
				// GenerateAndDeliver 的错误只代表渠道推送失败（站内消息已落库）；
				// 无论成败都记日期，失败不整点重试刷屏，等明天再推
				if err := GenerateAndDeliver(st, offlineAfter, unreadFn); err != nil {
					log.Printf("巡检报告定时推送失败: %v", err)
				} else {
					log.Printf("巡检报告已定时推送")
				}
				// 无论成败都记日期：失败时不整点重试刷屏，等明天再推
				_ = st.SetSetting(keyLastPush, today)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(tick):
		}
	}
}

func parseHHMM(s string) (int, int) {
	h, m := 9, 0
	if !validHHMM(s) {
		return h, m
	}
	parts := strings.Split(s, ":")
	h, _ = strconv.Atoi(parts[0])
	m, _ = strconv.Atoi(parts[1])
	return h, m
}
