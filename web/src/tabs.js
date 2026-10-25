// 多标签页全局状态（模块级 reactive，项目无 pinia，沿用 utils/theme.js 模式）。
// Layout 里 watch 路由 → addTab；keep-alive include 用 cachedNames（组件 name）。
import { computed, reactive, watch } from 'vue'

// 控制台固定首 tab（监控），tab 路径都在 /console 下
export const CONSOLE_HOME = '/console'

// 路由 → tab 默认标题：存 i18n key（routes.*），渲染时 $t 即时取当前语言
//（tab 结构见 addTab/restore：固定路径带 titleKey，动态标题仍走 title 字面量）
const TITLES = {
  '/console': 'routes.console',
  '/console/term': 'routes.term',
  '/console/files': 'routes.files',
  '/console/docs': 'routes.docs',
  '/console/admin/servers': 'routes.adminServers',
  '/console/status': 'routes.status',
  '/console/reminders': 'routes.reminders',
  '/console/tv': 'routes.tv',
  '/console/toolbox': 'routes.toolbox',
  '/console/mail': 'routes.mail',
  '/console/messages': 'routes.messages',
  '/console/database': 'routes.database',
  '/console/pt': 'routes.pt',
  '/console/svcmon': 'routes.svcmon',
}

// 路由 name → 组件 name（keep-alive include 按组件名匹配）
const ROUTE2COMP = {
  dashboard: 'DashboardView',
  term: 'TermView',
  files: 'FilesView',
  docs: 'DocsView',
  'server-admin': 'ServerAdminView',
  status: 'StatusView',
  reminders: 'ReminderView',
  tv: 'TVView',
  toolbox: 'ToolboxView',
  mail: 'MailView',
  messages: 'MessagesView',
  database: 'DbView',
  pt: 'PtView',
  svcmon: 'ServiceMonView',
}

export const tabs = reactive([])
export const tabState = reactive({ activePath: '' })

// ---- 旧版路径迁移：控制台整体从 / 迁到 /console 前刷新存档里的旧 tab 路径 ----
const LEGACY_PATHS = new Set([
  '/term', '/files', '/docs', '/admin/servers', '/alert-rules',
  '/status', '/certs', '/reminders', '/toolbox',
])

// 功能并入 tab 页（555/556）：用量查询/GitHub 发布并入服务监控、告警并入消息中心
const MERGED_PATHS = {
  '/console/aiusage': '/console/svcmon',
  '/console/gh': '/console/svcmon',
  '/console/alert-rules': '/console/messages',
}

function migratePath(p) {
  if (p === '/') return CONSOLE_HOME
  if (LEGACY_PATHS.has(p)) return '/console' + p
  // 功能并入 tab 页（555/556）：旧地址存档迁移到容器页（重复映射由 restore
  // 的 seen 去重兜住，两个旧 tab 只留一个）
  if (MERGED_PATHS[p]) return MERGED_PATHS[p]
  return p // 已是 /console/...（新版本存档）或未知路径
}

// ---- 刷新恢复：tab 列表存 localStorage——刷新、浏览器崩溃恢复、关浏览器重开都能
// 找回上次的 tab 列表；不想恢复的 tab 手动关掉即可。每次增删/激活/改标题都同步写回。 ----
const STORE_KEY = 'lz-tabs'

function persist() {
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify({ tabs: [...tabs], activePath: tabState.activePath }))
  } catch {
    /* 隐私模式等存储异常：恢复功能降级，不影响正常使用 */
  }
}

function restore() {
  try {
    const data = JSON.parse(localStorage.getItem(STORE_KEY) || '')
    if (!Array.isArray(data?.tabs)) return
    // 字段校验 + closable 一律重算（「/console」固定首 tab 不可关，其余可关），不信任存储内容；
    // '/alerts' 已并入 '/console/alert-rules'（告警页）、'/docs/:id' 已并入 '/docs' 内嵌 tab、
    // '/servers/:id' 已改为弹层（不再开 tab），旧版本存下的这些 tab 丢弃不恢复
    //（文档编辑 tab 由 DocsView 自行持久化恢复）
    const seen = new Set([CONSOLE_HOME])
    const rest = []
    for (const t of data.tabs) {
      if (!t || typeof t.path !== 'string' || (!t.title && !t.titleKey) || !t.name) continue
      const path = migratePath(t.path)
      if (path === CONSOLE_HOME || path === '/alerts' || path.startsWith('/docs/') || path.startsWith('/servers/')) continue
      if (seen.has(path)) continue
      seen.add(path)
      // 固定路径的标题以当前版本为准（titleKey 随语言即时渲染），动态标题（如服务器名）保留
      const tk = TITLES[path]
      rest.push({ ...t, path, title: tk ? '' : t.title, titleKey: tk, closable: true })
    }
    tabs.push({ path: CONSOLE_HOME, title: '', titleKey: TITLES[CONSOLE_HOME], name: 'DashboardView', closable: false }, ...rest)
    tabState.activePath = typeof data.activePath === 'string' && data.activePath ? migratePath(data.activePath) : CONSOLE_HOME
  } catch {
    /* 脏数据：忽略，空态开始 */
  }
}
restore()
watch(tabs, persist, { deep: true })
watch(() => tabState.activePath, persist)

// cachedNames：keep-alive include——当前打开 tab 的组件名去重集合
export const cachedNames = computed(() => [...new Set(tabs.map((t) => t.name).filter(Boolean))])

// addTab 打开（或激活）一个 tab。已存在的 tab 只切换激活（保留视图设置的标题，
// 如服务器详情 tab 显示机器名）；「/console」固定为首个 tab 且不可关闭。
// 固定路径标题存 titleKey（i18n key，随语言即时渲染），动态路径存 title 字面量。
export function addTab(route) {
  tabState.activePath = route.path
  const exist = tabs.find((t) => t.path === route.path)
  if (exist) return
  const tk = TITLES[route.path]
  tabs.push({
    path: route.path,
    title: tk ? '' : route.path,
    titleKey: tk,
    name: ROUTE2COMP[route.name] || '',
    closable: route.path !== CONSOLE_HOME,
  })
}

// closeTab 关闭 tab，返回应激活的相邻 tab 路径（关闭的不是当前激活 tab 时返回空）。
// 「/console」不可关；全部关完回落「/console」。
export function closeTab(path) {
  const idx = tabs.findIndex((t) => t.path === path)
  if (idx < 0 || !tabs[idx].closable) return ''
  const wasActive = tabState.activePath === path
  tabs.splice(idx, 1)
  if (!wasActive) return ''
  tabState.activePath = tabs[Math.max(0, idx - 1)]?.path || CONSOLE_HOME
  return tabState.activePath
}
