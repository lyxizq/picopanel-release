package api

import (
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"

	"lz-monitor/internal/server/store"
)

// StatusHandler 服务端自监控：只读展示运行时信息、数据库规模与监控概况，
// 不新增任何采集埋点（采集路径零改动）。
type StatusHandler struct {
	st           *store.Store
	dbPath       string
	version      string
	startedAt    time.Time
	offlineAfter int64 // 离线判定秒数（与 /servers、/stats 同口径）
}

// Get 返回自监控信息（运行 / 数据库 / 监控概况三块）。
func (h *StatusHandler) Get(c *gin.Context) {
	now := time.Now()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	// 服务端更新时间：可执行文件落盘时间（更新重启 / 在线更新替换文件时刷新），
	// 取不到时回落进程启动时间
	updatedAt := h.startedAt.Unix()
	if exe, err := os.Executable(); err == nil {
		if fi, err := os.Stat(exe); err == nil {
			updatedAt = fi.ModTime().Unix()
		}
	}

	// 数据库文件：主文件 + WAL（WAL 未 checkpoint 时单独占空间）
	var dbSize, walSize int64
	if h.dbPath != "" {
		if fi, err := os.Stat(h.dbPath); err == nil {
			dbSize = fi.Size()
		}
		if fi, err := os.Stat(h.dbPath + "-wal"); err == nil {
			walSize = fi.Size()
		}
	}
	rowApprox, _ := h.st.MetricRowApprox()
	archRows, _ := h.st.ArchiveRowsApprox()
	retention, _ := h.st.RetentionDays()

	// 监控概况：模式计数 / 在线状态 / 告警统计全部来自既有查询
	//（轻量行：状态页 30s 轮询，只数模式与在线，不解密任何敏感字段）
	servers, _ := h.st.ListServerLites(false)
	modeCount := map[string]int{}
	online, offline, disabled := 0, 0, 0
	for _, srv := range servers {
		modeCount[srv.CollectMode]++
		if !srv.Enabled {
			disabled++
		} else if srv.LastReportAt > 0 && now.Unix()-srv.LastReportAt <= h.offlineAfter {
			online++
		} else {
			offline++
		}
	}
	firing := 0
	if m, err := h.st.FiringCountByServer(); err == nil {
		for _, n := range m {
			firing += n
		}
	}
	rules, _ := h.st.ListRules()
	_, alertTotal, _ := h.st.ListAlerts("all", 0, 1, 1)
	logins, _ := h.st.ListLoginLog()

	c.JSON(http.StatusOK, gin.H{
		"runtime": gin.H{
			"version":    h.version,
			"go_version": runtime.Version(),
			"uptime":     int64(now.Sub(h.startedAt).Seconds()),
			"goroutines": runtime.NumGoroutine(),
			"mem_alloc":  ms.Alloc, // 堆上活跃字节
			"mem_sys":    ms.Sys,   // 从 OS 拿到的总字节
			"os":         runtime.GOOS + "/" + runtime.GOARCH,
			"now":        now.Unix(),
			"updated_at": updatedAt,
		},
		"db": gin.H{
			"size":         dbSize,
			"wal_size":     walSize,
			"rows_approx":  rowApprox, // MAX(rowid) 近似（清理后偏高，量级参考）
			"archive_rows": archRows,  // 5 分钟归档桶数（7 天外降采样）
			"retention":    retention,
		},
		"overview": gin.H{
			"servers":     len(servers),
			"online":      online,
			"offline":     offline,
			"disabled":    disabled,
			"mode_count":  modeCount,
			"rules":       len(rules),
			"firing":      firing,
			"alert_total": alertTotal,
		},
		"logins": logins, // 登录日志（最近 20 条，新→旧）
	})
}
