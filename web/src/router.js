import { createRouter, createWebHistory } from 'vue-router'
import Layout from './components/Layout.vue'
import { getToken, getRole } from './api/index.js'

const routes = [
  { path: '/login', name: 'login', component: () => import('./views/LoginView.vue') },
  // 主页：域名根路径直接访问（独立全屏页，不带主框架；路由守卫同样要求登录）
  { path: '/', name: 'page', component: () => import('./views/PageView.vue') },
  // 智能家居（355/361）：独立全屏页，任意登录用户可访问——受限用户登录后的唯一落点
  { path: '/ha', name: 'ha', component: () => import('./views/HaView.vue') },
  // 控制台：带菜单的主框架（原根路径整体迁到 /console）
  {
    path: '/console',
    component: Layout,
    children: [
      { path: '', name: 'dashboard', component: () => import('./views/DashboardView.vue') },
      // 服务器明细已改为弹层展示（openServerDetail）；旧地址/旧书签重定向回总览
      { path: 'servers/:id', redirect: '/console' },
      { path: 'term', name: 'term', component: () => import('./views/TermView.vue') },
      { path: 'files', name: 'files', component: () => import('./views/FilesView.vue') },
      { path: 'mail', name: 'mail', component: () => import('./views/MailView.vue') },
      { path: 'messages', name: 'messages', component: () => import('./views/MessagesView.vue') },
      { path: 'database', name: 'database', component: () => import('./views/DbView.vue') },
      { path: 'aiusage', redirect: { path: '/console/svcmon', query: { tab: 'aiusage' } } },
      { path: 'pt', name: 'pt', component: () => import('./views/PtView.vue') },
      { path: 'svcmon', name: 'svcmon', component: () => import('./views/ServiceMonView.vue') },
      { path: 'gh', redirect: { path: '/console/svcmon', query: { tab: 'gh' } } },
      { path: 'docs', name: 'docs', component: () => import('./views/DocsView.vue') },
      // 文档编辑已并入「文档」页内嵌 tab；旧地址/旧书签重定向为 ?open=<id>，由 DocsView 消费
      { path: 'docs/:id', redirect: (to) => ({ path: '/console/docs', query: { open: to.params.id } }) },
      { path: 'admin/servers', name: 'server-admin', component: () => import('./views/ServerAdminView.vue') },
      { path: 'alert-rules', name: 'alert-rules', component: () => import('./views/AlertRulesView.vue') },
      // 证书监控已并入服务监控页第 5 个 tab（TODO 26）：旧地址 redirect 落对应 tab
      { path: 'certs', redirect: { path: '/console/svcmon', query: { tab: 'certs' } } },
      { path: 'status', name: 'status', component: () => import('./views/StatusView.vue') },
      { path: 'reminders', name: 'reminders', component: () => import('./views/ReminderView.vue') },
      { path: 'tv', name: 'tv', component: () => import('./views/TVView.vue') },
      { path: 'toolbox', name: 'toolbox', component: () => import('./views/ToolboxView.vue') },
      // 告警并入消息中心（556）：函数式 redirect 透传原 query（/alerts 时代的
      // status=firing 初始筛选由历史面板继续读取）
      { path: 'alert-rules', redirect: (to) => ({ path: '/console/messages', query: { ...to.query, view: 'alerts' } }) },
    ],
  },
  // 旧地址重定向（旧书签/浏览器存档 tab）：/page 曾是主页地址、其余是旧控制台地址
  { path: '/page', redirect: '/' },
  { path: '/term', redirect: '/console/term' },
  { path: '/files', redirect: '/console/files' },
  { path: '/mail', redirect: '/console/mail' },
  { path: '/database', redirect: '/console/database' },
  { path: '/docs', redirect: '/console/docs' },
  { path: '/docs/:id', redirect: (to) => ({ path: '/console/docs', query: { open: to.params.id } }) },
  { path: '/servers/:id', redirect: '/console' },
  { path: '/admin/servers', redirect: '/console/admin/servers' },
  { path: '/alert-rules', redirect: (to) => ({ path: '/console/messages', query: { ...to.query, view: 'alerts' } }) },
  { path: '/alerts', redirect: (to) => ({ path: '/console/messages', query: { ...to.query, view: 'alerts' } }) },
  { path: '/certs', redirect: '/console/certs' },
  { path: '/status', redirect: '/console/status' },
  { path: '/reminders', redirect: '/console/reminders' },
  { path: '/tv', redirect: '/console/tv' },
  { path: '/toolbox', redirect: '/console/toolbox' },
  { path: '/gh', redirect: '/console/gh' },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

// 跨版本存活的旧标签页兜底：路由 chunk 按内容哈希命名，服务端发新版后旧 hash
// 文件已不存在，挂久的标签页点菜单动态导入即报 "Failed to fetch dynamically
// imported module"（Firefox/Safari 文案不同，Vite CSS 预载失败另有文案）。整页
// 刷新一次重新拉新 index.html 即恢复；10 秒节流防服务端不可达时刷新循环。
const CHUNK_LOAD_ERR = /Failed to fetch dynamically imported module|error loading dynamically imported module|Importing a module script failed|Unable to preload CSS/
let lastChunkReloadAt = 0
router.onError((error, to) => {
  const msg = (error && error.message) || String(error)
  if (!CHUNK_LOAD_ERR.test(msg)) return
  const now = Date.now()
  if (now - lastChunkReloadAt < 10000) return
  lastChunkReloadAt = now
  window.location.assign(to && to.fullPath ? to.fullPath : window.location.href)
})

// 路由守卫：未登录一律跳登录页（主页 / 与控制台 /console 都要登录）；
// 受限用户（361 role=user）除 /ha 外的任何地址一律送回 /ha
router.beforeEach((to) => {
  if (to.path !== '/login' && !getToken()) {
    return { path: '/login', query: to.fullPath === '/' ? {} : { redirect: to.fullPath } }
  }
  if (getToken() && getRole() === 'user' && to.path !== '/ha') {
    return { path: '/ha' }
  }
})

export default router
