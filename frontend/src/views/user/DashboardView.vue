<template>
  <AppLayout>
    <div class="mx-auto max-w-[1480px] space-y-6">
      <section class="rounded-2xl border border-[#dce6d6] bg-[#edf3e7] p-5 dark:border-[#344b32] dark:bg-[#203322] sm:p-7">
        <div class="flex flex-wrap items-end justify-between gap-5">
          <div class="min-w-0"><p class="text-[10px] font-semibold uppercase tracking-[.2em] text-[#527249] dark:text-[#acc69b]">YOUR WORKSPACE / {{ siteName }}</p><h1 class="mt-3 break-words text-2xl font-semibold tracking-tight text-[#243e25] dark:text-[#dfebd5] sm:text-3xl">你好，{{ displayName }}。</h1><p class="mt-2 text-sm leading-6 text-[#607454] dark:text-[#b0c4a2]">从一次调用，到下一个作品。你的密钥、用量和费用，都在这里。</p></div>
          <div class="flex gap-2"><button type="button" :disabled="refreshing" class="btn btn-secondary" @click="refreshAll"><Icon name="refresh" size="sm" :class="{ 'animate-spin': refreshing }" /><span>{{ refreshing ? '正在更新' : '刷新数据' }}</span></button><router-link to="/keys" class="btn bg-[#2c5239] text-white hover:bg-[#3b6849]"><Icon name="key" size="sm" />管理密钥</router-link></div>
        </div>
        <div class="mt-6 flex flex-wrap items-center justify-between gap-3 border-t border-[#d7e1cf] pt-4 dark:border-[#3d5136]">
          <div class="flex min-w-0 items-center gap-2 text-xs text-[#5e7350] dark:text-[#b7cbaa]"><Icon name="link" size="sm" class="shrink-0" /><span class="shrink-0">OpenAI Base URL</span><code class="break-all text-[#324c31] dark:text-[#e0ebd4]">{{ endpoint || '待配置' }}</code></div>
          <button type="button" :disabled="!endpoint" class="inline-flex items-center gap-1.5 rounded-md border border-[#c9d7be] px-2.5 py-1.5 text-xs text-[#3d5c36] hover:bg-white/50 dark:border-[#587047] dark:text-[#c8dfb5]" @click="copyEndpoint"><Icon :name="copied ? 'check' : 'copy'" size="sm" />{{ copied ? '已复制' : '复制地址' }}</button>
        </div>
        <p v-if="copyError" class="mt-2 text-xs text-red-700 dark:text-red-300" role="status">{{ copyError }}</p>
      </section>

      <div v-if="errors.length" class="rounded-xl border border-amber-200 bg-amber-50 px-5 py-4 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200" role="alert">
        <div class="flex items-center gap-2 text-sm font-semibold"><Icon name="exclamationCircle" size="sm" />部分数据未能更新</div>
        <p class="mt-2 text-xs leading-6">{{ errors.join('；') }}。已有数据保留上次结果，可点击「刷新数据」重试。</p>
      </div>

      <div class="flex items-center justify-between gap-3"><h2 class="text-base font-semibold text-gray-900 dark:text-white">账户概览</h2><span class="text-xs text-gray-500 dark:text-dark-400">{{ updatedAt ? '统计更新于 ' + updatedAt : '数据来自实际调用记录' }}</span></div>
      <div v-if="loading && !stats" class="card flex items-center justify-center gap-3 py-12 text-sm text-gray-500"><LoadingSpinner />正在加载账户数据…</div>
      <UserDashboardStats v-if="stats" :stats="stats" :balance="user?.balance || 0" :is-simple="authStore.isSimpleMode" :platform-quotas="platformQuotas" />
      <div v-if="stats && stats.total_api_keys === 0" class="card flex flex-wrap items-center justify-between gap-4 p-5"><div><h3 class="font-semibold text-gray-900 dark:text-white">创建第一把 API 密钥，开始接入</h3><p class="mt-1 text-sm text-gray-500 dark:text-dark-400">选择可用分组后，在密钥的使用说明中查看对应客户端配置。</p></div><router-link to="/keys" class="btn btn-primary">创建密钥<Icon name="arrowRight" size="sm" /></router-link></div>

      <div class="grid items-start gap-6 xl:grid-cols-[1.05fr_.95fr]"><UserDashboardQuickActions /><RelayQuickStart /></div>

      <section class="space-y-4">
        <div><h2 class="text-base font-semibold text-gray-900 dark:text-white">调用与费用趋势</h2><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">按日期查看真实用量；标准费用与实际扣除金额分别展示。</p></div>
        <UserDashboardCharts v-model:startDate="startDate" v-model:endDate="endDate" v-model:granularity="granularity" :loading="loadingCharts" :trend="trendData" :models="modelStats" @dateRangeChange="loadCharts" @granularityChange="loadCharts" @refresh="refreshAll" />
      </section>
      <UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" :error="failures.usage" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import { getMyPlatformQuotas } from '@/api/user'
import { formatDateLocalInput } from '@/utils/format'
import { useRelayBrand } from '@/composables/useRelayBrand'
import { useRelayEndpoint } from '@/composables/useRelayEndpoint'
import { useClipboard } from '@/composables/useClipboard'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import RelayQuickStart from '@/components/relay/RelayQuickStart.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'
import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem } from '@/types'

const authStore = useAuthStore()
const user = computed(() => authStore.user)
const { siteName } = useRelayBrand()
const { endpoint } = useRelayEndpoint()
const { copied, copyToClipboard } = useClipboard()
const displayName = computed(() => user.value?.username || user.value?.email?.split('@')[0] || '开发者')
const copyError = ref('')
async function copyEndpoint() {
  copyError.value = ''
  try {
    if (!await copyToClipboard(endpoint.value, 'API 地址已复制')) copyError.value = '复制失败，请选中地址手动复制。'
  } catch {
    copyError.value = '浏览器未允许复制，请选中地址手动复制。'
  }
}
const stats = ref<UserStatsType | null>(null)
const loading = ref(false)
const loadingUsage = ref(false)
const loadingCharts = ref(false)
const refreshing = ref(false)
const updatedAt = ref('')
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)
const failures = reactive({ stats: '', charts: '', usage: '', quotas: '', profile: '' })
const errors = computed(() => Object.values(failures).filter(Boolean))
const lastWeek = () => formatDateLocalInput(new Date(Date.now() - 6 * 86400000))
const today = () => formatDateLocalInput(new Date())
const startDate = ref(lastWeek())
const endDate = ref(today())
const granularity = ref('day')
let chartsRequest = 0

async function loadStats() {
  loading.value = true
  try {
    stats.value = await usageAPI.getDashboardStats()
    updatedAt.value = new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
    failures.stats = ''
  } catch {
    failures.stats = '账户统计加载失败'
  } finally {
    loading.value = false
  }
}
async function loadProfile() {
  try { await authStore.refreshUser(); failures.profile = '' }
  catch { failures.profile = '余额与账户信息更新失败' }
}
async function loadCharts() {
  const request = ++chartsRequest
  if (!startDate.value || !endDate.value || startDate.value > endDate.value) {
    loadingCharts.value = false
    failures.charts = '请选择有效的起止日期'
    return
  }
  loadingCharts.value = true
  try {
    const [trend, models] = await Promise.all([
      usageAPI.getDashboardTrend({ start_date: startDate.value, end_date: endDate.value, granularity: granularity.value === 'hour' ? 'hour' : 'day' }),
      usageAPI.getDashboardModels({ start_date: startDate.value, end_date: endDate.value })
    ])
    if (request !== chartsRequest) return
    trendData.value = trend.trend || []
    modelStats.value = models.models || []
    failures.charts = ''
  } catch {
    if (request === chartsRequest) failures.charts = '趋势与模型分布加载失败'
  } finally {
    if (request === chartsRequest) loadingCharts.value = false
  }
}
async function loadRecent() {
  loadingUsage.value = true
  try {
    const result = await usageAPI.getByDateRange(lastWeek(), today())
    recentUsage.value = result.items.slice(0, 5)
    failures.usage = ''
  } catch {
    failures.usage = '最近调用记录加载失败'
  } finally { loadingUsage.value = false }
}
async function loadPlatformQuotas() {
  try {
    const result = await getMyPlatformQuotas()
    platformQuotas.value = result.platform_quotas ?? []
    failures.quotas = ''
  } catch { failures.quotas = '平台额度加载失败' }
}
async function refreshAll() {
  if (refreshing.value) return
  refreshing.value = true
  try { await Promise.allSettled([loadStats(), loadProfile(), loadCharts(), loadRecent(), loadPlatformQuotas()]) }
  finally { refreshing.value = false }
}
onMounted(() => { void refreshAll() })
</script>

