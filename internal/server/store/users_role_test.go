package store

import (
	"testing"
	"time"
)

// TestUserRoleCRUD 多用户角色（361）：创建带角色、角色清洗、改角色、管理员计数、
// 删用户连带清会话。
func TestUserRoleCRUD(t *testing.T) {
	s := openUserTestStore(t)
	if _, err := s.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	if NormalizeRole("user") != RoleUser || NormalizeRole("weird") != RoleAdmin {
		t.Fatal("角色清洗口径不对")
	}

	// 创建受限用户
	id, err := s.CreateUser("fam", "pass123", "user")
	if err != nil {
		t.Fatal(err)
	}
	// 重名拒绝
	if _, err := s.CreateUser("fam", "pass123", "user"); err != ErrUsernameExists {
		t.Fatalf("重名应报 ErrUsernameExists: %v", err)
	}
	// 角色缺失值清洗为 admin
	id2, err := s.CreateUser("boss", "pass123", "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := s.UserByID(id)
	if u == nil || u.Role != RoleUser {
		t.Fatalf("fam 应是 user: %+v", u)
	}
	u2, _ := s.UserByID(id2)
	if u2 == nil || u2.Role != RoleAdmin {
		t.Fatalf("空角色应清洗为 admin: %+v", u2)
	}

	// 列表带角色、不含哈希外泄口径由 api 层负责；这里验证计数
	if n, _ := s.CountAdmins(); n != 2 {
		t.Fatalf("管理员应 2 个: %d", n)
	}

	// 降级一个还有 1 个 admin
	if err := s.UpdateUserRole(id2, RoleUser); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountAdmins(); n != 1 {
		t.Fatalf("降级后管理员应 1 个: %d", n)
	}

	// 删用户连带清会话
	token, err := s.CreateSession(id, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := s.SessionUser(token); u == nil {
		t.Fatal("会话应有效")
	}
	if err := s.DeleteUser(id); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.SessionUser(token); u != nil {
		t.Fatal("删用户后会话应失效")
	}
	if _, err := s.UserByID(id); err != nil {
		t.Fatal("查已删用户不应报错（返回 nil）")
	}
}
