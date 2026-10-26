package store

import (
	"database/sql"
	"fmt"
	"log"
)

// cleanupLegacySchema 清理已下线功能在存量库里留下的表、列与数据（2026-10-23 用户
// 指定「历史遗留一律清理」，覆盖证书续签下线时「不迁移不清理」的口径）。每项幂等：
// 对象不存在即跳过，升级后首次启动真正执行，之后每次启动都是空操作。
//
//   - 证书续签（TODO 25 下线）：cert_renewals 表、https_certs 续签四列、acme_* 设置键
//   - SMTP 邮件提醒渠道（2026-09-28 换 WxPusher）：mail_config/alert_email_on/cert_email_on
//   - 电信话费接入（491 全量移除）：ai_accounts.auth_token/device_id 列（device_id 只在
//     未提交的 490 批工作区建过，发布版从未带出，列入纯属兜底）、provider='telecom'
//     账户行与引用它的总览/主页数据监控格子（级联口径同 DeleteAiAccount）
//   - 巡检报告每日定时推送（2026-10-26 并入个人助理每日早报）：inspect_push_on/
//     inspect_push_time/inspect_last_push 设置键
//
// 视频下载模块（TODO 4）的 video_dl_tasks 表在 ddl 串里 DROP、video_dl_cookies* 键在
// OpenWithKey 里单独 DELETE，不在此重复。
func cleanupLegacySchema(db *sql.DB) error {
	if _, err := db.Exec(`DROP TABLE IF EXISTS cert_renewals`); err != nil {
		return fmt.Errorf("删除遗留表 cert_renewals: %w", err)
	}
	// DROP COLUMN（SQLite ≥3.35）要求列不带 UNIQUE/索引/外键引用，这六列都是普通
	// 数据列；两张表都是配置级小表，原地重写无感
	for _, c := range []struct{ table, col string }{
		{"https_certs", "auto_renew"},
		{"https_certs", "renew_on"},
		{"https_certs", "renew_error"},
		{"https_certs", "challenge"},
		{"ai_accounts", "auth_token"},
		{"ai_accounts", "device_id"},
		{"page_settings", "show_metrics"},  // 整区显隐开关（误做，已改逐项 hidden 列）
		{"page_settings", "show_services"}, // 同上
		{"page_settings", "show_links"},    // 同上
	} {
		if !hasColumn(db, c.table, c.col) {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE ` + c.table + ` DROP COLUMN ` + c.col); err != nil {
			return fmt.Errorf("删除遗留列 %s.%s: %w", c.table, c.col, err)
		}
		log.Printf("已删除遗留列 %s.%s", c.table, c.col)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []struct{ what, sql string }{
		{"电信账户总览格子", `DELETE FROM dashboard_metrics WHERE ai_id IN (SELECT id FROM ai_accounts WHERE provider = 'telecom')`},
		{"电信账户主页格子", `DELETE FROM page_metrics WHERE ai_id IN (SELECT id FROM ai_accounts WHERE provider = 'telecom')`},
		{"电信账户", `DELETE FROM ai_accounts WHERE provider = 'telecom'`},
		{"设置键", `DELETE FROM settings WHERE key IN ('acme_account_key', 'acme_email', 'acme_http_port', 'mail_config', 'alert_email_on', 'cert_email_on', 'inspect_push_on', 'inspect_push_time', 'inspect_last_push')`},
	} {
		res, err := tx.Exec(q.sql)
		if err != nil {
			return fmt.Errorf("清理遗留%s: %w", q.what, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("已清理遗留%s %d 行", q.what, n)
		}
	}
	return tx.Commit()
}

// hasColumn 判断表是否含指定列（PRAGMA table_info；表不存在视为无列）。
func hasColumn(db *sql.DB, table, col string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
}
