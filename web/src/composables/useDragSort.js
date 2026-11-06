// HTML5 原生拖拽排序（PC；移动端浏览器不支持原生 DnD，属性自动无效无碍）。
// 用法：容器子项绑定 draggable="true" + 四个事件，drop 时重排数组并回调新 id 顺序。
// 语义：拖到目标项上松手 = 插入到目标项之前。
import { ref } from 'vue'

export function useDragSort(list, onReorder) {
  const from = ref(-1) // 拖动起点下标（-1 = 未在拖动）
  const over = ref(-1) // 当前悬停目标下标（高亮用）

  function onStart(i, e) {
    from.value = i
    // Firefox 要求 dragstart 写入数据才会启动拖拽
    if (e?.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData('text/plain', String(i))
    }
  }
  function onOver(i, e) {
    if (from.value < 0) return
    e.preventDefault() // 允许 drop
    e.dataTransfer.dropEffect = 'move'
    over.value = i
  }
  // dragenter 同口径 cancel（2026-10-03 修「拖拽后无法排序」：Safari 等内核
  // 只 cancel dragover 不 cancel dragenter 时 drop 不触发）
  const onEnter = onOver
  function onDrop(i) {
    const f = from.value
    from.value = over.value = -1
    if (f < 0 || f === i) return
    const arr = list.value
    const [item] = arr.splice(f, 1)
    arr.splice(f < i ? i - 1 : i, 0, item)
    onReorder(arr.map((x) => x.id))
  }
  function onEnd() {
    from.value = over.value = -1
  }

  return { from, over, onStart, onOver, onEnter, onDrop, onEnd }
}
