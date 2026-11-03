<template>
  <el-dialog v-model="serverDetail.open" width="min(96vw, 1240px)" append-to-body destroy-on-close>
    <template #header>
      <div class="sdd-head">
        <span class="title" :title="withRemark(server?.hostname || server?.name || '', server?.remark)"><img v-if="server?.icon" class="srv-ico" :src="server.icon" alt="" @error="$event.target.style.display = 'none'" />{{ server ? withSpec(server.name, server) : '…' }}</span>
        <el-tag v-if="server" :type="server.online ? 'success' : 'info'" size="small">
          {{ server.online ? $t('serverdlg.detail.online') : server.enabled ? $t('serverdlg.detail.offline') : $t('serverdlg.detail.disabled') }}
        </el-tag>
        <span class="sysinfo">
          <template v-if="server?.hostname">{{ server.hostname }} · </template>
          {{ server?.os }} · {{ server?.arch }} · {{ server?.cpu_model }} ×{{ server?.cpu_cores }} ·
          {{ server?.kernel }}
          <template v-if="server?.latest"> · {{ $t('serverdlg.detail.boot', { n: fmtUptimeDays(server.latest.uptime) }) }}</template>
        </span>
      </div>
    </template>

    <div class="sdd-body">
      <div class="range-row">
        <TimeRangePicker v-model:range="range" v-model:auto="auto" @query="onCustomQuery" />
      </div>

      <!-- 折线图一行三个 -->
      <el-row :gutter="12">
        <el-col :xs="24" :lg="8" v-for="chart in charts" :key="chart.title">
          <el-card shadow="never" class="chart-card">
            <template #header>{{ chart.title }}</template>
            <!-- 点击图表区域放大查看（弹层内自选时段） -->
            <div class="zoom-wrap" @click="openZoom(chart)">
              <SeriesChart
                :series="chart.series"
                :percent="chart.percent"
                :mirror="chart.mirror"
                :value-formatter="chart.fmt"
                :y-axis-formatter="chart.yFmt"
                :height="'159px'"
              />
            </div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 外网流量账本：统计网口累计收发按日汇总（服务端对 /proc/net/dev 计数器差分，
           与上面的速率曲线无关）；本月合计 + 近 30 天逐日柱 + 当年逐月 -->
      <el-row v-if="server" :gutter="12">
        <el-col :xs="24">
          <el-card shadow="never" class="mount-card">
            <template #header>
              <div class="traffic-head">
                <span>{{ $t('serverdlg.detail.trafficTitle') }}</span>
                <span v-if="traffic?.ifaces?.length" class="traffic-sub">{{ $t('serverdlg.detail.trafficIfaces', { n: traffic.ifaces.join(', ') }) }}</span>
                <span v-if="traffic?.since" class="traffic-sub">{{ $t('serverdlg.detail.trafficSince', { n: traffic.since }) }}</span>
              </div>
            </template>
            <template v-if="traffic?.since">
              <div class="traffic-months">
                <span v-for="m in trafficMonths" :key="m.month" class="traffic-month" :class="{ cur: m.cur }">
                  <b>{{ m.cur ? $t('serverdlg.detail.trafficMonth') : m.month }}</b>
                  <span class="traffic-dn">↓ {{ fmtBytes(m.rx) }}</span>
                  <span class="traffic-up">↑ {{ fmtBytes(m.tx) }}</span>
                </span>
              </div>
              <NetTrafficChart :days="trafficDays" />
            </template>
            <div v-else class="traffic-empty">{{ $t('serverdlg.detail.trafficEmpty') }}</div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 其他类型卡片一行两个：全部磁盘挂载点 + 监听端口 -->
      <el-row v-if="mounts.length || listens.length" :gutter="12">
        <el-col v-if="mounts.length" :xs="24" :lg="12">
          <el-card shadow="never" class="mount-card">
            <template #header>{{ $t('serverdlg.detail.mountsTitle', { n: mounts.length }) }}</template>
            <div v-for="d in mounts" :key="d.mount" class="mount-row">
              <span class="mount-name" :title="d.mount">{{ d.mount }}</span>
              <el-progress
                class="mount-bar"
                :percentage="pct(d.percent)"
                :stroke-width="8"
                :show-text="false"
                :color="barColor(d.percent)"
              />
              <span class="mount-num">{{ fmtBytes(d.used) }} / {{ fmtBytes(d.total) }}</span>
              <span class="mount-pct" :class="utilClass(d.percent)">{{ fmtPercent(d.percent) }}</span>
            </div>
          </el-card>
        </el-col>
        <el-col v-if="listens.length" :xs="24" :lg="12">
          <el-card shadow="never" class="listen-card">
            <template #header>{{ $t('serverdlg.detail.listensTitle', { n: listens.length }) }}</template>
            <div class="listen-wrap">
              <el-tag
                v-for="l in listens"
                :key="l.proto + ':' + l.port"
                :type="l.proto === 'tcp' ? 'primary' : 'warning'"
                size="small"
                class="listen-tag"
                :title="(l.addrs || []).join(' ')"
              >
                {{ l.proto.toUpperCase() }} {{ l.port }}<span v-if="listenAddrs(l)" class="listen-addr">{{ listenAddrs(l) }}</span>
              </el-tag>
            </div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 温度 / 风扇当前值（旧版 agent / 无传感器机器不显示） -->
      <el-row v-if="temps.length || fans.length" :gutter="12">
        <el-col v-if="temps.length" :xs="24" :lg="12">
          <el-card shadow="never" class="mount-card">
            <template #header>{{ $t('serverdlg.detail.tempsTitle', { n: temps.length }) }}</template>
            <div v-for="t in temps" :key="t.sensor" class="mount-row">
              <span class="mount-name" :title="t.sensor">{{ tempLabel(t.sensor) }}</span>
              <span class="temp-num" :class="tempClass(t.temp)">{{ t.temp.toFixed(1) }} ℃</span>
            </div>
          </el-card>
        </el-col>
        <el-col v-if="fans.length" :xs="24" :lg="12">
          <el-card shadow="never" class="listen-card">
            <template #header>{{ $t('serverdlg.detail.fansTitle', { n: fans.length }) }}</template>
            <div class="listen-wrap">
              <el-tag v-for="f in fans" :key="f.sensor" size="small" class="listen-tag" :title="f.sensor">
                {{ f.sensor }} {{ f.rpm }} RPM
              </el-tag>
            </div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 应用负载（54 飞牛集成）：Docker 容器 + 虚拟机；push 模式可远程启停 -->
      <el-row v-if="containers.length || vms.length" :gutter="12">
        <el-col v-if="containers.length" :xs="24" :lg="12">
          <el-card shadow="never" class="mount-card">
            <template #header>{{ $t('serverdlg.detail.containersTitle', { n: containers.length }) }}<span v-if="!canControl" class="ro-hint">{{ $t('serverdlg.detail.roHint') }}</span></template>
            <div v-for="ct in containers" :key="ct.id" class="mount-row app-row" :title="ct.image">
              <el-tag :type="containerTag(ct.state)" size="small" class="app-tag">{{ ct.state }}</el-tag>
              <span class="app-name" :title="ct.name">{{ ct.name }}</span>
              <span class="app-sub">{{ ct.status || ct.image }}</span>
              <span v-if="canControl" class="app-ops">
                <el-button v-if="ct.state === 'running'" size="small" link type="danger" :disabled="!!busy['docker:' + ct.name]" @click="appAction('docker', ct.name, 'stop')">{{ $t('serverdlg.detail.stop') }}</el-button>
                <el-button v-if="ct.state === 'running'" size="small" link type="primary" :disabled="!!busy['docker:' + ct.name]" @click="appAction('docker', ct.name, 'restart')">{{ $t('serverdlg.detail.restart') }}</el-button>
                <el-button v-if="ct.state !== 'running'" size="small" link type="primary" :disabled="!!busy['docker:' + ct.name]" @click="appAction('docker', ct.name, 'start')">{{ $t('serverdlg.detail.start') }}</el-button>
              </span>
            </div>
          </el-card>
        </el-col>
        <el-col v-if="vms.length" :xs="24" :lg="12">
          <el-card shadow="never" class="mount-card">
            <template #header>{{ $t('serverdlg.detail.vmsTitle', { n: vms.length }) }}<span v-if="!canControl" class="ro-hint">{{ $t('serverdlg.detail.roHint') }}</span></template>
            <div v-for="vm in vms" :key="vm.name" class="mount-row app-row">
              <el-tag :type="vmTag(vm.state)" size="small" class="app-tag">{{ vmState(vm.state) }}</el-tag>
              <span class="app-name" :title="vm.name">{{ vm.name }}</span>
              <span class="app-ops">
                <el-button v-if="vmUp(vm)" size="small" link type="danger" :disabled="!!busy['vm:' + vm.name]" @click="appAction('vm', vm.name, 'shutdown')">{{ $t('serverdlg.detail.shutdown') }}</el-button>
                <el-button v-if="vmUp(vm)" size="small" link type="danger" :disabled="!!busy['vm:' + vm.name]" @click="appAction('vm', vm.name, 'destroy')">{{ $t('serverdlg.detail.forceOff') }}</el-button>
                <el-button v-else size="small" link type="primary" :disabled="!!busy['vm:' + vm.name]" @click="appAction('vm', vm.name, 'start')">{{ $t('serverdlg.detail.start') }}</el-button>
              </span>
            </div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 图表放大弹层（点击图表区域打开） -->
      <ChartZoomDialog ref="zoomDlg" />
    </div>
  </el-dialog>
</template>

<script setup>
// 服务器明细弹层：全站单例（Layout 挂载），任意页面 openServerDetail(id) 打开。
// 明细不再开新标签；打开即拉服务器信息 + 所选时段 history，时段切换重拉。
defineOptions({ name: 'ServerDetailDialog' })
import { computed, ref, watch, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api/index.js'
import { serverDetail } from '../serverDetail.js'
import SeriesChart from './SeriesChart.vue'
import TimeRangePicker from './TimeRangePicker.vue'
import ChartZoomDialog from './ChartZoomDialog.vue'
import NetTrafficChart from './NetTrafficChart.vue'
import { bpsAxisFmt, diskAxisFmt, fmtBps, fmtBytes, fmtDiskBps, fmtPercent, fmtUptimeDays, pct, splitIfaces, utilClass, withRemark, withSpec } from '../utils/format.js'
import { NET_DOWN_COLOR, NET_UP_COLOR, barColor, snapRange } from '../utils/chartTheme.js'
import { useI18n } from 'vue-i18n'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/serverdlg.js'

const { t: tr } = useI18n() // 别名 tr：函数内已有 [t, v] 解构与 tempClass(t) 参数
registerPageMessages(pageMsgs)

const server = ref(null)
const range = ref(3600)
const custom = ref(null) // 自定义时间段窗口 {from,to} 秒（选回预设档位时清空）
const auto = ref(false)
const seriesMap = ref({})

let timer = null

const serverId = computed(() => serverDetail.id)

async function loadServer() {
  try {
    const res = await api.get(`/servers/${serverId.value}`)
    server.value = res.server
  } catch {
    /* ignore */
  }
}

// 拉指定时段的 history 并转成 key → points 映射（明细与放大弹层共用）；
// win（自定义时间段）给定时用绝对窗口，否则「最近 rangeSec 秒」
async function fetchSeriesMap(rangeSec, win) {
  const to = win ? win.to : Math.floor(Date.now() / 1000)
  const from = win ? win.from : to - rangeSec
  const res = await api.get(`/servers/${serverId.value}/history?from=${from}&to=${to}`)
  const map = {}
  for (const s of res.series || []) map[s.key] = s.points || []
  return map
}

async function loadHistory() {
  if (range.value === -1 && !custom.value) return // 自定义档：等「查询」按钮
  try {
    seriesMap.value = await fetchSeriesMap(range.value, custom.value)
  } catch {
    /* ignore */
  }
}

// 自定义时间段查询（TimeRangePicker「查询」按钮）
function onCustomQuery(w) {
  custom.value = w
  loadHistory()
}

const mounts = computed(() => server.value?.latest?.disks || [])
const listens = computed(() => server.value?.latest?.listens || [])
const temps = computed(() => server.value?.latest?.temps || [])
const fans = computed(() => server.value?.latest?.fans || [])

// ---------- 外网流量账本（日粒度，不随时段选择变化；打开与自动刷新时拉） ----------

const traffic = ref(null)

async function loadTraffic() {
  try {
    traffic.value = await api.get(`/servers/${serverId.value}/traffic`)
  } catch {
    /* ignore */
  }
}

function dayKey(d) {
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

// 近 30 天连续日轴补零；尾日取浏览器今天与账本最晚日期的较大者（服务端时区
// 领先浏览器时账本"今天"不会被截掉）
const trafficDays = computed(() => {
  const rows = traffic.value?.days || []
  const byDay = Object.fromEntries(rows.map((r) => [r.day, r]))
  let end = dayKey(new Date())
  const lastRow = rows.length ? rows[rows.length - 1].day : ''
  if (lastRow > end) end = lastRow
  const [y, m, d] = end.split('-').map(Number)
  const out = []
  for (let i = 29; i >= 0; i--) {
    const day = dayKey(new Date(y, m - 1, d - i))
    out.push({ day, rx: byDay[day]?.rx || 0, tx: byDay[day]?.tx || 0 })
  }
  return out
})

// 当年逐月，最新在前；当月标「本月」
const trafficMonths = computed(() => {
  const cur = dayKey(new Date()).slice(0, 7)
  return [...(traffic.value?.months || [])].reverse().map((m) => ({ ...m, cur: m.month === cur }))
})

// ---------- 应用负载（54 飞牛集成）：容器/虚拟机列表与远程启停 ----------

const containers = computed(() => server.value?.latest?.containers || [])
const vms = computed(() => server.value?.latest?.vms || [])
// 仅 Agent 上报（push）模式可远程操作（任务经上报响应捎带下发，pull 无通道）
const canControl = computed(() => server.value?.collect_mode === 'push')
const busy = ref({}) // key: kind:target → 操作进行中（防连点堆指令，服务端亦有活跃唯一约束）

function containerTag(state) {
  if (state === 'running') return 'success'
  if (state === 'paused' || state === 'restarting') return 'warning'
  return 'info'
}
function vmTag(state) {
  const s = (state || '').toLowerCase()
  if (s === 'running') return 'success'
  if (s === 'paused' || s === 'pmsuspended') return 'warning'
  return 'info'
}
function vmState(state) {
  const s = (state || '').toLowerCase()
  if (s === 'running') return tr('serverdlg.detail.vmRunning')
  if (s === 'shut off') return tr('serverdlg.detail.vmOff')
  if (s === 'paused') return tr('serverdlg.detail.vmPaused')
  return state || tr('serverdlg.detail.vmUnknown')
}
function vmUp(vm) {
  const s = (vm.state || '').toLowerCase()
  return s !== 'shut off' && s !== '' // 运行/暂停等非常驻关机态都按可关机处理
}

// appAction 下发启停：确认弹窗（关机/强制关机强提示）→ 入队 → 轮询任务状态
// 直到终态（agent 下轮上报才领取，通常 1 个上报周期内完成）→ 刷新列表
async function appAction(kind, target, action) {
  const tips = {
    'docker|start': tr('serverdlg.detail.actDockerStart', { n: target }),
    'docker|stop': tr('serverdlg.detail.actDockerStop', { n: target }),
    'docker|restart': tr('serverdlg.detail.actDockerRestart', { n: target }),
    'vm|start': tr('serverdlg.detail.actVmStart', { n: target }),
    'vm|shutdown': tr('serverdlg.detail.actVmShutdown', { n: target }),
    'vm|destroy': tr('serverdlg.detail.actVmDestroy', { n: target }),
  }
  try {
    await ElMessageBox.confirm(tips[kind + '|' + action] || tr('serverdlg.detail.actFallback', { n: target, action }), tr('serverdlg.detail.confirmTitle'), {
      type: 'warning',
      confirmButtonText: tr('common.confirm'),
      cancelButtonText: tr('common.cancel'),
    })
  } catch {
    return
  }
  const key = kind + ':' + target
  busy.value[key] = true
  try {
    const res = await api.post(`/servers/${serverId.value}/apps/action`, { kind, target, action })
    const st = await pollAppTask(res.task_id)
    if (st && st.state === 'done' && st.ok) {
      ElMessage.success(st.message || tr('serverdlg.detail.opDone'))
    } else {
      ElMessage.error((st && st.message) || tr('serverdlg.detail.opFailed'))
    }
    loadServer() // 拉最新快照反映新状态
  } catch (e) {
    ElMessage.error(e?.message || tr('serverdlg.detail.dispatchFailed'))
  } finally {
    busy.value[key] = false
  }
}

// pollAppTask 2s 一跳直到终态（done/failed/expired）或 120s 超时（返回 null）；
// 轮询期网络抖动不中断，超时由上层文案兜底
function pollAppTask(id) {
  return new Promise((resolve) => {
    const started = Date.now()
    const tick = async () => {
      let st = null
      try {
        st = await api.get(`/apps-task/${id}`)
        if (['done', 'failed', 'expired'].includes(st.state)) return resolve(st)
      } catch {
        /* 下一跳重试 */
      }
      if (Date.now() - started > 120000) return resolve(st || null)
      setTimeout(tick, 2000)
    }
    tick()
  })
}
// 主板类传感器标签通常是「芯片名 + 通道标签」（如 acpitz temp1），太长，折叠展示
function tempLabel(sensor) {
  return sensor.length > 24 ? sensor.slice(0, 23) + '…' : sensor
}
// 温度状态色：<70 正常、70~85 偏高、>85 过热
function tempClass(t) {
  return t > 85 ? 'temp-hot' : t >= 70 ? 'temp-warm' : ''
}
// 通配地址（0.0.0.0 / ::）不逐个展示，仅非通配绑定时标注（如仅本机 127.0.0.1）
function listenAddrs(l) {
  const addrs = (l.addrs || []).filter((a) => a !== '0.0.0.0' && a !== '::')
  return addrs.length ? tr('serverdlg.detail.addrWrap', { n: addrs.join(' ') }) : ''
}

// 网络流量图仅展示服务器设置里配置的网口（未配置 = 全部），顺序跟随配置
const netIfaces = computed(() => splitIfaces(server.value?.net_ifaces))

// 由 history 序列构造图表：标量直接映射，net.* / diskio.* 按前缀聚合
// （磁盘占用率仅存最新、无历史曲线，当前各盘状态见「磁盘挂载点」卡片）。
// 网络图每网口 [上行, 下行] 排列——序列顺序即 tooltip/图例顺序，上行在前；
// 线色按方向钉住：下行蓝、上行橙。磁盘 IO 同款镜像：读向下蓝、写向上橙
function buildCharts(m, ifaces) {
  // net 序列名 net.eth0.rx/net.eth0.tx → 「eth0 下行/上行」，界面不出现 rx/tx 字样
  const netKeys = ifaces.length
    ? ifaces.flatMap((w) => [`net.${w}.tx`, `net.${w}.rx`])
    : Object.keys(m)
        .filter((k) => k.startsWith('net.'))
        .sort()
        // 同网口两键相邻（rx < tx），两两交换成 [上行, 下行]
        .flatMap((k, i, a) => (i % 2 === 0 ? [a[i + 1], k] : []))
  const netSeries = netKeys
    .filter((k) => m[k])
    .map((k) => ({
      name: k.endsWith('.rx') ? tr('serverdlg.detail.netDown', { n: k.slice(4, -3) }) : tr('serverdlg.detail.netUp', { n: k.slice(4, -3) }),
      // 镜像图：下行取负画向 0 轴下方，上行保持向上（tooltip/轴刻度取绝对值）
      points: k.endsWith('.rx') ? m[k].map(([t, v]) => [t, -v]) : m[k],
      color: k.endsWith('.rx') ? NET_DOWN_COLOR : NET_UP_COLOR,
    }))

  // 磁盘 IO：diskio.<device>.read|write → 「<device> 读/写」，读写速率 B/s
  const ioReadKeys = Object.keys(m).filter((k) => k.endsWith('.read') && k.startsWith('diskio.')).sort()
  const ioSeries = ioReadKeys.flatMap((rk) => {
    const wk = rk.replace(/\.read$/, '.write')
    const dev = rk.slice(7, -5)
    const out = []
    if (m[rk]) out.push({ name: tr('serverdlg.detail.ioRead', { n: dev }), points: m[rk].map(([t, v]) => [t, -v]), color: NET_DOWN_COLOR })
    if (m[wk]) out.push({ name: tr('serverdlg.detail.ioWrite', { n: dev }), points: m[wk], color: NET_UP_COLOR })
    return out
  })

  const list = [
    { title: tr('tileMetrics.cpu'), percent: true, series: [{ name: 'CPU', points: m.cpu || [] }] },
    { title: tr('tileMetrics.mem'), percent: true, series: [{ name: tr('serverdlg.detail.memSeries'), points: m.mem || [] }] },
    {
      title: tr('serverdlg.detail.loadTitle'),
      series: [
        { name: 'load1', points: m.load1 || [] },
        { name: 'load5', points: m.load5 || [] },
        { name: 'load15', points: m.load15 || [] },
      ],
    },
    {
      title: tr('serverdlg.detail.netTitle'),
      mirror: true, // 上下镜像面积图：上行向上、下行向下共用 0 轴
      series: netSeries,
      fmt: (v) => fmtBps(Math.abs(v ?? 0)),
      yFmt: bpsAxisFmt(netSeries),
    },
    {
      title: tr('serverdlg.detail.ioTitle'),
      mirror: true, // 与网络图同款镜像：写向上、读向下共用 0 轴
      series: ioSeries,
      fmt: (v) => fmtDiskBps(Math.abs(v ?? 0)),
      yFmt: diskAxisFmt(ioSeries),
    },
    { title: tr('tileMetrics.tcp_est'), series: [{ name: 'ESTABLISHED', points: m.tcp_est || [] }] },
  ]

  // 温度/风扇：temp.<sensor> / fan.<sensor> 每传感器一条线（旧版 agent / 无
  // 传感器机器不产出序列，整卡隐藏）
  const tempSeries = Object.keys(m)
    .filter((k) => k.startsWith('temp.'))
    .sort()
    .filter((k) => m[k])
    .map((k) => ({ name: k.slice(5), points: m[k] }))
  if (tempSeries.length) {
    list.push({ title: tr('serverdlg.detail.tempTitle'), series: tempSeries, fmt: (v) => fmtTemp(v) })
  }
  const fanSeries = Object.keys(m)
    .filter((k) => k.startsWith('fan.'))
    .sort()
    .filter((k) => m[k])
    .map((k) => ({ name: k.slice(4), points: m[k] }))
  if (fanSeries.length) {
    list.push({ title: tr('serverdlg.detail.fanTitle'), series: fanSeries, fmt: (v) => `${Math.round(v ?? 0)} RPM` })
  }
  return list.filter((c) => c.series.length > 0 && c.series.some((s) => s.points.length > 0))
}

function fmtTemp(v) {
  return v == null ? '-' : `${Number(v).toFixed(1)} ℃`
}

const charts = computed(() => buildCharts(seriesMap.value, netIfaces.value))

// ---------- 图表放大弹层 ----------

const zoomDlg = ref(null)

// 按所选时段重新拉 history，用同一套 buildCharts 构造序列；按稳定标题取图
//（列表会过滤掉该时段无数据的图，按下标取会错位）
async function loadChartSeries(title, rangeSec, win) {
  const map = await fetchSeriesMap(rangeSec, win)
  return buildCharts(map, netIfaces.value).find((c) => c.title === title)?.series || []
}

function openZoom(chart) {
  zoomDlg.value.open({
    title: `${withSpec(server.value?.name || '', server.value)} · ${chart.title}`,
    percent: chart.percent,
    bps: !!chart.mirror,
    initialRange: snapRange(range.value),
    loader: (r, win) => loadChartSeries(chart.title, r, win),
  })
}

function restartAuto() {
  clearInterval(timer)
  timer = null
  if (auto.value && serverDetail.open) {
    timer = setInterval(() => {
      loadServer()
      loadHistory()
      loadTraffic()
    }, 30 * 1000)
  }
}

// 打开即加载；换 id（不关弹层直接从另一台打开）也重拉
watch(
  () => [serverDetail.open, serverDetail.id],
  ([open], [prevOpen]) => {
    if (!open) {
      clearInterval(timer)
      timer = null
      return
    }
    if (open !== prevOpen || !server.value || server.value.id !== serverDetail.id) {
      server.value = null
      seriesMap.value = {}
      traffic.value = null
      loadServer()
      loadHistory()
      loadTraffic()
    }
    restartAuto()
  },
)

// 选回预设档位时清自定义窗口并重拉；-1（自定义档）等「查询」按钮
watch(range, (v) => {
  if (v === -1) return
  custom.value = null
  loadHistory()
})
watch(auto, restartAuto)
onUnmounted(() => clearInterval(timer))
</script>

<style scoped>
.sdd-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding-right: 16px;
}
.title {
  font-size: 17px;
  font-weight: 700;
}
.sysinfo {
  color: var(--muted);
  font-size: 12px;
}
.sdd-body {
  max-height: calc(88vh - 100px);
  overflow: auto;
}
.range-row {
  margin-bottom: 12px;
}
/* 磁盘挂载点明细：挂载点 + 条 + 用量 + 百分比，单行不折行 */
.mount-card {
  margin-bottom: 12px;
}
.mount-row {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  padding: 4px 0;
  font-variant-numeric: tabular-nums;
}
.mount-name {
  width: 130px;
  flex: none;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-2);
}
.mount-bar {
  flex: 1;
  min-width: 0;
}
.mount-num {
  flex: none;
  color: var(--text-2);
  white-space: nowrap;
}
.mount-pct {
  width: 48px;
  flex: none;
  text-align: right;
  white-space: nowrap;
}
.temp-num {
  flex: none;
  font-variant-numeric: tabular-nums;
}
.temp-warm {
  color: var(--el-color-warning);
  font-weight: 600;
}
.temp-hot {
  color: var(--el-color-danger);
  font-weight: 600;
}
.chart-card {
  margin-bottom: 12px;
}
/* 外网流量卡：头行标题 + 统计网口/起始日小字；逐月胶囊一行横排，本月加粗描边；
   下行/上行箭头色与柱图方向色一致（CHART_COLORS 槽 1 蓝 / 槽 2 橙） */
.traffic-head {
  display: flex;
  align-items: baseline;
  gap: 12px;
  flex-wrap: wrap;
}
.traffic-sub {
  color: var(--muted);
  font-size: 12px;
  font-weight: 400;
}
.traffic-months {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 14px;
  margin-bottom: 8px;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.traffic-month {
  display: inline-flex;
  gap: 8px;
  align-items: baseline;
  color: var(--text-2);
}
.traffic-month b {
  color: var(--text-1, inherit);
  font-weight: 500;
}
.traffic-month.cur b {
  font-weight: 700;
}
.traffic-dn {
  color: #2a78d6;
}
.traffic-up {
  color: #eb6834;
}
.traffic-empty {
  color: var(--muted);
  font-size: 13px;
  padding: 6px 0;
}
/* 图表区域点击放大 */
.zoom-wrap {
  cursor: zoom-in;
}
/* 图表卡片表头：黑体加粗，行高 40px；内边距压低把高度留给折线图 */
.chart-card :deep(.el-card__header) {
  padding: 2px 8px;
  font-size: 13px;
  font-weight: 600;
  line-height: 40px;
}
.chart-card :deep(.el-card__body) {
  padding: 4px 8px;
}
/* 监听端口：tag 流式排布 */
.listen-card {
  margin-bottom: 12px;
}
.listen-wrap {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.listen-addr {
  opacity: 0.75;
  margin-left: 3px;
}
/* 应用负载行：状态 tag + 名称 + 人读状态 + 行内启停按钮 */
.ro-hint {
  margin-left: 8px;
  font-size: 12px;
  font-weight: 400;
  color: var(--muted);
}
.app-row {
  gap: 8px;
}
.app-tag {
  flex: none;
  width: 64px;
  justify-content: center;
}
.app-name {
  flex: none;
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}
.app-sub {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-2);
  font-size: 12px;
}
.app-ops {
  margin-left: auto;
  flex: none;
  display: inline-flex;
  gap: 2px;
}
</style>
