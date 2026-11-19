<template>
  <el-card shadow="never">
    <template #header>
      <div class="head">
        <span>{{ $t('alertRules.title') }}</span>
        <el-button type="primary" :icon="Plus" @click="openForm()">{{ $t('alertRules.create') }}</el-button>
      </div>
    </template>

    <el-table :data="rules" v-loading="loading" :empty-text="$t('alertRules.empty')" class="row-table">
      <el-table-column prop="name" :label="$t('alertRules.colName')" min-width="140" show-overflow-tooltip />
      <el-table-column :label="$t('alertRules.colCondition')" min-width="200">
        <template #default="{ row }">
          {{ conditionText(row) }}
        </template>
      </el-table-column>
      <el-table-column :label="$t('alertRules.colDuration')" width="100">
        <template #default="{ row }">
          {{ row.duration_sec ? $t('alertRules.durationMin', { n: row.duration_sec / 60 }) : $t('alertRules.immediate') }}
        </template>
      </el-table-column>
      <el-table-column :label="$t('alertRules.colScope')" width="150" show-overflow-tooltip>
        <template #default="{ row }">
          {{ row.server_id ? serverName(row.server_id) : $t('alertRules.allServers') }}
        </template>
      </el-table-column>
      <el-table-column :label="$t('alertRules.colEnabled')" width="80">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="(v) => toggleEnabled(row, v)" />
        </template>
      </el-table-column>
      <el-table-column :label="$t('alertRules.colOps')" width="130" fixed="right">
        <template #default="{ row }">
          <el-button size="small" text type="primary" @click="openForm(row)">{{ $t('common.edit') }}</el-button>
          <el-button size="small" text type="danger" @click="remove(row)">{{ $t('common.delete') }}</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>

  <el-dialog append-to-body v-model="formVisible" :title="editing ? $t('alertRules.editTitle') : $t('alertRules.create')" width="520px" :before-close="ruleGuard.beforeClose">
    <el-form :model="form" label-width="90px">
      <el-form-item :label="$t('alertRules.fName')">
        <el-input v-model="form.name" :placeholder="$t('alertRules.phName')" />
      </el-form-item>
      <el-form-item :label="$t('alertRules.fMetric')">
        <el-select v-model="form.metric" style="width: 100%">
          <el-option v-for="m in metricOptions" :key="m.value" :label="$t(m.label)" :value="m.value" />
        </el-select>
      </el-form-item>
      <el-form-item
        v-if="form.metric === 'disk' || form.metric === 'net_rx' || form.metric === 'net_tx' || form.metric === 'port'"
        :label="$t('alertRules.fParam')"
      >
        <el-input v-model="form.param" :placeholder="paramPlaceholder" />
      </el-form-item>
      <el-form-item :label="$t('alertRules.fCondition')">
        <div v-if="form.metric === 'port'" class="cond-row">
          <el-select v-model="portMode" style="flex: 1">
            <el-option :label="$t('alertRules.portDown')" value="down" />
            <el-option :label="$t('alertRules.portUp')" value="up" />
          </el-select>
        </div>
        <div v-else class="cond-row">
          <el-select v-model="form.op" style="width: 100px">
            <el-option :label="$t('alertRules.opGt')" value=">" />
            <el-option :label="$t('alertRules.opLt')" value="<" />
          </el-select>
          <el-input-number v-model="form.threshold" :min="0" :step="thresholdStep" style="flex: 1" />
          <span class="unit">{{ thresholdUnit }}</span>
        </div>
      </el-form-item>
      <el-form-item :label="$t('alertRules.fDuration')">
        <div class="cond-row">
          <el-input-number
            v-model="form.duration_min"
            :min="0"
            :max="60"
            :disabled="form.metric === 'disk'"
            style="flex: 1"
          />
          <span class="unit">
            {{ form.metric === 'disk' ? $t('alertRules.unitDisk') : $t('alertRules.unitNormal') }}
          </span>
        </div>
      </el-form-item>
      <el-form-item :label="$t('alertRules.fScope')">
        <el-radio-group v-model="scope">
          <el-radio value="global">{{ $t('alertRules.scopeGlobal') }}</el-radio>
          <el-radio value="single">{{ $t('alertRules.scopeSingle') }}</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="scope === 'single'" :label="$t('alertRules.fServer')">
        <el-select v-model="form.server_id" style="width: 100%" :placeholder="$t('alertRules.phServer')">
          <el-option v-for="s in servers" :key="s.id" :label="s.hostname || s.name" :value="s.id" />
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="formVisible = false">{{ $t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="saving" @click="save">{{ $t('common.save') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 告警规则面板（「告警」页的规则 tab）。从独立页面迁来，逻辑不变。
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/messages.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

// 与后端 api.MetricLabels 对齐（label 存 i18n key，模板 $t 渲染；unit 由 thresholdUnit 计算属性的
// metricMeta 决定，en 下 unitCount 置空串走国际惯例）
const metricOptions = [
  { value: 'cpu', label: 'alertRules.mCpu', unit: '%', step: 5 },
  { value: 'mem', label: 'alertRules.mMem', unit: '%', step: 5 },
  { value: 'swap', label: 'alertRules.mSwap', unit: '%', step: 5 },
  { value: 'disk', label: 'alertRules.mDisk', unit: '%', step: 5 },
  { value: 'load1', label: 'alertRules.mLoad1', unit: '', step: 1 },
  { value: 'load5', label: 'alertRules.mLoad5', unit: '', step: 1 },
  { value: 'load15', label: 'alertRules.mLoad15', unit: '', step: 1 },
  { value: 'tcp_est', label: 'alertRules.mTcp', unit: '', step: 100 },
  { value: 'net_rx', label: 'alertRules.mNetRx', unit: 'B/s', step: 1024 },
  { value: 'net_tx', label: 'alertRules.mNetTx', unit: 'B/s', step: 1024 },
  { value: 'port', label: 'alertRules.mPort', unit: '', step: 1 },
]

// 翻译后的指标元信息（conditionText 拼条件串用，computed 随语言重算）
const metricI18n = computed(() =>
  metricOptions.map((m) => ({ ...m, text: t(m.label), unit: m.value === 'tcp_est' ? t('alertRules.unitCount') : m.unit })),
)

const rules = ref([])
const servers = ref([])
const loading = ref(false)
const saving = ref(false)
const formVisible = ref(false)
const editing = ref(null)
const scope = ref('global')

const form = reactive({
  name: '',
  metric: 'cpu',
  op: '>',
  threshold: 80,
  duration_min: 2,
  server_id: null,
  param: '',
  enabled: true,
})

const ruleGuard = useDirtyGuard(() => ({ ...form, scope: scope.value }))

const metricMeta = computed(() => metricI18n.value.find((m) => m.value === form.metric))
const thresholdUnit = computed(() => metricMeta.value?.unit || '')
const thresholdStep = computed(() => metricMeta.value?.step || 1)
const paramPlaceholder = computed(() => {
  if (form.metric === 'disk') return t('alertRules.phDisk')
  if (form.metric === 'port') return t('alertRules.phPort')
  return t('alertRules.phNet')
})

// 端口规则的触发条件以语义呈现，落到 op/threshold：未监听 = 监听数 < 1，
// 出现监听 = 监听数 > 0
const portMode = computed({
  get: () => (form.op === '<' ? 'down' : 'up'),
  set: (v) => {
    if (v === 'down') {
      form.op = '<'
      form.threshold = 1
    } else {
      form.op = '>'
      form.threshold = 0
    }
  },
})
// 切到端口指标时给一个合理缺省（未监听告警）；切到磁盘时持续时长归零（无历史曲线）
watch(
  () => form.metric,
  (m) => {
    if (m === 'port' && form.op !== '<') portMode.value = 'down'
    if (m === 'disk') form.duration_min = 0
  },
)

async function load() {
  loading.value = true
  try {
    const [ruleRes, srvRes] = await Promise.all([api.get('/alert-rules'), api.get('/servers')])
    rules.value = ruleRes.rules || []
    servers.value = srvRes.servers || []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

function serverName(id) {
  const s = servers.value.find((x) => x.id === id)
  return s ? s.hostname || s.name : `#${id}`
}

function conditionText(row) {
  const m = metricI18n.value.find((x) => x.value === row.metric)
  const label = m ? m.text : row.metric
  const param = row.param ? t('alertRules.paramWrap', { p: row.param }) : ''
  if (row.metric === 'port') {
    return `${label}${param} ${row.op === '<' ? t('alertRules.condPortDown') : t('alertRules.condPortUp')}`
  }
  const op = row.op === '<' ? t('alertRules.condBelow') : t('alertRules.condAbove')
  return `${label}${param} ${op} ${row.threshold}${m?.unit || ''}`
}

function openForm(row) {
  editing.value = row || null
  if (row) {
    Object.assign(form, {
      name: row.name,
      metric: row.metric,
      op: row.op,
      threshold: row.threshold,
      duration_min: row.duration_sec / 60,
      server_id: row.server_id,
      param: row.param || '',
      enabled: row.enabled,
    })
    scope.value = row.server_id ? 'single' : 'global'
  } else {
    Object.assign(form, {
      name: '',
      metric: 'cpu',
      op: '>',
      threshold: 80,
      duration_min: 2,
      server_id: null,
      param: '',
      enabled: true,
    })
    scope.value = 'global'
  }
  formVisible.value = true
  ruleGuard.snapshot()
}

async function save() {
  if (!form.name.trim()) {
    ElMessage.warning(t('alertRules.needName'))
    return
  }
  if (scope.value === 'single' && !form.server_id) {
    ElMessage.warning(t('alertRules.needServer'))
    return
  }
  if (form.metric === 'port' && !form.param.trim()) {
    ElMessage.warning(t('alertRules.needPort'))
    return
  }
  saving.value = true
  const body = {
    name: form.name.trim(),
    metric: form.metric,
    op: form.op,
    threshold: form.threshold,
    duration_min: form.duration_min,
    server_id: scope.value === 'single' ? form.server_id : null,
    param: form.param.trim(),
    enabled: form.enabled,
  }
  try {
    if (editing.value) {
      await api.put(`/alert-rules/${editing.value.id}`, body)
    } else {
      await api.post('/alert-rules', body)
    }
    ElMessage.success(t('common.saved'))
    formVisible.value = false
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(row, v) {
  try {
    await api.put(`/alert-rules/${row.id}`, {
      name: row.name,
      metric: row.metric,
      op: row.op,
      threshold: row.threshold,
      duration_min: row.duration_sec / 60,
      server_id: row.server_id,
      param: row.param || '',
      enabled: v,
    })
    row.enabled = v
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(t('alertRules.delConfirm', { name: row.name }), t('alertRules.delTitle'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.del(`/alert-rules/${row.id}`)
    await load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

onMounted(load)
</script>

<style scoped>
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.cond-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}
.unit {
  color: var(--muted);
  font-size: 12px;
  white-space: nowrap;
}
</style>
