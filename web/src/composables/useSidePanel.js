// 左右分栏（文档/终端/文件管理共用）：侧栏宽度可拖拽调节、单击分隔条收起/
// 展开，配置存 localStorage 按页面独立记忆（key: lz.side.width.<name> /
// lz.side.collapsed.<name>），下次打开保持。模块级缓存让同页面多实例
//（keep-alive 切换、组件重建）共享同一份状态。
import { reactive, ref } from 'vue'

const MIN = 180
const MAX = 480
const cache = {}

function create(name, defWidth) {
  const keyW = `lz.side.width.${name}`
  const keyC = `lz.side.collapsed.${name}`
  const stored = parseInt(localStorage.getItem(keyW))
  const width = ref(stored >= MIN && stored <= MAX ? stored : defWidth)
  const collapsed = ref(localStorage.getItem(keyC) === '1')
  const resizing = ref(false) // 拖拽中：分隔条高亮用
  let startX = 0
  let startW = 0
  let moved = 0

  function save() {
    localStorage.setItem(keyW, String(width.value))
    localStorage.setItem(keyC, collapsed.value ? '1' : '0')
  }

  function onMove(e) {
    if (collapsed.value) return // 收起状态只响应单击展开，不跟手
    width.value = Math.min(MAX, Math.max(MIN, startW + e.clientX - startX))
  }

  function onUp(e) {
    window.removeEventListener('mousemove', onMove)
    window.removeEventListener('mouseup', onUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    resizing.value = false
    moved = Math.max(moved, Math.abs(e.clientX - startX))
    if (moved < 3) collapsed.value = !collapsed.value // 原地单击 = 收起/展开
    save()
  }

  function startDrag(e) {
    if (e.button !== 0) return
    e.preventDefault()
    startX = e.clientX
    startW = width.value
    moved = 0
    resizing.value = true
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }

  return reactive({ width, collapsed, resizing, startDrag })
}

export function useSidePanel(name, defWidth = 220) {
  if (!cache[name]) cache[name] = create(name, defWidth)
  return cache[name]
}
