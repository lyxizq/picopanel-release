<template>
  <div ref="el" class="chart" :style="{ height }" />
</template>

<script setup>
import { ref, watch } from 'vue'
import { useChart } from '../composables/useChart.js'
import { areaGradient, CHART_COLORS, ink, tooltipStyle } from '../utils/chartTheme.js'
import { fmtChartNum } from '../utils/format.js'
import { theme } from '../utils/theme.js'

// 通用时间序列折线图。series: [{name, points: [[毫秒时间戳, 值], ...], color?}]
//（color 为可选钉色，序列顺序即 tooltip/图例顺序——需要某序列排前面就放前面）
// 面积渐变填充全站统一（2026-10-03 用户指定照截图样式）：80% 透明度贴线渐隐
const props = defineProps({
  series: { type: Array, required: true },
  unit: { type: String, default: '' }, // 数值单位（拼在 tooltip 值后）
  percent: { type: Boolean, default: false }, // y 轴固定 0~100 并带 % 刻度
  // 镜像网络图：调用方把下行序列取负传入，上行向上/下行向下共用 0 轴
  //（x 轴线默认 onZero 落在 0 即中轴），各序列面积填充到 0 线。
  mirror: { type: Boolean, default: false },
  valueFormatter: { type: Function, default: null }, // 自定义数值格式化 (v) => string
  height: { type: String, default: '260px' },
  yAxisFormatter: { type: Function, default: null }, // y 轴刻度格式化
  showLegend: { type: Boolean, default: true }, // 多序列时是否显示内置图例（外部自绘图例时关闭）
})

const el = ref(null)

const render = useChart(el, (chart) => {
  const INK = ink()
  const tip = tooltipStyle()
  const fmt =
    props.valueFormatter ||
    ((v) => (v == null || isNaN(v) ? '-' : fmtChartNum(v) + props.unit + (props.percent ? '%' : '')))
  const yFmt = props.yAxisFormatter || (props.percent ? (v) => `${v}%` : fmtChartNum)
  const showLegend = props.showLegend && props.series.length > 1
  chart.setOption(
    {
      color: CHART_COLORS,
      backgroundColor: 'transparent',
      // 带单位的自定义轴刻度（如 "1024.0 KB/s"）比默认刻度宽，左边距相应加大
      grid: { left: props.yAxisFormatter ? 72 : 56, right: 16, top: showLegend ? 34 : 16, bottom: 26 },
      tooltip: {
        trigger: 'axis',
        // 按序列顺序展示：网络图调用方按 [上行, 下行] 传入（含钉色），
        // tooltip 与图例都上行在前，无需 ECharts order 倒序
        axisPointer: { type: 'line', lineStyle: { color: INK.baseline } },
        backgroundColor: tip.backgroundColor,
        borderColor: tip.borderColor,
        textStyle: tip.textStyle,
        valueFormatter: (v) => fmt(v ?? 0),
      },
      legend:
        showLegend
          ? {
              top: 0,
              icon: 'rect',
              itemWidth: 10,
              itemHeight: 4,
              textStyle: { color: INK.text, fontSize: 12 },
              type: 'scroll',
            }
          : undefined,
      xAxis: {
        type: 'time',
        axisLine: { lineStyle: { color: INK.baseline } },
        axisLabel: { color: INK.axis, hideOverlap: true, fontSize: 11 },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'value',
        max: props.percent ? 100 : undefined,
        axisLabel: { color: INK.axis, fontSize: 11, formatter: yFmt },
        splitLine: { lineStyle: { color: INK.grid } },
      },
      series: props.series.map((s, i) => {
        const color = s.color || CHART_COLORS[i % CHART_COLORS.length]
        const rev = props.mirror && s.points.some((p) => p[1] < 0)
        return {
          name: s.name,
          type: 'line',
          color, // 可选钉色（如网络图按方向固定下行蓝/上行橙），缺省走顶层色板
          showSymbol: false,
          smooth: true,
          smoothMonotone: 'x', // 平滑时禁止曲线在两点间画出数据里不存在的过冲尖峰
          lineStyle: { width: 2 },
          emphasis: { focus: 'series' },
          areaStyle: {
            origin: props.mirror ? 0 : undefined, // 镜像图面积填到 0 线（origin 数值需 ECharts ≥5.3）
            color: areaGradient(color, rev),
          },
          data: s.points,
        }
      }),
    },
    true,
  )
})

// 浅监听即可：调用方每次都是整组替换序列数组（Dashboard 走 cardSeries 缓存、
// 详情弹窗/放大弹层按次构造），引用变化才重绘；deep 会在每次父级重渲染时
// 深度遍历上千点位白白触发全量重绘
watch(() => props.series, render)
watch(theme, render) // 明暗切换后按新墨色重绘
</script>

<style scoped>
.chart {
  width: 100%;
}
</style>
