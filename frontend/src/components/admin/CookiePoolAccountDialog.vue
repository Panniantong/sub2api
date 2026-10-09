<template>
  <BaseDialog :show="true" :title="`${groupName || '#' + groupId} · 账户与 Host`" width="extra-wide" @close="$emit('close')">
    <div class="space-y-4">
      <div class="flex flex-wrap items-center gap-3">
        <select v-model="state" class="input w-full sm:w-48" aria-label="账户状态筛选"><option value="">全部账户</option><option v-for="(label, key) in labels" :key="key" :value="key">{{ label }}</option></select>
        <input v-model="search" type="search" class="input min-w-0 flex-1" placeholder="搜索账户、邮箱、Host" aria-label="搜索池账户" />
        <button type="button" class="btn btn-secondary" :disabled="loading" @click="refresh">刷新</button>
      </div>
      <p class="text-xs text-gray-500">每 5 秒刷新 · 当前 {{ filtered.length }} 个账户。准备名额还受额度、重试等待、邮箱冷却及可用账户数量限制。</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}，可点击刷新重试。</p>
      <p v-if="!loaded && loading" class="text-sm text-gray-500">正在读取账户与 Host…</p>
      <p v-else-if="!filtered.length" class="rounded-xl bg-gray-50 p-5 text-sm text-gray-500 dark:bg-dark-800">当前没有该状态的账户；账户可能已完成准备或进入等待，可切换“全部账户”查看。</p>
      <article v-for="a in filtered" :key="a.id" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600">
        <header class="flex flex-wrap items-start justify-between gap-2"><div class="min-w-0"><h4 class="break-all font-medium">#{{ a.id }} · {{ a.name }}</h4><p class="mt-1 break-all text-xs text-gray-500">{{ a.email || '未记录邮箱' }} · {{ a.concurrency }} 并发</p></div><span class="rounded bg-indigo-50 px-2 py-1 text-xs text-indigo-600 dark:bg-indigo-950/30">{{ labels[a.state] || a.state }}</span></header>
        <p v-if="a.state === 'unavailable' || a.state === 'exhausted'" class="text-sm text-amber-600">{{ a.reason }}<span v-if="a.resume_at"> · 最早恢复 {{ formatTime(a.resume_at) }}（{{ remaining(a.resume_at) }} 秒）</span></p>
        <p v-if="a.preparing_since" class="text-sm text-indigo-600">已准备 {{ elapsed(a.preparing_since) }} 秒 · 本轮剩余 {{ remaining(a.preparing_deadline) }} 秒</p>
        <div class="space-y-1 text-xs leading-5"><p class="break-all"><span class="text-gray-500">正式绑定：</span>{{ a.bound_host || '未绑定' }}<span v-if="a.binding_expires_at"> · 至 {{ formatTime(a.binding_expires_at) }}</span></p><p class="break-all"><span class="text-gray-500">本轮最近尝试 Host：</span>{{ a.validation_host || '当前实例暂无执行中的 Host 验证' }}</p><p v-if="a.validation_stage"><span class="text-gray-500">验证进度：</span>{{ stages[a.validation_stage] || a.validation_stage }} · {{ formatTime(a.validation_updated_at) }}</p></div>
        <details class="rounded-lg bg-gray-50 p-3 dark:bg-dark-900">
          <summary class="cursor-pointer text-sm font-medium">查看 Host（可绑定 {{ a.available_hosts }} / 库内 {{ a.hosts.length }}）</summary>
          <div class="mt-3 flex flex-wrap gap-2"><input v-model="hostSearch" class="input min-w-0 flex-1" placeholder="搜索 Host" aria-label="搜索 Host" /><select v-model="hostState" class="input w-36" aria-label="Host 状态筛选"><option value="">全部状态</option><option v-for="(label, key) in hostLabels" :key="key" :value="key">{{ label }}</option></select></div>
          <div class="mt-3 max-h-64 space-y-2 overflow-y-auto"><div v-for="host in visibleHosts(a)" :key="host.host" class="rounded border border-gray-200 p-2 text-xs dark:border-dark-600"><p class="break-all font-medium">{{ host.host }} <span class="ml-2 text-gray-500">{{ hostLabels[host.state] || host.state }}</span></p><p class="mt-1 text-gray-500">Cookie 有效至 {{ formatTime(host.expires_at) }}<span v-if="host.cooldown_until"> · 邮箱冷却至 {{ formatTime(host.cooldown_until) }}（{{ remaining(host.cooldown_until) }} 秒）</span></p></div><p v-if="!visibleHosts(a).length" class="text-xs text-gray-500">没有匹配的 Host。</p></div>
        </details>
      </article>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getDashboard, type CookiePoolDashboardAccount } from '@/api/admin/cookies'
const props = defineProps<{ groupId: number; groupName: string; initialState: string }>()
defineEmits<{ close: [] }>()
const state = ref(props.initialState)
const search = ref(''), hostSearch = ref(''), hostState = ref('')
const accounts = ref<CookiePoolDashboardAccount[]>([])
const loading = ref(false), loaded = ref(false), error = ref(''), now = ref(Date.now())
const labels: Record<string, string> = { active: '活跃（含到期退出）', draining: '到期退出', preparing: '准备中', standby: '备用', exhausted: 'Host 耗尽', unavailable: '暂停 / 等待' }
const hostLabels: Record<string, string> = { available: '可绑定', bound: '当前绑定', cooldown: '邮箱冷却中', expired: 'Cookie 过期 / 缺失', out_of_scope: '不在白名单' }
const stages: Record<string, string> = { host_selected: '已选择 Host', host_cookie_bound: '候选 Cookie 已就位', validation_started: '验证请求进行中', validation_retry: '验证重试', validation_failed: '验证失败，准备尝试下一个', validation_rejected: '验证返回 no', validation_succeeded: '验证通过，正在接入', host_bound: '正式绑定完成', validation_skipped: '本次验证已跳过', validation_usage_limited: '验证额度不足', host_bind_failed: '绑定未接入', ws_build_skipped: 'Host 验证已完成' }
const filtered = computed(() => accounts.value.filter(a => (!state.value || a.state === state.value || state.value === 'active' && a.state === 'draining') && `${a.id} ${a.name} ${a.email} ${a.bound_host} ${a.validation_host}`.toLowerCase().includes(search.value.toLowerCase())))
const visibleHosts = (a: CookiePoolDashboardAccount) => a.hosts.filter(h => (!hostState.value || h.state === hostState.value) && h.host.toLowerCase().includes(hostSearch.value.toLowerCase()))
const formatTime = (value?: string) => value ? new Date(value).toLocaleString() : '—'
const remaining = (value?: string) => value ? Math.max(0, Math.ceil((Date.parse(value) - now.value) / 1000)) : 0
const elapsed = (value: string) => Math.max(0, Math.floor((now.value - Date.parse(value)) / 1000))
let disposed = false
async function refresh() {
  if (loading.value || disposed) return
  loading.value = true
  try { const data = await getDashboard(props.groupId); if (!disposed) { accounts.value = data.pool_groups?.find(g => g.policy.group_id === props.groupId)?.accounts || []; loaded.value = true; error.value = '' } }
  catch { if (!disposed) error.value = '账户明细读取失败' }
  finally { loading.value = false }
}
let poll: ReturnType<typeof setInterval>, clock: ReturnType<typeof setInterval>
onMounted(() => { void refresh(); poll = setInterval(() => { if (!document.hidden) void refresh() }, 5000); clock = setInterval(() => { now.value = Date.now() }, 1000) })
onUnmounted(() => { disposed = true; clearInterval(poll); clearInterval(clock) })
</script>
