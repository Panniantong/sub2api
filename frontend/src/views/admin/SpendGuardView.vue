<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.spendGuard.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.spendGuard.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="loadAll">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" @click="showSettings = true">
            <Icon name="cog" size="sm" />
            {{ t('admin.spendGuard.settings') }}
          </button>
        </div>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else-if="items.length === 0" class="rounded-lg border border-dashed border-gray-300 bg-white px-6 py-12 text-center dark:border-dark-600 dark:bg-dark-800">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.spendGuard.noData') }}</p>
      </div>

      <div v-else class="overflow-x-auto rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.spendGuard.key') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.spendGuard.requests') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.spendGuard.tokensPerMin') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.spendGuard.errRate') }}</th>
              <th class="px-4 py-3 text-center font-medium text-gray-500 dark:text-gray-300">{{ t('admin.spendGuard.status') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="item in items" :key="item.api_key_id" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
              <td class="px-4 py-3 text-gray-900 dark:text-white">
                <span class="font-medium">{{ item.name || ('#' + item.api_key_id) }}</span>
                <span class="ml-2 text-xs text-gray-400">#{{ item.api_key_id }}</span>
              </td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ item.requests }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ compactNum(item.tokens_per_min) }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ pct(item.err_rate) }} <span class="text-xs text-gray-400">({{ item.errors }})</span></td>
              <td class="px-4 py-3 text-center">
                <span class="inline-flex rounded-full px-2 py-0.5 text-xs font-medium" :class="statusClass(item)">
                  {{ item.frozen ? t('admin.spendGuard.frozen') : statusLabel(item.status) }}
                </span>
              </td>
              <td class="px-4 py-3 text-right">
                <button v-if="item.frozen" type="button" class="btn btn-sm btn-primary" @click="doUnfreeze(item)">{{ t('admin.spendGuard.unfreeze') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="events.length > 0" class="rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <h2 class="border-b border-gray-100 px-4 py-3 text-sm font-semibold text-gray-900 dark:border-dark-700 dark:text-white">{{ t('admin.spendGuard.events') }}</h2>
        <ul class="divide-y divide-gray-100 px-4 py-2 text-sm dark:divide-dark-700">
          <li v-for="(ev, idx) in events" :key="idx" class="flex items-center justify-between gap-3 py-2">
            <span class="text-gray-700 dark:text-gray-300">
              <span class="font-medium" :class="ev.action === 'frozen' ? 'text-red-600 dark:text-red-400' : 'text-green-600 dark:text-green-400'">{{ eventActionLabel(ev.action) }}</span>
              {{ ev.name }} <span class="text-xs text-gray-400">#{{ ev.api_key_id }}</span>
            </span>
            <span class="text-xs text-gray-400">{{ eventReasonLabel(ev.reason) }} · {{ formatTime(ev.at) }}</span>
          </li>
        </ul>
      </div>
    </div>

    <BaseDialog :show="showSettings" :title="t('admin.spendGuard.settings')" width="normal" @close="showSettings = false">
      <div class="space-y-4">
        <div class="flex items-center gap-2">
          <Toggle v-model="form.enabled" />
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.spendGuard.enableAuto') }}</span>
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('admin.spendGuard.windowMinutes') }}</label>
            <input v-model.number="form.window_minutes" type="number" min="1" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.spendGuard.tokensPerMinute') }}</label>
            <input v-model.number="form.tokens_per_minute" type="number" min="0" step="any" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.spendGuard.minRequests') }}</label>
            <input v-model.number="form.min_requests" type="number" min="1" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.spendGuard.maxErrorRate') }}</label>
            <input v-model.number="form.max_error_rate" type="number" min="0" max="1" step="0.05" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.spendGuard.intervalSeconds') }}</label>
            <input v-model.number="form.interval_seconds" type="number" min="10" step="10" class="input" />
          </div>
        </div>
        <p class="input-hint">{{ t('admin.spendGuard.settingsHint') }}</p>
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
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import { adminAPI } from '@/api/admin'
import type { SpendGuardOffender, SpendGuardSettings } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime as formatDateTimeValue } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

const items = ref<SpendGuardOffender[]>([])
const events = ref<{ action: string; name: string; api_key_id: number; reason: string; at: string }[]>([])
const loading = ref(false)
const saving = ref(false)
const showSettings = ref(false)
const form = reactive<SpendGuardSettings>({
  enabled: false,
  window_minutes: 5,
  tokens_per_minute: 2000000,
  min_requests: 5,
  max_error_rate: 0.8,
  interval_seconds: 60
})

const pct = (v: number): string => `${(v * 100).toFixed(1)}%`
const eventActionLabel = (action: string): string => {
  if (action === 'frozen') return t('admin.spendGuard.frozen')
  if (action === 'unfrozen') return t('admin.spendGuard.unfreezeSuccess')
  return action
}
const eventReasonLabel = (reason: string): string => {
  if (reason === 'token velocity') return t('admin.spendGuard.reasonTokenRate')
  if (reason === 'error rate') return t('admin.spendGuard.reasonErrorRate')
  if (reason === 'manual') return t('admin.spendGuard.reasonManual')
  return reason
}
const compactNum = (v: number): string => {
  if (v >= 1000000) return `${(v / 1000000).toFixed(2)}M`
  if (v >= 1000) return `${(v / 1000).toFixed(1)}K`
  return `${Math.round(v)}`
}
const formatTime = (v: string): string => {
  try {
    return formatDateTimeValue(v)
  } catch {
    return v
  }
}
const statusLabel = (s: string): string => {
  if (s === 'active') return t('admin.spendGuard.statusActive')
  if (s === 'disabled') return t('admin.spendGuard.statusDisabled')
  if (s === 'quota_exhausted') return t('admin.spendGuard.statusQuotaExhausted')
  if (s === 'expired') return t('admin.spendGuard.statusExpired')
  return s
}
const statusClass = (item: SpendGuardOffender): string => {
  if (item.frozen || item.status === 'disabled') return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
}

const loadAll = async () => {
  loading.value = true
  try {
    const [offenders, settings, ev] = await Promise.all([
      adminAPI.spendGuard.offenders(),
      adminAPI.spendGuard.getSettings(),
      adminAPI.spendGuard.events()
    ])
    items.value = offenders.items || []
    Object.assign(form, settings)
    events.value = ev.items || []
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.spendGuard.loadFailed')))
  } finally {
    loading.value = false
  }
}

const saveSettings = async () => {
  saving.value = true
  try {
    const updated = await adminAPI.spendGuard.updateSettings({ ...form })
    Object.assign(form, updated)
    appStore.showSuccess(t('admin.spendGuard.saveSuccess'))
    showSettings.value = false
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.spendGuard.saveFailed')))
  } finally {
    saving.value = false
  }
}

const doUnfreeze = async (item: SpendGuardOffender) => {
  try {
    await adminAPI.spendGuard.unfreeze(item.api_key_id)
    appStore.showSuccess(t('admin.spendGuard.unfreezeSuccess'))
    await loadAll()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.spendGuard.unfreezeFailed')))
  }
}

onMounted(() => {
  loadAll()
})
</script>
