package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"lz-monitor/internal/ptfetch"
)

// PT 站点信息（391）：内置站点库选站 + Cookie/API Key 凭据（AES-GCM 加密存库，
// 同 mail/aiusage 口径）+ 最新一份覆盖式用户信息快照。store 层出入明文（内部
// 消费），API 层不得原样外发凭据；站点目录（内置清单）由 pt 包维护，本层只管行。

// MaxPtSites 站点数上限（内置目录 27 个，留余量）。
const MaxPtSites = 32

// PtSite 对应 pt_sites 一行。Cookie 列对 M-Team 类 API 站存 x-api-key。
type PtSite struct {
	ID      int64  `json:"id"`
	SiteKey string `json:"site_key"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"` // 空 = 目录默认站点（镜像/备用域名可覆盖）
	OpenURL string `json:"open_url"` // 点击站点名打开的地址（空 = 站点首页）

	// ProxyServerID 抓取出口（391 扩展）：0 = 面板直连；>0 = 经该监控服务器的
	// push agent 中转抓取（家里 NAS 等零暴露机器，出口 IP 与浏览器登录一致防连坐）
	ProxyServerID int64  `json:"proxy_server_id"`
	Icon          string `json:"icon"`
	// SigninMode 签到模式（416，dian115 消费）：'' = 仅查询不签到；
	// normal/lucky = 每日轮与手动刷新时自动签到。
	SigninMode string `json:"signin_mode"`
	Cookie     string `json:"-"`
	UA         string `json:"ua"` // 空 = 内置默认 UA（Cloudflare 站建议填浏览器 UA）
	Enabled    bool   `json:"enabled"`
	Sort       int64  `json:"sort"`
	DataJSON   string `json:"-"`
	FetchedAt  int64  `json:"fetched_at"`
	LastOKAt   int64  `json:"last_ok_at"`
	LastError  string `json:"last_error"`
	HasCookie  bool   `json:"has_cookie"` // 查询时由 store 填充
}

// PtSnapshot 用户信息快照（data_json 列的序列化形态）。定义在 ptfetch 叶子包
// （agent 中转共用），此处别名保持 store 侧引用面不变。
type PtSnapshot = ptfetch.Snapshot

// ErrPtSiteNotFound / ErrPtSiteExists 站点行错误。
var (
	ErrPtSiteNotFound = errors.New("PT 站点不存在")
	ErrPtSiteExists   = errors.New("该站点已添加")
)

const ptSiteCols = `id, site_key, name, base_url, open_url, proxy_server_id, signin_mode, cookie, ua, enabled, sort, data_json, fetched_at, last_ok_at, last_error`

// scanPtSite 扫描站点行。decrypt=false 为轻量模式：Cookie 保持库中密文、
// HasCookie 由密文非空判定（seal 空串原样返回，密文非空 ⇔ 已配置）后置空
// Cookie——目录/清单/查重路径零解密，也不会把密文带出去。
func (s *Store) scanPtSite(row interface{ Scan(...any) error }, decrypt bool) (*PtSite, error) {
	var a PtSite
	var enabled int64
	err := row.Scan(&a.ID, &a.SiteKey, &a.Name, &a.BaseURL, &a.OpenURL, &a.ProxyServerID, &a.SigninMode, &a.Cookie, &a.UA, &enabled, &a.Sort,
		&a.DataJSON, &a.FetchedAt, &a.LastOKAt, &a.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPtSiteNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Enabled = enabled != 0
	a.HasCookie = a.Cookie != ""
	if decrypt {
		a.Cookie = s.unseal(a.Cookie)
	} else {
		a.Cookie = ""
	}
	return &a, nil
}

// ListPtSites 全部站点（含解密后的 Cookie 明文，抓取/签到用），按手动排序。
func (s *Store) ListPtSites() ([]PtSite, error) {
	return s.listPtSites(true)
}

// ListPtSiteMetas 全部站点的展示元信息（不解密：Cookie 恒为空、HasCookie
// 照常），目录已添加判定/清单展示/数量上限用。
func (s *Store) ListPtSiteMetas() ([]PtSite, error) {
	return s.listPtSites(false)
}

func (s *Store) listPtSites(decrypt bool) ([]PtSite, error) {
	rows, err := s.db.Query(`SELECT ` + ptSiteCols + ` FROM pt_sites ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PtSite
	for rows.Next() {
		a, err := s.scanPtSite(rows, decrypt)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetPtSite 单站点（含解密后的 Cookie 明文）。
func (s *Store) GetPtSite(id int64) (*PtSite, error) {
	return s.scanPtSite(s.db.QueryRow(`SELECT `+ptSiteCols+` FROM pt_sites WHERE id = ?`, id), true)
}

// CountPtSites 当前站点数（创建上限校验用，替代全量清单解密后计数）。
func (s *Store) CountPtSites() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pt_sites`).Scan(&n)
	return n, err
}

// PtSiteKeyExists 站点 key 是否已添加（查重用，SQL 等值查找替代全量清单解密）。
func (s *Store) PtSiteKeyExists(siteKey string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pt_sites WHERE site_key = ?`, siteKey).Scan(&n)
	return n > 0, err
}

// CreatePtSite 新建（站点 key 已由 API 层校验在目录内且未重复）。
func (s *Store) CreatePtSite(a *PtSite) (int64, error) {
	a.Name, a.BaseURL, a.OpenURL, a.UA = strings.TrimSpace(a.Name), strings.TrimSpace(a.BaseURL), strings.TrimSpace(a.OpenURL), strings.TrimSpace(a.UA)
	a.Icon = strings.TrimSpace(a.Icon)
	res, err := s.db.Exec(`INSERT INTO pt_sites (site_key, name, base_url, open_url, proxy_server_id, signin_mode, icon, cookie, ua, enabled, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, (SELECT COALESCE(MAX(sort),0)+1 FROM pt_sites), ?, ?)`,
		a.SiteKey, a.Name, a.BaseURL, a.OpenURL, a.ProxyServerID, a.SigninMode, a.Icon, s.seal(a.Cookie), a.UA, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePtSite 更新（Cookie 空串=沿用原值；name/base_url/ua/enabled 全量覆盖）。
func (s *Store) UpdatePtSite(a *PtSite) error {
	a.Name, a.BaseURL, a.OpenURL, a.UA = strings.TrimSpace(a.Name), strings.TrimSpace(a.BaseURL), strings.TrimSpace(a.OpenURL), strings.TrimSpace(a.UA)
	a.Icon = strings.TrimSpace(a.Icon)
	if a.Cookie == "" {
		old, err := s.GetPtSite(a.ID)
		if err != nil {
			return err
		}
		a.Cookie = old.Cookie
	}
	_, err := s.db.Exec(`UPDATE pt_sites SET name=?, base_url=?, open_url=?, proxy_server_id=?, signin_mode=?, icon=?, cookie=?, ua=?, enabled=?, updated_at=? WHERE id=?`,
		a.Name, a.BaseURL, a.OpenURL, a.ProxyServerID, a.SigninMode, a.Icon, s.seal(a.Cookie), a.UA, boolInt(a.Enabled), time.Now().Unix(), a.ID)
	return err
}

// DeletePtSite 删除站点。
func (s *Store) DeletePtSite(id int64) error {
	res, err := s.db.Exec(`DELETE FROM pt_sites WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPtSiteNotFound
	}
	return nil
}

// UpdatePtSitesOrder 保存手动排序。
func (s *Store) UpdatePtSitesOrder(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE pt_sites SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordPtSiteStatus 记录抓取健康态：成功才推进 last_ok_at 并清错误（内容
// 变化才写，省无谓写库）；失败只写最近错误，不碰 last_ok_at（它专指最近成功）。
func (s *Store) RecordPtSiteStatus(id int64, ok bool, errMsg string) error {
	now := time.Now().Unix()
	if ok {
		_, err := s.db.Exec(`UPDATE pt_sites SET last_ok_at = ?, last_error = '' WHERE id = ? AND (last_ok_at != ? OR last_error != '')`, now, id, now)
		return err
	}
	_, err := s.db.Exec(`UPDATE pt_sites SET last_error = ? WHERE id = ? AND last_error != ?`, errMsg, id, errMsg)
	return err
}

// SetPtSiteData 覆盖式写入最新快照。
func (s *Store) SetPtSiteData(id int64, snap *PtSnapshot) error {
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE pt_sites SET data_json = ?, fetched_at = ? WHERE id = ?`, string(b), time.Now().Unix(), id)
	return err
}
