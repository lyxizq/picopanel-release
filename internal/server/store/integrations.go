package store

import (
	"database/sql"
	"errors"
	"time"
)

// 服务集成类型（integrations.type 合法值，API 层校验用）。
const (
	IntegrationHA   = "homeassistant"
	IntegrationQBit = "qbittorrent"
	IntegrationEmby = "emby"

	// 436-441 扩容（Homepage widget 目录对照后点名引入）：
	IntegrationAdGuard      = "adguard"      // AdGuard Home DNS 拦截统计
	IntegrationPihole       = "pihole"       // Pi-hole（v6 优先、v5 回落）
	IntegrationTransmission = "transmission" // Transmission RPC
	IntegrationDeluge       = "deluge"       // Deluge Web JSON-RPC
	IntegrationAria2        = "aria2"        // aria2 JSON-RPC
	IntegrationNavidrome    = "navidrome"    // Navidrome（Subsonic API）
	IntegrationNextcloud    = "nextcloud"    // Nextcloud（OCS serverinfo）
	IntegrationProxmox      = "proxmox"      // Proxmox VE 集群资源
	IntegrationDocker       = "docker"       // 本机 Docker（unix socket，免 URL）
	IntegrationPortainer    = "portainer"    // Portainer 环境汇总
	IntegrationDSM          = "dsm"          // 群晖 DSM
	IntegrationTrueNAS      = "truenas"      // TrueNAS SCALE
)

// IntegrationTypes 合法类型集。
func IntegrationTypes() map[string]bool {
	return map[string]bool{
		IntegrationHA: true, IntegrationQBit: true, IntegrationEmby: true,
		IntegrationAdGuard: true, IntegrationPihole: true,
		IntegrationTransmission: true, IntegrationDeluge: true, IntegrationAria2: true,
		IntegrationNavidrome: true, IntegrationNextcloud: true,
		IntegrationProxmox: true, IntegrationDocker: true, IntegrationPortainer: true,
		IntegrationDSM: true, IntegrationTrueNAS: true,
	}
}

// Integration 对应 integrations 一行：一个第三方服务数据源。
// Auth 为凭据 JSON（homeassistant=长效访问令牌 / qbittorrent=WebUI 账号密码
// 或 ≥5.2 API Key / emby=API Key），AES-GCM 加密存库（secret.go）——store 层
// 出入均为明文（内部消费），API 层不得原样外发。Config 为明文配置 JSON
// （HA 实体列表、qB 跳过证书校验、Emby 正在播放开关等），结构由各类型客户端解析。
type Integration struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Auth      string `json:"-"` // 加密存储，禁止序列化外发；轻量扫描（ListIntegrationMetas）恒为空
	HasAuth   bool   `json:"-"` // 已配置凭据（库中密文非空判定，轻量/解密两模式都填）
	Config    string `json:"config"`
	Sort      int64  `json:"sort"`
	Enabled   bool   `json:"enabled"`
	LastOKAt  int64  `json:"last_ok_at"`
	LastError string `json:"last_error"`
}

// ErrIntegrationNotFound 集成不存在（GetIntegration）。
var ErrIntegrationNotFound = errors.New("服务集成不存在")

// ListIntegrations 全部集成（含解密后的凭据明文，抓取用），按手动排序
// （未排过的按创建序兜底）。
func (s *Store) ListIntegrations() ([]Integration, error) {
	return s.listIntegrations(true)
}

// ListIntegrationMetas 全部集成的展示元信息（不解密：Auth 恒为空、视图层
// has_auth 由密文非空判定）。管理清单/主页快照等展示路径用。
func (s *Store) ListIntegrationMetas() ([]Integration, error) {
	return s.listIntegrations(false)
}

func (s *Store) listIntegrations(decrypt bool) ([]Integration, error) {
	rows, err := s.db.Query(`SELECT id, type, name, url, auth, config, sort, enabled, last_ok_at, last_error
		FROM integrations ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Integration
	for rows.Next() {
		in, err := s.scanIntegrationOpts(rows, decrypt)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// FirstIntegrationIDByType 第一个指定类型的集成 ID（无则 0）——Emby 历史等
// 「回落第一个 emby 集成」的 SQL 下推，替代全量清单解密后线性找。
func (s *Store) FirstIntegrationIDByType(typ string) int64 {
	var id int64
	_ = s.db.QueryRow(`SELECT id FROM integrations WHERE type = ? ORDER BY sort, id LIMIT 1`, typ).Scan(&id)
	return id
}

// GetEnabledIntegrationByType 第一个启用中的指定类型集成（含解密凭据，无则 nil）。
func (s *Store) GetEnabledIntegrationByType(typ string) (*Integration, error) {
	rows, err := s.db.Query(`SELECT id, type, name, url, auth, config, sort, enabled, last_ok_at, last_error
		FROM integrations WHERE type = ? AND enabled = 1 ORDER BY sort, id LIMIT 1`, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	in, err := s.scanIntegrationOpts(rows, true)
	if err != nil {
		return nil, err
	}
	return &in, nil
}

// GetIntegration 单个集成；不存在返回 ErrIntegrationNotFound。
func (s *Store) GetIntegration(id int64) (*Integration, error) {
	rows, err := s.db.Query(`SELECT id, type, name, url, auth, config, sort, enabled, last_ok_at, last_error
		FROM integrations WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, ErrIntegrationNotFound
	}
	in, err := s.scanIntegration(rows)
	if err != nil {
		return nil, err
	}
	return &in, nil
}

// scanIntegrationOpts 扫描集成行。decrypt=false 为轻量模式：Auth 保持库中
// 密文、HasAuth 语义由调用方按「密文非空」判定后置空 Auth——展示路径零
// 解密，也不会把密文带出去（seal 空串原样返回，密文非空 ⇔ 已配置）。
func (s *Store) scanIntegrationOpts(rows *sql.Rows, decrypt bool) (Integration, error) {
	var in Integration
	var auth, config string
	var enabled int64
	if err := rows.Scan(&in.ID, &in.Type, &in.Name, &in.URL, &auth, &config, &in.Sort, &enabled, &in.LastOKAt, &in.LastError); err != nil {
		return in, err
	}
	in.HasAuth = auth != ""
	if decrypt {
		in.Auth = s.unseal(auth)
	}
	in.Config = config
	in.Enabled = enabled != 0
	return in, nil
}

func (s *Store) scanIntegration(rows *sql.Rows) (Integration, error) {
	return s.scanIntegrationOpts(rows, true)
}

// CreateIntegration 新增集成（数量上限由 API 层校验）。auth 明文入参，库内加密。
func (s *Store) CreateIntegration(typ, name, url, auth, config string) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO integrations (type, name, url, auth, config, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, typ, name, url, s.seal(auth), config, now, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// UpdateIntegration 编辑集成（全字段覆盖；auth 为合并后的最终凭据明文，加密落库）。
func (s *Store) UpdateIntegration(id int64, name, url, auth, config string, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	_, err := s.db.Exec(`UPDATE integrations SET name = ?, url = ?, auth = ?, config = ?, enabled = ?, updated_at = ?
		WHERE id = ?`, name, url, s.seal(auth), config, en, time.Now().Unix(), id)
	return err
}

// DeleteIntegration 删除一个集成（缓存由集成管理器侧清理）。
func (s *Store) DeleteIntegration(id int64) error {
	_, err := s.db.Exec(`DELETE FROM integrations WHERE id = ?`, id)
	return err
}

// CountIntegrations 当前集成数（上限校验用）。
func (s *Store) CountIntegrations() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM integrations`).Scan(&n)
	return n, err
}

// ReorderIntegrations 按传入 id 顺序写入 sort 1..n（上下移排序保存）。
func (s *Store) ReorderIntegrations(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE integrations SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveIntegrationStatus 回写一次抓取的健康态：ok=true 记成功时刻并清错误；
// 失败只更新错误文案（保留 last_ok_at，展示「最近一次成功」）。
func (s *Store) SaveIntegrationStatus(id int64, ok bool, errMsg string) error {
	if ok {
		_, err := s.db.Exec(`UPDATE integrations SET last_ok_at = ?, last_error = '' WHERE id = ?`, time.Now().Unix(), id)
		return err
	}
	_, err := s.db.Exec(`UPDATE integrations SET last_error = ? WHERE id = ?`, truncIntegrationErr(errMsg), id)
	return err
}

func truncIntegrationErr(s string) string {
	if r := []rune(s); len(r) > 200 {
		return string(r[:200])
	}
	return s
}
