<template>
  <div class="tv">
    <div class="toolbar">
      <span v-if="lastSweep" class="sweep-at">定时抓取 {{ fmtTime(lastSweep) }}</span>
      <div class="tv-filters">
        <el-checkbox v-model="fShort">短剧</el-checkbox>
        <el-checkbox v-model="fEnded">已完结</el-checkbox>
        <el-checkbox v-model="fOnline">已上线</el-checkbox>
      </div>
      <div class="tools">
        <el-input
          v-model="searchQ"
          class="tv-search"
          placeholder="搜索已追的剧名 / 原名"
          clearable
          :prefix-icon="Search"
        />
        <el-button :loading="loading" @click="load">
          <el-icon v-if="!loading"><Refresh /></el-icon>
          <span v-if="!loading">刷新</span>
        </el-button>
        <el-button type="primary" @click="openAdd">
          <el-icon><Plus /></el-icon>
          <span>添加追剧</span>
        </el-button>
      </div>
    </div>

    <div v-if="filteredFollows.length" class="tv-grid">
      <el-card v-for="r in filteredFollows" :key="r.id" shadow="never" class="tv-card" @click="openDetail(r)">
        <div class="f-body">
          <div class="f-poster">
            <img v-if="r.poster" :src="r.poster" :alt="r.name" loading="lazy" @error="r.poster = ''" />
            <div v-else class="f-poster-empty">{{ r.type === 'movie' ? '影' : '剧' }}</div>
          </div>
          <div class="f-info">
            <div class="f-head">
              <span class="f-name" :title="r.original_name && r.original_name !== r.name ? r.original_name : r.name">
                {{ r.name }}<span v-if="r.original_name && r.original_name !== r.name" class="f-orig">{{ r.original_name }}</span>
              </span>
              <span class="f-switch" @click.stop><el-switch v-model="r.enabled" size="small" @change="(v) => toggle(r, v)" /></span>
            </div>
            <div class="f-chips">
              <el-tag size="small" :type="r.type === 'movie' ? 'warning' : 'primary'" effect="plain">{{ r.type === 'movie' ? '电影' : '剧集' }}</el-tag>
              <el-tag v-if="r.short_drama" size="small" type="danger" effect="plain">短剧</el-tag>
              <el-tag v-if="r.type === 'movie'" size="small" :type="r.platforms ? 'success' : 'info'" effect="plain">
                {{ r.platforms ? `已上线：${r.platforms.split('/').join(' / ')}` : '未上线' }}
              </el-tag>
              <el-tag size="small" :type="statusTagType(r)" effect="plain">{{ statusText(r) }}</el-tag>
              <el-tag v-if="r.emby_checked_at" size="small" :type="r.in_emby ? 'success' : 'info'" effect="plain">
                {{ r.in_emby ? `Emby 已入库${r.emby_item_name ? '：' + r.emby_item_name : ''}` : 'Emby 未收录' }}
              </el-tag>
              <span v-if="r.checking" class="f-checking"><el-icon class="is-loading"><Loading /></el-icon>查询中</span>
            </div>
            <div class="f-progress">
              <template v-if="r.type === 'movie'">
                <span v-if="r.last_air_date">上映日期：{{ r.last_air_date }}</span>
                <span v-else class="muted">上映日期未知</span>
              </template>
              <template v-else>
                <span v-if="r.last_episode">
                  更新至 <b class="f-se">S{{ r.last_season }}E{{ r.last_episode }}</b>
                  <span v-if="r.last_air_date" class="muted">（{{ r.last_air_date }} 播出）</span>
                </span>
                <span v-else class="muted">尚未开播或暂无进度</span>
                <span v-if="r.next_episode && !r.done" class="f-next">下集 S{{ r.next_season }}E{{ r.next_episode }}{{ r.next_air_date ? '：' + r.next_air_date : '' }}</span>
                <span v-if="r.seasons || r.episodes" class="muted">全剧 {{ r.seasons }} 季 {{ r.episodes }} 集</span>
              </template>
              <span v-if="r.cast" class="f-cast" :title="r.cast">主演：{{ r.cast.split('/').join(' / ') }}</span>
            </div>
            <div v-if="r.last_error" class="f-err" :title="r.last_error">
              <el-icon><WarningFilled /></el-icon><span>{{ r.last_error }}</span>
            </div>
            <div class="f-ops" @click.stop>
              <el-button size="small" :loading="busy[r.id]" @click="check(r)">立即检查</el-button>
              <el-button size="small" :loading="embyBusy[r.id]" @click="embyCheck(r)">查在库</el-button>
              <el-button size="small" type="danger" plain @click="del(r)">删除</el-button>
              <span v-if="r.last_checked_at" class="f-checked">上次检查 {{ fmtTime(r.last_checked_at) }}</span>
            </div>
          </div>
        </div>
      </el-card>
    </div>
    <el-empty v-else-if="!loading && follows.length" description="没有匹配的追剧条目" />
    <el-empty v-else-if="!loading" description="还没有追的剧，点右上角「添加追剧」" />

    <!-- 添加追剧（append-to-body：violet 玻璃下 fixed 弹层必须逃出 .main，463 口径） -->
    <el-dialog v-model="addOpen" title="添加追剧" width="min(620px, 94vw)" append-to-body :close-on-click-modal="false">
      <div class="add-row">
        <div class="seg" role="tablist">
          <button type="button" :class="{ on: addType === 'tv' }" @click="addType = 'tv'">剧集</button>
          <button type="button" :class="{ on: addType === 'movie' }" @click="addType = 'movie'">电影</button>
        </div>
        <el-input
          v-model="addQ"
          :placeholder="addType === 'movie' ? '输入电影名，回车搜索' : '输入剧名，回车搜索'"
          clearable
          @keyup.enter="search"
        />
        <el-button type="primary" :loading="searching" @click="search">搜索</el-button>
      </div>
      <div class="add-hint">搜索经 TMDB（出口 agent 代查，约几秒）；同名剧按首播年份辨认，点选卡片添加。</div>
      <div v-if="candidates.length" class="cand-list">
        <div v-for="cand in candidates" :key="cand.id" class="cand" @click="add(cand)">
          <div class="cand-poster">
            <img v-if="cand.poster_path" :src="posterURL(cand.poster_path)" :alt="cand.name" loading="lazy" />
            <div v-else class="cand-poster-empty">无图</div>
          </div>
          <div class="cand-info">
            <div class="cand-name">
              {{ cand.name }}<span v-if="cand.original_name && cand.original_name !== cand.name" class="cand-orig">{{ cand.original_name }}</span>
              <el-tag v-if="cand.emby_checked" size="small" :type="cand.in_emby ? 'success' : 'info'" effect="plain" class="cand-emby"
                :title="cand.in_emby ? `Emby 库已收录：${cand.emby_item_name || cand.name}` : 'Emby 库未收录'">
                {{ cand.in_emby ? '已入库' : '未入库' }}
              </el-tag>
            </div>
            <div class="cand-meta">
              <span v-if="cand.year">{{ cand.year }}</span>
              <span v-if="cand.rating">⭐ {{ cand.rating.toFixed(1) }}</span>
            </div>
            <div v-if="cand.cast" class="cand-cast">主演：{{ cand.cast.split('/').join(' / ') }}</div>
            <div v-if="cand.overview" class="cand-overview">{{ cand.overview }}</div>
          </div>
        </div>
      </div>
      <el-empty v-else-if="searched && !searching" description="没有找到，换个说法或检查出口配置再试" :image-size="72" />
    </el-dialog>

    <!-- 剧集详情（554，append-to-body 遵 463 口径）：点卡片弹出全量信息 -->
    <el-dialog v-model="detailOpen" :title="detail ? detail.name : '详情'" width="min(560px, 94vw)" append-to-body>
      <div v-if="detail" class="d-body">
        <div class="d-head">
          <div class="d-poster">
            <img v-if="detail.poster" :src="detail.poster" :alt="detail.name" />
            <div v-else class="f-poster-empty">{{ detail.type === 'movie' ? '影' : '剧' }}</div>
          </div>
          <div class="d-meta">
            <div class="d-chips">
              <el-tag size="small" :type="detail.type === 'movie' ? 'warning' : 'primary'" effect="plain">{{ detail.type === 'movie' ? '电影' : '剧集' }}</el-tag>
              <el-tag v-if="detail.short_drama" size="small" type="danger" effect="plain">短剧</el-tag>
              <el-tag v-if="detail.type === 'movie'" size="small" :type="detail.platforms ? 'success' : 'info'" effect="plain">
                {{ detail.platforms ? `已上线：${detail.platforms.split('/').join(' / ')}` : '未上线' }}
              </el-tag>
              <el-tag size="small" :type="statusTagType(detail)" effect="plain">{{ statusText(detail) }}</el-tag>
              <el-tag v-if="detail.first_air_date" size="small" effect="plain">{{ detail.first_air_date }}</el-tag>
              <el-tag v-if="detail.emby_checked_at" size="small" :type="detail.in_emby ? 'success' : 'info'" effect="plain">
                {{ detail.in_emby ? `Emby 已入库${detail.emby_item_name ? '：' + detail.emby_item_name : ''}` : 'Emby 未收录' }}
              </el-tag>
            </div>
            <div v-if="detail.type === 'movie'" class="d-progress">
              <span>上映日期：{{ detail.last_air_date || '未知' }}</span>
            </div>
            <div v-else class="d-progress">
              <span v-if="detail.last_episode">
                更新至 <b class="f-se">S{{ detail.last_season }}E{{ detail.last_episode }}</b>
                <span v-if="detail.episode_name" class="d-epname">「{{ detail.episode_name }}」</span>
                <span v-if="detail.last_air_date" class="muted">（{{ detail.last_air_date }} 播出）</span>
              </span>
              <span v-if="detail.next_episode && !detail.done">下集 S{{ detail.next_season }}E{{ detail.next_episode }}{{ detail.next_air_date ? '：' + detail.next_air_date : '' }}</span>
              <span v-if="detail.seasons || detail.episodes">全剧 {{ detail.seasons }} 季 {{ detail.episodes }} 集</span>
            </div>
            <div v-if="detail.cast" class="d-cast">主演：{{ detail.cast.split('/').join(' / ') }}</div>
            <div class="d-links">
              <a :href="tmdbURL(detail)" target="_blank" rel="noopener noreferrer">TMDB 页面 ↗</a>
              <a :href="doubanURL(detail)" target="_blank" rel="noopener noreferrer">豆瓣搜索 ↗</a>
            </div>
          </div>
        </div>
        <div v-if="detail.overview" class="d-overview">{{ detail.overview }}</div>
        <div v-else class="muted" style="margin-top: 10px">暂无简介，点「立即检查」后自动补全</div>
        <div v-if="resLinks.length" class="d-res">
          <span class="d-res-label">资源搜索：</span>
          <a v-for="l in resLinks" :key="l.url" :href="l.url" target="_blank" rel="noopener noreferrer">{{ l.name }} ↗</a>
        </div>
        <div class="d-foot">
          <label class="d-sd" @click.stop>
            <el-switch v-model="detail.short_drama" size="small" @change="(v) => toggleShortDrama(detail, v)" />短剧
          </label>
          <span v-if="detail.last_checked_at">上次检查 {{ fmtTime(detail.last_checked_at) }}</span>
          <span v-if="detail.last_error" class="f-err"><el-icon><WarningFilled /></el-icon>{{ detail.last_error }}</span>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onActivated, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { fmtTime } from '../utils/format.js'

const follows = ref([])
const loading = ref(false)
const lastSweep = ref(0) // 最近一轮定时抓取时刻（63，秒级 ts）
const busy = ref({})
const embyBusy = ref({})

// 已订阅搜索（568）：剧名/原名包含匹配，前端过滤；状态过滤（61）：复选框勾选即
// 过滤、多选=条件与，判定与卡片标签同源——短剧=人工标记；已完结=状态标显示
// 「已完结」（Ended/allAired 追平）或 done 打标；已上线=电影且 platforms 非空
//（与 60 的上线状态标同口径，只认显示绿标的条目）
const searchQ = ref('')
const fShort = ref(false)
const fEnded = ref(false)
const fOnline = ref(false)
const filteredFollows = computed(() => {
  let list = follows.value
  const q = searchQ.value.trim().toLowerCase()
  if (q) {
    list = list.filter(
      (r) => (r.name || '').toLowerCase().includes(q) || (r.original_name || '').toLowerCase().includes(q),
    )
  }
  if (fShort.value) list = list.filter((r) => r.short_drama)
  if (fEnded.value) list = list.filter((r) => r.done || statusText(r) === '已完结')
  if (fOnline.value) list = list.filter((r) => r.type === 'movie' && !!r.platforms)
  return list
})

// 添加弹层：类型 + 关键词搜索 → 候选点选
const addOpen = ref(false)
const addType = ref('tv')
const addQ = ref('')
const searching = ref(false)
const searched = ref(false)
const candidates = ref([])

// 剧集详情弹层（554）：点卡片弹出
const detailOpen = ref(false)
const detail = ref(null)
const resLinks = ref([])
async function openDetail(r) {
  detail.value = r
  detailOpen.value = true
  // 资源站搜索链接（565）：按剧名拼好的各站搜索页，新窗打开
  resLinks.value = []
  try {
    const kw = r.original_name || r.name
    const res = await api.get(`/tvfollows-resource-links?keyword=${encodeURIComponent(kw)}`)
    resLinks.value = res.links || []
  } catch { /* 链接获取失败不挡详情 */ }
}
function tmdbURL(r) {
  return `https://www.themoviedb.org/${r.type === 'movie' ? 'movie' : 'tv'}/${r.tmdb_id}?language=zh-CN`
}
function doubanURL(r) {
  return `https://www.douban.com/search?cat=1002&q=${encodeURIComponent(r.original_name && r.original_name !== r.name ? r.original_name : r.name)}`
}

onMounted(load)
onActivated(load)

async function load() {
  loading.value = true
  try {
    const res = await api.get('/tvfollows')
    follows.value = res.follows || []
    lastSweep.value = res.last_sweep || 0 // 最近一轮定时抓取时刻（63）
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

function posterURL(path) {
  return `https://image.tmdb.org/t/p/w185${path}`
}

// TMDB status → 中文文案 / 标签色（终局两态红、在更绿、其余灰）。
// allAired（48）：TMDB 状态滞后（完结未翻 Ended/续订季未建）时，最新已播集
// 追平全剧总集数即视同完结展示——仅展示层，推送/done 仍以 TMDB 状态与
// PT「全集」通道为准；TMDB 补建新一季后数字自然错开，徽标自动回连载中。
function allAired(r) {
  return r.seasons > 0 && r.episodes > 0 &&
    r.last_season === r.seasons && r.last_episode === r.episodes
}
function statusText(r) {
  if (allAired(r) && r.status === 'Returning Series') return '已完结'
  const map = {
    'Returning Series': '连载中', Ended: '已完结', Canceled: '已取消',
    Released: '已上映', 'Post Production': '后期', 'In Production': '制作中',
    Planned: '计划中', Pilot: '试播',
  }
  return map[r.status] || (r.status || '未知')
}
function statusTagType(r) {
  if (r.status === 'Ended' || r.status === 'Canceled') return 'danger'
  if (r.status === 'Returning Series') return allAired(r) ? 'danger' : 'success'
  if (r.status === 'Released') return 'success'
  return 'info'
}

function openAdd() {
  addType.value = 'tv'
  addQ.value = ''
  candidates.value = []
  searched.value = false
  addOpen.value = true
}

// 中转任务轮询（573）：API 入队即回 task_id（同步等回传会撞边缘节点回源
// 超时 524），每 2s 查一次到终态；40s 上限视为出口 agent 离线
async function pollTask(taskID, { timeoutMs = 40000, stepMs = 2000 } = {}) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, stepMs))
    const res = await api.get(`/tvfollows-task/${taskID}`)
    if (res.running) continue
    return res
  }
  throw new Error('出口迟迟未返回，请检查出口 agent 是否在线')
}

async function search() {
  const q = addQ.value.trim()
  if (q.length < 2) {
    ElMessage.warning('剧名至少 2 个字')
    return
  }
  searching.value = true
  searched.value = true
  try {
    const enq = await api.get(`/tvfollows-search?type=${encodeURIComponent(addType.value)}&q=${encodeURIComponent(q)}`)
    let res = enq.task_id ? await pollTask(enq.task_id) : enq
    if (res.error) {
      candidates.value = []
      ElMessage.error(res.error)
      return
    }
    candidates.value = res.candidates || []
    // 主演/Emby 小标在服务端异步增强：基础候选先显示，最多再等 30s 刷新一次
    for (let i = 0; i < 10 && enq.task_id && res.enhanced === false; i++) {
      await new Promise((resolve) => setTimeout(resolve, 3000))
      res = await api.get(`/tvfollows-task/${enq.task_id}`)
      if (res.error) return
      if (res.candidates?.length) candidates.value = res.candidates
    }
    searching.value = false
  } catch (e) {
    candidates.value = []
    ElMessage.error(e.message)
  } finally {
    searching.value = false
  }
}

async function add(cand) {
  try {
    const row = await api.post('/tvfollows', {
      type: cand.type, tmdb_id: cand.id, name: cand.name,
      original_name: cand.original_name, poster_path: cand.poster_path,
      first_air_date: cand.year, status: '', cast: cand.cast || '', overview: cand.overview || '',
    })
    addOpen.value = false
    ElMessage.success(
      row.emby_checked_at
        ? row.in_emby
          ? `已添加「${row.name}」，Emby 库已收录：${row.emby_item_name || row.name}`
          : `已添加「${row.name}」，Emby 库未收录`
        : `已添加「${row.name}」`,
    )
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function check(r) {
  busy.value[r.id] = true
  try {
    const enq = await api.post(`/tvfollows/${r.id}/check`, {})
    const res = enq.task_id ? await pollTask(enq.task_id) : enq
    if (res.error) {
      ElMessage.error(res.error)
      return
    }
    if (res.follow) {
      const idx = follows.value.findIndex((x) => x.id === r.id)
      if (idx >= 0) follows.value[idx] = res.follow
      ElMessage.success(res.follow.done ? `「${r.name}」已完结，共 ${res.follow.seasons} 季 ${res.follow.episodes} 集` : `已检查「${r.name}」`)
    } else {
      load()
      ElMessage.success(`已发起检查「${r.name}」`)
    }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    busy.value[r.id] = false
  }
}

async function embyCheck(r) {
  embyBusy.value[r.id] = true
  try {
    const row = await api.post(`/tvfollows/${r.id}/emby-check`)
    const idx = follows.value.findIndex((x) => x.id === r.id)
    if (idx >= 0) follows.value[idx] = row
    ElMessage.success(row.in_emby ? `Emby 库已收录：${row.emby_item_name || r.name}` : 'Emby 库未收录')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    embyBusy.value[r.id] = false
  }
}

async function toggle(r, v) {
  try {
    await api.post(`/tvfollows/${r.id}/enable`, { enabled: v })
  } catch (e) {
    r.enabled = !v
    ElMessage.error(e.message)
  }
}

// 短剧标记（58）：detail 与列表同对象引用，开关即卡片标签同步变色
async function toggleShortDrama(r, v) {
  try {
    await api.post(`/tvfollows/${r.id}/short-drama`, { on: v })
  } catch (e) {
    r.short_drama = !v
    ElMessage.error(e.message)
  }
}

async function del(r) {
  try {
    await ElMessageBox.confirm(`不再追「${r.name}」？追踪记录一并删除。`, '删除', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api.del(`/tvfollows/${r.id}`)
    follows.value = follows.value.filter((x) => x.id !== r.id)
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<style scoped>
.tv {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 2px 12px;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.sweep-at {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.tv-filters {
  display: flex;
  align-items: center;
  gap: 4px;
}
.tools {
  display: flex;
  gap: 8px;
  align-items: center;
}
.tv-search {
  width: 240px; /* 200px 装不下「搜索已追的剧名 / 原名」占位（64 用户报截断） */
}
.tv-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(460px, 1fr));
  gap: 12px;
}
.tv-card {
  cursor: pointer;
}

/* 详情弹层（554） */
.d-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.d-head {
  display: flex;
  gap: 14px;
}
.d-poster {
  width: 120px;
  min-width: 120px;
  align-self: flex-start;
  aspect-ratio: 2 / 3;
  border-radius: 10px;
  overflow: hidden;
  background: rgba(127, 127, 127, 0.12);
}
.d-poster img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.d-meta {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.d-chips {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.d-progress {
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: 15px;
}
.d-epname {
  color: var(--el-text-color-regular);
}
.d-cast {
  font-size: 14px;
  color: var(--el-text-color-secondary);
}
.d-links {
  display: flex;
  gap: 14px;
  font-size: 14px;
}
.d-links a {
  color: var(--el-color-primary);
  text-decoration: none;
}
.d-links a:hover {
  text-decoration: underline;
}
.d-res {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 13px;
  border-top: 1px solid rgba(127, 127, 127, 0.15);
  padding-top: 10px;
}
.d-res-label {
  color: var(--el-text-color-secondary);
}
.d-res a {
  color: var(--el-color-primary);
  text-decoration: none;
}
.d-res a:hover {
  text-decoration: underline;
}
.d-overview {
  font-size: 15px;
  line-height: 1.75;
  color: var(--el-text-color-regular);
  white-space: pre-wrap;
}
.d-foot {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  font-size: 13px;
  color: var(--el-text-color-secondary);
  border-top: 1px solid rgba(127, 127, 127, 0.15);
  padding-top: 10px;
}
.d-sd {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  color: var(--el-text-color-secondary);
}
.f-body {
  display: flex;
  gap: 12px;
}
.f-poster {
  width: 150px;
  min-width: 150px;
  align-self: flex-start;
  aspect-ratio: 2 / 3;
  border-radius: 8px;
  overflow: hidden;
  background: rgba(127, 127, 127, 0.12);
}
.f-poster img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.f-poster-empty,
.cand-poster-empty {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 22px;
  color: var(--el-text-color-secondary);
}
.f-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.f-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.f-name {
  font-weight: 600;
  font-size: 17px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.f-orig {
  margin-left: 6px;
  font-weight: 400;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
.f-chips {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
.f-checking {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
.f-progress {
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: 15px;
}
.f-se {
  color: var(--el-color-primary);
  font-variant-numeric: tabular-nums;
}
.f-next {
  color: var(--el-text-color-regular);
}
.f-cast,
.cand-cast {
  font-size: 13.5px;
  color: var(--el-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.muted {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.f-err {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--el-color-danger);
  overflow: hidden;
}
.f-err span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.f-ops {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: auto;
  flex-wrap: wrap;
}
.f-checked {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.add-row {
  display: flex;
  gap: 8px;
}
.add-row .el-input {
  flex: 1;
}
/* 剧集/电影分段胶囊（553 返工：el-radio-button 组在 flex 行内被压成竖排） */
.seg {
  display: flex;
  flex-shrink: 0;
  gap: 4px;
  padding: 3px;
  border-radius: 8px;
  background: rgba(127, 127, 127, 0.12);
}
.seg button {
  border: none;
  background: transparent;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1;
  padding: 8px 14px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}
.seg button:hover {
  color: var(--el-text-color-primary);
}
.seg button.on {
  background: rgba(124, 92, 255, 0.45);
  color: #fff;
}
.add-hint {
  margin: 8px 0 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.cand-list {
  max-height: 52vh;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-right: 4px;
}
.cand {
  display: flex;
  gap: 10px;
  padding: 8px;
  border-radius: 8px;
  cursor: pointer;
  background: rgba(127, 127, 127, 0.06);
  transition: background 0.15s;
}
.cand:hover {
  background: rgba(124, 92, 255, 0.18);
}
.cand-poster {
  width: 64px;
  min-width: 64px;
  align-self: flex-start;
  aspect-ratio: 2 / 3;
  border-radius: 6px;
  overflow: hidden;
  background: rgba(127, 127, 127, 0.12);
  font-size: 13px;
}
.cand-poster img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.cand-info {
  flex: 1;
  min-width: 0;
}
.cand-name {
  font-weight: 600;
  font-size: 14px;
}
.cand-orig {
  margin-left: 6px;
  font-weight: 400;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.cand-emby {
  margin-left: 8px;
  vertical-align: 1px;
}
.cand-meta {
  display: flex;
  gap: 10px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin: 2px 0;
}
.cand-cast {
  margin-bottom: 2px;
}
.cand-overview {
  font-size: 12px;
  color: var(--el-text-color-regular);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

@media (max-width: 768px) {
  .tv-grid {
    grid-template-columns: 1fr;
  }
  .f-poster {
    width: 120px;
    min-width: 120px;
  }
  .tv-search {
    width: 100%;
  }
  .tools {
    width: 100%;
    flex-wrap: wrap;
  }
}
</style>
