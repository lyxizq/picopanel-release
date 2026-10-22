<template>
  <div class="msgs">
    <el-tabs v-model="pageTab" class="msgs-tabs" @tab-change="onPageTab">
      <el-tab-pane label="消息" name="messages">
      <div class="toolbar">
        <el-radio-group v-model="msgType" size="small" @change="onMsgFilterChange">
          <el-radio-button value="">全部</el-radio-button>
          <el-radio-button value="alert">告警</el-radio-button>
          <el-radio-button value="reminder">提醒</el-radio-button>
          <el-radio-button value="inspect">巡检</el-radio-button>
          <el-radio-button value="login">登录</el-radio-button>
          <el-radio-button value="mail">邮件</el-radio-button>
          <el-radio-button value="tv">追剧</el-radio-button>
          <el-radio-button value="cert">证书</el-radio-button>
        </el-radio-group>
        <div class="tools">
          <el-button size="small" text type="primary" :disabled="!unread" @click="readAll">全部已读</el-button>
          <el-button size="small" text type="danger" :disabled="!msgTotal" @click="clearMessages">清空</el-button>
        </div>
      </div>

      <div v-loading="msgLoading" class="msg-list">
        <div
          v-for="m in msgs"
          :key="m.id"
          class="msg-row"
          :class="{ unread: !m.read_at, red: m.type === 'reminder' && !m.ref_done }"
          @click="openMsg(m)"
        >
          <span class="msg-dot" />
          <div class="msg-main">
            <div class="msg-row-title">
              <el-tag size="small" :type="msgMeta(m.type).type" class="msg-tag">
                {{ msgMeta(m.type).label }}
              </el-tag>
              <span class="msg-row-text">{{ m.title }}</span>
            </div>
            <div class="msg-row-time">{{ fmtTime(m.created_at) }}</div>
          </div>
          <el-icon class="msg-del" title="删除" @click.stop="delMsg(m)"><Close /></el-icon>
        </div>
        <div v-if="!msgLoading && !msgs.length" class="msg-empty">暂无消息</div>
      </div>

      <div class="msg-foot">
        <el-select v-model="msgPageSize" size="small" style="width: 92px" @change="onMsgFilterChange">
          <el-option v-for="n in [10, 20, 50]" :key="n" :label="`${n} 条/页`" :value="n" />
        </el-select>
        <el-pagination
          v-model:current-page="msgPage"
          size="small"
          layout="prev, pager, next"
          :total="msgTotal"
          :page-size="msgPageSize"
          @current-change="loadMessages"
        />
      </div>

      <!-- 消息明细：点列表行弹出，查看完整内容并标已读（append-to-body：463 口径） -->
      <el-dialog v-model="msgDetail" :title="msgCur.title || '消息明细'" width="min(780px, 94vw)" append-to-body>
        <div class="msg-detail-meta">
          <el-tag size="small" :type="msgMeta(msgCur.type).type">
            {{ msgMeta(msgCur.type).label }}
          </el-tag>
          <span>{{ fmtTime(msgCur.created_at) }}</span>
          <span v-if="msgCur.read_at" class="muted">已读</span>
          <span v-else class="unread-label">未读</span>
        </div>
        <div class="msg-detail-body">{{ msgCur.content || '（无正文）' }}</div>
        <template #footer>
          <el-button @click="msgDetail = false">关闭</el-button>
          <el-button v-if="!msgCur.read_at" type="primary" @click="markRead(msgCur)">标为已读</el-button>
        </template>
      </el-dialog>
      </el-tab-pane>
      <el-tab-pane label="告警" name="alerts" lazy>
        <AlertRulesView />
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup>
// 消息中心（543）：原 Layout 用户下拉里的弹窗改独立页面（/console/messages，
// 菜单项/顶栏下拉/主页统计卡等全部消息入口均跳本页）；类型筛选/分页/已读/
// 删除/清空，点行看明细并标已读；提醒类消息关联事项未完成时整行红字（RefDone
// 服务端联查回填）。未读数变动后广播 messages-changed，Layout 顶栏角标即时报。
defineOptions({ name: 'MessagesView' })
import { Close } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { onActivated, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AlertRulesView from './AlertRulesView.vue'
import { api } from '../api/index.js'
import { fmtTime } from '../utils/format.js'

// 类型标签文案/配色（列表行与明细弹层共用；未知类型回落告警样式）
const MSG_META = {
  reminder: { label: '提醒', type: 'warning' },
  inspect: { label: '巡检', type: 'primary' },
  login: { label: '登录', type: 'success' },
  mail: { label: '邮件', type: 'info' },
  alert: { label: '告警', type: 'danger' },
  membership: { label: '会员', type: 'warning' },
  tv: { label: '追剧', type: 'warning' },
  cert: { label: '证书', type: 'warning' },
}
const msgMeta = (t) => MSG_META[t] || MSG_META.alert

const msgLoading = ref(false)
const msgs = ref([])
const msgType = ref('')
const msgPage = ref(1)
const msgPageSize = ref(10)
const msgTotal = ref(0)
const unread = ref(0)
const msgDetail = ref(false)
const msgCur = ref({})

// 顶栏未读角标即时同步（Layout 监听）
function syncUnread() {
  window.dispatchEvent(new CustomEvent('messages-changed'))
}

async function loadMessages() {
  msgLoading.value = true
  try {
    const q = `?page=${msgPage.value}&page_size=${msgPageSize.value}` + (msgType.value ? `&type=${msgType.value}` : '')
    const r = await api.get('/messages' + q)
    msgs.value = r.items || []
    msgTotal.value = r.total || 0
    unread.value = r.unread || 0
    syncUnread()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    msgLoading.value = false
  }
}
function onMsgFilterChange() {
  msgPage.value = 1
  loadMessages()
}
async function markRead(m) {
  if (m.read_at) return
  try {
    await api.post('/messages/read', { id: m.id })
    m.read_at = Math.floor(Date.now() / 1000)
    unread.value = Math.max(0, unread.value - 1)
    syncUnread()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function openMsg(m) {
  msgCur.value = m
  msgDetail.value = true
  await markRead(m)
}
async function readAll() {
  try {
    await api.post('/messages/read-all')
    ElMessage.success('已全部标为已读')
    loadMessages()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function delMsg(m) {
  try {
    await ElMessageBox.confirm('删除这条消息？（直接删库，不可恢复）', '删除消息', {
      type: 'warning',
      confirmButtonText: '删除',
    })
  } catch {
    return
  }
  try {
    await api.del(`/messages/${m.id}`)
    if (msgCur.value?.id === m.id) msgDetail.value = false
    loadMessages()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function clearMessages() {
  try {
    await ElMessageBox.confirm('清空消息中心全部消息？（直接删库，不可恢复）', '清空消息', {
      type: 'warning',
      confirmButtonText: '清空',
    })
  } catch {
    return
  }
  try {
    const res = await api.del('/messages')
    ElMessage.success(`已清空 ${res.cleared ?? ''} 条消息`.trim())
    msgDetail.value = false
    msgPage.value = 1
    loadMessages()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 消息/告警两 tab（556）：告警=AlertRulesView 薄壳（历史+规则子 tab 自带）；
// ?view=alerts 直达告警 tab（旧 /console/alert-rules redirect 落点），view 命名
// 避开 AlertRulesView 自读的 query.tab 语义（492 ?view=member 先例）
const route = useRoute()
const router = useRouter()
const pageTab = ref(route.query.view === 'alerts' ? 'alerts' : 'messages')
function onPageTab(name) {
  router.replace({ query: name === 'alerts' ? { view: 'alerts' } : {} })
}

onMounted(() => loadMessages())
onActivated(() => loadMessages())
</script>

<style scoped>
.msgs-tabs :deep(.el-tabs__header) {
  margin-bottom: 10px;
}
.msgs {
  display: flex;
  flex-direction: column;
  gap: 12px;
  /* 列表行通栏形态贴边感明显（545）：在 .main 6px 之上再补两侧留白，手机端收窄 */
  padding: 2px 12px;
}
@media (max-width: 768px) {
  .msgs {
    padding: 0 2px;
  }
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}
.tools {
  display: flex;
  align-items: center;
  gap: 12px;
}

.msg-list {
  min-height: 120px;
  display: flex;
  flex-direction: column;
}
.msg-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 8px 10px;
  margin-bottom: 6px;
  border: 1px solid var(--border);
  border-radius: 8px;
  cursor: pointer;
}
.msg-row:hover {
  background: var(--bg);
}
.msg-dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-top: 7px;
  background: transparent;
}
.msg-row.unread .msg-dot {
  background: var(--brand);
}
.msg-main {
  min-width: 0;
  flex: 1;
}
/* 行尾删除钮：悬停行才显示，点击不触发行打开 */
.msg-del {
  flex: none;
  margin-top: 3px;
  font-size: 14px;
  color: var(--muted);
  border-radius: 4px;
  padding: 2px;
  visibility: hidden;
  cursor: pointer;
}
.msg-row:hover .msg-del {
  visibility: visible;
}
.msg-del:hover {
  color: var(--crit, #d92d20);
  background: var(--bg);
}
.msg-row-title {
  display: flex;
  align-items: center;
  gap: 6px;
}
.msg-tag {
  flex: none;
}
.msg-row-text {
  font-size: 14px;
  color: var(--ink);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
/* 提醒类消息关联事项未完成 → 整行红字（完成后服务端联查自动复原） */
.msg-row.red .msg-row-text,
.msg-row.red .msg-row-time {
  color: var(--crit);
}
.msg-row-time {
  font-size: 13px;
  color: var(--muted);
  margin-top: 2px;
}
.msg-empty {
  text-align: center;
  color: var(--muted);
  padding: 28px 0;
}
.msg-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px solid var(--border);
  padding-top: 8px;
}
/* 消息明细弹层 */
.msg-detail-meta {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--muted);
  font-size: 14px;
  margin-bottom: 10px;
}
.msg-detail-meta .muted {
  color: var(--muted);
}
.msg-detail-meta .unread-label {
  color: var(--brand);
}
.msg-detail-body {
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 14px;
  line-height: 1.7;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 10px 12px;
  max-height: 50vh;
  overflow-y: auto;
}
</style>
