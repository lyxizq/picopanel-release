package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MailProviderPreset 常用邮箱服务商预设（316）：选预设自动填 IMAP/SMTP 服务器，
// custom=手填。smtp_ssl=465 隐式 TLS；0=587 STARTTLS。
type MailProviderPreset struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	SMTPSSL  bool   `json:"smtp_ssl"`
}

// MailProviderPresets 常用服务商清单（国内主流 + Gmail/Outlook + 企业邮）。
func MailProviderPresets() []MailProviderPreset {
	return []MailProviderPreset{
		{Key: "qq", Label: "QQ 邮箱", IMAPHost: "imap.qq.com", IMAPPort: 993, SMTPHost: "smtp.qq.com", SMTPPort: 465, SMTPSSL: true},
		{Key: "163", Label: "网易 163", IMAPHost: "imap.163.com", IMAPPort: 993, SMTPHost: "smtp.163.com", SMTPPort: 465, SMTPSSL: true},
		{Key: "126", Label: "网易 126", IMAPHost: "imap.126.com", IMAPPort: 993, SMTPHost: "smtp.126.com", SMTPPort: 465, SMTPSSL: true},
		{Key: "gmail", Label: "Gmail（应用专用密码）", IMAPHost: "imap.gmail.com", IMAPPort: 993, SMTPHost: "smtp.gmail.com", SMTPPort: 465, SMTPSSL: true},
		{Key: "outlook", Label: "Outlook / Microsoft 365", IMAPHost: "outlook.office365.com", IMAPPort: 993, SMTPHost: "smtp.office365.com", SMTPPort: 587, SMTPSSL: false},
		{Key: "sina", Label: "新浪邮箱", IMAPHost: "imap.sina.com", IMAPPort: 993, SMTPHost: "smtp.sina.com", SMTPPort: 465, SMTPSSL: true},
		{Key: "sohu", Label: "搜狐邮箱", IMAPHost: "imap.sohu.com", IMAPPort: 993, SMTPHost: "smtp.sohu.com", SMTPPort: 465, SMTPSSL: true},
		{Key: "139", Label: "中国移动 139", IMAPHost: "imap.139.com", IMAPPort: 993, SMTPHost: "smtp.139.com", SMTPPort: 465, SMTPSSL: true},
		{Key: "qiye163", Label: "网易企业邮", IMAPHost: "imap.qiye.163.com", IMAPPort: 993, SMTPHost: "smtp.qiye.163.com", SMTPPort: 465, SMTPSSL: true},
		{Key: "custom", Label: "自定义", IMAPHost: "", IMAPPort: 993, SMTPHost: "", SMTPPort: 465, SMTPSSL: true},
	}
}

// MailProviderByKey 预设键 → 预设（找不到返回 nil，按 custom 处理）。
func MailProviderByKey(key string) *MailProviderPreset {
	for _, p := range MailProviderPresets() {
		if p.Key == key {
			return &p
		}
	}
	return nil
}

// MaxMailAccounts 邮件账户上限。
const MaxMailAccounts = 8

// MailAccount 对应 mail_accounts 一行：一个收发邮箱。Password AES-GCM 加密存库
// （secret.go），store 层出入明文（内部消费），API 层不得原样外发。
// UserID 家庭成员数据隔离（2026-11-03）：0=管理员空间，>0=该成员个人空间，
// 互相不可见（可见性由 API 层按会话空间过滤 + 归属校验保证）。
type MailAccount struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	UserID    int64  `json:"user_id"`
	Provider  string `json:"provider"`
	IMAPHost  string `json:"imap_host"`
	IMAPPort  int    `json:"imap_port"`
	SMTPHost  string `json:"smtp_host"`
	SMTPPort  int    `json:"smtp_port"`
	SMTPSSL   bool   `json:"smtp_ssl"`
	Password  string `json:"-"`
	PollSec   int    `json:"poll_sec"` // 未读轮询间隔秒（默认 600，60~86400）
	Sort      int64  `json:"sort"`
	LastOKAt  int64  `json:"last_ok_at"`
	LastError string `json:"last_error"`
	HasAuth   bool   `json:"has_auth"` // 查询时由 store 填充
}

// DefaultMailPollSec 未读轮询默认间隔（10 分钟，2026-10-03 用户指定 60s 太频繁）。
const DefaultMailPollSec = 600

// clampPollSec 轮询间隔合法域（60~86400 秒，越界回落默认 600=10 分钟）。
func clampPollSec(v int) int {
	if v < 60 || v > 86400 {
		return 600
	}
	return v
}

// ErrMailAccountNotFound 账户不存在。
var ErrMailAccountNotFound = errors.New("邮件账户不存在")

const mailAccountCols = `id, name, email, user_id, provider, imap_host, imap_port, smtp_host, smtp_port, smtp_ssl, password, poll_sec, sort, last_ok_at, last_error`

// scanMailAccount 扫描账户行。decrypt=false 为轻量模式：Password 保持库中
// 密文、HasAuth 由密文非空判定（seal 空串原样返回，密文非空 ⇔ 已配置）后
// 置空 Password——列表/巡检/调度到期判断路径零解密，也不会把密文带出去。
func (s *Store) scanMailAccount(row interface{ Scan(...any) error }, decrypt bool) (*MailAccount, error) {
	var a MailAccount
	var ssl int
	err := row.Scan(&a.ID, &a.Name, &a.Email, &a.UserID, &a.Provider, &a.IMAPHost, &a.IMAPPort,
		&a.SMTPHost, &a.SMTPPort, &ssl, &a.Password, &a.PollSec, &a.Sort, &a.LastOKAt, &a.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMailAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	a.SMTPSSL = ssl != 0
	if a.PollSec <= 0 {
		a.PollSec = 600 // 未配置/旧数据回落默认 10 分钟
	}
	a.HasAuth = a.Password != ""
	if decrypt {
		a.Password = s.unseal(a.Password) // 密文→明文（与 integrations.Auth 同口径；漏了这步会用密文当密码登录）
	} else {
		a.Password = ""
	}
	return &a, nil
}

// ListMailAccounts 某空间（uid<0=全部）的账户（含解密后的密码明文，IMAP/SMTP
// 连接用），按手动排序。空间口径见 MailAccount.UserID 注释。
func (s *Store) ListMailAccounts(uid int64) ([]MailAccount, error) {
	return s.listMailAccounts(true, uid)
}

// ListMailAccountMetas 全部账户的展示/调度元信息（不解密：Password 恒为空、
// HasAuth 照常）。轮询管理器/巡检的全局视角（所有空间的账户都要拨号探活）。
func (s *Store) ListMailAccountMetas() ([]MailAccount, error) {
	return s.listMailAccounts(false, -1)
}

// ListMailAccountMetasFor 某空间（uid）账户的展示元信息（不解密），账户列表/
// 角标合计用（2026-11-03：成员只看自己账户）。
func (s *Store) ListMailAccountMetasFor(uid int64) ([]MailAccount, error) {
	return s.listMailAccounts(false, uid)
}

// listMailAccounts 账户清单：uid<0=全部，>=0=只取该空间。
func (s *Store) listMailAccounts(decrypt bool, uid int64) ([]MailAccount, error) {
	q := `SELECT ` + mailAccountCols + ` FROM mail_accounts`
	var args []any
	if uid >= 0 {
		q += ` WHERE user_id = ?`
		args = append(args, uid)
	}
	q += ` ORDER BY sort, id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailAccount
	for rows.Next() {
		a, err := s.scanMailAccount(rows, decrypt)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetMailAccount 单账户（含解密后的密码明文）。
func (s *Store) GetMailAccount(id int64) (*MailAccount, error) {
	return s.scanMailAccount(s.db.QueryRow(`SELECT `+mailAccountCols+` FROM mail_accounts WHERE id = ?`, id), true)
}

// CountMailAccounts 某空间（uid）当前账户数（创建上限校验用——各成员空间各自
// 计数，替代全量清单解密后计数）。
func (s *Store) CountMailAccounts(uid int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM mail_accounts WHERE user_id = ?`, uid).Scan(&n)
	return n, err
}

// CreateMailAccount 新建（密码明文入参、库内加密；归属打 a.UserID 创建者空间，
// 2026-11-03 家庭成员口径）。
func (s *Store) CreateMailAccount(a *MailAccount) (int64, error) {
	a.Name, a.Email = strings.TrimSpace(a.Name), strings.TrimSpace(a.Email)
	res, err := s.db.Exec(`INSERT INTO mail_accounts (name, email, user_id, provider, imap_host, imap_port, smtp_host, smtp_port, smtp_ssl, password, poll_sec, sort, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(sort),0)+1 FROM mail_accounts), ?, ?)`,
		a.Name, a.Email, a.UserID, a.Provider, a.IMAPHost, a.IMAPPort, a.SMTPHost, a.SMTPPort, boolInt(a.SMTPSSL), s.seal(a.Password), clampPollSec(a.PollSec), time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateMailAccount 更新（密码空串=沿用原密码）。
func (s *Store) UpdateMailAccount(a *MailAccount) error {
	a.Name, a.Email = strings.TrimSpace(a.Name), strings.TrimSpace(a.Email)
	if a.Password == "" {
		old, err := s.GetMailAccount(a.ID)
		if err != nil {
			return err
		}
		a.Password = old.Password
	}
	_, err := s.db.Exec(`UPDATE mail_accounts SET name=?, email=?, provider=?, imap_host=?, imap_port=?,
		smtp_host=?, smtp_port=?, smtp_ssl=?, password=?, poll_sec=?, updated_at=? WHERE id=?`,
		a.Name, a.Email, a.Provider, a.IMAPHost, a.IMAPPort, a.SMTPHost, a.SMTPPort,
		boolInt(a.SMTPSSL), s.seal(a.Password), clampPollSec(a.PollSec), time.Now().Unix(), a.ID)
	return err
}

// DeleteMailAccount 删除账户（连带信头缓存）。
func (s *Store) DeleteMailAccount(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM mail_headers WHERE account_id = ?`, id); err != nil {
		return err
	}
	res, err := s.db.Exec(`DELETE FROM mail_accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMailAccountNotFound
	}
	return nil
}

// UpdateMailAccountsOrder 保存手动排序（只动本空间 uid 内的账户，越空间 id
// 不生效——不报错，前端列表本就只含自己账户）。
func (s *Store) UpdateMailAccountsOrder(uid int64, ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE mail_accounts SET sort = ? WHERE id = ? AND user_id = ?`, i+1, id, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordMailAccountStatus 记录连接健康态：成功才推进 last_ok_at 并清错误（内容
// 变化才写，省无谓写库）；失败只写最近错误，不碰 last_ok_at（它专指最近成功，
// 503 修复：原失败路径也推进 last_ok_at 的休眠 bug，同 RecordPtSiteStatus 口径）。
func (s *Store) RecordMailAccountStatus(id int64, ok bool, errMsg string) error {
	now := time.Now().Unix()
	if ok {
		_, err := s.db.Exec(`UPDATE mail_accounts SET last_ok_at = ?, last_error = '' WHERE id = ? AND (last_ok_at != ? OR last_error != '')`, now, id, now)
		return err
	}
	_, err := s.db.Exec(`UPDATE mail_accounts SET last_error = ? WHERE id = ? AND last_error != ?`, errMsg, id, errMsg)
	return err
}

// ---- 信头缓存 ----

// MailHeader 信头缓存行（列表秒开，IMAP 为准）。
type MailHeader struct {
	UID      int64  `json:"uid"`
	FromName string `json:"from_name"`
	FromAddr string `json:"from_addr"`
	Subject  string `json:"subject"`
	DateUnix int64  `json:"date_unix"`
	Seen     bool   `json:"seen"`
	HasAtt   bool   `json:"has_att"`
	Size     int64  `json:"size"`
}

// ReplaceMailHeaders 整夹覆盖缓存（按账户+文件夹先删后插；手动刷新=服务器为准）。
func (s *Store) ReplaceMailHeaders(accountID int64, folder string, list []MailHeader) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM mail_headers WHERE account_id = ? AND folder = ?`, accountID, folder); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO mail_headers
		(account_id, folder, uid, from_name, from_addr, subject, date_unix, seen, has_att, size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, h := range list {
		if _, err := stmt.Exec(accountID, folder, h.UID, h.FromName, h.FromAddr, h.Subject, h.DateUnix, boolInt(h.Seen), boolInt(h.HasAtt), h.Size); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertMailHeaders 增量合并缓存（轮询入库用：只插新行/更新已知行，不动没提
// 到的行——整夹覆盖会清掉「刷新拉过的更深翻页」，轮询只拿最新 50 封）。
func (s *Store) UpsertMailHeaders(accountID int64, folder string, list []MailHeader) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO mail_headers
		(account_id, folder, uid, from_name, from_addr, subject, date_unix, seen, has_att, size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, h := range list {
		if _, err := stmt.Exec(accountID, folder, h.UID, h.FromName, h.FromAddr, h.Subject, h.DateUnix, boolInt(h.Seen), boolInt(h.HasAtt), h.Size); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteMailHeaders 邮件删除后同步清缓存行（本地库为准的列表，不清会「删了还在」）。
func (s *Store) DeleteMailHeaders(accountID int64, folder string, uids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`DELETE FROM mail_headers WHERE account_id = ? AND folder = ? AND uid = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, uid := range uids {
		if _, err := stmt.Exec(accountID, folder, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListMailHeaders 读缓存（按日期倒序，limit 条）。
func (s *Store) ListMailHeaders(accountID int64, folder string, limit int) ([]MailHeader, error) {
	return s.ListMailHeadersPage(accountID, folder, limit, 0)
}

// ListMailHeadersPage 读缓存分页（beforeUID>0 取更早一页，供「加载更多」）。
func (s *Store) ListMailHeadersPage(accountID int64, folder string, limit int, beforeUID int64) ([]MailHeader, error) {
	q := `SELECT uid, from_name, from_addr, subject, date_unix, seen, has_att, size
		FROM mail_headers WHERE account_id = ? AND folder = ?`
	args := []any{accountID, folder}
	if beforeUID > 0 {
		q += ` AND uid < ?`
		args = append(args, beforeUID)
	}
	q += ` ORDER BY date_unix DESC, uid DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailHeader
	for rows.Next() {
		var h MailHeader
		var seen, att int
		if err := rows.Scan(&h.UID, &h.FromName, &h.FromAddr, &h.Subject, &h.DateUnix, &seen, &att, &h.Size); err != nil {
			return nil, err
		}
		h.Seen, h.HasAtt = seen != 0, att != 0
		out = append(out, h)
	}
	return out, rows.Err()
}

// SetMailHeaderSeen 已读/未读状态同步进缓存。
func (s *Store) SetMailHeaderSeen(accountID int64, folder string, uid int64, seen bool) error {
	_, err := s.db.Exec(`UPDATE mail_headers SET seen = ? WHERE account_id = ? AND folder = ? AND uid = ?`, boolInt(seen), accountID, folder, uid)
	return err
}

// MailHeaderSeen 读单封缓存邮件的已读态（不存在 ok=false）——seen 接口在更新
// 前取旧值、据此修正 Manager 未读缓存用。
func (s *Store) MailHeaderSeen(accountID int64, folder string, uid int64) (seen, ok bool, err error) {
	var v int
	err = s.db.QueryRow(`SELECT seen FROM mail_headers WHERE account_id = ? AND folder = ? AND uid = ?`,
		accountID, folder, uid).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return v != 0, true, nil
}

// ---- 通讯录（506） ----

// 通讯录口径（2026-11-03 家庭成员隔离批）：mail_contacts 表无 user_id 列
// （schema 冻结不改），本批保持**全家共享通讯录**——只存姓名/邮箱的非敏感
// 常用项，与「家庭共享推送」同类的有意取舍；账户/信头/正文等私密数据已按
// 空间隔离。

// MailContact 通讯录一行：写信时按关键字带出/点选。email 归一小写存库（UNIQUE）。
type MailContact struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	CreatedAt int64  `json:"created_at"`
}

// ErrMailContactExists 邮箱已存在（UNIQUE 冲突的业务语义）。
var ErrMailContactExists = errors.New("该邮箱已在通讯录中")

// ErrMailContactNotFound 联系人不存在。
var ErrMailContactNotFound = errors.New("联系人不存在")

// normalizeContactEmail 归一：去空白 + 转小写（UNIQUE 按此判重）。
func normalizeContactEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ListMailContacts 全部联系人（按创建序；量小不分页，展示排序前端 localeCompare）。
func (s *Store) ListMailContacts() ([]MailContact, error) {
	rows, err := s.db.Query(`SELECT id, name, email, created_at FROM mail_contacts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailContact
	for rows.Next() {
		var ct MailContact
		if err := rows.Scan(&ct.ID, &ct.Name, &ct.Email, &ct.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ct)
	}
	return out, rows.Err()
}

// CreateMailContact 新增（email 必填已归一；同邮箱报 ErrMailContactExists）。
func (s *Store) CreateMailContact(name, email string) (*MailContact, error) {
	email = normalizeContactEmail(email)
	name = strings.TrimSpace(name)
	if email == "" {
		return nil, errors.New("邮箱地址不能为空")
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO mail_contacts (name, email, created_at) VALUES (?, ?, ?)`, name, email, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrMailContactExists
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &MailContact{ID: id, Name: name, Email: email, CreatedAt: now}, nil
}

// UpdateMailContact 修改（email 归一后查重，撞别人报 ErrMailContactExists）。
func (s *Store) UpdateMailContact(id int64, name, email string) (*MailContact, error) {
	email = normalizeContactEmail(email)
	name = strings.TrimSpace(name)
	if email == "" {
		return nil, errors.New("邮箱地址不能为空")
	}
	res, err := s.db.Exec(`UPDATE mail_contacts SET name = ?, email = ? WHERE id = ?`, name, email, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrMailContactExists
		}
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrMailContactNotFound
	}
	return &MailContact{ID: id, Name: name, Email: email}, nil
}

// DeleteMailContact 删除（不存在静默成功，幂等）。
func (s *Store) DeleteMailContact(id int64) error {
	_, err := s.db.Exec(`DELETE FROM mail_contacts WHERE id = ?`, id)
	return err
}

// ---- 个人签名（507） ----

const settingMailSignature = "mail_signature"

// MailSignatureKey 签名的 settings 键，按会话空间隔离（2026-11-03 家庭成员
// 口径，学 AvatarSettingKey 后缀式）：管理员 uid=0 沿用存量全局键（存量签名
// 归管理员，无需迁移），成员用 mail_signature:<uid>（改名不掉、删用户随
// DeleteUser 的 :<id> 键清理链路一并清掉）。
func MailSignatureKey(uid int64) string {
	if uid <= 0 {
		return settingMailSignature
	}
	return fmt.Sprintf("%s:%d", settingMailSignature, uid)
}

// MailSignature 读空间（uid）个人签名（settings kv，纯文本；写信/回复预填进
// 正文）。未配置返回空串。
func (s *Store) MailSignature(uid int64) (string, error) {
	v, _, err := s.GetSetting(MailSignatureKey(uid))
	return v, err
}

// SetMailSignature 保存空间（uid）个人签名（API 层已限 2000 字内）。
func (s *Store) SetMailSignature(uid int64, sig string) error {
	return s.SetSetting(MailSignatureKey(uid), sig)
}
