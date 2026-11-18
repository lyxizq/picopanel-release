package store

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// HA 智能家居总配置（475）：开机迁移=无 ha_master 键时从首个 HA 集成拷贝连接
// 参数与 rooms/devices/sensors（集成行原样保留）；无集成写 {} 哨兵防误播种；
// 连接保存 token 留空沿用、清单保存连接键不动。
func TestHAMaster(t *testing.T) {
	dir := t.TempDir()

	// 库 A：有 HA 集成 → 迁移拷贝，集成行原样保留
	s, err := OpenWithKey(filepath.Join(dir, "a.db"), filepath.Join(dir, "k1"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateIntegration(IntegrationHA, "家庭", "http://192.168.1.2:8123",
		`{"token":"ha-token-1"}`, `{"insecure":true,"entities":[{"entity":"light.x"}],"rooms":[{"id":"r1","name":"客厅"}],"devices":[{"entity":"light.x","room":"r1"}],"sensors":[{"entity":"sensor.t"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	// OpenWithKey 时库尚空已写 {} 哨兵；删键重跑迁移模拟真实升级场景（存量库：已有集成、无 ha_master）
	if err := s.DeleteSetting("ha_master"); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateHAMaster(); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetHAMaster()
	if err != nil {
		t.Fatal(err)
	}
	if m.URL != "http://192.168.1.2:8123" || m.Token != "ha-token-1" || !m.Insecure {
		t.Fatalf("迁移应拷贝连接参数: %+v", m)
	}
	var rooms []map[string]any
	if err := json.Unmarshal(m.Rooms, &rooms); err != nil || len(rooms) != 1 || rooms[0]["name"] != "客厅" {
		t.Fatalf("迁移应拷贝 rooms: %s err=%v", m.Rooms, err)
	}
	if len(json.RawMessage(m.Devices)) == 0 || len(json.RawMessage(m.Sensors)) == 0 {
		t.Fatalf("迁移应拷贝 devices/sensors")
	}
	in, _ := s.GetIntegration(id)
	if in == nil || in.Config == "" || in.URL != "http://192.168.1.2:8123" {
		t.Fatalf("集成行应原样保留: %+v", in)
	}

	// 连接保存：token 留空沿用；清单保存连接键不动
	if err := s.SaveHAMasterConnection("http://10.0.0.8:8123", "", false); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetHAMaster()
	if m.URL != "http://10.0.0.8:8123" || m.Token != "ha-token-1" || m.Insecure {
		t.Fatalf("token 留空应沿用且 insecure 覆盖: %+v", m)
	}
	if err := s.SaveHAMasterLists(json.RawMessage(`[{"id":"r2","name":"卧室"}]`), nil, nil); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetHAMaster()
	if m.URL != "http://10.0.0.8:8123" || m.Token != "ha-token-1" {
		t.Fatalf("清单保存不应动连接键: %+v", m)
	}
	if string(m.Rooms) != `[{"id":"r2","name":"卧室"}]` {
		t.Fatalf("rooms 应更新: %s", m.Rooms)
	}

	// 库 B：无 HA 集成 → {} 哨兵，日后新建集成不再播种
	s2, err := OpenWithKey(filepath.Join(dir, "b.db"), filepath.Join(dir, "k2"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	m2, _ := s2.GetHAMaster()
	if m2.URL != "" {
		t.Fatalf("无集成应零值: %+v", m2)
	}
	if _, err := s2.CreateIntegration(IntegrationHA, "后来", "http://x", `{"token":"t"}`, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s2.MigrateHAMaster(); err != nil { // 再跑一次（重启模拟）：哨兵在位不播种
		t.Fatal(err)
	}
	m2, _ = s2.GetHAMaster()
	if m2.URL != "" {
		t.Fatalf("哨兵在位不得播种: %+v", m2)
	}
}
