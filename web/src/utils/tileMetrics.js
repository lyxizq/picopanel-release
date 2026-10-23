// 数据监控模块的可选指标集（总览 DashboardView 与个人主页 PageView 共用）：
// kind: 'disk'=param 选挂载点（空=各盘最大值）、'net'=param 选网口（空=展示网口合计）、
// 'temp'=param 选传感器（空=CPU 温度，无 CPU 采样时取全部最高）
export const TILE_METRICS = [
  { value: 'cpumem', label: '资源占用（CPU+内存）' },
  { value: 'cpu', label: 'CPU 使用率' },
  { value: 'mem', label: '内存使用率' },
  { value: 'swap', label: '交换分区使用率' },
  { value: 'disk', label: '磁盘使用率', kind: 'disk' },
  { value: 'io', label: '磁盘 IO 使用率', kind: 'disk' },
  { value: 'diskio', label: '磁盘读写速率', kind: 'disk' },
  { value: 'diskrw', label: '磁盘(使用率+读写)', kind: 'disk' },
  { value: 'net', label: '网卡速率（上下行）', kind: 'net' },
  { value: 'temp', label: '温度', kind: 'temp' },
  { value: 'load1', label: '负载 load1' },
  { value: 'load5', label: '负载 load5' },
  { value: 'load15', label: '负载 load15' },
  { value: 'tcp_est', label: 'TCP 连接数' },
  { value: 'uptime', label: '运行时长' },
  // 用量查询数据源（486）：prov=要求的服务商，指标下拉按所选账户过滤
  { value: 'ai_zhipu_week', label: '周额度已用（进度条）', prov: 'zhipu' },
  { value: 'ai_zhipu_5h', label: '5 小时窗口已用（进度条）', prov: 'zhipu' },
  { value: 'ai_deepseek_balance', label: '总余额', prov: 'deepseek' },
  { value: 'ai_eo_traffic', label: '总流量（近 24 小时）', prov: 'edgeone' },
  { value: 'ai_eo_peak', label: '带宽峰值（近 24 小时）', prov: 'edgeone' },
  { value: 'ai_eo_requests', label: '请求数（近 24 小时）', prov: 'edgeone' },
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
