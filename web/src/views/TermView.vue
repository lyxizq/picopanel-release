<template>
  <div class="term-page">
    <!-- 左栏分组服务器列表（手机端为下拉选择器）；append 插槽挂后台断线红点。
         宽度可拖拽分隔条调节、单击分隔条收起/展开（useSidePanel 记忆） -->
    <ServerTree
      v-show="isMobile || !side.collapsed"
      :style="isMobile ? null : { width: side.width + 'px', flexShrink: 0 }"
      :servers="servers"
      :groups="groups"
      :selected-id="activeTab?.sid || 0"
      @select="onSelect"
      @open-new="openNew"
    >
      <template #append="{ s }">
        <span
          v-if="firstTabOf(s.id) && firstTabOf(s.id).status !== 'open' && firstTabOf(s.id).status !== 'connecting'"
          class="tab-alert"
        ></span>
        <!-- 每服务器行内的专属脚本入口（stop 防触发行选中/连接） -->
        <el-icon class="row-scripts" title="本服务器的专属脚本" @click.stop="openScripts(s)"><Tickets /></el-icon>
      </template>
    </ServerTree>
    <div
      v-if="!isMobile"
      class="side-divider"
      :class="{ on: side.resizing }"
      :title="side.collapsed ? '单击展开侧栏' : '拖拽调节宽度 · 单击收起侧栏'"
      @mousedown="side.startDrag"
    />

    <div class="term-main">
      <!-- 会话标签栏：同一服务器可多开，同名 tab 追加 ·2/·3 序号 -->
      <div v-if="tabs.length" class="tab-bar">
        <div
          v-for="t in tabs"
          :key="t.key"
          class="tab"
          :class="{ on: t.key === activeID }"
          :title="tabTitle(t)"
          @click="activeID = t.key"
          @dblclick="onTabDblclick(t, $event)"
        >
          <!-- 标签内文字最前方的专属脚本入口（真 button 保证各端可靠命中；
               仅激活 tab 响应，未激活点击无效；stop 防切换 tab） -->
          <button type="button" class="tab-scr" title="本服务器的专属脚本" @click.stop="onTabScrClick(t)">
            <el-icon><Tickets /></el-icon>
          </button>
          <span class="tab-name">{{ t.name }}</span>
          <el-icon class="tab-close" @click.stop="closeTab(t)"><Close /></el-icon>
        </div>
        <!-- 手机端兜底入口：侧栏是下拉选择器没有行内按钮（桌面端入口在服务器行内） -->
        <el-tooltip v-if="isMobile && activeTab" content="本服务器的专属脚本" placement="bottom" :hide-after="0">
          <el-button size="small" plain class="tab-scripts" @click="openScripts()">
            <el-icon><Tickets /></el-icon>
          </el-button>
        </el-tooltip>
      </div>

      <!-- 终端 panes：v-show 保活（切走不断会话）；tab 携带本次连接凭据 -->
      <div class="panes">
        <TermPane
          v-for="t in tabs"
          :key="t.key"
          v-show="t.key === activeID"
          :server="t"
          :active="t.key === activeID"
          @status="(st, msg) => setTabStatus(t, st, msg)"
          @auth-error="onAuthError(t)"
        />
        <div v-if="!tabs.length" class="pane-empty">
          <el-icon><Platform /></el-icon>
          <p>点击左侧服务器打开终端</p>
          <p class="sub">除「Agent 上报」外的服务器均可连接（自动取配置地址的 22 端口）</p>
        </div>
      </div>
    </div>

    <!-- 凭据输入（服务器未配齐用户名/密码时弹出） -->
    <SshCredDialog ref="credDlg" />

    <!-- 专属脚本：单弹层列表 + 底部预览/编辑窗（行点击预览、编辑/新增就地切换，Monaco 默认 shell 语法） -->
    <el-dialog v-model="scriptsDlg.show" :title="`专属脚本 - ${scriptsDlg.sname}`" width="min(94vw, 760px)" append-to-body>
      <div class="sc-head">
        <span class="sc-hint">脚本保存在服务端，仅本服务器可用；点击脚本在下方预览</span>
        <el-button size="small" type="primary" plain @click="newScript">新增</el-button>
      </div>
      <el-table
        class="sc-table"
        :data="scriptsDlg.list"
        v-loading="scriptsDlg.loading"
        size="small"
        empty-text="暂无脚本"
        :row-class-name="scRowClass"
        @row-click="previewScript"
      >
        <el-table-column label="名称" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">{{ row.name }}</template>
        </el-table-column>
        <el-table-column label="语言" width="90">
          <template #default="{ row }">{{ langLabel(row.lang) }}</template>
        </el-table-column>
        <el-table-column label="更新时间" width="160">
          <template #default="{ row }">{{ fmtTime(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="130" align="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click.stop="editScript(row)">编辑</el-button>
            <el-button link type="primary" size="small" @click.stop="copyScript(row)">复制</el-button>
            <el-button link type="danger" size="small" @click.stop="delScript(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <!-- 底部预览/编辑窗：预览态名称/语言标签 + 并列复制；编辑态表单就地切换，不再弹层 -->
      <div class="sc-pane">
        <div class="sc-pane-bar">
          <template v-if="scriptsDlg.pane === 'edit'">
            <el-input v-model="scriptsDlg.name" placeholder="脚本名称" maxlength="100" class="sc-name" />
            <el-select v-model="scriptsDlg.lang" size="small" filterable class="sc-lang">
              <el-option v-for="l in SCRIPT_LANGS" :key="l.v" :label="l.l" :value="l.v" />
            </el-select>
            <el-button size="small" @click="cancelEdit">取消</el-button>
            <el-button size="small" type="primary" :loading="scriptsDlg.saving" @click="saveScript">保存</el-button>
          </template>
          <template v-else-if="scriptsDlg.pane === 'view'">
            <span class="sc-pane-name" :title="scriptsDlg.name">{{ scriptsDlg.name }}</span>
            <span class="sc-pane-lang">{{ langLabel(scriptsDlg.lang) }}</span>
            <el-button size="small" type="primary" plain @click="copyText(scriptsDlg.name, scriptsDlg.content)">复制</el-button>
          </template>
          <span v-else class="sc-pane-empty">点击上方脚本查看内容</span>
        </div>
        <template v-if="scriptsDlg.pane">
          <div v-if="scPlain" class="sc-plain">
            <el-input v-model="scriptsDlg.content" type="textarea" resize="none" class="sc-plain-ta" :disabled="scriptsDlg.pane === 'view'" />
            <span class="sc-plain-tip">简易编辑模式（无语法高亮），保存不受影响</span>
          </div>
          <!-- 失败后模块图缓存失败结果，页内重试无效，给「刷新重试」+ 简易编辑器兜底 -->
          <div v-else-if="scErr" class="sc-chunk-tip">
            <p>编辑器组件加载失败（网络不稳定或系统更新后缓存失效）</p>
            <div class="sc-chunk-btns">
              <el-button size="small" @click="reloadForSc">刷新页面重试</el-button>
              <el-button size="small" type="primary" @click="scPlain = true">使用简易编辑器</el-button>
            </div>
          </div>
          <component :is="scComp" v-else-if="scComp" v-model="scriptsDlg.content" :language="scriptsDlg.lang" :read-only="scriptsDlg.pane === 'view'" class="sc-code" />
          <div v-else class="sc-chunk-tip">
            <el-icon class="is-loading sc-chunk-spin"><Loading /></el-icon>
            <p>编辑器组件加载中（约 4MB，弱网下首次打开稍慢）…</p>
          </div>
        </template>
      </div>
      <template #footer>
        <el-button @click="scriptsDlg.show = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// Web 终端主页：左栏分组服务器列表（term_available 过滤、按服务器分组聚合，
// 未分组固定最后）+ 多 tab 终端。panes 用 v-show 保活，切 tab 不断 SSH 会话。
// 凭据三分支：配齐直连；有用户名无密码→只补密码；无用户名→双框；
// 提交先经 /term/sftp-auth 试连（错密码框内报错可重输），成功后凭据随
// WS auth 帧下发（save=false 时密码不落库，仅本次连接有效）。
defineOptions({ name: 'TermView' })
import { computed, onActivated, onMounted, reactive, ref, shallowRef } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Close, Loading, Platform, Tickets } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { takeTermPending } from '../termPending.js'
import { fmtTime } from '../utils/format.js'
import { useIsMobile } from '../composables/useIsMobile.js'
import { useSidePanel } from '../composables/useSidePanel.js'
import TermPane from '../components/TermPane.vue'
import SshCredDialog from '../components/SshCredDialog.vue'
import ServerTree from '../components/ServerTree.vue'
// Monaco 编辑器独立 chunk（约 4MB）：进编辑/查看才拉取，手动管理加载态——
// 加载中显示提示、失败给重试（defineAsyncComponent 失败静默渲染空白）
const scComp = shallowRef(null)
const scErr = ref(false)
let scBusy = false
async function loadScChunk() {
  if (scComp.value || scBusy) return
  scBusy = true
  scErr.value = false
  try {
    const m = await import('../components/CodeEditor.vue')
    scComp.value = m.default
  } catch {
    scErr.value = true
  } finally {
    scBusy = false
  }
}
const scPlain = ref(false)
function reloadForSc() {
  location.reload()
}

const servers = ref([])
const groups = ref([])
// tab：key=会话唯一标识（同服务器多开），id=服务器 ID（TermPane 的 server_id /
// 凭据分支 / 脚本入口都按它走），name 显示名（重开追加 ·2/·3），cred/credSeq 同前
const tabs = ref([])
const activeID = ref('') // 当前激活 tab 的 key
const credDlg = ref(null)
const activeTab = computed(() => tabs.value.find((t) => t.key === activeID.value))
const isMobile = useIsMobile()
const side = useSidePanel('term', 210) // 左栏宽度/收起，localStorage 记忆
let tabSeq = 0

function firstTabOf(sid) {
  return tabs.value.find((t) => t.id === sid)
}

// tab tooltip（377）：名称 + 服务器地址（采集地址，含端口）——同服务器多开/同名
// tab 靠它区分机器；服务器清单里已删除的找不到时回落纯名称
function tabTitle(t) {
  const s = servers.value.find((x) => x.id === t.id)
  const addr = s?.agent_addr || ''
  return (addr ? `${t.name} · ${addr}` : t.name) + '（双击关闭）'
}

function setTabStatus(t, st, msg) {
  t.status = st
  t.error = st === 'error' ? msg || '' : ''
}

// 凭据分支：无用户名→双框；有用户名且有密码/私钥→直连；否则只补密码
function credKind(s) {
  if (!s.ssh_user) return 'both'
  if (s.ssh_password_set || s.ssh_key_path) return 'none'
  return 'pass'
}

// 凭据试连（对话框 verify）：失败抛错由对话框展示，成功即凭据有效。
// push 机器弹窗补填的 SSH 地址先落库（383），试连与后续 WS 连接才有地址可用
async function verifyCred(s, cred) {
  if (cred.sshAddr) {
    await api.put(`/servers/${s.id}`, { ssh_addr: cred.sshAddr })
    s.ssh_addr = cred.sshAddr
  }
  await api.post('/term/sftp-auth', {
    server_id: s.id,
    username: cred.username,
    password: cred.password,
    save: cred.save,
  })
}

// 单击：已有窗口→跳到第一个；没有→延时开首个窗口（延时是给双击抢占用：
// 双击 = 新开窗口，不能先弹凭据框）
let selectTimer = null
function onSelect(s) {
  if (!s) return
  const t = firstTabOf(s.id)
  if (t) {
    activeID.value = t.key
    return
  }
  clearTimeout(selectTimer)
  selectTimer = setTimeout(() => openWithCred(s, false), 250)
}

// 双击：总是新开一个窗口（凭据复用该服务器已开窗口的，本次会话内验过不再问）
function openNew(s) {
  if (!s) return
  clearTimeout(selectTimer)
  openWithCred(s, true)
}

// push 且未配专用终端地址：无采集地址可回退，首次连接需在凭据框补填（383）
function needsTermAddr(s) {
  return s.collect_mode === 'push' && !s.ssh_addr
}

async function openWithCred(s, forceNew) {
  if (!s) return
  const exist = firstTabOf(s.id)
  if (!forceNew && exist) {
    activeID.value = exist.key
    return
  }
  let cred = null
  const kind = credKind(s)
  if (kind !== 'none' || needsTermAddr(s)) {
    cred = exist?.cred || tabs.value.find((t) => t.id === s.id && t.cred)?.cred || null
    if (!cred || needsTermAddr(s)) {
      cred = await credDlg.value.open(s, kind === 'none' ? 'pass' : kind, { verify: (c) => verifyCred(s, c) })
      if (!cred) return
    }
  }
  const n = tabs.value.filter((t) => t.id === s.id).length
  const t = {
    key: `t${++tabSeq}`,
    id: s.id,
    name: n ? `${s.name}·${n + 1}` : s.name,
    status: 'connecting',
    cred,
    credSeq: 1,
  }
  tabs.value.push(t)
  activeID.value = t.key
}

// pane 遮罩「输入密码重连」：重开凭据框（带上 pane 上的失败原因）
async function onAuthError(t) {
  const s = servers.value.find((x) => x.id === t.id) || t
  let kind = credKind(s)
  if (kind === 'none') kind = 'pass' // 已配齐但仍失败：允许密码覆盖重试
  const cred = await credDlg.value.open(s, kind, {
    verify: (c) => verifyCred(s, c),
    error: t.error || '',
  })
  if (!cred) return
  t.cred = cred
  t.credSeq++
}

// 关 tab 二次确认（374）：断开的是真实 SSH 会话，× 误触/双击关 tab 代价高；
// × 按钮与双击两条路径都汇聚这里，确认后才真正摘除
async function closeTab(t) {
  try {
    await ElMessageBox.confirm(`关闭「${t.name}」的终端会话？关闭后连接将断开，不可恢复。`, '关闭确认', {
      type: 'warning', confirmButtonText: '关闭', cancelButtonText: '取消',
    })
  } catch {
    return
  }
  const i = tabs.value.findIndex((x) => x.key === t.key)
  if (i < 0) return
  tabs.value.splice(i, 1)
  if (activeID.value === t.key) {
    const next = tabs.value[Math.min(i, tabs.value.length - 1)]
    activeID.value = next ? next.key : ''
  }
}

// 专属脚本按钮：仅激活 tab 响应（未激活点击无效），避免误触打开别人的脚本列表
function onTabScrClick(t) {
  if (t.key !== activeID.value) return
  openScripts(serverOfTab(t))
}

// 双击关 tab 只对标签空白区/名称生效：脚本按钮、关闭按钮上的双击不关 tab
//（按钮的两次 click 已各自带 stop，dblclick 仍会冒泡到标签，这里显式排除）
function onTabDblclick(t, e) {
  if (e.target.closest('.tab-scr, .tab-close')) return
  closeTab(t)
}

async function load() {
  try {
    const [res, gres] = await Promise.all([api.get('/servers'), api.get('/groups')])
    servers.value = res.servers || []
    groups.value = gres.groups || []
    // 服务器被删后清掉对应 tab
    for (const t of [...tabs.value]) {
      if (!servers.value.find((s) => s.id === t.id)) {
        closeTab(t)
      }
    }
    // 文件管理页「终端」按钮带来的跳转意图：打开对应服务器会话（取走即清空）
    const pend = takeTermPending()
    if (pend) {
      const s = servers.value.find((x) => x.id === pend.id)
      if (s) openWithCred(s, false)
    }
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---------- 专属脚本（按服务器隔离，存服务端 term_scripts 表） ----------

// 语言下拉：shell 置顶作默认（与文件管理编辑页同一套 Monaco 语言集）
const SCRIPT_LANGS = [
  { v: 'shell', l: 'Shell' },
  { v: 'python', l: 'Python' },
  { v: 'yaml', l: 'YAML' },
  { v: 'json', l: 'JSON' },
  { v: 'ini', l: 'INI' },
  { v: 'sql', l: 'SQL' },
  { v: 'javascript', l: 'JavaScript' },
  { v: 'typescript', l: 'TypeScript' },
  { v: 'xml', l: 'XML' },
  { v: 'html', l: 'HTML' },
  { v: 'css', l: 'CSS' },
  { v: 'java', l: 'Java' },
  { v: 'php', l: 'PHP' },
  { v: 'markdown', l: 'Markdown' },
  { v: 'plaintext', l: '纯文本' },
]

const scriptsDlg = reactive({
  show: false,
  sid: 0,
  sname: '',
  list: [],
  loading: false,
  // 底部预览/编辑窗：''=未选（占位提示）、view=预览、edit=编辑（editID=0 为新增）
  pane: '',
  selID: 0, // 当前列表选中行（预览/编辑中的脚本，行高亮同步）
  editID: 0,
  name: '',
  lang: 'shell',
  content: '',
  saving: false,
})

function langLabel(v) {
  return SCRIPT_LANGS.find((l) => l.v === v)?.l || v || 'Shell'
}

// 选中行高亮（行点击预览/编辑按钮共用 selID 驱动）
function scRowClass({ row }) {
  return row.id === scriptsDlg.selID ? 'sc-cur' : ''
}

// 入口三处：侧栏服务器行内（传服务器对象）+ 手机端标签栏（缺省取当前 tab）
// + 桌面端会话标签内按钮（serverOfTab 由 tab 反查服务器对象）
function openScripts(s) {
  const t = s || activeTab.value
  if (!t) return
  scriptsDlg.sid = t.id
  scriptsDlg.sname = t.name
  clearPane()
  scriptsDlg.show = true
  loadScripts()
}

// 会话标签 → 服务器对象（tab.name 可能带 ·2 序号，名称取服务器清单为准）
function serverOfTab(t) {
  return servers.value.find((x) => x.id === t.id) || { id: t.id, name: t.name }
}

async function loadScripts() {
  scriptsDlg.loading = true
  try {
    const r = await api.get(`/term/scripts?server_id=${scriptsDlg.sid}`)
    scriptsDlg.list = r.scripts || []
    // 列表重载后选中行对象被替换（更新时间已变），预览窗内容同步刷新保持一致
    if (scriptsDlg.selID) {
      const row = scriptsDlg.list.find((x) => x.id === scriptsDlg.selID)
      if (row && scriptsDlg.pane === 'view') {
        scriptsDlg.name = row.name
        scriptsDlg.lang = row.lang || 'shell'
        scriptsDlg.content = row.content || ''
      }
    }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    scriptsDlg.loading = false
  }
}

function clearPane() {
  scriptsDlg.pane = ''
  scriptsDlg.selID = 0
  scriptsDlg.editID = 0
  scriptsDlg.name = ''
  scriptsDlg.lang = 'shell'
  scriptsDlg.content = ''
}

function newScript() {
  scriptsDlg.pane = 'edit'
  scriptsDlg.editID = 0
  scriptsDlg.selID = 0
  scriptsDlg.name = ''
  scriptsDlg.lang = 'shell'
  scriptsDlg.content = ''
  scPlain.value = false
  loadScChunk()
}

function editScript(row) {
  scriptsDlg.pane = 'edit'
  scriptsDlg.editID = row.id
  scriptsDlg.selID = row.id
  scriptsDlg.name = row.name
  scriptsDlg.lang = row.lang || 'shell'
  scriptsDlg.content = row.content || ''
  scPlain.value = false
  loadScChunk()
}

// 行点击 → 预览窗展示（只读 Monaco + 并列复制）；编辑中不切换，防误触丢未保存内容
function previewScript(row) {
  if (scriptsDlg.pane === 'edit') return
  applyPreview(row)
}

function applyPreview(row) {
  scriptsDlg.pane = 'view'
  scriptsDlg.editID = row.id
  scriptsDlg.selID = row.id
  scriptsDlg.name = row.name
  scriptsDlg.lang = row.lang || 'shell'
  scriptsDlg.content = row.content || ''
  loadScChunk()
}

// 取消编辑：已有脚本回到它的预览态，新增则清空预览窗
function cancelEdit() {
  if (scriptsDlg.editID) {
    const row = scriptsDlg.list.find((r) => r.id === scriptsDlg.editID)
    if (row) {
      applyPreview(row)
      return
    }
  }
  clearPane()
}

// 复制 = 复制脚本内容到剪贴板（不新建副本）；行内钮与预览窗并列复制钮共用
async function copyText(name, content) {
  try {
    await navigator.clipboard.writeText(content || '')
    ElMessage.success(`「${name}」内容已复制到剪贴板`)
  } catch {
    ElMessage.error('复制失败：浏览器未授权剪贴板访问')
  }
}

function copyScript(row) {
  copyText(row.name, row.content)
}

async function delScript(row) {
  try {
    await ElMessageBox.confirm(`删除脚本「${row.name}」？`, '删除脚本', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api.del(`/term/scripts/${row.id}?server_id=${scriptsDlg.sid}`)
    // 删的正是预览/编辑中的脚本：清空预览窗
    if (scriptsDlg.selID === row.id) clearPane()
    loadScripts()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function saveScript() {
  if (scriptsDlg.saving) return
  if (!scriptsDlg.name.trim()) {
    ElMessage.warning('脚本名称不能为空')
    return
  }
  scriptsDlg.saving = true
  try {
    const body = {
      server_id: scriptsDlg.sid,
      name: scriptsDlg.name.trim(),
      lang: scriptsDlg.lang,
      content: scriptsDlg.content,
    }
    let id = scriptsDlg.editID
    if (id) {
      await api.put(`/term/scripts/${id}?server_id=${scriptsDlg.sid}`, body)
    } else {
      const r = await api.post('/term/scripts', body)
      id = r.script?.id || 0
    }
    ElMessage.success('已保存')
    await loadScripts()
    // 保存后停在该脚本的预览态（列表已刷新，更新时间同步）
    const row = scriptsDlg.list.find((x) => x.id === id)
    if (row) applyPreview(row)
    else clearPane()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    scriptsDlg.saving = false
  }
}

// 页面失焦/切走时终端照常（服务端 30 分钟空闲策略管理），无需额外处理；
// 离开本页（tab 关闭/组件卸载）关闭全部会话释放 SSH 连接
onMounted(load)
onActivated(load) // 全局 tab 模式下切回本页重载列表（新增/删除的服务器同步可见）
</script>

<style scoped>
.term-page {
  display: flex;
  /* 侧栏与主区间距=分隔条自身 8px（可拖拽调宽、单击收起，见 useSidePanel） */
  gap: 0;
  min-height: 0;
  flex-direction: column;
  /* header 56 + main padding 20×2；main 是 overflow 容器，100% 不可靠，直接按视口算 */
  height: calc(100vh - 130px); /* 顶栏 + 标签栏 */
}
/* 桌面端才恢复左右布局（手机端：选择器在上、终端在下） */
@media (min-width: 769px) {
  .term-page {
    flex-direction: row;
  }
}
@media (max-width: 768px) {
  .term-page {
    height: calc(100vh - 110px); /* 手机端：顶栏 + 标签栏 + main padding 10×2 */
    gap: 8px; /* 手机端无分隔条（已隐藏），上下堆叠留常规间距 */
  }
}

/* 主区 */
.term-main {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.tab-bar {
  flex: none;
  display: flex;
  gap: 6px;
  overflow-x: auto;
  padding-bottom: 2px;
}
.tab {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px;
  font-size: 13px;
  color: var(--text-2);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: pointer;
  white-space: nowrap;
  user-select: none;
}
.tab:hover {
  color: var(--ink);
  border-color: var(--brand);
}
.tab.on {
  color: #fff;
  background: var(--brand);
  border-color: var(--brand);
  font-weight: 600;
}
.tab.on .tab-close {
  color: rgba(255, 255, 255, 0.85);
}
.tab.on .tab-close:hover {
  color: #fff;
  background: rgba(255, 255, 255, 0.2);
}
.tab-close {
  border-radius: 4px;
  font-size: 12px;
  color: var(--muted);
}
.tab-close:hover {
  background: var(--border);
  color: var(--danger, #f56c6c);
}
/* 标签内文字最前方的专属脚本按钮（真 button：保证各端可点击；激活态跟随白字） */
.tab-scr {
  flex: none;
  display: inline-flex;
  align-items: center;
  padding: 2px;
  font-size: 13px;
  line-height: 1;
  color: var(--muted);
  background: none;
  border: none;
  border-radius: 3px;
  cursor: pointer;
}
.tab-scr:hover {
  color: var(--brand);
}
.tab.on .tab-scr {
  color: rgba(255, 255, 255, 0.85);
}
.tab.on .tab-scr:hover {
  color: #fff;
  background: rgba(255, 255, 255, 0.2);
}
.tab-scripts {
  margin-left: auto; /* 顶到标签栏右端 */
  flex: none;
}
/* 服务器行内专属脚本按钮：行内右对齐，常显（主文字色，悬停品牌色） */
.row-scripts {
  flex: none;
  margin-left: auto;
  font-size: 15px;
  padding: 1px;
  border-radius: 4px;
  color: var(--ink);
}
.row-scripts:hover {
  color: var(--brand);
  background: var(--el-fill-color);
}

/* 专属脚本弹窗 */
.sc-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 10px;
}
.sc-hint {
  font-size: 12px;
  color: var(--muted);
}
/* 列表区：行可点击预览（操作钮 stop 不冒泡），选中行高亮同步预览窗 */
.sc-table :deep(tbody tr) {
  cursor: pointer;
}
.sc-table :deep(.sc-cur td) {
  /* 品牌紫半透：主题未覆盖 --el-color-primary-light-9（暗色下仍是亮蓝、浅字不可读，
     2026-10-12 用户报图），深浅主题下紫透高亮均保持文字可读 */
  background: rgba(138, 108, 240, 0.16);
}
/* 底部预览/编辑窗：工具条（预览=名称/语言/复制；编辑=表单+保存取消）+ 编辑器 */
.sc-pane {
  margin-top: 10px;
}
.sc-pane-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.sc-name {
  flex: 1;
  min-width: 160px;
}
.sc-lang {
  width: 140px;
  flex: none;
}
.sc-pane-name {
  font-weight: 600;
  font-size: 13px;
  max-width: 46%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sc-pane-lang {
  flex: 1;
  font-size: 12px;
  color: var(--muted);
}
.sc-pane-empty {
  font-size: 12px;
  color: var(--muted);
}
.sc-code {
  height: 34vh;
}
/* 编辑器 chunk 加载中/加载失败的占位提示（居中，占满 .sc-code 高度） */
.sc-chunk-tip {
  height: 34vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--muted);
  font-size: 13px;
}
.sc-chunk-spin {
  font-size: 26px;
  color: var(--brand, #409eff);
}
.sc-chunk-btns {
  display: flex;
  gap: 10px;
}
/* 简易编辑兜底：纯 textarea（编辑器 chunk 加载失败仍可编辑/保存脚本） */
.sc-plain {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.sc-plain-ta {
  height: 34vh;
}
.sc-plain-ta :deep(.el-textarea__inner) {
  height: 100%;
  font-family: var(--mono, 'SFMono-Regular', Consolas, monospace);
}
.sc-plain-tip {
  font-size: 12px;
  color: var(--muted);
}
.panes {
  flex: 1;
  min-height: 240px;
  position: relative;
  border-radius: 8px;
  overflow: hidden;
  border: 1px solid var(--border, #e4e7ed);
}
.pane-empty {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: var(--muted);
  background: var(--surface);
  font-size: 14px;
}
.pane-empty .sub {
  font-size: 12px;
  opacity: 0.8;
}
</style>
