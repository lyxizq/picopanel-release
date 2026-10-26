<script setup>
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { countWords, lineDiff, copyOut } from './lib.js'
import { registerPageMessages } from '../../i18n'
import pageMsgs from '../../i18n/pages/toolbox.js'

defineProps({ tool: { type: String, required: true } })

const { t } = useI18n()
registerPageMessages(pageMsgs)

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
  if (!Number.isFinite(n)) { ts.out = t('toolbox.text.ts.errNaN'); return }
  const ms = Math.abs(n) >= 1e12 ? n : n * 1000
  const d = new Date(ms)
  if (isNaN(d.getTime())) { ts.out = t('toolbox.text.ts.errRange'); return }
  const pad = x => String(x).padStart(2, '0')
  ts.out = [
    t('toolbox.text.ts.outLocal', { v: `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}` }),
    t('toolbox.text.ts.outUtc', { v: d.toUTCString() }),
    t('toolbox.text.ts.outIso', { v: d.toISOString() }),
    t('toolbox.text.ts.outRel', { n: ((Date.now() - d.getTime()) / 1000 / 86400).toFixed(2) }),
  ].join('\n')
}
function dateToTs() {
  const d = new Date(ts.input.trim())
  if (isNaN(d.getTime())) { ElMessage.warning(t('toolbox.text.ts.warnDate')); return }
  ts.out = `${t('toolbox.text.ts.outSec', { v: Math.floor(d.getTime() / 1000) })}\n${t('toolbox.text.ts.outMs', { v: d.getTime() })}`
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
  if (!rgb || rgb.some(x => x > 255)) { col.out = t('toolbox.text.color.errFmt'); return }
  const [r, g, b] = rgb
  const [h, sa, l] = rgb2hsl(r, g, b)
  const hexv = '#' + rgb.map(x => x.toString(16).padStart(2, '0')).join('')
  col.out = `HEX：${hexv}\nRGB：rgb(${r}, ${g}, ${b})\nHSL：hsl(${h}, ${sa}%, ${l}%)`
  col.swatch = hexv
}

// 文本替换
const tr = reactive({ src: '', find: '', rep: '', regex: false, ignoreCase: false, out: '' })
function doReplace() {
  if (!tr.find) { ElMessage.warning(t('toolbox.text.replace.warnFind')); return }
  try {
    const pattern = tr.regex ? tr.find : tr.find.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const re = new RegExp(pattern, 'g' + (tr.ignoreCase ? 'i' : ''))
    tr.out = tr.src.replace(re, tr.rep)
  } catch (e) { ElMessage.error(t('toolbox.text.replace.errRe', { msg: e.message })) }
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
    [/QQBrowser\/([\d.]+)/, t('toolbox.text.ua.bQQ')],
    [/MicroMessenger\/([\d.]+)/, t('toolbox.text.ua.bWeChat')],
    [/QiHooBrowser/, t('toolbox.text.ua.b360')],
    [/UBrowser\/([\d.]+)/, t('toolbox.text.ua.bUC')],
    [/SamsungBrowser\/([\d.]+)/, t('toolbox.text.ua.bSamsung')],
    [/Firefox\/([\d.]+)/, 'Firefox'],
    [/Chrome\/([\d.]+)/, 'Chrome'],
    [/Version\/([\d.]+).*Safari/, 'Safari'],
    [/MSIE\s([\d.]+)|Trident\/.*rv:([\d.]+)/, 'IE'],
  ]
  let browser = t('toolbox.text.ua.unknown')
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
    /Linux/.test(s) ? 'Linux' : t('toolbox.text.ua.unknown')
  const engine =
    /Firefox|FxiOS/.test(s) ? 'Gecko' :
    /Chrome|Edg|OPR|SamsungBrowser/.test(s) ? 'Blink' :
    /AppleWebKit/.test(s) ? 'WebKit' :
    /Trident/.test(s) ? 'Trident' : t('toolbox.text.ua.unknown')
  const device = /Mobile|Android.*Mobile|iPhone/.test(s) ? t('toolbox.text.ua.devMobile')
    : /iPad|Tablet/.test(s) ? t('toolbox.text.ua.devTablet') : /Bot|Spider|Crawl/.test(s) ? t('toolbox.text.ua.devBot') : t('toolbox.text.ua.devDesktop')
  ua.out = [
    t('toolbox.text.ua.outBrowser', { v: browser }),
    t('toolbox.text.ua.outOs', { v: os }),
    t('toolbox.text.ua.outEngine', { v: engine }),
    t('toolbox.text.ua.outDevice', { v: device }),
  ].join('\n')
}
</script>

<template>
  <!-- Unix 时间戳 -->
  <template v-if="tool === 'timestamp'">
    <div class="tb-h">{{ $t('toolbox.text.ts.hint') }}</div>
    <div class="tb-row">
      <el-input v-model="ts.input" :placeholder="$t('toolbox.text.ts.phIn')" style="width:340px" @keyup.enter="convTs" />
      <el-button type="primary" @click="convTs">{{ $t('toolbox.text.ts.tsToDate') }}</el-button>
      <el-button @click="dateToTs">{{ $t('toolbox.text.ts.dateToTs') }}</el-button>
      <el-button @click="tsNow">{{ $t('toolbox.text.ts.now') }}</el-button>
      <el-button @click="copyOut(ts.out)">{{ $t('toolbox.text.copy') }}</el-button>
    </div>
    <el-input v-model="ts.out" type="textarea" :rows="5" readonly :placeholder="$t('toolbox.text.phOut')" />
  </template>

  <!-- RGB 颜色转换 -->
  <template v-if="tool === 'rgb-color'">
    <div class="tb-h">{{ $t('toolbox.text.color.hint') }}</div>
    <div class="tb-row">
      <el-input v-model="col.input" :placeholder="$t('toolbox.text.color.ph')" style="width:280px" @keyup.enter="convColor" />
      <el-button type="primary" @click="convColor">{{ $t('toolbox.text.convert') }}</el-button>
      <span class="tb-swatch" :style="{ background: col.swatch || 'transparent' }" />
    </div>
    <el-input v-model="col.out" type="textarea" :rows="3" readonly :placeholder="$t('toolbox.text.phOut')" />
  </template>

  <!-- 文本替换 -->
  <template v-if="tool === 'text-replace'">
    <div class="tb-h">{{ $t('toolbox.text.replace.hint') }}</div>
    <el-input v-model="tr.src" type="textarea" :rows="6" :placeholder="$t('toolbox.text.replace.phSrc')" />
    <div class="tb-row">
      <el-input v-model="tr.find" :placeholder="$t('toolbox.text.replace.phFind')" style="width:200px" />
      <el-input v-model="tr.rep" :placeholder="$t('toolbox.text.replace.phRep')" style="width:200px" />
      <el-checkbox v-model="tr.regex">{{ $t('toolbox.text.replace.regex') }}</el-checkbox>
      <el-checkbox v-model="tr.ignoreCase">{{ $t('toolbox.text.replace.ignoreCase') }}</el-checkbox>
      <el-button type="primary" @click="doReplace">{{ $t('toolbox.text.replace.run') }}</el-button>
      <el-button @click="copyOut(tr.out)">{{ $t('toolbox.text.copy') }}</el-button>
    </div>
    <el-input v-model="tr.out" type="textarea" :rows="6" readonly :placeholder="$t('toolbox.text.phOut')" />
  </template>

  <!-- 文本对比 -->
  <template v-if="tool === 'text-diff'">
    <div class="tb-h">{{ $t('toolbox.text.diff.hint') }}</div>
    <div class="tb-cols">
      <el-input v-model="df.a" type="textarea" :rows="8" :placeholder="$t('toolbox.text.diff.phA')" />
      <el-input v-model="df.b" type="textarea" :rows="8" :placeholder="$t('toolbox.text.diff.phB')" />
    </div>
    <div class="tb-row"><el-button type="primary" @click="doDiff">{{ $t('toolbox.text.diff.run') }}</el-button></div>
    <div v-if="df.big" class="tb-h">{{ $t('toolbox.text.diff.big') }}</div>
    <div v-if="df.rows" class="tb-diff">
      <div v-for="(r, i) in df.rows" :key="i" :class="['tb-dl', r.t === '-' ? 'tb-dl-del' : r.t === '+' ? 'tb-dl-add' : '']">
        <span class="tb-dl-mark">{{ r.t === '=' ? ' ' : r.t }}</span>
        <span class="tb-dl-txt">{{ r.t === '+' ? r.b : r.a || ' ' }}</span>
      </div>
    </div>
  </template>

  <!-- 字数统计 -->
  <template v-if="tool === 'word-count'">
    <div class="tb-h">{{ $t('toolbox.text.wc.hint') }}</div>
    <el-input v-model="wc.src" type="textarea" :rows="9" :placeholder="$t('toolbox.text.wc.phIn')" />
    <div class="tb-stats">
      <div><b>{{ wcStat.chars }}</b><span>{{ $t('toolbox.text.wc.chars') }}</span></div>
      <div><b>{{ wcStat.noSpace }}</b><span>{{ $t('toolbox.text.wc.noSpace') }}</span></div>
      <div><b>{{ wcStat.words }}</b><span>{{ $t('toolbox.text.wc.words') }}</span></div>
      <div><b>{{ wcStat.cjk }}</b><span>{{ $t('toolbox.text.wc.cjk') }}</span></div>
      <div><b>{{ wcStat.en }}</b><span>{{ $t('toolbox.text.wc.enWords') }}</span></div>
      <div><b>{{ wcStat.lines }}</b><span>{{ $t('toolbox.text.wc.lines') }}</span></div>
      <div><b>{{ wcStat.bytes }}</b><span>{{ $t('toolbox.text.wc.bytes') }}</span></div>
    </div>
  </template>

  <!-- UA 分析 -->
  <template v-if="tool === 'ua-parse'">
    <div class="tb-h">{{ $t('toolbox.text.ua.hint') }}</div>
    <el-input v-model="ua.src" type="textarea" :rows="4" placeholder="Mozilla/5.0 (Windows NT 10.0; Win64; x64) …" />
    <div class="tb-row">
      <el-button type="primary" @click="parseUa">{{ $t('toolbox.text.ua.run') }}</el-button>
      <el-button @click="copyOut(ua.out)">{{ $t('toolbox.text.copy') }}</el-button>
    </div>
    <el-input v-model="ua.out" type="textarea" :rows="4" readonly :placeholder="$t('toolbox.text.phOut')" />
  </template>
</template>
