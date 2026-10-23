<template>
  <el-table :data="servers" class="row-table">
    <el-table-column label="名称" min-width="150">
      <template #default="{ row }">
        <span class="cell-name">
          <span v-if="!manage" class="dot" :class="row.online ? 'on' : 'off'" />
          <img v-if="row.icon" class="srv-ico" :src="row.icon" alt="" @error="$event.target.style.display = 'none'" />
          <!-- 仅点击名称进详情，行其余位置不跳转 -->
          <span
            class="t-name t-link"
            :title="withRemark(row.hostname && row.hostname !== row.name ? row.hostname : '', row.remark)"
            @click.stop="emit('open', row.id)"
          >
            {{ row.name }}
          </span>
          <el-tag v-if="!manage && row.firing > 0" type="danger" size="small" effect="dark">告警 {{ row.firing }}</el-tag>
        </span>
      </template>
    </el-table-column>
    <!-- 管理模式（服务器管理页）只保留配置信息列（名称/采集方式/地址/备注/核数/
         主机名/到期/启用态/操作），全部监控指标列不渲染（2026-10-05 用户指定 415） -->
    <el-table-column v-if="!manage" label="CPU%" width="115" align="center">
      <template #default="{ row }">
        <div class="pbar" v-if="row.latest">
          <span class="pb-txt" :class="utilClass(row.latest.cpu_percent)">{{ fmtPercent(row.latest.cpu_percent) }}</span>
          <el-progress class="pb-bar" :percentage="pct(row.latest.cpu_percent)" :stroke-width="8" :show-text="false" :color="barColor(row.latest.cpu_percent)" />
        </div>
        <span v-else class="muted">-</span>
      </template>
    </el-table-column>
    <el-table-column v-if="!manage" label="内存" width="115" align="center">
      <template #default="{ row }">
        <div
          class="pbar"
          v-if="row.latest"
          :title="`${fmtBytes(row.latest.mem_used)} / ${fmtBytes(row.latest.mem_total)}`"
        >
          <span class="pb-txt" :class="utilClass(row.latest.mem_percent)">{{ fmtPercent(row.latest.mem_percent) }}</span>
          <el-progress class="pb-bar" :percentage="pct(row.latest.mem_percent)" :stroke-width="8" :show-text="false" :color="barColor(row.latest.mem_percent)" />
        </div>
        <span v-else class="muted">-</span>
      </template>
    </el-table-column>
    <el-table-column v-if="!manage" label="磁盘" width="115">
      <template #default="{ row }">
        <!-- 只显示占用最高一块盘的进度条，明细悬停可见 -->
        <div class="pbar" v-if="worstDisk(row)" :title="diskTitle(row)">
          <span class="pb-txt" :class="utilClass(worstDisk(row).percent)">{{ fmtPercent(worstDisk(row).percent) }}</span>
          <el-progress class="pb-bar" :percentage="pct(worstDisk(row).percent)" :stroke-width="8" :show-text="false" :color="barColor(worstDisk(row).percent)" />
        </div>
        <span v-else class="muted">-</span>
      </template>
    </el-table-column>
    <el-table-column v-if="!manage" label="网络" width="220">
      <template #default="{ row }">
        <span v-if="shownNets(row).length" class="net-cell" :title="netTitle(row)">
          <span>↓ {{ fmtBps(netSum(row).rx) }}</span>
          <span>↑ {{ fmtBps(netSum(row).tx) }}</span>
        </span>
        <span v-else class="muted">-</span>
      </template>
    </el-table-column>
    <el-table-column v-if="!manage" label="磁盘读写" width="200">
      <template #default="{ row }">
        <!-- 读/写同列自适应单位（硬盘容量口径 KB/s→TB/s），各设备速率合计，分设备明细悬停可见（旧数据无速率字段显示 -） -->
        <span v-if="ioDevices(row).length" class="net-cell" :title="diskIOTitle(row)">
          <span>读 {{ fmtDiskBps(diskIOSum(row).r) }}</span>
          <span>写 {{ fmtDiskBps(diskIOSum(row).w) }}</span>
        </span>
        <span v-else class="muted">-</span>
      </template>
    </el-table-column>
    <el-table-column v-if="!manage" label="磁盘IO" width="115">
      <template #default="{ row }">
        <!-- 与磁盘列同款：显示最忙设备的 IO 使用率，分设备明细悬停可见（旧数据无 io_util 显示 -） -->
        <div class="pbar" v-if="worstIO(row)" :title="ioTitle(row)">
          <span class="pb-txt" :class="utilClass(worstIO(row).io_util)">{{ fmtPercent(worstIO(row).io_util) }}</span>
          <el-progress class="pb-bar" :percentage="pct(worstIO(row).io_util)" :stroke-width="8" :show-text="false" :color="barColor(worstIO(row).io_util)" />
        </div>
        <span v-else class="muted">-</span>
      </template>
    </el-table-column>
    <el-table-column v-if="!manage" label="连接数" width="80" align="center">
      <template #default="{ row }">
        <span class="tcp-cell">{{ row.latest?.tcp_established ?? '-' }}</span>
      </template>
    </el-table-column>
    <el-table-column label="核数" width="70" align="center">
      <template #default="{ row }">{{ row.cpu_cores ? `${row.cpu_cores} 核` : '-' }}</template>
    </el-table-column>
    <el-table-column v-if="!manage" label="内存大小" width="95" align="center">
      <template #default="{ row }">{{ fmtBytes(row.latest?.mem_total) }}</template>
    </el-table-column>
    <el-table-column label="主机名" min-width="120" show-overflow-tooltip>
      <template #default="{ row }">
        <!-- 系统信息改为悬停可见 -->
        <span :title="osTitle(row)">{{ row.hostname || '-' }}</span>
      </template>
    </el-table-column>
    <el-table-column v-if="!manage" label="在线时间" width="90" align="center">
      <template #default="{ row }">
        <!-- 点开机天数 = 编辑该服务器 -->
        <span class="uptime-link" title="点击编辑服务器" @click.stop="emit('edit', row)">
          {{ fmtUptimeDays(row.latest?.uptime) }}
        </span>
      </template>
    </el-table-column>
    <!-- 管理模式专属：配置信息列（编辑弹窗内容的只读呈现） -->
    <el-table-column v-if="manage" label="采集方式" width="96" align="center">
      <template #default="{ row }">
        <el-tag size="small" effect="plain" :type="modeTag(row.collect_mode).type">{{ modeTag(row.collect_mode).label }}</el-tag>
      </template>
    </el-table-column>
    <el-table-column v-if="manage" label="Agent 版本" width="132" align="center">
      <template #default="{ row }">
        <!-- 396：agent 版本 + 在线升级（ssh/node 模式无 agent 显示 -） -->
        <span v-if="row.collect_mode !== 'agent' && row.collect_mode !== 'push'" class="muted">-</span>
        <template v-else>
          <div class="ver-cell">
            <span class="mono" :title="row.agent_version || '旧版 agent 未上报版本'">{{ row.agent_version || '旧版' }}</span>
            <el-tooltip v-if="!upgradableIds || upgradableIds.includes(row.id)" content="在线升级到更新通道最新版" placement="top" :hide-after="0">
              <button
                type="button"
                class="op-btn"
                :disabled="!row.agent_version"
                @click.stop="emit('upgrade', row)"
              >
                <el-icon><Top /></el-icon>
              </button>
            </el-tooltip>
          </div>
        </template>
      </template>
    </el-table-column>
    <el-table-column v-if="manage" label="地址" min-width="170" show-overflow-tooltip>
      <template #default="{ row }">
        <span v-if="row.agent_addr" class="mono">{{ row.agent_addr }}</span>
        <span v-else-if="row.collect_mode === 'push'" class="muted">上报模式（无监听地址）</span>
        <span v-else class="muted">-</span>
      </template>
    </el-table-column>
    <el-table-column v-if="manage" label="备注" min-width="160" show-overflow-tooltip>
      <template #default="{ row }">
        <span :title="row.remark">{{ row.remark || '-' }}</span>
      </template>
    </el-table-column>
    <el-table-column label="到期" width="130" align="center">
      <template #default="{ row }">
        <!-- 倒计时：绿条长度=剩余时间占比，越接近到期越短；未配置日期=满条「永久」 -->
        <div class="pbar" :title="expiryTitle(row)">
          <span class="pb-txt ex-txt" :class="`ex-${expiryOf(row).level}`">{{ expiryOf(row).text }}</span>
          <el-progress
            class="pb-bar"
            :percentage="expiryOf(row).percent"
            :stroke-width="8"
            :show-text="false"
            :color="EXPIRY_BAR"
          />
        </div>
      </template>
    </el-table-column>
    <el-table-column label="状态" width="64" align="center" fixed="right">
      <template #default="{ row }">
        <el-tag v-if="manage" :type="row.enabled ? 'success' : 'info'" size="small">
          {{ row.enabled ? '启用' : '停用' }}
        </el-tag>
        <el-tag v-else class="st-tag" :type="row.online ? 'success' : 'info'" size="small">
          {{ row.online ? '在线' : row.enabled ? '离线' : '停用' }}
        </el-tag>
      </template>
    </el-table-column>
    <el-table-column v-if="manage" label="操作" width="118" fixed="right">
      <template #default="{ row }">
        <span class="ops">
          <el-tooltip content="编辑" placement="top" :hide-after="0">
            <button type="button" class="op-btn" @click.stop="emit('edit', row)">
              <el-icon><Edit /></el-icon>
            </button>
          </el-tooltip>
          <el-tooltip :content="row.enabled ? '停用' : '启用'" placement="top" :hide-after="0">
            <button type="button" class="op-btn" @click.stop="emit('toggle', row)">
              <el-icon><TurnOff v-if="row.enabled" /><Open v-else /></el-icon>
            </button>
          </el-tooltip>
          <el-tooltip content="删除" placement="top" :hide-after="0">
            <button type="button" class="op-btn danger" @click.stop="emit('remove', row)">
              <el-icon><Delete /></el-icon>
            </button>
          </el-tooltip>
        </span>
      </template>
    </el-table-column>
  </el-table>
</template>

<script setup>
// 行列表展示：默认尺寸（字号 14、行距宽松），仅点击名称进详情，
// 点"在线时间"编辑服务器；manage 模式（服务器管理页）追加操作列（编辑/停用/删除）。
import { EXPIRY_BAR, expiryInfo, expiryTitle, fmtBps, fmtBytes, fmtDiskBps, fmtPercent, fmtUptimeDays, pct, pickNets, utilClass, withRemark } from '../utils/format.js'
import { barColor } from '../utils/chartTheme.js'
import { Delete, Edit, Open, Top, TurnOff } from '@element-plus/icons-vue'

defineProps({
  servers: { type: Array, required: true },
  manage: { type: Boolean, default: false },
  // 可升级机器 id 集合（/update/agents 的 upgradable，后端 Newer 语义）；
  // null=调用方未提供时保持恒显，传入后仅集合内的行显示升级图标
  upgradableIds: { type: Array, default: null },
})
const emit = defineEmits(['open', 'edit', 'toggle', 'remove', 'upgrade'])

// 采集方式展示（管理模式列）：tag 配色按模式区分
function modeTag(mode) {
  switch (mode) {
    case 'agent': return { label: '拉取', type: 'primary' }
    case 'push': return { label: '推送', type: 'success' }
    case 'node': return { label: 'Exporter', type: 'info' }
    case 'ssh': return { label: 'SSH（已下线）', type: 'warning' }
    default: return { label: mode || '-', type: 'info' }
  }
}

// 主机名列悬停：系统/内核/架构信息
function osTitle(s) {
  return [s.os, s.kernel, s.arch].filter(Boolean).join('  ')
}

// 按服务器配置过滤网口（与 MetricCard 同规则：空=全部）
function shownNets(s) {
  return pickNets(s.latest?.nets, s.net_ifaces)
}

// 磁盘列展示占用最高的一块，悬停看全部
function worstDisk(s) {
  const disks = s.latest?.disks || []
  return disks.reduce((m, d) => (!m || d.percent > m.percent ? d : m), null)
}
function diskTitle(s) {
  return (s.latest?.disks || [])
    .map((d) => `${d.mount}  ${fmtBytes(d.used)}/${fmtBytes(d.total)}（${d.percent.toFixed(1)}%）`)
    .join('\n')
}

// 磁盘读写列：取带速率字段的设备（io_read_bps/io_write_bps，旧数据无此字段），
// 合计展示、悬停看分设备明细（omitempty 下 0 值字段缺失，按 0 参与合计）
function ioDevices(s) {
  return (s.latest?.disks || []).filter((d) => d.io_read_bps != null || d.io_write_bps != null)
}
function diskIOSum(s) {
  return ioDevices(s).reduce(
    (a, d) => ({ r: a.r + (d.io_read_bps || 0), w: a.w + (d.io_write_bps || 0) }),
    { r: 0, w: 0 },
  )
}
function diskIOTitle(s) {
  return ioDevices(s)
    .map((d) => `${d.device || d.mount || '?'}  读 ${fmtDiskBps(d.io_read_bps || 0)}  写 ${fmtDiskBps(d.io_write_bps || 0)}`)
    .join('\n')
}

// 磁盘 IO 列展示最忙的设备（iostat %util），悬停看分设备明细；旧数据无 io_util 时为 null
function worstIO(s) {
  const disks = (s.latest?.disks || []).filter((d) => d.io_util != null)
  return disks.reduce((m, d) => (!m || d.io_util > m.io_util ? d : m), null)
}
function ioTitle(s) {
  return (s.latest?.disks || [])
    .filter((d) => d.io_util != null)
    .map((d) => `${d.mount}（${d.device || '?'}）  IO ${fmtPercent(d.io_util)}`)
    .join('\n')
}

// 网络列展示各口合计，悬停看分口明细
function netSum(s) {
  return shownNets(s).reduce(
    (acc, n) => ({ rx: acc.rx + n.rx_bps, tx: acc.tx + n.tx_bps }),
    { rx: 0, tx: 0 },
  )
}
function netTitle(s) {
  return shownNets(s)
    .map((n) => `${n.iface}  ↓ ${fmtBps(n.rx_bps)}  ↑ ${fmtBps(n.tx_bps)}`)
    .join('\n')
}

// 到期倒计时：绿条长度=剩余时间占比，文字临期变黄/红；未配置=满条「永久」
function expiryOf(s) {
  return expiryInfo(s.purchased_on, s.expires_on)
}
</script>

<style scoped>
/* 默认尺寸表格；行高统一 50（td 定高 + 垂直居中，内容不再撑高） */
.row-table {
  font-size: 14px;
}
.row-table :deep(td.el-table__cell) {
  height: 50px;
  padding: 0;
  vertical-align: middle;
}
/* 数据单元格一律单行（数字/百分比/单位不折行）；名称与主机名列各自截断 */
.row-table :deep(td .cell) {
  white-space: nowrap;
}
.cell-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.t-name {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0; /* flex 子项允许收缩，超长名称走省略号 */
}
/* 名称：唯一进详情入口 */
.t-link {
  cursor: pointer;
}
.t-link:hover {
  color: var(--brand);
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
/* 操作列（manage）：圆形图标按钮 + tooltip（262），删除位危险色 */
.ops {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.op-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  border: 1px solid var(--border);
  background: none;
  color: var(--text-2);
  cursor: pointer;
  transition: border-color 0.15s, color 0.15s;
}
/* 版本列：版本串 + 升级钮同行（396） */
.ver-cell {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.ver-cell .op-btn {
  width: 22px;
  height: 22px;
}
.op-btn:hover {
  border-color: var(--brand);
  color: var(--brand);
}
.op-btn.danger:hover {
  border-color: var(--crit);
  color: var(--crit);
}

/* 网络列：上下行单行不换行 */
.net-cell {
  display: inline-flex;
  gap: 10px;
  white-space: nowrap;
  flex-wrap: nowrap;
  font-variant-numeric: tabular-nums;
}
.tcp-cell {
  font-variant-numeric: tabular-nums;
}
.muted {
  color: var(--muted);
}

/* 进度条单元格：数值 + 条同排 */
.pbar {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
}
.pb-txt {
  width: 44px;
  text-align: right;
  flex: none;
  font-variant-numeric: tabular-nums;
}
.pb-bar {
  flex: 1;
  min-width: 0;
}
/* 到期倒计时：文字比利用率数值长（如「过期 123 天」） */
.ex-txt {
  width: 72px;
}
.ex-warn {
  color: var(--warn-text);
  font-weight: 600;
}
.ex-crit {
  color: var(--crit);
  font-weight: 600;
}

/* 开机天数：点击编辑（悬停下划线提示可点） */
.uptime-link {
  cursor: pointer;
  border-bottom: 1px dashed var(--border);
}
.uptime-link:hover {
  color: var(--brand);
  border-bottom-color: var(--brand);
}

/* 状态（在线）tag：透明度 0.5 基础上增加 25%（0.5×1.25=0.625，2026-10-03 用户指定） */
.st-tag {
  opacity: 0.625;
}
</style>

.mono {
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 12px;
}
