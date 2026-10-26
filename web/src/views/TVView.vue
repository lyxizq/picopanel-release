<template>
  <div class="tv-tabs">
    <el-tabs v-model="pageTab" class="tv-tabs-bar" @tab-change="onPageTab">
      <el-tab-pane :label="$t('tv.tabTv')" name="tv" lazy>
        <TvFollowPane />
      </el-tab-pane>
      <el-tab-pane :label="$t('tv.tabBili')" name="bili" lazy>
        <BiliFollowView />
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import TvFollowPane from './TvFollowPane.vue'
import BiliFollowView from './BiliFollowView.vue'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/tv.js'

registerPageMessages(pageMsgs)

// 追剧页 tab 容器（TODO 5）：追剧 | 追更（B 站追更）。子组件不在 keep-alive
// include 名单内，首载靠各自 onMounted（ServiceMonView 容器同口径）；?tab= 可书签。
const route = useRoute()
const router = useRouter()
const pageTab = ref(route.query.tab === 'bili' ? 'bili' : 'tv')

function onPageTab(name) {
  router.replace({ query: name === 'bili' ? { tab: 'bili' } : {} })
}
</script>

<style scoped>
.tv-tabs {
  display: flex;
  flex-direction: column;
  padding: 2px 12px;
}
.tv-tabs-bar :deep(.el-tabs__header) {
  margin-bottom: 14px;
}
</style>
