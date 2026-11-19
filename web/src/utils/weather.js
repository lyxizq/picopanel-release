// 首页天气的 WMO 天气码映射与内联 SVG 图标（Open-Meteo weather_code 口径）。
// 图标为白色描线 SVG 字符串（v-html 渲染），尺寸由调用方 CSS 控制。
import { i18n, registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/page.js'

// 页面语言包（ns weather：WMO 文案表等）：纯 js 模块在模块顶层注册一次（重复注册无害）
registerPageMessages(pageMsgs)

// 命名 tr：统一出口，避免与局部变量撞名
const tr = (...a) => i18n.global.t(...a)

// WMO_TEXT 天气码 → i18n key（区间码归并；文案见 i18n/pages/page.js 的 weather.wmo）。
const WMO_TEXT = {
  0: 'weather.wmo.0', 1: 'weather.wmo.1', 2: 'weather.wmo.2', 3: 'weather.wmo.3',
  45: 'weather.wmo.45', 48: 'weather.wmo.48',
  51: 'weather.wmo.51', 53: 'weather.wmo.53', 55: 'weather.wmo.55', 56: 'weather.wmo.56', 57: 'weather.wmo.57',
  61: 'weather.wmo.61', 63: 'weather.wmo.63', 65: 'weather.wmo.65', 66: 'weather.wmo.66', 67: 'weather.wmo.67',
  71: 'weather.wmo.71', 73: 'weather.wmo.73', 75: 'weather.wmo.75', 77: 'weather.wmo.77',
  80: 'weather.wmo.80', 81: 'weather.wmo.81', 82: 'weather.wmo.82', 85: 'weather.wmo.85', 86: 'weather.wmo.86',
  95: 'weather.wmo.95', 96: 'weather.wmo.96', 99: 'weather.wmo.99',
}

export function wmoText(code) {
  const k = WMO_TEXT[code]
  return k ? tr(k) : tr('weather.unknown')
}

// WMO_KIND 天气码 → 图标形态（sun/moon/partly/cloud/fog/drizzle/rain/snow/thunder）。
function wmoKind(code, isDay) {
  if (code === 0) return isDay ? 'sun' : 'moon'
  if (code === 1 || code === 2) return isDay ? 'partly' : 'partlynight'
  if (code === 3) return 'cloud'
  if (code === 45 || code === 48) return 'fog'
  if ((code >= 51 && code <= 57)) return 'drizzle'
  if ((code >= 61 && code <= 67) || (code >= 80 && code <= 82)) return 'rain'
  if ((code >= 71 && code <= 77) || code === 85 || code === 86) return 'snow'
  if (code >= 95) return 'thunder'
  return 'cloud'
}

const S = (inner) =>
  `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${inner}</svg>`

// sun 圆 + 八向短线
const SUN = S(`<circle cx="12" cy="12" r="4.2"/>
  <path d="M12 2.8v2.4M12 18.8v2.4M2.8 12h2.4M18.8 12h2.4M5.5 5.5l1.7 1.7M16.8 16.8l1.7 1.7M18.5 5.5l-1.7 1.7M7.2 16.8l-1.7 1.7"/>`)

// moon 月牙
const MOON = S(`<path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z"/>`)

// partly 小太阳 + 云
const PARTLY = S(`<circle cx="8.5" cy="8" r="2.8"/>
  <path d="M8.5 2.6v1.6M2.9 8h1.6M4.5 4l1.1 1.1M12.5 4l-1.1 1.1"/>
  <path d="M9 19.5h8.2a3.3 3.3 0 0 0 .6-6.5 4.6 4.6 0 0 0-8.9-1A3.8 3.8 0 0 0 9 19.5Z"/>`)

// partlynight 月牙 + 云
const PARTLY_NIGHT = S(`<path d="M17.8 9.2A5.2 5.2 0 0 1 11.6 3a5.2 5.2 0 1 0 6.2 6.2Z"/>
  <path d="M9.5 20h6.7a3 3 0 0 0 .5-6 4 4 0 0 0-7.8-.9A3.4 3.4 0 0 0 9.5 20Z"/>`)

// cloud 阴
const CLOUD = S(`<path d="M7 18.5h10a3.8 3.8 0 0 0 .7-7.5 5.2 5.2 0 0 0-10.1-1.2A4.3 4.3 0 0 0 7 18.5Z"/>`)

// fog 雾：云 + 两横线
const FOG = S(`<path d="M7 14.5h10a3.8 3.8 0 0 0 .7-7.5 5.2 5.2 0 0 0-10.1-1.2A4.3 4.3 0 0 0 7 14.5Z"/>
  <path d="M5.5 18h13M8 21h8"/>`)

// drizzle 毛毛雨：云 + 两滴
const DRIZZLE = S(`<path d="M7 14h10a3.8 3.8 0 0 0 .7-7.5 5.2 5.2 0 0 0-10.1-1.2A4.3 4.3 0 0 0 7 14Z"/>
  <path d="M9 17.5v1.6M13 17.5v1.6M11 20.5v1.4M15.5 20.5v1.4" stroke-width="2"/>`)

// rain 雨：云 + 斜线
const RAIN = S(`<path d="M7 13.5h10a3.8 3.8 0 0 0 .7-7.5 5.2 5.2 0 0 0-10.1-1.2A4.3 4.3 0 0 0 7 13.5Z"/>
  <path d="M8.5 16.5 7.5 20M12.5 16.5l-1 3.5M16.5 16.5l-1 3.5"/>`)

// snow 雪：云 + 雪点
const SNOW = S(`<path d="M7 14h10a3.8 3.8 0 0 0 .7-7.5 5.2 5.2 0 0 0-10.1-1.2A4.3 4.3 0 0 0 7 14Z"/>
  <path d="M8.5 17.6h.01M12 19.4h.01M15.5 17.6h.01M10.3 20.8h.01M13.8 21.6h.01" stroke-width="2.4"/>`)

// thunder 雷雨：云 + 闪电
const THUNDER = S(`<path d="M7 13h10a3.8 3.8 0 0 0 .7-7.5 5.2 5.2 0 0 0-10.1-1.2A4.3 4.3 0 0 0 7 13Z"/>
  <path d="m12.8 14.5-2.6 3.6h3l-1.8 3.9 4.4-5.1h-2.7l1.5-2.4Z" fill="currentColor" stroke="none"/>`)

const ICONS = {
  sun: SUN, moon: MOON, partly: PARTLY, partlynight: PARTLY_NIGHT,
  cloud: CLOUD, fog: FOG, drizzle: DRIZZLE, rain: RAIN, snow: SNOW, thunder: THUNDER,
}

// wmoIcon 天气码 → 内联 SVG 字符串（v-html 用）。
export function wmoIcon(code, isDay = true) {
  return ICONS[wmoKind(code, isDay)] || CLOUD
}

// ---- 展示口径共用件（原 PageView 本地函数，5 智能家居头部天气复用而抽到这里）----

// 蒲福氏风级（521）：km/h → 级（各档下限，标准分级）
export function windLevel(kmh) {
  const lo = [1, 6, 12, 20, 29, 39, 50, 62, 75, 89, 103, 118]
  let lv = 0
  for (let i = 0; i < lo.length; i++) {
    if (kmh >= lo[i]) lv = i + 1
  }
  return lv
}

// WMO 天气码雨/雪族（538）：雨=毛毛雨/雨/阵雨/雷暴，雪=雪/阵雪——上游无
// 降水概率（高德）时的定性依据
export function wxIsRainCode(c) {
  return (c >= 51 && c <= 67) || (c >= 80 && c <= 82) || (c >= 95 && c <= 99)
}

export function wxIsSnowCode(c) {
  return (c >= 71 && c <= 77) || c === 85 || c === 86
}

// 七日行降水格文案（538 建、539 去「无」）：概率% → 定性 雪/雨 → 空白
export function wxDayPrecip(d) {
  if (d.precip > 0) return d.precip + '%'
  if (d.snow || wxIsSnowCode(d.code)) return tr('weather.snowShort')
  if (wxIsRainCode(d.code)) return tr('weather.rainShort')
  return ''
}

// 悬停详情（539 用户报原生 title 无显示）：概率/雪量/大风三段，空串=禁用 tooltip
export function wxDayTitle(d) {
  return [
    d.precip > 0 ? tr('weather.rainProb', { p: d.precip }) : wxIsSnowCode(d.code) ? tr('weather.snowing') : wxIsRainCode(d.code) ? tr('weather.rainy') : tr('weather.noRain'),
    d.snow ? tr('weather.snowAmt', { v: d.snow }) : '',
    d.wind ? tr('weather.windAmt', { v: d.wind }) : '',
  ].filter(Boolean).join(' · ')
}

export function wxDayName(date) {
  const d = new Date(`${date}T00:00:00`)
  const now = new Date()
  const midnight = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const diff = Math.round((midnight(d) - midnight(now)) / 86400000)
  if (diff === 0) return tr('weather.today')
  if (diff === 1) return tr('weather.tomorrow')
  return tr('weather.weekFmt', { d: tr('weather.week.' + d.getDay()) })
}
