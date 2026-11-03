package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"lz-monitor/internal/server/store"
)

// 远程节假日数据（holiday-cn，社区按国务院通知整理的逐年 JSON）：每天拉当年与下一年，
// 成功写入 settings 缓存并覆盖对应年份；拉不到用缓存，缓存也没有才落回内置表——
// 任何失败都不会比纯内置更差。服务器在国内：jsDelivr 两个节点优先，GitHub raw 兜底。

const (
	keyHolSyncAt      = "holiday_cn_sync_at" // 最近一次成功同步时刻（unix）
	keyHolError       = "holiday_cn_error"   // 最近一次失败原因（成功后清空）
	keyHolCachePrefix = "holiday_cn_"        // + 年份：{fetched_at, days}

	holidayFetchTimeout   = 15 * time.Second
	holidayMaxBytes       = 2 << 20
	holidaySyncEvery      = 24 * time.Hour
	holidayRetryAfter     = time.Hour // 失败后一小时再试（启动时断网不至于等到明天）
	holidayMaxTriesPerDay = 3         // 单日最多 3 次（首拉 + 重试 2 次），仍失败等第二天
	holidayFirstDelay     = 2 * time.Minute
)

// holidayCNURLs 数据源模板（%d=年份），按序尝试；包级变量供测试注入。
var holidayCNURLs = []string{
	"https://cdn.jsdelivr.net/gh/NateScarlet/holiday-cn@master/%d.json",
	"https://fastly.jsdelivr.net/gh/NateScarlet/holiday-cn@master/%d.json",
	"https://raw.githubusercontent.com/NateScarlet/holiday-cn/master/%d.json",
}

var holidayClient = &http.Client{Timeout: holidayFetchTimeout}

type holidayCNFile struct {
	Year int `json:"year"`
	Days []struct {
		Name     string `json:"name"`
		Date     string `json:"date"`
		IsOffDay bool   `json:"isOffDay"`
	} `json:"days"`
}

type holidayCache struct {
	FetchedAt int64        `json:"fetched_at"`
	Days      []HolidayDay `json:"days"`
}

// parseHolidayCN 解析一年的 holiday-cn JSON；年份不符或空清单视为无效。
func parseHolidayCN(body []byte, year int) ([]HolidayDay, error) {
	var f holidayCNFile
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	if f.Year != year {
		return nil, fmt.Errorf("年份不符（文件 %d，期望 %d）", f.Year, year)
	}
	if len(f.Days) == 0 {
		return nil, errors.New("清单为空")
	}
	out := make([]HolidayDay, 0, len(f.Days))
	for _, d := range f.Days {
		if _, err := time.Parse("2006-01-02", d.Date); err != nil || len(d.Date) != 10 || d.Date[:4] != strconv.Itoa(year) {
			continue
		}
		out = append(out, HolidayDay{Date: d.Date, Name: d.Name, Off: d.IsOffDay})
	}
	if len(out) == 0 {
		return nil, errors.New("清单无合法日期")
	}
	sortHolidayDays(out)
	return out, nil
}

// fetchHolidayCN 依次尝试各数据源，首个成功即返回。
func fetchHolidayCN(year int) ([]HolidayDay, error) {
	var lastErr error
	for _, tpl := range holidayCNURLs {
		url := fmt.Sprintf(tpl, year)
		body, err := httpGetLimited(url, holidayMaxBytes)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", url, err)
			continue
		}
		days, err := parseHolidayCN(body, year)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", url, err)
			continue
		}
		return days, nil
	}
	return nil, lastErr
}

func httpGetLimited(url string, max int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), holidayFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "PicoPanel-Assistant/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := holidayClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, errors.New("响应超过 2MB")
	}
	return body, nil
}

func holidayCacheKey(year int) string { return keyHolCachePrefix + strconv.Itoa(year) }

func saveHolidayCache(st *store.Store, year int, days []HolidayDay, at int64) error {
	b, err := json.Marshal(holidayCache{FetchedAt: at, Days: days})
	if err != nil {
		return err
	}
	return st.SetSetting(holidayCacheKey(year), string(b))
}

// LoadHolidayCache 启动时把缓存过的年份（去年/今年/明年）装进合并视图。
func LoadHolidayCache(st *store.Store, now time.Time) {
	for y := now.Year() - 1; y <= now.Year()+1; y++ {
		v, ok, err := st.GetSetting(holidayCacheKey(y))
		if err != nil || !ok || v == "" {
			continue
		}
		var c holidayCache
		if json.Unmarshal([]byte(v), &c) != nil || len(c.Days) == 0 {
			continue
		}
		applyHolidayYear(y, remoteYearData(c.Days, c.FetchedAt))
	}
}

// SyncHolidayCN 拉取当年与下一年；任一年成功即记成功时刻并清错误，全部失败只记错误
// （已有缓存/内置数据原样保留）。返回首个错误（有年份成功时返回 nil）。
func SyncHolidayCN(st *store.Store, now time.Time) error {
	var firstErr error
	got := false
	for _, y := range []int{now.Year(), now.Year() + 1} {
		days, err := fetchHolidayCN(y)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		got = true
		if err := saveHolidayCache(st, y, days, now.Unix()); err != nil {
			log.Printf("节假日缓存写入失败（%d）: %v", y, err)
		}
		applyHolidayYear(y, remoteYearData(days, now.Unix()))
	}
	if got {
		_ = st.SetSetting(keyHolSyncAt, strconv.FormatInt(now.Unix(), 10))
		_ = st.SetSetting(keyHolError, "")
		return nil
	}
	if firstErr != nil {
		_ = st.SetSetting(keyHolError, truncateRunes(firstErr.Error(), 300))
	}
	return firstErr
}

// HolidayStatus 设置弹层数据：同步时刻、最近错误、各年份清单。
type HolidayStatus struct {
	SyncAt int64             `json:"sync_at"`
	Error  string            `json:"error"`
	Years  []HolidayYearInfo `json:"years"`
}

// GetHolidayStatus 读状态（纯本地）。
func GetHolidayStatus(st *store.Store) HolidayStatus {
	return HolidayStatus{
		SyncAt: int64(intSetting(st, keyHolSyncAt, 0)),
		Error:  strSetting(st, keyHolError, ""),
		Years:  HolidayYearsSnapshot(),
	}
}
