<script setup>
// 邮件（316-318）：三栏收发台——左=账户+文件夹，中=信头列表（搜索/缓存兜底），
// 右=阅读（iframe sandbox+HTML 邮件沙箱渲染、远程图片/脚本默认屏蔽、附件条）。
// 写信弹窗（回复/转发引用原文，附件随表单上传，服务端组包 SMTP 即弃）。
import { computed, defineOptions, nextTick, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { EditPen, Refresh } from '@element-plus/icons-vue'
import { api, apiForm, BASE, getToken } from '../api/index.js'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { useSidePanel } from '../composables/useSidePanel.js'
import { useIsMobile } from '../composables/useIsMobile.js'

defineOptions({ name: 'MailView' })

// ---- 账户与文件夹 ----
const accounts = ref([])
const maxAccounts = ref(8)
const curAcctId = ref(0)
const curAcct = computed(() => accounts.value.find((a) => a.id === curAcctId.value) || null)
const folders = ref([])
const curFolder = ref('INBOX')

// ---- 列表与阅读 ----
const headers = ref([])
const search = ref('')
const listLoading = ref(false)
const loadingMore = ref(false)
const refreshing = ref(false) // 手动刷新（连服务器拉新）
const hasMore = ref(false)
const msg = ref(null)
const msgLoading = ref(false)
const msgDlg = ref(false) // 邮件详情弹层（11）：双击列表行打开，与右栏阅读共用 msg 状态
const selectedUid = ref(0)
const sendAcctId = ref(0) // 写信/回复使用的发件账户（默认当前选中，可切换）

// 分栏（354 全站统一文档页口径）：左右两条分隔条接入 useSidePanel——拖拽调宽
// 180~480、单击收起/展开、localStorage 按名记忆；样式用全局 .side-divider
const sideL = useSidePanel('mail-left', 190)
const sideM = useSidePanel('mail-mid', 340)
const isMobile = useIsMobile()

// ---- 账户管理弹窗 ----
const acctDlg = ref(false)
const acctList = ref([])
const presets = ref([])
const acctForm = ref(null) // null=列表视图；对象=新建/编辑表单
const preset = ref('')
const testing = ref(false)
const savingAcct = ref(false)

// ---- 写信弹窗 ----
const compose = ref(null)
const sending = ref(false)
const composeRef = ref(null)

// ---- 通讯录（506）/ 个人签名（507） ----
const contacts = ref([])
const contactsDlg = ref(false)
const ctForm = ref(null) // null=列表视图；对象=新增/编辑表单
const ctSaving = ref(false)
const contactQuery = ref('') // 写信下拉当前过滤词（filter-method 写入）
const signature = ref('')
const sigDlg = ref(false)
const sigText = ref('')
const sigSaving = ref(false)
const quoteOpen = ref(true) // 写信弹窗引用原信折叠态（默认展开）

// 通讯录按姓名拼音序展示（空名靠后），下拉带出同序
const contactsSorted = computed(() =>
  [...contacts.value].sort((a, b) => {
    const an = a.name || '', bn = b.name || ''
    if (!an && bn) return 1
    if (an && !bn) return -1
    return an.localeCompare(bn, 'zh-CN') || a.email.localeCompare(b.email)
  }))
// 写信下拉按关键字过滤（姓名/邮箱包含，大小写不敏感；空词=全部）
const contactHits = computed(() => {
  const kw = contactQuery.value.trim().toLowerCase()
  if (!kw) return contactsSorted.value
  return contactsSorted.value.filter((ct) => ct.name.toLowerCase().includes(kw) || ct.email.toLowerCase().includes(kw))
})
function filterContacts(q) { contactQuery.value = q || '' }

async function loadAccounts(pickFirst = true) {
  const r = await api.get('/mail/accounts')
  accounts.value = r.accounts || []
  maxAccounts.value = r.max || 8
  if (pickFirst && !accounts.value.find((a) => a.id === curAcctId.value)) {
    curAcctId.value = accounts.value[0]?.id || 0
    // 文件夹与列表互不依赖（列表默认 INBOX），并行取省 1 个 RTT
    if (curAcctId.value) { await Promise.all([loadFolders(), loadList()]) }
    else { folders.value = []; headers.value = []; msg.value = null }
  }
}

async function switchAccount(a) {
  if (a.id === curAcctId.value) return
  curAcctId.value = a.id
  msg.value = null
  headers.value = []
  await loadFolders()
  curFolder.value = 'INBOX'
  await loadList()
}

async function loadFolders() {
  try {
    const r = await api.get(`/mail/accounts/${curAcctId.value}/folders`)
    folders.value = r.folders || []
  } catch (e) {
    folders.value = [{ name: 'INBOX' }]
    console.warn('文件夹加载失败', e.message)
  }
}

// 手机端阅读视图返回列表（关阅读态即回中栏清高亮）
function closeMsg() {
  msg.value = null
  selectedUid.value = 0
}

// 表单弹层未保存守卫（328）：列表视图无快照（baseline null）守卫自动放行
const acctGuard = useDirtyGuard(() => (acctForm.value ? { ...acctForm.value, preset: preset.value } : null))

// 写信弹层守卫：正文/收件人/主题/附件数量任一有实质内容即视为未保存
const composeGuard = useDirtyGuard(() => (compose.value ? {
  to: compose.value.to, cc: compose.value.cc, subject: compose.value.subject,
  body: compose.value.body, nfiles: compose.value.files.length,
} : null))

function fmtPoll(sec) {
  if (!sec || sec < 60) return '10 分钟'
  if (sec % 3600 === 0) return sec / 3600 + ' 小时'
  if (sec % 60 === 0) return sec / 60 + ' 分钟'
  return sec + ' 秒'
}

function folderLabel(f) {
  if (f.name === 'INBOX') return '收件箱'
  if (f.sent) return '已发送'
  if (f.trash) return '已删除'
  if (f.drafts) return '草稿箱'
  if (f.junk) return '垃圾邮件'
  return f.name
}

async function switchFolder(f) {
  curFolder.value = f.name
  msg.value = null
  search.value = ''
  await loadList()
}

async function loadList() {
  if (!curAcctId.value) return
  listLoading.value = true
  try {
    const term = search.value.trim()
    const q = `folder=${encodeURIComponent(curFolder.value)}&limit=50${term ? `&search=${encodeURIComponent(term)}` : ''}`
    const r = await api.get(`/mail/accounts/${curAcctId.value}/messages?${q}`)
    let list = r.headers || []
    if (term && !list.length) {
      // 服务端 SEARCH 无命中：回落客户端过滤最近 50 封（主题/发件人包含，大小写不敏感），
      // 兜住服务器 SEARCH 方言差异（163 Coremail 等）
      const all = await api.get(`/mail/accounts/${curAcctId.value}/messages?folder=${encodeURIComponent(curFolder.value)}&limit=50`)
      const kw = term.toLowerCase()
      list = (all.headers || []).filter((h) =>
        (h.subject || '').toLowerCase().includes(kw) ||
        (h.from_name || '').toLowerCase().includes(kw) ||
        (h.from_addr || '').toLowerCase().includes(kw))
    }
    headers.value = list
    hasMore.value = !!r.has_more && !term
  } catch (e) {
    console.warn('列表加载失败', e.message)
    headers.value = []
  } finally {
    listLoading.value = false
  }
}

// 手动刷新：清搜索、强制连 IMAP 拉最新（其余路径读本地库，2026-10-03 用户指定）。
// 刷新期间列表遮罩与图标转圈同步给反馈（2026-10-04 用户反馈点了没反应：
// IMAP 往返数秒内界面纹丝不动，看起来像没生效）
async function refreshList() {
  if (!curAcctId.value || refreshing.value) return
  refreshing.value = true
  listLoading.value = true
  search.value = ''
  try {
    const r = await api.get(`/mail/accounts/${curAcctId.value}/messages?folder=${encodeURIComponent(curFolder.value)}&limit=50&refresh=1`)
    headers.value = r.headers || []
    hasMore.value = !!r.has_more
  } catch (e) {
    ElMessage.error('刷新失败：' + e.message)
  } finally {
    refreshing.value = false
    listLoading.value = false
  }
}

// 加载更早的邮件（按 UID 向前翻页，追加到列表尾部）
async function loadMore() {
  if (loadingMore.value || !headers.value.length || search.value.trim()) return
  loadingMore.value = true
  try {
    const minUid = Math.min(...headers.value.map((h) => h.uid))
    const r = await api.get(`/mail/accounts/${curAcctId.value}/messages?folder=${encodeURIComponent(curFolder.value)}&limit=50&before_uid=${minUid}`)
    const older = r.headers || []
    headers.value = headers.value.concat(older)
    hasMore.value = !!r.has_more && older.length > 0
  } catch (e) {
    console.warn('加载更多失败', e.message)
  } finally {
    loadingMore.value = false
  }
}

let searchTimer = null
function onSearch() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(loadList, 400)
}

function fmtDate(unix) {
  if (!unix) return ''
  const d = new Date(unix * 1000)
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  if (d.toDateString() === new Date().toDateString()) return hm
  return `${d.getMonth() + 1}/${d.getDate()}`
}

function fmtSize(n) {
  if (!n) return ''
  if (n > 1 << 20) return (n / (1 << 20)).toFixed(1) + 'M'
  if (n > 1 << 10) return (n / (1 << 10)).toFixed(0) + 'K'
  return n + 'B'
}

// ---- 阅读 ----
async function openMsg(h) {
  selectedUid.value = h.uid
  msgLoading.value = true
  msg.value = null
  allowRemote.value = false
  try {
    const r = await api.get(`/mail/accounts/${curAcctId.value}/message?folder=${encodeURIComponent(curFolder.value)}&uid=${h.uid}`)
    msg.value = r
    if (!r.seen) {
      markSeen(h, true)
    }
  } catch (e) {
    ElMessageBox.alert(e.message, '打开失败')
  } finally {
    msgLoading.value = false
  }
}

// 双击弹层打开详情（11）：双击第一击已触发 openMsg 取信+自动已读，这里显式
// 再调一次保证无先行单击也成立（同信两次 GET 幂等，开销可忽略）
function openMsgDlg(h) {
  openMsg(h)
  msgDlg.value = true
}

// HTML 邮件沙箱（317）：iframe sandbox 禁脚本；CSP 默认屏蔽远程图片/字体（防追踪
// 像素），点「显示远程图片」后按信放行 img-src *（脚本仍禁），纯文本按 <pre> 渲染；
// meta referrer + iframe referrerpolicy 双保险禁发 Referer（344：放行图片不泄露面板地址）。
const allowRemote = ref(false)
const safeHtml = computed(() => {
  const imgPolicy = allowRemote.value ? 'img-src * data: blob:;' : 'img-src data: blob:;'
  const head = '<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer">'
    + `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; ${imgPolicy} font-src data:;">`
    + '<base target="_blank"><style>body{margin:12px;font:14px/1.6 system-ui,sans-serif;word-break:break-word;color:#222;background:#fff;}</style></head><body>'
  if (msg.value?.html) return head + msg.value.html + '</body></html>'
  const text = msg.value?.text || ''
  return head + '<pre style="white-space:pre-wrap;margin:0">' + text.replace(/[<>&]/g, (c) => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;' }[c])) + '</pre></body></html>'
})

async function markSeen(h, seen) {
  h.seen = seen
  const row = headers.value.find((x) => x.uid === h.uid)
  if (row) row.seen = seen
  try {
    await api.post(`/mail/accounts/${curAcctId.value}/seen`, { folder: curFolder.value, uid: h.uid, seen })
    // 账户未读角标乐观修正（372，服务端同步修缓存、这里就地反映；非收件箱不计）
    if (curFolder.value === 'INBOX') {
      const a = accounts.value.find((x) => x.id === curAcctId.value)
      if (a && typeof a.unread === 'number' && a.unread > 0) {
        a.unread = Math.max(0, a.unread + (seen ? -1 : 1))
      }
    }
  } catch { /* 静默：角标下一轮自愈 */ }
}

async function deleteMsg(h) {
  try {
    await ElMessageBox.confirm('删除这封邮件？（有垃圾桶先移入垃圾桶）', '删除邮件', { type: 'warning' })
  } catch { return }
  try {
    await api.post(`/mail/accounts/${curAcctId.value}/delete-msgs`, { folder: curFolder.value, uids: [h.uid] })
    if (msg.value?.uid === h.uid) msg.value = null
    await loadList()
  } catch (e) {
    ElMessageBox.alert(e.message, '删除失败')
  }
}

async function downloadAtt(att) {
  try {
    const res = await fetch(`${BASE}/mail/accounts/${curAcctId.value}/attachment?folder=${encodeURIComponent(curFolder.value)}&uid=${msg.value.uid}&part=${encodeURIComponent(att.path)}`,
      { headers: { Authorization: 'Bearer ' + getToken() } })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = att.name || 'attachment'
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 5000)
  } catch (e) {
    ElMessageBox.alert(e.message, '附件下载失败')
  }
}

// ---- 账户管理 ----
async function openAcctDlg() {
  acctDlg.value = true
  acctForm.value = null
  const [r1, r2] = await Promise.all([api.get('/mail/accounts'), api.get('/mail/presets')])
  acctList.value = r1.accounts || []
  presets.value = r2.presets || []
}

function newAcct() {
  preset.value = 'qq'
  applyPreset('qq')
  acctForm.value = { id: 0, name: '', email: '', password: '', imap_host: '', imap_port: 993, smtp_host: '', smtp_port: 465, smtp_ssl: true, poll_sec: 600 }
  acctGuard.snapshot()
}

function editAcct(a) {
  preset.value = a.provider || 'custom'
  acctForm.value = { id: a.id, name: a.name, email: a.email, password: '', imap_host: a.imap_host, imap_port: a.imap_port, smtp_host: a.smtp_host, smtp_port: a.smtp_port, smtp_ssl: a.smtp_ssl, poll_sec: a.poll_sec || 600 }
  acctGuard.snapshot()
}

function applyPreset(key) {
  const p = presets.value.find((x) => x.key === key)
  if (p && acctForm.value) {
    acctForm.value.imap_host = p.imap_host
    acctForm.value.imap_port = p.imap_port
    acctForm.value.smtp_host = p.smtp_host
    acctForm.value.smtp_port = p.smtp_port
    acctForm.value.smtp_ssl = p.smtp_ssl
  }
}

async function saveAcct() {
  const f = acctForm.value
  // 编辑态密码留空=沿用原密码（后端口径），仅新建必填；原先无差别 return 会把
  // 编辑态静默拦下（保存按钮点了没反应），连带改名等一切编辑都存不进去
  if (!f.email || (!f.id && !f.password)) {
    ElMessageBox.alert(!f.email ? '请填写邮箱地址' : '请填写密码/授权码', '提示')
    return
  }
  savingAcct.value = true
  try {
    const body = { name: f.name, email: f.email, provider: preset.value, imap_host: f.imap_host, imap_port: f.imap_port, smtp_host: f.smtp_host, smtp_port: f.smtp_port, smtp_ssl: f.smtp_ssl, password: f.password, poll_sec: f.poll_sec }
    if (f.id) await api.put(`/mail/accounts/${f.id}`, body)
    else await api.post('/mail/accounts', body)
    acctForm.value = null
    await loadAccounts(!f.id)
    await openAcctDlgRefresh()
  } catch (e) {
    ElMessageBox.alert(e.message, '保存失败')
  } finally {
    savingAcct.value = false
  }
}

async function openAcctDlgRefresh() {
  const r1 = await api.get('/mail/accounts')
  acctList.value = r1.accounts || []
}

async function testAcct() {
  const f = acctForm.value
  testing.value = true
  try {
    if (f.id) {
      await api.post(`/mail/accounts/${f.id}/test`, { password: f.password })
    } else {
      // 新建未落库：直接以表单值走一次 IMAP+SMTP 测试（借用 test 接口不便，走 create 前置校验口径——临时用 presets 无 test；这里用轻量连接测试接口不支持，直接提示保存即测）
      ElMessageBox.alert('新建账户保存时会自动测试连接，连接失败不会保存。', '提示')
      return
    }
    ElMessageBox.alert('连接成功', '测试结果')
  } catch (e) {
    ElMessageBox.alert(e.message, '测试失败')
  } finally {
    testing.value = false
  }
}

async function removeAcct(a) {
  try {
    await ElMessageBox.confirm(`删除账户 ${a.email}？（本地信头缓存一并清理，服务器邮件不受影响）`, '删除账户', { type: 'warning' })
  } catch { return }
  await api.del(`/mail/accounts/${a.id}`)
  if (a.id === curAcctId.value) { curAcctId.value = 0 }
  await loadAccounts(true)
  await openAcctDlgRefresh()
}

const orderSaving = ref(false)
async function moveAcct(a, dir) {
  const list = acctList.value
  const i = list.findIndex((x) => x.id === a.id)
  const j = i + dir
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j], list[i]]
  orderSaving.value = true
  try {
    await api.put('/mail/accounts/order', { ids: list.map((x) => x.id) })
    await loadAccounts(false)
  } finally { orderSaving.value = false }
}

// ---- 通讯录 ----
async function loadContacts() {
  try {
    const r = await api.get('/mail/contacts')
    contacts.value = r.contacts || []
  } catch (e) {
    console.warn('通讯录加载失败', e.message)
  }
}

function openContacts() {
  contactsDlg.value = true
  ctForm.value = null
  loadContacts()
}

async function saveContact() {
  const f = ctForm.value
  if (!f.email.trim()) {
    ElMessageBox.alert('请填写邮箱地址', '提示')
    return
  }
  ctSaving.value = true
  try {
    const body = { name: f.name, email: f.email }
    if (f.id) await api.put(`/mail/contacts/${f.id}`, body)
    else await api.post('/mail/contacts', body)
    ctForm.value = null
    await loadContacts()
  } catch (e) {
    ElMessageBox.alert(e.message, '保存失败')
  } finally {
    ctSaving.value = false
  }
}

async function delContact(ct) {
  try {
    await ElMessageBox.confirm(`删除联系人 ${ct.name || ct.email}？`, '删除联系人', { type: 'warning' })
  } catch { return }
  try {
    await api.del(`/mail/contacts/${ct.id}`)
    await loadContacts()
  } catch (e) {
    ElMessageBox.alert(e.message, '删除失败')
  }
}

// ---- 个人签名 ----
async function loadSignature() {
  try {
    const r = await api.get('/mail/signature')
    signature.value = r.signature || ''
  } catch { /* 签名读取失败按未配置处理 */ }
}

function openSigDlg() {
  sigText.value = signature.value
  sigDlg.value = true
}

async function saveSignature() {
  sigSaving.value = true
  try {
    await api.put('/mail/signature', { signature: sigText.value })
    signature.value = sigText.value
    sigDlg.value = false
  } catch (e) {
    ElMessageBox.alert(e.message, '保存失败')
  } finally {
    sigSaving.value = false
  }
}

// 签名拼到正文尾部（写信/回复/转发开窗时预填，可当场删改）
function withSig(body) {
  const sig = signature.value.trim()
  if (!sig) return body
  return body ? body.replace(/\s+$/, '') + '\n\n' + sig : sig
}

// ---- 写信 ----
function openCompose() {
  sendAcctId.value = curAcctId.value || (accounts.value[0]?.id || 0) // 默认当前选中账户
  quoteOpen.value = true
  compose.value = { to: [], cc: [], subject: '', body: withSig(''), files: [], inReplyTo: '', quote: '' }
  nextTick(() => composeRef.value?.focus?.())
}

function quoteHtml() {
  if (!msg.value) return ''
  const body = msg.value.html || `<pre style="white-space:pre-wrap">${(msg.value.text || '').replace(/[<>&]/g, (c) => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;' }[c]))}</pre>`
  return `<br><br><div style="border-left:2px solid #ccc;padding-left:8px;color:#666">在 ${new Date((msg.value.date_unix || 0) * 1000).toLocaleString()}，${msg.value.from_name || msg.value.from_addr} 写道：<br>${body}</div>`
}

function replyMsg(all = false) {
  if (!msg.value) return
  const m = msg.value
  sendAcctId.value = curAcctId.value // 用当前读信的账户发
  quoteOpen.value = true
  // 口径参考常规邮件客户端（329）：回复=发件人；全部回复=发件人+原收件人（to），
  // 原抄送（cc）进抄送栏；两处均剔除本机邮箱账户自身地址并按邮箱地址去重。
  // 显示名可能带尖括号，取址一律剥 <> 取纯地址，发信只填裸地址最稳。
  const own = accounts.value.map((a) => (a.email || '').toLowerCase()).filter(Boolean)
  const emailOf = (s) => {
    const t = (s || '').trim()
    const e = t.match(/<([^>]+)>/)
    return (e ? e[1] : t).trim().toLowerCase()
  }
  const addrList = (s) => (s || '').split(/[,;，；]/).map((x) => x.trim()).filter((x) => x.includes('@'))
  const fresh = (list, seen) => list.filter((a) => {
    const em = emailOf(a)
    if (!em || own.includes(em) || seen.has(em)) return false
    seen.add(em)
    return true
  })
  const seen = new Set()
  let to = fresh([m.from_addr], seen)
  if (!to.length && m.from_addr) to = [m.from_addr] // 回自己发的信等极端场景兜底
  let cc = []
  if (all) {
    to.push(...fresh(addrList(m.to), seen))
    cc = fresh(addrList(m.cc), seen)
  }
  const subj = m.subject || ''
  compose.value = {
    to, cc,
    subject: /^(re|回复)\s*[:：]/i.test(subj) ? subj : 'Re: ' + subj,
    body: withSig(''), files: [], inReplyTo: m.message_id || '', quote: quoteHtml(),
  }
  nextTick(() => composeRef.value?.focus?.())
}

function forwardMsg() {
  if (!msg.value) return
  sendAcctId.value = curAcctId.value
  quoteOpen.value = true
  compose.value = {
    to: [], cc: [],
    subject: (msg.value.subject || '').startsWith('Fwd:') ? msg.value.subject : 'Fwd: ' + (msg.value.subject || ''),
    body: withSig(`---------- 转发邮件 ----------\n发件人: ${msg.value.from_addr}\n主题: ${msg.value.subject || ''}\n\n${msg.value.text || ''}`),
    files: [], inReplyTo: '', quote: '',
  }
}

function onComposeFiles(e) {
  compose.value.files = Array.from(e.target.files || [])
}

async function sendCompose() {
  const c = compose.value
  if (!c.to.length || !c.subject.trim()) {
    ElMessageBox.alert('请填写收件人与主题')
    return
  }
  sending.value = true
  try {
    const fd = new FormData()
    fd.append('to', c.to.join(', '))
    fd.append('cc', c.cc.join(', '))
    fd.append('subject', c.subject)
    const bodyHtml = (c.body + (c.quote || '')).replace(/\n/g, '<br>')
    fd.append('html', `<div style="font:14px/1.7 system-ui">${bodyHtml}</div>`)
    fd.append('text', c.body)
    if (c.inReplyTo) fd.append('in_reply_to', c.inReplyTo)
    for (const f of c.files) fd.append('attachments', f, f.name)
    await apiForm(`/mail/accounts/${sendAcctId.value}/send`, fd)
    compose.value = null
    ElMessageBox.alert('发送成功', '邮件')
  } catch (e) {
    ElMessageBox.alert(e.message, '发送失败')
  } finally {
    sending.value = false
  }
}

// 引用原信在写信弹窗内直接预览（508）：正文 HTML 来自外部邮件不可信，沿用阅读页
// 同款 iframe 沙箱 + CSP（禁脚本/默认屏蔽远程图片），发送时仍按原口径附在正文后
const quoteDoc = computed(() => {
  const q = compose.value?.quote || ''
  if (!q) return ''
  return '<!doctype html><html><head><meta charset="utf-8"><meta name="referrer" content="no-referrer">'
    + '<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; style-src \'unsafe-inline\'; img-src data: blob:; font-src data:;">'
    + '<base target="_blank"><style>body{margin:8px;font:13px/1.6 system-ui,sans-serif;word-break:break-word;color:#555;background:transparent;}</style></head><body>'
    + q + '</body></html>'
})

onMounted(() => {
  loadAccounts(true)
  loadContacts()
  loadSignature()
})
</script>

<template>
  <div class="mailp" :class="{ reading: !!msg }">
    <!-- 左栏：账户 + 文件夹 -->
    <aside class="m-left" v-show="!sideL.collapsed" :style="{ width: sideL.width + 'px' }">
      <div class="m-accts">
        <div class="m-sec-title">邮箱</div>
        <div v-for="a in accounts" :key="a.id" :class="['m-acct', { active: a.id === curAcctId }]" @click="switchAccount(a)">
          <span class="m-acct-dot" :class="{ bad: a.last_error }" />
          <span class="m-acct-name">{{ a.name }}</span>
          <span v-if="a.unread > 0" class="m-acct-unread">{{ a.unread > 99 ? '99+' : a.unread }}</span>
        </div>
        <div v-if="!accounts.length" class="m-empty-tip">还没有邮箱账户</div>
        <div class="m-manage-row">
          <button type="button" class="m-manage" @click="openAcctDlg">管理账户</button>
          <button type="button" class="m-manage" @click="openContacts">通讯录</button>
        </div>
        <button type="button" class="m-manage" @click="openSigDlg">邮件签名</button>
      </div>
      <div class="m-folders">
        <div class="m-sec-title">文件夹</div>
        <div v-for="f in folders" :key="f.name" :class="['m-folder', { active: f.name === curFolder }]" @click="switchFolder(f)">
          {{ folderLabel(f) }}
        </div>
      </div>
    </aside>
    <div class="side-divider" :class="{ on: sideL.resizing }" title="拖拽调节宽度 · 单击收起/展开" @mousedown="sideL.startDrag" />

    <!-- 中栏：搜索 + 信头列表 -->
    <section class="m-mid" v-show="!sideM.collapsed || isMobile" :style="{ width: sideM.width + 'px' }">
      <div class="m-mobile-bar">
        <el-select :model-value="curAcctId" size="small" style="flex:1" @change="(id) => switchAccount(accounts.find((a) => a.id === id))">
          <el-option v-for="a in accounts" :key="a.id" :label="a.name" :value="a.id" />
        </el-select>
        <el-select v-model="curFolder" size="small" style="flex:1" @change="loadList">
          <el-option v-for="f in folders" :key="f.name" :label="folderLabel(f)" :value="f.name" />
        </el-select>
      </div>
      <div class="m-toolbar">
        <input v-model="search" class="m-search" placeholder="搜索主题/发件人（IMAP SEARCH）" @input="onSearch" />
        <el-tooltip content="写邮件" placement="top">
          <button type="button" class="m-btn primary icon-btn" @click="openCompose"><el-icon :size="16"><EditPen /></el-icon></button>
        </el-tooltip>
        <el-tooltip content="从服务器拉取新邮件" placement="top">
          <button type="button" class="m-btn icon-btn" :disabled="refreshing" @click="refreshList"><el-icon :size="18" :class="{ 'm-spin': refreshing }"><Refresh /></el-icon></button>
        </el-tooltip>
      </div>
      <div v-loading="listLoading" class="m-list">
        <div v-for="h in headers" :key="h.uid" :class="['m-row', { unread: !h.seen, active: h.uid === selectedUid }]" @click="openMsg(h)" @dblclick="openMsgDlg(h)">
          <span v-if="!h.seen" class="m-unread-dot" />
          <div class="m-row-main">
            <div class="m-row-top">
              <span class="m-from">{{ curFolder === 'INBOX' ? (h.from_name || h.from_addr) : (h.to_name || '我') }}</span>
              <span class="m-date">{{ fmtDate(h.date_unix) }}</span>
            </div>
            <div class="m-row-bottom">
              <span class="m-subject">{{ h.subject || '(无主题)' }}</span>
              <span class="m-flags"><span v-if="h.has_att" title="有附件">📎</span><span v-if="h.size" class="m-size">{{ fmtSize(h.size) }}</span></span>
            </div>
          </div>
        </div>
        <div v-if="!listLoading && !headers.length" class="m-empty-tip">
          {{ search.trim() ? '没有匹配的邮件' : '尚未从服务器同步过该文件夹，点右上「刷新」拉取' }}
        </div>
        <button v-if="hasMore && headers.length" type="button" class="m-more" :disabled="loadingMore" @click="loadMore">
          {{ loadingMore ? '加载中…' : '加载更早的邮件' }}
        </button>
      </div>
    </section>
    <div class="side-divider" :class="{ on: sideM.resizing }" title="拖拽调节宽度 · 单击收起/展开" @mousedown="sideM.startDrag" />

    <!-- 右栏：阅读（327 回退：用户验收后要求恢复右栏方式） -->
    <section class="m-right" :class="{ on: !!msg }">
      <div v-if="msgLoading" v-loading="true" class="m-empty-tip" style="height:100%" />
      <template v-else-if="msg">
        <div class="m-reader-head">
          <div class="m-reader-subj">{{ msg.subject || '(无主题)' }}</div>
          <div class="m-reader-meta">
            <span class="m-reader-from"><b>{{ msg.from_name || msg.from_addr }}</b> &lt;{{ msg.from_addr }}&gt;</span>
            <span class="m-reader-date">{{ new Date((msg.date_unix || 0) * 1000).toLocaleString() }}</span>
          </div>
          <div class="m-reader-to">收件人：{{ msg.to || '-' }}</div>
          <div v-if="msg.cc" class="m-reader-to">抄送：{{ msg.cc }}</div>
          <div class="m-reader-btns">
            <button type="button" class="m-btn back" @click="closeMsg">← 返回</button>
            <button type="button" class="m-btn" @click="replyMsg(false)">回复</button>
            <button type="button" class="m-btn" title="收件人含原发件人与原收件人，抄送含原抄送人（均剔除本机账户）" @click="replyMsg(true)">全部回复</button>
            <button type="button" class="m-btn" @click="forwardMsg">转发</button>
            <button type="button" class="m-btn" @click="markSeen(headers.find((x) => x.uid === msg.uid) || { uid: msg.uid }, !msg.seen)">{{ msg.seen ? '标为未读' : '标为已读' }}</button>
            <button type="button" class="m-btn danger" @click="deleteMsg(headers.find((x) => x.uid === msg.uid) || { uid: msg.uid })">删除</button>
            <button v-if="msg.html && !allowRemote" type="button" class="m-btn" title="默认屏蔽远程图片防追踪，点击后加载本信图片" @click="allowRemote = true">显示远程图片</button>
          </div>
        </div>
        <div v-if="msg.attachments.length" class="m-atts">
          <span v-for="att in msg.attachments" :key="att.path" class="m-att" :title="`${att.name}（${fmtSize(att.size)}）`" @click="downloadAtt(att)">
            📎 {{ att.name }} <i v-if="att.size">{{ fmtSize(att.size) }}</i>
          </span>
        </div>
        <iframe class="m-frame" sandbox="allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer" :srcdoc="safeHtml" />
      </template>
      <div v-else class="m-empty-tip" style="height:100%">选择一封邮件阅读</div>
    </section>

    <!-- 邮件详情弹层（11）：双击列表行打开，复用右栏阅读的 msg 状态与沙箱渲染
         （safeHtml iframe CSP/防追踪、附件下载原样）；append-to-body 逃 .main -->
    <el-dialog v-model="msgDlg" title="邮件详情" width="880px" top="4vh" append-to-body>
      <template v-if="msg">
        <div class="m-reader-head m-dlg-head">
          <div class="m-reader-subj">{{ msg.subject || '(无主题)' }}</div>
          <div class="m-reader-meta">
            <span class="m-reader-from"><b>{{ msg.from_name || msg.from_addr }}</b> &lt;{{ msg.from_addr }}&gt;</span>
            <span class="m-reader-date">{{ new Date((msg.date_unix || 0) * 1000).toLocaleString() }}</span>
          </div>
          <div class="m-reader-to">收件人：{{ msg.to || '-' }}</div>
          <div v-if="msg.cc" class="m-reader-to">抄送：{{ msg.cc }}</div>
          <div v-if="msg.html && !allowRemote" class="m-dlg-remote">
            <button type="button" class="m-btn" title="默认屏蔽远程图片防追踪，点击后加载本信图片" @click="allowRemote = true">显示远程图片</button>
          </div>
        </div>
        <div v-if="msg.attachments.length" class="m-atts">
          <span v-for="att in msg.attachments" :key="att.path" class="m-att" :title="`${att.name}（${fmtSize(att.size)}）`" @click="downloadAtt(att)">
            📎 {{ att.name }} <i v-if="att.size">{{ fmtSize(att.size) }}</i>
          </span>
        </div>
        <iframe class="m-frame m-dlg-frame" sandbox="allow-popups allow-popups-to-escape-sandbox" referrerpolicy="no-referrer" :srcdoc="safeHtml" />
      </template>
      <div v-else v-loading="msgLoading" class="m-dlg-loading" />
    </el-dialog>

    <!-- 账户管理弹窗 -->
    <el-dialog v-model="acctDlg" title="邮箱账户" width="620px" append-to-body :before-close="acctGuard.beforeClose">
      <template v-if="!acctForm">
        <div class="m-acct-list">
          <div v-for="(a, i) in acctList" :key="a.id" class="m-acct-row">
            <span class="m-acct-arr" @click="moveAcct(a, -1)">↑</span><span class="m-acct-arr" @click="moveAcct(a, 1)">↓</span>
            <div class="m-acct-info">
              <b>{{ a.name }}</b> <span class="m-muted">{{ a.email }}</span>
              <div class="m-muted small">{{ a.imap_host }}:{{ a.imap_port }} · 每 {{ fmtPoll(a.poll_sec) }} · {{ a.last_error ? '异常: ' + a.last_error : (a.last_ok_at ? '正常' : '未连接') }}</div>
            </div>
            <el-button size="small" @click="editAcct(a)">编辑</el-button>
            <el-button size="small" type="danger" plain @click="removeAcct(a)">删除</el-button>
          </div>
        </div>
        <el-button v-if="acctList.length < maxAccounts" type="primary" style="width:100%;margin-top:10px" @click="newAcct">+ 添加账户（上限 {{ maxAccounts }}）</el-button>
      </template>
      <template v-else>
        <el-form label-width="92px" size="default">
          <el-form-item label="服务商">
            <el-select v-model="preset" style="width:100%" @change="applyPreset">
              <el-option v-for="p in presets" :key="p.key" :label="p.label" :value="p.key" />
            </el-select>
          </el-form-item>
          <el-form-item label="邮箱地址"><el-input v-model="acctForm.email" placeholder="user@example.com" /></el-form-item>
          <el-form-item label="名称"><el-input v-model="acctForm.name" placeholder="默认取邮箱前缀" /></el-form-item>
          <el-form-item :label="acctForm.id ? '新密码' : '密码/授权码'">
            <el-input v-model="acctForm.password" show-password :placeholder="acctForm.id ? '留空沿用原密码' : 'QQ/163 等需填授权码（设置里开启 IMAP/SMTP 后生成）'" />
          </el-form-item>
          <el-form-item label="轮询间隔">
            <el-select v-model="acctForm.poll_sec" style="width:100%">
              <el-option label="1 分钟" :value="60" />
              <el-option label="5 分钟" :value="300" />
              <el-option label="10 分钟（默认）" :value="600" />
              <el-option label="15 分钟" :value="900" />
              <el-option label="30 分钟" :value="1800" />
              <el-option label="1 小时" :value="3600" />
            </el-select>
          </el-form-item>
          <el-form-item label="IMAP 服务器"><el-input v-model="acctForm.imap_host" /><el-input-number v-model="acctForm.imap_port" :min="1" :max="65535" style="width:110px;margin-left:8px" /></el-form-item>
          <el-form-item label="SMTP 服务器">
            <el-input v-model="acctForm.smtp_host" /><el-input-number v-model="acctForm.smtp_port" :min="1" :max="65535" style="width:110px;margin-left:8px" />
            <el-checkbox v-model="acctForm.smtp_ssl" style="margin-left:8px">SSL(465)</el-checkbox>
          </el-form-item>
        </el-form>
        <div style="display:flex;gap:8px">
          <el-button style="flex:1" @click="acctForm = null">返回</el-button>
          <el-button v-if="acctForm.id" style="flex:1" :loading="testing" @click="testAcct">测试连接</el-button>
          <el-button type="primary" style="flex:1" :loading="savingAcct" @click="saveAcct">保存（自动测试连接）</el-button>
        </div>
      </template>
    </el-dialog>

    <!-- 写信弹窗（compose 为对象/ null 双态，v-model 需可写表达式故用 model-value 转接） -->
    <el-dialog :model-value="!!compose" title="写邮件" width="640px" append-to-body :before-close="composeGuard.beforeClose" @closed="compose = null">
      <template v-if="compose">
        <el-form label-width="72px" size="default">
          <el-form-item label="发件账户">
            <el-select v-model="sendAcctId" style="width:100%">
              <el-option v-for="a in accounts" :key="a.id" :label="`${a.name}（${a.email}）`" :value="a.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="收件人">
            <el-select v-model="compose.to" multiple filterable allow-create default-first-option :filter-method="filterContacts"
              placeholder="输入姓名/邮箱关键字从通讯录选择，或直接输入邮箱回车" style="width:100%">
              <el-option v-for="ct in contactHits" :key="ct.id" :label="ct.email" :value="ct.email">
                <span style="display:flex;justify-content:space-between;gap:12px"><span>{{ ct.name || '—' }}</span><span style="color:var(--muted,#888)">{{ ct.email }}</span></span>
              </el-option>
            </el-select>
          </el-form-item>
          <el-form-item label="抄送">
            <el-select v-model="compose.cc" multiple filterable allow-create default-first-option :filter-method="filterContacts"
              placeholder="可空，同上可选通讯录" style="width:100%">
              <el-option v-for="ct in contactHits" :key="ct.id" :label="ct.email" :value="ct.email">
                <span style="display:flex;justify-content:space-between;gap:12px"><span>{{ ct.name || '—' }}</span><span style="color:var(--muted,#888)">{{ ct.email }}</span></span>
              </el-option>
            </el-select>
          </el-form-item>
          <el-form-item label="主题"><el-input v-model="compose.subject" ref="composeRef" /></el-form-item>
          <el-form-item label="正文">
            <el-input v-model="compose.body" type="textarea" :rows="7" placeholder="支持换行" />
          </el-form-item>
          <el-form-item v-if="compose.quote" label="原信">
            <div class="m-quote" style="width:100%">
              <button type="button" class="m-quote-toggle" @click="quoteOpen = !quoteOpen">
                {{ quoteOpen ? '▾' : '▸' }} 引用原信（发送时附在正文后）
              </button>
              <iframe v-show="quoteOpen" class="m-quote-frame" sandbox="allow-popups allow-popups-to-escape-sandbox"
                referrerpolicy="no-referrer" :srcdoc="quoteDoc" />
            </div>
          </el-form-item>
          <el-form-item label="附件">
            <input type="file" multiple @change="onComposeFiles" />
            <span v-if="compose.files.length" class="m-muted small">已选 {{ compose.files.length }} 个（总量 ≤25MB）</span>
          </el-form-item>
        </el-form>
        <div style="text-align:right">
          <el-button @click="compose = null">取消</el-button>
          <el-button type="primary" :loading="sending" @click="sendCompose">发送</el-button>
        </div>
      </template>
    </el-dialog>

    <!-- 通讯录弹窗（506） -->
    <el-dialog v-model="contactsDlg" title="通讯录" width="560px" append-to-body>
      <template v-if="!ctForm">
        <div class="m-ct-list">
          <div v-for="ct in contactsSorted" :key="ct.id" class="m-ct-row">
            <div class="m-acct-info">
              <b>{{ ct.name || ct.email }}</b> <span v-if="ct.name" class="m-muted">{{ ct.email }}</span>
            </div>
            <el-button size="small" @click="ctForm = { id: ct.id, name: ct.name, email: ct.email }">编辑</el-button>
            <el-button size="small" type="danger" plain @click="delContact(ct)">删除</el-button>
          </div>
          <div v-if="!contacts.length" class="m-empty-tip">通讯录为空，点下面按钮添加</div>
        </div>
        <el-button type="primary" style="width:100%;margin-top:10px" @click="ctForm = { id: 0, name: '', email: '' }">+ 新增联系人</el-button>
      </template>
      <template v-else>
        <el-form label-width="92px" size="default" @submit.prevent>
          <el-form-item label="姓名"><el-input v-model="ctForm.name" placeholder="可选，写信时下拉显示" /></el-form-item>
          <el-form-item label="邮箱"><el-input v-model="ctForm.email" placeholder="user@example.com" @keyup.enter="saveContact" /></el-form-item>
        </el-form>
        <div style="display:flex;gap:8px">
          <el-button style="flex:1" @click="ctForm = null">返回</el-button>
          <el-button type="primary" style="flex:1" :loading="ctSaving" @click="saveContact">保存</el-button>
        </div>
      </template>
    </el-dialog>

    <!-- 邮件签名弹窗（507） -->
    <el-dialog v-model="sigDlg" title="邮件签名" width="520px" append-to-body>
      <el-input v-model="sigText" type="textarea" :rows="6" maxlength="2000" show-word-limit
        placeholder="署名、联系方式等，写信/回复/转发时自动带在正文尾部（可当场删改）" />
      <template #footer>
        <el-button @click="sigDlg = false">取消</el-button>
        <el-button type="primary" :loading="sigSaving" @click="saveSignature">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.mailp { display: flex; height: calc(100vh - 84px); gap: 0; padding: 10px; box-sizing: border-box; }
.m-left { width: 190px; flex: none; display: flex; flex-direction: column; gap: 10px; overflow-y: auto; }
.m-accts, .m-folders { background: var(--panel, rgba(127,127,127,.08)); border: 1px solid var(--border); border-radius: 10px; padding: 8px; }
.m-sec-title { font-size: 12px; color: var(--muted, #888); margin: 2px 4px 6px; }
.m-acct, .m-folder { padding: 7px 9px; border-radius: 7px; cursor: pointer; font-size: 13px; color: var(--ink); display: flex; align-items: center; gap: 6px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.m-acct:hover, .m-folder:hover { background: rgba(127,127,127,.12); }
.m-acct.active, .m-folder.active { background: var(--primary-weak, rgba(64,158,255,.18)); }
.m-acct-dot { width: 7px; height: 7px; border-radius: 50%; background: #67c23a; flex: none; }
.m-acct-dot.bad { background: #f56c6c; }
.m-acct-name { overflow: hidden; text-overflow: ellipsis; }
.m-acct-unread { margin-left: auto; background: #f56c6c; color: #fff; font-size: 10px; border-radius: 8px; min-width: 16px; height: 16px; line-height: 16px; text-align: center; padding: 0 4px; flex: none; }
.m-manage { margin-top: 6px; width: 100%; border: 1px dashed var(--border); background: none; color: var(--ink); border-radius: 7px; padding: 6px; cursor: pointer; font-size: 12px; }
.m-manage:hover { border-color: var(--primary, #409eff); color: var(--primary, #409eff); }
.m-manage-row { display: flex; gap: 6px; }
.m-manage-row .m-manage { margin-top: 6px; flex: 1; min-width: 0; padding: 6px 2px; }
/* 写信弹窗引用原信预览（508） */
.m-quote { display: flex; flex-direction: column; gap: 4px; }
.m-quote-toggle { align-self: flex-start; border: none; background: none; color: var(--muted, #888); font-size: 12px; cursor: pointer; padding: 0; }
.m-quote-toggle:hover { color: var(--primary, #409eff); }
.m-quote-frame { width: 100%; height: 170px; border: 1px solid var(--border); border-left: 3px solid var(--border); border-radius: 6px; background: #fff; }
/* 通讯录弹窗（506） */
.m-ct-list { max-height: 380px; overflow-y: auto; display: flex; flex-direction: column; gap: 6px; }
.m-ct-row { display: flex; align-items: center; gap: 8px; border: 1px solid var(--border); border-radius: 8px; padding: 8px 10px; }
.m-mid { flex: none; display: flex; flex-direction: column; background: var(--panel, rgba(127,127,127,.08)); border: 1px solid var(--border); border-radius: 10px; overflow: hidden; }
.m-toolbar { display: flex; gap: 6px; padding: 8px; border-bottom: 1px solid var(--border); }
.m-search { flex: 1; min-width: 0; background: none; border: 1px solid var(--border); border-radius: 7px; padding: 6px 9px; color: var(--ink); font-size: 13px; outline: none; }
.m-search:focus { border-color: var(--primary, #409eff); }
.m-btn { border: 1px solid var(--border); background: none; color: var(--ink); border-radius: 7px; padding: 6px 10px; cursor: pointer; font-size: 13px; white-space: nowrap; }
.m-btn:hover { border-color: var(--primary, #409eff); color: var(--primary, #409eff); }
.m-btn:disabled { opacity: 0.5; cursor: not-allowed; }
/* 刷新进行中图标转圈（按钮同时禁用），让「连服务器拉取」有可见过程 */
.m-spin { animation: m-spin 0.9s linear infinite; }
@keyframes m-spin { to { transform: rotate(360deg); } }
.m-btn.primary { background: var(--primary, #409eff); border-color: var(--primary, #409eff); color: #fff; }
.m-btn.danger:hover { border-color: #f56c6c; color: #f56c6c; }
/* 分栏用全局 .side-divider（文档同款 8px+把手线，354 迁移 useSidePanel），本地不再定义 */
.m-mobile-bar { display: none; }
.m-cache-tip { font-size: 12px; color: #e6a23c; padding: 4px 10px; }
.m-list { flex: 1; overflow-y: auto; }
.m-row { display: flex; gap: 7px; padding: 9px 10px; border-bottom: 1px solid var(--border); cursor: pointer; align-items: flex-start; }
.m-row:hover { background: rgba(127,127,127,.1); }
.m-row.active { background: var(--primary-weak, rgba(64,158,255,.14)); }
.m-row.unread .m-from, .m-row.unread .m-subject { font-weight: 700; }
.m-unread-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--primary, #409eff); flex: none; margin-top: 6px; }
.m-row-main { flex: 1; min-width: 0; }
.m-row-top, .m-row-bottom { display: flex; justify-content: space-between; gap: 8px; }
.m-from { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.m-date { font-size: 11px; color: var(--muted, #888); flex: none; }
.m-subject { font-size: 13px; color: var(--muted, #aaa); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.m-row.unread .m-subject { color: var(--ink); }
.m-flags { flex: none; font-size: 11px; color: var(--muted, #888); display: flex; gap: 5px; }
.m-reader-meta { display: flex; justify-content: space-between; gap: 8px; font-size: 12px; color: var(--ink); flex-wrap: wrap; }
.m-reader-from b { margin-right: 4px; }
.m-reader-date { color: var(--muted, #888); }
.m-reader-to { font-size: 12px; color: var(--muted, #888); margin-top: 3px; word-break: break-all; }
.m-reader-btns { display: flex; gap: 6px; margin-top: 8px; flex-wrap: wrap; }
.m-atts { display: flex; flex-wrap: wrap; gap: 6px; padding: 8px 12px; border-bottom: 1px solid var(--border); }
.m-att { font-size: 12px; border: 1px solid var(--border); border-radius: 6px; padding: 4px 8px; cursor: pointer; color: var(--ink); }
.m-att:hover { border-color: var(--primary, #409eff); color: var(--primary, #409eff); }
.m-att i { color: var(--muted, #888); font-style: normal; margin-left: 3px; }
.m-right { flex: 1; min-width: 0; display: none; flex-direction: column; background: var(--panel, rgba(127,127,127,.08)); border: 1px solid var(--border); border-radius: 10px; overflow: hidden; }
.mailp.reading .m-right { display: flex; }
.m-reader-head { padding: 12px 14px 8px; border-bottom: 1px solid var(--border); }
.m-reader-subj { font-size: 16px; font-weight: 700; color: var(--ink); margin-bottom: 6px; word-break: break-all; }
.m-frame { flex: 1; border: none; width: 100%; background: #fff; }
/* 邮件详情弹层（11）：窗格样式复用，仅弹层特有排布——iframe 不吃 flex:1 改定高 */
.m-dlg-head { padding: 0 0 8px; border-bottom: none; }
.m-dlg-remote { margin-top: 6px; }
.m-dlg-frame { flex: none; height: 62vh; border: 1px solid var(--border); border-radius: 8px; }
.m-dlg-loading { height: 220px; }
.m-btn.back { display: none; }
.m-empty-tip { color: var(--muted, #888); font-size: 13px; text-align: center; padding: 20px 10px; }
.m-more { display: block; width: 100%; border: none; background: none; color: var(--primary, #409eff); font-size: 12px; padding: 10px; cursor: pointer; }
.m-more:hover { background: rgba(127,127,127,.1); }
.m-more:disabled { color: var(--muted, #888); cursor: default; }
.m-acct-list { max-height: 320px; overflow-y: auto; display: flex; flex-direction: column; gap: 6px; }
.m-acct-row { display: flex; align-items: center; gap: 8px; border: 1px solid var(--border); border-radius: 8px; padding: 8px 10px; }
.m-acct-arr { cursor: pointer; color: var(--muted, #888); font-size: 12px; padding: 0 2px; }
.m-acct-arr:hover { color: var(--primary, #409eff); }
.m-acct-info { flex: 1; min-width: 0; font-size: 13px; }
.m-acct-info .small, .small { font-size: 11px; }
.m-muted { color: var(--muted, #888); }

/* 手机：左右栏隐藏/覆盖，列表与阅读全屏切换 */
@media (max-width: 768px) {
  .mailp { padding: 6px; gap: 6px; height: calc(100vh - 70px); position: relative; }
  .m-left { display: none; }
  .mailp:not(.reading) .m-right { display: none; }
  /* 中栏忽略拖拽宽度强制全宽；手机端不受桌面收起态影响（isMobile 守卫 + 重要度兜底） */
  .m-mid { width: 100% !important; flex: 1; }
  .mailp.reading .m-mid { display: none; }
  .m-right { position: absolute; inset: 6px; z-index: 5; }
  .m-row-top .m-date { flex: none; }
  /* 移动端补左栏能力：账户+文件夹下拉选择条 */
  .m-mobile-bar { display: flex; gap: 6px; padding: 8px; border-bottom: 1px solid var(--border); }
  .m-btn.back { display: inline-block; }
  .m-reader-head { padding-top: 8px; }
}
</style>
