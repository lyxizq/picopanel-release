// Package term 提供 Web SSH 终端与 SFTP 文件管理：WebSocket ↔ SSH 桥接。
//
// 连接拓扑：终端每个会话独占一条 *ssh.Client（tab 关闭即 Close，不挤占
// 目标机 MaxSessions，也不复用采集连接池——采集失败 Close 会连带断掉
// 用户终端）；SFTP 按 serverID 复用独立连接（idle 10 分钟回收）。
// 建连统一走 sshcollect.Dial：认证方式（密码/私钥）与 TOFU 指纹校验
// 和采集完全一致。
package term

import (
	"context"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"lz-monitor/internal/server/sshcollect"
	"lz-monitor/internal/server/store"
)

// 超时与空闲策略（常量定死，不进配置）：
//   - sshDialTimeout：终端/SFTP 建连超时（用户在页面等，不宜过长）
//   - wsReadTimeout：WS 读超时（TCP 死连接检测；超时后复验会话再续读，不直接断）
//   - wsWriteTimeout：单次 WS 写阻塞上限（写超时即断，防慢客户端拖死服务端）
//   - idleKickAfter：无任何输入输出流量持续 30 分钟才断（tail -f 等只读会话不算空闲）
//   - authWindow：WS 连上后首帧 auth 的等待上限
//   - detachTTL：WS 意外断开后会话挂起等待同 sid 重连的时长，超时回收
//   - wsPingEvery：服务端 WS 协议层 ping 间隔（浏览器自动回 pong：穿过反代
//     读超时、探测死链转挂起、维持会话活跃）
//   - detachBufMax：挂起期间输出缓冲上限（超出丢最旧，重连时重放）
//   - sftpIdleClose：SFTP 连接空闲回收时间
const (
	sshDialTimeout = 15 * time.Second
	wsReadTimeout  = 90 * time.Second
	wsWriteTimeout = 30 * time.Second
	idleKickAfter  = 30 * time.Minute
	authWindow     = 5 * time.Second
	detachTTL      = 10 * time.Minute
	wsPingEvery    = 25 * time.Second
	detachBufMax   = 512 << 10
	sftpIdleClose  = 10 * time.Minute
	reapInterval   = time.Minute
)

// Manager 管理全部终端会话与 SFTP 连接池。
type Manager struct {
	st *store.Store

	mu       sync.Mutex
	sessions map[*Session]struct{}
	sids     map[string]*Session // 前端 pane 标识 → 挂起/在线会话（重连续接键）
	sftp     map[int64]*sftpEntry
	ephem    map[int64]Cred        // 连接时提交且未保存的凭据（仅进程内存，重启即失）
	dialMu   map[int64]*sync.Mutex // 每服务器一把建连锁：并发取连接串行拨号，防止多余连接（见 sftp.go）

	unameMu    sync.Mutex
	unameCache map[int64]unameEntry // serverID → uid→用户名（远端 /etc/passwd，带 TTL，见 sftp.go）
}

// Cred 一次连接的凭据覆盖：空字段 = 沿用服务器配置里的值。
// 来源是前端连接时输入（终端 auth 帧 / sftp-auth），不落日志不进 URL。
type Cred struct {
	Username string
	Password string
}

// applyCred 把覆盖值套到内存副本上（不落库；落库由 persistCred 在连接成功后决定）。
func applyCred(srv *store.Server, c *Cred) {
	if c == nil {
		return
	}
	if c.Username != "" {
		srv.SSHUser = c.Username
	}
	if c.Password != "" {
		srv.SSHPassword = c.Password
	}
}

// SetEphemeral / ClearEphemeral 维护一台服务器的临时凭据（sftp-auth 未勾保存时写入；
// 勾选保存成功后清——DB 已是权威来源；服务器配置变更/删除时也清）。
func (m *Manager) SetEphemeral(serverID int64, c Cred) {
	m.mu.Lock()
	m.ephem[serverID] = c
	m.mu.Unlock()
}

func (m *Manager) ClearEphemeral(serverID int64) {
	m.mu.Lock()
	delete(m.ephem, serverID)
	m.mu.Unlock()
}

// ephemeralFor 取一台服务器的临时凭据（无则返回 nil）。
func (m *Manager) ephemeralFor(serverID int64) *Cred {
	m.mu.Lock()
	c, ok := m.ephem[serverID]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return &c
}

// persistCred 连接成功后按用户选择落库：用户名总是保存（不敏感，下次免输）；
// 密码仅勾选保存时入库。写库失败只记日志——连接已建立，不影响本次使用。
func (m *Manager) persistCred(serverID int64, username, password string, savePassword bool) {
	if username != "" {
		if err := m.st.UpdateServer(serverID, store.ServerUpdate{SSHUser: &username}); err != nil {
			log.Printf("term：保存 SSH 用户名失败 server=%d: %v", serverID, err)
		}
	}
	if password != "" && savePassword {
		if err := m.st.UpdateServer(serverID, store.ServerUpdate{SSHPassword: &password}); err != nil {
			log.Printf("term：保存 SSH 密码失败 server=%d: %v", serverID, err)
		}
		m.ClearEphemeral(serverID) // DB 已是权威来源
	}
}

// NewManager 创建管理器；随后调用 Run 启动空闲回收循环。
func NewManager(st *store.Store) *Manager {
	return &Manager{
		st:         st,
		sessions:   map[*Session]struct{}{},
		sids:       map[string]*Session{},
		sftp:       map[int64]*sftpEntry{},
		ephem:      map[int64]Cred{},
		dialMu:     map[int64]*sync.Mutex{},
		unameCache: map[int64]unameEntry{},
	}
}

// Run 阻塞运行空闲回收（SFTP 池 10 分钟无使用即断开），ctx 取消时
// 关闭全部终端会话与 SFTP 连接后返回。
func (m *Manager) Run(ctx context.Context) {
	defer m.Shutdown()
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reapSFTP()
		}
	}
}

// Shutdown 关闭全部终端会话与 SFTP 连接（进程退出时调用）。
func (m *Manager) Shutdown() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for s := range m.sessions {
		sessions = append(sessions, s)
	}
	entries := make([]*sftpEntry, 0, len(m.sftp))
	for _, e := range m.sftp {
		entries = append(entries, e)
	}
	m.sftp = map[int64]*sftpEntry{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.Close("shutdown")
	}
	for _, e := range entries {
		e.close()
	}
}

// register / unregister 维护会话集合（Shutdown 时逐一关闭）与 sid 重连索引。
func (m *Manager) register(s *Session) {
	m.mu.Lock()
	m.sessions[s] = struct{}{}
	if s.sid != "" {
		m.sids[s.sid] = s
	}
	m.mu.Unlock()
}

func (m *Manager) unregister(s *Session) {
	m.mu.Lock()
	delete(m.sessions, s)
	if s.sid != "" && m.sids[s.sid] == s {
		delete(m.sids, s.sid)
	}
	m.mu.Unlock()
}

// bySid 查挂起/在线中的会话（重连续接用；查不到或已注销返回 nil）。
func (m *Manager) bySid(sid string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sids[sid]
}

// TermAddr 派生 Web 终端/SFTP 使用的 SSH 地址：专用地址优先；push 模式没有
// 目标机地址（agent 主动上报进来），返回空=不可用；其余模式回退采集地址——
// ssh 模式地址本身就是 SSH 地址原样用，agent/node 模式取采集地址的 host 拼 :22
// （采集端口 19100/10200 不是 SSH 端口，必须换成 22）。
func TermAddr(srv *store.Server) string {
	if srv.SSHAddr != "" {
		return srv.SSHAddr
	}
	switch srv.CollectMode {
	case "push":
		return ""
	case "ssh":
		return srv.AgentAddr
	default: // agent / node
		if srv.AgentAddr == "" {
			return ""
		}
		return net.JoinHostPort(hostOf(srv.AgentAddr), "22")
	}
}

// hostOf 取地址的 host 部分。带端口（含 [IPv6]:port 形式）去掉端口；
// 不带端口（裸域名/裸 IPv6）SplitHostPort 报错则原样返回（随后
// JoinHostPort 会给裸 IPv6 补上方括号）。
func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// dialSSH 按服务器配置建立一条独立 SSH 连接（终端与 SFTP 共用入口）。
// TOFU 首连学习指纹入库，与采集路径行为一致。
func (m *Manager) dialSSH(srv *store.Server) (*ssh.Client, error) {
	addr := TermAddr(srv)
	if addr == "" {
		return nil, errNoTermAddr
	}
	return sshcollect.Dial(sshcollect.Options{
		Addr:     addr,
		User:     srv.SSHUser,
		Password: srv.SSHPassword,
		KeyPath:  srv.SSHKeyPath,
		HostKey:  srv.SSHHostKey,
		ServerID: srv.ID,
		Timeout:  sshDialTimeout,
		OnHostKey: func(id int64, fp string) {
			if err := m.st.SetHostKey(id, fp); err != nil {
				log.Printf("term：记录主机指纹失败 server=%s: %v", srv.Name, err)
				return
			}
			log.Printf("term：已记录 SSH 主机指纹 server=%s addr=%s fp=%s（TOFU）", srv.Name, addr, fp)
		},
	})
}

// reapSFTP 回收空闲 SFTP 连接（同一把锁内筛选，关闭在锁外）。
// 在用连接（SFTPHold 登记中，如大文件下载、后台压缩/解压）跳过——
// 长操作期间不算空闲，防止传输中途被空闲线掐断。
func (m *Manager) reapSFTP() {
	cutoff := time.Now().Add(-sftpIdleClose).Unix()
	m.mu.Lock()
	var stale []*sftpEntry
	for id, e := range m.sftp {
		if e.inUse.Load() > 0 {
			continue
		}
		if e.last.Load() < cutoff {
			stale = append(stale, e)
			delete(m.sftp, id)
		}
	}
	m.mu.Unlock()
	for _, e := range stale {
		e.close()
	}
}

// DropSFTP 丢弃一台服务器的池化连接（探活失败/操作报连接错误时由 API 层调用，下次重建）。
func (m *Manager) DropSFTP(serverID int64) {
	m.mu.Lock()
	e := m.sftp[serverID]
	delete(m.sftp, serverID)
	m.mu.Unlock()
	if e != nil {
		e.close()
	}
}

// sftpEntry 一台服务器的池化 SFTP 连接（底层独立 *ssh.Client）。
type sftpEntry struct {
	ssh   *ssh.Client
	cl    *sftp.Client
	last  atomic.Int64 // unix 秒，最近使用时间
	inUse atomic.Int32 // 正在执行长操作的持有计数（SFTPHold）；>0 时空闲回收跳过
}

func (e *sftpEntry) close() {
	_ = e.cl.Close()
	_ = e.ssh.Close()
}
