<template>
  <div v-if="tool === 'net-check'" class="netcheck">
    <div class="nc-form">
      <el-radio-group v-model="proto">
        <el-radio-button value="tcp">TCP</el-radio-button>
        <el-radio-button value="udp">UDP</el-radio-button>
      </el-radio-group>
      <el-input v-model="host" :placeholder="$t('toolbox.net.phHost')" class="nc-host" @keyup.enter="check" />
      <el-input v-model="port" :placeholder="$t('toolbox.net.phPort')" class="nc-port" @keyup.enter="check" />
      <el-button type="primary" :loading="busy" @click="check">{{ $t('toolbox.net.check') }}</el-button>
    </div>
    <div class="nc-hint">{{ $t('toolbox.net.hint') }}</div>
    <div v-if="result" :class="['nc-result', result.ok ? 'ok' : 'bad']">
      <div class="nc-line">
        <b>{{ result.proto.toUpperCase() }} {{ result.host }}:{{ result.port }}</b>
        <span :class="['nc-state', result.ok ? 'ok' : 'bad']">{{ result.ok ? '✓' : '✗' }} {{ result.ok ? $t('toolbox.net.ok') : $t('toolbox.net.fail') }}</span>
      </div>
      <div class="nc-detail">{{ result.detail }}</div>
      <div v-if="result.remote" class="nc-detail">{{ $t('toolbox.net.peer', { addr: result.remote }) }}</div>
      <div v-if="result.ms !== null" class="nc-detail">{{ $t('toolbox.net.cost', { n: result.ms }) }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { api } from '../../api/index.js'
import { registerPageMessages } from '../../i18n'
import pageMsgs from '../../i18n/pages/toolbox.js'

defineProps({ tool: { type: String, required: true } })

const { t } = useI18n()
registerPageMessages(pageMsgs)

const proto = ref('tcp')
const host = ref('')
const port = ref('')
const busy = ref(false)
const result = ref(null)

async function check() {
  const h = host.value.trim()
  const p = parseInt(port.value, 10)
  if (!h || !p || p < 1 || p > 65535) {
    ElMessage.warning(t('toolbox.net.warnPort'))
    return
  }
  busy.value = true
  result.value = null
  try {
    const r = await api.post('/net-check', { proto: proto.value, host: h, port: p })
    result.value = { ...r, host: h, port: p, ms: r.ms ?? null }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.netcheck {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.nc-form {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.nc-host {
  flex: 1;
  min-width: 220px;
}
.nc-port {
  width: 110px;
}
.nc-hint {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
}
.nc-result {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px 14px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  background: var(--surface);
}
.nc-line {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 14px;
}
.nc-state {
  font-weight: 600;
}
.nc-state.ok {
  color: var(--ok, #1f9d61);
}
.nc-state.bad {
  color: var(--crit, #d92d20);
}
.nc-detail {
  font-size: 13px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
</style>
