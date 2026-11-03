// 拷贝 vditor 运行时按需加载的资源到 public/vendor/（gitignore，构建期从
// node_modules 取，保证单二进制离线可用——vditor 默认从 unpkg CDN 拉这些文件）。
// 全量 dist 22MB，只挑本站用到的子集约 11MB：lute 渲染引擎（必需）、
// highlight.js 代码高亮、KaTeX 公式、Mermaid 图表、工具栏图标、中文语言包、
// 预览内容主题、表情图片。mathjax/echarts/graphviz/abcjs 等未启用对应功能不拷。
import { cpSync, rmSync, mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const src = join(root, 'node_modules/vditor/dist')
const dst = join(root, 'public/vendor/vditor/dist')

const parts = [
  'js/lute',
  'js/highlight.js',
  'js/katex',
  'js/mermaid',
  'js/icons',
  'js/i18n/zh_CN.js',
  'css/content-theme',
  'images',
]

rmSync(dst, { recursive: true, force: true })
for (const p of parts) {
  const target = join(dst, p)
  mkdirSync(dirname(target), { recursive: true })
  cpSync(join(src, p), target, { recursive: true })
}
console.log(`vditor vendor assets -> public/vendor/vditor/dist (${parts.length} entries)`)
