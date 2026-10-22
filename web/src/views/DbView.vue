<script setup>
// 数据库管理工具（323-325）：Navicat 式连接管理 + 库表浏览 + 数据增删改查 +
// 查询（SQL 控制台，monaco 复用 CodeEditor 懒加载 chunk + 关键字/表/字段补全）
// + 备份/导入 + SSH 隧道。
defineOptions({ name: 'DbView' })
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { api, apiForm, getToken } from '../api/index.js'
import { ElMessage, ElMessageBox } from 'element-plus'
// monaco 很重（~4MB），异步加载（同 TermView 先例），DB 页首开不再整包拉
const CodeEditor = defineAsyncComponent(() => import('../components/CodeEditor.vue'))
import { useSidePanel } from '../composables/useSidePanel.js'

// 2026-10-03 用户要求（TODO 330）：数据库工具整体只读——行级新增/编辑/删除与
// 导入按钮屏蔽、SQL 控制台仅查询（后端 dbtool.go 的 guardWrite/query 两处同步
// 收紧）。功能代码全部保留，恢复写入：本开关改 false 并还原后端两处。
const writesBlocked = true

// ---- 连接 ----
const conns = ref([])
const maxConns = ref(16)
const curId = ref(null) // null=未选/无连接；勿用 0——el-select 无匹配选项时会把原始值 0 直接显示出来
const cur = computed(() => conns.value.find(c => c.id === curId.value) || null)

async function loadConns(selectId) {
  const r = await api.get('/dbs')
  conns.value = r.connections || []
  maxConns.value = r.max || 16
  if (selectId && conns.value.some(c => c.id === selectId)) {
    curId.value = selectId
  } else if (!conns.value.some(c => c.id === curId.value)) {
    // 不自动选中首个连接（用户要求 341）：打开页面只列清单不连库，选定才连
    curId.value = null
  }
}

// ---- 浏览树（schema → 表） ----
const schemas = ref([])
const curSchema = ref('')
const tables = ref([])
const curTable = ref('')
const treeLoading = ref(false)
// 浏览树分栏可调宽（353，文档/终端同款 useSidePanel）：拖拽 180~480、单击收起/展开、记忆
const side = useSidePanel('db', 220)

async function selectConn() {
  curTable.value = ''
  detail.value = null
  rowsData.value = null
  backups.value = []
  if (!cur.value) { schemas.value = []; tables.value = []; return }
  treeLoading.value = true
  try {
    const r = await api.get(`/dbs/${cur.value.id}/schemas`)
    schemas.value = r.schemas || []
    // 上次选中的 schema 仍在则保持；否则优先连接配置的默认库，再取第一个
    if (!schemas.value.includes(curSchema.value)) {
      const def = cur.value?.database
      curSchema.value = schemas.value.includes(def) ? def : (schemas.value[0] || '')
    }
    if (curSchema.value) await loadTables()
    else tables.value = []
  } catch (e) {
    schemas.value = []; tables.value = []
    ElMessage.error(e.message)
  } finally {
    treeLoading.value = false
  }
}

async function loadTables() {
  treeLoading.value = true
  try {
    const r = await api.get(`/dbs/${cur.value.id}/tables?schema=${encodeURIComponent(curSchema.value)}`)
    tables.value = r.tables || []
  } catch (e) {
    tables.value = []
    ElMessage.error(e.message)
  } finally {
    treeLoading.value = false
  }
}

// ---- 表详情与数据 ----
const mainTab = ref('browse') // browse | console | backup
const tableTab = ref('data')  // data | struct | index | ddl
const detail = ref(null)
const rowsData = ref(null)
const rowsLoading = ref(false)
const page = ref(1)
const size = ref(10) // 默认 10 条/页，可选 10/20/50（350）
const sortCol = ref('')
const sortDir = ref('')
const fCol = ref('')
const fOp = ref('like')
const fVal = ref('')

async function selectTable(name) {
  curTable.value = name
  tableTab.value = 'data'
  page.value = 1
  sortCol.value = ''
  sortDir.value = ''
  detail.value = null
  rowsData.value = null
  await Promise.all([loadDetail(), loadRows()])
}

async function loadDetail() {
  if (!curTable.value) return
  try {
    const d = await api.get(`/dbs/${cur.value.id}/table?schema=${encodeURIComponent(curSchema.value)}&name=${encodeURIComponent(curTable.value)}`)
    // 数组字段兜底归一：无主键表/视图旧后端可能给 null（346 渲染炸点），防御性拉平
    detail.value = d ? { ...d, pk: d.pk || [], columns: d.columns || [], indexes: d.indexes || [] } : null
  } catch (e) {
    detail.value = null
    ElMessage.error(e.message)
  }
}

const gridRows = computed(() =>
  (rowsData.value?.rows || []).map(r => Object.fromEntries((rowsData.value?.columns || []).map((c, i) => [c, r[i]]))))

async function loadRows() {
  if (!curTable.value) return
  const p = new URLSearchParams({ schema: curSchema.value, table: curTable.value, page: page.value, size: size.value })
  if (sortCol.value) { p.set('sort', sortCol.value); p.set('dir', sortDir.value) }
  if (fCol.value && fVal.value !== '') {
    p.set('filters', JSON.stringify([{ col: fCol.value, op: fOp.value, val: fVal.value }]))
  }
  rowsLoading.value = true
  try {
    rowsData.value = await api.get(`/dbs/${cur.value.id}/rows?${p}`)
  } catch (e) {
    rowsData.value = null
    ElMessage.error(e.message)
  } finally {
    rowsLoading.value = false
  }
}

function onSortChange({ prop, order }) {
  if (!order) { sortCol.value = ''; sortDir.value = '' } else {
    sortCol.value = prop
    sortDir.value = order === 'ascending' ? 'asc' : 'desc'
  }
  loadRows()
}

function applyFilter() {
  page.value = 1
  loadRows()
}

// ---- 行编辑（主键定位；无主键表只读，Navicat 同款口径） ----
const editDlg = ref(false)
const rowDetailDlg = ref(false)
const rowDetailRow = ref(null)

function openRowDetail(row) {
  rowDetailRow.value = row
  rowDetailDlg.value = true
}
const editMode = ref('insert')
const editForm = ref([]) // [{name,type,nullable,auto,value,null}]
const editPk = ref({})

const canEdit = computed(() => (detail.value?.pk?.length || 0) > 0)

function openInsert() {
  editMode.value = 'insert'
  editForm.value = (detail.value?.columns || []).map(c => ({
    name: c.name, type: c.type, nullable: c.nullable, auto: c.auto_inc,
    value: '', useNull: c.nullable,
  }))
  editDlg.value = true
}

function openEdit(row) {
  editMode.value = 'update'
  editPk.value = {}
  for (const k of detail.value.pk) editPk.value[k] = row[k]
  editForm.value = (detail.value?.columns || []).map(c => {
    const v = row[c.name]
    return { name: c.name, type: c.type, nullable: c.nullable, auto: c.auto_inc, value: v === null ? '' : String(v), useNull: v === null }
  })
  editDlg.value = true
}

async function saveRow() {
  const values = {}
  for (const f of editForm.value) {
    if (f.useNull) values[f.name] = null
    else values[f.name] = f.value
  }
  try {
    if (editMode.value === 'insert') {
      await api.post(`/dbs/${cur.value.id}/rows`, { schema: curSchema.value, table: curTable.value, values })
      ElMessage.success('已插入')
    } else {
      await api.post(`/dbs/${cur.value.id}/rows/update`, { schema: curSchema.value, table: curTable.value, pk: editPk.value, values })
      ElMessage.success('已更新')
    }
    editDlg.value = false
    await loadDetail()
    loadRows()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function deleteRow(row) {
  try {
    await ElMessageBox.confirm('确认删除该行？此操作直接写库，不可撤销。', '删除确认', { type: 'warning' })
  } catch { return }
  try {
    const r = await api.post(`/dbs/${cur.value.id}/rows/delete`, { schema: curSchema.value, table: curTable.value, pk: Object.fromEntries(detail.value.pk.map(k => [k, row[k]])) })
    ElMessage.success(`已删除 ${r.affected} 行`)
    await loadDetail()
    loadRows()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 查询（SQL 控制台，338 改名）----
const sqlText = ref('')
const consoleRes = ref(null)
const running = ref(false)
const HIST_KEY = 'db-console-history'
const hist = ref(JSON.parse(localStorage.getItem(HIST_KEY) || '[]'))

function pushHist(sql) {
  hist.value = [sql, ...hist.value.filter(s => s !== sql)].slice(0, 50)
  localStorage.setItem(HIST_KEY, JSON.stringify(hist.value))
}

async function runSql(confirmed = false) {
  const sql = sqlText.value.trim()
  if (!sql) return
  running.value = true
  try {
    // schema=库上下文：后端钉住一条连接先 USE/SET search_path 再执行，
    // 连接未配默认库时不再报 No database selected（339）
    const r = await api.post(`/dbs/${cur.value.id}/query`, { sql, schema: curSchema.value, confirm: confirmed })
    if (r.need_confirm) {
      await ElMessageBox.confirm(`${r.reason}。确认执行？`, '危险语句确认', { type: 'warning', confirmButtonText: '执行', cancelButtonText: '取消' })
      running.value = false
      return runSql(true)
    }
    consoleRes.value = r
    pushHist(sql)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    running.value = false
  }
}

// 查询库切换（与浏览树共享 curSchema）：树与补全元数据跟随刷新
function onConsoleSchemaChange() {
  loadTables()
  loadColMap()
}

// ---- SQL 编辑器补全（340）：关键字词表 + 当前库表/字段元数据 ----
const colMap = ref({}) // 表名→列名清单（/columns 一次取全库，含视图）

async function loadColMap() {
  if (!cur.value) { colMap.value = {}; return }
  try {
    const r = await api.get(`/dbs/${cur.value.id}/columns?schema=${encodeURIComponent(curSchema.value)}`)
    colMap.value = r.columns || {}
  } catch {
    colMap.value = {}
  }
}

const SQL_KEYWORDS = [
  'SELECT', 'DISTINCT', 'FROM', 'WHERE', 'GROUP BY', 'HAVING', 'ORDER BY', 'ASC', 'DESC',
  'LIMIT', 'OFFSET', 'JOIN', 'LEFT JOIN', 'RIGHT JOIN', 'INNER JOIN', 'OUTER', 'ON', 'AS',
  'AND', 'OR', 'NOT', 'NULL', 'IS', 'IN', 'LIKE', 'BETWEEN', 'EXISTS', 'UNION', 'UNION ALL',
  'COUNT(*)', 'COUNT', 'SUM', 'AVG', 'MIN', 'MAX', 'CASE', 'WHEN', 'THEN', 'ELSE', 'END', 'WITH',
  'SHOW TABLES', 'SHOW DATABASES', 'DESCRIBE', 'EXPLAIN',
  'INSERT INTO', 'VALUES', 'UPDATE', 'SET', 'DELETE FROM', 'CREATE TABLE', 'DROP TABLE', 'ALTER TABLE',
]
const KW_SET = new Set(SQL_KEYWORDS.flatMap((k) => k.split(/\s+/)))

// FROM/JOIN 别名解析：`from users u` / `join t as x`；捕获词撞关键字视为无别名
function resolveTable(model, name) {
  if (colMap.value[name]) return name
  const re = /\b(?:from|join)\s+`?([\w$]+)`?(?:\s+(?:as\s+)?`?([\w$]+)`?)?/gi
  for (const m of model.getValue().matchAll(re)) {
    const [, t, alias] = m
    if (alias && !KW_SET.has(alias.toUpperCase()) && alias.toLowerCase() === name.toLowerCase()) return t
  }
  return null
}

let monacoRef = null
let sqlSuggester = null

function onEditorReady({ monaco: m }) {
  monacoRef = m
  sqlSuggester = m.languages.registerCompletionItemProvider('sql', {
    triggerCharacters: ['.'],
    provideCompletionItems(model, position) {
      const word = model.getWordUntilPosition(position)
      const range = {
        startLineNumber: position.lineNumber, endLineNumber: position.lineNumber,
        startColumn: word.startColumn || position.column, endColumn: word.endColumn || position.column,
      }
      const K = monacoRef.languages.CompletionItemKind
      const before = model.getValueInRange({
        startLineNumber: position.lineNumber, startColumn: 1,
        endLineNumber: position.lineNumber, endColumn: position.column,
      })
      const sug = []
      const dot = before.match(/([A-Za-z_][\w$]*)\s*\.\s*\w*$/)
      if (dot) { // t. → 该表（或别名对应表）的字段
        const t = resolveTable(model, dot[1])
        for (const c of (t && colMap.value[t]) || []) {
          sug.push({ label: c, kind: K.Field, detail: t, insertText: c, range, sortText: '0' + c })
        }
      } else {
        for (const t of Object.keys(colMap.value)) {
          sug.push({ label: t, kind: K.Struct, detail: '表', insertText: t, range, sortText: '0' + t })
        }
        for (const [t, cols] of Object.entries(colMap.value)) {
          for (const c of cols) sug.push({ label: c, kind: K.Field, detail: t, insertText: c, range, sortText: '1' + c })
        }
        for (const k of SQL_KEYWORDS) {
          sug.push({ label: k, kind: K.Keyword, insertText: k, range, sortText: '2' + k })
        }
      }
      return { suggestions: sug }
    },
  })
}

onBeforeUnmount(() => {
  sqlSuggester?.dispose()
  sqlSuggester = null
})

// ---- 备份与导入 ----
const backups = ref([])
const backing = ref(false)
const importing = ref(false)
const fileInput = ref(null)

async function loadBackups() {
  if (!cur.value) return
  try {
    const r = await api.get(`/dbs/${cur.value.id}/backups`)
    backups.value = r.files || []
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function createBackup() {
  if (!cur.value) return
  try {
    await ElMessageBox.confirm('对当前连接执行整库备份？大库可能耗时数分钟。', '备份', { type: 'info' })
  } catch { return }
  backing.value = true
  try {
    const r = await api.post(`/dbs/${cur.value.id}/backups`, { schema: curSchema.value })
    ElMessage.success(`备份完成：${r.file.name}（${fmtSize(r.file.size)}）`)
    loadBackups()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    backing.value = false
  }
}

async function downloadBackup(f) {
  try {
    const res = await fetch(`/api/v1/dbs/backups/download?conn=${cur.value.id}&name=${encodeURIComponent(f.name)}`,
      { headers: { Authorization: 'Bearer ' + getToken() } })
    if (!res.ok) throw new Error('下载失败')
    const blob = await res.blob()
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = f.name
    a.click()
    URL.revokeObjectURL(a.href)
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function deleteBackup(f) {
  try {
    await ElMessageBox.confirm(`删除备份 ${f.name}？`, '删除确认', { type: 'warning' })
  } catch { return }
  try {
    await api.post(`/dbs/${cur.value.id}/backups/delete`, { name: f.name })
    loadBackups()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

function pickImport() { fileInput.value?.click() }

async function onImportFile(ev) {
  const file = ev.target.files?.[0]
  ev.target.value = ''
  if (!file || !cur.value) return
  try {
    await ElMessageBox.confirm(`导入 ${file.name} 将直接对数据库执行其中语句（出错即停）。导入前建议先备份。继续？`, '导入确认', { type: 'warning', confirmButtonText: '导入' })
  } catch { return }
  importing.value = true
  try {
    const fd = new FormData()
    fd.append('file', file)
    const r = await apiForm(`/dbs/${cur.value.id}/import`, fd)
    ElMessage.success(`导入完成：执行 ${r.executed} 条语句`)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    importing.value = false
  }
}

// ---- 连接管理对话框 ----
const connDlg = ref(false)
const connEditId = ref(0)
const connSaving = ref(false)
const testing = ref(false)
const servers = ref([])
const connForm = ref({})
const connGuard = useDirtyGuard(() => ({ ...connForm.value, hasPwd: !!connForm.value.password }))

function blankConn(type) {
  return { name: '', type, host: '', port: 0, database: '', username: '', password: '',
    readonly: false, ssl: 'off', timeout_s: 10, tunnel_server_id: 0 }
}

function openCreate() {
  connEditId.value = 0
  connForm.value = blankConn('mysql')
  connDlg.value = true
  nextTick(() => connGuard.snapshot())
}

function openConnEdit(c) {
  connEditId.value = c.id
  connForm.value = blankConn(c.type)
  Object.assign(connForm.value, {
    name: c.name, host: c.host, port: c.port || 0, database: c.database,
    username: c.username || '', // 用户名随清单解密回显（343），密码仍留空沿用
    password: '', readonly: c.readonly, ssl: c.params?.ssl || 'off',
    timeout_s: c.params?.timeout_s || 10, tunnel_server_id: c.params?.tunnel_server_id || 0,
  })
  connDlg.value = true
  nextTick(() => connGuard.snapshot())
}

async function loadServers() {
  try {
    const r = await api.get('/servers')
    const list = r.servers || r || []
    servers.value = Array.isArray(list) ? list.map(s => ({ id: s.id, name: s.name })) : []
  } catch { servers.value = [] }
}

async function testConnForm() {
  const f = { ...connForm.value }
  testing.value = true
  try {
    if (connEditId.value > 0 && !f.password) {
      // 编辑态未改密码：用库存凭据测
      await api.post(`/dbs/${connEditId.value}/test`, {})
    } else {
      await api.post('/dbs/test', f)
    }
    ElMessage.success('连接成功')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testing.value = false
  }
}

async function saveConn() {
  connSaving.value = true
  try {
    if (connEditId.value > 0) {
      await api.put(`/dbs/${connEditId.value}`, connForm.value)
      ElMessage.success('已保存')
    } else {
      const r = await api.post('/dbs', connForm.value)
      ElMessage.success('已创建')
      connDlg.value = false
      await loadConns(r.id)
      return
    }
    connDlg.value = false
    await loadConns(connEditId.value)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    connSaving.value = false
  }
}

async function removeConn(c) {
  try {
    await ElMessageBox.confirm(`删除连接「${c.name}」？其全部备份文件将一并删除。`, '删除确认', { type: 'warning' })
  } catch { return }
  try {
    await api.del(`/dbs/${c.id}`)
    if (curId.value === c.id) { curId.value = null }
    await loadConns()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 小工具 ----
function fmtSize(n) {
  if (n == null) return '-'
  if (n < 1024) return n + ' B'
  if (n < 1048576) return (n / 1024).toFixed(1) + ' KB'
  if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB'
  return (n / 1073741824).toFixed(2) + ' GB'
}
function fmtTime(ts) {
  if (!ts) return '-'
  const d = new Date(ts * 1000)
  const p = (x) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
const typeLabel = { mysql: 'MySQL', postgres: 'PostgreSQL', sqlite: 'SQLite' }

watch(curId, () => selectConn())
watch(mainTab, (t) => { if (t === 'backup') loadBackups() })
// 查询页打开/换连接/换库时刷新补全元数据
watch([curId, curSchema, mainTab], () => { if (mainTab.value === 'console') loadColMap() })

onMounted(() => {
  loadConns() // 打开不自动选中/连库（341）；用户选定连接后才触发 watch → selectConn 连库
  loadServers()
})
</script>

<template>
  <div class="dbv">
    <el-alert class="dbv-notice" type="warning" :closable="false" show-icon
      title="此工具仅对数据库进行查询，无写入操作。" />
    <!-- 顶栏：连接选择与管理 -->
    <div class="dbv-top">
      <el-select v-model="curId" :placeholder="conns.length ? '选择数据库连接' : '无数据库'" :disabled="!conns.length" class="dbv-conn-sel" size="default" popper-class="dbv-conn-popper">
        <el-option v-for="c in conns" :key="c.id" :value="c.id" :label="c.name">
          <span class="dbv-conn-opt">
            <span class="dbv-conn-name">{{ c.name }}</span>
            <span class="dbv-conn-meta">{{ typeLabel[c.type] || c.type }}<template v-if="c.host"> · {{ c.host }}</template><template v-if="c.readonly"> · 只读</template></span>
          </span>
        </el-option>
      </el-select>
      <template v-if="cur">
        <el-button size="small" @click="openConnEdit(cur)">编辑</el-button>
        <el-button size="small" type="danger" plain @click="removeConn(cur)">删除</el-button>
      </template>
      <el-button size="small" @click="openCreate">新建连接</el-button>
      <el-tag v-if="cur?.readonly" type="warning" size="small" effect="plain">只读</el-tag>
      <el-tag v-if="cur?.last_error" type="danger" size="small" effect="plain" :title="cur.last_error">上次出错</el-tag>
    </div>

    <div v-if="!cur" class="dbv-empty">
      <template v-if="conns.length">
        <p class="dbv-empty-h">未选择数据库连接</p>
        <p class="dbv-empty-p">在上方下拉选定数据库连接后开始（选定后才连接数据库）。</p>
      </template>
      <template v-else>
        <p class="dbv-empty-h">还没有数据库连接</p>
        <p class="dbv-empty-p">支持 MySQL / PostgreSQL / SQLite，可经监控服务器 SSH 隧道连内网库。</p>
      </template>
      <el-button type="primary" @click="openCreate">新建连接</el-button>
    </div>

    <el-tabs v-else v-model="mainTab" class="dbv-tabs">
      <!-- 数据浏览 -->
      <el-tab-pane label="数据浏览" name="browse" lazy>
        <div class="dbv-main">
          <div v-show="!side.collapsed" class="dbv-side" :style="{ width: side.width + 'px' }" v-loading="treeLoading">
            <div v-if="schemas.length > 1" class="dbv-schemas">
              <el-select v-model="curSchema" size="small" @change="loadTables">
                <el-option v-for="s in schemas" :key="s" :value="s" :label="s" />
              </el-select>
            </div>
            <div v-else-if="schemas.length === 1" class="dbv-schema-name">{{ schemas[0] }}</div>
            <div v-for="t in tables" :key="t.name" :class="['dbv-tbl', { active: t.name === curTable }]" @click="selectTable(t.name)">
              <span class="dbv-tbl-icon">{{ t.view ? '◇' : '▤' }}</span>
              <span class="dbv-tbl-name">{{ t.name }}</span>
              <span v-if="!t.no_count && t.rows >= 0" class="dbv-tbl-rows">{{ t.rows }}</span>
            </div>
            <p v-if="!tables.length && !treeLoading" class="dbv-side-empty">该库暂无表</p>
          </div>
          <div class="side-divider" :class="{ on: side.resizing }" title="拖拽调节宽度 · 单击收起/展开" @mousedown="side.startDrag" />

          <div class="dbv-content">
            <template v-if="curTable">
              <div class="dbv-tbl-head">
                <span class="dbv-tbl-title">{{ curTable }}</span>
                <el-tag v-if="detail && !detail.pk.length" type="info" size="small" effect="plain">无主键 · 只读</el-tag>
              </div>
              <el-tabs v-model="tableTab" class="dbv-subtabs">
                <el-tab-pane label="数据" name="data">
                  <div class="dbv-filter">
                    <el-select v-model="fCol" size="small" class="dbv-filter-col" placeholder="字段">
                      <el-option v-for="c in detail?.columns || []" :key="c.name" :value="c.name" :label="c.name" />
                    </el-select>
                    <el-select v-model="fOp" size="small" class="dbv-filter-op">
                      <el-option value="like" label="包含" />
                      <el-option value="eq" label="=" />
                      <el-option value="ne" label="≠" />
                      <el-option value="gt" label=">" />
                      <el-option value="ge" label="≥" />
                      <el-option value="lt" label="<" />
                      <el-option value="le" label="≤" />
                      <el-option value="isnull" label="为空" />
                      <el-option value="isnotnull" label="非空" />
                    </el-select>
                    <el-input v-if="fOp !== 'isnull' && fOp !== 'isnotnull'" v-model="fVal" size="small" class="dbv-filter-val" placeholder="值" @keyup.enter="applyFilter" />
                    <el-button size="small" @click="applyFilter">筛选</el-button>
                  </div>
                  <div v-loading="rowsLoading" class="dbv-grid-wrap">
                    <el-table v-if="rowsData" :data="gridRows" size="small" border height="100%" @sort-change="onSortChange" @row-click="openRowDetail">
                      <el-table-column type="index" width="46" />
                      <el-table-column v-for="col in rowsData.columns" :key="col" :prop="col" :label="col" min-width="150" sortable="custom" show-overflow-tooltip>
                        <template #default="{ row }">
                          <span v-if="row[col] === null" class="dbv-null">NULL</span>
                          <span v-else>{{ row[col] }}</span>
                        </template>
                      </el-table-column>
                      <el-table-column v-if="canEdit && !cur?.readonly && !writesBlocked" label="操作" width="110" fixed="right">
                        <template #default="{ row }">
                          <el-button link type="primary" size="small" @click.stop="openEdit(row)">编辑</el-button>
                          <el-button link type="danger" size="small" @click.stop="deleteRow(row)">删除</el-button>
                        </template>
                      </el-table-column>
                    </el-table>
                  </div>
                  <el-pagination v-if="rowsData" class="dbv-page" layout="total, prev, pager, next, sizes"
                    :total="Number(rowsData.total)" :current-page="page" :page-size="size"
                    :page-sizes="[10, 20, 50]" @current-change="(p) => { page = p; loadRows() }"
                    @size-change="(s) => { size = s; page = 1; loadRows() }" />
                </el-tab-pane>

                <el-tab-pane label="结构" name="struct">
                  <el-table v-if="detail" :data="detail.columns" size="small" border>
                    <el-table-column prop="name" label="字段" min-width="140" />
                    <el-table-column prop="type" label="类型" min-width="120" />
                    <el-table-column label="可空" width="70">
                      <template #default="{ row }">{{ row.nullable ? '是' : '否' }}</template>
                    </el-table-column>
                    <el-table-column prop="default" label="默认值" min-width="100">
                      <template #default="{ row }"><span v-if="row.default" class="dbv-mono">{{ row.default }}</span><span v-else class="dbv-null">NULL</span></template>
                    </el-table-column>
                    <el-table-column label="自增" width="70">
                      <template #default="{ row }">{{ row.auto_inc ? '是' : '' }}</template>
                    </el-table-column>
                    <el-table-column prop="comment" label="注释" min-width="140" />
                  </el-table>
                </el-tab-pane>

                <el-tab-pane label="索引" name="index">
                  <el-table v-if="detail" :data="detail.indexes || []" size="small" border>
                    <el-table-column prop="name" label="名称" min-width="160" />
                    <el-table-column label="类型" width="90">
                      <template #default="{ row }">
                        <el-tag v-if="row.primary" type="warning" size="small">主键</el-tag>
                        <el-tag v-else-if="row.unique" type="success" size="small">唯一</el-tag>
                        <el-tag v-else size="small" type="info" effect="plain">普通</el-tag>
                      </template>
                    </el-table-column>
                    <el-table-column label="字段" min-width="200">
                      <template #default="{ row }">{{ (row.columns || []).join(', ') }}</template>
                    </el-table-column>
                  </el-table>
                  <p v-else class="dbv-side-empty">-</p>
                </el-tab-pane>

                <el-tab-pane label="DDL" name="ddl">
                  <pre class="dbv-ddl">{{ detail?.ddl || '-' }}</pre>
                </el-tab-pane>
              </el-tabs>
            </template>
            <div v-else class="dbv-empty dbv-content-empty">
              <p class="dbv-empty-p">从左侧选择一张表</p>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <!-- 查询（原 SQL 控制台，338 改名） -->
      <el-tab-pane label="查询" name="console" lazy>
        <div class="dbv-console">
          <div class="dbv-console-bar">
            <el-select v-if="cur && cur.type !== 'sqlite'" v-model="curSchema" size="small" class="dbv-console-db"
              placeholder="库" @change="onConsoleSchemaChange">
              <el-option v-for="s in schemas" :key="s" :value="s" :label="s" />
            </el-select>
            <el-button type="primary" size="small" :loading="running" @click="runSql(false)">执行</el-button>
            <el-dropdown v-if="hist.length" trigger="click" @command="(s) => (sqlText = s)">
              <el-button size="small">历史 ({{ hist.length }})</el-button>
              <template #dropdown>
                <el-dropdown-menu class="dbv-hist-menu">
                  <el-dropdown-item v-for="(s, i) in hist" :key="i" :command="s" class="dbv-hist-item">{{ s.slice(0, 80) }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
            <span v-if="writesBlocked" class="dbv-ro-hint">仅允许查询语句（SELECT / SHOW / EXPLAIN 等）</span>
            <span v-else-if="cur?.readonly" class="dbv-ro-hint">只读连接：仅允许 SELECT / SHOW / EXPLAIN</span>
          </div>
          <div class="dbv-editor">
            <CodeEditor v-model="sqlText" language="sql" @ready="onEditorReady" />
          </div>
          <div v-if="consoleRes" class="dbv-cres">
            <div class="dbv-cres-meta">
              <template v-if="consoleRes.columns">
                <span>{{ consoleRes.rows.length }} 行<template v-if="consoleRes.truncated">（已截断）</template></span>
              </template>
              <template v-else>
                <span>影响 {{ consoleRes.affected }} 行</span>
              </template>
              <span>耗时 {{ consoleRes.elapsed_ms }} ms</span>
            </div>
            <el-table v-if="consoleRes.columns" :data="consoleRes.rows.map(r => Object.fromEntries(consoleRes.columns.map((c, i) => [c, r[i]])))" size="small" border class="dbv-grid" max-height="420">
              <el-table-column v-for="col in consoleRes.columns" :key="col" :prop="col" :label="col" min-width="140" show-overflow-tooltip>
                <template #default="{ row }">
                  <span v-if="row[col] === null" class="dbv-null">NULL</span>
                  <span v-else>{{ row[col] }}</span>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </el-tab-pane>

      <!-- 备份与导入 -->
      <el-tab-pane label="备份 / 导入" name="backup" lazy>
        <div class="dbv-backup">
          <div class="dbv-backup-bar">
            <el-button type="primary" size="small" :loading="backing" @click="createBackup">备份整库</el-button>
            <el-button size="small" :loading="importing" :disabled="writesBlocked" @click="pickImport">导入 SQL 文件…</el-button>
            <input ref="fileInput" type="file" accept=".sql,.gz" class="dbv-hidden" @change="onImportFile" />
            <span class="dbv-backup-note">备份为 INSERT 语句的 .sql.gz，落服务端 {{ '{数据目录}' }}/dbbackups/{{ cur?.id }}/；导入出错即停。</span>
          </div>
          <el-table :data="backups" size="small" border>
            <el-table-column prop="name" label="文件" min-width="240">
              <template #default="{ row }"><span class="dbv-mono">{{ row.name }}</span></template>
            </el-table-column>
            <el-table-column label="大小" width="100">
              <template #default="{ row }">{{ fmtSize(row.size) }}</template>
            </el-table-column>
            <el-table-column label="时间" width="160">
              <template #default="{ row }">{{ fmtTime(row.mtime) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="140">
              <template #default="{ row }">
                <el-button link type="primary" size="small" @click="downloadBackup(row)">下载</el-button>
                <el-button link type="danger" size="small" @click="deleteBackup(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </el-tab-pane>
    </el-tabs>

    <!-- 连接编辑对话框 -->
    <el-dialog v-model="connDlg" :title="connEditId ? '编辑连接' : '新建连接'" width="480px" destroy-on-close :before-close="connGuard.beforeClose" append-to-body>
      <el-form label-width="92px" size="default" class="dbv-conn-form">
        <el-form-item label="名称">
          <el-input v-model="connForm.name" placeholder="如：生产 MySQL" />
        </el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="connForm.type" :disabled="connEditId > 0">
            <el-radio-button value="mysql">MySQL</el-radio-button>
            <el-radio-button value="postgres">PostgreSQL</el-radio-button>
            <el-radio-button value="sqlite">SQLite</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <template v-if="connForm.type !== 'sqlite'">
          <el-form-item label="主机">
            <el-input v-model="connForm.host" placeholder="如 127.0.0.1" />
          </el-form-item>
          <el-form-item label="端口">
            <el-input-number v-model="connForm.port" :min="0" :max="65535" :placeholder="connForm.type === 'mysql' ? '3306' : '5432'" controls-position="right" class="dbv-w120" />
          </el-form-item>
        </template>
        <el-form-item :label="connForm.type === 'sqlite' ? '库文件路径' : '数据库'">
          <el-input v-model="connForm.database" :placeholder="connForm.type === 'sqlite' ? '/data/app.db（服务器上的绝对路径）' : (connForm.type === 'postgres' ? '必填' : '默认连接的库，可空')" />
        </el-form-item>
        <template v-if="connForm.type !== 'sqlite'">
          <el-form-item label="用户名">
            <el-input v-model="connForm.username" autocomplete="off" />
          </el-form-item>
          <el-form-item label="密码">
            <el-input v-model="connForm.password" type="password" show-password autocomplete="new-password" :placeholder="connEditId ? '留空沿用原密码' : ''" />
          </el-form-item>
          <el-form-item label="SSH 隧道">
            <el-select v-model="connForm.tunnel_server_id" class="dbv-wfull">
              <el-option :value="0" label="直连" />
              <el-option v-for="s in servers" :key="s.id" :value="s.id" :label="`经 ${s.name} 隧道`" />
            </el-select>
          </el-form-item>
          <el-form-item label="TLS">
            <el-select v-model="connForm.ssl" class="dbv-w120">
              <el-option value="off" label="关闭" />
              <el-option value="skip-verify" label="加密（不验证书）" />
              <el-option value="on" label="加密（验证证书）" />
            </el-select>
          </el-form-item>
        </template>
        <el-form-item label="连接超时">
          <el-input-number v-model="connForm.timeout_s" :min="1" :max="60" controls-position="right" class="dbv-w120" />
          <span class="dbv-form-note">秒</span>
        </el-form-item>
        <el-form-item label="只读">
          <el-switch v-model="connForm.readonly" />
          <span class="dbv-form-note">开启后服务端拒绝一切写操作</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button :loading="testing" @click="testConnForm">测试连接</el-button>
        <el-button @click="connDlg = false">取消</el-button>
        <el-button type="primary" :loading="connSaving" @click="saveConn">保存（先测后存）</el-button>
      </template>
    </el-dialog>

    <!-- 行数据明细 -->
    <el-dialog v-model="rowDetailDlg" :title="`行数据明细 · ${curTable || ''}`" width="640px" destroy-on-close append-to-body>
      <div class="dbv-rowdetail">
        <div v-for="c in rowsData?.columns || []" :key="c" class="dbv-rowdetail-item">
          <div class="dbv-rowdetail-k">{{ c }}</div>
          <div class="dbv-rowdetail-v">
            <span v-if="rowDetailRow && rowDetailRow[c] === null" class="dbv-null">NULL</span>
            <span v-else-if="rowDetailRow" class="dbv-mono">{{ rowDetailRow[c] }}</span>
          </div>
        </div>
      </div>
      <template #footer>
        <el-button @click="rowDetailDlg = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.dbv { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.dbv-notice { margin-bottom: 10px; flex: none; }
.dbv-top { display: flex; align-items: center; gap: 8px; padding: 2px 0 12px; flex-wrap: wrap; }
.dbv-conn-sel { width: 260px; }
.dbv-conn-opt { display: flex; flex-direction: column; line-height: 1.3; }
.dbv-conn-name { font-weight: 600; }
.dbv-conn-meta { font-size: 12px; color: var(--muted); }
.dbv-w120 { width: 120px; }
.dbv-wfull { width: 100%; }

.dbv-tabs { flex: 1; min-height: 0; display: flex; flex-direction: column; }
.dbv-tabs > :deep(.el-tabs__content) { flex: 1; min-height: 0; }
.dbv-tabs > :deep(.el-tabs__content > .el-tab-pane) { height: 100%; }

/* 分栏间隔=分隔条自身 8px（文档 .side-divider 同口径，353） */
.dbv-main { display: flex; gap: 0; height: 100%; min-height: 0; }
.dbv-side { width: 220px; flex: none; overflow: auto; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 8px; }
.dbv-schemas { padding: 2px 2px 8px; }
.dbv-schemas .el-select { width: 100%; }
.dbv-schema-name { font-size: 12px; font-weight: 700; color: var(--muted); padding: 4px 6px 8px; }
.dbv-tbl { display: flex; align-items: center; gap: 6px; padding: 6px 8px; border-radius: 6px; cursor: pointer; font-size: 13px; color: var(--ink); }
.dbv-tbl:hover { background: var(--el-fill-color-light); }
.dbv-tbl.active { background: var(--brand); color: #fff; }
.dbv-tbl.active .dbv-tbl-rows { color: rgba(255, 255, 255, .75); }
.dbv-tbl-icon { flex: none; opacity: .7; font-size: 12px; }
.dbv-tbl-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dbv-tbl-rows { flex: none; font-size: 11px; color: var(--muted); }
.dbv-side-empty { font-size: 12px; color: var(--muted); text-align: center; padding: 16px 0; }

.dbv-content { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.dbv-content-empty { flex: 1; }
.dbv-tbl-head { display: flex; align-items: center; gap: 8px; padding-bottom: 6px; }
.dbv-tbl-title { font-size: 15px; font-weight: 700; color: var(--ink); }
.dbv-subtabs { flex: 1; min-height: 0; display: flex; flex-direction: column; }
.dbv-subtabs > :deep(.el-tabs__content) { flex: 1; min-height: 0; overflow: hidden; }
.dbv-subtabs > :deep(.el-tabs__content > .el-tab-pane) { height: 100%; overflow: auto; box-sizing: border-box; }
.dbv-subtabs > :deep(.el-tabs__content > #pane-data) { display: flex; flex-direction: column; overflow: hidden; }

.dbv-filter { flex: none; display: flex; align-items: center; gap: 6px; padding: 2px 0 8px; flex-wrap: wrap; }
.dbv-filter-col { width: 150px; }
.dbv-filter-op { width: 92px; }
.dbv-filter-val { width: 160px; }

.dbv-null { color: var(--muted); font-style: italic; font-size: 12px; opacity: .8; }
.dbv-mono { font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 12.5px; }
.dbv-grid-wrap { flex: 1; min-height: 0; }
.dbv-grid-wrap :deep(.el-table__row) { cursor: pointer; }
/* 行数据明细弹层 */
.dbv-rowdetail { max-height: 60vh; overflow: auto; }
.dbv-rowdetail-item { display: flex; gap: 12px; padding: 7px 2px; border-bottom: 1px dashed var(--border); }
.dbv-rowdetail-item:last-child { border-bottom: none; }
.dbv-rowdetail-k { flex: none; width: 170px; font-size: 12.5px; font-weight: 600; color: var(--muted); word-break: break-all; }
.dbv-rowdetail-v { flex: 1; min-width: 0; font-size: 12.5px; white-space: pre-wrap; word-break: break-all; user-select: text; }
.dbv-page { flex: none; padding: 10px 2px 0; justify-content: flex-end; }

.dbv-ddl {
  margin: 0; padding: 12px 14px; border: 1px solid var(--border); border-radius: 8px;
  background: var(--el-fill-color-light); font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 12.5px; white-space: pre-wrap; word-break: break-all; line-height: 1.7;
}

.dbv-console { display: flex; flex-direction: column; gap: 8px; height: 100%; min-height: 0; }
.dbv-console-bar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.dbv-console-db { width: 200px; }
.dbv-ro-hint { font-size: 12px; color: var(--muted); }
.dbv-editor { height: 220px; border: 1px solid var(--border); border-radius: 8px; overflow: hidden; flex: none; }
.dbv-cres { flex: 1; min-height: 0; overflow: auto; }
.dbv-cres-meta { display: flex; gap: 14px; font-size: 12px; color: var(--muted); padding: 4px 0; }
.dbv-hist-item { font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 12px; max-width: 480px; overflow: hidden; text-overflow: ellipsis; }

.dbv-backup-bar { display: flex; align-items: center; gap: 8px; padding-bottom: 10px; flex-wrap: wrap; }
.dbv-backup-note { font-size: 12px; color: var(--muted); }
.dbv-hidden { display: none; }

.dbv-empty { text-align: center; padding: 60px 0 30px; }
.dbv-empty-h { font-size: 16px; font-weight: 700; color: var(--ink); margin: 0 0 8px; }
.dbv-empty-p { font-size: 13px; color: var(--muted); margin: 0 0 16px; }

.dbv-conn-form :deep(.el-form-item) { margin-bottom: 12px; }
.dbv-form-note { font-size: 12px; color: var(--muted); margin-left: 8px; }

@media (max-width: 768px) {
  .dbv-main { flex-direction: column; }
  /* 手机端：分隔条已由全局样式隐藏，树忽略拖拽宽度/收起状态强制全宽显示 */
  .dbv-side { width: 100% !important; display: block !important; max-height: 200px; }
  .dbv-editor { height: 160px; }
}
</style>

<style>
/* 连接下拉 popper 传送挂 body，样式须非 scoped：选项两行内容（名称+类型主机）
   卡片化——高度自适应 + 上下留白拉开间距，多个连接好区分（345） */
.dbv-conn-popper .el-select-dropdown { padding: 6px; }
.dbv-conn-popper .el-select-dropdown__item {
  height: auto;
  line-height: 1.35;
  padding: 7px 12px;
  margin: 4px 0;
  border-radius: 8px;
}
</style>
