<template>
  <el-card shadow="never">
    <template #header>
      <div class="head">
        <span>告警规则</span>
        <el-button type="primary" :icon="Plus" @click="openForm()">新建规则</el-button>
      </div>
    </template>

    <el-table :data="rules" v-loading="loading" empty-text="还没有告警规则" class="row-table">
      <el-table-column prop="name" label="名称" min-width="140" show-overflow-tooltip />
      <el-table-column label="条件" min-width="200">
        <template #default="{ row }">
          {{ conditionText(row) }}
        </template>
      </el-table-column>
      <el-table-column label="持续时长" width="100">
        <template #default="{ row }">
          {{ row.duration_sec ? `${row.duration_sec / 60} 分钟` : '满足即触发' }}
        </template>
      </el-table-column>
      <el-table-column label="作用范围" width="150" show-overflow-tooltip>
        <template #default="{ row }">
          {{ row.server_id ? serverName(row.server_id) : '全部服务器' }}
        </template>
      </el-table-column>
      <el-table-column label="启用" width="80">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="(v) => toggleEnabled(row, v)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="130" fixed="right">
        <template #default="{ row }">
          <el-button size="small" text type="primary" @click="openForm(row)">编辑</el-button>
          <el-button size="small" text type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>

  <el-dialog append-to-body v-model="formVisible" :title="editing ? '编辑规则' : '新建规则'" width="520px" :before-close="ruleGuard.beforeClose">
    <el-form :model="form" label-width="90px">
      <el-form-item label="名称">
        <el-input v-model="form.name" placeholder="如：CPU 过高" />
      </el-form-item>
      <el-form-item label="指标">
        <el-select v-model="form.metric" style="width: 100%">
          <el-option v-for="m in metricOptions" :key="m.value" :label="m.label" :value="m.value" />
        </el-select>
      </el-form-item>
      <el-form-item
        v-if="form.metric === 'disk' || form.metric === 'net_rx' || form.metric === 'net_tx' || form.metric === 'port'"
        label="对象"
      >
        <el-input v-model="form.param" :placeholder="paramPlaceholder" />
      </el-form-item>
      <el-form-item label="触发条件">
        <div v-if="form.metric === 'port'" class="cond-row">
          <el-select v-model="portMode" style="flex: 1">
            <el-option label="未监听该端口时告警" value="down" />
            <el-option label="出现该端口监听时告警" value="up" />
          </el-select>
        </div>
        <div v-else class="cond-row">
          <el-select v-model="form.op" style="width: 100px">
            <el-option label="超过" value=">" />
            <el-option label="低于" value="<" />
          </el-select>
          <el-input-number v-model="form.threshold" :min="0" :step="thresholdStep" style="flex: 1" />
          <span class="unit">{{ thresholdUnit }}</span>
        </div>
      </el-form-item>
      <el-form-item label="持续时长">
        <div class="cond-row">
          <el-input-number
            v-model="form.duration_min"
            :min="0"
            :max="60"
            :disabled="form.metric === 'disk'"
            style="flex: 1"
          />
          <span class="unit">
            {{ form.metric === 'disk' ? '分钟（磁盘无历史曲线，仅支持满足即触发）' : '分钟（0 = 满足即触发）' }}
          </span>
        </div>
      </el-form-item>
      <el-form-item label="作用范围">
        <el-radio-group v-model="scope">
          <el-radio value="global">全部服务器</el-radio>
          <el-radio value="single">指定服务器</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="scope === 'single'" label="服务器">
        <el-select v-model="form.server_id" style="width: 100%" placeholder="选择服务器">
          <el-option v-for="s in servers" :key="s.id" :label="s.hostname || s.name" :value="s.id" />
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="formVisible = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 告警规则面板（「告警」页的规则 tab）。从独立页面迁来，逻辑不变。
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import { api } from '../api/index.js'

// 与后端 api.MetricLabels 对齐
const metricOptions = [
  { value: 'cpu', label: 'CPU 使用率', unit: '%', step: 5 },
  { value: 'mem', label: '内存使用率', unit: '%', step: 5 },
  { value: 'swap', label: 'Swap 使用率', unit: '%', step: 5 },
  { value: 'disk', label: '磁盘使用率', unit: '%', step: 5 },
  { value: 'load1', label: '1 分钟负载', unit: '', step: 1 },
  { value: 'load5', label: '5 分钟负载', unit: '', step: 1 },
  { value: 'load15', label: '15 分钟负载', unit: '', step: 1 },
  { value: 'tcp_est', label: 'TCP 连接数', unit: '个', step: 100 },
  { value: 'net_rx', label: '网卡下行速率', unit: 'B/s', step: 1024 },
  { value: 'net_tx', label: '网卡上行速率', unit: 'B/s', step: 1024 },
  { value: 'port', label: '端口监听', unit: '', step: 1 },
]

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

const metricMeta = computed(() => metricOptions.find((m) => m.value === form.metric))
const thresholdUnit = computed(() => metricMeta.value?.unit || '')
const thresholdStep = computed(() => metricMeta.value?.step || 1)
const paramPlaceholder = computed(() => {
  if (form.metric === 'disk') return '挂载点，如 /data；留空 = 所有磁盘取最大'
  if (form.metric === 'port') return '端口，如 tcp/443、udp/53；纯数字默认 tcp'
  return '网卡名，如 eth0；留空 = 所有网卡取最大'
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
  const m = metricOptions.find((x) => x.value === row.metric)
  const label = m ? m.label : row.metric
  const param = row.param ? `（${row.param}）` : ''
  if (row.metric === 'port') {
    return `${label}${param} ${row.op === '<' ? '未监听时告警' : '出现监听时告警'}`
  }
  const op = row.op === '<' ? '低于' : '超过'
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
    ElMessage.warning('请填写规则名称')
    return
  }
  if (scope.value === 'single' && !form.server_id) {
    ElMessage.warning('请选择服务器')
    return
  }
  if (form.metric === 'port' && !form.param.trim()) {
    ElMessage.warning('请填写端口，如 tcp/443 或 udp/53')
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
    ElMessage.success('已保存')
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
    await ElMessageBox.confirm(`删除规则「${row.name}」？历史告警记录保留。`, '删除规则', { type: 'warning' })
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
