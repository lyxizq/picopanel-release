<template>
  <div v-if="tool === 'net-check'" class="netcheck">
    <div class="nc-form">
      <el-radio-group v-model="proto">
        <el-radio-button value="tcp">TCP</el-radio-button>
        <el-radio-button value="udp">UDP</el-radio-button>
      </el-radio-group>
      <el-input v-model="host" placeholder="主机 / 域名 / IP，如 example.com 或 192.168.1.1" class="nc-host" @keyup.enter="check" />
      <el-input v-model="port" placeholder="端口" class="nc-port" @keyup.enter="check" />
      <el-button type="primary" :loading="busy" @click="check">检测</el-button>
    </div>
    <div class="nc-hint">
      由监控服务端发起探测（浏览器无原生 TCP/UDP 能力）：TCP 实测握手连通与耗时；
      UDP 无连接协议只能做发送级检测——数据报成功发出即报成功，收到对端响应会另行标注。
    </div>
    <div v-if="result" :class="['nc-result', result.ok ? 'ok' : 'bad']">
      <div class="nc-line">
        <b>{{ result.proto.toUpperCase() }} {{ result.host }}:{{ result.port }}</b>
        <span :class="['nc-state', result.ok ? 'ok' : 'bad']">{{ result.ok ? '✓' : '✗' }} {{ result.ok ? '成功' : '失败' }}</span>
      </div>
      <div class="nc-detail">{{ result.detail }}</div>
      <div v-if="result.remote" class="nc-detail">对端地址：{{ result.remote }}</div>
      <div v-if="result.ms !== null" class="nc-detail">耗时：{{ result.ms }} ms</div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '../../api/index.js'

defineProps({ tool: { type: String, required: true } })

const proto = ref('tcp')
const host = ref('')
const port = ref('')
const busy = ref(false)
const result = ref(null)

async function check() {
  const h = host.value.trim()
  const p = parseInt(port.value, 10)
  if (!h || !p || p < 1 || p > 65535) {
    ElMessage.warning('请填写主机与 1~65535 之间的端口')
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
