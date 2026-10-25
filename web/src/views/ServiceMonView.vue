<template>
  <div class="svcmon">
    <el-tabs v-model="pageTab" class="svcmon-tabs" @tab-change="onPageTab">
      <el-tab-pane :label="$t('svcmon.tabAiusage')" name="aiusage" lazy>
        <AiUsageView />
      </el-tab-pane>
      <el-tab-pane :label="$t('svcmon.tabSvcmon')" name="svcmon" lazy>
      <div class="toolbar">
        <span class="updated">{{ $t('svcmon.descProbe') }}{{ loadedAt ? $t('svcmon.updatedAt', { t: loadedAt }) : $t('svcmon.descIdle') }}</span>
        <div class="tools">
          <el-button :loading="checkingAll" @click="checkAll">
            <el-icon v-if="!checkingAll"><Refresh /></el-icon>
            <span v-if="!checkingAll">{{ $t('svcmon.checkAll') }}</span>
          </el-button>
          <el-button type="primary" @click="openAddServer">
            <el-icon><Plus /></el-icon>
            <span>{{ $t('svcmon.addServer') }}</span>
          </el-button>
        </div>
      </div>

      <div v-if="servers.length" class="s-grid">
        <el-card v-for="s in servers" :key="s.id" shadow="never" class="s-card">
          <div class="s-head">
            <div class="s-title">
              <span class="s-dot" :class="serverDot(s)"></span>
              <span class="s-name">{{ s.name || s.host }}</span>
              <el-tag v-if="!s.enabled" size="small" type="info" effect="plain">{{ $t('svcmon.disabled') }}</el-tag>
            </div>
            <el-switch :model-value="s.enabled" :loading="switchingId === s.id" @change="(v) => toggleServer(s, v)" />
          </div>
          <div class="s-host">{{ $t('svcmon.hostEvery', { host: s.host, interval: intervalLabel(s.interval_sec) }) }}</div>

          <div v-if="s.ports?.length" class="p-list">
            <div v-for="p in s.ports" :key="p.id" class="p-row">
              <span class="s-dot p-dot" :class="dotClass(p)"></span>
              <div class="p-main">
                <div class="p-line">
                  <span v-if="p.scheme === 'http' || p.scheme === 'https'" class="p-scheme">{{ p.scheme.toUpperCase() }}</span>
                  <span class="p-name">{{ p.name || $t('svcmon.portN', { n: p.port }) }}</span>
                  <span class="p-num">:{{ p.port }}</span>
                  <span v-if="p.up === 1" class="p-latency">{{ p.latency_ms }} ms</span>
                  <span class="p-state" :class="dotClass(p)">{{ stateText(p) }}</span>
                </div>
                <div class="p-meta">
                  <span v-if="p.up !== -1 && p.since">{{ $t('svcmon.upFor', { t: fmtDur(p.since) }) }}</span>
                  <span v-else>{{ $t('svcmon.notChecked') }}</span>
                  <span v-if="p.checked_at"> · {{ $t('svcmon.checkedAt', { t: fmtTime(p.checked_at) }) }}</span>
                  <span v-if="p.scheme !== 'tcp' && p.domain"> · {{ p.scheme.toUpperCase() }} {{ p.domain }}</span>
                  <span v-if="availOf(p.id) !== null" class="p-avail" :class="availClass(availOf(p.id))"> · {{ $t('svcmon.avail7d', { pct: availOf(p.id) }) }}</span>
                </div>
                <div v-if="p.up === 0 && p.last_error" class="p-err">{{ p.last_error }}</div>
              </div>
              <div class="p-acts">
                <el-button link size="small" :loading="checkingId === p.id" @click="checkPort(p)">{{ $t('svcmon.check') }}</el-button>
                <el-button link size="small" @click="openEditPort(s, p)">{{ $t('common.edit') }}</el-button>
                <el-button link type="danger" size="small" @click="removePort(p)">{{ $t('common.delete') }}</el-button>
              </div>
            </div>
          </div>
          <div v-else class="p-none">{{ $t('svcmon.nonePorts') }}</div>

          <div class="s-foot">
            <el-button size="small" :loading="kickingId === s.id" @click="kickServer(s)">{{ $t('svcmon.checkServer') }}</el-button>
            <el-button size="small" type="primary" plain @click="openAddPort(s)">{{ $t('svcmon.addPort') }}</el-button>
            <span class="s-flex"></span>
            <el-button link size="small" @click="openEditServer(s)">{{ $t('common.edit') }}</el-button>
            <el-button link type="danger" size="small" @click="removeServer(s)">{{ $t('common.delete') }}</el-button>
          </div>
        </el-card>
      </div>
      <el-empty v-else-if="!loading" :description="$t('svcmon.empty')" />

      <!-- 最近状态翻转事件（500）：翻转才记一条，恢复行展示此前持续时长 -->
      <el-card v-if="events.length" shadow="never" class="e-card">
        <template #header>
          <div class="e-head">
            <span class="e-title">{{ $t('svcmon.eventsTitle') }}</span>
            <span class="e-sub">{{ $t('svcmon.eventsSub') }}</span>
          </div>
        </template>
        <div class="e-list">
          <div v-for="ev in events" :key="ev.id" class="e-row">
            <span class="s-dot e-dot" :class="ev.up === 1 ? 'ok' : 'bad'"></span>
            <span class="e-time">{{ fmtTime(ev.occurred_at) }}</span>
            <span class="e-name">{{ ev.server_name || ev.host }} · {{ portLabel(ev) }}</span>
            <el-tag size="small" :type="ev.up === 1 ? 'success' : 'danger'" effect="plain">{{ ev.up === 1 ? $t('svcmon.evUp') : $t('svcmon.evDown') }}</el-tag>
            <span v-if="ev.up === 1 && ev.gap > 0" class="e-gap">{{ $t('svcmon.evPrevUp', { t: fmtSpan(ev.gap) }) }}</span>
            <span v-if="ev.up === 0 && ev.error" class="e-err">{{ ev.error }}</span>
          </div>
        </div>
      </el-card>

      <!-- 新增/编辑服务器 -->
      <el-dialog v-model="sDlg" :title="sf.id ? $t('svcmon.editServer') : $t('svcmon.addServer')" width="460px" :close-on-click-modal="false" :before-close="formGuard.beforeClose" append-to-body>
        <el-form label-width="90px">
          <el-form-item :label="$t('svcmon.labelName')">
            <el-input v-model="sf.name" :placeholder="$t('svcmon.phName')" maxlength="40" />
          </el-form-item>
          <el-form-item :label="$t('svcmon.labelHost')">
            <el-input v-model="sf.host" :placeholder="$t('svcmon.phHost')" maxlength="255" />
          </el-form-item>
          <el-form-item :label="$t('svcmon.labelInterval')">
            <el-select v-model="sf.interval_sec" style="width: 100%">
              <el-option v-for="sec in INTERVALS" :key="sec" :label="intervalLabel(sec)" :value="sec" />
            </el-select>
          </el-form-item>
          <el-form-item :label="$t('svcmon.labelEnabled')">
            <el-switch v-model="sf.enabled" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="sDlg = false">{{ $t('common.cancel') }}</el-button>
          <el-button type="primary" :loading="saving" @click="saveServer">{{ $t('common.save') }}</el-button>
        </template>
      </el-dialog>

      <!-- 添加/编辑端口 -->
      <el-dialog v-model="pDlg" :title="pf.id ? $t('svcmon.editPort') : $t('svcmon.addPort')" width="460px" :close-on-click-modal="false" :before-close="formGuard.beforeClose" append-to-body>
        <el-form label-width="90px">
          <el-form-item :label="$t('svcmon.labelBizName')">
            <el-input v-model="pf.name" :placeholder="$t('svcmon.phBizName')" maxlength="40" />
          </el-form-item>
          <el-form-item :label="$t('svcmon.labelScheme')">
            <el-radio-group v-model="pf.scheme" @change="onSchemeChange">
              <el-radio-button value="tcp">{{ $t('svcmon.schemeTcp') }}</el-radio-button>
              <el-radio-button value="http">HTTP</el-radio-button>
              <el-radio-button value="https">HTTPS</el-radio-button>
            </el-radio-group>
          </el-form-item>
          <el-form-item v-if="pf.scheme !== 'tcp'" :label="$t('svcmon.labelDomain')">
            <el-input v-model="pf.domain" :placeholder="curHost ? $t('svcmon.phDomain', { host: curHost }) : $t('svcmon.phDomainNoHost')" maxlength="255" />
          </el-form-item>
          <el-form-item :label="$t('svcmon.labelPort')">
            <el-input-number v-model="pf.port" :min="1" :max="65535" :controls="false" style="width: 100%" :placeholder="portPlaceholder" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="test" :loading="testing">{{ $t('svcmon.testConn') }}</el-button>
          <el-button @click="pDlg = false">{{ $t('common.cancel') }}</el-button>
          <el-button type="primary" :loading="saving" @click="savePort">{{ $t('common.save') }}</el-button>
        </template>
      </el-dialog>
      </el-tab-pane>
      <el-tab-pane :label="$t('svcmon.tabGh')" name="gh" lazy>
        <GhView />
      </el-tab-pane>
      <el-tab-pane label="MetaCube" name="mihomo" lazy>
        <MihomoView />
      </el-tab-pane>
      <el-tab-pane :label="$t('svcmon.tabCerts')" name="certs" lazy>
        <CertsView />
      </el-tab-pane>
    </el-tabs>
  </div>
</template>


<script setup>
// 服务监控（366/367 两级重构）：按「服务器 → 端口（业务名字）」组织 TCP 拨测。
// 状态由后台按服务器间隔轮询维护，页面读库直出 + 15s 轮询；端口级同步单检、
// 整台/全部为后台异步 + 补拉。
defineOptions({ name: 'ServiceMonView' })
import { computed, onActivated, onDeactivated, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { fmtTime } from '../utils/format.js'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { lang, registerPageMessages } from '../i18n'
import { useI18n } from 'vue-i18n'
import pageMsgs from '../i18n/pages/svcmon.js'
import AiUsageView from './AiUsageView.vue'
import CertsView from './CertsView.vue'
import GhView from './GhView.vue'
import MihomoView from './MihomoView.vue'

const { t } = useI18n()
registerPageMessages(pageMsgs)

// 五功能 tab 容器（555 起三功能，TODO 7 增 mihomo，TODO 26 增证书监控）：
// 用量查询 | 端口监控 | GitHub 发布 | MetaCube | 证书监控；tab 同步 ?tab= 可书签，
// 旧 /console/aiusage、/console/gh、/console/certs 地址 redirect 进来落在对应 tab
const route = useRoute()
const router = useRouter()
const VALID_TABS = ['aiusage', 'svcmon', 'gh', 'mihomo', 'certs']
const pageTab = ref(VALID_TABS.includes(route.query.tab) ? route.query.tab : 'aiusage')
function onPageTab(name) {
  router.replace({ query: { ...route.query, tab: name } })
}

const INTERVALS = [10, 30, 60, 300, 600, 1800, 3600]

const servers = ref([])
const loading = ref(false)
const loadedAt = ref('')
const checkingAll = ref(false)
const checkingId = ref(0)
const switchingId = ref(0)
const kickingId = ref(0)
// 附属信息（500）：最近翻转事件 + 逐端口 7 天可用率（port_id → pct）。
// 只在进页与手动检测后刷新，不进 15s 轮询（翻转是低频事件，省无谓查询）。
const events = ref([])
const availMap = ref({})

const sDlg = ref(false)
const pDlg = ref(false)
const saving = ref(false)
const testing = ref(false)
const sf = ref({ id: 0, name: '', host: '', interval_sec: 600, enabled: true })
// 协议（495）：tcp=握手（存量行 scheme 空=tcp）、http/https=请求检测；
// http/https 端口可留空走默认（80/443）
const pf = ref({ id: 0, server_id: 0, name: '', port: null, scheme: 'tcp', domain: '' })
const curHost = computed(() => servers.value.find((x) => x.id === pf.value.server_id)?.host || '')
const portPlaceholder = computed(() => (pf.value.scheme === 'https' ? t('svcmon.phPort443') : pf.value.scheme === 'http' ? t('svcmon.phPort80') : t('svcmon.phPortTcp')))
// 切到 http/https 且端口为空时补默认端口；不覆盖已填值
function onSchemeChange(scheme) {
  if (scheme !== 'tcp' && (!pf.value.port || pf.value.port < 1)) {
    pf.value.port = scheme === 'https' ? 443 : 80
  }
}
const formGuard = useDirtyGuard(() => {
  if (sDlg.value) return { ...sf.value }
  if (pDlg.value) return { ...pf.value }
  return null
})

let pollTimer = null

function dotClass(p) {
  if (p.up === 1) return 'ok'
  if (p.up === 0) return 'bad'
  return 'unknown'
}
// 服务器聚合状态：有端口不通即红，全通才绿，其余（含未检测/无端口/停用）灰
function serverDot(s) {
  const ps = s.ports || []
  if (!s.enabled || !ps.length) return 'unknown'
  if (ps.some((p) => p.up === 0)) return 'bad'
  if (ps.every((p) => p.up === 1)) return 'ok'
  return 'unknown'
}
function stateText(p) {
  if (p.up === 1) return t('svcmon.stateOk')
  if (p.up === 0) return t('svcmon.stateDown')
  return t('svcmon.stateUnknown')
}
function availOf(pid) {
  const v = availMap.value[pid]
  return v === undefined ? null : v
}
function availClass(pct) {
  if (pct < 95) return 'bad'
  if (pct < 99) return 'warn'
  return ''
}
// 事件行的端口展示：业务名优先，http(s) 显协议+域名，tcp 显端口数
function portLabel(ev) {
  if (ev.port_name) return ev.port_name
  if (ev.scheme === 'http' || ev.scheme === 'https') {
    const d = ev.domain || ev.host
    return (ev.port === 80 || ev.port === 443) ? `${ev.scheme.toUpperCase()} ${d}` : `${ev.scheme.toUpperCase()} ${d}:${ev.port}`
  }
  return t('svcmon.portN', { n: ev.port })
}
function intervalLabel(sec) {
  if (sec % 60 !== 0) return t('svcmon.secN', { n: sec })
  const m = sec / 60
  return t('svcmon.minN', { n: m })
}
// 状态持续时长：秒级 since 到当前的粗粒度人话（30 秒轮询下分钟级足够）
function fmtDur(sinceTs) {
  return fmtSpan(Math.max(0, Math.floor(Date.now() / 1000) - sinceTs))
}
// 任意秒数跨度的人话（事件行的「此前持续」也用它）
function fmtSpan(secs) {
  if (secs < 60) return t('svcmon.secN', { n: secs })
  if (secs < 3600) return t('svcmon.minN', { n: Math.floor(secs / 60) })
  if (secs < 86400) return t('svcmon.hourMin', { h: Math.floor(secs / 3600), m: Math.floor((secs % 3600) / 60) })
  return t('svcmon.dayHour', { d: Math.floor(secs / 86400), h: Math.floor((secs % 86400) / 3600) })
}

function replacePort(port) {
  for (const s of servers.value) {
    const i = (s.ports || []).findIndex((x) => x.id === port.id)
    if (i >= 0) {
      s.ports[i] = { ...s.ports[i], ...port }
      return
    }
  }
}
function dropPortLocal(pid) {
  for (const s of servers.value) {
    const i = (s.ports || []).findIndex((x) => x.id === pid)
    if (i >= 0) {
      s.ports.splice(i, 1)
      return
    }
  }
}

async function load(showLoading = true) {
  if (showLoading) loading.value = true
  try {
    const res = await api.get('/svcmon')
    servers.value = res.servers || []
    loadedAt.value = new Date().toLocaleTimeString(lang.value === 'en' ? 'en-US' : 'zh-CN', { hour12: false })
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

// 附属信息拉取：最近事件（恢复行的 gap=与同端口上一条事件的间隔，即此前状态持续）+ 可用率
async function loadExtras() {
  try {
    const [evRes, avRes] = await Promise.all([api.get('/svcmon/events?limit=50'), api.get('/svcmon/availability?days=7')])
    const list = evRes.events || []
    const lastAt = {}
    for (const ev of list) {
      ev.gap = lastAt[ev.port_id] ? ev.occurred_at - lastAt[ev.port_id] : 0
      lastAt[ev.port_id] = ev.occurred_at
    }
    events.value = list
    const m = {}
    for (const a of avRes.availability || []) m[a.port_id] = a.pct
    availMap.value = m
  } catch {
    // 附属信息失败不打断主列表（下次进页/手动检测再取）
  }
}

// 触发后台异步检测后的两次补拉（结果陆续入库；翻转可能发生，顺带刷事件/可用率）
function pullSoon() {
  setTimeout(load, 2500, false)
  setTimeout(load, 6000, false)
  setTimeout(loadExtras, 6000)
}

async function checkPort(p) {
  checkingId.value = p.id
  try {
    const res = await api.post(`/svcmon/ports/${p.id}/check`)
    if (res.port) replacePort(res.port)
    loadExtras() // 同步单检可能翻转，直接刷附属信息
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    checkingId.value = 0
  }
}

async function kickServer(s) {
  kickingId.value = s.id
  try {
    await api.post(`/svcmon/servers/${s.id}/check`)
    ElMessage.success(t('svcmon.kickedServer'))
    pullSoon()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    kickingId.value = 0
  }
}

async function checkAll() {
  checkingAll.value = true
  try {
    await api.post('/svcmon/refresh')
    ElMessage.success(t('svcmon.kickedAll'))
    pullSoon()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    checkingAll.value = false
  }
}

async function toggleServer(s, enabled) {
  switchingId.value = s.id
  try {
    const res = await api.put(`/svcmon/servers/${s.id}`, {
      name: s.name, host: s.host, interval_sec: s.interval_sec, enabled,
    })
    if (res.server) {
      s.enabled = res.server.enabled
      if (enabled) pullSoon()
    }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    switchingId.value = 0
  }
}

function openAddServer() {
  sf.value = { id: 0, name: '', host: '', interval_sec: 600, enabled: true }
  sDlg.value = true
  formGuard.snapshot()
}
function openEditServer(s) {
  sf.value = { id: s.id, name: s.name, host: s.host, interval_sec: s.interval_sec, enabled: s.enabled }
  sDlg.value = true
  formGuard.snapshot()
}

async function saveServer() {
  const f = sf.value
  if (!f.host.trim()) {
    ElMessage.warning(t('svcmon.needHost'))
    return
  }
  saving.value = true
  try {
    const body = { name: f.name.trim(), host: f.host.trim(), interval_sec: f.interval_sec, enabled: f.enabled }
    if (f.id) await api.put(`/svcmon/servers/${f.id}`, body)
    else await api.post('/svcmon/servers', body)
    sDlg.value = false
    ElMessage.success(f.id ? t('common.saved') : t('svcmon.added'))
    load(false)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function removeServer(s) {
  const label = s.name || s.host
  try {
    await ElMessageBox.confirm(t('svcmon.delServerConfirm', { name: label }), t('common.delete'), {
      type: 'warning', confirmButtonText: t('common.delete'), cancelButtonText: t('common.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/svcmon/servers/${s.id}`)
    ElMessage.success(t('common.deleted'))
  } catch (e) {
    ElMessage.error(e.message)
  }
  load(false)
}

function openAddPort(s) {
  pf.value = { id: 0, server_id: s.id, name: '', port: null, scheme: 'tcp', domain: '' }
  pDlg.value = true
  formGuard.snapshot()
}
function openEditPort(s, p) {
  pf.value = { id: p.id, server_id: s.id, name: p.name, port: p.port, scheme: p.scheme || 'tcp', domain: p.domain || '' }
  pDlg.value = true
  formGuard.snapshot()
}

// http/https 端口留空时取协议默认（后端同口径兜底）
function effectivePort() {
  const f = pf.value
  if (f.port && f.port >= 1) return Number(f.port)
  if (f.scheme === 'http') return 80
  if (f.scheme === 'https') return 443
  return 0
}

// 未入库连通测试（端口弹窗里先测后存用）：http/https 发请求、tcp 只握手
async function test() {
  const f = pf.value
  const port = effectivePort()
  if (!port || port < 1) {
    ElMessage.warning(t('svcmon.needPort'))
    return
  }
  const server = servers.value.find((x) => x.id === f.server_id)
  if (!server) return
  testing.value = true
  try {
    const res = await api.post('/svcmon/test', { host: server.host, port, scheme: f.scheme, domain: f.domain.trim() })
    if (res.result?.ok) ElMessage.success(f.scheme === 'tcp' ? t('svcmon.tcpOk', { ms: res.result.ms }) : t('svcmon.httpOk', { ms: res.result.ms }))
    else ElMessage.error(t('svcmon.failPrefix', { reason: res.result?.error || (f.scheme === 'tcp' ? t('svcmon.tcpFail') : t('svcmon.httpFail')) }))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testing.value = false
  }
}

async function savePort() {
  const f = pf.value
  const port = effectivePort()
  if (!port || port < 1) {
    ElMessage.warning(t('svcmon.portRule'))
    return
  }
  saving.value = true
  try {
    const body = { name: f.name.trim(), port, scheme: f.scheme, domain: f.domain.trim() }
    if (f.id) {
      const res = await api.put(`/svcmon/ports/${f.id}`, body)
      if (res.port) replacePort(res.port)
    } else {
      await api.post(`/svcmon/servers/${f.server_id}/ports`, body)
    }
    pDlg.value = false
    ElMessage.success(f.id ? t('common.saved') : t('svcmon.added'))
    load(false)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function removePort(p) {
  const label = p.name || t('svcmon.portN', { n: p.port })
  try {
    await ElMessageBox.confirm(t('svcmon.delPortConfirm', { name: label }), t('common.delete'), {
      type: 'warning', confirmButtonText: t('common.delete'), cancelButtonText: t('common.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/svcmon/ports/${p.id}`)
    dropPortLocal(p.id)
    ElMessage.success(t('common.deleted'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

function startPoll() {
  if (pollTimer) return
  pollTimer = setInterval(() => {
    if (!document.hidden && !sDlg.value && !pDlg.value) load(false)
  }, 15_000)
}
function stopPoll() {
  clearInterval(pollTimer)
  pollTimer = null
}

onMounted(() => {
  load(false) // 447 回归修复：原只启 15s 轮询定时器，首屏空到首个 tick；立即拉一次
  loadExtras()
  startPoll()
})
// keep-alive 下首挂载 onActivated 也会触发：标记跳过首刷双请求
let svcMountedJustNow = true
onActivated(() => {
  if (svcMountedJustNow) {
    svcMountedJustNow = false
  } else {
    load(false)
    loadExtras()
  }
  startPoll()
})
onDeactivated(stopPoll)
onUnmounted(stopPoll)
</script>

<style scoped>
.svcmon {
  display: flex;
  flex-direction: column;
  gap: 12px;
  /* .main 全局边距仅 6px（历史刻意收窄），本页 tab 标签与各 tab 内容整体贴边
     （29 用户报「文字太靠近左边」）——自加水平内边距 */
  padding: 0 12px;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}
.tools {
  display: flex;
  align-items: center;
  gap: 12px;
}
.updated {
  color: var(--muted);
  font-size: 12px;
}

/* 服务器卡片：自适应网格，手机端自然落成单列 */
.s-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 12px;
}
.s-card {
  border-radius: 10px;
}
.s-card :deep(.el-card__body) {
  padding: 14px 16px 12px;
}
.s-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.s-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.s-dot {
  flex: none;
  width: 9px;
  height: 9px;
  border-radius: 50%;
}
.s-dot.ok {
  background: var(--el-color-success, #67c23a);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--el-color-success, #67c23a) 18%, transparent);
}
.s-dot.bad {
  background: var(--crit, #f56c6c);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--crit, #f56c6c) 18%, transparent);
}
.s-dot.unknown {
  background: var(--muted, #909399);
}
.s-name {
  font-size: 15px;
  font-weight: 700;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.s-host {
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 12.5px;
  color: var(--muted);
  margin-top: 6px;
  word-break: break-all;
}

/* 端口行 */
.p-list {
  margin-top: 8px;
  border-top: 1px dashed var(--border, #ebeef5);
}
.p-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 8px 0;
  border-bottom: 1px dashed var(--border, #ebeef5);
}
.p-row:last-child {
  border-bottom: none;
}
.p-dot {
  margin-top: 5px;
}
.p-main {
  flex: 1;
  min-width: 0;
}
.p-line {
  display: flex;
  align-items: baseline;
  gap: 6px;
  flex-wrap: wrap;
}
.p-scheme {
  flex: none;
  font-size: 10px;
  font-weight: 700;
  line-height: 1;
  padding: 2px 4px;
  border-radius: 4px;
  color: #2563eb;
  background: rgba(37, 99, 235, 0.12);
}
.p-name {
  font-size: 13.5px;
  font-weight: 600;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.p-num {
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 12px;
  color: var(--muted);
}
.p-latency {
  font-size: 12px;
  color: var(--text-2);
  font-variant-numeric: tabular-nums;
}
.p-state {
  font-size: 12px;
}
.p-state.ok {
  color: var(--el-color-success, #67c23a);
}
.p-state.bad {
  color: var(--crit, #f56c6c);
}
.p-state.unknown {
  color: var(--muted);
}
.p-meta {
  color: var(--muted);
  font-size: 12px;
  margin-top: 2px;
}
.p-err {
  color: var(--crit, #f56c6c);
  font-size: 12px;
  line-height: 1.6;
  margin-top: 2px;
  word-break: break-all;
}
.p-acts {
  display: flex;
  align-items: center;
  flex: none;
}
.p-avail {
  font-variant-numeric: tabular-nums;
}
.p-avail.warn {
  color: var(--el-color-warning, #e6a23c);
}
.p-avail.bad {
  color: var(--crit, #f56c6c);
}
.p-none {
  color: var(--muted);
  font-size: 12.5px;
  padding: 10px 0 2px;
}

/* 最近事件（500） */
.e-card {
  border-radius: 10px;
}
.e-card :deep(.el-card__body) {
  padding: 6px 16px 10px;
}
.e-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
}
.e-title {
  font-weight: 700;
  font-size: 14px;
}
.e-sub {
  color: var(--muted);
  font-size: 12px;
}
.e-row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 7px 0;
  border-bottom: 1px dashed var(--border, #ebeef5);
  font-size: 12.5px;
  flex-wrap: wrap;
}
.e-row:last-child {
  border-bottom: none;
}
.e-dot {
  align-self: center;
}
.e-time {
  color: var(--muted);
  font-variant-numeric: tabular-nums;
  flex: none;
}
.e-name {
  font-weight: 600;
}
.e-gap {
  color: var(--muted);
}
.e-err {
  color: var(--crit, #f56c6c);
  word-break: break-all;
}

.s-foot {
  display: flex;
  align-items: center;
  gap: 8px;
  border-top: 1px dashed var(--border, #ebeef5);
  margin-top: 10px;
  padding-top: 10px;
}
.s-flex {
  flex: 1;
}
</style>
