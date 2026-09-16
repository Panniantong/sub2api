<template>
  <AppLayout>
    <AccountManagementTabs :active="mode" />
    <div class="space-y-5">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div><h1 class="text-xl font-semibold text-gray-900 dark:text-gray-100">{{ titles[mode] }}</h1><p class="mt-2 text-sm text-gray-500">{{ mode === 'settings' ? '按测试类型管理题目、答案判定和可见范围。' : '分别查看执行状态、答案与格式；单次测试不足以判断模型能力下降。' }}</p></div>
        <button class="btn btn-secondary" :disabled="loading || metadataLoading" @click="refresh"><Icon name="refresh" size="sm" :class="loading || metadataLoading ? 'animate-spin' : ''" /> 刷新</button>
      </div>
      <div v-if="error" role="alert" class="flex items-center justify-between gap-3 rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300"><span>{{ error }}</span><button class="shrink-0 underline" @click="load(false)">重试</button></div>
      <div v-if="metadataError" role="alert" class="rounded-xl bg-amber-50 p-4 text-sm text-amber-800">{{ metadataError }} <button class="ml-3 underline" @click="loadMetadata">重新加载测试设置与分组</button></div>
      <template v-if="mode !== 'settings'">
        <div v-if="mode === 'tests'" class="grid grid-cols-2 gap-3 lg:grid-cols-5">
          <div v-for="stat in stats" :key="stat.label" class="rounded-xl border border-gray-200 bg-white px-4 py-4 dark:border-dark-700 dark:bg-dark-800"><p class="text-xs text-gray-500">{{ stat.label }}</p><p class="mt-2 text-2xl font-semibold tabular-nums" :class="stat.warning ? 'text-amber-700 dark:text-amber-300' : 'text-gray-900 dark:text-gray-100'">{{ stat.value }}</p></div>
        </div>
        <form class="rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800" @submit.prevent="applyFilters">
          <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <label class="text-xs text-gray-500">{{ mode === 'history' ? '账号编号' : '搜索账号' }}<input v-model.trim="filters.search" class="input mt-1.5" :placeholder="mode === 'history' ? '输入账号 ID' : '名称、备注或账号 ID'" /></label>
            <template v-if="mode === 'tests'">
              <label class="text-xs text-gray-500">账号类型<select v-model="filters.type" class="input mt-1.5"><option value="">全部类型</option><option value="oauth">OAuth</option><option value="apikey">API Key</option><option value="setup-token">Setup Token</option><option value="upstream">Upstream</option></select></label>
              <label class="text-xs text-gray-500">账号池 / 分组<select v-model="filters.group_id" class="input mt-1.5"><option value="">全部账号池</option><option v-for="group in groups" :key="group.id" :value="String(group.id)">{{ group.name }}</option></select></label>
              <label class="text-xs text-gray-500">账号状态<select v-model="filters.account_status" class="input mt-1.5"><option value="">全部状态</option><option value="active">正常</option><option value="error">异常</option><option value="disabled">停用</option></select></label>
            </template>
            <label class="text-xs text-gray-500">测试类型<select v-model="filters.test_type" class="input mt-1.5"><option value="">全部测试</option><option v-for="setting in settings" :key="setting.test_type" :value="setting.test_type">{{ setting.name || testName(setting.test_type) }}</option></select></label>
            <label class="text-xs text-gray-500">测试结果<select v-model="filters.status" class="input mt-1.5"><option value="">全部结果</option><option v-for="(label, value) in statusLabels" :key="value" :value="value">{{ label }}</option></select></label>
            <label v-if="mode === 'tests'" class="text-xs text-gray-500">账号保护<select v-model="filters.anti_degradation" class="input mt-1.5"><option value="">全部</option><option value="true">已开启</option><option value="false">已关闭</option></select></label>
            <template v-else>
              <label class="text-xs text-gray-500">开始时间<input v-model="filters.from" type="datetime-local" class="input mt-1.5" /></label>
              <label class="text-xs text-gray-500">结束时间<input v-model="filters.to" type="datetime-local" class="input mt-1.5" /></label>
            </template>
          </div>
          <div class="mt-4 flex flex-wrap items-center justify-between gap-3">
            <label v-if="mode === 'tests'" class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300"><input v-model="filters.only_abnormal" type="checkbox" class="rounded text-primary-600" @change="applyFilters" />仅查看异常账号</label>
            <span v-else class="text-xs text-gray-500">历史记录按次保存，不覆盖以前的测试结果。</span>
            <div class="flex gap-2"><button type="button" class="btn btn-secondary" @click="resetFilters">清除筛选</button><button class="btn btn-primary" :disabled="loading">应用筛选</button></div>
          </div>
        </form>
        <template v-if="mode === 'tests'">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <label class="flex items-center gap-2 text-sm text-gray-500"><input type="checkbox" class="rounded text-primary-600" :checked="allPageSelected" :disabled="!accounts.length" @change="selectPage" />选择本页账号 <span class="text-xs">已选 {{ selected.length }}</span></label>
            <div class="flex flex-wrap gap-2"><button v-for="setting in settings" :key="setting.test_type" class="btn btn-secondary" :disabled="running || !selected.length || !setting.enabled" @click="run(selected, [setting.test_type])">批量{{ setting.name || testName(setting.test_type) }}</button><button class="btn btn-primary" :disabled="running || !selected.length || !enabledTypes.length" @click="run(selected, enabledTypes)">{{ running ? '正在提交…' : '运行全部测试' }}</button></div>
          </div>
          <p class="text-xs text-gray-500">每次最多选择 50 个账号，测试会产生上游用量。任务提交后可离开页面，刷新后继续查看进度。</p>
          <p v-if="loading && !accounts.length" class="py-12 text-center text-sm text-gray-500">正在加载账号…</p>
          <div v-else-if="!accounts.length" class="rounded-2xl border border-dashed border-gray-300 py-16 text-center dark:border-dark-700"><p class="text-sm text-gray-500">没有符合条件的账号</p><RouterLink to="/admin/accounts" class="mt-3 inline-block text-sm text-primary-600">前往账号列表</RouterLink></div>
          <div v-else class="grid gap-5 xl:grid-cols-2">
            <section v-for="account in accounts" :key="account.account_id" class="space-y-3">
              <label class="flex items-center gap-2 text-xs text-gray-500"><input v-model="selected" type="checkbox" :value="account.account_id" class="rounded text-primary-600" />{{ account.platform }} · #{{ account.account_id }} · {{ account.name }}</label>
              <div class="grid gap-4 sm:grid-cols-2"><TestResultCard v-for="summary in account.tests" :key="summary.test_type" :account="account" :summary="summary" :now="now" :disabled="running || !enabledTypes.includes(summary.test_type)" @detail="detailId = $event" @history="showHistory" @run="(id, type) => run([id], [type])" @cancel="cancel" /></div>
            </section>
          </div>
        </template>
        <TestHistoryTable v-else :records="records" @detail="detailId = $event" />
        <Pagination v-if="total" :page="page" :page-size="pageSize" :total="total" @update:page="changePage" @update:page-size="changePageSize" />
      </template>
      <TestSettingsPanel v-else-if="settings.length" :settings="settings" @saved="settingSaved" />
      <p v-else-if="loading" class="py-12 text-center text-sm text-gray-500">正在加载设置…</p>
    </div>
    <TestDetailDialog :record-id="detailId" :running="running" @close="detailId = null" @history="showHistory" @run="(id, type) => run([id], [type])" @updated="load(false)" />
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import Pagination from '@/components/common/Pagination.vue'
import AccountManagementTabs from '@/components/admin/intelligent-tests/AccountManagementTabs.vue'
import TestResultCard from '@/components/admin/intelligent-tests/TestResultCard.vue'
import TestDetailDialog from '@/components/admin/intelligent-tests/TestDetailDialog.vue'
import TestSettingsPanel from '@/components/admin/intelligent-tests/TestSettingsPanel.vue'
import TestHistoryTable from '@/components/admin/intelligent-tests/TestHistoryTable.vue'
import { statusLabels, testName, testSubmissionMessage } from '@/components/admin/intelligent-tests/display'
import { intelligentTestsAPI, newTestRequestKey, type TestAccount, type TestRecord, type TestSetting, type TestOverview, type TestFilters } from '@/api/intelligentTests'
import { getAllIncludingInactive } from '@/api/admin/groups'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
const props = defineProps<{ mode: 'tests' | 'history' | 'settings' }>()
const titles = { tests: '智能测试中心', history: '测试记录', settings: '测试设置' }
const app = useAppStore(), router = useRouter(), route = useRoute()
const accounts = ref<TestAccount[]>([]), records = ref<TestRecord[]>([]), settings = ref<TestSetting[]>([])
const groups = ref<{ id: number; name: string }[]>([]), selected = ref<number[]>([])
const loading = ref(false), running = ref(false), error = ref(''), detailId = ref<number | null>(null)
const metadataError = ref(''), metadataLoading = ref(false)
const page = ref(1), pageSize = ref(12), total = ref(0), now = ref(Date.now())
const overview = ref<TestOverview>({ total_accounts: 0, tested_today: 0, success_accounts: 0, abnormal_accounts: 0, suspected_degradation: 0 })
const defaults = { search: '', type: '', group_id: '', account_status: '', test_type: '', status: '', anti_degradation: '', only_abnormal: false, from: '', to: '' }
const filters = reactive({ ...defaults })
let applied: TestFilters = {}, controller: AbortController | undefined, version = 0, inFlight = false, metadataVersion = 0
let pollTimer: ReturnType<typeof setInterval>, clockTimer: ReturnType<typeof setInterval>, runKey = '', runSignature = ''
const enabledTypes = computed(() => settings.value.filter(item => item.enabled).map(item => item.test_type))
const allPageSelected = computed(() => accounts.value.length > 0 && accounts.value.every(item => selected.value.includes(item.account_id)))
const stats = computed(() => [
  { label: '账号总数', value: overview.value.total_accounts }, { label: '今日已测试', value: overview.value.tested_today },
  { label: '答案通过', value: overview.value.success_accounts }, { label: '执行异常', value: overview.value.abnormal_accounts, warning: true },
  { label: '答案待复核', value: overview.value.review_accounts ?? overview.value.suspected_degradation, warning: true }
])
async function load(quiet = false) {
  if (quiet && inFlight) return
  controller?.abort(); controller = new AbortController()
  inFlight = true
  const current = ++version, signal = controller.signal
  if (!quiet) loading.value = true
  try {
    if (props.mode === 'settings') {
      await loadMetadata()
    } else if (props.mode === 'history') {
      const data = await intelligentTestsAPI.records({ ...applied, page: page.value, page_size: pageSize.value }, signal)
      if (current === version) { records.value = data.items; total.value = data.total }
    } else {
      const data = await intelligentTestsAPI.accounts({ ...applied, page: page.value, page_size: pageSize.value }, signal)
      if (current === version) { accounts.value = data.items; total.value = data.total; overview.value = data.overview }
    }
    if (current === version) error.value = ''
  } catch (err) { if (current === version && !signal.aborted) error.value = extractApiErrorMessage(err, '加载失败，请重试') }
  finally { if (current === version) { loading.value = false; inFlight = false } }
}
async function loadMetadata() {
  const current = ++metadataVersion
  metadataLoading.value = true
  const results = await Promise.allSettled([intelligentTestsAPI.settings(), getAllIncludingInactive()])
  if (current !== metadataVersion) return
  const messages: string[] = []
  if (results[0].status === 'fulfilled') settings.value = results[0].value
  else messages.push(extractApiErrorMessage(results[0].reason, '无法加载测试类型'))
  if (results[1].status === 'fulfilled') groups.value = results[1].value
  else messages.push(extractApiErrorMessage(results[1].reason, '无法加载账号池筛选'))
  metadataError.value = messages.join('；'); metadataLoading.value = false
}
async function refresh() { await Promise.all([load(false), ...(props.mode === 'settings' ? [] : [loadMetadata()])]) }
async function cancel(id: number) {
  try { await intelligentTestsAPI.cancel(id); app.showSuccess('已取消排队任务'); await load(false) }
  catch (err) { app.showError(extractApiErrorMessage(err, '取消失败，任务可能已开始执行')) }
}
function applyFilters() {
  const next: TestFilters = {}
  for (const [key, value] of Object.entries(filters)) { if (value !== '' && value !== false) next[key] = value }
  if (props.mode === 'history') {
    if (filters.search && !/^[1-9]\d*$/.test(filters.search)) { error.value = '请输入有效的账号编号'; return }
    delete next.search; next.account_id = filters.search || undefined
    for (const key of ['from', 'to'] as const) if (filters[key]) next[key] = new Date(filters[key]).toISOString()
    if (filters.from && filters.to && filters.from > filters.to) { error.value = '结束时间不能早于开始时间'; return }
  }
  applied = next; page.value = 1; selected.value = []; void load()
}
function resetFilters() { Object.assign(filters, defaults); applyFilters() }
function changePage(value: number) { page.value = value; selected.value = []; void load() }
function changePageSize(value: number) { pageSize.value = value; changePage(1) }
function selectPage() { selected.value = allPageSelected.value ? [] : accounts.value.map(item => item.account_id) }
function settingSaved(setting: TestSetting) { const index = settings.value.findIndex(item => item.test_type === setting.test_type); if (index >= 0) settings.value[index] = setting }
function showHistory(id: number, type: string) { detailId.value = null; void router.push({ path: '/admin/accounts/test-history', query: { account_id: id, test_type: type } }) }
async function run(ids: number[], types: string[]) {
  if (running.value || !ids.length || !types.length) return
  if (ids.length > 50) { app.showError('每批最多选择 50 个账号'); return }
  const signature = JSON.stringify([[...ids].sort((a, b) => a - b), [...types].sort()])
  if (runSignature !== signature) { runKey = newTestRequestKey(); runSignature = signature }
  running.value = true
  try { const data = await intelligentTestsAPI.run(ids, types, runKey); app.showSuccess(testSubmissionMessage(data)); runSignature = ''; await load(false) }
  catch (err) { app.showError(extractApiErrorMessage(err, '任务提交失败，可重试')) }
  finally { running.value = false }
}
function routeFilters() {
  Object.assign(filters, defaults)
  if (props.mode === 'history') { filters.search = String(route.query.account_id || ''); filters.test_type = String(route.query.test_type || '') }
  applyFilters()
}
watch(() => [props.mode, route.query.account_id, route.query.test_type], routeFilters)
onMounted(() => {
  routeFilters()
  if (props.mode !== 'settings') void loadMetadata()
})
onMounted(() => {
  pollTimer = setInterval(() => { if (props.mode !== 'settings' && !document.hidden && !loading.value && !running.value) void load(true) }, 5000)
  clockTimer = setInterval(() => { now.value = Date.now() }, 1000)
})
onUnmounted(() => { version++; metadataVersion++; controller?.abort(); clearInterval(pollTimer); clearInterval(clockTimer) })
</script>
