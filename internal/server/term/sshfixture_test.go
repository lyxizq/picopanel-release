package term

import (
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// newSSHServer 造一台存量 ssh 模式服务器行（SSH 直连创建入口已下线，本函数
// 仅供测试当夹具；明文落库走 scanServer 的历史明文容忍路径，读取行为一致）。
func newSSHServer(t *testing.T, s *store.Store, name, addr, user, keyPath, password string, groupID int64) *store.Server {
	t.Helper()
	now := time.Now().Unix()
	res, err := s.DB().Exec(`INSERT INTO servers (name, agent_addr, collect_mode, ssh_user, ssh_key_path, ssh_password, group_id, created_at, updated_at)
		VALUES (?, ?, 'ssh', ?, ?, ?, ?, ?, ?)`,
		name, addr, user, keyPath, password, groupID, now, now)
	if err != nil {
		t.Fatalf("造 ssh 夹具: %v", err)
	}
	id, _ := res.LastInsertId()
	srv, err := s.GetServer(id)
	if err != nil {
		t.Fatalf("读 ssh 夹具: %v", err)
	}
	return srv
}
