package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User 对应 users 表一行。Role：admin=管理员（全部功能）、user=受限用户
// （361：仅开放 HA 页面及相关端点）。
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	CreatedAt    int64  `json:"created_at"`
}

// 角色取值。空值/未知值按 admin 兜底（存量库默认列值即 admin）。
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// NormalizeRole 角色清洗：仅接受 admin/user，其余回落 admin。
func NormalizeRole(r string) string {
	if r == RoleUser {
		return RoleUser
	}
	return RoleAdmin
}

// EnsureAdmin 确保至少存在一个管理员：用户表为空时创建 admin/初始密码。
// 返回是否执行了创建。
func (s *Store) EnsureAdmin(username, password string) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	_, err = s.db.Exec(`INSERT INTO users (username, password_hash, role, created_at) VALUES (?,?,?,?)`,
		username, string(hash), RoleAdmin, time.Now().Unix())
	return err == nil, err
}

// ListUsers 全部用户（按创建序）。
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, username, password_hash, role, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateUser 创建用户（密码在此处哈希）。用户名冲突返回 ErrUsernameExists。
func (s *Store) CreateUser(username, password, role string) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, role, created_at) VALUES (?,?,?,?)`,
		username, string(hash), NormalizeRole(role), time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, ErrUsernameExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateUserRole 改角色（角色即时生效：会话每请求现读用户行，无需踢会话）。
func (s *Store) UpdateUserRole(userID int64, role string) error {
	res, err := s.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, NormalizeRole(role), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// CountAdmins 现存管理员数（删除/降级前校验至少留一个）。
func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&n)
	return n, err
}

// UserByUsername 按用户名查询，未命中返回 (nil, nil)。
func (s *Store) UserByUsername(username string) (*User, error) {
	var u User
	err := s.db.QueryRow(`SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?`,
		username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UserByID 按 ID 查询（改用户名后会话中间件拿到的旧对象用户名可能过期，按 ID 重查）。
func (s *Store) UserByID(id int64) (*User, error) {
	var u User
	err := s.db.QueryRow(`SELECT id, username, password_hash, role, created_at FROM users WHERE id = ?`,
		id).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdatePassword 更新密码哈希。
func (s *Store) UpdatePassword(userID int64, hash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, userID)
	return err
}

// ErrUsernameExists 用户名已被占用（UNIQUE 约束），api 层转 400。
var ErrUsernameExists = errors.New("用户名已存在")

// UpdateUsername 改用户名（会话按 user_id 关联，改名不掉会话）；重名返回 ErrUsernameExists。
func (s *Store) UpdateUsername(userID int64, username string) error {
	res, err := s.db.Exec(`UPDATE users SET username = ? WHERE id = ?`, username, userID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrUsernameExists
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ResetFirstAdmin 把最小 id 的用户（首个创建的管理员）重置为指定用户名/密码/管理员角色，
// 并清掉其全部会话（-resetuser 恢复入口用）。用户表为空返回 false（不创建，交给 EnsureAdmin）。
func (s *Store) ResetFirstAdmin(username, passwordHash string) (bool, error) {
	var id int64
	err := s.db.QueryRow(`SELECT MIN(id) FROM users`).Scan(&id)
	if err != nil {
		return false, err
	}
	if id == 0 {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET username = ?, password_hash = ?, role = ? WHERE id = ?`,
		username, passwordHash, RoleAdmin, id); err != nil {
		// 用户名被其他行占用（理论上单用户系统不会发生）：先清掉占用行再重试
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			if _, err := tx.Exec(`DELETE FROM users WHERE id != ? AND username = ?`, id, username); err != nil {
				return false, err
			}
			if _, err := tx.Exec(`UPDATE users SET username = ?, password_hash = ? WHERE id = ?`,
				username, passwordHash, id); err != nil {
				return false, err
			}
		} else {
			return false, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// hashSessionToken 会话 token 落库前哈希（SHA-256 hex，65）：sessions 表不再
// 存明文 token——仅库文件/备份泄露的场景下拿不到可直接使用的活会话（token
// 本身 32 字节随机，哈希不降低猜测熵）。存量明文行由 SessionUser 命中时
// 原位升级，7 天 TTL 内自然过渡完毕。
func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession 创建登录会话，返回 token（明文只此一次给到客户端）。ttl 为有效期。
func (s *Store) CreateSession(userID int64, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO sessions (token, user_id, created_at, expires_at) VALUES (?,?,?,?)`,
		hashSessionToken(token), userID, now, now+int64(ttl.Seconds()))
	if err != nil {
		return "", err
	}
	return token, nil
}

// SessionUser 校验会话并返回对应用户，无效/过期返回 (nil, nil)。
// 角色随用户行现读：改角色/删用户即刻生效，无需清理会话。
// 只按哈希匹配——不回退明文：token 明文与哈希同为 64 位 hex 无法区分形态，
// 而库内存储值本身可当 token 走明文等值命中（库泄露即接管会话，恰是哈希
// 落库要防的场景）；哈希化迁移上线已久，存量明文会话（7 天 TTL）早已过期。
func (s *Store) SessionUser(token string) (*User, error) {
	return s.sessionUserByToken(hashSessionToken(token))
}

// sessionUserByToken 按库内存储的会话哈希查所属用户。
func (s *Store) sessionUserByToken(stored string) (*User, error) {
	var u User
	err := s.db.QueryRow(`SELECT u.id, u.username, u.password_hash, u.role, u.created_at
		FROM sessions se JOIN users u ON u.id = se.user_id
		WHERE se.token = ? AND se.expires_at > ?`, stored, time.Now().Unix()).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteSession 注销会话（哈希/存量明文双形态都清）。
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token IN (?, ?)`,
		hashSessionToken(token), token)
	return err
}

// DeleteOtherSessions 删除该用户除当前 token 外的全部会话（改密后踢出其他端）。
// 当前 token 以哈希与明文两种形态排除（存量会话场景下都能保住当前端）；
// keepToken 为空串时=全清（其哈希与空串均不会误命中任何真实会话行）。
func (s *Store) DeleteOtherSessions(userID int64, keepToken string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token NOT IN (?, ?)`,
		userID, hashSessionToken(keepToken), keepToken)
	return err
}

// DeleteUser 删除用户并连带清掉其全部登录会话（被删用户所有端立即失效）。
func (s *Store) DeleteUser(userID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	// 连带清理该用户的头像与两步验证配置（44/45 用户级 settings 键）
	for _, k := range []string{
		AvatarSettingKey(userID), TOTPSecretSettingKey(userID), TOTPPendingSettingKey(userID),
	} {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteExpiredSessions 清理过期会话。
func (s *Store) DeleteExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
	return err
}
