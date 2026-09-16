<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.globalPricing.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.globalPricing.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="loadItems">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
          <button type="button" class="btn btn-primary inline-flex items-center gap-2" @click="openCreate">
            <Icon name="plus" size="sm" />
            {{ t('admin.globalPricing.addPricing') }}
          </button>
        </div>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else-if="items.length === 0" class="rounded-lg border border-dashed border-gray-300 bg-white px-6 py-12 text-center dark:border-dark-600 dark:bg-dark-800">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.globalPricing.noPricing') }}</p>
      </div>

      <div v-else class="overflow-x-auto rounded-lg border border-gray-100 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.modelPattern') }}</th>
              <th class="px-4 py-3 text-left font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.billingMode') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.inputPrice') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.outputPrice') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.perRequestPrice') }}</th>
              <th class="px-4 py-3 text-center font-medium text-gray-500 dark:text-gray-300">{{ t('admin.globalPricing.enabled') }}</th>
              <th class="px-4 py-3 text-right font-medium text-gray-500 dark:text-gray-300">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="item in items" :key="item.id" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
              <td class="px-4 py-3 font-mono text-gray-900 dark:text-white">{{ item.model_pattern }}</td>
              <td class="px-4 py-3 text-gray-600 dark:text-gray-300">{{ modeLabel(item.billing_mode || 'token') }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ fmtPerMillion(item.input_price) }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ fmtPerMillion(item.output_price) }}</td>
              <td class="px-4 py-3 text-right tabular-nums text-gray-900 dark:text-white">{{ fmtMoney(item.per_request_price) }}</td>
              <td class="px-4 py-3 text-center">
                <Toggle :model-value="item.enabled" @update:model-value="(v: boolean) => toggleEnabled(item, v)" />
              </td>
              <td class="px-4 py-3 text-right">
                <button type="button" class="btn btn-sm btn-secondary mr-2" @click="openEdit(item)">{{ t('common.edit') }}</button>
                <button type="button" class="btn btn-sm btn-danger" @click="askDelete(item)">{{ t('common.delete') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <BaseDialog :show="showForm" :title="editing ? t('admin.globalPricing.editPricing') : t('admin.globalPricing.addPricing')" width="normal" @close="closeForm">
      <div class="space-y-4">
        <div>
          <label class="input-label">{{ t('admin.globalPricing.modelPattern') }}</label>
          <input v-model="form.model_pattern" type="text" class="input font-mono" :placeholder="t('admin.globalPricing.modelPatternPlaceholder')" />
          <p class="input-hint">{{ t('admin.globalPricing.modelPatternHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.globalPricing.billingMode') }}</label>
          <Select v-model="form.billing_mode" :options="modeOptions" />
        </div>
        <div v-if="form.billing_mode !== 'per_request'" class="grid grid-cols-2 gap-4">
          <div>
            <label class="input-label">{{ t('admin.globalPricing.inputPrice') }}</label>
            <input v-model="priceText.input" type="number" min="0" step="any" class="input" :placeholder="t('admin.globalPricing.optional')" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.globalPricing.outputPrice') }}</label>
            <input v-model="priceText.output" type="number" min="0" step="any" class="input" :placeholder="t('admin.globalPricing.optional')" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.globalPricing.cacheReadPrice') }}</label>
            <input v-model="priceText.cacheRead" type="number" min="0" step="any" class="input" :placeholder="t('admin.globalPricing.optional')" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.globalPricing.cacheWritePrice') }}</label>
            <input v-model="priceText.cacheWrite" type="number" min="0" step="any" class="input" :placeholder="t('admin.globalPricing.optional')" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.globalPricing.cacheWrite1hPrice') }}</label>
            <input v-model="priceText.cacheWrite1h" type="number" min="0" step="any" class="input" :placeholder="t('admin.globalPricing.optional')" />
          </div>
        </div>
        <div v-if="form.billing_mode !== 'token'">
          <label class="input-label">{{ t('admin.globalPricing.perRequestPrice') }}</label>
          <input v-model="priceText.perRequest" type="number" min="0" step="any" class="input" :placeholder="t('admin.globalPricing.optional')" />
        </div>
        <div class="flex items-center gap-2">
          <Toggle v-model="form.enabled" />
          <span class="text-sm text-gray-700 dark:text-gray-300">{{ t('admin.globalPricing.enabled') }}</span>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeForm">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="saving" @click="saveForm">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog :show="showDelete" :title="t('admin.globalPricing.deletePricing')" width="narrow" @close="showDelete = false">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.globalPricing.confirmDelete') }}</p>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="showDelete = false">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-danger" :disabled="saving" @click="confirmDelete">{{ t('common.delete') }}</button>
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
import type { GlobalModelPrice, GlobalModelPriceInput, GlobalPricingBillingMode, SelectOption } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const items = ref<GlobalModelPrice[]>([])
const loading = ref(false)
const saving = ref(false)
const showForm = ref(false)
const showDelete = ref(false)
const editing = ref<GlobalModelPrice | null>(null)
const deleting = ref<GlobalModelPrice | null>(null)

const form = reactive<{ model_pattern: string; billing_mode: GlobalPricingBillingMode; enabled: boolean }>({
  model_pattern: '',
  billing_mode: 'token',
  enabled: true
})
// Numeric inputs convert v-model values to numbers after editing.
const priceText = reactive<Record<'input' | 'output' | 'cacheRead' | 'cacheWrite' | 'cacheWrite1h' | 'perRequest', string | number>>({
  input: '',
  output: '',
  cacheRead: '',
  cacheWrite: '',
  cacheWrite1h: '',
  perRequest: ''
})

const modeOptions = computed<SelectOption[]>(() => [
  { label: t('admin.globalPricing.modeToken'), value: 'token' },
  { label: t('admin.globalPricing.modePerRequest'), value: 'per_request' },
  { label: t('admin.globalPricing.modeImage'), value: 'image' },
  { label: t('admin.globalPricing.modeVideo'), value: 'video' }
])
const modeLabel = (mode: string): string => modeOptions.value.find(option => option.value === mode)?.label || mode

// UI uses $ / 1M tokens; backend stores $ / token.
const trimNum = (n: number): string => {
  if (!Number.isFinite(n)) return ''
  // 最多 12 位有效数字且永不归零：极小价格保留科学计数法（如 1e-9）。
  const s = n.toPrecision(12)
  const e = s.search(/[eE]/)
  if (e >= 0) {
    const mant = s.slice(0, e)
    const exp = s.slice(e)
    const trimmed = mant.includes('.') ? mant.replace(/0+$/, '').replace(/\.$/, '') : mant
    return `${trimmed}${exp}`
  }
  return s.includes('.') ? s.replace(/0+$/, '').replace(/\.$/, '') : s
}
// 解析价格输入：空=未设置；非法/负数返回 ok=false 由调用方报错（不静默吞掉）。
const parsePrice = (s: string | number): { value: number | null; ok: boolean } => {
  const v = String(s).trim()
  if (!v) return { value: null, ok: true }
  const n = Number(v)
  if (!Number.isFinite(n) || n < 0) return { value: null, ok: false }
  return { value: n, ok: true }
}
const toPerToken = (s: string | number): { value: number | null; ok: boolean } => {
  const r = parsePrice(s)
  if (!r.ok || r.value === null) return r
  return { value: r.value / 1000000, ok: true }
}
const fmtPerMillion = (v?: number | null): string => {
  if (v === undefined || v === null) return '-'
  return trimNum(v * 1000000)
}
const fmtMoney = (v?: number | null): string => {
  if (v === undefined || v === null) return '-'
  return trimNum(v)
}
const fromPerToken = (v?: number | null): string => {
  if (v === undefined || v === null) return ''
  return trimNum(v * 1000000)
}

const loadItems = async () => {
  loading.value = true
  try {
    const res = await adminAPI.globalPricing.list()
    items.value = res.items || []
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.globalPricing.loadFailed')))
  } finally {
    loading.value = false
  }
}

const resetForm = () => {
  form.model_pattern = ''
  form.billing_mode = 'token'
  form.enabled = true
  priceText.input = ''
  priceText.output = ''
  priceText.cacheRead = ''
  priceText.cacheWrite = ''
  priceText.cacheWrite1h = ''
  priceText.perRequest = ''
}

const openCreate = () => {
  editing.value = null
  resetForm()
  showForm.value = true
}

const openEdit = (item: GlobalModelPrice) => {
  editing.value = item
  form.model_pattern = item.model_pattern
  form.billing_mode = (item.billing_mode as GlobalPricingBillingMode) || 'token'
  form.enabled = item.enabled
  priceText.input = fromPerToken(item.input_price)
  priceText.output = fromPerToken(item.output_price)
  priceText.cacheRead = fromPerToken(item.cache_read_price)
  priceText.cacheWrite = fromPerToken(item.cache_write_price)
  priceText.cacheWrite1h = fromPerToken(item.cache_write_1h_price)
  priceText.perRequest = item.per_request_price !== undefined && item.per_request_price !== null ? trimNum(item.per_request_price) : ''
  showForm.value = true
}

const closeForm = () => {
  showForm.value = false
}

const saveForm = async () => {
  if (!form.model_pattern.trim()) {
    appStore.showError(t('admin.globalPricing.patternRequired'))
    return
  }
  const parsed = {
    input: toPerToken(priceText.input),
    output: toPerToken(priceText.output),
    cacheRead: toPerToken(priceText.cacheRead),
    cacheWrite: toPerToken(priceText.cacheWrite),
    cacheWrite1h: toPerToken(priceText.cacheWrite1h),
    perRequest: parsePrice(priceText.perRequest)
  }
  if (!parsed.input.ok || !parsed.output.ok || !parsed.cacheRead.ok ||
    !parsed.cacheWrite.ok || !parsed.cacheWrite1h.ok || !parsed.perRequest.ok) {
    appStore.showError(t('admin.globalPricing.invalidPrice'))
    return
  }
  saving.value = true
  try {
    // 与计费模式无关的价格不发送，避免隐藏字段的陈旧值被持久化。
    const isToken = form.billing_mode === 'token'
    const input: GlobalModelPriceInput = {
      model_pattern: form.model_pattern.trim(),
      billing_mode: form.billing_mode,
      input_price: isToken ? parsed.input.value : null,
      output_price: isToken ? parsed.output.value : null,
      cache_read_price: isToken ? parsed.cacheRead.value : null,
      cache_write_price: isToken ? parsed.cacheWrite.value : null,
      cache_write_1h_price: isToken ? parsed.cacheWrite1h.value : null,
      per_request_price: isToken ? null : parsed.perRequest.value,
      enabled: form.enabled
    }
    if (editing.value) {
      await adminAPI.globalPricing.update(editing.value.id, input)
      appStore.showSuccess(t('admin.globalPricing.updateSuccess'))
    } else {
      await adminAPI.globalPricing.create(input)
      appStore.showSuccess(t('admin.globalPricing.createSuccess'))
    }
    closeForm()
    await loadItems()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.globalPricing.saveFailed')))
  } finally {
    saving.value = false
  }
}

const pendingToggles = ref<Set<number>>(new Set())
const toggleEnabled = async (item: GlobalModelPrice, v: boolean) => {
  if (pendingToggles.value.has(item.id)) return
  pendingToggles.value.add(item.id)
  try {
    const updated = await adminAPI.globalPricing.setEnabled(item.id, v)
    const idx = items.value.findIndex((x) => x.id === item.id)
    if (idx >= 0) items.value[idx] = updated
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.globalPricing.saveFailed')))
  } finally {
    pendingToggles.value.delete(item.id)
  }
}

const askDelete = (item: GlobalModelPrice) => {
  deleting.value = item
  showDelete.value = true
}

const confirmDelete = async () => {
  if (!deleting.value) return
  saving.value = true
  try {
    await adminAPI.globalPricing.remove(deleting.value.id)
    appStore.showSuccess(t('admin.globalPricing.deleteSuccess'))
    showDelete.value = false
    deleting.value = null
    await loadItems()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.globalPricing.deleteFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  loadItems()
})
</script>
