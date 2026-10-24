package store

// 智能家居历史数据（510-512，2026-10-09）：
//   - ha_events：设备/门窗开合翻转事件（integrations manager 抓帧与基线比对产出）
//   - ha_samples：温湿度/电量等数值传感器采样明细，7 天外降采样 5 分钟桶进
//     ha_samples_archive——留存口径同服务器监控（7 日明细 + 七日外归档，归档按
//     参数设置保留天数清理），清理统一挂 server.runRetentionLoop。
// 归档逐窗单事务、INSERT OR REPLACE 幂等（中途崩溃重跑当前窗即可）。

import (
	"database/sql"
	"fmt"
)

const haEventsDDL = `CREATE TABLE IF NOT EXISTS ha_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	entity_id TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	from_state TEXT NOT NULL DEFAULT '',
	to_state TEXT NOT NULL DEFAULT '',
	ts INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ha_events_entity ON ha_events(entity_id, ts);
CREATE INDEX IF NOT EXISTS idx_ha_events_entity_id ON ha_events(entity_id, id);
CREATE INDEX IF NOT EXISTS idx_ha_events_ts ON ha_events(ts);`

const haSamplesDDL = `CREATE TABLE IF NOT EXISTS ha_samples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	entity_id TEXT NOT NULL,
	value REAL NOT NULL,
	ts INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ha_samples_entity ON ha_samples(entity_id, ts);
CREATE INDEX IF NOT EXISTS idx_ha_samples_ts ON ha_samples(ts);`

const haSamplesArchiveDDL = `CREATE TABLE IF NOT EXISTS ha_samples_archive (
	entity_id TEXT NOT NULL,
	value REAL NOT NULL,
	ts INTEGER NOT NULL,
	PRIMARY KEY (entity_id, ts)
);
CREATE INDEX IF NOT EXISTS idx_ha_samples_archive_ts ON ha_samples_archive(ts)`

// HAEvent 一次开合翻转（开合语义归一 on/off，门窗=打开/关闭）。
type HAEvent struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	TS       int64  `json:"ts"`
}

// HASample 一帧数值采样。
type HASample struct {
	EntityID string
	Value    float64
	TS       int64
}

// RecordHAFrame 一轮抓帧的增量落库：事件+采样单事务，两个切片都空直接返回。
func (s *Store) RecordHAFrame(events []HAEvent, samples []HASample) error {
	if len(events) == 0 && len(samples) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(events) > 0 {
		ins, err := tx.Prepare(`INSERT INTO ha_events (entity_id, name, from_state, to_state, ts) VALUES (?,?,?,?,?)`)
		if err != nil {
			return err
		}
		for _, e := range events {
			if _, err := ins.Exec(e.EntityID, e.Name, e.From, e.To, e.TS); err != nil {
				ins.Close()
				return err
			}
		}
		ins.Close()
	}
	if len(samples) > 0 {
		ins, err := tx.Prepare(`INSERT INTO ha_samples (entity_id, value, ts) VALUES (?,?,?)`)
		if err != nil {
			return err
		}
		for _, sm := range samples {
			if _, err := ins.Exec(sm.EntityID, sm.Value, sm.TS); err != nil {
				ins.Close()
				return err
			}
		}
		ins.Close()
	}
	return tx.Commit()
}

// HAEntityEvents 某实体最近 limit 条翻转事件（新→旧）。排序按 id（插入序）
// 而非 ts：ts 秒级粒度且取自墙钟，NTP 回跳时按 ts 排会把同实体日志打乱
//（2026-10-12 TestObserveHAFrameConcurrent 偶发抓到）；日志语义本就该忠实
// 落库顺序，ts 只作展示。
func (s *Store) HAEntityEvents(entityID string, limit int) ([]HAEvent, error) {
	if limit < 1 {
		limit = 200
	} else if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT entity_id, name, from_state, to_state, ts
		FROM ha_events WHERE entity_id = ? ORDER BY id DESC LIMIT ?`, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HAEvent
	for rows.Next() {
		var e HAEvent
		if err := rows.Scan(&e.EntityID, &e.Name, &e.From, &e.To, &e.TS); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// haDetailMinTS 该实体采样明细最早 ts（明细/归档两段查询的分割点，口径同
// metrics.detailMinTS：归档段 [from, detailMin)、明细段 [max(from,detailMin), to]，
// 明细删除只发生在归档写入成功之后，不会两侧都缺）。
func (s *Store) haDetailMinTS(entityID string) (int64, bool, error) {
	var ts sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(ts) FROM ha_samples WHERE entity_id = ?`, entityID).Scan(&ts); err != nil {
		return 0, false, err
	}
	return ts.Int64, ts.Valid, nil
}

// HASeries 单实体曲线序列（升序 [ts, value]，ts 秒）：归档段与明细段各按 step
// 分桶平均后拼接。step<=1 视为原样点；归档段桶宽不低于 5 分钟（已是 5 分钟桶，
// 等宽桶可直接再平均，口径同 AggregateScalars）。调用方长区间传 step=range/500。
func (s *Store) HASeries(entityID string, from, to, step int64) ([][2]float64, error) {
	detailMin, hasDetail, err := s.haDetailMinTS(entityID)
	if err != nil {
		return nil, err
	}
	var out [][2]float64
	archHi := to
	if hasDetail && detailMin-1 < archHi {
		archHi = detailMin - 1
	}
	if archHi >= from {
		archStep := step
		if archStep < archiveBucket {
			archStep = archiveBucket
		}
		rows, err := s.db.Query(`SELECT (ts/?)*?, AVG(value) FROM ha_samples_archive
			WHERE entity_id = ? AND ts BETWEEN ? AND ? GROUP BY 1 ORDER BY 1`,
			archStep, archStep, entityID, from, archHi)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p [2]float64
			if err := rows.Scan(&p[0], &p[1]); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if !hasDetail {
		return out, nil
	}
	lo := from
	if detailMin > lo {
		lo = detailMin
	}
	var rows *sql.Rows
	if step > 1 {
		rows, err = s.db.Query(`SELECT (ts/?)*?, AVG(value) FROM ha_samples
			WHERE entity_id = ? AND ts BETWEEN ? AND ? GROUP BY 1 ORDER BY 1`,
			step, step, entityID, lo, to)
	} else {
		rows, err = s.db.Query(`SELECT ts, value FROM ha_samples
			WHERE entity_id = ? AND ts BETWEEN ? AND ? ORDER BY ts`, entityID, lo, to)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p [2]float64
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ArchiveHASamples 把 before 之前的采样明细按 5 分钟桶降采样进 ha_samples_archive，
// 写入成功后删除对应明细（同窗）。逐天分窗、单窗单事务；跨全部实体一条
// SET 基 SQL，不分实体循环。返回归档桶数。
func (s *Store) ArchiveHASamples(before int64) (int64, error) {
	var total int64
	for {
		var lo sql.NullInt64
		if err := s.db.QueryRow(`SELECT MIN(ts) FROM ha_samples`).Scan(&lo); err != nil {
			return total, err
		}
		if !lo.Valid || lo.Int64 >= before {
			return total, nil
		}
		hi := lo.Int64 + archiveWindow
		if hi > before {
			hi = before
		}
		n, err := s.archiveHASamplesWindow(lo.Int64, hi)
		if err != nil {
			return total, fmt.Errorf("归档 HA 采样 [%d,%d): %w", lo.Int64, hi, err)
		}
		total += n
	}
}

// archiveHASamplesWindow 归档一窗 [lo, hi)：桶均值一条 SQL，随后删除同窗明细。
func (s *Store) archiveHASamplesWindow(lo, hi int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR REPLACE INTO ha_samples_archive (entity_id, ts, value)
		SELECT entity_id, (ts/?)*?, AVG(value) FROM ha_samples
		WHERE ts >= ? AND ts < ? GROUP BY entity_id, (ts/?)*?`,
		archiveBucket, archiveBucket, lo, hi, archiveBucket, archiveBucket)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM ha_samples WHERE ts >= ? AND ts < ?`, lo, hi); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// DeleteHAEventsBefore 分批删除保留期外事件清单。
func (s *Store) DeleteHAEventsBefore(ts int64) (int64, error) {
	return s.deleteBatched("ha_events", ts, 5000)
}

// DeleteHASamplesBefore 分批删除保留期外采样明细（正常运行时归档先删 7 天边界，
// 这里只兜底保留期外残留，口径同 DeleteMetricsBefore）。
func (s *Store) DeleteHASamplesBefore(ts int64) (int64, error) {
	return s.deleteBatched("ha_samples", ts, 5000)
}

// DeleteHAArchiveBefore 分批删除保留期外归档桶。
func (s *Store) DeleteHAArchiveBefore(ts int64) (int64, error) {
	return s.deleteBatched("ha_samples_archive", ts, 5000)
}
