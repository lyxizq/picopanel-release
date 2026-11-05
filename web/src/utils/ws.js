// 单条 WebSocket 长连接（/api/v1/ws）：api 请求经此通道直达服务端（进程内
// 回环复用现有 handler，鉴权/家庭闸/滑动续期与 HTTP 同路），断线自动回落
// HTTP——24h 常开页面的轮询从此零 HTTP 请求。另接收三类事件反向推送：
// messages.unread（未读数）/ mail.badge（邮件角标变更）/ weather.changed（天气采样）。
// 登录态靠同源 HttpOnly 会话 Cookie（握手自动携带，SameSite=Strict 跨站不带）。
import { getToken, clearSession, BASE } from '../api/index.js'

const listeners = new Map() // topic → Set<fn>
let sock = null
let seq = 1
const pending = new Map() // id → { resolve, reject, timer }
let wantConnect = false
let backoffMs = 1000
let connectTimer = null
let handshakeFails = 0

function wsURL() {
  return (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/v1/ws'
}

export function wsConnected() {
  return !!sock && sock.readyState === WebSocket.OPEN
}

// wsEnable 允许建连（登录后/HTTP 探活成功后调用；未登录不发握手）。
export function wsEnable() {
  if (typeof WebSocket === 'undefined') return
  wantConnect = true
  schedule(0)
}

// wsStop 停止并放弃重连（登出/清会话）。
export function wsStop() {
  wantConnect = false
  clearTimeout(connectTimer)
  const s = sock
  sock = null
  if (s) {
    s.onclose = null
    s.onmessage = null
    s.onerror = null
    try { s.close() } catch { /* 已断 */ }
  }
  failAllPending()
}

function schedule(delay) {
  clearTimeout(connectTimer)
  connectTimer = setTimeout(connect, delay)
}

function connect() {
  if (!wantConnect || sock || !getToken()) return
  let opened = false
  const s = new WebSocket(wsURL())
  sock = s
  s.onopen = () => {
    opened = true
    backoffMs = 1000
    handshakeFails = 0
  }
  s.onmessage = (e) => {
    let msg
    try { msg = JSON.parse(e.data) } catch { return }
    const data = decodeBody(msg)
    if (msg.t === 'res') {
      const p = pending.get(msg.id)
      if (p) {
        clearTimeout(p.timer)
        pending.delete(msg.id)
        if (!msg.status) {
          p.reject(wsDown()) // 异常应答帧（缺 status）按传输故障处理，GET 回落 HTTP
        } else {
          p.resolve({ status: msg.status, data })
        }
      }
    } else if (msg.t === 'event') {
      emit(msg.topic, data)
    }
  }
  s.onclose = (e) => {
    if (sock !== s) return
    sock = null
    failAllPending()
    if (e.code === 4001) {
      // 服务端判会话失效（登出/改密踢会话/过期）：同 api 层 401 口径
      wantConnect = false
      clearSession()
      if (location.pathname !== '/login') location.href = '/login'
      return
    }
    if (!opened) {
      // 握手连败（边缘未开 WS 代理等）：停试走 HTTP 回落，任何 HTTP 200
      //（探活/对账）都会再 wsEnable 重启尝试
      handshakeFails++
      if (handshakeFails >= 3) {
        wantConnect = false
        return
      }
    }
    schedule(Math.round(backoffMs * (0.8 + Math.random() * 0.4))) // 退避 + 抖动
    backoffMs = Math.min(backoffMs * 2, 60000)
  }
  s.onerror = () => { /* 收敛到 onclose */ }
}

// wsRequest 经 WS 发一次 API 请求，resolve { status, data }（与 HTTP 应答同形）。
// 传输失败/超时统一 reject { wsDown: true }（GET 由调用方回落 HTTP；写请求
// 不重放——服务端可能已执行，重试会双写）。
export function wsRequest(method, path, body) {
  if (!wsConnected()) return Promise.reject(wsDown())
  const id = seq++
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      if (pending.delete(id)) reject(wsDown())
    }, 20000)
    pending.set(id, { resolve, reject, timer })
    try {
      sock.send(JSON.stringify({ t: 'req', id, method, path, body: body === undefined ? undefined : body }))
    } catch {
      clearTimeout(timer)
      pending.delete(id)
      reject(wsDown())
    }
  })
}

// onWSEvent 订阅推送事件，返回取消订阅函数。
export function onWSEvent(topic, fn) {
  if (!listeners.has(topic)) listeners.set(topic, new Set())
  listeners.get(topic).add(fn)
  return () => listeners.get(topic)?.delete(fn)
}

function emit(topic, data) {
  const set = listeners.get(topic)
  if (set) for (const fn of set) { try { fn(data) } catch { /* 订阅方自兜 */ } }
}

// decodeBody 应答/事件体：服务端 Body 列是 json.RawMessage 内嵌（已随外层
// JSON 解析成 JS 值）；b64 兜底支是 base64（非 JSON 端点，常规轮询不走）。
function decodeBody(msg) {
  if (msg.body == null) return {}
  if (!msg.b64) return msg.body
  try {
    const bytes = Uint8Array.from(atob(msg.body), (ch) => ch.charCodeAt(0))
    return JSON.parse(new TextDecoder().decode(bytes))
  } catch {
    return {}
  }
}

function wsDown() {
  return Object.assign(new Error('ws down'), { wsDown: true })
}

function failAllPending() {
  for (const p of pending.values()) {
    clearTimeout(p.timer)
    p.reject(wsDown())
  }
  pending.clear()
}

// 低频 HTTP 对账（5 分钟）：①会话 Cookie 续期（WS 回环的 Set-Cookie 落在
// 服务端内存，浏览器侧续期靠这条真实请求）；②未读数兜底（推送丢了也不 stale）；
// ③HTTP 200 顺带 wsEnable——握手连败停试后由它重启 WS 尝试。
setInterval(() => {
  if (!getToken() || location.pathname === '/login') return
  fetch(BASE + '/messages/unread', { headers: { Authorization: 'Bearer ' + getToken() } })
    .then((res) => {
      if (!res.ok) return null
      wsEnable()
      return res.json().catch(() => null)
    })
    .then((data) => {
      if (data && typeof data.count === 'number') emit('messages.unread', data)
    })
    .catch(() => { /* 网络异常静默，下个周期再试 */ })
}, 5 * 60 * 1000)
