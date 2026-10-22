<template>
  <el-dialog
    v-model="visible"
    :title="`连接 ${server?.name || ''}`"
    width="380px"
    :close-on-click-modal="false"
    append-to-body
    @close="onClose"
  >
    <div v-if="error" class="cred-error">{{ error }}</div>
    <div class="cred-ctx">{{ ctxText }}</div>
    <el-form label-width="64px" @submit.prevent="confirm">
      <el-form-item v-if="needAddr" label="SSH 地址">
        <el-input ref="addrRef" v-model="sshAddr" placeholder="推送模式无采集地址，填目标机 host[:22]" autocomplete="off" />
      </el-form-item>
      <el-form-item v-if="mode === 'both'" label="用户名">
        <el-input ref="userRef" v-model="username" placeholder="root" autocomplete="off" />
      </el-form-item>
      <el-form-item label="密码">
        <el-input
          ref="passRef"
          v-model="password"
          type="password"
          show-password
          placeholder="SSH 登录密码"
          autocomplete="new-password"
          @keyup.enter="confirm"
        />
      </el-form-item>
    </el-form>
    <el-checkbox v-model="save" class="cred-save">保存密码（下次免输，可在服务器配置中清除）</el-checkbox>
    <div v-if="mode === 'both'" class="cred-note">用户名将保存到服务器配置，下次只需输入密码</div>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="testing" @click="confirm">连接</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// SSH 凭据输入对话框（终端页与文件管理页共用）。
// open(server, mode, opts) 返回 Promise：
//   mode='pass'：服务器已配用户名，只补密码；'both'：用户名密码都要输。
//   opts.verify(cred)：可选的异步校验（文件页 POST /term/sftp-auth）——
//   提供时「连接」按钮先试连，失败把错误显示在框内可重输，成功才 resolve；
//   不提供（终端页凭 WS auth 帧连接）则直接 resolve。
//   opts.error：上次失败的原因（重开时展示在顶部）。
// resolve 值 {username, password, save, sshAddr?}；取消/关闭 resolve null。
// sshAddr：push 模式未配专用地址时弹窗补填（383），调用方负责先落库再试连。
import { computed, nextTick, ref } from 'vue'
import { ElMessage } from 'element-plus'

const visible = ref(false)
const server = ref(null)
const mode = ref('pass')
const username = ref('')
const password = ref('')
const sshAddr = ref('')
const save = ref(false)
const error = ref('')
const testing = ref(false)
const userRef = ref(null)
const passRef = ref(null)
const addrRef = ref(null)

// push 且未配专用地址：无采集地址可回退，连接前必须补填
const needAddr = computed(() => {
  const s = server.value
  return !!s && s.collect_mode === 'push' && !s.ssh_addr
})

let verify = null
let resolver = null
let done = false

const ctxText = ref('')

function open(srv, m, opts = {}) {
  server.value = srv
  mode.value = m
  username.value = m === 'both' ? '' : srv.ssh_user || ''
  password.value = ''
  sshAddr.value = ''
  save.value = false
  error.value = opts.error || ''
  verify = opts.verify || null
  ctxText.value = needAddr.value
    ? '推送模式服务器未配置 SSH 地址，请在下方填写（将保存到服务器配置，下次免输）'
    : m === 'pass'
      ? `以 ${srv.ssh_user || '当前用户'} 连接${srv.ssh_addr || srv.agent_addr || ''}`
      : srv.ssh_addr || srv.agent_addr || ''
  done = false
  visible.value = true
  nextTick(() => (needAddr.value ? addrRef.value?.focus() : m === 'both' ? userRef.value?.focus() : passRef.value?.focus()))
  return new Promise((res) => {
    resolver = res
  })
}

function onClose() {
  if (!done) {
    resolver?.(null)
    resolver = null
  }
}

async function confirm() {
  if (needAddr.value && !sshAddr.value.trim()) {
    ElMessage.warning('请填写 SSH 地址')
    return
  }
  if (mode.value === 'both' && !username.value.trim()) {
    ElMessage.warning('请输入用户名')
    return
  }
  if (!password.value) {
    ElMessage.warning('请输入密码')
    return
  }
  const cred = { username: username.value.trim(), password: password.value, save: save.value }
  if (needAddr.value) cred.sshAddr = sshAddr.value.trim()
  if (verify) {
    testing.value = true
    try {
      await verify(cred)
    } catch (e) {
      error.value = e.message || '连接失败'
      testing.value = false
      return
    }
    testing.value = false
  }
  done = true
  visible.value = false
  resolver?.(cred)
  resolver = null
}

defineExpose({ open })
</script>

<style scoped>
.cred-error {
  margin-bottom: 10px;
  padding: 7px 10px;
  border-radius: 6px;
  background: var(--el-color-danger-light-9, #fef0f0);
  color: var(--el-color-danger, #f56c6c);
  font-size: 12px;
  line-height: 1.5;
  word-break: break-all;
}
.cred-ctx {
  margin-bottom: 12px;
  color: var(--muted, #909399);
  font-size: 12px;
  word-break: break-all;
}
.cred-save {
  margin-left: 64px;
  height: auto;
  white-space: normal;
  line-height: 1.4;
}
.cred-note {
  margin: 6px 0 0 64px;
  color: var(--muted, #909399);
  font-size: 12px;
}
</style>
