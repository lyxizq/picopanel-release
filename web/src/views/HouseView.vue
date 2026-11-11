<template>
  <!-- 房屋管理（2026-11-03）：左栏总览（统计卡 + 房屋卡片列表）+ 右栏详情三 tab
       （房屋信息含水电燃气卡号一键复制 / 家具电器 / 租约含合同下载）。
       useSidePanel 分栏约定：可拖拽调宽、单击分隔条收起；手机端改上下堆叠 + 顶部下拉选房 -->
  <div class="house-page" :class="{ mobile: isMobile }">
    <aside v-if="!isMobile" v-show="!side.collapsed" class="house-side" :style="{ width: side.width + 'px' }">
      <div class="side-head">
        <span class="side-title">{{ $t('routes.houses') }}</span>
        <div class="side-head-btns">
          <el-button size="small" :icon="Notebook" :title="$t('houses.tplTitle')" @click="openTpl">{{ $t('houses.tplTitle') }}</el-button>
          <el-button type="primary" size="small" :icon="Plus" @click="openHouseDlg()">{{ $t('houses.addHouse') }}</el-button>
        </div>
      </div>
      <div class="stat-grid">
        <div class="stat-card">
          <div class="stat-label">{{ $t('houses.statMonth') }}</div>
          <div class="stat-value">¥{{ fmtMoney(stats.month_rent) }}</div>
        </div>
        <div class="stat-card" :title="$t('houses.statYearHint')">
          <div class="stat-label">{{ $t('houses.statYear') }}</div>
          <div class="stat-value">¥{{ fmtMoney(stats.year_rent) }}</div>
        </div>
        <button type="button" class="stat-card stat-btn" :title="$t('houses.incomeHint')" @click="openIncome()">
          <div class="stat-label">{{ $t('houses.incomeYearNet') }}</div>
          <div class="stat-value" :class="{ neg: (incomeData.year_income?.net || 0) < 0 }">¥{{ fmtMoney(incomeData.year_income?.net) }}</div>
        </button>
        <button type="button" class="stat-card stat-btn" :title="$t('houses.incomeHint')" @click="openIncome()">
          <div class="stat-label">{{ $t('houses.incomeQNet') }}</div>
          <div class="stat-value" :class="{ neg: curQuarterNet < 0 }">¥{{ fmtMoney(curQuarterNet) }}</div>
        </button>
        <div class="stat-card">
          <div class="stat-label">{{ $t('houses.statTotal') }}</div>
          <div class="stat-value">{{ stats.total || 0 }}</div>
        </div>
        <div class="stat-card" :title="occText">
          <div class="stat-label">{{ $t('houses.statOcc') }}</div>
          <div class="stat-value">{{ occPct }}%</div>
        </div>
      </div>
      <div class="house-list">
        <div v-if="!list.length" class="list-empty">{{ $t('houses.emptyList') }}</div>
        <button
          v-for="hse in list" :key="hse.id"
          class="house-card" :class="{ active: cur === hse.id }"
          @click="selectHouse(hse.id)"
        >
          <div class="hc-row">
            <span class="hc-name">{{ hse.name }}</span>
            <span class="hc-tag" :class="'st-' + hse.display_status">{{ statusLabel(hse.display_status) }}</span>
          </div>
          <div v-if="hse.address" class="hc-addr">{{ hse.address }}</div>
          <div class="hc-sub">
            <template v-if="hse.active_lease">
              <span>{{ $t('houses.leaseTenantOf') }} {{ hse.active_lease.tenant_name }}</span>
              <span>¥{{ fmtMoney(hse.active_lease.monthly_rent) }}</span>
              <span v-if="hse.rent_due_day" class="hc-rent">{{ tr('houses.rentChip', { d: hse.rent_due_day }) }}</span>
              <span class="hc-days" :class="daysCls(hse.active_lease)">{{ daysLabel(hse.active_lease) }}</span>
            </template>
            <template v-else>
              <span>{{ hse.items_count ? $t('houses.tabItems') + ' ' + hse.items_count : statusLabel(hse.display_status) }}</span>
            </template>
          </div>
        </button>
      </div>
    </aside>
    <div
      v-if="!isMobile"
      class="side-divider"
      :class="{ on: side.resizing }"
      :title="side.collapsed ? $t('houses.expandTip') : $t('houses.sideTip')"
      @mousedown="side.startDrag"
    />

    <main class="house-main">
      <div v-if="isMobile" class="stat-grid m-stats">
        <div class="stat-card" :title="$t('houses.statYearHint')">
          <div class="stat-label">{{ $t('houses.statMonth') }}</div>
          <div class="stat-value">¥{{ fmtMoney(stats.month_rent) }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-label">{{ $t('houses.statYear') }}</div>
          <div class="stat-value">¥{{ fmtMoney(stats.year_rent) }}</div>
        </div>
        <button type="button" class="stat-card stat-btn" :title="$t('houses.incomeHint')" @click="openIncome()">
          <div class="stat-label">{{ $t('houses.incomeYearNet') }}</div>
          <div class="stat-value" :class="{ neg: (incomeData.year_income?.net || 0) < 0 }">¥{{ fmtMoney(incomeData.year_income?.net) }}</div>
        </button>
        <button type="button" class="stat-card stat-btn" :title="$t('houses.incomeHint')" @click="openIncome()">
          <div class="stat-label">{{ $t('houses.incomeQNet') }}</div>
          <div class="stat-value" :class="{ neg: curQuarterNet < 0 }">¥{{ fmtMoney(curQuarterNet) }}</div>
        </button>
      </div>

      <template v-if="curHouse">
        <div class="detail-head">
          <div class="detail-title">
            <h2>{{ curHouse.name }}</h2>
            <span class="hc-tag" :class="'st-' + curHouse.display_status">{{ statusLabel(curHouse.display_status) }}</span>
          </div>
          <div class="detail-actions">
            <el-select
              v-if="isMobile" :model-value="cur" class="m-pick" size="small"
              @change="(v) => selectHouse(v)"
            >
              <el-option v-for="hse in list" :key="hse.id" :value="hse.id" :label="hse.name" />
            </el-select>
            <el-button size="small" :icon="Plus" @click="openHouseDlg()">{{ $t('houses.addHouse') }}</el-button>
            <el-button size="small" :icon="EditPen" @click="openHouseDlg(curHouse)">{{ $t('houses.editHouse') }}</el-button>
            <el-button size="small" type="danger" plain :icon="Delete" @click="delHouse(curHouse)">{{ $t('houses.delHouse') }}</el-button>
          </div>
        </div>
        <div v-if="curHouse.address" class="detail-addr">{{ curHouse.address }}</div>

        <el-tabs v-model="tab" class="detail-tabs">
          <el-tab-pane :label="$t('houses.tabInfo')" name="info">
            <div class="info-grid">
              <div class="info-block">
                <div class="info-title">{{ $t('houses.address') }} / {{ $t('houses.layout') }}</div>
                <div class="info-row"><span>{{ $t('houses.address') }}</span><b>{{ curHouse.address || $t('houses.noneFilled') }}</b></div>
                <div class="info-row"><span>{{ $t('houses.area') }}</span><b>{{ curHouse.area || $t('houses.noneFilled') }}</b></div>
                <div class="info-row"><span>{{ $t('houses.layout') }}</span><b>{{ curHouse.layout || $t('houses.noneFilled') }}</b></div>
                <div v-if="curHouse.rent_due_day" class="info-row">
                  <span>{{ $t('houses.rentRemind') }}</span><b>{{ tr('houses.rentInfoRow', { d: curHouse.rent_due_day, t: curHouse.rent_due_time }) }}</b>
                </div>
                <div class="info-row col"><span>{{ $t('houses.notes') }}</span><b>{{ curHouse.notes || $t('houses.noneFilled') }}</b></div>
              </div>
              <div class="info-block cards">
                <div class="info-title">
                  {{ $t('houses.cardSec') }}
                  <el-button size="small" type="primary" plain :icon="CopyDocument" @click="copyCards">{{ $t('houses.copyCards') }}</el-button>
                </div>
                <div v-for="cd in cardRows" :key="cd.label" class="info-row card-row">
                  <span>{{ cd.label }}</span><b class="mono">{{ cd.val || $t('houses.noneFilled') }}</b>
                  <el-button v-if="cd.val" text size="small" :icon="CopyDocument" :title="$t('houses.copied')" @click="copyOne(cd.val)" />
                </div>
              </div>
            </div>
          </el-tab-pane>

          <el-tab-pane :label="`${$t('houses.tabItems')} (${detail.items.length})`" name="items">
            <div class="pane-head">
              <el-button type="primary" size="small" :icon="Plus" @click="openItemDlg()">{{ $t('houses.addItem') }}</el-button>
            </div>
            <el-table v-if="detail.items.length" :data="detail.items" size="small">
              <el-table-column :label="$t('houses.category')" width="80">
                <template #default="{ row }">
                  <el-tag size="small" :type="row.category === 'appliance' ? 'warning' : 'primary'">{{ catLabel(row.category) }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column :label="$t('houses.itemName')" prop="name" min-width="110" show-overflow-tooltip />
              <el-table-column :label="$t('houses.brandModel')" prop="brand_model" min-width="120" show-overflow-tooltip />
              <el-table-column :label="$t('houses.qty')" prop="qty" width="60" align="center" />
              <el-table-column :label="$t('houses.itemStatus')" width="76">
                <template #default="{ row }">
                  <el-tag size="small" :type="itemStTag(row.status)">{{ itemStLabel(row.status) }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column width="64" align="center">
                <template #header>
                  <span :title="$t('houses.inContractHint')">{{ $t('houses.inContract') }}</span>
                </template>
                <template #default="{ row }">
                  <el-checkbox :model-value="row.in_contract" :disabled="row.status === 'removed'" @change="toggleItemContract(row)" />
                </template>
              </el-table-column>
              <el-table-column :label="$t('houses.notes')" prop="notes" min-width="100" show-overflow-tooltip />
              <el-table-column width="90" align="right">
                <template #default="{ row }">
                  <el-button text size="small" :icon="EditPen" @click="openItemDlg(row)" />
                  <el-button text size="small" type="danger" :icon="Delete" @click="delItem(row)" />
                </template>
              </el-table-column>
            </el-table>
            <el-empty v-else :description="$t('houses.itemsEmpty')" :image-size="60" />
          </el-tab-pane>

          <el-tab-pane v-if="curHouse.display_status !== 'selfuse'" :label="`${$t('houses.tabLeases')} (${detail.leases.length})`" name="leases">
            <div class="pane-head">
              <el-tooltip :disabled="!hasActive" :content="$t('houses.addLeaseDisabled')" placement="top">
                <span>
                  <el-button type="primary" size="small" :icon="Plus" :disabled="hasActive" @click="openLeaseDlg()">{{ $t('houses.addLease') }}</el-button>
                </span>
              </el-tooltip>
            </div>
            <div v-if="!detail.leases.length" class="list-empty">{{ $t('houses.leaseEmpty') }}</div>
            <div v-for="l in detail.leases" :key="l.id" class="lease-card" :class="{ ended: l.status === 'ended' }">
              <div class="lc-head">
                <b class="lc-name">{{ l.tenant_name }}</b>
                <el-tag size="small" :type="l.status === 'active' ? 'success' : 'info'">
                  {{ l.status === 'active' ? $t('houses.lsActive') : $t('houses.lsEnded') }}
                </el-tag>
                <span v-if="l.status === 'active'" class="hc-days" :class="daysCls(l)">{{ daysLabel(l) }}</span>
                <span class="lc-actions">
                  <el-button size="small" :icon="Download" @click="downloadContract(l)">{{ $t('houses.downloadContract') }}</el-button>
                  <el-button size="small" :icon="Promotion" :title="$t('houses.pushSent')" @click="testLeasePush(l)">{{ $t('houses.testPush') }}</el-button>
                  <el-button v-if="l.status === 'active'" size="small" @click="renewLease(l)">{{ $t('houses.renew') }}</el-button>
                  <el-button v-if="l.status === 'active'" size="small" @click="endLease(l)">{{ $t('houses.endLease') }}</el-button>
                  <el-button v-else size="small" @click="restoreLease(l)">{{ $t('houses.restoreLease') }}</el-button>
                  <el-button size="small" text :icon="EditPen" @click="openLeaseDlg(l)" />
                  <el-button size="small" text type="danger" :icon="Delete" @click="delLease(l)" />
                </span>
              </div>
              <div class="lc-grid">
                <span>📅 {{ l.start_date }} ~ {{ l.end_date }}</span>
                <span>{{ tr('houses.rentPerMonth', { v: fmtMoney(l.monthly_rent) }) }}</span>
                <span v-if="l.deposit">{{ $t('houses.deposit') }} ¥{{ fmtMoney(l.deposit) }}</span>
                <span v-if="l.payment_cycle">{{ l.payment_cycle }}</span>
                <span v-if="l.tenant_phone" class="mono">📞 {{ l.tenant_phone }}</span>
                <span v-if="l.tenant_id_no" class="mono">🪪 {{ l.tenant_id_no }}</span>
                <span v-if="l.water_reading">💧 {{ fmtMoney(l.water_reading) }} {{ $t('houses.unitL') }}</span>
                <span v-if="l.electric_reading">⚡ {{ fmtMoney(l.electric_reading) }} {{ $t('houses.unitYuan') }}</span>
                <span v-if="l.gas_reading">🔥 {{ fmtMoney(l.gas_reading) }} {{ $t('houses.unitYuan') }}</span>
              </div>
              <div v-if="l.notes" class="lc-notes">{{ l.notes }}</div>
            </div>
          </el-tab-pane>

          <el-tab-pane v-if="curHouse.display_status !== 'selfuse'" :label="`${$t('houses.tabExpenses')} (${detail.expenses.length})`" name="expenses">
            <div class="pane-head">
              <el-button type="primary" size="small" :icon="Plus" @click="openExpenseDlg()">{{ $t('houses.addExpense') }}</el-button>
              <span v-if="detail.expenses.length" class="exp-total">{{ $t('houses.expenseTotal') }} ¥{{ fmtMoney(expenseTotal) }}</span>
            </div>
            <el-table v-if="detail.expenses.length" :data="detail.expenses" size="small">
              <el-table-column :label="$t('houses.expenseName')" prop="name" min-width="120" show-overflow-tooltip />
              <el-table-column :label="$t('houses.expenseAmount')" width="100" align="right">
                <template #default="{ row }">¥{{ fmtMoney(row.amount) }}</template>
              </el-table-column>
              <el-table-column :label="$t('houses.expenseDate')" prop="spend_date" width="110" />
              <el-table-column :label="$t('houses.notes')" prop="notes" min-width="100" show-overflow-tooltip />
              <el-table-column width="90" align="right">
                <template #default="{ row }">
                  <el-button text size="small" :icon="EditPen" @click="openExpenseDlg(row)" />
                  <el-button text size="small" type="danger" :icon="Delete" @click="delExpense(row)" />
                </template>
              </el-table-column>
            </el-table>
            <el-empty v-else :description="$t('houses.expensesEmpty')" :image-size="60" />
          </el-tab-pane>
        </el-tabs>
      </template>
      <el-empty v-else :description="$t('houses.selectHouse')" />
    </main>

    <!-- 房屋编辑弹层 -->
    <el-dialog v-model="houseDlg.open" :title="houseForm.id ? $t('houses.editHouse') : $t('houses.addHouse')"
      :width="isMobile ? '94%' : '620px'" append-to-body :close-on-click-modal="false">
      <el-form :model="houseForm" label-width="110px">
        <el-form-item :label="$t('houses.houseName')" required>
          <el-input v-model="houseForm.name" :placeholder="$t('houses.houseNamePh')" maxlength="50" />
        </el-form-item>
        <el-form-item :label="$t('houses.address')">
          <el-input v-model="houseForm.address" :placeholder="$t('houses.addressPh')" maxlength="200" />
        </el-form-item>
        <el-form-item :label="$t('houses.area') + ' / ' + $t('houses.layout')">
          <div class="frm-2col">
            <el-input v-model="houseForm.area" :placeholder="$t('houses.areaPh')" maxlength="50" />
            <el-input v-model="houseForm.layout" :placeholder="$t('houses.layoutPh')" maxlength="50" />
          </div>
        </el-form-item>
        <el-form-item :label="$t('houses.statusVacant') + '/' + $t('houses.statusSelfuse')">
          <el-radio-group v-model="houseForm.status">
            <el-radio value="vacant">{{ $t('houses.statusVacant') }}</el-radio>
            <el-radio value="selfuse">{{ $t('houses.statusSelfuse') }}</el-radio>
          </el-radio-group>
          <div class="frm-hint">{{ $t('houses.statusRented') }} = {{ $t('houses.addLeaseDisabled') }}</div>
        </el-form-item>
        <el-form-item :label="$t('houses.cardSec')">
          <div class="frm-3col">
            <el-input v-model="houseForm.water_card" :placeholder="$t('houses.waterCard')" maxlength="50" />
            <el-input v-model="houseForm.electric_card" :placeholder="$t('houses.electricCard')" maxlength="50" />
            <el-input v-model="houseForm.gas_card" :placeholder="$t('houses.gasCard')" maxlength="50" />
          </div>
        </el-form-item>
        <el-form-item :label="$t('houses.rentRemind')">
          <div class="frm-rent">
            <el-switch :model-value="rentOn" @update:model-value="toggleRent" />
            <template v-if="rentOn">
              <el-input-number v-model="houseForm.rent_due_day" :min="1" :max="31" :controls="false" style="width: 64px" />
              <span>{{ $t('houses.rentDayUnit') }}</span>
              <el-time-picker v-model="houseForm.rent_due_time" format="HH:mm" value-format="HH:mm" style="width: 100px" />
            </template>
          </div>
          <div class="frm-hint">{{ $t('houses.rentHint') }}</div>
        </el-form-item>
        <el-form-item :label="$t('houses.notes')">
          <el-input v-model="houseForm.notes" type="textarea" :rows="2" :placeholder="$t('houses.notesPh')" maxlength="500" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="houseDlg.open = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="saveHouse">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 家具电器弹层 -->
    <el-dialog v-model="itemDlg.open" :title="itemForm.id ? $t('houses.editItemTitle') : $t('houses.addItemTitle')"
      :width="isMobile ? '94%' : '480px'" append-to-body :close-on-click-modal="false">
      <el-form :model="itemForm" label-width="90px">
        <el-form-item :label="$t('houses.category')">
          <el-radio-group v-model="itemForm.category">
            <el-radio value="furniture">{{ $t('houses.catFurniture') }}</el-radio>
            <el-radio value="appliance">{{ $t('houses.catAppliance') }}</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="$t('houses.itemName')" required>
          <el-input v-model="itemForm.name" :placeholder="$t('houses.itemNamePh')" maxlength="50" />
        </el-form-item>
        <el-form-item :label="$t('houses.brandModel')">
          <el-input v-model="itemForm.brand_model" :placeholder="$t('houses.brandPh')" maxlength="100" />
        </el-form-item>
        <el-form-item :label="$t('houses.itemStatus')">
          <div class="frm-2col">
            <el-select v-model="itemForm.status">
              <el-option value="active" :label="$t('houses.stActive')" />
              <el-option value="broken" :label="$t('houses.stBroken')" />
              <el-option value="removed" :label="$t('houses.stRemoved')" />
            </el-select>
            <el-input-number v-model="itemForm.qty" :min="1" :max="999" />
          </div>
        </el-form-item>
        <el-form-item :label="$t('houses.inContract')">
          <div class="frm-rent">
            <el-switch v-model="itemForm.in_contract" />
            <span class="frm-hint-inline">{{ $t('houses.inContractHint') }}</span>
          </div>
        </el-form-item>
        <el-form-item :label="$t('houses.notes')">
          <el-input v-model="itemForm.notes" type="textarea" :rows="2" maxlength="200" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="itemDlg.open = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="saveItem">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 租约弹层 -->
    <el-dialog v-model="leaseDlg.open" :title="leaseForm.id ? $t('houses.editLeaseTitle') : $t('houses.leaseTitle')"
      :width="isMobile ? '94%' : '560px'" append-to-body :close-on-click-modal="false">
      <el-form :model="leaseForm" label-width="100px">
        <el-form-item :label="$t('houses.tenantName')" required>
          <el-input v-model="leaseForm.tenant_name" :placeholder="$t('houses.tenantNamePh')" maxlength="50" />
        </el-form-item>
        <el-form-item :label="$t('houses.tenantPhone')">
          <el-input v-model="leaseForm.tenant_phone" maxlength="30" />
        </el-form-item>
        <el-form-item :label="$t('houses.tenantIDNo')">
          <el-input v-model="leaseForm.tenant_id_no" maxlength="30" />
        </el-form-item>
        <el-form-item :label="$t('houses.ownerSec')">
          <div class="frm-3col">
            <el-input v-model="leaseForm.owner_name" :placeholder="$t('houses.ownerName')" maxlength="50" />
            <el-input v-model="leaseForm.owner_phone" :placeholder="$t('houses.ownerPhone')" maxlength="30" />
            <el-input v-model="leaseForm.owner_id_no" :placeholder="$t('houses.ownerIDNo')" maxlength="30" />
          </div>
        </el-form-item>
        <el-form-item :label="$t('houses.leasePeriod')" required>
          <div class="frm-2col">
            <el-date-picker v-model="leaseForm.start_date" type="date" value-format="YYYY-MM-DD" :clearable="false" style="width: 100%" />
            <el-date-picker v-model="leaseForm.end_date" type="date" value-format="YYYY-MM-DD" :clearable="false" style="width: 100%" />
          </div>
        </el-form-item>
        <el-form-item :label="$t('houses.monthlyRent')">
          <el-input-number v-model="leaseForm.monthly_rent" :min="0" :max="9999999" :precision="2" :controls="false" style="width: 100%" />
        </el-form-item>
        <el-form-item :label="$t('houses.deposit')">
          <el-input-number v-model="leaseForm.deposit" :min="0" :max="9999999" :precision="2" :controls="false" style="width: 100%" />
        </el-form-item>
        <el-form-item :label="$t('houses.utility')">
          <div class="frm-3col">
            <div class="frm-unit"><el-input-number v-model="leaseForm.water_reading" :min="0" :max="999999999" :precision="2" :controls="false" style="width: 100%" /><span class="frm-unit-suffix">{{ $t('houses.unitL') }}</span></div>
            <div class="frm-unit"><el-input-number v-model="leaseForm.electric_reading" :min="0" :max="999999999" :precision="2" :controls="false" style="width: 100%" /><span class="frm-unit-suffix">{{ $t('houses.unitYuan') }}</span></div>
            <div class="frm-unit"><el-input-number v-model="leaseForm.gas_reading" :min="0" :max="999999999" :precision="2" :controls="false" style="width: 100%" /><span class="frm-unit-suffix">{{ $t('houses.unitYuan') }}</span></div>
          </div>
          <div class="frm-hint">{{ $t('houses.utilityHint') }}</div>
        </el-form-item>
        <el-form-item :label="$t('houses.paymentCycle')">
          <el-input v-model="leaseForm.payment_cycle" :placeholder="$t('houses.cyclePh')" maxlength="20" />
        </el-form-item>
        <el-form-item :label="$t('houses.notes')">
          <el-input v-model="leaseForm.notes" type="textarea" :rows="2" :placeholder="$t('houses.notesPh')" maxlength="500" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="leaseDlg.open = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="saveLease">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 消耗费用弹层 -->
    <el-dialog v-model="expenseDlg.open" :title="expenseForm.id ? $t('houses.editExpenseTitle') : $t('houses.addExpense')"
      :width="isMobile ? '94%' : '480px'" append-to-body :close-on-click-modal="false">
      <el-form :model="expenseForm" label-width="90px">
        <el-form-item :label="$t('houses.expenseName')" required>
          <el-input v-model="expenseForm.name" :placeholder="$t('houses.expenseNamePh')" maxlength="50" />
        </el-form-item>
        <el-form-item :label="$t('houses.expenseAmount')">
          <el-input-number v-model="expenseForm.amount" :min="0" :max="10000000" :precision="2" :controls="false" style="width: 100%" />
        </el-form-item>
        <el-form-item :label="$t('houses.expenseDate')" required>
          <el-date-picker v-model="expenseForm.spend_date" type="date" value-format="YYYY-MM-DD" :clearable="false" style="width: 100%" />
        </el-form-item>
        <el-form-item :label="$t('houses.notes')">
          <el-input v-model="expenseForm.notes" type="textarea" :rows="2" maxlength="200" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="expenseDlg.open = false">{{ $t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="saveExpense">{{ $t('common.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 收支统计弹层：年度/季度 总收入、消耗费用、实际收入 -->
    <el-dialog v-model="incomeDlg.open" :title="$t('houses.incomeTitle')"
      :width="isMobile ? '94%' : '560px'" append-to-body>
      <div class="income-head">
        <el-select v-model="incomeYear" size="small" style="width: 130px" @change="loadIncome">
          <el-option v-for="y in incomeYears" :key="y" :value="y" :label="String(y)" />
        </el-select>
      </div>
      <div class="income-cards">
        <div class="income-card">
          <span>{{ $t('houses.incomeGross') }}</span>
          <b>¥{{ fmtMoney(incomeData.year_income?.gross) }}</b>
        </div>
        <div class="income-card">
          <span>{{ $t('houses.incomeExpense') }}</span>
          <b>¥{{ fmtMoney(incomeData.year_income?.expense) }}</b>
        </div>
        <div class="income-card">
          <span>{{ $t('houses.incomeNet') }}</span>
          <b :class="{ neg: (incomeData.year_income?.net || 0) < 0 }">¥{{ fmtMoney(incomeData.year_income?.net) }}</b>
        </div>
      </div>
      <el-table :data="incomeData.quarters || []" size="small">
        <el-table-column :label="$t('houses.quarter')" width="90">
          <template #default="{ row }">{{ quarterLabel(row.q) }}</template>
        </el-table-column>
        <el-table-column :label="$t('houses.incomeGross')" align="right">
          <template #default="{ row }">¥{{ fmtMoney(row.gross) }}</template>
        </el-table-column>
        <el-table-column :label="$t('houses.incomeExpense')" align="right">
          <template #default="{ row }">¥{{ fmtMoney(row.expense) }}</template>
        </el-table-column>
        <el-table-column :label="$t('houses.incomeNet')" align="right">
          <template #default="{ row }">
            <span :class="{ neg: (row.net || 0) < 0 }">¥{{ fmtMoney(row.net) }}</span>
          </template>
        </el-table-column>
      </el-table>
      <div class="income-hint">{{ $t('houses.incomeHint') }}</div>
    </el-dialog>

    <!-- 合同模板管理：统一模板上传/恢复，内置标准模板兜底 -->
    <el-dialog v-model="tplDlg.open" :title="$t('houses.tplTitle')"
      :width="isMobile ? '94%' : '560px'" append-to-body>
      <div class="tpl-cur">
        <span class="tpl-cur-label">{{ $t('houses.tplCurrent') }}</span>
        <el-tag v-if="!tplInfo.custom" size="small">{{ $t('houses.tplBuiltin') }}</el-tag>
        <template v-else>
          <el-tag size="small" type="warning">{{ $t('houses.tplCustom') }}</el-tag>
          <span class="tpl-cur-name">{{ tplInfo.name }}（{{ $t('houses.tplUploadedAt') }} {{ tplInfo.uploaded_at }}）</span>
        </template>
      </div>
      <div class="tpl-ops">
        <el-upload :auto-upload="false" :show-file-list="false" accept=".docx" :on-change="onTplFile">
          <el-button type="primary" size="small" :icon="Upload">{{ $t('houses.tplUpload') }}</el-button>
        </el-upload>
        <el-button v-if="tplInfo.custom" size="small" type="danger" plain :icon="RefreshLeft" @click="restoreTpl">{{ $t('houses.tplRestore') }}</el-button>
      </div>
      <div class="income-hint">{{ $t('houses.tplUploadHint') }}</div>
      <div class="tpl-fields">
        <div class="info-title">{{ $t('houses.tplFields') }}</div>
        <div class="tpl-field-tags">
          <el-tag v-for="f in tplFields" :key="f" size="small" class="tpl-field-tag">{{ tplToken(f) }}</el-tag>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup>
// 房屋管理视图：总览统计（服务端现算）+ 房屋/家具电器/租约 CRUD + 合同 docx
// 下载（fetch blob，解析 RFC 5987 中文文件名）+ 卡号一键复制。弹层一律
// append-to-body（玻璃主题 .main backdrop-filter 会吃 fixed 锚点）。
defineOptions({ name: 'HouseView' })
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, Delete, Download, EditPen, Notebook, Plus, Promotion, RefreshLeft, Upload } from '@element-plus/icons-vue'
import { api, getToken } from '../api/index.js'
import { useIsMobile } from '../composables/useIsMobile.js'
import { useSidePanel } from '../composables/useSidePanel.js'
import { useI18n } from 'vue-i18n'
import { registerPageMessages } from '../i18n'
import pageMsgs from '../i18n/pages/houses.js'

registerPageMessages(pageMsgs)
const { t: tr, te } = useI18n()

const isMobile = useIsMobile()
const side = useSidePanel('houses', 320)
const loading = ref(false)
const saving = ref(false)
const list = ref([])
const stats = ref({})
const cur = ref(null)
const detail = ref({ house: null, items: [], leases: [], expenses: [] })
const tab = ref('info')
// 收支统计（GET /house-income）：年度与四季度 总收入/消耗费用/实际收入
const incomeData = ref({ year_income: {}, quarters: [] })
const incomeDlg = reactive({ open: false })
const incomeYear = ref(new Date().getFullYear())
const incomeYears = Array.from({ length: 6 }, (_, i) => new Date().getFullYear() - i)

const curHouse = computed(() => detail.value.house)
// 自住房屋不显示租约/消耗 tab，切到自住时若停在这两个 tab 回到信息页
watch(() => curHouse.value?.display_status, (s) => {
  if (s === 'selfuse' && (tab.value === 'leases' || tab.value === 'expenses')) tab.value = 'info'
})
const hasActive = computed(() => detail.value.leases.some((l) => l.status === 'active'))
const occPct = computed(() => (stats.value.total ? Math.round(((stats.value.rented || 0) / stats.value.total) * 100) : 0))
const occText = computed(() => tr('houses.occDetail', {
  rented: stats.value.rented || 0, selfuse: stats.value.selfuse || 0, vacant: stats.value.vacant || 0,
}))

const cardRows = computed(() => {
  const h = curHouse.value || {}
  return [
    { label: tr('houses.waterCard'), val: h.water_card },
    { label: tr('houses.electricCard'), val: h.electric_card },
    { label: tr('houses.gasCard'), val: h.gas_card },
  ]
})

// i18n 动态键（status/item 状态）走 te 探测回落，避免缺键裸键名
function statusLabel(s) {
  const k = `houses.status${s === 'rented' ? 'Rented' : s === 'selfuse' ? 'Selfuse' : 'Vacant'}`
  return te(k) ? tr(k) : s
}
function catLabel(c) { return c === 'appliance' ? tr('houses.catAppliance') : tr('houses.catFurniture') }
function itemStLabel(s) {
  return s === 'broken' ? tr('houses.stBroken') : s === 'removed' ? tr('houses.stRemoved') : tr('houses.stActive')
}
function itemStTag(s) {
  return s === 'broken' ? 'danger' : s === 'removed' ? 'info' : 'success'
}
function daysCls(l) {
  if (l.remaining_days <= 7) return 'd-red'
  if (l.remaining_days <= 30) return 'd-orange'
  return 'd-green'
}
function daysLabel(l) {
  if (l.remaining_days < 0) return tr('houses.daysOverdue', { n: -l.remaining_days })
  return tr('houses.daysLeft', { n: l.remaining_days })
}
function fmtMoney(v) {
  const n = Number(v) || 0
  return n % 1 === 0 ? String(n) : n.toFixed(2)
}

async function loadList() {
  const res = await api.get('/houses')
  list.value = res.items || []
  stats.value = res.stats || {}
  if (cur.value && !list.value.some((h) => h.id === cur.value)) {
    cur.value = null
    detail.value = { house: null, items: [], leases: [], expenses: [] }
  }
}
// 详情请求序号：快速切换房源时旧响应到达即丢弃，避免慢响应覆盖新选中房
let detailSeq = 0
async function loadDetail(id) {
  if (!id) return
  const seq = ++detailSeq
  const res = await api.get(`/houses/${id}`)
  if (seq !== detailSeq) return // 已切换到别的房源，过期响应不落地
  detail.value = { house: res.house, items: res.items || [], leases: res.leases || [], expenses: res.expenses || [] }
}
async function selectHouse(id) {
  cur.value = id
  try {
    await loadDetail(id)
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function refresh() {
  // 总览与详情互不依赖，并行取（保存/删除后整页回刷更快）
  await Promise.all([loadList(), cur.value ? loadDetail(cur.value) : Promise.resolve()])
  loadIncome().catch(() => {}) // 收支卡随数据变动刷新（失败不阻塞主流程）
}

// ---- 收支统计 ----
const curQuarterNet = computed(() => {
  const q = Math.floor(new Date().getMonth() / 3) // 0-based
  return incomeData.value.quarters?.[q]?.net ?? 0
})
function quarterLabel(q) {
  return [tr('houses.q1'), tr('houses.q2'), tr('houses.q3'), tr('houses.q4')][q - 1] || String(q)
}
async function loadIncome() {
  const res = await api.get(`/house-income?year=${incomeYear.value}`)
  incomeData.value = res
}
function openIncome() {
  incomeDlg.open = true
  loadIncome().catch((e) => ElMessage.error(e.message))
}

// ---- 消耗费用 ----
const expenseDlg = reactive({ open: false })
const expenseForm = reactive({ id: null, name: '', amount: 0, spend_date: '', notes: '' })
const expenseTotal = computed(() => detail.value.expenses.reduce((sum, e) => sum + (Number(e.amount) || 0), 0))

function openExpenseDlg(e) {
  if (!cur.value) {
    ElMessage.warning(tr('houses.itemHouseErr'))
    return
  }
  Object.assign(expenseForm, { id: null, name: '', amount: 0, spend_date: new Date().toISOString().slice(0, 10), notes: '' })
  if (e) Object.assign(expenseForm, { id: e.id, name: e.name, amount: e.amount, spend_date: e.spend_date, notes: e.notes })
  expenseDlg.open = true
}
async function saveExpense() {
  saving.value = true
  try {
    const body = { ...expenseForm, house_id: cur.value }
    if (body.id) await api.put(`/house-expenses/${body.id}`, body)
    else await api.post('/house-expenses', body)
    expenseDlg.open = false
    await refresh()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}
async function delExpense(e) {
  try {
    await ElMessageBox.confirm(tr('houses.confirmDel', { name: e.name }), '', { type: 'warning' })
  } catch { return }
  try {
    await api.del(`/house-expenses/${e.id}`)
    await refresh()
  } catch (err) {
    ElMessage.error(err.message)
  }
}

// ---- 房屋 ----
const houseDlg = reactive({ open: false })
const houseForm = reactive({ id: null, name: '', address: '', area: '', layout: '', status: 'vacant',
  owner_name: '', owner_phone: '', owner_id_no: '', water_card: '', electric_card: '', gas_card: '',
  rent_due_day: 0, rent_due_time: '10:00', notes: '' })

// rentOn 收租提醒开关（映射 rent_due_day 0↔1..31）
const rentOn = computed(() => houseForm.rent_due_day > 0)
function toggleRent(v) {
  houseForm.rent_due_day = v ? (houseForm.rent_due_day > 0 ? houseForm.rent_due_day : 1) : 0
}

function openHouseDlg(h) {
  Object.assign(houseForm, { id: null, name: '', address: '', area: '', layout: '', status: 'vacant',
    owner_name: '', owner_phone: '', owner_id_no: '', water_card: '', electric_card: '', gas_card: '',
    rent_due_day: 0, rent_due_time: '10:00', notes: '' })
  if (h) {
    Object.assign(houseForm, {
      id: h.id, name: h.name, address: h.address, area: h.area, layout: h.layout, status: h.status,
      owner_name: h.owner_name, owner_phone: h.owner_phone, owner_id_no: h.owner_id_no,
      water_card: h.water_card, electric_card: h.electric_card, gas_card: h.gas_card,
      rent_due_day: h.rent_due_day || 0, rent_due_time: h.rent_due_time || '10:00', notes: h.notes,
    })
  }
  houseDlg.open = true
}
async function saveHouse() {
  if (!houseForm.name.trim()) {
    ElMessage.warning(tr('houses.requiredName'))
    return
  }
  saving.value = true
  try {
    const body = { ...houseForm }
    if (body.id) await api.put(`/houses/${body.id}`, body)
    else await api.post('/houses', body)
    houseDlg.open = false
    await refresh()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}
async function delHouse(h) {
  try {
    await ElMessageBox.confirm(tr('houses.delHouseConfirm', { name: h.name }), tr('houses.delHouse'), { type: 'warning' })
  } catch { return }
  try {
    await api.del(`/houses/${h.id}`)
    await loadList()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 家具电器 ----
const itemDlg = reactive({ open: false })
const itemForm = reactive({ id: null, category: 'furniture', name: '', brand_model: '', qty: 1, status: 'active', in_contract: true, notes: '' })

function openItemDlg(i) {
  if (!cur.value) {
    ElMessage.warning(tr('houses.itemHouseErr'))
    return
  }
  Object.assign(itemForm, { id: null, category: 'furniture', name: '', brand_model: '', qty: 1, status: 'active', in_contract: true, notes: '' })
  if (i) Object.assign(itemForm, { id: i.id, category: i.category, name: i.name, brand_model: i.brand_model, qty: i.qty, status: i.status, in_contract: !!i.in_contract, notes: i.notes })
  itemDlg.open = true
}
async function saveItem() {
  saving.value = true
  try {
    const body = { ...itemForm, house_id: cur.value }
    if (body.id) await api.put(`/house-items/${body.id}`, body)
    else await api.post('/house-items', body)
    itemDlg.open = false
    await refresh()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}
async function delItem(i) {
  try {
    await ElMessageBox.confirm(tr('houses.confirmDel', { name: i.name }), '', { type: 'warning' })
  } catch { return }
  try {
    await api.del(`/house-items/${i.id}`)
    await refresh()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// toggleItemContract 表格内快速勾选「加入物品清单」（整行回写）
async function toggleItemContract(row) {
  try {
    await api.put(`/house-items/${row.id}`, { ...row, in_contract: !row.in_contract })
    row.in_contract = !row.in_contract
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 合同模板 ----
const tplDlg = reactive({ open: false })
const tplInfo = ref({ custom: false, name: '', uploaded_at: '' })
// 与后端 contractData 字段对齐（中文占位符名，两种语言下都是模板 token 原样展示）
const tplFields = ['甲方姓名', '甲方身份证号', '甲方电话', '乙方姓名', '乙方身份证号', '乙方电话', '房屋地址', '建筑面积', '户型', '租期开始', '租期结束', '月租金', '押金', '付款方式', '水卡号', '电卡号', '燃气卡号', '家具电器清单', '其他约定', '签订日期']
// tplToken 模板占位符展示串（字面双大括号不能直接写进 Vue 插值表达式，挪到 JS 拼）
function tplToken(f) {
  return '{{' + f + '}}'
}

function openTpl() {
  tplDlg.open = true
  api.get('/house-contract-template').then((res) => {
    tplInfo.value = res
  }).catch((e) => ElMessage.error(e.message))
}
function onTplFile(file) {
  const raw = file?.raw
  if (!raw) return
  if (!/\.docx$/i.test(raw.name || '')) {
    ElMessage.warning(tr('houses.tplNotDocx'))
    return
  }
  if (raw.size > 5 * 1024 * 1024) {
    // 前端预检上限，免得大文件全量读入 + base64 后才被服务端拒绝
    ElMessage.warning(tr('houses.tplTooLarge'))
    return
  }
  const reader = new FileReader()
  reader.onload = async () => {
    try {
      const buf = new Uint8Array(reader.result)
      let bin = ''
      const chunk = 0x8000
      for (let i = 0; i < buf.length; i += chunk) {
        bin += String.fromCharCode.apply(null, buf.subarray(i, i + chunk))
      }
      await api.post('/house-contract-template', { filename: raw.name, data: btoa(bin) })
      ElMessage.success(tr('houses.tplUploaded'))
      tplInfo.value = await api.get('/house-contract-template')
    } catch (e) {
      ElMessage.error(e.message)
    }
  }
  reader.onerror = () => ElMessage.error(tr('houses.tplFileFail'))
  reader.readAsArrayBuffer(raw)
}
async function restoreTpl() {
  try {
    await ElMessageBox.confirm(tr('houses.tplRestore') + '?', '', { type: 'warning' })
  } catch { return }
  try {
    await api.del('/house-contract-template')
    tplInfo.value = await api.get('/house-contract-template')
    ElMessage.success(tr('houses.tplRestored'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 租约 ----
const leaseDlg = reactive({ open: false })
const leaseForm = reactive({ id: null, tenant_name: '', tenant_phone: '', tenant_id_no: '',
  owner_name: '', owner_phone: '', owner_id_no: '', water_reading: 0, electric_reading: 0, gas_reading: 0,
  start_date: '', end_date: '', monthly_rent: 0, deposit: 0, payment_cycle: '', notes: '' })

function openLeaseDlg(l) {
  if (!cur.value) return
  const today = new Date().toISOString().slice(0, 10)
  Object.assign(leaseForm, { id: null, tenant_name: '', tenant_phone: '', tenant_id_no: '',
    owner_name: curHouse.value?.owner_name || '', owner_phone: curHouse.value?.owner_phone || '', owner_id_no: curHouse.value?.owner_id_no || '',
    water_reading: 0, electric_reading: 0, gas_reading: 0,
    start_date: today, end_date: '', monthly_rent: 0, deposit: 0, payment_cycle: '', notes: '' })
  if (l) {
    Object.assign(leaseForm, { id: l.id, tenant_name: l.tenant_name, tenant_phone: l.tenant_phone, tenant_id_no: l.tenant_id_no,
      owner_name: l.owner_name, owner_phone: l.owner_phone, owner_id_no: l.owner_id_no,
      water_reading: l.water_reading || 0, electric_reading: l.electric_reading || 0, gas_reading: l.gas_reading || 0,
      start_date: l.start_date, end_date: l.end_date, monthly_rent: l.monthly_rent, deposit: l.deposit,
      payment_cycle: l.payment_cycle, notes: l.notes })
  }
  leaseDlg.open = true
}
async function saveLease() {
  saving.value = true
  try {
    const body = { ...leaseForm, house_id: cur.value }
    if (body.id) await api.put(`/house-leases/${body.id}`, body)
    else await api.post('/house-leases', body)
    leaseDlg.open = false
    await refresh()
  } catch (e) {
    ElMessage.error(e.message)
  } finally {
    saving.value = false
  }
}
async function renewLease(l) {
  try {
    await ElMessageBox.confirm(tr('houses.renewConfirm', { name: l.tenant_name }), tr('houses.renew'), { type: 'info' })
  } catch { return }
  try {
    await api.post(`/house-leases/${l.id}/renew`)
    ElMessage.success(tr('houses.renew'))
    await refresh()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function endLease(l) {
  try {
    await ElMessageBox.confirm(tr('houses.endLeaseConfirm', { name: l.tenant_name }), tr('houses.endLease'), { type: 'warning' })
  } catch { return }
  try {
    await api.post(`/house-leases/${l.id}/status`, { status: 'ended' })
    await refresh()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function restoreLease(l) {
  try {
    await ElMessageBox.confirm(tr('houses.restoreLeaseConfirm'), tr('houses.restoreLease'), { type: 'warning' })
  } catch { return }
  try {
    await api.post(`/house-leases/${l.id}/status`, { status: 'active' })
    await refresh()
  } catch (e) {
    ElMessage.error(e.message)
  }
}
async function delLease(l) {
  try {
    await ElMessageBox.confirm(tr('houses.confirmDel', { name: l.tenant_name }), '', { type: 'warning' })
  } catch { return }
  try {
    await api.del(`/house-leases/${l.id}`)
    await refresh()
  } catch (e) {
    ElMessage.error(e.message)
  }
}

async function testLeasePush(l) {
  try {
    await api.post(`/house-leases/${l.id}/test-push`)
    ElMessage.success(tr('houses.pushSent'))
  } catch (e) {
    ElMessage.error(e.message)
  }
}

// ---- 合同下载：fetch blob（带会话鉴权），解析 attachment 文件名（中文走
// RFC 5987 filename*，回落 filename=，再回落本地拼名） ----
async function downloadContract(l) {
  const headers = {}
  const token = getToken()
  if (token) headers.Authorization = 'Bearer ' + token
  let res
  try {
    res = await fetch(`/api/v1/house-leases/${l.id}/contract`, { headers })
  } catch {
    ElMessage.error(tr('houses.contractFail'))
    return
  }
  if (!res.ok) {
    let msg = tr('houses.contractFail')
    try { msg = (await res.json()).error || msg } catch { /* 保底文案 */ }
    ElMessage.error(msg)
    return
  }
  const blob = await res.blob()
  const cd = res.headers.get('Content-Disposition') || ''
  let name = `contract-${curHouse.value?.name || ''}-${l.tenant_name}.docx`
  const m = cd.match(/filename\*=UTF-8''([^;]+)/i) || cd.match(/filename="?([^";]+)"?/i)
  if (m) {
    try { name = decodeURIComponent(m[1]) } catch { name = m[1] }
  }
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  // 延迟 revoke：Safari/Firefox 下 click 后立即释放 blob URL 可能中断下载
  setTimeout(() => URL.revokeObjectURL(url), 10000)
}

// ---- 卡号复制：一键复制三个（空的不带），逐条另有小按钮 ----
async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // 非安全上下文/权限拒绝：退回手工选中复制
    const el = document.createElement('textarea')
    el.value = text
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    document.body.removeChild(el)
  }
}
async function copyCards() {
  const parts = cardRows.value.filter((c) => c.val).map((c) => `${c.label}：${c.val}`)
  if (!parts.length) {
    ElMessage.warning(tr('houses.noCards'))
    return
  }
  await copyText(parts.join('\n'))
  ElMessage.success(tr('houses.cardsCopied'))
}
async function copyOne(val) {
  await copyText(val)
  ElMessage.success(tr('houses.copied'))
}

onMounted(() => {
  loadList()
  loadIncome().catch(() => {})
})
</script>

<style scoped>
.house-page {
  display: flex;
  gap: 0;
  height: calc(100vh - 130px);
}
.house-side {
  flex: none;
  display: flex;
  flex-direction: column;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  min-height: 0;
  overflow: hidden;
}
.side-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 14px 10px;
  border-bottom: 1px solid var(--border);
}
.side-head-btns {
  display: flex;
  align-items: center;
  gap: 6px;
}
.frm-hint-inline {
  font-size: 11px;
  color: var(--muted);
}
.tpl-cur {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
  min-width: 0;
}
.tpl-cur-label {
  flex: none;
  font-size: 12px;
  color: var(--muted);
}
.tpl-cur-name {
  font-size: 12px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tpl-ops {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}
.tpl-fields {
  margin-top: 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 14px;
}
.tpl-field-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.tpl-field-tag {
  font-family: ui-monospace, Menlo, Consolas, monospace;
}
.side-title {
  font-size: 15px;
  font-weight: 600;
}
.stat-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--border);
}
.stat-card {
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--el-fill-color-light);
  min-width: 0;
}
.stat-btn {
  border: none;
  cursor: pointer;
  text-align: left;
  font: inherit;
  color: inherit;
  width: 100%;
}
.stat-btn:hover {
  outline: 1px solid var(--el-color-primary-light-5);
}
.stat-value.neg {
  color: var(--el-color-danger);
}
.stat-label {
  font-size: 11px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.stat-value {
  margin-top: 2px;
  font-size: 16px;
  font-weight: 700;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.house-list {
  flex: 1;
  overflow-y: auto;
  padding: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.list-empty {
  padding: 20px 8px;
  text-align: center;
  font-size: 12px;
  color: var(--muted);
}
.house-card {
  text-align: left;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: transparent;
  padding: 9px 11px;
  cursor: pointer;
  display: block;
  width: 100%;
  color: inherit;
  font: inherit;
}
.house-card:hover {
  background: var(--el-fill-color-light);
}
.house-card.active {
  border-color: var(--el-color-primary);
  background: var(--primary-weak, rgba(64, 158, 255, 0.14));
}
.hc-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}
.hc-name {
  font-weight: 600;
  font-size: 13px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.hc-tag {
  flex: none;
  font-size: 11px;
  line-height: 18px;
  padding: 0 7px;
  border-radius: 4px;
}
.hc-tag.st-rented {
  background: var(--el-color-success-light-9);
  color: var(--el-color-success);
}
.hc-tag.st-selfuse {
  background: rgba(155, 110, 243, 0.18);
  color: #b08cf7;
}
.hc-tag.st-vacant {
  background: var(--el-fill-color);
  color: var(--muted);
}
.hc-addr {
  margin-top: 3px;
  font-size: 11px;
  color: var(--muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.hc-sub {
  margin-top: 4px;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  color: var(--muted);
  min-width: 0;
}
.hc-days {
  margin-left: auto;
  flex: none;
}
.hc-rent {
  flex: none;
  padding: 0 5px;
  border-radius: 4px;
  background: var(--el-color-warning-light-9);
  color: var(--el-color-warning);
}
.hc-days.d-green {
  color: var(--el-color-success);
}
.hc-days.d-orange {
  color: var(--el-color-warning);
}
.hc-days.d-red {
  color: var(--el-color-danger);
  font-weight: 600;
}

.house-main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 14px 18px 20px;
}
.m-stats {
  border-bottom: 1px solid var(--border);
  padding: 0 0 10px;
  margin-bottom: 10px;
}
.m-pick {
  width: 150px;
}
.detail-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}
.detail-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.detail-title h2 {
  margin: 0;
  font-size: 17px;
}
.detail-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.detail-addr {
  margin-top: 2px;
  font-size: 12px;
  color: var(--muted);
}
.detail-tabs {
  margin-top: 8px;
}
.info-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 12px;
}
.info-block {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 14px;
}
.info-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--muted);
  margin-bottom: 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.info-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  padding: 3px 0;
  font-size: 13px;
  min-width: 0;
}
.info-row span {
  flex: none;
  width: 64px;
  color: var(--muted);
  font-size: 12px;
}
.info-row b {
  font-weight: 500;
  word-break: break-all;
}
.info-row.col {
  flex-direction: column;
  gap: 2px;
}
.info-row.col span {
  width: auto;
}
.mono {
  font-family: ui-monospace, Menlo, Consolas, monospace;
}
.pane-head {
  margin-bottom: 10px;
  display: flex;
  align-items: center;
  gap: 12px;
}
.exp-total {
  font-size: 12px;
  color: var(--muted);
}
.income-head {
  margin-bottom: 10px;
}
.income-cards {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr;
  gap: 8px;
  margin-bottom: 12px;
}
.income-card {
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--el-fill-color-light);
}
.income-card span {
  display: block;
  font-size: 11px;
  color: var(--muted);
}
.income-card b {
  display: block;
  margin-top: 2px;
  font-size: 16px;
  font-weight: 700;
}
.income-card b.neg {
  color: var(--el-color-danger);
}
.income-hint {
  margin-top: 10px;
  font-size: 11px;
  color: var(--muted);
  line-height: 1.6;
}
.lease-card {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 14px;
  margin-bottom: 10px;
}
.lease-card.ended {
  opacity: 0.72;
}
.lc-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.lc-name {
  font-size: 14px;
}
.lc-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0;
}
.lc-grid {
  margin-top: 6px;
  display: flex;
  flex-wrap: wrap;
  gap: 4px 18px;
  font-size: 12px;
  color: var(--muted);
}
.lc-notes {
  margin-top: 6px;
  font-size: 12px;
  color: var(--muted);
  border-top: 1px dashed var(--border);
  padding-top: 6px;
  white-space: pre-wrap;
}
.frm-2col {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  width: 100%;
}
.frm-3col {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr;
  gap: 8px;
  width: 100%;
}
.frm-hint {
  width: 100%;
  font-size: 11px;
  color: var(--muted);
  line-height: 1.5;
}
.frm-rent {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}
.frm-unit {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
.frm-unit-suffix {
  flex: none;
  font-size: 11px;
  color: var(--muted);
}

.house-page.mobile {
  flex-direction: column;
  height: auto;
}
.house-page.mobile .house-main {
  margin-top: 10px;
  padding: 12px;
}
</style>
