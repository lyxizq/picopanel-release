<template>
  <div class="pv" :class="frontCls" v-loading="loading">
    <div class="pv-bg"><i class="w1" /><i class="w2" /><i class="w3" /></div>

    <!-- 主体：首屏数据（配置/快照/服务集成）全部就绪后才渲染，整页一帧成型，
         避免各区块先后弹出造成的高度跳动与分组区位移 -->
    <div class="pv-body" v-if="ready">
      <!-- 1. 标题区：自定义标题 | 时间，下方可选搜索框（Sun-Panel 版式：居中大标题 + 时钟 + 胶囊搜索框） -->
      <header class="pv-head">
        <div class="pv-title-row">
          <span v-if="cfg.show_avatar" class="pv-me" :title="me.username">
            <img v-if="me.avatar" :src="me.avatar" alt="" @error="me.avatar = ''">
            <span v-else class="pv-me-letter" :style="{ background: iconBg(me.username || '?') }">{{ (me.username || '?').slice(0, 1).toUpperCase() }}</span>
          </span>
          <h1 class="pv-title">{{ cfg.title || $t('page.defaultTitle') }}</h1>
          <span class="pv-sep">|</span>
          <div class="pv-clock">
            <div class="pv-time">{{ clock.time }}</div>
            <div class="pv-date">{{ clock.date }}</div>
          </div>
          <!-- 天气胶囊（管理抽屉「基础设置」可开；点击看 7 日预报） -->
          <el-popover v-if="wx" placement="bottom-end" :width="368" trigger="click" popper-class="pv-wx-pop">
            <template #reference>
              <button class="pv-wx" type="button" :title="$t('page.wxTip')">
                <span class="pv-wx-ico" v-html="wx.icon" />
                <b class="pv-wx-temp">{{ wx.temp }}°</b>
                <span class="pv-wx-city">{{ wx.city }}</span>
              </button>
            </template>
            <div v-if="wxTab === 'now'" class="pv-wx-pop-body">
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
                <span class="pv-wx-hero-feels">{{ $t('page.feelsLike', { n: wx.feels }) }}</span>
              </div>
              <!-- 分钟降水摘要 + 今日极端概率 chips 同行（539 用户指定上提一行；minutely 空时 chips 独占右对齐） -->
              <div v-if="wx.minutely || wx.extremes.length" class="pv-wx-min">
                <span class="pv-wx-min-txt">{{ wx.minutely }}</span>
                <span class="pv-wx-min-ext">
                  <span v-for="c in wx.extremes" :key="c.label" class="pv-wx-idx-chip ext">
                    <i>{{ c.label }}</i>{{ c.value }}
                  </span>
                </span>
              </div>
              <div class="pv-wx-days">
                <div class="pv-wx-days-head">{{ $t('page.nextDays', { n: wx.daily.length }) }}</div>
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
              <div v-if="wx.indices.length" class="pv-wx-idx">
                <span v-for="ix in wx.indices" :key="ix.name" class="pv-wx-idx-chip" :title="ix.text || ''">
                  <i>{{ ix.name }}</i>{{ ix.level || '--' }}
                </span>
              </div>
              <div class="pv-wx-foot">
                <a class="pv-wx-hist-link" @click.prevent="openWxHist">{{ $t('page.history') }}</a>
                <div v-if="wxUpd" class="pv-wx-upd">{{ wxSrcName ? wxSrcName + ' · ' : '' }}{{ $t('page.updatedAt', { t: wxUpd }) }}</div>
              </div>
            </div>
            <!-- 历史视图（522）：弹层内切换，指标 + 区间两排胶囊，SeriesChart 画线 -->
            <div v-else class="pv-wx-pop-body">
              <div class="pv-wx-hist-head">
                <a class="pv-wx-hist-back" @click.prevent="wxTab = 'now'">‹ {{ $t('page.wxCurrent') }}</a>
                <b class="pv-wx-hist-title">{{ $t('page.wxHistTitle', { name: wxHistName }) }}</b>
              </div>
              <div class="pv-wx-seg">
                <button v-for="m in WX_METRICS" :key="m.k" type="button" :class="{ on: wxHistMetric === m.k }" @click="wxHistMetric = m.k">{{ $t(m.name) }}</button>
              </div>
              <SeriesChart v-if="wxHistPoints.length" :series="wxHistSeries" :height="'210px'" :unit="wxHistUnit" :percent="wxHistMetric === 'humidity'" />
              <div v-else class="pv-wx-hist-empty">{{ wxHistLoading ? $t('page.loading') : $t('page.wxHistEmpty') }}</div>
              <div class="pv-wx-seg">
                <button v-for="r in WX_RANGES" :key="r.h" type="button" :class="{ on: wxHistHours === r.h }" @click="setWxRange(r.h)">{{ $t(r.name) }}</button>
              </div>
            </div>
          </el-popover>
        </div>
        <div v-if="cfg.show_search" class="pv-search">
          <el-dropdown trigger="click" popper-class="pv-eng-pop" @command="setEngine">
            <button class="pv-engine" type="button" :title="$t('page.engineTitle', { name: engine.name })">
              <span class="pv-engine-ico" v-html="ENGINE_ICOS[engine.id]" />{{ engine.name }}
            </button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item v-for="e in ENGINES" :key="e.id" :command="e.id" :class="{ 'pv-eng-sel': e.id === engineId }" :divided="e.id === 'gscholar'">
                  <span class="pv-eng-ico" v-html="ENGINE_ICOS[e.id]" /><span>{{ e.name }}</span>
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
          <input
            v-model="keyword"
            class="pv-search-input"
            type="text"
            :placeholder="$t('page.searchPlaceholder', { name: engine.name })"
            @keydown.enter="doSearch"
          >
          <button class="pv-search-go" type="button" :title="$t('page.search')" @click="doSearch">
            <el-icon><Search /></el-icon>
          </button>
        </div>
      </header>

      <!-- 1.5 统计概览区（459）：消息/服务器/GitHub/邮件/待办/证书/PT 签到聚合卡片，
           卡内数据一行横排（474：图标+名称+数值+说明），卡片按内容宽度自动换行排列；
           说明行只留异常信息（473：好态后缀字去掉），数据来自 /page/stats，随快照同节拍
           轮询；空数据域整卡隐藏，全部为空整区隐藏。点击跳对应控制台页（Cmd/Ctrl 新开窗口） -->
      <section v-if="statCards.length" class="pv-sec">
        <div class="pv-stats">
          <button
            v-for="c in statCards"
            :key="c.key"
            class="pv-stat"
            :class="{ linked: !!c.to, alert: c.alert }"
            type="button"
            :title="c.title || (c.to ? $t('page.openNewWin', { name: c.name }) : c.name)"
            @click="openStatLink($event, c)"
          >
            <span class="pv-stat-ico" v-html="c.ico" />
            <span class="pv-stat-name">{{ c.name }}</span>
            <b class="pv-stat-val">{{ c.value }}</b>
            <span v-if="c.sub" class="pv-stat-sub" :class="{ 'pv-stat-bad': c.subAlert }">{{ c.sub }}</span>
          </button>
        </div>
      </section>

      <!-- 1.6 倒数日卡（个人助理批）：未到的按剩余天数升序在前、累计天数在后，最多 4 条；
           只读展示——增删改在后台日历页（用户指定主页不放管理入口）；显隐走管理抽屉 show_countdown -->
      <section v-if="cfg.show_countdown && countdowns.length" class="pv-sec">
        <div class="pv-cd">
          <div class="pv-cd-head">
            <span class="pv-cd-name">{{ $t('page.countdownCard') }}</span>
          </div>
          <div class="pv-cd-list">
            <div v-for="c in countdowns.slice(0, 4)" :key="c.id" class="pv-cd-item">
              <i class="pv-cd-dot" :style="{ background: c.color || '#909399' }" />
              <span class="pv-cd-n">{{ c.name }}</span>
              <b class="pv-cd-days" :class="{ soon: c.kind !== 'since' && c.days >= 0 && c.days <= 7 }">{{ cdDaysText(c, t) }}</b>
            </div>
          </div>
        </div>
      </section>

      <!-- 2. 监控区：主页独立配置的监控模块，玻璃卡片版式（可配置点击跳转） -->
      <section v-if="tileViews.length" class="pv-sec">
        <div class="pv-tiles">
          <div
            v-for="(t, i) in tileViews"
            :key="t.id"
            class="pv-tile"
            :class="{ linked: t.link, dragging: tileDrag.from === i, 'drag-over': tileDrag.over === i && tileDrag.from !== i }"
            :title="t.link ? $t('page.openNameNewWin', { name: t.link }) : ''"
            draggable="true"
            @dragstart="tileStart(i, $event)"
            @dragenter.prevent="tileOver(i, $event)"
            @dragover.prevent="tileOver(i, $event)"
            @drop.prevent="tileDrop(i)"
            @dragend="tileDrag.from = tileDrag.over = -1"
            @click="openTileLink($event, t)"
          >
            <div class="pv-tile-label">
              <!-- 图标留空不渲染任何图标（321：去掉首字徽标回退），加载失败 onTileIconErr 置空后同样不显示 -->
              <span v-if="t.icon" class="pv-tile-ico">
                <img :src="t.icon" alt="" @error="onTileIconErr(t)">
              </span>{{ t.label }}
            </div>
            <!-- 网络：上下行双行（上行橙在前、下行蓝在后，同总览方向色） -->
            <div v-if="t.net" class="pv-net">
              <div class="pv-net-row"><el-icon class="pv-arr up"><Top /></el-icon><span>{{ t.net.tx }}</span></div>
              <div class="pv-net-row"><el-icon class="pv-arr down"><Bottom /></el-icon><span>{{ t.net.rx }}</span></div>
            </div>
            <!-- 磁盘读写：双行中文标（写橙、读蓝） -->
            <div v-else-if="t.dio" class="pv-net">
              <div class="pv-net-row"><span class="pv-io-tag up">{{ $t('page.write') }}</span><span>{{ t.dio.w }}</span></div>
              <div class="pv-net-row"><span class="pv-io-tag down">{{ $t('page.read') }}</span><span>{{ t.dio.r }}</span></div>
            </div>
            <!-- 外网流量：当日/当月上传下载（账本口径；上传橙、下载蓝同网络方向色） -->
            <div v-else-if="t.tfc" class="pv-net">
              <div class="pv-net-row"><span class="pv-io-tag wide up">{{ $t('page.upload') }}</span><span>{{ t.tfc.up }}</span></div>
              <div class="pv-net-row"><span class="pv-io-tag wide down">{{ $t('page.download') }}</span><span>{{ t.tfc.down }}</span></div>
            </div>

            <!-- 磁盘组合：左读写右竖进度条 -->
            <div v-else-if="t.drw" class="pv-drw">
              <div class="pv-drw-io">
                <div class="pv-net-row"><span class="pv-io-tag down">{{ $t('page.read') }}</span><span>{{ t.drw.r }}</span></div>
                <div class="pv-net-row"><span class="pv-io-tag up">{{ $t('page.write') }}</span><span>{{ t.drw.w }}</span></div>
              </div>
              <div class="pv-vbar"><div class="pv-vbar-fill" :style="{ height: barW(t.drw.pct), background: barColor(t.drw.pct) }" /></div>
            </div>

            <!-- 资源占用组合：CPU/内存两条横进度条 -->
            <div v-else-if="t.cm" class="pv-cm">
              <div class="pv-cm-row">
                <span class="pv-cm-name">CPU</span>
                <div class="pv-track"><div class="pv-track-fill" :style="{ width: barW(t.cm.cpu), background: barColor(t.cm.cpu) }" /></div>
                <b class="pv-cm-pct" :class="utilClass(t.cm.cpu)">{{ fmtPercent(t.cm.cpu) }}</b>
              </div>
              <div class="pv-cm-row">
                <span class="pv-cm-name">{{ $t('page.mem') }}</span>
                <div class="pv-track"><div class="pv-track-fill" :style="{ width: barW(t.cm.mem), background: barColor(t.cm.mem) }" /></div>
                <b class="pv-cm-pct" :class="utilClass(t.cm.mem)">{{ fmtPercent(t.cm.mem) }}</b>
              </div>
            </div>

            <!-- 底行：服务器名（265/回归修复：必须位于互斥链之后，独立 v-if 不打断 v-else-if 链）；
                 diskrw（272）右侧加磁盘空间占用占比（同后台 km-bar-foot 口径） -->
            <div v-if="t.drw" class="pv-foot">
              <span :title="withRemark(t.serverName, t.remark)">{{ t.serverName }}</span>
              <b :class="t.cls">{{ t.value }}</b>
            </div>
            <div v-else-if="t.net || t.dio || t.cm || t.tfc" class="pv-foot"><span :title="withRemark(t.serverName, t.remark)">{{ t.serverName }}</span></div>
            <!-- 百分比：粗进度条 + 底行服务器与占比 -->
            <template v-else-if="t.bar != null">
              <div class="pv-bar">
                <div class="pv-track"><div class="pv-track-fill" :style="{ width: barW(t.bar), background: barColor(t.bar) }" /></div>
              </div>
              <!-- 节奏小字（智谱周额度格：剩余 N tokens 按当前节奏可撑约 D 天，口径同用量详情弹层） -->
              <div v-if="t.note" class="pv-note" :title="t.note">{{ t.note }}</div>
              <div class="pv-foot">
                <span :title="withRemark(t.serverName, t.remark)">{{ t.serverName }}</span>
                <b :class="t.cls">{{ t.value }}</b>
              </div>
            </template>
            <template v-else>
              <div class="pv-value" :class="t.cls">{{ t.value }}</div>
              <div class="pv-foot"><span :title="withRemark(t.serverName, t.remark)">{{ t.serverName }}</span></div>
            </template>
          </div>
        </div>
      </section>

      <!-- 2.5 服务区：服务集成卡片（15 类，图标+名称+四格统计，Homepage widget 同口径） -->
      <section v-if="svcCards.length" class="pv-sec">
        <div class="pv-svcs">
          <div
            v-for="card in svcCards"
            :key="card.key"
            class="pv-svc"
            :class="{ off: card.off, linked: !!card.url }"
            :title="card.url ? $t('page.openNameNewWin', { name: card.name }) : ''"
            @click="openSvcLink($event, card)"
          >
            <div class="pv-svc-head">
              <span class="pv-svc-ico" :style="{ background: card.icoBg }" v-html="card.ico" />
              <span class="pv-svc-name">{{ card.name }}</span>
              <span v-if="card.off" class="pv-svc-badge" :title="card.error || $t('page.cannotConnect')">{{ $t('page.offline') }}</span>
              <!-- Emby 播放记录入口已移入「已观看」格子（2026-10-23 用户指定） -->
              <span v-if="card.streams.length" class="pv-svc-live">
                <span
                  v-for="s in card.streams.slice(0, 2)"
                  :key="s.user + s.title"
                  class="pv-svc-live-item"
                  :title="`${s.user} · ${s.title}${s.sub ? ' · ' + s.sub : ''}（${s.pct}%）`"
                >
                  <b class="pv-svc-live-u">{{ s.user }}</b><MarqueeText :content="`${s.title}${s.sub ? ' · ' + s.sub : ''}`">{{ s.title }}<template v-if="s.sub"> · {{ s.sub }}</template></MarqueeText>
                </span>
              </span>
            </div>
            <div v-if="card.cells.length" class="pv-svc-grid">
              <div
                v-for="cell in card.cells"
                :key="cell.label"
                class="pv-svc-cell"
                :class="{ watch: cell.watch }"
                @click.stop="cell.watch && openEmbyHist(card)"
              >
                <b>{{ cell.value }}</b>
                <i>{{ cell.label }}</i>
              </div>
            </div>
            <div v-else class="pv-svc-hint">{{ card.off ? (card.error || $t('page.cannotConnect')) : $t('page.noData') }}</div>

          </div>
        </div>
      </section>

      <!-- 3. 自定义链接区：分组手风琴（横向=同时展开一组 / 竖向=上下排列默认全展开，设置可选） -->
      <section v-if="groups.length" class="pv-sec">
        <div class="pv-acc" :class="linkMode">
          <div
            v-for="(g, gi) in groups"
            :key="g.id"
            class="pv-grp"
            :class="{ open: isGrpOpen(g.id), dragging: grpDrag.from === gi, 'drag-over': grpDrag.over === gi && grpDrag.from !== gi }"
          >
            <button
              class="pv-grp-bar"
              type="button"
              draggable="true"
              @click="toggleGrp(g.id)"
              @dragstart="grpStart(gi, $event)"
              @dragenter.prevent="grpOver(gi, $event)"
              @dragover.prevent="grpOver(gi, $event)"
              @drop.prevent="grpDrop(gi)"
              @dragend="grpDrag.from = grpDrag.over = -1"
              :title="$t('page.grpDragTitle', { name: g.name })"
            >
              <span class="pv-grp-name">{{ g.name }}</span>
              <span class="pv-grp-count">{{ homeLinksOf(g.id).length }}</span>
              <el-icon class="pv-grp-arrow" :class="{ open: isGrpOpen(g.id) }"><ArrowDown /></el-icon>
            </button>
            <div
              class="pv-grp-body"
              @dragenter.prevent="linkOverInto(g.id, $event)"
              @dragover.prevent="linkOverInto(g.id, $event)"
              @drop.prevent="linkDropInto(g.id)"
            >
              <a
                v-for="l in homeLinksOf(g.id)"
                :key="l.id"
                class="pv-link"
                :class="{ dragging: linkDrag.id === l.id, 'drag-over': linkDrag.over === l.id && linkDrag.id !== l.id }"
                :href="safeUrl(l.url)"
                :title="$t('page.linkDragTitle', { name: l.url })"
                draggable="true"
                @dragstart="linkStart(l, $event)"
                @dragenter.prevent.stop="linkOver(l.id, $event)"
                @dragover.prevent.stop="linkOver(l.id, $event)"
                @drop.prevent.stop="linkDrop(l.id)"
                @dragend="linkDrag.id = null; linkDrag.over = null"
              >
                <span class="pv-ico" :style="{ background: iconBg(l.name) }">
                  <span class="pv-ico-letter">{{ l.name.slice(0, 1).toUpperCase() }}</span>
                  <!-- 不用 loading="lazy"（72）：快速滚动时图标进视口才起加载，空白→弹出即闪动；
                       favicon 体量数量有限，立即加载无成本 -->
                  <img v-if="l.icon" :src="l.icon" alt="" :class="{ fit: lowResIcons.has(l.id) }" @load="onIconLoad(l, $event)" @error="onIconErr">
                </span>
                <span class="pv-link-name">{{ l.name }}</span>
              </a>
              <span v-if="!homeLinksOf(g.id).length" class="pv-grp-empty">{{ $t('page.grpEmpty') }}</span>
            </div>
          </div>
        </div>
      </section>
    </div>

    <!-- 右上角悬浮按钮：返回控制台（带未读角标） / 设置 -->
    <div class="pv-fab">
      <div class="pv-fab-wrap">
        <button class="pv-fab-btn" type="button" :title="$t('page.goConsole')" @click="goConsole($event)"><el-icon><HomeFilled /></el-icon></button>
        <span v-if="unread" class="pv-fab-badge" :title="$t('page.unreadN', { n: unread })">{{ unread > 99 ? '99+' : unread }}</span>
      </div>
        <button v-if="cfg.show_ha" class="pv-fab-btn ha" type="button" :title="$t('page.smartHome')" @click="router.push('/ha')"><el-icon><HaMenuIcon /></el-icon></button>
      <button class="pv-fab-btn" type="button" :title="$t('page.manageHome')" @click="openAdmin"><el-icon><Setting /></el-icon></button>
    </div>

    <!-- 管理抽屉：基础设置 + 分组与链接 -->
    <el-drawer append-to-body v-model="adminOpen" :size="isMobile ? '94%' : '500px'" :title="null" class="pv-drawer">
      <template #header>
        <span class="pv-drawer-title">{{ $t('page.manageHome') }}</span>
      </template>
      <el-tabs v-model="adminTab" @tab-change="onAdminTab">
        <el-tab-pane :label="$t('page.tabBase')" name="base">
          <el-form label-position="top" class="pv-form">
            <el-form-item :label="$t('page.fTitle')">
              <el-input v-model="form.title" maxlength="60" show-word-limit :placeholder="$t('page.phTitle')" />
            </el-form-item>
            <div class="pv-form-row">
              <el-form-item :label="$t('page.fShowSearch')">
                <el-switch v-model="form.show_search" />
              </el-form-item>
              <el-form-item :label="$t('page.fShowAvatar')">
                <el-switch v-model="form.show_avatar" />
              </el-form-item>
              <el-form-item :label="$t('page.fSmartHome')">
                <el-switch v-model="form.show_ha" />
              </el-form-item>
              <el-form-item :label="$t('page.fShowStats')">
                <el-switch v-model="form.show_stats" />
              </el-form-item>
              <el-form-item :label="$t('page.fShowCountdown')">
                <el-switch v-model="form.show_countdown" />
              </el-form-item>

            </div>
            <el-form-item :label="$t('page.fRefresh')">
              <el-select v-model="form.refresh_sec" style="width: 100%">
                <el-option v-for="o in REFRESH_OPTIONS" :key="o.value" :label="$t(o.label)" :value="o.value" />
              </el-select>
            </el-form-item>
            <el-form-item :label="$t('page.fLinkMode')">
              <el-radio-group v-model="form.link_mode">
                <el-radio value="vertical">{{ $t('page.modeVertical') }}</el-radio>
                <el-radio value="horizontal">{{ $t('page.modeHorizontal') }}</el-radio>
              </el-radio-group>
            </el-form-item>
            <el-form-item :label="$t('page.fWeather')">
              <el-switch v-model="form.weather_enabled" />
            </el-form-item>
            <template v-if="form.weather_enabled">
              <el-form-item :label="$t('page.fProvider')">
                <el-select v-model="form.weather_provider" style="width: 100%">
                  <el-option :label="$t('page.provOpenMeteo')" value="" />
                  <el-option :label="$t('page.provGaode')" value="gaode" />
                  <el-option :label="$t('page.provQweather')" value="qweather" />
                </el-select>
              </el-form-item>
              <!-- 所选服务商的 Key 申请地址（免 Key 的给一句无需申请说明） -->
              <div class="pv-wx-api">
                <template v-if="form.weather_provider === 'gaode'">
                  {{ $t('page.keyApply') }}<a href="https://console.amap.com/dev/key/app" target="_blank" rel="noopener">console.amap.com/dev/key/app</a>{{ $t('page.keyAmapHint') }}
                </template>
                <template v-else-if="form.weather_provider === 'qweather'">
                  {{ $t('page.keyApply') }}<a href="https://console.qweather.com" target="_blank" rel="noopener">console.qweather.com</a>{{ $t('page.keyQweatherHint') }}
                </template>
                <template v-else>{{ $t('page.keyNone') }}</template>
              </div>
              <el-form-item v-if="form.weather_provider" :label="form.weather_provider === 'qweather' ? $t('page.keyQweather') : $t('page.keyAmap')">
                <el-input
                  v-model="form.weather_key"
                  clearable
                  :placeholder="cfg.has_weather_key ? $t('page.phKeySet') : (form.weather_provider === 'qweather' ? $t('page.phQweatherKey') : $t('page.phAmapKey'))"
                />
              </el-form-item>
              <el-form-item v-if="form.weather_provider === 'qweather'" label="API Host">
                <el-input v-model="form.weather_api_host" clearable :placeholder="$t('page.phApiHost')" />
              </el-form-item>
              <el-form-item :label="$t('page.fLocate')">
                <el-radio-group v-model="form.weather_mode">
                  <el-radio value="auto">{{ $t('page.locAuto') }}</el-radio>
                  <el-radio value="manual">{{ $t('page.locManual') }}</el-radio>
                </el-radio-group>
              </el-form-item>
              <el-form-item v-if="form.weather_mode === 'manual'" :label="$t('page.fCity')">
                <el-select
                  v-model="form.weather_pick"
                  filterable
                  remote
                  :remote-method="searchCity"
                  :loading="citySearching"
                  default-first-option
                  :placeholder="$t('page.phCity')"
                  :no-data-text="$t('page.citySearchHint')"
                  style="width: 100%"
                  @change="onCityPick"
                >
                  <el-option v-for="ct in cityOptions" :key="ct.key" :label="ct.label" :value="ct.key" />
                </el-select>
              </el-form-item>
            </template>
            <el-button type="primary" :loading="savingBase" @click="saveSettings">{{ $t('page.saveSettings') }}</el-button>
          </el-form>
          <!-- 系统名称与版本号：面板底部右侧（2026-10-03 用户指定） -->
          <div class="adm-sys">PicoPanel<span v-if="sysVer"> · {{ sysVer }}</span></div>
        </el-tab-pane>
        <el-tab-pane :label="$t('page.tabMetrics')" name="metrics">
          <div class="adm-metrics">
            <div class="adm-main-head">
              <span class="adm-main-title">{{ $t('page.metricsHead', { n: tileCfg.length }) }}</span>
              <el-button size="small" type="primary" :icon="Plus" :disabled="tileCfg.length >= 16" @click="openMetricDlg()">{{ $t('page.addMetric') }}</el-button>
            </div>
            <div v-for="(m, i) in metricRows" :key="m.id" class="adm-link" :class="{ 'adm-row-off': m.hidden }">
              <span class="adm-ico" :style="{ background: iconBg(m.label || '?') }">
                <span>{{ (m.label || '?').slice(0, 1).toUpperCase() }}</span>
                <img v-if="m.icon" :src="m.icon" alt="" @error="onMetricRowIconErr(m)">
              </span>
              <span class="adm-metric-text">
                <b>{{ m.label }}</b>
                <i>{{ m.server_name }} · {{ $t(m.metricLabel) }}{{ m.param ? ' · ' + m.param : '' }}{{ m.link ? ' · ↗ ' + m.link : '' }}</i>
              </span>
              <span class="adm-ops">
                <el-icon :title="m.hidden ? $t('page.show') : $t('page.hide')" @click="toggleMetric(m)"><component :is="m.hidden ? View : Hide" /></el-icon>
                <el-icon :class="{ off: i === 0 }" :title="$t('page.moveUp')" @click="moveMetric(i, -1)"><ArrowUp /></el-icon>
                <el-icon :class="{ off: i === metricRows.length - 1 }" :title="$t('page.moveDown')" @click="moveMetric(i, 1)"><ArrowDown /></el-icon>
                <el-icon :title="$t('common.edit')" @click="openMetricDlg(m)"><Edit /></el-icon>
                <el-icon class="danger" :title="$t('common.delete')" @click="removeMetric(m)"><Delete /></el-icon>
              </span>
            </div>
            <div v-if="!metricRows.length" class="adm-empty">{{ $t('page.metricsEmpty') }}</div>
          </div>
        </el-tab-pane>
        <el-tab-pane :label="$t('page.tabServices')" name="services">
          <div class="adm-services">
            <div class="adm-main-head">
              <span class="adm-main-title">{{ $t('page.svcHead', { n: intRows.length }) }}</span>
              <el-button size="small" type="primary" :icon="Plus" :disabled="intRows.length >= 8" @click="openSvcDlg()">{{ $t('page.addService') }}</el-button>
            </div>
            <div v-for="(it, i) in intRows" :key="it.id" class="adm-link" :class="{ 'adm-row-off': it.hidden }">
              <span class="adm-ico" :style="{ background: svcTypeMeta(it.type).bg }" v-html="svcTypeMeta(it.type).ico" />
              <span class="adm-link-text">
                <b>{{ it.name }}<i class="adm-svc-flag">{{ $t(SVC_TYPE_LABELS[it.type] || it.type) }}{{ it.enabled ? '' : $t('page.disabledSuffix') }}</i></b>
                <i :title="it.url">{{ it.url }}</i>
              </span>
              <span class="adm-svc-state" :class="{ good: !it.last_error && it.last_ok_at, bad: !!it.last_error }"
                :title="it.last_error || (it.last_ok_at ? $t('page.lastOk', { t: new Date(it.last_ok_at * 1000).toLocaleString(sysLocale()) }) : $t('page.notConnectedYet'))">
                {{ it.last_error ? $t('page.stateError') : it.last_ok_at ? $t('page.stateOnline') : $t('page.stateUnconnected') }}
              </span>
              <span class="adm-ops">
                <el-icon :title="it.hidden ? $t('page.show') : $t('page.hide')" @click="toggleInt(it)"><component :is="it.hidden ? View : Hide" /></el-icon>
                <el-icon :class="{ off: i === 0 }" :title="$t('page.moveUp')" @click="moveInt(i, -1)"><ArrowUp /></el-icon>
                <el-icon :class="{ off: i === intRows.length - 1 }" :title="$t('page.moveDown')" @click="moveInt(i, 1)"><ArrowDown /></el-icon>
                <el-icon :title="$t('page.testConn')" @click="testSaved(it)"><Connection /></el-icon>
                <el-icon :title="$t('common.edit')" @click="openSvcDlg(it)"><Edit /></el-icon>
                <el-icon class="danger" :title="$t('common.delete')" @click="delInt(it)"><Delete /></el-icon>
              </span>
            </div>
            <div v-if="!intRows.length" class="adm-empty">{{ $t('page.svcEmpty') }}</div>
          </div>
        </el-tab-pane>
        <el-tab-pane :label="$t('page.tabLinks')" name="links">
          <div class="adm">
            <div class="adm-side">
              <div class="adm-side-head">{{ $t('page.groups') }}</div>
              <div
                v-for="(g, i) in groups"
                :key="g.id"
                class="adm-grp"
                :class="{ sel: g.id === selGrp }"
                @click="selGrp = g.id"
              >
                <span class="adm-grp-name" :title="g.name">{{ g.name }}</span>
                <span class="adm-ops" @click.stop>
                  <el-icon :class="{ off: i === 0 }" :title="$t('page.moveLeft')" @click="moveGrp(i, -1)"><Back /></el-icon>
                  <el-icon :class="{ off: i === groups.length - 1 }" :title="$t('page.moveRight')" @click="moveGrp(i, 1)"><Right /></el-icon>
                  <el-icon :title="$t('page.rename')" @click="renameGrp(g)"><Edit /></el-icon>
                  <el-icon class="danger" :title="$t('page.delGroup')" @click="delGrp(g)"><Delete /></el-icon>
                </span>
              </div>
              <el-button class="adm-add" text type="primary" :icon="Plus" @click="addGroup">{{ $t('page.addGroup') }}</el-button>
            </div>
            <div class="adm-main">
              <template v-if="selGrpName">
                <div class="adm-main-head">
                  <span class="adm-main-title">{{ selGrpName }}</span>
                  <el-button size="small" type="primary" :icon="Plus" @click="openLinkDlg()">{{ $t('page.addLink') }}</el-button>
                </div>
                <div v-for="(l, i) in linksOf(selGrp)" :key="l.id" class="adm-link" :class="{ 'adm-row-off': l.hidden }">
                  <span class="adm-ico" :style="{ background: iconBg(l.name) }">
                    <span>{{ l.name.slice(0, 1).toUpperCase() }}</span>
                    <img v-if="l.icon" :src="l.icon" alt="" @error="onIconErr">
                  </span>
                  <span class="adm-link-text">
                    <b>{{ l.name }}</b>
                    <i :title="l.url">{{ l.url }}</i>
                  </span>
                  <span class="adm-ops">
                    <el-icon :title="l.hidden ? $t('page.show') : $t('page.hide')" @click="toggleLink(l)"><component :is="l.hidden ? View : Hide" /></el-icon>
                    <el-icon :class="{ off: i === 0 }" :title="$t('page.moveUp')" @click="moveLink(l, i, -1)"><ArrowUp /></el-icon>
                    <el-icon :class="{ off: i === linksOf(selGrp).length - 1 }" :title="$t('page.moveDown')" @click="moveLink(l, i, 1)"><ArrowDown /></el-icon>
                    <el-icon :title="$t('common.edit')" @click="openLinkDlg(l)"><Edit /></el-icon>
                    <el-icon class="danger" :title="$t('common.delete')" @click="delLink(l)"><Delete /></el-icon>
                  </span>
                </div>
                <div v-if="!linksOf(selGrp).length" class="adm-empty">{{ $t('page.grpNoLinks') }}</div>
              </template>
              <div v-else class="adm-empty">{{ $t('page.pickGroup') }}</div>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-drawer>

    <!-- 链接新增/编辑弹窗 -->
    <el-dialog v-model="linkDlg" :title="editingLink ? $t('page.editLink') : $t('page.addLink')" width="420px" append-to-body :before-close="linkGuard.beforeClose">
      <el-form label-position="top">
        <el-form-item :label="$t('page.fLinkText')" required>
          <el-input v-model="linkForm.name" maxlength="60" :placeholder="$t('page.phLinkName')" />
        </el-form-item>
        <el-form-item :label="$t('page.fLinkUrl')" required>
          <el-input v-model="linkForm.url" placeholder="https://example.com" />
        </el-form-item>
        <el-form-item :label="$t('page.fLinkIcon')">
          <div class="pv-icon-row">
            <el-input v-model="linkForm.icon" placeholder="https://example.com/favicon.ico" />
            <span class="adm-ico" :style="{ background: iconBg(linkForm.name || '?') }">
              <span>{{ (linkForm.name || '?').slice(0, 1).toUpperCase() }}</span>
              <img v-if="previewIcon" :src="previewIcon" alt="" @error="onIconErr">
            </span>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="linkDlg = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="linkSaving" @click="saveLink">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 监控模块新增/编辑弹窗（与总览数据监控同款表单，多一个跳转链接） -->
    <el-dialog v-model="metricDlg" :title="editingMetric ? $t('page.editMetric') : $t('page.addMetric')" width="460px" append-to-body :before-close="metricGuard.beforeClose">
      <el-form label-position="top">
        <el-form-item :label="$t('page.fSrc')" required>
          <el-radio-group v-model="metricForm.src" @change="onMetricSrcChange">
            <el-radio-button value="server">{{ $t('page.server') }}</el-radio-button>
            <el-radio-button value="ai">{{ $t('page.usageAcc') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="metricForm.src === 'server'" :label="$t('page.server')" required>
          <el-select v-model="metricForm.server_id" filterable style="width: 100%" @change="() => (metricForm.param = '')">
            <el-option v-for="s in servers" :key="s.id" :label="s.name" :value="s.id" />
          </el-select>
        </el-form-item>
        <el-form-item v-else :label="$t('page.usageAcc')" required>
          <el-select v-model="metricForm.ai_id" filterable style="width: 100%" @change="onMetricAccChange">
            <el-option v-for="a in metricAccOptions" :key="a.id" :label="a.label" :value="a.id" />
          </el-select>
          <div v-if="!metricAccOptions.length" class="cc-hint">{{ $t('page.noAcc') }}</div>
        </el-form-item>
        <el-form-item :label="$t('page.fMetric')" required>
          <el-select v-model="metricForm.metric" style="width: 100%" @change="() => (metricForm.param = '')">
            <el-option v-for="m in metricOptions" :key="m.value" :label="$t(m.label)" :value="m.value" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="metricParamKind" :label="metricParamKind === 'net' ? $t('page.paramNet') : metricParamKind === 'disk' ? $t('page.paramDisk') : $t('page.paramTemp')">
          <el-select v-model="metricForm.param" clearable style="width: 100%">
            <el-option v-for="p in metricParamOptions" :key="p" :label="p" :value="p" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('page.fLabel')" required>
          <el-input v-model="metricForm.label" maxlength="60" :placeholder="$t('page.phLabel')" />
        </el-form-item>
        <el-form-item :label="$t('page.fJumpLink')">
          <el-input v-model="metricForm.link" :placeholder="$t('page.phJumpLink')" />
        </el-form-item>
        <el-form-item :label="$t('page.fIcon')">
          <div class="pv-icon-row">
            <el-input v-model="metricForm.icon" placeholder="https://example.com/favicon.ico" />
            <span class="adm-ico" :style="{ background: iconBg(metricForm.label || '?') }">
              <span>{{ (metricForm.label || '?').slice(0, 1).toUpperCase() }}</span>
              <img v-if="previewMetricIcon" :src="previewMetricIcon" alt="" @error="onIconErr">
            </span>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="metricDlg = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="metricSaving" @click="saveMetric">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>
    <!-- 服务集成新增/编辑弹窗（凭据不回显，留空沿用库内值） -->
    <el-dialog v-model="svcDlg" :title="editingSvc ? $t('page.editService') : $t('page.addService')" width="540px" append-to-body :before-close="svcGuard.beforeClose">
      <el-form label-position="top">
        <el-form-item :label="$t('page.fSvcType')" required>
          <el-radio-group v-model="svcForm.type" :disabled="!!editingSvc">
            <el-radio v-for="t in SVC_TYPES" :key="t.value" :value="t.value">{{ $t(t.label) }}</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="$t('page.fName')" required>
          <el-input v-model="svcForm.name" maxlength="60" :placeholder="svcNamePH(svcForm.type)" />
        </el-form-item>
        <el-form-item v-if="svcForm.type !== 'docker'" :label="$t('page.fSvcUrl')" required>
          <el-input v-model="svcForm.url" :placeholder="svcTypeMeta(svcForm.type).urlPH || 'http://'" />
        </el-form-item>
        <el-form-item v-else :label="$t('page.fSock')" required>
          <el-input v-model="svcForm.sock" placeholder="/var/run/docker.sock" />
        </el-form-item>
        <el-form-item :label="$t('page.fOpenUrl')">
          <el-input v-model="svcForm.open_url" :placeholder="$t('page.phOpenUrl')" />
        </el-form-item>
        <template v-if="svcForm.type === 'qbittorrent'">
          <el-form-item :label="$t('page.fWebuiAuth')">
            <div class="svc-row">
              <el-input v-model="svcForm.username" :placeholder="editingSvc ? $t('page.phKeep') : $t('page.username')" autocomplete="off" />
              <el-input v-model="svcForm.password" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.password')" autocomplete="new-password" />
            </div>
          </el-form-item>
          <el-form-item :label="$t('page.fQbToken')">
            <el-input v-model="svcForm.token" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.phOptional')" autocomplete="new-password" />
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'emby'">
          <el-form-item :label="$t('page.fEmbyToken')" required>
            <el-input v-model="svcForm.token" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : 'API Key'" autocomplete="new-password" />
          </el-form-item>
          <el-form-item :label="$t('page.fNowPlaying')">
            <el-switch v-model="svcForm.now_playing" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'homeassistant'">
          <el-form-item :label="$t('page.fHaToken')" required>
            <el-input v-model="svcForm.token" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.phToken')" autocomplete="new-password" />
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
          <el-form-item :label="$t('page.fEntities')">
            <div class="svc-ents">
              <div v-for="(e, i) in svcForm.entities" :key="i" class="svc-ent">
                <el-input v-model="e.label" :placeholder="$t('page.phEntLabel')" maxlength="60" />
                <el-input v-model="e.entity" placeholder="sensor.temperature" maxlength="120" />
                <el-input v-model="e.unit" :placeholder="$t('page.phUnit')" maxlength="30" class="svc-ent-unit" />
                <el-input-number
                  v-model="e.decimals"
                  :min="0" :max="4" :step="1" :controls="false"
                  :placeholder="$t('page.phDecimals')"
                  class="svc-ent-dec"
                />
                <el-icon class="svc-ent-del" :title="$t('common.delete')" @click="svcForm.entities.splice(i, 1)"><Delete /></el-icon>
              </div>
              <el-button text type="primary" :icon="Plus" :disabled="svcForm.entities.length >= 16" @click="svcForm.entities.push({ label: '', entity: '', unit: '', decimals: null })">{{ $t('page.addEntity') }}</el-button>
            </div>
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'adguard' || svcForm.type === 'transmission'">
          <el-form-item :label="$t('page.fUserPass')">
            <div class="svc-row">
              <el-input v-model="svcForm.username" :placeholder="editingSvc ? $t('page.phKeep') : $t('page.username')" autocomplete="off" />
              <el-input v-model="svcForm.password" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.password')" autocomplete="new-password" />
            </div>
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'pihole'">
          <el-form-item :label="$t('page.fPiholePass')">
            <el-input v-model="svcForm.token" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.phOptional')" autocomplete="new-password" />
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'deluge'">
          <el-form-item :label="$t('page.fDelugePass')" required>
            <el-input v-model="svcForm.password" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.password')" autocomplete="new-password" />
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'aria2'">
          <el-form-item :label="$t('page.fAria2Token')">
            <el-input v-model="svcForm.token" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.phOptional')" autocomplete="new-password" />
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'navidrome' || svcForm.type === 'dsm'">
          <el-form-item :label="svcForm.type === 'navidrome' ? $t('page.fNavidromePass') : $t('page.fRestrictedPass')" required>
            <div class="svc-row">
              <el-input v-model="svcForm.username" :placeholder="editingSvc ? $t('page.phKeep') : $t('page.username')" autocomplete="off" />
              <el-input v-model="svcForm.password" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.password')" autocomplete="new-password" />
            </div>
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'nextcloud'">
          <el-form-item :label="$t('page.fNextcloudPass')" required>
            <div class="svc-row">
              <el-input v-model="svcForm.username" :placeholder="editingSvc ? $t('page.phKeep') : $t('page.username')" autocomplete="off" />
              <el-input v-model="svcForm.password" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.phAppPass')" autocomplete="new-password" />
            </div>
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'proxmox'">
          <el-form-item :label="$t('page.fPveToken', { cred: PVE_TOKEN_CRED })">
            <el-input v-model="svcForm.token" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.phOptional')" autocomplete="new-password" />
          </el-form-item>
          <el-form-item :label="$t('page.fPveUserPass', { cred: PVE_REALM_CRED })">
            <div class="svc-row">
              <el-input v-model="svcForm.username" placeholder="root@pam" autocomplete="off" />
              <el-input v-model="svcForm.password" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : $t('page.password')" autocomplete="new-password" />
            </div>
          </el-form-item>
          <el-form-item :label="$t('page.fInsecurePve')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'portainer'">
          <el-form-item :label="$t('page.fPortToken')" required>
            <el-input v-model="svcForm.token" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : 'API Key'" autocomplete="new-password" />
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
        <template v-else-if="svcForm.type === 'truenas'">
          <el-form-item :label="$t('page.fTnToken')" required>
            <el-input v-model="svcForm.token" type="password" show-password :placeholder="editingSvc ? $t('page.phKeep') : 'API Key'" autocomplete="new-password" />
          </el-form-item>
          <el-form-item :label="$t('page.fInsecure')">
            <el-switch v-model="svcForm.insecure" />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button :loading="svcTesting" @click="testSvc">{{ $t('page.testConn') }}</el-button>
        <el-button @click="svcDlg = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="svcSaving" @click="saveSvc">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- Emby 播放记录（528/530）：服务卡「记录」弹层——清单（筛选+翻页）/统计（用户/类型/每日）两视图 -->
    <el-dialog v-model="embyHistDlg" :title="$t('page.embyHistTitle')" width="600px" append-to-body @close="stopEmbyHistTimer">
      <div class="pv-emby-topbar">
        <div class="pv-emby-views">
          <button class="pv-emby-view" :class="{ on: embyView === 'list' }" @click="embyView = 'list'">{{ $t('page.embyTabList') }}</button>
          <button class="pv-emby-view" :class="{ on: embyView === 'stats' }" @click="switchEmbyStats()">{{ $t('page.embyTabStats') }}</button>
        </div>
        <template v-if="embyView === 'list'">
          <el-select v-model="embyHistUser" clearable :placeholder="$t('common.user')" size="small" class="pv-emby-filter" @change="embyFilterChanged">
            <el-option v-for="u in embyHistUsers" :key="u" :label="u" :value="u" />
          </el-select>
          <el-select v-model="embyHistType" clearable :placeholder="$t('page.fType')" size="small" class="pv-emby-filter" @change="embyFilterChanged">
            <el-option v-for="t in embyHistTypes" :key="t" :label="$t(EMBY_TYPES[t] || t)" :value="t" />
          </el-select>
          <el-date-picker
            v-model="embyHistDates"
            type="daterange"
            size="small"
            value-format="YYYY-MM-DD"
            range-separator="–"
            :start-placeholder="$t('page.startDate')"
            :end-placeholder="$t('page.endDate')"
            class="pv-emby-date"
            @change="embyFilterChanged"
          />
        </template>
        <div v-else class="pv-emby-ranges">
          <button v-for="r in EMBY_RANGES" :key="r.d" class="pv-emby-range" :class="{ on: embyStatDays === r.d }" @click="loadEmbyStats(r.d)">{{ $t(r.label) }}</button>
        </div>
      </div>

      <!-- 清单视图：筛选 + 页码翻页（每页 50）；页码器放滚动区外，翻到第 2 页起不用滚过整页记录找（3） -->
      <div v-show="embyView === 'list'">
        <div v-loading="embyHistLoading" class="pv-emby-hist">
          <div v-for="e in embyHistRows" :key="e.id" class="pv-emby-row" :class="{ live: !e.ended_at }">
            <div class="pv-emby-top">
              <b class="pv-emby-user">{{ e.user || '-' }}</b>
              <span v-if="EMBY_TYPES[e.type] || e.type" class="pv-emby-type">{{ $t(EMBY_TYPES[e.type] || e.type) }}</span>
              <span class="pv-emby-title"><template v-if="e.type === 'Episode' && e.episode > 0">{{ embySE(e) }} </template>{{ e.title }}<template v-if="e.sub"> · {{ e.sub }}</template></span>
              <i v-if="!e.ended_at" class="pv-emby-live"><span class="dot" />{{ $t('page.playingNow') }}</i>
            </div>
            <div class="pv-emby-meta">
              <span>{{ embyRange(e) }}</span>
              <span v-if="e.device">{{ e.device }}</span>
              <span class="pv-emby-dur">{{ embyDur(e) }}</span>
            </div>
            <div v-if="e.runtime_ticks > 0" class="pv-emby-bar">
              <div class="pv-emby-track"><div class="pv-emby-fill" :class="{ on: !e.ended_at }" :style="{ width: embyPct(e) + '%' }" /></div>
              <span class="pv-emby-pct">{{ embyPct(e) }}%</span>
            </div>
          </div>
          <div v-if="!embyHistLoading && !embyHistRows.length" class="pv-emby-empty">{{ $t('page.embyEmpty') }}</div>
        </div>
        <el-pagination
          v-if="embyHistTotal > 50"
          class="pv-emby-pager"
          small
          layout="total, prev, pager, next"
          :total="embyHistTotal"
          :page-size="50"
          :current-page="embyHistPage"
          @current-change="embyPageChanged"
        />
      </div>

      <!-- 统计视图（530）：用户排行（次数+时长）/ 类型 / 每日次数与时长折线 -->
      <div v-show="embyView === 'stats'" v-loading="embyStatLoading" class="pv-emby-stats">
        <div v-if="!embyStatLoading && embyStatEmpty" class="pv-emby-empty">{{ $t('page.embyStatEmpty') }}</div>
        <template v-else>
          <div v-if="embyStat.users.length" class="pv-emby-sblock">
            <div class="pv-emby-shead">{{ embyUsersHead }}</div>
            <div v-for="u in embyStat.users" :key="u.user" class="pv-emby-srow">
              <span class="pv-emby-sname">{{ u.user }}</span>
              <div class="pv-emby-strack"><div class="pv-emby-sfill" :style="{ width: statBarW(u.plays, maxUserPlays) + '%' }" /></div>
              <span class="pv-emby-sval">{{ $t('page.playsN', { n: u.plays }) }} · {{ fmtEmbySecs(u.seconds) }}</span>
            </div>
          </div>
          <div v-if="embyStat.types.length" class="pv-emby-sblock">
            <div class="pv-emby-shead">{{ $t('page.byType') }}</div>
            <div v-for="t in embyStat.types" :key="t.type" class="pv-emby-srow">
              <span class="pv-emby-sname">{{ $t(EMBY_TYPES[t.type] || t.type) }}</span>
              <div class="pv-emby-strack"><div class="pv-emby-sfill type" :style="{ width: statBarW(t.plays, maxTypePlays) + '%' }" /></div>
              <span class="pv-emby-sval">{{ $t('page.playsN', { n: t.plays }) }}</span>
            </div>
          </div>
          <div v-if="embyStat.days.length" class="pv-emby-sblock">
            <div class="pv-emby-shead">{{ $t('page.dailyPlays') }}</div>
            <SeriesChart :series="[{ name: $t('page.plays'), points: embyDayPlays }]" height="130px" :show-legend="false" />
          </div>
          <div v-if="embyStat.days.length" class="pv-emby-sblock">
            <div class="pv-emby-shead">{{ $t('page.dailyHours') }}</div>
            <SeriesChart :series="[{ name: $t('page.duration'), points: embyDayHours }]" height="130px" :show-legend="false" :valueFormatter="fmtEmbyHours" />
          </div>
          <!-- 每用户看过的剧影清单（1）：剧集按剧名/电影按片名归并，多次播放合并一条，
               时间=最后播放；按用户排行序分组、组内按最后播放新→旧 -->
          <div v-if="embyItemsByUser.length" class="pv-emby-sblock">
            <div class="pv-emby-shead">{{ embyItemsHead }}</div>
            <div v-for="g in embyItemsByUser" :key="g.user" class="pv-emby-uitems">
              <div class="pv-emby-uname">{{ g.user }}<span class="pv-emby-ucount">{{ $t('page.itemsN', { n: g.items.length }) }}</span></div>
              <div class="pv-emby-items">
                <span
                  v-for="it in g.items"
                  :key="it.type + ':' + it.name"
                  class="pv-emby-item"
                  :title="$t('page.itemTitle', { name: it.name, type: $t(EMBY_TYPES[it.type] || it.type), n: it.plays })"
                >
                  <i class="pv-emby-item-tag">{{ $t(EMBY_TYPES[it.type] || it.type) }}</i>
                  <span class="pv-emby-item-name">{{ it.name }}</span>
                  <span class="pv-emby-item-time">{{ embyTS(it.last_at) }}</span>
                </span>
              </div>
            </div>
          </div>
        </template>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useDirtyGuard } from '../composables/dirtyGuard.js'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  ArrowDown, ArrowUp, Back, Bottom, Connection, Delete, Edit, Hide, HomeFilled, Plus, Right, Search, Setting, Top, View,
} from '@element-plus/icons-vue'
import { HaMenuIcon } from '../utils/haMenuIcon.js'
import { ENGINE_ICOS } from '../utils/engineIcons.js'
import { api, getAvatar, getAvatarVer, setAvatar } from '../api/index.js'
import { computeTileViews } from '../composables/useTileViews.js'
import { useIsMobile } from '../composables/useIsMobile.js'
import { fmtDiskBps, fmtPercent, utilClass, withRemark } from '../utils/format.js'
import { moveById } from '../utils/dragReorder.js'
import { AI_PROV_LABELS, TILE_METRICS } from '../utils/tileMetrics.js'
import { barColor } from '../utils/chartTheme.js'
import { safeUrl } from '../utils/url.js'
import { windLevel, wmoIcon, wmoText, wxDayName, wxDayPrecip, wxDayTitle, wxIsRainCode, wxIsSnowCode } from '../utils/weather.js'
import SeriesChart from '../components/SeriesChart.vue'
import MarqueeText from '../components/MarqueeText.vue'
import { cdDaysText } from '../utils/countdown.js'
import { HA_BRAND_ICO } from '../utils/haIcons.js'
import { theme, FRONT_THEMES } from '../utils/theme.js'
import { useI18n } from 'vue-i18n'
import { lang, registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/page.js'
import calendarMsgs from '../i18n/pages/calendar.js'

// 页面语言包（ns page / weather）：setup 顶层注册一次（重复注册无害）；
// composer 的 t 追踪 locale，语言切换即时重渲。倒数日卡的 cd.* 文案在日历碎片里，
// 主页不再引 CountdownDialog 后须自行注册
registerPageMessages(pageMsgs)
registerPageMessages(calendarMsgs)
const { t } = useI18n()
// 浏览器格式化（toLocaleString 等）用的 lang 感知 locale
const sysLocale = () => (lang.value === 'en' ? 'en-US' : 'zh-CN')
// CSS content 伪元素文案经 v-bind 注入（语言切换跟随）
const hiddenSuffix = computed(() => t('page.hiddenSuffix'))
// PVE 凭据字面量（含 @，不能进语言包，经 {cred} 插值传入）
const PVE_TOKEN_CRED = 'user@realm!id=secret'
const PVE_REALM_CRED = 'root@pam'

// 品牌暗色主题（38 gold + 41 八套）：前台整套跟随，violet/白/暗维持深紫缺省
const frontCls = computed(() => (FRONT_THEMES.includes(theme.value) ? theme.value : ''))
const isMobile = useIsMobile()

const router = useRouter()

// 回控制台：普通点击当前页跳转（按住 Cmd/Ctrl 新开窗口）
function goConsole(e) {
  if (e.metaKey || e.ctrlKey) {
    window.open(router.resolve('/console').href, '_blank', 'noopener')
    return
  }
  router.push('/console')
}

// ---- 数据加载：主页配置一次（含监控模块） + 服务器快照按配置间隔轮询（页签隐藏时跳过，切回即补拉） ----
const loading = ref(false)
// 首屏门槛：初始请求全部落定才放行主体渲染（只管首帧，后续轮询不受控）
const ready = ref(false)
const cfg = reactive({
  title: '', show_search: true, link_mode: 'vertical', show_avatar: true, show_ha: true, show_stats: true, refresh_sec: 30,
  weather_enabled: false, weather_mode: 'auto', weather_city: '', weather_lat: 0, weather_lon: 0, weather_provider: '', weather_key: '', has_weather_key: false, weather_api_host: '',
  show_countdown: true,
})
const groups = ref([])
const links = ref([])


// ---- 倒数日卡（个人助理批）：/countdowns 已按卡片序排好；每小时重拉对日期；只读展示，管理在后台日历页 ----
const countdowns = ref([])
let cdTimer = null
async function loadCountdowns() {
  try {
    const r = await api.get('/countdowns')
    countdowns.value = r.items || []
  } catch {
    /* 倒数日失败静默：不挡主页其余区块 */
  }
}
const servers = ref([])
const aiAccounts = ref([]) // 用量查询账户（486 数据监控用量格子数据源，loadServers 同节拍刷新）

// 未读消息数（返回控制台按钮角标）：30s 轮询，页签隐藏时跳过
const unread = ref(0)
let unreadTimer = null
// 页签可见性变化：回到主页时立即补刷未读角标与统计概览（256/459）
function onVis() {
  if (!document.hidden) {
    loadUnread()
    loadStats()
  }
}
async function loadUnread() {
  if (document.hidden) return
  try {
    const res = await api.get('/messages/unread')
    unread.value = res.count || 0
  } catch {
    /* 401 已由 api 层跳转 */
  }
}

// ---- 统计概览（459）：/page/stats 聚合计数，随快照同节拍轮询+回前台补拉 ----
const stats = ref(null)

async function loadStats() {
  if (document.hidden) return
  try {
    stats.value = await api.get('/page/stats')
  } catch {
    /* 概览失败静默：不挡主页其余区块 */
  }
}

// 统计卡小图标（stroke 继承文字色，同页内联 SVG 口径）
const STATS_ICOS = {
  messages: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 8-3 8h18s-3-1-3-8"/><path d="M13.7 20a2 2 0 0 1-3.4 0"/></svg>`,
  servers: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 7.5h.01M7 16.5h.01"/></svg>`,
  gh: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="6" cy="6" r="2.6"/><circle cx="6" cy="18" r="2.6"/><circle cx="18" cy="6" r="2.6"/><path d="M6 8.6v6.8M18 8.6c0 5.2-4.2 6.6-8 7"/></svg>`,
  mail: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="5" width="18" height="14" rx="2"/><path d="m3.5 7 8.5 6 8.5-6"/></svg>`,
  reminders: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="5" width="18" height="16" rx="2"/><path d="M8 3v4M16 3v4M3 10h18M9.5 15.5l2 2 3.5-3.5"/></svg>`,
  certs: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2 4.5 5v6c0 5 3.2 8.4 7.5 10 4.3-1.6 7.5-5 7.5-10V5z"/><path d="m9 11.5 2 2 4-4"/></svg>`,
  pt: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M5 21V4"/><path d="M5 4c4-2.5 8 2.5 14 0v10c-6 2.5-10-2.5-14 0"/></svg>`,
  members: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 9V6a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2v3a2.5 2.5 0 0 0 0 6v3a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-3a2.5 2.5 0 0 0 0-6z"/><path d="M13 5v2M13 11v2M13 17v2"/></svg>`,
  tv: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="6" width="18" height="13" rx="2"/><path d="m8 2.5 4 3.5 4-3.5"/><path d="m10.2 10.2 4.6 2.3-4.6 2.3z"/></svg>`,
}

// 统计卡视图：空数据域整卡隐藏（无邮件账号/无证书/无应签站/无服务器/无 GitHub 仓库）；
// 待办恒显（0 = 今日清零也是有效状态）。说明行只留异常信息（473：无未读/全部在线等
// 好态字去掉，空=不渲染）；alert = 有需要注意的非零项（红点强调）。
const statCards = computed(() => {
  const s = stats.value
  if (!s || !cfg.show_stats) return []
  const cards = []
  cards.push({
    key: 'messages', name: t('page.statMessages'), ico: STATS_ICOS.messages, to: '/console/messages',
    value: s.messages_unread, sub: s.messages_unread ? t('page.statUnreadUnit') : '', alert: s.messages_unread > 0,
  })
  if (s.servers.total > 0) {
    const bad = s.servers.offline + s.servers.firing
    const parts = []
    if (s.servers.offline) parts.push(t('page.offlineN', { n: s.servers.offline }))
    if (s.servers.firing) parts.push(t('page.alertN', { n: s.servers.firing }))
    cards.push({
      key: 'servers', name: t('page.statServers'), ico: STATS_ICOS.servers, to: '/console',
      value: `${s.servers.online}/${s.servers.total}`,
      sub: parts.join(' · '),
      subAlert: bad > 0, alert: bad > 0,
    })
  }
  if (s.mail_accounts > 0) {
    cards.push({
      key: 'mail', name: t('page.statMail'), ico: STATS_ICOS.mail, to: '/console/mail',
      value: s.mail_unread, sub: s.mail_unread ? t('page.mailUnreadUnit') : '',
      title: t('page.mailAccounts', { n: s.mail_accounts }), alert: s.mail_unread > 0,
    })
  }
  cards.push({
    key: 'reminders', name: t('page.statReminders'), ico: STATS_ICOS.reminders, to: '/console/reminders',
    value: s.reminders.today,
    sub: s.reminders.overdue ? t('page.overdueN', { n: s.reminders.overdue }) : '',
    subAlert: s.reminders.overdue > 0, alert: s.reminders.overdue > 0,
  })
  if (s.certs.total > 0) {
    cards.push({
      key: 'certs', name: t('page.statCerts'), ico: STATS_ICOS.certs, to: { path: '/console/svcmon', query: { tab: 'certs' } },
      value: s.certs.expiring,
      title: t('page.certsTotal', { n: s.certs.total }), subAlert: s.certs.expiring > 0, alert: s.certs.expiring > 0,
    })
  }
  // 会员到期（492）：主值=总数，说明行=15 日内到期数（含已过期未删的）
  if (s.memberships && s.memberships.total > 0) {
    cards.push({
      key: 'members', name: t('page.statMembers'), ico: STATS_ICOS.members, to: '/console/reminders?view=member',
      value: s.memberships.total,
      sub: s.memberships.expiring ? t('page.expiringN', { n: s.memberships.expiring }) : '',
      title: t('page.membersTitle', { total: s.memberships.total, expiring: s.memberships.expiring }),
      subAlert: s.memberships.expiring > 0, alert: s.memberships.expiring > 0,
    })
  }
  // 追剧（552）：已完结数/全部数，PT 签到同款 X/Y 形态
  if (s.tv && s.tv.total > 0) {
    cards.push({
      key: 'tv', name: t('page.statTv'), ico: STATS_ICOS.tv, to: '/console/tv',
      value: `${s.tv.done}/${s.tv.total}`,
      title: t('page.tvTitle', { total: s.tv.total, done: s.tv.done }),
    })
  }
  if (s.pt.due > 0) {
    cards.push({
      key: 'pt', name: t('page.statPt'), ico: STATS_ICOS.pt, to: '/console/pt',
      value: `${s.pt.signed}/${s.pt.due}`,
      sub: s.pt.signed >= s.pt.due ? '' : t('page.toCheckIn'),
      subAlert: s.pt.signed < s.pt.due, alert: s.pt.signed < s.pt.due,
    })
  }
  if (s.gh && s.gh.repos > 0) {
    cards.push({
      key: 'gh', name: 'GitHub', ico: STATS_ICOS.gh, to: '/console/svcmon?tab=gh',
      value: s.gh.news, sub: s.gh.news ? t('page.ghNews') : '',
      title: t('page.ghTitle', { n: s.gh.repos }), subAlert: s.gh.news > 0, alert: s.gh.news > 0,
    })
  }
  return cards
})

// 统计卡点击跳控制台对应页（Cmd/Ctrl 新开窗口，同链接区口径）；无目标不动作
function openStatLink(e, c) {
  if (!c.to) return
  if (e.metaKey || e.ctrlKey) {
    window.open(router.resolve(c.to).href, '_blank', 'noopener')
    return
  }
  router.push(c.to)
}

// 登录账号（标题前头像用）：头像 data URL，未设置回落首字彩徽标
const me = reactive({ username: '', avatar: getAvatar() })

async function loadConfig() {
  const res = await api.get('/page/config')
  cfg.title = res.title || ''
  cfg.show_search = !!res.show_search
  cfg.link_mode = res.link_mode === 'horizontal' ? 'horizontal' : 'vertical'
  cfg.show_avatar = res.show_avatar !== false
  cfg.show_ha = res.show_ha !== false
  cfg.show_stats = res.show_stats !== false
  const rs = Number(res.refresh_sec)
  cfg.refresh_sec = (rs === 0 || (rs >= 5 && rs <= 86400)) ? rs : 30
  cfg.weather_enabled = !!res.weather_enabled
  cfg.weather_mode = res.weather_mode === 'manual' ? 'manual' : 'auto'
  cfg.weather_city = res.weather_city || ''
  cfg.weather_lat = Number(res.weather_lat) || 0
  cfg.weather_lon = Number(res.weather_lon) || 0
  cfg.weather_provider = ['gaode', 'qweather'].includes(res.weather_provider) ? res.weather_provider : ''
  cfg.has_weather_key = !!res.has_weather_key // Key 不回显，空表单=沿用已存值
  cfg.weather_api_host = res.weather_api_host || ''
  cfg.show_countdown = res.show_countdown !== false
  groups.value = res.groups || []
  links.value = res.links || []
  tileCfg.value = res.metrics || []
  if (!activeGrp.value && groups.value.length) activeGrp.value = groups.value[0].id
  for (const g of groups.value) vOpen.add(g.id) // 竖向模式默认全展开
  applyTitle()
}

let serversTimer = null
async function loadServers() {
  if (document.hidden) return
  try {
    // 用量账户快照（486 用量格子数据源）与服务器快照同节拍刷新
    const [res, ares] = await Promise.all([api.get('/servers'), api.get('/aiusage/accounts')])
    servers.value = (res.servers || []).sort((a, b) =>
      a.name.localeCompare(b.name, 'zh-Hans-CN', { numeric: true, sensitivity: 'base' }))
    aiAccounts.value = ares.accounts || []
  } catch {
    /* 401 已由 api 层跳转 */
  }
}

// 自动刷新：按配置间隔的 setTimeout 链（保存设置后即时生效；0=不自动刷新）
function scheduleServers() {
  clearTimeout(serversTimer)
  if (!cfg.refresh_sec) return
  serversTimer = setTimeout(async () => {
    await loadServers()
    loadUnread() // 256：返回控制台按钮的未读角标与数据监控同节拍刷新
    loadStats()  // 459：统计概览同节拍
    scheduleServers()
  }, cfg.refresh_sec * 1000)
}

const tileViews = computed(() => computeTileViews(tiles.value.filter((m) => !m.hidden), servers.value, aiAccounts.value))
const tiles = computed(() => tileCfg.value)
const tileCfg = ref([])

// 点击跳转：配置了链接的模块点击当前页打开，按住 Cmd/Ctrl 新开窗口（未配置
// 无动作）。safeUrl 兜底：location.href 赋值会执行 javascript: URL，仅靠
// 保存期校验挡不住历史脏数据/直写 API
function openTileLink(e, t) {
  const u = safeUrl(t.link)
  if (!u) return
  if (e.metaKey || e.ctrlKey) {
    window.open(u, '_blank', 'noopener')
    return
  }
  window.location.href = u
}

// 磁贴图标加载失败回落首字徽标
function onTileIconErr(t) {
  tileCfg.value = tileCfg.value.map((x) => (x.id === t.id ? { ...x, icon: '' } : x))
}

function onMetricRowIconErr(m) {
  tileCfg.value = tileCfg.value.map((x) => (x.id === m.id ? { ...x, icon: '' } : x))
}

// ---- 标题区：时钟 + 搜索 ----
const clock = reactive({ time: '', date: '' })
let clockTimer = null

function tick() {
  const d = new Date()
  const p = (n) => String(n).padStart(2, '0')
  clock.time = `${p(d.getHours())}:${p(d.getMinutes())}`
  clock.date = `${p(d.getMonth() + 1)}-${p(d.getDate())} ${t('page.dateWeek', { d: t('page.week.' + d.getDay()) })}`
}

const ENGINES = [
  { id: 'baidu', name: '百度', url: 'https://www.baidu.com/s?wd=', color: '#2932e1' },
  { id: 'google', name: 'Google', url: 'https://www.google.com/search?q=', color: '#ea4335' },
  { id: 'bing', name: 'Bing', url: 'https://www.bing.com/search?q=', color: '#0d8484' },
  { id: 'sogou', name: '搜狗', url: 'https://www.sogou.com/web?query=' },
  { id: 'so360', name: '360搜索', url: 'https://www.so.com/s?q=' },
  { id: 'shenma', name: '神马', url: 'https://m.sm.cn/s?q=' },
  { id: 'quark', name: '夸克', url: 'https://quark.sm.cn/s?q=' },
  { id: 'toutiao', name: '头条搜索', url: 'https://so.toutiao.com/search?keyword=' },
  { id: 'gscholar', name: '谷歌学术', url: 'https://scholar.google.com/scholar?hl=zh-CN&q=' },
  { id: 'wanfang', name: '万方', url: 'https://s.wanfangdata.com.cn/paper?q=' },
  { id: 'cnki', name: '知网', url: 'https://kns.cnki.net/kns8s/defaultresult/index?kw=' },
  { id: 'xueshu', name: '百度学术', url: 'https://xueshu.baidu.com/s?wd=' },
]
const engineId = ref(localStorage.getItem('page-engine') || 'baidu')
const engine = computed(() => ENGINES.find((e) => e.id === engineId.value) || ENGINES[0])
const keyword = ref('')

function setEngine(id) {
  engineId.value = id
  localStorage.setItem('page-engine', id)
}

function doSearch() {
  const q = keyword.value.trim()
  if (!q) return
  window.open(engine.value.url + encodeURIComponent(q), '_blank', 'noopener')
}

// ---- 链接区：分组手风琴。竖向（默认）=上下排列、默认全展开可逐组收起；
// 横向=同时只展开一组 ----
const activeGrp = ref(0)
const vOpen = reactive(new Set())

const linkMode = computed(() => cfg.link_mode)

function isGrpOpen(id) {
  return linkMode.value === 'vertical' ? vOpen.has(id) : activeGrp.value === id
}

function toggleGrp(id) {
  if (linkMode.value === 'vertical') {
    if (vOpen.has(id)) vOpen.delete(id)
    else vOpen.add(id)
  } else {
    activeGrp.value = id
  }
}

function linksOf(gid) {
  return links.value.filter((l) => l.group_id === gid)
}
// 主页链接清单（隐藏的条目不显示；管理抽屉仍用 linksOf 全量管理）
function homeLinksOf(gid) {
  return linksOf(gid).filter((l) => !l.hidden)
}

// 子项显隐切换（数据监控格子 / 服务卡 / 链接，各走专用轻量端点）：
// 磁贴行是 tileCfg 的克隆，改回源对象才能驱动主页 tileViews 重算
async function toggleHidden(url, obj, sync) {
  try {
    await api.put(url, { hidden: !obj.hidden })
    obj.hidden = !obj.hidden
    if (sync) sync()
  } catch {
    ElMessage.error(t('page.opFailed'))
  }
}
function toggleMetric(m) {
  toggleHidden(`/page/metrics/${m.id}/hidden`, m, () => {
    const t = tileCfg.value.find((x) => x.id === m.id)
    if (t) t.hidden = m.hidden
  })
}
function toggleInt(it) {
  toggleHidden(`/integrations/${it.id}/hidden`, it)
}
function toggleLink(l) {
  toggleHidden(`/page/links/${l.id}/hidden`, l)
}

// 图标加载失败隐藏 img 露出首字徽标
function onIconErr(e) {
  e.target.style.display = 'none'
}

// 低清小图（favicon.ico 常见 16/32px）不放大铺满：cover 放大后会糊成一片色块、
// 带边框的图只剩黑边/白边；改为原尺寸居中完整显示，≥48px 的正常图保持铺满
const lowResIcons = reactive(new Set())

function onIconLoad(l, e) {
  const img = e.target
  if (img.naturalWidth && (img.naturalWidth < 48 || img.naturalHeight < 48)) lowResIcons.add(l.id)
  else lowResIcons.delete(l.id)
}

// 首字徽标底色：按名称哈希取固定色板，同名恒色
const ICON_HUES = [212, 262, 332, 12, 28, 152, 192]
function iconBg(name) {
  let h = 0
  for (const c of name || '') h = (h * 31 + c.codePointAt(0)) >>> 0
  return `hsl(${ICON_HUES[h % ICON_HUES.length]} 52% 42%)`
}

// ---- 管理抽屉 ----
const adminOpen = ref(false)
const adminTab = ref('base')
const sysVer = ref('') // 服务端版本号（基础设置底部展示，/status 非首屏关键路径）
const form = reactive({
  title: '', show_search: true, link_mode: 'vertical', show_avatar: true, show_ha: true, show_stats: true, refresh_sec: 30,
  weather_enabled: false, weather_mode: 'auto', weather_city: '', weather_lat: 0, weather_lon: 0, weather_provider: '', weather_key: '', has_weather_key: false, weather_api_host: '', weather_pick: '',
  show_countdown: true,
})
const savingBase = ref(false)

// 监控自动刷新档位（秒；0=不自动刷新；label 存 i18n key）
const REFRESH_OPTIONS = [
  { value: 0, label: 'page.refresh0' },
  { value: 5, label: 'page.refresh5' },
  { value: 10, label: 'page.refresh10' },
  { value: 30, label: 'page.refresh30' },
  { value: 60, label: 'page.refresh60' },
  { value: 300, label: 'page.refresh300' },
]

function openAdmin() {
  form.title = cfg.title
  form.show_search = cfg.show_search
  form.link_mode = cfg.link_mode
  form.show_avatar = cfg.show_avatar
  form.show_ha = cfg.show_ha
  form.show_stats = cfg.show_stats
  form.show_countdown = cfg.show_countdown
  form.refresh_sec = cfg.refresh_sec
  form.weather_enabled = cfg.weather_enabled
  form.weather_mode = cfg.weather_mode
  form.weather_city = cfg.weather_city
  form.weather_lat = cfg.weather_lat
  form.weather_lon = cfg.weather_lon
  form.weather_provider = cfg.weather_provider || ''
  form.weather_key = '' // Key 不回显：留空=沿用已存值（cfg.has_weather_key 只用于占位提示）
  form.weather_api_host = cfg.weather_api_host || ''
  form.weather_pick = ''
  cityOptions.value = []
  if (cfg.weather_mode === 'manual' && (cfg.weather_lat || cfg.weather_lon)) {
    const key = `${cfg.weather_lat},${cfg.weather_lon},${cfg.weather_city}`
    cityOptions.value = [{ key, label: cfg.weather_city || t('page.currentCity') }]
    form.weather_pick = key
  }
  if (!selGrp.value && groups.value.length) selGrp.value = groups.value[0].id
  adminOpen.value = true
}

async function saveSettings() {
  if (form.weather_enabled && form.weather_mode === 'manual' && !form.weather_lat && !form.weather_lon) {
    return ElMessage.warning(t('page.needCity'))
  }
  savingBase.value = true
  try {
    await api.put('/page/settings', {
      title: form.title.trim(),
      show_search: form.show_search,
      show_avatar: form.show_avatar,
      show_ha: form.show_ha,
      show_stats: form.show_stats,
      link_mode: form.link_mode,
      refresh_sec: form.refresh_sec,
      weather_enabled: form.weather_enabled,
      weather_mode: form.weather_mode,
      weather_city: form.weather_city,
      weather_lat: form.weather_lat,
      weather_lon: form.weather_lon,
      weather_provider: form.weather_provider,
      weather_key: form.weather_key.trim(),
      weather_api_host: form.weather_api_host.trim(),
      show_countdown: form.show_countdown,
    })
    cfg.title = form.title.trim()
    cfg.show_countdown = form.show_countdown
    cfg.show_search = form.show_search
    cfg.link_mode = form.link_mode
    cfg.show_avatar = form.show_avatar
    cfg.show_ha = form.show_ha
    cfg.show_stats = form.show_stats
    cfg.refresh_sec = form.refresh_sec
    cfg.weather_enabled = form.weather_enabled
    cfg.weather_mode = form.weather_mode
    cfg.weather_city = form.weather_city
    cfg.weather_lat = form.weather_lat
    cfg.weather_lon = form.weather_lon
    cfg.weather_provider = form.weather_provider
    if (form.weather_key.trim()) { cfg.has_weather_key = true } // 留空沿用：本次没填不改变已存状态
    form.weather_key = ''
    cfg.weather_api_host = form.weather_api_host.trim()
    if (cfg.link_mode === 'vertical') {
      for (const g of groups.value) vOpen.add(g.id) // 切回竖向时全展开
    } else if (!isGrpOpen(activeGrp.value) && groups.value.length) {
      activeGrp.value = groups.value[0].id
    }
    applyTitle()
    scheduleServers() // 刷新间隔即时生效
    scheduleWx() // 天气开关/城市即时生效
    ElMessage.success(t('common.saved'))
  } catch (e) {
    ElMessage.error(t('page.saveFailed', { msg: e.message }))
  } finally {
    savingBase.value = false
  }
}

// ---- 天气配置：手动城市远程搜索（选中值 = 纬度,经度,城市名 组合键） ----
const cityOptions = ref([])
const citySearching = ref(false)

async function searchCity(q) {
  const kw = String(q || '').trim()
  if (!kw) return
  citySearching.value = true
  try {
    const res = await api.get(`/page/weather/city?name=${encodeURIComponent(kw)}`)
    cityOptions.value = (res.cities || []).map((c) => ({ key: `${c.lat},${c.lon},${c.name}`, label: c.label }))
  } catch (e) {
    ElMessage.error(t('page.citySearchFailed', { msg: e.message }))
  } finally {
    citySearching.value = false
  }
}

// 选中候选 → 解析回 lat/lon/city（组合键 `纬度,经度,城市名`）
function onCityPick(v) {
  if (!v) return
  const [lat, lon, ...rest] = String(v).split(',')
  form.weather_lat = Number(lat) || 0
  form.weather_lon = Number(lon) || 0
  form.weather_city = rest.join(',')
}

async function persistGroupOrder() {
  await api.put('/page/groups/order', { ids: groups.value.map((g) => g.id) }).catch(() => ElMessage.error(t('page.orderSaveFailed')))
}

function moveGrp(i, dir) {
  const j = i + dir
  if (j < 0 || j >= groups.value.length) return
  const arr = [...groups.value]
  ;[arr[i], arr[j]] = [arr[j], arr[i]]
  groups.value = arr
  persistGroupOrder()
}

async function addGroup() {
  let name = ''
  try {
    const r = await ElMessageBox.prompt(t('page.grpName'), t('page.addGroup'), {
      closeOnClickModal: false,
      inputPattern: /\S/,
      inputErrorMessage: t('page.needGrpName'),
      confirmButtonText: t('page.create'),
      cancelButtonText: t('common.cancel'),
    })
    name = r.value.trim()
  } catch {
    return
  }
  try {
    const res = await api.post('/page/groups', { name })
    groups.value = [...groups.value, res.group]
    selGrp.value = res.group.id
  } catch (e) {
    ElMessage.error(t('page.createFailed', { msg: e.message }))
  }
}

async function renameGrp(g) {
  let name = ''
  try {
    const r = await ElMessageBox.prompt(t('page.grpName'), t('page.renameGroup'), {
      closeOnClickModal: false,
      inputValue: g.name,
      inputPattern: /\S/,
      inputErrorMessage: t('page.needGrpName'),
      confirmButtonText: t('common.save'),
      cancelButtonText: t('common.cancel'),
    })
    name = r.value.trim()
  } catch {
    return
  }
  try {
    await api.put(`/page/groups/${g.id}`, { name })
    groups.value = groups.value.map((x) => (x.id === g.id ? { ...x, name } : x))
  } catch (e) {
    ElMessage.error(t('page.saveFailed', { msg: e.message }))
  }
}

async function delGrp(g) {
  const n = linksOf(g.id).length
  try {
    await ElMessageBox.confirm(
      t('page.delGroupConfirm', { name: g.name }) + (n ? t('page.delGroupLinks', { n }) : ''),
      t('page.delGroup'),
      { type: 'warning' },
    )
  } catch {
    return
  }
  try {
    await api.del(`/page/groups/${g.id}`)
    groups.value = groups.value.filter((x) => x.id !== g.id)
    links.value = links.value.filter((l) => l.group_id !== g.id)
    if (selGrp.value === g.id) selGrp.value = groups.value[0]?.id || 0
    if (activeGrp.value === g.id) activeGrp.value = groups.value[0]?.id || 0
  } catch (e) {
    ElMessage.error(t('page.delFailed', { msg: e.message }))
  }
}

const selGrp = ref(0)
const selGrpName = computed(() => groups.value.find((g) => g.id === selGrp.value)?.name || '')

async function persistLinkOrder(gid) {
  // group_id 一并传：组内拖拽时为同组幂等；跨组拖入时后端把 ids 归入该组
  const ids = linksOf(gid).map((l) => l.id)
  await api.put('/page/links/order', { ids, group_id: gid }).catch(() => ElMessage.error(t('page.orderSaveFailed')))
}

// ---- 主页拖拽排序（253 链接/分组 + 254 数据监控模块；PC 原生 HTML5 DnD）----
// 跨浏览器硬性口径（2026-10-03 修「拖拽后无法排序」）：每个投放目标必须同时
// cancel dragenter 与 dragover（Safari 只 cancel dragover 不触发 drop），
// dragover 里显式 dropEffect='move'（部分内核不设置即判禁止投放）。
function dragHasType(e, type) {
  const t = e.dataTransfer?.types
  if (!t) return false
  return t.includes ? t.includes(type) : t.contains(type) // 老 Safari types 是 DOMStringList 无 includes
}
function allowMove(e) {
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
}

// 分组顺序：拖组条到另一组条上 = 插到其前
const grpDrag = reactive({ from: -1, over: -1 })
function grpStart(i, e) {
  grpDrag.from = i
  e.dataTransfer?.setData('application/x-pv-grp', String(i))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function grpOver(i, e) {
  if (grpDrag.from < 0 || !dragHasType(e, 'application/x-pv-grp')) return
  allowMove(e)
  grpDrag.over = i
}
async function grpDrop(i) {
  const f = grpDrag.from
  grpDrag.from = grpDrag.over = -1
  if (f < 0 || f === i) return
  const arr = [...groups.value]
  const [g] = arr.splice(f, 1)
  arr.splice(f < i ? i - 1 : i, 0, g)
  groups.value = arr
  await persistGroupOrder()
}

// 链接：拖到另一链接上 = 插到其前；拖到组体空白 = 追加到该组（跨组同理）
const linkDrag = reactive({ id: null, over: null })
function linkStart(l, e) {
  linkDrag.id = l.id
  e.dataTransfer?.setData('application/x-pv-link', String(l.id))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function linkOver(overId, e) {
  if (linkDrag.id == null || linkDrag.id === overId) return
  if (!dragHasType(e, 'application/x-pv-link')) return
  allowMove(e)
  linkDrag.over = overId
}
function linkMoveFlat(movedId, targetId) {
  // 拖动链接到目标链接的位置（语义见 utils/dragReorder.js：落到目标位置，跨组同步归属）。
  // movedId 由调用方显式传入——linkDrop 先清了 linkDrag.id，此处再读它恒为 null
  // （386 修：组内「拖到链接上」从未生效的根因）。
  const r = moveById(links.value, movedId, targetId, (t) => t?.group_id ?? null)
  if (!r) return null
  links.value = r.arr
  return r.group
}
async function linkDrop(targetId) {
  const moved = linkDrag.id
  linkDrag.id = null
  linkDrag.over = null
  if (moved == null || moved === targetId) return
  const gid = linkMoveFlat(moved, targetId)
  if (gid != null) await persistLinkOrder(gid)
}
function linkOverInto(gid, e) {
  if (linkDrag.id == null || !dragHasType(e, 'application/x-pv-link')) return
  allowMove(e)
  const arr = linksOf(gid)
  linkDrag.over = arr.length ? arr[arr.length - 1].id : null
}
async function linkDropInto(gid) {
  const moved = linkDrag.id
  linkDrag.id = null
  const overId = linkDrag.over
  linkDrag.over = null
  if (moved == null) return
  const link = links.value.find((x) => x.id === moved)
  if (!link || link.group_id === gid) return // 原组内空白处 = 顺序不变
  // 跨组拖到组体空白：移到该组末尾
  const arr = [...links.value]
  const from = arr.findIndex((x) => x.id === moved)
  const [item] = arr.splice(from, 1)
  item.group_id = gid
  const last = [...arr].reverse().find((x) => x.group_id === gid)
  const idx = last ? arr.findIndex((x) => x.id === last.id) + 1 : arr.length
  arr.splice(idx, 0, item)
  links.value = arr
  await persistLinkOrder(gid)
}

// 数据监控模块：拖到另一模块上 = 插到其前
const tileDrag = reactive({ from: -1, over: -1 })
function tileStart(i, e) {
  tileDrag.from = i
  e.dataTransfer?.setData('text/plain', String(i))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}
function tileOver(i, e) {
  if (tileDrag.from < 0) return
  allowMove(e)
  tileDrag.over = i
}
async function tileDrop(i) {
  const f = tileDrag.from
  tileDrag.from = tileDrag.over = -1
  if (f < 0 || f === i) return
  const arr = [...tileCfg.value]
  const [m] = arr.splice(f, 1)
  arr.splice(f < i ? i - 1 : i, 0, m)
  tileCfg.value = arr
  await persistMetricOrder()
}

function moveLink(l, i, dir) {
  const arr = linksOf(l.group_id)
  const j = i + dir
  if (j < 0 || j >= arr.length) return
  const swapped = [...arr]
  ;[swapped[i], swapped[j]] = [swapped[j], swapped[i]]
  links.value = links.value.filter((x) => x.group_id !== l.group_id).concat(swapped)
  persistLinkOrder(l.group_id)
}

const linkDlg = ref(false)
const linkSaving = ref(false)
const editingLink = ref(null)
const linkForm = reactive({ name: '', url: '', icon: '' })
const linkGuard = useDirtyGuard(() => ({ ...linkForm }))

function openLinkDlg(l = null) {
  editingLink.value = l
  linkForm.name = l?.name || ''
  linkForm.url = l?.url || ''
  linkForm.icon = l?.icon || ''
  linkDlg.value = true
  nextTick(() => linkGuard.snapshot())
}

// 图标解析：填了图片地址直接用；留空或填的不是图片地址（页面链接/乱填）时
// 自动取目标站点根路径的 /favicon.ico；链接地址本身不合法则原样保留兜底
const IMG_EXT = /\.(png|jpe?g|gif|webp|svg|ico|bmp|avif)([?#]|$)/i

function resolveIcon(url, icon) {
  const iconTrim = (icon || '').trim()
  if (iconTrim && IMG_EXT.test(iconTrim)) return iconTrim
  try {
    return new URL(url).origin + '/favicon.ico'
  } catch {
    return iconTrim
  }
}

// 弹窗内实时预览：按保存时的同款规则解析
const previewIcon = computed(() => resolveIcon(linkForm.url, linkForm.icon))

async function saveLink() {
  const f = {
    name: linkForm.name.trim(),
    url: linkForm.url.trim(),
    icon: resolveIcon(linkForm.url.trim(), linkForm.icon),
  }
  if (!f.name) return ElMessage.warning(t('page.needLinkName'))
  if (!/^https?:\/\//.test(f.url)) return ElMessage.warning(t('page.needHttpUrl'))
  linkSaving.value = true
  try {
    if (editingLink.value) {
      await api.put(`/page/links/${editingLink.value.id}`, { group_id: editingLink.value.group_id, ...f })
      links.value = links.value.map((x) => (x.id === editingLink.value.id ? { ...x, ...f } : x))
    } else {
      const res = await api.post('/page/links', { group_id: selGrp.value, ...f })
      links.value = [...links.value, res.link]
    }
    linkDlg.value = false
  } catch (e) {
    ElMessage.error(t('page.saveFailed', { msg: e.message }))
  } finally {
    linkSaving.value = false
  }
}

async function delLink(l) {
  try {
    await ElMessageBox.confirm(t('page.delLinkConfirm', { name: l.name }), t('page.delLink'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.del(`/page/links/${l.id}`)
    links.value = links.value.filter((x) => x.id !== l.id)
  } catch (e) {
    ElMessage.error(t('page.delFailed', { msg: e.message }))
  }
}

// ---- 主页数据监控模块管理（与总览「数据监控」同款表单；数据源=服务器快照或用量账户快照） ----
// METRIC_LABELS 值为 i18n key（tileMetrics.*），渲染经 $t(m.metricLabel)
const METRIC_LABELS = new Map(TILE_METRICS.map((m) => [m.value, m.label]))
const metricRows = computed(() =>
  tileCfg.value.map((m) => ({
    ...m,
    metricLabel: METRIC_LABELS.get(m.metric) || m.metric,
    server_name: m.ai_id
      ? (aiAccounts.value.find((a) => a.id === m.ai_id)?.name || m.ai_name || t('tileViews.accDeleted'))
      : (servers.value.find((s) => s.id === m.server_id)?.name || m.server_name || t('tileViews.serverDeleted')),
  })))

const metricDlg = ref(false)
const metricSaving = ref(false)
const editingMetric = ref(null)
const metricForm = reactive({ src: 'server', server_id: null, ai_id: null, metric: '', param: '', label: '', link: '', icon: '' })
const metricGuard = useDirtyGuard(() => ({ ...metricForm }))

// 用量账户下拉（486）：账户名（服务商名）；指标下拉按所选账户服务商过滤
const metricAccOptions = computed(() =>
  aiAccounts.value.map((a) => ({ id: a.id, label: t('page.accOpt', { name: a.name, prov: AI_PROV_LABELS[a.provider] || a.provider }), provider: a.provider })))
const metricOptions = computed(() => {
  if (metricForm.src !== 'ai') return TILE_METRICS.filter((m) => !m.prov)
  const prov = aiAccounts.value.find((a) => a.id === metricForm.ai_id)?.provider
  return TILE_METRICS.filter((m) => m.prov && m.prov === prov)
})
function onMetricSrcChange() {
  metricForm.metric = ''
  metricForm.param = ''
}
function onMetricAccChange() {
  metricForm.metric = ''
}

const metricSel = computed(() => servers.value.find((s) => s.id === metricForm.server_id))
const metricParamKind = computed(() => TILE_METRICS.find((m) => m.value === metricForm.metric)?.kind || '')
const metricParamOptions = computed(() => {
  const latest = metricSel.value?.latest
  if (!latest) return []
  if (metricParamKind.value === 'disk') return (latest.disks || []).map((d) => d.mount)
  if (metricParamKind.value === 'net') return (latest.nets || []).map((n) => n.iface)
  if (metricParamKind.value === 'temp') return (latest.temps || []).map((x) => x.sensor)
  return []
})

function openMetricDlg(m = null) {
  editingMetric.value = m
  metricForm.src = m?.ai_id ? 'ai' : 'server'
  metricForm.server_id = m?.ai_id ? null : (m?.server_id ?? null)
  metricForm.ai_id = m?.ai_id || null
  metricForm.metric = m?.metric || ''
  metricForm.param = m?.param || ''
  metricForm.label = m?.label || ''
  metricForm.link = m?.link || ''
  metricForm.icon = m?.icon || ''
  metricDlg.value = true
  nextTick(() => metricGuard.snapshot())
}

// 弹窗内实时预览：icon 填了图片地址原样用，否则取跳转站点 favicon（与保存同款规则）
const previewMetricIcon = computed(() => resolveIcon(metricForm.link, metricForm.icon))

async function saveMetric() {
  const f = {
    server_id: metricForm.src === 'server' ? metricForm.server_id : 0,
    ai_id: metricForm.src === 'ai' ? metricForm.ai_id : 0,
    metric: metricForm.metric,
    param: metricForm.param,
    label: metricForm.label.trim(),
    link: metricForm.link.trim(),
    icon: resolveIcon(metricForm.link.trim(), metricForm.icon),
  }
  if (metricForm.src === 'ai' ? !f.ai_id : !f.server_id) return ElMessage.warning(metricForm.src === 'ai' ? t('page.needAcc') : t('page.needServer'))
  if (!f.metric) return ElMessage.warning(t('page.needMetric'))
  if (!f.label) return ElMessage.warning(t('page.needLabel'))
  if (f.link && !/^https?:\/\//.test(f.link)) return ElMessage.warning(t('page.needHttpLink'))
  metricSaving.value = true
  try {
    if (editingMetric.value) {
      await api.put(`/page/metrics/${editingMetric.value.id}`, f)
      tileCfg.value = tileCfg.value.map((x) => (x.id === editingMetric.value.id ? { ...x, ...f } : x))
    } else {
      const res = await api.post('/page/metrics', f)
      tileCfg.value = [...tileCfg.value, { ...res.metric, server_name: '', ai_name: '' }]
    }
    metricDlg.value = false
  } catch (e) {
    ElMessage.error(t('page.saveFailed', { msg: e.message }))
  } finally {
    metricSaving.value = false
  }
}

async function removeMetric(m) {
  try {
    await ElMessageBox.confirm(t('page.delMetricConfirm', { name: m.label }), t('page.delMetric'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.del(`/page/metrics/${m.id}`)
    tileCfg.value = tileCfg.value.filter((x) => x.id !== m.id)
  } catch (e) {
    ElMessage.error(t('page.delFailed', { msg: e.message }))
  }
}

async function persistMetricOrder() {
  await api.put('/page/metrics/order', { ids: tileCfg.value.map((m) => m.id) }).catch(() => ElMessage.error(t('page.orderSaveFailed')))
}

function moveMetric(i, dir) {
  const j = i + dir
  if (j < 0 || j >= tileCfg.value.length) return
  const arr = [...tileCfg.value]
  ;[arr[i], arr[j]] = [arr[j], arr[i]]
  tileCfg.value = arr
  persistMetricOrder()
}

// ---- 首页天气（Open-Meteo 免 key）：auto=服务端按访客 IP 定位 / manual=配置城市；5 分钟一拉（523，与服务端缓存 TTL 对齐） ----
// 上次成功结果落 localStorage，开页先用缓存占位再后台刷新原位纠正，胶囊随首屏一帧成型不迟到；
// 缓存按定位坐标记账（改城市不串显旧城市）、24h 过期（过期宁可贵一帧也不整天显示昨日天气）。
const WX_CACHE_KEY = 'pv-wx-cache'
const wxData = ref(null)
const wxAt = ref(0) // 当前展示数据的取数时刻（519）：拉取成功记当下，缓存占位记落库时间
let wxTimer = null

function readWxCache() {
  try {
    const c = JSON.parse(localStorage.getItem(WX_CACHE_KEY) || 'null')
    const k = `${cfg.weather_lat},${cfg.weather_lon}`
    return c && c.k === k && c.t && Date.now() - c.t < 86400000 && c.d && c.d.current ? c : null
  } catch {
    return null
  }
}

async function loadWx() {
  if (!cfg.weather_enabled || document.hidden) return
  try {
    const d = await api.get('/page/weather')
    wxData.value = d
    wxAt.value = Date.now()
    try { localStorage.setItem(WX_CACHE_KEY, JSON.stringify({ k: `${cfg.weather_lat},${cfg.weather_lon}`, t: Date.now(), d })) } catch {}
  } catch {
    // 失败保持现状：有缓存续显缓存（wxAt 不刷新，如实反映数据新旧），首拉无缓存维持 null 胶囊整体不显示
  }
}

function scheduleWx() {
  clearInterval(wxTimer)
  if (!cfg.weather_enabled) {
    wxData.value = null
    wxAt.value = 0
    return
  }
  if (!wxData.value) {
    const c = readWxCache() // 首帧占位（更新时间用缓存落库时刻），随后的 loadWx 原位刷新
    if (c) {
      wxData.value = c.d
      wxAt.value = c.t
    }
  }
  loadWx()
  wxTimer = setInterval(loadWx, 600000) // 56：全链路采集统一 10 分钟节拍（与服务端缓存 TTL/采样协程对齐）
}

// 「更新于」文案（519）：当天 HH:MM，跨天 M/D HH:MM（缓存 24h 过期，最多跨一天）
const wxUpd = computed(() => {
  if (!wx.value || !wxAt.value) return ''
  const d = new Date(wxAt.value)
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  if (d.toDateString() === new Date().toDateString()) return hm
  return `${d.getMonth() + 1}/${d.getDate()} ${hm}`
})

// 数据源中文名（519 同日补）：后端 provider 如实标注（含回落 openmeteo）；存量缓存无该字段按设置猜
const WX_SRC_NAME = { gaode: '高德', qweather: '和风', openmeteo: 'Open-Meteo', '': 'Open-Meteo' }
const wxSrcName = computed(() => WX_SRC_NAME[wxData.value?.provider ?? cfg.weather_provider] || '')

// ---- 天气历史（522）：弹层内历史视图，指标 + 区间两排胶囊，复用 SeriesChart ----
// name 存 i18n key（渲染经 $t/t，语言切换即时跟随）
const WX_METRICS = [
  { k: 'temp', name: 'page.temperature', unit: '°C' },
  { k: 'humidity', name: 'page.humidity', unit: '' }, // % 走 percent 刻度（tooltip 不重复拼 %）
  { k: 'wind', name: 'page.windSpeed', unit: 'km/h' },
  { k: 'pressure', name: 'page.pressure', unit: 'hPa' },
]
const WX_RANGES = [
  { h: 24, name: 'page.range24h' },
  { h: 168, name: 'page.range7d' },
  { h: 720, name: 'page.range30d' },
  { h: 2160, name: 'page.range90d' },
]
const wxTab = ref('now') // now=当前天气 / hist=历史曲线
const wxHistMetric = ref('temp')
const wxHistHours = ref(168)
const wxHistPoints = ref([])
const wxHistLoading = ref(false)
let wxHistSeq = 0 // 防竞态：慢响应不覆盖新查询

async function loadWxHist() {
  const seq = ++wxHistSeq
  wxHistLoading.value = true
  try {
    const d = await api.get(`/page/weather/history?hours=${wxHistHours.value}`)
    if (seq === wxHistSeq) wxHistPoints.value = d.points || []
  } catch {
    if (seq === wxHistSeq) wxHistPoints.value = []
  } finally {
    if (seq === wxHistSeq) wxHistLoading.value = false
  }
}

function openWxHist() {
  wxTab.value = 'hist'
  loadWxHist()
}

function setWxRange(h) {
  if (wxHistHours.value === h) return
  wxHistHours.value = h
  loadWxHist()
}

const wxHistName = computed(() => t((WX_METRICS.find((m) => m.k === wxHistMetric.value) || WX_METRICS[0]).name))
const wxHistUnit = computed(() => (WX_METRICS.find((m) => m.k === wxHistMetric.value) || WX_METRICS[0]).unit)
const wxHistSeries = computed(() => {
  const m = WX_METRICS.find((x) => x.k === wxHistMetric.value) || WX_METRICS[0]
  // 服务端已按区间/500 均桶，前端整段直喂（SeriesChart points 取毫秒 ts）
  return [{ name: t(m.name), points: wxHistPoints.value.map((p) => [p.ts * 1000, p[m.k]]) }]
})

const wx = computed(() => {
  const d = wxData.value
  if (!d || !d.current) return null
  // 详情 3×2 格（515）：日出/日落并一格凑 6 格
  const details = []
  if (d.current.humidity != null) details.push({ label: t('page.humidity'), value: `${d.current.humidity}%` })
  if (d.air) details.push({ label: t('page.detailAqi'), value: `${d.air.aqi}${d.air.category ? ' ' + d.air.category : ''}` })
  if (d.current.wind) {
    const dir = d.current.wind_dir || ''
    const dirTxt = dir ? (dir.endsWith('风') ? dir : dir + '风') + ' ' : '' // 风向串可能自带「风」字（东北风），防重复
    details.push({ label: t('page.detailWind'), value: `${dirTxt}${t('page.windLevelN', { n: windLevel(Math.round(d.current.wind)) })} · ${Math.round(d.current.wind)} km/h` })
  }
  // 气压不进详情格（2026-10-26 用户指定「天气指数去掉气压」）；历史曲线 tab 仍可看气压
  if (d.current.visibility) details.push({ label: t('page.detailVisibility'), value: `${Math.round(d.current.visibility * 10) / 10} km` })
  if (d.current.sunrise && d.current.sunset) details.push({ label: t('page.detailSun'), value: `${d.current.sunrise} / ${d.current.sunset}` })
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
  // 今日极端天气 chips（536 建、538 恒显）：降雨概率/降雪/大风——上游无概率
  //（高德）时按 WMO 码雨/雪族定性，全无时给「无降雨降雪」，不再整行隐藏
  const extremes = []
  if (daily.length) {
    const t0 = daily[0]
    if (t0.precip > 0) extremes.push({ label: t('page.xRainProb'), value: `${t0.precip}%` })
    else if (wxIsRainCode(t0.code)) extremes.push({ label: t('page.xRainfall'), value: t('page.willRain') })
    if (t0.snow || wxIsSnowCode(t0.code)) extremes.push({ label: t('page.xSnow'), value: t0.snow || t('page.willSnow') })
    if (t0.wind) extremes.push({ label: t('page.xWind'), value: t0.wind })
    if (!extremes.length) extremes.push({ label: t('page.xPrecip'), value: t('page.noRainSnow') })
  }
  // 温区条（515）：按周内最低/最高定标，单日区间过窄也保 6% 可见
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
  return {
    temp: Math.round(d.current.temp),
    city: d.city || t('page.unknown'),
    text: wmoText(d.current.code),
    icon: wmoIcon(d.current.code, d.current.is_day),
    feels: Math.round(d.current.feels),
    details,
    daily,
    extremes,
    minutely: d.minutely || '',
    warnings: d.warnings || [],
    indices: d.indices || [],
  }
})

// 蒲福氏风级/雨雪码/七日行文案等展示口径已抽 utils/weather.js（5 智能家居头部天气共用）

// ---- 服务区：服务集成卡片（Homepage widget 同口径，436-441 扩容 15 类） ----
// 值存 i18n key（page.svcTypes.*），渲染经 $t
const SVC_TYPE_LABELS = {
  qbittorrent: 'page.svcTypes.qbittorrent', emby: 'page.svcTypes.emby', homeassistant: 'page.svcTypes.homeassistant',
  adguard: 'page.svcTypes.adguard', pihole: 'page.svcTypes.pihole', transmission: 'page.svcTypes.transmission', deluge: 'page.svcTypes.deluge',
  aria2: 'page.svcTypes.aria2', navidrome: 'page.svcTypes.navidrome', nextcloud: 'page.svcTypes.nextcloud', proxmox: 'page.svcTypes.proxmox',
  docker: 'page.svcTypes.docker', portainer: 'page.svcTypes.portainer', dsm: 'page.svcTypes.dsm', truenas: 'page.svcTypes.truenas',
}

// 类型内置图标（白字形 + 品牌色渐变底，v-html 渲染）
const ICO_QB = `<svg viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="8.4"/><path d="M12 7.6v5.6"/><path d="m9.4 10.8 2.6 2.6 2.6-2.6"/></svg>`
const ICO_EMBY = `<svg viewBox="0 0 24 24" fill="#fff" stroke="none"><path d="M8.6 5.9v12.2c0 .8.9 1.3 1.6.9l9.5-6.1c.6-.4.6-1.4 0-1.8L10.2 5c-.7-.4-1.6.1-1.6.9Z"/></svg>`
const ICO_ADGUARD = `<svg viewBox="0 0 24 24" fill="#fff" stroke="none"><path d="M12 2.6 19.5 5.6v5.6c0 4.8-3.1 8.8-7.5 10.2-4.4-1.4-7.5-5.4-7.5-10.2V5.6L12 2.6z"/></svg>`
const ICO_PIHOLE = `<svg viewBox="0 0 24 24" fill="#fff" stroke="none" fill-rule="evenodd"><path d="M12 2.6 19.5 5.6v5.6c0 4.8-3.1 8.8-7.5 10.2-4.4-1.4-7.5-5.4-7.5-10.2V5.6L12 2.6zm0 6.2a3.4 3.4 0 1 0 0 6.8 3.4 3.4 0 0 0 0-6.8z"/></svg>`
const ICO_TRANS = `<svg viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3.5v10.5"/><path d="m7.5 10.5 4.5 4.5 4.5-4.5"/><path d="M4.5 19.5h15"/></svg>`
const ICO_DELUGE = `<svg viewBox="0 0 24 24" fill="#fff" stroke="none"><path d="M12 3c3.4 4.3 6.2 7.4 6.2 10.6a6.2 6.2 0 0 1-12.4 0C5.8 10.4 8.6 7.3 12 3z"/></svg>`
const ICO_ARIA = `<svg viewBox="0 0 24 24" fill="#fff" stroke="none"><path d="M13.4 2 5 13.6h5.2L9 22l8.6-11.6h-5.2L13.4 2z"/></svg>`
const ICO_NAV = `<svg viewBox="0 0 24 24" fill="#fff" stroke="none"><path d="M9.2 18.2V6l9.3-2.1v11.3"/><circle cx="6.7" cy="18.4" r="2.5"/><circle cx="16" cy="15.4" r="2.5"/></svg>`
const ICO_NC = `<svg viewBox="0 0 24 24" fill="#fff" stroke="none"><path d="M7.2 18.4a4.4 4.4 0 0 1-.7-8.7 5.6 5.6 0 0 1 10.9-1.2 4.6 4.6 0 0 1-.7 9.9H7.2z"/></svg>`
const ICO_PVE = `<svg viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2.8 19.6 7.2v9.6L12 21.2 4.4 16.8V7.2L12 2.8z"/><path d="m9.4 9.4 5.2 5.2m0-5.2-5.2 5.2"/></svg>`
const ICO_DOCKER = `<svg viewBox="0 0 24 24" fill="#fff" stroke="none"><path d="M3.5 13.2h2.7v2.7H3.5zM7.1 13.2h2.7v2.7H7.1zM10.7 13.2h2.7v2.7h-2.7zM14.3 13.2H17v2.7h-2.7zM7.1 9.6h2.7v2.7H7.1zM10.7 9.6h2.7v2.7h-2.7zM14.3 9.6H17v2.7h-2.7z"/><path d="M17.6 13.9c1.6-.3 2.9.2 3.6.9-1.9 2.4-4.8 3.4-8.6 3.4-3.4 0-6.4-1.2-8.1-3.6h2.4v-.7H3.6c-.4-.7-.7-1.5-.9-2.4l1.4-.3-1.5-.6c0-2.4 1.2-4 1.2-4s1.9 1.9 4.4 1.5c.2-2.5 1.8-3.8 1.8-3.8s1.5 1.7 1.2 4.1c2 .4 3.6 1.5 4.4 2.9h1z" opacity=".55"/></svg>`
const ICO_PORT = `<svg viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2.8 19.6 7.2v9.6L12 21.2 4.4 16.8V7.2L12 2.8z"/><path d="m4.4 7.2 7.6 4.4 7.6-4.4"/><path d="M12 11.6v9.6"/></svg>`
const ICO_DSM = `<svg viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="16" height="16" rx="3.5"/><circle cx="9.2" cy="10" r="1.1" fill="#fff" stroke="none"/><circle cx="14.8" cy="10" r="1.1" fill="#fff" stroke="none"/><path d="M8.8 15c1.9 1.4 4.5 1.4 6.4 0"/></svg>`
const ICO_TNAS = `<svg viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5.6" rx="7.4" ry="2.7"/><path d="M4.6 5.6v12.8c0 1.5 3.3 2.7 7.4 2.7s7.4-1.2 7.4-2.7V5.6"/><path d="M4.6 12c0 1.5 3.3 2.7 7.4 2.7s7.4-1.2 7.4-2.7"/></svg>`

// namePH 存 i18n key（page.svcNamePH.*），渲染经 svcNamePH() 的 t；urlPH 为字面 URL 原样用
const SVC_META = {
  qbittorrent: { ico: ICO_QB, bg: 'linear-gradient(135deg,#41a3e3,#2769ad)', namePH: 'page.svcNamePH.qbittorrent', urlPH: 'http://192.168.1.10:8080' },
  emby: { ico: ICO_EMBY, bg: 'linear-gradient(135deg,#5cb852,#357f2c)', namePH: 'page.svcNamePH.emby', urlPH: 'http://192.168.1.10:8096' },
  homeassistant: { ico: HA_BRAND_ICO, bg: 'linear-gradient(135deg,#38b5f2,#1b7fc4)', namePH: 'page.svcNamePH.homeassistant', urlPH: 'http://192.168.1.10:8123' },
  adguard: { ico: ICO_ADGUARD, bg: 'linear-gradient(135deg,#68bc71,#3d8a4c)', namePH: 'page.svcNamePH.adguard', urlPH: 'http://192.168.1.10:3000' },
  pihole: { ico: ICO_PIHOLE, bg: 'linear-gradient(135deg,#e05a44,#8e2117)', namePH: 'page.svcNamePH.pihole', urlPH: 'http://192.168.1.10/admin' },
  transmission: { ico: ICO_TRANS, bg: 'linear-gradient(135deg,#e04a42,#9c1f18)', namePH: 'page.svcNamePH.transmission', urlPH: 'http://192.168.1.10:9091' },
  deluge: { ico: ICO_DELUGE, bg: 'linear-gradient(135deg,#3f76c9,#25488c)', namePH: 'page.svcNamePH.deluge', urlPH: 'http://192.168.1.10:8112' },
  aria2: { ico: ICO_ARIA, bg: 'linear-gradient(135deg,#8b5cd6,#5c3aa3)', namePH: 'page.svcNamePH.aria2', urlPH: 'http://192.168.1.10:6800' },
  navidrome: { ico: ICO_NAV, bg: 'linear-gradient(135deg,#38a3e8,#1f6fb2)', namePH: 'page.svcNamePH.navidrome', urlPH: 'http://192.168.1.10:4533' },
  nextcloud: { ico: ICO_NC, bg: 'linear-gradient(135deg,#2f97e0,#0f6aa8)', namePH: 'page.svcNamePH.nextcloud', urlPH: 'https://cloud.example.com' },
  proxmox: { ico: ICO_PVE, bg: 'linear-gradient(135deg,#e57000,#a84e00)', namePH: 'page.svcNamePH.proxmox', urlPH: 'https://192.168.1.10:8006' },
  docker: { ico: ICO_DOCKER, bg: 'linear-gradient(135deg,#34a3ee,#1265a8)', namePH: 'page.svcNamePH.docker', urlPH: '' },
  portainer: { ico: ICO_PORT, bg: 'linear-gradient(135deg,#27c0f0,#0d8fbd)', namePH: 'page.svcNamePH.portainer', urlPH: 'http://192.168.1.10:9000' },
  dsm: { ico: ICO_DSM, bg: 'linear-gradient(135deg,#43b04a,#2b7a33)', namePH: 'page.svcNamePH.dsm', urlPH: 'http://192.168.1.10:5000' },
  truenas: { ico: ICO_TNAS, bg: 'linear-gradient(135deg,#21a4d9,#0e6f9c)', namePH: 'page.svcNamePH.truenas', urlPH: 'http://192.168.1.10' },
}

function svcTypeMeta(t) {
  return SVC_META[t] || { ico: '', bg: 'var(--el-fill-color)', namePH: '', urlPH: 'http://' }
}

// 名称占位（namePH 存 i18n key）：经 t 取文案，空 key 返回空串
function svcNamePH(type) {
  const ph = svcTypeMeta(type).namePH
  return ph ? t(ph) : ''
}

const svcData = ref([])
let svcTimer = null

async function loadSvc() {
  if (document.hidden) return
  try {
    const res = await api.get('/integrations/data')
    svcData.value = res.integrations || []
  } catch {
    /* 401 已由 api 层跳转 */
  }
}

function fmtSvcCount(v) {
  const n = Number(v)
  if (v == null || isNaN(n)) return '-'
  return String(Math.round(n))
}

// 四格速率（267）：单位简写（B/s→B、KB/s→K、MB/s→M、GB/s→G）+ 一位小数去 .0，
// 窄格不再截成「17.1 M...」
function svcRate(bps) {
  if (bps == null || isNaN(bps)) return '-'
  const short = ['B', 'K', 'M', 'G', 'T']
  let v = Number(bps)
  if (!isFinite(v)) return '-'
  if (v < 1024) return `${Math.round(v)}${short[0]}`
  let i = 0
  v /= 1024
  while (v >= 1024 && i < short.length - 1) {
    v /= 1024
    i++
  }
  const s = v >= 100 ? Math.round(v).toString() : v.toFixed(1).replace(/\.0$/, '')
  return `${s}${short[i + 1]}`
}

// 实体值+单位拼接：单字符单位（%/°）直接贴合，其余空格分隔；
// decimals 配置（268）在后端已四舍五入，这里只透传展示
function svcEntityValue(x) {
  const v = x.value ?? '-'
  const u = x.unit || ''
  if (!u) return String(v)
  return `${v}${u.length === 1 ? u : ' ' + u}`
}

// 扩容类型（436-441）的数值格辅助：百分比/毫秒/比例/天数/温度/负载，
// 缺数据一律 '-'，温度 ≤0 视为无传感器（DSM 上游给 -1）
function svcPct(v) {
  const n = Number(v)
  if (v == null || isNaN(n)) return '-'
  return `${(Math.round(n * 10) / 10).toString()}%`
}
function svcMs(v) {
  const n = Number(v)
  if (v == null || isNaN(n)) return '-'
  return `${n >= 100 ? Math.round(n) : (Math.round(n * 10) / 10).toString()}ms`
}
function svcRatio(a, b) {
  return `${fmtSvcCount(a)}/${fmtSvcCount(b)}`
}
function svcDays(v) {
  const n = Number(v)
  if (v == null || isNaN(n)) return '-'
  return t('page.daysN', { n: Math.round(n) })
}
function svcTemp(v) {
  const n = Number(v)
  if (v == null || isNaN(n) || n <= 0) return '-'
  return `${Math.round(n)}°`
}
function svcLoad(v) {
  const n = Number(v)
  if (v == null || isNaN(n)) return '-'
  return (Math.round(n * 100) / 100).toString()
}

// 卡片视图：qB/Emby 一张四格卡；HA 实体每 4 个分一张卡（名称·N 续卡）
const svcCards = computed(() => {
  const out = []
  for (const it of svcData.value) {
    if (!it.enabled) continue
    const meta = svcTypeMeta(it.type)
    const base = { id: it.id, url: it.open_url || it.url, off: !it.ok, error: it.error || '', ico: meta.ico, icoBg: meta.bg }
    if (it.type === 'homeassistant') {
      const items = it.data?.items || []
      const pages = Math.max(1, Math.ceil(items.length / 4))
      for (let i = 0; i < pages; i++) {
        out.push({
          ...base, key: `ha-${it.id}-${i}`,
          name: i ? `${it.name} · ${i + 1}` : it.name,
          cells: items.slice(i * 4, i * 4 + 4).map((x) => ({
            label: x.label || x.entity || '-',
            value: svcEntityValue(x),
          })),
          streams: [],
        })
      }
    } else if (it.type === 'qbittorrent') {
      const d = it.data || {}
      out.push({
        ...base, key: `qb-${it.id}`, name: it.name,
        cells: [
          { label: t('page.downloading'), value: fmtSvcCount(d.leech) },
          { label: t('page.dlRate'), value: svcRate(d.download_bps ?? null) },
          { label: t('page.seeding'), value: fmtSvcCount(d.seed) },
          { label: t('page.ulRate'), value: svcRate(d.upload_bps ?? null) },
        ],
        streams: [],
      })
    } else if (it.type === 'emby') {
      const d = it.data || {}
      out.push({
        ...base, key: `emby-${it.id}`, name: it.name,
        cells: [
          { label: t('page.movies'), value: fmtSvcCount(d.movies) },
          { label: t('page.series'), value: fmtSvcCount(d.series) },
          { label: t('page.episodes'), value: fmtSvcCount(d.episodes) },
          // 观看记录（2026-10-24 用户指定，原「已观看」）：全部用户看过的电影+剧集去重部数，点格子开播放记录弹层
          { label: t('page.watchRec'), value: d.watched != null ? t('page.itemsN', { n: fmtSvcCount(d.watched) }) : '-', watch: true },
        ],
        streams: (d.streams || []).slice(0, 2).map((s) => ({
          user: s.user || '-', title: s.title || '-', sub: s.sub || '',
          pct: Math.max(0, Math.min(Math.round(Number(s.pct) || 0), 100)),
        })),
      })
    } else if (it.type === 'adguard') {
      const d = it.data || {}
      out.push({
        ...base, key: `adg-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.queries'), value: fmtSvcCount(d.queries) },
          { label: t('page.blocked'), value: fmtSvcCount(d.blocked) },
          { label: t('page.blockRate'), value: svcPct(d.blocked_pct) },
          { label: t('page.avgLatency'), value: svcMs(d.avg_ms) },
        ],
      })
    } else if (it.type === 'pihole') {
      const d = it.data || {}
      out.push({
        ...base, key: `ph-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.queries'), value: fmtSvcCount(d.queries) },
          { label: t('page.blocked'), value: fmtSvcCount(d.blocked) },
          { label: t('page.blockRate'), value: svcPct(d.blocked_pct) },
          { label: t('page.gravity'), value: fmtSvcCount(d.gravity) },
        ],
      })
    } else if (it.type === 'transmission') {
      const d = it.data || {}
      out.push({
        ...base, key: `tr-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.active'), value: fmtSvcCount(d.active) },
          { label: t('page.dlRate'), value: svcRate(d.download_bps ?? null) },
          { label: t('page.paused'), value: fmtSvcCount(d.paused) },
          { label: t('page.ulRate'), value: svcRate(d.upload_bps ?? null) },
        ],
      })
    } else if (it.type === 'deluge') {
      const d = it.data || {}
      out.push({
        ...base, key: `dl-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.downloading'), value: fmtSvcCount(d.leech) },
          { label: t('page.dlRate'), value: svcRate(d.download_bps ?? null) },
          { label: t('page.seeding'), value: fmtSvcCount(d.seed) },
          { label: t('page.ulRate'), value: svcRate(d.upload_bps ?? null) },
        ],
      })
    } else if (it.type === 'aria2') {
      const d = it.data || {}
      out.push({
        ...base, key: `ar-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.active'), value: fmtSvcCount(d.active) },
          { label: t('page.dlRate'), value: svcRate(d.download_bps ?? null) },
          { label: t('page.waiting'), value: fmtSvcCount(d.waiting) },
          { label: t('page.ulRate'), value: svcRate(d.upload_bps ?? null) },
        ],
      })
    } else if (it.type === 'navidrome') {
      const d = it.data || {}
      out.push({
        ...base, key: `nv-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.artists'), value: fmtSvcCount(d.artists) },
          { label: t('page.albums'), value: fmtSvcCount(d.albums) },
          { label: t('page.songs'), value: fmtSvcCount(d.songs) },
          { label: t('page.version'), value: d.version || '-' },
        ],
      })
    } else if (it.type === 'nextcloud') {
      const d = it.data || {}
      out.push({
        ...base, key: `nc-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.users'), value: fmtSvcCount(d.users) },
          { label: t('page.files'), value: fmtSvcCount(d.files) },
          { label: t('page.shares'), value: fmtSvcCount(d.shares) },
          { label: t('page.version'), value: d.version || '-' },
        ],
      })
    } else if (it.type === 'proxmox') {
      const d = it.data || {}
      out.push({
        ...base, key: `pve-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.nodes'), value: svcRatio(d.nodes_up, d.nodes_total) },
          { label: t('page.vms'), value: fmtSvcCount(d.qemu_running) },
          { label: t('page.containers'), value: fmtSvcCount(d.lxc_running) },
          { label: t('page.running'), value: fmtSvcCount(d.vm_running) },
        ],
      })
    } else if (it.type === 'docker') {
      const d = it.data || {}
      out.push({
        ...base, key: `dk-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.running'), value: fmtSvcCount(d.running) },
          { label: t('page.stopped'), value: fmtSvcCount(d.stopped) },
          { label: t('page.images'), value: fmtSvcCount(d.images) },
          { label: t('page.version'), value: d.version || '-' },
        ],
      })
    } else if (it.type === 'portainer') {
      const d = it.data || {}
      out.push({
        ...base, key: `pt-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.env'), value: svcRatio(d.endpoints_up, d.endpoints_total) },
          { label: t('page.running'), value: fmtSvcCount(d.running) },
          { label: t('page.stopped'), value: fmtSvcCount(d.stopped) },
          { label: 'Stack', value: fmtSvcCount(d.stacks) },
        ],
      })
    } else if (it.type === 'dsm') {
      const d = it.data || {}
      out.push({
        ...base, key: `dsm-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: 'CPU', value: svcPct(d.cpu_pct) },
          { label: t('page.mem'), value: svcPct(d.mem_pct) },
          { label: t('page.temperature'), value: svcTemp(d.temp_c) },
          { label: t('page.ranFor'), value: svcDays(d.uptime_days) },
        ],
      })
    } else if (it.type === 'truenas') {
      const d = it.data || {}
      out.push({
        ...base, key: `tn-${it.id}`, name: it.name, streams: [],
        cells: [
          { label: t('page.version'), value: d.version || '-' },
          { label: t('page.pool'), value: svcRatio(d.pools_healthy, d.pools_total) },
          { label: t('page.ranFor'), value: svcDays(d.uptime_days) },
          { label: t('page.load'), value: svcLoad(d.load1) },
        ],
      })
    }
  }
  return out
})

function openSvcLink(e, card) {
  const u = safeUrl(card.url)
  if (!u) return
  if (e.metaKey || e.ctrlKey) {
    window.open(u, '_blank', 'noopener')
    return
  }
  window.location.href = u
}

// ---- Emby 播放记录（528/530）：服务卡「记录」弹层——清单（筛选+页码翻页）/统计 ----
const embyHistDlg = ref(false)
const embyView = ref('list')
const embyHistRows = ref([])
const embyHistLoading = ref(false)
const embyHistPage = ref(1)
const embyHistTotal = ref(0)
const embyHistUser = ref('')
const embyHistType = ref('')
const embyHistDates = ref(null) // [开始日, 结束日] 'YYYY-MM-DD'，null=不限（3）
const embyHistUsers = ref([])
const embyHistTypes = ref([])
const embyHistID = ref(0)
// EMBY_TYPES/EMBY_RANGES 值存 i18n key，渲染经 $t
const EMBY_TYPES = { Movie: 'page.embyTypes.Movie', Episode: 'page.embyTypes.Episode', Series: 'page.embyTypes.Series', Song: 'page.embyTypes.Song', Audio: 'page.embyTypes.Audio', Video: 'page.embyTypes.Video', MusicVideo: 'page.embyTypes.MusicVideo', TvChannel: 'page.embyTypes.TvChannel', Program: 'page.embyTypes.Program' }
const EMBY_RANGES = [
  { label: 'page.embyRange7', d: 7 },
  { label: 'page.embyRange30', d: 30 },
  { label: 'page.embyRange90', d: 90 },
  { label: 'page.embyRangeAll', d: 0 },
]
const embyStat = ref({ users: [], types: [], days: [], items: [] })
const embyStatLoading = ref(false)
const embyStatDays = ref(30)

function openEmbyHist(card) {
  embyHistID.value = card.id
  embyHistDlg.value = true
  embyView.value = 'list'
  embyHistUser.value = ''
  embyHistType.value = ''
  embyHistDates.value = null
  embyHistPage.value = 1
  loadEmbyHist()
  startEmbyHistTimer()
}
// 弹层开着且停在清单视图时每 30s 静默重拉当前页（534）：进行中行的进度条
// 跟着走；关弹层/切统计视图/离开页面即停
let embyHistTimer = null
function startEmbyHistTimer() {
  stopEmbyHistTimer()
  embyHistTimer = setInterval(() => {
    if (embyHistDlg.value && embyView.value === 'list' && !embyHistLoading.value) loadEmbyHist()
  }, 30000)
}
function stopEmbyHistTimer() {
  if (embyHistTimer) {
    clearInterval(embyHistTimer)
    embyHistTimer = null
  }
}
async function loadEmbyHist() {
  embyHistLoading.value = true
  try {
    const p = new URLSearchParams({ integration: embyHistID.value, limit: 50, offset: (embyHistPage.value - 1) * 50 })
    if (embyHistUser.value) p.set('user', embyHistUser.value)
    if (embyHistType.value) p.set('type', embyHistType.value)
    // 日期范围换本地时区 unix 秒（左闭右开：结束日 +1 天整天含入）
    if (Array.isArray(embyHistDates.value) && embyHistDates.value.length === 2) {
      const day0 = (s) => Math.floor(new Date(s + 'T00:00:00').getTime() / 1000)
      p.set('from', day0(embyHistDates.value[0]))
      p.set('to', day0(embyHistDates.value[1]) + 86400)
    }
    const d = await api.get(`/emby/history?${p}`)
    embyHistRows.value = d.events || []
    embyHistTotal.value = d.total || 0
    embyHistUsers.value = d.users || []
    embyHistTypes.value = d.types || []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    embyHistLoading.value = false
  }
}
function embyFilterChanged() {
  embyHistPage.value = 1
  loadEmbyHist()
}
function embyPageChanged(p) {
  embyHistPage.value = p
  loadEmbyHist()
}
function switchEmbyStats() {
  if (embyView.value !== 'stats') {
    embyView.value = 'stats'
    loadEmbyStats(embyStatDays.value)
  }
}
async function loadEmbyStats(days) {
  embyStatDays.value = days
  embyStatLoading.value = true
  try {
    const d = await api.get(`/emby/stats?integration=${embyHistID.value}&days=${days}`)
    embyStat.value = { users: d.users || [], types: d.types || [], days: d.days || [], items: d.items || [] }
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    embyStatLoading.value = false
  }
}
const maxUserPlays = computed(() => Math.max(1, ...embyStat.value.users.map((u) => u.plays)))
const maxTypePlays = computed(() => Math.max(1, ...embyStat.value.types.map((t) => t.plays)))
const embyStatEmpty = computed(() => !embyStat.value.users.length && !embyStat.value.days.length)
// 统计区标题：带/不带「近 N 天」的整句在语言包里成对维护（zh 括号收尾位置不同，不能拼接）
const embyUsersHead = computed(() => (embyStatDays.value ? t('page.byUsersDays', { n: embyStatDays.value }) : t('page.byUsers')))
const embyItemsHead = computed(() => (embyStatDays.value ? t('page.watchedItemsDays', { n: embyStatDays.value }) : t('page.watchedItems')))
// 每用户看过的剧影（1）：按用户排行序分组（接口无记录的用户跳过），组内已按最后播放新→旧
const embyItemsByUser = computed(() => {
  const map = new Map()
  for (const it of embyStat.value.items || []) {
    if (!map.has(it.user)) map.set(it.user, [])
    map.get(it.user).push(it)
  }
  return embyStat.value.users
    .filter((u) => map.has(u.user))
    .map((u) => ({ user: u.user, items: map.get(u.user) }))
})
const embyDayPlays = computed(() => embyStat.value.days.map((d) => [new Date(d.date + 'T00:00:00').getTime(), d.plays]))
const embyDayHours = computed(() => embyStat.value.days.map((d) => [new Date(d.date + 'T00:00:00').getTime(), Math.round((d.seconds / 3600) * 10) / 10]))
function statBarW(v, max) {
  return Math.max(3, Math.round((v / max) * 100))
}
function fmtEmbySecs(sec) {
  if (!sec) return t('page.minN', { n: 0 })
  const m = Math.round(sec / 60)
  if (m < 60) return t('page.minN', { n: m })
  return t('page.hourMin', { h: Math.floor(m / 60), m: m % 60 })
}
function fmtEmbyHours(v) {
  return t('page.hoursN', { n: v })
}
function embyTS(unix) {
  const d = new Date(unix * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function embyRange(e) {
  return e.ended_at ? `${embyTS(e.started_at)} – ${embyTS(e.ended_at)}` : t('page.since', { t: embyTS(e.started_at) })
}
function embyDur(e) {
  const end = e.ended_at || Math.floor(Date.now() / 1000)
  const mins = Math.max(0, Math.round((end - e.started_at) / 60))
  if (mins < 60) return t('page.minN', { n: mins })
  return t('page.hourMin', { h: Math.floor(mins / 60), m: mins % 60 })
}
function embyPct(e) {
  if (!e.runtime_ticks) return 0
  const pct = Math.max(0, Math.min(100, Math.round((e.pos_ticks / e.runtime_ticks) * 100)))
  // 展示口径（2）：≥92% 视为看完按 100% 展示，时长统计不受影响
  return pct >= 92 ? 100 : pct
}
function embySE(e) {
  const s = String(e.season || 0).padStart(2, '0')
  const ep = String(e.episode || 0).padStart(2, '0')
  return `S${s}E${ep}`
}

// ---- 管理抽屉「服务」tab：集成 CRUD ----
const intRows = ref([])
const svcDlg = ref(false)
const svcSaving = ref(false)
const svcTesting = ref(false)
const editingSvc = ref(null)
const SVC_TYPES = [
  { value: 'qbittorrent', label: 'page.svcTypes.qbittorrent' },
  { value: 'emby', label: 'page.svcTypes.emby' },
  { value: 'homeassistant', label: 'page.svcTypes.homeassistant' },
  { value: 'adguard', label: 'page.svcTypes.adguard' },
  { value: 'pihole', label: 'page.svcTypes.pihole' },
  { value: 'transmission', label: 'page.svcTypes.transmission' },
  { value: 'deluge', label: 'page.svcTypes.deluge' },
  { value: 'aria2', label: 'page.svcTypes.aria2' },
  { value: 'navidrome', label: 'page.svcTypes.navidrome' },
  { value: 'nextcloud', label: 'page.svcTypes.nextcloud' },
  { value: 'proxmox', label: 'page.svcTypes.proxmox' },
  { value: 'docker', label: 'page.svcTypes.docker' },
  { value: 'portainer', label: 'page.svcTypes.portainer' },
  { value: 'dsm', label: 'page.svcTypes.dsm' },
  { value: 'truenas', label: 'page.svcTypes.truenas' },
]
const svcForm = reactive({ type: 'qbittorrent', name: '', url: '', open_url: '', username: '', password: '', token: '', sock: '/var/run/docker.sock', insecure: false, now_playing: true, entities: [] })
const svcGuard = useDirtyGuard(() => ({ ...svcForm }))

async function loadInts() {
  try {
    const res = await api.get('/integrations')
    intRows.value = res.integrations || []
  } catch (e) {
    ElMessage.error(t('page.svcLoadFailed', { msg: e.message }))
  }
}

function onAdminTab(name) {
  if (name === 'services') loadInts()
}

function openSvcDlg(it = null) {
  editingSvc.value = it
  svcForm.type = it?.type || 'qbittorrent'
  svcForm.name = it?.name || ''
  svcForm.url = it?.url || ''
  svcForm.username = ''
  svcForm.password = ''
  svcForm.token = ''
  const c = it?.config && typeof it.config === 'object' ? it.config : {}
  svcForm.open_url = c.open_url || ''
  svcForm.insecure = !!c.insecure
  svcForm.now_playing = c.now_playing !== false
  svcForm.sock = c.sock || '/var/run/docker.sock'
  svcForm.entities = (c.entities || []).map((e) => ({ label: e.label || '', entity: e.entity || '', unit: e.unit || '', decimals: e.decimals ?? null }))
  svcDlg.value = true
  nextTick(() => svcGuard.snapshot())
}

function svcConfigPayload() {
  const openUrl = { open_url: svcForm.open_url.trim() } // 464：打开地址全类型通用
  if (svcForm.type === 'qbittorrent') return { insecure: svcForm.insecure, ...openUrl }
  if (svcForm.type === 'emby') return { now_playing: svcForm.now_playing, ...openUrl }
  if (svcForm.type === 'docker') return { sock: svcForm.sock.trim() || '/var/run/docker.sock', ...openUrl }
  if (svcForm.type === 'homeassistant') return { entities: svcForm.entities.filter((e) => e.entity.trim()), ...openUrl }
  return { insecure: svcForm.insecure, ...openUrl } // 其余扩容类型均支持自签跳过
}

function svcValidate() {
  if (!svcForm.name.trim()) {
    ElMessage.warning(t('page.needName'))
    return false
  }
  if (svcForm.type !== 'docker' && !/^https?:\/\//.test(svcForm.url.trim())) {
    ElMessage.warning(t('page.needHttpSvcUrl'))
    return false
  }
  if (svcForm.open_url.trim() && !/^https?:\/\//.test(svcForm.open_url.trim())) {
    ElMessage.warning(t('page.needHttpOpenUrl'))
    return false
  }
  if (!editingSvc.value) {
    const needUserPass = ['navidrome', 'nextcloud', 'dsm'].includes(svcForm.type)
    if (needUserPass && (!svcForm.username.trim() || !svcForm.password)) {
      ElMessage.warning(t('page.needUserPass'))
      return false
    }
    if (svcForm.type === 'qbittorrent' && !svcForm.token && (!svcForm.username.trim() || !svcForm.password)) {
      ElMessage.warning(t('page.needUserPassQb'))
      return false
    }
    if (svcForm.type === 'deluge' && !svcForm.password) {
      ElMessage.warning(t('page.needDelugePass'))
      return false
    }
    if ((svcForm.type === 'portainer' || svcForm.type === 'truenas') && !svcForm.token.trim()) {
      ElMessage.warning(t('page.needApiKey'))
      return false
    }
    if (svcForm.type === 'proxmox' && !svcForm.token.trim() && !svcForm.password) {
      ElMessage.warning(t('page.needPveAuth'))
      return false
    }
    if (svcForm.type === 'emby' && !svcForm.token.trim()) {
      ElMessage.warning(t('page.needApiKey'))
      return false
    }
    if (svcForm.type === 'homeassistant' && !svcForm.token.trim()) {
      ElMessage.warning(t('page.needHaToken'))
      return false
    }
    if (svcForm.type === 'homeassistant' && !svcForm.entities.some((e) => e.entity.trim())) {
      ElMessage.warning(t('page.needEntity'))
      return false
    }
  }
  return true
}

async function testSvc() {
  if (svcForm.type !== 'docker' && !/^https?:\/\//.test(svcForm.url.trim())) {
    return ElMessage.warning(t('page.needHttpSvcUrl'))
  }
  svcTesting.value = true
  try {
    await api.post('/integrations/test', {
      id: editingSvc.value?.id || null,
      type: svcForm.type,
      url: svcForm.url.trim(),
      auth: { username: svcForm.username, password: svcForm.password, token: svcForm.token },
      config: svcConfigPayload(),
    })
    ElMessage.success(t('page.connOk'))
  } catch (e) {
    ElMessage.error(t('page.connFailed', { msg: e.message }))
  } finally {
    svcTesting.value = false
  }
}

async function saveSvc() {
  if (!svcValidate()) return
  svcSaving.value = true
  try {
    const payload = {
      type: svcForm.type,
      name: svcForm.name.trim(),
      url: svcForm.url.trim(),
      enabled: true,
      config: svcConfigPayload(),
      auth: { username: svcForm.username, password: svcForm.password, token: svcForm.token },
    }
    if (editingSvc.value) {
      const res = await api.put(`/integrations/${editingSvc.value.id}`, payload)
      intRows.value = intRows.value.map((x) => (x.id === editingSvc.value.id ? res.integration : x))
    } else {
      const res = await api.post('/integrations', payload)
      intRows.value = [...intRows.value, res.integration]
    }
    svcDlg.value = false
    loadSvc() // 卡片即时拉新
  } catch (e) {
    ElMessage.error(t('page.saveFailed', { msg: e.message }))
  } finally {
    svcSaving.value = false
  }
}

async function delInt(it) {
  try {
    await ElMessageBox.confirm(t('page.delSvcConfirm', { name: it.name }), t('page.delService'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.del(`/integrations/${it.id}`)
    intRows.value = intRows.value.filter((x) => x.id !== it.id)
    loadSvc()
  } catch (e) {
    ElMessage.error(t('page.delFailed', { msg: e.message }))
  }
}

async function persistIntOrder() {
  await api.put('/integrations/order', { ids: intRows.value.map((x) => x.id) }).catch(() => ElMessage.error(t('page.orderSaveFailed')))
}

function moveInt(i, dir) {
  const j = i + dir
  if (j < 0 || j >= intRows.value.length) return
  const arr = [...intRows.value]
  ;[arr[i], arr[j]] = [arr[j], arr[i]]
  intRows.value = arr
  persistIntOrder()
}

async function testSaved(it) {
  try {
    await api.post('/integrations/test', { id: it.id, type: it.type, url: it.url, config: it.config || {} })
    ElMessage.success(t('page.testOkN', { name: it.name }))
  } catch (e) {
    ElMessage.error(t('page.testFailN', { name: it.name, msg: e.message }))
  }
}

// ---- 页面标题：随主页标题变化，离开恢复 ----
let savedTitle = ''
function applyTitle() {
  document.title = `${cfg.title || t('page.defaultTitle')} · PicoPanel`
}

onMounted(async () => {
  savedTitle = document.title
  tick()
  clockTimer = setInterval(tick, 1000)
  cdTimer = setInterval(loadCountdowns, 3600 * 1000)
  // 首屏并行拉齐配置/快照/服务集成/未读数/头像（原先串行：配置→快照→集成，服务卡片
  // 迟到约 1s 弹出顶得下方分组区跳动）；全部落定后同一帧放行渲染，整页一次成型。
  // 天气不进来（外部源延迟不可控）：scheduleWx 在 ready 置位同一 tick 先用缓存占位，
  // 胶囊随首屏一帧成型，后台刷新原位纠正
  loading.value = true
  try {
    await Promise.all([
      loadConfig(),
      loadServers(),
      loadSvc(),
      loadUnread(),
      loadStats(),
      loadCountdowns(),
      // /auth/me 已不背头像（6，data URL 曾让 me 请求 184KB/17s）：头像另走
      // /auth/avatar（private 短缓存 + ?v= 破缓存），失败沿用本地缓存/首字徽标
      api.get('/auth/me').then((res) => {
        me.username = res.username || ''
      }).catch(() => {}),
      api.get(`/auth/avatar?v=${getAvatarVer()}`).then((r) => {
        me.avatar = r.avatar || ''
        setAvatar(me.avatar)
      }).catch(() => { /* 头像拉取失败回落首字徽标 */ }),
    ])
  } catch {
    /* 401 已由 api 层跳转；配置失败时其余并行请求照常生效 */
  } finally {
    loading.value = false
    ready.value = true
  }
  scheduleServers()
  unreadTimer = setInterval(loadUnread, 30000)
  // 页签从后台切回时立即刷新角标（隐藏期间 30s 轮询在跳过，回来先补一发）
  document.addEventListener('visibilitychange', onVis)
  svcTimer = setInterval(loadSvc, 30000)
  scheduleWx() // 天气胶囊
  // 系统版本号（管理抽屉基础设置底部展示）：不进首屏 ready 门槛，失败静默
  //（version 在 /status 的 runtime 对象里，此前误读顶层字段导致一直不显示）
  api.get('/status').then((r) => { sysVer.value = r.runtime?.version || '' }).catch(() => {})
})

onUnmounted(() => {
  document.removeEventListener('visibilitychange', onVis)
  clearInterval(clockTimer)
  clearInterval(cdTimer)
  clearTimeout(serversTimer)
  clearInterval(unreadTimer)
  clearInterval(svcTimer)
  clearInterval(wxTimer)
  stopEmbyHistTimer()
  document.title = savedTitle
})

function barW(pct) {
  return `${Math.max(0, Math.min(pct || 0, 100))}%`
}
</script>

<style scoped>
/* ---- Sun-Panel 风格基底：深色渐变波浪背景 + 玻璃拟态卡片（独立页外观，不随应用主题） ---- */
.pv {
  position: relative;
  min-height: 100vh;
  color: #fff;
  overflow-x: hidden;
}

.pv {
  /* 41 批：壁纸/柔光斑改 --pv-* 变量驱动——缺省深紫（violet/白/暗），品牌暗色主题在文件尾整组覆盖 */
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, #2b4c9b 0%, transparent 55%),
    radial-gradient(130% 100% at 10% 92%, #b03b6f 0%, transparent 58%),
    radial-gradient(150% 120% at 55% 125%, #7a2e6e 0%, transparent 65%),
    linear-gradient(160deg, #101a3a 0%, #1b2350 45%, #3a2158 100%);
  --pv-w1: rgba(192, 74, 127, 0.5);
  --pv-w2: rgba(61, 99, 196, 0.5);
  --pv-w3: rgba(90, 58, 155, 0.5);
}
.pv-bg {
  position: fixed;
  inset: 0;
  z-index: 0;
  background: var(--pv-wall);
}

/* 氛围色块：原 filter: blur(70px) 的大色块在色块边界被 GPU 光栅化切出硬竖线
   （499，首页/智能家居右上同报）——改纯 radial-gradient 柔光，零滤镜无伪影，
   原 opacity 0.5 烙进渐变首停；closest-side = 椭圆四边都渐隐到 0，无截断边 */
.pv-bg i {
  position: absolute;
  border-radius: 50%;
}

.pv-bg .w1 { width: 62vw; height: 44vh; left: -12vw; bottom: -14vh; background: radial-gradient(closest-side, var(--pv-w1), transparent); }
.pv-bg .w2 { width: 48vw; height: 36vh; right: -10vw; top: -10vh; background: radial-gradient(closest-side, var(--pv-w2), transparent); }
.pv-bg .w3 { width: 36vw; height: 30vh; right: 18vw; bottom: -12vh; background: radial-gradient(closest-side, var(--pv-w3), transparent); }

.pv-body {
  position: relative;
  z-index: 1;
  max-width: 1200px;
  margin: 0 auto;
  padding: 3.5vh 24px 120px;
}

/* ---- 1. 标题区 ---- */
.pv-head { text-align: center; }

.pv-title-row {
  display: inline-flex;
  align-items: center;
  gap: 18px;
}

.pv-me {
  flex: none;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  overflow: hidden;
  border: 2px solid rgba(255, 255, 255, 0.35);
  box-shadow: 0 2px 10px rgba(0, 0, 0, 0.25);
  display: inline-flex;
}

.pv-me img { width: 100%; height: 100%; object-fit: cover; }

.pv-me-letter {
  width: 100%;
  height: 100%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 19px;
  font-weight: 800;
  color: #fff;
}

.pv-title {
  margin: 0;
  font-size: 33px;
  font-weight: 800;
  letter-spacing: 1px;
  max-width: 56vw;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pv-sep {
  font-size: 30px;
  font-weight: 200;
  opacity: 0.55;
}

.pv-clock { text-align: left; line-height: 1.15; }

.pv-time { font-size: 22px; font-weight: 700; font-variant-numeric: tabular-nums; }

.pv-date { font-size: 12px; opacity: 0.85; }

.pv-search {
  margin: 12px auto 0;
  display: flex;
  align-items: center;
  width: min(640px, 92%);
  height: 45px;
  border-radius: 22px;
  background: rgba(255, 255, 255, 0.1);
  border: 1px solid rgba(255, 255, 255, 0.18);
  backdrop-filter: blur(14px);
  padding: 0 6px 0 5px;
  transition: background 0.2s;
}

.pv-search:focus-within { background: rgba(255, 255, 255, 0.16); }

.pv-engine {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 12px;
  border: 0;
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.12);
  color: #fff;
  font-size: 13px;
  cursor: pointer;
  flex: none;
}

.pv-engine:hover { background: rgba(255, 255, 255, 0.2); }

/* 引擎标志（398 建，408 加大）：内置 SVG 替代彩色圆点 */
.pv-engine-ico {
  width: 18px;
  height: 18px;
  display: inline-flex;
}
.pv-engine-ico :deep(svg) { width: 100%; height: 100%; display: block; }

.pv-search-input {
  flex: 1;
  height: 100%;
  border: 0;
  outline: 0;
  background: transparent;
  color: #fff;
  font-size: 15px;
  padding: 0 14px;
}

.pv-search-input::placeholder { color: rgba(255, 255, 255, 0.55); }

.pv-search-go {
  flex: none;
  width: 34px;
  height: 34px;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: rgba(255, 255, 255, 0.85);
  cursor: pointer;
  font-size: 17px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.pv-search-go:hover { background: rgba(255, 255, 255, 0.16); }

:deep(.el-dropdown-menu .pv-eng-sel) { color: var(--el-color-primary); font-weight: 600; }

/* ---- 2. 监控区 ---- */
.pv-sec { margin-top: 25px; }

.pv-tiles {
  display: grid;
  /* 桌面默认一行 8 个（窄屏自动降列）；模块收窄、内部间隔降低 */
  grid-template-columns: repeat(8, 1fr);
  gap: 8px;
}

@media (max-width: 1439px) {
  .pv-tiles { grid-template-columns: repeat(auto-fill, minmax(128px, 1fr)); }
}

/* ---- 统计概览区（459/474）：卡内数据一行横排（图标+名称+数值+说明），
     卡片按内容宽度 flex-wrap 自动换行排列 ---- */
.pv-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
}

.pv-stat {
  display: flex;
  align-items: center;
  gap: 7px;
  max-width: 100%;
  padding: 9px 14px;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.13);
  backdrop-filter: blur(14px);
  color: #fff;
  text-align: left;
  /* 51：玻璃卡 hover 只过渡 transform（同 .pv-tile 口径），background 动画会
     逐帧重 raster 模糊层出竖向白带 */
  transition: transform 0.15s;
  will-change: transform;
}

.pv-stat.linked { cursor: pointer; }
.pv-stat.linked:hover { transform: translateY(-2px); background: rgba(255, 255, 255, 0.12); }

/* 倒数日卡（个人助理批）：玻璃卡同 .pv-stat 口径，内容按内容宽度换行排列 */
.pv-cd {
  display: inline-flex;
  flex-direction: column;
  gap: 6px;
  max-width: 100%;
  padding: 9px 14px 10px;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.13);
  backdrop-filter: blur(14px);
  color: #fff;
}
.pv-cd-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.pv-cd-name {
  font-size: 12px;
  opacity: 0.75;
}
.pv-cd-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 22px;
}
.pv-cd-item {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 13px;
  white-space: nowrap;
}
.pv-cd-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.pv-cd-n { opacity: 0.9; }
.pv-cd-days { font-size: 14px; }
.pv-cd-days.soon { color: #ffb4a8; }

.pv-stat-ico {
  width: 16px;
  height: 16px;
  flex: none;
  opacity: 0.85;
}

.pv-stat-ico :deep(svg) { width: 100%; height: 100%; }

.pv-stat-name {
  font-size: 13px;
  opacity: 0.75;
  white-space: nowrap;
}

.pv-stat-val {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.2;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.pv-stat-sub {
  font-size: 12px;
  opacity: 0.65;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.pv-stat-sub.pv-stat-bad { color: #ff9f8a; opacity: 1; }

/* 有注意事项的卡（未读/离线/逾期/到期）：名称旁红色圆点提示 */
.pv-stat.alert .pv-stat-name::after {
  content: '';
  display: inline-block;
  width: 6px;
  height: 6px;
  margin-left: 5px;
  border-radius: 50%;
  background: #ff6b4a;
  vertical-align: 1px;
}

.pv-tile {
  min-height: 92px;
  padding: 6px 9px;
  border-radius: 12px;
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.13);
  backdrop-filter: blur(14px);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: 3px;
  /* 51：backdrop-filter 玻璃卡 hover 只过渡 transform——background 动画会逐帧
     重 raster 模糊层，Chromium 合成出未同步的竖向 raster 带（白竖条闪烁）；
     变底瞬时生效 + will-change 提层让浮起动画走合成器不再重刷模糊 */
  transition: transform 0.15s;
  will-change: transform;
}

.pv-tile:hover { transform: translateY(-2px); background: rgba(255, 255, 255, 0.12); }
/* 拖拽排序反馈：拖动项半透明，悬停目标加品牌描边（链接/分组/磁贴通用口径） */
.pv-tile.dragging, .pv-link.dragging { opacity: 0.4; }
.pv-tile.drag-over { border-color: var(--brand, #4a90e2); }
.pv-link.drag-over { outline: 2px solid var(--brand, #4a90e2); outline-offset: -2px; }
.pv-grp.dragging { opacity: 0.5; }
.pv-grp.drag-over .pv-grp-bar { outline: 2px solid var(--brand, #4a90e2); outline-offset: -2px; }
.pv-grp-bar[draggable="true"] { cursor: grab; }

.pv-tile.linked { cursor: pointer; }

.pv-tile-label {
  font-size: 11px;
  line-height: 1.3;
  opacity: 0.85;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

/* 标签前小图标：图片原样；未配置回落首字彩徽标 */
.pv-tile-ico {
  flex: none;
  display: inline-flex;
  width: 14px;
  height: 14px;
  border-radius: 4px;
  overflow: hidden;
  vertical-align: -2.5px;
  margin-right: 5px;
}

.pv-tile-ico img { width: 100%; height: 100%; object-fit: cover; }

.pv-tile-ico-letter {
  width: 100%;
  height: 100%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 9px;
  font-weight: 800;
  color: #fff;
}

.pv-value {
  font-size: 17px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  padding: 1px 0;
}

.pv-value.u-warn, .pv-foot b.u-warn { color: #f5c451; }
.pv-value.u-crit, .pv-foot b.u-crit, .pv-cm-pct.u-crit { color: #ff7d68; }
.pv-cm-pct.u-warn { color: #f5c451; }

.pv-foot {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 6px;
  font-size: 11px;
  opacity: 0.85;
}
.pv-note {
  font-size: 11px;
  opacity: 0.7;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin: 2px 0 1px;
}

.pv-foot span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pv-foot b { font-size: 11.5px; flex: none; }

.pv-track {
  flex: 1;
  height: 8px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.16);
  overflow: hidden;
}

.pv-track-fill { height: 100%; border-radius: 4px; transition: width 0.4s; }

.pv-net { display: flex; flex-direction: column; gap: 3px; padding: 2px 0; }

.pv-net-row {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 12.5px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.pv-arr.up { color: #ff9f5a; }
.pv-arr.down { color: #5aa7ff; }

.pv-io-tag {
  flex: none;
  width: 18px;
  height: 18px;
  border-radius: 5px;
  font-size: 11px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-weight: 700;
}

.pv-io-tag.up { background: rgba(255, 159, 90, 0.22); color: #ff9f5a; }
.pv-io-tag.down { background: rgba(90, 167, 255, 0.22); color: #5aa7ff; }
.pv-io-tag.wide { width: auto; padding: 0 5px; }

.pv-drw { display: flex; gap: 12px; align-items: stretch; padding: 1px 0; }

.pv-drw-io { flex: 1; display: flex; flex-direction: column; gap: 5px; justify-content: center; }

.pv-vbar {
  flex: none;
  width: 8px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.16);
  display: flex;
  align-items: flex-end;
  overflow: hidden;
}

.pv-vbar-fill { width: 100%; border-radius: 4px; transition: height 0.4s; }

.pv-cm { display: flex; flex-direction: column; gap: 6px; justify-content: center; padding: 2px 0; }

.pv-cm-row { display: flex; align-items: center; gap: 8px; }

.pv-cm-name { flex: none; font-size: 12px; opacity: 0.85; width: 30px; }

.pv-cm-pct { flex: none; font-size: 12.5px; font-variant-numeric: tabular-nums; }

/* ---- 2.5 服务区：集成卡片（图标+名称+四格统计，玻璃口径同 .pv-grp） ---- */
.pv-svcs {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
}

.pv-svc {
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.13);
  backdrop-filter: blur(14px);
  padding: 13px 15px 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  /* 51：玻璃卡 hover 只过渡 transform（同 .pv-tile 口径） */
  transition: transform 0.15s;
  will-change: transform;
}

.pv-svc:hover { transform: translateY(-2px); background: rgba(255, 255, 255, 0.12); }

.pv-svc.linked { cursor: pointer; }

.pv-svc.off { opacity: 0.55; }

.pv-svc-head { display: flex; align-items: center; gap: 10px; min-width: 0; }

.pv-svc-hist:hover { background: rgba(255, 255, 255, 0.12); color: #cfe3ff; }
.pv-svc-hist .el-icon { font-size: 14px; }

.pv-svc-ico {
  flex: none;
  width: 29px;
  height: 29px;
  border-radius: 8px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.25);
}

.pv-svc-ico :deep(svg) { width: 18px; height: 18px; }

.pv-svc-name {
  flex: none; /* 名称永不 flex 收缩（2026-10-23 用户报图：Emby 被正在播放长文本挤成 E...）；超长自定义名 60% 兜底省略，挤压让给 .pv-svc-live */
  max-width: 60%;
  font-size: 15px;
  font-weight: 700;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pv-svc-badge {
  margin-left: auto;
  flex: none;
  font-size: 11px;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 9px;
  background: rgba(227, 77, 77, 0.85);
  color: #fff;
}

.pv-svc-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 6px;
}

.pv-svc-cell {
  background: rgba(255, 255, 255, 0.06);
  border-radius: 8px;
  padding: 8px 4px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  min-width: 0;
}
/* 已观看格可点开播放记录弹层（2026-10-23 用户指定，原头行胶片按钮已删） */
.pv-svc-cell.watch {
  cursor: pointer;
  transition: background 0.15s;
}
.pv-svc-cell.watch:hover {
  background: rgba(255, 255, 255, 0.12);
}

.pv-svc-cell b {
  font-size: 14px; /* 267：数值缩一档，窄格长值不截断 */
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  max-width: 100%;
  padding: 0 2px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.pv-svc-cell i {
  font-style: normal;
  font-size: 11px;
  opacity: 0.72;
  white-space: nowrap;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
}

.pv-svc-hint { font-size: 12.5px; opacity: 0.75; padding: 4px 2px 0; }

/* Emby 正在播放（266）：名称行右侧竖排两条，细字截断 */
.pv-svc-live {
  margin-left: auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
  align-items: flex-end;
}
.pv-svc-live-item {
  display: flex; /* 用户名固定 + 标题滚动区吃剩余宽（行内布局下 inline-block max-width:100% 会按整行解析、把用户名顶出省略点） */
  align-items: baseline;
  max-width: 100%;
  font-size: 11.5px;
  opacity: 0.92;
  white-space: nowrap;
  overflow: hidden;
}
.pv-svc-live-item :deep(.mq) {
  flex: 1 1 auto;
  min-width: 0;
}
.pv-svc-live-u { color: #8fc7ff; font-weight: 700; margin-right: 4px; }

/* Emby 播放记录弹层（528） */
.pv-emby-hist { display: flex; flex-direction: column; gap: 10px; max-height: 56vh; overflow-y: auto; padding-right: 4px; }
.pv-emby-row {
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.05);
  padding: 9px 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.pv-emby-row.live { box-shadow: inset 0 0 0 1px rgba(143, 199, 255, 0.35); }
.pv-emby-top { display: flex; align-items: center; gap: 8px; min-width: 0; }
.pv-emby-user { color: #8fc7ff; font-size: 13px; flex: none; }
.pv-emby-type { flex: none; font-size: 11px; padding: 1px 7px; border-radius: 8px; background: rgba(143, 199, 255, 0.14); color: #a9c8ea; }
.pv-emby-title { font-size: 13.5px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.pv-emby-live { margin-left: auto; flex: none; font-style: normal; font-size: 11.5px; color: #7dd8a8; display: inline-flex; align-items: center; gap: 5px; }
.pv-emby-live .dot { width: 6px; height: 6px; border-radius: 50%; background: #7dd8a8; box-shadow: 0 0 6px #7dd8a8; animation: pv-emby-pulse 1.6s infinite; }
@keyframes pv-emby-pulse { 50% { opacity: 0.35; } }
.pv-emby-meta { display: flex; gap: 12px; font-size: 11.5px; opacity: 0.68; flex-wrap: wrap; }
.pv-emby-dur { margin-left: auto; }
.pv-emby-bar { display: flex; align-items: center; gap: 8px; }
.pv-emby-track { flex: 1; height: 4px; border-radius: 3px; background: rgba(255, 255, 255, 0.1); overflow: hidden; }
.pv-emby-fill { height: 100%; border-radius: 3px; background: linear-gradient(90deg, #5b8dd9, #8fc7ff); }
.pv-emby-fill.on { background: linear-gradient(90deg, #3fa974, #7dd8a8); }
.pv-emby-pct { flex: none; font-size: 11px; opacity: 0.75; min-width: 30px; text-align: right; }
.pv-emby-empty { text-align: center; opacity: 0.6; font-size: 13px; padding: 26px 0; }

/* 弹层顶栏：清单/统计切换 + 筛选下拉/范围胶囊（530） */
.pv-emby-topbar { display: flex; align-items: center; gap: 10px; margin-bottom: 10px; flex-wrap: wrap; }
.pv-emby-views {
  display: inline-flex;
  background: rgba(255, 255, 255, 0.08);
  border-radius: 9px;
  padding: 2px;
}
.pv-emby-view {
  border: none;
  background: transparent;
  color: rgba(255, 255, 255, 0.65);
  font-size: 12.5px;
  padding: 4px 14px;
  border-radius: 7px;
  cursor: pointer;
}
.pv-emby-view.on { background: rgba(143, 199, 255, 0.25); color: #dceaff; font-weight: 600; }
.pv-emby-filter { width: 110px; }
.pv-emby-date.el-date-editor {
  width: 240px !important; /* el-range-editor 自带 flex-grow，不加会被顶栏拉伸（同 trp-win 惯例） */
  flex: none !important;
}
.pv-emby-ranges { display: flex; gap: 6px; margin-left: auto; flex-wrap: wrap; }
.pv-emby-range {
  border: 1px solid rgba(255, 255, 255, 0.16);
  background: transparent;
  color: rgba(255, 255, 255, 0.7);
  font-size: 12px;
  padding: 3px 10px;
  border-radius: 9px;
  cursor: pointer;
}
.pv-emby-range.on { background: rgba(143, 199, 255, 0.25); border-color: transparent; color: #dceaff; font-weight: 600; }
.pv-emby-pager { margin-top: 10px; justify-content: center; }

/* 统计视图（530）：排行条形 + 迷你折线 */
.pv-emby-stats { display: flex; flex-direction: column; gap: 14px; }
.pv-emby-sblock { display: flex; flex-direction: column; gap: 6px; }
.pv-emby-shead { font-size: 12.5px; opacity: 0.75; }
.pv-emby-srow { display: flex; align-items: center; gap: 10px; }
.pv-emby-sname { flex: none; width: 72px; font-size: 12.5px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.pv-emby-strack { flex: 1; height: 6px; border-radius: 4px; background: rgba(255, 255, 255, 0.08); overflow: hidden; }
.pv-emby-sfill { height: 100%; border-radius: 4px; background: linear-gradient(90deg, #5b8dd9, #8fc7ff); }
.pv-emby-sfill.type { background: linear-gradient(90deg, #3fa974, #7dd8a8); }
.pv-emby-sval { flex: none; font-size: 11.5px; opacity: 0.8; min-width: 120px; text-align: right; }

/* 每用户看过的剧影清单（1）：用户行 + 胶囊墙 */
.pv-emby-uitems { display: flex; flex-direction: column; gap: 5px; }
.pv-emby-uname { display: flex; align-items: baseline; gap: 8px; font-size: 12.5px; font-weight: 600; }
.pv-emby-ucount { font-size: 11px; font-weight: 400; opacity: 0.6; }
.pv-emby-items { display: flex; flex-wrap: wrap; gap: 5px; }
.pv-emby-item {
  display: inline-flex; align-items: center; gap: 5px;
  max-width: 100%;
  padding: 3px 9px 3px 4px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.07);
  font-size: 11.5px;
}
.pv-emby-item-tag { flex: none; font-style: normal; font-size: 10px; padding: 1px 5px; border-radius: 999px; background: rgba(255, 255, 255, 0.1); opacity: 0.85; }
.pv-emby-item-name { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.pv-emby-item-time { flex: none; opacity: 0.55; font-size: 10.5px; }

.pv-svc-now {
  display: flex;
  flex-direction: column;
  gap: 7px;
  border-top: 1px solid rgba(255, 255, 255, 0.1);
  padding-top: 9px;
}

.pv-svc-stream-t {
  font-size: 12px;
  opacity: 0.92;
  display: flex;
  gap: 6px;
  align-items: baseline;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pv-svc-stream-u { flex: none; color: #8fc7ff; font-weight: 700; }

/* ---- 天气胶囊（标题行内，玻璃胶囊同搜索框口径） ---- */
.pv-wx {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 40px;
  padding: 0 14px;
  border-radius: 20px;
  background: rgba(255, 255, 255, 0.1);
  border: 1px solid rgba(255, 255, 255, 0.18);
  backdrop-filter: blur(14px);
  color: #fff;
  cursor: pointer;
  font-size: 13px;
  flex: none;
  transition: background 0.2s;
}

.pv-wx:hover { background: rgba(255, 255, 255, 0.16); }

/* 点开弹层后胶囊持焦，UA 默认焦点描边在手机 WebView 上呈黑色粗环（538 用户
   报「黑边」实测即此）——玻璃胶囊口径下抑制，同搜索输入框 outline:0 先例 */
.pv-wx:focus,
.pv-wx:focus-visible {
  outline: 0;
}

.pv-wx-ico { display: inline-flex; width: 22px; height: 22px; color: #fff; }

.pv-wx-ico svg { width: 100%; height: 100%; }

.pv-wx-temp { font-size: 16px; font-weight: 700; font-variant-numeric: tabular-nums; }

.pv-wx-city {
  font-size: 12.5px;
  opacity: 0.85;
  max-width: 7em;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ---- 3. 链接区：分组手风琴（横向=同时展开一组 / 竖向=上下排列默认全展开） ---- */
.pv-acc {
  display: flex;
  gap: 12px;
  align-items: stretch;
  min-height: 190px;
}

/* 竖向模式：上下堆叠，组头横排全宽（名称+计数+箭头），点头部收起/展开 */
.pv-acc.vertical {
  flex-direction: column;
  min-height: 0;
  gap: 10px;
}

.pv-acc.vertical .pv-grp {
  width: 100%;
  flex: none !important;
}

.pv-acc.vertical .pv-grp-bar {
  writing-mode: horizontal-tb;
  min-height: 0;
  width: 100%;
  justify-content: flex-start;
  padding: 11px 16px;
}

.pv-acc.vertical .pv-grp-count { order: 3; }

.pv-grp-arrow {
  order: 4;
  margin-left: auto;
  transition: transform 0.2s;
  opacity: 0.7;
}

.pv-grp-arrow.open { transform: rotate(180deg); }

.pv-grp {
  border-radius: 16px;
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.13);
  backdrop-filter: blur(14px);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  transition: flex 0.25s, background 0.2s;
}

.pv-grp-bar {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  border: 0;
  background: transparent;
  color: #fff;
  cursor: pointer;
  padding: 12px 14px;
  writing-mode: vertical-rl;
  min-height: 150px;
  justify-content: flex-start;
}

/* 横向模式专属：收起组为竖排名条（竖向模式组全宽，不受这些约束） */
.pv-acc.horizontal .pv-grp.open { flex: 1; background: rgba(255, 255, 255, 0.1); }

.pv-acc.horizontal .pv-grp:not(.open) { flex: none; width: 52px; }

.pv-acc.horizontal .pv-grp:not(.open):hover { background: rgba(255, 255, 255, 0.13); }

.pv-acc.horizontal .pv-grp.open .pv-grp-bar {
  writing-mode: horizontal-tb;
  min-height: 0;
  padding: 13px 16px 9px;
}

.pv-acc.vertical .pv-grp-name { flex: 1; text-align: left; letter-spacing: normal; }

.pv-grp-name {
  font-size: 15px;
  font-weight: 700;
  letter-spacing: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 100%;
}

.pv-grp-count {
  font-size: 11.5px;
  opacity: 0.65;
  flex: none;
}

.pv-grp-body {
  flex: 1;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(84px, 1fr));
  gap: 10px 6px;
  align-content: start;
  padding: 4px 14px 16px;
  overflow-y: auto;
  max-height: 46vh;
}

.pv-grp:not(.open) .pv-grp-body { display: none; }

.pv-grp-empty {
  grid-column: 1 / -1;
  font-size: 12.5px;
  opacity: 0.6;
  padding: 8px 2px;
}

.pv-link {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 7px;
  text-decoration: none;
  color: #fff;
  padding: 8px 2px;
  border-radius: 12px;
  transition: background 0.15s, transform 0.15s;
}

.pv-link:hover { background: rgba(255, 255, 255, 0.1); transform: translateY(-2px); }

.pv-ico {
  position: relative;
  width: 46px;
  height: 46px;
  border-radius: 13px;
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.25);
}

.pv-ico-letter { font-size: 20px; font-weight: 800; color: #fff; }

.pv-ico img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

/* 低清小图：原尺寸居中不放大（inset:0 + margin:auto 居中），min 保底 26px 免得过小；
   浅底衬让透明底黑 logo 图标可见，并隐藏首字徽标（:has 覆盖内联 iconBg 底色须 !important） */
.pv-ico img.fit {
  width: auto;
  height: auto;
  max-width: calc(100% - 12px);
  max-height: calc(100% - 12px);
  min-width: 26px;
  min-height: 26px;
  margin: auto;
  object-fit: contain;
}

.pv-ico:has(> img.fit) { background: #eef0f4 !important; }

.pv-ico:has(> img.fit) .pv-ico-letter { display: none; }

.pv-link-name {
  font-size: 12.5px;
  max-width: 96px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ---- 右下角悬浮按钮 ---- */
.pv-fab {
  position: fixed;
  right: 18px;
  top: 18px;
  z-index: 5;
  display: flex;
  flex-direction: column; /* 竖排（395） */
  align-items: flex-end;
  gap: 8px;
}

.pv-fab-wrap {
  position: relative;
  display: inline-flex;
}

.pv-fab-badge {
  position: absolute;
  top: -7px;
  right: -8px;
  min-width: 19px;
  height: 19px;
  line-height: 19px;
  padding: 0 5px;
  border-radius: 10px;
  background: #e34d4d;
  color: #fff;
  font-size: 11.5px;
  font-weight: 700;
  text-align: center;
  box-sizing: border-box;
  box-shadow: 0 1px 5px rgba(0, 0, 0, 0.3);
  pointer-events: none;
}

/* 右下角版本号（11）：默认弱化展示，有更新时高亮可点跳在线更新 */
/* 管理抽屉里已隐藏的子项：整行弱化（仍可编辑/排序/删除） */
.adm-row-off {
  opacity: 0.45;
}
.adm-row-off .adm-link-text b::after {
  content: v-bind(hiddenSuffix);
  font-style: normal;
  font-size: 11px;
  color: var(--el-text-color-secondary, #909399);
  margin-left: 6px;
}
.pv-fab-btn {
  width: 34px;   /* 缩小 20%（395） */
  height: 34px;
  border-radius: 50%;
  border: 1px solid rgba(255, 255, 255, 0.2);
  background: rgba(255, 255, 255, 0.12);
  backdrop-filter: blur(14px);
  color: #fff;
  font-size: 15px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s;
}

.pv-fab-btn:hover { background: rgba(255, 255, 255, 0.22); }
/* HA 钮图标单独加大（419）：房子描线图 viewBox 留白多，同字号显小 */
.pv-fab-btn.ha { font-size: 19px; }

/* ---- 管理抽屉 ----
   抽屉默认渲染在 .pv 内部，会继承根节点的白色文字——白底抽屉里必须逐处显式给定主题色，否则加粗文字白字白底不可见 */
.pv-drawer-title { font-size: 16px; font-weight: 700; color: var(--el-text-color-primary); }

.pv-form { max-width: 100%; }

/* 天气服务商 Key 申请地址提示（贴在服务商下拉正下方） */
.pv-wx-api {
  margin: -12px 0 18px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-regular);
}

.pv-wx-api a { color: var(--el-color-primary); text-decoration: none; word-break: break-all; }

.pv-wx-api a:hover { text-decoration: underline; }

/* 基础设置底部：系统名称与当前版本号（面板底右角，右对齐通栏） */
.adm-sys {
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid var(--el-border-color-lighter);
  text-align: right;
  font-size: 12.5px;
  color: var(--el-text-color-secondary);
  letter-spacing: 0.3px;
}

.pv-form-row { display: flex; gap: 12px; }
.pv-form-hint { font-size: 12px; color: var(--el-text-color-secondary); line-height: 1.5; margin-top: 4px; }
.pv-form-row .el-form-item { flex: 1; }

.adm { display: flex; gap: 14px; min-height: 320px; color: var(--el-text-color-primary); }

.adm-side {
  flex: none;
  width: 190px;
  border-right: 1px solid var(--el-border-color);
  padding-right: 12px;
}

.adm-side-head { font-size: 13px; font-weight: 700; margin-bottom: 8px; opacity: 0.85; }

.adm-grp {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 7px 8px;
  border-radius: 8px;
  cursor: pointer;
  font-size: 13.5px;
}

.adm-grp:hover { background: var(--el-fill-color-light); }

/* 选中组：实底品牌色白字（全站 tab 胶囊同口径，任何主题下都清晰可辨） */
.adm-grp.sel { background: var(--el-color-primary); color: #fff; font-weight: 600; }

.adm-grp.sel .adm-grp-name { color: #fff; }

.adm-grp.sel .adm-ops { color: rgba(255, 255, 255, 0.85); }

.adm-grp-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--el-text-color-primary);
}

/* 行内操作钮：默认收起悬停出现（触屏设备常显），避免挤爆窄行 */
.adm-ops {
  display: inline-flex;
  gap: 5px;
  font-size: 13px;
  color: var(--el-text-color-regular);
  flex: none;
  margin-left: auto;
  opacity: 0;
  transition: opacity 0.15s;
}

.adm-grp:hover .adm-ops,
.adm-link:hover .adm-ops { opacity: 1; }

@media (hover: none) {
  .adm-ops { opacity: 1; }
}

.adm-ops .el-icon { cursor: pointer; }

.adm-ops .el-icon:hover { color: var(--el-color-primary); }

.adm-ops .el-icon.off { opacity: 0.3; cursor: default; }

.adm-ops .el-icon.danger:hover { color: var(--el-color-danger); }

.adm-add { margin-top: 6px; }

.adm-main { flex: 1; min-width: 0; }

.adm-main-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.adm-main-title { font-size: 14px; font-weight: 700; color: var(--el-text-color-primary); }

.adm-link {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 6px;
  border-radius: 8px;
}

.adm-link:hover { background: var(--el-fill-color-light); }

.adm-ico {
  position: relative;
  flex: none;
  width: 30px;
  height: 30px;
  border-radius: 8px;
  overflow: hidden;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-size: 14px;
  font-weight: 700;
}

.adm-ico img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }

.adm-link-text { flex: 1; min-width: 0; display: flex; flex-direction: column; line-height: 1.35; }

.adm-link-text b { font-size: 13.5px; color: var(--el-text-color-primary); }

.adm-link-text i {
  font-style: normal;
  font-size: 12px;
  color: var(--el-text-color-regular);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.adm-empty { font-size: 13px; color: var(--el-text-color-regular); padding: 18px 4px; }

/* 数据监控模块管理行（同链接行布局） */
.adm-metric-text { flex: 1; min-width: 0; display: flex; flex-direction: column; line-height: 1.35; }

.adm-metric-text b { font-size: 13.5px; color: var(--el-text-color-primary); }

.adm-metric-text i {
  font-style: normal;
  font-size: 12px;
  color: var(--el-text-color-regular);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pv-icon-row { display: flex; align-items: center; gap: 10px; width: 100%; }

/* ---- 管理抽屉「服务」tab ---- */
.adm-services { min-height: 240px; color: var(--el-text-color-primary); }

.adm-svc-flag {
  font-style: normal;
  font-weight: 400;
  font-size: 11.5px;
  color: var(--el-text-color-secondary);
  margin-left: 6px;
}

.adm-svc-state {
  flex: none;
  font-size: 11.5px;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 9px;
  background: var(--el-fill-color);
  color: var(--el-text-color-secondary);
}

.adm-svc-state.good { background: var(--el-color-success-light-9); color: var(--el-color-success); }

.adm-svc-state.bad { background: var(--el-color-danger-light-9); color: var(--el-color-danger); }

/* 类型图标（v-html 的 svg 无 scope 属性，:deep 穿透） */
.adm-ico :deep(svg) { width: 18px; height: 18px; }

.svc-row { display: flex; gap: 8px; width: 100%; }

.svc-ents { width: 100%; display: flex; flex-direction: column; gap: 6px; }

.svc-ent { display: flex; gap: 6px; align-items: center; }

.svc-ent .el-input { flex: 1; }

.svc-ent .svc-ent-unit { flex: none; width: 76px; }
.svc-ent .svc-ent-dec { flex: none; width: 64px; } /* 小数位数（0-4，可空=原样，268） */

.svc-ent-del { flex: none; cursor: pointer; color: var(--el-text-color-secondary); }

.svc-ent-del:hover { color: var(--el-color-danger); }

/* ---- 手机端 ---- */
@media (max-width: 768px) {
  .pv-body { padding: 3vh 12px 110px; }

  .pv-title { font-size: 22px; max-width: 44vw; }

  .pv-sep { font-size: 20px; }

  .pv-time { font-size: 17px; }

  .pv-date { font-size: 11px; }

  .pv-search { margin-top: 12px; height: 42px; }

  .pv-tiles { grid-template-columns: repeat(4, 1fr); gap: 9px; }

  .pv-acc.horizontal {
    overflow-x: auto;
    padding-bottom: 6px;
  }

  .pv-acc.horizontal .pv-grp:not(.open) { width: 44px; }

  .pv-acc.horizontal .pv-grp.open { min-width: 78%; }

  .pv-grp-body { max-height: none; }

  .pv-fab { right: 10px; top: 10px; gap: 6px; }

  .pv-fab-btn { width: 26px; height: 26px; font-size: 11px; } /* 缩小 20% 同口径（395） */
  .pv-fab-btn.ha { font-size: 14px; }

  .pv-fab-badge {
    top: -6px;
    right: -6px;
    min-width: 16px;
    height: 16px;
    line-height: 16px;
    padding: 0 4px;
    border-radius: 8px;
    font-size: 10px;
  }

  /* 标题行右侧让出按钮位（inline 布局 margin 计入居中，行整体左移），时间不被右上角按钮遮挡；
     395 按钮竖排后收窄避让宽 */
  .pv-title-row { margin-right: 44px; gap: 10px; }

  /* 时钟日期不折行（天气胶囊加入后 390px 行宽紧张） */
  .pv-date { white-space: nowrap; }

  /* 天气胶囊收窄：手机端只留 图标+温度（城市名隐藏） */
  .pv-wx { height: 34px; padding: 0 10px; gap: 6px; }

  .pv-wx-temp { font-size: 14px; }

  .pv-wx-ico { width: 18px; height: 18px; }

  .pv-wx-city { display: none; }

  /* 服务卡片单列铺满；四格值字号收一档防速率文本溢出 */
  .pv-svcs { grid-template-columns: 1fr; gap: 9px; }

  .pv-svc { padding: 11px 12px 12px; }

  .pv-svc-cell b { font-size: 13px; }
}

/* 最小屏（≤480px 手机）：数据监控磁贴一行放宽到最少 3 个（与后台总览同口径） */
@media (max-width: 480px) {
  .pv-tiles { grid-template-columns: repeat(3, 1fr); }
}

/* 品牌暗色主题（38 gold + 41 八套）：后台选中时主页换同系壁纸/柔光斑，白透玻璃
   件不动（白玻璃在各色底上观感一致）；紫罗兰仍是缺省外观 */
.pv.gold {
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(118, 92, 28, 0.45) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(138, 72, 32, 0.4) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(108, 80, 30, 0.4) 0%, transparent 65%),
linear-gradient(160deg, #0b0803 0%, #140f07 45%, #1d1608 100%);
  --pv-w1: rgba(212, 175, 55, 0.3);
  --pv-w2: rgba(232, 201, 106, 0.22);
  --pv-w3: rgba(166, 120, 48, 0.3);
}
.pv.teal {
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(20, 118, 92, 0.55) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(17, 104, 96, 0.5) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(13, 82, 62, 0.55) 0%, transparent 65%),
linear-gradient(160deg, #04100d 0%, #07211a 45%, #0b2d22 100%);
  --pv-w1: rgba(46, 196, 155, 0.4);
  --pv-w2: rgba(38, 152, 168, 0.38);
  --pv-w3: rgba(22, 116, 92, 0.42);
}
.pv.rose {
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(122, 32, 58, 0.55) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(134, 38, 66, 0.5) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(92, 26, 46, 0.55) 0%, transparent 65%),
linear-gradient(160deg, #150810 0%, #230d16 45%, #2f121a 100%);
  --pv-w1: rgba(224, 86, 111, 0.34);
  --pv-w2: rgba(158, 52, 96, 0.34);
  --pv-w3: rgba(118, 38, 66, 0.4);
}
.pv.glacier {
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(30, 98, 150, 0.5) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(26, 86, 124, 0.45) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(19, 64, 98, 0.5) 0%, transparent 65%),
linear-gradient(160deg, #060b12 0%, #0a1520 45%, #0e1f2e 100%);
  --pv-w1: rgba(86, 170, 224, 0.36);
  --pv-w2: rgba(56, 130, 180, 0.34);
  --pv-w3: rgba(36, 94, 138, 0.4);
}
.pv.ember {
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(142, 62, 24, 0.45) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(122, 47, 20, 0.4) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(92, 37, 16, 0.45) 0%, transparent 65%),
linear-gradient(160deg, #0d0c0b 0%, #131010 45%, #191411 100%);
  --pv-w1: rgba(232, 129, 79, 0.32);
  --pv-w2: rgba(190, 84, 40, 0.3);
  --pv-w3: rgba(142, 57, 25, 0.36);
}
.pv.moss {
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(98, 140, 44, 0.45) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(80, 118, 40, 0.4) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(58, 92, 30, 0.45) 0%, transparent 65%),
linear-gradient(160deg, #0a0d06 0%, #10140a 45%, #161c0e 100%);
  --pv-w1: rgba(148, 195, 86, 0.34);
  --pv-w2: rgba(108, 150, 56, 0.32);
  --pv-w3: rgba(76, 112, 40, 0.38);
}
.pv.graphite {
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(74, 96, 118, 0.35) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(60, 78, 96, 0.32) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(44, 58, 74, 0.35) 0%, transparent 65%),
linear-gradient(160deg, #0b0d0f 0%, #111417 45%, #16191d 100%);
  --pv-w1: rgba(140, 165, 190, 0.22);
  --pv-w2: rgba(105, 130, 155, 0.2);
  --pv-w3: rgba(75, 95, 115, 0.24);
}
.pv.neon {
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(130, 44, 118, 0.5) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(110, 36, 102, 0.45) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(82, 28, 78, 0.5) 0%, transparent 65%),
linear-gradient(160deg, #110811 0%, #1a0c1a 45%, #241124 100%);
  --pv-w1: rgba(217, 91, 184, 0.34);
  --pv-w2: rgba(168, 62, 150, 0.32);
  --pv-w3: rgba(120, 44, 108, 0.38);
}
.pv.abyss {
  --pv-wall:
    radial-gradient(120% 90% at 88% 8%, rgba(24, 62, 86, 0.38) 0%, transparent 55%),
radial-gradient(130% 100% at 10% 92%, rgba(18, 54, 64, 0.34) 0%, transparent 58%),
radial-gradient(150% 120% at 55% 125%, rgba(14, 42, 58, 0.4) 0%, transparent 65%),
linear-gradient(160deg, #05070a 0%, #070d12 45%, #0a1218 100%);
  --pv-w1: rgba(63, 143, 163, 0.28);
  --pv-w2: rgba(40, 100, 120, 0.26);
  --pv-w3: rgba(24, 66, 84, 0.32);
}
</style>
