<template>
  <AppLayout>
    <div class="space-y-6 pb-6">
      <!-- Toolbar: group filter + the gallery rules + refresh -->
      <section class="flex flex-col gap-3 pt-3 md:flex-row md:items-center md:justify-between md:pt-4">
        <div
          v-if="groups.length > 1"
          role="tablist"
          :aria-label="t('pelicanShowcase.title')"
          class="flex flex-wrap gap-1 self-start rounded-xl border border-gray-200/60 bg-gray-100 p-0.5 text-xs dark:border-dark-700/60 dark:bg-dark-800"
        >
          <button
            v-for="tab in tabs"
            :key="tab.key"
            type="button"
            role="tab"
            :aria-selected="activeGroup === tab.key"
            class="rounded-lg px-3 py-1 transition-colors"
            :class="activeGroup === tab.key
              ? 'bg-white font-semibold text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'"
            :data-testid="`showcase-tab-${tab.key}`"
            @click="activeGroup = tab.key"
          >
            {{ tab.label }}<span v-if="tab.count !== undefined" class="ml-1 tabular-nums opacity-60">{{ tab.count }}</span>
          </button>
        </div>
        <div v-else class="hidden md:block" />

        <div class="flex flex-wrap items-center gap-2 md:justify-end">
          <template v-if="view?.enabled && groups.length">
            <span class="inline-flex items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300" data-testid="showcase-keep-rule">
              {{ t('pelicanShowcase.keepRule', { count: view.max_items }) }}
            </span>
            <span
              v-if="view.retention_days > 0"
              class="inline-flex items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300"
              data-testid="showcase-retention-rule"
            >
              {{ t('pelicanShowcase.retentionRule', { days: view.retention_days }) }}
            </span>
          </template>
          <button
            type="button"
            class="flex h-8 w-8 items-center justify-center rounded-lg text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 disabled:opacity-50 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-gray-200"
            :disabled="loading"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
            @click="load"
          >
            <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
      </section>

      <!-- First load -->
      <div v-if="loading && !view" class="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
        <div
          v-for="i in 8"
          :key="i"
          class="overflow-hidden rounded-2xl border border-gray-200/80 bg-white/70 dark:border-dark-700/70 dark:bg-dark-800/60"
        >
          <div class="aspect-[4/3] animate-pulse bg-gray-100 dark:bg-dark-900/40" />
          <div class="space-y-2 p-4">
            <div class="h-4 w-2/3 animate-pulse rounded bg-gray-200 dark:bg-dark-700" />
            <div class="h-3 w-1/3 animate-pulse rounded bg-gray-100 dark:bg-dark-700/60" />
          </div>
        </div>
      </div>

      <EmptyState
        v-else-if="view && !view.enabled"
        :title="t('pelicanShowcase.disabled.title')"
        :description="t('pelicanShowcase.disabled.description')"
        data-testid="showcase-disabled"
      />
      <EmptyState
        v-else-if="view && !groups.length"
        :title="t('pelicanShowcase.empty.title')"
        :description="t('pelicanShowcase.empty.description')"
        data-testid="showcase-empty"
      />

      <!-- One section per showcased group -->
      <section
        v-for="group in shownGroups"
        :key="group.id"
        class="space-y-4"
        :data-testid="`showcase-group-${group.id}`"
      >
        <header class="flex min-w-0 items-center gap-3">
          <span
            class="grid h-9 w-9 flex-shrink-0 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10"
            :class="platformBadgeLightClass(group.platform)"
          >
            <PlatformIcon :platform="group.platform as GroupPlatform" size="sm" />
          </span>
          <div class="min-w-0">
            <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ group.name }}</h2>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ platformLabel(group.platform) }}
              · {{ t('pelicanShowcase.itemCount', { count: group.items.length }) }}
              <template v-if="group.items.length">
                · {{ t('pelicanShowcase.latestAt', { time: formatRelativeTime(group.items[0].generated_at) }) }}
              </template>
            </p>
          </div>
        </header>

        <div
          v-if="!group.items.length"
          class="rounded-2xl border border-dashed border-gray-300 px-4 py-10 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
        >
          {{ t('pelicanShowcase.groupEmpty') }}
        </div>
        <template v-else>
          <div class="grid grid-cols-1 gap-5 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
            <PelicanShowcaseCard
              v-for="item in visibleItems(group)"
              :key="item.id"
              :item="item"
              :group-name="group.name"
              :body="bodies[item.id]"
              @visible="requestBody(item.id)"
              @open="openPreview(group, item)"
            />
          </div>
          <div v-if="group.items.length > visibleCount(group)" class="flex justify-center">
            <button type="button" class="btn btn-secondary" :data-testid="`showcase-more-${group.id}`" @click="showMore(group)">
              {{ t('pelicanShowcase.loadMore') }}
            </button>
          </div>
        </template>
      </section>
    </div>

    <BaseDialog
      :show="preview !== null"
      :title="previewTitle"
      width="full"
      close-on-click-outside
      @close="closePreview"
    >
      <div v-if="preview" class="space-y-3" data-testid="showcase-preview">
        <!-- Chips instead of "·" separators, so a wrapped line never starts with a dot on phones. -->
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
          <span class="inline-flex items-center rounded-md px-1.5 py-0.5 font-medium" :class="platformBadgeLightClass(preview.group.platform)">
            {{ preview.group.name }}
          </span>
          <span>{{ formatDateTimeToMinute(preview.item.generated_at) }}</span>
          <span v-if="previewEffort" class="rounded-md bg-gray-100 px-1.5 py-0.5 text-gray-600 dark:bg-dark-700 dark:text-gray-300">
            {{ previewEffort }}
          </span>
          <span class="tabular-nums">{{ pelicanDurationLabel(t, preview.item.latency_ms) }}</span>
        </div>
        <div class="h-[65vh] min-h-[320px] overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700">
          <iframe
            v-if="previewBody?.status === 'ready'"
            :srcdoc="previewBody.html"
            class="h-full w-full border-0"
            sandbox="allow-scripts"
            referrerpolicy="no-referrer"
            :title="previewTitle"
          />
          <div v-else class="flex h-full items-center justify-center p-6 text-sm text-gray-500">
            <span v-if="!previewBody || previewBody.status === 'loading'" class="animate-pulse">{{ t('pelicanShowcase.itemLoading') }}</span>
            <span v-else-if="previewBody.status === 'invalid'">{{ t('pelicanShowcase.invalidHtml') }}</span>
            <span v-else class="text-red-500">{{ t('pelicanShowcase.itemLoadError') }}</span>
          </div>
        </div>
        <p class="text-xs text-gray-400 dark:text-gray-500">{{ t('pelicanShowcase.sandboxNote') }}</p>
      </div>
      <template #footer>
        <div class="flex w-full items-center justify-between gap-3">
          <button
            v-if="isAdmin"
            type="button"
            class="btn btn-danger"
            :disabled="removing"
            data-testid="showcase-remove"
            @click="confirmingRemove = true"
          >
            {{ t('pelicanShowcase.remove') }}
          </button>
          <span v-else />
          <button type="button" class="btn btn-secondary" @click="closePreview">{{ t('common.close') }}</button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="confirmingRemove"
      :title="t('pelicanShowcase.remove')"
      :message="t('pelicanShowcase.removeConfirm')"
      :confirm-text="t('pelicanShowcase.remove')"
      danger
      @confirm="removeItem"
      @cancel="confirmingRemove = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import PelicanShowcaseCard from '@/components/user/pelican/PelicanShowcaseCard.vue'
import {
  pelicanDurationLabel,
  pelicanEffortLabel,
  type PelicanBody,
} from '@/components/user/pelican/pelicanShowcaseFormat'
import {
  getShowcase,
  getShowcaseItem,
  removeShowcaseItem,
  type PelicanShowcaseGroup,
  type PelicanShowcaseItem,
  type PelicanShowcaseView,
} from '@/api/pelicanShowcase'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { GroupPlatform } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTimeToMinute, formatRelativeTime } from '@/utils/format'
import { extractPelicanHtml } from '@/utils/pelicanHtml'
import { platformBadgeLightClass, platformLabel } from '@/utils/platformColors'

const PAGE_SIZE = 8
const MAX_CONCURRENT_BODIES = 4

type TabKey = number | 'all'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isAdmin = computed(() => authStore.isAdmin)

const view = ref<PelicanShowcaseView | null>(null)
const loading = ref(false)
const activeGroup = ref<TabKey>('all')
const pageSizes = reactive<Record<number, number>>({})
const bodies = reactive<Record<number, PelicanBody>>({})
const preview = ref<{ group: PelicanShowcaseGroup; item: PelicanShowcaseItem } | null>(null)
const confirmingRemove = ref(false)
const removing = ref(false)

let alive = true
let loadController: AbortController | null = null
const bodyQueue: number[] = []
let bodiesInFlight = 0

const groups = computed(() => (view.value?.enabled ? view.value.groups : []))
const tabs = computed(() => [
  { key: 'all' as TabKey, label: t('pelicanShowcase.allGroups'), count: undefined as number | undefined },
  ...groups.value.map((group) => ({ key: group.id as TabKey, label: group.name, count: group.items.length })),
])
const shownGroups = computed(() =>
  activeGroup.value === 'all' ? groups.value : groups.value.filter((group) => group.id === activeGroup.value)
)
const previewBody = computed(() => (preview.value ? bodies[preview.value.item.id] : undefined))
const previewTitle = computed(() =>
  preview.value
    ? t('pelicanShowcase.previewTitle', { group: preview.value.group.name, model: preview.value.item.model_id || '—' })
    : ''
)
const previewEffort = computed(() => (preview.value ? pelicanEffortLabel(t, preview.value.item.reasoning_effort) : ''))

// A removed group must not leave the filter pointing at nothing.
watch(groups, (list) => {
  if (activeGroup.value !== 'all' && !list.some((group) => group.id === activeGroup.value)) activeGroup.value = 'all'
})

function visibleCount(group: PelicanShowcaseGroup) {
  return pageSizes[group.id] ?? PAGE_SIZE
}

function visibleItems(group: PelicanShowcaseGroup) {
  return group.items.slice(0, visibleCount(group))
}

function showMore(group: PelicanShowcaseGroup) {
  pageSizes[group.id] = visibleCount(group) + PAGE_SIZE
}

function requestBody(id: number) {
  if (bodies[id]) return
  bodies[id] = { status: 'loading', html: '' }
  bodyQueue.push(id)
  pumpBodies()
}

function pumpBodies() {
  while (alive && bodiesInFlight < MAX_CONCURRENT_BODIES && bodyQueue.length) {
    const id = bodyQueue.shift()!
    bodiesInFlight++
    getShowcaseItem(id)
      .then((item) => {
        const html = extractPelicanHtml(item.response_text || '')
        if (alive) bodies[id] = html ? { status: 'ready', html } : { status: 'invalid', html: '' }
      })
      .catch(() => {
        if (alive) bodies[id] = { status: 'error', html: '' }
      })
      .finally(() => {
        bodiesInFlight--
        pumpBodies()
      })
  }
}

async function load() {
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  loading.value = true
  try {
    const next = await getShowcase({ signal: controller.signal })
    if (!alive || controller.signal.aborted) return
    // Keep loaded HTML of items still shown. Failed ones are fetched again right away:
    // their cards were on screen already and will not report visibility a second time.
    const kept = new Set(next.groups.flatMap((group) => group.items.map((item) => item.id)))
    const retry: number[] = []
    for (const key of Object.keys(bodies)) {
      const id = Number(key)
      if (kept.has(id) && bodies[id].status === 'error') retry.push(id)
      if (!kept.has(id) || bodies[id].status === 'error') delete bodies[id]
    }
    view.value = next
    retry.forEach(requestBody)
  } catch (err: unknown) {
    const e = err as { name?: string; code?: string }
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(err, t('pelicanShowcase.loadError')))
  } finally {
    if (loadController === controller) {
      loading.value = false
      loadController = null
    }
  }
}

function openPreview(group: PelicanShowcaseGroup, item: PelicanShowcaseItem) {
  preview.value = { group, item }
  requestBody(item.id)
}

function closePreview() {
  preview.value = null
  confirmingRemove.value = false
}

async function removeItem() {
  const target = preview.value
  confirmingRemove.value = false
  if (!target || removing.value) return
  removing.value = true
  try {
    await removeShowcaseItem(target.item.id)
    target.group.items = target.group.items.filter((item) => item.id !== target.item.id)
    delete bodies[target.item.id]
    closePreview()
    appStore.showSuccess(t('pelicanShowcase.removed'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('pelicanShowcase.removeFailed')))
  } finally {
    removing.value = false
  }
}

onMounted(load)
onBeforeUnmount(() => {
  alive = false
  loadController?.abort()
})
</script>
