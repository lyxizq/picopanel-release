// 工具箱共享纯函数库：全部浏览器本地计算，零后端依赖。
import { ElMessage } from 'element-plus'
import { i18n, registerPageMessages } from '../../i18n'
import pageMsgs from '../../i18n/pages/toolbox.js'

registerPageMessages(pageMsgs) // 纯模块顶层注册（幂等，.vue 侧重复注册无害）

export async function copyOut(s) {
  if (!s) { ElMessage.warning(i18n.global.t('toolbox.lib.empty')); return }
  try { await navigator.clipboard.writeText(s); ElMessage.success(i18n.global.t('toolbox.lib.copied')) }
  catch { ElMessage.warning(i18n.global.t('toolbox.lib.copyFail')) }
}

/* ---------- MD5（RFC 1321，支持字符串与字节） ---------- */
const md5K = (() => {
  const k = new Uint32Array(64)
  for (let i = 0; i < 64; i++) k[i] = Math.floor(Math.abs(Math.sin(i + 1)) * 4294967296)
  return k
})()
const md5S = [7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22,
  5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20, 5, 9, 14, 20,
  4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23,
  6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21]

function md5Blocks(bytes) {
  const len = bytes.length
  const withPad = new Uint8Array((((len + 8) >> 6) + 1) << 6)
  withPad.set(bytes)
  withPad[len] = 0x80
  const bitLen = len * 8
  const dv = new DataView(withPad.buffer)
  dv.setUint32(withPad.length - 8, bitLen >>> 0, true)
  dv.setUint32(withPad.length - 4, Math.floor(bitLen / 4294967296), true)
  return { words: new Int32Array(withPad.buffer), nBlocks: withPad.length >> 6 }
}

export function md5FromBytes(input) {
  const { words, nBlocks } = md5Blocks(input instanceof Uint8Array ? input : new Uint8Array(input))
  let a = 0x67452301, b = 0xefcdab89, c = 0x98badcfe, d = 0x10325476
  const rol = (x, n) => (x << n) | (x >>> (32 - n))
  for (let i = 0; i < nBlocks; i++) {
    const m = [words[i * 16], words[i * 16 + 1], words[i * 16 + 2], words[i * 16 + 3], words[i * 16 + 4], words[i * 16 + 5], words[i * 16 + 6], words[i * 16 + 7], words[i * 16 + 8], words[i * 16 + 9], words[i * 16 + 10], words[i * 16 + 11], words[i * 16 + 12], words[i * 16 + 13], words[i * 16 + 14], words[i * 16 + 15]]
    let A = a, B = b, C = c, D = d
    for (let j = 0; j < 64; j++) {
      let f, g
      if (j < 16) { f = (B & C) | (~B & D); g = j }
      else if (j < 32) { f = (D & B) | (~D & C); g = (5 * j + 1) & 15 }
      else if (j < 48) { f = B ^ C ^ D; g = (3 * j + 5) & 15 }
      else { f = C ^ (B | ~D); g = (7 * j) & 15 }
      const tmp = D
      D = C; C = B
      B = (B + rol((A + f + md5K[j] + m[g]) | 0, md5S[j])) | 0
      A = tmp
    }
    a = (a + A) | 0; b = (b + B) | 0; c = (c + C) | 0; d = (d + D) | 0
  }
  const out = new DataView(new ArrayBuffer(16))
  out.setUint32(0, a >>> 0, true); out.setUint32(4, b >>> 0, true)
  out.setUint32(8, c >>> 0, true); out.setUint32(12, d >>> 0, true)
  return [...new Uint8Array(out.buffer)].map(x => x.toString(16).padStart(2, '0')).join('')
}

export function md5(str) { return md5FromBytes(new TextEncoder().encode(str)) }

/* ---------- SHA 系列（Web Crypto） ---------- */
export async function shaHex(algo, data) {
  const bytes = data instanceof Uint8Array ? data : new TextEncoder().encode(data)
  const buf = await crypto.subtle.digest(algo, bytes)
  return [...new Uint8Array(buf)].map(x => x.toString(16).padStart(2, '0')).join('')
}

/* ---------- Base64（UTF-8 安全） ---------- */
export function b64encode(str) {
  const bytes = new TextEncoder().encode(str)
  let bin = ''
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin)
}
export function b64decode(str) {
  const bin = atob(str.replace(/\s+/g, ''))
  const bytes = Uint8Array.from(bin, ch => ch.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

/* ---------- Legacy escape/unescape（%uxxxx + URL 转义） ---------- */
export function escapeStr(str) {
  let out = ''
  for (const ch of str) {
    const c = ch.codePointAt(0)
    if (c < 128 && /[A-Za-z0-9@*_+-./]/.test(ch)) out += ch
    else if (c < 256) out += '%' + c.toString(16).padStart(2, '0').toUpperCase()
    else out += '%u' + c.toString(16).padStart(4, '0').toUpperCase()
  }
  return out
}
export function unescapeStr(str) {
  return str.replace(/%u([0-9a-fA-F]{4})|%([0-9a-fA-F]{2})/g, (_, u, h) =>
    String.fromCharCode(parseInt(u || h, 16)))
}

/* ---------- Punycode（RFC 3492）中文域名转码 ---------- */
const PC_BASE = 36, PC_TMIN = 1, PC_TMAX = 26, PC_DAMP = 700, PC_SKEW = 38
function pcAdapt(delta, numPoints, firstTime) {
  delta = firstTime ? Math.floor(delta / PC_DAMP) : Math.floor(delta / 2)
  delta += Math.floor(delta / numPoints)
  let k = 0
  while (delta > ((PC_BASE - PC_TMIN) * PC_TMAX) >> 1) {
    delta = Math.floor(delta / (PC_BASE - PC_TMIN))
    k += PC_BASE
  }
  return k + Math.floor(((PC_BASE - PC_TMIN + 1) * delta) / (delta + PC_SKEW))
}
function digitChar(d) { return d < 26 ? String.fromCharCode(97 + d) : String.fromCharCode(22 + d) }
function digitVal(ch) {
  const c = ch.charCodeAt(0)
  if (c >= 97 && c <= 122) return c - 97
  if (c >= 65 && c <= 90) return c - 22
  if (c >= 48 && c <= 57) return c - 48 + 26
  return -1
}
function punyEncodeLabel(label) {
  const pts = [...label].map(c => c.codePointAt(0))
  const b = pts.filter(p => p < 0x80).length
  let output = pts.filter(p => p < 0x80).map(c => String.fromCharCode(c)).join('')
  let n = 128, delta = 0, bias = 72, handled = b
  if (b > 0) output += '-'
  while (handled < pts.length) {
    const m = Math.min(...pts.filter(p => p >= n))
    delta += (m - n) * (handled + 1)
    n = m
    for (const p of pts) {
      if (p < n) { delta++; continue }
      if (p !== n) continue
      let q = delta, k = PC_BASE
      for (;;) {
        const t = k <= bias ? PC_TMIN : k >= bias + PC_TMAX ? PC_TMAX : k - bias
        if (q < t) break
        output += digitChar(t + ((q - t) % (PC_BASE - t)))
        q = Math.floor((q - t) / (PC_BASE - t))
        k += PC_BASE
      }
      output += digitChar(q)
      bias = pcAdapt(delta, handled + 1, handled === b)
      delta = 0
      handled++
    }
    delta++
    n++
  }
  return 'xn--' + output
}
function punyDecodeLabel(label) {
  const src = label.replace(/^xn--/i, '')
  const dash = src.lastIndexOf('-')
  const basicPart = dash > 0 ? src.slice(0, dash) : ''
  const ext = dash > 0 ? src.slice(dash + 1) : src
  const output = [...basicPart].map(c => c.charCodeAt(0))
  let n = 128, i = 0, bias = 72, pos = 0
  while (pos < ext.length) {
    const oldi = i
    let w = 1
    for (let k = PC_BASE; ; k += PC_BASE) {
      if (pos >= ext.length) throw new Error(i18n.global.t('toolbox.lib.punyTruncated'))
      const dv = digitVal(ext[pos++])
      if (dv < 0) throw new Error(i18n.global.t('toolbox.lib.punyInvalid'))
      i += dv * w
      const t = k <= bias ? PC_TMIN : k >= bias + PC_TMAX ? PC_TMAX : k - bias
      if (dv < t) break
      w *= PC_BASE - t
    }
    const out = output.length
    bias = pcAdapt(i - oldi, out + 1, oldi === 0)
    n += Math.floor(i / (out + 1))
    i %= out + 1
    output.splice(i, 0, n)
    i++
  }
  return String.fromCharCode(...output)
}

export function idnEncode(domain) {
  return domain.trim().split(/\./).map(l =>
    /[^\x00-\x7F]/.test(l) ? punyEncodeLabel(l.toLowerCase()) : l).join('.')
}
export function idnDecode(domain) {
  return domain.trim().split(/\./).map(l =>
    /^xn--/i.test(l) ? punyDecodeLabel(l) : l).join('.')
}

/* ---------- 文本工具 ---------- */
export function countWords(text) {
  const cjk = (text.match(/[一-龥぀-ヿ가-힯]/g) || []).length
  const en = (text.replace(/[一-龥぀-ヿ가-힯]/g, ' ')
    .match(/[A-Za-z0-9_]+/g) || []).length
  return { chars: text.length, words: cjk + en, cjk, en,
    lines: text ? text.split(/\r\n|\r|\n/).length : 0,
    bytes: new TextEncoder().encode(text).length,
    noSpace: text.replace(/\s/g, '').length }
}

/* ---------- 行级 LCS Diff ---------- */
export function lineDiff(aText, bText) {
  const a = aText.split(/\r\n|\r|\n/), b = bText.split(/\r\n|\r|\n/)
  const MAX = 1200
  const big = Math.max(a.length, b.length) > MAX
  if (big) return null // 过大直接提示，避免 O(n·m) 卡死
  const n = a.length, m = b.length
  const dp = Array.from({ length: n + 1 }, () => new Uint16Array(m + 1))
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
  const rows = []
  let i = 0, j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) { rows.push({ t: '=', a: a[i] }); i++; j++ }
    else if (dp[i + 1][j] >= dp[i][j + 1]) { rows.push({ t: '-', a: a[i] }); i++ }
    else { rows.push({ t: '+', b: b[j] }); j++ }
  }
  while (i < n) rows.push({ t: '-', a: a[i++] })
  while (j < m) rows.push({ t: '+', b: b[j++] })
  return rows
}

/* ---------- Markdown ↔ HTML（基础语法，非完整实现） ---------- */
function escHtml(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}
// 链接/图片地址白名单：只放行 http(s)、mailto、站内相对路径与锚点，
// 防 [x](javascript:...) 这类协议注入（预览是 v-html，输入来自粘贴内容）
function safeMdUrl(u) {
  return /^(https?:\/\/|mailto:|\/|#)/i.test(u)
}
export function mdToHtml(md) {
  const lines = md.replace(/\r\n/g, '\n').split('\n')
  let html = '', inCode = false, inList = null, inQuote = false
  const closeList = () => { if (inList) { html += inList === 'ul' ? '</ul>\n' : '</ol>\n'; inList = null } }
  const closeQuote = () => { if (inQuote) { html += '</blockquote>\n'; inQuote = false } }
  const inline = (s) => s
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (m, alt, href) => safeMdUrl(href) ? `<img src="${href}" alt="${alt}">` : m)
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, (m, text, href) => safeMdUrl(href) ? `<a href="${href}" target="_blank" rel="noopener">${text}</a>` : m)
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/__([^_]+)__/g, '<strong>$1</strong>')
    .replace(/\*([^*]+)\*/g, '<em>$1</em>')
    .replace(/_([^_]+)_/g, '<em>$1</em>')
  for (const raw of lines) {
    const line = raw
    if (/^```/.test(line)) {
      if (inCode) { html += '</code></pre>\n'; inCode = false }
      else { closeList(); closeQuote(); html += '<pre><code>'; inCode = true }
      continue
    }
    if (inCode) { html += escHtml(line) + '\n'; continue }
    const h = line.match(/^(#{1,6})\s+(.*)$/)
    if (h) { closeList(); closeQuote(); html += `<h${h[1].length}>${inline(escHtml(h[2]))}</h${h[1].length}>\n`; continue }
    if (/^\s*(-{3,}|\*{3,})\s*$/.test(line)) { closeList(); closeQuote(); html += '<hr>\n'; continue }
    const q = line.match(/^>\s?(.*)$/)
    if (q) { closeList(); if (!inQuote) { html += '<blockquote>\n'; inQuote = true }
      html += `<p>${inline(escHtml(q[1]))}</p>\n`; continue }
    closeQuote()
    const ul = line.match(/^\s*[-*+]\s+(.*)$/)
    const ol = line.match(/^\s*\d+\.\s+(.*)$/)
    if (ul || ol) {
      const type = ul ? 'ul' : 'ol'
      if (inList !== type) { closeList(); html += `<${type}>\n`; inList = type }
      html += `<li>${inline(escHtml((ul || ol)[1]))}</li>\n`; continue
    }
    closeList()
    if (line.trim() === '') continue
    html += `<p>${inline(escHtml(line))}</p>\n`
  }
  if (inCode) html += '</code></pre>\n'
  closeList(); closeQuote()
  return html.trim()
}

export function htmlToMd(html) {
  let s = html.replace(/\r\n/g, '\n')
  s = s.replace(/<script[\s\S]*?<\/script>/gi, '').replace(/<style[\s\S]*?<\/style>/gi, '')
  s = s.replace(/<pre[^>]*>\s*<code[^>]*>([\s\S]*?)<\/code>\s*<\/pre>/gi, (_, c) =>
    `\n\`\`\`\n${c.replace(/<br\s*\/?>/gi, '\n')}\n\`\`\`\n`)
  s = s.replace(/<(h[1-6])[^>]*>([\s\S]*?)<\/\1>/gi, (_, tag, c) => {
    const lvl = +tag[1]
    return `\n${'#'.repeat(lvl)} ${c.replace(/<[^>]+>/g, '').trim()}\n`
  })
  s = s.replace(/<(strong|b)[^>]*>([\s\S]*?)<\/\1>/gi, '**$2**')
  s = s.replace(/<(em|i)[^>]*>([\s\S]*?)<\/\1>/gi, '*$2*')
  s = s.replace(/<a [^>]*href="([^"]*)"[^>]*>([\s\S]*?)<\/a>/gi, '[$2]($1)')
  s = s.replace(/<img [^>]*src="([^"]*)"[^>]*alt="([^"]*)"[^>]*>/gi, '![$2]($1)')
  s = s.replace(/<img [^>]*src="([^"]*)"[^>]*>/gi, '![]($1)')
  s = s.replace(/<code[^>]*>([\s\S]*?)<\/code>/gi, '`$1`')
  s = s.replace(/<li[^>]*>([\s\S]*?)<\/li>/gi, '- $1\n')
  s = s.replace(/<\/?(ul|ol)[^>]*>/gi, '\n')
  s = s.replace(/<blockquote[^>]*>/gi, '> ').replace(/<\/blockquote>/gi, '\n\n')
  s = s.replace(/<hr\s*\/?>/gi, '\n---\n')
  s = s.replace(/<br\s*\/?>/gi, '\n')
  s = s.replace(/<\/(p|div|table|tr|h[1-6])>/gi, '\n\n')
  s = s.replace(/<[^>]+>/g, '')
  s = s.replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&nbsp;/g, ' ')
  return s.replace(/\n{3,}/g, '\n\n').trim()
}

/* ---------- 简易 JS 格式化/压缩（字符串/注释感知，非完整 AST） ---------- */
function scanJs(src) {
  // 切分为 [代码|字符串|模板|注释] 片段，code 片段才做替换
  const parts = []
  let i = 0, code = ''
  const push = () => { if (code) { parts.push({ t: 'code', s: code }); code = '' } }
  while (i < src.length) {
    const ch = src[i], nx = src[i + 1]
    if (ch === '/' && nx === '/') {
      push(); let j = src.indexOf('\n', i); j = j === -1 ? src.length : j
      parts.push({ t: 'comment', s: src.slice(i, j) }); i = j; continue
    }
    if (ch === '/' && nx === '*') {
      push(); let j = src.indexOf('*/', i + 2); j = j === -1 ? src.length : j + 2
      parts.push({ t: 'comment', s: src.slice(i, j) }); i = j; continue
    }
    if (ch === '"' || ch === "'" || ch === '`') {
      push(); let j = i + 1
      while (j < src.length && src[j] !== ch) { if (src[j] === '\\') j++; j++ }
      j = Math.min(j + 1, src.length)
      parts.push({ t: ch === '`' ? 'tpl' : 'str', s: src.slice(i, j) }); i = j; continue
    }
    code += ch; i++
  }
  push()
  return parts
}

export function jsMinify(src) {
  let out = ''
  for (const p of scanJs(src)) {
    if (p.t === 'comment') continue
    if (p.t !== 'code') { out += p.s; continue } // 字符串内容原样保留，不做任何正则替换
    out += p.s
      .replace(/[ \t\r\n]+/g, ' ')
      .replace(/ ?([=;{},<>!&|?:%~^]) ?/g, '$1') // +-*/ 不紧贴，避免 a + +b 变 a++b
  }
  return out.trim()
}

export function jsFormat(src) {
  let indent = 0, res = '', line = ''
  const flush = () => { if (line.trim()) res += '  '.repeat(Math.max(0, indent)) + line.trim() + '\n'; line = '' }
  for (const p of scanJs(src)) {
    if (p.t === 'comment') continue
    if (p.t === 'str' || p.t === 'tpl') { line += p.s; continue } // 字符串原子保留（内部 ; { } 换行不处理）
    const s = p.s
    let i = 0
    while (i < s.length) {
      const ch = s[i]
      if (ch === '{') { line += '{'; flush(); indent++; i++; continue }
      if (ch === '}') { flush(); indent--; line = '}'; flush(); i++
        if (s[i] === ';') { line = '};'; flush(); i++ }
        continue }
      if (ch === ';') { line += ';'; flush(); i++; continue }
      if (ch === '\n') { i++; continue }
      line += ch; i++
    }
  }
  flush()
  return res.trim()
}

/* ---------- HTML/XML/CSS 格式化 ---------- */
export function htmlFormat(src, indentStr = '  ') {
  const noComment = src.replace(/<!--[\s\S]*?-->/g, m => '\x00C' + btoa(unescape(encodeURIComponent(m))) + 'C\x00')
  let depth = 0
  const tokens = noComment.replace(/>\s*</g, '>\n<').split('\n')
  const res = tokens.map(tok => {
    const comment = tok.match(/^\x00C(\S+)C\x00$/)
    if (comment) { tok = decodeURIComponent(escape(atob(comment[1]))) }
    const closing = /^<\//.test(tok)
    const selfClose = /\/>$/.test(tok) || /^<(!|input|br|img|hr|meta|link)\b/i.test(tok)
    if (closing) depth = Math.max(0, depth - 1)
    const line = indentStr.repeat(depth) + tok.trim()
    if (/^<[^/!][^>]*>$/.test(tok) && !selfClose && !/<\/[a-zA-Z]+>$/.test(tok)) depth++
    return line
  })
  return res.join('\n').replace(/\n{3,}/g, '\n\n').trim()
}

export function htmlMinify(src) {
  return src
    .replace(/<!--(?!\[if)[\s\S]*?-->/g, '')
    .replace(/>\s+</g, '><')
    .replace(/\s{2,}/g, ' ')
    .trim()
}

export function xmlFormat(src, indentStr = '  ') {
  const tokens = src.replace(/>\s*</g, '>\n<').split('\n')
  let depth = 0
  return tokens.map(tok => {
    tok = tok.trim()
    const closing = /^<\//.test(tok)
    const selfClose = /\/>$/.test(tok) || /^<\?/.test(tok)
    if (closing) depth = Math.max(0, depth - 1)
    const line = indentStr.repeat(depth) + tok
    if (/^<[^/!?][^>]*[^/]>$/.test(tok) && !/^<(!\[CDATA|!--)/.test(tok)) depth++
    return line
  }).join('\n').trim()
}

export function cssFormat(src) {
  const s = src.replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\s*\{\s*/g, ' {\n').replace(/\s*;\s*/g, ';\n')
    .replace(/\s*\}\s*/g, '\n}\n').replace(/,\s*\n/g, ',\n')
  let indent = 0
  return s.split('\n').map(l => {
    l = l.trim()
    if (!l) return ''
    if (l.startsWith('}')) { indent = Math.max(0, indent - 1); return '  '.repeat(indent) + l }
    const line = '  '.repeat(indent) + l
    if (l.endsWith('{')) indent++
    return line
  }).filter(Boolean).join('\n').replace(/\n{3,}/g, '\n\n').trim()
}

export function cssMinify(src) {
  return src.replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\s*([{}:;,>~])\s*/g, '$1')
    .replace(/;}/g, '}')
    .replace(/\s+/g, ' ')
    .trim()
}

/* ---------- HTML/JS 通用工具 ---------- */
export function escapeHtmlText(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}
