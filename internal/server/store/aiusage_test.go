package store

import (
	"strings"
	"testing"
	"time"
)

// TestAiAccountCRUD AI 账户全链路：建（Key 加密落库）→读（解密还原明文）→
// 改（Key 留空沿用）→快照覆盖写→排序→删。
func TestAiAccountCRUD(t *testing.T) {
	s := openTestStore(t)

	id, err := s.CreateAiAccount(&AiAccount{
		Name: "智谱主力", Provider: "zhipu", BaseURL: "https://mirror.example.com", APIKey: "zk-abc123",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 库内必须密文
	var dbKey string
	if err := s.db.QueryRow(`SELECT api_key FROM ai_accounts WHERE id = ?`, id).Scan(&dbKey); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dbKey, "enc:v1:") || dbKey == "zk-abc123" {
		t.Fatalf("API Key 应加密落库，实际: %q", dbKey)
	}

	// 读回必须解密成原文
	a, err := s.GetAiAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	if a.APIKey != "zk-abc123" || !a.HasKey {
		t.Fatalf("读回应为明文 Key，实际: %q has_key=%v", a.APIKey, a.HasKey)
	}
	if a.Name != "智谱主力" || a.BaseURL != "https://mirror.example.com" {
		t.Fatalf("字段回读不符: %+v", a)
	}

	// 编辑：Key 留空沿用原名；base_url 清空回落预设站点
	a2 := &AiAccount{ID: id, Name: "改名", Provider: "zhipu", APIKey: ""}
	if err := s.UpdateAiAccount(a2); err != nil {
		t.Fatal(err)
	}
	a3, err := s.GetAiAccount(id)
	if err != nil {
		t.Fatal(err)
	}
	if a3.APIKey != "zk-abc123" {
		t.Fatalf("Key 留空应沿用原值，实际: %q", a3.APIKey)
	}
	if got := AiAccountBaseURL(a3); got != "https://open.bigmodel.cn" {
		t.Fatalf("base_url 清空应回落预设站点，实际: %q", got)
	}

	// 快照覆盖写：后写整份替换前写（仅最新一份）
	if err := s.SetAiAccountUsage(id, &AiUsageSnapshot{Kind: "quota", Level: "max",
		Windows: []AiQuotaWindow{{Key: "week", Label: "本周额度（7 天）", UsedPct: 27.5, ResetAt: 1760000000}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAiAccountUsage(id, &AiUsageSnapshot{Kind: "balance",
		Balances: []AiBalance{{Currency: "CNY", Total: "110.00", Available: true}}}); err != nil {
		t.Fatal(err)
	}
	a4, _ := s.GetAiAccount(id)
	if !strings.Contains(a4.UsageJSON, `"balance"`) || strings.Contains(a4.UsageJSON, "week") {
		t.Fatalf("快照应整份覆盖为最新，实际: %s", a4.UsageJSON)
	}
	if a4.UsageAt == 0 {
		t.Fatal("usage_at 应随快照更新")
	}

	// 健康态：失败写错误、成功清错误
	if err := s.RecordAiAccountStatus(id, false, "上游返回 401"); err != nil {
		t.Fatal(err)
	}
	a5, _ := s.GetAiAccount(id)
	if a5.LastError != "上游返回 401" {
		t.Fatalf("失败应记录错误，实际: %q", a5.LastError)
	}
	_ = s.RecordAiAccountStatus(id, true, "")
	a6, _ := s.GetAiAccount(id)
	if a6.LastError != "" || a6.LastOKAt == 0 {
		t.Fatalf("成功应清错误并记时间，实际: %q %d", a6.LastError, a6.LastOKAt)
	}

	// 删除
	list, err := s.ListAiAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("删除前应只剩 1 个账户: %+v", list)
	}
	if err := s.DeleteAiAccount(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAiAccount(id); err != ErrAiAccountNotFound {
		t.Fatalf("删后应报不存在，实际: %v", err)
	}
}

// TestAiAccountBaseURL 站点归一（2026-10-04 404 案例）：聊天补全前缀误填自动
// 剥除（deepseek /v1、zhipu /api/paas/v4），空/非法回落预设。
func TestAiAccountBaseURL(t *testing.T) {
	cases := []struct {
		provider, base, want string
	}{
		{"deepseek", "", "https://api.deepseek.com"},
		{"deepseek", "https://api.deepseek.com/v1", "https://api.deepseek.com"},
		{"deepseek", "https://api.deepseek.com/v1/", "https://api.deepseek.com"},
		{"deepseek", "https://api.deepseek.com/anthropic", "https://api.deepseek.com"}, // Claude Code 兼容层
		{"deepseek", "https://relay.example.com/v1", "https://relay.example.com"},
		{"deepseek", "/v1", "https://api.deepseek.com"}, // 剥完不剩合法站点 → 回落预设
		{"zhipu", "https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn"},
		{"zhipu", "https://open.bigmodel.cn/api/anthropic", "https://open.bigmodel.cn"}, // GLM 编码订阅兼容层前缀
		{"zhipu", "https://open.bigmodel.cn/api/anthropic/", "https://open.bigmodel.cn"},
		{"zhipu", "https://mirror.example.com/", "https://mirror.example.com"},
	}
	for _, c := range cases {
		got := AiAccountBaseURL(&AiAccount{Provider: c.provider, BaseURL: c.base})
		if got != c.want {
			t.Fatalf("BaseURL(%s,%q)=%q，期望 %q", c.provider, c.base, got, c.want)
		}
	}
}

// TestSyncQWeatherSystemAccount 和风系统账户行同步（3）：配置齐备自动建行
// （Key 加密落库、base_url 随专属 Host）、变更原位更新（id 不变）、服务商切走
// 连级联清理一并删行；系统行不占手动账户上限（CountAiAccounts 排除）。
func TestSyncQWeatherSystemAccount(t *testing.T) {
	s := openTestStore(t)

	// 未配置：同步为无操作
	if err := s.SyncQWeatherSystemAccount(&PageSettings{WeatherKey: "k", WeatherHost: "h"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountAiAccounts(); n != 0 {
		t.Fatalf("未配置不应产生账户行，实得 %d", n)
	}

	// 配置齐备：自动建行
	if err := s.SyncQWeatherSystemAccount(&PageSettings{WeatherProvider: QWeatherProvider, WeatherKey: "qw-key", WeatherHost: "h1demo.qweatherapi.com"}); err != nil {
		t.Fatal(err)
	}
	accs, err := s.ListAiAccounts()
	if err != nil || len(accs) != 1 {
		t.Fatalf("应恰好 1 行系统账户: err=%v n=%d", err, len(accs))
	}
	sys := accs[0]
	if sys.Provider != QWeatherProvider || sys.Name != "和风天气" || sys.BaseURL != "https://h1demo.qweatherapi.com" || sys.APIKey != "qw-key" || !sys.HasKey {
		t.Fatalf("系统账户字段不符: %+v", sys)
	}
	// 不占手动账户名额：建满 8 个手动账户后上限计数仍=8（系统行被排除；
	// 上限拦截本身在 API 层，store 的 Create 不限量）
	if n, _ := s.CountAiAccounts(); n != 0 {
		t.Fatalf("系统账户不应计入上限计数，实得 %d", n)
	}
	for i := 0; i < MaxAIAccounts; i++ {
		if _, err := s.CreateAiAccount(&AiAccount{Name: "x", Provider: "zhipu", APIKey: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := s.CountAiAccounts(); n != MaxAIAccounts {
		t.Fatalf("8 个手动账户时上限计数应=8，实得 %d", n)
	}

	// 变更配置：同一行原位更新（id/快照位保留，Key/Host 跟随）
	oldID := sys.ID
	if err := s.SyncQWeatherSystemAccount(&PageSettings{WeatherProvider: QWeatherProvider, WeatherKey: "qw-key2", WeatherHost: "h2demo.qweatherapi.com"}); err != nil {
		t.Fatal(err)
	}
	accs, _ = s.ListAiAccounts()
	var got *AiAccount
	for i := range accs {
		if accs[i].Provider == QWeatherProvider {
			got = &accs[i]
		}
	}
	if got == nil || got.ID != oldID || got.APIKey != "qw-key2" || got.BaseURL != "https://h2demo.qweatherapi.com" {
		t.Fatalf("变更配置应原位更新系统行: %+v", got)
	}

	// 服务商切走：系统行删除（含磁贴引用级联，口径同 DeleteAiAccount）
	if err := s.SyncQWeatherSystemAccount(&PageSettings{WeatherProvider: "gaode", WeatherKey: "gk"}); err != nil {
		t.Fatal(err)
	}
	accs, _ = s.ListAiAccounts()
	for _, a := range accs {
		if a.Provider == QWeatherProvider {
			t.Fatal("配置移除后系统行应被删除")
		}
	}
}

// TestWxUsageLedger 和风请求量小时桶账本（3 的当月请求量口径）：幂等落桶
// （重复轮询不重复计数）、退化桶（Ts<=0）跳过、当月合计、40 天前旧桶随写清理。
func TestWxUsageLedger(t *testing.T) {
	if time.Now().Day() == 1 && time.Now().Hour() < 2 {
		t.Skip("跨月边界小时：桶会落到上月，当月合计断言不适用")
	}
	s := openTestStore(t)
	base := time.Now().Truncate(time.Hour)
	mk := func(offset int, success, errors int64) WxHour {
		ts := base.Add(time.Duration(offset) * time.Hour)
		return WxHour{Label: ts.Format("15:04"), Ts: ts.Unix(), Success: success, Errors: errors}
	}

	hours := []WxHour{mk(-2, 10, 1), mk(-1, 20, 0)}
	// 同一批桶落两轮（模拟 20 分钟一拍重复轮询）必须幂等
	if err := s.RecordWxHours(hours); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWxHours(hours); err != nil {
		t.Fatal(err)
	}
	ms, me, ok, err := s.WxMonthUsage()
	if err != nil || !ok {
		t.Fatalf("当月应有桶: ok=%v err=%v", ok, err)
	}
	if ms != 30 || me != 1 {
		t.Fatalf("当月合计应为 30/1，实得 %d/%d", ms, me)
	}

	// 退化桶（asOf 不可解析时的序号桶）不入账本
	if err := s.RecordWxHours([]WxHour{{Label: "#0", Success: 99}}); err != nil {
		t.Fatal(err)
	}
	if ms, _, _, _ := s.WxMonthUsage(); ms != 30 {
		t.Fatalf("退化桶不应入账，当月仍应 30，实得 %d", ms)
	}

	// 40 天前旧桶随下一轮写入被清理
	old := time.Now().AddDate(0, 0, -41)
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO wx_usage_hours (hour_key, success, errors) VALUES (?, 5, 0)`, old.Format("2006-01-02T15")); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordWxHours(hours); err != nil {
		t.Fatal(err)
	}
	var stale int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM wx_usage_hours WHERE hour_key < ?`, time.Now().AddDate(0, 0, -40).Format("2006-01-02T15")).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("40 天前旧桶应被清理，残留 %d", stale)
	}
}
