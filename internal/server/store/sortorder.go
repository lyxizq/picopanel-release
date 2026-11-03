package store

import (
	"database/sql"
	"fmt"
)

// 拖拽排序通用件（服务监控页四类模块：用量账户 / 服务监控服务器 / GitHub 仓库 / 证书）：
// 表都带 sort 列，列表 ORDER BY sort 再按原序；drop 即按新顺序写 1..n（会员到期先例）。
// 表名只来自下面的编译期常量，不接外部输入。

func (s *Store) reorderRows(table string, ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET sort = ? WHERE id = ?`, table), i+1, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReorderAiAccounts / ReorderSvcServers / ReorderGhRepos / ReorderCerts 按 ids 顺序落排序。
func (s *Store) ReorderAiAccounts(ids []int64) error { return s.reorderRows("ai_accounts", ids) }
func (s *Store) ReorderSvcServers(ids []int64) error { return s.reorderRows("svc_servers", ids) }
func (s *Store) ReorderGhRepos(ids []int64) error    { return s.reorderRows("gh_repos", ids) }
func (s *Store) ReorderCerts(ids []int64) error      { return s.reorderRows("https_certs", ids) }

// backfillSort 存量回填（幂等，只命中 sort=0 的行）：按 id 序编 1..n。子查询按全表计
// 数而不带 sort=0 谓词——自引用 UPDATE 逐行生效，带谓词会让已更新的行脱离计数造成
// 重号（会员到期回填同款坑）；首次迁移存量行全为 0，结果即稠密的创建序。
func backfillSort(db *sql.DB, table string) error {
	_, err := db.Exec(fmt.Sprintf(`UPDATE %[1]s SET sort = (
		SELECT COUNT(*) FROM %[1]s AS t2 WHERE t2.id <= %[1]s.id
	) WHERE sort = 0`, table))
	return err
}
