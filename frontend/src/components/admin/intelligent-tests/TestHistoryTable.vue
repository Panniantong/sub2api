<template>
  <div class="overflow-x-auto rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
    <table class="w-full whitespace-nowrap text-left text-sm">
      <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-900"><tr><th class="p-4">测试时间</th><th class="p-4">账号</th><th class="p-4">测试类型</th><th class="p-4">结果</th><th class="p-4">耗时</th><th class="p-4">模型</th><th class="p-4">操作</th></tr></thead>
      <tbody class="divide-y divide-gray-100 dark:divide-dark-700"><tr v-for="record in records" :key="record.id" class="hover:bg-gray-50 dark:hover:bg-dark-700/40">
        <td class="p-4 text-gray-500">{{ testTime(record.created_at) }}</td><td class="p-4 font-medium">#{{ record.account_id }}</td><td class="p-4">{{ testName(record.test_type) }}</td>
        <td class="space-y-2 p-4"><TestStatusBadge :status="record.status" /><TestAssessment :assessment="record.evaluation" :status="record.status" compact /></td><td class="p-4 tabular-nums">{{ isPending(record.status) ? '尚未完成' : `${(record.duration_ms / 1000).toFixed(1)} 秒` }}</td><td class="max-w-48 truncate p-4">{{ record.model || '默认' }}</td>
        <td class="p-4"><button class="text-primary-600 hover:underline" @click="emit('detail', record.id)">查看详情</button></td>
      </tr></tbody>
    </table>
    <p v-if="!records.length" class="py-16 text-center text-sm text-gray-500">没有符合条件的测试记录</p>
  </div>
</template>
<script setup lang="ts">
import type { TestRecord } from '@/api/intelligentTests'
import TestStatusBadge from './TestStatusBadge.vue'
import TestAssessment from './TestAssessment.vue'
import { testName, testTime, isPending } from './display'
defineProps<{ records: TestRecord[] }>()
const emit = defineEmits<{ detail: [id: number] }>()
</script>
