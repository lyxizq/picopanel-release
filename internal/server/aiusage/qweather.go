package aiusage

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

// 和风天气控制台 API（3，2026-10-23）：官方开放的两个账号级端点，与天气数据面
// 共用前台页面配置的专属 API Host 和 API Key（X-QW-Api-Key 头）。前置：账号
// 所有者须在和风控制台为该凭据勾选「控制台权限」（财务汇总/请求量汇总统计），
// 默认全拒（HTTP 403）。控制台数据延迟 ≥1 小时（以响应 asOf 为准），轮询跟
// 大部队 20 分钟一拍无信息增益但实现零特判；官方仅提供滚动 24 小时请求量，
// 无自然日/自然月口径（本月/昨日请求量不做，除非日后自建累计）。

const (
	qwFinancePath = "/finance/v1/summary"
	qwStatsPath   = "/metrics/v1/stats"
)

// fetchQWeather 拉和风用量：财务汇总 + 请求量统计并行（407 同款），各块
// best-effort 独立降级（失败只缺省对应块，经去重器记日志），双失败才报错。
func fetchQWeather(a store.AiAccount) (*store.AiUsageSnapshot, error) {
	if a.APIKey == "" {
		return nil, fmt.Errorf("缺少和风 API Key")
	}
	base := store.AiAccountBaseURL(&a)
	if base == "" {
		return nil, fmt.Errorf("缺少和风专属 API Host（请在前台页面天气配置里补齐）")
	}
	var (
		wg          sync.WaitGroup
		fin         *store.WxFinance
		st          *store.WxStats
		finErr, stE error
	)
	wg.Add(2)
	go func() { defer wg.Done(); fin, finErr = fetchWxFinance(base, a.APIKey) }()
	go func() { defer wg.Done(); st, stE = fetchWxStats(base, a.APIKey) }()
	wg.Wait()
	logDegraded(a.ID, "和风", "财务汇总", finErr)
	logDegraded(a.ID, "和风", "请求量", stE)
	if finErr != nil && stE != nil {
		return nil, fmt.Errorf("财务汇总与请求量统计均失败: %v；%v", finErr, stE)
	}
	return &store.AiUsageSnapshot{Kind: "qweather", Wx: &store.WxUsage{Finance: fin, Stats: st}}, nil
}

// qwFinanceResp /finance/v1/summary 响应（无请求参数）：metadata 含归因声明，
// 面板内部展示不透出。节省计划（availableSavingsPlans）暂不解析，资源包取请求量包。
type qwFinanceResp struct {
	AsOf     string   `json:"asOf"`
	Currency string   `json:"currency"`
	Balance  *float64 `json:"balance"`
	AccruedCharges struct {
		PreviousDay   *float64 `json:"previousDay"`
		ThisMonth     *float64 `json:"thisMonth"`
		SinceLastBill *float64 `json:"sinceLastBill"`
	} `json:"accruedCharges"`
	PendingBills []struct {
		Number    string  `json:"number"`
		Date      string  `json:"date"`
		Type      string  `json:"type"`
		Status    string  `json:"status"`
		Amount    float64 `json:"amount"`
		AmountDue float64 `json:"amountDue"`
		DueDate   string  `json:"dueDate"`
	} `json:"pendingBills"`
	AvailableResourcePlans []struct {
		BillNumber    string   `json:"billNumber"`
		Status        string   `json:"status"`
		Requests      *int64   `json:"requests"`
		Utilized      *int64   `json:"utilized"`
		Commitments   *float64 `json:"commitments"`
		EffectiveTime string   `json:"effectiveTime"`
	} `json:"availableResourcePlans"`
}

// fetchWxFinance 拉财务汇总。
func fetchWxFinance(base, key string) (*store.WxFinance, error) {
	var r qwFinanceResp
	if _, err := getJSONH(base, qwFinancePath, "X-QW-Api-Key", key, &r); err != nil {
		return nil, err
	}
	f := &store.WxFinance{
		AsOf: r.AsOf, Currency: r.Currency, Balance: r.Balance,
		PrevDay: r.AccruedCharges.PreviousDay, ThisMonth: r.AccruedCharges.ThisMonth, SinceBill: r.AccruedCharges.SinceLastBill,
	}
	for _, b := range r.PendingBills {
		f.PendingBills = append(f.PendingBills, store.WxBill{
			Number: b.Number, Date: b.Date, Type: b.Type, Status: b.Status,
			Amount: b.Amount, AmountDue: b.AmountDue, DueDate: b.DueDate,
		})
	}
	for _, p := range r.AvailableResourcePlans {
		f.ResourcePlans = append(f.ResourcePlans, store.WxPlan{
			Status: p.Status, Requests: p.Requests, Utilized: p.Utilized,
			Commitments: p.Commitments, EffectiveTime: p.EffectiveTime,
		})
	}
	return f, nil
}

// qwStatsResp /metrics/v1/stats 响应：success/errors 两组，各按 API 分组挂
// 最近 24 小时的逐小时计数（hours 最后一桶=截至 asOf 的那个小时）。
type qwStatsResp struct {
	AsOf    string `json:"asOf"`
	Success []qwAPIHours `json:"success"`
	Errors  []qwAPIHours `json:"errors"`
}

type qwAPIHours struct {
	API   string  `json:"api"`
	Hours []int64 `json:"hours"`
}

// fetchWxStats 拉请求量统计并聚合：PerAPI 按 API 合计（总量降序），Hours 跨
// API 合并成逐小时曲线，标签按 asOf 所在时区自最后一桶向前推（asOf 不可解析
// 时退化为序号）。空数组是合法态（新账号/无流量），落全 0 不报错。
func fetchWxStats(base, key string) (*store.WxStats, error) {
	var r qwStatsResp
	if _, err := getJSONH(base, qwStatsPath, "X-QW-Api-Key", key, &r); err != nil {
		return nil, err
	}
	// 成功/失败两组分开累计（同一 API 出现在两组、逐桶求和各自归位）
	per := map[string]*store.WxAPIStat{}
	var hourSuccess, hourErrors []int64
	for _, it := range r.Success {
		p := per[it.API]
		if p == nil {
			p = &store.WxAPIStat{API: it.API}
			per[it.API] = p
		}
		for i, v := range it.Hours {
			for len(hourSuccess) <= i {
				hourSuccess = append(hourSuccess, 0)
			}
			hourSuccess[i] += v
			p.Success += v
		}
	}
	for _, it := range r.Errors {
		p := per[it.API]
		if p == nil {
			p = &store.WxAPIStat{API: it.API}
			per[it.API] = p
		}
		for i, v := range it.Hours {
			for len(hourErrors) <= i {
				hourErrors = append(hourErrors, 0)
			}
			hourErrors[i] += v
			p.Errors += v
		}
	}
	s := &store.WxStats{AsOf: r.AsOf}
	for _, p := range per {
		s.Success += p.Success
		s.Errors += p.Errors
		s.PerAPI = append(s.PerAPI, *p)
	}
	sort.Slice(s.PerAPI, func(i, j int) bool {
		ti, tj := s.PerAPI[i].Success+s.PerAPI[i].Errors, s.PerAPI[j].Success+s.PerAPI[j].Errors
		if ti != tj {
			return ti > tj
		}
		return s.PerAPI[i].API < s.PerAPI[j].API
	})
	// 逐小时合计曲线：标签自 asOf 桶向前推（桶数取成功/失败数组的较大者）
	n := len(hourSuccess)
	if len(hourErrors) > n {
		n = len(hourErrors)
	}
	if n > 0 {
		base0 := qwParseTime(r.AsOf)
		for i := 0; i < n; i++ {
			h := store.WxHour{}
			if i < len(hourSuccess) {
				h.Success = hourSuccess[i]
			}
			if i < len(hourErrors) {
				h.Errors = hourErrors[i]
			}
			if !base0.IsZero() {
				bt := base0.Add(time.Duration(i-(n-1)) * time.Hour)
				h.Ts = bt.Unix()
				h.Label = bt.Format("15:04")
			} else {
				h.Label = fmt.Sprintf("#%d", i)
			}
			s.Hours = append(s.Hours, h)
		}
	}
	return s, nil
}

// qwTimeLayouts asOf 的实测形态：文档示例是无秒的 2025-05-12T02:59Z，首版只按
// RFC3339（必须带秒）解析失败退化成 #序号 标签——多格式逐一试。
var qwTimeLayouts = []string{
	time.RFC3339,             // 2026-10-23T09:59:00+08:00
	"2006-01-02T15:04Z07:00", // 2026-10-23T09:59Z / +08:00（无秒，文档示例形态）
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
}

// qwParseTime 解析 asOf 并截断到整点作小时桶基准（标签/账本桶起点都取它）。
func qwParseTime(s string) time.Time {
	for _, layout := range qwTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
		}
	}
	return time.Time{}
}
