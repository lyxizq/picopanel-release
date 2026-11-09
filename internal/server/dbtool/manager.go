package dbtool

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"lz-monitor/internal/server/sshcollect"
	"lz-monitor/internal/server/store"
)

// ConnConfig 打开一个连接池所需的运行参数（凭据已解密）。
type ConnConfig struct {
	Host        string
	Port        int
	User        string
	Pass        string
	Database    string
	SSL         string // off / skip-verify / on
	SSLMode     string // PG 专用映射（off=disable 等）
	ReadOnly    bool
	Timeout     time.Duration
	ReadTimeout time.Duration // MySQL 读写超时（其余驱动用 ctx）
	Dial        DialFunc
}

// Manager 连接池与 SSH 隧道的管理器：按连接 id 惰性建池，配置指纹变化自动
// 重建（改配置后无需显式 Kick）。备份目录由 server.go 传（数据库同目录）。
type Manager struct {
	st  *store.Store
	dir string

	mu      sync.Mutex
	pools   map[int64]*poolEntry
	tunnels map[int64]*tunnelEntry // connID → 复用的 SSH 隧道
}

type poolEntry struct {
	db *sql.DB
	fp string // 配置指纹，变了即重建
}

type tunnelEntry struct {
	serverID int64
	client   *ssh.Client
}

// New 构造。
func New(st *store.Store, backupDir string) *Manager {
	return &Manager{st: st, dir: backupDir, pools: map[int64]*poolEntry{}, tunnels: map[int64]*tunnelEntry{}}
}

// Conn 惰性取连接池（配置指纹不符自动重建）。
func (m *Manager) Conn(ctx context.Context, id int64) (*sql.DB, *store.DBConnection, error) {
	cfg, err := m.st.GetDBConnection(id)
	if err != nil {
		return nil, nil, err
	}
	if !cfg.Enabled {
		return nil, nil, fmt.Errorf("连接已停用")
	}
	fp := cfgFingerprint(cfg)
	m.mu.Lock()
	e := m.pools[id]
	if e != nil && e.fp != fp {
		_ = e.db.Close()
		e = nil
	}
	var db *sql.DB
	if e != nil {
		db = e.db
	}
	m.mu.Unlock()
	if db == nil {
		// 建池必须在锁外：open 内含解密、SSH 隧道拨号（tunnelDial 也要拿
		// m.mu，非重入锁，持锁同步调=自死锁并拖死全部 dbtool 请求）与
		// MySQL Ping 等秒级慢 IO
		db, err = m.open(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}
		m.mu.Lock()
		if cur := m.pools[id]; cur != nil {
			if cur.fp == fp {
				_ = db.Close() // 并发他人已建好同指纹池：关自己这份，复用对方的
				db = cur.db
			} else {
				_ = cur.db.Close() // 期间指纹又变了：作废旧池，下次请求按新指纹重建
				m.pools[id] = &poolEntry{db: db, fp: fp}
			}
		} else {
			m.pools[id] = &poolEntry{db: db, fp: fp}
		}
		m.mu.Unlock()
	}
	return db, cfg, nil
}

// Drop 删除连接后清池与隧道。
func (m *Manager) Drop(id int64) {
	m.mu.Lock()
	e := m.pools[id]
	delete(m.pools, id)
	t := m.tunnels[id]
	delete(m.tunnels, id)
	m.mu.Unlock()
	if e != nil {
		_ = e.db.Close()
	}
	if t != nil {
		_ = t.client.Close()
	}
}

// Test 不入库的连通性测试（建连 + ping + 浏览权限），用后即关。
func (m *Manager) Test(ctx context.Context, cfg *store.DBConnection, user, pass string) error {
	cc, err := m.dialConfig(ctx, cfg, user, pass)
	if err != nil {
		return err
	}
	db, d, err := openDialect(cfg, cc)
	if err != nil {
		return err
	}
	defer db.Close()
	pctx, cancel := context.WithTimeout(ctx, cfgTimeout(cfg)+5*time.Second)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		return err
	}
	_, err = d.Schemas(pctx, db)
	return err
}

// Close 全量关闭（进程退出/测试收尾）。
func (m *Manager) Close() {
	m.mu.Lock()
	pools, tunnels := m.pools, m.tunnels
	m.pools, m.tunnels = map[int64]*poolEntry{}, map[int64]*tunnelEntry{}
	m.mu.Unlock()
	for _, e := range pools {
		_ = e.db.Close()
	}
	for _, t := range tunnels {
		_ = t.client.Close()
	}
}

// open 建池（调用方不得持锁：内部含慢 IO 且隧道拨号需拿 m.mu）。
func (m *Manager) open(ctx context.Context, cfg *store.DBConnection) (*sql.DB, error) {
	user, pass, err := m.st.DBConnCredentials(cfg)
	if err != nil {
		return nil, fmt.Errorf("解密凭据失败: %w", err)
	}
	cc, err := m.dialConfig(ctx, cfg, user, pass)
	if err != nil {
		return nil, err
	}
	db, _, err := openDialect(cfg, cc)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(time.Hour)
	return db, nil
}

// dialConfig 凭据 + 隧道拨号器（sqlite 无需凭据与隧道）。
func (m *Manager) dialConfig(ctx context.Context, cfg *store.DBConnection, user, pass string) (*ConnConfig, error) {
	cc := &ConnConfig{
		Host: cfg.Host, Port: cfg.Port, User: user, Pass: pass, Database: cfg.Database,
		SSL: cfg.Params.SSL,
		// TODO 330 全工具仅查询：池级只读硬约束（PG default_transaction_read_only；
		// SQLite 在 openDialect 里 mode=ro+query_only；MySQL 会话级
		// transaction_read_only，OpenMySQL 内对不支持的旧版本自动回退）。
		// 恢复写入时改回 cfg.Readonly。
		ReadOnly: true,
		Timeout:  cfgTimeout(cfg), ReadTimeout: cfgTimeout(cfg) * 3,
	}
	if cfg.Type == "sqlite" {
		return cc, nil
	}
	if cfg.Params.TunnelServerID > 0 {
		dial, err := m.tunnelDial(cfg.ID, cfg.Params.TunnelServerID, cfg.Host, cfg.Port)
		if err != nil {
			return nil, err
		}
		cc.Dial = dial
	}
	return cc, nil
}

// tunnelDial 经监控服务器 SSH 隧道连库：ssh.Client.Dial(库地址) 产 net.Conn。
// 隧道按连接 id 复用，断连后拨号失败自动重拨一次。
func (m *Manager) tunnelDial(connID, serverID int64, dbHost string, dbPort int) (DialFunc, error) {
	srv, err := m.st.GetServer(serverID)
	if err != nil {
		return nil, fmt.Errorf("隧道服务器不存在: %w", err)
	}
	addr := sshAddrOf(srv)
	if addr == "" {
		return nil, fmt.Errorf("服务器 %s 未配置 SSH 地址，无法建隧道", srv.Name)
	}
	target := net.JoinHostPort(defaultHost(dbHost), strconv.Itoa(dbPort))

	m.mu.Lock()
	t := m.tunnels[connID]
	if t != nil && t.serverID != serverID {
		_ = t.client.Close()
		t = nil
	}
	m.mu.Unlock()

	dialOnce := func() (*ssh.Client, error) {
		m.mu.Lock()
		cur := m.tunnels[connID]
		if cur != nil {
			cl := cur.client
			m.mu.Unlock()
			return cl, nil
		}
		m.mu.Unlock()
		opts := sshcollect.Options{
			Addr: addr, User: srv.SSHUser, Password: srv.SSHPassword,
			KeyPath: srv.SSHKeyPath, HostKey: srv.SSHHostKey, ServerID: srv.ID,
			Timeout: 15 * time.Second,
			OnHostKey: func(id int64, fp string) {
				_ = m.st.SetHostKey(id, fp) // TOFU，同终端/采集口径
			},
		}
		cl, err := sshcollect.Dial(opts)
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		if cur := m.tunnels[connID]; cur != nil {
			if cur.serverID == serverID {
				// 并发他人已拨好：关自己这份防跳板机孤儿 sshd，复用对方的
				cl2 := cur.client
				m.mu.Unlock()
				_ = cl.Close()
				return cl2, nil
			}
			_ = cur.client.Close() // 并发期间配置换了出口：旧隧道一并收掉防泄漏
		}
		m.tunnels[connID] = &tunnelEntry{serverID: serverID, client: cl}
		m.mu.Unlock()
		return cl, nil
	}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		_ = network
		_ = addr // 隧道下一律拨目标机视角的 target，驱动算出的公网地址无意义
		cl, err := dialOnce()
		if err == nil {
			var c net.Conn
			c, err = cl.Dial("tcp", target)
			if err == nil {
				return c, nil
			}
		}
		// 隧道可能已断：清掉重拨一次
		m.mu.Lock()
		if t := m.tunnels[connID]; t != nil {
			_ = t.client.Close()
			delete(m.tunnels, connID)
		}
		m.mu.Unlock()
		if cl2, err2 := dialOnce(); err2 == nil {
			return cl2.Dial("tcp", target)
		}
		return nil, fmt.Errorf("SSH 隧道连接 %s 失败: %w", target, err)
	}, nil
}

// sshAddrOf 服务器 SSH 地址（专用地址优先，采集地址回退并补 :22）。
func sshAddrOf(srv *store.Server) string {
	if srv.SSHAddr != "" {
		if strings.Contains(srv.SSHAddr, ":") {
			return srv.SSHAddr
		}
		return srv.SSHAddr + ":22"
	}
	if srv.AgentAddr == "" {
		return ""
	}
	h, _, err := net.SplitHostPort(srv.AgentAddr)
	if err != nil {
		h = srv.AgentAddr
	}
	return net.JoinHostPort(h, "22")
}

func defaultHost(h string) string {
	if h == "" {
		return "127.0.0.1"
	}
	return h
}

// cfgFingerprint 连接运行配置指纹（变了自动重建池）。
func cfgFingerprint(c *store.DBConnection) string {
	raw, _ := json.Marshal(struct {
		Type, Host, Database, Auth, Params string
		Port                               int
		Readonly, Enabled                  bool
	}{c.Type, c.Host, c.Database, c.Auth, mustJSON(c.Params), c.Port, c.Readonly, c.Enabled})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func cfgTimeout(c *store.DBConnection) time.Duration {
	if c.Params.TimeoutSec <= 0 {
		return 10 * time.Second
	}
	if c.Params.TimeoutSec > 60 {
		return 60 * time.Second
	}
	return time.Duration(c.Params.TimeoutSec) * time.Second
}

// openDialect 按类型组装连接池。
func openDialect(cfg *store.DBConnection, cc *ConnConfig) (*sql.DB, Dialect, error) {
	switch cfg.Type {
	case "mysql":
		db, err := OpenMySQL(cc)
		return db, mysqlDialect{}, err
	case "postgres":
		cc.SSLMode = pgSSLMode(cc.SSL)
		db, err := OpenPG(cc)
		return db, pgDialect{}, err
	case "sqlite":
		if strings.TrimSpace(cc.Database) == "" {
			return nil, nil, fmt.Errorf("SQLite 需要库文件路径")
		}
		// TODO 330 全工具仅查询：一律 mode=ro（modernc 驱动整串执行多语句，
		// rw 池曾被「SELECT 内藏分号」绕过分类写入——只读位才是兜底）。
		// 恢复写入时改回按 cfg.Readonly 分支 ro/rw。
		// query_only(1) 是第二道锁：mode=ro 只锁主库，ATTACH 挂上可写库后
		// 写操作与 VACUUM INTO 照样落盘；query_only 是连接级全库只读，且
		// PRAGMA 动词在白名单里本就按写拒绝、运行期无法反转。
		// DSN 用 URL 组装并转义路径：路径里的 ?/# 曾会串进参数区。
		dsn := url.URL{Scheme: "file", OmitHost: true, Path: cc.Database,
			RawQuery: "mode=ro&_pragma=busy_timeout%3d5000&_pragma=query_only%3d1"}
		db, err := sql.Open("sqlite", dsn.String())
		return db, sqliteDialect{}, err
	}
	return nil, nil, fmt.Errorf("不支持的数据库类型: %s", cfg.Type)
}

// pgSSLMode 面板 ssl 语义 → PG sslmode。
func pgSSLMode(ssl string) string {
	switch ssl {
	case "on":
		return "verify-full"
	case "skip-verify":
		return "require"
	default:
		return "disable"
	}
}
