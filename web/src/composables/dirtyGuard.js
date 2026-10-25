// 统一的弹层未保存守卫（328）：新增/编辑弹层关闭前（X/Esc/点遮罩）对比表单快照，
// 有未保存变更时弹确认，防止误触直接丢失。保存按钮走程序化关闭不经 before-close，
// 天然绕过守卫。用法：
//   const guard = useDirtyGuard(() => ({ ...form }))
//   弹层打开并填完初值后调用 guard.snapshot()（编辑态在回填完成后调）
//   <el-dialog :before-close="guard.beforeClose">
//   快照 getter 返回 null/undefined = 弹层当前非表单态，守卫自动放行。
import { ElMessageBox } from 'element-plus'
import { i18n } from '../i18n'

const t = (...a) => i18n.global.t(...a)

export function useDirtyGuard(getSnapshot) {
  let baseline = null
  // 闭包函数而非对象方法：模板里 :before-close="guard.beforeClose" 传引用会丢 this，
  // 用 this.isDirty() 曾导致 TypeError 弹层关不掉（事故）
  function isDirty() {
    const snap = getSnapshot()
    if (snap == null || baseline === null) return false
    return JSON.stringify(snap) !== baseline
  }

  function beforeClose(done) {
    if (!isDirty()) return done()
    ElMessageBox.confirm(t('dirty.body'), t('dirty.title'), {
      confirmButtonText: t('dirty.discard'),
      cancelButtonText: t('dirty.keep'),
      type: 'warning',
    })
      // 勿写 .then(done)：confirm 的 resolve 值 'confirm' 会作为首参传给
      // done(shouldCancel)——真值被 EP 当「取消关闭」直接 return，弹层关不掉
      //（2026-10-05 用户报障根因）
      .then(() => done())
      .catch(() => {})
  }

  return {
    // 记录基线快照（打开弹层回填完成后调用；不调用则守卫不生效）
    snapshot() {
      baseline = JSON.stringify(getSnapshot() || {})
    },
    isDirty,
    beforeClose,
  }
}
