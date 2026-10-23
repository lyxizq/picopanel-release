<template>
  <div class="vdl">
    <div class="toolbar">
      <span class="updated">基于 yt-dlp，支持 B 站/抖音等热门平台，统一转 mp4；执行机器需已安装 yt-dlp（合并音视频需 ffmpeg）</span>
      <div class="tools">
        <el-button @click="openSettings">
          <el-icon><Setting /></el-icon>
          <span>Cookie</span>
        </el-button>
        <el-button :loading="loading" @click="load">
          <el-icon v-if="!loading"><Refresh /></el-icon>
          <span v-if="!loading">刷新</span>
        </el-button>
      </div>
    </div>

    <!-- 新建下载：贴地址即下 -->
    <el-card shadow="never" class="new-card">
      <div class="new-row">
        <el-input v-model="form.url" placeholder="视频页面链接，如 https://www.bilibili.com/video/BV… / 抖音分享链接" maxlength="1000" @keyup.enter="create" class="u-input">
          <template #prepend><el-icon><VideoPlay /></el-icon></template>
        </el-input>
        <el-button type="primary" :loading="creating" @click="create">开始下载</el-button>
      </div>
      <div class="new-row sub">
        <el-input v-model="form.save_dir" placeholder="保存目录（执行机器上的绝对路径，如 /volume1/media/videos）" maxlength="300" class="d-input" />
        <div class="loc">
          <span class="loc-label">下载到</span>
          <el-radio-group v-model="form.executor" size="small">
            <el-radio-button value="server">本服务器</el-radio-button>
            <el-radio-button value="agent">agent 机器</el-radio-button>
          </el-radio-group>
          <el-select v-if="form.executor === 'agent'" v-model="form.server_id" placeholder="选一台 Agent 上报（push）机器" class="srv-select" size="small">
            <el-option v-for="s in pushServers" :key="s.id" :label="s.name || s.host" :value="s.id" />
          </el-select>
        </div>
      </div>
      <div v-if="(form.executor === 'server' && !serverYtdlp)" class="new-hint warn">
        <el-icon><WarningFilled /></el-icon>
        <span>本服务器未检测到 yt-dlp，任务会失败——请先安装（pip install -U yt-dlp 或系统包管理器）</span>
      </div>
      <div v-else class="new-hint">B 站高清（1080P+）等需登录的场景可在右上角配置 Cookie（Netscape 格式）；下载到 agent 机器时由该机执行，需其在线。</div>
    </el-card>

    <!-- 下载历史 -->
    <el-table v-if="tasks.length" :data="tasks" size="small" class="t-table">
      <el-table-column label="视频" min-width="260">
        <template #default="{ row }">
          <div class="t-title" :title="row.title || row.url">
            <span class="t-name">{{ row.title || row.url }}</span>
            <a v-if="row.title" class="t-link" :href="row.url" target="_blank" rel="noopener noreferrer">原链接</a>
          </div>
          <div v-if="row.error" class="t-err" :title="row.error">
            <el-icon><WarningFilled /></el-icon><span>{{ row.error }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="保存目录" min-width="180">
        <template #default="{ row }">
          <span class="t-dir" :title="row.file_name || row.save_dir">{{ row.file_name || row.save_dir }}</span>
        </template>
      </el-table-column>
      <el-table-column label="位置" width="110">
        <template #default="{ row }">
          <span v-if="row.executor === 'server'">本服务器</span>
          <span v-else>{{ row.server_name || `机器#${row.server_id}` }}</span>
          <el-tag v-if="row.stale_agent" size="small" type="danger" effect="plain" style="margin-left:4px">版本旧</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="170">
        <template #default="{ row }">
          <template v-if="row.state === 'leased'">
            <div class="t-prog">
              <el-progress :percentage="row.progress || 2" :stroke-width="8" :show-text="false" class="p-bar" />
              <span class="p-text">{{ row.progress || 0 }}%<template v-if="row.speed"> · {{ row.speed }}</template></span>
            </div>
          </template>
          <el-tag v-else size="small" :type="stateMeta(row.state).type" effect="plain">{{ stateMeta(row.state).text }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="大小" width="90">
        <template #default="{ row }">{{ row.file_size ? fmtSize(row.file_size) : '—' }}</template>
      </el-table-column>
      <el-table-column label="时间" width="150">
        <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="130" align="right">
        <template #default="{ row }">
          <el-button v-if="row.state === 'pending' || row.state === 'leased'" size="small" type="warning" plain :loading="busy[row.id]" @click="stop(row)">停止</el-button>
          <el-button v-else size="small" type="danger" plain @click="del(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-else-if="!loading" description="还没有下载任务，贴个视频链接试试" />

    <!-- Cookie 设置（不回显，空=沿用）：B 站高清/一些平台需要登录态 -->
    <el-dialog v-model="setOpen" title="下载 Cookie" width="min(560px, 94vw)" append-to-body :close-on-click-modal="false">
      <el-form label-width="90px">
        <el-form-item label="Cookies">
          <el-input v-model="cookies" type="textarea" :rows="6" :placeholder="cookiesHas ? '已配置（留空则沿用）' : 'Netscape 格式 Cookie 文件内容'" />
        </el-form-item>
      </el-form>
      <div class="add-hint">把浏览器导出的 Netscape 格式 cookies.txt 内容整段粘贴到这里，B 站 1080P+/部分平台内容需要登录态；未配置时以游客身份下载。该配置会加密保存并随任务下发到执行机器。</div>
      <template #footer>
        <el-button @click="setOpen = false">取消</el-button>
        <el-button type="primary" :loading="setSaving" @click="saveSettings">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Setting } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { fmtTime } from '../utils/format.js'

const tasks = ref([])
const loading = ref(false)
const creating = ref(false)
const busy = ref({})
const pushServers = ref([])
const serverYtdlp = ref(true)

// 表单缺省记住上次用的目录与位置（localStorage，换浏览器不带走）
const form = reactive({
  url: '',
  save_dir: localStorage.getItem('vd_save_dir') || '',
  executor: localStorage.getItem('vd_executor') === 'agent' ? 'agent' : 'server',
  server_id: Number(localStorage.getItem('vd_server_id')) || null,
})

onMounted(() => {
  load()
  loadServers()
})

async function load() {
  loading.value = true
  try {
    const res = await api.get('/video-dls')
    tasks.value = res.tasks || []
    serverYtdlp.value = res.server_ytdlp !== false
    ensurePoll() // 有进行中的任务就保持 3s 轮询（下载进度顺滑刷新）
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

async function loadServers() {
  try {
    const res = await api.get('/servers')
    pushServers.value = (res.servers || []).filter((x) => x && x.collect_mode === 'push')
    // 上次选的机器被删了就回退到第一台
    if (form.server_id && !pushServers.value.some((s) => s.id === form.server_id)) {
      form.server_id = pushServers.value[0]?.id || null
    }
  } catch { /* 机器清单加载失败不阻塞页面 */ }
}

// 活跃任务轮询：3s 一次（进度/速度在 leased 期间持续回写），全终态自动停
let pollTimer = null
function ensurePoll() {
  if (pollTimer) return
  pollTimer = setInterval(async () => {
    if (!tasks.value.some((r) => r.state === 'pending' || r.state === 'leased')) {
      clearInterval(pollTimer)
      pollTimer = null
      return
    }
    try {
      const res = await api.get('/video-dls')
      tasks.value = res.tasks || []
    } catch { /* 轮询失败下次再来 */ }
  }, 3000)
}
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})

async function create() {
  if (!form.url.trim()) {
    ElMessage.warning('请填视频链接')
    return
  }
  if (!form.save_dir.trim()) {
    ElMessage.warning('请填保存目录（执行机器上的绝对路径）')
    return
  }
  if (form.executor === 'agent' && !form.server_id) {
    ElMessage.warning('请选执行机器')
    return
  }
  creating.value = true
  try {
    const body = {
      url: form.url.trim(), save_dir: form.save_dir.trim(),
      executor: form.executor, server_id: Number(form.server_id) || 0,
    }
    localStorage.setItem('vd_save_dir', body.save_dir)
    localStorage.setItem('vd_executor', body.executor)
    if (body.executor === 'agent') localStorage.setItem('vd_server_id', String(body.server_id))
    await api.post('/video-dls', body)
    form.url = ''
    ElMessage.success('已加入下载队列')
    load()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    creating.value = false
  }
}

async function stop(r) {
  busy.value[r.id] = true
  try {
    await api.post(`/video-dls/${r.id}/stop`, {})
    ElMessage.success(r.state === 'pending' ? '已取消（还没被领取）' : '已请求停止，执行机器正在中止下载')
    load()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    busy.value[r.id] = false
  }
}

async function del(r) {
  try {
    await ElMessageBox.confirm(`删除这条下载记录？已下载的文件保留在执行机器上。`, '删除', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api.del(`/video-dls/${r.id}`)
    tasks.value = tasks.value.filter((x) => x.id !== r.id)
  } catch (e) {
    ElMessage.error(e.message)
  }
}

function stateMeta(s) {
  switch (s) {
    case 'pending': return { text: '排队中', type: 'info' }
    case 'done': return { text: '已完成', type: 'success' }
    case 'failed': return { text: '失败', type: 'danger' }
    default: return { text: '已作废', type: 'info' }
  }
}

function fmtSize(n) {
  if (n < 1024) return `${n}B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = n
  for (const u of units) {
    v /= 1024
    if (v < 1024) return `${v.toFixed(1)}${u}`
  }
  return `${v.toFixed(1)}PiB`
}

// Cookie 设置（服务端不回显，空提交=沿用）
const setOpen = ref(false)
const setSaving = ref(false)
const cookies = ref('')
const cookiesHas = ref(false)
async function openSettings() {
  cookies.value = ''
  setOpen.value = true
  try {
    const res = await api.get('/video-dls-settings')
    cookiesHas.value = !!res.cookies_has
  } catch { /* 设置读取失败不阻塞弹层 */ }
}
async function saveSettings() {
  setSaving.value = true
  try {
    const body = { cookies: cookies.value.trim() }
    await api.put('/video-dls-settings', body)
    cookiesHas.value = cookiesHas.value || body.cookies !== ''
    setOpen.value = false
    ElMessage.success('设置已保存')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    setSaving.value = false
  }
}
</script>

<style scoped>
.vdl {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.updated {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.tools {
  display: flex;
  gap: 8px;
  align-items: center;
}
.new-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
}
.new-row {
  display: flex;
  gap: 10px;
  align-items: center;
}
.new-row.sub {
  flex-wrap: wrap;
}
.u-input {
  flex: 1;
}
.d-input {
  flex: 1;
  min-width: 260px;
}
.loc {
  display: flex;
  align-items: center;
  gap: 8px;
}
.loc-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
.srv-select {
  width: 220px;
}
.new-hint {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.new-hint.warn {
  color: var(--el-color-warning);
}
.t-table {
  width: 100%;
}
.t-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.t-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 500;
}
.t-link {
  flex: none;
  font-size: 12px;
  color: var(--el-color-primary);
  text-decoration: none;
}
.t-dir {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12.5px;
  color: var(--el-text-color-secondary);
}
.t-err {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12.5px;
  color: var(--el-color-danger);
  margin-top: 2px;
  overflow: hidden;
}
.t-err span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.t-prog {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.p-bar {
  width: 130px;
}
.p-text {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  font-variant-numeric: tabular-nums;
}
.add-hint {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

@media (max-width: 768px) {
  .tools {
    width: 100%;
    flex-wrap: wrap;
  }
  .srv-select {
    width: 160px;
  }
}
</style>
