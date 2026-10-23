<template>
  <div class="gh">
    <div class="toolbar">
      <span class="updated">
        每 {{ pollLabel(pollSec) }}自动同步一次；每仓记录最近 {{ kept }} 条 Release，草稿不显示
      </span>
      <div class="tools">
        <el-select v-model="pollSel" size="small" class="poll-sel" @change="savePoll">
          <el-option v-for="o in pollOptions" :key="o" :label="`每 ${pollLabel(o)}同步`" :value="o" />
        </el-select>
        <el-button :loading="refreshing" @click="refreshAll">
          <el-icon v-if="!refreshing"><Refresh /></el-icon>
          <span v-if="!refreshing">全部刷新</span>
        </el-button>
        <el-button type="primary" @click="openAdd">
          <el-icon><Plus /></el-icon>
          <span>添加仓库</span>
        </el-button>
      </div>
    </div>

    <div v-if="repos.length" class="repo-grid">
      <el-card v-for="r in repos" :key="r.id" shadow="never" class="repo-card">
        <div class="r-head">
          <div class="r-title">
            <a class="r-name" :href="r.url" target="_blank" rel="noopener noreferrer" :title="`打开 GitHub 仓库 ${r.full_name}`">
              <span>{{ r.display_name || r.full_name }}</span>
              <span v-if="r.display_name" class="r-full">{{ r.full_name }}</span>
              <el-icon class="r-link"><TopRight /></el-icon>
            </a>
            <button v-if="r.has_new" type="button" class="r-new" title="有新版本发布，查看后点此清除提醒" @click="markSeen(r)">new</button>
          </div>
          <el-switch v-model="r.enabled" size="small" :disabled="busy[r.id]" @change="(v) => toggle(r, v)" />
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
              <el-tag v-if="rel.prerelease" size="small" type="warning" effect="plain">预发布</el-tag>
              <span v-if="rel.name && rel.name !== rel.tag_name" class="rel-name" :title="rel.name">{{ rel.name }}</span>
              <span class="rel-date">{{ rel.published_at ? fmtDate(rel.published_at) : '-' }}</span>
            </div>
            <template v-if="rel.body">
              <pre v-if="expanded[relKey(r, rel)]" class="rel-body">{{ rel.body }}</pre>
              <pre v-else class="rel-body clamp">{{ rel.body }}</pre>
              <button v-if="rel.body.length > 120 || rel.body.split('\n').length > 4" type="button" class="rel-more"
                @click="expanded[relKey(r, rel)] = !expanded[relKey(r, rel)]">
                {{ expanded[relKey(r, rel)] ? '收起说明' : '展开说明' }}
              </button>
            </template>
          </div>
          <button v-if="r.releases.length > 1" type="button" class="rel-more" @click="openMore(r)">
            查看更多（共 {{ r.releases.length }} 条）
          </button>
        </div>
        <div v-else class="r-empty">暂无 Release{{ r.last_ok_at ? '（该仓库还没有发布过版本）' : '' }}</div>

        <div class="r-foot">
          <span class="r-checked">{{ r.last_ok_at ? `上次同步 ${fmtTime(r.last_ok_at)}` : '尚未同步' }}</span>
          <div class="r-ops">
            <el-button link size="small" :disabled="busy[r.id]" @click="refreshOne(r)">刷新</el-button>
            <el-button link type="danger" size="small" @click="remove(r)">删除</el-button>
          </div>
        </div>
      </el-card>
    </div>
    <el-empty v-else-if="!loading" description="暂无监控仓库，点右上角「添加仓库」" />

    <!-- 添加仓库（append-to-body：violet 玻璃下 fixed 弹层必须逃出 .main，463 口径） -->
    <el-dialog v-model="addOpen" title="添加 GitHub 仓库" width="min(560px, 92vw)" append-to-body :close-on-click-modal="false">
      <div class="add-row">
        <el-input v-model="addInput" placeholder="owner/repo 或 https://github.com/owner/repo" clearable
          @keyup.enter="testAdd" />
        <el-button :loading="testing" @click="testAdd">测试</el-button>
      </div>
      <el-input v-model="addName" class="add-name" maxlength="64" placeholder="显示名称（选填，留空显示 owner/repo）" clearable />
      <div v-if="testResult" class="add-result">
        <template v-if="testResult.ok">
          <div class="add-ok">
            <span class="add-full">{{ testResult.owner }}/{{ testResult.repo }}</span>
            <span>最近 {{ testResult.releases.length }} 条 Release</span>
          </div>
          <div v-for="rel in testResult.releases" :key="rel.tag_name" class="add-rel">
            <span class="add-tag">{{ rel.tag_name }}</span>
            <el-tag v-if="rel.prerelease" size="small" type="warning" effect="plain">预发布</el-tag>
            <span class="add-date">{{ rel.published_at ? fmtDate(rel.published_at) : '-' }}</span>
          </div>
          <div v-if="!testResult.releases.length" class="add-none">该仓库还没有发布过 Release（仍可添加监控）</div>
        </template>
        <div v-else class="add-fail">{{ testResult.error }}</div>
      </div>
      <template #footer>
        <el-button @click="addOpen = false">取消</el-button>
        <el-button type="primary" :disabled="!canSave" :loading="saving" @click="saveAdd">添加</el-button>
      </template>
    </el-dialog>

    <!-- 查看更多：最近 5 条全量说明（append-to-body 遵 463 口径） -->
    <el-dialog v-model="moreOpen" :title="moreRepo ? (moreRepo.display_name || moreRepo.full_name) : 'Release 记录'"
      width="min(640px, 94vw)" append-to-body>
      <div v-if="moreRepo" class="r-list more-list">
        <div v-for="rel in moreRepo.releases" :key="rel.tag_name" class="rel">
          <div class="rel-head">
            <a class="rel-tag" :href="rel.html_url" target="_blank" rel="noopener noreferrer" :title="rel.html_url">
              {{ rel.tag_name }}
            </a>
            <el-tag v-if="rel.prerelease" size="small" type="warning" effect="plain">预发布</el-tag>
            <span v-if="rel.name && rel.name !== rel.tag_name" class="rel-name" :title="rel.name">{{ rel.name }}</span>
            <span class="rel-date">{{ rel.published_at ? fmtDate(rel.published_at) : '-' }}</span>
          </div>
          <pre v-if="rel.body" class="rel-body">{{ rel.body }}</pre>
        </div>
      </div>
      <div v-if="moreRepo && !moreRepo.releases.length" class="r-empty">该仓库还没有发布过 Release</div>
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
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api/index.js'
import { fmtDate, fmtTime } from '../utils/format.js'

const repos = ref([])
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

// 秒 → 「30 分钟」「4 小时」「24 小时」
function pollLabel(sec) {
  return sec % 3600 === 0 ? `${sec / 3600} 小时` : `${sec / 60} 分钟`
}

async function savePoll() {
  try {
    await api.put('/gh/settings', { poll_sec: pollSel.value })
    pollSec.value = pollSel.value
    ElMessage.success(`轮询间隔已改为 ${pollLabel(pollSel.value)}，下一轮生效`)
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
    ElMessage.success('已在后台开始刷新，稍候列表自动更新')
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
    else ElMessage.success(`已同步 ${r.full_name}`)
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
    await ElMessageBox.confirm(`删除「${r.full_name}」的发布监控？Release 记录一并清除。`, '删除', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api.del(`/gh/repos/${r.id}`)
    ElMessage.success('已删除')
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
    else ElMessage.success(`已添加 ${res.repo.full_name}`)
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
