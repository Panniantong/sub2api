<template>
  <div class="space-y-2" data-testid="test-assessment">
    <template v-if="assessment && isModernAssessment(assessment.evaluator_version)">
      <div class="flex flex-wrap gap-2 text-xs">
        <span class="rounded-md px-2 py-1" :class="answerTone" data-testid="answer-verdict">{{ answerLabels[assessment.answer_verdict ?? 'not_evaluated'] }}</span>
        <span class="rounded-md bg-gray-100 px-2 py-1 text-gray-600 dark:bg-dark-700 dark:text-gray-300" data-testid="format-verdict">{{ formatLabels[assessment.format_verdict ?? 'not_evaluated'] }}</span>
      </div>
      <p v-if="!compact && !publicView && assessment.reason" class="text-xs leading-relaxed text-gray-600 dark:text-gray-300">{{ assessment.reason }}</p>
      <p v-if="!compact && !publicView && assessment.format_reason" class="text-xs leading-relaxed text-gray-500">{{ assessment.format_reason }}</p>
      <p v-if="!compact" class="text-xs text-gray-500" data-testid="capability-verdict">单次测试不足以判断模型能力下降。</p>
      <p v-if="!compact && !publicView && assessment.evaluator_version !== currentEvaluatorVersion" class="text-xs text-gray-500">此记录采用较早的 B 规则，可在详情中按当前规则重新评估。</p>
    </template>
    <p v-else-if="status && !isPending(status) && status !== 'waiting' && status !== 'cancelled'" class="text-xs text-gray-500">{{ status === 'success' || status === 'suspected_degradation' ? '旧规则记录，可按 B 方案重新评估。' : '本次未完成答案评估。' }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { TestAssessment, TestStatus } from '@/api/intelligentTests'
import { answerLabels, formatLabels, isPending, isModernAssessment, currentEvaluatorVersion } from './display'
const props = defineProps<{ assessment?: TestAssessment; status?: TestStatus; compact?: boolean; publicView?: boolean }>()
const answerTone = computed(() => props.assessment?.answer_verdict === 'correct'
  ? 'bg-emerald-50 text-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-300'
  : props.assessment?.answer_verdict === 'incorrect' ? 'bg-rose-50 text-rose-800 dark:bg-rose-900/20 dark:text-rose-300'
    : 'bg-slate-100 text-slate-600 dark:bg-dark-700 dark:text-gray-300')
</script>
