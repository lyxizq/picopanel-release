<script setup>
import { computed, ref } from 'vue'
import {
  Connection, Link, Document, Share, Histogram,
  Key, Lock, Picture, CircleCheck, DocumentChecked, Sort, Clock, Brush,
  EditPen, CopyDocument, DataBoard, Monitor, Search,
} from '@element-plus/icons-vue'
import UrlTools from './toolbox/UrlTools.vue'
import CodeTools from './toolbox/CodeTools.vue'
import TextTools from './toolbox/TextTools.vue'
import FormatTools from './toolbox/FormatTools.vue'
import NetTools from './toolbox/NetTools.vue'

defineOptions({ name: 'ToolboxView' })

const ICON = {
  Connection, Link, Document, Share, Histogram,
  Key, Lock, Picture, CircleCheck, DocumentChecked, Sort, Clock, Brush,
  EditPen, CopyDocument, DataBoard, Monitor, Search,
}

const TOOLS = [
  { id: 'extract-links', name: '链接提取', tab: 'url', icon: 'Link', color: '#3370ff', comp: UrlTools },
  { id: 'punycode', name: '中文域名转码', tab: 'url', glyph: 'A文', color: '#3370ff', comp: UrlTools },
  { id: 'robots-gen', name: 'Robots 生成器', tab: 'url', icon: 'Document', color: '#3370ff', comp: UrlTools },
  { id: 'sitemap-gen', name: 'Sitemap 生成器', tab: 'url', icon: 'Share', color: '#3370ff', comp: UrlTools },

  { id: 'url-code', name: 'URL 编码/解码', tab: 'code', glyph: '</>', color: '#3370ff', comp: CodeTools },
  { id: 'md5', name: 'MD5 生成', tab: 'code', glyph: '#', color: '#4b5563', comp: CodeTools },
  { id: 'base-convert', name: '进制转换', tab: 'code', icon: 'Histogram', color: '#22b8cf', comp: CodeTools },
  { id: 'uuid', name: 'UUID 生成', tab: 'code', icon: 'Key', color: '#4b5563', comp: CodeTools },
  { id: 'pwd-gen', name: '密码生成器', tab: 'code', icon: 'Lock', color: '#e6a700', comp: CodeTools },
  { id: 'base64', name: 'Base64 加密/解密', tab: 'code', glyph: '01', color: '#3370ff', comp: CodeTools },
  { id: 'img-base64', name: '图片转 Base64', tab: 'code', icon: 'Picture', color: '#e6a700', comp: CodeTools },
  { id: 'unicode', name: 'Unicode/ASCII 转换', tab: 'code', glyph: '文', color: '#22b8cf', comp: CodeTools },
  { id: 'ascii-code', name: 'ASCII 编码/解码', tab: 'code', glyph: 'A文', color: '#22b8cf', comp: CodeTools },
  { id: 'escape', name: 'Escape 加密/解密', tab: 'code', glyph: '{ }', color: '#3370ff', comp: CodeTools },
  { id: 'sha', name: 'SHA/SHA256 加密', tab: 'code', icon: 'CircleCheck', color: '#4b5563', comp: CodeTools },
  { id: 'file-hash', name: '文件 Hash 校验', tab: 'code', icon: 'DocumentChecked', color: '#4b5563', comp: CodeTools },
  { id: 'rand-num', name: '随机数生成器', tab: 'code', icon: 'Sort', color: '#e6a700', comp: CodeTools },

  { id: 'timestamp', name: 'Unix 时间戳转换', tab: 'util', icon: 'Clock', color: '#e6a700', comp: TextTools },
  { id: 'rgb-color', name: 'RGB 颜色转换', tab: 'util', icon: 'Brush', color: '#e6a700', comp: TextTools },
  { id: 'text-replace', name: '文本替换', tab: 'util', icon: 'EditPen', color: '#4b5563', comp: TextTools },
  { id: 'text-diff', name: '文本对比 (Diff)', tab: 'util', icon: 'CopyDocument', color: '#4b5563', comp: TextTools },
  { id: 'word-count', name: '在线统计字数', tab: 'util', icon: 'DataBoard', color: '#4b5563', comp: TextTools },
  { id: 'ua-parse', name: 'UA 分析', tab: 'util', icon: 'Monitor', color: '#e6a700', comp: TextTools },
  { id: 'net-check', name: 'TCP/UDP 连通检测', tab: 'util', icon: 'Connection', color: '#1f9d61', comp: NetTools },

  { id: 'json-format', name: 'JSON 格式化', tab: 'code', icon: 'Document', color: '#3370ff', comp: FormatTools },
  { id: 'json-compress', name: 'JSON 压缩转义', tab: 'code', icon: 'Document', color: '#3370ff', comp: FormatTools },
  { id: 'html-format', name: 'HTML 格式化压缩', tab: 'code', glyph: '</>', color: '#3370ff', comp: FormatTools },
  { id: 'xml-format', name: 'XML 格式化', tab: 'code', icon: 'Document', color: '#3370ff', comp: FormatTools },
  { id: 'css-format', name: 'CSS 格式化压缩', tab: 'code', glyph: '</>', color: '#3370ff', comp: FormatTools },
  { id: 'css-gradient', name: 'CSS 渐变生成器', tab: 'code', icon: 'Brush', color: '#e6a700', comp: FormatTools },
  { id: 'js-format', name: 'JS 格式化压缩', tab: 'code', glyph: '</>', color: '#3370ff', comp: FormatTools },
  { id: 'regex-test', name: '正则表达式测试', tab: 'code', icon: 'Search', color: '#e6a700', comp: FormatTools },
  { id: 'md-html', name: 'HTML/Markdown 互转', tab: 'code', glyph: '</>', color: '#3370ff', comp: FormatTools },
]

const TABS = [
  { key: 'all', label: '全部工具' },
  { key: 'url', label: '网址工具' },
  { key: 'code', label: '编码工具' },
  { key: 'util', label: '实用工具' },
]

const cur = ref(null)
const toolDlg = ref(false)
const tabK = ref('all')

const shown = computed(() =>
  tabK.value === 'all' ? TOOLS : TOOLS.filter(t => t.tab === tabK.value))

// 点击卡片弹层打开工具（39 用户指定不做页内跳转）；关完再清 cur 保关闭动画不空白
function openTool(t) {
  cur.value = t
  toolDlg.value = true
}
</script>

<template>
  <div class="toolbox">
    <!-- 工具列表（顶部 tab + 卡片网格）；点卡片弹层开工具，不跳页 -->
    <div class="tb-home">
      <div class="tb-tabs">
        <button
          v-for="tab in TABS" :key="tab.key" type="button"
          :class="['tb-tab', { active: tabK === tab.key }]"
          @click="tabK = tab.key"
        >{{ tab.label }}</button>
      </div>
      <div class="tb-grid">
        <!-- 只放独立小工具；系统自带整页功能不放这里（2026-10-19 用户约定，见 memory/toolbox-scope.md） -->
        <div v-for="t in shown" :key="t.id" class="tb-card" @click="openTool(t)">
          <span v-if="t.glyph" class="tb-glyph" :style="{ color: t.color }">{{ t.glyph }}</span>
          <el-icon v-else class="tb-card-icon" :size="26" :style="{ color: t.color }">
            <component :is="ICON[t.icon]" />
          </el-icon>
          <span class="tb-card-name">{{ t.name }}</span>
        </div>
      </div>
    </div>

    <!-- 工具弹层：标题=工具名，内容即原工具页组件（append-to-body 逃玻璃 .main 口径） -->
    <el-dialog append-to-body v-model="toolDlg" :title="cur ? cur.name : ''" width="min(860px, 94vw)" @closed="cur = null">
      <div v-if="cur" class="tb-dlg-body">
        <component :is="cur.comp" :key="cur.id" :tool="cur.id" />
      </div>
    </el-dialog>
  </div>
</template>

<style>
/* 工具箱共享样式（非 scoped：四个分组子组件同用） */
.toolbox { height: 100%; min-height: 0; }
.tb-home { height: 100%; overflow-y: auto; padding-right: 2px; }
.tb-tabs { display: flex; align-items: center; gap: 6px; padding: 12px 0 20px; flex-wrap: wrap; }
.tb-tab {
  border: none; background: none; font-size: 15px; color: var(--text-2);
  padding: 7px 16px; border-radius: 999px; cursor: pointer;
  transition: color .15s, background-color .15s;
}
.tb-tab:hover:not(.active) { color: var(--brand); }
.tb-tab.active { background: var(--brand); color: #fff; font-weight: 600; }
.tb-grid {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(148px, 1fr));
  gap: 12px 13px; padding: 2px 0 30px;
}
.tb-card {
  background: var(--surface); border: 1px solid var(--border); border-radius: 12px;
  min-height: 128px; display: flex; flex-direction: column; align-items: center;
  justify-content: center; gap: 12px; cursor: pointer; padding: 18px 12px;
  transition: border-color .15s, transform .15s, box-shadow .15s;
}
.tb-card:hover {
  border-color: var(--brand); transform: translateY(-3px);
  box-shadow: 0 6px 18px rgba(0, 0, 0, .07);
}
.tb-card-icon { display: block; }
.tb-glyph {
  height: 26px; display: flex; align-items: center; justify-content: center;
  font-size: 16px; font-weight: 700; line-height: 1;
  font-family: ui-monospace, Menlo, Consolas, monospace;
}
.tb-card-name { font-size: 14px; font-weight: 600; color: var(--ink); text-align: center; line-height: 1.4; }

/* 工具弹层内容：原工具页体的留白口径 */
.tb-dlg-body { padding: 2px 4px 6px; }

/* 分组子组件共用 */
.tb-h { font-size: 13px; color: var(--muted); margin: 2px 0 8px; line-height: 1.6; }
.tb-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin: 8px 0; }
.tb-lb { font-size: 13px; color: var(--text-2); }
.tb-row .el-textarea, .tb-panel > .el-textarea { width: 100%; }
.tb-panel .el-textarea__inner[readonly] { font-family: ui-monospace, Menlo, Consolas, monospace; }
.tb-cols { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.tb-kv {
  display: flex; align-items: center; gap: 10px; padding: 6px 0;
  font-size: 13px; border-bottom: 1px dashed var(--border);
}
.tb-kv b { flex: none; width: 64px; }
.tb-kv span { flex: 1; word-break: break-all; font-family: ui-monospace, Menlo, Consolas, monospace; }
.tb-swatch {
  display: inline-block; width: 34px; height: 24px; border-radius: 4px;
  border: 1px solid var(--border); vertical-align: middle;
}
.tb-stats { display: grid; grid-template-columns: repeat(auto-fill, minmax(110px, 1fr)); gap: 8px; margin-top: 10px; }
.tb-stats > div {
  border: 1px solid var(--border); border-radius: 8px; padding: 10px 12px;
  display: flex; flex-direction: column; gap: 2px;
}
.tb-stats b { font-size: 20px; }
.tb-stats span { font-size: 12px; color: var(--muted); }
.tb-diff {
  border: 1px solid var(--border); border-radius: 8px; overflow: auto;
  max-height: 420px; font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 12.5px;
}
.tb-dl { display: flex; white-space: pre-wrap; word-break: break-all; padding: 0 6px; }
.tb-dl-mark { flex: none; width: 18px; color: var(--muted); user-select: none; }
.tb-dl-del { background: rgba(245, 108, 108, .12); }
.tb-dl-add { background: rgba(103, 194, 58, .12); }
.tb-regex-out {
  border: 1px solid var(--border); border-radius: 8px; padding: 10px 12px;
  font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 13px;
  white-space: pre-wrap; word-break: break-all; max-height: 320px; overflow: auto;
  line-height: 1.7;
}
.tb-regex-out mark { background: #f8d347; color: #21262d; border-radius: 2px; padding: 0 1px; }
.tb-err { color: var(--crit, #f56c6c); font-size: 13px; margin: 4px 0; }
.tb-grad-preview { height: 110px; border-radius: 8px; border: 1px solid var(--border); margin: 8px 0; }
.tb-md-preview {
  border: 1px solid var(--border); border-radius: 8px; padding: 12px 14px;
  overflow: auto; max-height: 420px; font-size: 14px; line-height: 1.7;
}
.tb-md-preview h1, .tb-md-preview h2, .tb-md-preview h3 { margin: 10px 0 6px; }
.tb-md-preview pre { background: var(--el-fill-color-light); padding: 10px; border-radius: 6px; overflow: auto; }
.tb-md-preview blockquote { border-left: 3px solid var(--brand); margin: 6px 0; padding: 2px 12px; color: var(--text-2); }

@media (max-width: 768px) {
  .tb-cols { grid-template-columns: 1fr; }
  .tb-grid { grid-template-columns: repeat(auto-fill, minmax(112px, 1fr)); gap: 10px 11px; }
  .tb-card { min-height: 102px; gap: 9px; }
  .tb-card-name { font-size: 13px; }
  .tb-tab { font-size: 14px; padding: 6px 12px; }
  .tb-panel-head { padding: 12px 14px; }
  .tb-panel-title h1 { font-size: 17px; }
  .tb-panel-body { padding: 14px 14px 20px; }
}
</style>
