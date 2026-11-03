// 用量查询共用取数（用量查询页账户卡 / 数据监控格子 / 主页格子三处共用，避免口径漂移）。
import { i18n, registerPageMessages } from '../i18n'
import aiusageMsgs from '../i18n/pages/aiusage.js'

// 文案在用量查询页语言包里，主页/总览可能从未打开过该 tab，这里自行注册（幂等）
registerPageMessages(aiusageMsgs)

const tr = (...a) => i18n.global.t(...a)

// tokens 计数人性化（lang 感知，与 AiUsageView 同口径）：zh 亿/万、en B/M/K
export function fmtTokens(n) {
  if (n == null) n = 0
  if (i18n.global.locale.value === 'en') {
    if (n >= 1e9) return (n / 1e9).toFixed(2) + 'B'
    if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M'
    if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K'
    return String(n)
  }
  if (n >= 1e8) return tr('aiusage.tokensYi', { v: (n / 1e8).toFixed(2) })
  if (n >= 1e4) return tr('aiusage.tokensWan', { v: (n / 1e4).toFixed(1) })
  return String(n)
}

// 智谱周窗口「剩余 N tokens 按当前节奏可撑约 D 天」（口径同用量详情弹层 rhythm）：
// 日均 = 周已用 tokens / 已过天数（至少按半天算）；剩余 = 窗口 remaining，缺了按上限反推
// （上限缺了再按已用 ÷ 百分比估）；V3 积分窗口的 remaining 是积分，须按 creditRatio
// （周真实 tokens ÷ 周积分）折回 tokens。任一输入不齐返回 ''（调用方不显示该行）。
export function zhipuRunway(u) {
  const w = u?.windows?.find((x) => x.key === 'week')
  const weekTotal = (u?.week_daily || []).reduce((s, d) => s + (d.tokens || 0), 0)
  if (!w || !u?.week_start || !w.reset_at) return ''
  const used = w.credit ? (weekTotal > 0 ? weekTotal : null) : (w.used != null ? w.used : (weekTotal > 0 ? weekTotal : null))
  if (used == null) return ''
  const ratio = w.credit && w.used ? weekTotal / w.used : null
  const cap = w.total || (w.used_pct > 0 ? Math.round(used / (w.used_pct / 100)) : null)
  const rem = w.remaining != null ? w.remaining : (cap != null ? Math.max(0, cap - used) : null)
  if (rem == null) return ''
  const remTokens = w.credit ? (ratio != null ? rem * ratio : null) : rem
  if (remTokens == null) return ''
  const avg = used / Math.max(0.5, (Date.now() / 1000 - u.week_start) / 86400)
  if (avg <= 0) return ''
  return tr('aiusage.runwayShort', { n: fmtTokens(remTokens), d: (remTokens / avg).toFixed(1) })
}
