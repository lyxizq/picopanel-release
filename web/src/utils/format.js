// 人读格式化：容量/速率/时长/时间。原始数据单位见 internal/proto/report.go 注释。
// 数值展示统一约定：小数保留一位（整数计数不补 .0）。
export function fmtBytes(n) {
  if (n == null || isNaN(n)) return '-'
  if (n < 1024) return `${Math.round(n)} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

// 网络速率：入参 B/s，展示为 Kbps 规格（×8 转比特、1000 进制）。
export function fmtBps(bps) {
  if (bps == null || isNaN(bps)) return '-'
  const units = ['Kbps', 'Mbps', 'Gbps']
  let v = (bps * 8) / 1000
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

// 镜像网络图 tooltip 用：下行序列画为负值，展示时取绝对值恢复速率语义。
export function fmtBpsAbs(bps) {
  return fmtBps(Math.abs(bps ?? 0))
}

// 磁盘吞吐：入参 B/s，按硬盘容量口径展示（字节、1024 进制：KB/s→MB/s→GB/s→TB/s）。
export function fmtDiskBps(n) {
  if (n == null || isNaN(n)) return '-'
  if (n < 1024) return `${Math.round(n)} B/s`
  const units = ['KB/s', 'MB/s', 'GB/s', 'TB/s']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

// 磁盘 IO 图 y 轴：按序列最大值选定统一单位（KB/s→TB/s，1024 进制），
// 序列值单位 B/s。镜像图读写序列为负值，刻度取绝对值显示。
export function diskAxisFmt(series) {
  let max = 0
  for (const s of series || []) {
    for (const p of s.points || []) {
      if (Math.abs(p[1]) > max) max = Math.abs(p[1])
    }
  }
  if (max < 1024) return (v) => `${Math.abs(v).toFixed(0)} B/s`
  const units = ['KB/s', 'MB/s', 'GB/s', 'TB/s']
  let v = max / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  const div = 1024 ** (i + 1)
  return (v2) => `${Math.abs(v2 / div).toFixed(1)} ${units[i]}`
}

// 网络图 y 轴：按序列最大值选定统一单位（Kbps→Mbps→Gbps，1000 进制），
// 序列值单位 B/s。返回可直接传给 ECharts axisLabel.formatter 的函数。
// 镜像网络图的下行序列为负值，刻度取绝对值显示（上下只标速率不标方向）。
export function bpsAxisFmt(series) {
  let max = 0
  for (const s of series || []) {
    for (const p of s.points || []) {
      if (Math.abs(p[1]) > max) max = Math.abs(p[1])
    }
  }
  const units = ['Kbps', 'Mbps', 'Gbps']
  let v = (max * 8) / 1000 // 最大值折 Kbps 选档
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  const div = [125, 125000, 125000000][i] // B/s → 该单位：×8/1000^（i+1）
  return (v2) => (v2 === 0 ? '0' : `${Math.abs(v2 / div).toFixed(1)} ${units[i]}`)
}

export function fmtUptime(sec) {
  if (!sec) return '-'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${d}天${h}小时`
  if (h > 0) return `${h}小时${m}分`
  return `${m}分钟`
}

// 服务器开机时长，单位：天（不足 1 天按小时显示）。
export function fmtUptimeDays(sec) {
  if (!sec) return '-'
  const d = Math.floor(sec / 86400)
  if (d > 0) return `${d} 天`
  return `${Math.floor(sec / 3600)} 小时`
}

// 时间戳 → 「yyyy-MM-dd HH:mm:ss」（系统内所有年月日展示统一用连字符格式）
export function fmtTime(ts) {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

// 时间戳 → 「yyyy-MM-dd」（只显示日期处用）
export function fmtDate(ts) {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

export function fmtPercent(v) {
  return v == null || isNaN(v) ? '-' : `${v.toFixed(1)}%`
}

// 进度条百分比数值：空值安全，保留一位小数并夹紧 0~100（异常值不撑破进度条）。
export function pct(v) {
  if (v == null || isNaN(v)) return 0
  return Math.min(100, Math.max(0, Math.round(v * 10) / 10))
}

// 利用率文字配色档位：≥70 黄、≥90 红（全局 .u-warn/.u-crit 样式在 App.vue）。
export function utilClass(v) {
  if (v == null || isNaN(v)) return ''
  return v >= 90 ? 'u-crit' : v >= 70 ? 'u-warn' : ''
}

// 按逗号拆网口配置：空白项剔除（「eth0, eth1」→ ['eth0','eth1']）。
export function splitIfaces(cfg) {
  return (cfg || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

// 按服务器「展示网口」配置过滤网口序列：空=全部；配置了则按配置顺序，
// 当前不存在的网口跳过。
export function pickNets(nets, cfg) {
  const want = splitIfaces(cfg)
  const list = nets || []
  if (!want.length) return list
  return want.map((iface) => list.find((n) => n.iface === iface)).filter(Boolean)
}

export function fmtNum(v, digits = 1) {
  return v == null || isNaN(v) ? '-' : v.toFixed(digits)
}

// 图表数值：整数保持整数（连接数等计数），小数统一保留一位。
export function fmtChartNum(v) {
  if (v == null || isNaN(v)) return '-'
  const n = Number(v)
  return Number.isInteger(n) ? String(n) : n.toFixed(1)
}

// 到期倒计时：购买/到期日期（YYYY-MM-DD）齐备时返回
// { total, remain, percent, text, level, permanent }——total=总天数、remain=剩余天数（负=已过期）、
// percent=剩余时间占比（0~100，绿条长度：剩余越少条越短，到期后为 0）、text=剩余天数文案、
// level=ok | warn（剩余 ≤30 天）| crit（剩余 ≤7 天或已过期），level 只影响文字配色、
// permanent=未配置日期。
// 任一日期缺失/非法/到期不晚于购买时返回「永久」展示（满条、绿色）。
export function expiryInfo(purchasedOn, expiresOn) {
  const permanent = { total: 0, remain: 0, percent: 100, text: '永久', level: 'ok', permanent: true }
  if (!purchasedOn || !expiresOn) return permanent
  const start = Date.parse(purchasedOn)
  const end = Date.parse(expiresOn)
  if (isNaN(start) || isNaN(end) || end <= start) return permanent
  const DAY = 86400000
  const now = new Date()
  const today = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate()) // 本地当天零点（UTC 基准）
  const total = Math.round((end - start) / DAY)
  const remain = Math.round((end - today) / DAY)
  const percent = Math.min(100, Math.max(0, Math.round(((end - today) / (end - start)) * 100)))
  const level = remain < 0 || remain <= 7 ? 'crit' : remain <= 30 ? 'warn' : 'ok'
  const text = remain < 0 ? `过期 ${-remain} 天` : remain === 0 ? '今天到期' : `剩 ${remain} 天`
  return { total, remain, percent, text, level, permanent: false }
}

// 到期倒计时进度条颜色：绿条=剩余时间（明暗主题通用）
export const EXPIRY_BAR = '#37b57c'

// 到期倒计时悬停文案：完整周期与总天数；未配置=「永久」。
export function expiryTitle(s) {
  const e = expiryInfo(s.purchased_on, s.expires_on)
  return e.permanent ? '未配置有效期（永久）' : `${s.purchased_on} ~ ${s.expires_on}（共 ${e.total} 天）`
}

// 服务器名 tooltip：保留原有内容为第一行，备注换行加在第二行
//（原生 title 属性的 \n 即换行；两者皆空返回空串 = 不弹 tooltip）。
export function withRemark(base, remark) {
  const a = (base || '').trim()
  const b = (remark || '').trim()
  if (a && b) return a + '\n' + b
  return a || b
}

// 服务器规格「几C几G」：C=CPU 核心数，G=内存（一律 G 单位，一律取整——有小数
// 直接整数+1，如 1.8G→2G、234.375G→235G）。核心数或内存缺失只拼已有部分，
// 全缺返回空串（从未上报的机器 latest 为空）。入参为 /servers 的 serverView。
export function specText(server) {
  if (!server) return ''
  const cores = Number(server.cpu_cores) || 0
  const memTotal = Number(server.latest?.mem_total) || 0
  let spec = ''
  if (cores > 0) spec += `${cores}C`
  if (memTotal > 0) spec += `${Math.ceil(memTotal / 1024 ** 3)}G`
  return spec
}

// 服务器详情弹层名称用的「 | 几C几G」后缀形态。
export function specSuffix(server) {
  const spec = specText(server)
  return spec ? ` | ${spec}` : ''
}

// 服务器名 + 规格后缀（服务器详情弹层用；其余位置只显名称不带规格）。
export function withSpec(name, server) {
  return `${name || ''}${specSuffix(server)}`
}
