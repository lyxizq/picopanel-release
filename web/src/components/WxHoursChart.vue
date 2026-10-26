<template>
  <div ref="el" class="wx-chart" />
</template>

<script setup>
// 和风逐小时堆叠柱（3，2026-10-23 用户指定坐标轴版式）：横轴两行「MM-DD / HH:00」
// 每 4 桶一签、纵轴「请求次数」、柱顶标该小时总数（成功+失败，0 不标），失败红
// 堆在成功蓝上。独立组件而非页面内 useChart——容器在弹层 v-if 里，页面挂载时
// ref 还是 null，必须在弹层首次渲染（本组件挂载）时才能 init。
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useChart } from '../composables/useChart.js'
import { ink, tooltipStyle } from '../utils/chartTheme.js'
import { theme } from '../utils/theme.js'
import { registerPageMessages, lang } from '../i18n'
import pageMsgs from '../i18n/pages/aiusage.js'

const { t: tr } = useI18n()
registerPageMessages(pageMsgs)

const props = defineProps({ hours: { type: Array, required: true } })

// 桶两行轴签（退化桶 ts=0 退回上游/序号标签）
function bucketLabel(h) {
  if (!h.ts) return h.label || ''
  const d = new Date(h.ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())}\n${p(d.getHours())}:00`
}
// tooltip 里的完整时间点
function bucketFull(h) {
  if (!h.ts) return h.label || ''
  const d = new Date(h.ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:00`
}

const el = ref(null)
const render = useChart(el, (chart) => {
  if (!props.hours.length) return
  const INK = ink()
  const tip = tooltipStyle()
  const succ = props.hours.map((h) => h.success)
  const err = props.hours.map((h) => h.errors)
  chart.setOption(
    {
      backgroundColor: 'transparent',
      grid: { left: 48, right: 12, top: 34, bottom: 46 },
      legend: {
        top: 0, right: 0, icon: 'rect', itemWidth: 10, itemHeight: 4, itemGap: 12,
        textStyle: { color: INK.text, fontSize: 11 },
      },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        backgroundColor: tip.backgroundColor,
        borderColor: tip.borderColor,
        textStyle: tip.textStyle,
        formatter: (ps) => {
          const h = props.hours[ps?.[0]?.dataIndex ?? -1]
          return h ? `<b>${bucketFull(h)}</b><br/>${tr('aiusage.chart.tip', { ok: h.success, err: h.errors })}` : ''
        },
      },
      xAxis: {
        type: 'category',
        data: props.hours.map(bucketLabel),
        axisLine: { lineStyle: { color: INK.baseline } },
        axisTick: { show: false },
        axisLabel: { color: INK.axis, fontSize: 10, interval: 3, lineHeight: 13, margin: 10 },
      },
      yAxis: {
        type: 'value',
        name: tr('aiusage.chart.yAxisName'),
        nameTextStyle: { color: INK.axis, fontSize: 11, align: 'left' },
        axisLabel: { color: INK.axis, fontSize: 11 },
        splitLine: { lineStyle: { color: INK.grid } },
      },
      series: [
        { name: tr('aiusage.chart.ok'), type: 'bar', stack: 'wx', data: succ, barMaxWidth: 16, itemStyle: { color: '#2a78d6' } },
        {
          name: tr('aiusage.chart.failed'), type: 'bar', stack: 'wx', data: err, barMaxWidth: 16, itemStyle: { color: '#f56c6c' },
          label: {
            show: true, position: 'top', color: INK.text, fontSize: 10,
            formatter: (p) => {
              const t = succ[p.dataIndex] + err[p.dataIndex]
              return t > 0 ? String(t) : ''
            },
          },
        },
      ],
    },
    true,
  )
})
// 刷新/换账户（hours 引用变化）、明暗主题切换后按新墨色重绘、语言切换后按新文案重建 option
watch(() => props.hours, render)
watch(theme, render)
watch(lang, render)
</script>

<style scoped>
.wx-chart {
  width: 100%;
  height: 240px;
}
</style>
