<script setup>
import { reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { idnEncode, idnDecode, copyOut } from './lib.js'
import { registerPageMessages } from '../../i18n'
import pageMsgs from '../../i18n/pages/toolbox.js'

defineProps({ tool: { type: String, required: true } })

const { t } = useI18n()
registerPageMessages(pageMsgs)

// 链接提取
const el = reactive({ src: '', dedupe: true, sort: false, out: '' })
function extractLinks() {
  let list = el.src.match(/https?:\/\/[^\s"'<>（）()【】，。；]+/g) || []
  if (el.dedupe) list = [...new Set(list)]
  if (el.sort) list.sort()
  el.out = list.join('\n')
}

// 中文域名转码
const pc = reactive({ src: '', mode: 'encode', out: '' })
function convPuny() {
  try {
    const lines = pc.src.split(/\s+/).filter(Boolean)
    pc.out = lines.map(l => pc.mode === 'encode' ? idnEncode(l) : idnDecode(l)).join('\n')
  } catch (e) { ElMessage.error(t('toolbox.url.convFail', { msg: e.message })) }
}

// Robots 生成器
const rb = reactive({ ua: '*', delay: '', allow: '/\n', disallow: '/admin/', sitemap: '', out: '' })
function genRobots() {
  const lines = []
  for (const ua of rb.ua.split(/[\s,;，；\n]+/).filter(Boolean)) lines.push(`User-agent: ${ua}`)
  if (rb.delay) lines.push(`Crawl-delay: ${rb.delay}`)
  for (const l of rb.allow.split('\n').filter(x => x.trim())) lines.push(`Allow: ${l.trim()}`)
  for (const l of rb.disallow.split('\n').filter(x => x.trim())) lines.push(`Disallow: ${l.trim()}`)
  if (rb.sitemap.trim()) lines.push(`Sitemap: ${rb.sitemap.trim()}`)
  rb.out = lines.join('\n')
}

// Sitemap 生成器
const sm = reactive({ urls: '', freq: 'daily', priority: '0.8', lastmod: today(), out: '' })
function today() { return new Date().toISOString().slice(0, 10) }
function genSitemap() {
  const urls = sm.urls.split(/\s+/).filter(Boolean).map(u => /^https?:\/\//.test(u) ? u : 'https://' + u)
  if (!urls.length) { ElMessage.warning(t('toolbox.url.sitemap.needUrl')); return }
  const items = urls.map(u => {
    let it = `  <url>\n    <loc>${u}</loc>\n`
    if (sm.lastmod) it += `    <lastmod>${sm.lastmod}</lastmod>\n`
    if (sm.freq) it += `    <changefreq>${sm.freq}</changefreq>\n`
    if (sm.priority) it += `    <priority>${sm.priority}</priority>\n`
    return it + `  </url>`
  }).join('\n')
  sm.out = `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${items}\n</urlset>`
}

</script>

<template>
  <!-- 链接提取 -->
  <template v-if="tool === 'extract-links'">
    <div class="tb-h">{{ $t('toolbox.url.extract.hint') }}</div>
    <el-input v-model="el.src" type="textarea" :rows="7" :placeholder="$t('toolbox.url.extract.phIn')" />
    <div class="tb-row">
      <el-checkbox v-model="el.dedupe">{{ $t('toolbox.url.extract.dedupe') }}</el-checkbox>
      <el-checkbox v-model="el.sort">{{ $t('toolbox.url.extract.sort') }}</el-checkbox>
      <el-button type="primary" @click="extractLinks">{{ $t('toolbox.url.extract.run') }}</el-button>
      <el-button @click="copyOut(el.out)">{{ $t('toolbox.url.copyResult') }}</el-button>
    </div>
    <el-input v-model="el.out" type="textarea" :rows="7" readonly :placeholder="$t('toolbox.url.extract.phOut')" />
  </template>

  <!-- 中文域名转码 -->
  <template v-if="tool === 'punycode'">
    <div class="tb-h">{{ $t('toolbox.url.puny.hint') }}</div>
    <el-input v-model="pc.src" type="textarea" :rows="6" :placeholder="$t('toolbox.url.puny.phIn')" />
    <div class="tb-row">
      <el-radio-group v-model="pc.mode">
        <el-radio-button value="encode">{{ $t('toolbox.url.puny.toPuny') }}</el-radio-button>
        <el-radio-button value="decode">{{ $t('toolbox.url.puny.toChinese') }}</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="convPuny">{{ $t('toolbox.url.convert') }}</el-button>
      <el-button @click="copyOut(pc.out)">{{ $t('toolbox.url.copyResult') }}</el-button>
    </div>
    <el-input v-model="pc.out" type="textarea" :rows="6" readonly :placeholder="$t('toolbox.url.puny.phOut')" />
  </template>

  <!-- Robots 生成器 -->
  <template v-if="tool === 'robots-gen'">
    <div class="tb-h">{{ $t('toolbox.url.robots.hint') }}</div>
    <div class="tb-row">
      <el-input v-model="rb.ua" :placeholder="$t('toolbox.url.robots.phUa')" style="width:240px" />
      <el-input v-model="rb.delay" :placeholder="$t('toolbox.url.robots.phDelay')" style="width:150px" />
    </div>
    <div class="tb-row">
      <el-input v-model="rb.allow" type="textarea" :rows="3" :placeholder="$t('toolbox.url.robots.phAllow')" style="width:320px" />
      <el-input v-model="rb.disallow" type="textarea" :rows="3" :placeholder="$t('toolbox.url.robots.phDisallow')" style="width:320px" />
    </div>
    <div class="tb-row">
      <el-input v-model="rb.sitemap" :placeholder="$t('toolbox.url.robots.phSitemap')" style="width:360px" />
      <el-button type="primary" @click="genRobots">{{ $t('toolbox.url.gen') }}</el-button>
      <el-button @click="copyOut(rb.out)">{{ $t('toolbox.url.copy') }}</el-button>
    </div>
    <el-input v-model="rb.out" type="textarea" :rows="8" readonly :placeholder="$t('toolbox.url.robots.phOut')" />
  </template>

  <!-- Sitemap 生成器 -->
  <template v-if="tool === 'sitemap-gen'">
    <div class="tb-h">{{ $t('toolbox.url.sitemap.hint') }}</div>
    <el-input v-model="sm.urls" type="textarea" :rows="6" :placeholder="$t('toolbox.url.sitemap.phUrls')" />
    <div class="tb-row">
      <el-select v-model="sm.freq" style="width:130px">
        <el-option v-for="f in ['always','hourly','daily','weekly','monthly','yearly','never']" :key="f" :label="f" :value="f" />
      </el-select>
      <el-input v-model="sm.priority" placeholder="priority" style="width:110px" />
      <el-input v-model="sm.lastmod" placeholder="lastmod" style="width:150px" />
      <el-button type="primary" @click="genSitemap">{{ $t('toolbox.url.gen') }}</el-button>
      <el-button @click="copyOut(sm.out)">{{ $t('toolbox.url.copy') }}</el-button>
    </div>
    <el-input v-model="sm.out" type="textarea" :rows="8" readonly :placeholder="$t('toolbox.url.sitemap.phOut')" />
  </template>

  </template>
