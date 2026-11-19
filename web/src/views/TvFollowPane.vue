<template>
  <div class="tv">
    <div class="toolbar">
      <span v-if="lastSweep" class="sweep-at">{{ $t('tv.sweepAt', { t: fmtTime(lastSweep) }) }}</span>
      <div class="tv-filters">
        <el-checkbox v-model="fShort">{{ $t('tv.shortDrama') }}</el-checkbox>
        <el-checkbox v-model="fEnded">{{ $t('tv.stEnded') }}</el-checkbox>
        <el-checkbox v-model="fOnline">{{ $t('tv.filterOnline') }}</el-checkbox>
      </div>
      <div class="tools">
        <el-input
          v-model="searchQ"
          class="tv-search"
          :placeholder="$t('tv.searchPh')"
          clearable
          :prefix-icon="Search"
        />
        <el-button :loading="loading" @click="load">
          <el-icon v-if="!loading"><Refresh /></el-icon>
          <span v-if="!loading">{{ $t('common.refresh') }}</span>
        </el-button>
        <el-button type="primary" @click="openAdd">
          <el-icon><Plus /></el-icon>
          <span>{{ $t('tv.add') }}</span>
        </el-button>
      </div>
    </div>

    <div v-if="filteredFollows.length" class="tv-grid">
      <el-card v-for="r in filteredFollows" :key="r.id" shadow="never" class="tv-card" @click="openDetail(r)">
        <div class="f-body">
          <div class="f-poster">
            <img v-if="r.poster" :src="r.poster" :alt="r.name" loading="lazy" @error="r.poster = ''" />
            <div v-else class="f-poster-empty">{{ r.type === 'movie' ? $t('tv.posterMovie') : $t('tv.posterTv') }}</div>
          </div>
          <div class="f-info">
            <div class="f-head">
              <span class="f-name" :title="r.original_name && r.original_name !== r.name ? r.original_name : r.name">
                {{ r.name }}<span v-if="r.original_name && r.original_name !== r.name" class="f-orig">{{ r.original_name }}</span>
              </span>
              <span class="name-copy" :title="$t('tv.copyName')" @click.stop="copyName(r.name)"><el-icon><CopyDocument /></el-icon></span>
              <span class="f-switch" @click.stop><el-switch v-model="r.enabled" size="small" @change="(v) => toggle(r, v)" /></span>
            </div>
            <div class="f-chips">
              <el-tag size="small" :type="r.type === 'movie' ? 'warning' : 'primary'" effect="plain">{{ r.type === 'movie' ? $t('tv.typeMovie') : $t('tv.typeTv') }}</el-tag>
              <el-tag v-if="r.short_drama" size="small" type="danger" effect="plain">{{ $t('tv.shortDrama') }}</el-tag>
              <el-tag v-if="r.type === 'movie'" size="small" :type="r.platforms ? 'success' : 'info'" effect="plain">
                {{ r.platforms ? $t('tv.onlineWith', { p: r.platforms.split('/').join(' / ') }) : $t('tv.notOnline') }}
              </el-tag>
              <el-tag size="small" :type="statusTagType(r)" effect="plain">{{ statusText(r) }}</el-tag>
              <el-tag v-if="r.emby_checked_at" size="small" :type="r.in_emby ? 'success' : 'info'" effect="plain">
                {{ r.in_emby ? (r.emby_item_name ? $t('tv.embyInName', { name: r.emby_item_name }) : $t('tv.embyIn')) : $t('tv.embyNotIn') }}
              </el-tag>
              <span v-if="r.checking" class="f-checking"><el-icon class="is-loading"><Loading /></el-icon>{{ $t('tv.checking') }}</span>
            </div>
            <div class="f-progress">
              <template v-if="r.type === 'movie'">
                <span v-if="r.last_air_date">{{ $t('tv.releaseDate', { d: r.last_air_date }) }}</span>
                <span v-else class="muted">{{ $t('tv.releaseUnknown') }}</span>
              </template>
              <template v-else>
                <span v-if="r.last_episode">
                  {{ $t('tv.upTo') }} <b class="f-se">S{{ r.last_season }}E{{ r.last_episode }}</b>
                  <span v-if="r.last_air_date" class="muted">{{ $t('tv.airDate', { d: r.last_air_date }) }}</span>
                </span>
                <span v-else class="muted">{{ $t('tv.noProgress') }}</span>
                <span v-if="r.next_episode && !r.done" class="f-next">{{ r.next_air_date ? $t('tv.nextEpWith', { s: r.next_season, e: r.next_episode, d: r.next_air_date }) : $t('tv.nextEp', { s: r.next_season, e: r.next_episode }) }}</span>
                <span v-if="r.seasons || r.episodes" class="muted">{{ $t('tv.totalEp', { s: r.seasons, e: r.episodes }) }}</span>
              </template>
              <span v-if="r.cast" class="f-cast" :title="r.cast">{{ $t('tv.castLabel', { cast: r.cast.split('/').join(' / ') }) }}</span>
            </div>
            <div v-if="r.last_error" class="f-err" :title="r.last_error">
              <el-icon><WarningFilled /></el-icon><span>{{ r.last_error }}</span>
            </div>
            <div class="f-ops" @click.stop>
              <el-button size="small" :loading="busy[r.id]" @click="check(r)">{{ $t('tv.checkNow') }}</el-button>
              <el-button size="small" :loading="embyBusy[r.id]" @click="embyCheck(r)">{{ $t('tv.checkLibrary') }}</el-button>
              <el-button size="small" type="danger" plain @click="del(r)">{{ $t('common.delete') }}</el-button>
              <span v-if="r.last_checked_at" class="f-checked">{{ $t('tv.lastCheck', { t: fmtTime(r.last_checked_at) }) }}</span>
            </div>
          </div>
        </div>
      </el-card>
    </div>
    <el-empty v-else-if="!loading && follows.length" :description="$t('tv.emptyFiltered')" />
    <el-empty v-else-if="!loading" :description="$t('tv.empty')" />

    <!-- 添加追剧（append-to-body：violet 玻璃下 fixed 弹层必须逃出 .main，463 口径） -->
    <el-dialog v-model="addOpen" :title="$t('tv.add')" width="min(620px, 94vw)" append-to-body :close-on-click-modal="false">
      <div class="add-row">
        <div class="seg" role="tablist">
          <button type="button" :class="{ on: addType === 'tv' }" @click="addType = 'tv'">{{ $t('tv.typeTv') }}</button>
          <button type="button" :class="{ on: addType === 'movie' }" @click="addType = 'movie'">{{ $t('tv.typeMovie') }}</button>
        </div>
        <el-input
          v-model="addQ"
          :placeholder="addType === 'movie' ? $t('tv.searchMoviePh') : $t('tv.searchTvPh')"
          clearable
          @keyup.enter="search"
        />
        <el-button type="primary" :loading="searching" @click="search">{{ $t('tv.search') }}</el-button>
      </div>
      <div class="add-hint">{{ $t('tv.addHint') }}</div>
      <div v-if="candidates.length" class="cand-list">
        <div v-for="cand in candidates" :key="cand.id" class="cand" @click="add(cand)">
          <div class="cand-poster">
            <img v-if="cand.poster_path" :src="posterURL(cand.poster_path)" :alt="cand.name" loading="lazy" />
            <div v-else class="cand-poster-empty">{{ $t('tv.noPoster') }}</div>
          </div>
          <div class="cand-info">
            <div class="cand-name">
              {{ cand.name }}<span v-if="cand.original_name && cand.original_name !== cand.name" class="cand-orig">{{ cand.original_name }}</span>
              <el-tag v-if="cand.emby_checked" size="small" :type="cand.in_emby ? 'success' : 'info'" effect="plain" class="cand-emby"
                :title="cand.in_emby ? $t('tv.libInName', { name: cand.emby_item_name || cand.name }) : $t('tv.libNotIn')">
                {{ cand.in_emby ? $t('tv.inLib') : $t('tv.notInLib') }}
              </el-tag>
            </div>
            <div class="cand-meta">
              <span v-if="cand.year">{{ cand.year }}</span>
              <span v-if="cand.rating">⭐ {{ cand.rating.toFixed(1) }}</span>
            </div>
            <div v-if="cand.cast" class="cand-cast">{{ $t('tv.castLabel', { cast: cand.cast.split('/').join(' / ') }) }}</div>
            <div v-if="cand.overview" class="cand-overview">{{ cand.overview }}</div>
          </div>
        </div>
      </div>
      <el-empty v-else-if="searched && !searching" :description="$t('tv.noResult')" :image-size="72" />
    </el-dialog>

    <!-- 剧集详情（554，append-to-body 遵 463 口径）：点卡片弹出全量信息 -->
    <el-dialog v-model="detailOpen" :title="detail ? detail.name : $t('tv.detail')" width="min(560px, 94vw)" append-to-body>
      <div v-if="detail" class="d-body">
        <div class="d-head">
          <div class="d-poster">
            <img v-if="detail.poster" :src="detail.poster" :alt="detail.name" />
            <div v-else class="f-poster-empty">{{ detail.type === 'movie' ? $t('tv.posterMovie') : $t('tv.posterTv') }}</div>
          </div>
          <div class="d-meta">
            <div class="d-name-row">
              <span class="d-name">{{ detail.name }}</span>
              <span class="name-copy" :title="$t('tv.copyName')" @click="copyName(detail.name)"><el-icon><CopyDocument /></el-icon></span>
              <span v-if="detail.original_name && detail.original_name !== detail.name" class="f-orig">{{ detail.original_name }}</span>
            </div>
            <div class="d-chips">
              <el-tag size="small" :type="detail.type === 'movie' ? 'warning' : 'primary'" effect="plain">{{ detail.type === 'movie' ? $t('tv.typeMovie') : $t('tv.typeTv') }}</el-tag>
              <el-tag v-if="detail.short_drama" size="small" type="danger" effect="plain">{{ $t('tv.shortDrama') }}</el-tag>
              <el-tag v-if="detail.type === 'movie'" size="small" :type="detail.platforms ? 'success' : 'info'" effect="plain">
                {{ detail.platforms ? $t('tv.onlineWith', { p: detail.platforms.split('/').join(' / ') }) : $t('tv.notOnline') }}
              </el-tag>
              <el-tag size="small" :type="statusTagType(detail)" effect="plain">{{ statusText(detail) }}</el-tag>
              <el-tag v-if="detail.first_air_date" size="small" effect="plain">{{ detail.first_air_date }}</el-tag>
              <el-tag v-if="detail.emby_checked_at" size="small" :type="detail.in_emby ? 'success' : 'info'" effect="plain">
                {{ detail.in_emby ? (detail.emby_item_name ? $t('tv.embyInName', { name: detail.emby_item_name }) : $t('tv.embyIn')) : $t('tv.embyNotIn') }}
              </el-tag>
            </div>
            <div v-if="detail.type === 'movie'" class="d-progress">
              <span>{{ $t('tv.releaseDate', { d: detail.last_air_date || $t('tv.unknown') }) }}</span>
            </div>
            <div v-else class="d-progress">
              <span v-if="detail.last_episode">
                {{ $t('tv.upTo') }} <b class="f-se">S{{ detail.last_season }}E{{ detail.last_episode }}</b>
                <span v-if="detail.episode_name" class="d-epname">{{ $t('tv.epName', { n: detail.episode_name }) }}</span>
                <span v-if="detail.last_air_date" class="muted">{{ $t('tv.airDate', { d: detail.last_air_date }) }}</span>
              </span>
              <span v-if="detail.next_episode && !detail.done">{{ detail.next_air_date ? $t('tv.nextEpWith', { s: detail.next_season, e: detail.next_episode, d: detail.next_air_date }) : $t('tv.nextEp', { s: detail.next_season, e: detail.next_episode }) }}</span>
              <span v-if="detail.seasons || detail.episodes">{{ $t('tv.totalEp', { s: detail.seasons, e: detail.episodes }) }}</span>
            </div>
            <div v-if="detail.cast" class="d-cast">{{ $t('tv.castLabel', { cast: detail.cast.split('/').join(' / ') }) }}</div>
            <div class="d-links">
              <a :href="tmdbURL(detail)" target="_blank" rel="noopener noreferrer">{{ $t('tv.tmdbLink') }}</a>
              <a :href="doubanURL(detail)" target="_blank" rel="noopener noreferrer">{{ $t('tv.doubanLink') }}</a>
            </div>
          </div>
        </div>
        <div v-if="detail.overview" class="d-overview">{{ detail.overview }}</div>
        <div v-else class="muted" style="margin-top: 10px">{{ $t('tv.noOverview') }}</div>
        <div v-if="resLinks.length" class="d-res">
          <span class="d-res-label">{{ $t('tv.resSearch') }}</span>
          <a v-for="l in resLinks" :key="l.url" :href="l.url" target="_blank" rel="noopener noreferrer">{{ l.name }} ↗</a>
        </div>
        <div class="d-foot">
          <label class="d-sd" @click.stop>
            <el-switch v-model="detail.short_drama" size="small" @change="(v) => toggleShortDrama(detail, v)" />{{ $t('tv.shortDrama') }}
          </label>
          <span v-if="detail.last_checked_at">{{ $t('tv.lastCheck', { t: fmtTime(detail.last_checked_at) }) }}</span>
          <span v-if="detail.last_error" class="f-err"><el-icon><WarningFilled /></el-icon>{{ detail.last_error }}</span>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onActivated, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, Search } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { fmtTime } from '../utils/format.js'
import { lang, registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/tv.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

const follows = ref([])
const loading = ref(false)
const lastSweep = ref(0) // 最近一轮定时抓取时刻（63，秒级 ts）
const busy = ref({})
const embyBusy = ref({})

// 复制剧名（卡片/详情两处共用）：写系统剪贴板
async function copyName(name) {
  try {
    await navigator.clipboard.writeText(name)
    ElMessage.success(t('tv.copied', { name }))
  } catch {
    ElMessage.error(t('tv.copyFail'))
  }
}

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
  if (fEnded.value) list = list.filter((r) => r.done || isEnded(r))
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
  return `https://www.themoviedb.org/${r.type === 'movie' ? 'movie' : 'tv'}/${r.tmdb_id}?language=${lang.value === 'en' ? 'en-US' : 'zh-CN'}`
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
// 完结口径（i18n 化后不再比文案，语言无关）：Ended 状态，或 allAired 追平视同完结
function isEnded(r) {
  return r.status === 'Ended' || (allAired(r) && r.status === 'Returning Series')
}
function statusText(r) {
  if (allAired(r) && r.status === 'Returning Series') return t('tv.stEnded')
  const map = {
    'Returning Series': t('tv.stReturning'), Ended: t('tv.stEnded'), Canceled: t('tv.stCanceled'),
    Released: t('tv.stReleased'), 'Post Production': t('tv.stPost'), 'In Production': t('tv.stProducing'),
    Planned: t('tv.stPlanned'), Pilot: t('tv.stPilot'),
  }
  return map[r.status] || (r.status || t('tv.stUnknown'))
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
  throw new Error(t('tv.egressTimeout'))
}

async function search() {
  const q = addQ.value.trim()
  if (q.length < 2) {
    ElMessage.warning(t('tv.nameTooShort'))
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
          ? t('tv.addedInLib', { name: row.name, item: row.emby_item_name || row.name })
          : t('tv.addedNotInLib', { name: row.name })
        : t('tv.added', { name: row.name }),
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
      ElMessage.success(res.follow.done ? t('tv.checkedDone', { name: r.name, s: res.follow.seasons, e: res.follow.episodes }) : t('tv.checked', { name: r.name }))
    } else {
      load()
      ElMessage.success(t('tv.checkQueued', { name: r.name }))
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
    ElMessage.success(row.in_emby ? t('tv.libInName', { name: row.emby_item_name || r.name }) : t('tv.libNotIn'))
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
    await ElMessageBox.confirm(t('tv.delConfirm', { name: r.name }), t('common.delete'), {
      type: 'warning', confirmButtonText: t('common.delete'), cancelButtonText: t('common.cancel'),
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
.name-copy {
  display: inline-flex;
  align-items: center;
  flex: none;
  margin-left: 6px;
  color: var(--el-text-color-secondary, #909399);
  cursor: pointer;
  opacity: 0.65;
  transition: opacity 0.15s;
}
.name-copy:hover {
  opacity: 1;
  color: var(--el-color-primary);
}
.d-name-row {
  display: flex;
  align-items: baseline;
  gap: 6px;
  margin-bottom: 8px;
}
.d-name {
  font-size: 19px;
  font-weight: 700;
  line-height: 1.3;
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
