<template>
  <div ref="el" class="traffic-chart" />
</template>

<script setup>
// 外网流量逐日堆叠柱（详情弹层「外网流量」卡）：下行蓝叠上行橙，颜色与速率曲线
// 的方向约定一致；纵轴字节自动选档、横轴 MM-DD 每 5 天一签尾日必签。独立组件
// 而非页面内 useChart——容器在弹层里，页面挂载时 ref 还是 null，必须在本组件
// 挂载时才能 init（同 WxHoursChart）。
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useChart } from '../composables/useChart.js'
import { NET_DOWN_COLOR, NET_UP_COLOR, ink, tooltipStyle } from '../utils/chartTheme.js'
import { fmtBytes } from '../utils/format.js'
import { theme } from '../utils/theme.js'
import { registerPageMessages, lang } from '../i18n'
import pageMsgs from '../i18n/pages/serverdlg.js'

const { t: tr } = useI18n()
registerPageMessages(pageMsgs)

// days：连续日期数组 [{ day: 'YYYY-MM-DD', rx, tx }]（父组件已补零）
const props = defineProps({ days: { type: Array, required: true } })

const el = ref(null)
const render = useChart(el, (chart) => {
  if (!props.days.length) return
  const INK = ink()
  const tip = tooltipStyle()
  const rx = props.days.map((d) => d.rx)
  const tx = props.days.map((d) => d.tx)
  const last = props.days.length - 1
  chart.setOption(
    {
      backgroundColor: 'transparent',
      grid: { left: 64, right: 12, top: 30, bottom: 28 },
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
          const d = props.days[ps?.[0]?.dataIndex ?? -1]
          if (!d) return ''
          return `<b>${d.day}</b><br/>↓ ${tr('serverdlg.detail.trafficDown')} ${fmtBytes(d.rx)}<br/>↑ ${tr('serverdlg.detail.trafficUp')} ${fmtBytes(d.tx)}<br/>${tr('serverdlg.detail.trafficTotal')} ${fmtBytes(d.rx + d.tx)}`
        },
      },
      xAxis: {
        type: 'category',
        data: props.days.map((d) => d.day.slice(5)),
        axisLine: { lineStyle: { color: INK.baseline } },
        axisTick: { show: false },
        axisLabel: { color: INK.axis, fontSize: 10, interval: (i) => i % 5 === 0 || i === last },
      },
      yAxis: {
        type: 'value',
        axisLabel: { color: INK.axis, fontSize: 11, formatter: (v) => fmtBytes(v) },
        splitLine: { lineStyle: { color: INK.grid } },
      },
      series: [
        { name: tr('serverdlg.detail.trafficDown'), type: 'bar', stack: 'traffic', data: rx, barMaxWidth: 18, itemStyle: { color: NET_DOWN_COLOR } },
        { name: tr('serverdlg.detail.trafficUp'), type: 'bar', stack: 'traffic', data: tx, barMaxWidth: 18, itemStyle: { color: NET_UP_COLOR } },
      ],
    },
    true,
  )
})
// 刷新/换机器（days 引用变化）、明暗主题切换后按新墨色重绘、语言切换后按新文案重建 option
watch(() => props.days, render)
watch(theme, render)
watch(lang, render)
</script>

<style scoped>
.traffic-chart {
  width: 100%;
  height: 200px;
}
</style>
