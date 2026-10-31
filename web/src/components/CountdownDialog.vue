<template>
  <!-- 倒数日管理弹层（个人助理批）：主页倒数日卡与日历页共用；append-to-body 逃出 .main -->
  <el-dialog
    :model-value="modelValue"
    :title="$t('cd.title')"
    :width="isMobile ? '94%' : '560px'"
    append-to-body
    :close-on-click-modal="false"
    class="cd-dialog"
    @update:model-value="(v) => emit('update:modelValue', v)"
    @open="load"
  >
    <div class="cd-toolbar">
      <el-button type="primary" size="small" :icon="Plus" @click="startAdd">{{ $t('cd.add') }}</el-button>
      <span class="cd-hint">{{ items.length }} / 100</span>
    </div>

    <div v-if="editing" class="cd-form">
      <el-form label-width="72px" size="small" @submit.prevent>
        <el-form-item :label="$t('cd.name')">
          <el-input v-model="form.name" maxlength="40" :placeholder="$t('cd.namePh')" @keyup.enter="save" />
        </el-form-item>
        <el-form-item :label="$t('cd.kind')">
          <el-radio-group v-model="form.kind">
            <el-radio-button value="once">{{ $t('cd.kindOnce') }}</el-radio-button>
            <el-radio-button value="yearly">{{ $t('cd.kindYearly') }}</el-radio-button>
            <el-radio-button value="since">{{ $t('cd.kindSince') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="$t('cd.date')">
          <el-date-picker v-model="form.date" type="date" value-format="YYYY-MM-DD" style="width: 160px" />
          <el-checkbox v-if="form.kind === 'yearly'" v-model="form.lunar" style="margin-left: 12px">{{ $t('cd.lunar') }}</el-checkbox>
        </el-form-item>
        <div v-if="form.kind === 'yearly' && form.lunar" class="cd-hint cd-hint-block">{{ $t('cd.lunarHint') }}</div>
        <el-form-item :label="$t('cd.color')">
          <div class="cd-palette">
            <button
              v-for="c in CD_PALETTE"
              :key="c"
              type="button"
              class="cd-swatch"
              :class="{ on: form.color === c }"
              :style="{ background: c }"
              @click="form.color = c"
            />
          </div>
        </el-form-item>
      </el-form>
      <div class="cd-form-foot">
        <el-button size="small" @click="editing = false">{{ $t('cd.cancel') }}</el-button>
        <el-button size="small" type="primary" :loading="saving" @click="save">{{ $t('cd.save') }}</el-button>
      </div>
    </div>

    <div v-if="!items.length && !editing && !loading" class="cd-empty">{{ $t('cd.empty') }}</div>
    <div v-for="c in items" :key="c.id" class="cd-row">
      <span class="cd-dot" :style="{ background: c.color || '#909399' }" />
      <div class="cd-main">
        <div class="cd-name">
          {{ c.name }}
          <el-tag v-if="c.lunar && c.lunar_label" size="small" type="info" effect="plain" class="cd-lunar">{{ c.lunar_label }}</el-tag>
        </div>
        <div class="cd-sub">{{ kindLabel(c) }} · {{ c.next_date }}</div>
      </div>
      <b class="cd-days" :class="{ soon: c.kind !== 'since' && c.days >= 0 && c.days <= 7 }">{{ cdDaysText(c, t) }}</b>
      <el-button size="small" text :icon="EditPen" :title="$t('cd.edit')" @click="startEdit(c)" />
      <el-button size="small" text type="danger" :icon="Delete" :title="$t('cd.delete')" @click="remove(c)" />
    </div>
  </el-dialog>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, EditPen, Plus } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api/index.js'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/calendar.js'
import { useIsMobile } from '../composables/useIsMobile.js'
import { CD_PALETTE, cdDaysText } from '../utils/countdown.js'

registerPageMessages(pageMsgs)
const { t } = useI18n()
const isMobile = useIsMobile()

defineProps({ modelValue: Boolean })
const emit = defineEmits(['update:modelValue', 'changed'])

const items = ref([])
const loading = ref(false)
const editing = ref(false)
const saving = ref(false)
const form = reactive({ id: 0, name: '', kind: 'once', lunar: false, date: '', color: CD_PALETTE[3] })

async function load() {
  loading.value = true
  try {
    const r = await api.get('/countdowns')
    items.value = r.items || []
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    loading.value = false
  }
}

function kindLabel(c) {
  return t(c.kind === 'yearly' ? 'cd.kindYearly' : c.kind === 'since' ? 'cd.kindSince' : 'cd.kindOnce')
}

function startAdd() {
  Object.assign(form, { id: 0, name: '', kind: 'once', lunar: false, date: '', color: CD_PALETTE[3] })
  editing.value = true
}

function startEdit(c) {
  Object.assign(form, { id: c.id, name: c.name, kind: c.kind, lunar: !!c.lunar, date: c.date, color: c.color || CD_PALETTE[3] })
  editing.value = true
}

async function save() {
  if (!form.name.trim()) return ElMessage.warning(t('cd.needName'))
  if (!form.date) return ElMessage.warning(t('cd.needDate'))
  saving.value = true
  try {
    const body = { name: form.name.trim(), kind: form.kind, lunar: form.kind === 'yearly' && form.lunar, date: form.date, color: form.color }
    const r = form.id ? await api.put(`/countdowns/${form.id}`, body) : await api.post('/countdowns', body)
    items.value = r.items || []
    editing.value = false
    ElMessage.success(t('cd.saved'))
    emit('changed')
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}

async function remove(c) {
  try {
    await ElMessageBox.confirm(t('cd.delConfirm', { name: c.name }), t('cd.delete'), { type: 'warning', confirmButtonText: t('cd.delete') })
  } catch {
    return
  }
  try {
    await api.del(`/countdowns/${c.id}`)
    items.value = items.value.filter((x) => x.id !== c.id)
    ElMessage.success(t('cd.deleted'))
    emit('changed')
  } catch (e) {
    ElMessage.error(e.message)
  }
}
</script>

<style scoped>
.cd-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.cd-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.cd-hint-block {
  margin: -6px 0 10px 72px;
}
.cd-form {
  padding: 10px 12px 6px;
  margin-bottom: 10px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
  background: var(--el-fill-color-lighter);
}
.cd-form-foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.cd-palette {
  display: flex;
  gap: 8px;
}
.cd-swatch {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: 2px solid transparent;
  cursor: pointer;
  padding: 0;
}
.cd-swatch.on {
  border-color: var(--el-text-color-primary);
  box-shadow: 0 0 0 2px var(--el-bg-color);
}
.cd-empty {
  padding: 28px 0;
  text-align: center;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.cd-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 4px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}
.cd-row:last-child {
  border-bottom: none;
}
.cd-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex: none;
}
.cd-main {
  flex: 1;
  min-width: 0;
}
.cd-name {
  font-size: 14px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}
.cd-lunar {
  font-weight: normal;
}
.cd-sub {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin-top: 2px;
}
.cd-days {
  font-size: 14px;
  white-space: nowrap;
  color: var(--el-color-primary);
}
.cd-days.soon {
  color: var(--el-color-danger);
}
</style>
