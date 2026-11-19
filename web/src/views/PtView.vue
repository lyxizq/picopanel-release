<template>
  <div class="ptview">
    <div class="toolbar">
      <span class="updated">{{ $t('pt.dailyFetch') }}{{ loadedAt ? $t('pt.updatedAt', { t: loadedAt }) : $t('pt.initialHint') }}</span>
      <div class="tools">
        <el-button :loading="refreshingAll" @click="refreshAll" :title="$t('pt.refreshAllTip')">
          <el-icon v-if="!refreshingAll"><Refresh /></el-icon>
          <span v-if="!refreshingAll">{{ $t('pt.refreshAll') }}</span>
        </el-button>
        <el-button type="primary" @click="openAdd">
          <el-icon><Plus /></el-icon>
          <span>{{ $t('pt.addSite') }}</span>
        </el-button>
      </div>
    </div>

    <div v-if="sites.length" class="site-grid">
      <div v-for="s in sites" :key="s.id" class="site-card" :class="{ off: !s.enabled, over: drag.over === s.id }"
        draggable="true"
        @dragstart="dragStart(s, $event)"
        @dragend="dragEnd"
        @dragenter.prevent="dragOver(s.id, $event)"
        @dragover.prevent="dragOver(s.id, $event)"
        @drop.prevent="dragDrop(s.id)">
        <div class="c-head">
          <img v-if="!s._fallback" class="c-ico" :src="iconSrc(s)" alt="" draggable="false" @error="iconErr(s)" />
          <span v-else class="c-ico c-ico-fallback">{{ s.name.slice(0, 1) }}</span>
          <a class="c-name" :href="s.site_url" target="_blank" rel="noopener" draggable="false" :title="$t('pt.openSiteTip', { url: s.site_url })">{{ s.name }}</a>
          <span v-if="d(s).level" class="c-level-chip" :title="d(s).level">{{ d(s).level }}</span>
          <span v-else class="c-level-chip c-level-dim" :title="$t('pt.notFetchedTip')">{{ $t('pt.notLoggedIn') }}</span>
        </div>

        <div class="c-user">
          <span class="c-username" :title="d(s).username || ''">{{ d(s).username || $t('pt.noUsername') }}</span>
          <span class="c-fetched" :title="s.fetched_at ? fmtTime(s.fetched_at) : ''">{{ s.fetched_at ? relTime(s.fetched_at) : $t('pt.notFetched') }} · {{ exitLabel(s) }}</span>
        </div>

        <div v-if="s.site_key === 'dian115'" class="c-grid">
          <div class="c-item"><span class="c-label">{{ $t('pt.bonus') }}</span><span class="c-val c-strong">{{ fmtInt(d(s).bonus) }}</span></div>
          <div class="c-item">
            <span class="c-label">{{ $t('pt.today') }}</span>
            <span class="c-val">
              <el-tag v-if="signToday(s, 'dian')" size="small" type="success" effect="plain">{{ $t('pt.signed', { n: dianReward(s) }) }}</el-tag>
              <el-tag v-else size="small" type="info" effect="plain">{{ $t('pt.notSigned') }}</el-tag>
              <span v-if="lastSignShort(s)" class="c-last-sign" :title="$t('pt.lastSignTip', { d: lastSignDate(s) })">{{ lastSignShort(s) }}</span>
            </span>
          </div>
          <div class="c-item"><span class="c-label">{{ $t('pt.streak') }}</span><span class="c-val">{{ d(s).dian ? $t('pt.days', { n: d(s).dian.streak || 0 }) : '-' }}</span></div>
          <div class="c-item"><span class="c-label">{{ $t('pt.joined') }}</span><span class="c-val">{{ d(s).joined_at ? fmtDate(d(s).joined_at) : '-' }}</span></div>
        </div>
        <!-- 栖影 HDHaven：网盘资源社区无上传/下载语义，只展示积分与签到状态（432） -->
        <div v-else-if="s.site_key === 'hdhaven'" class="c-grid">
          <div class="c-item"><span class="c-label">{{ $t('pt.bonus') }}</span><span class="c-val c-strong">{{ fmtInt(d(s).bonus) }}</span></div>
          <div class="c-item">
            <span class="c-label">{{ $t('pt.today') }}</span>
            <span class="c-val">
              <el-tag v-if="signToday(s, 'hdh')" size="small" type="success" effect="plain">{{ $t('pt.signed', { n: hdhReward(s) }) }}</el-tag>
              <el-tag v-else size="small" type="info" effect="plain">{{ $t('pt.notSigned') }}</el-tag>
              <span v-if="lastSignShort(s)" class="c-last-sign" :title="$t('pt.lastSignTip', { d: lastSignDate(s) })">{{ lastSignShort(s) }}</span>
            </span>
          </div>
          <!-- 成长等级/账号类型（453，实测 me 接口 level.name/is_member）；旧快照无 level 显 '-' -->
          <div class="c-item"><span class="c-label">{{ $t('pt.growthLevel') }}</span><span class="c-val">{{ (d(s).hdh && d(s).hdh.level) || '-' }}</span></div>
          <div class="c-item"><span class="c-label">{{ $t('pt.accountType') }}</span><span class="c-val">{{ d(s).hdh && d(s).hdh.vip ? $t('pt.member') : $t('pt.regularUser') }}</span></div>
        </div>
        <!-- 蜂巢：Next.js 论坛 Cookie 会话（457/465），展示 用户状态(等级)/积分/签到；
             surface.checkedInToday 只读可判已签，「仅查询」模式也显真实状态 -->
        <div v-else-if="s.site_key === 'fengchao'" class="c-grid">
          <div class="c-item"><span class="c-label">{{ $t('pt.bonus') }}</span><span class="c-val c-strong">{{ fmtInt(d(s).bonus) }}</span></div>
          <div class="c-item">
            <span class="c-label">{{ $t('pt.today') }}</span>
            <span class="c-val">
              <el-tag v-if="signToday(s)" size="small" type="success" effect="plain">{{ $t('pt.signed', { n: fcReward(s) }) }}</el-tag>
              <el-tag v-else size="small" type="info" effect="plain">{{ $t('pt.notSigned') }}</el-tag>
              <span v-if="lastSignShort(s)" class="c-last-sign" :title="$t('pt.lastSignTip', { d: lastSignDate(s) })">{{ lastSignShort(s) }}</span>
            </span>
          </div>
          <div class="c-item"><span class="c-label">{{ $t('pt.userStatus') }}</span><span class="c-val">{{ d(s).level || '-' }}</span></div>
          <div class="c-item"><span class="c-label">{{ $t('pt.joined') }}</span><span class="c-val">{{ d(s).joined_at ? fmtDate(d(s).joined_at) : '-' }}</span></div>
        </div>
        <div v-else class="c-data">
          <span class="c-data-label">{{ $t('pt.data') }}</span>
          <span class="c-data-val" :title="$t('pt.upDownTip', { up: fmtB(d(s).uploaded), down: fmtB(d(s).downloaded) })">
            <span><span class="arr up">↑</span>{{ fmtB(d(s).uploaded) }}</span>
            <span><span class="arr down">↓</span>{{ fmtB(d(s).downloaded) }}</span>
          </span>
        </div>

        <div v-if="!canSigninRow(s)" class="c-grid">
          <div class="c-item"><span class="c-label">{{ $t('pt.ratio') }}</span><span class="c-val">{{ fmtRatio(d(s).ratio) }}</span></div>
          <div class="c-item"><span class="c-label">{{ $t('pt.seeding') }}</span><span class="c-val">{{ fmtSeeding(d(s)) }}</span></div>
          <div class="c-item"><span class="c-label">{{ $t('pt.joined') }}</span><span class="c-val">{{ d(s).joined_at ? fmtDate(d(s).joined_at) : '-' }}</span></div>
          <div class="c-item"><span class="c-label">{{ $t('pt.bonusPerHour') }}</span><span class="c-val">{{ d(s).bonus_ph == null ? '-' : fmtNum(d(s).bonus_ph) }}</span></div>
          <!-- 积分跨整行：千分位长数值（含 ⚡ 做种积分副值）在半行列宽下会截断 -->
          <div class="c-item c-wide">
            <span class="c-label">{{ $t('pt.bonus') }}</span>
            <span class="c-val"><span class="cur">$</span>{{ fmtNum(d(s).bonus) }}<span v-if="d(s).has_bonus2" class="bonus2" :title="$t('pt.seedingBonusTip')"> <span class="bolt">⚡</span>{{ fmtNum(d(s).bonus2) }}</span></span>
          </div>
        </div>

        <div v-if="s.last_error" class="c-err" :title="s.last_error">{{ s.last_error }}</div>
        <div v-else-if="!s.enabled" class="c-off">{{ $t('pt.disabledNote') }}</div>

        <div class="c-foot">
          <div class="c-moves">
            <button type="button" class="c-move" :disabled="s.id === sites[0].id" :title="$t('pt.moveUpTip')" @click="move(s, -1)"><el-icon><ArrowUp /></el-icon></button>
            <button type="button" class="c-move" :disabled="s.id === sites[sites.length - 1].id" :title="$t('pt.moveDownTip')" @click="move(s, 1)"><el-icon><ArrowDown /></el-icon></button>
          </div>
          <div class="c-acts">
            <el-button v-if="canSigninRow(s)" link type="primary" size="small" :loading="signingId === s.id" :title="$t('pt.signinTip')" @click="signinOne(s)">{{ $t('pt.signin') }}</el-button>
            <el-button link size="small" :loading="refreshingId === s.id" @click="refreshOne(s)">{{ $t('pt.refresh') }}</el-button>
            <el-button link size="small" @click="openEdit(s)">{{ $t('pt.edit') }}</el-button>
            <el-button link type="danger" size="small" @click="remove(s)">{{ $t('pt.del') }}</el-button>
          </div>
        </div>
      </div>
    </div>
    <el-empty v-else-if="!loading" :description="$t('pt.empty')" />

    <!-- 添加/编辑站点：凭据先测后存，浏览器 F12 复制 Cookie 粘贴即可 -->
    <el-dialog v-model="dlg" :title="form.id ? $t('pt.editSite') : $t('pt.addSite')" width="520px" :close-on-click-modal="false" :before-close="formGuard.beforeClose" append-to-body>
      <el-form label-width="92px">
        <el-form-item :label="$t('pt.site')">
          <el-select v-model="form.site_key" style="width: 100%" :disabled="!!form.id" filterable :placeholder="$t('pt.selectSitePh')">
            <el-option v-for="d in catalog" :key="d.key" :label="d.name" :value="d.key" :disabled="d.added && !form.id" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('pt.name')">
          <el-input v-model="form.name" :placeholder="defOf(form.site_key)?.name || $t('pt.namePh')" maxlength="40" />
        </el-form-item>
        <el-form-item :label="$t('pt.icon')">
          <el-input v-model="form.icon" :placeholder="$t('pt.iconPh')" />
        </el-form-item>
        <el-form-item :label="$t('pt.siteUrl')">
          <el-input v-model="form.base_url" :placeholder="defOf(form.site_key)?.url + $t('pt.defaultSuffix')" :title="$t('pt.baseUrlTip')" />
        </el-form-item>
        <el-form-item :label="$t('pt.openUrl')">
          <el-input v-model="form.open_url" :placeholder="defOf(form.site_key)?.url + $t('pt.defaultHomeSuffix')" :title="$t('pt.openUrlTip')" />
        </el-form-item>
        <el-form-item :label="$t('pt.proxy')">
          <div class="proxy-wrap">
            <el-select v-model="form.proxy_server_id" style="width: 100%">
              <el-option :label="$t('pt.direct')" :value="0" />
              <el-option v-for="sv in servers" :key="sv.id" :label="sv.name || sv.host" :value="sv.id" />
            </el-select>
            <div class="proxy-tip">{{ $t('pt.proxyTip') }}</div>
          </div>
        </el-form-item>
        <el-form-item v-if="canSignin" :label="$t('pt.autoSignin')">
          <div class="proxy-wrap">
            <template v-if="isDian">
              <el-select v-model="form.signin_mode" style="width: 100%">
                <el-option :label="$t('pt.signNormal')" value="normal" />
                <el-option :label="$t('pt.signLuckyDian')" value="lucky" />
                <el-option :label="$t('pt.signOff')" value="" />
              </el-select>
            </template>
            <!-- 蜂巢只有普通档（MP 集成 API 无玩法分支） -->
            <template v-else-if="isFc">
              <el-select v-model="form.signin_mode" style="width: 100%">
                <el-option :label="$t('pt.signNormal')" value="normal" />
                <el-option :label="$t('pt.signOff')" value="" />
              </el-select>
            </template>
            <template v-else>
              <el-select v-model="form.signin_mode" style="width: 100%">
                <el-option :label="$t('pt.signNormal')" value="normal" />
                <el-option :label="$t('pt.signLucky')" value="lucky" />
                <el-option :label="$t('pt.signOff')" value="" />
              </el-select>
            </template>
            <div class="proxy-tip">{{ $t('pt.autoSigninTip') }}</div>
          </div>
        </el-form-item>
        <el-form-item :label="authLabel">
          <el-input v-model="form.cookie" type="textarea" :rows="3" :placeholder="form.id ? $t('pt.keepCredPh') : authHint" />
        </el-form-item>
        <el-form-item label="User-Agent">
          <el-input v-model="form.ua" :placeholder="$t('pt.uaPh')" />
        </el-form-item>
        <el-form-item v-if="form.id" :label="$t('pt.enable')">
          <el-switch v-model="form.enabled" :active-text="$t('pt.enabledText')" />
        </el-form-item>
      </el-form>
      <!-- 部署 IP 与账号安全提醒（2026-10-05 用户指定：放添加/编辑弹窗最下面） -->
      <el-alert class="pt-notice" type="warning" :closable="false" show-icon
        :title="$t('pt.riskTitle')"
        :description="$t('pt.riskDesc')" />
      <template #footer>
        <el-button v-if="form.proxy_server_id === 0" :loading="testing" @click="test">{{ $t('pt.testConn') }}</el-button>
        <el-button @click="dlg = false">{{ $t('pt.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ $t('pt.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// PT 站点信息（391）：内置站点目录选站 + 浏览器 Cookie 导入，后台每天 06:00
// 抓一轮用户信息；卡片模块展示，点击站名新窗口打开站点（打开地址可配置，
// 默认站点首页，site_url 由后端算好下发）；可全部/单站点手动刷新。
defineOptions({ name: 'PtView' })
import { computed, onActivated, onDeactivated, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api/index.js'
import { fmtBytes, fmtDate, fmtTime } from '../utils/format.js'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { moveById } from '../utils/dragReorder.js'
import { useI18n } from 'vue-i18n'
import { lang, registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/pt.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

const sites = ref([])

// 补拉 setTimeout 句柄统一管理：keep-alive 保活下离开页面仍会到点触发，
// 收进数组、失活时统一清掉（连点多个刷新会叠加多条定时链，一并治理）
const pullTimers = []
function pullLater(...delays) {
  for (const d of delays) pullTimers.push(setTimeout(() => load(false), d))
}
function clearPullTimers() {
  for (const t of pullTimers) clearTimeout(t)
  pullTimers.length = 0
}
const catalog = ref([])
const loading = ref(false)
const loadedAt = ref('')
const refreshingAll = ref(false)
const refreshingId = ref(0)

const dlg = ref(false)
const saving = ref(false)
const testing = ref(false)
const signingId = ref(0)
const form = ref({ id: 0, site_key: '', name: '', base_url: '', open_url: '', proxy_server_id: 0, signin_mode: '', icon: '', cookie: '', ua: '', enabled: true })
const servers = ref([]) // 抓取出口候选（监控服务器清单）
const formGuard = useDirtyGuard(() => (dlg.value ? { ...form.value } : null))

function d(row) {
  return row.data || {}
}
function defOf(key) {
  return catalog.value.find((x) => x.key === key)
}
const isDian = computed(() => defOf(form.value.site_key)?.kind === 'dian115')
const isHdh = computed(() => defOf(form.value.site_key)?.kind === 'hdhaven')
const isFc = computed(() => defOf(form.value.site_key)?.kind === 'fengchao')
const canSignin = computed(() => isDian.value || isHdh.value || isFc.value)
const authLabel = computed(() => (defOf(form.value.site_key)?.auth === 'apikey' ? 'API Key' : 'Cookie'))
const authHint = computed(() => {
  if (isDian.value) return t('pt.authHintDian')
  if (isHdh.value) return t('pt.authHintHdh')
  if (isFc.value) return t('pt.authHintFc')
  return defOf(form.value.site_key)?.auth === 'apikey'
    ? t('pt.authHintApikey')
    : t('pt.authHintCookie')
})

// 卡片行级判断（模板用）：有签到语义的站点显示「签到」按钮、隐藏上传/下载行
function canSigninRow(s) {
  return s.site_key === 'dian115' || s.site_key === 'hdhaven' || s.site_key === 'fengchao'
}

function fmtInt(v) {
  if (v == null) return '-'
  return Math.round(v).toLocaleString('zh-CN')
}

// 栖影当日签到得分（453）：已签且站点给了 checkin_reward 才带「 +N」；今日判定走 460 signToday
function hdhReward(s) {
  const h = d(s).hdh
  return h && h.checkin_reward > 0 && signToday(s) ? ' +' + Math.round(h.checkin_reward) : ''
}

// 癫影当日签到得分（454）：面板签到那次记录的 award（负数=倒霉签显 -N），
// 空签/网页手动签（me 不回传）不显示；今日判定走 460 signToday（防隔夜快照）
function dianReward(s) {
  const di = d(s).dian
  if (!di || !di.award || !signToday(s)) return ''
  return (di.award > 0 ? '+' : '') + Math.round(di.award)
}

// 蜂巢当日签到得分（457）：recentCheckIns 当日 reward（网页手动签也能拿到），
// 已签且 >0 才带「+N」（无空格省宽，同癫影风格）；今日判定走 460 signToday
function fcReward(s) {
  const fc = d(s).fc
  return fc && fc.award > 0 && signToday(s) ? '+' + Math.round(fc.award) : ''
}

// 抓取接口（455）：卡片抓取时间后缀——0=面板直连，>0=中转出口服务器名
//（servers 清单只收 push agent；指向已删/已下线机器时回落业务文案）
function exitLabel(s) {
  if (!s.proxy_server_id) return t('pt.exitDirect')
  const sv = servers.value.find((x) => x.id === s.proxy_server_id)
  return sv ? (sv.name || sv.host) : t('pt.exitOffline')
}

// 今日已签判定（460）：signed_today 是快照抓取时刻的口径，快照隔夜未刷新
// 会把昨天的已签误显成今天——改按站点回传的最后签到成功日期==今天
//（癫影 last_signin / 栖影 last_checkin，站点东八区日期；字段缺失回落旧布尔）
function todayStr() {
  const n = new Date()
  return `${n.getFullYear()}-${String(n.getMonth() + 1).padStart(2, '0')}-${String(n.getDate()).padStart(2, '0')}`
}
function lastSignDate(s) {
  const di = d(s).dian, h = d(s).hdh, fc = d(s).fc
  return (di && di.last_signin) || (h && h.last_checkin) || (fc && fc.last_checkin) || ''
}
function signToday(s) {
  const last = lastSignDate(s)
  if (last) return last === todayStr()
  const x = d(s).dian || d(s).hdh || d(s).fc
  return !!(x && x.signed_today)
}
function lastSignShort(s) {
  const t = lastSignDate(s)
  return t.length >= 10 ? t.slice(5, 10) : ''
}

// 新增签到类站点默认普通签到（配置即二选一：普通/运气·赌狗；选「不自动签到」是显式降级）
watch(() => form.value.site_key, (k) => {
  if (dlg.value && !form.value.id) {
    form.value.signin_mode = k === 'dian115' || k === 'hdhaven' || k === 'fengchao' ? 'normal' : ''
  }
})

function iconSrc(row) {
  return row._customFail ? `/pt-icons/${row.site_key}.png` : (row.icon || `/pt-icons/${row.site_key}.png`)
}
// 两级回落：自定义 URL 失败 → 内置目录图标 → 首字块
function iconErr(row) {
  if (row.icon && !row._customFail) row._customFail = true
  else row._fallback = true
}

function fmtB(n) {
  return n ? fmtBytes(n) : '-'
}
function fmtRatio(v) {
  if (!v) return '-'
  if (v < 0) return '∞'
  return v.toFixed(2)
}
function fmtNum(v) {
  if (v == null) return '-'
  return v.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}
function fmtSeeding(x) {
  if (x.seeding == null || x.seeding < 0) return '-'
  return `${x.seeding} · ${x.seeding_sz ? fmtBytes(x.seeding_sz) : '0 B'}`
}
function relTime(ts) {
  const diff = Date.now() / 1000 - ts
  if (diff < 60) return t('pt.justNow')
  if (diff < 3600) return t('pt.minutesAgo', { n: Math.floor(diff / 60) })
  if (diff < 86400) return t('pt.hoursAgo', { n: Math.floor(diff / 3600) })
  return fmtDate(ts)
}

async function load(showLoading = true) {
  if (showLoading) loading.value = true
  try {
    const res = await api.get('/pt/sites')
    sites.value = res.sites || []
    loadedAt.value = new Date().toLocaleTimeString(lang.value === 'en' ? 'en-US' : 'zh-CN', { hour12: false })
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

async function loadCatalog() {
  if (catalog.value.length) return
  try {
    const res = await api.get('/pt/catalog')
    catalog.value = res.catalog || []
  } catch { /* 添加下拉兜底：空时禁用保存 */ }
}

async function refreshAll() {
  refreshingAll.value = true
  try {
    const res = await api.post('/pt/refresh') // 异步整轮，稍候拉列表看逐站更新
    if (res.already) {
      ElMessage.info(t('pt.refreshAllBusy'))
    } else {
      ElMessage.success(t('pt.refreshAllStarted'))
    }
    pullLater(5000, 15000, 30000)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    // 整轮 26 站约 10~20 秒，按钮按住到前两次补拉后再放开，防连点叠加轮次
    setTimeout(() => (refreshingAll.value = false), 16000)
  }
}

async function refreshOne(s) {
  refreshingId.value = s.id
  try {
    const res = await api.post(`/pt/sites/${s.id}/refresh`)
    if (res.queued) {
      ElMessage.info(t('pt.refreshQueued', { name: s.name }))
      pullLater(15000, 40000)
    } else {
      const i = sites.value.findIndex((x) => x.id === s.id)
      if (i >= 0 && res.site) sites.value[i] = res.site
      ElMessage.success(t('pt.refreshed', { name: s.name }))
    }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    refreshingId.value = 0
  }
}

// 手动签到（癫影类站）：直连同步返回结果文案，中转出口返回 queued
async function signinOne(s) {
  signingId.value = s.id
  try {
    const res = await api.post(`/pt/sites/${s.id}/signin`)
    if (res.queued) {
      ElMessage.info(t('pt.signinQueued', { name: s.name }))
      pullLater(15000, 40000)
      return
    }
    const i = sites.value.findIndex((x) => x.id === s.id)
    if (i >= 0 && res.site) sites.value[i] = res.site
    const msg = res.msg || t('pt.done')
    if (msg.includes('已签到') || msg.includes('空签') || msg.includes('倒霉')) {
      ElMessage.info(msg)
    } else {
      ElMessage.success(msg)
    }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    signingId.value = 0
  }
}

// ---- 卡片拖拽排序（PC 原生 HTML5 DnD，口径同主页链接组/磁贴；手机端仍用排序钮）----
const drag = reactive({ id: null, over: null })
function dragHasType(e, type) {
  const t = e.dataTransfer?.types
  if (!t) return false
  return t.includes ? t.includes(type) : t.contains(type) // 老 Safari types 是 DOMStringList
}
function dragStart(s, e) {
  drag.id = s.id
  e.dataTransfer?.setData('application/x-pt-site', String(s.id))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function dragEnd() {
  drag.id = null
  drag.over = null
}
function dragOver(overId, e) {
  if (drag.id == null || drag.id === overId) return
  if (!dragHasType(e, 'application/x-pt-site')) return
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  drag.over = overId
}
async function dragDrop(targetId) {
  const moved = drag.id
  drag.id = null
  drag.over = null
  if (moved == null || moved === targetId) return
  // moveById 语义：向下拖（from<to）落到目标之后、向上拖落到目标之前
  const r = moveById(sites.value, moved, targetId)
  if (!r) return
  sites.value = r.arr
  try {
    await api.put('/pt/sites/order', { ids: sites.value.map((x) => x.id) })
  } catch (e) {
    ElMessage.error(e.message)
    load(false)
  }
}

async function move(s, dir) {
  const ids = sites.value.map((x) => x.id)
  const i = sites.value.findIndex((x) => x.id === s.id)
  const j = i + dir
  if (j < 0 || j >= ids.length) return
  ;[ids[i], ids[j]] = [ids[j], ids[i]]
  sites.value.sort((a, b) => ids.indexOf(a.id) - ids.indexOf(b.id))
  try {
    await api.put('/pt/sites/order', { ids })
  } catch (e) {
    ElMessage.error(e.message)
    load(false)
  }
}

function resetForm() {
  form.value = { id: 0, site_key: '', name: '', base_url: '', open_url: '', proxy_server_id: 0, signin_mode: '', icon: '', cookie: '', ua: '', enabled: true }
}
function openAdd() {
  resetForm()
  loadCatalog()
  dlg.value = true
  formGuard.snapshot()
}
function openEdit(s) {
  loadCatalog()
  form.value = { id: s.id, site_key: s.site_key, name: s.name, base_url: s.base_url || '', open_url: s.open_url || '', proxy_server_id: s.proxy_server_id || 0, signin_mode: s.signin_mode || '', icon: s.icon || '', cookie: '', ua: s.ua || '', enabled: !!s.enabled }
  dlg.value = true
  formGuard.snapshot()
}

function formBody() {
  const f = form.value
  return { site_key: f.site_key, name: f.name.trim(), base_url: f.base_url.trim(), open_url: f.open_url.trim(), proxy_server_id: f.proxy_server_id || 0, signin_mode: f.signin_mode || '', icon: f.icon.trim(), cookie: f.cookie.trim(), ua: f.ua.trim(), enabled: f.enabled }
}

async function test() {
  const b = formBody()
  if (!b.site_key) {
    ElMessage.warning(t('pt.needSite'))
    return
  }
  if (!b.cookie) {
    ElMessage.warning(form.value.id ? t('pt.needNewCred') : t('pt.needCred'))
    return
  }
  testing.value = true
  try {
    await api.post('/pt/test', b)
    ElMessage.success(t('pt.connOk'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testing.value = false
  }
}

async function save() {
  const b = formBody()
  if (!b.site_key) {
    ElMessage.warning(t('pt.needSite'))
    return
  }
  if (!form.value.id && !b.cookie) {
    ElMessage.warning(t('pt.needCredAny'))
    return
  }
  saving.value = true
  try {
    const res = form.value.id ? await api.put(`/pt/sites/${form.value.id}`, b) : await api.post('/pt/sites', b)
    dlg.value = false
    if (res.queued) {
      ElMessage.success(form.value.id ? t('pt.savedQueued') : t('pt.addedQueued'))
      pullLater(15000, 40000)
    } else {
      ElMessage.success(form.value.id ? t('pt.saved') : t('pt.added'))
    }
    load(false)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function remove(s) {
  try {
    await ElMessageBox.confirm(t('pt.delConfirm', { name: s.name }), t('pt.del'), {
      type: 'warning', confirmButtonText: t('pt.del'), cancelButtonText: t('pt.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/pt/sites/${s.id}`)
    ElMessage.success(t('pt.deleted'))
  } catch (e) {
    ElMessage.error(e.message)
  }
  load(false)
}

async function loadServers() {
  if (servers.value.length) return
  try {
    const res = await api.get('/servers')
    // 中转任务经 agent 上报响应捎带下发——只有推送模式（push）agent 能领取；
    // 拉取模式（agent）无上报通道、ssh/node 无 agent，选了任务只会租约超时
    servers.value = (res.servers || []).filter((x) => x && x.collect_mode === 'push')
  } catch { /* 出口下拉兜底：拉不到时只有直连 */ }
}

onMounted(() => { load(); loadCatalog(); loadServers() })
// 首挂载 onActivated 也会触发：标记跳过，避免 /pt/sites 双请求
let ptMountedJustNow = true
onActivated(() => {
  if (ptMountedJustNow) {
    ptMountedJustNow = false
    return
  }
  load()
})
onDeactivated(clearPullTimers)
onUnmounted(clearPullTimers)
</script>

<style scoped>
.ptview {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.pt-notice {
  margin-top: 6px;
  flex: none;
}
.pt-notice :deep(.el-alert__title) {
  font-weight: 700;
}
.pt-notice :deep(.el-alert__description) {
  font-size: 12px;
  line-height: 1.7;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}
.tools {
  display: flex;
  align-items: center;
  gap: 12px;
}
.updated {
  color: var(--muted);
  font-size: 12px;
}

/* 卡片模块网格：固定 3 列（444 用户指定一行 3 个），手机端落单列 */
.site-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
}
@media (max-width: 640px) {
  .site-grid {
    grid-template-columns: 1fr;
  }
}
/* 431 整体缩小 20%：列宽 300→240，卡内 padding/图标/字号/行距同比收一档 */
.site-card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 12px 8px;
  border-radius: 10px;
  background: var(--surface, var(--el-bg-color));
  border: 1px solid var(--border, var(--el-border-color-lighter));
  min-width: 0;
}
.site-card.off {
  opacity: 0.6;
}
.site-card[draggable="true"] {
  cursor: grab;
}
.site-card.over {
  outline: 2px dashed var(--el-color-primary, #409eff);
  outline-offset: 2px;
}

/* 头行：图标 + 站名（点击新窗口打开）+ 等级 */
.c-head {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.c-ico {
  width: 21px;
  height: 21px;
  object-fit: contain;
  flex: none;
}
.c-ico-fallback {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 5px;
  background: var(--el-fill-color, #f5f7fa);
  color: var(--muted, #909399);
  font-size: 12px;
  font-weight: 700;
}
.c-name {
  font-size: 13px;
  font-weight: 700;
  color: var(--el-text-color-primary);
  text-decoration: none;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.c-name:hover {
  color: var(--el-color-primary);
  text-decoration: underline;
  text-underline-offset: 3px;
}
.c-level-chip {
  margin-left: auto;
  flex: none;
  max-width: 130px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
  padding: 3px 8px;
  border-radius: 999px;
  background: var(--el-fill-color, rgba(144, 147, 153, 0.16));
  color: var(--el-text-color-primary);
  border: 1px solid var(--el-border-color, rgba(144, 147, 153, 0.45));
  cursor: default;
}
.c-level-dim {
  color: var(--muted, #909399);
  font-weight: 500;
}

/* 用户名 + 更新时间 */
.c-user {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  font-size: 11px;
  color: var(--muted);
  min-width: 0;
}
.c-username {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 数据量：上传绿↑ / 下载红↓ 上下两行（对齐参考信息结构的主视觉） */
.c-data {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin: 1px 0;
}
/* 「数据」标签比下方两列小项标签（12px 灰弱）显眼：14px 加粗主文字色 */
.c-data-label {
  flex: none;
  width: 38px; /* 与小项标签左缘对齐 */
  font-size: 12.5px;
  font-weight: 700;
  color: var(--el-text-color-primary);
}
.c-data-val {
  display: inline-flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
  font-size: 12.5px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: hidden;
}
.c-data-val .arr {
  font-size: 11px;
  margin-right: 2px;
}
.arr.up {
  color: var(--el-color-success, #67c23a);
}
.arr.down {
  color: var(--el-color-danger, #f56c6c);
}

/* 抓取出口 */
.proxy-wrap {
  width: 100%;
}
.proxy-tip {
  font-size: 11px;
  line-height: 1.6;
  color: var(--warn-text, #e6a23c);
  margin-top: 4px;
}

/* 信息网格：两列小项 */
.c-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 3px 10px;
}
.c-item {
  display: flex;
  align-items: baseline;
  gap: 4px; /* 6→4（465 蜂巢卡「今日」格 tag+日期放不下，用户指定缩间隔不换行） */
  min-width: 0;
  font-size: 12px;
  line-height: 1.55;
}
.c-label {
  flex: none;
  width: 44px; /* 40→44（447）：四字标签「连续签到」放得下，配 nowrap 不折行 */
  white-space: nowrap;
  color: var(--muted, #909399);
  font-size: 11px;
}
.c-val {
  min-width: 0;
  font-variant-numeric: tabular-nums;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.c-item.c-wide {
  grid-column: 1 / -1;
}
.cur {
  color: var(--el-color-success, #67c23a);
  font-size: 11px;
  margin-right: 1px;
}
/* 癫影卡：积分主数值加粗 */
.c-strong {
  font-size: 13.5px;
  font-weight: 700;
}
.vip-tag {
  margin-left: 6px;
}
/* 签到格日期小字（460/465）：今日 tag 右侧，悬浮看完整日期；tag 与日期间距
   收紧防半列宽下被 .c-val 的 ellipsis 截掉（蜂巢卡首报「10…」，用户指定
   不换行缩间隔） */
.c-last-sign {
  margin-left: 3px;
  font-size: 10.5px;
  color: var(--muted);
  white-space: nowrap;
}
.bonus2 {
  color: var(--muted, #909399);
  font-size: 12px;
  white-space: nowrap;
}
.bolt {
  font-size: 10px;
}

/* 错误/停用 */
.c-err {
  color: var(--crit, #f56c6c);
  font-size: 11px;
  line-height: 1.5;
  word-break: break-all;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.c-off {
  color: var(--muted, #909399);
  font-size: 11px;
}

/* 脚行：排序小钮 + 文本操作 */
.c-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px dashed var(--border, var(--el-border-color-lighter));
  margin-top: auto;
  padding-top: 5px;
}
.c-moves {
  display: flex;
  gap: 2px;
}
.c-move {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--muted, #909399);
  cursor: pointer;
}
.c-move:hover:not(:disabled) {
  background: var(--el-fill-color, #f5f7fa);
  color: var(--el-color-primary, #409eff);
}
.c-move:disabled {
  opacity: 0.3;
  cursor: default;
}
.c-acts {
  display: flex;
  align-items: center;
}
</style>
