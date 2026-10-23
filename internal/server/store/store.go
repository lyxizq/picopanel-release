// Package store 封装全部 SQLite 访问。
//
// 约束：连接池固定为 1（单写者模型），彻底规避 "database is locked"。
// 按几十台服务器、10s 间隔的写入量（约 5 写/秒）完全够用；
// 所有表访问必须经本包，不得在包外直接使用 *sql.DB 并发开连接。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lz-monitor/internal/proto"

	_ "modernc.org/sqlite" // 纯 Go 驱动，注册 "sqlite"，交叉编译无需 CGO
)

// Store 是数据访问入口。
type Store struct {
	db  *sql.DB
	sec *secretBox // 敏感字段加解密器；nil = 不加密（测试直连库的兼容路径）
}

// Open 打开（或创建）数据库并完成初始化（密钥用固定路径 lmKeyPath）。
func Open(path string) (*Store, error) {
	return OpenWithKey(path, lmKeyPath)
}

// OpenWithKey 同 Open，密钥文件路径由调用方指定（测试注入临时路径用）。
func OpenWithKey(path, keyPath string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",   // 读写不互斥
		"PRAGMA synchronous=NORMAL", // WAL 下安全且更快
		"PRAGMA busy_timeout=5000",  // 兜底等待
		"PRAGMA foreign_keys=ON",
		"PRAGMA auto_vacuum=INCREMENTAL", // 必须在建表之前设置，之后才能 incremental_vacuum
		"PRAGMA cache_size=-65536",       // 页缓存 64MB（默认 2MB）：历史曲线/归档查询重扫大表时少走磁盘
		"PRAGMA temp_store=MEMORY",       // GROUP BY 桶聚合的临时 b-tree 进内存
		"PRAGMA mmap_size=268435456",     // 256MB mmap 读：明细表顺序扫描少一次内核拷贝
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	if _, err := db.Exec(ddl); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化表结构: %w", err)
	}
	if _, err := db.Exec(groupsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化分组表: %w", err)
	}
	if _, err := db.Exec(dashChartsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化总览图表配置表: %w", err)
	}
	if _, err := db.Exec(serverDisksDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化最新磁盘表: %w", err)
	}
	if _, err := db.Exec(serverAppsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化应用负载快照表: %w", err)
	}
	if _, err := db.Exec(dashMetricsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化重点指标配置表: %w", err)
	}
	if _, err := db.Exec(settingsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化参数设置表: %w", err)
	}
	if _, err := db.Exec(metricsNetsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化网卡速率表: %w", err)
	}
	if _, err := db.Exec(serverListensDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化服务器端口表: %w", err)
	}
	if _, err := db.Exec(metricsArchiveDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化指标归档表: %w", err)
	}
	if _, err := db.Exec(loginLogDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化登录日志表: %w", err)
	}
	if _, err := db.Exec(pageSettingsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化主页配置表: %w", err)
	}
	if _, err := db.Exec(pageGroupsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化主页链接分组表: %w", err)
	}
	if _, err := db.Exec(pageLinksDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化主页链接表: %w", err)
	}
	if _, err := db.Exec(pageMetricsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化主页数据监控表: %w", err)
	}
	if _, err := db.Exec(docGroupsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化文档分组表: %w", err)
	}
	if _, err := db.Exec(docsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化文档表: %w", err)
	}
	if _, err := db.Exec(docVersionsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化文档版本表: %w", err)
	}
	if _, err := db.Exec(docAssetsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化文档附件表: %w", err)
	}
	if _, err := db.Exec(certsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化证书监控表: %w", err)
	}
	if _, err := db.Exec(integrationsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化服务集成表: %w", err)
	}
	if _, err := db.Exec(mailAccountsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化邮件账户表: %w", err)
	}
	if _, err := db.Exec(mailHeadersDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化邮件信头缓存表: %w", err)
	}
	if _, err := db.Exec(mailContactsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化邮件通讯录表: %w", err)
	}
	if _, err := db.Exec(termScriptsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化终端脚本表: %w", err)
	}
	if _, err := db.Exec(termHistoryDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化终端历史表: %w", err)
	}
	if _, err := db.Exec(fileFavsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化文件收藏表: %w", err)
	}
	if _, err := db.Exec(pushSubsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化推送订阅表: %w", err)
	}
	if _, err := db.Exec(messagesDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化站内消息表: %w", err)
	}
	if _, err := db.Exec(reminderGroupsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化提醒分组表: %w", err)
	}
	if _, err := db.Exec(remindersDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化提醒事项表: %w", err)
	}
	if _, err := db.Exec(membershipsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化会员到期表: %w", err)
	}
	if _, err := db.Exec(tvFollowsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化追剧表: %w", err)
	}
	if _, err := db.Exec(netTasksDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化通用中转任务表: %w", err)
	}
	if _, err := db.Exec(netTasksIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化通用中转任务索引: %w", err)
	}
	if _, err := db.Exec(appTasksDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化应用操作任务表: %w", err)
	}
	if _, err := db.Exec(appTasksIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化应用操作任务索引: %w", err)
	}
	if _, err := db.Exec(biliFollowsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化B站追更配置表: %w", err)
	}
	if _, err := db.Exec(biliFollowsIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化B站追更配置索引: %w", err)
	}
	if _, err := db.Exec(biliTasksDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化B站追更任务表: %w", err)
	}
	if _, err := db.Exec(biliTasksIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化B站追更任务索引: %w", err)
	}
	if _, err := db.Exec(biliItemsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化B站追更记录表: %w", err)
	}
	if _, err := db.Exec(biliItemsIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化B站追更记录索引: %w", err)
	}
	if _, err := db.Exec(dbConnectionsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库连接表: %w", err)
	}
	if _, err := db.Exec(haEventsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化智能家居开关事件表: %w", err)
	}
	if _, err := db.Exec(haSamplesDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化智能家居采样表: %w", err)
	}
	if _, err := db.Exec(haSamplesArchiveDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化智能家居采样归档表: %w", err)
	}
	if _, err := db.Exec(wxSamplesDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化天气采样表: %w", err)
	}
	if _, err := db.Exec(wxSamplesArchiveDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化天气采样归档表: %w", err)
	}
	if _, err := db.Exec(embyEventsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 Emby 播放记录表: %w", err)
	}
	if _, err := db.Exec(aiAccountsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 AI 用量账户表: %w", err)
	}
	if _, err := db.Exec(svcServersDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化服务监控服务器表: %w", err)
	}
	if _, err := db.Exec(svcPortsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化服务监控端口表: %w", err)
	}
	if _, err := db.Exec(svcPortsIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化服务监控端口索引: %w", err)
	}
	if _, err := db.Exec(svcEventsDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化服务监控翻转事件表: %w", err)
	}
	if _, err := db.Exec(svcEventsIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化服务监控翻转事件索引: %w", err)
	}
	if _, err := db.Exec(ptSitesDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 PT 站点表: %w", err)
	}
	if _, err := db.Exec(ptSitesIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 PT 站点索引: %w", err)
	}
	if _, err := db.Exec(ptTasksDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 PT 抓取任务表: %w", err)
	}
	if _, err := db.Exec(ptTasksIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 PT 抓取任务索引: %w", err)
	}
	if _, err := db.Exec(ghReposDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 GitHub 监控仓库表: %w", err)
	}
	if _, err := db.Exec(ghReleasesDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 GitHub Release 快照表: %w", err)
	}
	if _, err := db.Exec(ghReleasesIdxDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化 GitHub Release 快照索引: %w", err)
	}
	if err := migrateSvcMonV2(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("迁移旧服务监控表: %w", err)
	}
	// v1 升级（user_version 作一次性标记）：
	//  1. 磁盘占用改为仅存最新（server_disks 单行 upsert，metrics.disks 不再写历史），
	//     用历史行里最近一份非空磁盘状态回填，保住已部署实例的「当前磁盘」展示；
	//  2. 磁盘告警规则的持续时长归零——历史曲线没了，持续判定无处取数，归零=满足即触发。
	// 仅旧结构（含 disks 列）执行；新库建表即 v2 结构，跳过。
	var uv int
	_ = db.QueryRow(`PRAGMA user_version`).Scan(&uv)
	if uv < 1 && hasMetricColumn(db, "disks") {
		if _, err := db.Exec(`INSERT OR IGNORE INTO server_disks (server_id, disks, ts)
			SELECT m.server_id, m.disks, m.ts FROM metrics m
			JOIN (SELECT server_id, MAX(ts) AS ts FROM metrics
				WHERE disks IS NOT NULL AND disks != '' GROUP BY server_id) t
				ON m.server_id = t.server_id AND m.ts = t.ts`); err != nil {
			db.Close()
			return nil, fmt.Errorf("回填最新磁盘状态: %w", err)
		}
		if _, err := db.Exec(`UPDATE alert_rules SET duration_sec = 0 WHERE metric = 'disk' AND duration_sec > 0`); err != nil {
			db.Close()
			return nil, fmt.Errorf("磁盘规则持续时长归零: %w", err)
		}
		if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
			db.Close()
			return nil, fmt.Errorf("写入升级标记: %w", err)
		}
	}
	// v2 升级（存储精简）：nets JSON 拆到 metrics_nets（同时去掉服务端从不使用的
	// rx_bytes/tx_bytes 累计计数器）、listens 改 server_listens 单行最新、metrics
	// 重建为纯标量表。7 天前明细降采样 5 分钟桶进 metrics_archive 由后台清理循环
	// 在启动后立即执行首轮（分批，大库耗时数分钟，启动日志可见进度）。
	if hasMetricColumn(db, "nets") {
		if err := migrateMetricsV2(db); err != nil {
			db.Close()
			return nil, err
		}
	}
	if uv < 2 {
		if _, err := db.Exec(`PRAGMA user_version = 2`); err != nil {
			db.Close()
			return nil, fmt.Errorf("写入升级标记: %w", err)
		}
	}
	// 旧库升级（早期版本缺列）；已存在时忽略报错，幂等。
	for _, col := range []string{
		`ALTER TABLE servers ADD COLUMN net_ifaces TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN agent_addr TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN collect_mode TEXT NOT NULL DEFAULT 'agent'`,
		`ALTER TABLE servers ADD COLUMN ssh_user TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN ssh_key_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN ssh_addr TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN ssh_password TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN ssh_host_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN group_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE servers ADD COLUMN purchased_on TEXT NOT NULL DEFAULT ''`,            // 购买日期
		`ALTER TABLE servers ADD COLUMN expires_on TEXT NOT NULL DEFAULT ''`,              // 到期日期
		`ALTER TABLE servers ADD COLUMN remark TEXT NOT NULL DEFAULT ''`,                  // 备注（悬停服务器名展示）
		`ALTER TABLE dashboard_charts ADD COLUMN sort INTEGER NOT NULL DEFAULT 0`,         // 拖拽排序
		`ALTER TABLE dashboard_metrics ADD COLUMN sort INTEGER NOT NULL DEFAULT 0`,        // 拖拽排序
		`ALTER TABLE docs ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0`,               // 回收站软删标记
		`ALTER TABLE docs ADD COLUMN sort INTEGER NOT NULL DEFAULT 0`,                     // 组内拖拽排序
		`ALTER TABLE https_certs ADD COLUMN issuer_org TEXT NOT NULL DEFAULT ''`,          // 证书颁发者组织
		`ALTER TABLE https_certs ADD COLUMN issued_to TEXT NOT NULL DEFAULT ''`,           // 证书颁发给（CN）
		`ALTER TABLE metrics_archive ADD COLUMN disks TEXT NOT NULL DEFAULT ''`,           // 磁盘 IO 桶均值（v0.4.2 起归档）
		`ALTER TABLE metrics_archive ADD COLUMN thermal TEXT NOT NULL DEFAULT ''`,         // 温度/风扇桶均值
		`ALTER TABLE reminders ADD COLUMN important INTEGER NOT NULL DEFAULT 0`,           // 星标（重要视图聚合）
		`ALTER TABLE reminders ADD COLUMN my_day INTEGER NOT NULL DEFAULT 0`,              // 加入「我的一天」
		`ALTER TABLE reminders ADD COLUMN prev_id INTEGER NOT NULL DEFAULT 0`,             // 本条由哪条完成生成（恢复时撤回下一条）
		`ALTER TABLE reminders ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0`,          // 软删时间戳（0=正常，>0=回收站）
		`ALTER TABLE reminders ADD COLUMN parent_id INTEGER NOT NULL DEFAULT 0`,           // 父任务 ID（0=顶级；子任务最多 5 级）
		`ALTER TABLE reminders ADD COLUMN notify INTEGER NOT NULL DEFAULT 1`,              // 是否提醒（0=仅记录，1=推送+站内消息）
		`ALTER TABLE page_settings ADD COLUMN link_mode TEXT NOT NULL DEFAULT 'vertical'`, // 链接分组展示模式（horizontal/vertical，竖向默认全展开）
		`ALTER TABLE page_settings ADD COLUMN show_avatar INTEGER NOT NULL DEFAULT 1`,     // 标题前登录头像显隐（默认显示）
		`ALTER TABLE page_settings ADD COLUMN show_ha INTEGER NOT NULL DEFAULT 1`,         // 智能家居 FAB 钮显隐（381，默认显示）
		`ALTER TABLE page_settings ADD COLUMN refresh_sec INTEGER NOT NULL DEFAULT 30`,    // 数据监控自动刷新间隔秒（0=不自动刷新）
		`ALTER TABLE page_metrics ADD COLUMN icon TEXT NOT NULL DEFAULT ''`,               // 模块标签前小图标地址
		`ALTER TABLE page_settings ADD COLUMN weather_enabled INTEGER NOT NULL DEFAULT 0`, // 首页天气显示开关（默认关）
		`ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin'`,                 // 多用户角色（361：admin/user，存量默认管理员）
		`ALTER TABLE page_settings ADD COLUMN weather_mode TEXT NOT NULL DEFAULT 'auto'`,  // 定位方式：auto=按访客 IP / manual=手动城市
		`ALTER TABLE page_settings ADD COLUMN weather_city TEXT NOT NULL DEFAULT ''`,      // 城市名（展示用）
		`ALTER TABLE page_settings ADD COLUMN weather_lat REAL NOT NULL DEFAULT 0`,        // 纬度（manual 模式取数）
		`ALTER TABLE page_settings ADD COLUMN weather_lon REAL NOT NULL DEFAULT 0`,        // 经度（manual 模式取数）
		`ALTER TABLE page_settings ADD COLUMN weather_provider TEXT NOT NULL DEFAULT ''`,  // 天气服务商（空=Open-Meteo 免 key / gaode=高德）
		`ALTER TABLE page_settings ADD COLUMN weather_key TEXT NOT NULL DEFAULT ''`,       // 服务商 API Key（高德 Web 服务 key）
		`ALTER TABLE page_settings ADD COLUMN weather_api_host TEXT NOT NULL DEFAULT ''`,  // 和风专属 API Host（518，v1 起必配且参与鉴权）
		`ALTER TABLE servers ADD COLUMN icon TEXT NOT NULL DEFAULT ''`,                    // 服务器图标 URL（名称前展示；空=不显示）
		`ALTER TABLE mail_accounts ADD COLUMN poll_sec INTEGER NOT NULL DEFAULT 600`,      // 邮件未读轮询间隔秒（326；建表 DDL 只对新建库生效，存量库必须走这里）
		`ALTER TABLE ai_accounts ADD COLUMN secret_id TEXT NOT NULL DEFAULT ''`,           // EdgeOne SecretId（393，加密存库）
		`ALTER TABLE ai_accounts ADD COLUMN secret_key TEXT NOT NULL DEFAULT ''`,          // EdgeOne SecretKey（393，加密存库）
		`ALTER TABLE ai_accounts ADD COLUMN zone_ids TEXT NOT NULL DEFAULT ''`,            // EdgeOne 站点 ID 过滤（空=全部站点）
		`ALTER TABLE pt_sites ADD COLUMN open_url TEXT NOT NULL DEFAULT ''`,               // PT 站点打开地址（391，空=站点首页）
		`ALTER TABLE pt_sites ADD COLUMN proxy_server_id INTEGER NOT NULL DEFAULT 0`,      // 抓取出口（0=直连，>0=经该 push agent 中转）
		`ALTER TABLE pt_sites ADD COLUMN signin_mode TEXT NOT NULL DEFAULT ''`,            // 签到模式（416，dian115：空=仅查询/normal/lucky）
		`ALTER TABLE pt_sites ADD COLUMN icon TEXT NOT NULL DEFAULT ''`,                   // 自定义站点图标 URL（391，空=内置目录图标）
		`ALTER TABLE servers ADD COLUMN agent_version TEXT NOT NULL DEFAULT ''`,           // agent 版本串（396，随 Sys 上报；空=旧版 agent 未带）
		`ALTER TABLE page_settings ADD COLUMN show_stats INTEGER NOT NULL DEFAULT 1`,      // 主页统计概览区显隐（459，默认显示）
		`ALTER TABLE gh_repos ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`,           // GitHub 仓库显示名（472，空=回落 owner/repo）
		`ALTER TABLE gh_repos ADD COLUMN seen_at INTEGER NOT NULL DEFAULT 0`,              // GitHub 已看时刻（474，新更新提醒基准；0=旧数据下次进页即清）
		`ALTER TABLE dashboard_metrics ADD COLUMN ai_id INTEGER NOT NULL DEFAULT 0`,       // 用量查询账户数据源（486，>0=用量格子）
		`ALTER TABLE page_metrics ADD COLUMN ai_id INTEGER NOT NULL DEFAULT 0`,            // 同上（主页数据监控模块）
		`ALTER TABLE memberships ADD COLUMN start_date TEXT NOT NULL DEFAULT ''`,          // 会员开始时间（492 返工：进度条按起止区间算；空=回落剩余/周期）
		`ALTER TABLE memberships ADD COLUMN sort INTEGER NOT NULL DEFAULT 0`,              // 会员拖拽排序（492 返工；0=未排过，建表 DDL 已含此列的库忽略）
		`ALTER TABLE svc_ports ADD COLUMN scheme TEXT NOT NULL DEFAULT ''`,                // 端口协议（495：''/'tcp'=握手、http/https=请求检测）
		`ALTER TABLE svc_ports ADD COLUMN domain TEXT NOT NULL DEFAULT ''`,                // 请求检测域名（495，空=服务器 host）
		`ALTER TABLE tv_follows ADD COLUMN actors TEXT NOT NULL DEFAULT ''`,               // 主要演员（550；建表 DDL 已含此列的库忽略）
		`ALTER TABLE tv_follows ADD COLUMN overview TEXT NOT NULL DEFAULT ''`,             // 简介（554 详情弹层）
		`ALTER TABLE tv_follows ADD COLUMN episode_name TEXT NOT NULL DEFAULT ''`,         // 最新集名（554 详情弹层）
		`ALTER TABLE tv_follows ADD COLUMN platforms TEXT NOT NULL DEFAULT ''`,            // 播出平台（564 watch/providers）
		`ALTER TABLE tv_follows ADD COLUMN short_drama INTEGER NOT NULL DEFAULT 0`,        // 短剧手动标记（58；建表 DDL 已含此列的库忽略）
		`ALTER TABLE net_tasks ADD COLUMN allow_any_host INTEGER NOT NULL DEFAULT 0`,      // PT 类任务跳过 agent 白名单（566；建表 DDL 已含此列的库忽略）
		`ALTER TABLE bili_follows ADD COLUMN watermark INTEGER NOT NULL DEFAULT 0`,        // 追更水位=已见最新 pubdate（7：只追比它新的，不回填旧视频）
		`ALTER TABLE bili_follows ADD COLUMN stop_flag INTEGER NOT NULL DEFAULT 0`,        // 追更手动停止标记（8：agent 捎带领取，回传后清除）
		`ALTER TABLE bili_items ADD COLUMN status TEXT NOT NULL DEFAULT 'downloaded'`,     // 追更条目状态（7：downloaded/exists/failed）
		`ALTER TABLE wx_samples ADD COLUMN data TEXT NOT NULL DEFAULT ''`,                 // 采样时完整天气 Resp JSON（56，HA 胶囊读库；建表 DDL 已含此列的库忽略）
		`ALTER TABLE emby_events ADD COLUMN season INTEGER NOT NULL DEFAULT 0`,            // 播放季号（70 清单显示 SxxExx；建表 DDL 已含此列的库忽略）
		`ALTER TABLE emby_events ADD COLUMN episode INTEGER NOT NULL DEFAULT 0`,           // 播放集号（70，同上）
	} {
		if _, err := db.Exec(col); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				db.Close()
				return nil, fmt.Errorf("升级表结构: %w", err)
			}
		}
	}
	// 489 存量回填（幂等，只命中 seen_at=0 的行）：全局「进页清账」废弃后，
	// 474 时代的老库行 seen_at 恒为 0，会把全部存量 Release 误判「新更新」；
	// 回填 created_at=存量版本不算新，之后发布的才算（新行建仓即写 seen_at，
	// 不会再出现 0）。
	if _, err := db.Exec(`UPDATE gh_repos SET seen_at = created_at WHERE seen_at = 0`); err != nil {
		db.Close()
		return nil, fmt.Errorf("GitHub 已看时刻回填: %w", err)
	}
	// 视频下载模块已移除（2026-10-23 用户指定连表与记录一并清理；表本体在 ddl 串里
	// DROP）：v1.0.41 写入的通用/按平台 Cookie 设置键幂等清掉，无行可删时即空操作。
	if _, err := db.Exec(`DELETE FROM settings WHERE key = 'video_dl_cookies' OR key LIKE 'video_dl_cookies_%'`); err != nil {
		db.Close()
		return nil, fmt.Errorf("清理视频下载遗留设置: %w", err)
	}
	// 其余已下线功能的遗留表/列/数据（证书续签、SMTP 邮件渠道、电信接入）同口径清理
	if err := cleanupLegacySchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("清理历史遗留结构: %w", err)
	}
	// 492 存量回填（幂等，只命中 sort=0 的行）：拖拽排序列补上后，老行按
	// 到期日序编 sort 1..n；拖过序的行 sort≠0 永不重排（须在补列之后执行）
	if err := BackfillMembershipSort(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("回填会员排序: %w", err)
	}
	// reminders.parent_id 的索引须在补列之后建（老库先有表无该列，放进建表
	// DDL 会在启动迁移时炸 no such column——2026-09-29 发布后启动失败的根因）
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_reminders_parent ON reminders(parent_id)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化提醒事项表索引: %w", err)
	}
	// 457 纠错迁移（幂等，条件命中才动）：v1.0.9 曾把「蜂巢」误接到 hdcity.city
	//（真实站点是 fengchao.chat，MP 集成 API + Bearer Key）。存量 hdcity 行重
	// 映射到 fengchao；凭据形态不同（Cookie 串 → API Key）无法自动转换，清掉
	// 快照与成功水位、留编辑提示，等用户重贴 Key 后正常抓取。
	if _, err := db.Exec(`UPDATE pt_sites SET
		site_key = 'fengchao',
		name = CASE WHEN name IN ('', '蜂巢 HDCity') THEN '蜂巢' ELSE name END,
		base_url = CASE WHEN base_url LIKE '%hdcity%' THEN '' ELSE base_url END,
		open_url = CASE WHEN open_url LIKE '%hdcity%' THEN '' ELSE open_url END,
		data_json = '',
		fetched_at = 0,
		last_ok_at = 0,
		last_error = '站点已更正为蜂巢（fengchao.chat）：请编辑站点粘贴 MP 专用 API Key（论坛「隐秘的角落」生成）'
		WHERE site_key = 'hdcity'`); err != nil {
		db.Close()
		return nil, fmt.Errorf("蜂巢站点纠错迁移: %w", err)
	}
	// v0.1 的 token 列带 NOT NULL UNIQUE（当时是推送凭据）；现为可选共享密钥，
	// 需去掉唯一约束（两台都不配密钥会撞 ''），检测到旧结构即整表重建。
	if err := migrateServers(db); err != nil {
		db.Close()
		return nil, err
	}
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		db.Close()
		return nil, err
	}
	box, err := newSecretBox(key)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化加密器: %w", err)
	}
	s := &Store{db: db, sec: box}
	if err := s.migrateEncryptPlaintext(); err != nil {
		db.Close()
		return nil, err
	}
	// 44/45 存量迁移（幂等）：全局单份头像/两步验证密钥过户给首个用户并删旧键；
	// 受限用户不继承 → 升级后所有受限用户默认关闭登录两步验证（2026-10-20 口径）
	if err := s.migratePerUserAuthKeys(); err != nil {
		db.Close()
		return nil, fmt.Errorf("迁移用户级头像/两步验证配置: %w", err)
	}
	// HA 智能家居总配置一次性播种（475）：settings 无 ha_master 时从首个 HA 集成
	// 拷贝连接参数与 rooms/devices/sensors（迁移纪律同其余 schema/数据迁移：失败
	// 即启动失败，不带病运行——重试到成功为止，哨兵写入后不再重复）
	if err := s.MigrateHAMaster(); err != nil {
		db.Close()
		return nil, fmt.Errorf("迁移 HA 智能家居总配置: %w", err)
	}
	return s, nil
}

// migrateServers 检测 servers 表残留的 UNIQUE 约束并按目标结构重建。
// 表无外键引用，重建安全；单事务保证原子。
func migrateServers(db *sql.DB) error {
	var schema string
	if err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='servers'`).Scan(&schema); err != nil {
		return fmt.Errorf("读取表结构: %w", err)
	}
	if !strings.Contains(schema, "UNIQUE") {
		return nil // 已是目标结构
	}
	newTable := strings.Replace(serversDDL, "servers", "servers_mig", 1)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		newTable,
		`INSERT INTO servers_mig (id, name, token, agent_addr, hostname, os, kernel, arch,
		   cpu_model, cpu_cores, enabled, net_ifaces, last_report_at, created_at, updated_at,
		   collect_mode, ssh_user, ssh_key_path, ssh_addr, ssh_password, ssh_host_key, group_id, purchased_on, expires_on, remark)
		 SELECT id, name, COALESCE(token, ''), agent_addr, hostname, os, kernel, arch,
		   cpu_model, cpu_cores, enabled, net_ifaces, last_report_at, created_at, updated_at,
		   collect_mode, ssh_user, ssh_key_path, ssh_addr, ssh_password, ssh_host_key, group_id, purchased_on, expires_on, remark FROM servers`,
		`DROP TABLE servers`,
		`ALTER TABLE servers_mig RENAME TO servers`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("迁移 servers 表: %w", err)
		}
	}
	log.Printf("已迁移 servers 表：token 改为可选共享密钥（原唯一约束已去除）")
	return tx.Commit()
}

// hasMetricColumn 判断 metrics 表是否含指定列（区分旧结构与 v2/全新库）。
func hasMetricColumn(db *sql.DB, col string) bool { return hasColumn(db, "metrics", col) }

// migrateMetricsV2 v2 存储精简迁移：nets 拆到 metrics_nets（JSON 瘦身）、
// listens 单行化到 server_listens、metrics 重建为纯标量表。
// 启动时同步执行（迁移完成前不对外服务），分批处理大库耗时数分钟；
// 各步幂等，中途失败重跑不产生重复数据。
func migrateMetricsV2(db *sql.DB) error {
	start := time.Now()
	log.Printf("开始 v2 存储迁移：nets 拆表 / listens 单行化 / metrics 纯标量化…")

	// 更早的旧库可能没有 listens 列：先补齐（已存在则忽略），后续 SQL 才能引用
	if _, err := db.Exec(`ALTER TABLE metrics ADD COLUMN listens TEXT`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("补齐 listens 列: %w", err)
	}

	// 1. 分批拆 nets：rowid 游标扫描非空行，瘦身 JSON 后写 metrics_nets
	var total, lastRowid int64
	for {
		rows, err := db.Query(`SELECT rowid, server_id, ts, nets FROM metrics
			WHERE rowid > ? AND nets IS NOT NULL AND nets != '' ORDER BY rowid LIMIT 5000`, lastRowid)
		if err != nil {
			return fmt.Errorf("迁移 v2 读取 nets: %w", err)
		}
		type item struct {
			rowid, sid, ts int64
			raw            string
		}
		var batch []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.rowid, &it.sid, &it.ts, &it.raw); err != nil {
				rows.Close()
				return fmt.Errorf("迁移 v2 解析 nets: %w", err)
			}
			batch = append(batch, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("迁移 v2 遍历 nets: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		for _, it := range batch {
			var nets []proto.NetInfo
			if err := json.Unmarshal([]byte(it.raw), &nets); err != nil || len(nets) == 0 {
				continue // 逐行解不开的脏数据丢弃，不让单行阻塞整库迁移
			}
			slim, err := encodeNets(nets)
			if err != nil {
				continue
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO metrics_nets (server_id, ts, nets) VALUES (?,?,?)`,
				it.sid, it.ts, slim); err != nil {
				tx.Rollback()
				return fmt.Errorf("迁移 v2 写入 metrics_nets: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		lastRowid = batch[len(batch)-1].rowid
		total += int64(len(batch))
		if total%500000 < 5000 {
			log.Printf("v2 迁移：已处理 nets %d 行…", total)
		}
	}
	log.Printf("v2 迁移：nets 拆出完成（%d 行）", total)

	// 2. server_listens 回填：每台最近一条非空 listens（当前端口集合）
	if _, err := db.Exec(`INSERT OR IGNORE INTO server_listens (server_id, listens, ts)
		SELECT m.server_id, m.listens, m.ts FROM metrics m
		JOIN (SELECT server_id, MAX(ts) AS ts FROM metrics
			WHERE listens IS NOT NULL AND listens != '' GROUP BY server_id) t
		ON m.server_id = t.server_id AND m.ts = t.ts`); err != nil {
		return fmt.Errorf("回填监听端口: %w", err)
	}

	// 3. 重建 metrics 为纯标量结构（单事务；千万行级在 WAL 下可行，
	//    额外空间由后续 incremental_vacuum 回收）
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	newTable := strings.Replace(metricsDDL, "metrics", "metrics_mig", 1)
	for _, q := range []string{
		newTable,
		`INSERT INTO metrics_mig (server_id, ts, cpu, mem, mem_used, mem_total, swap,
		   load1, load5, load15, uptime, tcp_est)
		 SELECT server_id, ts, cpu, mem, mem_used, mem_total, swap,
		   load1, load5, load15, uptime, tcp_est FROM metrics`,
		`DROP TABLE metrics`,
		`ALTER TABLE metrics_mig RENAME TO metrics`,
		`CREATE UNIQUE INDEX idx_metrics_server_ts ON metrics(server_id, ts)`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("重建 metrics 表: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("v2 存储迁移完成，耗时 %s（7 天外明细的降采样归档由后台清理循环立即执行）",
		time.Since(start).Round(time.Millisecond))
	return nil
}

// Close 关闭数据库（触发 WAL checkpoint）。
func (s *Store) Close() error { return s.db.Close() }

// Checkpoint 把 WAL 日志合并回主库并截断（在线更新前调：升级前备份/换二进制
// 期间保证 .db 主文件是完整最新状态，文件级拷贝即可兜底整库）。
func (s *Store) Checkpoint() error {
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// BackupTo 一致性快照到目标文件（SQLite VACUUM INTO，目标文件须不存在），
// 产出 standalone 完整库（无 WAL 依赖），可直接用于恢复/重新部署。
func (s *Store) BackupTo(path string) error {
	_, err := s.db.Exec(`VACUUM INTO ?`, path)
	return err
}

// DB 暴露底层连接：仅供本包与 api 包的测试直接建数/核对数据，生产代码不要用。
func (s *Store) DB() *sql.DB { return s.db }

// serversDDL 单独抽出：迁移时以此为目标结构重建旧表。
const serversDDL = `CREATE TABLE IF NOT EXISTS servers (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT    NOT NULL,
  token          TEXT    DEFAULT '',           -- 密钥（加密存库）：agent 模式=可选共享抓取密钥（空=NULL=不鉴权）；push 模式=一对一上报密钥（应用层校验唯一，见 pushTokenUsed）
  agent_addr     TEXT    NOT NULL DEFAULT '',  -- agent 模式=指标端口(192.168.1.5:19100)；ssh 模式=SSH 地址(host[:22])（加密存库，见 secret.go）
  hostname       TEXT    NOT NULL DEFAULT '',
  os             TEXT    NOT NULL DEFAULT '',
  kernel         TEXT    NOT NULL DEFAULT '',
  arch           TEXT    NOT NULL DEFAULT '',
  cpu_model      TEXT    NOT NULL DEFAULT '',
  cpu_cores      INTEGER NOT NULL DEFAULT 0,
  enabled        INTEGER NOT NULL DEFAULT 1,
  net_ifaces     TEXT    NOT NULL DEFAULT '',  -- Dashboard 展示的网口（逗号分隔，空=全部）
  last_report_at INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  collect_mode   TEXT    NOT NULL DEFAULT 'agent', -- 采集方式：agent=拉取指标端口；ssh=SSH 直连执行只读命令
  ssh_user       TEXT    NOT NULL DEFAULT '',  -- ssh 模式登录用户（加密存库，见 secret.go）
  ssh_key_path   TEXT    NOT NULL DEFAULT '',  -- ssh 私钥路径（服务端本机；空=尝试默认 ~/.ssh/id_*）
  ssh_addr       TEXT    NOT NULL DEFAULT '',  -- Web 终端/SFTP 专用 SSH 地址（加密存库；空=ssh 模式回退 agent_addr）
  ssh_password   TEXT    NOT NULL DEFAULT '',  -- SSH 密码认证（空=私钥认证；加密存库，见 secret.go）
  ssh_host_key   TEXT    NOT NULL DEFAULT '',  -- TOFU 记录的主机指纹（SHA256:...；变更即拒绝）
  group_id       INTEGER NOT NULL DEFAULT 0,   -- 所属分组（server_groups.id；0=未分组）
  purchased_on   TEXT    NOT NULL DEFAULT '',  -- 购买日期（YYYY-MM-DD；空=未配置）
  expires_on     TEXT    NOT NULL DEFAULT '',  -- 到期日期（YYYY-MM-DD；空=未配置，前端展示「永久」）
  remark         TEXT    NOT NULL DEFAULT ''   -- 备注（悬停服务器名时第二行展示）
);`

// groupsDDL 服务器分组：Dashboard 按分组手风琴展示。
const groupsDDL = `CREATE TABLE IF NOT EXISTS server_groups (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);`

// dashChartsDDL 总览页自定义图表：一台服务器的一个指标折线（网卡带 param）。
// 数量上限 6 由 API 层限制；sort 为拖拽排序（0=未排序，按 id 兜底）。
const dashChartsDDL = `CREATE TABLE IF NOT EXISTS dashboard_charts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id  INTEGER NOT NULL,
  metric     TEXT    NOT NULL,             -- cpu/mem/swap/load1/load5/load15/tcp_est/net
  param      TEXT    NOT NULL DEFAULT '',  -- net=网口；其余为空
  sort       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);`

// serverDisksDDL 最新磁盘状态：每台服务器单行，报告时 upsert（不保留历史曲线）。
const serverDisksDDL = `CREATE TABLE IF NOT EXISTS server_disks (
  server_id INTEGER PRIMARY KEY,
  disks     TEXT    NOT NULL,             -- JSON []proto.DiskInfo
  ts        INTEGER NOT NULL              -- 该状态对应的报告时间
);`

// serverAppsDDL 应用负载当前快照（54 飞牛集成）：Docker 容器与虚拟机列表
// 单行 upsert，与 server_disks/server_listens 同款「只存最新」口径。命名
// 刻意避开已废弃的 server_containers——启动 ddl 含该名的 DROP 语句，同名
// 新表会被每次启动清空。
const serverAppsDDL = `CREATE TABLE IF NOT EXISTS server_apps (
  server_id  INTEGER PRIMARY KEY,
  containers TEXT    NOT NULL DEFAULT '',  -- JSON []proto.ContainerInfo（空串=无数据/无容器）
  vms        TEXT    NOT NULL DEFAULT '',  -- JSON []proto.VMInfo
  ts         INTEGER NOT NULL              -- 该状态对应的报告时间
);`

// dashMetricsDDL 总览页顶部重点指标：一台服务器的一个数据 + 自定义标签。
// 数量上限 8 由 API 层限制；sort 为拖拽排序（0=未排序，按 id 兜底）。
const dashMetricsDDL = `CREATE TABLE IF NOT EXISTS dashboard_metrics (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id  INTEGER NOT NULL,
  ai_id      INTEGER NOT NULL DEFAULT 0, -- 用量查询账户 id（486，>0=用量格子，server_id 忽略）
  metric     TEXT    NOT NULL,             -- cpu/mem/swap/load1/load5/load15/tcp_est/uptime/disk/io/net(上下行)/net_rx/net_tx(存量单项)/ai_*(用量指标)
  param      TEXT    NOT NULL DEFAULT '',  -- disk/io=挂载点（空=最大值）；net=网口（空=展示网口合计）
  label      TEXT    NOT NULL,             -- 展示标签（用户填写）
  sort       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);`

// settingsDDL 系统参数（Web「参数设置」页运行期可改，改后即时生效，不再走配置文件）。
const settingsDDL = `CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);`

// loginLogDDL 登录日志：只记成功登录，写入后裁剪保留最近 20 条（loginLogKeep）。
const loginLogDDL = `CREATE TABLE IF NOT EXISTS login_log (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT    NOT NULL,
  ts       INTEGER NOT NULL,
  ip       TEXT    NOT NULL
);`

// termScriptsDDL SSH 终端的每服务器专属脚本（终端页新增/编辑/复制，内容入库；
// 名称在服务器内唯一，语言仅作前端高亮提示）。
const termScriptsDDL = `CREATE TABLE IF NOT EXISTS term_scripts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id  INTEGER NOT NULL,
  name       TEXT    NOT NULL,
  lang       TEXT    NOT NULL DEFAULT 'shell', -- 前端高亮语言（shell/python…）
  content    TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE(server_id, name)
);
CREATE INDEX IF NOT EXISTS idx_term_scripts_server ON term_scripts(server_id);`

// termHistoryDDL SSH 终端命令补全历史（每服务器一份 JSON 数组，跨设备同步）。
const termHistoryDDL = `CREATE TABLE IF NOT EXISTS term_history (
  server_id  INTEGER PRIMARY KEY,
  items      TEXT    NOT NULL DEFAULT '[]',
  updated_at INTEGER NOT NULL
);`

// fileFavsDDL 文件管理收藏夹（每服务器一份 JSON 数组，跨设备同步）。
const fileFavsDDL = `CREATE TABLE IF NOT EXISTS file_favs (
  server_id  INTEGER PRIMARY KEY,
  items      TEXT    NOT NULL DEFAULT '[]',
  updated_at INTEGER NOT NULL
);`

// pageSettingsDDL 个人主页配置（单行 id=1）：标题、搜索框显隐、链接分组展示模式、
// 登录头像显隐、智能家居钮显隐、数据监控自动刷新间隔与首页天气配置。
const pageSettingsDDL = `CREATE TABLE IF NOT EXISTS page_settings (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  title       TEXT    NOT NULL DEFAULT '',
  show_search INTEGER NOT NULL DEFAULT 1,
  link_mode   TEXT    NOT NULL DEFAULT 'vertical', -- horizontal=横向手风琴 / vertical=竖向（默认全展开）
  show_avatar INTEGER NOT NULL DEFAULT 1,          -- 标题前登录头像显隐
  show_ha     INTEGER NOT NULL DEFAULT 1,          -- 智能家居 FAB 钮显隐（381）
  refresh_sec INTEGER NOT NULL DEFAULT 30,         -- 数据监控自动刷新间隔秒（0=不自动刷新）
  weather_enabled INTEGER NOT NULL DEFAULT 0,      -- 首页天气显示开关（默认关）
  weather_mode    TEXT    NOT NULL DEFAULT 'auto', -- 定位方式：auto=按访客 IP / manual=手动城市
  weather_city    TEXT    NOT NULL DEFAULT '',     -- 城市名（展示用）
  weather_lat     REAL    NOT NULL DEFAULT 0,      -- 纬度（manual 模式取数）
  weather_lon     REAL    NOT NULL DEFAULT 0,      -- 经度（manual 模式取数）
  updated_at  INTEGER NOT NULL
);`

// pageMetricsDDL 主页数据监控模块（与总览「数据监控」同构但独立配置：
// 数值同样取自 /servers 最新快照；link 为模块点击跳转链接、icon 为标签前小图标，均选填）。
const pageMetricsDDL = `CREATE TABLE IF NOT EXISTS page_metrics (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id  INTEGER NOT NULL,
  ai_id      INTEGER NOT NULL DEFAULT 0, -- 用量查询账户 id（486，>0=用量格子，server_id 忽略）
  metric     TEXT    NOT NULL,
  param      TEXT    NOT NULL DEFAULT '',
  label      TEXT    NOT NULL,
  link       TEXT    NOT NULL DEFAULT '',
  icon       TEXT    NOT NULL DEFAULT '',
  sort       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);`

// pageGroupsDDL 个人主页链接分组（横向手风琴面板，sort 为拖拽排序）。
const pageGroupsDDL = `CREATE TABLE IF NOT EXISTS page_groups (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL,
  sort       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);`

// pageLinksDDL 个人主页链接（图标+文字，组内 sort 为拖拽排序）。
const pageLinksDDL = `CREATE TABLE IF NOT EXISTS page_links (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  group_id   INTEGER NOT NULL,
  name       TEXT    NOT NULL,
  url        TEXT    NOT NULL,
  icon       TEXT    NOT NULL DEFAULT '',
  sort       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_page_links_group ON page_links(group_id);`

// integrationsDDL 服务集成（Homepage widget 同口径的第三方服务数据源）：
// type=homeassistant|qbittorrent|emby。auth 为凭据 JSON（AES-GCM 加密存库，
// 见 secret.go）；config 为明文配置 JSON（HA 实体列表、Emby 显示开关等）。
// last_ok_at/last_error 由集成轮询器回写（状态变化才写），供管理列表展示健康态。
const integrationsDDL = `CREATE TABLE IF NOT EXISTS integrations (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  type       TEXT    NOT NULL,
  name       TEXT    NOT NULL,
  url        TEXT    NOT NULL DEFAULT '',
  auth       TEXT    NOT NULL DEFAULT '',
  config     TEXT    NOT NULL DEFAULT '',
  sort       INTEGER NOT NULL DEFAULT 0,
  enabled    INTEGER NOT NULL DEFAULT 1,
  last_ok_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`

// docGroupsDDL 文档分组。
const docGroupsDDL = `CREATE TABLE IF NOT EXISTS doc_groups (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL UNIQUE,
  created_at INTEGER NOT NULL
);`

// docsDDL 文档。format：richtext=富文本（存 HTML）、md=Markdown（存源码）。
// deleted_at：0=正常；>0=删除时间戳（回收站软删，可恢复或彻底删除）。
// sort：组内手动拖拽序（0=未拖过，参与 updated_at 倒序；>0 按其升序）。
const docsDDL = `CREATE TABLE IF NOT EXISTS docs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  group_id   INTEGER NOT NULL DEFAULT 0,   -- 所属分组（doc_groups.id；0=未分组）
  title      TEXT    NOT NULL,
  format     TEXT    NOT NULL DEFAULT 'richtext', -- richtext | md
  content    TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER NOT NULL DEFAULT 0,
  sort       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_docs_group ON docs(group_id);`

// docVersionsDDL 文档历史版本：UpdateDoc 覆盖 content 前对旧内容留快照
// （时间节流见 store.UpdateDoc，每文档仅保留最近 20 版）。
const docVersionsDDL = `CREATE TABLE IF NOT EXISTS doc_versions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  doc_id     INTEGER NOT NULL,
  title      TEXT    NOT NULL,
  content    TEXT    NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_doc_versions_doc ON doc_versions(doc_id, id);`

// docAssetsDDL 文档附件（富文本编辑器上传的图片/视频等，BLOB 直存）。
const docAssetsDDL = `CREATE TABLE IF NOT EXISTS doc_assets (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  mime       TEXT NOT NULL,
  size       INTEGER NOT NULL,
  data       BLOB NOT NULL,
  created_at INTEGER NOT NULL
);`

// metricsDDL 单独抽出：v2 迁移重建时以此为目标结构。
// 纯标量表——nets/listens/disks 已拆出（metrics_nets / server_listens / server_disks），
// 标量查询不再跳过 JSON 溢出页（读放大），每行从几百字节缩到约 60 字节。
const metricsDDL = `CREATE TABLE IF NOT EXISTS metrics (
  server_id INTEGER NOT NULL,
  ts        INTEGER NOT NULL,          -- 服务端接收时间（unix 秒）
  cpu       REAL    NOT NULL DEFAULT 0,
  mem       REAL,                      -- 内存使用率 %
  mem_used  INTEGER,
  mem_total INTEGER,
  swap      REAL,
  load1     REAL, load5 REAL, load15 REAL,
  uptime    INTEGER,
  tcp_est   INTEGER
);`

// metricsNetsDDL 网卡速率历史（从 metrics 拆出）。JSON 瘦身只存 iface 与两个速率，
// 服务端/前端从不使用的 rx_bytes/tx_bytes 累计计数器不落库（见 encodeNets）。
const metricsNetsDDL = `CREATE TABLE IF NOT EXISTS metrics_nets (
  server_id INTEGER NOT NULL,
  ts        INTEGER NOT NULL,
  nets      TEXT    NOT NULL
);`

// metricsDisksDDL 磁盘 IO 速率历史（与 metrics_nets 同款布局）。
// JSON 瘦身只存设备名与读/写速率（diskSlim）；磁盘容量本身仍只存最新
// （server_disks 单行 upsert），IO 速率是唯一有历史曲线消费方的磁盘指标。
const metricsDisksDDL = `CREATE TABLE IF NOT EXISTS metrics_disks (
  server_id INTEGER NOT NULL,
  ts        INTEGER NOT NULL,
  disks     TEXT    NOT NULL
);`

// metricsThermalDDL 温度/风扇历史（与 metrics_nets 同款布局）。
// JSON 瘦身存温度数组（CPU/硬盘/主板，sensor 标签区分）与风扇数组（thermalSlim）。
const metricsThermalDDL = `CREATE TABLE IF NOT EXISTS metrics_thermal (
  server_id INTEGER NOT NULL,
  ts        INTEGER NOT NULL,
  thermal   TEXT    NOT NULL
);`

// serverListensDDL 监听端口当前状态：每台单行 upsert（变化才写，与 server_disks 同款）。
const serverListensDDL = `CREATE TABLE IF NOT EXISTS server_listens (
  server_id INTEGER PRIMARY KEY,
  listens   TEXT    NOT NULL,             -- JSON []proto.ListenPort
  ts        INTEGER NOT NULL              -- 最近变化时间
);`

// metricsArchiveDDL 降采样归档：7 天外明细按 5 分钟桶聚合的均值。
// 标量取 AVG；nets/disks 为各网口/各设备桶均值（瘦身格式）；uptime/listens
// 不归档（无曲线消费方；磁盘容量当前值在 server_disks、端口在 server_listens）。
const metricsArchiveDDL = `CREATE TABLE IF NOT EXISTS metrics_archive (
  server_id INTEGER NOT NULL,
  ts        INTEGER NOT NULL,             -- 桶起始时间（300s 对齐）
  cpu       REAL NOT NULL DEFAULT 0,
  mem       REAL, swap REAL,
  load1     REAL, load5 REAL, load15 REAL,
  mem_used  REAL, mem_total REAL,
  tcp_est   REAL,
  nets      TEXT NOT NULL,
  disks     TEXT NOT NULL DEFAULT '',
  thermal   TEXT NOT NULL DEFAULT ''
);`

const ddl = serversDDL + metricsDDL + metricsNetsDDL + metricsDisksDDL + metricsThermalDDL + metricsArchiveDDL + `
-- Docker 容器监控已移除：清掉旧版本遗留的快照表
DROP TABLE IF EXISTS server_containers;
-- 视频下载模块已移除（2026-10-23 用户指定连表与记录一并清理）：v1.0.41 建的任务表，索引随表删
DROP TABLE IF EXISTS video_dl_tasks;
-- (server_id, ts) 唯一索引同时是时间范围扫描索引；同秒重复上报靠 INSERT OR REPLACE 幂等
CREATE UNIQUE INDEX IF NOT EXISTS idx_metrics_server_ts ON metrics(server_id, ts);
CREATE UNIQUE INDEX IF NOT EXISTS idx_metrics_nets ON metrics_nets(server_id, ts);
CREATE UNIQUE INDEX IF NOT EXISTS idx_metrics_disks ON metrics_disks(server_id, ts);
CREATE UNIQUE INDEX IF NOT EXISTS idx_metrics_thermal ON metrics_thermal(server_id, ts);
CREATE UNIQUE INDEX IF NOT EXISTS idx_metrics_archive ON metrics_archive(server_id, ts);
-- 保留期清理按 ts < ? 批删（runRetentionLoop 每小时一轮），单列 ts 索引让
-- 「无事可删」的常态轮变成索引探针而非全索引扫
CREATE INDEX IF NOT EXISTS idx_metrics_ts ON metrics(ts);
CREATE INDEX IF NOT EXISTS idx_metrics_nets_ts ON metrics_nets(ts);
CREATE INDEX IF NOT EXISTS idx_metrics_disks_ts ON metrics_disks(ts);
CREATE INDEX IF NOT EXISTS idx_metrics_thermal_ts ON metrics_thermal(ts);
CREATE INDEX IF NOT EXISTS idx_metrics_archive_ts ON metrics_archive(ts);

CREATE TABLE IF NOT EXISTS alert_rules (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT    NOT NULL,
  metric       TEXT    NOT NULL,   -- cpu/mem/swap/load1/load5/load15/tcp_est/disk/net_rx/net_tx
  op           TEXT    NOT NULL,   -- '>' | '<'
  threshold    REAL    NOT NULL,
  duration_sec INTEGER NOT NULL DEFAULT 0,
  server_id    INTEGER,            -- NULL=全局规则；非空=单机规则
  param        TEXT,               -- disk 规则=mount、net 规则=iface；空=取最大值
  enabled      INTEGER NOT NULL DEFAULT 1,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS alerts (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id      INTEGER NOT NULL,   -- 0 = 内置离线告警
  server_id    INTEGER NOT NULL,
  metric       TEXT,
  op           TEXT,
  threshold    REAL,
  value        REAL,               -- 触发/最近观测值
  detail       TEXT,               -- 人读消息
  status       TEXT    NOT NULL,   -- 'firing' | 'resolved'
  triggered_at INTEGER NOT NULL,
  resolved_at  INTEGER,
  updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_alerts_status_time ON alerts(status, triggered_at DESC);
-- 部分唯一索引：同一 (规则,服务器) 同时最多一条 firing，数据库层面防重复告警
CREATE UNIQUE INDEX IF NOT EXISTS idx_alerts_open ON alerts(rule_id, server_id) WHERE status='firing';

CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'admin', -- admin=管理员 / user=受限用户（361，仅 HA 页面）
  created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  token      TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
`

// mailAccountsDDL 邮件账户（Foxmail 式收发，316）：provider=服务商预设（imap/smtp
// 主机端口随预设带出、可改），password AES-GCM 加密存库（secret.go，同 integrations）。
const mailAccountsDDL = `CREATE TABLE IF NOT EXISTS mail_accounts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL,
  email      TEXT    NOT NULL,
  provider   TEXT    NOT NULL DEFAULT '',
  imap_host  TEXT    NOT NULL DEFAULT '',
  imap_port  INTEGER NOT NULL DEFAULT 993,
  smtp_host  TEXT    NOT NULL DEFAULT '',
  smtp_port  INTEGER NOT NULL DEFAULT 465,
  smtp_ssl   INTEGER NOT NULL DEFAULT 1,
  password   TEXT    NOT NULL DEFAULT '',
  poll_sec   INTEGER NOT NULL DEFAULT 600,
  sort       INTEGER NOT NULL DEFAULT 0,
  last_ok_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`

// mailHeadersDDL 邮件信头缓存（按账户+文件夹镜像最近信头，列表秒开）：
// uid 为 IMAP UID，(account_id, folder, uid) 唯一。
const mailHeadersDDL = `CREATE TABLE IF NOT EXISTS mail_headers (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id INTEGER NOT NULL,
  folder     TEXT    NOT NULL,
  uid        INTEGER NOT NULL,
  from_name  TEXT    NOT NULL DEFAULT '',
  from_addr  TEXT    NOT NULL DEFAULT '',
  subject    TEXT    NOT NULL DEFAULT '',
  date_unix  INTEGER NOT NULL DEFAULT 0,
  seen       INTEGER NOT NULL DEFAULT 0,
  has_att    INTEGER NOT NULL DEFAULT 0,
  size       INTEGER NOT NULL DEFAULT 0,
  UNIQUE(account_id, folder, uid)
);
CREATE INDEX IF NOT EXISTS idx_mail_headers_list ON mail_headers(account_id, folder, date_unix DESC);`

// mailContactsDDL 邮件通讯录（506）：写信收件人关键字带出/选择，email 归一小写
// 全局唯一（大小写不敏感防重）。
const mailContactsDDL = `CREATE TABLE IF NOT EXISTS mail_contacts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL DEFAULT '',
  email      TEXT    NOT NULL UNIQUE,
  created_at INTEGER NOT NULL DEFAULT 0
);`

// dbConnectionsDDL 数据库管理工具连接表（323）：auth 为 {username,password}
// JSON 的 AES-GCM 密文（secret.go，同 mail/integrations），params 为扩展参数
// JSON（ssl/超时/SSH 隧道服务器），database 对 sqlite 为库文件路径。
const dbConnectionsDDL = `CREATE TABLE IF NOT EXISTS db_connections (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL,
  type       TEXT    NOT NULL,             -- mysql / postgres / sqlite
  host       TEXT    NOT NULL DEFAULT '',
  port       INTEGER NOT NULL DEFAULT 0,
  database   TEXT    NOT NULL DEFAULT '',  -- sqlite=库文件绝对路径
  auth       TEXT    NOT NULL DEFAULT '',  -- 密文 JSON {username,password}
  params     TEXT    NOT NULL DEFAULT '{}',
  readonly   INTEGER NOT NULL DEFAULT 0,
  enabled    INTEGER NOT NULL DEFAULT 1,
  sort       INTEGER NOT NULL DEFAULT 0,
  last_ok_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`

// aiAccountsDDL 用量账户（356）：provider=zhipu/deepseek/edgeone 白名单，
// base_url 空回落预设站点（代理/镜像/国际站场景可覆盖），api_key（及 EdgeOne 的
// secret_id/secret_key）AES-GCM 加密存库（secret.go），usage_json 仅存最新一份
// 覆盖式快照；zone_ids 为 EdgeOne 站点 ID 过滤（空=账号全部站点）。
const aiAccountsDDL = `CREATE TABLE IF NOT EXISTS ai_accounts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL,
  provider   TEXT    NOT NULL,
  base_url   TEXT    NOT NULL DEFAULT '',
  api_key    TEXT    NOT NULL DEFAULT '',
  sort       INTEGER NOT NULL DEFAULT 0,
  usage_json TEXT    NOT NULL DEFAULT '',
  usage_at   INTEGER NOT NULL DEFAULT 0,
  last_ok_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  secret_id  TEXT    NOT NULL DEFAULT '',
  secret_key TEXT    NOT NULL DEFAULT '',
  zone_ids   TEXT    NOT NULL DEFAULT ''
);`

// svcServersDDL/svcPortsDDL 服务监控两级表（366 重构，原 358 平铺 svc_monitors）：
// 服务器（业务名/地址/检查间隔/启停）+ 端口（业务名/端口号/最新联通状态）。
// up 三态（1=通 0=不通 -1=从未检测），since=当前状态的起始时刻（状态变化时由
// UPDATE 内 CASE 推进），interval_sec 为服务器级检查间隔（列名避开 INTERVAL 字样）。
const svcServersDDL = `CREATE TABLE IF NOT EXISTS svc_servers (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT    NOT NULL DEFAULT '',
  host         TEXT    NOT NULL,
  interval_sec INTEGER NOT NULL DEFAULT 30,
  enabled      INTEGER NOT NULL DEFAULT 1,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);`

const svcPortsDDL = `CREATE TABLE IF NOT EXISTS svc_ports (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id  INTEGER NOT NULL,
  name       TEXT    NOT NULL DEFAULT '',
  port       INTEGER NOT NULL,
  scheme     TEXT    NOT NULL DEFAULT '',
  domain     TEXT    NOT NULL DEFAULT '',
  up         INTEGER NOT NULL DEFAULT -1,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  checked_at INTEGER NOT NULL DEFAULT 0,
  since      INTEGER NOT NULL DEFAULT 0,
  last_ok_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`

const svcPortsIdxDDL = `CREATE INDEX IF NOT EXISTS idx_svc_ports_server ON svc_ports (server_id);`

// svcEventsDDL/svcEventsIdxDDL 状态翻转事件（500，通↔不通边界才落行）：
// 可用率与「最近事件」由它算，按数据保留天数滚动清理。
const svcEventsDDL = `CREATE TABLE IF NOT EXISTS svc_events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  port_id     INTEGER NOT NULL,
  server_id   INTEGER NOT NULL,
  up          INTEGER NOT NULL,
  latency_ms  INTEGER NOT NULL DEFAULT 0,
  error       TEXT    NOT NULL DEFAULT '',
  occurred_at INTEGER NOT NULL
);`

const svcEventsIdxDDL = `CREATE INDEX IF NOT EXISTS idx_svc_events_port ON svc_events (port_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_svc_events_time ON svc_events (occurred_at);`

// ptSitesDDL PT 站点信息（391）：site_key 限内置站点目录（唯一，一站点一行），
// cookie 列存 Cookie（M-Team 类 API 站存 x-api-key，均 AES-GCM 加密），base_url
// 空=目录默认站点（镜像/备用域名可覆盖），data_json 仅存最新一份覆盖式快照。
const ptSitesDDL = `CREATE TABLE IF NOT EXISTS pt_sites (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  site_key   TEXT    NOT NULL,
  name       TEXT    NOT NULL DEFAULT '',
  base_url   TEXT    NOT NULL DEFAULT '',
  open_url   TEXT    NOT NULL DEFAULT '',
  proxy_server_id INTEGER NOT NULL DEFAULT 0,
  icon       TEXT    NOT NULL DEFAULT '',
  signin_mode TEXT   NOT NULL DEFAULT '',
  cookie     TEXT    NOT NULL DEFAULT '',
  ua         TEXT    NOT NULL DEFAULT '',
  enabled    INTEGER NOT NULL DEFAULT 1,
  sort       INTEGER NOT NULL DEFAULT 0,
  data_json  TEXT    NOT NULL DEFAULT '',
  fetched_at INTEGER NOT NULL DEFAULT 0,
  last_ok_at INTEGER NOT NULL DEFAULT 0,
  last_error TEXT    NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`

const ptSitesIdxDDL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_pt_sites_key ON pt_sites (site_key);`

// ptTasksDDL PT 抓取任务队列（391 扩展）：中转抓取任务（server 入队 → push
// agent 上报时领取 → 回传结果）。Cookie 加密存库；idx_pt_tasks_active 部分
// 唯一索引保证每站最多一个活跃任务（防死循环第一道防线）。
const ptTasksDDL = `CREATE TABLE IF NOT EXISTS pt_tasks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  site_id     INTEGER NOT NULL,
  server_id   INTEGER NOT NULL,
  site_key    TEXT    NOT NULL DEFAULT '',
  name        TEXT    NOT NULL DEFAULT '',
  base_url    TEXT    NOT NULL DEFAULT '',
  cookie      TEXT    NOT NULL DEFAULT '',
  ua          TEXT    NOT NULL DEFAULT '',
  attempt     INTEGER NOT NULL DEFAULT 0,
  state       TEXT    NOT NULL DEFAULT 'pending',
  leased_at   INTEGER NOT NULL DEFAULT 0,
  result_json TEXT    NOT NULL DEFAULT '',
  error       TEXT    NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);`

const ptTasksIdxDDL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_pt_tasks_active ON pt_tasks (site_id) WHERE state IN ('pending','leased');`

// migrateSvcMonV2 旧 svc_monitors 平铺目标表（358）迁到两级表（366 重构）：
// 按 host 分组——同主机多个端口归一台服务器（服务器名=host，间隔/启停取该组
// 第一行），端口业务名沿用原目标名，拨测状态字段随端口行迁移；迁完 DROP 旧表。
// 旧表不存在（新库/已迁过）直接跳过。
func migrateSvcMonV2(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='svc_monitors'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	rows, err := db.Query(`SELECT name, host, port, interval_sec, enabled,
		up, latency_ms, checked_at, since, last_ok_at, last_error
		FROM svc_monitors ORDER BY sort, id`)
	if err != nil {
		return err
	}
	type oldTarget struct {
		name                     string
		host                     string
		port                     int
		interval                 int
		enabled                  int
		up                       int
		latencyMs                int64
		checkedAt, since, lastOK int64
		lastErr                  string
	}
	var olds []oldTarget
	for rows.Next() {
		var o oldTarget
		if err := rows.Scan(&o.name, &o.host, &o.port, &o.interval, &o.enabled,
			&o.up, &o.latencyMs, &o.checkedAt, &o.since, &o.lastOK, &o.lastErr); err != nil {
			rows.Close()
			return err
		}
		olds = append(olds, o)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	serverIDs := map[string]int64{} // host → 已建服务器 id
	for _, o := range olds {
		sid, ok := serverIDs[o.host]
		if !ok {
			res, err := tx.Exec(`INSERT INTO svc_servers (name, host, interval_sec, enabled, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?)`, o.host, o.host, o.interval, o.enabled, now, now)
			if err != nil {
				tx.Rollback()
				return err
			}
			sid, err = res.LastInsertId()
			if err != nil {
				tx.Rollback()
				return err
			}
			serverIDs[o.host] = sid
		}
		if _, err := tx.Exec(`INSERT INTO svc_ports (server_id, name, port, up, latency_ms, checked_at, since, last_ok_at, last_error, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sid, o.name, o.port, o.up, o.latencyMs, o.checkedAt, o.since, o.lastOK, o.lastErr, now, now); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := db.Exec(`DROP TABLE svc_monitors`); err != nil {
		return err
	}
	return nil
}
