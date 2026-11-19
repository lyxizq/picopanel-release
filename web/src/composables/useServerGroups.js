import { computed, ref, watch } from 'vue'
import { grpColor } from '../utils/chartTheme.js'
import { i18n } from '../i18n'

const t = (...a) => i18n.global.t(...a)

// 服务器侧栏的分组聚合与折叠状态（终端页/文件管理页共用）。
// 入参为服务器与分组列表的 ref；服务器按 term_available 过滤 + 关键字过滤
// （名称/备注）；有名分组按名称排序（zh locale），未分组固定最后，组内按名称正序。
// 折叠为手风琴：默认只展开第一组；分组集合变化时保留用户对既有组的操作，
// 新组默认收起、消失的组清出。
export function useServerGroups(servers, groups) {
  const kw = ref('')

  const filtered = computed(() => {
    const all = servers.value.filter((s) => s.term_available)
    const k = kw.value.trim().toLowerCase()
    const hit = k ? all.filter((s) => (s.name + ' ' + (s.remark || '')).toLowerCase().includes(k)) : all
    return hit.sort((a, b) => a.name.localeCompare(b.name, 'zh'))
  })

  const grouped = computed(() => {
    const byG = new Map()
    for (const s of filtered.value) {
      const gid = s.group_id || 0
      if (!byG.has(gid)) byG.set(gid, [])
      byG.get(gid).push(s)
    }
    const byName = (a, b) => a.name.localeCompare(b.name, 'zh')
    const out = groups.value
      .filter((g) => byG.has(g.id))
      .sort((a, b) => a.name.localeCompare(b.name, 'zh'))
      .map((g) => ({ id: g.id, name: g.name, color: grpColor(g.id), servers: byG.get(g.id).sort(byName) }))
    if (byG.has(0)) {
      out.push({ id: 0, name: t('groups.ungrouped'), color: grpColor(0), servers: byG.get(0).sort(byName) })
    }
    return out
  })

  const openGroups = ref([])
  watch(
    () => grouped.value.map((g) => g.id).join('|'),
    (nv, ov) => {
      const first = grouped.value.length ? String(grouped.value[0].id) : ''
      const prev = new Set((ov || '').split('|').filter(Boolean))
      const cur = new Set(openGroups.value)
      openGroups.value = grouped.value
        .map((g) => String(g.id))
        .filter((id) => (prev.has(id) ? cur.has(id) : id === first && !prev.size))
    },
    { immediate: true },
  )

  return { kw, filtered, grouped, openGroups }
}
