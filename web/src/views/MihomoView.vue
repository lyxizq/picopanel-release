<template>
  <div class="mihomo">
    <!-- 未配置引导态（空态业务文案，不摆裸 0） -->
    <el-empty v-if="inited && !cfg.ready" :description="$t('mihomo.notConfigured')">
      <el-button type="primary" @click="openCfg">{{ $t('mihomo.openSettings') }}</el-button>
    </el-empty>

    <template v-else-if="cfg.ready">
      <div class="toolbar">
        <span class="updated">{{ $t('mihomo.via', { server: cfg.server_name || $t('mihomo.execFallback'), url: cfg.base_url }) }}</span>
        <div class="tools">
          <el-button :loading="refreshing" @click="refreshAll">
            <el-icon v-if="!refreshing"><Refresh /></el-icon>
            <span v-if="!refreshing">{{ $t('common.refresh') }}</span>
          </el-button>
          <el-button @click="openCfg">
            <el-icon><Setting /></el-icon>
            <span>{{ $t('mihomo.settings') }}</span>
          </el-button>
        </div>
      </div>

      <!-- 概览条 -->
      <div class="mh-ov">
        <div class="mh-ov-item">
          <span class="mh-k">{{ $t('mihomo.mode') }}</span>
          <el-dropdown trigger="click" @command="setMode">
            <button type="button" class="mh-mode">
              {{ modeLabel(overview?.mode) }}<el-icon><ArrowDown /></el-icon>
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item v-for="m in ['rule', 'global', 'direct']" :key="m" :command="m" :disabled="m === overview?.mode">
                  {{ modeLabel(m) }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
        <div class="mh-ov-item"><span class="mh-k">{{ $t('mihomo.version') }}</span><span class="mh-v">{{ version || '—' }}</span></div>
        <div class="mh-ov-item"><span class="mh-k">{{ $t('mihomo.activeConns') }}</span><span class="mh-v">{{ conn ? conn.total : '—' }}</span></div>
        <div class="mh-ov-item">
          <span class="mh-k">{{ $t('mihomo.rate') }}</span>
          <span class="mh-v">↓ {{ downRate ?? '—' }} · ↑ {{ upRate ?? '—' }}</span>
        </div>
        <div class="mh-ov-item">
          <span class="mh-k">{{ $t('mihomo.total') }}</span>
          <span class="mh-v">↓ {{ conn ? fmtBytes(conn.download_total) : '—' }} · ↑ {{ conn ? fmtBytes(conn.upload_total) : '—' }}</span>
        </div>
        <div v-if="conn && conn.memory > 0" class="mh-ov-item"><span class="mh-k">{{ $t('mihomo.memory') }}</span><span class="mh-v">{{ fmtBytes(conn.memory) }}</span></div>
        <div class="mh-ov-item">
          <el-button size="small" :loading="opBusy.reload" @click="reloadCfg">{{ $t('mihomo.reload') }}</el-button>
        </div>
      </div>

      <!-- 代理组 -->
      <div class="mh-sec">
        <div class="mh-sec-head">
          <span>{{ $t('mihomo.groups') }}<span class="mh-count">{{ $t('mihomo.groupsN', { n: proxies ? proxies.groups.length : 0 }) }}</span></span>
          <el-button size="small" :loading="opBusy.proxies" @click="refreshProxies">{{ $t('mihomo.refreshNodes') }}</el-button>
        </div>
        <div v-for="g in proxies?.groups || []" :key="g.name" class="mh-group">
          <div class="mh-g-head">
            <button
              type="button" class="mh-g-toggle"
              :title="$t('mihomo.groupToggleTip', { name: g.name, action: isCollapsed(g.name) ? $t('mihomo.expand') : $t('mihomo.collapse') })"
              @click="toggleGroup(g.name)"
            >
              <el-icon class="mh-caret" :class="{ open: !isCollapsed(g.name) }"><CaretRight /></el-icon>
              <span class="mh-g-name" :title="g.name">{{ g.name }}</span>
              <el-tag size="small" effect="plain">{{ g.type }}</el-tag>
              <el-tag size="small" type="success" effect="plain" class="mh-g-now" :title="g.now">{{ $t('mihomo.current', { now: g.now }) }}</el-tag>
              <span class="mh-g-count">{{ $t('mihomo.nodesN', { n: g.all.length }) }}</span>
            </button>
            <el-button size="small" plain :loading="opBusy['gd:' + g.name]" @click="groupDelay(g)">{{ $t('mihomo.groupDelay') }}</el-button>
          </div>
          <div v-if="!isCollapsed(g.name)" class="mh-nodes">
            <button
              v-for="n in g.all" :key="g.name + '/' + n" type="button"
              class="mh-node" :class="{ active: n === g.now }"
              :title="$t('mihomo.switchTip', { name: n })"
              @click="switchNode(g, n)"
            >
              <span class="mh-node-name">{{ n }}</span>
              <span
                class="mh-node-delay" :class="delayCls(delayOf(n))"
                :title="$t('mihomo.delayTip', { name: n })"
                @click.stop="nodeDelay(n)"
              >{{ delayText(delayOf(n)) }}</span>
            </button>
          </div>
        </div>
        <el-empty v-if="proxies && !proxies.groups.length" :description="$t('mihomo.noGroups')" :image-size="72" />
      </div>

      <!-- 连接 -->
      <div class="mh-sec">
        <div class="mh-sec-head">
          <span>{{ $t('mihomo.conns') }}<span class="mh-count">{{ $t('mihomo.connsN', { n: conn ? conn.total : 0 }) }}</span></span>
          <el-button size="small" type="danger" plain :loading="opBusy.close_all" @click="closeAll">{{ $t('mihomo.closeAll') }}</el-button>
        </div>
        <div v-if="conn && conn.connections.length" class="mh-table-wrap">
          <el-table :data="conn.connections" size="small" class="mh-table">
            <el-table-column :label="$t('mihomo.colHost')" min-width="180">
              <template #default="{ row }">
                <div class="mh-host">
                  <span class="mh-host-name">{{ row.host }}<i v-if="row.network === 'udp'" class="mh-udp">UDP</i></span>
                  <span v-if="row.process || row.source" class="mh-host-sub">{{ row.process || row.source }}</span>
                </div>
              </template>
            </el-table-column>
            <el-table-column :label="$t('mihomo.colRule')" min-width="120" show-overflow-tooltip>
              <template #default="{ row }">{{ row.rule || '—' }}</template>
            </el-table-column>
            <el-table-column :label="$t('mihomo.colChain')" min-width="150" show-overflow-tooltip>
              <template #default="{ row }">{{ (row.chains || []).join(' → ') || '—' }}</template>
            </el-table-column>
            <el-table-column :label="$t('mihomo.colDown')" width="90" align="right">
              <template #default="{ row }">{{ fmtBytes(row.download) }}</template>
            </el-table-column>
            <el-table-column :label="$t('mihomo.colUp')" width="90" align="right">
              <template #default="{ row }">{{ fmtBytes(row.upload) }}</template>
            </el-table-column>
            <el-table-column :label="$t('mihomo.colAge')" width="80" align="right">
              <template #default="{ row }">{{ connAge(row.start) }}</template>
            </el-table-column>
            <el-table-column label="" width="56" align="center">
              <template #default="{ row }">
                <el-button size="small" type="danger" plain link @click="closeConn(row)">{{ $t('mihomo.close') }}</el-button>
              </template>
            </el-table-column>
          </el-table>
          <div v-if="conn.total > conn.connections.length" class="mh-truncated">{{ $t('mihomo.connsTruncated', { shown: conn.connections.length, total: conn.total }) }}</div>
        </div>
        <el-empty v-else :description="$t('mihomo.noConns')" :image-size="72" />
      </div>

      <!-- 规则 -->
      <div class="mh-sec">
        <div class="mh-sec-head">
          <span>{{ $t('mihomo.rules') }}<span class="mh-count">{{ $t('mihomo.rulesN', { n: rules ? rules.total : 0 }) }}</span></span>
          <div class="mh-rule-tools">
            <el-input v-model="ruleKw" size="small" :placeholder="$t('mihomo.searchRules')" clearable style="width: 180px" />
            <el-button size="small" :loading="opBusy.rules" @click="refreshRules">{{ $t('common.refresh') }}</el-button>
          </div>
        </div>
        <div v-if="filteredRules.length" class="mh-table-wrap">
          <el-table :data="filteredRules" size="small" class="mh-table">
            <el-table-column prop="type" :label="$t('mihomo.colType')" width="150" />
            <el-table-column prop="payload" :label="$t('mihomo.colPayload')" min-width="220" show-overflow-tooltip />
            <el-table-column prop="proxy" :label="$t('mihomo.colProxy')" min-width="120" show-overflow-tooltip />
          </el-table>
          <div v-if="rules.total > rules.rules.length || filteredRules.length < matchRules.length" class="mh-truncated">
            {{ $t('mihomo.rulesTruncated', { shown: filteredRules.length, hit: matchRules.length, total: rules.total }) }}
          </div>
        </div>
        <el-empty v-else-if="rules" :description="ruleKw ? $t('mihomo.noMatchRules') : $t('mihomo.noRules')" :image-size="72" />
        <div v-else class="mh-rule-load">
          <el-button size="small" plain :loading="opBusy.rules" @click="refreshRules">{{ $t('mihomo.loadRules') }}</el-button>
        </div>
      </div>
    </template>

    <!-- 设置弹层（append-to-body 遵 463 口径） -->
    <el-dialog v-model="cfgDlg" :title="$t('mihomo.cfgTitle')" width="min(520px, 94vw)" append-to-body :close-on-click-modal="false">
      <el-form label-width="96px">
        <el-form-item :label="$t('mihomo.cfgServer')">
          <el-select v-model="cf.server_id" :placeholder="$t('mihomo.cfgServerPh')" style="width: 100%">
            <el-option v-for="s in pushServers" :key="s.id" :label="s.name || s.host" :value="s.id" />
          </el-select>
          <div class="mh-hint">{{ $t('mihomo.cfgServerHint') }}</div>
        </el-form-item>
        <el-form-item :label="$t('mihomo.cfgUrl')">
          <el-input v-model="cf.base_url" placeholder="http://127.0.0.1:9090" />
          <div class="mh-hint">{{ $t('mihomo.cfgUrlHint') }}</div>
        </el-form-item>
        <el-form-item label="Secret">
          <el-input v-model="cf.secret" type="password" show-password :placeholder="$t('mihomo.cfgSecretPh')" />
        </el-form-item>
        <el-form-item :label="$t('mihomo.cfgTestUrl')">
          <el-input v-model="cf.test_url" :placeholder="$t('mihomo.cfgTestUrlPh')" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button :loading="testing" @click="testConn">{{ $t('mihomo.saveAndTest') }}</el-button>
        <el-button type="primary" :loading="saving" @click="saveCfg">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// mihomo 控制台（TODO 7，服务监控页第 4 个 tab）：经 net_tasks 中转读写
// mihomo external-controller（功能面参考 metacubexd 裁剪：概览/代理组/连接/
// 规则 + 切节点/测延迟/断连接/切模式/重载；WS 实时流中转通道做不了，速率用
// 连接总量 5s 差分近似）。读面板按 ref_key 服务端去重，轮询复用活跃任务。
defineOptions({ name: 'MihomoView' })
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, CaretRight, Refresh, Setting } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { fmtBytes, fmtBps, fmtUptime } from '../utils/format.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/svctabs.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

const route = useRoute()

const inited = ref(false)
const cfg = ref({ ready: false, server_id: null, server_name: '', base_url: '', test_url: '', has_secret: false })
const pushServers = ref([])
const cfgDlg = ref(false)
const cf = ref({ server_id: null, base_url: '', secret: '', test_url: '' })
const saving = ref(false)
const testing = ref(false)

const overview = ref(null)
const version = ref('')
const proxies = ref(null)
const conn = ref(null)
const rules = ref(null)
const ruleKw = ref('')
const extraDelays = ref({}) // 组测/单点测延迟覆盖（node name → ms）
const refreshing = ref(false)
const opBusy = ref({}) // 切换/测延迟等按钮 loading：key = 动作标识

// 速率差分（连接总量两次快照）
let lastTotals = null
const downRate = ref(null)
const upRate = ref(null)

const MODE_KEYS = { rule: 'mihomo.modeRule', global: 'mihomo.modeGlobal', direct: 'mihomo.modeDirect' }
const modeLabel = (m) => (MODE_KEYS[m] ? t(MODE_KEYS[m]) : m || '—')

function isActive() {
  return route.query.tab === 'mihomo' && !document.hidden
}

// ---------- 任务收发 ----------

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function pollTask(id) {
  for (;;) {
    await sleep(1500)
    const r = await api.get(`/mihomo/task/${id}`)
    if (r.running) continue
    if (r.error) throw new Error(r.error)
    return r.data
  }
}

async function fireQuery(op) {
  const r = await api.post('/mihomo/query', { op })
  return await pollTask(r.task_id)
}

async function fireOp(payload, busyKey) {
  if (busyKey) opBusy.value[busyKey] = true
  try {
    const r = await api.post('/mihomo/op', payload)
    return await pollTask(r.task_id)
  } finally {
    if (busyKey) opBusy.value[busyKey] = false
  }
}

// ---------- 各读面板 ----------

const inflight = new Set()

async function guarded(key, fn) {
  if (inflight.has(key)) return
  inflight.add(key)
  try {
    await fn()
  } catch {
    /* 面板刷新失败静默（轮询下一轮自然重试） */
  } finally {
    inflight.delete(key)
  }
}

async function refreshOverview() {
  await guarded('overview', async () => {
    overview.value = await fireQuery('overview')
  })
}

async function refreshConnections() {
  await guarded('connections', async () => {
    const data = await fireQuery('connections')
    const now = Date.now()
    if (lastTotals && data && conn.value) {
      const dt = (now - lastTotals.at) / 1000
      if (dt > 0) {
        downRate.value = fmtBps(Math.max(0, (data.download_total - lastTotals.down) / dt))
        upRate.value = fmtBps(Math.max(0, (data.upload_total - lastTotals.up) / dt))
      }
    }
    lastTotals = { down: data.download_total, up: data.upload_total, at: now }
    conn.value = data
  })
}

async function refreshProxies() {
  await guarded('proxies', async () => {
    proxies.value = await fireQuery('proxies')
    extraDelays.value = {}
  })
}

async function refreshRules() {
  await guarded('rules', async () => {
    rules.value = await fireQuery('rules')
  })
}

async function refreshVersion() {
  await guarded('version', async () => {
    version.value = (await fireQuery('version')).version || ''
  })
}

async function refreshAll() {
  refreshing.value = true
  await Promise.allSettled([refreshVersion(), refreshOverview(), refreshProxies(), refreshConnections(), rules.value ? refreshRules() : null])
  refreshing.value = false
}

// 5s 轮询调度：仅 tab 激活且页面可见时转；进行中的面自动跳过
let pollTimer = null
function tick() {
  if (!cfg.value.ready || !isActive()) return
  refreshOverview()
  refreshConnections()
}

function startPoll() {
  if (pollTimer) return
  pollTimer = setInterval(tick, 5000)
}
function stopPoll() {
  clearInterval(pollTimer)
  pollTimer = null
}

// ---------- 写操作 ----------

// ---------- 代理组折叠（TODO 3）：expanded 集合语义，默认空=全部折叠；刷新
// 不重置用户已展开的组（组名稳定，新组不在集合里天然折叠）
const expandedGroups = ref(new Set())

function isCollapsed(name) {
  return !expandedGroups.value.has(name)
}
function toggleGroup(name) {
  const next = new Set(expandedGroups.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  expandedGroups.value = next
}

async function switchNode(g, name) {
  if (name === g.now) return
  try {
    await fireOp({ action: 'switch', group: g.name, name }, 'sw:' + g.name)
    g.now = name
    ElMessage.success(t('mihomo.switched', { group: g.name, node: name }))
    refreshOverview()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

function delayOf(name) {
  const v = extraDelays.value[name]
  if (v !== undefined) return v
  return proxies.value?.nodes?.[name]?.delay ?? -1
}
function delayCls(d) {
  if (d > 0 && d < 200) return 'good'
  if (d >= 200 && d < 500) return 'mid'
  if (d >= 500) return 'bad'
  return 'na'
}
function delayText(d) {
  if (d > 0) return `${d}ms`
  return d === 0 ? t('mihomo.timeout') : t('mihomo.untested')
}

async function nodeDelay(name) {
  try {
    const data = await fireOp({ action: 'delay', name }, 'nd:' + name)
    extraDelays.value = { ...extraDelays.value, [name]: data.delay }
  } catch (e) {
    extraDelays.value = { ...extraDelays.value, [name]: 0 }
    ElMessage.warning(`${name}：${e.message}`)
  }
}

async function groupDelay(g) {
  try {
    const data = await fireOp({ action: 'group_delay', group: g.name }, 'gd:' + g.name)
    extraDelays.value = { ...extraDelays.value, ...data.delays }
    if (!expandedGroups.value.has(g.name)) toggleGroup(g.name) // 折叠态测完自动展开，结果可见
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function setMode(mode) {
  try {
    await fireOp({ action: 'mode', mode }, 'mode')
    ElMessage.success(t('mihomo.modeSwitched', { mode: modeLabel(mode) }))
    refreshOverview()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function reloadCfg() {
  try {
    await ElMessageBox.confirm(t('mihomo.reloadConfirm'), t('mihomo.reload'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await fireOp({ action: 'reload' }, 'reload')
    ElMessage.success(t('mihomo.reloaded'))
    await Promise.allSettled([refreshOverview(), refreshProxies()])
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function closeConn(row) {
  try {
    await fireOp({ action: 'close', id: row.id })
    conn.value = { ...conn.value, total: Math.max(0, conn.value.total - 1), connections: conn.value.connections.filter((x) => x.id !== row.id) }
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function closeAll() {
  try {
    await ElMessageBox.confirm(t('mihomo.closeAllConfirm', { n: conn.value?.total ?? '' }), t('mihomo.closeAll'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await fireOp({ action: 'close_all' }, 'close_all')
    ElMessage.success(t('mihomo.closedAll'))
    refreshConnections()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---------- 设置 ----------

async function loadServers() {
  try {
    const res = await api.get('/servers')
    pushServers.value = (res.servers || []).filter((x) => x && x.collect_mode === 'push')
  } catch { /* 机器清单加载失败不阻塞 */ }
}

function openCfg() {
  cf.value = { server_id: cfg.value.server_id || null, base_url: cfg.value.base_url || '', secret: '', test_url: cfg.value.test_url || '' }
  cfgDlg.value = true
}

async function saveCfg() {
  if (!cf.value.server_id) {
    ElMessage.warning(t('mihomo.needServer'))
    return
  }
  if (!cf.value.base_url.trim()) {
    ElMessage.warning(t('mihomo.needUrl'))
    return
  }
  saving.value = true
  try {
    cfg.value = await api.put('/mihomo/config', { ...cf.value })
    cfgDlg.value = false
    ElMessage.success(t('common.saved'))
    if (cfg.value.ready) {
      lastTotals = null
      downRate.value = upRate.value = null
      startPoll()
      refreshAll()
    }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function testConn() {
  testing.value = true
  try {
    await saveCfgInner()
    const data = await fireQuery('version')
    ElMessage.success(t('mihomo.connected', { v: data.version || '' }))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testing.value = false
  }
}

async function saveCfgInner() {
  if (!cf.value.server_id) throw new Error(t('mihomo.needServer'))
  if (!cf.value.base_url.trim()) throw new Error(t('mihomo.needUrl'))
  cfg.value = await api.put('/mihomo/config', { ...cf.value })
  cfgDlg.value = false
  if (cfg.value.ready) {
    lastTotals = null
    startPoll()
  }
}

// ---------- 展示辅助 ----------

const matchRules = computed(() => {
  if (!rules.value) return []
  const kw = ruleKw.value.trim().toLowerCase()
  if (!kw) return rules.value.rules
  return rules.value.rules.filter((r) => `${r.type} ${r.payload} ${r.proxy}`.toLowerCase().includes(kw))
})
const filteredRules = computed(() => matchRules.value.slice(0, 200))

function connAge(start) {
  const ms = Date.now() - new Date(start).getTime()
  if (!(ms > 0)) return '—'
  return fmtUptime(Math.floor(ms / 1000))
}

// ---------- 生命周期 ----------

onMounted(async () => {
  loadServers()
  try {
    cfg.value = await api.get('/mihomo/config')
  } catch { /* 未配置态照常渲染 */ }
  inited.value = true
  if (cfg.value.ready) {
    startPoll()
    refreshAll()
  }
})

watch(
  () => route.query.tab,
  (v) => {
    if (v === 'mihomo' && cfg.value.ready && !pollTimer) {
      startPoll()
      tick()
    }
  }
)
const onVis = () => { if (!isActive()) stopPoll(); else if (cfg.value.ready) { startPoll(); tick() } }
document.addEventListener('visibilitychange', onVis)
onBeforeUnmount(() => {
  stopPoll()
  document.removeEventListener('visibilitychange', onVis)
})
</script>

<style scoped>
.mihomo {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.updated {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.tools {
  display: flex;
  gap: 8px;
  align-items: center;
}

/* 概览条 */
.mh-ov {
  display: flex;
  align-items: center;
  gap: 10px 22px;
  flex-wrap: wrap;
  padding: 10px 14px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 10px;
}
.mh-ov-item {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13.5px;
}
.mh-k {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.mh-v {
  font-variant-numeric: tabular-nums;
}
.mh-mode {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  border: 1px solid var(--el-border-color);
  background: transparent;
  border-radius: 6px;
  padding: 2px 8px;
  font-size: 13px;
  cursor: pointer;
  color: var(--el-text-color-primary);
}
.mh-mode:hover {
  border-color: var(--el-color-primary);
  color: var(--el-color-primary);
}

/* 分区 */
.mh-sec {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.mh-sec-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 14.5px;
  font-weight: 600;
}
.mh-count {
  margin-left: 8px;
  font-size: 12px;
  font-weight: 400;
  color: var(--el-text-color-secondary);
}
.mh-rule-tools {
  display: flex;
  gap: 8px;
  align-items: center;
}
.mh-rule-load {
  display: flex;
}

/* 代理组 */
.mh-group {
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 10px;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.mh-g-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.mh-g-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  min-width: 0;
  background: transparent;
  border: none;
  padding: 2px 0;
  cursor: pointer;
  color: var(--el-text-color-primary);
  font-size: inherit;
  text-align: left;
}
.mh-caret {
  flex-shrink: 0;
  transition: transform 0.15s;
  color: var(--el-text-color-secondary);
}
.mh-caret.open {
  transform: rotate(90deg);
}
.mh-g-count {
  flex-shrink: 0;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.mh-g-name {
  font-weight: 600;
  font-size: 14.5px;
  max-width: 40%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mh-g-now {
  max-width: 34%;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mh-nodes {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(190px, 1fr));
  gap: 6px;
}
.mh-node {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  border: 1px solid var(--el-border-color-lighter);
  background: transparent;
  border-radius: 8px;
  padding: 5px 9px;
  font-size: 13px;
  cursor: pointer;
  color: var(--el-text-color-primary);
  text-align: left;
}
.mh-node:hover {
  border-color: var(--el-color-primary);
}
.mh-node.active {
  border-color: var(--el-color-primary);
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}
.mh-node-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mh-node-delay {
  flex-shrink: 0;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.mh-node-delay.good { color: var(--el-color-success); }
.mh-node-delay.mid { color: var(--el-color-warning); }
.mh-node-delay.bad { color: var(--el-color-danger); }
.mh-node-delay.na { color: var(--el-text-color-secondary); }

/* 连接/规则表 */
.mh-table-wrap {
  overflow-x: auto;
}
.mh-table {
  width: 100%;
}
.mh-host {
  display: flex;
  flex-direction: column;
  line-height: 1.35;
}
.mh-host-name {
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mh-host-sub {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mh-udp {
  font-style: normal;
  font-size: 10px;
  margin-left: 5px;
  border: 1px solid var(--el-border-color);
  border-radius: 4px;
  padding: 0 3px;
  color: var(--el-text-color-secondary);
  vertical-align: 1px;
}
.mh-truncated {
  margin-top: 6px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.mh-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.5;
  margin-top: 2px;
  width: 100%;
}

@media (max-width: 640px) {
  .mh-nodes {
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  }
  .mh-g-name {
    max-width: 100%;
  }
}
</style>
