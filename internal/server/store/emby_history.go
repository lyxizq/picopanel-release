package store

import "strconv"

// Emby/Jellyfin 播放记录（528，2026-10-11）：integrations manager 30s 抓帧
// 差分观测 /Sessions 产出——播放开始立即落行（ended_at=0 表示进行中），会话
// 消失回填结束时间与最后观测位置。永久留存不随保留天数清理（530 用户指定，
// 作为统计报表的长期数据源；一次播放一行，体积无压力；清单显示层按 2 小时
// 窗口归并同片多次播放——546，存储不分并）。

const embyEventsDDL = `CREATE TABLE IF NOT EXISTS emby_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	integration_id INTEGER NOT NULL,
	user TEXT NOT NULL DEFAULT '',
	device TEXT NOT NULL DEFAULT '',
	item_id TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	sub TEXT NOT NULL DEFAULT '',
	item_type TEXT NOT NULL DEFAULT '',
	season INTEGER NOT NULL DEFAULT 0,
	episode INTEGER NOT NULL DEFAULT 0,
	started_at INTEGER NOT NULL,
	ended_at INTEGER NOT NULL DEFAULT 0,
	pos_ticks INTEGER NOT NULL DEFAULT 0,
	runtime_ticks INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_emby_events_src ON emby_events(integration_id, id);
CREATE INDEX IF NOT EXISTS idx_emby_events_src_time ON emby_events(integration_id, started_at, id);
CREATE INDEX IF NOT EXISTS idx_emby_events_user ON emby_events(integration_id, user);
CREATE INDEX IF NOT EXISTS idx_emby_events_type ON emby_events(integration_id, item_type)`

// EmbyEvent 一次播放（ended_at=0 表示进行中）。
type EmbyEvent struct {
	ID            int64  `json:"id"`
	IntegrationID int64  `json:"-"`
	User          string `json:"user"`
	Device        string `json:"device"`
	ItemID        string `json:"item_id"`
	Title         string `json:"title"`
	Sub           string `json:"sub"`
	Type          string `json:"type"`
	Season        int64  `json:"season"`  // 季号（仅剧集；0=无/旧数据，前端不显示）
	Episode       int64  `json:"episode"` // 集号
	StartedAt     int64  `json:"started_at"`
	EndedAt       int64  `json:"ended_at"`
	PosTicks      int64  `json:"pos_ticks"`
	RuntimeTicks  int64  `json:"runtime_ticks"`
}

// InsertEmbyStart 记一次播放开始（pos_ticks 带初始位置——恢复播放场景开
// 始就不是 0），返回行 ID（结束时回填）。
func (s *Store) InsertEmbyStart(e EmbyEvent) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO emby_events
		(integration_id, user, device, item_id, title, sub, item_type, season, episode, started_at, pos_ticks, runtime_ticks)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.IntegrationID, e.User, e.Device, e.ItemID, e.Title, e.Sub, e.Type, e.Season, e.Episode, e.StartedAt, e.PosTicks, e.RuntimeTicks)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateEmbyPos 回写进行中播放的最新位置（每帧跟进，534；开始时 RunTimeTicks
// 缺失为 0 的行，后到非 0 值一并补上——进度条分母才对）。
func (s *Store) UpdateEmbyPos(id, pos, runtime int64) error {
	_, err := s.db.Exec(`UPDATE emby_events SET pos_ticks=?,
		runtime_ticks=CASE WHEN ?>0 THEN ? ELSE runtime_ticks END WHERE id=?`, pos, runtime, runtime, id)
	return err
}

// FinishEmbyPlay 回填播放结束时间与观看位置。
func (s *Store) FinishEmbyPlay(id, endedAt, posTicks int64) error {
	_, err := s.db.Exec(`UPDATE emby_events SET ended_at=?, pos_ticks=? WHERE id=?`, endedAt, posTicks, id)
	return err
}

// EmbyHistoryQuery 清单查询参数（530）：User/Type 空串不过滤；
// From/To 为 started_at 的 unix 秒区间（左闭右开，0 不过滤），前端按本地时区换算。
type EmbyHistoryQuery struct {
	IntegrationID int64
	User          string
	Type          string
	From          int64
	To            int64
	Limit         int
	Offset        int
}

// embyWhere 按集成与筛选拼 WHERE 子句与参数（清单与计数共用，保证口径一致）。
func embyWhere(q EmbyHistoryQuery) (string, []any) {
	where := "integration_id=?"
	args := []any{q.IntegrationID}
	if q.User != "" {
		where += " AND user=?"
		args = append(args, q.User)
	}
	if q.Type != "" {
		where += " AND item_type=?"
		args = append(args, q.Type)
	}
	if q.From > 0 {
		where += " AND started_at>=?"
		args = append(args, q.From)
	}
	if q.To > 0 {
		where += " AND started_at<?"
		args = append(args, q.To)
	}
	return where, args
}

// embyMergeGap 清单合并窗口（546）：同一用户同一 item 相邻两次播放间隔不超过
// 2 小时即归并为一条显示（链式续接，跨多段也并）；统计报表不受影响。
const embyMergeGap = int64(2 * 3600)

// EmbyHistory 按条件分页查播放记录（新→旧），返回当页行与命中总数（530）。
// 546 起清单口径：同一用户同一 item（item_id 非空）且相邻间隔 ≤2h 的多次播放
// 合并为一条——开始时间取最早一次、结束/进度/设备取最近一次（末段进行中的
// ended_at=0 随之透出），total 按合并后条数计；统计报表 EmbyStats 仍按原始
// 行聚合不受影响。
// 归并整体下推 SQL（窗口函数，与原 Go 流式归并同口径）：emby_events 永久
// 留存随时间无界增长，前端 30s 轮询此接口，Go 侧全量拉行归并在大表上是
// 主要慢点——LAG 判相邻归并、SUM 累计编组后一次查询出页，配合
// (integration_id, started_at, id) 索引免排序。
func (s *Store) EmbyHistory(q EmbyHistoryQuery) ([]EmbyEvent, int, error) {
	if q.Limit < 1 {
		q.Limit = 50
	} else if q.Limit > 1000 {
		q.Limit = 1000
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	where, args := embyWhere(q)
	// seq：按新→旧逐行判「与上一行是否同组」（同用户同 item 非空且上一行
	// 开始时间距本行结束 ≤2h；首行 LAG 为 NULL 比较不成立自然开新组）；
	// grp：step 累计和=组号；grp2：组内新→旧行号 rn 与组行数 cnt。
	// 聚合取数与原 Go 口径一致：head=rn 1（组内最近一段）出各字段，
	// started_at 取 MIN（最早段），runtime head 为 0 时回落最早段；
	// total=COUNT(*) OVER () 在 GROUP BY 之后、LIMIT 之前求值=合并后总组数。
	// 注意 SELECT 里的归并窗口比 WHERE 先出现在 SQL 文本里，间隔常量直接
	// 内联字面量，避免占位符按文本序绑定错位。
	qry := `WITH seq AS (
		SELECT id, user, device, item_id, title, sub, item_type, season, episode, started_at, ended_at, pos_ticks, runtime_ticks,
			CASE WHEN item_id != ''
				AND user = LAG(user) OVER o
				AND item_id = LAG(item_id) OVER o
				AND LAG(started_at) OVER o - COALESCE(NULLIF(ended_at, 0), started_at) <= ` +
		strconv.FormatInt(embyMergeGap, 10) + `
				THEN 0 ELSE 1 END AS step
		FROM emby_events WHERE ` + where + `
		WINDOW o AS (ORDER BY started_at DESC, id DESC)
	), grp AS (
		SELECT *, SUM(step) OVER (ORDER BY started_at DESC, id DESC) AS gid FROM seq
	), grp2 AS (
		SELECT *, ROW_NUMBER() OVER (PARTITION BY gid ORDER BY started_at DESC, id DESC) AS rn,
			COUNT(*) OVER (PARTITION BY gid) AS cnt FROM grp
	)
	SELECT id, user, device, item_id, title, sub, item_type, season, episode, started_at, ended_at, pos_ticks, runtime_ticks, total
	FROM (
		SELECT MAX(CASE WHEN rn = 1 THEN id END) AS id,
			MAX(CASE WHEN rn = 1 THEN user END) AS user,
			MAX(CASE WHEN rn = 1 THEN device END) AS device,
			MAX(CASE WHEN rn = 1 THEN item_id END) AS item_id,
			MAX(CASE WHEN rn = 1 THEN title END) AS title,
			MAX(CASE WHEN rn = 1 THEN sub END) AS sub,
			MAX(CASE WHEN rn = 1 THEN item_type END) AS item_type,
			MAX(CASE WHEN rn = 1 THEN season END) AS season,
			MAX(CASE WHEN rn = 1 THEN episode END) AS episode,
			MIN(started_at) AS started_at,
			MAX(CASE WHEN rn = 1 THEN ended_at END) AS ended_at,
			MAX(CASE WHEN rn = 1 THEN pos_ticks END) AS pos_ticks,
			COALESCE(NULLIF(MAX(CASE WHEN rn = 1 THEN runtime_ticks END), 0),
				MAX(CASE WHEN rn = cnt THEN runtime_ticks END)) AS runtime_ticks,
			MAX(CASE WHEN rn = 1 THEN started_at END) AS head_at,
			MAX(CASE WHEN rn = 1 THEN id END) AS head_id,
			COUNT(*) OVER () AS total
		FROM grp2 GROUP BY gid
	)
	ORDER BY head_at DESC, head_id DESC LIMIT ? OFFSET ?`
	all := append(args, q.Limit, q.Offset)
	rows, err := s.db.Query(qry, all...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var (
		out   []EmbyEvent
		total int
	)
	for rows.Next() {
		var e EmbyEvent
		if err := rows.Scan(&e.ID, &e.User, &e.Device, &e.ItemID, &e.Title, &e.Sub, &e.Type,
			&e.Season, &e.Episode, &e.StartedAt, &e.EndedAt, &e.PosTicks, &e.RuntimeTicks, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// EmbyFilterOptions 某集成的用户/类型去重清单（清单筛选下拉的数据源，530）。
func (s *Store) EmbyFilterOptions(integrationID int64) (users, types []string, err error) {
	us, err := s.db.Query(`SELECT DISTINCT user FROM emby_events
		WHERE integration_id=? AND user!='' ORDER BY user`, integrationID)
	if err != nil {
		return nil, nil, err
	}
	defer us.Close()
	for us.Next() {
		var u string
		if err := us.Scan(&u); err != nil {
			return nil, nil, err
		}
		users = append(users, u)
	}
	if err := us.Err(); err != nil {
		return nil, nil, err
	}
	ts, err := s.db.Query(`SELECT DISTINCT item_type FROM emby_events
		WHERE integration_id=? AND item_type!='' ORDER BY item_type`, integrationID)
	if err != nil {
		return nil, nil, err
	}
	defer ts.Close()
	for ts.Next() {
		var v string
		if err := ts.Scan(&v); err != nil {
			return nil, nil, err
		}
		types = append(types, v)
	}
	return users, types, ts.Err()
}

// 聚合行（530 统计报表）：Seconds 为会话时长合计（ended-started，暂停计入、
// 进行中只计次数不计时长）。
type (
	EmbyUserStat struct {
		User    string `json:"user"`
		Plays   int64  `json:"plays"`
		Seconds int64  `json:"seconds"`
	}
	EmbyTypeStat struct {
		Type  string `json:"type"`
		Plays int64  `json:"plays"`
	}
	EmbyDayStat struct {
		Date    string `json:"date"`
		Plays   int64  `json:"plays"`
		Seconds int64  `json:"seconds"`
	}
	// EmbyUserItemStat 每用户看过的剧影条目（1，2026-10-22）：剧集按剧名
	// （sub）归并、电影按片名——多次播放合并一条不列集数明细，LastAt=最后
	// 播放时刻（进行中行取 started_at）。
	EmbyUserItemStat struct {
		User   string `json:"user"`
		Name   string `json:"name"`
		Type   string `json:"type"` // Episode=剧集 / Movie=电影（同名剧影各一条）
		Plays  int64  `json:"plays"`
		LastAt int64  `json:"last_at"`
	}
)

// embyDurSum 时长口径：已结束的取 ended-started，进行中计 0（只计次数）。
const embyDurSum = `SUM(CASE WHEN ended_at>0 THEN ended_at-started_at ELSE 0 END)`

// EmbyStats 聚合报表（530）：按用户（次数+时长排行）/按类型（次数）/按日期
// （次数+时长，date(started_at,unixepoch,localtime) 按服务器本地时区切日）。
// since=0 表示全部历史。
func (s *Store) EmbyStats(integrationID, since int64) ([]EmbyUserStat, []EmbyTypeStat, []EmbyDayStat, error) {
	base := `FROM emby_events WHERE integration_id=?`
	args := []any{integrationID}
	if since > 0 {
		base += ` AND started_at>=?`
		args = append(args, since)
	}
	ur, err := s.db.Query(`SELECT user, COUNT(*), `+embyDurSum+` `+base+
		` AND user!='' GROUP BY user ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer ur.Close()
	var users []EmbyUserStat
	for ur.Next() {
		var u EmbyUserStat
		if err := ur.Scan(&u.User, &u.Plays, &u.Seconds); err != nil {
			return nil, nil, nil, err
		}
		users = append(users, u)
	}
	if err := ur.Err(); err != nil {
		return nil, nil, nil, err
	}

	tr, err := s.db.Query(`SELECT item_type, COUNT(*) `+base+
		` AND item_type!='' GROUP BY item_type ORDER BY COUNT(*) DESC`, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer tr.Close()
	var types []EmbyTypeStat
	for tr.Next() {
		var v EmbyTypeStat
		if err := tr.Scan(&v.Type, &v.Plays); err != nil {
			return nil, nil, nil, err
		}
		types = append(types, v)
	}
	if err := tr.Err(); err != nil {
		return nil, nil, nil, err
	}

	dr, err := s.db.Query(`SELECT date(started_at,'unixepoch','localtime'), COUNT(*), `+embyDurSum+` `+base+
		` GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer dr.Close()
	var days []EmbyDayStat
	for dr.Next() {
		var d EmbyDayStat
		if err := dr.Scan(&d.Date, &d.Plays, &d.Seconds); err != nil {
			return nil, nil, nil, err
		}
		days = append(days, d)
	}
	return users, types, days, dr.Err()
}

// EmbyUserItems 每用户看过的剧影清单（1）：仅 Episode/Movie 两类，剧集按剧名
// （sub 空回落片名）电影按片名归并——同用户同片名多次播放合并一条，Plays=播放
// 次数、LastAt=最后播放时刻（MAX(结束时刻，进行中取开始)）。user 空行不计
// （与 EmbyStats 按用户口径一致），since=0 表示全部历史。按 LastAt 新→旧。
func (s *Store) EmbyUserItems(integrationID, since int64) ([]EmbyUserItemStat, error) {
	q := `SELECT user,
			CASE WHEN item_type='Episode' AND sub!='' THEN sub ELSE title END AS name,
			CASE WHEN item_type='Episode' THEN 'Episode' ELSE 'Movie' END AS kind,
			COUNT(*) AS plays,
			MAX(COALESCE(NULLIF(ended_at, 0), started_at)) AS last_at
		FROM emby_events
		WHERE integration_id=? AND user!='' AND item_type IN ('Episode','Movie')`
	var args []any
	args = append(args, integrationID)
	if since > 0 {
		q += ` AND started_at>=?`
		args = append(args, since)
	}
	q += ` GROUP BY user, name, kind ORDER BY last_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmbyUserItemStat
	for rows.Next() {
		var it EmbyUserItemStat
		if err := rows.Scan(&it.User, &it.Name, &it.Type, &it.Plays, &it.LastAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// EmbyWatchedCount 已观看部数（2026-10-23 用户指定；2026-10-24 修口径）：所有
// 用户看过的电影部数 + 电视剧部数各自去重——剧集按所属剧（sub=SeriesName）归并，
// 一部剧看多少集都只计 1 部，不按集数计；sub 缺失的存量行退回 item_id 单独计
// （宁按集计也不抹掉）。重看/多人同看不重复计；emby_events 永久留存，只增不减。
func (s *Store) EmbyWatchedCount(integrationID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT
		COUNT(DISTINCT CASE WHEN item_type='Movie' THEN item_id END)
		+ COUNT(DISTINCT CASE WHEN item_type='Episode' THEN COALESCE(NULLIF(sub,''), item_id) END)
		FROM emby_events
		WHERE integration_id = ? AND item_id != '' AND item_type IN ('Movie','Episode')`, integrationID).Scan(&n)
	return n, err
}
