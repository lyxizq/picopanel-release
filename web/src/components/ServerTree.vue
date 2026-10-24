<template>
  <!-- 桌面端：搜索框 + 分组折叠列表 -->
  <div v-if="!isMobile" class="side">
    <el-input v-model="kw" placeholder="搜索服务器" clearable size="small" :prefix-icon="Search" class="side-search" />
    <div class="side-list">
      <el-collapse v-model="openGroups" class="side-collapse">
        <el-collapse-item v-for="g in grouped" :key="g.id" :name="String(g.id)">
          <template #title>
            <div class="g-row">
              <span class="g-accent" :style="{ background: g.color }" />
              <el-icon class="g-icon" :style="{ color: g.color }"><Monitor /></el-icon>
              <span class="g-name" :title="g.name">{{ g.name }}</span>
              <span class="g-count">{{ g.servers.length || '' }}</span>
            </div>
          </template>
          <div
            v-for="s in g.servers"
            :key="s.id"
            class="side-item"
            :class="{ on: s.id === selectedId }"
            @click="$emit('select', s)"
            @dblclick="$emit('open-new', s)"
          >
            <span class="dot" :class="s.online ? 'up' : 'down'"></span>
            <span class="side-name" :title="`${s.name}${s.remark ? ' · ' + s.remark : ''}`"><img v-if="s.icon" class="srv-ico" :src="s.icon" alt="" @error="$event.target.style.display = 'none'" />{{ s.name }}</span>
            <slot name="append" :s="s" />
          </div>
        </el-collapse-item>
      </el-collapse>
      <div v-if="!grouped.length" class="side-empty">
        {{ servers.length ? '没有匹配的服务器' : '暂无可用服务器：在编辑服务器里配置 SSH 信息' }}
      </div>
    </div>
  </div>
  <!-- 手机端：分组下拉选择器 -->
  <el-select
    v-else
    :model-value="selectedId || null"
    placeholder="选择服务器"
    size="small"
    class="mobile-select"
    @change="(id) => $emit('select', servers.find((x) => x.id === id))"
  >
    <el-option-group v-for="g in grouped" :key="g.id" :label="g.name">
      <el-option v-for="s in g.servers" :key="s.id" :label="s.name" :value="s.id" />
    </el-option-group>
  </el-select>
</template>

<script setup>
// 分组服务器侧栏（终端页/文件管理页共用）：数据经 props 传入、由父组件加载；
// 聚合/过滤/折叠状态在 useServerGroups()。条目选中由 selectedId 控制，
// 点击经 select 事件上抛；append 插槽用于名称后追加标记（如终端页断线红点）。
import { Search, Monitor } from '@element-plus/icons-vue'
import { toRef } from 'vue'
import { useIsMobile } from '../composables/useIsMobile.js'
import { useServerGroups } from '../composables/useServerGroups.js'


const props = defineProps({
  servers: { type: Array, required: true },
  groups: { type: Array, required: true },
  selectedId: { type: Number, default: 0 },
})
// open-new：双击服务器行（终端页多开窗口用；文件管理页不监听则无副作用）
defineEmits(['select', 'open-new'])

const isMobile = useIsMobile()
const { kw, grouped, openGroups } = useServerGroups(toRef(props, 'servers'), toRef(props, 'groups'))
</script>

<style scoped>
.side {
  width: 210px;
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 0;
}
.side-search {
  flex: none;
}
.side-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  border: 1px solid var(--border, #e4e7ed);
  border-radius: 8px;
  background: var(--surface);
  padding: 4px;
}
/* 分组折叠（文档页同款）：组头着色（色条 + 图标同色），服务器行为叶子 */
.side-collapse {
  --el-collapse-header-bg-color: transparent;
  --el-collapse-content-bg-color: transparent;
  --el-collapse-border-color: transparent;
  border-top: none;
  border-bottom: none;
}
.side-collapse :deep(.el-collapse-item__header) {
  height: 30px;
  line-height: 30px;
  border-bottom: none;
}
.side-collapse :deep(.el-collapse-item__wrap) {
  border-bottom: none;
}
.side-collapse :deep(.el-collapse-item__content) {
  padding-bottom: 4px;
}
.g-row {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  min-width: 0;
  padding-right: 8px;
  color: var(--ink);
  font-size: 14px;
  user-select: none;
}
.g-row:hover .g-name {
  color: var(--brand);
}
.g-accent {
  width: 3px;
  height: 14px;
  border-radius: 2px;
  flex: none;
}
.g-icon {
  font-size: 15px;
  flex: none;
}
.g-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}
.g-count {
  font-size: 12px;
  color: var(--muted);
}
.side-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
}
.side-item:hover {
  background: var(--el-fill-color-light);
}
.side-item.on {
  background: var(--el-color-primary-light-9, #ecf5ff);
  color: var(--el-color-primary);
}
.side-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex: none;
}
.dot.up {
  background: #67c23a;
}
.dot.down {
  background: #c0c4cc;
}
/* 后台 tab 断线红点（append 插槽） */
.tab-alert {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #f56c6c;
  flex: none;
}
.side-empty {
  color: var(--muted);
  font-size: 12px;
  padding: 14px 10px;
  line-height: 1.6;
}
.mobile-select {
  flex: none;
}
</style>
