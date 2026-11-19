package assistant

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"lz-monitor/internal/server/store"
)

// ICS 导出订阅：手机日历用带令牌的 URL 订阅面板自有数据。令牌存 settings，
// 首次打开导出面板时生成，可随时重置（旧 URL 立即失效）。

const (
	keyFeedToken   = "calendar_feed_token"
	keyFeedSources = "calendar_feed_sources"
)

// FeedSources 导出哪些来源（ICS 订阅事件不再转出、B 站更新太碎不进日历订阅）。
type FeedSources struct {
	Reminders   bool `json:"reminders"`
	Memberships bool `json:"memberships"`
	Countdowns  bool `json:"countdowns"`
	TV          bool `json:"tv"`
	Certs       bool `json:"certs"`
}

func defaultFeedSources() FeedSources {
	return FeedSources{Reminders: true, Memberships: true, Countdowns: true, TV: true}
}

// LoadFeedSources 读导出来源配置（缺省：提醒/会员/倒数日/追剧开、证书关）。
func LoadFeedSources(st *store.Store) FeedSources {
	v, ok, err := st.GetSetting(keyFeedSources)
	if err != nil || !ok || v == "" {
		return defaultFeedSources()
	}
	var fs FeedSources
	if json.Unmarshal([]byte(v), &fs) != nil {
		return defaultFeedSources()
	}
	return fs
}

// SaveFeedSources 写导出来源配置。
func SaveFeedSources(st *store.Store, fs FeedSources) error {
	b, _ := json.Marshal(fs)
	return st.SetSetting(keyFeedSources, string(b))
}

// FeedToken 读令牌（空=尚未生成，订阅端点 404）。
func FeedToken(st *store.Store) string {
	v, _, _ := st.GetSetting(keyFeedToken)
	return v
}

// EnsureFeedToken 无令牌则生成（32 字节随机 hex）。
func EnsureFeedToken(st *store.Store) (string, error) {
	if v := FeedToken(st); v != "" {
		return v, nil
	}
	return RotateFeedToken(st)
}

// RotateFeedToken 重置令牌。
func RotateFeedToken(st *store.Store) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	if err := st.SetSetting(keyFeedToken, tok); err != nil {
		return "", err
	}
	return tok, nil
}

const feedUIDSuffix = "@picopanel"

// BuildFeed 生成导出日历：窗口为前 30 天到后 1 年（会员/倒数日放宽到 5 年）。
func BuildFeed(st *store.Store, fs FeedSources, now time.Time) ([]byte, error) {
	loc := now.Location()
	today := dateOnly(now)
	from, to := today.AddDate(0, 0, -30), today.AddDate(1, 0, 0)
	fromS, toS := ymd(from), ymd(to)
	var evs []FeedEvent
	if fs.Reminders {
		rs, err := st.RemindersDueBetween(fromS, toS)
		if err != nil {
			return nil, fmt.Errorf("提醒事项: %w", err)
		}
		for _, r := range rs {
			if r.DoneAt != nil {
				continue
			}
			tm := r.DueTime
			if tm == "" {
				tm = "09:00"
			}
			start, err := time.ParseInLocation("2006-01-02 15:04", r.DueDate+" "+tm, loc)
			if err != nil {
				continue
			}
			desc := r.Note
			if r.GroupName != "" {
				desc = "[" + r.GroupName + "] " + desc
			}
			evs = append(evs, FeedEvent{UID: fmt.Sprintf("rem-%d%s", r.ID, feedUIDSuffix), Summary: r.Title,
				Description: desc, Start: start, End: start.Add(30 * time.Minute)})
		}
	}
	if fs.Memberships {
		ms, err := st.ListMemberships()
		if err != nil {
			return nil, fmt.Errorf("会员: %w", err)
		}
		far := ymd(today.AddDate(5, 0, 0))
		for _, m := range ms {
			if m.ExpireDate < fromS || m.ExpireDate > far {
				continue
			}
			d, err := parseYMD(m.ExpireDate, loc)
			if err != nil {
				continue
			}
			evs = append(evs, FeedEvent{UID: fmt.Sprintf("mem-%d%s", m.ID, feedUIDSuffix),
				Summary: m.Biz + " 会员到期", Description: m.Name, Start: d, AllDay: true})
		}
	}
	if fs.Countdowns {
		cs, err := st.ListCountdowns()
		if err != nil {
			return nil, fmt.Errorf("倒数日: %w", err)
		}
		for _, c := range cs {
			anchor, err := parseYMD(c.Date, loc)
			if err != nil {
				continue
			}
			switch {
			case c.Kind == "yearly" && c.Lunar:
				for _, d := range CountdownOccurrences(c, today.AddDate(-1, 0, 0), today.AddDate(5, 0, 0)) {
					evs = append(evs, FeedEvent{UID: fmt.Sprintf("cd-%d-%s%s", c.ID, ymd(d), feedUIDSuffix),
						Summary: c.Name, Description: LunarLabel(c.LunarMonth, c.LunarDay), Start: d, AllDay: true})
				}
			case c.Kind == "yearly":
				// 公历每年：从锚点起 RRULE 重复，订阅端自行展开
				evs = append(evs, FeedEvent{UID: fmt.Sprintf("cd-%d%s", c.ID, feedUIDSuffix), Summary: c.Name,
					Start: anchor, AllDay: true, RRule: "FREQ=YEARLY"})
			default:
				if anchor.Before(today.AddDate(-1, 0, 0)) || anchor.After(today.AddDate(5, 0, 0)) {
					continue
				}
				evs = append(evs, FeedEvent{UID: fmt.Sprintf("cd-%d%s", c.ID, feedUIDSuffix), Summary: c.Name,
					Start: anchor, AllDay: true})
			}
		}
	}
	if fs.TV {
		tvs, err := st.ListTVFollows()
		if err != nil {
			return nil, fmt.Errorf("追剧: %w", err)
		}
		for _, t := range tvs {
			if !t.Enabled || t.Done || t.NextAirDate < fromS || t.NextAirDate > toS {
				continue
			}
			d, err := parseYMD(t.NextAirDate, loc)
			if err != nil {
				continue
			}
			sum := t.Name
			if t.NextEpisode > 0 {
				sum = fmt.Sprintf("《%s》第 %d 集", t.Name, t.NextEpisode)
			}
			evs = append(evs, FeedEvent{UID: fmt.Sprintf("tv-%d-%s%s", t.ID, t.NextAirDate, feedUIDSuffix),
				Summary: sum, Start: d, AllDay: true})
		}
	}
	if fs.Certs {
		certs, err := st.ListCerts()
		if err != nil {
			return nil, fmt.Errorf("证书: %w", err)
		}
		for _, c := range certs {
			if c.NotAfter <= 0 {
				continue
			}
			d := dateOnly(time.Unix(c.NotAfter, 0).In(loc))
			if d.Before(today) || d.After(to) {
				continue
			}
			evs = append(evs, FeedEvent{UID: fmt.Sprintf("cert-%d%s", c.ID, feedUIDSuffix),
				Summary: "证书到期：" + c.Domain, Start: d, AllDay: true})
		}
	}
	return BuildICS("PicoPanel", evs, now), nil
}
