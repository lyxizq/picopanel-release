<script setup>
import { reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { jsFormat, jsMinify, htmlFormat, htmlMinify, xmlFormat, cssFormat, cssMinify,
  mdToHtml, htmlToMd, copyOut, escapeHtmlText } from './lib.js'
import { registerPageMessages } from '../../i18n'
import pageMsgs from '../../i18n/pages/toolbox.js'

defineProps({ tool: { type: String, required: true } })

const { t } = useI18n()
registerPageMessages(pageMsgs)

// JSON 格式化
const jf = reactive({ src: '', indent: '2', out: '' })
function fmtJson() {
  try {
    const obj = JSON.parse(jf.src)
    jf.out = JSON.stringify(obj, null, jf.indent === 'tab' ? '\t' : +jf.indent)
  } catch (e) { ElMessage.error(t('toolbox.format.json.errJson', { msg: e.message.slice(0, 120) })) }
}

// JSON 压缩转义
const jc = reactive({ src: '', unicode: false, out: '' })
function minJson() {
  try {
    const obj = JSON.parse(jc.src)
    let s = JSON.stringify(obj)
    if (jc.unicode) {
      s = s.replace(/[\u0080-￿]/g, ch => '\\u' + ch.charCodeAt(0).toString(16).padStart(4, '0'))
    }
    jc.out = s
  } catch (e) { ElMessage.error(t('toolbox.format.json.errJson', { msg: e.message.slice(0, 120) })) }
}

// HTML 格式化/压缩
const hf = reactive({ src: '', mode: 'format', out: '' })
function doHtml() {
  try {
    hf.out = hf.mode === 'format' ? htmlFormat(hf.src) : htmlMinify(hf.src)
  } catch (e) { ElMessage.error(t('toolbox.format.errProc', { msg: e.message })) }
}

// XML 格式化
const xf = reactive({ src: '', out: '' })
function doXml() {
  try {
    const doc = new DOMParser().parseFromString(xf.src, 'application/xml')
    if (doc.querySelector('parsererror')) { ElMessage.error(t('toolbox.format.xml.errXml')); return }
    xf.out = xmlFormat(xf.src)
  } catch (e) { ElMessage.error(t('toolbox.format.errProc', { msg: e.message })) }
}

// CSS 格式化/压缩
const cf = reactive({ src: '', mode: 'format', out: '' })
function doCss() {
  cf.out = cf.mode === 'format' ? cssFormat(cf.src) : cssMinify(cf.src)
}

// CSS 渐变生成器
const gr = reactive({ type: 'linear', angle: 135, c1: '#409eff', p1: 0, c2: '#67c23a', p2: 60, c3On: false, c3: '#f56c6c', p3: 100 })
const gradCss = computed(() => {
  const stops = [[gr.c1, gr.p1], [gr.c2, gr.p2]]
  if (gr.c3On) stops.push([gr.c3, gr.p3])
  const s = stops.map(([c, p]) => `${c} ${p}%`).join(', ')
  return gr.type === 'linear' ? `linear-gradient(${gr.angle}deg, ${s})` : `radial-gradient(circle, ${s})`
})

// JS 格式化/压缩
const jsn = reactive({ src: '', mode: 'format', out: '' })
function doJs() {
  try {
    jsn.out = jsn.mode === 'format' ? jsFormat(jsn.src) : jsMinify(jsn.src)
  } catch (e) { ElMessage.error(t('toolbox.format.errProc', { msg: e.message })) }
}

// 正则测试
const rg = reactive({ pattern: '', flags: ['g'], text: '', matches: [], err: '' })
const rgRe = computed(() => {
  const f = [...new Set(['g', ...rg.flags])].join('')
  return new RegExp(rg.pattern, f)
})
function testRegex() {
  rg.err = ''
  rg.matches = []
  if (!rg.pattern) return
  let re
  try { re = rgRe.value } catch (e) { rg.err = t('toolbox.format.regex.errRe', { msg: e.message }); return }
  if (!rg.flags.includes('g')) {
    const m = re.exec(rg.text)
    if (m) rg.matches = [m]
    return
  }
  let m, guard = 0
  while ((m = re.exec(rg.text)) !== null) {
    rg.matches.push(m)
    if (m[0] === '') { re.lastIndex++; if (++guard > 100000) break; continue }
    if (++guard > 5000) { rg.err = t('toolbox.format.regex.errMany'); break }
  }
}
const rgHtml = computed(() => {
  if (!rg.matches.length || rg.err) return escapeHtmlText(rg.text)
  let out = '', last = 0
  for (const m of rg.matches) {
    out += escapeHtmlText(rg.text.slice(last, m.index))
    out += '<mark>' + escapeHtmlText(m[0]) + '</mark>'
    last = m.index + m[0].length
  }
  out += escapeHtmlText(rg.text.slice(last))
  return out
})

// HTML/Markdown 互转
const mh = reactive({ src: '', mode: 'md2h', out: '', preview: false })
function convMd() {
  mh.out = mh.mode === 'md2h' ? mdToHtml(mh.src) : htmlToMd(mh.src)
}
watch(() => mh.mode, () => { mh.out = ''; mh.preview = false })
</script>

<template>
  <!-- JSON 格式化 -->
  <template v-if="tool === 'json-format'">
    <div class="tb-h">{{ $t('toolbox.format.json.hint') }}</div>
    <el-input v-model="jf.src" type="textarea" :rows="8" placeholder='{"a":1,"b":[2,3]}' />
    <div class="tb-row">
      <el-select v-model="jf.indent" style="width:110px">
        <el-option :label="$t('toolbox.format.json.indent2')" value="2" />
        <el-option :label="$t('toolbox.format.json.indent4')" value="4" />
        <el-option :label="$t('toolbox.format.json.indentTab')" value="tab" />
      </el-select>
      <el-button type="primary" @click="fmtJson">{{ $t('toolbox.format.fmt') }}</el-button>
      <el-button @click="copyOut(jf.out)">{{ $t('toolbox.format.copy') }}</el-button>
    </div>
    <el-input v-model="jf.out" type="textarea" :rows="8" readonly :placeholder="$t('toolbox.format.phOut')" />
  </template>

  <!-- JSON 压缩 -->
  <template v-if="tool === 'json-compress'">
    <div class="tb-h">{{ $t('toolbox.format.jsonC.hint') }}</div>
    <el-input v-model="jc.src" type="textarea" :rows="8" :placeholder="$t('toolbox.format.jsonC.phIn')" />
    <div class="tb-row">
      <el-checkbox v-model="jc.unicode">{{ $t('toolbox.format.jsonC.unicode') }}</el-checkbox>
      <el-button type="primary" @click="minJson">{{ $t('toolbox.format.min') }}</el-button>
      <el-button @click="copyOut(jc.out)">{{ $t('toolbox.format.copy') }}</el-button>
    </div>
    <el-input v-model="jc.out" type="textarea" :rows="6" readonly :placeholder="$t('toolbox.format.phOut')" />
  </template>

  <!-- HTML 格式化 -->
  <template v-if="tool === 'html-format'">
    <div class="tb-h">{{ $t('toolbox.format.html.hint') }}</div>
    <el-input v-model="hf.src" type="textarea" :rows="8" :placeholder="$t('toolbox.format.html.phIn')" />
    <div class="tb-row">
      <el-radio-group v-model="hf.mode">
        <el-radio-button value="format">{{ $t('toolbox.format.fmt') }}</el-radio-button>
        <el-radio-button value="minify">{{ $t('toolbox.format.min') }}</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="doHtml">{{ $t('toolbox.format.run') }}</el-button>
      <el-button @click="copyOut(hf.out)">{{ $t('toolbox.format.copy') }}</el-button>
    </div>
    <el-input v-model="hf.out" type="textarea" :rows="8" readonly :placeholder="$t('toolbox.format.phOut')" />
  </template>

  <!-- XML 格式化 -->
  <template v-if="tool === 'xml-format'">
    <div class="tb-h">{{ $t('toolbox.format.xml.hint') }}</div>
    <el-input v-model="xf.src" type="textarea" :rows="8" :placeholder="$t('toolbox.format.xml.phIn')" />
    <div class="tb-row">
      <el-button type="primary" @click="doXml">{{ $t('toolbox.format.fmt') }}</el-button>
      <el-button @click="copyOut(xf.out)">{{ $t('toolbox.format.copy') }}</el-button>
    </div>
    <el-input v-model="xf.out" type="textarea" :rows="8" readonly :placeholder="$t('toolbox.format.phOut')" />
  </template>

  <!-- CSS 格式化 -->
  <template v-if="tool === 'css-format'">
    <div class="tb-h">{{ $t('toolbox.format.css.hint') }}</div>
    <el-input v-model="cf.src" type="textarea" :rows="8" placeholder="body { color: red; }" />
    <div class="tb-row">
      <el-radio-group v-model="cf.mode">
        <el-radio-button value="format">{{ $t('toolbox.format.fmt') }}</el-radio-button>
        <el-radio-button value="minify">{{ $t('toolbox.format.min') }}</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="doCss">{{ $t('toolbox.format.run') }}</el-button>
      <el-button @click="copyOut(cf.out)">{{ $t('toolbox.format.copy') }}</el-button>
    </div>
    <el-input v-model="cf.out" type="textarea" :rows="8" readonly :placeholder="$t('toolbox.format.phOut')" />
  </template>

  <!-- CSS 渐变生成器 -->
  <template v-if="tool === 'css-gradient'">
    <div class="tb-h">{{ $t('toolbox.format.grad.hint') }}</div>
    <div class="tb-row">
      <el-radio-group v-model="gr.type">
        <el-radio-button value="linear">{{ $t('toolbox.format.grad.linear') }}</el-radio-button>
        <el-radio-button value="radial">{{ $t('toolbox.format.grad.radial') }}</el-radio-button>
      </el-radio-group>
      <span v-if="gr.type === 'linear'" class="tb-lb">{{ $t('toolbox.format.grad.angle', { n: gr.angle }) }}</span>
      <el-slider v-if="gr.type === 'linear'" v-model="gr.angle" :min="0" :max="360" style="width:180px" />
    </div>
    <div class="tb-row">
      <input type="color" v-model="gr.c1" /><el-input-number v-model="gr.p1" :min="0" :max="100" :controls="false" style="width:70px" />
      <input type="color" v-model="gr.c2" /><el-input-number v-model="gr.p2" :min="0" :max="100" :controls="false" style="width:70px" />
      <el-checkbox v-model="gr.c3On">{{ $t('toolbox.format.grad.third') }}</el-checkbox>
      <template v-if="gr.c3On">
        <input type="color" v-model="gr.c3" /><el-input-number v-model="gr.p3" :min="0" :max="100" :controls="false" style="width:70px" />
      </template>
    </div>
    <div class="tb-grad-preview" :style="{ background: gradCss }" />
    <div class="tb-row">
      <el-input :model-value="`background: ${gradCss};`" readonly style="flex:1" />
      <el-button @click="copyOut(`background: ${gradCss};`)">{{ $t('toolbox.format.copy') }}</el-button>
    </div>
  </template>

  <!-- JS 格式化 -->
  <template v-if="tool === 'js-format'">
    <div class="tb-h">{{ $t('toolbox.format.js.hint') }}</div>
    <el-input v-model="jsn.src" type="textarea" :rows="8" :placeholder="$t('toolbox.format.js.phIn')" />
    <div class="tb-row">
      <el-radio-group v-model="jsn.mode">
        <el-radio-button value="format">{{ $t('toolbox.format.fmt') }}</el-radio-button>
        <el-radio-button value="minify">{{ $t('toolbox.format.min') }}</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="doJs">{{ $t('toolbox.format.run') }}</el-button>
      <el-button @click="copyOut(jsn.out)">{{ $t('toolbox.format.copy') }}</el-button>
    </div>
    <el-input v-model="jsn.out" type="textarea" :rows="8" readonly :placeholder="$t('toolbox.format.phOut')" />
  </template>

  <!-- 正则测试 -->
  <template v-if="tool === 'regex-test'">
    <div class="tb-h">{{ $t('toolbox.format.regex.hint') }}</div>
    <div class="tb-row">
      <span class="tb-lb">/</span>
      <el-input v-model="rg.pattern" :placeholder="$t('toolbox.format.regex.phPattern')" style="width:320px" @input="testRegex" />
      <span class="tb-lb">/</span>
      <el-checkbox-group v-model="rg.flags" @change="testRegex">
        <el-checkbox value="i">{{ $t('toolbox.format.regex.fI') }}</el-checkbox>
        <el-checkbox value="m">{{ $t('toolbox.format.regex.fM') }}</el-checkbox>
        <el-checkbox value="s">{{ $t('toolbox.format.regex.fS') }}</el-checkbox>
      </el-checkbox-group>
    </div>
    <el-input v-model="rg.text" type="textarea" :rows="7" :placeholder="$t('toolbox.format.regex.phText')" @input="testRegex" />
    <div v-if="rg.err" class="tb-err">{{ rg.err }}</div>
    <div class="tb-lb">{{ $t('toolbox.format.regex.hits', { n: rg.matches.length }) }}</div>
    <div class="tb-regex-out" v-html="rgHtml" />
    <el-table v-if="rg.matches.length" :data="rg.matches.slice(0, 50)" size="small" max-height="260">
      <el-table-column label="#" width="60"><template #default="{ $index }">{{ $index + 1 }}</template></el-table-column>
      <el-table-column :label="$t('toolbox.format.regex.colMatch')" min-width="180"><template #default="{ row }">{{ row[0] }}</template></el-table-column>
      <el-table-column :label="$t('toolbox.format.regex.colPos')" width="90"><template #default="{ row }">{{ row.index }}</template></el-table-column>
      <el-table-column :label="$t('toolbox.format.regex.colGroups')" min-width="160"><template #default="{ row }">{{ row.slice(1).map((g, i) => (i + 1) + ': ' + (g ?? '-')).join('　') }}</template></el-table-column>
    </el-table>
  </template>

  <!-- HTML/Markdown 互转 -->
  <template v-if="tool === 'md-html'">
    <div class="tb-h">{{ $t('toolbox.format.mdHtml.hint') }}</div>
    <el-input v-model="mh.src" type="textarea" :rows="8" :placeholder="$t('toolbox.format.mdHtml.phIn')" />
    <div class="tb-row">
      <el-radio-group v-model="mh.mode">
        <el-radio-button value="md2h">{{ $t('toolbox.format.mdHtml.m2h') }}</el-radio-button>
        <el-radio-button value="h2m">{{ $t('toolbox.format.mdHtml.h2m') }}</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="convMd">{{ $t('toolbox.format.convert') }}</el-button>
      <el-button @click="copyOut(mh.out)">{{ $t('toolbox.format.copy') }}</el-button>
      <el-checkbox v-if="mh.mode === 'md2h'" v-model="mh.preview">{{ $t('toolbox.format.mdHtml.preview') }}</el-checkbox>
    </div>
    <el-input v-if="!mh.preview || mh.mode !== 'md2h'" v-model="mh.out" type="textarea" :rows="8" readonly :placeholder="$t('toolbox.format.phOut')" />
    <div v-else class="tb-md-preview" v-html="mh.out" />
  </template>
</template>
