import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import AutoImport from 'unplugin-auto-import/vite'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'

// 部署包精简（TODO 21）：monaco 根入口固定携带 ts/css/html 三个语言服务
// （features/*/register.js，连 worker 合计 dist 8.4MB+，纯为这三种语法的智能
// 提示/格式化存在，控制台编辑场景用不到）与站内无人使用的 monaco-lsp-client
// （2.7MB 源码，`monaco.lsp` 导出零引用）。这里在打包期把它们替成空桩：
// 语法高亮走 definitions/* 的 Monarch 分词器不受影响，JSON 服务保留，
// getWorker 对应语言回落 editorWorker（见 CodeEditor.vue）。
const MONACO_STUB =
  /monaco-editor[\\/]esm[\\/](?:vs[\\/]languages[\\/]features[\\/](?:css|html|typescript)[\\/]register\.js|external[\\/]monaco-lsp-client[\\/]out[\\/]index\.js)$/
const monacoSlim = {
  name: 'monaco-slim',
  enforce: 'pre',
  async resolveId(id, importer, opts) {
    if (!importer) return null
    const r = await this.resolve(id, importer, { ...opts, skipSelf: true })
    return r && MONACO_STUB.test(r.id) ? '\0virtual:monaco-slim-stub' : null
  },
  load(id) {
    return id === '\0virtual:monaco-slim-stub' ? 'export default {}' : null
  },
}

// 构建产物直接输出到服务端 embed 目录，`go build` 后即为单二进制部署。
export default defineConfig({
  plugins: [
    monacoSlim,
    vue(),
    // Element Plus 按需引入（2026-10-06 性能批次）：模板里的 <el-*> 编译期
    // 自动带上组件与样式 import，入口不再全量打包（约省一半首屏 JS）。
    // 注意配套约定：
    //  - ElMessage/ElMessageBox 等服务式 API 走显式 import（各文件现状），
    //    其样式在 main.js 全局补（message/message-box/notification/loading）；
    //  - EP 图标仍全量全局注册（icons-vue 包小，模板里 <component :is="'Suitcase'">
    //    字符串形态依赖全局注册，按需会静默丢图标）；
    //  - locale 由 App.vue 的 <el-config-provider> 提供（原 app.use(ElementPlus) 移除）。
    AutoImport({ resolvers: [ElementPlusResolver()] }),
    Components({ resolvers: [ElementPlusResolver()] }),
  ],
  server: {
    port: 5173,
    // 开发模式代理到本机服务端（WS 终端需要 ws:true，否则 /term/ws 升级失败）
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', ws: true },
    },
  },
  build: {
    outDir: '../internal/server/ui/dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // 只手动拆「入口静态引用」的大库（缓存友好：业务改动不再失效整个大包）。
        // 异步专用重库（monaco/echarts/vditor/wangeditor）交给 Rollup 自动拆：
        // 手动归组会把 Vite 的 preload helper 一起塞进该 chunk，所有异步 chunk
        // 反过来静态依赖它，入口就会预载 4.6MB 的 monaco（本批次实测踩坑）。
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (id.includes('monaco-editor') || id.includes('echarts') || id.includes('zrender')
            || id.includes('vditor') || id.includes('wangeditor') || id.includes('xterm')) return undefined
          if (id.includes('element-plus') || id.includes('@element-plus')) return 'element-plus'
          if (id.includes('@vue') || id.includes('/vue/') || id.includes('vue-router')) return 'vue'
          return 'vendor' // 其余均为 element-plus 的静态依赖（dayjs/popper/lodash-es 等）
        },
      },
    },
  },
})
