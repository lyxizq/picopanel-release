<template>
  <!-- EP 按需引入后 locale 走 ConfigProvider（原 app.use(ElementPlus,{locale}) 已移除）；
       组件内置文案（日期选择/分页/弹窗按钮等）跟随界面语言 -->
  <el-config-provider :locale="epLocale">
    <router-view />
  </el-config-provider>
</template>

<script setup>
import { computed } from 'vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'
import { lang } from './i18n'

const epLocale = computed(() => (lang.value === 'en' ? en : zhCn))
</script>

<style>
/* 明暗两套语义色：组件里一律用 var(--*)，暗色取 Grafana 风格的深蓝灰面板色 */
:root {
  --bg: #f9f9f7; /* 页面底色 */
  --surface: #ffffff; /* 卡片/侧栏等面板 */
  --border: #e1e0d9; /* 分隔线/描边 */
  --ink: #0b0b0b; /* 主文字 */
  --text-2: #52514e; /* 次级文字 */
  --muted: #898781; /* 弱化文字 */
  --ok: #0ca30c; /* 在线/正常 */
  --warn-text: #b27b00; /* 利用率高（文字用） */
  --crit: #d03b3b; /* 告警/危险 */
  --dot-off: #c3c2b7; /* 离线圆点 */
  --brand: #2a78d6; /* 品牌蓝 */

  /* 表格线比 Element 默认（#ebeef5）加深一档，全站表格可读 */
  --el-table-border-color: #d4d3cc;
}
html.dark {
  --bg: #111217; /* Grafana 风格页面底色 */
  --surface: #181b1f; /* 面板 */
  --border: #2a2e33;
  --ink: #e0e2e5;
  --text-2: #9aa0a6;
  --muted: #7a8085;
  --ok: #29c128;
  --warn-text: #e0a03c;
  --crit: #ef5b5b;
  --dot-off: #565b61;
  --brand: #3d8bdd;

  /* Element Plus 暗色变量对齐到同一套面板色 */
  --el-bg-color: #181b1f;
  --el-bg-color-overlay: #1f2328;
  --el-bg-color-page: #111217;
  --el-border-color: #2a2e33;
  --el-border-color-light: #2a2e33;
  --el-border-color-lighter: #23272b;
  --el-text-color-primary: #e0e2e5;
  --el-text-color-regular: #c3c7cb;
  --el-text-color-secondary: #9aa0a6;
  --el-fill-color-light: #20252b;
  --el-fill-color-blank: #181b1f;

  /* 暗色下面板更深，表格线取亮一档才可见 */
  --el-table-border-color: #363c44;
}

/* 紫罗兰主题（完全照抄主页：同款深紫渐变背景 + 白透玻璃卡片）：
   挂 :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss)——Element Plus 暗色变量打底，此处只覆盖紫色段。
   面板底色与主页玻璃卡同口径（rgba 白 8% + 13% 描边），body 画主页同款渐变 */
html.dark.violet {
  /* --bg 作次级底色（事项行/tab 底/胶囊等），用白透保持玻璃体系；body 底色由下方渐变承担 */
  --bg: rgba(255, 255, 255, 0.04);
  --surface: rgba(255, 255, 255, 0.08);
  --border: rgba(255, 255, 255, 0.13);
  --ink: #ece8fa;
  --text-2: #cdc6e8;
  --muted: #a9a2ce;
  --dot-off: #6b6390;
  --brand: #9d85f0;

  /* Element Plus 变量对齐主页玻璃卡；弹层统一深紫磨砂（白透太浅看不清，2026-10-02 定） */
  --el-bg-color: rgba(255, 255, 255, 0.08);
  --el-bg-color-overlay: rgba(32, 24, 66, 0.94);
  --el-dialog-bg-color: rgba(32, 24, 66, 0.94);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(255, 255, 255, 0.13);
  --el-border-color-light: rgba(255, 255, 255, 0.10);
  --el-border-color-lighter: rgba(255, 255, 255, 0.08);
  --el-text-color-primary: #ece8fa;
  --el-text-color-regular: #d4cee9;
  --el-text-color-secondary: #a49bcf;
  --el-text-color-placeholder: #7d75a5;
  --el-fill-color-light: rgba(255, 255, 255, 0.07);
  --el-fill-color-lighter: rgba(255, 255, 255, 0.05);
  --el-fill-color: rgba(255, 255, 255, 0.09);
  --el-fill-color-dark: rgba(255, 255, 255, 0.10);
  --el-fill-color-blank: rgba(255, 255, 255, 0.05);
  --el-table-border-color: rgba(255, 255, 255, 0.15);
  --el-mask-color: rgba(18, 12, 40, 0.55);

  /* 表格：行底透明融进玻璃，表头浅白透（EP 暗色默认深底在玻璃上是暗块） */
  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(255, 255, 255, 0.10);
  --el-table-header-text-color: #d4cee9;
  --el-table-row-hover-bg-color: rgba(255, 255, 255, 0.08);
  /* 描述列表（系统信息等）label 列底：EP 暗色默认深底改浅白透 */
  --el-descriptions-item-bordered-label-background: rgba(255, 255, 255, 0.08);
  /* 卡片（服务器模块/我的关注图表卡等 el-card）对齐数据监控玻璃磁贴：同底色同圆角 */
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  /* 标签（Agent/上报/状态等）：EP 暗色默认黑底改彩色半透，玻璃上不突兀 */
  --el-tag-bg-color: rgba(255, 255, 255, 0.10);
  --el-tag-border-color: rgba(255, 255, 255, 0.18);
  --el-tag-text-color: #d4cee9;

  /* 品牌紫（主页 #7c5ce8 系），按钮/激活/胶囊全站跟随 */
  --el-color-primary: #8a6cf0;
  --el-color-primary-light-3: #a48cf4;
  --el-color-primary-light-5: #b8a6f7;
  --el-color-primary-light-7: #d1c6fa;
  --el-color-primary-light-8: #ded6fc;
  --el-color-primary-light-9: #efeafd;
  --el-color-primary-dark-2: #6f52d6;
}

/* 紫罗兰：页面底 = 主页 .pv-bg 同款渐变原样照抄（fixed 不随滚动），面板玻璃透出 */
html.dark.violet body {
  background:
    radial-gradient(120% 90% at 88% 8%, #2b4c9b 0%, transparent 55%),
    radial-gradient(130% 100% at 10% 92%, #b03b6f 0%, transparent 58%),
    radial-gradient(150% 120% at 55% 125%, #7a2e6e 0%, transparent 65%),
    linear-gradient(160deg, #101a3a 0%, #1b2350 45%, #3a2158 100%);
  background-attachment: fixed;
}

/* 紫罗兰：全局玻璃拟态——面板级容器统一毛玻璃（半透明底色来自上面的变量）。
   自定义面板（各页 .panel/.km-tile 等）+ Element Plus 弹层容器都在清单内 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .aside,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .header,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tabbar,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .side-list,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .side,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .main,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .panel,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .km-tile,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .grp,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tb-card,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tb-panel,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tb-grid,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .docs-side,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .docs-main,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .mobile-bar,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .doc-edit-page,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .fm-empty,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .ctx-menu,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tab-ctx-menu,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .xfers,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .nc-result,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-card,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dialog,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-drawer,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dropdown-menu,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-select-dropdown,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-popover.el-popper:not(.el-tooltip),
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-message,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-message-box {
  backdrop-filter: blur(16px) saturate(1.35);
  -webkit-backdrop-filter: blur(16px) saturate(1.35);
}

/* 紫罗兰：标签按类型改彩色半透底（EP 暗色默认深底换浅彩玻璃） */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-tag--success {
  --el-tag-bg-color: rgba(63, 224, 130, 0.16);
  --el-tag-border-color: rgba(63, 224, 130, 0.35);
  --el-tag-text-color: #7ee2a1;
}
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-tag--info {
  --el-tag-bg-color: rgba(255, 255, 255, 0.10);
  --el-tag-border-color: rgba(255, 255, 255, 0.18);
  --el-tag-text-color: #bdb5dd;
}
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-tag--warning {
  --el-tag-bg-color: rgba(255, 159, 67, 0.16);
  --el-tag-border-color: rgba(255, 159, 67, 0.35);
  --el-tag-text-color: #ffc98a;
}
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-tag--danger {
  --el-tag-bg-color: rgba(239, 91, 91, 0.16);
  --el-tag-border-color: rgba(239, 91, 91, 0.35);
  --el-tag-text-color: #ff9d97;
}
/* effect=dark 的标签（告警计数等）同口径浅化 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-tag--dark {
  --el-tag-bg-color: rgba(255, 255, 255, 0.12);
  --el-tag-border-color: rgba(255, 255, 255, 0.20);
  --el-tag-text-color: #ece8fa;
}
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-tag--dark.el-tag--danger {
  --el-tag-bg-color: rgba(239, 91, 91, 0.30);
  --el-tag-border-color: rgba(239, 91, 91, 0.45);
  --el-tag-text-color: #ffc9c5;
}

/* 紫罗兰：plain 按钮（更新重启/重启等）EP 暗色深底改玻璃白透，字色保持类型色 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-button.is-plain {
  background-color: rgba(255, 255, 255, 0.08);
}
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-button.is-plain:hover,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-button.is-plain:focus {
  background-color: rgba(255, 255, 255, 0.14);
}

/* 玻璃弹层：浅色白透底 + 半透明白描边（玻璃边框）+ 深紫投影 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dropdown-menu,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-select-dropdown,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-popover.el-popper:not(.el-tooltip),
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dialog,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-message-box,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-message {
  border: 1px solid rgba(255, 255, 255, 0.25);
  box-shadow: 0 8px 32px rgba(15, 8, 40, 0.5);
}
/* 下拉弹层只画一层玻璃：EP 的 .el-popper 外壳与内层 menu/dropdown 都画
   --el-bg-color-overlay（violet 半透白 0.14 叠 0.14 成两层），外壳整层关掉，
   底色/描边/模糊/投影由内层 menu 承担（浅色/暗色主题底色不透明，无此问题不动）。
   el-select 同病（345 用户反馈连接下拉双层）一并关掉，箭头随浅玻璃重配色 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dropdown__popper.el-popper,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-select__popper.el-popper {
  background: transparent;
  border: none;
  box-shadow: none;
}
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-select__popper.el-popper .el-popper__arrow::before {
  background: linear-gradient(rgba(255, 255, 255, 0.08), rgba(255, 255, 255, 0.08)), var(--el-bg-color-overlay);
  border: 1px solid rgba(255, 255, 255, 0.25);
}
/* 选项下拉玻璃底（345 曾调白透 10%，2026-10-23 用户报图下拉透出底下表单文字）：
   改主题 overlay 深底（0.94+ 不透明）叠 8% 白提亮保留玻璃感，全局 blur 继续兜底；
   悬停项提亮一档，避免与底色粘连难分辨 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-select-dropdown {
  --el-fill-color-light: rgba(255, 255, 255, 0.16);
  background: linear-gradient(rgba(255, 255, 255, 0.08), rgba(255, 255, 255, 0.08)), var(--el-bg-color-overlay);
}
/* dropdown 菜单（用户菜单等）同口径（348 曾调白透 12%过透，随上条一起收）：悬停项走
   EP 主色浅档自然提亮 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dropdown-menu {
  background: linear-gradient(rgba(255, 255, 255, 0.08), rgba(255, 255, 255, 0.08)), var(--el-bg-color-overlay);
}
/* dropdown popper 箭头直接隐藏（2026-10-03 用户指定）：261 关外壳底色后箭头
   （z-index:-1 悬在壳层、菜单盖不住）悬在菜单上方成小菱形，重配色仍显突兀 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dropdown__popper.el-popper .el-popper__arrow {
  display: none;
}
/* 弹层深紫磨砂（2026-10-03 用户指定）：底色换首页 .pv-bg 同款四层渐变紫（半透让
   毛玻璃透出底层），玻璃边=25% 白描边 + 内圈微光 + 深紫投影。gold 同构段见下方金色版 */
html.dark.violet .el-dialog,
html.dark.violet .el-drawer,
html.dark.violet .el-message-box {
  background:
    radial-gradient(120% 90% at 88% 8%, rgba(43, 76, 155, 0.5) 0%, transparent 55%),
    radial-gradient(130% 100% at 10% 92%, rgba(176, 59, 111, 0.45) 0%, transparent 58%),
    radial-gradient(150% 120% at 55% 125%, rgba(122, 46, 110, 0.5) 0%, transparent 65%),
    linear-gradient(160deg, rgba(16, 26, 58, 0.94) 0%, rgba(27, 35, 80, 0.94) 45%, rgba(58, 33, 88, 0.94) 100%);
  backdrop-filter: blur(18px) saturate(1.3);
  -webkit-backdrop-filter: blur(18px) saturate(1.3);
  border: 1px solid rgba(255, 255, 255, 0.25);
  border-radius: 12px;
  box-shadow: 0 18px 50px rgba(10, 6, 30, 0.55), inset 0 0 0 1px rgba(255, 255, 255, 0.06);
}
/* violet 全部 tooltip（is-dark 底色取 EP 变量，白透叠白透看不清字）：深紫实底 + 亮字 */
html.dark.violet .el-popper.is-dark {
  background: rgba(32, 24, 66, 0.96);
  color: #ece8fa;
  border: 1px solid rgba(255, 255, 255, 0.2);
}
html.dark.violet .el-popper.is-dark .el-popper__arrow::before {
  background: rgba(32, 24, 66, 0.96);
  border-color: rgba(255, 255, 255, 0.2);
}
/* violet wangeditor 弹出面板（颜色选择/链接弹窗等，默认 #fff 白底 + 继承亮色字 → 看不清）：
   统一换深紫磨砂变量底 + 亮字（输入框/按钮走同一组变量，颜色一致） */
html.dark.violet .w-e-drop-panel,
html.dark.violet .w-e-select-list,
html.dark.violet .w-e-modal,
html.dark.violet .w-e-toolbar {
  --w-e-toolbar-bg-color: rgba(32, 24, 66, 0.97);
  --w-e-toolbar-color: #ece8fa;
  --w-e-toolbar-border-color: rgba(255, 255, 255, 0.2);
  --w-e-toolbar-active-bg-color: rgba(255, 255, 255, 0.12);
  --w-e-toolbar-active-color: #ffffff;
  --w-e-toolbar-disabled-color: #7d75a5;
  --w-e-modal-button-bg-color: rgba(255, 255, 255, 0.1);
  --w-e-modal-button-border-color: rgba(255, 255, 255, 0.25);
  --w-e-textarea-bg-color: rgba(255, 255, 255, 0.08);
  --w-e-textarea-border-color: rgba(255, 255, 255, 0.22);
  --w-e-textarea-color: #ece8fa;
  background: rgba(32, 24, 66, 0.97);
  color: #ece8fa;
}
/* 文档富文本待办复选框：暗色/紫罗兰 color-scheme:dark 把原生未选框渲染成黑底，
   编辑器与预览里的待办框强制亮色渲染（同亮色主题白底） */
.w-e-text-container input[type="checkbox"],
.doc-preview input[type="checkbox"],
.doc-render input[type="checkbox"] {
  color-scheme: light;
}
/* violet plain 按钮描边（EP 暗色 plain 边框近黑，玻璃上像黑边）：换类型色半透描边 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-button.is-plain {
  border-color: rgba(255, 255, 255, 0.22);
}
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-button--success.is-plain {
  background-color: rgba(63, 224, 130, 0.13);
  border-color: rgba(63, 224, 130, 0.45);
  color: #8ee6ae;
}
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-button--success.is-plain:hover,
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-button--success.is-plain:focus {
  background-color: rgba(63, 224, 130, 0.22);
  border-color: rgba(63, 224, 130, 0.6);
  color: #a9efc2;
}

/* 手机端 blur 减半保性能（玻璃底色半透明不变，观感略收敛） */
@media (max-width: 768px) {
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .aside,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .header,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tabbar,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .side-list,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .side,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .main,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .panel,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .km-tile,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .grp,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tb-card,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tb-panel,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tb-grid,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .docs-side,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .docs-main,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .mobile-bar,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .doc-edit-page,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .fm-empty,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .ctx-menu,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .tab-ctx-menu,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .xfers,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .nc-result,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-card,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dialog,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-drawer,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dropdown-menu,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-select-dropdown,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-popover.el-popper:not(.el-tooltip),
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-message,
  :is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-message-box {
    backdrop-filter: blur(8px) saturate(1.25);
    -webkit-backdrop-filter: blur(8px) saturate(1.25);
  }
}

* {
  box-sizing: border-box;
}
body {
  margin: 0;
  font-family: system-ui, -apple-system, 'Segoe UI', 'PingFang SC', sans-serif;
  background: var(--bg);
  color: var(--ink);
}

/* 表格圆角统一（监控列表/告警历史/告警规则/分组管理/脚本列表等所有 el-table）：
   圆角描边容器 + 裁切，表头底色随圆角收口 */
.el-table {
  border-radius: 10px;
  overflow: hidden;
  border: 1px solid var(--border);
}
/* 与监控页服务器列表（ServerRows）同款行观感的表格：行高统一 50、单元格单行 */
.el-table.row-table {
  font-size: 14px;
}
.el-table.row-table td.el-table__cell {
  height: 50px;
  padding: 0;
  vertical-align: middle;
}
.el-table.row-table td .cell {
  white-space: nowrap;
}

/* 文档编辑/查看全屏（258）：编辑器全屏本就是 fixed 视口定位，但祖先带
   backdrop-filter（violet 手机端玻璃）会成为 containing block 把全屏限在面板内——
   全屏进行中摘掉文档区祖先的模糊，保证铺满整个浏览器窗口 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .docs-main:has(.w-e-full-screen-container),
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .docs-page:has(.w-e-full-screen-container),
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .docs-main:has(.vditor--fullscreen),
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .docs-page:has(.vditor--fullscreen) {
  backdrop-filter: none !important;
  -webkit-backdrop-filter: none !important;
  transform: none !important;
  filter: none !important;
}

/* 表格固定列改近实色底（2026-10-03，342 玻璃方案的性能返工）：此前每格挂
   backdrop-filter blur(16px)，45 台机器的列表 = 90+ 个独立模糊面，滚动时
   每帧全量重算导致绘制跟不上、出现大片空白表格。改用弹层同款深紫近实底
   （0.96 不透明，底下内容不可辨=延续 261/342 防透字口径），零模糊开销 */
html.dark.violet .el-table .el-table-fixed-column--left,
html.dark.violet .el-table .el-table-fixed-column--right {
  background: rgba(32, 24, 66, 0.96);
}
html.dark.violet .el-table .el-table__header-wrapper .el-table-fixed-column--left,
html.dark.violet .el-table .el-table__header-wrapper .el-table-fixed-column--right {
  background: rgba(54, 47, 86, 0.96); /* 表头浅一档（原白透 0.10 叠加观感的实色近似） */
}


/* ===== 暗金主题（38，2026-10-19）：暖黑金壁纸 + 白透玻璃 + 品牌金 #d4af37。
   结构性玻璃规则已与 violet 以 :is() 合流，本段只补 gold 配色（变量/壁纸/弹层底/
   tooltip/富文本面板/菜单选中/表格固定列），口径同 violet 各对应段 ===== */
html.dark.gold {
  --bg: rgba(255, 236, 190, 0.04);
  --surface: rgba(255, 236, 190, 0.07);
  --border: rgba(232, 201, 106, 0.18);
  --ink: #f3ead2;
  --text-2: #ddd0ab;
  --muted: #b3a67e;
  --dot-off: #8a7c55;
  --brand: #d4af37;

  --el-bg-color: rgba(255, 236, 190, 0.07);
  --el-bg-color-overlay: rgba(22, 18, 9, 0.96);
  --el-dialog-bg-color: rgba(22, 18, 9, 0.96);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(232, 201, 106, 0.16);
  --el-border-color-light: rgba(232, 201, 106, 0.12);
  --el-border-color-lighter: rgba(232, 201, 106, 0.09);
  --el-text-color-primary: #f3ead2;
  --el-text-color-regular: #e2d6b4;
  --el-text-color-secondary: #bfb28a;
  --el-text-color-placeholder: #8a7c55;
  --el-fill-color-light: rgba(255, 244, 214, 0.07);
  --el-fill-color-lighter: rgba(255, 244, 214, 0.05);
  --el-fill-color: rgba(255, 244, 214, 0.09);
  --el-fill-color-dark: rgba(255, 244, 214, 0.10);
  --el-fill-color-blank: rgba(255, 244, 214, 0.05);
  --el-table-border-color: rgba(232, 201, 106, 0.18);
  --el-mask-color: rgba(8, 6, 3, 0.6);

  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(255, 236, 190, 0.10);
  --el-table-header-text-color: #e2d6b4;
  --el-table-row-hover-bg-color: rgba(255, 244, 214, 0.08);
  --el-descriptions-item-bordered-label-background: rgba(255, 244, 214, 0.08);
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  --el-tag-bg-color: rgba(255, 244, 214, 0.10);
  --el-tag-border-color: rgba(232, 201, 106, 0.20);
  --el-tag-text-color: #e2d6b4;

  /* 品牌金，按钮/激活/胶囊全站跟随 */
  --el-color-primary: #d4af37;
  --el-color-primary-light-3: #dfc069;
  --el-color-primary-light-5: #e8d08e;
  --el-color-primary-light-7: #f1e0b4;
  --el-color-primary-light-8: #f6e9c9;
  --el-color-primary-light-9: #faf3dd;
  --el-color-primary-dark-2: #aa8c28;
}

/* 暗金：页面底 = 设计图同款暖黑金渐变（fixed 不随滚动），面板玻璃透出 */
html.dark.gold body {
  background:
    radial-gradient(120% 90% at 88% 8%, rgba(118, 92, 28, 0.45) 0%, transparent 55%),
    radial-gradient(130% 100% at 10% 92%, rgba(138, 72, 32, 0.4) 0%, transparent 58%),
    radial-gradient(150% 120% at 55% 125%, rgba(108, 80, 30, 0.4) 0%, transparent 65%),
    linear-gradient(160deg, #0b0803 0%, #140f07 45%, #1d1608 100%);
  background-attachment: fixed;
}

/* 暗金：弹层深棕夜磨砂（同 violet 弹层四层渐变结构，色调换金铜） */
html.dark.gold .el-dialog,
html.dark.gold .el-drawer,
html.dark.gold .el-message-box {
  background:
    radial-gradient(120% 90% at 88% 8%, rgba(118, 92, 28, 0.34) 0%, transparent 55%),
    radial-gradient(130% 100% at 10% 92%, rgba(138, 72, 32, 0.3) 0%, transparent 58%),
    radial-gradient(150% 120% at 55% 125%, rgba(108, 80, 30, 0.34) 0%, transparent 65%),
    linear-gradient(160deg, rgba(12, 9, 4, 0.95) 0%, rgba(20, 15, 7, 0.95) 45%, rgba(28, 21, 9, 0.95) 100%);
  backdrop-filter: blur(18px) saturate(1.3);
  -webkit-backdrop-filter: blur(18px) saturate(1.3);
  border: 1px solid rgba(232, 201, 106, 0.25);
  border-radius: 12px;
  box-shadow: 0 18px 50px rgba(12, 9, 3, 0.55), inset 0 0 0 1px rgba(255, 244, 214, 0.06);
}

/* 暗金：dark tooltip 深棕夜 */
html.dark.gold .el-popper.is-dark {
  background: rgba(18, 15, 8, 0.97);
  color: #f3ead2;
  border: 1px solid rgba(232, 201, 106, 0.2);
}
html.dark.gold .el-popper.is-dark .el-popper__arrow::before {
  background: rgba(18, 15, 8, 0.97);
  border-color: rgba(232, 201, 106, 0.2);
}

/* 暗金 wangeditor 弹出面板（同 violet 口径金色版） */
html.dark.gold .w-e-drop-panel,
html.dark.gold .w-e-select-list,
html.dark.gold .w-e-modal,
html.dark.gold .w-e-toolbar {
  --w-e-toolbar-bg-color: rgba(18, 15, 8, 0.97);
  --w-e-toolbar-color: #f3ead2;
  --w-e-toolbar-border-color: rgba(232, 201, 106, 0.2);
  --w-e-toolbar-active-bg-color: rgba(255, 244, 214, 0.12);
  --w-e-toolbar-active-color: #ffffff;
  --w-e-toolbar-disabled-color: #8a7c55;
  --w-e-modal-button-bg-color: rgba(255, 244, 214, 0.1);
  --w-e-modal-button-border-color: rgba(232, 201, 106, 0.25);
  --w-e-textarea-bg-color: rgba(255, 244, 214, 0.08);
  --w-e-textarea-border-color: rgba(232, 201, 106, 0.22);
  --w-e-textarea-color: #f3ead2;
  background: rgba(18, 15, 8, 0.97);
  color: #f3ead2;
}

/* 暗金：表格固定列近实色深棕夜（延续 violet 342 防透字/性能口径） */
html.dark.gold .el-table .el-table-fixed-column--left,
html.dark.gold .el-table .el-table-fixed-column--right {
  background: rgba(16, 13, 6, 0.97);
}
html.dark.gold .el-table .el-table__header-wrapper .el-table-fixed-column--left,
html.dark.gold .el-table .el-table__header-wrapper .el-table-fixed-column--right {
  background: rgba(38, 31, 14, 0.97);
}

/* ===== 八套新暗色主题（41，2026-10-19 用户确认设计图后落地）：青碧 teal / 绯夜 rose /
   冰川 glacier / 熔岩 ember / 苔原 moss / 石墨 graphite / 霓 neon / 深海 abyss。
   结构与 gold 全同构：结构性玻璃规则已随 :is() 十主题合流；本段只补配色——每主题一个
   变量块（EP 变量 + --fp-* 壁纸/磨砂参数），弹层磨砂/tooltip/富文本/固定列为八主题共用
   的变量驱动通用段（下方），色相全由变量带出 ===== */

/* 青碧 teal：壁纸深绿松石系 + 品牌 #2ec49b */
html.dark.teal {
  --bg: rgba(216, 244, 235, 0.04);
  --surface: rgba(216, 244, 235, 0.07);
  --border: rgba(46, 196, 155, 0.18);
  --ink: #e6f6ef;
  --text-2: #c0e2d6;
  --muted: #93c2b4;
  --dot-off: #5f8f80;
  --brand: #2ec49b;

  --el-bg-color: rgba(216, 244, 235, 0.07);
  --el-bg-color-overlay: rgba(9, 30, 25, 0.96);
  --el-dialog-bg-color: rgba(9, 30, 25, 0.96);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(46, 196, 155, 0.16);
  --el-border-color-light: rgba(46, 196, 155, 0.12);
  --el-border-color-lighter: rgba(46, 196, 155, 0.09);
  --el-text-color-primary: #e6f6ef;
  --el-text-color-regular: #c0e2d6;
  --el-text-color-secondary: #93c2b4;
  --el-text-color-placeholder: #5f8f80;
  --el-fill-color-light: rgba(216, 244, 235, 0.07);
  --el-fill-color-lighter: rgba(216, 244, 235, 0.05);
  --el-fill-color: rgba(216, 244, 235, 0.09);
  --el-fill-color-dark: rgba(216, 244, 235, 0.10);
  --el-fill-color-blank: rgba(216, 244, 235, 0.05);
  --el-table-border-color: rgba(46, 196, 155, 0.18);
  --el-mask-color: rgba(4, 16, 13, 0.6);

  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(216, 244, 235, 0.10);
  --el-table-header-text-color: #c0e2d6;
  --el-table-row-hover-bg-color: rgba(216, 244, 235, 0.08);
  --el-descriptions-item-bordered-label-background: rgba(216, 244, 235, 0.08);
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  --el-tag-bg-color: rgba(216, 244, 235, 0.10);
  --el-tag-border-color: rgba(46, 196, 155, 0.20);
  --el-tag-text-color: #c0e2d6;

  /* 品牌 #2ec49b，按钮/激活/胶囊全站跟随 */
  --el-color-primary: #2ec49b;
  --el-color-primary-light-3: #6dd6b9;
  --el-color-primary-light-5: #96e2cd;
  --el-color-primary-light-7: #c0ede1;
  --el-color-primary-light-8: #d5f3eb;
  --el-color-primary-light-9: #eaf9f5;
  --el-color-primary-dark-2: #259d7c;

  /* --fp-*：壁纸/弹层磨砂通用段取参（frost=深夜磨砂底 rgb、head=磨砂表头浅一档 rgb、
     glow/wall=body 壁纸四层渐变、dlg=弹层同构渐变（暗一档）、line=弹层描边品牌半透） */
  --fp-frost: 9, 30, 25;
  --fp-frost-head: 24, 54, 46;
  --fp-line: rgba(46, 196, 155, 0.25);
  --fp-line-soft: rgba(46, 196, 155, 0.2);
  --fp-glow-a: rgba(20, 118, 92, 0.55);
  --fp-glow-b: rgba(17, 104, 96, 0.5);
  --fp-glow-c: rgba(13, 82, 62, 0.55);
  --fp-wall-1: #04100d; --fp-wall-2: #07211a; --fp-wall-3: #0b2d22;
  --fp-dlg-glow-a: rgba(20, 118, 92, 0.34);
  --fp-dlg-glow-b: rgba(17, 104, 96, 0.31);
  --fp-dlg-glow-c: rgba(13, 82, 62, 0.34);
  --fp-dlg-1: rgba(4, 16, 13, 0.95); --fp-dlg-2: rgba(7, 33, 26, 0.95); --fp-dlg-3: rgba(11, 45, 34, 0.95);
}

/* 绯夜 rose：壁纸深酒红系 + 品牌 #e0566f */
html.dark.rose {
  --bg: rgba(252, 232, 238, 0.04);
  --surface: rgba(252, 232, 238, 0.07);
  --border: rgba(224, 86, 111, 0.18);
  --ink: #f8eaee;
  --text-2: #e6cbd3;
  --muted: #c79daa;
  --dot-off: #8f6570;
  --brand: #e0566f;

  --el-bg-color: rgba(252, 232, 238, 0.07);
  --el-bg-color-overlay: rgba(31, 11, 18, 0.96);
  --el-dialog-bg-color: rgba(31, 11, 18, 0.96);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(224, 86, 111, 0.16);
  --el-border-color-light: rgba(224, 86, 111, 0.12);
  --el-border-color-lighter: rgba(224, 86, 111, 0.09);
  --el-text-color-primary: #f8eaee;
  --el-text-color-regular: #e6cbd3;
  --el-text-color-secondary: #c79daa;
  --el-text-color-placeholder: #8f6570;
  --el-fill-color-light: rgba(252, 232, 238, 0.07);
  --el-fill-color-lighter: rgba(252, 232, 238, 0.05);
  --el-fill-color: rgba(252, 232, 238, 0.09);
  --el-fill-color-dark: rgba(252, 232, 238, 0.10);
  --el-fill-color-blank: rgba(252, 232, 238, 0.05);
  --el-table-border-color: rgba(224, 86, 111, 0.18);
  --el-mask-color: rgba(17, 6, 9, 0.6);

  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(252, 232, 238, 0.10);
  --el-table-header-text-color: #e6cbd3;
  --el-table-row-hover-bg-color: rgba(252, 232, 238, 0.08);
  --el-descriptions-item-bordered-label-background: rgba(252, 232, 238, 0.08);
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  --el-tag-bg-color: rgba(252, 232, 238, 0.10);
  --el-tag-border-color: rgba(224, 86, 111, 0.20);
  --el-tag-text-color: #e6cbd3;

  /* 品牌 #e0566f，按钮/激活/胶囊全站跟随 */
  --el-color-primary: #e0566f;
  --el-color-primary-light-3: #e9899a;
  --el-color-primary-light-5: #f0aab7;
  --el-color-primary-light-7: #f6ccd4;
  --el-color-primary-light-8: #f9dde2;
  --el-color-primary-light-9: #fceef1;
  --el-color-primary-dark-2: #b34559;

  /* --fp-*：壁纸/弹层磨砂通用段取参（frost=深夜磨砂底 rgb、head=磨砂表头浅一档 rgb、
     glow/wall=body 壁纸四层渐变、dlg=弹层同构渐变（暗一档）、line=弹层描边品牌半透） */
  --fp-frost: 31, 11, 18;
  --fp-frost-head: 48, 28, 34;
  --fp-line: rgba(224, 86, 111, 0.25);
  --fp-line-soft: rgba(224, 86, 111, 0.2);
  --fp-glow-a: rgba(122, 32, 58, 0.55);
  --fp-glow-b: rgba(134, 38, 66, 0.5);
  --fp-glow-c: rgba(92, 26, 46, 0.55);
  --fp-wall-1: #150810; --fp-wall-2: #230d16; --fp-wall-3: #2f121a;
  --fp-dlg-glow-a: rgba(122, 32, 58, 0.34);
  --fp-dlg-glow-b: rgba(134, 38, 66, 0.31);
  --fp-dlg-glow-c: rgba(92, 26, 46, 0.34);
  --fp-dlg-1: rgba(21, 8, 16, 0.95); --fp-dlg-2: rgba(35, 13, 22, 0.95); --fp-dlg-3: rgba(47, 18, 26, 0.95);
}

/* 冰川 glacier：壁纸深海军蓝系 + 品牌 #56aae0 */
html.dark.glacier {
  --bg: rgba(224, 240, 250, 0.04);
  --surface: rgba(224, 240, 250, 0.07);
  --border: rgba(86, 170, 224, 0.18);
  --ink: #e9f3f9;
  --text-2: #c6dce9;
  --muted: #96b5c7;
  --dot-off: #64808f;
  --brand: #56aae0;

  --el-bg-color: rgba(224, 240, 250, 0.07);
  --el-bg-color-overlay: rgba(10, 22, 33, 0.96);
  --el-dialog-bg-color: rgba(10, 22, 33, 0.96);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(86, 170, 224, 0.16);
  --el-border-color-light: rgba(86, 170, 224, 0.12);
  --el-border-color-lighter: rgba(86, 170, 224, 0.09);
  --el-text-color-primary: #e9f3f9;
  --el-text-color-regular: #c6dce9;
  --el-text-color-secondary: #96b5c7;
  --el-text-color-placeholder: #64808f;
  --el-fill-color-light: rgba(224, 240, 250, 0.07);
  --el-fill-color-lighter: rgba(224, 240, 250, 0.05);
  --el-fill-color: rgba(224, 240, 250, 0.09);
  --el-fill-color-dark: rgba(224, 240, 250, 0.10);
  --el-fill-color-blank: rgba(224, 240, 250, 0.05);
  --el-table-border-color: rgba(86, 170, 224, 0.18);
  --el-mask-color: rgba(5, 12, 18, 0.6);

  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(224, 240, 250, 0.10);
  --el-table-header-text-color: #c6dce9;
  --el-table-row-hover-bg-color: rgba(224, 240, 250, 0.08);
  --el-descriptions-item-bordered-label-background: rgba(224, 240, 250, 0.08);
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  --el-tag-bg-color: rgba(224, 240, 250, 0.10);
  --el-tag-border-color: rgba(86, 170, 224, 0.20);
  --el-tag-text-color: #c6dce9;

  /* 品牌 #56aae0，按钮/激活/胶囊全站跟随 */
  --el-color-primary: #56aae0;
  --el-color-primary-light-3: #89c4e9;
  --el-color-primary-light-5: #aad4f0;
  --el-color-primary-light-7: #cce6f6;
  --el-color-primary-light-8: #ddeef9;
  --el-color-primary-light-9: #eef6fc;
  --el-color-primary-dark-2: #4588b3;

  /* --fp-*：壁纸/弹层磨砂通用段取参（frost=深夜磨砂底 rgb、head=磨砂表头浅一档 rgb、
     glow/wall=body 壁纸四层渐变、dlg=弹层同构渐变（暗一档）、line=弹层描边品牌半透） */
  --fp-frost: 10, 22, 33;
  --fp-frost-head: 27, 42, 52;
  --fp-line: rgba(86, 170, 224, 0.25);
  --fp-line-soft: rgba(86, 170, 224, 0.2);
  --fp-glow-a: rgba(30, 98, 150, 0.5);
  --fp-glow-b: rgba(26, 86, 124, 0.45);
  --fp-glow-c: rgba(19, 64, 98, 0.5);
  --fp-wall-1: #060b12; --fp-wall-2: #0a1520; --fp-wall-3: #0e1f2e;
  --fp-dlg-glow-a: rgba(30, 98, 150, 0.31);
  --fp-dlg-glow-b: rgba(26, 86, 124, 0.28);
  --fp-dlg-glow-c: rgba(19, 64, 98, 0.31);
  --fp-dlg-1: rgba(6, 11, 18, 0.95); --fp-dlg-2: rgba(10, 21, 32, 0.95); --fp-dlg-3: rgba(14, 31, 46, 0.95);
}

/* 熔岩 ember：壁纸炭黑烬橙系 + 品牌 #e8814f */
html.dark.ember {
  --bg: rgba(250, 236, 226, 0.04);
  --surface: rgba(250, 236, 226, 0.07);
  --border: rgba(232, 129, 79, 0.18);
  --ink: #f8efe9;
  --text-2: #ecd9cb;
  --muted: #c9a58f;
  --dot-off: #8f7663;
  --brand: #e8814f;

  --el-bg-color: rgba(250, 236, 226, 0.07);
  --el-bg-color-overlay: rgba(28, 19, 13, 0.96);
  --el-dialog-bg-color: rgba(28, 19, 13, 0.96);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(232, 129, 79, 0.16);
  --el-border-color-light: rgba(232, 129, 79, 0.12);
  --el-border-color-lighter: rgba(232, 129, 79, 0.09);
  --el-text-color-primary: #f8efe9;
  --el-text-color-regular: #ecd9cb;
  --el-text-color-secondary: #c9a58f;
  --el-text-color-placeholder: #8f7663;
  --el-fill-color-light: rgba(250, 236, 226, 0.07);
  --el-fill-color-lighter: rgba(250, 236, 226, 0.05);
  --el-fill-color: rgba(250, 236, 226, 0.09);
  --el-fill-color-dark: rgba(250, 236, 226, 0.10);
  --el-fill-color-blank: rgba(250, 236, 226, 0.05);
  --el-table-border-color: rgba(232, 129, 79, 0.18);
  --el-mask-color: rgba(15, 10, 7, 0.6);

  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(250, 236, 226, 0.10);
  --el-table-header-text-color: #ecd9cb;
  --el-table-row-hover-bg-color: rgba(250, 236, 226, 0.08);
  --el-descriptions-item-bordered-label-background: rgba(250, 236, 226, 0.08);
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  --el-tag-bg-color: rgba(250, 236, 226, 0.10);
  --el-tag-border-color: rgba(232, 129, 79, 0.20);
  --el-tag-text-color: #ecd9cb;

  /* 品牌 #e8814f，按钮/激活/胶囊全站跟随 */
  --el-color-primary: #e8814f;
  --el-color-primary-light-3: #efa784;
  --el-color-primary-light-5: #f4c0a7;
  --el-color-primary-light-7: #f8d9ca;
  --el-color-primary-light-8: #fae6dc;
  --el-color-primary-light-9: #fdf2ed;
  --el-color-primary-dark-2: #ba673f;

  /* --fp-*：壁纸/弹层磨砂通用段取参（frost=深夜磨砂底 rgb、head=磨砂表头浅一档 rgb、
     glow/wall=body 壁纸四层渐变、dlg=弹层同构渐变（暗一档）、line=弹层描边品牌半透） */
  --fp-frost: 28, 19, 13;
  --fp-frost-head: 46, 35, 27;
  --fp-line: rgba(232, 129, 79, 0.25);
  --fp-line-soft: rgba(232, 129, 79, 0.2);
  --fp-glow-a: rgba(142, 62, 24, 0.45);
  --fp-glow-b: rgba(122, 47, 20, 0.4);
  --fp-glow-c: rgba(92, 37, 16, 0.45);
  --fp-wall-1: #0d0c0b; --fp-wall-2: #131010; --fp-wall-3: #191411;
  --fp-dlg-glow-a: rgba(142, 62, 24, 0.28);
  --fp-dlg-glow-b: rgba(122, 47, 20, 0.25);
  --fp-dlg-glow-c: rgba(92, 37, 16, 0.28);
  --fp-dlg-1: rgba(13, 12, 11, 0.95); --fp-dlg-2: rgba(19, 16, 16, 0.95); --fp-dlg-3: rgba(25, 20, 17, 0.95);
}

/* 苔原 moss：壁纸深橄榄绿系 + 品牌 #94c356 */
html.dark.moss {
  --bg: rgba(234, 246, 220, 0.04);
  --surface: rgba(234, 246, 220, 0.07);
  --border: rgba(148, 195, 86, 0.18);
  --ink: #eef6e6;
  --text-2: #d4e5c3;
  --muted: #a8c090;
  --dot-off: #71855c;
  --brand: #94c356;

  --el-bg-color: rgba(234, 246, 220, 0.07);
  --el-bg-color-overlay: rgba(17, 23, 11, 0.96);
  --el-dialog-bg-color: rgba(17, 23, 11, 0.96);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(148, 195, 86, 0.16);
  --el-border-color-light: rgba(148, 195, 86, 0.12);
  --el-border-color-lighter: rgba(148, 195, 86, 0.09);
  --el-text-color-primary: #eef6e6;
  --el-text-color-regular: #d4e5c3;
  --el-text-color-secondary: #a8c090;
  --el-text-color-placeholder: #71855c;
  --el-fill-color-light: rgba(234, 246, 220, 0.07);
  --el-fill-color-lighter: rgba(234, 246, 220, 0.05);
  --el-fill-color: rgba(234, 246, 220, 0.09);
  --el-fill-color-dark: rgba(234, 246, 220, 0.10);
  --el-fill-color-blank: rgba(234, 246, 220, 0.05);
  --el-table-border-color: rgba(148, 195, 86, 0.18);
  --el-mask-color: rgba(9, 12, 6, 0.6);

  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(234, 246, 220, 0.10);
  --el-table-header-text-color: #d4e5c3;
  --el-table-row-hover-bg-color: rgba(234, 246, 220, 0.08);
  --el-descriptions-item-bordered-label-background: rgba(234, 246, 220, 0.08);
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  --el-tag-bg-color: rgba(234, 246, 220, 0.10);
  --el-tag-border-color: rgba(148, 195, 86, 0.20);
  --el-tag-text-color: #d4e5c3;

  /* 品牌 #94c356，按钮/激活/胶囊全站跟随 */
  --el-color-primary: #94c356;
  --el-color-primary-light-3: #b4d589;
  --el-color-primary-light-5: #cae1aa;
  --el-color-primary-light-7: #dfedcc;
  --el-color-primary-light-8: #eaf3dd;
  --el-color-primary-light-9: #f4f9ee;
  --el-color-primary-dark-2: #769c45;

  /* --fp-*：壁纸/弹层磨砂通用段取参（frost=深夜磨砂底 rgb、head=磨砂表头浅一档 rgb、
     glow/wall=body 壁纸四层渐变、dlg=弹层同构渐变（暗一档）、line=弹层描边品牌半透） */
  --fp-frost: 17, 23, 11;
  --fp-frost-head: 34, 40, 25;
  --fp-line: rgba(148, 195, 86, 0.25);
  --fp-line-soft: rgba(148, 195, 86, 0.2);
  --fp-glow-a: rgba(98, 140, 44, 0.45);
  --fp-glow-b: rgba(80, 118, 40, 0.4);
  --fp-glow-c: rgba(58, 92, 30, 0.45);
  --fp-wall-1: #0a0d06; --fp-wall-2: #10140a; --fp-wall-3: #161c0e;
  --fp-dlg-glow-a: rgba(98, 140, 44, 0.28);
  --fp-dlg-glow-b: rgba(80, 118, 40, 0.25);
  --fp-dlg-glow-c: rgba(58, 92, 30, 0.28);
  --fp-dlg-1: rgba(10, 13, 6, 0.95); --fp-dlg-2: rgba(16, 20, 10, 0.95); --fp-dlg-3: rgba(22, 28, 14, 0.95);
}

/* 石墨 graphite：壁纸中性炭灰系 + 品牌 #9db4c8 */
html.dark.graphite {
  --bg: rgba(235, 240, 245, 0.04);
  --surface: rgba(235, 240, 245, 0.07);
  --border: rgba(157, 180, 200, 0.18);
  --ink: #e9edf2;
  --text-2: #ccd5de;
  --muted: #9aa6b2;
  --dot-off: #6b7580;
  --brand: #9db4c8;

  --el-bg-color: rgba(235, 240, 245, 0.07);
  --el-bg-color-overlay: rgba(17, 20, 24, 0.96);
  --el-dialog-bg-color: rgba(17, 20, 24, 0.96);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(157, 180, 200, 0.16);
  --el-border-color-light: rgba(157, 180, 200, 0.12);
  --el-border-color-lighter: rgba(157, 180, 200, 0.09);
  --el-text-color-primary: #e9edf2;
  --el-text-color-regular: #ccd5de;
  --el-text-color-secondary: #9aa6b2;
  --el-text-color-placeholder: #6b7580;
  --el-fill-color-light: rgba(235, 240, 245, 0.07);
  --el-fill-color-lighter: rgba(235, 240, 245, 0.05);
  --el-fill-color: rgba(235, 240, 245, 0.09);
  --el-fill-color-dark: rgba(235, 240, 245, 0.10);
  --el-fill-color-blank: rgba(235, 240, 245, 0.05);
  --el-table-border-color: rgba(157, 180, 200, 0.18);
  --el-mask-color: rgba(9, 11, 13, 0.6);

  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(235, 240, 245, 0.10);
  --el-table-header-text-color: #ccd5de;
  --el-table-row-hover-bg-color: rgba(235, 240, 245, 0.08);
  --el-descriptions-item-bordered-label-background: rgba(235, 240, 245, 0.08);
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  --el-tag-bg-color: rgba(235, 240, 245, 0.10);
  --el-tag-border-color: rgba(157, 180, 200, 0.20);
  --el-tag-text-color: #ccd5de;

  /* 品牌 #9db4c8，按钮/激活/胶囊全站跟随 */
  --el-color-primary: #9db4c8;
  --el-color-primary-light-3: #bacad8;
  --el-color-primary-light-5: #cedae4;
  --el-color-primary-light-7: #e2e8ee;
  --el-color-primary-light-8: #ebf0f4;
  --el-color-primary-light-9: #f5f8fa;
  --el-color-primary-dark-2: #7e90a0;

  /* --fp-*：壁纸/弹层磨砂通用段取参（frost=深夜磨砂底 rgb、head=磨砂表头浅一档 rgb、
     glow/wall=body 壁纸四层渐变、dlg=弹层同构渐变（暗一档）、line=弹层描边品牌半透） */
  --fp-frost: 17, 20, 24;
  --fp-frost-head: 33, 37, 42;
  --fp-line: rgba(157, 180, 200, 0.25);
  --fp-line-soft: rgba(157, 180, 200, 0.2);
  --fp-glow-a: rgba(74, 96, 118, 0.35);
  --fp-glow-b: rgba(60, 78, 96, 0.32);
  --fp-glow-c: rgba(44, 58, 74, 0.35);
  --fp-wall-1: #0b0d0f; --fp-wall-2: #111417; --fp-wall-3: #16191d;
  --fp-dlg-glow-a: rgba(74, 96, 118, 0.22);
  --fp-dlg-glow-b: rgba(60, 78, 96, 0.2);
  --fp-dlg-glow-c: rgba(44, 58, 74, 0.22);
  --fp-dlg-1: rgba(11, 13, 15, 0.95); --fp-dlg-2: rgba(17, 20, 23, 0.95); --fp-dlg-3: rgba(22, 25, 29, 0.95);
}

/* 霓 neon：壁纸深茄紫系 + 品牌 #d95bb8 */
html.dark.neon {
  --bg: rgba(250, 232, 246, 0.04);
  --surface: rgba(250, 232, 246, 0.07);
  --border: rgba(217, 91, 184, 0.18);
  --ink: #f9e9f5;
  --text-2: #e8cce2;
  --muted: #c49bb8;
  --dot-off: #8a6280;
  --brand: #d95bb8;

  --el-bg-color: rgba(250, 232, 246, 0.07);
  --el-bg-color-overlay: rgba(26, 11, 26, 0.96);
  --el-dialog-bg-color: rgba(26, 11, 26, 0.96);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(217, 91, 184, 0.16);
  --el-border-color-light: rgba(217, 91, 184, 0.12);
  --el-border-color-lighter: rgba(217, 91, 184, 0.09);
  --el-text-color-primary: #f9e9f5;
  --el-text-color-regular: #e8cce2;
  --el-text-color-secondary: #c49bb8;
  --el-text-color-placeholder: #8a6280;
  --el-fill-color-light: rgba(250, 232, 246, 0.07);
  --el-fill-color-lighter: rgba(250, 232, 246, 0.05);
  --el-fill-color: rgba(250, 232, 246, 0.09);
  --el-fill-color-dark: rgba(250, 232, 246, 0.10);
  --el-fill-color-blank: rgba(250, 232, 246, 0.05);
  --el-table-border-color: rgba(217, 91, 184, 0.18);
  --el-mask-color: rgba(14, 6, 14, 0.6);

  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(250, 232, 246, 0.10);
  --el-table-header-text-color: #e8cce2;
  --el-table-row-hover-bg-color: rgba(250, 232, 246, 0.08);
  --el-descriptions-item-bordered-label-background: rgba(250, 232, 246, 0.08);
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  --el-tag-bg-color: rgba(250, 232, 246, 0.10);
  --el-tag-border-color: rgba(217, 91, 184, 0.20);
  --el-tag-text-color: #e8cce2;

  /* 品牌 #d95bb8，按钮/激活/胶囊全站跟随 */
  --el-color-primary: #d95bb8;
  --el-color-primary-light-3: #e48ccd;
  --el-color-primary-light-5: #ecaddc;
  --el-color-primary-light-7: #f4ceea;
  --el-color-primary-light-8: #f7def1;
  --el-color-primary-light-9: #fbeff8;
  --el-color-primary-dark-2: #ae4993;

  /* --fp-*：壁纸/弹层磨砂通用段取参（frost=深夜磨砂底 rgb、head=磨砂表头浅一档 rgb、
     glow/wall=body 壁纸四层渐变、dlg=弹层同构渐变（暗一档）、line=弹层描边品牌半透） */
  --fp-frost: 26, 11, 26;
  --fp-frost-head: 44, 26, 44;
  --fp-line: rgba(217, 91, 184, 0.25);
  --fp-line-soft: rgba(217, 91, 184, 0.2);
  --fp-glow-a: rgba(130, 44, 118, 0.5);
  --fp-glow-b: rgba(110, 36, 102, 0.45);
  --fp-glow-c: rgba(82, 28, 78, 0.5);
  --fp-wall-1: #110811; --fp-wall-2: #1a0c1a; --fp-wall-3: #241124;
  --fp-dlg-glow-a: rgba(130, 44, 118, 0.31);
  --fp-dlg-glow-b: rgba(110, 36, 102, 0.28);
  --fp-dlg-glow-c: rgba(82, 28, 78, 0.31);
  --fp-dlg-1: rgba(17, 8, 17, 0.95); --fp-dlg-2: rgba(26, 12, 26, 0.95); --fp-dlg-3: rgba(36, 17, 36, 0.95);
}

/* 深海 abyss：壁纸近黑深渊蓝青系 + 品牌 #3f8fa3 */
html.dark.abyss {
  --bg: rgba(222, 238, 243, 0.04);
  --surface: rgba(222, 238, 243, 0.07);
  --border: rgba(63, 143, 163, 0.18);
  --ink: #e7eef2;
  --text-2: #c3d5dd;
  --muted: #8ea6b1;
  --dot-off: #5c7079;
  --brand: #3f8fa3;

  --el-bg-color: rgba(222, 238, 243, 0.07);
  --el-bg-color-overlay: rgba(7, 14, 19, 0.96);
  --el-dialog-bg-color: rgba(7, 14, 19, 0.96);
  --el-bg-color-page: transparent;
  --el-border-color: rgba(63, 143, 163, 0.16);
  --el-border-color-light: rgba(63, 143, 163, 0.12);
  --el-border-color-lighter: rgba(63, 143, 163, 0.09);
  --el-text-color-primary: #e7eef2;
  --el-text-color-regular: #c3d5dd;
  --el-text-color-secondary: #8ea6b1;
  --el-text-color-placeholder: #5c7079;
  --el-fill-color-light: rgba(222, 238, 243, 0.07);
  --el-fill-color-lighter: rgba(222, 238, 243, 0.05);
  --el-fill-color: rgba(222, 238, 243, 0.09);
  --el-fill-color-dark: rgba(222, 238, 243, 0.10);
  --el-fill-color-blank: rgba(222, 238, 243, 0.05);
  --el-table-border-color: rgba(63, 143, 163, 0.18);
  --el-mask-color: rgba(3, 7, 10, 0.6);

  --el-table-tr-bg-color: transparent;
  --el-table-header-bg-color: rgba(222, 238, 243, 0.10);
  --el-table-header-text-color: #c3d5dd;
  --el-table-row-hover-bg-color: rgba(222, 238, 243, 0.08);
  --el-descriptions-item-bordered-label-background: rgba(222, 238, 243, 0.08);
  --el-card-bg-color: var(--surface);
  --el-card-border-radius: 8px;

  --el-tag-bg-color: rgba(222, 238, 243, 0.10);
  --el-tag-border-color: rgba(63, 143, 163, 0.20);
  --el-tag-text-color: #c3d5dd;

  /* 品牌 #3f8fa3，按钮/激活/胶囊全站跟随 */
  --el-color-primary: #3f8fa3;
  --el-color-primary-light-3: #79b1bf;
  --el-color-primary-light-5: #9fc7d1;
  --el-color-primary-light-7: #c5dde3;
  --el-color-primary-light-8: #d9e9ed;
  --el-color-primary-light-9: #ecf4f6;
  --el-color-primary-dark-2: #327282;

  /* --fp-*：壁纸/弹层磨砂通用段取参（frost=深夜磨砂底 rgb、head=磨砂表头浅一档 rgb、
     glow/wall=body 壁纸四层渐变、dlg=弹层同构渐变（暗一档）、line=弹层描边品牌半透） */
  --fp-frost: 7, 14, 19;
  --fp-frost-head: 22, 31, 37;
  --fp-line: rgba(63, 143, 163, 0.25);
  --fp-line-soft: rgba(63, 143, 163, 0.2);
  --fp-glow-a: rgba(24, 62, 86, 0.38);
  --fp-glow-b: rgba(18, 54, 64, 0.34);
  --fp-glow-c: rgba(14, 42, 58, 0.4);
  --fp-wall-1: #05070a; --fp-wall-2: #070d12; --fp-wall-3: #0a1218;
  --fp-dlg-glow-a: rgba(24, 62, 86, 0.24);
  --fp-dlg-glow-b: rgba(18, 54, 64, 0.21);
  --fp-dlg-glow-c: rgba(14, 42, 58, 0.25);
  --fp-dlg-1: rgba(5, 7, 10, 0.95); --fp-dlg-2: rgba(7, 13, 18, 0.95); --fp-dlg-3: rgba(10, 18, 24, 0.95);
}

/* 八主题：页面底 = 各自壁纸渐变（fixed 不随滚动），面板玻璃透出 */
html.dark.teal body,
html.dark.rose body,
html.dark.glacier body,
html.dark.ember body,
html.dark.moss body,
html.dark.graphite body,
html.dark.neon body,
html.dark.abyss body {
  background:
    radial-gradient(120% 90% at 88% 8%, var(--fp-glow-a) 0%, transparent 55%),
    radial-gradient(130% 100% at 10% 92%, var(--fp-glow-b) 0%, transparent 58%),
    radial-gradient(150% 120% at 55% 125%, var(--fp-glow-c) 0%, transparent 65%),
    linear-gradient(160deg, var(--fp-wall-1) 0%, var(--fp-wall-2) 45%, var(--fp-wall-3) 100%);
  background-attachment: fixed;
}

/* 八主题：弹层深夜磨砂（同 gold 弹层四层渐变结构，色值走 --fp-* 变量） */
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-dialog,
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-drawer,
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-message-box {
  background:
    radial-gradient(120% 90% at 88% 8%, var(--fp-dlg-glow-a) 0%, transparent 55%),
    radial-gradient(130% 100% at 10% 92%, var(--fp-dlg-glow-b) 0%, transparent 58%),
    radial-gradient(150% 120% at 55% 125%, var(--fp-dlg-glow-c) 0%, transparent 65%),
    linear-gradient(160deg, var(--fp-dlg-1) 0%, var(--fp-dlg-2) 45%, var(--fp-dlg-3) 100%);
  backdrop-filter: blur(18px) saturate(1.3);
  -webkit-backdrop-filter: blur(18px) saturate(1.3);
  border: 1px solid var(--fp-line);
  border-radius: 12px;
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.55), inset 0 0 0 1px rgba(255, 255, 255, 0.06);
}

/* 八主题：dark tooltip 深夜实底（is-dark 底色取 EP 变量会白透叠白透看不清字） */
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-popper.is-dark {
  background: rgba(var(--fp-frost), 0.97);
  color: var(--ink);
  border: 1px solid var(--fp-line-soft);
}
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-popper.is-dark .el-popper__arrow::before {
  background: rgba(var(--fp-frost), 0.97);
  border-color: var(--fp-line-soft);
}

/* 八主题 wangeditor 弹出面板（同 gold 口径，变量驱动版） */
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .w-e-drop-panel,
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .w-e-select-list,
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .w-e-modal,
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .w-e-toolbar {
  --w-e-toolbar-bg-color: rgba(var(--fp-frost), 0.97);
  --w-e-toolbar-color: var(--ink);
  --w-e-toolbar-border-color: var(--fp-line-soft);
  --w-e-toolbar-active-bg-color: rgba(255, 255, 255, 0.12);
  --w-e-toolbar-active-color: #ffffff;
  --w-e-toolbar-disabled-color: var(--el-text-color-placeholder);
  --w-e-modal-button-bg-color: rgba(255, 255, 255, 0.1);
  --w-e-modal-button-border-color: var(--fp-line-soft);
  --w-e-textarea-bg-color: rgba(255, 255, 255, 0.08);
  --w-e-textarea-border-color: var(--fp-line);
  --w-e-textarea-color: var(--ink);
  background: rgba(var(--fp-frost), 0.97);
  color: var(--ink);
}

/* 石墨品牌为浅银灰（#9db4c8），EP 主按钮白字对比不足：字压深。
   49 收窄为仅实心（:not 排除 plain/link/text）——原规则无差别改 color，
   plain 按钮文字也被压成深字落在深玻璃上（反向看不清） */
html.dark.graphite .el-button--primary:not(.is-plain):not(.is-link):not(.is-text) {
  color: #131a21;
}

/* 49 十主题按钮对比度统一（用户报部分主题按钮看不清）：十套暗主题的
   light-* 阶梯是近白粉彩（为浅色悬停底设计），EP 按钮按「暗色主题
   light-* = 深色 mix」的规范消费，两条口径相撞塌对比——
   ① 禁用/loading 态：EP 把禁用底设为 light-5（近白）、字留白 → 彻底看不见。
      改禁用底=按钮自身品牌底色（--el-button-bg-color 已被 EP 按类型指向
      primary/danger 等基色）、字 72% 白（比常态明显发虚但仍可辨）；默认型
      按钮底本就是深 fill，仅字变亮=顺带把「灰得看不见」的禁用态修亮。
      plain 的底/字另有更高优先级规则接管，不受此段影响 */
:is(html.dark.violet, html.dark.gold, html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-button.is-disabled {
  --el-button-disabled-bg-color: var(--el-button-bg-color);
  --el-button-disabled-border-color: var(--el-button-bg-color);
  --el-button-disabled-text-color: rgba(255, 255, 255, 0.72);
}

/* ② 品牌过亮的主题（暗金 #d4af37 / 苔原 #94c356）实心 primary 白字对比不足
  （~2.1:1），随石墨口径字压深；直接改 color 优先级压过 EP 悬停/禁用的
  变量链，各状态下深字都落在品牌底上 */
html.dark.gold .el-button--primary:not(.is-plain):not(.is-link):not(.is-text) {
  color: #2b230b;
}
html.dark.moss .el-button--primary:not(.is-plain):not(.is-link):not(.is-text) {
  color: #20290f;
}

/* ③ 深海品牌基色 #3f8fa3 深底上偏暗（~4.3:1），plain primary 文字提亮一档
  （仅动文字，主题色板/标签/激活态等仍用基色） */
html.dark.abyss .el-button--primary.is-plain {
  color: #82cade;
}

/* 八主题：表格固定列近实色深夜（延续 violet 342 防透字/性能口径），表头浅一档 */
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-table .el-table-fixed-column--left,
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-table .el-table-fixed-column--right {
  background: rgba(var(--fp-frost), 0.97);
}
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-table .el-table__header-wrapper .el-table-fixed-column--left,
:is(html.dark.teal, html.dark.rose, html.dark.glacier, html.dark.ember, html.dark.moss, html.dark.graphite, html.dark.neon, html.dark.abyss) .el-table .el-table__header-wrapper .el-table-fixed-column--right {
  background: rgba(var(--fp-frost-head), 0.97);
}

/* 服务器图标（251）：所有服务器名称前的可选小图标，加载失败自动隐藏 */
.srv-ico {
  width: 16px;
  height: 16px;
  border-radius: 4px;
  object-fit: cover;
  flex: none;
  vertical-align: -3px;
  margin-right: 4px;
  background: #fff;
}

/* 全局 tooltip（图标悬停提示、表格溢出提示等所有 el-tooltip）：
   字号 14px（默认 12px 大两号），内边距/圆角/箭头略紧凑；
   只命中 .el-popper.el-tooltip，不影响下拉菜单/选择框弹层 */
.el-popper.el-tooltip {
  font-size: 14px;
  line-height: 20px;
  padding: 5px 10px;
  min-width: 10px; /* 覆盖 .el-tooltip 的 50px 最小宽，短文案不再撑宽 */
  --el-popper-border-radius: 3px;
}
.el-popper.el-tooltip .el-popper__arrow {
  width: 8px;
  height: 8px;
}
.el-popper.el-tooltip[data-popper-placement^='top'] > .el-popper__arrow {
  bottom: -4px;
}
.el-popper.el-tooltip[data-popper-placement^='bottom'] > .el-popper__arrow {
  top: -4px;
}
.el-popper.el-tooltip[data-popper-placement^='left'] > .el-popper__arrow {
  right: -4px;
}
.el-popper.el-tooltip[data-popper-placement^='right'] > .el-popper__arrow {
  left: -4px;
}

/* 描述列表（系统信息等 bordered 表格）统一圆角：外层裁切出圆角 */
.el-descriptions__body {
  border-radius: 10px;
  overflow: hidden;
}

/* 利用率阈值分档配色（utils/format.js 的 utilClass 输出）：≥70 黄、≥90 红 */
.u-warn {
  color: var(--warn-text);
  font-weight: 600;
}
.u-crit {
  color: var(--crit);
  font-weight: 600;
}

/* 左右分栏分隔条（文档/终端/文件管理共用，逻辑在 useSidePanel）：
   占位 8px 即两卡片间距（替代原 10px gap）；悬停/拖拽高亮，中间竖线是把手，
   单击收起或展开侧栏。手机端上下堆叠布局，分隔条隐藏 */
.side-divider {
  flex: none;
  width: 8px;
  border-radius: 4px;
  cursor: col-resize;
  position: relative;
  transition: background 0.15s;
}
.side-divider::before {
  content: '';
  position: absolute;
  top: 50%;
  left: 50%;
  width: 3px;
  height: 32px;
  transform: translate(-50%, -50%);
  border-radius: 2px;
  background: var(--border);
  transition: background 0.15s;
}
.side-divider:hover,
.side-divider.on {
  background: rgba(61, 139, 221, 0.16);
}
.side-divider:hover::before,
.side-divider.on::before {
  background: var(--brand);
}
@media (max-width: 768px) {
  .side-divider {
    display: none;
  }
}

/* 手机宽度兼容：弹窗/确认框不超出屏幕。
   注意 message-box 本体是 width:100% + max-width 限宽，这里必须带上
   420px（Element Plus 默认值）——只写 92vw 会覆盖默认上限，桌面端确认框撑满屏 */
.el-dialog {
  max-width: 92vw;
}
.el-message-box {
  max-width: min(420px, 92vw);
}

/* 全局提示（ElMessage）下移：顶栏高 56px，EP 默认 top 20px 会压在顶栏上。
   不动 EP 的内联 top（多条提示的堆叠偏移是它自己算的），用 margin-top
   整体平移：20px + 48px = 68px，落在顶栏下方 12px 处，堆叠逻辑不变 */
.el-message {
  margin-top: 48px;
}

/* 手机端输入字号 ≥16px，避免 iOS 聚焦时自动放大页面 */
@media (max-width: 768px) {
  .el-input__inner,
  .el-textarea__inner,
  .el-select__input,
  .el-select__wrapper {
    font-size: 16px !important;
  }
  .el-message {
    max-width: 92vw;
  }
}

/* 深色玻璃独立页（主页 .pv / 智能家居 .ha）玻璃钮点击持焦后，UA 默认焦点描边
   在深底上呈黑色粗环（538 主页天气胶囊报「黑边」实测即此，17 HA 菜单钮同症）
   ——两页玻璃钮一律抑制；触/鼠为主的独立页，可点性由 hover 态承载 */
.pv button:focus,
.pv button:focus-visible,
.ha button:focus,
.ha button:focus-visible {
  outline: 0;
}

/* el-tabs 激活项点击持焦后的 EP 焦点环（:focus-visible 内阴影蓝框，29 服务
   监控页「证书监控 tab 有蓝边」同 17 号一族）——全站 tabs 一律抑制，当前项
   的可点性由激活态高亮（下划线+主色文字）承载 */
.el-tabs__item:focus-visible {
  box-shadow: none;
}

/* 下拉菜单项悬停/选中（41 用户指定）：与左侧菜单选中态同款（38 口径）——
   品牌主色 15% 软底 + 40% 内环 + 药丸化，颜色随各主题 --el-color-primary，
   浅/暗/紫/暗金全部主题一份样式（用户菜单/行操作下拉等所有 el-dropdown 通用） */
.el-dropdown-menu .el-dropdown-menu__item {
  margin: 3px 8px;
  border-radius: 8px;
}
.el-dropdown-menu .el-dropdown-menu__item:not(.is-disabled):hover,
.el-dropdown-menu .el-dropdown-menu__item:not(.is-disabled):focus {
  background: color-mix(in srgb, var(--el-color-primary) 15%, transparent);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--el-color-primary) 40%, transparent);
  color: var(--el-color-primary);
}
</style>
