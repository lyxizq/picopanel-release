<script setup>
import { reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { md5, md5FromBytes, shaHex, b64encode, b64decode, escapeStr, unescapeStr, copyOut } from './lib.js'
import { registerPageMessages } from '../../i18n'
import pageMsgs from '../../i18n/pages/toolbox.js'

defineProps({ tool: { type: String, required: true } })

const { t } = useI18n()
registerPageMessages(pageMsgs)

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
  } catch { ElMessage.error(t('toolbox.code.urlCode.errDecode')) }
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
  if (!/^[0-9a-z]+$/.test(clean)) { bc.out = ''; ElMessage.error(t('toolbox.code.base.errIllegal')); return }
  let dec = 0n
  const base = BigInt(bc.from)
  for (const ch of clean) {
    const d = parseInt(ch, 36)
    if (isNaN(d) || d >= bc.from) { bc.out = ''; ElMessage.error(t('toolbox.code.base.errDigit', { ch, base: bc.from })); return }
    dec = dec * base + BigInt(d)
  }
  if (v.startsWith('-')) dec = -dec
  bc.out = BASES.map(b => t('toolbox.code.base.line', { b, v: dec.toString(b) })).join('\n')
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
  if (!pool) { ElMessage.warning(t('toolbox.code.pwd.errNoSet')); return }
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
  } catch { ElMessage.error(t('toolbox.code.b64.errDecode')) }
}

// 图片转 Base64
const ib = reactive({ dataUrl: '', name: '', size: 0 })
function onImgPick(ev) {
  const f = ev.target.files?.[0]
  if (!f) return
  if (f.size > 8 * 1024 * 1024) { ElMessage.warning(t('toolbox.code.imgB64.warnSize')); return }
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
  } catch { ElMessage.error(t('toolbox.code.uni.errDecode')) }
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
      if (bad) throw new Error(t('toolbox.code.ascii.errNaN', { ch: bad }))
      ac.out = parts.map(p => String.fromCodePoint(parseInt(p, ac.hex ? 16 : 10))).join('')
    }
  } catch (e) { ElMessage.error(e.message || t('toolbox.code.errConv')) }
}

// Escape 加密/解密
const es = reactive({ src: '', mode: 'encode', out: '' })
function convEscape() {
  try {
    es.out = es.mode === 'encode' ? escapeStr(es.src) : unescapeStr(es.src)
  } catch { ElMessage.error(t('toolbox.code.errConv')) }
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
  if (isNaN(lo) || isNaN(hi) || hi < lo) { ElMessage.warning(t('toolbox.code.rand.errRange')); return }
  let n = Math.min(Math.max(1, +rn.count || 1), 10000)
  const list = []
  if (rn.unique) {
    const span = hi - lo + 1
    if (n > span) { ElMessage.warning(t('toolbox.code.rand.errSpan', { n: span })); return }
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
    <div class="tb-h">{{ $t('toolbox.code.urlCode.hint') }}</div>
    <el-input v-model="uc.src" type="textarea" :rows="6" :placeholder="$t('toolbox.code.phIn')" />
    <div class="tb-row">
      <el-radio-group v-model="uc.mode">
        <el-radio-button value="encode">{{ $t('toolbox.code.encode') }}</el-radio-button>
        <el-radio-button value="decode">{{ $t('toolbox.code.decode') }}</el-radio-button>
      </el-radio-group>
      <el-checkbox v-model="uc.full">{{ $t('toolbox.code.urlCode.full') }}</el-checkbox>
      <el-checkbox v-model="uc.plus">{{ $t('toolbox.code.urlCode.plus') }}</el-checkbox>
      <el-button type="primary" @click="convUrl">{{ $t('toolbox.code.convert') }}</el-button>
      <el-button @click="copyOut(uc.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="uc.out" type="textarea" :rows="6" readonly :placeholder="$t('toolbox.code.phOut')" />
  </template>

  <!-- MD5 -->
  <template v-if="tool === 'md5'">
    <div class="tb-h">{{ $t('toolbox.code.md5.hint') }}</div>
    <el-input v-model="m5.src" type="textarea" :rows="5" :placeholder="$t('toolbox.code.phText')" @input="genMd5" />
    <div class="tb-row">
      <el-radio-group v-model="m5.mode" @change="genMd5">
        <el-radio-button value="32l">{{ $t('toolbox.code.md5.l32') }}</el-radio-button>
        <el-radio-button value="32u">{{ $t('toolbox.code.md5.u32') }}</el-radio-button>
        <el-radio-button value="16l">{{ $t('toolbox.code.md5.l16') }}</el-radio-button>
        <el-radio-button value="16u">{{ $t('toolbox.code.md5.u16') }}</el-radio-button>
      </el-radio-group>
      <el-button @click="copyOut(m5.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="m5.out" readonly :placeholder="$t('toolbox.code.phAuto')" />
  </template>

  <!-- 进制转换 -->
  <template v-if="tool === 'base-convert'">
    <div class="tb-h">{{ $t('toolbox.code.base.hint') }}</div>
    <div class="tb-row">
      <el-input v-model="bc.val" :placeholder="$t('toolbox.code.base.phVal')" style="width:280px" @input="convBase" />
      <el-select v-model="bc.from" style="width:110px" @change="convBase">
        <el-option v-for="b in BASES" :key="b" :label="$t('toolbox.code.base.baseOpt', { b })" :value="b" />
      </el-select>
      <el-button @click="copyOut(bc.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="bc.out" type="textarea" :rows="6" readonly :placeholder="$t('toolbox.code.base.phOut')" />
  </template>

  <!-- UUID 生成 -->
  <template v-if="tool === 'uuid'">
    <div class="tb-h">{{ $t('toolbox.code.uuid.hint') }}</div>
    <div class="tb-row">
      <el-input-number v-model="uu.count" :min="1" :max="500" />
      <el-button type="primary" @click="genUuid">{{ $t('toolbox.code.gen') }}</el-button>
      <el-button @click="copyOut(uu.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="uu.out" type="textarea" :rows="10" readonly :placeholder="$t('toolbox.code.phGenOut')" />
  </template>

  <!-- 密码生成器 -->
  <template v-if="tool === 'pwd-gen'">
    <div class="tb-h">{{ $t('toolbox.code.pwd.hint') }}</div>
    <div class="tb-row">
      <span class="tb-lb">{{ $t('toolbox.code.pwd.lenN', { n: pw.len }) }}</span>
      <el-slider v-model="pw.len" :min="4" :max="64" style="width:220px" />
    </div>
    <div class="tb-row">
      <el-checkbox v-model="pw.upper">{{ $t('toolbox.code.pwd.upper') }}</el-checkbox>
      <el-checkbox v-model="pw.lower">{{ $t('toolbox.code.pwd.lower') }}</el-checkbox>
      <el-checkbox v-model="pw.digit">{{ $t('toolbox.code.pwd.digit') }}</el-checkbox>
      <el-checkbox v-model="pw.symbol">{{ $t('toolbox.code.pwd.symbol') }}</el-checkbox>
      <el-checkbox v-model="pw.noAmbiguous">{{ $t('toolbox.code.pwd.noAmbig') }}</el-checkbox>
      <el-input-number v-model="pw.count" :min="1" :max="50" />
      <el-button type="primary" @click="genPwd">{{ $t('toolbox.code.gen') }}</el-button>
      <el-button @click="copyOut(pw.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="pw.out" type="textarea" :rows="8" readonly :placeholder="$t('toolbox.code.phGenOut')" />
  </template>

  <!-- Base64 -->
  <template v-if="tool === 'base64'">
    <div class="tb-h">{{ $t('toolbox.code.b64.hint') }}</div>
    <el-input v-model="b6.src" type="textarea" :rows="6" :placeholder="$t('toolbox.code.phIn')" />
    <div class="tb-row">
      <el-radio-group v-model="b6.mode">
        <el-radio-button value="encode">{{ $t('toolbox.code.encode') }}</el-radio-button>
        <el-radio-button value="decode">{{ $t('toolbox.code.decode') }}</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="convB64">{{ $t('toolbox.code.convert') }}</el-button>
      <el-button @click="copyOut(b6.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="b6.out" type="textarea" :rows="6" readonly :placeholder="$t('toolbox.code.phOut')" />
  </template>

  <!-- 图片转 Base64 -->
  <template v-if="tool === 'img-base64'">
    <div class="tb-h">{{ $t('toolbox.code.imgB64.hint') }}</div>
    <div class="tb-row">
      <input type="file" accept="image/*" @change="onImgPick" />
      <span v-if="ib.name" class="tb-lb">{{ ib.name }}（{{ (ib.size / 1024).toFixed(1) }} KB → {{ (ib.dataUrl.length / 1024).toFixed(1) }} KB）</span>
    </div>
    <template v-if="ib.dataUrl">
      <img :src="ib.dataUrl" style="max-width:240px;max-height:160px;border:1px solid var(--el-border-color);border-radius:4px;margin:6px 0" />
      <el-input :model-value="ib.dataUrl" type="textarea" :rows="4" readonly />
      <div class="tb-row">
        <el-button @click="copyOut(ib.dataUrl)">{{ $t('toolbox.code.imgB64.copyData') }}</el-button>
        <el-button @click="copyOut(`<img src=&quot;${ib.dataUrl}&quot; />`)">{{ $t('toolbox.code.imgB64.copyImg') }}</el-button>
        <el-button @click="copyOut(`background-image: url(${ib.dataUrl});`)">{{ $t('toolbox.code.imgB64.copyCss') }}</el-button>
      </div>
    </template>
  </template>

  <!-- Unicode 转换 -->
  <template v-if="tool === 'unicode'">
    <div class="tb-h">{{ $t('toolbox.code.uni.hint') }}</div>
    <el-input v-model="un.src" type="textarea" :rows="5" :placeholder="$t('toolbox.code.phIn')" />
    <div class="tb-row">
      <el-radio-group v-model="un.mode">
        <el-radio-button value="encode">{{ $t('toolbox.code.uni.toU') }}</el-radio-button>
        <el-radio-button value="decode">{{ $t('toolbox.code.uni.restore') }}</el-radio-button>
      </el-radio-group>
      <el-checkbox v-model="un.skipAscii">{{ $t('toolbox.code.uni.skipAscii') }}</el-checkbox>
      <el-button type="primary" @click="convUnicode">{{ $t('toolbox.code.convert') }}</el-button>
      <el-button @click="copyOut(un.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="un.out" type="textarea" :rows="5" readonly :placeholder="$t('toolbox.code.phOut')" />
  </template>

  <!-- ASCII 转换 -->
  <template v-if="tool === 'ascii-code'">
    <div class="tb-h">{{ $t('toolbox.code.ascii.hint') }}</div>
    <el-input v-model="ac.src" type="textarea" :rows="5" :placeholder="$t('toolbox.code.phIn')" />
    <div class="tb-row">
      <el-radio-group v-model="ac.mode">
        <el-radio-button value="encode">{{ $t('toolbox.code.ascii.toCode') }}</el-radio-button>
        <el-radio-button value="decode">{{ $t('toolbox.code.ascii.toText') }}</el-radio-button>
      </el-radio-group>
      <el-checkbox v-model="ac.hex">{{ $t('toolbox.code.ascii.hex') }}</el-checkbox>
      <el-button type="primary" @click="convAscii">{{ $t('toolbox.code.convert') }}</el-button>
      <el-button @click="copyOut(ac.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="ac.out" type="textarea" :rows="5" readonly :placeholder="$t('toolbox.code.phOut')" />
  </template>

  <!-- Escape -->
  <template v-if="tool === 'escape'">
    <div class="tb-h">{{ $t('toolbox.code.esc.hint') }}</div>
    <el-input v-model="es.src" type="textarea" :rows="5" :placeholder="$t('toolbox.code.phIn')" />
    <div class="tb-row">
      <el-radio-group v-model="es.mode">
        <el-radio-button value="encode">{{ $t('toolbox.code.esc.enc') }}</el-radio-button>
        <el-radio-button value="decode">{{ $t('toolbox.code.esc.dec') }}</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="convEscape">{{ $t('toolbox.code.convert') }}</el-button>
      <el-button @click="copyOut(es.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="es.out" type="textarea" :rows="5" readonly :placeholder="$t('toolbox.code.phOut')" />
  </template>

  <!-- SHA -->
  <template v-if="tool === 'sha'">
    <div class="tb-h">{{ $t('toolbox.code.sha.hint') }}</div>
    <el-input v-model="sh.src" type="textarea" :rows="5" :placeholder="$t('toolbox.code.phText')" @input="genSha" />
    <div class="tb-row">
      <el-select v-model="sh.algo" style="width:140px" @change="genSha">
        <el-option v-for="a in ['SHA-1', 'SHA-256', 'SHA-384', 'SHA-512']" :key="a" :label="a" :value="a" />
      </el-select>
      <el-button @click="copyOut(sh.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="sh.out" readonly :placeholder="$t('toolbox.code.phAuto')" />
  </template>

  <!-- 文件 Hash -->
  <template v-if="tool === 'file-hash'">
    <div class="tb-h">{{ $t('toolbox.code.fileHash.hint') }}</div>
    <div class="tb-row">
      <input type="file" @change="onHashPick" />
      <span v-if="fh.busy" class="tb-lb">{{ $t('toolbox.code.fileHash.busy') }}</span>
      <span v-else-if="fh.name" class="tb-lb">{{ fh.name }}（{{ (fh.size / 1024).toFixed(1) }} KB）</span>
    </div>
    <template v-if="fh.md5">
      <div class="tb-kv"><b>MD5</b><span>{{ fh.md5 }}</span><el-button size="small" @click="copyOut(fh.md5)">{{ $t('toolbox.code.copy') }}</el-button></div>
      <div class="tb-kv"><b>SHA-1</b><span>{{ fh.sha1 }}</span><el-button size="small" @click="copyOut(fh.sha1)">{{ $t('toolbox.code.copy') }}</el-button></div>
      <div class="tb-kv"><b>SHA-256</b><span>{{ fh.sha256 }}</span><el-button size="small" @click="copyOut(fh.sha256)">{{ $t('toolbox.code.copy') }}</el-button></div>
    </template>
  </template>

  <!-- 随机数 -->
  <template v-if="tool === 'rand-num'">
    <div class="tb-h">{{ $t('toolbox.code.rand.hint') }}</div>
    <div class="tb-row">
      <el-input-number v-model="rn.min" :controls="false" :placeholder="$t('toolbox.code.rand.phMin')" style="width:110px" />
      <span class="tb-lb">~</span>
      <el-input-number v-model="rn.max" :controls="false" :placeholder="$t('toolbox.code.rand.phMax')" style="width:110px" />
      <el-input-number v-model="rn.count" :min="1" :max="10000" :placeholder="$t('toolbox.code.rand.phCount')" />
      <el-checkbox v-model="rn.unique">{{ $t('toolbox.code.rand.unique') }}</el-checkbox>
      <el-checkbox v-model="rn.sort">{{ $t('toolbox.code.rand.sort') }}</el-checkbox>
      <el-button type="primary" @click="genRand">{{ $t('toolbox.code.gen') }}</el-button>
      <el-button @click="copyOut(rn.out)">{{ $t('toolbox.code.copy') }}</el-button>
    </div>
    <el-input v-model="rn.out" type="textarea" :rows="10" readonly :placeholder="$t('toolbox.code.phGenOut')" />
  </template>
</template>
