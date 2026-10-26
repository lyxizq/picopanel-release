<template>
  <div v-loading="loading" class="code-editor">
    <div v-if="dead" class="ce-dead">{{ $t('docedit.editor.initFailed') }}</div>
    <div v-else ref="host" class="ce-host"></div>
  </div>
</template>

<script setup>
// Monaco 代码编辑器（文件管理在线编辑用）：语法高亮 / JSON 错误标记 /
// 格式化 / Ctrl+F 查找。随调用方懒加载，独占一个 chunk（本体+worker 数 MB），
// 不拖累主包。明暗主题跟随 <html class="dark">（创建时定格，编辑中途切主题
// 少见，不做实时切换）。
defineOptions({ name: 'CodeEditor' })
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as monaco from 'monaco-editor'
// worker 入口用相对路径引：包 exports map 对带 ?worker 查询的裸导入解析失败。
// 只带 json worker（399KB，JSON 校验/格式化）：ts/css/html 三个语言服务 worker
// 合计 8.4MB，是部署包体积大头，砍掉——这些语言高亮不受影响（Monarch 分词器
// 在编辑器核心内，随主 chunk 走），只失去智能提示与自动格式化，控制台编辑
// 场景（配置/SQL/脚本）用不到；对应语言 getWorker 走缺省 editorWorker。
import editorWorker from '../../node_modules/monaco-editor/esm/vs/editor/editor.worker.js?worker'
import jsonWorker from '../../node_modules/monaco-editor/esm/vs/language/json/json.worker.js?worker'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/docedit.js'

registerPageMessages(pageMsgs)

self.MonacoEnvironment = {
  getWorker(_id, label) {
    if (label === 'json') return new jsonWorker()
    return new editorWorker()
  },
}

const props = defineProps({
  modelValue: { type: String, default: '' },
  language: { type: String, default: 'plaintext' },
  loading: Boolean,
  readOnly: Boolean,
})
// ready：编辑器创建完成后透出 monaco/实例，供调用方注册语言级扩展
//（如 DbView 的 SQL 补全 provider）——monaco 是单例，主包不因此多打包
const emit = defineEmits(['update:modelValue', 'ready'])

const host = ref(null)
const dead = ref(false)
let ed = null

onMounted(() => {
  // 初始化失败（极端环境/移动端兼容问题）不静默白屏，给出可见提示
  try {
    ed = monaco.editor.create(host.value, {
      value: props.modelValue || '',
      language: props.language,
      theme: document.documentElement.classList.contains('dark') ? 'vs-dark' : 'vs',
      automaticLayout: true,
      fontSize: 13,
      minimap: { enabled: false },
      wordWrap: 'on',
      renderWhitespace: 'boundary',
      scrollBeyondLastLine: false,
      readOnly: props.readOnly,
    })
    ed.onDidChangeModelContent(() => emit('update:modelValue', ed.getValue()))
    emit('ready', { ed, monaco })
    // 弹窗过渡动画期间容器尺寸为 0，automaticLayout 的 ResizeObserver 偶发
    // 收不到终态通知（移动端 Safari 尤甚）——挂载后补一脚 layout 兜底
    nextTick(() => ed?.layout())
  } catch (e) {
    dead.value = true
    console.error('Monaco 初始化失败:', e)
  }
})

watch(
  () => props.readOnly,
  (ro) => {
    ed?.updateOptions({ readOnly: !!ro })
  },
)

watch(
  () => props.language,
  (l) => {
    if (ed) monaco.editor.setModelLanguage(ed.getModel(), l || 'plaintext')
  },
)

// 外部整体替换内容（打开新文件/载入）时 setValue 重建 undo 栈；编辑产生的
// 回环（emit 后同值）天然短路
watch(
  () => props.modelValue,
  (v) => {
    if (ed && typeof v === 'string' && v !== ed.getValue()) ed.setValue(v)
  },
)

onBeforeUnmount(() => {
  ed?.dispose?.()
  ed = null
})

function format() {
  ed?.getAction('editor.action.formatDocument')?.run()
}

function find() {
  ed?.getAction('actions.find')?.run()
}

defineExpose({ format, find })
</script>

<style scoped>
.code-editor {
  height: 100%;
}
.ce-host {
  height: 100%;
  min-height: 320px;
  border: 1px solid var(--border, #dcdfe6);
  border-radius: 6px;
  overflow: hidden;
}
.ce-dead {
  height: 100%;
  min-height: 320px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--crit, #f56c6c);
  font-size: 13px;
}
</style>
