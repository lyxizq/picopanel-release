<template>
  <div class="webcheck">
    <div class="toolbar">
      <span class="updated">{{ $t('webcheck.summary') }}</span>
      <div class="tools">
        <el-button :loading="refreshing" @click="refreshAll">
          <el-icon v-if="!refreshing"><Refresh /></el-icon>
          <span v-if="!refreshing">{{ $t('webcheck.refreshAll') }}</span>
        </el-button>
        <el-button type="primary" @click="openAdd">
          <el-icon><Plus /></el-icon>
          <span>{{ $t('webcheck.addTarget') }}</span>
        </el-button>
      </div>
    </div>

    <div v-if="checks.length" class="wc-grid">
      <!-- 目标卡可拖拽排序：drop 即存 /webchecks/order -->
      <el-card
        v-for="(w, i) in checks"
        :key="w.id"
        shadow="never"
        draggable="true"
        class="wc-card"
        :class="{ dragging: wcDrag.from === i, 'drag-over': wcDrag.over === i && wcDrag.from !== i }"
        @dragstart="wcDrag.onStart(i, $event)"
        @dragenter="wcDrag.onEnter(i, $event)"
        @dragover="wcDrag.onOver(i, $event)"
        @drop="wcDrag.onDrop(i)"
        @dragend="wcDrag.onEnd"
      >
        <div class="w-head">
          <div class="w-title">
            <span class="w-dot" :class="dotClass(w)"></span>
            <span class="w-name" :title="w.url">{{ w.name || w.url }}</span>
            <el-tag v-if="!w.enabled" size="small" type="info" effect="plain">{{ $t('webcheck.disabled') }}</el-tag>
          </div>
          <el-switch v-model="w.enabled" size="small" :disabled="busy[w.id]" @change="(v) => toggle(w, v)" />
        </div>
        <div class="w-url">{{ w.url }}</div>

        <div v-if="w.rules?.length" class="w-kws">
          <template v-for="(rule, ri) in w.rules.slice(0, 4)" :key="ri">
            <span v-if="ri > 0" class="w-or">{{ $t('webcheck.ruleOr') }}</span>
            <span class="w-rule">
              <el-tag v-for="k in rule.slice(0, 6)" :key="k" size="small" effect="plain" class="w-kw">{{ k }}</el-tag>
              <span v-if="rule.length > 6" class="w-kw-more">+{{ rule.length - 6 }}</span>
            </span>
          </template>
          <span v-if="w.rules.length > 4" class="w-kw-more">+{{ w.rules.length - 4 }}</span>
        </div>

        <div v-if="w.up === 0 && w.last_error" class="w-err" :title="w.last_error">{{ w.last_error }}</div>

        <div class="w-meta">
          <span v-if="w.up !== -1 && w.since" :class="w.up === 0 ? 'bad' : ''">{{ stateSpan(w) }}</span>
          <span v-else>{{ $t('webcheck.notChecked') }}</span>
          <span v-if="w.checked_at"> · {{ $t('webcheck.checkedAt', { t: fmtTime(w.checked_at) }) }}</span>
        </div>

        <div class="w-foot">
          <span class="w-state" :class="dotClass(w)">{{ stateText(w) }}</span>
          <div class="w-ops">
            <el-button link size="small" :loading="checkingId === w.id" @click="checkOne(w)">{{ $t('webcheck.check') }}</el-button>
            <el-button link size="small" @click="openEdit(w)">{{ $t('common.edit') }}</el-button>
            <el-button link type="danger" size="small" @click="remove(w)">{{ $t('common.delete') }}</el-button>
          </div>
        </div>
      </el-card>
    </div>
    <el-empty v-else-if="!loading" :description="$t('webcheck.empty')" />

    <!-- 添加/编辑目标（append-to-body：violet 玻璃下 fixed 弹层必须逃出 .main，463 口径） -->
    <el-dialog v-model="dlg" :title="f.id ? $t('webcheck.editTitle') : $t('webcheck.addTitle')" width="min(560px, 92vw)"
      append-to-body :close-on-click-modal="false" :before-close="formGuard.beforeClose">
      <el-form label-width="90px">
        <el-form-item :label="$t('webcheck.labelName')">
          <el-input v-model="f.name" :placeholder="$t('webcheck.namePh')" maxlength="64" clearable />
        </el-form-item>
        <el-form-item :label="$t('webcheck.labelUrl')">
          <el-input v-model="f.url" :placeholder="$t('webcheck.urlPh')" maxlength="512" clearable />
          <div class="w-hint">{{ $t('webcheck.urlHint') }}</div>
        </el-form-item>
        <!-- 多规则（2026-11-03 改版）：规则内「且」、规则间「或」，任一规则满足即通过 -->
        <template v-for="(rt, ri) in f.ruleTexts" :key="ri">
          <el-form-item :label="ri === 0 ? $t('webcheck.labelRules') : ''">
            <div class="rule-row">
              <el-input v-model="f.ruleTexts[ri]" type="textarea" :rows="2" :placeholder="ri === 0 ? $t('webcheck.ruleKeywordsPh') : $t('webcheck.ruleN', { n: ri + 1 })" />
              <el-button v-if="f.ruleTexts.length > 1" class="rule-del" link type="danger" :title="$t('common.delete')" @click="removeRule(ri)">
                <el-icon><Delete /></el-icon>
              </el-button>
            </div>
          </el-form-item>
        </template>
        <el-form-item label="">
          <div class="rule-extra">
            <el-button link type="primary" @click="f.ruleTexts.push('')">
              <el-icon><Plus /></el-icon>
              <span>{{ $t('webcheck.addRule') }}</span>
            </el-button>
          </div>
          <div class="w-hint">{{ $t('webcheck.ruleHint') }}</div>
        </el-form-item>
        <el-form-item :label="$t('webcheck.labelEnabled')">
          <el-switch v-model="f.enabled" />
        </el-form-item>
      </el-form>
      <div v-if="testResult" class="test-result">
        <template v-if="testResult.ok">
          <div class="tr-ok">{{ $t('webcheck.testOk', { n: testResult.matched_rule }) }}</div>
        </template>
        <template v-else>
          <div class="tr-fail">{{ $t('webcheck.testFail', { reason: testResult.error }) }}</div>
          <div v-for="(missing, ri) in testResult.missing || []" :key="ri" class="tr-missing">
            <span class="tr-missing-label">{{ $t('webcheck.ruleMissing', { n: ri + 1 }) }}：</span>
            <el-tag v-for="k in missing" :key="k" size="small" type="danger" effect="plain" class="w-kw">{{ k }}</el-tag>
          </div>
        </template>
      </div>
      <template #footer>
        <el-button @click="test" :loading="testing">{{ $t('webcheck.test') }}</el-button>
        <el-button @click="dlg = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// 网站检查（服务监控页新 tab，2026-11-03）：目标卡片网格（URL + 关键字全包含
// 判定），后台每 2 小时轮询（夜间 0:00–6:00 静默），状态翻转推消息；添加/编辑
// 弹窗先测后存（实抓页面看关键字命中）。页面读库直出，检测后本地更新。
defineOptions({ name: 'WebCheckView' })
import { onActivated, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, Plus, Refresh } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { fmtTime } from '../utils/format.js'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { useDragSort } from '../composables/useDragSort.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/svctabs.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

const checks = ref([])
const loading = ref(false)
const refreshing = ref(false)
const busy = reactive({}) // 启停切换中的 id 集合
const checkingId = ref(0)

// 目标卡拖拽排序：drop 后本地已重排，落库失败只提示
const wcDrag = useDragSort(checks, async (ids) => {
  try {
    await api.put('/webchecks/order', { ids })
  } catch (e) {
    ElMessage.error(e.message)
  }
})

const dlg = ref(false)
const saving = ref(false)
const testing = ref(false)
const testResult = ref(null)
// 弹窗表单：ruleTexts 为多规则编辑器（每条规则一个 textarea，一行一个关键字），
// 保存/测试时逐条拆行成 rules [][]string（2026-11-03 改版：规则内且、规则间或）
const f = ref({ id: 0, name: '', url: '', ruleTexts: [''], enabled: true })
const formGuard = useDirtyGuard(() => (dlg.value ? { ...f.value, ruleTexts: [...f.value.ruleTexts] } : null))

// 拆单条规则 textarea → 关键字清单（去空行）
function ruleKws(text) {
  return text.split('\n').map((s) => s.trim()).filter(Boolean)
}
// 全部规则 → API 的 rules 形态（空规则丢弃）
function rulesPayload() {
  return f.value.ruleTexts.map(ruleKws).filter((kws) => kws.length > 0)
}

function removeRule(ri) {
  f.value.ruleTexts.splice(ri, 1)
}

function dotClass(w) {
  if (w.up === 1) return 'ok'
  if (w.up === 0) return 'bad'
  return 'unknown'
}
function stateText(w) {
  if (w.up === 1) return t('webcheck.stateOk')
  if (w.up === 0) return t('webcheck.stateFail')
  return t('webcheck.notChecked')
}
// 任意秒数跨度的人话（状态持续时长）
function fmtSpan(secs) {
  if (secs < 60) return t('webcheck.durSec', { n: secs })
  if (secs < 3600) return t('webcheck.durMin', { n: Math.floor(secs / 60) })
  if (secs < 86400) return t('webcheck.durHour', { h: Math.floor(secs / 3600), m: Math.floor((secs % 3600) / 60) })
  return t('webcheck.durDay', { d: Math.floor(secs / 86400), h: Math.floor((secs % 86400) / 3600) })
}
function stateSpan(w) {
  const dur = fmtSpan(Math.max(0, Math.floor(Date.now() / 1000) - w.since))
  return w.up === 1 ? t('webcheck.okSince', { t: dur }) : t('webcheck.failSince', { t: dur })
}

function replaceLocal(row) {
  const i = checks.value.findIndex((x) => x.id === row.id)
  if (i >= 0) checks.value[i] = { ...checks.value[i], ...row }
}

async function load(showLoading = true) {
  if (showLoading) loading.value = true
  try {
    const res = await api.get('/webchecks')
    checks.value = res.checks || []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

async function refreshAll() {
  refreshing.value = true
  try {
    await api.post('/webchecks/refresh')
    ElMessage.success(t('webcheck.kickStarted'))
    setTimeout(() => load(false), 3000)
    setTimeout(() => load(false), 8000)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    refreshing.value = false
  }
}

async function checkOne(w) {
  checkingId.value = w.id
  try {
    const res = await api.post(`/webchecks/${w.id}/check`)
    if (res.check) replaceLocal(res.check)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    checkingId.value = 0
  }
}

async function toggle(w, enabled) {
  busy[w.id] = true
  try {
    await api.put(`/webchecks/${w.id}`, { name: w.name, url: w.url, rules: w.rules, enabled })
    if (enabled) setTimeout(() => load(false), 1500) // 启用触发即检一轮
  } catch (e) {
    w.enabled = !enabled
    ElMessage.error(e.message)
  } finally {
    delete busy[w.id]
  }
}

async function remove(w) {
  const label = w.name || w.url
  try {
    await ElMessageBox.confirm(t('webcheck.delConfirm', { name: label }), t('common.delete'), {
      type: 'warning', confirmButtonText: t('common.delete'), cancelButtonText: t('common.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/webchecks/${w.id}`)
    ElMessage.success(t('common.deleted'))
  } catch (e) {
    ElMessage.error(e.message)
  }
  load(false)
}

function openAdd() {
  f.value = { id: 0, name: '', url: '', ruleTexts: [''], enabled: true }
  testResult.value = null
  dlg.value = true
  formGuard.snapshot()
}
function openEdit(w) {
  // 每条规则回填为一个 textarea（关键字换行拼接）；无规则行兜底一条空规则
  const ruleTexts = (w.rules || []).map((kws) => kws.join('\n'))
  f.value = { id: w.id, name: w.name, url: w.url, ruleTexts: ruleTexts.length ? ruleTexts : [''], enabled: w.enabled }
  testResult.value = null
  dlg.value = true
  formGuard.snapshot()
}

// 未入库检查测试（弹窗「测试」按钮）：按当前表单实抓页面做多规则判定
async function test() {
  const rules = rulesPayload()
  if (!f.value.url.trim() || !rules.length) {
    testResult.value = null
    return
  }
  testing.value = true
  testResult.value = null
  try {
    const res = await api.post('/webchecks/test', { name: f.value.name, url: f.value.url.trim(), rules })
    testResult.value = res.result
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testing.value = false
  }
}

async function save() {
  const rules = rulesPayload()
  if (!f.value.url.trim()) {
    ElMessage.warning(t('webcheck.urlPh'))
    return
  }
  if (!rules.length) {
    ElMessage.warning(t('webcheck.ruleKeywordsPh'))
    return
  }
  saving.value = true
  try {
    const body = { name: f.value.name.trim(), url: f.value.url.trim(), rules, enabled: f.value.enabled }
    let row
    if (f.value.id) {
      const res = await api.put(`/webchecks/${f.value.id}`, body)
      row = res.check
      ElMessage.success(t('webcheck.saved'))
    } else {
      const res = await api.post('/webchecks', body)
      row = res.check
      ElMessage.success(t('webcheck.added', { name: (row && (row.name || row.url)) || body.url }))
    }
    if (row) replaceLocal(row)
    dlg.value = false
    load(false)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

onMounted(() => load())
onActivated(() => load())
</script>

<style scoped>
.webcheck {
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
.updated {
  color: var(--muted);
  font-size: 12px;
}

.wc-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 12px;
}
.wc-card.dragging {
  opacity: 0.45;
}
.wc-card.drag-over {
  border-color: var(--brand);
  box-shadow: inset 0 0 0 1px var(--brand);
}
.wc-card :deep(.el-card__body) {
  padding: 14px 16px 12px;
}
.w-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.w-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.w-dot {
  flex: none;
  width: 9px;
  height: 9px;
  border-radius: 50%;
}
.w-dot.ok {
  background: var(--el-color-success, #67c23a);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--el-color-success, #67c23a) 18%, transparent);
}
.w-dot.bad {
  background: var(--crit, #f56c6c);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--crit, #f56c6c) 18%, transparent);
}
.w-dot.unknown {
  background: var(--muted, #909399);
}
.w-name {
  font-size: 15px;
  font-weight: 700;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.w-url {
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 12.5px;
  color: var(--muted);
  margin-top: 6px;
  word-break: break-all;
}
.w-kws {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 8px;
}
/* 规则分组（2026-11-03 多规则）：组内虚线框包住且关系的关键字，组间「或」相连 */
.w-rule {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  border: 1px dashed var(--border, #dcdfe6);
  border-radius: 6px;
}
.w-or {
  color: var(--muted);
  font-size: 11.5px;
  flex: none;
}
.w-kw {
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.w-kw-more {
  color: var(--muted);
  font-size: 12px;
}
.w-err {
  color: var(--crit, #f56c6c);
  font-size: 12px;
  line-height: 1.6;
  margin-top: 6px;
  word-break: break-all;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.w-meta {
  color: var(--muted);
  font-size: 12px;
  margin-top: 6px;
}
.w-meta .bad {
  color: var(--crit, #f56c6c);
}
.w-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px dashed var(--border, #ebeef5);
  margin-top: 10px;
  padding-top: 8px;
}
.w-state {
  font-size: 12.5px;
  font-weight: 600;
}
.w-state.ok {
  color: var(--el-color-success, #67c23a);
}
.w-state.bad {
  color: var(--crit, #f56c6c);
}
.w-state.unknown {
  color: var(--muted);
}
.w-ops {
  display: flex;
  align-items: center;
}

/* 弹窗 */
.w-hint {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
  margin-top: 2px;
}
/* 多规则编辑器：textarea 占满 + 右侧删除钮；添加规则按钮行 */
.rule-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  width: 100%;
}
.rule-row .el-textarea {
  flex: 1;
}
.rule-del {
  margin-top: 4px;
}
.rule-extra {
  display: flex;
  align-items: center;
  margin-bottom: 4px;
}
.test-result {
  border-top: 1px dashed var(--border, #ebeef5);
  padding-top: 10px;
  font-size: 13px;
}
.tr-ok {
  color: var(--el-color-success, #67c23a);
}
.tr-fail {
  color: var(--crit, #f56c6c);
  word-break: break-all;
}
.tr-missing {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 8px;
}
.tr-missing-label {
  color: var(--muted);
  font-size: 12px;
}
</style>
