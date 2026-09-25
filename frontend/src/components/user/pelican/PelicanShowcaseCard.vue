<template>
  <article
    ref="cardRef"
    class="group relative overflow-hidden rounded-2xl border border-gray-200/80 bg-white/70 shadow-card backdrop-blur-xl transition-all duration-300 ease-out hover:-translate-y-1 hover:border-gray-300 hover:shadow-card-hover dark:border-dark-700/70 dark:bg-dark-800/60 dark:hover:border-primary-500/30"
    data-testid="pelican-showcase-card"
  >
    <div class="relative aspect-[4/3] overflow-hidden border-b border-gray-100 bg-gray-50 dark:border-dark-700/70 dark:bg-dark-900/40">
      <iframe
        v-if="body?.status === 'ready'"
        :srcdoc="body.html"
        class="pointer-events-none h-full w-full border-0"
        tabindex="-1"
        sandbox="allow-scripts"
        referrerpolicy="no-referrer"
        :title="label"
      />
      <div v-else class="absolute inset-0 flex items-center justify-center p-4 text-center text-xs text-gray-400 dark:text-gray-500">
        <span v-if="!body || body.status === 'loading'" class="animate-pulse">{{ t('pelicanShowcase.itemLoading') }}</span>
        <span v-else-if="body.status === 'invalid'">{{ t('pelicanShowcase.invalidHtml') }}</span>
        <span v-else class="text-red-500 dark:text-red-400">{{ t('pelicanShowcase.itemLoadError') }}</span>
      </div>
    </div>

    <div class="flex items-start justify-between gap-3 px-4 py-3">
      <div class="min-w-0">
        <p class="truncate font-mono text-sm font-medium text-gray-900 dark:text-gray-100" :title="item.model_id">
          {{ item.model_id || '—' }}
        </p>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ formatDateTimeToMinute(item.generated_at) }}</p>
      </div>
      <div class="flex shrink-0 flex-col items-end gap-1 text-xs">
        <span
          v-if="effortLabel"
          class="rounded-md bg-gray-100 px-1.5 py-0.5 font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300"
        >
          {{ effortLabel }}
        </span>
        <span class="tabular-nums text-gray-500 dark:text-gray-400">{{ durationLabel }}</span>
      </div>
    </div>

    <!-- The iframe is not interactive content of a button, so a full-card overlay opens the preview. -->
    <button
      type="button"
      class="absolute inset-0 z-10 rounded-2xl focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary-500"
      :aria-label="`${label} · ${t('pelicanShowcase.preview')}`"
      @click="emit('open')"
    />
  </article>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PelicanShowcaseItem } from '@/api/pelicanShowcase'
import { formatDateTimeToMinute } from '@/utils/format'
import { pelicanDurationLabel, pelicanEffortLabel, type PelicanBody } from './pelicanShowcaseFormat'

const props = defineProps<{
  item: PelicanShowcaseItem
  groupName: string
  body?: PelicanBody
}>()

const emit = defineEmits<{
  (e: 'visible'): void
  (e: 'open'): void
}>()

const { t } = useI18n()
const cardRef = ref<HTMLElement | null>(null)
let observer: IntersectionObserver | null = null

const label = computed(() => `${props.groupName} · ${props.item.model_id || '—'}`)
const effortLabel = computed(() => pelicanEffortLabel(t, props.item.reasoning_effort))
const durationLabel = computed(() => pelicanDurationLabel(t, props.item.latency_ms))

// HTML bodies load only once a card nears the viewport, so a long gallery costs nothing up front.
onMounted(() => {
  if (typeof IntersectionObserver === 'undefined' || !cardRef.value) {
    emit('visible')
    return
  }
  observer = new IntersectionObserver((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) {
      emit('visible')
      observer?.disconnect()
      observer = null
    }
  }, { rootMargin: '200px' })
  observer.observe(cardRef.value)
})

onBeforeUnmount(() => observer?.disconnect())
</script>
