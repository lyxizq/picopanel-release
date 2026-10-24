<template>
  <div class="bili">
    <div class="toolbar">
      <span class="updated">每天凌晨 4 点自动追更；音频转 m4a 保存在所选机器上，检测到 ffmpeg 自动无损封装；可在设置里配 SESSDATA 提升音质</span>
      <div class="tools">
        <el-button @click="openSettings">
          <el-icon><Setting /></el-icon>
          <span>设置</span>
        </el-button>
        <el-button :loading="loading" @click="load">
          <el-icon v-if="!loading"><Refresh /></el-icon>
          <span v-if="!loading">刷新</span>
        </el-button>
        <el-button type="primary" @click="openAdd">
          <el-icon><Plus /></el-icon>
          <span>添加追更</span>
        </el-button>
      </div>
    </div>

    <div v-if="follows.length" class="b-grid">
      <el-card v-for="r in follows" :key="r.id" shadow="never" class="b-card" @click="openDetail(r)">
        <div class="b-head">
          <span class="b-name" :title="`${r.up_name} · ${r.list_title || '待解析'}`">
            {{ r.up_name }}<span class="b-sep">·</span>{{ r.list_title || '待解析' }}
          </span>
          <span class="b-switch" @click.stop><el-switch v-model="r.enabled" size="small" @change="(v) => toggle(r, v)" /></span>
        </div>
        <div class="b-chips">
          <el-tag size="small" effect="plain">B站</el-tag>
          <el-tag v-if="r.task_state" size="small" type="warning" effect="plain">
            <el-icon class="is-loading" style="vertical-align:-2px"><Loading /></el-icon>
            {{ r.kind === '' ? '解析中' : '追更中' }}
          </el-tag>
          <el-tag v-else-if="!r.mid" size="small" type="danger" effect="plain">待解析</el-tag>
          <el-tag v-if="r.stale_agent" size="small" type="danger" effect="plain">机器 agent 版本过旧，升级到 v1.0.28+ 后自动继续</el-tag>
          <el-tag size="small" :type="r.auto_enabled ? 'success' : 'info'" effect="plain">{{ r.auto_enabled ? '每日 4 点自动' : '仅手动' }}</el-tag>
          <el-tag v-if="r.seeded" size="small" effect="plain">已首抓</el-tag>
          <el-tag v-if="r.server_name" size="small" type="info" effect="plain">{{ r.server_name }}</el-tag>
        </div>
        <div class="b-progress">
          <template v-if="r.mid">
            <!-- 上次抓取数=last_new_count（本次新增，跑次落库）；台账总数只在详情弹层看（15） -->
            <span v-if="r.last_ok_at || r.seeded">上次抓取 <b class="b-num">{{ r.last_new_count }}</b> 条</span>
            <span v-if="r.last_ok_at" class="muted">上次追更 {{ fmtTime(r.last_ok_at) }}</span>
          </template>
          <span v-else-if="!r.task_state" class="muted">地址还没解析成功，可编辑重新提交</span>
        </div>
        <div v-if="r.mid" class="b-path" :title="`${r.save_dir}/${r.sub_dir}`">
          <el-icon><FolderOpened /></el-icon><span>{{ r.save_dir }}/{{ r.sub_dir }}</span>
        </div>
        <div v-if="r.last_error" class="b-err" :title="r.last_error">
          <el-icon><WarningFilled /></el-icon><span>{{ r.last_error }}</span>
        </div>
        <div class="b-ops" @click.stop>
          <el-button size="small" type="primary" plain :loading="busy[r.id]" :disabled="!r.mid || !!r.task_state" @click="run(r)">立即追更</el-button>
          <el-button v-if="r.task_state && r.kind" size="small" type="warning" plain :loading="busyStop[r.id]" @click="stop(r)">停止</el-button>
          <el-button size="small" @click="openEdit(r)">编辑</el-button>
          <el-button size="small" type="danger" plain @click="del(r)">删除</el-button>
        </div>
      </el-card>
    </div>
    <el-empty v-else-if="!loading" description="还没有追更配置，点右上角「添加追更」" />

    <!-- 添加/编辑追更（append-to-body：violet 玻璃下 fixed 弹层必须逃出 .main，463 口径） -->
    <el-dialog v-model="dlgOpen" :title="form.id ? '编辑追更' : '添加追更'" width="min(560px, 94vw)" append-to-body :close-on-click-modal="false">
      <el-form label-width="90px">
        <el-form-item label="站点">
          <el-select v-model="form.site" style="width: 100%">
            <el-option label="B站" value="bilibili" />
            <el-option label="爱奇艺（暂未支持）" value="iqiyi" disabled />
            <el-option label="腾讯视频（暂未支持）" value="qq" disabled />
          </el-select>
        </el-form-item>
        <el-form-item label="up 主名字">
          <el-input v-model="form.up_name" placeholder="如：罗翔说刑法（地址填合集名字时用于搜索定位）" maxlength="60" />
        </el-form-item>
        <el-form-item label="追更地址">
          <el-input v-model="form.address" placeholder="合集页 / 视频列表页 / up 主投稿页链接，或直接填合集名字" maxlength="500" />
        </el-form-item>
        <el-form-item label="执行机器">
          <el-select v-model="form.agent_server_id" placeholder="选一台 Agent 上报（push）机器" style="width: 100%">
            <el-option v-for="s in pushServers" :key="s.id" :label="s.name || s.host" :value="s.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="保存目录">
          <el-input v-model="form.save_dir" placeholder="该机器上的绝对路径，如 /volume1/media/bilibili" maxlength="300" />
        </el-form-item>
        <el-form-item label="自动追更">
          <el-switch v-model="form.auto_enabled" />
          <span class="form-hint">每天凌晨 4 点自动检查更新</span>
        </el-form-item>
      </el-form>
      <div class="add-hint">首次抓取最新 10 条，按「up 主名 + 合集名」子目录保存；之后每次自动补新。解析在所选机器上执行，需其在线。</div>
      <template #footer>
        <el-button @click="dlgOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <!-- 追更详情（append-to-body 遵 463 口径）：点卡片弹出统计与最近条目 -->
    <el-dialog v-model="detailOpen" :title="detail ? `${detail.up_name} · ${detail.list_title || '待解析'}` : '追更详情'" width="min(560px, 94vw)" append-to-body>
      <div v-if="detail" class="d-body">
        <div class="d-rows">
          <div class="d-row"><span class="d-k">站点</span><span>B站</span></div>
          <div class="d-row"><span class="d-k">up 主</span><span>{{ detail.up_name || '—' }}</span></div>
          <div class="d-row"><span class="d-k">追更地址</span><span class="d-long" :title="detail.address">{{ detail.address }}</span></div>
          <div class="d-row"><span class="d-k">{{ detail.kind === 'uploads' ? '列表' : detail.kind === 'series' ? '视频列表' : '合集' }}</span><span>{{ detail.kind === 'uploads' ? '全部投稿' : (detail.list_title || '—') }}<span v-if="detail.list_total">（共 {{ detail.list_total }} 条）</span></span></div>
          <div class="d-row"><span class="d-k">执行机器</span><span>{{ detail.server_name || '—' }}</span></div>
          <div class="d-row"><span class="d-k">保存路径</span><span class="d-long" :title="`${detail.save_dir}/${detail.sub_dir}`">{{ detail.save_dir }}/{{ detail.sub_dir }}</span></div>
          <div class="d-row"><span class="d-k">已抓条目</span><span>{{ detail.item_count }}<span v-if="detail.list_total"> / {{ detail.list_total }}</span> 条</span></div>
          <div class="d-row"><span class="d-k">最后更新</span><span>{{ detail.last_new_count > 0 ? `新增 ${detail.last_new_count} 条` : '—' }}</span></div>
          <div class="d-row"><span class="d-k">最后抓取</span><span>{{ detail.last_ok_at ? fmtTime(detail.last_ok_at) : '还没抓过' }}</span></div>
          <div class="d-row"><span class="d-k">自动追更</span><span>{{ detail.auto_enabled ? '每天凌晨 4 点' : '仅手动' }}</span></div>
        </div>
        <div v-if="detail?.stale_agent" class="b-err"><el-icon><WarningFilled /></el-icon><span>执行机器 agent 版本过旧（不含追更执行器），请升级该机器 agent 到 v1.0.28+，升级后任务自动继续</span></div>
        <div v-if="detail.last_error" class="b-err"><el-icon><WarningFilled /></el-icon><span>{{ detail.last_error }}</span></div>
        <div v-if="recordLoading" class="b-checking" style="margin-top: 4px"><el-icon class="is-loading"><Loading /></el-icon>记录加载中…</div>
        <div v-else-if="records.length" class="d-items">
          <div class="d-items-title">已抓记录（最后 {{ records.length }} 条 / 共 {{ recordsTotal }} 条，服务端台账）</div>
          <div v-for="rec in records" :key="rec.id" class="d-item">
            <a :href="`https://www.bilibili.com/video/${rec.bvid}`" target="_blank" rel="noopener noreferrer">{{ rec.file_name || rec.title }}</a>
          </div>
        </div>
        <div v-else-if="detail?.mid" class="muted" style="font-size: 13px">还没有已抓记录</div>
        <div class="d-foot">
          <el-button size="small" type="primary" plain :loading="busy[detail.id]" :disabled="!detail.mid || !!detail.task_state" @click="run(detail)">立即追更</el-button>
          <el-button v-if="detail.task_state && detail.kind" size="small" type="warning" plain :loading="busyStop[detail.id]" @click="stop(detail)">停止</el-button>
          <span v-if="detail.task_state" class="b-checking"><el-icon class="is-loading"><Loading /></el-icon>{{ detail.kind === '' ? '解析中…' : '追更执行中…' }}</span>
        </div>
      </div>
    </el-dialog>

    <!-- 设置：SESSDATA（不回显，空=沿用） -->
    <el-dialog v-model="setOpen" title="追更设置" width="min(480px, 94vw)" append-to-body :close-on-click-modal="false">
      <el-form label-width="90px">
        <el-form-item label="SESSDATA">
          <el-input v-model="sessdata" type="password" show-password :placeholder="sessHas ? '已配置（留空则沿用）' : 'B 站登录 Cookie，可提升音质至 320k'" maxlength="800" />
        </el-form-item>
      </el-form>
      <div class="add-hint">从浏览器 B 站登录 Cookie 里复制 SESSDATA 值（可整段 Cookie 粘贴，自动提取）。未配置时以未登录音质（64k）下载。</div>
      <template #footer>
        <el-button @click="setOpen = false">取消</el-button>
        <el-button type="primary" :loading="setSaving" @click="saveSettings">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Setting } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { fmtTime } from '../utils/format.js'

const follows = ref([])
const loading = ref(false)
const busy = ref({})
const pushServers = ref([])

const detailOpen = ref(false)
const detail = ref(null)
// 已抓记录（16：服务端只回最近 10 条，total 为台账总数）
const records = ref([])
const recordsTotal = ref(0)
const recordLoading = ref(false)

function openDetail(r) {
  detail.value = r
  detailOpen.value = true
  loadRecords(r.id)
}

async function loadRecords(id) {
  recordLoading.value = true
  try {
    const res = await api.get(`/bili-follows/${id}/items`)
    if (detail.value?.id === id) {
      records.value = res.items || []
      recordsTotal.value = res.total || 0
    }
  } catch { /* 记录加载失败不挡详情，保留旧值 */ }
  recordLoading.value = false
}

onMounted(() => {
  load()
  loadServers()
  loadSettings()
})

async function load() {
  loading.value = true
  try {
    const res = await api.get('/bili-follows')
    follows.value = res.follows || []
    ensurePoll() // 有进行中的任务（解析/追更）就保持 5s 轮询
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
  } catch { /* 机器清单加载失败不阻塞页面 */ }
}

// 活跃任务轮询：解析/追更期间 5s 刷新一次列表（任务都终态后自动停）；
// probe 解析另走 pollTask 快速轮询（见 save）
let pollTimer = null
function ensurePoll() {
  if (pollTimer) return
  pollTimer = setInterval(async () => {
    if (!follows.value.some((r) => r.task_state)) {
      clearInterval(pollTimer)
      pollTimer = null
      return
    }
    try {
      const res = await api.get('/bili-follows')
      follows.value = res.follows || []
      if (detail.value) {
        const prev = detail.value
        detail.value = follows.value.find((r) => r.id === prev.id) || prev
        // 详情开着且该行任务刚收尾：刷新已抓记录台账
        if (prev.task_state && !detail.value.task_state) loadRecords(prev.id)
      }
    } catch { /* 轮询失败下次再来 */ }
  }, 5000)
}
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})

// probe 解析轮询（573 口径）：2s 一次、40s 上限，done 即拿到回填后的行；
// 超时多为执行机器 agent 版本过旧（收到任务也不认识）或离线——行留在
// 「解析中」，机器升级后任务自动继续，调度器兜底补消费
async function pollTask(taskID) {
  const deadline = Date.now() + 40000
  while (Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 2000))
    const res = await api.get(`/bili-follows-task/${taskID}`)
    if (res.running) continue
    return res
  }
  ElMessage.warning('机器迟迟未领取任务：请确认该机器 agent 已升级到 v1.0.28+ 且在线')
  return null
}

// 添加/编辑弹层
const dlgOpen = ref(false)
const saving = ref(false)
const form = ref({})
function blankForm() {
  return { id: 0, site: 'bilibili', up_name: '', address: '', agent_server_id: null, save_dir: '', auto_enabled: true }
}
function openAdd() {
  form.value = blankForm()
  dlgOpen.value = true
}
function openEdit(r) {
  form.value = {
    id: r.id, site: r.site || 'bilibili', up_name: r.up_name, address: r.address,
    agent_server_id: r.agent_server_id, save_dir: r.save_dir, auto_enabled: r.auto_enabled,
  }
  dlgOpen.value = true
}

async function save() {
  const f = form.value
  if (!f.address.trim()) {
    ElMessage.warning('请填追更地址（链接或合集名字）')
    return
  }
  if (!f.up_name.trim() && !/space\.bilibili\.com/i.test(f.address)) {
    ElMessage.warning('地址不是链接时必须填 up 主名字')
    return
  }
  if (!f.agent_server_id) {
    ElMessage.warning('请选执行机器')
    return
  }
  if (!f.save_dir.trim()) {
    ElMessage.warning('请填保存目录（执行机器上的绝对路径）')
    return
  }
  saving.value = true
  try {
    const body = {
      up_name: f.up_name.trim(), address: f.address.trim(),
      agent_server_id: Number(f.agent_server_id), save_dir: f.save_dir.trim(), auto_enabled: !!f.auto_enabled,
    }
    let res
    if (f.id) {
      res = await api.put(`/bili-follows/${f.id}`, body)
    } else {
      res = await api.post('/bili-follows', body)
    }
    dlgOpen.value = false
    if (res.follow) upsert(res.follow)
    ElMessage.success(f.id ? '已保存' : '已添加，正在解析追更地址…')
    if (res.task_id) {
      const done = await pollTask(res.task_id)
      if (done?.follow) upsert(done.follow)
      if (done?.error) ElMessage.warning(`解析失败：${done.error}`)
      else if (done?.follow) ElMessage.success(`解析成功：${done.follow.list_title}（共 ${done.follow.list_total} 条）`)
      load()
    }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

function upsert(row) {
  const idx = follows.value.findIndex((x) => x.id === row.id)
  if (idx >= 0) follows.value[idx] = row
  else follows.value.unshift(row)
  if (detail.value?.id === row.id) detail.value = row
}

// 手动追更：入队即回，靠列表轮询看结果
async function run(r) {
  busy.value[r.id] = true
  try {
    await api.post(`/bili-follows/${r.id}/run`, {})
    ElMessage.success(`已发起追更「${r.list_title || r.up_name}」，机器领取后自动执行`)
    r.task_state = 'pending'
    ensurePoll()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    busy.value[r.id] = false
  }
}

const busyStop = ref({})

// 手动停止：pending 任务即时作废；下载中经 agent 捎带停止命令（当前条目
// 完成即中止，已完成部分正常记账）
async function stop(r) {
  busyStop.value[r.id] = true
  try {
    await api.post(`/bili-follows/${r.id}/stop`, {})
    ElMessage.success('已请求停止，机器完成当前条目后中止')
    r.task_state = r.task_state // 保持展示，由 5s 轮询刷新终态
    ensurePoll()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    busyStop.value[r.id] = false
  }
}

async function toggle(r, v) {
  try {
    await api.put(`/bili-follows/${r.id}`, { enabled: v })
  } catch (e) {
    r.enabled = !v
    ElMessage.error(e.message)
  }
}

async function del(r) {
  try {
    await ElMessageBox.confirm(`删除「${r.up_name} · ${r.list_title || '待解析'}」追更？已下载的文件保留在机器上。`, '删除', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api.del(`/bili-follows/${r.id}`)
    follows.value = follows.value.filter((x) => x.id !== r.id)
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 设置：SESSDATA（服务端不回显，空提交=沿用）
const setOpen = ref(false)
const setSaving = ref(false)
const sessdata = ref('')
const sessHas = ref(false)
async function loadSettings() {
  try {
    const res = await api.get('/bili-follows-settings')
    sessHas.value = !!res.sessdata_has
  } catch { /* 设置读取失败不阻塞页面 */ }
}
function openSettings() {
  sessdata.value = ''
  setOpen.value = true
}
async function saveSettings() {
  setSaving.value = true
  try {
    const v = sessdata.value.trim()
    const m = v.match(/SESSDATA=([^;]+)/) // 整段 Cookie 粘贴时提取值
    const body = { sessdata: m ? m[1] : v }
    await api.put('/bili-follows-settings', body)
    sessHas.value = sessHas.value || body.sessdata !== ''
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
.bili {
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
.b-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(380px, 1fr));
  gap: 12px;
}
.b-card {
  cursor: pointer;
}
.b-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.b-name {
  font-weight: 600;
  font-size: 16px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.b-sep {
  margin: 0 4px;
  color: var(--el-text-color-secondary);
}
.b-chips {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 6px;
}
.b-progress {
  display: flex;
  align-items: baseline;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 14.5px;
  margin-top: 8px;
}
.b-num {
  color: var(--el-color-primary);
  font-variant-numeric: tabular-nums;
}
.b-new {
  color: var(--el-color-success);
  font-size: 13.5px;
}
.muted {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.b-path {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12.5px;
  color: var(--el-text-color-secondary);
  margin-top: 6px;
  overflow: hidden;
}
.b-path span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.b-err {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--el-color-danger);
  margin-top: 6px;
  overflow: hidden;
}
.b-err span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.b-ops {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 10px;
}
.b-checking {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

/* 详情弹层 */
.d-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.d-rows {
  display: flex;
  flex-direction: column;
  gap: 7px;
  font-size: 14px;
}
.d-row {
  display: flex;
  gap: 12px;
}
.d-k {
  width: 72px;
  min-width: 72px;
  color: var(--el-text-color-secondary);
}
.d-long {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  word-break: break-all;
}
.d-items {
  border-top: 1px solid rgba(127, 127, 127, 0.15);
  padding-top: 8px;
}
.d-items-title {
  font-size: 13px;
  color: var(--el-text-color-secondary);
  margin-bottom: 6px;
}
.d-item {
  font-size: 13.5px;
  padding: 3px 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.d-item a {
  color: var(--el-text-color-regular);
  text-decoration: none;
}
.d-item a:hover {
  color: var(--el-color-primary);
}
.d-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  border-top: 1px solid rgba(127, 127, 127, 0.15);
  padding-top: 10px;
}

.form-hint {
  margin-left: 10px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.add-hint {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

@media (max-width: 768px) {
  .b-grid {
    grid-template-columns: 1fr;
  }
  .tools {
    width: 100%;
    flex-wrap: wrap;
  }
}
</style>
