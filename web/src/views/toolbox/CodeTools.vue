<script setup>
import { reactive } from 'vue'
import { ElMessage } from 'element-plus'
import { md5, md5FromBytes, shaHex, b64encode, b64decode, escapeStr, unescapeStr, copyOut } from './lib.js'

defineProps({ tool: { type: String, required: true } })

// URL 编码/解码
const uc = reactive({ src: '', mode: 'encode', full: false, plus: false, out: '' })
function convUrl() {
  try {
    uc.out = uc.mode === 'encode'
      ? (uc.full ? encodeURI(uc.src) : uc.plus
        ? encodeURIComponent(uc.src).replace(/%20/g, '+')
        : encodeURIComponent(uc.src))
      : (uc.full ? decodeURI(uc.src) : uc.plus
        ? decodeURIComponent(uc.src.replace(/\+/g, '%20'))
        : decodeURIComponent(uc.src))
  } catch { ElMessage.error('解码失败：含有无效的百分号编码') }
}

// MD5
const m5 = reactive({ src: '', mode: '32l', out: '' })
function genMd5() {
  const h = md5(m5.src)
  m5.out = m5.mode === '32u' ? h.toUpperCase() : m5.mode === '16l' ? h.slice(8, 24) : m5.mode === '16u' ? h.slice(8, 24).toUpperCase() : h
}

// 进制转换
const bc = reactive({ val: '', from: 10, out: '' })
const BASES = [2, 8, 10, 16, 36]
function convBase() {
  const v = bc.val.trim().replace(/\s/g, '')
  if (!v) { bc.out = ''; return }
  const clean = v.replace(/^[+-]/, '').toLowerCase()
  if (!/^[0-9a-z]+$/.test(clean)) { bc.out = ''; ElMessage.error('含有非法字符'); return }
  let dec = 0n
  const base = BigInt(bc.from)
  for (const ch of clean) {
    const d = parseInt(ch, 36)
    if (isNaN(d) || d >= bc.from) { bc.out = ''; ElMessage.error(`字符 "${ch}" 不是 ${bc.from} 进制位`); return }
    dec = dec * base + BigInt(d)
  }
  if (v.startsWith('-')) dec = -dec
  bc.out = BASES.map(b => `${b} 进制：${dec.toString(b)}`).join('\n')
}

// UUID 生成
const uu = reactive({ count: 5, out: '' })
function genUuid() {
  const n = Math.min(Math.max(1, +uu.count || 1), 500)
  const list = []
  for (let i = 0; i < n; i++) list.push(crypto.randomUUID())
  uu.out = list.join('\n')
}

// 密码生成器
const pw = reactive({ len: 16, upper: true, lower: true, digit: true, symbol: true, noAmbiguous: true, count: 5, out: '' })
const PW_SETS = { upper: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', lower: 'abcdefghijklmnopqrstuvwxyz', digit: '0123456789', symbol: '!@#$%^&*()-_=+[]{};:,.<>?' }
function genPwd() {
  let pool = ''
  const required = []
  for (const k of ['upper', 'lower', 'digit', 'symbol']) {
    if (!pw[k]) continue
    let set = PW_SETS[k]
    if (pw.noAmbiguous) set = set.replace(/[O0oIl1|]/g, '')
    if (!set) continue
    pool += set
    required.push(set)
  }
  if (!pool) { ElMessage.warning('请至少选择一种字符集'); return }
  const n = Math.min(Math.max(4, pw.len || 8), 128)
  const cnt = Math.min(Math.max(1, pw.count || 1), 50)
  const list = []
  for (let i = 0; i < cnt; i++) {
    let pwd
    do {
      pwd = [...crypto.getRandomValues(new Uint32Array(n))]
        .map(r => pool[r % pool.length]).join('')
    } while (!required.every(set => [...pwd].some(c => set.includes(c))))
    list.push(pwd)
  }
  pw.out = list.join('\n')
}

// Base64
const b6 = reactive({ src: '', mode: 'encode', out: '' })
function convB64() {
  try {
    b6.out = b6.mode === 'encode' ? b64encode(b6.src) : b64decode(b6.src)
  } catch { ElMessage.error('解码失败：不是有效的 Base64') }
}

// 图片转 Base64
const ib = reactive({ dataUrl: '', name: '', size: 0 })
function onImgPick(ev) {
  const f = ev.target.files?.[0]
  if (!f) return
  if (f.size > 8 * 1024 * 1024) { ElMessage.warning('图片请小于 8MB'); return }
  ib.name = f.name
  ib.size = f.size
  const r = new FileReader()
  r.onload = () => { ib.dataUrl = String(r.result || '') }
  r.readAsDataURL(f)
}

// Unicode 转换
const un = reactive({ src: '', mode: 'encode', skipAscii: true, out: '' })
function convUnicode() {
  try {
    if (un.mode === 'encode') {
      un.out = [...un.src].map(ch => {
        const c = ch.codePointAt(0)
        return un.skipAscii && c < 128 ? ch : (c > 0xffff
          ? '\\u{' + c.toString(16) + '}'
          : '\\u' + c.toString(16).padStart(4, '0'))
      }).join('')
    } else {
      un.out = un.src.replace(/\\u\{([0-9a-fA-F]+)\}|\\u([0-9a-fA-F]{4})/g, (_, b, s) =>
        String.fromCodePoint(parseInt(b || s, 16)))
    }
  } catch { ElMessage.error('解码失败') }
}

// ASCII 转换
const ac = reactive({ src: '', mode: 'encode', hex: false, out: '' })
function convAscii() {
  try {
    if (ac.mode === 'encode') {
      ac.out = [...ac.src].map(ch => ac.hex ? ch.codePointAt(0).toString(16) : ch.codePointAt(0)).join(' ')
    } else {
      const parts = ac.src.trim().split(/[\s,;，]+/).filter(Boolean)
      const bad = parts.find(p => !/^[0-9a-fA-F]+$/.test(p))
      if (bad) throw new Error(`"${bad}" 不是有效数字`)
      ac.out = parts.map(p => String.fromCodePoint(parseInt(p, ac.hex ? 16 : 10))).join('')
    }
  } catch (e) { ElMessage.error(e.message || '转换失败') }
}

// Escape 加密/解密
const es = reactive({ src: '', mode: 'encode', out: '' })
function convEscape() {
  try {
    es.out = es.mode === 'encode' ? escapeStr(es.src) : unescapeStr(es.src)
  } catch { ElMessage.error('转换失败') }
}

// SHA 加密
const sh = reactive({ src: '', algo: 'SHA-256', out: '' })
async function genSha() {
  if (!sh.src) { sh.out = ''; return }
  sh.out = await shaHex(sh.algo, sh.src)
}

// 文件 Hash 校验
const fh = reactive({ name: '', size: 0, md5: '', sha1: '', sha256: '', busy: false })
async function onHashPick(ev) {
  const f = ev.target.files?.[0]
  if (!f) return
  fh.name = f.name
  fh.size = f.size
  fh.md5 = fh.sha1 = fh.sha256 = ''
  fh.busy = true
  try {
    const buf = new Uint8Array(await f.arrayBuffer())
    fh.md5 = md5FromBytes(buf)
    fh.sha1 = await shaHex('SHA-1', buf)
    fh.sha256 = await shaHex('SHA-256', buf)
  } finally { fh.busy = false }
}

// 随机数生成
const rn = reactive({ min: 1, max: 100, count: 10, unique: false, sort: false, out: '' })
function genRand() {
  const lo = Math.ceil(+rn.min), hi = Math.floor(+rn.max)
  if (isNaN(lo) || isNaN(hi) || hi < lo) { ElMessage.warning('数值范围无效'); return }
  let n = Math.min(Math.max(1, +rn.count || 1), 10000)
  const list = []
  if (rn.unique) {
    const span = hi - lo + 1
    if (n > span) { ElMessage.warning(`范围内只有 ${span} 个不重复数`); return }
    const pool = new Set()
    while (pool.size < n) pool.add(lo + Math.floor(Math.random() * span))
    list.push(...pool)
  } else {
    for (let i = 0; i < n; i++) list.push(lo + Math.floor(Math.random() * (hi - lo + 1)))
  }
  if (rn.sort) list.sort((a, b) => a - b)
  rn.out = list.join('\n')
}
</script>

<template>
  <!-- URL 编码/解码 -->
  <template v-if="tool === 'url-code'">
    <div class="tb-h">URL 百分号编码/解码。</div>
    <el-input v-model="uc.src" type="textarea" :rows="6" placeholder="输入内容…" />
    <div class="tb-row">
      <el-radio-group v-model="uc.mode">
        <el-radio-button value="encode">编码</el-radio-button>
        <el-radio-button value="decode">解码</el-radio-button>
      </el-radio-group>
      <el-checkbox v-model="uc.full">整 URL 模式（保留 ://?&=）</el-checkbox>
      <el-checkbox v-model="uc.plus">空格转 +</el-checkbox>
      <el-button type="primary" @click="convUrl">转换</el-button>
      <el-button @click="copyOut(uc.out)">复制</el-button>
    </div>
    <el-input v-model="uc.out" type="textarea" :rows="6" readonly placeholder="结果…" />
  </template>

  <!-- MD5 -->
  <template v-if="tool === 'md5'">
    <div class="tb-h">MD5 哈希（32/16 位，UTF-8 编码计算）。</div>
    <el-input v-model="m5.src" type="textarea" :rows="5" placeholder="输入文本…" @input="genMd5" />
    <div class="tb-row">
      <el-radio-group v-model="m5.mode" @change="genMd5">
        <el-radio-button value="32l">32位小写</el-radio-button>
        <el-radio-button value="32u">32位大写</el-radio-button>
        <el-radio-button value="16l">16位小写</el-radio-button>
        <el-radio-button value="16u">16位大写</el-radio-button>
      </el-radio-group>
      <el-button @click="copyOut(m5.out)">复制</el-button>
    </div>
    <el-input v-model="m5.out" readonly placeholder="输入后自动计算…" />
  </template>

  <!-- 进制转换 -->
  <template v-if="tool === 'base-convert'">
    <div class="tb-h">2/8/10/16/36 进制互转，支持超大整数（BigInt）。</div>
    <div class="tb-row">
      <el-input v-model="bc.val" placeholder="输入数值" style="width:280px" @input="convBase" />
      <el-select v-model="bc.from" style="width:110px" @change="convBase">
        <el-option v-for="b in BASES" :key="b" :label="b + ' 进制'" :value="b" />
      </el-select>
      <el-button @click="copyOut(bc.out)">复制</el-button>
    </div>
    <el-input v-model="bc.out" type="textarea" :rows="6" readonly placeholder="结果（同时列出各进制）…" />
  </template>

  <!-- UUID 生成 -->
  <template v-if="tool === 'uuid'">
    <div class="tb-h">UUID v4 批量生成（crypto.randomUUID）。</div>
    <div class="tb-row">
      <el-input-number v-model="uu.count" :min="1" :max="500" />
      <el-button type="primary" @click="genUuid">生成</el-button>
      <el-button @click="copyOut(uu.out)">复制</el-button>
    </div>
    <el-input v-model="uu.out" type="textarea" :rows="10" readonly placeholder="生成结果…" />
  </template>

  <!-- 密码生成器 -->
  <template v-if="tool === 'pwd-gen'">
    <div class="tb-h">随机强密码生成（crypto 安全随机源，保证每类字符至少 1 个）。</div>
    <div class="tb-row">
      <span class="tb-lb">长度 {{ pw.len }}</span>
      <el-slider v-model="pw.len" :min="4" :max="64" style="width:220px" />
    </div>
    <div class="tb-row">
      <el-checkbox v-model="pw.upper">大写</el-checkbox>
      <el-checkbox v-model="pw.lower">小写</el-checkbox>
      <el-checkbox v-model="pw.digit">数字</el-checkbox>
      <el-checkbox v-model="pw.symbol">符号</el-checkbox>
      <el-checkbox v-model="pw.noAmbiguous">排除易混淆 (0OoIl1)</el-checkbox>
      <el-input-number v-model="pw.count" :min="1" :max="50" />
      <el-button type="primary" @click="genPwd">生成</el-button>
      <el-button @click="copyOut(pw.out)">复制</el-button>
    </div>
    <el-input v-model="pw.out" type="textarea" :rows="8" readonly placeholder="生成结果…" />
  </template>

  <!-- Base64 -->
  <template v-if="tool === 'base64'">
    <div class="tb-h">Base64 编码/解码（中文 UTF-8 安全）。</div>
    <el-input v-model="b6.src" type="textarea" :rows="6" placeholder="输入内容…" />
    <div class="tb-row">
      <el-radio-group v-model="b6.mode">
        <el-radio-button value="encode">编码</el-radio-button>
        <el-radio-button value="decode">解码</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="convB64">转换</el-button>
      <el-button @click="copyOut(b6.out)">复制</el-button>
    </div>
    <el-input v-model="b6.out" type="textarea" :rows="6" readonly placeholder="结果…" />
  </template>

  <!-- 图片转 Base64 -->
  <template v-if="tool === 'img-base64'">
    <div class="tb-h">本地图片转 DataURL，可复制 HTML/CSS 引用代码（不上传服务器）。</div>
    <div class="tb-row">
      <input type="file" accept="image/*" @change="onImgPick" />
      <span v-if="ib.name" class="tb-lb">{{ ib.name }}（{{ (ib.size / 1024).toFixed(1) }} KB → {{ (ib.dataUrl.length / 1024).toFixed(1) }} KB）</span>
    </div>
    <template v-if="ib.dataUrl">
      <img :src="ib.dataUrl" style="max-width:240px;max-height:160px;border:1px solid var(--el-border-color);border-radius:4px;margin:6px 0" />
      <el-input :model-value="ib.dataUrl" type="textarea" :rows="4" readonly />
      <div class="tb-row">
        <el-button @click="copyOut(ib.dataUrl)">复制 DataURL</el-button>
        <el-button @click="copyOut(`<img src=&quot;${ib.dataUrl}&quot; />`)">复制 img 标签</el-button>
        <el-button @click="copyOut(`background-image: url(${ib.dataUrl});`)">复制 CSS</el-button>
      </div>
    </template>
  </template>

  <!-- Unicode 转换 -->
  <template v-if="tool === 'unicode'">
    <div class="tb-h">中文/emoji ↔ \\uXXXX Unicode 转义互转。</div>
    <el-input v-model="un.src" type="textarea" :rows="5" placeholder="输入内容…" />
    <div class="tb-row">
      <el-radio-group v-model="un.mode">
        <el-radio-button value="encode">转 \\uXXXX</el-radio-button>
        <el-radio-button value="decode">还原</el-radio-button>
      </el-radio-group>
      <el-checkbox v-model="un.skipAscii">跳过 ASCII</el-checkbox>
      <el-button type="primary" @click="convUnicode">转换</el-button>
      <el-button @click="copyOut(un.out)">复制</el-button>
    </div>
    <el-input v-model="un.out" type="textarea" :rows="5" readonly placeholder="结果…" />
  </template>

  <!-- ASCII 转换 -->
  <template v-if="tool === 'ascii-code'">
    <div class="tb-h">文本 ↔ ASCII 码值（十进制/十六进制，空格分隔）。</div>
    <el-input v-model="ac.src" type="textarea" :rows="5" placeholder="输入内容…" />
    <div class="tb-row">
      <el-radio-group v-model="ac.mode">
        <el-radio-button value="encode">文本 → 码值</el-radio-button>
        <el-radio-button value="decode">码值 → 文本</el-radio-button>
      </el-radio-group>
      <el-checkbox v-model="ac.hex">十六进制</el-checkbox>
      <el-button type="primary" @click="convAscii">转换</el-button>
      <el-button @click="copyOut(ac.out)">复制</el-button>
    </div>
    <el-input v-model="ac.out" type="textarea" :rows="5" readonly placeholder="结果…" />
  </template>

  <!-- Escape -->
  <template v-if="tool === 'escape'">
    <div class="tb-h">JS Legacy escape/unescape（%XX 与 %uXXXX）。</div>
    <el-input v-model="es.src" type="textarea" :rows="5" placeholder="输入内容…" />
    <div class="tb-row">
      <el-radio-group v-model="es.mode">
        <el-radio-button value="encode">加密</el-radio-button>
        <el-radio-button value="decode">解密</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="convEscape">转换</el-button>
      <el-button @click="copyOut(es.out)">复制</el-button>
    </div>
    <el-input v-model="es.out" type="textarea" :rows="5" readonly placeholder="结果…" />
  </template>

  <!-- SHA -->
  <template v-if="tool === 'sha'">
    <div class="tb-h">SHA-1/256/384/512 哈希（Web Crypto）。</div>
    <el-input v-model="sh.src" type="textarea" :rows="5" placeholder="输入文本…" @input="genSha" />
    <div class="tb-row">
      <el-select v-model="sh.algo" style="width:140px" @change="genSha">
        <el-option v-for="a in ['SHA-1', 'SHA-256', 'SHA-384', 'SHA-512']" :key="a" :label="a" :value="a" />
      </el-select>
      <el-button @click="copyOut(sh.out)">复制</el-button>
    </div>
    <el-input v-model="sh.out" readonly placeholder="输入后自动计算…" />
  </template>

  <!-- 文件 Hash -->
  <template v-if="tool === 'file-hash'">
    <div class="tb-h">计算文件 MD5 / SHA-1 / SHA-256 校验值（本地计算，不上传）。</div>
    <div class="tb-row">
      <input type="file" @change="onHashPick" />
      <span v-if="fh.busy" class="tb-lb">计算中…</span>
      <span v-else-if="fh.name" class="tb-lb">{{ fh.name }}（{{ (fh.size / 1024).toFixed(1) }} KB）</span>
    </div>
    <template v-if="fh.md5">
      <div class="tb-kv"><b>MD5</b><span>{{ fh.md5 }}</span><el-button size="small" @click="copyOut(fh.md5)">复制</el-button></div>
      <div class="tb-kv"><b>SHA-1</b><span>{{ fh.sha1 }}</span><el-button size="small" @click="copyOut(fh.sha1)">复制</el-button></div>
      <div class="tb-kv"><b>SHA-256</b><span>{{ fh.sha256 }}</span><el-button size="small" @click="copyOut(fh.sha256)">复制</el-button></div>
    </template>
  </template>

  <!-- 随机数 -->
  <template v-if="tool === 'rand-num'">
    <div class="tb-h">范围内随机整数批量生成。</div>
    <div class="tb-row">
      <el-input-number v-model="rn.min" :controls="false" placeholder="最小" style="width:110px" />
      <span class="tb-lb">~</span>
      <el-input-number v-model="rn.max" :controls="false" placeholder="最大" style="width:110px" />
      <el-input-number v-model="rn.count" :min="1" :max="10000" placeholder="个数" />
      <el-checkbox v-model="rn.unique">不重复</el-checkbox>
      <el-checkbox v-model="rn.sort">排序</el-checkbox>
      <el-button type="primary" @click="genRand">生成</el-button>
      <el-button @click="copyOut(rn.out)">复制</el-button>
    </div>
    <el-input v-model="rn.out" type="textarea" :rows="10" readonly placeholder="生成结果…" />
  </template>
</template>
