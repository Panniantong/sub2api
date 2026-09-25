<template>
  <div v-if="groups.length > 0" class="space-y-3" data-testid="account-group-model-limits">
    <div>
      <label class="input-label">{{ t('admin.accounts.groupModelLimits.title') }}</label>
      <p class="input-hint">{{ t('admin.accounts.groupModelLimits.hint') }}</p>
    </div>
    <div
      v-for="group in groups"
      :key="group.id"
      class="rounded-lg border border-gray-200 p-3 dark:border-dark-600"
      :data-testid="`group-model-limit-${group.id}`"
    >
      <div class="flex flex-wrap items-center justify-between gap-2">
        <span class="text-sm font-medium text-gray-900 dark:text-white">{{ group.name }}</span>
        <div class="flex gap-2">
          <button
            type="button"
            data-testid="group-model-limit-all"
            :class="modeButtonClass(!isLimited(group.id))"
            @click="setLimited(group.id, false)"
          >
            {{ t('admin.accounts.groupModelLimits.allModels') }}
          </button>
          <button
            type="button"
            data-testid="group-model-limit-selected"
            :class="modeButtonClass(isLimited(group.id))"
            @click="setLimited(group.id, true)"
          >
            {{ t('admin.accounts.groupModelLimits.selectedModels') }}
          </button>
        </div>
      </div>
      <div v-if="isLimited(group.id)" class="mt-3">
        <ModelWhitelistSelector
          :model-value="modelValue[group.id] ?? []"
          :platform="platform || 'anthropic'"
          :account-id="accountId"
          @update:model-value="setModels(group.id, $event)"
        />
        <p
          v-if="(modelValue[group.id] ?? []).length === 0"
          class="text-xs text-amber-600 dark:text-amber-400"
          data-testid="group-model-limit-empty"
        >
          {{ t('admin.accounts.groupModelLimits.emptyHint') }}
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import type { GroupAllowedModels } from '@/components/account/groupAllowedModels'

const props = defineProps<{
  modelValue: GroupAllowedModels
  groups: { id: number; name: string }[]
  platform?: string
  accountId?: number
}>()

const emit = defineEmits<{
  'update:modelValue': [value: GroupAllowedModels]
}>()

const { t } = useI18n()

// 分组在 modelValue 里有条目就处于「仅限部分」；还没挑模型的空清单保存时会被丢弃，等同于不限制。
const isLimited = (groupId: number) => groupId in props.modelValue

const setLimited = (groupId: number, limited: boolean) => {
  if (limited === isLimited(groupId)) return
  if (limited) {
    emit('update:modelValue', { ...props.modelValue, [groupId]: [] })
    return
  }
  const rest = { ...props.modelValue }
  delete rest[groupId]
  emit('update:modelValue', rest)
}

const setModels = (groupId: number, models: string[]) => {
  emit('update:modelValue', { ...props.modelValue, [groupId]: models })
}

const modeButtonClass = (active: boolean) => [
  'rounded-lg px-3 py-1.5 text-xs font-medium transition-all',
  active
    ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-400'
    : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-400 dark:hover:bg-dark-500'
]
</script>
