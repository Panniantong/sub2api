<template>
  <section class="space-y-5" aria-label="Cookie Host 活跃池驾驶舱">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div><h2 class="text-lg font-semibold">Cookie Host 活跃动态池</h2><p class="mt-1 text-sm leading-6 text-gray-500">按已保存策略展示。请求按实际使用的分组计数，账户的原始分组归属不参与流量统计。</p></div>
      <button type="button" class="btn btn-secondary" @click="$emit('settings')">管理账户池策略 →</button>
    </div>
    <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      <div v-for="metric in totals" :key="metric.label" class="card p-5"><p class="text-sm text-gray-500">{{ metric.label }}</p><p class="mt-2 text-3xl font-semibold tabular-nums" :class="metric.tone">{{ metric.value }}</p><p class="mt-2 text-xs leading-5 text-gray-500">{{ metric.hint }}</p></div>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-orange-200 bg-orange-50 px-5 py-4 dark:border-orange-900 dark:bg-orange-950/20">
      <div><h3 class="text-sm font-semibold text-orange-700 dark:text-orange-300">降级流量独立统计</h3><p class="mt-1 text-xs leading-5 text-gray-500">{{ dashboard?.degraded_group_id ? `降级组 #${dashboard.degraded_group_id}` : '未配置降级组' }} · 降级请求不计入正常组当前请求、窗口峰值或动态目标容量。</p></div>
      <div class="flex items-baseline gap-2"><strong class="text-2xl tabular-nums text-orange-600">{{ dashboard?.degraded_group_id ? dashboard.degraded_current_requests ?? '—' : '—' }}</strong><span class="text-sm text-gray-500">降级组当前请求</span></div>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="font-semibold">分组运行状态 <span class="ml-1 text-sm font-normal text-gray-500">{{ rows.length }} 组</span></h3><input v-model="search" type="search" class="input w-full sm:w-64" placeholder="搜索分组名称或编号" aria-label="搜索驾驶舱分组" /></div>
    <p v-if="!rows.length" class="card p-6 text-sm text-gray-500">{{ dashboard ? '暂无账户池策略，请先添加并保存策略。' : '正在加载运行数据…' }}</p>
    <p v-else-if="!filtered.length" class="card p-6 text-sm text-gray-500">没有匹配的分组。</p>
    <div class="grid items-start gap-4 xl:grid-cols-2">
      <article v-for="row in filtered" :key="row.policy.group_id" class="overflow-hidden rounded-2xl border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
        <header class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-5 py-4 dark:border-dark-700"><div><h4 class="font-semibold">{{ row.name || `分组 #${row.policy.group_id}` }}</h4><p class="mt-1 text-xs text-gray-500">#{{ row.policy.group_id }} · {{ row.policy.demand_mode === 'dynamic' ? '实时分配' : '固定目标' }} · 冗余 {{ row.policy.redundancy_mode === 'fixed' ? `${row.policy.redundancy_concurrency || 0} 并发` : `${row.policy.redundancy_percent}%` }}</p></div><span class="rounded-full px-3 py-1 text-xs font-medium" :class="row.enabled ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700'">{{ row.enabled ? '已启用' : '已暂停' }}</span></header>
        <div class="space-y-4 p-5">
          <div class="grid grid-cols-2 gap-4 sm:grid-cols-4"><div v-for="metric in groupMetrics(row)" :key="metric.label"><p class="text-xs text-gray-500">{{ metric.label }}</p><p class="mt-1 text-2xl font-semibold tabular-nums">{{ metric.value }}</p></div></div>
          <div><div class="mb-2 flex justify-between gap-2 text-xs"><span class="text-gray-500">已就绪 / 目标容量</span><span :class="row.ready_capacity < row.target_capacity ? 'text-amber-600' : 'text-emerald-600'">{{ row.ready_capacity }} / {{ row.target_capacity }}{{ row.ready_capacity < row.target_capacity ? ` · 缺口 ${row.target_capacity - row.ready_capacity}` : ' · 已覆盖' }}</span></div><div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"><div class="h-full rounded-full transition-all" :class="row.ready_capacity < row.target_capacity ? 'bg-amber-500' : 'bg-emerald-500'" :style="{ width: `${coverage(row)}%` }" /></div><p v-if="row.low_host && (row.policy.low_host_rotation_enabled || row.low_host.slots_used)" class="mt-2 text-xs text-gray-500">本组总数已包含强制轮转：活跃 {{ row.low_host.active }} 个、准备中 {{ row.low_host.preparing }} 个、已就绪 {{ row.low_host.ready_capacity }} 并发。目标容量仍按分组需求计算。</p></div>
          <div class="grid grid-cols-3 gap-2"><button v-for="(state, index) in states(row)" :key="state.label" type="button" class="rounded-lg bg-gray-50 px-3 py-2 text-left transition hover:bg-primary-50 focus-visible:ring-2 focus-visible:ring-primary-500 dark:bg-dark-900 dark:hover:bg-dark-700" :aria-label="`${row.name || row.policy.group_id} ${state.label} 查看账户`" @click="details = { group: row.policy.group_id, name: row.name, state: detailStates[index]! }"><p class="text-xs text-gray-500">{{ state.label }} ↗</p><p class="mt-1 text-lg font-semibold tabular-nums" :class="state.tone">{{ state.value }}</p></button></div>
          <div class="rounded-xl border border-indigo-200 p-4 dark:border-indigo-900" data-testid="low-host-dashboard">
            <div class="flex flex-wrap justify-between gap-2 text-sm"><strong>低 Host 强制轮转</strong><span>{{ row.policy.low_host_rotation_enabled ? (row.enabled ? '已启用' : '随策略暂停') : '未启用' }}</span></div>
            <p class="mt-2 text-xs text-gray-500">可用 Host 少于 {{ row.policy.low_host_threshold || 5 }} · 强制名额 {{ row.policy.low_host_account_slots || 10 }} · 当前占用 {{ row.low_host?.slots_used ?? '—' }}</p>
            <div class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-3"><button v-for="metric in lowHostMetrics(row)" :key="metric.state" type="button" class="rounded-lg bg-gray-50 p-2 text-left dark:bg-dark-900" :aria-label="`${row.name || row.policy.group_id} ${metric.label} 查看账户`" @click="details = { group: row.policy.group_id, name: row.name, state: metric.state }"><p class="text-xs text-gray-500">{{ metric.label }} ↗</p><p class="mt-1 font-semibold tabular-nums">{{ metric.value }}</p></button></div>
            <p class="mt-2 text-xs leading-5 text-gray-500">其中强制轮转就绪 {{ row.low_host?.ready_capacity ?? '—' }} 并发，已计入上方总数，不重复相加。名额包括准备中和成功保留中的账号；等待账号仍受邮箱去重和安全限制。</p>
          </div>
          <p v-if="row.unavailable" class="text-xs leading-5 text-amber-600">等待原因：{{ Object.entries(row.wait_reasons || {}).map(([reason, count]) => `${reason} ${count}`).join(' · ') }}。点击“暂停 / 等待”查看恢复时间。</p>
          <p class="text-xs leading-5 text-gray-500">按需补位：准备 {{ row.policy.preparation_multiplier || 1 }} 倍 · 同时准备上限 {{ row.policy.max_preparing_accounts || '不限制' }} · 最少活跃 {{ row.policy.min_accounts }} 个 · 最大 {{ row.policy.max_accounts || '不限制' }} · {{ row.policy.demand_mode === 'dynamic' ? `缩容窗口 ${row.policy.scale_down_seconds || 60} 秒` : `固定需求 ${row.policy.target_concurrency}` }}。强制轮转使用单独名额，成功后计入本组活跃账户。当前 {{ row.degraded }} 个账号具有降级资格（含恢复期间继续降智），降级流量单列。</p>
          <p v-if="row.draining" class="text-xs leading-5 text-amber-600">{{ row.draining }} 个账户将在本次绑定到期后退出，当前仍接收请求，不再提前轮换。</p>
          <p v-if="row.current_requests === null" class="text-xs text-amber-600">请求计数暂不可用，不显示为零；调度器保留上次容量目标。</p>
          <p v-if="!row.enabled" class="text-xs text-amber-600">已暂停新请求调度；在途请求仍可完成。</p>
          <p class="text-xs text-gray-400">{{ row.updated_at ? `调度更新于 ${new Date(row.updated_at).toLocaleTimeString()}` : '等待首次调度' }}</p>
        </div>
      </article>
    </div>
    <p class="text-xs leading-5 text-gray-500">当前请求包含执行与等待账号的请求；窗口峰值用于平滑缩容，并非当前并发。已就绪容量只统计绑定可用的活跃账户，准备中及 Host 耗尽账户不计入。</p>
    <CookiePoolAccountDialog v-if="details" :group-id="details.group" :group-name="details.name" :initial-state="details.state" @close="details = null" />
  </section>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import type { CookieDashboard, CookiePoolDashboardGroup } from '@/api/admin/cookies'
import CookiePoolAccountDialog from './CookiePoolAccountDialog.vue'
const props = defineProps<{ dashboard: CookieDashboard | null }>()
const emit = defineEmits<{ settings: []; detailOpen: [boolean] }>()
const search = ref('')
const details = ref<{ group: number; name: string; state: string } | null>(null)
watch(details, value => emit('detailOpen', value !== null))
onUnmounted(() => emit('detailOpen', false))
const detailStates = ['active', 'draining', 'preparing', 'standby', 'exhausted', 'unavailable']
const rows = computed(() => props.dashboard?.pool_groups || [])
const filtered = computed(() => rows.value.filter(r => `${r.name} ${r.policy.group_id}`.toLowerCase().includes(search.value.toLowerCase())))
const sum = (field: 'active' | 'preparing' | 'ready_capacity') => props.dashboard ? rows.value.reduce((total, r) => total + r[field], 0) : '—'
const totals = computed(() => [
  { label: '正常组当前请求', value: !props.dashboard || rows.value.some(r => r.current_requests === null) ? '—' : rows.value.reduce((n, r) => n + (r.current_requests || 0), 0), tone: 'text-sky-600', hint: '包含强制轮转账号承接的正常组请求，不含降级组流量' },
  { label: '活跃账户', value: sum('active'), tone: 'text-emerald-600', hint: '包含强制轮转成功且绑定有效的账户' },
  { label: '准备中账户', value: sum('preparing'), tone: 'text-indigo-600', hint: '包含强制轮转验证，原有降智资格可继续服务' },
  { label: '已就绪容量', value: sum('ready_capacity'), tone: 'text-teal-600', hint: '包含强制轮转就绪并发，不代表实际吞吐' }
])
const coverage = (r: CookiePoolDashboardGroup) => r.target_capacity > 0 ? Math.min(100, Math.round(100 * r.ready_capacity / r.target_capacity)) : r.ready_capacity > 0 ? 100 : 0
const groupMetrics = (r: CookiePoolDashboardGroup) => [{ label: '当前请求', value: r.current_requests ?? '—' }, { label: '窗口峰值', value: r.window_demand ?? '—' }, { label: '目标容量', value: r.target_capacity }, { label: '已就绪', value: r.ready_capacity }]
const states = (r: CookiePoolDashboardGroup) => [{ label: '活跃（含到期退出）', value: r.active, tone: 'text-emerald-600' }, { label: '到期退出', value: r.draining || 0, tone: 'text-amber-600' }, { label: '准备中', value: r.preparing, tone: 'text-indigo-600' }, { label: '备用', value: r.standby, tone: '' }, { label: 'Host 耗尽', value: r.exhausted, tone: 'text-orange-600' }, { label: '暂停 / 等待', value: r.unavailable, tone: 'text-gray-500' }]
const lowHostMetrics = (r: CookiePoolDashboardGroup) => [
  { label: '强制准备', state: 'low_preparing', value: r.low_host?.preparing ?? '—' },
  { label: '成功保留', state: 'low_active', value: r.low_host?.active ?? '—' },
  { label: '等待处理', state: 'low_waiting', value: r.low_host?.waiting ?? '—' },
  { label: '受限 / 退避', state: 'low_blocked', value: r.low_host?.blocked ?? '—' },
  { label: '待释放名额', state: 'low_retiring', value: r.low_host?.retiring ?? '—' },
  { label: '继续降智', state: 'low_degraded', value: r.low_host?.degraded_continuing ?? '—' },
]
</script>
