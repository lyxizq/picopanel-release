// PicoPanel Service Worker：接收 Web Push（payload {title, body, done?}）并展示
// 系统通知；done 携带 {kind, ref} 时通知带「完成」action 按钮（2026-11-03 提醒
// 窗口直达完成：med=用药剂量 / rem=提醒事项），点击后经同源 Cookie 会话调打卡
// 端点；点击通知主体聚焦已开页面或新开首页。SW 本身不做缓存拦截。
self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = { title: 'PicoPanel', body: event.data ? event.data.text() : '' }
  }
  const opts = {
    body: data.body || '',
    icon: '/pwa-192.png',
    badge: '/pwa-192.png',
    tag: 'lz-monitor',
    renotify: true,
    data: { url: '/', done: data.done || null, title: data.title || '' },
  }
  if (data.done && data.done.ref) {
    opts.actions = [{ action: 'done', title: data.done.title || '完成' }]
  }
  event.waitUntil(self.registration.showNotification(data.title || 'PicoPanel', opts))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const data = (event.notification.data && typeof event.notification.data === 'object') ? event.notification.data : {}
  // 「完成」action：按 kind 调既有打卡端点（同源 Cookie 鉴权，浏览器会话须有效），
  // 结果以独立 tag 的短通知反馈（renotify 抢焦点提示；成功与否都不打开页面）
  if (event.action === 'done' && data.done && data.done.ref) {
    const ep = data.done.kind === 'rem'
      ? `/api/v1/reminders/${encodeURIComponent(data.done.ref)}/complete`
      : `/api/v1/meds/log-complete/${encodeURIComponent(data.done.ref)}`
    event.waitUntil(
      fetch(ep, { method: 'POST', credentials: 'include' })
        .then((r) => {
          self.registration.showNotification(r.ok ? '已完成' : '处理失败', {
            body: r.ok
              ? `${data.title || '该提醒'}已标记完成。`
              : '登录态无效或无权处理，请进面板后操作。',
            icon: '/pwa-192.png',
            badge: '/pwa-192.png',
            tag: 'lz-monitor-done',
            renotify: true,
          })
        })
        .catch(() => {
          self.registration.showNotification('处理失败', {
            body: '网络异常，请进面板后操作。',
            icon: '/pwa-192.png',
            badge: '/pwa-192.png',
            tag: 'lz-monitor-done',
            renotify: true,
          })
        }),
    )
    return
  }
  let url = data.url || '/'
  // 同源断言：推送内容驱动的导航只放行站内路径，绝对 URL/异源地址一律回落首页
  if (!String(url).startsWith('/')) url = '/'
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((list) => {
      for (const client of list) {
        if (client.url.includes(self.location.origin) && 'focus' in client) {
          client.navigate(url).catch(() => {})
          return client.focus()
        }
      }
      return self.clients.openWindow(url)
    }),
  )
})
