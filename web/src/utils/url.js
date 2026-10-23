// safeUrl 存储型 URL 的使用侧兜底：书签/磁贴/集成卡等链接值只在保存入口做过
// http(s) 校验（历史脏数据或绕过前端直写 API 可不受控），渲染与跳转前统一
// 再过滤——非 http(s) 协议一律返回空串，javascript: 等危险协议由此拦死。
export function safeUrl(u) {
  const s = String(u || '').trim()
  if (!/^https?:\/\//i.test(s)) return ''
  try {
    const parsed = new URL(s)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? s : ''
  } catch {
    return ''
  }
}
