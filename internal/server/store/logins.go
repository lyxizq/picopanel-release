package store

import (
	"fmt"
	"time"
)

// loginLogKeep 登录日志保留条数：只记成功登录，超量自动裁剪最老的。
const loginLogKeep = 20

// LoginLog 一条成功登录记录（用户名 / 时间 / 来源 IP）。
type LoginLog struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	TS       int64  `json:"ts"`
	IP       string `json:"ip"`
}

// RecordLogin 记录一次成功登录：写登录日志（裁剪到最近 loginLogKeep 条），
// 并落一条「登录」站内消息供消息中心查看（文案与外部推送同口径，
// 不受推送渠道开关影响）。
func (s *Store) RecordLogin(username, ip string) error {
	now := time.Now()
	if _, err := s.db.Exec(`INSERT INTO login_log (username, ts, ip) VALUES (?, ?, ?)`,
		username, now.Unix(), ip); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM login_log WHERE id NOT IN
		(SELECT id FROM login_log ORDER BY id DESC LIMIT ?)`, loginLogKeep); err != nil {
		return err
	}
	return s.CreateMessage(&Message{
		Type:      "login",
		Title:     "登录提醒：" + username,
		Content:   fmt.Sprintf("账号 %s 于 %s 成功登录，来源 IP：%s", username, now.Format("2006-01-02 15:04:05"), ip),
		CreatedAt: now.Unix(),
	})
}

// ListLoginLog 最近登录记录，新→旧。
func (s *Store) ListLoginLog() ([]LoginLog, error) {
	rows, err := s.db.Query(`SELECT id, username, ts, ip FROM login_log
		ORDER BY id DESC LIMIT ?`, loginLogKeep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoginLog
	for rows.Next() {
		var l LoginLog
		if err := rows.Scan(&l.ID, &l.Username, &l.TS, &l.IP); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
