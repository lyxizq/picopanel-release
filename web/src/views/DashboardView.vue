<template>
  <div class="dash">
    <!-- 数据监控：任一服务器的任一数据固定在顶部（最多 16 个；PC 一行 8 个 / 平板 6 个 / 手机 3 个） -->
    <div class="km-sec">
      <div class="km-head">
        <span class="km-title">数据监控</span>
        <span class="km-count">{{ tiles.length }}/16</span>
        <div class="km-tools">
          <el-button size="small" type="primary" :icon="Plus" :disabled="tiles.length >= 16" @click="openTileDlg">
            添加指标
          </el-button>
        </div>
      </div>
      <div v-if="tiles.length" class="km-grid">
        <div
          v-for="(t, i) in tileViews"
          :key="t.id"
          class="km-tile"
          draggable="true"
          :class="{ dragging: tileDrag.from === i, 'drag-over': tileDrag.over === i && tileDrag.from !== i }"
          @click="t.server_id ? openDetail(t.server_id) : undefined"
          @dragstart="tileDrag.onStart(i, $event)"
          @dragenter="tileDrag.onEnter(i, $event)"
          @dragover="tileDrag.onOver(i, $event)"
          @drop="tileDrag.onDrop(i)"
          @dragend="tileDrag.onEnd"
        >
          <el-tooltip content="编辑参数">
            <el-icon class="km-edit" @click.stop="editTile(t)"><Edit /></el-icon>
          </el-tooltip>
          <el-tooltip content="删除指标">
            <el-icon class="km-del" @click.stop="removeTile(t)"><Delete /></el-icon>
          </el-tooltip>
          <div class="km-label" :title="t.label">{{ t.label }}</div>
          <!-- 网络模块：上下行合并一个格子，箭头图标标识（上行橙在前、下行蓝在后），
               双行小字号让格子高度与其他单值模块一致 -->
          <div v-if="t.net" class="km-net">
            <div class="km-net-row">
              <el-icon class="km-arr up"><Top /></el-icon>
              <span>{{ t.net.tx }}</span>
            </div>
            <div class="km-net-row">
              <el-icon class="km-arr down"><Bottom /></el-icon>
              <span>{{ t.net.rx }}</span>
            </div>
          </div>
          <!-- 磁盘读写模块：双行中文字标（写橙、读蓝），单位为硬盘容量口径 -->
          <div v-else-if="t.dio" class="km-net">
            <div class="km-net-row">
              <span class="km-io-tag up">写</span>
              <span>{{ t.dio.w }}</span>
            </div>
            <div class="km-net-row">
              <span class="km-io-tag down">读</span>
              <span>{{ t.dio.r }}</span>
            </div>
          </div>
          <!-- 磁盘组合模块（使用率+读写）：左右布局——左侧读写两行（上读下写），
               右侧竖装进度条显示磁盘容量使用率；底行服务器名+占比同百分比模块 -->
          <div v-else-if="t.drw" class="km-drw">
            <div class="km-drw-body">
              <div class="km-drw-io">
                <div class="km-net-row">
                  <span class="km-io-tag down">读</span>
                  <span class="km-drw-val">{{ t.drw.r }}</span>
                </div>
                <div class="km-net-row">
                  <span class="km-io-tag up">写</span>
                  <span class="km-drw-val">{{ t.drw.w }}</span>
                </div>
              </div>
              <div class="km-drw-bar">
                <div class="km-drw-fill" :style="{ height: barWidth(t.drw.pct), background: barColor(t.drw.pct) }" />
              </div>
            </div>
            <div class="km-bar-foot">
              <span class="km-gsrv" :title="withRemark(t.serverName, t.remark)">{{ t.serverName }}</span>
              <b class="km-bar-val" :class="t.cls">{{ t.value }}</b>
            </div>
          </div>
          <!-- 资源占用组合模块：CPU/内存上下两条进度条（三档渐变），
               占满模块主体高度使格子与其他模块等高；底行服务器名同其他模块 -->
          <div v-else-if="t.cm" class="km-cm">
            <div class="km-cm-row">
              <span class="km-cm-name">CPU</span>
              <div class="km-cm-track">
                <div class="km-cm-fill" :style="{ width: barWidth(t.cm.cpu), background: barColor(t.cm.cpu) }" />
              </div>
              <b class="km-cm-pct" :class="utilClass(t.cm.cpu)">{{ fmtPercent(t.cm.cpu) }}</b>
            </div>
            <div class="km-cm-row">
              <span class="km-cm-name">内存</span>
              <div class="km-cm-track">
                <div class="km-cm-fill" :style="{ width: barWidth(t.cm.mem), background: barColor(t.cm.mem) }" />
              </div>
              <b class="km-cm-pct" :class="utilClass(t.cm.mem)">{{ fmtPercent(t.cm.mem) }}</b>
            </div>
          </div>
          <!-- 百分比模块（CPU/内存/Swap/磁盘/IO）：粗进度条（三档渐变，同列表视图），
               底行左服务器名、右占比（≥70 黄 / ≥90 红，与全站口径一致） -->
          <div v-else-if="t.bar != null" class="km-bar">
            <div class="km-bar-track">
              <div class="km-bar-fill" :style="{ width: barWidth(t.bar), background: barColor(t.bar) }" />
            </div>
            <div class="km-bar-foot">
              <span class="km-gsrv" :title="withRemark(t.serverName, t.remark)">{{ t.serverName }}</span>
              <b class="km-bar-val" :class="t.cls">{{ t.value }}</b>
            </div>
          </div>
          <div v-else class="km-value" :class="t.cls">{{ t.value }}</div>
          <div v-if="t.bar == null && !t.drw" class="km-server" :title="withRemark(t.serverName, t.remark)">{{ t.serverName }}</div>
        </div>
      </div>
      <div v-else class="km-empty">把任意服务器的关键数据固定在这里（最多 16 个，自定义标签）</div>
    </div>

    <!-- 我的关注：任一服务器的任一指标折线（最多 6 个，一行 3 个） -->
    <div class="cc-sec" v-loading="chartsLoading">
      <div class="cc-head">
        <span class="cc-title">我的关注</span>
        <span class="cc-count">{{ charts.length }}/6</span>
        <div class="cc-tools">
          <el-select :model-value="chartRange" size="small" class="cc-range" @update:model-value="onChartRange">
            <el-option v-for="r in DIALOG_RANGES" :key="r.value" :value="r.value" :label="r.label" />
            <el-option :value="-1" label="自定义时间段" />
          </el-select>
          <!-- 自定义时间段：选起止后点「查询」按绝对窗口拉数据（-1 档位等查询按钮） -->
          <template v-if="chartRange === -1">
            <el-date-picker
              v-model="chartWin"
              type="datetimerange"
              size="small"
              class="cc-win"
              start-placeholder="开始时间"
              end-placeholder="结束时间"
              format="YYYY-MM-DD HH:mm"
              value-format="YYYY-MM-DDTHH:mm:ss"
              :clearable="false"
            />
            <el-button size="small" type="primary" :disabled="!chartWin" @click="queryChartWin">查询</el-button>
          </template>
          <el-button size="small" type="primary" :icon="Plus" :disabled="charts.length >= 6" @click="openChartDlg">
            新增图表
          </el-button>
        </div>
      </div>
      <el-row v-if="chartsData.length" :gutter="12">
        <el-col v-for="(c, i) in chartsData" :key="c.id" :xs="24" :sm="12" :lg="8">
          <el-card
            shadow="never"
            class="cc-card"
            draggable="true"
            :class="{ dragging: chartDrag.from === i, 'drag-over': chartDrag.over === i && chartDrag.from !== i }"
            @dragstart="chartDrag.onStart(i, $event)"
            @dragenter="chartDrag.onEnter(i, $event)"
            @dragover="chartDrag.onOver(i, $event)"
            @drop="chartDrag.onDrop(i)"
            @dragend="chartDrag.onEnd"
          >
            <template #header>
              <div class="cc-card-head">
                <!-- 标题可点：跳转该图表所属服务器的详情页 -->
                <span class="cc-card-name" :title="c.name" @click="openDetail(c.server_id)">{{ c.name }}</span>
                <el-tooltip content="删除图表">
                  <el-icon class="cc-del" @click="removeChart(c)"><Delete /></el-icon>
                </el-tooltip>
              </div>
            </template>
            <!-- 网络图：自绘图例，图例后跟近5分钟平均速率 -->
            <div v-if="c.bps" class="cc-legend">
              <span v-for="s in cardSeries[c.id] || []" :key="s.name" class="cc-lg-item">
                <i class="cc-lg-dot" :style="{ background: s.color }" />
                {{ s.name }}
                <b class="cc-lg-avg">{{ s.avg5m != null ? fmtBps(s.avg5m) : '-' }}</b>
              </span>
              <span class="cc-lg-note">近5分钟均值</span>
            </div>
            <!-- 点击图表区域放大查看（弹层内自选时段） -->
            <div class="zoom-wrap" @click="openZoom(c)">
              <SeriesChart
                :series="cardSeries[c.id] || []"
                :percent="c.percent"
                :mirror="c.bps"
                :value-formatter="c.bps ? fmtBpsAbs : undefined"
                :y-axis-formatter="c.bps ? bpsAxisFmt(cardSeries[c.id] || []) : undefined"
                :show-legend="!c.bps"
                :height="c.bps ? '168px' : '201px'"
              />
            </div>
          </el-card>
        </el-col>
      </el-row>
      <!-- 首次加载中显示占位（避免误读为「没有关注内容」的空白）；真正为空才提示 -->
      <div v-else-if="chartsLoading" class="cc-empty" v-loading="true" style="min-height: 160px" element-loading-background="transparent" />
      <div v-else class="cc-empty">还没有关注的内容——点「新增图表」把常用指标固定在这里（最多 6 个）</div>
    </div>

    <!-- 刷新控件 + 展示方式切换：模块卡片 / 行列表 -->
    <div class="view-bar">
      <div class="refresh-ctl">
        <el-button size="small" :icon="Refresh" :loading="loading" @click="load()">刷新</el-button>
        <el-checkbox v-model="autoRefresh" size="small">自动刷新</el-checkbox>
        <el-select v-model="refreshSec" size="small" class="refresh-sel" :disabled="!autoRefresh">
          <el-option :value="10" label="10 秒" />
          <el-option :value="30" label="30 秒" />
          <el-option :value="60" label="60 秒" />
          <el-option :value="120" label="2 分钟" />
          <el-option :value="180" label="3 分钟" />
          <el-option :value="300" label="5 分钟" />
          <el-option :value="600" label="10 分钟" />
        </el-select>
      </div>
      <el-radio-group v-model="viewMode" size="small">
        <el-radio-button value="card">
          <el-icon class="vb-icon"><Grid /></el-icon>模块
        </el-radio-button>
        <el-radio-button value="list">
          <el-icon class="vb-icon"><Menu /></el-icon>列表
        </el-radio-button>
      </el-radio-group>
    </div>

    <el-empty v-if="!loading && servers.length === 0" description="还没有服务器">
      <el-button type="primary" @click="$router.push('/console/admin/servers')">去创建服务器</el-button>
    </el-empty>

    <!-- 无分组：平铺展示，不套手风琴 -->
    <template v-if="flatSection">
      <ServerGrid v-if="viewMode === 'card'" :servers="flatSection.servers" @open="openDetail" @edit="editServer" />
      <ServerRows v-else :servers="flatSection.servers" @open="openDetail" @edit="editServer" />
    </template>

    <!-- 有分组：每组一个独立卡片，彻底分离，表头分组色底、点击折叠 -->
    <div v-for="(sec, gi) in sections" :key="sec.key" class="grp">
      <div class="grp-head" :style="{ background: headBg(grpColor(gi)) }" @click="toggle(sec.key)">
        <span class="grp-accent" :style="{ background: grpColor(gi) }" />
        <span class="grp-name" :style="{ color: grpColor(gi) }">{{ sec.name }}</span>
        <span class="grp-stats">
          <span>共 {{ sec.servers.length }} 台</span>
          <span class="ok">在线 {{ onlineOf(sec) }}</span>
          <span :class="{ off: offlineOf(sec) > 0 }">离线 {{ offlineOf(sec) }}</span>
          <span :class="{ bad: firingOf(sec) > 0 }">告警 {{ firingOf(sec) }}</span>
        </span>
        <el-icon class="grp-arrow" :class="{ open: isOpen(sec.key) }"><ArrowDown /></el-icon>
      </div>
      <div v-show="isOpen(sec.key)" class="grp-body">
        <ServerGrid v-if="viewMode === 'card'" :servers="sec.servers" @open="openDetail" @edit="editServer" />
        <ServerRows v-else :servers="sec.servers" @open="openDetail" @edit="editServer" />
      </div>
    </div>

    <!-- 编辑服务器（卡片三点菜单 / 列表点开机天数进入） -->
    <ServerEditDialog ref="editDlg" @saved="load" />

    <!-- 图表放大弹层（点击图表区域打开） -->
    <ChartZoomDialog ref="zoomDlg" />

    <!-- 新增/编辑重点指标 -->
    <el-dialog append-to-body v-model="tileDlg" :title="editingTileId ? '编辑数据监控' : '添加数据监控'" width="480px" :before-close="tileGuard.beforeClose">
      <el-form label-width="80px" @submit.prevent="saveTile">
        <el-form-item label="数据源">
          <el-radio-group v-model="tileForm.src">
            <el-radio-button value="server">服务器</el-radio-button>
            <el-radio-button value="ai">用量账户</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="tileForm.src === 'server'" label="服务器">
          <el-select v-model="tileForm.server_id" placeholder="选择服务器" filterable style="width: 100%">
            <el-option v-for="s in servers" :key="s.id" :value="s.id" :label="s.name" />
          </el-select>
        </el-form-item>
        <el-form-item v-else label="用量账户">
          <el-select v-model="tileForm.ai_id" placeholder="选择用量查询账户" filterable style="width: 100%">
            <el-option v-for="a in tileAccOptions" :key="a.id" :value="a.id" :label="a.label" />
          </el-select>
          <div v-if="!tileAccOptions.length" class="cc-hint">尚未配置用量查询账户，请先到「用量查询」页添加</div>
        </el-form-item>
        <el-form-item label="指标">
          <el-select v-model="tileForm.metric" placeholder="选择指标" style="width: 100%">
            <el-option v-for="m in tileMetricOptions" :key="m.value" :value="m.value" :label="m.label" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="tileParamKind" :label="{ disk: '挂载点', net: '网口', temp: '传感器' }[tileParamKind]">
          <el-select v-model="tileForm.param" style="width: 100%">
            <el-option value="" :label="{ disk: '最大值（任一挂载点）', net: '展示网口合计', temp: 'CPU 温度（无则取最高）' }[tileParamKind]" />
            <el-option v-for="p in tileParamOptions" :key="p" :value="p" :label="p" />
          </el-select>
          <div v-if="!tileParamOptions.length" class="cc-hint">该服务器暂无采集数据，取不到列表</div>
        </el-form-item>
        <el-form-item label="标签">
          <el-input v-model="tileForm.label" placeholder="展示名称，如「官网 CPU」" maxlength="30" show-word-limit @keyup.enter="saveTile" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="tileDlg = false">取消</el-button>
        <el-button type="primary" :loading="tileSaving" @click="saveTile">{{ editingTileId ? '保存' : '创建' }}</el-button>
      </template>
    </el-dialog>

    <!-- 新增自定义图表 -->
    <el-dialog append-to-body v-model="chartDlg" title="新增图表" width="480px" :before-close="chartGuard.beforeClose">
      <el-form label-width="80px" @submit.prevent="addChart">
        <el-form-item label="服务器">
          <el-select v-model="chartForm.server_id" placeholder="选择服务器" filterable style="width: 100%">
            <el-option v-for="s in servers" :key="s.id" :value="s.id" :label="s.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="指标">
          <el-select v-model="chartForm.metric" placeholder="选择指标" style="width: 100%">
            <el-option v-for="m in CHART_METRICS" :key="m.value" :value="m.value" :label="m.label" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="chartForm.metric === 'net'" label="网口">
          <el-select v-model="chartForm.param" placeholder="选择网口" style="width: 100%">
            <el-option v-for="n in paramOptions" :key="n" :value="n" :label="n" />
          </el-select>
          <div v-if="!paramOptions.length" class="cc-hint">该服务器暂无采集数据，取不到网口列表</div>
        </el-form-item>
        <el-form-item v-else-if="chartForm.metric === 'temp'" label="传感器">
          <el-select v-model="chartForm.param" placeholder="CPU 温度" clearable style="width: 100%">
            <el-option value="" label="CPU 温度（缺省）" />
            <el-option v-for="p in paramOptions" :key="p" :value="p" :label="p" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="chartDlg = false">取消</el-button>
        <el-button type="primary" :loading="chartSaving" @click="addChart">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
// keep-alive 按组件名缓存（tabs.js 的 cachedNames 引用）
defineOptions({ name: 'DashboardView' })
import { computed, nextTick, onActivated, onDeactivated, onMounted, onUnmounted, ref, watch } from 'vue'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Bottom, Delete, Edit, Plus, Refresh, Top } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { openServerDetail } from '../serverDetail.js'
import { bpsAxisFmt, fmtBps, fmtBpsAbs, fmtPercent, utilClass, withRemark } from '../utils/format.js'
import { useDragSort } from '../composables/useDragSort.js'
import { computeTileViews } from '../composables/useTileViews.js'
import { AI_PROV_LABELS, TILE_METRICS } from '../utils/tileMetrics.js'
import { DIALOG_RANGES, NET_DOWN_COLOR, NET_UP_COLOR, barColor, grpColor, snapRange } from '../utils/chartTheme.js'
import ServerGrid from '../components/ServerGrid.vue'
import ServerRows from '../components/ServerRows.vue'
import ServerEditDialog from '../components/ServerEditDialog.vue'
import SeriesChart from '../components/SeriesChart.vue'
import ChartZoomDialog from '../components/ChartZoomDialog.vue'

// 自定义图表可选指标；needsParam: net=网口（上下行合并双线展示）。
// temp=温度（param 选传感器，可空=CPU）。磁盘占用率仅存最新、无历史曲线，不支持图表
const CHART_METRICS = [
  { value: 'cpu', label: 'CPU 使用率' },
  { value: 'mem', label: '内存使用率' },
  { value: 'swap', label: '交换分区使用率' },
  { value: 'load1', label: '负载 load1' },
  { value: 'load5', label: '负载 load5' },
  { value: 'load15', label: '负载 load15' },
  { value: 'tcp_est', label: 'TCP 连接数' },
  { value: 'net', label: '网卡速率（上下行）', needsParam: true },
  { value: 'temp', label: '温度（℃）', tempParam: true },
]

const servers = ref([])
const groups = ref([])
const loading = ref(false)

// 展示方式：模块 / 列表（记忆到本地，下次打开保持上次选择）
const viewMode = ref(localStorage.getItem('dash-view') || 'card')
watch(viewMode, (v) => localStorage.setItem('dash-view', v))

// 分组区段：有分组时每组一节（空分组也展示，便于规划），未分组的服务器单独一节。
// 分组按名称排序（中文按拼音序），未分组固定最后。
const sections = computed(() => {
  const secs = groups.value
    .map((g) => ({ key: 'g' + g.id, id: g.id, name: g.name, servers: [] }))
    .sort((a, b) => a.name.localeCompare(b.name, 'zh-Hans-CN'))
  const byId = new Map(secs.map((s) => [s.id, s]))
  const ungrouped = { key: 'g0', id: 0, name: '未分组', servers: [] }
  for (const s of servers.value) {
    const sec = byId.get(s.group_id)
    ;(sec || ungrouped).servers.push(s)
  }
  if (ungrouped.servers.length) secs.push(ungrouped)
  return secs
})

// 一个分组都没有时整体平铺（不套手风琴）
const flatSection = computed(() =>
  groups.value.length === 0 && servers.value.length > 0
    ? { key: 'all', name: '', servers: servers.value }
    : null,
)

// 分组折叠状态：默认全部展开；分组集合变化时新组默认展开
const openMap = ref({})
watch(
  () => sections.value.map((s) => s.key).join('|'),
  () => {
    const m = {}
    for (const s of sections.value) m[s.key] = true
    openMap.value = m
  },
  { immediate: true },
)
function isOpen(key) {
  return openMap.value[key] !== false
}
function toggle(key) {
  openMap.value[key] = !isOpen(key)
}

// 表头底色：分组色的低透明度浅底（明暗主题都成立）
function headBg(hex) {
  const n = parseInt(hex.slice(1), 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, 0.12)`
}

// 分组表头统计（在线/离线/告警）沿用同一组口径
function onlineOf(sec) {
  return sec.servers.filter((s) => s.online).length
}
// 离线 = 不在线且未停用（停用机器不计入）
function offlineOf(sec) {
  return sec.servers.filter((s) => !s.online && s.enabled).length
}
function firingOf(sec) {
  return sec.servers.reduce((n, s) => n + (s.firing || 0), 0)
}

// tooltip 序列名去掉服务器名前缀：卡片标题已标明所属服务器，
// tip 里只留指标名（后端单序列名为「服务器 · 指标」，取最后一段）
function tipName(n) {
  return n.includes(' · ') ? n.split(' · ').pop() : n
}

// 图表序列构造（页内卡片与放大弹层共用）：上行在前、下行在后——序列顺序即
// tooltip/图例顺序；线色按方向钉住（下行蓝/上行橙，与数据监控格子的箭头一致，
// 仅网络图钉色，其余图表走默认色板）；镜像图下行取负画向 0 轴下方
function chartSeries(c) {
  return [...c.series]
    .sort((a, b) => (a.name.includes('下行') ? 1 : 0) - (b.name.includes('下行') ? 1 : 0))
    .map((s) => {
      const down = s.name.includes('下行')
      return {
        name: tipName(s.name || c.name),
        avg5m: s.avg5m,
        points: c.bps && down ? s.points.map(([t, v]) => [t, -v]) : s.points,
        color: c.bps ? (down ? NET_DOWN_COLOR : NET_UP_COLOR) : undefined,
      }
    })
}

// 序列按卡片缓存：模板三处取值共用同一数组引用，父级重渲染不再每次重建
// 序列（旧写法每次新引用，SeriesChart 的 watch 每轮全部图表全量重绘）
const cardSeries = computed(() => {
  const m = {}
  for (const c of chartsData.value) m[c.id] = chartSeries(c)
  return m
})

// 刷新：手动按钮 + 可勾选的自动刷新（间隔 10 秒 ~ 10 分钟，缺省 60 秒），选择记住。
// 键名换过一次（dash-interval → dash-refresh-sec）：旧版默认 30 秒的记忆值作废，一律回到 60
const autoRefresh = ref(localStorage.getItem('dash-auto') !== '0')
const refreshSec = ref(Number(localStorage.getItem('dash-refresh-sec')) || 60)
watch(autoRefresh, (v) => localStorage.setItem('dash-auto', v ? '1' : '0'))
watch(refreshSec, (v) => localStorage.setItem('dash-refresh-sec', String(v)))

let timer = null
function resetTimer() {
  if (timer) clearInterval(timer)
  timer = null
  if (autoRefresh.value) timer = setInterval(load, refreshSec.value * 1000)
}
watch([autoRefresh, refreshSec], resetTimer)

const editDlg = ref(null)

// ---- 重点指标（首页顶部，最多 16 个；数值由 /servers 或用量账户最新快照计算）----
const tiles = ref([])
const tileDlg = ref(false)
const tileSaving = ref(false)
const tileForm = ref({ src: 'server', server_id: null, ai_id: null, metric: '', param: '', label: '' })
const tileGuard = useDirtyGuard(() => ({ ...tileForm.value }))

// 用量查询账户（486 用量格子数据源；与快照同节拍随 load() 刷新）
const aiAccounts = ref([])
const tileAccOptions = computed(() =>
  aiAccounts.value.map((a) => ({ id: a.id, label: `${a.name}（${AI_PROV_LABELS[a.provider] || a.provider}）`, provider: a.provider })))
const tileMetricOptions = computed(() => {
  if (tileForm.value.src !== 'ai') return TILE_METRICS.filter((m) => !m.prov)
  const prov = aiAccounts.value.find((a) => a.id === tileForm.value.ai_id)?.provider
  return TILE_METRICS.filter((m) => m.prov && m.prov === prov)
})

// param 下拉选项：disk/io→挂载点列表；net→网口列表（来自所选服务器最新快照）
const tileSel = computed(() => servers.value.find((s) => s.id === tileForm.value.server_id))
const tileParamKind = computed(() => TILE_METRICS.find((m) => m.value === tileForm.value.metric)?.kind || '')
const tileParamOptions = computed(() => {
  const latest = tileSel.value?.latest
  if (!latest) return []
  if (tileParamKind.value === 'disk') return (latest.disks || []).map((d) => d.mount)
  if (tileParamKind.value === 'net') return (latest.nets || []).map((n) => n.iface)
  if (tileParamKind.value === 'temp') return (latest.temps || []).map((x) => x.sensor)
  return []
})
// 换服务器/换指标/换数据源或账户后 param（及指标）重置（编辑回填期间跳过，避免清掉回填值）
let tileFilling = false
watch(() => tileForm.value.server_id, () => {
  if (!tileFilling) tileForm.value.param = ''
})
watch(() => tileForm.value.metric, () => {
  if (!tileFilling) tileForm.value.param = ''
})
watch(() => tileForm.value.src, () => {
  if (tileFilling) return
  tileForm.value.metric = ''
  tileForm.value.param = ''
})
watch(() => tileForm.value.ai_id, () => {
  if (tileFilling) return
  tileForm.value.metric = ''
})

const editingTileId = ref(0) // 0=新增

function openTileDlg() {
  tileFilling = false
  editingTileId.value = 0
  tileForm.value = { src: 'server', server_id: null, ai_id: null, metric: '', param: '', label: '' }
  tileDlg.value = true
  tileGuard.snapshot()
}

// 编辑已有指标：回填当前配置（回填期间不触发重置）
function editTile(t) {
  const src = tiles.value.find((x) => x.id === t.id)
  if (!src) return
  tileFilling = true
  editingTileId.value = src.id
  tileForm.value = {
    src: src.ai_id ? 'ai' : 'server',
    server_id: src.ai_id ? null : src.server_id,
    ai_id: src.ai_id || null,
    metric: src.metric, param: src.param || '', label: src.label,
  }
  nextTick(() => (tileFilling = false))
  tileDlg.value = true
  tileGuard.snapshot()
}

async function saveTile() {
  const f = tileForm.value
  if (f.src === 'ai' ? !f.ai_id : !f.server_id) return ElMessageBox.alert(f.src === 'ai' ? '请选择用量账户' : '请选择服务器', '提示', { type: 'warning' })
  if (!f.metric) return ElMessageBox.alert('请选择指标', '提示', { type: 'warning' })
  if (!f.label.trim()) return ElMessageBox.alert('请填写展示标签', '提示', { type: 'warning' })
  tileSaving.value = true
  try {
    const body = {
      server_id: f.src === 'server' ? f.server_id : 0,
      ai_id: f.src === 'ai' ? f.ai_id : 0,
      metric: f.metric, param: f.param, label: f.label.trim(),
    }
    if (editingTileId.value) {
      await api.put(`/dashboard-metrics/${editingTileId.value}`, body)
    } else {
      await api.post('/dashboard-metrics', body)
    }
    tileDlg.value = false
    await loadTiles()
  } catch (e) {
    ElMessageBox.alert(e.message, editingTileId.value ? '保存失败' : '添加失败', { type: 'error' })
  } finally {
    tileSaving.value = false
  }
}

async function removeTile(t) {
  try {
    await ElMessageBox.confirm(`删除「${t.label}」？`, '删除指标', { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.del(`/dashboard-metrics/${t.id}`)
  } catch (e) {
    ElMessageBox.alert(e.message, '删除失败', { type: 'error' })
  }
  await loadTiles()
}

async function loadTiles() {
  try {
    const res = await api.get('/dashboard-metrics')
    tiles.value = res.metrics || []
  } catch {
    /* ignore */
  }
}

// 格子拖拽排序：本地立即重排（tileViews 随 tiles 计算），顺序保存到后端
const tileDrag = useDragSort(tiles, async (ids) => {
  try {
    await api.put('/dashboard-metrics/order', { ids })
  } catch (e) {
    ElMessage.error('保存顺序失败：' + e.message)
  }
})

// 百分比模块进度条宽度：夹紧 0–100，异常值不撑破格子
function barWidth(pct) {
  return `${Math.max(0, Math.min(pct || 0, 100))}%`
}

// 格子展示数据：配置 + 最新快照（服务器/用量账户）→ { 标签, 格式化值, 配色, 来源名 }
// （取数计算在 composables/useTileViews.js，与个人主页 /page 监控区共用）
const tileViews = computed(() => computeTileViews(tiles.value, servers.value, aiAccounts.value))

// ---- 自定义图表（统计条与模块列表之间）----
const charts = ref([]) // 配置列表（数量上限展示用）
const chartsData = ref([]) // 含曲线点的展示数据
const chartRange = ref(Number(localStorage.getItem('dash-chart-range')) || 43200)
const chartCustom = ref(null) // 自定义时间段窗口 {from,to} 秒（选回预设档时清空）
const chartWin = ref(null) // datetimerange 绑定值
const chartDlg = ref(false)
const chartSaving = ref(false)
const chartForm = ref({ server_id: null, metric: '', param: '' })
const chartGuard = useDirtyGuard(() => ({ ...chartForm.value }))

// 网卡→网口列表（来自所选服务器最新快照）
const selServer = computed(() => servers.value.find((s) => s.id === chartForm.value.server_id))
const paramOptions = computed(() => {
  const latest = selServer.value?.latest
  if (!latest) return []
  if (chartForm.value.metric === 'net') return (latest.nets || []).map((n) => n.iface)
  if (chartForm.value.metric === 'temp') return (latest.temps || []).map((x) => x.sensor)
  return []
})
// 换服务器/换指标后 param 必须重选
watch(() => chartForm.value.server_id, () => (chartForm.value.param = ''))
watch(() => chartForm.value.metric, () => (chartForm.value.param = ''))

function openChartDlg() {
  chartForm.value = { server_id: null, metric: '', param: '' }
  chartDlg.value = true
  chartGuard.snapshot()
}

async function addChart() {
  const f = chartForm.value
  if (!f.server_id) return ElMessageBox.alert('请选择服务器', '提示', { type: 'warning' })
  if (!f.metric) return ElMessageBox.alert('请选择指标', '提示', { type: 'warning' })
  const needs = CHART_METRICS.find((m) => m.value === f.metric)?.needsParam
  if (needs && !f.param) {
    return ElMessageBox.alert('请选择网口', '提示', { type: 'warning' })
  }
  chartSaving.value = true
  try {
    await api.post('/dashboard-charts', { server_id: f.server_id, metric: f.metric, param: f.param })
    chartDlg.value = false
    await Promise.all([loadCharts(), loadChartsData()])
  } catch (e) {
    ElMessageBox.alert(e.message, '创建失败', { type: 'error' })
  } finally {
    chartSaving.value = false
  }
}

async function removeChart(c) {
  try {
    await ElMessageBox.confirm(`删除图表「${c.name}」？`, '删除图表', { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.del(`/dashboard-charts/${c.id}`)
  } catch (e) {
    ElMessageBox.alert(e.message, '删除失败', { type: 'error' })
  }
  await Promise.all([loadCharts(), loadChartsData()])
}

async function loadCharts() {
  try {
    const res = await api.get('/dashboard-charts')
    charts.value = res.charts || []
  } catch {
    /* ignore */
  }
}

// 图表曲线加载：仅切换时段时显示遮罩（showLoading）；自动刷新/首次进入/
// 增删图表静默更新，避免周期性闪烁。序号守卫防竞态——晚到的旧响应丢弃；
// 最新一次请求结束即撤遮罩（即使它是静默请求，此时数据已更新）。
const chartsLoading = ref(false)
let chartReqSeq = 0
async function loadChartsData(showLoading = false) {
  if (chartRange.value === -1 && !chartCustom.value) return // 自定义档：等「查询」按钮
  const seq = ++chartReqSeq
  if (showLoading) chartsLoading.value = true
  try {
    const q = chartCustom.value
      ? `from=${chartCustom.value.from}&to=${chartCustom.value.to}`
      : `range=${chartRange.value}`
    const res = await api.get(`/dashboard-charts/data?${q}`)
    if (seq !== chartReqSeq) return
    chartsData.value = res.charts || []
  } catch {
    /* ignore */
  } finally {
    if (seq === chartReqSeq) chartsLoading.value = false
  }
}
// 预设档：记住选择并重拉（自定义档 -1 不入库，等「查询」按钮）
function onChartRange(v) {
  chartRange.value = v
  if (v === -1) return
  chartCustom.value = null
  localStorage.setItem('dash-chart-range', String(v))
  loadChartsData(true)
}
function queryChartWin() {
  if (!chartWin.value || chartWin.value.length !== 2) return
  const from = Math.floor(new Date(chartWin.value[0]).getTime() / 1000)
  const to = Math.floor(new Date(chartWin.value[1]).getTime() / 1000)
  if (!(from < to)) {
    ElMessage.warning('开始时间需早于结束时间')
    return
  }
  chartCustom.value = { from, to }
  loadChartsData(true)
}

// 图表卡片拖拽排序：重排 chartsData 并让 charts（配置列表）同步同序，顺序保存到后端
const chartDrag = useDragSort(chartsData, async (ids) => {
  const m = new Map(charts.value.map((c) => [c.id, c]))
  charts.value = ids.map((id) => m.get(id)).filter(Boolean)
  try {
    await api.put('/dashboard-charts/order', { ids })
  } catch (e) {
    ElMessage.error('保存顺序失败：' + e.message)
  }
})

// ---------- 图表放大弹层 ----------

const zoomDlg = ref(null)

// 点击图表区域放大：按弹层所选时段重新拉全部图表数据，按 id 找回该图，
// 与页内渲染走同一个 chartSeries（顺序/钉色/取负完全同构）
function openZoom(c) {
  zoomDlg.value.open({
    title: c.name,
    percent: c.percent,
    bps: c.bps,
    initialRange: snapRange(chartRange.value), // 自定义档(-1)向下取档打开，弹层内可再选自定义
    loader: async (r, win) => {
      const q = win ? `from=${win.from}&to=${win.to}` : `range=${r}`
      const res = await api.get(`/dashboard-charts/data?${q}`)
      const c2 = (res.charts || []).find((x) => x.id === c.id)
      return c2 ? chartSeries(c2) : []
    },
  })
}

async function load() {
  if (document.hidden) return // 后台标签页跳过：轮询到点但不打请求，切回时 activated 已补刷
  loading.value = true
  try {
    const [res, gres, ares] = await Promise.all([api.get('/servers'), api.get('/groups'), loadChartsData(), api.get('/aiusage/accounts')])
    // 按名称正序（中文按拼音、数字按数值，忽略大小写）：模块/列表/分组/下拉共用此顺序
    servers.value = (res.servers || []).sort((a, b) =>
      a.name.localeCompare(b.name, 'zh-Hans-CN', { numeric: true, sensitivity: 'base' }),
    )
    groups.value = gres.groups || []
    aiAccounts.value = ares.accounts || []
  } catch {
    /* 401 已由 api 层跳转 */
  } finally {
    loading.value = false
  }
}

function openDetail(id) {
  openServerDetail(id)
}

function editServer(row) {
  editDlg.value.open(row)
}

onMounted(() => {
  load() // 447 回归修复：keep-alive 重构时丢失——首开 servers/图表数据无人拉，磁贴值全"-"、列表空，切走再切回才出现
  loadTiles()
  loadCharts()
  resetTimer()
})
// keep-alive 下首挂载 onActivated 也会触发：用标记跳过，避免 mounted/activated
// 双请求；切回本页时补一次即时刷新并恢复轮询；切走/隐藏即停（保活期间定时器
// 不再空转打请求）
let mountedJustNow = true
onActivated(() => {
  if (mountedJustNow) {
    mountedJustNow = false
    return
  }
  resetTimer()
  load()
  loadTiles()
})
onDeactivated(() => {
  if (timer) clearInterval(timer)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<style scoped>
/* 与其他页一致：边距只由 Layout 的 .main（6px）提供，不再额外垫 padding；
   图表栅格 gutter 的 -6px 负外边距自行抵消首尾列内边距，天然与其他内容对齐 */
/* 重点指标区：首页顶部；PC 一行 8 个、手机一行 4 个 */
.km-sec {
  margin-bottom: 14px;
}
.km-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.km-title {
  font-weight: 600;
}
.km-count {
  color: var(--muted);
  font-size: 12px;
}
.km-tools {
  margin-left: auto;
}
.km-grid {
  display: grid;
  grid-template-columns: repeat(8, 1fr);
  grid-auto-rows: 100px; /* 所有格子统一高度，混合模块类型也整齐 */
  gap: 10px;
}
.km-tile {
  position: relative;
  display: flex; /* 内部各段弹性分布，把统一行高填满 */
  flex-direction: column;
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px 8px;
  cursor: pointer;
  min-width: 0;
  transition: border-color 0.2s;
}
.km-tile:hover {
  border-color: var(--brand);
}
/* 拖拽排序反馈：拖动中的格子半透明，悬停目标加粗描边 */
.km-tile.dragging {
  opacity: 0.45;
}
.km-tile.drag-over {
  border-color: var(--brand);
  box-shadow: inset 0 0 0 1px var(--brand);
}
.km-label {
  font-size: 12px;
  color: var(--muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding-right: 14px;
}
.km-value {
  flex: 1; /* 大数值区垂直居中填满，底行服务器名对齐其他模块 */
  display: flex;
  align-items: center;
  font-size: 20px;
  font-weight: 600;
  color: var(--ink);
  margin: 4px 0 2px;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.km-server {
  margin-top: auto; /* 底行固定贴底，各模块底边整齐 */
  padding-top: 2px;
  font-size: 12px;
  color: var(--text-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 网络模块：上下行两行小字号（12px×2 ≈ 单值 20px 一行的高度，格子不比别人高），
   箭头颜色与折线图/图例同色系（下行品牌蓝、上行橙） */
.km-net {
  flex: 1; /* 双行区弹性居中填满 */
  margin: 3px 0 2px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 1px;
}
.km-net-row {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  font-weight: 600;
  color: var(--ink);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  line-height: 1.2;
}
.km-arr {
  font-size: 10px;
  flex: none;
}
.km-arr.down {
  color: var(--brand);
}
.km-arr.up {
  color: v-bind('NET_UP_COLOR');
}
/* 磁盘读写格子中文字标：写橙/读蓝，与网络箭头同色系 */
.km-io-tag {
  font-size: 10px;
  flex: none;
  width: 14px;
  text-align: center;
}
.km-io-tag.down {
  color: var(--brand);
}
.km-io-tag.up {
  color: v-bind('NET_UP_COLOR');
}
/* 磁盘组合格子：左右布局——左侧读写两行（上读/下写，字标配色同磁盘读写格子），
   右侧竖装进度条（容量使用率，三档渐变自下而上填充）；底行 foot 复用百分比模块 */
.km-drw-body {
  flex: 1; /* 组合区弹性填满，与其他模块等高 */
  display: flex;
  align-items: stretch;
  gap: 10px;
  margin: 4px 0 2px;
}
.km-drw-io {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 3px;
}
.km-drw-val {
  font-size: 12px;
  font-weight: 600;
  color: var(--ink);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.km-drw-bar {
  width: 8px;
  min-height: 40px;
  border-radius: 4px;
  background: var(--border);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  flex: none;
}
.km-drw-fill {
  width: 100%;
  border-radius: 4px;
  transition: height 0.3s;
}
/* 百分比模块进度条：细条（10px）+ 三档渐变（barColor，同列表视图） */
.km-bar {
  flex: 1; /* 进度条区弹性居中填满 */
  margin: 6px 0 2px;
  display: flex;
  flex-direction: column;
  justify-content: center;
}
.km-bar-track {
  position: relative;
  height: 10px;
  border-radius: 5px;
  background: var(--border);
  overflow: hidden;
}
.km-bar-fill {
  height: 100%;
  border-radius: 5px;
  transition: width 0.3s;
}
.km-bar-foot {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 6px;
  margin-top: 4px;
  min-width: 0;
}
.km-gsrv {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--text-2);
}
.km-bar-val {
  font-size: 14px;
  font-weight: 700;
  color: var(--ink);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
/* 删除按钮：悬停格子时显形 */
.km-del {
  position: absolute;
  top: 6px;
  right: 6px;
  font-size: 14px;
  color: var(--muted);
  opacity: 0;
  cursor: pointer;
}
.km-edit {
  position: absolute;
  top: 6px;
  right: 24px;
  font-size: 14px;
  color: var(--muted);
  opacity: 0;
  cursor: pointer;
}
.km-tile:hover .km-del,
.km-tile:hover .km-edit {
  opacity: 1;
}
.km-del:hover {
  color: var(--crit);
}
.km-edit:hover {
  color: var(--brand);
}
.km-empty {
  color: var(--muted);
  font-size: 13px;
  padding: 8px 0 4px;
}
/* 资源占用组合模块：CPU/内存上下两条进度条均分模块主体，格子高度与其他模块一致 */
.km-cm {
  flex: 1; /* 双进度条区弹性填满，与其他模块等高 */
  margin: 4px 0 2px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 8px;
}
.km-cm-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.km-cm-name {
  flex: none;
  font-size: 12px;
  color: var(--muted);
}
.km-cm-track {
  flex: 1;
  height: 8px;
  border-radius: 4px;
  background: var(--border);
  overflow: hidden;
}
.km-cm-fill {
  height: 100%;
  transition: width 0.3s;
}
.km-cm-pct {
  flex: none;
  min-width: 44px;
  text-align: right;
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.view-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 12px;
}
.refresh-ctl {
  display: flex;
  align-items: center;
  gap: 10px;
}
.refresh-sel {
  width: 88px;
}

/* 自定义图表区：统计条与模块列表之间 */
.cc-sec {
  margin-bottom: 14px;
}
.cc-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}
.cc-title {
  font-weight: 600;
}
.cc-count {
  color: var(--muted);
  font-size: 12px;
}
.cc-tools {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
}
.cc-range {
  width: 120px;
}
.cc-win.el-date-editor {
  width: 290px !important; /* 刚好容纳两段「YYYY-MM-DD HH:mm」完整日期时间（内嵌输入宽度见文末全局规则） */
  flex: none !important;
}
.cc-card {
  margin-bottom: 12px;
}
.cc-card.dragging {
  opacity: 0.45;
}
.cc-card.drag-over {
  border-color: var(--brand);
  box-shadow: inset 0 0 0 1px var(--brand);
}
/* 图表区域点击放大 */
.zoom-wrap {
  cursor: zoom-in;
}
/* 图表卡片表头：黑体加粗，行高 40px；内边距压低把高度留给折线图 */
.cc-card :deep(.el-card__header) {
  padding: 2px 8px;
  font-size: 13px;
  font-weight: 600;
  line-height: 40px;
}
.cc-card :deep(.el-card__body) {
  padding: 4px 8px;
}
.cc-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.cc-card-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  cursor: pointer;
}
/* 标题可点进服务器详情：hover 变色提示 */
.cc-card-name:hover {
  color: var(--brand);
}
.cc-del {
  flex: none;
  color: var(--muted);
  cursor: pointer;
}
.cc-del:hover {
  color: var(--crit);
}
.cc-empty {
  color: var(--muted);
  font-size: 13px;
  padding: 14px;
  text-align: center;
  border: 1px dashed var(--border);
  border-radius: 8px;
}
.cc-hint {
  color: var(--muted);
  font-size: 12px;
  margin-top: 4px;
}
/* 网络图自绘图例：色点 + 名称 + 近5分钟均值。
   行高固定 22px，网络图（镜像双线）图体 168px、普通图 201px，
   图例 22px 由网络图体吸收，两类卡片总高一致 */
.cc-legend {
  display: flex;
  align-items: center;
  gap: 14px;
  font-size: 12px;
  line-height: 18px;
  height: 22px;
  color: var(--text-2);
  padding: 0 2px;
}
.cc-lg-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.cc-lg-dot {
  width: 10px;
  height: 4px;
  border-radius: 1px;
  flex: none;
}
.cc-lg-avg {
  font-weight: 600;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}
.cc-lg-note {
  margin-left: auto;
  color: var(--muted);
  font-size: 11px;
  flex: none;
}
.vb-icon {
  margin-right: 4px;
  vertical-align: -2px;
}

/* 分组独立卡片：组间彻底分离，表头分组色底、可点击折叠 */
.grp {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-bottom: 14px;
  overflow: hidden;
}
.grp-head {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px 14px;
  min-width: 0;
  cursor: pointer;
  user-select: none;
}
/* 表头色条：颜色随分组循环 */
.grp-accent {
  width: 4px;
  height: 14px;
  border-radius: 2px;
  flex: none;
}
.grp-name {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.grp-stats {
  margin-left: auto;
  display: flex;
  gap: 12px;
  color: var(--muted);
  font-size: 12px;
  white-space: nowrap;
  flex: none;
}
.grp-stats .ok {
  color: var(--ok);
}
.grp-stats .off {
  color: var(--warn-text);
  font-weight: 600;
}
.grp-stats .bad {
  color: var(--crit);
  font-weight: 600;
}
.grp-arrow {
  flex: none;
  margin-left: 4px;
  color: var(--muted);
  transition: transform 0.2s;
}
.grp-arrow.open {
  transform: rotate(180deg);
}
.grp-body {
  padding: 12px 14px;
  border-top: 1px solid var(--border);
}

/* 平板：一行 6 个（桌面 8 与手机 3 之间） */
@media (min-width: 769px) and (max-width: 1024px) {
  .km-grid {
    grid-template-columns: repeat(6, 1fr);
  }
}

/* 手机端：窄屏不需要大留白，边距只由 Layout 的 .main（3px）提供 */
@media (max-width: 768px) {
  /* 重点指标：手机一行 4 个（行高统一略矮于桌面） */
  .km-grid {
    grid-template-columns: repeat(4, 1fr);
    gap: 8px;
    grid-auto-rows: 92px;
  }
  .km-value {
    font-size: 17px;
  }
  /* 窄格防遮挡：内边距与字号压缩，资源占用/磁盘组合/进度条区降高，
     保证底行服务器名完整可见（格子 92px 预算：label 15 + 主体 + 底行 17） */
  .km-tile {
    padding: 8px 9px 6px;
  }
  .km-label {
    font-size: 11px;
  }
  .km-cm {
    gap: 4px;
    margin: 2px 0 0;
  }
  .km-cm-track {
    height: 8px;
    border-radius: 4px;
  }
  .km-drw-bar {
    min-height: 32px;
  }
  .km-bar-track {
    height: 8px;
    border-radius: 4px;
  }
  .grp-stats {
    gap: 8px;
    font-size: 11px;
  }
  /* 窄屏：刷新控件与视图切换允许换行堆叠 */
  .view-bar {
    flex-wrap: wrap;
    gap: 8px;
  }
  .cc-head {
    flex-wrap: wrap;
    gap: 8px;
  }
}

/* 最小屏（≤480px 手机）：一行放宽到最少 3 个（磁贴更宽更可读；更大屏幕列数只多不少） */
@media (max-width: 480px) {
  .km-grid {
    grid-template-columns: repeat(3, 1fr);
  }
}
</style>

<!-- 日期控件内嵌输入宽度用全局规则：scoped 落不到 el-date-picker 根节点（同 TimeRangePicker） -->
<style>
.cc-win .el-range-input {
  width: 104px !important;
  text-align: left;
  padding-left: 0;
}
</style>
