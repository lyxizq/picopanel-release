<template>
  <div class="certs">
    <div class="toolbar">
      <span v-if="loadedAt" class="updated">{{ $t('certs.updatedAt', { t: loadedAt }) }}</span>
      <span v-else class="updated">{{ $t('certs.idleDesc') }}</span>
      <div class="tools">
        <el-button :loading="checking" @click="checkAll">
          <el-icon v-if="!checking"><Refresh /></el-icon>
          <span v-if="!checking">{{ $t('certs.checkNow') }}</span>
        </el-button>
        <el-button type="primary" @click="add">
          <el-icon><Plus /></el-icon>
          <span>{{ $t('certs.addDomain') }}</span>
        </el-button>
      </div>
    </div>

    <div v-if="sorted.length" class="cert-grid">
      <el-card v-for="c in sorted" :key="c.id" shadow="never" class="cert-card" :class="`st-${c.state}`" @click="openDetail(c)">
        <div class="c-head">
          <div class="c-domain" :title="c.subject && c.subject !== c.domain ? $t('certs.covers', { domain: c.domain, subject: c.subject }) : c.domain">
            {{ c.domain }}<span v-if="c.port !== 443" class="c-port">:{{ c.port }}</span>
          </div>
          <el-tooltip v-if="c.state === 'error'" :content="c.last_error" placement="top">
            <el-tag size="small" type="danger" effect="plain">{{ $t('certs.stError') }}</el-tag>
          </el-tooltip>
          <el-tag v-else-if="c.state === 'expired'" size="small" type="danger">{{ $t('certs.stExpired') }}</el-tag>
          <el-tag v-else-if="c.state === 'expiring'" size="small" type="warning">{{ $t('certs.stExpiring') }}</el-tag>
          <el-tag v-else-if="c.state === 'unchecked'" size="small" type="info" effect="plain">{{ $t('certs.stUnchecked') }}</el-tag>
          <el-tag v-else size="small" type="success" effect="plain">{{ $t('certs.stOk') }}</el-tag>
        </div>

        <template v-if="c.state !== 'error'">
          <div class="c-row"><span class="c-label">{{ $t('certs.lbIssuedTo') }}</span><span class="c-val">{{ c.issued_to || '-' }}</span></div>
          <div class="c-row"><span class="c-label">{{ $t('certs.lbIssuer') }}</span><span class="c-val">{{ c.issuer || '-' }}</span></div>
          <div class="c-row"><span class="c-label">{{ $t('certs.lbIssuedAt') }}</span><span class="c-val">{{ c.not_before ? fmtTime(c.not_before) : '-' }}</span></div>
          <div class="c-row"><span class="c-label">{{ $t('certs.lbExpiresAt') }}</span><span class="c-val">{{ c.not_after ? fmtTime(c.not_after) : '-' }}</span></div>
        </template>
        <div v-else class="c-err">{{ c.last_error }}</div>

        <template v-if="c.state === 'ok' || c.state === 'expiring' || c.state === 'expired'">
          <div class="c-row">
            <span class="c-label">{{ $t('certs.lbLeft') }}</span>
            <span class="c-val c-days" :class="{ soon: c.state === 'expiring', gone: c.state === 'expired' }">
              {{ c.days_left < 0 ? $t('certs.daysAgo', { n: -c.days_left }) : $t('certs.daysLeft', { n: c.days_left }) }}
            </span>
          </div>
          <el-progress
            :percentage="lifePct(c)"
            :color="c.state === 'expired' ? '#f56c6c' : c.state === 'expiring' ? '#e6a23c' : '#67c23a'"
            :stroke-width="6"
            :show-text="false"
            class="c-bar"
          />
        </template>

        <div class="c-foot">
          <span class="c-checked">{{ c.last_check ? $t('certs.lastCheck', { t: fmtTime(c.last_check) }) : $t('certs.neverChecked') }}</span>
          <span class="c-ops">
            <el-button link type="danger" size="small" @click.stop="remove(c)">{{ $t('common.delete') }}</el-button>
          </span>
        </div>
      </el-card>
    </div>
    <el-empty v-else-if="!loading" :description="$t('certs.empty')" />

    <!-- 域名详情弹层：证书信息全量展示（续签功能已下线，2026-10-19） -->
    <el-dialog v-model="detailDlg" :title="detail ? detail.domain : ''" width="min(640px, 94vw)" append-to-body>
      <template v-if="detail">
        <div class="d-sec">{{ $t('certs.secCert') }}</div>
        <template v-if="detail.state !== 'error'">
          <div class="c-row"><span class="c-label">{{ $t('certs.stLabel') }}</span><span class="c-val">
            <el-tag size="small" :type="detail.state === 'ok' ? 'success' : detail.state === 'expiring' ? 'warning' : detail.state === 'expired' ? 'danger' : 'info'" effect="plain">
              {{ detail.state === 'ok' ? $t('certs.stOk') : detail.state === 'expiring' ? $t('certs.stExpiring') : detail.state === 'expired' ? $t('certs.stExpired') : $t('certs.stUnchecked') }}
            </el-tag>
          </span></div>
          <div class="c-row"><span class="c-label">{{ $t('certs.lbIssuedTo') }}</span><span class="c-val">{{ detail.issued_to || '-' }}</span></div>
          <div class="c-row"><span class="c-label">{{ $t('certs.lbIssuer') }}</span><span class="c-val">{{ detail.issuer || '-' }}<template v-if="detail.issuer_org && detail.issuer_org !== detail.issuer">{{ $t('certs.issuerOrgWrap', { org: detail.issuer_org }) }}</template></span></div>
          <div class="c-row"><span class="c-label">{{ $t('certs.lbSubject') }}</span><span class="c-val">{{ detail.subject || '-' }}</span></div>
          <div class="c-row"><span class="c-label">{{ $t('certs.lbIssuedAt') }}</span><span class="c-val">{{ detail.not_before ? fmtTime(detail.not_before) : '-' }}</span></div>
          <div class="c-row"><span class="c-label">{{ $t('certs.lbExpiresAt') }}</span><span class="c-val">{{ detail.not_after ? fmtTime(detail.not_after) : '-' }}</span></div>
          <div class="c-row" v-if="detail.days_left !== null && detail.days_left !== undefined">
            <span class="c-label">{{ $t('certs.lbLeft') }}</span>
            <span class="c-val c-days" :class="{ soon: detail.state === 'expiring', gone: detail.state === 'expired' }">
              {{ detail.days_left < 0 ? $t('certs.daysAgo', { n: -detail.days_left }) : $t('certs.daysLeft', { n: detail.days_left }) }}
            </span>
          </div>
        </template>
        <el-alert v-else type="error" :closable="false" :title="detail.last_error || $t('certs.stError')" />
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// HTTPS 证书到期监控：域名清单以模块卡片展示（状态着色 + 剩余有效期进度条，
// 有问题的排前面），每小时自动/手动立即检查（TLS 拨号取对端证书），到期前
// 15 天顶栏统计条出现提醒入口，微信提醒在系统设置页配置。点卡片弹纯证书
// 信息详情层。证书续签功能已整体下线（2026-10-19，TODO 25）。
defineOptions({ name: 'CertsView' })
import { computed, onActivated, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api/index.js'
import { fmtTime } from '../utils/format.js'
import { lang, registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/svcmon.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

const certs = ref([])
const loading = ref(false)
const checking = ref(false)
const loadedAt = ref('')

// 有问题的排前面：已过期 > 即将到期 > 检查失败 > 未检查 > 正常，同级按域名
const ST_ORDER = { expired: 0, expiring: 1, error: 2, unchecked: 3, ok: 4 }
const sorted = computed(() =>
  [...certs.value].sort((a, b) => (ST_ORDER[a.state] ?? 9) - (ST_ORDER[b.state] ?? 9) || a.domain.localeCompare(b.domain)),
)

// 剩余有效期占整个证书周期的比例（0~100），作卡片进度条
function lifePct(c) {
  if (!c.not_before || !c.not_after || c.not_after <= c.not_before) return 0
  const now = Date.now() / 1000
  return Math.max(0, Math.min(100, Math.round(((c.not_after - now) / (c.not_after - c.not_before)) * 100)))
}

async function load(showLoading = true) {
  if (showLoading) loading.value = true
  try {
    const res = await api.get('/certs')
    certs.value = res.certs || []
    loadedAt.value = new Date().toLocaleTimeString(lang.value === 'en' ? 'en-US' : 'zh-CN', { hour12: false })
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

async function checkAll() {
  checking.value = true
  try {
    await api.post('/certs/check')
    await load(false)
    ElMessage.success(t('certs.checkDone'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    checking.value = false
  }
}

// 添加域名：支持「example.com」或「example.com:8443」写法
async function add() {
  let r
  try {
    r = await ElMessageBox.prompt(t('certs.addPrompt'), t('certs.addTitle'), {
      closeOnClickModal: false,
      inputValue: '',
      confirmButtonText: t('certs.addBtn'),
      cancelButtonText: t('common.cancel'),
      inputPlaceholder: 'example.com',
      inputValidator: (v) => (/^[a-zA-Z0-9.-]+(:\d{1,5})?$/.test(v.trim()) ? true : t('certs.invalidDomain')),
    })
  } catch {
    return
  }
  const raw = r.value.trim()
  let domain = raw
  let port = 0
  const i = raw.lastIndexOf(':')
  if (i > 0) {
    domain = raw.slice(0, i)
    port = parseInt(raw.slice(i + 1), 10)
  }
  try {
    await api.post('/certs', { domain, port })
    ElMessage.success(t('certs.added', { domain }))
    load(false)
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(t('certs.delConfirm', { domain: row.domain }), t('common.delete'), {
      type: 'warning',
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/certs/${row.id}`)
    ElMessage.success(t('common.deleted'))
    if (detail.value && detail.value.id === row.id) detailDlg.value = false
  } catch (e) {
    ElMessage.error(e.message)
  }
  load(false)
}

// ===== 域名详情弹层 =====
const detailDlg = ref(false)
const detail = ref(null)

function openDetail(c) {
  detail.value = c
  detailDlg.value = true
}

onMounted(() => load())
onActivated(() => load())
</script>

<style scoped>
.certs {
  display: flex;
  flex-direction: column;
  gap: 12px;
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

/* 证书模块卡片：自适应网格，手机端自然落成单列 */
.cert-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 12px;
}
.cert-card {
  border-radius: 10px;
}
/* 状态着色：左侧竖色条（与总览分组卡片同语言） */
.cert-card :deep(.el-card__body) {
  padding: 14px 16px 10px;
}
.c-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}
.c-domain {
  font-size: 15px;
  font-weight: 700;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.c-port {
  color: var(--muted);
  font-size: 12px;
  font-weight: 400;
}
.c-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  font-size: 13px;
  line-height: 1.9;
}
.c-label {
  flex: none;
  width: 70px;
  color: var(--muted);
  font-size: 12px;
}
.c-val {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.c-days {
  font-weight: 600;
}
.c-days.soon {
  color: var(--warn-text, #e6a23c);
}
.c-days.gone {
  color: var(--crit, #f56c6c);
}
.c-err {
  color: var(--crit, #f56c6c);
  font-size: 12px;
  line-height: 1.6;
  margin-bottom: 6px;
  word-break: break-all;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.c-bar {
  margin: 6px 0 2px;
}
.c-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px dashed var(--border, #ebeef5);
  margin-top: 8px;
  padding-top: 6px;
}
.c-checked {
  color: var(--muted);
  font-size: 12px;
}
.c-ops {
  display: flex;
  align-items: center;
  gap: 10px;
}
/* 卡片可点击（点开详情弹层） */
.cert-card {
  cursor: pointer;
  transition: transform 0.15s, box-shadow 0.15s;
}
.cert-card:hover {
  transform: translateY(-1px);
  box-shadow: var(--el-box-shadow-light, 0 4px 12px rgba(0, 0, 0, 0.08));
}
/* 详情弹层：小节标题 */
.d-sec {
  font-size: 13px;
  font-weight: 700;
  margin: 14px 0 4px;
  padding-left: 8px;
  border-left: 3px solid var(--el-color-primary, #409eff);
}
.d-sec:first-of-type {
  margin-top: 0;
}
</style>
