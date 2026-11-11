import { onScopeDispose, ref } from 'vue'

// 手机端（≤768px）判定：实时响应窗口宽度变化（横竖屏切换），作用域销毁自动解绑。
export function useIsMobile() {
  const mql = window.matchMedia('(max-width: 768px)')
  const isMobile = ref(mql.matches)
  const onChange = (e) => {
    isMobile.value = e.matches
  }
  mql.addEventListener('change', onChange)
  onScopeDispose(() => mql.removeEventListener('change', onChange))
  return isMobile
}
