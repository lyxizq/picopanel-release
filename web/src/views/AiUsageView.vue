<template>
  <div class="aiusage">
    <div class="toolbar">
      <span class="updated">{{ $t('aiusage.pollNote') }}{{ loadedAt ? $t('aiusage.updatedAt', { t: loadedAt }) : $t('aiusage.showLatest') }}</span>
      <div class="tools">
        <el-button :loading="refreshingAll" @click="refreshAll">
          <el-icon v-if="!refreshingAll"><Refresh /></el-icon>
          <span v-if="!refreshingAll">{{ $t('aiusage.refreshAll') }}</span>
        </el-button>
        <el-button type="primary" @click="openAdd">
          <el-icon><Plus /></el-icon>
          <span>{{ $t('aiusage.add') }}</span>
        </el-button>
      </div>
    </div>

    <div v-if="accounts.length" class="acc-grid">
      <el-card v-for="a in accounts" :key="a.id" shadow="never" :class="['acc-card', { clickable: ['quota', 'edgeone', 'qweather'].includes(a.usage?.kind) }]" @click="cardClick(a)">
        <div class="a-head">
          <!-- 智谱/EdgeOne/和风：标题点击与卡片点击殊途同归（409/411/3，cardClick 按 kind 分流） -->
          <div
            :class="['a-name', { 'a-name-link': ['quota', 'edgeone', 'qweather'].includes(a.usage?.kind) }]"
            :title="a.usage?.kind === 'quota' ? $t('aiusage.clickReport') : ['edgeone', 'qweather'].includes(a.usage?.kind) ? $t('aiusage.clickDetail') : a.name"
            @click.stop="cardClick(a)"
          >{{ a.name }}</div>
          <div class="a-tags">
            <el-tag v-if="a.usage?.level" size="small" effect="plain" class="a-level">{{ a.usage.level }}</el-tag>
            <el-tag size="small" :type="a.provider === 'zhipu' ? 'warning' : 'info'" effect="plain">{{ providerLabel(a.provider) }}</el-tag>
          </div>
        </div>

        <!-- 智谱：百分比窗口进度条 + 剩余百分比 + 额度上限（上游给绝对计数时） + MCP 月配额；点卡片或标题看完整报告（401/409） -->
        <template v-if="a.usage?.kind === 'quota'">
          <div class="eo-sub">{{ $t('aiusage.cardFullHint') }}</div>
          <div v-for="w in a.usage.windows" :key="w.key" class="a-win">
            <div class="a-win-row">
              <span class="a-win-label">{{ w.label }}</span>
              <span class="a-win-pct" :style="{ color: pctColor(w.used_pct) }">{{ $t('aiusage.winPct', { used: Math.round(w.used_pct), left: Math.round(100 - w.used_pct) }) }}</span>
            </div>
            <el-progress :percentage="clampPct(w.used_pct)" :color="barColor(w.used_pct)" :stroke-width="6" :show-text="false" />
            <div v-if="w.total" class="a-counts">{{ $t('aiusage.quotaCap', { v: fmtTokens(w.total) }) }}<template v-if="w.credit"> {{ $t('aiusage.credits') }}</template> · {{ $t('aiusage.usedShort', { v: fmtTokens(w.used) }) }} · {{ $t('aiusage.leftShort', { v: fmtTokens(w.remaining) }) }}</div>
            <div v-if="w.reset_at" class="a-reset">{{ $t('aiusage.resetAt', { t: fmtReset(w.reset_at) }) }}</div>
          </div>
          <div v-if="a.usage.tools" class="a-row">
            <span class="a-label">{{ $t('aiusage.mcpQuota') }}</span>
            <span class="a-val">{{ $t('aiusage.toolsQuota', { cur: a.usage.tools.current, usage: a.usage.tools.usage, left: a.usage.tools.remaining }) }}</span>
          </div>
        </template>

        <!-- DeepSeek：余额构成（官方无用量统计 API，余额即全部口径） -->
        <template v-else-if="a.usage?.kind === 'balance'">
          <div v-for="(b, i) in a.usage.balances" :key="i" class="a-bal">
            <div class="a-row">
              <span class="a-label">{{ $t('aiusage.totalBalance') }}</span>
              <span class="a-val a-total">{{ b.total }} {{ b.currency }}
                <el-tag size="small" :type="b.available ? 'success' : 'danger'" effect="plain">{{ b.available ? $t('aiusage.availOk') : $t('aiusage.availNo') }}</el-tag>
              </span>
            </div>
            <div class="a-row"><span class="a-label">{{ $t('aiusage.toppedUp') }}</span><span class="a-val">{{ b.topped_up }} {{ b.currency }}</span></div>
            <div class="a-row"><span class="a-label">{{ $t('aiusage.granted') }}</span><span class="a-val">{{ b.granted }} {{ b.currency }}</span></div>
          </div>
        </template>

        <!-- EdgeOne：站点近 24 小时四指标 + 环比（393，对齐控制台「站点数据概览」；点击卡片开详情弹层 398） -->
        <template v-else-if="a.usage?.kind === 'edgeone'">
          <div class="eo-sub">{{ $t('aiusage.eoCardSub', { zones: a.usage.edgeone.zone_count ? $t('aiusage.zonesDot', { n: a.usage.edgeone.zone_count }) : '' }) }}</div>
          <div v-for="s in edgeOneRows(a.usage.edgeone)" :key="s.label" class="a-row">
            <span class="a-label">{{ s.label }}</span>
            <span class="a-val a-total">
              <template v-if="s.value == null">—</template>
              <template v-else>{{ s.value }}</template>
              <span v-if="s.delta != null" :class="['eo-delta', { down: s.delta < 0 }]">{{ $t('aiusage.dod') }} {{ s.delta < 0 ? '↓' : '↑' }} {{ Math.abs(s.delta).toFixed(2) }}%</span>
            </span>
          </div>
        </template>
        <!-- 和风天气（3）：控制台 API 财务汇总+请求量统计；凭据随前台页面天气配置自动接入，卡片只读不可编辑删除 -->
        <template v-else-if="a.usage?.kind === 'qweather'">
          <div class="eo-sub">{{ $t('aiusage.qwCardSub') }}</div>
          <div class="qw-grid">
            <div>
              <div class="a-row"><span class="a-label">{{ $t('aiusage.availQuota') }}</span><span class="a-val a-total" :class="{ 'qw-neg': (a.usage.wx?.finance?.balance || 0) < 0 }">{{ wxMoney(a.usage.wx?.finance?.balance, a.usage.wx?.finance?.currency) ?? '—' }}</span></div>
              <div class="a-row"><span class="a-label">{{ $t('aiusage.thisMonth') }}</span><span class="a-val">{{ wxMoney(a.usage.wx?.finance?.this_month, a.usage.wx?.finance?.currency) ?? '—' }}</span></div>
              <div class="a-row"><span class="a-label">{{ $t('aiusage.req24h') }}</span><span class="a-val">{{ fmtTimes(a.usage.wx?.stats?.success) ?? '—' }}<template v-if="a.usage.wx?.stats?.errors"> · {{ $t('aiusage.failed') }} <span class="qw-err">{{ fmtTimes(a.usage.wx.stats.errors) }}</span></template></span></div>
            </div>
            <div>
              <div v-if="a.usage.wx?.day_avg_success != null" class="a-row"><span class="a-label">{{ $t('aiusage.dailyAvg') }}</span><span class="a-val">{{ fmtTimes(a.usage.wx.day_avg_success) }}<template v-if="a.usage.wx.day_avg_errors"> · {{ $t('aiusage.failed') }} <span class="qw-err">{{ fmtTimes(a.usage.wx.day_avg_errors) }}</span></template></span></div>
              <div v-if="a.usage.wx?.month_success != null" class="a-row"><span class="a-label">{{ $t('aiusage.monthlyTotal') }}</span><span class="a-val">{{ wxMonthTotal(a.usage.wx) }}<template v-if="a.usage.wx.month_errors"> · {{ $t('aiusage.failed') }} <span class="qw-err">{{ fmtTimes(a.usage.wx.month_errors) }}</span></template></span></div>
              <div v-for="q in wxQuotas(a.usage.wx)" :key="q.key" class="qw-quota">
                <div class="qw-quota-head"><span class="qw-quota-label">{{ q.label }}</span><span class="qw-quota-pct">{{ q.pct.toFixed(1) }}%</span></div>
                <div class="eo-bar"><div class="eo-bar-fill" :style="{ width: q.width + '%', background: barColor(q.pct) }" /></div>
              </div>
            </div>
          </div>
          <div v-if="wxTop(a.usage.wx?.stats).length" class="qw-top">
            <div v-for="t in wxTop(a.usage.wx?.stats)" :key="t.api" class="eo-rank-line">
              <div class="eo-rank-row"><span class="eo-rank-name">{{ wxAPIName(t.api) }}</span><span class="eo-rank-val">{{ fmtTimes(t.success + t.errors) }}</span></div>
              <div class="eo-bar"><div class="eo-bar-fill" :style="{ width: t.pct + '%' }" /></div>
            </div>
          </div>
        </template>
        <div v-else class="a-empty">{{ $t('aiusage.notFetched') }}</div>

        <div v-if="a.last_error" class="a-err">{{ a.last_error }}</div>

        <div class="a-foot" @click.stop>
          <span class="a-checked">{{ a.usage_at ? $t('aiusage.usageUpdatedAt', { t: fmtTime(a.usage_at) }) : $t('aiusage.neverFetched') }}</span>
          <div class="a-acts">
            <el-button link size="small" :loading="refreshingId === a.id" @click="refreshOne(a)">{{ $t('aiusage.refresh') }}</el-button>
            <el-button v-if="a.provider !== 'qweather'" link size="small" @click="openEdit(a)">{{ $t('aiusage.edit') }}</el-button>
            <el-button v-if="a.provider !== 'qweather'" link type="danger" size="small" @click="remove(a)">{{ $t('aiusage.del') }}</el-button>
          </div>
        </div>
      </el-card>
    </div>
    <el-empty v-else-if="!loading" :description="$t('aiusage.empty')" />

    <!-- 添加/编辑账户 -->
    <el-dialog v-model="dlg" :title="form.id ? $t('aiusage.editAcc') : $t('aiusage.addAcc')" width="460px" :close-on-click-modal="false" :before-close="formGuard.beforeClose" append-to-body>
      <el-form label-width="90px">
        <el-form-item :label="$t('aiusage.provider')">
          <el-select v-model="form.provider" style="width: 100%">
            <el-option v-for="p in presets" :key="p.key" :label="p.label" :value="p.key" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('aiusage.name')">
          <el-input v-model="form.name" :placeholder="$t('aiusage.namePh')" maxlength="40" />
        </el-form-item>
        <el-form-item :label="$t('aiusage.urlLabel')">
          <el-input v-model="form.base_url" :placeholder="presetBase(form.provider) + $t('aiusage.defaultSuffix')" />
          <div v-if="form.provider === 'edgeone'" class="eo-hint">{{ $t('aiusage.eoHint') }}</div>
        </el-form-item>
        <template v-if="form.provider === 'edgeone'">
          <el-form-item label="SecretId">
            <el-input v-model="form.secret_id" :placeholder="form.id ? $t('aiusage.keepVal') : $t('aiusage.secretIdPh')" />
          </el-form-item>
          <el-form-item label="SecretKey">
            <el-input v-model="form.secret_key" type="password" show-password :placeholder="form.id ? $t('aiusage.keepVal') : $t('aiusage.secretKeyPh')" />
          </el-form-item>
          <el-form-item :label="$t('aiusage.siteId')">
            <el-input v-model="form.zone_ids" :placeholder="$t('aiusage.zonePh')" />
          </el-form-item>
        </template>
        <el-form-item v-else label="API Key">
          <el-input v-model="form.api_key" type="password" show-password :placeholder="form.id ? $t('aiusage.keepKey') : $t('aiusage.apiKeyPh')" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dlg = false">{{ $t('aiusage.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">{{ $t('aiusage.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- EdgeOne 详情弹层（398/410）：数据为最近一次轮询/刷新的快照，随卡片读库直出 -->
    <el-dialog v-model="detailOpen" class="eo-detail-dlg" top="6vh" :title="detailAcc ? $t('aiusage.eoDetailTitle', { name: detailAcc.name }) : $t('aiusage.eoDetail')" width="min(880px, 96vw)" append-to-body>
      <template v-if="detailAcc?.usage?.edgeone">
        <div class="eo-sub">{{ $t('aiusage.eoSub24h') }}<template v-if="detailAcc.usage.edgeone.zone_count">{{ $t('aiusage.zonesDot', { n: detailAcc.usage.edgeone.zone_count }) }}</template> · {{ $t('aiusage.updatedAt', { t: detailAcc.usage_at ? fmtTime(detailAcc.usage_at) : '--' }) }}</div>

        <!-- 主指标 / 缓存与回源 并排（414） -->
        <div class="eo-cols">
          <div>
            <div class="eo-sec">{{ $t('aiusage.mainMetrics') }}</div>
            <div v-for="s in edgeOneRows(detailAcc.usage.edgeone)" :key="'m' + s.label" class="a-row">
              <span class="a-label">{{ s.label }}</span>
              <span class="a-val a-total"><template v-if="s.value == null">—</template><template v-else>{{ s.value }}</template></span>
            </div>
          </div>
          <div>
            <div class="eo-sec">{{ $t('aiusage.cacheOrigin') }}</div>
            <div class="a-row"><span class="a-label">{{ $t('aiusage.respFlux') }}</span><span class="a-val">{{ fmtBytes(detailAcc.usage.edgeone.detail?.out_flux) ?? '—' }}</span></div>
            <div class="a-row"><span class="a-label">{{ $t('aiusage.cacheHit') }}</span><span class="a-val">{{ fmtBytes(detailAcc.usage.edgeone.detail?.cache_hit_flux) ?? '—' }}<template v-if="hitRatePct != null">{{ $t('aiusage.hitRate', { p: hitRatePct }) }}</template></span></div>
            <div class="a-row"><span class="a-label">{{ $t('aiusage.originReq') }}</span><span class="a-val">{{ fmtTimes(detailAcc.usage.edgeone.detail?.origin_req) ?? '—' }}</span></div>
            <div class="a-row"><span class="a-label">{{ $t('aiusage.originFlux') }}</span><span class="a-val">{{ fmtBytes(detailAcc.usage.edgeone.detail?.origin_flux) ?? '—' }}</span></div>
          </div>
        </div>

        <div class="eo-sec">{{ $t('aiusage.statusDist') }}</div>
        <div v-if="detailAcc.usage.edgeone.detail?.status_codes?.length" class="eo-chips">
          <span v-for="s in detailAcc.usage.edgeone.detail.status_codes" :key="s.key" class="a-model" :title="$t('aiusage.statusTip', { k: s.key, n: s.count })">
            <span class="a-model-name">{{ s.key }}</span>
            <span class="a-model-tokens">{{ fmtTimes(s.count) }}</span>
          </span>
        </div>
        <div v-else class="a-empty">{{ $t('aiusage.noData') }}</div>

        <div class="eo-cols">
          <div>
            <div class="eo-sec">{{ $t('aiusage.topDomains') }}</div>
            <div v-if="detailAcc.usage.edgeone.detail?.top_domains?.length" class="eo-list">
              <div v-for="(t, i) in detailAcc.usage.edgeone.detail.top_domains" :key="i" class="eo-item">
                <span class="eo-item-name" :title="t.key">{{ t.key }}</span>
                <span class="eo-item-count">{{ fmtTimes(t.count) }}</span>
              </div>
            </div>
            <div v-else class="a-empty">{{ $t('aiusage.noData') }}</div>
          </div>
          <div>
            <div class="eo-sec">{{ $t('aiusage.topUrls') }}</div>
            <div v-if="detailAcc.usage.edgeone.detail?.top_urls?.length" class="eo-list">
              <div v-for="(t, i) in detailAcc.usage.edgeone.detail.top_urls" :key="i" class="eo-item">
                <span class="eo-item-name" :title="t.key">{{ t.key }}</span>
                <span class="eo-item-count">{{ fmtTimes(t.count) }}</span>
              </div>
            </div>
            <div v-else class="a-empty">{{ $t('aiusage.noData') }}</div>
          </div>
        </div>

        <!-- 响应流量排行（410，对齐控制台指标分析）：Host/客户端 IP/Referer/URL Path，条长=占总响应流量比 -->
        <div class="eo-sec">{{ $t('aiusage.fluxRank') }}</div>
        <div class="eo-rank-grid">
          <div v-for="rk in fluxRanks" :key="rk.label">
            <div class="eo-rank-title">{{ rk.label }}</div>
            <template v-if="rk.items?.length">
              <div v-for="t in rk.items" :key="t.key" class="eo-rank-line">
                <div class="eo-rank-row">
                  <span class="eo-rank-name" :title="t.key">{{ t.key }}</span>
                  <span class="eo-rank-val">{{ fmtBytes(t.count) }}<template v-if="rankPct(t.count) != null">{{ $t('aiusage.pctInParen', { p: rankPct(t.count).toFixed(2) }) }}</template></span>
                </div>
                <div class="eo-bar"><div class="eo-bar-fill" :style="{ width: barWidth(t.count) }" /></div>
              </div>
            </template>
            <div v-else class="a-empty">{{ $t('aiusage.noData') }}</div>
          </div>
        </div>

        <div class="eo-sec">{{ $t('aiusage.ddos') }}</div>
        <div class="a-row"><span class="a-label">{{ $t('aiusage.bandwidthPeak') }}</span><span class="a-val">{{ detailAcc.usage.edgeone.detail?.ddos_peak_bps != null ? fmtMbps(detailAcc.usage.edgeone.detail.ddos_peak_bps) : '—' }}</span></div>
        <div class="a-row"><span class="a-label">{{ $t('aiusage.ppsPeak') }}</span><span class="a-val">{{ detailAcc.usage.edgeone.detail?.ddos_peak_pps != null ? fmtPps(detailAcc.usage.edgeone.detail.ddos_peak_pps) : '—' }}</span></div>
      </template>
    </el-dialog>

    <!-- 和风天气详情弹层（3）：数据为最近一次轮询/刷新的快照直出；缺失的块多为凭据未勾控制台权限 -->
    <el-dialog v-model="qwOpen" class="qw-detail-dlg" top="6vh" :title="qwAcc ? $t('aiusage.qwTitle', { name: qwAcc.name }) : $t('aiusage.qwTitleShort')" width="min(880px, 96vw)" append-to-body>
      <template v-if="qwAcc?.usage?.kind === 'qweather'">
        <div class="eo-sub">
          {{ $t('aiusage.qwDelay') }} · {{ $t('aiusage.updatedAt', { t: qwAcc.usage_at ? fmtTime(qwAcc.usage_at) : '--' }) }}<span v-if="!qwAcc.usage.wx?.finance || !qwAcc.usage.wx?.stats"> · {{ $t('aiusage.qwMissingHint') }}</span>
          · <a class="qw-console-link" href="https://console.qweather.com" target="_blank" rel="noopener noreferrer">{{ $t('aiusage.openConsole') }}</a>
        </div>

        <!-- 财务（左）与请求量（右）两列并排（2026-10-23 用户指定，对齐卡片版式） -->
        <div class="eo-cols">
          <div>
            <div class="eo-sec">{{ $t('aiusage.finance') }}</div>
            <template v-if="qwAcc.usage.wx.finance">
              <div class="a-row"><span class="a-label">{{ $t('aiusage.availQuota') }}</span><span class="a-val a-total" :class="{ 'qw-neg': (qwAcc.usage.wx.finance.balance || 0) < 0 }">{{ wxMoney(qwAcc.usage.wx.finance.balance, qwAcc.usage.wx.finance.currency) ?? '—' }}</span></div>
              <div class="a-row"><span class="a-label">{{ $t('aiusage.thisMonth') }}</span><span class="a-val">{{ wxMoney(qwAcc.usage.wx.finance.this_month, qwAcc.usage.wx.finance.currency) ?? '—' }}</span></div>
              <div class="a-row"><span class="a-label">{{ $t('aiusage.prevDay') }}</span><span class="a-val">{{ wxMoney(qwAcc.usage.wx.finance.prev_day, qwAcc.usage.wx.finance.currency) ?? '—' }}</span></div>
              <div class="a-row"><span class="a-label">{{ $t('aiusage.sinceBill') }}</span><span class="a-val">{{ wxMoney(qwAcc.usage.wx.finance.since_bill, qwAcc.usage.wx.finance.currency) ?? '—' }}</span></div>
              <div v-if="qwAcc.usage.wx.finance.resource_plans?.length" class="eo-sec">{{ $t('aiusage.packages') }}</div>
              <div v-if="qwAcc.usage.wx.finance.resource_plans?.length" class="qw-plans">
                <div v-for="(p, i) in qwAcc.usage.wx.finance.resource_plans" :key="i" class="zr-mcell">
                  <div class="zr-mlabel">{{ $t('aiusage.planCell', { status: p.status === 'active' ? $t('aiusage.planActive') : $t('aiusage.planPending'), from: p.effective_time ? $t('aiusage.planFrom', { d: qwDate(p.effective_time) }) : '' }) }}</div>
                  <div class="zr-mval">
                    <template v-if="p.requests != null">{{ fmtTimes(p.utilized || 0) }} / {{ fmtTimes(p.requests) }}</template>
                    <template v-else-if="p.commitments != null">{{ wxMoney(p.commitments, qwAcc.usage.wx.finance.currency) }}</template>
                    <template v-else>—</template>
                  </div>
                </div>
              </div>
              <div v-else class="a-empty">{{ $t('aiusage.noPlans') }}</div>
            </template>
            <div v-else class="a-empty">{{ $t('aiusage.financeUnavailable') }}</div>
          </div>
          <div>
            <div class="eo-sec">{{ $t('aiusage.req24hSec') }}</div>
            <template v-if="qwAcc.usage.wx.stats">
              <div class="a-row"><span class="a-label">{{ $t('aiusage.ok') }}</span><span class="a-val">{{ fmtTimes(qwAcc.usage.wx.stats.success) }}</span></div>
              <div class="a-row"><span class="a-label">{{ $t('aiusage.failed') }}</span><span class="a-val">{{ fmtTimes(qwAcc.usage.wx.stats.errors) }}</span></div>
              <div class="a-row"><span class="a-label">{{ $t('aiusage.successRate') }}</span><span class="a-val">{{ wxRate(qwAcc.usage.wx.stats) }}</span></div>
              <div v-if="qwAcc.usage.wx.day_avg_success != null" class="a-row"><span class="a-label">{{ $t('aiusage.dailyAvg') }}</span><span class="a-val">{{ fmtTimes(qwAcc.usage.wx.day_avg_success) }}<span class="qw-sub">{{ $t('aiusage.last30') }}</span><template v-if="qwAcc.usage.wx.day_avg_errors">{{ $t('aiusage.failedN', { n: fmtTimes(qwAcc.usage.wx.day_avg_errors) }) }}</template></span></div>
              <div v-if="qwAcc.usage.wx.month_success != null" class="a-row"><span class="a-label">{{ $t('aiusage.monthlyTotal') }}</span><span class="a-val">{{ wxMonthTotal(qwAcc.usage.wx) }}<template v-if="qwAcc.usage.wx.month_errors">{{ $t('aiusage.failedN', { n: fmtTimes(qwAcc.usage.wx.month_errors) }) }}</template></span></div>
              <div v-if="qwAcc.usage.wx.month_success != null" class="qw-period">{{ $t('aiusage.periodAll', { p: wxPeriod() }) }}</div>
              <div v-for="q in wxQuotas(qwAcc.usage.wx)" :key="'q' + q.key" class="qw-quota">
                <div class="qw-quota-head"><span class="qw-quota-label">{{ q.label }}</span><span class="qw-quota-pct">{{ q.pct.toFixed(1) }}%</span></div>
                <div class="eo-bar"><div class="eo-bar-fill" :style="{ width: q.width + '%', background: barColor(q.pct) }" /></div>
              </div>
            </template>
            <div v-else class="a-empty">{{ $t('aiusage.statsUnavailable') }}</div>
          </div>
        </div>

        <template v-if="qwAcc.usage.wx.finance?.pending_bills?.length">
          <div class="eo-sec" style="margin-top: 12px">{{ $t('aiusage.pendingBills') }}</div>
          <table class="zr-table zr-grid">
            <thead><tr><th>{{ $t('aiusage.thBillNo') }}</th><th>{{ $t('aiusage.thBillDate') }}</th><th>{{ $t('aiusage.thType') }}</th><th>{{ $t('aiusage.thStatus') }}</th><th>{{ $t('aiusage.thAmount') }}</th><th>{{ $t('aiusage.thDueRemain') }}</th><th>{{ $t('aiusage.thDueDate') }}</th></tr></thead>
            <tbody>
              <tr v-for="b in qwAcc.usage.wx.finance.pending_bills" :key="b.number">
                <td>{{ b.number || '—' }}</td>
                <td>{{ qwDate(b.date) || '—' }}</td>
                <td>{{ b.type || '—' }}</td>
                <td><el-tag size="small" :type="b.status === 'overdue' ? 'danger' : 'warning'" effect="plain">{{ b.status === 'overdue' ? $t('aiusage.statusOverdue') : $t('aiusage.statusUnpaid') }}</el-tag></td>
                <td class="zr-num">{{ wxMoney(b.amount, qwAcc.usage.wx.finance.currency) ?? '—' }}</td>
                <td class="zr-num">{{ wxMoney(b.amount_due, qwAcc.usage.wx.finance.currency) ?? '—' }}</td>
                <td>{{ qwDate(b.due_date) || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-if="qwAcc.usage.wx.stats">
          <WxHoursChart v-if="qwHours(qwAcc.usage.wx.stats).list.length" :hours="qwHours(qwAcc.usage.wx.stats).list" />
          <div v-if="wxTop(qwAcc.usage.wx.stats, 20).length" class="eo-sec" style="margin-top: 12px">{{ $t('aiusage.perApi') }}</div>
          <div v-for="t in wxTop(qwAcc.usage.wx.stats, 20)" :key="'d' + t.api" class="eo-rank-line">
            <div class="eo-rank-row"><span class="eo-rank-name">{{ wxAPIName(t.api) }}</span><span class="eo-rank-val">{{ fmtTimes(t.success + t.errors) }}<template v-if="t.errors"> · {{ $t('aiusage.failed') }} {{ fmtTimes(t.errors) }}</template></span></div>
            <div class="eo-bar"><div class="eo-bar-fill" :style="{ width: t.pct + '%' }" /></div>
          </div>
        </template>
        <div v-else class="a-empty">{{ $t('aiusage.statsUnavailable') }}</div>

        <!-- 月请求量（10，2026-10-24 改版沉底）：自然月账本汇总；条=实际请求对
             月配额 5 万次的配额进度（分档色、pct 真实占比可超封条）；仅显示所选
             年份（默认当年），年份下拉切换历史年份 -->
        <template v-if="qwOpen">
          <div class="qw-month-sec">
            <span class="eo-sec">{{ $t('aiusage.monthSec') }}</span>
            <el-select v-model="wxYear" size="small" class="qw-year-sel">
              <el-option v-for="y in wxYearOptions" :key="y" :label="$t('aiusage.yearLabel', { y })" :value="y" />
            </el-select>
          </div>
          <template v-if="wxYearRows.length">
            <div v-for="m in wxYearRows" :key="m.month" class="qw-month-row">
              <div class="qw-month-head">
                <span class="qw-month-name">{{ m.month }}<el-tag v-if="m.current" size="small" effect="plain" class="qw-month-tag">{{ $t('aiusage.ongoingTag') }}</el-tag></span>
                <span class="qw-month-val">{{ fmtTimes(m.success + m.errors) }} · {{ m.pct.toFixed(1) }}%<template v-if="m.errors"> · {{ $t('aiusage.failed') }} <span class="qw-err">{{ fmtTimes(m.errors) }}</span></template></span>
              </div>
              <div class="eo-bar"><div class="eo-bar-fill" :style="{ width: m.barW + '%', background: barColor(m.pct) }" /></div>
            </div>
            <div v-if="wxYearRows.length < 12" class="qw-month-hint">{{ $t('aiusage.monthHint') }}</div>
          </template>
          <div v-else class="a-empty">{{ $t('aiusage.yearEmpty') }}</div>
        </template>
      </template>
      <div v-else class="zr-none">{{ $t('aiusage.noDataYet') }}</div>
    </el-dialog>

    <!-- 智谱用量报告弹层（401）：标题点击展开，数据为最近一次轮询快照直出 -->
    <el-dialog v-model="reportOpen" :title="reportAcc ? $t('aiusage.reportTitle', { name: reportAcc.name }) : $t('aiusage.reportTitleShort')" width="700px" append-to-body>
      <template v-if="zr?.kind === 'quota'">
        <div class="zr-range">
          {{ $t('aiusage.rangeLabel', { from: zr.week_from || '—', to: zr.week_to || '—' }) }}
          <el-tag v-if="zr.level" size="small" effect="plain" class="zr-level">{{ $t('aiusage.levelTag', { v: String(zr.level).toUpperCase() }) }}</el-tag>
        </div>

        <div class="zr-sec">{{ $t('aiusage.weekOverview') }}</div>
        <template v-if="weekWin">
          <div class="zr-bar">
            <el-progress class="zr-bar-p" :percentage="clampPct(weekWin.used_pct)" :color="barColor(weekWin.used_pct)" :stroke-width="10" :show-text="false" />
            <span class="zr-bar-txt">{{ $t('aiusage.barTxt', { used: Math.round(weekWin.used_pct), left: Math.round(100 - weekWin.used_pct) }) }}</span>
          </div>
          <!-- 周额度指标 2×2 网格（412）：一行两格，缺格自动流式 -->
          <div class="zr-mgrid">
            <div class="zr-mcell">
              <div class="zr-mlabel">{{ $t('aiusage.weekUsed') }}</div>
              <div class="zr-mval"><b>{{ weekUsedLabel }}</b><span v-if="weekCalls">{{ $t('aiusage.parenN', { n: fmtTimes(weekCalls) }) }}</span></div>
            </div>
            <div v-if="capInfo" class="zr-mcell">
              <div class="zr-mlabel">{{ $t('aiusage.weekCap') }}</div>
              <div class="zr-mval">{{ capLabel }}</div>
            </div>
            <div v-if="remVal != null" class="zr-mcell">
              <div class="zr-mlabel">{{ $t('aiusage.remaining') }}</div>
              <div class="zr-mval">{{ remLabel }}</div>
            </div>
            <div v-if="weekWin.reset_at" class="zr-mcell">
              <div class="zr-mlabel">{{ $t('aiusage.resetTime') }}</div>
              <div class="zr-mval">{{ fmtFull(weekWin.reset_at) }}<span>{{ $t('aiusage.resetRule', { d: weekDow, t: fmtClock(weekWin.reset_at) }) }}</span></div>
            </div>
          </div>
        </template>
        <div v-else class="zr-none">{{ $t('aiusage.noWeekWin') }}</div>

        <template v-if="zr.week_daily?.length">
          <div class="zr-sec">{{ $t('aiusage.dailyDist', { from: fmtClock(weekWin?.reset_at) || '--:--', to: fmtClock(weekWin?.reset_at) || '--:--' }) }}</div>
          <div class="zr-scroll">
            <table class="zr-table zr-grid">
              <thead>
                <tr>
                  <th>{{ $t('aiusage.thDate') }}</th><th>{{ $t('aiusage.thUsage') }}</th><th>{{ $t('aiusage.thShare') }}</th>
                  <th v-if="hasCalls">{{ $t('aiusage.thCalls') }}</th>
                  <th v-for="m in modelCols" :key="m">{{ m }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="d in zr.week_daily" :key="d.label">
                  <td>{{ d.label }}<template v-if="d.partial">{{ $t('aiusage.partialTo', { t: (zr.week_to || '').slice(-5) || '--:--' }) }}</template></td>
                  <td>{{ fmtTokens(d.tokens) }}</td>
                  <td>{{ pctOfWeek(d.tokens) }}</td>
                  <td v-if="hasCalls">{{ d.calls || '—' }}</td>
                  <td v-for="m in modelCols" :key="m" class="zr-num">{{ modelVal(d, m) }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div class="zr-sec">{{ $t('aiusage.modelTotalsSec') }}</div>
          <table class="zr-table zr-grid">
            <thead><tr><th>{{ $t('aiusage.thModel') }}</th><th>{{ $t('aiusage.thUsage') }}</th><th>{{ $t('aiusage.thPct') }}</th></tr></thead>
            <tbody>
              <tr v-for="m in modelTotals" :key="m.name">
                <td>{{ m.name }}</td>
                <td class="zr-num">{{ fmtTokens(m.tokens) }}</td>
                <td class="zr-num">{{ m.pct }}</td>
              </tr>
            </tbody>
          </table>
        </template>

        <template v-if="fiveWin || zr.tools">
          <div class="zr-sec">{{ $t('aiusage.otherQuotas') }}</div>
          <ul class="zr-ul">
            <li v-if="fiveWin">{{ $t('aiusage.fiveWinLine', { pct: Math.round(fiveWin.used_pct), used: fiveWin.used != null ? $t('aiusage.usedApp', { v: fiveUsedLabel }) : '', t: fiveWin.reset_at ? fmtFull(fiveWin.reset_at) : '—' }) }}</li>
            <li v-if="zr.tools">{{ $t('aiusage.mcpLine', { cur: zr.tools.current, usage: zr.tools.usage, pct: toolsPct, t: zr.tools.reset_at ? fmtFull(zr.tools.reset_at) : '—' }) }}</li>
          </ul>
        </template>

        <div v-if="rhythm" class="zr-note">{{ $t('aiusage.rhythmLabel', { v: rhythm }) }}</div>
      </template>
      <div v-else class="zr-none">{{ $t('aiusage.noDataYet') }}</div>
    </el-dialog>
  </div>
</template>

<script setup>
// 用量查询（356/357/393）：服务商账户（智谱 GLM / DeepSeek / 腾讯 EdgeOne）
// 配置 + 最新用量卡片。数据由后台每 20 分钟轮询覆盖写入，页面读库直出，可
// 全部/单账户手动刷新。EdgeOne 四指标=控制台「站点数据概览」口径。
defineOptions({ name: 'AiUsageView' })
import { computed, onActivated, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api/index.js'
import { fmtTime } from '../utils/format.js'
import { barColor } from '../utils/chartTheme.js'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { registerPageMessages, lang } from '../i18n'
import pageMsgs from '../i18n/pages/aiusage.js'
import WxHoursChart from '../components/WxHoursChart.vue'

const { t: tr } = useI18n()
registerPageMessages(pageMsgs)

const accounts = ref([])
const presets = ref([])
const loading = ref(false)
const loadedAt = ref('')
const refreshingAll = ref(false)
const refreshingId = ref(0)

const dlg = ref(false)
const saving = ref(false)
const blankForm = () => ({ id: 0, provider: 'zhipu', name: '', base_url: '', api_key: '', secret_id: '', secret_key: '', zone_ids: '' })
const form = ref(blankForm())
const formGuard = useDirtyGuard(() => (dlg.value ? { ...form.value } : null))

const PRESET_DEFAULT = { zhipu: 'https://open.bigmodel.cn', deepseek: 'https://api.deepseek.com', edgeone: 'https://teo.intl.tencentcloudapi.com' }
function providerLabel(key) {
  const p = presets.value.find((x) => x.key === key)
  return p?.label || PRESET_LABEL[key]?.[lang.value === 'en' ? 'en' : 'zh'] || key
}
// 服务商品牌 fallback 名（presets 加载失败时兜底）：和风有官方英文名，其余中英同形
const PRESET_LABEL = {
  zhipu: { zh: '智谱 GLM', en: 'Zhipu GLM' },
  deepseek: { zh: 'DeepSeek', en: 'DeepSeek' },
  edgeone: { zh: '腾讯 EdgeOne', en: 'Tencent EdgeOne' },
  qweather: { zh: '和风天气', en: 'QWeather' },
}
function presetBase(key) {
  return PRESET_DEFAULT[key] || 'https://'
}

async function load(showLoading = true) {
  if (showLoading) loading.value = true
  try {
    const res = await api.get('/aiusage/accounts')
    accounts.value = res.accounts || []
    loadedAt.value = new Date().toLocaleTimeString(lang.value === 'en' ? 'en-US' : 'zh-CN', { hour12: false })
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

async function loadPresets() {
  if (presets.value.length) return
  try {
    const res = await api.get('/aiusage/presets')
    presets.value = res.presets || []
  } catch { /* 下拉空时按钮端兜底文案仍可用 */ }
}

async function refreshAll() {
  refreshingAll.value = true
  try {
    await api.post('/aiusage/refresh')
    await load(false)
    ElMessage.success(tr('aiusage.refreshedAll'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    refreshingAll.value = false
  }
}

async function refreshOne(a) {
  refreshingId.value = a.id
  try {
    const res = await api.post(`/aiusage/accounts/${a.id}/refresh`)
    const i = accounts.value.findIndex((x) => x.id === a.id)
    if (i >= 0 && res.account) accounts.value[i] = res.account
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    refreshingId.value = 0
  }
}

function resetForm() {
  form.value = blankForm()
}

function openAdd() {
  resetForm()
  dlg.value = true
  formGuard.snapshot()
}

function openEdit(a) {
  form.value = { id: a.id, provider: a.provider, name: a.name, base_url: a.base_url || '', api_key: '', secret_id: '', secret_key: '', zone_ids: a.zone_ids || '' }
  dlg.value = true
  formGuard.snapshot()
}

async function save() {
  const f = form.value
  if (f.provider === 'edgeone') {
    if (!f.id && (!f.secret_id.trim() || !f.secret_key.trim())) {
      ElMessage.warning(tr('aiusage.needSecrets'))
      return
    }
  } else if (!f.id && !f.api_key.trim()) {
    ElMessage.warning(tr('aiusage.needKey'))
    return
  }
  saving.value = true
  try {
    const body = {
      provider: f.provider, name: f.name.trim(), base_url: f.base_url.trim(),
      api_key: f.api_key.trim(), zone_ids: f.zone_ids.trim(),
      secret_id: f.secret_id.trim(), secret_key: f.secret_key.trim(),
    }
    if (f.id) await api.put(`/aiusage/accounts/${f.id}`, body)
    else await api.post('/aiusage/accounts', body)
    dlg.value = false
    ElMessage.success(f.id ? tr('aiusage.saved') : tr('aiusage.added'))
    load(false)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function remove(a) {
  try {
    await ElMessageBox.confirm(tr('aiusage.delConfirm', { name: a.name }), tr('aiusage.del'), {
      type: 'warning', confirmButtonText: tr('aiusage.del'), cancelButtonText: tr('aiusage.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/aiusage/accounts/${a.id}`)
    ElMessage.success(tr('aiusage.deleted'))
  } catch (e) {
    ElMessage.error(e.message)
  }
  load(false)
}

// ---- EdgeOne 卡片（393）----

// 卡片点击分流（409）：智谱→用量报告弹窗（401）、EdgeOne→详情弹层（398）；
// 标题点击与卡片点击殊途同归，DeepSeek 等其余服务商卡片不可点
const detailOpen = ref(false)
const detailAcc = ref(null)
// 和风天气详情弹层（3）：与 EdgeOne 详情同款交互
const qwOpen = ref(false)
const qwAcc = ref(null)
function cardClick(a) {
  if (a.usage?.kind === 'edgeone') {
    detailAcc.value = a
    detailOpen.value = true
  } else if (a.usage?.kind === 'qweather') {
    qwAcc.value = a
    qwOpen.value = true
    loadWxMonths()
  } else if (a.usage?.kind === 'quota') {
    openReport(a)
  }
}
// 流量命中率：缓存命中流量 / 访问总流量（l7Flow_flux 口径，近似）
const hitRatePct = computed(() => {
  const eo = detailAcc.value?.usage?.edgeone
  if (!eo?.detail?.cache_hit_flux || !eo.traffic_bytes) return null
  return Math.min(100, ((eo.detail.cache_hit_flux / eo.traffic_bytes) * 100).toFixed(1))
})

// 响应流量排行（410/414）：四个维度，条长与百分比按占总响应流量算；
// 每榜取前 5（对齐控制台默认视图，行紧凑保一屏可见）
const fluxRanks = computed(() => {
  const dd = detailAcc.value?.usage?.edgeone?.detail
  const top5 = (items) => (items || []).slice(0, 5)
  return [
    { label: tr('aiusage.rankHost'), items: top5(dd?.flux_hosts) },
    { label: tr('aiusage.rankIp'), items: top5(dd?.flux_ips) },
    { label: tr('aiusage.rankReferer'), items: top5(dd?.flux_referers) },
    { label: tr('aiusage.rankPath'), items: top5(dd?.flux_urls) },
  ]
})
function rankPct(count) {
  const total = detailAcc.value?.usage?.edgeone?.detail?.out_flux
  if (!total || count == null) return null
  const p = (count / total) * 100
  return Number.isFinite(p) ? p : null
}
function barWidth(count) {
  const p = rankPct(count)
  return p == null ? '0%' : Math.min(100, p) + '%'
}
function fmtPps(n) {
  if (n >= 1e6) return (n / 1e6).toFixed(2) + ' Mpps'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + ' Kpps'
  return n + ' pps'
}

// 环比百分比：任一值缺失或基期为 0 时不显示（基期 0 算出的倍数没有意义）
function pctDelta(cur, prev) {
  if (cur == null || prev == null || prev === 0) return null
  return ((cur - prev) / prev) * 100
}
function fmtBytes(n) {
  if (n == null) return null
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return (i >= 2 ? v.toFixed(2) : Math.round(v)) + ' ' + units[i]
}
function fmtMbps(n) {
  if (n == null) return null
  if (n >= 1e6) return (n / 1e6).toFixed(2) + ' Mbps'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + ' Kbps'
  return n + ' bps'
}
function fmtTimes(n) {
  if (n == null) return null
  // lang 感知：en ≥1e6 用 M 缩写；zh 保持原口径（亿缩写 + 千分位完整数字）
  if (lang.value === 'en') {
    if (n >= 1e6) return tr('aiusage.timesM', { n: (n / 1e6).toFixed(2) })
    return tr('aiusage.timesN', { n: Number(n).toLocaleString('en-US') })
  }
  if (n >= 1e8) return tr('aiusage.timesYi', { n: (n / 1e8).toFixed(2) })
  // 1 亿以下完整数字千分位（9：1.23 万次这类缩写看不清实际请求量）
  return tr('aiusage.timesN', { n: Number(n).toLocaleString('en-US') })
}
// 四指标行（对齐截图顺序），value=null 渲染 '—'（防护命中上游不支持/失败时）
function edgeOneRows(eo) {
  return [
    { label: tr('aiusage.eoTraffic'), value: fmtBytes(eo.traffic_bytes), delta: pctDelta(eo.traffic_bytes, eo.prev_traffic) },
    { label: tr('aiusage.eoPeak'), value: fmtMbps(eo.peak_bandwidth), delta: pctDelta(eo.peak_bandwidth, eo.prev_peak) },
    { label: tr('aiusage.eoReqs'), value: fmtTimes(eo.requests), delta: pctDelta(eo.requests, eo.prev_requests) },
    { label: tr('aiusage.eoProtect'), value: eo.protect_hits == null ? null : fmtTimes(eo.protect_hits), delta: pctDelta(eo.protect_hits, eo.prev_hits) },
  ]
}

// ---- 和风天气卡片/详情（3，控制台 API 财务汇总+请求量统计）----

// 上游 api 代码 → 语言包键（对齐和风控制台「Top 数据请求量」文案），未知代码原样展示
const WX_API_KEYS = {
  Weather: 'wxWeather', Geo: 'wxGeo', WeatherAlert: 'wxAlert', WeatherIndices: 'wxIndices',
  AirQuality: 'wxAir', Minutely: 'wxMinutely', WeatherPrecipitation: 'wxMinutely',
  Historical: 'wxHistorical', SolarRadiation: 'wxSolar', Air: 'wxAir',
}
function wxAPIName(k) {
  const key = WX_API_KEYS[k]
  return key ? tr('aiusage.' + key) : k
}
// 金额展示：CNY/USD 出符号，负数（按量计费欠费）带负号；null（降级缺席）返回 null 由模板渲染 '—'
function wxMoney(v, currency) {
  if (v == null) return null
  const sym = currency === 'USD' ? '$' : currency === 'CNY' || !currency ? '¥' : currency + ' '
  return (v < 0 ? '-' : '') + sym + Math.abs(Number(v)).toFixed(2)
}
function wxRate(st) {
  const total = (st?.success || 0) + (st?.errors || 0)
  if (!total) return '—'
  return ((st.success / total) * 100).toFixed(1) + '%'
}
// 当月请求量合计（自累计账本回填，nil=账本当月无桶不显示）
function wxMonthTotal(wx) {
  const total = (wx?.month_success || 0) + (wx?.month_errors || 0)
  return fmtTimes(total)
}
// 本月配额进度条（9/10，2026-10-24 返工只留本月一根，「近 30 天 / 月配额」条
// 已删）：分母=生效中请求量包合计优先，无包回退和风月配额 5 万次（用户指定
// 口径）；pct 展示真实占比（超 100% 照显），条宽封顶 100；本月无数据不显示。
const WX_MONTH_QUOTA = 50000
function wxQuotas(wx) {
  if (wx?.month_success == null) return []
  const pkg = (wx?.finance?.resource_plans || [])
    .filter((p) => p.status === 'active' && p.requests != null)
    .reduce((s, p) => s + p.requests, 0)
  const total = pkg > 0 ? pkg : WX_MONTH_QUOTA
  const v = wx.month_success + (wx.month_errors || 0)
  const pct = (v / total) * 100
  return [{ key: 'month', label: pkg > 0 ? tr('aiusage.quotaPkg') : tr('aiusage.quotaMonth'), v, pct, width: Math.min(100, pct) }]
}
// 自然月统计周期（10）：本月 1 日 00:00 → 现在（详情弹层用，带「进行中」）。
// 账本只入完整小时，当月合计略滞后于墙钟，周期按自然月窗口展示。
function wxPeriod() {
  const d = new Date()
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-01 00:00 → ${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}${tr('aiusage.ongoing')}`
}
// 月请求量按年查看（10，2026-10-24 返工）：开弹层拉全量月份（≤36 个），前端按
// 年过滤——仅显示所选年份（默认当年），条=实际请求对月配额 5 万次的进度。
const wxMonthRaw = ref([])
const wxYear = ref(new Date().getFullYear())
const wxYearOptions = computed(() => {
  const ys = new Set(wxMonthRaw.value.map((m) => Number(m.month.slice(0, 4))))
  ys.add(new Date().getFullYear())
  return [...ys].sort((a, b) => b - a)
})
const wxYearRows = computed(() => {
  const d = new Date()
  const cur = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
  return wxMonthRaw.value
    .filter((m) => Number(m.month.slice(0, 4)) === wxYear.value)
    .map((m) => {
      const pct = ((m.success + m.errors) / WX_MONTH_QUOTA) * 100
      return { ...m, current: m.month === cur, pct, barW: Math.min(100, pct) }
    })
})
async function loadWxMonths() {
  try {
    const res = await api.get('/aiusage/wx-months')
    wxMonthRaw.value = res.months || []
  } catch {
    wxMonthRaw.value = []
  }
}
// 分 API 排行：总量降序取前 n（卡片 5 / 详情 20），条长=占最大值比（空值给 2% 免得整条消失）
function wxTop(st, n = 5) {
  const list = [...(st?.per_api || [])].sort((a, b) => b.success + b.errors - (a.success + a.errors)).slice(0, n)
  const max = Math.max(...list.map((t) => t.success + t.errors), 0)
  return list.map((t) => ({ ...t, pct: max ? Math.max(2, ((t.success + t.errors) / max) * 100) : 0 }))
}
// 逐小时柱：成功/失败堆叠（失败在上），高度=占 24h 峰值比
function qwHours(st) {
  const list = st?.hours || []
  const max = Math.max(...list.map((h) => h.success + h.errors), 0)
  return { max, list: list.map((h) => ({ ...h, okH: max ? (h.success / max) * 100 : 0, errH: max ? (h.errors / max) * 100 : 0 })) }
}
function qwDate(s) {
  if (!s) return ''
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

// ---- 智谱用量报告弹层（401）----

const reportOpen = ref(false)
const reportAcc = ref(null)
function openReport(a) {
  reportAcc.value = a
  reportOpen.value = true
}
const zr = computed(() => reportAcc.value?.usage || null)
const weekWin = computed(() => zr.value?.windows?.find((w) => w.key === 'week') || null)
const fiveWin = computed(() => zr.value?.windows?.find((w) => w.key === '5h') || null)
const hasCalls = computed(() => (zr.value?.week_daily || []).some((d) => d.calls > 0))
const weekCalls = computed(() => (zr.value?.week_daily || []).reduce((s, d) => s + (d.calls || 0), 0))

// 本周分布里的模型并集（按周用量降序）作动态列
const weekTokenTotal = computed(() => (zr.value?.week_daily || []).reduce((s, d) => s + d.tokens, 0))
const modelCols = computed(() => {
  const acc = new Map()
  for (const d of zr.value?.week_daily || []) {
    for (const m of d.models || []) acc.set(m.name, (acc.get(m.name) || 0) + m.tokens)
  }
  return [...acc.entries()].sort((a, b) => b[1] - a[1]).map(([n]) => n)
})
const modelTotals = computed(() => {
  const total = weekTokenTotal.value
  return modelCols.value.map((name) => {
    const tokens = (zr.value?.week_daily || []).reduce((s, d) => s + ((d.models || []).find((x) => x.name === name)?.tokens || 0), 0)
    return { name, tokens, pct: total > 0 ? ((tokens / total) * 100).toFixed(1) + '%' : '—' }
  })
})

// 本周已用 tokens（405）：优先窗口绝对计数，缺则用周分布（model-usage 分析
// 端点）逐日求和兜底——保证报告里「本周已用/上限/剩余」一律有 tokens 口径；
// 积分窗口（V3 CREDIT_LIMIT）绝对计数是积分非 tokens，一律走周分布求和
const weekUsedTokens = computed(() => {
  const w = weekWin.value
  if (w?.credit) return weekTokenTotal.value > 0 ? weekTokenTotal.value : null
  if (w?.used != null) return w.used
  const sum = weekTokenTotal.value
  return sum > 0 ? sum : null
})

// 积分→tokens 折算比（2026-10-17）：model-usage 整周真实 tokens ÷ 同期周积分
// 消耗 = 1 积分≈N tokens 经验比；周积分计数或周分布缺一即不折算
const creditRatio = computed(() => {
  const w = weekWin.value
  if (!w?.credit || !w.used) return null
  return weekTokenTotal.value > 0 ? weekTokenTotal.value / w.used : null
})

// ── 详情弹层展示串（574 双口径定稿）：积分窗口一律 tokens 在前 / 积分在后，
// 折算值带 ≈；无折算比（缺 model-usage 或积分为 0）只显积分原值 ──
const weekUsedLabel = computed(() => {
  const w = weekWin.value
  if (!w) return ''
  if (weekUsedTokens.value != null) {
    const t = `${fmtTokens(weekUsedTokens.value)} tokens`
    return w.credit && w.used != null ? `${t} / ${tr('aiusage.creditsN', { v: fmtTokens(w.used) })}` : t
  }
  if (w.credit && w.used != null) return tr('aiusage.creditsN', { v: fmtTokens(w.used) })
  return Math.round(w.used_pct) + '%'
})
const capLabel = computed(() => {
  const info = capInfo.value
  if (!info) return ''
  if (weekWin.value?.credit) {
    return creditRatio.value
      ? tr('aiusage.capCredits', { a: fmtTokens(info.v * creditRatio.value), v: fmtTokens(info.v) })
      : tr('aiusage.creditsN', { v: fmtTokens(info.v) })
  }
  return (info.est ? '≈ ' : '') + fmtTokens(info.v) + (info.est ? tr('aiusage.estNote') : '')
})
const remLabel = computed(() => {
  if (remVal.value == null) return ''
  if (weekWin.value?.credit) {
    return creditRatio.value
      ? tr('aiusage.capCredits', { a: fmtTokens(remVal.value * creditRatio.value), v: fmtTokens(remVal.value) })
      : tr('aiusage.creditsN', { v: fmtTokens(remVal.value) })
  }
  return (capInfo.value?.est ? '≈ ' : '') + fmtTokens(remVal.value)
})
const fiveUsedLabel = computed(() => {
  const w = fiveWin.value
  if (!w || w.used == null) return ''
  if (w.credit) {
    return creditRatio.value
      ? tr('aiusage.capCredits', { a: fmtTokens(w.used * creditRatio.value), v: fmtTokens(w.used) })
      : tr('aiusage.creditsN', { v: fmtTokens(w.used) })
  }
  return fmtTokens(w.used)
})

// 额度上限：绝对值直出；缺了但有已用 tokens + 百分比则反推（标注估计值）
const capInfo = computed(() => {
  const w = weekWin.value
  if (!w) return null
  if (w.total) return { v: w.total, est: false }
  if (weekUsedTokens.value && w.used_pct > 0) {
    return { v: Math.round(weekUsedTokens.value / (w.used_pct / 100)), est: true }
  }
  return null
})
const remVal = computed(() => {
  const w = weekWin.value
  if (!w) return null
  if (w.remaining != null) return w.remaining
  if (capInfo.value && weekUsedTokens.value != null) {
    return Math.max(0, capInfo.value.v - weekUsedTokens.value)
  }
  return null
})

function modelVal(d, name) {
  const m = (d.models || []).find((x) => x.name === name)
  return m && m.tokens > 0 ? fmtTokens(m.tokens) : '—'
}
function pctOfWeek(tokens) {
  return weekTokenTotal.value > 0 ? ((tokens / weekTokenTotal.value) * 100).toFixed(1) + '%' : '—'
}
const toolsPct = computed(() => {
  const t = zr.value?.tools
  if (!t || !t.usage) return '—'
  return Math.round((t.current / t.usage) * 100)
})
function fmtFull(ts) {
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}${tr('aiusage.dowFull', { d: dowName(d.getDay()) })}${p(d.getHours())}:${p(d.getMinutes())}`
}
// 星期人读名（i18n：zh 日一二… / en Sun Mon…）
function dowName(i) {
  return tr('aiusage.dow' + i)
}
function fmtClock(ts) {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}`
}
const weekDow = computed(() => (weekWin.value?.reset_at ? dowName(new Date(weekWin.value.reset_at * 1000).getDay()) : tr('aiusage.dow1')))

// 节奏参考：已用/剩余 tokens（补齐口径后基本总有）与周起点齐才显示
const rhythm = computed(() => {
  const w = weekWin.value
  if (!w || weekUsedTokens.value == null || remVal.value == null || !zr.value?.week_start || !w.reset_at) return ''
  const now = Date.now() / 1000
  const daysElapsed = Math.max(0.5, (now - zr.value.week_start) / 86400)
  const daysLeft = Math.max(0, (w.reset_at - now) / 86400)
  const avg = weekUsedTokens.value / daysElapsed
  let s = tr('aiusage.rhythmMain', { a: fmtTokens(Math.round(avg)), d: daysLeft.toFixed(1) })
  // 积分窗口（V3 CREDIT_LIMIT）的 remaining 是积分非 tokens，须按 creditRatio 折成 tokens 再与日均（tokens）相除，否则单位错商恒近 0
  const remTokens = w.credit ? (creditRatio.value != null ? remVal.value * creditRatio.value : null) : remVal.value
  if (avg > 0 && remTokens != null)
    s += tr('aiusage.rhythmLeft', { n: fmtTokens(remTokens), d: (remTokens / avg).toFixed(1) })
  return s
})

function clampPct(p) {
  return Math.max(0, Math.min(100, Math.round(p)))
}
// tokens 计数人性化（lang 感知）：zh 亿（周额度量级）/ 万 / 原值；en B / M / K / 原值
function fmtTokens(n) {
  if (n == null) n = 0
  if (lang.value === 'en') {
    if (n >= 1e9) return (n / 1e9).toFixed(2) + 'B'
    if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M'
    if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K'
    return String(n)
  }
  if (n >= 1e8) return tr('aiusage.tokensYi', { v: (n / 1e8).toFixed(2) })
  if (n >= 1e4) return tr('aiusage.tokensWan', { v: (n / 1e4).toFixed(1) })
  return String(n)
}
function pctColor(p) {
  if (p >= 81) return 'var(--crit, #f56c6c)'
  if (p >= 51) return 'var(--warn-text, #e6a23c)'
  return 'var(--el-color-success, #67c23a)'
}
function fmtReset(ts) {
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())} `
}

onMounted(() => { load(); loadPresets() })
onActivated(() => load())
</script>

<style scoped>
.aiusage {
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

/* 账户卡片：自适应网格，手机端自然落成单列 */
.acc-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
}
.acc-card {
  border-radius: 10px;
}
.acc-card :deep(.el-card__body) {
  padding: 14px 16px 10px;
}
.a-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
}
.a-name {
  font-size: 15px;
  font-weight: 700;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.a-tags {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: none;
}
.a-level {
  text-transform: uppercase;
}

/* 智谱窗口条 */
.a-win {
  margin-bottom: 10px;
}
.a-win-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 3px;
}
.a-win-label {
  color: var(--muted);
  font-size: 12px;
}
.a-win-pct {
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.a-reset {
  color: var(--muted);
  font-size: 11px;
  margin-top: 2px;
}
.a-counts {
  color: var(--muted);
  font-size: 11px;
  margin-top: 3px;
  font-variant-numeric: tabular-nums;
}

/* DeepSeek 余额与通用行 */
.a-bal {
  padding-bottom: 4px;
}
/* EdgeOne 卡片（393） */
.eo-sub {
  color: var(--muted);
  font-size: 12px;
  margin-bottom: 4px;
}
.eo-delta {
  color: var(--crit, #f56c6c);
  font-size: 11px;
  margin-left: 8px;
  font-variant-numeric: tabular-nums;
}
.eo-delta.down {
  color: var(--el-color-success, #67c23a);
}
.eo-hint {
  color: var(--muted);
  font-size: 11px;
  line-height: 1.5;
  margin-top: 2px;
}
/* EdgeOne 详情弹层（398） */
.acc-card.clickable {
  cursor: pointer;
  transition: border-color 0.15s;
}
.acc-card.clickable:hover {
  border-color: var(--el-color-primary-light-5, #79bbff);
}
.eo-sec {
  font-size: 13px;
  font-weight: 700;
  margin: 14px 0 4px;
}
.eo-sec:first-of-type {
  margin-top: 4px;
}
.eo-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 8px;
}
.eo-cols {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 16px;
}
.eo-list {
  display: flex;
  flex-direction: column;
}
.eo-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 12px;
  line-height: 1.7;
}
.eo-item + .eo-item {
  border-top: 1px dashed var(--border, #ebeef5);
}
.eo-item-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, Menlo, Consolas, monospace;
  color: var(--el-text-color-regular, #606266);
}
.eo-item-count {
  flex: none;
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}
/* 响应流量排行（410，对齐控制台指标分析版式） */
.eo-rank-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px 20px;
}
.eo-rank-grid > div {
  min-width: 0;
}
.eo-rank-title {
  font-size: 12px;
  font-weight: 700;
  margin-bottom: 4px;
}
.eo-rank-line {
  margin-bottom: 5px;
}
.eo-rank-row {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  font-size: 12px;
  line-height: 1.5;
}
.eo-rank-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, Menlo, Consolas, monospace;
  color: var(--el-text-color-regular, #606266);
}
.eo-rank-val {
  flex: none;
  font-variant-numeric: tabular-nums;
}
.eo-bar {
  height: 3px;
  border-radius: 2px;
  background: var(--el-fill-color-darker, #ebedf0);
  margin-top: 2px;
  overflow: hidden;
}
.eo-bar-fill {
  height: 100%;
  border-radius: 2px;
  background: var(--el-color-primary, #409eff);
  transition: width 0.3s;
}
.a-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  font-size: 13px;
  line-height: 1.9;
}
/* 当日分模型明细（384）：缩进小胶囊流，与「当日已用」行左对齐 */
.a-models {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 8px;
  margin: -4px 0 2px 80px;
  font-size: 12px;
}
.a-model {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px 8px;
  border-radius: 8px;
  background: var(--el-fill-color, #f5f7fa);
  max-width: 100%;
}
.a-model-name {
  color: var(--muted, #909399);
  font-family: ui-monospace, Menlo, Consolas, monospace;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.a-model-tokens {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}
.a-label {
  flex: none;
  width: 70px;
  color: var(--muted);
  font-size: 12px;
}
.a-val {
  min-width: 0;
  font-variant-numeric: tabular-nums;
}
.a-total {
  font-weight: 600;
}
.a-empty {
  color: var(--muted);
  font-size: 12px;
  padding: 6px 0;
}
.a-err {
  color: var(--crit, #f56c6c);
  font-size: 12px;
  line-height: 1.6;
  margin-top: 6px;
  word-break: break-all;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.a-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px dashed var(--border, #ebeef5);
  margin-top: 8px;
  padding-top: 6px;
}
.a-checked {
  color: var(--muted);
  font-size: 12px;
}
.a-acts {
  display: flex;
  align-items: center;
}

/* ---- 智谱用量报告弹层（401，版式对齐附件 GLM Coding Plan 用量报告） ---- */
.a-name-link { cursor: pointer; }
.a-name-link:hover { color: var(--el-color-primary); }
.zr-range {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  opacity: 0.85;
}
.zr-level { margin-left: 2px; }
.zr-sec {
  margin: 14px 0 8px;
  font-size: 13.5px;
  font-weight: 700;
}
.zr-bar { display: flex; align-items: center; gap: 12px; }
.zr-bar-p { flex: 1; }
.zr-bar-txt { flex: none; font-size: 12.5px; opacity: 0.8; white-space: nowrap; }
.zr-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.zr-table th {
  padding: 7px 10px;
  border: 1px solid var(--el-border-color-lighter);
  background: var(--el-fill-color-light);
  font-weight: 600;
  text-align: center;
  white-space: nowrap;
}
.zr-table td {
  padding: 7px 10px;
  border: 1px solid var(--el-border-color-lighter);
  text-align: center;
  word-break: break-all;
}
.zr-table:not(.zr-grid) th {
  width: 110px;
  text-align: left;
}
.zr-table:not(.zr-grid) td { text-align: left; }
.zr-table td b { font-variant-numeric: tabular-nums; }
/* 周额度指标 2×2 网格（412）：一行两格 */
.zr-mgrid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin: 10px 0 2px;
}
.zr-mcell {
  border: 1px solid var(--el-border-color-lighter);
  background: var(--el-fill-color-light);
  border-radius: 8px;
  padding: 8px 12px;
  min-width: 0;
}
.zr-mlabel {
  color: var(--muted);
  font-size: 11.5px;
  margin-bottom: 2px;
}
.zr-mval {
  font-size: 13.5px;
  font-variant-numeric: tabular-nums;
  word-break: break-all;
}
.zr-mval b { font-weight: 700; }
.zr-mval span { color: var(--muted); font-size: 12px; }
.zr-grid td:first-child, .zr-grid th:first-child { text-align: left; }
.zr-num { font-variant-numeric: tabular-nums; white-space: nowrap; }
.zr-scroll { overflow-x: auto; }
.zr-ul {
  margin: 0;
  padding-left: 20px;
  font-size: 13px;
  line-height: 1.9;
}
.zr-note {
  margin-top: 14px;
  padding: 9px 12px;
  border-radius: 8px;
  background: var(--el-fill-color-light);
  font-size: 12.5px;
  opacity: 0.9;
}

/* 和风天气卡片/详情（3） */
.qw-console-link {
  color: var(--el-color-primary, #409eff);
  text-decoration: none;
}
.qw-console-link:hover {
  text-decoration: underline;
}
.qw-neg {
  color: var(--crit, #f56c6c);
}
.qw-err {
  color: var(--crit, #f56c6c);
}
.qw-top {
  margin-top: 6px;
}
/* 资源包进度条（9）：标签左、占比右，条宽封顶 100%、分档色按真实占比 */
.qw-quota {
  margin-top: 7px;
}
.qw-quota-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  font-size: 12px;
  margin-bottom: 3px;
}
.qw-quota-label {
  color: var(--el-text-color-secondary, #909399);
}
.qw-quota-pct {
  font-variant-numeric: tabular-nums;
}
/* 日均请求值后的口径小字（近 30 天） */
.qw-sub {
  font-size: 12px;
  color: var(--el-text-color-secondary, #909399);
}
/* 统计周期（10）：月总请求行下的小字周期说明 */
.qw-period {
  font-size: 12px;
  color: var(--el-text-color-secondary, #909399);
  margin-top: 2px;
}
/* 月请求量（10 沉底改版）：标题左+年份下拉右；条=实际对 5 万次/月配额的进度 */
.qw-month-sec {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 14px;
}
.qw-month-sec .eo-sec {
  margin: 0;
}
.qw-year-sel {
  width: 104px;
}
.qw-month-row {
  margin-top: 6px;
}
.qw-month-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  font-size: 13px;
}
.qw-month-name {
  display: flex;
  align-items: center;
  gap: 6px;
  font-variant-numeric: tabular-nums;
}
.qw-month-tag {
  transform: scale(0.85);
}
.qw-month-val {
  font-variant-numeric: tabular-nums;
}
.qw-month-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary, #909399);
  margin-top: 8px;
}
/* 额度（左）与请求量（右）两列并排（2026-10-23 用户指定版式） */
.qw-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 14px;
  margin-bottom: 2px;
}
.qw-grid .a-label {
  width: auto;
  min-width: 62px;
}
.qw-plans {
  display: grid;
  gap: 8px;
}

/* 手机端：EdgeOne/和风 详情两组栅格落单列（414） */
@media (max-width: 768px) {
  .eo-detail-dlg .eo-cols,
  .qw-detail-dlg .eo-cols,
  .eo-detail-dlg .eo-rank-grid {
    grid-template-columns: 1fr;
  }
}
.zr-none {
  padding: 8px 0;
  font-size: 13px;
  opacity: 0.65;
}
</style>

<style>
/* EdgeOne 详情弹层（410）：内容多，body 限高滚动（el-dialog 结构在 scoped 之外） */
.eo-detail-dlg .el-dialog__body {
  max-height: 80vh;
  overflow-y: auto;
  padding-top: 8px;
}
/* 和风天气详情弹层（3）：同款限高 */
.qw-detail-dlg .el-dialog__body {
  max-height: 80vh;
  overflow-y: auto;
  padding-top: 8px;
}
</style>
