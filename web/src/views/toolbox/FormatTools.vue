<script setup>
import { reactive, computed, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { jsFormat, jsMinify, htmlFormat, htmlMinify, xmlFormat, cssFormat, cssMinify,
  mdToHtml, htmlToMd, copyOut, escapeHtmlText } from './lib.js'

defineProps({ tool: { type: String, required: true } })

// JSON 格式化
const jf = reactive({ src: '', indent: '2', out: '' })
function fmtJson() {
  try {
    const obj = JSON.parse(jf.src)
    jf.out = JSON.stringify(obj, null, jf.indent === 'tab' ? '\t' : +jf.indent)
  } catch (e) { ElMessage.error('JSON 无效：' + e.message.slice(0, 120)) }
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
  } catch (e) { ElMessage.error('JSON 无效：' + e.message.slice(0, 120)) }
}

// HTML 格式化/压缩
const hf = reactive({ src: '', mode: 'format', out: '' })
function doHtml() {
  try {
    hf.out = hf.mode === 'format' ? htmlFormat(hf.src) : htmlMinify(hf.src)
  } catch (e) { ElMessage.error('处理失败：' + e.message) }
}

// XML 格式化
const xf = reactive({ src: '', out: '' })
function doXml() {
  try {
    const doc = new DOMParser().parseFromString(xf.src, 'application/xml')
    if (doc.querySelector('parsererror')) { ElMessage.error('XML 无效'); return }
    xf.out = xmlFormat(xf.src)
  } catch (e) { ElMessage.error('处理失败：' + e.message) }
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
  } catch (e) { ElMessage.error('处理失败：' + e.message) }
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
  try { re = rgRe.value } catch (e) { rg.err = '正则无效：' + e.message; return }
  if (!rg.flags.includes('g')) {
    const m = re.exec(rg.text)
    if (m) rg.matches = [m]
    return
  }
  let m, guard = 0
  while ((m = re.exec(rg.text)) !== null) {
    rg.matches.push(m)
    if (m[0] === '') { re.lastIndex++; if (++guard > 100000) break; continue }
    if (++guard > 5000) { rg.err = '匹配过多，仅显示前 5000 个'; break }
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
    <div class="tb-h">JSON 美化格式化，可切换缩进。</div>
    <el-input v-model="jf.src" type="textarea" :rows="8" placeholder='{"a":1,"b":[2,3]}' />
    <div class="tb-row">
      <el-select v-model="jf.indent" style="width:110px">
        <el-option label="缩进 2" value="2" />
        <el-option label="缩进 4" value="4" />
        <el-option label="缩进 Tab" value="tab" />
      </el-select>
      <el-button type="primary" @click="fmtJson">格式化</el-button>
      <el-button @click="copyOut(jf.out)">复制</el-button>
    </div>
    <el-input v-model="jf.out" type="textarea" :rows="8" readonly placeholder="结果…" />
  </template>

  <!-- JSON 压缩 -->
  <template v-if="tool === 'json-compress'">
    <div class="tb-h">JSON 压缩成单行，可选非 ASCII 字符转 \\uXXXX 转义。</div>
    <el-input v-model="jc.src" type="textarea" :rows="8" placeholder="粘贴 JSON…" />
    <div class="tb-row">
      <el-checkbox v-model="jc.unicode">中文转 \\u 转义</el-checkbox>
      <el-button type="primary" @click="minJson">压缩</el-button>
      <el-button @click="copyOut(jc.out)">复制</el-button>
    </div>
    <el-input v-model="jc.out" type="textarea" :rows="6" readonly placeholder="结果…" />
  </template>

  <!-- HTML 格式化 -->
  <template v-if="tool === 'html-format'">
    <div class="tb-h">HTML 美化缩进 / 压缩（去注释、去标签间空白）。</div>
    <el-input v-model="hf.src" type="textarea" :rows="8" placeholder="粘贴 HTML…" />
    <div class="tb-row">
      <el-radio-group v-model="hf.mode">
        <el-radio-button value="format">格式化</el-radio-button>
        <el-radio-button value="minify">压缩</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="doHtml">执行</el-button>
      <el-button @click="copyOut(hf.out)">复制</el-button>
    </div>
    <el-input v-model="hf.out" type="textarea" :rows="8" readonly placeholder="结果…" />
  </template>

  <!-- XML 格式化 -->
  <template v-if="tool === 'xml-format'">
    <div class="tb-h">XML 美化缩进（先校验合法性）。</div>
    <el-input v-model="xf.src" type="textarea" :rows="8" placeholder="粘贴 XML…" />
    <div class="tb-row">
      <el-button type="primary" @click="doXml">格式化</el-button>
      <el-button @click="copyOut(xf.out)">复制</el-button>
    </div>
    <el-input v-model="xf.out" type="textarea" :rows="8" readonly placeholder="结果…" />
  </template>

  <!-- CSS 格式化 -->
  <template v-if="tool === 'css-format'">
    <div class="tb-h">CSS 美化缩进 / 压缩（去注释）。</div>
    <el-input v-model="cf.src" type="textarea" :rows="8" placeholder="body { color: red; }" />
    <div class="tb-row">
      <el-radio-group v-model="cf.mode">
        <el-radio-button value="format">格式化</el-radio-button>
        <el-radio-button value="minify">压缩</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="doCss">执行</el-button>
      <el-button @click="copyOut(cf.out)">复制</el-button>
    </div>
    <el-input v-model="cf.out" type="textarea" :rows="8" readonly placeholder="结果…" />
  </template>

  <!-- CSS 渐变生成器 -->
  <template v-if="tool === 'css-gradient'">
    <div class="tb-h">可视化生成 CSS 渐变，实时预览。</div>
    <div class="tb-row">
      <el-radio-group v-model="gr.type">
        <el-radio-button value="linear">线性</el-radio-button>
        <el-radio-button value="radial">径向</el-radio-button>
      </el-radio-group>
      <span v-if="gr.type === 'linear'" class="tb-lb">角度 {{ gr.angle }}°</span>
      <el-slider v-if="gr.type === 'linear'" v-model="gr.angle" :min="0" :max="360" style="width:180px" />
    </div>
    <div class="tb-row">
      <input type="color" v-model="gr.c1" /><el-input-number v-model="gr.p1" :min="0" :max="100" :controls="false" style="width:70px" />
      <input type="color" v-model="gr.c2" /><el-input-number v-model="gr.p2" :min="0" :max="100" :controls="false" style="width:70px" />
      <el-checkbox v-model="gr.c3On">第三色</el-checkbox>
      <template v-if="gr.c3On">
        <input type="color" v-model="gr.c3" /><el-input-number v-model="gr.p3" :min="0" :max="100" :controls="false" style="width:70px" />
      </template>
    </div>
    <div class="tb-grad-preview" :style="{ background: gradCss }" />
    <div class="tb-row">
      <el-input :model-value="`background: ${gradCss};`" readonly style="flex:1" />
      <el-button @click="copyOut(`background: ${gradCss};`)">复制</el-button>
    </div>
  </template>

  <!-- JS 格式化 -->
  <template v-if="tool === 'js-format'">
    <div class="tb-h">JS 简易格式化 / 压缩（字符串感知；复杂源码建议本地工具处理）。</div>
    <el-input v-model="jsn.src" type="textarea" :rows="8" placeholder="粘贴 JavaScript…" />
    <div class="tb-row">
      <el-radio-group v-model="jsn.mode">
        <el-radio-button value="format">格式化</el-radio-button>
        <el-radio-button value="minify">压缩</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="doJs">执行</el-button>
      <el-button @click="copyOut(jsn.out)">复制</el-button>
    </div>
    <el-input v-model="jsn.out" type="textarea" :rows="8" readonly placeholder="结果…" />
  </template>

  <!-- 正则测试 -->
  <template v-if="tool === 'regex-test'">
    <div class="tb-h">正则表达式在线测试，高亮命中并显示分组。</div>
    <div class="tb-row">
      <span class="tb-lb">/</span>
      <el-input v-model="rg.pattern" placeholder="正则表达式" style="width:320px" @input="testRegex" />
      <span class="tb-lb">/</span>
      <el-checkbox-group v-model="rg.flags" @change="testRegex">
        <el-checkbox value="i">i 忽略大小写</el-checkbox>
        <el-checkbox value="m">m 多行</el-checkbox>
        <el-checkbox value="s">s 点匹配换行</el-checkbox>
      </el-checkbox-group>
    </div>
    <el-input v-model="rg.text" type="textarea" :rows="7" placeholder="测试文本…" @input="testRegex" />
    <div v-if="rg.err" class="tb-err">{{ rg.err }}</div>
    <div class="tb-lb">命中 {{ rg.matches.length }} 处</div>
    <div class="tb-regex-out" v-html="rgHtml" />
    <el-table v-if="rg.matches.length" :data="rg.matches.slice(0, 50)" size="small" max-height="260">
      <el-table-column label="#" width="60"><template #default="{ $index }">{{ $index + 1 }}</template></el-table-column>
      <el-table-column label="完整匹配" min-width="180"><template #default="{ row }">{{ row[0] }}</template></el-table-column>
      <el-table-column label="位置" width="90"><template #default="{ row }">{{ row.index }}</template></el-table-column>
      <el-table-column label="分组" min-width="160"><template #default="{ row }">{{ row.slice(1).map((g, i) => (i + 1) + ': ' + (g ?? '-')).join('　') }}</template></el-table-column>
    </el-table>
  </template>

  <!-- HTML/Markdown 互转 -->
  <template v-if="tool === 'md-html'">
    <div class="tb-h">Markdown ↔ HTML 基础语法互转（标题/列表/粗斜体/链接/图片/代码块/引用）。</div>
    <el-input v-model="mh.src" type="textarea" :rows="8" placeholder="输入内容…" />
    <div class="tb-row">
      <el-radio-group v-model="mh.mode">
        <el-radio-button value="md2h">MD → HTML</el-radio-button>
        <el-radio-button value="h2m">HTML → MD</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="convMd">转换</el-button>
      <el-button @click="copyOut(mh.out)">复制</el-button>
      <el-checkbox v-if="mh.mode === 'md2h'" v-model="mh.preview">预览</el-checkbox>
    </div>
    <el-input v-if="!mh.preview || mh.mode !== 'md2h'" v-model="mh.out" type="textarea" :rows="8" readonly placeholder="结果…" />
    <div v-else class="tb-md-preview" v-html="mh.out" />
  </template>
</template>
