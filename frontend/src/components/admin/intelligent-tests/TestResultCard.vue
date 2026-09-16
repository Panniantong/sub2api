<template>
  <article class="flex min-w-0 flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
    <div class="flex items-start justify-between gap-3 px-4 pt-4">
      <div class="min-w-0">
        <p class="truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ account.account_type }} #{{ account.account_id }}</p>
        <p class="mt-1 truncate text-xs text-gray-500" :title="account.notes || account.name">{{ account.notes || account.name }}</p>
      </div>
      <button class="text-xs text-primary-600 hover:underline dark:text-primary-300" @click="emit('history', account.account_id, summary.test_type)">历史 {{ summary.history_count }}</button>
    </div>
    <p class="px-4 pt-3 text-xs font-medium text-gray-600 dark:text-gray-300">{{ testName(summary.test_type) }}</p>
    <button class="m-4 mb-3 flex aspect-[4/3] items-center justify-center overflow-hidden rounded-xl bg-gray-50 text-left dark:bg-dark-900"
      :disabled="!resultRecord" :aria-label="`查看账号 ${account.account_id} 的${testName(summary.test_type)}结果`" @click="resultRecord && emit('detail', resultRecord.id)">
      <TestGeneratedImage v-if="resultRecord && summary.test_type === 'pelican' && !isPending(resultRecord.status)" :source="resultRecord.result_image" :record-id="resultRecord.id" />
      <div v-else-if="resultRecord?.result" class="max-h-full overflow-hidden p-5">
        <p class="mb-3 text-xs text-gray-400">结果预览</p>
        <p class="line-clamp-6 whitespace-pre-wrap break-words text-sm leading-relaxed text-gray-700 dark:text-gray-200">{{ resultRecord.result }}</p>
      </div>
      <div v-else class="p-6 text-center text-sm text-gray-400">
        <span class="mb-3 block text-2xl" aria-hidden="true">{{ record?.status === 'running' ? '◌' : '▷' }}</span>
        {{ record?.status === 'running' ? '正在等待模型输出' : record?.status === 'queued' ? '当前排队' : '等待第一份检测结果' }}
      </div>
    </button>
    <div class="flex items-center justify-between gap-2 px-4">
      <TestStatusBadge :status="record?.status ?? 'waiting'" />
      <span class="text-xs tabular-nums text-gray-400">{{ duration }}</span>
    </div>
    <p v-if="resultRecord && resultRecord.id !== record?.id" class="px-4 pt-2 text-xs text-gray-500">展示最近完成的结果 · #{{ resultRecord.id }}</p>
    <TestAssessment v-if="resultRecord" class="px-4 pt-3" :assessment="resultRecord.evaluation" :status="resultRecord.status" />
    <p v-if="record?.queue_reason" class="px-4 pt-2 text-xs text-gray-500">{{ record.queue_reason }}</p>
    <p v-if="record?.status === 'queued' && record.queue_reason && record.available_at" class="px-4 pt-1 text-xs text-gray-500">下次检查：{{ testTime(record.available_at) }}</p>
    <p v-if="summary.risk" class="mx-4 mt-3 rounded-lg bg-amber-50 p-2 text-xs leading-relaxed text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">⚠ {{ summary.risk }}</p>
    <div class="mt-auto space-y-2 px-4 pb-4 pt-4 text-xs text-gray-500">
      <div class="flex items-center justify-between gap-2">
        <span>账号保护：{{ account.anti_degradation ? '开启' : '关闭' }}</span>
        <button v-if="record?.status === 'queued'" class="text-primary-600" @click="emit('cancel', record.id)">取消排队</button>
        <button class="text-primary-600 disabled:opacity-40 dark:text-primary-300" :disabled="disabled || isPending(record?.status)" @click="emit('run', account.account_id, summary.test_type)">{{ record ? '重新测试' : '开始测试' }}</button>
      </div>
      <p>最近测试：{{ testTime(record?.created_at) }}</p>
    </div>
  </article>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { TestAccount, TestSummary } from '@/api/intelligentTests'
import TestStatusBadge from './TestStatusBadge.vue'
import TestGeneratedImage from './TestGeneratedImage.vue'
import TestAssessment from './TestAssessment.vue'
import { testTime, testName, isPending } from './display'
const props = defineProps<{ account: TestAccount; summary: TestSummary; now: number; disabled?: boolean }>()
const emit = defineEmits<{ detail: [id: number]; history: [id: number, type: string]; run: [id: number, type: string]; cancel: [id: number] }>()
const record = computed(() => props.summary.latest)
const resultRecord = computed(() => props.summary.latest_completed || record.value)
const duration = computed(() => {
  const item = record.value
  if (item?.status === 'running' && item.started_at) return `已运行 ${Math.max(0, Math.floor((props.now - Date.parse(item.started_at)) / 1000))} 秒`
  return item?.duration_ms ? `${(item.duration_ms / 1000).toFixed(1)} 秒` : '—'
})
</script>
