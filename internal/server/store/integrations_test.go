package store

import (
	"strings"
	"testing"
)

// TestIntegrationCRUD 集成全链路：建（凭据加密落库）→列→改（凭据合并口径）→排序→删。
func TestIntegrationCRUD(t *testing.T) {
	s := openTestStore(t)

	id, err := s.CreateIntegration(IntegrationQBit, "PT保种", "http://192.168.1.10:8080",
		`{"username":"admin","password":"secret123"}`, `{"insecure":false}`)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.CreateIntegration(IntegrationHA, "智能家居", "http://192.168.1.20:8123",
		`{"token":"haToken"}`, `{"entities":[{"label":"客厅温度","entity":"sensor.temp","unit":""}]}`)
	if err != nil {
		t.Fatal(err)
	}

	// 库内凭据必须密文（带 enc:v1: 前缀），读出还原明文
	var dbAuth string
	if err := s.db.QueryRow(`SELECT auth FROM integrations WHERE id = ?`, id).Scan(&dbAuth); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dbAuth, "enc:v1:") {
		t.Fatalf("凭据应加密落库，实际: %q", dbAuth)
	}
	in, err := s.GetIntegration(id)
	if err != nil || in == nil {
		t.Fatalf("读取失败: %v", err)
	}
	if in.Auth != `{"username":"admin","password":"secret123"}` {
		t.Fatalf("凭据读出应还原明文: %q", in.Auth)
	}
	if in.Enabled != true || in.Type != IntegrationQBit {
		t.Fatalf("字段不一致: %+v", in)
	}

	// 编辑：全字段覆盖 + enabled=false
	if err := s.UpdateIntegration(id, "PT", "http://127.0.0.1:8081", `{"token":"apikey"}`, `{}`, false); err != nil {
		t.Fatal(err)
	}
	in, _ = s.GetIntegration(id)
	if in.Name != "PT" || in.Auth != `{"token":"apikey"}` || in.Enabled {
		t.Fatalf("编辑未生效: %+v", in)
	}

	// 排序：id2 排最前
	if err := s.ReorderIntegrations([]int64{id2, id}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListIntegrations()
	if len(list) != 2 || list[0].ID != id2 {
		t.Fatalf("排序未生效: %+v", list)
	}
	if n, _ := s.CountIntegrations(); n != 2 {
		t.Fatalf("计数应 2: %d", n)
	}

	// 健康态回写：成功记 last_ok_at 清错误；失败保留 last_ok_at
	if err := s.SaveIntegrationStatus(id, true, ""); err != nil {
		t.Fatal(err)
	}
	in, _ = s.GetIntegration(id)
	if in.LastOKAt == 0 || in.LastError != "" {
		t.Fatalf("成功态未回写: %+v", in)
	}
	if err := s.SaveIntegrationStatus(id, false, "HTTP 403"); err != nil {
		t.Fatal(err)
	}
	in, _ = s.GetIntegration(id)
	if in.LastError != "HTTP 403" || in.LastOKAt == 0 {
		t.Fatalf("失败态回写错误（应保留 last_ok_at）: %+v", in)
	}

	// 不存在的集成
	if _, err := s.GetIntegration(9999); err != ErrIntegrationNotFound {
		t.Fatalf("应返回 NotFound: %v", err)
	}

	// 删除
	if err := s.DeleteIntegration(id); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.ListIntegrations(); len(list) != 1 {
		t.Fatalf("删除失败: %v", list)
	}
}
