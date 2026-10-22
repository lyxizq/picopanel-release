<template>
  <div class="doc-edit-page">
    <!-- 文档在回收站：只读空态 + 关闭入口 -->
    <div v-if="missing" class="de-missing">
      <el-icon :size="42"><Notebook /></el-icon>
      <p>文档不存在或已在回收站</p>
      <el-button size="small" @click="emit('close')">关闭</el-button>
    </div>

    <template v-else-if="doc">
      <div class="main-head">
        <input
          v-model="doc.title"
          class="title-input"
          maxlength="200"
          placeholder="文档标题"
          @blur="saveTitle"
          @keydown.enter="($event) => $event.target.blur()"
        />
        <span class="save-state" :class="saveStatus">{{ saveText }}</span>
        <!-- 可选分组（53）：切换即保存换组，免回列表拖拽/移动；updated_at 回填防 CAS 409 -->
        <el-select v-model="doc.group_id" size="small" class="grp-sel" @change="changeGroup">
          <el-option v-for="g in props.groups" :key="g.id" :label="g.name" :value="g.id" />
        </el-select>
        <el-button size="small" @click="openHistory">
          <el-icon class="op-icon"><Timer /></el-icon>历史
        </el-button>
        <el-button size="small" type="danger" plain @click="removeDoc">删除</el-button>
      </div>

      <div ref="wrapEl" class="editor-wrap" @keydown.ctrl.s.prevent="doSave" @keydown.meta.s.prevent="doSave">
        <!-- 富文本（wangeditor，随本页 chunk 懒加载）。Toolbar 与 Editor 必须是
             同一父节点的直接子节点：wangeditor 全屏是把共同父节点设为 fixed
             全屏容器，多包一层 div 工具栏就进不了全屏、退出按钮够不着 -->
        <template v-if="doc.format === 'richtext'">
          <Toolbar class="we-toolbar" :editor="weRef" :default-config="weToolbarCfg" mode="simple" />
          <Editor class="we-editor" v-model="doc.content" :default-config="weEditorCfg" mode="simple" @on-created="onWeCreated" />
        </template>
        <!-- Markdown：Vditor 所见即所得（即时渲染 / 分屏两种模式）。
             容器 key 含模式——切换即换新容器重建实例，内容取 doc 最新值 -->
        <template v-else>
          <div class="md-bar">
            <el-radio-group :model-value="mdMode" size="small" @update:model-value="switchMdMode">
              <el-radio-button value="ir">即时渲染</el-radio-button>
              <el-radio-button value="sv">分屏预览</el-radio-button>
            </el-radio-group>
            <!-- 排版控件（Markdown 内联 HTML 语义，作用于当前选中文字） -->
            <el-select
              v-model="mdSize"
              size="small"
              placeholder="字号"
              class="md-size"
              @change="(v) => { mdApplyStyle(`font-size:${v}px`); mdSize = null }"
            >
              <el-option v-for="s in MD_SIZES" :key="s" :label="`${s}px`" :value="s" />
            </el-select>
            <el-tooltip content="文字颜色" placement="top" :hide-after="0">
              <el-dropdown trigger="click" @command="mdPickColor">
                <el-button size="small" class="md-ico-btn">
                  <span class="md-a md-a-fg">A</span>
                </el-button>
                <template #dropdown>
                  <el-dropdown-menu class="md-palette">
                    <el-dropdown-item v-for="c in MD_TEXT_COLORS" :key="c" :command="c">
                      <span class="md-swatch" :style="{ background: c }" :title="c"></span>
                    </el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </el-tooltip>
            <el-tooltip content="背景色" placement="top" :hide-after="0">
              <el-dropdown trigger="click" @command="mdPickBg">
                <el-button size="small" class="md-ico-btn">
                  <span class="md-a md-a-bg">A</span>
                </el-button>
                <template #dropdown>
                  <el-dropdown-menu class="md-palette">
                    <el-dropdown-item v-for="c in MD_BG_COLORS" :key="c" :command="c">
                      <span class="md-swatch" :style="{ background: c }" :title="c"></span>
                    </el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </el-tooltip>
            <el-tooltip content="清除选中文字的格式" placement="top" :hide-after="0">
              <el-button size="small" class="md-ico-btn" @click="mdClearFormat">
                <el-icon class="md-ico"><Brush /></el-icon>
              </el-button>
            </el-tooltip>
            <el-tooltip content="插入 Mermaid 图（流程图/时序图等），预览自动渲染" placement="top" :hide-after="0">
              <el-button size="small" class="md-ico-btn" @click="mdInsertMermaid">
                <el-icon class="md-ico"><Share /></el-icon>
              </el-button>
            </el-tooltip>
            <el-dropdown trigger="click" @command="exportDoc">
              <el-button size="small">
                导出<el-icon class="md-exp-arrow"><ArrowDown /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="md">Markdown（.md）</el-dropdown-item>
                  <el-dropdown-item command="html">HTML 网页</el-dropdown-item>
                  <el-dropdown-item command="pdf">PDF（打印另存）</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
            <span class="md-hint">选中文字可调字号/颜色 · Ctrl+S 保存</span>
          </div>
          <div :key="mdMode" ref="mdEl" class="md-vditor"></div>
        </template>

        <!-- 代码块复制按钮：注入 pre 右上角（悬停显示），点击走容器事件委托 -->
      </div>
    </template>

    <div v-else class="de-missing">
      <el-icon class="is-loading" :size="28"><Loading /></el-icon>
    </div>

    <!-- 历史版本：每篇保留最近 10 版（保存覆盖前的旧内容快照，10 分钟内的
         连续编辑合并为一版）；恢复=把该版本内容载入编辑器，走正常自动保存 -->
    <el-drawer append-to-body v-model="historyDlg" title="历史版本" size="380px">
      <div v-if="!versions.length" class="his-empty">暂无历史版本（首次编辑或近期已清理）</div>
      <div v-for="v in versions" :key="v.id" class="his-row">
        <div class="his-main">
          <div class="his-title">{{ v.title || '（无标题）' }}</div>
          <div class="his-meta">{{ fmtTime(v.created_at) }} · {{ fmtBytes(v.size) }}</div>
        </div>
        <el-button size="small" text type="primary" @click="restoreVersion(v)">恢复</el-button>
      </div>
    </el-drawer>

    <!-- 插入链接：内置按钮在光标不在正文时是禁用态点击无响应（dist 实证
         restoreSelection 后 selection 仍无效就直接 return），捕获拦截改自定义弹窗 -->
    <el-dialog v-model="linkDlg.show" title="插入链接" width="440px" append-to-body>
      <el-form label-width="64px" @submit.prevent>
        <el-form-item label="文本">
          <el-input v-model="linkDlg.text" placeholder="留空则显示链接地址" maxlength="200" />
        </el-form-item>
        <el-form-item label="链接">
          <el-input v-model="linkDlg.url" placeholder="https://…" maxlength="2000" @keyup.enter="insertLinkGo" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="linkDlg.show = false">取消</el-button>
        <el-button type="primary" @click="insertLinkGo">插入</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// 文档编辑组件：由文档页（DocsView）内嵌 tab 打开，一篇文档一个实例
//（docId 作 props+key，keep-alive 切换保编辑态）。富文本 = wangeditor；
// Markdown = Vditor 所见即所得（均懒加载在本 chunk，vditor 按需资源本地化
// 在 /vendor/vditor）。内容改动 debounce 2s 自动保存，Ctrl+S 立即保存；
// 历史版本可恢复（载入编辑器→正常保存链路）。
defineOptions({ name: 'DocEditView' })
import { computed, onActivated, onBeforeUnmount, onDeactivated, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, Brush, Loading, Notebook, Share, Timer } from '@element-plus/icons-vue'
import Vditor from 'vditor'
import { api, putKeepalive, uploadDocAsset } from '../api/index.js'
import { theme as appTheme } from '../utils/theme.js'
import { fmtBytes, fmtTime } from '../utils/format.js'
import { Toolbar, Editor } from '@wangeditor/editor-for-vue'
import 'vditor/dist/index.css'
import '@wangeditor/editor/dist/css/style.css'

const props = defineProps({
  docId: { type: Number, required: true },
  groups: { type: Array, default: () => [] }, // 分组成员下拉（53，父级传含未分组的全量清单）
})
// renamed：标题变化（父级同步内嵌 tab 文案）；close：请求关闭本编辑 tab
//（删除、存活校验失败、空态关闭按钮）；moved：换组成功（父级重拉列表）
const emit = defineEmits(['renamed', 'close', 'deleted', 'moved'])

const doc = ref(null) // { id, group_id, title, format, content, deleted_at }
const missing = ref(false)
const saveStatus = ref('') // '' | pending | saving | saved
const savedAt = ref('')
const mdMode = ref('sv') // ir=即时渲染 sv=分屏预览（默认分屏，可随时切换）
const historyDlg = ref(false)
const versions = ref([])

const saveText = computed(() =>
  saveStatus.value === 'pending' ? '待保存…' : saveStatus.value === 'saving' ? '保存中…' : saveStatus.value === 'conflict' ? '已在其他窗口修改' : saveStatus.value === 'saved' ? `已保存 ${savedAt.value}` : '',
)

// ---------- 加载 ----------

let suppressWatch = false // 加载文档时的内容赋值不算改动

async function loadDoc() {
  try {
    const res = await api.get(`/docs/${props.docId}`)
    if (!res.doc || res.doc.deleted_at > 0) {
      missing.value = true
      return
    }
    suppressWatch = true
    doc.value = res.doc
    origTitle = res.doc.title || ''
    origGroupId = res.doc.group_id || 0
    emit('renamed', res.doc.title || '文档')
  } catch (e) {
    // 404（已彻底删除）与其他错误都落到空态，避免半开页面
    missing.value = true
    if (!String(e.message).includes('不存在')) ElMessage.error(e.message)
  }
}

// ---------- 自动保存 ----------

let saveTimer = null
let dirty = false

function scheduleSave() {
  if (!doc.value) return
  dirty = true
  saveStatus.value = 'pending'
  clearTimeout(saveTimer)
  saveTimer = setTimeout(doSave, 2000)
}

async function doSave() {
  clearTimeout(saveTimer)
  saveTimer = null
  if (!doc.value || !dirty) return
  // Markdown 内容以编辑器实例为准（兜底 input 回调漏触发的极端场景；
  // 实例返回空且当前有内容时保守跳过，防半初始化实例清空内容）
  if (doc.value.format === 'md' && vd && mdReady) {
    const latest = vd.getValue()
    if (typeof latest === 'string' && (latest || !doc.value.content)) doc.value.content = latest
  }
  dirty = false
  saveStatus.value = 'saving'
  try {
    // 乐观锁：带 base_updated_at，其他窗口已保存过时服务端拒绝（409）
    const res = await api.put(`/docs/${doc.value.id}`, {
      content: doc.value.content,
      base_updated_at: doc.value.updated_at,
    })
    if (res.updated_at) doc.value.updated_at = res.updated_at
    savedAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
    saveStatus.value = 'saved'
  } catch (e) {
    if (e.status === 409) return onConflict('content')
    // 超出单篇上限是永久性失败，重试只会刷屏——提示后停在「待保存」，
    // 用户删减内容再编辑时自动保存会重新触发
    if (e.status === 400 && String(e.message).includes('文档过大')) {
      saveStatus.value = 'pending'
      ElMessage.warning(e.message + '，请删减内容（拆分文档或图片外链）后再保存')
      return
    }
    dirty = true
    saveStatus.value = 'pending'
    saveTimer = setTimeout(doSave, 3000) // 失败自动重试，不停在「待保存」
    ElMessage.error('保存失败：' + e.message + '，3 秒后自动重试')
  }
}

// 保存冲突（其他窗口先保存了这篇文档）：覆盖远端，或放弃本地改动载入最新版
async function onConflict(kind) {
  saveStatus.value = 'conflict'
  let overwrite = false
  try {
    await ElMessageBox.confirm(
      '文档已在其他窗口被修改。用当前内容覆盖远端，或载入最新版（本地未保存的改动将丢弃）？',
      '保存冲突',
      { confirmButtonText: '覆盖远端', cancelButtonText: '载入最新版', type: 'warning' },
    )
    overwrite = true
  } catch {
    return reloadAfterConflict()
  }
  try {
    saveStatus.value = 'saving'
    const body = kind === 'title' ? { title: doc.value.title.trim() } : { content: doc.value.content }
    const res = await api.put(`/docs/${doc.value.id}`, body) // 不带 base_updated_at = 强制覆盖
    if (res.updated_at) doc.value.updated_at = res.updated_at
    if (kind === 'title') {
      origTitle = doc.value.title.trim()
      emit('renamed', doc.value.title)
    } else {
      savedAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
      dirty = false
    }
    saveStatus.value = 'saved'
    ElMessage.success('已覆盖远端')
  } catch (e) {
    saveStatus.value = 'pending'
    ElMessage.error('覆盖保存失败：' + e.message)
  }
}

// 冲突后载入远端最新版（覆盖本地编辑器内容，重建 undo 栈）
async function reloadAfterConflict() {
  try {
    const res = await api.get(`/docs/${doc.value.id}`)
    if (!res.doc || res.doc.deleted_at > 0) {
      missing.value = true
      return
    }
    suppressWatch = true
    const content = res.doc.content ?? ''
    if (doc.value.format === 'md' && vd && mdReady) {
      vd.setValue(content, true)
    } else if (doc.value.format === 'richtext' && weRef.value) {
      weRef.value.setHtml(content)
    }
    doc.value.title = res.doc.title
    doc.value.content = content
    doc.value.updated_at = res.doc.updated_at
    origTitle = res.doc.title || ''
    origGroupId = res.doc.group_id || 0
    dirty = false
    saveStatus.value = 'saved'
    ElMessage.success('已载入最新版本')
  } catch (e) {
    ElMessage.error('载入最新版失败：' + e.message)
  }
}

// 关 tab / 刷新 / 切后台前的最后一搏：keepalive PUT 发出后即使页面卸载也会送达
//（body 超浏览器 64KB 上限时不发，交给 beforeunload 原生确认拦住用户）
function flushOnExit() {
  if (!dirty || !doc.value) return
  let content = doc.value.content
  if (doc.value.format === 'md' && vd && mdReady) {
    const latest = vd.getValue()
    if (typeof latest === 'string' && (latest || !content)) content = latest
  }
  if (JSON.stringify({ content }).length > 60000) return
  dirty = false
  putKeepalive(`/docs/${doc.value.id}`, { content }).catch(() => {})
}

// 有未保存内容时刷新/关闭页面：尽力冲刷 + 原生确认兜底（离开=丢最后 2 秒输入）
function onBeforeUnload(e) {
  if (!dirty || !doc.value) return
  flushOnExit()
  e.preventDefault()
  e.returnValue = ''
}

// 手机切后台/电脑最小化时页面随时可能被杀，切走即冲刷
function onVisChange() {
  if (document.visibilityState === 'hidden') flushOnExit()
}

let origTitle = ''
let origGroupId = 0 // 载入时的分组（53）：换组失败回退下拉用
function saveTitle() {
  if (!doc.value) return
  const title = doc.value.title.trim()
  if (!title) {
    ElMessage.warning('标题不能为空')
    doc.value.title = origTitle
    return
  }
  if (title === origTitle) return
  api.put(`/docs/${doc.value.id}`, { title, base_updated_at: doc.value.updated_at }).then(
    (res) => {
      if (res.updated_at) doc.value.updated_at = res.updated_at
      origTitle = title
      emit('renamed', title)
    },
    (e) => {
      if (e.status === 409) return onConflict('title')
      ElMessage.error('标题保存失败：' + e.message)
    },
  )
}

// 内容变化 → 自动保存（标题走 blur/Enter 单独保存）

// 换组（53）：切换即保存；不带 base_updated_at（组变更不涉内容冲突），响应的
// updated_at 回填乐观锁基准——MoveDoc 在服务端刷新了它，不回填下次自动保存必 409
async function changeGroup() {
  const prev = origGroupId
  origGroupId = doc.value.group_id
  try {
    const res = await api.put(`/docs/${doc.value.id}`, { group_id: doc.value.group_id })
    if (res.updated_at) doc.value.updated_at = res.updated_at
    emit('moved')
    ElMessage.success('已移动分组')
  } catch (e) {
    doc.value.group_id = prev
    ElMessage.error('移动分组失败：' + e.message)
  }
}

watch(
  () => doc.value?.content,
  () => {
    if (suppressWatch) {
      suppressWatch = false
      return
    }
    if (doc.value) scheduleSave()
  },
)

// ---------- 历史版本 ----------

async function openHistory() {
  historyDlg.value = true
  try {
    const res = await api.get(`/docs/${doc.value.id}/versions`)
    versions.value = res.versions || []
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 恢复=取版本内容载入编辑器（重建 undo 栈），随后走正常自动保存链路；
// 距上一版本超 10 分钟时，被覆盖的当前内容会自动留新版本，恢复错了还能再回退
async function restoreVersion(v) {
  try {
    const res = await api.get(`/docs/${doc.value.id}/versions/${v.id}`)
    const content = res.version?.content ?? ''
    if (doc.value.format === 'md' && vd && mdReady) {
      vd.setValue(content, true)
    } else if (doc.value.format === 'richtext' && weRef.value) {
      weRef.value.setHtml(content)
    }
    suppressWatch = false
    if (doc.value.content !== content) {
      doc.value.content = content // 触发 watch → 自动保存
    } else {
      scheduleSave() // 同值不触发 watch，手动标脏（如恢复到与当前一致的内容）
    }
    historyDlg.value = false
    ElMessage.success('已载入该版本内容，2 秒后自动保存生效')
  } catch (e) {
    ElMessage.error('恢复失败：' + e.message)
  }
}

// ---------- 删除（软删进回收站）----------

async function removeDoc() {
  try {
    await ElMessageBox.confirm(`删除文档「${doc.value.title}」？删除后进入回收站，可在回收站恢复。`, '删除文档', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    dirty = false // 删除即放弃未保存的尾巴
    await api.del(`/docs/${doc.value.id}`)
    ElMessage.success('已移入回收站')
    emit('deleted') // 让父级重拉文档树，左侧列表立即去掉该文档
    emit('close')
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---------- wangeditor ----------

const weRef = ref(null) // 编辑器实例（onCreated 赋值，Toolbar 需要）
// 剔除内置图片/视频菜单（group-image 下拉含 insertImage/uploadImage、insertVideo）：
// 它们在光标不在正文/有选中内容时是禁用态点了没响应；曾用 we-extra 自绘
// 按钮接管（doc_assets 附件接口），2026-09-26 按用户要求移除。
const weToolbarCfg = { excludeKeys: ['group-image', 'insertImage', 'uploadImage', 'insertVideo', 'uploadVideo'] }
// 粘贴/拖入的图片文件走 customUpload 上传 doc_assets，正文只插短 URL——
// base64 内嵌会让 docs/doc_versions 随版本快照放大（~33% 膨胀），含图文档
// 还会超 60KB 跳过 keepalive 冲刷。dist 实证路由链：insertData 拦到图片
// 文件 → uploadImages → base64LimitSize(默认 0 跳过) → customUpload。
// 图片展示走 /docs-assets/:id，HttpOnly Cookie 过鉴权（img 带不了 Authorization）。
const weEditorCfg = {
  placeholder: '开始记录…（图片直接粘贴/拖入，自动保存）',
  MENU_CONF: {
    uploadImage: {
      customUpload(file, insertFn) {
        uploadDocAsset(file)
          .then((url) => insertFn(url, file.name, ''))
          .catch((e) => ElMessage.error(`「${file.name}」上传失败：${e.message}`))
      },
    },
  },
}

let weDispose = null // 编辑器销毁时的清理（document/容器监听 + Observer + 定时器）
// onWeCreated 注入的面板收起工具（insertLink 拦截后要自行收掉其他面板用）
let panelEnforce = null

// ---------- 插入表格/链接：捕获阶段处理工具栏按钮 ----------
// 表格：回归内置点选面板（2026-09-26 用户指定）；选区无效时内置按钮是禁用
// 态点了没反应、点格子插表会抛错——只补选区（restoreSelection，无选区聚焦
// 末尾）后放行内置逻辑。链接：同样因禁用态无响应，拦截改自定义弹窗。
// 监听挂在本实例的 editor-wrap 上，多文档 tab 并存时互不干扰。

const linkDlg = reactive({ show: false, text: '', url: '' })

// 删除表格二次确认：拦下内置 deleteTable 菜单（工具栏 group-table 下拉与
// 选区悬浮条同源 data-menu-key），确认后补一击原按钮放行内置删除。
// 弹窗会抢走编辑器焦点，补击前先 restoreSelection 回表格光标
let tableDelArmed = false
function confirmDeleteTable(btn) {
  ElMessageBox.confirm('确定删除该表格吗？表格内容将一并删除（可 Ctrl+Z 撤销）。', '删除表格', {
    confirmButtonText: '删除',
    cancelButtonText: '取消',
    type: 'warning',
  })
    .then(() => {
      try {
        weRef.value?.restoreSelection?.()
      } catch {
        /* 选区已失效时交内置逻辑自行判断 */
      }
      tableDelArmed = true
      btn.click()
      tableDelArmed = false
    })
    .catch(() => {})
}

const onToolbarClickCapture = (e) => {
  const t = e.target
  if (!(t instanceof Element)) return
  const btn = t.closest('button[data-menu-key]')
  if (!btn) return
  const key = btn.getAttribute('data-menu-key')
  if (key === 'deleteTable') {
    if (!tableDelArmed) {
      e.preventDefault()
      e.stopImmediatePropagation()
      e.stopPropagation()
      confirmDeleteTable(btn)
    }
    return
  }
  if (key === 'insertTable') {
    const ed = weRef.value
    if (ed) focusEditor(ed)
  } else if (key === 'insertLink') {
    e.preventDefault()
    e.stopPropagation()
    // 整击拦下后内置的 hidePanelOrModal 不会跑——开着的其他面板会一直挂着，
    // 自行开一轮追杀收掉（DOM 强隐不吃 200ms 拒关守卫）
    panelEnforce?.burst()
    linkDlg.text = weRef.value?.getSelectionText?.() || '' // 有选中文本则预填
    linkDlg.url = ''
    linkDlg.show = true
  }
}

// 弹窗接管焦点后编辑器选区可能失效：先 restoreSelection 回到原光标，
// 仍无选区（从未点过正文等）就聚焦到末尾，再执行插入
function focusEditor(ed) {
  try {
    ed.restoreSelection?.()
  } catch {
    /* ignore */
  }
  try {
    if (!ed.selection) ed.focus(true)
  } catch {
    /* ignore */
  }
}

function insertLinkGo() {
  const url = linkDlg.url.trim()
  if (!url) {
    ElMessage.warning('请输入链接地址')
    return
  }
  linkDlg.show = false
  const ed = weRef.value
  if (!ed) return
  focusEditor(ed)
  const text = linkDlg.text.trim() || url
  try {
    // 有选中文本时替换为链接，折叠光标处则插入新链接
    ed.insertNode({ type: 'link', url, children: [{ text }] })
  } catch {
    ElMessage.error('插入链接失败')
  }
}

// ---------- 代码块复制按钮（最初版：注入 pre 右上角，悬停显隐） ----------
// Slate 重渲染的子节点协调会把注入 pre 的按钮清掉——靠 MutationObserver +
// change 补挂；pre 的 position:relative 用外部样式表常置（行内 style 会被
// Slate 抹掉，是此前「按钮漂到编辑器左端」的根因），按钮不漂移。
// 点击走容器事件委托，悬停显隐纯 CSS。

const wrapEl = ref(null) // .editor-wrap（事件委托/表格拦截的挂载点）

async function copyCode(pre) {
  if (!pre) return
  try {
    await navigator.clipboard.writeText(pre.querySelector('code')?.innerText || pre.innerText)
    ElMessage.success('代码已复制')
  } catch {
    ElMessage.error('复制失败')
  }
}

function onWeCreated(editor) {
  weRef.value = editor
  // 面板（表格点选/颜色/背景/标题下拉等）自动收起——「DOM 强隐 + 多拍 + 重开追杀」：
  // 根因（dist 源码实证 + 无头复现）三条：① 面板 hide() 内置「打开后 200ms 内
  // 拒绝关闭」守卫，单拍 API 收起会被吃掉；② 工具栏重渲染会把面板重开并重置
  // 守卫计时——点面板选项/点面板外引发选区变化都触发，重开后无人补刀即永久
  // 挂着；③ 点工具栏按钮时内置虽有 hidePanelOrModal 关旧开新，但同样被 ①②
  // 拒关，两个面板就叠在一起（insertLink 拦截更是整击拦下、内置根本没跑）。
  // 对策：收起一律走 DOM 强隐（display:none 直改样式，不经 hide()、不吃守卫），
  // 并开 2 秒追杀窗口，窗口内面板每次 show()（必 emit modalOrPanelShow）再补
  // 一轮；点工具栏菜单按钮则停追杀（那一击可能要开新面板），但先强隐「别的
  // 菜单」的已开面板防叠加。
  let burstUntil = 0 // 追杀窗口截止时刻
  let hideTimers = []
  // DOM 级强隐 wrapEl 内所有可见面板（w-e-modal 弹窗有输入框，不碰）；
  // exceptBarItem 内的面板跳过（同一菜单自己刚开的面板不误杀）
  const forceHidePanels = (exceptBarItem) => {
    const root = wrapEl.value
    if (!root) return
    root.querySelectorAll('.w-e-drop-panel, .w-e-select-list').forEach((el) => {
      if (el.offsetParent && !(exceptBarItem && exceptBarItem.contains(el))) el.style.display = 'none'
    })
  }
  const hideSoon = () => {
    hideTimers.forEach(clearTimeout)
    hideTimers = [0, 120, 250, 400, 600, 900, 1200].map((ms) =>
      setTimeout(() => {
        try {
          editor.hidePanelOrModal()
        } catch {
          /* 编辑器已销毁等场景忽略 */
        }
        forceHidePanels(null)
      }, ms),
    )
  }
  const stopHide = () => {
    hideTimers.forEach(clearTimeout)
    hideTimers = []
    burstUntil = 0
  }
  const onPanelShow = () => {
    if (Date.now() <= burstUntil) hideSoon()
  }
  editor.on('modalOrPanelShow', onPanelShow)
  const onDocClick = (e) => {
    const t = e.target
    if (!(t instanceof Element)) return
    const menuBtn = t.closest('button[data-menu-key]')
    if (menuBtn) {
      stopHide()
      // 这一击先把「别的菜单」的已开面板直接收掉：内置的关旧开新会被守卫/
      // 重渲染竞态拒关（面板叠加根因）；同一菜单自己的面板交给内置 toggle
      forceHidePanels(menuBtn.closest('.w-e-bar-item'))
      return
    }
    if (t.closest('.w-e-modal')) return
    // 链接等弹窗（w-e-modal）开着时不动：误关会丢输入，交给其自身取消逻辑
    const modal = document.querySelector('.w-e-modal')
    if (modal && modal.offsetParent) return
    // 面板内（色块/表格格子）与面板外（正文/空白/任意按钮输入框）同策：
    // DOM 强隐多拍 + 2 秒追杀——面板内命中选项、面板外引发选区变化，都会经
    // 工具栏重渲染把面板重开，靠追杀窗口内的 modalOrPanelShow 补刀
    burstUntil = Date.now() + 2000
    hideSoon()
  }
  document.addEventListener('click', onDocClick, true)
  panelEnforce = {
    // 给 insertLink 拦截用：内置 hidePanelOrModal 被拦掉，自行开追杀收面板
    burst: () => {
      burstUntil = Date.now() + 2000
      hideSoon()
    },
  }

  // 插入表格/链接拦截（捕获阶段，见上）
  wrapEl.value?.addEventListener('click', onToolbarClickCapture, true)

  // 复制按钮：给没有按钮的 pre 补挂（被 Slate 协调清掉后由 MO/change 再补），
  // 显隐纯 CSS（pre:hover），点击走 wrap 上的委托
  const wrap = editor.getEditableContainer()
  const decorateCopyBtns = () => {
    wrap.querySelectorAll('pre').forEach((pre) => {
      if (pre.querySelector(':scope > .cc-btn')) return
      const b = document.createElement('div')
      b.className = 'cc-btn'
      b.title = '复制代码'
      b.innerHTML =
        '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/></svg>'
      pre.appendChild(b)
    })
  }
  let decoRaf = 0
  const scheduleDecorate = () => {
    cancelAnimationFrame(decoRaf)
    decoRaf = requestAnimationFrame(decorateCopyBtns)
  }
  const mo = new MutationObserver(scheduleDecorate)
  mo.observe(wrap, { childList: true, subtree: true })
  editor.on('change', scheduleDecorate)
  const onWrapMousedown = (e) => {
    // 按钮不抢编辑器焦点（否则选区丢失）
    if (e.target instanceof Element && e.target.closest('.cc-btn')) e.preventDefault()
  }
  const onWrapClick = (e) => {
    const t = e.target
    if (!(t instanceof Element)) return
    const b = t.closest('.cc-btn')
    if (!b) return
    e.preventDefault()
    e.stopPropagation()
    copyCode(b.closest('pre'))
  }
  wrap.addEventListener('mousedown', onWrapMousedown)
  wrap.addEventListener('click', onWrapClick, true)
  decorateCopyBtns()
  weDispose = () => {
    panelEnforce = null
    document.removeEventListener('click', onDocClick, true)
    stopHide()
    wrapEl.value?.removeEventListener('click', onToolbarClickCapture, true)
    wrap.removeEventListener('mousedown', onWrapMousedown)
    wrap.removeEventListener('click', onWrapClick, true)
    try {
      editor.off('modalOrPanelShow', onPanelShow)
      editor.off('change', scheduleDecorate)
    } catch {
      /* 编辑器已销毁等场景忽略 */
    }
    cancelAnimationFrame(decoRaf)
    mo.disconnect()
  }
}

// ---------- Markdown（Vditor）----------
// 实例随模式重建：切换即销毁重建，undo 栈清零可接受；
// 用户输入经 input 回写 doc.content（走统一的自动保存链路），初始值取 doc。

const mdEl = ref(null)
let vd = null // Vditor 实例
let mdReady = false // after 回调后才接受 input/取值（构造期间的 input 一律忽略）

// ---------- Markdown 排版（字号/颜色，Markdown 内联 HTML 语义，Lute 渲染） ----------
const MD_SIZES = [14, 16, 18, 24, 32]
const MD_TEXT_COLORS = ['#000000', '#595959', '#e02020', '#fa8c16', '#52c41a', '#1890ff', '#722ed1', '#eb2f96']
const MD_BG_COLORS = ['#f5f5f5', '#fff1f0', '#fff7e6', '#f6ffed', '#e6f7ff', '#f9f0ff', '#fff0f6', '#feffe6']
const mdSize = ref(null)

// 给选中文字包一层内联样式 span；textarea 失焦不丢选区，点工具栏控件后仍可取到
function mdApplyStyle(css) {
  if (!vd || !mdReady) return
  const sel = (vd.getSelection() || '').replace(/\n+/g, ' ').trim()
  if (!sel) {
    ElMessage.info('请先在正文中选中文字')
    return
  }
  vd.insertValue(`<span style="${css}">${sel}</span>`)
  scheduleSave() // insertValue 不触发 input 回调，手动标脏（否则排版结果不进保存链路）
}

function mdPickColor(c) {
  mdApplyStyle(`color:${c}`)
}

function mdPickBg(c) {
  mdApplyStyle(`background-color:${c}`)
}

// 清除选中文字的内联格式（剥排版 span；嵌套一并去掉）
function mdClearFormat() {
  if (!vd || !mdReady) return
  const sel = vd.getSelection() || ''
  if (!sel.trim()) {
    ElMessage.info('请先在正文中选中文字')
    return
  }
  vd.insertValue(sel.replace(/<\/?span[^>]*>/g, ''))
  scheduleSave()
}

// 插入 Mermaid 代码块模板（预览/导出自动渲染为图）
function mdInsertMermaid() {
  if (!vd || !mdReady) return
  vd.insertValue('\n```mermaid\ngraph TD\n  A[开始] --> B[结束]\n```\n')
  scheduleSave()
}

// 明暗两套外观（内容主题 css/content-theme/*.css + 代码高亮 js/highlight.js/styles/）
function vdThemes() {
  return appTheme.value === 'dark'
    ? { theme: 'dark', content: 'dark', code: 'github-dark' }
    : { theme: 'classic', content: 'light', code: 'github' }
}

function switchMdMode(m) {
  if (m === mdMode.value) return
  // 切换前从实例拉最新内容（防 input 回调万一漏触发的极端场景），
  // 新实例以 doc.content 为初始值重建
  if (vd && mdReady && doc.value) {
    const latest = vd.getValue()
    if (typeof latest === 'string' && latest !== doc.value.content) doc.value.content = latest
  }
  mdMode.value = m
}

// 工具栏上传按钮 / 粘贴 / 拖入图片 → 上传 doc_assets、正文插短 URL
//（与富文本同口径；base64 内嵌会随版本快照放大文档体积，弃用）
function uploadMdImage(files) {
  for (const f of files) {
    if (!f.type.startsWith('image/')) continue
    uploadDocAsset(f)
      .then((url) => {
        vd?.insertValue(`\n![${f.name.replace(/[[\]]/g, ' ')}](${url})\n`)
        scheduleSave() // 同排版控件：insertValue 不触发 input 回调
      })
      .catch((e) => ElMessage.error(`「${f.name}」上传失败：${e.message}`))
  }
  return null // 阻止默认上传行为
}

// 编辑器挂载（flush:'post' 保证 key 变化后新容器已在 DOM 里；doc 异步加载到位也会触发）
watch(
  () => [doc.value?.id, doc.value?.format, mdMode.value],
  () => {
    if (doc.value?.format !== 'md') {
      destroyMd() // 切到富文本/未加载：实例随容器摘除，主动销毁清监听
      return
    }
    destroyMd()
    if (!mdEl.value) return
    mdReady = false
    const t = vdThemes()
    vd = new Vditor(mdEl.value, {
      mode: mdMode.value,
      lang: 'zh_CN',
      cdn: '/vendor/vditor', // 按需资源本地化（lute/highlight/katex/mermaid…），离线可用
      height: '100%',
      value: doc.value.content || '', // 初始内容：加载文档/切模式重建实例的数据来源
      placeholder: '# 开始记录…',
      cache: { enable: false }, // 关闭 localStorage 草稿，保存统一走本系统
      counter: { enable: true, type: 'text' },
      theme: t.theme,
      preview: { theme: t.content, hljs: { style: t.code, lineNumber: false } },
      toolbar: [
        'headings', 'bold', 'italic', 'strike', '|',
        'line', 'quote', 'list', 'ordered-list', 'check', 'outdent', 'indent', '|',
        'code', 'inline-code', 'link', 'upload', 'table', 'emoji', '|',
        'undo', 'redo', '|', 'preview', 'outline', 'fullscreen',
      ],
      upload: { handler: uploadMdImage, accept: 'image/*' },
      input: (v) => {
        // 构造期渲染初始 value 也会触发 input，靠「同值短路」避免误标脏
        //（同值赋值不触发 watch）；此处不设 mdReady 门槛，保证任何情况下输入都能进保存链路
        if (typeof v !== 'string' || !doc.value) return
        if (doc.value.content !== v) doc.value.content = v
      },
      after: () => {
        mdReady = true
      },
    })
  },
  { flush: 'post' },
)

function destroyMd() {
  if (vd) {
    try {
      vd.destroy() // 容器可能已被 Vue 摘除，销毁失败不阻断新实例创建
    } catch {
      /* ignore */
    }
    vd = null
    mdReady = false
  }
}

// 应用明暗切换 → 同步编辑器外观
watch(appTheme, () => {
  if (!vd) return
  const t = vdThemes()
  vd.setTheme(t.theme, t.content, t.code)
})

// ---------- 导出（Markdown / HTML / PDF-打印）----------

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]))
}

function triggerDownload(url, name) {
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 5000)
}

// getHTML() 是编辑器当前渲染结果（代码高亮/Mermaid/公式已展开为静态 DOM），
// 套一层最小排版即成独立网页；PDF 走浏览器打印（选「另存为 PDF」）
function exportDoc(kind) {
  if (!doc.value) return
  const title = doc.value.title.trim() || '文档'
  if (kind === 'md') {
    const blob = new Blob([vd?.getValue() || doc.value.content || ''], { type: 'text/markdown;charset=utf-8' })
    triggerDownload(URL.createObjectURL(blob), `${title}.md`)
    return
  }
  const html = `<!doctype html><html><head><meta charset="utf-8"><title>${escapeHtml(title)}</title><style>
body{font-family:system-ui,-apple-system,'PingFang SC',sans-serif;max-width:820px;margin:40px auto;padding:0 24px;line-height:1.8;color:#1a1a1a}
pre{background:#f6f8fa;border:1px solid #e1e4e8;border-radius:6px;padding:12px;overflow-x:auto}
code{font-family:Menlo,Monaco,monospace;font-size:13px}
table{border-collapse:collapse}th,td{border:1px solid #d0d7de;padding:6px 12px}
blockquote{border-left:4px solid #d0d7de;margin:1em 0;padding:2px 16px;color:#57606a;background:#f6f8fa}
img{max-width:100%}
</style></head><body>${vd?.getHTML() || escapeHtml(doc.value.content)}</body></html>`
  if (kind === 'html') {
    const blob = new Blob([html], { type: 'text/html;charset=utf-8' })
    triggerDownload(URL.createObjectURL(blob), `${title}.html`)
  } else {
    const w = window.open('', '_blank')
    if (!w) {
      ElMessage.warning('弹窗被浏览器拦截，请允许弹窗后重试')
      return
    }
    w.document.write(html)
    w.document.close()
    setTimeout(() => w.print(), 500) // 等内嵌 base64 图片先解码
  }
}

onMounted(() => {
  window.addEventListener('beforeunload', onBeforeUnload)
  document.addEventListener('visibilitychange', onVisChange)
  loadDoc()
})
// 内嵌 tab 切走即冲刷待保存内容（关 tab 不走卸载，靠这里把最后 2 秒输入送出）
onDeactivated(flushOnExit)
// keep-alive 切回本 tab：不重载内容（保编辑态），只验证文档仍存活——
// 在列表/回收站被删的文档，切回时提示并关 tab（只取存在性，不覆盖任何字段）
onActivated(async () => {
  if (!doc.value || missing.value) return
  try {
    const res = await api.get(`/docs/${props.docId}`)
    if (!res.doc || res.doc.deleted_at > 0) throw new Error('deleted')
  } catch {
    ElMessage.warning('「' + (doc.value.title || '文档') + '」已删除，编辑页已关闭')
    dirty = false
    emit('close')
  }
})
onBeforeUnmount(() => {
  flushOnExit() // 实例销毁（文档页整体关闭）：未保存内容在销毁编辑器前送出
  window.removeEventListener('beforeunload', onBeforeUnload)
  document.removeEventListener('visibilitychange', onVisChange)
  clearTimeout(saveTimer)
  destroyMd()
  if (weDispose) weDispose()
  if (weRef.value) weRef.value.destroy()
})
</script>

<style scoped>
.doc-edit-page {
  display: flex;
  flex-direction: column;
  height: 100%; /* 高度由文档页容器约束（内嵌 tab 布局） */
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  min-height: 0;
  overflow: hidden;
}
.main-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px 8px;
  border-bottom: 1px solid var(--border);
  flex: none;
}
.title-input {
  flex: 1;
  min-width: 0;
  border: none;
  outline: none;
  background: transparent;
  font-size: 17px;
  font-weight: 700;
  color: var(--ink);
}
.title-input:focus {
  border-bottom: 1px dashed var(--border);
}
.save-state {
  flex: none;
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
}
/* 可选分组下拉（53）：头行内紧凑小钮 */
.grp-sel {
  flex: none;
  width: 118px;
}
.save-state.pending {
  color: var(--warn-text, #e6a23c);
}
.save-state.saved {
  color: var(--ok, #67c23a);
}
.op-icon {
  margin-right: 4px;
}
.editor-wrap {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.we-toolbar {
  border-bottom: 1px solid var(--border);
  flex: none;
}
/* 不能 overflow-x:auto：会裁掉工具栏按钮的弹层（表格插入面板/标题字号下拉/
   颜色选择器等全是绝对定位在按钮下方），看起来就是「按钮点了没反应」；
   窄屏改为换行铺开 */
.we-toolbar :deep(.w-e-bar) {
  flex-wrap: wrap;
}
.we-editor {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
/* wangeditor 全屏容器自身不带 z-index，压过应用顶栏/标签栏
   （低于 Element 弹层 ~2000，全屏内开下拉不受影响） */
.editor-wrap.w-e-full-screen-container {
  z-index: 1500;
}
.md-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 14px;
  border-bottom: 1px solid var(--border);
  flex: none;
}
.md-size {
  width: 90px;
}
/* 文字颜色按钮首字母配色（视觉提示按钮用途） */
/* 排版自定义按钮：图标+tooltip 形式（图标加大、清晰辨识） */
.md-ico-btn {
  padding: 6px;
}
.md-ico {
  font-size: 17px;
}
/* 文字颜色/背景色的字形按钮：A 字母，下划线色条 / 高亮底色区分用途 */
.md-a {
  font-size: 15px;
  font-weight: 800;
  line-height: 1;
  display: inline-block;
  padding: 0 1px 2px;
}
.md-a-fg {
  color: #e02020;
  border-bottom: 2.5px solid #e02020;
}
.md-a-bg {
  background: #ffe37e;
  border-radius: 2px;
}
/* 颜色下拉色板：色块小方格 */
.md-palette {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 2px;
  padding: 6px;
}
.md-palette .el-dropdown-menu__item {
  padding: 4px;
  line-height: 1;
}
.md-swatch {
  display: inline-block;
  width: 22px;
  height: 22px;
  border-radius: 4px;
  border: 1px solid var(--border, #dcdfe6);
}
.md-hint {
  font-size: 12px;
  color: var(--muted);
}
.md-exp-arrow {
  margin-left: 2px;
}
.md-vditor {
  flex: 1;
  min-height: 0;
}
/* vditor 本体铺满；去自带描边（外层已有多栏边框） */
.md-vditor :deep(.vditor) {
  height: 100%;
  border: none;
  border-radius: 0;
}
/* 全屏 z-index 自带只有 90，压不过应用顶栏/标签栏（同 wangeditor 的兜底，
   低于 Element 弹层 ~2000，全屏内开下拉不受影响） */
.md-vditor :deep(.vditor--fullscreen) {
  z-index: 1500;
}
.de-missing {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--muted);
}

/* 历史版本抽屉 */
.his-empty {
  padding: 24px 0;
  text-align: center;
  font-size: 13px;
  color: var(--muted);
}
.his-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 4px;
  border-bottom: 1px solid var(--border);
}
.his-main {
  flex: 1;
  min-width: 0;
}
.his-title {
  font-size: 13px;
  color: var(--ink);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.his-meta {
  font-size: 12px;
  color: var(--muted);
  margin-top: 2px;
}

/* 手机端 */
@media (max-width: 768px) {
  /* 窄屏工具条/单选已占满，提示文字省略 */
  .md-hint {
    display: none;
  }
}

/* 代码块复制按钮（最初版）：pre 内右上角，悬停 pre 显示。pre 的
   position:relative 必须走外部样式表——Slate 重渲染会抹 pre 的行内 style
   （此前「按钮漂到编辑器左端」的根因），样式表规则抹不掉 */
.we-editor :deep(pre) {
  position: relative;
}
.we-editor :deep(.cc-btn) {
  position: absolute;
  top: 6px;
  right: 8px;
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  cursor: pointer;
  user-select: none;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.15s;
  z-index: 5;
}
.we-editor :deep(pre:hover .cc-btn) {
  opacity: 1;
  pointer-events: auto;
}
.we-editor :deep(.cc-btn:hover) {
  background: rgba(0, 0, 0, 0.75);
}
</style>
