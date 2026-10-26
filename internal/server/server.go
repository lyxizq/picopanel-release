package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"lz-monitor/internal/logutil"
	"lz-monitor/internal/server/aiusage"
	"lz-monitor/internal/server/api"
	"lz-monitor/internal/server/assistant"
	"lz-monitor/internal/server/bilifollow"
	"lz-monitor/internal/server/certcheck"
	"lz-monitor/internal/server/config"
	"lz-monitor/internal/server/dbtool"
	"lz-monitor/internal/server/engine"
	"lz-monitor/internal/server/ghwatch"
	"lz-monitor/internal/server/integrations"
	"lz-monitor/internal/server/mail"
	"lz-monitor/internal/server/message"
	"lz-monitor/internal/server/pt"
	"lz-monitor/internal/server/reminder"
	"lz-monitor/internal/server/scrape"
	"lz-monitor/internal/server/store"
	"lz-monitor/internal/server/svcmon"
	"lz-monitor/internal/server/term"
	"lz-monitor/internal/server/totp"
	"lz-monitor/internal/server/tvfollow"
	"lz-monitor/internal/server/updmgr"
	"lz-monitor/internal/server/weather"
	"lz-monitor/internal/server/webhook"
	"lz-monitor/internal/server/webpush"
	"lz-monitor/internal/server/wxpusher"
	"lz-monitor/internal/update"
)

// 默认管理员：首次启动自动创建，登录后请立即在页面修改密码。
const (
	defaultAdminUser     = "admin"
	defaultAdminPassword = "admin123"
)

// Run 装配并启动服务，阻塞直至收到退出信号。version 为构建时注入的版本号
// （自监控页展示；启动时刻在 api.Register 记录，供运行时长计算），pubKey 为
// 构建注入的发布验签公钥（在线更新验签，395）。
func Run(cfg *config.Config, version string, pubKey string) error {
	gin.SetMode(gin.ReleaseMode)

	// 滚动日志：终端与文件双写，gin 恢复日志一并纳入（nohup 重定向到
	// 同名文件时自动只留终端）
	logWriter := logutil.New(cfg.LogFile, int64(cfg.LogMaxMB)<<20, logutil.Backups)
	log.SetOutput(logWriter)
	gin.DefaultWriter = logWriter
	gin.DefaultErrorWriter = logWriter

	// 升级标记诊断（395⑧）：升级后启动走到这里而最终失败时，main 凭标记自动
	// exec 旧版回滚；一切正常则在监听绑定成功后由下方 ClearMarker 确认
	marker := update.DefaultMarkerPath()
	if update.MarkerExists(marker) {
		log.Printf("检测到升级标记：本次为升级后启动，健康自检通过后自动清除")
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	if created, err := st.EnsureAdmin(defaultAdminUser, defaultAdminPassword); err != nil {
		return err
	} else if created {
		log.Printf("【重要】已创建默认管理员 %s / %s，请登录后立即修改密码！", defaultAdminUser, defaultAdminPassword)
	}

	// -resetuser：忘记登录名/密码的恢复入口——第一个管理员重置为默认账号后继续正常启动。
	// 注意会清掉该用户全部登录会话（改密语义），重启后需用默认账号重新登录。
	if cfg.ResetUser {
		hash, err := bcrypt.GenerateFromPassword([]byte(defaultAdminPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if reset, err := st.ResetFirstAdmin(defaultAdminUser, string(hash)); err != nil {
			return err
		} else if reset {
			log.Printf("【重要】已按 -resetuser 重置管理员为 %s / %s，请登录后立即在「个人信息」修改！", defaultAdminUser, defaultAdminPassword)
		}
	}

	// -resettotp：Authenticator 丢失的恢复入口——清除全部用户的两步验证配置
	//（启用/待确认密钥）后继续正常启动，之后用正确密码即可直接登录；恢复后
	// 各自在「个人信息」重新开启两步验证。
	if cfg.ResetTOTP {
		had, err := totp.Reset(st)
		if err != nil {
			return fmt.Errorf("清除两步验证配置失败: %w", err)
		}
		if had {
			log.Printf("【重要】已按 -resettotp 清除全部用户的两步验证配置，请用密码登录后在「个人信息」重新开启。")
		} else {
			log.Printf("已按 -resettotp 检查：两步验证本就未开启，无需清除。")
		}
	}

	// 告警通知渠道：日志（基础）+ WxPusher 微信推送（系统设置页配置并开启后生效）；
	// EnableMerge 后事件先经聚合器——短时间内多台设备同时告警/恢复合并成
	// 一条通知（站内 + 推送同口径），防服务器多时手机被推送刷屏
	eng := engine.New(st, engine.Config{OfflineAfter: cfg.OfflineAfter.D()},
		engine.ConsoleNotifier{}, wxpusher.NewAlertNotifier(st), webhook.NewAlertNotifier(st),
		webpush.NewAlertNotifier(st), message.NewSink(st))
	eng.EnableMerge()
	scraper := scrape.New(st, eng, scrape.Config{
		Interval: cfg.ScrapeInterval.D(),
		Timeout:  cfg.ScrapeTimeout.D(),
	})
	termMgr := term.NewManager(st)
	intMgr := integrations.New(st)
	mailMgr := mail.New(st)
	aiMgr := aiusage.New(st)
	// 和风天气系统账户随启动同步（3）：前台天气服务商=和风即自动挂进用量轮询，
	// 配置移除即摘除（保存页面设置时也会同步）
	if ps, err := st.GetPageSettings(); err != nil {
		log.Printf("读取主页配置失败（跳过和风用量账户同步）: %v", err)
	} else if err := st.SyncQWeatherSystemAccount(ps); err != nil {
		log.Printf("同步和风用量账户失败: %v", err)
	}
	svcMgr := svcmon.New(st)
	svcMgr.SetNotify(svcmon.NotifyFunc(st)) // 翻转推送+站内消息（500）
	ghMgr := ghwatch.New(st)
	ptMgr := pt.New(st)
	// 数据库管理工具连接池（惰性建池，无轮询；备份落数据库同目录 dbbackups/）
	dbMgr := dbtool.New(st, filepath.Join(filepath.Dir(cfg.DBPath), "dbbackups"))

	// 在线更新管理器（395/396）：源列表为空=功能关闭；缓存目录在部署目录
	// updates/（与分发包同盘，替换二进制/缓存 agent 包都不会跨盘）
	exePath, _ := os.Executable()
	updMgr := updmgr.New(st, updmgr.Config{
		Sources: cfg.Update.Sources,
		Channel: cfg.Update.Channel,
		PubKey:  pubKey,
		Version: version,
		Dir:     filepath.Join(filepath.Dir(exePath), "updates"),
		Agents:  *cfg.Update.Agents,
	})

	r := gin.New()
	// 精简日志：不开逐请求访问日志（面板轮询+agent 定时抓取一天能刷数万行），
	// 只记 5xx 服务端错误；4xx 属正常业务口径（未登录/参数错/登录失败），
	// 各自有业务日志或登录日志承载。gin.Recovery 的 panic 恢复照旧落错误日志。
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Next()
		if c.Writer.Status() >= http.StatusInternalServerError {
			log.Printf("http %d %s %s（来源 %s）", c.Writer.Status(), c.Request.Method, c.Request.URL.Path, c.ClientIP())
		}
	})
	// 不信任任何代理头（X-Forwarded-For 可伪造），ClientIP 一律取 TCP 对端地址，
	// 供登录失败限流与 web_allow 白名单使用；如部署在反代后面需要真实 IP 再放开。
	_ = r.SetTrustedProxies(nil)
	if err := api.Register(r, st, eng, cfg, version, termMgr, intMgr, mailMgr, aiMgr, dbMgr, svcMgr, ptMgr, updMgr, ghMgr); err != nil {
		return fmt.Errorf("注册路由: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // 指标抓取：按 scrape_interval 主动拉取各 agent
		defer wg.Done()
		scraper.Run(ctx)
	}()
	wg.Add(1)
	go func() { // Web 终端/SFTP：会话与连接池管理（含空闲回收）
		defer wg.Done()
		termMgr.Run(ctx)
	}()
	wg.Add(1)
	go func() { // 离线检测：30s 一轮
		defer wg.Done()
		eng.RunOfflineCheck(ctx, 30*time.Second)
	}()
	wg.Add(1)
	go func() { // 通知聚合 flush：静默 60s 或窗口满 10 分钟外发
		defer wg.Done()
		eng.RunMerge(ctx)
	}()
	wg.Add(1)
	go func() { // 数据保留清理：每小时一轮（含启动时立即执行一次）
		defer wg.Done()
		runRetentionLoop(ctx, st, cfg, time.Hour)
	}()
	wg.Add(1)
	go func() { // 天气采样（522）：10 分钟一轮落当前天气（含完整 Resp JSON 供 HA 胶囊读库，56），夜间 0:00–5:30 静默跳过
		defer wg.Done()
		weather.StartSampler(ctx, st, 10*time.Minute)
	}()
	wg.Add(1)
	go func() { // HTTPS 证书到期检查：每 12 小时一轮（含启动时立即执行一次），到期窗口联动自动续签
		defer wg.Done()
		certcheck.Run(ctx, st)
	}()
	wg.Add(1)
	go func() { // 个人助理：每日早报到点推送（巡检段并入早报，巡检包不再自带调度）、天气预警/空气巡视、带伞到点判定、ICS 订阅 6h 刷新
		defer wg.Done()
		assistant.Run(ctx, st, int64(cfg.OfflineAfter.D()/time.Second), mailMgr.UnreadAll)
	}()

	wg.Add(1)
	go func() { // 提醒事项到期推送：30s 一轮（前 1 小时/到期时/次日最后提醒三段）
		defer wg.Done()
		reminder.Run(ctx, st)
	}()

	wg.Add(1)
	go func() { // 追剧轮询（548）：30s 一 tick（待查条目入队 + 中转任务租约回收）
		defer wg.Done()
		tvfollow.Run(ctx, st)
	}()

	wg.Add(1)
	go func() { // B 站追更调度（TODO 6）：30s 一 tick（每日 4 点自动追更对表 + 任务租约回收）
		defer wg.Done()
		bilifollow.Run(ctx, st)
	}()

	wg.Add(1)
	go func() { // 应用操作任务巡检（54）：30s 一 tick——租约超时回收重派 + 终态 24h 清理
		defer wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if dead := st.ReclaimExpiredAppTasks(); dead > 0 {
					log.Printf("已回收 %d 个超时的容器/虚拟机操作任务", dead)
				}
				st.CleanupAppTasks(24 * time.Hour)
			}
		}
	}()

	wg.Add(1)
	go func() { // 服务集成轮询：30s 一轮（HA/qB/Emby 快照缓存，主页读缓存）
		defer wg.Done()
		intMgr.Run(ctx)
	}()

	wg.Add(1)
	go func() { // 邮件未读轮询：60s 一轮（各账户 INBOX 未读数缓存，侧栏角标）
		defer wg.Done()
		mailMgr.Run(ctx)
	}()

	wg.Add(1)
	go func() { // 用量查询轮询：每 20 分钟一轮（智谱额度/DeepSeek 余额/EdgeOne 站点用量最新快照入库）
		defer wg.Done()
		aiMgr.Run(ctx)
	}()

	wg.Add(1)
	go func() { // PT 抓取调度：06:00 每日轮 + 启动补拉 + 中转任务租约回收。
		// 必须独立 goroutine：曾排在 aiMgr.Run（跑到停机才返回的轮询）后面，
		// 调度从未启动——上线以来每日轮/补拉一次没跑过（461，2026-10-07 定案）
		defer wg.Done()
		ptMgr.Run(ctx)
	}()

	wg.Add(1)
	go func() { // 服务监控拨测：各目标按配置间隔 TCP 握手（状态最新一份入库）
		defer wg.Done()
		svcMgr.Run(ctx)
	}()

	wg.Add(1)
	go func() { // GitHub 发布轮询（466）：每 30 分钟拉各监控仓库最近 5 条 Releases 入库
		defer wg.Done()
		ghMgr.Run(ctx)
	}()

	wg.Add(1)
	go func() { // 在线更新周期检查（395）：check_interval>0 才跑；检出新版且
		// 配置允许时自动缓存 agent 分发包并分发升级
		defer wg.Done()
		updMgr.Run(ctx, cfg.Update.CheckInterval.D())
	}()

	// 请求超时：防止慢连接（slowloris）长期占用 fd/内存把服务端拖垮。
	// 上报体 ≤1MB，30s 读写超时绰绰有余。
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       75 * time.Second,
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		log.Printf("收到退出信号，正在关闭…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// 更新管理器的重启钩子（395⑧）：BackupDB=升级前 checkpoint+主库文件备份
	//（仅保留 1 份，2026-10-05 用户指定；迁移有 bug 时整库回退）；Shutdown=
	// 取消后台 ctx 并停 HTTP——之后 update.Apply 原子替换二进制并 exec，进程
	// 同 PID 换映像重启
	updMgr.SetHooks(updmgr.Hooks{
		BackupDB: func() error {
			if err := st.Checkpoint(); err != nil {
				return err
			}
			return updmgr.RotateBackup(cfg.DBPath, 1)
		},
		Shutdown: func() {
			stop()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		},
	})

	retDays, _ := st.RetentionDays() // 读取失败按默认 90 展示（清理循环里会再报错）
	log.Printf("lz-server 启动：http://%s（数据库 %s，指标保留 %d 天，离线判定 %s）",
		cfg.Listen, cfg.DBPath, retDays, cfg.OfflineAfter.D())
	// 先绑端口再 Serve：绑定成功 = 监听健康，升级标记在此刻清除（395⑧）；
	// 绑定失败走返回错误路径，main 凭标记自动 exec 旧版回滚
	ln, lnErr := net.Listen("tcp", cfg.Listen)
	if lnErr != nil {
		return lnErr
	}
	update.ClearMarker(marker)
	if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	wg.Wait() // 等后台任务退出后再关库（defer st.Close 触发 WAL checkpoint）
	log.Printf("lz-server 已退出")
	return nil
}

// runRetentionLoop 周期数据维护：7 天外明细降采样 5 分钟桶进 metrics_archive
// （行数与体积大头），归档与残留明细、消息中心按保留天数（参数设置页可改，
// 每轮重新读取）清理，已恢复告警按配置文件保留天数删除，随后
// incremental_vacuum 回收空间。
func runRetentionLoop(ctx context.Context, st *store.Store, cfg *config.Config, interval time.Duration) {
	clean := func() {
		days, err := st.RetentionDays()
		if err != nil {
			log.Printf("读取数据保留天数失败，本轮跳过: %v", err)
			return
		}
		now := time.Now().Unix()
		if n, err := st.ArchiveBefore(now - 7*86400); err != nil {
			log.Printf("降采样归档失败: %v", err)
		} else if n > 0 {
			log.Printf("已归档 %d 个 5 分钟桶（7 天外明细降采样）", n)
		}
		if n, err := st.DeleteArchiveBefore(now - int64(days)*86400); err != nil {
			log.Printf("清理过期归档失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期归档 %d 桶（保留 %d 天）", n, days)
		}
		// 兜底：归档正常时 7 天边界已删明细，此处只清保留期外的意外残留
		if n, err := st.DeleteMetricsBefore(now-int64(days)*86400, 5000); err != nil {
			log.Printf("清理过期指标失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期指标残留 %d 行", n)
		}
		if n, err := st.DeleteResolvedBefore(now - int64(cfg.AlertKeepDays)*86400); err != nil {
			log.Printf("清理过期告警失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期告警 %d 条（保留 %d 天）", n, cfg.AlertKeepDays)
		}
		// 消息中心同口径保留：登录/告警/提醒/邮件通知每条都落库，不清会无界增长
		if n, err := st.DeleteMessagesBefore(now - int64(days)*86400); err != nil {
			log.Printf("清理过期消息失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期消息 %d 条（保留 %d 天）", n, days)
		}
		// 服务监控翻转事件同口径保留（500；只留边界行本就不大，防长尾无界）
		if n, err := st.DeleteSvcEventsBefore(now - int64(days)*86400); err != nil {
			log.Printf("清理过期服务监控事件失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期服务监控事件 %d 条（保留 %d 天）", n, days)
		}
		// HA 历史数据（511/512）：采样 7 天外降采样 5 分钟桶进归档，归档/残留
		// 明细/开关事件按保留天数清理（留存口径同服务器监控，2026-10-09 用户指定）
		if n, err := st.ArchiveHASamples(now - 7*86400); err != nil {
			log.Printf("HA 采样归档失败: %v", err)
		} else if n > 0 {
			log.Printf("已归档 %d 个 HA 采样桶（7 天外明细降采样）", n)
		}
		if n, err := st.DeleteHAArchiveBefore(now - int64(days)*86400); err != nil {
			log.Printf("清理过期 HA 归档失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期 HA 归档 %d 桶（保留 %d 天）", n, days)
		}
		if n, err := st.DeleteHASamplesBefore(now - int64(days)*86400); err != nil {
			log.Printf("清理过期 HA 采样失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期 HA 采样残留 %d 行", n)
		}
		if n, err := st.DeleteHAEventsBefore(now - int64(days)*86400); err != nil {
			log.Printf("清理过期 HA 开关日志失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期 HA 开关日志 %d 条（保留 %d 天）", n, days)
		}
		// Emby 播放记录（528/530）：永久留存不清理——一次播放一行 MB 级，
		// 用户指定作为长期报表数据源
		// 天气历史（522）：采样 7 天外降采样 5 分钟桶进归档；归档固定保留 90 天
		// （天气曲线非告警数据，不随参数设置保留天数）；残留明细同口径兜底清理
		if n, err := st.ArchiveWXSamples(now - 7*86400); err != nil {
			log.Printf("天气采样归档失败: %v", err)
		} else if n > 0 {
			log.Printf("已归档 %d 个天气采样桶（7 天外明细降采样）", n)
		}
		if n, err := st.DeleteWXArchiveBefore(now - 90*86400); err != nil {
			log.Printf("清理过期天气归档失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期天气归档 %d 桶（保留 90 天）", n)
		}
		if n, err := st.DeleteWXSamplesBefore(now - int64(days)*86400); err != nil {
			log.Printf("清理过期天气采样失败: %v", err)
		} else if n > 0 {
			log.Printf("已清理过期天气采样残留 %d 行", n)
		}
		if err := st.Vacuum(); err != nil {
			log.Printf("回收数据库空间失败: %v", err)
		}
	}
	clean() // 启动立即执行一次
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			clean()
		}
	}
}
