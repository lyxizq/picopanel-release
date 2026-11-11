<template>
  <div class="trp">
    <el-select
      :model-value="range"
      size="small"
      style="width: 110px"
      @update:model-value="onPick"
    >
      <el-option v-for="p in presets" :key="p.value" :label="p.label" :value="p.value" />
      <el-option :label="$t('charts.custom')" :value="-1" />
    </el-select>
    <!-- 自定义时间段：选好起止后点「查询」按绝对窗口拉数据（-1 档位由父级等待 query 事件） -->
    <template v-if="range === -1">
      <el-date-picker
        v-model="win"
        type="datetimerange"
        size="small"
        class="trp-win"
        :start-placeholder="$t('charts.startPh')"
        :end-placeholder="$t('charts.endPh')"
        format="YYYY-MM-DD HH:mm"
        value-format="YYYY-MM-DDTHH:mm:ss"
        :clearable="false"
      />
      <el-button size="small" type="primary" :disabled="!win" @click="query">{{ $t('charts.query') }}</el-button>
    </template>
    <el-checkbox
      :model-value="auto"
      @update:model-value="$emit('update:auto', $event)"
      style="margin-left: 8px"
    >
      {{ $t('charts.autoRefresh') }}
    </el-checkbox>
  </div>
</template>

<script setup>
// 时段选择器：预设档位下拉 + 可选「自定义时间段」（datetimerange 选起止、
// 点查询按绝对窗口查）。range=-1 是自定义档的哨兵值——父级收到 -1 不应
// 直接拉数据，等 query 事件携带 {from, to}（unix 秒）再查。
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/svcmon.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

defineProps({
  range: { type: Number, required: true }, // 秒；-1 = 自定义时间段
  auto: { type: Boolean, default: false },
})
const emit = defineEmits(['update:range', 'update:auto', 'query'])

const win = ref(null) // datetimerange 绑定值 [startStr, endStr]

function onPick(v) {
  emit('update:range', v)
}

function query() {
  if (!win.value || win.value.length !== 2) return
  const from = Math.floor(new Date(win.value[0]).getTime() / 1000)
  const to = Math.floor(new Date(win.value[1]).getTime() / 1000)
  if (!(from < to)) return
  emit('query', { from, to })
}

// 预设档位秒数与对应 i18n 键（charts.p*），label 走 computed 随语言切换重建
const PRESET_VALS = [300, 900, 1800, 3600, 21600, 43200, 86400, 3 * 86400, 7 * 86400, 30 * 86400, 60 * 86400, 90 * 86400]
const P_LABELS = {
  300: 'p5m', 900: 'p15m', 1800: 'p30m', 3600: 'p1h', 21600: 'p6h', 43200: 'p12h',
  86400: 'p24h', 259200: 'p3d', 604800: 'p7d', 2592000: 'p30d', 5184000: 'p60d', 7776000: 'p90d',
}
const presets = computed(() => PRESET_VALS.map((v) => ({ value: v, label: t(`charts.${P_LABELS[v]}`) })))
</script>

<style scoped>
.trp {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
</style>

<!-- 宽度规则用全局样式：el-date-picker 的根节点拿不到父组件 scoped 属性
（实测 data-v 落不上），scoped/:deep 选择器都不生效 -->
<style>
/* 刚好容纳「YYYY-MM-DD HH:mm」两段完整日期时间：内嵌输入去侧边距，容器不放余量 */
.trp-win.el-date-editor {
  width: 290px !important; /* el-range-editor 自带 flex-grow，不加 flex:none 会被容器拉伸 */
  flex: none !important;
}
.trp-win .el-range-input {
  width: 104px !important;
  text-align: left;
  padding-left: 0;
}
</style>
