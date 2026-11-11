<template>
  <!-- 用药提醒（2026-11-02，参考 Apple Health 药物，分家庭成员多账号）：
       顶部人员筛选 + 7 天概览条；今日剂量时间轴（按时刻分组，服用/跳过/撤销打卡）；
       按需药记一笔；药物列表（点行开编辑抽屉，归档折叠）；成员管理弹层与历史弹层。
       弹层一律 append-to-body（玻璃 .main 磨砂 fixed containing block 约定），
       抽屉内 popper 全部 :teleported="false"（517 教训）。 -->
  <div class="med-page">
    <div class="med-toolbar">
      <div class="med-persons">
        <button type="button" class="med-chip" :class="{ on: !personFilter }" @click="setPerson(0)">
          {{ $t('med.personAll') }}
        </button>
        <button
          v-for="p in persons"
          :key="p.id"
          type="button"
          class="med-chip"
          :class="{ on: personFilter === p.id }"
          @click="setPerson(p.id)"
        ><i class="med-dot" :style="{ background: p.color || '#909399' }" />{{ p.name }}</button>
      </div>
      <div class="med-actions">
        <el-button v-if="isAdmin" size="small" :icon="User" @click="personsOpen = true">{{ $t('med.managePersons') }}</el-button>
        <el-button size="small" :icon="CalendarIcon" @click="openHistory">{{ $t('med.history') }}</el-button>
        <el-button size="small" type="primary" :icon="Plus" :disabled="!persons.length" @click="openDrawer(0)">
          {{ $t('med.addMed') }}
        </el-button>
      </div>
    </div>

    <!-- 7 天概览条：绿=全服 / 黄=部分 / 红=有漏 / 灰=无计划 -->
    <div v-if="persons.length && meds.length" class="med-strip">
      <button
        v-for="d in strip"
        :key="d.day"
        type="button"
        class="med-strip-day"
        :class="[d.level, { sel: d.day === selDate }]"
        :title="`${d.day} · ${$t('med.' + d.levelKey)}`"
        @click="selDate = d.day; loadDay()"
      >
        <i>{{ d.label }}</i>
        <span class="med-strip-dots"><i v-for="k in d.total" :key="k" :class="{ done: k <= d.taken }" /></span>
      </button>
    </div>

    <!-- 今日（选中日）剂量时间轴 -->
    <section class="med-sec">
      <div class="med-sec-head">
        <h3>{{ dayTitle }}</h3>
        <el-button-group size="small">
          <el-button :icon="ArrowLeft" @click="shiftDay(-1)" />
          <el-button @click="goToday">{{ $t('med.todayIntake') }}</el-button>
          <el-button :icon="ArrowRight" @click="shiftDay(1)" />
        </el-button-group>
      </div>
      <div v-if="!persons.length" class="med-empty">{{ $t('med.emptyPersons') }}</div>
      <div v-else-if="!doses.length" class="med-empty">{{ $t('med.emptyDoses') }}</div>
      <div v-loading="dayLoading" class="med-timeline">
        <div v-for="g in doseGroups" :key="g.time" class="med-slot">
          <div class="med-slot-time">{{ g.time }}</div>
          <div class="med-slot-rows">
            <div v-for="r in g.rows" :key="r.id" class="med-dose" :class="{ done: r.status === 'taken', off: r.status === 'skipped' }">
              <span class="med-ico" :style="{ '--c': FORM_COLORS[r.form] || '#909399' }" v-html="FORM_ICOS[r.form] || FORM_ICOS.tablet" />
              <div class="med-dose-main">
                <div class="med-dose-name">
                  <i class="med-dot" :style="{ background: r.person_color || '#909399' }" />
                  <b>{{ r.med_name }}</b>
                  <span class="med-dose-dose">{{ fmtDose(r) }}</span>
                  <span class="med-dose-person">{{ r.person_name }}</span>
                </div>
                <div v-if="r.note" class="med-dose-note">{{ r.note }}</div>
              </div>
              <div class="med-dose-act">
                <template v-if="r.status === 'pending'">
                  <span v-if="r.time > nowHHMM" class="med-tag gray">{{ $t('med.upcoming') }}</span>
                  <el-button size="small" type="primary" @click="act(r, 'taken')">{{ $t('med.take') }}</el-button>
                  <el-button size="small" @click="act(r, 'skipped')">{{ $t('med.skip') }}</el-button>
                </template>
                <template v-else-if="r.status === 'taken'">
                  <span class="med-tag green">✓ {{ $t('med.taken') }}</span>
                  <el-button size="small" text @click="act(r, 'pending')">{{ $t('med.undo') }}</el-button>
                </template>
                <template v-else-if="r.status === 'skipped'">
                  <span class="med-tag gray">{{ $t('med.skipped') }}</span>
                  <el-button size="small" text @click="act(r, 'pending')">{{ $t('med.undo') }}</el-button>
                </template>
                <template v-else>
                  <span class="med-tag red">{{ $t('med.missed') }}</span>
                  <el-button size="small" text type="primary" @click="act(r, 'taken')">{{ $t('med.take') }}</el-button>
                </template>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>

    <!-- 按需药 -->
    <section v-if="prnMeds.length" class="med-sec">
      <div class="med-sec-head"><h3>{{ $t('med.prnTitle') }}</h3></div>
      <div class="med-prn">
        <div v-for="m in prnMeds" :key="m.id" class="med-dose">
          <span class="med-ico" :style="{ '--c': FORM_COLORS[m.form] || '#909399' }" v-html="FORM_ICOS[m.form] || FORM_ICOS.tablet" />
          <div class="med-dose-main">
            <div class="med-dose-name">
              <i class="med-dot" :style="{ background: m.person_color || '#909399' }" />
              <b>{{ m.name }}</b>
              <span class="med-dose-dose">{{ fmtDose(m) }}</span>
              <span class="med-dose-person">{{ m.person_name }}</span>
            </div>
          </div>
          <el-button size="small" @click="prnLog(m)">{{ $t('med.prnRecord') }}</el-button>
        </div>
      </div>
    </section>

    <!-- 药物列表 -->
    <section class="med-sec">
      <div class="med-sec-head"><h3>{{ $t('med.medList') }}</h3></div>
      <div v-if="!persons.length" class="med-empty">{{ $t('med.emptyPersons') }}</div>
      <div v-else-if="!meds.length" class="med-empty">{{ $t('med.emptyMeds') }}</div>
      <div v-else class="med-list">
        <div v-for="m in meds" :key="m.id" class="med-row" @click="openDrawer(m.id)">
          <span class="med-ico" :style="{ '--c': FORM_COLORS[m.form] || '#909399' }" v-html="FORM_ICOS[m.form] || FORM_ICOS.tablet" />
          <div class="med-row-main">
            <div class="med-row-name">
              <i class="med-dot" :style="{ background: m.person_color || '#909399' }" />
              <b>{{ m.name }}</b>
              <span v-if="medEnded(m)" class="med-tag gray">{{ $t('med.endedTag') }}</span>
              <span v-if="stockLow(m)" class="med-tag red">{{ $t('med.stockLine', { qty: fmtNum(m.stock_qty), alert: fmtNum(m.stock_alert) }) }}</span>
            </div>
            <div class="med-row-sub">
              {{ schedText(m) }}
              <template v-if="m.stock_qty != null"> · {{ m.stock_qty != null ? $t('med.stockLine', { qty: fmtNum(m.stock_qty), alert: fmtNum(m.stock_alert) }) : $t('med.stockOff') }}</template>
              <template v-if="m.note"> · {{ m.note }}</template>
            </div>
          </div>
          <el-icon class="med-row-go"><ArrowRight /></el-icon>
        </div>
      </div>
      <div v-if="archivedMeds.length" class="med-arch">
        <button type="button" class="med-arch-bar" @click="archOpen = !archOpen">
          {{ $t('med.archivedFold', { n: archivedMeds.length }) }}
          <el-icon><component :is="archOpen ? ArrowUp : ArrowDown" /></el-icon>
        </button>
        <div v-show="archOpen" class="med-list">
          <div v-for="m in archivedMeds" :key="m.id" class="med-row dim" @click="openDrawer(m.id)">
            <span class="med-ico" :style="{ '--c': FORM_COLORS[m.form] || '#909399' }" v-html="FORM_ICOS[m.form] || FORM_ICOS.tablet" />
            <div class="med-row-main">
              <div class="med-row-name"><b>{{ m.name }}</b><span class="med-dose-person">{{ m.person_name }}</span></div>
            </div>
          </div>
        </div>
      </div>
    </section>

    <!-- 成员管理弹层 -->
    <el-dialog v-model="personsOpen" :title="$t('med.personsTitle')" width="440px" append-to-body>
      <div class="med-person-list">
        <div v-for="(p, i) in persons" :key="p.id" class="med-person-row">
          <input v-model="p.color" type="color" class="med-color" :title="$t('med.personColor')" @change="savePerson(p)" />
          <el-input v-model="p.name" size="small" :placeholder="$t('med.personName')" @change="savePerson(p)" />
          <span class="med-person-n">{{ $t('med.personMedN', { n: p.med_count }) }}</span>
          <el-button size="small" text :icon="ArrowUp" :disabled="i === 0" @click="movePerson(i, -1)" />
          <el-button size="small" text :icon="ArrowDown" :disabled="i === persons.length - 1" @click="movePerson(i, 1)" />
          <el-button size="small" text type="danger" :icon="Delete" @click="delPerson(p)" />
        </div>
        <div class="med-person-row add">
          <input v-model="newPerson.color" type="color" class="med-color" :title="$t('med.personColor')" />
          <el-input v-model="newPerson.name" size="small" :placeholder="$t('med.addPerson')" @keyup.enter="addPerson" />
          <el-button size="small" type="primary" :icon="Plus" @click="addPerson" />
        </div>
      </div>
    </el-dialog>

    <!-- 历史弹层：月历 + 逐天明细 + 依从率 -->
    <el-dialog v-model="histOpen" :title="$t('med.histTitle')" width="520px" append-to-body @open="loadHist">
      <div class="med-hist-nav">
        <el-button-group size="small">
          <el-button :icon="ArrowLeft" @click="shiftHistMonth(-1)" />
          <el-button @click="histNow">{{ histMonthTitle }}</el-button>
          <el-button :icon="ArrowRight" @click="shiftHistMonth(1)" />
        </el-button-group>
        <div class="med-adh">
          <el-radio-group v-model="adhDays" size="small" @change="loadAdh">
            <el-radio-button :value="7">{{ $t('med.adherence7') }}</el-radio-button>
            <el-radio-button :value="30">{{ $t('med.adherence30') }}</el-radio-button>
            <el-radio-button :value="90">{{ $t('med.adherence90') }}</el-radio-button>
          </el-radio-group>
        </div>
      </div>
      <div class="med-hist-grid">
        <span v-for="w in histWeekdays" :key="w" class="med-hist-wd">{{ w }}</span>
        <button
          v-for="c in histCells"
          :key="c.key"
          type="button"
          class="med-hist-cell"
          :class="[c.level, { out: !c.inMonth, sel: c.day === histSel }]"
          @click="histSel = c.day; loadHistDay()"
        >
          <i>{{ c.day ? Number(c.day.slice(8)) : '' }}</i>
          <span v-if="c.total" class="med-strip-dots"><i v-for="k in Math.min(c.total, 6)" :key="k" :class="{ done: k <= c.taken, miss: k > c.taken && k <= c.taken + c.missed }" /></span>
        </button>
      </div>
      <div v-if="adh" class="med-adh-line">
        <span>{{ $t('med.adhRate', { pct: adhPct }) }}</span>
        <span class="med-tag green">{{ $t('med.adhTaken', { n: adh.taken }) }}</span>
        <span class="med-tag gray">{{ $t('med.adhSkipped', { n: adh.skipped }) }}</span>
        <span class="med-tag red">{{ $t('med.adhMissed', { n: adh.missed }) }}</span>
        <span>{{ $t('med.adhTotal', { n: adh.total }) }}</span>
      </div>
      <div class="med-hist-detail">
        <div v-if="!histDayRows.length" class="med-empty">{{ $t('med.histEmpty') }}</div>
        <div v-for="r in histDayRows" :key="r.id" class="med-dose">
          <span class="med-ico" :style="{ '--c': FORM_COLORS[r.form] || '#909399' }" v-html="FORM_ICOS[r.form] || FORM_ICOS.tablet" />
          <div class="med-dose-main">
            <div class="med-dose-name">
              <i class="med-dot" :style="{ background: r.person_color || '#909399' }" />
              <b>{{ r.med_name }}</b>
              <span class="med-dose-dose">{{ fmtDose(r) }}</span>
              <span class="med-dose-time">{{ r.time }} · {{ r.person_name }}</span>
            </div>
          </div>
          <span class="med-tag" :class="STATUS_CLS[r.status]">{{ $t('med.' + STATUS_KEY[r.status]) }}</span>
        </div>
      </div>
    </el-dialog>

    <!-- 药物编辑抽屉 -->
    <el-drawer v-model="drawerOpen" :title="$t('med.medDrawerTitle')" size="440px" append-to-body :destroy-on-close="true">
      <el-form label-position="top" class="med-form" @submit.prevent>
        <el-form-item :label="$t('med.person')">
          <el-select v-model="form.person_id" :teleported="false" size="small">
            <el-option v-for="p in persons" :key="p.id" :label="p.name" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item :label="$t('med.medName')">
          <el-input v-model="form.name" size="small" maxlength="50" />
        </el-form-item>
        <div class="med-form-2col">
          <el-form-item :label="$t('med.form')">
            <el-select v-model="form.form" :teleported="false" size="small">
              <el-option v-for="f in FORMS" :key="f.key" :label="$t('med.form' + f.label)" :value="f.key">
                <span class="med-form-opt">
                  <span class="med-ico med-ico-opt" :style="{ '--c': FORM_COLORS[f.key] }" v-html="FORM_ICOS[f.key]" />
                  <span>{{ $t('med.form' + f.label) }}</span>
                </span>
              </el-option>
            </el-select>
          </el-form-item>
          <el-form-item :label="$t('med.dose')">
            <div class="med-dose-input">
              <el-input-number v-model="form.dose_amount" size="small" :min="0.1" :step="0.5" />
              <el-input v-model="form.dose_unit" size="small" :placeholder="$t('med.doseUnit')" maxlength="10" />
            </div>
          </el-form-item>
        </div>
        <el-form-item :label="$t('med.schedule')">
          <el-radio-group v-model="form.kind" size="small">
            <el-radio-button value="daily">{{ $t('med.kindDaily') }}</el-radio-button>
            <el-radio-button value="weekdays">{{ $t('med.kindWeekdays') }}</el-radio-button>
            <el-radio-button value="interval">{{ $t('med.kindInterval') }}</el-radio-button>
            <el-radio-button value="prn">{{ $t('med.kindPrn') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <template v-if="form.kind === 'daily' || form.kind === 'weekdays' || form.kind === 'interval'">
          <el-form-item :label="$t('med.times')">
            <div class="med-times">
              <div v-for="(tm, i) in form.times" :key="i" class="med-time-row">
                <el-time-select v-model="form.times[i]" size="small" start="00:00" step="00:15" end="23:45" :teleported="false" />
                <el-button size="small" text type="danger" :icon="Delete" :disabled="form.times.length <= 1" @click="form.times.splice(i, 1)" />
              </div>
              <el-button size="small" text type="primary" :icon="Plus" :disabled="form.times.length >= 6" @click="form.times.push('08:00')">
                {{ $t('med.addTime') }}
              </el-button>
            </div>
          </el-form-item>
        </template>
        <el-form-item v-if="form.kind === 'weekdays'" :label="$t('med.weekdays')">
          <el-checkbox-group v-model="form.weekdays">
            <el-checkbox v-for="(w, i) in 7" :key="w" :value="w" size="small">{{ weekdayNames[i] }}</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <template v-if="form.kind === 'interval'">
          <el-form-item :label="$t('med.intervalDays')">
            <el-input-number v-model="form.interval_days" size="small" :min="2" :max="30" />
            <div class="med-hint">{{ $t('med.intervalHint', { n: form.interval_days }) }}</div>
          </el-form-item>
        </template>
        <div class="med-form-2col">
          <el-form-item :label="$t('med.startDate')">
            <el-date-picker v-model="form.start_date" type="date" size="small" value-format="YYYY-MM-DD" :teleported="false" :clearable="form.kind !== 'interval'" />
          </el-form-item>
          <el-form-item v-if="form.kind !== 'prn'" :label="$t('med.durationDays')">
            <el-input-number v-model="form.duration_days" size="small" :min="0" :max="3650" />
            <div class="med-hint">{{ $t('med.durationHint') }}</div>
          </el-form-item>
        </div>
        <el-form-item :label="$t('med.note')">
          <el-input v-model="form.note" size="small" maxlength="200" />
        </el-form-item>
        <el-form-item :label="$t('med.stock')">
          <el-switch v-model="stockOpen" size="small" />
          <span class="med-hint">{{ $t('med.stockOpen') }}</span>
        </el-form-item>
        <div v-if="stockOpen" class="med-form-2col">
          <el-form-item :label="$t('med.stockQty')">
            <el-input-number v-model="form.stock_qty" size="small" :min="0" :step="1" />
          </el-form-item>
          <el-form-item :label="$t('med.stockAlert')">
            <el-input-number v-model="form.stock_alert" size="small" :min="0" :step="1" />
          </el-form-item>
        </div>
        <div v-if="stockOpen" class="med-hint block">{{ $t('med.stockHint') }}</div>
        <el-form-item v-if="form.id > 0" :label="$t('med.archived')">
          <el-switch v-model="form.archived" size="small" />
          <div class="med-hint">{{ $t('med.archivedHint') }}</div>
        </el-form-item>
        <div class="med-form-foot">
          <el-button v-if="form.id > 0" size="small" type="danger" text :icon="Delete" @click="delMed">{{ $t('med.delete') }}</el-button>
          <span class="grow" />
          <el-button size="small" @click="drawerOpen = false">{{ $t('med.cancel') }}</el-button>
          <el-button size="small" type="primary" @click="saveMed">{{ $t('med.save') }}</el-button>
        </div>
      </el-form>
    </el-drawer>
  </div>
</template>

<script setup>
defineOptions({ name: 'MedView' })
import { computed, onActivated, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, Calendar as CalendarIcon, Delete, Plus, User } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { api, getRole } from '../api/index.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/med.js'

registerPageMessages(pageMsgs)
const { t } = useI18n()

// 人员管理（新建/编辑/删除/排序）只管理员：成员 GET /med-persons 只返回自己
// 绑定的一个人，选择器无需特殊处理，只藏管理入口（2026-11-03 家庭成员口径）
const isAdmin = getRole() !== 'user'

// ---- 剂型图标与配色（内联 SVG，颜色随剂型； integrations ico 同款做法） ----
const FORMS = [
  { key: 'tablet', label: 'Tablet' }, { key: 'capsule', label: 'Capsule' }, { key: 'softgel', label: 'Softgel' },
  { key: 'liquid', label: 'Liquid' }, { key: 'injection', label: 'Injection' }, { key: 'drops', label: 'Drops' },
  { key: 'inhaler', label: 'Inhaler' }, { key: 'topical', label: 'Topical' },
]
const FORM_COLORS = {
  tablet: '#409eff', capsule: '#67c23a', softgel: '#e6a23c', liquid: '#0ea5e9',
  injection: '#f56c6c', drops: '#9d6cf0', inhaler: '#eb2f96', topical: '#f2721c',
}
const S = `fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"`
// 剂型图标（2026-11-03 重绘，立体填充风格）：currentColor 主体随剂型色 + 白色
// 高光 + 暗部叠层出体积感；24 viewBox 几何居中、不用 transform 旋转（旧线性
// 图标胶囊斜放显歪、片剂斜分药线像禁止符——均已废弃）
// 剂型图标（2026-11-03 二版重绘，按真实药物形态）：每个剂型独立剪影——片剂圆盘
// 带刻痕、胶囊双色两半中缝环带、软胶囊椭圆缝合线、糖浆瓶三段瓶+标签白带、注射器
// 针筒刻度+针头、滴瓶+滴落药滴、MDI 吸入器 L 形、软膏管盖+管身；立体=厚度底影
// （黑 .18）+ 白高光；currentColor 随剂型色，24 viewBox 内不旋转不出界
const FORM_ICOS = {
  tablet: `<svg viewBox="0 0 24 24"><circle cx="12" cy="13.2" r="8.6" fill="#000" opacity=".18"/><circle cx="12" cy="11.2" r="8.6" fill="currentColor"/><ellipse cx="8.8" cy="7" rx="3.4" ry="1.9" fill="#fff" opacity=".45"/><path d="M5.6 11.2h12.8" stroke="#000" opacity=".25" stroke-width="1.7" stroke-linecap="round" fill="none"/></svg>`,
  capsule: `<svg viewBox="0 0 24 24"><path d="M12 2.5a5.5 5.5 0 0 1 5.5 5.5V11h-11V8A5.5 5.5 0 0 1 12 2.5z" fill="currentColor"/><path d="M6.5 11h11v5a5.5 5.5 0 0 1-11 0z" fill="currentColor" opacity=".55"/><rect x="6.5" y="10.55" width="11" height=".9" fill="#fff" opacity=".55"/><path d="M9.4 4.6a4.3 4.3 0 0 1 2.6-1.3" stroke="#fff" opacity=".6" stroke-width="1.5" fill="none" stroke-linecap="round"/></svg>`,
  softgel: `<svg viewBox="0 0 24 24"><ellipse cx="12" cy="12" rx="9.2" ry="7" fill="currentColor"/><path d="M3.2 10.5c2.6-1.5 5.6-2.3 8.8-2.3s6.2.8 8.8 2.3" stroke="#000" opacity=".15" stroke-width="1.4" fill="none"/><ellipse cx="8.6" cy="8.4" rx="3.1" ry="2" fill="#fff" opacity=".5"/></svg>`,
  liquid: `<svg viewBox="0 0 24 24"><rect x="9" y="2.6" width="6" height="3.6" rx="1.2" fill="currentColor"/><rect x="10" y="5.6" width="4" height="2.6" fill="currentColor"/><rect x="6.8" y="7.8" width="10.4" height="13.4" rx="2.8" fill="currentColor"/><rect x="7.6" y="13.4" width="8.8" height="3.2" rx=".8" fill="#fff" opacity=".5"/><rect x="8.3" y="9.2" width="1.6" height="7.5" rx=".8" fill="#fff" opacity=".35"/></svg>`,
  injection: `<svg viewBox="0 0 24 24"><rect x="10.4" y="2.8" width="3.2" height="2.2" rx=".7" fill="currentColor"/><rect x="9.6" y="4.8" width="4.8" height="10" rx="1.1" fill="currentColor"/><path d="M10.8 7.2h2.4M10.8 9.6h2.4M10.8 12h2.4" stroke="#fff" opacity=".5" stroke-width="1" fill="none" stroke-linecap="round"/><path d="M11.7 14.8h.6l.6 1.6h-1.8z" fill="currentColor"/><path d="M12 16.6v4.6" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" fill="none"/></svg>`,
  drops: `<svg viewBox="0 0 24 24"><rect x="9.6" y="1.8" width="4.8" height="2" rx="1" fill="currentColor"/><path d="M6.8 3h10.4v4.6a2.4 2.4 0 0 1-2.4 2.4H9.2A2.4 2.4 0 0 1 6.8 7.6z" fill="currentColor"/><rect x="8" y="4.6" width="1.5" height="4.4" rx=".75" fill="#fff" opacity=".4"/><path d="M12 12.6c2.2 2.6 3.7 4.4 3.7 6.1a3.7 3.7 0 1 1-7.4 0c0-1.7 1.5-3.5 3.7-6.1z" fill="currentColor"/></svg>`,
  inhaler: `<svg viewBox="0 0 24 24"><path d="M15.8 9.8 20.8 5l1.4 1.6-5 4.8z" fill="currentColor"/><rect x="9" y="6.4" width="3.6" height="3.4" rx="1" fill="currentColor"/><rect x="8" y="9.6" width="6.4" height="11.9" rx="2.2" fill="currentColor"/><rect x="9" y="16.6" width="4.4" height="3" rx="1" fill="#000" opacity=".18"/><path d="M9.2 11h1.4v7" stroke="#fff" opacity=".4" stroke-width="1.2" fill="none"/></svg>`,
  topical: `<svg viewBox="0 0 24 24"><rect x="9.9" y="2.4" width="4.2" height="2.8" rx="1" fill="currentColor"/><path d="M9.2 7.6 11 5.2h2l1.8 2.4v1.2H9.2z" fill="currentColor"/><path d="M9.2 8.8h5.6v9.8a2.6 2.6 0 0 1-2.6 2.6h-.4a2.6 2.6 0 0 1-2.6-2.6z" fill="currentColor"/><rect x="10" y="10.4" width="1.7" height="8.2" rx=".85" fill="#fff" opacity=".4"/></svg>`,
}

// 历史明细状态标签映射（状态 → 颜色类 / i18n key）
const STATUS_CLS = { taken: 'green', skipped: 'gray', missed: 'red', pending: 'gray' }
const STATUS_KEY = { taken: 'taken', skipped: 'skipped', missed: 'missed', pending: 'upcoming' }

// ---- 本地日期/时刻工具（服务端同款本地时区口径） ----
const pad = (n) => String(n).padStart(2, '0')
const fmtDay = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
const todayStr = () => fmtDay(new Date())
const nowHHMM = `${pad(new Date().getHours())}:${pad(new Date().getMinutes())}`
const fmtNum = (q) => (q == null ? '' : String(Math.round(q * 100) / 100))

// ---- 数据 ----
const persons = ref([])
const meds = ref([]) // 全量（含归档），active/archived 由 computed 拆
const doses = ref([])
const selDate = ref(todayStr())
const personFilter = ref(0)
const dayLoading = ref(false)

async function loadPersons() {
  const d = await api.get('/med-persons')
  persons.value = d.items || []
  if (personFilter.value && !persons.value.some((p) => p.id === personFilter.value)) personFilter.value = 0
}
async function loadMeds() {
  const d = await api.get('/medications?archived=1')
  meds.value = d.items || []
}
async function loadDay() {
  dayLoading.value = true
  try {
    const q = '/meds/day?date=' + selDate.value + (personFilter.value ? '&person_id=' + personFilter.value : '')
    const d = await api.get(q)
    doses.value = d.items || []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    dayLoading.value = false
  }
}
async function load() {
  try {
    await Promise.all([loadPersons(), loadMeds()])
    await loadDay()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
onMounted(load)
onActivated(load)

function setPerson(id) {
  personFilter.value = id
  loadDay()
}
function shiftDay(n) {
  const d = new Date(selDate.value + 'T00:00:00')
  d.setDate(d.getDate() + n)
  selDate.value = fmtDay(d)
  loadDay()
}
function goToday() {
  selDate.value = todayStr()
  loadDay()
}
const dayTitle = computed(() => {
  if (selDate.value === todayStr()) return t('med.todayIntake')
  return selDate.value
})

// 药物列表拆分与展示（全量 meds 拆 active/归档，人员筛选同滤）
const activeMeds = computed(() => meds.value.filter((m) => !m.archived_at && (!personFilter.value || m.person_id === personFilter.value)))
const archivedMeds = computed(() => meds.value.filter((m) => m.archived_at && (!personFilter.value || m.person_id === personFilter.value)))
const prnMeds = computed(() => activeMeds.value.filter((m) => m.kind === 'prn'))

// 今日剂量按时刻分组
const doseGroups = computed(() => {
  const map = new Map()
  for (const r of doses.value) {
    if (!map.has(r.time)) map.set(r.time, [])
    map.get(r.time).push(r)
  }
  return [...map.entries()].sort((a, b) => a[0].localeCompare(b[0])).map(([time, rows]) => ({ time, rows }))
})

const weekdayNames = computed(() => t('med.weekdaysShort').split(','))

function schedText(m) {
  const tm = (m.times || []).join(' / ')
  switch (m.kind) {
    case 'daily':
      return `${t('med.kindDaily')} ${tm}`
    case 'weekdays': {
      const names = (m.weekdays || []).map((w) => weekdayNames.value[w - 1] || w).join('/')
      return `${t('med.kindWeekdays')} ${names} ${tm}`
    }
    case 'interval':
      return `${t('med.intervalShort', { n: m.interval_days })} ${tm}`
    default:
      return t('med.kindPrn')
  }
}
function medEnded(m) {
  if (m.kind === 'prn' || !m.duration_days || !m.start_date) return false
  const end = new Date(m.start_date + 'T00:00:00')
  end.setDate(end.getDate() + m.duration_days)
  return fmtDay(end) <= todayStr()
}
function stockLow(m) {
  return m.stock_qty != null && m.stock_alert > 0 && m.stock_qty <= m.stock_alert
}
function fmtDose(m) {
  const n = fmtNum(m.dose_amount)
  return m.dose_unit ? `${n} ${m.dose_unit}` : n
}

// ---- 打卡 / 按需记一笔 ----
async function act(r, status) {
  try {
    await api.post('/meds/log', { med_id: r.med_id, day: r.day, time: r.time, status })
    await Promise.all([loadDay(), loadMeds(), loadStrip()])
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function prnLog(m) {
  try {
    await api.post(`/medications/${m.id}/prn`, { med_id: m.id })
    ElMessage.success(`${m.name} · ${t('med.taken')}`)
    await Promise.all([loadMeds()])
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 7 天概览条（今天收尾 7 天，月聚合上色） ----
const strip = ref([])
async function loadStrip() {
  const days = []
  for (let i = 6; i >= 0; i--) {
    const d = new Date()
    d.setDate(d.getDate() - i)
    days.push(fmtDay(d))
  }
  const months = [...new Set(days.map((d) => d.slice(0, 7)))]
  const summary = {}
  try {
    for (const mo of months) {
      const d = await api.get('/meds/month?month=' + mo)
      for (const it of d.items || []) summary[it.day] = it
    }
  } catch (e) {
    ElMessage.error(e.message)
    return
  }
  strip.value = days.map((day) => {
    const s = summary[day] || { total: 0, taken: 0, missed: 0 }
    let level = 'none'
    let levelKey = 'weekNone'
    if (s.total > 0) {
      if (s.missed > 0) { level = 'miss'; levelKey = 'weekMiss' }
      else if (s.taken >= s.total) { level = 'full'; levelKey = 'weekTaken' }
      else { level = 'part'; levelKey = 'weekPart' }
    }
    const dt = new Date(day + 'T00:00:00')
    return { day, label: String(dt.getDate()), total: Math.min(s.total, 6), taken: Math.min(s.taken, 6), level, levelKey }
  })
}
onMounted(loadStrip)

// ---- 成员管理 ----
const personsOpen = ref(false)
const newPerson = reactive({ name: '', color: '#409eff' })
const PERSON_PALETTE = ['#409eff', '#67c23a', '#e6a23c', '#f56c6c', '#9d6cf0', '#00b8a9', '#f0709a', '#7f8c4d']
async function addPerson() {
  if (!newPerson.name.trim()) return
  try {
    await api.post('/med-persons', { name: newPerson.name, color: newPerson.color })
    newPerson.name = ''
    newPerson.color = PERSON_PALETTE[persons.value.length % PERSON_PALETTE.length]
    await loadPersons()
    await loadMeds()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function savePerson(p) {
  try {
    await api.put(`/med-persons/${p.id}`, { name: p.name, color: p.color })
    await loadPersons()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function delPerson(p) {
  try {
    await ElMessageBox.confirm(t('med.delPersonConfirm', { name: p.name }), t('med.personsTitle'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.del(`/med-persons/${p.id}`)
    await Promise.all([loadPersons(), loadMeds(), loadDay(), loadStrip()])
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function movePerson(i, dir) {
  const ids = persons.value.map((p) => p.id)
  const j = i + dir
  if (j < 0 || j >= ids.length) return
  ;[ids[i], ids[j]] = [ids[j], ids[i]]
  try {
    await api.post('/med-persons-reorder', { ids })
    await loadPersons()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 药物编辑抽屉 ----
const drawerOpen = ref(false)
const stockOpen = ref(false)
const form = reactive({
  id: 0, person_id: 0, name: '', note: '', form: 'tablet', dose_amount: 1, dose_unit: '',
  kind: 'daily', times: ['08:00'], weekdays: [], interval_days: 2, start_date: '', duration_days: 0,
  stock_qty: 0, stock_alert: 1, archived: false,
})
function openDrawer(id) {
  form.id = id || 0
  if (id) {
    const m = meds.value.find((x) => x.id === id)
    if (!m) return
    Object.assign(form, {
      id: m.id, person_id: m.person_id, name: m.name, note: m.note || '', form: m.form || 'tablet',
      dose_amount: m.dose_amount || 1, dose_unit: m.dose_unit || '', kind: m.kind || 'daily',
      times: (m.times && m.times.length ? [...m.times] : ['08:00']), weekdays: [...(m.weekdays || [])],
      interval_days: m.interval_days || 2, start_date: m.start_date || '', duration_days: m.duration_days || 0,
      stock_qty: m.stock_qty != null ? m.stock_qty : 0, stock_alert: m.stock_alert || 0,
      archived: !!m.archived_at,
    })
    stockOpen.value = m.stock_qty != null
  } else {
    Object.assign(form, {
      id: 0, person_id: personFilter.value || persons.value[0]?.id || 0, name: '', note: '', form: 'tablet',
      dose_amount: 1, dose_unit: '', kind: 'daily', times: ['08:00'], weekdays: [1, 2, 3, 4, 5],
      interval_days: 2, start_date: todayStr(), duration_days: 0, stock_qty: 0, stock_alert: 1, archived: false,
    })
    stockOpen.value = false
  }
  drawerOpen.value = true
}
async function saveMed() {
  const payload = {
    person_id: form.person_id, name: form.name, note: form.note, form: form.form,
    dose_amount: form.dose_amount, dose_unit: form.dose_unit, kind: form.kind,
    times: form.kind === 'prn' ? [] : form.times, weekdays: form.kind === 'weekdays' ? form.weekdays : [],
    interval_days: form.interval_days, start_date: form.start_date, duration_days: form.duration_days,
    stock_qty: stockOpen.value ? form.stock_qty : null, stock_alert: stockOpen.value ? form.stock_alert : 0,
    archived: form.archived,
  }
  try {
    if (form.id > 0) await api.put(`/medications/${form.id}`, payload)
    else await api.post('/medications', payload)
    drawerOpen.value = false
    await Promise.all([loadMeds(), loadDay(), loadStrip()])
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function delMed() {
  try {
    await ElMessageBox.confirm(t('med.delMedConfirm', { name: form.name }), t('med.medDrawerTitle'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await api.del(`/medications/${form.id}`)
    drawerOpen.value = false
    await Promise.all([loadMeds(), loadDay(), loadStrip()])
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 历史弹层 ----
const histOpen = ref(false)
const histMonth = ref(todayStr().slice(0, 7))
const histSel = ref('')
const histSummary = ref({})
const histDayRows = ref([])
const adhDays = ref(7)
const adh = ref(null)
const histWeekdays = computed(() => weekdayNames.value)
const histMonthTitle = computed(() => histMonth.value.replace('-', ' / '))
const histCells = computed(() => {
  const [y, mo] = histMonth.value.split('-').map(Number)
  const first = new Date(y, mo - 1, 1)
  const start = new Date(first)
  start.setDate(1 - ((first.getDay() + 6) % 7)) // 周一起
  const cells = []
  for (let i = 0; i < 42; i++) {
    const d = new Date(start)
    d.setDate(start.getDate() + i)
    const day = fmtDay(d)
    const s = histSummary.value[day]
    let level = 'none'
    if (s && s.total > 0) level = s.missed > 0 ? 'miss' : s.taken >= s.total ? 'full' : 'part'
    cells.push({ key: day + i, day, inMonth: d.getMonth() === mo - 1, total: s?.total || 0, taken: s?.taken || 0, missed: s?.missed || 0, level })
  }
  return cells
})
const adhPct = computed(() => {
  if (!adh.value || !adh.value.total) return 0
  return Math.round((adh.value.taken * 100) / adh.value.total)
})
function openHistory() {
  histMonth.value = todayStr().slice(0, 7)
  histSel.value = todayStr()
  histOpen.value = true
}
function shiftHistMonth(n) {
  const [y, mo] = histMonth.value.split('-').map(Number)
  const d = new Date(y, mo - 1 + n, 1)
  histMonth.value = `${d.getFullYear()}-${pad(d.getMonth() + 1)}`
  loadHist()
}
function histNow() {
  histMonth.value = todayStr().slice(0, 7)
  histSel.value = todayStr()
  loadHist()
}
async function loadHist() {
  loadHistDay()
  loadAdh()
  try {
    const d = await api.get('/meds/month?month=' + histMonth.value)
    const map = {}
    for (const it of d.items || []) map[it.day] = it
    histSummary.value = map
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function loadHistDay() {
  if (!histSel.value) return
  try {
    const d = await api.get('/meds/day?date=' + histSel.value)
    histDayRows.value = d.items || []
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function loadAdh() {
  try {
    adh.value = await api.get('/meds/adherence?days=' + adhDays.value)
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<style scoped>
.med-page { padding: 4px 0 24px; display: flex; flex-direction: column; gap: 14px; }
.med-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.med-persons { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.med-chip {
  display: inline-flex; align-items: center; gap: 6px; border: 1px solid var(--line, rgba(255,255,255,.14));
  background: transparent; color: inherit; border-radius: 999px; padding: 4px 14px; font-size: 13px; cursor: pointer;
}
.med-chip.on { background: rgba(255, 255, 255, 0.12); border-color: transparent; }
.med-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; flex: none; }
.med-actions { display: flex; align-items: center; gap: 8px; }

.med-strip { display: flex; gap: 8px; }
.med-strip-day {
  flex: 1; display: flex; flex-direction: column; align-items: center; gap: 6px; padding: 10px 4px 8px;
  border-radius: 12px; border: 1px solid var(--line, rgba(255,255,255,.1)); background: rgba(255,255,255,.04);
  color: inherit; cursor: pointer; min-width: 0;
}
.med-strip-day i { font-style: normal; font-size: 15px; font-weight: 700; }
.med-strip-day.full { border-color: rgba(103, 194, 58, 0.45); }
.med-strip-day.part { border-color: rgba(230, 162, 60, 0.45); }
.med-strip-day.miss { border-color: rgba(245, 108, 108, 0.55); }
.med-strip-day.sel { background: rgba(255, 255, 255, 0.12); }
.med-strip-dots { display: flex; gap: 3px; }
.med-strip-dots i { width: 6px; height: 6px; border-radius: 50%; background: rgba(255, 255, 255, 0.18); }
.med-strip-dots i.done { background: #67c23a; }
.med-strip-dots i.miss { background: #f56c6c; }

.med-sec { display: flex; flex-direction: column; gap: 10px; }
.med-sec-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.med-sec-head h3 { margin: 0; font-size: 15px; }
.med-empty { color: var(--ink-2, rgba(255,255,255,.55)); font-size: 13px; padding: 18px 4px; text-align: center; }

.med-timeline, .med-prn { display: flex; flex-direction: column; gap: 10px; }
.med-slot { display: flex; gap: 12px; align-items: flex-start; }
.med-slot-time { flex: none; width: 52px; font-weight: 700; font-variant-numeric: tabular-nums; font-size: 14px; padding-top: 10px; }
.med-slot-rows { flex: 1; display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.med-dose {
  display: flex; align-items: center; gap: 10px; padding: 8px 12px; border-radius: 12px;
  background: rgba(255, 255, 255, 0.06); min-width: 0;
}
.med-dose.done { opacity: 0.62; }
.med-dose.off .med-dose-name b { text-decoration: line-through; }
.med-ico { flex: none; width: 32px; height: 32px; border-radius: 10px; display: inline-flex; align-items: center; justify-content: center; color: var(--c); background: color-mix(in srgb, var(--c) 18%, transparent); }
.med-ico :deep(svg) { width: 19px; height: 19px; }
/* 剂型下拉选项：小号剂型图标 + 文字（2026-11-03 用户指定） */
.med-form-opt { display: inline-flex; align-items: center; gap: 8px; }
.med-ico-opt { width: 22px; height: 22px; border-radius: 6px; }
.med-ico-opt :deep(svg) { width: 14px; height: 14px; }
.med-dose-main { flex: 1; min-width: 0; }
.med-dose-name { display: flex; align-items: center; gap: 7px; min-width: 0; flex-wrap: wrap; }
.med-dose-name b { font-size: 14px; }
.med-dose-dose { color: var(--ink-2, rgba(255,255,255,.55)); font-size: 12.5px; }
.med-dose-person { color: var(--ink-2, rgba(255,255,255,.55)); font-size: 12px; border: 1px solid var(--line, rgba(255,255,255,.14)); border-radius: 999px; padding: 1px 8px; }
.med-dose-time { color: var(--ink-2, rgba(255,255,255,.55)); font-size: 12px; }
.med-dose-note { color: var(--ink-2, rgba(255,255,255,.45)); font-size: 12px; margin-top: 2px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.med-dose-act { display: flex; align-items: center; gap: 6px; flex: none; }
.med-tag { display: inline-flex; align-items: center; border-radius: 999px; padding: 2px 9px; font-size: 12px; white-space: nowrap; }
.med-tag.green { background: rgba(103, 194, 58, 0.16); color: #85cf60; }
.med-tag.red { background: rgba(245, 108, 108, 0.16); color: #f56c6c; }
.med-tag.gray { background: rgba(255, 255, 255, 0.1); color: var(--ink-2, rgba(255,255,255,.6)); }

.med-list { display: flex; flex-direction: column; gap: 8px; }
.med-row {
  display: flex; align-items: center; gap: 10px; padding: 9px 12px; border-radius: 12px;
  background: rgba(255, 255, 255, 0.06); cursor: pointer; min-width: 0;
}
.med-row:hover { background: rgba(255, 255, 255, 0.1); }
.med-row.dim { opacity: 0.6; }
.med-row-main { flex: 1; min-width: 0; }
.med-row-name { display: flex; align-items: center; gap: 7px; flex-wrap: wrap; }
.med-row-name b { font-size: 14px; }
.med-row-sub { color: var(--ink-2, rgba(255,255,255,.5)); font-size: 12.5px; margin-top: 2px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.med-row-go { color: var(--ink-2, rgba(255,255,255,.35)); }
.med-arch-bar {
  display: inline-flex; align-items: center; gap: 6px; background: transparent; border: none; color: inherit;
  color: var(--ink-2, rgba(255,255,255,.55)); font-size: 13px; cursor: pointer; padding: 6px 2px;
}

.med-person-list { display: flex; flex-direction: column; gap: 8px; }
.med-person-row { display: flex; align-items: center; gap: 8px; }
.med-person-row.add { border-top: 1px dashed var(--line, rgba(255,255,255,.14)); padding-top: 10px; }
.med-person-n { color: var(--ink-2, rgba(255,255,255,.5)); font-size: 12px; flex: 1; text-align: right; white-space: nowrap; }
.med-color { width: 30px; height: 30px; padding: 0; border: none; background: transparent; cursor: pointer; }

.med-hist-nav { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; margin-bottom: 10px; }
.med-hist-grid { display: grid; grid-template-columns: repeat(7, 1fr); gap: 4px; }
.med-hist-wd { text-align: center; font-size: 12px; color: var(--ink-2, rgba(255,255,255,.45)); padding: 2px 0; }
.med-hist-cell {
  min-height: 46px; border-radius: 8px; border: 1px solid var(--line, rgba(255,255,255,.08));
  background: rgba(255,255,255,.03); color: inherit; cursor: pointer; display: flex; flex-direction: column;
  align-items: center; justify-content: center; gap: 3px; padding: 3px;
}
.med-hist-cell i { font-style: normal; font-size: 12.5px; }
.med-hist-cell.out { opacity: 0.35; }
.med-hist-cell.full { border-color: rgba(103, 194, 58, 0.5); }
.med-hist-cell.part { border-color: rgba(230, 162, 60, 0.5); }
.med-hist-cell.miss { border-color: rgba(245, 108, 108, 0.6); }
.med-hist-cell.sel { background: rgba(255, 255, 255, 0.14); }
.med-adh-line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin: 10px 0; font-size: 13px; }
.med-hist-detail { display: flex; flex-direction: column; gap: 6px; max-height: 240px; overflow-y: auto; }

.med-form :deep(.el-form-item) { margin-bottom: 12px; }
.med-form-2col { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.med-dose-input { display: flex; gap: 8px; align-items: center; width: 100%; }
.med-times { display: flex; flex-direction: column; gap: 6px; align-items: flex-start; }
.med-time-row { display: flex; align-items: center; gap: 6px; }
.med-hint { color: var(--ink-2, rgba(255,255,255,.45)); font-size: 12px; margin-top: 2px; }
.med-hint.block { margin: -6px 0 10px; }
.med-form-foot { display: flex; align-items: center; gap: 8px; margin-top: 4px; }
.med-form-foot .grow { flex: 1; }

@media (max-width: 640px) {
  .med-strip { flex-wrap: nowrap; overflow-x: auto; }
  .med-strip-day { min-width: 42px; }
  .med-slot { flex-direction: column; gap: 4px; }
  .med-slot-time { width: auto; padding-top: 0; }
  .med-form-2col { grid-template-columns: 1fr; }
}
</style>
