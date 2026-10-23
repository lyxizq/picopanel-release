// Package config 定义服务端配置（独立成包供 server 与 api 共用，避免循环导入）。
package config

import (
	"flag"
	"fmt"
	"os"
	"time"

	"lz-monitor/internal/conf"
)

// Config 是服务端 yaml 配置。全部字段有默认值，无配置文件也能启动（手动运行友好）。
// 注意：指标数据保留天数不再在此配置，改为 Web「参数设置」页运行期修改（存 DB，默认 90 天）。
type Config struct {
	Listen         string        `yaml:"listen"`
	DBPath         string        `yaml:"db_path"`
	AlertKeepDays  int           `yaml:"alert_keep_days"` // 已恢复告警保留天数
	OfflineAfter   conf.Duration `yaml:"offline_after"`   // 超过该时长无成功抓取判定离线
	ScrapeInterval conf.Duration `yaml:"scrape_interval"` // 抓取 agent 的周期
	ScrapeTimeout  conf.Duration `yaml:"scrape_timeout"`  // 单次抓取超时
	LogFile        string        `yaml:"log_file"`        // 滚动日志文件；置空串可关闭文件日志
	LogMaxMB       int           `yaml:"log_max_mb"`      // 日志总占用上限（当前文件+备份）
	SnapshotToken  string        `yaml:"snapshot_token"`  // 只读快照接口令牌；空=不开通（见 /api/v1/snapshot）
	WebAllow       []string      `yaml:"web_allow"`       // Web 访问白名单：条目支持 IP/CIDR/域名（域名按访问用的 Host 头匹配，忽略大小写与端口）；空 = 不限制。例外：agent 上报接口
	RealIPHeader   string        `yaml:"real_ip_header"`  // 真实客户端 IP 请求头（如阿里 ESA 托管转换注入的 Ali-Real-Client-Ip）；空=取 TCP 对端
	TrustedProxies []string      `yaml:"trusted_proxies"` // 可信代理网段（IP/CIDR）：仅这些对端注入的 real_ip_header 被采信；空=一律不采信（直连可伪造，fail-closed）
	CookieSecure   bool          `yaml:"cookie_secure"`   // 会话 Cookie 加 Secure 属性（仅 HTTPS 可传）；HTTPS/反代 TLS 部署建议打开，纯 HTTP 内网保持关
	ResetUser      bool          `yaml:"-"`               // -resetuser 启动参数：重置第一个管理员为默认账号（只能来自命令行，不从 yaml 读）
	ResetTOTP      bool          `yaml:"-"`               // -resettotp 启动参数：清除两步验证配置（Authenticator 丢失的恢复入口，只能来自命令行，不从 yaml 读）
	Update         UpdateConfig  `yaml:"update"`          // 在线更新（395）：源列表/通道/检查周期/agent 自动分发
}

// UpdateConfig 在线更新配置（TODO 395/396）。全部可缺省：sources 为空=功能关闭，
// check_interval 为 0=只手动检查（后台按钮），agents 缺省=检出新版后自动分发给 agent。
type UpdateConfig struct {
	Sources       []string      `yaml:"sources"`        // 源列表按序回退：URL 目录（静态 manifest）或 "github:owner/repo"
	Channel       string        `yaml:"channel"`        // stable（缺省）| beta（GitHub 源 = prerelease）
	CheckInterval conf.Duration `yaml:"check_interval"` // 自动检查周期；0=仅后台手动检查
	Agents        *bool         `yaml:"agents"`         // 检出新版后自动升级名下 agent（缺省 true；agent 无状态重启无感）
}

// DefaultConfig 返回默认配置：当前目录建库、10s 抓取
// （agent 与 ssh 两种采集方式共用该间隔）。
func DefaultConfig() *Config {
	return &Config{
		Listen:         ":8080",
		DBPath:         "lz-monitor.db",
		AlertKeepDays:  90,
		OfflineAfter:   conf.Duration(2 * time.Minute),
		ScrapeInterval: conf.Duration(10 * time.Second),
		ScrapeTimeout:  conf.Duration(10 * time.Second),
		LogFile:        "lz-server.log",
		LogMaxMB:       50, // 2026-10-03 精简：50MB 对单体面板绰绰有余（错误口径日志，不再刷协议流）
	}
}

// LoadConfig 解析配置：-config 指定 yaml 文件（可选，不存在直接用默认值）；
// -resetuser 重置管理员账号、-resettotp 清除两步验证（均为命令行恢复入口，见 server.Run）。
func LoadConfig(args []string) (*Config, error) {
	fs := flag.NewFlagSet("lz-server", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "配置文件路径（可选，默认全默认值）")
	resetUser := fs.Bool("resetuser", false, "重置管理员为 admin/admin123（忘记登录名/密码时使用）")
	resetTOTP := fs.Bool("resettotp", false, "清除登录两步验证配置（Authenticator 丢失时使用，之后密码可直接登录）")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	cfg := DefaultConfig()
	if *cfgPath != "" {
		if _, err := os.Stat(*cfgPath); err == nil {
			if err := conf.Load(*cfgPath, cfg); err != nil {
				return nil, err
			}
		}
	}
	// 填充零值字段，保证配置文件缺项时仍有合理默认
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "lz-monitor.db"
	}
	if cfg.AlertKeepDays <= 0 {
		cfg.AlertKeepDays = 90
	}
	if cfg.OfflineAfter.D() <= 0 {
		cfg.OfflineAfter = conf.Duration(2 * time.Minute)
	}
	if cfg.ScrapeInterval.D() < 2*time.Second {
		cfg.ScrapeInterval = conf.Duration(10 * time.Second)
	}
	if cfg.ScrapeTimeout.D() <= 0 || cfg.ScrapeTimeout.D() >= cfg.ScrapeInterval.D() {
		cfg.ScrapeTimeout = conf.Duration(cfg.ScrapeInterval.D() / 2)
	}
	if cfg.LogMaxMB <= 0 {
		cfg.LogMaxMB = 300
	}
	if cfg.Update.Channel == "" {
		cfg.Update.Channel = "stable"
	}
	if cfg.Update.Channel != "stable" && cfg.Update.Channel != "beta" {
		return nil, fmt.Errorf("update.channel 仅支持 stable/beta，当前为 %q", cfg.Update.Channel)
	}
	if cfg.Update.Agents == nil {
		on := true
		cfg.Update.Agents = &on
	}
	cfg.ResetUser = *resetUser
	cfg.ResetTOTP = *resetTOTP
	return cfg, nil
}
