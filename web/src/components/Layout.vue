<template>
  <el-container class="layout">
    <!-- 桌面端：固定侧栏（可折叠为仅图标；图标彩色化，名称悬停提示） -->
    <el-aside v-if="!isMobile" :width="navCollapsed ? '64px' : '156px'" class="aside">
      <div class="brand brand-link" :title="$t('layout.openHome')" @click="openHome">
        <img class="brand-logo" src="/favicon.svg" alt="PicoPanel" /><span v-if="!navCollapsed">PicoPanel</span>
      </div>
      <el-menu :default-active="activeMenu" class="menu" :collapse="navCollapsed" :collapse-transition="false" @select="onMenuSelect">
        <el-menu-item
          v-for="(m, i) in menuItems" :key="m.index" :index="m.index"
          :style="{ '--mi-c': m.color }"
          draggable="true"
          :class="{ 'nav-dragging': navDragIndex === i, 'nav-over': navOverIndex === i && navDragIndex !== -1 && navDragIndex !== i }"
          @dragstart="navDragStart(i, $event)"
          @dragover="navDragOver(i, $event)"
          @drop="navDrop(i)"
          @dragend="navDragEnd"
        >
          <el-icon :size="17" style="color: var(--mi-c)"><component :is="m.icon" /></el-icon><span>{{ $t(m.labelKey) }}</span><span v-if="m.index === '/console/mail' && mailBadge > 0" class="mail-badge">{{ mailBadge > 99 ? '99+' : mailBadge }}</span>
        </el-menu-item>
      </el-menu>
      <div class="menu-toggle">
        <!-- 直接 import 的图标组件（不走全局名解析，杜绝静默不渲染）+ 描边按钮样式保证可见 -->
        <button type="button" class="menu-collapse" :title="navCollapsed ? $t('layout.expandMenu') : $t('layout.collapseMenu')" @click="toggleNav">
          <el-icon :size="16"><Expand v-if="navCollapsed" /><Fold v-else /></el-icon>
        </button>
      </div>
    </el-aside>

    <!-- 手机端：抽屉导航 -->
    <el-drawer append-to-body
      v-else
      v-model="drawer"
      direction="ltr"
      :with-header="false"
      size="156px"
      class="nav-drawer"
    >
      <div class="brand brand-link" :title="$t('layout.openHome')" @click="openHome">
        <img class="brand-logo" src="/favicon.svg" alt="PicoPanel" /><span>PicoPanel</span>
      </div>
      <el-menu :default-active="activeMenu" class="menu" @select="onMenuSelect">
        <el-menu-item v-for="m in menuItems" :key="m.index" :index="m.index" :style="{ '--mi-c': m.color }">
          <el-icon :size="17" style="color: var(--mi-c)"><component :is="m.icon" /></el-icon><span>{{ $t(m.labelKey) }}</span><span v-if="m.index === '/console/mail' && mailBadge > 0" class="mail-badge">{{ mailBadge > 99 ? '99+' : mailBadge }}</span>
        </el-menu-item>
      </el-menu>
    </el-drawer>


    <el-container>
      <el-header class="header">
        <div class="header-left">
          <el-icon v-if="isMobile" class="burger" @click="drawer = true">
            <Expand />
          </el-icon>
          <!-- 全局统计：靠左带背景方框，与登录用户名同一行，所有页面可见 -->
          <span v-if="stats.total > 0" class="top-stats">
            <span class="ts-item">{{ $t('layout.statsServers') }} <b>{{ stats.total }}</b></span>
            <span class="ts-item ok">{{ $t('layout.statsOnline') }} <b>{{ stats.online }}</b></span>
            <span class="ts-item" :class="{ warn: stats.offline > 0 }">{{ $t('layout.statsOffline') }} <b>{{ stats.offline }}</b></span>
            <span class="ts-item" :class="{ bad: stats.firing > 0 }">{{ $t('layout.statsAlerts') }} <b>{{ stats.firing }}</b></span>
            <span
              v-if="stats.cert_expiring > 0"
              class="ts-item warn ts-cert"
              :title="$t('layout.certTip')"
              @click="router.push({ path: '/console/svcmon', query: { tab: 'certs' } })"
            >{{ $t('layout.statsCerts') }} <b>{{ stats.cert_expiring }}</b></span>
          </span>
        </div>
        <div class="header-right">
          <!-- 用户下拉：消息中心（543 起跳独立页面）与主题切换收进菜单，用户名右上角红点提示未读 -->
          <el-dropdown @command="onCommand">
          <span class="user" :title="unread ? $t('layout.unreadN', { n: unread }) : $t('layout.menu')">
            <i v-if="unread" class="user-dot" />
            <img v-if="avatar" :src="avatar" class="user-avatar" :alt="$t('common.avatar')" />
            <el-icon v-else class="user-icon"><UserFilled /></el-icon>
            {{ username || $t('common.user') }}
            <el-icon><ArrowDown /></el-icon>
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="messages">
                <span class="dd-row">
                  <el-icon><Bell /></el-icon>
                  <span>{{ $t('routes.messages') }}</span>
                  <span v-if="unread" class="dd-badge">{{ unread > 99 ? '99+' : unread }}</span>
                </span>
              </el-dropdown-item>
              <el-dropdown-item command="theme:noop">
                <span class="dd-themes">
                  <button
                    v-for="t in THEME_SWATCHES"
                    :key="t.key"
                    type="button"
                    class="dd-swatch"
                    :class="{ cur: theme === t.key }"
                    :title="$t('theme.' + t.key + '_tip')"
                    @click.stop="setTheme(t.key)"
                  >
                    <span class="dd-swatch-dot" :style="{ background: t.bg, boxShadow: t.ring }" />
                    <span class="dd-swatch-label">{{ $t('theme.' + t.key) }}</span>
                  </button>
                </span>
              </el-dropdown-item>
              <el-dropdown-item command="reload">
                <span class="dd-row">
                  <el-icon><RefreshRight /></el-icon>
                  <span>{{ $t('layout.refreshPage') }}</span>
                </span>
              </el-dropdown-item>
              <el-dropdown-item command="settings" divided>
                <span class="dd-row">
                  <el-icon><Setting /></el-icon>
                  <span>{{ $t('routes.status') }}</span>
                </span>
              </el-dropdown-item>
              <el-dropdown-item command="profile">
                <span class="dd-row">
                  <el-icon><UserFilled /></el-icon>
                  <span>{{ $t('settings.tabs.profile') }}</span>
                </span>
              </el-dropdown-item>
              <el-dropdown-item command="logout" divided>
                <span class="dd-row">
                  <el-icon><SwitchButton /></el-icon>
                  <span>{{ $t('layout.logout') }}</span>
                </span>
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
          </el-dropdown>
        </div>
      </el-header>
      <!-- 多标签页：点左侧菜单开 tab，切 tab 保活（keep-alive）；
           「监控」固定首个不可关，其余可关（点 × 或双击 tab），关后激活相邻 -->
      <div class="tabbar">
        <div
          v-for="t in tabs"
          :key="t.path"
          class="tab"
          :class="{ active: t.path === tabState.activePath }"
          @click="router.push(t.path)"
          @dblclick="t.closable && onCloseTab(t.path)"
          @contextmenu.prevent="openTabCtx($event, t)"
        >
          <span class="tab-title">{{ t.titleKey ? $t(t.titleKey) : t.title }}</span>
          <el-icon v-if="t.closable" class="tab-close" @click.stop="onCloseTab(t.path)"><Close /></el-icon>
        </div>
      </div>
      <!-- 标签右键菜单：关闭标签 / 关闭全部 / 关闭其他（监控固定页不受影响） -->
      <teleport to="body">
        <div
          v-if="tabCtx.show"
          class="tab-ctx-menu"
          :style="{ left: tabCtx.x + 'px', top: tabCtx.y + 'px' }"
          @mousedown.stop
        >
          <button type="button" :disabled="!tabCtx.tab?.closable" @click="tabCtxClose">{{ $t('layout.ctxClose') }}</button>
          <button type="button" @click="tabCtxCloseAll">{{ $t('layout.ctxCloseAll') }}</button>
          <button type="button" @click="tabCtxCloseOthers">{{ $t('layout.ctxCloseOthers') }}</button>
        </div>
      </teleport>
      <el-main class="main">
        <router-view v-slot="{ Component, route: viewRoute }">
          <keep-alive :include="cachedNames" :max="12">
            <component :is="Component" :key="viewRoute.path" />
          </keep-alive>
        </router-view>
      </el-main>
    </el-container>

    <ServerDetailDialog />
  </el-container>
</template>

<script setup>
import { computed, defineAsyncComponent, h, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { Close, Expand, Fold, RefreshRight, SwitchButton, UserFilled } from '@element-plus/icons-vue'
import { useIsMobile } from '../composables/useIsMobile.js'
import { useRoute, useRouter } from 'vue-router'
import { api, clearSession, getAvatar, getAvatarVer, getToken, getUsername, setAvatar } from '../api/index.js'
import { setTheme, theme, THEMES, THEME_SWATCHES } from '../utils/theme.js'
import { tabs, tabState, cachedNames, addTab, closeTab } from '../tabs.js'
// 详情弹窗及其图表链路异步加载：不把 echarts 拖进控制台首屏 chunk
const ServerDetailDialog = defineAsyncComponent(() => import('./ServerDetailDialog.vue'))

// 终端菜单图标：显示器内嵌 SSH 终端窗口（屏幕里 > 与光标线），描线风格随
// Element Plus 图标（currentColor），颜色跟随主题文字色（亮色主题下即黑色）
const TermMonIcon = () =>
  h('svg', {
    viewBox: '0 0 24 24', width: '1em', height: '1em',
    fill: 'none', stroke: 'currentColor', 'stroke-width': '1.7',
    'stroke-linecap': 'round', 'stroke-linejoin': 'round',
  }, [
    h('rect', { x: '2', y: '3.5', width: '20', height: '13', rx: '1.6' }),
    h('path', { d: 'M9 20.5h6M12 16.5v4' }),
    h('path', { d: 'M6.2 7.4l2.7 2.2-2.7 2.2' }),
    h('path', { d: 'M11.6 12.3h5.2' }),
  ])

// 服务器管理菜单图标：机架服务器（上下两层各带电源指示灯与插槽线），描线风格
// 同 TermMonIcon（currentColor，颜色跟随主题文字色）
const ServerRackIcon = () =>
  h('svg', {
    viewBox: '0 0 24 24', width: '1em', height: '1em',
    fill: 'none', stroke: 'currentColor', 'stroke-width': '1.7',
    'stroke-linecap': 'round', 'stroke-linejoin': 'round',
  }, [
    h('rect', { x: '2', y: '3.5', width: '20', height: '7', rx: '1.5' }),
    h('rect', { x: '2', y: '13.5', width: '20', height: '7', rx: '1.5' }),
    h('path', { d: 'M5.2 7h.01M5.2 17h.01' }),
    h('path', { d: 'M9.5 7h9.5M9.5 17h9.5' }),
  ])

// 菜单树：桌面侧栏与手机抽屉共用；icon 为全局注册的图标组件名（main.js，
// 终端/服务器管理为上方自定义组件对象），color 为菜单图标彩色化配色（明暗主题下同一套）。
// label 存 i18n key（routes.*，模板 $t 即时跟随语言）；默认菜单序（403 起 nav-order
// 本地记忆可拖拽覆盖：存序合法项按存序、新增项按默认序补尾、下线项剔除，解析失败整体回落默认序）
const DEFAULT_MENU_ITEMS = [
  { index: '/console', icon: 'Monitor', labelKey: 'routes.console', color: '#409eff' },
  { index: '/docs', icon: 'Notebook', labelKey: 'routes.docs', color: '#67c23a' },
  { index: '/term', icon: TermMonIcon, labelKey: 'routes.term', color: 'var(--ink)' },
  { index: '/files', icon: 'FolderOpened', labelKey: 'routes.files', color: '#14b8c4' },
  { index: '/reminders', icon: 'AlarmClock', labelKey: 'routes.reminders', color: '#e6a23c' },
  { index: '/console/messages', icon: 'BellFilled', labelKey: 'routes.messages', color: '#eb2f96' },
  { index: '/console/tv', icon: 'VideoPlay', labelKey: 'routes.tv', color: '#e1306c' },
  { index: '/console/mail', icon: 'Message', labelKey: 'routes.mail', color: '#3b82f6' },
  { index: '/console/database', icon: 'Coin', labelKey: 'routes.database', color: '#10b981' },
  { index: '/console/pt', icon: 'Trophy', labelKey: 'routes.pt', color: '#e0524d' },
  { index: '/console/svcmon', icon: 'Connection', labelKey: 'routes.svcmon', color: '#0ea5e9' },
  { index: '/toolbox', icon: 'Suitcase', labelKey: 'routes.toolbox', color: '#f2721c' },
  { index: '/admin/servers', icon: ServerRackIcon, labelKey: 'routes.adminServers', color: '#9b6ef3' },
  { index: '/status', icon: 'Setting', labelKey: 'routes.status', color: '#8a919f' },
]

// 菜单顺序（403）：本地记忆 + 桌面侧栏可拖拽调整
function loadMenuOrder() {
  try {
    const saved = JSON.parse(localStorage.getItem('nav-order') || '[]')
    if (Array.isArray(saved) && saved.length) {
      const byIdx = new Map(DEFAULT_MENU_ITEMS.map((m) => [m.index, m]))
      const out = saved.map((i) => byIdx.get(i)).filter(Boolean)
      for (const m of DEFAULT_MENU_ITEMS) {
        if (!out.some((x) => x.index === m.index)) out.push(m)
      }
      return out
    }
  } catch { /* 坏数据回落默认序 */ }
  return [...DEFAULT_MENU_ITEMS]
}
const menuItems = ref(loadMenuOrder())
const navDragIndex = ref(-1)
const navOverIndex = ref(-1)
function navDragStart(i, e) {
  navDragIndex.value = i
  e.dataTransfer.effectAllowed = 'move'
  e.dataTransfer.setData('text/plain', String(i)) // Firefox 需非空 data 才起拖
}
function navDragOver(i, e) {
  e.preventDefault()
  e.dataTransfer.dropEffect = 'move'
  navOverIndex.value = i
}
function navDrop(i) {
  const from = navDragIndex.value
  if (from >= 0 && from !== i) {
    const arr = menuItems.value
    const [it] = arr.splice(from, 1)
    arr.splice(i, 0, it)
    localStorage.setItem('nav-order', JSON.stringify(arr.map((m) => m.index)))
  }
  navDragIndex.value = -1
  navOverIndex.value = -1
}
function navDragEnd() {
  navDragIndex.value = -1
  navOverIndex.value = -1
}

// 桌面侧栏折叠（仅图标），记忆到本地
const navCollapsed = ref(localStorage.getItem('nav-collapsed') === '1')
function toggleNav() {
  navCollapsed.value = !navCollapsed.value
  localStorage.setItem('nav-collapsed', navCollapsed.value ? '1' : '0')
}

const route = useRoute()
const router = useRouter()
const username = ref(getUsername())
const avatar = ref(getAvatar())

// 头像初值取本地缓存；/auth/me 已不背头像（6，data URL 曾让 me 请求 184KB/17s）：
// 用户名走 /auth/me，头像另走 /auth/avatar（private 短缓存 + ?v= 破缓存）异步
// 回填不阻塞首屏；个人信息页改名/换头像后通过 window 事件同步到这里
async function loadMe() {
  try {
    const me = await api.get('/auth/me')
    username.value = me.username || ''
  } catch {
    return // 401 已由 api 层跳转登录
  }
  try {
    const r = await api.get(`/auth/avatar?v=${getAvatarVer()}`)
    if ((r.avatar || '') !== getAvatar()) {
      setAvatar(r.avatar || '')
      avatar.value = r.avatar || ''
    }
  } catch { /* 头像拉取失败沿用本地缓存 */ }
}
function onProfileUpdated(e) {
  username.value = e.detail?.username || getUsername()
}
function onAvatarChanged() {
  avatar.value = getAvatar()
}

// 路由变化 → 打开/激活 tab（登录页在 Layout 外，不会进来）
watch(
  () => route.path,
  () => addTab(route),
  { immediate: true },
)

// 主题色板（269）：三圆形色标 + 下方文字，点击即切换（当前主题高亮描边）
function onCloseTab(path) {
  const next = closeTab(path)
  if (next && next !== route.path) router.push(next)
}

// ---- 标签右键菜单（255）：关闭当前 / 全部 / 其他；监控为固定首页不可关 ----
const tabCtx = reactive({ show: false, x: 0, y: 0, tab: null })
function openTabCtx(e, t) {
  tabCtx.tab = t
  tabCtx.x = Math.min(e.clientX, window.innerWidth - 150)
  tabCtx.y = Math.min(e.clientY, window.innerHeight - 120)
  tabCtx.show = true
  // 延后挂关闭监听：挂早了会被当前这个 contextmenu 事件的冒泡立刻触发（菜单秒关）
  setTimeout(() => {
    document.addEventListener('mousedown', closeTabCtx, { once: true })
    document.addEventListener('contextmenu', closeTabCtx, { once: true })
  })
}
function closeTabCtx() {
  tabCtx.show = false
}
function tabCtxClose() {
  if (tabCtx.tab?.closable) onCloseTab(tabCtx.tab.path)
  closeTabCtx()
}
function tabCtxCloseAll() {
  const cur = tabState.activePath
  for (const t of tabs.filter((x) => x.closable)) closeTab(t.path)
  if (!tabs.some((x) => x.path === cur)) router.push(tabState.activePath || '/console')
  closeTabCtx()
}
function tabCtxCloseOthers() {
  const keep = tabCtx.tab
  const cur = tabState.activePath
  for (const t of tabs.filter((x) => x.closable && x.path !== keep?.path)) closeTab(t.path)
  if (keep && cur !== keep.path) router.push(keep.path)
  closeTabCtx()
}

// 手机端（≤768px）侧栏改抽屉；回到桌面时收起抽屉
const isMobile = useIsMobile()

// 邮件未读角标（317）：读 Manager 轮询缓存（/mail/badge 不现查 IMAP），60s 一拉
const mailBadge = ref(0)
let mailBadgeTimer = null
async function refreshMailBadge() {
  if (!getToken()) return
  try {
    const r = await api.get('/mail/badge')
    let total = 0
    for (const v of Object.values(r.unread || {})) if (v > 0) total += v
    mailBadge.value = total
  } catch { /* 静默：角标失败不打扰 */ }
}
const drawer = ref(false)
watch(isMobile, (m) => {
  if (!m) drawer.value = false
})

const activeMenu = computed(() => route.path)

// 菜单点击：页内路由（主页入口在左上角品牌区）
// 452：进「邮件/提醒事项」菜单即把消息中心对应未读提醒标已读
//（mail=邮件提醒、reminder=待办提醒），静默执行后刷新顶栏未读角标
function onMenuSelect(index) {
  drawer.value = false
  if (index === '/console/mail') markMsgTypeRead('mail')
  else if (index === '/reminders') markMsgTypeRead('reminder')
  router.push(index)
}
async function markMsgTypeRead(t) {
  try {
    await api.post(`/messages/read-all?type=${t}`)
    loadUnread()
  } catch { /* 静默：不打扰导航 */ }
}

// 品牌区（图标+PicoPanel 文字）点击：当前窗口打开主页（373 用户指定，与侧栏菜单跳转同口径）
function openHome() {
  router.push('/')
}

// 顶栏全局统计（不分分组）：总数/在线/离线/告警中，30s 自动刷新
// 走专用轻量端点 /stats（~100 字节）；此前全量拉 /servers 会与总览页刷新
// 叠着每周期触发整套最新快照查询与大 JSON 传输
const stats = ref({ total: 0, online: 0, offline: 0, firing: 0, cert_expiring: 0 })
let statsTimer = null
async function loadStats() {
  try {
    stats.value = await api.get('/stats')
  } catch {
    /* 401 已由 api 层跳转登录 */
  }
}

// 消息中心（543）：列表/明细/已读/删除/清空已迁往独立页面 MessagesView
//（/console/messages），这里只留顶栏未读角标——30s 轮询（后台标签跳过）+
// 监听页面广播的 messages-changed 即时刷新；452 的进邮件/提醒菜单自动已读也在本页触发
const unread = ref(0)
let unreadTimer = null
async function loadUnread() {
  if (document.hidden) return
  try {
    const r = await api.get('/messages/unread')
    unread.value = r.count || 0
  } catch {
    /* 会话过期由 api 层处理 */
  }
}

// 后台标签页统一静默：三个常驻轮询到点只跳过不打请求，回前台由
// visibilitychange 立即补一次（具名监听器，卸载时移除）
function refreshMailBadgeVisible() {
  if (!document.hidden) refreshMailBadge()
}
// 消息中心页内已读/删除/清空后广播：顶栏角标即时跟进（543）
function onMessagesChanged() {
  loadUnread()
}
onMounted(() => {
  refreshMailBadge()
  mailBadgeTimer = setInterval(refreshMailBadge, 60_000)
  document.addEventListener('visibilitychange', refreshMailBadgeVisible)
  loadStats()
  loadMe()
  loadUnread()
  window.addEventListener('messages-changed', onMessagesChanged)
  window.addEventListener('profile-updated', onProfileUpdated)
  window.addEventListener('avatar-changed', onAvatarChanged)
  statsTimer = setInterval(() => { if (!document.hidden) loadStats() }, 5 * 60 * 1000)
  unreadTimer = setInterval(loadUnread, 30 * 1000)
})
onUnmounted(() => {
  clearInterval(statsTimer)
  clearInterval(unreadTimer)
  clearInterval(mailBadgeTimer)
  document.removeEventListener('visibilitychange', refreshMailBadgeVisible)
  window.removeEventListener('messages-changed', onMessagesChanged)
  window.removeEventListener('profile-updated', onProfileUpdated)
  window.removeEventListener('avatar-changed', onAvatarChanged)
})

async function onCommand(cmd) {
  if (cmd === 'profile') {
    router.push({ path: '/console/status', query: { tab: 'profile' } })
  } else if (cmd === 'settings') {
    router.push('/console/status')
  } else if (cmd === 'messages') {
    // 消息中心改独立页面（543）：下拉项直接跳页，不再弹窗
    router.push('/console/messages')
  } else if (cmd.startsWith('theme:')) {
    // theme:noop = 色板容器项（269），点击在色标按钮上自行处理，不落任何主题
    const t = cmd.slice(6)
    if (THEMES.includes(t)) setTheme(t)
  } else if (cmd === 'reload') {
    location.reload()
  } else if (cmd === 'logout') {
    try {
      await api.post('/auth/logout')
    } catch {
      /* 会话可能已过期，直接清理本地即可 */
    }
    clearSession()
    router.replace('/login')
  }
}
</script>

<style scoped>
.layout {
  height: 100vh;
}
.aside {
  background: var(--surface);
  border-right: 1px solid var(--border);
  display: flex; /* 纵向：品牌 / 菜单（弹性滚动）/ 折叠按钮 */
  flex-direction: column;
  transition: width 0.2s;
  overflow: hidden;
}
.brand {
  height: 56px;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--ink);
  white-space: nowrap;
}
/* 品牌区可点：打开主页 */
.brand-link {
  cursor: pointer;
  transition: opacity 0.15s;
}
.brand-link:hover {
  opacity: 0.75;
}
.brand-logo {
  width: 24px;
  height: 24px;
  border-radius: 5px;
  flex: none;
}
.menu {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  border-right: none;
}
/* 菜单文字与图标：字号 14px、行高 48px（折叠时图标由 EP 居中、span 自动隐藏） */
.menu :deep(.el-menu-item) {
  --mi-c: var(--el-text-color-regular);
  height: 42px;
  line-height: 42px;
  margin: 3px 8px;
  border-radius: 10px;
  font-size: 14px;
  transition: background-color 0.15s, box-shadow 0.15s;
}
/* 菜单拖拽排序（403）：拖拽项半透明、落点顶部主色指示线 */
.menu :deep(.el-menu-item[draggable="true"]) { cursor: grab; }
.menu :deep(.el-menu-item.nav-dragging) { opacity: 0.45; }
.menu :deep(.el-menu-item.nav-over) { box-shadow: inset 0 2px 0 var(--el-color-primary); }
.menu :deep(.el-menu-item .el-icon) {
  flex: none;
  width: 27px;
  height: 27px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--mi-c) 15%, transparent);
  font-size: 17px;
}
.menu :deep(.el-menu-item:hover) { background: var(--el-fill-color); }
.menu :deep(.el-menu-item.is-active) {
  background: color-mix(in srgb, var(--el-color-primary) 15%, transparent);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--el-color-primary) 40%, transparent);
}
/* 折叠开关：侧栏底部固定，描边小按钮（比裸图标更可见可点） */
.menu-toggle {
  flex: none;
  border-top: 1px solid var(--border);
  padding: 8px 0;
  display: flex;
  justify-content: center;
}
.menu-collapse {
  display: inline-flex;
  align-items: center;
  padding: 5px 12px;
  font-size: 16px;
  color: var(--text-2);
  background: none;
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: pointer;
}
.menu-collapse:hover {
  color: var(--brand);
  border-color: var(--brand);
  background: var(--bg);
}
.header {
  background: var(--surface);
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.header-left {
  display: flex;
  align-items: stretch;
  gap: 10px;
  min-width: 0;
}
.header-right {
  display: flex;
  align-items: center;
  gap: 14px;
}
/* 手机端抽屉开关：与旁边统计框同款边框盒、等高（header-left stretch 拉齐；
   el-icon 基础样式固定 1em×1em，需置 auto 才能被拉伸） */
.burger {
  display: inline-flex;
  align-items: center;
  width: auto;
  height: auto;
  font-size: 22px;
  padding: 0 11px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  cursor: pointer;
  color: var(--text-2);
}
/* 用户名入口：relative 供未读红点定位（铃铛/主题已收进下拉菜单） */
.user {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  color: var(--text-2);
  font-size: 14px;
  outline: none;
}
.user-dot {
  position: absolute;
  top: -3px;
  right: -4px;
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--crit, #d92d20);
  border: 1.5px solid var(--surface);
}
/* 顶栏头像（系统设置·个人信息上传）：登录名前小圆头像，无则回落图标 */
.user-avatar {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  object-fit: cover;
  border: 1px solid var(--border);
}
.user-icon {
  font-size: 28px;
}
/* 顶栏全局统计：靠左的分段统计条，背景方框 + 竖分隔线；
   异常项（离线/告警）才亮色强调，正常时低调灰 */
.top-stats {
  display: flex;
  align-items: stretch;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  font-size: 15px;
  font-weight: 600;
  color: var(--text-2);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.top-stats .ts-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 5px 12px;
}
.top-stats .ts-item + .ts-item {
  border-left: 1px solid var(--border);
}
.top-stats b {
  font-weight: 700;
  color: var(--ink);
}
.top-stats .ok b {
  color: var(--ok);
}
.top-stats .warn b {
  color: var(--warn-text);
}
.top-stats .bad b {
  color: var(--crit);
}
/* 证书到期提醒：统计条上的可点击入口，进证书监控页 */
.ts-cert {
  cursor: pointer;
}
.ts-cert:hover b {
  text-decoration: underline;
}
.main {
  overflow-y: auto;
  /* 显式覆盖 el-main 默认 20px：内容区边距收窄至 6px（30%），页面更满 */
  padding: 6px;
}

/* 多标签栏：横向滚动；激活项文字着色 + 底部高亮条 */
.tabbar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 10px;
  background: var(--surface);
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
  scrollbar-width: thin;
  flex: none;
}
/* 文档管理同款胶囊标签：bg 底 + 描边圆角，激活实底品牌色白字 */
.tabbar .tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px;
  font-size: 13px;
  color: var(--text-2);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: pointer;
  white-space: nowrap;
  user-select: none;
  max-width: 180px;
}
.tabbar .tab:hover {
  color: var(--ink);
  border-color: var(--brand);
}
.tabbar .tab.active {
  color: #fff;
  background: var(--brand);
  border-color: var(--brand);
  font-weight: 600;
}
.tabbar .tab-title {
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tabbar .tab-close {
  font-size: 12px;
  border-radius: 3px;
  padding: 1px;
}
.tabbar .tab-close:hover {
  color: var(--ink);
  background: var(--border);
}
.tabbar .tab.active .tab-close {
  color: rgba(255, 255, 255, 0.85);
}
.tabbar .tab.active .tab-close:hover {
  color: #fff;
  background: rgba(255, 255, 255, 0.2);
}

/* 手机端：抽屉内导航样式（drawer 渲染在组件树内，scoped 可作用） */
@media (max-width: 768px) {
  .main {
    padding: 3px;
  }
  .header {
    padding: 0 12px;
  }
  .header-right {
    gap: 10px;
  }
  .top-stats {
    font-size: 14px;
  }
  .top-stats .ts-item {
    padding: 3px 8px;
    gap: 3px;
  }
}

/* 邮件未读角标（菜单项内右侧小徽标，317） */
.menu :deep(.el-menu-item) { position: relative; }
.menu .mail-badge {
  position: absolute; right: 8px; top: 50%; transform: translateY(-50%);
  min-width: 16px; height: 16px; line-height: 16px; padding: 0 4px;
  border-radius: 8px; background: #f56c6c; color: #fff; font-size: 10px;
  text-align: center; font-weight: 600;
}
</style>

<style>

/* 主题色板（269）：下拉内一行三个圆形色标，下方文字，当前主题描边高亮 */
.dd-themes {
  /* 12 主题圆标三行×4（47，HA 页下拉同口径） */
  display: grid;
  grid-template-columns: repeat(4, auto);
  justify-content: space-around;
  row-gap: 8px;
  column-gap: 10px;
  width: 100%;
  padding: 2px 0;
}
.dd-swatch {
  border: none;
  background: none;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 8px;
}
.dd-swatch-dot {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  border: 2px solid transparent;
  transition: border-color 0.15s, transform 0.15s;
}
.dd-swatch:hover .dd-swatch-dot { transform: scale(1.12); }
.dd-swatch.cur .dd-swatch-dot { border-color: var(--brand, #2a78d6); }
.dd-swatch-label {
  font-size: 11px;
  color: var(--text-2, #666);
}
.dd-swatch.cur .dd-swatch-label { color: var(--brand, #2a78d6); font-weight: 600; }

/* 标签右键菜单（255）：轻量系统菜单观感，跟随主题变量；
   底色用弹层实底（玻璃主题 --surface 仅白透 8%，fixed 菜单直浮内容上看不清） */
.tab-ctx-menu {
  position: fixed;
  z-index: 3000;
  min-width: 132px;
  padding: 4px;
  background: var(--el-bg-color-overlay, var(--surface, #fff));
  border: 1px solid var(--border, #ddd);
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.14);
  display: flex;
  flex-direction: column;
}
.tab-ctx-menu button {
  border: none;
  background: none;
  text-align: left;
  font-size: 13px;
  color: var(--ink, #333);
  padding: 7px 10px;
  border-radius: 6px;
  cursor: pointer;
}
.tab-ctx-menu button:hover:not(:disabled) { background: var(--el-fill-color-light, #f5f5f5); color: var(--brand, #2a78d6); }
.tab-ctx-menu button:disabled { opacity: 0.4; cursor: not-allowed; }
/* 抽屉内容面板色 + 去掉默认内边距（导航铺满） */
.nav-drawer .el-drawer__body {
  padding: 0;
  background: var(--surface);
}
/* 下拉菜单内行布局与未读角标（菜单渲染在 body，需非 scoped） */
.dd-row {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 132px;
}
.dd-badge {
  margin-left: auto;
  min-width: 18px;
  height: 18px;
  line-height: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: var(--crit, #d92d20);
  color: #fff;
  font-size: 11px;
  font-weight: 600;
  text-align: center;
  box-sizing: border-box;
}
.dd-check {
  margin-left: auto;
  color: var(--brand);
}

/* 邮件未读角标（菜单项内右侧小徽标，317） */
.menu :deep(.el-menu-item) { position: relative; }
.menu .mail-badge {
  position: absolute; right: 8px; top: 50%; transform: translateY(-50%);
  min-width: 16px; height: 16px; line-height: 16px; padding: 0 4px;
  border-radius: 8px; background: #f56c6c; color: #fff; font-size: 10px;
  text-align: center; font-weight: 600;
}

/* ---- macOS 玻璃主题（32）：侧栏退场、菜单改底部玻璃 Dock ---- */
/* 侧栏隐藏（菜单入口移至 Dock；品牌区由 Dock 首格承担） */
</style>
