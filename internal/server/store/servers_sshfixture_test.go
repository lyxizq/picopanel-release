package store

import "time"

// newSSHServer 造一台存量 ssh 模式服务器行（SSH 直连创建入口已下线，本函数
// 仅供测试当夹具造兼容存量数据；字段加密形态与生产写入一致）。
func newSSHServer(s *Store, name, addr, user, keyPath, password string, groupID int64) (*Server, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO servers (name, agent_addr, collect_mode, ssh_user, ssh_key_path, ssh_password, group_id, created_at, updated_at)
		VALUES (?, ?, 'ssh', ?, ?, ?, ?, ?, ?)`,
		name, s.seal(addr), s.seal(user), keyPath, s.seal(password), groupID, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetServer(id)
}
