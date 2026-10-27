<template>
  <el-card
    shadow="hover"
    class="metric-card"
    :class="{ 'is-firing': server.firing > 0, 'is-offline': !server.online && server.enabled }"
  >
    <div class="head">
      <el-tooltip :content="server.online ? $t('charts.online') : $t('charts.offline')">
        <span class="dot" :class="server.online ? 'on' : 'off'" />
      </el-tooltip>
      <!-- 仅点击名称进详情，卡片其余位置不跳转 -->
      <span class="name name-link" :title="nameTitle" @click="$emit('open', server.id)">
        <img v-if="server.icon" class="srv-ico" :src="server.icon" alt="" @error="$event.target.style.display = 'none'" />{{ displayName }}
      </span>
      <el-tag v-if="server.firing > 0" type="danger" size="small" effect="dark">
        {{ $t('charts.alertsN', { n: server.firing }) }}
      </el-tag>
      <el-tag v-else-if="!server.enabled" type="info" size="small">{{ $t('charts.disabled') }}</el-tag>
      <el-tag v-else-if="!server.online" type="info" size="small">{{ $t('charts.offline') }}</el-tag>
      <!-- 右上角三点菜单：编辑服务器（不触发卡片进详情）。
           触发器必须是真实元素——中间再包 el-tooltip 会让 dropdown 挂不上点击 -->
      <el-dropdown trigger="click" @command="onCmd">
        <span class="more-btn" :title="$t('charts.srvOps')" @click.stop>
          <el-icon><MoreFilled /></el-icon>
        </span>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item command="edit">{{ $t('charts.editServer') }}</el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>

    <div class="meta">
      <span>{{ $t('charts.upFor', { t: fmtUptimeDays(latest?.uptime) }) }}</span>
      <!-- 规格几C几G（内存取整有小数+1）；无任何规格数据不显示 -->
      <span v-if="spec">{{ spec }}</span>
      <!-- 系统改展示主机名，系统/内核信息悬停可见 -->
      <span :title="[server.os, server.kernel, server.arch].filter(Boolean).join('  ')">
        {{ server.hostname || '-' }}
      </span>
    </div>

    <div class="bars">
      <div class="bar-row">
        <span class="bar-label">CPU</span>
        <el-progress :percentage="pct(latest?.cpu_percent)" :stroke-width="8" :color="barColor(latest?.cpu_percent)" />
        <span class="bar-value">{{ fmtPercent(latest?.cpu_percent) }}</span>
      </div>
      <div class="bar-row">
        <span class="bar-label">{{ $t('charts.mem') }}</span>
        <el-progress :percentage="pct(latest?.mem_percent)" :stroke-width="8" :color="barColor(latest?.mem_percent)" />
        <span class="bar-value" :title="memDetail">{{ fmtPercent(latest?.mem_percent) }}</span>
      </div>
      <!-- 磁盘：仅显示根分区（/），与 CPU/内存同款行；全部挂载点见详情页 -->
      <div class="bar-row" :title="diskDetail">
        <span class="bar-label">{{ $t('charts.disk') }}</span>
        <el-progress :percentage="pct(rootDisk?.percent)" :stroke-width="8" :color="barColor(rootDisk?.percent)" />
        <span class="bar-value">{{ fmtPercent(rootDisk?.percent) }}</span>
      </div>
      <!-- IO：根分区底层设备的 IO 使用率（iostat %util 口径） -->
      <div class="bar-row" :title="ioTitle">
        <span class="bar-label">IO</span>
        <el-progress :percentage="pct(rootDisk?.io_util)" :stroke-width="8" :color="barColor(rootDisk?.io_util)" />
        <span class="bar-value">{{ fmtPercent(rootDisk?.io_util) }}</span>
      </div>
      <!-- 到期倒计时：绿条长度=剩余时间占比，越接近到期越短；未配置日期=满条「永久」 -->
      <div class="bar-row" :title="expiryTitleText">
        <span class="bar-label">{{ $t('charts.expires') }}</span>
        <el-progress :percentage="expiry.percent" :stroke-width="8" :color="EXPIRY_BAR" />
        <span class="bar-value ex-val" :class="`ex-${expiry.level}`">{{ expiry.text }}</span>
      </div>
    </div>

    <!-- 网络：按服务器配置的网口过滤（未配置=全部） -->
    <div v-if="shownNets.length" class="section">
      <div class="sec-title">{{ $t('charts.net') }}</div>
      <div v-for="n in shownNets" :key="n.iface" class="net-row">
        <span class="disk-mount">{{ n.iface }}</span>
        <span class="net-rate">↓ {{ fmtBps(n.rx_bps) }}</span>
        <span class="net-rate">↑ {{ fmtBps(n.tx_bps) }}</span>
      </div>
    </div>
  </el-card>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { MoreFilled } from '@element-plus/icons-vue'
import { EXPIRY_BAR, expiryInfo, expiryTitle, fmtBps, fmtBytes, fmtPercent, fmtUptimeDays, pct, pickNets, specText, withRemark } from '../utils/format.js'
import { barColor } from '../utils/chartTheme.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/svcmon.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

const props = defineProps({
  server: { type: Object, required: true },
})
const emit = defineEmits(['open', 'edit'])

function onCmd(cmd) {
  if (cmd === 'edit') emit('edit', props.server)
}

const latest = computed(() => props.server.latest)
const disks = computed(() => latest.value?.disks || [])
// 卡片只展示根分区；全部挂载点在详情页
const rootDisk = computed(() => disks.value.find((d) => d.mount === '/') || null)
const diskDetail = computed(() =>
  rootDisk.value ? `/  ${fmtBytes(rootDisk.value.used)} / ${fmtBytes(rootDisk.value.total)}` : '',
)

// 名称展示创建时填写的名字，悬停可见主机名（第二行为备注，若有）
const displayName = computed(() => props.server.name)
// meta 行规格（几C几G，取代原 load 数值）
const spec = computed(() => specText(props.server))
const nameTitle = computed(() =>
  withRemark(
    props.server.hostname && props.server.hostname !== props.server.name
      ? `${props.server.name} / ${props.server.hostname}`
      : props.server.name,
    props.server.remark,
  ),
)

// net_ifaces 为空 = 展示全部网口；配置了则按顺序只展示配置的
const shownNets = computed(() => pickNets(latest.value?.nets, props.server.net_ifaces))

const memDetail = computed(() =>
  latest.value ? `${fmtBytes(latest.value.mem_used)} / ${fmtBytes(latest.value.mem_total)}` : '',
)

// IO 行悬停：设备名 + 口径说明（旧版 agent 无 io_util 时百分比显示 -）
const ioTitle = computed(() => {
  const d = rootDisk.value
  if (!d || d.io_util == null) return ''
  return t('charts.ioTitle', { mount: d.mount, device: d.device || t('charts.unknownDevice') })
})

// 到期倒计时：绿条长度=剩余时间占比，文字=剩余天数（临期变黄/红）；未配置=满条「永久」
const expiry = computed(() => expiryInfo(props.server.purchased_on, props.server.expires_on))
const expiryTitleText = computed(() => expiryTitle(props.server))
</script>

<style scoped>
.metric-card.is-firing {
  border-color: var(--crit);
}
.metric-card.is-offline {
  opacity: 0.75;
}
.head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.dot.on {
  background: var(--ok);
}
.dot.off {
  background: var(--dot-off);
}
.name {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
  min-width: 0;
}
/* 名称：唯一进详情入口 */
.name-link {
  cursor: pointer;
}
.name-link:hover {
  color: var(--brand);
}
/* 右上角三点：悬停才显形，避免与名称/标签抢位置 */
.more-btn {
  flex: none;
  display: inline-flex;
  align-items: center;
  padding: 2px;
  font-size: 16px;
  color: var(--muted);
  cursor: pointer;
  outline: none;
}
.more-btn:hover {
  color: var(--brand);
}
.meta {
  display: flex;
  gap: 12px;
  color: var(--text-2);
  font-size: 12px;
  margin-bottom: 10px;
  overflow: hidden;
  white-space: nowrap;
}
.bars {
  display: flex;
  flex-direction: column;
}
/* CPU/内存/磁盘 各行之间以横线分隔 */
.bar-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  padding: 5px 0;
}
.bar-row + .bar-row {
  border-top: 1px solid var(--border);
}
/* 网络块与上方磁盘行之间同样以横线分隔 */
.section {
  margin-top: 2px;
  padding-top: 6px;
  border-top: 1px solid var(--border);
}
.sec-title {
  color: var(--muted);
  font-size: 12px;
  margin-bottom: 4px;
}
.section .net-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
/* 磁盘行与 CPU/内存同结构；标签列放挂载点（超长省略） */
.disk-mount {
  width: 56px;
  color: var(--text-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: none;
}
.bar-row :deep(.el-progress) {
  flex: 1;
  min-width: 0;
}
.bar-row :deep(.el-progress__text) {
  display: none;
}
.bar-label {
  width: 30px;
  color: var(--text-2);
  flex: none;
}
.bar-value {
  width: 44px;
  text-align: right;
  font-variant-numeric: tabular-nums;
  flex: none;
}
/* 到期倒计时：文字比利用率数值长（如「过期 123 天」） */
.ex-val {
  width: 68px;
}
.ex-warn {
  color: var(--warn-text);
  font-weight: 600;
}
.ex-crit {
  color: var(--crit);
  font-weight: 600;
}
.net-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  margin-bottom: 2px;
  flex-wrap: nowrap;
  font-variant-numeric: tabular-nums;
}
.net-rate {
  flex: 1;
  text-align: right;
  color: var(--text-2);
  white-space: nowrap;
}
</style>
