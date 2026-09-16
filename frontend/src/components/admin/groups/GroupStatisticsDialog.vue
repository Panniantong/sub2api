<template>
  <BaseDialog :show="groupId !== null" :title="stats ? `${stats.group_name} · 完整统计` : '分组完整统计'" width="wide" @close="emit('close')">
    <p class="text-xs leading-relaxed text-gray-500">用量按请求发生时的分组归属统计。三种金额分别展示，避免把用户计费与上游成本混在一起；订阅用量不是现金支付，上游成本采用本地保存的定价与倍率。</p>
    <div class="mt-4 flex flex-wrap items-end gap-3">
      <label class="text-xs">开始时间<input v-model="from" type="datetime-local" class="input mt-1" /></label>
      <label class="text-xs">结束时间（不含）<input v-model="to" type="datetime-local" class="input mt-1" /></label>
      <button type="button" class="btn btn-secondary" :disabled="loading" @click="load">应用 / 刷新</button>
      <button type="button" class="btn btn-secondary" :disabled="loading" @click="from = ''; to = ''; load()">全部历史</button>
    </div>
    <p v-if="loading" role="status" class="py-8 text-center text-sm text-gray-500">正在统计…</p>
    <p v-if="error" role="alert" class="mt-4 text-sm text-red-600">{{ error }}</p>
    <div v-if="stats && !loading" class="mt-5 space-y-4">
      <div class="grid gap-3 sm:grid-cols-3">
        <div v-for="item in amounts" :key="item.label" class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900"><p class="text-xs text-gray-500">{{ item.label }}</p><p class="mt-2 break-all text-lg font-semibold tabular-nums">${{ money(item.value) }}</p><p class="mt-2 text-xs text-gray-500">{{ item.note }}</p></div>
      </div>
      <dl class="grid grid-cols-2 gap-4 rounded-xl border border-gray-200 p-4 text-sm dark:border-dark-700 sm:grid-cols-3">
        <div><dt class="text-xs text-gray-500">已记录请求</dt><dd>{{ stats.total_requests.toLocaleString() }}</dd></div>
        <div><dt class="text-xs text-gray-500">Tokens（含缓存）</dt><dd>{{ stats.total_tokens.toLocaleString() }}</dd></div>
        <div><dt class="text-xs text-gray-500">平均耗时</dt><dd>{{ (stats.average_duration_ms / 1000).toFixed(2) }} 秒</dd></div>
        <div><dt class="text-xs text-gray-500">当前密钥</dt><dd>{{ stats.total_api_keys }}</dd></div>
        <div><dt class="text-xs text-gray-500">启用且未过期密钥</dt><dd>{{ stats.active_api_keys }}</dd></div>
        <div><dt class="text-xs text-gray-500">当前绑定账号</dt><dd>{{ stats.total_accounts }}</dd></div>
        <div><dt class="text-xs text-gray-500">余额计费用量</dt><dd>${{ money(stats.balance_cost) }}</dd></div>
        <div><dt class="text-xs text-gray-500">订阅计费用量</dt><dd>${{ money(stats.subscription_cost) }}</dd></div>
        <div><dt class="text-xs text-gray-500">零计费记录</dt><dd>{{ stats.zero_charge_requests }}</dd></div>
      </dl>
      <p v-if="stats.total_requests === 0" class="text-sm text-gray-500">当前范围内没有用量记录。</p>
      <p class="text-xs text-gray-500">{{ stats.from || stats.to ? '已按选定时间范围统计用量' : '全部历史用量' }} · 生成于 {{ new Date(stats.generated_at).toLocaleString() }}。零计费记录可能包括免费请求与失败记录，不作为成功率。</p>
    </div>
    <template #footer><button type="button" class="btn btn-secondary" @click="emit('close')">关闭</button></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getStats, type GroupDetailStats } from '@/api/admin/groups'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ groupId: number | null }>()
const emit = defineEmits<{ close: [] }>()
const stats = ref<GroupDetailStats | null>(null), loading = ref(false), error = ref(''), from = ref(''), to = ref('')
let controller: AbortController | undefined, version = 0
const money = (value: number) => value.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 10 })
const amounts = computed(() => stats.value ? [
  { label: '实际计费用量', value: stats.value.total_actual_cost, note: '余额扣减与订阅消耗，按历史用量记录汇总' },
  { label: '账面费用', value: stats.value.total_cost, note: '使用记录中的标准定价费用' },
  { label: '上游账号成本（本地统计）', value: stats.value.total_account_cost, note: '使用每条记录的账号成本及历史倍率，不是上游发票' }
] : [])
async function load() {
  if (props.groupId === null) return
  const params: { from?: string; to?: string } = {}
  for (const [key, value] of [['from', from.value], ['to', to.value]] as const) {
    if (value) {
      const date = new Date(value)
      if (!Number.isFinite(date.getTime())) { error.value = '时间格式无效'; return }
      params[key] = date.toISOString()
    }
  }
  if (params.from && params.to && params.from >= params.to) { error.value = '结束时间必须晚于开始时间'; return }
  const current = ++version; controller?.abort(); controller = new AbortController(); loading.value = true; error.value = ''
  try { const result = await getStats(props.groupId, params, controller.signal); if (current === version) stats.value = result }
  catch (err) { if (current === version) { stats.value = null; error.value = extractApiErrorMessage(err, '无法获取分组统计') } }
  finally { if (current === version) loading.value = false }
}
watch(() => props.groupId, () => { version++; controller?.abort(); stats.value = null; error.value = ''; from.value = ''; to.value = ''; void load() }, { immediate: true })
onUnmounted(() => { version++; controller?.abort() })
</script>
