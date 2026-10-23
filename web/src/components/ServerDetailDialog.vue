<template>
  <el-dialog v-model="serverDetail.open" width="min(96vw, 1240px)" append-to-body destroy-on-close>
    <template #header>
      <div class="sdd-head">
        <span class="title" :title="withRemark(server?.hostname || server?.name || '', server?.remark)"><img v-if="server?.icon" class="srv-ico" :src="server.icon" alt="" @error="$event.target.style.display = 'none'" />{{ server ? withSpec(server.name, server) : '…' }}</span>
        <el-tag v-if="server" :type="server.online ? 'success' : 'info'" size="small">
          {{ server.online ? '在线' : server.enabled ? '离线' : '已停用' }}
        </el-tag>
        <span class="sysinfo">
          <template v-if="server?.hostname">{{ server.hostname }} · </template>
          {{ server?.os }} · {{ server?.arch }} · {{ server?.cpu_model }} ×{{ server?.cpu_cores }} ·
          {{ server?.kernel }}
          <template v-if="server?.latest"> · 开机 {{ fmtUptimeDays(server.latest.uptime) }}</template>
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

      <!-- 其他类型卡片一行两个：全部磁盘挂载点 + 监听端口 -->
      <el-row v-if="mounts.length || listens.length" :gutter="12">
        <el-col v-if="mounts.length" :xs="24" :lg="12">
          <el-card shadow="never" class="mount-card">
            <template #header>磁盘挂载点（{{ mounts.length }}）</template>
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
            <template #header>监听端口（{{ listens.length }}）</template>
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
            <template #header>温度传感器（{{ temps.length }}）</template>
            <div v-for="t in temps" :key="t.sensor" class="mount-row">
              <span class="mount-name" :title="t.sensor">{{ tempLabel(t.sensor) }}</span>
              <span class="temp-num" :class="tempClass(t.temp)">{{ t.temp.toFixed(1) }} ℃</span>
            </div>
          </el-card>
        </el-col>
        <el-col v-if="fans.length" :xs="24" :lg="12">
          <el-card shadow="never" class="listen-card">
            <template #header>风扇（{{ fans.length }}）</template>
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
            <template #header>Docker 容器（{{ containers.length }}）<span v-if="!canControl" class="ro-hint">只读 · 需 Agent 上报模式</span></template>
            <div v-for="ct in containers" :key="ct.id" class="mount-row app-row" :title="ct.image">
              <el-tag :type="containerTag(ct.state)" size="small" class="app-tag">{{ ct.state }}</el-tag>
              <span class="app-name" :title="ct.name">{{ ct.name }}</span>
              <span class="app-sub">{{ ct.status || ct.image }}</span>
              <span v-if="canControl" class="app-ops">
                <el-button v-if="ct.state === 'running'" size="small" link type="danger" :disabled="!!busy['docker:' + ct.name]" @click="appAction('docker', ct.name, 'stop')">停止</el-button>
                <el-button v-if="ct.state === 'running'" size="small" link type="primary" :disabled="!!busy['docker:' + ct.name]" @click="appAction('docker', ct.name, 'restart')">重启</el-button>
                <el-button v-if="ct.state !== 'running'" size="small" link type="primary" :disabled="!!busy['docker:' + ct.name]" @click="appAction('docker', ct.name, 'start')">启动</el-button>
              </span>
            </div>
          </el-card>
        </el-col>
        <el-col v-if="vms.length" :xs="24" :lg="12">
          <el-card shadow="never" class="mount-card">
            <template #header>虚拟机（{{ vms.length }}）<span v-if="!canControl" class="ro-hint">只读 · 需 Agent 上报模式</span></template>
            <div v-for="vm in vms" :key="vm.name" class="mount-row app-row">
              <el-tag :type="vmTag(vm.state)" size="small" class="app-tag">{{ vmState(vm.state) }}</el-tag>
              <span class="app-name" :title="vm.name">{{ vm.name }}</span>
              <span class="app-ops">
                <el-button v-if="vmUp(vm)" size="small" link type="danger" :disabled="!!busy['vm:' + vm.name]" @click="appAction('vm', vm.name, 'shutdown')">关机</el-button>
                <el-button v-if="vmUp(vm)" size="small" link type="danger" :disabled="!!busy['vm:' + vm.name]" @click="appAction('vm', vm.name, 'destroy')">强制关机</el-button>
                <el-button v-else size="small" link type="primary" :disabled="!!busy['vm:' + vm.name]" @click="appAction('vm', vm.name, 'start')">启动</el-button>
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
import { bpsAxisFmt, diskAxisFmt, fmtBps, fmtBytes, fmtDiskBps, fmtPercent, fmtUptimeDays, pct, splitIfaces, utilClass, withRemark, withSpec } from '../utils/format.js'
import { NET_DOWN_COLOR, NET_UP_COLOR, barColor, snapRange } from '../utils/chartTheme.js'

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
  if (s === 'running') return '运行中'
  if (s === 'shut off') return '已关机'
  if (s === 'paused') return '已暂停'
  return state || '未知'
}
function vmUp(vm) {
  const s = (vm.state || '').toLowerCase()
  return s !== 'shut off' && s !== '' // 运行/暂停等非常驻关机态都按可关机处理
}

// appAction 下发启停：确认弹窗（关机/强制关机强提示）→ 入队 → 轮询任务状态
// 直到终态（agent 下轮上报才领取，通常 1 个上报周期内完成）→ 刷新列表
async function appAction(kind, target, action) {
  const tips = {
    'docker|start': `启动容器「${target}」？`,
    'docker|stop': `停止容器「${target}」？`,
    'docker|restart': `重启容器「${target}」？`,
    'vm|start': `启动虚拟机「${target}」？`,
    'vm|shutdown': `关闭虚拟机「${target}」？等同按电源键，虚拟机内未保存的数据可能丢失。`,
    'vm|destroy': `强制断电虚拟机「${target}」？等同拔电源，可能损坏虚拟机文件系统，仅在正常关机无效时使用。`,
  }
  try {
    await ElMessageBox.confirm(tips[kind + '|' + action] || `对「${target}」执行 ${action}？`, '操作确认', {
      type: 'warning',
      confirmButtonText: '确认',
      cancelButtonText: '取消',
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
      ElMessage.success(st.message || '操作已完成')
    } else {
      ElMessage.error((st && st.message) || '操作失败或超时，请稍后刷新查看状态')
    }
    loadServer() // 拉最新快照反映新状态
  } catch (e) {
    ElMessage.error(e?.message || '下发失败')
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
  return addrs.length ? `（${addrs.join(' ')}）` : ''
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
      name: k.slice(4).replace(/\.rx$/, ' 下行').replace(/\.tx$/, ' 上行'),
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
    if (m[rk]) out.push({ name: `${dev} 读`, points: m[rk].map(([t, v]) => [t, -v]), color: NET_DOWN_COLOR })
    if (m[wk]) out.push({ name: `${dev} 写`, points: m[wk], color: NET_UP_COLOR })
    return out
  })

  const list = [
    { title: 'CPU 使用率', percent: true, series: [{ name: 'CPU', points: m.cpu || [] }] },
    { title: '内存使用率', percent: true, series: [{ name: '内存', points: m.mem || [] }] },
    {
      title: '系统负载',
      series: [
        { name: 'load1', points: m.load1 || [] },
        { name: 'load5', points: m.load5 || [] },
        { name: 'load15', points: m.load15 || [] },
      ],
    },
    {
      title: '网络流量',
      mirror: true, // 上下镜像面积图：上行向上、下行向下共用 0 轴
      series: netSeries,
      fmt: (v) => fmtBps(Math.abs(v ?? 0)),
      yFmt: bpsAxisFmt(netSeries),
    },
    {
      title: '磁盘 IO',
      mirror: true, // 与网络图同款镜像：写向上、读向下共用 0 轴
      series: ioSeries,
      fmt: (v) => fmtDiskBps(Math.abs(v ?? 0)),
      yFmt: diskAxisFmt(ioSeries),
    },
    { title: 'TCP 连接数', series: [{ name: 'ESTABLISHED', points: m.tcp_est || [] }] },
  ]

  // 温度/风扇：temp.<sensor> / fan.<sensor> 每传感器一条线（旧版 agent / 无
  // 传感器机器不产出序列，整卡隐藏）
  const tempSeries = Object.keys(m)
    .filter((k) => k.startsWith('temp.'))
    .sort()
    .filter((k) => m[k])
    .map((k) => ({ name: k.slice(5), points: m[k] }))
  if (tempSeries.length) {
    list.push({ title: '温度（℃）', series: tempSeries, fmt: (v) => fmtTemp(v) })
  }
  const fanSeries = Object.keys(m)
    .filter((k) => k.startsWith('fan.'))
    .sort()
    .filter((k) => m[k])
    .map((k) => ({ name: k.slice(4), points: m[k] }))
  if (fanSeries.length) {
    list.push({ title: '风扇转速（RPM）', series: fanSeries, fmt: (v) => `${Math.round(v ?? 0)} RPM` })
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
      loadServer()
      loadHistory()
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
