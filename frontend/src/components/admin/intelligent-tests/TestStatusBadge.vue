<template>
  <span class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium" :class="tone">
    <span class="h-1.5 w-1.5 rounded-full bg-current" :class="status === 'running' ? 'animate-pulse' : ''" />
    {{ statusLabels[status] ?? status }}
  </span>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { TestStatus } from '@/api/intelligentTests'
import { statusLabels } from './display'
const props = defineProps<{ status: TestStatus }>()
const tone = computed(() => {
  if (props.status === 'success') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/25 dark:text-emerald-300'
  if (props.status === 'completed') return 'bg-blue-50 text-blue-700 dark:bg-blue-900/25 dark:text-blue-300'
  if (props.status === 'running') return 'bg-blue-50 text-blue-700 dark:bg-blue-900/25 dark:text-blue-300'
  if (['rate_limited', 'suspected_degradation'].includes(props.status)) return 'bg-amber-50 text-amber-800 dark:bg-amber-900/25 dark:text-amber-300'
  if (['failed', 'account_error', 'model_error', 'request_error', 'network_error'].includes(props.status)) return 'bg-red-50 text-red-700 dark:bg-red-900/25 dark:text-red-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
})
</script>
