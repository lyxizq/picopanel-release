package term

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"lz-monitor/internal/server/store"
)

// SFTP 返回一台服务器的 SFTP 客户端（按 serverID 池化，idle 10 分钟回收）。
// 适合读完即返回的短操作；下载/后台任务等长操作请用 SFTPHold，持有期间
// 空闲回收器不会回收连接。池内连接先轻量探活（Getwd 一来一回）；失活即
// 丢弃重建——长 idle 后中间设备可能已掐断 TCP，对象本身无从得知。未保存
// 的临时凭据（sftp-auth 提交）在重建时同样生效，池被回收后无需重输。
// 调用方遇连接类错误应调 DropSFTP 后重试一次（由 API 层包装）。
func (m *Manager) SFTP(serverID int64) (*sftp.Client, error) {
	cl, release, err := m.SFTPHold(serverID)
	if err != nil {
		return nil, err
	}
	release()
	return cl, nil
}

// SFTPHold 取池化连接并登记在用（用完必须调用返回的 release）。
// 大文件下载、后台压缩/解压等长操作经此入口持有连接，空闲回收器跳过
// 在用连接——否则同一台服务器无其他操作时，超 10 分钟的传输会在中途
// 被空闲线掐断。
func (m *Manager) SFTPHold(serverID int64) (*sftp.Client, func(), error) {
	srv, err := m.st.GetServer(serverID)
	if err != nil || srv == nil {
		return nil, nil, fmt.Errorf("服务器不存在")
	}
	if TermAddr(srv) == "" {
		return nil, nil, errNoTermAddr
	}
	applyCred(srv, m.ephemeralFor(serverID))

	if e := m.sftpPeek(serverID); e != nil {
		if cl, rel, ok := m.probeHold(e); ok {
			return cl, rel, nil
		}
		m.DropSFTP(serverID) // 已死：丢弃，下面重建
	}

	// 并发建连串行 + 双检：两个请求同时发现无可用连接时，只拨一条，
	// 后到者等锁期间发现先到者已建好就直接复用——否则两条连接先后入池，
	// 先入池者被覆盖成不可达的孤儿连接（永不被回收，目标机残留 sshd）。
	l := m.dialLock(serverID)
	l.Lock()
	defer l.Unlock()
	if e := m.sftpPeek(serverID); e != nil {
		if cl, rel, ok := m.probeHold(e); ok {
			return cl, rel, nil
		}
		m.DropSFTP(serverID)
	}

	e, err := m.dialInsert(serverID, srv)
	if err != nil {
		return nil, nil, err
	}
	return e.cl, e.release(), nil
}

// probeHold 探活一条池内连接：活着即刷新最近使用时间并登记在用。
func (m *Manager) probeHold(e *sftpEntry) (*sftp.Client, func(), bool) {
	e.last.Store(time.Now().Unix())
	if _, err := e.cl.Getwd(); err != nil {
		return nil, nil, false
	}
	return e.cl, e.release(), true
}

// SFTPAuth 用提交的凭据试连并替换该服务器的池化连接（文件页「连接」动作）：
// 凭据有效即可用；username 总是入库，save=true 时密码入库并清临时凭据，
// 否则存 ephem（仅进程内存，重启后需重输）。
func (m *Manager) SFTPAuth(serverID int64, cred Cred, save bool) error {
	srv, err := m.st.GetServer(serverID)
	if err != nil || srv == nil {
		return fmt.Errorf("服务器不存在")
	}
	applyCred(srv, &cred)
	m.DropSFTP(serverID) // 旧连接（可能用的旧凭据/已死）一并丢弃
	// 不做双检：本函数的语义就是用提交的新凭据真的拨一次验证有效
	if _, err := m.dialInsert(serverID, srv); err != nil {
		return err
	}
	m.persistCred(serverID, cred.Username, cred.Password, save)
	if !save {
		m.SetEphemeral(serverID, cred)
	}
	return nil
}

// dialInsert 按给定服务器配置（调用方已套好凭据覆盖）拨一条新 SSH 连接、
// 开 SFTP 通道并入池。落池时若覆盖了池内已有条目（SFTPAuth 换凭据替换、
// 或与等锁期间他人建连的条目竞争），旧连接当场关闭——保证任何时刻被替换
// 的连接都不会变成不可达的孤儿。
func (m *Manager) dialInsert(serverID int64, srv *store.Server) (*sftpEntry, error) {
	client, err := m.dialSSH(srv)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	cl, err := sftp.NewClient(client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("打开 SFTP 通道失败: %w", err)
	}
	e := &sftpEntry{ssh: client, cl: cl}
	e.last.Store(time.Now().Unix())
	m.mu.Lock()
	if old := m.sftp[serverID]; old != nil {
		old.close()
	}
	m.sftp[serverID] = e
	m.mu.Unlock()
	return e, nil
}

// sftpPeek 取池内条目（无则 nil）。
func (m *Manager) sftpPeek(serverID int64) *sftpEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sftp[serverID]
}

// dialLock 取一台服务器的建连锁（SFTPHold 串行拨号用）。
func (m *Manager) dialLock(serverID int64) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.dialMu[serverID]
	if l == nil {
		l = &sync.Mutex{}
		m.dialMu[serverID] = l
	}
	return l
}

// release 返回登记在用的解除函数（与 SFTPHold/probeHold 配对）。
func (e *sftpEntry) release() func() {
	e.inUse.Add(1)
	return func() { e.inUse.Add(-1) }
}

// unameEntry 用户名缓存条目：远端 /etc/passwd 的双向映射与加载时间。
type unameEntry struct {
	at    time.Time
	names map[uint32]string    // uid → 用户名（文件列表「所有者」列展示）
	ids   map[string][2]uint32 // 用户名 → uid,gid（修改所有者 chown 反查）
}

// unameTTL 用户名缓存有效期：远端账号不常变，文件列表每次都解析代价高。
const unameTTL = 10 * time.Minute

// loadUname 走池化 SFTP 连接读远端 /etc/passwd 并缓存 10 分钟。缓存与连接
// 生命周期解耦——账号映射不随连接重建变化，池回收后缓存照常命中。
func (m *Manager) loadUname(serverID int64) (*unameEntry, error) {
	m.unameMu.Lock()
	if e, ok := m.unameCache[serverID]; ok && time.Since(e.at) < unameTTL {
		m.unameMu.Unlock()
		return &e, nil
	}
	m.unameMu.Unlock()

	cl, err := m.SFTP(serverID)
	if err != nil {
		return nil, err
	}
	f, err := cl.Open("/etc/passwd")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, ids := parsePasswd(f)
	e := unameEntry{at: time.Now(), names: names, ids: ids}
	m.unameMu.Lock()
	m.unameCache[serverID] = e
	m.unameMu.Unlock()
	return &e, nil
}

// UserNames 返回一台服务器的 uid→用户名映射（文件列表「所有者」列展示用）。
// 任何失败返回 nil，调用方回退显示数字 uid。
func (m *Manager) UserNames(serverID int64) map[uint32]string {
	e, err := m.loadUname(serverID)
	if err != nil {
		return nil
	}
	return e.names
}

// UserIDs 返回一台服务器的 用户名→uid,gid 映射（修改所有者用）。
func (m *Manager) UserIDs(serverID int64) (map[string][2]uint32, error) {
	e, err := m.loadUname(serverID)
	if err != nil {
		return nil, err
	}
	return e.ids, nil
}

// parsePasswd 解析 /etc/passwd 内容为 uid→用户名与 用户名→uid,gid 两个映射：
// 每行 name:x:uid:gid:...，字段不足、uid 非数字或空用户名的行容错跳过。
func parsePasswd(r io.Reader) (map[uint32]string, map[string][2]uint32) {
	names := map[uint32]string{}
	ids := map[string][2]uint32{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) < 4 || fields[0] == "" {
			continue
		}
		uid, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil {
			continue
		}
		gid, err := strconv.ParseUint(fields[3], 10, 32)
		if err != nil {
			continue
		}
		names[uint32(uid)] = fields[0]
		ids[fields[0]] = [2]uint32{uint32(uid), uint32(gid)}
	}
	return names, ids
}
