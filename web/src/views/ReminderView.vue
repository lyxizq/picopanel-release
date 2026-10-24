<template>
  <div class="todo-page">
    <!-- 左侧列表导航（MS To Do 形态）：智能列表 + 我的列表；移动端收成顶部横向 chips -->
    <aside v-if="!isMobile" class="side">
      <div class="side-list">
        <button
          v-for="s in smartLists" :key="s.key"
          class="side-item" :class="{ active: view === s.key }"
          @click="view = s.key"
        >
          <el-icon class="side-ico" :class="s.key"><component :is="s.icon" /></el-icon>
          <span class="side-name">{{ s.name }}</span>
          <span class="side-count">{{ s.count }}</span>
        </button>
      </div>
      <div class="side-sec">我的列表</div>
      <div class="side-list">
        <button
          v-for="g in groupItems" :key="g.key"
          class="side-item" :class="{ active: view === g.key }"
          @click="view = g.key"
        >
          <span class="grp-dot" :style="{ background: groupColor(g.raw.id) }"><el-icon><List /></el-icon></span>
          <span class="side-name">{{ g.name }}</span>
          <span class="side-count">{{ g.count }}</span>
          <span v-if="!g.fixed" class="side-ops" @click.stop>
            <el-icon class="op" title="重命名" @click="renameGroup(g.raw)"><EditPen /></el-icon>
            <el-icon class="op" title="删除" @click="removeGroup(g.raw)"><Delete /></el-icon>
          </span>
        </button>
      </div>
      <div class="side-add">
        <el-input v-model="newGroupName" size="small" placeholder="新建列表" maxlength="20" @keyup.enter="addGroup">
          <template #suffix><el-icon class="add-ico" @click="addGroup"><Plus /></el-icon></template>
        </el-input>
      </div>
    </aside>

    <div class="main">
      <!-- 移动端：智能列表 + 分组横向 chips -->
      <div v-if="isMobile" class="m-chips">
        <button
          v-for="c in mobileChips" :key="c.key"
          class="chip" :class="{ active: view === c.key }"
          @click="view = c.key"
        ><span v-if="c.color" class="chip-dot" :style="{ background: c.color }"></span>{{ c.name }}<span v-if="c.count" class="chip-n">{{ c.count }}</span></button>
      </div>

      <div class="panel" v-loading="loading">
        <div class="panel-head">
          <h2 class="panel-title">{{ currentTitle }}</h2>
          <el-button v-if="view !== 'done' && view !== 'trash'" type="primary" plain size="small" @click="view === 'member' ? openMemberDrawer(null) : openDrawer(null)">
            <el-icon style="margin-right: 4px"><Plus /></el-icon>新增
          </el-button>
        </div>

        <!-- 快速添加（MS 同款：回车或点 + 即建，详细参数进详情改） -->
        <div v-if="view !== 'done' && view !== 'trash' && view !== 'member'" class="quick-add">
          <el-icon class="qa-ico" title="新增" @click="quickAdd"><CirclePlus /></el-icon>
          <el-input
            v-model="quickTitle"
            :placeholder="quickPlaceholder"
            maxlength="100"
            @compositionstart="quickComposing = true"
            @compositionend="quickComposing = false"
            @keyup.enter="onQuickEnter"
          />
        </div>

        <!-- 事项列表：按日期分段（我的一天/重要为平铺）；子任务嵌在父任务行下，
             当前视图内父任务不可见（跨视图成员）的子任务升为根展示 -->
        <template v-for="sec in sections" :key="sec.label || 'flat'">
          <div v-if="sec.label" class="sec-title" :class="sec.cls">{{ sec.label }}</div>
          <RemTree
            v-for="r in sec.items" :key="r.id"
            :r="r" :show-group="showGroupBadge" :kids-of="kidsMap" :visible="visibleIds" :expanded-set="expandedSet"
            @complete="complete" @reopen="reopen" @open="openDrawer" @star="toggleStar" @note="showNote"
          />
        </template>
        <div v-if="!sections.length && view !== 'done' && view !== 'trash' && view !== 'member'" class="empty">没有事项，点右上「新增」或在上方输入框快速添加</div>

        <!-- 到期提醒视图（492）：一页统一展示全部会员，剩余天数进度条，
             到期前 15 天服务端推送提醒；行点击编辑 -->
        <template v-if="view === 'member'">
          <div class="member-tip">到期前 15 天推送一次提醒（站内消息 + 已开启的推送渠道）；续期后修改到期日期会重新武装提醒；拖拽行可调整顺序。点击行编辑。</div>
          <div class="member-grid">
            <div
              v-for="(m, idx) in members" :key="m.id"
              class="rem-row member-row" :class="{ expired: m.expired, soon: !m.expired && m.remaining_days <= 15, dragging: memberDrag.from === idx, 'drag-over': memberDrag.over === idx && memberDrag.from !== idx }"
              draggable="true"
              @dragstart="memberStart(idx, $event)"
              @dragenter.prevent="memberOver(idx)"
              @dragover.prevent="memberOver(idx)"
              @drop.prevent="memberDrop(idx)"
              @dragend="memberDrag.from = memberDrag.over = -1"
              @click="openMemberDrawer(m)"
            >
              <div class="row-inner">
                <div class="row-main">
                  <div class="row-title">
                    <span class="row-title-text">{{ m.biz }}</span>
                    <span v-if="m.name" class="row-badge">{{ m.name }}</span>
                    <span v-if="m.expired" class="row-badge danger">已过期 {{ -m.remaining_days }} 天</span>
                    <span v-else-if="m.remaining_days === 0" class="row-badge danger">今天到期</span>
                    <span v-else-if="m.remaining_days <= 15" class="row-badge danger">剩 {{ m.remaining_days }} 天</span>
                  </div>
                  <div class="row-note">{{ m.start_date ? `${m.start_date} → ${m.expire_date}` : `到期 ${m.expire_date}` }}</div>
                  <el-progress
                    class="member-bar" :percentage="m.progress || 0" :color="memberBarColor(m)"
                    :stroke-width="6" :show-text="false"
                  />
                </div>
                <span class="member-left" :class="{ danger: m.expired || m.remaining_days <= 15 }">
                  {{ m.expired ? '已过期' : `剩 ${m.remaining_days} 天` }}
                </span>
                <span class="member-ops" @click.stop>
                  <el-tooltip content="删除" placement="top" :hide-after="0">
                    <el-button class="dt-ico-btn" circle type="danger" plain size="small" @click="removeMember(m)">
                      <el-icon><Delete /></el-icon>
                    </el-button>
                  </el-tooltip>
                </span>
              </div>
            </div>
          </div>
          <div v-if="!members.length" class="empty">还没有会员记录，点右上「新增」添加</div>
        </template>

        <!-- 分组视图内的已完成（折叠；已完成平铺不含子任务嵌套） -->
        <template v-if="view.startsWith('g-')">
          <div class="sec-title done-toggle" @click="showGroupDone = !showGroupDone">
            <el-icon class="arrow" :class="{ open: showGroupDone }"><ArrowRight /></el-icon>
            已完成（{{ groupDone.length }}）
          </div>
          <template v-if="showGroupDone">
            <RemTree v-for="r in groupDone" :key="r.id" :r="r" :show-group="false" @reopen="reopen" @open="openDrawer" />
          </template>
        </template>

        <!-- 已完成视图 -->
        <template v-if="view === 'done'">
          <RemTree v-for="r in doneItems" :key="r.id" :r="r" :show-group="true" @reopen="reopen" @open="openDrawer" />
          <div v-if="!doneItems.length" class="empty">暂无已完成记录</div>
        </template>

        <!-- 回收站视图：软删条目按删除时间倒序，可恢复/彻底删除/清空 -->
        <template v-if="view === 'trash'">
          <div class="trash-bar">
            <span class="trash-tip">删除的事项在此保留，可恢复或彻底删除</span>
            <el-button v-if="board.trash.length" size="small" type="danger" plain @click="emptyTrash">清空回收站</el-button>
          </div>
          <div v-for="r in board.trash" :key="r.id" class="rem-row trash">
            <div class="row-inner trash-inner">
              <div class="row-main">
                <div class="row-title" :class="{ 'is-done': r.done_at }">
                  <span class="row-title-text">{{ r.title }}</span>
                  <span v-if="repeatText(r.repeat)" class="row-badge"><span class="badge-ico">↻</span>{{ repeatText(r.repeat) }}</span>
                  <span v-if="r.group_name" class="row-badge">{{ r.group_name }}</span>
                  <span v-if="r.done_at" class="row-badge">已完成</span>
                </div>
                <div class="row-note">删除于 {{ fmtDone(r.deleted_at) }} · 到期 {{ r.due_date }} {{ r.due_time || '09:00' }}</div>
              </div>
              <el-button size="small" text type="primary" @click="restoreTrash(r)">恢复</el-button>
              <el-button size="small" text type="danger" @click="hardRemove(r)">彻底删除</el-button>
            </div>
          </div>
          <div v-if="!board.trash.length" class="empty">回收站是空的</div>
        </template>
      </div>
    </div>

    <!-- 详情侧栏（替代原弹窗表单） -->
    <el-drawer append-to-body
      v-model="drawerOpen"
      :title="form.id ? '任务详情' : form.parent_id ? '新建子任务' : '新建任务'"
      :size="isMobile ? '100%' : '504px'"
      class="rem-drawer"
      :before-close="drawerGuard.beforeClose"
    >
      <div class="dt-body">
        <el-input v-model="form.title" maxlength="100" placeholder="任务标题" class="dt-title" />
        <div class="dt-flags">
          <button class="flag" :class="{ on: form.important }" @click="form.important = !form.important">
            <el-icon><Star /></el-icon>重要
          </button>
          <button class="flag" :class="{ on: form.my_day }" @click="form.my_day = !form.my_day">
            <el-icon><Sunny /></el-icon>我的一天
          </button>
        </div>
        <div class="dt-grid">
          <div class="dt-label"><el-icon><Calendar /></el-icon>到期日期</div>
          <el-date-picker v-model="form.due_date" type="date" value-format="YYYY-MM-DD" placeholder="选择日期" style="width: 100%" :teleported="false" />
          <div class="dt-label"><el-icon><AlarmClock /></el-icon>提醒时间</div>
          <el-time-select v-model="form.due_time" start="00:00" step="00:15" end="23:45" placeholder="默认 09:00" style="width: 100%" :teleported="false" />
          <div class="dt-label"><el-icon><Refresh /></el-icon>重复</div>
          <el-select v-model="form.repeatType" :teleported="false" @change="onRepeatTypeChange">
            <el-option label="不重复" value="none" />
            <el-option label="每天" value="daily" />
            <el-option label="每周" value="weekly" />
            <el-option label="每月" value="monthly" />
            <el-option label="每年" value="yearly" />
            <el-option label="自定义…" value="custom" />
          </el-select>
        </div>
        <div v-if="form.repeatType === 'custom'" class="dt-custom">
          每 <el-input-number v-model="form.every" :min="1" :max="999" controls-position="right" style="width: 88px" />
          <el-select v-model="form.unit" style="width: 82px" :teleported="false">
            <el-option label="天" value="day" />
            <el-option label="周" value="week" />
            <el-option label="月" value="month" />
            <el-option label="年" value="year" />
          </el-select>
          <el-date-picker v-model="form.until" type="date" value-format="YYYY-MM-DD" placeholder="结束日期（可选）" style="flex: 1; min-width: 130px" :teleported="false" />
        </div>
        <div class="dt-preview">{{ repeatPreview }}</div>
        <div class="dt-label" style="margin-top: 10px"><el-icon><Document /></el-icon>备注</div>
        <el-input v-model="form.note" type="textarea" :rows="7" resize="vertical" class="dt-note" maxlength="500" placeholder="可选" />
        <div class="dt-label" style="margin-top: 10px"><el-icon><List /></el-icon>所属列表</div>
        <!-- 抽屉内全部下拉/日期面板 teleported=false：弹层锚定在触发框正下方（抽屉 transform 祖先会让全局传送弹层定位错乱，2026-10-03/10-09 用户反馈） -->
        <el-select v-model="form.group_id" style="width: 100%" :teleported="false">
          <el-option label="未分组" :value="0" />
          <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id" />
        </el-select>
        <div class="dt-label" style="margin-top: 12px"><el-icon><Bell /></el-icon>是否提醒</div>
        <el-switch v-model="form.notify" active-text="到期提醒" inactive-text="仅记录" />
        <div v-if="form.parent_id && !form.id" class="form-hint">将作为「{{ parentTitle }}」的子任务添加。</div>
        <div class="form-hint">{{ form.notify ? '开启后到期的提醒经站内消息与推送渠道送达：到期前 1 小时一次、到期时一次，一直未完成则次日再提醒一次。' : '已关闭提醒：只记录不推送、不进站内消息。' }}重复事项完成后自动生成下一条。</div>
      </div>
      <template #footer>
        <div class="dt-footer">
          <div class="fg">
            <el-tooltip v-if="form.id" content="删除任务" placement="top" :hide-after="0">
              <el-button class="dt-ico-btn" circle type="danger" plain @click="remove(form2Ref)">
                <el-icon class="dt-ico"><Delete /></el-icon>
              </el-button>
            </el-tooltip>
            <el-tooltip v-if="form.id && levelOf(form2Ref) < 5" content="添加子任务" placement="top" :hide-after="0">
              <el-button class="dt-ico-btn" circle plain @click="addChild">
                <el-icon class="dt-ico"><Plus /></el-icon>
              </el-button>
            </el-tooltip>
            <el-tooltip v-if="form.id" content="测试推送（按到期文案立即推送，汇总各渠道结果）" placement="top" :hide-after="0">
              <el-button class="dt-ico-btn" circle type="warning" plain :loading="testingPush" @click="testPush">
                <el-icon v-if="!testingPush" class="dt-ico"><Promotion /></el-icon>
              </el-button>
            </el-tooltip>
          </div>
          <div class="fg">
            <el-tooltip content="取消" placement="top" :hide-after="0">
              <el-button class="dt-ico-btn" circle @click="drawerOpen = false">
                <el-icon class="dt-ico"><Close /></el-icon>
              </el-button>
            </el-tooltip>
            <el-tooltip content="保存" placement="top" :hide-after="0">
              <el-button class="dt-ico-btn" circle type="primary" :loading="saving" @click="save">
                <el-icon v-if="!saving" class="dt-ico"><Check /></el-icon>
              </el-button>
            </el-tooltip>
          </div>
        </div>
      </template>
    </el-drawer>

    <!-- 到期提醒侧栏（492）：名称/会员名/开始时间/到期日期（进度条按起止区间） -->
    <el-drawer append-to-body
      v-model="memberDrawerOpen"
      :title="memberForm.id ? '会员详情' : '新增会员'"
      :size="isMobile ? '100%' : '504px'"
      class="rem-drawer"
      :before-close="memberGuard.beforeClose"
    >
      <div class="dt-body">
        <div class="dt-grid">
          <div class="dt-label"><el-icon><Ticket /></el-icon>名称</div>
          <el-input v-model="memberForm.biz" maxlength="50" placeholder="如 Netflix、百度网盘" />
          <div class="dt-label"><el-icon><User /></el-icon>会员名</div>
          <el-input v-model="memberForm.name" maxlength="50" placeholder="如 年费会员 / 黄金会员（可选）" />
          <div class="dt-label"><el-icon><Clock /></el-icon>开始时间</div>
          <el-date-picker v-model="memberForm.start_date" type="date" value-format="YYYY-MM-DD" placeholder="进度条起点（可选）" style="width: 100%" />
          <div class="dt-label"><el-icon><Calendar /></el-icon>到期日期</div>
          <el-date-picker v-model="memberForm.expire_date" type="date" value-format="YYYY-MM-DD" placeholder="选择日期" style="width: 100%" />
        </div>
        <div class="form-hint">进度条按 开始时间 → 到期日期 区间算剩余占比；开始时间留空则按 剩余天数/365 估算。到期前 15 天推送一次提醒：站内消息 + 已开启的推送渠道（沿用提醒事项推送开关）；修改到期日期会重新武装提醒。</div>
      </div>
      <template #footer>
        <div class="dt-footer">
          <div class="fg">
            <el-tooltip v-if="memberForm.id" content="删除会员" placement="top" :hide-after="0">
              <el-button class="dt-ico-btn" circle type="danger" plain @click="removeMember(memberFormRef)">
                <el-icon class="dt-ico"><Delete /></el-icon>
              </el-button>
            </el-tooltip>
            <el-tooltip v-if="memberForm.id" content="测试推送（按到期文案立即推送，汇总各渠道结果）" placement="top" :hide-after="0">
              <el-button class="dt-ico-btn" circle type="warning" plain :loading="testingMemberPush" @click="testMemberPush()">
                <el-icon v-if="!testingMemberPush" class="dt-ico"><Promotion /></el-icon>
              </el-button>
            </el-tooltip>
          </div>
          <div class="fg">
            <el-tooltip content="取消" placement="top" :hide-after="0">
              <el-button class="dt-ico-btn" circle @click="memberDrawerOpen = false">
                <el-icon class="dt-ico"><Close /></el-icon>
              </el-button>
            </el-tooltip>
            <el-tooltip content="保存" placement="top" :hide-after="0">
              <el-button class="dt-ico-btn" circle type="primary" :loading="memberSaving" @click="saveMember">
                <el-icon v-if="!memberSaving" class="dt-ico"><Check /></el-icon>
              </el-button>
            </el-tooltip>
          </div>
        </div>
      </template>
    </el-drawer>
  </div>
</template>

<script setup>
// 提醒事项（MS To Do 形态，2026-10-09 由「待办事项」更名）：左侧列表导航（我的一天/重要/计划内/已完成 + 分组列表）
// + 右侧事项列表 + 快速添加 + 详情侧栏。子任务（最多 5 级）各自独立的待办，
// 嵌在父任务行下展开/收起。「是否提醒」关闭 = 仅记录（不推送、不进站内消息）。
// 重复规则存 JSON（每天/每周/每月/每年/自定义每 N 天|周|月|年 + 可选结束日期），
// 完成后由服务端生成下一条（每 3 个月即到期日 +3 个月，月末自动收敛）。
// 到期推送（前 1 小时/到期时/次日）由服务端调度（只调度开了提醒的事项）。
defineOptions({ name: 'ReminderView' })
import { computed, h, nextTick, onActivated, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { ElMessage, ElMessageBox } from 'element-plus'
import { AlarmClock, ArrowRight, Bell, Calendar, Check, CirclePlus, Clock, Close, Delete, Document, EditPen, List, Plus, Promotion, Refresh, Star, Sunny, Ticket, User } from '@element-plus/icons-vue'
import { api } from '../api/index.js'
import { useIsMobile } from '../composables/useIsMobile.js'

const isMobile = useIsMobile()
const route = useRoute()
const loading = ref(false)
// 支持 ?view=member 直达到期提醒视图（主页「会员」统计卡入口）
const view = ref(route.query.view === 'member' ? 'member' : 'plan') // myday | important | plan | member | done | g-<id>
const showGroupDone = ref(false)
const board = reactive({ today: [], plan: [], done: [], groups: [], trash: [] })
// 会员到期（492）：独立配置数据，随看板一并拉取
const members = ref([])
const memberStats = reactive({ total: 0, expiring: 0 })
const groups = computed(() => board.groups || [])
const allOpen = computed(() => [...board.today, ...board.plan]) // 全部未完成（服务端已按日期升序）

// ---- 左侧导航数据 ----
// 分组配色：固定色板按组 id 取模循环（参考 MS To Do 每列表一个颜色），零后端改动
const GROUP_PALETTE = ['#2563eb', '#7c3aed', '#d97706', '#059669', '#db2777', '#0891b2', '#dc2626', '#4d7c0f']
function groupColor(id) {
  return GROUP_PALETTE[Math.abs(Number(id) || 0) % GROUP_PALETTE.length]
}
// 我的一天 = 勾选我的一天的事项 ∪ 今天（含逾期）到期的事项——当天该做的都该在这里看到；
// 当天已完成的相关事项也保留展示（划线样式，RemTree 原生支持），次日自动移出
const mydayItems = computed(() => {
  const t = todayStr()
  const open = allOpen.value.filter((r) => r.my_day || r.due_date <= t)
  const doneToday = board.done.filter((r) => {
    if (!r.done_at || !(r.my_day || r.due_date <= t)) return false
    const d = new Date(r.done_at * 1000)
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` === t
  })
  return [...open, ...doneToday]
})
const importantItems = computed(() => allOpen.value.filter((r) => r.important))
// 分组列表 + 固定的「未分组」（同监控页/服务器管理页口径：未分组固定最后，
// 收纳 group_id=0 的事项，可查看/快速添加归入；fixed 标记不渲染重命名/删除）
const groupItems = computed(() => {
  const items = (board.groups || []).map((g) => ({
    key: `g-${g.id}`, name: g.name, raw: g,
    count: allOpen.value.filter((r) => (r.group_id || 0) === g.id).length,
  }))
  items.push({
    key: 'g-0', name: '未分组', raw: { id: 0 }, fixed: true,
    count: allOpen.value.filter((r) => !r.group_id).length,
  })
  return items
})
const smartLists = computed(() => [
  { key: 'myday', name: '我的一天', icon: Sunny, count: mydayItems.value.length },
  { key: 'important', name: '重要', icon: Star, count: importantItems.value.length },
  { key: 'plan', name: '计划内', icon: Calendar, count: allOpen.value.length },
  // 到期提醒（492）：角标数 = 15 日内到期数（提醒关注点），全量在视图内看
  { key: 'member', name: '到期提醒', icon: Ticket, count: memberStats.expiring },
  { key: 'done', name: '已完成', icon: Clock, count: board.done.length },
  { key: 'trash', name: '回收站', icon: Delete, count: board.trash.length },
])
const mobileChips = computed(() => [
  ...smartLists.value.map((s) => ({ key: s.key, name: s.name, count: s.count })),
  ...groupItems.value.map((g) => ({ key: g.key, name: g.name, count: g.count, color: groupColor(g.raw.id) })),
])

const currentGroupId = computed(() => (view.value.startsWith('g-') ? Number(view.value.slice(2)) : -1))
const currentTitle = computed(() => {
  if (view.value === 'myday') return '我的一天'
  if (view.value === 'important') return '重要'
  if (view.value === 'plan') return '计划内'
  if (view.value === 'member') return '到期提醒'
  if (view.value === 'done') return '已完成'
  if (view.value === 'trash') return '回收站'
  if (view.value === 'g-0') return '未分组'
  const g = groups.value.find((x) => x.id === currentGroupId.value)
  return g ? g.name : '列表'
})
const currentItems = computed(() => {
  if (view.value === 'myday') return mydayItems.value
  if (view.value === 'important') return importantItems.value
  if (view.value === 'plan') return allOpen.value
  if (view.value.startsWith('g-')) return allOpen.value.filter((r) => (r.group_id || 0) === currentGroupId.value)
  return []
})
const showGroupBadge = computed(() => view.value === 'myday' || view.value === 'important')

// ---- 子任务（parent_id 关联，最多 5 级，各自独立的待办） ----
// kidsMap：父 ID → 未完成子任务列表（来自 today+plan 全量）
const kidsMap = computed(() => {
  const m = new Map()
  for (const r of allOpen.value) {
    if (!r.parent_id) continue
    if (!m.has(r.parent_id)) m.set(r.parent_id, [])
    m.get(r.parent_id).push(r)
  }
  return m
})
// 当前视图可见条目集合：视图内父任务可见的子任务嵌套展示，
// 父任务不在本视图（如跨分组/未勾我的一天）的子任务升为根展示
const visibleIds = computed(() => new Set(currentItems.value.map((r) => r.id)))
// 展开状态（默认收起）：reactive Set，RemTree 直接增删触发重渲染
const expandedSet = reactive(new Set())
// 全量条目索引（含已完成）+ 层级计算（顶级 = 1）
const allById = computed(() => {
  const m = new Map()
  for (const r of [...allOpen.value, ...board.done]) m.set(r.id, r)
  return m
})
function levelOf(r) {
  let lvl = 1
  const seen = new Set()
  let cur = r
  while (cur && cur.parent_id && !seen.has(cur.id)) {
    seen.add(cur.id)
    const p = allById.value.get(cur.parent_id)
    if (!p) break
    lvl++
    cur = p
  }
  return lvl
}
const groupDone = computed(() => board.done.filter((r) => (r.group_id || 0) === currentGroupId.value))
const doneItems = computed(() => board.done)

// ---- 日期分段（已逾期合并置顶 → 今天 → 未来按日） ----
const WD = ['日', '一', '二', '三', '四', '五', '六']
function pad(n) { return String(n).padStart(2, '0') }
function todayStr() {
  const d = new Date()
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}
function dayShift(days) {
  const d = new Date()
  d.setDate(d.getDate() + days)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}
function weekLabel(d) {
  return '周' + WD[new Date(d + 'T00:00:00').getDay()]
}
function sectionLabel(d) {
  if (d === todayStr()) return `今天（${weekLabel(d)}）`
  if (d === dayShift(1)) return `明天（${weekLabel(d)}）`
  return `${d}（${weekLabel(d)}）`
}
const sections = computed(() => {
  const src = currentItems.value
  // 视图内可见的根条目：无父任务、或父任务不在本视图（子任务升为根）
  const vis = visibleIds.value
  const roots = src.filter((r) => !r.parent_id || !vis.has(r.parent_id))
  if (view.value === 'myday' || view.value === 'important') return [{ label: '', items: roots }]
  const t = todayStr()
  const overdue = []
  const byDate = new Map()
  for (const r of roots) {
    if (r.due_date < t) overdue.push(r)
    else {
      if (!byDate.has(r.due_date)) byDate.set(r.due_date, [])
      byDate.get(r.due_date).push(r)
    }
  }
  const out = []
  if (overdue.length) out.push({ label: `已逾期（${overdue.length}）`, cls: 'overdue-title', items: overdue })
  for (const [d, items] of byDate) out.push({ label: `${sectionLabel(d)}（${items.length}）`, items })
  return out
})

async function load() {
  loading.value = true
  try {
    const [res, mres] = await Promise.all([api.get('/reminders'), api.get('/memberships')])
    board.today = res.today || []
    board.plan = res.plan || []
    board.done = res.done || []
    board.groups = res.groups || []
    board.trash = res.trash || []
    members.value = mres.items || []
    memberStats.total = mres.total || 0
    memberStats.expiring = mres.expiring || 0
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
onMounted(load)
onActivated(load)

// ---- 行内展示组件（函数式，样式全局挂载）：完成圆钮 / 标题+徽标 / 星标；
// 有可见子任务时行首带展开箭头，递归嵌套渲染子任务（缩进 22px/级）；
// 不提醒的事项带「不提醒」徽标。点主体开详情 ----
const RemTree = (props, { emit }) => {
  const r = props.r
  const repeatLabel = repeatText(r.repeat)
  const kids = props.kidsOf && props.visible
    ? (props.kidsOf.get(r.id) || []).filter((k) => props.visible.has(k.id))
    : []
  const hasKids = kids.length > 0
  const expanded = hasKids && props.expandedSet?.has(r.id)
  const fwd = { onComplete: (x) => emit('complete', x), onReopen: (x) => emit('reopen', x), onOpen: (x) => emit('open', x), onStar: (x) => emit('star', x), onNote: (x) => emit('note', x) }
  const treeProps = (k) => ({ r: k, showGroup: false, kidsOf: props.kidsOf, visible: props.visible, expandedSet: props.expandedSet, ...fwd })
  const inner = h('div', { class: 'row-inner' }, [
    hasKids
      ? h('button', {
          class: ['kid-arrow', expanded ? 'open' : ''],
          title: expanded ? '收起子任务' : `展开子任务（${kids.length}）`,
          onClick: (e) => {
            e.stopPropagation()
            const s = props.expandedSet
            if (s.has(r.id)) s.delete(r.id)
            else s.add(r.id)
          },
        }, '▸')
      : h('span', { class: 'kid-arrow ghost' }),
    h('button', {
      class: ['check', r.done_at ? 'checked' : ''],
      title: r.done_at ? '恢复未完成' : '点击完成',
      onClick: () => (r.done_at ? emit('reopen', r) : emit('complete', r)),
    }, r.done_at ? '✓' : ''),
    h('div', { class: 'row-main', onClick: () => emit('open', r) }, [
      h('div', { class: ['row-title', r.done_at ? 'is-done' : ''] }, [
        !r.done_at ? h('span', { class: 'row-time' }, r.due_time || '09:00') : null,
        h('span', { class: 'row-title-text' }, r.title),
        !r.done_at && r.overdue ? h('span', { class: 'row-badge danger' }, '已逾期') : null,
        !r.done_at && r.notify === false ? h('span', { class: 'row-badge' }, '不提醒') : null,
        repeatLabel ? h('span', { class: 'row-badge' }, [h('span', { class: 'badge-ico' }, '↻'), repeatLabel]) : null,
        props.showGroup && r.group_name ? h('span', { class: 'row-badge' }, r.group_name) : null,
      ]),
      // 备注只显示第一行，多行尾部「更多」点开完整备注弹层
      r.note
        ? h('div', { class: 'row-note' }, [
            r.note.split('\n')[0],
            r.note.includes('\n')
              ? h('span', {
                  class: 'note-more',
                  onClick: (e) => {
                    e.stopPropagation()
                    emit('note', r)
                  },
                }, '更多')
              : null,
          ])
        : null,
      r.done_at ? h('div', { class: 'row-note' }, `完成于 ${fmtDone(r.done_at)}`) : null,
    ]),
    !r.done_at ? h('button', {
      class: ['star', r.important ? 'on' : ''],
      title: r.important ? '取消重要' : '标记重要',
      onClick: () => emit('star', r),
    }, '★') : null,
  ])
  return h('div', { class: 'rem-branch' }, [
    h('div', { class: ['rem-row', r.done_at ? 'done' : '', r.overdue && !r.done_at ? 'overdue' : ''] }, [inner]),
    expanded ? h('div', { class: 'rem-kids' }, kids.map((k) => h(RemTree, treeProps(k)))) : null,
  ])
}
RemTree.props = ['r', 'showGroup', 'kidsOf', 'visible', 'expandedSet']
RemTree.emits = ['complete', 'open', 'star', 'reopen', 'note']

function repeatText(repeatJson) {
  if (!repeatJson) return ''
  try {
    const r = JSON.parse(repeatJson)
    if (!r.type || r.type === 'none') return ''
    if (r.type !== 'custom') return { daily: '每天', weekly: '每周', monthly: '每月', yearly: '每年' }[r.type]
    const unit = { day: '天', week: '周', month: '月', year: '年' }[r.unit] || '天'
    return `每 ${r.every} ${unit}`
  } catch {
    return ''
  }
}
function fmtDone(unix) {
  const d = new Date(unix * 1000)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// ---- 快速添加：默认今天 09:00 不重复；我的一天/重要/分组视图带对应属性 ----
const quickTitle = ref('')
// 输入法组合守卫：Safari/WebKit 用回车确认候选时 keyup 先于 compositionend 到达，
// v-model 尚未同步（quickTitle 为空），无守卫会静默返回表现为「回车没反应」；
// 组合中的回车只当确认不当提交，结束后再补一次回车（或点 + / 新增按钮）即创建。
const quickComposing = ref(false)
function onQuickEnter() {
  if (quickComposing.value) return
  quickAdd()
}
const quickPlaceholder = computed(() => {
  if (view.value === 'myday') return '添加到我的一天'
  if (view.value === 'important') return '添加重要事项'
  if (view.value.startsWith('g-')) return `添加到「${currentTitle.value}」`
  return '添加任务（回车或点左侧 + 新建）'
})
async function quickAdd() {
  const title = quickTitle.value.trim()
  if (!title) return
  try {
    await api.post('/reminders', {
      group_id: view.value.startsWith('g-') ? currentGroupId.value : 0,
      parent_id: 0,
      title,
      note: '',
      due_date: todayStr(),
      due_time: '',
      repeat: '',
      important: view.value === 'important',
      my_day: view.value === 'myday',
      notify: true,
    })
    quickTitle.value = ''
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function complete(r) {
  try {
    await ElMessageBox.confirm(
      `完成「${r.title}」？${repeatText(r.repeat) ? '重复事项完成后会自动生成下一条。' : ''}`,
      '完成确认',
      { type: 'info', confirmButtonText: '确定完成', cancelButtonText: '取消' }
    )
  } catch {
    return
  }
  try {
    const res = await api.post(`/reminders/${r.id}/complete`)
    ElMessage.success(res.next ? '已完成，并已生成下一次提醒' : '已完成')
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 恢复未完成：周期任务恢复时服务端会撤回自动生成的下一条，再次完成不重复生成
async function reopen(r) {
  try {
    await ElMessageBox.confirm(`将「${r.title}」恢复为未完成？`, '恢复确认', {
      type: 'info',
      confirmButtonText: '确定',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    const res = await api.post(`/reminders/${r.id}/reopen`)
    ElMessage.success(res.removed_next ? '已恢复为未完成，并撤回了自动生成的下一条' : '已恢复为未完成')
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 完整备注弹层（列表里只露第一行，「更多」点开）
function showNote(r) {
  ElMessageBox.alert(h('div', { style: 'white-space:pre-wrap;word-break:break-word;font-size:14px;line-height:1.7' }, r.note), r.title || '备注', {
    confirmButtonText: '关闭',
  }).catch(() => {})
}

// 星标即时保存（乐观更新，失败回滚）
async function toggleStar(r) {
  const body = reminderBody(r)
  body.important = !r.important
  r.important = !r.important
  try {
    await api.put(`/reminders/${r.id}`, body)
  } catch (e) {
    r.important = !r.important
    ElMessage.error(e.message)
  }
}
function reminderBody(r) {
  return {
    group_id: r.group_id || 0,
    title: r.title,
    note: r.note,
    due_date: r.due_date,
    due_time: r.due_time || '',
    repeat: r.repeat || '',
    important: !!r.important,
    my_day: !!r.my_day,
    notify: r.notify !== false,
  }
}

async function remove(r) {
  try {
    await ElMessageBox.confirm('确定要删除此待办吗？子任务与后续提醒将一并进入回收站。', '删除待办', {
      type: 'warning', confirmButtonText: '删除',
    })
  } catch {
    return
  }
  try {
    await api.del(`/reminders/${r.id}`)
    drawerOpen.value = false
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 回收站：恢复 / 单条彻底删除 / 清空 ----
async function restoreTrash(r) {
  try {
    await api.post(`/reminders-trash/${r.id}/restore`)
    ElMessage.success('已恢复')
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function hardRemove(r) {
  try {
    await ElMessageBox.confirm(`彻底删除「${r.title}」？删除后不可恢复。`, '彻底删除', {
      type: 'error', confirmButtonText: '删除',
    })
  } catch {
    return
  }
  try {
    await api.del(`/reminders-trash/${r.id}`)
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function emptyTrash() {
  try {
    await ElMessageBox.confirm(`清空回收站？共 ${board.trash.length} 条，删除后不可恢复。`, '清空回收站', {
      type: 'error', confirmButtonText: '清空',
    })
  } catch {
    return
  }
  try {
    await api.del('/reminders-trash')
    ElMessage.success('已清空')
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 详情侧栏 ----
const drawerOpen = ref(false)
const saving = ref(false)
const form = reactive({
  id: 0, group_id: 0, parent_id: 0, title: '', note: '', due_date: '', due_time: '',
  repeatType: 'none', every: 1, unit: 'day', until: '', important: false, my_day: false, notify: true,
})
const form2Ref = ref(null) // 当前编辑的原行数据（删除/算层级用）
const parentTitle = ref('') // 新建子任务时显示的父任务标题
const drawerGuard = useDirtyGuard(() => ({ ...form, parentTitle: parentTitle.value }))

function openDrawer(r) {
  form2Ref.value = r
  parentTitle.value = ''
  if (r) {
    Object.assign(form, {
      id: r.id, group_id: r.group_id || 0, parent_id: r.parent_id || 0, title: r.title, note: r.note,
      due_date: r.due_date, due_time: r.due_time || '',
      repeatType: 'none', every: 1, unit: 'day', until: '',
      important: !!r.important, my_day: !!r.my_day, notify: r.notify !== false,
    })
    try {
      const rule = JSON.parse(r.repeat || '{}')
      if (rule.type && rule.type !== 'none') {
        form.repeatType = rule.type
        if (rule.type === 'custom') {
          form.every = rule.every || 1
          form.unit = rule.unit || 'day'
          form.until = rule.until || ''
        }
      }
    } catch { /* 坏规则按不重复打开 */ }
  } else {
    Object.assign(form, {
      id: 0, group_id: 0, parent_id: 0, title: '', note: '', due_date: todayStr(), due_time: '',
      repeatType: 'none', every: 1, unit: 'day', until: '', important: false, my_day: false, notify: true,
    })
  }
  drawerOpen.value = true
}

// 添加子任务：以当前任务为父（继承分组），最多 5 级（按钮只在层级 <5 时出现）
function addChild() {
  const parent = form2Ref.value
  if (!parent || levelOf(parent) >= 5) return
  parentTitle.value = parent.title
  Object.assign(form, {
    id: 0, parent_id: parent.id, title: '', note: '', due_date: todayStr(), due_time: '',
    repeatType: 'none', every: 1, unit: 'day', until: '', important: false, my_day: false, notify: true,
  })
  form2Ref.value = null
}

function onRepeatTypeChange(t) {
  if (t === 'custom' && !form.every) form.every = 1
  nextTick(() => drawerGuard.snapshot())
}

const repeatPreview = computed(() => {
  const tail = '；完成后自动生成下一条'
  switch (form.repeatType) {
    case 'none': return '不重复：到期只提醒这一次'
    case 'daily': return '每天重复一次' + tail
    case 'weekly': return '每周重复一次' + tail
    case 'monthly': return '每月重复一次' + tail
    case 'yearly': return '每年重复一次' + tail
    case 'custom': {
      const unit = { day: '天', week: '周', month: '月', year: '年' }[form.unit] || '天'
      let s = `每 ${form.every || 1} ${unit}重复一次` + tail
      if (form.until) s += `，至 ${form.until} 止`
      return s
    }
    default: return ''
  }
})

function repeatJSON() {
  if (form.repeatType === 'none') return ''
  if (form.repeatType === 'custom') {
    return JSON.stringify({ type: 'custom', every: form.every, unit: form.unit, until: form.until || '' })
  }
  return JSON.stringify({ type: form.repeatType })
}

async function save() {
  if (!form.title.trim()) {
    ElMessage.warning('请填写任务标题')
    return
  }
  if (!form.due_date) {
    ElMessage.warning('请选择到期日期')
    return
  }
  saving.value = true
  try {
    const body = {
      group_id: form.group_id || 0,
      title: form.title.trim(),
      note: form.note,
      due_date: form.due_date,
      due_time: form.due_time || '',
      repeat: repeatJSON(),
      important: form.important,
      my_day: form.my_day,
      notify: form.notify,
    }
    if (form.id) {
      await api.put(`/reminders/${form.id}`, body)
    } else {
      if (form.parent_id) body.parent_id = form.parent_id // 父任务仅新建时指定
      await api.post('/reminders', body)
      if (form.parent_id) expandedSet.add(form.parent_id) // 建完自动展开父任务
    }
    ElMessage.success(form.id ? '已保存' : '已添加')
    drawerOpen.value = false
    load()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

// 测试推送：按到期文案立即推一次（站内必达 + 已开渠道），toast 汇总各渠道结果
const testingPush = ref(false)
const CH_LABEL = { wxpusher: 'WxPusher', webhook: 'Webhook', webpush: 'Web Push' }
function toastPushResults(results) {
  const ok = []
  const off = []
  const fail = []
  for (const [ch, v] of Object.entries(results)) {
    if (v === 'ok') ok.push(CH_LABEL[ch] || ch)
    else if (v === 'off') off.push(CH_LABEL[ch] || ch)
    else fail.push(`${CH_LABEL[ch] || ch}：${v}`)
  }
  const parts = ['站内消息已发送']
  if (ok.length) parts.push(`${ok.join('、')} 已推送`)
  if (off.length) parts.push(`${off.join('、')} 未开启`)
  ElMessage({
    type: fail.length ? 'warning' : 'success',
    message: parts.join('；'),
    duration: fail.length ? 6000 : 3000,
  })
  if (fail.length) console.warn('测试推送失败：', fail.join('；'))
}
async function testPush() {
  testingPush.value = true
  try {
    const res = await api.post(`/reminders/${form.id}/test-push`)
    toastPushResults(res.results || {})
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testingPush.value = false
  }
}

// ---- 会员到期（492）：进度条占比由服务端现算（progress 字段：有开始时间按
// 开始→到期区间算剩余占比，留空回落 剩余/周期 默认 365）；
// 颜色按紧迫度：已过期或剩 ≤15 天红、≤45 天橙、其余蓝 ----
const memberBarColor = (m) => (m.expired || m.remaining_days <= 15 ? '#f56c6c' : m.remaining_days <= 45 ? '#e6a23c' : '#409eff')

const memberDrawerOpen = ref(false)
const memberSaving = ref(false)
const testingMemberPush = ref(false)
const memberForm = reactive({ id: 0, biz: '', name: '', start_date: '', expire_date: '', period_days: 365 })
const memberFormRef = ref(null) // 当前编辑的原行数据（删除用）
const memberGuard = useDirtyGuard(() => ({ ...memberForm }))

function openMemberDrawer(m) {
  memberFormRef.value = m
  if (m) {
    Object.assign(memberForm, { id: m.id, biz: m.biz, name: m.name, start_date: m.start_date || '', expire_date: m.expire_date, period_days: m.period_days || 365 })
  } else {
    Object.assign(memberForm, { id: 0, biz: '', name: '', start_date: todayStr(), expire_date: todayStr(), period_days: 365 })
  }
  memberDrawerOpen.value = true
}

async function saveMember() {
  if (!memberForm.biz.trim()) {
    ElMessage.warning('请填写名称')
    return
  }
  if (!memberForm.expire_date) {
    ElMessage.warning('请选择到期日期')
    return
  }
  memberSaving.value = true
  try {
    const body = { biz: memberForm.biz.trim(), name: memberForm.name.trim(), start_date: memberForm.start_date || '', expire_date: memberForm.expire_date, period_days: memberForm.period_days || 365 }
    if (memberForm.id) {
      await api.put(`/memberships/${memberForm.id}`, body)
    } else {
      await api.post('/memberships', body)
    }
    ElMessage.success(memberForm.id ? '已保存' : '已添加')
    memberDrawerOpen.value = false
    load()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    memberSaving.value = false
  }
}

async function removeMember(m) {
  if (!m || !m.id) return
  try {
    await ElMessageBox.confirm(`删除「${m.biz}${m.name ? ' · ' + m.name : ''}」？删除后不可恢复。`, '删除会员', {
      type: 'warning', confirmButtonText: '删除',
    })
  } catch {
    return
  }
  try {
    await api.del(`/memberships/${m.id}`)
    if (memberForm.id === m.id) memberDrawerOpen.value = false
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 测试推送：m 传行数据（列表按钮）或不传（抽屉内按当前表单）
async function testMemberPush(m) {
  const id = (m && m.id) || memberForm.id
  if (!id) return
  testingMemberPush.value = true
  try {
    const res = await api.post(`/memberships/${id}/test-push`)
    toastPushResults(res.results || {})
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testingMemberPush.value = false
  }
}

// ---- 拖拽排序（磁贴/菜单同款 HTML5 DnD）：drop 即本地重排 + 持久化 sort，失败回滚重拉 ----
const memberDrag = reactive({ from: -1, over: -1 })
function memberStart(i, e) {
  memberDrag.from = i
  if (e.dataTransfer) {
    e.dataTransfer.setData('text/plain', String(i))
    e.dataTransfer.effectAllowed = 'move'
  }
}
function memberOver(i) {
  if (memberDrag.from < 0) return
  memberDrag.over = i
}
async function memberDrop(i) {
  const f = memberDrag.from
  memberDrag.from = memberDrag.over = -1
  if (f < 0 || f === i) return
  const list = [...members.value]
  const [moved] = list.splice(f, 1)
  list.splice(f < i ? i - 1 : i, 0, moved)
  members.value = list
  try {
    await api.post('/memberships-reorder', { ids: list.map((x) => x.id) })
  } catch (e) {
    ElMessage.error(e.message)
    load()
  }
}

// ---- 分组（列表）管理：侧栏内联新增 / 重命名 / 删除 ----
const newGroupName = ref('')
async function addGroup() {
  const name = newGroupName.value.trim()
  if (!name) return
  try {
    await api.post('/reminder-groups', { name, sort: groups.value.length })
    newGroupName.value = ''
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function renameGroup(g) {
  let name = ''
  try {
    const res = await ElMessageBox.prompt('新的列表名称', '重命名列表', {
      closeOnClickModal: false,
      inputValue: g.name, inputPattern: /\S/, inputErrorMessage: '名称不能为空',
      confirmButtonText: '保存', cancelButtonText: '取消',
    })
    name = res.value.trim()
  } catch {
    return
  }
  try {
    await api.put(`/reminder-groups/${g.id}`, { name, sort: g.sort })
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function removeGroup(g) {
  try {
    await ElMessageBox.confirm(`删除列表「${g.name}」？组内提醒事项会回到「未分组」。`, '删除列表', {
      type: 'warning', confirmButtonText: '删除',
    })
  } catch {
    return
  }
  try {
    await api.del(`/reminder-groups/${g.id}`)
    if (view.value === `g-${g.id}`) view.value = 'plan'
    load()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<style scoped>
.todo-page {
  display: flex;
  gap: 16px;
  align-items: flex-start;
}
/* ---- 左侧导航 ---- */
.side {
  flex: none;
  width: 208px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 10px 8px;
  position: sticky;
  top: 12px;
}
.side-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.side-item {
  display: flex;
  align-items: center;
  gap: 8px;
  border: none;
  background: none;
  width: 100%;
  padding: 7px 8px;
  border-radius: 7px;
  cursor: pointer;
  color: var(--text-2);
  font-size: 15.5px;
  font-weight: 600;
  text-align: left;
}
.side-item:hover {
  background: var(--bg);
}
.side-item.active {
  background: var(--bg);
  color: var(--brand);
  font-weight: 600;
}
.side-ico {
  flex: none;
  font-size: 15px;
  color: var(--brand);
}
.side-ico.myday {
  color: #d97706;
}
.side-ico.important {
  color: #e6a23c;
}
.side-ico.done {
  color: var(--muted);
}
.side-ico.trash {
  color: var(--muted);
}
.side-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.side-count {
  flex: none;
  font-size: 12px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}
.side-ops {
  flex: none;
  display: none;
  gap: 2px;
}
.side-item:hover .side-ops {
  display: inline-flex;
}
.side-ops .op {
  font-size: 12px;
  color: var(--muted);
  padding: 2px;
  border-radius: 3px;
}
.side-ops .op:hover {
  color: var(--brand);
  background: var(--surface);
}
.side-ops .op:last-child:hover {
  color: var(--crit);
}
.side-sec {
  font-size: 12px;
  font-weight: 600;
  color: var(--muted);
  margin: 14px 8px 4px;
}
/* 分组彩色圆图标（MS To Do：填充圆 + 白色列表字形），颜色按组 id 走色板 */
.grp-dot {
  flex: none;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #fff;
}
.grp-dot .el-icon {
  font-size: 12px;
}
.side-add {
  margin-top: 8px;
  padding: 0 2px;
}
.add-ico {
  cursor: pointer;
  color: var(--muted);
}
.add-ico:hover {
  color: var(--brand);
}
/* ---- 右侧内容 ---- */
.main {
  flex: 1;
  min-width: 0;
}
.m-chips {
  display: none;
}
.panel {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 14px 16px;
  min-height: 300px;
}
.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.panel-title {
  margin: 0;
  font-size: 18px;
  color: var(--ink);
}
.quick-add {
  display: flex;
  align-items: center;
  gap: 10px;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 8px 12px;
  margin-bottom: 6px;
  background: var(--bg);
}
.quick-add:focus-within {
  border-color: var(--brand);
}
.qa-ico {
  flex: none;
  font-size: 17px;
  color: var(--muted);
  cursor: pointer;
}
.qa-ico:hover {
  color: var(--brand);
}
.quick-add :deep(.el-input__wrapper) {
  background: transparent;
  box-shadow: none;
  padding: 0;
}
.sec-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--muted);
  margin: 14px 0 6px;
}
.overdue-title {
  color: var(--crit);
}
.done-toggle {
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 4px;
  user-select: none;
}
.done-toggle .arrow {
  transition: transform 0.15s;
}
.done-toggle .arrow.open {
  transform: rotate(90deg);
}
/* 事项行本体（.rem-row 等）在 RemTree 函数式组件里渲染，样式放全局块 */
.empty {
  color: var(--muted);
  font-size: 13px;
  text-align: center;
  padding: 18px 0;
}
/* ---- 详情抽屉 ---- */
.dt-title :deep(.el-input__wrapper) {
  box-shadow: none;
  padding-left: 0;
  font-size: 16px;
  font-weight: 600;
}
/* 备注框默认高度 160px（可继续拖拽调整） */
.dt-note :deep(.el-textarea__inner) {
  height: 160px;
}
.dt-flags {
  display: flex;
  gap: 8px;
  margin: 10px 0 14px;
}
.flag {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: 1px solid var(--border);
  background: var(--bg);
  color: var(--muted);
  border-radius: 999px;
  padding: 4px 12px;
  font-size: 13px;
  cursor: pointer;
}
.flag:hover {
  border-color: var(--brand);
}
.flag.on {
  color: var(--brand);
  border-color: var(--brand);
  background: var(--bg);
}
.dt-grid {
  display: grid;
  grid-template-columns: 88px 1fr;
  gap: 10px 8px;
  align-items: center;
}
.dt-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--text-2);
}
.dt-custom {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin: 10px 0 0 96px;
}
.dt-preview {
  margin: 8px 0 0 96px;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.6;
}
.form-hint {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
  margin-top: 14px;
}
.dt-footer {
  display: flex;
  align-items: center;
  flex-wrap: wrap; /* 420px 抽屉放不下全部按钮：窄时左右两组换行，保存按钮不再被挤出可视区 */
  gap: 8px;
}
.dt-footer .fg {
  display: flex;
  align-items: center;
  gap: 8px;
}
.dt-footer .fg:last-child {
  margin-left: auto; /* 与左组同行时靠右；换行后独占一行也保持右对齐 */
}
/* 图标钮：圆形加大图标（原文字按钮改图标+tooltip 后的尺寸口径） */
.dt-ico-btn {
  width: 36px;
  height: 36px;
}
.dt-footer .dt-ico {
  font-size: 19px;
}
/* ---- 移动端：侧栏转顶部 chips ---- */
@media (max-width: 768px) {
  .todo-page {
    flex-direction: column;
    /* 纵向排布后必须 stretch：flex-start 下 .main 按内容收缩成超宽（不自适应屏宽） */
    align-items: stretch;
  }
  .main {
    max-width: 100%;
  }
  .quick-add :deep(.el-input) {
    flex: 1;
    min-width: 0;
  }
  .side {
    display: none;
  }
  .m-chips {
    display: flex;
    gap: 8px;
    overflow-x: auto;
    padding-bottom: 6px;
    margin-bottom: 8px;
  }
  .chip {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 5px;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--text-2);
    border-radius: 999px;
    padding: 5px 12px;
    font-size: 13px;
    cursor: pointer;
    white-space: nowrap;
  }
  .chip.active {
    color: var(--brand);
    border-color: var(--brand);
    font-weight: 600;
  }
  .chip-n {
    font-size: 11px;
    color: var(--muted);
  }
  .chip-dot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }
  .panel {
    padding: 12px;
  }
  .dt-custom,
  .dt-preview {
    margin-left: 0;
  }
}

/* 会员到期（492）：行 = 标题行 + 日期行 + 剩余/周期进度条，右侧剩余天数与操作。
   497：列表一行两卡（手机单列），行上推送按钮撤下（抽屉里保留测试推送） */
.member-tip {
  font-size: 12px;
  line-height: 1.6;
  color: var(--muted);
  padding: 2px 2px 10px;
}
.member-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}
.member-grid .rem-row {
  margin-bottom: 0;
}
.member-row {
  cursor: pointer;
}
.member-row .row-title {
  flex-wrap: wrap;
}
.member-row.dragging {
  opacity: 0.5;
}
.member-row.drag-over {
  border-color: var(--brand);
  box-shadow: 0 0 0 1px var(--brand) inset;
}
.member-row .row-inner {
  align-items: center;
}
.member-bar {
  margin-top: 7px;
}
.member-left {
  flex: none;
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  color: var(--text-2);
}
.member-left.danger {
  color: var(--el-color-danger);
}
.member-ops {
  flex: none;
  display: inline-flex;
  gap: 6px;
}
.member-row.expired .row-title-text {
  text-decoration: line-through;
  color: var(--muted);
}
@media (max-width: 640px) {
  .member-grid {
    grid-template-columns: 1fr;
  }
}
</style>

<style>
/* RemTree 为函数式组件（含递归的子任务行），样式不能 scoped，全局挂一次。
   .rem-branch = 行 + 展开的子任务块（保持块级嵌套，子块缩进逐级累积） */
.rem-row {
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-bottom: 6px;
  background: var(--bg);
}
.rem-row.overdue {
  border-color: var(--crit);
}
.rem-row.done {
  opacity: 0.72;
}
/* 子任务嵌套块：每级缩进 22px（层层嵌套自动累积） */
.rem-kids {
  margin-left: 22px;
}
/* 行首子任务展开箭头（无子任务时占位对齐） */
.rem-row .kid-arrow {
  flex: none;
  width: 20px;
  margin-top: 3px;
  border: none;
  background: none;
  padding: 0;
  font-size: 18px;
  line-height: 1;
  color: var(--muted);
  cursor: pointer;
  transition: transform 0.15s;
}
.rem-row .kid-arrow.open {
  transform: rotate(90deg);
}
.rem-row .kid-arrow.ghost {
  cursor: default;
}
.rem-row .row-inner {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 9px 12px;
}
.rem-row .check {
  flex: none;
  width: 18px;
  height: 18px;
  margin-top: 2px;
  border-radius: 50%;
  border: 1.5px solid var(--brand);
  background: transparent;
  color: #fff;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 0;
}
.rem-row .check:hover {
  background: var(--brand);
}
.rem-row .check.checked {
  border-color: var(--muted);
  background: var(--muted);
  cursor: pointer;
}
.rem-row .check.checked:hover {
  border-color: var(--brand);
  background: var(--brand);
}
.rem-row .row-main {
  flex: 1;
  min-width: 0;
  cursor: pointer;
}
.rem-row .row-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  color: var(--ink);
}
.rem-row .row-title.is-done {
  text-decoration: line-through;
  color: var(--muted);
}
.rem-row .row-time {
  font-size: 12px;
  font-weight: 600;
  color: var(--brand);
  font-variant-numeric: tabular-nums;
}
.rem-row .row-note {
  font-size: 12px;
  color: var(--muted);
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.rem-row .note-more {
  color: var(--brand);
  cursor: pointer;
  margin-left: 6px;
  white-space: nowrap;
}
.rem-row .note-more:hover {
  text-decoration: underline;
}
.rem-row .row-badge {
  flex: none;
  font-size: 11px;
  color: var(--muted);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 0 5px;
  line-height: 16px;
  white-space: nowrap;
}
.rem-row .row-badge.danger {
  color: var(--crit);
  border-color: var(--crit);
}
.rem-row .badge-ico {
  margin-right: 2px;
}
.rem-row .star {
  flex: none;
  border: none;
  background: none;
  font-size: 16px;
  line-height: 1;
  padding: 2px 4px;
  margin-top: 1px;
  border-radius: 4px;
  color: var(--border);
  cursor: pointer;
}
.rem-row .star:hover {
  color: #e6a23c;
}
.rem-row .star.on {
  color: #e6a23c;
}
/* ---- 回收站行（复用 RemRow 的行内样式，操作换恢复/彻底删除） ---- */
.trash-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
}
.trash-tip {
  font-size: 12px;
  color: var(--muted);
}
.rem-row .trash-inner {
  align-items: center;
}
.rem-row .trash-inner .el-button + .el-button {
  margin-left: 0;
}
/* 移动端窄屏：标题行允许换行（徽标挤到下一行），防止长标题+徽标撑宽整行 */
@media (max-width: 768px) {
  .rem-row .row-title {
    flex-wrap: wrap;
  }
}
</style>
