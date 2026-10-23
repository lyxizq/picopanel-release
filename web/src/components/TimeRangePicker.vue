<template>
  <div class="trp">
    <el-select
      :model-value="range"
      size="small"
      style="width: 110px"
      @update:model-value="onPick"
    >
      <el-option v-for="p in presets" :key="p.value" :label="p.label" :value="p.value" />
      <el-option label="自定义时间段" :value="-1" />
    </el-select>
    <!-- 自定义时间段：选好起止后点「查询」按绝对窗口拉数据（-1 档位由父级等待 query 事件） -->
    <template v-if="range === -1">
      <el-date-picker
        v-model="win"
        type="datetimerange"
        size="small"
        class="trp-win"
        start-placeholder="开始时间"
        end-placeholder="结束时间"
        format="YYYY-MM-DD HH:mm"
        value-format="YYYY-MM-DDTHH:mm:ss"
        :clearable="false"
      />
      <el-button size="small" type="primary" :disabled="!win" @click="query">查询</el-button>
    </template>
    <el-checkbox
      :model-value="auto"
      @update:model-value="$emit('update:auto', $event)"
      style="margin-left: 8px"
    >
      30s 自动刷新
    </el-checkbox>
  </div>
</template>

<script setup>
// 时段选择器：预设档位下拉 + 可选「自定义时间段」（datetimerange 选起止、
// 点查询按绝对窗口查）。range=-1 是自定义档的哨兵值——父级收到 -1 不应
// 直接拉数据，等 query 事件携带 {from, to}（unix 秒）再查。
import { ref } from 'vue'

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

const presets = [
  { label: '5分钟', value: 300 },
  { label: '15分钟', value: 900 },
  { label: '30分钟', value: 1800 },
  { label: '1小时', value: 3600 },
  { label: '6小时', value: 21600 },
  { label: '12小时', value: 43200 },
  { label: '24小时', value: 86400 },
  { label: '3天', value: 3 * 86400 },
  { label: '7天', value: 7 * 86400 },
  { label: '30天', value: 30 * 86400 },
  { label: '60天', value: 60 * 86400 },
  { label: '90天', value: 90 * 86400 },
]
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
