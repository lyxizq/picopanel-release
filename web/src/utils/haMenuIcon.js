// HA 菜单/FAB 图标（380）：「房 + 中央灯泡（顶挂线+灯丝）」，密度与智能家居
// 语义兼顾（EP House 原版太空旷被用户否掉）。h() 函数组件 + currentColor，
// 描线 1.7 与 TermMonIcon/ServerRackIcon 同族；候选对比稿 .tmp/ha-menu-ico.png。
import { h } from 'vue'

export const HaMenuIcon = () =>
  h('svg', {
    viewBox: '0 0 24 24', width: '1em', height: '1em',
    fill: 'none', stroke: 'currentColor', 'stroke-width': '1.7',
    'stroke-linecap': 'round', 'stroke-linejoin': 'round',
  }, [
    h('path', { d: 'M4 11.2 12 4l8 7.2' }),
    h('path', { d: 'M6.2 9.6V20h11.6V9.6' }),
    h('path', { d: 'M12 4.2v3' }),
    h('path', { d: 'M10 17.4a2.6 2.6 0 1 1 4 0c-.5.5-.8 1-.8 1.7h-2.4c0-.7-.3-1.2-.8-1.7z' }),
    h('path', { d: 'M9.9 20.8h4.2' }),
  ])

