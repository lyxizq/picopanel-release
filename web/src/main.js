import { createApp } from 'vue'
import * as Icons from '@element-plus/icons-vue'
import { ElLoadingDirective } from 'element-plus'
// Element Plus 按需引入（vite.config.js 里 unplugin 解析 <el-*>）：全量 CSS 不再
// 打进入口。服务式 API（ElMessage/ElMessageBox/ElNotification）的样式不走模板
// 解析，这里全局补齐（均为小文件）；v-loading 指令生产构建下解析器不注入、
// 失效是静默的，显式全局注册（v-infinite-scroll/v-popover 全站未用）：
import 'element-plus/es/components/message/style/css'
import 'element-plus/es/components/message-box/style/css'
import 'element-plus/es/components/notification/style/css'
import 'element-plus/es/components/loading/style/css'
import 'element-plus/theme-chalk/dark/css-vars.css'
// 主页弹层全局样式（天气气泡/引擎下拉，popper 挂 body）：须常驻——写在 PageView
// 非 scoped 块里会随主页 chunk 懒加载，直接进智能家居页时弹层样式缺失（12）
import './styles/poppers.css'
import App from './App.vue'
import router from './router'
import { initTheme } from './utils/theme.js'

initTheme() // 挂载前应用 <html class="dark">（index.html 内联脚本已先行设置，避免闪色）

const app = createApp(App)
app.directive('loading', ElLoadingDirective)
// EP 图标全量全局注册：模板大量 <component :is="'Suitcase'"> 字符串形态依赖它，
// 包体小（几十 KB），不值得为按需引入冒静默丢图标的风险
for (const [name, comp] of Object.entries(Icons)) {
  app.component(name, comp)
}
app.use(router)
app.mount('#app')

// Service Worker（Web Push 推送用）：仅在 HTTPS/localhost 安全上下文注册；
// 未支持的环境静默跳过，设置页 Web Push 卡片会给出说明。
if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(() => {})
}
