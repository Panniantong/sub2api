<template>
  <BaseDialog :show="recordId !== null" :title="record ? `${testName(record.test_type)} · #${record.account_id}` : '测试详情'" width="wide" @close="emit('close')">
    <p v-if="loading" class="py-10 text-center text-sm text-gray-500">正在加载测试详情…</p>
    <div v-else-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }} <button class="ml-3 underline" @click="load">重试</button></div>
    <div v-else-if="record" class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3"><TestStatusBadge :status="record.status" /><span class="text-xs text-gray-500">{{ testTime(record.created_at) }}</span></div>
      <dl class="grid grid-cols-2 gap-4 rounded-xl bg-gray-50 p-4 text-sm dark:bg-dark-900 sm:grid-cols-4">
        <div><dt class="text-xs text-gray-500">模型</dt><dd class="mt-1 break-all">{{ record.model || '上游默认' }}</dd></div>
        <div><dt class="text-xs text-gray-500">耗时</dt><dd class="mt-1">{{ (record.duration_ms / 1000).toFixed(1) }} 秒</dd></div>
        <div><dt class="text-xs text-gray-500">最终答案得分</dt><dd class="mt-1">{{ isModernAssessment(record.evaluation?.evaluator_version) ? record.score ?? '未评分' : '旧规则，待复核' }}</dd></div>
        <div v-if="!publicView"><dt class="text-xs text-gray-500">检测时账号保护</dt><dd class="mt-1">{{ record.anti_degradation ? '开启' : '关闭' }}</dd></div>
      </dl>
      <section v-if="!publicView && execution" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-testid="execution-snapshot">
        <h4 class="mb-2 text-sm font-semibold">执行时账号配置</h4>
        <dl class="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <div><dt class="text-xs text-gray-500">策略</dt><dd>{{ execution.strategy }}</dd></div>
          <div><dt class="text-xs text-gray-500">身份模式</dt><dd>{{ execution.identity_mode }}</dd></div>
          <div><dt class="text-xs text-gray-500">有效传输</dt><dd>{{ execution.effective_tls }}</dd></div>
          <div><dt class="text-xs text-gray-500">并发上限</dt><dd>{{ execution.concurrency }}</dd></div>
          <div><dt class="text-xs text-gray-500">请求完整性</dt><dd>{{ execution.integrity_mode === 'enforce' ? '严格拦截' : execution.integrity_mode === 'observe' ? '仅观察' : execution.integrity_mode === 'off' ? '关闭' : '未记录' }}</dd></div>
        </dl>
        <p v-if="execution.tls_reason" class="mt-2 text-xs text-amber-700">{{ execution.tls_reason }}</p>
      </section>
      <TestAssessment :assessment="record.evaluation" :status="record.status" :public-view="publicView" />
      <dl v-if="!publicView && record.evaluation?.expected_answer" class="grid grid-cols-2 gap-3 text-sm">
        <div><dt class="text-xs text-gray-500">标准答案</dt><dd class="break-words">{{ record.evaluation.expected_answer }}</dd></div>
        <div><dt class="text-xs text-gray-500">抽取答案</dt><dd class="break-words">{{ record.evaluation.actual_answer || '无法唯一确定' }}</dd></div>
      </dl>
      <p v-if="!publicView && record.evaluation?.limitation" class="text-xs leading-relaxed text-gray-500">{{ record.evaluation.limitation }}</p>
      <p v-if="record.queue_reason" class="text-sm text-gray-500">{{ record.queue_reason }}</p>
      <p v-if="record.status === 'queued' && record.queue_reason && record.available_at" class="text-xs text-gray-500">下次检查：{{ testTime(record.available_at) }}</p>
      <div v-if="record.test_type === 'pelican' && !isPending(record.status)" class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
        <TestGeneratedImage :source="record.result_image" :record-id="record.id" :public-view="publicView" />
        <button class="mt-3 text-sm text-primary-600" @click="fullImage = !fullImage">{{ fullImage ? '收起原尺寸' : '查看原图' }}</button>
        <div v-if="fullImage" class="mt-3 max-h-[65vh] overflow-auto rounded-lg border p-2"><TestGeneratedImage :source="record.result_image" :record-id="record.id" :public-view="publicView" full-size /></div>
      </div>
      <section v-if="!publicView && record.input"><h4 class="mb-2 text-sm font-semibold">测试输入</h4><pre class="test-output">{{ record.input }}</pre></section>
      <details v-if="record.test_type === 'pelican'"><summary class="cursor-pointer text-sm text-gray-500">模型原始答复 / SVG 源码</summary><pre class="test-output mt-2">{{ record.result || '暂无输出' }}</pre></details>
      <section v-else><h4 class="mb-2 text-sm font-semibold">测试结果</h4><pre class="test-output">{{ record.result || '暂无输出' }}</pre></section>
      <section v-if="!publicView && record.error_message" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300"><h4 class="mb-2 font-semibold">异常信息</h4>{{ record.error_message }}</section>
      <details v-if="!publicView && record.raw_response"><summary class="cursor-pointer text-sm text-gray-500">原始响应{{ record.raw_truncated ? '（超过存储上限，已截断）' : '' }}</summary><pre class="test-output mt-2">{{ typeof record.raw_response === 'string' ? record.raw_response : JSON.stringify(record.raw_response, null, 2) }}</pre></details>
      <details v-if="!publicView && record.config_snapshot"><summary class="cursor-pointer text-sm text-gray-500">本次测试配置</summary><pre class="test-output mt-2">{{ JSON.stringify(record.config_snapshot, null, 2) }}</pre></details>
      <details v-if="!publicView && record.evaluation?.original_judgment"><summary class="cursor-pointer text-sm text-gray-500">重新评估前的原判定（已保留）</summary><pre class="test-output mt-2">{{ JSON.stringify(record.evaluation.original_judgment, null, 2) }}</pre></details>
      <p v-if="actionError" class="text-sm text-red-600" role="alert">{{ actionError }}</p>
    </div>
    <template #footer>
      <div class="flex w-full flex-wrap justify-end gap-2">
        <button v-if="record" class="btn btn-secondary" @click="copy">{{ copied ? '已复制' : '复制测试信息' }}</button>
        <button v-if="record && !publicView" class="btn btn-secondary" @click="emit('history', record.account_id, record.test_type)">查看历史测试</button>
        <button v-if="canReevaluate" class="btn btn-secondary" :disabled="actionBusy" @click="act('reevaluate')">按 B 方案重新评估（不调用模型）</button>
        <button v-if="record?.status === 'queued' && !publicView" class="btn btn-secondary" :disabled="actionBusy" @click="act('cancel')">取消排队</button>
        <button v-if="record && !publicView" class="btn btn-primary" :disabled="isPending(record.status) || running" @click="emit('run', record.account_id, record.test_type)">重新测试</button>
        <button class="btn btn-secondary" @click="emit('close')">关闭</button>
      </div>
    </template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { intelligentTestsAPI, type TestRecord } from '@/api/intelligentTests'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useClipboard } from '@/composables/useClipboard'
import TestStatusBadge from './TestStatusBadge.vue'
import TestGeneratedImage from './TestGeneratedImage.vue'
import TestAssessment from './TestAssessment.vue'
import { testName, testTime, isPending, isModernAssessment, currentEvaluatorVersion } from './display'
const props = defineProps<{ recordId: number | null; publicView?: boolean; running?: boolean }>()
const emit = defineEmits<{ close: []; history: [id: number, type: string]; run: [id: number, type: string]; updated: [] }>()
const record = ref<TestRecord | null>(null)
const execution = computed(() => {
  if (props.publicView) return undefined
  const config = record.value?.config_snapshot as { execution?: { strategy: string; identity_mode: string; effective_tls: string; concurrency: number; tls_reason?: string; integrity_mode?: string } } | undefined
  return config?.execution
})
const loading = ref(false), error = ref(''), fullImage = ref(false)
const actionBusy = ref(false), actionError = ref('')
const { copied, copyToClipboard } = useClipboard()
const canReevaluate = computed(() => {
  const item = record.value
  return !props.publicView && item && !item.raw_truncated && item.evaluation?.evaluator_version !== currentEvaluatorVersion && !!item.result &&
    (['success', 'suspected_degradation', 'completed'].includes(item.status) || (item.status === 'failed' && !!item.evaluation?.method && !item.error_message))
})
async function act(action: 'cancel' | 'reevaluate') {
  if (!record.value || actionBusy.value) return
  const id = record.value.id
  const version = ++requestVersion
  clearTimeout(timer)
  actionBusy.value = true; actionError.value = ''
  try {
    const result = await intelligentTestsAPI[action](id)
    if (props.recordId === id && version === requestVersion) record.value = result
    emit('updated')
  } catch (err) { if (props.recordId === id && version === requestVersion) actionError.value = extractApiErrorMessage(err, '操作失败') }
  finally {
    actionBusy.value = false
    if (version === requestVersion && isPending(record.value?.status)) timer = setTimeout(load, 5000)
  }
}
let requestVersion = 0, timer: ReturnType<typeof setTimeout> | undefined
async function load() {
  clearTimeout(timer)
  const id = props.recordId
  if (id === null) return
  const version = ++requestVersion
  loading.value = record.value === null
  try {
    const result = props.publicView ? { ...await intelligentTestsAPI.publicDetail(id), anti_degradation: false, started_at: null } : await intelligentTestsAPI.detail(id)
    if (version !== requestVersion) return
    record.value = result; error.value = ''
    if (isPending(result.status) || props.publicView) timer = setTimeout(load, 5000)
  } catch (err) {
    if (version === requestVersion) { record.value = null; error.value = extractApiErrorMessage(err, '无法获取测试结果') }
  } finally { if (version === requestVersion) loading.value = false }
}
watch(() => props.recordId, () => { requestVersion++; clearTimeout(timer); record.value = null; error.value = ''; actionError.value = ''; copied.value = false; fullImage.value = false; void load() }, { immediate: true })
onUnmounted(() => { requestVersion++; clearTimeout(timer) })
async function copy() {
  if (!record.value) return
  try {
    const { id, account_id, test_type, status, score, model, duration_ms, created_at, result, evaluation, config_snapshot, error_message } = record.value
    await copyToClipboard(JSON.stringify({ id, account_id, test_type, status, score, model, duration_ms, created_at, result, evaluation, ...(!props.publicView ? { config_snapshot, error_message } : {}) }, null, 2))
  } catch { copied.value = false }
}
</script>
<style scoped>
.test-output { max-height:22rem; overflow:auto; white-space:pre-wrap; overflow-wrap:anywhere; border-radius:.75rem; padding:1rem; background:rgb(128 128 128 / .06); font-size:.75rem; line-height:1.8; }
</style>
