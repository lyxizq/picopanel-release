// 页面可用性共享模块（2026-11-03 家庭成员口径）：主页/智能家居与后台菜单项
// （六模块）全部由管理员按成员配置（登录/Me 下发 permissions，localStorage 记忆；
// 未配置=默认全量）。后端 api/perms.go familyRoutes 按同一张表放行/拒绝。
// router.js/Layout/tabs/StatusView 共用本模块（独立成件避免循环依赖）。
import { getRole, getPerms, getLanding } from '../api/index.js'

// 可配置页面全集（与后端 allPerms 对齐）
export const ALL_PERMS = ['home', 'ha', 'docs', 'reminders', 'meds', 'mail', 'messages', 'toolbox']

// 后台菜单项候选（除主页/HA 外的六模块；「控制台」入口落第一个可用页）
export const CONSOLE_PERMS = ['docs', 'reminders', 'meds', 'mail', 'messages', 'toolbox']

// 后台模块 → 路径（成员「控制台」入口落第一个可用页；顺序=菜单主次）
export const CONSOLE_PATHS = {
  docs: '/console/docs',
  reminders: '/console/reminders',
  meds: '/console/meds',
  messages: '/console/messages',
  mail: '/console/mail',
  toolbox: '/console/toolbox',
}

// ROUTE_PERMS 路径 → 权限 key（守卫用；逐条 match）。key 在 ALL_PERMS 里的按
// 成员配置放行，其余（监控类）一律 admin-only。
export const ROUTE_PERMS = [
  { match: (p) => p === '/', key: 'home' },
  { match: (p) => p === '/ha' || p.startsWith('/ha/'), key: 'ha' },
  { match: (p) => p === '/console' || p === '/console/', key: 'console' },
  { match: (p) => p.startsWith('/console/docs'), key: 'docs' },
  { match: (p) => p.startsWith('/console/term') || p.startsWith('/console/files'), key: 'term' },
  { match: (p) => p.startsWith('/console/mail'), key: 'mail' },
  { match: (p) => p.startsWith('/console/messages'), key: 'messages' },
  { match: (p) => p.startsWith('/console/database'), key: 'database' },
  { match: (p) => p.startsWith('/console/pt'), key: 'pt' },
  { match: (p) => p.startsWith('/console/svcmon'), key: 'svcmon' },
  { match: (p) => p.startsWith('/console/reminders'), key: 'reminders' },
  { match: (p) => p.startsWith('/console/meds'), key: 'meds' },
  { match: (p) => p.startsWith('/console/calendar'), key: 'calendar' },
  // 房屋管理：管理员专属模块（key 不在 ALL_PERMS，成员恒 false；后端 familyRoutes
  // 同样不含 /houses 组，deny-by-default 兜底）
  { match: (p) => p.startsWith('/console/houses'), key: 'houses' },
  { match: (p) => p.startsWith('/console/tv'), key: 'tv' },
  { match: (p) => p.startsWith('/console/toolbox'), key: 'toolbox' },
  { match: (p) => p.startsWith('/console/admin/servers'), key: 'admin-servers' },
  { match: (p) => p.startsWith('/console/status'), key: 'status' },
]

// permOfPath 路径对应权限 key，未列出的（登录页/未知）返回 ''（按无权限处理）。
export function permOfPath(path) {
  for (const r of ROUTE_PERMS) {
    if (r.match(path)) return r.key
  }
  return ''
}

// hasPerm 当前登录者是否可用某页面 key：admin 恒可用；成员按配置
//（未配置=默认全量，与后端 userAllowed 同口径）。
export function hasPerm(key) {
  if (getRole() !== 'user') return true
  if (!ALL_PERMS.includes(key)) return false
  const perms = getPerms()
  if (!perms.length) return true
  return perms.includes(key)
}

// landingPath 成员登录落点：管理员配置的 landing 优先（须已授权），否则按
// 主页 > HA > 第一个可用后台页回落；skipHome=true 用于主页无权时的越权回退
//（再算 home 会原地循环）。全无可用返回 null（理论上不可达：permissions 非空
// 必含至少一个 key，空=默认全量）。
export function landingPath(skipHome = false) {
  if (getLanding() === 'ha' && hasPerm('ha')) return '/ha'
  if (!skipHome && hasPerm('home')) return '/'
  if (hasPerm('ha')) return '/ha'
  return firstConsolePath()
}

// firstConsolePath 成员第一个可用的后台页（「控制台」入口引导/关 tab 回落）；
// 一个后台模块都没有则 null（成员只用主页/HA，不进后台）。
export function firstConsolePath() {
  for (const key of CONSOLE_PERMS) {
    if (hasPerm(key)) return CONSOLE_PATHS[key]
  }
  return null
}
