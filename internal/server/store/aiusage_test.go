package store

import (
	"strings"
	"testing"
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
