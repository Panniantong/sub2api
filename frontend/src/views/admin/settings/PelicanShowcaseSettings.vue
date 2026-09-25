<template>
  <div class="card" data-testid="pelican-showcase-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.features.pelicanShowcase.title') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.features.pelicanShowcase.description') }}
      </p>
      <p class="mt-1.5 text-xs">
        <router-link
          to="/admin/accounts"
          class="inline-flex items-center gap-1 text-primary-600 hover:underline dark:text-primary-400"
        >
          {{ t('admin.settings.features.pelicanShowcase.configureLink') }}
          <span aria-hidden="true">→</span>
        </router-link>
      </p>
    </div>
    <div class="space-y-5 p-6">
      <div class="flex items-center justify-between gap-4">
        <div>
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.settings.features.pelicanShowcase.enabled') }}
          </label>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.features.pelicanShowcase.enabledHint') }}
          </p>
        </div>
        <Toggle v-model="enabledModel" data-testid="pelican-showcase-enabled" />
      </div>

      <div v-if="enabled" class="space-y-5 border-t border-gray-100 pt-5 dark:border-dark-700">
        <div>
          <GroupSelector
            v-model="groupIds"
            :groups="groups"
            :label="t('admin.settings.features.pelicanShowcase.groups')"
          />
          <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.settings.features.pelicanShowcase.groupsHint') }}
          </p>
          <p v-if="groupsLoadFailed" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
            {{ t('admin.settings.features.pelicanShowcase.groupsLoadFailed') }}
          </p>
          <p
            v-else-if="config.group_ids.length === 0"
            class="mt-1 text-xs text-amber-600 dark:text-amber-400"
            data-testid="pelican-showcase-no-groups"
          >
            {{ t('admin.settings.features.pelicanShowcase.noGroups') }}
          </p>
          <div
            v-if="staleGroupIds.length"
            class="mt-2 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-500/10 dark:text-amber-300"
            data-testid="pelican-showcase-stale-groups"
          >
            <p>{{ t('admin.settings.features.pelicanShowcase.staleGroups') }}</p>
            <div class="mt-1.5 flex flex-wrap gap-1.5">
              <span
                v-for="id in staleGroupIds"
                :key="id"
                class="inline-flex items-center gap-1.5 rounded-md bg-white px-2 py-0.5 dark:bg-dark-800"
              >
                {{ t('admin.settings.features.pelicanShowcase.staleGroupLabel', { id }) }}
                <button
                  type="button"
                  class="font-medium text-amber-600 hover:text-amber-800 dark:text-amber-300 dark:hover:text-amber-200"
                  @click="groupIds = config.group_ids.filter((groupId) => groupId !== id)"
                >
                  {{ t('admin.settings.features.pelicanShowcase.removeStaleGroup') }}
                </button>
              </span>
            </div>
          </div>
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
            <label class="input-label" for="pelican-showcase-max-items">
              {{ t('admin.settings.features.pelicanShowcase.maxItems') }}
            </label>
            <div class="relative">
              <input
                id="pelican-showcase-max-items"
                v-model.number="maxItems"
                type="number"
                min="1"
                :max="PELICAN_SHOWCASE_MAX_ITEMS"
                class="input pr-12"
                data-testid="pelican-showcase-max-items"
              />
              <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-sm text-gray-400">
                {{ t('admin.settings.features.pelicanShowcase.maxItemsUnit') }}
              </span>
            </div>
            <p class="mt-1.5 text-xs text-gray-400">
              {{ t('admin.settings.features.pelicanShowcase.maxItemsHint') }}
            </p>
          </div>

          <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <p class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.settings.features.pelicanShowcase.autoCleanup') }}
                </p>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.settings.features.pelicanShowcase.autoCleanupHint') }}
                </p>
              </div>
              <Toggle v-model="autoCleanup" data-testid="pelican-showcase-auto-cleanup" />
            </div>
            <div v-if="config.auto_cleanup" class="mt-3">
              <label class="input-label" for="pelican-showcase-retention-days">
                {{ t('admin.settings.features.pelicanShowcase.retentionDays') }}
              </label>
              <div class="relative">
                <input
                  id="pelican-showcase-retention-days"
                  v-model.number="retentionDays"
                  type="number"
                  min="1"
                  :max="PELICAN_SHOWCASE_MAX_RETENTION_DAYS"
                  class="input pr-12"
                  data-testid="pelican-showcase-retention-days"
                />
                <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-sm text-gray-400">
                  {{ t('admin.settings.features.pelicanShowcase.retentionDaysUnit') }}
                </span>
              </div>
              <p class="mt-1.5 text-xs text-gray-400">
                {{ t('admin.settings.features.pelicanShowcase.retentionDaysHint') }}
              </p>
            </div>
          </div>
        </div>

        <p class="rounded-lg bg-gray-50 px-3 py-2 text-xs leading-relaxed text-gray-500 dark:bg-dark-900/40 dark:text-gray-400">
          {{ t('admin.settings.features.pelicanShowcase.independentNote') }}
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupSelector from '@/components/common/GroupSelector.vue'
import Toggle from '@/components/common/Toggle.vue'
import type { PelicanShowcaseConfig } from '@/api/admin/settings'
import type { AdminGroup } from '@/types'
import { PELICAN_SHOWCASE_MAX_ITEMS, PELICAN_SHOWCASE_MAX_RETENTION_DAYS } from './pelicanShowcase'

const props = defineProps<{
  enabled: boolean
  config: PelicanShowcaseConfig
  /** Groups that can be showcased (active ones). */
  groups: AdminGroup[]
  groupsLoaded: boolean
  groupsLoadFailed: boolean
}>()

const emit = defineEmits<{
  (e: 'update:enabled', value: boolean): void
  (e: 'update:config', value: PelicanShowcaseConfig): void
}>()

const { t } = useI18n()

function field<K extends keyof PelicanShowcaseConfig>(key: K) {
  return computed<PelicanShowcaseConfig[K]>({
    get: () => props.config[key],
    set: (value) => emit('update:config', { ...props.config, [key]: value }),
  })
}

const enabledModel = computed({
  get: () => props.enabled,
  set: (value: boolean) => emit('update:enabled', value),
})
const groupIds = field('group_ids')
const maxItems = field('max_items')
const autoCleanup = field('auto_cleanup')
const retentionDays = field('retention_days')

// Selected groups that were deleted or disabled since; users no longer see them.
const staleGroupIds = computed(() => {
  if (!props.groupsLoaded) return []
  const known = new Set(props.groups.map((group) => group.id))
  return props.config.group_ids.filter((id) => !known.has(id))
})
</script>
