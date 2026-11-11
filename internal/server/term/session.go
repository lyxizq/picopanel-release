package term

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"

	"lz-monitor/internal/server/sshcollect"
	"lz-monitor/internal/server/store"
)

// codeHostKeyMismatch 指纹与 TOFU 记录不一致的机器可读错误码（376）：
// 前端据此在失败遮罩上显示「重置指纹并重连」入口。
const codeHostKeyMismatch = "host_key_mismatch"

// errNoTermAddr 表示该服务器未配置终端可用的 SSH 地址（push 模式常见——
// agent 主动上报无服务端可达的采集地址，需配专用终端地址或连接时补填）。
var errNoTermAddr = errors.New("该服务器未配置终端 SSH 地址（推送模式请在「编辑服务器 → Web 终端/SFTP」填写专用地址）")

// 帧协议：文本消息为 JSON 控制帧，二进制消息为原始终端字节流，
// 靠 WebSocket 消息边界分帧（零自定义协议解析）。
//
//	C→S {"type":"auth","token":"..","cols":80,"rows":24,"sid":"..",
//	     "username":"..","password":"..","save_password":bool}   首帧，5s 内；
//	     username/password 为服务器未配齐凭据时用户本次输入的覆盖值（可只带其一），
//	     save_password 勾选时连接成功后密码入库（用户名总是入库）；
//	     sid 为前端 pane 实例标识：同 sid 重连 = 续接挂起中的会话（不重开 shell）
//	C→S {"type":"resize","cols":..,"rows":..} / {"type":"ping"}
//	C→S 二进制 = 终端输入（stdin）
//	S→C {"type":"ready"} / {"type":"pong"} / {"type":"error","msg":".."}
//	S→C {"type":"closed","reason":"idle|kicked|ssh-closed|shutdown|detached"}
//	S→C 二进制 = 终端输出（stdout/stderr）
type ctlFrame struct {
	Type         string `json:"type"`
	Token        string `json:"token,omitempty"`
	Sid          string `json:"sid,omitempty"`
	Cols         uint16 `json:"cols,omitempty"`
	Rows         uint16 `json:"rows,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	SavePassword bool   `json:"save_password,omitempty"`
}

// wsMsg 写泵的一条待发消息（控制帧或二进制输出）。
type wsMsg struct {
	typ  int
	data []byte
}

// Session 是一条 WebSocket ↔ SSH 终端会话（独占一条 SSH 连接）。
//
// 会话与 WS 解耦：WS 意外断开（切后台心跳间隙被反代掐、网络抖动、移动端
// 切后台杀连接等）进入挂起态——SSH 连接与 PTY 内进程继续跑，输出转入环形
// 缓冲；同 sid 重连即续接（先重放缓冲输出，再续实时流），前端配合自动重连，
// 用户切菜单/切窗口回来终端无感恢复。只有用户主动关闭（浏览器 close 帧）、
// 挂起超过 detachTTL、空闲超时、登出被踢、服务端重启才真正结束会话。
type Session struct {
	m        *Manager
	token    string // 登录会话 token：ping/读超时复验，登出或改密即踢终端
	sid      string // 前端 pane 实例标识（重连续接键）
	serverID int64
	name     string // 服务器名（日志用）

	sshClient *ssh.Client
	sshSess   *ssh.Session
	stdin     io.WriteCloser

	mu       sync.Mutex
	ptyReady bool
	cols     uint16
	rows     uint16

	writes    chan wsMsg
	closed    chan struct{}
	swapCh    chan struct{} // 重连换接唤醒（写泵 select 立刻回循环顶处理 pendingWS）
	closeOnce sync.Once
	lastAct   atomic.Int64 // unix 秒，最近一次任一方向的流量

	// 会话挂接状态（stateMu 保护）：live=当前挂接的 WS（挂起时 nil）；
	// pendingWS=已受理待写泵换接的重连 WS；detached=true 期间实时输出转存
	// outBuf（换接时整体移交重放，保证先积压后实时的严格顺序）。
	stateMu     sync.Mutex
	live        *websocket.Conn
	pendingWS   *websocket.Conn
	detached    bool
	outBuf      [][]byte
	outBytes    int
	detachTimer *time.Timer
	connGen     atomic.Int64 // WS 挂接代数：僵尸旧连接的读循环退出时不误伤新挂接

	cleanClose bool // 本条 WS 是否浏览器主动关闭（bridgeEnd 分流用，仅读泵 goroutine 访问）
}

// Serve 处理一条已升级的 WebSocket：首帧鉴权 → 同 sid 续接挂起会话（或建新
// 连接 PTY/Shell）→ 双向桥。阻塞至本条 WS 结束；WS 断开不等于会话结束
// （非浏览器主动关闭一律挂起等待同 sid 重连）。
func (m *Manager) Serve(ws *websocket.Conn, serverID int64) error {
	// 1. 首帧鉴权（token 不进 URL/日志，只在帧内传输）
	_ = ws.SetReadDeadline(time.Now().Add(authWindow))
	ws.SetReadLimit(1 << 20)
	typ, data, err := ws.ReadMessage()
	if err != nil {
		return fmt.Errorf("读取首帧: %w", err)
	}
	var auth ctlFrame
	if typ != websocket.TextMessage ||
		json.Unmarshal(data, &auth) != nil || auth.Type != "auth" || auth.Token == "" {
		ctlDirect(ws, "error", "首帧必须是 auth")
		return errors.New("首帧不是 auth")
	}
	u, err := m.st.SessionUser(auth.Token)
	if err != nil || u == nil {
		ctlDirect(ws, "error", "登录会话无效或已过期")
		return errors.New("会话无效")
	}
	// 终端属管理员能力：受限用户（361）即使持有有效会话也不可开终端
	if u.Role != store.RoleAdmin {
		ctlDirect(ws, "error", "无权限使用终端")
		return errors.New("受限用户无权使用终端")
	}

	// 2. 同 sid 有挂起中的会话 → 续接：不重新拨号，PTY 里跑着的进程不受影响，
	//    挂起期间的输出先重放再续实时流。不匹配（token 不同/服务器不同/已终态）回落建新会话
	if auth.Sid != "" {
		if s := m.bySid(auth.Sid); s != nil && s.serverID == serverID && s.reattach(ws, auth) {
			gen := s.connGen.Load()
			s.readLoop(ws)
			s.bridgeEnd(gen)
			return nil
		}
	}

	// 3. 新会话：建连 + PTY + Shell（建连耗时用户可感，日志留痕）。
	//    auth 帧带的凭据覆盖 DB 配置（服务器未配齐时用户本次输入），连接成功再落库
	sess := &Session{
		m:        m,
		token:    auth.Token,
		sid:      auth.Sid,
		serverID: serverID,
		writes:   make(chan wsMsg, 128),
		closed:   make(chan struct{}),
		swapCh:   make(chan struct{}, 1),
	}
	// fail：建桥前的任何失败——报错给前端并终结这条半成品会话
	fail := func(msg string, err error) error {
		ctlDirect(ws, "error", msg)
		sess.Close("closed")
		return err
	}
	cols, rows := orDefaultSize(auth.Cols, auth.Rows)
	sess.touch()

	srv, err := m.st.GetServer(serverID)
	if err != nil || srv == nil {
		return fail("服务器不存在", errors.New("服务器不存在"))
	}
	sess.name = srv.Name
	applyCred(srv, &Cred{Username: auth.Username, Password: auth.Password})
	client, err := m.dialSSH(srv)
	if err != nil {
		msg := err.Error()
		code := ""
		if errors.Is(err, errNoTermAddr) {
			msg = errNoTermAddr.Error()
		} else if errors.Is(err, sshcollect.ErrHostKeyMismatch) {
			code = codeHostKeyMismatch // 前端在失败遮罩提供「重置指纹并重连」（376）
		}
		ctlDirect(ws, "error", "SSH 连接失败："+msg, code)
		sess.Close("closed")
		return fmt.Errorf("server=%s 连接: %w", srv.Name, err)
	}
	sess.sshClient = client
	m.persistCred(serverID, auth.Username, auth.Password, auth.SavePassword)

	sshSess, err := client.NewSession()
	if err != nil {
		return fail("打开会话失败："+err.Error(), fmt.Errorf("server=%s NewSession: %w", srv.Name, err))
	}
	sess.sshSess = sshSess
	sess.stdin, err = sshSess.StdinPipe()
	if err != nil {
		return fail("打开输入通道失败："+err.Error(), err)
	}
	stdout, _ := sshSess.StdoutPipe()
	stderr, _ := sshSess.StderrPipe()
	if err := sshSess.RequestPty("xterm-256color", int(cols), int(rows), ssh.TerminalModes{
		ssh.ECHO: 1,
	}); err != nil {
		return fail("请求 PTY 失败："+err.Error(), fmt.Errorf("server=%s RequestPty: %w", srv.Name, err))
	}
	if err := sshSess.Shell(); err != nil {
		return fail("启动 Shell 失败："+err.Error(), fmt.Errorf("server=%s Shell: %w", srv.Name, err))
	}
	sess.mu.Lock()
	sess.ptyReady = true
	sess.cols, sess.rows = cols, rows
	sess.mu.Unlock()
	sess.setWS(ws)
	m.register(sess)
	log.Printf("term：会话开始 server=%s addr=%s user=%s", srv.Name, TermAddr(srv), srv.SSHUser)

	// 4. ready 之后双向桥：写泵（串行化全部 WS 写）、输出泵 ×2、Shell 退出兜底
	sess.sendControl("ready", "")
	go sess.writePump()
	go sess.pump(stdout)
	go sess.pump(stderr)
	go func() { _ = sshSess.Wait(); sess.Close("ssh-closed") }()

	// 5. 读循环（当前 goroutine）；结束后按断法分流（用户关闭 vs 意外中断）
	gen := sess.connGen.Load()
	sess.readLoop(ws)
	sess.bridgeEnd(gen)
	return nil
}

// bridgeEnd 本条 WS 读循环结束后的分流：浏览器主动关闭（close 帧）= 用户
// 结束会话；其余（网络断/被反代掐/超时）= 挂起等待同 sid 重连。
// connGen 与启动读循环时不同 = 本连接已被更新的重连顶掉（僵尸半开连接），
// 此刻会话归新连接所有，直接退出不动状态。
func (s *Session) bridgeEnd(gen int64) {
	if s.connGen.Load() != gen {
		return
	}
	if s.isClosed() {
		return
	}
	if s.cleanClose {
		s.Close("closed")
		return
	}
	s.detach("连接中断")
}

// readLoop 消费 WS 消息直至断开：二进制→stdin，控制帧→resize/ping。
// 每轮读超时（90s）做两件事：复验登录会话（登出/改密踢终端）、空闲检查。
func (s *Session) readLoop(ws *websocket.Conn) {
	ws.SetPongHandler(func(string) error { s.touch(); return nil }) // 服务端 ping 的回包维持活跃
	for {
		_ = ws.SetReadDeadline(time.Now().Add(wsReadTimeout))
		typ, data, err := ws.ReadMessage()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if !s.checkAlive() {
					return
				}
				continue
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
				s.cleanClose = true // 前端主动关（关 pane、组件卸载）
			}
			return // 连接断开/消息超限：分流在 bridgeEnd
		}
		s.touch()
		switch typ {
		case websocket.BinaryMessage:
			if _, err := s.stdin.Write(data); err != nil {
				return // SSH 侧已断
			}
		case websocket.TextMessage:
			var f ctlFrame
			if json.Unmarshal(data, &f) != nil {
				continue
			}
			switch f.Type {
			case "ping":
				if !s.reauth() {
					return
				}
				s.sendControl("pong", "")
			case "resize":
				s.resize(f.Cols, f.Rows)
			}
		}
	}
}

// checkAlive 读超时路径的复验：会话失效踢出，无流量超 30 分钟断开。
func (s *Session) checkAlive() bool {
	if !s.reauth() {
		return false
	}
	if time.Since(time.Unix(s.lastAct.Load(), 0)) > idleKickAfter {
		s.Close("idle")
		return false
	}
	return true
}

// reauth 复验登录会话：登出/改密（会话被删）后踢掉终端。
func (s *Session) reauth() bool {
	u, err := s.m.st.SessionUser(s.token)
	if err == nil && u != nil {
		return true
	}
	s.Close("kicked")
	return false
}

// detach 会话挂起：WS 已断但 SSH 保活，实时输出转入 outBuf，等同 sid 重连。
// 幂等（detachOnce 语义用 detached 标志实现）。挂起超时由 detachTimer 兜底回收。
func (s *Session) detach(why string) {
	s.stateMu.Lock()
	if s.detached {
		s.stateMu.Unlock()
		return
	}
	s.detached = true
	ws := s.live
	s.live = nil
	s.stateMu.Unlock()
	if ws != nil {
		_ = ws.Close() // 唤醒可能阻塞在写上的泵
	}
	if s.name != "" {
		log.Printf("term：会话挂起（等待重连） server=%s (%s)", s.name, why)
	}
	s.stateMu.Lock()
	s.detachTimer = time.AfterFunc(detachTTL, func() { s.Close("detached") })
	s.stateMu.Unlock()
}

// reattach 受理重连：复验 token 后只把新 WS 登记为 pendingWS，真正的换接、
// 缓冲移交重放与 ready 帧全部由写泵在循环顶完成——那是唯一的 WS 写方，
// 天然保证「积压输出 → ready → 实时输出」的严格顺序。
// 若旧连接仍是僵尸半开（未走完挂起流程），直接顶掉并关闭——其读循环退出时
// 经 connGen 识别为过期连接，不会误伤本次挂接。
// 返回 false 表示不匹配/已终态（调用方回落建新会话）。
func (s *Session) reattach(ws *websocket.Conn, auth ctlFrame) bool {
	if auth.Token != s.token || auth.Sid != s.sid {
		return false
	}
	s.stateMu.Lock()
	if s.isClosed() {
		s.stateMu.Unlock()
		return false
	}
	old := s.live
	parked := s.pendingWS
	s.pendingWS = ws
	s.connGen.Add(1)
	if s.detachTimer != nil {
		s.detachTimer.Stop()
		s.detachTimer = nil
	}
	s.stateMu.Unlock()
	select { // 唤醒写泵立即回循环顶换接（缓冲 1，已有待处理则不必重复）
	case s.swapCh <- struct{}{}:
	default:
	}
	if parked != nil && parked != ws {
		_ = parked.Close() // 连续两次重连：顶掉更早那个还没换接上的
	}
	if old != nil && old != ws && old != parked {
		_ = old.Close()
	}
	ws.SetPongHandler(func(string) error { s.touch(); return nil })
	s.resize(orDefaultSize(auth.Cols, auth.Rows))
	s.touch()
	if s.name != "" {
		log.Printf("term：会话重连 server=%s", s.name)
	}
	return true
}

// setWS 首次挂接（新会话建桥前）。
func (s *Session) setWS(ws *websocket.Conn) {
	s.stateMu.Lock()
	s.live = ws
	s.connGen.Add(1)
	s.stateMu.Unlock()
	ws.SetPongHandler(func(string) error { s.touch(); return nil })
}

// isClosed 会话是否已进入终态。
func (s *Session) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// resize 更新 PTY 尺寸；PTY 尚未就绪时只记录（创建时已带上）。
// 注意 WindowChange 参数序是 (h, w)——行在列前（x/crypto 惯例，容易写反）。
func (s *Session) resize(cols, rows uint16) {
	if cols == 0 || rows == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols, s.rows = cols, rows
	if s.ptyReady {
		_ = s.sshSess.WindowChange(int(rows), int(cols))
	}
}

// writePump 单 goroutine 串行写 WS（gorilla 不允许并发写），会话全生命周期
// 存活：挂接态直写 WS 并发协议层 ping（保活反代读超时）；挂起态把二进制
// 输出转存 outBuf（控制帧丢弃）；重连换接后先重放积压输出再发 ready 续实时流。
// 写失败不再终结会话而是转挂起——连接可能只是暂时不可达。
func (s *Session) writePump() {
	ping := time.NewTicker(wsPingEvery)
	defer ping.Stop()
	for {
		// 换接优先：有待挂接的重连 WS 则在此接管，顺带把挂起期积压输出
		// 整体取出重放（此时实时输出仍在入缓冲，队列序=严格时序）
		s.stateMu.Lock()
		ws := s.live
		var rp [][]byte
		reattached := false
		if s.pendingWS != nil {
			ws = s.pendingWS
			s.live = ws
			s.pendingWS = nil
			s.detached = false
			rp = s.outBuf
			s.outBuf = nil
			s.outBytes = 0
			reattached = true
			// 写泵路径的挂起可能发生在 reattach 受理之后（写泵阻塞在旧 WS 的
			// 写上，被 reattach 关闭后才报错自行 detach，此刻才启动 timer）——
			// 换接成功即会话复活，必须停掉回收 timer，否则续用的会话 10 分钟后
			// 被 detachTimer 误杀（reattach 路径停的 timer 可能还没被创建）
			if s.detachTimer != nil {
				s.detachTimer.Stop()
				s.detachTimer = nil
			}
		}
		s.stateMu.Unlock()
		for _, d := range rp {
			_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := ws.WriteMessage(websocket.BinaryMessage, d); err != nil {
				s.detach("重放失败")
				break
			}
		}
		if reattached {
			s.sendControl("ready", "") // 排在积压之后（写泵队列保序）
		}
		select {
		case <-s.swapCh: // 重连待换接：回循环顶处理 pendingWS
		case <-s.closed:
			for { // 排空剩余消息（尤其 closed 帧）后退出
				select {
				case msg := <-s.writes:
					s.stateMu.Lock()
					ws := s.live
					s.stateMu.Unlock()
					if ws == nil {
						continue // 挂起中无 WS：closed 帧无处可发，丢弃
					}
					_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
					if ws.WriteMessage(msg.typ, msg.data) != nil {
						return
					}
				default:
					return
				}
			}
		case <-ping.C:
			s.stateMu.Lock()
			ws := s.live
			s.stateMu.Unlock()
			if ws == nil {
				continue // 挂起中：无 WS 可 ping
			}
			_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				s.detach("ping 失败")
			}
		case msg := <-s.writes:
			s.stateMu.Lock()
			ws := s.live
			det := s.detached
			s.stateMu.Unlock()
			if det || ws == nil {
				if msg.typ == websocket.BinaryMessage {
					s.bufAppend(msg.data)
				}
				continue
			}
			_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := ws.WriteMessage(msg.typ, msg.data); err != nil {
				s.detach("写出失败")
				if msg.typ == websocket.BinaryMessage {
					s.bufAppend(msg.data) // 本条不丢，转缓冲
				}
			}
		}
	}
}

// bufAppend 挂起期间的输出入环形缓冲，超上限丢最旧（重连重放最近 512KB）。
func (s *Session) bufAppend(d []byte) {
	s.stateMu.Lock()
	s.outBuf = append(s.outBuf, d)
	s.outBytes += len(d)
	for s.outBytes > detachBufMax && len(s.outBuf) > 1 {
		s.outBytes -= len(s.outBuf[0])
		s.outBuf = s.outBuf[1:]
	}
	s.stateMu.Unlock()
}

// pump 把 SSH 输出搬到 WS（stdout 与 stderr 各一个 goroutine，块边界即消息边界）。
func (s *Session) pump(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			select {
			case s.writes <- wsMsg{typ: websocket.BinaryMessage, data: data}:
			case <-s.closed:
				return
			}
			s.touch()
		}
		if err != nil {
			return
		}
	}
}

// ctlDirect 建桥前的同步直写（写泵未启动，队列没有消费者）。code 可选（机器可读错误码）。
func ctlDirect(ws *websocket.Conn, typ, msg string, code ...string) {
	frame := ctlFrameOut{Type: typ, Msg: msg}
	if len(code) > 0 {
		frame.Code = code[0]
	}
	_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	_ = ws.WriteMessage(websocket.TextMessage, mustJSON(frame))
}

// sendControl 发一条控制帧（走写泵队列，与终端输出保序）。
func (s *Session) sendControl(typ, msg string) {
	data := mustJSON(ctlFrameOut{Type: typ, Msg: msg})
	select {
	case s.writes <- wsMsg{typ: websocket.TextMessage, data: data}:
	case <-s.closed:
	}
}

// Close 关闭会话（幂等）：通知前端 closed 帧后断 SSH 与 WS，并从管理器注销。
func (s *Session) Close(reason string) {
	s.closeOnce.Do(func() {
		close(s.closed)
		if reason == "idle" || reason == "kicked" || reason == "ssh-closed" || reason == "shutdown" || reason == "detached" {
			select {
			case s.writes <- wsMsg{typ: websocket.TextMessage, data: mustJSON(ctlFrameOut{Type: "closed", Reason: reason})}:
			case <-time.After(500 * time.Millisecond):
			}
		}
		if s.sshSess != nil {
			_ = s.sshSess.Close()
		}
		if s.sshClient != nil {
			_ = s.sshClient.Close()
		}
		s.stateMu.Lock()
		ws := s.live
		s.live = nil
		pw := s.pendingWS
		s.pendingWS = nil
		if s.detachTimer != nil {
			s.detachTimer.Stop()
			s.detachTimer = nil
		}
		s.stateMu.Unlock()
		if ws != nil {
			_ = ws.Close()
		}
		if pw != nil {
			_ = pw.Close()
		}
		s.m.unregister(s)
		if s.name != "" {
			log.Printf("term：会话结束 server=%s reason=%s", s.name, reason)
		}
	})
}

func (s *Session) touch() { s.lastAct.Store(time.Now().Unix()) }

// ctlFrameOut 服务端→客户端控制帧（error/closed 带 msg/reason）。
type ctlFrameOut struct {
	Type   string `json:"type"`
	Msg    string `json:"msg,omitempty"`
	Reason string `json:"reason,omitempty"`
	Code   string `json:"code,omitempty"` // 机器可读错误码（如 host_key_mismatch，376）
}

func mustJSON(f ctlFrameOut) []byte {
	b, _ := json.Marshal(f)
	return b
}

// orDefaultSize 首帧未带尺寸时按 80×24 起（ready 后前端 resize 校准）。
func orDefaultSize(cols, rows uint16) (uint16, uint16) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	return cols, rows
}
