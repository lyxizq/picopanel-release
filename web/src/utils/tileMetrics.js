// 数据监控模块的可选指标集（总览 DashboardView 与个人主页 PageView 共用）：
// kind: 'disk'=param 选挂载点（空=各盘最大值）、'net'=param 选网口（空=展示网口合计）、
// 'temp'=param 选传感器（空=CPU 温度，无 CPU 采样时取全部最高）。
// label 存 i18n key（tileMetrics.*），消费端 $t(m.label) 渲染（语言切换即时跟随）
export const TILE_METRICS = [
  { value: 'cpumem', label: 'tileMetrics.cpumem' },
  { value: 'cpu', label: 'tileMetrics.cpu' },
  { value: 'mem', label: 'tileMetrics.mem' },
  { value: 'swap', label: 'tileMetrics.swap' },
  { value: 'disk', label: 'tileMetrics.disk', kind: 'disk' },
  { value: 'io', label: 'tileMetrics.io', kind: 'disk' },
  { value: 'diskio', label: 'tileMetrics.diskio', kind: 'disk' },
  { value: 'diskrw', label: 'tileMetrics.diskrw', kind: 'disk' },
  { value: 'net', label: 'tileMetrics.net', kind: 'net' },
  { value: 'temp', label: 'tileMetrics.temp', kind: 'temp' },
  { value: 'load1', label: 'tileMetrics.load1' },
  { value: 'load5', label: 'tileMetrics.load5' },
  { value: 'load15', label: 'tileMetrics.load15' },
  { value: 'tcp_est', label: 'tileMetrics.tcp_est' },
  { value: 'uptime', label: 'tileMetrics.uptime' },
  // 用量查询数据源（486）：prov=要求的服务商，指标下拉按所选账户过滤
  { value: 'ai_zhipu_week', label: 'tileMetrics.ai_zhipu_week', prov: 'zhipu' },
  { value: 'ai_zhipu_5h', label: 'tileMetrics.ai_zhipu_5h', prov: 'zhipu' },
  { value: 'ai_deepseek_balance', label: 'tileMetrics.ai_deepseek_balance', prov: 'deepseek' },
  { value: 'ai_eo_traffic', label: 'tileMetrics.ai_eo_traffic', prov: 'edgeone' },
  { value: 'ai_eo_peak', label: 'tileMetrics.ai_eo_peak', prov: 'edgeone' },
  { value: 'ai_eo_requests', label: 'tileMetrics.ai_eo_requests', prov: 'edgeone' },
]

// 用量指标所属服务商（无 prov 项=服务器指标，返回 ''）
export function tileMetricProv(value) {
  return TILE_METRICS.find((m) => m.value === value)?.prov || ''
}

// 用量账户服务商展示名（弹窗账户下拉后缀）
export const AI_PROV_LABELS = {
  zhipu: '智谱 GLM',
  deepseek: 'DeepSeek',
  edgeone: '腾讯 EdgeOne',
}
