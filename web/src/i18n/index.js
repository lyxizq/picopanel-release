// 界面语言（中/英）全局状态：模块级 ref，沿用 utils/theme.js 模式（项目无 pinia）。
// zh 为源语言常驻主包并作 fallbackLocale；en 语言包 dynamic import 懒加载
// （不进首屏 chunk）。main.js 在挂载前 await initI18n()，英文用户等语言包
// 到齐再挂载，首屏不闪中文。偏好存 localStorage「lz-lang」，默认中文不跟随系统。
import { ref } from 'vue'
import { createI18n } from 'vue-i18n'
import zh from './zh.js'

export const LANGS = [
  { key: 'zh', label: '中文' },
  { key: 'en', label: 'English' },
]

const saved = localStorage.getItem('lz-lang')

export const lang = ref(saved === 'en' ? 'en' : 'zh')

export const i18n = createI18n({
  legacy: false,
  globalInjection: true, // 模板直接用 $t，无需逐组件 useI18n
  locale: lang.value,
  fallbackLocale: 'zh',
  messages: { zh },
  // 抽取按批次推进，en 缺键回落 zh 属正常路径，不打 warning
  missingWarn: false,
  fallbackWarn: false,
})

const loaded = { zh: true, en: false }
let enLoaded = false
const pendingEn = [] // en 语言包到齐前页面先注册的英文碎片，加载后补并

// 页面级语言包注册：各页/组件把自己的 {zh, en} 碎片并进全局（merge 语义，
// 重复注册无害）。碎片放各页自己的 chunk（i18n/pages/<ns>.js 由页面 import），
// 不进主包——zh 文案跟着页面代码走，en 未访问的页不加载。
export function registerPageMessages(msgs) {
  if (msgs?.zh) i18n.global.mergeLocaleMessage('zh', msgs.zh)
  if (msgs?.en) {
    if (enLoaded) i18n.global.mergeLocaleMessage('en', msgs.en)
    else pendingEn.push(msgs.en)
  }
}

async function loadLang(l) {
  if (loaded[l]) return
  if (l === 'en') {
    const mod = await import('./en.js')
    i18n.global.setLocaleMessage('en', mod.default)
    enLoaded = true
    for (const m of pendingEn.splice(0)) i18n.global.mergeLocaleMessage('en', m)
    loaded.en = true
  }
}

function applyLang(l) {
  document.documentElement.lang = l === 'en' ? 'en' : 'zh-CN'
}

// 应用挂载前调用一次：语言包就绪 + <html lang> 定稿（index.html 内联脚本已先行
// 设过同一结果，这里主要覆盖动态切换后的场景）
export async function initI18n() {
  await loadLang(lang.value)
  applyLang(lang.value)
}

// 切换语言（设置页下拉）：懒加载 → 切 locale（全站 $t 即时跟随重渲）→ 记忆
export async function setLang(l) {
  if (l !== 'zh' && l !== 'en') return
  await loadLang(l)
  lang.value = l
  i18n.global.locale.value = l
  localStorage.setItem('lz-lang', l)
  applyLang(l)
}
