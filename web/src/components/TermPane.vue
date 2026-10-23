<template>
  <div ref="hostEl" class="term-pane">
    <!-- 触屏设备：辅助键条（Esc/Tab/Ctrl 粘滞/方向键）+ 复制粘贴 -->
    <div v-if="isTouch" class="term-toolbar">
      <button v-for="k in keyBtns" :key="k.label" type="button" :class="{ on: k.sticky && ctrlArmed }" @click="tapKey(k)">{{ k.label }}</button>
      <span class="tb-spacer"></span>
      <button type="button" class="tb-wide" @click="copySelection">复制</button>
      <button type="button" class="tb-wide" @click="pasteFromClipboard">粘贴</button>
    </div>
    <div ref="termEl" class="term-host" @contextmenu="onContextMenu"></div>
    <!-- 底部服务器信息栏：最新监控快照（30s 轮询），跟随明暗主题；
         网络/磁盘读写为上下双行（同数据监控格子的方向色：上行写橙、下行读蓝） -->
    <div class="term-info">
      <template v-if="info">
        <div v-for="m in infoItems" :key="m.label" class="ti-item">
          <span class="ti-label">{{ m.label }}</span>
          <div v-if="m.net" class="ti-duo">
            <div class="ti-duo-row">
              <el-icon class="up"><Top /></el-icon>
              <span>{{ m.net.tx }}</span>
            </div>
            <div class="ti-duo-row">
              <el-icon class="down"><Bottom /></el-icon>
              <span>{{ m.net.rx }}</span>
            </div>
          </div>
          <div v-else-if="m.dio" class="ti-duo">
            <div class="ti-duo-row">
              <span class="ti-tag up">写</span>
              <span>{{ m.dio.w }}</span>
            </div>
            <div class="ti-duo-row">
              <span class="ti-tag down">读</span>
              <span>{{ m.dio.r }}</span>
            </div>
          </div>
          <template v-else>
            <span class="ti-val" :class="m.cls || ''">{{ m.val }}</span>
            <div v-if="m.pct != null" class="ti-bar">
              <i :style="{ width: barWidth(m.pct), background: barColor(m.pct) }" />
            </div>
          </template>
        </div>
      </template>
      <span v-else class="ti-none">暂无监控数据</span>
    </div>
    <!-- 命令候选弹出层（IDE 下拉风格）：纯 DOM 浮层锚在光标附近，
         ↑↓ 选候选、→ 采纳、Esc 关闭后再 ↑↓ 召回 shell 历史；Tab 透传原生补全，不碰终端数据流与光标 -->
    <div v-if="sugOpen" class="sug-pop" :style="sugStyle">
      <div
        v-for="(s, i) in sugList"
        :key="s.cmd"
        class="sug-item"
        :class="{ sel: i === sugSel }"
        @mousedown.prevent
        @click="sugAccept(s.cmd)"
      >
        {{ s.cmd }}
      </div>
      <div class="sug-foot">↑↓ 选择 · → 采纳 · Esc 关闭</div>
    </div>
    <Transition name="fade">
      <div v-if="state !== 'open'" class="term-mask">
        <template v-if="state === 'connecting'">
          <el-icon class="is-loading"><Loading /></el-icon>
          <span>正在连接 {{ server.name }}…</span>
        </template>
        <template v-else>
          <span class="mask-msg">{{ maskMsg }}</span>
          <div class="mask-btns">
            <el-button size="small" type="primary" plain @click="connect">重新连接</el-button>
            <el-button
              v-if="state === 'error' && errCode === 'host_key_mismatch'"
              size="small" type="warning" plain :loading="resettingFp" @click="resetHostKey"
            >重置指纹并重连</el-button>
            <el-button v-if="state === 'error'" size="small" plain @click="emit('auth-error')">输入密码重连</el-button>
          </div>
        </template>
      </div>
    </Transition>
  </div>
</template>

<script setup>
// 单个终端 tab：xterm 实例 + WebSocket 生命周期。
// 协议：文本 JSON 控制帧（auth/resize/ping ↔ ready/pong/error/closed），
// 二进制 = 原始终端字节流。父组件用 v-show 切换 tab（不销毁会话），
// 激活时 nextTick fit 校准尺寸；断线显示遮罩可重连。
//
// 复制粘贴（桌面）：拖选后 Ctrl+Shift+C / Cmd+C 复制，Ctrl+Shift+V / Cmd+V / Ctrl+V
// 走原生 paste 事件（容器捕获阶段拦截，http 环境不依赖 clipboard API），
// 右键有选区=复制、无选区=粘贴。多行粘贴先弹确认（防粘贴即执行的误操作）。
// 触屏：顶部辅助键条 + 输入法粘贴气泡（同样被捕获拦截后转发）。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Bottom, Top } from '@element-plus/icons-vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { api, getToken } from '../api/index.js'
import { fmtBps, fmtDiskBps, fmtNum, fmtPercent, fmtUptimeDays, pickNets, utilClass } from '../utils/format.js'
import { barColor, NET_UP_COLOR } from '../utils/chartTheme.js'
import { COMMON_COMMANDS } from '../utils/termSuggest.js'
import { attachLogHighlight } from '../utils/termLogHighlight.js'

const props = defineProps({
  // { id, name, cred: {username,password,save}|null, credSeq: number }
  server: { type: Object, required: true },
  active: { type: Boolean, default: false },
})
const emit = defineEmits(['status', 'auth-error']) // status: connecting | open | closed | error（附错误文本）

const termEl = ref(null)
const hostEl = ref(null)
const state = ref('connecting')
const maskMsg = ref('')
const errCode = ref('') // 机器可读错误码（host_key_mismatch 时遮罩显示重置指纹入口，376）
const resettingFp = ref(false)

let term = null
let fit = null
let ws = null
let pingTimer = null
let ro = null
let resizeTimer = null
let logHL = null
let disposed = false
let retryTimer = null
let retryDelay = 0

// pane 实例标识：WS 意外断开时服务端按此挂起会话（SSH 保活 + 输出缓冲），
// 同 sid 重连即续接重放。keep-alive 切菜单/切窗口不换 id；页面刷新换新 id，
// 旧会话由服务端超时（10 分钟）回收。
const sid = crypto?.randomUUID
  ? crypto.randomUUID()
  : `pane-${Date.now()}-${Math.random().toString(36).slice(2)}`

// 触屏（手机/平板）显示辅助键条；带触控的笔记本（细指针）不显示
const isTouch = typeof matchMedia !== 'undefined' && matchMedia('(pointer: coarse)').matches
const ctrlArmed = ref(false) // 工具条 Ctrl 粘滞开关：下一个字母键以 Ctrl+<key> 发送

// ---------- 底部服务器信息栏（最新监控快照，30s 轮询） ----------
const info = ref(null)
let infoTimer = null
async function loadInfo() {
  // 页签切走（v-show 隐藏）或浏览器后台时跳过本轮，回前台由 visibilitychange 补拉
  if (disposed || !props.active || document.visibilityState !== 'visible') return
  try {
    const res = await api.get('/servers')
    const s = (res.servers || []).find((x) => x.id === props.server.id)
    info.value = s?.latest || null
  } catch {
    /* 下轮再试 */
  }
}
// 快照 → 展示块：百分比块带迷你进度条（三档渐变同全站口径）、网络/磁盘读写
// 上下双行（方向色同数据监控格子：上行写橙、下行读蓝）；磁盘读写速率按设备
// 去重合计（同一块盘多挂载点不重复计），旧版 agent 无该字段显示 -
const infoItems = computed(() => {
  const L = info.value
  if (!L) return []
  const disks = L.disks || []
  const diskMax = disks.reduce((m, d) => Math.max(m, d.percent ?? 0), 0)
  const nets = pickNets(L.nets)
  const net = {
    rx: nets.reduce((a, n) => a + (n.rx_bps || 0), 0),
    tx: nets.reduce((a, n) => a + (n.tx_bps || 0), 0),
  }
  let dio = { r: '-', w: '-' }
  const withIO = disks.filter((d) => d.io_read_bps != null || d.io_write_bps != null)
  if (withIO.length) {
    let r = 0
    let w = 0
    const seen = new Set()
    for (const d of withIO) {
      const key = d.device || d.mount
      if (seen.has(key)) continue
      seen.add(key)
      r += d.io_read_bps || 0
      w += d.io_write_bps || 0
    }
    dio = { r: fmtDiskBps(r), w: fmtDiskBps(w) }
  }
  const items = [
    { label: 'CPU', val: fmtPercent(L.cpu_percent), cls: utilClass(L.cpu_percent), pct: L.cpu_percent },
    { label: '内存', val: fmtPercent(L.mem_percent), cls: utilClass(L.mem_percent), pct: L.mem_percent },
    { label: '磁盘', val: fmtPercent(diskMax), cls: utilClass(diskMax), pct: diskMax },
  ]
  if (L.swap_percent > 0) {
    items.push({ label: 'Swap', val: fmtPercent(L.swap_percent), cls: utilClass(L.swap_percent), pct: L.swap_percent })
  }
  items.push(
    { label: '负载', val: fmtNum(L.load1) },
    { label: '网络', net: { tx: fmtBps(net.tx), rx: fmtBps(net.rx) } },
    { label: '磁盘读写', dio },
    { label: '运行', val: fmtUptimeDays(L.uptime) },
  )
  return items
})
function barWidth(pct) {
  return `${Math.max(0, Math.min(pct || 0, 100))}%`
}
const keyBtns = [
  { label: 'Esc', data: '\x1b' },
  { label: 'Tab', data: '\t' },
  { label: 'Ctrl', sticky: true },
  { label: '↑', data: '\x1b[A' },
  { label: '↓', data: '\x1b[B' },
  { label: '←', data: '\x1b[D' },
  { label: '→', data: '\x1b[C' },
]

function setStatus(s) {
  state.value = s
  emit('status', s, maskMsg.value) // 附错误文本：父组件重开凭据框时展示
}

// 统一发送：必须是二进制帧（文本帧保留给 JSON 控制帧，服务端对非 JSON 文本静默丢弃）
function sendText(t) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(new TextEncoder().encode(t))
  }
}

// 重置 TOFU 指纹并立即重连（376）：目标机重装后指纹变更会被拒，此前只能去
// 「编辑服务器」勾选重置；现在失败遮罩上直接做。部分更新只带 reset_host_key，
// 其余字段服务端按缺省不动。
async function resetHostKey() {
  resettingFp.value = true
  try {
    await api.put(`/servers/${props.server.id}`, { reset_host_key: true })
    errCode.value = ''
    maskMsg.value = '指纹已重置，正在重新学习并连接…'
    connect()
  } catch (e) {
    ElMessage.error(e.message || '重置失败')
  } finally {
    resettingFp.value = false
  }
}

function connect() {
  if (retryTimer) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  closeWs()
  setStatus('connecting')
  maskMsg.value = ''
  errCode.value = ''
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/api/v1/term/ws?server_id=${props.server.id}`)
  ws.binaryType = 'arraybuffer'

  ws.onopen = () => {
    const { cols, rows } = safeSize()
    // 服务器未配齐凭据时带上本次输入的覆盖值（连接成功后服务端按 save 落库）
    const cred = props.server.cred || {}
    ws.send(
      JSON.stringify({
        type: 'auth',
        token: getToken(),
        sid,
        cols,
        rows,
        username: cred.username || '',
        password: cred.password || '',
        save_password: !!cred.save,
      }),
    )
    startPing()
  }
  ws.onmessage = (ev) => {
    if (typeof ev.data === 'string') {
      let f
      try {
        f = JSON.parse(ev.data)
      } catch {
        return
      }
      switch (f.type) {
        case 'ready':
          retryDelay = 0 // 连上即复位退避
          setStatus('open')
          doFit()
          syncSize() // auth 帧尺寸可能还是构造默认 80×24（握手快于首次 fit/挂载时隐藏跳过校准），会话存活即刻补齐
          break
        case 'error':
          maskMsg.value = f.msg || '连接失败'
          errCode.value = f.code || ''
          setStatus('error')
          break
        case 'closed':
          maskMsg.value = closedText(f.reason)
          setStatus('closed')
          break
        // pong：忽略（读超时/活跃判定由服务端负责）
      }
      return
    }
    term?.write(new Uint8Array(ev.data))
  }
  ws.onclose = () => {
    stopPing()
    if (disposed) return
    // error 帧（凭据/SSH 拨号失败）与 closed 帧（服务端终态）不自动重连
    if (state.value === 'error' || state.value === 'closed') return
    // 会话中途断开：服务端已挂起（SSH 保活 + 输出缓冲），退避自动重连续接；
    // 同 sid 重连成功后积压输出重放、会话无感续上
    const wasOpen = state.value === 'open'
    retryDelay = retryDelay ? Math.min(retryDelay * 2, 10000) : 1000
    maskMsg.value = wasOpen ? '连接中断，会话保活中，正在重连…' : '连接断开，正在重连…'
    setStatus('closed')
    retryTimer = setTimeout(connect, retryDelay)
  }
}

function closedText(reason) {
  switch (reason) {
    case 'idle':
      return '已断开：30 分钟无操作'
    case 'kicked':
      return '已断开：登录会话失效（登出或修改密码）'
    case 'ssh-closed':
      return '远程主机关闭了连接'
    case 'shutdown':
      return '监控服务端正在重启'
    case 'detached':
      return '会话已回收：连接中断超过 10 分钟'
    default:
      return '连接已关闭'
  }
}

function startPing() {
  stopPing()
  pingTimer = setInterval(() => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'ping' }))
    }
  }, 30000)
}
function stopPing() {
  if (pingTimer) {
    clearInterval(pingTimer)
    pingTimer = null
  }
}

function closeWs() {
  stopPing()
  if (ws) {
    ws.onclose = null
    ws.onmessage = null
    try {
      ws.close()
    } catch {
      /* 已关闭 */
    }
    ws = null
  }
}

// ---------- 复制 / 粘贴 ----------

// 复制当前选区：优先 clipboard API（https/localhost），http 环境回退隐藏 textarea + execCommand
function copySelection() {
  if (!term || !term.hasSelection()) {
    ElMessage.info('没有选中的内容')
    return
  }
  const text = term.getSelection()
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).then(
      () => ElMessage.success('已复制'),
      () => legacyCopy(text),
    )
  } else {
    legacyCopy(text)
  }
}

function legacyCopy(text) {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.cssText = 'position:fixed;left:-9999px;top:0'
  document.body.appendChild(ta)
  ta.focus()
  ta.select()
  const ok = document.execCommand('copy')
  ta.remove()
  term?.focus()
  ok ? ElMessage.success('已复制') : ElMessage.error('复制失败，请改用浏览器复制')
}

// 统一粘贴入口：换行统一为 \r；多行内容先确认（回车会自动逐行执行，防误操作）
async function pasteText(text) {
  if (!text) return
  text = text.replace(/\r\n/g, '\r').replace(/\n/g, '\r')
  const lines = text.split('\r').filter((l) => l !== '').length
  if (text.includes('\r')) {
    try {
      await ElMessageBox.confirm(
        `粘贴内容共 ${lines} 行，其中回车会自动执行命令，确认粘贴？`,
        '多行粘贴',
        { type: 'warning', confirmButtonText: '粘贴', cancelButtonText: '取消' },
      )
    } catch {
      return
    }
  }
  sendText(text)
  term?.focus()
}

// 主动读剪贴板（工具条/右键入口）：clipboard API 不可用（http）时提示走输入法粘贴
function pasteFromClipboard() {
  if (navigator.clipboard?.readText) {
    navigator.clipboard
      .readText()
      .then((t) => pasteText(t))
      .catch(() => {
        term?.focus()
        ElMessage.info('浏览器不允许读取剪贴板，请点击终端后用输入法的粘贴')
      })
  } else {
    term?.focus()
    ElMessage.info('当前环境不支持读取剪贴板，请点击终端后用输入法的粘贴')
  }
}

// 原生 paste 事件拦截（捕获阶段，先于 xterm 自身监听）：桌面 Ctrl+V/Cmd+V 与
// 手机输入法「粘贴」气泡都走这里，统一换行归一 + 多行确认
function onPasteEvent(ev) {
  if (state.value !== 'open') return
  ev.preventDefault()
  ev.stopPropagation()
  const text = ev.clipboardData?.getData('text') ?? ''
  if (text) pasteText(text)
}

// 右键：有选区=复制，无选区=粘贴（PuTTY/FinalShell 惯例）
function onContextMenu(ev) {
  if (state.value !== 'open') return
  ev.preventDefault()
  if (term?.hasSelection()) copySelection()
  else pasteFromClipboard()
}

// auth 帧尺寸兜底：容器隐藏时 doFit 跳过、term 维持上次值（首次即 80×24 构造默认，
// cols 不会为 0，|| 80 仅无 term 的极端兜底），失真由 ready 后 doFit+syncSize 校准
function safeSize() {
  const cols = term?.cols || 80
  const rows = term?.rows || 24
  return { cols, rows }
}

// 工具条按键（粘滞 Ctrl 组合/方向键等）：不影响本地补全跟踪的命令才原样过
function tapKey(k) {
  if (k.sticky) {
    ctrlArmed.value = !ctrlArmed.value
    return
  }
  if (k.data) {
    if (k.data === '\x03') {
      // Ctrl+C：清空本地行跟踪
      hideSuggest()
      lineBuf = ''
      atEnd = true
    } else if (k.data.startsWith('\x1b')) {
      // 方向键：放弃跟踪（触屏键条不参与弹层导航）
      hideSuggest()
      lineBuf = ''
    }
    sendText(k.data)
  }
}

function doFit() {
  if (!fit || !hostEl.value || hostEl.value.clientWidth === 0) return
  try {
    fit.fit()
  } catch {
    /* 布局竞态时忽略，下次触发重试 */
  }
}

// 把 xterm 当前尺寸同步给服务端 PTY。唯一发送点抽公共函数：ready 后 fit 完补发 +
// scheduleFit 防抖回调共用——PTY 与渲染宽度不一致时 top/vim 等全屏程序必然错位
function syncSize() {
  if (ws && ws.readyState === WebSocket.OPEN && term) {
    ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
  }
}

// 面板折叠/窗口缩放/字体环境变化都会改容器尺寸，统一 ResizeObserver + debounce
function scheduleFit() {
  if (resizeTimer) clearTimeout(resizeTimer)
  resizeTimer = setTimeout(() => {
    resizeTimer = null
    doFit()
    syncSize()
  }, 150)
}

onMounted(() => {
  histLoad()
  term = new Terminal({
    fontSize: 14,
    fontFamily: 'Menlo, Monaco, "Courier New", monospace',
    cursorBlink: true,
    scrollback: 99999,
    theme: {
      background: '#1b1f24',
      foreground: '#d7dae0',
      cursor: '#d7dae0',
      selectionBackground: '#3a405d',
      // 显式定义 ANSI 16 色调色板（One Dark 系，配深底），保证 ls/vim 等彩色输出稳定渲染
      black: '#2a2f36',
      red: '#e06c75',
      green: '#98c379',
      yellow: '#e5c07b',
      blue: '#61afef',
      magenta: '#c678dd',
      cyan: '#56b6c2',
      white: '#d7dae0',
      brightBlack: '#5c6370',
      brightRed: '#e8868e',
      brightGreen: '#a9d48b',
      brightYellow: '#efd08e',
      brightBlue: '#7ec2f5',
      brightMagenta: '#d896e8',
      brightCyan: '#6fc7d4',
      brightWhite: '#f0f2f5',
    },
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(termEl.value)
  term.onData((d) => {
    if (ctrlArmed.value && d.length === 1) {
      const up = d.toUpperCase()
      if (up >= 'A' && up <= 'Z') {
        ctrlArmed.value = false
        hideSuggest()
        lineBuf = ''
        atEnd = true
        sendText(String.fromCharCode(up.charCodeAt(0) - 64)) // Ctrl+字母 → 控制字符
        return
      }
    }
    sendText(handleInput(d))
  })
  // 有选区时拦截复制组合键（Mac Cmd+C / Win|Linux Ctrl+Shift+C），否则放行为 ^C
  term.attachCustomKeyEventHandler((ev) => {
    if (ev.type !== 'keydown') return true
    const combo =
      (ev.metaKey && !ev.ctrlKey && ev.code === 'KeyC') || (ev.ctrlKey && ev.shiftKey && ev.code === 'KeyC')
    if (combo && term.hasSelection()) {
      ev.preventDefault()
      copySelection()
      return false
    }
    return true
  })
  // 捕获阶段拦截 paste（xterm 在 element/textarea 上的监听不再触发）
  hostEl.value.addEventListener('paste', onPasteEvent, true)
  // 触屏点击终端即聚焦（弹出软键盘）；xterm 自身点击处理在部分移动浏览器不可靠
  hostEl.value.addEventListener(
    'touchend',
    () => {
      if (state.value === 'open') term?.focus()
    },
    { passive: true },
  )
  ro = new ResizeObserver(scheduleFit)
  ro.observe(hostEl.value)
  logHL = attachLogHighlight(term)
  loadInfo()
  infoTimer = setInterval(loadInfo, 30000)
  // 回前台：退避等待中的重连立即执行（后台定时器被浏览器节流，不必干等剩余倒计时），
  // 信息栏补拉一轮
  document.addEventListener('visibilitychange', onVisible)
  connect()
})

function onVisible() {
  if (disposed || document.visibilityState !== 'visible') return
  loadInfo()
  if (retryTimer) connect() // connect 内部先清 retryTimer，无双发
}

// tab 切回：v-show 恢复显示后容器尺寸就绪，校准并同步 PTY；信息栏补拉一轮
watch(
  () => props.active,
  (a) => {
    if (a) {
      nextTick(scheduleFit)
      loadInfo()
    }
  },
)

// 父组件换凭据重连（credSeq 递增 = 用户在凭据框提交了新值）
watch(
  () => props.server.credSeq,
  () => connect(),
)

onBeforeUnmount(() => {
  disposed = true
  if (resizeTimer) clearTimeout(resizeTimer)
  if (suggestTimer) clearTimeout(suggestTimer)
  if (retryTimer) clearTimeout(retryTimer)
  if (infoTimer) clearInterval(infoTimer)
  document.removeEventListener('visibilitychange', onVisible)
  ro?.disconnect()
  if (hostEl.value) {
    hostEl.value.removeEventListener('paste', onPasteEvent, true)
  }
  logHL?.dispose()
  closeWs()
  term?.dispose()
  clearTimeout(histTimer)
})

// ---------- 输入补全（命令候选弹出层）：历史命令（入库按服务器记忆，跨设备同步）+ 常用命令 ----------
// 弹层是纯 DOM 浮层（不往终端数据流写任何转义序列，回显快慢都不影响它），
// 只跟踪行尾输入（←/→ 移动光标或 Tab 原生补全后放弃跟踪，回车后重新开始）；
// ↑↓ 在候选间移动（弹层开着时不透传）、→ / 点击采纳、Esc 关闭、Tab 关弹层透传原生补全、其他键关闭后照常处理。
const HIST_MAX = 100
const histKey = `term-hist-${props.server.id}`
let hist = []
try {
  hist = JSON.parse(localStorage.getItem(histKey) || '[]')
  if (!Array.isArray(hist)) hist = []
} catch {
  hist = []
}
let lineBuf = ''
let atEnd = true // 光标是否在输入行尾（候选弹出层只在行尾给出）
const sugOpen = ref(false)
const sugList = ref([])
const sugSel = ref(0)
const sugStyle = ref({})

// 历史入库：服务端为权威（跨设备同步）；localStorage 作镜像兜底。
// 挂载时拉取——服务端有记录则覆盖本地；服务端为空而本地有旧记录则一次性迁移上传。
async function histLoad() {
  try {
    const { data } = await api.get(`/term/history`, { params: { server_id: props.server.id } })
    const items = Array.isArray(data?.items) ? data.items : []
    if (items.length) {
      hist = items
      try { localStorage.setItem(histKey, JSON.stringify(hist)) } catch { /* 忽略 */ }
    } else if (hist.length) {
      histSaveRemote()
    }
  } catch {
    /* 拉取失败沿用本地记录 */
  }
}
let histTimer = 0
function histSave() {
  try {
    localStorage.setItem(histKey, JSON.stringify(hist))
  } catch {
    /* 隐身模式等存储失败场景静默 */
  }
  clearTimeout(histTimer)
  histTimer = setTimeout(histSaveRemote, 800)
}
function histSaveRemote() {
  api.put('/term/history', { server_id: props.server.id, items: hist }).catch(() => {})
}
function histPush(cmd) {
  if (!cmd) return
  if (hist[0] === cmd) return
  const i = hist.indexOf(cmd)
  if (i >= 0) hist.splice(i, 1)
  hist.unshift(cmd)
  if (hist.length > HIST_MAX) hist.length = HIST_MAX
  histSave()
}
// 候选 = 本地历史 + 内置常用命令，对已输入前缀做「前缀优先、子串其次」匹配，
// 历史按最近使用优先；同条去重，最多弹 8 条
function findCandidates() {
  if (!lineBuf) return []
  const q = lineBuf.toLowerCase()
  const seen = new Set()
  const out = []
  const push = (c) => {
    const k = c.trim()
    if (!k || k === lineBuf || seen.has(k)) return
    const i = k.toLowerCase().indexOf(q)
    if (i < 0) return
    seen.add(k)
    out.push({ cmd: k, score: (i === 0 ? 0 : 1) * 100000 + out.length })
  }
  for (const c of hist) push(c)
  for (const c of COMMON_COMMANDS) push(c)
  out.sort((a, b) => a.score - b.score)
  return out.slice(0, 8)
}
function hideSuggest() {
  sugOpen.value = false
  sugList.value = []
  sugSel.value = 0
}
// 弹出层锚在光标下方；底部放不下（输入行通常在最底下）时翻到光标上方。
// 纯 DOM 浮层，不碰终端数据流与光标，回显快慢都影响不到它。
function refreshSuggest() {
  if (disposed || !term) return
  const list = findCandidates()
  if (!list.length || !atEnd || !term.element || !hostEl.value) {
    hideSuggest()
    return
  }
  const rowsEl = term.element.querySelector('.xterm-rows')
  if (!rowsEl) {
    hideSuggest()
    return
  }
  const rr = rowsEl.getBoundingClientRect()
  const pr = hostEl.value.getBoundingClientRect()
  const cw = rr.width / term.cols
  const ch = rr.height / term.rows
  const buf = term.buffer.active
  let left = rr.left - pr.left + buf.cursorX * cw
  const cursorTop = rr.top - pr.top + buf.cursorY * ch
  let top = cursorTop + ch + 2
  const popupH = Math.min(list.length, 8) * 27 + 10
  if (top + popupH > hostEl.value.clientHeight && cursorTop - popupH - 4 > 0) {
    top = cursorTop - popupH - 4
  }
  const hw = hostEl.value.clientWidth
  left = Math.max(4, Math.min(left, hw - 260))
  sugList.value = list
  sugSel.value = 0
  sugStyle.value = { left: `${Math.round(left)}px`, top: `${Math.round(top)}px` }
  sugOpen.value = true
}
// 采纳候选：剩余部分直接续发（远端已回显已输入前缀）；候选不是前缀延伸时
// 先按已输入长度发退格删掉再发整条
function sugAccept(cmd) {
  if (!cmd) return
  if (cmd.startsWith(lineBuf)) {
    sendText(cmd.slice(lineBuf.length))
  } else if (lineBuf) {
    sendText('\x7f'.repeat(lineBuf.length) + cmd)
  } else {
    sendText(cmd)
  }
  lineBuf = cmd
  hideSuggest()
}
let suggestTimer = null
// 打字后等一拍（回显把光标推到新位置）再定位弹层
function scheduleSuggest() {
  clearTimeout(suggestTimer)
  suggestTimer = setTimeout(() => {
    if (!disposed) refreshSuggest()
  }, 40)
}

// 输入分流：弹层开着时先吃掉导航键，其余更新行跟踪并返回应转发的字节（''=已就地处理）
// 方向键兼容两种形态：普通模式 \x1b[A/B/C 与 application 光标模式 \x1bOA/B/C
//（DECCKM，readline/全屏程序开启后 xterm 改发后者）
function handleInput(d) {
  if (sugOpen.value && sugList.value.length) {
    if (d === '\x1b[C' || d === '\x1bOC') {
      sugAccept(sugList.value[sugSel.value].cmd)
      return ''
    }
    if (d === '\x1b[A' || d === '\x1bOA') {
      sugSel.value = (sugSel.value - 1 + sugList.value.length) % sugList.value.length
      return ''
    }
    if (d === '\x1b[B' || d === '\x1bOB') {
      sugSel.value = (sugSel.value + 1) % sugList.value.length
      return ''
    }
    if (d === '\t') {
      // Tab 不采纳候选（259）：关弹层后放行，保持 shell 原生自动补全
      hideSuggest()
    }
    // ↑↓ 弹层开着时只导航候选（260 终版）；要召回 shell 历史先按 Esc 关弹层
    if (d === '\x1b') {
      hideSuggest()
      return ''
    }
    // 回车等其余按键：关弹层后按原逻辑继续（回车=直接执行当前行，不代采纳）
    hideSuggest()
  }
  if (d === '\r') {
    histPush(lineBuf.trim())
    lineBuf = ''
    atEnd = true
    return d
  }
  if (d === '\x7f') {
    if (lineBuf) {
      lineBuf = lineBuf.slice(0, -1)
      scheduleSuggest()
    }
    return d
  }
  if (d === '\x03' || d === '\x15') {
    lineBuf = ''
    atEnd = true
    return d
  }
  if (d === '\x1b[D') {
    atEnd = false
    return d
  }
  if (d === '\t' || d === '\x1b[A' || d === '\x1b[B' || d === '\x1bOA' || d === '\x1bOB') {
    // Tab（原生补全）与 ↑/↓（历史召回）之后行内容不可知，放弃跟踪。
    // 召回重绘会推动 buffer 滚动，视口若停在历史浏览位置强制回底，
    // 保证召回的命令始终可见（xterm 自带的 scrollOnUserInput 的兜底加固）
    lineBuf = ''
    atEnd = true
    term?.scrollToBottom()
    return d
  }
  if (d.startsWith('\x1b')) {
    // 其余转义序列（粘贴标记等）：放弃跟踪
    lineBuf = ''
    atEnd = true
    return d
  }
  if (d.charCodeAt(0) >= 32) {
    lineBuf += d
    if (atEnd && lineBuf.length <= term.cols) scheduleSuggest()
    return d
  }
  return d
}
</script>

<style scoped>
.term-pane {
  position: relative;
  width: 100%;
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.term-toolbar {
  flex: none;
  display: flex;
  gap: 4px;
  padding: 5px 6px;
  background: #232830;
  overflow-x: auto;
  scrollbar-width: none;
}
.term-toolbar::-webkit-scrollbar {
  display: none;
}
.term-toolbar button {
  flex: none;
  min-width: 38px;
  height: 30px;
  padding: 0 8px;
  border: 1px solid #3a414d;
  border-radius: 5px;
  background: #2b313c;
  color: #d7dae0;
  font-size: 13px;
  line-height: 1;
  cursor: pointer;
}
.term-toolbar button:active {
  background: #3a414d;
}
.term-toolbar button.on {
  background: #3d5a92;
  border-color: #5577b8;
}
.tb-wide {
  padding: 0 12px;
}
.tb-spacer {
  flex: 1;
}
.term-host {
  flex: 1;
  min-height: 0;
  padding: 4px 6px;
  box-sizing: border-box;
  background: #1b1f24;
}
/* 底部服务器信息栏：跟随明暗主题（亮=白底、暗=面板深底），指标块横排 */
.term-info {
  flex: none;
  height: 80px;
  box-sizing: border-box;
  display: flex;
  align-items: stretch;
  gap: 4px;
  padding: 6px 10px;
  background: var(--surface, #fff);
  border-top: 1px solid var(--border, #d4d3cc);
  overflow-x: auto;
  scrollbar-width: thin;
}
.ti-item {
  flex: 1 1 100px;
  min-width: 92px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
  padding: 0 8px;
  border-right: 1px solid var(--border, #d4d3cc);
}
.ti-item:last-child {
  border-right: none;
}
.ti-label {
  font-size: 11px;
  color: var(--muted, #909399);
}
.ti-val {
  font-size: 15px;
  font-weight: 600;
  color: var(--ink, #303133);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.ti-bar {
  height: 6px;
  border-radius: 3px;
  background: var(--border, #d4d3cc);
  overflow: hidden;
}
/* 紫罗兰：信息栏白透底叠在终端深底上发闷、muted 标签字看不清——换深紫实底提对比 */
html.dark.violet .term-info {
  background: rgba(32, 24, 66, 0.88);
  border-top-color: rgba(255, 255, 255, 0.13);
}
html.dark.violet .ti-label {
  color: #b3abd8;
}
.ti-bar i {
  display: block;
  height: 100%;
  border-radius: 3px;
}
/* 网络上下行 / 磁盘读写双行块：方向色同数据监控格子（上行写橙、下行读蓝） */
.ti-duo {
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.ti-duo-row {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  font-weight: 600;
  color: var(--ink, #303133);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  line-height: 1.2;
}
.ti-duo-row .el-icon {
  font-size: 11px;
  flex: none;
}
.ti-duo-row .down {
  color: var(--brand, #3d5a92);
}
.ti-duo-row .up {
  color: v-bind('NET_UP_COLOR');
}
.ti-tag {
  font-size: 10px;
  flex: none;
  width: 14px;
  text-align: center;
}
.ti-tag.down {
  color: var(--brand, #3d5a92);
}
.ti-tag.up {
  color: v-bind('NET_UP_COLOR');
}
.ti-none {
  margin: auto;
  font-size: 12px;
  color: var(--muted, #909399);
}
/* 命令候选弹出层：终端同系深底，选中行品牌蓝，等宽字体对齐终端字符 */
.sug-pop {
  position: absolute;
  z-index: 5;
  min-width: 180px;
  max-width: 420px;
  max-height: 246px;
  overflow-y: auto;
  background: #232830;
  border: 1px solid #3a414d;
  border-radius: 6px;
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.45);
  padding: 4px;
  user-select: none;
}
.sug-item {
  padding: 4px 10px;
  border-radius: 4px;
  font-family: Menlo, Monaco, 'Courier New', monospace;
  font-size: 14px;
  color: #d7dae0;
  white-space: pre;
  overflow: hidden;
  text-overflow: ellipsis;
  cursor: pointer;
}
.sug-item.sel {
  background: var(--brand, #3d5a92);
  color: #fff;
}
.sug-foot {
  padding: 4px 10px 2px;
  border-top: 1px solid #3a414d;
  margin-top: 3px;
  font-size: 11px;
  color: #5c6370;
  white-space: nowrap;
}
.term-mask {
  z-index: 10;
}
.term-mask {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  background: rgba(27, 31, 36, 0.92);
  color: #d7dae0;
  font-size: 13px;
}
.mask-msg {
  max-width: 80%;
  text-align: center;
  line-height: 1.6;
  white-space: pre-line;
}
.mask-btns {
  display: flex;
  gap: 10px;
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
