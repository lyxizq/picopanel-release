package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// med_persons 表：用药提醒的家庭成员（分账号，2026-11-02 用户指定参考 Apple Health
// 药物做成多账号）。一个人员名下挂多味药（medications.person_id），删人员级联删
// 其药物与剂量台账；color 为标识色（前端色点/时间轴 chip 用），sort 拖拽排序。
// user_id：绑定的登录账号（2026-11-03 家庭成员口径：受限用户首次进用药页自动
// 建人绑定，只见自己；0=未绑定，管理员代管的家庭成员）。
const medPersonsDDL = `CREATE TABLE IF NOT EXISTS med_persons (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL,
  user_id    INTEGER NOT NULL DEFAULT 0,
  color      TEXT    NOT NULL DEFAULT '',
  sort       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`

// MedPerson 一名家庭成员。Color 空时前端按 id 落调色板兜底。
type MedPerson struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	UserID    int64  `json:"user_id"`
	Color     string `json:"color"`
	Sort      int64  `json:"sort"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	MedCount  int64  `json:"med_count"` // 展示用：名下药物数（List 时联查）
}

const medPersonCols = `SELECT p.id, p.name, p.user_id, p.color, p.sort, p.created_at, p.updated_at`

// MedPersonPalette 人员标识色板（前端色点同源；后端建人兜底用，取模轮转）。
var MedPersonPalette = []string{"#409eff", "#67c23a", "#e6a23c", "#f56c6c", "#9d6cf0", "#00b8a9", "#f0709a", "#7f8c4d"}

// ListMedPersons 全部家庭成员（sort 升序，联查名下药物数）。
func (s *Store) ListMedPersons() ([]MedPerson, error) {
	rows, err := s.db.Query(medPersonCols + `,
		(SELECT COUNT(*) FROM medications m WHERE m.person_id = p.id) AS med_count
		FROM med_persons p ORDER BY p.sort, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MedPerson
	for rows.Next() {
		var p MedPerson
		if err := rows.Scan(&p.ID, &p.Name, &p.UserID, &p.Color, &p.Sort, &p.CreatedAt, &p.UpdatedAt, &p.MedCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetMedPerson 单个家庭成员。
func (s *Store) GetMedPerson(id int64) (*MedPerson, error) {
	var p MedPerson
	err := s.db.QueryRow(medPersonCols+` FROM med_persons p WHERE p.id = ?`, id).
		Scan(&p.ID, &p.Name, &p.UserID, &p.Color, &p.Sort, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("家庭成员不存在")
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// MedPersonByUser 取登录账号（uid）绑定的家庭成员，没有则自动创建（2026-11-03
// 家庭成员口径：受限用户首次进用药页即建人绑定，姓名=用户名、色板轮转），
// 之后该用户在用药模块的 person 维度恒定为此人。管理员（uid=0）不绑定任何
// 人员、看全部（代管口径），不应调用本函数。
func (s *Store) MedPersonByUser(uid int64) (*MedPerson, error) {
	var p MedPerson
	err := s.db.QueryRow(medPersonCols+` FROM med_persons p WHERE p.user_id = ?`, uid).
		Scan(&p.ID, &p.Name, &p.UserID, &p.Color, &p.Sort, &p.CreatedAt, &p.UpdatedAt)
	if err == nil {
		return &p, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	u, err := s.UserByID(uid)
	if err != nil || u == nil {
		return nil, fmt.Errorf("登录账号不存在")
	}
	np := &MedPerson{Name: u.Username, UserID: uid} // Color 空串 → CreateMedPerson 按现有人数轮转色板
	if err := s.CreateMedPerson(np); err != nil {
		return nil, err
	}
	return np, nil
}

// validateMedPerson 新增/编辑共用校验与规整：姓名必填 ≤20 字；色值空=后端按
// id 落色板兜底（Create 时），非空须为 #RRGGBB。
func validateMedPerson(p *MedPerson) error {
	p.Name = strings.TrimSpace(p.Name)
	p.Color = strings.TrimSpace(p.Color)
	if p.Name == "" {
		return fmt.Errorf("请填写姓名")
	}
	if len([]rune(p.Name)) > 20 {
		return fmt.Errorf("姓名最长 20 字")
	}
	if p.Color != "" && !isHexColor(p.Color) {
		return fmt.Errorf("颜色格式应为 #RRGGBB")
	}
	return nil
}

// isHexColor #RRGGBB 格式校验（人员颜色与药物剂型色共用）。
func isHexColor(c string) bool {
	if len(c) != 7 || c[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		if !((c[i] >= '0' && c[i] <= '9') || (c[i] >= 'a' && c[i] <= 'f') || (c[i] >= 'A' && c[i] <= 'F')) {
			return false
		}
	}
	return true
}

// CreateMedPerson 新建家庭成员（sort 追加尾部；色值空则按现有人数取色板轮转）。
func (s *Store) CreateMedPerson(p *MedPerson) error {
	if err := validateMedPerson(p); err != nil {
		return err
	}
	now := time.Now().Unix()
	var sort int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(sort), 0) + 1 FROM med_persons`).Scan(&sort); err != nil {
		return err
	}
	if p.Color == "" {
		var n int64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM med_persons`).Scan(&n); err != nil {
			return err
		}
		p.Color = MedPersonPalette[int(n)%len(MedPersonPalette)]
	}
	res, err := s.db.Exec(`INSERT INTO med_persons (name, user_id, color, sort, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.Name, p.UserID, p.Color, sort, now, now)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	p.Sort = sort
	p.CreatedAt, p.UpdatedAt = now, now
	return nil
}

// UpdateMedPerson 编辑家庭成员。
func (s *Store) UpdateMedPerson(p *MedPerson) error {
	if err := validateMedPerson(p); err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE med_persons SET name = ?, color = ?, updated_at = ? WHERE id = ?`,
		p.Name, p.Color, time.Now().Unix(), p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("家庭成员不存在")
	}
	return nil
}

// DeleteMedPerson 删除家庭成员并级联删其药物与剂量台账（配置型数据不进回收站，
// 事务保证三表原子）。
func (s *Store) DeleteMedPerson(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM med_logs WHERE med_id IN (SELECT id FROM medications WHERE person_id = ?)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM medications WHERE person_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM med_persons WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("家庭成员不存在")
	}
	return tx.Commit()
}

// ReorderMedPersons 按传入 id 顺序写 sort 1..n（人员管理拖拽/移动排序保存，
// memberships 同款口径）。
func (s *Store) ReorderMedPersons(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE med_persons SET sort = ? WHERE id = ?`, i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
