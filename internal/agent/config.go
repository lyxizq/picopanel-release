// Package agent 实现监控采集端：本地周期采集 + 只读指标端口，由服务端主动抓取
// （Prometheus 拉取模式，agent 不需要知道服务端地址，也不外发任何数据）。
package agent

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lz-monitor/internal/conf"
)

// Config 是 agent 的 yaml 配置。
type Config struct {
	Mode           string        `yaml:"mode"`            // 采集数据出口：pull（缺省，服务端来抓）/ push（主动上报）
	Listen         string        `yaml:"listen"`          // pull 模式：指标端口（服务端来抓取的地址）
	ServerURL      string        `yaml:"server_url"`      // push 模式：服务端地址（如 http://10.0.0.1:8080）
	Interval       conf.Duration `yaml:"interval"`        // 内部采集周期（保持 CPU/网络差值新鲜度）
	Token          string        `yaml:"token"`           // pull=抓取密钥(X-Server-Token)；push=上报身份(X-Agent-Token)
	CollectListens *bool         `yaml:"collect_listens"` // 是否采集监听端口（缺省 true）；false=不读端口、不上报，服务端端口列表与端口告警随之停更
	Upgrade        *bool         `yaml:"upgrade"`         // 是否接受在线升级（396，缺省 true）：pull=接受服务端 POST /upgrade 推包；push=接受上报响应的升级指令。置 false 即本机退出自动升级
	LogFile        string        `yaml:"log_file"`        // 默认 "off" 零日志（什么都不写，无磁盘写入）；其他值=滚动日志文件；置空串仅终端
	LogMaxMB       int           `yaml:"log_max_mb"`      // 日志总占用上限（当前文件+备份）
}

// UpgradeEnabled 归一化后的升级开关（配置缺省 = 开启；置 false 拒绝一切升级指令）。
func (c *Config) UpgradeEnabled() bool {
	return c.Upgrade == nil || *c.Upgrade
}

// ListensEnabled 归一化后的端口采集开关（配置缺省 = 开启，兼容旧配置文件）。
func (c *Config) ListensEnabled() bool {
	return c.CollectListens == nil || *c.CollectListens
}

// defaultConfigPaths 返回默认配置查找路径：可执行文件同目录优先（把
// lz-agent 与 agent.yaml 拷到同一目录即可直接启动），其次当前工作目录。
func defaultConfigPaths() []string {
	var paths []string
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "agent.yaml"))
	}
	return append(paths, "agent.yaml")
}

// LoadConfig 解析配置：-config 指定文件，缺省依次尝试可执行文件同目录
// 与当前目录的 agent.yaml；-listen 可直接覆盖端口，便于临时调试。
func LoadConfig(args []string) (*Config, error) {
	fs := flag.NewFlagSet("lz-agent", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "配置文件路径（默认 agent 同目录 agent.yaml）")
	listen := fs.String("listen", "", "指标端口，覆盖配置文件")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg := &Config{
		Mode:     "pull",
		Listen:   "127.0.0.1:19100", // 无配置文件的裸跑默认只绑回环（指标含主机/网卡/容器清单，全接口无鉴权=内网侦察面）；跨机抓取按文档显式配 :19100 并建议配 token
		Interval: conf.Duration(10 * time.Second),
		LogFile:  "off", // 默认零日志：不写任何文件；需要日志时显式配置 log_file
		LogMaxMB: 300,
	}
	paths := []string{*cfgPath}
	if *cfgPath == "" {
		paths = defaultConfigPaths()
	} else if _, err := os.Stat(*cfgPath); err != nil {
		// 显式指定的配置文件不存在=启动参数错误，硬报错——静默回退默认值会
		// 变成「看似正常但无 token/无上报地址」的监控盲飞
		return nil, fmt.Errorf("指定的配置文件不存在: %s", *cfgPath)
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := conf.Load(p, cfg); err != nil {
			return nil, err
		}
		// 配置文件权限检查：agent.yaml 内含上报/抓取密钥，权限宽于 0600 时提醒
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o077 != 0 {
			log.Printf("警告: %s 权限为 %v（宽于 600），token 可能被同机其他用户读取，建议 chmod 600", p, fi.Mode().Perm())
		}
		// 零日志模式尽早生效：连"配置来源"行也不输出（防 nohup 落到 nohup.out）
		if strings.EqualFold(cfg.LogFile, "off") {
			log.SetOutput(io.Discard)
		}
		log.Printf("配置来源: %s", p)
		break
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	switch cfg.Mode {
	case "pull", "push":
	case "":
		cfg.Mode = "pull"
	default:
		return nil, fmt.Errorf("mode 仅支持 pull/push，当前为 %q", cfg.Mode)
	}
	if cfg.Mode == "pull" && cfg.Listen == "" {
		return nil, errors.New("pull 模式需要配置 listen（指标端口）")
	}
	if cfg.Mode == "push" {
		if cfg.ServerURL == "" {
			return nil, errors.New("push 模式需要配置 server_url（服务端地址）")
		}
		if cfg.Token == "" {
			return nil, errors.New("push 模式需要配置 token（服务端创建该服务器时生成的接入密钥）")
		}
		// 公网地址强制 https：上报响应携带任务载荷（PT Cookie/中转抓取/停容器
		// 等），明文 http 只允许私网/回环目标——公网明文=任务通道可被中间人注入
		if err := requireHTTPSForPublic(cfg.ServerURL); err != nil {
			return nil, err
		}
	}
	if cfg.Interval.D() < time.Second {
		cfg.Interval = conf.Duration(time.Second)
	}
	if cfg.LogMaxMB <= 0 {
		cfg.LogMaxMB = 300
	}
	return cfg, nil
}

func (c *Config) String() string {
	return fmt.Sprintf("mode=%s listen=%s server_url=%s interval=%s token=%s collect_listens=%v",
		c.Mode, c.Listen, c.ServerURL, c.Interval.D(), maskToken(c.Token), c.ListensEnabled())
}

// maskToken 日志里不打印完整 token。
func maskToken(t string) string {
	if t == "" {
		return "未设置"
	}
	return "已设置"
}

// requireHTTPSForPublic 校验 push 模式 server_url：https 直接放行；http 仅允许
// 私网/回环目标（IP 字面量直接判，域名解析后全部为私网才放行——解析失败按
// 私网放行并警告，避免 DNS 未就绪时锁死启动）。
func requireHTTPSForPublic(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("server_url 无法解析: %w", err)
	}
	if u.Scheme != "http" {
		return nil
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return nil
		}
		return fmt.Errorf("server_url 为公网地址时必须使用 https（当前 http://%s）：上报响应携带任务载荷，明文公网传输可被中间人注入；请改 https:// 或反代 TLS", host)
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		log.Printf("警告: server_url 域名 %s 解析失败，按私网口径放行 http（若为公网域名请改 https）", host)
		return nil
	}
	for _, ip := range ips {
		if !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() {
			return fmt.Errorf("server_url 域名 %s 解析到公网 IP，必须使用 https：上报响应携带任务载荷，明文公网传输可被中间人注入；请改 https:// 或反代 TLS", host)
		}
	}
	return nil
}
