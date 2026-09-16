<template>
  <details :open="embedded" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700" data-testid="account-traffic-controls" @toggle="onToggle">
    <summary class="cursor-pointer text-sm font-medium">可选流量控制 · 严格 RPM / 自适应并发</summary>
    <p class="mt-3 text-xs leading-relaxed text-gray-500">两个功能独立开关，默认关闭。启用后在账号固定并发上限内执行，关闭后恢复固定上限；不会改动身份或 TLS 策略。</p>
    <p v-if="platform === 'grok'" class="mt-2 text-xs leading-relaxed text-amber-700 dark:text-amber-300">Grok 实时语音目前只能按会话观测。开启严格 RPM 或自动并发后，实时语音连接会被明确拒绝；HTTP 语音接口仍支持这些限制。</p>
    <p v-if="loading" class="mt-3 text-sm text-gray-500">正在读取配置…</p>
    <div v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }} <button type="button" class="underline" @click="load">重试</button></div>
    <fieldset v-if="ready || embedded" :disabled="saving || disabled" class="mt-4 space-y-4" @input="saved = false" @change="saved = false">
      <div class="rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">
        <p class="leading-relaxed">起步建议：RPM 60、突发 5、最低并发 1；60 秒内累计 3 次上游 429 / 5xx 后建议降速，稳定 60 秒且至少成功 3 次后逐步恢复。这是可修改的起点，不代表上游账号的实际额度。</p>
        <button type="button" class="mt-2 text-primary-600 underline" data-testid="traffic-recommended" @click="useRecommended">填入建议参数</button>
        <span class="ml-2 text-gray-500">只填数值，保留功能开关和运行方式；点击保存后生效</span>
      </div>
      <label class="flex items-center gap-2 text-sm"><input :form="embedded ? undefined : detachedFormId" v-model="policy.strict_rpm_enabled" type="checkbox" data-testid="strict-rpm-toggle" />启用严格 RPM</label>
      <div v-if="policy.strict_rpm_enabled" class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">每分钟硬上限<input :form="embedded ? undefined : detachedFormId" v-model.number="policy.rpm" class="input mt-1" type="number" min="1" max="60000" /></label>
        <label class="text-sm">瞬时突发额度<input :form="embedded ? undefined : detachedFormId" v-model.number="policy.burst" class="input mt-1" type="number" min="1" :max="policy.rpm" /></label>
        <p class="text-xs text-gray-500 sm:col-span-2">滚动 60 秒内不超过硬上限，突发额度包含在上限内。模型请求与重试共享预算；达到限制时返回可重试提示。</p>
      </div>
      <label class="flex items-center gap-2 text-sm"><input :form="embedded ? undefined : detachedFormId" v-model="policy.adaptive_enabled" type="checkbox" data-testid="adaptive-toggle" />启用自适应并发</label>
      <div v-if="policy.adaptive_enabled" class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">运行方式<select :form="embedded ? undefined : detachedFormId" v-model="policy.adaptive_mode" class="input mt-1"><option value="observe">只显示建议，不自动调整</option><option value="automatic">自动降速、缓慢恢复</option></select></label>
        <label class="text-sm">最低并发<input :form="embedded ? undefined : detachedFormId" v-model.number="policy.min_concurrency" class="input mt-1" type="number" min="1" :max="hardLimit > 0 ? hardLimit : 10000" /></label>
        <label class="text-sm">触发失败次数<input :form="embedded ? undefined : detachedFormId" v-model.number="policy.failure_threshold" class="input mt-1" type="number" min="1" max="100" /></label>
        <label class="text-sm">失败统计窗口（秒）<input :form="embedded ? undefined : detachedFormId" v-model.number="policy.failure_window_seconds" class="input mt-1" type="number" min="10" max="3600" /></label>
        <label class="text-sm">稳定恢复周期（秒）<input :form="embedded ? undefined : detachedFormId" v-model.number="policy.recovery_seconds" class="input mt-1" type="number" min="10" max="3600" /></label>
        <p class="text-xs text-gray-500 sm:col-span-2">上游 429 / 5xx 达到阈值时建议将并发减半；稳定一段时间且至少完成 3 次成功响应后，每周期恢复 1 个名额。{{ embedded ? '本次设置的并发上限' : '当前固定上限' }}：{{ hardLimit > 0 ? hardLimit : '未设置，请先设置账号并发' }}。以后调低固定上限时，运行中的最低并发也会随之限制。</p>
      </div>
      <p v-if="embedded" class="text-xs text-gray-500">与账号其他设置一起保存，关闭开关即可停用该功能。</p>
      <div v-else class="flex flex-wrap items-center gap-3"><button type="button" class="btn btn-secondary" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存流量控制' }}</button><span v-if="saved" class="text-xs text-emerald-700">已保存</span><span class="text-xs text-gray-500">此区域独立保存</span></div>
    </fieldset>
    <div v-if="ready" class="mt-4 rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900">
      <div class="flex items-center justify-between"><span>已保存配置的运行观测</span><button type="button" class="text-primary-600" :disabled="loading || saving || refreshing" @click="refreshState">{{ refreshing ? '刷新中…' : '刷新状态' }}</button></div>
      <p v-if="!stateAvailable" class="mt-2 text-gray-500">状态暂不可用，配置仍可查看。严格 RPM 或自动并发控制无法确认准入时会暂停新请求；仅建议模式继续使用固定并发。</p>
      <p v-else-if="!activePolicy.strict_rpm_enabled && !activePolicy.adaptive_enabled" class="mt-2 text-gray-500">当前未启用流量控制，不主动采集这组观测。</p>
      <dl v-else-if="state" class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-3">
        <div><dt>有效并发上限</dt><dd>{{ state.effective_concurrency || '不限' }}</dd></div><div><dt>建议并发</dt><dd>{{ state.recommended_concurrency || '—' }}</dd></div><div><dt>执行中</dt><dd>{{ state.in_flight }}</dd></div>
        <div><dt>最近一分钟请求</dt><dd>{{ state.requests_last_minute }}</dd></div><div><dt>上游 429 / 5xx</dt><dd>{{ state.upstream_429 }} / {{ state.upstream_5xx }}</dd></div><div><dt>本地 RPM / 并发拒绝</dt><dd>{{ state.rejected_rpm }} / {{ state.rejected_concurrency }}</dd></div>
        <div><dt>平均响应耗时</dt><dd>{{ (state.average_duration_ms / 1000).toFixed(2) }} 秒</dd></div>
      </dl>
      <p v-if="stateAvailable && (activePolicy.strict_rpm_enabled || activePolicy.adaptive_enabled)" class="mt-2 text-gray-500">累计数来自当前缓存观测周期，长期无流量或缓存重置后会重新累计。</p>
    </div>
  </details>
</template>
<script setup lang="ts">
import { computed, getCurrentInstance, onUnmounted, ref, watch } from 'vue'
import { accountTrafficAPI, defaultTrafficPolicy, normalizeTrafficDraft, trafficPolicyError, type AccountTrafficPolicy, type AccountTrafficState } from '@/api/admin/accountTraffic'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ accountId: number; platform?: string; embedded?: boolean; modelValue?: AccountTrafficPolicy; hardLimit?: number; disabled?: boolean }>()
const emit = defineEmits<{ saved: [policy: AccountTrafficPolicy]; 'update:modelValue': [policy: AccountTrafficPolicy] }>()
const detachedFormId = `account-traffic-${getCurrentInstance()?.uid}`
const policy = ref(defaultTrafficPolicy()), activePolicy = ref(defaultTrafficPolicy()), state = ref<AccountTrafficState | null>(null)
const loading = ref(false), saving = ref(false), refreshing = ref(false), ready = ref(false), saved = ref(false), error = ref(''), stateAvailable = ref(false), storedHardLimit = ref(0), expanded = ref(false)
const hardLimit = computed(() => props.hardLimit ?? storedHardLimit.value)
const samePolicy = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)
watch(() => props.modelValue, value => { if (value && !samePolicy(value, policy.value)) policy.value = { ...value } }, { immediate: true, deep: true })
watch(policy, value => { if (props.embedded && !samePolicy(value, props.modelValue)) emit('update:modelValue', { ...value }) }, { deep: true })
let version = 0, controller: AbortController | undefined
function useRecommended() {
  policy.value = { ...defaultTrafficPolicy(), strict_rpm_enabled: policy.value.strict_rpm_enabled, adaptive_enabled: policy.value.adaptive_enabled, adaptive_mode: policy.value.adaptive_mode }
  saved.value = false; error.value = ''
}
function onToggle(event: Event) { expanded.value = (event.target as HTMLDetailsElement).open; if (expanded.value && !ready.value && !loading.value) void load() }
async function load() { await readState(!props.embedded) }
async function readState(replaceDraft: boolean) {
  if (saving.value) return
  const current = ++version; controller?.abort(); controller = new AbortController(); loading.value = !ready.value; refreshing.value = true; error.value = ''
  try {
    const data = await accountTrafficAPI.get(props.accountId, controller.signal)
    if (current !== version) return
    if (replaceDraft) policy.value = { ...data.policy }
    activePolicy.value = { ...data.policy }; state.value = data.state ?? null; stateAvailable.value = data.state_available; storedHardLimit.value = data.hard_limit; ready.value = true
  } catch (err) { if (current === version) error.value = extractApiErrorMessage(err, '无法读取流量控制配置') }
  finally { if (current === version) { loading.value = false; refreshing.value = false } }
}
async function refreshState() { await readState(false) }
function prepareForSave(): AccountTrafficPolicy | null {
  const normalized = normalizeTrafficDraft(policy.value)
  error.value = trafficPolicyError(normalized, hardLimit.value)
  if (error.value) return null
  policy.value = normalized
  return { ...normalized }
}
async function save() {
  if (saving.value || props.embedded) return
  const snapshot = prepareForSave()
  if (!snapshot) return
  const current = ++version, id = props.accountId
  controller?.abort(); refreshing.value = false; loading.value = false
  saving.value = true; saved.value = false
  try {
    const data = await accountTrafficAPI.save(id, snapshot)
    if (current !== version) return
    policy.value = { ...data.policy }; activePolicy.value = { ...data.policy }; saved.value = true; stateAvailable.value = data.state_available
    emit('saved', { ...data.policy })
    if (!data.state_available) error.value = '配置已保存，运行状态同步暂不可用，请稍后刷新状态'
    else { saving.value = false; await refreshState() }
  } catch (err) { if (current === version) error.value = extractApiErrorMessage(err, '流量控制保存失败') }
  finally { saving.value = false }
}
watch(() => props.accountId, () => { version++; controller?.abort(); ready.value = false; loading.value = false; refreshing.value = false; saved.value = false; state.value = null; if (expanded.value) void load() })
onUnmounted(() => { version++; controller?.abort() })
defineExpose({ prepareForSave })
</script>
