package assistant

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

// ICS 订阅刷新：抓取 → 解析 → 按窗口展开 → 整订阅替换事件表。失败只记错误、
// 保留上一轮事件（宁旧勿空）。

const (
	subFetchTimeout = 20 * time.Second
	subMaxBytes     = 5 << 20 // 5MB，个人日历远小于此；防异常源撑爆内存
	subMaxEvents    = 3000
	subWindowBack   = 30  // 天
	subWindowAhead  = 365 // 天
	subRefreshEvery = 6 * time.Hour
	subMaxURLLen    = 2048
)

var subClient = &http.Client{
	Timeout: subFetchTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("重定向过多")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("重定向到非 http(s) 地址")
		}
		return nil
	},
}

// ValidateSubURL 订阅地址只收 http(s)（webcal:// 自动改写为 https://）。
func ValidateSubURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("订阅地址不能为空")
	}
	if len(raw) > subMaxURLLen {
		return "", errors.New("订阅地址过长")
	}
	if strings.HasPrefix(strings.ToLower(raw), "webcal://") {
		raw = "https://" + raw[len("webcal://"):]
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("订阅地址须为 http(s):// 开头的完整 URL")
	}
	return u.String(), nil
}

// RefreshSub 刷新一个订阅；返回错误同时已写入 last_error。
func RefreshSub(st *store.Store, sub store.CalendarSub, now time.Time) error {
	events, err := fetchAndExpand(sub.URL, now)
	if err != nil {
		_ = st.MarkCalendarSubError(sub.ID, truncateRunes(err.Error(), 200))
		return err
	}
	if err := st.ReplaceCalendarEvents(sub.ID, events); err != nil {
		_ = st.MarkCalendarSubError(sub.ID, "写入事件失败: "+truncateRunes(err.Error(), 150))
		return err
	}
	return nil
}

func fetchAndExpand(rawURL string, now time.Time) ([]store.CalendarEvent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), subFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("地址无效: %w", err)
	}
	req.Header.Set("User-Agent", "PicoPanel-Calendar/1.0")
	req.Header.Set("Accept", "text/calendar, */*;q=0.5")
	resp, err := subClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, subMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取失败: %w", err)
	}
	if len(body) > subMaxBytes {
		return nil, errors.New("日历文件超过 5MB")
	}
	vevents, err := ParseICS(body)
	if err != nil {
		return nil, err
	}
	return expandAll(vevents, now), nil
}

// expandAll 把解析出的 VEVENT 展开到窗口内；同 UID 的 RECURRENCE-ID 覆盖实例
// 替代主规则对应那次发生（取消的覆盖直接抹掉）。
func expandAll(vevents []VEvent, now time.Time) []store.CalendarEvent {
	today := dateOnly(now)
	winFrom, winTo := today.AddDate(0, 0, -subWindowBack), today.AddDate(0, 0, subWindowAhead)
	overrides := map[string]map[int64]bool{}
	for _, v := range vevents {
		if !v.RecurrenceID.IsZero() && v.UID != "" {
			if overrides[v.UID] == nil {
				overrides[v.UID] = map[int64]bool{}
			}
			overrides[v.UID][v.RecurrenceID.Unix()] = true
		}
	}
	var out []store.CalendarEvent
	for _, v := range vevents {
		if v.Cancelled {
			continue
		}
		var ov map[int64]bool
		if v.RecurrenceID.IsZero() {
			ov = overrides[v.UID]
		}
		for _, oc := range Expand(v, winFrom, winTo, ov) {
			title := strings.TrimSpace(v.Summary)
			if title == "" {
				title = "(无标题)"
			}
			out = append(out, store.CalendarEvent{UID: v.UID, Title: truncateRunes(title, 200), StartAt: oc.Start.Unix(),
				EndAt: oc.End.Unix(), AllDay: oc.AllDay, Location: truncateRunes(v.Location, 200),
				Description: truncateRunes(v.Description, 1000)})
			if len(out) >= subMaxEvents {
				return out
			}
		}
	}
	return out
}

// RefreshAllSubs 刷新全部启用订阅（逐个独立容错）。
func RefreshAllSubs(st *store.Store, now time.Time) {
	subs, err := st.ListCalendarSubs()
	if err != nil {
		log.Printf("日历订阅清单读取失败: %v", err)
		return
	}
	for _, s := range subs {
		if !s.Enabled {
			continue
		}
		if err := RefreshSub(st, s, now); err != nil {
			log.Printf("日历订阅「%s」刷新失败: %v", s.Name, err)
		}
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
