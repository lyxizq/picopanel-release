<script setup>
import { computed, reactive } from 'vue'
import { ElMessage } from 'element-plus'
import { countWords, lineDiff, copyOut } from './lib.js'

defineProps({ tool: { type: String, required: true } })

// Unix 时间戳
const ts = reactive({ input: '', out: '' })
function tsNow() {
  const d = new Date()
  ts.input = String(Math.floor(d.getTime() / 1000))
  convTs()
}
function convTs() {
  const v = ts.input.trim()
  if (!v) { ts.out = ''; return }
  const n = Number(v)
  if (!Number.isFinite(n)) { ts.out = '不是有效数字'; return }
  const ms = Math.abs(n) >= 1e12 ? n : n * 1000
  const d = new Date(ms)
  if (isNaN(d.getTime())) { ts.out = '超出日期范围'; return }
  const pad = x => String(x).padStart(2, '0')
  ts.out = [
    `本地时间：${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`,
    `UTC　时间：${d.toUTCString()}`,
    `ISO 8601：${d.toISOString()}`,
    `相对现在：${((Date.now() - d.getTime()) / 1000 / 86400).toFixed(2)} 天前（负数为未来）`,
  ].join('\n')
}
function dateToTs() {
  const d = new Date(ts.input.trim())
  if (isNaN(d.getTime())) { ElMessage.warning('无法解析为日期，请输入如 2026-09-28 12:00:00 或 ISO 格式'); return }
  ts.out = `秒级：${Math.floor(d.getTime() / 1000)}\n毫秒级：${d.getTime()}`
}

// RGB 颜色转换
const col = reactive({ input: '#409eff', out: '' })
function hex2rgb(h) {
  const s = h.replace('#', '').trim()
  const v = s.length === 3 ? [...s].map(c => c + c).join('') : s
  if (!/^[0-9a-fA-F]{6}$/.test(v)) return null
  return [parseInt(v.slice(0, 2), 16), parseInt(v.slice(2, 4), 16), parseInt(v.slice(4, 6), 16)]
}
function rgb2hsl(r, g, b) {
  r /= 255; g /= 255; b /= 255
  const mx = Math.max(r, g, b), mn = Math.min(r, g, b), l = (mx + mn) / 2
  if (mx === mn) return [0, 0, Math.round(l * 100)]
  const d = mx - mn
  const s = l > 0.5 ? d / (2 - mx - mn) : d / (mx + mn)
  let h = mx === r ? (g - b) / d + (g < b ? 6 : 0) : mx === g ? (b - r) / d + 2 : (r - g) / d + 4
  return [Math.round(h * 60), Math.round(s * 100), Math.round(l * 100)]
}
function convColor() {
  const s = col.input.trim()
  let rgb = null
  const m = s.match(/rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/i)
  if (m) rgb = [+m[1], +m[2], +m[3]]
  else if (/^#?[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$/.test(s)) rgb = hex2rgb(s)
  if (!rgb || rgb.some(x => x > 255)) { col.out = '无法识别，请输入 #RRGGBB 或 rgb(r,g,b)'; return }
  const [r, g, b] = rgb
  const [h, sa, l] = rgb2hsl(r, g, b)
  const hexv = '#' + rgb.map(x => x.toString(16).padStart(2, '0')).join('')
  col.out = `HEX：${hexv}\nRGB：rgb(${r}, ${g}, ${b})\nHSL：hsl(${h}, ${sa}%, ${l}%)`
  col.swatch = hexv
}

// 文本替换
const tr = reactive({ src: '', find: '', rep: '', regex: false, ignoreCase: false, out: '' })
function doReplace() {
  if (!tr.find) { ElMessage.warning('请输入查找内容'); return }
  try {
    const pattern = tr.regex ? tr.find : tr.find.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const re = new RegExp(pattern, 'g' + (tr.ignoreCase ? 'i' : ''))
    tr.out = tr.src.replace(re, tr.rep)
  } catch (e) { ElMessage.error('正则表达式无效：' + e.message) }
}

// 文本对比
const df = reactive({ a: '', b: '', rows: null, big: false })
function doDiff() {
  const rows = lineDiff(df.a, df.b)
  if (rows === null) { df.big = true; df.rows = null; return }
  df.big = false
  df.rows = rows
}

// 字数统计
const wc = reactive({ src: '' })
const wcStat = computed(() => countWords(wc.src))

// UA 分析
const ua = reactive({ src: '', out: '' })
function parseUa() {
  const s = ua.src.trim()
  if (!s) { ua.out = ''; return }
  const rules = [
    [/Edg(?:e|A|iOS)?\/([\d.]+)/, 'Edge'],
    [/OPR\/([\d.]+)/, 'Opera'],
    [/QQBrowser\/([\d.]+)/, 'QQ 浏览器'],
    [/MicroMessenger\/([\d.]+)/, '微信内置浏览器'],
    [/QiHooBrowser/, '360 浏览器'],
    [/UBrowser\/([\d.]+)/, 'UC 浏览器'],
    [/SamsungBrowser\/([\d.]+)/, '三星浏览器'],
    [/Firefox\/([\d.]+)/, 'Firefox'],
    [/Chrome\/([\d.]+)/, 'Chrome'],
    [/Version\/([\d.]+).*Safari/, 'Safari'],
    [/MSIE\s([\d.]+)|Trident\/.*rv:([\d.]+)/, 'IE'],
  ]
  let browser = '未知'
  for (const [re, name] of rules) {
    const m = s.match(re)
    if (m) { browser = `${name} ${m[1] || m[2] || ''}`.trim(); break }
  }
  const and = /Android ([\d.]+)/.exec(s)
  const mac = /Mac OS X ([\d_.]+)/.exec(s)
  const os =
    /Windows NT 10\.0|Windows NT 11\.0/.test(s) ? 'Windows 10/11' :
    /Windows NT 6\.1/.test(s) ? 'Windows 7' :
    /Windows/.test(s) ? 'Windows' :
    and ? `Android ${and[1]}` :
    /iPhone|iPad|iPod/.test(s) ? 'iOS' :
    mac ? `macOS ${mac[1].replace(/_/g, '.')}` :
    /CrOS/.test(s) ? 'ChromeOS' :
    /Linux/.test(s) ? 'Linux' : '未知'
  const engine =
    /Firefox|FxiOS/.test(s) ? 'Gecko' :
    /Chrome|Edg|OPR|SamsungBrowser/.test(s) ? 'Blink' :
    /AppleWebKit/.test(s) ? 'WebKit' :
    /Trident/.test(s) ? 'Trident' : '未知'
  const device = /Mobile|Android.*Mobile|iPhone/.test(s) ? '手机'
    : /iPad|Tablet/.test(s) ? '平板' : /Bot|Spider|Crawl/.test(s) ? '爬虫' : '桌面'
  ua.out = `浏览器：${browser}\n操作系统：${os}\n渲染引擎：${engine}\n设备类型：${device}`
}
</script>

<template>
  <!-- Unix 时间戳 -->
  <template v-if="tool === 'timestamp'">
    <div class="tb-h">Unix 时间戳 ↔ 日期互转（自动识别秒/毫秒）。</div>
    <div class="tb-row">
      <el-input v-model="ts.input" placeholder="时间戳或日期（2026-09-28 12:00:00）" style="width:340px" @keyup.enter="convTs" />
      <el-button type="primary" @click="convTs">时间戳 → 日期</el-button>
      <el-button @click="dateToTs">日期 → 时间戳</el-button>
      <el-button @click="tsNow">当前时间戳</el-button>
      <el-button @click="copyOut(ts.out)">复制</el-button>
    </div>
    <el-input v-model="ts.out" type="textarea" :rows="5" readonly placeholder="结果…" />
  </template>

  <!-- RGB 颜色转换 -->
  <template v-if="tool === 'rgb-color'">
    <div class="tb-h">HEX / RGB / HSL 颜色值互转。</div>
    <div class="tb-row">
      <el-input v-model="col.input" placeholder="#409eff 或 rgb(64,158,255)" style="width:280px" @keyup.enter="convColor" />
      <el-button type="primary" @click="convColor">转换</el-button>
      <span class="tb-swatch" :style="{ background: col.swatch || 'transparent' }" />
    </div>
    <el-input v-model="col.out" type="textarea" :rows="3" readonly placeholder="结果…" />
  </template>

  <!-- 文本替换 -->
  <template v-if="tool === 'text-replace'">
    <div class="tb-h">批量文本替换，支持普通文本与正则表达式。</div>
    <el-input v-model="tr.src" type="textarea" :rows="6" placeholder="原文…" />
    <div class="tb-row">
      <el-input v-model="tr.find" placeholder="查找" style="width:200px" />
      <el-input v-model="tr.rep" placeholder="替换为" style="width:200px" />
      <el-checkbox v-model="tr.regex">正则</el-checkbox>
      <el-checkbox v-model="tr.ignoreCase">忽略大小写</el-checkbox>
      <el-button type="primary" @click="doReplace">替换</el-button>
      <el-button @click="copyOut(tr.out)">复制</el-button>
    </div>
    <el-input v-model="tr.out" type="textarea" :rows="6" readonly placeholder="结果…" />
  </template>

  <!-- 文本对比 -->
  <template v-if="tool === 'text-diff'">
    <div class="tb-h">两段文本按行对比（红色删除 / 绿色新增，最多 1200 行）。</div>
    <div class="tb-cols">
      <el-input v-model="df.a" type="textarea" :rows="8" placeholder="原文本…" />
      <el-input v-model="df.b" type="textarea" :rows="8" placeholder="新文本…" />
    </div>
    <div class="tb-row"><el-button type="primary" @click="doDiff">对比</el-button></div>
    <div v-if="df.big" class="tb-h">文本过大，请分段对比（每侧 ≤1200 行）。</div>
    <div v-if="df.rows" class="tb-diff">
      <div v-for="(r, i) in df.rows" :key="i" :class="['tb-dl', r.t === '-' ? 'tb-dl-del' : r.t === '+' ? 'tb-dl-add' : '']">
        <span class="tb-dl-mark">{{ r.t === '=' ? ' ' : r.t }}</span>
        <span class="tb-dl-txt">{{ r.t === '+' ? r.b : r.a || ' ' }}</span>
      </div>
    </div>
  </template>

  <!-- 字数统计 -->
  <template v-if="tool === 'word-count'">
    <div class="tb-h">实时统计字符/字数/行数/字节数。</div>
    <el-input v-model="wc.src" type="textarea" :rows="9" placeholder="输入或粘贴文本…" />
    <div class="tb-stats">
      <div><b>{{ wcStat.chars }}</b><span>字符数</span></div>
      <div><b>{{ wcStat.noSpace }}</b><span>不含空白</span></div>
      <div><b>{{ wcStat.words }}</b><span>词数（中西混合）</span></div>
      <div><b>{{ wcStat.cjk }}</b><span>中文/日文/韩文字</span></div>
      <div><b>{{ wcStat.en }}</b><span>英文单词</span></div>
      <div><b>{{ wcStat.lines }}</b><span>行数</span></div>
      <div><b>{{ wcStat.bytes }}</b><span>UTF-8 字节</span></div>
    </div>
  </template>

  <!-- UA 分析 -->
  <template v-if="tool === 'ua-parse'">
    <div class="tb-h">解析 User-Agent 识别浏览器/系统/引擎/设备。</div>
    <el-input v-model="ua.src" type="textarea" :rows="4" placeholder="Mozilla/5.0 (Windows NT 10.0; Win64; x64) …" />
    <div class="tb-row">
      <el-button type="primary" @click="parseUa">解析</el-button>
      <el-button @click="copyOut(ua.out)">复制</el-button>
    </div>
    <el-input v-model="ua.out" type="textarea" :rows="4" readonly placeholder="结果…" />
  </template>
</template>
