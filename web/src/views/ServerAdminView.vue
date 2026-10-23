<template>
  <div>
    <el-card shadow="never">
      <template #header>
        <div class="head">
          <span>服务器管理</span>
          <div class="head-btns">
            <el-button :icon="FolderOpened" @click="openGroups">分组管理</el-button>
            <el-button :icon="Top" :loading="upgradingAll" @click="upgradeAgentsAll">升级全部 Agent</el-button>
            <el-button type="primary" :icon="Plus" @click="openCreate">新建服务器</el-button>
          </div>
        </div>
      </template>

      <!-- 按分组分节（同监控页口径）：分组拼音序、未分组最后、分组色表头可折叠；
           无任何分组时平铺一张表不套分节框 -->
      <div v-for="(sec, gi) in sections" :key="sec.key" :class="sec.flat ? 'grp-flat' : 'grp'">
        <div v-if="!sec.flat" class="grp-head" :style="{ background: headBg(grpColor(gi)) }" @click="toggle(sec.key)">
          <span class="grp-accent" :style="{ background: grpColor(gi) }" />
          <span class="grp-name" :style="{ color: grpColor(gi) }">{{ sec.name }}</span>
          <span class="grp-stats">
            <span>共 {{ sec.servers.length }} 台</span>
            <span v-if="disabledOf(sec)" class="muted">停用 {{ disabledOf(sec) }}</span>
          </span>
          <el-icon class="grp-arrow" :class="{ open: isOpen(sec.key) }"><ArrowDown /></el-icon>
        </div>
        <div v-show="sec.flat || isOpen(sec.key)" :class="sec.flat ? '' : 'grp-body'" v-loading="loading">
          <!-- 与监控页服务器列表同款行列表（ServerRows manage 模式追加操作列） -->
          <ServerRows
            :servers="sec.servers"
            manage
            :upgradable-ids="upgradableIds"
            @open="openServerDetail"
            @edit="openEdit"
            @toggle="toggleEnabled"
            @remove="remove"
            @upgrade="upgradeAgent"
          />
        </div>
      </div>
    </el-card>

    <!-- 新建 -->
    <el-dialog append-to-body v-model="createVisible" title="新建服务器" width="480px" :before-close="createGuard.beforeClose">
      <el-form @submit.prevent="create">
        <el-form-item label="采集方式" label-width="100px">
          <el-radio-group v-model="newForm.mode">
            <el-radio value="agent">Agent 拉取</el-radio>
            <el-radio value="push">Agent 上报</el-radio>
            <el-radio value="ssh" disabled>SSH 直连（已下线）</el-radio>
            <el-radio value="node">node_exporter</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="名称" label-width="100px">
          <el-input v-model="newForm.name" placeholder="给这台服务器起个名字，如 web-01" @keyup.enter="create" />
        </el-form-item>
        <el-form-item label="备注" label-width="100px">
          <el-input v-model="newForm.remark" placeholder="可选；用途/位置等，悬停服务器名时显示" maxlength="500" />
        </el-form-item>
        <el-form-item v-if="newForm.mode !== 'push'" :label="newForm.mode === 'node' ? 'Exporter 地址' : 'Agent 地址'" label-width="100px">
          <el-input v-model="newForm.agent_addr" :placeholder="newForm.mode === 'node'
            ? 'node_exporter 的 host:port，如 192.168.1.5:10200（缺省自动补 :10200）'
            : 'agent 所在机器的 host:port，如 192.168.1.5:19100'" />
          <div class="iface-hint" v-if="newForm.mode === 'node'">
            目标机只需运行 node_exporter；服务端周期拉取 /metrics（指标与本系统 agent 同口径，无监听端口）
          </div>
          <div class="iface-hint" v-else>服务端主动抓取该地址的 /metrics；缺省端口自动补 :19100</div>
        </el-form-item>
                <el-form-item label-width="0">
          <div class="iface-hint">
            需要「终端」页 Web SSH/SFTP 的，创建后在编辑弹窗里补配该机的 SSH 信息（任意采集模式均可）
          </div>
        </el-form-item>
        <el-form-item v-if="newForm.mode === 'agent' || newForm.mode === 'node'" label="访问密钥" label-width="100px">
          <div class="token-row">
            <el-input
              v-model="newForm.token"
              :placeholder="newForm.mode === 'node'
                ? '可选；经 X-Server-Token 发送，供前置反代校验'
                : '可选；需与 agent.yaml 中 token 一致'"
            />
            <el-button @click="newForm.token = genToken()">生成</el-button>
          </div>
          <div class="iface-hint">
            {{ newForm.mode === 'node'
              ? 'node_exporter 本体不校验密钥；用 Nginx 等反代加认证时填写'
              : '密钥可多台服务器/agent 共用' }}
          </div>
        </el-form-item>
        <el-form-item v-if="newForm.mode === 'push'" label="上报密钥" label-width="100px">
          <div class="token-row">
            <el-input v-model="newForm.token" placeholder="留空自动生成；也可手动填入或点「生成」" />
            <el-button @click="newForm.token = genToken()">生成</el-button>
          </div>
          <div class="iface-hint">
            agent 主动把指标 POST 上报服务端（适合 agent 在内网/NAT 后、服务端无法主动访问的场景），无需开端口；
            上报密钥与服务器一对一，请勿多台共用（每台机器分别创建并各生成一个密钥）。
          </div>
        </el-form-item>
        <el-form-item label="分组" label-width="100px">
          <el-select v-model="newForm.group_id">
            <el-option label="未分组" :value="0" />
            <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id" />
          </el-select>
          <div class="iface-hint">在「分组管理」里可新建分组；总览页按分组手风琴展示</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="create">创建</el-button>
      </template>
    </el-dialog>

    <!-- push 创建成功：上报密钥仅此一次展示（接口不回显明文，忘了只能换新） -->
    <el-dialog append-to-body v-model="tokenVisible" title="上报密钥（仅此一次展示）" width="460px" :close-on-click-modal="false">
      <div class="once-tip">请立即复制并填入该机 agent.yaml —— 关闭后不再显示，遗失只能「生成新密钥」。</div>
      <div class="token-show">
        <code>{{ onceToken }}</code>
        <el-button size="small" type="primary" plain @click="copyToken">复制</el-button>
      </div>
    </el-dialog>

    <!-- 编辑（与总览页共用的弹窗组件） -->
    <ServerEditDialog ref="editDlg" @saved="load" />

    <!-- 分组管理 -->
    <el-dialog append-to-body v-model="groupsVisible" title="分组管理" width="560px">
      <div class="grp-add">
        <el-input
          v-model="newGroupName"
          placeholder="新分组名称，如：生产环境 / 办公机房"
          maxlength="50"
          @keyup.enter="addGroup"
        />
        <el-button type="primary" @click="addGroup">添加</el-button>
      </div>
      <el-table :data="groups" empty-text="还没有分组；总览页没有分组时平铺展示服务器">
        <el-table-column prop="name" label="名称" min-width="150" />
        <el-table-column label="服务器数" width="90" align="center">
          <template #default="{ row }">{{ groupCount(row.id) }} 台</template>
        </el-table-column>
        <el-table-column label="操作" width="140" align="center">
          <template #default="{ row }">
            <el-button size="small" text type="primary" @click="renameGroup(row)">重命名</el-button>
            <el-button size="small" text type="danger" @click="removeGroup(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="iface-hint" style="margin-top: 8px">
        删除分组后，组内服务器自动移到「未分组」，服务器本身与历史数据不受影响。
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
defineOptions({ name: 'ServerAdminView' })
import { computed, nextTick, onActivated, onMounted, ref, watch } from 'vue'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, FolderOpened, Plus, Top } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { openServerDetail } from '../serverDetail.js'
import { genToken } from '../utils/token.js'
import { grpColor } from '../utils/chartTheme.js'
import ServerEditDialog from '../components/ServerEditDialog.vue'
import ServerRows from '../components/ServerRows.vue'

const servers = ref([])
const loading = ref(false)

// 分组（本页与总览页都按分节展示；0=未分组）
const groups = ref([])
const groupsVisible = ref(false)
const newGroupName = ref('')

// 分节展示（同监控页口径）：组内服务器按名称拼音序，分组按名称拼音序、
// 未分组固定最后（空分组也展示便于规划）；一个分组都没有时平铺一节。
const sections = computed(() => {
  const sorted = [...servers.value].sort((a, b) =>
    a.name.localeCompare(b.name, 'zh-Hans-CN', { numeric: true, sensitivity: 'base' }))
  if (!groups.value.length) return [{ key: 'all', name: '', flat: true, servers: sorted }]
  const secs = groups.value
    .map((g) => ({ key: 'g' + g.id, id: g.id, name: g.name, servers: [] }))
    .sort((a, b) => a.name.localeCompare(b.name, 'zh-Hans-CN'))
  const byId = new Map(secs.map((s) => [s.id, s]))
  const ungrouped = { key: 'g0', id: 0, name: '未分组', servers: [] }
  for (const s of sorted) {
    const sec = byId.get(s.group_id)
    ;(sec || ungrouped).servers.push(s)
  }
  if (ungrouped.servers.length) secs.push(ungrouped)
  return secs
})

// 分节折叠：默认全部展开，分组集合变化时新组默认展开
const openMap = ref({})
watch(
  () => sections.value.map((s) => s.key).join('|'),
  () => {
    const m = {}
    for (const s of sections.value) m[s.key] = true
    openMap.value = m
  },
  { immediate: true },
)
function isOpen(key) {
  return openMap.value[key] !== false
}
function toggle(key) {
  openMap.value[key] = !isOpen(key)
}

// 表头底色：分组色的低透明度浅底（明暗主题都成立）
function headBg(hex) {
  const n = parseInt(hex.slice(1), 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, 0.12)`
}

// 分节表头统计（415：管理页只看配置面，不显示在线/离线）
const disabledOf = (sec) => sec.servers.filter((s) => !s.enabled).length

function groupCount(id) {
  return servers.value.filter((s) => s.group_id === id).length
}

const createVisible = ref(false)
const creating = ref(false)
const newForm = ref({ name: '', agent_addr: '', token: '', mode: 'node', group_id: 0, remark: '' })

// 编辑弹窗（共享组件 ServerEditDialog）
const editDlg = ref(null)

// 可升级 agent 的机器 id 集合（458：已是最新版的行不再显示升级图标；后端 Newer 语义）
const upgradableIds = ref([])

async function load() {
  loading.value = true
  try {
    const [res, gres, ul] = await Promise.all([api.get('/servers'), api.get('/groups'), api.get('/update/agents')])
    servers.value = res.servers || []
    groups.value = gres.groups || []
    upgradableIds.value = (ul || []).filter((u) => u.upgradable).map((u) => u.id)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

const createGuard = useDirtyGuard(() => ({ ...newForm.value }))
function openCreate() {
  newForm.value = { name: '', agent_addr: '', token: '', mode: 'node', group_id: 0, remark: '' }
  createVisible.value = true
  nextTick(() => createGuard.snapshot())
}

async function create() {
  const f = newForm.value
  if (!f.name.trim()) {
    ElMessage.warning('名称必填')
    return
  }
  if (f.mode !== 'push' && !f.agent_addr.trim()) {
    ElMessage.warning('地址必填')
    return
  }
  const body = { name: f.name.trim(), mode: f.mode, group_id: f.group_id || 0, remark: f.remark.trim() }
  if (f.mode === 'push') {
    // push：无地址；密钥手动填/生成，留空由服务端生成
    body.token = f.token.trim()
  } else {
    body.agent_addr = f.agent_addr.trim()
    body.token = f.token.trim()
  }
  creating.value = true
  try {
    const res = await api.post('/servers', body)
    createVisible.value = false
    if (f.mode === 'push' && res?.token) {
      // 上报密钥只在创建响应里回传一次（此后接口不回显明文）
      onceToken.value = res.token
      tokenVisible.value = true
    } else {
      ElMessage.success('创建成功；接入配置见部署目录 agent.yaml')
    }
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    creating.value = false
  }
}

const tokenVisible = ref(false)
const onceToken = ref('')
function copyToken() {
  const t = onceToken.value
  const done = () => ElMessage.success('已复制')
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(t).then(done, () => legacyCopy(t, done))
  } else {
    legacyCopy(t, done)
  }
}
function legacyCopy(text, done) {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.cssText = 'position:fixed;left:-9999px;top:0'
  document.body.appendChild(ta)
  ta.select()
  document.execCommand('copy')
  ta.remove()
  done()
}

function openEdit(row) {
  editDlg.value.open(row)
}

async function toggleEnabled(row) {
  const action = row.enabled ? '停用后不再抓取该机数据' : '启用'
  try {
    await ElMessageBox.confirm(`${action} ${row.name}？`, '确认', { type: 'warning' })
  } catch {
    return
  }
  await api.put(`/servers/${row.id}`, { enabled: !row.enabled })
  await load()
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(
      `删除 ${row.name}？历史数据将一并删除，不可恢复。`,
      '删除服务器',
      { type: 'error', confirmButtonText: '删除' },
    )
  } catch {
    return
  }
  try {
    await api.del(`/servers/${row.id}`)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---------- 在线升级 agent（396）----------
const upgradingAll = ref(false)

// 触发升级并展示每台结果：pull=直推、push=下次上报领取；版本变化即成功
//（列表里的版本号来自各机器下轮采集/上报，稍候刷新可见）
async function showUpgradeResults(results) {
  const lines = results.map((r) => {
    const st = { triggered: '✅ 已推送', queued: '📨 已排队（push 机器下次上报领取）', skipped: '⏭ 跳过', error: '❌ 失败' }[r.state] || r.state
    return `${st} · ${r.name}${r.detail ? '：' + r.detail : ''}`
  })
  await ElMessageBox.alert(lines.join('\n'), '升级指令下发结果', { customStyle: { whiteSpace: 'pre-wrap' }, confirmButtonText: '知道了' }).catch(() => {})
  setTimeout(() => load(), 5000)
}

async function upgradeAgent(row) {
  try {
    await ElMessageBox.confirm(
      `把 ${row.name} 的 agent 在线升级到更新通道最新版？（agent 将校验签名后自动替换重启，失败自动回滚旧版）`,
      '在线升级 Agent',
      { type: 'warning', confirmButtonText: '升级', cancelButtonText: '取消' },
    )
  } catch { return }
  try {
    const results = await api.post('/update/agents/upgrade', { ids: [row.id] })
    await showUpgradeResults(results)
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function upgradeAgentsAll() {
  try {
    await ElMessageBox.confirm(
      '对所有版本落后于更新通道的 agent 触发在线升级（并发限 2，push 机器下次上报时领取）？继续？',
      '升级全部 Agent',
      { type: 'warning', confirmButtonText: '全部升级', cancelButtonText: '取消' },
    )
  } catch { return }
  upgradingAll.value = true
  try {
    const list = await api.get('/update/agents')
    const ids = list.filter((u) => u.upgradable).map((u) => u.id)
    if (!ids.length) {
      ElMessage.info('没有可升级的 agent（全部最新，或版本未知）')
      return
    }
    const results = await api.post('/update/agents/upgrade', { ids })
    await showUpgradeResults(results)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    upgradingAll.value = false
  }
}

// ---------- 分组管理 ----------
function openGroups() {
  newGroupName.value = ''
  groupsVisible.value = true
}

async function addGroup() {
  const name = newGroupName.value.trim()
  if (!name) {
    ElMessage.warning('请输入分组名称')
    return
  }
  try {
    await api.post('/groups', { name })
    newGroupName.value = ''
    ElMessage.success('分组已添加')
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function renameGroup(row) {
  let name
  try {
    ;({ value: name } = await ElMessageBox.prompt('修改分组名称', `重命名「${row.name}」`, {
      closeOnClickModal: false,
      inputValue: row.name,
      inputPattern: /\S+/,
      inputErrorMessage: '名称不能为空',
    }))
  } catch {
    return
  }
  try {
    await api.put(`/groups/${row.id}`, { name: name.trim() })
    ElMessage.success('已重命名')
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function removeGroup(row) {
  const n = groupCount(row.id)
  try {
    await ElMessageBox.confirm(
      `删除分组「${row.name}」？组内 ${n} 台服务器将移到「未分组」，数据不受影响。`,
      '删除分组',
      { type: 'warning', confirmButtonText: '删除' },
    )
  } catch {
    return
  }
  try {
    await api.del(`/groups/${row.id}`)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

onMounted(load)
onActivated(load) // tab 模式下切回本页重载（其他页的编辑/新增即时可见）
</script>

<style scoped>
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}
.head-btns {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
/* 分组分节框（同监控页样式）：组间分离、分组色表头、点击折叠 */
.grp {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-bottom: 14px;
  overflow: hidden;
}
.grp-flat {
  margin-bottom: 14px;
}
.grp-head {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px 14px;
  min-width: 0;
  cursor: pointer;
  user-select: none;
}
.grp-accent {
  width: 4px;
  height: 14px;
  border-radius: 2px;
  flex: none;
}
.grp-name {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.grp-stats {
  margin-left: auto;
  display: flex;
  gap: 12px;
  color: var(--muted);
  font-size: 12px;
  white-space: nowrap;
  flex: none;
}
.grp-stats .ok {
  color: var(--ok);
}
.grp-stats .off {
  color: var(--warn-text);
  font-weight: 600;
}
.grp-stats .muted {
  color: var(--muted);
}
.grp-arrow {
  flex: none;
  margin-left: 4px;
  color: var(--muted);
  transition: transform 0.2s;
}
.grp-arrow.open {
  transform: rotate(180deg);
}
.grp-body {
  padding: 12px 14px;
  border-top: 1px solid var(--border);
}
.grp-add {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}
.grp-add .el-input {
  flex: 1;
}
.token-row {
  display: flex;
  gap: 8px;
  width: 100%;
}
.token-row .el-input {
  flex: 1;
}
.once-tip {
  color: var(--muted);
  font-size: 12px;
  margin-bottom: 10px;
  line-height: 1.5;
}
.token-show {
  display: flex;
  gap: 8px;
  align-items: center;
}
.token-show code {
  flex: 1;
  min-width: 0;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--el-fill-color-light);
  font-size: 12px;
  word-break: break-all;
  user-select: all;
}
.iface-hint {
  color: var(--muted);
  font-size: 12px;
  margin-top: 4px;
  line-height: 1.4;
}
/* ssh 认证区末尾的整行提示（form-item 外） */
.auth-hint {
  margin: -4px 0 12px 100px;
}
</style>
