<template>
  <div class="status">
    <div class="toolbar">
      <span v-if="loadedAt" class="updated">更新于 {{ loadedAt }}</span>
      <span v-else></span>
      <div class="tools">
        <el-checkbox v-model="autoRefresh">30s 自动刷新</el-checkbox>
        <el-button :loading="loading" @click="load()">
          <el-icon v-if="!loading"><Refresh /></el-icon>
          <span v-if="!loading">刷新</span>
        </el-button>
      </div>
    </div>

    <el-tabs v-model="tab" class="status-tabs">
      <!-- ① 系统信息：运行信息 / 监控概况 / 登录日志 -->
      <el-tab-pane label="系统信息" name="system">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              运行信息<span class="head-upd">服务端更新时间：{{ rt.updated_at ? fmtTime(rt.updated_at) : '-' }}</span>
            </div>
          </template>
          <el-descriptions :column="col" border size="small">
            <el-descriptions-item label="系统名称">PicoPanel</el-descriptions-item>
            <el-descriptions-item label="版本">{{ rt.version || '-' }}</el-descriptions-item>
            <el-descriptions-item label="已运行">{{ fmtUptime(rt.uptime) }}</el-descriptions-item>
            <el-descriptions-item label="Go 版本">{{ rt.go_version || '-' }}</el-descriptions-item>
            <el-descriptions-item label="平台">{{ rt.os || '-' }}</el-descriptions-item>
            <el-descriptions-item label="goroutine 数">{{ rt.goroutines ?? '-' }}</el-descriptions-item>
            <el-descriptions-item label="内存（活跃 / 从 OS）">
              {{ fmtBytes(rt.mem_alloc) }} / {{ fmtBytes(rt.mem_sys) }}
            </el-descriptions-item>
            <el-descriptions-item label="服务端时间">{{ fmtTime(rt.now) }}</el-descriptions-item>
          </el-descriptions>
          <div class="ops">
            <div class="op-row">
              <el-button type="warning" plain :loading="restarting" @click="restart('upgrade')">更新重启</el-button>
              <el-button type="danger" plain :loading="restarting" @click="restart('restart')">重启</el-button>
              <span class="hint">更新重启：把服务端目录里的 lz-server.gz 解压替换为新版并重启（旧版保留为 lz-server.old）</span>
            </div>
            <div class="hint">
              重启期间页面（含终端 / 文件传输）会中断数秒，完成后需手动刷新页面。仅支持脚本部署方式（可执行文件同目录有 lz-server.sh）。
            </div>
          </div>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">在线更新<span class="card-sub">GitHub 更新通道 · 数据库自动备份 · 失败自动保留旧版</span></div>
          </template>
          <el-descriptions :column="col" border size="small">
            <el-descriptions-item label="当前版本">{{ upd.current || rt.version || '-' }}</el-descriptions-item>
            <el-descriptions-item label="通道最新版本">
              <span :class="{ firing: upd.available }">{{ upd.latest || '未检查' }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="Agent 最新版本">
              {{ upd.agents_latest || '未缓存' }}
              <span v-if="upd.agents_upgradable > 0" :class="{ firing: true }">（{{ upd.agents_upgradable }} 台待更新）</span>
            </el-descriptions-item>
            <el-descriptions-item label="更新通道">
              {{ (upd.channel || 'stable') + (upd.cached ? '（agent 分发包已缓存）' : '') }}
            </el-descriptions-item>
            <el-descriptions-item label="检查时间">{{ upd.checked_at ? fmtTime(upd.checked_at) : '尚未检查' }}</el-descriptions-item>
          </el-descriptions>
          <div v-if="upd.agents_upgradable > 0" class="hint" style="color: var(--el-color-warning);">
            有 {{ upd.agents_upgradable }} 台 agent 版本落后于渠道：到「服务器管理」页可单台或「升级全部 Agent」；update.agents 开启时检出新版也会自动分发。
          </div>
          <div v-if="upd.agents_unknown > 0" class="hint">
            另有 {{ upd.agents_unknown }} 台 agent 未上报版本（v1.0.1 之前的旧版），自动分发不会触碰——手动升级到 v1.0.2+ 后即纳入。
          </div>
          <!-- 更新日志默认收起（429），点「查看更新日志」展开 -->
          <div v-if="upd.notes" class="upd-notes-wrap">
            <button type="button" class="upd-notes-toggle" @click="notesOpen = !notesOpen">
              <el-icon :class="{ open: notesOpen }"><ArrowDown /></el-icon>{{ notesOpen ? '收起更新日志' : '查看更新日志' }}
            </button>
            <div v-show="notesOpen" class="hint upd-notes">{{ upd.notes }}</div>
          </div>
          <div v-if="upd.error" class="hint" style="color: var(--el-color-danger);">{{ upd.error }}</div>
          <div v-if="upd.configured === false" class="hint">
            未配置更新源：在 server.yaml 增加 update.sources（如「github:用户名/picopanel-release」或静态目录 URL）后重启生效；配置 check_interval 可自动检查。
          </div>
          <div class="ops">
            <div class="op-row">
              <el-button type="primary" plain :loading="checking" @click="checkUpdate">检查更新</el-button>
              <el-button type="warning" :disabled="!upd.available" :loading="applying" @click="applyUpdate">
                应用更新并自动重启{{ agentAutoHint }}
              </el-button>
            </div>
            <div class="hint">
              应用更新会先自动备份数据库、试运行新版本（-check-config），任何一步失败都保留当前版本继续运行；成功时同进程自动重启（页面断连数秒后刷新即新版本）。检出新版会按配置自动升级各台 agent。
            </div>
          </div>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">监控概况</div>
          </template>
          <el-descriptions :column="col" border size="small">
            <el-descriptions-item label="服务器">
              {{ ov.servers }} 台（在线 {{ ov.online }} / 离线 {{ ov.offline }} / 停用 {{ ov.disabled }}）
            </el-descriptions-item>
            <el-descriptions-item label="采集模式">{{ modeText }}</el-descriptions-item>
            <el-descriptions-item label="告警规则">{{ ov.rules ?? '-' }} 条</el-descriptions-item>
            <el-descriptions-item label="告警中">
              <span :class="{ firing: ov.firing > 0 }">{{ ov.firing ?? 0 }}</span>
            </el-descriptions-item>
            <el-descriptions-item label="告警历史">{{ fmtInt(ov.alert_total) }} 条</el-descriptions-item>
          </el-descriptions>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">登录日志<span class="card-sub">最近 {{ logins.length }} 条，成功登录</span></div>
          </template>
          <!-- 与服务器管理/监控列表同款行列表视觉（无重底色、圆角行、悬停浅白透） -->
          <div v-if="logins.length" class="log-list">
            <div class="log-head">
              <span>用户名</span><span>登录时间</span><span>来源 IP</span>
            </div>
            <div v-for="(l, i) in logins" :key="i" class="log-row">
              <span class="log-user">{{ l.username }}</span>
              <span>{{ fmtTime(l.ts) }}</span>
              <span class="log-ip">{{ l.ip }}</span>
            </div>
          </div>
          <div v-else class="hint">暂无登录记录</div>
        </el-card>
      </el-tab-pane>

      <!-- ② 个人信息：头像 / 账号 / 两步验证 -->
      <el-tab-pane label="个人信息" name="profile">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">账号<span class="card-sub">用户名与密码修改，任意一项都要验证旧密码</span></div>
          </template>
          <div class="profile-wrap">
            <div class="avatar-col">
              <div class="avatar-box" :title="avatar ? '点击更换头像' : '点击上传头像'" @click="pickAvatar">
                <img v-if="avatar" :src="avatar" class="avatar-img" alt="头像" />
                <el-icon v-else class="avatar-placeholder"><UserFilled /></el-icon>
                <div class="avatar-mask">更换</div>
              </div>
              <button v-if="avatar" type="button" class="avatar-remove" @click.stop="clearAvatar">移除头像</button>
              <input ref="avatarInput" type="file" accept="image/*" class="avatar-input" @change="onAvatarFile" />
            </div>
            <el-form :model="prof" label-width="90px" class="prof-form" @submit.prevent>
              <el-form-item label="用户名">
                <el-input v-model="prof.username" autocomplete="username" placeholder="登录用户名" />
              </el-form-item>
              <el-form-item label="旧密码">
                <el-input v-model="prof.old" type="password" show-password autocomplete="current-password" placeholder="验证身份必填" />
              </el-form-item>
              <el-form-item label="新密码">
                <el-input v-model="prof.next" type="password" show-password autocomplete="new-password" placeholder="留空表示不修改" />
              </el-form-item>
              <el-form-item v-if="prof.next" label="确认新密码">
                <el-input v-model="prof.confirm" type="password" show-password autocomplete="new-password" />
              </el-form-item>
              <el-form-item>
                <el-button type="primary" :loading="profSaving" @click="saveProfile">保存</el-button>
                <span class="hint" style="margin-left: 10px">改用户名不掉当前会话；改密码保留当前会话、踢出其他端</span>
              </el-form-item>
            </el-form>
          </div>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">登录两步验证（TOTP）<span class="card-sub">开启后本账号登录需输入 Authenticator 6 位验证码（各账号独立）</span></div>
          </template>
          <template v-if="!totpEnabled">
            <div v-if="!totpSetup" class="op-row">
              <el-button type="primary" :loading="totpSettingUp" @click="startTotp">开启两步验证</el-button>
              <span class="hint">用 Google Authenticator / 微信小程序「腾讯身份验证器」等扫码或手输密钥添加</span>
            </div>
            <template v-else>
              <div class="totp-enroll">
                <img v-if="totpSetup.qr" :src="totpSetup.qr" class="totp-qr" alt="TOTP 二维码" />
                <div class="totp-info">
                  <div class="hint">
                    扫码添加，或手动输入密钥（类型：基于时间 TOTP，SHA-1，6 位，30 秒）：
                  </div>
                  <el-text class="totp-secret">{{ totpSetup.secret }}</el-text>
                  <div class="op-row" style="margin-top: 10px">
                    <el-input
                      v-model="totpCode"
                      placeholder="输入 App 显示的 6 位验证码"
                      style="width: 220px"
                      maxlength="6"
                      inputmode="numeric"
                    />
                    <el-button type="primary" :loading="totpSaving" @click="confirmTotp">确认开启</el-button>
                    <el-button @click="cancelTotp">取消</el-button>
                  </div>
                </div>
              </div>
            </template>
          </template>
          <template v-else>
            <div class="op-row">
              <el-tag type="success" size="small">已开启</el-tag>
              <el-input
                v-model="totpCode"
                placeholder="输入当前 6 位验证码以确认关闭"
                style="width: 240px"
                maxlength="6"
                inputmode="numeric"
              />
              <el-button type="danger" plain :loading="totpSaving" @click="disableTotp">关闭两步验证</el-button>
            </div>
            <div class="hint" style="margin-top: 6px">关闭需验证码确认；换手机/丢 Authenticator 时可先关闭再重新开启。</div>
          </template>
        </el-card>
      </el-tab-pane>

      <!-- ③ 数据库 -->
      <el-tab-pane label="数据库" name="db">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">数据库</div>
          </template>
          <el-descriptions :column="col" border size="small">
            <el-descriptions-item label="主文件大小">{{ fmtBytes(db.size) }}</el-descriptions-item>
            <el-descriptions-item label="WAL 大小">{{ fmtBytes(db.wal_size) }}</el-descriptions-item>
            <el-descriptions-item label="明细行数（约，近 7 天）">{{ fmtInt(db.rows_approx) }}</el-descriptions-item>
            <el-descriptions-item label="归档桶数（5 分钟）">{{ fmtInt(db.archive_rows) }}</el-descriptions-item>
            <el-descriptions-item label="数据保留">{{ db.retention ?? '-' }} 天</el-descriptions-item>
          </el-descriptions>
          <div class="hint">
            两段式存储：近 7 天保留 10s 明细，更早降采样为 5 分钟均值桶；行数按 rowid 近似（清理后偏高，仅量级参考）；
            WAL 在写入 checkpoint 后回收，长期偏大属正常。
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
              <span class="unit">天</span>
              <el-button type="primary" :loading="saving" @click="saveRetention">保存</el-button>
            </div>
            <div class="hint">
              数据保留天数，默认 90 天；修改保存后立即生效（后台每小时自动清理一次过期数据，无需重启）。
              备份生成在服务端数据库同目录 backup/ 下（gzip 压缩完整库，解压即可用于恢复/重新部署），请自行取回保存。
            </div>
            <div class="op-row" style="margin-top: 10px">
              <el-button type="danger" plain :loading="cleaning" @click="cleanup">
                立即清理过期数据
              </el-button>
              <el-button type="success" plain :loading="backing" @click="createBackup">
                创建备份
              </el-button>
            </div>
          </div>
        </el-card>
      </el-tab-pane>

      <!-- ④ 消息推送：Web Push / WxPusher / Webhook / 一键巡检 -->
      <el-tab-pane label="消息推送" name="push">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              Web Push<span class="card-sub">浏览器系统通知（手机/桌面 PWA），无需第三方服务</span>
              <span class="master-switch">
                <span class="master-label">{{ wpOn ? '已开启' : '已关闭' }}</span>
                <el-switch v-model="wpOn" @change="saveWpOn" />
              </span>
            </div>
          </template>
          <el-collapse-transition>
            <div v-show="wpOn">
              <div v-if="!wpSupported" class="hint">
                当前环境不支持 Web Push（缺少 Service Worker 或 Push API）：需要 HTTPS（或 localhost）访问；
                iPhone / iPad 上的 Edge、Chrome 等第三方浏览器系统不开放推送能力，请改用 Safari，或将本站添加到主屏幕后从主屏图标打开。
              </div>
              <template v-else>
            <div class="op-row">
              <el-button v-if="!wp.subscribed" type="primary" :loading="wp.busy" :disabled="!wpOn" @click="subscribePush">
                订阅此浏览器
              </el-button>
              <el-button v-else :loading="wp.busy" :disabled="!wpOn" @click="unsubscribePush">退订此浏览器</el-button>
              <el-button :loading="testingWp" :disabled="!wpOn" @click="testWp">发测试通知</el-button>
              <span class="hint">
                {{ wp.subscribed ? '本浏览器已订阅，告警 / 证书提醒会弹系统通知' : '点击订阅后允许浏览器的通知权限' }}
              </span>
            </div>
            <div class="mail-switches">
              <el-checkbox v-model="alertWpOn" :disabled="!wpOn" @change="saveAlertWpOn">告警发生 / 恢复时 Web Push 通知</el-checkbox>
              <el-checkbox v-model="certWpOn" :disabled="!wpOn" @change="saveCertWpOn">HTTPS 证书到期前 15 天 Web Push 提醒</el-checkbox>
              <el-checkbox v-model="reminderWpOn" :disabled="!wpOn" @change="saveReminderWpOn">提醒事项到期 Web Push 推送</el-checkbox>
              <el-checkbox v-model="loginWpOn" :disabled="!wpOn" @change="saveLoginWpOn">登录成功 Web Push 提醒（账号/时间/IP）</el-checkbox>
              <el-checkbox v-model="mailWpOn" :disabled="!wpOn" @change="saveMailWpOn">有新邮件到达时 Web Push 通知</el-checkbox>
              <el-checkbox v-model="tvWpOn" :disabled="!wpOn" @change="saveTvWpOn">追剧更新 Web Push 通知</el-checkbox>
            </div>
            <div v-if="wp.subs.length" class="insp-sec">已订阅的浏览器（{{ wp.subs.length }}）</div>
            <div v-for="s in wp.subs" :key="s.id" class="insp-line sub-line">
              <span class="sub-txt">{{ s.ua || '未知浏览器' }} · 订阅于 {{ fmtTime(s.created_at) }}<span v-if="s.fp === selfFp" class="sub-self">（本浏览器）</span></span>
              <el-button size="small" text type="danger" class="sub-del" @click="deleteSub(s)">删除</el-button>
            </div>
            <div class="hint" style="margin-top: 8px">
              推送经浏览器推送服务（Google/Microsoft/Mozilla/Apple 等）投递，服务端只存订阅端点与密钥；换 VAPID 密钥后各浏览器需重新订阅。删除订阅后对应浏览器可重新订阅。
            </div>
          </template>
            </div>
          </el-collapse-transition>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              WxPusher<span class="card-sub">告警与证书到期提醒共用一个应用配置</span>
              <span class="master-switch">
                <span class="master-label">{{ wxOn ? '已开启' : '已关闭' }}</span>
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
                  :placeholder="wx.hasToken ? '已保存（不回显），留空不修改' : 'WxPusher 应用 appToken（AT_ 开头）'"
                />
              </el-form-item>
              <el-form-item label="UID">
                <el-input v-model="wx.uids" placeholder="接收者 UID，多个用逗号分隔（UID_ 开头）" />
              </el-form-item>
              <el-form-item label="主题 ID">
                <el-input v-model="wx.topics" placeholder="可选，主题 topicId，多个用逗号分隔" />
              </el-form-item>
            </div>
            <div class="hint">
              到 wxpusher.zjiecode.com 创建应用拿 appToken，扫码关注拿 UID（或建主题用 topicId）；UID 与主题至少填一种。
            </div>
            <div class="mail-switches">
              <el-checkbox v-model="alertWxOn">告警发生 / 恢复时 WxPusher 通知</el-checkbox>
              <el-checkbox v-model="certWxOn">HTTPS 证书到期前 15 天 WxPusher 提醒</el-checkbox>
              <el-checkbox v-model="reminderWxOn">提醒事项到期 WxPusher 推送</el-checkbox>
              <el-checkbox v-model="loginWxOn">登录成功 WxPusher 提醒（账号/时间/IP）</el-checkbox>
              <el-checkbox v-model="mailWxOn">有新邮件到达时 WxPusher 推送</el-checkbox>
              <el-checkbox v-model="tvWxOn">追剧更新 WxPusher 推送</el-checkbox>
            </div>
            <div class="op-row">
              <el-button type="primary" :loading="savingMail" @click="saveMail">保存</el-button>
              <el-button :loading="testing" @click="testMail">发测试消息</el-button>
              <span class="hint">保存后当前配置即时生效；测试消息发给上述 UID / 主题（用已保存配置发送）</span>
            </div>
          </el-form>
            </div>
          </el-collapse-transition>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              通用 Webhook<span class="card-sub">自定义 JSON 模板，适配钉钉/飞书/Slack 等机器人，与 WxPusher 并存</span>
              <span class="master-switch">
                <span class="master-label">{{ whOn ? '已开启' : '已关闭' }}</span>
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
                :placeholder="wh.hasURL ? '已保存（不回显），留空不修改' : '完整回调地址（可含 access_token 等参数）'"
              />
            </el-form-item>
            <el-form-item label="JSON 模板">
              <el-input
                v-model="wh.template"
                type="textarea"
                :rows="4"
                :placeholder='`留空使用默认模板：{"title": {{title}}, "content": {{message}}}`'
              />
            </el-form-item>
            <div class="hint">
              占位符 <code v-pre>{{title}}</code>（标题）与 <code v-pre>{{message}}</code>（标题+正文）替换为带引号的
              JSON 字符串——模板里占位符直接放值位置、不要再加引号（如 <code v-pre>"content": {{message}}</code>）；
              按钉钉/飞书/Slack 机器人文档填 URL 与对应模板即可接入。
            </div>
            <div class="mail-switches">
              <el-checkbox v-model="alertWhOn">告警发生 / 恢复时 Webhook 通知</el-checkbox>
              <el-checkbox v-model="certWhOn">HTTPS 证书到期前 15 天 Webhook 提醒</el-checkbox>
              <el-checkbox v-model="reminderWhOn">提醒事项到期 Webhook 推送</el-checkbox>
              <el-checkbox v-model="loginWhOn">登录成功 Webhook 提醒（账号/时间/IP）</el-checkbox>
              <el-checkbox v-model="mailWhOn">有新邮件到达时 Webhook 推送</el-checkbox>
              <el-checkbox v-model="tvWhOn">追剧更新 Webhook 推送</el-checkbox>
            </div>
            <div class="op-row">
              <el-button type="primary" :loading="savingWh" @click="saveWh">保存</el-button>
              <el-button :loading="testingWh" @click="testWh">发测试消息</el-button>
              <span class="hint">保存后当前配置即时生效；测试消息 POST 到上述 URL（用已保存配置发送）</span>
            </div>
          </el-form>
            </div>
          </el-collapse-transition>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">一键巡检<span class="card-sub">汇总在线状态 / 资源占用 / 告警中 / 证书临期 / 未读邮件，可推送或每日定时推送</span></div>
          </template>
          <div class="op-row">
            <el-button type="primary" :loading="inspLoading" @click="runInspect">生成巡检报告</el-button>
            <el-button type="success" plain :loading="inspPushing" @click="pushInspect">立即推送</el-button>
            <span class="hint">推送走已开启的渠道（WxPusher / Webhook / Web Push）；手动推送不受下方定时开关影响</span>
          </div>
          <div class="op-row" style="margin-top: 10px">
            <el-checkbox v-model="inspOn">每日定时推送</el-checkbox>
            <el-input v-model="inspTime" style="width: 100px" placeholder="09:00" />
            <el-button :loading="savingInsp" @click="saveInsp">保存</el-button>
            <span class="hint">每日到达该时刻自动推送一份（本地时区 HH:MM，错过时刻在服务端下次检查时补推）</span>
          </div>

          <template v-if="insp">
            <el-descriptions :column="col" border size="small" style="margin-top: 14px">
              <el-descriptions-item label="巡检时间">{{ fmtTime(insp.generated_at) }}</el-descriptions-item>
              <el-descriptions-item label="服务器">
                {{ insp.total }} 台（在线 {{ insp.online }} / 离线 {{ insp.offline }} / 停用 {{ insp.disabled }}）
              </el-descriptions-item>
              <el-descriptions-item label="告警中">
                <span :class="{ firing: insp.firing_count > 0 }">{{ insp.firing_count }}</span> 条
              </el-descriptions-item>
              <el-descriptions-item label="证书临期（15 天内）">{{ insp.cert_due }} 张</el-descriptions-item>
              <el-descriptions-item label="未读邮件">
                <span :class="{ firing: insp.mail_unread > 0 }">{{ insp.mail_unread }}</span> 封
              </el-descriptions-item>
            </el-descriptions>

            <template v-if="insp.firing_alert?.length">
              <div class="insp-sec">告警中</div>
              <div v-for="(a, i) in insp.firing_alert" :key="i" class="insp-line">{{ a }}</div>
            </template>
            <template v-if="insp.disk_warn?.length">
              <div class="insp-sec">磁盘紧张（≥80%）</div>
              <div v-for="(d, i) in insp.disk_warn" :key="i" class="insp-line">{{ d }}</div>
            </template>
            <template v-if="insp.certs?.length">
              <div class="insp-sec">证书临期（15 天内）</div>
              <div v-for="(ct, i) in insp.certs" :key="i" class="insp-line">
                {{ ct.domain }}（{{ ct.days < 0 ? `已过期 ${-ct.days} 天` : `还有 ${ct.days} 天` }}）
              </div>
            </template>

            <template v-if="insp.mail_lines?.length">
              <div class="insp-sec">未读邮件</div>
              <div v-for="(m, i) in insp.mail_lines" :key="i" class="insp-line">{{ m }}</div>
            </template>

            <div class="insp-sec">服务器明细</div>
            <el-table :data="insp.servers" size="small" max-height="360">
              <el-table-column prop="name" label="名称" min-width="140" show-overflow-tooltip />
              <el-table-column label="状态" width="90">
                <template #default="{ row }">
                  <span v-if="!row.enabled" class="muted">停用</span>
                  <span v-else-if="row.online" class="ok">在线</span>
                  <span v-else class="firing">离线</span>
                </template>
              </el-table-column>
              <el-table-column label="CPU" width="90">
                <template #default="{ row }">{{ row.online ? fmtPercent(row.cpu) : '-' }}</template>
              </el-table-column>
              <el-table-column label="内存" width="90">
                <template #default="{ row }">{{ row.online ? fmtPercent(row.mem) : '-' }}</template>
              </el-table-column>
              <el-table-column label="磁盘最大" min-width="150">
                <template #default="{ row }">
                  <span v-if="row.online && row.disk_mount">{{ row.disk_mount }} {{ fmtPercent(row.disk) }}</span>
                  <span v-else>-</span>
                </template>
              </el-table-column>
              <el-table-column label="告警中" width="80">
                <template #default="{ row }">{{ row.firing || '-' }}</template>
              </el-table-column>
            </el-table>
          </template>
          <div v-else class="hint" style="margin-top: 10px">尚未生成报告；点击「生成巡检报告」即时汇总（不落库）。</div>
        </el-card>

        <el-card shadow="never">
          <template #header>
            <div class="card-head">追剧<span class="card-sub">TMDB 剧集更新追踪（独立「追剧」页），出口 agent 与 Key 为全局配置</span></div>
          </template>
          <el-form label-width="90px" size="small" class="mail-form">
            <div class="mail-grid">
              <el-form-item label="TMDB Key">
                <el-input
                  v-model="tvTmdbKey"
                  type="password"
                  show-password
                  :placeholder="tvTmdbHasKey ? '已保存（不回显），留空不修改' : 'TMDB API Key（v3 key，themoviedb.org 免费申请）'"
                />
              </el-form-item>
              <el-form-item label="出口 agent">
                <el-select v-model="tvEgress" style="width: 100%">
                  <el-option v-for="s in tvServers" :key="s.id" :label="s.name" :value="s.id" />
                </el-select>
              </el-form-item>
            </div>
            <div class="hint">
              TMDB 查询经所选 push agent 的上报通道代查（server 直连不了 TMDB 时选一台能出网的机器，如家里 NAS）；
              Key 与出口缺一不可，未配齐时追剧页搜索不可用、已追条目暂停检查。换出口会作废未执行任务并立即重查一轮。
            </div>
            <div class="op-row">
              <el-button type="primary" :loading="savingTv" @click="saveTvConfig">保存</el-button>
              <span class="hint">themoviedb.org → 设置 → API → Developer，申请 v3 key 即可</span>
            </div>
          </el-form>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="用户管理" name="users">
        <el-card shadow="never" class="user-card">
          <template #header>
            <div class="user-head">
              <span>登录账号（受限用户仅可访问「智能家居」页面）</span>
              <el-button type="primary" size="small" @click="openUserCreate">新建用户</el-button>
            </div>
          </template>
          <el-table :data="users" size="small" class="row-table">
            <el-table-column prop="username" label="用户名" min-width="140" />
            <el-table-column label="角色" width="110">
              <template #default="{ row }">
                <el-tag :type="row.role === 'admin' ? 'danger' : 'info'" size="small">
                  {{ row.role === 'admin' ? '管理员' : '受限用户' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="创建时间" width="170">
              <template #default="{ row }">{{ row.created_at ? fmtTime(row.created_at) : '-' }}</template>
            </el-table-column>
            <el-table-column label="操作" width="190">
              <template #default="{ row }">
                <el-button size="small" @click="openUserEdit(row)">编辑</el-button>
                <el-button size="small" type="danger" plain @click="removeUser(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>

        <el-dialog v-model="userDlg.open" :title="userDlg.id ? '编辑用户' : '新建用户'" width="420px" append-to-body>
          <el-form label-width="82px">
            <el-form-item label="用户名">
              <el-input v-model="userDlg.username" :disabled="!!userDlg.id" maxlength="100" />
            </el-form-item>
            <el-form-item label="密码" :required="!userDlg.id">
              <el-input v-model="userDlg.password" type="password" show-password
                        :placeholder="userDlg.id ? '留空 = 不修改密码' : '至少 6 位'" maxlength="100" />
            </el-form-item>
            <el-form-item label="角色">
              <el-radio-group v-model="userDlg.role">
                <el-radio value="admin">管理员</el-radio>
                <el-radio value="user">受限用户（仅 HA 页）</el-radio>
              </el-radio-group>
            </el-form-item>
          </el-form>
          <template #footer>
            <el-button @click="userDlg.open = false">取消</el-button>
            <el-button type="primary" :loading="userDlg.saving" @click="saveUser">保存</el-button>
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
import { useIsMobile } from '../composables/useIsMobile.js'
import { useRoute } from 'vue-router'
import { ArrowDown, UserFilled } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, bumpAvatarVer, getAvatar, setAvatar, setUsername } from '../api/index.js'
import { compressAvatar } from '../utils/avatar.js'
import { fmtBytes, fmtPercent, fmtUptime, fmtTime } from '../utils/format.js'

const MODE_LABEL = { agent: 'Agent 拉取', push: 'Agent 上报', ssh: 'SSH 直连', node: 'exporter' }

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
    ElMessage.warning('请输入用户名')
    return
  }
  if (userDlg.password && userDlg.password.length < 6) {
    ElMessage.warning('密码至少 6 位')
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
        ElMessage.warning('请输入密码')
        return
      }
      await api.post('/auth/users', { username, password: userDlg.password, role: userDlg.role })
    }
    userDlg.open = false
    ElMessage.success('已保存')
    loadUsers()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    userDlg.saving = false
  }
}

async function removeUser(row) {
  try {
    await ElMessageBox.confirm(`确定删除用户「${row.username}」？其全部登录会话将立即失效。`, '删除用户', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api.del(`/auth/users/${row.id}`)
    ElMessage.success('已删除')
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
    ElMessage.success('追剧设置已保存')
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
      `删除 ${s.ua || '未知浏览器'} 的订阅？删除后该浏览器可重新订阅。`,
      '删除订阅',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
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
      ElMessage.success('已删除本浏览器的订阅，可重新订阅')
    } else {
      ElMessage.success('已删除该订阅')
    }
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 一键巡检：报告现查现算不落库；定时推送配置存 settings
const insp = ref(null)
const inspLoading = ref(false)
const inspPushing = ref(false)
const inspOn = ref(false)
const inspTime = ref('09:00')
const savingInsp = ref(false)

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
    .map((m) => `${MODE_LABEL[m]} ${mc[m]}`)
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
    loadedAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
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
    tvTmdbHasKey.value = !!res.tv_tmdb_has_key
    tvEgress.value = res.tv_egress_server_id || 0
    wp.subs = res.webpush?.subs || []
    if (wpSupported && wpOn.value) refreshWpSubscription()
    inspOn.value = !!res.inspect_push_on
    if (res.inspect_push_time) inspTime.value = res.inspect_push_time
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
    ElMessage.warning('数据保留天数至少 1 天')
    return
  }
  saving.value = true
  try {
    await api.put('/settings', { retention_days: retention.value })
    retentionTouched = false
    ElMessage.success(`已保存：数据保留 ${retention.value} 天`)
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
      '立即清理过期数据？将删除保留期之外的指标与已恢复告警并回收磁盘空间，不可恢复。',
      '清理数据库',
      { type: 'warning', confirmButtonText: '清理' },
    )
  } catch {
    return
  }
  cleaning.value = true
  try {
    const res = await api.post('/settings/cleanup')
    ElMessage.success(
      `清理完成：删除过期指标 ${res.metrics_deleted} 行、已恢复告警 ${res.alerts_deleted} 条，` +
        `当前占用 ${fmtBytes(res.db_size)}`,
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
      '在服务端数据库同目录 backup/ 下创建最新备份（gzip 压缩完整库）？大库会持续数秒到数十秒。',
      '创建备份',
      { type: 'info', confirmButtonText: '创建' },
    )
  } catch {
    return
  } finally {
    backupConfirmOpen = false
  }
  backing.value = true
  try {
    const res = await api.post('/settings/db-backup')
    ElMessage.success(`备份完成：${res.file}（${fmtBytes(res.size)}）`, { duration: 6000 })
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
    ElMessage.warning('UID 与主题 ID 至少填一种')
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
    })
    wxTouched = false
    wx.token = ''
    ElMessage.success('WxPusher 设置已保存')
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
    ElMessage.success('测试消息已发送，请在微信里查收')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    testing.value = false
  }
}

async function saveWh() {
  if (!wh.url && !wh.hasURL) {
    ElMessage.warning('请填写 Webhook URL')
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
    })
    whTouched = false
    wh.url = ''
    ElMessage.success('Webhook 通知设置已保存')
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
    ElMessage.success('测试消息已发送，请到对应群里查收')
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
    ElMessage.success(wxOn.value ? 'WxPusher 渠道已开启' : 'WxPusher 渠道已关闭，将不再推送')
  } catch (e) {
    wxOn.value = !wxOn.value
    ElMessage.error(e.message)
  }
}

async function saveWhOn() {
  try {
    await api.put('/settings', { webhook_on: whOn.value })
    ElMessage.success(whOn.value ? 'Webhook 渠道已开启' : 'Webhook 渠道已关闭，将不再推送')
  } catch (e) {
    whOn.value = !whOn.value
    ElMessage.error(e.message)
  }
}

async function saveWpOn() {
  try {
    await api.put('/settings', { webpush_on: wpOn.value })
    ElMessage.success(wpOn.value ? 'Web Push 渠道已开启' : 'Web Push 渠道已关闭，将不再推送')
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
    throw new Error(`通知脚本 /sw.js 注册失败：${e.message || e}（请确认服务端为最新版且未经代理缓存）`)
  }
  return navigator.serviceWorker.ready
}

async function subscribePush() {
  wp.busy = true
  try {
    // 通知权限申请必须紧跟点击手势（先于网络请求），部分浏览器会因失去用户激活而静默拒绝
    const perm = await Notification.requestPermission()
    if (perm !== 'granted') {
      throw new Error(
        perm === 'denied'
          ? '通知权限已被拒绝：请在浏览器地址栏锁图标 → 网站权限中允许「通知」后重试'
          : '浏览器通知权限未授权，无法订阅',
      )
    }
    const reg = await ensureSWRegistration()
    const res = await api.get('/settings')
    const key = res.webpush?.public_key
    if (!key) throw new Error('VAPID 公钥未生成，请先开启 Web Push 总开关')
    const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToUint8Array(key) })
    const j = sub.toJSON()
    await api.post('/webpush/subscribe', { endpoint: j.endpoint, keys: j.keys })
    wp.subscribed = true
    await computeSelfFp()
    ElMessage.success('已订阅本浏览器，测试通知可验证效果')
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
    ElMessage.success('已退订本浏览器')
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
    ElMessage.success('测试通知已发送，请查看系统通知')
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
    ElMessage.success('巡检报告已推送，请查收')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    inspPushing.value = false
  }
}

async function saveInsp() {
  const t = inspTime.value.trim()
  if (!/^\d{1,2}:\d{2}$/.test(t)) {
    ElMessage.warning('推送时间格式应为 HH:MM，如 09:00')
    return
  }
  const [h, m] = t.split(':').map((n) => parseInt(n, 10))
  if (!(h >= 0 && h < 24 && m >= 0 && m < 60)) {
    ElMessage.warning('推送时间应在 00:00 ~ 23:59 之间')
    return
  }
  savingInsp.value = true
  try {
    await api.put('/settings', { inspect_push_on: inspOn.value, inspect_push_time: t })
    ElMessage.success(`已保存：每日定时推送${inspOn.value ? `（${t}）` : '开关已关闭'}`)
    loadSettings()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    savingInsp.value = false
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
    ElMessage.warning('请输入 App 显示的 6 位验证码')
    return
  }
  totpSaving.value = true
  try {
    await api.post('/auth/totp-enable', { code: totpCode.value.trim() })
    ElMessage.success('两步验证已开启，下次登录需输入验证码')
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
    ElMessage.warning('请输入当前 6 位验证码以确认关闭')
    return
  }
  totpSaving.value = true
  try {
    await api.post('/auth/totp-disable', { code: totpCode.value.trim() })
    ElMessage.success('两步验证已关闭')
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
    ElMessage.warning('用户名不能为空')
    return
  }
  if (!prof.old) {
    ElMessage.warning('请填写旧密码验证身份')
    return
  }
  if (prof.next) {
    if (prof.next.length < 6) {
      ElMessage.warning('新密码至少 6 位')
      return
    }
    if (prof.next !== prof.confirm) {
      ElMessage.warning('两次输入的新密码不一致')
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
    ElMessage.success('已保存')
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
    ElMessage.warning('请选择图片文件')
    return
  }
  try {
    const dataURL = await compressAvatar(file)
    await api.put('/auth/avatar', { avatar: dataURL })
    avatar.value = dataURL
    setAvatar(dataURL)
    bumpAvatarVer() // 变 ?v= 让 /auth/avatar 短缓存失效（6）
    window.dispatchEvent(new Event('avatar-changed'))
    ElMessage.success('头像已更新')
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
    ElMessage.success('已移除头像')
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 服务端重启 / 更新重启：脚本 2s 后执行，响应先送达；完成后页面需手动刷新
async function restart(mode) {
  const tip =
    mode === 'upgrade'
      ? '将把服务端目录里的 lz-server.gz 解压替换为新版并重启（旧版保留为 lz-server.old）。'
      : '将重启服务端进程。'
  try {
    await ElMessageBox.confirm(
      `${tip}重启期间页面（含终端 / 文件传输）会中断数秒，完成后需手动刷新页面。继续？`,
      mode === 'upgrade' ? '更新重启' : '重启服务端',
      { type: 'warning', confirmButtonText: mode === 'upgrade' ? '更新并重启' : '重启', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  restarting.value = true
  try {
    const res = await api.post('/system/restart', { mode })
    ElMessage.success(res.message || '已触发重启')
  } catch (e) {
    ElMessage.error(e.message)
    restarting.value = false
  }
}

// 在线更新（395）：状态 / 检查 / 应用
const upd = reactive({ configured: true, current: '', latest: '', channel: '', notes: '', published_at: '', available: false, cached: false, checked_at: 0, error: '', agents_latest: '', agents_upgradable: 0, agents_unknown: 0 })
const checking = ref(false)
const applying = ref(false)
const agentAutoHint = computed(() => (upd.available && upd.cached ? '（agent 同步升级）' : ''))
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
    if (upd.available) ElMessage.success(`发现新版本 ${upd.latest}`)
    else if (!upd.error) ElMessage.info('已是最新版本')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    checking.value = false
  }
}

async function applyUpdate() {
  try {
    await ElMessageBox.confirm(
      `将更新到 ${upd.latest}：自动备份数据库 → 校验试运行 → 原子替换 → 自动重启。任何一步失败都会保留当前版本；成功时页面断连数秒后刷新即新版本。继续？`,
      '应用在线更新',
      { type: 'warning', confirmButtonText: '更新并自动重启', cancelButtonText: '取消' },
    )
  } catch { return }
  applying.value = true
  try {
    const res = await api.post('/update/apply')
    // ElMessage 第二参是 appContext 不是时长——传数字会以 vnode.appContext=5 渲染炸出「Object prototype…」（用户报障）；长驻用对象形式
    ElMessage.success({ message: res.message || '正在应用更新', duration: 7000 })
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
