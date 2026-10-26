<template>
  <div class="status">
    <div class="toolbar">
      <span v-if="loadedAt" class="updated">{{ $t('settings.updatedAt', { t: loadedAt }) }}</span>
      <span v-else></span>
      <div class="tools">
        <el-checkbox v-model="autoRefresh">{{ $t('settings.autoRefresh') }}</el-checkbox>
        <el-button :loading="loading" @click="load()">
          <el-icon v-if="!loading"><Refresh /></el-icon>
          <span v-if="!loading">{{ $t('common.refresh') }}</span>
        </el-button>
      </div>
    </div>

    <el-tabs v-model="tab" class="status-tabs">
      <!-- ① 系统信息：界面语言（选择器并入标题行）/ 运行信息 / 监控概况 / 登录日志 -->
      <el-tab-pane :label="$t('settings.tabs.system')" name="system">
        <el-card shadow="never" class="lang-card">
          <template #header>
            <div class="card-head">
              {{ $t('lang.label') }}<span class="card-sub">{{ $t('lang.sub') }}</span>
              <span class="lang-inline">
                <el-select :model-value="lang" style="width: 120px" size="small" @change="setLang">
                  <el-option v-for="l in LANGS" :key="l.key" :label="l.label" :value="l.key" />
                </el-select>
                <span class="hint">{{ $t('lang.hint') }}</span>
              </span>
            </div>
          </template>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              {{ $t('settings.rt.card') }}<span class="head-upd">{{ $t('settings.rt.serverUpdatedAt') }}{{ rt.updated_at ? fmtTime(rt.updated_at) : '-' }}</span>
            </div>
          </template>
          <el-descriptions :column="col" border size="small">
            <el-descriptions-item :label="$t('settings.rt.name')">PicoPanel</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.rt.version')">{{ rt.version || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.rt.uptime')">{{ fmtUptime(rt.uptime) }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.rt.goVersion')">{{ rt.go_version || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.rt.os')">{{ rt.os || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.rt.goroutines')">{{ rt.goroutines ?? '-' }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.rt.mem')">
              {{ fmtBytes(rt.mem_alloc) }} / {{ fmtBytes(rt.mem_sys) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('settings.rt.time')">{{ fmtTime(rt.now) }}</el-descriptions-item>
          </el-descriptions>
          <div class="ops">
            <div class="op-row">
              <el-button type="warning" plain :loading="restarting" @click="restart('upgrade')">{{ $t('settings.rt.upgradeRestart') }}</el-button>
              <el-button type="danger" plain :loading="restarting" @click="restart('restart')">{{ $t('settings.rt.restart') }}</el-button>
              <span class="hint">{{ $t('settings.rt.upgradeHint') }}</span>
            </div>
            <div class="hint">
              {{ $t('settings.rt.restartHint') }}
            </div>
          </div>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">{{ $t('settings.upd.card') }}<span class="card-sub">{{ $t('settings.upd.sub') }}</span></div>
          </template>
          <el-descriptions :column="col" border size="small">
            <el-descriptions-item :label="$t('settings.upd.current')">{{ upd.current || rt.version || '-' }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.upd.latest')">
              <span :class="{ firing: upd.available }">{{ upd.latest || $t('settings.upd.notChecked') }}</span>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('settings.upd.agentLatest')">
              {{ upd.agents_latest || $t('settings.upd.notCached') }}
              <span v-if="upd.agents_upgradable > 0" :class="{ firing: true }">{{ $t('settings.upd.agentsPending', { n: upd.agents_upgradable }) }}</span>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('settings.upd.channel')">
              {{ (upd.channel || 'stable') + (upd.cached ? $t('settings.upd.agentCached') : '') }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('settings.upd.checkTime')">{{ upd.checked_at ? fmtTime(upd.checked_at) : $t('settings.upd.neverChecked') }}</el-descriptions-item>
          </el-descriptions>
          <div v-if="upd.agents_upgradable > 0" class="hint" style="color: var(--el-color-warning);">
            {{ $t('settings.upd.agentsBehind', { n: upd.agents_upgradable }) }}
          </div>
          <div v-if="upd.agents_unknown > 0" class="hint">
            {{ $t('settings.upd.agentsUnknown', { n: upd.agents_unknown }) }}
          </div>
          <!-- 更新日志默认收起（429），点「查看更新日志」展开 -->
          <div v-if="upd.notes" class="upd-notes-wrap">
            <button type="button" class="upd-notes-toggle" @click="notesOpen = !notesOpen">
              <el-icon :class="{ open: notesOpen }"><ArrowDown /></el-icon>{{ notesOpen ? $t('settings.upd.hideNotes') : $t('settings.upd.showNotes') }}
            </button>
            <div v-show="notesOpen" class="hint upd-notes">{{ upd.notes }}</div>
          </div>
          <div v-if="upd.error" class="hint" style="color: var(--el-color-danger);">{{ upd.error }}</div>
          <div v-if="upd.configured === false" class="hint">
            {{ $t('settings.upd.notConfigured') }}
          </div>
          <div class="ops">
            <div class="op-row">
              <el-button type="primary" plain :loading="checking" @click="checkUpdate">{{ $t('settings.upd.check') }}</el-button>
              <el-button type="warning" :disabled="!upd.available" :loading="applying" @click="applyUpdate">
                {{ $t('settings.upd.apply') }}{{ agentAutoHint }}
              </el-button>
            </div>
            <div class="hint">
              {{ $t('settings.upd.applyHint') }}
            </div>
          </div>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">{{ $t('settings.ov.card') }}</div>
          </template>
          <el-descriptions :column="col" border size="small">
            <el-descriptions-item :label="$t('settings.ov.servers')">
              {{ $t('settings.ov.serversDetail', { n: ov.servers, online: ov.online, offline: ov.offline, disabled: ov.disabled }) }}
            </el-descriptions-item>
            <el-descriptions-item :label="$t('settings.ov.mode')">{{ modeText }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.ov.rules')">{{ ov.rules == null ? '-' : $t('settings.ov.rulesN', { n: ov.rules }) }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.ov.firing')">
              <span :class="{ firing: ov.firing > 0 }">{{ ov.firing ?? 0 }}</span>
            </el-descriptions-item>
            <el-descriptions-item :label="$t('settings.ov.history')">{{ ov.alert_total == null ? '-' : $t('settings.ov.historyN', { n: fmtInt(ov.alert_total) }) }}</el-descriptions-item>
          </el-descriptions>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">{{ $t('settings.logins.card') }}<span class="card-sub">{{ $t('settings.logins.sub', { n: logins.length }) }}</span></div>
          </template>
          <!-- 与服务器管理/监控列表同款行列表视觉（无重底色、圆角行、悬停浅白透） -->
          <div v-if="logins.length" class="log-list">
            <div class="log-head">
              <span>{{ $t('settings.logins.user') }}</span><span>{{ $t('settings.logins.time') }}</span><span>{{ $t('settings.logins.ip') }}</span>
            </div>
            <div v-for="(l, i) in logins" :key="i" class="log-row">
              <span class="log-user">{{ l.username }}</span>
              <span>{{ fmtTime(l.ts) }}</span>
              <span class="log-ip">{{ l.ip }}</span>
            </div>
          </div>
          <div v-else class="hint">{{ $t('settings.logins.empty') }}</div>
        </el-card>
      </el-tab-pane>

      <!-- ② 个人信息：头像 / 账号 / 两步验证 -->
      <el-tab-pane :label="$t('settings.tabs.profile')" name="profile">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">{{ $t('settings.account.card') }}<span class="card-sub">{{ $t('settings.account.sub') }}</span></div>
          </template>
          <div class="profile-wrap">
            <div class="avatar-col">
              <div class="avatar-box" :title="avatar ? $t('settings.avatar.change') : $t('settings.avatar.upload')" @click="pickAvatar">
                <img v-if="avatar" :src="avatar" class="avatar-img" :alt="$t('common.avatar')" />
                <el-icon v-else class="avatar-placeholder"><UserFilled /></el-icon>
                <div class="avatar-mask">{{ $t('settings.avatar.mask') }}</div>
              </div>
              <button v-if="avatar" type="button" class="avatar-remove" @click.stop="clearAvatar">{{ $t('settings.avatar.remove') }}</button>
              <input ref="avatarInput" type="file" accept="image/*" class="avatar-input" @change="onAvatarFile" />
            </div>
            <el-form :model="prof" label-width="90px" class="prof-form" @submit.prevent>
              <el-form-item :label="$t('settings.prof.username')">
                <el-input v-model="prof.username" autocomplete="username" :placeholder="$t('settings.prof.phUsername')" />
              </el-form-item>
              <el-form-item :label="$t('settings.prof.old')">
                <el-input v-model="prof.old" type="password" show-password autocomplete="current-password" :placeholder="$t('settings.prof.phOld')" />
              </el-form-item>
              <el-form-item :label="$t('settings.prof.next')">
                <el-input v-model="prof.next" type="password" show-password autocomplete="new-password" :placeholder="$t('settings.prof.phNext')" />
              </el-form-item>
              <el-form-item v-if="prof.next" :label="$t('settings.prof.confirm')">
                <el-input v-model="prof.confirm" type="password" show-password autocomplete="new-password" />
              </el-form-item>
              <el-form-item>
                <el-button type="primary" :loading="profSaving" @click="saveProfile">{{ $t('common.save') }}</el-button>
                <span class="hint" style="margin-left: 10px">{{ $t('settings.prof.hint') }}</span>
              </el-form-item>
            </el-form>
          </div>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">{{ $t('settings.totp.card') }}<span class="card-sub">{{ $t('settings.totp.sub') }}</span></div>
          </template>
          <template v-if="!totpEnabled">
            <div v-if="!totpSetup" class="op-row">
              <el-button type="primary" :loading="totpSettingUp" @click="startTotp">{{ $t('settings.totp.enable') }}</el-button>
              <span class="hint">{{ $t('settings.totp.enableHint') }}</span>
            </div>
            <template v-else>
              <div class="totp-enroll">
                <img v-if="totpSetup.qr" :src="totpSetup.qr" class="totp-qr" alt="TOTP" />
                <div class="totp-info">
                  <div class="hint">
                    {{ $t('settings.totp.manualHint') }}
                  </div>
                  <el-text class="totp-secret">{{ totpSetup.secret }}</el-text>
                  <div class="op-row" style="margin-top: 10px">
                    <el-input
                      v-model="totpCode"
                      :placeholder="$t('settings.totp.phCode')"
                      style="width: 220px"
                      maxlength="6"
                      inputmode="numeric"
                    />
                    <el-button type="primary" :loading="totpSaving" @click="confirmTotp">{{ $t('settings.totp.confirmBtn') }}</el-button>
                    <el-button @click="cancelTotp">{{ $t('common.cancel') }}</el-button>
                  </div>
                </div>
              </div>
            </template>
          </template>
          <template v-else>
            <div class="op-row">
              <el-tag type="success" size="small">{{ $t('settings.totp.on') }}</el-tag>
              <el-input
                v-model="totpCode"
                :placeholder="$t('settings.totp.phDisable')"
                style="width: 240px"
                maxlength="6"
                inputmode="numeric"
              />
              <el-button type="danger" plain :loading="totpSaving" @click="disableTotp">{{ $t('settings.totp.disableBtn') }}</el-button>
            </div>
            <div class="hint" style="margin-top: 6px">{{ $t('settings.totp.disableHint') }}</div>
          </template>
        </el-card>
      </el-tab-pane>

      <!-- ③ 数据库 -->
      <el-tab-pane :label="$t('settings.tabs.db')" name="db">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">{{ $t('settings.tabs.db') }}</div>
          </template>
          <el-descriptions :column="col" border size="small">
            <el-descriptions-item :label="$t('settings.db.size')">{{ fmtBytes(db.size) }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.db.wal')">{{ fmtBytes(db.wal_size) }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.db.rows')">{{ fmtInt(db.rows_approx) }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.db.buckets')">{{ fmtInt(db.archive_rows) }}</el-descriptions-item>
            <el-descriptions-item :label="$t('settings.db.retention')">{{ db.retention ?? '-' }} {{ $t('settings.db.days') }}</el-descriptions-item>
          </el-descriptions>
          <div class="hint">
            {{ $t('settings.db.hint') }}
          </div>

          <div class="ops">
            <div class="op-row">
              <el-input-number
                v-model="retention"
                :min="1"
                :max="3650"
                :step="1"
                step-strictly
                @change="onRetentionChange"
              />
              <span class="unit">{{ $t('settings.db.days') }}</span>
              <el-button type="primary" :loading="saving" @click="saveRetention">{{ $t('common.save') }}</el-button>
            </div>
            <div class="hint">
              {{ $t('settings.db.retentionHint') }}
            </div>
            <div class="op-row" style="margin-top: 10px">
              <el-button type="danger" plain :loading="cleaning" @click="cleanup">
                {{ $t('settings.db.cleanup') }}
              </el-button>
              <el-button type="success" plain :loading="backing" @click="createBackup">
                {{ $t('settings.db.backup') }}
              </el-button>
            </div>
          </div>
        </el-card>
      </el-tab-pane>

      <!-- ④ 消息推送：Web Push / WxPusher / Webhook / 一键巡检 -->
      <el-tab-pane :label="$t('settings.tabs.push')" name="push">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              Web Push<span class="card-sub">{{ $t('settings.wp.sub') }}</span>
              <span class="master-switch">
                <span class="master-label">{{ wpOn ? $t('settings.on') : $t('settings.off') }}</span>
                <el-switch v-model="wpOn" @change="saveWpOn" />
              </span>
            </div>
          </template>
          <el-collapse-transition>
            <div v-show="wpOn">
              <div v-if="!wpSupported" class="hint">
                {{ $t('settings.wp.unsupported') }}
              </div>
              <template v-else>
            <div class="op-row">
              <el-button v-if="!wp.subscribed" type="primary" :loading="wp.busy" :disabled="!wpOn" @click="subscribePush">
                {{ $t('settings.wp.subscribe') }}
              </el-button>
              <el-button v-else :loading="wp.busy" :disabled="!wpOn" @click="unsubscribePush">{{ $t('settings.wp.unsubscribe') }}</el-button>
              <el-button :loading="testingWp" :disabled="!wpOn" @click="testWp">{{ $t('settings.wp.test') }}</el-button>
              <span class="hint">
                {{ wp.subscribed ? $t('settings.wp.subscribedHint') : $t('settings.wp.subscribeHint') }}
              </span>
            </div>
            <div class="mail-switches">
              <el-checkbox v-model="alertWpOn" :disabled="!wpOn" @change="saveAlertWpOn">{{ $t('settings.wp.cbAlert') }}</el-checkbox>
              <el-checkbox v-model="certWpOn" :disabled="!wpOn" @change="saveCertWpOn">{{ $t('settings.wp.cbCert') }}</el-checkbox>
              <el-checkbox v-model="reminderWpOn" :disabled="!wpOn" @change="saveReminderWpOn">{{ $t('settings.wp.cbReminder') }}</el-checkbox>
              <el-checkbox v-model="loginWpOn" :disabled="!wpOn" @change="saveLoginWpOn">{{ $t('settings.wp.cbLogin') }}</el-checkbox>
              <el-checkbox v-model="mailWpOn" :disabled="!wpOn" @change="saveMailWpOn">{{ $t('settings.wp.cbMail') }}</el-checkbox>
              <el-checkbox v-model="tvWpOn" :disabled="!wpOn" @change="saveTvWpOn">{{ $t('settings.wp.cbTv') }}</el-checkbox>
              <el-checkbox v-model="assistWpOn" :disabled="!wpOn" @change="saveAssistWpOn">{{ $t('settings.wp.cbAssist') }}</el-checkbox>
            </div>
            <div v-if="wp.subs.length" class="insp-sec">{{ $t('settings.wp.subsN', { n: wp.subs.length }) }}</div>
            <div v-for="s in wp.subs" :key="s.id" class="insp-line sub-line">
              <span class="sub-txt">{{ s.ua || $t('settings.unknownBrowser') }} · {{ $t('settings.wp.subscribedAt', { t: fmtTime(s.created_at) }) }}<span v-if="s.fp === selfFp" class="sub-self">{{ $t('settings.wp.selfBrowser') }}</span></span>
              <el-button size="small" text type="danger" class="sub-del" @click="deleteSub(s)">{{ $t('common.delete') }}</el-button>
            </div>
            <div class="hint" style="margin-top: 8px">
              {{ $t('settings.wp.hint') }}
            </div>
          </template>
            </div>
          </el-collapse-transition>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              WxPusher<span class="card-sub">{{ $t('settings.wx.sub') }}</span>
              <span class="master-switch">
                <span class="master-label">{{ wxOn ? $t('settings.on') : $t('settings.off') }}</span>
                <el-switch v-model="wxOn" @change="saveWxOn" />
              </span>
            </div>
          </template>
          <el-collapse-transition>
            <div v-show="wxOn">
              <el-form label-width="90px" size="small" class="mail-form" :disabled="!wxOn">
            <div class="mail-grid">
              <el-form-item label="APP_TOKEN">
                <el-input
                  v-model="wx.token"
                  type="password"
                  show-password
                  :placeholder="wx.hasToken ? $t('settings.savedNoEcho') : $t('settings.wx.phToken')"
                />
              </el-form-item>
              <el-form-item label="UID">
                <el-input v-model="wx.uids" :placeholder="$t('settings.wx.phUids')" />
              </el-form-item>
              <el-form-item :label="$t('settings.wx.topicId')">
                <el-input v-model="wx.topics" :placeholder="$t('settings.wx.phTopics')" />
              </el-form-item>
            </div>
            <div class="hint">
              {{ $t('settings.wx.hint') }}
            </div>
            <div class="mail-switches">
              <el-checkbox v-model="alertWxOn">{{ $t('settings.wx.cbAlert') }}</el-checkbox>
              <el-checkbox v-model="certWxOn">{{ $t('settings.wx.cbCert') }}</el-checkbox>
              <el-checkbox v-model="reminderWxOn">{{ $t('settings.wx.cbReminder') }}</el-checkbox>
              <el-checkbox v-model="loginWxOn">{{ $t('settings.wx.cbLogin') }}</el-checkbox>
              <el-checkbox v-model="mailWxOn">{{ $t('settings.wx.cbMail') }}</el-checkbox>
              <el-checkbox v-model="tvWxOn">{{ $t('settings.wx.cbTv') }}</el-checkbox>
              <el-checkbox v-model="assistWxOn">{{ $t('settings.wx.cbAssist') }}</el-checkbox>
            </div>
            <div class="op-row">
              <el-button type="primary" :loading="savingMail" @click="saveMail">{{ $t('common.save') }}</el-button>
              <el-button :loading="testing" @click="testMail">{{ $t('settings.wx.test') }}</el-button>
              <span class="hint">{{ $t('settings.wx.saveHint') }}</span>
            </div>
          </el-form>
            </div>
          </el-collapse-transition>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              {{ $t('settings.wh.card') }}<span class="card-sub">{{ $t('settings.wh.sub') }}</span>
              <span class="master-switch">
                <span class="master-label">{{ whOn ? $t('settings.on') : $t('settings.off') }}</span>
                <el-switch v-model="whOn" @change="saveWhOn" />
              </span>
            </div>
          </template>
          <el-collapse-transition>
            <div v-show="whOn">
              <el-form label-width="90px" size="small" class="mail-form" :disabled="!whOn">
            <el-form-item label="URL">
              <el-input
                v-model="wh.url"
                type="password"
                show-password
                :placeholder="wh.hasURL ? $t('settings.savedNoEcho') : $t('settings.wh.phUrl')"
              />
            </el-form-item>
            <el-form-item :label="$t('settings.wh.template')">
              <el-input
                v-model="wh.template"
                type="textarea"
                :rows="4"
                :placeholder="whTemplatePh"
              />
            </el-form-item>
            <div class="hint">
              {{ $t('settings.wh.hintA') }}<code v-pre>{{title}}</code>{{ $t('settings.wh.hintB') }}<code v-pre>{{message}}</code>{{ $t('settings.wh.hintC') }}<code v-pre>"content": {{message}}</code>{{ $t('settings.wh.hintD') }}
            </div>
            <div class="mail-switches">
              <el-checkbox v-model="alertWhOn">{{ $t('settings.wh.cbAlert') }}</el-checkbox>
              <el-checkbox v-model="certWhOn">{{ $t('settings.wh.cbCert') }}</el-checkbox>
              <el-checkbox v-model="reminderWhOn">{{ $t('settings.wh.cbReminder') }}</el-checkbox>
              <el-checkbox v-model="loginWhOn">{{ $t('settings.wh.cbLogin') }}</el-checkbox>
              <el-checkbox v-model="mailWhOn">{{ $t('settings.wh.cbMail') }}</el-checkbox>
              <el-checkbox v-model="tvWhOn">{{ $t('settings.wh.cbTv') }}</el-checkbox>
              <el-checkbox v-model="assistWhOn">{{ $t('settings.wh.cbAssist') }}</el-checkbox>
            </div>
            <div class="op-row">
              <el-button type="primary" :loading="savingWh" @click="saveWh">{{ $t('common.save') }}</el-button>
              <el-button :loading="testingWh" @click="testWh">{{ $t('settings.wh.test') }}</el-button>
              <span class="hint">{{ $t('settings.wh.saveHint') }}</span>
            </div>
          </el-form>
            </div>
          </el-collapse-transition>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">{{ $t('settings.insp.card') }}<span class="card-sub">{{ $t('settings.insp.sub') }}</span></div>
          </template>
          <div class="op-row">
            <el-button type="primary" :loading="inspLoading" @click="runInspect">{{ $t('settings.insp.run') }}</el-button>
            <el-button type="success" plain :loading="inspPushing" @click="pushInspect">{{ $t('settings.insp.push') }}</el-button>
            <span class="hint">{{ $t('settings.insp.hint') }}</span>
          </div>

          <template v-if="insp">
            <el-descriptions :column="col" border size="small" style="margin-top: 14px">
              <el-descriptions-item :label="$t('settings.insp.time')">{{ fmtTime(insp.generated_at) }}</el-descriptions-item>
              <el-descriptions-item :label="$t('settings.insp.servers')">
                {{ $t('settings.insp.serversDetail', { n: insp.total, online: insp.online, offline: insp.offline, disabled: insp.disabled }) }}
              </el-descriptions-item>
              <el-descriptions-item :label="$t('settings.insp.firing')">
                <span :class="{ firing: insp.firing_count > 0 }">{{ $t('settings.insp.firingN', { n: insp.firing_count }) }}</span>
              </el-descriptions-item>
              <el-descriptions-item :label="$t('settings.insp.certs')">{{ $t('settings.insp.certsN', { n: insp.cert_due }) }}</el-descriptions-item>
              <el-descriptions-item :label="$t('settings.insp.mail')">
                <span :class="{ firing: insp.mail_unread > 0 }">{{ $t('settings.insp.mailN', { n: insp.mail_unread }) }}</span>
              </el-descriptions-item>
            </el-descriptions>

            <template v-if="insp.firing_alert?.length">
              <div class="insp-sec">{{ $t('settings.insp.secFiring') }}</div>
              <div v-for="(a, i) in insp.firing_alert" :key="i" class="insp-line">{{ a }}</div>
            </template>
            <template v-if="insp.disk_warn?.length">
              <div class="insp-sec">{{ $t('settings.insp.secDisk') }}</div>
              <div v-for="(d, i) in insp.disk_warn" :key="i" class="insp-line">{{ d }}</div>
            </template>
            <template v-if="insp.certs?.length">
              <div class="insp-sec">{{ $t('settings.insp.secCerts') }}</div>
              <div v-for="(ct, i) in insp.certs" :key="i" class="insp-line">
                {{ ct.domain }}（{{ ct.days < 0 ? $t('settings.insp.expiredN', { n: -ct.days }) : $t('settings.insp.leftN', { n: ct.days }) }}）
              </div>
            </template>

            <template v-if="insp.mail_lines?.length">
              <div class="insp-sec">{{ $t('settings.insp.secMail') }}</div>
              <div v-for="(m, i) in insp.mail_lines" :key="i" class="insp-line">{{ m }}</div>
            </template>

            <div class="insp-sec">{{ $t('settings.insp.secServers') }}</div>
            <el-table :data="insp.servers" size="small" max-height="360">
              <el-table-column prop="name" :label="$t('settings.insp.colName')" min-width="140" show-overflow-tooltip />
              <el-table-column :label="$t('settings.insp.colStatus')" width="90">
                <template #default="{ row }">
                  <span v-if="!row.enabled" class="muted">{{ $t('settings.status.disabled') }}</span>
                  <span v-else-if="row.online" class="ok">{{ $t('settings.status.online') }}</span>
                  <span v-else class="firing">{{ $t('settings.status.offline') }}</span>
                </template>
              </el-table-column>
              <el-table-column label="CPU" width="90">
                <template #default="{ row }">{{ row.online ? fmtPercent(row.cpu) : '-' }}</template>
              </el-table-column>
              <el-table-column :label="$t('settings.insp.colMem')" width="90">
                <template #default="{ row }">{{ row.online ? fmtPercent(row.mem) : '-' }}</template>
              </el-table-column>
              <el-table-column :label="$t('settings.insp.colDisk')" min-width="150">
                <template #default="{ row }">
                  <span v-if="row.online && row.disk_mount">{{ row.disk_mount }} {{ fmtPercent(row.disk) }}</span>
                  <span v-else>-</span>
                </template>
              </el-table-column>
              <el-table-column :label="$t('settings.insp.colFiring')" width="80">
                <template #default="{ row }">{{ row.firing || '-' }}</template>
              </el-table-column>
            </el-table>
          </template>
          <div v-else class="hint" style="margin-top: 10px">{{ $t('settings.insp.empty') }}</div>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">{{ $t('settings.tv.card') }}<span class="card-sub">{{ $t('settings.tv.sub') }}</span></div>
          </template>
          <el-form label-width="90px" size="small" class="mail-form">
            <div class="mail-grid">
              <el-form-item label="TMDB Key">
                <el-input
                  v-model="tvTmdbKey"
                  type="password"
                  show-password
                  :placeholder="tvTmdbHasKey ? $t('settings.savedNoEcho') : $t('settings.tv.phKey')"
                />
              </el-form-item>
              <el-form-item :label="$t('settings.tv.egress')">
                <el-select v-model="tvEgress" style="width: 100%">
                  <el-option v-for="s in tvServers" :key="s.id" :label="s.name" :value="s.id" />
                </el-select>
              </el-form-item>
            </div>
            <div class="hint">
              {{ $t('settings.tv.hint') }}
            </div>
            <div class="op-row">
              <el-button type="primary" :loading="savingTv" @click="saveTvConfig">{{ $t('common.save') }}</el-button>
              <span class="hint">{{ $t('settings.tv.hint2') }}</span>
            </div>
          </el-form>
        </el-card>

        <!-- 个人助理（2026-10-26 批）：早报/天气预警/带伞/空气提醒；渠道走上方三卡的「助理」细分开关 -->
        <el-card shadow="never">
          <template #header>
            <div class="card-head">{{ $t('settings.assist.card') }}<span class="card-sub">{{ $t('settings.assist.sub') }}</span></div>
          </template>
          <div class="op-row assist-row">
            <el-checkbox v-model="assist.brief_on">{{ $t('settings.assist.brief') }}</el-checkbox>
            <el-input v-model="assist.brief_time" style="width: 100px" placeholder="08:00" />
            <el-button plain :loading="assistPushing" @click="briefNow">{{ $t('settings.assist.briefNow') }}</el-button>
            <el-button plain :loading="assistPreviewing" @click="briefPreview">{{ $t('settings.assist.preview') }}</el-button>
          </div>
          <div class="hint">{{ $t('settings.assist.briefHint') }}</div>
          <div class="op-row assist-row" style="margin-top: 10px">
            <el-checkbox v-model="assist.warn_on">{{ $t('settings.assist.warn') }}</el-checkbox>
            <span class="hint">{{ $t('settings.assist.warnHint') }}</span>
          </div>
          <div class="op-row assist-row" style="margin-top: 10px">
            <el-checkbox v-model="assist.umbrella_on">{{ $t('settings.assist.umbrella') }}</el-checkbox>
            <el-input v-model="assist.umbrella_time" style="width: 100px" placeholder="20:00" />
            <span class="hint">{{ $t('settings.assist.umbrellaThresh') }}</span>
            <el-input-number v-model="assist.umbrella_threshold" :min="1" :max="100" size="small" style="width: 110px" />
            <span class="hint">%</span>
          </div>
          <div class="op-row assist-row" style="margin-top: 10px">
            <el-checkbox v-model="assist.air_on">{{ $t('settings.assist.air') }}</el-checkbox>
            <span class="hint">{{ $t('settings.assist.airThresh') }}</span>
            <el-input-number v-model="assist.air_threshold" :min="50" :max="500" :step="50" size="small" style="width: 120px" />
          </div>
          <div class="op-row assist-row" style="margin-top: 10px">
            <el-button type="primary" :loading="savingAssist" @click="saveAssist">{{ $t('common.save') }}</el-button>
            <span class="hint">{{ $t('settings.assist.hint') }}</span>
          </div>
          <pre v-if="assistPreview" class="assist-preview">{{ assistPreview }}</pre>
        </el-card>
      </el-tab-pane>

      <el-tab-pane :label="$t('settings.tabs.users')" name="users">
        <el-card shadow="never" class="user-card">
          <template #header>
            <div class="user-head">
              <span>{{ $t('settings.users.head') }}</span>
              <el-button type="primary" size="small" @click="openUserCreate">{{ $t('settings.users.create') }}</el-button>
            </div>
          </template>
          <el-table :data="users" size="small" class="row-table">
            <el-table-column prop="username" :label="$t('settings.users.colUser')" min-width="140" />
            <el-table-column :label="$t('settings.users.colRole')" width="110">
              <template #default="{ row }">
                <el-tag :type="row.role === 'admin' ? 'danger' : 'info'" size="small">
                  {{ row.role === 'admin' ? $t('settings.users.admin') : $t('settings.users.limited') }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column :label="$t('settings.users.colCreated')" width="170">
              <template #default="{ row }">{{ row.created_at ? fmtTime(row.created_at) : '-' }}</template>
            </el-table-column>
            <el-table-column :label="$t('settings.users.colOps')" width="190">
              <template #default="{ row }">
                <el-button size="small" @click="openUserEdit(row)">{{ $t('common.edit') }}</el-button>
                <el-button size="small" type="danger" plain @click="removeUser(row)">{{ $t('common.delete') }}</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>

        <el-dialog v-model="userDlg.open" :title="userDlg.id ? $t('settings.users.editTitle') : $t('settings.users.createTitle')" width="420px" append-to-body>
          <el-form label-width="82px">
            <el-form-item :label="$t('settings.users.username')">
              <el-input v-model="userDlg.username" :disabled="!!userDlg.id" maxlength="100" />
            </el-form-item>
            <el-form-item :label="$t('settings.users.password')" :required="!userDlg.id">
              <el-input v-model="userDlg.password" type="password" show-password
                        :placeholder="userDlg.id ? $t('settings.users.phKeepPass') : $t('settings.users.phMinLen')" maxlength="100" />
            </el-form-item>
            <el-form-item :label="$t('settings.users.role')">
              <el-radio-group v-model="userDlg.role">
                <el-radio value="admin">{{ $t('settings.users.admin') }}</el-radio>
                <el-radio value="user">{{ $t('settings.users.radioLimited') }}</el-radio>
              </el-radio-group>
            </el-form-item>
          </el-form>
          <template #footer>
            <el-button @click="userDlg.open = false">{{ $t('common.cancel') }}</el-button>
            <el-button type="primary" :loading="userDlg.saving" @click="saveUser">{{ $t('common.save') }}</el-button>
          </template>
        </el-dialog>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup>
// 系统设置页（四标签：系统信息 / 个人信息 / 数据库 / 消息推送）。
// 系统信息与数据库只读展示 GET /status；个人信息承接原 ProfileDialog 弹层
// （用户名/密码 + 头像）与 TOTP 两步验证；消息推送承接原推送与巡检配置。
defineOptions({ name: 'StatusView' })
import { computed, onActivated, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useIsMobile } from '../composables/useIsMobile.js'
import { useRoute } from 'vue-router'
import { ArrowDown, UserFilled } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, bumpAvatarVer, getAvatar, setAvatar, setUsername } from '../api/index.js'
import { compressAvatar } from '../utils/avatar.js'
import { fmtBytes, fmtPercent, fmtUptime, fmtTime } from '../utils/format.js'
import { LANGS, lang, setLang } from '../i18n'

const { t } = useI18n()

// 采集模式 label 的 i18n key（modeText computed 内 t() 取当前语言）
const MODE_KEYS = { agent: 'settings.modes.agent', push: 'settings.modes.push', ssh: 'settings.modes.ssh', node: 'settings.modes.node' }

const route = useRoute()
// 标签页：支持 ?tab= 深链（顶栏「个人信息」下拉直达），未知值回落 system
const tab = ref(['profile', 'system', 'db', 'push', 'users'].includes(route.query.tab) ? route.query.tab : 'system')
watch(
  () => route.query.tab,
  (t) => {
    if (t && ['profile', 'system', 'db', 'push', 'users'].includes(t)) tab.value = t
  },
)

// ---- 用户管理（361 多用户）：管理员维护其他登录账号 ----
const users = ref([])
const userDlg = reactive({ open: false, id: 0, username: '', password: '', role: 'user', saving: false })

async function loadUsers() {
  try {
    const r = await api.get('/auth/users')
    users.value = r.users || []
  } catch (e) {
    ElMessage.error(e.message)
  }
}

function openUserCreate() {
  Object.assign(userDlg, { open: true, id: 0, username: '', password: '', role: 'user', saving: false })
}

function openUserEdit(row) {
  Object.assign(userDlg, { open: true, id: row.id, username: row.username, password: '', role: row.role, saving: false })
}

async function saveUser() {
  const username = userDlg.username.trim()
  if (!username) {
    ElMessage.warning(t('settings.users.needName'))
    return
  }
  if (userDlg.password && userDlg.password.length < 6) {
    ElMessage.warning(t('settings.users.shortPass'))
    return
  }
  userDlg.saving = true
  try {
    if (userDlg.id) {
      const body = { role: userDlg.role }
      if (userDlg.password) body.password = userDlg.password
      await api.put(`/auth/users/${userDlg.id}`, body)
    } else {
      if (!userDlg.password) {
        ElMessage.warning(t('settings.users.needPass'))
        return
      }
      await api.post('/auth/users', { username, password: userDlg.password, role: userDlg.role })
    }
    userDlg.open = false
    ElMessage.success(t('common.saved'))
    loadUsers()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    userDlg.saving = false
  }
}

async function removeUser(row) {
  try {
    await ElMessageBox.confirm(t('settings.users.delConfirm', { name: row.username }), t('settings.users.delTitle'), {
      type: 'warning', confirmButtonText: t('common.delete'), cancelButtonText: t('common.cancel'),
    })
  } catch {
    return
  }
  try {
    await api.del(`/auth/users/${row.id}`)
    ElMessage.success(t('common.deleted'))
    loadUsers()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

watch(tab, (t) => {
  if (t === 'users' && !users.value.length) loadUsers()
})

const info = reactive({ runtime: {}, db: {}, overview: {}, logins: [] })
const loading = ref(false)
const loadedAt = ref('')
const autoRefresh = ref(false)
let reqSeq = 0
let timer = null

// 数据维护（数据库标签页）
const retention = ref(90)
const saving = ref(false)
const cleaning = ref(false)
let retentionTouched = false // 编辑中不被 30s 自动刷新弹回

// WxPusher 配置（后端 settings 表；appToken 不回显，留空 = 沿用旧值）
const wx = reactive({ token: '', uids: '', topics: '', hasToken: false })
const wxOn = ref(false)
const alertWxOn = ref(false)
const certWxOn = ref(false)
const reminderWxOn = ref(false)
const loginWxOn = ref(false)
const mailWxOn = ref(false)
const savingMail = ref(false)
const testing = ref(false)
const restarting = ref(false)

// 通用 Webhook 配置（URL 可能携带密钥不回显，留空 = 沿用旧值；模板可回显）
const wh = reactive({ url: '', template: '', hasURL: false })
// 默认模板占位符含字面 {{ }}，不能走 i18n 消息编译器（会被当插值吃掉），文案+拼接
const whTemplatePh = computed(() => `${t('settings.wh.phTemplatePre')}{"title": {{title}}, "content": {{message}}}`)
const whOn = ref(false)
const alertWhOn = ref(false)
const certWhOn = ref(false)
const reminderWhOn = ref(false)
const loginWhOn = ref(false)
const mailWhOn = ref(false)
const savingWh = ref(false)
const testingWh = ref(false)

// Web Push：总开关 + 本浏览器订阅态 + 全部订阅列表
const wpOn = ref(false)
const alertWpOn = ref(false)
const certWpOn = ref(false)
const reminderWpOn = ref(false)
const loginWpOn = ref(false)
const mailWpOn = ref(false)
const testingWp = ref(false)

// 追剧（548）：TMDB Key 与出口 agent（全局配置；Key 不回显，留空 = 沿用旧值）
const tvWxOn = ref(false)
const tvWhOn = ref(false)
const tvWpOn = ref(false)
// 个人助理（2026-10-26 批）：三渠道「助理」细分开关走 /settings（与追剧同形态），
// 早报/预警/带伞/空气配置整组走 /assistant/settings
const assistWxOn = ref(true)
const assistWhOn = ref(true)
const assistWpOn = ref(true)
const assist = reactive({ brief_on: true, brief_time: '08:00', warn_on: true, umbrella_on: true, umbrella_time: '20:00', umbrella_threshold: 50, air_on: true, air_threshold: 150 })
const savingAssist = ref(false)
const assistPushing = ref(false)
const assistPreviewing = ref(false)
const assistPreview = ref('')
async function loadAssist() {
  try {
    Object.assign(assist, await api.get('/assistant/settings'))
  } catch {
    /* 助理配置加载失败不阻塞状态页 */
  }
}
async function saveAssist() {
  savingAssist.value = true
  try {
    Object.assign(assist, await api.put('/assistant/settings', { ...assist }))
    ElMessage.success(t('common.saved'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    savingAssist.value = false
  }
}
async function briefNow() {
  assistPushing.value = true
  try {
    await api.post('/assistant/brief-now')
    ElMessage.success(t('settings.assist.pushed'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    assistPushing.value = false
  }
}
async function briefPreview() {
  assistPreviewing.value = true
  try {
    const r = await api.get('/assistant/brief-preview')
    assistPreview.value = `${r.title}\n${r.body}`
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    assistPreviewing.value = false
  }
}
async function saveAssistWpOn() {
  try {
    await api.put('/settings', { assist_webpush_on: assistWpOn.value })
  } catch (e) {
    assistWpOn.value = !assistWpOn.value
    ElMessage.error(e.message)
  }
}
const tvTmdbKey = ref('')
const tvTmdbHasKey = ref(false)
const tvEgress = ref(0)
const tvServers = ref([])
const savingTv = ref(false)

async function loadTvServers() {
  try {
    const res = await api.get('/servers')
    tvServers.value = (res.servers || []).filter((x) => x && x.collect_mode === 'push')
  } catch { /* 出口候选加载失败不阻塞设置页 */ }
}

async function saveTvConfig() {
  savingTv.value = true
  try {
    const body = { tv_egress_server_id: Number(tvEgress.value) || 0 }
    const key = tvTmdbKey.value.trim()
    if (key) body.tv_tmdb_key = key
    await api.put('/settings', body)
    tvTmdbKey.value = ''
    ElMessage.success(t('settings.tv.saved'))
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    savingTv.value = false
  }
}

async function saveTvWpOn() {
  try {
    await api.put('/settings', { tv_webpush_on: tvWpOn.value })
  } catch (e) {
    tvWpOn.value = !tvWpOn.value
    ElMessage.error(e.message)
  }
}
const wpSupported = 'serviceWorker' in navigator && 'PushManager' in window
const wp = reactive({ subscribed: false, busy: false, subs: [] })
// 本浏览器订阅的 endpoint 短指纹（与服务端列表 fp 同算法：SHA-256 前 16 hex），
// 用于识别列表里哪条是本浏览器、删除自己的订阅时同步复位本地状态
const selfFp = ref('')

async function computeSelfFp() {
  try {
    const reg = await navigator.serviceWorker.ready
    const sub = await reg.pushManager.getSubscription()
    if (!sub) { selfFp.value = ''; return }
    const buf = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(sub.endpoint))
    selfFp.value = [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, '0')).join('').slice(0, 16)
  } catch {
    selfFp.value = ''
  }
}

// 删除一条订阅（仅删服务端记录；是本浏览器自己的订阅时顺带本地退订复位）
async function deleteSub(s) {
  try {
    await ElMessageBox.confirm(
      t('settings.wp.delConfirm', { ua: s.ua || t('settings.unknownBrowser') }),
      t('settings.wp.delTitle'),
      { type: 'warning', confirmButtonText: t('common.delete'), cancelButtonText: t('common.cancel') },
    )
  } catch { return }
  try {
    await api.del(`/webpush/subs/${s.id}`)
    if (s.fp && s.fp === selfFp.value) {
      try {
        const reg = await navigator.serviceWorker.ready
        const sub = await reg.pushManager.getSubscription()
        await sub?.unsubscribe()
      } catch { /* 本地状态尽力清理 */ }
      wp.subscribed = false
      selfFp.value = ''
      ElMessage.success(t('settings.wp.unsubscribedMsg'))
    } else {
      ElMessage.success(t('common.deleted'))
    }
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 一键巡检：报告现查现算不落库；每日定时推送已并入个人助理早报（巡检段），此处只剩手动生成/推送
const insp = ref(null)
const inspLoading = ref(false)
const inspPushing = ref(false)

// 登录两步验证（TOTP，45 按用户隔离）：setup 后待确认，验码通过才启用；
// 关闭也需验码。状态走 /auth/totp-status（本账号），不再随全局 settings 返回
const totpEnabled = ref(false)
const totpSetup = ref(null) // {secret, uri, qr}
const totpCode = ref('')
const totpSettingUp = ref(false)
const totpSaving = ref(false)

async function loadTotp() {
  try {
    const st = await api.get('/auth/totp-status')
    totpEnabled.value = !!st.enabled
  } catch {
    /* 状态加载失败不阻塞个人信息页 */
  }
}

// 个人信息（原 ProfileDialog 内联）：用户名/密码修改 + 头像
const prof = reactive({ username: '', old: '', next: '', confirm: '' })
const profSaving = ref(false)
const avatar = ref(getAvatar())
const avatarInput = ref(null)

const rt = computed(() => info.runtime || {})
const db = computed(() => info.db || {})
const ov = computed(() => info.overview || {})
const logins = computed(() => info.logins || [])

// 采集模式分布：按固定顺序拼「Agent 拉取 2 · SSH 直连 1」
const modeText = computed(() => {
  const mc = ov.value.mode_count || {}
  const parts = ['agent', 'push', 'ssh', 'node']
    .filter((m) => mc[m] > 0)
    .map((m) => `${t(MODE_KEYS[m])} ${mc[m]}`)
  return parts.length ? parts.join(' · ') : '-'
})

async function load(showLoading = true) {
  const seq = ++reqSeq
  if (showLoading) loading.value = true
  try {
    const res = await api.get('/status')
    if (seq !== reqSeq) return
    info.runtime = res.runtime || {}
    info.db = res.db || {}
    info.overview = res.overview || {}
    info.logins = res.logins || []
    loadedAt.value = new Date().toLocaleTimeString(lang.value === 'en' ? 'en-US' : 'zh-CN', { hour12: false })
    loadSettings()
  } catch (e) {
    if (seq === reqSeq && showLoading) ElMessage.error(e.message)
  } finally {
    if (seq === reqSeq && showLoading) loading.value = false
  }
}

// WxPusher 配置：仅在未被编辑时回填（避免自动刷新清掉输入中的 token）
let wxTouched = false
watch(
  () => wx.token,
  () => {
    wxTouched = true
  },
)
// Webhook 模板编辑中不被自动刷新弹回
let whTouched = false
watch(
  () => wh.template,
  () => {
    whTouched = true
  },
)
async function loadSettings() {
  loadAssist()
  try {
    const res = await api.get('/settings')
    if (res.wx && !wxTouched) {
      wx.uids = (res.wx.uids || []).join(', ')
      wx.topics = (res.wx.topic_ids || []).join(', ')
      wx.hasToken = !!res.wx.has_token
      wx.token = ''
    }
    alertWxOn.value = !!res.alert_wx_on
    certWxOn.value = !!res.cert_wx_on
    reminderWxOn.value = !!res.reminder_wx_on
    loginWxOn.value = !!res.login_wx_on
    mailWxOn.value = !!res.mail_wx_on
    if (res.webhook && !whTouched) {
      wh.template = res.webhook.template || ''
      wh.hasURL = !!res.webhook.has_url
      wh.url = ''
    }
    alertWhOn.value = !!res.alert_webhook_on
    certWhOn.value = !!res.cert_webhook_on
    reminderWhOn.value = !!res.reminder_wh_on
    loginWhOn.value = !!res.login_wh_on
    mailWhOn.value = !!res.mail_wh_on
    wxOn.value = !!res.wx_on
    whOn.value = !!res.webhook_on
    wpOn.value = !!res.webpush_on
    alertWpOn.value = !!res.alert_webpush_on
    certWpOn.value = !!res.cert_webpush_on
    reminderWpOn.value = !!res.reminder_webpush_on
    loginWpOn.value = !!res.login_webpush_on
    mailWpOn.value = !!res.mail_webpush_on
    tvWxOn.value = !!res.tv_wx_on
    tvWhOn.value = !!res.tv_wh_on
    tvWpOn.value = !!res.tv_webpush_on
    assistWxOn.value = res.assist_wx_on !== false
    assistWhOn.value = res.assist_wh_on !== false
    assistWpOn.value = res.assist_webpush_on !== false
    tvTmdbHasKey.value = !!res.tv_tmdb_has_key
    tvEgress.value = res.tv_egress_server_id || 0
    wp.subs = res.webpush?.subs || []
    if (wpSupported && wpOn.value) refreshWpSubscription()
  } catch {
    /* 设置加载失败不阻塞状态页 */
  }
}

// 服务端保留天数 → 输入框（仅未编辑时同步）
watch(
  () => db.value.retention,
  (v) => {
    if (!retentionTouched && v) retention.value = v
  },
)

function onRetentionChange() {
  retentionTouched = true
}

async function saveRetention() {
  if (!retention.value || retention.value < 1) {
    ElMessage.warning(t('settings.db.minRetention'))
    return
  }
  saving.value = true
  try {
    await api.put('/settings', { retention_days: retention.value })
    retentionTouched = false
    ElMessage.success(t('settings.db.savedRetention', { n: retention.value }))
    load(false)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function cleanup() {
  try {
    await ElMessageBox.confirm(
      t('settings.db.cleanupConfirm'),
      t('settings.db.cleanupTitle'),
      { type: 'warning', confirmButtonText: t('settings.db.cleanupBtn') },
    )
  } catch {
    return
  }
  cleaning.value = true
  try {
    const res = await api.post('/settings/cleanup')
    ElMessage.success(
      t('settings.db.cleaned', { metrics: res.metrics_deleted, alerts: res.alerts_deleted, size: fmtBytes(res.db_size) }),
    )
    load(false)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    cleaning.value = false
  }
}

// 创建数据库备份：服务端生成一致性快照并 gzip 压缩到库同目录 backup/ 下
const backing = ref(false)
let backupConfirmOpen = false // 防连点叠出第二个确认框——双确认必然撞服务端「备份进行中」409
async function createBackup() {
  if (backing.value || backupConfirmOpen) return
  backupConfirmOpen = true
  try {
    await ElMessageBox.confirm(
      t('settings.db.backupConfirm'),
      t('settings.db.backupTitle'),
      { type: 'info', confirmButtonText: t('settings.db.backupBtn') },
    )
  } catch {
    return
  } finally {
    backupConfirmOpen = false
  }
  backing.value = true
  try {
    const res = await api.post('/settings/db-backup')
    ElMessage.success(t('settings.db.backupDone', { file: res.file, size: fmtBytes(res.size) }), { duration: 6000 })
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    backing.value = false
  }
}

async function saveMail() {
  const uids = wx.uids.split(/[,;，；\s]+/).filter(Boolean)
  const topics = wx.topics
    .split(/[,;，；\s]+/)
    .map((s) => parseInt(s, 10))
    .filter((n) => Number.isFinite(n) && n > 0)
  if (!uids.length && !topics.length) {
    ElMessage.warning(t('settings.wx.needUid'))
    return
  }
  savingMail.value = true
  try {
    await api.put('/settings', {
      wx: {
        app_token: wx.token, // 空 = 沿用旧 token
        uids,
        topic_ids: topics,
      },
      alert_wx_on: alertWxOn.value,
      cert_wx_on: certWxOn.value,
      reminder_wx_on: reminderWxOn.value,
      login_wx_on: loginWxOn.value,
      mail_wx_on: mailWxOn.value,
      tv_wx_on: tvWxOn.value,
      assist_wx_on: assistWxOn.value,
    })
    wxTouched = false
    wx.token = ''
    ElMessage.success(t('settings.wx.saved'))
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    savingMail.value = false
  }
}

async function testMail() {
  testing.value = true
  try {
    await api.post('/settings/wxpusher-test')
    ElMessage.success(t('settings.wx.testSent'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testing.value = false
  }
}

async function saveWh() {
  if (!wh.url && !wh.hasURL) {
    ElMessage.warning(t('settings.wh.needUrl'))
    return
  }
  savingWh.value = true
  try {
    await api.put('/settings', {
      webhook: {
        url: wh.url, // 空 = 沿用旧 URL
        template: wh.template,
      },
      alert_webhook_on: alertWhOn.value,
      cert_webhook_on: certWhOn.value,
      reminder_wh_on: reminderWhOn.value,
      login_wh_on: loginWhOn.value,
      mail_wh_on: mailWhOn.value,
      tv_wh_on: tvWhOn.value,
      assist_wh_on: assistWhOn.value,
    })
    whTouched = false
    wh.url = ''
    ElMessage.success(t('settings.wh.saved'))
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    savingWh.value = false
  }
}

async function testWh() {
  testingWh.value = true
  try {
    await api.post('/settings/webhook-test')
    ElMessage.success(t('settings.wh.testSent'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testingWh.value = false
  }
}

// 渠道总开关：关 = 不再推送且配置禁改（已有配置保留，重开即恢复）
async function saveWxOn() {
  try {
    await api.put('/settings', { wx_on: wxOn.value })
    ElMessage.success(wxOn.value ? t('settings.wx.enabled') : t('settings.wx.disabled'))
  } catch (e) {
    wxOn.value = !wxOn.value
    ElMessage.error(e.message)
  }
}

async function saveWhOn() {
  try {
    await api.put('/settings', { webhook_on: whOn.value })
    ElMessage.success(whOn.value ? t('settings.wh.enabled') : t('settings.wh.disabled'))
  } catch (e) {
    whOn.value = !whOn.value
    ElMessage.error(e.message)
  }
}

async function saveWpOn() {
  try {
    await api.put('/settings', { webpush_on: wpOn.value })
    ElMessage.success(wpOn.value ? t('settings.wp.enabled') : t('settings.wp.disabled'))
    loadSettings()
  } catch (e) {
    wpOn.value = !wpOn.value
    ElMessage.error(e.message)
  }
}

// Web Push 细分开关：勾选即保存（无独立保存按钮），失败回滚勾选态
async function saveAlertWpOn() {
  try {
    await api.put('/settings', { alert_webpush_on: alertWpOn.value })
  } catch (e) {
    alertWpOn.value = !alertWpOn.value
    ElMessage.error(e.message)
  }
}

async function saveCertWpOn() {
  try {
    await api.put('/settings', { cert_webpush_on: certWpOn.value })
  } catch (e) {
    certWpOn.value = !certWpOn.value
    ElMessage.error(e.message)
  }
}

async function saveReminderWpOn() {
  try {
    await api.put('/settings', { reminder_webpush_on: reminderWpOn.value })
  } catch (e) {
    reminderWpOn.value = !reminderWpOn.value
    ElMessage.error(e.message)
  }
}

async function saveLoginWpOn() {
  try {
    await api.put('/settings', { login_webpush_on: loginWpOn.value })
  } catch (e) {
    loginWpOn.value = !loginWpOn.value
    ElMessage.error(e.message)
  }
}

async function saveMailWpOn() {
  try {
    await api.put('/settings', { mail_webpush_on: mailWpOn.value })
  } catch (e) {
    mailWpOn.value = !mailWpOn.value
    ElMessage.error(e.message)
  }
}

// 本浏览器当前订阅态（SW 注册后查 pushManager），顺带算本机订阅指纹
async function refreshWpSubscription() {
  try {
    const reg = await navigator.serviceWorker.ready
    const sub = await reg.pushManager.getSubscription()
    wp.subscribed = !!sub
  } catch {
    wp.subscribed = false
  }
  await computeSelfFp()
}

function urlBase64ToUint8Array(base64) {
  const pad = '='.repeat((4 - (base64.length % 4)) % 4)
  const raw = atob((base64 + pad).replace(/-/g, '+').replace(/_/g, '/'))
  return Uint8Array.from(raw, (ch) => ch.charCodeAt(0))
}

// 确保 Service Worker 就绪：注册失败（/sw.js 404、CDN 拦截等）会显式抛错，
// 不能靠 boot 时的静默注册 + serviceWorker.ready 挂起——那表现为「点了没反应」
async function ensureSWRegistration() {
  const existing = await navigator.serviceWorker.getRegistration('/')
  if (existing) return existing
  try {
    await navigator.serviceWorker.register('/sw.js')
  } catch (e) {
    throw new Error(t('settings.wp.swError', { msg: e.message || e }))
  }
  return navigator.serviceWorker.ready
}

async function subscribePush() {
  wp.busy = true
  try {
    // 通知权限申请必须紧跟点击手势（先于网络请求），部分浏览器会因失去用户激活而静默拒绝
    const perm = await Notification.requestPermission()
    if (perm !== 'granted') {
      throw new Error(perm === 'denied' ? t('settings.wp.permDenied') : t('settings.wp.permPrompt'))
    }
    const reg = await ensureSWRegistration()
    const res = await api.get('/settings')
    const key = res.webpush?.public_key
    if (!key) throw new Error(t('settings.wp.noKey'))
    const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToUint8Array(key) })
    const j = sub.toJSON()
    await api.post('/webpush/subscribe', { endpoint: j.endpoint, keys: j.keys })
    wp.subscribed = true
    await computeSelfFp()
    ElMessage.success(t('settings.wp.subscribedMsg'))
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    wp.busy = false
  }
}

async function unsubscribePush() {
  wp.busy = true
  try {
    const reg = await navigator.serviceWorker.ready
    const sub = await reg.pushManager.getSubscription()
    if (sub) {
      const j = sub.toJSON()
      await api.post('/webpush/unsubscribe', { endpoint: j.endpoint }).catch(() => {})
      await sub.unsubscribe()
    }
    wp.subscribed = false
    ElMessage.success(t('settings.wp.unsubscribedMsg'))
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    wp.busy = false
  }
}

async function testWp() {
  testingWp.value = true
  try {
    await api.post('/settings/webpush-test')
    ElMessage.success(t('settings.wp.testSent'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testingWp.value = false
  }
}

async function runInspect() {
  inspLoading.value = true
  try {
    insp.value = await api.get('/inspection')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    inspLoading.value = false
  }
}

async function pushInspect() {
  inspPushing.value = true
  try {
    await api.post('/inspection/push')
    ElMessage.success(t('settings.insp.pushed'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    inspPushing.value = false
  }
}

async function startTotp() {
  totpSettingUp.value = true
  try {
    totpSetup.value = await api.post('/auth/totp-setup')
    totpCode.value = ''
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    totpSettingUp.value = false
  }
}

function cancelTotp() {
  totpSetup.value = null
  totpCode.value = ''
}

async function confirmTotp() {
  if (!totpCode.value.trim()) {
    ElMessage.warning(t('settings.totp.needCode'))
    return
  }
  totpSaving.value = true
  try {
    await api.post('/auth/totp-enable', { code: totpCode.value.trim() })
    ElMessage.success(t('settings.totp.enabledMsg'))
    cancelTotp()
    loadTotp()
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    totpSaving.value = false
  }
}

async function disableTotp() {
  if (!totpCode.value.trim()) {
    ElMessage.warning(t('settings.totp.needCodeOff'))
    return
  }
  totpSaving.value = true
  try {
    await api.post('/auth/totp-disable', { code: totpCode.value.trim() })
    ElMessage.success(t('settings.totp.disabledMsg'))
    totpCode.value = ''
    loadTotp()
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    totpSaving.value = false
  }
}

// 个人信息保存（原 ProfileDialog 逻辑内联）：任意修改都要验旧密码
async function saveProfile() {
  const username = prof.username.trim()
  if (!username) {
    ElMessage.warning(t('settings.prof.emptyName'))
    return
  }
  if (!prof.old) {
    ElMessage.warning(t('settings.prof.needOld'))
    return
  }
  if (prof.next) {
    if (prof.next.length < 6) {
      ElMessage.warning(t('settings.prof.shortNext'))
      return
    }
    if (prof.next !== prof.confirm) {
      ElMessage.warning(t('settings.prof.mismatch'))
      return
    }
  }
  profSaving.value = true
  try {
    await api.put('/auth/profile', {
      username,
      old_password: prof.old,
      new_password: prof.next || '',
    })
    setUsername(username)
    prof.old = prof.next = prof.confirm = ''
    ElMessage.success(t('common.saved'))
    window.dispatchEvent(new CustomEvent('profile-updated', { detail: { username } }))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    profSaving.value = false
  }
}

// 头像：本地 canvas 压缩到 ≤256px 后传 data URL（PNG 保透明，超 280KB 回落 JPEG）
function pickAvatar() {
  avatarInput.value?.click()
}


async function onAvatarFile(ev) {
  const file = ev.target.files?.[0]
  ev.target.value = ''
  if (!file) return
  if (!file.type.startsWith('image/')) {
    ElMessage.warning(t('settings.avatar.pickImage'))
    return
  }
  try {
    const dataURL = await compressAvatar(file)
    await api.put('/auth/avatar', { avatar: dataURL })
    avatar.value = dataURL
    setAvatar(dataURL)
    bumpAvatarVer() // 变 ?v= 让 /auth/avatar 短缓存失效（6）
    window.dispatchEvent(new Event('avatar-changed'))
    ElMessage.success(t('settings.avatar.updated'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function clearAvatar() {
  try {
    await api.put('/auth/avatar', { avatar: '' })
    avatar.value = ''
    setAvatar('')
    bumpAvatarVer() // 变 ?v= 让 /auth/avatar 短缓存失效（6）
    window.dispatchEvent(new Event('avatar-changed'))
    ElMessage.success(t('settings.avatar.removed'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 服务端重启 / 更新重启：脚本 2s 后执行，响应先送达；完成后页面需手动刷新
async function restart(mode) {
  const upgrade = mode === 'upgrade'
  try {
    await ElMessageBox.confirm(
      `${t(upgrade ? 'settings.rt.upgradeTip' : 'settings.rt.restartTip')}${t('settings.rt.confirmSuffix')}`,
      t(upgrade ? 'settings.rt.upgradeTitle' : 'settings.rt.restartTitle'),
      { type: 'warning', confirmButtonText: t(upgrade ? 'settings.rt.upgradeBtn' : 'settings.rt.restartBtn'), cancelButtonText: t('common.cancel') },
    )
  } catch {
    return
  }
  restarting.value = true
  try {
    const res = await api.post('/system/restart', { mode })
    ElMessage.success(res.message || t('settings.rt.triggered'))
  } catch (e) {
    ElMessage.error(e.message)
    restarting.value = false
  }
}

// 在线更新（395）：状态 / 检查 / 应用
const upd = reactive({ configured: true, current: '', latest: '', channel: '', notes: '', published_at: '', available: false, cached: false, checked_at: 0, error: '', agents_latest: '', agents_upgradable: 0, agents_unknown: 0 })
const checking = ref(false)
const applying = ref(false)
const agentAutoHint = computed(() => (upd.available && upd.cached ? t('settings.upd.agentAutoHint') : ''))
const notesOpen = ref(false) // 更新日志默认收起（429），点切换钮展开

async function loadUpd() {
  try {
    Object.assign(upd, await api.get('/update/status'))
  } catch { /* 未登录/旧版后端静默 */ }
}

async function checkUpdate() {
  checking.value = true
  try {
    Object.assign(upd, await api.post('/update/check'))
    if (upd.available) ElMessage.success(t('settings.upd.foundNew', { v: upd.latest }))
    else if (!upd.error) ElMessage.info(t('settings.upd.upToDate'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    checking.value = false
  }
}

async function applyUpdate() {
  try {
    await ElMessageBox.confirm(
      t('settings.upd.applyConfirm', { v: upd.latest }),
      t('settings.upd.applyTitle'),
      { type: 'warning', confirmButtonText: t('settings.upd.applyBtn'), cancelButtonText: t('common.cancel') },
    )
  } catch { return }
  applying.value = true
  try {
    const res = await api.post('/update/apply')
    // ElMessage 第二参是 appContext 不是时长——传数字会以 vnode.appContext=5 渲染炸出「Object prototype…」（用户报障）；长驻用对象形式
    ElMessage.success({ message: res.message || t('settings.upd.applying'), duration: 7000 })
    setTimeout(() => window.location.reload(), 8000)
  } catch (e) {
    ElMessage.error(e.message)
    applying.value = false
  }
}

// 自动刷新开关即时生效（默认关，需要时手动勾选）
watch(
  autoRefresh,
  (on) => {
    clearInterval(timer)
    if (on) timer = setInterval(() => load(false), 30 * 1000)
  },
  { immediate: true },
)
onUnmounted(() => clearInterval(timer))

// 手机端 descriptions 单列，避免标签与值挤在一行
const isMobile = useIsMobile()
const col = computed(() => (isMobile.value ? 1 : 2))

function fmtInt(n) {
  return n == null || isNaN(n) ? '-' : n.toLocaleString()
}

onMounted(() => {
  prof.username = localStorage.getItem('username') || ''
  load()
  loadTotp()
  loadUpd()
  loadTvServers()
})
onActivated(() => {
  prof.username = localStorage.getItem('username') || ''
  load()
  loadTotp()
  loadUpd()
}) // tab 模式下切回本页刷新
</script>

<style scoped>
/* 系统设置页与其他页一致铺满（仅 el-main 的 6px 内边距，不限宽） */
.status {
  max-width: none;
}
/* 标签样式增强：默认 tab 字小色淡不易分辨，放大加粗、激活走品牌色 */
.status-tabs :deep(.el-tabs__item) {
  font-size: 15px;
  font-weight: 600;
  color: var(--text-2);
}
.status-tabs :deep(.el-tabs__item:hover) {
  color: var(--brand);
}
.status-tabs :deep(.el-tabs__item.is-active) {
  color: var(--brand);
}
.status-tabs :deep(.el-tabs__active-bar) {
  height: 3px;
  border-radius: 2px;
}
.status-tabs :deep(.el-tabs__nav-wrap::after) {
  height: 1px;
}
/* 界面语言卡：选择器并入标题行，卡片体无内容，隐藏默认 padding 留白 */
.lang-card :deep(.el-card__body) {
  display: none;
}
.lang-inline {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  margin-left: 12px;
}
.lang-inline .hint {
  margin: 0;
}
@media (max-width: 768px) {
  .lang-inline {
    margin-left: 8px;
  }
  .lang-inline .hint {
    display: none; /* 手机端标题行放不下「偏好保存在本浏览器」提示 */
  }
}
.status-tabs :deep(.el-tabs__nav) {
  padding: 0 10px; /* 首个标签与页面左缘留出间距 */
  box-sizing: border-box;
}
.status-tabs :deep(.el-tabs__content) {
  overflow: visible;
}
.status-tabs .el-card + .el-card,
.status-tabs :deep(.el-tab-pane .el-card + .el-card) {
  margin-top: 14px;
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.tools {
  display: flex;
  align-items: center;
  gap: 12px;
}
.updated {
  color: var(--muted);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.card-head {
  font-weight: 600;
}
.head-upd {
  float: right;
  color: var(--muted);
  font-size: 12px;
  font-weight: 400;
  font-variant-numeric: tabular-nums;
}
.card-sub {
  margin-left: 8px;
  color: var(--muted);
  font-size: 12px;
  font-weight: 400;
}
.master-switch {
  float: right;
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 400;
}
.master-label {
  color: var(--muted);
  font-size: 12px;
}
.hint {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.5;
  margin-top: 4px;
}
/* 在线更新卡：更新说明保留换行（tag 注释原文多行） */
.upd-notes {
  white-space: pre-wrap;
  margin-top: 8px;
}
/* 更新日志默认收起（429）：查看/收起切换钮 */
.upd-notes-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: 4px;
  padding: 2px 0;
  border: none;
  background: transparent;
  color: var(--el-color-primary, #409eff);
  font-size: 12px;
  cursor: pointer;
}
.upd-notes-toggle:hover { text-decoration: underline; text-underline-offset: 3px; }
.upd-notes-toggle .el-icon { transition: transform 0.15s; }
.upd-notes-toggle .el-icon.open { transform: rotate(180deg); }
/* 数据维护操作区：descriptions 下方留白分段 */
.ops {
  border-top: 1px solid var(--border);
  margin-top: 14px;
  padding-top: 14px;
}
/* 行内相邻按钮清零 EP 自带 margin-left（叠在 flex gap 上双倍间距；
   margin-top 版会让行内第二个按钮在居中对齐时整体下坠错位） */
.op-row .el-button + .el-button {
  margin-left: 0;
}
.op-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.assist-row {
  flex-wrap: wrap;
}
.assist-preview {
  margin: 12px 0 0;
  padding: 10px 12px;
  background: var(--el-fill-color-light);
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-word;
}
.op-row .hint {
  margin: 0;
  flex: 1;
}
.unit {
  color: var(--text-2);
}
.firing {
  color: var(--crit);
  font-weight: 600;
}
.ok {
  color: var(--el-color-success);
}
.muted {
  color: var(--muted);
}
/* 个人信息：头像列 + 表单横向排布（手机端上下堆叠） */
.profile-wrap {
  display: flex;
  gap: 32px;
  align-items: flex-start;
}
.avatar-col {
  flex: none;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.avatar-box {
  position: relative;
  width: 96px;
  height: 96px;
  border-radius: 50%;
  border: 1px solid var(--border);
  background: var(--bg);
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  cursor: pointer;
}
.avatar-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.avatar-placeholder {
  font-size: 44px;
  color: var(--muted);
}
.avatar-mask {
  position: absolute;
  inset: auto 0 0 0;
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
  font-size: 12px;
  text-align: center;
  padding: 3px 0;
  opacity: 0;
  transition: opacity 0.15s;
}
.avatar-box:hover .avatar-mask {
  opacity: 1;
}
.avatar-remove {
  border: none;
  background: none;
  color: var(--muted);
  font-size: 12px;
  cursor: pointer;
  padding: 0;
}
.avatar-remove:hover {
  color: var(--crit);
}
.avatar-input {
  display: none;
}
.prof-form {
  flex: 1;
  min-width: 0;
  max-width: 460px;
}
/* 巡检报告小节标题与明细行 */
.insp-sec {
  font-weight: 600;
  margin: 12px 0 4px;
}
.insp-line {
  color: var(--text-2);
  font-size: 13px;
  line-height: 1.7;
  padding-left: 8px;
}
/* Web Push 订阅列表行：文字与删除按钮两端分布（230/231 批新增删除功能） */
.sub-line {
  display: flex;
  align-items: center;
  gap: 8px;
}
.sub-line .sub-txt { flex: 1; min-width: 0; word-break: break-all; }
.sub-line .sub-self { color: var(--brand); }
.sub-line .sub-del { flex: none; padding: 2px 6px; }
/* TOTP 开启引导：二维码 + 密钥与确认输入 */
.totp-enroll {
  display: flex;
  gap: 20px;
  align-items: flex-start;
}
.totp-qr {
  width: 160px;
  height: 160px;
  border-radius: 8px;
  border: 1px solid var(--border);
  flex-shrink: 0;
}
.totp-secret {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 14px;
  letter-spacing: 1px;
  word-break: break-all;
  margin-top: 6px;
}
/* WxPusher 配置：双列表单（手机端单列） */
.mail-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 24px;
}
.mail-grid :deep(.el-form-item) {
  margin-bottom: 12px;
}
.mail-switches {
  display: flex;
  flex-direction: column;
  align-items: flex-start; /* 子项收窄到内容宽度：勾选命中区只有框+文字，不再整行误触 */
  gap: 4px;
  margin: 8px 0 12px;
  padding-left: 6px;
}
@media (max-width: 768px) {
  .totp-enroll,
  .profile-wrap {
    flex-direction: column;
  }
  .prof-form {
    max-width: none;
    width: 100%;
  }
  .mail-grid {
    grid-template-columns: 1fr;
  }
}

/* 登录日志行列表（与监控/服务器管理列表同视觉：无重底色、圆角、悬停浅白透） */
.log-list {
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
}
.log-head {
  display: grid;
  grid-template-columns: 1fr 1.6fr 1.2fr;
  gap: 10px;
  padding: 9px 14px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-2);
  background: var(--el-fill-color-light);
}
.log-row {
  display: grid;
  grid-template-columns: 1fr 1.6fr 1.2fr;
  gap: 10px;
  padding: 9px 14px;
  font-size: 13.5px;
  border-top: 1px solid var(--border);
  transition: background 0.15s;
}
.log-row:hover {
  background: var(--el-fill-color-light);
}
.log-user {
  font-weight: 600;
}
.log-ip {
  font-variant-numeric: tabular-nums;
}
.user-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
</style>
