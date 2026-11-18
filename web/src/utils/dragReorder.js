// 拖拽排序的核心数组操作（从主页 PageView 抽出，便于单元测试验证语义）。
// 语义：把拖动项放到目标项的位置——向下拖（from<to）落到目标之后、向上拖落到目标之前，
// 即「A 拖到 B 上 = A 落到 B 的位置」（与文件管理/总览拖拽一致，270 用户反馈修正）。

// 按 id 在扁平数组中移动：movedId 放到 targetId 的位置。
// groupOf(nextTarget) 回调用于跨组场景同步归属（返回目标项所在组，写回 item.group_id）。
// 返回 { arr, group }；movedId/targetId 找不到或相同返回 null（调用方无需处理）。
export function moveById(arr, movedId, targetId, groupOf) {
  const from = arr.findIndex((x) => x.id === movedId)
  const to = arr.findIndex((x) => x.id === targetId)
  if (from < 0 || to < 0 || from === to) return null
  const next = [...arr]
  const [item] = next.splice(from, 1)
  const tIdx = next.findIndex((x) => x.id === targetId)
  const group = groupOf ? groupOf(next[tIdx]) : null
  if (groupOf) item.group_id = group
  next.splice(from < to ? tIdx + 1 : tIdx, 0, item)
  return { arr: next, group }
}
