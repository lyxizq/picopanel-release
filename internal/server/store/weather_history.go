package store

// 天气历史（522，2026-10-09）：服务端定时采样当前天气（温度/湿度/风速/气压，
// 成功才记），7 天外降采样 5 分钟桶进 wx_samples_archive——留存口径同
// ha_samples/metrics（7 日明细 + 七日外归档），归档保留固定 90 天（天气曲线非
// 告警数据，不走参数设置保留天数），清理统一挂 server.runRetentionLoop。
// 位置键 loc="纬度,经度" 两位小数（weather.SampleLocKey 统一产出，改城市即换键、
// 旧键历史随保留期清理）。归档逐窗单事务、INSERT OR REPLACE 幂等（同 HA 口径）。

import (
	"database/sql"
	"fmt"
)

const wxSamplesDDL = `CREATE TABLE IF NOT EXISTS wx_samples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	loc TEXT NOT NULL,
	temp REAL NOT NULL,
	humidity REAL NOT NULL,
	wind REAL NOT NULL,
	pressure REAL NOT NULL,
	ts INTEGER NOT NULL,
	data TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_wx_samples_loc ON wx_samples(loc, ts);
CREATE INDEX IF NOT EXISTS idx_wx_samples_ts ON wx_samples(ts);`

const wxSamplesArchiveDDL = `CREATE TABLE IF NOT EXISTS wx_samples_archive (
	loc TEXT NOT NULL,
	temp REAL NOT NULL,
	humidity REAL NOT NULL,
	wind REAL NOT NULL,
	pressure REAL NOT NULL,
	ts INTEGER NOT NULL,
	PRIMARY KEY (loc, ts)
);
CREATE INDEX IF NOT EXISTS idx_wx_samples_archive_ts ON wx_samples_archive(ts)`

// WXSample 一次天气采样。Data=采样时的完整天气 Resp JSON（56，HA 胶囊读库用，
// 空串=升级前旧采样），历史曲线只消费四个数值字段、Data 不参与归档聚合。
type WXSample struct {
	Loc      string
	Temp     float64
	Humidity float64
	Wind     float64 // km/h
	Pressure float64 // hPa
	TS       int64
	Data     string
}

// WXPoint 历史曲线一个点（ts 秒）。
type WXPoint struct {
	TS       int64   `json:"ts"`
	Temp     float64 `json:"temp"`
	Humidity float64 `json:"humidity"`
	Wind     float64 `json:"wind"`
	Pressure float64 `json:"pressure"`
}

// RecordWXSample 落一条采样（采样循环 10 分钟一条，单条插入无需事务）。
func (s *Store) RecordWXSample(sm WXSample) error {
	_, err := s.db.Exec(`INSERT INTO wx_samples (loc, temp, humidity, wind, pressure, ts, data) VALUES (?,?,?,?,?,?,?)`,
		sm.Loc, sm.Temp, sm.Humidity, sm.Wind, sm.Pressure, sm.TS, sm.Data)
	return err
}

// LatestWXSample 某位置最新一条带 Resp JSON 的采样（56，HA 胶囊 /page/weather/latest
// 直读）：升级前旧采样 data 为空串不返回（胶囊宁可暂空一轮，≤10 分钟自愈）。
func (s *Store) LatestWXSample(loc string) (*WXSample, error) {
	var sm WXSample
	err := s.db.QueryRow(`SELECT loc, temp, humidity, wind, pressure, ts, data FROM wx_samples
		WHERE loc = ? AND data != '' ORDER BY ts DESC LIMIT 1`, loc).
		Scan(&sm.Loc, &sm.Temp, &sm.Humidity, &sm.Wind, &sm.Pressure, &sm.TS, &sm.Data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sm, nil
}

// wxDetailMinTS 该位置采样明细最早 ts（明细/归档两段查询分割点，口径同
// haDetailMinTS：归档段 [from, detailMin)、明细段 [max(from,detailMin), to]）。
func (s *Store) wxDetailMinTS(loc string) (int64, bool, error) {
	var ts sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(ts) FROM wx_samples WHERE loc = ?`, loc).Scan(&ts); err != nil {
		return 0, false, err
	}
	return ts.Int64, ts.Valid, nil
}

// WXSeries 某位置历史曲线（升序，ts 秒）：归档段与明细段各按 step 分桶平均后
// 拼接（口径同 HASeries）。step<=1 视为原样点；归档段桶宽不低于 5 分钟（已是
// 5 分钟桶可再平均）。调用方长区间传 step=range/500。
func (s *Store) WXSeries(loc string, from, to, step int64) ([]WXPoint, error) {
	detailMin, hasDetail, err := s.wxDetailMinTS(loc)
	if err != nil {
		return nil, err
	}
	var out []WXPoint
	archHi := to
	if hasDetail && detailMin-1 < archHi {
		archHi = detailMin - 1
	}
	if archHi >= from {
		archStep := step
		if archStep < archiveBucket {
			archStep = archiveBucket
		}
		rows, err := s.db.Query(`SELECT (ts/?)*?, AVG(temp), AVG(humidity), AVG(wind), AVG(pressure)
			FROM wx_samples_archive WHERE loc = ? AND ts BETWEEN ? AND ? GROUP BY 1 ORDER BY 1`,
			archStep, archStep, loc, from, archHi)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p WXPoint
			if err := rows.Scan(&p.TS, &p.Temp, &p.Humidity, &p.Wind, &p.Pressure); err != nil {
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
	q := `SELECT (ts/?)*?, AVG(temp), AVG(humidity), AVG(wind), AVG(pressure)
		FROM wx_samples WHERE loc = ? AND ts BETWEEN ? AND ? GROUP BY 1 ORDER BY 1`
	args := []any{step, step, loc, lo, to}
	if step <= 1 {
		q = `SELECT ts, temp, humidity, wind, pressure
			FROM wx_samples WHERE loc = ? AND ts BETWEEN ? AND ? ORDER BY ts`
		args = []any{loc, lo, to}
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p WXPoint
		if err := rows.Scan(&p.TS, &p.Temp, &p.Humidity, &p.Wind, &p.Pressure); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ArchiveWXSamples 把 before 之前的采样明细按 5 分钟桶降采样进 wx_samples_archive，
// 写入成功后删除同窗明细（口径同 ArchiveHASamples：逐天分窗、单窗单事务）。
// 返回归档桶数。
func (s *Store) ArchiveWXSamples(before int64) (int64, error) {
	var total int64
	for {
		var lo sql.NullInt64
		if err := s.db.QueryRow(`SELECT MIN(ts) FROM wx_samples`).Scan(&lo); err != nil {
			return total, err
		}
		if !lo.Valid || lo.Int64 >= before {
			return total, nil
		}
		hi := lo.Int64 + archiveWindow
		if hi > before {
			hi = before
		}
		n, err := s.archiveWXSamplesWindow(lo.Int64, hi)
		if err != nil {
			return total, fmt.Errorf("归档天气采样 [%d,%d): %w", lo.Int64, hi, err)
		}
		total += n
	}
}

// archiveWXSamplesWindow 归档一窗 [lo, hi)：四指标桶均值一条 SQL，随后删除同窗明细。
func (s *Store) archiveWXSamplesWindow(lo, hi int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR REPLACE INTO wx_samples_archive (loc, ts, temp, humidity, wind, pressure)
		SELECT loc, (ts/?)*?, AVG(temp), AVG(humidity), AVG(wind), AVG(pressure) FROM wx_samples
		WHERE ts >= ? AND ts < ? GROUP BY loc, (ts/?)*?`,
		archiveBucket, archiveBucket, lo, hi, archiveBucket, archiveBucket)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM wx_samples WHERE ts >= ? AND ts < ?`, lo, hi); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// DeleteWXSamplesBefore 分批删除保留期外采样明细（正常运行时归档先删 7 天边界，
// 这里只兜底保留期外残留，口径同 DeleteHASamplesBefore）。
func (s *Store) DeleteWXSamplesBefore(ts int64) (int64, error) {
	return s.deleteBatched("wx_samples", ts, 5000)
}

// DeleteWXArchiveBefore 分批删除保留期外归档桶（固定保留 90 天，调用方传 now-90d）。
func (s *Store) DeleteWXArchiveBefore(ts int64) (int64, error) {
	return s.deleteBatched("wx_samples_archive", ts, 5000)
}
