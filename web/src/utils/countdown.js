// 倒数日展示口径（CountdownDialog 与主页卡共用）：kind=since 显「已 N 天」，
// 其余按剩余天数 今天/明天/还有 N 天/已过 N 天（once 已过为负）。
export function cdDaysText(c, t) {
  if (c.kind === 'since') return t('cd.since', { n: c.days })
  if (c.days === 0) return t('cd.today')
  if (c.days === 1) return t('cd.tomorrow')
  if (c.days < 0) return t('cd.daysAgo', { n: -c.days })
  return t('cd.daysLeft', { n: c.days })
}

// 色板：沿用菜单图标彩色化配色，缺色回落灰
export const CD_PALETTE = ['#f56c6c', '#e6a23c', '#67c23a', '#409eff', '#9b6ef3', '#14b8c4', '#e1306c', '#8a919f']
