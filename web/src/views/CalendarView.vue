<template>
  <!-- 日历（个人助理批，2026-10-26）：月视图聚合提醒/会员到期/倒数日/追剧/B 站/证书/ICS 订阅，
       日格标农历（节日 > 节气 > 初一显月名 > 日名）与法定节假日休/班；左栏=选中日的事件清单
      （useSidePanel 分栏约定：可拖拽调宽、单击分隔条收起；手机端改底部抽屉） -->
  <div class="cal-page">
    <aside v-if="!isMobile" v-show="!side.collapsed" class="cal-side" :style="{ width: side.width + 'px' }">
      <div class="cal-side-head">
        <div class="cal-side-date">{{ selectedTitle }}</div>
        <div v-if="selMark" class="cal-side-lunar">
          {{ selMark.lunar_month }}{{ selMark.lunar_day }}
          <span v-if="selMark.jieqi" class="cal-tag jq">{{ selMark.jieqi }}</span>
          <span v-if="selMark.festival" class="cal-tag fest">{{ selMark.festival }}</span>
          <span v-if="selMark.holiday" class="cal-tag" :class="selMark.holiday.off ? 'off' : 'work'">
            {{ selMark.holiday.name }} · {{ selMark.holiday.off ? $t('calendar.holidayOff') : $t('calendar.holidayWork') }}
          </span>
        </div>
      </div>
      <div class="cal-side-list">
        <div v-if="!selEvents.length" class="cal-empty">{{ $t('calendar.noEvents') }}</div>
        <div v-for="e in selEvents" :key="e.id" class="cal-ev">
          <span class="cal-ev-dot" :style="{ background: colorOf(e) }" />
          <div class="cal-ev-main">
            <div class="cal-ev-title" :class="{ done: e.done }">{{ e.title }}</div>
            <div class="cal-ev-sub">
              {{ e.time || $t('calendar.allDay') }} · {{ srcLabel(e) }}<template v-if="e.sub"> · {{ e.sub }}</template>
              <template v-if="e.done"> · {{ $t('calendar.done') }}</template>
            </div>
            <div v-if="e.note" class="cal-ev-note">{{ e.note }}</div>
          </div>
          <el-button v-if="e.source === 'reminder'" text size="small" :title="$t('calendar.openReminder')" :icon="ArrowRight" @click="openReminder" />
        </div>
      </div>
    </aside>
    <div
      v-if="!isMobile"
      class="side-divider"
      :class="{ on: side.resizing }"
      :title="side.collapsed ? $t('calendar.expandSideTip') : $t('calendar.sideTip')"
      @mousedown="side.startDrag"
    />

    <div class="cal-main">
      <div class="cal-toolbar">
        <div class="cal-nav">
          <el-button-group size="small">
            <el-button :icon="ArrowLeft" :title="$t('calendar.prevMonth')" @click="shiftMonth(-1)" />
            <el-button @click="goToday">{{ $t('calendar.today') }}</el-button>
            <el-button :icon="ArrowRight" :title="$t('calendar.nextMonth')" @click="shiftMonth(1)" />
          </el-button-group>
          <h2 class="cal-title">{{ monthTitle }}</h2>
        </div>
        <div class="cal-actions">
          <el-button size="small" :icon="Timer" @click="cdOpen = true">{{ $t('calendar.countdowns') }}</el-button>
          <el-button size="small" :icon="Coffee" @click="openHolidays">{{ $t('calendar.holidays') }}</el-button>
          <el-button size="small" :icon="Link" @click="openSubs">{{ $t('calendar.subs') }}</el-button>
          <el-button size="small" :icon="Share" @click="openFeed">{{ $t('calendar.export') }}</el-button>
        </div>
      </div>
      <div class="cal-legend">
        <button
          v-for="s in SOURCES"
          :key="s.key"
          type="button"
          class="cal-legend-item"
          :class="{ off: hidden.has(s.key) }"
          :style="{ '--c': s.color }"
          :title="legendTip(s)"
          @click="toggleSrc(s.key)"
        ><i />{{ $t(s.label) }}</button>
        <span v-if="!holidayKnown" class="cal-legend-warn">{{ $t('calendar.noHolidayData', { year: cur.y }) }}</span>
      </div>

      <div class="cal-weekdays">
        <span v-for="(w, i) in weekdays" :key="w" :class="{ we: i >= 5 }">{{ w }}</span>
      </div>
      <div v-loading="loading" class="cal-grid">
        <button
          v-for="c in cells"
          :key="c.date"
          type="button"
          class="cal-cell"
          :class="{ out: !c.inMonth, today: c.isToday, sel: c.date === selected, we: c.weekend }"
          @click="select(c.date)"
        >
          <div class="cal-cell-head">
            <span class="cal-num">{{ c.day }}</span>
            <span v-if="c.mark?.holiday" class="cal-hol" :class="c.mark.holiday.off ? 'off' : 'work'">
              {{ c.mark.holiday.off ? $t('calendar.holidayOff') : $t('calendar.holidayWork') }}
            </span>
          </div>
          <div class="cal-lunar" :class="c.labelKind">{{ c.label }}</div>
          <div class="cal-evs">
            <span
              v-for="e in c.events.slice(0, maxChips)"
              :key="e.id"
              class="cal-chip"
              :class="{ done: e.done }"
              :style="{ '--c': colorOf(e) }"
              :title="e.title"
            >{{ e.title }}</span>
            <span v-if="c.events.length > maxChips" class="cal-more">{{ $t('calendar.more', { n: c.events.length - maxChips }) }}</span>
          </div>
        </button>
      </div>
    </div>

    <!-- 手机端：点日格弹底部抽屉看当天事件 -->
    <el-drawer v-if="isMobile" v-model="dayDrawer" direction="btt" size="62%" append-to-body :title="selectedTitle">
      <div v-if="selMark" class="cal-side-lunar" style="margin-bottom: 8px">
        {{ selMark.lunar_month }}{{ selMark.lunar_day }}
        <span v-if="selMark.jieqi" class="cal-tag jq">{{ selMark.jieqi }}</span>
        <span v-if="selMark.festival" class="cal-tag fest">{{ selMark.festival }}</span>
        <span v-if="selMark.holiday" class="cal-tag" :class="selMark.holiday.off ? 'off' : 'work'">
          {{ selMark.holiday.name }} · {{ selMark.holiday.off ? $t('calendar.holidayOff') : $t('calendar.holidayWork') }}
        </span>
      </div>
      <div v-if="!selEvents.length" class="cal-empty">{{ $t('calendar.noEvents') }}</div>
      <div v-for="e in selEvents" :key="e.id" class="cal-ev">
        <span class="cal-ev-dot" :style="{ background: colorOf(e) }" />
        <div class="cal-ev-main">
          <div class="cal-ev-title" :class="{ done: e.done }">{{ e.title }}</div>
          <div class="cal-ev-sub">
            {{ e.time || $t('calendar.allDay') }} · {{ srcLabel(e) }}<template v-if="e.sub"> · {{ e.sub }}</template>
          </div>
          <div v-if="e.note" class="cal-ev-note">{{ e.note }}</div>
        </div>
      </div>
    </el-drawer>

    <!-- 订阅管理 -->
    <el-dialog v-model="subsOpen" :title="$t('calendar.subsTitle')" :width="isMobile ? '94%' : '760px'" append-to-body :close-on-click-modal="false">
      <div class="cal-hint">{{ $t('calendar.subsHint') }}</div>
      <div class="cal-sub-toolbar">
        <el-button type="primary" size="small" :icon="Plus" @click="startSubAdd">{{ $t('calendar.subAdd') }}</el-button>
        <span class="cal-hint">{{ subs.length }} / 20</span>
      </div>
      <div v-if="subEditing" class="cal-sub-form">
        <el-form label-width="80px" size="small" @submit.prevent>
          <el-form-item :label="$t('calendar.subName')">
            <el-input v-model="subForm.name" maxlength="40" :placeholder="$t('calendar.subNamePh')" />
          </el-form-item>
          <el-form-item :label="$t('calendar.subUrl')">
            <el-input v-model="subForm.url" :placeholder="$t('calendar.subUrlPh')" />
          </el-form-item>
          <el-form-item :label="$t('calendar.subColor')">
            <div class="cal-palette">
              <button
                v-for="c in CD_PALETTE"
                :key="c"
                type="button"
                class="cal-swatch"
                :class="{ on: subForm.color === c }"
                :style="{ background: c }"
                @click="subForm.color = c"
              />
            </div>
          </el-form-item>
          <el-form-item :label="$t('calendar.subEnabled')">
            <el-switch v-model="subForm.enabled" />
          </el-form-item>
        </el-form>
        <div class="cal-sub-foot">
          <el-button size="small" @click="subEditing = false">{{ $t('cd.cancel') }}</el-button>
          <el-button size="small" type="primary" :loading="subSaving" @click="saveSub">{{ $t('cd.save') }}</el-button>
        </div>
      </div>
      <div v-if="!subs.length && !subEditing" class="cal-empty">{{ $t('calendar.subEmpty') }}</div>
      <div v-for="s in subs" :key="s.id" class="cal-sub-row">
        <span class="cal-ev-dot" :style="{ background: s.color || '#67c23a' }" />
        <div class="cal-ev-main">
          <div class="cal-ev-title">
            {{ s.name }}
            <el-tag v-if="!s.enabled" size="small" type="info" effect="plain">{{ $t('calendar.subEnabled') }}: ✕</el-tag>
          </div>
          <div class="cal-ev-sub cal-sub-url" :title="s.url">{{ s.url }}</div>
          <div class="cal-ev-sub">
            {{ $t('calendar.subLastFetch') }}: {{ s.last_fetch_at ? fmtTime(s.last_fetch_at) : $t('calendar.subNever') }}
            · {{ $t('calendar.subEventsN', { n: s.event_count }) }}
            <span v-if="s.last_error" class="cal-sub-err" :title="s.last_error">· {{ s.last_error }}</span>
          </div>
        </div>
        <el-button size="small" text :icon="Refresh" :loading="subRefreshing === s.id" :title="$t('calendar.subRefresh')" @click="refreshSub(s)" />
        <el-button size="small" text :icon="EditPen" :title="$t('calendar.subEdit')" @click="startSubEdit(s)" />
        <el-button size="small" text type="danger" :icon="Delete" :title="$t('calendar.subDelete')" @click="delSub(s)" />
      </div>
    </el-dialog>

    <!-- 导出到手机日历 -->
    <el-dialog v-model="feedOpen" :title="$t('calendar.feedTitle')" :width="isMobile ? '94%' : '600px'" append-to-body>
      <div class="cal-hint">{{ $t('calendar.feedHint') }}</div>
      <el-form label-width="80px" size="small" style="margin-top: 12px" @submit.prevent>
        <el-form-item :label="$t('calendar.feedUrl')">
          <el-input :model-value="feedUrl" readonly>
            <template #append>
              <el-button :icon="CopyDocument" @click="copyFeed">{{ $t('calendar.feedCopy') }}</el-button>
            </template>
          </el-input>
        </el-form-item>
        <el-form-item :label="$t('calendar.feedSources')">
          <div class="cal-feed-src">
            <el-checkbox v-model="feed.sources.reminders">{{ $t('calendar.srcReminder') }}</el-checkbox>
            <el-checkbox v-model="feed.sources.memberships">{{ $t('calendar.srcMembership') }}</el-checkbox>
            <el-checkbox v-model="feed.sources.countdowns">{{ $t('calendar.srcCountdown') }}</el-checkbox>
            <el-checkbox v-model="feed.sources.tv">{{ $t('calendar.srcTv') }}</el-checkbox>
            <el-checkbox v-model="feed.sources.certs">{{ $t('calendar.srcCert') }}</el-checkbox>
          </div>
        </el-form-item>
      </el-form>
      <div class="cal-sub-foot">
        <el-button size="small" type="danger" plain @click="rotateFeed">{{ $t('calendar.feedRotate') }}</el-button>
        <el-button size="small" type="primary" :loading="feedSaving" @click="saveFeedSources">{{ $t('cd.save') }}</el-button>
      </div>
    </el-dialog>

    <!-- 休息日设置：法定节假日数据来源（内置表 + holiday-cn 每日同步）、更新时间、各年份清单、手动更新 -->
    <el-dialog v-model="holOpen" :title="$t('calendar.holTitle')" :width="isMobile ? '94%' : '660px'" append-to-body>
      <div class="cal-hint">{{ $t('calendar.holHint') }}</div>
      <div class="cal-hol-status">
        <span class="cal-hol-time">{{ hol.sync_at ? $t('calendar.holUpdated', { t: fmtTime(hol.sync_at) }) : $t('calendar.holNever') }}</span>
        <el-button size="small" :icon="Refresh" :loading="holSyncing" @click="syncHolidays">{{ $t('calendar.holRefresh') }}</el-button>
      </div>
      <el-alert v-if="hol.error" type="warning" :closable="false" show-icon :title="$t('calendar.holError', { msg: hol.error })" class="cal-hol-err" />
      <el-tabs v-if="hol.years.length" v-model="holYear">
        <el-tab-pane v-for="y in hol.years" :key="y.year" :name="String(y.year)" :label="String(y.year)">
          <div class="cal-hol-meta">
            {{ y.source === 'remote' ? $t('calendar.holSrcRemote', { t: fmtTime(y.fetched_at) }) : $t('calendar.holSrcBuiltin') }}
            · {{ $t('calendar.holCount', { off: holCount(y, true), work: holCount(y, false) }) }}
          </div>
          <el-table :data="y.days" size="small" max-height="380">
            <el-table-column :label="$t('calendar.holDate')" width="190">
              <template #default="{ row }">{{ row.date }} {{ $t('calendar.holWeekday', { w: weekdayOf(row.date) }) }}</template>
            </el-table-column>
            <el-table-column :label="$t('calendar.holName')" prop="name" min-width="140" />
            <el-table-column :label="$t('calendar.holType')" width="90">
              <template #default="{ row }">
                <span class="cal-tag" :class="row.off ? 'off' : 'work'">{{ row.off ? $t('calendar.holidayOff') : $t('calendar.holidayWork') }}</span>
              </template>
            </el-table-column>
          </el-table>
        </el-tab-pane>
      </el-tabs>
      <div v-else class="cal-empty">{{ $t('calendar.holEmpty') }}</div>
    </el-dialog>

    <CountdownDialog v-model="cdOpen" @changed="load" />
  </div>
</template>

<script setup>
defineOptions({ name: 'CalendarView' })
import { computed, onActivated, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowLeft, ArrowRight, Coffee, CopyDocument, Delete, EditPen, Link, Plus, Refresh, Share, Timer } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api/index.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/calendar.js'
import { useIsMobile } from '../composables/useIsMobile.js'
import { useSidePanel } from '../composables/useSidePanel.js'
import { fmtTime } from '../utils/format.js'
import { CD_PALETTE } from '../utils/countdown.js'
import CountdownDialog from '../components/CountdownDialog.vue'

registerPageMessages(pageMsgs)
const { t } = useI18n()
const router = useRouter()
const isMobile = useIsMobile()
const side = useSidePanel('calendar', 300)

// 来源配色（倒数日/ICS 自带颜色优先）
const SOURCES = [
  { key: 'reminder', label: 'calendar.srcReminder', color: '#409eff' },
  { key: 'membership', label: 'calendar.srcMembership', color: '#e6a23c' },
  { key: 'countdown', label: 'calendar.srcCountdown', color: '#9b6ef3' },
  { key: 'tv', label: 'calendar.srcTv', color: '#e1306c' },
  { key: 'bili', label: 'calendar.srcBili', color: '#14b8c4' },
  { key: 'cert', label: 'calendar.srcCert', color: '#f56c6c' },
  { key: 'ics', label: 'calendar.srcIcs', color: '#67c23a' },
]
const SRC_MAP = Object.fromEntries(SOURCES.map((s) => [s.key, s]))
function colorOf(e) {
  if ((e.source === 'countdown' || e.source === 'ics') && e.color) return e.color
  return SRC_MAP[e.source]?.color || '#909399'
}
function srcLabel(e) {
  return t(SRC_MAP[e.source]?.label || 'calendar.srcIcs')
}

// 图例即开关：点一下隐藏/显示该来源，日格与侧栏清单同步过滤。localStorage 记「隐藏项」而非
// 「显示项」，日后新增来源默认可见；未知键（来源下线/改名）读入时丢弃
const HIDDEN_KEY = 'lz.cal.hidden'
function loadHidden() {
  try {
    const a = JSON.parse(localStorage.getItem(HIDDEN_KEY) || '[]')
    return new Set(Array.isArray(a) ? a.filter((k) => SRC_MAP[k]) : [])
  } catch {
    return new Set()
  }
}
const hidden = ref(loadHidden())
function toggleSrc(key) {
  const s = new Set(hidden.value)
  if (s.has(key)) s.delete(key)
  else s.add(key)
  hidden.value = s
  localStorage.setItem(HIDDEN_KEY, JSON.stringify([...s]))
}
function legendTip(s) {
  const name = t(s.label)
  return hidden.value.has(s.key) ? t('calendar.legendShowTip', { name }) : t('calendar.legendHideTip', { name })
}

// 本地日期串（避开 toISOString 的 UTC 偏移）
function ymd(d) {
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}
const todayStr = () => ymd(new Date())

const now = new Date()
const cur = reactive({ y: now.getFullYear(), m: now.getMonth() + 1 })
const selected = ref(todayStr())
const marks = ref({}) // date → DayMark
const evMap = ref({}) // date → Event[]
// 过滤掉图例关掉的来源；日格 +N 计数与侧栏清单都按过滤后的算
const shownMap = computed(() => {
  if (!hidden.value.size) return evMap.value
  const out = {}
  for (const [k, list] of Object.entries(evMap.value)) {
    const v = list.filter((e) => !hidden.value.has(e.source))
    if (v.length) out[k] = v
  }
  return out
})
const knownYears = ref([])
const holidayKnown = computed(() => !knownYears.value.length || knownYears.value.includes(cur.y))
const loading = ref(false)
const dayDrawer = ref(false)
const cdOpen = ref(false)

const weekdays = computed(() => t('calendar.weekdaysShort').split(','))
const monthTitle = computed(() => t('calendar.monthTitle', { y: cur.y, m: cur.m }))
const maxChips = computed(() => (isMobile.value ? 1 : 3))

// 6 行 × 7 列，周一起（与国内日历习惯一致）；网格首日=本月 1 号所在周的周一
const gridStart = computed(() => {
  const first = new Date(cur.y, cur.m - 1, 1)
  const off = (first.getDay() + 6) % 7
  return new Date(cur.y, cur.m - 1, 1 - off)
})
const cells = computed(() => {
  const out = []
  const td = todayStr()
  for (let i = 0; i < 42; i++) {
    const d = new Date(gridStart.value.getFullYear(), gridStart.value.getMonth(), gridStart.value.getDate() + i)
    const key = ymd(d)
    const mark = marks.value[key]
    const { text, kind } = cellLabel(mark)
    out.push({
      date: key,
      day: d.getDate(),
      inMonth: d.getMonth() === cur.m - 1,
      isToday: key === td,
      weekend: d.getDay() === 0 || d.getDay() === 6,
      mark,
      label: text,
      labelKind: kind,
      events: shownMap.value[key] || [],
    })
  }
  return out
})
function cellLabel(mark) {
  if (!mark) return { text: '', kind: '' }
  if (mark.festival) return { text: mark.festival.split(' ')[0], kind: 'fest' }
  if (mark.jieqi) return { text: mark.jieqi, kind: 'jq' }
  if (mark.lunar_day === '初一') return { text: mark.lunar_month, kind: 'm1' }
  return { text: mark.lunar_day, kind: '' }
}

const selMark = computed(() => marks.value[selected.value])
const selEvents = computed(() => shownMap.value[selected.value] || [])
const selectedTitle = computed(() => {
  const d = new Date(selected.value + 'T00:00:00')
  const wd = weekdays.value[(d.getDay() + 6) % 7]
  return t('calendar.dayEvents', { date: `${selected.value} ${wd}` })
})

async function load() {
  loading.value = true
  try {
    const from = ymd(gridStart.value)
    const end = new Date(gridStart.value.getFullYear(), gridStart.value.getMonth(), gridStart.value.getDate() + 41)
    const r = await api.get(`/calendar?from=${from}&to=${ymd(end)}`)
    const m = {}
    for (const d of r.days || []) m[d.date] = d
    marks.value = m
    const ev = {}
    for (const e of r.events || []) {
      // 多日事件（ICS）在起止每天都挂一条
      const dates = [e.date]
      if (e.end_date && e.end_date > e.date) {
        const s = new Date(e.date + 'T00:00:00')
        const t2 = new Date(e.end_date + 'T00:00:00')
        for (let d = new Date(s.getTime() + 86400000); d <= t2 && dates.length < 62; d = new Date(d.getTime() + 86400000)) dates.push(ymd(d))
      }
      for (const dt of dates) (ev[dt] ||= []).push(e)
    }
    for (const k of Object.keys(ev)) ev[k].sort((a, b) => (a.all_day === b.all_day ? (a.time || '').localeCompare(b.time || '') : a.all_day ? -1 : 1))
    evMap.value = ev
    knownYears.value = r.known_years || []
  } catch (e) {
    ElMessage.error(e.message || t('calendar.loadFail'))
  } finally {
    loading.value = false
  }
}

function shiftMonth(n) {
  const d = new Date(cur.y, cur.m - 1 + n, 1)
  cur.y = d.getFullYear()
  cur.m = d.getMonth() + 1
  load()
}
function goToday() {
  const d = new Date()
  cur.y = d.getFullYear()
  cur.m = d.getMonth() + 1
  selected.value = todayStr()
  load()
}
function select(date) {
  selected.value = date
  if (isMobile.value) dayDrawer.value = true
}
function openReminder() {
  router.push('/console/reminders')
}

// ---- 订阅管理 ----
const subsOpen = ref(false)
const subs = ref([])
const subEditing = ref(false)
const subSaving = ref(false)
const subRefreshing = ref(0)
const subForm = reactive({ id: 0, name: '', url: '', color: CD_PALETTE[2], enabled: true })
async function loadSubs() {
  try {
    const r = await api.get('/calendar/subs')
    subs.value = r.items || []
  } catch (e) {
    ElMessage.error(e.message)
  }
}
function openSubs() {
  subEditing.value = false
  subsOpen.value = true
  loadSubs()
}
function startSubAdd() {
  Object.assign(subForm, { id: 0, name: '', url: '', color: CD_PALETTE[2], enabled: true })
  subEditing.value = true
}
function startSubEdit(s) {
  Object.assign(subForm, { id: s.id, name: s.name, url: s.url, color: s.color || CD_PALETTE[2], enabled: !!s.enabled })
  subEditing.value = true
}
async function saveSub() {
  subSaving.value = true
  try {
    const body = { name: subForm.name.trim(), url: subForm.url.trim(), color: subForm.color, enabled: subForm.enabled }
    if (subForm.id) await api.put(`/calendar/subs/${subForm.id}`, body)
    else await api.post('/calendar/subs', body)
    subEditing.value = false
    ElMessage.success(t('calendar.subSaved'))
    await loadSubs()
    load()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    subSaving.value = false
  }
}
async function refreshSub(s) {
  subRefreshing.value = s.id
  try {
    const r = await api.post(`/calendar/subs/${s.id}/refresh`)
    ElMessage.success(t('calendar.subRefreshed', { n: r.event_count }))
    await loadSubs()
    load()
  } catch (e) {
    ElMessage.error(e.message)
    loadSubs()
  } finally {
    subRefreshing.value = 0
  }
}
async function delSub(s) {
  try {
    await ElMessageBox.confirm(t('calendar.subDelConfirm', { name: s.name }), t('calendar.subDelete'), { type: 'warning', confirmButtonText: t('calendar.subDelete') })
  } catch {
    return
  }
  try {
    await api.del(`/calendar/subs/${s.id}`)
    await loadSubs()
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 休息日设置 ----
const holOpen = ref(false)
const holSyncing = ref(false)
const holYear = ref('')
const hol = reactive({ sync_at: 0, error: '', years: [] })
function pickHolYear() {
  const ys = hol.years.map((y) => String(y.year))
  if (!ys.includes(holYear.value)) holYear.value = ys.includes(String(cur.y)) ? String(cur.y) : ys[0] || ''
}
async function openHolidays() {
  try {
    Object.assign(hol, await api.get('/calendar/holidays'))
    pickHolYear()
    holOpen.value = true
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function syncHolidays() {
  holSyncing.value = true
  try {
    Object.assign(hol, await api.post('/calendar/holidays/sync'))
    pickHolYear()
    if (hol.error) ElMessage.warning(t('calendar.holSyncFail'))
    else ElMessage.success(t('calendar.holSynced'))
    load()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    holSyncing.value = false
  }
}
const holCount = (y, off) => (y.days || []).filter((d) => !!d.off === off).length
function weekdayOf(date) {
  return weekdays.value[(new Date(date + 'T00:00:00').getDay() + 6) % 7]
}

// ---- 导出订阅 ----
const feedOpen = ref(false)
const feedSaving = ref(false)
const feed = reactive({ path: '', token: '', sources: { reminders: true, memberships: true, countdowns: true, tv: true, certs: false } })
const feedUrl = computed(() => (feed.token ? `${location.origin}${feed.path}?token=${feed.token}` : ''))
async function openFeed() {
  try {
    const r = await api.get('/calendar/feed-info')
    feed.path = r.path
    feed.token = r.token
    Object.assign(feed.sources, r.sources || {})
    feedOpen.value = true
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function copyFeed() {
  try {
    await navigator.clipboard.writeText(feedUrl.value)
    ElMessage.success(t('calendar.feedCopied'))
  } catch {
    // 非安全上下文/权限拒绝：退回手工选中复制
    const el = document.createElement('textarea')
    el.value = feedUrl.value
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    document.body.removeChild(el)
    ElMessage.success(t('calendar.feedCopied'))
  }
}
async function rotateFeed() {
  try {
    await ElMessageBox.confirm(t('calendar.feedRotateConfirm'), t('calendar.feedRotate'), { type: 'warning' })
  } catch {
    return
  }
  try {
    const r = await api.post('/calendar/feed-token')
    feed.token = r.token
    ElMessage.success(t('calendar.feedRotated'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function saveFeedSources() {
  feedSaving.value = true
  try {
    await api.put('/calendar/feed-sources', { ...feed.sources })
    ElMessage.success(t('calendar.feedSaved'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    feedSaving.value = false
  }
}

onMounted(load)
onActivated(() => {
  // keep-alive 切回：正在看当月且跨天了，把选中日对回今天
  const d = new Date()
  if (selected.value !== todayStr() && cur.y === d.getFullYear() && cur.m === d.getMonth() + 1) selected.value = todayStr()
  load()
})
</script>

<style scoped>
.cal-page {
  display: flex;
  gap: 0;
  height: calc(100vh - 130px);
}
.cal-side {
  flex: none;
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  min-height: 0;
  overflow: hidden;
}
.cal-side-head {
  padding: 12px 14px 10px;
  border-bottom: 1px solid var(--border);
}
.cal-side-date {
  font-size: 15px;
  font-weight: 600;
}
.cal-side-lunar {
  margin-top: 4px;
  font-size: 12px;
  color: var(--muted);
  display: flex;
  flex-wrap: wrap;
  gap: 4px 6px;
  align-items: center;
}
.cal-tag {
  display: inline-block;
  padding: 0 6px;
  border-radius: 4px;
  font-size: 11px;
  line-height: 18px;
  background: var(--el-fill-color-light);
}
.cal-tag.jq {
  color: var(--ok);
}
.cal-tag.fest {
  color: var(--crit);
}
.cal-tag.off {
  color: #fff;
  background: var(--crit);
}
.cal-tag.work {
  color: #fff;
  background: var(--muted);
}
.cal-side-list {
  flex: 1;
  overflow: auto;
  padding: 6px 10px;
}
.cal-empty {
  padding: 26px 0;
  text-align: center;
  color: var(--muted);
  font-size: 13px;
}
.cal-ev {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 8px 4px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.cal-ev:last-child {
  border-bottom: none;
}
.cal-ev-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex: none;
  margin-top: 5px;
}
.cal-ev-main {
  flex: 1;
  min-width: 0;
}
.cal-ev-title {
  font-size: 14px;
  font-weight: 600;
  word-break: break-word;
}
.cal-ev-title.done {
  text-decoration: line-through;
  color: var(--muted);
  font-weight: normal;
}
.cal-ev-sub {
  font-size: 12px;
  color: var(--muted);
  margin-top: 2px;
}
.cal-ev-note {
  font-size: 12px;
  color: var(--el-text-color-regular);
  margin-top: 2px;
  white-space: pre-wrap;
}

.cal-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px 12px;
}
.cal-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.cal-nav {
  display: flex;
  align-items: center;
  gap: 12px;
}
.cal-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}
.cal-actions {
  display: flex;
  gap: 8px;
}
.cal-legend {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 2px;
  margin: 8px 0 6px;
  font-size: 12px;
  color: var(--muted);
}
/* 图例项是开关按钮：关掉的来源整项变淡、圆点改空心 */
.cal-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px 7px;
  border: none;
  border-radius: 10px;
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
}
.cal-legend-item:hover {
  background: var(--el-fill-color-light);
  color: var(--ink);
}
.cal-legend-item i {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--c);
  flex: none;
}
.cal-legend-item.off {
  opacity: 0.5;
}
.cal-legend-item.off i {
  background: transparent;
  box-shadow: inset 0 0 0 1.5px var(--c);
}
.cal-legend-warn {
  color: var(--warn-text);
  padding-left: 7px;
}
.cal-weekdays {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  font-size: 12px;
  color: var(--muted);
  text-align: center;
  padding: 2px 0 4px;
}
.cal-weekdays .we {
  color: var(--crit);
}
.cal-grid {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  grid-auto-rows: minmax(84px, 1fr);
  gap: 8px;
  padding: 2px;
}
/* 日格=独立磨砂小卡：底色取主题面板色再降不透明度（玻璃主题本就是半透明色值，叠在
   容器上成两层玻璃；经典主题实色打薄成淡块），backdrop-filter 把后面的主题光晕揉进来 */
.cal-cell {
  background: var(--surface);
  background: color-mix(in srgb, var(--surface) 72%, transparent);
  border: 1px solid var(--border);
  border-radius: 10px;
  backdrop-filter: blur(10px) saturate(1.2);
  -webkit-backdrop-filter: blur(10px) saturate(1.2);
  padding: 5px 7px;
  text-align: left;
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  min-height: 0;
  cursor: pointer;
  font: inherit;
  color: var(--ink);
  position: relative;
  /* 带 backdrop-filter 的元素不过渡 background（主页玻璃卡先例：逐帧重 raster 模糊层出白带） */
  transition: box-shadow 0.15s;
}
.cal-cell:hover {
  background: color-mix(in srgb, var(--surface) 92%, transparent);
}
.cal-cell.sel {
  box-shadow: inset 0 0 0 2px var(--brand);
}
.cal-cell.out {
  color: var(--muted);
  opacity: 0.6;
}
.cal-cell-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.cal-num {
  font-size: 14px;
  font-weight: 600;
  width: 24px;
  height: 24px;
  line-height: 24px;
  text-align: center;
  border-radius: 50%;
}
.cal-cell.we .cal-num {
  color: var(--crit);
}
.cal-cell.today .cal-num {
  background: var(--brand);
  color: #fff;
}
.cal-hol {
  font-size: 10px;
  line-height: 16px;
  padding: 0 4px;
  border-radius: 3px;
  color: #fff;
}
.cal-hol.off {
  background: var(--crit);
}
.cal-hol.work {
  background: var(--muted);
}
.cal-lunar {
  font-size: 11px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.cal-lunar.fest {
  color: var(--crit);
}
.cal-lunar.jq {
  color: var(--ok);
}
.cal-lunar.m1 {
  color: var(--brand);
}
.cal-evs {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  overflow: hidden;
}
.cal-chip {
  font-size: 11px;
  line-height: 16px;
  padding: 0 4px;
  border-radius: 3px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  border-left: 3px solid var(--c);
  background: var(--el-fill-color-light);
  background: color-mix(in srgb, var(--c) 16%, transparent);
}
.cal-chip.done {
  text-decoration: line-through;
  opacity: 0.6;
}
.cal-more {
  font-size: 11px;
  color: var(--muted);
}

.cal-hint {
  font-size: 12px;
  color: var(--muted);
  line-height: 1.6;
}
.cal-sub-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 10px 0;
}
.cal-sub-form {
  padding: 10px 12px 6px;
  margin-bottom: 10px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
  background: var(--el-fill-color-lighter);
}
.cal-sub-foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 8px;
}
.cal-palette {
  display: flex;
  gap: 8px;
}
.cal-swatch {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: 2px solid transparent;
  cursor: pointer;
  padding: 0;
}
.cal-swatch.on {
  border-color: var(--el-text-color-primary);
  box-shadow: 0 0 0 2px var(--el-bg-color);
}
.cal-sub-row {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 8px 4px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.cal-sub-row:last-child {
  border-bottom: none;
}
.cal-sub-url {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.cal-sub-err {
  color: var(--crit);
}
.cal-feed-src {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
}
.cal-hol-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin: 10px 0 6px;
}
.cal-hol-time {
  font-size: 13px;
}
.cal-hol-err {
  margin-bottom: 8px;
}
.cal-hol-meta {
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 6px;
}

@media (max-width: 768px) {
  .cal-page {
    height: auto;
    min-height: calc(100vh - 130px);
  }
  .cal-main {
    padding: 8px;
  }
  .cal-grid {
    grid-auto-rows: minmax(64px, auto);
    gap: 5px;
  }
  .cal-cell {
    padding: 3px 4px;
    border-radius: 8px;
  }
  .cal-num {
    font-size: 13px;
    width: 22px;
    height: 22px;
    line-height: 22px;
  }
}
</style>
