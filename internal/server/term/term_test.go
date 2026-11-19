package term

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"lz-monitor/internal/server/store"
)

// serveTermSSH 起一个支持密码认证 + PTY/Shell/回显 的最小 SSH 服务器。
// lastCols 记录最近一次 window-change 的列数（resize 断言用）。
// signer 由调用方（测试主协程）生成——这里跑在独立 goroutine，不能 t.Fatal。
func serveTermSSH(ln net.Listener, signer ssh.Signer, password string, lastCols *atomic.Uint32) {
	cfg := ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) != password {
				return nil, fmt.Errorf("密码错误")
			}
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			sconn, chans, reqs, err := ssh.NewServerConn(conn, &cfg)
			if err != nil {
				conn.Close()
				return
			}
			go ssh.DiscardRequests(reqs)
			for newCh := range chans {
				if newCh.ChannelType() != "session" {
					newCh.Reject(ssh.UnknownChannelType, "unsupported")
					continue
				}
				ch, chReqs, _ := newCh.Accept()
				go func(ch ssh.Channel, chReqs <-chan *ssh.Request) {
					for req := range chReqs {
						switch req.Type {
						case "pty-req":
							req.Reply(true, nil)
						case "shell":
							req.Reply(true, nil)
							go func() { // 回显：收到什么写回什么
								buf := make([]byte, 512)
								for {
									n, err := ch.Read(buf)
									if n > 0 {
										_, _ = ch.Write(buf[:n])
									}
									if err != nil {
										return
									}
								}
							}()
						case "window-change":
							var wc struct{ Cols, Rows, Xpix, Ypix uint32 }
							_ = ssh.Unmarshal(req.Payload, &wc)
							lastCols.Store(wc.Cols)
							if req.WantReply {
								req.Reply(true, nil)
							}
						case "subsystem":
							// sftp subsystem：服务测试进程的真实文件系统
							//（sftp_test.go / api 层文件操作测试用，只在 TempDir 里读写）
							var payload struct{ Subsystem string }
							if ssh.Unmarshal(req.Payload, &payload) == nil && payload.Subsystem == "sftp" {
								req.Reply(true, nil)
								if sv, err := sftp.NewServer(ch); err == nil {
									_ = sv.Serve()
									_ = ch.Close()
								}
							} else if req.WantReply {
								req.Reply(false, nil)
							}
						default:
							if req.WantReply {
								req.Reply(false, nil)
							}
						}
					}
				}(ch, chReqs)
			}
			_ = sconn.Close()
		}()
	}
}

// readCtl 读 WS 消息直到拿到指定类型的控制帧（跳过二进制输出帧）。
func readCtl(t *testing.T, ws *websocket.Conn, want string) ctlFrameOut {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; i < 50; i++ {
		_ = ws.SetReadDeadline(deadline)
		typ, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("读 WS（等 %s）: %v", want, err)
		}
		if typ != websocket.TextMessage {
			continue
		}
		var f ctlFrameOut
		if json.Unmarshal(data, &f) == nil && f.Type == want {
			return f
		}
	}
	t.Fatalf("未等到控制帧 %s", want)
	return ctlFrameOut{}
}

// readEcho 读 WS 直到收到包含 s 的二进制帧（mock 终端回显）。
func readEcho(t *testing.T, ws *websocket.Conn, s string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; i < 50; i++ {
		_ = ws.SetReadDeadline(deadline)
		typ, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("读 WS（等回显 %q）: %v", s, err)
		}
		if typ == websocket.BinaryMessage && strings.Contains(string(data), s) {
			return
		}
	}
	t.Fatalf("未等到回显 %q", s)
}

// newTermEnv 搭测试环境：临时库 + 管理员会话 + mock SSH + WS 服务。
func newTermEnv(t *testing.T) (m *Manager, wsURL func() string, srvID int64, token string, st *store.Store, lastCols *atomic.Uint32) {
	t.Helper()
	st, err := store.OpenWithKey(filepath.Join(t.TempDir(), "term.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.EnsureAdmin("admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByUsername("admin")
	if err != nil || u == nil {
		t.Fatalf("查管理员: %v %v", u, err)
	}
	token, err = st.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

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
	lastCols = &atomic.Uint32{}
	go serveTermSSH(ln, signer, "pw123", lastCols)

	srv := newSSHServer(t, st, "mock", ln.Addr().String(), "root", "", "pw123", 0)

	m = NewManager(st)
	up := websocket.Upgrader{}
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = m.Serve(ws, srv.ID)
	}))
	t.Cleanup(hs.Close)
	return m, func() string {
		return "ws" + strings.TrimPrefix(hs.URL, "http")
	}, srv.ID, token, st, lastCols
}

func dialTermWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(url, http.Header{
		"Origin": []string{strings.Replace(url, "ws", "http", 1)},
	})
	if err != nil {
		t.Fatalf("拨 WS: %v", err)
	}
	return ws
}

// TestServeLifecycle 全流程：auth→ready→回显→resize→ping/pong→登出踢出。
func TestServeLifecycle(t *testing.T) {
	_, wsURL, srvID, token, st, lastCols := newTermEnv(t)
	_ = wsURL()
	ws := dialTermWS(t, wsURL())
	defer ws.Close()

	// 首帧 auth → ready
	if err := ws.WriteJSON(ctlFrame{Type: "auth", Token: token, Cols: 100, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	readCtl(t, ws, "ready")

	// 二进制输入 → mock 回显
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte("hello-term\n")); err != nil {
		t.Fatal(err)
	}
	readEcho(t, ws, "hello-term")

	// resize → 服务端转发 window-change（轮询等 mock 收到）
	if err := ws.WriteJSON(ctlFrame{Type: "resize", Cols: 120, Rows: 40}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50 && lastCols.Load() != 120; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if lastCols.Load() != 120 {
		t.Fatalf("resize 未生效: cols=%d", lastCols.Load())
	}

	// ping → pong
	if err := ws.WriteJSON(ctlFrame{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	readCtl(t, ws, "pong")

	// 登出（会话删除）→ 下一次 ping 复验失败 → closed(kicked)
	if err := st.DeleteSession(token); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteJSON(ctlFrame{Type: "ping"}); err != nil {
		t.Fatal(err)
	}
	f := readCtl(t, ws, "closed")
	if f.Reason != "kicked" {
		t.Fatalf("登出后应踢出: %+v", f)
	}
	_ = srvID
}

// TestServeAuthFail 首帧鉴权失败：坏 token 收 error 帧后会话结束。
func TestServeAuthFail(t *testing.T) {
	_, wsURL, _, _, _, _ := newTermEnv(t)
	ws := dialTermWS(t, wsURL())
	defer ws.Close()

	if err := ws.WriteJSON(ctlFrame{Type: "auth", Token: "not-a-token"}); err != nil {
		t.Fatal(err)
	}
	f := readCtl(t, ws, "error")
	if f.Msg == "" {
		t.Fatalf("error 帧应带原因: %+v", f)
	}
}

// TestServeWrongPassword 密码错误：SSH 认证失败以 error 帧透传。
func TestServeWrongPassword(t *testing.T) {
	_, wsURL, srvID, token, st, _ := newTermEnv(t)
	// 改错密码（模拟配置错误）
	bad := "wrong"
	if err := st.UpdateServer(srvID, store.ServerUpdate{SSHPassword: &bad}); err != nil {
		t.Fatal(err)
	}
	ws := dialTermWS(t, wsURL())
	defer ws.Close()

	if err := ws.WriteJSON(ctlFrame{Type: "auth", Token: token}); err != nil {
		t.Fatal(err)
	}
	f := readCtl(t, ws, "error")
	if !strings.Contains(f.Msg, "SSH") {
		t.Fatalf("应为 SSH 连接失败: %+v", f)
	}
}

// TestServeFrameCred DB 凭据缺失/失效时 auth 帧补齐的场景：
//
//	未配用户名+DB 密码错误 → 帧带正确用户名/密码 → 连接成功 ready；
//	save=true 密码与用户名都落库；save=false 只落用户名、密码保持 DB 原值。
func TestServeFrameCred(t *testing.T) {
	t.Run("save=true 密码落库", func(t *testing.T) {
		_, wsURL, srvID, token, st, _ := newTermEnv(t)
		bad, empty := "wrong", ""
		if err := st.UpdateServer(srvID, store.ServerUpdate{SSHUser: &empty, SSHPassword: &bad}); err != nil {
			t.Fatal(err)
		}
		ws := dialTermWS(t, wsURL())
		defer ws.Close()
		if err := ws.WriteJSON(ctlFrame{Type: "auth", Token: token,
			Username: "root", Password: "pw123", SavePassword: true}); err != nil {
			t.Fatal(err)
		}
		readCtl(t, ws, "ready")
		got, err := st.GetServer(srvID)
		if err != nil || got == nil {
			t.Fatalf("查服务器: %v %v", got, err)
		}
		if got.SSHUser != "root" {
			t.Fatalf("用户名应总是落库: %q", got.SSHUser)
		}
		if got.SSHPassword != "pw123" {
			t.Fatalf("save=true 密码应落库: %q", got.SSHPassword)
		}
	})
	t.Run("save=false 密码保持 DB 原值", func(t *testing.T) {
		_, wsURL, srvID, token, st, _ := newTermEnv(t)
		bad, empty := "wrong", ""
		if err := st.UpdateServer(srvID, store.ServerUpdate{SSHUser: &empty, SSHPassword: &bad}); err != nil {
			t.Fatal(err)
		}
		ws := dialTermWS(t, wsURL())
		defer ws.Close()
		if err := ws.WriteJSON(ctlFrame{Type: "auth", Token: token,
			Username: "root", Password: "pw123"}); err != nil {
			t.Fatal(err)
		}
		readCtl(t, ws, "ready")
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
	})
}

// detachedNow 测试辅助：读取挂起标志（stateMu 保护）。
func (s *Session) detachedNow() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.detached
}

// TestServeReattach 会话挂起/重连：WS 异常断开（裸断 TCP 不发 close 帧，
// 模拟反代掐断/切后台杀连接）会话挂起而非结束；同 sid 重连换接成功（echo
// 链路照常），挂起期间的输出经缓冲重放到达；浏览器主动关闭（close 帧）
// 仍正常终结会话。
func TestServeReattach(t *testing.T) {
	m, wsURL, _, token, _, _ := newTermEnv(t)
	const sid = "pane-test-1"

	// 1. 首连
	ws1 := dialTermWS(t, wsURL())
	if err := ws1.WriteJSON(ctlFrame{Type: "auth", Token: token, Sid: sid, Cols: 100, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	readCtl(t, ws1, "ready")

	// 2. 裸断 TCP → 会话应挂起（而非结束）
	if err := ws1.UnderlyingConn().Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s := m.bySid(sid); s != nil && s.detachedNow() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s := m.bySid(sid)
	if s == nil {
		t.Fatal("异常断开后会话应仍存在（挂起等待重连）")
	}
	if !s.detachedNow() {
		t.Fatal("会话应处于挂起态")
	}

	// 3. 挂起期间的输出入缓冲（与写泵挂起分支同路径），重连后必须经重放到达
	s.bufAppend([]byte("detached-output\r\n"))

	// 4. 同 sid 重连：ready 后旧输出重放、回显链路照常
	ws2 := dialTermWS(t, wsURL())
	defer ws2.Close()
	if err := ws2.WriteJSON(ctlFrame{Type: "auth", Token: token, Sid: sid, Cols: 100, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	// 顺序严格：先重放积压输出，后 ready 帧（readEcho/readCtl 各自跳过无关帧型）
	readEcho(t, ws2, "detached-output")
	readCtl(t, ws2, "ready")
	if err := ws2.WriteMessage(websocket.BinaryMessage, []byte("after-reconnect\n")); err != nil {
		t.Fatal(err)
	}
	readEcho(t, ws2, "after-reconnect")

	// 5. 正常关闭（close 帧，同浏览器行为；gorilla 的 Conn.Close 不发帧）→ 会话终结
	if err := ws2.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := ws2.Close(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.bySid(sid) == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("主动关闭后会话应被终结")
}

// TestTermAddr 终端地址派生：专用地址优先；ssh 模式回退采集地址原样；
// agent/node 模式取采集地址 host 拼 :22（采集端口 19100/10200 不是 SSH 端口）；
// push 无目标地址不可用；裸 IPv6 由 JoinHostPort 补方括号。
func TestTermAddr(t *testing.T) {
	cases := []struct {
		srv  store.Server
		want string
	}{
		{store.Server{CollectMode: "ssh", AgentAddr: "1.2.3.4:22"}, "1.2.3.4:22"},
		{store.Server{CollectMode: "agent", AgentAddr: "1.2.3.4:19100"}, "1.2.3.4:22"},
		{store.Server{CollectMode: "node", AgentAddr: "1.2.3.4:10200"}, "1.2.3.4:22"},
		{store.Server{CollectMode: "agent", SSHAddr: "1.2.3.4:2222"}, "1.2.3.4:2222"},
		{store.Server{CollectMode: "ssh", AgentAddr: "1.2.3.4:22", SSHAddr: "5.6.7.8:22"}, "5.6.7.8:22"},
		{store.Server{CollectMode: "push"}, ""},
		{store.Server{CollectMode: "agent", AgentAddr: ""}, ""},
		{store.Server{CollectMode: "agent", AgentAddr: "fe80::1"}, "[fe80::1]:22"},
		{store.Server{CollectMode: "node", AgentAddr: "[2001:db8::1]:10200"}, "[2001:db8::1]:22"},
		{store.Server{CollectMode: "push", SSHAddr: "1.2.3.4:2222"}, "1.2.3.4:2222"}, // 专用地址 push 也可用
	}
	for i, c := range cases {
		if got := TermAddr(&c.srv); got != c.want {
			t.Fatalf("case %d: got %q want %q", i, got, c.want)
		}
	}
}
