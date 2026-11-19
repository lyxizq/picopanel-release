<template>
  <el-row :gutter="12">
    <el-col v-for="s in servers" :key="s.id" :xs="24" :sm="12" :md="8" :lg="6" class="card-col">
      <MetricCard :server="s" @open="$emit('open', $event)" @edit="$emit('edit', $event)" />
    </el-col>
  </el-row>
</template>

<script setup>
// 模块卡片网格：Dashboard 平铺/分组手风琴两处共用。
import MetricCard from './MetricCard.vue'

defineProps({
  servers: { type: Array, required: true },
})
defineEmits(['open', 'edit'])
</script>

<style scoped>
.card-col {
  margin-bottom: 12px;
}
</style>
