<template>
  <section class="space-y-3 rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800" aria-labelledby="country-filter-title">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h4 id="country-filter-title" class="text-sm font-semibold">{{ text('国家／地区过滤', 'Country / region filter') }}</h4>
      <span class="text-xs text-gray-500">{{ text('预计可用', 'Eligible') }} {{ eligible }} / {{ nodes.length }}</span>
    </div>
    <p class="text-xs text-gray-500">{{ text('按最近的出口 IP 检测结果筛选，不依据节点名称。动态出口改变地区后需要重新检测。', 'Uses the last exit-IP country check, not node names. Recheck when a dynamic exit changes region.') }}</p>
    <div class="flex flex-wrap items-center gap-2">
      <label class="sr-only" for="country-filter-mode">{{ text('过滤模式', 'Filter mode') }}</label>
      <select id="country-filter-mode" v-model="draft.mode" class="input w-auto" :disabled="busy" @change="dirty = true">
        <option value="off">{{ text('不限制地区', 'No region filter') }}</option>
        <option value="exclude">{{ text('排除所选地区', 'Exclude selected regions') }}</option>
        <option value="include">{{ text('仅允许所选地区', 'Allow selected regions only') }}</option>
      </select>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="excludeHongKong">{{ text('快捷：排除香港', 'Exclude Hong Kong') }}</button>
    </div>
    <template v-if="draft.mode !== 'off'">
      <div class="flex flex-wrap gap-1.5" aria-live="polite">
        <button v-for="code in draft.codes" :key="code" type="button" class="rounded-full bg-primary-100 px-2.5 py-1 text-xs text-primary-800 dark:bg-primary-900 dark:text-primary-200" :disabled="busy" :aria-label="text('移除 ', 'Remove ') + label(code)" @click="toggle(code)">{{ label(code) }} ×</button>
        <span v-if="!draft.codes.length" class="text-xs text-amber-700">{{ text('请至少选择一个国家或地区', 'Select at least one country or region') }}</span>
      </div>
      <input v-model="search" type="search" class="input w-full" :placeholder="text('搜索地区名称或代码，例如 香港、Hong Kong、HK', 'Search a region or code, e.g. Hong Kong, HK')" :aria-label="text('搜索国家地区', 'Search countries and regions')" />
      <div class="flex max-h-32 flex-wrap gap-1.5 overflow-y-auto" :aria-label="text('地区选项', 'Region options')">
        <button v-for="code in options" :key="code" type="button" class="rounded-md border px-2 py-1 text-xs" :class="draft.codes.includes(code) ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900 dark:text-primary-200' : 'border-gray-200 dark:border-dark-600'" :disabled="busy" :aria-pressed="draft.codes.includes(code)" :data-country="code" @click="toggle(code)">{{ label(code) }}</button>
      </div>
      <label class="flex items-start gap-2 text-xs"><input v-model="draft.allow_unknown" type="checkbox" class="mt-0.5" :disabled="busy" @change="dirty = true" />{{ text('允许未知地区节点（尚未检测或检测失败）', 'Allow unknown regions (unchecked or failed checks)') }}</label>
    </template>
    <p v-if="eligible === 0 && nodes.length" class="text-xs text-amber-700 dark:text-amber-400" role="status">{{ text('应用后没有可参与打票的节点。可先检测地区或调整筛选；不会自动回退到被排除地区。', 'No eligible harvest nodes after applying. Check regions or adjust the filter; excluded regions are never a fallback.') }}</p>
    <div class="flex flex-wrap gap-2">
      <button type="button" class="btn btn-primary btn-sm" :disabled="busy || (draft.mode !== 'off' && !draft.codes.length)" @click="save">{{ text('保存地区规则', 'Save region rules') }}</button>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !nodes.length" @click="$emit('scan')">{{ text('检测出口地区', 'Check exit regions') }}</button>
    </div>
    <p class="text-xs text-gray-500">{{ text('检测通过节点访问 country.is 查询出口地区，每次至多 20 个最久未检测的节点，不调用模型。地区规则与手动停用、用后移出同时生效。', 'Checks exit regions via country.is through up to 20 least-recently checked nodes, without model calls. Region rules also apply to disabled and retired nodes.') }}</p>
  </section>
</template>
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CountryFilter, CountryNode } from './mihomoCountry'
const props = defineProps<{ filter?: CountryFilter; codes: string[]; nodes: CountryNode[]; busy: boolean }>()
const emit = defineEmits<{ save: [filter: CountryFilter]; scan: [] }>()
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const draft = reactive<CountryFilter>({ mode: 'off', codes: [], allow_unknown: false })
const dirty = ref(false); const search = ref('')
watch(() => props.filter, value => {
  const next = value || { mode: 'off' as const, codes: [], allow_unknown: false }
  const equal = draft.mode === next.mode && draft.allow_unknown === next.allow_unknown && [...draft.codes].sort().join(',') === [...(next.codes || [])].sort().join(',')
  if (!dirty.value || equal) { Object.assign(draft, next, { codes: [...(next.codes || [])] }); dirty.value = false }
}, { immediate: true, deep: true })
const names = computed(() => new Intl.DisplayNames([locale.value.startsWith('zh') ? 'zh' : 'en'], { type: 'region' }))
const englishNames = new Intl.DisplayNames(['en'], { type: 'region' })
const label = (code: string) => `${code === 'HK' ? text('香港', 'Hong Kong') : names.value.of(code) || code} ${code}`
const common = ['HK', 'CN', 'MO', 'US', 'JP', 'SG', 'TW', 'KR', 'GB', 'DE', 'CA', 'AU']
const options = computed(() => {
  const codes = [...new Set([...common.filter(code => props.codes.includes(code)), ...props.codes])]
  const needle = search.value.trim().toLowerCase()
  return codes.filter(code => !needle || `${label(code)} ${englishNames.of(code) || ''}`.toLowerCase().includes(needle))
})
function toggle(code: string) { dirty.value = true; draft.codes = draft.codes.includes(code) ? draft.codes.filter(c => c !== code) : [...draft.codes, code] }
function excludeHongKong() { dirty.value = true; draft.codes = draft.mode === 'exclude' ? [...new Set([...draft.codes, 'HK'])] : ['HK']; draft.mode = 'exclude' }
function save() { emit('save', { ...draft, codes: [...draft.codes] }) }
const eligible = computed(() => props.nodes.filter(node => {
  if (node.state !== 'enabled' && node.state !== 'country_excluded') return false
  if (draft.mode === 'off') return true
  if (!node.country_code) return draft.allow_unknown
  const selected = draft.codes.includes(node.country_code)
  return draft.mode === 'include' ? selected : !selected
}).length)
</script>
