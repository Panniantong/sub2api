<template>
  <AppLayout>
    <div class="max-w-2xl space-y-6">
      <div>
        <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.tieredRouting.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.tieredRouting.description') }}</p>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else class="space-y-4 rounded-lg border border-gray-100 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800">
        <div class="flex items-center gap-2">
          <Toggle v-model="form.enabled" />
          <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.tieredRouting.enable') }}</span>
        </div>
        <div>
          <label class="input-label">{{ t('admin.tieredRouting.premiumUserIds') }}</label>
          <textarea
            v-model="userIdsText"
            rows="3"
            class="input font-mono text-sm"
            :placeholder="t('admin.tieredRouting.premiumUserIdsPlaceholder')"
          ></textarea>
          <p class="input-hint">{{ t('admin.tieredRouting.premiumUserIdsHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.tieredRouting.minBalance') }}</label>
          <input v-model.number="form.min_balance_for_premium" type="number" min="0" step="any" class="input" />
          <p class="input-hint">{{ t('admin.tieredRouting.minBalanceHint') }}</p>
        </div>
        <p class="input-hint">{{ t('admin.tieredRouting.poolHint') }}</p>
        <div class="flex justify-end">
          <button type="button" class="btn btn-primary" :disabled="saving" @click="save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Toggle from '@/components/common/Toggle.vue'
import { adminAPI } from '@/api/admin'
import type { TieredRoutingSettings } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const saving = ref(false)
const userIdsText = ref('')
const form = reactive<TieredRoutingSettings>({
  enabled: false,
  premium_user_ids: [],
  min_balance_for_premium: 0
})

const load = async () => {
  loading.value = true
  try {
    const settings = await adminAPI.tieredRouting.getSettings()
    Object.assign(form, settings)
    userIdsText.value = (form.premium_user_ids || []).join(', ')
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.tieredRouting.loadFailed')))
  } finally {
    loading.value = false
  }
}

const save = async () => {
  const ids = userIdsText.value
    .split(/[\s,;，；、]+/)
    .map((s) => s.trim())
    .filter((s) => s !== '')
    .map((s) => Number(s))
    .filter((n) => Number.isInteger(n) && n > 0)
  const minBalance = Number(form.min_balance_for_premium)
  if (!Number.isFinite(minBalance) || minBalance < 0) {
    appStore.showError(t('admin.tieredRouting.invalidBalance'))
    return
  }
  saving.value = true
  try {
    const updated = await adminAPI.tieredRouting.updateSettings({
      enabled: form.enabled,
      premium_user_ids: ids,
      min_balance_for_premium: minBalance
    })
    Object.assign(form, updated)
    userIdsText.value = (form.premium_user_ids || []).join(', ')
    appStore.showSuccess(t('admin.tieredRouting.saveSuccess'))
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.tieredRouting.saveFailed')))
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  load()
})
</script>
