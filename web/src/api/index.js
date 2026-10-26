// 统一 API 封装：/api/v1 前缀、Bearer token、401 自动跳登录。
import { i18n } from '../i18n'

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

// 登录角色（361 多用户）：admin=管理员、user=受限用户（仅开放 /ha 页面）。
export function getRole() {
  return localStorage.getItem('role') || ''
}

export function setRole(r) {
  if (r) localStorage.setItem('role', r)
  else localStorage.removeItem('role')
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
  localStorage.removeItem('token')
  localStorage.removeItem('username')
  localStorage.removeItem('avatar')
  localStorage.removeItem('role')
}

async function request(method, path, body) {
  const headers = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const token = getToken()
  if (token) headers.Authorization = 'Bearer ' + token

  let res
  try {
    res = await fetch(BASE + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new Error(t('api.netError'))
  }

  // 登录接口的 401 是业务应答（密码错误 / 需要两步验证码 need_code），
  // 必须走下方正常错误通道带回具体文案与标记，不能被会话过期拦截吞掉
  if (res.status === 401 && path !== '/auth/login') {
    clearSession()
    if (location.pathname !== '/login') {
      location.href = '/login'
    }
    throw new Error(t('api.notLoggedIn'))
  }

  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const err = new Error(data.error || t('api.requestFailed', { status: res.status }))
    err.status = res.status
    err.data = data // 附加响应体（如登录接口的 need_code 标记）
    throw err
  }
  return data
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
