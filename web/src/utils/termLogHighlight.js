// 终端日志高亮：对正常缓冲区按渲染到的行扫描，命中日志要素（时间戳/级别/IP）
// 后用 xterm decoration 着色——只改显示层，不动数据流，不影响复制与远端。
// 口径（参考用户截图）：时间戳绿、[ERROR/FATAL] 红、[INFO/DEBUG] 青、
// [WARN] 黄、IPv4/IPv6 蓝。备选屏程序（vim/htop）不处理。
const PATTERNS = [
  { re: /\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:[.,]\d{1,3})?/g, color: '#98c379' },
  { re: /\[(?:ERROR|FATAL|ERR)\]/g, color: '#e06c75' },
  { re: /\[(?:INFO|NOTICE|DEBUG)\]/g, color: '#56b6c2' },
  { re: /\[(?:WARN|WARNING)\]/g, color: '#e5c07b' },
  { re: /\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b/g, color: '#61afef' },
  { re: /\b[0-9A-Fa-f]{1,4}(?::[0-9A-Fa-f]{0,4}){4,7}\b/g, color: '#61afef' },
]

const MAX_ENTRIES = 2000 // 已处理行数上限（超出淘汰最早的，滚动历史久处会褪色）
const MAX_PER_PASS = 300 // 单次渲染最多新扫行数（洪峰时限流）
const MAX_HITS_PER_LINE = 8

// attachLogHighlight(term) → { dispose }。onRender 视口行 → 缓冲行，
// 未处理过的行扫描并注册 decoration；marker 随行滚动/裁剪自动维护。
export function attachLogHighlight(term) {
  const entries = new Map() // marker → deco[]（含无命中的行，避免反复重扫）

  const dropEntry = (marker) => {
    const decos = entries.get(marker)
    entries.delete(marker)
    if (decos) for (const d of decos) d.dispose()
  }

  // 字符串下标 → 单元格 x（CJK 等双宽字符占两格，decoration 按格定位）
  const cellXOf = (line, strIdx) => {
    let si = 0
    for (let x = 0; x < term.cols; x++) {
      if (si >= strIdx) return x
      si += line.getCell(x)?.getWidth() || 1
    }
    return term.cols - 1
  }
  const cellWidth = (line, strIdx, len) => {
    let w = 0
    let si = 0
    for (let x = 0; x < term.cols; x++) {
      const cw = line.getCell(x)?.getWidth() || 1
      if (si >= strIdx && si < strIdx + len) w += cw
      si += cw
      if (si >= strIdx + len) break
    }
    return Math.max(w, 1)
  }

  const scanLine = (bl, cursorLine) => {
    const line = term.buffer.active.getLine(bl)
    if (!line) return
    const marker = term.registerMarker(bl - cursorLine)
    if (!marker) return
    const decos = []
    const text = line.translateToString(true)
    if (text) {
      for (const p of PATTERNS) {
        for (const mt of text.matchAll(p.re)) {
          if (decos.length >= MAX_HITS_PER_LINE) break
          const s = mt.index
          const deco = term.registerDecoration({
            marker,
            x: cellXOf(line, s),
            width: cellWidth(line, s, mt[0].length),
            foregroundColor: p.color,
            layer: 'bottom',
          })
          if (deco) decos.push(deco)
        }
      }
    }
    marker.onDispose(() => dropEntry(marker))
    entries.set(marker, decos)
    while (entries.size > MAX_ENTRIES) {
      const oldest = entries.keys().next().value
      if (!oldest) break
      dropEntry(oldest) // 最老的行淘汰（其 marker.dispose 会触发 onDispose 清理）
      oldest.dispose()
    }
  }

  const unsub = term.onRender(({ start, end }) => {
    const buf = term.buffer.active
    if (buf.type === 'alternate') return
    let newInPass = 0
    const done = new Set()
    for (const m of entries.keys()) {
      if (m.line >= 0) done.add(m.line)
    }
    const cursorLine = buf.baseY + buf.cursorY
    for (let row = start; row <= end; row++) {
      if (newInPass >= MAX_PER_PASS) return
      const bl = buf.viewportY + row
      if (bl === cursorLine || done.has(bl)) continue
      scanLine(bl, cursorLine)
      newInPass++
    }
  })

  return {
    dispose() {
      unsub.dispose()
      for (const [marker, decos] of [...entries]) {
        for (const d of decos) d.dispose()
        entries.delete(marker)
        marker.dispose()
      }
    },
  }
}
