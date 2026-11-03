// Package certcheck 实现 HTTPS 证书到期监控：对 https_certs 表里的每个域名
// 定期 TLS 拨号取对端证书，回写颁发者/起止时间，并在到期前 RemindDays 天
// （含已过期）经 WxPusher/Webhook 推送提醒（每天最多一条/域名，notified_on 去重）。
// 注：证书续签功能已整体下线（2026-10-19，TODO 25）。
package certcheck

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
)

// RemindDays 到期前多少天开始提醒（用户需求：半个月）。
const RemindDays = 15

// Run 阻塞循环：启动立即查一轮，之后每 12 小时查一轮。
func Run(ctx context.Context, st *store.Store) {
	CheckAll(st)
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			CheckAll(st)
		}
	}
}

// checkMu CheckAll 防重入：API「立即检查」与 12h 后台轮并发跑两份时，各自
// 持独立证书快照，NotifiedOn 的判-发-标非原子，同一证书可能双推三渠道。
var checkMu sync.Mutex

// CheckAll 检查全部证书并回写结果（API「立即检查」也走这里）。
func CheckAll(st *store.Store) {
	checkMu.Lock()
	defer checkMu.Unlock()
	certs, err := st.ListCerts()
	if err != nil {
		log.Printf("读取证书清单失败: %v", err)
		return
	}
	for i := range certs {
		CheckOne(st, &certs[i])
	}
}

// CheckOne 拨号检查单个证书：回写结果，到期窗口内按日去重经推送渠道提醒。
func CheckOne(st *store.Store, c *store.Cert) {
	r, err := dial(c.Domain, c.Port)
	if err != nil {
		if serr := st.SaveCertCheck(c.ID, store.CertResult{}, err.Error()); serr != nil {
			log.Printf("回写证书检查结果失败 (cert=%d): %v", c.ID, serr)
		}
		log.Printf("证书检查失败 %s:%d: %v", c.Domain, c.Port, err)
		return
	}
	if err := st.SaveCertCheck(c.ID, r, ""); err != nil {
		log.Printf("回写证书检查结果失败 (cert=%d): %v", c.ID, err)
	}
	c.NotBefore, c.NotAfter = r.NotBefore, r.NotAfter
	c.Issuer, c.IssuerOrg, c.IssuedTo, c.Subject = r.Issuer, r.IssuerOrg, r.IssuedTo, r.Subject
	c.LastError = ""

	days := daysLeft(r.NotAfter)

	// 到期提醒：≤15 天（含已过期）每天一封（notified_on 记录最近发送日）
	if days > RemindDays {
		return
	}
	// 三渠道并列：各自「总开关 && 证书细分开关」都开才推
	wxOn := wxpusher.On(st) && wxpusher.CertWXOn(st)
	whOn := webhook.On(st) && webhook.CertOn(st)
	wpOn := webpush.On(st) && webpush.CertOn(st)
	if !wxOn && !whOn && !wpOn {
		return
	}
	today := time.Now().Format("2006-01-02")
	if c.NotifiedOn == today {
		return
	}
	state := fmt.Sprintf("还有 %d 天到期", days)
	if days < 0 {
		state = fmt.Sprintf("已过期 %d 天", -days)
	}
	issuerLine := r.Issuer
	if r.IssuerOrg != "" && r.IssuerOrg != r.Issuer {
		issuerLine = r.IssuerOrg + "（" + r.Issuer + "）"
	}
	wxSubject := fmt.Sprintf("[PicoPanel] 证书到期提醒：%s（%s）", c.Domain, state)
	wxBody := fmt.Sprintf("域名：%s\n颁发给：%s\n颁发者：%s\n颁发时间：%s\n到期时间：%s\n剩余：%s\n（证书换新后自动停止提醒）\n",
		c.Domain, r.IssuedTo, issuerLine,
		time.Unix(r.NotBefore, 0).Format("2006-01-02 15:04:05"),
		time.Unix(r.NotAfter, 0).Format("2006-01-02 15:04:05"),
		state)
	// 双通道并存：WxPusher 与 Webhook 各按自己的开关推送，任一成功即记 notified_on
	//（发送全失败不记，下一轮（12 小时后）自然重试）
	sent := false
	if wxOn {
		if err := wxpusher.SendFromStore(st, wxSubject, wxBody); err != nil {
			log.Printf("证书到期提醒 WxPusher 推送失败 (cert=%d): %v", c.ID, err)
		} else {
			sent = true
		}
	}
	if whOn {
		if err := webhook.SendFromStore(st, wxSubject, wxBody); err != nil {
			log.Printf("证书到期提醒 Webhook 推送失败 (cert=%d): %v", c.ID, err)
		} else {
			sent = true
		}
	}
	if wpOn {
		if err := webpush.SendFromStore(st, wxSubject, wxBody); err != nil {
			log.Printf("证书到期提醒 Web Push 推送失败 (cert=%d): %v", c.ID, err)
		} else {
			sent = true
		}
	}
	if !sent {
		return
	}
	// 证书到期提醒同时入站为「告警」类站内消息（与推送同去重节奏：一天一条）
	if err := st.CreateMessage(&store.Message{
		Type: "alert", Title: "证书到期提醒：" + c.Domain + "（" + state + "）",
		Content: wxBody,
		RefType: "cert", RefID: c.ID,
		CreatedAt: time.Now().Unix(),
	}); err != nil {
		log.Printf("证书到期提醒入站消息失败 (cert=%d): %v", c.ID, err)
	}
	if err := st.MarkCertNotified(c.ID, today); err != nil {
		log.Printf("记录提醒日期失败 (cert=%d): %v", c.ID, err)
	}
}

// daysLeft 距到期天数（负=已过期）。
func daysLeft(notAfter int64) int64 {
	return int64(time.Until(time.Unix(notAfter, 0)).Hours() / 24)
}

// dial 对 domain:port 做 TLS 握手并取对端叶子证书。
// 颁发者取 CommonName（如 R11 / DigiCert TLS RSA SHA256 2020 CA1，比完整
// DN 序列化更贴近用户认知），组织单独取（O 字段，如 Let's Encrypt）；
// 颁发给取 Subject CN（现代证书 CN 常为空，取首个 SAN 兜底）；
// 证书覆盖的域名取 SAN 列表。
func dial(domain string, port int) (store.CertResult, error) {
	addr := fmt.Sprintf("%s:%d", domain, port)
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr,
		&tls.Config{ServerName: domain})
	if err != nil {
		return store.CertResult{}, fmt.Errorf("TLS 连接失败: %w", err)
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return store.CertResult{}, fmt.Errorf("对端未返回证书")
	}
	leaf := certs[0]
	issuer := leaf.Issuer.CommonName
	if issuer == "" {
		issuer = leaf.Issuer.String()
	}
	issuedTo := leaf.Subject.CommonName
	if issuedTo == "" && len(leaf.DNSNames) > 0 {
		issuedTo = leaf.DNSNames[0]
	}
	sans := leaf.DNSNames
	if len(sans) == 0 && leaf.Subject.CommonName != "" {
		sans = []string{leaf.Subject.CommonName}
	}
	if len(sans) > 5 { // 超长 SAN 截断，避免一行展示不下
		sans = append(sans[:5:5], fmt.Sprintf("等 %d 个", len(leaf.DNSNames)))
	}
	return store.CertResult{
		NotBefore: leaf.NotBefore.Unix(),
		NotAfter:  leaf.NotAfter.Unix(),
		Issuer:    issuer,
		IssuerOrg: strings.Join(leaf.Issuer.Organization, "、"),
		IssuedTo:  issuedTo,
		Subject:   strings.Join(sans, ", "),
	}, nil
}
