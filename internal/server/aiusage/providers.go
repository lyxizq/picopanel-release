package aiusage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"lz-monitor/internal/server/store"
)

// HTTPTimeout 上游查询超时（余额/额度接口都是轻查询，15s 足够）。
const HTTPTimeout = 15 * time.Second

var httpClient = &http.Client{Timeout: HTTPTimeout}

// getJSON GET {base}{path}（Bearer key）并解析 JSON；非 2xx 带状态码报错，
// 响应体截断防刷日志。返回原始 body 供业务层在「解析得到空数据」时带进错误
// （智谱该接口对无效 Key 也回 HTTP 200，业务错误只在 JSON 里，必须留底可查）。
func getJSON(base, path, key string, out any) ([]byte, error) {
	return getJSONH(base, path, "Authorization", "Bearer "+key, out)
}

// getJSONH 同 getJSON，鉴权头形态由调用方给（和风控制台 API 用 X-QW-Api-Key，
// 3）：header=value 原样设置。
func getJSONH(base, path, header, value string, out any) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set(header, value)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// 带上实际请求地址（URL 无密钥，可示人）——404 多为自定义站点没有
		// 该端点（中转站通常不代理余额/额度接口），一眼定位填错
		return nil, fmt.Errorf("上游返回 %d（请求地址 %s）: %s", resp.StatusCode, req.URL, truncate(string(body), 160))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w（原始响应: %s）", err, truncate(string(body), 200))
	}
	return body, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// ---- 智谱 GLM（Coding Plan 额度）----

type zhipuResp struct {
	Code    json.RawMessage `json:"code"` // 实测无效 Key 也回 HTTP 200，业务错误走 JSON：{"code":401,"msg":"令牌已过期…","success":false}；code 可能为数字或字符串
	Msg     string          `json:"msg"`
	Success *bool           `json:"success"`
	Data    struct {
		Level  string      `json:"level"`
		Limits []zhipuCard `json:"limits"`
	} `json:"data"`
}

// zhipuCode 业务码提取（数字/字符串双形态兼容；缺失返回 0,false）。
func zhipuCode(raw json.RawMessage) (int, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, true // 形态异常按「有码但非 200」处理，走业务错误分支带出原文
	}
	return n, true
}

// zhipuCard unit: 3=小时(number=窗口小时数) / 6=周；type TOKENS_LIMIT（token
// 计量）/CREDIT_LIMIT（V3 起积分计量，字段同构）给 percentage（0~100），
// TIME_LIMIT 给工具月配额绝对次数。
type zhipuCard struct {
	Type          string           `json:"type"`
	Unit          int              `json:"unit"`
	Number        int              `json:"number"`
	Percentage    float64          `json:"percentage"`
	NextResetTime int64            `json:"nextResetTime"` // epoch 毫秒
	Usage         int64            `json:"usage"`
	CurrentValue  int64            `json:"currentValue"`
	Remaining     int64            `json:"remaining"`
	UsageDetails  []map[string]any `json:"usageDetails"`
}

// fetchZhipu 拉智谱 Coding Plan 额度：GET /api/monitor/usage/quota/limit。
func fetchZhipu(a store.AiAccount) (*store.AiUsageSnapshot, error) {
	var r zhipuResp
	body, err := getJSON(store.AiAccountBaseURL(&a), "/api/monitor/usage/quota/limit", a.APIKey, &r)
	if err != nil {
		return nil, err
	}
	// 业务错误（HTTP 200 但 code!=200 / success=false）：把服务端 msg 与请求
	// 地址带出来（URL 无密钥）——「404 NOT_FOUND」这类网关话术要靠地址定位填错
	if code, ok := zhipuCode(r.Code); (ok && code != 200) || (r.Success != nil && !*r.Success) {
		msg := r.Msg
		if msg == "" {
			msg = truncate(string(body), 160)
		}
		return nil, fmt.Errorf("智谱返回业务错误 %d: %s（请求地址 %s）", code, msg, store.AiAccountBaseURL(&a)+"/api/monitor/usage/quota/limit")
	}
	snap := &store.AiUsageSnapshot{Kind: "quota", Level: r.Data.Level}
	for _, c := range r.Data.Limits {
		switch c.Type {
		case "TOKENS_LIMIT", "CREDIT_LIMIT":
			w := store.AiQuotaWindow{UsedPct: c.Percentage, Credit: c.Type == "CREDIT_LIMIT"}
			if c.NextResetTime > 0 {
				w.ResetAt = c.NextResetTime / 1000
			}
			// V3 积分制上游可能不给 percentage：有总量与已用时按计数现算兜底
			if c.Percentage == 0 && c.Usage > 0 && c.CurrentValue > 0 {
				w.UsedPct = float64(c.CurrentValue) / float64(c.Usage) * 100
			}
			// 绝对计数三件套（部分套餐才有）：有总量且给了已用/剩余其一才带上，
			// 供前端展示「额度上限/已用/剩余」（TOKENS_LIMIT 为 tokens、
			// CREDIT_LIMIT 为积分）
			if c.Usage > 0 && (c.CurrentValue > 0 || c.Remaining > 0) {
				w.Used, w.Total, w.Remaining = &c.CurrentValue, &c.Usage, &c.Remaining
			}
			note := ""
			if c.Type == "CREDIT_LIMIT" {
				note = "（积分）"
			}
			switch c.Unit {
			case 3:
				w.Key, w.Label = "5h", fmt.Sprintf("%d 小时窗口%s", c.Number, note)
			case 6:
				w.Key, w.Label = "week", fmt.Sprintf("本周额度（7 天）%s", note)
			default:
				w.Key, w.Label = "other", fmt.Sprintf("窗口（unit=%d）%s", c.Unit, note)
			}
			snap.Windows = append(snap.Windows, w)
		case "TIME_LIMIT":
			if snap.Tools == nil { // 上游只有一份月配额，多份时取第一份
				t := store.AiToolQuota{Usage: c.Usage, Current: c.CurrentValue, Remaining: c.Remaining}
				if c.NextResetTime > 0 {
					t.ResetAt = c.NextResetTime / 1000
				}
				snap.Tools = &t
			}
		}
	}
	if len(snap.Windows) == 0 && snap.Tools == nil {
		// 200 且无业务错误但解析不到额度：多为 Key 无 Coding Plan 套餐（普通
		// API Key）或上游改了响应结构——带原文报错，重测即见真因
		return nil, fmt.Errorf("上游未返回额度数据（原始响应: %s）", truncate(string(body), 200))
	}
	// 周维度用量分析（401，报告弹窗）：以周窗口重置点倒推 7 天为账务期起点，
	// 可选分析端点 best-effort，失败静默不带
	for _, w := range snap.Windows {
		if w.Key == "week" && w.ResetAt > 0 {
			snap.WeekStart = w.ResetAt - 7*86400
			snap.WeekFrom, snap.WeekTo, snap.WeekDaily = fetchZhipuWeekUsage(a, snap.WeekStart)
			break
		}
	}
	return snap, nil
}

// zhipuUsageResp model-usage 响应（契约同 CodexBar zai.js）：x_time 时间桶 +
// modelDataList[].tokensUsage 对齐数组，逐桶求和即区间用量。callsUsage 调用
// 次数数组为可选（上游给才非空），与 x_time 对齐。
type zhipuUsageResp struct {
	Code    json.RawMessage `json:"code"`
	Success *bool           `json:"success"`
	Data    struct {
		XTime         []string `json:"x_time"`
		ModelDataList []struct {
			ModelName   string  `json:"modelName"`
			TokensUsage []int64 `json:"tokensUsage"`
			CallsUsage  []int64 `json:"callsUsage"`
		} `json:"modelDataList"`
	} `json:"data"`
}

// zhipuParseBucket 解析 model-usage 时间桶：上游可能带秒（"2006-01-02 15:04:05"）
// 或不带年（"01-02 15:04"），后者按基准年补齐（跨年周把落在年初的桶归到下一年）。
func zhipuParseBucket(s string, base time.Time) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			if layout == "01-02 15:04" {
				t = time.Date(base.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
				if t.Before(base.Add(-24 * time.Hour)) { // 无年桶落在基准前一年年底的跨年场景
					t = t.AddDate(1, 0, 0)
				}
			}
			return t, true
		}
	}
	return time.Time{}, false
}

// fetchZhipuWeekUsage 周账务期用量分析（401，报告弹窗数据）：GET
// /api/monitor/usage/model-usage，startTime=账务期起点（周重置点-7 天）、
// endTime=现在；x_time 逐桶按账务日（起点起的 24h 周期）归位聚桶，产出逐日
// tokens/调用次数/分模型明细与统计区间文案。属可选分析端点——任何失败（网络/
// 业务错误/结构变化/桶全不可解析）都静默返回零值，不影响额度主数据展示。
func fetchZhipuWeekUsage(a store.AiAccount, weekStart int64) (string, string, []store.AiWeekDay) {
	start := time.Unix(weekStart, 0)
	now := time.Now()
	stamp := func(t time.Time) string { return t.Format("2006-01-02 15:04:05") }
	q := url.Values{}
	q.Set("startTime", stamp(start))
	q.Set("endTime", stamp(now))
	path := "/api/monitor/usage/model-usage?" + q.Encode()
	var r zhipuUsageResp
	if _, err := getJSON(store.AiAccountBaseURL(&a), path, a.APIKey, &r); err != nil {
		return "", "", nil
	}
	if code, ok := zhipuCode(r.Code); (ok && code != 200) || (r.Success != nil && !*r.Success) {
		return "", "", nil
	}
	// 逐桶归账务日：dayIndex = (桶时刻 - 起点) / 24h；不可解析的桶跳过
	type dayAgg struct {
		tokens, calls int64
		models        map[string]int64
	}
	days := map[int]*dayAgg{}
	maxIdx := -1
	for bi, bt := range r.Data.XTime {
		t, ok := zhipuParseBucket(bt, start)
		if !ok {
			continue
		}
		idx := int(t.Sub(start).Hours() / 24)
		if idx < 0 || idx > 7 {
			continue
		}
		d := days[idx]
		if d == nil {
			d = &dayAgg{models: map[string]int64{}}
			days[idx] = d
		}
		for _, m := range r.Data.ModelDataList {
			if bi >= len(m.TokensUsage) {
				continue
			}
			if v := m.TokensUsage[bi]; v > 0 {
				d.tokens += v
				d.models[m.ModelName] += v
			}
			if bi < len(m.CallsUsage) && m.CallsUsage[bi] > 0 {
				d.calls += m.CallsUsage[bi]
			}
		}
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	if maxIdx < 0 {
		return "", "", nil
	}
	weekDay := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	rows := make([]store.AiWeekDay, 0, maxIdx+1)
	for i := 0; i <= maxIdx; i++ {
		day := start.AddDate(0, 0, i)
		row := store.AiWeekDay{Label: day.Format("01-02") + " " + weekDay[int(day.Weekday())]}
		if agg := days[i]; agg != nil {
			row.Tokens, row.Calls = agg.tokens, agg.calls
			for name, tk := range agg.models {
				if tk > 0 {
					row.Models = append(row.Models, store.AiModelUsage{Name: name, Tokens: tk})
				}
			}
			sort.Slice(row.Models, func(x, y int) bool { return row.Models[x].Tokens > row.Models[y].Tokens })
		}
		if i == maxIdx {
			dayEnd := start.AddDate(0, 0, i+1)
			// 末个账务日未走完（含恰好止于当前时刻的边界）→ 标 partial
			if dayEnd.After(now.Add(-time.Minute)) {
				row.Partial = true
			}
		}
		rows = append(rows, row)
	}
	from := start.Format("2006-01-02（" + weekDay[int(start.Weekday())] + "）15:04")
	to := now.Format("01-02 15:04")
	return from, to, rows
}

// ---- DeepSeek（预付余额）----

type deepseekResp struct {
	IsAvailable  bool          `json:"is_available"`
	BalanceInfos []deepseekBal `json:"balance_infos"`
}

type deepseekBal struct {
	Currency       string `json:"currency"`
	TotalBalance   string `json:"total_balance"`
	GrantedBalance string `json:"granted_balance"`
	ToppedUp       string `json:"topped_up_balance"`
}

// fetchDeepSeek 拉 DeepSeek 余额：GET /user/balance（官方无用量统计 API，
// token 消耗只能在控制台看，余额即该账号全部可展示用量口径）。
func fetchDeepSeek(a store.AiAccount) (*store.AiUsageSnapshot, error) {
	var r deepseekResp
	body, err := getJSON(store.AiAccountBaseURL(&a), "/user/balance", a.APIKey, &r)
	if err != nil {
		return nil, err
	}
	if len(r.BalanceInfos) == 0 {
		return nil, fmt.Errorf("上游未返回余额数据（原始响应: %s）", truncate(string(body), 200))
	}
	snap := &store.AiUsageSnapshot{Kind: "balance"}
	for _, b := range r.BalanceInfos {
		snap.Balances = append(snap.Balances, store.AiBalance{
			Currency: b.Currency, Total: b.TotalBalance, Granted: b.GrantedBalance,
			ToppedUp: b.ToppedUp, Available: r.IsAvailable,
		})
	}
	return snap, nil
}
