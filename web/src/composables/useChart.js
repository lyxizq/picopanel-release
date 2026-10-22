import { init, use } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { onBeforeUnmount, onMounted } from 'vue'

// 按需注册（全站图表只有时间序列折线一种）：比全量 import * 少 ~1MB 进包体。
// 新图表类型/组件在这里补登记。
use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

// ECharts 实例生命周期管理：初始化、ResizeObserver 自适应、卸载时 dispose。
// 返回 render(chart => option)，组件内 watch 数据变化调用即可（挂载前调用会被忽略）。
export function useChart(containerRef, renderFn) {
  let chart = null
  let ro = null

  const render = () => {
    if (chart) renderFn(chart)
  }

  onMounted(() => {
    chart = init(containerRef.value)
    ro = new ResizeObserver(() => chart && chart.resize())
    ro.observe(containerRef.value)
    render()
  })

  onBeforeUnmount(() => {
    if (ro) ro.disconnect()
    if (chart) chart.dispose()
    chart = null
  })

  return render
}
