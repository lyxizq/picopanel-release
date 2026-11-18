<template>
  <span ref="box" class="mq" :class="{ on: overflow }" :style="mqStyle"><span ref="txt" class="mq-txt"><slot /></span></span>
</template>

<script setup>
// 溢出来回滚动的单行文本（2026-10-23 用户指定：首页 Emby 正在播放标题滚动展示）：
// 不溢出=静止原样；溢出=alternate 来回滚动（比单向循环少一次硬跳变），速度约
// 30px/s、距离按实测溢出宽度；窗口尺寸（ResizeObserver）与内容（content 快照
// prop，仅作重测信号）变化后重测；系统开启「减少动态效果」时保持静止。
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'

const props = defineProps({
  content: { type: String, default: '' }, // 滚动内容的纯文本快照（变化触发重测）
  speed: { type: Number, default: 30 }, // px/s
})

const box = ref(null)
const txt = ref(null)
const overflow = ref(false)
const dist = ref(0)
const dur = ref(10)
const mqStyle = computed(() => ({ '--mq-dist': `-${dist.value}px`, '--mq-dur': `${dur.value}s` }))

let ro = null
function measure() {
  const b = box.value
  const t = txt.value
  if (!b || !t) return
  const d = Math.ceil(t.scrollWidth - b.clientWidth)
  overflow.value = d > 2
  dist.value = d + 8 // 略过量，折返前留点余量
  dur.value = Math.max(6, Math.round((d + 8) / props.speed))
}
onMounted(() => {
  measure()
  ro = new ResizeObserver(() => measure())
  ro.observe(box.value)
  // 字体晚于首测到达会改变文本实测宽，就绪后重测一次
  document.fonts?.ready?.then(() => measure()).catch(() => {})
})
onBeforeUnmount(() => ro && ro.disconnect())
watch(() => props.content, () => nextTick(measure))
</script>

<style scoped>
.mq {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  vertical-align: bottom;
}
.mq-txt {
  display: inline-block; /* 必须块化：inline 的 scrollWidth 拿不到内容宽（恒 0），溢出永远测不出 */
  white-space: nowrap;
}
.mq.on .mq-txt {
  animation: mq-scroll var(--mq-dur) ease-in-out infinite alternate;
}
@media (prefers-reduced-motion: reduce) {
  .mq.on .mq-txt {
    animation: none;
  }
}
@keyframes mq-scroll {
  from {
    transform: translateX(0);
  }
  to {
    transform: translateX(var(--mq-dist));
  }
}
</style>
