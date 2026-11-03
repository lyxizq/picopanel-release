package store

import (
	"strings"
	"testing"
)

// TestDBConnCRUD 连接全链路：建（凭据加密落库）→列→改（凭据留空沿用）→健康态→删。
func TestDBConnCRUD(t *testing.T) {
	s := openTestStore(t)

	id, err := s.CreateDBConnection(&DBConnection{Name: "主库", Type: "mysql", Host: "10.0.0.5",
		Port: 3306, Database: "app", Auth: `{"username":"root","password":"pw123"}`, Params: DBConnParams{SSL: "off", TimeoutSec: 10}})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.CreateDBConnection(&DBConnection{Name: "本机 SQLite", Type: "sqlite", Database: "/data/x.db"})
	if err != nil {
		t.Fatal(err)
	}

	// 库内凭据必须密文；读出经 DBConnCredentials 还原明文
	var dbAuth string
	if err := s.db.QueryRow(`SELECT auth FROM db_connections WHERE id = ?`, id).Scan(&dbAuth); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dbAuth, "enc:v1:") {
		t.Fatalf("凭据应加密落库，实际: %q", dbAuth)
	}
	c, err := s.GetDBConnection(id)
	if err != nil {
		t.Fatal(err)
	}
	if !c.HasAuth || !strings.HasPrefix(c.Auth, "enc:v1:") {
		t.Fatalf("读出应保持密文且有 HasAuth 标记: %+v", c)
	}
	user, pass, err := s.DBConnCredentials(c)
	if err != nil || user != "root" || pass != "pw123" {
		t.Fatalf("凭据解密不符: %q %q %v", user, pass, err)
	}

	// 编辑：密码留空沿用
	c2, _ := s.GetDBConnection(id)
	c2.Name = "主库改名"
	c2.Auth = ""
	c2.Readonly = true
	if err := s.UpdateDBConnection(c2); err != nil {
		t.Fatal(err)
	}
	c3, _ := s.GetDBConnection(id)
	if c3.Name != "主库改名" || !c3.Readonly {
		t.Fatalf("编辑未生效: %+v", c3)
	}
	if _, pass, _ := s.DBConnCredentials(c3); pass != "pw123" {
		t.Fatalf("密码留空应沿用: %q", pass)
	}

	// 健康态变化才回写
	if err := s.RecordDBConnStatus(id, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDBConnStatus(id, false, "dial refused"); err != nil {
		t.Fatal(err)
	}
	c4, _ := s.GetDBConnection(id)
	if c4.LastError != "dial refused" {
		t.Fatalf("错误未回写: %q", c4.LastError)
	}
	at := c4.LastOKAt
	if err := s.RecordDBConnStatus(id, false, "dial refused"); err != nil {
		t.Fatal(err)
	}
	c5, _ := s.GetDBConnection(id)
	if c5.LastOKAt != at {
		t.Fatalf("同错误重复回写应跳过")
	}

	// 列表含全部；删除生效
	list, _ := s.ListDBConnections()
	if len(list) != 2 {
		t.Fatalf("应有 2 个连接: %d", len(list))
	}
	if err := s.DeleteDBConnection(id2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDBConnection(id2); err != ErrDBConnNotFound {
		t.Fatalf("删除后应不存在: %v", err)
	}
}
