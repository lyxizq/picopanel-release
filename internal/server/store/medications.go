package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// medications 表：用药提醒的药物（2026-11-02 用户指定，参考 Apple Health 药物）。
// kind 四种时间表：daily 每天固定时刻 / weekdays 每周几 / interval 每 N 天（以
// start_date 锚定）/ prn 按需不排程；times 为时刻 JSON 数组（≤6 个 HH:MM），
// weekdays 为 ISO 星期 JSON 数组（1=周一..7=周日）。库存 stock_qty 可空（空=不
// 启用），服用自动扣 dose_amount，≤stock_alert 推补货（stock_notified_at 打标，
// 手动改库存即重置）。archived_at 非空=归档停药（停排程停推送，历史保留）。
const medicationsDDL = `CREATE TABLE IF NOT EXISTS medications (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  person_id         INTEGER NOT NULL,
  name              TEXT    NOT NULL,
  note              TEXT    NOT NULL DEFAULT '',
  form              TEXT    NOT NULL DEFAULT 'tablet',
  dose_amount       REAL    NOT NULL DEFAULT 1,
  dose_unit         TEXT    NOT NULL DEFAULT '',
  kind              TEXT    NOT NULL DEFAULT 'daily',
  times             TEXT    NOT NULL DEFAULT '[]',
  weekdays          TEXT    NOT NULL DEFAULT '[]',
  interval_days     INTEGER NOT NULL DEFAULT 2,
  start_date        TEXT    NOT NULL DEFAULT '',
  duration_days     INTEGER NOT NULL DEFAULT 0,
  stock_qty         REAL,
  stock_alert       REAL    NOT NULL DEFAULT 0,
  stock_notified_at INTEGER,
  sort              INTEGER NOT NULL DEFAULT 0,
  archived_at       INTEGER,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_medications_person ON medications(person_id);`

// MedForms 剂型白名单（前端按 key 渲染图标与文案，后端只管白名单）。
var MedForms = map[string]bool{
	"tablet": true, "capsule": true, "softgel": true, "liquid": true,
	"injection": true, "drops": true, "inhaler": true, "topical": true,
}

// MedKinds 时间表形态白名单。
var MedKinds = map[string]bool{"daily": true, "weekdays": true, "interval": true, "prn": true}

// Medication 一味药。Times/Weekdays 为解析后的数组（列内存 JSON 原文）；
// PersonName/PersonColor 为联查展示字段；StockOpen = stock_qty 非空的语义化开关。
type Medication struct {
	ID          int64    `json:"id"`
	PersonID    int64    `json:"person_id"`
	Name        string   `json:"name"`
	Note        string   `json:"note"`
	Form        string   `json:"form"`
	DoseAmount  float64  `json:"dose_amount"`
	DoseUnit    string   `json:"dose_unit"`
	Kind        string   `json:"kind"`
	Times       []string `json:"times"`
	Weekdays    []int    `json:"weekdays"`
	IntervalDays int     `json:"interval_days"`
	StartDate   string   `json:"start_date"`
	DurationDays int     `json:"duration_days"`
	StockQty    *float64 `json:"stock_qty"`
	StockAlert  float64  `json:"stock_alert"`
	StockNotifiedAt *int64 `json:"stock_notified_at"`
	Sort        int64    `json:"sort"`
	ArchivedAt  *int64   `json:"archived_at"`
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
	// 联查展示字段
	PersonName  string `json:"person_name"`
	PersonColor string `json:"person_color"`
	PersonUserID int64 `json:"-"` // 人员绑定的登录账号（2026-11-03：站内消息归属用，0=未绑定/管理员空间）
}

// StockOpen 库存是否启用（stock_qty 非空）。
func (m *Medication) StockOpen() bool { return m.StockQty != nil }

const medicationCols = `SELECT m.id, m.person_id, m.name, m.note, m.form, m.dose_amount, m.dose_unit,
	m.kind, m.times, m.weekdays, m.interval_days, m.start_date, m.duration_days,
	m.stock_qty, m.stock_alert, m.stock_notified_at, m.sort, m.archived_at, m.created_at, m.updated_at,
	p.name, p.color, p.user_id`

func scanMedication(scan func(...any) error) (*Medication, error) {
	var m Medication
	var timesRaw, weekdaysRaw string
	if err := scan(&m.ID, &m.PersonID, &m.Name, &m.Note, &m.Form, &m.DoseAmount, &m.DoseUnit,
		&m.Kind, &timesRaw, &weekdaysRaw, &m.IntervalDays, &m.StartDate, &m.DurationDays,
		&m.StockQty, &m.StockAlert, &m.StockNotifiedAt, &m.Sort, &m.ArchivedAt, &m.CreatedAt, &m.UpdatedAt,
		&m.PersonName, &m.PersonColor, &m.PersonUserID); err != nil {
		return nil, err
	}
	m.Times = parseMedTimes(timesRaw)
	m.Weekdays = parseMedWeekdays(weekdaysRaw)
	return &m, nil
}

// parseMedTimes 解析时刻 JSON 数组，坏值静默过滤并排序去重（历史脏数据不炸查询）。
func parseMedTimes(raw string) []string {
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	set := map[string]bool{}
	var out []string
	for _, t := range arr {
		if isValidHHMM(t) && !set[t] {
			set[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// parseMedWeekdays 解析 ISO 星期 JSON 数组（1..7），越界/重复滤掉。
func parseMedWeekdays(raw string) []int {
	var arr []int
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	set := map[int]bool{}
	var out []int
	for _, w := range arr {
		if w >= 1 && w <= 7 && !set[w] {
			set[w] = true
			out = append(out, w)
		}
	}
	sort.Ints(out)
	return out
}

// isValidHHMM 时刻格式与钟点合法性。
func isValidHHMM(t string) bool {
	if len(t) != 5 || t[2] != ':' {
		return false
	}
	h, mi := t[0:2], t[3:5]
	for _, part := range []string{h, mi} {
		if len(part) != 2 || part[0] < '0' || part[0] > '9' || part[1] < '0' || part[1] > '9' {
			return false
		}
	}
	return h <= "23" && mi <= "59"
}

// weekdayOf ISO 星期（1=周一..7=周日）。
func weekdayOf(day string) int {
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return 0
	}
	w := int(t.Weekday())
	if w == 0 {
		w = 7
	}
	return w
}

// medDosesOn 药物在某自然日的计划时刻（滤归档/启用窗口/星期/间隔锚点）；
// prn 与未到开始的日期返回空。start_date 空=当天起效（interval 建药时前端默认
// 今天必填，此兜底防历史数据把锚点丢成空）。
func medDosesOn(m *Medication, day string) []string {
	if m.ArchivedAt != nil || m.Kind == "prn" || len(m.Times) == 0 {
		return nil
	}
	start := m.StartDate
	if start == "" {
		start = day
	}
	if day < start {
		return nil
	}
	if m.DurationDays > 0 {
		sd, e1 := time.ParseInLocation("2006-01-02", start, time.Local)
		dd, e2 := time.ParseInLocation("2006-01-02", day, time.Local)
		if e1 != nil || e2 != nil {
			return nil
		}
		if int(dd.Sub(sd).Hours()/24) >= m.DurationDays {
			return nil // 连续 N 天已服满，停排程（标「已结束」，非归档）
		}
	}
	switch m.Kind {
	case "daily":
		return m.Times
	case "weekdays":
		wd := weekdayOf(day)
		for _, w := range m.Weekdays {
			if w == wd {
				return m.Times
			}
		}
		return nil
	case "interval":
		if m.IntervalDays < 2 {
			return nil
		}
		sd, e1 := time.ParseInLocation("2006-01-02", start, time.Local)
		dd, e2 := time.ParseInLocation("2006-01-02", day, time.Local)
		if e1 != nil || e2 != nil {
			return nil
		}
		if diff := int(dd.Sub(sd).Hours() / 24); diff >= 0 && diff%m.IntervalDays == 0 {
			return m.Times
		}
		return nil
	}
	return nil
}

// ListMedications 全部药物（人员序 + 药序；排除归档用 ArchivedFilter，列表页
// 归档区单独拉）。personID > 0 时只看该人员。
func (s *Store) ListMedications(personID int64, withArchived bool) ([]Medication, error) {
	q := medicationCols + ` FROM medications m JOIN med_persons p ON p.id = m.person_id WHERE 1=1`
	if personID > 0 {
		q += fmt.Sprintf(` AND m.person_id = %d`, personID)
	}
	if !withArchived {
		q += ` AND m.archived_at IS NULL`
	}
	q += ` ORDER BY p.sort, p.id, m.sort, m.id`
	return s.queryMedications(q)
}

func (s *Store) queryMedications(q string, args ...any) ([]Medication, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Medication
	for rows.Next() {
		m, err := scanMedication(func(dst ...any) error { return rows.Scan(dst...) })
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// GetMedication 单味药（带人员联查）。
func (s *Store) GetMedication(id int64) (*Medication, error) {
	list, err := s.queryMedications(medicationCols+` FROM medications m JOIN med_persons p ON p.id = m.person_id WHERE m.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("药物不存在")
	}
	return &list[0], nil
}

// validateMedication 新增/编辑共用校验与规整（字段口径见表注释）。
func (s *Store) validateMedication(m *Medication) error {
	m.Name = strings.TrimSpace(m.Name)
	m.Note = strings.TrimSpace(m.Note)
	m.DoseUnit = strings.TrimSpace(m.DoseUnit)
	m.StartDate = strings.TrimSpace(m.StartDate)
	if p, err := s.GetMedPerson(m.PersonID); err != nil || p == nil {
		return fmt.Errorf("请选择家庭成员")
	}
	if m.Name == "" {
		return fmt.Errorf("请填写药名")
	}
	if len([]rune(m.Name)) > 50 || len([]rune(m.Note)) > 200 || len([]rune(m.DoseUnit)) > 10 {
		return fmt.Errorf("药名/备注/单位超长（50/200/10 字）")
	}
	if !MedForms[m.Form] {
		m.Form = "tablet"
	}
	if !MedKinds[m.Kind] {
		return fmt.Errorf("时间表形态不合法")
	}
	m.Times = normalizeMedTimes(m.Times)
	m.Weekdays = normalizeMedWeekdays(m.Weekdays)
	switch m.Kind {
	case "prn":
		m.Times, m.Weekdays = nil, nil
	case "weekdays":
		if len(m.Times) == 0 {
			return fmt.Errorf("请至少填写一个服药时间")
		}
		if len(m.Weekdays) == 0 {
			return fmt.Errorf("请至少选择一个星期")
		}
	default: // daily / interval
		if len(m.Times) == 0 {
			return fmt.Errorf("请至少填写一个服药时间")
		}
		m.Weekdays = nil
	}
	if m.Kind == "interval" {
		if m.IntervalDays < 2 || m.IntervalDays > 30 {
			return fmt.Errorf("间隔天数应为 2~30")
		}
		if _, err := time.ParseInLocation("2006-01-02", m.StartDate, time.Local); err != nil {
			return fmt.Errorf("每隔 N 天需有效开始日期（YYYY-MM-DD）作为锚点")
		}
	} else {
		m.IntervalDays = 2
		if m.StartDate != "" {
			if _, err := time.ParseInLocation("2006-01-02", m.StartDate, time.Local); err != nil {
				return fmt.Errorf("开始日期格式应为 YYYY-MM-DD")
			}
		}
	}
	if m.DurationDays < 0 || m.DurationDays > 3650 {
		m.DurationDays = 0
	}
	if m.DoseAmount <= 0 {
		m.DoseAmount = 1
	}
	if m.DoseAmount > 1000 {
		m.DoseAmount = 1000
	}
	if m.StockQty != nil {
		if *m.StockQty < 0 {
			q := 0.0
			m.StockQty = &q
		}
		if m.StockAlert < 0 {
			m.StockAlert = 0
		}
	} else {
		m.StockAlert = 0
	}
	return nil
}

// normalizeMedTimes 时刻数组规整：只留合法 HH:MM、排序去重、封顶 6 个。
func normalizeMedTimes(in []string) []string {
	set := map[string]bool{}
	var out []string
	for _, t := range in {
		t = strings.TrimSpace(t)
		if isValidHHMM(t) && !set[t] {
			set[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

// normalizeMedWeekdays 星期数组规整：只留 1..7、排序去重。
func normalizeMedWeekdays(in []int) []int {
	set := map[int]bool{}
	var out []int
	for _, w := range in {
		if w >= 1 && w <= 7 && !set[w] {
			set[w] = true
			out = append(out, w)
		}
	}
	sort.Ints(out)
	return out
}

func medTimesJSON(times []string) string {
	if times == nil {
		times = []string{}
	}
	b, _ := json.Marshal(times)
	return string(b)
}

func medWeekdaysJSON(days []int) string {
	if days == nil {
		days = []int{}
	}
	b, _ := json.Marshal(days)
	return string(b)
}

// CreateMedication 新建药物。
func (s *Store) CreateMedication(m *Medication) error {
	if err := s.validateMedication(m); err != nil {
		return err
	}
	now := time.Now().Unix()
	var sort int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(sort), 0) + 1 FROM medications`).Scan(&sort); err != nil {
		return err
	}
	res, err := s.db.Exec(`INSERT INTO medications (person_id, name, note, form, dose_amount, dose_unit,
		kind, times, weekdays, interval_days, start_date, duration_days, stock_qty, stock_alert,
		sort, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.PersonID, m.Name, m.Note, m.Form, m.DoseAmount, m.DoseUnit,
		m.Kind, medTimesJSON(m.Times), medWeekdaysJSON(m.Weekdays), m.IntervalDays, m.StartDate, m.DurationDays,
		m.StockQty, m.StockAlert, sort, now, now)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.Sort = sort
	m.CreatedAt, m.UpdatedAt = now, now
	return nil
}

// UpdateMedication 编辑药物；时间表相关字段（形态/时刻/星期/间隔/锚点/时长/
// 归档）有变动时清掉今天及以后的 pending 剂量行，由日结按新表重排（已打卡的
// taken/skipped/missed 历史保留不动）。sort 列不动（沿用现有行的手动排序，
// 编辑请求不带 sort、落 m.Sort=0 会把顺序清零——2026-11-03 修，学
// UpdateMedPerson 不碰 sort 的口径）。
func (s *Store) UpdateMedication(m *Medication) error {
	if err := s.validateMedication(m); err != nil {
		return err
	}
	cur, err := s.GetMedication(m.ID)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	if medScheduleChanged(cur, m) {
		today := time.Now().Format("2006-01-02")
		if _, err := s.db.Exec(`DELETE FROM med_logs WHERE med_id = ? AND status = 'pending' AND day >= ?`, m.ID, today); err != nil {
			return err
		}
	}
	res, err := s.db.Exec(`UPDATE medications SET person_id = ?, name = ?, note = ?, form = ?, dose_amount = ?,
		dose_unit = ?, kind = ?, times = ?, weekdays = ?, interval_days = ?, start_date = ?, duration_days = ?,
		stock_qty = ?, stock_alert = ?, sort = ?, archived_at = ?, updated_at = ? WHERE id = ?`,
		m.PersonID, m.Name, m.Note, m.Form, m.DoseAmount, m.DoseUnit,
		m.Kind, medTimesJSON(m.Times), medWeekdaysJSON(m.Weekdays), m.IntervalDays, m.StartDate, m.DurationDays,
		m.StockQty, m.StockAlert, cur.Sort, m.ArchivedAt, now, m.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("药物不存在")
	}
	return nil
}

// medScheduleChanged 判定时间表相关字段是否变动（含 归档/换人——换人不动历史
// 台账，但清 pending 无害且口径统一）。
func medScheduleChanged(a, b *Medication) bool {
	return a.Kind != b.Kind || a.StartDate != b.StartDate ||
		strings.Join(a.Times, ",") != strings.Join(b.Times, ",") ||
		fmt.Sprint(a.Weekdays) != fmt.Sprint(b.Weekdays) ||
		a.IntervalDays != b.IntervalDays || a.DurationDays != b.DurationDays ||
		(a.ArchivedAt == nil) != (b.ArchivedAt == nil) || a.PersonID != b.PersonID
}

// DeleteMedication 删除药物并级联删其剂量台账（事务原子）。
func (s *Store) DeleteMedication(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM med_logs WHERE med_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM medications WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("药物不存在")
	}
	return tx.Commit()
}

// ReorderMedications 按传入 id 顺序写 sort 1..n（药物列表拖拽排序保存）。
func (s *Store) ReorderMedications(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE medications SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateMedStock 手动改库存（补货后用户自己改数字）：改数即重置补货提醒标记，
// 库存回警戒线上时能重新武装、再跌破会再提醒。
func (s *Store) UpdateMedStock(id int64, qty float64) error {
	if qty < 0 {
		qty = 0
	}
	res, err := s.db.Exec(`UPDATE medications SET stock_qty = ?, stock_notified_at = NULL, updated_at = ? WHERE id = ?`,
		qty, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("药物不存在")
	}
	return nil
}
