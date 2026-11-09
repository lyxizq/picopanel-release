// 文件管理 → 终端页的跳转意图（跨页传递）：模块级变量，FilesView 设置，
// TermView 挂载/激活加载完服务器列表后取走并打开对应会话（取走即清空，
// 不会重复打开）。
let pending = null

export function setTermPending(server) {
  pending = server
}

export function takeTermPending() {
  const p = pending
  pending = null
  return p
}
