<template>
  <AppLayout>
    <div class="mx-auto max-w-4xl space-y-6">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('billing.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('billing.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <input v-model="monthText" type="month" class="input w-44" @change="loadStatement" />
          <button type="button" class="btn btn-secondary" :disabled="exporting" @click="doExport">
            {{ exporting ? t('billing.exporting') : t('billing.exportCSV') }}
          </button>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div v-for="card in overviewCards" :key="card.key" class="rounded-lg border border-gray-100 bg-white px-4 py-3 shadow-sm dark:border-dark-700 dark:bg-dark-800">
          <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ card.label }}</p>
          <p class="mt-1 truncate text-xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ card.value }}</p>
        </div>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else-if="rows.length === 0" class="rounded-lg border border-dashed border-gray-300 bg-white px-6 py-12 text-center dark:border-dark-600 dark:bg-dark-800">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('billing.noData') }}</p>
      </div>

      <div v-else class="overflow-x-auto rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('billing.model') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('billing.requests') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('billing.tokens') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('billing.cost') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="r in rows" :key="r.model" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
              <td class="px-4 py-3 font-mono text-gray-900 dark:text-white">{{ r.model }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ r.requests }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ compactNum(r.total_tokens) }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ r.cost != null ? '$' + money(r.cost) : '-' }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="rounded-lg border border-gray-100 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <h2 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('billing.exportTitle') }}</h2>
        <div class="flex flex-col gap-3 sm:flex-row sm:items-end">
          <div>
            <label class="input-label">{{ t('billing.startDate') }}</label>
            <input v-model="exportStart" type="date" class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('billing.endDate') }}</label>
            <input v-model="exportEnd" type="date" class="input" />
          </div>
          <button type="button" class="btn btn-secondary" :disabled="exporting" @click="doExport">
            {{ exporting ? t('billing.exporting') : t('billing.exportCSV') }}
          </button>
        </div>
        <p class="input-hint mt-2">{{ t('billing.exportHint') }}</p>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import billingAPI from '@/api/billing'
import type { BillingStatementRow } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const now = new Date()
const monthText = ref(`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`)
const rows = ref<BillingStatementRow[]>([])
const totalRequests = ref(0)
const totalCost = ref(0)
const loading = ref(false)
const exporting = ref(false)
const exportStart = ref('')
const exportEnd = ref('')

const money = (v?: number | null): string => {
  if (v === undefined || v === null || !Number.isFinite(v)) return '-'
  return v.toFixed(4)
}
const startOfLocalDay = (d: string): Date => {
  const [y, m, day] = d.split('-').map((s) => Number(s))
  return new Date(y, (m || 1) - 1, day || 1, 0, 0, 0)
}
const endOfLocalDay = (d: string): Date => {
  const [y, m, day] = d.split('-').map((s) => Number(s))
  return new Date(y, (m || 1) - 1, day || 1, 23, 59, 59)
}
const compactNum = (v: number): string => {
  if (v >= 1000000) return `${(v / 1000000).toFixed(2)}M`
  if (v >= 1000) return `${(v / 1000).toFixed(1)}K`
  return `${v}`
}

const overviewCards = computed(() => [
  { key: 'requests', label: t('billing.requests'), value: `${totalRequests.value}` },
  { key: 'cost', label: t('billing.totalCost'), value: `$${money(totalCost.value)}` },
  { key: 'models', label: t('billing.models'), value: `${rows.value.length}` }
])

const loadStatement = async () => {
  const [y, m] = monthText.value.split('-').map((s) => Number(s))
  if (!y || !m) {
    rows.value = []
    totalRequests.value = 0
    totalCost.value = 0
    return
  }
  loading.value = true
  try {
    const stmt = await billingAPI.statement(y, m)
    rows.value = stmt.rows || []
    totalRequests.value = stmt.requests || 0
    totalCost.value = stmt.cost || 0
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('billing.loadFailed')))
  } finally {
    loading.value = false
  }
}

const doExport = async () => {
  // <input type="date"> 给的是本地日期：按本地时区构造，避免 UTC 错位 8 小时。
  const end = exportEnd.value ? endOfLocalDay(exportEnd.value) : new Date()
  const start = exportStart.value
    ? startOfLocalDay(exportStart.value)
    : new Date(end.getTime() - 7 * 24 * 3600 * 1000)
  if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) || start > end) {
    appStore.showError(t('billing.invalidRange'))
    return
  }
  if (end.getTime() - start.getTime() > 31 * 24 * 3600 * 1000) {
    appStore.showError(t('billing.rangeTooLong'))
    return
  }
  exporting.value = true
  try {
    const blob = await billingAPI.exportCSV(start.toISOString(), end.toISOString())
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `billing-${start.toISOString().slice(0, 10)}.csv`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('billing.exportFailed')))
  } finally {
    exporting.value = false
  }
}

onMounted(() => {
  loadStatement()
})
</script>
