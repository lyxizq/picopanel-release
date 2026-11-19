<template>
  <el-dialog append-to-body v-model="visible" :title="$t('serverdlg.edit.title')" width="480px" :before-close="guard.beforeClose">
    <el-form @submit.prevent="save">
      <el-form-item :label="$t('serverdlg.edit.fMode')" label-width="100px">
        <el-radio-group v-model="form.mode">
          <el-radio value="agent">{{ $t('settings.modes.agent') }}</el-radio>
          <el-radio value="push">{{ $t('settings.modes.push') }}</el-radio>
          <el-radio value="ssh" disabled>{{ $t('serverdlg.edit.modeSshDisabled') }}</el-radio>
          <el-radio value="node">node_exporter</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item :label="$t('serverdlg.edit.fName')" label-width="100px">
        <el-input v-model="form.name" :placeholder="$t('serverdlg.edit.phName')" @keyup.enter="save" />
      </el-form-item>
      <el-form-item :label="$t('serverdlg.edit.fIcon')" label-width="100px">
        <el-input v-model="form.icon" :placeholder="$t('serverdlg.edit.phIcon')" />
      </el-form-item>
      <el-form-item :label="$t('serverdlg.edit.fRemark')" label-width="100px">
        <el-input v-model="form.remark" :placeholder="$t('serverdlg.edit.phRemark')" maxlength="500" />
      </el-form-item>
      <el-form-item
        v-if="form.mode !== 'push'"
        :label="form.mode === 'ssh' ? $t('serverdlg.edit.addrSsh') : form.mode === 'node' ? $t('serverdlg.edit.addrExporter') : $t('serverdlg.edit.addrAgent')"
        label-width="100px"
      >
        <el-input
          v-model="form.agent_addr"
          :placeholder="form.mode === 'ssh' ? 'host[:22]' : form.mode === 'node' ? 'host[:10200]' : 'host:19100'"
        />
      </el-form-item>
      <template v-if="form.mode === 'ssh'">
        <el-form-item :label="$t('serverdlg.edit.fSshUser')" label-width="100px">
          <el-input v-model="form.ssh_user" :placeholder="$t('serverdlg.edit.phSshUser')" />
        </el-form-item>
        <el-form-item :label="$t('serverdlg.edit.fAuth')" label-width="100px">
          <el-radio-group v-model="authMode">
            <el-radio value="password">{{ $t('serverdlg.edit.authPass') }}</el-radio>
            <el-radio value="key">{{ $t('serverdlg.edit.authKey') }}</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="authMode === 'password'" :label="$t('serverdlg.edit.fSshPass')" label-width="100px">
          <el-input
            v-model="form.ssh_password"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="pwSet ? $t('settings.savedNoEcho') : $t('serverdlg.edit.phPw')"
            @input="clearPW = false"
          />
          <div v-if="pwSet" class="iface-hint">
            <el-checkbox v-model="clearPW" size="small">{{ $t('serverdlg.edit.clearPw') }}</el-checkbox>
          </div>
        </el-form-item>
        <el-form-item v-else :label="$t('serverdlg.edit.fKeyPath')" label-width="100px">
          <el-input v-model="form.ssh_key_path" :placeholder="$t('serverdlg.edit.phKeyPath')" />
          <div v-if="pwSet" class="iface-hint">{{ $t('serverdlg.edit.pwClearedHint') }}</div>
        </el-form-item>
        <el-form-item label-width="100px">
          <div class="iface-hint" v-if="hasHostKey">
            {{ $t('serverdlg.edit.hostKeySet') }}
            <el-checkbox v-model="resetHostKey" size="small">{{ $t('serverdlg.edit.resetHostKey') }}</el-checkbox>
          </div>
          <div class="iface-hint" v-else>{{ $t('serverdlg.edit.hostKeyUnset') }}</div>
        </el-form-item>
      </template>
      <el-form-item
        v-if="form.mode !== 'ssh'"
        :label="form.mode === 'push' ? $t('serverdlg.edit.fPushToken') : $t('serverdlg.edit.fToken')"
        label-width="100px"
      >
        <div class="token-row">
          <el-input
            v-model="form.token"
            :placeholder="tokenSet ? $t('serverdlg.edit.phTokenSet') : tokenPlaceholder"
          />
          <el-button @click="regenToken">{{ tokenSet ? $t('serverdlg.edit.regenBtn') : $t('serverdlg.edit.genBtn') }}</el-button>
        </div>
        <div v-if="tokenSet && form.mode !== 'push'" class="iface-hint">
          <el-checkbox v-model="clearToken" size="small" @change="form.token = ''">{{ $t('serverdlg.edit.clearToken') }}</el-checkbox>
        </div>
        <div class="iface-hint">{{ tokenHint }}</div>
      </el-form-item>
      <!-- Web 终端 / SFTP：ssh 模式复用上方采集配置（仅可覆盖地址）；
           其他模式在此补配独立的 SSH 信息（监控走 agent、终端走 SSH 两不误） -->
      <el-divider content-position="left" class="term-divider">{{ $t('serverdlg.edit.termDivider') }}</el-divider>
      <template v-if="form.mode !== 'ssh'">
        <el-form-item :label="$t('serverdlg.edit.fTermAddr')" label-width="100px">
          <el-input v-model="form.ssh_addr" :placeholder="$t('serverdlg.edit.phTermAddr')" />
        </el-form-item>
        <el-form-item :label="$t('serverdlg.edit.fTermUser')" label-width="100px">
          <el-input v-model="form.ssh_user" :placeholder="$t('serverdlg.edit.phTermUser')" />
        </el-form-item>
        <el-form-item :label="$t('serverdlg.edit.fAuth')" label-width="100px">
          <el-radio-group v-model="authMode">
            <el-radio value="password">{{ $t('serverdlg.edit.authPass') }}</el-radio>
            <el-radio value="key">{{ $t('serverdlg.edit.authKey') }}</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="authMode === 'password'" :label="$t('serverdlg.edit.fSshPass')" label-width="100px">
          <el-input
            v-model="form.ssh_password"
            type="password"
            show-password
            autocomplete="new-password"
            :placeholder="pwSet ? $t('settings.savedNoEcho') : $t('serverdlg.edit.phPw')"
            @input="clearPW = false"
          />
          <div v-if="pwSet" class="iface-hint">
            <el-checkbox v-model="clearPW" size="small">{{ $t('serverdlg.edit.clearPw') }}</el-checkbox>
          </div>
          <div class="iface-hint" v-if="hasHostKey">
            {{ $t('serverdlg.edit.hostKeySet') }}
            <el-checkbox v-model="resetHostKey" size="small">{{ $t('serverdlg.edit.resetHostKey') }}</el-checkbox>
          </div>
          <div class="iface-hint" v-else>{{ $t('serverdlg.edit.hostKeyUnsetTerm') }}</div>
        </el-form-item>
        <el-form-item v-else :label="$t('serverdlg.edit.fKeyPath')" label-width="100px">
          <el-input v-model="form.ssh_key_path" :placeholder="$t('serverdlg.edit.phKeyPath')" />
          <div v-if="pwSet" class="iface-hint">{{ $t('serverdlg.edit.pwClearedHint') }}</div>
          <div class="iface-hint" v-if="hasHostKey">
            {{ $t('serverdlg.edit.hostKeySet') }}
            <el-checkbox v-model="resetHostKey" size="small">{{ $t('serverdlg.edit.resetHostKey') }}</el-checkbox>
          </div>
          <div class="iface-hint" v-else>{{ $t('serverdlg.edit.hostKeyUnsetTerm') }}</div>
        </el-form-item>
      </template>
      <el-form-item v-else :label="$t('serverdlg.edit.fDedicatedAddr')" label-width="100px">
        <el-input v-model="form.ssh_addr" :placeholder="$t('serverdlg.edit.phDedicatedAddr')" />
      </el-form-item>
      <el-form-item :label="$t('serverdlg.edit.fGroup')" label-width="100px">
        <el-select v-model="form.group_id">
          <el-option :label="$t('groups.ungrouped')" :value="0" />
          <el-option v-for="g in groups" :key="g.id" :label="g.name" :value="g.id" />
        </el-select>
      </el-form-item>
      <el-form-item :label="$t('serverdlg.edit.fNetIfaces')" label-width="100px">
        <el-input v-model="form.net_ifaces" :placeholder="$t('serverdlg.edit.phNetIfaces')" />
        <div class="iface-hint">{{ $t('serverdlg.edit.curIfaces', { n: ifaces || $t('serverdlg.edit.noNetData') }) }}</div>
      </el-form-item>
      <el-form-item :label="$t('serverdlg.edit.fExpiry')" label-width="100px">
        <div class="date-row">
          <el-date-picker
            v-model="form.purchased_on"
            type="date"
            value-format="YYYY-MM-DD"
            :placeholder="$t('serverdlg.edit.phPurchased')"
            clearable
            class="date-pick"
          />
          <span class="date-sep">{{ $t('serverdlg.edit.dateSep') }}</span>
          <el-date-picker
            v-model="form.expires_on"
            type="date"
            value-format="YYYY-MM-DD"
            :placeholder="$t('serverdlg.edit.phExpires')"
            clearable
            class="date-pick"
          />
        </div>
        <div class="iface-hint">{{ $t('serverdlg.edit.expiryHint') }}</div>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">{{ $t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="saving" @click="save">{{ $t('common.save') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
// 编辑服务器弹窗：服务器管理页与总览页（卡片三点菜单 / 列表点开机天数）共用。
// open(row) 传入 /servers 列表里的整行数据，保存成功后 emit('saved') 由调用方刷新。
import { computed, nextTick, ref, watch } from 'vue'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api/index.js'
import { genToken } from '../utils/token.js'
import { useI18n } from 'vue-i18n'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/serverdlg.js'

const { t } = useI18n()
registerPageMessages(pageMsgs)

const emit = defineEmits(['saved'])

const visible = ref(false)
const saving = ref(false)
const form = ref({ id: 0, name: '', agent_addr: '', token: '', net_ifaces: '', mode: 'agent', ssh_user: '', ssh_key_path: '', ssh_addr: '', ssh_password: '', group_id: 0, purchased_on: '', expires_on: '', remark: '' })
const groups = ref([])
const ifaces = ref('')
// 未保存守卫（328）：关闭前对比表单快照，有变更弹确认
const guard = useDirtyGuard(() => ({ ...form.value, authMode: authMode.value, clearPW: clearPW.value, resetHostKey: resetHostKey.value, ifaces: ifaces.value }))

// SSH 认证方式：不存列，由 ssh_password 是否非空派生（服务端口径一致）。
// 切换时清掉另一方式的字段，避免两种凭据同时残留。
const authMode = ref('key')
watch(authMode, (m) => {
  if (m === 'password') {
    form.value.ssh_key_path = ''
  } else {
    form.value.ssh_password = ''
  }
})

// SSH 主机指纹（TOFU）：可勾选重置
const hasHostKey = ref(false)
const resetHostKey = ref(false)

// 打码配套：/servers 不回显密钥/密码明文，仅 token_set / ssh_password_set 布尔。
// 输入框留空=不修改；勾选清除才下发 clear_* 字段。
const tokenSet = ref(false)
const clearToken = ref(false)
const pwSet = ref(false)
const clearPW = ref(false)

// 已配置密钥时换新先确认（旧密钥立即失效，目标机需同步改配置）
async function regenToken() {
  if (tokenSet.value) {
    try {
      await ElMessageBox.confirm(
        t('serverdlg.edit.regenConfirm'),
        t('serverdlg.edit.regenTitle'),
        { type: 'warning', confirmButtonText: t('serverdlg.edit.genBtn'), cancelButtonText: t('common.cancel') },
      )
    } catch {
      return
    }
  }
  clearToken.value = false
  form.value.token = genToken()
}

// 密钥提示按模式区分：node_exporter 本体不校验，密钥仅供前置反代校验
const tokenPlaceholder = computed(() =>
  form.value.mode === 'push'
    ? t('serverdlg.edit.phTokenPush')
    : form.value.mode === 'node'
      ? t('serverdlg.edit.phTokenNode')
      : t('serverdlg.edit.phTokenAgent'),
)
const tokenHint = computed(() =>
  form.value.mode === 'push'
    ? t('serverdlg.edit.hintTokenPush')
    : form.value.mode === 'node'
      ? t('serverdlg.edit.hintTokenNode')
      : t('serverdlg.edit.hintTokenAgent'),
)

async function open(row) {
  form.value = {
    id: row.id,
    name: row.name,
    agent_addr: row.agent_addr || '',
    token: '', // 已配置的密钥不回显（token_set 布尔告知），留空=不修改
    net_ifaces: row.net_ifaces || '',
    mode: row.collect_mode || 'agent',
    ssh_user: row.ssh_user || '',
    ssh_key_path: row.ssh_key_path || '',
    ssh_addr: row.ssh_addr || '',
    ssh_password: '', // 已保存的密码不回显，留空=不修改
    group_id: row.group_id || 0,
    purchased_on: row.purchased_on || '',
    expires_on: row.expires_on || '',
    remark: row.remark || '',
    icon: row.icon || '',
  }
  tokenSet.value = !!row.token_set
  clearToken.value = false
  pwSet.value = !!row.ssh_password_set
  clearPW.value = false
  authMode.value = row.ssh_password_set ? 'password' : 'key'
  hasHostKey.value = !!row.has_host_key
  resetHostKey.value = false
  ifaces.value = (row.latest?.nets || []).map((n) => n.iface).join(', ')
  nextTick(() => guard.snapshot())
  visible.value = true
  // 分组列表自己拉取，组件自包含（弹窗打开的同时请求，无感知延迟）
  try {
    const res = await api.get('/groups')
    groups.value = res.groups || []
  } catch {
    groups.value = []
  }
}

async function save() {
  const f = form.value
  if (!f.name.trim()) {
    ElMessage.warning(t('serverdlg.edit.warnName'))
    return
  }
  if (f.mode !== 'push' && !f.agent_addr.trim()) {
    ElMessage.warning(t('serverdlg.edit.warnAddr'))
    return
  }
  if (f.mode === 'ssh' && !f.ssh_user.trim()) {
    ElMessage.warning(t('serverdlg.edit.warnSshUser'))
    return
  }
  if (f.mode === 'push' && !f.token.trim() && !tokenSet.value) {
    ElMessage.warning(t('serverdlg.edit.warnPushToken'))
    return
  }
  const body = {
    name: f.name.trim(),
    net_ifaces: f.net_ifaces.trim(),
    mode: f.mode,
    group_id: f.group_id || 0,
    remark: f.remark.trim(),
    icon: f.icon.trim(),
    // 清空日期选择后 el-date-picker 会把模型置为 null，统一落成空串（= 清空）
    purchased_on: f.purchased_on || '',
    expires_on: f.expires_on || '',
  }
  if (f.mode !== 'push') {
    body.agent_addr = f.agent_addr.trim()
  }
  // SSH 字段两种模式都传：ssh 模式=采集认证；其他模式=终端专用认证。
  // 密码打码配套：非空=更新，留空=不变，清空走 clear_ssh_password；
  // 切到私钥认证时已存密码随保存清除（认证 radio 二选一的语义）
  body.ssh_user = f.ssh_user.trim()
  body.ssh_key_path = f.ssh_key_path.trim()
  body.ssh_addr = f.ssh_addr.trim()
  if (authMode.value === 'password') {
    if (f.ssh_password) {
      body.ssh_password = f.ssh_password
    } else if (pwSet.value && clearPW.value) {
      body.clear_ssh_password = true
    }
  } else if (pwSet.value) {
    body.clear_ssh_password = true
  }
  if (resetHostKey.value) {
    body.reset_host_key = true
  }
  if (f.mode !== 'ssh') {
    // 密钥同口径：非空=更新（生成新密钥也走这里），留空=不变，勾选清除才清
    if (f.token.trim()) {
      body.token = f.token.trim()
    } else if (clearToken.value && f.mode !== 'push') {
      body.clear_token = true
    }
  }
  saving.value = true
  try {
    await api.put(`/servers/${f.id}`, body)
    visible.value = false
    ElMessage.success(t('common.saved'))
    emit('saved')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.iface-hint {
  color: var(--muted);
  font-size: 12px;
  margin-top: 4px;
  line-height: 1.4;
}
.term-divider {
  margin: 8px 0 16px;
}
.term-divider :deep(.el-divider__text) {
  font-size: 13px;
  color: var(--muted);
}
.token-row {
  display: flex;
  gap: 8px;
  width: 100%;
}
.token-row .el-input {
  flex: 1;
}
/* 有效期限：两个日期选择器同排 */
.date-row {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
}
.date-pick {
  flex: 1;
  min-width: 0;
}
.date-sep {
  color: var(--muted);
  flex: none;
}
</style>
