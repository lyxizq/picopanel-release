package term

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"lz-monitor/internal/server/store"
)

// TestSFTPAuthFlow 文件页「连接」的凭据链路：
//
//	DB 密码错误 → SFTP() 连不上；
//	SFTPAuth 错密码 → 拒绝；
//	SFTPAuth 正确密码 save=false → 用户名落库、密码只进 ephem（DB 不变），
//	  池内连接立即可用，池被丢弃重建后 ephem 依然生效；
//	SFTPAuth save=true → 密码落库、ephem 清除，之后仅凭 DB 也能重连。
func TestSFTPAuthFlow(t *testing.T) {
	m, _, srvID, _, st, _ := newTermEnv(t)
	bad, empty := "wrong", ""
	if err := st.UpdateServer(srvID, store.ServerUpdate{SSHUser: &empty, SSHPassword: &bad}); err != nil {
		t.Fatal(err)
	}

	// DB 凭据失效：池化建连失败
	if _, err := m.SFTP(srvID); err == nil {
		t.Fatal("DB 错密码时 SFTP() 应失败")
	}
	// 错密码提交：拒绝且不留任何状态
	if err := m.SFTPAuth(srvID, Cred{Username: "root", Password: "nope"}, false); err == nil {
		t.Fatal("错密码 SFTPAuth 应失败")
	}

	// 正确密码，不保存
	if err := m.SFTPAuth(srvID, Cred{Username: "root", Password: "pw123"}, false); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetServer(srvID)
	if got == nil {
		t.Fatal("查服务器失败")
	}
	if got.SSHUser != "root" {
		t.Fatalf("用户名应落库: %q", got.SSHUser)
	}
	if got.SSHPassword != bad {
		t.Fatalf("save=false 密码不应落库: %q", got.SSHPassword)
	}
	if m.ephemeralFor(srvID) == nil {
		t.Fatal("save=false 应写入 ephem")
	}

	// 池内连接可用（sftp subsystem 服务真实 FS，列 TempDir 验证）
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cl, err := m.SFTP(srvID)
	if err != nil {
		t.Fatalf("SFTPAuth 后 SFTP() 应可用: %v", err)
	}
	infos, err := cl.ReadDir(dir)
	if err != nil || len(infos) != 1 || infos[0].Name() != "a.txt" {
		t.Fatalf("列目录: %v %+v", err, infos)
	}

	// 池丢弃重建：ephem 覆盖仍生效（无需重新 SFTPAuth）
	m.DropSFTP(srvID)
	cl2, err := m.SFTP(srvID)
	if err != nil {
		t.Fatalf("池重建应走 ephem: %v", err)
	}
	if _, err := cl2.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatalf("ephem 重建后应能访问文件: %v", err)
	}

	// 勾选保存：密码落库、ephem 清除
	if err := m.SFTPAuth(srvID, Cred{Username: "root", Password: "pw123"}, true); err != nil {
		t.Fatal(err)
	}
	got2, _ := st.GetServer(srvID)
	if got2 == nil || got2.SSHPassword != "pw123" {
		t.Fatalf("save=true 密码应落库: %+v", got2)
	}
	if m.ephemeralFor(srvID) != nil {
		t.Fatal("save=true 后 ephem 应清除")
	}
	// 仅凭 DB 凭据重连
	m.DropSFTP(srvID)
	if _, err := m.SFTP(srvID); err != nil {
		t.Fatalf("save=true 后应仅凭 DB 重连: %v", err)
	}
}

// countingListener 统计 mock SSH 服务端实际接受的 TCP 连接数
// （并发建连回归用：连接拨了几条要从服务侧数，池状态看不见被覆盖的孤儿）。
type countingListener struct {
	net.Listener
	n *atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.n.Add(1)
	}
	return c, err
}

// TestSFTPHoldConcurrentSingleDial 并发建连回归：多个请求同时首次取同一台
// 服务器的连接，只应拨一条 SSH 连接、池内恰一条目、全员拿到同一客户端。
// 修复前的竞态是各拨一条、先后入池时先入者被覆盖成不可达孤儿连接
// （永不被空闲回收器扫到，目标机残留空闲 sshd 进程直到监控端重启）。
func TestSFTPHoldConcurrentSingleDial(t *testing.T) {
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "sftp.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cln := &countingListener{Listener: ln, n: &atomic.Int32{}}
	go serveTermSSH(cln, signer, "pw123", &atomic.Uint32{})

	srv := newSSHServer(t, st, "mock", ln.Addr().String(), "root", "", "pw123", 0)
	m := NewManager(st)
	t.Cleanup(m.Shutdown)

	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	cls := make([]*sftp.Client, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			cl, release, err := m.SFTPHold(srv.ID)
			if err != nil {
				t.Errorf("并发射手 %d: %v", i, err)
				return
			}
			defer release()
			if _, err := cl.Getwd(); err != nil { // 拿到就能用
				t.Errorf("并发射手 %d Getwd: %v", i, err)
				return
			}
			cls[i] = cl
		}(i)
	}
	close(start)
	wg.Wait()

	m.mu.Lock()
	poolLen := len(m.sftp)
	m.mu.Unlock()
	if poolLen != 1 {
		t.Fatalf("池内应恰一条连接，实际 %d", poolLen)
	}
	for i := 1; i < n; i++ {
		if cls[i] == nil || cls[i] != cls[0] {
			t.Fatalf("并发取到的客户端应一致（射手 %d）", i)
		}
	}
	if got := cln.n.Load(); got != 1 {
		t.Fatalf("mock SSH 服务端应只接受 1 条连接，实际 %d（孤儿连接泄漏）", got)
	}
}

// TestParsePasswd /etc/passwd 解析：常规行取 uid→用户名，畸形行容错跳过。
func TestParsePasswd(t *testing.T) {
	in := strings.NewReader(strings.Join([]string{
		"root:x:0:0:root:/root:/bin/bash",
		"daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin",
		"www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin",
		"bad-line-without-colons",
		":x:33:33:empty-name:/:",  // 空用户名跳过
		"oops:x:not-a-uid:1::/:/", // uid 非数字跳过
		"short:x:1",               // 字段不足跳过
		"",                        // 空行跳过
	}, "\n"))
	got, ids := parsePasswd(in)
	want := map[uint32]string{0: "root", 1: "daemon", 33: "www-data"}
	if len(got) != len(want) {
		t.Fatalf("parsePasswd = %v, want %v", got, want)
	}
	for uid, name := range want {
		if got[uid] != name {
			t.Fatalf("uid %d = %q, want %q", uid, got[uid], name)
		}
	}
	// 反查映射：用户名 → uid,gid
	if ids["root"] != [2]uint32{0, 0} || ids["www-data"] != [2]uint32{33, 33} {
		t.Fatalf("ids 反查不符: %v", ids)
	}
	if _, ok := ids["oops"]; ok {
		t.Fatalf("uid 非数字的行不应进入反查映射")
	}
}
