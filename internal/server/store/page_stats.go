package store

import (
	"encoding/json"
	"time"
)

// HomePageStats 主页统计概览区（459）一次拉齐的全部计数：单页小 COUNT 查询，
// SQLite 常表量级，无分页/无快照扫描。Mail 段的未读数在 API 层并入
// （来自 mail.Manager 内存轮询缓存，不经库）。
type HomePageStats struct {
	MessagesUnread int               `json:"messages_unread"`
	Servers        HomePageServers   `json:"servers"`
	Gh             HomePageGh        `json:"gh"`
	MailAccounts   int               `json:"mail_accounts"`
	MailUnread     int               `json:"mail_unread"` // API 层并入（mail.Manager 缓存，库中无此数）
	Reminders      HomePageReminders `json:"reminders"`
	Certs          HomePageCerts     `json:"certs"`
	PT             HomePagePT        `json:"pt"`
	Memberships    HomePageMembers   `json:"memberships"`
	TV             HomePageTV        `json:"tv"`
	GeneratedAt    int64             `json:"generated_at"`
}

// HomePageServers 服务器监控状态：Enabled 机器才算在线候选（与 /servers 列表口径一致），
// Firing = 当前告警中总数（FiringCountByServer 求和）。
type HomePageServers struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
	Firing  int `json:"firing"`
}

// HomePageGh GitHub 发布监控（474）：Repos=启用仓数；News=启用仓中存在
// published_at > seen_at（用户上次看过之后发布）Release 的仓数，主页卡据此提醒。
type HomePageGh struct {
	Repos int `json:"repos"`
	News  int `json:"news"`
}

// HomePageReminders 待办：Open=全部未完成；Today=到期日≤今天未完成（同
// ReminderBoard「我的一天/今天」桶，含逾期）；Overdue=到期日早于今天的未完成。
type HomePageReminders struct {
	Open    int `json:"open"`
	Today   int `json:"today"`
	Overdue int `json:"overdue"`
}

// HomePageCerts 证书：总数 + 10 天内到期数（ExpiringCertCount 同口径；2026-10-21
// 用户指定由 30 天收紧到 10 天，仅主页统计卡口径，顶栏 15 天与提醒周期不动）。
type HomePageCerts struct {
	Total    int `json:"total"`
	Expiring int `json:"expiring"`
}

// HomePagePT 签到站进度：Due=启用且配了签到模式的站；Signed=其中今日已签
// （data_json 扩展段 dian/hdh/hdc 的 signed_today，癫影/栖影/蜂巢三站）。
type HomePagePT struct {
	Due    int `json:"due"`
	Signed int `json:"signed"`
}

// HomePageMembers 会员到期（492）：Total=全部记录数；Expiring=15 日内到期
// （含已过期未删的，MembershipStats 同口径）。
type HomePageMembers struct {
	Total    int `json:"total"`
	Expiring int `json:"expiring"`
}

// HomePageTV 追剧（552）：Total=全部条目（剧+电影）；Done=已完结数（done=1，
// 只有剧集会打 done，电影不追踪自然不计入完结）。
type HomePageTV struct {
	Total int `json:"total"`
	Done  int `json:"done"`
}

// ptSignExtra data_json 中签到相关的最小解出段（快照其余字段不关心）。
type ptSignExtra struct {
	Dian *struct {
		SignedToday bool `json:"signed_today"`
	} `json:"dian"`
	Hdh *struct {
		SignedToday bool `json:"signed_today"`
	} `json:"hdh"`
	Fc *struct {
		SignedToday bool `json:"signed_today"`
	} `json:"fc"`
}

// HomePageStats 聚合主页统计概览全部计数。offlineAfter 为离线判定秒数
// （同 /servers 列表口径，由配置 OfflineAfter 换算传入）。
func (s *Store) HomePageStats(offlineAfter int64) (*HomePageStats, error) {
	out := &HomePageStats{}
	var err error
	if out.MessagesUnread, err = s.UnreadCount(); err != nil {
		return nil, err
	}

	// 服务器：Enabled 才计入在线/离线，禁用机器不算离线（同 serverView.Online）
	//（主页统计高频轮询，走轻量行不解密敏感字段）
	servers, err := s.ListServerLites(false)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	firing, err := s.FiringCountByServer()
	if err != nil {
		return nil, err
	}
	for _, srv := range servers {
		out.Servers.Total++
		if !srv.Enabled {
			continue
		}
		if srv.LastReportAt > 0 && now-srv.LastReportAt <= offlineAfter {
			out.Servers.Online++
		} else {
			out.Servers.Offline++
		}
	}
	for _, n := range firing {
		out.Servers.Firing += n
	}

	// GitHub 发布监控（474）：启用仓数 + 有新更新（seen_at 之后发布过 Release）仓数
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM gh_repos WHERE enabled = 1`).Scan(&out.Gh.Repos); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT r.id) FROM gh_repos r
		JOIN gh_releases l ON l.repo_id = r.id
		WHERE r.enabled = 1 AND l.published_at > r.seen_at`).Scan(&out.Gh.News); err != nil {
		return nil, err
	}

	// 邮件：账号数（未读数 API 层并入 mail.Manager 缓存）
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM mail_accounts`).Scan(&out.MailAccounts); err != nil {
		return nil, err
	}

	// 待办：口径同 ReminderBoard（今天桶含逾期）
	today := time.Now().Format("2006-01-02")
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM reminders WHERE deleted_at = 0 AND done_at IS NULL`).Scan(&out.Reminders.Open); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM reminders WHERE deleted_at = 0 AND done_at IS NULL AND due_date <= ?`, today).Scan(&out.Reminders.Today); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM reminders WHERE deleted_at = 0 AND done_at IS NULL AND due_date < ?`, today).Scan(&out.Reminders.Overdue); err != nil {
		return nil, err
	}

	// 证书：总数 + 10 天内到期
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM https_certs`).Scan(&out.Certs.Total); err != nil {
		return nil, err
	}
	if out.Certs.Expiring, err = s.ExpiringCertCount(10); err != nil {
		return nil, err
	}

	// 会员到期（492）：总数 + 15 日内到期数
	if out.Memberships.Total, out.Memberships.Expiring, err = s.MembershipStats(); err != nil {
		return nil, err
	}

	// 追剧（552）：总数 + 已完结数（单条聚合，表不存在/零条都是 0）
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(done), 0) FROM tv_follows`).Scan(&out.TV.Total, &out.TV.Done); err != nil {
		return nil, err
	}

	// PT 签到进度：enabled 且 signin_mode 非空为应签；已签看 data_json 扩展段
	ptRows, err := s.db.Query(`SELECT signin_mode, data_json FROM pt_sites WHERE enabled = 1 AND signin_mode != ''`)
	if err != nil {
		return nil, err
	}
	defer ptRows.Close()
	for ptRows.Next() {
		var mode, data string
		if err := ptRows.Scan(&mode, &data); err != nil {
			return nil, err
		}
		out.PT.Due++
		var extra ptSignExtra
		_ = json.Unmarshal([]byte(data), &extra) // 解不出按未签处理（快照缺失/脏数据不挡计数）
		if (extra.Dian != nil && extra.Dian.SignedToday) || (extra.Hdh != nil && extra.Hdh.SignedToday) ||
			(extra.Fc != nil && extra.Fc.SignedToday) {
			out.PT.Signed++
		}
	}
	return out, ptRows.Err()
}
