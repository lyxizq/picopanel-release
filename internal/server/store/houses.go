package store

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

// 房屋管理（2026-11-03）：管理员专属模块，不做家庭成员隔离（家庭资产无
// 个人空间语义，与 integrations 同口径；受限用户由 familyRoutes deny-by-default
// 拦在接口层）。四表：
//
//	houses        房屋（地址/户型/水电燃气卡号/出租方信息；status 为手工状态
//	              自住/空置，「出租中」由在租租约现算派生不落库）
//	house_items   家具电器（房下属件，随房级联删除）
//	house_leases  租约（同一房屋同时最多一条 active；退租置 ended 留档；
//	              到期前 30 天推一次，notify_30d_at 打标、改到期日重置）
//	house_expenses 消耗费用清单（桌椅/电器置换、取暖费等，spend_date 作收支
//	              统计的期间归属；2026-11-04）
const housesDDL = `CREATE TABLE IF NOT EXISTS houses (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  name          TEXT    NOT NULL,
  address       TEXT    NOT NULL DEFAULT '',
  area          TEXT    NOT NULL DEFAULT '',  -- 建筑面积，自由文本（如 89㎡）
  layout        TEXT    NOT NULL DEFAULT '',  -- 户型（如 两室一厅）
  status        TEXT    NOT NULL DEFAULT 'vacant', -- 手工状态：selfuse 自住 / vacant 空置
  owner_name    TEXT    NOT NULL DEFAULT '',  -- 出租方（甲方）
  owner_phone   TEXT    NOT NULL DEFAULT '',
  owner_id_no   TEXT    NOT NULL DEFAULT '',
  water_card    TEXT    NOT NULL DEFAULT '',  -- 水卡号
  electric_card TEXT    NOT NULL DEFAULT '',  -- 电卡号
  gas_card      TEXT    NOT NULL DEFAULT '',  -- 燃气卡号
  rent_due_day  INTEGER NOT NULL DEFAULT 0,   -- 收租日（0=不提醒，1-31；月末无该日收敛到最后一日）
  rent_due_time TEXT    NOT NULL DEFAULT '10:00', -- 收租时刻 HH:mm（三段推送基准时刻）
  rent_notify_month TEXT NOT NULL DEFAULT '', -- 收租三段打标所属期号 YYYY-MM（换月重置）
  rent_notify_1h_at      INTEGER,             -- 前 1 小时段已发标记
  rent_notify_due_at     INTEGER,             -- 到期段已发标记
  rent_notify_nextday_at INTEGER,             -- 超期 24~48h 段已发标记
  notes         TEXT    NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);`

const houseItemsDDL = `CREATE TABLE IF NOT EXISTS house_items (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  house_id    INTEGER NOT NULL,
  category    TEXT    NOT NULL DEFAULT 'furniture', -- furniture 家具 / appliance 家电
  name        TEXT    NOT NULL,
  brand_model TEXT    NOT NULL DEFAULT '',
  qty         INTEGER NOT NULL DEFAULT 1,
  status      TEXT    NOT NULL DEFAULT 'active', -- active 在用 / broken 损坏 / removed 已移除
  in_contract INTEGER NOT NULL DEFAULT 1, -- 加入合同物品清单（2026-11-04：默认勾选）
  notes       TEXT    NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_house_items_house ON house_items(house_id);`

const houseLeasesDDL = `CREATE TABLE IF NOT EXISTS house_leases (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  house_id      INTEGER NOT NULL,
  status        TEXT    NOT NULL DEFAULT 'active', -- active 在租 / ended 已退租
  tenant_name   TEXT    NOT NULL,
  tenant_phone  TEXT    NOT NULL DEFAULT '',
  tenant_id_no  TEXT    NOT NULL DEFAULT '',
  owner_name    TEXT    NOT NULL DEFAULT '',  -- 出租方（甲方），合同甲方来源（2026-11-05 自房屋挪入）
  owner_phone   TEXT    NOT NULL DEFAULT '',
  owner_id_no   TEXT    NOT NULL DEFAULT '',
  water_reading   REAL  NOT NULL DEFAULT 0, -- 交接水余量（升）
  electric_reading REAL NOT NULL DEFAULT 0, -- 交接电余额（元）
  gas_reading     REAL  NOT NULL DEFAULT 0, -- 交接燃气余额（元）
  start_date    TEXT    NOT NULL, -- YYYY-MM-DD
  end_date      TEXT    NOT NULL, -- YYYY-MM-DD
  monthly_rent  REAL    NOT NULL DEFAULT 0, -- 元
  deposit       REAL    NOT NULL DEFAULT 0, -- 元
  payment_cycle TEXT    NOT NULL DEFAULT '', -- 押几付几（如 押一付三）
  notes         TEXT    NOT NULL DEFAULT '',
  notify_30d_at INTEGER, -- 到期提醒已发标记；改到期日重置
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_house_leases_house ON house_leases(house_id);`

// House 一条房屋记录。ActiveLease 为 LEFT JOIN 出的在租租约（无则 nil，
// 有在租租约即视为「出租中」）；DisplayStatus/ItemsCount 为查询现算展示字段。
type House struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Area    string `json:"area"`
	Layout  string `json:"layout"`
	Status  string `json:"status"` // 手工状态 selfuse/vacant（出租中不落库，现算）
	OwnerName  string `json:"owner_name"`
	OwnerPhone string `json:"owner_phone"`
	OwnerIDNo  string `json:"owner_id_no"`
	WaterCard    string `json:"water_card"`
	ElectricCard string `json:"electric_card"`
	GasCard      string `json:"gas_card"`
	RentDueDay     int    `json:"rent_due_day"`   // 收租日（0=不提醒，1-31）
	RentDueTime    string `json:"rent_due_time"`  // 收租时刻 HH:mm
	RentNotifyMonth string `json:"-"`            // 三段打标所属期号 YYYY-MM
	RentNotify1hAt      *int64 `json:"-"`
	RentNotifyDueAt     *int64 `json:"-"`
	RentNotifyNextdayAt *int64 `json:"-"`
	Notes     string `json:"notes"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	// 现算展示字段
	ActiveLease   *Lease `json:"active_lease"`
	DisplayStatus string `json:"display_status"` // rented/selfuse/vacant
	ItemsCount    int    `json:"items_count"`    // 家具电器条数（不含已移除）
}

// HouseItem 一条家具电器记录。
type HouseItem struct {
	ID         int64  `json:"id"`
	HouseID    int64  `json:"house_id"`
	Category   string `json:"category"`
	Name       string `json:"name"`
	BrandModel string `json:"brand_model"`
	Qty        int64  `json:"qty"`
	Status     string `json:"status"`
	InContract bool   `json:"in_contract"` // 加入合同物品清单（默认勾选）
	Notes      string `json:"notes"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// Lease 一条租约。RemainingDays 为查询现算字段（负 = 到期已过天数）。
type Lease struct {
	ID           int64   `json:"id"`
	HouseID      int64   `json:"house_id"`
	Status       string  `json:"status"` // active 在租 / ended 已退租
	TenantName   string  `json:"tenant_name"`
	TenantPhone  string  `json:"tenant_phone"`
	TenantIDNo   string  `json:"tenant_id_no"`
	OwnerName    string  `json:"owner_name"`
	OwnerPhone   string  `json:"owner_phone"`
	OwnerIDNo    string  `json:"owner_id_no"`
	WaterReading    float64 `json:"water_reading"`    // 升
	ElectricReading float64 `json:"electric_reading"` // 元
	GasReading      float64 `json:"gas_reading"`      // 元
	StartDate    string  `json:"start_date"`
	EndDate      string  `json:"end_date"`
	MonthlyRent  float64 `json:"monthly_rent"`
	Deposit      float64 `json:"deposit"`
	PaymentCycle string  `json:"payment_cycle"`
	Notes        string  `json:"notes"`
	Notify30dAt  *int64  `json:"notify_30d_at"`
	CreatedAt    int64   `json:"created_at"`
	UpdatedAt    int64   `json:"updated_at"`
	// 现算展示字段
	HouseName     string `json:"house_name"` // 联查（调度/合同生成用）
	RemainingDays int    `json:"remaining_days"`
}

// HouseStats 房屋总览统计（GET /houses 一次带回，服务端现算）：
// 出租中 = 有 active 租约的房屋；月总租金 = Σ active 租约月租；年总 = 月总×12
// （年化口径，一期无收款台账不做实收）。
type HouseStats struct {
	Total     int     `json:"total"`
	Rented    int     `json:"rented"`
	SelfUse   int     `json:"selfuse"`
	Vacant    int     `json:"vacant"`
	MonthRent float64 `json:"month_rent"`
	YearRent  float64 `json:"year_rent"`
}

const houseCols = `id, name, address, area, layout, status, owner_name, owner_phone, owner_id_no,
	water_card, electric_card, gas_card, rent_due_day, rent_due_time, rent_notify_month,
	rent_notify_1h_at, rent_notify_due_at, rent_notify_nextday_at, notes, created_at, updated_at`

func scanHouse(scan func(...any) error) (*House, error) {
	var h House
	if err := scan(&h.ID, &h.Name, &h.Address, &h.Area, &h.Layout, &h.Status, &h.OwnerName, &h.OwnerPhone, &h.OwnerIDNo,
		&h.WaterCard, &h.ElectricCard, &h.GasCard, &h.RentDueDay, &h.RentDueTime, &h.RentNotifyMonth,
		&h.RentNotify1hAt, &h.RentNotifyDueAt, &h.RentNotifyNextdayAt, &h.Notes, &h.CreatedAt, &h.UpdatedAt); err != nil {
		return nil, err
	}
	return &h, nil
}

// leaseCols 列清单（ListLeases/GetLease/DueExpiringLeases 三处共用，列序与
// scanLease 对齐）。
const leaseCols = `id, house_id, status, tenant_name, tenant_phone, tenant_id_no, owner_name, owner_phone, owner_id_no,
	water_reading, electric_reading, gas_reading, start_date, end_date,
	monthly_rent, deposit, payment_cycle, notes, notify_30d_at, created_at, updated_at`

func scanLease(scan func(...any) error) (*Lease, error) {
	var l Lease
	if err := scan(&l.ID, &l.HouseID, &l.Status, &l.TenantName, &l.TenantPhone, &l.TenantIDNo, &l.OwnerName, &l.OwnerPhone, &l.OwnerIDNo,
		&l.WaterReading, &l.ElectricReading, &l.GasReading, &l.StartDate, &l.EndDate,
		&l.MonthlyRent, &l.Deposit, &l.PaymentCycle, &l.Notes, &l.Notify30dAt, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, err
	}
	l.RemainingDays = leaseRemainingDays(l.EndDate, time.Now())
	return &l, nil
}

// ListHouses 全部房屋（含在租租约联查与总览统计）。同一房屋最多一条 active
// 租约（写入侧排他保证），LEFT JOIN 不会放大行；无联查行的列用 COALESCE
// 兜空值。统计同函数现算：出租中/月总租金只数 active 租约。
func (s *Store) ListHouses() ([]House, *HouseStats, error) {
	rows, err := s.db.Query(`SELECT h.id, h.name, h.address, h.area, h.layout, h.status, h.owner_name, h.owner_phone,
		h.owner_id_no, h.water_card, h.electric_card, h.gas_card, h.rent_due_day, h.rent_due_time, h.rent_notify_month,
		h.rent_notify_1h_at, h.rent_notify_due_at, h.rent_notify_nextday_at, h.notes, h.created_at, h.updated_at,
		COALESCE(l.id, 0), COALESCE(l.house_id, 0), COALESCE(l.status, ''), COALESCE(l.tenant_name, ''),
		COALESCE(l.tenant_phone, ''), COALESCE(l.tenant_id_no, ''), COALESCE(l.start_date, ''), COALESCE(l.end_date, ''),
		COALESCE(l.monthly_rent, 0), COALESCE(l.deposit, 0), COALESCE(l.payment_cycle, ''), COALESCE(l.notes, ''),
		l.notify_30d_at, COALESCE(l.created_at, 0), COALESCE(l.updated_at, 0),
		(SELECT COUNT(*) FROM house_items i WHERE i.house_id = h.id AND i.status != 'removed') AS items_count
		FROM houses h
		LEFT JOIN house_leases l ON l.house_id = h.id AND l.status = 'active'
		ORDER BY h.id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []House
	stats := &HouseStats{}
	for rows.Next() {
		h := &House{}
		l := &Lease{}
		err := rows.Scan(&h.ID, &h.Name, &h.Address, &h.Area, &h.Layout, &h.Status, &h.OwnerName, &h.OwnerPhone, &h.OwnerIDNo,
			&h.WaterCard, &h.ElectricCard, &h.GasCard, &h.RentDueDay, &h.RentDueTime, &h.RentNotifyMonth,
			&h.RentNotify1hAt, &h.RentNotifyDueAt, &h.RentNotifyNextdayAt, &h.Notes, &h.CreatedAt, &h.UpdatedAt,
			&l.ID, &l.HouseID, &l.Status, &l.TenantName, &l.TenantPhone, &l.TenantIDNo, &l.StartDate, &l.EndDate,
			&l.MonthlyRent, &l.Deposit, &l.PaymentCycle, &l.Notes, &l.Notify30dAt, &l.CreatedAt, &l.UpdatedAt,
			&h.ItemsCount)
		if err != nil {
			return nil, nil, err
		}
		if l.ID > 0 {
			l.RemainingDays = leaseRemainingDays(l.EndDate, time.Now())
			h.ActiveLease = l
			h.DisplayStatus = "rented"
			stats.Rented++
			stats.MonthRent += l.MonthlyRent
		} else {
			h.DisplayStatus = h.Status
			if h.Status == "selfuse" {
				stats.SelfUse++
			} else {
				stats.Vacant++
			}
		}
		stats.Total++
		out = append(out, *h)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	stats.YearRent = stats.MonthRent * 12
	return out, stats, nil
}

// GetHouse 单条房屋（不含家具电器与租约，详情接口另行取）。
func (s *Store) GetHouse(id int64) (*House, error) {
	h, err := scanHouse(func(dst ...any) error {
		return s.db.QueryRow(`SELECT `+houseCols+` FROM houses WHERE id = ?`, id).Scan(dst...)
	})
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("房屋不存在")
	}
	if err != nil {
		return nil, err
	}
	return h, nil
}

// validateHouse 新增/编辑共用校验与规整。
func validateHouse(h *House) error {
	h.Name = strings.TrimSpace(h.Name)
	h.Address = strings.TrimSpace(h.Address)
	h.Area = strings.TrimSpace(h.Area)
	h.Layout = strings.TrimSpace(h.Layout)
	h.OwnerName = strings.TrimSpace(h.OwnerName)
	h.OwnerPhone = strings.TrimSpace(h.OwnerPhone)
	h.OwnerIDNo = strings.TrimSpace(h.OwnerIDNo)
	h.WaterCard = strings.TrimSpace(h.WaterCard)
	h.ElectricCard = strings.TrimSpace(h.ElectricCard)
	h.GasCard = strings.TrimSpace(h.GasCard)
	h.Notes = strings.TrimSpace(h.Notes)
	if h.Name == "" {
		return fmt.Errorf("请填写房屋名称")
	}
	if h.Status != "selfuse" && h.Status != "vacant" {
		h.Status = "vacant"
	}
	if len(h.Name) > 50 || len(h.Area) > 50 || len(h.Layout) > 50 {
		return fmt.Errorf("名称/面积/户型最长 50 字")
	}
	if len(h.Address) > 200 {
		return fmt.Errorf("地址最长 200 字")
	}
	if len(h.OwnerName) > 50 || len(h.OwnerPhone) > 30 || len(h.OwnerIDNo) > 30 {
		return fmt.Errorf("出租方信息过长")
	}
	if len(h.WaterCard) > 50 || len(h.ElectricCard) > 50 || len(h.GasCard) > 50 {
		return fmt.Errorf("卡号最长 50 字")
	}
	// 收租提醒：日 0=关（1-31 合法），时刻 HH:mm 非法回落默认 10:00
	if h.RentDueDay < 0 || h.RentDueDay > 31 {
		h.RentDueDay = 0
	}
	if _, err := time.ParseInLocation("15:04", h.RentDueTime, time.Local); err != nil {
		h.RentDueTime = "10:00"
	}
	if len(h.Notes) > 500 {
		return fmt.Errorf("备注最长 500 字")
	}
	return nil
}

// CreateHouse 新建房屋。
func (s *Store) CreateHouse(h *House) error {
	if err := validateHouse(h); err != nil {
		return err
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO houses (name, address, area, layout, status, owner_name, owner_phone, owner_id_no,
		water_card, electric_card, gas_card, rent_due_day, rent_due_time, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.Name, h.Address, h.Area, h.Layout, h.Status, h.OwnerName, h.OwnerPhone, h.OwnerIDNo,
		h.WaterCard, h.ElectricCard, h.GasCard, h.RentDueDay, h.RentDueTime, h.Notes, now, now)
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	h.CreatedAt, h.UpdatedAt = now, now
	return nil
}

// UpdateHouse 编辑房屋：收租日/收租时刻有变动时清空三段打标（改收租安排后
// 重新武装，同改租期重置到期提醒的口径）。
func (s *Store) UpdateHouse(h *House) error {
	if err := validateHouse(h); err != nil {
		return err
	}
	cur, err := s.GetHouse(h.ID)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE houses SET name = ?, address = ?, area = ?, layout = ?, status = ?,
		owner_name = ?, owner_phone = ?, owner_id_no = ?, water_card = ?, electric_card = ?, gas_card = ?,
		rent_due_day = ?, rent_due_time = ?, notes = ?, updated_at = ? WHERE id = ?`,
		h.Name, h.Address, h.Area, h.Layout, h.Status, h.OwnerName, h.OwnerPhone, h.OwnerIDNo,
		h.WaterCard, h.ElectricCard, h.GasCard, h.RentDueDay, h.RentDueTime, h.Notes, time.Now().Unix(), h.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("房屋不存在")
	}
	if cur.RentDueDay != h.RentDueDay || cur.RentDueTime != h.RentDueTime {
		if err := s.ResetHouseRentFlags(h.ID, ""); err != nil {
			return err
		}
	}
	return nil
}

// DeleteHouse 删除房屋：有在租租约先退租（防误删在租台账），其余连同家具
// 电器与全部租约（含历史）一并级联删除——房屋没了这些从属数据无意义。
func (s *Store) DeleteHouse(id int64) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM house_leases WHERE house_id = ? AND status = 'active'`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("该房屋有在租租约，请先退租再删除")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM house_items WHERE house_id = ?`,
		`DELETE FROM house_leases WHERE house_id = ?`,
		`DELETE FROM houses WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListHouseItems 某房屋的家具电器（新增在先）。
func (s *Store) ListHouseItems(houseID int64) ([]HouseItem, error) {
	rows, err := s.db.Query(`SELECT id, house_id, category, name, brand_model, qty, status, in_contract, notes, created_at, updated_at
		FROM house_items WHERE house_id = ? ORDER BY id`, houseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HouseItem
	for rows.Next() {
		var i HouseItem
		if err := rows.Scan(&i.ID, &i.HouseID, &i.Category, &i.Name, &i.BrandModel, &i.Qty, &i.Status, &i.InContract, &i.Notes, &i.CreatedAt, &i.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// validateHouseItem 家具电器校验与规整；数量夹到 1..999。
func validateHouseItem(i *HouseItem) error {
	i.Name = strings.TrimSpace(i.Name)
	i.BrandModel = strings.TrimSpace(i.BrandModel)
	i.Notes = strings.TrimSpace(i.Notes)
	if i.Name == "" {
		return fmt.Errorf("请填写名称")
	}
	if i.Category != "appliance" {
		i.Category = "furniture"
	}
	if i.Status != "broken" && i.Status != "removed" {
		i.Status = "active"
	}
	if i.Qty < 1 {
		i.Qty = 1
	}
	if i.Qty > 999 {
		i.Qty = 999
	}
	_ = i.InContract // 布尔字段无非法值，入库即所见
	if len(i.Name) > 50 || len(i.BrandModel) > 100 {
		return fmt.Errorf("名称/品牌型号过长")
	}
	if len(i.Notes) > 200 {
		return fmt.Errorf("备注最长 200 字")
	}
	return nil
}

// CreateHouseItem 新建家具电器（房必须存在）。
func (s *Store) CreateHouseItem(i *HouseItem) error {
	if err := validateHouseItem(i); err != nil {
		return err
	}
	if n, err := s.houseExists(i.HouseID); err != nil {
		return err
	} else if !n {
		return fmt.Errorf("房屋不存在")
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO house_items (house_id, category, name, brand_model, qty, status, in_contract, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.HouseID, i.Category, i.Name, i.BrandModel, i.Qty, i.Status, i.InContract, i.Notes, now, now)
	if err != nil {
		return err
	}
	i.ID, _ = res.LastInsertId()
	i.CreatedAt, i.UpdatedAt = now, now
	return nil
}

// UpdateHouseItem 编辑家具电器（house_id 不可改，归属跟随创建时的房屋）。
func (s *Store) UpdateHouseItem(i *HouseItem) error {
	if err := validateHouseItem(i); err != nil {
		return err
	}
	var houseID int64
	err := s.db.QueryRow(`SELECT house_id FROM house_items WHERE id = ?`, i.ID).Scan(&houseID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("家具电器不存在")
	}
	if err != nil {
		return err
	}
	i.HouseID = houseID
	_, err = s.db.Exec(`UPDATE house_items SET category = ?, name = ?, brand_model = ?, qty = ?, status = ?, in_contract = ?, notes = ?, updated_at = ? WHERE id = ?`,
		i.Category, i.Name, i.BrandModel, i.Qty, i.Status, i.InContract, i.Notes, time.Now().Unix(), i.ID)
	return err
}

// DeleteHouseItem 删除家具电器。
func (s *Store) DeleteHouseItem(id int64) error {
	res, err := s.db.Exec(`DELETE FROM house_items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("家具电器不存在")
	}
	return nil
}

// ListLeases 某房屋的全部租约（含已退租历史，租期开始倒序）。
func (s *Store) ListLeases(houseID int64) ([]Lease, error) {
	rows, err := s.db.Query(`SELECT `+leaseCols+` FROM house_leases WHERE house_id = ? ORDER BY start_date DESC, id DESC`, houseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lease
	for rows.Next() {
		l, err := scanLease(func(dst ...any) error { return rows.Scan(dst...) })
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// GetLease 单条租约（JOIN 房屋名，调度/合同生成取数用）。
func (s *Store) GetLease(id int64) (*Lease, error) {
	var houseName string
	l, err := scanLease(func(dst ...any) error {
		return s.db.QueryRow(`SELECT l.id, l.house_id, l.status, l.tenant_name, l.tenant_phone, l.tenant_id_no,
			l.owner_name, l.owner_phone, l.owner_id_no, l.water_reading, l.electric_reading, l.gas_reading,
			l.start_date, l.end_date, l.monthly_rent, l.deposit, l.payment_cycle, l.notes, l.notify_30d_at,
			l.created_at, l.updated_at, h.name FROM house_leases l
			JOIN houses h ON h.id = l.house_id WHERE l.id = ?`, id).Scan(append(dst, &houseName)...)
	})
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("租约不存在")
	}
	if err != nil {
		return nil, err
	}
	l.HouseName = houseName
	return l, nil
}

// leaseRemainingDays 剩余天数（到期日 − 今天，本地时区自然日；负 = 已过期天数）。
// 与 membershipRemaining 同口径，独立成函避免语义混淆。
func leaseRemainingDays(endDate string, now time.Time) int {
	end, err := time.ParseInLocation("2006-01-02", endDate, now.Location())
	if err != nil {
		return 0
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return int(end.Sub(today).Hours() / 24)
}

// validateLease 租约校验与规整；租金/押金非负，租期开始 ≤ 结束。
// 状态迁移不走本入口（SetLeaseStatus 独立语义）。
func validateLease(l *Lease) error {
	l.TenantName = strings.TrimSpace(l.TenantName)
	l.TenantPhone = strings.TrimSpace(l.TenantPhone)
	l.TenantIDNo = strings.TrimSpace(l.TenantIDNo)
	l.OwnerName = strings.TrimSpace(l.OwnerName)
	l.OwnerPhone = strings.TrimSpace(l.OwnerPhone)
	l.OwnerIDNo = strings.TrimSpace(l.OwnerIDNo)
	l.PaymentCycle = strings.TrimSpace(l.PaymentCycle)
	l.Notes = strings.TrimSpace(l.Notes)
	if l.WaterReading < 0 || l.ElectricReading < 0 || l.GasReading < 0 {
		return fmt.Errorf("水电气读数不能为负数")
	}
	if l.TenantName == "" {
		return fmt.Errorf("请填写租客姓名")
	}
	if _, err := time.ParseInLocation("2006-01-02", l.StartDate, time.Local); err != nil {
		return fmt.Errorf("租期开始日期格式应为 YYYY-MM-DD")
	}
	if _, err := time.ParseInLocation("2006-01-02", l.EndDate, time.Local); err != nil {
		return fmt.Errorf("租期结束日期格式应为 YYYY-MM-DD")
	}
	if l.StartDate > l.EndDate {
		return fmt.Errorf("租期开始不能晚于结束")
	}
	if l.MonthlyRent < 0 || l.Deposit < 0 {
		return fmt.Errorf("租金/押金不能为负数")
	}
	if len(l.TenantName) > 50 || len(l.TenantPhone) > 30 || len(l.TenantIDNo) > 30 || len(l.PaymentCycle) > 20 {
		return fmt.Errorf("租客信息/付款方式过长")
	}
	if len(l.OwnerName) > 50 || len(l.OwnerPhone) > 30 || len(l.OwnerIDNo) > 30 {
		return fmt.Errorf("出租方信息过长")
	}
	if len(l.Notes) > 500 {
		return fmt.Errorf("备注最长 500 字")
	}
	return nil
}

// houseExists 房屋存在性检查。
func (s *Store) houseExists(id int64) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM houses WHERE id = ?`, id).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// hasActiveLease 同房在租租约排他检查（excludeID 用于编辑排除自身）。
func (s *Store) hasActiveLease(houseID, excludeID int64) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM house_leases WHERE house_id = ? AND status = 'active' AND id != ?`,
		houseID, excludeID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// CreateLease 新建租约：同房只允许一条在租（防两份合同同住冲突）；退租
// 记录（status=ended）不限。
func (s *Store) CreateLease(l *Lease) error {
	if err := validateLease(l); err != nil {
		return err
	}
	if n, err := s.houseExists(l.HouseID); err != nil {
		return err
	} else if !n {
		return fmt.Errorf("房屋不存在")
	}
	if n, err := s.hasActiveLease(l.HouseID, 0); err != nil {
		return err
	} else if n {
		return fmt.Errorf("该房屋已有在租租约，请先退租")
	}
	now := time.Now().Unix()
	// 排他以 NOT EXISTS 单语句兜底：预检查与 INSERT 之间的并发窗口（TOCTOU）
	// 内另一请求抢先插入时，本条落 0 行，按同口径报错
	res, err := s.db.Exec(`INSERT INTO house_leases (house_id, status, tenant_name, tenant_phone, tenant_id_no, owner_name, owner_phone, owner_id_no,
		water_reading, electric_reading, gas_reading, start_date, end_date,
		monthly_rent, deposit, payment_cycle, notes, created_at, updated_at)
		SELECT ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM house_leases WHERE house_id = ? AND status = 'active')`,
		l.HouseID, l.TenantName, l.TenantPhone, l.TenantIDNo, l.OwnerName, l.OwnerPhone, l.OwnerIDNo,
		l.WaterReading, l.ElectricReading, l.GasReading, l.StartDate, l.EndDate,
		l.MonthlyRent, l.Deposit, l.PaymentCycle, l.Notes, now, now, l.HouseID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("该房屋已有在租租约，请先退租")
	}
	l.ID, _ = res.LastInsertId()
	l.Status = "active"
	l.CreatedAt, l.UpdatedAt = now, now
	return nil
}

// UpdateLease 编辑租约：到期日变动重置 30 天提醒标记（续租改期后重新武装）。
func (s *Store) UpdateLease(l *Lease) error {
	if err := validateLease(l); err != nil {
		return err
	}
	cur, err := s.GetLease(l.ID)
	if err != nil {
		return err
	}
	if cur.EndDate != l.EndDate {
		l.Notify30dAt = nil
	} else {
		l.Notify30dAt = cur.Notify30dAt
	}
	res, err := s.db.Exec(`UPDATE house_leases SET tenant_name = ?, tenant_phone = ?, tenant_id_no = ?, owner_name = ?, owner_phone = ?, owner_id_no = ?,
		water_reading = ?, electric_reading = ?, gas_reading = ?, start_date = ?,
		end_date = ?, monthly_rent = ?, deposit = ?, payment_cycle = ?, notes = ?, notify_30d_at = ?, updated_at = ? WHERE id = ?`,
		l.TenantName, l.TenantPhone, l.TenantIDNo, l.OwnerName, l.OwnerPhone, l.OwnerIDNo,
		l.WaterReading, l.ElectricReading, l.GasReading, l.StartDate, l.EndDate,
		l.MonthlyRent, l.Deposit, l.PaymentCycle, l.Notes, l.Notify30dAt, time.Now().Unix(), l.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("租约不存在")
	}
	return nil
}

// SetLeaseStatus 退租/恢复在租（状态迁移走独立入口：编辑租约信息不动状态，
// 同房排他检查只在这一处）。恢复在租时同房排他。
func (s *Store) SetLeaseStatus(id int64, status string) error {
	if status != "active" && status != "ended" {
		return fmt.Errorf("非法状态")
	}
	if status == "active" {
		var houseID int64
		err := s.db.QueryRow(`SELECT house_id FROM house_leases WHERE id = ?`, id).Scan(&houseID)
		if err == sql.ErrNoRows {
			return fmt.Errorf("租约不存在")
		}
		if err != nil {
			return err
		}
		if n, err := s.hasActiveLease(houseID, id); err != nil {
			return err
		} else if n {
			return fmt.Errorf("该房屋已有在租租约，请先退租")
		}
		// 恢复在租同样以 NOT EXISTS 单语句兜底并发窗口
		res, err := s.db.Exec(`UPDATE house_leases SET status = 'active', updated_at = ?
			WHERE id = ? AND NOT EXISTS (SELECT 1 FROM house_leases WHERE house_id = ? AND status = 'active' AND id != ?)`,
			time.Now().Unix(), id, houseID, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("该房屋已有在租租约，请先退租")
		}
		return nil
	}
	res, err := s.db.Exec(`UPDATE house_leases SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("租约不存在")
	}
	return nil
}

// RenewLease 续租（2026-11-05）：把当前租约标记为退租留档，并按其内容复制
// 生成一条新在租租约——开始日期 = 旧租约截止日期后一天，截止默认一年，租客/
// 出租方/月租/押金/付款方式/水电气读数与备注全部沿用。单事务保证原子。
func (s *Store) RenewLease(id int64) (*Lease, error) {
	cur, err := s.GetLease(id)
	if err != nil {
		return nil, err
	}
	if cur.Status != "active" {
		return nil, fmt.Errorf("仅退租中的在租租约可续租")
	}
	start, err := time.ParseInLocation("2006-01-02", cur.EndDate, time.Local)
	if err != nil {
		return nil, fmt.Errorf("旧租约截止日期格式异常")
	}
	start = start.AddDate(0, 0, 1)
	end := start.AddDate(1, 0, 0)
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE house_leases SET status = 'ended', updated_at = ? WHERE id = ?`, now, id); err != nil {
		return nil, err
	}
	res, err := tx.Exec(`INSERT INTO house_leases (house_id, status, tenant_name, tenant_phone, tenant_id_no, owner_name, owner_phone, owner_id_no,
		water_reading, electric_reading, gas_reading, start_date, end_date,
		monthly_rent, deposit, payment_cycle, notes, created_at, updated_at)
		VALUES (?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cur.HouseID, cur.TenantName, cur.TenantPhone, cur.TenantIDNo, cur.OwnerName, cur.OwnerPhone, cur.OwnerIDNo,
		cur.WaterReading, cur.ElectricReading, cur.GasReading,
		start.Format("2006-01-02"), end.Format("2006-01-02"),
		cur.MonthlyRent, cur.Deposit, cur.PaymentCycle, cur.Notes, now, now)
	if err != nil {
		return nil, err
	}
	newID, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetLease(newID)
}

// DeleteLease 删除租约（物理删除，租约是配置型数据不进回收站）。
func (s *Store) DeleteLease(id int64) error {
	res, err := s.db.Exec(`DELETE FROM house_leases WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("租约不存在")
	}
	return nil
}

// DueExpiringLeases 30 天到期提醒调度取数：在租、到期日 ≤ 今天+30（含已过期
// 未退租的）、未发过提醒。发送成功才打标；改到期日重置标记重新武装。
func (s *Store) DueExpiringLeases() ([]Lease, error) {
	limit := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	rows, err := s.db.Query(`SELECT l.id, l.house_id, l.status, l.tenant_name, l.tenant_phone, l.tenant_id_no,
		l.owner_name, l.owner_phone, l.owner_id_no, l.water_reading, l.electric_reading, l.gas_reading,
		l.start_date, l.end_date, l.monthly_rent, l.deposit, l.payment_cycle, l.notes, l.notify_30d_at,
		l.created_at, l.updated_at, h.name FROM house_leases l
		JOIN houses h ON h.id = l.house_id
		WHERE l.status = 'active' AND l.notify_30d_at IS NULL AND l.end_date <= ?
		ORDER BY l.end_date, l.id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lease
	for rows.Next() {
		l := &Lease{}
		err := rows.Scan(&l.ID, &l.HouseID, &l.Status, &l.TenantName, &l.TenantPhone, &l.TenantIDNo, &l.OwnerName, &l.OwnerPhone, &l.OwnerIDNo,
			&l.WaterReading, &l.ElectricReading, &l.GasReading, &l.StartDate, &l.EndDate,
			&l.MonthlyRent, &l.Deposit, &l.PaymentCycle, &l.Notes, &l.Notify30dAt, &l.CreatedAt, &l.UpdatedAt, &l.HouseName)
		if err != nil {
			return nil, err
		}
		l.RemainingDays = leaseRemainingDays(l.EndDate, time.Now())
		out = append(out, *l)
	}
	return out, rows.Err()
}

// MarkLeaseNotified 记录 30 天到期提醒已发送。
func (s *Store) MarkLeaseNotified(id int64, at int64) error {
	_, err := s.db.Exec(`UPDATE house_leases SET notify_30d_at = ? WHERE id = ?`, at, id)
	return err
}

// HouseRentDue 一条待收租提醒的取数行：开启收租提醒且当前有在租租约的房屋
//（无在租租约 = 无租可收，调度跳过）。
type HouseRentDue struct {
	HouseID   int64   `json:"house_id"`
	Name      string  `json:"name"`
	DueDay    int     `json:"due_day"`
	DueTime   string  `json:"due_time"`
	Month     string  `json:"-"` // 三段打标所属期号 YYYY-MM（存量值）
	N1h       *int64  `json:"-"`
	NDue      *int64  `json:"-"`
	NNextday  *int64  `json:"-"`
	Tenant    string  `json:"tenant"`
	MonthRent float64 `json:"month_rent"`
}

// RentDueHouses 收租提醒调度取数：rent_due_day > 0 且有在租租约的房屋，带
// 三段打标与租客/月租。
func (s *Store) RentDueHouses() ([]HouseRentDue, error) {
	rows, err := s.db.Query(`SELECT h.id, h.name, h.rent_due_day, h.rent_due_time, h.rent_notify_month,
		h.rent_notify_1h_at, h.rent_notify_due_at, h.rent_notify_nextday_at, l.tenant_name, l.monthly_rent
		FROM houses h
		JOIN house_leases l ON l.house_id = h.id AND l.status = 'active'
		WHERE h.rent_due_day > 0
		ORDER BY h.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HouseRentDue
	for rows.Next() {
		var r HouseRentDue
		if err := rows.Scan(&r.HouseID, &r.Name, &r.DueDay, &r.DueTime, &r.Month,
			&r.N1h, &r.NDue, &r.NNextday, &r.Tenant, &r.MonthRent); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResetHouseRentFlags 清三段打标并登记所属期号（换月滚动/改收租安排重新武装）。
func (s *Store) ResetHouseRentFlags(id int64, month string) error {
	_, err := s.db.Exec(`UPDATE houses SET rent_notify_month = ?, rent_notify_1h_at = NULL,
		rent_notify_due_at = NULL, rent_notify_nextday_at = NULL WHERE id = ?`, month, id)
	return err
}

// MarkHouseRentNotified 记录某段收租提醒已发送（seg: 1h/due/nextday，白名单）。
func (s *Store) MarkHouseRentNotified(id int64, month, seg string, at int64) error {
	col := map[string]string{"1h": "rent_notify_1h_at", "due": "rent_notify_due_at", "nextday": "rent_notify_nextday_at"}[seg]
	if col == "" {
		return fmt.Errorf("非法提醒段 %q", seg)
	}
	_, err := s.db.Exec(`UPDATE houses SET rent_notify_month = ?, `+col+` = ? WHERE id = ?`, month, at, id)
	return err
}

// HouseRentOccurrence 计算收租提醒的当前期次（对齐事项提醒三段窗口语义）：
// 取「最近一个 ≤ now+1h 的到期时刻」——本月的期次还没到（且超出 1h 窗）或本月
// 无该日时，取上月的（上月期次的超期段可能仍在 24~48h 窗口内，如 31 日收敛）。
// 日 > 当月天数收敛到当月最后一日（同提醒事项月末语义）。返回期号 YYYY-MM。
func HouseRentOccurrence(day int, timeStr string, now time.Time) (occ time.Time, month string) {
	loc := now.Location()
	hh, mm := 10, 0
	if t, err := time.ParseInLocation("15:04", timeStr, loc); err == nil {
		hh, mm = t.Hour(), t.Minute()
	}
	build := func(y int, m time.Month) time.Time {
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day() // 当月最后一日
		d := day
		if d > last {
			d = last
		}
		return time.Date(y, m, d, hh, mm, 0, 0, loc)
	}
	cur := build(now.Year(), now.Month())
	if !cur.After(now.Add(time.Hour)) {
		return cur, cur.Format("2006-01")
	}
	// 显式减月（2026-11-06 审计批）：AddDate(0,-1,0) 在 29/30/31 日会发生
	// 月份归一化下溢（3/30 −1mo → 3/2 而非 2 月），「上月」算成本月未来时刻；
	// time.Date 对月份参数自带 1→去年 12 月的正确回绕
	prev := build(now.Year(), now.Month()-1)
	return prev, prev.Format("2006-01")
}

// house_expenses 表：房屋消耗费用清单（2026-11-04，管理员专属随房屋管理批）。
// 桌椅置换/电器置换/取暖费等一次性或周期性支出，自由命名不设类别枚举；
// spend_date 是收支统计的期间归属依据（年收入/季度收入扣减用）。
const houseExpensesDDL = `CREATE TABLE IF NOT EXISTS house_expenses (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  house_id   INTEGER NOT NULL,
  name       TEXT    NOT NULL,
  amount     REAL    NOT NULL DEFAULT 0, -- 元
  spend_date TEXT    NOT NULL, -- YYYY-MM-DD（收支期间归属）
  notes      TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_house_expenses_house ON house_expenses(house_id);
CREATE INDEX IF NOT EXISTS idx_house_expenses_date ON house_expenses(spend_date);`

// HouseExpense 一条消耗费用记录。
type HouseExpense struct {
	ID        int64   `json:"id"`
	HouseID   int64   `json:"house_id"`
	Name      string  `json:"name"`
	Amount    float64 `json:"amount"`
	SpendDate string  `json:"spend_date"`
	Notes     string  `json:"notes"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

// ListHouseExpenses 某房屋的费用清单（日期倒序，新费用在前）。
func (s *Store) ListHouseExpenses(houseID int64) ([]HouseExpense, error) {
	rows, err := s.db.Query(`SELECT id, house_id, name, amount, spend_date, notes, created_at, updated_at
		FROM house_expenses WHERE house_id = ? ORDER BY spend_date DESC, id DESC`, houseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HouseExpense
	for rows.Next() {
		var e HouseExpense
		if err := rows.Scan(&e.ID, &e.HouseID, &e.Name, &e.Amount, &e.SpendDate, &e.Notes, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// validateHouseExpense 费用校验与规整：名称必填、金额非负（封顶千万）、日期
// YYYY-MM-DD 必填。
func validateHouseExpense(e *HouseExpense) error {
	e.Name = strings.TrimSpace(e.Name)
	e.Notes = strings.TrimSpace(e.Notes)
	if e.Name == "" {
		return fmt.Errorf("请填写费用名称")
	}
	if e.Amount < 0 {
		e.Amount = 0
	}
	if e.Amount > 10000000 {
		return fmt.Errorf("金额过大")
	}
	if _, err := time.ParseInLocation("2006-01-02", e.SpendDate, time.Local); err != nil {
		return fmt.Errorf("费用日期格式应为 YYYY-MM-DD")
	}
	if len(e.Name) > 50 {
		return fmt.Errorf("费用名称最长 50 字")
	}
	if len(e.Notes) > 200 {
		return fmt.Errorf("备注最长 200 字")
	}
	return nil
}

// CreateHouseExpense 新建费用（房必须存在）。
func (s *Store) CreateHouseExpense(e *HouseExpense) error {
	if err := validateHouseExpense(e); err != nil {
		return err
	}
	if n, err := s.houseExists(e.HouseID); err != nil {
		return err
	} else if !n {
		return fmt.Errorf("房屋不存在")
	}
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO house_expenses (house_id, name, amount, spend_date, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.HouseID, e.Name, e.Amount, e.SpendDate, e.Notes, now, now)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	e.CreatedAt, e.UpdatedAt = now, now
	return nil
}

// UpdateHouseExpense 编辑费用（house_id 不可改）。
func (s *Store) UpdateHouseExpense(e *HouseExpense) error {
	if err := validateHouseExpense(e); err != nil {
		return err
	}
	var houseID int64
	err := s.db.QueryRow(`SELECT house_id FROM house_expenses WHERE id = ?`, e.ID).Scan(&houseID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("费用记录不存在")
	}
	if err != nil {
		return err
	}
	e.HouseID = houseID
	_, err = s.db.Exec(`UPDATE house_expenses SET name = ?, amount = ?, spend_date = ?, notes = ?, updated_at = ? WHERE id = ?`,
		e.Name, e.Amount, e.SpendDate, e.Notes, time.Now().Unix(), e.ID)
	return err
}

// DeleteHouseExpense 删除费用记录。
func (s *Store) DeleteHouseExpense(id int64) error {
	res, err := s.db.Exec(`DELETE FROM house_expenses WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("费用记录不存在")
	}
	return nil
}

// HouseIncome 一段时间窗的收支统计：总收入=租期在期间内覆盖天数折算的应收
// 租金（含已退租历史租约，无收款台账的应收口径）、消耗费用=期间费用记录合计、
// 实际收入=总收入−消耗费用。
type HouseIncome struct {
	Gross   float64 `json:"gross"`
	Expense float64 `json:"expense"`
	Net     float64 `json:"net"`
}

// HouseIncomeRange 计算 [from,to]（YYYY-MM-DD 闭区间）期间的房屋收支。
func (s *Store) HouseIncomeRange(from, to string) (*HouseIncome, error) {
	inc := &HouseIncome{}
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(amount), 0) FROM house_expenses WHERE spend_date >= ? AND spend_date <= ?`,
		from, to).Scan(&inc.Expense); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT start_date, end_date, monthly_rent FROM house_leases
		WHERE end_date >= ? AND start_date <= ?`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ls, le string
		var rent float64
		if err := rows.Scan(&ls, &le, &rent); err != nil {
			return nil, err
		}
		inc.Gross += leaseRentInRange(ls, le, rent, from, to)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	inc.Gross = math.Round(inc.Gross*100) / 100
	inc.Expense = math.Round(inc.Expense*100) / 100
	inc.Net = math.Round((inc.Gross-inc.Expense)*100) / 100
	return inc, nil
}

// leaseRentInRange 单条租约在 [from,to] 内的应收租金：按租期覆盖天数占当月
// 天数比例折算月租（月中起租/月中退租按天计），只统计与期间相交的部分。
func leaseRentInRange(lStart, lEnd string, rent float64, from, to string) float64 {
	if rent <= 0 || lEnd < from || lStart > to {
		return 0 // 不相交或零租金
	}
	if lStart < from {
		lStart = from
	}
	if lEnd > to {
		lEnd = to
	}
	sd, err1 := time.ParseInLocation("2006-01-02", lStart, time.Local)
	ed, err2 := time.ParseInLocation("2006-01-02", lEnd, time.Local)
	if err1 != nil || err2 != nil || ed.Before(sd) {
		return 0
	}
	total := 0.0
	cur := time.Date(sd.Year(), sd.Month(), 1, 0, 0, 0, 0, time.Local)
	for !cur.After(ed) {
		mStart := cur
		mEnd := cur.AddDate(0, 1, 0).AddDate(0, 0, -1) // 当月最后一日
		os, oe := sd, ed
		if os.Before(mStart) {
			os = mStart
		}
		if oe.After(mEnd) {
			oe = mEnd
		}
		days := int(oe.Sub(os).Hours()/24) + 1
		total += rent * float64(days) / float64(mEnd.Day())
		cur = cur.AddDate(0, 1, 0)
	}
	return total
}
