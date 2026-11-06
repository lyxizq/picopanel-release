<template>
  <div>
    <!-- 告警页：历史 + 规则两个内嵌 tab，默认展示告警历史；
         el-tab-pane 切换为显隐不销毁，两边的筛选/表单状态互不丢失 -->
    <el-tabs v-model="tab" class="alert-tabs">
      <el-tab-pane :label="$t('alertHistory.title')" name="history">
        <AlertHistoryPanel />
      </el-tab-pane>
      <el-tab-pane :label="$t('alertRules.title')" name="rules" lazy>
        <AlertRulesPanel />
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup>
// keep-alive 按组件名缓存（tabs.js 的 cachedNames 引用）。
// 组件名沿用 AlertRulesView：老用户 localStorage 恢复的 tab（按 name 匹配）不失配。
defineOptions({ name: 'AlertRulesView' })
import { ref } from 'vue'
import { useRoute } from 'vue-router'
import AlertHistoryPanel from '../components/AlertHistoryPanel.vue'
import AlertRulesPanel from '../components/AlertRulesPanel.vue'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/messages.js'

registerPageMessages(pageMsgs)

// 默认展示告警历史；?tab=rules 直达规则 tab（旧地址 /alerts 的 redirect 会透传 query，
// 其中 status=firing 由历史面板读取作初始筛选）
const route = useRoute()
const tab = ref(route.query.tab === 'rules' ? 'rules' : 'history')
</script>

<style scoped>
.alert-tabs :deep(.el-tabs__header) {
  margin-bottom: 10px;
}
</style>
