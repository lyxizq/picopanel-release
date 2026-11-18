<template>
  <el-dialog v-model="visible" width="min(96vw, 1080px)" append-to-body>
    <template #header>
      <div class="czd-head">
        <span class="czd-title" :title="opts.title">{{ opts.title }}</span>
        <el-select :model-value="rangeSec" size="small" class="czd-range" @update:model-value="onPick">
          <el-option v-for="r in DIALOG_RANGES" :key="r.value" :value="r.value" :label="rangeLabel(r.value)" />
          <el-option :value="-1" :label="$t('charts.custom')" />
        </el-select>
        <!-- 自定义时间段：选起止后点「查询」按绝对窗口拉数据 -->
        <template v-if="rangeSec === -1">
          <el-date-picker
            v-model="win"
            type="datetimerange"
            size="small"
            class="czd-win"
            :start-placeholder="$t('charts.startPh')"
            :end-placeholder="$t('charts.endPh')"
            format="YYYY-MM-DD HH:mm"
            value-format="YYYY-MM-DDTHH:mm:ss"
            :clearable="false"
          />
          <el-button size="small" type="primary" :disabled="!win" @click="query">{{ $t('charts.query') }}</el-button>
        </template>
      </div>
    </template>
    <div v-if="visible" class="czd-body" v-loading="loading">
      <SeriesChart
        :series="series"
        :percent="opts.percent"
        :mirror="opts.bps"
        :value-formatter="opts.bps ? fmtBpsAbs : undefined"
        :y-axis-formatter="opts.bps ? bpsAxisFmt(series) : undefined"
        height="30vh"
      />
    </div>
  </el-dialog>
</template>

<script setup>
// 图表放大弹层：点击图表区域打开，弹层内自选时段重新拉数据画大图。
// 数据由调用方通过 loader(range秒, win) 提供（win={from,to} 秒 为自定义
// 时间段时的绝对窗口，null = 预设档）；组件只管弹层、时段与加载态。
defineOptions({ name: 'ChartZoomDialog' })
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import SeriesChart from './SeriesChart.vue'
import { bpsAxisFmt, fmtBpsAbs } from '../utils/format.js'
import { DIALOG_RANGES } from '../utils/chartTheme.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/svcmon.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

// DIALOG_RANGES 的 label 已是核心包 i18n key（fmt.r*），直接 t() 渲染
function rangeLabel(sec) {
  const r = DIALOG_RANGES.find((x) => x.value === sec)
  return r ? t(r.label) : ''
}

const visible = ref(false)
const loading = ref(false)
const series = ref([])
const rangeSec = ref(3600)
const customWin = ref(null) // 已生效的自定义窗口（选回预设档时清空）
const win = ref(null) // datetimerange 绑定值
const opts = ref({})

let seq = 0 // 序号守卫：晚到的旧响应丢弃

// open({title, percent, bps, initialRange, loader})；
// loader(rangeSec, win) 返回 Promise<series[]>（SeriesChart 的序列数组）
function open(o) {
  opts.value = o
  rangeSec.value = o.initialRange || 3600
  customWin.value = null
  win.value = null
  visible.value = true
  reload()
}

function onPick(v) {
  rangeSec.value = v
  if (v === -1) return // 自定义档：等「查询」按钮
  customWin.value = null
  reload()
}

function query() {
  if (!win.value || win.value.length !== 2) return
  const from = Math.floor(new Date(win.value[0]).getTime() / 1000)
  const to = Math.floor(new Date(win.value[1]).getTime() / 1000)
  if (!(from < to)) {
    ElMessage.warning(t('charts.startBeforeEnd'))
    return
  }
  customWin.value = { from, to }
  reload()
}

async function reload() {
  const cur = ++seq
  loading.value = true
  try {
    const list = (await opts.value.loader(rangeSec.value, customWin.value)) || []
    if (cur === seq) series.value = list
  } catch (e) {
    if (cur === seq) {
      series.value = []
      ElMessage.error(e.message)
    }
  } finally {
    if (cur === seq) loading.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.czd-head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding-right: 16px;
}
.czd-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 15px;
  font-weight: 600;
}
.czd-range {
  width: 130px;
  flex: none;
}
.czd-win.el-date-editor {
  width: 290px !important; /* 刚好容纳两段「YYYY-MM-DD HH:mm」完整日期时间 */
  flex: none !important; /* el-range-editor 自带 flex-grow，不加会被工具行拉伸 */
}
.czd-body {
  min-height: 20vh;
}
</style>

<!-- 日期控件内嵌输入宽度用全局规则：scoped 落不到 el-date-picker 根节点（同 TimeRangePicker） -->
<style>
.czd-win .el-range-input {
  width: 104px !important;
  text-align: left;
  padding-left: 0;
}
</style>
