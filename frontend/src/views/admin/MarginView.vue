<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.margin.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.margin.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <Select v-model="hours" :options="hoursOptions" class="w-36" @change="loadAll" />
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="loadAll">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" @click="showSettings = true">
            <Icon name="cog" size="sm" />
            {{ t('admin.margin.fuseSettings') }}
          </button>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-3 md:grid-cols-4">
        <div v-for="card in overviewCards" :key="card.key" class="rounded-lg border border-gray-100 bg-white px-4 py-3 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ card.label }}</p>
          <p class="mt-1 truncate text-xl font-semibold tabular-nums" :class="card.cls">{{ card.value }}</p>
        </div>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else-if="items.length === 0" class="rounded-lg border border-dashed border-gray-300 bg-white px-6 py-12 text-center dark:border-dark-600 dark:bg-dark-800">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.margin.noData') }}</p>
      </div>

      <div v-else class="overflow-x-auto rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.margin.channel') }}</th>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.margin.model') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.margin.requests') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.margin.revenue') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.margin.estCost') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.margin.margin') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.margin.marginRate') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="item in items" :key="(item.channel_id ?? 'direct') + ':' + (item.group_id ?? 'ungrouped') + ':' + item.model" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
              <td class="px-4 py-3 text-gray-900 dark:text-white">{{ item.channel_name }}<span class="ml-1 text-xs text-gray-400">{{ item.group_name }}</span></td>
              <td class="px-4 py-3 font-mono text-gray-600 dark:text-gray-300">{{ item.model }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ item.requests }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ item.revenue != null ? '$' + money(item.revenue) : '-' }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ item.est_cost != null ? '$' + money(item.est_cost) : '-' }}</td>
              <td class="px-4 py-3 text-right font-semibold tabular-nums" :class="marginClass(item.margin)">{{ item.margin != null ? '$' + money(item.margin) : '-' }}</td>
              <td class="px-4 py-3 text-right font-semibold tabular-nums" :class="marginClass(item.margin)">{{ item.margin_rate != null ? pct(item.margin_rate) : '-' }}</td>
              <td class="px-4 py-3 text-right">
                <button v-if="item.channel_id != null" type="button" class="btn btn-sm btn-secondary" @click="doUnfuse(item)">{{ t('admin.margin.unfuse') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="events.length > 0" class="rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <h2 class="border-b border-gray-100 px-4 py-3 text-sm font-semibold text-gray-900 dark:border-dark-700 dark:text-white">{{ t('admin.margin.events') }}</h2>
        <ul class="divide-y divide-gray-100 px-4 py-2 text-sm dark:divide-dark-700">
          <li v-for="(ev, idx) in events" :key="idx" class="flex items-center justify-between gap-3 py-2">
            <span class="text-gray-700 dark:text-gray-300">
              <span class="font-medium" :class="ev.action === 'fused' ? 'text-red-600 dark:text-red-400' : 'text-green-600 dark:text-green-400'">{{ eventActionLabel(ev.action) }}</span>
              {{ ev.name }} <span class="text-xs text-gray-400">#{{ ev.channel_id }}</span>
            </span>
            <span class="text-xs text-gray-400">{{ eventReasonLabel(ev.reason) }} · {{ formatTime(ev.at) }}</span>
          </li>
        </ul>
      </div>
    </div>

    <BaseDialog :show="showSettings" :title="t('admin.margin.fuseSettings')" width="normal" @close="showSettings = false">
      <div class="space-y-4">
        <div class="flex items-center gap-2">
          <Toggle v-model="form.enabled" />
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.margin.enableFuse') }}</span>
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('admin.margin.windowHours') }}</label>
            <input v-model.number="form.window_hours" type="number" min="1" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.margin.minSpend') }}</label>
            <input v-model.number="form.min_spend" type="number" min="0" step="any" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.margin.minMarginRate') }}</label>
            <input v-model.number="form.min_margin_rate" type="number" step="0.05" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.margin.cooldownHours') }}</label>
            <input v-model.number="form.cooldown_hours" type="number" min="1" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.margin.intervalMinutes') }}</label>
            <input v-model.number="form.interval_minutes" type="number" min="1" class="input" />
          </div>
        </div>
        <p class="input-hint">{{ t('admin.margin.fuseHint') }}</p>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="showSettings = false">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="saving" @click="saveSettings">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import { adminAPI } from '@/api/admin'
import type { MarginFuseSettings, MarginRow, SelectOption } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime as formatDateTimeValue } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

const items = ref<MarginRow[]>([])
const events = ref<{ action: string; name: string; channel_id: number; reason: string; at: string }[]>([])
const loading = ref(false)
const saving = ref(false)
const showSettings = ref(false)
const hours = ref<number>(24)
const form = reactive<MarginFuseSettings>({
  enabled: true,
  window_hours: 24,
  min_spend: 5,
  min_margin_rate: 0,
  cooldown_hours: 6,
  interval_minutes: 5
})

const hoursOptions = computed<SelectOption[]>(() => [
  { label: t('admin.margin.last24Hours'), value: 24 },
  { label: t('admin.margin.last7Days'), value: 168 },
  { label: t('admin.margin.last30Days'), value: 720 }
])
const eventActionLabel = (action: string): string => {
  if (action === 'fused') return t('admin.margin.actionFused')
  if (action === 'recovered') return t('admin.margin.actionRecovered')
  return action
}
const eventReasonLabel = (reason: string): string => {
  if (reason === 'margin_rate below threshold') return t('admin.margin.reasonLowMargin')
  if (reason === 'cooldown elapsed, probing') return t('admin.margin.reasonCooldown')
  if (reason === 'manual') return t('admin.margin.reasonManual')
  return reason
}

const money = (v?: number | null): string => {
  if (v === undefined || v === null || !Number.isFinite(v)) return '-'
  return v.toFixed(4)
}
const pct = (v: number): string => `${(v * 100).toFixed(1)}%`
const formatTime = (v: string): string => {
  try {
    return formatDateTimeValue(v)
  } catch {
    return v
  }
}
const marginClass = (v?: number | null): string => {
  if (v === undefined || v === null) return 'text-gray-400'
  return v >= 0
    ? 'text-green-600 dark:text-green-400'
    : 'text-red-600 dark:text-red-400'
}

const totals = computed(() => {
  let revenue = 0
  let cost = 0
  let known = false
  for (const r of items.value) {
    revenue += r.revenue
    if (r.est_cost != null) {
      cost += r.est_cost
      known = true
    }
  }
  return { revenue, cost, known }
})

const overviewCards = computed(() => {
  const t3 = totals.value
  const margin = t3.revenue - t3.cost
  return [
    { key: 'revenue', label: t('admin.margin.revenue'), value: `$${money(t3.revenue)}`, cls: 'text-gray-900 dark:text-white' },
    { key: 'cost', label: t('admin.margin.estCost'), value: t3.known ? `$${money(t3.cost)}` : '-', cls: 'text-gray-900 dark:text-white' },
    { key: 'margin', label: t('admin.margin.margin'), value: t3.known ? `$${money(margin)}` : '-', cls: marginClass(t3.known ? margin : null) },
    {
      key: 'rate',
      label: t('admin.margin.marginRate'),
      value: t3.known && t3.revenue > 0 ? pct(margin / t3.revenue) : '-',
      cls: marginClass(t3.known && t3.revenue > 0 ? margin / t3.revenue : null)
    }
  ]
})

const loadAll = async () => {
  loading.value = true
  try {
    const [summary, settings, ev] = await Promise.all([
      adminAPI.margins.summary({ hours: hours.value }),
      adminAPI.margins.getSettings(),
      adminAPI.margins.events()
    ])
    items.value = summary.items || []
    Object.assign(form, settings)
    events.value = ev.items || []
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.margin.loadFailed')))
  } finally {
    loading.value = false
  }
}

const saveSettings = async () => {
  saving.value = true
  try {
    const updated = await adminAPI.margins.updateSettings({ ...form })
    Object.assign(form, updated)
    appStore.showSuccess(t('admin.margin.saveSuccess'))
    showSettings.value = false
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.margin.saveFailed')))
  } finally {
    saving.value = false
  }
}

const doUnfuse = async (item: MarginRow) => {
  if (item.channel_id == null) return
  try {
    await adminAPI.margins.unfuse(item.channel_id)
    appStore.showSuccess(t('admin.margin.unfuseSuccess'))
    await loadAll()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.margin.saveFailed')))
  }
}

onMounted(() => {
  loadAll()
})
</script>
