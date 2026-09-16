<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.accountHealth.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accountHealth.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="loadAll">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" @click="showSettings = true">
            <Icon name="cog" size="sm" />
            {{ t('admin.accountHealth.settings') }}
          </button>
        </div>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else-if="items.length === 0" class="rounded-lg border border-dashed border-gray-300 bg-white px-6 py-12 text-center dark:border-dark-600 dark:bg-dark-800">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accountHealth.noData') }}</p>
      </div>

      <div v-else class="overflow-x-auto rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.accountHealth.account') }}</th>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.accountHealth.platform') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.accountHealth.score') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.accountHealth.errRate') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.accountHealth.latency') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.accountHealth.samples') }}</th>
              <th class="px-4 py-3 text-center font-medium text-gray-500 dark:text-gray-300">{{ t('admin.accountHealth.state') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="item in items" :key="item.account_id" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
              <td class="px-4 py-3 text-gray-900 dark:text-white">
                <span class="font-medium">{{ item.name || ('#' + item.account_id) }}</span>
                <span class="ml-2 text-xs text-gray-400">#{{ item.account_id }}</span>
              </td>
              <td class="px-4 py-3 text-gray-600 dark:text-gray-300">{{ item.platform }}</td>
              <td class="px-4 py-3 text-right font-semibold tabular-nums" :class="scoreClass(item.score)">{{ item.score }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ pct(item.err_rate) }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ item.avg_latency_ms ?? '-' }}{{ item.avg_latency_ms != null ? ' ms' : '' }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ item.total }} <span class="text-xs text-gray-400">({{ t('admin.accountHealth.errorCount', { count: item.errors }) }})</span></td>
              <td class="px-4 py-3 text-center">
                <span class="inline-flex rounded-full px-2 py-0.5 text-xs font-medium" :class="stateClass(item.state)">
                  {{ stateLabel(item.state) }}
                </span>
                <div v-if="item.isolated && item.isolate_reason" class="mt-1 text-xs text-gray-400" :title="isolateReasonLabel(item.isolate_reason)">
                  {{ shortReason(isolateReasonLabel(item.isolate_reason)) }}
                </div>
              </td>
              <td class="px-4 py-3 text-right">
                <button v-if="!item.isolated" type="button" class="btn btn-sm btn-secondary mr-2" @click="doIsolate(item)">{{ t('admin.accountHealth.isolate') }}</button>
                <button v-else type="button" class="btn btn-sm btn-primary" @click="doResume(item)">{{ t('admin.accountHealth.resume') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <BaseDialog :show="showSettings" :title="t('admin.accountHealth.settings')" width="normal" @close="showSettings = false">
      <div class="space-y-4">
        <div class="flex items-center gap-2">
          <Toggle v-model="form.enabled" />
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.accountHealth.enableAuto') }}</span>
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('admin.accountHealth.windowMinutes') }}</label>
            <input v-model.number="form.window_minutes" type="number" min="1" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accountHealth.minSamples') }}</label>
            <input v-model.number="form.min_samples" type="number" min="1" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accountHealth.isolateErrRate') }}</label>
            <input v-model.number="form.isolate_err_rate" type="number" min="0" max="1" step="0.05" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accountHealth.recoverErrRate') }}</label>
            <input v-model.number="form.recover_err_rate" type="number" min="0" max="1" step="0.05" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accountHealth.cooldownMinutes') }}</label>
            <input v-model.number="form.cooldown_minutes" type="number" min="1" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accountHealth.intervalSeconds') }}</label>
            <input v-model.number="form.interval_seconds" type="number" min="10" step="10" class="input" />
          </div>
        </div>
        <p class="input-hint">{{ t('admin.accountHealth.settingsHint') }}</p>
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
import type { AccountHealthSettings, AccountHealthSnapshot } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const items = ref<AccountHealthSnapshot[]>([])
const loading = ref(false)
const saving = ref(false)
const showSettings = ref(false)
const form = reactive<AccountHealthSettings>({
  enabled: true,
  window_minutes: 10,
  min_samples: 10,
  isolate_err_rate: 0.5,
  recover_err_rate: 0.2,
  cooldown_minutes: 30,
  interval_seconds: 60
})

const pct = (v?: number | null): string => {
  if (v === undefined || v === null || !Number.isFinite(v)) return '-'
  return `${(v * 100).toFixed(1)}%`
}
const scoreClass = (s: number): string => {
  if (s >= 90) return 'text-green-600 dark:text-green-400'
  if (s >= 60) return 'text-amber-600 dark:text-amber-400'
  return 'text-red-600 dark:text-red-400'
}
const stateClass = (s: string): string => {
  if (s === 'isolated') return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (s === 'degraded') return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
}
const stateLabel = (s: string): string => {
  if (s === 'isolated') return t('admin.accountHealth.stateIsolated')
  if (s === 'degraded') return t('admin.accountHealth.stateDegraded')
  return t('admin.accountHealth.stateHealthy')
}
const shortReason = (r: string): string => (r.length > 32 ? r.slice(0, 32) + '…' : r)
const isolateReasonLabel = (reason: string): string => {
  if (reason === 'health:manual') return t('admin.accountHealth.reasonManual')
  const match = /^health:auto err_rate=(\d+(?:\.\d+)?%)$/.exec(reason)
  if (match) return t('admin.accountHealth.reasonErrorRate', { rate: match[1] })
  return reason
}

const loadAll = async () => {
  loading.value = true
  try {
    const [snap, settings] = await Promise.all([
      adminAPI.accountHealth.snapshot(),
      adminAPI.accountHealth.getSettings()
    ])
    items.value = snap.items || []
    Object.assign(form, settings)
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accountHealth.loadFailed')))
  } finally {
    loading.value = false
  }
}

const saveSettings = async () => {
  saving.value = true
  try {
    const updated = await adminAPI.accountHealth.updateSettings({ ...form })
    Object.assign(form, updated)
    appStore.showSuccess(t('admin.accountHealth.saveSuccess'))
    showSettings.value = false
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accountHealth.saveFailed')))
  } finally {
    saving.value = false
  }
}

const doIsolate = async (item: AccountHealthSnapshot) => {
  try {
    await adminAPI.accountHealth.isolate(item.account_id)
    appStore.showSuccess(t('admin.accountHealth.isolateSuccess'))
    await loadAll()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accountHealth.isolateFailed')))
  }
}

const doResume = async (item: AccountHealthSnapshot) => {
  try {
    await adminAPI.accountHealth.resume(item.account_id)
    appStore.showSuccess(t('admin.accountHealth.resumeSuccess'))
    await loadAll()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accountHealth.resumeFailed')))
  }
}

onMounted(() => {
  loadAll()
})
</script>
