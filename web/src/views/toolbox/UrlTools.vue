<script setup>
import { reactive } from 'vue'
import { ElMessage } from 'element-plus'
import { idnEncode, idnDecode, copyOut } from './lib.js'

defineProps({ tool: { type: String, required: true } })

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
  } catch (e) { ElMessage.error('转换失败：' + e.message) }
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
  if (!urls.length) { ElMessage.warning('请输入网址'); return }
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
    <div class="tb-h">从任意文本中提取所有 http/https 链接。</div>
    <el-input v-model="el.src" type="textarea" :rows="7" placeholder="粘贴含链接的文本…" />
    <div class="tb-row">
      <el-checkbox v-model="el.dedupe">去重</el-checkbox>
      <el-checkbox v-model="el.sort">排序</el-checkbox>
      <el-button type="primary" @click="extractLinks">提取</el-button>
      <el-button @click="copyOut(el.out)">复制结果</el-button>
    </div>
    <el-input v-model="el.out" type="textarea" :rows="7" readonly placeholder="提取结果…" />
  </template>

  <!-- 中文域名转码 -->
  <template v-if="tool === 'punycode'">
    <div class="tb-h">中文域名 ↔ Punycode（xn--）互转，每行一个域名。</div>
    <el-input v-model="pc.src" type="textarea" :rows="6" placeholder="例：例子.中国 或 xn--fsqu00a.xn--55qx5d" />
    <div class="tb-row">
      <el-radio-group v-model="pc.mode">
        <el-radio-button value="encode">中文 → xn--</el-radio-button>
        <el-radio-button value="decode">xn-- → 中文</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="convPuny">转换</el-button>
      <el-button @click="copyOut(pc.out)">复制结果</el-button>
    </div>
    <el-input v-model="pc.out" type="textarea" :rows="6" readonly placeholder="转换结果…" />
  </template>

  <!-- Robots 生成器 -->
  <template v-if="tool === 'robots-gen'">
    <div class="tb-h">可视化生成 robots.txt。</div>
    <div class="tb-row">
      <el-input v-model="rb.ua" placeholder="User-agent（多个逗号分隔）" style="width:240px" />
      <el-input v-model="rb.delay" placeholder="Crawl-delay(可选)" style="width:150px" />
    </div>
    <div class="tb-row">
      <el-input v-model="rb.allow" type="textarea" :rows="3" placeholder="Allow 目录（每行一个）" style="width:320px" />
      <el-input v-model="rb.disallow" type="textarea" :rows="3" placeholder="Disallow 目录（每行一个）" style="width:320px" />
    </div>
    <div class="tb-row">
      <el-input v-model="rb.sitemap" placeholder="Sitemap 完整地址(可选)" style="width:360px" />
      <el-button type="primary" @click="genRobots">生成</el-button>
      <el-button @click="copyOut(rb.out)">复制</el-button>
    </div>
    <el-input v-model="rb.out" type="textarea" :rows="8" readonly placeholder="robots.txt 内容…" />
  </template>

  <!-- Sitemap 生成器 -->
  <template v-if="tool === 'sitemap-gen'">
    <div class="tb-h">网址列表生成 sitemap.xml。</div>
    <el-input v-model="sm.urls" type="textarea" :rows="6" placeholder="每行一个网址…" />
    <div class="tb-row">
      <el-select v-model="sm.freq" style="width:130px">
        <el-option v-for="f in ['always','hourly','daily','weekly','monthly','yearly','never']" :key="f" :label="f" :value="f" />
      </el-select>
      <el-input v-model="sm.priority" placeholder="priority" style="width:110px" />
      <el-input v-model="sm.lastmod" placeholder="lastmod" style="width:150px" />
      <el-button type="primary" @click="genSitemap">生成</el-button>
      <el-button @click="copyOut(sm.out)">复制</el-button>
    </div>
    <el-input v-model="sm.out" type="textarea" :rows="8" readonly placeholder="sitemap.xml 内容…" />
  </template>

  </template>
