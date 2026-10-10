<template>
  <section class="space-y-5 border-t border-gray-200 pt-6 dark:border-dark-700">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div><h4 class="text-base font-semibold">按分组配置活跃账户池</h4><p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500">随请求负载分配活跃账户，Host 耗尽后自动补位。一条规则可覆盖多个分组，各分组独立计算容量。</p></div>
      <button type="button" class="btn btn-secondary" :disabled="!availableGroups.length" @click="addPool">＋ 添加策略</button>
    </div>
    <div class="grid gap-3 text-xs sm:grid-cols-3">
      <div class="rounded-xl bg-sky-50 p-4 dark:bg-sky-950/30"><strong class="text-sky-700 dark:text-sky-300">01 / 按需求启用</strong><p class="mt-2 leading-5 text-gray-600 dark:text-gray-400">70 并发、50% 冗余、每号 35，启用 3 个账号。动态模式会随实时需求调整。</p></div>
      <div class="rounded-xl bg-indigo-50 p-4 dark:bg-indigo-950/30"><strong class="text-indigo-700 dark:text-indigo-300">02 / 耗尽自动补位</strong><p class="mt-2 leading-5 text-gray-600 dark:text-gray-400">仅活跃账户接收请求。备用、暂停和准备中的账户不调度，补位验证通过后立即接入。</p></div>
      <div class="rounded-xl bg-amber-50 p-4 dark:bg-amber-950/30"><strong class="text-amber-700 dark:text-amber-300">03 / Host 耗尽才降级</strong><p class="mt-2 leading-5 text-gray-600 dark:text-gray-400">仍有候选 Host 时继续轮换。空库、读取失败或绑定到期不作为耗尽证据。</p></div>
    </div>
    <p v-if="!modelValue.length" class="rounded-xl border border-dashed p-5 text-sm text-gray-500 dark:border-dark-600">尚未添加策略。添加后按需启用账户；其他轮换分组继续使用原有的全量轮换方式。</p>
    <article v-for="(policy, index) in modelValue" :key="index" class="overflow-hidden rounded-2xl border border-gray-200 dark:border-dark-600">
      <header class="flex flex-wrap items-center justify-between gap-4 border-b border-gray-100 bg-gray-50/70 px-5 py-4 dark:border-dark-700 dark:bg-dark-800/70">
        <div class="flex items-center gap-3"><span class="flex h-9 w-9 items-center justify-center rounded-lg bg-white text-sm font-semibold text-gray-500 shadow-sm dark:bg-dark-700">{{ String(index + 1).padStart(2, '0') }}</span><div><h5 class="font-semibold">账户池策略</h5><p class="mt-0.5 text-xs text-gray-500">{{ policyGroups(policy).length }} 个分组 · {{ policy.demand_mode === 'dynamic' ? '实时分配' : '固定容量' }} · 保存后生效</p></div></div>
        <div class="flex flex-wrap items-center gap-3">
          <button type="button" role="switch" :aria-checked="policy.enabled" :aria-label="`启用账户池策略 ${index + 1}`" class="flex items-center gap-3 rounded-xl border px-4 py-2 text-left transition focus-visible:ring-2 focus-visible:ring-primary-500" :class="policy.enabled ? 'border-emerald-200 bg-emerald-50 dark:border-emerald-900 dark:bg-emerald-950/30' : 'border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800'" @click="policy.enabled = !policy.enabled">
            <span><strong class="block text-sm" :class="policy.enabled ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-600 dark:text-gray-300'">{{ policy.enabled ? '已启用' : '已暂停' }}</strong><span class="block text-xs text-gray-500">{{ policy.enabled ? '按策略分配账户' : '不轮换、不调度' }}</span></span>
            <span class="relative h-6 w-11 shrink-0 rounded-full transition" :class="policy.enabled ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-dark-600'"><span class="absolute left-1 top-1 h-4 w-4 rounded-full bg-white shadow transition-transform" :class="policy.enabled ? 'translate-x-5' : ''" /></span>
          </button>
          <button type="button" class="rounded-lg px-3 py-2 text-sm text-gray-500 transition hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-950/30" @click="removeIndex = index">移除规则</button>
        </div>
      </header>
      <div class="space-y-5 p-5">
        <div>
          <span class="text-sm font-medium">Host 轮换分组（可多选）</span>
          <button type="button" class="input mt-2 flex min-h-12 w-full items-center justify-between text-left" aria-label="选择策略的 Host 轮换分组" @click="openPicker(index)"><span class="flex min-w-0 flex-wrap gap-1.5"><span v-for="id in policyGroups(policy)" :key="id" class="max-w-full truncate rounded-md bg-primary-50 px-2.5 py-1 text-xs text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ groupName(id) }}</span><span v-if="!policyGroups(policy).length" class="text-gray-500">选择分组</span></span><span class="ml-3 shrink-0 text-gray-400">⌄</span></button>
          <p class="mt-2 text-xs text-gray-500">共用下面的配置，各分组独立统计需求和选择账户，不合并并发。</p>
          <p v-if="policyGroups(policy).some(id => !selectedGroups.includes(id))" class="mt-2 text-xs text-amber-600">部分分组已不在上方的轮换范围内，请重新选择后保存。</p>
        </div>
        <p v-if="!policy.enabled" role="status" class="rounded-lg bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:bg-amber-950/20 dark:text-amber-300">保存后暂停这些分组的账户池：不轮换、不调度，也不进入降级组。移除规则则恢复全量轮换。</p>
        <div class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
          <div class="flex flex-wrap items-center justify-between gap-3"><span class="text-sm font-semibold">分组需求并发</span><div class="inline-flex rounded-lg bg-gray-100 p-1 dark:bg-dark-800"><button v-for="mode in modes" :key="mode.value" type="button" class="rounded-md px-4 py-2 text-sm transition" :aria-pressed="(policy.demand_mode || 'fixed') === mode.value" :class="(policy.demand_mode || 'fixed') === mode.value ? 'bg-white font-medium text-primary-600 shadow-sm dark:bg-dark-600 dark:text-primary-300' : 'text-gray-500'" @click="policy.demand_mode = mode.value">{{ mode.label }}</button></div></div>
          <div v-if="policy.demand_mode === 'dynamic'" class="mt-3 space-y-2"><p class="text-sm leading-6 text-gray-600 dark:text-gray-400">按分组内正在执行及等待账号的请求数实时计算。每秒检查，需求上升时扩容；使用观察窗口内的峰值缓冲缩容，避免账户频繁进出。</p><p class="text-xs text-gray-500">WebSocket 按请求轮次计数，空闲连接不占需求。无流量时保留最少活跃账户，冗余账户也参与请求。</p></div>
          <label v-else class="mt-3 block max-w-sm text-sm">固定需求并发<input v-model.number="policy.target_concurrency" required type="number" min="1" max="1000000" class="input mt-2 w-full" /></label>
        </div>
        <fieldset class="grid gap-x-5 gap-y-5 sm:grid-cols-2 xl:grid-cols-3">
          <label class="text-sm">容量不足时重试间隔（秒）<input :value="policy.shortage_retry_seconds || 0" required type="number" min="0" :max="policy.retry_seconds" class="input mt-2 w-full" @input="policy.shortage_retry_seconds = Number(($event.target as HTMLInputElement).value)" /><span class="mt-2 block text-xs text-gray-500">0 沿用普通间隔；可设 5 秒至普通重试间隔，例如 10 秒。只加快缺口补位，不跳过额度限制或邮箱 Host 冷却。</span></label>
          <label class="text-sm">Host 准备额度优先顺序<select :value="policy.quota_priority || ''" class="input mt-2 w-full" @change="policy.quota_priority = ($event.target as HTMLSelectElement).value as '' | 'used_desc' | 'remaining_desc'"><option value="">保持原顺序</option><option value="used_desc">已用额度接近上限优先</option><option value="remaining_desc">剩余额度充足优先</option></select><span class="mt-2 block text-xs text-gray-500">按有效的 5 小时 / 7 天窗口中已用比例较高者排序，仅决定哪些账户优先准备 Cookie Host，不影响请求分配。额度未知排后，仍遵守停用阈值。</span></label>
          <label class="text-sm">准备倍率<input :value="policy.preparation_multiplier || 1" required type="number" min="1" max="5" class="input mt-2 w-full" @input="policy.preparation_multiplier = Number(($event.target as HTMLInputElement).value)" /><span class="mt-2 block text-xs text-gray-500">缺 3 个账户、倍率 2，最多启动 6 个准备账户。先成功先接入，目标满足后取消多余准备。</span></label>
          <label class="text-sm">同时准备账户上限<input :value="policy.max_preparing_accounts || 0" required type="number" min="0" max="10000" class="input mt-2 w-full" @input="policy.max_preparing_accounts = Number(($event.target as HTMLInputElement).value)" /><span class="mt-2 block text-xs text-gray-500">0 表示不另设上限。准备名额独立于最大活跃账户，仍受本组可用邮箱数量限制。</span></label>
          <div class="rounded-xl border border-primary-100 bg-primary-50/30 p-4 text-sm sm:col-span-2 xl:col-span-3 dark:border-dark-600 dark:bg-dark-800">
            <div class="flex flex-wrap items-center justify-between gap-3"><span class="font-semibold">容量冗余</span><div class="inline-flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700"><button v-for="mode in redundancyModes" :key="mode.value" type="button" class="rounded-md px-4 py-2 text-sm" :aria-pressed="(policy.redundancy_mode || 'percent') === mode.value" :class="(policy.redundancy_mode || 'percent') === mode.value ? 'bg-white font-medium text-primary-600 shadow-sm dark:bg-dark-600 dark:text-primary-300' : 'text-gray-500'" @click="policy.redundancy_mode = mode.value">{{ mode.label }}</button></div></div>
            <label v-if="policy.redundancy_mode === 'fixed'" class="mt-3 block max-w-sm">额外预留并发<input :value="policy.redundancy_concurrency || 0" required type="number" min="0" max="1000000" class="input mt-2 w-full" @input="policy.redundancy_concurrency = Number(($event.target as HTMLInputElement).value)" /><span class="mt-2 block text-xs text-gray-500">目标容量 = 需求并发 + 固定冗余。例如需求 70、预留 35，目标为 105；无流量时仍保留固定冗余容量。</span></label>
            <label v-else class="mt-3 block max-w-sm">冗余比例（%）<input v-model.number="policy.redundancy_percent" required type="number" min="0" max="500" class="input mt-2 w-full" /><span class="mt-2 block text-xs text-gray-500">目标容量 = 需求并发 ×（1 + 冗余比例），向上取整。例如需求 70、冗余 50%，目标为 105。</span></label>
            <p class="mt-3 text-xs text-gray-500">两种数值分别保存，仅所选方式生效，不叠加。冗余账户正常参与请求，实际容量按账户并发累加，并遵守活跃账户上限及绑定到期退出规则。</p>
          </div>
          <label class="text-sm">最少活跃账户<input v-model.number="policy.min_accounts" required type="number" min="1" :max="policy.max_accounts || 10000" class="input mt-2 w-full" /><span class="mt-2 block text-xs text-gray-500">无流量时仍保持这些账户准备就绪。</span></label>
          <div class="text-sm"><span>最大活跃账户</span><div class="mt-2 flex gap-2"><select :value="policy.max_accounts === 0 ? 'unlimited' : 'limited'" class="input min-w-0 flex-1" aria-label="最大活跃账户限制" @change="setLimit(policy, $event)"><option value="unlimited">不限制</option><option value="limited">指定上限</option></select><input v-if="policy.max_accounts > 0" v-model.number="policy.max_accounts" required type="number" :min="policy.min_accounts" max="10000" class="input w-24" aria-label="最大活跃账户数量" /></div><p class="mt-2 text-xs text-gray-500">不限制策略上限，最多使用本组可用账户。</p></div>
          <label v-if="policy.demand_mode === 'dynamic'" class="text-sm">缩容观察窗口（秒）<input :value="policy.scale_down_seconds || 60" required type="number" min="10" max="3600" class="input mt-2 w-full" @input="policy.scale_down_seconds = Number(($event.target as HTMLInputElement).value)" /><span class="mt-2 block text-xs text-gray-500">默认 60 秒，窗口内需求峰值过后再缩容。</span></label>
          <label class="text-sm">补位就绪超时（秒）<input v-model.number="policy.ready_timeout_seconds" required type="number" min="10" max="3600" class="input mt-2 w-full" /><span class="mt-2 block text-xs text-gray-500">验证未完成则让出名额，尝试下个账户。</span></label>
          <label class="text-sm">失败账户重试间隔（秒）<input v-model.number="policy.retry_seconds" required type="number" min="10" max="86400" class="input mt-2 w-full" /><span class="mt-2 block text-xs text-gray-500">恢复后排在未使用备用账户之后，仍遵守邮箱冷却。</span></label>
        </fieldset>
        <div class="space-y-3 rounded-xl bg-gray-50 p-4 dark:bg-dark-800" role="status">
          <div class="flex flex-wrap justify-between gap-2"><strong class="text-sm">分组容量预览</strong><span class="text-xs text-gray-500">已保存策略 · 不含降级组流量</span></div>
          <div v-for="id in policyGroups(policy)" :key="id" class="space-y-2 border-t border-gray-200 pt-3 dark:border-dark-600">
            <div class="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm"><span class="font-medium">{{ groupName(id) }}</span><span v-if="policy.demand_mode === 'dynamic'">本组请求 <strong>{{ runtime?.groups?.[id]?.current_demand ?? '—' }}</strong> · 本组窗口需求 <strong>{{ runtime?.groups?.[id]?.demand_concurrency ?? '—' }}</strong></span><span>目标容量 <strong>{{ target(policy, demand(id)) }}</strong></span><span>估算 {{ preview(policy, id).count }} 个账户 / {{ preview(policy, id).capacity }} 并发</span></div>
            <p v-if="runtime?.groups?.[id]" class="text-xs text-indigo-600 dark:text-indigo-300">{{ stateSummary(id) }} · 已就绪 {{ runtime.groups[id]?.ready_capacity }} / {{ runtime.groups[id]?.target_capacity }}</p>
            <p v-if="runtime?.groups?.[id]?.demand_unavailable" class="text-xs text-amber-600">实时负载读取失败，保持上次目标容量，不按零需求缩容。</p>
            <p v-if="preview(policy, id).capacity < target(policy, demand(id))" class="text-xs text-amber-600">容量缺口 {{ target(policy, demand(id)) - preview(policy, id).capacity }} 并发，等待可用账户。</p>
          </div>
          <p class="text-xs leading-5 text-gray-500">请求按实际使用的分组计数，降级组请求不计入本组需求。容量估算使用已加载账户配置；完整状态请查看“活跃池驾驶舱”。修改配置后请保存。</p>
        </div>
      </div>
    </article>
    <p class="text-xs leading-5 text-gray-500">缩容按本次绑定到期退出：例如绑定 170 秒，已运行 120 秒则继续剩余 50 秒，期间接收请求但不提前轮换。需求回升可撤销退出；到期停止接新请求，在途请求继续完成。未绑定的多余准备账户直接取消。同一邮箱不重复分配容量，多组账户由靠前的分组管理。</p>
    <BaseDialog :show="pickerIndex !== null" title="选择 Host 轮换分组" width="normal" @close="pickerIndex = null">
      <div class="space-y-3"><p class="text-sm text-gray-500">勾选多个分组，应用后共用当前策略配置。已用于其他策略的分组不可重复选择。</p><input v-model="search" type="search" class="input w-full" placeholder="搜索分组名称或编号" aria-label="搜索策略分组" /><div class="max-h-80 space-y-1 overflow-y-auto"><label v-for="group in pickerGroups" :key="group.id" class="flex items-center gap-3 rounded-lg px-3 py-3 text-sm" :class="groupTaken(group.id) ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700'"><input v-model="pickerDraft" type="checkbox" :value="group.id" :disabled="groupTaken(group.id)" class="h-4 w-4" /><span>{{ group.name }} (#{{ group.id }})</span><span v-if="groupTaken(group.id)" class="ml-auto text-xs text-gray-400">已有策略</span></label><p v-if="!pickerGroups.length" class="p-3 text-sm text-gray-500">无匹配分组，请先在上方选择 Host 轮换分组。</p></div></div>
      <template #footer><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" @click="pickerIndex = null">取消</button><button type="button" class="btn btn-primary" :disabled="!pickerDraft.length" @click="applyPicker">应用（{{ pickerDraft.length }}）</button></div></template>
    </BaseDialog>
    <BaseDialog :show="removeIndex !== null" title="移除账户池规则" width="normal" @close="removeIndex = null">
      <p class="text-sm leading-6 text-gray-600 dark:text-gray-300">保存后，{{ removalGroups }} 将恢复原有的全量 Host 轮换，不再受本规则的活跃账户数量限制。如果希望停止调度，请使用“暂停”开关。</p>
      <template #footer><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" @click="removeIndex = null">保留规则</button><button type="button" class="btn bg-red-600 text-white hover:bg-red-700" @click="removePool">移除规则</button></div></template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { AccountListItem, AdminGroup } from '@/types'
import type { CookieRotationPoolPolicy, CookieRotationPoolSnapshot } from '@/api/admin/cookies'
import { cookiePoolGroupIDs, cookiePoolPreview, cookiePoolTarget, defaultCookieRotationPool } from '@/views/admin/cookieRotationPools'

const props = defineProps<{ modelValue: CookieRotationPoolPolicy[]; selectedGroups: number[]; groups: AdminGroup[]; accounts: AccountListItem[]; runtime?: CookieRotationPoolSnapshot }>()
const emit = defineEmits<{ 'update:modelValue': [CookieRotationPoolPolicy[]] }>()
const modes = [{ value: 'dynamic', label: '实时分配' }, { value: 'fixed', label: '固定目标' }] as const
const redundancyModes = [{ value: 'percent', label: '按比例' }, { value: 'fixed', label: '固定并发' }] as const
const pickerIndex = ref<number | null>(null)
const removeIndex = ref<number | null>(null)
const pickerDraft = ref<number[]>([])
const search = ref('')
const policyGroups = cookiePoolGroupIDs
const groupName = (id: number) => props.groups.find(g => g.id === id)?.name || `分组 #${id}`
const availableGroups = computed(() => props.groups.filter(g => props.selectedGroups.includes(g.id) && !props.modelValue.some(p => policyGroups(p).includes(g.id))))
const pickerGroups = computed(() => props.groups.filter(g => props.selectedGroups.includes(g.id) && `${g.name} ${g.id}`.toLowerCase().includes(search.value.toLowerCase())))
const groupTaken = (id: number) => props.modelValue.some((p, i) => i !== pickerIndex.value && policyGroups(p).includes(id))
const removalGroups = computed(() => removeIndex.value === null ? '' : policyGroups(props.modelValue[removeIndex.value]!).map(groupName).join('、'))
const target = cookiePoolTarget
const demand = (id: number) => props.runtime?.groups?.[id]?.demand_concurrency || 0
const preview = (p: CookieRotationPoolPolicy, id: number) => cookiePoolPreview(p, props.accounts, demand(id), id)
function openPicker(index: number) { pickerIndex.value = index; search.value = ''; pickerDraft.value = policyGroups(props.modelValue[index]!).filter(id => props.selectedGroups.includes(id)) }
function applyPicker() {
  if (pickerIndex.value === null) return
  const ids = [...new Set(pickerDraft.value)].filter(id => props.selectedGroups.includes(id) && !groupTaken(id))
  if (!ids.length) return
  emit('update:modelValue', props.modelValue.map((p, i) => i === pickerIndex.value ? { ...p, group_id: ids[0]!, group_ids: ids } : p))
  pickerIndex.value = null
}
function setLimit(p: CookieRotationPoolPolicy, event: Event) { p.max_accounts = (event.target as HTMLSelectElement).value === 'unlimited' ? 0 : Math.max(p.min_accounts, 10) }
function addPool() { const group = availableGroups.value[0]; if (group) emit('update:modelValue', [...props.modelValue, defaultCookieRotationPool(group.id)]) }
function removePool() { emit('update:modelValue', props.modelValue.filter((_, i) => i !== removeIndex.value)); removeIndex.value = null }
function stateSummary(id: number) {
  const members = Object.values(props.runtime?.groups?.[id]?.members || {})
  return `活跃 ${members.filter(m => m.state === 'active' || m.state === 'draining').length} · 到期退出 ${members.filter(m => m.state === 'draining').length} · 准备中 ${members.filter(m => m.state === 'preparing').length} · Host 耗尽 ${members.filter(m => m.state === 'exhausted').length}`
}
</script>
