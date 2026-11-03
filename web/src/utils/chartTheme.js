// 图表主题常量：分类色板（固定顺序，超过 8 个序列由调用方限制）、
// 网格与文字墨色。数值来源：dataviz 参考调色板（已通过验证）。
// 墨色随明暗主题变化：ink()/tooltipStyle() 在渲染时读取当前主题，
// 切换主题后由调用方重新 render。
import { graphic } from 'echarts/core'
import { theme, FRONT_THEMES } from './theme.js'

// 41 批八套新暗色主题（teal..abyss）的墨色/tooltip 直接读 html 上的主题变量
// （App.vue 各 html.dark.<key> 段定义），chartTheme 不再维护第二份色板；
// violet/gold 维持既有分支不动（gold 现行为用户已接受，勿顺手改）
function isNewGlass() {
  return FRONT_THEMES.includes(theme.value) && theme.value !== 'gold'
}

function cssVar(name, fallback) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return v || fallback
}

// hex（#rrggbb）→ rgba(alpha)；非 hex 串原样返回兜底
export function withAlpha(color, alpha) {
  if (!color || color[0] !== '#' || color.length < 7) return color
  const n = color.slice(1)
  return `rgba(${parseInt(n.slice(0, 2), 16)}, ${parseInt(n.slice(2, 4), 16)}, ${parseInt(n.slice(4, 6), 16)}, ${alpha})`
}

// 折线面积渐变填充（2026-10-03 用户指定照截图样式全站折线统一）：80% 透明度
// = 峰值 α0.2 贴线，向基准边（x 轴 / 镜像 0 线）渐隐至 α0.02；reverse 用于
// 镜像图负值序列（下行/读），保证浓端仍然贴线而非贴 0 轴。
export function areaGradient(color, reverse = false) {
  return new graphic.LinearGradient(0, reverse ? 1 : 0, 0, reverse ? 0 : 1, [
    { offset: 0, color: withAlpha(color, 0.2) },
    { offset: 1, color: withAlpha(color, 0.02) },
  ])
}

export const CHART_COLORS = [
  '#2a78d6', // slot 1 blue
  '#eb6834', // slot 2 orange
  '#1baf7a', // slot 3 aqua
  '#eda100', // slot 4 yellow
  '#e87ba4', // slot 5 magenta
  '#008300', // slot 6 green
  '#4a3aa7', // slot 7 violet
  '#e34948', // slot 8 red
]

// 轴/网格/文字墨色（随主题）
export function ink() {
  if (isNewGlass()) {
    return {
      axis: cssVar('--muted', '#8b9198'), // 轴标签（弱化，取主题 muted）
      grid: 'rgba(255, 255, 255, 0.10)', // 网格细线
      baseline: 'rgba(255, 255, 255, 0.22)', // 轴线
      text: cssVar('--text-2', '#9aa0a6'), // 图例等次级文字
    }
  }
  if (theme.value === 'violet') {
    return {
      axis: '#a49bcf', // 轴标签（弱化）
      grid: 'rgba(255, 255, 255, 0.10)', // 网格细线
      baseline: 'rgba(255, 255, 255, 0.22)', // 轴线
      text: '#bcb4e0', // 图例等次级文字
    }
  }
  if (theme.value === 'dark') {
    return {
      axis: '#8b9198', // 轴标签（弱化）
      grid: '#262b31', // 网格细线
      baseline: '#3a4046', // 轴线
      text: '#9aa0a6', // 图例等次级文字
    }
  }
  return {
    axis: '#898781',
    grid: '#e1e0d9',
    baseline: '#c3c2b7',
    text: '#52514e',
  }
}

// 悬浮提示配色（随主题）。图表 tooltip 字号 12px = ECharts 默认
// （el-tooltip 的字号在 App.vue 全局样式中单独设置）
export function tooltipStyle() {
  if (isNewGlass()) {
    return {
      backgroundColor: `rgba(${cssVar('--fp-frost', '17, 20, 24')}, 0.94)`,
      borderColor: 'rgba(255, 255, 255, 0.18)',
      textStyle: { color: cssVar('--ink', '#e9edf2'), fontSize: 12 },
    }
  }
  if (theme.value === 'violet') {
    return {
      backgroundColor: 'rgba(38, 30, 78, 0.94)',
      borderColor: 'rgba(255, 255, 255, 0.18)',
      textStyle: { color: '#eae6f7', fontSize: 12 },
    }
  }
  if (theme.value === 'dark') {
    return {
      backgroundColor: '#1f2328',
      borderColor: '#2a2e33',
      textStyle: { color: '#e0e2e5', fontSize: 12 },
    }
  }
  return {
    backgroundColor: '#ffffff',
    borderColor: '#e1e0d9',
    textStyle: { color: '#0b0b0b', fontSize: 12 },
  }
}

// 指标类进度条按值分档渐变（Element Plus 的 color 属性原生支持
// linear-gradient）：蓝 0-50 / 橙 51-80 / 红 81-100，每档自身浅→深渐变。
// 适用：磁贴/列表/明细弹层/总览模块/磁盘条等指标条。证书到期条不走此口径
//（保留原状态色）；上传/下载/压缩/解压进度条用 barColorBlue()（进度本身
// 无好坏之分，统一商务蓝）。
export function barColor(pct) {
  if (pct >= 81) {
    return 'linear-gradient(90deg, #ffb3ad 0%, #e8564f 55%, #d63a31 100%)'
  }
  if (pct >= 51) {
    return 'linear-gradient(90deg, #ffd39b 0%, #f09a3e 55%, #e07f1f 100%)'
  }
  return 'linear-gradient(90deg, #9dbdf9 0%, #4a86ee 55%, #2e6be1 100%)'
}

// 上传/下载/压缩/解压进度条：单一商务蓝浅→深渐变，与取值无关。
export function barColorBlue() {
  return 'linear-gradient(90deg, #9dbdf9 0%, #4a86ee 55%, #2e6be1 100%)'
}

// 网络图方向钉色：下行蓝、上行橙（数据监控格子的箭头颜色与此一致）；
// 重排序列顺序（上行在前）时线色不跟序号走。
export const NET_DOWN_COLOR = CHART_COLORS[0]
export const NET_UP_COLOR = CHART_COLORS[1]

// 分组点缀色：低饱和商务色板按组 id 循环（总览分组卡片/终端/文件/文档同款；
// 按 id 取模而非序号，增删分组时其他组的颜色不跳）
export const GRP_COLORS = ['#4d8df5', '#1fb8b8', '#9d7bf5', '#e0a63d', '#e8738e', '#37b57c', '#7d8ef7', '#d47a4e']
export function grpColor(id) {
  return GRP_COLORS[Math.abs(id) % GRP_COLORS.length]
}

// 图表放大弹层的时段档位：与总览「我的关注」一致（5 分钟 ~ 90 天共 14 档）。
// label 存 i18n key（核心包 fmt.r*），消费方 $t(r.label) 渲染随语言切换
export const DIALOG_RANGES = [
  { value: 300, label: 'fmt.r5m' },
  { value: 900, label: 'fmt.r15m' },
  { value: 1800, label: 'fmt.r30m' },
  { value: 3600, label: 'fmt.r1h' },
  { value: 21600, label: 'fmt.r6h' },
  { value: 43200, label: 'fmt.r12h' },
  { value: 86400, label: 'fmt.r24h' },
  { value: 172800, label: 'fmt.r2d' },
  { value: 259200, label: 'fmt.r3d' },
  { value: 604800, label: 'fmt.r7d' },
  { value: 1209600, label: 'fmt.r14d' },
  { value: 2592000, label: 'fmt.r30d' },
  { value: 5184000, label: 'fmt.r60d' },
  { value: 7776000, label: 'fmt.r90d' },
]

// 向下就近取档：当前时段不在档位里时取不大于它的最大档
export function snapRange(v) {
  return DIALOG_RANGES.filter((r) => r.value <= v).pop()?.value || 3600
}
