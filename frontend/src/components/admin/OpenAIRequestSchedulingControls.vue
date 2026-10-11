<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import OpenAIGroupSchedulingControls from './OpenAIGroupSchedulingControls.vue'

const { t } = useI18n()
defineProps<{ enabled: boolean }>()
const mode = defineModel<string>('mode', { required: true })
const selectedMode = computed({ get: () => mode.value || 'balanced', set: (value: string) => { mode.value = value } })
const queueSeconds = defineModel<string>('queueSeconds', { required: true })
const preOutputSeconds = defineModel<string>('preOutputSeconds', { required: true })
const maxSwitches = defineModel<string>('maxSwitches', { required: true })
const groupPolicies = defineModel<string>('groupPolicies', { default: '' })
defineEmits<{ preset: [] }>()
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-4">
      <p class="text-sm text-gray-500">{{ t('admin.settings.requestScheduling.applyHint') }}</p>
      <button type="button" class="btn btn-secondary shrink-0" data-testid="low-load-preset" @click="$emit('preset')">
        {{ t('admin.settings.requestScheduling.preset') }}
      </button>
    </div>
    <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <label class="block text-sm">
        {{ t('admin.settings.requestScheduling.mode') }}
        <select v-model="selectedMode" :disabled="!enabled" class="input mt-1" data-testid="request-scheduling-mode">
          <option value="balanced">{{ t('admin.settings.requestScheduling.balanced') }}</option>
          <option value="low_load">{{ t('admin.settings.requestScheduling.lowLoad') }}</option>
        </select>
        <span class="mt-1 block text-xs text-gray-500">{{ t('admin.settings.requestScheduling.modeHint') }}</span>
      </label>
      <label class="block text-sm">
        {{ t('admin.settings.requestScheduling.queue') }}
        <input v-model="queueSeconds" :disabled="!enabled" class="input mt-1" inputmode="numeric" placeholder="2" data-testid="request-queue-seconds" />
        <span class="mt-1 block text-xs text-gray-500">{{ t('admin.settings.requestScheduling.queueHint') }}</span>
      </label>
      <label class="block text-sm">
        {{ t('admin.settings.requestScheduling.preOutput') }}
        <input v-model="preOutputSeconds" class="input mt-1" inputmode="numeric" placeholder="25" data-testid="request-pre-output-seconds" />
        <span class="mt-1 block text-xs text-gray-500">{{ t('admin.settings.requestScheduling.preOutputHint') }}</span>
      </label>
      <label class="block text-sm">
        {{ t('admin.settings.requestScheduling.switches') }}
        <input v-model="maxSwitches" class="input mt-1" inputmode="numeric" placeholder="5" data-testid="request-max-switches" />
        <span class="mt-1 block text-xs text-gray-500">{{ t('admin.settings.requestScheduling.switchesHint') }}</span>
      </label>
    </div>
    <p class="rounded-lg bg-primary-50 p-3 text-sm text-gray-700 dark:bg-dark-700 dark:text-gray-300">{{ t('admin.settings.requestScheduling.cookieHint') }}</p>
    <OpenAIGroupSchedulingControls v-model="groupPolicies" />
  </div>
</template>
