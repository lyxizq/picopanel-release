package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// AI 用量查询（356）：服务商预设 + 账户配置 + 最新用量快照。API Key AES-GCM
// 加密存库（secret.go，同 mail/integrations），store 层出入明文（内部消费），
// API 层不得原样外发；用量仅存最新一份覆盖式快照（2026-10-03 用户指定）。

// AiProviderPreset 服务商预设：BaseURL 为用量接口的默认站点（可被账户
// base_url 覆盖，代理/镜像站场景），接口路径由 provider 类型决定。
type AiProviderPreset struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	BaseURL string `json:"base_url"`
}

// AiProviderPresets 支持的服务商（智谱 + DeepSeek + 腾讯 EdgeOne + 中国电信）。
func AiProviderPresets() []AiProviderPreset {
	return []AiProviderPreset{
		{Key: "zhipu", Label: "智谱 GLM", BaseURL: "https://open.bigmodel.cn"},
		{Key: "deepseek", Label: "DeepSeek", BaseURL: "https://api.deepseek.com"},
		// EdgeOne 用量走腾讯云 API 3.0（teo），截图对齐的是国际站控制台
		// （站点数据概览），防护命中接口仅国际站端点提供——预设国际站，
		// 国内站账户把 base_url 改成 teo.tencentcloudapi.com
		{Key: "edgeone", Label: "腾讯 EdgeOne", BaseURL: "https://teo.intl.tencentcloudapi.com"},
	}
}

// AiProviderByKey 预设键 → 预设（不在白名单返回 nil）。
func AiProviderByKey(key string) *AiProviderPreset {
	for _, p := range AiProviderPresets() {
		if p.Key == key {
			return &p
		}
	}
	return nil
}

// MaxAIAccounts AI 账户上限。
const MaxAIAccounts = 8

// AiAccount 对应 ai_accounts 一行。zhipu/deepseek 用 APIKey；edgeone 用
// SecretID/SecretKey（腾讯云 API 密钥对）+ ZoneIDs 站点过滤。
type AiAccount struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"` // zhipu / deepseek / edgeone
	BaseURL   string `json:"base_url"` // 空 = 预设默认站点
	APIKey    string `json:"-"`
	SecretID  string `json:"-"`        // EdgeOne 腾讯云 SecretId（加密存库）
	SecretKey string `json:"-"`        // EdgeOne 腾讯云 SecretKey（加密存库）
	ZoneIDs   string `json:"zone_ids"` // EdgeOne 站点 ID 逗号分隔（空=全部）
	Sort      int64  `json:"sort"`
	UsageJSON string `json:"-"` // 最新快照 JSON（内部，API 解析后下发）
	UsageAt   int64  `json:"usage_at"`
	LastOKAt  int64  `json:"last_ok_at"`
	LastError string `json:"last_error"`
	HasKey    bool   `json:"has_key"` // 查询时由 store 填充（凭据齐备与否）
}

// AiQuotaWindow 智谱 TOKENS_LIMIT/CREDIT_LIMIT 窗口（5 小时/7 天；后者 V3 起
// 积分计量，计数为积分数非 tokens）：ResetAt=秒级时间戳
// （0=上游没给）。Used/Total/Remaining 为绝对计数，部分套餐的上游
// 才给（2026-10-04 用户要求展示周额度上限），三个都有才下发展示。
type AiQuotaWindow struct {
	Key       string  `json:"key"` // 5h / week / other
	Label     string  `json:"label"`
	UsedPct   float64 `json:"used_pct"`
	ResetAt   int64   `json:"reset_at"`
	Credit    bool    `json:"credit,omitempty"` // CREDIT_LIMIT 积分计量窗口（V3），计数为积分数非 tokens
	Used      *int64  `json:"used,omitempty"`
	Total     *int64  `json:"total,omitempty"`
	Remaining *int64  `json:"remaining,omitempty"`
}

// AiToolQuota 智谱 TIME_LIMIT（MCP 等工具月配额）：绝对次数；ResetAt=秒级
// 重置时间（0=上游没给，401 弹窗「其他配额」展示）。
type AiToolQuota struct {
	Usage     int64 `json:"usage"`              // 总量
	Current   int64 `json:"current"`            // 已用
	Remaining int64 `json:"remaining"`          // 剩余
	ResetAt   int64 `json:"reset_at,omitempty"` // 重置时间（秒级时间戳）
}

// AiBalance DeepSeek 余额（金额为上游字符串原样，避免浮点失真）。
type AiBalance struct {
	Currency  string `json:"currency"`
	Total     string `json:"total"`
	Granted   string `json:"granted"`
	ToppedUp  string `json:"topped_up"`
	Available bool   `json:"available"`
}

// AiModelUsage 智谱分模型 tokens 用量（384 建，401 起供周维度按天分布复用）。
type AiModelUsage struct {
	Name   string `json:"name"`
	Tokens int64  `json:"tokens"`
}

// AiWeekDay 智谱周用量按账务日分布一行（401，报告弹窗「按天分布」表）：
// 账务日=周重置时刻起的 24 小时周期（如每周一 15:00 → 次日 15:00），末行未满
// 一整天 Partial=true。Tokens=当日合计；Models=当日分模型明细（用量降序）；
// Calls=调用次数（上游分析端点给才非 0，全 0 时前端隐藏调用列）。
type AiWeekDay struct {
	Label   string         `json:"label"`            // "09-28 周一"
	Tokens  int64          `json:"tokens"`           // 当日合计 tokens
	Calls   int64          `json:"calls,omitempty"`  // 当日调用次数（可选）
	Models  []AiModelUsage `json:"models,omitempty"` // 当日分模型（用量降序）
	Partial bool           `json:"partial,omitempty"`
}

// AiUsageSnapshot 最新用量快照（usage_json 列的序列化形态）。
type AiUsageSnapshot struct {
	Kind      string          `json:"kind"` // quota=百分比窗口 / balance=预付余额 / edgeone=站点用量 / qweather=和风控制台用量
	Level     string          `json:"level,omitempty"`
	Windows   []AiQuotaWindow `json:"windows,omitempty"`
	Tools     *AiToolQuota    `json:"tools,omitempty"`
	Balances  []AiBalance     `json:"balances,omitempty"`
	EdgeOne   *EdgeOneStats   `json:"edgeone,omitempty"`
	Wx        *WxUsage        `json:"wx,omitempty"`
	WeekStart int64           `json:"week_start,omitempty"` // 周账务期起点（秒级，401 报告弹窗节奏参考用）
	WeekFrom  string          `json:"week_from,omitempty"`  // 统计区间文案 "2026-09-28（周一）15:00"
	WeekTo    string          `json:"week_to,omitempty"`    // 统计区间终点 "10-02 20:28"
	WeekDaily []AiWeekDay     `json:"week_daily,omitempty"` // 按账务日分布（401，可选分析端点给才带）
}

// EdgeOneTopItem EdgeOne 弹层 TopN 条目（398，Key=域名/URL/状态码，Count=请求数）。
type EdgeOneTopItem struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// EdgeOneDetail EdgeOne 详情弹层数据（398/410，近 24 小时）：各块独立 best-effort
// 拉取，任一失败对应字段保持 nil（前端显示 '—'），不影响主指标与其余块。
// TopN 条目 Count 的量纲由指标决定：请求数类=次，流量类（Flux*）=Byte。
type EdgeOneDetail struct {
	CacheHitFlux *int64           `json:"cache_hit_flux,omitempty"` // 缓存命中流量 Byte（intl 专属指标）
	OutFlux      *int64           `json:"out_flux,omitempty"`       // 响应流量总值 Byte（410，排行百分比分母）
	OriginReq    *int64           `json:"origin_req,omitempty"`     // 回源请求数
	OriginFlux   *int64           `json:"origin_flux,omitempty"`    // 回源流量（节点→源站）Byte
	StatusCodes  []EdgeOneTopItem `json:"status_codes,omitempty"`   // 请求数按状态码分布
	TopDomains   []EdgeOneTopItem `json:"top_domains,omitempty"`    // Top 域名（按请求数，前 10）
	TopURLs      []EdgeOneTopItem `json:"top_urls,omitempty"`       // Top URL Path（按请求数，前 10）
	FluxHosts    []EdgeOneTopItem `json:"flux_hosts,omitempty"`     // Host 排行（按响应流量，410）
	FluxIPs      []EdgeOneTopItem `json:"flux_ips,omitempty"`       // 客户端 IP 排行（按响应流量，410）
	FluxReferers []EdgeOneTopItem `json:"flux_referers,omitempty"`  // Referer 排行（按响应流量，410）
	FluxURLs     []EdgeOneTopItem `json:"flux_urls,omitempty"`      // URL Path 排行（按响应流量，410）
	DDoSPeakBps  *int64           `json:"ddos_peak_bps,omitempty"`  // 攻击带宽峰值 bps
	DDoSPeakPps  *int64           `json:"ddos_peak_pps,omitempty"`  // 攻击包速率峰值 pps
}

// EdgeOneStats EdgeOne 站点近 24 小时用量（393，对齐控制台「站点数据概览」四指标）。
// Prev* 为前一个 24 小时窗口同口径值（环比由前端计算）；ProtectHits=nil 表示
// 上游不支持/查询失败（国内站端点暂无防护数据接口），前端显示 '-'。
// 带宽峰值为 5 分钟粒度时序的 Max（bps），多站点为各站点加总/取大。
type EdgeOneStats struct {
	ZoneCount     int            `json:"zone_count"`
	TrafficBytes  int64          `json:"traffic_bytes"`
	PeakBandwidth int64          `json:"peak_bandwidth"` // bps
	Requests      int64          `json:"requests"`
	ProtectHits   *int64         `json:"protect_hits,omitempty"`
	PrevTraffic   *int64         `json:"prev_traffic,omitempty"`
	PrevPeak      *int64         `json:"prev_peak,omitempty"`
	PrevRequests  *int64         `json:"prev_requests,omitempty"`
	PrevHits      *int64         `json:"prev_hits,omitempty"`
	Detail        *EdgeOneDetail `json:"detail,omitempty"` // 弹层详情（398，全块失败则缺省）
}

// WxUsage 和风天气控制台 API 用量快照（3，kind=qweather）：财务汇总与请求量
// 统计两个端点独立 best-effort 拉取，任一失败对应字段保持 nil（前端显示 '—'），
// 双失败才整体报错。控制台数据延迟约 1 小时，以各块 asOf 为准。Month* 为当月
// 请求量（自然月，官方无此口径）：轮询时把完整小时桶幂等落 wx_usage_hours
// 账本后求和回填，账本当月无桶（刚启用/跨月初）保持 nil 不显示。
type WxUsage struct {
	Finance *WxFinance `json:"finance,omitempty"` // /finance/v1/summary（需凭据勾选「允许访问财务汇总数据」）
	Stats   *WxStats   `json:"stats,omitempty"`   // /metrics/v1/stats（需勾选「允许访问请求量汇总统计」）

	MonthSuccess *int64 `json:"month_success,omitempty"`
	MonthErrors  *int64 `json:"month_errors,omitempty"`

	// DayAvg* 近 30 天日均请求量（含今天共 30 个自然日，账本求和 ÷30）：滚动
	// 口径而非当月日均——当月日均×当月天数恒等于月总量，与月总量对同一资源包
	// 分母画两根进度条会完全重合；且月初不跳变。窗口内无桶保持 nil 不显示。
	DayAvgSuccess *int64 `json:"day_avg_success,omitempty"`
	DayAvgErrors  *int64 `json:"day_avg_errors,omitempty"`
}

// WxFinance 财务汇总：金额单位随 Currency（CNY/USD），余额可为负（按量计费
// 欠费），三个指针字段缺席=上游没给/拉取降级。
type WxFinance struct {
	AsOf      string   `json:"as_of,omitempty"`
	Currency  string   `json:"currency,omitempty"`
	Balance   *float64 `json:"balance,omitempty"`    // 可用额度
	PrevDay   *float64 `json:"prev_day,omitempty"`   // 前一日应计费用
	ThisMonth *float64 `json:"this_month,omitempty"` // 本月应计费用
	SinceBill *float64 `json:"since_bill,omitempty"` // 自上次出账应计费用
	PendingBills   []WxBill `json:"pending_bills,omitempty"`   // 待支付账单（unpaid/overdue）
	ResourcePlans  []WxPlan `json:"resource_plans,omitempty"`  // 可用资源包（请求量包）
}

// WxBill 待支付账单一行。
type WxBill struct {
	Number    string  `json:"number,omitempty"`
	Date      string  `json:"date,omitempty"`
	Type      string  `json:"type,omitempty"` // PayAsYouGo / SavingsPlans / …
	Status    string  `json:"status,omitempty"`
	Amount    float64 `json:"amount"`
	AmountDue float64 `json:"amount_due"`
	DueDate   string  `json:"due_date,omitempty"`
}

// WxPlan 资源包一行（节省计划 savingsPlans 暂不解析——面板场景请求量包才有用）。
type WxPlan struct {
	Status        string   `json:"status,omitempty"` // pending / active
	Requests      *int64   `json:"requests,omitempty"`
	Utilized      *int64   `json:"utilized,omitempty"`
	Commitments   *float64 `json:"commitments,omitempty"`
	EffectiveTime string   `json:"effective_time,omitempty"`
}

// WxStats 请求量统计（仅最近 24 小时逐小时，官方无自然日/自然月口径）：PerAPI
// 按 API 分组合计，Hours 为跨 API 合计的逐小时曲线（标签取 asOf 所在时区的
// 小时桶，最后一桶=截至 asOf 的那个小时）。
type WxStats struct {
	AsOf    string      `json:"as_of,omitempty"`
	Success int64       `json:"success"`
	Errors  int64       `json:"errors"`
	PerAPI  []WxAPIStat `json:"per_api,omitempty"`
	Hours   []WxHour    `json:"hours,omitempty"`
}

// WxAPIStat 单个 API 的 24 小时成功/失败合计（Total=Success+Errors，排序用）。
type WxAPIStat struct {
	API     string `json:"api"`
	Success int64  `json:"success"`
	Errors  int64  `json:"errors"`
}

// WxHour 逐小时合计一点（Ts=桶起始秒级时间戳，0=asOf 不可解析的退化序号桶，
// 仅展示用、不入账本）。
type WxHour struct {
	Label   string `json:"label"`
	Ts      int64  `json:"ts,omitempty"`
	Success int64  `json:"success"`
	Errors  int64  `json:"errors"`
}

// ErrAiAccountNotFound 账户不存在。
var ErrAiAccountNotFound = errors.New("用量账户不存在")

const aiAccountCols = `id, name, provider, base_url, api_key, sort, usage_json, usage_at, last_ok_at, last_error, secret_id, secret_key, zone_ids`

// scanAiAccount 扫描账户行。decrypt=false 为轻量模式：APIKey/SecretID/SecretKey
// 保持库中密文、HasKey 由密文非空判定（seal 空串原样返回，密文非空 ⇔ 已配置）
// 后置空凭据字段——列表/调度到期判断路径零解密，也不会把密文带出去。
func (s *Store) scanAiAccount(row interface{ Scan(...any) error }, decrypt bool) (*AiAccount, error) {
	var a AiAccount
	err := row.Scan(&a.ID, &a.Name, &a.Provider, &a.BaseURL, &a.APIKey, &a.Sort,
		&a.UsageJSON, &a.UsageAt, &a.LastOKAt, &a.LastError, &a.SecretID, &a.SecretKey, &a.ZoneIDs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAiAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	a.HasKey = a.APIKey != "" || (a.SecretID != "" && a.SecretKey != "")
	if decrypt {
		a.APIKey = s.unseal(a.APIKey)
		a.SecretID = s.unseal(a.SecretID)
		a.SecretKey = s.unseal(a.SecretKey)
	} else {
		a.APIKey, a.SecretID, a.SecretKey = "", "", ""
	}
	return &a, nil
}

// AiAccountBaseURL 账户实际站点（base_url 优先，空回落预设默认）。常见误填
// 自动纠偏：聊天/兼容层前缀下没有余额/额度端点，剥掉后缀——deepseek 的 /v1
// 与 /anthropic（Claude Code 兼容层）、zhipu 的 /api/paas/v4 与 /api/anthropic
// （2026-10-04 用户实测：把 deepseek 的 /anthropic 地址填进智谱账户 → 404）。
func AiAccountBaseURL(a *AiAccount) string {
	u := strings.TrimSpace(a.BaseURL)
	if u == "" {
		if p := AiProviderByKey(a.Provider); p != nil {
			u = p.BaseURL
		}
	}
	u = strings.TrimRight(u, "/")
	switch a.Provider {
	case "deepseek":
		for _, suffix := range []string{"/v1", "/anthropic"} {
			u = strings.TrimSuffix(u, suffix)
		}
	case "zhipu":
		for _, suffix := range []string{"/api/paas/v4", "/api/anthropic"} {
			u = strings.TrimSuffix(u, suffix)
		}
	}
	if u == "" || (!strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://")) {
		if p := AiProviderByKey(a.Provider); p != nil {
			return p.BaseURL
		}
	}
	return u
}

// ListAiAccounts 全部账户（含解密后的凭据明文，上游拉取用），按手动排序。
func (s *Store) ListAiAccounts() ([]AiAccount, error) {
	return s.listAiAccounts(true)
}

// ListAiAccountMetas 全部账户的展示/调度元信息（不解密：凭据字段恒为空、
// HasKey 照常），账户清单/轮询到期判断用。
func (s *Store) ListAiAccountMetas() ([]AiAccount, error) {
	return s.listAiAccounts(false)
}

func (s *Store) listAiAccounts(decrypt bool) ([]AiAccount, error) {
	rows, err := s.db.Query(`SELECT ` + aiAccountCols + ` FROM ai_accounts ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AiAccount
	for rows.Next() {
		a, err := s.scanAiAccount(rows, decrypt)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// CountAiAccounts 当前账户数（创建上限校验用，替代全量清单解密后计数）。
// 和风系统账户（QWeatherProvider）由天气配置自动维护，不占手动账户名额。
func (s *Store) CountAiAccounts() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM ai_accounts WHERE provider != ?`, QWeatherProvider).Scan(&n)
	return n, err
}

// GetAiAccount 单账户（含解密后的凭据明文）。
func (s *Store) GetAiAccount(id int64) (*AiAccount, error) {
	return s.scanAiAccount(s.db.QueryRow(`SELECT `+aiAccountCols+` FROM ai_accounts WHERE id = ?`, id), true)
}

// CreateAiAccount 新建（Key/腾讯云密钥对明文入参、库内加密）。
func (s *Store) CreateAiAccount(a *AiAccount) (int64, error) {
	a.Name, a.BaseURL, a.ZoneIDs = strings.TrimSpace(a.Name), strings.TrimSpace(a.BaseURL), strings.TrimSpace(a.ZoneIDs)
	res, err := s.db.Exec(`INSERT INTO ai_accounts (name, provider, base_url, api_key, secret_id, secret_key, zone_ids, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(sort),0)+1 FROM ai_accounts), ?, ?)`,
		a.Name, a.Provider, a.BaseURL, s.seal(a.APIKey), s.seal(a.SecretID), s.seal(a.SecretKey), a.ZoneIDs, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateAiAccount 更新（APIKey/SecretKey/SecretId 空串=各自沿用原值）。
func (s *Store) UpdateAiAccount(a *AiAccount) error {
	a.Name, a.BaseURL, a.ZoneIDs = strings.TrimSpace(a.Name), strings.TrimSpace(a.BaseURL), strings.TrimSpace(a.ZoneIDs)
	old, err := s.GetAiAccount(a.ID)
	if err != nil {
		return err
	}
	if a.APIKey == "" {
		a.APIKey = old.APIKey
	}
	if a.SecretID == "" {
		a.SecretID = old.SecretID
	}
	if a.SecretKey == "" {
		a.SecretKey = old.SecretKey
	}
	_, err = s.db.Exec(`UPDATE ai_accounts SET name=?, provider=?, base_url=?, api_key=?, secret_id=?, secret_key=?, zone_ids=?, updated_at=? WHERE id=?`,
		a.Name, a.Provider, a.BaseURL, s.seal(a.APIKey), s.seal(a.SecretID), s.seal(a.SecretKey), a.ZoneIDs, time.Now().Unix(), a.ID)
	return err
}

// DeleteAiAccount 删除账户（快照随行删除；数据监控里引用该账户的格子连带
// 清理——486 用量格子数据源，口径同删服务器级联清理 dashboard_metrics）。
func (s *Store) DeleteAiAccount(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM dashboard_metrics WHERE ai_id = ?`,
		`DELETE FROM page_metrics WHERE ai_id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`DELETE FROM ai_accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAiAccountNotFound
	}
	return tx.Commit()
}

// RecordAiAccountStatus 记录拉取健康态：成功才推进 last_ok_at 并清错误（内容
// 变化才写，省无谓写库）；失败只写最近错误，不碰 last_ok_at（它专指最近成功，
// 503 修复：原失败路径也推进 last_ok_at 的休眠 bug，同 RecordPtSiteStatus 口径）。
func (s *Store) RecordAiAccountStatus(id int64, ok bool, errMsg string) error {
	now := time.Now().Unix()
	if ok {
		_, err := s.db.Exec(`UPDATE ai_accounts SET last_ok_at = ?, last_error = '' WHERE id = ? AND (last_ok_at != ? OR last_error != '')`, now, id, now)
		return err
	}
	_, err := s.db.Exec(`UPDATE ai_accounts SET last_error = ? WHERE id = ? AND last_error != ?`, errMsg, id, errMsg)
	return err
}

// SetAiAccountUsage 覆盖式写入最新快照（仅显示最新数据，每次整份替换）。
func (s *Store) SetAiAccountUsage(id int64, snap *AiUsageSnapshot) error {
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE ai_accounts SET usage_json = ?, usage_at = ? WHERE id = ?`, string(b), time.Now().Unix(), id)
	return err
}

// QWeatherProvider 和风天气系统账户的 provider 标识：该行由前台页面天气配置
// 自动维护（3），不在服务商预设白名单里（API 层手动创建/编辑天然被拒）、不占
// 手动账户名额，仅删除端点单独设防。
const QWeatherProvider = "qweather"

// SyncQWeatherSystemAccount 按前台页面天气配置维护和风系统账户行：服务商=和风
// 且 Key/专属 Host 齐备时 upsert（Key 重加密、base_url 随 Host 更新；行内
// usage_json/健康态/排序原样保留），配置移除时删行（快照与磁贴引用随行级联
// 清理，口径同 DeleteAiAccount）。启动与保存页面设置两处调用，幂等。
func (s *Store) SyncQWeatherSystemAccount(ps *PageSettings) error {
	key, host := "", ""
	if ps != nil && ps.WeatherProvider == QWeatherProvider {
		key, host = strings.TrimSpace(ps.WeatherKey), strings.TrimSpace(ps.WeatherHost)
	}
	var id int64
	err := s.db.QueryRow(`SELECT id FROM ai_accounts WHERE provider = ?`, QWeatherProvider).Scan(&id)
	has := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if key == "" || host == "" {
		if has {
			return s.DeleteAiAccount(id)
		}
		return nil
	}
	if has {
		_, err = s.db.Exec(`UPDATE ai_accounts SET name=?, base_url=?, api_key=?, updated_at=? WHERE id=?`,
			"和风天气", "https://"+host, s.seal(key), time.Now().Unix(), id)
		return err
	}
	_, err = s.db.Exec(`INSERT INTO ai_accounts (name, provider, base_url, api_key, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(sort),0)+1 FROM ai_accounts), ?, ?)`,
		"和风天气", QWeatherProvider, "https://"+host, s.seal(key), time.Now().Unix(), time.Now().Unix())
	return err
}

// RecordWxHours 把完整小时桶幂等落账本（3 的当月请求量口径）：小时桶一旦完整
// 上游值即稳定，INSERT OR IGNORE 首见写入——20 分钟一拍的重复轮询与手动刷新
// 都不重复计数；调用方负责只传完整桶（桶起点 < asOf 桶）。顺带清 400 天前旧桶
// （10，2026-10-24：留存覆盖近 12 个完整历史月+当月供历史月份视图；24 行/天
// 上限约 9.6k 行/年，体积无压力；首版 40 天只够跨月）。
func (s *Store) RecordWxHours(hours []WxHour) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, h := range hours {
		if h.Ts <= 0 {
			continue
		}
		key := time.Unix(h.Ts, 0).Format("2006-01-02T15")
		if _, err := tx.Exec(`INSERT OR IGNORE INTO wx_usage_hours (hour_key, success, errors) VALUES (?, ?, ?)`,
			key, h.Success, h.Errors); err != nil {
			return err
		}
	}
	cutoff := time.Now().AddDate(0, 0, -400).Format("2006-01-02T15")
	if _, err := tx.Exec(`DELETE FROM wx_usage_hours WHERE hour_key < ?`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

// WxMonthUsage 当月请求量合计（本地自然月）：当月无桶返回 ok=false（账本刚
// 启用/跨月初，前端不显示，与真实的 0 区分）。
func (s *Store) WxMonthUsage() (success, errors int64, ok bool, err error) {
	prefix := time.Now().Format("2006-01") + "%"
	var n int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM wx_usage_hours WHERE hour_key LIKE ?`, prefix).Scan(&n); err != nil {
		return 0, 0, false, err
	}
	if n == 0 {
		return 0, 0, false, nil
	}
	if err = s.db.QueryRow(`SELECT COALESCE(SUM(success),0), COALESCE(SUM(errors),0) FROM wx_usage_hours WHERE hour_key LIKE ?`, prefix).Scan(&success, &errors); err != nil {
		return 0, 0, false, err
	}
	return success, errors, true, nil
}

// WxDailyAvg 近 30 天日均请求量（本地时区，窗口=含今天共 30 个自然日，账本
// hour_key >= 30 天前当天 0 点求和 ÷30）：整除即可，日均不需要小数次。窗口内
// 无桶返回 ok=false（账本刚启用，前端不显示，与真实的 0 区分）。
func (s *Store) WxDailyAvg() (avgSuccess, avgErrors int64, ok bool, err error) {
	from := time.Now().AddDate(0, 0, -29).Format("2006-01-02") + "T00"
	var n int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM wx_usage_hours WHERE hour_key >= ?`, from).Scan(&n); err != nil {
		return
	}
	if n == 0 {
		return
	}
	var sumS, sumE int64
	if err = s.db.QueryRow(`SELECT COALESCE(SUM(success),0), COALESCE(SUM(errors),0) FROM wx_usage_hours WHERE hour_key >= ?`, from).Scan(&sumS, &sumE); err != nil {
		return
	}
	return sumS / 30, sumE / 30, true, nil
}

// WxMonthStat 历史月份一行（10）：Month=本地时区自然月 'YYYY-MM'，当月也在列
// （进行中，只含已入账的完整小时桶）。
type WxMonthStat struct {
	Month   string `json:"month"`
	Success int64  `json:"success"`
	Errors  int64  `json:"errors"`
}

// WxMonthHistory 按自然月汇总请求量账本（10，2026-10-24）：新→旧最多 limit 个
// 月（默认 13=近 12 个完整历史月+当月）。账本自启用起积累，启用前的月份天然
// 无行，前端据返回条数自行提示。
func (s *Store) WxMonthHistory(limit int) ([]WxMonthStat, error) {
	if limit < 1 || limit > 36 {
		limit = 13
	}
	rows, err := s.db.Query(`SELECT substr(hour_key,1,7) AS m, COALESCE(SUM(success),0), COALESCE(SUM(errors),0)
		FROM wx_usage_hours GROUP BY m ORDER BY m DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WxMonthStat
	for rows.Next() {
		var it WxMonthStat
		if err := rows.Scan(&it.Month, &it.Success, &it.Errors); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
