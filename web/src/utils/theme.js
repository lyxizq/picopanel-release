// 十二套外观主题：light 明亮 / dark 暗黑 / violet 紫罗兰（主页同款深紫渐变 + 全局玻璃拟态）
// / gold 暗金 / teal 青碧 / rose 绯夜 / glacier 冰川 / ember 熔岩 / moss 苔原 / graphite 石墨
// / neon 霓 / abyss 深海（41，2026-10-19 用户确认设计图后落地）。
// 除 light 外全部挂 html.dark 双类——Element Plus 暗色变量做底，各主题段在其上覆盖；
// 结构性玻璃规则在 App.vue 以 :is() 合流，新主题只补配色段。
// 初次访问跟随系统偏好，手动切换后记住选择（localStorage）；
// index.html 里有同逻辑的内联脚本先行设置，避免加载时闪一下错误配色。
import { ref } from 'vue'

export const THEMES = ['light', 'dark', 'violet', 'gold', 'teal', 'rose', 'glacier', 'ember', 'moss', 'graphite', 'neon', 'abyss']

// 挂独立主题类的暗色主题（html 上会带同名 class）；dark 是纯暗色不挂类。
// gold 与八套新主题属于「品牌暗色」：前台/HA 整页跟随换外观（FRONT_THEMES）。
export const CLASS_THEMES = ['violet', 'gold', 'teal', 'rose', 'glacier', 'ember', 'moss', 'graphite', 'neon', 'abyss']

// 前台（主页/HA）跟随换整套外观的主题：gold + 41 批八套；violet 与白/暗下两页维持深紫缺省
export const FRONT_THEMES = ['gold', 'teal', 'rose', 'glacier', 'ember', 'moss', 'graphite', 'neon', 'abyss']

// 主题色板（269 色板/43 HA 下拉共用一份定义）
export const THEME_SWATCHES = [
  { key: 'light', label: '浅色', tip: '切换到浅色模式', bg: 'linear-gradient(135deg, #f8f8f4 0%, #dfe7f2 100%)', ring: 'none' },
  { key: 'dark', label: '暗色', tip: '切换到暗色模式', bg: 'linear-gradient(135deg, #23262c 0%, #111217 100%)', ring: 'none' },
  { key: 'violet', label: '紫色', tip: '切换到紫罗兰模式', bg: 'linear-gradient(135deg, #7b5ce0 0%, #b23b6f 100%)', ring: 'none' },
  { key: 'gold', label: '暗金', tip: '切换到暗金模式（前台/HA 同步换金）', bg: 'linear-gradient(135deg, #e8c96a 0%, #8a6a1e 100%)', ring: 'none' },
  { key: 'teal', label: '青碧', tip: '切换到青碧模式（前台/HA 同步换色）', bg: 'linear-gradient(135deg, #43d6ac 0%, #0d4f3f 100%)', ring: 'none' },
  { key: 'rose', label: '绯夜', tip: '切换到绯夜模式（前台/HA 同步换色）', bg: 'linear-gradient(135deg, #ef7a95 0%, #7c1f36 100%)', ring: 'none' },
  { key: 'glacier', label: '冰川', tip: '切换到冰川模式（前台/HA 同步换色）', bg: 'linear-gradient(135deg, #6fc4ec 0%, #14486b 100%)', ring: 'none' },
  { key: 'ember', label: '熔岩', tip: '切换到熔岩模式（前台/HA 同步换色）', bg: 'linear-gradient(135deg, #f09a5e 0%, #7c3a16 100%)', ring: 'none' },
  { key: 'moss', label: '苔原', tip: '切换到苔原模式（前台/HA 同步换色）', bg: 'linear-gradient(135deg, #a9d66e 0%, #3f5c1c 100%)', ring: 'none' },
  { key: 'graphite', label: '石墨', tip: '切换到石墨模式（前台/HA 同步换色）', bg: 'linear-gradient(135deg, #b8c6d4 0%, #46545f 100%)', ring: 'none' },
  { key: 'neon', label: '霓', tip: '切换到霓模式（前台/HA 同步换色）', bg: 'linear-gradient(135deg, #e883cc 0%, #5c1e50 100%)', ring: 'none' },
  { key: 'abyss', label: '深海', tip: '切换到深海模式（前台/HA 同步换色）', bg: 'linear-gradient(135deg, #4f9cb0 0%, #123240 100%)', ring: 'none' },
]

export const theme = ref(readInitial())

function readInitial() {
  const saved = localStorage.getItem('lz-theme')
  if (saved === 'macos') return 'violet' // macos 主题已移除（36），存量选择回落 violet
  if (THEMES.includes(saved)) return saved
  return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function applyTheme(t) {
  theme.value = t
  localStorage.setItem('lz-theme', t)
  const root = document.documentElement
  // 品牌暗色主题需要 EP 暗色变量打底，dark class 对非明亮主题都挂
  root.classList.toggle('dark', t !== 'light')
  for (const k of CLASS_THEMES) root.classList.toggle(k, t === k)
}

export function setTheme(t) {
  if (!THEMES.includes(t)) return
  applyTheme(t)
}

// 应用启动时调用一次（与 index.html 内联脚本的结果一致，主要是初始化响应式状态）
export function initTheme() {
  applyTheme(theme.value)
}
