<template>
  <div class="gh">
    <div class="toolbar">
      <span class="updated">
        {{ $t('gh.summary', { interval: pollLabel(pollSec), n: kept }) }}
      </span>
      <div class="tools">
        <el-select v-model="pollSel" size="small" class="poll-sel" @change="savePoll">
          <el-option v-for="o in pollOptions" :key="o" :label="$t('gh.pollOpt', { interval: pollLabel(o) })" :value="o" />
        </el-select>
        <el-button :loading="refreshing" @click="refreshAll">
          <el-icon v-if="!refreshing"><Refresh /></el-icon>
          <span v-if="!refreshing">{{ $t('gh.refreshAll') }}</span>
        </el-button>
        <el-button type="primary" @click="openAdd">
          <el-icon><Plus /></el-icon>
          <span>{{ $t('gh.addRepo') }}</span>
        </el-button>
      </div>
    </div>

    <div v-if="repos.length" class="repo-grid">
      <!-- 仓库卡可拖拽排序（2026-10-27）：drop 即存 /gh/repos/order -->
      <el-card
        v-for="(r, i) in repos"
        :key="r.id"
        shadow="never"
        draggable="true"
        class="repo-card"
        :class="{ dragging: repoDrag.from === i, 'drag-over': repoDrag.over === i && repoDrag.from !== i }"
        @dragstart="repoDrag.onStart(i, $event)"
        @dragenter="repoDrag.onEnter(i, $event)"
        @dragover="repoDrag.onOver(i, $event)"
        @drop="repoDrag.onDrop(i)"
        @dragend="repoDrag.onEnd"
      >
        <div class="r-head">
          <div class="r-title">
            <a class="r-name" :href="r.url" target="_blank" rel="noopener noreferrer" :title="$t('gh.openRepoTip', { name: r.full_name })">
              <span>{{ r.display_name || r.full_name }}</span>
              <span v-if="r.display_name" class="r-full">{{ r.full_name }}</span>
              <el-icon class="r-link"><TopRight /></el-icon>
            </a>
            <button v-if="r.has_new" type="button" class="r-new" :title="$t('gh.newTip')" @click="markSeen(r)">new</button>
          </div>
          <el-switch v-model="r.enabled" size="small" :disabled="busy[r.id]" @change="(v) => toggle(r, v)" />
        </div>

        <!-- 仓库元数据（2026-10-26）：Star / Watch / Fork，-1=尚未拉到不渲染 -->
        <div v-if="r.stars >= 0 || r.watchers >= 0 || r.forks >= 0" class="r-meta">
          <span v-if="r.stars >= 0" class="r-meta-item" :title="$t('gh.stars')">
            <svg class="r-meta-ico" viewBox="0 0 16 16" aria-hidden="true"><path :d="ICO_STAR" /></svg>{{ fmtCount(r.stars) }}
          </span>
          <span v-if="r.watchers >= 0" class="r-meta-item" :title="$t('gh.watching')">
            <svg class="r-meta-ico" viewBox="0 0 16 16" aria-hidden="true"><path :d="ICO_EYE" /></svg>{{ fmtCount(r.watchers) }}
          </span>
          <span v-if="r.forks >= 0" class="r-meta-item" :title="$t('gh.forks')">
            <svg class="r-meta-ico" viewBox="0 0 16 16" aria-hidden="true"><path :d="ICO_FORK" /></svg>{{ fmtCount(r.forks) }}
          </span>
        </div>

        <div v-if="r.last_error" class="r-err" :title="r.last_error">
          <el-icon><WarningFilled /></el-icon><span>{{ r.last_error }}</span>
        </div>

        <div v-if="r.releases.length" class="r-list">
          <div v-for="rel in r.releases.slice(0, 1)" :key="rel.tag_name" class="rel">
            <div class="rel-head">
              <a class="rel-tag" :href="rel.html_url" target="_blank" rel="noopener noreferrer" :title="rel.html_url"
                @click="markSeen(r)">
                {{ rel.tag_name }}
              </a>
              <el-tag v-if="rel.prerelease" size="small" type="warning" effect="plain">{{ $t('gh.prerelease') }}</el-tag>
              <span v-if="rel.name && rel.name !== rel.tag_name" class="rel-name" :title="rel.name">{{ rel.name }}</span>
              <span class="rel-date">{{ rel.published_at ? fmtDate(rel.published_at) : '-' }}</span>
            </div>
            <template v-if="rel.body">
              <pre v-if="expanded[relKey(r, rel)]" class="rel-body">{{ rel.body }}</pre>
              <pre v-else class="rel-body clamp">{{ rel.body }}</pre>
              <button v-if="rel.body.length > 120 || rel.body.split('\n').length > 4" type="button" class="rel-more"
                @click="expanded[relKey(r, rel)] = !expanded[relKey(r, rel)]">
                {{ expanded[relKey(r, rel)] ? $t('gh.collapseNotes') : $t('gh.expandNotes') }}
              </button>
            </template>
          </div>
          <button v-if="r.releases.length > 1" type="button" class="rel-more" @click="openMore(r)">
            {{ $t('gh.viewMore', { n: r.releases.length }) }}
          </button>
        </div>
        <div v-else class="r-empty">{{ $t('gh.noRelease') }}{{ r.last_ok_at ? $t('gh.noReleaseYet') : '' }}</div>

        <div class="r-foot">
          <span class="r-checked">{{ r.last_ok_at ? $t('gh.lastSync', { t: fmtTime(r.last_ok_at) }) : $t('gh.neverSynced') }}</span>
          <div class="r-ops">
            <el-button link size="small" :disabled="busy[r.id]" @click="refreshOne(r)">{{ $t('common.refresh') }}</el-button>
            <el-button link type="danger" size="small" @click="remove(r)">{{ $t('common.delete') }}</el-button>
          </div>
        </div>
      </el-card>
    </div>
    <el-empty v-else-if="!loading" :description="$t('gh.empty')" />

    <!-- 添加仓库（append-to-body：violet 玻璃下 fixed 弹层必须逃出 .main，463 口径） -->
    <el-dialog v-model="addOpen" :title="$t('gh.addTitle')" width="min(560px, 92vw)" append-to-body :close-on-click-modal="false">
      <div class="add-row">
        <el-input v-model="addInput" :placeholder="$t('gh.addPh')" clearable
          @keyup.enter="testAdd" />
        <el-button :loading="testing" @click="testAdd">{{ $t('gh.test') }}</el-button>
      </div>
      <el-input v-model="addName" class="add-name" maxlength="64" :placeholder="$t('gh.namePh')" clearable />
      <div v-if="testResult" class="add-result">
        <template v-if="testResult.ok">
          <div class="add-ok">
            <span class="add-full">{{ testResult.owner }}/{{ testResult.repo }}</span>
            <span>{{ $t('gh.recentN', { n: testResult.releases.length }) }}</span>
          </div>
          <div v-for="rel in testResult.releases" :key="rel.tag_name" class="add-rel">
            <span class="add-tag">{{ rel.tag_name }}</span>
            <el-tag v-if="rel.prerelease" size="small" type="warning" effect="plain">{{ $t('gh.prerelease') }}</el-tag>
            <span class="add-date">{{ rel.published_at ? fmtDate(rel.published_at) : '-' }}</span>
          </div>
          <div v-if="!testResult.releases.length" class="add-none">{{ $t('gh.noneYet') }}</div>
        </template>
        <div v-else class="add-fail">{{ testResult.error }}</div>
      </div>
      <template #footer>
        <el-button @click="addOpen = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :disabled="!canSave" :loading="saving" @click="saveAdd">{{ $t('gh.add') }}</el-button>
      </template>
    </el-dialog>

    <!-- 查看更多：最近 5 条全量说明（append-to-body 遵 463 口径） -->
    <el-dialog v-model="moreOpen" :title="moreRepo ? (moreRepo.display_name || moreRepo.full_name) : $t('gh.moreTitle')"
      width="min(640px, 94vw)" append-to-body>
      <div v-if="moreRepo" class="r-list more-list">
        <div v-for="rel in moreRepo.releases" :key="rel.tag_name" class="rel">
          <div class="rel-head">
            <a class="rel-tag" :href="rel.html_url" target="_blank" rel="noopener noreferrer" :title="rel.html_url">
              {{ rel.tag_name }}
            </a>
            <el-tag v-if="rel.prerelease" size="small" type="warning" effect="plain">{{ $t('gh.prerelease') }}</el-tag>
            <span v-if="rel.name && rel.name !== rel.tag_name" class="rel-name" :title="rel.name">{{ rel.name }}</span>
            <span class="rel-date">{{ rel.published_at ? fmtDate(rel.published_at) : '-' }}</span>
          </div>
          <pre v-if="rel.body" class="rel-body">{{ rel.body }}</pre>
        </div>
      </div>
      <div v-if="moreRepo && !moreRepo.releases.length" class="r-empty">{{ $t('gh.noneYetShort') }}</div>
    </el-dialog>
  </div>
</template>

<script setup>
// GitHub 仓库发布监控（466）：仓库卡片网格 + 最近 5 条 Release 时间线（tag 可
// 点直达 release 页、仓库名可点直达仓库页），后台常驻轮询入库，添加弹窗
// 先测后存（实拉 GitHub API 预览），启停只停轮询不清快照。卡片有 seen_at
// 之后发布的 Release 时显 new 角标（489），查看后清账。
defineOptions({ name: 'GhView' })
import { computed, onActivated, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api/index.js'
import { useDragSort } from '../composables/useDragSort.js'
import { fmtDate, fmtTime } from '../utils/format.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/svctabs.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

const repos = ref([])
// 仓库卡拖拽排序：drop 后本地已重排，落库失败只提示
const repoDrag = useDragSort(repos, async (ids) => {
  try {
    await api.put('/gh/repos/order', { ids })
  } catch (e) {
    ElMessage.error(e.message)
  }
})
const kept = ref(5)
const loading = ref(false)
const refreshing = ref(false)
const busy = reactive({}) // 单仓刷新/切换中的 id 集合
const expanded = reactive({}) // 展开更新说明的 release key 集合

// 轮询间隔（秒）：服务端七档白名单，默认 4 小时；pollSel 为选择器绑定值，
// 保存失败回退 pollSec（服务端生效值）
const pollSec = ref(14400)
const pollSel = ref(14400)
const pollOptions = ref([1800, 3600, 7200, 14400, 21600, 43200, 86400])

const addOpen = ref(false)
const addInput = ref('')
const addName = ref('') // 显示名称（选填，472）
const testing = ref(false)
const saving = ref(false)
const testResult = ref(null)

const moreOpen = ref(false)
const moreRepo = ref(null) // 查看更多弹层当前展示的仓库（471）

const canSave = computed(() => Boolean(testResult.value?.ok))

function relKey(r, rel) {
  return `${r.id}:${rel.tag_name}`
}

// 仓库元数据三枚（2026-10-26）：GitHub octicon 图标（MIT）；计数 ≥1 万缩写 k
const ICO_STAR = 'M8 .25a.75.75 0 0 1 .673.418l1.882 3.815 4.21.612a.75.75 0 0 1 .416 1.279l-3.046 2.97.719 4.192a.751.751 0 0 1-1.088.791L8 12.347l-3.766 1.98a.75.75 0 0 1-1.088-.79l.72-4.194L.818 6.374a.75.75 0 0 1 .416-1.28l4.21-.611L7.327.668A.75.75 0 0 1 8 .25Zm0 2.445L6.615 5.5a.75.75 0 0 1-.564.41l-3.097.45 2.24 2.184a.75.75 0 0 1 .216.664l-.528 3.084 2.769-1.456a.75.75 0 0 1 .698 0l2.77 1.456-.53-3.084a.75.75 0 0 1 .216-.664l2.24-2.183-3.096-.45a.75.75 0 0 1-.564-.41L8 2.694Z'
const ICO_EYE = 'M8 2c1.981 0 3.671.992 4.933 2.078 1.27 1.091 2.187 2.345 2.637 3.023a1.62 1.62 0 0 1 0 1.798c-.45.678-1.367 1.932-2.637 3.023C11.67 13.008 9.981 14 8 14c-1.981 0-3.671-.992-4.933-2.078C1.797 10.83.88 9.576.43 8.898a1.62 1.62 0 0 1 0-1.798c.45-.677 1.367-1.931 2.637-3.022C4.33 2.992 6.019 2 8 2ZM1.679 7.932a.12.12 0 0 0 0 .136c.411.622 1.241 1.75 2.366 2.717C5.176 11.758 6.527 12.5 8 12.5c1.473 0 2.825-.742 3.955-1.715 1.124-.967 1.954-2.096 2.366-2.717a.12.12 0 0 0 0-.136c-.412-.621-1.242-1.75-2.366-2.717C10.824 4.242 9.473 3.5 8 3.5c-1.473 0-2.825.742-3.955 1.715-1.124.967-1.954 2.096-2.366 2.717ZM8 10a2 2 0 1 1-.001-4 2 2 0 0 1 0 4Z'
const ICO_FORK = 'M5 5.372v.878c0 .414.336.75.75.75h4.5a.75.75 0 0 0 .75-.75v-.878a2.25 2.25 0 1 1 1.5 0v.878a2.25 2.25 0 0 1-2.25 2.25h-1.5v2.128a2.251 2.251 0 1 1-1.5 0V8.5h-1.5A2.25 2.25 0 0 1 3.5 6.25v-.878a2.25 2.25 0 1 1 1.5 0ZM5 3.25a.75.75 0 1 0-1.5 0 .75.75 0 0 0 1.5 0Zm6.75.75a.75.75 0 1 0 0-1.5.75.75 0 0 0 0 1.5Zm-3 8.75a.75.75 0 1 0-1.5 0 .75.75 0 0 0 1.5 0Z'

function fmtCount(n) {
  if (n >= 100000) return Math.round(n / 1000) + 'k'
  if (n >= 10000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'k'
  return String(n)
}

// 秒 → 「30 分钟」「4 小时」「24 小时」
function pollLabel(sec) {
  return sec % 3600 === 0 ? t('gh.hours', { n: sec / 3600 }) : t('gh.minutes', { n: sec / 60 })
}

async function savePoll() {
  try {
    await api.put('/gh/settings', { poll_sec: pollSel.value })
    pollSec.value = pollSel.value
    ElMessage.success(t('gh.pollSaved', { interval: pollLabel(pollSel.value) }))
  } catch (e) {
    pollSel.value = pollSec.value
    ElMessage.error(e.message)
  }
}

async function load(showLoading = true) {
  if (showLoading) loading.value = true
  try {
    const res = await api.get('/gh/repos')
    repos.value = res.repos || []
    kept.value = res.kept || 5
    if (res.poll_sec) pollSec.value = res.poll_sec
    pollSel.value = pollSec.value
    if (Array.isArray(res.poll_options) && res.poll_options.length) pollOptions.value = res.poll_options
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

// 卡片 new 角标记账（489）：查看更多弹层/点开最新 Release/点角标任一即整卡
// 清账（seen_at 推进到 now），角标就地消除、主页「有更新」计数随之回落；
// 记账失败回弹角标，下次进页以服务端为准
async function markSeen(r) {
  if (!r.has_new) return
  r.has_new = false
  try {
    await api.post(`/gh/repos/${r.id}/seen`)
  } catch {
    r.has_new = true
  }
}

async function refreshAll() {
  refreshing.value = true
  try {
    await api.post('/gh/refresh')
    ElMessage.success(t('gh.refreshStarted'))
    setTimeout(() => load(false), 1500)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    refreshing.value = false
  }
}

async function refreshOne(r) {
  busy[r.id] = true
  try {
    const res = await api.post(`/gh/repos/${r.id}/refresh`)
    if (res.warn) ElMessage.warning(res.warn)
    else ElMessage.success(t('gh.synced', { name: r.full_name }))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    delete busy[r.id]
  }
  load(false)
}

async function toggle(r, v) {
  busy[r.id] = true
  try {
    await api.put(`/gh/repos/${r.id}`, { enabled: v })
    if (v) setTimeout(() => load(false), 1500) // 启用触发补刷，稍候拉新快照
  } catch (e) {
    r.enabled = !v
    ElMessage.error(e.message)
  } finally {
    delete busy[r.id]
  }
}

async function remove(r) {
  try {
    await ElMessageBox.confirm(t('gh.delConfirm', { name: r.full_name }), t('common.delete'), {
      type: 'warning',
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/gh/repos/${r.id}`)
    ElMessage.success(t('common.deleted'))
  } catch (e) {
    ElMessage.error(e.message)
  }
  load(false)
}

function openAdd() {
  addInput.value = ''
  addName.value = ''
  testResult.value = null
  addOpen.value = true
}

function openMore(r) {
  moreRepo.value = r
  moreOpen.value = true
  markSeen(r) // 看过全部记录即整卡清账（489）
}

async function testAdd() {
  const v = addInput.value.trim()
  if (!v) return
  testing.value = true
  testResult.value = null
  try {
    const res = await api.post('/gh/test', { repo: v })
    testResult.value = res.result
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testing.value = false
  }
}

async function saveAdd() {
  if (!testResult.value?.ok) return
  saving.value = true
  try {
    const res = await api.post('/gh/repos', { repo: `${testResult.value.owner}/${testResult.value.repo}`, display_name: addName.value.trim() })
    if (res.warn) ElMessage.warning(res.warn)
    else ElMessage.success(t('gh.added', { name: res.repo.full_name }))
    addOpen.value = false
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
  load(false)
}

onMounted(() => load())
onActivated(() => load())
</script>

<style scoped>
.gh {
  display: flex;
  flex-direction: column;
  gap: 12px;
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
.poll-sel {
  width: 128px;
}
.updated {
  color: var(--muted);
  font-size: 12px;
}

.repo-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
  gap: 12px;
}
.repo-card.dragging {
  opacity: 0.45;
}
.repo-card.drag-over {
  border-color: var(--brand);
  box-shadow: inset 0 0 0 1px var(--brand);
}
.repo-card :deep(.el-card__body) {
  padding: 14px 16px 10px;
}
.r-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}
/* 仓库元数据（2026-10-26）：Star/Watch/Fork 三枚小字，GitHub octicon 随行 */
.r-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: -2px 0 8px;
  font-size: 12px;
  opacity: 0.85;
}
.r-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-variant-numeric: tabular-nums;
}
.r-meta-ico {
  width: 13px;
  height: 13px;
  fill: currentColor;
  opacity: 0.8;
}
.r-title {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
}
/* 489 new 角标：seen_at 之后有 Release 即显，查看后点角标/弹层/最新 tag 清账 */
.r-new {
  flex: none;
  border: none;
  cursor: pointer;
  padding: 0 7px;
  border-radius: 999px;
  background: var(--crit, #f56c6c);
  color: #fff;
  font-size: 11px;
  font-weight: 700;
  line-height: 18px;
  font-family: inherit;
}
.r-new:hover {
  filter: brightness(1.12);
}
.r-name {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 15px;
  font-weight: 700;
  color: var(--ink, var(--el-text-color-primary));
  text-decoration: none;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.r-name:hover {
  color: var(--el-color-primary);
}
.r-link {
  flex: none;
  font-size: 12px;
  opacity: 0.55;
}
.r-full {
  flex: none;
  font-weight: 400;
  font-size: 11px;
  color: var(--muted);
  overflow: hidden;
  text-overflow: ellipsis;
}
.r-err {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: var(--crit, #f56c6c);
  font-size: 12px;
  line-height: 1.6;
  margin-bottom: 6px;
  word-break: break-all;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

/* Release 时间线：左侧竖线 + 圆点，tag 行 + 可折叠更新说明 */
.r-list {
  display: flex;
  flex-direction: column;
}
.rel {
  position: relative;
  padding: 6px 0 8px 16px;
  border-left: 1px dashed var(--border, #ebeef5);
}
.rel:last-child {
  border-left-color: transparent;
}
.rel::before {
  content: '';
  position: absolute;
  left: -3.5px;
  top: 13px;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--el-color-primary);
  opacity: 0.75;
}
.rel-head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.rel-tag {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  font-weight: 700;
  color: var(--el-color-primary);
  text-decoration: none;
  white-space: nowrap;
}
.rel-tag:hover {
  text-decoration: underline;
}
.rel-name {
  color: var(--muted);
  font-size: 12px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rel-date {
  margin-left: auto;
  flex: none;
  color: var(--muted);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.rel-body {
  margin: 4px 0 0;
  padding: 6px 8px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--el-fill-color, #f5f7fa) 65%, transparent);
  font-family: inherit;
  font-size: 12px;
  line-height: 1.6;
  color: var(--muted);
  white-space: pre-wrap;
  word-break: break-word;
  overflow: hidden;
}
.rel-body.clamp {
  display: -webkit-box;
  -webkit-line-clamp: 4;
  -webkit-box-orient: vertical;
}
.rel-more {
  border: none;
  background: none;
  padding: 2px 0 0;
  font-size: 12px;
  color: var(--el-color-primary);
  cursor: pointer;
}
.r-empty {
  color: var(--muted);
  font-size: 13px;
  padding: 10px 0 6px;
}
.r-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px dashed var(--border, #ebeef5);
  margin-top: 8px;
  padding-top: 6px;
}
.r-checked {
  color: var(--muted);
  font-size: 12px;
}
.r-ops {
  display: flex;
  align-items: center;
}

/* 添加弹窗 */
.add-row {
  display: flex;
  gap: 8px;
}
.add-name {
  margin-top: 10px;
}
.more-list {
  max-height: 60vh;
  overflow: auto;
}
.add-result {
  margin-top: 10px;
  font-size: 13px;
}
.add-ok {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}
.add-full {
  font-weight: 700;
}
.add-rel {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 0;
  border-top: 1px dashed var(--border, #ebeef5);
}
.add-tag {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-weight: 600;
}
.add-date {
  margin-left: auto;
  color: var(--muted);
  font-size: 12px;
}
.add-none,
.add-fail {
  color: var(--muted);
  font-size: 12px;
  padding: 4px 0;
}
.add-fail {
  color: var(--crit, #f56c6c);
}
</style>
