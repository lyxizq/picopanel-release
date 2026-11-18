// 数据监控模块（重点指标格子）的取数计算：配置 + /servers 最新快照（+用量查询
// 账户快照，486）→ 格子展示数据。
// 总览页（DashboardView）与个人主页（PageView）共用，各自按同一套数据渲染不同样式。
import { fmtBps, fmtBytes, fmtBytesShort, fmtDiskBps, fmtNum, fmtPercent, fmtUptimeDays, pickNets, utilClass, withRemark } from '../utils/format.js'
import { i18n } from '../i18n'
import { zhipuRunway } from '../utils/aiusage.js'

// 命名 tr：本文件多处用 t 作格子参数名，避免遮蔽
const tr = (...a) => i18n.global.t(...a)

// 网口合计：未指定网口时按服务器「展示网口」配置过滤（空=全部），与快照接口同口径
function netSum(server) {
  const sel = pickNets(server.latest?.nets, server.net_ifaces)
  return {
    rx: sel.reduce((a, n) => a + (n.rx_bps || 0), 0),
    tx: sel.reduce((a, n) => a + (n.tx_bps || 0), 0),
  }
}

// 读写速率取值：指定挂载点取该盘，未指定按设备去重合计（同一块盘多挂载点
// 不重复计）；旧版 agent 无速率字段返回 null
function diskRW(disks, param) {
  const withIO = disks.filter((x) => x.io_read_bps != null || x.io_write_bps != null)
  if (!withIO.length) return null
  if (param) {
    const d = disks.find((x) => x.mount === param)
    if (!d || (d.io_read_bps == null && d.io_write_bps == null)) return null
    return { r: d.io_read_bps || 0, w: d.io_write_bps || 0 }
  }
  let r = 0
  let w = 0
  const seen = new Set()
  for (const x of withIO) {
    const key = x.device || x.mount
    if (seen.has(key)) continue
    seen.add(key)
    r += x.io_read_bps || 0
    w += x.io_write_bps || 0
  }
  return { r, w }
}

// 计数人读格式（对齐用量查询页 fmtTimes 口径；英文按 K/M 缩写）
function fmtCount(n) {
  if (n == null || isNaN(n)) return '-'
  const en = i18n.global.locale.value === 'en'
  if (n >= 1e8) return en ? (n / 1e6).toFixed(0) + 'M' : (n / 1e8).toFixed(2) + ' 亿次'
  if (n >= 1e4) return en ? (n / 1e3).toFixed(0) + 'K' : (n / 1e4).toFixed(2) + ' 万次'
  return String(n)
}

// 带宽峰值（bps 原始单位，与网卡速率的 B/s 入参区分）
function fmtBpsPeak(bps) {
  if (bps == null || isNaN(bps)) return '-'
  const units = ['Kbps', 'Mbps', 'Gbps']
  let v = bps / 1000
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

// 用量查询格子取数（486）：acc=GET /aiusage/accounts 一行（含 usage 最新快照）
function aiTileView(t, acc) {
  let value = '-'
  let cls = ''
  let bar = null
  let note = '' // 标签下小字（zhipu 周窗口的「剩余可撑 N 天」节奏估计）
  const u = acc?.usage
  switch (t.metric) {
    case 'ai_zhipu_week':
    case 'ai_zhipu_5h': {
      const w = u?.windows?.find((x) => x.key === (t.metric === 'ai_zhipu_week' ? 'week' : '5h'))
      if (w?.used_pct != null) {
        value = fmtPercent(w.used_pct); cls = utilClass(w.used_pct); bar = w.used_pct
      }
      if (t.metric === 'ai_zhipu_week') note = zhipuRunway(u)
      break
    }
    case 'ai_deepseek_balance': {
      const b = u?.balances?.[0]
      if (b) value = `${b.total} ${b.currency}`
      break
    }
    case 'ai_eo_traffic':
      if (u?.edgeone) value = fmtBytes(u.edgeone.traffic_bytes)
      break
    case 'ai_eo_peak':
      if (u?.edgeone) value = fmtBpsPeak(u.edgeone.peak_bandwidth)
      break
    case 'ai_eo_requests':
      if (u?.edgeone) value = fmtCount(u.edgeone.requests)
      break
  }
  return {
    id: t.id, label: t.label, value, cls, bar, note,
    net: null, dio: null, drw: null, cm: null, tfc: null,
    link: t.link || '', icon: t.icon || '',
    server_id: 0, serverName: acc?.name || tr('tileViews.accDeleted'),
    remark: '',
  }
}


// 计算全部格子的展示数据：{ 标签, 格式化值, 配色档位, 网络/磁盘/组合模块数据, 服务器/用量账户 }
export function computeTileViews(tiles, servers, aiAccounts = []) {
  return (tiles || []).map((t) => {
    if (t.ai_id) {
      return aiTileView(t, (aiAccounts || []).find((x) => x.id === t.ai_id))
    }
    const s = (servers || []).find((x) => x.id === t.server_id)
    const L = s?.latest
    let value = '-'
    let cls = ''
    let net = null // 网络模块：上下行同格显示 { rx, tx }
    let dio = null // 磁盘读写模块：与网络同格双行显示 { r, w }（格式化后的读/写速率文本）
    let drw = null // 磁盘组合模块：读写一行 + 使用率进度条 { r, w, pct }
    let cm = null // 资源占用组合模块：CPU/内存两条进度条 { cpu, mem }
    let bar = null // 百分比模块：进度条数值（null=无数据或非百分比，退回文本展示）
    let tfc = null // 外网流量模块：当日/当月上传下载 { up: '10G/200G', down: '90G/1T' }（取自账本，不依赖 latest）
    if (t.metric === 'traffic' && s?.traffic) {
      const f = s.traffic
      tfc = {
        up: `${fmtBytesShort(f.today_tx)}/${fmtBytesShort(f.month_tx)}`,
        down: `${fmtBytesShort(f.today_rx)}/${fmtBytesShort(f.month_rx)}`,
      }
    }
    if (L) {
      const disks = L.disks || []
      const pickDisk = (field) => {
        const d = t.param ? disks.find((x) => x.mount === t.param) : null
        if (d && d[field] != null) return d[field]
        if (t.param) return null // 指定挂载点不存在（卸载/改名）
        return disks.reduce((m, x) => Math.max(m, x[field] ?? 0), 0)
      }
      switch (t.metric) {
        case 'cpumem': {
          const c = L.cpu_percent
          const mm = L.mem_percent
          if (c == null && mm == null) break
          cm = { cpu: c ?? 0, mem: mm ?? 0 }
          cls = utilClass(Math.max(c ?? 0, mm ?? 0))
          break
        }
        case 'cpu':
          value = fmtPercent(L.cpu_percent); cls = utilClass(L.cpu_percent); bar = L.cpu_percent; break
        case 'mem':
          value = fmtPercent(L.mem_percent); cls = utilClass(L.mem_percent); bar = L.mem_percent; break
        case 'swap':
          value = fmtPercent(L.swap_percent); cls = utilClass(L.swap_percent); bar = L.swap_percent; break
        case 'disk': {
          const v = pickDisk('percent')
          value = v != null ? fmtPercent(v) : '-'; cls = utilClass(v); bar = v; break
        }
        case 'io': {
          const v = pickDisk('io_util')
          value = v != null ? fmtPercent(v) : '-'; cls = utilClass(v); bar = v; break
        }
        case 'diskio': {
          const rw = diskRW(disks, t.param)
          if (!rw) break
          dio = { r: fmtDiskBps(rw.r), w: fmtDiskBps(rw.w) }
          break
        }
        case 'diskrw': {
          // 组合格子：读写速率（口径同 diskio）+ 磁盘使用率进度条
          //（param 指定挂载点取该盘，空=各盘最大值，与 disk 指标一致）
          const pct = pickDisk('percent')
          if (pct == null) break
          value = fmtPercent(pct)
          cls = utilClass(pct)
          const rw = diskRW(disks, t.param)
          drw = {
            r: rw ? fmtDiskBps(rw.r) : '-',
            w: rw ? fmtDiskBps(rw.w) : '-',
            pct,
          }
          break
        }
        case 'net': {
          // param 空=展示网口合计（netSum 返回 {rx,tx}），指定网口取快照原字段（rx_bps/tx_bps）
          if (t.param) {
            const one = L.nets?.find((n) => n.iface === t.param)
            net = { rx: fmtBps(one?.rx_bps), tx: fmtBps(one?.tx_bps) }
          } else {
            const sum = netSum(s)
            net = { rx: fmtBps(sum.rx), tx: fmtBps(sum.tx) }
          }
          break
        }
        // 存量单项配置仍可展示（新建入口已合并为 net）
        case 'net_rx':
          value = fmtBps(t.param ? L.nets?.find((n) => n.iface === t.param)?.rx_bps : netSum(s).rx); break
        case 'net_tx':
          value = fmtBps(t.param ? L.nets?.find((n) => n.iface === t.param)?.tx_bps : netSum(s).tx); break
        case 'load1':
          value = fmtNum(L.load1); break
        case 'load5':
          value = fmtNum(L.load5); break
        case 'load15':
          value = fmtNum(L.load15); break
        case 'tcp_est':
          value = L.tcp_established != null ? String(Math.round(L.tcp_established)) : '-'; break
        case 'uptime':
          value = fmtUptimeDays(L.uptime); break
        case 'temp': {
          // param 空=CPU 温度（无 CPU 采样时取全部传感器最高）；param=传感器名
          const temps = L.temps || []
          if (!temps.length) break
          const one = t.param ? temps.find((x) => x.sensor === t.param) : null
          const v = one ? one.temp : temps.find((x) => x.sensor === 'CPU')?.temp ?? Math.max(...temps.map((x) => x.temp))
          value = `${Number(v).toFixed(1)} ℃`
          cls = utilClass(v) // 复用全局 70 黄 / 90 红档位
          break
        }
      }
    }
    return {
      id: t.id, label: t.label, value, cls, net, dio, drw, cm, bar, tfc, note: '',
      link: t.link || '', // 点击跳转链接（主页模块专用，总览传空即无动作）
      icon: t.icon || '', // 标签前小图标（主页模块专用）
      server_id: t.server_id, serverName: s?.name || t.server_name || tr('tileViews.serverDeleted'),
      remark: s?.remark || '',
    }
  })
}
