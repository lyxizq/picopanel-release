<template>
  <div class="docs-page">
    <!-- 左栏：分组折叠树（多开，组即折叠项、文档为叶子，点击文档开编辑 tab）。
         宽度可拖拽分隔条调节、单击分隔条收起/展开（useSidePanel 记忆） -->
    <div v-if="!isMobile" v-show="!side.collapsed" class="docs-side" :style="{ width: side.width + 'px' }">
      <div class="side-actions">
        <el-button size="small" @click="createGroup">{{ $t('docs.newGroup') }}</el-button>
        <el-button size="small" type="primary" plain @click="newDlg = true">{{ $t('docs.newDoc') }}</el-button>
      </div>
      <el-scrollbar class="side-scroll">
        <el-collapse v-model="openGroups" class="doc-collapse" @change="onCollapse">
          <el-collapse-item v-for="g in groupList" :key="g.id" :name="String(g.id)">
            <template #title>
              <div
                class="g-row"
                :class="{ 'grp-over': docDrag.from >= 0 && docDrag.gid !== g.id && docDrag.overGrp === g.id }"
                @dragover="grpDragOver(g.id, $event)"
                @dragleave="grpDragLeave(g.id, $event)"
                @drop="grpDragDrop(g.id)"
              >
                <span class="g-accent" :style="{ background: grpColor(g.id) }" />
                <el-icon class="g-icon" :style="{ color: grpColor(g.id) }"><FolderOpened /></el-icon>
                <span class="g-name" :title="g.name">{{ g.name }}</span>
                <span class="g-count">{{ docsOf(g.id).length || '' }}</span>
                <span v-if="g.id !== 0" class="g-ops" @click.stop>
                  <el-icon :title="$t('docs.rename')" @click="renameGroup(g)"><Edit /></el-icon>
                  <el-icon :title="$t('docs.deleteGroup')" @click="deleteGroup(g)"><Delete /></el-icon>
                </span>
              </div>
            </template>
            <div
              v-for="(d, i) in docsOf(g.id)"
              :key="d.id"
              class="doc-row"
              :class="{ dragging: docDrag.from === i && docDrag.gid === g.id, 'drag-over': docDrag.over === i && docDrag.gid === g.id && docDrag.from !== i }"
              draggable="true"
              @click="openDoc(d)"
              @dragstart="docDragStart(i, g.id, $event)"
              @dragover="docDragOver(i, g.id, $event)"
              @drop="docDragDrop(i, g.id)"
              @dragend="docDragEnd"
            >
              <el-icon class="d-icon">
                <Document v-if="d.format === 'richtext'" />
                <Memo v-else />
              </el-icon>
              <div class="d-main">
                <div class="d-title">{{ d.title }}</div>
                <div class="d-time">{{ fmtBytes(d.size || 0) }} · {{ fmtAgo(d.updated_at) }}</div>
              </div>
              <el-icon class="d-del" :title="$t('docs.moveToTrash')" @click.stop="deleteDoc(d)"><Delete /></el-icon>
            </div>
            <div v-if="!docsOf(g.id).length" class="side-empty">{{ $t('docs.noDocs') }}</div>
          </el-collapse-item>
        </el-collapse>
      </el-scrollbar>
      <el-button class="trash-btn" size="small" @click="openTrash">
        <el-icon class="op-icon"><Delete /></el-icon>{{ $t('docs.trash') }}<template v-if="trashCount">{{ $t('docs.trashCountSuffix', { n: trashCount }) }}</template>
      </el-button>
    </div>
    <div
      v-if="!isMobile"
      class="side-divider"
      :class="{ on: side.resizing }"
      :title="side.collapsed ? $t('docs.expandSideTip') : $t('docs.sideTip')"
      @mousedown="side.startDrag"
    />

    <!-- 手机端：分组/文档下拉 -->
    <div v-else class="mobile-bar">
      <el-select v-model="selGroup" size="small" style="flex: 1">
        <el-option v-for="g in groupList" :key="g.id" :label="g.name" :value="g.id" />
      </el-select>
      <el-select :model-value="null" size="small" style="flex: 1.4" :placeholder="$t('docs.openDocPh')" @change="openDocById">
        <el-option v-for="d in docsOf(selGroup)" :key="d.id" :label="d.title" :value="d.id" />
      </el-select>
      <el-button size="small" @click="createGroup">{{ $t('docs.group') }}</el-button>
      <el-button size="small" type="primary" plain @click="newDlg = true">{{ $t('docs.new') }}</el-button>
      <el-button size="small" @click="openTrash">
        <el-icon><Delete /></el-icon>
      </el-button>
    </div>

    <!-- 右侧：内嵌 tab——「列表」+ 已打开的文档（一篇一个 tab，keep-alive 保编辑态） -->
    <div class="docs-main">
      <div v-if="openDocs.length" class="doc-tabs">
        <div class="dtab" :class="{ active: activeId === null }" @click="activeId = null">
          <el-icon class="t-icon"><Folder /></el-icon>{{ $t('docs.list') }}
        </div>
        <div
          v-for="t in openDocs"
          :key="t.id"
          class="dtab"
          :class="{ active: activeId === t.id }"
          :title="t.title + $t('docs.dblCloseSuffix')"
          @click="activeId = t.id"
          @dblclick="closeDoc(t.id)"
        >
          <span class="t-title">{{ t.title }}</span>
          <el-icon class="t-close" @click.stop="closeDoc(t.id)"><Close /></el-icon>
        </div>
      </div>

      <!-- 列表面板（无打开文档时不渲染 tab 栏，直接列表） -->
      <template v-if="activeId === null">
        <template v-if="curGroupDocs.length">
          <div class="list-head">
            <span class="list-title">{{ curGroupName }}</span>
            <span class="list-count">{{ $t('docs.docsN', { n: curGroupDocs.length }) }}</span>
          </div>
          <el-scrollbar class="list-scroll">
            <div
              v-for="(d, i) in curGroupDocs"
              :key="d.id"
              class="list-row"
              :class="{ dragging: docDrag.from === i && docDrag.gid === selGroup, 'drag-over': docDrag.over === i && docDrag.gid === selGroup && docDrag.from !== i }"
              draggable="true"
              @click="openDoc(d)"
              @dragstart="docDragStart(i, selGroup, $event)"
              @dragover="docDragOver(i, selGroup, $event)"
              @drop="docDragDrop(i, selGroup)"
              @dragend="docDragEnd"
            >
              <el-icon class="row-icon" :style="{ color: grpColor(selGroup) }">
                <Document v-if="d.format === 'richtext'" />
                <Memo v-else />
              </el-icon>
              <div class="row-main">
                <div class="row-title">{{ d.title }}</div>
                <div class="row-sub">{{ d.format === 'md' ? 'Markdown' : $t('docs.richtext') }} · {{ fmtBytes(d.size || 0) }} · {{ $t('docs.updatedAt') }} {{ fmtAgo(d.updated_at) }}</div>
              </div>
              <el-tag size="small" :type="d.format === 'md' ? 'info' : 'warning'" effect="plain" class="row-tag">
                {{ d.format === 'md' ? 'MD' : 'RT' }}
              </el-tag>
              <el-button size="small" text type="primary" class="row-del" @click.stop="openMove(d)">{{ $t('docs.move') }}</el-button>
              <el-button size="small" text type="danger" class="row-del" @click.stop="deleteDoc(d)">{{ $t('docs.delete') }}</el-button>
            </div>
          </el-scrollbar>
        </template>
        <div v-else class="main-empty">
          <el-icon :size="42"><Notebook /></el-icon>
          <p>{{ groups.length ? $t('docs.emptyGroupHint') : $t('docs.emptyAllHint') }}</p>
        </div>
      </template>

      <!-- 编辑面板：keep-alive 缓存最近 12 个实例，切 tab/回列表不丢编辑态；
           事件只可能来自当前活动实例（loadDoc/onActivated 均在其激活时跑），
           直接绑 activeId 即可 -->
      <div v-else class="doc-panel">
        <KeepAlive :max="12">
          <DocEditView :key="activeId" :doc-id="activeId" :groups="groupList" @renamed="(t) => onRenamed(activeId, t)" @deleted="load()" @moved="load()" @close="closeDoc(activeId)" />
        </KeepAlive>
      </div>
    </div>

    <!-- 新建文档弹窗：标题 + 格式 -->
    <el-dialog append-to-body v-model="newDlg" :title="$t('docs.newDoc')" width="420px" :before-close="newGuard.beforeClose" @open="openNewDoc" @closed="resetNew">
      <el-form label-width="70px" @submit.prevent>
        <el-form-item :label="$t('docs.labelTitle')">
          <el-input v-model="newForm.title" maxlength="200" :placeholder="$t('docs.titlePh')" @keydown.enter="createDoc" />
        </el-form-item>
        <el-form-item :label="$t('docs.labelFormat')">
          <el-radio-group v-model="newForm.format">
            <el-radio value="richtext">{{ $t('docs.richtextWysiwyg') }}</el-radio>
            <el-radio value="md">Markdown</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="$t('docs.group')">
          <el-select v-model="newForm.groupID" style="width: 100%">
            <el-option v-for="g in groupList" :key="g.id" :label="g.name" :value="g.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="newDlg = false">{{ $t('docs.cancel') }}</el-button>
        <el-button type="primary" :loading="creating" @click="createDoc">{{ $t('docs.createAndOpen') }}</el-button>
      </template>
    </el-dialog>

    <!-- 移动分组弹窗（2026-10-19）：拖拽之外的显式换组入口，复用 /docs/move -->
    <el-dialog append-to-body v-model="moveDlg.show" :title="$t('docs.moveGroupTitle')" width="380px">
      <el-select v-model="moveDlg.groupID" style="width: 100%">
        <el-option v-for="g in groupList" :key="g.id" :label="g.name" :value="g.id" />
      </el-select>
      <template #footer>
        <el-button @click="moveDlg.show = false">{{ $t('docs.cancel') }}</el-button>
        <el-button type="primary" :disabled="!moveDlg.doc || moveDlg.groupID === moveDlg.doc.group_id" @click="doMove">{{ $t('docs.move') }}</el-button>
      </template>
    </el-dialog>

    <!-- 回收站：删除的文档先进这里，可恢复、彻底删除或一键清空 -->
    <el-drawer append-to-body v-model="trashDlg" size="440px">
      <template #header>
        <div class="trash-head">
          <span>{{ $t('docs.trash') }}<template v-if="trashCount">{{ $t('docs.trashCountSuffix', { n: trashCount }) }}</template></span>
          <el-button size="small" text type="danger" :disabled="!trash.length" @click="emptyTrash">
            {{ $t('docs.emptyTrash') }}
          </el-button>
        </div>
      </template>
      <div v-if="!trash.length" class="trash-empty">{{ $t('docs.trashEmpty') }}</div>
      <div v-for="d in trash" :key="d.id" class="trash-row">
        <el-icon class="row-icon">
          <Document v-if="d.format === 'richtext'" />
          <Memo v-else />
        </el-icon>
        <div class="row-main">
          <div class="row-title">{{ d.title }}</div>
          <div class="row-sub">{{ $t('docs.deletedAt') }} {{ fmtTime(d.deleted_at) }} · {{ d.format === 'md' ? 'Markdown' : $t('docs.richtext') }}</div>
        </div>
        <el-button size="small" text type="primary" @click="restoreDoc(d)">{{ $t('docs.restore') }}</el-button>
        <el-button size="small" text type="danger" @click="hardDelete(d)">{{ $t('docs.hardDelete') }}</el-button>
      </div>
    </el-drawer>
  </div>
</template>

<script setup>
// 文档管理页：左栏分组折叠树，右侧内嵌 tab——「列表」+ 已打开的文档编辑器
//（一篇一个 tab，keep-alive 保编辑态，状态随 localStorage 持久化）。
// 删除进回收站（软删，可恢复/彻底删/一键清空）。
defineOptions({ name: 'DocsView' })
import { computed, defineAsyncComponent, onActivated, onMounted, ref, watch } from 'vue'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Close, Delete, Document, Edit, Folder, FolderOpened, Memo, Notebook } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { grpColor } from '../utils/chartTheme.js'
import { fmtBytes, fmtTime } from '../utils/format.js'
import { useIsMobile } from '../composables/useIsMobile.js'
import { useSidePanel } from '../composables/useSidePanel.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/docs.js'
import doceditMsgs from '../i18n/pages/docedit.js'

const route = useRoute()
const router = useRouter()
// 本文件多处用 t 作局部变量（tab 项），i18n 函数别名 tr 避让
const { t: tr } = useI18n()
registerPageMessages(pageMsgs)
// docedit 键由懒加载的 DocEditView 注册，但本页 restoreTabs 在 setup 同步用到
// docedit.doc（tab 兜底标题），这里提前并注册一次（merge 幂等，重复无害）
registerPageMessages(doceditMsgs)

// 编辑器（wangeditor/vditor）保持独立懒加载 chunk：首次打开文档 tab 才拉取
const DocEditView = defineAsyncComponent(() => import('./DocEditView.vue'))

const groups = ref([])
const docs = ref([])
const selGroup = ref(0) // 当前分组：右侧列表 + 新建文档的默认去处
const openGroups = ref([]) // 左栏折叠树展开的分组（el-collapse v-model，字符串 name）
let treeInited = false
const newDlg = ref(false)
const creating = ref(false)
const newForm = ref({ title: '', format: 'richtext', groupID: 0 })

// ---------- 内嵌文档 tab ----------

const openDocs = ref([]) // [{ id, title }]，打开顺序即 tab 顺序
const activeId = ref(null) // null = 列表面板；数字 = 激活的文档 tab
const TABS_KEY = 'lz-doc-tabs'

function saveTabs() {
  try {
    localStorage.setItem(TABS_KEY, JSON.stringify({ docs: openDocs.value, active: activeId.value }))
  } catch {
    /* 隐私模式等存储失败可忽略 */
  }
}

// 恢复上次会话打开的文档 tab（文档可能已被删：编辑器加载落到空态可关闭，
// 或切回该 tab 时被存活校验自动关闭，均无需在此预检）
function restoreTabs() {
  try {
    const raw = JSON.parse(localStorage.getItem(TABS_KEY) || 'null')
    if (!raw || !Array.isArray(raw.docs)) return
    openDocs.value = raw.docs
      .filter((t) => t && Number(t.id) > 0)
      .slice(0, 12)
      .map((t) => ({ id: Number(t.id), title: String(t.title || tr('docedit.doc')).slice(0, 200) }))
    activeId.value = openDocs.value.some((t) => t.id === raw.active) ? Number(raw.active) : null
  } catch {
    /* 数据损坏则当作没有 */
  }
}
restoreTabs()

// 打开文档 = 开/激活内嵌 tab（同一文档重复点击只激活已有 tab）；
// d 可为树里的文档元数据，也可为仅 { id, title }（旧路由 /docs/:id 跳转，树未必已加载）
function openDoc(d) {
  const t = openDocs.value.find((x) => x.id === d.id)
  if (t) {
    if (d.title && t.title !== d.title) t.title = d.title
  } else {
    openDocs.value.push({ id: d.id, title: d.title || tr('docs.tabFallback', { id: d.id }) })
  }
  activeId.value = d.id
  saveTabs()
}

function openDocById(id) {
  const d = docs.value.find((x) => x.id === id)
  openDoc(d || { id })
}

function closeDoc(id) {
  const i = openDocs.value.findIndex((t) => t.id === id)
  if (i < 0) return
  openDocs.value.splice(i, 1)
  // 关的是当前 tab：激活相邻（浏览器习惯取右侧），全关完回列表
  if (activeId.value === id) activeId.value = openDocs.value[i]?.id ?? null
  saveTabs()
}

// 编辑器加载/改名后同步 tab 文案
function onRenamed(id, title) {
  const t = openDocs.value.find((x) => x.id === id)
  if (t && title && t.title !== title) {
    t.title = title
    saveTabs()
  }
}

const trashDlg = ref(false)
const trash = ref([])

const isMobile = useIsMobile()
const side = useSidePanel('docs', 240) // 左栏宽度/收起，localStorage 记忆

// 「未分组」固定排最后（id=0）
const groupList = computed(() => [...groups.value, { id: 0, name: tr('docs.ungrouped') }])
const docsOf = (gid) => docs.value.filter((d) => d.group_id === gid)
const curGroupDocs = computed(() => docsOf(selGroup.value))
const curGroupName = computed(() => groupList.value.find((g) => g.id === selGroup.value)?.name || tr('docs.ungrouped'))
const trashCount = computed(() => trash.value.length)

// ---------- 文档拖拽（HTML5 原生 DnD，仅桌面）：组内排序 + 跨分组移动 ----------
// 左栏折叠树与右栏列表共用：gid 标记本次拖拽发生在哪个分组，
// docsOf(gid) 是 filter 拷贝，drop 后按新序写回源 docs 数组再调保存接口；
// 拖到其他分组的组头（.g-row）即移动分组（PUT /docs/move）
const docDrag = ref({ from: -1, over: -1, gid: -1, id: 0, overGrp: -1 })

function docDragStart(i, gid, e) {
  const id = docsOf(gid)[i]?.id || 0
  if (e?.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', String(i)) // Firefox 要求写入数据才启动拖拽
  }
  // 状态更新延后到 dragstart 之后：Safari 会在拖拽源元素于 dragstart 派发期间
  // 被 reactive 重渲染（class 变化）时直接取消整个拖拽，Chromium 无此问题
  setTimeout(() => {
    docDrag.value = { from: i, over: -1, gid, id, overGrp: -1 }
  }, 0)
}
function docDragOver(i, gid, e) {
  if (docDrag.value.from < 0 || docDrag.value.gid !== gid) return
  e.preventDefault() // 允许 drop
  e.dataTransfer.dropEffect = 'move'
  docDrag.value.over = i
}
function docDragEnd() {
  docDrag.value = { from: -1, over: -1, gid: -1, id: 0, overGrp: -1 }
}
function docDragDrop(i, gid) {
  const f = docDrag.value.from
  docDragEnd()
  if (f < 0 || f === i) return
  const ordered = [...docsOf(gid)]
  const [item] = ordered.splice(f, 1)
  ordered.splice(f < i ? i - 1 : i, 0, item) // 松手=插入目标项之前
  docs.value = [...ordered, ...docs.value.filter((d) => d.group_id !== gid)]
  api.put('/docs/order', { ids: ordered.map((d) => d.id) }).catch(() => {
    ElMessage.error(tr('docs.saveOrderFailed'))
  })
}

// 跨分组：组头作为 drop 目标（仅接受来自其他分组的文档拖拽）
function grpDragOver(gid, e) {
  if (docDrag.value.from < 0 || docDrag.value.gid === gid) return
  e.preventDefault()
  e.dataTransfer.dropEffect = 'move'
  docDrag.value.overGrp = gid
}
function grpDragLeave(gid, e) {
  // 只在真正离开组头时清高亮（移过子元素触发的 dragleave 不算）
  if (e.currentTarget.contains(e.relatedTarget)) return
  if (docDrag.value.overGrp === gid) docDrag.value.overGrp = -1
}
async function grpDragDrop(gid) {
  const id = docDrag.value.id
  const fromGid = docDrag.value.gid
  docDragEnd()
  if (!id || fromGid === gid) return
  try {
    await api.put('/docs/move', { id, group_id: gid })
    ElMessage.success(tr('docs.movedGroup'))
    load(true)
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 移动分组弹窗（2026-10-19）：列表行「移动」按钮，拖拽之外的显式换组入口；
// 成功后跳到目标分组（含未分组 id=0）让用户直接看到结果
const moveDlg = ref({ show: false, doc: null, groupID: 0 })
function openMove(d) {
  moveDlg.value = { show: true, doc: d, groupID: d.group_id }
}
async function doMove() {
  const { doc, groupID } = moveDlg.value
  if (!doc || groupID === doc.group_id) return
  try {
    await api.put('/docs/move', { id: doc.id, group_id: groupID })
    moveDlg.value.show = false
    selGroup.value = groupID
    ElMessage.success(tr('docs.movedGroupNamed', { name: doc.title }))
    load(true)
  } catch (e) {
    ElMessage.error(e.message)
  }
}

function fmtAgo(ts) {
  if (!ts) return ''
  const s = Math.floor(Date.now() / 1000) - ts
  if (s < 60) return tr('docs.justNow')
  if (s < 3600) return tr('docs.minutesAgo', { n: Math.floor(s / 60) })
  if (s < 86400) return tr('docs.hoursAgo', { n: Math.floor(s / 3600) })
  return tr('docs.daysAgo', { n: Math.floor(s / 86400) })
}

// ---------- 数据加载 ----------

async function load(silent = false) {
  try {
    const res = await api.get('/docs')
    groups.value = res.groups || []
    docs.value = res.docs || []
    if (!groups.value.find((g) => g.id === selGroup.value) && groups.value.length) {
      selGroup.value = groups.value[0].id
    }
    // 首次加载展开第一个分组（之后静默刷新不动用户的展开状态），
    // 并清掉已消失分组的展开项
    if (!treeInited && groups.value.length) {
      treeInited = true
      openGroups.value = [String(groups.value[0].id)]
    }
    const alive = new Set(groupList.value.map((g) => String(g.id)))
    openGroups.value = openGroups.value.filter((n) => alive.has(n))
  } catch (e) {
    if (!silent) ElMessage.error(e.message)
  }
}

async function loadTrash(silent = false) {
  try {
    const res = await api.get('/docs-trash')
    trash.value = res.docs || []
  } catch (e) {
    if (!silent) ElMessage.error(e.message)
  }
}

// 折叠树开合变化：最后展开的分组作为右侧列表与「新建文档」的默认去处
function onCollapse(names) {
  const arr = Array.isArray(names) ? names : [names]
  if (arr.length) selGroup.value = Number(arr[arr.length - 1]) || 0
}

// ---------- 分组操作 ----------

async function createGroup() {
  try {
    const { value } = await ElMessageBox.prompt(tr('docs.groupPrompt'), tr('docs.newGroup'), {
      closeOnClickModal: false,
      inputPattern: /\S+/,
      inputErrorMessage: tr('docs.nameRequired'),
    })
    const name = value.trim().slice(0, 50)
    const res = await api.post('/docs/groups', { name })
    await load(true)
    selGroup.value = res.id
    openGroups.value.push(String(res.id)) // 新组自动展开
    ElMessage.success(tr('docs.groupCreated'))
  } catch {
    /* 取消 */
  }
}

async function renameGroup(g) {
  try {
    const { value } = await ElMessageBox.prompt(tr('docs.groupPrompt'), tr('docs.renameGroup'), {
      closeOnClickModal: false,
      inputValue: g.name,
      inputPattern: /\S+/,
      inputErrorMessage: tr('docs.nameRequired'),
    })
    await api.put(`/docs/groups/${g.id}`, { name: value.trim().slice(0, 50) })
    await load(true)
  } catch (e) {
    if (e && e.message) ElMessage.error(e.message)
  }
}

async function deleteGroup(g) {
  const n = docsOf(g.id).length
  const tip = n
    ? tr('docs.deleteGroupConfirmDocs', { name: g.name, n })
    : tr('docs.deleteGroupConfirm', { name: g.name })
  try {
    await ElMessageBox.confirm(tip, tr('docs.deleteGroup'), { type: 'warning', confirmButtonText: tr('docs.delete'), cancelButtonText: tr('docs.cancel') })
  } catch {
    return
  }
  try {
    await api.del(`/docs/groups/${g.id}`)
    openGroups.value = openGroups.value.filter((n) => n !== String(g.id))
    await Promise.all([load(true), loadTrash(true)])
    ElMessage.success(tr('docs.deleted'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---------- 文档操作 ----------

const newGuard = useDirtyGuard(() => ({ ...newForm.value }))
function openNewDoc() {
  newForm.value.groupID = selGroup.value
  newGuard.snapshot()
}

function resetNew() {
  newForm.value.title = ''
  newForm.value.format = 'richtext' // 新建文档默认富文本（2026-09-27 用户指定）
  newForm.value.groupID = selGroup.value
}

async function createDoc() {
  const f = newForm.value
  const title = f.title.trim()
  if (!title) {
    ElMessage.warning(tr('docs.titleRequired'))
    return
  }
  creating.value = true
  try {
    const res = await api.post('/docs', { title, format: f.format, group_id: f.groupID })
    newDlg.value = false
    await load(true)
    selGroup.value = f.groupID
    if (!openGroups.value.includes(String(f.groupID))) openGroups.value.push(String(f.groupID))
    openDoc({ id: res.id, title: res.title || title }) // 创建即开编辑 tab
    ElMessage.success(tr('docs.docCreated'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    creating.value = false
  }
}

async function deleteDoc(d) {
  try {
    await ElMessageBox.confirm(tr('docs.deleteDocConfirm', { name: d.title }), tr('docs.deleteDoc'), {
      type: 'warning',
      confirmButtonText: tr('docs.delete'),
      cancelButtonText: tr('docs.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/docs/${d.id}`)
    closeDoc(d.id) // 若开着该文档的编辑 tab，一并关闭
    await Promise.all([load(true), loadTrash(true)])
    ElMessage.success(tr('docs.trashed'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---------- 回收站 ----------

async function openTrash() {
  trashDlg.value = true
  await loadTrash()
}

async function restoreDoc(d) {
  try {
    await api.post(`/docs-trash/${d.id}/restore`)
    await Promise.all([load(true), loadTrash(true)])
    ElMessage.success(d.group_id ? tr('docs.restored', { name: d.title }) : tr('docs.restoredToUngrouped', { name: d.title }))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function hardDelete(d) {
  try {
    await ElMessageBox.confirm(tr('docs.hardDeleteConfirm', { name: d.title }), tr('docs.hardDelete'), {
      type: 'warning',
      confirmButtonText: tr('docs.hardDelete'),
      cancelButtonText: tr('docs.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/docs-trash/${d.id}`)
    await loadTrash(true)
    ElMessage.success(tr('docs.hardDeleted'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 清空回收站：全部软删文档连同历史版本彻底删除（编辑器内删除的同样生效）
async function emptyTrash() {
  const n = trash.value.length
  if (!n) return
  try {
    await ElMessageBox.confirm(tr('docs.emptyTrashConfirm', { n }), tr('docs.emptyTrash'), {
      type: 'warning',
      confirmButtonText: tr('docs.emptyAction'),
      cancelButtonText: tr('docs.cancel'),
    })
  } catch {
    return
  }
  try {
    const ids = trash.value.map((d) => d.id) // 先记 id：清空后要连带关闭仍开着的编辑 tab
    const res = await api.del('/docs-trash')
    ids.forEach(closeDoc)
    await Promise.all([load(true), loadTrash(true)])
    ElMessage.success(tr('docs.trashEmptied', { n: res.deleted ?? n }))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 旧路由 /docs/:id 已并入本页内嵌 tab：redirect 到 /docs?open=id，这里消费后清 query
watch(
  () => route.query.open,
  (v) => {
    const id = Number(v)
    if (id > 0) {
      openDocById(id)
      router.replace({ query: { ...route.query, open: undefined } })
    }
  },
  { immediate: true },
)

onMounted(() => {
  load()
  loadTrash(true)
  // 预热编辑器 chunk（1MB+，含 vditor/wangeditor）：浏览文档列表时后台先拉，
  // 首次点开文档不再等下载
  import('./DocEditView.vue')
})
// keep-alive 切回本页：静默刷新树与回收站计数，不动用户的展开状态（load 内已保护）
onActivated(() => {
  load(true)
  loadTrash(true)
})
</script>

<style scoped>
.docs-page {
  display: flex;
  /* 侧栏与主区间距=分隔条自身 8px（可拖拽调宽、单击收起，见 useSidePanel） */
  gap: 0;
  height: calc(100vh - 130px); /* 顶栏 + 标签栏 */
}
.docs-side {
  width: 240px;
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px;
  min-height: 0;
}
.side-actions {
  display: flex;
  gap: 8px;
  flex: none;
}
.side-actions .el-button {
  flex: 1;
  margin: 0;
}
.side-scroll {
  flex: 1;
  min-height: 0;
}
.trash-btn {
  flex: none;
  margin: 0;
}
.op-icon {
  margin-right: 4px;
}
/* 折叠树：组头（#title 插槽）+ 组内文档叶子 */
.doc-collapse {
  --el-collapse-header-bg-color: transparent;
  --el-collapse-content-bg-color: transparent;
  --el-collapse-border-color: transparent;
  border-top: none;
  border-bottom: none;
}
.doc-collapse :deep(.el-collapse-item__header) {
  height: 34px;
  line-height: 34px;
  border-bottom: none;
  font-size: 14px;
}
.doc-collapse :deep(.el-collapse-item__wrap) {
  border-bottom: none;
}
.doc-collapse :deep(.el-collapse-item__content) {
  padding-bottom: 4px;
}
.g-row {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  min-width: 0;
  padding-right: 8px;
  color: var(--ink);
  font-size: 14px;
  cursor: pointer;
  user-select: none;
}
.g-row:hover .g-name {
  color: var(--brand);
}
/* 跨分组拖拽：组头 drop 目标高亮 */
.g-row.grp-over {
  background: var(--brand-bg, rgba(64, 128, 255, 0.12));
  border-radius: 6px;
  outline: 1px dashed var(--brand);
}
.g-accent {
  width: 3px;
  height: 14px;
  border-radius: 2px;
  flex: none;
}
.g-icon {
  font-size: 15px;
  flex: none;
  color: var(--muted);
}
.g-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}
.g-count {
  font-size: 12px;
  color: var(--muted);
}
.g-ops {
  display: none;
  gap: 4px;
}
.g-row:hover .g-ops {
  display: inline-flex;
}
.g-ops .el-icon {
  font-size: 13px;
  padding: 2px;
  border-radius: 3px;
  color: var(--text-2);
}
.g-ops .el-icon:hover {
  color: var(--brand);
  background: var(--border);
}
.doc-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px 6px 10px;
  border-radius: 6px;
  cursor: grab; /* 可拖拽排序的视觉提示 */
  user-select: none;
}
.doc-row:hover {
  background: var(--bg);
}
.doc-row:active {
  cursor: grabbing;
}
.doc-row.dragging {
  opacity: 0.45;
}
.doc-row.drag-over {
  box-shadow: inset 0 2px 0 0 var(--brand);
}
.d-icon {
  font-size: 14px;
  color: var(--muted);
  flex: none;
}
.d-main {
  flex: 1;
  min-width: 0;
}
.d-title {
  font-size: 13px;
  color: var(--ink);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.d-time {
  font-size: 11px;
  color: var(--muted);
}
.d-del {
  display: none;
  font-size: 13px;
  color: var(--muted);
}
.doc-row:hover .d-del {
  display: inline-flex;
}
.d-del:hover {
  color: var(--danger, #f56c6c);
}
.side-empty {
  padding: 16px 8px;
  font-size: 12px;
  color: var(--muted);
  text-align: center;
}
.mobile-bar {
  display: none;
}
.docs-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  min-height: 0;
}
.list-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 12px 16px 8px;
  flex: none;
}
/* 内嵌文档 tab 栏：仅在有打开文档时出现；胶囊式标签，激活项实底品牌色（双击关闭） */
.doc-tabs {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 10px;
  border-bottom: 1px solid var(--border);
  flex: none;
  overflow-x: auto;
}
.dtab {
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
  max-width: 180px;
}
.dtab:hover {
  color: var(--ink);
  border-color: var(--brand);
}
.dtab.active {
  color: #fff;
  background: var(--brand);
  border-color: var(--brand);
  font-weight: 600;
}
.dtab.active .t-close {
  color: rgba(255, 255, 255, 0.85);
}
.dtab.active .t-close:hover {
  color: #fff;
  background: rgba(255, 255, 255, 0.2);
}
.t-icon {
  font-size: 14px;
}
.t-title {
  overflow: hidden;
  text-overflow: ellipsis;
}
.t-close {
  flex: none;
  font-size: 12px;
  border-radius: 3px;
  color: var(--muted);
}
.t-close:hover {
  color: var(--danger, #f56c6c);
  background: var(--border);
}
/* 编辑面板：约束高度，DocEditView 内部 100% 铺满 */
.doc-panel {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.list-title {
  font-size: 15px;
  font-weight: 700;
  color: var(--ink);
}
.list-count {
  font-size: 12px;
  color: var(--muted);
}
.list-scroll {
  flex: 1;
  min-height: 0;
}
.list-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 16px;
  border-top: 1px solid var(--border);
  cursor: grab; /* 可拖拽排序的视觉提示 */
  user-select: none;
}
.list-row:first-child {
  border-top: none;
}
.list-row:hover {
  background: var(--bg);
}
.list-row:active {
  cursor: grabbing;
}
.list-row.dragging {
  opacity: 0.45;
}
.list-row.drag-over {
  box-shadow: inset 0 2px 0 0 var(--brand);
}
.row-icon {
  font-size: 15px;
  color: var(--muted);
  flex: none;
}
.row-main {
  flex: 1;
  min-width: 0;
}
.row-title {
  font-size: 14px;
  color: var(--ink);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.row-sub {
  font-size: 12px;
  color: var(--muted);
  margin-top: 2px;
}
.row-tag {
  flex: none;
}
.row-del {
  flex: none;
  opacity: 0;
}
.list-row:hover .row-del {
  opacity: 1;
}
.main-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--muted);
}

/* 回收站抽屉 */
.trash-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 16px;
  color: var(--ink);
}
.trash-empty {
  padding: 24px 0;
  text-align: center;
  font-size: 13px;
  color: var(--muted);
}
.trash-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 4px;
  border-bottom: 1px solid var(--border);
}
.trash-row .row-main {
  flex: 1;
}
.trash-row .el-button {
  padding: 4px 6px;
}

/* 手机端：左栏改顶部下拉条 */
@media (max-width: 768px) {
  .docs-page {
    flex-direction: column;
    height: calc(100vh - 110px); /* 顶栏 + 标签栏 + main padding */
    gap: 8px;
  }
  .mobile-bar {
    display: flex;
    gap: 8px;
    flex: none;
  }
  .docs-side {
    display: none;
  }
  .row-del {
    opacity: 1;
  }
}
</style>
