<template>
  <el-card shadow="never">
    <template #header>
      <div class="head">
        <span>告警历史</span>
        <div class="filters">
          <el-select v-model="status" size="small" style="width: 120px" @change="reload">
            <el-option label="全部" value="all" />
            <el-option label="告警中" value="firing" />
            <el-option label="已恢复" value="resolved" />
          </el-select>
          <!-- teleported=false：弹层锚定在触发框正下方（玻璃卡容器内全局传送弹层定位错乱，2026-10-03 用户反馈） -->
          <el-select
            v-model="serverId"
            size="small"
            clearable
            placeholder="全部服务器"
            style="width: 180px"
            :teleported="false"
            @change="reload"
          >
            <el-option v-for="s in servers" :key="s.id" :label="s.hostname || s.name" :value="s.id" />
          </el-select>
        </div>
      </div>
    </template>

    <el-table :data="alerts" v-loading="loading" empty-text="暂无告警记录" class="row-table">
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'firing' ? 'danger' : 'success'" size="small" effect="dark">
            {{ row.status === 'firing' ? '告警中' : '已恢复' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="服务器" width="140" show-overflow-tooltip>
        <template #default="{ row }">
          <el-link type="primary" @click="openServerDetail(row.server_id)">
            {{ row.server_name }}
          </el-link>
        </template>
      </el-table-column>
      <el-table-column prop="detail" label="详情" min-width="260" show-overflow-tooltip />
      <el-table-column label="规则" width="130" show-overflow-tooltip>
        <template #default="{ row }">
          {{ row.rule_id === 0 ? '离线检测（内置）' : row.rule_name || `#${row.rule_id}` }}
        </template>
      </el-table-column>
      <el-table-column label="触发时间" width="165">
        <template #default="{ row }">{{ fmtTime(row.triggered_at) }}</template>
      </el-table-column>
      <el-table-column label="恢复时间" width="165">
        <template #default="{ row }">{{ fmtTime(row.resolved_at) }}</template>
      </el-table-column>
      <el-table-column label="持续" width="100">
        <template #default="{ row }">
          {{ row.resolved_at ? fmtUptime(row.resolved_at - row.triggered_at) : '-' }}
        </template>
      </el-table-column>
    </el-table>

    <div class="pager">
      <el-pagination
        v-model:current-page="page"
        :page-size="pageSize"
        :total="total"
        layout="total, prev, pager, next"
        @current-change="reload"
      />
    </div>
  </el-card>
</template>

<script setup>
// 告警历史面板（「告警」页默认 tab）。从独立页面迁来，逻辑不变。
import { onActivated, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api } from '../api/index.js'
import { openServerDetail } from '../serverDetail.js'
import { fmtTime, fmtUptime } from '../utils/format.js'

const route = useRoute()
const alerts = ref([])
const servers = ref([])
const loading = ref(false)
const status = ref(route.query.status === 'firing' ? 'firing' : 'all')
const serverId = ref(null)
const page = ref(1)
const pageSize = 20
const total = ref(0)

async function reload() {
  loading.value = true
  try {
    const params = new URLSearchParams({ status: status.value, page: page.value, page_size: pageSize })
    if (serverId.value) params.set('server_id', serverId.value)
    const res = await api.get(`/alerts?${params}`)
    alerts.value = res.alerts || []
    total.value = res.total || 0
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  reload()
  try {
    const res = await api.get('/servers')
    servers.value = res.servers || []
  } catch {
    /* ignore */
  }
})
// 宿主「告警」页被 keep-alive 缓存，切回该 tab 时刷新列表
onActivated(reload)
</script>

<style scoped>
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}
.filters {
  display: flex;
  gap: 8px;
}
.pager {
  margin-top: 12px;
  display: flex;
  justify-content: flex-end;
}
</style>
