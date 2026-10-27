<template>
  <div class="ha" :class="frontCls">
    <div class="ha-bg"><i class="w1"/><i class="w2"/><i class="w3"/></div>
    <div class="ha-body">
      <!-- 顶栏：标题 + 操作区（退出登录；管理员另有管理设备/回控制台） -->
      <div class="ha-top">
        <!-- 品牌（13）：点击回主页——仅管理员（421 受限用户导航钮隐藏防困死会话，口径不变） -->
        <button :class="['ha-brand', { link: isAdmin }]" type="button" :title="isAdmin ? $t('ha.backHome') : ''" @click="goHome">
          <span class="ha-brand-ico" v-html="HA_BRAND_ICO" />
          <div>
            <div class="ha-title">{{ $t('ha.title') }}</div>
            <div class="ha-sub">Home Assistant</div>
          </div>
        </button>
        <!-- 天气胶囊（5；2026-10-23 改版=仅图标+温度，今日区间/风力/降水撤出胶囊），
             点击弹 7 日详情不变（弹层仿主页，pv-wx-pop 全局样式直接复用）；
             /page/weather 为管理员端点，受限用户拉取 403 → wx 恒 null 胶囊整体不显示 -->
        <el-popover v-if="wx" placement="bottom-start" :width="368" trigger="click" popper-class="pv-wx-pop">
          <template #reference>
            <button class="ha-wx" type="button" :title="$t('ha.wxTip')">
              <span class="ha-wx-ico" v-html="wx.icon" />
              <b class="ha-wx-temp">{{ wx.temp }}°</b>
            </button>
          </template>
          <div class="pv-wx-pop-body">
            <div v-for="(w, i) in wx.warnings" :key="'w' + i" class="pv-wx-warn">
              <b>{{ w.title || w.type }}</b>
              <i>{{ w.text }}</i>
            </div>
            <div class="pv-wx-hero">
              <span class="pv-wx-hero-ico" v-html="wx.icon" />
              <span class="pv-wx-hero-main">
                <b class="pv-wx-hero-temp">{{ wx.temp }}<i>°</i></b>
                <span class="pv-wx-hero-text">{{ wx.text }} · {{ wx.city }}</span>
              </span>
              <span class="pv-wx-hero-feels">{{ $t('ha.feels', { v: wx.feels }) }}</span>
            </div>
            <div class="pv-wx-days">
              <div class="pv-wx-days-head">{{ $t('ha.nextDays', { n: wx.daily.length }) }}</div>
              <div v-for="d in wx.daily" :key="d.date" class="pv-wx-day">
                <span class="pv-wx-day-name">{{ d.name }}</span>
                <span class="pv-wx-day-ico" v-html="d.icon" />
                <el-tooltip
                  placement="top"
                  :show-after="80"
                  :hide-after="0"
                  :content="wxDayTitle(d)"
                  popper-class="pv-wx-tip"
                  :disabled="!wxDayTitle(d)"
                >
                  <span class="pv-wx-day-precip">{{ wxDayPrecip(d) }}<i v-if="d.snow || d.wind" class="pv-wx-day-ex">{{ d.snow ? '❄' : '' }}{{ d.wind ? '🌬' : '' }}</i></span>
                </el-tooltip>
                <b class="pv-wx-day-min">{{ d.min }}°</b>
                <span class="pv-wx-day-bar"><i :style="{ left: d.barLeft + '%', width: d.barWidth + '%' }" /></span>
                <b class="pv-wx-day-max">{{ d.max }}°</b>
              </div>
            </div>
            <div v-if="wx.details.length" class="pv-wx-idx">
              <span v-for="c in wx.details" :key="c.label" class="pv-wx-idx-chip">
                <i>{{ c.label }}</i>{{ c.value }}
              </span>
            </div>
            <div class="pv-wx-foot">
              <div v-if="wxUpd" class="pv-wx-upd">{{ wxSrcName ? wxSrcName + ' · ' : '' }}{{ $t('ha.updatedAt', { t: wxUpd }) }}</div>
            </div>
          </div>
        </el-popover>
        <div class="ha-actions">
          <!-- 用户下拉（43）：头像+名字，主题色板/回到主页/管理设备/控制台/个人信息/退出；
               受限用户（421）不显示导航三项，个人信息走本页弹层（改密+头像） -->
          <el-dropdown trigger="click" @command="onUserCmd">
            <button type="button" class="ha-user">
              <img v-if="avatar" :src="avatar" class="ha-user-avatar" alt="" />
              <span v-else class="ha-user-avatar ha-user-letter">{{ (me.username || $t('ha.userInitial')).slice(0, 1).toUpperCase() }}</span>
              <span class="ha-user-name">{{ me.username || $t('ha.userFallback') }}</span>
              <el-icon :size="12" class="ha-user-caret"><arrow-down /></el-icon>
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="theme:noop">
                  <span class="ha-dd-themes">
                    <button
                      v-for="t in THEME_SWATCHES" :key="t.key" type="button"
                      class="ha-dd-swatch" :class="{ cur: theme === t.key }" :title="$t('theme.' + t.key + '_tip')"
                      @click.stop="setTheme(t.key)"
                    >
                      <span class="ha-dd-dot" :style="{ background: t.bg }" />
                      <span class="ha-dd-label">{{ $t('theme.' + t.key) }}</span>
                    </button>
                  </span>
                </el-dropdown-item>
                <el-dropdown-item v-if="isAdmin" command="home" divided>
                  <el-icon><HomeFilled /></el-icon>{{ $t('ha.ddHome') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="isAdmin" command="manage">
                  <el-icon><Setting /></el-icon>{{ $t('ha.ddManage') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="isAdmin" command="console">
                  <el-icon><Monitor /></el-icon>{{ $t('ha.ddConsole') }}
                </el-dropdown-item>
                <el-dropdown-item command="profile" divided>
                  <el-icon><UserFilled /></el-icon>{{ $t('ha.ddProfile') }}
                </el-dropdown-item>
                <el-dropdown-item command="logout">
                  <el-icon><SwitchButton /></el-icon>{{ $t('ha.ddLogout') }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>

      <!-- 来源切换（多 HA 实例） -->
      <div v-if="sources.length > 1" class="ha-srcs">
        <button
          v-for="s in sources" :key="s.id" type="button"
          :class="['ha-src', { active: s.id === sourceId }]"
          @click="pickSource(s.id)"
        >{{ s.name }}<span v-if="!s.enabled" class="ha-src-off">{{ $t('ha.srcOff') }}</span></button>
      </div>

      <!-- 上游异常横幅 -->
      <div v-if="srcErr" class="ha-err">{{ $t('ha.unreachable', { e: srcErr }) }}</div>

      <!-- 无任何 HA 集成 -->
      <div v-if="!loading && sources.length === 0" class="ha-empty">
        <div class="ha-empty-ico" v-html="HA_BRAND_ICO" />
        <template v-if="isAdmin">
          <p>{{ $t('ha.emptyNoHa') }}</p>
          <p class="ha-empty-hint">{{ $t('ha.emptyNoHaHint') }}</p>
          <button class="ha-btn ha-btn-main" type="button" @click="$router.push('/console')">{{ $t('ha.goConsole') }}</button>
        </template>
        <template v-else>
          <p>{{ $t('ha.emptyPanelNoHa') }}</p>
          <p class="ha-empty-hint">{{ $t('ha.emptyContactAdmin') }}</p>
        </template>
      </div>

      <!-- 设备/状态展示（397 参考 Apple 家庭式信息结构，视觉仍走本页深紫玻璃）：
           房间平铺分区——房名大字直接落背景不再包玻璃大卡，头行温/湿度读数与
           添加钮保留；可控设备改家庭式磁贴：双列圆角卡、左圆形图标（开态着色）
           +右名称/状态两行、无内嵌开关、整卡点按即开关；环境电器通栏宽磁贴
           保留行内统计/步进/开关；状态实体小磁贴随新风格 -->
      <div v-else>
        <!-- 拖拽排序（425）：管理员可拖房间节调顺序，拖到「未分组」节=移到末尾 -->
        <div
          v-for="sec in displaySections" :key="sec.key"
          :class="['ha-room', { dragging: roomDrag.id && roomDrag.id === sec.roomId, over: roomDrag.id && roomDrag.over === sec.key }]"
          :draggable="isAdmin && !!sec.roomId"
          @dragstart="roomStart(sec, $event)" @dragend="roomEnd"
          @dragenter="roomOver(sec, $event)" @dragover="roomOver(sec, $event)"
          @drop.prevent="roomDrop(sec)"
        >
          <div v-if="sec.name" class="ha-room-head">
            <span class="ha-room-name">{{ sec.name }}</span>
            <!-- 头行状态 chips（390 建，406 靠右集中）：温湿度读数按数值分档
                 着色；锁可点按开关；门窗开合只读。整组右对齐紧邻添加钮 -->
            <span class="ha-head-sp" />
            <span v-if="!sec.battRoom && (sec.headSens.length || sec.locks.length || sec.doors.length)" class="ha-head-chips">
              <!-- 温/湿/电池芯片可点开历史曲线（510 补：光照为非数值态无曲线不可点） -->
              <button
                v-for="e in sec.headSens" :key="e.entity" type="button"
                :class="['ha-head-sensor', 'k-' + chipKind(e), { dead: e.unavailable || e.missing, clickable: chipKind(e) !== 'lux' }]"
                :style="{ color: chipColor(e) }"
                :title="`${e.entity}${e.state ? ' · ' + e.state : ''}${chipKind(e) === 'lux' ? '' : $t('ha.chipHistoryTip')}`"
                @click="onHeadChipClick(e)"
              >
                <!-- 电池（476 动态电量条）/光照（480 亮度形态）升头行（481） -->
                <svg v-if="chipKind(e) === 'batt'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                     stroke-linecap="round" stroke-linejoin="round">
                  <rect x="2.8" y="7.8" width="16.4" height="8.4" rx="2" />
                  <path d="M21.4 10.6v2.8" />
                  <rect v-if="battFillW(e) > 0" x="4.6" y="9.6" :width="battFillW(e)" height="4.8" rx="0.9" fill="currentColor" stroke="none" />
                </svg>
                <svg v-else-if="chipKind(e) === 'lux'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                     stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="12" cy="12" r="3.4" />
                  <path d="M8.6 12a3.4 3.4 0 0 0 6.8 0z" fill="currentColor" stroke="none" />
                  <path d="M12 2.6v2M12 19.4v2M2.6 12h2M19.4 12h2M5.4 5.4l1.4 1.4M17.2 17.2l1.4 1.4M18.6 5.4l-1.4 1.4M5.4 18.6l1.4-1.4" />
                </svg>
                <svg v-else-if="chipKind(e) === 'hum'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                     stroke-linecap="round" stroke-linejoin="round"><path d="M2 6.5c.6.5 1.2 1 2.5 1C7 7.5 7 5.5 9.5 5.5c2.6 0 2.4 2 5 2 2.5 0 2.5-2 5-2 1.3 0 1.9.5 2.5 1" /><path d="M2 12c.6.5 1.2 1 2.5 1 2.5 0 2.5-2 5-2 2.6 0 2.4 2 5 2 2.5 0 2.5-2 5-2 1.3 0 1.9.5 2.5 1" /><path d="M2 17.5c.6.5 1.2 1 2.5 1 2.5 0 2.5-2 5-2 2.6 0 2.4 2 5 2 2.5 0 2.5-2 5-2 1.3 0 1.9.5 2.5 1" /></svg>
                <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                     stroke-linecap="round" stroke-linejoin="round"><path d="M10 4a2 2 0 0 1 4 0v9.2a4.6 4.6 0 1 1-4 0z" /><circle cx="12" cy="16.8" r="1.6" fill="currentColor" stroke="none" /></svg>
                {{ chipVal(e) }}<i v-if="chipUnit(e)">{{ chipUnit(e) }}</i>
              </button>
              <button
                v-for="d in sec.locks" :key="d.entity" type="button"
                :class="['ha-head-lock', { dead: d.unavailable || d.missing }]"
                :style="{ color: lockColor(d) }"
                :disabled="d.unavailable || d.missing || pending.has(d.entity)"
                :title="`${d.entity} · ${d.state || '-'}${d.on ? $t('ha.lockTipLock') : $t('ha.lockTipUnlock')}`"
                @click="toggle(d)"
              >
                <span v-if="pending.has(d.entity)" class="ha-dev-spin ha-head-spin" />
                <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                     stroke-linecap="round" stroke-linejoin="round" v-html="haIconSvg((d.state || '').trim().toLowerCase() === 'unlocked' ? 'lock_open' : 'lock')" />
                {{ stateText(d) }}
              </button>
              <!-- 门窗芯片（406 只读 → 510 可点）：点开开关日志清单 -->
              <button
                v-for="e in sec.doors" :key="e.entity"
                type="button"
                :class="['ha-head-door', { open: doorOpen(e) && !e.unavailable && !e.missing, dead: e.unavailable || e.missing }]"
                :title="`${e.entity}${e.state ? ' · ' + e.state : ''}${$t('ha.doorLogTip')}`"
                @click="openDevLog(e, 'door')"
              >
                <svg v-if="doorKind(e) === 'win'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                     stroke-linecap="round" stroke-linejoin="round" v-html="haIconSvg(doorOpen(e) ? 'win_open' : 'win')" />
                <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                     stroke-linecap="round" stroke-linejoin="round" v-html="haIconSvg(doorOpen(e) ? 'door_open' : 'door')" />
                {{ doorStateText(e) }}
              </button>
            </span>
            <!-- 房间内添加设备（394）：弹目录选择框，点选即入该房 -->
            <button
              v-if="isAdmin" type="button" class="ha-room-addbtn"
              :title="$t('ha.addToRoom', { name: sec.name })"
              @click="openPicker(sec.key)"
            >＋</button>
          </div>

          <!-- 家庭式磁贴区（397）：环境电器=通栏宽磁贴（保留统计/步进/开关），
               其余可控设备=标准磁贴，整卡点按即开关 -->
          <div v-if="sec.env.length || sec.devs.length" class="ha-tile-grid">
            <!-- 环境电器宽磁贴：开关走右侧拨动钮（510 起卡体其余区域点开开关日志） -->
            <div
              v-for="d in sec.env" :key="d.entity"
              :class="['ha-env', 'ha-tile', { on: d.on && !d.unavailable && !d.missing, dead: d.unavailable || d.missing, dragging: dragDev.id === d.entity, over: dragDev.id && dragDev.over === d.entity }]"
              :draggable="isAdmin"
              @dragstart="tileStart(d, $event)" @dragend="tileEnd"
              @dragenter.stop="tileOver(d, $event)" @dragover.stop="tileOver(d, $event)"
              @drop.prevent.stop="tileDrop(d)"
              @click="openDevLog(d)"
            >
              <span class="ha-tile-ico">
                <img v-if="devIcon(d)" :src="devIcon(d)" alt="" draggable="false" @error="iconFail[d.entity] = true">
                <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                     stroke-linecap="round" stroke-linejoin="round" v-html="haTileIcon(d)" />
              </span>
              <span class="ha-tile-main">
                <span class="ha-tile-name" :title="`${d.entity} · ${d.state || '-'}`">{{ d.label || d.entity }}</span>
                <span class="ha-tile-state">{{ envStateLine(d) }}</span>
              </span>
              <span class="ha-env-stats">
                <template v-if="d.domain === 'climate' || d.domain === 'water_heater'">
                  <span v-if="d.env && d.env.cur_temp != null" class="ha-env-stat"><i>{{ $t('ha.cur') }}</i><b>{{ fmtNum(d.env.cur_temp) }}<em>°C</em></b></span>
                  <span v-if="d.env && d.env.cur_hum != null" class="ha-env-stat"><i>{{ $t('ha.humidity') }}</i><b>{{ fmtNum(d.env.cur_hum) }}<em>%</em></b></span>
                  <span v-if="d.env && d.env.tgt_temp != null && !d.unavailable && !d.missing" class="ha-env-stat">
                    <i>{{ $t('ha.target') }}</i>
                    <b class="ha-env-step">
                      <button type="button" :disabled="pending.has(d.entity)" :title="$t('ha.decTemp')" @click.stop="envAdjust(d, -1)">−</button>
                      <span>{{ fmtNum(d.env.tgt_temp) }}°</span>
                      <button type="button" :disabled="pending.has(d.entity)" :title="$t('ha.incTemp')" @click.stop="envAdjust(d, 1)">＋</button>
                    </b>
                  </span>
                  <!-- 空调电量读数收进控制条（451）：月电量/日电量/电功率取一
                       位小数，同节命中关键词的传感器不再单独占磁贴（归属规则
                       见 acEmbeds）；510 起读数可点开历史曲线 -->
                  <template v-if="d.domain === 'climate' && sec.embeds && sec.embeds[d.entity]">
                    <span v-if="sec.embeds[d.entity].mon" class="ha-env-stat ha-stat-link" :title="$t('ha.viewHistory')"
                          @click.stop="openEmbedChart(sec.embeds[d.entity].mon, d, $t('ha.eMon'))">
                      <i>{{ $t('ha.eMon') }}</i><b>{{ energyVal(sec.embeds[d.entity].mon) }}<em v-if="embedUnit(sec.embeds[d.entity].mon)">{{ embedUnit(sec.embeds[d.entity].mon) }}</em></b>
                    </span>
                    <span v-if="sec.embeds[d.entity].day" class="ha-env-stat ha-stat-link" :title="$t('ha.viewHistory')"
                          @click.stop="openEmbedChart(sec.embeds[d.entity].day, d, $t('ha.eDay'))">
                      <i>{{ $t('ha.eDay') }}</i><b>{{ energyVal(sec.embeds[d.entity].day) }}<em v-if="embedUnit(sec.embeds[d.entity].day)">{{ embedUnit(sec.embeds[d.entity].day) }}</em></b>
                    </span>
                    <span v-if="sec.embeds[d.entity].pwr" class="ha-env-stat ha-stat-link" :title="$t('ha.viewHistory')"
                          @click.stop="openEmbedChart(sec.embeds[d.entity].pwr, d, $t('ha.ePwr'))">
                      <i>{{ $t('ha.ePwr') }}</i><b>{{ energyVal(sec.embeds[d.entity].pwr) }}<em v-if="embedUnit(sec.embeds[d.entity].pwr)">{{ embedUnit(sec.embeds[d.entity].pwr) }}</em></b>
                    </span>
                  </template>
                </template>
                <template v-else-if="d.domain === 'humidifier'">
                  <span v-if="d.env && d.env.cur_hum != null" class="ha-env-stat"><i>{{ $t('ha.curHum') }}</i><b>{{ fmtNum(d.env.cur_hum) }}<em>%</em></b></span>
                  <span v-if="d.env && d.env.tgt_hum != null && !d.unavailable && !d.missing" class="ha-env-stat">
                    <i>{{ $t('ha.tgtHum') }}</i>
                    <b class="ha-env-step">
                      <button type="button" :disabled="pending.has(d.entity)" :title="$t('ha.decHum')" @click.stop="envAdjust(d, -1)">−</button>
                      <span>{{ fmtNum(d.env.tgt_hum) }}%</span>
                      <button type="button" :disabled="pending.has(d.entity)" :title="$t('ha.incHum')" @click.stop="envAdjust(d, 1)">＋</button>
                    </b>
                  </span>
                  <!-- 滤芯剩余/水位收进主控件（427）：同房间命中关键词的传感器
                       不再单独占磁贴（归属规则见 humEmbeds） -->
                  <template v-if="sec.embeds && sec.embeds[d.entity]">
                    <span v-if="sec.embeds[d.entity].filter" class="ha-env-stat">
                      <i>{{ $t('ha.filter') }}</i><b>{{ embedVal(sec.embeds[d.entity].filter) }}<em v-if="embedUnit(sec.embeds[d.entity].filter)">{{ embedUnit(sec.embeds[d.entity].filter) }}</em></b>
                    </span>
                    <span v-if="sec.embeds[d.entity].water" class="ha-env-stat">
                      <i>{{ $t('ha.waterLevel') }}</i><b>{{ embedVal(sec.embeds[d.entity].water) }}<em v-if="embedUnit(sec.embeds[d.entity].water)">{{ embedUnit(sec.embeds[d.entity].water) }}</em></b>
                    </span>
                  </template>
                </template>
                <template v-else>
                  <span v-if="d.env && d.env.pct != null" class="ha-env-stat"><i>{{ $t('ha.fanSpeed') }}</i><b>{{ Math.round(d.env.pct) }}<em>%</em></b></span>
                </template>
                <span v-if="pending.has(d.entity)" class="ha-dev-spin ha-env-spin" />
              </span>
              <button
                class="ha-env-sw" type="button" role="switch" :aria-checked="d.on"
                :disabled="d.unavailable || d.missing || pending.has(d.entity)"
                :title="$t(d.on ? 'ha.off' : 'ha.on')"
                @click.stop="toggle(d)"
              ><i class="knob" /></button>
            </div>

            <!-- 标准设备磁贴（510 分叉）：灯=点灯图标开关、浴霸=点拨动开关钮，
                 卡体其余区域点开开关日志；其余可控设备维持整卡点按开关（397） -->
            <template v-for="d in sec.devs" :key="d.entity">
              <div
                v-if="isLightDev(d) || isYubaDev(d)"
                :class="['ha-ctl', 'ha-tile', 'ha-ctl-div', { on: d.on && !d.unavailable && !d.missing, dead: d.unavailable || d.missing, dragging: dragDev.id === d.entity, over: dragDev.id && dragDev.over === d.entity }]"
                :title="`${d.entity} · ${d.state || '-'}${$t('ha.lightTileTip', { act: $t(d.on ? 'ha.charOff' : 'ha.charOn') })}`"
                :draggable="isAdmin && !d.unavailable && !d.missing"
                @dragstart="tileStart(d, $event)" @dragend="tileEnd"
                @dragenter.stop="tileOver(d, $event)" @dragover.stop="tileOver(d, $event)"
                @drop.prevent.stop="tileDrop(d)"
                @click="openDevLog(d)"
              >
                <button
                  v-if="isLightDev(d)"
                  class="ha-tile-ico" type="button" role="switch" :aria-checked="d.on"
                  :disabled="d.unavailable || d.missing || pending.has(d.entity)"
                  :title="$t(d.on ? 'ha.off' : 'ha.on')"
                  @click.stop="toggle(d)"
                >
                  <img v-if="devIcon(d)" :src="devIcon(d)" alt="" draggable="false" @error="iconFail[d.entity] = true">
                  <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round" v-html="haTileIcon(d)" />
                </button>
                <span v-else class="ha-tile-ico">
                  <img v-if="devIcon(d)" :src="devIcon(d)" alt="" draggable="false" @error="iconFail[d.entity] = true">
                  <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round" v-html="haTileIcon(d)" />
                </span>
                <span class="ha-tile-main">
                  <span class="ha-tile-name">{{ d.label || d.entity }}</span>
                  <span class="ha-tile-state" :class="{ on: d.on && !d.unavailable && !d.missing }">{{ stateText(d) }}</span>
                </span>
                <button
                  v-if="isYubaDev(d)"
                  class="ha-env-sw" type="button" role="switch" :aria-checked="d.on"
                  :disabled="d.unavailable || d.missing || pending.has(d.entity)"
                  :title="$t(d.on ? 'ha.off' : 'ha.on')"
                  @click.stop="toggle(d)"
                ><i class="knob" /></button>
                <span v-if="pending.has(d.entity)" class="ha-dev-spin" :class="isYubaDev(d) ? 'ha-env-spin' : 'ha-ctl-spin'" />
              </div>
              <button
                v-else type="button"
                :class="['ha-ctl', 'ha-tile', { on: d.on && !d.unavailable && !d.missing, dead: d.unavailable || d.missing, dragging: dragDev.id === d.entity, over: dragDev.id && dragDev.over === d.entity }]"
                role="switch" :aria-checked="d.on"
                :disabled="d.unavailable || d.missing || pending.has(d.entity)"
                :title="`${d.entity} · ${d.state || '-'}${$t('ha.clickToggleTip')}`"
                :draggable="isAdmin && !d.unavailable && !d.missing"
                @dragstart="tileStart(d, $event)" @dragend="tileEnd"
                @dragenter.stop="tileOver(d, $event)" @dragover.stop="tileOver(d, $event)"
                @drop.prevent.stop="tileDrop(d)"
                @click="toggle(d)"
              >
                <span class="ha-tile-ico">
                  <img v-if="devIcon(d)" :src="devIcon(d)" alt="" draggable="false" @error="iconFail[d.entity] = true">
                  <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round" v-html="haTileIcon(d)" />
                </span>
                <span class="ha-tile-main">
                  <span class="ha-tile-name">{{ d.label || d.entity }}</span>
                  <span class="ha-tile-state" :class="{ on: d.on && !d.unavailable && !d.missing }">{{ stateText(d) }}</span>
                </span>
                <span v-if="pending.has(d.entity)" class="ha-dev-spin ha-ctl-spin" />
              </button>
            </template>
          </div>
          <div v-if="sec.sens.length || sec.egroups.length" class="ha-sensor-grid">
            <!-- 电量表合并模块（478）：同一设备 功率/月电量/日电量 一卡展示
                 （读数一位小数，energyVal 复用 451 口径）；不参与拖拽排序；
                 510 起点开历史曲线（三序列同图） -->
            <div v-for="g in sec.egroups" :key="g.key" class="ha-sensor ha-egroup" :title="$t('ha.viewHistory')"
                 @click="openEgroupChart(g)">
              <div class="ha-sensor-name" :title="g.name">{{ g.name }}</div>
              <div class="ha-egroup-stats">
                <span v-if="g.mon" class="ha-egroup-stat"><i>{{ $t('ha.eMon') }}</i><b>{{ energyVal(g.mon) }}<em v-if="embedUnit(g.mon)">{{ embedUnit(g.mon) }}</em></b></span>
                <span v-if="g.day" class="ha-egroup-stat"><i>{{ $t('ha.eDay') }}</i><b>{{ energyVal(g.day) }}<em v-if="embedUnit(g.day)">{{ embedUnit(g.day) }}</em></b></span>
                <span v-if="g.pwr" class="ha-egroup-stat"><i>{{ $t('ha.ePwr') }}</i><b>{{ energyVal(g.pwr) }}<em v-if="embedUnit(g.pwr)">{{ embedUnit(g.pwr) }}</em></b></span>
              </div>
            </div>
            <div
              v-for="sn in sec.sens" :key="sn.entity"
              :class="['ha-sensor', { dead: sn.unavailable || sn.missing, dragging: dragDev.id === sn.entity, over: dragDev.id && dragDev.over === sn.entity, clickable: sensClickable(sn) }]"
              :title="sensClickable(sn) ? $t(isDoorSensor(sn) ? 'ha.doorLogShort' : 'ha.viewHistory') : ''"
              :draggable="isAdmin"
              @dragstart="tileStart(sn, $event)" @dragend="tileEnd"
              @dragenter.stop="tileOver(sn, $event)" @dragover.stop="tileOver(sn, $event)"
              @drop.prevent.stop="tileDrop(sn)"
              @click="onSensClick(sn)"
            >
              <!-- 电池（476）/光照（477）传感器配专用图标：电池=取整百分比，
                   光照=数值/单位原样；其余传感器原样 -->
              <!-- 电量/电池房间（514）：数值态电池磁贴改进度条（一位小数百分比，
                   红橙绿渐变随剩余电量变色）；非数值态/其他房间走原图标分支 -->
              <template v-if="sec.battRoom && isRoomBatt(sn) && battPct(sn)">
                <div class="ha-batt-row">
                  <!-- 图标跟进度条三档联动（520）：色同档色、绿=满格/橙=半格/红=空格 -->
                  <svg class="ha-sens-ico" viewBox="0 0 24 24" fill="none" :stroke="battBarColor(battNum(sn))" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round">
                    <rect x="2.8" y="7.8" width="16.4" height="8.4" rx="2" />
                    <path d="M21.4 10.6v2.8" />
                    <rect v-if="battIcoFillW(battNum(sn)) > 0" x="4.6" y="9.6" :width="battIcoFillW(battNum(sn))" height="4.8" rx="0.9"
                          :fill="battBarColor(battNum(sn))" stroke="none" />
                  </svg>
                  <b class="ha-batt-num" :style="{ color: battBarColor(battNum(sn)) }">{{ Math.min(100, Math.max(0, battNum(sn))).toFixed(1) }}<i>%</i></b>
                </div>
                <div class="ha-batt-bar">
                  <i :style="{ width: Math.max(2, Math.min(100, battNum(sn))) + '%', background: battBarGrad(battNum(sn)) }" />
                </div>
              </template>
              <div v-else class="ha-sensor-val">
                <template v-if="isBatterySensor(sn)">
                  <svg class="ha-sens-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round">
                    <rect x="2.8" y="7.8" width="16.4" height="8.4" rx="2" />
                    <path d="M21.4 10.6v2.8" />
                    <rect v-if="battFillW(sn) > 0" x="4.6" y="9.6" :width="battFillW(sn)" height="4.8" rx="0.9" fill="currentColor" stroke="none" />
                  </svg>{{ battVal(sn) }}<span v-if="battPct(sn)" class="ha-sensor-unit">%</span>
                </template>
                <template v-else-if="isLuxSensor(sn)">
                  <svg class="ha-sens-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                       stroke-linecap="round" stroke-linejoin="round">
                    <circle cx="12" cy="12" r="3.4" />
                    <path d="M8.6 12a3.4 3.4 0 0 0 6.8 0z" fill="currentColor" stroke="none" />
                    <path d="M12 2.6v2M12 19.4v2M2.6 12h2M19.4 12h2M5.4 5.4l1.4 1.4M17.2 17.2l1.4 1.4M18.6 5.4l-1.4 1.4M5.4 18.6l1.4-1.4" />
                  </svg>{{ sensorVal(sn) }}<span v-if="!sn.unavailable && !sn.missing && sn.unit" class="ha-sensor-unit">{{ sn.unit }}</span>
                </template>
                <template v-else>{{ sensorVal(sn) }}<span v-if="!sn.unavailable && !sn.missing && sn.unit" class="ha-sensor-unit">{{ sn.unit }}</span></template>
              </div>
              <div class="ha-sensor-name" :title="`${sn.entity}${sn.state ? ' · ' + sn.state : ''}`">{{ sn.label || sn.entity }}</div>
            </div>
          </div>

          <!-- 空房间（394）：先建空房间再添加设备的工作流，不再跳过渲染 -->
          <div v-if="!sec.env.length && !sec.devs.length && !sec.sens.length && !sec.egroups.length" class="ha-room-empty">
            {{ isAdmin ? $t('ha.roomEmptyAdmin') : $t('ha.roomEmpty') }}
          </div>
        </div>

        <!-- 新建房间（394）：提到页面直建，可先建空房间 -->
        <button v-if="isAdmin" type="button" class="ha-new-room" @click="addRoomInline">{{ $t('ha.newRoom') }}</button>
      </div>

      <div v-if="!loading && sources.length > 0 && devices.length === 0 && !sensors.length" class="ha-empty">
        <template v-if="isAdmin">
          <p>{{ $t('ha.emptyNoPicked') }}</p>
          <button class="ha-btn ha-btn-main" type="button" @click="openManage">{{ $t('ha.pickDevices') }}</button>
        </template>
        <template v-else>
          <p>{{ $t('ha.emptyAdminNotConfigured') }}</p>
        </template>
      </div>
    </div>

    <!-- 管理设备弹窗（仅管理员）：收藏清单编辑 + 从目录挑选 -->
    <el-dialog v-model="mgOpen" :title="$t('ha.manageTitle')" width="640px" class="ha-mg-dlg" append-to-body>
      <div class="ha-mg">
        <div class="ha-mg-tip">{{ $t('ha.mgTip') }}</div>
        <div class="ha-mg-sec">{{ $t('ha.connSec') }}</div>
        <div class="ha-mg-conn">
          <el-input v-model="mgConn.url" :placeholder="$t('ha.phServiceUrl')" size="small" />
          <el-input v-model="mgConn.token" type="password" show-password autocomplete="new-password" size="small" :placeholder="mgConn.has_token ? $t('ha.phTokenSaved') : $t('ha.phToken')" />
          <label class="ha-mg-conn-opt"><input type="checkbox" v-model="mgConn.insecure">{{ $t('ha.insecure') }}</label>
          <div class="ha-mg-conn-btns">
            <el-button size="small" :loading="mgConnTesting" @click="testMaster">{{ $t('ha.testConn') }}</el-button>
            <el-button size="small" type="primary" :loading="mgConnSaving" @click="saveMaster">{{ $t('ha.saveConn') }}</el-button>
          </div>
        </div>
        <div class="ha-mg-sec">{{ $t('ha.roomsSec') }}</div>
        <div class="ha-mg-rooms">
          <button type="button" :class="['ha-room-chip', { active: mgRoomSel === '' }]" @click="mgRoomSel = ''">{{ $t('ha.ungrouped') }}</button>
          <button
            v-for="r in mgRooms" :key="r.id" type="button"
            :class="['ha-room-chip', { active: mgRoomSel === r.id }]"
            :title="r.name || $t('ha.unnamedRoom')"
            @click="mgRoomSel = r.id"
          >{{ r.name || $t('ha.unnamedRoom') }}</button>
          <button type="button" class="ha-room-add" :disabled="mgRooms.length >= 12" @click="mgAddRoom">{{ $t('ha.addRoom') }}</button>
        </div>
        <div v-if="mgRoomSel" class="ha-room-ops">
          <span class="ha-room-cur">{{ $t('ha.curRoomEnts', { name: roomLabel(mgRoomSel), n: mgListCur.length + mgSensorsCur.length }) }}</span>
          <button type="button" class="ha-room-op" @click="mgRenameRoom">{{ $t('ha.rename') }}</button>
          <button type="button" class="ha-room-op" @click="mgMoveRoomSel(-1)">{{ $t('ha.moveUp') }}</button>
          <button type="button" class="ha-room-op" @click="mgMoveRoomSel(1)">{{ $t('ha.moveDown') }}</button>
          <button type="button" class="ha-room-op danger" @click="mgDelRoomSel">{{ $t('ha.delRoom') }}</button>
        </div>
        <div class="ha-mg-sec">{{ $t('ha.ctlDevices', { tag: roomTag() }) }}</div>
        <div v-if="mgListCur.length" class="ha-mg-list">
          <div v-for="d in mgListCur" :key="d.entity" class="ha-mg-row">
            <span class="ha-mg-ent" :title="d.entity">{{ d.entity }}</span>
            <el-input v-model="d.label" :placeholder="$t('ha.phLabel')" size="small" maxlength="60" class="ha-mg-label" />
            <el-select v-model="d.room" size="small" class="ha-mg-room" :title="$t('ha.moveToRoom')">
              <el-option value="" :label="$t('ha.toUngrouped')" />
              <el-option v-for="r in mgRooms" :key="r.id" :value="r.id" :label="'→ ' + (r.name || $t('ha.unnamedRoom'))" />
            </el-select>
            <el-input v-model="d.icon_on" :placeholder="$t('ha.phIconOn')" size="small" maxlength="500" class="ha-mg-ico" />
            <el-input v-model="d.icon_off" :placeholder="$t('ha.phIconOff')" size="small" maxlength="500" class="ha-mg-ico" />
            <el-icon class="ha-mg-del" :size="16" :title="$t('ha.removeFromList')" @click="mgRemoveItem(mgList, d.entity)"><Delete /></el-icon>
          </div>
        </div>
        <div v-else class="ha-mg-none">{{ $t('ha.mgNoDevs') }}</div>
        <div class="ha-mg-sec">{{ $t('ha.roEntities', { tag: roomTag() }) }}</div>
        <div v-if="mgSensorsCur.length" class="ha-mg-list">
          <div v-for="sn in mgSensorsCur" :key="sn.entity" class="ha-mg-row">
            <span class="ha-mg-ent" :title="sn.entity">{{ sn.entity }}</span>
            <el-input v-model="sn.label" :placeholder="$t('ha.phLabel')" size="small" maxlength="60" class="ha-mg-label" />
            <el-select v-model="sn.room" size="small" class="ha-mg-room" :title="$t('ha.moveToRoom')">
              <el-option value="" :label="$t('ha.toUngrouped')" />
              <el-option v-for="r in mgRooms" :key="r.id" :value="r.id" :label="'→ ' + (r.name || $t('ha.unnamedRoom'))" />
            </el-select>
            <el-icon class="ha-mg-del" :size="16" :title="$t('ha.removeFromList')" @click="mgRemoveItem(mgSensors, sn.entity)"><Delete /></el-icon>
          </div>
        </div>
        <div v-else class="ha-mg-none">{{ $t('ha.mgNoSens') }}</div>
        <el-input v-model="mgFilter" :placeholder="$t('ha.phSearch')" size="small" clearable class="ha-mg-search" />
        <div class="ha-mg-tabs">
          <button type="button" :class="['ha-mg-tab', { active: mgView === 'domain' }]" @click="mgView = 'domain'">{{ $t('ha.tabByDomain') }}</button>
          <button type="button" :class="['ha-mg-tab', { active: mgView === 'entity' }]" @click="mgView = 'entity'">{{ $t('ha.tabByEntity') }}</button>
        </div>
        <div v-if="mgView === 'domain'" class="ha-mg-catalog">
          <div v-for="g in mgCatalogView" :key="g.domain" class="ha-mg-group">
            <div class="ha-mg-domain">{{ g.domain }}</div>
            <button
              v-for="e in g.entities" :key="e.entity" type="button"
              :class="['ha-mg-ent-btn', { added: mgHas(e.entity) }]"
              :disabled="mgHas(e.entity)"
              :title="`${e.entity} · ${e.state}`"
              @click="mgAdd(e)"
            >{{ e.label }}</button>
          </div>
          <div v-if="!mgCatalog.length" class="ha-mg-none">{{ $t('ha.catalogEmpty') }}</div>
        </div>
        <div v-else class="ha-mg-catalog ha-mg-ents">
          <div v-for="e in mgEntityView" :key="e.entity" class="ha-mg-ent-item">
            <div class="ha-mg-ent-line">
              <button
                type="button"
                :class="['ha-mg-ent-row', { added: mgHas(e.entity) }]"
                :disabled="mgHas(e.entity)"
                :title="`${e.entity} · ${e.state}${$t('ha.clickAddTip')}`"
                @click="mgAdd(e)"
              >
                <span class="ha-mg-ent-id">{{ e.entity }}</span>
                <span class="ha-mg-ent-name">{{ e.label }}</span>
                <span class="ha-mg-ent-state">{{ e.state }}</span>
              </button>
              <button
                type="button" class="ha-mg-ent-exp"
                :title="mgExp === e.entity ? $t('ha.collapse') : $t('ha.viewFuncs')"
                @click="mgExp = mgExp === e.entity ? null : e.entity"
              >
                <el-icon :class="{ open: mgExp === e.entity }"><ArrowDown /></el-icon>
              </button>
            </div>
            <div v-if="mgExp === e.entity" class="ha-mg-funcs">
              <span class="ha-mg-func-cap">{{ $t('ha.functions') }}</span>
              <span v-for="f in entFunctions(e.entity.split('.')[0])" :key="f" class="ha-mg-func">{{ f }}</span>
              <span class="ha-mg-func-cur">{{ $t('ha.curState', { s: e.state || '-' }) }}</span>
            </div>
          </div>
          <div v-if="!mgEntityView.length" class="ha-mg-none">{{ $t('ha.noMatch') }}</div>
        </div>
      </div>
      <template #footer>
        <el-button @click="mgOpen = false">{{ $t('ha.cancel') }}</el-button>
        <el-button type="primary" :loading="mgSaving" @click="saveDevices">{{ $t('ha.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 房间添加设备弹窗（394）：按域分组目录，点选即加入该房间并即时保存 -->
    <el-dialog v-model="pkOpen" :title="$t('ha.addToRoom', { name: pkRoomName })" width="520px" class="ha-mg-dlg" append-to-body>
      <div class="ha-mg">
        <div class="ha-mg-tip">{{ $t('ha.pkTip', { name: pkRoomName }) }}</div>
        <el-input v-model="pkFilter" :placeholder="$t('ha.phSearch')" size="small" clearable class="ha-mg-search" />
        <div class="ha-mg-catalog ha-pk-catalog">
          <div v-for="g in pkView" :key="g.domain" class="ha-mg-group">
            <div class="ha-mg-domain">{{ g.domain }}</div>
            <button
              v-for="e in g.entities" :key="e.entity" type="button"
              :class="['ha-mg-ent-btn', { added: pkHas(e.entity) }]"
              :disabled="pkHas(e.entity)"
              :title="`${e.entity} · ${e.state}`"
              @click="pkAdd(e)"
            >{{ e.label }}</button>
          </div>
          <div v-if="!pkCatalog.length" class="ha-mg-none">{{ $t('ha.catalogEmpty') }}</div>
        </div>
      </div>
      <template #footer>
        <el-button @click="pkOpen = false">{{ $t('ha.done') }}</el-button>
      </template>
    </el-dialog>

    <!-- 开关日志清单（511）：灯/设备磁贴非按钮区、门窗芯片点开 -->
    <el-dialog v-model="logOpen" :title="$t('ha.logTitle', { name: logName })" width="440px" class="ha-mg-dlg" append-to-body>
      <div v-if="logRows.length" class="ha-log-list">
        <div v-for="(r, i) in logRows" :key="`${r.ts}-${i}`" class="ha-log-row">
          <span class="ha-log-time">{{ fmtLogTime(r.ts) }}</span>
          <span class="ha-log-act" :class="{ on: r.to === 'on' }">{{ logActText(r) }}</span>
        </div>
      </div>
      <div v-else class="ha-log-empty">{{ logLoading ? $t('ha.loading') : $t('ha.noLog') }}</div>
      <template #footer>
        <span class="ha-log-note">{{ $t('ha.logNote') }}</span>
        <el-button @click="logOpen = false">{{ $t('ha.close') }}</el-button>
      </template>
    </el-dialog>

    <!-- 数值历史曲线（512）：温/湿/电量传感器磁贴、电量表模块、空调电量读数点开 -->
    <el-dialog v-model="chartOpen" :title="$t('ha.chartTitle', { name: chartName })" width="660px" class="ha-mg-dlg" append-to-body>
      <div class="ha-chart-bar">
        <el-select v-model="chartSecs" style="width: 110px" @change="loadChart">
          <el-option v-for="r in CHART_RANGES" :key="r.value" :value="r.value" :label="$t('ha.' + r.key)" />
        </el-select>
        <span class="ha-chart-note">{{ $t('ha.chartNote') }}</span>
      </div>
      <SeriesChart v-if="chartHasData" :series="chartSeries" height="300px" />
      <div v-else class="ha-log-empty">{{ chartLoading ? $t('ha.loading') : $t('ha.chartEmpty') }}</div>
    </el-dialog>

    <!-- 个人信息弹层（43）：受限用户在本页改名/改密/传头像；管理员跳后台个人信息页不经此 -->
    <el-dialog append-to-body v-model="profDlg" :title="$t('ha.profileTitle')" width="min(420px, 94vw)">
      <div class="prof-ava">
        <img v-if="avatar" :src="avatar" alt="" />
        <span v-else class="prof-ava-letter">{{ (me.username || $t('ha.userInitial')).slice(0, 1).toUpperCase() }}</span>
        <div class="prof-ava-ops">
          <el-button size="small" type="primary" plain @click="avaInput?.click()">{{ $t('ha.uploadAvatar') }}</el-button>
          <el-button v-if="avatar" size="small" @click="clearAvatar">{{ $t('ha.removeAvatar') }}</el-button>
        </div>
        <input ref="avaInput" type="file" accept="image/*" style="display: none" @change="onAvatarFile" />
      </div>
      <el-form label-width="82px" @submit.prevent>
        <el-form-item :label="$t('ha.fUsername')"><el-input v-model="prof.username" maxlength="32" /></el-form-item>
        <el-form-item :label="$t('ha.fOldPass')"><el-input v-model="prof.old" type="password" show-password /></el-form-item>
        <el-form-item :label="$t('ha.fNewPass')"><el-input v-model="prof.next" type="password" show-password :placeholder="$t('ha.phKeepPass')" /></el-form-item>
        <el-form-item :label="$t('ha.fConfirmPass')"><el-input v-model="prof.confirm" type="password" show-password /></el-form-item>
      </el-form>
      <!-- 自助两步验证（45 按用户隔离）：本账号独立开启/关闭 -->
      <el-divider style="margin: 4px 0 12px" />
      <div class="prof-totp-head">{{ $t('ha.totpHead') }}<span v-if="totpEnabled" class="prof-totp-on">{{ $t('ha.totpOn') }}</span></div>
      <template v-if="!totpEnabled">
        <div v-if="!totpSetup" class="prof-totp-row">
          <el-button size="small" type="primary" plain :loading="totpBusy" @click="startTotp">{{ $t('ha.totpEnableBtn') }}</el-button>
          <span class="prof-totp-tip">{{ $t('ha.totpEnableTip') }}</span>
        </div>
        <template v-else>
          <img v-if="totpSetup.qr" :src="totpSetup.qr" class="prof-totp-qr" :alt="$t('ha.totpQrAlt')" />
          <div class="prof-totp-tip">
            {{ $t('ha.totpSetupTip') }}<span class="prof-totp-secret">{{ totpSetup.secret }}</span>
          </div>
          <div class="prof-totp-row">
            <el-input v-model="totpCode" size="small" :placeholder="$t('ha.phTotpCode')" maxlength="6" inputmode="numeric" style="width: 160px" />
            <el-button size="small" type="primary" :loading="totpBusy" @click="confirmTotp">{{ $t('ha.totpConfirmBtn') }}</el-button>
            <el-button size="small" @click="cancelTotp">{{ $t('ha.cancel') }}</el-button>
          </div>
        </template>
      </template>
      <template v-else>
        <div class="prof-totp-row">
          <el-input v-model="totpCode" size="small" :placeholder="$t('ha.phTotpCur')" maxlength="6" inputmode="numeric" style="width: 160px" />
          <el-button size="small" type="danger" plain :loading="totpBusy" @click="disableTotp">{{ $t('ha.totpDisableBtn') }}</el-button>
        </div>
        <div class="prof-totp-tip" style="margin-top: 6px">{{ $t('ha.totpDisableTip') }}</div>
      </template>
      <template #footer>
        <el-button @click="profDlg = false">{{ $t('ha.cancel') }}</el-button>
        <el-button type="primary" :loading="profSaving" @click="saveProfile">{{ $t('ha.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, ArrowUp, Delete, HomeFilled, Monitor, Setting, SwitchButton, UserFilled } from '@element-plus/icons-vue'
import { api, bumpAvatarVer, clearSession, getAvatar, getAvatarVer, getRole, getToken, setAvatar, setRole, setUsername, getUsername } from '../api/index.js'
import { compressAvatar } from '../utils/avatar.js'
import { haIconSvg, haTileIcon, HA_BRAND_ICO } from '../utils/haIcons.js'
import { windLevel, wmoIcon, wmoText, wxDayName, wxDayPrecip, wxDayTitle } from '../utils/weather.js'
import { theme, setTheme, THEME_SWATCHES, FRONT_THEMES } from '../utils/theme.js'
import SeriesChart from '../components/SeriesChart.vue'
import pageMsgs from '../i18n/pages/ha.js'
import { registerPageMessages } from '../i18n'

// 页面语言包（ns ha）：本页文案注册进全局（重复注册无害）
registerPageMessages(pageMsgs)
// i18n：脚本用 t()，模板走全局 $t
const { t } = useI18n()

// 品牌暗色主题（38 gold + 41 八套）：后台选中时 HA 整套跟随，紫罗兰仍是缺省
const frontCls = computed(() => (FRONT_THEMES.includes(theme.value) ? theme.value : ''))
// 用户下拉（43）：头像/名字/主题色板/导航/个人信息
const avatar = ref(getAvatar())
const profDlg = ref(false)
const prof = reactive({ username: '', old: '', next: '', confirm: '' })
const profSaving = ref(false)
const avaInput = ref(null)

function onUserCmd(cmd) {
  if (cmd === 'home') return router.push('/')
  if (cmd === 'manage') return openManage()
  if (cmd === 'console') return router.push('/console')
  if (cmd === 'profile') {
    if (isAdmin.value) return router.push({ path: '/console/status', query: { tab: 'profile' } })
    prof.username = me.username || getUsername() || ''
    prof.old = prof.next = prof.confirm = ''
    totpSetup.value = null
    totpCode.value = ''
    profDlg.value = true
    loadTotp()
    return
  }
  if (cmd === 'logout') logout()
}

// 受限用户个人信息保存：改名/改密一体（不改密码 new_password 传空）
async function saveProfile() {
  const username = prof.username.trim()
  if (!username) { ElMessage.warning(t('ha.needUsername')); return }
  if (prof.next || prof.confirm) {
    if (!prof.old) { ElMessage.warning(t('ha.needOldPass')); return }
    if (prof.next !== prof.confirm) { ElMessage.warning(t('ha.passMismatch')); return }
  }
  profSaving.value = true
  try {
    await api.put('/auth/profile', { username, old_password: prof.old, new_password: prof.next || '' })
    setUsername(username)
    me.username = username
    prof.old = prof.next = prof.confirm = ''
    profDlg.value = false
    window.dispatchEvent(new CustomEvent('profile-updated', { detail: { username } }))
    ElMessage.success(t('ha.saved'))
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    profSaving.value = false
  }
}

async function onAvatarFile(ev) {
  const file = ev.target.files?.[0]
  ev.target.value = ''
  if (!file) return
  if (!file.type.startsWith('image/')) { ElMessage.warning(t('ha.needImage')); return }
  try {
    const dataURL = await compressAvatar(file)
    await api.put('/auth/avatar', { avatar: dataURL })
    avatar.value = dataURL
    setAvatar(dataURL)
    bumpAvatarVer()
    ElMessage.success(t('ha.avatarUpdated'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function clearAvatar() {
  try {
    await api.put('/auth/avatar', { avatar: '' })
    avatar.value = ''
    setAvatar('')
    bumpAvatarVer()
    ElMessage.success(t('ha.avatarRemoved'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// 自助两步验证（45 按用户隔离）：开启扫码验码确认、关闭验码确认，只约束本账号
const totpEnabled = ref(false)
const totpSetup = ref(null) // {secret, uri, qr}
const totpCode = ref('')
const totpBusy = ref(false)

async function loadTotp() {
  try {
    totpEnabled.value = !!(await api.get('/auth/totp-status')).enabled
  } catch { /* 状态加载失败不阻塞弹层 */ }
}

async function startTotp() {
  totpBusy.value = true
  try {
    totpSetup.value = await api.post('/auth/totp-setup')
    totpCode.value = ''
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    totpBusy.value = false
  }
}

function cancelTotp() {
  totpSetup.value = null
  totpCode.value = ''
}

async function confirmTotp() {
  if (!totpCode.value.trim()) { ElMessage.warning(t('ha.needCode')); return }
  totpBusy.value = true
  try {
    await api.post('/auth/totp-enable', { code: totpCode.value.trim() })
    ElMessage.success(t('ha.totpEnabledMsg'))
    cancelTotp()
    totpEnabled.value = true
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    totpBusy.value = false
  }
}

async function disableTotp() {
  if (!totpCode.value.trim()) { ElMessage.warning(t('ha.needCode')); return }
  totpBusy.value = true
  try {
    await api.post('/auth/totp-disable', { code: totpCode.value.trim() })
    ElMessage.success(t('ha.totpDisabledMsg'))
    totpCode.value = ''
    totpEnabled.value = false
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    totpBusy.value = false
  }
}

// 头像异步回填（同 Layout 口径：private 短缓存 + ?v= 破缓存）
api.get(`/auth/avatar?v=${getAvatarVer()}`).then((r) => {
  if ((r.avatar || '') !== getAvatar()) {
    setAvatar(r.avatar || '')
    avatar.value = r.avatar || ''
  }
}).catch(() => {})

const router = useRouter()

const isAdmin = ref(getRole() === 'admin')

// 品牌回主页（13）与右上折叠菜单（13）：受限用户品牌不作按钮态（421 口径不变）
function goHome() {
  if (isAdmin.value) router.push('/')
}
const me = reactive({ username: getUsername() })
const sources = ref([])
const sourceId = ref(0)
const devices = ref([])
const sensors = ref([])
const rooms = ref([])
const srcErr = ref('')
const loading = ref(true)
const pending = reactive(new Set())
const iconFail = reactive({})

// 状态文案映射（i18n 化）：值改为 ha.* 键名，stTxt() 取当前语言，表外原样透传
const STATE_TEXT = {
  on: 'stOn', off: 'stOff', locked: 'stLocked', unlocked: 'stUnlocked',
  locking: 'stLocking', unlocking: 'stUnlocking',
  open: 'stOpen', closed: 'stClosed', opening: 'stOpening', closing: 'stClosing',
  cleaning: 'stCleaning', idle: 'stIdle', docked: 'stDocked', returning: 'stReturning',
  playing: 'stPlaying', paused: 'stPaused', standby: 'stStandby', heating: 'stHeating',
  cooling: 'stCooling', 'auto': 'stAuto', 'dry': 'stDry', 'fan_only': 'stFanOnly',
}
function stTxt(v) {
  const k = STATE_TEXT[(v || '').toLowerCase()]
  return k ? t('ha.' + k) : ''
}
function stateText(d) {
  if (d.missing) return t('ha.stMissing')
  if (d.unavailable) return t('ha.stOffline')
  return stTxt(d.state) || d.state || '-'
}

// 电量/电池房间（514）：房名命中则房内电池传感器不升头行、磁贴改进度条
const BATT_ROOM_KW = /电量|电池/
// 电量/电池房内电池进度条磁贴判定（514 五返工收窄：仅这类房间生效，普通房间
// 电池维持 476 图标分支）：实体名（显示名/友好名/实体 id）含中文 电量/电池 或
// 英文 battery 即认，不要求单位 %（部分 HA 电池实体无单位）；能源/功率单位
//（kWh/W 等）恒不算电池（476 防误伤口径）
const BATT_NAME_KW = /电量|电池|battery/i
function isBattName(sn) {
  return BATT_NAME_KW.test(`${sn.label || ''} ${sn.friendly || ''} ${sn.entity}`)
}
function isRoomBatt(sn) {
  if (/^(wh|kwh|mwh|w|kw)$/i.test((sn.unit || '').trim())) return false
  return isBattName(sn)
}

// 展示分区（371 建，390 二改，397/406 三改）：配了房间按房间渲染（空房间
// 跳过，未归房/悬空引用收「未分组」），没配房间整体一节平铺且不显示分组头。
// 每节内拆四组：locks（锁域，升头行做可点按状态 chips）、env（环境电器，通栏
// 宽磁贴）、devs（普通磁贴）；状态实体中命中温度/湿度关键词的升头行做读数
//（headSens），门窗类传感器（406）也升头行（doors），其余留磁贴区。平铺/未
// 分组节无头行，锁不提升回落磁贴。
const displaySections = computed(() => {
  const split = (devs) => {
    const locks = devs.filter((d) => d.domain === 'lock')
    const rest = devs.filter((d) => d.domain !== 'lock')
    const env = rest.filter(isEnvDev)
    return { locks, env, devs: rest.filter((d) => !isEnvDev(d)) }
  }
  if (!rooms.value.length) {
    if (!devices.value.length && !sensors.value.length) return []
    const s = split(devices.value)
    const seen = new Set()
    const embeds = { ...humEmbeds(s.env, sensors.value, seen), ...acEmbeds(s.env, sensors.value, seen) }
    const egroups = energyGroups(sensors.value, seen)
    return [{ key: 'flat', roomId: '', name: '', battRoom: false, headSens: [], locks: [], doors: [], env: s.env, devs: [...s.locks, ...s.devs], sens: sensors.value.filter((x) => !seen.has(x.entity)), egroups, embeds }]
  }
  const secs = []
  for (const r of rooms.value) {
    const s = split(devices.value.filter((d) => (d.room || '') === r.id))
    const all = sensors.value.filter((x) => (x.room || '') === r.id)
    const seen = new Set()
    // 电量/电池房间（514 三返工）：头行读数整组不上，所有传感器数据落房间内磁贴
    //（按名排除总有漏网形态，用户口径=这类房间右上角不放任何读数）
    const battRoom = BATT_ROOM_KW.test(r.name || '')
    // 头行读数（406 温/湿起，481 扩 光照/电量）：temp<hum<lux<batt 稳定排序
    const headSens = battRoom
      ? []
      : all
          .filter((x) => {
            if (seen.has(x.entity)) return false
            const take = isTempSensor(x) || isHumSensor(x) || isLuxSensor(x) || isBatterySensor(x)
            if (take) seen.add(x.entity)
            return take
          })
          .sort((a, b) => CHIP_KIND_W[chipKind(a)] - CHIP_KIND_W[chipKind(b)])
    // 门窗晋升排除电池实体（514 六返工）：「xx门锁 电池电量」「Smart Door Lock Battery
    // Level」这类名字带 门/door 的电池传感器曾被提成门窗芯片、裸显电量数值，永远
    // 到不了磁贴区——名字命中电池关键词的实体一律不按门窗处理
    const doors = all.filter((x) => !seen.has(x.entity) && isDoorSensor(x) && !isBattName(x) && seen.add(x.entity))
    const embeds = { ...humEmbeds(s.env, all, seen), ...acEmbeds(s.env, all, seen) }
    const egroups = energyGroups(all, seen)
    const sens = all.filter((x) => !seen.has(x.entity))
    // 空房间也渲染（394 先建空房间再添加设备的工作流），卡片内出空态提示
    secs.push({ key: r.id, roomId: r.id, name: r.name || t('ha.unnamedRoom'), battRoom, headSens, locks: s.locks, doors, env: s.env, devs: s.devs, sens, egroups, embeds })
  }
  const loose = split(devices.value.filter((d) => !d.room || !rooms.value.some((r) => r.id === d.room)))
  const looseSens = sensors.value.filter((s) => !s.room || !rooms.value.some((r) => r.id === s.room))
  const seenL = new Set()
  const looseEmbeds = { ...humEmbeds(loose.env, looseSens, seenL), ...acEmbeds(loose.env, looseSens, seenL) }
  const looseLeft = looseSens.filter((x) => !seenL.has(x.entity))
  const looseEgroups = energyGroups(looseLeft, seenL)
  const looseSensLeft = looseLeft.filter((x) => !seenL.has(x.entity))
  if (loose.env.length || loose.devs.length || looseSensLeft.length || looseEgroups.length) {
    secs.push({ key: '_loose', roomId: '', name: t('ha.ungrouped'), battRoom: false, headSens: [], locks: [], doors: [], env: loose.env, devs: [...loose.locks, ...loose.devs], sens: looseSensLeft, egroups: looseEgroups, embeds: looseEmbeds })
  }
  return secs
})

// 加湿器附属读数收编（427）：滤芯/水位传感器不再单独占磁贴，收进同节加湿器
// 宽磁贴内部。归属：实体 slug 命中优先（sensor.<slug>_filter_life 等与加湿器
// humidifier.<slug> 同源，实体 id 含 slug 即认）；同节只有一台加湿器时退按关
// 键词认领，多台不猜（认不走的留在磁贴区）。
const FILTER_KW = /(滤芯|filter)/i
const WATER_KW = /(水位|water[_ ]?level)/i
function humEmbeds(env, all, seen) {
  const hums = env.filter((d) => d.domain === 'humidifier')
  if (!hums.length || !all.length) return {}
  const embeds = {}
  const take = (kw, slug) => {
    let hit = slug
      ? all.find((x) => !seen.has(x.entity) && kw.test(sensKW(x)) && x.entity.toLowerCase().includes(slug))
      : null
    if (!hit && hums.length === 1) {
      hit = all.find((x) => !seen.has(x.entity) && kw.test(sensKW(x)))
    }
    if (hit) seen.add(hit.entity)
    return hit
  }
  for (const h of hums) {
    const slug = (h.entity.split('.')[1] || '').toLowerCase()
    embeds[h.entity] = { filter: take(FILTER_KW, slug), water: take(WATER_KW, slug) }
  }
  return embeds
}
// 加湿器内嵌读数取值：on/off 类状态走统一 i18n 映射，数值原样
function embedVal(sn) {
  if (!sn || sn.missing || sn.unavailable) return '--'
  return stTxt(sn.state) || sn.state || '-'
}
function embedUnit(sn) {
  return sn && !sn.missing && !sn.unavailable && /^\d/.test(sn.state || '') ? (sn.unit || '') : ''
}

// 空调电量读数收编（451）：月电量/日电量/电功率传感器不再单独占磁贴，收进
// 同节空调宽磁贴控制条（读数取一位小数）。归属规则同 427 加湿器：实体 slug
// 命中优先（sensor 实体 id 含空调 climate.<slug> 同源 slug 即认），同节只有
// 一台空调退按关键词认领，多台不猜（认不走的留在磁贴区）。功率排除功率因数。
const MON_E_KW = /(月电量|月用电|本月电量|月耗电|本月耗电|monthly[_ ]?energy|energy[_ ]?month)/i
const DAY_E_KW = /(日电量|日用电|今日电量|今日用电|日耗电|今日耗电|daily[_ ]?energy|energy[_ ]?(today|day))/i
const PWR_KW = /(电功率|功率|power)/i
const PWR_EXC = /(因数|factor)/i
function acEmbeds(env, all, seen) {
  const acs = env.filter((d) => d.domain === 'climate')
  if (!acs.length || !all.length) return {}
  const embeds = {}
  const take = (kw, slug, exc) => {
    const ok = (x) => !seen.has(x.entity) && kw.test(sensKW(x)) && (!exc || !exc.test(sensKW(x)))
    let hit = slug ? all.find((x) => ok(x) && x.entity.toLowerCase().includes(slug)) : null
    if (!hit && acs.length === 1) {
      hit = all.find(ok)
    }
    if (hit) seen.add(hit.entity)
    return hit
  }
  for (const a of acs) {
    const slug = (a.entity.split('.')[1] || '').toLowerCase()
    embeds[a.entity] = { mon: take(MON_E_KW, slug), day: take(DAY_E_KW, slug), pwr: take(PWR_KW, slug, PWR_EXC) }
  }
  return embeds
}
// 电量/功率读数取值（451 用户指定一位小数）：数值态 parseFloat 保留一位小
// 数，非数值态（unavailable 等）回落 embedVal 的 '--'/中文映射
function energyVal(sn) {
  const v = parseFloat(sn && !sn.missing && !sn.unavailable ? sn.state : '')
  return Number.isFinite(v) ? v.toFixed(1) : embedVal(sn)
}

// 非空调设备的电量表合并模块（478，482 修组键）：功率/月电量/日电量不再各
// 自占磁贴，同一设备凑满 ≥2 个的收编成一个显示模块，其余原样。组键 = 实体
// id 剥掉全部电量噪声词后的设备前缀——478 只剥命中关键词，xxx_total_power
// 剥完剩 xxx_total 与 xxx_monthly_energy 的 xxx 对不上（总功率落单）；
// 剥后过短回落整 id 独组。空调不受影响：acEmbeds（451）先收编，此处捡剩的。
const E_NOISE = /总功率|monthly|daily|energy|month|today|total|electric|elec|active|instant|power|day|电量|耗电|功率|用电|今日|本月|月|日/gi
function eGroupKey(sn) {
  const id = (sn.entity || '').toLowerCase()
  const key = id.replace(E_NOISE, '').replace(/[_\-\s]+/g, '_').replace(/^_+|_+$/g, '')
  return key.length > 8 ? key : id
}
function energyKind(sn) {
  const id = (sn.entity || '').toLowerCase()
  const m = MON_E_KW.exec(id)
  if (m) return 'mon'
  const d = DAY_E_KW.exec(id)
  if (d) return 'day'
  const p = PWR_KW.exec(id)
  if (p && !PWR_EXC.test(id)) return 'pwr'
  return null
}
function energyGroups(all, seen) {
  const cands = all.filter((x) => !seen.has(x.entity) && energyKind(x))
  if (!cands.length) return []
  const groups = new Map()
  for (const x of cands) {
    const key = eGroupKey(x)
    const kind = energyKind(x)
    const g = groups.get(key) || { key, mon: null, day: null, pwr: null }
    if (!g[kind]) g[kind] = x // 同类重复（两颗功率）第二颗留磁贴区不猜
    groups.set(key, g)
  }
  const mods = []
  for (const g of groups.values()) {
    const members = [g.mon, g.day, g.pwr].filter(Boolean)
    if (members.length < 2) continue
    for (const x of members) seen.add(x.entity)
    // 模块名 = 显示名剥全部噪声词（「插座总功率」→「插座」，防残字），空则用设备前缀
    let name = ((g.mon || g.day || g.pwr).label || '').replace(E_NOISE, '').replace(/[\s_-]+$/, '').trim()
    if (!name) name = g.key.replace(/^sensor[._]/, '') || (g.mon || g.day || g.pwr).entity
    mods.push({ ...g, name })
  }
  return mods
}

// 房间头行温/湿度读数判定（390）：按显示名/实体名关键词，温度在前湿度在后
const HEAD_TEMP_KW = /(温度|气温|temperature|temp\b)/i
const HEAD_HUM_KW = /(湿度|humidity)/i
function sensKW(sn) {
  return `${sn.label || ''} ${sn.friendly || ''} ${sn.entity}`
}
function isTempSensor(sn) {
  return HEAD_TEMP_KW.test(sensKW(sn))
}
function isHumSensor(sn) {
  return HEAD_HUM_KW.test(sensKW(sn))
}
// 头行温/湿度种类判定（390 关键词法，406 改单位优先）：温湿度关键词双命中
// 的组合实体（如 temp_hum）按单位判——% 归湿度、°C/°F 归温度，都不沾则按
// 关键词（湿度且非温度 → 湿度，其余 → 温度）
function headKind(sn) {
  const unit = (sn.unit || '').trim().toLowerCase()
  if (unit === '%' || unit === 'rh' || unit.startsWith('%r')) return 'hum'
  if (unit.startsWith('°')) return 'temp'
  return isHumSensor(sn) && !isTempSensor(sn) ? 'hum' : 'temp'
}

// 头行芯片种类（481）：电池/光照优先于温湿度（组合实体单位判类见 headKind）
function chipKind(sn) {
  if (isBatterySensor(sn)) return 'batt'
  if (isLuxSensor(sn)) return 'lux'
  return headKind(sn)
}
const CHIP_KIND_W = { temp: 0, hum: 1, lux: 2, batt: 3 }
// 仅温湿度给分档色（406 口径），光照/电池走默认白
function chipColor(sn) {
  const k = chipKind(sn)
  return k === 'hum' ? humColor(sn) : k === 'temp' ? tempColor(sn) : ''
}
// 电池读数走 battVal/battPct（476 取整+% 口径），其余原样
function chipVal(sn) {
  return chipKind(sn) === 'batt' ? battVal(sn) : sensorVal(sn)
}
function chipUnit(sn) {
  if (sn.missing || sn.unavailable) return ''
  return chipKind(sn) === 'batt' ? (battPct(sn) ? '%' : '') : (sn.unit || '')
}

// 门窗传感器判定（406）：显示名/实体名命中 门/窗/door/window；排除 indoor/
// outdoor（含 door 子串的常见误报）与 门铃/风铃/bell 类
const DOOR_KW = /door|window|门|窗/i
const DOOR_EXC = /indoor|outdoor|铃|bell/i
function isDoorSensor(sn) {
  const s = `${sn.label || ''} ${sn.friendly || ''} ${sn.entity}`
  return DOOR_KW.test(s) && !DOOR_EXC.test(s)
}
function doorKind(sn) {
  return /窗|window/i.test(`${sn.label || ''} ${sn.friendly || ''} ${sn.entity}`) ? 'win' : 'door'
}
function doorOpen(sn) {
  const v = (sn.state || '').trim().toLowerCase()
  return v === 'on' || v === 'open' || v === 'opening'
}
function doorStateText(sn) {
  if (sn.missing || sn.unavailable) return '--'
  const v = (sn.state || '').trim().toLowerCase()
  if (v === 'on' || v === 'open' || v === 'opening') return t('ha.stOpen')
  if (v === 'off' || v === 'closed' || v === 'closing') return t('ha.stClosed')
  return stTxt(v) || sn.state || '-'
}

// 温湿度分档配色（406）：沿用进度条 蓝→橙→红 的「越极端越暖」口径，中间外延
// 一档舒适绿。温度：冷 <10 靛蓝 / 偏冷 10–16 浅蓝 / 舒适 16–26 绿 / 偏热
// 26–30 橙 / 热 ≥30 红；湿度：干燥 <30 橙 / 舒适 30–60 绿 / 偏湿 60–80 浅蓝 /
// 高湿 ≥80 靛蓝。非数值状态不给色走默认。
function tempColor(sn) {
  const v = parseFloat(sn.state)
  if (sn.missing || sn.unavailable || Number.isNaN(v)) return ''
  if (v >= 30) return '#ff6b6b'
  if (v >= 26) return '#ff9a54'
  if (v >= 16) return '#7ece8a'
  if (v >= 10) return '#64b5f6'
  return '#5a8df0'
}
function humColor(sn) {
  const v = parseFloat(sn.state)
  if (sn.missing || sn.unavailable || Number.isNaN(v)) return ''
  if (v >= 80) return '#5a8df0'
  if (v >= 60) return '#64b5f6'
  if (v >= 30) return '#7ece8a'
  return '#ff9a54'
}
// 锁状态配色（406）：已上锁绿（安心）、已开锁琥珀（提醒），过渡态/离线不给色
function lockColor(d) {
  const v = (d.state || '').toLowerCase()
  if (v === 'unlocked') return '#ffc95c'
  if (v === 'locked') return '#7ece8a'
  return ''
}

// 环境电器判定（388）：climate/humidifier/water_heater 域天然属于环境模块；
// 浴霸/净化器/新风机等常挂在 switch/light/fan 域，按显示名关键词识别。
const ENV_DOMAINS = new Set(['climate', 'humidifier', 'water_heater'])
const ENV_KW = /空调|浴霸|加湿|净化|新风|地暖|除湿|暖气|purifier|air_?con|heater|humid/i
function isEnvDev(d) {
  if (ENV_DOMAINS.has(d.domain)) return true
  return ENV_KW.test(`${d.label || ''} ${d.friendly || ''}`)
}

const CLIMATE_MODE_TEXT = {
  off: 'stOff', cool: 'mCool', heat: 'mHeat', auto: 'mAuto', dry: 'mDry',
  fan_only: 'mFanOnly', heat_cool: 'mHeatCool',
}
const HVAC_ACTION_TEXT = {
  cooling: 'aCooling', heating: 'aHeating', drying: 'aDrying', idle: 'aIdle', fan: 'aFan',
  preheating: 'aPreheating', defrosting: 'aDefrosting',
}
const FAN_MODE_TEXT = {
  low: 'fLow', medium: 'fMedium', mid: 'fMedium', high: 'fHigh', auto: 'fAuto', quiet: 'fQuiet', sleep: 'fSleep',
}
// 状态行（390 精简）：离线/未找到直显；开态 climate 优先实际动作（制冷中），
// 其余显模式/开关文本；风速档与预设跟在后面，重复段去重
function envStateLine(d) {
  if (d.missing) return t('ha.stMissing')
  if (d.unavailable) return t('ha.stOffline')
  const parts = []
  const v = (d.state || '').toLowerCase()
  if (!d.on) {
    parts.push(d.domain === 'climate' ? t('ha.stOff') : (stTxt(v) || d.state || t('ha.stOff')))
  } else if (d.domain === 'climate') {
    const act = HVAC_ACTION_TEXT[(d.env?.action || '').toLowerCase()]
    const mode = CLIMATE_MODE_TEXT[v]
    if (act && act !== 'aIdle') parts.push(t('ha.' + act))
    else if (mode) parts.push(t('ha.' + mode))
    else parts.push(d.state || t('ha.stOn'))
  } else {
    parts.push(stTxt(v) || d.state || t('ha.stOn'))
  }
  if (d.env?.fan) parts.push(fanModeText(d.env.fan))
  if (d.env?.preset && !parts.includes(d.env.preset)) parts.push(d.env.preset)
  const uniq = parts.filter((x, i) => x && parts.indexOf(x) === i)
  return uniq.length ? uniq.join(' · ') : t('ha.stOn')
}
function fanModeText(v) {
  const k = FAN_MODE_TEXT[(v || '').toLowerCase()]
  return k ? t('ha.' + k) : v
}
function fmtNum(v) {
  if (v == null || Number.isNaN(v)) return '--'
  const n = Math.round(v * 10) / 10
  return Number.isInteger(n) ? String(n) : n.toFixed(1)
}

// 环境电器目标值步进（388）：climate/water_heater 调目标温度（0.5°C）、
// humidifier 调目标湿度（5%），范围取上游 min/max 属性缺省兜底
async function envAdjust(d, dir) {
  if (pending.has(d.entity) || d.unavailable || d.missing) return
  const humid = d.domain === 'humidifier'
  const range = humid
    ? { min: d.env?.min_hum ?? 30, max: d.env?.max_hum ?? 90, step: 5 }
    : { min: d.env?.min_temp ?? 16, max: d.env?.max_temp ?? 32, step: 0.5 }
  const cur = humid ? d.env?.tgt_hum : d.env?.tgt_temp
  if (cur == null) return
  let nv = Math.round((cur + dir * range.step) * 2) / 2
  nv = Math.min(range.max, Math.max(range.min, nv))
  if (nv === cur) return
  pending.add(d.entity)
  try {
    const r = await api.post('/ha/call', {
      source: sourceId.value, entity_id: d.entity, action: 'set', value: nv,
    })
    if (r.device) {
      d.state = r.device.state
      d.on = r.device.on
      d.unavailable = r.device.unavailable
      if (r.device.env) d.env = r.device.env
    }
  } catch (e) {
    ElMessage.error(e.message)
    loadDevices()
  } finally {
    pending.delete(d.entity)
  }
}

// 状态实体展示值：数值原样（温度/电量），on/off 类文本走统一 i18n 映射
function sensorVal(sn) {
  if (sn.missing || sn.unavailable) return '--'
  const lower = (sn.state || '').trim().toLowerCase()
  return stTxt(lower) || sn.state || '-'
}

// 电池传感器（476）：显示名/实体名命中 电池/battery 直接认；「电量」须单位
// 为 % 才认（用电量/发电量等能源实体名字也带电量，但单位是 kWh，不误伤）
const BATT_KW = /(电池|battery)/i
function isBatterySensor(sn) {
  const s = `${sn.label || ''} ${sn.friendly || ''} ${sn.entity}`
  return BATT_KW.test(s) || (/电量/.test(s) && (sn.unit || '').trim() === '%')
}
// 电池读数（476 用户指定取整百分比）：数值态 Math.round + 强制 % 单位
//（HA 电池实体单位缺失也按百分比口径）；非数值态回落统一文案映射、不出 %
function battNum(sn) {
  if (sn.missing || sn.unavailable) return NaN
  return parseFloat(sn.state)
}
function battVal(sn) {
  if (sn.missing || sn.unavailable) return '--'
  const v = battNum(sn)
  if (Number.isFinite(v)) return String(Math.round(v))
  return stTxt((sn.state || '').trim().toLowerCase()) || sn.state || '-'
}
function battPct(sn) {
  return Number.isFinite(battNum(sn))
}
// 图标电量条宽度：数值态按 clamp 后百分比算（图标内可绘区 12.8），非数值态不出条
function battFillW(sn) {
  const v = battNum(sn)
  if (!Number.isFinite(v)) return 0
  return (Math.min(100, Math.max(0, v)) / 100) * 12.8
}

// 电量/电池房间进度条配色（514）：红 <20 / 橙 20-50 / 绿 ≥50（阈值暂定，用户要调再改），
// 渐变沿进度方向由浅入深提亮 + 磁贴数值同色
function battBarColor(pct) {
  return pct < 20 ? '#ff6b6b' : pct < 50 ? '#ff9a3d' : '#5fce7f'
}
function battBarGrad(pct) {
  if (pct < 20) return 'linear-gradient(90deg, #ffb199, #ff5f6d)'
  if (pct < 50) return 'linear-gradient(90deg, #ffd08a, #ff9a3d)'
  return 'linear-gradient(90deg, #a8e6b8, #4fc47a)'
}

// 电量/电池房间磁贴的电池图标填充宽（520）：跟进度条三档联动只取档位不按精确
// 百分比画——绿 ≥50 全满、橙 20-50 半格、红 <20 空格（不出填充，红描边即空电池）；
// 图标内可绘区 12.8 同 battFillW 口径，色值复用 battBarColor
function battIcoFillW(pct) {
  if (!Number.isFinite(pct)) return 0
  if (pct < 20) return 0
  if (pct < 50) return 6.4
  return 12.8
}

// 光照传感器（477）：单位 lx/lux 即认（HA 光照的标准单位），名字带
// 光照/照度/illuminance 也认；只加图标，数值/单位渲染原样
function isLuxSensor(sn) {
  const unit = (sn.unit || '').trim().toLowerCase()
  if (unit === 'lx' || unit === 'lux') return true
  return /(光照|照度|illuminance)/i.test(sensKW(sn))
}
function devIcon(d) {
  if (d.unavailable || d.missing) return ''
  if (iconFail[d.entity]) return ''
  const url = d.on ? d.icon_on : d.icon_off
  return url || ''
}

async function loadMe() {
  try {
    const r = await api.get('/auth/me')
    me.username = r.username || ''
    if (r.role) {
      setRole(r.role) // 旧会话登录时 localStorage 还没有 role，借机补齐
      isAdmin.value = r.role === 'admin'
    }
  } catch { /* 401 已由封装统一跳登录 */ }
}

async function loadSources() {
  const r = await api.get('/ha/sources')
  sources.value = r.sources || []
  const saved = Number(localStorage.getItem('lz.ha.source')) || 0
  const hit = sources.value.find((s) => s.id === saved)
  sourceId.value = (hit || sources.value[0] || {}).id || 0
}

async function loadDevices() {
  if (!sources.value.length) {
    devices.value = []
    sensors.value = []
    srcErr.value = ''
    return
  }
  try {
    const r = await api.get(`/ha/devices?source=${sourceId.value}`)
    devices.value = r.devices || []
    sensors.value = r.sensors || []
    rooms.value = r.rooms || []
    srcErr.value = r.ok ? '' : (r.error || t('ha.upstreamUnreachable'))
  } catch (e) {
    srcErr.value = e.message
  }
}

function pickSource(id) {
  sourceId.value = id
  localStorage.setItem('lz.ha.source', String(id))
  loadDevices()
}

async function toggle(d) {
  if (pending.has(d.entity)) return
  pending.add(d.entity)
  try {
    const r = await api.post('/ha/call', {
      source: sourceId.value, entity_id: d.entity, action: d.on ? 'turn_off' : 'turn_on',
    })
    if (r.device) {
      d.state = r.device.state
      d.on = r.device.on
      d.unavailable = r.device.unavailable
    }
  } catch (e) {
    ElMessage.error(e.message)
    loadDevices()
  } finally {
    pending.delete(d.entity)
  }
}

// ---- 磁贴点详情（510，2026-10-09）：灯/浴霸改钮触发，非按钮区开详情弹层 ----
// 灯类：light 域或名带 灯|lamp（口径同 haTileIcon 选形 479）；浴霸：名中
// 浴霸|yuba|bath heater（常挂 switch/light/fan 域，图标恒优先同理）。
const LIGHT_KW = /灯|lamp/i
function devName(d) {
  return `${d.label || ''} ${d.friendly || ''} ${d.entity}`
}
function isLightDev(d) {
  return d.domain === 'light' || LIGHT_KW.test(devName(d))
}
function isYubaDev(d) {
  return /浴霸|yuba|bath[_ ]?heater/i.test(devName(d))
}
// 可开曲线图的传感器：单位白名单同后端采样（512）——°C/°F 温度、% 湿度/电池
// 电量（2026-10-09 用户点名电量含电池，后端同步放开）、W/kW 功率、kWh/Wh/MWh 电量
function isEnergySensor(sn) {
  return /^(wh|kwh|mwh|w|kw)$/i.test((sn.unit || '').trim())
}
function isChartSens(sn) {
  const u = (sn.unit || '').trim().toLowerCase()
  if (u.startsWith('°')) return true
  if (u === '%') return true
  return isEnergySensor(sn)
}
// 传感器磁贴点击分流：门窗=开关日志、温湿/电量=曲线，其余不响应
function sensClickable(sn) {
  return isDoorSensor(sn) || isChartSens(sn)
}
function onSensClick(sn) {
  if (isDoorSensor(sn)) openDevLog(sn, 'door')
  else if (isChartSens(sn)) openSensChart(sn)
}
// 头行芯片点击（510 补）：温/湿/电池开曲线，光照（非数值态无曲线）不响应
function onHeadChipClick(e) {
  if (chipKind(e) !== 'lux') openSensChart(e)
}

// 开关日志清单弹层：from/to 已按开合语义归一 on/off（锁 on=已开锁、门窗 on=打开）
const logOpen = ref(false)
const logLoading = ref(false)
const logEntity = ref('')
const logName = ref('')
const logKind = ref('dev')
const logRows = ref([])
const LOG_ACT = {
  dev: { on: 'stOn', off: 'stOff' },
  door: { on: 'stOpen', off: 'stClosed' },
  lock: { on: 'stUnlocked', off: 'stLocked' },
}
function logActText(r) {
  const m = LOG_ACT[logKind.value] || LOG_ACT.dev
  const k = m[r.to]
  return k ? t('ha.' + k) : r.to // 只显示结果状态（511 返工：去掉「X → Y」过程式）
}
function fmtLogTime(ts) {
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
async function openDevLog(d, kind) {
  logEntity.value = d.entity
  logName.value = d.label || d.friendly || d.entity
  logKind.value = kind || (d.domain === 'lock' ? 'lock' : isDoorSensor(d) ? 'door' : 'dev')
  logRows.value = []
  logLoading.value = true
  logOpen.value = true
  try {
    const r = await api.get(`/ha/entity/events?entity=${encodeURIComponent(d.entity)}&limit=300`)
    logRows.value = r.events || []
  } catch (e) {
    ElMessage.error(t('ha.logLoadFail') + e.message)
  } finally {
    logLoading.value = false
  }
}

// 历史曲线弹层：后端按区间自动选明细/归档段（512），点序列毫秒时间戳
const chartOpen = ref(false)
const chartLoading = ref(false)
const chartName = ref('')
const chartEnts = ref([])
const chartSecs = ref(86400) // 区间一律存秒，调接口时换算成 hours（曾有秒当 hours 传、被后端钳到 90 天的口径事故）
const chartSeries = ref([])
const CHART_RANGES = [
  { value: 300, key: 'r5m' },
  { value: 600, key: 'r10m' },
  { value: 1800, key: 'r30m' },
  { value: 86400, key: 'r24h' },
  { value: 604800, key: 'r7d' },
  { value: 2592000, key: 'r30d' },
  { value: 7776000, key: 'r90d' },
]
const chartHasData = computed(() => chartSeries.value.some((s) => s.points.length))
async function loadChart() {
  if (!chartEnts.value.length) return
  chartLoading.value = true
  try {
    const q = chartEnts.value.map((x) => encodeURIComponent(x.entity)).join(',')
    const r = await api.get(`/ha/entity/history?entities=${q}&hours=${chartSecs.value / 3600}`)
    const names = new Map(chartEnts.value.map((x) => [x.entity, x.name]))
    chartSeries.value = (r.series || []).map((s) => ({
      name: names.get(s.entity) || s.entity,
      points: (s.points || []).map((p) => [p[0] * 1000, p[1]]),
    }))
  } catch (e) {
    ElMessage.error(t('ha.chartLoadFail') + e.message)
  } finally {
    chartLoading.value = false
  }
}
function openChartFor(ents, title) {
  chartEnts.value = ents
  chartName.value = title
  chartSecs.value = 86400
  chartSeries.value = []
  chartOpen.value = true
  loadChart()
}
function openSensChart(sn) {
  const name = sn.label || sn.friendly || sn.entity
  openChartFor([{ entity: sn.entity, name }], name)
}
function openEmbedChart(sn, dev, label) {
  if (!sn) return
  const name = `${dev.label || dev.entity} ${label}`
  openChartFor([{ entity: sn.entity, name }], name)
}
function openEgroupChart(g) {
  const ents = [
    g.mon && { entity: g.mon.entity, name: `${g.name} ${t('ha.eMon')}` },
    g.day && { entity: g.day.entity, name: `${g.name} ${t('ha.eDay')}` },
    g.pwr && { entity: g.pwr.entity, name: `${g.name} ${t('ha.ePwr')}` },
  ].filter(Boolean)
  openChartFor(ents, g.name)
}

// ---- 拖拽排序（425 房间 / 426 房间内磁贴，仅管理员）----
// 口径照抄 PageView（2026-10-03 硬性口径）：每个投放目标 cancel dragenter 与
// dragover（Safari 只 cancel dragover 不触发 drop），dragover 里显式
// dropEffect='move'；磁贴 handler .stop 防止冒泡误触房间节投放。
// 落点语义同 utils/dragReorder.js：A 拖到 B 上 = A 落到 B 的位置。
function dragHasType(e, type) {
  const t = e.dataTransfer?.types
  if (!t) return false
  return t.includes ? t.includes(type) : t.contains(type) // 老 Safari types 是 DOMStringList 无 includes
}

// 配置顺序落库（房间/设备/状态实体同一条路：persistPayload 保数组原序）
async function persistOrder() {
  try {
    await api.put('/ha/devices-config', persistPayload())
  } catch (e) {
    ElMessage.error(e.message)
    loadDevices()
  }
}

const roomDrag = reactive({ id: '', over: '' })
function roomStart(sec, e) {
  if (!sec.roomId) return // 未分组/平铺节不参与
  roomDrag.id = sec.roomId
  e.dataTransfer?.setData('application/x-ha-room', sec.roomId)
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function roomEnd() {
  roomDrag.id = ''
  roomDrag.over = ''
}
function roomOver(sec, e) {
  if (!roomDrag.id || sec.key === 'flat' || !dragHasType(e, 'application/x-ha-room')) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  roomDrag.over = sec.key
}
function roomDrop(sec) {
  const moved = roomDrag.id
  roomEnd()
  if (!moved || sec.key === 'flat') return
  const from = rooms.value.findIndex((r) => r.id === moved)
  if (from < 0) return
  const to = rooms.value.findIndex((r) => r.id === sec.roomId)
  if (to === from) return
  const arr = [...rooms.value]
  const [it] = arr.splice(from, 1)
  if (to < 0) arr.push(it) // 拖到「未分组」节 = 移到房间清单末尾
  else arr.splice(to, 0, it)
  rooms.value = arr
  persistOrder()
}

// 房间内磁贴：环境电器/可控设备磁贴在 devices 内重排，状态磁贴在 sensors 内
// 重排；跨房间移动走管理弹窗（拖拽只重排不换房），两类磁贴互不混排
const dragDev = reactive({ id: '', over: '' })
function tileStart(d, e) {
  dragDev.id = d.entity
  e.dataTransfer?.setData('application/x-ha-tile', d.entity)
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function tileEnd() {
  dragDev.id = ''
  dragDev.over = ''
}
function tileOver(d, e) {
  if (!dragDev.id || dragDev.id === d.entity || !dragHasType(e, 'application/x-ha-tile')) return
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  dragDev.over = d.entity
}
function tileDrop(d) {
  const moved = dragDev.id
  tileEnd()
  if (!moved || moved === d.entity) return
  const isDev = devices.value.some((x) => x.entity === moved)
  const cur = isDev ? devices.value : sensors.value
  if (!cur.some((x) => x.entity === d.entity)) return // 设备磁贴与状态磁贴互不混排
  const from = cur.findIndex((x) => x.entity === moved)
  const to = cur.findIndex((x) => x.entity === d.entity)
  if (from < 0 || to < 0 || from === to) return
  const arr = [...cur]
  const [it] = arr.splice(from, 1)
  arr.splice(to, 0, it)
  if (isDev) devices.value = arr
  else sensors.value = arr
  persistOrder()
}

// ---- 页面级房间/设备编辑（394）：新建空房间 + 房间卡片右上角添加设备，动作即时保存 ----

// 当前端配置 → devices-config 载荷（页面行剥掉运行时字段；label 与实体同值视为未自定义存空）
function persistPayload() {
  return {
    source: sourceId.value,
    rooms: rooms.value.map((r) => ({ id: r.id, name: (r.name || '').trim() })).filter((r) => r.name),
    devices: devices.value.map((d) => ({
      entity: d.entity, label: d.label === d.entity ? '' : (d.label || ''), room: d.room || '',
      icon_on: d.icon_on || '', icon_off: d.icon_off || '',
    })),
    sensors: sensors.value.map((s) => ({
      entity: s.entity, label: s.label === s.entity ? '' : (s.label || ''), room: s.room || '',
    })),
  }
}

async function addRoomInline() {
  if (rooms.value.length >= 12) {
    ElMessage.warning(t('ha.maxRooms', { n: 12 }))
    return
  }
  let name = ''
  try {
    const r = await ElMessageBox.prompt(t('ha.roomNamePrompt'), t('ha.newRoomTitle'), {
      confirmButtonText: t('ha.create'), cancelButtonText: t('ha.cancel'), inputPattern: /\S/, inputErrorMessage: t('ha.inputRoomName'),
    })
    name = (r.value || '').trim().slice(0, 20)
  } catch { return }
  if (!name) return
  if (rooms.value.some((r) => (r.name || '') === name)) {
    ElMessage.warning(t('ha.roomNameDup'))
    return
  }
  const room = { id: `r_${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`, name }
  rooms.value.push(room)
  try {
    await api.put('/ha/devices-config', persistPayload())
    ElMessage.success(t('ha.roomCreated', { name }))
  } catch (e) {
    rooms.value.pop()
    ElMessage.error(e.message)
  }
}

// 目录选择框（394）：按域分组目录（复用管理弹窗目录样式），点选即入该房
const pkOpen = ref(false)
const pkRoomId = ref('')
const pkFilter = ref('')
const pkCatalog = ref([])
const pkRoomName = computed(() => {
  if (!pkRoomId.value) return t('ha.ungrouped')
  return rooms.value.find((r) => r.id === pkRoomId.value)?.name || t('ha.unnamedRoom')
})
const pkView = computed(() => {
  const kw = pkFilter.value.trim().toLowerCase()
  if (!kw) return pkCatalog.value
  return pkCatalog.value
    .map((g) => ({
      domain: g.domain,
      entities: g.entities.filter((e) =>
        e.entity.toLowerCase().includes(kw) || (e.label || '').toLowerCase().includes(kw)),
    }))
    .filter((g) => g.entities.length)
})
function pkHas(entity) {
  return devices.value.some((d) => d.entity === entity) || sensors.value.some((s) => s.entity === entity)
}
// 目录快照态 → 行初始 on（后端口径的子集，5s 轮询即校正）
function onOf(domain, state) {
  const v = (state || '').toLowerCase()
  if (domain === 'cover') return v === 'open' || v === 'opening'
  if (domain === 'lock') return v === 'unlocked' || v === 'unlocking'
  if (domain === 'vacuum') return v === 'cleaning' || v === 'on' || v === 'returning'
  if (domain === 'climate') return v !== 'off'
  if (domain === 'media_player') return v !== 'off' && v !== 'idle' && v !== 'standby'
  return v === 'on'
}
async function openPicker(roomId) {
  pkRoomId.value = roomId
  pkFilter.value = ''
  pkOpen.value = true
  try {
    const r = await api.get(`/ha/catalog?source=${sourceId.value}`)
    pkCatalog.value = r.catalog || []
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function pkAdd(e) {
  if (pkHas(e.entity)) return
  const domain = e.entity.split('.')[0] || ''
  const isDev = CONTROL_DOMAINS.has(domain)
  const st = (e.state || '').toLowerCase()
  const dead = st === '' || st === 'unavailable' || st === 'unknown'
  const item = isDev
    ? { entity: e.entity, label: e.label || '', room: pkRoomId.value, icon_on: '', icon_off: '',
        domain, state: e.state || '', friendly: e.label || '', on: onOf(domain, e.state), unavailable: dead, missing: false }
    : { entity: e.entity, label: e.label || '', room: pkRoomId.value, state: e.state || '', unit: '',
        friendly: e.label || '', unavailable: dead, missing: false }
  ;(isDev ? devices : sensors).value.push(item) // 乐观插入，保存失败回滚
  try {
    await api.put('/ha/devices-config', persistPayload())
    ElMessage.success(t('ha.addedTo', { name: e.label || e.entity, room: pkRoomName.value }))
  } catch (err) {
    const arr = isDev ? devices : sensors
    const i = arr.value.findIndex((x) => x.entity === e.entity)
    if (i >= 0) arr.value.splice(i, 1)
    ElMessage.error(err.message)
  }
}

// 页面可见时 5s 轮询（面板常驻口径）；切后台停，回前台立即补一轮
let timer = null
function startPoll() {
  stopPoll()
  timer = setInterval(() => {
    if (!document.hidden) loadDevices()
  }, 5000)
}
function stopPoll() {
  if (timer) clearInterval(timer)
  timer = null
}
function onVisible() {
  if (!document.hidden) loadDevices()
}

// ---- 管理设备（仅管理员） ----
const mgOpen = ref(false)
// 连接配置（475 总配置）：url/token/insecure，token 不回显只标 has_token
const mgConn = reactive({ url: '', token: '', insecure: false, has_token: false })
const mgConnSaving = ref(false)
const mgConnTesting = ref(false)

async function loadMaster() {
  try {
    const r = await api.get('/ha/master')
    mgConn.url = r.url || ''
    mgConn.insecure = !!r.insecure
    mgConn.has_token = !!r.has_token
    mgConn.token = ''
  } catch { /* 弹窗内静默，保存时报错 */ }
}

async function saveMaster() {
  const url = mgConn.url.trim()
  if (url && !/^https?:\/\//.test(url)) {
    ElMessage.warning(t('ha.needHttpUrl'))
    return
  }
  mgConnSaving.value = true
  try {
    await api.put('/ha/master', { url, token: mgConn.token, insecure: mgConn.insecure })
    ElMessage.success(t('ha.connSaved'))
    loadMaster()
  } catch (e) {
    ElMessage.error(t('ha.saveFail') + e.message)
  } finally {
    mgConnSaving.value = false
  }
}

async function testMaster() {
  const url = mgConn.url.trim()
  if (!/^https?:\/\//.test(url)) {
    ElMessage.warning(t('ha.needUrlFirst'))
    return
  }
  if (!mgConn.token) {
    ElMessage.warning(mgConn.has_token ? t('ha.tokenNotEchoed') : t('ha.needToken'))
    return
  }
  mgConnTesting.value = true
  try {
    await api.post('/integrations/test', { type: 'homeassistant', url, auth: { token: mgConn.token } })
    ElMessage.success(t('ha.connOk'))
  } catch (e) {
    ElMessage.error(t('ha.connFail') + e.message)
  } finally {
    mgConnTesting.value = false
  }
}
const mgList = ref([])
const mgSensors = ref([])
const mgRooms = ref([])
const mgRoomSel = ref('') // 当前选中房间 id（''=未分组，387 房间优先工作流）
const mgExp = ref(null)   // 按实体视图展开的实体 id
const mgFilter = ref('')
const mgCatalog = ref([])
const mgSaving = ref(false)
const mgView = ref('domain') // 目录查看方式：domain=按功能（域分组）/ entity=按实体（平铺）

// 当前房间的清单（行对象仍引用 mgList/mgSensors 原项，v-model 直改源数据）
const mgListCur = computed(() => mgList.value.filter((d) => (d.room || '') === mgRoomSel.value))
const mgSensorsCur = computed(() => mgSensors.value.filter((s) => (s.room || '') === mgRoomSel.value))

function roomLabel(id) {
  return mgRooms.value.find((r) => r.id === id)?.name || t('ha.unnamedRoom')
}
function roomTag() {
  return mgRoomSel.value ? t('ha.roomTag', { name: roomLabel(mgRoomSel.value) }) : t('ha.ungrouped')
}
function mgRemoveItem(arr, entity) {
  const i = arr.findIndex((x) => x.entity === entity)
  if (i >= 0) arr.splice(i, 1)
}

// 可控域白名单（与 integrations.haControllableDomains 同步维护）：目录点选
// 按它分流——可控域进设备清单，其余域进状态实体清单。
const CONTROL_DOMAINS = new Set([
  'light', 'switch', 'fan', 'climate', 'cover', 'humidifier',
  'media_player', 'lock', 'vacuum', 'water_heater', 'input_boolean', 'siren',
])

function mgHas(entity) {
  return mgList.value.some((d) => d.entity === entity) || mgSensors.value.some((s) => s.entity === entity)
}
function mgAdd(e) {
  if (mgHas(e.entity)) return
  const domain = e.entity.split('.')[0] || ''
  // 加入当前选中房间（387 房间优先工作流）
  if (CONTROL_DOMAINS.has(domain)) {
    mgList.value.push({ entity: e.entity, label: e.label || '', room: mgRoomSel.value, icon_on: '', icon_off: '' })
  } else {
    mgSensors.value.push({ entity: e.entity, label: e.label || '', room: mgRoomSel.value })
  }
}
const mgCatalogView = computed(() => {
  const kw = mgFilter.value.trim().toLowerCase()
  if (!kw) return mgCatalog.value
  return mgCatalog.value
    .map((g) => ({
      domain: g.domain,
      entities: g.entities.filter((e) =>
        e.entity.toLowerCase().includes(kw) || (e.label || '').toLowerCase().includes(kw)),
    }))
    .filter((g) => g.entities.length)
})

// 按实体视图（370 建，387 改可展开看功能）：全实体平铺 entity_id 字母序，随搜索框过滤
const mgEntityView = computed(() => {
  const all = []
  for (const g of mgCatalog.value) {
    for (const e of g.entities) all.push(e)
  }
  all.sort((a, b) => (a.entity < b.entity ? -1 : a.entity > b.entity ? 1 : 0))
  const kw = mgFilter.value.trim().toLowerCase()
  if (!kw) return all
  return all.filter((e) =>
    e.entity.toLowerCase().includes(kw) || (e.label || '').toLowerCase().includes(kw))
})

// 实体功能按域推导（387）：与本系统对 HA 的实际控制能力一致——可控域=开/关
//（cover/lock/vacuum 用域内真服务），其余域=只读展示
const ENT_FUNCS = {
  light: ['stOn', 'stOff'], switch: ['stOn', 'stOff'], fan: ['stOn', 'stOff'],
  humidifier: ['stOn', 'stOff'], input_boolean: ['stOn', 'stOff'], siren: ['stOn', 'stOff'],
  water_heater: ['stOn', 'stOff'], media_player: ['stOn', 'stOff'], climate: ['stOn', 'stOff'],
  cover: ['stOpen', 'stClosed'], lock: ['lock', 'unlock'], vacuum: ['vStart', 'vStop'],
}
function entFunctions(domain) {
  return (ENT_FUNCS[domain] || ['roShow', 'roFollow']).map((k) => t('ha.' + k))
}

async function openManage() {
  mgList.value = devices.value.map((d) => ({
    entity: d.entity, label: d.label === d.entity ? '' : (d.label || ''),
    room: d.room || '', icon_on: d.icon_on || '', icon_off: d.icon_off || '',
  }))
  mgSensors.value = sensors.value.map((sn) => ({
    entity: sn.entity, label: sn.label === sn.entity ? '' : (sn.label || ''), room: sn.room || '',
  }))
  mgRooms.value = rooms.value.map((r) => ({ id: r.id, name: r.name || '' }))
  mgRoomSel.value = mgRooms.value[0]?.id || '' // 房间优先：默认选中第一个房间
  mgExp.value = null
  mgFilter.value = ''
  mgOpen.value = true
  loadMaster() // 475：连接配置（总配置，独立于服务集成）
  try {
    const r = await api.get(`/ha/catalog?source=${sourceId.value}`)
    mgCatalog.value = r.catalog || []
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function saveDevices() {
  mgSaving.value = true
  try {
    // 未命名的房间不提交，其引用一并清空（避免后端校验失败）
    const kept = mgRooms.value.map((r) => ({ id: r.id, name: (r.name || '').trim() })).filter((r) => r.name)
    const keptIds = new Set(kept.map((r) => r.id))
    const cleanRoom = (rid) => (keptIds.has(rid) ? rid : '')
    await api.put('/ha/devices-config', {
      source: sourceId.value,
      rooms: kept,
      devices: mgList.value.map((d) => ({ ...d, room: cleanRoom(d.room || '') })),
      sensors: mgSensors.value.map((s) => ({ ...s, room: cleanRoom(s.room || '') })),
    })
    mgOpen.value = false
    ElMessage.success(t('ha.saved'))
    loadDevices()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    mgSaving.value = false
  }
}

// ---- 房间管理（371 建，387 改选中式：chips 选中 → 操作行） ----
async function mgAddRoom() {
  if (mgRooms.value.length >= 12) {
    ElMessage.warning(t('ha.maxRooms', { n: 12 }))
    return
  }
  let name = ''
  try {
    const r = await ElMessageBox.prompt(t('ha.roomNamePrompt'), t('ha.addRoomTitle'), {
      confirmButtonText: t('ha.add'), cancelButtonText: t('ha.cancel'), inputPattern: /\S/, inputErrorMessage: t('ha.inputRoomName'),
    })
    name = (r.value || '').trim().slice(0, 20)
  } catch { return }
  if (!name) return
  if (mgRooms.value.some((r) => (r.name || '') === name)) {
    ElMessage.warning(t('ha.roomNameDup'))
    return
  }
  const room = { id: `r_${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`, name }
  mgRooms.value.push(room)
  mgRoomSel.value = room.id // 添加完即选中，接着向该房间添加实体
}
async function mgRenameRoom() {
  const room = mgRooms.value.find((r) => r.id === mgRoomSel.value)
  if (!room) return
  try {
    const r = await ElMessageBox.prompt(t('ha.renamePrompt'), t('ha.renameTitle'), {
      confirmButtonText: t('ha.save'), cancelButtonText: t('ha.cancel'), inputValue: room.name || '',
      inputPattern: /\S/, inputErrorMessage: t('ha.inputRoomName'),
    })
    const name = (r.value || '').trim().slice(0, 20)
    if (mgRooms.value.some((x) => x.id !== room.id && (x.name || '') === name)) {
      ElMessage.warning(t('ha.roomNameDup'))
      return
    }
    room.name = name
  } catch { /* 取消 */ }
}
function mgMoveRoomSel(dir) {
  const i = mgRooms.value.findIndex((r) => r.id === mgRoomSel.value)
  const j = i + dir
  if (i < 0 || j < 0 || j >= mgRooms.value.length) return
  const arr = mgRooms.value
  ;[arr[i], arr[j]] = [arr[j], arr[i]]
}
function mgDelRoomSel() {
  const i = mgRooms.value.findIndex((r) => r.id === mgRoomSel.value)
  if (i < 0) return
  const rid = mgRoomSel.value
  const name = roomLabel(rid)
  const n = mgList.value.filter((d) => d.room === rid).length + mgSensors.value.filter((s) => s.room === rid).length
  mgRooms.value.splice(i, 1)
  // 删除房间：其下设备/状态实体回落未分组
  mgList.value.forEach((d) => { if (d.room === rid) d.room = '' })
  mgSensors.value.forEach((s) => { if (s.room === rid) s.room = '' })
  mgRoomSel.value = ''
  ElMessage.success(t('ha.delRoomDone', { name, n }))
}

async function logout() {
  try {
    await ElMessageBox.confirm(t('ha.logoutConfirm'), t('ha.logoutTitle'), { type: 'warning' })
  } catch { return /* 用户取消 */ }
  try {
    await api.post('/auth/logout')
  } catch { /* 网络失败也照常清本地会话 */ }
  clearSession()
  router.replace('/login')
}

// ---- 头部天气（5）：56 起不再拉 /page/weather 触发上游取数，改读采样入库数据
//（/page/weather/latest=最新 wx_samples 行存的完整 Resp），10 分钟一拉与采样节拍对齐；
// 失败保持现状（无数据=胶囊不显示，不占顶栏），hidden 时跳过省请求
const WX_SRC_NAME = { gaode: '高德', qweather: '和风', openmeteo: 'Open-Meteo', '': 'Open-Meteo' }
const wxData = ref(null)
const wxAt = ref(0) // 当前展示数据的采样落库时刻（「更新于」用）
let wxTimer = null

async function loadWx() {
  if (document.hidden) return
  try {
    const r = await api.get('/page/weather/latest')
    if (r.weather) {
      wxData.value = r.weather
      wxAt.value = r.ts * 1000 // 如实显示采样时刻（夜间静默期自然停在入夜前最后一轮）
    }
  } catch { /* 失败保持现状（天气未开 400/受限用户 403 → 胶囊不显示） */ }
}

// 胶囊与弹层视图模型（仿主页 wx computed；详情格无紫外线=任务 4 口径）
const wx = computed(() => {
  const d = wxData.value
  if (!d || !d.current) return null
  const details = []
  // 风向串可能自带「风」字（东北风），防重复拼「风风」（18 胶囊风段同用）
  const wdir = d.current.wind_dir || ''
  const wdirTxt = wdir ? (wdir.endsWith('风') ? wdir : wdir + '风') + ' ' : ''
  if (d.current.humidity != null) details.push({ label: t('ha.humidity'), value: `${d.current.humidity}%` })
  if (d.air) details.push({ label: t('ha.airQuality'), value: `${d.air.aqi}${d.air.category ? ' ' + d.air.category : ''}` })
  if (d.current.wind) {
    details.push({ label: t('ha.wind'), value: `${wdirTxt}${t('ha.windForce', { n: windLevel(Math.round(d.current.wind)) })} · ${Math.round(d.current.wind)} km/h` })
  }
  // 气压不进详情格（2026-10-26 用户指定，与主页同口径）
  if (d.current.visibility) details.push({ label: t('ha.visibility'), value: `${Math.round(d.current.visibility * 10) / 10} km` })
  if (d.current.sunrise && d.current.sunset) details.push({ label: t('ha.sun'), value: `${d.current.sunrise} / ${d.current.sunset}` })
  const daily = (d.daily || []).map((x) => ({
    date: x.date,
    name: wxDayName(x.date),
    icon: wmoIcon(x.code, true),
    code: x.code,
    max: Math.round(x.max),
    min: Math.round(x.min),
    precip: x.precip || 0,
    snow: x.snow || '',
    wind: x.wind || '',
  }))
  // 温区条：按周内最低/最高定标，单日区间过窄也保 6% 可见（同主页）
  let lo = Infinity
  let hi = -Infinity
  for (const dy of daily) {
    lo = Math.min(lo, dy.min)
    hi = Math.max(hi, dy.max)
  }
  const range = Math.max(1, hi - lo)
  for (const dy of daily) {
    dy.barLeft = ((dy.min - lo) / range) * 100
    dy.barWidth = Math.max(6, ((dy.max - dy.min) / range) * 100)
  }
  const t0 = daily[0] || null
  return {
    temp: Math.round(d.current.temp),
    city: d.city || t('ha.unknownCity'),
    text: wmoText(d.current.code),
    icon: wmoIcon(d.current.code, d.current.is_day),
    feels: Math.round(d.current.feels),
    details,
    daily,
    warnings: d.warnings || [],
    today: t0,
    windTxt: d.current.wind ? `${wdirTxt}${t('ha.windForce', { n: windLevel(Math.round(d.current.wind)) })}`.trim() : '',
    precipTxt: t0 ? (wxDayPrecip(t0) || t('ha.noPrecip')) : '',
  }
})

// 「更新于」文案：当天 HH:MM，跨天 M/D HH:MM（同主页口径）
const wxUpd = computed(() => {
  if (!wx.value || !wxAt.value) return ''
  const d = new Date(wxAt.value)
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  if (d.toDateString() === new Date().toDateString()) return hm
  return `${d.getMonth() + 1}/${d.getDate()} ${hm}`
})
const wxSrcName = computed(() => WX_SRC_NAME[wxData.value?.provider ?? ''] || '')

// 页签标题：智能家居 · PicoPanel（离开恢复；口径同 PageView）
let savedTitle = ''
onMounted(async () => {
  savedTitle = document.title
  document.title = t('ha.docTitle')
  if (!getToken()) return
  loadWx()
  wxTimer = setInterval(loadWx, 600000) // 56：读库 10 分钟一拉与采样节拍对齐
  loading.value = true
  try {
    await Promise.all([loadMe(), loadSources()])
    await loadDevices()
    startPoll()
    document.addEventListener('visibilitychange', onVisible)
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
})
onUnmounted(() => {
  stopPoll()
  clearInterval(wxTimer)
  document.title = savedTitle
  document.removeEventListener('visibilitychange', onVisible)
})
</script>

<style scoped>
/* 独立全屏页：常驻深紫玻璃（同主页口径，不随应用主题） */
.ha {
  position: relative;
  min-height: 100vh;
  color: #fff;
  overflow-x: hidden;
}
.ha {
  /* 41 批：壁纸/柔光斑/品牌强调改 --ha-* 变量驱动——缺省深紫（violet/白/暗），品牌暗色主题在文件尾整组覆盖 */
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, #2b4c9b 0%, transparent 55%),
    radial-gradient(130% 100% at 10% 92%, #b03b6f 0%, transparent 58%),
    radial-gradient(150% 120% at 55% 125%, #7a2e6e 0%, transparent 65%),
    linear-gradient(160deg, #101a3a 0%, #1b2350 45%, #3a2158 100%);
  --ha-w1: rgba(192, 74, 127, 0.5);
  --ha-w2: rgba(61, 99, 196, 0.5);
  --ha-w3: rgba(90, 58, 155, 0.5);
  --ha-brand: var(--brand, #2a78d6);
  --ha-brand-deep: #1d4f86;
  --ha-btn-deep: #1d5ea8;
}
.ha-bg {
  position: fixed;
  inset: 0;
  z-index: 0;
  background: var(--ha-wall);
}
/* 氛围色块：原 filter: blur(70px) 的大色块在色块边界被 GPU 光栅化切出硬竖线
   （499，首页/智能家居右上同报）——改纯 radial-gradient 柔光，零滤镜无伪影，
   原 opacity 0.5 烙进渐变首停；closest-side = 椭圆四边都渐隐到 0，无截断边 */
.ha-bg i {
  position: absolute;
  border-radius: 50%;
}
.ha-bg .w1 { width: 62vw; height: 44vh; left: -12vw; bottom: -14vh; background: radial-gradient(closest-side, var(--ha-w1), transparent); }
.ha-bg .w2 { width: 48vw; height: 36vh; right: -10vw; top: -10vh; background: radial-gradient(closest-side, var(--ha-w2), transparent); }
.ha-bg .w3 { width: 36vw; height: 30vh; right: 18vw; bottom: -12vh; background: radial-gradient(closest-side, var(--ha-w3), transparent); }

.ha-body {
  position: relative;
  z-index: 1;
  max-width: 1080px;
  margin: 0 auto;
  padding: 3.5vh 24px 80px;
}

.ha-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
/* 品牌改真 button（13）：div 语义换 button，重置表单外观；仅管理员可点（.link）
   回主页，受限用户维持纯展示（421 口径） */
.ha-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  border: 0;
  background: none;
  padding: 0;
  margin: 0;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: default;
}
.ha-brand.link { cursor: pointer; }
.ha-brand.link:hover { opacity: 0.85; } /* 仅整块变淡提示可点，不加下划线（17 用户指定） */
.ha-brand-ico {
  width: 42px;
  height: 42px;
  border-radius: 12px;
  background: linear-gradient(135deg, #7c5cff, #b03bd6);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 4px 14px rgba(124, 92, 255, 0.45);
}
.ha-brand-ico :deep(svg) { width: 22px; height: 22px; }
.ha-title { font-size: 20px; font-weight: 700; line-height: 1.2; }
.ha-sub { font-size: 12px; opacity: 0.65; }

.ha-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
/* 用户下拉触发钮（43）：头像+名字玻璃胶囊，同房间玻璃口径 */
.ha-user {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 5px 12px 5px 6px;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.22);
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s;
}
.ha-user:hover { background: rgba(255, 255, 255, 0.18); }
.ha-user-avatar { width: 26px; height: 26px; border-radius: 50%; object-fit: cover; flex: none; }
.ha-user-letter {
  display: inline-flex; align-items: center; justify-content: center;
  background: rgba(255, 255, 255, 0.16); color: #fff; font-size: 12px; font-weight: 600;
}
.ha-user-name { max-width: 120px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ha-user-caret { color: rgba(255, 255, 255, 0.7); }
/* 下拉内主题色板（同后台 269 色板口径；47 三行×4 同后台） */
.ha-dd-themes {
  display: grid;
  grid-template-columns: repeat(4, auto);
  justify-content: space-around;
  row-gap: 6px;
  column-gap: 2px;
  width: 100%;
}
.ha-dd-swatch {
  border: none; background: none; cursor: pointer;
  display: flex; flex-direction: column; align-items: center; gap: 3px;
  padding: 2px 4px; border-radius: 8px;
}
.ha-dd-swatch.cur { background: var(--el-fill-color); }
.ha-dd-dot { width: 22px; height: 22px; border-radius: 50%; border: 1px solid rgba(255, 255, 255, 0.25); }
.ha-dd-label { font-size: 10.5px; color: var(--el-text-color-secondary); }
/* 个人信息弹层（受限用户改密/头像） */
.prof-ava { display: flex; align-items: center; gap: 14px; margin-bottom: 14px; }
.prof-ava img, .prof-ava-letter { width: 60px; height: 60px; border-radius: 50%; object-fit: cover; }
.prof-ava-letter {
  display: inline-flex; align-items: center; justify-content: center;
  background: rgba(255, 255, 255, 0.16); color: #fff; font-size: 22px; font-weight: 600;
}
.prof-ava-ops { display: flex; flex-direction: column; gap: 8px; }
/* 自助两步验证段（45） */
.prof-totp-head { font-size: 13px; font-weight: 600; display: flex; align-items: center; gap: 8px; }
.prof-totp-on { font-size: 12px; font-weight: 400; color: var(--el-color-success); }
.prof-totp-row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-top: 8px; }
.prof-totp-tip { font-size: 12px; line-height: 1.5; color: var(--el-text-color-secondary); margin-top: 6px; }
.prof-totp-qr { display: block; width: 132px; height: 132px; margin: 8px 0 2px; padding: 4px; background: #fff; border-radius: 6px; }
.prof-totp-secret { word-break: break-all; font-family: ui-monospace, monospace; }
/* 头部天气胶囊（5）：紧跟品牌排布（margin-right:auto 吸走剩余空隙，操作钮仍靠右），
   深底玻璃口径同 .ha-btn；弹层本体走全局 pv-wx-pop 样式 */
.ha-wx {
  margin-right: auto;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 7px 14px;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.22);
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s;
}
.ha-wx:hover { background: rgba(255, 255, 255, 0.18); }
.ha-wx-ico { display: inline-flex; width: 22px; height: 22px; color: #fff; }
.ha-wx-ico svg { width: 100%; height: 100%; }
.ha-wx-temp { font-size: 15px; font-weight: 700; font-variant-numeric: tabular-nums; }
.ha-btn-main {
  background: linear-gradient(135deg, #7c5cff, #a44ae0);
  border-color: transparent;
  margin-top: 10px;
}

.ha-srcs { display: flex; gap: 8px; margin: 18px 0 0; flex-wrap: wrap; }
.ha-src {
  padding: 6px 14px;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.08);
  color: #fff;
  font-size: 13px;
  cursor: pointer;
}
.ha-src.active { background: rgba(255, 255, 255, 0.22); border-color: rgba(255, 255, 255, 0.4); }
.ha-src-off { opacity: 0.6; font-size: 12px; }

.ha-err {
  margin-top: 14px;
  padding: 10px 14px;
  border-radius: 10px;
  background: rgba(226, 82, 110, 0.18);
  border: 1px solid rgba(226, 82, 110, 0.4);
  font-size: 13px;
}

/* 房间平铺分区（397 参考 Apple 家庭）：房名大字直接落背景，不再包玻璃大卡 */
.ha-room { margin-top: 24px; }
.ha-room-head { display: flex; align-items: center; gap: 10px; margin-bottom: 12px; }
.ha-room-name { font-size: 18px; font-weight: 700; letter-spacing: 0.3px; }
.ha-head-sp { flex: 1; }
/* 房间头行状态 chips（390 建，406 集中到房名后）：温湿度读数（颜色随数值
   分档，内联 style 上色）+ 锁（可点按开关）+ 门窗开合（只读） */
.ha-head-chips {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin-left: auto; /* 手机端换行到第二行时仍靠右 */
}
.ha-head-sensor {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 13px; /* 513 二返工：12px 偏小回调（门窗/锁芯片维持 12px） */
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.ha-head-sensor svg { width: 14px; height: 14px; }
.ha-head-sensor i {
  font-style: normal;
  font-size: 11px;
  font-weight: 500;
  opacity: 0.7;
  margin-left: 1px;
}
.ha-head-lock,
.ha-head-door {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.ha-head-lock {
  border: none;
  background: transparent;
  color: #fff;
  padding: 0;
  cursor: pointer;
}
.ha-head-lock:disabled { cursor: default; }
.ha-head-lock svg { width: 13px; height: 13px; }
.ha-head-door { color: rgba(255, 255, 255, 0.62); }
.ha-head-door.open { color: #ffc95c; }
.ha-head-door svg { width: 13px; height: 13px; }
.ha-head-lock.dead, .ha-head-door.dead { opacity: 0.45; }
.ha-head-sensor.dead { opacity: 0.45; }
/* 行内等待旋钮：覆盖 ha-dev-spin 的绝对定位基准 */
.ha-head-spin {
  position: static;
  width: 12px;
  height: 12px;
}
/* 房间头行添加设备钮（394，仅管理员）+ 空房间提示 */
.ha-room-addbtn {
  flex: none;
  width: 24px;
  height: 24px;
  margin-left: 10px;
  border-radius: 50%;
  border: 1px solid rgba(255, 255, 255, 0.28);
  background: transparent;
  color: rgba(255, 255, 255, 0.85);
  font-size: 15px;
  line-height: 1;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.ha-room-addbtn:hover { border-color: #ffc95c; color: #ffc95c; }
.ha-room-empty { padding: 6px 2px 2px; font-size: 12.5px; opacity: 0.55; }
/* 页面级新建房间（394）：房间清单末尾虚线钮 */
.ha-new-room {
  width: 100%;
  margin-top: 12px;
  padding: 11px;
  border-radius: 14px;
  border: 1px dashed rgba(255, 255, 255, 0.28);
  background: transparent;
  color: rgba(255, 255, 255, 0.75);
  font-size: 13px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}
.ha-new-room:hover { border-color: rgba(255, 255, 255, 0.55); color: #fff; background: rgba(255, 255, 255, 0.05); }

/* 拖拽排序（425 房间 / 426 磁贴）：可拖节 grab 光标，拖动中半透明、
   悬停目标描虚线（房间白、磁贴琥珀），磁贴 outline 圆角跟卡片 */
.ha-room[draggable="true"] { cursor: grab; }
.ha-room.dragging, .ha-tile.dragging, .ha-sensor.dragging { opacity: 0.45; }
.ha-room.over { outline: 2px dashed rgba(255, 255, 255, 0.55); outline-offset: 6px; border-radius: 6px; }
.ha-tile.over, .ha-sensor.over { outline: 2px dashed rgba(255, 201, 92, 0.75); outline-offset: 2px; }
.ha-sensor[draggable="true"] { cursor: grab; }

/* 家庭式磁贴（397）：双列圆角卡、左圆形图标开态着色 + 右名称/状态两行，
   无内嵌开关、整卡点按；环境电器为通栏宽磁贴（占满一行保留行内控制） */
.ha-tile-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(232px, 1fr));
  gap: 10px;
}
.ha-tile {
  position: relative;
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  padding: 13px 14px;
  border-radius: 18px;
  border: 1px solid rgba(255, 255, 255, 0.09);
  background: rgba(255, 255, 255, 0.06);
}
.ha-tile-ico {
  flex: none;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #9aa3c7;
  background: rgba(255, 255, 255, 0.1);
  overflow: hidden;
  transition: background 0.2s, color 0.2s, box-shadow 0.2s;
}
.ha-tile-ico svg { width: 20px; height: 20px; }
.ha-tile-ico img { width: 24px; height: 24px; object-fit: contain; }
.ha-tile-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.ha-tile-name {
  font-size: 14px;
  font-weight: 650;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ha-tile-state {
  font-size: 12px;
  opacity: 0.62;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
/* 开态（Apple 式只给图标圆上色）：svg 图标实底琥珀圆发光，卡体微琥珀、
   状态字琥珀；自定义 img 图标保留透明底色罩避免与图片花色打架 */
.ha-ctl.on, .ha-env.on {
  border-color: rgba(255, 200, 92, 0.26);
  background: linear-gradient(135deg, rgba(255, 200, 92, 0.1), rgba(255, 255, 255, 0.05));
}
.ha-ctl.on .ha-tile-ico, .ha-env.on .ha-tile-ico {
  color: #ffc95c;
  background: rgba(255, 200, 92, 0.2);
}
.ha-ctl.on .ha-tile-ico:not(:has(img)), .ha-env.on .ha-tile-ico:not(:has(img)) {
  color: #fff;
  background: #f5b84f;
  box-shadow: 0 2px 14px rgba(255, 200, 92, 0.35);
}
.ha-tile-state.on { color: #ffc95c; opacity: 0.95; }
.ha-env.on .ha-tile-state { color: #ffc95c; opacity: 0.95; }
.ha-env { grid-column: 1 / -1; }
.ha-env.dead, .ha-ctl.dead { opacity: 0.5; }
/* 统计列：label 上 value 下，右对齐成组 */
.ha-env-stats {
  flex: none;
  display: flex;
  align-items: center;
  gap: 22px;
  margin-left: auto;
}
.ha-env-stat {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 1px;
  min-width: 44px;
}
.ha-env-stat i {
  font-style: normal;
  font-size: 10.5px;
  opacity: 0.55;
  white-space: nowrap;
}
.ha-env-stat b {
  font-size: 15px;
  font-weight: 650;
  line-height: 1.15;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.ha-env-stat em {
  font-style: normal;
  font-size: 10.5px;
  font-weight: 500;
  opacity: 0.6;
  margin-left: 1px;
}
/* 目标值列内步进：− 值 ＋ */
.ha-env-step { display: inline-flex; align-items: center; }
.ha-env-step button {
  width: 22px;
  height: 24px;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: #fff;
  font-size: 14px;
  line-height: 1;
  padding: 0;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.ha-env-step button:hover:not(:disabled) { background: rgba(255, 255, 255, 0.14); }
.ha-env-step button:disabled { opacity: 0.4; cursor: default; }
.ha-env-step span {
  min-width: 36px;
  text-align: center;
  font-size: 15px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}
/* 拨动开关（真 button）：紧凑款 40×22 */
.ha-env-sw {
  flex: none;
  width: 40px;
  height: 22px;
  border-radius: 11px;
  border: none;
  background: rgba(255, 255, 255, 0.16);
  position: relative;
  cursor: pointer;
  padding: 0;
  transition: background 0.2s;
}
.ha-env-sw:disabled { cursor: default; }
.ha-env-sw .knob {
  position: absolute;
  left: 3px;
  top: 2px;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.3);
  transition: transform 0.2s;
}
.ha-env.on .ha-env-sw,
.ha-ctl-div.on .ha-env-sw { background: #ffc95c; }
.ha-env.on .ha-env-sw .knob,
.ha-ctl-div.on .ha-env-sw .knob { transform: translateX(16px); }
.ha-env-spin { right: 62px; top: 50%; margin-top: -6px; width: 12px; height: 12px; }

/* 标准磁贴（397）：整卡点按即开关，hover 微浮；pending 旋钮落卡片右侧 */
.ha-ctl {
  color: #fff;
  cursor: pointer;
  text-align: left;
  transition: transform 0.15s, background 0.15s, border-color 0.2s;
}
.ha-ctl:hover { transform: translateY(-1px); background: rgba(255, 255, 255, 0.1); }
.ha-ctl:disabled { cursor: default; }
.ha-ctl:disabled:hover { transform: none; background: rgba(255, 255, 255, 0.06); }
.ha-ctl .ha-ctl-spin { position: absolute; right: 14px; top: 50%; margin-top: -7px; width: 14px; height: 14px; }

/* 状态实体（368 建，397 随磁贴风格微调）：只读小磁贴，值+单位前置 */
.ha-sensor-grid {
  margin-top: 10px;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(112px, 1fr));
  gap: 10px;
}
.ha-sensor {
  min-width: 0;
  padding: 11px 13px 10px;
  border-radius: 16px;
  border: 1px solid rgba(255, 255, 255, 0.09);
  background: rgba(255, 255, 255, 0.06);
  color: #fff;
}
.ha-sensor.dead { opacity: 0.5; }
.ha-sensor-val {
  font-size: 17px;
  font-weight: 700;
  line-height: 1.25;
  font-variant-numeric: tabular-nums;
  word-break: break-all;
}
.ha-sensor-unit {
  font-size: 11px;
  font-weight: 500;
  opacity: 0.75;
  margin-left: 2px;
}
.ha-sens-ico {
  width: 20px;
  height: 20px;
  vertical-align: -4px;
  margin-right: 3px;
}
.ha-sensor-name {
  margin-top: 2px;
  font-size: 11.5px;
  opacity: 0.7;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ha-egroup {
  grid-column: span 2;
}
.ha-egroup-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 5px 16px;
  margin-top: 4px;
}
.ha-egroup-stat {
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.ha-egroup-stat i {
  font-style: normal;
  font-size: 10.5px;
  opacity: 0.55;
  white-space: nowrap;
}
.ha-egroup-stat b {
  font-size: 15px;
  font-weight: 650;
  line-height: 1.15;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.ha-egroup-stat em {
  font-style: normal;
  font-size: 10.5px;
  font-weight: 500;
  opacity: 0.75;
  margin-left: 1px;
}
.ha-dev-spin {
  position: absolute;
  right: 10px;
  top: 10px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.35);
  border-top-color: #fff;
  animation: ha-spin 0.8s linear infinite;
}
@keyframes ha-spin { to { transform: rotate(360deg); } }

.ha-empty {
  margin-top: 8vh;
  text-align: center;
  font-size: 15px;
}
.ha-empty-ico { opacity: 0.4; margin-bottom: 10px; }
.ha-empty-ico :deep(svg) { width: 52px; height: 52px; }
.ha-empty-hint { font-size: 13px; opacity: 0.65; max-width: 460px; margin: 8px auto 0; }

/* ---- 管理弹窗 ---- */
.ha-mg-tip { font-size: 12.5px; opacity: 0.75; margin-bottom: 10px; }
/* 连接配置区（475 总配置） */
.ha-mg-conn { display: flex; flex-direction: column; gap: 8px; margin-bottom: 12px; }
.ha-mg-conn-opt { display: flex; align-items: center; gap: 6px; font-size: 12.5px; opacity: 0.85; cursor: pointer; }
.ha-mg-conn-btns { display: flex; gap: 8px; }
.ha-mg-sec {
  font-size: 12px;
  font-weight: 700;
  opacity: 0.8;
  margin: 2px 0 6px;
}
.ha-mg-list { display: flex; flex-direction: column; gap: 6px; margin-bottom: 12px; }
.ha-mg-row { display: flex; align-items: center; gap: 8px; }
.ha-mg-ent {
  flex: 1.1;
  min-width: 0;
  font-size: 12.5px;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ha-mg-label { flex: 1; min-width: 0; }
.ha-mg-room { width: 96px; flex: none; }
.ha-mg-ico { flex: 1; min-width: 0; }

/* 房间 chips + 选中操作行（387 房间优先工作流） */
.ha-mg-rooms {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
}
.ha-room-chip {
  padding: 3px 14px;
  border-radius: 999px;
  border: 1px solid var(--el-border-color);
  background: transparent;
  color: inherit;
  font-size: 12.5px;
  cursor: pointer;
  max-width: 160px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ha-room-chip:hover { border-color: var(--el-color-primary); color: var(--el-color-primary); }
.ha-room-chip.active {
  background: var(--el-color-primary);
  border-color: var(--el-color-primary);
  color: #fff;
}
.ha-room-add {
  padding: 3px 10px;
  border-radius: 999px;
  border: 1px dashed var(--el-border-color);
  background: transparent;
  color: var(--muted, #909399);
  font-size: 12.5px;
  cursor: pointer;
}
.ha-room-add:hover:not(:disabled) { border-color: var(--el-color-primary); color: var(--el-color-primary); }
.ha-room-add:disabled { opacity: 0.45; cursor: default; }
.ha-room-ops {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: -2px 0 8px;
  font-size: 12px;
}
.ha-room-cur { color: var(--muted, #909399); }
.ha-room-op {
  padding: 2px 10px;
  border-radius: 6px;
  border: 1px solid var(--el-border-color);
  background: transparent;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
}
.ha-room-op:hover { border-color: var(--el-color-primary); color: var(--el-color-primary); }
.ha-room-op.danger:hover { border-color: var(--el-color-danger); color: var(--el-color-danger); }
.ha-mg-del { flex: none; cursor: pointer; opacity: 0.6; }
.ha-mg-del:hover { opacity: 1; color: #e2526e; }
.ha-mg-none { font-size: 13px; opacity: 0.6; padding: 6px 0 12px; }
.ha-mg-search { width: 260px; margin-bottom: 10px; }
.ha-mg-catalog {
  max-height: 260px;
  overflow: auto;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
  padding: 8px;
}
/* 房间添加设备弹窗目录（394）：更高一档，点多选少翻页 */
.ha-pk-catalog { max-height: 340px; }
.ha-mg-group { margin-bottom: 8px; }
.ha-mg-domain {
  font-size: 12px;
  font-weight: 700;
  opacity: 0.6;
  text-transform: uppercase;
  margin: 4px 0 6px;
}
.ha-mg-ent-btn {
  display: inline-block;
  margin: 0 6px 6px 0;
  padding: 4px 10px;
  border-radius: 8px;
  border: 1px solid var(--el-border-color);
  background: transparent;
  font-size: 12.5px;
  cursor: pointer;
  max-width: 220px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ha-mg-ent-btn:hover { border-color: var(--el-color-primary); color: var(--el-color-primary); }
.ha-mg-ent-btn.added { opacity: 0.45; cursor: default; }
.ha-mg-ent-btn.added:hover { border-color: var(--el-border-color); color: inherit; }

/* 目录双 tab（370）：按功能（域分组）/按实体（平铺行） */
.ha-mg-tabs { display: flex; gap: 6px; margin: 2px 0 8px; }
.ha-mg-tab {
  padding: 3px 14px;
  border-radius: 999px;
  border: 1px solid var(--el-border-color);
  background: transparent;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
}
.ha-mg-tab:hover { border-color: var(--el-color-primary); color: var(--el-color-primary); }
.ha-mg-tab.active {
  background: var(--el-color-primary);
  border-color: var(--el-color-primary);
  color: #fff;
}
.ha-mg-ents { display: flex; flex-direction: column; gap: 2px; }
.ha-mg-ent-item { border-radius: 8px; }
.ha-mg-ent-line { display: flex; align-items: center; gap: 4px; }
.ha-mg-ent-line .ha-mg-ent-row { flex: 1; min-width: 0; }
.ha-mg-ent-exp {
  flex: none;
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--muted, #909399);
  cursor: pointer;
}
.ha-mg-ent-exp:hover { background: var(--el-fill-color-light); color: var(--el-color-primary); }
.ha-mg-ent-exp .el-icon { transition: transform 0.15s; }
.ha-mg-ent-exp .el-icon.open { transform: rotate(180deg); }
/* 展开的功能面板（387）：功能 chips + 当前状态 */
.ha-mg-funcs {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 6px 10px 8px 34px;
  font-size: 12px;
}
.ha-mg-func-cap { color: var(--muted, #909399); }
.ha-mg-func {
  padding: 1px 10px;
  border-radius: 999px;
  background: var(--el-fill-color, #f5f7fa);
  color: inherit;
}
.ha-mg-func-cur { margin-left: auto; color: var(--muted, #909399); }
.ha-mg-ent-row {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 6px 10px;
  border-radius: 8px;
  border: none;
  background: transparent;
  color: inherit;
  font-size: 12.5px;
  text-align: left;
  cursor: pointer;
}
.ha-mg-ent-row:hover { background: var(--el-fill-color-light); }
.ha-mg-ent-row.added { opacity: 0.45; cursor: default; }
.ha-mg-ent-row.added:hover { background: transparent; }
.ha-mg-ent-id {
  flex: 1.2;
  min-width: 0;
  font-family: ui-monospace, Menlo, Consolas, monospace;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ha-mg-ent-name {
  flex: 1;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ha-mg-ent-state {
  flex: none;
  max-width: 110px;
  opacity: 0.65;
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 640px) {
  .ha-body { padding: 2.5vh 12px 60px; }
  .ha-room { margin-top: 20px; }
  .ha-room-head { flex-wrap: wrap; row-gap: 3px; margin-bottom: 10px; }
  .ha-room-name { font-size: 16.5px; }
  .ha-head-chips { gap: 3px 12px; }
  /* 磁贴手机端双列对齐参考图；环境宽磁贴统计列折到第二行，缩进对齐名称列 */
  .ha-tile-grid { grid-template-columns: repeat(2, 1fr); gap: 9px; }
  .ha-tile { padding: 11px 12px; gap: 10px; border-radius: 16px; }
  .ha-tile-ico { width: 36px; height: 36px; }
  .ha-tile-ico svg { width: 18px; height: 18px; }
  .ha-tile-ico img { width: 21px; height: 21px; }
  .ha-tile-name { font-size: 13px; }
  .ha-tile-state { font-size: 11.5px; }
  /* 环境电器手机端不换行（428）：统计列保持行内（icon | 名称 | 统计 | 开关
     一行塞下），整体压缩一档——gap/步进/开关缩小，名称列让宽截断 */
  .ha-env { flex-wrap: nowrap; }
  .ha-env-stats {
    order: 0;
    width: auto;
    margin-left: auto;
    padding-left: 0;
    gap: 8px;
    /* 451：空调带电量读数后统计可达 6 个，一行塞不下时列内换行右对齐
       （塞得下的如加湿器 4 统计仍一行） */
    flex: 0 1 auto;
    min-width: 0;
    flex-wrap: wrap;
    justify-content: flex-end;
    row-gap: 3px;
  }
  .ha-env-stat { min-width: 0; }
  .ha-env-stat i { font-size: 9.5px; }
  .ha-env-stat b { font-size: 13px; }
  .ha-env-stat em { font-size: 9.5px; }
  .ha-env-step span { min-width: 22px; font-size: 13px; }
  .ha-env-step button { width: 16px; height: 20px; font-size: 13px; }
  .ha-env-sw { width: 34px; height: 20px; }
  .ha-env-sw .knob { width: 16px; height: 16px; }
  .ha-env.on .ha-env-sw .knob,
  .ha-ctl-div.on .ha-env-sw .knob { transform: translateX(14px); }
  .ha-env-spin { right: 46px; }
  .ha-sensor-grid { grid-template-columns: repeat(auto-fill, minmax(100px, 1fr)); gap: 9px; }
  .ha-sensor-val { font-size: 15px; }
}

/* ---- 磁贴点详情（510）：门窗芯片/灯图标按钮化 reset、可点区域光标 ---- */
button.ha-head-door {
  border: none;
  background: transparent;
  padding: 0;
  font-family: inherit; /* 同上：font:inherit 会压掉共享块里的 12px */
  cursor: pointer;
}
button.ha-head-door:hover { filter: brightness(1.2); }
/* 头行温/湿/电池芯片按钮化（510 补）：span 改 button 的 reset + 可点光标 */
button.ha-head-sensor {
  border: none;
  background: transparent;
  padding: 0;
  font-family: inherit; /* 只继承字体族——font:inherit 简写会连同字号一起重置，特异性又高，把 12px 压掉（513 没生效的根因） */
}
button.ha-head-sensor.clickable { cursor: pointer; }
button.ha-head-sensor.clickable:hover { filter: brightness(1.2); }
button.ha-tile-ico {
  appearance: none;
  border: none;
  padding: 0;
  font: inherit;
  cursor: pointer;
}
.ha-ctl-div:hover .ha-tile-ico:not(:disabled) { box-shadow: 0 0 0 2px rgba(255, 255, 255, 0.16); }
.ha-sensor.clickable,
.ha-egroup { cursor: pointer; }
.ha-sensor.clickable:hover,
.ha-egroup:hover { background: rgba(255, 255, 255, 0.1); }
.ha-stat-link { cursor: pointer; }
.ha-stat-link:hover b { text-decoration: underline; }

/* 开关日志清单弹层（511） */
.ha-log-list { max-height: 46vh; overflow: auto; }
.ha-log-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 7px 2px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
  font-size: 13px;
}
.ha-log-time { font-variant-numeric: tabular-nums; opacity: 0.72; white-space: nowrap; }
.ha-log-act { font-weight: 600; }
.ha-log-act.on { color: #e8963d; }
.ha-log-empty { padding: 24px 0; text-align: center; font-size: 13px; opacity: 0.6; }
.ha-log-note { float: left; font-size: 12px; opacity: 0.55; line-height: 32px; }

/* 历史曲线弹层（512） */
.ha-chart-bar { display: flex; align-items: center; gap: 12px; margin-bottom: 10px; }
.ha-chart-note { font-size: 12px; opacity: 0.55; }

/* 电量/电池房间电池磁贴（514）：进度条 + 一位小数百分比，红橙绿渐变随电量变色 */
.ha-batt-row { display: flex; align-items: center; gap: 6px; }
.ha-batt-num {
  font-size: 17px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.1;
}
.ha-batt-num i { font-style: normal; font-size: 11px; font-weight: 500; opacity: 0.75; margin-left: 1px; }
.ha-batt-bar {
  margin-top: 7px;
  height: 7px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.1);
  overflow: hidden;
}
.ha-batt-bar i {
  display: block;
  height: 100%;
  border-radius: 4px;
  box-shadow: 0 0 8px rgba(255, 255, 255, 0.22);
  transition: width 0.4s ease;
}

/* 品牌暗色主题（38 gold + 41 八套）：后台选中时 HA 换同系壁纸/柔光斑/品牌强调，
   白透玻璃件不动；紫罗兰仍是缺省外观。柔光斑比主页亮一档（沿 gold 双版本口径），
   品牌强调（图标格/主钮）走通用规则 + 各主题 --ha-brand* 变量 */
.ha.gold {
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(150, 116, 38, 0.6) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(166, 88, 42, 0.55) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(140, 104, 40, 0.55) 0%, transparent 65%),
linear-gradient(160deg, #171208 0%, #261d0e 45%, #33270f 100%);
  --ha-w1: rgba(212, 175, 55, 0.42);
  --ha-w2: rgba(232, 201, 106, 0.34);
  --ha-w3: rgba(166, 120, 48, 0.42);
  --ha-brand: #d4af37;
  --ha-brand-deep: #8a6a1e;
  --ha-btn-deep: #a8842a;
}
.ha.gold .ha-brand-ico { background: linear-gradient(135deg, var(--ha-brand), var(--ha-brand-deep)); }
.ha.gold .ha-btn-main { background: linear-gradient(135deg, var(--ha-brand), var(--ha-btn-deep)); }
.ha.teal {
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(20, 118, 92, 0.65) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(17, 104, 96, 0.65) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(13, 82, 62, 0.65) 0%, transparent 65%),
linear-gradient(160deg, #101c19 0%, #132d26 45%, #17392e 100%);
  --ha-w1: rgba(46, 196, 155, 0.5);
  --ha-w2: rgba(38, 152, 168, 0.48);
  --ha-w3: rgba(22, 116, 92, 0.52);
  --ha-brand: #2ec49b;
  --ha-brand-deep: #1c765d;
  --ha-btn-deep: #249979;
}
.ha.rose {
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(122, 32, 58, 0.65) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(134, 38, 66, 0.65) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(92, 26, 46, 0.65) 0%, transparent 65%),
linear-gradient(160deg, #21141c 0%, #2f1922 45%, #3b1e26 100%);
  --ha-w1: rgba(224, 86, 111, 0.44);
  --ha-w2: rgba(158, 52, 96, 0.44);
  --ha-w3: rgba(118, 38, 66, 0.5);
  --ha-brand: #e0566f;
  --ha-brand-deep: #863443;
  --ha-btn-deep: #af4357;
}
.ha.glacier {
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(30, 98, 150, 0.65) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(26, 86, 124, 0.6) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(19, 64, 98, 0.65) 0%, transparent 65%),
linear-gradient(160deg, #12171e 0%, #16212c 45%, #1a2b3a 100%);
  --ha-w1: rgba(86, 170, 224, 0.46);
  --ha-w2: rgba(56, 130, 180, 0.44);
  --ha-w3: rgba(36, 94, 138, 0.5);
  --ha-brand: #56aae0;
  --ha-brand-deep: #346686;
  --ha-btn-deep: #4385af;
}
.ha.ember {
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(142, 62, 24, 0.6) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(122, 47, 20, 0.55) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(92, 37, 16, 0.6) 0%, transparent 65%),
linear-gradient(160deg, #191817 0%, #1f1c1c 45%, #25201d 100%);
  --ha-w1: rgba(232, 129, 79, 0.42);
  --ha-w2: rgba(190, 84, 40, 0.4);
  --ha-w3: rgba(142, 57, 25, 0.46);
  --ha-brand: #e8814f;
  --ha-brand-deep: #8b4d2f;
  --ha-btn-deep: #b5653e;
}
.ha.moss {
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(98, 140, 44, 0.6) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(80, 118, 40, 0.55) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(58, 92, 30, 0.6) 0%, transparent 65%),
linear-gradient(160deg, #161912 0%, #1c2016 45%, #22281a 100%);
  --ha-w1: rgba(148, 195, 86, 0.44);
  --ha-w2: rgba(108, 150, 56, 0.42);
  --ha-w3: rgba(76, 112, 40, 0.48);
  --ha-brand: #94c356;
  --ha-brand-deep: #597534;
  --ha-btn-deep: #739843;
}
.ha.graphite {
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(74, 96, 118, 0.5) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(60, 78, 96, 0.47) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(44, 58, 74, 0.5) 0%, transparent 65%),
linear-gradient(160deg, #17191b 0%, #1d2023 45%, #222529 100%);
  --ha-w1: rgba(140, 165, 190, 0.32);
  --ha-w2: rgba(105, 130, 155, 0.3);
  --ha-w3: rgba(75, 95, 115, 0.34);
  --ha-brand: #9db4c8;
  --ha-brand-deep: #5e6c78;
  --ha-btn-deep: #7a8c9c;
}
.ha.neon {
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(130, 44, 118, 0.65) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(110, 36, 102, 0.6) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(82, 28, 78, 0.65) 0%, transparent 65%),
linear-gradient(160deg, #1d141d 0%, #261826 45%, #301d30 100%);
  --ha-w1: rgba(217, 91, 184, 0.44);
  --ha-w2: rgba(168, 62, 150, 0.42);
  --ha-w3: rgba(120, 44, 108, 0.48);
  --ha-brand: #d95bb8;
  --ha-brand-deep: #82376e;
  --ha-btn-deep: #a94790;
}
.ha.abyss {
  --ha-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(24, 62, 86, 0.53) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(18, 54, 64, 0.49) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(14, 42, 58, 0.55) 0%, transparent 65%),
linear-gradient(160deg, #111316 0%, #13191e 45%, #161e24 100%);
  --ha-w1: rgba(63, 143, 163, 0.38);
  --ha-w2: rgba(40, 100, 120, 0.36);
  --ha-w3: rgba(24, 66, 84, 0.42);
  --ha-brand: #3f8fa3;
  --ha-brand-deep: #265662;
  --ha-btn-deep: #31707f;
}
.ha.teal .ha-brand-ico, .ha.rose .ha-brand-ico, .ha.glacier .ha-brand-ico, .ha.ember .ha-brand-ico,
.ha.moss .ha-brand-ico, .ha.graphite .ha-brand-ico, .ha.neon .ha-brand-ico, .ha.abyss .ha-brand-ico { background: linear-gradient(135deg, var(--ha-brand), var(--ha-brand-deep)); }
.ha.teal .ha-btn-main, .ha.rose .ha-btn-main, .ha.glacier .ha-btn-main, .ha.ember .ha-btn-main,
.ha.moss .ha-btn-main, .ha.graphite .ha-btn-main, .ha.neon .ha-btn-main, .ha.abyss .ha-btn-main { background: linear-gradient(135deg, var(--ha-brand), var(--ha-btn-deep)); }
.ha.graphite .ha-btn-main { color: #131a21; }
</style>
