package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// lmKeyPath 敏感字段加密密钥的固定路径（写死在代码里，不进配置文件，
// 避免从配置一眼看到密钥位置）。存放 32 字节密钥的 base64 文本，
// 首次启动自动生成；文件不存在则直接在 /etc 下创建。
// 注意：该文件必须随数据库一起备份——丢失后已加密的密码/IP 无法恢复。
const lmKeyPath = "/etc/lm.idc"

// KeyFilePath 暴露密钥路径（api 层文件管理黑名单用：该文件禁止经面板读写）。
func KeyFilePath() string { return lmKeyPath }

// encPrefix 密文前缀：库中带此前缀的值为 AES-256-GCM 密文
// （base64(nonce||ciphertext)），不带前缀视为历史明文（读取时原样返回，
// 写入时统一加密，启动时自动迁移）。
const encPrefix = "enc:v1:"

// secretBox 持有密钥的字段级加解密器。加密字段：agent_addr、ssh_addr、
// ssh_user、ssh_password、token。
type secretBox struct {
	gcm cipher.AEAD
}

// newSecretBox 用 32 字节密钥构造。
func newSecretBox(key []byte) (*secretBox, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &secretBox{gcm: gcm}, nil
}

// seal 加密明文：空串原样返回；已是密文（带前缀）幂等返回；
// 其余返回 enc:v1: 前缀的随机 nonce 密文（同一明文每次密文不同）。
func (b *secretBox) seal(plain string) string {
	if plain == "" || strings.HasPrefix(plain, encPrefix) {
		return plain
	}
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		// 随机数耗尽属灾难性环境错误，明文写入比丢数据更糟，直接报错上抛由调用方决定
		panic(fmt.Sprintf("生成加密 nonce 失败: %v", err))
	}
	ct := b.gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ct)
}

// open 解密：空串原样；不带前缀视为历史明文原样返回（迁移前的旧数据）；
// 解密失败（密钥不匹配/数据损坏）返回空串——字段缺失比暴露错误数据安全，
// 上层表现为该服务器连不上/未配置，重新填写即可。
func (b *secretBox) open(txt string) string {
	if txt == "" || !strings.HasPrefix(txt, encPrefix) {
		return txt
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(txt, encPrefix))
	if err != nil || len(raw) < b.gcm.NonceSize() {
		return ""
	}
	nonce, ct := raw[:b.gcm.NonceSize()], raw[b.gcm.NonceSize():]
	plain, err := b.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

// loadOrCreateKey 读取密钥文件；不存在则生成（含创建目录，权限 0600）。
// 文件内容为 32 字节密钥的 base64 文本（便于查看/备份，末尾换行容忍）。
func loadOrCreateKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		key, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if derr != nil || len(key) != 32 {
			return nil, fmt.Errorf("密钥文件 %s 内容无效（应为 32 字节密钥的 base64）", path)
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取密钥文件 %s: %w", path, err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成密钥: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建密钥目录 %s: %w", filepath.Dir(path), err)
	}
	txt := base64.StdEncoding.EncodeToString(key) + "\n"
	if err := os.WriteFile(path, []byte(txt), 0o600); err != nil {
		return nil, fmt.Errorf("写入密钥文件 %s: %w", path, err)
	}
	return key, nil
}

// encFields 加密的敏感字段（servers 表列名）。
var encFields = []string{"agent_addr", "ssh_addr", "ssh_user", "ssh_password", "token"}

// seal/unseal Store 级便捷入口：sec 为 nil（测试直连库）时原样返回。
func (s *Store) seal(v string) string {
	if s == nil || s.sec == nil {
		return v
	}
	return s.sec.seal(v)
}

func (s *Store) unseal(v string) string {
	if s == nil || s.sec == nil {
		return v
	}
	return s.sec.open(v)
}

// migrateEncryptPlaintext 启动时把历史明文敏感字段一次性加密重写
// （幂等：已带密文前缀的行跳过）。放在 Open 末尾执行，此后写入路径
// 一律先加密，库里不再出现明文。
func (s *Store) migrateEncryptPlaintext() error {
	rows, err := s.db.Query(`SELECT id, agent_addr, ssh_addr, ssh_user, ssh_password,
		COALESCE(token,'') FROM servers`)
	if err != nil {
		return fmt.Errorf("扫描敏感字段: %w", err)
	}
	type upd struct {
		id      int64
		vals    [5]string
		changed bool
	}
	var ups []upd
	for rows.Next() {
		var u upd
		if err := rows.Scan(&u.id, &u.vals[0], &u.vals[1], &u.vals[2], &u.vals[3], &u.vals[4]); err != nil {
			rows.Close()
			return err
		}
		for i, v := range u.vals {
			if v != "" && !strings.HasPrefix(v, encPrefix) {
				u.vals[i] = s.sec.seal(v)
				u.changed = true
			}
		}
		if u.changed {
			ups = append(ups, u)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ups) == 0 {
		return nil
	}
	for _, u := range ups {
		if _, err := s.db.Exec(`UPDATE servers SET agent_addr = ?, ssh_addr = ?, ssh_user = ?,
			ssh_password = ?, token = ? WHERE id = ?`,
			u.vals[0], u.vals[1], u.vals[2], u.vals[3], tokenOrNull(u.vals[4]), u.id); err != nil {
			return fmt.Errorf("加密迁移服务器 %d: %w", u.id, err)
		}
	}
	log.Printf("已加密 %d 台服务器的敏感字段（地址/SSH 凭据/密钥）", len(ups))
	return nil
}
