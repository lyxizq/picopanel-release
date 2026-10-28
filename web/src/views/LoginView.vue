<template>
  <div class="login-wrap">
    <el-card class="login-card">
      <div class="title">
        <img class="logo" src="/favicon.svg" alt="PicoPanel" /><span>PicoPanel</span>
      </div>
      <div class="sub">{{ $t('login.sub') }}</div>
      <el-form @submit.prevent="submit">
        <el-form-item>
          <el-input v-model="username" :placeholder="$t('login.username')" size="large" :prefix-icon="User" />
        </el-form-item>
        <el-form-item>
          <el-input
            v-model="password"
            type="password"
            :placeholder="$t('login.password')"
            size="large"
            show-password
            :prefix-icon="Lock"
          />
        </el-form-item>
        <el-form-item v-if="needCode">
          <el-input
            v-model="code"
            :placeholder="$t('login.code')"
            size="large"
            inputmode="numeric"
            maxlength="6"
            :prefix-icon="Key"
            @keyup.enter="submit"
          />
        </el-form-item>
        <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">
          {{ $t('login.submit') }}
        </el-button>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { User, Lock, Key } from '@element-plus/icons-vue'
import { api, bumpAvatarVer, setAvatar, setToken, setUsername, setRole } from '../api/index.js'

const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const username = ref('')
const password = ref('')
const code = ref('')
const needCode = ref(false)
const loading = ref(false)

async function submit() {
  if (!username.value || !password.value) {
    ElMessage.warning(t('login.needUserPass'))
    return
  }
  if (needCode.value && !code.value.trim()) {
    ElMessage.warning(t('login.needCode'))
    return
  }
  loading.value = true
  try {
    const res = await api.post('/auth/login', {
      username: username.value,
      password: password.value,
      code: code.value.trim(),
    })
    setToken(res.token)
    setUsername(res.username)
    // 头像按用户隔离（44）：换账号登录即清本地缓存并递增版本号，
    // 各页进面重新拉 /auth/avatar，避免显示上一个账号的头像（HTTP 缓存同理）
    setAvatar('')
    bumpAvatarVer()
    // 受限用户（361 role=user）登录后唯一落点是 /ha 智能家居页；redirect 参数对其不生效
    const role = res.role || 'admin'
    setRole(role)
    router.replace(role === 'user' ? '/ha' : route.query.redirect || '/')
  } catch (e) {
    if (e.data?.need_code) needCode.value = true
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-wrap {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg);
}
.login-card {
  width: 360px;
  padding: 12px 8px;
}
.title {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  font-size: 24px;
  font-weight: 700;
  margin-bottom: 4px;
}
.logo {
  width: 40px;
  height: 40px;
  border-radius: 9px;
}
.sub {
  text-align: center;
  color: var(--text-2);
  font-size: 13px;
  margin-bottom: 24px;
}
</style>
