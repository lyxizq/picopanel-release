// 服务器明细弹层的全局状态（模块级 reactive，同 tabs.js 模式，项目无 pinia）：
// 任意页面 import openServerDetail(id) 打开明细；弹层单例挂载在 Layout。
import { reactive } from 'vue'

export const serverDetail = reactive({ id: 0, open: false })

export function openServerDetail(id) {
  serverDetail.id = id
  serverDetail.open = true
}
