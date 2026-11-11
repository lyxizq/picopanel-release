// 统一 API 封装：/api/v1 前缀、Bearer token、401 自动跳登录。
// 传输分层（2026-11-05 WS 通道批）：JSON 请求优先走单条 WebSocket 长连接
//（utils/ws.js，服务端进程内回环复用现有 handler），WS 未连上/传输失败回落
// 本文件的原生 fetch——24h 常开页面的轮询从此零 HTTP 请求。两类请求恒走
// HTTP：登录/登出（Set-Cookie 只在真实 HTTP 响应上生效）与 FormData 上传。
import { i18n } from '../i18n'
import { wsRequest, wsEnable, wsStop } from '../utils/ws.js'

// 纯 js 模块取当前语言文案（全局 composer，任何地方可调；t 追踪 locale）
const t = (...a) => i18n.global.t(...a)

export const BASE = '/api/v1'

export function getToken() {
  return localStorage.getItem('token') || ''
}

export function setToken(t) {
  localStorage.setItem('token', t)
}

export function getUsername() {
  return localStorage.getItem('username') || ''
}

export function setUsername(u) {
  localStorage.setItem('username', u)
}

// 登录角色（361 多用户）：admin=管理员、user=家庭成员（固定可用页面，不可配置）。
export function getRole() {
  return localStorage.getItem('role') || ''
}

export function setRole(r) {
  if (r) localStorage.setItem('role', r)
  else localStorage.removeItem('role')
}

// 后台菜单项（2026-11-03 家庭成员口径）：登录/Me 下发 permissions（成员被
// 勾选的后台模块 key 数组，null=默认全量），localStorage 记忆；可用性判定见
// utils/perms.js。
export function getPerms() {
  try {
    const a = JSON.parse(localStorage.getItem('lz-perms') || 'null')
    return Array.isArray(a) ? a : []
  } catch {
    return []
  }
}

// userKey 用户域 localStorage 键（2026-11-03）：标签页/最近打开文档等个人工作区
// 状态按登录用户独立记录，换账号互不可见（未登录回落裸键名）。
export function userKey(name) {
  const u = getUsername()
  return u ? `${name}:${u}` : name
}

// 登录落点（2026-11-03）：home=主页 / ha=智能家居（管理员按成员配置，登录下发）
export function getLanding() {
  return localStorage.getItem('lz-landing') || ''
}

export function setLanding(l) {
  if (l === 'home' || l === 'ha') localStorage.setItem('lz-landing', l)
  else localStorage.removeItem('lz-landing')
}

export function setPerms(perms) {
  if (Array.isArray(perms) && perms.length) localStorage.setItem('lz-perms', JSON.stringify(perms))
  else localStorage.removeItem('lz-perms') // null/空=默认口径（六模块全量），不落盘
}

export function getAvatar() {
  return localStorage.getItem('avatar') || ''
}

export function setAvatar(a) {
  if (a) localStorage.setItem('avatar', a)
  else localStorage.removeItem('avatar')
}

// 头像版本（6）：上传/清除头像时递增，作 /auth/avatar 的 ?v= 查询参数——
// 服务端响应 private 短缓存，变 URL 才能让下次拉取绕开旧缓存
export function getAvatarVer() {
  return localStorage.getItem('avatarVer') || '0'
}

export function bumpAvatarVer() {
  localStorage.setItem('avatarVer', String(Date.now()))
}

export function clearSession() {
  wsStop() // 放弃 WS 重连（4001 收敛在 ws 层另有一份，这里是本地登出路径）
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  localStorage.removeItem('avatar')
  localStorage.removeItem('role')
  localStorage.removeItem('lz-perms')
  localStorage.removeItem('lz-landing')
}

// 控制台侧栏菜单排序（2026-11-05 按登录用户记录）：user 域 localStorage 键即存
// 即生效，PUT /auth/menu-order 服务端持久化（登录/me 下发，换设备/换账号互不串）。
// 值 = 菜单 index 数组的 JSON 串；坏数据由 Layout loadMenuOrder 补缺/回落默认序兜底。
export function getMenuOrderRaw() {
  let raw = localStorage.getItem(userKey('nav-order'))
  if (raw === null) {
    // 旧全局裸键一次性迁移为 user 域键：仅在写入成功后才删源——配额满时
    // 先删会两头落空（旧键没了新键没写上），写失败保留旧键下次再试
    raw = localStorage.getItem('nav-order')
    if (raw !== null) {
      try {
        localStorage.setItem(userKey('nav-order'), raw)
        localStorage.removeItem('nav-order')
      } catch { /* 存储满：保留旧键待重试 */ }
    }
  }
  return raw
}

export function setMenuOrderRaw(json) {
  if (typeof json === 'string' && json) {
    try { localStorage.setItem(userKey('nav-order'), json) } catch { /* 存储满忽略 */ }
  }
}

// WS 通道不承载的端点：登录/登出要靠 HTTP 响应的 Set-Cookie 建立/清除会话
//（WS 回环应答落在服务端内存，浏览器收不到；HttpOnly Cookie 也无法 JS 侧补）
const WS_SKIP = ['/auth/login', '/auth/logout']

async function request(method, path, body) {
  const bodyStr = body === undefined ? undefined : JSON.stringify(body)
  // 超大请求体不经 WS（服务端单帧 8MB 上限，超限帧会废掉整条连接且写请求
  // 无回落）：直走 HTTP。阈值 6MB 留出帧头与 JSON 包装余量
  const wsEligible = !WS_SKIP.includes(path) && (bodyStr === undefined || bodyStr.length <= 6 * 1024 * 1024)
  if (wsEligible) {
    try {
      const r = await wsRequest(method, path, body)
      return finish(r.status, path, r.data)
    } catch (e) {
      if (!e || !e.wsDown) throw e // 业务错误（finish 已抛出）原样上行
      if (method !== 'GET') throw new Error(t('api.netError')) // 写请求不重放防双执行
      // GET 传输失败 → 回落 HTTP
    }
  }
  return httpFetch(method, path, bodyStr)
}

// finish 应答后处理（WS/HTTP 两通道共口径）：401 清会话跳登录、4xx/5xx 抛错。
// 登录接口的 401 是业务应答（密码错误 / 需要两步验证码 need_code），必须走
// 正常错误通道带回具体文案与标记，不能被会话过期拦截吞掉
function finish(status, path, data) {
  if (status === 401 && path !== '/auth/login') {
    clearSession()
    if (location.pathname !== '/login') {
      location.href = '/login'
    }
    throw new Error(t('api.notLoggedIn'))
  }
  if (status >= 400) {
    // WS 通道限流（服务端错误键固定为 "busy"）：精确匹配给业务文案，不裸显
    // 键名；其余 429（如各端点自有语义）仍原样透传服务端消息
    const msg = (data && data.error === 'busy')
      ? t('api.busy')
      : ((data && data.error) || t('api.requestFailed', { status }))
    const err = new Error(msg)
    err.status = status
    err.data = data // 附加响应体（如登录接口的 need_code 标记）
    throw err
  }
  return data
}

// httpFetch 原生 HTTP 传输（WS 回落 + 恒走 HTTP 的端点）。bodyStr 为已序列化
// 的请求体（request() 统一序列化一次传入，大体路径避免二次 stringify）。
async function httpFetch(method, path, bodyStr) {
  const headers = {}
  if (bodyStr !== undefined) headers['Content-Type'] = 'application/json'
  const token = getToken()
  if (token) headers.Authorization = 'Bearer ' + token

  let res
  try {
    res = await fetch(BASE + path, {
      method,
      headers,
      body: bodyStr,
    })
  } catch {
    throw new Error(t('api.netError'))
  }
  const data = await res.json().catch(() => ({}))
  const out = finish(res.status, path, data)
  // HTTP 200 = 会话活着：作为探活信号重启 WS 尝试（握手连败停试后的重启点；
  // 未停试时 wsEnable 是幂等空操作）
  if (res.ok) wsEnable()
  return out
}

export const api = {
  get: (path) => request('GET', path),
  post: (path, body) => request('POST', path, body),
  put: (path, body) => request('PUT', path, body),
  del: (path) => request('DELETE', path),
}

// FormData 提交（邮件发信带附件用，316）：鉴权与 401 口径同 request。
export async function apiForm(path, form) {
  const headers = {}
  const token = getToken()
  if (token) headers.Authorization = 'Bearer ' + token
  let res
  try {
    res = await fetch(BASE + path, { method: 'POST', headers, body: form })
  } catch {
    throw new Error(t('api.netError'))
  }
  if (res.status === 401) {
    clearSession()
    if (location.pathname !== '/login') location.href = '/login'
    throw new Error(t('api.notLoggedIn'))
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data.error || t('api.requestFailed', { status: res.status }))
  return data
}

// keepalive PUT：关标签页/刷新/切后台前的最后一次保存——fetch 发出后即使页面
// 卸载浏览器也会送达。受浏览器限制 body ≤64KB，更大的内容由调用方靠
// beforeunload 原生确认拦住用户（等 2s 防抖保存完成再离开）。
export function putKeepalive(path, body) {
  const headers = { 'Content-Type': 'application/json' }
  const token = getToken()
  if (token) headers.Authorization = 'Bearer ' + token
  return fetch(BASE + path, {
    method: 'PUT',
    headers,
    body: JSON.stringify(body),
    keepalive: true,
  })
}

// 文档附件上传（multipart 单文件 → doc_assets 表 BLOB 直存）。返回可直接
// 写进正文的 URL（/docs-assets/:id，img 加载走登录时种下的 HttpOnly Cookie
// 过鉴权）。响应是 wangeditor 约定的 {errno:0,data:{url}}，这里只取 url。
export async function uploadDocAsset(file) {
  const fd = new FormData()
  fd.append('file', file, file.name)
  let res
  try {
    res = await fetch(BASE + '/docs-assets', {
      method: 'POST',
      headers: { Authorization: 'Bearer ' + getToken() },
      body: fd,
    })
  } catch {
    throw new Error(t('api.netError'))
  }
  if (res.status === 401) {
    clearSession()
    if (location.pathname !== '/login') location.href = '/login'
    throw new Error(t('api.notLoggedIn'))
  }
  const data = await res.json().catch(() => ({}))
  const url = data?.data?.url
  if (!res.ok || data.errno !== 0 || !url) {
    throw new Error(data.error || t('api.uploadFailed', { status: res.status }))
  }
  return url
}

// SFTP 文件上传（multipart 单文件；多选由调用方逐个串行）。进度/取消同上，
// onProgress 同为全文件口径。断点续传：offset（远端已有字节数）→ 只发
// file.slice(offset) 起的剩余部分，服务端按 X-Upload-Offset 定位追加。
// 注意：文件下载已改走浏览器原生下载（<a> 导航 + HttpOnly Cookie 鉴权，
// 落盘/.crdownload/断点续传全由浏览器下载器管理），不再经 JS。
export function uploadFileXHR(serverID, dir, file, { onProgress, onReady, offset = 0, final, name, presliced } = {}) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `${BASE}/term/files/upload?server_id=${serverID}&path=${encodeURIComponent(dir)}`)
    const token = getToken()
    if (token) xhr.setRequestHeader('Authorization', 'Bearer ' + token)
    if (offset > 0) xhr.setRequestHeader('X-Upload-Offset', String(offset))
    if (final !== undefined) xhr.setRequestHeader('X-Upload-Final', final ? '1' : '0')
    const fd = new FormData()
    // presliced：file 已是切好的分片 blob（offset 只是绝对位置头）；缺省整文件
    // 续传语义——按 offset 自切剩余部分
    fd.append('files', presliced ? file : offset > 0 ? file.slice(offset) : file, name || file.name)
    xhr.onload = () => {
      if (xhr.status === 401) {
        clearSession()
        location.href = '/login'
        reject(new Error(t('api.notLoggedIn')))
        return
      }
      let data = {}
      try {
        data = JSON.parse(xhr.responseText)
      } catch {
        /* 非 JSON 体 */
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(data)
        return
      }
      reject(new Error(data.error || t('api.uploadFailed', { status: xhr.status })))
    }
    xhr.onerror = () => reject(new Error(t('api.netError')))
    xhr.onabort = () => {
      const err = new Error(t('api.cancelled'))
      err.code = 'cancelled' // 结构化取消标记：调用方按 code 分类，勿比对 i18n 文案
      reject(err)
    }
    if (onProgress) {
      xhr.upload.onprogress = (ev) => onProgress(offset + ev.loaded, offset + ev.total, xhr)
    }
    onReady?.(xhr)
    xhr.send(fd)
  })
}

// 分片上传包装：按 8MiB 一片顺序 POST（X-Upload-Final 标记最后一片），
// 绕开反向代理对单请求 body 的大小限制（413）。复用服务端 X-Upload-Offset
// 偏移追加通道，offset 取远端 .lzpart 已有字节数即可从断点续传。
// 服务端报偏移与临时文件大小不一致（如续传与重传交错、残片被截断）时，
// 经 realign 取远端实际大小重新对齐后继续（最多 3 次），不再要求人工重试。
// ≤单片大小走单请求整文件（行为同旧版）；onReady 每片透传最新 xhr 供取消。
const UPLOAD_CHUNK = 8 * 1024 * 1024

// 文件下载（应用内传输面板用）：XHR GET 拉远端文件为 Blob，onProgress 报
// 全文件 loaded/total，onReady 透传 xhr 供取消。完成后由调用方 objectURL
// 触发浏览器落盘——整文件驻内存，超大文件（GB 级）仍建议浏览器原生下载。
// 鉴权走 Bearer 头（同上传），不复用 HttpOnly Cookie 通道。
export function downloadFileXHR(serverID, absPath, { onProgress, onReady } = {}) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('GET', `${BASE}/term/files/download?server_id=${serverID}&path=${encodeURIComponent(absPath)}`)
    xhr.responseType = 'blob'
    const token = getToken()
    if (token) xhr.setRequestHeader('Authorization', 'Bearer ' + token)
    xhr.onload = () => {
      if (xhr.status === 401) {
        clearSession()
        location.href = '/login'
        reject(new Error(t('api.notLoggedIn')))
        return
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(xhr.response)
        return
      }
      let data = {}
      try {
        data = JSON.parse(xhr.responseText)
      } catch {
        /* 非 JSON 体 */
      }
      reject(new Error(data.error || t('api.requestFailed', { status: xhr.status })))
    }
    xhr.onerror = () => reject(new Error(t('api.netError')))
    xhr.onabort = () => {
      const err = new Error(t('api.cancelled'))
      err.code = 'cancelled'
      reject(err)
    }
    if (onProgress) xhr.onprogress = (ev) => onProgress(ev.loaded, ev.total, xhr)
    onReady?.(xhr)
    xhr.send()
  })
}

export async function uploadFileChunked(serverID, dir, file, { onProgress, onReady, offset = 0, realign } = {}) {
  let start = offset
  if (file.size - start <= UPLOAD_CHUNK) {
    return uploadFileXHR(serverID, dir, file, { onProgress, onReady, offset: start, final: true })
  }
  let realigns = 0
  while (start < file.size) {
    const end = Math.min(start + UPLOAD_CHUNK, file.size)
    try {
      await uploadFileXHR(serverID, dir, file.slice(start, end), {
        onProgress: (ld) => onProgress?.(ld, file.size),
        onReady,
        offset: start,
        final: end >= file.size,
        name: file.name,
        presliced: true,
      })
    } catch (e) {
      if (!realign || ++realigns > 3 || !/不一致/.test(e?.message || '')) throw e
      start = await realign()
      continue
    }
    start = end
  }
  return {}
}
